package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/schneik80/fusionlocalserver/chat"
)

// quietLogger returns a logger that discards all output, for tests that
// construct a Server without exercising logging.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The hub every test session locks to by default (login does it), matching
// the "hub-1" the fixtures have always used in store envelopes.
const (
	testHubID   = "hub-1"
	testHubName = "Hub One"
)

// testHubStores builds a hubStores over a throwaway config dir and arranges
// its shutdown.
func testHubStores(t testing.TB, authz *chat.Authorizer) *hubStores {
	t.Helper()
	hs := newHubStores(t.TempDir(), authz)
	t.Cleanup(hs.closeAll)
	return hs
}

// hubSet resolves (building if needed) the store set for hubID on s — the
// test-side stand-in for what requireHub does per request.
func hubSet(t testing.TB, s *Server, hubID string) *storeSet {
	t.Helper()
	set, err := s.hubs.get(hubID, hubID)
	if err != nil {
		t.Fatalf("hubSet(%q): %v", hubID, err)
	}
	return set
}

// lockHub points the cookie's session at hubID — the test-side equivalent of
// POST /api/session/hub (which needs a live APS hub list to validate).
func lockHub(t testing.TB, s *Server, cookie *http.Cookie, hubID string) {
	t.Helper()
	if !s.sessions.SetSelectedHub(cookie.Value, hubID, hubID) {
		t.Fatalf("lockHub: session %q not found", cookie.Value)
	}
}

// withHubCtx stamps a request with a storeSet, standing in for requireHub in
// direct-handler tests (pins handlers only consult set.hubID).
func withHubCtx(r *http.Request, set *storeSet) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), storesCtxKey, set))
}
