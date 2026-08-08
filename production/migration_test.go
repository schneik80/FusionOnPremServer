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
	// A v1 file migrates all the way to the current version in one load — the
	// registry chains every step, it does not stop at v2.
	if pf.Version != fileVersion || pf.Schema.CreatedByVersion != "pre-schema" {
		t.Errorf("persisted envelope = %+v, want version %d", pf, fileVersion)
	}
}

// Golden v2 fixture: a job with a plain step, an edge and a batch, none of
// which carry the v3 fields (kind / results / fromResultId / hiddenSteps).
const prodGoldenV2 = `{
  "version": 2,
  "schema": {"createdAt": "2026-01-02T00:00:00Z", "createdByVersion": "test",
             "updatedAt": "2026-01-02T00:00:00Z", "updatedByVersion": "test"},
  "projectId": "urn:proj:prodmig",
  "hubId": "hub-1",
  "projectName": "P",
  "nextJobNum": 2,
  "jobs": [
    {"id": "j1", "num": 1, "name": "Job One", "nextStepNum": 3, "nextBatchNum": 2,
     "nextChildNum": 5,
     "steps": [
       {"id": "s1", "num": 1, "title": "Setup", "x": 0, "y": 0,
        "planDocs": [], "placeholders": [],
        "createdAt": "2026-01-02T00:00:00Z", "updatedAt": "2026-01-02T00:00:00Z"},
       {"id": "s2", "num": 2, "title": "Mill", "x": 240, "y": 0,
        "planDocs": [], "placeholders": [],
        "createdAt": "2026-01-02T00:00:00Z", "updatedAt": "2026-01-02T00:00:00Z"}
     ],
     "edges": [{"id": "e3", "from": "s1", "to": "s2"}],
     "batches": [
       {"id": "b1", "num": 1, "name": "Run 1", "kind": "prove",
        "runAt": "2026-01-03T00:00:00Z", "status": "planned",
        "steps": [{"stepId": "s1", "num": 1, "title": "Setup",
                   "planDocs": [], "placeholders": []}],
        "fulfillments": [], "refs": [],
        "createdBy": {"id": "u1"}, "createdAt": "2026-01-03T00:00:00Z",
        "updatedAt": "2026-01-03T00:00:00Z"}
     ],
     "createdBy": {"id": "u1"}, "createdAt": "2026-01-02T00:00:00Z",
     "updatedAt": "2026-01-02T00:00:00Z"}
  ]
}`

// The v2→v3 step is a no-op by design: every field v3 adds has a zero value
// that already means what the v2 file meant. This asserts that reading is
// lossless and that the absent kind reads as "step" everywhere it matters —
// including for AddEdge, which rejects a result binding on a plain step.
func TestProductionMigrationV2ToV3(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, sanitizeID(prodMigProj))
	if err := os.MkdirAll(pdir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pdir, "production.json")
	if err := os.WriteFile(path, []byte(prodGoldenV2), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	j, err := s.GetJob(prodMigProj, "j1")
	if err != nil {
		t.Fatalf("GetJob after migration: %v", err)
	}
	if len(j.Steps) != 2 || len(j.Edges) != 1 || len(j.Batches) != 1 {
		t.Fatalf("data lost: %d steps, %d edges, %d batches", len(j.Steps), len(j.Edges), len(j.Batches))
	}
	// Absent fields keep their legacy meaning rather than being rewritten.
	for _, st := range j.Steps {
		if IsDecision(st.Kind) {
			t.Errorf("step %s read as a decision", st.ID)
		}
		if len(st.Results) != 0 {
			t.Errorf("step %s invented results: %+v", st.ID, st.Results)
		}
	}
	if j.Edges[0].FromResultID != "" {
		t.Errorf("edge gained a result binding: %+v", j.Edges[0])
	}
	if len(j.Batches[0].HiddenSteps) != 0 {
		t.Errorf("batch invented hidden steps: %+v", j.Batches[0].HiddenSteps)
	}

	// A pre-v3 step must behave as a plain step, not as an unknown kind.
	if _, err := s.AddResult(prodMigProj, "j1", "s1", ResultDraft{Label: "Pass"}); err == nil {
		t.Errorf("expected a pre-v3 step to reject results")
	}
	if _, err := s.AddEdge(prodMigProj, "j1", "s2", "", "s1"); err == nil {
		t.Errorf("expected the cycle check to still hold after migration")
	}

	if _, err := os.Stat(path + ".v2.bak"); err != nil {
		t.Errorf("no pre-migration snapshot: %v", err)
	}

	// The counters survive, so new children don't collide with existing ids.
	if _, err := s.CreateStep(prodMigProj, "j1", StepDraft{Kind: "decision", Title: "QC"}); err != nil {
		t.Fatalf("CreateStep after migration: %v", err)
	}
	after, _ := s.GetJob(prodMigProj, "j1")
	if got := after.Steps[len(after.Steps)-1]; got.ID != "s3" || !IsDecision(got.Kind) {
		t.Errorf("new decision step = %+v", got)
	}

	data, _ := os.ReadFile(path)
	var pf struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatal(err)
	}
	if pf.Version != fileVersion {
		t.Errorf("persisted version = %d, want %d", pf.Version, fileVersion)
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
