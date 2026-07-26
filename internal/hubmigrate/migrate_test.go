package hubmigrate

import (
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// envelope renders a realistic v2 store envelope naming hubID (optionally a
// hubName, which real files today do not carry but the probe tolerates).
func envelope(t *testing.T, projectID, hubID, hubName string) string {
	t.Helper()
	env := map[string]any{
		"version":     2,
		"schema":      map[string]any{},
		"projectId":   projectID,
		"hubId":       hubID,
		"projectName": "Project " + projectID,
	}
	if hubName != "" {
		env["hubName"] = hubName
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func pinsFileJSON(hubID string) string {
	return `{"version":1,"schema":{},"pins":[{"id":"item-1","name":"Thing","kind":"project","hub_id":"` + hubID + `","pinned_at":"2025-01-02T03:04:05Z"}]}`
}

func mustExist(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

func mustNotExist(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			t.Errorf("expected %s to be gone", rel)
		}
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}

func readJSON(t *testing.T, root, rel string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(readFile(t, root, rel)), &m); err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	return m
}

// snapshotTree maps every file's slash-relative path to its content, for
// exact before/after comparisons. The .migrated marker's content (a
// timestamp) is normalized so a rerun that rewrites it still compares equal.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		if d.Name() == MarkerName {
			out[rel] = "<marker>"
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// buildMixedFixture lays down the full pre-hub layout: three envelope stores
// across hubs A and B, chat siblings, one chat-only project, a corrupt
// envelope, two pins files (one for a pins-only hub C), a legacy server.json
// with global backup fields, and the global files that must stay put.
func buildMixedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// hub-A: project a1 has tasks+production+chat; a2 has whiteboards.
	write(t, dir, "tasks/urn_project_a1/tasks.json", envelope(t, "urn:project:a1", "hub-A", "Hub Aye"))
	write(t, dir, "production/urn_project_a1/production.json", envelope(t, "urn:project:a1", "hub-A", ""))
	write(t, dir, "whiteboards/urn_project_a2/whiteboards.json", envelope(t, "urn:project:a2", "hub-A", ""))
	write(t, dir, "whiteboards/urn_project_a2/doc-1.json", `{"schema":1,"records":[]}`)
	write(t, dir, "chat/urn_project_a1/meta.json", `{"version":2,"projectId":"urn:project:a1","channels":[]}`)
	write(t, dir, "chat/urn_project_a1/msg-c1.jsonl", `{"seq":1,"body":"hello"}`+"\n")

	// hub-B: b1 has tasks; b2 has production+chat.
	write(t, dir, "tasks/urn_project_b1/tasks.json", envelope(t, "urn:project:b1", "hub-B", ""))
	write(t, dir, "production/urn_project_b2/production.json", envelope(t, "urn:project:b2", "hub-B", ""))
	write(t, dir, "chat/urn_project_b2/meta.json", `{"version":2,"projectId":"urn:project:b2","channels":[]}`)

	// A chat project with no sibling anywhere, and a corrupt tasks envelope.
	write(t, dir, "chat/urn_project_orphan/meta.json", `{"version":2,"projectId":"urn:project:orphan","channels":[]}`)
	write(t, dir, "tasks/urn_project_corrupt/tasks.json", `{not json!`)

	// Pins: hub-A (profile also holds store data) and hub-C (pins-only).
	write(t, dir, "pins-hub-A.json", pinsFileJSON("hub-A"))
	write(t, dir, "pins-hub-C.json", pinsFileJSON("hub-C"))

	// Legacy server.json still carrying the global backup config.
	write(t, dir, "server.json", `{"port":9090,"backupDir":"/backups/fls","backupTime":"03:30","backupEnabled":true}`)

	// Globals that must never move.
	write(t, dir, "sessions.enc", "ENCRYPTED-SESSIONS")
	write(t, dir, "tls-cert.pem", "CERT")
	write(t, dir, "config.json", `{"client_id":"abc"}`)
	write(t, dir, "server.log", "log line\n")

	return dir
}

func TestRun_MixedFixture(t *testing.T) {
	dir := buildMixedFixture(t)
	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Exact expected placement.
	mustExist(t, dir,
		"hubs/hub-A/tasks/urn_project_a1/tasks.json",
		"hubs/hub-A/production/urn_project_a1/production.json",
		"hubs/hub-A/whiteboards/urn_project_a2/whiteboards.json",
		"hubs/hub-A/whiteboards/urn_project_a2/doc-1.json",
		"hubs/hub-A/chat/urn_project_a1/meta.json",
		"hubs/hub-A/chat/urn_project_a1/msg-c1.jsonl",
		"hubs/hub-A/pins-hub-A.json",
		"hubs/hub-A/hub.json",
		"hubs/hub-A/backup.json",
		"hubs/hub-B/tasks/urn_project_b1/tasks.json",
		"hubs/hub-B/production/urn_project_b2/production.json",
		"hubs/hub-B/chat/urn_project_b2/meta.json",
		"hubs/hub-B/hub.json",
		"hubs/hub-B/backup.json",
		"hubs/hub-C/pins-hub-C.json",
		"hubs/hub-C/hub.json",
		"hubs/hub-C/backup.json",
		"hubs/_unassigned/chat/urn_project_orphan/meta.json",
		"hubs/_unassigned/tasks/urn_project_corrupt/tasks.json",
		"hubs/"+MarkerName,
	)

	// Old store roots are gone; quarantine got no backup config.
	mustNotExist(t, dir, "tasks", "production", "whiteboards", "chat",
		"pins-hub-A.json", "pins-hub-C.json", "hubs/_unassigned/backup.json")

	// Globals untouched, byte for byte.
	for rel, want := range map[string]string{
		"sessions.enc": "ENCRYPTED-SESSIONS",
		"tls-cert.pem": "CERT",
		"config.json":  `{"client_id":"abc"}`,
		"server.log":   "log line\n",
	} {
		if got := readFile(t, dir, rel); got != want {
			t.Errorf("%s changed: %q", rel, got)
		}
	}

	// hub.json identity: id (and the name where an envelope carried one).
	hubA := readJSON(t, dir, "hubs/hub-A/hub.json")
	if hubA["hubId"] != "hub-A" || hubA["hubName"] != "Hub Aye" {
		t.Errorf("hub-A hub.json = %v", hubA)
	}
	if hubA["createdAt"] == nil || hubA["createdAt"] == "" {
		t.Errorf("hub-A hub.json missing createdAt: %v", hubA)
	}
	if got := readJSON(t, dir, "hubs/hub-B/hub.json")["hubId"]; got != "hub-B" {
		t.Errorf("hub-B hub.json hubId = %v", got)
	}
	// Pins-only profile seeded its hub.json from the pin entries.
	if got := readJSON(t, dir, "hubs/hub-C/hub.json")["hubId"]; got != "hub-C" {
		t.Errorf("hub-C hub.json hubId = %v", got)
	}

	// Backup config fan-out: same legacy values in every real hub profile.
	for _, hub := range []string{"hub-A", "hub-B", "hub-C"} {
		cfg := readJSON(t, dir, "hubs/"+hub+"/backup.json")
		if cfg["version"] != float64(1) || cfg["backupDir"] != "/backups/fls" ||
			cfg["backupTime"] != "03:30" || cfg["backupEnabled"] != true {
			t.Errorf("%s backup.json = %v", hub, cfg)
		}
	}

	// server.json keeps the port and nothing else backup-related.
	srv := readJSON(t, dir, "server.json")
	if srv["port"] != float64(9090) {
		t.Errorf("server.json port = %v", srv["port"])
	}
	for _, k := range []string{"backupDir", "backupTime", "backupEnabled"} {
		if _, ok := srv[k]; ok {
			t.Errorf("server.json still carries %s", k)
		}
	}

	// Moved content survived byte-identical (including the corrupt envelope —
	// quarantined, never dropped or repaired).
	if got := readFile(t, dir, "hubs/hub-A/chat/urn_project_a1/msg-c1.jsonl"); got != `{"seq":1,"body":"hello"}`+"\n" {
		t.Errorf("chat log changed: %q", got)
	}
	if got := readFile(t, dir, "hubs/_unassigned/tasks/urn_project_corrupt/tasks.json"); got != `{not json!` {
		t.Errorf("corrupt envelope changed: %q", got)
	}
}

func TestRun_DoubleRunIdempotent(t *testing.T) {
	dir := buildMixedFixture(t)
	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	before := snapshotTree(t, dir)

	// Second run fast-exits on the marker.
	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if diff := treeDiff(before, snapshotTree(t, dir)); diff != "" {
		t.Errorf("tree changed on marker fast-exit rerun:\n%s", diff)
	}

	// Even without the marker a rerun must be a no-op on the data.
	if err := os.Remove(filepath.Join(dir, "hubs", MarkerName)); err != nil {
		t.Fatal(err)
	}
	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("marker-less rerun: %v", err)
	}
	if diff := treeDiff(before, snapshotTree(t, dir)); diff != "" {
		t.Errorf("tree changed on marker-less rerun:\n%s", diff)
	}
}

