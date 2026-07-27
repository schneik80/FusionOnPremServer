package notifications

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
	"github.com/schneik80/fusionlocalserver/internal/migrate"
	"github.com/schneik80/fusionlocalserver/internal/schemameta"
)

// registry is the notifications migration table: Target tracks fileVersion.
// There are no steps yet — fileVersion is the floor — but the hook is where
// every future upgrade lands, exactly like tasks.
var registry = newRegistry()

func newRegistry() *migrate.Registry {
	return migrate.NewRegistry("notifications", fileVersion)
}

// Store owns all notification persistence. One Store per hub; all mutation of
// a user's inbox happens under that user's mutex, so the single process is
// the only writer. Every mutation rewrites <userkey>.json before returning,
// so disk always matches memory. Unlike tasks, the key is the user, and the
// files sit directly under dir (no per-project subdirectories) because there
// is nothing project-shaped to nest.
type Store struct {
	dir string // root directory, e.g. hubs/<slug>/notifications

	mu    sync.Mutex // guards users map
	users map[string]*userState
}

// userState is the in-memory copy of one user's inbox. mu serializes every
// read and write for that user.
type userState struct {
	mu   sync.Mutex
	file *userFile
}

// NewStore returns a Store rooted at dir, creating it if needed.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("notifications: creating store dir: %w", err)
	}
	return &Store{dir: dir, users: make(map[string]*userState)}, nil
}

// Reset drops all in-memory state so the next access reloads from disk.
// Required after a backup restore replaces the files under a still-running
// process (tasks.Store.Reset precedent).
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = make(map[string]*userState)
}

// ---- reads ----

