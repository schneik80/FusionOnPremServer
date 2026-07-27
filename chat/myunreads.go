package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ChannelUnread is one channel that has messages a user has not read, annotated
// with its project so a cross-project caller (the notification inbox) can show
// where it is.
type ChannelUnread struct {
	ProjectID   string
	ChannelID   string
	ChannelName string
	IsPrivate   bool
	UnreadCount int
	LatestSeq   int64
	// LatestAt is the newest unread message's timestamp, for ordering the
	// inbox — a channel that went quiet an hour ago should not outrank one
	// someone posted in a minute ago.
	LatestAt time.Time
}

// MyUnreads reports every channel across the hub in which userKey has unread
// messages. It backs the notification bell's chat rows, which are derived at
// read time rather than stored — so reading a channel makes its row disappear
// on its own, with no emission on the write path and no stored row to go stale.
//
// SCOPE. This is a cross-project scan with no APS call, so it cannot ask which
// projects the caller may see. Instead it only ever reports a channel the
// caller demonstrably participates in:
//
//   - one they hold a read cursor for — a cursor exists only because they
//     opened the channel, which required access at the time; or
//   - a private channel whose member list names them.
//
// A public channel in a project they have never opened is therefore invisible
// here, which is also the behaviour you want: the bell should not sprout a row
// for every channel in the hub. Note that "opened" means the cursor actually
// moved — SetReadCursor is monotonic, so a channel someone opened but read
// nothing in has no cursor entry and stays invisible until they read something.
//
// The residual is that access revoked AFTER a channel was read still yields a
// name and a count until the cursor is gone — the same residual every stored
// notification already carries.
func (s *Store) MyUnreads(userKey string) ([]ChannelUnread, error) {
	out := []ChannelUnread{}
	if userKey == "" {
		return out, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("chat: scanning store dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.dir, e.Name())
		meta, ok := readMetaFile(filepath.Join(dir, "meta.json"))
		if !ok || meta.ProjectID == "" {
			continue
		}
		mine := readUserCursors(filepath.Join(dir, "cursors.json"), userKey)

		// Narrow to the caller's own channels BEFORE touching any message log:
		// a channel's unread count means replaying it, and most channels in a
		// hub are none of this caller's business.
		candidates := make([]Channel, 0, len(meta.Channels))
		for _, ch := range meta.Channels {
			if ch == nil || ch.ArchivedAt != nil {
				continue
			}
			_, read := mine[ch.ID]
			if !read && !(ch.IsPrivate && memberIndex(ch.Members, userKey) >= 0) {
				continue
			}
			candidates = append(candidates, copyChannel(ch))
		}
		if len(candidates) == 0 {
			continue
		}
		unreads, err := s.Unreads(meta.ProjectID, userKey, candidates)
		if err != nil {
			continue // one unreadable project must not empty the whole inbox
		}
		byID := make(map[string]Channel, len(candidates))
		for _, ch := range candidates {
			byID[ch.ID] = ch
		}
		for _, u := range unreads {
			if u.UnreadCount == 0 {
				continue
			}
			ch := byID[u.ChannelID]
			out = append(out, ChannelUnread{
				ProjectID:   meta.ProjectID,
				ChannelID:   u.ChannelID,
				ChannelName: ch.Name,
				IsPrivate:   ch.IsPrivate,
				UnreadCount: u.UnreadCount,
				LatestSeq:   u.LatestSeq,
				LatestAt:    s.latestAt(meta.ProjectID, u.ChannelID),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LatestAt.Equal(out[j].LatestAt) {
			return out[i].LatestAt.After(out[j].LatestAt)
		}
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].ChannelName < out[j].ChannelName
	})
	return out, nil
}

// latestAt is the newest live message's timestamp in a channel, or the zero
// time when it cannot be read. Best-effort: it only orders the inbox.
func (s *Store) latestAt(projectID, channelID string) time.Time {
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return time.Time{}
	}
	defer ps.mu.Unlock()
	for i := len(cs.msgs) - 1; i >= 0; i-- {
		if cs.msgs[i].DeletedAt == nil {
			return cs.msgs[i].CreatedAt
		}
	}
	return time.Time{}
}

// readMetaFile reads a project's channel list straight off disk. Deliberately
// not through the project state: that lazily creates a root channel and bumps
// the event epoch, and a scan for the bell must not write to every project it
// looks at.
func readMetaFile(path string) (projectMeta, bool) {
	var meta projectMeta
	data, err := os.ReadFile(path)
	if err != nil {
		return meta, false
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, false
	}
	if meta.Version > metaVersion {
		return meta, false // written by a newer build; leave it alone
	}
	return meta, true
}

// readUserCursors returns one user's channel→seq cursors, or an empty map.
func readUserCursors(path, userKey string) map[string]int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]int64{}
	}
	var cf cursorsFile
	if err := json.Unmarshal(data, &cf); err != nil || cf.Version > cursorsVersion {
		return map[string]int64{}
	}
	if c, ok := cf.Cursors[userKey]; ok {
		return c
	}
	return map[string]int64{}
}
