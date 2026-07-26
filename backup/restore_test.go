package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/tasks"
)

// restoreExpected mirrors the server's expectedSchemaVersion for the
// fixtures used here: tasks.json is v2, everything else unversioned.
func restoreExpected(store, rel string) (int, bool) {
	if store == "tasks" {
		return 2, true
	}
	return 0, false
}

const fixtureTasksV2 = `{
  "version": 2,
  "projectId": "urn:p:a",
  "hubId": "hub-1",
  "projectName": "Proj A",
  "nextTaskId": 2,
  "tasks": [{"id": "t1", "num": 1, "title": "Original", "status": "todo", "priority": "medium",
    "createdBy": {"id": "u1"}, "createdAt": "2026-01-02T03:04:05Z",
    "updatedAt": "2026-01-02T03:04:05Z", "docRefs": [], "rank": 1024}]
}`

// writeFixtureConfigDir lays out a live config dir: one tasks project,
// config.json with a real secret, server.json with a port.
func writeFixtureConfigDir(t *testing.T) (configDir, tasksFile string) {
	t.Helper()
	configDir = t.TempDir()
	projDir := filepath.Join(configDir, "tasks", "urn_p_a")
	if err := os.MkdirAll(projDir, 0700); err != nil {
		t.Fatal(err)
	}
	tasksFile = filepath.Join(projDir, "tasks.json")
	if err := os.WriteFile(tasksFile, []byte(fixtureTasksV2), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"),
		[]byte(`{"client_id":"cid-backup","client_secret":"live-secret","region":"EMEA"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.json"),
		[]byte(`{"port":9999}`), 0600); err != nil {
		t.Fatal(err)
	}
	return configDir, tasksFile
}

// fixtureEngine builds an engine over the fixture config dir's real tasks
// store plus the redacted config source.
func fixtureEngine(t *testing.T, configDir string) *Engine {
	t.Helper()
	st, err := tasks.NewStore(filepath.Join(configDir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{
		Dir:        t.TempDir(),
		Sources:    []Source{StoreSource("tasks", st.Snapshot), ConfigSource(configDir)},
		AppVersion: "1.2.3",
	}
}

// onlySnapshotDir returns the single snapshot dir under <dir>/<kind>.
func onlySnapshotDir(t *testing.T, dir string, kind Kind) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, string(kind)))
	if err != nil || len(entries) != 1 {
		t.Fatalf("%s tier = %v, %v (want exactly 1)", kind, entries, err)
	}
	return filepath.Join(dir, string(kind), entries[0].Name())
}

func TestRestoreRoundTrip(t *testing.T) {
	configDir, tasksFile := writeFixtureConfigDir(t)
	eng := fixtureEngine(t, configDir)

	if _, err := eng.Run(KindManual); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snapDir := onlySnapshotDir(t, eng.Dir, KindManual)

	// Mutate every live file after the backup.
	mutatedTasks := strings.Replace(fixtureTasksV2, "Original", "Mutated", 1)
	if err := os.WriteFile(tasksFile, []byte(mutatedTasks), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"),
		[]byte(`{"client_id":"cid-live","client_secret":"live-secret-2","region":"US"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.json"),
		[]byte(`{"port":1234}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := eng.Restore(snapDir, configDir, restoreExpected); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// tasks.json is byte-identical to the backed-up copy.
	got, err := os.ReadFile(tasksFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fixtureTasksV2 {
		t.Errorf("tasks.json not restored byte-identical:\n%s", got)
	}

	// server.json policy: never restored — the live (mutated) file stands.
	sj, err := os.ReadFile(filepath.Join(configDir, "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(sj) != `{"port":1234}` {
		t.Errorf("server.json = %s, want the live file untouched", sj)
	}

	// config.json: backup fields restored, live client_secret preserved.
	var cfg map[string]any
	cj, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cj, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["client_id"] != "cid-backup" || cfg["region"] != "EMEA" {
		t.Errorf("config fields not restored from backup: %v", cfg)
	}
	if cfg["client_secret"] != "live-secret-2" {
		t.Errorf("client_secret = %v, want the live value preserved", cfg["client_secret"])
	}

	// The pre-restore snapshot exists and froze the MUTATED (pre-restore)
	// state, with the secret blanked as every backup does.
	preDir := onlySnapshotDir(t, eng.Dir, KindPreRestore)
	pre, err := os.ReadFile(filepath.Join(preDir, "tasks", "urn_p_a", "tasks.json"))
	if err != nil {
		t.Fatalf("pre-restore tasks.json: %v", err)
	}
	if string(pre) != mutatedTasks {
		t.Errorf("pre-restore snapshot holds %q-era data, want the mutated state", "backup")
	}
	var preCfg map[string]any
	pcj, err := os.ReadFile(filepath.Join(preDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(pcj, &preCfg); err != nil {
		t.Fatal(err)
	}
	if preCfg["client_secret"] != "" {
		t.Errorf("pre-restore config.json secret = %v, want blanked", preCfg["client_secret"])
	}
}

// writeHandmadeSnapshot builds a snapshot dir with the given files and a
// consistent manifest, then lets mutate doctor the manifest before writing.
func writeHandmadeSnapshot(t *testing.T, root string, files map[string]struct {
	data []byte
	sv   int
}, appVersion string, mutate func(*Manifest)) string {
	t.Helper()
	snapDir := filepath.Join(root, "manual", "20260101-000000")
	m := &Manifest{
		ManifestVersion: ManifestVersion,
		AppVersion:      appVersion,
		CreatedAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Kind:            KindManual,
		Files:           []ManifestFile{},
	}
	for rel, f := range files {
		dest := filepath.Join(snapDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, f.data, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(f.data)
		m.Files = append(m.Files, ManifestFile{
			Path: rel, Store: strings.SplitN(rel, "/", 2)[0],
			SHA256: hex.EncodeToString(sum[:]), Size: int64(len(f.data)), SchemaVersion: f.sv,
		})
	}
	if mutate != nil {
		mutate(m)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return snapDir
}

func TestRestoreRefusesFutureSchemaVersion(t *testing.T) {
	root := t.TempDir()
	snapDir := writeHandmadeSnapshot(t, root, map[string]struct {
		data []byte
		sv   int
	}{
		"tasks/p/tasks.json": {data: []byte(`{"version":99,"projectId":"urn:p"}`), sv: 99},
	}, "1.0.0", nil)

	configDir := t.TempDir()
	eng := &Engine{Dir: root, AppVersion: "1.2.3"}
	err := eng.Restore(snapDir, configDir, restoreExpected)
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Restore future schema: err = %v, want a clear 'newer' refusal", err)
	}
	// Refused before anything happened: no pre-restore snapshot, no copy.
	if _, serr := os.Stat(filepath.Join(root, string(KindPreRestore))); !os.IsNotExist(serr) {
		t.Error("pre-restore snapshot was taken despite refusal")
	}
	if _, serr := os.Stat(filepath.Join(configDir, "tasks", "p", "tasks.json")); !os.IsNotExist(serr) {
		t.Error("file was copied despite refusal")
	}
}

func TestRestoreRefusesNewerAppVersion(t *testing.T) {
	root := t.TempDir()
	snapDir := writeHandmadeSnapshot(t, root, map[string]struct {
		data []byte
		sv   int
	}{
		"tasks/p/tasks.json": {data: []byte(`{"version":2}`), sv: 2},
	}, "9.9.9", nil)

	eng := &Engine{Dir: root, AppVersion: "1.2.3"}
	err := eng.Restore(snapDir, t.TempDir(), restoreExpected)
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Restore newer app: err = %v, want a clear 'newer' refusal", err)
	}

	// A dev build restores anything — "dev" bypasses the version gate.
	devEng := &Engine{Dir: root, AppVersion: "dev"}
	configDir := t.TempDir()
	if err := devEng.Restore(snapDir, configDir, restoreExpected); err != nil {
		t.Fatalf("Restore as dev: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(configDir, "tasks", "p", "tasks.json")); serr != nil {
		t.Errorf("dev restore did not copy: %v", serr)
	}
}

func TestRestoreRefusesCorruptSnapshotFile(t *testing.T) {
	root := t.TempDir()
	snapDir := writeHandmadeSnapshot(t, root, map[string]struct {
		data []byte
		sv   int
	}{
		"tasks/p/tasks.json": {data: []byte(`{"version":2}`), sv: 2},
	}, "1.0.0", nil)
	// Corrupt the snapshot copy after the manifest recorded its hash.
	if err := os.WriteFile(filepath.Join(snapDir, "tasks", "p", "tasks.json"),
		[]byte(`{"version":3}`), 0600); err != nil {
		t.Fatal(err)
	}

	eng := &Engine{Dir: root, AppVersion: "1.2.3"}
	configDir := t.TempDir()
	err := eng.Restore(snapDir, configDir, restoreExpected)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Restore corrupt file: err = %v, want a checksum refusal", err)
	}
	if _, serr := os.Stat(filepath.Join(configDir, "tasks", "p", "tasks.json")); !os.IsNotExist(serr) {
		t.Error("corrupt file was copied")
	}
}

func TestRestoreConfigSecretWithAbsentLiveFile(t *testing.T) {
	root := t.TempDir()
	snapDir := writeHandmadeSnapshot(t, root, map[string]struct {
		data []byte
		sv   int
	}{
		"config.json": {data: []byte(`{"client_id":"cid","client_secret":"","region":"EMEA"}`), sv: 0},
	}, "1.0.0", nil)

	eng := &Engine{Dir: root, AppVersion: "1.2.3"}
	configDir := t.TempDir() // no live config.json
	if err := eng.Restore(snapDir, configDir, restoreExpected); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	var cfg map[string]any
	data, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["client_secret"] != "" || cfg["client_id"] != "cid" {
		t.Errorf("config.json = %v, want blank secret + backup fields", cfg)
	}
}

func TestSnapshotNewerThanApp(t *testing.T) {
	cases := []struct {
		snap, app string
		want      bool
	}{
		{"1.2.4", "1.2.3", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.2", "1.2.3", false},
		{"v1.10.0", "1.9.9", true},
		{"1.2.3.1", "1.2.3", true},
		{"2.0-beta", "1.9", true},
		{"dev", "1.0.0", false},
		{"2.0.0", "dev", false},
		{"garbage", "1.0.0", false},
		{"", "1.0.0", false},
	}
	for _, c := range cases {
		if got := snapshotNewerThanApp(c.snap, c.app); got != c.want {
			t.Errorf("snapshotNewerThanApp(%q, %q) = %v, want %v", c.snap, c.app, got, c.want)
		}
	}
}