// List returns a copy of a user's notifications, newest first, capped to
// limit (0 = all). Never nil.
func (s *Store) List(userKey string, limit int) ([]Notification, error) {
	us, err := s.user(userKey)
	if err != nil {
		return nil, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	n := len(us.file.Notifications)
	out := make([]Notification, 0, n)
	// Stored oldest→newest; emit newest→oldest for the inbox.
	for i := n - 1; i >= 0; i-- {
		out = append(out, copyNotification(us.file.Notifications[i]))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// UnreadCount returns how many of a user's notifications are unread.
func (s *Store) UnreadCount(userKey string) (int, error) {
	us, err := s.user(userKey)
	if err != nil {
		return 0, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	count := 0
	for _, n := range us.file.Notifications {
		if !n.Read {
			count++
		}
	}
	return count, nil
}

// ---- mutations ----

// Add appends a notification to a user's inbox and returns it. When DedupeKey
// is set and an existing entry (read or unread) already carries it, Add is a
// no-op that returns the existing entry with created=false — this is what
// makes the temporal reconciler (overdue/due-soon) safe to run on every
// inbox fetch, and stops a double-emit from a retried write. A full inbox
// prunes its oldest entries down to MaxPerUser.
func (s *Store) Add(userKey string, n Notification) (Notification, bool, error) {
	if strings.TrimSpace(userKey) == "" {
		return Notification{}, false, fmt.Errorf("%w: empty user key", ErrInvalid)
	}
	if !validKind(n.Kind) {
		return Notification{}, false, fmt.Errorf("%w: unknown kind %q", ErrInvalid, n.Kind)
	}
	us, err := s.user(userKey)
	if err != nil {
		return Notification{}, false, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	if n.DedupeKey != "" {
		for _, ex := range us.file.Notifications {
			if ex.DedupeKey == n.DedupeKey {
				return copyNotification(ex), false, nil
			}
		}
	}
	prev := cloneFile(us.file)
	created := &Notification{
		ID:          fmt.Sprintf("n%d", us.file.NextID),
		Num:         us.file.NextID,
		Kind:        n.Kind,
		HubID:       n.HubID,
		ProjectID:   n.ProjectID,
		ProjectName: n.ProjectName,
		Actor:       copyRef(n.Actor),
		Subject:     truncateRunes(n.Subject, MaxSubjectRunes),
		Ref:         truncateBytes(n.Ref, MaxRefLen),
		ChannelID:   n.ChannelID,
		ChannelName: truncateRunes(n.ChannelName, MaxSubjectRunes),
		MessageSeq:  n.MessageSeq,
		DedupeKey:   n.DedupeKey,
		Read:        false,
		CreatedAt:   time.Now().UTC(),
	}
	us.file.NextID++
	us.file.Notifications = append(us.file.Notifications, created)
	pruneOldest(us.file, MaxPerUser)
	if err := s.saveFile(userKey, us.file); err != nil {
		us.file = prev
		return Notification{}, false, err
	}
	return copyNotification(created), true, nil
}

// MarkRead flips the given ids to read and returns the user's remaining
// unread count. Unknown ids are ignored (idempotent); the count reflects the
// post-write state.
func (s *Store) MarkRead(userKey string, ids []string) (int, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	us, err := s.user(userKey)
	if err != nil {
		return 0, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	return s.setRead(userKey, us, func(n *Notification) bool { return want[n.ID] })
}

// MarkAllRead flips every unread notification to read and returns 0 (the new
// unread count) on success.
func (s *Store) MarkAllRead(userKey string) (int, error) {
	us, err := s.user(userKey)
	if err != nil {
		return 0, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	return s.setRead(userKey, us, func(*Notification) bool { return true })
}

// setRead marks every notification select() picks as read, persists, and
// returns the remaining unread count. Caller holds us.mu. A save with no
// actual change is skipped so a redundant mark-read does no disk IO.
func (s *Store) setRead(userKey string, us *userState, sel func(*Notification) bool) (int, error) {
	now := time.Now().UTC()
	changed := false
	for _, n := range us.file.Notifications {
		if !n.Read && sel(n) {
			n.Read = true
			t := now
			n.ReadAt = &t
			changed = true
		}
	}
	if changed {
		prev := cloneFile(us.file)
		if err := s.saveFile(userKey, us.file); err != nil {
			us.file = prev
			return 0, err
		}
	}
	unread := 0
	for _, n := range us.file.Notifications {
		if !n.Read {
			unread++
		}
	}
	return unread, nil
}

// Delete removes one notification (a dismiss). A missing id is not an error
// (idempotent). Returns the remaining unread count.
func (s *Store) Delete(userKey, id string) (int, error) {
	us, err := s.user(userKey)
	if err != nil {
		return 0, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	idx := -1
	for i, n := range us.file.Notifications {
		if n.ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		prev := cloneFile(us.file)
		us.file.Notifications = append(us.file.Notifications[:idx], us.file.Notifications[idx+1:]...)
		if err := s.saveFile(userKey, us.file); err != nil {
			us.file = prev
			return 0, err
		}
	}
	unread := 0
	for _, n := range us.file.Notifications {
		if !n.Read {
			unread++
		}
	}
	return unread, nil
}

// HasDedupe reports whether the user already has a notification with the
// given dedupe key. The temporal reconciler uses it to avoid building a full
// Notification for an entry Add would only reject.
func (s *Store) HasDedupe(userKey, dedupeKey string) (bool, error) {
	if dedupeKey == "" {
		return false, nil
	}
	us, err := s.user(userKey)
	if err != nil {
		return false, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	for _, n := range us.file.Notifications {
		if n.DedupeKey == dedupeKey {
			return true, nil
		}
	}
	return false, nil
}

// ---- helpers ----

// pruneOldest trims the inbox to at most max entries, dropping from the front
// (oldest). Called under the user lock after an append.
func pruneOldest(uf *userFile, max int) {
	if max <= 0 || len(uf.Notifications) <= max {
		return
	}
	drop := len(uf.Notifications) - max
	uf.Notifications = append(uf.Notifications[:0], uf.Notifications[drop:]...)
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}

func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func copyRef(r *UserRef) *UserRef {
	if r == nil {
		return nil
	}
	c := *r
	return &c
}

func copyNotification(n *Notification) Notification {
	out := *n
	out.Actor = copyRef(n.Actor)
	if n.ReadAt != nil {
		t := *n.ReadAt
		out.ReadAt = &t
	}
	return out
}

// cloneFile deep-copies a user file so a failed save can roll memory back.
// Inbox size is capped at MaxPerUser, so the copy is cheap relative to the
// rewrite it accompanies (tasks.cloneFile precedent).
func cloneFile(uf *userFile) *userFile {
	out := *uf
	out.Notifications = make([]*Notification, len(uf.Notifications))
	for i, n := range uf.Notifications {
		c := copyNotification(n)
		out.Notifications[i] = &c
	}
	return &out
}

// ---- persistence ----

// user returns the cached state for a user, loading <userkey>.json on first
// touch.
func (s *Store) user(userKey string) (*userState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if us, ok := s.users[userKey]; ok {
		return us, nil
	}
	uf, err := s.loadFile(userKey)
	if err != nil {
		return nil, err
	}
	us := &userState{file: uf}
	s.users[userKey] = us
	return us, nil
}

func (s *Store) filePath(userKey string) string {
	return filepath.Join(s.dir, sanitizeID(userKey)+".json")
}

// loadFile reads a user's inbox. Absent → fresh empty file. Newer version →
// ErrFutureVersion (never rewrite what we don't understand). Corrupt → rename
// to .bak and start clean rather than wedge the user's whole bell (tasks
// loadFile precedent).
func (s *Store) loadFile(userKey string) (*userFile, error) {
	path := s.filePath(userKey)
	fresh := &userFile{
		Version:       fileVersion,
		Schema:        schemameta.New(),
		UserKey:       userKey,
		NextID:        1,
		Notifications: []*Notification{},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fresh, nil
	}
	if err != nil {
		return nil, fmt.Errorf("notifications: reading %s: %w", path, err)
	}
	data, _, err = registry.Apply(path, data)
	if err != nil {
		if errors.Is(err, migrate.ErrFutureVersion) {
			return nil, fmt.Errorf("%w: %s", ErrFutureVersion, err)
		}
		_ = os.Rename(path, path+".bak")
		return fresh, nil
	}
	var uf userFile
	if err := json.Unmarshal(data, &uf); err != nil {
		_ = os.Rename(path, path+".bak")
		return fresh, nil
	}
	if uf.Notifications == nil {
		uf.Notifications = []*Notification{}
	}
	if uf.NextID < 1 {
		uf.NextID = 1
	}
	if uf.UserKey == "" {
		uf.UserKey = userKey
	}
	if uf.Schema.CreatedAt.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			uf.Schema = schemameta.Backfill(info.ModTime())
		} else {
			uf.Schema = schemameta.New()
		}
	}
	return &uf, nil
}

// saveFile atomically rewrites <userkey>.json (temp file + rename, 0600).
func (s *Store) saveFile(userKey string, uf *userFile) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("notifications: creating store dir: %w", err)
	}
	uf.Schema.Touch()
	data, err := json.MarshalIndent(uf, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(s.filePath(userKey), data, 0600)
}

// sanitizeID maps an OIDC sub / email key to a filesystem-safe slug: any
// character outside [A-Za-z0-9_.\-] becomes '_', capped at 120 chars — copied
// verbatim from tasks.sanitizeID so both stores age identically on disk.
func sanitizeID(id string) string {
	if id == "" {
		return "_unset"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}
