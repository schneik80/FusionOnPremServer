package fusionmcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeProjectID(t *testing.T) {
	// A payload that base64-encodes differently in std vs URL-safe encoding.
	// "foo:bar#baz" -> bytes that map to a `+` or `/` in std encoding when
	// permuted. We pick a payload where std encoding differs from URL-safe.
	stdPayload := "\xfb\xff?foo#bar" // bytes guaranteed to produce + and / in std encoding
	stdEncoded := base64.RawStdEncoding.EncodeToString([]byte(stdPayload))
	urlEncoded := base64.RawURLEncoding.EncodeToString([]byte(stdPayload))
	if stdEncoded == urlEncoded {
		t.Fatalf("test setup: expected std and URL-safe encodings to differ, got %q == %q", stdEncoded, urlEncoded)
	}

	// Self-encoded payload for the documented assertion in the spec.
	selfPayload := "business:autodesk#12345"
	selfEncoded := "a." + base64.RawURLEncoding.EncodeToString([]byte(selfPayload))

	// "justatag" with no '#'
	noHash := "a." + base64.RawURLEncoding.EncodeToString([]byte("justatag"))

	// "foo#" - ends in '#' with no id
	endHash := "a." + base64.RawURLEncoding.EncodeToString([]byte("foo#"))

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "valid url-safe base64 from spec",
			in:   "a.YnVzaW5lc3M6YXV0b2Rlc2s4MDgzIzIwMjUwMjEzODc2NjAyNTMx",
			want: "20250213876602531",
		},
		{
			name: "self-encoded url-safe",
			in:   selfEncoded,
			want: "12345",
		},
		{
			name: "missing a. prefix",
			in:   "b.something",
			want: "",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "garbage base64",
			in:   "a.!!!notbase64!!!",
			want: "",
		},
		{
			name: "decoded payload missing #",
			in:   noHash,
			want: "",
		},
		{
			name: "decoded payload ends in # with no id",
			in:   endHash,
			want: "",
		},
		{
			name: "std-base64 fallback decodes when url-safe fails",
			in:   "a." + stdEncoded,
			want: "bar",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeProjectID(tc.in)
			if got != tc.want {
				t.Errorf("NormalizeProjectID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidFileID(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		// Accepted
		{"lineage urn", "urn:adsk.wipprod:dm.lineage:hC6gVxs6QYC6OWpnQNd7Ow", true},
		{"a. prefixed b64", "a.YnVzaW5lc3M6YXV0b2Rlc2s", true},
		{"plain alnum", "abc123", true},
		{"all allowed chars", "A-Z_0.9:colons", true},

		// Rejected
		{"empty", "", false},
		{"single space", " ", false},
		{"contains space", "foo bar", false},
		{"contains slash", "foo/bar", false},
		{"contains single quote", "foo'bar", false},
		{"contains double quote", "foo\"bar", false},
		{"contains backslash", "foo\\bar", false},
		{"contains newline", "foo\nbar", false},
		{"contains tab", "foo\tbar", false},
		{"unicode chars", "caractères", false},
		{"contains paren", "foo)bar", false},
		{"contains semicolon", "foo;bar", false},
		{"contains dollar", "foo$bar", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validFileID.MatchString(tc.input)
			if got != tc.want {
				t.Errorf("validFileID.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestBuildInsertScript_JSONEscaping(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"normal lineage urn", "urn:adsk.wipprod:dm.lineage:abc123"},
		{"plain", "plain"},
		{"empty", ""},
		{"with double quote", "with\"quote"},
		{"with backslash", "with\\backslash"},
		{"with newline", "with\nnewline"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := buildInsertScript(tc.input)

			marker := "file_id = "
			start := strings.Index(script, marker)
			if start < 0 {
				t.Fatalf("buildInsertScript output missing %q marker:\n%s", marker, script)
			}
			afterMarker := script[start+len(marker):]
			end := strings.Index(afterMarker, "\n")
			if end < 0 {
				t.Fatalf("buildInsertScript output missing newline after marker:\n%s", script)
			}
			found := afterMarker[:end]

			var out string
			if err := json.Unmarshal([]byte(found), &out); err != nil {
				t.Fatalf("found literal %q is not valid JSON string: %v", found, err)
			}
			if out != tc.input {
				t.Errorf("round-trip: decoded %q from script literal %q, want %q", out, found, tc.input)
			}
		})
	}
}

func TestParseToolErrorText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"success false with error", `{"success":false,"error":"oops"}`, "oops"},
		{"success false no error", `{"success":false}`, "tool reported failure"},
		{"success true", `{"success":true}`, ""},
		{"success true with error ignored", `{"success":true,"error":"ignored"}`, ""},
		{"plain text not json", "plain text not json", ""},
		{"json array starts with bracket", "[1,2,3]", ""},
		{"empty", "", ""},
		{"trim spaces around json", `  {"success":false,"error":"trim me"}  `, "trim me"},
		{"malformed json", "{garbage", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseToolErrorText(tc.in)
			if got != tc.want {
				t.Errorf("parseToolErrorText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractSSEData(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single data line", "data: {\"foo\":1}\n", `{"foo":1}`},
		{"multiple data lines concat", "data: {\"a\":1}\ndata: {\"b\":2}\n", `{"a":1}{"b":2}`},
		{"event prefix then data", "event: ping\ndata: {\"x\":true}\n\n", `{"x":true}`},
		{"crlf line endings", "data: {\"v\":1}\r\n", `{"v":1}`},
		{"no sse framing returns input", "plain body", "plain body"},
		{"empty returns empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(extractSSEData([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("extractSSEData(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// invalidFileIDCases lists fileId values that should be rejected by the public
// OpenDocument / InsertDocument validators *before* any network I/O happens.
// The tests below use a sentinel endpoint that would refuse to connect — so a
// connect-error in place of a validation-error is a regression.
var invalidFileIDCases = []struct {
	name string
	id   string
}{
	{"empty", ""},
	{"with space", "with space"},
	{"shell metachars", "foo;rm -rf"},
	{"single quote", "foo'bar"},
	{"newline", "with\nnewline"},
	{"unicode", "caractères"},
}

// tripwireClient returns a Client whose endpoint will refuse connections,
// so that any code path which tries to dial out fails the test.
func tripwireClient() *Client {
	return &Client{Endpoint: "http://127.0.0.1:1/should-not-be-called"}
}

func assertValidationRejection(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil (validation did not run)", name)
	}
	msg := err.Error()
	if !(strings.Contains(msg, "fileId") || strings.Contains(msg, "empty") || strings.Contains(msg, "invalid")) {
		t.Fatalf("%s: error %q does not look like a validation rejection — validation may not be running before dial", name, msg)
	}
}

func TestOpenDocument_ValidatesInput(t *testing.T) {
	client := tripwireClient()
	for _, tc := range invalidFileIDCases {
		t.Run(tc.name, func(t *testing.T) {
			err := client.OpenDocument(context.Background(), tc.id)
			assertValidationRejection(t, "OpenDocument("+tc.name+")", err)
		})
	}
}

func TestInsertDocument_ValidatesInput(t *testing.T) {
	client := tripwireClient()
	for _, tc := range invalidFileIDCases {
		t.Run(tc.name, func(t *testing.T) {
			err := client.InsertDocument(context.Background(), tc.id)
			assertValidationRejection(t, "InsertDocument("+tc.name+")", err)
		})
	}
}

// ----------------------------------------------------------------------------
// Phase 2 (L2) — httptest-mocked MCP server tests.
//
// These exercise the JSON-RPC + session-cache machinery in invoke / session /
// callTool against a faked MCP server (NewMCPServer). Each test
// constructs a fresh *Client so that cached session state from one test
// cannot leak into another.
// ----------------------------------------------------------------------------

// testCtx returns a 5-second-bounded context, freed via t.Cleanup.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestActiveHubProjects_Success(t *testing.T) {
	var capturedArgs map[string]any
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "sid-success",
		Tools: map[string]MCPHandler{
			"fusion_mcp_read": func(args map[string]any) MCPResponse {
				capturedArgs = args
				return MCPResponse{
					ContentText: `{"success": true, "projects": [{"id": "P1", "name": "Project One"}, {"id": "P2", "name": "Project Two"}]}`,
				}
			},
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	projects, err := client.ActiveHubProjects(testCtx(t))
	if err != nil {
		t.Fatalf("ActiveHubProjects: unexpected error: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("ActiveHubProjects: got %d projects, want 2 (%+v)", len(projects), projects)
	}
	if projects[0].Name != "Project One" || projects[1].Name != "Project Two" {
		t.Errorf("ActiveHubProjects: names = [%q,%q], want [\"Project One\",\"Project Two\"]",
			projects[0].Name, projects[1].Name)
	}
	if got := srv.InitCount(); got != 1 {
		t.Errorf("InitCount = %d, want 1", got)
	}
	if got := srv.CallCount("fusion_mcp_read"); got != 1 {
		t.Errorf("CallCount(fusion_mcp_read) = %d, want 1", got)
	}
	if capturedArgs == nil {
		t.Fatal("handler was not invoked (capturedArgs is nil)")
	}
	if got := capturedArgs["queryType"]; got != "projects" {
		t.Errorf("args[queryType] = %v, want \"projects\"", got)
	}
}

func TestActiveHubProjects_SuccessFalse(t *testing.T) {
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "sid-fail",
		Tools: map[string]MCPHandler{
			"fusion_mcp_read": func(args map[string]any) MCPResponse {
				return MCPResponse{
					ContentText: `{"success": false, "projects": []}`,
				}
			},
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	_, err := client.ActiveHubProjects(testCtx(t))
	if err == nil {
		t.Fatal("ActiveHubProjects: expected error for success:false, got nil")
	}
	// callTool's parseToolErrorText catches {success:false} (no error field)
	// and surfaces it as "tool reported failure". The per-method success
	// guard in ActiveHubProjects was removed because it was unreachable.
	if msg := err.Error(); !strings.Contains(msg, "tool reported failure") {
		t.Errorf("ActiveHubProjects: error %q does not contain %q", msg, "tool reported failure")
	}
}

func TestActiveHubProjects_SuccessFalse_WithErrorMsg(t *testing.T) {
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "sid-autherr",
		Tools: map[string]MCPHandler{
			"fusion_mcp_read": func(args map[string]any) MCPResponse {
				return MCPResponse{
					ContentText: `{"success": false, "error": "auth failed"}`,
				}
			},
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	_, err := client.ActiveHubProjects(testCtx(t))
	if err == nil {
		t.Fatal("ActiveHubProjects: expected error for success:false with error, got nil")
	}
	if !strings.Contains(err.Error(), "auth failed") {
		t.Errorf("ActiveHubProjects: error %q does not contain \"auth failed\"", err.Error())
	}
}

func TestOpenDocument_Roundtrip(t *testing.T) {
	var capturedArgs map[string]any
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "sid-open",
		Tools: map[string]MCPHandler{
			"fusion_mcp_execute": func(args map[string]any) MCPResponse {
				capturedArgs = args
				// Plain non-JSON text — parseToolErrorText returns "" because
				// it doesn't start with '{' or '['. Production treats as success.
				return MCPResponse{ContentText: "opened"}
			},
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	if err := client.OpenDocument(testCtx(t), "validfileid"); err != nil {
		t.Fatalf("OpenDocument: unexpected error: %v", err)
	}
	if capturedArgs == nil {
		t.Fatal("handler was not invoked (capturedArgs is nil)")
	}
	if got := capturedArgs["featureType"]; got != "document" {
		t.Errorf("args[featureType] = %v, want \"document\"", got)
	}
	obj, ok := capturedArgs["object"].(map[string]any)
	if !ok {
		t.Fatalf("args[object] is %T, want map[string]any", capturedArgs["object"])
	}
	if got := obj["operation"]; got != "open" {
		t.Errorf("args.object[operation] = %v, want \"open\"", got)
	}
	if got := obj["fileId"]; got != "validfileid" {
		t.Errorf("args.object[fileId] = %v, want \"validfileid\"", got)
	}
}

func TestInsertDocument_ScriptShape(t *testing.T) {
	var capturedArgs map[string]any
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "sid-insert",
		Tools: map[string]MCPHandler{
			"fusion_mcp_execute": func(args map[string]any) MCPResponse {
				capturedArgs = args
				return MCPResponse{ContentText: "inserted"}
			},
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	const fileID = "urn:adsk.wipprod:dm.lineage:abc"
	if err := client.InsertDocument(testCtx(t), fileID); err != nil {
		t.Fatalf("InsertDocument: unexpected error: %v", err)
	}
	if capturedArgs == nil {
		t.Fatal("handler was not invoked (capturedArgs is nil)")
	}
	if got := capturedArgs["featureType"]; got != "script" {
		t.Errorf("args[featureType] = %v, want \"script\"", got)
	}
	obj, ok := capturedArgs["object"].(map[string]any)
	if !ok {
		t.Fatalf("args[object] is %T, want map[string]any", capturedArgs["object"])
	}
	script, ok := obj["script"].(string)
	if !ok {
		t.Fatalf("args.object[script] is %T, want string", obj["script"])
	}
	if !strings.Contains(script, "file_id") {
		t.Errorf("script does not contain \"file_id\" variable name:\n%s", script)
	}
	// JSON-encoded fileId is the original URN wrapped in double quotes.
	wantQuoted := `"` + fileID + `"`
	if !strings.Contains(script, wantQuoted) {
		t.Errorf("script does not contain JSON-encoded fileId %s:\n%s", wantQuoted, script)
	}
}

func TestInvoke_SessionRetryOn404(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	handler := func(args map[string]any) MCPResponse {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return MCPResponse{SessionExpired: true}
		}
		return MCPResponse{ContentText: `{"success":true,"projects":[]}`}
	}
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "sid-1",
		Tools: map[string]MCPHandler{
			"fusion_mcp_read": handler,
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	projects, err := client.ActiveHubProjects(testCtx(t))
	if err != nil {
		t.Fatalf("ActiveHubProjects: unexpected error: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects after retry, got %d (%+v)", len(projects), projects)
	}
	if got := srv.InitCount(); got != 2 {
		t.Errorf("InitCount = %d, want 2 (initial + re-handshake after 404)", got)
	}
	if got := srv.CallCount("fusion_mcp_read"); got != 2 {
		t.Errorf("CallCount(fusion_mcp_read) = %d, want 2 (one 404 + one success)", got)
	}
	sids := srv.SessionIDsSeen()
	if len(sids) != 2 {
		t.Fatalf("SessionIDsSeen len = %d, want 2 (got %v)", len(sids), sids)
	}
	for i, s := range sids {
		if s != "sid-1" {
			t.Errorf("SessionIDsSeen[%d] = %q, want \"sid-1\"", i, s)
		}
	}
}

func TestInvoke_SSEResponse(t *testing.T) {
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "sid-sse",
		SSEMode:   true,
		Tools: map[string]MCPHandler{
			"fusion_mcp_read": func(args map[string]any) MCPResponse {
				return MCPResponse{
					ContentText: `{"success":true,"projects":[{"id":"P1","name":"Solo"}]}`,
				}
			},
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	projects, err := client.ActiveHubProjects(testCtx(t))
	if err != nil {
		t.Fatalf("ActiveHubProjects (SSE): unexpected error: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("ActiveHubProjects (SSE): got %d projects, want 1 (%+v)", len(projects), projects)
	}
	if projects[0].Name != "Solo" {
		t.Errorf("ActiveHubProjects (SSE): name = %q, want \"Solo\"", projects[0].Name)
	}
}

func TestSession_StatelessMode(t *testing.T) {
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "", // stateless: server emits no Mcp-Session-Id header on init
		Tools: map[string]MCPHandler{
			"fusion_mcp_read": func(args map[string]any) MCPResponse {
				return MCPResponse{
					ContentText: `{"success":true,"projects":[]}`,
				}
			},
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	if _, err := client.ActiveHubProjects(testCtx(t)); err != nil {
		t.Fatalf("ActiveHubProjects (stateless): unexpected error: %v", err)
	}
	sids := srv.SessionIDsSeen()
	if len(sids) != 1 {
		t.Fatalf("SessionIDsSeen len = %d, want 1 (got %v)", len(sids), sids)
	}
	if sids[0] != "" {
		t.Errorf("SessionIDsSeen[0] = %q, want \"\" (stateless: client must omit header)", sids[0])
	}
}

// ----------------------------------------------------------------------------
// Sentinel classification — added on top of the vendored client so callers can
// map "Fusion is closed" and "nothing is open to insert into" onto their own
// localized error codes instead of showing a raw transport or Python message.
// ----------------------------------------------------------------------------

func TestUnreachableEndpoint_IsErrUnreachable(t *testing.T) {
	// Point at a port nothing is listening on: the closest reproduction of
	// Fusion simply not running.
	client := &Client{Endpoint: "http://127.0.0.1:1/mcp", HTTP: sharedHTTPClient}
	err := client.OpenDocument(testCtx(t), "urn:adsk.wipprod:dm.lineage:abc")
	if err == nil {
		t.Fatal("OpenDocument against a dead endpoint: want error, got nil")
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("error = %v, want it to wrap ErrUnreachable", err)
	}
}

func TestInsertDocument_NoActiveDesign_IsSentinel(t *testing.T) {
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "sid-insert",
		Tools: map[string]MCPHandler{
			// What Fusion actually returns when buildInsertScript's guard trips:
			// HTTP 200, no isError flag, the failure inside the content text.
			"fusion_mcp_execute": func(args map[string]any) MCPResponse {
				return MCPResponse{
					ContentText: `{"success": false, "error": "No active Fusion design to insert into"}`,
				}
			},
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	err := client.InsertDocument(testCtx(t), "urn:adsk.wipprod:dm.lineage:abc")
	if err == nil {
		t.Fatal("InsertDocument with no active design: want error, got nil")
	}
	if !errors.Is(err, ErrNoActiveDesign) {
		t.Errorf("error = %v, want it to wrap ErrNoActiveDesign", err)
	}
}

func TestOpenDocument_ToolFailure_StaysGeneric(t *testing.T) {
	// A failure we cannot explain precisely must NOT be promoted to a
	// sentinel — otherwise every Fusion-side problem reads as "no active
	// design" and the user is sent down the wrong path.
	srv := NewMCPServer(t, MCPScenario{
		SessionID: "sid-open",
		Tools: map[string]MCPHandler{
			"fusion_mcp_execute": func(args map[string]any) MCPResponse {
				return MCPResponse{ContentText: `{"success": false, "error": "File not found: urn:x"}`}
			},
		},
	})

	client := &Client{Endpoint: srv.URL, HTTP: srv.Client()}
	err := client.OpenDocument(testCtx(t), "urn:adsk.wipprod:dm.lineage:abc")
	if err == nil {
		t.Fatal("OpenDocument with a tool failure: want error, got nil")
	}
	if errors.Is(err, ErrNoActiveDesign) || errors.Is(err, ErrUnreachable) {
		t.Errorf("error = %v, want a plain error (no sentinel)", err)
	}
	if !strings.Contains(err.Error(), "File not found") {
		t.Errorf("error = %v, want it to carry the tool's own message", err)
	}
}
