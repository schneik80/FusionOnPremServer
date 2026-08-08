package production

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const (
	testProject = "urn:adsk.wipprod:dm.folder:proj/abc"
	testHub     = "urn:adsk.wipprod:dm.folder:hub/xyz"
	testName    = "Demo Project"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func user() UserRef { return UserRef{ID: "sub-1", Name: "Alice", Email: "alice@example.com"} }

func mustJob(t *testing.T, s *Store, name string) Job {
	t.Helper()
	j, err := s.CreateJob(testProject, testHub, testName, JobDraft{Name: name}, user())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	return j
}

func mustStep(t *testing.T, s *Store, jobID, title string) Step {
	t.Helper()
	j, err := s.CreateStep(testProject, jobID, StepDraft{Title: title})
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}
	return *j.Steps[len(j.Steps)-1]
}

func snapshot(item string) DocSnapshot {
	return DocSnapshot{
		HubID:                  testHub,
		ItemID:                 item,
		Name:                   "part.f3d",
		Kind:                   "design",
		VersionID:              item + "?version=3",
		VersionNumber:          3,
		RootComponentVersionID: "cv-" + item,
		DMProjectID:            "b.altid",
	}
}

func TestJobCRUD(t *testing.T) {
	s := newStore(t)

	j := mustJob(t, s, "  First Job  ")
	if j.ID != "j1" || j.Num != 1 {
		t.Fatalf("unexpected job id/num: %+v", j)
	}
	if j.Name != "First Job" {
		t.Fatalf("name not trimmed: %q", j.Name)
	}
	if j.Steps == nil || j.Edges == nil || j.Batches == nil {
		t.Fatalf("slices must be non-nil: %+v", j)
	}

	j2 := mustJob(t, s, "Second")
	if j2.ID != "j2" {
		t.Fatalf("expected j2, got %s", j2.ID)
	}

	list, err := s.ListJobs(testProject)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(list) != 2 || list[0].ID != "j2" { // newest first
		t.Fatalf("unexpected list order: %+v", list)
	}

	name := "Renamed"
	upd, err := s.UpdateJob(testProject, "j1", JobPatch{Name: &name})
	if err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	if upd.Name != "Renamed" {
		t.Fatalf("rename failed: %q", upd.Name)
	}

	if err := s.DeleteJob(testProject, "j1"); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	if _, err := s.GetJob(testProject, "j1"); err == nil {
		t.Fatalf("expected not-found after delete")
	}
}

func TestJobValidation(t *testing.T) {
	s := newStore(t)
	if _, err := s.CreateJob(testProject, testHub, testName, JobDraft{Name: "   "}, user()); err == nil {
		t.Fatalf("expected empty-name rejection")
	}
	if _, err := s.UpdateJob(testProject, "nope", JobPatch{}); err == nil {
		t.Fatalf("expected not-found for unknown job")
	}
}

