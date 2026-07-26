package tasks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotStreamsEveryProject(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	u := UserRef{ID: "u1", Name: "User One", Email: "u1@x.io"}
	if _, err := s.Create("urn:project:a", "hub-1", "Alpha", Draft{Title: "task in a"}, u); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("urn:project:b", "hub-1", "Beta", Draft{Title: "task in b"}, u); err != nil {
		t.Fatal(err)
	}
	// Corruption-recovery droppings must never be backed up.
	if err := os.WriteFile(filepath.Join(dir, sanitizeID("urn:project:a"), "tasks.json.v1.bak"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	type seen struct {
		data []byte
		sv   int
	}
	got := map[string]seen{}
	err = s.Snapshot(func(rel string, data []byte, schemaVersion int) error {
		got[rel] = seen{data: append([]byte(nil), data...), sv: schemaVersion}
		return nil
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	wantRels := []string{
		"tasks/" + sanitizeID("urn:project:a") + "/tasks.json",
		"tasks/" + sanitizeID("urn:project:b") + "/tasks.json",
	}
	if len(got) != len(wantRels) {
		t.Fatalf("Snapshot visited %d files (%v), want %d", len(got), keys(got), len(wantRels))
	}
	for _, rel := range wantRels {
		entry, ok := got[rel]
		if !ok {
			t.Fatalf("missing rel %q (have %v)", rel, keys(got))
		}
		var pf projectFile
		if err := json.Unmarshal(entry.data, &pf); err != nil {
			t.Errorf("%s: not parseable JSON: %v", rel, err)
		}
		if len(pf.Tasks) != 1 {
			t.Errorf("%s: %d tasks, want 1", rel, len(pf.Tasks))
		}
		if entry.sv != fileVersion {
			t.Errorf("%s: schemaVersion = %d, want %d", rel, entry.sv, fileVersion)
		}
	}
}

func TestSnapshotPropagatesVisitError(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("urn:project:a", "hub-1", "Alpha", Draft{Title: "x"}, UserRef{ID: "u1", Name: "U"}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("visit failed")
	if err := s.Snapshot(func(string, []byte, int) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("Snapshot err = %v, want %v", err, boom)
	}
}

func TestSnapshotEmptyStore(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := s.Snapshot(func(string, []byte, int) error { calls++; return nil }); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if calls != 0 {
		t.Errorf("visited %d files in an empty store", calls)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
