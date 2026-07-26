package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/backup"
	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/internal/testutil"
	"github.com/schneik80/fusionlocalserver/pins"
	"github.com/schneik80/fusionlocalserver/production"
	"github.com/schneik80/fusionlocalserver/tasks"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// Two-hub attack matrix (the spec's acceptance bar): a session locked to hub
// A must never see, alter, or leak hub B's data — for any store, for pins,
// for the admin data tools, for backups, or over SSE. hubA/hubB share ONE
// server (one hubStores) and the SAME projectId, which is exactly the
// aliasing the physical partition must withstand.

const (
	isoHubA    = "hub-1"
	isoHubB    = "hub-2"
	isoProject = "urn:project:shared-id"
)

// newIsoTestServer is the two-hub fixture: full store sets per hub, a fake
// APS roster granting the editor write everywhere, roomy limiters.
func newIsoTestServer(t *testing.T) *Server {
	t.Helper()
	roster := &fakeRoster{rows: []map[string]any{
		rosterRow("u-editor", "editor@x.io", "EDITOR"),
		rosterRow("u-manager", "manager@x.io", "MANAGER"),
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
	t.Cleanup(api.SetGraphqlEndpointForTesting(srv.URL))

	authz := chat.NewAuthorizer()
	return &Server{
		logger:          quietLogger(),
		clientID:        "test-client",
		sessions:        NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger()),
		pending:         NewPendingStore(pendingTTL),
		hubs:            testHubStores(t, authz),
		chatAuthz:       authz,
		chatMsgLim:      chat.NewLimiter(1e6, 1e6),
		chatOpLim:       chat.NewLimiter(1e6, 1e6),
		chatSyncLim:     chat.NewLimiter(1e6, 1e6),
		taskOpLim:       chat.NewLimiter(1e6, 1e6),
		prodOpLim:       chat.NewLimiter(1e6, 1e6),
		whiteboardOpLim: chat.NewLimiter(1e6, 1e6),
	}
}

// treeSnapshot maps every file under root to its contents.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("treeSnapshot(%s): %v", root, err)
	}
	return out
}

func assertTreeUnchanged(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("%s: file count changed %d → %d", what, len(before), len(after))
	}
	for rel, data := range before {
		if after[rel] != data {
			t.Errorf("%s: %s changed", what, rel)
		}
	}
}

func TestHubIsolation_TasksInvisibleAcrossHubs(t *testing.T) {
	s := newIsoTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io") // locked to hub A

	// Create a task in hub A under the shared project id (assigned to self,
	// so /mine finds it).
	body := map[string]any{"hubId": isoHubA, "projectName": "Proj", "title": "A-secret-task",
		"assignee": map[string]any{"id": "u-editor", "email": "editor@x.io"}}
	var created TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPost,
		"/api/tasks?projectId="+isoProject, editor, body, &created); code != http.StatusCreated {
		t.Fatalf("create in A = %d", code)
	}
	aRoot := hubSet(t, s, isoHubA).root
	before := treeSnapshot(t, aRoot)

	// Switch the session to hub B: the same projectId reads empty, the task
	// id 404s, and /mine is empty.
	lockHub(t, s, editor, isoHubB)
	var list TaskListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/tasks?projectId="+isoProject, editor, nil, &list); code != http.StatusOK {
		t.Fatalf("B list = %d", code)
	}
	if len(list.Tasks) != 0 {
		t.Fatalf("hub B sees hub A tasks: %+v", list.Tasks)
	}
	if code := chatDo(t, ts.URL, http.MethodGet,
		"/api/tasks/get?projectId="+isoProject+"&taskId="+created.ID, editor, nil, nil); code != http.StatusNotFound {
		t.Errorf("B get(A task) = %d, want 404", code)
	}
	var mine MyTasksDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/tasks/mine", editor, nil, &mine); code != http.StatusOK {
		t.Fatalf("B mine = %d", code)
	}
	if len(mine.Tasks) != 0 {
		t.Errorf("hub B /mine sees hub A tasks: %+v", mine.Tasks)
	}

	// Create with a body hubId naming A while locked to B → 403 hub_mismatch.
	code, e := errBody(t, ts.URL, http.MethodPost, "/api/tasks?projectId="+isoProject, editor, body)
	if code != http.StatusForbidden || e.Code != codeHubMismatch {
		t.Errorf("B create with A body-hub = %d %q, want 403 hub_mismatch", code, e.Code)
	}

	// Deleting the A task via B answers 404 and A's files are byte-identical.
	if code := chatDo(t, ts.URL, http.MethodDelete,
		"/api/tasks?projectId="+isoProject+"&taskId="+created.ID, editor, nil, nil); code != http.StatusNotFound {
		t.Errorf("B delete(A task) = %d, want 404", code)
	}
	assertTreeUnchanged(t, "hub A profile", before, treeSnapshot(t, aRoot))

	// Back on A everything is still there.
	lockHub(t, s, editor, isoHubA)
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/tasks/mine", editor, nil, &mine); code != http.StatusOK || len(mine.Tasks) != 1 {
		t.Errorf("A /mine after round trip = %d, %d tasks; want 1", code, len(mine.Tasks))
	}
}

