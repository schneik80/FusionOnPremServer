package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/config"
	"github.com/schneik80/fusionlocalserver/production"
	"github.com/schneik80/fusionlocalserver/tasks"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// newDataTestServer builds a Server whose four stores are rooted under the
// real config.Dir() (HOME is a per-test TempDir — the pins withTempConfigDir
// trick), because the Data endpoints walk the config dir itself rather than
// asking the stores.
func newDataTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	chatStore, err := chat.NewStore(filepath.Join(dir, "chat"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(chatStore.Close)
	tasksStore, err := tasks.NewStore(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	prodStore, err := production.NewStore(filepath.Join(dir, "production"))
	if err != nil {
		t.Fatal(err)
	}
	wbStore, err := whiteboards.NewStore(filepath.Join(dir, "whiteboards"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		logger:      quietLogger(),
		clientID:    "test-client",
		sessions:    NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger()),
		pending:     NewPendingStore(pendingTTL),
		chat:        chatStore,
		tasks:       tasksStore,
		production:  prodStore,
		whiteboards: wbStore,
	}
	return s, dir
}

const dataTestProject = "urn:project:data-1"

func TestAdminDiskUsage(t *testing.T) {
	s, dir := newDataTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	// Seed two app stores for one project, a pins file, and an "other" file.
	if _, err := s.tasks.Create(dataTestProject, "hub-1", "Data Project",
		tasks.Draft{Title: "sized"}, tasks.UserRef{ID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.production.CreateJob(dataTestProject, "hub-1", "Data Project",
		production.JobDraft{Name: "sized job"}, production.UserRef{ID: "u1"}); err != nil {
		t.Fatal(err)
	}
	pinsJSON := `{"version":1,"pins":[{"id":"urn:x","hub_id":"hub-1","kind":"project"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pins-hub_1.json"), []byte(pinsJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.log"), []byte("log line\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var out DiskUsageDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/disk", editor, nil, &out); code != http.StatusOK {
		t.Fatalf("disk: status = %d, want 200", code)
	}

	// All five stores present, in order, with non-nil project slices.
	wantNames := []string{"chat", "tasks", "production", "whiteboards", "pins"}
	gotNames := make([]string, 0, len(out.Stores))
	byName := map[string]DiskStoreDTO{}
	for _, st := range out.Stores {
		gotNames = append(gotNames, st.Name)
		byName[st.Name] = st
		if st.Projects == nil {
			t.Errorf("store %s: projects slice is nil, want []", st.Name)
		}
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("store names = %v, want %v", gotNames, wantNames)
	}

	// The tasks project row self-describes from its envelope.
	tasksStore := byName["tasks"]
	if len(tasksStore.Projects) != 1 {
		t.Fatalf("tasks projects = %d, want 1", len(tasksStore.Projects))
	}
	p := tasksStore.Projects[0]
	if p.ProjectID != dataTestProject || p.HubID != "hub-1" || p.ProjectName != "Data Project" {
		t.Errorf("tasks project row = %+v, want envelope identity fields", p)
	}
	if p.Bytes <= 0 || tasksStore.Bytes != p.Bytes {
		t.Errorf("tasks sizes: project %d, store %d — want equal and > 0", p.Bytes, tasksStore.Bytes)
	}
	if got := byName["production"]; len(got.Projects) != 1 || got.Projects[0].ProjectID != dataTestProject {
		t.Errorf("production projects = %+v", got.Projects)
	}
	if got := byName["chat"]; len(got.Projects) != 0 || got.Bytes != 0 {
		t.Errorf("chat store (never touched) = %+v, want empty", got)
	}

	// Pins pseudo-store: per-hub rows, hub id from the file, no project fields.
	pins := byName["pins"]
	if len(pins.Projects) != 1 {
		t.Fatalf("pins rows = %d, want 1", len(pins.Projects))
	}
	pr := pins.Projects[0]
	if pr.Dir != "pins-hub_1.json" || pr.HubID != "hub-1" || pr.ProjectID != "" || pr.ProjectName != "" {
		t.Errorf("pins row = %+v", pr)
	}
	if pr.Bytes != int64(len(pinsJSON)) {
		t.Errorf("pins bytes = %d, want %d", pr.Bytes, len(pinsJSON))
	}

	// server.log lands in otherBytes; the totals add up.
	if out.OtherBytes < int64(len("log line\n")) {
		t.Errorf("otherBytes = %d, want at least the server.log size", out.OtherBytes)
	}
	var accounted int64
	for _, st := range out.Stores {
		accounted += st.Bytes
	}
	if out.TotalBytes != accounted+out.OtherBytes {
		t.Errorf("totalBytes = %d, want stores %d + other %d", out.TotalBytes, accounted, out.OtherBytes)
	}
}

func TestAdminProjectDataDelete(t *testing.T) {
	s, dir := newDataTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	seed := func() {
		t.Helper()
		if _, err := s.tasks.Create(dataTestProject, "hub-1", "Data Project",
			tasks.Draft{Title: "doomed"}, tasks.UserRef{ID: "u1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.production.CreateJob(dataTestProject, "hub-1", "Data Project",
			production.JobDraft{Name: "survivor"}, production.UserRef{ID: "u1"}); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	// The slug both stores write for this project (sanitizeID is shared).
	slug := "urn_project_data-1"
	tasksDir := filepath.Join(dir, "tasks", slug)
	prodDir := filepath.Join(dir, "production", slug)
	deleteURL := func(projectID, apps string) string {
		return "/api/admin/projects/data?projectId=" + url.QueryEscape(projectID) +
			"&apps=" + url.QueryEscape(apps)
	}

	// Missing params → 400.
	if code := chatDo(t, ts.URL, http.MethodDelete, "/api/admin/projects/data?apps=tasks", editor, nil, nil); code != http.StatusBadRequest {
		t.Errorf("missing projectId: status = %d, want 400", code)
	}
	if code := chatDo(t, ts.URL, http.MethodDelete, deleteURL(dataTestProject, " , "), editor, nil, nil); code != http.StatusBadRequest {
		t.Errorf("empty apps: status = %d, want 400", code)
	}

	// Unknown app anywhere in the list → 400, and NOTHING deletes.
	if code := chatDo(t, ts.URL, http.MethodDelete, deleteURL(dataTestProject, "tasks,bogus"), editor, nil, nil); code != http.StatusBadRequest {
		t.Errorf("unknown app: status = %d, want 400", code)
	}
	if _, err := os.Stat(tasksDir); err != nil {
		t.Fatalf("tasks dir deleted despite invalid apps list: %v", err)
	}

	// Happy path: only the named app's dir goes; the other app survives.
	var res ProjectDataDeleteDTO
	if code := chatDo(t, ts.URL, http.MethodDelete, deleteURL(dataTestProject, "tasks"), editor, nil, &res); code != http.StatusOK {
		t.Fatalf("delete tasks: status = %d, want 200", code)
	}
	if !res.Deleted["tasks"] || len(res.Deleted) != 1 {
		t.Errorf("deleted = %+v, want exactly {tasks:true}", res.Deleted)
	}
	if _, err := os.Stat(tasksDir); !os.IsNotExist(err) {
		t.Errorf("tasks dir still present: %v", err)
	}
	if _, err := os.Stat(prodDir); err != nil {
		t.Errorf("production dir was collateral damage: %v", err)
	}

	// Idempotent: the same delete on the now-missing dir still reports true.
	res = ProjectDataDeleteDTO{}
	if code := chatDo(t, ts.URL, http.MethodDelete, deleteURL(dataTestProject, "tasks"), editor, nil, &res); code != http.StatusOK {
		t.Fatalf("repeat delete: status = %d, want 200", code)
	}
	if !res.Deleted["tasks"] {
		t.Errorf("repeat deleted = %+v, want tasks:true", res.Deleted)
	}

	// All four apps at once (duplicates tolerated), and unauthenticated → 401.
	res = ProjectDataDeleteDTO{}
	if code := chatDo(t, ts.URL, http.MethodDelete,
		deleteURL(dataTestProject, "chat,tasks,production,whiteboards,tasks"), editor, nil, &res); code != http.StatusOK {
		t.Fatalf("delete all: status = %d, want 200", code)
	}
	for _, app := range []string{"chat", "tasks", "production", "whiteboards"} {
		if !res.Deleted[app] {
			t.Errorf("deleted[%s] = false, want true", app)
		}
	}
	if _, err := os.Stat(prodDir); !os.IsNotExist(err) {
		t.Errorf("production dir still present after delete-all: %v", err)
	}
	if code := chatDo(t, ts.URL, http.MethodDelete, deleteURL(dataTestProject, "tasks"), nil, nil, nil); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated delete: status = %d, want 401", code)
	}
}

func TestAdminCleanup(t *testing.T) {
	s, dir := newDataTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// The full stale set…
	write(filepath.Join(dir, "tokens.json"), `{"old":"token"}`)
	write(filepath.Join(dir, "debug.log"), "debug\n")
	write(filepath.Join(dir, "server-restart.log"), "restart\n")
	write(filepath.Join(dir, "models", "cache.bin"), "modeldata")
	// …files that must NEVER be touched…
	write(filepath.Join(dir, "sessions.enc"), "cipher")
	write(filepath.Join(dir, "session.key"), "key")
	write(filepath.Join(dir, "tls-cert.pem"), "cert")
	write(filepath.Join(dir, "server.log"), "log\n")
	write(filepath.Join(dir, "config.json"), `{"client_id":"x"}`)
	// …and store-dir .bak/.tmp files: one recent (survives), two old (pruned),
	// plus an old live file that matches no suffix (survives).
	recentBak := filepath.Join(dir, "tasks", "proj_a", "tasks.json.bak")
	oldBak := filepath.Join(dir, "tasks", "proj_a", "tasks.json.v1.bak")
	oldTmp := filepath.Join(dir, "whiteboards", "proj_b", "doc-w1.json.tmp")
	oldLive := filepath.Join(dir, "tasks", "proj_a", "tasks.json")
	write(recentBak, "recent backup")
	write(oldBak, "v1 backup")
	write(oldTmp, "half-written temp")
	write(oldLive, `{"version":2}`)
	past := time.Now().Add(-31 * 24 * time.Hour)
	for _, p := range []string{oldBak, oldTmp, oldLive} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}

	var out CleanupResultDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/cleanup", editor, nil, &out); code != http.StatusOK {
		t.Fatalf("cleanup: status = %d, want 200", code)
	}

	wantRemoved := []string{
		"tokens.json", "debug.log", "server-restart.log", "models/",
		"tasks/proj_a/tasks.json.v1.bak", "whiteboards/proj_b/doc-w1.json.tmp",
	}
	for _, name := range wantRemoved {
		if !slices.Contains(out.Removed, name) {
			t.Errorf("removed list %v is missing %q", out.Removed, name)
		}
	}
	if len(out.Removed) != len(wantRemoved) {
		t.Errorf("removed = %v, want exactly %v", out.Removed, wantRemoved)
	}
	if out.BytesFreed <= 0 {
		t.Errorf("bytesFreed = %d, want > 0", out.BytesFreed)
	}

	// Gone: the stale set + old bak/tmp.
	for _, p := range []string{
		filepath.Join(dir, "tokens.json"), filepath.Join(dir, "debug.log"),
		filepath.Join(dir, "server-restart.log"), filepath.Join(dir, "models"),
		oldBak, oldTmp,
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still present after cleanup: %v", p, err)
		}
	}
	// Untouched: protected files, the recent .bak, and old live data.
	for _, p := range []string{
		filepath.Join(dir, "sessions.enc"), filepath.Join(dir, "session.key"),
		filepath.Join(dir, "tls-cert.pem"), filepath.Join(dir, "server.log"),
		filepath.Join(dir, "config.json"), recentBak, oldLive,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed by cleanup: %v", p, err)
		}
	}

	// Second run finds nothing.
	out = CleanupResultDTO{}
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/cleanup", editor, nil, &out); code != http.StatusOK {
		t.Fatalf("second cleanup: status = %d, want 200", code)
	}
	if len(out.Removed) != 0 || out.BytesFreed != 0 {
		t.Errorf("second cleanup = %+v, want nothing removed", out)
	}
}
