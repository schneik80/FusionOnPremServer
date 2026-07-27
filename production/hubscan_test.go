package production

import "testing"

// TestListForProjects_ScopesAndEmpties mirrors the tasks-store check: an empty
// id set yields nothing, and results are restricted to the requested projects.
func TestListForProjects_ScopesAndEmpties(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	me := UserRef{ID: "u", Name: "U"}
	if _, err := st.CreateJob("p1", "h", "P1", JobDraft{Name: "j1"}, me); err != nil {
		t.Fatalf("seed p1: %v", err)
	}
	if _, err := st.CreateJob("p2", "h", "P2", JobDraft{Name: "j2"}, me); err != nil {
		t.Fatalf("seed p2: %v", err)
	}

	if got, err := st.ListForProjects(nil); err != nil || len(got) != 0 {
		t.Fatalf("ListForProjects(nil) = %v, %v; want empty, nil", got, err)
	}

	got, err := st.ListForProjects([]string{"p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ProjectID != "p1" {
		t.Fatalf("p1 jobs = %+v, want exactly one in p1", got)
	}
}
