package whiteboards

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- helpers -------------------------------------------------------------

func newRooms(t *testing.T) (*Store, *Rooms, Board) {
	t.Helper()
	s := newStore(t)
	rs := NewRooms(s)
	b := mustBoard(t, s, "Board")
	return s, rs, b
}

func rec(s string) json.RawMessage { return json.RawMessage(s) }

func put(client string, seq, base int64, records map[string]string) DocPatchRequest {
	p := DocPatch{Put: map[string]json.RawMessage{}}
	for id, body := range records {
		p.Put[id] = rec(body)
	}
	return DocPatchRequest{ClientID: client, Seq: seq, BaseRev: base, DocPatch: p}
}

func remove(client string, seq, base int64, ids ...string) DocPatchRequest {
	return DocPatchRequest{ClientID: client, Seq: seq, BaseRev: base, DocPatch: DocPatch{Remove: ids}}
}

func mustApply(t *testing.T, rs *Rooms, b Board, req DocPatchRequest) Applied {
	t.Helper()
	got, err := rs.Apply(testProject, b.ID, req, user())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return got
}

func liveRecords(t *testing.T, rs *Rooms, b Board) map[string]json.RawMessage {
	t.Helper()
	doc, _, err := rs.Snapshot(testProject, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ld liveDoc
	if err := json.Unmarshal(doc, &ld); err != nil {
		t.Fatal(err)
	}
	return ld.Store
}

// --- applying ------------------------------------------------------------

// TestApply_RevisionAdvancesPerPatch: the revision counts patches, not records.
// Clients compare against it to detect gaps, so a patch touching forty shapes
// must advance it exactly once.
func TestApply_RevisionAdvancesPerPatch(t *testing.T) {
	_, rs, b := newRooms(t)
	got := mustApply(t, rs, b, put("c1", 1, 0, map[string]string{
		"shape:a": `{"id":"shape:a"}`,
		"shape:b": `{"id":"shape:b"}`,
	}))
	if got.Rev != 1 {
		t.Fatalf("rev = %d, want 1", got.Rev)
	}
	got = mustApply(t, rs, b, put("c1", 2, 1, map[string]string{"shape:c": `{"id":"shape:c"}`}))
	if got.Rev != 2 {
		t.Fatalf("rev = %d, want 2", got.Rev)
	}
	if n := len(liveRecords(t, rs, b)); n != 3 {
		t.Fatalf("records = %d, want 3", n)
	}
}

// TestApply_RetriedPatchIsNotAppliedTwice: a client whose response was lost
// retries. Re-applying would undo an edit the user has made since.
func TestApply_RetriedPatchIsNotAppliedTwice(t *testing.T) {
	_, rs, b := newRooms(t)
	mustApply(t, rs, b, put("c1", 1, 0, map[string]string{"shape:a": `{"v":1}`}))
	// The user moves on; someone else's edit lands.
	mustApply(t, rs, b, put("c2", 1, 1, map[string]string{"shape:a": `{"v":2}`}))
	// Now c1's retry of its FIRST patch arrives.
	got := mustApply(t, rs, b, put("c1", 1, 0, map[string]string{"shape:a": `{"v":1}`}))
	if got.Rev != 2 {
		t.Errorf("retry rev = %d, want the current 2", got.Rev)
	}
	if string(liveRecords(t, rs, b)["shape:a"]) != `{"v":2}` {
		t.Error("a retried patch clobbered a newer value")
	}
}

// TestApply_TombstoneStopsResurrection is the delete-vs-edit race: A deletes a
// shape, B (who hadn't heard) drags it. Naive last-write-wins brings the shape
// back from the dead.
func TestApply_TombstoneStopsResurrection(t *testing.T) {
	_, rs, b := newRooms(t)
	mustApply(t, rs, b, put("a", 1, 0, map[string]string{"shape:x": `{"v":1}`}))
	mustApply(t, rs, b, remove("a", 2, 1, "shape:x"))

	// B is still at revision 1 and pushes its drag.
	got := mustApply(t, rs, b, put("b", 1, 1, map[string]string{"shape:x": `{"v":2}`}))
	if _, alive := liveRecords(t, rs, b)["shape:x"]; alive {
		t.Fatal("deleted shape was resurrected")
	}
	if len(got.Rejected) != 1 || got.Rejected[0] != "shape:x" {
		t.Fatalf("rejected = %v, want shape:x so the client can drop it too", got.Rejected)
	}
	// Nothing changed, so no revision was burned and nothing is broadcast.
	if got.Rev != 2 || !got.Patch.empty() {
		t.Fatalf("rejected-only patch changed state: rev=%d patch=%+v", got.Rev, got.Patch)
	}

	// A client that HAS seen the delete may legitimately re-create the id.
	got = mustApply(t, rs, b, put("b", 2, 2, map[string]string{"shape:x": `{"v":3}`}))
	if len(got.Rejected) != 0 {
		t.Fatalf("re-creation after seeing the delete was rejected: %v", got.Rejected)
	}
	if string(liveRecords(t, rs, b)["shape:x"]) != `{"v":3}` {
		t.Error("re-created shape did not take")
	}
}

// TestApply_BindingCascade: removing a shape must take its bindings with it, or
// peers are left with an arrow bound to nothing — which tldraw's own validation
// rejects, forcing a full resync.
func TestApply_BindingCascade(t *testing.T) {
	_, rs, b := newRooms(t)
	mustApply(t, rs, b, put("a", 1, 0, map[string]string{
		"shape:from":  `{"id":"shape:from"}`,
		"shape:to":    `{"id":"shape:to"}`,
		"shape:other": `{"id":"shape:other"}`,
		"binding:1":   `{"fromId":"shape:from","toId":"shape:to"}`,
		"binding:2":   `{"fromId":"shape:other","toId":"shape:other"}`,
	}))
	got := mustApply(t, rs, b, remove("a", 2, 1, "shape:to"))

	live := liveRecords(t, rs, b)
	if _, ok := live["binding:1"]; ok {
		t.Error("binding to a removed shape survived")
	}
	if _, ok := live["binding:2"]; !ok {
		t.Error("unrelated binding was swept up in the cascade")
	}
	// The cascade must be BROADCAST, or peers keep the dangling binding.
	found := false
	for _, id := range got.Patch.Remove {
		if id == "binding:1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cascade not broadcast: %v", got.Patch.Remove)
	}
}