func TestHubIsolation_ProductionInvisibleAcrossHubs(t *testing.T) {
	s := newIsoTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")

	body := map[string]any{"hubId": isoHubA, "projectName": "Proj", "name": "A-secret-job"}
	var job ProdJobDTO
	if code := chatDo(t, ts.URL, http.MethodPost,
		"/api/production/jobs?projectId="+isoProject, editor, body, &job); code != http.StatusCreated {
		t.Fatalf("create job in A = %d", code)
	}
	aRoot := hubSet(t, s, isoHubA).root
	before := treeSnapshot(t, aRoot)

	lockHub(t, s, editor, isoHubB)
	var list ProdJobListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/production/jobs?projectId="+isoProject, editor, nil, &list); code != http.StatusOK {
		t.Fatalf("B jobs = %d", code)
	}
	if len(list.Jobs) != 0 {
		t.Fatalf("hub B sees hub A jobs: %+v", list.Jobs)
	}
	if code := chatDo(t, ts.URL, http.MethodGet,
		"/api/production/job?projectId="+isoProject+"&jobId="+job.ID, editor, nil, nil); code != http.StatusNotFound {
		t.Errorf("B get(A job) = %d, want 404", code)
	}
	var mine MyProductionDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/production/mine", editor, nil, &mine); code != http.StatusOK || len(mine.Jobs) != 0 {
		t.Errorf("B production /mine = %d, %d jobs; want 0", code, len(mine.Jobs))
	}
	code, e := errBody(t, ts.URL, http.MethodPost, "/api/production/jobs?projectId="+isoProject, editor, body)
	if code != http.StatusForbidden || e.Code != codeHubMismatch {
		t.Errorf("B create with A body-hub = %d %q, want 403 hub_mismatch", code, e.Code)
	}
	// A plan-doc reference naming hub A refuses before any APS call.
	code, e = errBody(t, ts.URL, http.MethodPost,
		"/api/production/plandocs?projectId="+isoProject+"&jobId=j1&stepId=s1", editor,
		map[string]any{"hubId": isoHubA, "itemId": "i1", "dmProjectId": "d1"})
	if code != http.StatusForbidden || e.Code != codeHubMismatch {
		t.Errorf("B plandoc with A hub = %d %q, want 403 hub_mismatch", code, e.Code)
	}
	assertTreeUnchanged(t, "hub A profile", before, treeSnapshot(t, aRoot))
}

