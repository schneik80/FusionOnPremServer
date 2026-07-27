// Package whiteboards persists per-project tldraw whiteboards locally, the same
// posture as tasks/production: projects and users are APS-side (referenced by
// URN / OIDC sub), the server stores only its own data under the config dir.
//
// Storage differs from its siblings in one way that matters. A whiteboard's
// tldraw document is orders of magnitude larger than a task or a job — megabytes
// of shapes — and it is rewritten on every autosave. Keeping them all in one
// file per project (the tasks pattern) would mean serialising every board on
// each stroke, and shipping every board's document just to list their names. So
// the small, listable metadata lives in whiteboards.json while each document is
// its own <boardId>.json, written independently.
package whiteboards

import (
	"errors"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/schemameta"
)

// fileVersion 2 added the schema provenance stamp (v1→v2 backfill).
const fileVersion = 2

// CurrentVersion exposes the whiteboards.json schema version this build
// writes, for the backup verify/restore wiring.
func CurrentVersion() int { return fileVersion }

// Validation caps, enforced in the store as well as at the HTTP boundary so no
// caller can bypass them.
const (
	MaxBoardsPerProject = 200
	MaxNameRunes        = 200
	// MaxSnapshotBytes bounds one board's serialised tldraw document. Generous
	// (a dense board with images runs into the megabytes) but finite: the file
	// is rewritten on every autosave and read back whole.
	MaxSnapshotBytes = 24 << 20 // 24 MiB
)

var (
	// ErrFutureVersion is returned when a project's whiteboard data was written
	// by a newer build than this one.
	ErrFutureVersion = errors.New("whiteboards: data written by a newer version")

	// ErrNotFound is returned for unknown boards (→ 404).
	ErrNotFound = errors.New("whiteboards: not found")

	// ErrInvalid is returned for requests that violate an invariant — empty
	// names, cap overruns, oversized documents (→ 400).
	ErrInvalid = errors.New("whiteboards: invalid request")

	// ErrConflict is returned when a save is based on a revision the board has
	// already moved past — someone else saved in between (→ 409). The caller
	// must not retry blindly: a retry is exactly what would discard the other
	// person's work, which is the bug this sentinel exists to prevent.
	ErrConflict = errors.New("whiteboards: document changed since it was loaded")
)

// UserRef identifies a project member the way tasks/chat do: by OIDC sub, with
// name/email captured at write time for display without an APS round-trip.
type UserRef struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Board is one whiteboard's metadata — everything the list needs. The tldraw
// document itself is stored separately (see the package comment) and fetched
// only when a board is opened.
type Board struct {
	ID        string    `json:"id"` // "w<num>"
	Num       int64     `json:"num"`
	Name      string    `json:"name"`
	CreatedBy UserRef   `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// UpdatedBy is whoever last saved the document, so the list can show who
	// touched a board without opening it.
	UpdatedBy UserRef `json:"updatedBy"`
	// SnapshotBytes is the stored document's size, for the list view.
	SnapshotBytes int64 `json:"snapshotBytes"`
	// DocRev counts document saves. A client sends back the revision it loaded
	// and the save is refused if the board has moved on, so two people editing
	// one board can no longer silently overwrite each other.
	//
	// It rides the EXISTING file version rather than bumping it: the field is
	// omitempty and an absent value decodes to 0, which is the correct starting
	// revision, so an older binary can still read the file (a version bump would
	// make it refuse). The same trade the task schedule fields made. If an older
	// build does rewrite the file it drops the field back to 0 — safe, because a
	// client holding a higher revision then gets a clean conflict rather than a
	// silent overwrite.
	DocRev int64 `json:"docRev,omitempty"`
}

// ProjectBoard is a board annotated with its project, for cross-project
// listings (parity with tasks' ProjectTask / production's ProjectJob).
type ProjectBoard struct {
	Board
	ProjectID   string `json:"projectId"`
	HubID       string `json:"hubId"`
	ProjectName string `json:"projectName"`
}

// Draft is the create payload.
type Draft struct {
	Name string
}

// Patch updates a board's metadata; nil = leave unchanged.
type Patch struct {
	Name *string
}

// projectFile mirrors whiteboards.json. It self-describes hubId/projectName for
// the same reason tasks and production do: the directory slug is not reversible
// to a URN, so a cross-project listing must need no APS call.
type projectFile struct {
	Version     int              `json:"version"`
	Schema      schemameta.Stamp `json:"schema"`
	ProjectID   string           `json:"projectId"`
	HubID       string           `json:"hubId"`
	ProjectName string           `json:"projectName"`
	NextBoardID int64            `json:"nextBoardId"`
	Boards      []*Board         `json:"boards"`
}