// TestApply_StructuralRecordsNeedCurrentRevision: pages are rare, user-driven
// and catastrophic to race, so they get true optimistic concurrency rather than
// last-write-wins.
func TestApply_StructuralRecordsNeedCurrentRevision(t *testing.T) {
	_, rs, b := newRooms(t)
	mustApply(t, rs, b, put("a", 1, 0, map[string]string{"shape:a": `{}`}))

	_, err := rs.Apply(testProject, b.ID, put("b", 1, 0, map[string]string{"page:1": `{}`}), user())
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale page write = %v, want ErrConflict", err)
	}
	// At the current revision it is allowed.
	if _, err := rs.Apply(testProject, b.ID, put("b", 2, 1, map[string]string{"page:1": `{}`}), user()); err != nil {
		t.Fatalf("current page write: %v", err)
	}
	// Ordinary shapes are unaffected: a stale base is fine, LWW settles it.
	if _, err := rs.Apply(testProject, b.ID, put("c", 1, 0, map[string]string{"shape:z": `{}`}), user()); err != nil {
		t.Fatalf("stale shape write should be allowed: %v", err)
	}
}

// TestApply_RejectsNonDocumentRecords: camera, pointer and presence records are
// per-user view state. Accepting one would move every peer's viewport or write
// a cursor into the saved document.
func TestApply_RejectsNonDocumentRecords(t *testing.T) {
	_, rs, b := newRooms(t)
	for _, id := range []string{
		"instance:x", "camera:x", "pointer:x", "instance_page_state:x", "instance_presence:x", "",
	} {
		req := DocPatchRequest{ClientID: "c", Seq: 1, DocPatch: DocPatch{
			Put: map[string]json.RawMessage{id: rec(`{}`)},
		}}
		if _, err := rs.Apply(testProject, b.ID, req, user()); !errors.Is(err, ErrInvalid) {
			t.Errorf("record %q = %v, want ErrInvalid", id, err)
		}
	}
}

// TestApply_BaseRevisionAheadIsRefused: a client claiming a revision the room
// has never reached (a restore beneath it) must resync, not be guessed at.
func TestApply_BaseRevisionAheadIsRefused(t *testing.T) {
	_, rs, b := newRooms(t)
	_, err := rs.Apply(testProject, b.ID, put("c", 1, 99, map[string]string{"shape:a": `{}`}), user())
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("future base revision = %v, want ErrConflict", err)
	}
}

