package whiteboards

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const wbMigProj = "urn:proj:wbmig"

const wbGoldenV1 = `{
  "version": 1,
  "projectId": "urn:proj:wbmig",
  "hubId": "hub-1",
  "projectName": "P",
  "nextBoardId": 2,
  "boards": [
    {"id": "w1", "num": 1, "name": "Sketches",
     "createdBy": {"id": "u1"}, "createdAt": "2025-03-01T00:00:00Z",
     "updatedAt": "2025-03-01T00:00:00Z", "updatedBy": {"id": "u1"}}
  ]
}`

func TestWhiteboardsMigrationV1ToV2(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, sanitizeID(wbMigProj))
	if err := os.MkdirAll(pdir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pdir, "whiteboards.json")
	if err := os.WriteFile(path, []byte(wbGoldenV1), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	boards, err := s.List(wbMigProj)
	if err != nil || len(boards) != 1 || boards[0].Name != "Sketches" {
		t.Fatalf("List after migration = %v, %v", boards, err)
	}
	if _, err := os.Stat(path + ".v1.bak"); err != nil {
		t.Errorf("no pre-migration snapshot: %v", err)
	}
	// First write persists v2 + stamp.
	if _, err := s.Update(wbMigProj, "w1", Patch{Name: strptr("Renamed")}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var pf struct {
		Version int `json:"version"`
		Schema  struct {
			CreatedByVersion string `json:"createdByVersion"`
		} `json:"schema"`
	}
	_ = json.Unmarshal(data, &pf)
	if pf.Version != 2 || pf.Schema.CreatedByVersion != "pre-schema" {
		t.Errorf("persisted envelope = %+v", pf)
	}
}

func strptr(s string) *string { return &s }

func TestWhiteboardsMigrationFutureRefused(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, sanitizeID(wbMigProj))
	_ = os.MkdirAll(pdir, 0700)
	_ = os.WriteFile(filepath.Join(pdir, "whiteboards.json"),
		[]byte(`{"version": 99, "nextBoardId": 1, "boards": []}`), 0600)
	s, _ := NewStore(dir)
	if _, err := s.List(wbMigProj); !errors.Is(err, ErrFutureVersion) {
		t.Errorf("err = %v, want ErrFutureVersion", err)
	}
}
