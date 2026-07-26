package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/backup"
	"github.com/schneik80/fusionlocalserver/tasks"
)

func TestBackupConfigValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newTaskTestServer(t)
	s.backupPoke = make(chan struct{}, 1)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")
	hubRoot := hubSet(t, s, testHubID).root

	backupDir := t.TempDir()

	// Bad HH:MM → 400.
	for _, bad := range []string{"3:30", "24:00", "12:60", "noon"} {
		body := BackupConfigDTO{BackupDir: backupDir, BackupTime: bad}
		if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/config", editor, body, nil); code != http.StatusBadRequest {
			t.Errorf("backupTime %q: status = %d, want 400", bad, code)
		}
	}

	// Relative dir → 400.
	body := BackupConfigDTO{BackupDir: "relative/backups", BackupTime: "03:30"}
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/config", editor, body, nil); code != http.StatusBadRequest {
		t.Errorf("relative dir: status = %d, want 400", code)
	}

	// Enabled without a dir → 400.
	body = BackupConfigDTO{BackupDir: "", BackupTime: "03:30", BackupEnabled: true}
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/config", editor, body, nil); code != http.StatusBadRequest {
		t.Errorf("enabled without dir: status = %d, want 400", code)
	}

	// Valid → 200, persisted into the SESSION HUB's backup.json, an engine
	// resolvable on demand, and server.json (the port) untouched.
	if err := SaveSettings(Settings{Port: 9123}); err != nil {
		t.Fatal(err)
	}
	body = BackupConfigDTO{BackupDir: backupDir, BackupTime: "04:15", BackupEnabled: true}
	var echoed BackupConfigDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/config", editor, body, &echoed); code != http.StatusOK {
		t.Fatalf("valid config: status = %d, want 200", code)
	}
	if echoed.BackupDir != backupDir || echoed.BackupTime != "04:15" || !echoed.BackupEnabled {
		t.Errorf("echoed config = %+v", echoed)
	}
	cfg, err := loadHubBackupConfig(hubRoot)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackupDir != backupDir || cfg.BackupTime != "04:15" || !cfg.BackupEnabled {
		t.Errorf("persisted hub backup config = %+v", cfg)
	}
	set, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if set.Port != 9123 {
		t.Errorf("persisted settings = %+v, want the port untouched", set)
	}
	if eng, eerr := s.backupEngineFor(hubSet(t, s, testHubID)); eerr != nil || eng == nil {
		t.Errorf("engine not resolvable after valid config: %v", eerr)
	} else if eng.Dir != filepath.Join(backupDir, testHubID) {
		t.Errorf("engine dir = %q, want the hub's own subtree %q", eng.Dir, filepath.Join(backupDir, testHubID))
	}

	// GET reflects it.
	var got BackupConfigDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/backups/config", editor, nil, &got); code != http.StatusOK {
		t.Fatalf("config GET: status = %d", code)
	}
	if got != echoed {
		t.Errorf("config GET = %+v, want %+v", got, echoed)
	}
}

func TestBackupRunEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	// No engine (no backup dir configured) → 503.
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/run", editor, nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("run without engine: status = %d, want 503", code)
	}

	// With a configured destination → 200 and a snapshot on disk, inside the
	// hub's own <dir>/<slug>/ subtree.
	backupDir := t.TempDir()
	if err := saveHubBackupConfig(hubSet(t, s, testHubID).root, hubBackupConfig{BackupDir: backupDir}); err != nil {
		t.Fatal(err)
	}

	var sum BackupSummaryDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/run", editor, nil, &sum); code != http.StatusOK {
		t.Fatalf("run with engine: status = %d, want 200", code)
	}
	if sum.Kind != "manual" || sum.CreatedAt == "" {
		t.Errorf("run summary = %+v", sum)
	}
	entries, err := os.ReadDir(filepath.Join(backupDir, testHubID, "manual"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("manual tier = %v, %v", entries, err)
	}

	// The list endpoint shows it.
	var list BackupListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/backups", editor, nil, &list); code != http.StatusOK {
		t.Fatalf("list: status = %d", code)
	}
	if len(list.Backups) != 1 || list.Backups[0].Kind != "manual" {
		t.Errorf("list = %+v", list.Backups)
	}

	// Unauthenticated → 401.
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/run", nil, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated run: status = %d, want 401", code)
	}
}

