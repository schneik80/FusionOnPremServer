package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/fusionlink"
	"github.com/schneik80/fusionlocalserver/notifications"
)

// fusionTestServer builds a Server with just enough wired for the two
// session-less helper endpoints: the ticket store, a notifications store to
// receive the failure entry, and a quiet logger.
func fusionTestServer(t *testing.T) (*Server, *storeSet) {
	t.Helper()
	s := &Server{logger: quietLogger(), hubs: testHubStores(t, nil)}
	s.ensureAuthLimiters()
	s.ensureJobRegistries()
	set := hubSet(t, s, testHubID)
	return s, set
}

func seedTicket(s *Server, id, action string) *fusionTicket {
	tk := &fusionTicket{
		ID:          id,
		SessionID:   "sess-1",
		UserKey:     "user-1",
		Action:      action,
		FileID:      "urn:adsk.wipprod:dm.lineage:abc",
		DMProjectID: "b.proj",
		DocName:     "Widget",
		HubID:       testHubID,
		ExpiresAt:   time.Now().Add(fusionTicketTTL),
	}
	s.fusionTickets.add(tk)
	return tk
}

func TestHandleFusionTicket_RedeemsOnce(t *testing.T) {
	s, _ := fusionTestServer(t)
	seedTicket(s, "tk-1", fusionlink.ActionOpen)

	rec := httptest.NewRecorder()
	s.handleFusionTicket(rec, httptest.NewRequest(http.MethodGet, "/api/fusion/ticket?ticket=tk-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first redeem: status %d, want 200 (%s)", rec.Code, rec.Body)
	}
	var got FusionTicketDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	if got.Action != fusionlink.ActionOpen || got.FileID == "" || got.DMProjectID != "b.proj" {
		t.Errorf("payload = %+v, want the action, file and project", got)
	}

	// A replayed fusionlocal:// URL must get nothing.
	rec2 := httptest.NewRecorder()
	s.handleFusionTicket(rec2, httptest.NewRequest(http.MethodGet, "/api/fusion/ticket?ticket=tk-1", nil))
	if rec2.Code != http.StatusNotFound {
		t.Errorf("second redeem: status %d, want 404", rec2.Code)
	}
}

