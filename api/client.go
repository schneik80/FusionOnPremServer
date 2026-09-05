package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// graphqlEndpoint is a var (not const) so tests can point it at an
// httptest.Server. Production code never reassigns it.
var graphqlEndpoint = "https://developer.api.autodesk.com/mfg/graphql"

// graphqlEndpointV3 is the v3 ("Collaborative Editing") Manufacturing Data
// Model endpoint. The app is a v2 app — every listing, detail, reference and
// thumbnail query goes to graphqlEndpoint — and reaches for v3 only where v2
// has no data at all: a design's non-save HISTORY (api/history.go). The two
// schemas share the item/hub id space and the bearer token, so a v3 query is
// the same request to a different URL. v3 resolves only on Collaborative
// Editing hubs (hubDataVersion major ≥ 2); on an older hub the v3 query fails
// and the feature that asked degrades, nothing else is affected.
var graphqlEndpointV3 = "https://developer.api.autodesk.com/mfg/v3/graphql/public"

// region is the X-Ads-Region header value sent with every request.
// Empty means no header is sent (defaults to US on the server side).
var region string

// httpClient is the shared HTTP client used for every APS request.
// A single client with a tuned transport keeps connections alive across
// pagination and rapid navigation; per-call timeouts come from the caller's
// context (so streaming downloads aren't capped by a global Client.Timeout).
var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// SetRegion configures the ADS region header (e.g. "EMEA", "AUS").
// Call this once at startup from the config; an empty string or "US" sends no header.
func SetRegion(r string) {
	if r == "US" {
		r = ""
	}
	region = r
}

// SetGraphqlEndpointForTesting overrides the GraphQL endpoint URL and
// returns a function that restores the prior value. Intended only for
// tests that need to point the api package at an httptest.Server from a
// different test package (notably ui flow tests that drive a tea.Cmd which
// internally calls into api). Production code MUST NOT call this.
func SetGraphqlEndpointForTesting(url string) (restore func()) {
	prev := graphqlEndpoint
	graphqlEndpoint = url
	return func() { graphqlEndpoint = prev }
}