func treeDiff(a, b map[string]string) string {
	var out string
	for k, va := range a {
		if vb, ok := b[k]; !ok {
			out += "missing after rerun: " + k + "\n"
		} else if va != vb {
			out += "content changed: " + k + "\n"
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out += "appeared after rerun: " + k + "\n"
		}
	}
	return out
}

// TestRun_CrashRerunResolvesChatSibling simulates a crash after tasks moved
// but before chat did: the rerun's sibling map must find the ALREADY-migrated
// tasks dir under hubs/ (this run moved nothing for that project) and route
// the chat dir to the same hub, not to quarantine — spec correctness risk #1.
func TestRun_CrashRerunResolvesChatSibling(t *testing.T) {
	dir := t.TempDir()

	// Half-migrated state a crash would leave: the tasks dir already sits in
	// the hub profile (with its hub.json), the chat dir is still at the old
	// root, and there is no marker.
	write(t, dir, "hubs/hub-A/tasks/urn_project_solo/tasks.json", envelope(t, "urn:project:solo", "hub-A", ""))
	write(t, dir, "hubs/hub-A/hub.json", `{"hubId":"hub-A","hubName":"","createdAt":"2025-01-01T00:00:00Z"}`)
	write(t, dir, "chat/urn_project_solo/meta.json", `{"version":2,"projectId":"urn:project:solo","channels":[]}`)
	write(t, dir, "chat/urn_project_solo/msg-c1.jsonl", "{}\n")

	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mustExist(t, dir,
		"hubs/hub-A/chat/urn_project_solo/meta.json",
		"hubs/hub-A/chat/urn_project_solo/msg-c1.jsonl",
		"hubs/"+MarkerName,
	)
	mustNotExist(t, dir, "hubs/_unassigned/chat/urn_project_solo", "chat")
}