func TestDuplicateJob(t *testing.T) {
	s := newStore(t)
	src := mustJob(t, s, "Original")
	a := mustStep(t, s, src.ID, "A")
	b := mustStep(t, s, src.ID, "B")
	if _, err := s.AddEdge(testProject, src.ID, a.ID, b.ID); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if _, err := s.AttachPlanDoc(testProject, src.ID, a.ID, snapshot("item-1"), user()); err != nil {
		t.Fatalf("AttachPlanDoc: %v", err)
	}
	if _, err := s.AddPlaceholder(testProject, src.ID, b.ID, PlaceholderDraft{Label: "NC program", Required: true}); err != nil {
		t.Fatalf("AddPlaceholder: %v", err)
	}
	if _, err := s.CreateBatch(testProject, src.ID, BatchDraft{Name: "Run 1"}, user()); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	before, err := s.GetJob(testProject, src.ID)
	if err != nil {
		t.Fatalf("GetJob source: %v", err)
	}

	other := UserRef{ID: "sub-2", Name: "Bob"}
	dup, err := s.DuplicateJob(testProject, testHub, testName, src.ID, other)
	if err != nil {
		t.Fatalf("DuplicateJob: %v", err)
	}

	if dup.ID != "j2" || dup.Num != 2 {
		t.Fatalf("copy did not get a fresh job id: %+v", dup)
	}
	if dup.Name != "Original (copy)" {
		t.Fatalf("copy name = %q", dup.Name)
	}
	if dup.CreatedBy.ID != "sub-2" {
		t.Fatalf("copy should be authored by the duplicator: %+v", dup.CreatedBy)
	}

	// Runs belong to the job they ran under.
	if len(dup.Batches) != 0 || dup.NextBatchNum != 1 {
		t.Fatalf("copy carried batches: %d batches, nextBatchNum %d", len(dup.Batches), dup.NextBatchNum)
	}

	// Child ids and counters ride along verbatim — renumbering would mean
	// remapping Edge.From/To, and a reset NextChildNum would mint a second e1.
	if len(dup.Steps) != 2 || dup.Steps[0].ID != a.ID || dup.Steps[1].ID != b.ID {
		t.Fatalf("step ids not preserved: %+v", dup.Steps)
	}
	if len(dup.Edges) != 1 || dup.Edges[0].From != a.ID || dup.Edges[0].To != b.ID {
		t.Fatalf("edges not preserved: %+v", dup.Edges)
	}
	if dup.NextStepNum != before.NextStepNum || dup.NextChildNum != before.NextChildNum {
		t.Fatalf("counters not preserved: steps %d/%d children %d/%d",
			dup.NextStepNum, before.NextStepNum, dup.NextChildNum, before.NextChildNum)
	}

	// A pin is exact — a copy must not silently follow the tip.
	if len(dup.Steps[0].PlanDocs) != 1 || dup.Steps[0].PlanDocs[0].Doc.VersionNumber != 3 {
		t.Fatalf("plan doc pin not copied verbatim: %+v", dup.Steps[0].PlanDocs)
	}
	if dup.Steps[0].PlanDocs[0].AddedBy.ID != user().ID {
		t.Fatalf("pin attribution rewritten: %+v", dup.Steps[0].PlanDocs[0].AddedBy)
	}
	if len(dup.Steps[1].Placeholders) != 1 || !dup.Steps[1].Placeholders[0].Required {
		t.Fatalf("placeholders not copied: %+v", dup.Steps[1].Placeholders)
	}

	// The copy is independent: adding a step to it must not touch the source.
	if _, err := s.CreateStep(testProject, dup.ID, StepDraft{Title: "C"}); err != nil {
		t.Fatalf("CreateStep on copy: %v", err)
	}
	after, err := s.GetJob(testProject, src.ID)
	if err != nil {
		t.Fatalf("GetJob source after: %v", err)
	}
	if len(after.Steps) != 2 {
		t.Fatalf("editing the copy changed the source: %+v", after.Steps)
	}

	if _, err := s.DuplicateJob(testProject, testHub, testName, "nope", other); err == nil {
		t.Fatalf("expected not-found duplicating an unknown job")
	}
}

func TestDuplicateJobLongName(t *testing.T) {
	s := newStore(t)
	// A name already at the cap must still be duplicable: the base is trimmed
	// by runes to make room rather than failing validation. Non-ASCII so a
	// byte-wise trim would split a character.
	long := ""
	for utf8.RuneCountInString(long) < MaxNameRunes {
		long += "é"
	}
	src := mustJob(t, s, long)
	dup, err := s.DuplicateJob(testProject, testHub, testName, src.ID, user())
	if err != nil {
		t.Fatalf("DuplicateJob with a max-length name: %v", err)
	}
	if n := utf8.RuneCountInString(dup.Name); n != MaxNameRunes {
		t.Fatalf("copy name is %d runes, want %d", n, MaxNameRunes)
	}
	if !strings.HasSuffix(dup.Name, copySuffix) {
		t.Fatalf("copy name lost its suffix: %q", dup.Name)
	}
	if !utf8.ValidString(dup.Name) {
		t.Fatalf("copy name is not valid UTF-8: %q", dup.Name)
	}
}

func TestStepsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	j := mustJob(t, s, "Job")
	st := mustStep(t, s, j.ID, "Setup 1")
	if st.ID != "s1" {
		t.Fatalf("expected s1, got %s", st.ID)
	}

	// position patch
	pos := Position{X: 120, Y: -40}
	upd, err := s.UpdateStep(testProject, j.ID, st.ID, StepPatch{Position: &pos})
	if err != nil {
		t.Fatalf("UpdateStep: %v", err)
	}
	if upd.Steps[0].X != 120 || upd.Steps[0].Y != -40 {
		t.Fatalf("position not saved: %+v", upd.Steps[0])
	}

	// reload from disk in a fresh store — positions must survive
	s2, _ := NewStore(dir)
	got, err := s2.GetJob(testProject, j.ID)
	if err != nil {
		t.Fatalf("GetJob after reload: %v", err)
	}
	if len(got.Steps) != 1 || got.Steps[0].X != 120 {
		t.Fatalf("step position did not persist: %+v", got.Steps)
	}

	if _, err := s.DeleteStep(testProject, j.ID, st.ID); err != nil {
		t.Fatalf("DeleteStep: %v", err)
	}
}

func TestEdgesDAG(t *testing.T) {
	s := newStore(t)
	j := mustJob(t, s, "Job")
	a := mustStep(t, s, j.ID, "A")
	b := mustStep(t, s, j.ID, "B")
	c := mustStep(t, s, j.ID, "C")

	if _, err := s.AddEdge(testProject, j.ID, a.ID, b.ID); err != nil {
		t.Fatalf("AddEdge a->b: %v", err)
	}
	if _, err := s.AddEdge(testProject, j.ID, b.ID, c.ID); err != nil {
		t.Fatalf("AddEdge b->c: %v", err)
	}

	// self-loop rejected
	if _, err := s.AddEdge(testProject, j.ID, a.ID, a.ID); err == nil {
		t.Fatalf("expected self-loop rejection")
	}
	// duplicate rejected
	if _, err := s.AddEdge(testProject, j.ID, a.ID, b.ID); err == nil {
		t.Fatalf("expected duplicate rejection")
	}
	// unknown endpoint rejected
	if _, err := s.AddEdge(testProject, j.ID, a.ID, "s99"); err == nil {
		t.Fatalf("expected unknown-endpoint rejection")
	}
	// cycle rejected: c->a would close a->b->c->a
	if _, err := s.AddEdge(testProject, j.ID, c.ID, a.ID); err == nil {
		t.Fatalf("expected cycle rejection")
	}

	job, _ := s.GetJob(testProject, j.ID)
	if len(job.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(job.Edges))
	}

	// delete an edge, then the previously-cyclic edge becomes legal
	if _, err := s.DeleteEdge(testProject, j.ID, job.Edges[0].ID); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}

	// deleting a step drops its incident edges
	if _, err := s.DeleteStep(testProject, j.ID, b.ID); err != nil {
		t.Fatalf("DeleteStep: %v", err)
	}
	job, _ = s.GetJob(testProject, j.ID)
	for _, e := range job.Edges {
		if e.From == b.ID || e.To == b.ID {
			t.Fatalf("edge incident to deleted step survived: %+v", e)
		}
	}
}