func TestHubIsolation_WhiteboardsInvisibleAcrossHubs(t *testing.T) {
	s := newIsoTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")

	body := map[string]any{"hubId": isoHubA, "projectName": "Proj", "name": "A-secret-board"}
	var board WhiteboardDTO
	if code := chatDo(t, ts.URL, http.MethodPost,
		"/api/whiteboards?projectId="+isoProject, editor, body, &board); code != http.StatusCreated {
		t.Fatalf("create board in A = %d", code)
	}

	lockHub(t, s, editor, isoHubB)
	var list WhiteboardListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/whiteboards?projectId="+isoProject, editor, nil, &list); code != http.StatusOK {
		t.Fatalf("B boards = %d", code)
	}
	if len(list.Whiteboards) != 0 {
		t.Fatalf("hub B sees hub A boards: %+v", list.Whiteboards)
	}
	if code := chatDo(t, ts.URL, http.MethodGet,
		"/api/whiteboards/doc?projectId="+isoProject+"&boardId="+board.ID, editor, nil, nil); code != http.StatusNotFound {
		t.Errorf("B doc(A board) = %d, want 404", code)
	}
	code, e := errBody(t, ts.URL, http.MethodPost, "/api/whiteboards?projectId="+isoProject, editor, body)
	if code != http.StatusForbidden || e.Code != codeHubMismatch {
		t.Errorf("B create with A body-hub = %d %q, want 403 hub_mismatch", code, e.Code)
	}
}

func TestHubIsolation_ChatStoresAndSSEDisjoint(t *testing.T) {
	s := newIsoTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")

	// Post a message in hub A's root channel of the shared project.
	var list ChannelListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/chat/channels?projectId="+isoProject, editor, nil, &list); code != http.StatusOK {
		t.Fatalf("A channels = %d", code)
	}
	rootA := list.Channels[0].ID
	if code := chatDo(t, ts.URL, http.MethodPost,
		"/api/chat/messages?projectId="+isoProject+"&channelId="+rootA, editor,
		map[string]any{"body": "A-secret-message", "clientMsgId": "cm-1"}, nil); code != http.StatusCreated {
		t.Fatalf("A post = %d", code)
	}
	aRoot := hubSet(t, s, isoHubA).root
	before := treeSnapshot(t, aRoot)

	// A second session locked to hub B: same project id → B's OWN (fresh)
	// store: a root channel with zero messages.
	other := login(t, s, "u-manager", "Man", "manager@x.io")
	lockHub(t, s, other, isoHubB)
	var listB ChannelListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/chat/channels?projectId="+isoProject, other, nil, &listB); code != http.StatusOK {
		t.Fatalf("B channels = %d", code)
	}
	if len(listB.Channels) != 1 {
		t.Fatalf("hub B root channel not pristine: %+v", listB.Channels)
	}
	var msgs MessageListDTO
	if code := chatDo(t, ts.URL, http.MethodGet,
		"/api/chat/messages?projectId="+isoProject+"&channelId="+listB.Channels[0].ID, other, nil, &msgs); code != http.StatusOK {
		t.Fatalf("B messages = %d", code)
	}
	if len(msgs.Messages) != 0 || msgs.LatestSeq != 0 {
		t.Fatalf("hub B reads hub A messages: %+v (latestSeq %d)", msgs.Messages, msgs.LatestSeq)
	}

	// SSE isolation: the hubs' fan-outs are distinct objects, and a message
	// posted in hub A never reaches a hub-B subscriber of the same project.
	if hubSet(t, s, isoHubA).chatHub == hubSet(t, s, isoHubB).chatHub {
		t.Fatal("hub A and hub B share one SSE fan-out")
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/chat/events?projectId="+isoProject, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(other)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("B events stream = %d", resp.StatusCode)
	}
	if code := chatDo(t, ts.URL, http.MethodPost,
		"/api/chat/messages?projectId="+isoProject+"&channelId="+rootA, editor,
		map[string]any{"body": "A-second-message", "clientMsgId": "cm-2"}, nil); code != http.StatusCreated {
		t.Fatalf("A second post = %d", code)
	}
	// Read what arrives on B's stream for a short window (the connection is
	// closed from the outside to end the blocking read); no frame may carry
	// the A message.
	frames := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				frames <- string(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		close(frames)
	}()
	time.AfterFunc(600*time.Millisecond, func() { resp.Body.Close() })
	for chunk := range frames {
		if strings.Contains(chunk, "A-second-message") || strings.Contains(chunk, "A-secret-message") {
			t.Fatal("hub A chat event leaked onto hub B's SSE stream")
		}
	}

	// Re-read hub A through the second session: exactly the two A messages,
	// untouched by anything the B session did.
	lockHub(t, s, other, isoHubA)
	var msgsA MessageListDTO
	if code := chatDo(t, ts.URL, http.MethodGet,
		"/api/chat/messages?projectId="+isoProject+"&channelId="+rootA, other, nil, &msgsA); code != http.StatusOK {
		t.Fatalf("A messages re-read = %d", code)
	}
	if len(msgsA.Messages) != 2 || msgsA.LatestSeq != 2 {
		t.Errorf("hub A messages = %d (latestSeq %d), want the 2 originals", len(msgsA.Messages), msgsA.LatestSeq)
	}
	_ = before // the byte-compare guarantee is exercised in the task/production tests; A's log grew by its own second post here
}

