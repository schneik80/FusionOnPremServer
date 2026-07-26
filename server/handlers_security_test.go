package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schneik80/fusionlocalserver/pins"
)

// --- Constraint #3: internal errors must not leak to the client ------------

// fail() logs the full error but must return only a generic, status-keyed
// message. A regression that pipes err.Error() back to the client (as the old
// code did) would surface the sensitive substring and fail this test.
func TestFailDoesNotLeakInternalError(t *testing.T) {
	s := &Server{logger: quietLogger()}

	// An error chain of the kind the APS/GraphQL layer produces: it embeds an
	// internal URL and a raw upstream body — none of which may reach the client.
	secret := "https://developer.api.autodesk.com/internal/graphql leaked-body {\"detail\":\"stack trace\"}"
	err := errors.New("token request failed (HTTP 502): " + secret)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/items/details?itemId=x", nil)
	s.fail(rec, req, err)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if strings.Contains(rec.Body.String(), "leaked-body") ||
		strings.Contains(rec.Body.String(), "developer.api.autodesk.com") ||
		strings.Contains(rec.Body.String(), "stack trace") {
		t.Errorf("client response leaked internal error detail: %q", rec.Body.String())
	}
	if body.Error != "upstream service error" {
		t.Errorf("client message = %q, want generic %q", body.Error, "upstream service error")
	}
}

// Under -v (Verbose), fail() appends the detailed error to the client message
// as a developer-run diagnostic. This is the opt-in escape hatch for the leak
// guard above; the two tests together pin both halves of the contract.
func TestFailIncludesDetailWhenVerbose(t *testing.T) {
	s := &Server{logger: quietLogger(), opts: Options{Verbose: true}}

	err := errors.New("token request failed (HTTP 502): upstream-detail-xyz")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/items/details?itemId=x", nil)
	s.fail(rec, req, err)

	var body struct {
		Error string `json:"error"`
	}
	if jerr := json.Unmarshal(rec.Body.Bytes(), &body); jerr != nil {
		t.Fatalf("response not JSON: %v", jerr)
	}
	if !strings.HasPrefix(body.Error, "upstream service error: ") ||
		!strings.Contains(body.Error, "upstream-detail-xyz") {
		t.Errorf("verbose message = %q, want generic prefix + detail", body.Error)
	}
}

func TestSafeErrorMessageByStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        "authentication required",
		http.StatusForbidden:           "you do not have access to this resource",
		http.StatusTooManyRequests:     "rate limited; please retry shortly",
		http.StatusGatewayTimeout:      "upstream request timed out",
		http.StatusBadGateway:          "upstream service error",
		http.StatusInternalServerError: "upstream service error", // default
	}
	for status, want := range cases {
		if got := safeErrorMessage(status); got != want {
			t.Errorf("safeErrorMessage(%d) = %q, want %q", status, got, want)
		}
	}
}

// --- Constraint #1: malformed / oversized payloads are rejected ------------

// withTempHome points the pins package's on-disk store at a throwaway dir so
// the handler tests don't touch the real home directory.
func withTempHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "fusionlocalserver"), 0700); err != nil {
		t.Fatalf("setup config dir: %v", err)
	}
}

func TestPinsAddRejectsMalformedJSON(t *testing.T) {
	withTempHome(t)
	s := &Server{logger: quietLogger()}

	rec := httptest.NewRecorder()
	req := withHubCtx(httptest.NewRequest(http.MethodPost, "/api/pins",
		strings.NewReader(`{"id": "x", "kind": `)), &storeSet{hubID: "h1"}) // truncated JSON
	s.handlePinsAdd(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	// The decode error text must not be echoed back (no "looking for beginning
	// of value" etc.) — only the generic message.
	if !strings.Contains(rec.Body.String(), "invalid pin body") ||
		strings.Contains(rec.Body.String(), "looking for") {
		t.Errorf("unexpected body: %q", rec.Body.String())
	}
}

func TestPinsAddRejectsOversizedBody(t *testing.T) {
	withTempHome(t)
	s := &Server{logger: quietLogger()}

	// 128 KiB of padding inside an otherwise-valid pin — over the 64 KiB cap.
	huge := `{"id":"x","kind":"design","name":"` + strings.Repeat("A", 128<<10) + `"}`
	rec := httptest.NewRecorder()
	req := withHubCtx(httptest.NewRequest(http.MethodPost, "/api/pins", strings.NewReader(huge)), &storeSet{hubID: "h1"})
	s.handlePinsAdd(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body should exceed MaxBytesReader cap)", rec.Code)
	}
}

func TestPinsAddRejectsUnpinnableKind(t *testing.T) {
	withTempHome(t)
	s := &Server{logger: quietLogger()}

	rec := httptest.NewRecorder()
	req := withHubCtx(httptest.NewRequest(http.MethodPost, "/api/pins",
		strings.NewReader(`{"id":"x","kind":"bogus"}`)), &storeSet{hubID: "h1"})
	s.handlePinsAdd(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- Constraint #2: there is no SQL/exec sink; injection payloads are inert -
//
// This codebase has no database and no shell-out, so a classic SQLi/command-
// injection test has no sink to probe. The honest equivalent is to prove that
// an injection-style payload is treated as opaque data end-to-end: a pin id
// laden with SQL/shell metacharacters is stored and read back byte-for-byte,
// never interpreted. If a database or exec call is ever introduced, this test
// (and the round-trip it asserts) is where a parameterization regression would
// first surface.
func TestInjectionPayloadIsStoredVerbatim(t *testing.T) {
	withTempHome(t)
	s := &Server{logger: quietLogger()}

	payload := `'; DROP TABLE pins;-- $(rm -rf /) <script>alert(1)</script>`
	pin := pins.Pin{ID: payload, Kind: "design", Name: "x"}
	bodyBytes, _ := json.Marshal(pin)

	rec := httptest.NewRecorder()
	req := withHubCtx(httptest.NewRequest(http.MethodPost, "/api/pins", strings.NewReader(string(bodyBytes))), &storeSet{hubID: "h1"})
	s.handlePinsAdd(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	var stored []pins.Pin
	if err := json.Unmarshal(rec.Body.Bytes(), &stored); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if len(stored) != 1 || stored[0].ID != payload {
		t.Fatalf("payload not round-tripped verbatim: %+v", stored)
	}
}