func TestPlanDocsAndPlaceholders(t *testing.T) {
	s := newStore(t)
	j := mustJob(t, s, "Job")
	st := mustStep(t, s, j.ID, "Setup 1")

	// invalid snapshot (no versionId) rejected
	bad := snapshot("item-1")
	bad.VersionID = ""
	if _, err := s.AttachPlanDoc(testProject, j.ID, st.ID, bad, user()); err == nil {
		t.Fatalf("expected snapshot-validation rejection")
	}

	job, err := s.AttachPlanDoc(testProject, j.ID, st.ID, snapshot("item-1"), user())
	if err != nil {
		t.Fatalf("AttachPlanDoc: %v", err)
	}
	if len(job.Steps[0].PlanDocs) != 1 || job.Steps[0].PlanDocs[0].ID != "pd1" {
		t.Fatalf("plan doc not attached: %+v", job.Steps[0].PlanDocs)
	}

	job, err = s.AddPlaceholder(testProject, j.ID, st.ID, PlaceholderDraft{Label: "Setup 1 NC", Required: true})
	if err != nil {
		t.Fatalf("AddPlaceholder: %v", err)
	}
	ph := job.Steps[0].Placeholders[0]
	if ph.ID != "ph2" || !ph.Required { // shares NextChildNum with the plan doc (pd1)
		t.Fatalf("unexpected placeholder: %+v", ph)
	}

	req := false
	job, err = s.UpdatePlaceholder(testProject, j.ID, st.ID, ph.ID, PlaceholderPatch{Required: &req})
	if err != nil {
		t.Fatalf("UpdatePlaceholder: %v", err)
	}
	if job.Steps[0].Placeholders[0].Required {
		t.Fatalf("placeholder required not cleared")
	}

	if _, err := s.RemovePlanDoc(testProject, j.ID, st.ID, "pd1"); err != nil {
		t.Fatalf("RemovePlanDoc: %v", err)
	}
	if _, err := s.RemovePlaceholder(testProject, j.ID, st.ID, ph.ID); err != nil {
		t.Fatalf("RemovePlaceholder: %v", err)
	}
}

