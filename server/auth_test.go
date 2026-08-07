package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/auth"
)

func newAuthTestServer() *Server {
	return &Server{
		logger:   quietLogger(),
		clientID: "test-client",
		sessions: NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger()),
		pending:  NewPendingStore(pendingTTL),
	}
}

func TestRequireAuth_NoCookie(t *testing.T) {
	s := newAuthTestServer()
	h := s.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler must not run without a session")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hubs", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuth_UnknownSession(t *testing.T) {
	s := newAuthTestServer()
	h := s.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler must not run for an unknown session")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/hubs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "bogus"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuth_InjectsToken(t *testing.T) {
	s := newAuthTestServer()
	sess, _ := s.sessions.Create(
		&auth.TokenData{AccessToken: "tok-123", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{},
	)

	var gotTok string
	h := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTok, _ = tokenFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/hubs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotTok != "tok-123" {
		t.Errorf("token in context = %q, want tok-123", gotTok)
	}
}

func TestHandleAuthMe(t *testing.T) {
	s := newAuthTestServer()

	rec := httptest.NewRecorder()
	s.handleAuthMe(rec, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	if !strings.Contains(rec.Body.String(), `"authenticated":false`) {
		t.Errorf("unauthenticated me body = %q", rec.Body.String())
	}

	sess, _ := s.sessions.Create(
		&auth.TokenData{AccessToken: "AT", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{Name: "Ada", Email: "ada@x.io"},
	)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.ID})
	rec = httptest.NewRecorder()
	s.handleAuthMe(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `"authenticated":true`) || !strings.Contains(body, "ada@x.io") {
		t.Errorf("authenticated me body = %q", body)
	}
}

func TestHandleAuthLogin_RedirectAndPending(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodGet, "http://host.lan:8080/api/auth/login", nil)
	rec := httptest.NewRecorder()
	s.handleAuthLogin(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("authorize URL is missing the state param")
	}
	if got, want := u.Query().Get("redirect_uri"), "http://host.lan:8080/api/auth/callback"; got != want {
		t.Errorf("redirect_uri = %q, want %q", got, want)
	}

	var pendingCookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == pendingCookieName {
			pendingCookie = c.Value
		}
	}
	if pendingCookie != state {
		t.Errorf("pending cookie = %q, want it to equal state %q", pendingCookie, state)
	}
	if _, ok := s.pending.Take(state); !ok {
		t.Error("pending store has no entry for the issued state")
	}
}

func TestHandleAuthCallback_ErrorParam(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?error=access_denied&error_description=nope", nil)
	rec := httptest.NewRecorder()
	s.handleAuthCallback(rec, req)
	assertAuthErrorRedirect(t, rec, "access_denied")
}

func TestHandleAuthCallback_StateMismatch(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c&state=abc", nil)
	req.AddCookie(&http.Cookie{Name: pendingCookieName, Value: "different"})
	rec := httptest.NewRecorder()
	s.handleAuthCallback(rec, req)
	assertAuthErrorRedirect(t, rec, "state_mismatch")
}

func TestHandleAuthCallback_HappyPath(t *testing.T) {
	s := newAuthTestServer()

	const state = "the-state"
	const redirectURI = "http://h/api/auth/callback"
	s.pending.Put(state, pendingEntry{verifier: "v", redirectURI: redirectURI, createdAt: time.Now()})

	prevEx, prevUI := authExchange, authUserInfo
	t.Cleanup(func() { authExchange, authUserInfo = prevEx, prevUI })
	authExchange = func(ctx context.Context, id, secret, code, verifier, ru string) (*auth.TokenData, error) {
		if code != "the-code" || verifier != "v" || ru != redirectURI {
			t.Errorf("exchange args: code=%q verifier=%q redirect=%q", code, verifier, ru)
		}
		return &auth.TokenData{AccessToken: "AT", RefreshToken: "RT", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	authUserInfo = func(context.Context, string) (auth.UserProfile, error) {
		return auth.UserProfile{Name: "Grace", Email: "grace@x.io"}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=the-code&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: pendingCookieName, Value: state})
	rec := httptest.NewRecorder()
	s.handleAuthCallback(rec, req)

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback success: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}

	var sid string
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sid = c.Value
		}
	}
	if sid == "" {
		t.Fatal("no session cookie set on success")
	}
	sess, ok := s.sessions.Get(sid)
	if !ok {
		t.Fatal("session not found after a successful callback")
	}
	if sess.Profile.Email != "grace@x.io" {
		t.Errorf("session profile = %+v", sess.Profile)
	}
	if _, ok := s.pending.Take(state); ok {
		t.Error("pending entry was not consumed by the callback")
	}
}

