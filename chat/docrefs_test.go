package chat

import (
	"fmt"
	"os"
	"testing"
)

const (
	docItem  = "urn:adsk.wipprod:dm.lineage:hC6k4hndRWaeIVhIjvHu8w"
	docToken = "fls:doc?hubId=b.1&itemId=urn%3Aadsk.wipprod%3Adm.lineage%3AhC6k4hndRWaeIVhIjvHu8w&name=bracket&kind=design"
)

// TestFindDocRefs_AggregatesPerChannel covers the shape the graph consumes —
// one hit per channel with a count and the newest mention — and the replay
// semantics that make it correct: an edit that removes the token drops the
// message, a deleted message never counts.
func TestFindDocRefs_AggregatesPerChannel(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root, err := st.EnsureRoot("p1")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	post := func(body string) Message {
		t.Helper()
		n++
		m, _, err := st.CreateMessage("p1", root.ID, "u1", "Ada", fmt.Sprintf("c%d", n), body, 0)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	post("first look at " + docToken)
	post("no reference here")
	edited := post("temporary " + docToken)
	deleted := post("also " + docToken)
	last := post("latest word on " + docToken)

	if _, err := st.EditMessage("p1", root.ID, edited.Seq, "changed my mind"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteMessage("p1", root.ID, deleted.Seq); err != nil {
		t.Fatal(err)
	}

	got, err := st.FindDocRefs([]string{"p1"}, docItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("hits = %+v, want one channel", got)
	}
	h := got[0]
	if h.Count != 2 {
		t.Errorf("count = %d, want 2 (edited-away and deleted must not count)", h.Count)
	}
	if h.LastSeq != last.Seq {
		t.Errorf("last seq = %d, want the newest live mention %d", h.LastSeq, last.Seq)
	}
	if h.LastAuthor != "Ada" || h.LastBody == "" {
		t.Errorf("excerpt provenance missing: %+v", h)
	}
	if h.ChannelName != RootChannelName || h.IsPrivate {
		t.Errorf("channel identity wrong: %+v", h)
	}
}

// TestFindDocRefs_EditIntoAMatch is the reason the scan replays the whole log
// instead of only reading create records: a message can acquire a reference
// long after it was posted.
func TestFindDocRefs_EditIntoAMatch(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root, err := st.EnsureRoot("p1")
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := st.CreateMessage("p1", root.ID, "u1", "Ada", "c1", "placeholder", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EditMessage("p1", root.ID, m.Seq, "actually see "+docToken); err != nil {
		t.Fatal(err)
	}
	got, err := st.FindDocRefs([]string{"p1"}, docItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Count != 1 || got[0].LastAuthor != "Ada" {
		t.Fatalf("hits = %+v, want the edited message with its create's author", got)
	}
}

// TestFindDocRefs_PrivateChannelsCarryTheirACL: the store reports private
// channels with their member list rather than filtering them, because the
// filter is the caller's (the handler applies the ACL).
func TestFindDocRefs_PrivateChannelsCarryTheirACL(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ch, err := st.CreateChannel("p1", "secret", "", "u1", true, []string{"u2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateMessage("p1", ch.ID, "u1", "Ada", "c1", "see "+docToken, 0); err != nil {
		t.Fatal(err)
	}
	got, err := st.FindDocRefs([]string{"p1"}, docItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("hits = %+v, want the private channel", got)
	}
	if !got[0].IsPrivate || len(got[0].Members) == 0 {
		t.Fatalf("private hit lost its ACL: %+v", got[0])
	}
}

// TestFindDocRefs_ScopesAndDoesNotWrite holds two invariants at once: the
// shared project scoping, and that a read-only scan never creates chat data
// for a project that has none (Channels() would lazily make a root channel).
func TestFindDocRefs_ScopesAndDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root, err := st.EnsureRoot("p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateMessage("p1", root.ID, "u1", "Ada", "c1", "see "+docToken, 0); err != nil {
		t.Fatal(err)
	}

	if got, err := st.FindDocRefs(nil, docItem); err != nil || len(got) != 0 {
		t.Fatalf("FindDocRefs(nil) = %v, %v; want empty, nil", got, err)
	}
	if got, err := st.FindDocRefs([]string{"p2"}, docItem); err != nil || len(got) != 0 {
		t.Fatalf("out-of-scope hits = %v, %v; want none", got, err)
	}

	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.FindDocRefs([]string{"p1", "p2", "p3"}, docItem); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("scan created chat data: %d dirs before, %d after", len(before), len(after))
	}
}