func TestBatchFreezeImmutability(t *testing.T) {
	s := newStore(t)
	j := mustJob(t, s, "Job")
	st := mustStep(t, s, j.ID, "Setup 1")
	if _, err := s.AttachPlanDoc(testProject, j.ID, st.ID, snapshot("item-1"), user()); err != nil {
		t.Fatalf("AttachPlanDoc: %v", err)
	}
	jb, err := s.AddPlaceholder(testProject, j.ID, st.ID, PlaceholderDraft{Label: "Setup 1 NC", Required: true})
	if err != nil {
		t.Fatalf("AddPlaceholder: %v", err)
	}
	phID := jb.Steps[0].Placeholders[0].ID

	runAt := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	b, err := s.CreateBatch(testProject, j.ID, BatchDraft{Name: "Batch 1", Kind: "prove", RunAt: runAt}, user())
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if len(b.Steps) != 1 || b.Steps[0].Title != "Setup 1" {
		t.Fatalf("batch did not freeze step identity: %+v", b.Steps)
	}
	if len(b.Steps[0].PlanDocs) != 1 || b.Steps[0].PlanDocs[0].Doc.VersionNumber != 3 {
		t.Fatalf("batch did not freeze plan doc: %+v", b.Steps[0].PlanDocs)
	}
	if len(b.Steps[0].Placeholders) != 1 || b.Steps[0].Placeholders[0].ID != phID {
		t.Fatalf("batch did not freeze placeholders: %+v", b.Steps[0].Placeholders)
	}
	if b.Status != "planned" || !b.RunAt.Equal(runAt) {
		t.Fatalf("unexpected batch defaults: %+v", b)
	}

	// Mutate the plan AFTER the batch: attach a newer version, remove the old
	// doc, add a placeholder for the NEXT run, then delete the step entirely.
	newer := snapshot("item-1")
	newer.VersionNumber = 9
	newer.VersionID = "item-1?version=9"
	if _, err := s.AttachPlanDoc(testProject, j.ID, st.ID, newer, user()); err != nil {
		t.Fatalf("AttachPlanDoc newer: %v", err)
	}
	if _, err := s.RemovePlanDoc(testProject, j.ID, st.ID, "pd1"); err != nil {
		t.Fatalf("RemovePlanDoc: %v", err)
	}
	jb2, err := s.AddPlaceholder(testProject, j.ID, st.ID, PlaceholderDraft{Label: "Added later"})
	if err != nil {
		t.Fatalf("AddPlaceholder later: %v", err)
	}
	newPhID := jb2.Steps[0].Placeholders[len(jb2.Steps[0].Placeholders)-1].ID

	// Edit the frozen placeholder IN PLACE on the live plan. This is the case
	// the append/reslice mutations above cannot catch: UpdatePlaceholder writes
	// through a *Placeholder into the live slice, so if copyStep ever stopped
	// copying Placeholders the frozen record would change under it. Same shape
	// as the rollback snapshot in mutateJob, which is why this matters beyond
	// the freeze.
	relabel := "Renamed on the live plan"
	if _, err := s.UpdatePlaceholder(testProject, j.ID, st.ID, phID, PlaceholderPatch{Label: &relabel}); err != nil {
		t.Fatalf("UpdatePlaceholder: %v", err)
	}
	inPlace, err := s.GetBatch(testProject, j.ID, b.ID)
	if err != nil {
		t.Fatalf("GetBatch after in-place edit: %v", err)
	}
	if inPlace.Steps[0].Placeholders[0].Label != "Setup 1 NC" {
		t.Fatalf("in-place plan edit leaked into the frozen batch: %q", inPlace.Steps[0].Placeholders[0].Label)
	}

	if _, err := s.DeleteStep(testProject, j.ID, st.ID); err != nil {
		t.Fatalf("DeleteStep: %v", err)
	}

	// The frozen record is untouched: still v3, still one placeholder.
	got, err := s.GetBatch(testProject, j.ID, b.ID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if got.Steps[0].PlanDocs[0].Doc.VersionNumber != 3 {
		t.Fatalf("batch snapshot changed under plan edit: %+v", got.Steps)
	}
	if len(got.Steps[0].Placeholders) != 1 {
		t.Fatalf("later placeholder leaked into frozen batch: %+v", got.Steps[0].Placeholders)
	}

	// Fulfillments validate against the FROZEN plan: the deleted step is
	// still recordable, the frozen placeholder still fillable, and the
	// later-added placeholder is not part of this run.
	fb, err := s.AddFulfillment(testProject, j.ID, b.ID, FulfillmentDraft{
		StepID:        st.ID,
		PlaceholderID: phID,
		Doc:           snapshot("nc-supplied"),
		Source:        "hub",
	}, user())
	if err != nil {
		t.Fatalf("AddFulfillment after step delete: %v", err)
	}
	if len(fb.Fulfillments) != 1 {
		t.Fatalf("fulfillment not recorded: %+v", fb.Fulfillments)
	}
	// Duplicate fulfillment for the same slot is rejected.
	if _, err := s.AddFulfillment(testProject, j.ID, b.ID, FulfillmentDraft{
		StepID:        st.ID,
		PlaceholderID: phID,
		Doc:           snapshot("nc-dup"),
	}, user()); err == nil {
		t.Fatalf("expected duplicate-fulfillment rejection")
	}
	// A placeholder added after the freeze does not exist on this batch.
	if _, err := s.AddFulfillment(testProject, j.ID, b.ID, FulfillmentDraft{
		StepID:        st.ID,
		PlaceholderID: newPhID,
		Doc:           snapshot("nc-late"),
	}, user()); err == nil {
		t.Fatalf("expected unknown-placeholder rejection for post-freeze slot")
	}
	// As-run artifacts (no placeholder) still attach to the frozen step.
	fb, err = s.AddFulfillment(testProject, j.ID, b.ID, FulfillmentDraft{
		StepID:  st.ID,
		Doc:     snapshot("nc-out"),
		Source:  "upload",
		IsAsRun: true,
	}, user())
	if err != nil {
		t.Fatalf("AddFulfillment as-run: %v", err)
	}
	if len(fb.Fulfillments) != 2 || !fb.Fulfillments[1].IsAsRun {
		t.Fatalf("as-run not recorded: %+v", fb.Fulfillments)
	}

	// bad kind / status rejected
	if _, err := s.UpdateBatch(testProject, j.ID, b.ID, BatchPatch{Status: strptr("bogus")}); err == nil {
		t.Fatalf("expected bad-status rejection")
	}
	done := "complete"
	if _, err := s.UpdateBatch(testProject, j.ID, b.ID, BatchPatch{Status: &done}); err != nil {
		t.Fatalf("UpdateBatch: %v", err)
	}

	if _, err := s.RemoveFulfillment(testProject, j.ID, b.ID, fb.Fulfillments[0].ID); err != nil {
		t.Fatalf("RemoveFulfillment: %v", err)
	}
	if err := s.DeleteBatch(testProject, j.ID, b.ID); err != nil {
		t.Fatalf("DeleteBatch: %v", err)
	}
}

func strptr(s string) *string { return &s }

func TestBatchRefs(t *testing.T) {
	s := newStore(t)
	j := mustJob(t, s, "Job")
	b, err := s.CreateBatch(testProject, j.ID, BatchDraft{Name: "Batch 1"}, user())
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if b.Refs == nil {
		t.Fatalf("Refs must be non-nil")
	}

	taskTok := "fls:task?projectId=p&taskId=t3&title=Do+it"
	docTok := "fls:doc?hubId=h&itemId=urn:x&name=spec.pdf"

	if _, err := s.AddBatchRef(testProject, j.ID, b.ID, taskTok); err != nil {
		t.Fatalf("AddBatchRef task: %v", err)
	}
	got, err := s.AddBatchRef(testProject, j.ID, b.ID, docTok)
	if err != nil {
		t.Fatalf("AddBatchRef doc: %v", err)
	}
	if len(got.Refs) != 2 {
		t.Fatalf("expected 2 refs, got %+v", got.Refs)
	}
	// idempotent add
	got, _ = s.AddBatchRef(testProject, j.ID, b.ID, taskTok)
	if len(got.Refs) != 2 {
		t.Fatalf("duplicate add should be a no-op, got %+v", got.Refs)
	}
	// bad token rejected
	if _, err := s.AddBatchRef(testProject, j.ID, b.ID, "fls:job?jobId=j1"); err == nil {
		t.Fatalf("expected rejection of non task/doc token")
	}
	if _, err := s.RemoveBatchRef(testProject, j.ID, b.ID, taskTok); err != nil {
		t.Fatalf("RemoveBatchRef: %v", err)
	}
	got, _ = s.GetBatch(testProject, j.ID, b.ID)
	if len(got.Refs) != 1 || got.Refs[0] != docTok {
		t.Fatalf("unexpected refs after remove: %+v", got.Refs)
	}
}

// TestConcurrentMutations drives job renames against step edits on the same
// job from two goroutines. Run under -race it guards the copy-under-lock
// discipline: every mutation must deep-copy its return value INSIDE the
// project mutex (a post-unlock copy races the other goroutine's writes).
func TestConcurrentMutations(t *testing.T) {
	s := newStore(t)
	j := mustJob(t, s, "Job")
	st := mustStep(t, s, j.ID, "Step")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			name := fmt.Sprintf("Job %d", i)
			if _, err := s.UpdateJob(testProject, j.ID, JobPatch{Name: &name}); err != nil {
				t.Errorf("UpdateJob: %v", err)
				return
			}
		}
	}()
	for i := 0; i < 50; i++ {
		pos := Position{X: float64(i), Y: float64(-i)}
		if _, err := s.UpdateStep(testProject, j.ID, st.ID, StepPatch{Position: &pos}); err != nil {
			t.Fatalf("UpdateStep: %v", err)
		}
	}
	<-done
}

