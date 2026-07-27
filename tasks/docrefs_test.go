package tasks

import "testing"

const (
	docItem  = "urn:adsk.wipprod:dm.lineage:hC6k4hndRWaeIVhIjvHu8w"
	docToken = "fls:doc?hubId=b.1&itemId=urn%3Aadsk.wipprod%3Adm.lineage%3AhC6k4hndRWaeIVhIjvHu8w&name=bracket&kind=design"
)

// TestFindDocRefs_ScopesAndMatches covers the security-critical behavior an
// unscoped scan would get wrong (an empty id set must never mean "all
// projects", and a project outside the set must never surface) plus the two
// places a task can hold a reference: its attached doc cards and its body.
func TestFindDocRefs_ScopesAndMatches(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	me := UserRef{ID: "u", Name: "U"}
	if _, err := st.Create("p1", "h", "P1", Draft{Title: "attach", DocRefs: []string{docToken}}, me); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create("p1", "h", "P1", Draft{Title: "inline", Description: "see " + docToken + " please"}, me); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create("p1", "h", "P1", Draft{Title: "unrelated", Description: "no refs here"}, me); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create("p2", "h", "P2", Draft{Title: "other project", DocRefs: []string{docToken}}, me); err != nil {
		t.Fatal(err)
	}

	if got, err := st.FindDocRefs(nil, docItem); err != nil || len(got) != 0 {
		t.Fatalf("FindDocRefs(nil) = %v, %v; want empty, nil", got, err)
	}

	got, err := st.FindDocRefs([]string{"p1"}, docItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("p1 hits = %d, want 2 (attached + inline)", len(got))
	}
	for _, h := range got {
		if h.ProjectID != "p1" {
			t.Errorf("leaked project %q", h.ProjectID)
		}
		if h.Count != 1 {
			t.Errorf("%s: count = %d, want 1", h.Title, h.Count)
		}
	}

	if got, err := st.FindDocRefs([]string{"p1", "p2"}, docItem); err != nil || len(got) != 3 {
		t.Fatalf("p1+p2 hits = %d (err %v), want 3", len(got), err)
	}
}

// TestFindDocRefs_IgnoresNearMisses guards the prefilter: a task that merely
// mentions the lineage urn as text, or references a different document, is not
// a hit.
func TestFindDocRefs_IgnoresNearMisses(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	me := UserRef{ID: "u"}
	if _, err := st.Create("p1", "h", "P1", Draft{Title: "plain urn", Description: "the id is " + docItem}, me); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create("p1", "h", "P1", Draft{
		Title:   "other doc",
		DocRefs: []string{"fls:doc?hubId=b.1&itemId=urn%3Aadsk%3Adm.lineage%3Aelse&name=x&kind=design"},
	}, me); err != nil {
		t.Fatal(err)
	}
	got, err := st.FindDocRefs([]string{"p1"}, docItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("hits = %v, want none", got)
	}
}
