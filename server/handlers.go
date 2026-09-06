package server

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"
)

// handlerTimeout bounds every upstream APS call. Folder/version pagination and
// the iterative folder-ancestry walk can fan out into several round-trips, so
// 30s gives headroom while still capping a wedged gateway. It mirrors the
// TUI's nav-command timeout.
const handlerTimeout = 30 * time.Second

// reqCtx derives a timeout-bounded context from the request. The caller must
// defer the returned cancel.
func (s *Server) reqCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), handlerTimeout)
}

// token returns the caller's APS access token, which requireAuth resolved from
// their session and placed in the request context. It writes a 401 envelope and
// reports ok=false if absent (which shouldn't happen on a requireAuth'd route).
func (s *Server) token(ctx context.Context, w http.ResponseWriter, r *http.Request) (string, bool) {
	tok, ok := tokenFromCtx(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return "", false
	}
	return tok, true
}

// reqParam reads a required query parameter, or writes a 400 envelope and
// reports ok=false.
func reqParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		writeError(w, http.StatusBadRequest, "missing required query parameter: "+name)
		return "", false
	}
	return v, true
}

// fail logs the full upstream/handler error internally and writes a safe,
// status-keyed envelope to the client. The raw error chain can carry APS
// GraphQL response bodies, internal URLs, and other upstream detail, so by
// default only the category (auth, forbidden, rate-limit, timeout, upstream)
// crosses the wire. Under -v (Verbose), the detailed error is appended to the
// client message too — a deliberate diagnostic affordance for a developer run,
// never the production default. The full err is logged regardless.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := statusForError(err)
	msg := safeErrorMessage(status)
	if s.opts.Verbose {
		msg += ": " + err.Error()
	}
	if rl := rateLimitFrom(err); rl != nil {
		// A rate limit is expected behaviour in slow mode, not a fault: the
		// scheduler refusing (Queued) logs at Info, a real upstream 429 at
		// Warn (it is also the free calibration signal — the message carries
		// the rejected query's exact cost). Both tell the client how long to
		// wait, as a header and in the body.
		level := slog.LevelWarn
		if rl.Queued {
			level = slog.LevelInfo
		}
		s.logger.Log(r.Context(), level, "rate limited", "path", r.URL.Path, "query", r.URL.RawQuery,
			"lane", rl.Lane.String(), "queued", rl.Queued, "retryAfter", rl.RetryAfter,
			"pointValue", rl.PointValue, "remaining", rl.Remaining)
		secs := int64(math.Ceil(rl.RetryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
		writeJSON(w, status, errorResponse{Error: msg, Code: codeForStatus(status), RetryAfterMs: rl.RetryAfter.Milliseconds()})
		return
	}
	s.logger.Error("handler error", "path", r.URL.Path, "query", r.URL.RawQuery, "status", status, "err", err)
	writeError(w, status, msg)
}

// safeErrorMessage maps an HTTP status to a generic, client-safe message. It
// conveys the failure category without echoing any internal error text.
func safeErrorMessage(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication required"
	case http.StatusForbidden:
		return "you do not have access to this resource"
	case http.StatusTooManyRequests:
		return "rate limited; please retry shortly"
	case http.StatusGatewayTimeout:
		return "upstream request timed out"
	default:
		return "upstream service error"
	}
}

// handleAPINotFound returns a JSON 404 for unmatched /api/* paths so API
// clients never receive the SPA's index.html in place of an error envelope.
func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "no such API endpoint: "+r.URL.Path)
}
