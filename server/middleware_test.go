package server

import (
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler is a trivial downstream handler the middleware wraps.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestSecurityHeadersProd(t *testing.T) {
	s := &Server{logger: quietLogger()} // opts.Dev == false
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)

	s.securityHeaders(okHandler()).ServeHTTP(rec, req)

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header missing")
	}
	// Spot-check the directives that carry security weight; a regression that
	// loosens any of these should fail here.
	for _, dir := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'self'"} {
		if !strings.Contains(csp, dir) {
			t.Errorf("CSP missing %q; got %q", dir, csp)
		}
	}

	// HSTS must NOT be set on a plain-HTTP request (LAN mode stays unpinned).
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security set on non-TLS request: %q", got)
	}
}

func TestSecurityHeadersHSTSOverTLS(t *testing.T) {
	s := &Server{logger: quietLogger()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	req.TLS = &tls.ConnectionState{} // mark the request as TLS-terminated

	s.securityHeaders(okHandler()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("Strict-Transport-Security missing on TLS request")
	}
}

func TestRedactQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"benign query untouched", "hubId=abc&projectId=urn%3Ax", "hubId=abc&projectId=urn%3Ax"},
		{"oauth callback", "code=SEKRIT&state=abcdef", "code=REDACTED&state=REDACTED"},
		{"mixed case key", "CODE=SEKRIT", "CODE=REDACTED"},
		{"repeated param", "code=a&code=b", "code=REDACTED&code=REDACTED"},
		{"keeps benign neighbours", "code=SEKRIT&hubId=h1", "code=REDACTED&hubId=h1"},
		{"tokens", "access_token=a&refresh_token=b&id_token=c", "access_token=REDACTED&id_token=REDACTED&refresh_token=REDACTED"},
		{"unparseable is redacted whole", "code=%zz", "REDACTED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactQuery(tc.in); got != tc.want {
				t.Errorf("redactQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestLogRequestRedactsOAuthSecrets is the end-to-end half of the above: the
// OAuth code/state must not reach the -v request log.
func TestLogRequestRedactsOAuthSecrets(t *testing.T) {
	var buf strings.Builder
	s := &Server{logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=SEKRIT-CODE&state=SEKRIT-STATE", nil)
	s.logRequest(okHandler()).ServeHTTP(httptest.NewRecorder(), req)

	line := buf.String()
	for _, secret := range []string{"SEKRIT-CODE", "SEKRIT-STATE"} {
		if strings.Contains(line, secret) {
			t.Errorf("request log leaked %q: %s", secret, line)
		}
	}
	if !strings.Contains(line, "REDACTED") {
		t.Errorf("request log has no redaction marker: %s", line)
	}
}

func TestSecurityHeadersDevNoOp(t *testing.T) {
	s := &Server{logger: quietLogger(), opts: Options{Dev: true}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost:5173/", nil)

	s.securityHeaders(okHandler()).ServeHTTP(rec, req)

	// Dev is a no-op so Vite HMR (inline preamble + websocket) isn't broken.
	for _, k := range []string{
		"Content-Security-Policy",
		"X-Frame-Options",
		"X-Content-Type-Options",
		"Referrer-Policy",
	} {
		if got := rec.Header().Get(k); got != "" {
			t.Errorf("dev mode set %s = %q, want empty", k, got)
		}
	}
}
