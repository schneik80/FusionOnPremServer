package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/schneik80/fusionlocalserver/backup"
)

func TestBackupConfigValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newTaskTestServer(t)
	s.backupPoke = make(chan struct{}, 1)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

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

	// Valid → 200, persisted, engine constructed, and the port setting (if
	// any) survives the load-modify-save cycle.
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
	set, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if set.BackupDir != backupDir || set.BackupTime != "04:15" || !set.BackupEnabled || set.Port != 9123 {
		t.Errorf("persisted settings = %+v", set)
	}
	if s.backupEngine() == nil {
		t.Error("engine not constructed after valid config")
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

	// With an engine → 200 and a snapshot on disk.
	backupDir := t.TempDir()
	s.backupMu.Lock()
	s.backups = &backup.Engine{
		Dir:        backupDir,
		Sources:    []backup.Source{backup.StoreSource("tasks", s.tasks.Snapshot)},
		AppVersion: "test",
	}
	s.backupMu.Unlock()

	var sum BackupSummaryDTO
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/admin/backups/run", editor, nil, &sum); code != http.StatusOK {
		t.Fatalf("run with engine: status = %d, want 200", code)
	}
	if sum.Kind != "manual" || sum.CreatedAt == "" {
		t.Errorf("run summary = %+v", sum)
	}
	entries, err := os.ReadDir(filepath.Join(backupDir, "manual"))
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
