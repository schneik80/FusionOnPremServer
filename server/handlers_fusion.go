package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/internal/fusionact"
	"github.com/schneik80/fusionlocalserver/internal/fusionlink"
	"github.com/schneik80/fusionlocalserver/internal/fusionmcp"
	"github.com/schneik80/fusionlocalserver/notifications"
)

// Open and Insert drive the user's *running Fusion desktop client* through its
// local MCP server on 127.0.0.1. There are two ways to get there, and which one
// applies is decided here rather than by the client:
//
//   - "proxy": the request arrived from loopback, so the browser, this server
//     and Fusion are all the same machine. We make the MCP call ourselves,
//     synchronously, and answer with the outcome. Nothing to install.
//   - "launch": anything else. The browser is elsewhere on the network; only
//     the user's own machine can reach their Fusion. The SPA navigates to a
//     fusionlocal:// URL, the helper app performs the action and reports back.
//
// Getting this backwards would be a real bug, not a cosmetic one: proxying for
// a remote browser would drive the SERVER OPERATOR'S Fusion on someone else's
// behalf. So the loopback test is on the connection, never on a client hint.

// fusionMaxBody caps the action and callback request bodies.
const fusionMaxBody = 8 << 10

// fusionProxyTimeout bounds the same-machine MCP call. Fusion answers a
// document open in well under this; the point is to fail fast rather than hold
// the request while a wedged Fusion thinks about it.
const fusionProxyTimeout = 20 * time.Second

// fusionActionReq is the SPA's request to act on a document.
type fusionActionReq struct {
	HubID       string `json:"hubId"`
	DMProjectID string `json:"dmProjectId"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ItemID      string `json:"itemId"`
	Name        string `json:"name"`
	Action      string `json:"action"`
}

// FusionActionDTO tells the SPA how the action will be delivered. In proxy mode
// it is already done (ok/errorCode are final). In launch mode the SPA navigates
// to url and then polls the outcome by ticket.
type FusionActionDTO struct {
	Mode      string `json:"mode"` // "proxy" | "launch"
	Ticket    string `json:"ticket,omitempty"`
	URL       string `json:"url,omitempty"`
	OK        bool   `json:"ok,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

// FusionTicketDTO is what the helper collects when it redeems a ticket. It
// carries the least the helper needs: what to do, to which document, and the
// project whose hub it should verify Fusion is signed in to.
type FusionTicketDTO struct {
	Action      string `json:"action"`
	FileID      string `json:"fileId"`
	DMProjectID string `json:"dmProjectId"`
	DocName     string `json:"docName,omitempty"`
}

// FusionOutcomeDTO is the SPA's view of a launched action.
type FusionOutcomeDTO struct {
	Status    string `json:"status"` // "pending" | "ok" | "error"
	ErrorCode string `json:"errorCode,omitempty"`
}

// handleFusionAction mints a ticket for an Open/Insert, or performs the action
// directly when the caller is on this machine.
// POST /api/fusion/action
func (s *Server) handleFusionAction(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	var in fusionActionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, fusionMaxBody)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.HubID != "" && !hubMatches(w, set.hubID, in.HubID) {
		return
	}
	if !fusionlink.ValidAction(in.Action) {
		writeError(w, http.StatusBadRequest, "action must be open or insert")
		return
	}
	if in.ItemID == "" || in.DMProjectID == "" {
		writeError(w, http.StatusBadRequest, "dmProjectId and itemId are required")
		return
	}

	// Same machine: do it now and answer with the result. No ticket, no helper,
	// no notification — the SPA has the outcome in hand.
	if s.isLoopbackRequest(r) {
		ctx, cancel := context.WithTimeout(r.Context(), fusionProxyTimeout)
		defer cancel()
		code := fusionact.Perform(ctx, fusionmcp.NewClient(), in.Action, in.ItemID)
		if code != "" {
			s.logger.Info("fusion action failed (local)", "action", in.Action, "code", code)
			writeJSON(w, http.StatusOK, FusionActionDTO{Mode: "proxy", OK: false, ErrorCode: code})
			return
		}
		writeJSON(w, http.StatusOK, FusionActionDTO{Mode: "proxy", OK: true})
		return
	}

	id, err := randToken(24)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.fusionTickets.add(&fusionTicket{
		ID:          id,
		SessionID:   sess.ID,
		UserKey:     notifUserKey(chat.Identity{UserID: sess.Profile.Sub, Email: sess.Profile.Email}),
		Action:      in.Action,
		FileID:      in.ItemID,
		DMProjectID: in.DMProjectID,
		ProjectID:   in.ProjectID,
		ProjectName: in.ProjectName,
		DocName:     in.Name,
		HubID:       set.hubID,
		ExpiresAt:   time.Now().Add(fusionTicketTTL),
	})
	writeJSON(w, http.StatusOK, FusionActionDTO{
		Mode:   "launch",
		Ticket: id,
		URL:    fusionlink.BuildURL(in.Action, id, s.helperOrigin(r)),
	})
}

