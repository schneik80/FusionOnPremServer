package tasks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	projA = "urn:adsk.wipprod:fs.folder:co.projA"
	projB = "urn:adsk.wipprod:fs.folder:co.projB"
)

var alice = UserRef{ID: "sub-alice", Name: "Alice", Email: "alice@example.com"}
var bob = UserRef{ID: "sub-bob", Name: "Bob", Email: "bob@example.com"}

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, dir
}

func TestCreateGetList(t *testing.T) {
	s, _ := newTestStore(t)
	created, err := s.Create(projA, "hub1", "Project A", Draft{Title: "  First task  "}, alice)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != "t1" || created.Num != 1 {
		t.Errorf("id/num = %q/%d, want t1/1", created.ID, created.Num)
	}
	if created.Title != "First task" {
		t.Errorf("title not trimmed: %q", created.Title)
	}
	if created.Status != "todo" || created.Priority != "medium" {
		t.Errorf("defaults = %q/%q, want todo/medium", created.Status, created.Priority)
	}
	if created.Rank != 1024 {
		t.Errorf("rank = %v, want 1024", created.Rank)
	}
	if created.DocRefs == nil {
		t.Error("DocRefs is nil, want empty slice")
	}
	got, err := s.Get(projA, "t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "First task" || got.CreatedBy.ID != alice.ID {
		t.Errorf("Get mismatch: %+v", got)
	}
	second, err := s.Create(projA, "hub1", "Project A", Draft{Title: "Second"}, alice)
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if second.ID != "t2" || second.Rank != 2048 {
		t.Errorf("second id/rank = %q/%v, want t2/2048", second.ID, second.Rank)
	}
	list, err := s.List(projA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
}

func TestCreateValidation(t *testing.T) {
	s, _ := newTestStore(t)
	cases := []Draft{
		{Title: ""},
		{Title: "   "},
		{Title: strings.Repeat("x", MaxTitleRunes+1)},
		{Title: "ok", Description: strings.Repeat("d", MaxDescRunes+1)},
		{Title: "ok", Status: "bogus"},
		{Title: "ok", Priority: "bogus"},
		{Title: "ok", DueDate: "tomorrow"},
		{Title: "ok", DueDate: "2026-13-40"},
		{Title: "ok", DocRefs: []string{"https://example.com"}},
		{Title: "ok", DocRefs: make([]string, MaxDocRefs+1)},
	}
	for i, d := range cases {
		if len(d.DocRefs) > MaxDocRefs {
			for j := range d.DocRefs {
				d.DocRefs[j] = "fls:doc?hubId=h&itemId=i"
			}
		}
		if _, err := s.Create(projA, "hub1", "A", d, alice); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
		}
	}
}

func TestUpdatePatchAndClear(t *testing.T) {
	s, _ := newTestStore(t)
	created, _ := s.Create(projA, "hub1", "A", Draft{
		Title: "Task", DueDate: "2026-07-10", Assignee: &bob,
		DocRefs: []string{"fls:doc?hubId=h&itemId=i1"},
	}, alice)

	newTitle := "Renamed"
	newPrio := "high"
	got, err := s.Update(projA, created.ID, Patch{Title: &newTitle, Priority: &newPrio})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Title != "Renamed" || got.Priority != "high" {
		t.Errorf("patch not applied: %+v", got)
	}
	if got.DueDate != "2026-07-10" || got.Assignee == nil || got.Assignee.ID != bob.ID {
		t.Errorf("untouched fields changed: %+v", got)
	}
	if !got.UpdatedAt.After(created.UpdatedAt) && got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Log("updatedAt unchanged (fast clock) — tolerated")
	}

	got, err = s.Update(projA, created.ID, Patch{ClearAssignee: true, ClearDueDate: true})
	if err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	if got.Assignee != nil || got.DueDate != "" {
		t.Errorf("clear failed: %+v", got)
	}

	// Every cross-link scheme a task may carry: documents plus our own apps'
	// cards. A scheme missing from the whitelist is rejected as ErrInvalid, so
	// this list is the contract the composers encode against.
	refs := []string{
		"fls:doc?hubId=h&itemId=i2",
		"fls:doc?hubId=h&itemId=i3",
		"fls:job?hubId=h&projectId=p&jobId=j1",
		"fls:batch?hubId=h&projectId=p&jobId=j1&batchId=b1",
		"fls:whiteboard?hubId=h&projectId=p&boardId=w1&name=Sketch",
	}
	got, err = s.Update(projA, created.ID, Patch{DocRefs: &refs})
	if err != nil {
		t.Fatalf("Update docRefs: %v", err)
	}
	if len(got.DocRefs) != len(refs) {
		t.Errorf("docRefs = %v", got.DocRefs)
	}

	bad := "bogus"
	if _, err := s.Update(projA, created.ID, Patch{Status: &bad}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad status err = %v, want ErrInvalid", err)
	}
	if _, err := s.Update(projA, "t999", Patch{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing task err = %v, want ErrNotFound", err)
	}
}

