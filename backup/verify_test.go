package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// verifyExpected is the schema-version authority the tests hand to Verify:
// what the "current build" writes for each fixture file.
func verifyExpected(store, rel string) (int, bool) {
	switch store {
	case "tasks":
		return 2, true
	case "chat":
		if strings.HasSuffix(rel, ".jsonl") {
			return 1, true
		}
		return 2, true
	}
	return 0, false // whiteboard docs, config: no schema authority
}

// makeVerifySnapshot runs the engine over scripted sources and returns the
// snapshot dir — a pristine fixture each caller then corrupts its own way.
func makeVerifySnapshot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	e := &Engine{
		Dir: dir,
		Sources: []Source{
			&fakeSource{name: "tasks", files: []fakeFile{
				{rel: "tasks/proj-a/tasks.json", data: []byte(`{"version":2,"tasks":[{"id":"t1"}]}`), sv: 2},
			}},
			&fakeSource{name: "chat", files: []fakeFile{
				{rel: "chat/proj-a/meta.json", data: []byte(`{"version":2,"channels":[]}`), sv: 2},
				{rel: "chat/proj-a/msg-c1.jsonl", data: []byte("{\"v\":1,\"op\":\"create\"}\n{\"v\":1,\"op\":\"edit\"}\n"), sv: 1},
			}},
			&fakeSource{name: "whiteboards", files: []fakeFile{
				{rel: "whiteboards/proj-a/doc-b1.json", data: []byte(`{"store":{}}`), sv: 1},
			}},
		},
		AppVersion: "1.0.0",
	}
	if _, err := e.Run(KindManual); err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "manual"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("manual tier = %v, %v", entries, err)
	}
	return filepath.Join(dir, "manual", entries[0].Name())
}

// fileResult finds one path's result in a report.
func fileResult(t *testing.T, rep *VerifyReport, path string) FileResult {
	t.Helper()
	for _, f := range rep.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no result for %s in %+v", path, rep.Files)
	return FileResult{}
}

func TestVerifyPristineSnapshotOK(t *testing.T) {
	snap := makeVerifySnapshot(t)
	rep, err := Verify(snap, verifyExpected)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.OK {
		t.Errorf("pristine snapshot: OK = false, files = %+v", rep.Files)
	}
	if len(rep.Files) != 4 {
		t.Errorf("files = %d, want 4", len(rep.Files))
	}
	if rep.Kind != KindManual || rep.CreatedAt.IsZero() {
		t.Errorf("header = kind %q createdAt %v", rep.Kind, rep.CreatedAt)
	}
	for _, f := range rep.Files {
		if f.Missing || !f.HashOK || !f.ParseOK || !f.VersionOK || f.Detail != "" {
			t.Errorf("file %s not fully OK: %+v", f.Path, f)
		}
	}
}

func TestVerifyFlippedByteFailsHash(t *testing.T) {
	snap := makeVerifySnapshot(t)
	p := filepath.Join(snap, "tasks", "proj-a", "tasks.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Same length, still valid JSON — only the hash can catch it.
	flipped := strings.Replace(string(data), `"t1"`, `"t9"`, 1)
	if flipped == string(data) {
		t.Fatal("fixture did not flip")
	}
	if err := os.WriteFile(p, []byte(flipped), 0600); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(snap, verifyExpected)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Error("OK = true after byte flip")
	}
	f := fileResult(t, rep, "tasks/proj-a/tasks.json")
	if f.HashOK || !f.ParseOK || !f.VersionOK || f.Missing {
		t.Errorf("flipped file = %+v, want HashOK=false only", f)
	}
	// The untouched files still pass.
	if g := fileResult(t, rep, "chat/proj-a/meta.json"); !g.HashOK {
		t.Errorf("unrelated file failed: %+v", g)
	}
}

func TestVerifyTruncatedJSONFailsParse(t *testing.T) {
	snap := makeVerifySnapshot(t)
	p := filepath.Join(snap, "chat", "proj-a", "meta.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data[:len(data)/2], 0600); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(snap, verifyExpected)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Error("OK = true after truncation")
	}
	f := fileResult(t, rep, "chat/proj-a/meta.json")
	if f.ParseOK {
		t.Errorf("truncated file = %+v, want ParseOK=false", f)
	}
}

func TestVerifyBadJSONLLineFailsParse(t *testing.T) {
	snap := makeVerifySnapshot(t)
	p := filepath.Join(snap, "chat", "proj-a", "msg-c1.jsonl")
	if err := os.WriteFile(p, []byte("{\"v\":1}\nnot json at all\n"), 0600); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(snap, verifyExpected)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	f := fileResult(t, rep, "chat/proj-a/msg-c1.jsonl")
	if f.ParseOK {
		t.Errorf("bad JSONL = %+v, want ParseOK=false", f)
	}
	if rep.OK {
		t.Error("OK = true with a bad JSONL line")
	}
}

func TestVerifyFutureSchemaVersionFails(t *testing.T) {
	snap := makeVerifySnapshot(t)
	// Bump one entry's recorded schemaVersion beyond current. The file bytes
	// are untouched, so the hash still passes — only the version check trips.
	m, err := ReadManifest(snap)
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.Files {
		if m.Files[i].Path == "tasks/proj-a/tasks.json" {
			m.Files[i].SchemaVersion = 99
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snap, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(snap, verifyExpected)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Error("OK = true with a future schemaVersion")
	}
	f := fileResult(t, rep, "tasks/proj-a/tasks.json")
	if f.VersionOK || !f.HashOK || f.Detail == "" {
		t.Errorf("future-schema file = %+v, want VersionOK=false with detail", f)
	}
	// The whiteboard doc has no schema authority → version check never trips.
	if g := fileResult(t, rep, "whiteboards/proj-a/doc-b1.json"); !g.VersionOK {
		t.Errorf("no-authority file failed version check: %+v", g)
	}
}

func TestVerifyDeletedFileIsMissing(t *testing.T) {
	snap := makeVerifySnapshot(t)
	if err := os.Remove(filepath.Join(snap, "chat", "proj-a", "msg-c1.jsonl")); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(snap, verifyExpected)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Error("OK = true with a missing file")
	}
	f := fileResult(t, rep, "chat/proj-a/msg-c1.jsonl")
	if !f.Missing || f.HashOK || f.ParseOK || f.VersionOK {
		t.Errorf("deleted file = %+v, want Missing=true", f)
	}
}

func TestVerifyStrayFileFlagged(t *testing.T) {
	snap := makeVerifySnapshot(t)
	if err := os.WriteFile(filepath.Join(snap, "tasks", "proj-a", "stray.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(snap, verifyExpected)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Error("OK = true with a stray file")
	}
	f := fileResult(t, rep, "tasks/proj-a/stray.json")
	if f.Detail != "not in manifest" {
		t.Errorf("stray file = %+v, want Detail %q", f, "not in manifest")
	}
	if len(rep.Files) != 5 {
		t.Errorf("files = %d, want 4 manifest + 1 stray", len(rep.Files))
	}
}

func TestVerifyMissingManifestErrors(t *testing.T) {
	if _, err := Verify(t.TempDir(), verifyExpected); err == nil {
		t.Error("Verify with no manifest: err = nil")
	}
}
