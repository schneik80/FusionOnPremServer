package server

import (
	"testing"
	"time"
)

func newArchive(id, sessionID, itemID string) *archiveJob {
	return &archiveJob{
		ID:          id,
		SessionID:   sessionID,
		UserKey:     "user-1",
		HubID:       "hub-1",
		DMProjectID: "b.proj",
		ItemID:      itemID,
		DocName:     "Widget",
		CreatedAt:   time.Now(),
		status:      archiveQueued,
	}
}

func TestArchiveJob_FinishIsFirstWriterWins(t *testing.T) {
	j := newArchive("a1", "sess-1", "item-1")

	if !j.finish(archiveReady, "", "", "dl-1") {
		t.Fatal("first finish: want true (this call settled the job)")
	}
	// A cancel that raced completion, or a second error path, must not rewrite
	// the outcome — nor emit a second notification, which is what the bool
	// return exists to prevent.
	if j.finish(archiveError, "boom", "upstream_failed", "") {
		t.Error("second finish: want false")
	}
	id, ok := j.ready()
	if !ok || id != "dl-1" {
		t.Errorf("ready() = (%q, %v), want (\"dl-1\", true)", id, ok)
	}
}

func TestArchiveJob_CancelBeforeRunIsHonoured(t *testing.T) {
	j := newArchive("a1", "sess-1", "item-1")
	j.cancel()

	// setCancel must fire the context immediately for a job canceled while it
	// was still queued, or the run would proceed after the user gave up.
	fired := false
	j.setCancel(func() { fired = true })
	if !fired {
		t.Error("setCancel on an already-canceled job did not fire the cancel func")
	}
	// And a canceled job never becomes ready.
	j.setStatus(archivePreparing)
	if _, ok := j.ready(); ok {
		t.Error("canceled job reports ready")
	}
}

func TestArchiveManager_GetIsSessionScoped(t *testing.T) {
	m := newArchiveManager(2)
	m.add(newArchive("a1", "sess-1", "item-1"))

	if _, ok := m.get("a1", "sess-2"); ok {
		t.Error("another session fetched the job; archives must be invisible across sessions")
	}
	if _, ok := m.get("a1", "sess-1"); !ok {
		t.Error("owning session could not fetch its own job")
	}
}

func TestArchiveManager_ListIsSessionScopedAndOrdered(t *testing.T) {
	m := newArchiveManager(2)
	m.add(newArchive("a1", "sess-1", "item-1"))
	m.add(newArchive("b1", "sess-2", "item-2"))
	m.add(newArchive("a2", "sess-1", "item-3"))

	got := m.listFor("sess-1")
	if len(got) != 2 {
		t.Fatalf("listFor(sess-1) returned %d jobs, want 2", len(got))
	}
	if got[0].ID != "a1" || got[1].ID != "a2" {
		t.Errorf("order = %q, %q; want submission order a1, a2", got[0].ID, got[1].ID)
	}
}

func TestArchiveManager_ActiveForBlocksDuplicates(t *testing.T) {
	m := newArchiveManager(2)
	j := newArchive("a1", "sess-1", "item-1")
	m.add(j)

	if !m.activeFor("sess-1", "item-1", "") {
		t.Error("a queued job does not count as active; a second click would burn APS quota")
	}
	if m.activeFor("sess-1", "item-2", "") {
		t.Error("a different document reports active")
	}
	if m.activeFor("sess-2", "item-1", "") {
		t.Error("another session's job blocks this one")
	}
	// A pinned version of the same document is a different archive, so a tip
	// job in flight must not block it (nor the other way round).
	if m.activeFor("sess-1", "item-1", "urn:vf.item-1?version=3") {
		t.Error("a tip job blocks archiving a pinned version of the same document")
	}

	// Once it settles, the same document can be archived again.
	j.finish(archiveReady, "", "", "dl-1")
	if m.activeFor("sess-1", "item-1", "") {
		t.Error("a finished job still reports active")
	}
}