func TestHandleAuthLogout(t *testing.T) {
	s := newAuthTestServer()
	sess, _ := s.sessions.Create(
		&auth.TokenData{AccessToken: "AT", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{},
	)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	s.handleAuthLogout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if _, ok := s.sessions.Get(sess.ID); ok {
		t.Error("session was not deleted on logout")
	}
}

// TestSessionToken_RefreshesExactlyOnce verifies the per-session refresh lock:
// many concurrent requests on one expired session trigger a single refresh
// (APS rotates the refresh token, so a double refresh would brick the session).
func TestSessionToken_RefreshesExactlyOnce(t *testing.T) {
	s := newAuthTestServer()
	sess, _ := s.sessions.Create(
		&auth.TokenData{AccessToken: "old", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Minute)},
		auth.UserProfile{},
	)

	prev := authRefresh
	t.Cleanup(func() { authRefresh = prev })
	var calls int32
	authRefresh = func(context.Context, string, string, string) (*auth.TokenData, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond) // widen the race window
		return &auth.TokenData{AccessToken: "new", RefreshToken: "rt2", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}

	const n = 8
	var wg sync.WaitGroup
	toks := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := s.sessionToken(context.Background(), sess)
			if err != nil {
				t.Errorf("sessionToken: %v", err)
			}
			toks[i] = tok
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("refresh calls = %d, want 1", got)
	}
	for i, tk := range toks {
		if tk != "new" {
			t.Errorf("goroutine %d token = %q, want new", i, tk)
		}
	}
}

func TestSessionToken_NoRefreshToken(t *testing.T) {
	s := newAuthTestServer()
	sess, _ := s.sessions.Create(
		&auth.TokenData{AccessToken: "old", ExpiresAt: time.Now().Add(-time.Minute)},
		auth.UserProfile{},
	)
	if _, err := s.sessionToken(context.Background(), sess); err == nil {
		t.Error("expected an error when the token is expired with no refresh token")
	}
}

func assertAuthErrorRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantReason string) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/?auth_error=") || !strings.Contains(loc, wantReason) {
		t.Errorf("Location = %q, want /?auth_error=...%s", loc, wantReason)
	}
}

func TestUserAllowed(t *testing.T) {
	cases := []struct {
		name    string
		list    []string
		profile auth.UserProfile
		want    bool
	}{
		{"empty list allows anyone", nil, auth.UserProfile{Email: "x@y.z"}, true},
		{"empty list allows empty profile", nil, auth.UserProfile{}, true},
		{"sub matches exactly", []string{"sub-1"}, auth.UserProfile{Sub: "sub-1"}, true},
		{"email matches case-insensitively", []string{"Ada@X.io"}, auth.UserProfile{Email: "ada@x.io"}, true},
		{"unlisted user denied", []string{"ada@x.io"}, auth.UserProfile{Sub: "s", Email: "bob@x.io"}, false},
		{"empty profile fails closed", []string{"ada@x.io"}, auth.UserProfile{}, false},
		{"blank entry never matches empty sub", []string{""}, auth.UserProfile{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newAuthTestServer()
			s.adminUsers = tc.list
			if got := s.userAllowed(tc.profile); got != tc.want {
				t.Errorf("userAllowed(%+v) with list %v = %v, want %v", tc.profile, tc.list, got, tc.want)
			}
		})
	}
}

// runCallback drives a full callback round-trip with stubbed exchange and
// userinfo. A non-nil profileErr simulates a userinfo outage, in which case
// the callback sees the zero profile — exactly like production.
func runCallback(t *testing.T, s *Server, profile auth.UserProfile, profileErr error) *httptest.ResponseRecorder {
	t.Helper()
	const state = "wl-state"
	s.pending.Put(state, pendingEntry{verifier: "v", redirectURI: "http://h/api/auth/callback", createdAt: time.Now()})
	prevEx, prevUI := authExchange, authUserInfo
	t.Cleanup(func() { authExchange, authUserInfo = prevEx, prevUI })
	authExchange = func(context.Context, string, string, string, string, string) (*auth.TokenData, error) {
		return &auth.TokenData{AccessToken: "AT", RefreshToken: "RT", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	authUserInfo = func(context.Context, string) (auth.UserProfile, error) {
		return profile, profileErr
	}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: pendingCookieName, Value: state})
	rec := httptest.NewRecorder()
	s.handleAuthCallback(rec, req)
	return rec
}

// sessionCookieValue returns the value of the session cookie set on rec, or ""
// when none was set (cleared cookies have an empty value, so they read as none).
func sessionCookieValue(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c.Value
		}
	}
	return ""
}

func TestHandleAuthCallback_WhitelistDenied(t *testing.T) {
	s := newAuthTestServer()
	s.adminUsers = []string{"ada@x.io"}
	rec := runCallback(t, s, auth.UserProfile{Sub: "sub-bob", Email: "bob@x.io"}, nil)
	assertAuthErrorRedirect(t, rec, "not_allowed")
	if sid := sessionCookieValue(rec); sid != "" {
		t.Errorf("denied sign-in still set a session cookie %q", sid)
	}
}