func TestHubIsolation_PinsPerSessionHub(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // pins resolve through config.Dir()
	s := newIsoTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")

	pin := map[string]any{"id": "urn:item:a", "name": "A pin", "kind": "design"}
	var psA []pins.Pin
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/pins", editor, pin, &psA); code != http.StatusOK {
		t.Fatalf("A pin add = %d", code)
	}
	if len(psA) != 1 || psA[0].HubID != isoHubA {
		t.Fatalf("A pins = %+v (hub must be the session's, never the body's)", psA)
	}
	dir, _ := os.UserHomeDir()
	aPinsFile := filepath.Join(dir, ".config", "fusionlocalserver", "hubs", isoHubA, "pins-"+isoHubA+".json")
	beforeBytes, err := os.ReadFile(aPinsFile)
	if err != nil {
		t.Fatalf("hub A pins file not under the profile: %v", err)
	}

	// Locked to B: the list is empty; naming hub A in the query 403s and A's
	// file is untouched afterward.
	lockHub(t, s, editor, isoHubB)
	var psB []pins.Pin
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/pins", editor, nil, &psB); code != http.StatusOK {
		t.Fatalf("B pins list = %d", code)
	}
	if len(psB) != 0 {
		t.Fatalf("hub B sees hub A pins: %+v", psB)
	}
	code, e := errBody(t, ts.URL, http.MethodGet, "/api/pins?hubId="+isoHubA, editor, nil)
	if code != http.StatusForbidden || e.Code != codeHubMismatch {
		t.Errorf("B list with A hubId = %d %q, want 403 hub_mismatch", code, e.Code)
	}
	if code := chatDo(t, ts.URL, http.MethodDelete, "/api/pins?id=urn:item:a", editor, nil, nil); code != http.StatusOK {
		t.Fatalf("B remove = %d", code) // removes from B's (empty) list — a no-op
	}
	afterBytes, err := os.ReadFile(aPinsFile)
	if err != nil || string(afterBytes) != string(beforeBytes) {
		t.Errorf("hub A pins file changed under a hub B session (err=%v)", err)
	}
}

func TestHubIsolation_AdminDataScopedToSessionHub(t *testing.T) {
	s := newIsoTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")

	// Seed hub A: one task project + a stale .bak old enough to prune.
	setA := hubSet(t, s, isoHubA)
	if _, err := setA.tasks.Create(isoProject, isoHubA, "Proj",
		tasks.Draft{Title: "sized"}, tasks.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}
	staleBak := filepath.Join(setA.root, "tasks", "proj_x", "tasks.json.bak")
	if err := os.MkdirAll(filepath.Dir(staleBak), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleBak, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(staleBak, past, past); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, setA.root)

	// Disk via hub B: no hub A rows.
	lockHub(t, s, editor, isoHubB)
	var disk DiskUsageDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/disk", editor, nil, &disk); code != http.StatusOK {
		t.Fatalf("B disk = %d", code)
	}
	for _, st := range disk.Stores {
		if len(st.Projects) != 0 {
			t.Errorf("hub B disk lists hub A data in %s: %+v", st.Name, st.Projects)
		}
	}

	// Delete via hub B: idempotent-true against B's empty store, A intact.
	if code := chatDo(t, ts.URL, http.MethodDelete,
		"/api/admin/projects/data?projectId="+isoProject+"&apps=tasks", editor, nil, nil); code != http.StatusOK {
		t.Fatalf("B delete = %d", code)
	}
	// Cleanup via hub B: hub A's stale .bak survives.
	var cl CleanupResultDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/cleanup", editor, nil, &cl); code != http.StatusOK {
		t.Fatalf("B cleanup = %d", code)
	}
	if _, err := os.Stat(staleBak); err != nil {
		t.Errorf("hub A's stale .bak was pruned by hub B's cleanup: %v", err)
	}
	assertTreeUnchanged(t, "hub A profile", before, treeSnapshot(t, setA.root))

	// Cleanup via hub A DOES prune it.
	lockHub(t, s, editor, isoHubA)
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/cleanup", editor, nil, &cl); code != http.StatusOK {
		t.Fatalf("A cleanup = %d", code)
	}
	if _, err := os.Stat(staleBak); !os.IsNotExist(err) {
		t.Errorf("hub A cleanup left its own stale .bak: %v", err)
	}
}

