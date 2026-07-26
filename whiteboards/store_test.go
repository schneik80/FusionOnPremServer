package whiteboards

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func mustBoard(t *testing.T, s *Store, name string) Board {
	t.Helper()
	b, err := s.Create(testProject, testHub, testName, Draft{Name: name}, user())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return b
}

func TestBoardCRUD(t *testing.T) {
	s := newStore(t)
	b := mustBoard(t, s, "  Layout ideas  ")
	if b.ID != "w1" || b.Num != 1 {
		t.Fatalf("unexpected id/num: %+v", b)
	}
	if b.Name != "Layout ideas" {
		t.Fatalf("name not trimmed: %q", b.Name)
	}

	mustBoard(t, s, "Second")
	list, err := s.List(testProject)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].Num != 2 { // newest first
		t.Fatalf("unexpected list: %+v", list)
	}

	name := "Renamed"
	if _, err := s.Update(testProject, b.ID, Patch{Name: &name}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get(testProject, b.ID)
	if got.Name != "Renamed" {
		t.Fatalf("rename failed: %q", got.Name)
	}

	if _, err := s.Create(testProject, testHub, testName, Draft{Name: "  "}, user()); err == nil {
		t.Fatalf("expected empty-name rejection")
	}
	if _, err := s.Update(testProject, "nope", Patch{}); err == nil {
		t.Fatalf("expected not-found on unknown board")
	}
}

func TestSnapshotRoundTripAndDeleteRemovesDocument(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	b := mustBoard(t, s, "Board")

	// A never-saved board reads back as nil, not an error: that's an empty
	// canvas, not a failure.
	doc, err := s.Document(testProject, b.ID)
	if err != nil || doc != nil {
		t.Fatalf("expected (nil, nil) for unsaved board, got (%v, %v)", doc, err)
	}

	saved := []byte(`{"document":{"shapes":[{"id":"s1"}]}}`)
	updated, err := s.SaveSnapshot(testProject, b.ID, saved, user())
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if updated.SnapshotBytes != int64(len(saved)) || updated.UpdatedBy.ID != user().ID {
		t.Fatalf("metadata not stamped: %+v", updated)
	}

	back, err := s.Document(testProject, b.ID)
	if err != nil || string(back) != string(saved) {
		t.Fatalf("round trip mismatch: %q vs %q (err %v)", back, saved, err)
	}

	// Documents live in their own files, so the metadata file stays small.
	metaPath := filepath.Join(dir, sanitizeID(testProject), "whiteboards.json")
	meta, _ := os.ReadFile(metaPath)
	if strings.Contains(string(meta), "shapes") {
		t.Fatalf("document leaked into the metadata file")
	}

	// A fresh store reads the same document from disk.
	s2, _ := NewStore(dir)
	back2, err := s2.Document(testProject, b.ID)
	if err != nil || string(back2) != string(saved) {
		t.Fatalf("document did not persist across reload: %q (err %v)", back2, err)
	}

	docPath := filepath.Join(dir, sanitizeID(testProject), "doc-"+sanitizeID(b.ID)+".json")
	if _, err := os.Stat(docPath); err != nil {
		t.Fatalf("expected document file: %v", err)
	}
	if err := s.Delete(testProject, b.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(docPath); !os.IsNotExist(err) {
		t.Fatalf("document file outlived its board")
	}
	if _, err := s.Document(testProject, b.ID); err == nil {
		t.Fatalf("expected not-found after delete")
	}
}

func TestSnapshotValidation(t *testing.T) {
	s := newStore(t)
	b := mustBoard(t, s, "Board")

	if _, err := s.SaveSnapshot(testProject, b.ID, nil, user()); err == nil {
		t.Fatalf("expected empty-document rejection")
	}
	if _, err := s.SaveSnapshot(testProject, b.ID, []byte("not json"), user()); err == nil {
		t.Fatalf("expected invalid-JSON rejection")
	}
	big := make([]byte, MaxSnapshotBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := s.SaveSnapshot(testProject, b.ID, big, user()); err == nil {
		t.Fatalf("expected oversize rejection")
	}
	if _, err := s.SaveSnapshot(testProject, "w999", []byte(`{}`), user()); err == nil {
		t.Fatalf("expected not-found for unknown board")
	}
}

func TestCorruptAndFutureVersion(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	mustBoard(t, s, "Board")

	path := filepath.Join(dir, sanitizeID(testProject), "whiteboards.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s2, _ := NewStore(dir)
	list, err := s2.List(testProject)
	if err != nil || len(list) != 0 {
		t.Fatalf("expected clean recovery, got %d boards (err %v)", len(list), err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("expected .bak of the corrupt file: %v", err)
	}

	future, _ := json.Marshal(projectFile{Version: fileVersion + 1, ProjectID: testProject, NextBoardID: 1, Boards: []*Board{}})
	if err := os.WriteFile(path, future, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s3, _ := NewStore(dir)
	if _, err := s3.List(testProject); err == nil {
		t.Fatalf("expected ErrFutureVersion")
	}
}

func TestDeleteProject(t *testing.T) {
	s := newStore(t)
	b := mustBoard(t, s, "Doomed board")
	if _, err := s.SaveSnapshot(testProject, b.ID, []byte(`{"shapes":[]}`), user()); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if _, err := s.Create("urn:other:project", testHub, "Other Project", Draft{Name: "Survivor"}, user()); err != nil {
		t.Fatalf("Create other: %v", err)
	}
	dirA := filepath.Join(s.dir, sanitizeID(testProject))
	if _, err := os.Stat(filepath.Join(dirA, "doc-"+b.ID+".json")); err != nil {
		t.Fatalf("board document missing before delete: %v", err)
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
	boards, err := s.List(testProject)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(boards) != 0 {
		t.Errorf("List after delete = %d boards, want 0", len(boards))
	}
	reborn := mustBoard(t, s, "Reborn")
	if reborn.ID != "w1" || reborn.Num != 1 {
		t.Errorf("recreated board = %s/%d, want fresh w1/1", reborn.ID, reborn.Num)
	}

	// Idempotent: delete again, then with the dir already gone.
	if err := s.DeleteProject(testProject); err != nil {
		t.Fatalf("second DeleteProject: %v", err)
	}
	if err := s.DeleteProject(testProject); err != nil {
		t.Errorf("DeleteProject on missing dir = %v, want nil", err)
	}
}