func TestStatusChangeAppendsToColumn(t *testing.T) {
	s, _ := newTestStore(t)
	t1, _ := s.Create(projA, "h", "A", Draft{Title: "one", Status: "done"}, alice) // done rank 1024
	t2, _ := s.Create(projA, "h", "A", Draft{Title: "two"}, alice)                 // todo rank 1024
	_ = t1
	done := "done"
	got, err := s.Update(projA, t2.ID, Patch{Status: &done})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Rank <= 1024 {
		t.Errorf("rank = %v, want > 1024 (appended after existing done task)", got.Rank)
	}
	// Explicit rank wins.
	rank := 512.0
	todo := "todo"
	got, err = s.Update(projA, t2.ID, Patch{Status: &todo, Rank: &rank})
	if err != nil {
		t.Fatalf("Update explicit rank: %v", err)
	}
	if got.Rank != 512 {
		t.Errorf("rank = %v, want 512", got.Rank)
	}
}

func TestDelete(t *testing.T) {
	s, _ := newTestStore(t)
	created, _ := s.Create(projA, "h", "A", Draft{Title: "doomed"}, alice)
	if err := s.Delete(projA, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(projA, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := s.Delete(projA, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete = %v, want ErrNotFound", err)
	}
}

func TestMine(t *testing.T) {
	s, _ := newTestStore(t)
	_, _ = s.Create(projA, "hub1", "Project A", Draft{Title: "mine by creation"}, alice)
	_, _ = s.Create(projA, "hub1", "Project A", Draft{Title: "assigned to alice", Assignee: &alice}, bob)
	_, _ = s.Create(projB, "hub1", "Project B", Draft{Title: "unrelated"}, bob)
	// Email-only match (session predating the sub claim).
	_, _ = s.Create(projB, "hub1", "Project B", Draft{
		Title:    "assigned by email",
		Assignee: &UserRef{ID: "sub-other", Email: "ALICE@example.com"},
	}, bob)

	mine, err := s.Mine(alice.ID, alice.Email)
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	if len(mine) != 3 {
		t.Fatalf("Mine len = %d, want 3: %+v", len(mine), mine)
	}
	for _, pt := range mine {
		if pt.ProjectID == "" || pt.HubID == "" || pt.ProjectName == "" {
			t.Errorf("missing project annotation: %+v", pt)
		}
	}
	bobs, err := s.Mine(bob.ID, bob.Email)
	if err != nil {
		t.Fatalf("Mine bob: %v", err)
	}
	if len(bobs) != 3 {
		t.Errorf("Mine bob len = %d, want 3 (creator of 3)", len(bobs))
	}
}

func TestPersistenceAcrossReload(t *testing.T) {
	s, dir := newTestStore(t)
	created, _ := s.Create(projA, "hub1", "Project A", Draft{Title: "persisted", Assignee: &bob}, alice)

	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	got, err := s2.Get(projA, created.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.Title != "persisted" || got.Assignee == nil || got.Assignee.ID != bob.ID {
		t.Errorf("reload mismatch: %+v", got)
	}
	// Counter survives: next create continues the sequence.
	next, err := s2.Create(projA, "hub1", "Project A", Draft{Title: "next"}, alice)
	if err != nil {
		t.Fatalf("Create after reload: %v", err)
	}
	if next.ID != "t2" {
		t.Errorf("next id = %q, want t2", next.ID)
	}
}

func TestCorruptFileRecovers(t *testing.T) {
	s, dir := newTestStore(t)
	_, _ = s.Create(projA, "h", "A", Draft{Title: "x"}, alice)
	path := filepath.Join(dir, sanitizeID(projA), "tasks.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	s2, _ := NewStore(dir)
	list, err := s2.List(projA)
	if err != nil {
		t.Fatalf("List after corruption: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List = %v, want empty fresh state", list)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("no .bak preserved: %v", err)
	}
}

func TestFutureVersionRefused(t *testing.T) {
	s, dir := newTestStore(t)
	_, _ = s.Create(projA, "h", "A", Draft{Title: "x"}, alice)
	path := filepath.Join(dir, sanitizeID(projA), "tasks.json")
	if err := os.WriteFile(path, []byte(`{"version": 99, "projectId": "p", "nextTaskId": 1, "tasks": []}`), 0600); err != nil {
		t.Fatal(err)
	}
	s2, _ := NewStore(dir)
	if _, err := s2.List(projA); !errors.Is(err, ErrFutureVersion) {
		t.Errorf("List = %v, want ErrFutureVersion", err)
	}
	// Mine skips the bad project instead of failing.
	mine, err := s2.Mine(alice.ID, alice.Email)
	if err != nil {
		t.Fatalf("Mine: %v", err)
	}
	if len(mine) != 0 {
		t.Errorf("Mine = %v, want empty", mine)
	}
}

// ---- schedule (Gantt) ----

func TestScheduleCreateRoundTrip(t *testing.T) {
	s, dir := newTestStore(t)
	created, err := s.Create(projA, "h", "A", Draft{
		Title: "planned", StartDate: "2026-08-03", EndDate: "2026-08-07",
		Progress: 40, Stage: "  Design  ",
	}, alice)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.StartDate != "2026-08-03" || created.EndDate != "2026-08-07" ||
		created.Progress != 40 || created.Stage != "Design" {
		t.Errorf("schedule fields: %+v", created)
	}
	// Milestone with only a start date: end mirrors start.
	ms, err := s.Create(projA, "h", "A", Draft{Title: "ship", StartDate: "2026-09-01", Milestone: true}, alice)
	if err != nil {
		t.Fatalf("Create milestone: %v", err)
	}
	if !ms.Milestone || ms.EndDate != "2026-09-01" {
		t.Errorf("milestone end not mirrored: %+v", ms)
	}
	dep, err := s.Create(projA, "h", "A", Draft{
		Title: "follow-up", StartDate: "2026-08-10", EndDate: "2026-08-12",
		DependsOn: []string{created.ID, created.ID, " "},
	}, alice)
	if err != nil {
		t.Fatalf("Create with deps: %v", err)
	}
	if len(dep.DependsOn) != 1 || dep.DependsOn[0] != created.ID {
		t.Errorf("deps not deduped/trimmed: %v", dep.DependsOn)
	}
	// Everything survives a reload from disk.
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get(projA, dep.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.StartDate != "2026-08-10" || len(got.DependsOn) != 1 {
		t.Errorf("schedule lost across reload: %+v", got)
	}
}

func TestScheduleValidation(t *testing.T) {
	s, _ := newTestStore(t)
	cases := []Draft{
		{Title: "ok", StartDate: "2026-08-03"},                                       // one-sided
		{Title: "ok", EndDate: "2026-08-03"},                                         // one-sided
		{Title: "ok", StartDate: "aug 3", EndDate: "2026-08-07"},                     // bad format
		{Title: "ok", StartDate: "2026-08-03", EndDate: "2026-13-40"},                // bad format
		{Title: "ok", StartDate: "2026-08-07", EndDate: "2026-08-03"},                // end < start
		{Title: "ok", StartDate: "2026-08-03", EndDate: "2026-08-07", Progress: -1},  // progress
		{Title: "ok", StartDate: "2026-08-03", EndDate: "2026-08-07", Progress: 101}, // progress
		{Title: "ok", Milestone: true},                                               // milestone w/o dates
		{Title: "ok", StartDate: "2026-08-03", EndDate: "2026-08-07", Stage: strings.Repeat("s", MaxStageRunes+1)},
	}
	for i, d := range cases {
		if _, err := s.Create(projA, "h", "A", d, alice); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
		}
	}
}

func TestDependsOnValidation(t *testing.T) {
	s, _ := newTestStore(t)
	t1, _ := s.Create(projA, "h", "A", Draft{Title: "one"}, alice)
	if _, err := s.Create(projA, "h", "A", Draft{Title: "x", DependsOn: []string{"t999"}}, alice); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown dep err = %v, want ErrInvalid", err)
	}
	tooMany := make([]string, MaxDependsOn+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("t%d", i+1000) // distinct so dedupe can't save it
	}
	if _, err := s.Create(projA, "h", "A", Draft{Title: "x", DependsOn: tooMany}, alice); !errors.Is(err, ErrInvalid) {
		t.Errorf("cap err = %v, want ErrInvalid", err)
	}
	self := []string{t1.ID}
	if _, err := s.Update(projA, t1.ID, Patch{DependsOn: &self}); !errors.Is(err, ErrInvalid) {
		t.Errorf("self-dep err = %v, want ErrInvalid", err)
	}
}

func TestDependsOnCycleRejected(t *testing.T) {
	s, _ := newTestStore(t)
	t1, _ := s.Create(projA, "h", "A", Draft{Title: "one"}, alice)
	t2, _ := s.Create(projA, "h", "A", Draft{Title: "two", DependsOn: []string{t1.ID}}, alice)
	t3, _ := s.Create(projA, "h", "A", Draft{Title: "three", DependsOn: []string{t2.ID}}, alice)

	// Direct 2-node cycle: t1 -> t2 while t2 -> t1.
	back := []string{t2.ID}
	if _, err := s.Update(projA, t1.ID, Patch{DependsOn: &back}); !errors.Is(err, ErrInvalid) {
		t.Errorf("2-cycle err = %v, want ErrInvalid", err)
	}
	// 3-node cycle: t1 -> t3 -> t2 -> t1.
	far := []string{t3.ID}
	if _, err := s.Update(projA, t1.ID, Patch{DependsOn: &far}); !errors.Is(err, ErrInvalid) {
		t.Errorf("3-cycle err = %v, want ErrInvalid", err)
	}
	// A legitimate re-wire still works: t3 -> t1 (diamond, no cycle).
	ok := []string{t2.ID, t1.ID}
	got, err := s.Update(projA, t3.ID, Patch{DependsOn: &ok})
	if err != nil {
		t.Fatalf("valid deps rejected: %v", err)
	}
	if len(got.DependsOn) != 2 {
		t.Errorf("deps = %v", got.DependsOn)
	}
}

func TestUpdateScheduleAndClear(t *testing.T) {
	s, _ := newTestStore(t)
	created, _ := s.Create(projA, "h", "A", Draft{Title: "plan me"}, alice)

	start, end, prog := "2026-08-03", "2026-08-07", 60
	got, err := s.Update(projA, created.ID, Patch{StartDate: &start, EndDate: &end, Progress: &prog})
	if err != nil {
		t.Fatalf("Update schedule: %v", err)
	}
	if got.StartDate != start || got.EndDate != end || got.Progress != 60 {
		t.Errorf("schedule not applied: %+v", got)
	}
	// Moving only one edge keeps the pair valid.
	newEnd := "2026-08-10"
	if _, err := s.Update(projA, created.ID, Patch{EndDate: &newEnd}); err != nil {
		t.Fatalf("resize end: %v", err)
	}
	// One-sided invalid patch is rejected.
	bad := "2026-08-20"
	if _, err := s.Update(projA, created.ID, Patch{StartDate: &bad}); !errors.Is(err, ErrInvalid) {
		t.Errorf("start past end err = %v, want ErrInvalid", err)
	}
	// ClearSchedule drops dates+milestone but leaves progress.
	got, err = s.Update(projA, created.ID, Patch{ClearSchedule: true})
	if err != nil {
		t.Fatalf("ClearSchedule: %v", err)
	}
	if got.StartDate != "" || got.EndDate != "" || got.Milestone {
		t.Errorf("schedule not cleared: %+v", got)
	}
	if got.Progress != 60 {
		t.Errorf("progress should survive clear: %+v", got)
	}
	// Empty DependsOn clears.
	t2, _ := s.Create(projA, "h", "A", Draft{Title: "dep"}, alice)
	deps := []string{t2.ID}
	if _, err := s.Update(projA, created.ID, Patch{DependsOn: &deps}); err != nil {
		t.Fatal(err)
	}
	none := []string{}
	got, err = s.Update(projA, created.ID, Patch{DependsOn: &none})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DependsOn) != 0 {
		t.Errorf("deps not cleared: %v", got.DependsOn)
	}
	// Stage clears via empty string.
	stage := "Build"
	got, _ = s.Update(projA, created.ID, Patch{Stage: &stage})
	if got.Stage != "Build" {
		t.Errorf("stage = %q", got.Stage)
	}
	empty := ""
	got, _ = s.Update(projA, created.ID, Patch{Stage: &empty})
	if got.Stage != "" {
		t.Errorf("stage not cleared: %q", got.Stage)
	}
}

func TestDeletePrunesDependsOn(t *testing.T) {
	s, dir := newTestStore(t)
	t1, _ := s.Create(projA, "h", "A", Draft{Title: "one"}, alice)
	t2, _ := s.Create(projA, "h", "A", Draft{Title: "two"}, alice)
	t3, _ := s.Create(projA, "h", "A", Draft{Title: "three", DependsOn: []string{t1.ID, t2.ID}}, alice)

	if err := s.Delete(projA, t2.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := s.Get(projA, t3.ID)
	if len(got.DependsOn) != 1 || got.DependsOn[0] != t1.ID {
		t.Errorf("deps after prune = %v, want [%s]", got.DependsOn, t1.ID)
	}
	// Prune persisted, not just in memory.
	s2, _ := NewStore(dir)
	got, _ = s2.Get(projA, t3.ID)
	if len(got.DependsOn) != 1 || got.DependsOn[0] != t1.ID {
		t.Errorf("prune not persisted: %v", got.DependsOn)
	}
}

func TestShiftTasks(t *testing.T) {
	s, _ := newTestStore(t)
	t1, _ := s.Create(projA, "h", "A", Draft{Title: "one", StartDate: "2026-08-03", EndDate: "2026-08-07"}, alice)
	t2, _ := s.Create(projA, "h", "A", Draft{Title: "two", StartDate: "2026-08-28", EndDate: "2026-09-02"}, alice)
	t3, _ := s.Create(projA, "h", "A", Draft{Title: "backlog"}, alice)

	out, err := s.ShiftTasks(projA, []string{t1.ID, t2.ID}, 5)
	if err != nil {
		t.Fatalf("ShiftTasks: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("shifted %d, want 2", len(out))
	}
	got1, _ := s.Get(projA, t1.ID)
	if got1.StartDate != "2026-08-08" || got1.EndDate != "2026-08-12" {
		t.Errorf("t1 = %s..%s", got1.StartDate, got1.EndDate)
	}
	got2, _ := s.Get(projA, t2.ID)
	if got2.StartDate != "2026-09-02" || got2.EndDate != "2026-09-07" {
		t.Errorf("t2 month-boundary = %s..%s", got2.StartDate, got2.EndDate)
	}
	// Negative shift moves earlier.
	if _, err := s.ShiftTasks(projA, []string{t1.ID}, -5); err != nil {
		t.Fatalf("negative shift: %v", err)
	}
	got1, _ = s.Get(projA, t1.ID)
	if got1.StartDate != "2026-08-03" {
		t.Errorf("t1 after -5 = %s", got1.StartDate)
	}
	// All-or-nothing: unscheduled member rejects the whole shift.
	before, _ := s.Get(projA, t1.ID)
	if _, err := s.ShiftTasks(projA, []string{t1.ID, t3.ID}, 1); !errors.Is(err, ErrInvalid) {
		t.Errorf("unscheduled member err = %v, want ErrInvalid", err)
	}
	after, _ := s.Get(projA, t1.ID)
	if after.StartDate != before.StartDate {
		t.Error("partial shift applied despite rejection")
	}
	// Unknown ID, zero days, out-of-range days.
	if _, err := s.ShiftTasks(projA, []string{"t999"}, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
	if _, err := s.ShiftTasks(projA, []string{t1.ID}, 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("zero days err = %v, want ErrInvalid", err)
	}
	if _, err := s.ShiftTasks(projA, []string{t1.ID}, MaxShiftDays+1); !errors.Is(err, ErrInvalid) {
		t.Errorf("range err = %v, want ErrInvalid", err)
	}
}

func TestDeleteProject(t *testing.T) {
	s, dir := newTestStore(t)
	if _, err := s.Create(projA, "hub1", "Project A", Draft{Title: "Keep me not"}, alice); err != nil {
		t.Fatalf("Create A: %v", err)
	}
	if _, err := s.Create(projB, "hub1", "Project B", Draft{Title: "Survivor"}, bob); err != nil {
		t.Fatalf("Create B: %v", err)
	}
	dirA := filepath.Join(dir, sanitizeID(projA))
	if _, err := os.Stat(dirA); err != nil {
		t.Fatalf("project A dir missing before delete: %v", err)
	}

	if err := s.DeleteProject(projA); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := os.Stat(dirA); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("project A dir still present after delete: %v", err)
	}
	s.mu.Lock()
	_, cached := s.projects[projA]
	s.mu.Unlock()
	if cached {
		t.Error("project A still in the in-memory map after delete")
	}
	if _, err := os.Stat(filepath.Join(dir, sanitizeID(projB))); err != nil {
		t.Errorf("project B dir was collateral damage: %v", err)
	}

	// Next access recreates fresh state lazily: empty list, counters reset.
	list, err := s.List(projA)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List after delete = %d tasks, want 0", len(list))
	}
	created, err := s.Create(projA, "hub1", "Project A", Draft{Title: "Reborn"}, alice)
	if err != nil {
		t.Fatalf("Create after delete: %v", err)
	}
	if created.ID != "t1" || created.Num != 1 {
		t.Errorf("recreated task = %s/%d, want fresh t1/1", created.ID, created.Num)
	}

	// Deleting again (dir present) and then once more (dir gone) both succeed.
	if err := s.DeleteProject(projA); err != nil {
		t.Fatalf("second DeleteProject: %v", err)
	}
	if err := s.DeleteProject(projA); err != nil {
		t.Errorf("DeleteProject on missing dir = %v, want nil", err)
	}
	if err := s.DeleteProject("urn:project:never-existed"); err != nil {
		t.Errorf("DeleteProject on unknown project = %v, want nil", err)
	}
}

// TestDeleteProjectUncached deletes a project whose state is on disk but not
// in the map (post-Reset) — the files must still go.
func TestDeleteProjectUncached(t *testing.T) {
	s, dir := newTestStore(t)
	if _, err := s.Create(projA, "hub1", "Project A", Draft{Title: "On disk"}, alice); err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.Reset()
	if err := s.DeleteProject(projA); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sanitizeID(projA))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("project dir still present after uncached delete: %v", err)
	}
}
