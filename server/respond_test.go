package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// TestFail_RateLimited_SetsRetryAfter is the wire contract for slow mode: a
// typed 429 anywhere in the error chain becomes HTTP 429 + Retry-After (whole
// seconds, at least 1) + retryAfterMs in the envelope, with the catalog code.
func TestFail_RateLimited_SetsRetryAfter(t *testing.T) {
	s := &Server{logger: quietLogger()}
	cases := []struct {
		name     string
		err      error
		wantHdr  string
		wantMs   int64
		wantCode string
	}{
		{"upstream 42s", fmt.Errorf("wrapped: %w", &apsbudget.RateLimitError{RetryAfter: 42 * time.Second, PointValue: 231, Remaining: 69}), "42", 42000, "rate_limited"},
		{"queued 1500ms rounds up", &apsbudget.RateLimitError{RetryAfter: 1500 * time.Millisecond, Queued: true}, "2", 1500, "rate_limited"},
		{"no hint still at least 1s", &apsbudget.RateLimitError{}, "1", 0, "rate_limited"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/items/details?itemId=x", nil)
			s.fail(rec, req, c.err)
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429", rec.Code)
			}
			if got := rec.Header().Get("Retry-After"); got != c.wantHdr {
				t.Errorf("Retry-After = %q, want %q", got, c.wantHdr)
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body: %v", err)
			}
			if body.Code != c.wantCode || body.RetryAfterMs != c.wantMs {
				t.Errorf("body = %+v", body)
			}
		})
	}
}

// TestStatusForError_TypedBeforeSubstring: the typed errors win regardless of
// their text, and the too-complex 400 is an upstream (our) failure, not a 429.
func TestStatusForError_TypedBeforeSubstring(t *testing.T) {
	if got := statusForError(&apsbudget.RateLimitError{}); got != http.StatusTooManyRequests {
		t.Errorf("RateLimitError -> %d", got)
	}
	if got := statusForError(&apsbudget.QueryTooComplexError{Op: "GetItemDetails", Points: 1231, Max: 1000}); got != http.StatusBadGateway {
		t.Errorf("QueryTooComplexError -> %d", got)
	}
	if got := statusForError(errors.New("HTTP 429 rate limited: legacy text")); got != http.StatusTooManyRequests {
		t.Errorf("legacy substring -> %d", got)
	}
}