// handleFusionTicket hands a redeemed ticket's payload to the helper.
//
// Unauthenticated by design: the helper is a native app with no browser
// session. The ticket IS the credential — unguessable, single-use, and expiring
// in two minutes — so this endpoint is metered per IP like the other
// session-less routes and gives nothing away to a wrong guess.
// GET /api/fusion/ticket?ticket=<id>
func (s *Server) handleFusionTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := reqParam(w, r, "ticket")
	if !ok {
		return
	}
	t, ok := s.fusionTickets.redeem(id)
	if !ok {
		// One answer for unknown, expired and already-redeemed alike: a caller
		// probing ids learns nothing from the difference.
		writeError(w, http.StatusNotFound, "unknown or expired ticket")
		return
	}
	writeJSON(w, http.StatusOK, FusionTicketDTO{
		Action:      t.Action,
		FileID:      t.FileID,
		DMProjectID: t.DMProjectID,
		DocName:     t.DocName,
	})
}

// fusionCallbackReq is the helper's outcome report.
type fusionCallbackReq struct {
	Ticket string `json:"ticket"`
	OK     bool   `json:"ok"`
	Code   string `json:"code"`
}

// handleFusionCallback records what the helper managed to do. Also
// unauthenticated, and safe for the same reason: it can only report on a ticket
// that was already redeemed, exactly once, and the reported code must be one
// this build defines. A failure additionally lands in the requester's inbox so
// it isn't lost if they navigated away while the helper was working.
// POST /api/fusion/callback
func (s *Server) handleFusionCallback(w http.ResponseWriter, r *http.Request) {
	var in fusionCallbackReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, fusionMaxBody)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	code := in.Code
	if !in.OK && !fusionlink.ValidCode(code) {
		// Never store a string an unauthenticated caller chose: an unknown
		// code becomes the generic failure.
		code = fusionlink.CodeFailed
	}
	if in.OK {
		code = ""
	}
	t, ok := s.fusionTickets.report(in.Ticket, in.OK, code)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown ticket")
		return
	}
	if !in.OK {
		s.emitFusionFailure(t, code)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleFusionOutcome lets the SPA that launched an action learn how it went.
// Scoped to the originating session. A ticket that was never redeemed reads as
// "pending", which is also what "the helper isn't installed" looks like — the
// SPA distinguishes them by giving up after a few seconds.
// GET /api/fusion/outcome?ticket=<id>
func (s *Server) handleFusionOutcome(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	id, ok := reqParam(w, r, "ticket")
	if !ok {
		return
	}
	t, ok := s.fusionTickets.outcome(id, sess.ID)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown ticket")
		return
	}
	switch {
	case !t.Reported:
		writeJSON(w, http.StatusOK, FusionOutcomeDTO{Status: "pending"})
	case t.OK:
		writeJSON(w, http.StatusOK, FusionOutcomeDTO{Status: "ok"})
	default:
		writeJSON(w, http.StatusOK, FusionOutcomeDTO{Status: "error", ErrorCode: t.ErrCode})
	}
}

// emitFusionFailure drops a failed Open/Insert in the requester's inbox.
// Best-effort: the callback must still succeed if the inbox write doesn't.
func (s *Server) emitFusionFailure(t fusionTicket, code string) {
	// The hub's store set was built when the session locked to it, so this is
	// a lookup rather than a build; the name is only used on first creation.
	set, err := s.hubs.get(t.HubID, "")
	if err != nil || set == nil || set.notifications == nil || t.UserKey == "" {
		return
	}
	if _, _, err := set.notifications.Add(t.UserKey, notifications.Notification{
		Kind:        notifications.KindFusionFailed,
		HubID:       t.HubID,
		ProjectID:   t.ProjectID,
		ProjectName: t.ProjectName,
		Subject:     t.DocName,
		// The code rides in Ref rather than Subject: Subject is user data the
		// client renders verbatim, while this is an enum token it localizes.
		Ref: "fls:fusion?action=" + t.Action + "&code=" + code,
	}); err != nil {
		s.logger.Error("notifications: fusion failure emit failed", "ticket", t.ID, "err", err)
	}
}

// isLoopbackRequest reports whether the connection came from this machine.
//
// It reads RemoteAddr directly and deliberately ignores X-Forwarded-For: a
// forwarded header is a client assertion, and believing one here would let a
// remote browser claim the loopback fast path and drive the server operator's
// Fusion. A reverse proxy on the same host therefore makes every client look
// local — which is the safe direction to be wrong in only if the proxy is the
// only way in, so the check also requires that we are not behind a proxy at all.
func (s *Server) isLoopbackRequest(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// helperOrigin is the origin the helper redeems tickets against: the canonical
// public URL when the operator configured one, otherwise the host this request
// arrived on. It must be an absolute origin — the helper compares it against
// its pairing and refuses anything it doesn't recognize.
func (s *Server) helperOrigin(r *http.Request) string {
	if s.publicOrigin != "" {
		return s.publicOrigin
	}
	scheme := "http"
	if s.tlsEnabled || r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
