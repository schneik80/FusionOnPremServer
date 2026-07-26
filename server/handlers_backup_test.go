package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
		Hub:             testHubID,
		HubSlug:         testHubID, // Slug("hub-1") == "hub-1"
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

// ---- phase H3: hub-identity hardening ----

// makeHubSnapshot hand-builds a uniquely named manual snapshot in eng's tree:
// one valid tasks file, a consistent manifest stamped with eng's hub identity,
// then `mutate` doctors the manifest — simulating exactly what a foreign or
// pre-isolation snapshot looks like on disk. Returns the List()-style relative
// path ("manual/<name>").
func makeHubSnapshot(t *testing.T, eng *backup.Engine, name string, mutate func(*backup.Manifest)) string {
	t.Helper()
	const content = `{"version":2}`
	const rel = "tasks/urn_p_snap/tasks.json"
	snapDir := filepath.Join(eng.Dir, "manual", name)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(snapDir, filepath.FromSlash(rel))), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, filepath.FromSlash(rel)), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	m := backup.Manifest{
		ManifestVersion: backup.ManifestVersion,
		AppVersion:      "test",
		Hub:             eng.Hub,
		HubSlug:         eng.HubSlug,
		CreatedAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Kind:            backup.KindManual,
		Files: []backup.ManifestFile{{
			Path: rel, Store: "tasks", SHA256: hex.EncodeToString(sum[:]),
			Size: int64(len(content)), SchemaVersion: 2,
		}},
	}
	if mutate != nil {
		mutate(&m)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return "manual/" + name
}

// TestBackupRestoreRefusesForeignAndLegacyManifests drives the HANDLER's
// pre-check: a pre-v2 manifest, a future manifest, and both foreign-hub
// branches all answer 400 with the specific refusal message, and the live
// data is untouched (no pre-restore snapshot was even taken).
func TestBackupRestoreRefusesForeignAndLegacyManifests(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")
	eng := withTestEngine(t, s)

	cases := []struct {
		name    string
		mutate  func(*backup.Manifest)
		wantMsg string
	}{
		{"v1 manifest", func(m *backup.Manifest) {
			m.ManifestVersion = 1
			m.Hub, m.HubSlug = "", ""
		}, "predates hub isolation and cannot be restored; take a fresh backup"},
		{"future manifest", func(m *backup.Manifest) {
			m.ManifestVersion = backup.ManifestVersion + 1
		}, "newer than this build understands"},
		{"foreign slug", func(m *backup.Manifest) {
			m.Hub, m.HubSlug = "hub-2", "hub-2"
		}, "cross-hub restore refused"},
		{"foreign raw id, same slug", func(m *backup.Manifest) {
			m.Hub = "some-other-raw-id"
		}, "cross-hub restore refused"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("20260101-00000%d", i)
			path := makeHubSnapshot(t, eng, name, tc.mutate)

			code, eb := errBody(t, ts.URL, http.MethodPost, "/api/admin/backups/restore", editor,
				BackupRestoreRequest{Path: path, Confirm: name})
			if code != http.StatusBadRequest {
				t.Fatalf("restore: status = %d, want 400", code)
			}
			if !strings.Contains(eb.Error, tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", eb.Error, tc.wantMsg)
			}
			// Refused before anything happened: no pre-restore snapshot.
			if _, err := os.Stat(filepath.Join(eng.Dir, "pre-restore")); !os.IsNotExist(err) {
				t.Errorf("pre-restore snapshot exists despite refusal: %v", err)
			}
		})
	}

	// A foreign snapshot still VERIFIES — its bytes are clean — but the
	// report carries the hub warning the UI surfaces.
	foreignPath := makeHubSnapshot(t, eng, "20260101-000010", func(m *backup.Manifest) {
		m.Hub, m.HubSlug = "hub-2", "hub-2"
	})
	var rep BackupVerifyReportDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/verify", editor,
		BackupVerifyRequest{Path: foreignPath}, &rep); code != http.StatusOK {
		t.Fatalf("verify: status = %d", code)
	}
	if !rep.OK {
		t.Errorf("foreign snapshot's files should still verify OK, got %+v", rep)
	}
	if !strings.Contains(rep.Warning, "not this session's hub") {
		t.Errorf("verify warning = %q, want the foreign-hub notice", rep.Warning)
	}

	// A v1 snapshot LISTS with a warning (visible, not restorable).
	v1Path := makeHubSnapshot(t, eng, "20260101-000011", func(m *backup.Manifest) {
		m.ManifestVersion = 1
	})
	var list BackupListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/backups", editor, nil, &list); code != http.StatusOK {
		t.Fatalf("list: status = %d", code)
	}
	found := false
	for _, b := range list.Backups {
		if b.Path == v1Path {
			found = true
			if !strings.Contains(b.Warning, "predates hub isolation") {
				t.Errorf("v1 row warning = %q", b.Warning)
			}
		}
	}
	if !found {
		t.Error("v1 snapshot missing from the list")
	}
}

