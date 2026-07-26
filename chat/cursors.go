package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
	"github.com/schneik80/fusionlocalserver/internal/migrate"

	"github.com/schneik80/fusionlocalserver/internal/schemameta"
)

// Per-user, per-channel read cursors (docs/chat/PLAN.md phase 4) — the
// file-store analog of the design doc's channel_read_cursors table. One
// cursors.json per project, rewritten atomically like meta.json; cursor
// moves are monotonic so racing tabs can only advance a user's position,
// never rewind it.

// cursorsVersion gates cursors.json the same way metaVersion gates
// meta.json: newer files are refused, older ones upgrade in place.
// cursorsVersion 2 added the schema provenance stamp.
const cursorsVersion = 2

// CursorsVersion exposes the current cursors.json schema version to the
// backup verify/restore wiring.
func CursorsVersion() int { return cursorsVersion }

// cursorsFile mirrors cursors.json: user key → channel id → last-read seq.
// The user key is the caller's stable identity (OIDC sub, with the
// handler's email fallback), matching the ExtraUserIDs the hub targets
// read.updated events at.
type cursorsFile struct {
	Version int                         `json:"version"`
	Schema  schemameta.Stamp            `json:"schema"`
	Cursors map[string]map[string]int64 `json:"cursors"`
}

// Unread is one channel's unread summary for one user: their cursor, the
// number of live messages past it (thread replies included, tombstones
// not), and the channel's newest seq.
type Unread struct {
	ChannelID   string
	LastReadSeq int64
	UnreadCount int
	LatestSeq   int64
}

// SetReadCursor advances userKey's read cursor in a channel. Moves are
// monotonic: a seq at or behind the stored cursor is a no-op (advanced
// false) so stale tabs can't rewind fresher ones. seq is clamped to the
// channel's newest seq — a cursor can't point past messages that exist.
// The persisted value is returned alongside the channel's unread summary.
func (s *Store) SetReadCursor(projectID, userKey, channelID string, seq int64) (Unread, bool, error) {
	if userKey == "" {
		return Unread{}, false, fmt.Errorf("%w: missing user identity", ErrInvalid)
	}
	if seq < 0 {
		return Unread{}, false, fmt.Errorf("%w: invalid lastReadSeq", ErrInvalid)
	}
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return Unread{}, false, err
	}
	defer ps.mu.Unlock()
	if err := s.ensureCursorsLocked(projectID, ps); err != nil {
		return Unread{}, false, err
	}
	if latest := cs.nextSeq - 1; seq > latest {
		seq = latest
	}
	user := ps.cursors.Cursors[userKey]
	prev := user[channelID]
	advanced := seq > prev
	if advanced {
		if user == nil {
			user = make(map[string]int64)
			ps.cursors.Cursors[userKey] = user
		}
		user[channelID] = seq
		if err := s.saveCursors(projectID, ps.cursors); err != nil {
			user[channelID] = prev
			return Unread{}, false, err
		}
	} else {
		seq = prev
	}
	return unreadLocked(cs, channelID, seq), advanced, nil
}

// Unreads reports userKey's unread summary for each of the given channels
// (the handler passes the ones visible to the caller). Archived channels
// are skipped — they can't gain messages, so they don't count as unread.
func (s *Store) Unreads(projectID, userKey string, channels []Channel) ([]Unread, error) {
	out := []Unread{}
	for _, ch := range channels {
		if ch.ArchivedAt != nil {
			continue
		}
		ps, cs, _, err := s.channelState(projectID, ch.ID)
		if err != nil {
			return nil, err
		}
		if err := s.ensureCursorsLocked(projectID, ps); err != nil {
			ps.mu.Unlock()
			return nil, err
		}
		cursor := ps.cursors.Cursors[userKey][ch.ID]
		out = append(out, unreadLocked(cs, ch.ID, cursor))
		ps.mu.Unlock()
	}
	return out, nil
}

// unreadLocked counts the live messages past the cursor. Called with the
// owning projectState's mutex held.
func unreadLocked(cs *channelState, channelID string, cursor int64) Unread {
	u := Unread{ChannelID: channelID, LastReadSeq: cursor, LatestSeq: cs.nextSeq - 1}
	for i := len(cs.msgs) - 1; i >= 0; i-- {
		m := cs.msgs[i]
		if m.Seq <= cursor {
			break
		}
		if m.DeletedAt == nil {
			u.UnreadCount++
		}
	}
	return u
}

// ensureCursorsLocked lazily loads cursors.json into the project state.
// Called under ps.mu.
func (s *Store) ensureCursorsLocked(projectID string, ps *projectState) error {
	if ps.cursors != nil {
		return nil
	}
	cf, err := s.loadCursors(projectID)
	if err != nil {
		return err
	}
	ps.cursors = cf
	return nil
}

func (s *Store) cursorsPath(projectID string) string {
	return filepath.Join(s.projectDir(projectID), "cursors.json")
}

// loadCursors reads a project's cursors.json with the same corruption and
// version policy as meta.json: absent → fresh, corrupt → .bak + fresh
// (losing read positions is annoying, not data loss), newer → refuse.
func (s *Store) loadCursors(projectID string) (*cursorsFile, error) {
	path := s.cursorsPath(projectID)
	fresh := &cursorsFile{Version: cursorsVersion, Schema: schemameta.New(), Cursors: make(map[string]map[string]int64)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fresh, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat: reading %s: %w", path, err)
	}
	// Same registry hook as meta.json (no steps at v1; future refuses).
	data, _, err = cursorsRegistry.Apply(path, data)
	if err != nil {
		if errors.Is(err, migrate.ErrFutureVersion) {
			return nil, fmt.Errorf("%w: %s", ErrFutureVersion, err)
		}
		_ = os.Rename(path, path+".bak")
		return fresh, nil
	}
	var cf cursorsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		_ = os.Rename(path, path+".bak")
		return fresh, nil
	}
	if cf.Cursors == nil {
		cf.Cursors = make(map[string]map[string]int64)
	}
	if cf.Schema.CreatedAt.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			cf.Schema = schemameta.Backfill(info.ModTime())
		} else {
			cf.Schema = schemameta.New()
		}
	}
	return &cf, nil
}

// saveCursors atomically rewrites cursors.json (same temp+rename dance as
// saveMeta).
func (s *Store) saveCursors(projectID string, cf *cursorsFile) error {
	dir := s.projectDir(projectID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("chat: creating project dir: %w", err)
	}
	cf.Schema.Touch()
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(s.cursorsPath(projectID), data, 0600); err != nil {
		return err
	}
	return nil
}