// A rejected mutation must leave the touched job exactly as it was — and must
// not disturb its neighbours. Guards the job-scoped rollback in mutateJob.
func TestRejectedMutationRollsBackOnlyTouchedJob(t *testing.T) {
	s := newStore(t)
	a := mustJob(t, s, "A")
	b := mustJob(t, s, "B")
	s1 := mustStep(t, s, a.ID, "one")
	s2 := mustStep(t, s, a.ID, "two")
	if _, err := s.AddEdge(testProject, a.ID, s1.ID, s2.ID); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if _, err := s.CreateStep(testProject, b.ID, StepDraft{Title: "b-step"}); err != nil {
		t.Fatalf("CreateStep b: %v", err)
	}

	before, _ := s.GetJob(testProject, a.ID)
	otherBefore, _ := s.GetJob(testProject, b.ID)

	// Rejected: would close a cycle.
	if _, err := s.AddEdge(testProject, a.ID, s2.ID, s1.ID); err == nil {
		t.Fatalf("expected cycle rejection")
	}
	// Rejected: unknown step.
	if _, err := s.AddPlaceholder(testProject, a.ID, "s999", PlaceholderDraft{Label: "x"}); err == nil {
		t.Fatalf("expected unknown-step rejection")
	}

	after, _ := s.GetJob(testProject, a.ID)
	if len(after.Edges) != len(before.Edges) || len(after.Steps) != len(before.Steps) {
		t.Fatalf("touched job mutated by a rejected write: before %d edges/%d steps, after %d/%d",
			len(before.Edges), len(before.Steps), len(after.Edges), len(after.Steps))
	}
	otherAfter, _ := s.GetJob(testProject, b.ID)
	if len(otherAfter.Steps) != len(otherBefore.Steps) || otherAfter.Name != otherBefore.Name {
		t.Fatalf("neighbouring job disturbed: %+v vs %+v", otherBefore, otherAfter)
	}

	// And the file on disk still agrees after a reload.
	if _, err := s.AddEdge(testProject, a.ID, s2.ID, s1.ID); err == nil {
		t.Fatalf("expected cycle rejection on retry")
	}
	reloaded, _ := s.GetJob(testProject, a.ID)
	if len(reloaded.Edges) != len(before.Edges) {
		t.Fatalf("edge count drifted across retries: %d vs %d", len(reloaded.Edges), len(before.Edges))
	}
}