// TestBackupSchedulerPerHubProfiles drives the scheduler's per-hub posture
// over two hubs with DIFFERENT destinations and schedules: min-next selection
// (pure, controlled clock), each hub's snapshots landing in its own tree with
// zero foreign bytes, a disabled hub skipped, and one hub's failure never
// stopping the other. _unassigned without a hub.json never participates.
func TestBackupSchedulerPerHubProfiles(t *testing.T) {
	s := newIsoTestServer(t)
	setA := hubSet(t, s, isoHubA)
	setB := hubSet(t, s, isoHubB)
	if _, err := setA.tasks.Create(isoProject, isoHubA, "Proj",
		tasks.Draft{Title: "SCHED-MARKER-A"}, tasks.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := setB.tasks.Create(isoProject, isoHubB, "Proj",
		tasks.Draft{Title: "SCHED-MARKER-B"}, tasks.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}

	dirA, dirB := t.TempDir(), t.TempDir()
	if err := saveHubBackupConfig(setA.root, hubBackupConfig{BackupDir: dirA, BackupTime: "01:00", BackupEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := saveHubBackupConfig(setB.root, hubBackupConfig{BackupDir: dirB, BackupTime: "02:00", BackupEnabled: true}); err != nil {
		t.Fatal(err)
	}
	// An _unassigned profile with an enabled config but no hub.json: the
	// scheduler must skip it (getBySlug cannot resolve a hub identity).
	unassignedRoot := filepath.Join(s.hubs.configDir, "hubs", "_unassigned")
	if err := os.MkdirAll(unassignedRoot, 0700); err != nil {
		t.Fatal(err)
	}
	dirU := t.TempDir()
	if err := saveHubBackupConfig(unassignedRoot, hubBackupConfig{BackupDir: dirU, BackupTime: "01:00", BackupEnabled: true}); err != nil {
		t.Fatal(err)
	}

	// Min-next selection with a controlled clock: before both slots the
	// earlier schedule wins; between them the later one is next (the earlier
	// hub's next occurrence is tomorrow).
	day := time.Date(2026, 7, 1, 0, 30, 0, 0, time.Local)
	if got, want := s.nextScheduledBackup(day), time.Date(2026, 7, 1, 1, 0, 0, 0, time.Local); !got.Equal(want) {
		t.Errorf("nextScheduledBackup(00:30) = %v, want %v", got, want)
	}
	between := time.Date(2026, 7, 1, 1, 30, 0, 0, time.Local)
	if got, want := s.nextScheduledBackup(between), time.Date(2026, 7, 1, 2, 0, 0, 0, time.Local); !got.Equal(want) {
		t.Errorf("nextScheduledBackup(01:30) = %v, want %v", got, want)
	}

	countDaily := func(dir, slug string) int {
		entries, err := os.ReadDir(filepath.Join(dir, slug, "daily"))
		if err != nil {
			return 0
		}
		return len(entries)
	}

	// Both due (computedAt far in the past): each hub snapshots into ITS OWN
	// tree, stamped with its own identity, no foreign bytes.
	past := time.Now().Add(-25 * time.Hour)
	s.runDueHubBackups(past)
	if n := countDaily(dirA, isoHubA); n != 1 {
		t.Fatalf("hub A daily snapshots = %d, want 1", n)
	}
	if n := countDaily(dirB, isoHubB); n != 1 {
		t.Fatalf("hub B daily snapshots = %d, want 1", n)
	}
	if entries, _ := os.ReadDir(dirU); len(entries) != 0 {
		t.Errorf("_unassigned was backed up: %v", entries)
	}
	for _, chk := range []struct{ dir, slug, own, foreign string }{
		{dirA, isoHubA, "SCHED-MARKER-A", "SCHED-MARKER-B"},
		{dirB, isoHubB, "SCHED-MARKER-B", "SCHED-MARKER-A"},
	} {
		tree := treeSnapshot(t, filepath.Join(chk.dir, chk.slug))
		foundOwn := false
		for rel, data := range tree {
			if strings.Contains(data, chk.foreign) {
				t.Errorf("%s: foreign marker in %s", chk.slug, rel)
			}
			if strings.Contains(data, chk.own) {
				foundOwn = true
			}
		}
		if !foundOwn {
			t.Errorf("%s: own marker missing from its snapshot tree", chk.slug)
		}
		snaps, err := os.ReadDir(filepath.Join(chk.dir, chk.slug, "daily"))
		if err != nil || len(snaps) == 0 {
			t.Fatalf("%s daily tier: %v %v", chk.slug, snaps, err)
		}
		m, err := backup.ReadManifest(filepath.Join(chk.dir, chk.slug, "daily", snaps[0].Name()))
		if err != nil {
			t.Fatal(err)
		}
		if m.Hub != chk.slug || m.HubSlug != chk.slug {
			t.Errorf("%s manifest identity = %q/%q", chk.slug, m.Hub, m.HubSlug)
		}
	}

	// Hub B disabled → skipped while A still runs.
	if err := saveHubBackupConfig(setB.root, hubBackupConfig{BackupDir: dirB, BackupTime: "02:00", BackupEnabled: false}); err != nil {
		t.Fatal(err)
	}
	s.runDueHubBackups(past)
	if n := countDaily(dirA, isoHubA); n != 2 {
		t.Errorf("hub A daily snapshots after second run = %d, want 2", n)
	}
	if n := countDaily(dirB, isoHubB); n != 1 {
		t.Errorf("disabled hub B gained a snapshot: %d", n)
	}

	// Hub A failing (its destination is a FILE) must not stop hub B — A sorts
	// first, so B running proves the loop survives a predecessor's failure.
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveHubBackupConfig(setA.root, hubBackupConfig{BackupDir: notADir, BackupTime: "01:00", BackupEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := saveHubBackupConfig(setB.root, hubBackupConfig{BackupDir: dirB, BackupTime: "02:00", BackupEnabled: true}); err != nil {
		t.Fatal(err)
	}
	s.runDueHubBackups(past)
	if n := countDaily(dirB, isoHubB); n != 2 {
		t.Errorf("hub B daily snapshots after hub A's failure = %d, want 2 (failure must not cascade)", n)
	}
}

// TestBackupRestoreTouchesOnlySessionHub proves restore's blast radius: the
// pre-restore safety snapshot lands under the session hub's OWN backup
// subtree, and the restore (including its store Reset) rewrites only the
// session hub — hub B's files and live store are byte-identical after.
func TestBackupRestoreTouchesOnlySessionHub(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newIsoTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io") // locked to hub A

	setA := hubSet(t, s, isoHubA)
	setB := hubSet(t, s, isoHubB)
	if _, err := setA.tasks.Create(isoProject, isoHubA, "Proj",
		tasks.Draft{Title: "RESTORE-KEEP"}, tasks.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}
	dirA := t.TempDir()
	if err := saveHubBackupConfig(setA.root, hubBackupConfig{BackupDir: dirA}); err != nil {
		t.Fatal(err)
	}
	var sum BackupSummaryDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/run", editor, nil, &sum); code != http.StatusOK {
		t.Fatalf("run: status = %d", code)
	}

	// Post-backup state: another task in A (to be rolled back) and marker
	// state in B (must survive untouched).
	if _, err := setA.tasks.Create(isoProject, isoHubA, "Proj",
		tasks.Draft{Title: "RESTORE-DROP"}, tasks.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := setB.tasks.Create(isoProject, isoHubB, "Proj",
		tasks.Draft{Title: "MARKER-B-STATE"}, tasks.UserRef{ID: "u-editor"}); err != nil {
		t.Fatal(err)
	}
	beforeB := treeSnapshot(t, setB.root)

	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/restore", editor,
		BackupRestoreRequest{Path: sum.Path, Confirm: filepath.Base(sum.Path)}, nil); code != http.StatusOK {
		t.Fatalf("restore: status = %d", code)
	}

	// The pre-restore safety snapshot landed inside hub A's own subtree, and
	// nothing appeared at the destination root beside hub A's slug dir.
	pre, err := os.ReadDir(filepath.Join(dirA, isoHubA, "pre-restore"))
	if err != nil || len(pre) != 1 {
		t.Fatalf("pre-restore tier = %v, %v (want exactly 1, inside hub A's subtree)", pre, err)
	}
	rootEntries, err := os.ReadDir(dirA)
	if err != nil {
		t.Fatal(err)
	}
	if len(rootEntries) != 1 || rootEntries[0].Name() != isoHubA {
		t.Errorf("backup destination root = %v, want only %q", rootEntries, isoHubA)
	}

	// Hub A rolled back to the snapshot (the Reset dropped the cached
	// post-backup task); the API serves the restored state.
	var list TaskListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/tasks?projectId="+isoProject, editor, nil, &list); code != http.StatusOK {
		t.Fatalf("A list after restore = %d", code)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].Title != "RESTORE-KEEP" {
		t.Errorf("hub A after restore = %+v, want only RESTORE-KEEP", list.Tasks)
	}

	// Hub B: files byte-identical, live store still serving its state.
	assertTreeUnchanged(t, "hub B after hub A restore", beforeB, treeSnapshot(t, setB.root))
	lockHub(t, s, editor, isoHubB)
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/tasks?projectId="+isoProject, editor, nil, &list); code != http.StatusOK {
		t.Fatalf("B list after restore = %d", code)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].Title != "MARKER-B-STATE" {
		t.Errorf("hub B after restore = %+v, want its own state untouched", list.Tasks)
	}
}
