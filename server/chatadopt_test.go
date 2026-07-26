package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/internal/hubslug"
	"github.com/schneik80/fusionlocalserver/internal/testutil"
)

// quarantinedMeta is a valid chat meta.json (v2) carrying a distinctive
// channel, so a listing proves the served data is the adopted quarantined
// file and not a freshly-created empty project.
const quarantinedMeta = `{
  "version": 2,
  "schema": {},
  "projectId": "urn:project:1",
  "eventEpoch": 1,
  "nextChannelId": 3,
  "channels": [
    {"id": "c1", "name": "general", "topic": "", "isRoot": true, "isPrivate": false, "createdBy": "", "createdAt": "2025-01-01T00:00:00Z"},
    {"id": "c2", "name": "quarantine-proof", "topic": "", "isRoot": false, "isPrivate": false, "createdBy": "u-editor", "createdAt": "2025-01-02T00:00:00Z"}
  ]
}`

// newAdoptionTestServer mirrors newChatTestServer but roots the hub stores at
// a caller-provided config dir (so the test can pre-plant quarantined data)
// and builds the startup quarantine index the way server.Run does.
func newAdoptionTestServer(t *testing.T, configDir string) *Server {
	t.Helper()
	// u-suspended is listed but INACTIVE — the one roster state that denies
	// (an unlisted user falls through to group-derived contributor access).
	suspended := rosterRow("u-suspended", "suspended@x.io", "EDITOR")
	suspended["status"] = "INACTIVE"
	roster := &fakeRoster{rows: []map[string]any{
		rosterRow("u-editor", "editor@x.io", "EDITOR"),
		suspended,
	}}
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: map[string]any{
			"project": map[string]any{
				"folderLevelProjectMembers": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results":    roster.snapshot(),
				},
			},
		}}
	})
	restore := api.SetGraphqlEndpointForTesting(srv.URL)
	t.Cleanup(restore)

	authz := chat.NewAuthorizer()
	hs := newHubStores(configDir, authz)
	t.Cleanup(hs.closeAll)
	s := &Server{
		logger:         quietLogger(),
		clientID:       "test-client",
		sessions:       NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger()),
		pending:        NewPendingStore(pendingTTL),
		hubs:           hs,
		chatAuthz:      authz,
		chatMsgLim:     chat.NewLimiter(2, 5),
		chatOpLim:      chat.NewLimiter(10.0/60.0, 10),
		chatSyncLim:    chat.NewLimiter(2, 20),
		chatQuarantine: loadChatQuarantine(configDir),
	}
	return s
}

func plantQuarantinedChat(t *testing.T, configDir, projSlug, meta string) {
	t.Helper()
	dir := filepath.Join(configDir, "hubs", hubslug.Unassigned, "chat", projSlug)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0600); err != nil {
		t.Fatal(err)
	}
}

func channelNames(list ChannelListDTO) map[string]bool {
	names := map[string]bool{}
	for _, ch := range list.Channels {
		names[ch.Name] = true
	}
	return names
}

