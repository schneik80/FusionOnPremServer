package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/testutil"
)

func TestSetRegion(t *testing.T) {
	orig := region
	t.Cleanup(func() { region = orig })

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty stays empty", input: "", want: ""},
		{name: "US is normalized to empty", input: "US", want: ""},
		{name: "EMEA is preserved", input: "EMEA", want: "EMEA"},
		{name: "AUS is preserved", input: "AUS", want: "AUS"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SetRegion(tc.input)
			if region != tc.want {
				t.Errorf("SetRegion(%q): region = %q, want %q", tc.input, region, tc.want)
			}
		})
	}
}

// swapEndpoint redirects the package-level graphqlEndpoint to url and
// schedules restoration via t.Cleanup. Tests use this to point the GraphQL
// client at an httptest.Server.
func swapEndpoint(t *testing.T, url string) {
	t.Helper()
	prev, prevV3 := graphqlEndpoint, graphqlEndpointV3
	t.Cleanup(func() { graphqlEndpoint, graphqlEndpointV3 = prev, prevV3 })
	// Both endpoints go to the one fake: a test that must tell them apart
	// asserts on the query text (a v3 query selects `history`).
	graphqlEndpoint, graphqlEndpointV3 = url, url
}

func TestGqlQuery_HappyPath(t *testing.T) {
	var sawAuth, sawQuery, sawFoo bool
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		if req.AuthHeader == "Bearer test-token" {
			sawAuth = true
		} else {
			t.Errorf("AuthHeader = %q, want %q", req.AuthHeader, "Bearer test-token")
		}
		if strings.Contains(req.Query, "Marker") {
			sawQuery = true
		} else {
			t.Errorf("Query missing marker: %q", req.Query)
		}
		if v, ok := req.Variables["foo"].(string); ok && v == "bar" {
			sawFoo = true
		} else {
			t.Errorf("Variables[foo] = %v, want \"bar\"", req.Variables["foo"])
		}
		return testutil.GraphQLResponse{Data: map[string]any{"x": 1}}
	})
	swapEndpoint(t, srv.URL)

	ctx := context.Background()
	raw, err := gqlQuery(ctx, "test-token", "query Marker { hubs { id } }", map[string]any{"foo": "bar"})
	if err != nil {
		t.Fatalf("gqlQuery returned error: %v", err)
	}

	var got map[string]int
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decoding raw data: %v (raw=%s)", err, raw)
	}
	if got["x"] != 1 {
		t.Errorf("decoded data = %v, want {x:1}", got)
	}
	if !sawAuth || !sawQuery || !sawFoo {
		t.Errorf("handler missed an assertion: auth=%v query=%v foo=%v", sawAuth, sawQuery, sawFoo)
	}
}

func TestGqlQuery_401_Wraps(t *testing.T) {
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Status: 401}
	})
	swapEndpoint(t, srv.URL)

	_, err := gqlQuery(context.Background(), "tok", "query Q {}", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
		t.Errorf("error = %q, want substring \"unauthorized\"", err.Error())
	}
}

func TestGqlQuery_GraphQLErrors_Joined(t *testing.T) {
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Errors: []string{"first failure", "second failure"}}
	})
	swapEndpoint(t, srv.URL)

	_, err := gqlQuery(context.Background(), "tok", "query Q {}", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "first failure; second failure") {
		t.Errorf("error = %q, want both messages joined by \"; \"", msg)
	}
}

func TestGqlQuery_EmptyData_Errors(t *testing.T) {
	// A response with no "data" field at all leaves gr.Data as a zero-length
	// json.RawMessage, which trips the production code's len(gr.Data) == 0
	// guard. (Strings like `""` decode to a 2-byte RawMessage and would
	// pass that check.)
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{RawBody: `{}`}
	})
	swapEndpoint(t, srv.URL)

	_, err := gqlQuery(context.Background(), "tok", "query Q {}", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "empty") {
		t.Errorf("error = %q, want substring \"empty\"", err.Error())
	}
}

func TestGqlQuery_RetriesOnUnknownErrorType(t *testing.T) {
	// Temporarily collapse the retry backoffs so the test runs instantly.
	prev := retryBackoffs
	retryBackoffs = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryBackoffs = prev })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			// First two attempts: the APS gateway's flaky-NOT_FOUND shape.
			_, _ = io.WriteString(w, `{"data":{"hub":{"projects":null}},"errors":[{"message":"Requested resource not found.","extensions":{"code":"NOT_FOUND","errorType":"UNKNOWN"}}]}`)
			return
		}
		// Third attempt: success.
		_, _ = io.WriteString(w, `{"data":{"x":1}}`)
	}))
	t.Cleanup(srv.Close)
	swapEndpoint(t, srv.URL)

	raw, err := gqlQuery(context.Background(), "tok", "query Q {}", nil)
	if err != nil {
		t.Fatalf("gqlQuery: expected success after retry, got %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("call count = %d, want 3 (1 initial + 2 retries)", got)
	}
	var got map[string]int
	if err := json.Unmarshal(raw, &got); err != nil || got["x"] != 1 {
		t.Errorf("decoded data = %v err=%v, want {x:1}", got, err)
	}
}

