// Package notifications persists a per-user notification inbox locally, the
// same store posture as tasks (versioned envelope, schema provenance stamp,
// atomic rewrites, a per-key mutex, .bak-on-corruption, a future-version
// guard, and a migration registry). It differs from tasks in one axis: the
// key is the USER, not the project. The bell in the app chrome is global
// across every project the user can see, so one <userkey>.json holds a
// user's whole cross-project inbox and the "unread count" is a cheap read of
// that single small file.
//
// Notifications are FIRST-PARTY: the Autodesk/Fusion notifications feed is
// app-gated (see docs/activity-reports/STATUS.md), so every entry here is
// something the server itself observed — a mention, an assignment, a due
// date crossing, a production change — never an APS-sourced event.
//
// Text is composed CLIENT-SIDE from Kind + captured params so it localizes
// into the six catalogs; user-entered data (task titles, channel names) is
// carried verbatim and never translated. The server stores structure, not
// prose.
package notifications

import (
	"errors"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/schemameta"
)

// fileVersion 1 is the initial schema (envelope + schema stamp from day one,
// so there is no v0 backfill path the way tasks needed one).
const fileVersion = 1

// CurrentVersion exposes the schema version this build writes, for the backup
// verify/restore wiring.
func CurrentVersion() int { return fileVersion }

const (
	// MaxPerUser caps a user's inbox. Notifications are transient and the
	// newest matter most, so a full inbox prunes its oldest entries rather
	// than refusing new ones (a flood must never wedge the bell).
	MaxPerUser = 500

	// MaxSubjectRunes bounds the captured subject (task title / channel name)
	// so a pathological title can't bloat the file; the emitter truncates.
	MaxSubjectRunes = 200

	// MaxRefLen bounds a captured fls: deep-link token.
	MaxRefLen = 2048
)

// Kinds. These are stable enum tokens rendered through i18n on the client
// (src/i18n/enums.ts → notifKind); never English prose.
const (
	KindMention    = "mention"    // @you in a chat message
	KindAssigned   = "assigned"   // a task was assigned to you
	KindDueSoon    = "due_soon"   // a task assigned to you is due within the window
	KindOverdue    = "overdue"    // a task assigned to you is past its due date
	KindProduction = "production" // a production job/batch you own changed
	// KindChatUnread is DERIVED at inbox-read time from chat's read cursors,
	// never stored: one row per channel with unread messages. It is not accepted
	// by Add — validKind below deliberately excludes it — because a stored copy
	// would go stale the moment the channel was read.
	KindChatUnread = "chat_unread"
)

func validKind(k string) bool {
	switch k {
	case KindMention, KindAssigned, KindDueSoon, KindOverdue, KindProduction:
		return true
	}
	return false
}

var (
	// ErrFutureVersion is returned when a user's inbox was written by a newer
	// build than this one — the caller must refuse to serve or rewrite it.
	ErrFutureVersion = errors.New("notifications: data written by a newer version")

	// ErrInvalid marks a malformed emit (unknown kind, empty user key). It is
	// an internal guard — the HTTP surface never lets a client author a
	// notification directly.
	ErrInvalid = errors.New("notifications: invalid notification")
)

// UserRef identifies the actor who caused a notification, captured at emit so
// the inbox renders without an APS round-trip (tasks.UserRef / chat parity).
type UserRef struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Notification is one inbox entry. The subject/actor/context fields are
// captured at emit time so the row renders offline; the client composes the
// localized sentence from Kind plus these params. Ref is an fls: token the
// client unfurls for click-through (fls:task?… / fls:job?… / fls:batch?…);
// mentions additionally carry channel context for a chat deep-link.
type Notification struct {
	ID          string     `json:"id"`  // "n<num>", per-user counter
	Num         int64      `json:"num"` // monotonic within the user's inbox
	Kind        string     `json:"kind"`
	HubID       string     `json:"hubId,omitempty"`
	ProjectID   string     `json:"projectId,omitempty"`
	ProjectName string     `json:"projectName,omitempty"` // captured when the emitter has it cheaply
	Actor       *UserRef   `json:"actor,omitempty"`       // who caused it (nil for system/temporal kinds)
	Subject     string     `json:"subject,omitempty"`     // task title / channel name — user data, never translated
	Ref         string     `json:"ref,omitempty"`         // fls: deep-link token for click-through
	ChannelID   string     `json:"channelId,omitempty"`   // mention context
	ChannelName string     `json:"channelName,omitempty"` // mention context
	MessageSeq  int64      `json:"messageSeq,omitempty"`  // mention context
	DedupeKey   string     `json:"dedupeKey,omitempty"`   // at-most-once guard (Add is idempotent on it)
	Read        bool       `json:"read"`
	CreatedAt   time.Time  `json:"createdAt"`
	ReadAt      *time.Time `json:"readAt,omitempty"`
}

// userFile mirrors <userkey>.json. UserKey self-describes the file the way
// tasks' projectFile carries ProjectID — the slugged filename is not
// reversible to the raw OIDC sub, and the snapshot/backup path needs the key
// back.
type userFile struct {
	Version       int              `json:"version"`
	Schema        schemameta.Stamp `json:"schema"`
	UserKey       string           `json:"userKey"`
	NextID        int64            `json:"nextId"`
	Notifications []*Notification  `json:"notifications"`
}
