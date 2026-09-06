package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/schneik80/fusionlocalserver/api"

	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// priorityHeader lets the SPA say how urgent a request is. The route table
// below is the default; the header exists to DEMOTE work the SPA knows is
// prefetch (a dashboard roll-up behind another tab) and to PROMOTE a per-row
// call the user just clicked. It is a first-party hint behind the session
// cookie, honoured as given.
const priorityHeader = "X-FLS-Priority"

// Throttle state rides on every authenticated response so the SPA learns
// about slow mode from traffic it already makes, error responses included.
const (
	throttleHeader      = "X-FLS-Throttle"       // ok | slow | cooldown
	throttleUntilHeader = "X-FLS-Throttle-Until" // unix ms, only when not ok
)

// routePriority classifies routes the table knows; anything unlisted is P0,
// so a forgotten route is never demoted behind background work.
var routePriority = map[string]apsbudget.Priority{
	// Per-row probes for what is on screen. Everything else — including the
	// aggregates (hub overview, roll-up, descendants, the dashboard's
	// permissions path) — is P0 unless the SPA says otherwise in the header,
	// which it does for exactly the calls it knows are not what the user is
	// waiting on (api/queries.ts). A tab's own content (local-refs on Where
	// Used, the activity report) must never sit in the P1 lane's 10 s window.
	"/api/items/classify":        apsbudget.P1,
	"/api/items/thumbnail":       apsbudget.P1,
	"/api/items/thumbnail/image": apsbudget.P1,
	"/api/items/drawing/preview": apsbudget.P1,
}

// requestPriority reads the header, else the route table, else P0.
func requestPriority(r *http.Request) apsbudget.Priority {
	switch strings.TrimSpace(r.Header.Get(priorityHeader)) {
	case "0":
		return apsbudget.P0
	case "1":
		return apsbudget.P1
	case "2":
		return apsbudget.P2
	}
	// Images cannot carry headers; thumbnailSrc appends ?p= instead.
	switch r.URL.Query().Get("p") {
	case "0":
		return apsbudget.P0
	case "1":
		return apsbudget.P1
	case "2":
		return apsbudget.P2
	}
	if strings.HasPrefix(r.URL.Path, "/api/debug/") {
		return apsbudget.P2
	}
	if p, ok := routePriority[r.URL.Path]; ok {
		return p
	}
	return apsbudget.P0
}

// budgetContext tags ctx for the budget layer: the caller's subject (the
// OIDC sub, falling back to the session id so an identity-less session never
// coalesces with anyone), the request's priority and a label for -v logs.
func budgetContext(r *http.Request, sess *Session) contextWithValues {
	sub := ""
	if sess != nil {
		sub = sess.Profile.Sub
		if sub == "" {
			sub = "sess:" + sess.ID
		}
	}
	return contextWithValues{subject: sub, priority: requestPriority(r), label: r.URL.Path}
}

type contextWithValues struct {
	subject  string
	priority apsbudget.Priority
	label    string
}

// setThrottleHeaders writes the scheduler's current level onto w. Headers
// must precede the body, so this reports state at request start — which is
// what the SPA wants: "are we slow right now?".
func setThrottleHeaders(w http.ResponseWriter) {
	b := apiBudget()
	if b == nil {
		return
	}
	snap := b.Snapshot()
	w.Header().Set(throttleHeader, snap.Level)
	if snap.Throttled && !snap.CooldownUntil.IsZero() {
		w.Header().Set(throttleUntilHeader, itoa64(snap.CooldownUntil.UnixMilli()))
	} else if snap.Throttled && !snap.RESTCooldownUntil.IsZero() {
		w.Header().Set(throttleUntilHeader, itoa64(snap.RESTCooldownUntil.UnixMilli()))
	}
}

// invalidateUpstream drops the caller's coalesced GraphQL answers after one
// of our own writes changed what APS will list (a wiki publish, an upload):
// the author sees their write on the next fetch; other users within the
// cache's seconds-long TTL.
func (s *Server) invalidateUpstream(r *http.Request) {
	sess, _ := sessionFromCtx(r.Context())
	if bc := budgetContext(r, sess); bc.subject != "" {
		api.InvalidateSubject(bc.subject)
	}
}

// hubDMID answers a hub's Data Management id from the session when the hub
// is the one the session is locked to (captured at selection from the hub
// list), else from the GraphQL lookup. A wiki, browse or upload request no
// longer spends a call re-deriving an immutable id.
func (s *Server) hubDMID(ctx context.Context, r *http.Request, token, hubID string) (string, error) {
	if sess, ok := sessionFromCtx(r.Context()); ok && sess != nil {
		if locked, _ := sess.SelectedHub(); locked == hubID {
			if alt := sess.SelectedHubAltID(); alt != "" {
				return alt, nil
			}
		}
	}
	return api.GetHubDataManagementID(ctx, token, hubID)
}