func TestFsDirsListsOnlyVisibleDirectories(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	root := t.TempDir()
	for _, d := range []string{"beta", "alpha", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	var out FsDirsDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/fs/dirs?path="+root, editor, nil, &out); code != http.StatusOK {
		t.Fatalf("fs/dirs: status = %d", code)
	}
	if out.Path != root {
		t.Errorf("path = %q, want %q", out.Path, root)
	}
	if out.Parent != filepath.Dir(root) {
		t.Errorf("parent = %q, want %q", out.Parent, filepath.Dir(root))
	}
	if len(out.Dirs) != 2 || out.Dirs[0] != "alpha" || out.Dirs[1] != "beta" {
		t.Errorf("dirs = %v, want [alpha beta] (sorted, no files, no hidden)", out.Dirs)
	}

	// Relative path → 400.
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/fs/dirs?path=relative", editor, nil, nil); code != http.StatusBadRequest {
		t.Errorf("relative path: status = %d, want 400", code)
	}

	// Empty path → home (just needs to answer 200 with an absolute path).
	t.Setenv("HOME", root)
	var home FsDirsDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/fs/dirs", editor, nil, &home); code != http.StatusOK {
		t.Fatalf("fs/dirs (home): status = %d", code)
	}
	if !filepath.IsAbs(home.Path) {
		t.Errorf("home path = %q, want absolute", home.Path)
	}
}

// withTestEngine configures a backup destination for the test hub and
// returns the engine the handlers will resolve for it (rooted at the hub's
// own <dir>/<slug>/ subtree).
func withTestEngine(t *testing.T, s *Server) *backup.Engine {
	t.Helper()
	set := hubSet(t, s, testHubID)
	if err := saveHubBackupConfig(set.root, hubBackupConfig{BackupDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	eng, err := s.backupEngineFor(set)
	if err != nil || eng == nil {
		t.Fatalf("backupEngineFor = %v, %v", eng, err)
	}
	return eng
}

func TestBackupVerifyEndpoint(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	// No engine → 503.
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/verify", editor,
		BackupVerifyRequest{Path: "manual/x"}, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("verify without engine: status = %d, want 503", code)
	}

	eng := withTestEngine(t, s)

	// Path escaping the backup dir → 400 (traversal and absolute alike).
	for _, bad := range []string{"../outside", "manual/../../etc", "/etc/passwd", ""} {
		if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/verify", editor,
			BackupVerifyRequest{Path: bad}, nil); code != http.StatusBadRequest {
			t.Errorf("verify path %q: status = %d, want 400", bad, code)
		}
	}

	// Nonexistent snapshot → 400 (no manifest to read).
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/verify", editor,
		BackupVerifyRequest{Path: "manual/20200101-000000"}, nil); code != http.StatusBadRequest {
		t.Errorf("verify missing snapshot: status = %d, want 400", code)
	}

	// A real snapshot verifies OK end to end.
	if _, err := hubSet(t, s, testHubID).tasks.Create(taskTestProject, "hub-1", "Test Project",
		tasks.Draft{Title: "backed up"}, tasks.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}
	var sum BackupSummaryDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/run", editor, nil, &sum); code != http.StatusOK {
		t.Fatalf("run: status = %d", code)
	}
	var rep BackupVerifyReportDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/verify", editor,
		BackupVerifyRequest{Path: sum.Path}, &rep); code != http.StatusOK {
		t.Fatalf("verify: status = %d", code)
	}
	if !rep.OK || len(rep.Files) == 0 || rep.Path != sum.Path || rep.Kind != "manual" {
		t.Errorf("verify report = %+v", rep)
	}

	// Corrupt one byte in the snapshot → the report fails with HashOK=false.
	snapDir := filepath.Join(eng.Dir, filepath.FromSlash(sum.Path))
	target := filepath.Join(snapDir, filepath.FromSlash(rep.Files[0].Path))
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0x01
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	var rep2 BackupVerifyReportDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/verify", editor,
		BackupVerifyRequest{Path: sum.Path}, &rep2); code != http.StatusOK {
		t.Fatalf("verify corrupted: status = %d", code)
	}
	if rep2.OK || rep2.Files[0].HashOK {
		t.Errorf("corrupted verify report = %+v, want !OK with HashOK=false", rep2)
	}
}

func TestBackupRestoreValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	// No engine → 503.
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/restore", editor,
		BackupRestoreRequest{Path: "manual/x", Confirm: "x"}, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("restore without engine: status = %d, want 503", code)
	}

	withTestEngine(t, s)

	// Path escape → 400.
	for _, bad := range []string{"../outside", "manual/../../etc", "/abs"} {
		if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/restore", editor,
			BackupRestoreRequest{Path: bad, Confirm: filepath.Base(bad)}, nil); code != http.StatusBadRequest {
			t.Errorf("restore path %q: status = %d, want 400", bad, code)
		}
	}

	// Confirm mismatch → 400, even for a plausible path.
	var sum BackupSummaryDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/run", editor, nil, &sum); code != http.StatusOK {
		t.Fatalf("run: status = %d", code)
	}
	for _, confirm := range []string{"", "wrong", "manual/" + filepath.Base(sum.Path)} {
		if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/restore", editor,
			BackupRestoreRequest{Path: sum.Path, Confirm: confirm}, nil); code != http.StatusBadRequest {
			t.Errorf("restore confirm %q: status = %d, want 400", confirm, code)
		}
	}
}

