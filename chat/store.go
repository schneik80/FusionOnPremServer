package chat

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

// metaRegistry / cursorsRegistry are chat's migration tables (meta.json
// and cursors.json version independently; JSONL records have their own
// per-record hook in replay).
var (
	metaRegistry    = newMetaRegistry()
	cursorsRegistry = newCursorsRegistry()
)

func newMetaRegistry() *migrate.Registry {
	r := migrate.NewRegistry("chat-meta", metaVersion)
	// v1→v2: schema stamp joins the envelope; loader backfills it.
	r.Register(1, func(raw map[string]any) (map[string]any, error) { return raw, nil })
	return r
}

func newCursorsRegistry() *migrate.Registry {
	r := migrate.NewRegistry("chat-cursors", cursorsVersion)
	r.Register(1, func(raw map[string]any) (map[string]any, error) { return raw, nil })
	return r
}

// Validation caps, enforced here as well as at the HTTP boundary so no
// caller can bypass them.
const (
	MaxBodyRunes  = 4000
	MaxNameRunes  = 80
	MaxTopicRunes = 500
)

var (
	// ErrFutureVersion is returned when a project's chat data was written
	// by a newer build than this one. The caller must refuse to serve chat
	// for that project rather than risk rewriting data it doesn't understand.
	ErrFutureVersion = errors.New("chat: data written by a newer version")

	// ErrNotFound is returned for unknown channels and messages (→ 404).
	ErrNotFound = errors.New("chat: not found")

	// ErrInvalid is returned for requests that violate a chat invariant —
	// bad thread roots, archived channels, name collisions, cap overruns
	// (→ 400). Wrapped errors carry the specific reason.
	ErrInvalid = errors.New("chat: invalid request")
)

// Store owns all chat persistence. One Store per server; all mutation of a
// project's data happens under that project's mutex, so the single process
// is the only writer (multi-process servers sharing a config dir are a
// documented non-goal).
type Store struct {
	dir string // root directory, e.g. ~/.config/fusionlocalserver/chat

	mu       sync.Mutex // guards projects map
	projects map[string]*projectState
}

// projectState is the in-memory index for one project's chat data. mu
// serializes every read and write for the project; channel message state
// and read cursors load lazily on first touch.
type projectState struct {
	mu       sync.Mutex
	meta     *projectMeta
	channels map[string]*channelState
	cursors  *cursorsFile
}

// channelState is a channel's replayed message log plus its open append
// handle. All access is under the owning projectState's mutex.
type channelState struct {
	msgs     []*Message // ascending seq
	bySeq    map[int64]*Message
	byClient map[string]*Message
	nextSeq  int64
	file     *os.File // O_APPEND|O_WRONLY
}

// projectMeta mirrors meta.json: channel definitions plus the counters that
// must survive restarts. Message content lives in per-channel JSONL logs,
// not here, so this file stays small enough for whole-file atomic rewrites.
type projectMeta struct {
	Version       int              `json:"version"`
	Schema        schemameta.Stamp `json:"schema"`
	ProjectID     string           `json:"projectId"`
	EventEpoch    int64            `json:"eventEpoch"` // bumped at startup; prefixes SSE event ids
	NextChannelID int64            `json:"nextChannelId"`
	Channels      []*Channel       `json:"channels"`
}

// NewStore returns a Store rooted at dir, creating it if needed.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("chat: creating store dir: %w", err)
	}
	return &Store{dir: dir, projects: make(map[string]*projectState)}, nil
}

// Close releases every open message-log handle. Call on server shutdown.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeHandlesLocked()
}

// Reset closes every open log handle and drops all in-memory project state,
// so the next access replays fresh from disk. Required after a backup
// restore replaces the files under a still-running process — the listener
// rebind that follows a restore does NOT recreate the store, so a stale
// cache here would rewrite pre-restore data on the next mutation. Requests
// already holding a projectState may still finish against the old state;
// the restart that follows immediately bounds that window.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeHandlesLocked()
	s.projects = make(map[string]*projectState)
}

// closeHandlesLocked closes every channel's append handle. Caller holds s.mu.
func (s *Store) closeHandlesLocked() {
	for _, ps := range s.projects {
		ps.mu.Lock()
		for _, cs := range ps.channels {
			if cs.file != nil {
				_ = cs.file.Close()
				cs.file = nil
			}
		}
		ps.mu.Unlock()
	}
}

