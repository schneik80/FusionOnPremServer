package production

import "testing"

const (
	docItem  = "urn:adsk.wipprod:dm.lineage:hC6k4hndRWaeIVhIjvHu8w"
	docToken = "fls:doc?hubId=b.1&itemId=urn%3Aadsk.wipprod%3Adm.lineage%3AhC6k4hndRWaeIVhIjvHu8w&name=bracket&kind=design"
)

func pinned(item string) DocSnapshot {
	return DocSnapshot{HubID: "h", ItemID: item, Name: "bracket", VersionID: item + "?version=3", VersionNumber: 3}
}

// TestFindDocRefs_PlanAndBatchAreSeparateHits is the behaviour the graph's
// separate Jobs / Batches checkboxes rest on: a document pinned to a job's
// plan is one fact, and each run that froze that plan is another.
func TestFindDocRefs_PlanAndBatchAreSeparateHits(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	me := UserRef{ID: "u"}
	job, err := st.CreateJob("p1", "h", "P1", JobDraft{Name: "Mill housing"}, me)
	if err != nil {
		t.Fatal(err)
	}
	j, err := st.CreateStep("p1", job.ID, StepDraft{Title: "Setup 1"})
	if err != nil {
		t.Fatal(err)
	}
	stepID := j.Steps[0].ID
	if _, err := st.AttachPlanDoc("p1", job.ID, stepID, pinned(docItem), me); err != nil {
		t.Fatal(err)
	}
	// Creating the batch freezes the plan, so the run references it too.
	if _, err := st.CreateBatch("p1", job.ID, BatchDraft{Name: "Run 1", Kind: "prove"}, me); err != nil {
		t.Fatal(err)
	}

	got, err := st.FindDocRefs([]string{"p1"}, docItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("hits = %d, want 2 (plan + batch): %+v", len(got), got)
	}
	plan, batch := got[0], got[1]
	if plan.BatchID != "" {
		t.Fatalf("first hit should be the plan, got batch %q", plan.BatchID)
	}
	if plan.Via != ViaStep || plan.Where != "Setup 1" {
		t.Errorf("plan hit via/where = %q/%q, want step/Setup 1", plan.Via, plan.Where)
	}
	if batch.BatchID == "" || batch.BatchName != "Run 1" || batch.Via != ViaStep {
		t.Errorf("batch hit = %+v", batch)
	}
}

// TestFindDocRefs_FulfillmentsAndRefs covers the run-only paths: a document
// supplied into a batch, and one attached as a plain fls:doc token.
func TestFindDocRefs_FulfillmentsAndRefs(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	me := UserRef{ID: "u"}
	job, err := st.CreateJob("p1", "h", "P1", JobDraft{Name: "J"}, me)
	if err != nil {
		t.Fatal(err)
	}
	j, err := st.CreateStep("p1", job.ID, StepDraft{Title: "Setup 1"})
	if err != nil {
		t.Fatal(err)
	}
	stepID := j.Steps[0].ID
	batch, err := st.CreateBatch("p1", job.ID, BatchDraft{Name: "Run 1", Kind: "production"}, me)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFulfillment("p1", job.ID, batch.ID,
		FulfillmentDraft{StepID: stepID, Doc: pinned(docItem), Source: "hub", IsAsRun: true}, me); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddBatchRef("p1", job.ID, batch.ID, docToken); err != nil {
		t.Fatal(err)
	}

	got, err := st.FindDocRefs([]string{"p1"}, docItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("hits = %d, want 1 (the batch): %+v", len(got), got)
	}
	if got[0].Count != 2 {
		t.Errorf("count = %d, want 2 (fulfilment + ref)", got[0].Count)
	}
	if got[0].Via != ViaFulfillment {
		t.Errorf("via = %q, want %q", got[0].Via, ViaFulfillment)
	}
	if got[0].BatchKind != "production" {
		t.Errorf("batch kind = %q", got[0].BatchKind)
	}
}

// TestFindDocRefs_Scoping is the same security invariant the sibling stores
// hold: no id set means no results, and results never leave the set.
func TestFindDocRefs_Scoping(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	me := UserRef{ID: "u"}
	for _, p := range []string{"p1", "p2"} {
		job, err := st.CreateJob(p, "h", p, JobDraft{Name: "J"}, me)
		if err != nil {
			t.Fatal(err)
		}
		j, err := st.CreateStep(p, job.ID, StepDraft{Title: "S"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AttachPlanDoc(p, job.ID, j.Steps[0].ID, pinned(docItem), me); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := st.FindDocRefs(nil, docItem); err != nil || len(got) != 0 {
		t.Fatalf("FindDocRefs(nil) = %v, %v; want empty, nil", got, err)
	}
	got, err := st.FindDocRefs([]string{"p2"}, docItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ProjectID != "p2" {
		t.Fatalf("scoped hits = %+v, want exactly p2", got)
	}
}
