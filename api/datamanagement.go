package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The Data Management API (data/v1) resolves an item's tip *version* URN, which
// the Model Derivative API then renders into a preview thumbnail. Fusion Team
// hubs expose no MFGDM `binary` field and Fusion composite docs (e.g. .f2d
// drawings) have no downloadable OSS storage, so DM is only used to map an item
// lineage id -> its current version URN. All calls use the same bearer token and
// the data:read scope the app already holds, on the same host as MFGDM, so we
// reuse httpClient.
//
// dmBaseURL is a var (not const) only so tests can point it at an httptest
// server; production never reassigns it.
var dmBaseURL = "https://developer.api.autodesk.com"

// dmBaseURLForTest overrides the Data Management base URL and returns a restore
// func. Test-only.
func dmBaseURLForTest(u string) func() {
	old := dmBaseURL
	dmBaseURL = u
	return func() { dmBaseURL = old }
}

// dmEscape percent-encodes a URN for use as a single path segment, matching
// JavaScript's encodeURIComponent (which APS examples use): ':' and '?' and '='
// in version URNs must be escaped. url.PathEscape leaves ':' unescaped, so we
// use QueryEscape (URNs contain no spaces, so the '+'-for-space quirk is moot).
func dmEscape(s string) string { return url.QueryEscape(s) }

// dmGet performs an authenticated GET against the Data Management API and
// returns the response body, failing on non-2xx.
func dmGet(ctx context.Context, token, fullURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// The cap is a runaway-response guard, not a working budget: one JSON:API
	// contents page (up to 200 entries plus their `included` versions) can top
	// 1 MiB for a big folder, and truncating it surfaces as a baffling
	// "unexpected end of JSON input" — so leave generous headroom.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("DM GET %s -> HTTP %d: %s", trimURL(fullURL), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// noRedirectClient shares httpClient's connection pool but stops at the first
// redirect instead of following it. The archive-generation job (archive.go)
// signals completion with a 303 whose Location names the finished download —
// following it would throw away the one bit we are polling for.
var noRedirectClient = &http.Client{
	Transport: httpClient.Transport,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// GetItemTipVersion returns the Data Management tip *version* URN for an item
// lineage id (urn:adsk.wipprod:dm.lineage:…). This is exactly what the Model
// Derivative API needs to render a thumbnail (see GetVersionThumbnail).
func GetItemTipVersion(ctx context.Context, token, dmProjectID, itemID string) (string, error) {
	if dmProjectID == "" || itemID == "" {
		return "", fmt.Errorf("item tip: empty project or item")
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/items/%s/tip", dmBaseURL, dmEscape(dmProjectID), dmEscape(itemID))
	body, err := dmGet(ctx, token, u)
	if err != nil {
		return "", fmt.Errorf("item tip: %w", err)
	}
	var doc struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("item tip decode: %w", err)
	}
	if doc.Data.ID == "" {
		return "", fmt.Errorf("item tip: no version id in response")
	}
	return doc.Data.ID, nil
}

// trimURL strips the query string from a URL for error messages (signed URLs
// and tokens must never be logged).
func trimURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}