// TestApply_Caps guards the size limits that stop one paste stalling everyone
// on the board.
func TestApply_Caps(t *testing.T) {
	_, rs, b := newRooms(t)
	big := make([]byte, MaxRecordBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	oversize := DocPatchRequest{ClientID: "c", Seq: 1, DocPatch: DocPatch{
		Put: map[string]json.RawMessage{"asset:big": rec(`"` + string(big) + `"`)},
	}}
	if _, err := rs.Apply(testProject, b.ID, oversize, user()); !errors.Is(err, ErrInvalid) {
		t.Errorf("oversized record = %v, want ErrInvalid", err)
	}
	empty := DocPatchRequest{ClientID: "c", Seq: 1}
	if _, err := rs.Apply(testProject, b.ID, empty, user()); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty patch = %v, want ErrInvalid", err)
	}
	noClient := put("", 1, 0, map[string]string{"shape:a": `{}`})
	if _, err := rs.Apply(testProject, b.ID, noClient, user()); !errors.Is(err, ErrInvalid) {
		t.Errorf("missing clientId = %v, want ErrInvalid", err)
	}
}

// --- persistence ---------------------------------------------------------

// TestRooms_PersistenceCoalescesAndSurvivesReload: many patches become one
// file write, and the revision written is the room's — not one increment per
// save, or the file and the room would drift apart.
func TestRooms_PersistenceCoalescesAndSurvivesReload(t *testing.T) {
	s, rs, b := newRooms(t)
	clock := time.Now()
	rs.now = func() time.Time { return clock }

	for i := int64(1); i <= 5; i++ {
		mustApply(t, rs, b, put("c1", i, i-1, map[string]string{"shape:a": `{"v":1}`}))
	}
	// Nothing settled yet, so nothing is on disk.
	if _, rev, _ := s.Document(testProject, b.ID); rev != 0 {
		t.Fatalf("stored rev = %d, want 0 before the debounce", rev)
	}

	clock = clock.Add(RoomSaveQuiet + time.Second)
	rs.Sweep()

	doc, rev, err := s.Document(testProject, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rev != 5 {
		t.Fatalf("stored rev = %d, want the room's 5", rev)
	}
	var ld liveDoc
	if err := json.Unmarshal(doc, &ld); err != nil {
		t.Fatalf("stored document is not a tldraw snapshot: %v", err)
	}
	if _, ok := ld.Store["shape:a"]; !ok {
		t.Fatalf("stored document lost the shape: %s", doc)
	}
}

// TestRooms_SaveCeilingFiresUnderContinuousEditing: the quiet timer alone would
// never fire while someone keeps drawing, so unsaved work must still checkpoint.
func TestRooms_SaveCeilingFiresUnderContinuousEditing(t *testing.T) {
	s, rs, b := newRooms(t)
	clock := time.Now()
	rs.now = func() time.Time { return clock }

	for i := int64(1); i <= 30; i++ {
		mustApply(t, rs, b, put("c1", i, i-1, map[string]string{"shape:a": `{"v":1}`}))
		clock = clock.Add(time.Second) // never quiet
		rs.Sweep()
	}
	if _, rev, _ := s.Document(testProject, b.ID); rev == 0 {
		t.Fatal("continuous editing never checkpointed")
	}
}

// TestRooms_IdleEvictionFlushesFirst: a room may only be forgotten once its
// work is on disk, and reopening it must come back at the same revision.
func TestRooms_IdleEvictionFlushesFirst(t *testing.T) {
	s, rs, b := newRooms(t)
	clock := time.Now()
	rs.now = func() time.Time { return clock }

	if err := rs.Open(testProject, b.ID); err != nil {
		t.Fatal(err)
	}
	mustApply(t, rs, b, put("c1", 1, 0, map[string]string{"shape:a": `{"v":1}`}))
	rs.Release(testProject, b.ID)

	clock = clock.Add(RoomIdleTTL + time.Second)
	rs.Sweep()
	if rs.Live() != 0 {
		t.Fatalf("idle room was not evicted: %d live", rs.Live())
	}
	if _, rev, _ := s.Document(testProject, b.ID); rev != 1 {
		t.Fatalf("evicted room did not flush: stored rev %d", rev)
	}
	// Reopening reloads from disk at the same revision.
	rev, err := rs.Rev(testProject, b.ID)
	if err != nil || rev != 1 {
		t.Fatalf("reopened rev = %d (err %v), want 1", rev, err)
	}
}

// TestRooms_DropAllWritesNothing is the backup-restore invariant: the files
// have just been replaced, and flushing a live room would overwrite the
// restored board with the state from before the restore.
func TestRooms_DropAllWritesNothing(t *testing.T) {
	s, rs, b := newRooms(t)
	mustApply(t, rs, b, put("c1", 1, 0, map[string]string{"shape:a": `{"v":1}`}))

	s.Reset() // the restore path — must discard, not flush

	if rs.Live() != 0 {
		t.Fatalf("Reset left %d live rooms", rs.Live())
	}
	if _, rev, _ := s.Document(testProject, b.ID); rev != 0 {
		t.Fatalf("Reset wrote a live room to disk (rev %d) — it would have overwritten the restore", rev)
	}
}

// TestRooms_DeleteDropsTheRoom: without this an eviction flush recreates the
// document file after the board it belonged to is gone.
func TestRooms_DeleteDropsTheRoom(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rs := NewRooms(s)
	b := mustBoard(t, s, "Board")
	mustApply(t, rs, b, put("c1", 1, 0, map[string]string{"shape:a": `{"v":1}`}))

	if err := s.Delete(testProject, b.ID); err != nil {
		t.Fatal(err)
	}
	rs.FlushAll()

	docPath := filepath.Join(dir, sanitizeID(testProject), "doc-"+sanitizeID(b.ID)+".json")
	if _, err := os.Stat(docPath); !os.IsNotExist(err) {
		t.Fatal("a flush resurrected the document of a deleted board")
	}
}

// TestRooms_ReplaceBumpsRevision: a full-document PUT landing while people are
// editing must go through the room, or the room and the file would disagree
// about what the board contains.
func TestRooms_ReplaceBumpsRevision(t *testing.T) {
	_, rs, b := newRooms(t)
	mustApply(t, rs, b, put("c1", 1, 0, map[string]string{"shape:a": `{"v":1}`}))

	rev, live, err := rs.Replace(testProject, b.ID, []byte(`{"store":{"shape:z":{"v":9}}}`), user())
	if err != nil || !live {
		t.Fatalf("Replace on a live board: rev=%d live=%v err=%v", rev, live, err)
	}
	if rev != 2 {
		t.Fatalf("rev after replace = %d, want 2", rev)
	}
	live2 := liveRecords(t, rs, b)
	if _, gone := live2["shape:a"]; gone {
		t.Error("replace did not clear the previous document")
	}
	if _, ok := live2["shape:z"]; !ok {
		t.Error("replace did not install the new document")
	}
}

// TestRooms_CorruptDocumentRefusesToOpen: starting an empty room over an
// unreadable file would look like a blank board, and the first flush would
// overwrite whatever the file really held.
func TestRooms_CorruptDocumentRefusesToOpen(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rs := NewRooms(s)
	b := mustBoard(t, s, "Board")
	if _, err := s.SaveSnapshot(testProject, b.ID, []byte(`{"store":{}}`), user(), 0, false); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, sanitizeID(testProject), "doc-"+sanitizeID(b.ID)+".json")
	if err := os.WriteFile(docPath, []byte(`{"store": [not json`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rs.Open(testProject, b.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("opening a corrupt document = %v, want ErrInvalid", err)
	}
}

// TestRooms_MaxLiveRoomsRefusesRatherThanGrows: a room holds a whole document
// resident, so the cap is the memory bound. Idle rooms are reclaimed first.
func TestRooms_MaxLiveRoomsRefusesRatherThanGrows(t *testing.T) {
	s, rs, _ := newRooms(t)
	boards := make([]Board, 0, MaxLiveRooms+1)
	for i := 0; i < MaxLiveRooms+1; i++ {
		boards = append(boards, mustBoard(t, s, "B"))
	}
	for i := 0; i < MaxLiveRooms; i++ {
		if err := rs.Open(testProject, boards[i].ID); err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
	}
	if err := rs.Open(testProject, boards[MaxLiveRooms].ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("beyond the cap = %v, want ErrBusy", err)
	}
	// Freeing one lets the next in.
	rs.Release(testProject, boards[0].ID)
	rs.now = func() time.Time { return time.Now().Add(RoomIdleTTL + time.Minute) }
	if err := rs.Open(testProject, boards[MaxLiveRooms].ID); err != nil {
		t.Fatalf("after releasing one: %v", err)
	}
}