// DeleteProject permanently removes one project's chat data — channels,
// message logs, read cursors: the project's open append handles are closed,
// its in-memory state is evicted, and the project directory deleted. A
// missing directory is not an error (idempotent); the next access lazily
// recreates fresh state (EnsureRoot mints a new root channel). Lock order is
// s.mu → ps.mu, the order closeHandlesLocked established; no code path
// acquires s.mu while holding a project mutex, so this cannot deadlock.
// Holding the project mutex through the removal means no in-flight append
// can race the delete.
func (s *Store) DeleteProject(projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps, ok := s.projects[projectID]; ok {
		delete(s.projects, projectID)
		ps.mu.Lock()
		defer ps.mu.Unlock()
		for _, cs := range ps.channels {
			if cs.file != nil {
				_ = cs.file.Close()
				cs.file = nil
			}
		}
	}
	if err := os.RemoveAll(s.projectDir(projectID)); err != nil {
		return fmt.Errorf("chat: deleting project data: %w", err)
	}
	return nil
}

// ---- channels ----

// EnsureRoot loads (or initialises) the project's chat metadata and
// guarantees the root channel exists — the file-store analog of the design
// doc's "root channel created in the same transaction as the project":
// projects are APS-side, so first chat access is the creation hook.
// Idempotent; safe under concurrent calls.
func (s *Store) EnsureRoot(projectID string) (Channel, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Channel{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	c, err := s.ensureRootLocked(projectID, ps)
	if err != nil {
		return Channel{}, err
	}
	return copyChannel(c), nil
}

func (s *Store) ensureRootLocked(projectID string, ps *projectState) (*Channel, error) {
	for _, c := range ps.meta.Channels {
		if c.IsRoot {
			return c, nil
		}
	}
	root := &Channel{
		ID:        fmt.Sprintf("c%d", ps.meta.NextChannelID),
		Name:      RootChannelName,
		IsRoot:    true,
		CreatedAt: time.Now().UTC(),
	}
	ps.meta.NextChannelID++
	ps.meta.Channels = append(ps.meta.Channels, root)
	if err := s.saveMeta(projectID, ps.meta); err != nil {
		// Roll back the in-memory append so a later retry re-creates it.
		ps.meta.Channels = ps.meta.Channels[:len(ps.meta.Channels)-1]
		ps.meta.NextChannelID--
		return nil, err
	}
	return root, nil
}

// Channels returns the project's channels (root guaranteed present), as
// copies safe to use outside the store's locks. Never nil. Visibility
// filtering for private channels is the authorizer's job, not the store's.
func (s *Store) Channels(projectID string) ([]Channel, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return nil, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, err := s.ensureRootLocked(projectID, ps); err != nil {
		return nil, err
	}
	out := make([]Channel, 0, len(ps.meta.Channels))
	for _, c := range ps.meta.Channels {
		out = append(out, copyChannel(c))
	}
	return out, nil
}

// GetChannel returns one channel by id.
func (s *Store) GetChannel(projectID, channelID string) (Channel, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Channel{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	c, err := findChannel(ps, channelID)
	if err != nil {
		return Channel{}, err
	}
	return copyChannel(c), nil
}

// CreateChannel adds a channel. For private channels the creator becomes
// owner and memberIDs join as members. Name is unique per project
// (case-insensitive), mirroring the design doc's UNIQUE(project_id, name).
func (s *Store) CreateChannel(projectID, name, topic, createdBy string, isPrivate bool, memberIDs []string) (Channel, error) {
	name = strings.TrimSpace(name)
	topic = strings.TrimSpace(topic)
	if err := validateName(name); err != nil {
		return Channel{}, err
	}
	if utf8.RuneCountInString(topic) > MaxTopicRunes {
		return Channel{}, fmt.Errorf("%w: topic exceeds %d characters", ErrInvalid, MaxTopicRunes)
	}

	ps, err := s.project(projectID)
	if err != nil {
		return Channel{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, err := s.ensureRootLocked(projectID, ps); err != nil {
		return Channel{}, err
	}
	for _, c := range ps.meta.Channels {
		if strings.EqualFold(c.Name, name) {
			return Channel{}, fmt.Errorf("%w: a channel named %q already exists", ErrInvalid, name)
		}
	}

	ch := &Channel{
		ID:        fmt.Sprintf("c%d", ps.meta.NextChannelID),
		Name:      name,
		Topic:     topic,
		IsPrivate: isPrivate,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}
	if isPrivate {
		ch.Members = []ChannelMember{{UserID: createdBy, Role: "owner", AddedBy: createdBy, AddedAt: ch.CreatedAt}}
		for _, id := range memberIDs {
			if id == "" || id == createdBy || memberIndex(ch.Members, id) >= 0 {
				continue
			}
			ch.Members = append(ch.Members, ChannelMember{UserID: id, Role: "member", AddedBy: createdBy, AddedAt: ch.CreatedAt})
		}
	}
	ps.meta.NextChannelID++
	ps.meta.Channels = append(ps.meta.Channels, ch)
	if err := s.saveMeta(projectID, ps.meta); err != nil {
		ps.meta.Channels = ps.meta.Channels[:len(ps.meta.Channels)-1]
		ps.meta.NextChannelID--
		return Channel{}, err
	}
	return copyChannel(ch), nil
}

// UpdateChannel renames a channel and/or replaces its topic (nil = leave
// unchanged). The root channel's name is fixed; its topic may change.
func (s *Store) UpdateChannel(projectID, channelID string, name, topic *string) (Channel, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Channel{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ch, err := findChannel(ps, channelID)
	if err != nil {
		return Channel{}, err
	}

	oldName, oldTopic := ch.Name, ch.Topic
	if name != nil {
		n := strings.TrimSpace(*name)
		if ch.IsRoot && !strings.EqualFold(n, ch.Name) {
			return Channel{}, fmt.Errorf("%w: the root channel cannot be renamed", ErrInvalid)
		}
		if err := validateName(n); err != nil {
			return Channel{}, err
		}
		for _, c := range ps.meta.Channels {
			if c.ID != ch.ID && strings.EqualFold(c.Name, n) {
				return Channel{}, fmt.Errorf("%w: a channel named %q already exists", ErrInvalid, n)
			}
		}
		ch.Name = n
	}
	if topic != nil {
		t := strings.TrimSpace(*topic)
		if utf8.RuneCountInString(t) > MaxTopicRunes {
			return Channel{}, fmt.Errorf("%w: topic exceeds %d characters", ErrInvalid, MaxTopicRunes)
		}
		ch.Topic = t
	}
	if err := s.saveMeta(projectID, ps.meta); err != nil {
		ch.Name, ch.Topic = oldName, oldTopic
		return Channel{}, err
	}
	return copyChannel(ch), nil
}

// ArchiveChannel soft-closes a channel (no new messages). The root channel
// cannot be archived. Idempotent.
func (s *Store) ArchiveChannel(projectID, channelID string) (Channel, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Channel{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ch, err := findChannel(ps, channelID)
	if err != nil {
		return Channel{}, err
	}
	if ch.IsRoot {
		return Channel{}, fmt.Errorf("%w: the root channel cannot be archived", ErrInvalid)
	}
	if ch.ArchivedAt == nil {
		now := time.Now().UTC()
		ch.ArchivedAt = &now
		if err := s.saveMeta(projectID, ps.meta); err != nil {
			ch.ArchivedAt = nil
			return Channel{}, err
		}
	}
	return copyChannel(ch), nil
}

// AddChannelMember adds a user to a private channel's ACL. Idempotent.
func (s *Store) AddChannelMember(projectID, channelID, userID, addedBy string) (Channel, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Channel{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ch, err := findChannel(ps, channelID)
	if err != nil {
		return Channel{}, err
	}
	if !ch.IsPrivate {
		return Channel{}, fmt.Errorf("%w: %q is a public channel; membership comes from the project", ErrInvalid, ch.Name)
	}
	if memberIndex(ch.Members, userID) >= 0 {
		return copyChannel(ch), nil
	}
	ch.Members = append(ch.Members, ChannelMember{UserID: userID, Role: "member", AddedBy: addedBy, AddedAt: time.Now().UTC()})
	if err := s.saveMeta(projectID, ps.meta); err != nil {
		ch.Members = ch.Members[:len(ch.Members)-1]
		return Channel{}, err
	}
	return copyChannel(ch), nil
}

// RemoveChannelMember drops a user from a private channel's ACL. Idempotent.
func (s *Store) RemoveChannelMember(projectID, channelID, userID string) (Channel, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Channel{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ch, err := findChannel(ps, channelID)
	if err != nil {
		return Channel{}, err
	}
	if !ch.IsPrivate {
		return Channel{}, fmt.Errorf("%w: %q is a public channel; membership comes from the project", ErrInvalid, ch.Name)
	}
	if i := memberIndex(ch.Members, userID); i >= 0 {
		old := ch.Members
		ch.Members = append(append([]ChannelMember{}, old[:i]...), old[i+1:]...)
		if err := s.saveMeta(projectID, ps.meta); err != nil {
			ch.Members = old
			return Channel{}, err
		}
	}
	return copyChannel(ch), nil
}

// ---- messages ----

// CreateMessage appends a message (or thread reply, when threadRoot != 0).
// clientMsgID makes it idempotent: a duplicate returns the existing message
// with created=false. Thread replies are one level deep — the root must be
// an existing, live, top-level message in the same channel.
func (s *Store) CreateMessage(projectID, channelID, authorID, authorName, clientMsgID, body string, threadRoot int64) (Message, bool, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Message{}, false, fmt.Errorf("%w: message body is empty", ErrInvalid)
	}
	if utf8.RuneCountInString(body) > MaxBodyRunes {
		return Message{}, false, fmt.Errorf("%w: message exceeds %d characters", ErrInvalid, MaxBodyRunes)
	}
	if clientMsgID == "" {
		return Message{}, false, fmt.Errorf("%w: clientMsgId is required", ErrInvalid)
	}

	ps, cs, ch, err := s.channelState(projectID, channelID)
	if err != nil {
		return Message{}, false, err
	}
	defer ps.mu.Unlock()
	if ch.ArchivedAt != nil {
		return Message{}, false, fmt.Errorf("%w: channel %q is archived", ErrInvalid, ch.Name)
	}
	if existing, ok := cs.byClient[clientMsgID]; ok {
		return copyMessage(existing), false, nil
	}
	if threadRoot != 0 {
		root := cs.bySeq[threadRoot]
		switch {
		case root == nil:
			return Message{}, false, fmt.Errorf("%w: thread root %d does not exist in this channel", ErrInvalid, threadRoot)
		case root.ThreadRoot != 0:
			return Message{}, false, fmt.Errorf("%w: threads are one level deep; %d is itself a reply", ErrInvalid, threadRoot)
		case root.DeletedAt != nil:
			return Message{}, false, fmt.Errorf("%w: thread root %d was deleted", ErrInvalid, threadRoot)
		}
	}

	rec := record{
		V: recordVersion, Op: opCreate,
		Seq: cs.nextSeq, ThreadRoot: threadRoot,
		AuthorID: authorID, AuthorName: authorName,
		ClientMsgID: clientMsgID, Body: body,
		At: time.Now().UTC(),
	}
	if err := appendRecord(cs.file, rec); err != nil {
		return Message{}, false, err
	}
	cs.nextSeq++
	m := &Message{
		Seq: rec.Seq, ThreadRoot: threadRoot,
		AuthorID: authorID, AuthorName: authorName,
		ClientMsgID: clientMsgID, Body: body,
		CreatedAt: rec.At, Reactions: []Reaction{},
	}
	cs.msgs = append(cs.msgs, m)
	cs.bySeq[m.Seq] = m
	cs.byClient[clientMsgID] = m
	if root := cs.bySeq[threadRoot]; threadRoot != 0 && root != nil {
		root.ReplyCount++
		at := rec.At
		root.LastReplyAt = &at
	}
	return copyMessage(m), true, nil
}

// EditMessage replaces a live message's body. Ownership/moderation checks
// are the handler's job; the store only enforces invariants.
func (s *Store) EditMessage(projectID, channelID string, seq int64, body string) (Message, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Message{}, fmt.Errorf("%w: message body is empty", ErrInvalid)
	}
	if utf8.RuneCountInString(body) > MaxBodyRunes {
		return Message{}, fmt.Errorf("%w: message exceeds %d characters", ErrInvalid, MaxBodyRunes)
	}
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return Message{}, err
	}
	defer ps.mu.Unlock()
	m := cs.bySeq[seq]
	if m == nil {
		return Message{}, fmt.Errorf("%w: message %d", ErrNotFound, seq)
	}
	if m.DeletedAt != nil {
		return Message{}, fmt.Errorf("%w: message %d was deleted", ErrInvalid, seq)
	}
	rec := record{V: recordVersion, Op: opEdit, Target: seq, Body: body, At: time.Now().UTC()}
	if err := appendRecord(cs.file, rec); err != nil {
		return Message{}, err
	}
	m.Body = body
	at := rec.At
	m.EditedAt = &at
	return copyMessage(m), nil
}

// DeleteMessage soft-deletes a message: the tombstone stays in the timeline
// but the body and reactions are gone. Idempotent.
func (s *Store) DeleteMessage(projectID, channelID string, seq int64) (Message, error) {
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return Message{}, err
	}
	defer ps.mu.Unlock()
	m := cs.bySeq[seq]
	if m == nil {
		return Message{}, fmt.Errorf("%w: message %d", ErrNotFound, seq)
	}
	if m.DeletedAt != nil {
		return copyMessage(m), nil
	}
	rec := record{V: recordVersion, Op: opDelete, Target: seq, At: time.Now().UTC()}
	if err := appendRecord(cs.file, rec); err != nil {
		return Message{}, err
	}
	at := rec.At
	m.DeletedAt = &at
	m.Body = ""
	m.Reactions = []Reaction{}
	if root := cs.bySeq[m.ThreadRoot]; m.ThreadRoot != 0 && root != nil && root.ReplyCount > 0 {
		root.ReplyCount--
	}
	return copyMessage(m), nil
}

// GetMessage returns one message (for ownership checks before edit/delete).
func (s *Store) GetMessage(projectID, channelID string, seq int64) (Message, error) {
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return Message{}, err
	}
	defer ps.mu.Unlock()
	m := cs.bySeq[seq]
	if m == nil {
		return Message{}, fmt.Errorf("%w: message %d", ErrNotFound, seq)
	}
	return copyMessage(m), nil
}

// AddReaction records userID's emoji on a live message. Idempotent — the
// duplicate case appends no log record.
func (s *Store) AddReaction(projectID, channelID string, seq int64, userID, emoji string) (Message, error) {
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return Message{}, err
	}
	defer ps.mu.Unlock()
	m := cs.bySeq[seq]
	if m == nil {
		return Message{}, fmt.Errorf("%w: message %d", ErrNotFound, seq)
	}
	if m.DeletedAt != nil {
		return Message{}, fmt.Errorf("%w: message %d was deleted", ErrInvalid, seq)
	}
	if hasReaction(m.Reactions, userID, emoji) {
		return copyMessage(m), nil
	}
	rec := record{V: recordVersion, Op: opReact, Target: seq, UserID: userID, Emoji: emoji, At: time.Now().UTC()}
	if err := appendRecord(cs.file, rec); err != nil {
		return Message{}, err
	}
	m.Reactions = append(m.Reactions, Reaction{UserID: userID, Emoji: emoji, At: rec.At})
	return copyMessage(m), nil
}

// RemoveReaction removes userID's emoji from a message. Idempotent.
func (s *Store) RemoveReaction(projectID, channelID string, seq int64, userID, emoji string) (Message, error) {
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return Message{}, err
	}
	defer ps.mu.Unlock()
	m := cs.bySeq[seq]
	if m == nil {
		return Message{}, fmt.Errorf("%w: message %d", ErrNotFound, seq)
	}
	if !hasReaction(m.Reactions, userID, emoji) {
		return copyMessage(m), nil
	}
	rec := record{V: recordVersion, Op: opUnreact, Target: seq, UserID: userID, Emoji: emoji, At: time.Now().UTC()}
	if err := appendRecord(cs.file, rec); err != nil {
		return Message{}, err
	}
	m.Reactions = removeReaction(m.Reactions, userID, emoji)
	return copyMessage(m), nil
}

// ListMessages returns the channel's top-level timeline, ascending by seq.
// beforeSeq > 0 pages backward (strictly older); limit caps the page
// (default 50, max 200).
func (s *Store) ListMessages(projectID, channelID string, beforeSeq int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return nil, err
	}
	defer ps.mu.Unlock()
	var out []Message
	for i := len(cs.msgs) - 1; i >= 0 && len(out) < limit; i-- {
		m := cs.msgs[i]
		if m.ThreadRoot != 0 {
			continue
		}
		if beforeSeq > 0 && m.Seq >= beforeSeq {
			continue
		}
		out = append(out, copyMessage(m))
	}
	reverse(out)
	if out == nil {
		out = []Message{}
	}
	return out, nil
}

