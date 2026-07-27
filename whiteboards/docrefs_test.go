package whiteboards

import (
	"strings"
	"testing"
)

const (
	docItem  = "urn:adsk.wipprod:dm.lineage:hC6k4hndRWaeIVhIjvHu8w"
	docToken = "fls:doc?hubId=b.1&itemId=urn%3Aadsk.wipprod%3Adm.lineage%3AhC6k4hndRWaeIVhIjvHu8w&name=bracket&kind=design"
)

// board writes a board whose document holds n fls:doc cards for the item, in
// the shape tldraw stores them (the token is a shape prop, a plain JSON
// string).
func board(t *testing.T, st *Store, projectID, name string, n int) Board {
	t.Helper()
	b, err := st.Create(projectID, "h", projectID, Draft{Name: name}, UserRef{ID: "u", Name: "U"})
	if err != nil {
		t.Fatal(err)
	}
	shapes := make([]string, n)
	for i := range shapes {
		shapes[i] = `{"type":"fls-card","props":{"w":320,"h":96,"token":"` + docToken + `"}}`
	}
	doc := `{"store":{"shapes":[` + strings.Join(shapes, ",") + `]}}`
	if _, err := st.SaveSnapshot(projectID, b.ID, []byte(doc), UserRef{ID: "u", Name: "U"}, 0, true); err != nil {
		t.Fatal(err)
	}
	return b
}

// TestFindDocRefs_CountsCardsPerBoard is the whole feature for whiteboards:
// one node per board, carrying how many cards on it point at the document.
func TestFindDocRefs_CountsCardsPerBoard(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	board(t, st, "p1", "Layout", 3)
	// A board with no cards at all, and one whose card points elsewhere: both
	// must be rejected by the prefilter, not merely by the count.
	if _, err := st.Create("p1", "h", "p1", Draft{Name: "Empty"}, UserRef{ID: "u"}); err != nil {
		t.Fatal(err)
	}
	other, err := st.Create("p1", "h", "p1", Draft{Name: "Other"}, UserRef{ID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSnapshot("p1", other.ID,
		[]byte(`{"token":"fls:doc?itemId=urn%3Aadsk%3Adm.lineage%3Aelse"}`), UserRef{ID: "u"}, 0, true); err != nil {
		t.Fatal(err)
	}

	got, err := st.FindDocRefs([]string{"p1"}, docItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("hits = %+v, want just the board with cards", got)
	}
	if got[0].BoardName != "Layout" || got[0].Count != 3 {
		t.Errorf("hit = %+v, want Layout with 3 cards", got[0])
	}
	if got[0].UpdatedBy != "U" || got[0].UpdatedAt.IsZero() {
		t.Errorf("hit is missing save provenance: %+v", got[0])
	}
}

// TestFindDocRefs_Scoping holds the shared invariant: an empty id set yields
// nothing, and boards never surface from outside the set.
func TestFindDocRefs_Scoping(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	board(t, st, "p1", "One", 1)
	board(t, st, "p2", "Two", 1)

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
