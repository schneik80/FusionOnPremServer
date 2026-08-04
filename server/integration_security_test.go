package server

// QA security: black-box integration tests through the full middleware stack
// (recover -> log -> securityHeaders -> canonicalRedirect -> requireSameOrigin
// -> devCORS -> mux).
// Each test drives a real httptest.Server built from s.routes(), so it exercises
// routing, auth gating, headers, and the OAuth callback exactly as a client hits
// them. No live APS calls are made: the data routes are tested unauthenticated
// (they 401 before any upstream call), and the OAuth callback is tested on its
// state-validation path (which short-circuits before the token exchange).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newIntegrationServer builds a fully-wired, non-dev Server with real session
// and pending stores, ready to serve s.routes() over httptest.
func newIntegrationServer() *Server {
	return &Server{
		logger:   quietLogger(),
		clientID: "test-client",
		sessions: NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger()),
		pending:  NewPendingStore(pendingTTL),
	}
}

func TestIntegration_SecurityHeadersPresent(t *testing.T) {
	srv := httptest.NewServer(newIntegrationServer().routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := res.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors 'none': %q", csp)
	}
}

// Data routes must reject an unauthenticated caller with a JSON 401 envelope —
// never the SPA shell, never a 5xx, never an upstream call.
func TestIntegration_UnauthenticatedDataRoutes401(t *testing.T) {
	srv := httptest.NewServer(newIntegrationServer().routes())
	defer srv.Close()

	for _, path := range []string{
		"/api/hubs",
		"/api/items/details?hubId=h&itemId=i",
		"/api/pins?hubId=h",
	} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", path, res.StatusCode)
		}
		var env struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &env); err != nil || env.Error == "" {
			t.Errorf("%s: want JSON error envelope, got %q", path, body)
		}
	}
}

// A POST with a malformed body to an auth-gated route is rejected at the auth
// gate (401) before the body is ever parsed — confirming the gate runs first.
func TestIntegration_MalformedBodyHitsAuthGateFirst(t *testing.T) {
	srv := httptest.NewServer(newIntegrationServer().routes())
	defer srv.Close()

	res, err := http.Post(srv.URL+"/api/pins?hubId=h", "application/json",
		strings.NewReader(`{"id": malformed`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (auth before body parse)", res.StatusCode)
	}
}

func TestIntegration_UnknownAPIPathJSON404(t *testing.T) {
	srv := httptest.NewServer(newIntegrationServer().routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
		t.Errorf("404 should be JSON, got %q", res.Header.Get("Content-Type"))
	}
}

// OAuth callback abuse: a forged/replayed state with no matching pending cookie
// must be rejected (redirect to the login error), never exchanged. This is the
// auth-flow equivalent of a brute-force/replay attempt — the defense is the
// single-use, cookie-bound, 256-bit state, not a rate limiter.
func TestIntegration_OAuthCallbackRejectsForgedState(t *testing.T) {
	srv := httptest.NewServer(newIntegrationServer().routes())
	defer srv.Close()

	// Don't follow the redirect; inspect it.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	cases := []struct {
		name string
		url  string
	}{
		{"no_state_no_cookie", "/api/auth/callback?code=abc"},
		{"forged_state_no_cookie", "/api/auth/callback?code=abc&state=attacker-supplied"},
		{"upstream_error", "/api/auth/callback?error=access_denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := client.Get(srv.URL + tc.url)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusFound {
				t.Fatalf("status = %d, want 302 redirect", res.StatusCode)
			}
			loc := res.Header.Get("Location")
			if !strings.HasPrefix(loc, "/?auth_error=") {
				t.Errorf("redirect = %q, want /?auth_error=...", loc)
			}
			// A forged callback must never set a session cookie.
			for _, c := range res.Cookies() {
				if c.Name == sessionCookieName && c.Value != "" {
					t.Errorf("forged callback minted a session cookie: %q", c.Value)
				}
			}
		})
	}
}

// TestIntegration_CrossSiteMutationBlocked is the CSRF backstop end-to-end: a
// mutating request carrying a foreign Origin is refused by the middleware,
// before the auth gate — so it can't even probe which routes exist.
func TestIntegration_CrossSiteMutationBlocked(t *testing.T) {
	srv := httptest.NewServer(newIntegrationServer().routes())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/pins", strings.NewReader(`{"id":"x","kind":"design"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-site mutation", res.StatusCode)
	}
	var body errorResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decoding error envelope: %v", err)
	}
	if body.Code != "forbidden" {
		t.Errorf("error code = %q, want forbidden", body.Code)
	}
}

// CHARACTERIZATION (not a pass/fail security assertion): the server currently
// has NO application-level rate limiter on any endpoint, and there is no
// password / password-reset endpoint to brute-force (auth is delegated OAuth).
// This test documents that reality: a burst of requests all succeed, none are
// throttled (429). It is the harness where a real limiter test would live — once
// a limiter is added, flip the expectation to require a 429 within the burst.
func TestIntegration_NoRateLimiterPresent_Characterization(t *testing.T) {
	srv := httptest.NewServer(newIntegrationServer().routes())
	defer srv.Close()

	const burst = 60
	throttled := 0
	for i := 0; i < burst; i++ {
		res, err := http.Get(srv.URL + "/api/meta")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			throttled++
		}
	}
	// Today: zero throttling. We assert the *current* contract so the test is
	// honest and green; the t.Logf flags the gap for whoever reads the output.
	if throttled != 0 {
		t.Errorf("a rate limiter now triggers (%d/%d throttled) — update this test to assert the limiter's policy", throttled, burst)
	}
	t.Logf("characterization: %d/%d requests served, 0 throttled — no app-level rate limiter exists", burst, burst)
}
