package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// TestDmDo_429_IsTypedOnRESTLane verifies a Data Management 429 surfaces as
// the typed RateLimitError on the REST lane with the platform's Retry-After,
// wrapped so the calling helper's context survives errors.As.
func TestDmDo_429_IsTypedOnRESTLane(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "25")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"developerMessage":"Quota limit exceeded."}`)
	}))
	t.Cleanup(srv.Close)
	defer dmBaseURLForTest(srv.URL)()

	_, err := dmGet(context.Background(), "tok", srv.URL+"/data/v1/projects/p/items/i")
	var rl *apsbudget.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("error %v (%T) is not a RateLimitError", err, err)
	}
	if rl.Lane != apsbudget.LaneREST || rl.RetryAfter != 25*time.Second || rl.PointValue != 0 {
		t.Errorf("typed 429 = %+v", rl)
	}
}

// TestDmDo_NonOK_KeepsMethodAndStatus pins the error text shape the wiki and
// archive paths already match on.
func TestDmDo_NonOK_KeepsMethodAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.Header.Get("Content-Type") != "application/vnd.api+json" {
			t.Errorf("method/content-type = %s %q", r.Method, r.Header.Get("Content-Type"))
		}
		http.Error(w, "nope", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	defer dmBaseURLForTest(srv.URL)()

	_, err := dmPatch(context.Background(), "tok", srv.URL+"/x", []byte(`{}`))
	if err == nil || !contains(err.Error(), "DM PATCH") || !contains(err.Error(), "HTTP 403") {
		t.Errorf("err = %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (sub == "" || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