// ListMessagesAfter returns every message (any thread level) with
// seq > afterSeq, ascending — the polling/recovery delta feed.
func (s *Store) ListMessagesAfter(projectID, channelID string, afterSeq int64) ([]Message, error) {
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return nil, err
	}
	defer ps.mu.Unlock()
	out := []Message{}
	for _, m := range cs.msgs {
		if m.Seq > afterSeq {
			out = append(out, copyMessage(m))
		}
	}
	return out, nil
}

// ListThread returns a thread: the root message followed by its replies,
// ascending. The root must be a top-level message in this channel.
func (s *Store) ListThread(projectID, channelID string, rootSeq int64) ([]Message, error) {
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return nil, err
	}
	defer ps.mu.Unlock()
	root := cs.bySeq[rootSeq]
	if root == nil {
		return nil, fmt.Errorf("%w: message %d", ErrNotFound, rootSeq)
	}
	if root.ThreadRoot != 0 {
		return nil, fmt.Errorf("%w: %d is a reply, not a thread root", ErrInvalid, rootSeq)
	}
	out := []Message{copyMessage(root)}
	for _, m := range cs.msgs {
		if m.ThreadRoot == rootSeq {
			out = append(out, copyMessage(m))
		}
	}
	return out, nil
}

// LatestSeq reports the channel's highest assigned message seq (0 when
// empty) — the client's polling cursor.
func (s *Store) LatestSeq(projectID, channelID string) (int64, error) {
	ps, cs, _, err := s.channelState(projectID, channelID)
	if err != nil {
		return 0, err
	}
	defer ps.mu.Unlock()
	return cs.nextSeq - 1, nil
}