func TestHandleFusionTicket_UnknownAndExpiredAreIndistinguishable(t *testing.T) {
	s, _ := fusionTestServer(t)
	expired := seedTicket(s, "tk-old", fusionlink.ActionOpen)
	expired.ExpiresAt = time.Now().Add(-time.Second)

	var bodies []string
	for _, id := range []string{"tk-old", "tk-never-existed"} {
		rec := httptest.NewRecorder()
		s.handleFusionTicket(rec, httptest.NewRequest(http.MethodGet, "/api/fusion/ticket?ticket="+id, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("ticket %q: status %d, want 404", id, rec.Code)
		}
		bodies = append(bodies, rec.Body.String())
	}
	// Probing ids must not reveal which ones exist.
	if bodies[0] != bodies[1] {
		t.Errorf("expired and unknown gave different answers:\n  %s\n  %s", bodies[0], bodies[1])
	}
}

// postCallback drives POST /api/fusion/callback with a JSON body.
func postCallback(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/fusion/callback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleFusionCallback(rec, req)
	return rec
}

func TestHandleFusionCallback_RequiresRedeemedTicket(t *testing.T) {
	s, _ := fusionTestServer(t)
	seedTicket(s, "tk-1", fusionlink.ActionOpen)

	// Never redeemed: the callback is unauthenticated, so it must not be usable
	// to report on a ticket the helper never collected.
	if rec := postCallback(t, s, `{"ticket":"tk-1","ok":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("callback before redeem: status %d, want 404", rec.Code)
	}
	if rec := postCallback(t, s, `{"ticket":"nope","ok":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("callback for unknown ticket: status %d, want 404", rec.Code)
	}
}

func TestHandleFusionCallback_FailureNotifiesAndCannotBeRepeated(t *testing.T) {
	s, set := fusionTestServer(t)
	seedTicket(s, "tk-1", fusionlink.ActionOpen)
	if _, ok := s.fusionTickets.redeem("tk-1"); !ok {
		t.Fatal("redeem: want ok")
	}

	rec := postCallback(t, s, `{"ticket":"tk-1","ok":false,"code":"fusion_not_running"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: status %d, want 200 (%s)", rec.Code, rec.Body)
	}

	list, err := set.notifications.List("user-1", 0)
	if err != nil {
		t.Fatalf("listing inbox: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("inbox has %d entries, want 1", len(list))
	}
	n := list[0]
	if n.Kind != notifications.KindFusionFailed {
		t.Errorf("kind = %q, want %q", n.Kind, notifications.KindFusionFailed)
	}
	if n.Subject != "Widget" {
		t.Errorf("subject = %q, want the document name", n.Subject)
	}
	if !strings.Contains(n.Ref, "code=fusion_not_running") {
		t.Errorf("ref = %q, want it to carry the outcome code", n.Ref)
	}

	// A second callback must not overwrite the outcome or emit a second entry.
	if rec := postCallback(t, s, `{"ticket":"tk-1","ok":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("second callback: status %d, want 404", rec.Code)
	}
	if list, _ := set.notifications.List("user-1", 0); len(list) != 1 {
		t.Errorf("inbox has %d entries after a repeat callback, want 1", len(list))
	}
}

func TestHandleFusionCallback_UnknownCodeIsFlattened(t *testing.T) {
	s, set := fusionTestServer(t)
	seedTicket(s, "tk-1", fusionlink.ActionInsert)
	_, _ = s.fusionTickets.redeem("tk-1")

	// The caller is unauthenticated, so a code it invented must never be
	// stored verbatim — it would end up rendered at another user.
	if rec := postCallback(t, s, `{"ticket":"tk-1","ok":false,"code":"<script>alert(1)</script>"}`); rec.Code != http.StatusOK {
		t.Fatalf("callback: status %d, want 200", rec.Code)
	}
	list, _ := set.notifications.List("user-1", 0)
	if len(list) != 1 {
		t.Fatalf("inbox has %d entries, want 1", len(list))
	}
	if !strings.Contains(list[0].Ref, "code="+fusionlink.CodeFailed) {
		t.Errorf("ref = %q, want the invented code flattened to %q", list[0].Ref, fusionlink.CodeFailed)
	}
}

func TestHandleFusionCallback_SuccessDoesNotNotify(t *testing.T) {
	s, set := fusionTestServer(t)
	seedTicket(s, "tk-1", fusionlink.ActionOpen)
	_, _ = s.fusionTickets.redeem("tk-1")

	if rec := postCallback(t, s, `{"ticket":"tk-1","ok":true}`); rec.Code != http.StatusOK {
		t.Fatalf("callback: status %d, want 200", rec.Code)
	}
	// Success is deliberately silent: Fusion coming to the front is the
	// feedback, and a bell badge per click would be noise.
	if list, _ := set.notifications.List("user-1", 0); len(list) != 0 {
		t.Errorf("a successful action emitted %d notification(s), want 0", len(list))
	}
}

func TestIsLoopbackRequest(t *testing.T) {
	s := &Server{logger: quietLogger()}
	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    bool
	}{
		{"ipv4 loopback", "127.0.0.1:54321", nil, true},
		{"ipv6 loopback", "[::1]:54321", nil, true},
		{"lan address", "192.168.1.20:54321", nil, false},
		// A forwarded header is a CLIENT ASSERTION. Believing one here would
		// let a remote browser claim the fast path and drive the server
		// operator's Fusion, so any forwarding header forces the remote path.
		{"loopback but forwarded", "127.0.0.1:54321", map[string]string{"X-Forwarded-For": "1.2.3.4"}, false},
		{"loopback but real-ip", "127.0.0.1:54321", map[string]string{"X-Real-IP": "1.2.3.4"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/fusion/action", nil)
			r.RemoteAddr = tc.remote
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := s.isLoopbackRequest(r); got != tc.want {
				t.Errorf("isLoopbackRequest(%s%v) = %v, want %v", tc.remote, tc.headers, got, tc.want)
			}
		})
	}
}

func TestHelperOrigin_PrefersPublicOrigin(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/fusion/action", nil)
	r.Host = "192.168.1.5:8080"

	// Without a canonical URL, the origin is whatever host the request used.
	plain := &Server{logger: quietLogger()}
	if got := plain.helperOrigin(r); got != "http://192.168.1.5:8080" {
		t.Errorf("helperOrigin = %q, want the request host", got)
	}
	// With TLS on, the helper must be told https — pairing compares scheme too,
	// so an http origin here would never match the recorded pairing.
	tls := &Server{logger: quietLogger(), tlsEnabled: true}
	if got := tls.helperOrigin(r); got != "https://192.168.1.5:8080" {
		t.Errorf("helperOrigin (tls) = %q, want https", got)
	}
	// A configured canonical URL wins, so every helper pairs with one origin.
	pub := &Server{logger: quietLogger(), publicOrigin: "https://fusion.example:8080"}
	if got := pub.helperOrigin(r); got != "https://fusion.example:8080" {
		t.Errorf("helperOrigin (public) = %q, want the canonical origin", got)
	}
}

func TestFusionRoutes_TicketAndCallbackAreSessionLess(t *testing.T) {
	// The helper is a native app with no cookie, so these two must be reachable
	// without one — and everything else under /api/fusion must not be.
	s := &Server{logger: quietLogger(), hubs: testHubStores(t, nil)}
	h := s.routes()

	for _, tc := range []struct {
		method, path string
		wantAuthWall bool
	}{
		{http.MethodGet, "/api/fusion/ticket?ticket=nope", false},
		{http.MethodPost, "/api/fusion/callback", false},
		{http.MethodPost, "/api/fusion/action", true},
		{http.MethodGet, "/api/fusion/outcome?ticket=nope", true},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		gotWall := rec.Code == http.StatusUnauthorized
		if gotWall != tc.wantAuthWall {
			t.Errorf("%s %s: status %d (auth wall %v), want auth wall %v",
				tc.method, tc.path, rec.Code, gotWall, tc.wantAuthWall)
		}
	}
}