func TestHubIsolation_BackupsPerHubTreeAndNoForeignBytes(t *testing.T) {
	s := newIsoTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")

	// Marker data in BOTH hubs.
	setA := hubSet(t, s, isoHubA)
	setB := hubSet(t, s, isoHubB)
	if _, err := setA.tasks.Create(isoProject, isoHubA, "Proj",
		tasks.Draft{Title: "MARKER-HUB-A"}, tasks.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := setB.production.CreateJob(isoProject, isoHubB, "Proj",
		production.JobDraft{Name: "MARKER-HUB-B"}, production.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := setB.whiteboards.Create(isoProject, isoHubB, "Proj",
		whiteboards.Draft{Name: "MARKER-HUB-B-BOARD"}, whiteboards.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}

	// Configure ONE shared destination from hub A's session; only hub A's
	// backup.json is written and the tree roots at <dir>/<slugA>/.
	sharedDir := t.TempDir()
	cfg := BackupConfigDTO{BackupDir: sharedDir, BackupTime: "03:30"}
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/config", editor, cfg, nil); code != http.StatusOK {
		t.Fatalf("A config = %d", code)
	}
	if _, err := os.Stat(filepath.Join(setA.root, hubBackupConfigFile)); err != nil {
		t.Fatalf("hub A backup.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(setB.root, hubBackupConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("hub A's config POST touched hub B's backup.json: %v", err)
	}

	// Run a manual backup as hub A and prove zero hub-B bytes in the tree.
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/run", editor, nil, nil); code != http.StatusOK {
		t.Fatalf("A run = %d", code)
	}
	tree := treeSnapshot(t, filepath.Join(sharedDir, isoHubA))
	if len(tree) == 0 {
		t.Fatal("hub A snapshot tree is empty")
	}
	foundA := false
	for rel, data := range tree {
		if strings.Contains(data, "MARKER-HUB-B") {
			t.Errorf("hub B bytes leaked into hub A's snapshot: %s", rel)
		}
		if strings.Contains(data, "MARKER-HUB-A") {
			foundA = true
		}
	}
	if !foundA {
		t.Error("hub A's own data missing from its snapshot")
	}
	// Nothing landed outside the hub A subtree.
	entries, err := os.ReadDir(sharedDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != isoHubA {
		t.Errorf("backup destination entries = %v, want only %q", entries, isoHubA)
	}

	// The manifest is stamped with hub A's identity (v2): Hub is the raw id,
	// HubSlug the profile slug — what restore's foreign-hub gate keys on.
	manualTier := filepath.Join(sharedDir, isoHubA, "manual")
	snaps, err := os.ReadDir(manualTier)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("manual tier = %v, %v (want exactly 1)", snaps, err)
	}
	m, err := backup.ReadManifest(filepath.Join(manualTier, snaps[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if m.ManifestVersion != backup.ManifestVersion || m.Hub != isoHubA || m.HubSlug != setA.slug {
		t.Errorf("manifest identity = v%d hub %q slug %q, want v%d %q/%q",
			m.ManifestVersion, m.Hub, m.HubSlug, backup.ManifestVersion, isoHubA, setA.slug)
	}

	// Hub B has no destination configured → its run answers 503.
	lockHub(t, s, editor, isoHubB)
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/run", editor, nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("B run without config = %d, want 503", code)
	}
}
