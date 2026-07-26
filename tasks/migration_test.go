package tasks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenV1 is a real pre-stamp tasks.json as fileVersion 1 wrote it.
const goldenV1 = `{
  "version": 1,
  "projectId": "urn:proj:mig",
  "hubId": "hub-1",
  "projectName": "Migration Project",
  "nextTaskId": 3,
  "tasks": [
    {"id": "t1", "num": 1, "title": "Old task", "status": "todo", "priority": "medium",
     "createdBy": {"id": "u1"}, "createdAt": "2025-01-02T03:04:05Z",
     "updatedAt": "2025-01-02T03:04:05Z", "docRefs": [], "rank": 1024},
    {"id": "t2", "num": 2, "title": "Scheduled", "status": "inprogress", "priority": "high",
     "startDate": "2026-07-01", "endDate": "2026-07-05", "progress": 30,
     "createdBy": {"id": "u1"}, "createdAt": "2025-01-03T00:00:00Z",
     "updatedAt": "2025-01-03T00:00:00Z", "docRefs": [], "rank": 1024}
  ]
}`

const migProj = "urn:proj:mig"

func writeGolden(t *testing.T, dir, content string) string {
	t.Helper()
	pdir := filepath.Join(dir, sanitizeID(migProj))
	if err := os.MkdirAll(pdir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pdir, "tasks.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigrationV1ToV2(t *testing.T) {
	dir := t.TempDir()
	path := writeGolden(t, dir, goldenV1)
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Load migrates in memory; all data intact.
	list, err := s.List(migProj)
	if err != nil {
		t.Fatalf("List after migration: %v", err)
	}
	if len(list) != 2 || list[1].StartDate != "2026-07-01" {
		t.Fatalf("data lost in migration: %+v", list)
	}
	// Pre-migration snapshot preserved the v1 bytes.
	snap, err := os.ReadFile(path + ".v1.bak")
	if err != nil {
		t.Fatalf("no .v1.bak: %v", err)
	}
	if !strings.Contains(string(snap), `"version": 1`) {
		t.Errorf("snapshot is not the v1 original: %s", snap[:80])
	}

	// First write persists v2 + schema stamp; birthdate backfilled.
	if _, err := s.Create(migProj, "hub-1", "Migration Project", Draft{Title: "new"}, alice); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var pf struct {
		Version int `json:"version"`
		Schema  struct {
			CreatedByVersion string `json:"createdByVersion"`
			UpdatedByVersion string `json:"updatedByVersion"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatal(err)
	}
	if pf.Version != 2 || pf.Schema.CreatedByVersion != "pre-schema" || pf.Schema.UpdatedByVersion == "" {
		t.Errorf("persisted envelope = %+v", pf)
	}

	// Reload from disk: migrated file is now at target — no second snapshot.
	s2, _ := NewStore(dir)
	if _, err := s2.List(migProj); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := os.Stat(path + ".v2.bak"); !errors.Is(err, os.ErrNotExist) {
		t.Error("unexpected .v2.bak — migration should be idempotent")
	}
}

func TestMigrationFutureStillRefused(t *testing.T) {
	dir := t.TempDir()
	writeGolden(t, dir, `{"version": 99, "projectId": "urn:proj:mig", "nextTaskId": 1, "tasks": []}`)
	s, _ := NewStore(dir)
	if _, err := s.List(migProj); !errors.Is(err, ErrFutureVersion) {
		t.Errorf("err = %v, want ErrFutureVersion", err)
	}
}

func TestMigrationCorruptStillRecovers(t *testing.T) {
	dir := t.TempDir()
	path := writeGolden(t, dir, `{"version": 1, "tasks": [truncated`)
	s, _ := NewStore(dir)
	list, err := s.List(migProj)
	if err != nil || len(list) != 0 {
		t.Fatalf("List = %v, %v; want clean empty", list, err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Error("corrupt original not preserved")
	}
}