func TestMine(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	other := UserRef{ID: "sub-2", Name: "Bob", Email: "bob@example.com"}

	// Project A: one job by me, one by someone else.
	mineJob, err := s.CreateJob(testProject, testHub, "Alpha", JobDraft{Name: "Mine"}, user())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	theirs, err := s.CreateJob(testProject, testHub, "Alpha", JobDraft{Name: "Theirs"}, other)
	if err != nil {
		t.Fatalf("CreateJob other: %v", err)
	}
	// Project B: a job by someone else, but with a run I created — still mine.
	const projB = "urn:adsk.wipprod:dm.folder:proj/bbb"
	viaBatch, err := s.CreateJob(projB, testHub, "Beta", JobDraft{Name: "ViaBatch"}, other)
	if err != nil {
		t.Fatalf("CreateJob projB: %v", err)
	}
	if _, err := s.CreateBatch(projB, viaBatch.ID, BatchDraft{Name: "Run 1"}, user()); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	got, err := s.Mine(user().ID, user().Email)
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	ids := map[string]string{} // jobID -> projectName
	for _, pj := range got {
		ids[pj.ID] = pj.ProjectName
	}
	if _, ok := ids[mineJob.ID]; !ok {
		t.Fatalf("job I created missing from Mine: %+v", got)
	}
	if _, ok := ids[viaBatch.ID]; !ok {
		t.Fatalf("job with my batch missing from Mine: %+v", got)
	}
	if _, ok := ids[theirs.ID]; ok {
		t.Fatalf("someone else's job leaked into Mine: %+v", got)
	}
	// Self-describing project annotation must survive the directory-slug scan.
	if ids[viaBatch.ID] != "Beta" {
		t.Fatalf("expected projectName Beta, got %q", ids[viaBatch.ID])
	}

	// Email fallback matches when the OIDC sub is absent.
	byEmail, err := s.Mine("", user().Email)
	if err != nil || len(byEmail) != len(got) {
		t.Fatalf("email fallback mismatch: %d vs %d (err %v)", len(byEmail), len(got), err)
	}

	// A stranger sees nothing of mine.
	none, err := s.Mine("sub-9", "nobody@example.com")
	if err != nil {
		t.Fatalf("Mine stranger: %v", err)
	}
	for _, pj := range none {
		if pj.ID == mineJob.ID {
			t.Fatalf("stranger saw my job")
		}
	}
}