// NavItem is a navigable node in the APS Manufacturing Data Model hierarchy.
type NavItem struct {
	ID          string
	Name        string
	Kind        string // "hub" | "project" | "folder" | "design" | "configured" | "unknown"
	AltID       string // alternativeIdentifier (data management API ID)
	WebURL      string // direct web URL when provided by the API
	IsContainer bool   // true if this item can be entered (hub, project, folder)

	// ComponentVersionID is the lineage id of the design's
	// tipRootComponentVersion, captured at items-list time so the
	// asynchronous assembly classifier can issue its occurrences probe
	// without a second per-row round-trip. Populated only for Kind ==
	// "design"; empty for everything else.
	ComponentVersionID string

	// Subtype refines Kind for designs after classification: "assembly"
	// when the design has at least one sub-component, "part" when it
	// has none, "" while the classification is still in flight (or for
	// non-design rows). The Contents-column renderer reads this to
	// pick the icon; an unclassified design falls back to the generic
	// design icon until the refinement message lands.
	Subtype string

	// ModifiedOn is the item's last-modified time, captured at items-list time so
	// the Project Dashboard can list recently-modified items without a per-row
	// fetch. Populated for items (itemsByProject / itemsByFolder); zero for
	// folders (the Folder type's modified timestamp isn't surfaced).
	ModifiedOn time.Time
}

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type gqlError struct {
	Message string `json:"message"`
	// Path is the location of the error in the GraphQL response. Per spec
	// it is a list of strings (field names) and ints (array indices), so
	// we decode as []any. Its presence distinguishes a localized field-level
	// failure (the data still has usable content) from a path-less, non-
	// localized gateway/root failure (retry-worthy).
	Path       []any `json:"path"`
	Extensions struct {
		Code      string `json:"code"`
		ErrorType string `json:"errorType"`
	} `json:"extensions"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// retryBackoffs is the delay before each retry attempt (the first attempt
// has no preceding delay, hence the leading 0). APS's MFG GraphQL gateway
// returns intermittent NOT_FOUND with errorType:UNKNOWN on valid hub URNs;
// see ~/Documents/aps-mfg-graphql-flakiness.md. Two retries with these
// delays absorb the observed flakiness while keeping total worst-case
// added latency under 2 s and well inside the 30 s nav-cmd context.
var retryBackoffs = []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond}

// gqlQuery runs q against the v2 endpoint — the app's default.
func gqlQuery(ctx context.Context, token, q string, vars map[string]any) (json.RawMessage, error) {
	return gqlQueryAt(ctx, graphqlEndpoint, token, q, vars)
}

// gqlQueryV3 runs q against the v3 endpoint. Same token, same retry and 429
// posture, same partial-data handling; only the schema differs.
func gqlQueryV3(ctx context.Context, token, q string, vars map[string]any) (json.RawMessage, error) {
	return gqlQueryAt(ctx, graphqlEndpointV3, token, q, vars)
}

func gqlQueryAt(ctx context.Context, endpoint, token, q string, vars map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(gqlRequest{Query: q, Variables: vars})
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt, delay := range retryBackoffs {
		if delay > 0 {
			dbgLog("RETRY attempt=%d delay=%s lastErr=%v", attempt+1, delay, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		data, err, retriable := gqlQueryOnce(ctx, endpoint, token, body, vars)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !retriable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("APS GraphQL flaky after %d attempts: %w", len(retryBackoffs), lastErr)
}

// gqlQueryOnce performs a single HTTP round-trip. The third return value
// reports whether the error class is worth retrying — true for transport
// errors, HTTP 5xx/408/429, and GraphQL errors carrying
// extensions.errorType="UNKNOWN" (APS gateway's marker for intermittent
// upstream failures). False for HTTP 401, parse errors, and concrete
// GraphQL errors.
func gqlQueryOnce(ctx context.Context, endpoint, token string, body []byte, vars map[string]any) (json.RawMessage, error, bool) {
	dbgLog("REQUEST %s vars=%v\n%s", endpoint, vars, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if region != "" {
		req.Header.Set("X-Ads-Region", region)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err, true
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err, true
	}

	dbgLog("RESPONSE HTTP %d\n%s", resp.StatusCode, raw)

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("unauthorized (HTTP 401) — token may be expired or lacks scope/entitlement; body: %s", raw), false
	}
	// 429 = rate limited (APS's per-minute, cost-based query-point quota).
	// Retrying inside the flakiness window cannot replenish a per-minute quota
	// and only spends more of it — so fail fast (non-retriable) with a distinct
	// error the handler maps to a 429. Surface Retry-After when present so the
	// client can back off intelligently.
	if resp.StatusCode == 429 {
		retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After"))
		suffix := ""
		if retryAfter != "" {
			suffix = fmt.Sprintf(" (Retry-After: %s)", retryAfter)
		}
		return nil, fmt.Errorf("HTTP 429 rate limited%s: %s", suffix, raw), false
	}
	// 5xx/408 are transient gateway hiccups — worth a quick retry.
	if resp.StatusCode >= 500 || resp.StatusCode == 408 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw), true
	}

	var gr gqlResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return nil, fmt.Errorf("parsing GraphQL response: %w", err), false
	}
	hasData := len(gr.Data) > 0 && string(gr.Data) != "null"
	if len(gr.Errors) > 0 {
		msgs := make([]string, len(gr.Errors))
		// Retry only a path-less UNKNOWN error: that is a non-localized
		// (gateway/root) failure where the result is unusable even when the data
		// envelope is present (e.g. {"hub":{"projects":null}}). An error that
		// carries a path is field-level — even a shallow one like
		// ["item","fusionWebUrl"], which the MFG gateway intermittently 500s —
		// and leaves usable sibling data, so it is surfaced below rather than
		// retried (a retry just reproduces the same partial response).
		retriable := false
		for i, e := range gr.Errors {
			msgs[i] = e.Message
			if e.Extensions.ErrorType == "UNKNOWN" && len(e.Path) == 0 {
				retriable = true
			}
		}
		// Surface partial data: if the response still carries usable content and
		// we are not retrying, pass it through. The errors are still recorded in
		// the debug response dump above, so they are not lost.
		if !retriable && hasData {
			dbgLog("GraphQL partial errors (kept data): %s", strings.Join(msgs, "; "))
			return gr.Data, nil, false
		}
		return nil, fmt.Errorf("GraphQL errors: %s", strings.Join(msgs, "; ")), retriable
	}
	if !hasData {
		return nil, fmt.Errorf("empty GraphQL response (HTTP %d): %s", resp.StatusCode, raw), false
	}
	return gr.Data, nil, false
}
