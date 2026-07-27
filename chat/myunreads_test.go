package chat

import (
	"fmt"
	"os"
	"testing"
)

func seedChannelWith(t *testing.T, st *Store, projectID, name string, private bool, members []string, n int) Channel {
	t.Helper()
	ch, err := st.CreateChannel(projectID, name, "", "u-author", private, members)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, _, err := st.CreateMessage(projectID, ch.ID, "u-author", "Author",
			fmt.Sprintf("%s-%s-%d", projectID, ch.ID, i), "hello", 0); err != nil {
			t.Fatal(err)
		}
	}
	return ch
}

// TestMyUnreads_OnlyChannelsTheUserParticipatesIn is the security case. The
// scan has no APS call and therefore cannot ask which projects the caller may
// see, so it must confine itself to channels the caller demonstrably belongs
// to — otherwise the bell would name every channel in the hub.
func TestMyUnreads_OnlyChannelsTheUserParticipatesIn(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// A channel the user has read before (so holds a cursor for).
	mine := seedChannelWith(t, st, "p1", "mine", false, nil, 3)
	if _, _, err := st.SetReadCursor("p1", "u-me", mine.ID, 1); err != nil {
		t.Fatal(err)
	}
	// A public channel in the same project the user has never opened.
	seedChannelWith(t, st, "p1", "never-opened", false, nil, 5)
	// A private channel the user is a member of but has not read.
	seedChannelWith(t, st, "p1", "invited", true, []string{"u-me"}, 2)
	// A private channel the user is NOT in — must never appear.
	seedChannelWith(t, st, "p2", "secret", true, []string{"u-someone"}, 4)
	// A whole project the user has nothing to do with.
	seedChannelWith(t, st, "p3", "elsewhere", false, nil, 6)

	got, err := st.MyUnreads("u-me")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ChannelUnread{}
	for _, u := range got {
		byName[u.ChannelName] = u
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d (%v), want the read channel and the private invite only", len(got), byName)
	}
	if u, ok := byName["mine"]; !ok || u.UnreadCount != 2 {
		t.Errorf("read channel: %+v, want 2 unread past the cursor", u)
	}
	if u, ok := byName["invited"]; !ok || u.UnreadCount != 2 {
		t.Errorf("private invite: %+v, want 2 unread", u)
	}
	for _, leaked := range []string{"never-opened", "secret", "elsewhere"} {
		if _, bad := byName[leaked]; bad {
			t.Errorf("leaked channel %q into the inbox", leaked)
		}
	}
}

// TestMyUnreads_ReadingAChannelClearsItsRow is the property that makes derived
// rows safe: nothing has to remember to delete them.
func TestMyUnreads_ReadingAChannelClearsItsRow(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ch := seedChannelWith(t, st, "p1", "talk", false, nil, 3)
	if _, _, err := st.SetReadCursor("p1", "u-me", ch.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.MyUnreads("u-me"); len(got) != 1 || got[0].UnreadCount != 2 {
		t.Fatalf("before reading: %+v, want one row with 2 unread", got)
	}
	// Read to the end.
	if _, _, err := st.SetReadCursor("p1", "u-me", ch.ID, 3); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.MyUnreads("u-me"); len(got) != 0 {
		t.Fatalf("after reading: %+v, want no rows", got)
	}
}

// TestMyUnreads_ArchivedAndDeletedDoNotCount: an archived channel is not
// somewhere to be sent back to, and a deleted message is not unread.
func TestMyUnreads_ArchivedAndDeletedDoNotCount(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	live := seedChannelWith(t, st, "p1", "live", false, nil, 3)
	if _, _, err := st.SetReadCursor("p1", "u-me", live.ID, 1); err != nil {
		t.Fatal(err)
	}
	archived := seedChannelWith(t, st, "p1", "old", false, nil, 3)
	if _, _, err := st.SetReadCursor("p1", "u-me", archived.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveChannel("p1", archived.ID); err != nil {
		t.Fatal(err)
	}
	// Delete one of the live channel's two unread messages.
	if _, err := st.DeleteMessage("p1", live.ID, 2); err != nil {
		t.Fatal(err)
	}

	got, err := st.MyUnreads("u-me")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ChannelName != "live" {
		t.Fatalf("rows = %+v, want the live channel only", got)
	}
	if got[0].UnreadCount != 1 {
		t.Errorf("unread = %d, want 1 of 2 (the deleted message must not count)", got[0].UnreadCount)
	}
}

// TestMyUnreads_ScanDoesNotWrite: the bell polls, so a scan that lazily created
// a root channel or bumped an event epoch in every project it looked at would
// rewrite the store on a timer.
func TestMyUnreads_ScanDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ch := seedChannelWith(t, st, "p1", "talk", false, nil, 1)
	if _, _, err := st.SetReadCursor("p1", "u-me", ch.ID, 0); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.MyUnreads("u-nobody"); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("scanning created project data: %d dirs before, %d after", len(before), len(after))
	}
	// And a user with nothing to their name gets nothing.
	if got, _ := st.MyUnreads("u-nobody"); len(got) != 0 {
		t.Fatalf("stranger's inbox = %+v, want empty", got)
	}
}