// ---- internals ----

// project returns the cached state for projectID, loading meta.json on
// first access. The load bumps and persists EventEpoch, so SSE event ids
// minted by different process runs can never alias: a client's
// Last-Event-ID from before a restart parses as a stale epoch and forces a
// clean resync instead of silently missing events.
func (s *Store) project(projectID string) (*projectState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps, ok := s.projects[projectID]; ok {
		return ps, nil
	}
	meta, err := s.loadMeta(projectID)
	if err != nil {
		return nil, err
	}
	meta.EventEpoch++
	if err := s.saveMeta(projectID, meta); err != nil {
		return nil, err
	}
	ps := &projectState{meta: meta, channels: make(map[string]*channelState)}
	s.projects[projectID] = ps
	return ps, nil
}

// EventEpoch reports the project's event epoch for this process run (see
// project() for why it bumps every run).
func (s *Store) EventEpoch(projectID string) (int64, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return 0, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.meta.EventEpoch, nil
}

// channelState resolves a channel and its replayed message state, returning
// with ps.mu HELD on success (the caller must unlock; on error the lock is
// already released).
func (s *Store) channelState(projectID, channelID string) (*projectState, *channelState, *Channel, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return nil, nil, nil, err
	}
	ps.mu.Lock()
	ch, err := findChannel(ps, channelID)
	if err != nil {
		ps.mu.Unlock()
		return nil, nil, nil, err
	}
	cs, ok := ps.channels[channelID]
	if !ok {
		cs, err = s.loadChannel(projectID, channelID)
		if err != nil {
			ps.mu.Unlock()
			return nil, nil, nil, err
		}
		ps.channels[channelID] = cs
	}
	return ps, cs, ch, nil
}

