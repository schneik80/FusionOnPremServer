package server

import (
	"net/http"
	"testing"
)

// The status→code table is a wire contract with the SPA's errors catalog —
// lock the categories the frontend localizes outright.
func TestCodeForStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:          "invalid_request",
		http.StatusUnauthorized:        "unauthorized",
		http.StatusForbidden:           "forbidden",
		http.StatusNotFound:            "not_found",
		http.StatusConflict:            "conflict",
		http.StatusTooManyRequests:     "rate_limited",
		http.StatusServiceUnavailable:  "service_unavailable",
		http.StatusGatewayTimeout:      "upstream_timeout",
		http.StatusBadGateway:          "upstream_failed",
		http.StatusInternalServerError: "server_error",
		http.StatusOK:                  "",
		http.StatusCreated:             "",
	}
	for status, want := range cases {
		if got := codeForStatus(status); got != want {
			t.Errorf("codeForStatus(%d) = %q, want %q", status, got, want)
		}
	}
}
