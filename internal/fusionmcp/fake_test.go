package fusionmcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// MCPHandler is invoked for each tools/call request the fake server
// receives. args is the decoded "arguments" map from the JSON-RPC params.
// The returned MCPResponse controls what the fake replies with (or whether
// it rejects the call as session-expired).
type MCPHandler func(args map[string]any) MCPResponse

// MCPResponse is one tools/call reply.
//
//   - ContentText: payload returned as result.content[0].text (the JSON
//     string the production client further decodes via parseToolErrorText).
//   - IsError: sets result.isError on the JSON-RPC response.
//   - SessionExpired: when true, the server returns 404 (production retries
//     with a fresh handshake).
//   - Status: overrides the default 200 (rare; use for non-404 error
//     responses like 500).
type MCPResponse struct {
	ContentText    string
	IsError        bool
	SessionExpired bool
	Status         int
}

// MCPScenario configures the fake MCP server.
//
//   - SessionID: returned in the Mcp-Session-Id response header on
//     initialize. Empty string emulates stateless mode (no header).
//   - Tools: handlers indexed by tool name. A request for an unmapped tool
//     fails the test.
//   - SSEMode: when true, tools/call responses are wrapped as
//     "data: <json>\n\n" SSE events instead of plain JSON.
type MCPScenario struct {
	SessionID string
	Tools     map[string]MCPHandler
	SSEMode   bool
}

// MCPServer wraps an httptest.Server with per-tool call counts so tests
// can assert on retry / session-cache behaviour.
type MCPServer struct {
	*httptest.Server
	mu      sync.Mutex
	calls   map[string]int
	inits   int
	sidSeen []string // session IDs received on tools/call requests
}

// InitCount reports how many times the initialize handshake ran.
func (s *MCPServer) InitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inits
}

// CallCount reports how many tools/call requests targeted toolName.
func (s *MCPServer) CallCount(toolName string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[toolName]
}

// SessionIDsSeen returns the Mcp-Session-Id header values received on
// tools/call requests in arrival order. Useful for asserting that the
// production client sent the cached SID after init, and re-handshake
// happened after a session-expired response.
func (s *MCPServer) SessionIDsSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.sidSeen))
	copy(out, s.sidSeen)
	return out
}

// NewMCPServer starts a fake Fusion MCP JSON-RPC server scripted by
// scenario. The server handles `initialize`, the
// `notifications/initialized` notification (HTTP 204), and `tools/call`
// dispatched via scenario.Tools. Auto-closed via t.Cleanup.
func NewMCPServer(t *testing.T, scenario MCPScenario) *MCPServer {
	t.Helper()
	s := &MCPServer{calls: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("testutil: reading MCP request body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("testutil: decoding MCP request %q: %v", body, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "initialize":
			s.mu.Lock()
			s.inits++
			s.mu.Unlock()
			if scenario.SessionID != "" {
				w.Header().Set("Mcp-Session-Id", scenario.SessionID)
			}
			writeJSONRPC(w, req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake-mcp", "version": "0"},
			})

		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)

		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				t.Errorf("testutil: decoding tools/call params: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			s.calls[p.Name]++
			s.sidSeen = append(s.sidSeen, r.Header.Get("Mcp-Session-Id"))
			s.mu.Unlock()

			handler, ok := scenario.Tools[p.Name]
			if !ok {
				t.Errorf("testutil: no scenario handler for tool %q", p.Name)
				http.Error(w, "no handler", http.StatusInternalServerError)
				return
			}
			resp := handler(p.Arguments)
			if resp.SessionExpired {
				http.Error(w, "session expired", http.StatusNotFound)
				return
			}
			if resp.Status != 0 && resp.Status != http.StatusOK {
				http.Error(w, "scripted non-OK", resp.Status)
				return
			}

			payload := map[string]any{
				"content": []map[string]any{{"type": "text", "text": resp.ContentText}},
				"isError": resp.IsError,
			}
			if scenario.SSEMode {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				var buf bytes.Buffer
				_ = json.NewEncoder(&buf).Encode(fakeRPCResponse{
					JSONRPC: "2.0", ID: req.ID, Result: fakeMarshal(payload),
				})
				_, _ = io.WriteString(w, "data: "+buf.String()+"\n")
				return
			}
			writeJSONRPC(w, req.ID, payload)

		default:
			t.Errorf("testutil: unexpected MCP method %q", req.Method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

type fakeRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func writeJSONRPC(w http.ResponseWriter, id int, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fakeRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  fakeMarshal(result),
	})
}

func fakeMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