// TestGqlQuery_DeepFieldErrorReturnsData covers a real APS pattern: the
// data is fully populated, but a deeply nested field on a few rows can't
// be resolved (e.g. owning project was deactivated). The errors carry
// errorType:UNKNOWN with deep paths. We must NOT retry (the next attempt
// returns the same partial response), and we MUST surface the data so
// the calling tab can render the usable rows.
func TestGqlQuery_DeepFieldErrorReturnsData(t *testing.T) {
	prev := retryBackoffs
	retryBackoffs = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryBackoffs = prev })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Data has real content; errors point at a deep leaf field.
		_, _ = io.WriteString(w, `{
			"data": { "x": [{"y": 1}, {"y": null}] },
			"errors": [{
				"message": "deactivated project",
				"path": ["x", 1, "y", "fusionWebUrl"],
				"extensions": { "code": "EXCEPTION_DISABLED_COMMUNITY", "errorType": "UNKNOWN" }
			}]
		}`)
	}))
	t.Cleanup(srv.Close)
	swapEndpoint(t, srv.URL)

	raw, err := gqlQuery(context.Background(), "tok", "query Q {}", nil)
	if err != nil {
		t.Fatalf("expected no error (data should be passed through), got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("call count = %d, want 1 (no retry on deep-field error)", got)
	}
	if !strings.Contains(string(raw), `"x"`) {
		t.Errorf("data not surfaced: %s", raw)
	}
}

// TestGqlQuery_ShallowFieldErrorReturnsData covers the MFG gateway's flaky
// item.fusionWebUrl: a depth-2 leaf error (path ["item","fusionWebUrl"]) on an
// otherwise fully-populated item. The whole item's sibling data is usable, so
// we must surface it and NOT retry — the old len(path) <= 2 heuristic wrongly
// retried this and failed the whole call.
func TestGqlQuery_ShallowFieldErrorReturnsData(t *testing.T) {
	prev := retryBackoffs
	retryBackoffs = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryBackoffs = prev })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"data": { "item": { "id": "I1", "name": "Cylinder Body", "fusionWebUrl": null } },
			"errors": [{
				"message": "Internal Server Error",
				"path": ["item", "fusionWebUrl"],
				"extensions": { "code": "INTERNAL_SERVER_ERROR", "errorType": "UNKNOWN" }
			}]
		}`)
	}))
	t.Cleanup(srv.Close)
	swapEndpoint(t, srv.URL)

	raw, err := gqlQuery(context.Background(), "tok", "query Q {}", nil)
	if err != nil {
		t.Fatalf("expected no error (data should pass through), got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("call count = %d, want 1 (no retry on shallow leaf error)", got)
	}
	if !strings.Contains(string(raw), `"Cylinder Body"`) {
		t.Errorf("usable data not surfaced: %s", raw)
	}
}

func TestGqlQuery_NoRetryOnConcreteError(t *testing.T) {
	// errorType not "UNKNOWN" → must not retry.
	prev := retryBackoffs
	retryBackoffs = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryBackoffs = prev })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errors":[{"message":"bad input","extensions":{"code":"BAD_USER_INPUT","errorType":"VALIDATION"}}]}`)
	}))
	t.Cleanup(srv.Close)
	swapEndpoint(t, srv.URL)

	if _, err := gqlQuery(context.Background(), "tok", "query Q {}", nil); err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("call count = %d, want 1 (no retries on concrete error)", got)
	}
}

// TestGqlQuery_429_FailsFast_NoRetry verifies a rate-limit (429) is NOT retried:
// the per-minute query-point quota can't replenish inside the flakiness window,
// so a retry only spends more of it. The call must hit the server exactly once
// and surface a distinct, mappable error.
func TestGqlQuery_429_FailsFast_NoRetry(t *testing.T) {
	prev := retryBackoffs
	retryBackoffs = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryBackoffs = prev })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"errors":[{"message":"Query point value per minute quota exceeded","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`)
	}))
	t.Cleanup(srv.Close)
	swapEndpoint(t, srv.URL)

	_, err := gqlQuery(context.Background(), "tok", "query Q {}", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("call count = %d, want 1 (429 must not be retried)", got)
	}
	msg := err.Error()
	if !strings.Contains(msg, "429") || !strings.Contains(strings.ToLower(msg), "rate limited") {
		t.Errorf("error = %q, want it to mention 429 and rate limiting", msg)
	}
	if !strings.Contains(msg, "Retry-After: 42") {
		t.Errorf("error = %q, want it to surface Retry-After", msg)
	}
}

func TestGqlQuery_RegionHeader(t *testing.T) {
	// Region is shared global state — back up & restore around the whole test.
	origRegion := region
	t.Cleanup(func() { region = origRegion })

	cases := []struct {
		name       string
		setRegion  string
		wantRegion string
	}{
		{name: "with_region", setRegion: "EMEA", wantRegion: "EMEA"},
		{name: "without_region", setRegion: "", wantRegion: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen atomic.Value // string
			seen.Store("")
			srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
				seen.Store(req.Region)
				return testutil.GraphQLResponse{Data: map[string]any{"ok": true}}
			})
			swapEndpoint(t, srv.URL)

			SetRegion(tc.setRegion)

			if _, err := gqlQuery(context.Background(), "tok", "query Q {}", nil); err != nil {
				t.Fatalf("gqlQuery: %v", err)
			}
			if got := seen.Load().(string); got != tc.wantRegion {
				t.Errorf("X-Ads-Region = %q, want %q", got, tc.wantRegion)
			}
		})
	}
}