// loadChannel replays a channel's message log and opens its append handle.
// Called under the owning projectState's mutex.
func (s *Store) loadChannel(projectID, channelID string) (*channelState, error) {
	path := s.logPath(projectID, channelID)
	recs, err := replayLog(path)
	if err != nil {
		return nil, err
	}
	msgs, bySeq, byClient, nextSeq := replayMessages(recs)
	f, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &channelState{msgs: msgs, bySeq: bySeq, byClient: byClient, nextSeq: nextSeq, file: f}, nil
}

// openAppend opens (creating if needed) a log for appending, retrying once
// after a short pause — antivirus and indexer scans on Windows can hold a
// just-created file briefly.
func openAppend(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		return f, nil
	}
	time.Sleep(50 * time.Millisecond)
	f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("chat: opening %s: %w", path, err)
	}
	return f, nil
}

func findChannel(ps *projectState, channelID string) (*Channel, error) {
	for _, c := range ps.meta.Channels {
		if c.ID == channelID {
			return c, nil
		}
	}
	return nil, fmt.Errorf("%w: channel %s", ErrNotFound, channelID)
}

func memberIndex(ms []ChannelMember, userID string) int {
	for i, m := range ms {
		if m.UserID == userID {
			return i
		}
	}
	return -1
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: channel name is empty", ErrInvalid)
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return fmt.Errorf("%w: channel name exceeds %d characters", ErrInvalid, MaxNameRunes)
	}
	return nil
}