// TestBackupRestoreEndToEndMigration proves the restore → schema-migration
// tie: a snapshot holding a v1-era tasks.json restores cleanly, and the
// tasks store then migrates it forward on next load (.v1.bak snapshot, v2 +
// stamp on next save) — old backups stay restorable forever.
func TestBackupRestoreEndToEndMigration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")
	eng := withTestEngine(t, s)

	// Hand-build a snapshot holding a v1-era tasks.json with a manifest that
	// declares schemaVersion 1 and the matching hash.
	const v1Tasks = `{
  "version": 1,
  "projectId": "urn:proj:restored",
  "hubId": "hub-1",
  "projectName": "Restored Project",
  "nextTaskId": 2,
  "tasks": [{"id": "t1", "num": 1, "title": "From the v1 era", "status": "todo",
    "priority": "medium", "createdBy": {"id": "u1"}, "createdAt": "2025-01-02T03:04:05Z",
    "updatedAt": "2025-01-02T03:04:05Z", "docRefs": [], "rank": 1024}]
}`
	snapName := "20250101-000000"
	snapDir := filepath.Join(eng.Dir, "manual", snapName)
	rel := "tasks/urn_proj_restored/tasks.json"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(snapDir, filepath.FromSlash(rel))), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, filepath.FromSlash(rel)), []byte(v1Tasks), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(v1Tasks))
	manifest := backup.Manifest{
		ManifestVersion: backup.ManifestVersion,
		AppVersion:      "test",
		CreatedAt:       time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Kind:            backup.KindManual,
		Files: []backup.ManifestFile{{
			Path: rel, Store: "tasks", SHA256: hex.EncodeToString(sum[:]),
			Size: int64(len(v1Tasks)), SchemaVersion: 1,
		}},
	}
	mdata, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "manifest.json"), mdata, 0600); err != nil {
		t.Fatal(err)
	}

	// Restore through the endpoint with the typed confirmation.
	var resp BackupRestoreResponse
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/restore", editor,
		BackupRestoreRequest{Path: "manual/" + snapName, Confirm: snapName}, &resp); code != http.StatusOK {
		t.Fatalf("restore: status = %d, want 200", code)
	}
	if !resp.Restarting {
		t.Error("restore response restarting = false")
	}

	// The pre-restore safety snapshot was taken before any write.
	preEntries, err := os.ReadDir(filepath.Join(eng.Dir, "pre-restore"))
	if err != nil || len(preEntries) != 1 {
		t.Fatalf("pre-restore tier = %v, %v (want exactly 1)", preEntries, err)
	}

	// The v1 file landed in the HUB PROFILE byte-identical (store/pins files
	// restore into the session hub's root, never the config-dir root).
	hubRoot := hubSet(t, s, testHubID).root
	restored := filepath.Join(hubRoot, filepath.FromSlash(rel))
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatalf("restored file: %v", err)
	}
	if string(got) != v1Tasks {
		t.Errorf("restored tasks.json differs from the snapshot copy")
	}

	// A store over the restored dir loads it, migrating v1→v2: the load
	// snapshots the v1 bytes, and the next save persists version 2 + stamp.
	st, err := tasks.NewStore(filepath.Join(hubRoot, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	list, err := st.List("urn:proj:restored")
	if err != nil {
		t.Fatalf("List after restore: %v", err)
	}
	if len(list) != 1 || list[0].Title != "From the v1 era" {
		t.Fatalf("restored data = %+v", list)
	}
	bak, err := os.ReadFile(restored + ".v1.bak")
	if err != nil {
		t.Fatalf("no .v1.bak after migrating load: %v", err)
	}
	if !strings.Contains(string(bak), `"version": 1`) {
		t.Errorf(".v1.bak is not the v1 original")
	}
	if _, err := st.Create("urn:proj:restored", "hub-1", "Restored Project",
		tasks.Draft{Title: "post-restore"}, tasks.UserRef{ID: "u1"}); err != nil {
		t.Fatal(err)
	}
	var pf struct {
		Version int `json:"version"`
		Schema  struct {
			CreatedAt time.Time `json:"createdAt"`
		} `json:"schema"`
	}
	data, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatal(err)
	}
	if pf.Version != 2 || pf.Schema.CreatedAt.IsZero() {
		t.Errorf("post-save envelope = version %d, stamp %v; want v2 with a backfilled stamp", pf.Version, pf.Schema.CreatedAt)
	}
}