func TestArchiveManager_ListPrunesStaleTerminalJobs(t *testing.T) {
	m := newArchiveManager(2)

	old := newArchive("old", "sess-1", "item-1")
	old.finish(archiveReady, "", "", "dl-1")
	old.mu.Lock()
	old.finishedAt = time.Now().Add(-archiveRetention - time.Minute)
	old.mu.Unlock()
	m.add(old)

	recent := newArchive("recent", "sess-1", "item-2")
	recent.finish(archiveReady, "", "", "dl-2")
	m.add(recent)

	live := newArchive("live", "sess-1", "item-3")
	m.add(live)

	got := m.listFor("sess-1")
	if len(got) != 2 {
		t.Fatalf("listFor returned %d jobs, want 2 (the stale one pruned)", len(got))
	}
	for _, j := range got {
		if j.ID == "old" {
			t.Error("a job finished beyond the retention window is still listed")
		}
	}
	if _, ok := m.get("old", "sess-1"); ok {
		t.Error("pruned job is still fetchable")
	}
}

func TestArchiveManager_Dismiss(t *testing.T) {
	m := newArchiveManager(2)

	done := newArchive("done", "sess-1", "item-1")
	done.finish(archiveReady, "", "", "dl-1")
	m.add(done)
	m.add(newArchive("live", "sess-1", "item-2"))
	other := newArchive("other", "sess-2", "item-3")
	other.finish(archiveReady, "", "", "dl-3")
	m.add(other)

	// Dismiss-all clears only this session's terminal jobs.
	m.dismiss("", "sess-1")
	if _, ok := m.get("done", "sess-1"); ok {
		t.Error("dismissed job is still fetchable")
	}
	if _, ok := m.get("live", "sess-1"); !ok {
		t.Error("dismiss removed a job that was still running")
	}
	if _, ok := m.get("other", "sess-2"); !ok {
		t.Error("dismiss reached into another session's list")
	}
}

func TestArchiveFileName(t *testing.T) {
	cases := []struct {
		name     string
		docName  string
		version  int
		fileType string
		want     string
	}{
		{"plain", "Widget", 0, "f3z", "Widget.f3z"},
		{"spaces kept", "Front Bracket v2", 0, "f3d", "Front Bracket v2.f3d"},
		// Separators become underscores and the leading dots are trimmed, so
		// traversal can't survive and the result can't be a hidden dotfile.
		{"path separators neutralized", "../../etc/passwd", 0, "f3z", "_.._etc_passwd.f3z"},
		{"quotes neutralized", `He said "hi"`, 0, "f3z", "He said _hi_.f3z"},
		// CR/LF would otherwise let a document name inject a response header.
		{"newline neutralized", "Widget\r\nX-Header: y", 0, "f3z", "Widget__X-Header_ y.f3z"},
		{"empty falls back", "", 0, "f3z", "design.f3z"},
		// Every character replaced is still a usable, distinct name — the
		// fallback is only for having nothing left at all.
		{"all separators", "///", 0, "f3z", "___.f3z"},
		// A pinned version is named after the version it actually is, so two
		// archives of one design can't collide in the download folder.
		{"pinned version", "Widget", 3, "f3z", "Widget-v3.f3z"},
		{"pinned version on a fallback name", "", 12, "f3d", "design-v12.f3d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := archiveFileName(tc.docName, tc.version, tc.fileType); got != tc.want {
				t.Errorf("archiveFileName(%q, %d, %q) = %q, want %q",
					tc.docName, tc.version, tc.fileType, got, tc.want)
			}
		})
	}
}

func TestSanitizeDownloadName_BoundsLength(t *testing.T) {
	long := ""
	for range 300 {
		long += "a"
	}
	got := sanitizeDownloadName(long)
	if len(got) > 120 {
		t.Errorf("sanitized name is %d chars, want <= 120", len(got))
	}
}
