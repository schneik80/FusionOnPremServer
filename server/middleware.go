package server

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

// statusRecorder wraps a ResponseWriter to capture the status code and byte
// count for request logging. It forwards Hijack and Flush so the dev-mode Vite
// reverse proxy's websocket Upgrade (HMR) and streaming responses still work.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	n, err := sr.ResponseWriter.Write(b)
	sr.bytes += n
	return n, err
}

func (sr *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := sr.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
	}
	return hj.Hijack()
}

func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequest logs one structured line per request after it completes. It logs
// at debug level, so the per-request stream is off by default and appears only
// under -v; essential lines (startup, warnings, errors) stay at info and above.
func (s *Server) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		s.requestCount.Add(1)
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		s.logger.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"query", redactQuery(r.URL.RawQuery),
			"status", rec.status,
			"bytes", rec.bytes,
			"dur_ms", time.Since(start).Milliseconds(),
			"remote", s.clientIP(r),
		)
	})
}

// secretQueryParams are the query parameters whose values never belong in a
// log line. The OAuth callback carries `code` and `state` — single-use and
// PKCE-bound, but still credentials — and the rest are here so a future route
// can't leak one by accident.
var secretQueryParams = map[string]bool{
	"code":          true,
	"state":         true,
	"code_verifier": true,
	"access_token":  true,
	"refresh_token": true,
	"id_token":      true,
	"token":         true,
	"client_secret": true,
}

// redactQuery replaces the values of sensitive query parameters with REDACTED,
// leaving the rest of the query readable for debugging. A query that won't
// parse is redacted whole rather than logged on the chance it holds a secret.
// Same posture as redactSignedURLs in api/debug.go.
func redactQuery(raw string) string {
	if raw == "" {
		return ""
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return "REDACTED"
	}
	touched := false
	for k, vs := range vals {
		if !secretQueryParams[strings.ToLower(k)] {
			continue
		}
		for i := range vs {
			vs[i] = "REDACTED"
		}
		touched = true
	}
	if !touched {
		return raw // untouched queries keep their original ordering
	}
	return vals.Encode()
}

// recoverPanic converts a panic in any downstream handler into a 500 JSON
// envelope and logs the stack, so a single bad request can't take the server
// down.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("panic recovered",
					"path", r.URL.Path,
					"panic", fmt.Sprintf("%v", rec),
					"stack", string(debug.Stack()),
				)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets defense-in-depth response headers on every reply. The
// CSP is tuned to the SPA's actual needs: same-origin scripts/connections,
// images from self plus data:/blob: (thumbnails and canvas-rendered previews),
// and inline <style> (MUI/emotion injects it). frame-ancestors 'none' is the
// clickjacking backstop; HSTS is emitted only when the request is already TLS,
// so the plain-HTTP LAN mode isn't pinned to https.
//
// Disabled in -dev mode: the Vite dev server's HMR (inline preamble + a
// websocket to its own origin) is incompatible with this CSP, and dev never
// faces the network anyway.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	if s.opts.Dev {
		return next
	}
	const csp = "default-src 'self'; " +
		"img-src 'self' data: blob:; " +
		"style-src 'self' 'unsafe-inline'; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		if s.isSecure(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// canonicalRedirect bounces any request that arrived via a host other than the
// configured public URL to that canonical origin, so the SPA load and the whole
// OAuth round-trip stay same-origin — which is what lets a single redirect_uri
// be registered with APS. No-op when no public URL is set.
func (s *Server) canonicalRedirect(next http.Handler) http.Handler {
	if s.publicOrigin == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.requestOrigin(r) != s.publicOrigin {
			http.Redirect(w, r, s.publicURL+r.URL.RequestURI(), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireSameOrigin is the CSRF backstop behind the session cookie's
// SameSite=Lax: a mutating request whose Origin (or, failing that, Referer)
// names a different site is refused before it reaches a handler. Lax already
// blocks cross-site cookie POSTs in current browsers; this catches the cases it
// doesn't (a browser that mis-implements Lax, a same-site-but-different-origin
// page) without a token round-trip.
//
// Deliberately lenient when a request carries neither header: browsers always
// send Origin on cross-site mutations, so nothing exploitable slips through,
// while curl, scripts, and the standalone web/test/api-security.test.ts suite
// keep working. Disabled under -dev, where the Vite dev server is a different
// origin by design (same carve-out as securityHeaders and devCORS).
func (s *Server) requireSameOrigin(next http.Handler) http.Handler {
	if s.opts.Dev {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			// Safe verbs, and the OAuth callback and SSE streams with them.
			next.ServeHTTP(w, r)
			return
		}
		if claimed, ok := claimedOrigin(r); ok && !s.originAllowed(r, claimed) {
			s.logger.Warn("blocked cross-site request",
				"method", r.Method, "path", r.URL.Path,
				"origin", claimed, "remote", s.clientIP(r))
			writeError(w, http.StatusForbidden, "cross-site request blocked")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed reports whether an origin the request claims is this server:
// the origin it actually arrived on, or the configured canonical public one.
func (s *Server) originAllowed(r *http.Request, origin string) bool {
	if strings.EqualFold(origin, s.requestOrigin(r)) {
		return true
	}
	return s.publicOrigin != "" && strings.EqualFold(origin, s.publicOrigin)
}

// claimedOrigin is the scheme://host the request says it came from — the Origin
// header, or the Referer's origin when Origin is absent. ok is false when the
// request claims nothing parseable.
func claimedOrigin(r *http.Request) (string, bool) {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		return o, true
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		return "", false
	}
	u, err := url.Parse(ref)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// devCORS adds permissive CORS headers, but only in -dev mode. It lets a
// Vite dev server running on a different origin (e.g. :5173) call the API on
// :8080 directly. Production serves the SPA same-origin, so no CORS is emitted.
func (s *Server) devCORS(next http.Handler) http.Handler {
	if !s.opts.Dev {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