// TestChatAdoption_FirstAuthorizedAccessMovesQuarantinedProject drives the
// full adoption flow over HTTP: a quarantined chat project is moved into the
// session hub's profile by the first roster-passing chat access and served
// from there; the second access is a plain hit; an untouched quarantined
// project stays put.
func TestChatAdoption_FirstAuthorizedAccessMovesQuarantinedProject(t *testing.T) {
	configDir := t.TempDir()
	projSlug := hubslug.Slug(chatTestProject) // "urn_project_1"
	plantQuarantinedChat(t, configDir, projSlug, quarantinedMeta)
	plantQuarantinedChat(t, configDir, "urn_project_other",
		`{"version":2,"schema":{},"projectId":"urn:project:other","eventEpoch":1,"nextChannelId":1,"channels":[]}`)

	s := newAdoptionTestServer(t, configDir)
	if s.chatQuarantine == nil {
		t.Fatal("quarantine index empty despite planted dirs")
	}
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	quarantinedDir := filepath.Join(configDir, "hubs", hubslug.Unassigned, "chat", projSlug)
	adoptedDir := filepath.Join(configDir, "hubs", hubslug.Slug(testHubID), "chat", projSlug)

	// First access: adopted and served from the session hub's profile.
	var list ChannelListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, &list); code != http.StatusOK {
		t.Fatalf("first access status = %d, want 200", code)
	}
	if names := channelNames(list); !names["quarantine-proof"] || !names["general"] {
		t.Fatalf("first access channels = %v, want the adopted quarantined channels", list.Channels)
	}
	if _, err := os.Stat(filepath.Join(adoptedDir, "meta.json")); err != nil {
		t.Errorf("adopted dir missing: %v", err)
	}
	if _, err := os.Stat(quarantinedDir); !os.IsNotExist(err) {
		t.Errorf("quarantined dir still present (err=%v)", err)
	}

	// Second access: a plain hit on the adopted data.
	list = ChannelListDTO{}
	if code := chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, &list); code != http.StatusOK {
		t.Fatalf("second access status = %d, want 200", code)
	}
	if names := channelNames(list); !names["quarantine-proof"] {
		t.Fatalf("second access channels = %v", list.Channels)
	}

	// The other quarantined project was never accessed and stays put.
	if _, err := os.Stat(filepath.Join(configDir, "hubs", hubslug.Unassigned, "chat", "urn_project_other", "meta.json")); err != nil {
		t.Errorf("untouched quarantined project moved or vanished: %v", err)
	}
}

// TestChatAdoption_DeniedAccessDoesNotAdopt: a caller who fails the roster
// check must not trigger the move — quarantined data only follows a proven
// project member's session hub.
func TestChatAdoption_DeniedAccessDoesNotAdopt(t *testing.T) {
	configDir := t.TempDir()
	projSlug := hubslug.Slug(chatTestProject)
	plantQuarantinedChat(t, configDir, projSlug, quarantinedMeta)

	s := newAdoptionTestServer(t, configDir)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	suspended := login(t, s, "u-suspended", "Sue Spended", "suspended@x.io")

	if code := chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), suspended, nil, nil); code != http.StatusForbidden {
		t.Fatalf("suspended member status = %d, want 403", code)
	}
	if _, err := os.Stat(filepath.Join(configDir, "hubs", hubslug.Unassigned, "chat", projSlug, "meta.json")); err != nil {
		t.Errorf("quarantined data moved despite denied access: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "hubs", hubslug.Slug(testHubID), "chat", projSlug)); !os.IsNotExist(err) {
		t.Errorf("adopted dir exists despite denied access (err=%v)", err)
	}
}

// TestChatAdoption_DestExistsSkipsAndWarns: when the session hub already has
// chat data for the project, adoption must not merge or overwrite — the
// quarantined copy stays for hand recovery and the live data keeps serving.
func TestChatAdoption_DestExistsSkips(t *testing.T) {
	configDir := t.TempDir()
	projSlug := hubslug.Slug(chatTestProject)
	plantQuarantinedChat(t, configDir, projSlug, quarantinedMeta)

	// Live chat data already in the hub's profile.
	liveDir := filepath.Join(configDir, "hubs", hubslug.Slug(testHubID), "chat", projSlug)
	if err := os.MkdirAll(liveDir, 0700); err != nil {
		t.Fatal(err)
	}
	liveMeta := `{"version":2,"schema":{},"projectId":"urn:project:1","eventEpoch":1,"nextChannelId":2,"channels":[{"id":"c1","name":"general","topic":"","isRoot":true,"isPrivate":false,"createdBy":"","createdAt":"2025-06-01T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(liveDir, "meta.json"), []byte(liveMeta), 0600); err != nil {
		t.Fatal(err)
	}

	s := newAdoptionTestServer(t, configDir)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	var list ChannelListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, &list); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	// Served from the LIVE data, not the quarantined copy.
	if names := channelNames(list); names["quarantine-proof"] {
		t.Fatalf("quarantined data overwrote live data: %v", list.Channels)
	}
	// The quarantined copy survives untouched for hand recovery.
	if _, err := os.Stat(filepath.Join(configDir, "hubs", hubslug.Unassigned, "chat", projSlug, "meta.json")); err != nil {
		t.Errorf("quarantined copy vanished: %v", err)
	}
}
