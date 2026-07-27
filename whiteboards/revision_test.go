package whiteboards

import (
	"errors"
	"testing"
)

func otherUser() UserRef { return UserRef{ID: "sub-2", Name: "Bob", Email: "bob@example.com"} }

// TestSaveSnapshot_StaleSaveIsRefused is the whole point of the revision: two
// people open one board, both autosave their own full document, and the later
// save must NOT silently replace the earlier one's work.
func TestSaveSnapshot_StaleSaveIsRefused(t *testing.T) {
	s := newStore(t)
	b := mustBoard(t, s, "Board")

	// Both clients opened the board at revision 0.
	alice := []byte(`{"store":{"shape:a":{"id":"shape:a"}}}`)
	bob := []byte(`{"store":{"shape:b":{"id":"shape:b"}}}`)

	after, err := s.SaveSnapshot(testProject, b.ID, alice, user(), 0, false)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	if after.DocRev != 1 {
		t.Fatalf("rev after first save = %d, want 1", after.DocRev)
	}

	if _, err := s.SaveSnapshot(testProject, b.ID, bob, otherUser(), 0, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save error = %v, want ErrConflict", err)
	}

	// Alice's document survived, untouched.
	doc, rev, err := s.Document(testProject, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(doc) != string(alice) || rev != 1 {
		t.Fatalf("refused save still altered the board: doc=%q rev=%d", doc, rev)
	}
}

// TestSaveSnapshot_CurrentRevisionSavesAndAdvances covers the ordinary
// autosave loop: each save carries the revision the last one returned.
func TestSaveSnapshot_CurrentRevisionSavesAndAdvances(t *testing.T) {
	s := newStore(t)
	b := mustBoard(t, s, "Board")

	rev := int64(0)
	for i := 0; i < 3; i++ {
		after, err := s.SaveSnapshot(testProject, b.ID, []byte(`{"store":{}}`), user(), rev, false)
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		if after.DocRev != rev+1 {
			t.Fatalf("save %d: rev = %d, want %d", i, after.DocRev, rev+1)
		}
		rev = after.DocRev
	}
}

// TestSaveSnapshot_ForceOverwrites is the user's acknowledged "overwrite
// anyway" after being shown the conflict — it must still advance the revision,
// so the OTHER client's next save is refused in turn rather than racing back.
func TestSaveSnapshot_ForceOverwrites(t *testing.T) {
	s := newStore(t)
	b := mustBoard(t, s, "Board")

	if _, err := s.SaveSnapshot(testProject, b.ID, []byte(`{"store":{"a":1}}`), user(), 0, false); err != nil {
		t.Fatal(err)
	}
	forced := []byte(`{"store":{"b":2}}`)
	after, err := s.SaveSnapshot(testProject, b.ID, forced, otherUser(), 0, true)
	if err != nil {
		t.Fatalf("forced save: %v", err)
	}
	if after.DocRev != 2 {
		t.Fatalf("forced save rev = %d, want 2 (must still advance)", after.DocRev)
	}
	doc, rev, err := s.Document(testProject, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(doc) != string(forced) || rev != 2 {
		t.Fatalf("force did not take: doc=%q rev=%d", doc, rev)
	}
	// The client that was overwritten is now the stale one.
	if _, err := s.SaveSnapshot(testProject, b.ID, []byte(`{"store":{}}`), user(), 1, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("overwritten client's next save = %v, want ErrConflict", err)
	}
}

// TestSaveSnapshot_RevisionSurvivesReload: the revision lives in
// whiteboards.json, not in memory, so a restarted server still refuses a save
// from a client that has been holding a stale document across the restart.
func TestSaveSnapshot_RevisionSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := mustBoard(t, s, "Board")
	if _, err := s.SaveSnapshot(testProject, b.ID, []byte(`{"store":{}}`), user(), 0, false); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.SaveSnapshot(testProject, b.ID, []byte(`{"store":{}}`), otherUser(), 0, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("after reload, stale save = %v, want ErrConflict", err)
	}
	if _, err := s2.SaveSnapshot(testProject, b.ID, []byte(`{"store":{}}`), user(), 1, false); err != nil {
		t.Fatalf("after reload, current save: %v", err)
	}
}

// TestSaveSnapshot_LegacyFileStartsAtZero: boards written before this field
// existed decode with DocRev 0, so the first client to open one saves cleanly
// rather than being locked out of its own board.
func TestSaveSnapshot_LegacyFileStartsAtZero(t *testing.T) {
	s := newStore(t)
	b := mustBoard(t, s, "Board")

	ps, err := s.project(testProject)
	if err != nil {
		t.Fatal(err)
	}
	ps.mu.Lock()
	findBoard(ps.file, b.ID).DocRev = 0 // as an absent json field decodes
	ps.mu.Unlock()

	if _, err := s.SaveSnapshot(testProject, b.ID, []byte(`{"store":{}}`), user(), 0, false); err != nil {
		t.Fatalf("first save on a pre-revision board: %v", err)
	}
}