func copyChannel(c *Channel) Channel {
	out := *c
	if c.ArchivedAt != nil {
		at := *c.ArchivedAt
		out.ArchivedAt = &at
	}
	out.Members = append([]ChannelMember{}, c.Members...)
	return out
}

func copyMessage(m *Message) Message {
	out := *m
	for _, p := range []struct {
		src *time.Time
		dst **time.Time
	}{{m.EditedAt, &out.EditedAt}, {m.DeletedAt, &out.DeletedAt}, {m.LastReplyAt, &out.LastReplyAt}} {
		if p.src != nil {
			at := *p.src
			*p.dst = &at
		}
	}
	out.Reactions = append([]Reaction{}, m.Reactions...)
	return out
}

func reverse(ms []Message) {
	for i, j := 0, len(ms)-1; i < j; i, j = i+1, j-1 {
		ms[i], ms[j] = ms[j], ms[i]
	}
}

// projectDir maps a project URN to its on-disk directory, sanitized the
// same way pins maps hub IDs to filenames (URNs contain ':' and '/').
func (s *Store) projectDir(projectID string) string {
	return filepath.Join(s.dir, sanitizeID(projectID))
}

func (s *Store) metaPath(projectID string) string {
	return filepath.Join(s.projectDir(projectID), "meta.json")
}

