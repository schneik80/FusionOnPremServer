package chat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testProject = "urn:adsk.wipprod:fs.folder:co.example/123"

func newTestStore(t *testing.T) *Store {
	return newStoreAt(t, t.TempDir())
}

// newStoreAt opens a Store over dir and registers its Close as cleanup —
// Windows can't remove the TempDir while message-log handles are open.
func newStoreAt(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestEnsureRoot_CreatesOnceAndPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	root, err := s.EnsureRoot(testProject)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	if !root.IsRoot || root.Name != RootChannelName || root.IsPrivate {
		t.Fatalf("unexpected root channel: %+v", root)
	}

	// Idempotent within the same store instance.
	again, err := s.EnsureRoot(testProject)
	if err != nil {
		t.Fatalf("EnsureRoot (again): %v", err)
	}
	if again.ID != root.ID {
		t.Fatalf("root recreated: %q != %q", again.ID, root.ID)
	}

	// Survives a restart: a second store over the same dir sees the same root.
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore (reload): %v", err)
	}
	reloaded, err := s2.EnsureRoot(testProject)
	if err != nil {
		t.Fatalf("EnsureRoot (reload): %v", err)
	}
	if reloaded.ID != root.ID || !reloaded.IsRoot {
		t.Fatalf("root not persisted: %+v", reloaded)
	}

	chans, err := s2.Channels(testProject)
	if err != nil {
		t.Fatalf("Channels: %v", err)
	}
	if len(chans) != 1 {
		t.Fatalf("want exactly one channel after reload, got %d", len(chans))
	}
}

func TestLoadMeta_FutureVersionRefused(t *testing.T) {
	s := newTestStore(t)
	dir := s.projectDir(testProject)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	future, _ := json.Marshal(map[string]any{"version": metaVersion + 1, "projectId": testProject})
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), future, 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.EnsureRoot(testProject); !errors.Is(err, ErrFutureVersion) {
		t.Fatalf("want ErrFutureVersion, got %v", err)
	}
	// The future-versioned file must be left untouched.
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil || string(data) != string(future) {
		t.Fatalf("future meta.json modified (err=%v)", err)
	}
}

func TestLoadMeta_CorruptFileBackedUp(t *testing.T) {
	s := newTestStore(t)
	dir := s.projectDir(testProject)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	root, err := s.EnsureRoot(testProject)
	if err != nil {
		t.Fatalf("EnsureRoot over corrupt meta: %v", err)
	}
	if !root.IsRoot {
		t.Fatalf("no root after corrupt recovery: %+v", root)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta.json.bak")); err != nil {
		t.Fatalf("corrupt meta.json not backed up: %v", err)
	}
}

func TestSanitizeID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "_unset"},
		{"urn:adsk.wipprod:fs.folder:co.x/1", "urn_adsk.wipprod_fs.folder_co.x_1"},
		{"simple-Id_0.9", "simple-Id_0.9"},
		{strings.Repeat("a", 200), strings.Repeat("a", 120)},
	}
	for _, c := range cases {
		if got := sanitizeID(c.in); got != c.want {
			t.Errorf("sanitizeID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEnsureRoot_ConcurrentSingleRoot(t *testing.T) {
	s := newTestStore(t)
	const n = 16
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() {
			c, err := s.EnsureRoot(testProject)
			if err != nil {
				ids <- "err:" + err.Error()
				return
			}
			ids <- c.ID
		}()
	}
	first := <-ids
	for i := 1; i < n; i++ {
		if got := <-ids; got != first {
			t.Fatalf("concurrent EnsureRoot diverged: %q vs %q", got, first)
		}
	}
	chans, err := s.Channels(testProject)
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 1 {
		t.Fatalf("want 1 channel, got %d", len(chans))
	}
}

// TestDeleteProject covers the chat-specific delete concerns: the project's
// open message-log append handle must close before the directory goes (no fd
// leak, and Windows could not remove the dir otherwise), other projects
// survive, and the next access rebuilds a fresh project from nothing.
func TestDeleteProject(t *testing.T) {
	dir := t.TempDir()
	s := newStoreAt(t, dir)

	root, err := s.EnsureRoot(testProject)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	// Posting opens the channel's O_APPEND handle — the state DeleteProject
	// must tear down.
	if _, _, err := s.CreateMessage(testProject, root.ID, "u1", "Alice", "c-1", "so long", 0); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	const otherProject = "urn:project:other"
	if _, err := s.EnsureRoot(otherProject); err != nil {
		t.Fatalf("EnsureRoot other: %v", err)
	}
	dirA := filepath.Join(dir, sanitizeID(testProject))
	if _, err := os.Stat(filepath.Join(dirA, "msg-"+root.ID+".jsonl")); err != nil {
		t.Fatalf("message log missing before delete: %v", err)
	}

	if err := s.DeleteProject(testProject); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := os.Stat(dirA); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("project dir still present after delete: %v", err)
	}
	s.mu.Lock()
	_, cached := s.projects[testProject]
	s.mu.Unlock()
	if cached {
		t.Error("project still in the in-memory map after delete")
	}
	if _, err := os.Stat(filepath.Join(dir, sanitizeID(otherProject), "meta.json")); err != nil {
		t.Errorf("other project's meta was collateral damage: %v", err)
	}

	// Next access recreates from scratch: a new root channel, seq restarting
	// at 1 — proof no stale channelState (or its dead handle) survived.
	root2, err := s.EnsureRoot(testProject)
	if err != nil {
		t.Fatalf("EnsureRoot after delete: %v", err)
	}
	if root2.ID != "c1" {
		t.Errorf("recreated root id = %s, want fresh c1", root2.ID)
	}
	m, created, err := s.CreateMessage(testProject, root2.ID, "u1", "Alice", "c-2", "hello again", 0)
	if err != nil || !created {
		t.Fatalf("CreateMessage after delete: %v (created=%v)", err, created)
	}
	if m.Seq != 1 {
		t.Errorf("post-delete first message seq = %d, want 1", m.Seq)
	}

	// Idempotent: delete again, then with the dir already gone. Close (via
	// the newStoreAt cleanup) must not double-close the deleted project's
	// handle — DeleteProject nils it out.
	if err := s.DeleteProject(testProject); err != nil {
		t.Fatalf("second DeleteProject: %v", err)
	}
	if err := s.DeleteProject(testProject); err != nil {
		t.Errorf("DeleteProject on missing dir = %v, want nil", err)
	}
}