func TestRun_ChatOnlyGoesUnassigned(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "chat/urn_project_lonely/meta.json", `{"version":2,"projectId":"urn:project:lonely","channels":[]}`)

	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mustExist(t, dir, "hubs/_unassigned/chat/urn_project_lonely/meta.json")
	mustNotExist(t, dir, "chat")
}

func TestRun_CorruptEnvelopeQuarantinedNeverDropped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "production/urn_project_bad/production.json", "\x00\x01 not json")
	write(t, dir, "production/urn_project_bad/extra.bin", "payload bytes")

	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mustExist(t, dir, "hubs/_unassigned/production/urn_project_bad/production.json")
	if got := readFile(t, dir, "hubs/_unassigned/production/urn_project_bad/extra.bin"); got != "payload bytes" {
		t.Errorf("quarantined payload changed: %q", got)
	}
	mustNotExist(t, dir, "production")
}

func TestRun_PinsOnlyProfileSeedsHubJSON(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pins-hub-Z.json", pinsFileJSON("hub-Z"))

	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mustExist(t, dir, "hubs/hub-Z/pins-hub-Z.json")
	mustNotExist(t, dir, "pins-hub-Z.json")
	if got := readJSON(t, dir, "hubs/hub-Z/hub.json")["hubId"]; got != "hub-Z" {
		t.Errorf("seeded hubId = %v, want hub-Z", got)
	}
}

func TestRun_EmptyEnvelopeHubIDQuarantines(t *testing.T) {
	dir := t.TempDir()
	// Valid JSON, but no hubId — same quarantine path as unreadable.
	write(t, dir, "tasks/urn_project_nohub/tasks.json", `{"version":2,"projectId":"urn:project:nohub","hubId":"","tasks":[]}`)

	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mustExist(t, dir, "hubs/_unassigned/tasks/urn_project_nohub/tasks.json")
}

func TestRun_FreshDirJustWritesMarker(t *testing.T) {
	dir := t.TempDir()
	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mustExist(t, dir, "hubs/"+MarkerName)
}

func TestRun_MarkerFastExit(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "hubs/"+MarkerName, "2026-01-01T00:00:00Z\n")
	// Old-layout data present but the marker says done: nothing may move.
	write(t, dir, "tasks/urn_p/tasks.json", envelope(t, "urn:p", "hub-A", ""))

	if err := Run(dir, quiet()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mustExist(t, dir, "tasks/urn_p/tasks.json")
	mustNotExist(t, dir, "hubs/hub-A")
}
