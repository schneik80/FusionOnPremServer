// Package chat is the file-backed store and authorization layer for
// project-scoped threaded chat (docs/chat/PLAN.md). Projects and users are
// APS-side entities identified by string URNs; chat persists only its own
// data — channels, messages, reactions, read cursors — under
// ~/.config/fusionlocalserver/chat/<projectSlug>/. The design doc's SQL
// schema (docs/chat/centrifuge-chat-design.md §2) maps onto the structs
// here, with its DB constraints enforced in code by Store.
package chat

import "time"

// metaVersion is the current schema version of a project's meta.json. The
// loader refuses files written by a newer build (see loadMeta) — the
// file-store analog of a migration gate.
// metaVersion 2 added the schema provenance stamp (v1→v2 backfill).
const metaVersion = 2

// MetaVersion exposes the current meta.json schema version to the backup
// verify/restore wiring, which must refuse files newer than this build.
func MetaVersion() int { return metaVersion }

// RootChannelName is the name of the auto-created root channel every
// project gets on first chat access ("general", per design doc §2).
const RootChannelName = "general"

// Channel is a named message stream within a project. The root channel is
// created lazily and can never be private or archived. Members is populated
// only for private channels; public channels fall through to project-level
// (APS) membership, mirroring the design doc's channel_members table.
type Channel struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Topic      string          `json:"topic"`
	IsRoot     bool            `json:"isRoot"`
	IsPrivate  bool            `json:"isPrivate"`
	CreatedBy  string          `json:"createdBy"` // author id; empty for the auto-created root
	CreatedAt  time.Time       `json:"createdAt"`
	ArchivedAt *time.Time      `json:"archivedAt,omitempty"`
	Members    []ChannelMember `json:"members,omitempty"`
}

// ChannelMember is one row of a private channel's ACL (design doc §2
// channel_members). Role is "member" or "owner".
type ChannelMember struct {
	UserID  string    `json:"userId"`
	Role    string    `json:"role"`
	AddedBy string    `json:"addedBy"`
	AddedAt time.Time `json:"addedAt"`
}

// Message is a fully-replayed message: the create record with edits,
// deletion, reactions, and thread counters folded in. ThreadRoot is 0 for
// top-level messages, else the seq of the (top-level) root it replies to.
// ReplyCount and LastReplyAt are derived during JSONL replay, never stored.
type Message struct {
	Seq         int64      `json:"seq"`
	ThreadRoot  int64      `json:"threadRoot,omitempty"`
	AuthorID    string     `json:"authorId"`
	AuthorName  string     `json:"authorName"`
	ClientMsgID string     `json:"clientMsgId"`
	Body        string     `json:"body"`
	CreatedAt   time.Time  `json:"createdAt"`
	EditedAt    *time.Time `json:"editedAt,omitempty"`
	DeletedAt   *time.Time `json:"deletedAt,omitempty"`
	ReplyCount  int        `json:"replyCount"`
	LastReplyAt *time.Time `json:"lastReplyAt,omitempty"`
	Reactions   []Reaction `json:"reactions"`
}

// Reaction is one user's emoji reaction to a message.
type Reaction struct {
	UserID string    `json:"userId"`
	Emoji  string    `json:"emoji"`
	At     time.Time `json:"at"`
}