func TestHandleAuthCallback_WhitelistAllowed(t *testing.T) {
	s := newAuthTestServer()
	// Email listed with different casing plus an unrelated sub entry.
	s.adminUsers = []string{"sub-someone-else", "ADA@X.IO"}
	rec := runCallback(t, s, auth.UserProfile{Sub: "sub-ada", Email: "ada@x.io"}, nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("allowed sign-in: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	if sid := sessionCookieValue(rec); sid == "" {
		t.Error("allowed sign-in did not set a session cookie")
	}
}

// TestHandleAuthCallback_WhitelistFailsClosed: with a whitelist active, a
// failed userinfo fetch (zero profile) must deny — otherwise a userinfo outage
// becomes an auth bypass.
func TestHandleAuthCallback_WhitelistFailsClosed(t *testing.T) {
	s := newAuthTestServer()
	s.adminUsers = []string{"ada@x.io"}
	rec := runCallback(t, s, auth.UserProfile{}, errors.New("userinfo down"))
	assertAuthErrorRedirect(t, rec, "not_allowed")
	if sid := sessionCookieValue(rec); sid != "" {
		t.Errorf("fail-closed sign-in still set a session cookie %q", sid)
	}
}

// TestRequireAuth_WhitelistRevocation: an existing session whose user is no
// longer listed is deleted and answered 401 at the requireAuth choke point.
func TestRequireAuth_WhitelistRevocation(t *testing.T) {
	s := newAuthTestServer()
	sess, _ := s.sessions.Create(
		&auth.TokenData{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{Sub: "sub-bob", Email: "bob@x.io"},
	)
	s.adminUsers = []string{"ada@x.io"}

	h := s.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler must not run for a revoked user")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/hubs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if _, ok := s.sessions.Get(sess.ID); ok {
		t.Error("revoked session was not deleted")
	}
}

// TestHandleAuthMe_WhitelistRevocation: /api/auth/me sits outside requireAuth,
// so it must apply the same revocation instead of reporting authenticated:true.
func TestHandleAuthMe_WhitelistRevocation(t *testing.T) {
	s := newAuthTestServer()
	sess, _ := s.sessions.Create(
		&auth.TokenData{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{Sub: "sub-bob", Email: "bob@x.io"},
	)
	s.adminUsers = []string{"ada@x.io"}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	s.handleAuthMe(rec, req)

	if !strings.Contains(rec.Body.String(), `"authenticated":false`) {
		t.Errorf("revoked me body = %q, want authenticated:false", rec.Body.String())
	}
	if _, ok := s.sessions.Get(sess.ID); ok {
		t.Error("revoked session was not deleted by the me probe")
	}
}

func TestSanitizeNext(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"embed path with params kept", "/embed.html?dmProjectId=a.p1&theme=dark", "/embed.html?dmProjectId=a.p1&theme=dark"},
		{"root kept", "/", "/"},
		{"empty collapses", "", ""},
		{"absolute url rejected", "https://evil.example/x", ""},
		{"protocol-relative rejected", "//evil.example", ""},
		{"backslash prefix rejected", "/\\evil.example", ""},
		{"embedded backslash rejected", "/a\\b", ""},
		{"embedded scheme rejected", "/x://evil", ""},
		{"javascript scheme rejected", "javascript:alert(1)", ""},
		{"relative path rejected", "embed.html", ""},
		{"api path rejected", "/api/auth/login", ""},
		{"control char rejected", "/a\nb", ""},
		{"overlong rejected", "/" + strings.Repeat("a", 600), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeNext(tc.in); got != tc.want {
				t.Errorf("sanitizeNext(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// runCallbackWithNext is runCallback with a pending entry whose next was
// produced by handleAuthLogin's sanitizeNext call.
func runCallbackWithNext(t *testing.T, s *Server, next string) *httptest.ResponseRecorder {
	t.Helper()
	const state = "next-state"
	s.pending.Put(state, pendingEntry{
		verifier: "v", redirectURI: "http://h/api/auth/callback",
		next: sanitizeNext(next), createdAt: time.Now(),
	})
	prevEx, prevUI := authExchange, authUserInfo
	t.Cleanup(func() { authExchange, authUserInfo = prevEx, prevUI })
	authExchange = func(context.Context, string, string, string, string, string) (*auth.TokenData, error) {
		return &auth.TokenData{AccessToken: "AT", RefreshToken: "RT", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	authUserInfo = func(context.Context, string) (auth.UserProfile, error) {
		return auth.UserProfile{Sub: "sub-ada", Email: "ada@x.io"}, nil
	}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: pendingCookieName, Value: state})
	rec := httptest.NewRecorder()
	s.handleAuthCallback(rec, req)
	return rec
}

func TestHandleAuthCallback_RedirectsToNext(t *testing.T) {
	s := newAuthTestServer()
	rec := runCallbackWithNext(t, s, "/embed.html?dmProjectId=a.p1")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/embed.html?dmProjectId=a.p1" {
		t.Fatalf("status=%d location=%q, want 302 /embed.html?dmProjectId=a.p1", rec.Code, rec.Header().Get("Location"))
	}
	if sid := sessionCookieValue(rec); sid == "" {
		t.Error("sign-in with next did not set a session cookie")
	}
}

func TestHandleAuthCallback_MaliciousNextFallsBackToRoot(t *testing.T) {
	s := newAuthTestServer()
	rec := runCallbackWithNext(t, s, "https://evil.example/phish")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("status=%d location=%q, want 302 /", rec.Code, rec.Header().Get("Location"))
	}
}
