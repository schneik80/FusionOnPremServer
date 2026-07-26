package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// errorResponse is the uniform error envelope every failing endpoint returns.
// Code is a stable machine token the SPA maps to localized text; Error stays
// the human-readable (English) detail and the fallback for unmapped codes.
type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// writeJSON serialises v as JSON with the given status code. Encoding happens
// after WriteHeader, so a mid-encode error can't change the already-sent
// status; such errors are vanishingly rare for our DTOs and are dropped.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends the uniform error envelope with the given status. The
// code derives from the status: category-level statuses (auth, rate limit,
// upstream) get catalog-mapped codes the SPA localizes outright, while
// invalid_request/not_found deliberately stay OUT of the SPA catalog so
// their specific store messages ("end date is before start date") surface
// verbatim rather than being flattened to a generic sentence.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg, Code: codeForStatus(status)})
}

// writeErrorCode sends the error envelope with an explicit machine code,
// for errors whose code is more specific than the status-derived one (the
// hub session lock: 409 hub_not_selected, 403 hub_mismatch).
func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorResponse{Error: msg, Code: code})
}

// codeForStatus is the status→code table for the error envelope. Stable —
// the SPA's errors catalog keys off these tokens.
func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusGatewayTimeout:
		return "upstream_timeout"
	case http.StatusBadGateway:
		return "upstream_failed"
	default:
		if status >= 500 {
			return "server_error"
		}
		return ""
	}
}

// statusForError maps an api/auth error to an HTTP status code. The wrapped
// APS layer returns plain fmt.Errorf chains, so detection leans on a couple of
// well-known markers; everything else is treated as an upstream (502) failure
// since the data ultimately comes from the APS GraphQL gateway.
func statusForError(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		// Client went away; no useful status, but 499-style isn't standard.
		return http.StatusGatewayTimeout
	case strings.Contains(err.Error(), "HTTP 401"), strings.Contains(err.Error(), "unauthorized"):
		return http.StatusUnauthorized
	case strings.Contains(err.Error(), "HTTP 429"),
		strings.Contains(err.Error(), "rate limited"),
		strings.Contains(err.Error(), "quota exceeded"):
		return http.StatusTooManyRequests
	case strings.Contains(err.Error(), "Access Denied"),
		strings.Contains(err.Error(), "does not have permission"),
		strings.Contains(err.Error(), "Forbidden"):
		return http.StatusForbidden
	default:
		return http.StatusBadGateway
	}
}
