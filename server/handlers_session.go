package server

import (
	"encoding/json"
	"net/http"

	"github.com/schneik80/fusionlocalserver/api"
)

// fetchHubs is the indirection over api.GetHubs used to validate hub
// membership at lock time, stubbed in tests (matching the auth package's
// const→var injection style). Production code never reassigns it.
var fetchHubs = api.GetHubs

// handleSessionHub is POST /api/session/hub {hubId}: lock the session to one
// hub after validating, with the caller's own APS token, that they are a
// member of it. Re-POSTing a different hub IS the hub switch — the storeSet
// already resolved into in-flight requests keeps them on the old hub, and
// every subsequent request resolves the new one.
func (s *Server) handleSessionHub(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	tok, ok := s.token(r.Context(), w, r)
	if !ok {
		return
	}
	var in struct {
		HubID string `json:"hubId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.HubID == "" {
		writeError(w, http.StatusBadRequest, "hubId is required")
		return
	}

	// Membership check: the hub must appear in the caller's own hub list.
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	hubs, err := fetchHubs(ctx, tok)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	hubName := ""
	member := false
	for _, h := range hubs {
		if h.ID == in.HubID {
			member = true
			hubName = h.Name
			break
		}
	}
	if !member {
		writeError(w, http.StatusForbidden, "you are not a member of this hub")
		return
	}

	// Open (or create) the hub's store set before locking the session, so a
	// collision/IO refusal never leaves the session pointing at a hub whose
	// stores can't open.
	if _, err := s.hubs.get(in.HubID, hubName); err != nil {
		s.logger.Error("hub: store set unavailable at selection", "hub", in.HubID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "local storage for this hub is unavailable")
		return
	}
	if !s.sessions.SetSelectedHub(sess.ID, in.HubID, hubName) {
		writeError(w, http.StatusUnauthorized, "session expired or unknown")
		return
	}
	s.logger.Info("session: hub selected", "user", sess.Profile.Email, "hub", in.HubID, "hubName", hubName)
	writeJSON(w, http.StatusOK, authMeDTO(sess))
}
