package server

import (
	"context"
	"net/http"
)

// Error codes for the hub session lock. Stable wire tokens: the SPA maps
// hub_not_selected to its hub gate (like a 401 maps to login) and
// hub_mismatch marks a request that named a hub other than the session's.
const (
	codeHubNotSelected = "hub_not_selected"
	codeHubMismatch    = "hub_mismatch"
)

// storesFromCtx returns the storeSet requireHub resolved for this request —
// the ONLY sanctioned way for a handler to reach local stores. Resolving it
// once per request (rather than re-reading the session) makes a request
// immune to a mid-flight hub switch.
func storesFromCtx(ctx context.Context) (*storeSet, bool) {
	set, ok := ctx.Value(storesCtxKey).(*storeSet)
	return set, ok && set != nil
}

// sessionHubFromCtx returns the hub the request is locked to.
func sessionHubFromCtx(ctx context.Context) (hubID, hubName string, ok bool) {
	set, ok := storesFromCtx(ctx)
	if !ok {
		return "", "", false
	}
	return set.hubID, set.hubName, true
}

// reqStores fetches the request's storeSet or writes the 409 envelope. The
// absent case is unreachable behind requireHub; the guard exists so a
// handler wired outside the middleware fails closed, never open.
func reqStores(w http.ResponseWriter, r *http.Request) (*storeSet, bool) {
	set, ok := storesFromCtx(r.Context())
	if !ok {
		writeErrorCode(w, http.StatusConflict, codeHubNotSelected, "no hub selected for this session")
		return nil, false
	}
	return set, true
}

// hubMatches enforces the session-hub equality on a hub id that arrived in a
// request BODY or multipart field (query params are checked centrally in
// requireHub; bodies are only read by their handlers).
func hubMatches(w http.ResponseWriter, sessionHubID, bodyHubID string) bool {
	if bodyHubID == sessionHubID {
		return true
	}
	writeErrorCode(w, http.StatusForbidden, codeHubMismatch,
		"request names a hub other than the session's selected hub")
	return false
}

// requireHub is the hub choke point, layered AFTER requireAuth on every data
// route: it refuses sessions with no selected hub (409 hub_not_selected),
// centrally 403s any query-param hubId that differs from the session hub
// (bodies are never read here), and resolves the session hub's storeSet ONCE
// into the request context. Handlers therefore cannot reach another hub's
// stores no matter what identifiers the wire carries.
func (s *Server) requireHub(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := sessionFromCtx(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		hubID, hubName := sess.SelectedHub()
		if hubID == "" {
			writeErrorCode(w, http.StatusConflict, codeHubNotSelected, "no hub selected for this session")
			return
		}
		if q := r.URL.Query().Get("hubId"); q != "" && q != hubID {
			writeErrorCode(w, http.StatusForbidden, codeHubMismatch,
				"request names a hub other than the session's selected hub")
			return
		}
		set, err := s.hubs.get(hubID, hubName)
		if err != nil {
			s.logger.Error("hub: store set unavailable", "hub", hubID, "err", err)
			writeError(w, http.StatusServiceUnavailable, "local storage for this hub is unavailable")
			return
		}
		ctx := context.WithValue(r.Context(), storesCtxKey, set)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
