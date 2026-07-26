package production

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const prodMigProj = "urn:proj:prodmig"

// Golden v1 fixture with a zeroed child counter to prove the load-time
// repair still runs AFTER migration (repairs fix value drift; migrations
// fix shape).
const prodGoldenV1 = `{
  "version": 1,
  "projectId": "urn:proj:prodmig",
  "hubId": "hub-1",
  "projectName": "P",
  "nextJobNum": 2,
  "jobs": [
    {"id": "j1", "num": 1, "name": "Job One", "nextStepNum": 0, "nextBatchNum": 0,
     "nextChildNum": 0, "steps": [], "edges": [], "batches": [],
     "createdBy": {"id": "u1"}, "createdAt": "2025-02-01T00:00:00Z",
     "updatedAt": "2025-02-01T00:00:00Z"}
  ]
}`

func TestProductionMigrationV1ToV2(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, sanitizeID(prodMigProj))
	if err := os.MkdirAll(pdir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pdir, "production.json")
	if err := os.WriteFile(path, []byte(prodGoldenV1), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListJobs(prodMigProj)
	if err != nil {
		t.Fatalf("ListJobs after migration: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Name != "Job One" {
		t.Fatalf("data lost: %+v", jobs)
	}
	if _, err := os.Stat(path + ".v1.bak"); err != nil {
		t.Errorf("no pre-migration snapshot: %v", err)
	}
	// Counter repair ran post-migration: adding a step works (would panic /
	// duplicate ids if NextStepNum stayed 0).
	if _, err := s.CreateStep(prodMigProj, "j1", StepDraft{Title: "step"}); err != nil {
		t.Fatalf("CreateStep after repair: %v", err)
	}
	data, _ := os.ReadFile(path)
	var pf struct {
		Version int `json:"version"`
		Schema  struct {
			CreatedByVersion string `json:"createdByVersion"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatal(err)
	}
	if pf.Version != 2 || pf.Schema.CreatedByVersion != "pre-schema" {
		t.Errorf("persisted envelope = %+v", pf)
	}
}

func TestProductionMigrationFutureRefused(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, sanitizeID(prodMigProj))
	_ = os.MkdirAll(pdir, 0700)
	_ = os.WriteFile(filepath.Join(pdir, "production.json"),
		[]byte(`{"version": 99, "projectId": "x", "nextJobNum": 1, "jobs": []}`), 0600)
	s, _ := NewStore(dir)
	if _, err := s.ListJobs(prodMigProj); !errors.Is(err, ErrFutureVersion) {
		t.Errorf("err = %v, want ErrFutureVersion", err)
	}
}
