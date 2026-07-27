package tasks

import "testing"

// TestListForProjects_ScopesAndEmpties covers the security-critical behavior:
// an empty id set yields nothing (never "all projects"), and results are
// restricted to exactly the requested projects.
func TestListForProjects_ScopesAndEmpties(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	me := UserRef{ID: "u", Name: "U"}
	for _, p := range []string{"p1", "p1", "p2"} {
		if _, err := st.Create(p, "h", p, Draft{Title: "task"}, me); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	if got, err := st.ListForProjects(nil); err != nil || len(got) != 0 {
		t.Fatalf("ListForProjects(nil) = %v, %v; want empty, nil", got, err)
	}

	got, err := st.ListForProjects([]string{"p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("p1 tasks = %d, want 2", len(got))
	}
	for _, pt := range got {
		if pt.ProjectID != "p1" {
			t.Errorf("leaked project %q", pt.ProjectID)
		}
	}

	if got, err := st.ListForProjects([]string{"p1", "p2"}); err != nil || len(got) != 3 {
		t.Fatalf("p1+p2 tasks = %d (err %v), want 3", len(got), err)
	}
}