func (s *Store) logPath(projectID, channelID string) string {
	return filepath.Join(s.projectDir(projectID), "msg-"+channelID+".jsonl")
}

// loadMeta reads a project's meta.json. Absent → fresh empty meta. Newer
// version → ErrFutureVersion (never rewrite what we don't understand).
// Corrupt → rename to .bak and start clean rather than block chat for the
// whole project (shared store posture).
func (s *Store) loadMeta(projectID string) (*projectMeta, error) {
	path := s.metaPath(projectID)
	fresh := &projectMeta{
		Version:       metaVersion,
		Schema:        schemameta.New(),
		ProjectID:     projectID,
		EventEpoch:    1,
		NextChannelID: 1,
		Channels:      []*Channel{},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fresh, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat: reading %s: %w", path, err)
	}
	// Migrate older files forward before decoding (no steps registered
	// while metaVersion is the floor); future versions refuse.
	data, _, err = metaRegistry.Apply(path, data)
	if err != nil {
		if errors.Is(err, migrate.ErrFutureVersion) {
			return nil, fmt.Errorf("%w: %s", ErrFutureVersion, err)
		}
		_ = os.Rename(path, path+".bak")
		return fresh, nil
	}
	var meta projectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		_ = os.Rename(path, path+".bak")
		return fresh, nil
	}
	if meta.Channels == nil {
		meta.Channels = []*Channel{}
	}
	if meta.Schema.CreatedAt.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			meta.Schema = schemameta.Backfill(info.ModTime())
		} else {
			meta.Schema = schemameta.New()
		}
	}
	return &meta, nil
}

// saveMeta atomically rewrites meta.json (temp file + rename, 0600), so a
// crash mid-write can never leave a half-written file behind.
func (s *Store) saveMeta(projectID string, meta *projectMeta) error {
	dir := s.projectDir(projectID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("chat: creating project dir: %w", err)
	}
	meta.Schema.Touch()
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(s.metaPath(projectID), data, 0600)
}

// sanitizeID maps a URN-format identifier to a filesystem-safe slug: any
// character outside [A-Za-z0-9_.\-] becomes '_', capped at 120 chars (same
// rules as pins.sanitizeHubID so both stores age identically on disk).
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