func TestCorruptRecovery(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	mustJob(t, s, "Job") // creates the project dir + file

	path := filepath.Join(dir, sanitizeID(testProject), "production.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0600); err != nil {
		t.Fatalf("corrupt write: %v", err)
	}

	s2, _ := NewStore(dir)
	list, err := s2.ListJobs(testProject)
	if err != nil {
		t.Fatalf("ListJobs after corrupt: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list after corrupt recovery, got %d", len(list))
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("expected .bak of corrupt file: %v", err)
	}
}

func TestFutureVersion(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, sanitizeID(testProject))
	if err := os.MkdirAll(pdir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	future := projectFile{Version: fileVersion + 1, ProjectID: testProject, NextJobNum: 1, Jobs: []*Job{}}
	data, _ := json.Marshal(future)
	if err := os.WriteFile(filepath.Join(pdir, "production.json"), data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, _ := NewStore(dir)
	if _, err := s.ListJobs(testProject); err == nil {
		t.Fatalf("expected ErrFutureVersion")
	}
}

func TestDeleteProject(t *testing.T) {
	s := newStore(t)
	mustJob(t, s, "Doomed job")
	if _, err := s.CreateJob("urn:other:project", testHub, "Other Project",
		JobDraft{Name: "Survivor"}, user()); err != nil {
		t.Fatalf("CreateJob other: %v", err)
	}
	dirA := filepath.Join(s.dir, sanitizeID(testProject))
	if _, err := os.Stat(dirA); err != nil {
		t.Fatalf("project dir missing before delete: %v", err)
	}

	if err := s.DeleteProject(testProject); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := os.Stat(dirA); !os.IsNotExist(err) {
		t.Errorf("project dir still present after delete: %v", err)
	}
	s.mu.Lock()
	_, cached := s.projects[testProject]
	s.mu.Unlock()
	if cached {
		t.Error("project still in the in-memory map after delete")
	}
	if _, err := os.Stat(filepath.Join(s.dir, sanitizeID("urn:other:project"))); err != nil {
		t.Errorf("other project dir was collateral damage: %v", err)
	}

	// Next access recreates fresh state lazily.
	jobs, err := s.ListJobs(testProject)
	if err != nil {
		t.Fatalf("ListJobs after delete: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("ListJobs after delete = %d jobs, want 0", len(jobs))
	}
	reborn := mustJob(t, s, "Reborn")
	if reborn.Num != 1 {
		t.Errorf("recreated job num = %d, want fresh 1", reborn.Num)
	}

	// Idempotent: delete again, then with the dir already gone.
	if err := s.DeleteProject(testProject); err != nil {
		t.Fatalf("second DeleteProject: %v", err)
	}
	if err := s.DeleteProject(testProject); err != nil {
		t.Errorf("DeleteProject on missing dir = %v, want nil", err)
	}
}
