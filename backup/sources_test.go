package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeFixture populates a fake config dir with everything a real one holds —
// including every file that must NEVER reach a backup.
func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"config.json":         `{"client_id":"abc123","client_secret":"SUPER-SECRET","region":"EMEA"}`,
		"server.json":         `{"port":9000,"backupDir":"/backups"}`,
		"pins-hub-a.json":     `{"version":1,"pins":[]}`,
		"pins-hub-b.json":     `[{"id":"legacy"}]`, // legacy bare-array pins (v0)
		"pins-hub-a.json.bak": `{"version":1,"pins":[]}`,
		// The forbidden set: secrets, keys, logs, stale junk.
		"sessions.enc": "ENCRYPTED-SESSIONS",
		"session.key":  "AES-KEY-BYTES",
		"tls-cert.pem": "CERT",
		"tls-key.pem":  "KEY",
		"server.log":   "log line\n",
		"tokens.json":  `{"access_token":"leak"}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models", "cache.bin"), []byte("blob"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestConfigAndPinsSourcesAllowListOnly(t *testing.T) {
	cfgDir := writeFixture(t)
	e := &Engine{
		Dir:        t.TempDir(),
		Sources:    []Source{PinsSource(cfgDir), ConfigSource(cfgDir)},
		AppVersion: "test",
	}
	m, err := e.Run(KindManual)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := map[string]ManifestFile{}
	for _, f := range m.Files {
		got[f.Path] = f
	}

	// Exactly the allow-listed set — nothing else.
	want := []string{"config.json", "server.json", "pins-hub-a.json", "pins-hub-b.json"}
	if len(got) != len(want) {
		t.Fatalf("manifest paths = %v, want exactly %v", m.Files, want)
	}
	for _, p := range want {
		if _, ok := got[p]; !ok {
			t.Errorf("manifest missing %s", p)
		}
	}

	// The forbidden names never appear — the point of the allow-list.
	for _, forbidden := range []string{
		"sessions.enc", "session.key", "tls-cert.pem", "tls-key.pem",
		"server.log", "tokens.json", "models/cache.bin", "pins-hub-a.json.bak",
	} {
		if _, ok := got[forbidden]; ok {
			t.Errorf("forbidden file %s reached the manifest", forbidden)
		}
	}

	// The backed-up config.json has the secret blanked but everything else
	// intact; the raw secret string appears nowhere in the bytes.
	snapDir := filepath.Join(e.Dir, "manual")
	entries, _ := os.ReadDir(snapDir)
	if len(entries) != 1 {
		t.Fatalf("manual tier = %v", entries)
	}
	data, err := os.ReadFile(filepath.Join(snapDir, entries[0].Name(), "config.json"))
	if err != nil {
		t.Fatalf("reading backed-up config.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("backed-up config.json is not JSON: %v", err)
	}
	if cfg["client_secret"] != "" {
		t.Errorf("client_secret = %q, want blanked", cfg["client_secret"])
	}
	if cfg["client_id"] != "abc123" || cfg["region"] != "EMEA" {
		t.Errorf("non-secret fields mangled: %v", cfg)
	}

	// Pins schema versions: enveloped v1 peeks 1, legacy bare array reports 0.
	if got["pins-hub-a.json"].SchemaVersion != 1 {
		t.Errorf("pins-hub-a schemaVersion = %d, want 1", got["pins-hub-a.json"].SchemaVersion)
	}
	if got["pins-hub-b.json"].SchemaVersion != 0 {
		t.Errorf("legacy pins schemaVersion = %d, want 0", got["pins-hub-b.json"].SchemaVersion)
	}
}

func TestConfigSourceAbsentFilesSkip(t *testing.T) {
	e := &Engine{
		Dir:     t.TempDir(),
		Sources: []Source{ConfigSource(t.TempDir())}, // empty config dir
	}
	m, err := e.Run(KindManual)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(m.Files) != 0 {
		t.Errorf("manifest = %v, want empty", m.Files)
	}
}
