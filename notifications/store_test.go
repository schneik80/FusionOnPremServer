package notifications

import (
	"fmt"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestAddAndList(t *testing.T) {
	s := newTestStore(t)
	const u = "user-sub-1"
	for i := 0; i < 3; i++ {
		if _, created, err := s.Add(u, Notification{Kind: KindMention, Subject: fmt.Sprintf("ch%d", i)}); err != nil || !created {
			t.Fatalf("Add %d: created=%v err=%v", i, created, err)
		}
	}
	list, err := s.List(u, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List returned %d, want 3", len(list))
	}
	// Newest first.
	if list[0].Subject != "ch2" || list[2].Subject != "ch0" {
		t.Fatalf("List order wrong: %q..%q", list[0].Subject, list[2].Subject)
	}
	if n, err := s.UnreadCount(u); err != nil || n != 3 {
		t.Fatalf("UnreadCount = %d, %v; want 3", n, err)
	}
}

func TestAddDedupe(t *testing.T) {
	s := newTestStore(t)
	const u = "u"
	first, created, err := s.Add(u, Notification{Kind: KindOverdue, DedupeKey: "overdue:t7", Subject: "A"})
	if err != nil || !created {
		t.Fatalf("first Add: created=%v err=%v", created, err)
	}
	again, created, err := s.Add(u, Notification{Kind: KindOverdue, DedupeKey: "overdue:t7", Subject: "B"})
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if created {
		t.Fatal("second Add with same dedupe key should not create")
	}
	if again.ID != first.ID {
		t.Fatalf("dedupe returned different entry: %q vs %q", again.ID, first.ID)
	}
	if list, _ := s.List(u, 0); len(list) != 1 {
		t.Fatalf("dedupe left %d entries, want 1", len(list))
	}
}

func TestMarkRead(t *testing.T) {
	s := newTestStore(t)
	const u = "u"
	a, _, _ := s.Add(u, Notification{Kind: KindMention})
	_, _, _ = s.Add(u, Notification{Kind: KindMention})
	unread, err := s.MarkRead(u, []string{a.ID})
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unread after marking one read = %d, want 1", unread)
	}
	unread, err = s.MarkAllRead(u)
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if unread != 0 {
		t.Fatalf("unread after mark-all = %d, want 0", unread)
	}
	list, _ := s.List(u, 0)
	for _, n := range list {
		if !n.Read || n.ReadAt == nil {
			t.Fatalf("notification %s not marked read (read=%v readAt=%v)", n.ID, n.Read, n.ReadAt)
		}
	}
}

func TestDismiss(t *testing.T) {
	s := newTestStore(t)
	const u = "u"
	a, _, _ := s.Add(u, Notification{Kind: KindMention})
	unread, err := s.Delete(u, a.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if unread != 0 {
		t.Fatalf("unread after dismiss = %d, want 0", unread)
	}
	if list, _ := s.List(u, 0); len(list) != 0 {
		t.Fatalf("dismiss left %d entries, want 0", len(list))
	}
	// Dismissing a missing id is a no-op, not an error.
	if _, err := s.Delete(u, "nope"); err != nil {
		t.Fatalf("Delete missing id: %v", err)
	}
}

func TestPruneOldest(t *testing.T) {
	s := newTestStore(t)
	const u = "u"
	for i := 0; i < MaxPerUser+10; i++ {
		if _, _, err := s.Add(u, Notification{Kind: KindMention, Subject: fmt.Sprintf("%d", i)}); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	list, _ := s.List(u, 0)
	if len(list) != MaxPerUser {
		t.Fatalf("inbox size = %d, want capped at %d", len(list), MaxPerUser)
	}
	// Newest survived; oldest pruned. Newest first, so [0] is the last added.
	if list[0].Subject != fmt.Sprintf("%d", MaxPerUser+9) {
		t.Fatalf("newest entry = %q, want %d", list[0].Subject, MaxPerUser+9)
	}
}

func TestListLimit(t *testing.T) {
	s := newTestStore(t)
	const u = "u"
	for i := 0; i < 5; i++ {
		_, _, _ = s.Add(u, Notification{Kind: KindMention})
	}
	if list, _ := s.List(u, 2); len(list) != 2 {
		t.Fatalf("List(limit=2) = %d, want 2", len(list))
	}
}

func TestAddRejectsUnknownKind(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.Add("u", Notification{Kind: "bogus"}); err == nil {
		t.Fatal("Add with unknown kind should error")
	}
	if _, _, err := s.Add("", Notification{Kind: KindMention}); err == nil {
		t.Fatal("Add with empty user key should error")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewStore(dir)
	const u = "user@example.com"
	a, _, _ := s1.Add(u, Notification{Kind: KindAssigned, Subject: "Fix rev B", DedupeKey: "assign:t1:v2"})
	_, _ = s1.MarkRead(u, []string{a.ID})

	// A fresh store over the same dir reloads the file from disk.
	s2, _ := NewStore(dir)
	list, err := s2.List(u, 0)
	if err != nil {
		t.Fatalf("reload List: %v", err)
	}
	if len(list) != 1 || list[0].Subject != "Fix rev B" || !list[0].Read {
		t.Fatalf("reload mismatch: %+v", list)
	}
	if has, _ := s2.HasDedupe(u, "assign:t1:v2"); !has {
		t.Fatal("HasDedupe should find the persisted key")
	}
}
