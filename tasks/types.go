// Package tasks persists per-project task lists locally, the same posture
// as chat: projects and users are APS-side (referenced by URN / OIDC sub),
// the server stores only its own data under the config dir. Unlike chat's
// append-only JSONL logs, a project's tasks are a small mutable set, so the
// whole set lives in one tasks.json rewritten atomically per mutation —
// which also makes the cross-project "my tasks" scan a cheap read of one
// small file per project. No pagination: MaxTasksPerProject bounds the file
// at human scale.
package tasks

import (
	"errors"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/schemameta"
)

// fileVersion 2 added the schema provenance stamp (v1→v2 backfills it
// from the file's ModTime at load).
const fileVersion = 2

// CurrentVersion exposes the tasks.json schema version this build writes,
// for the backup verify/restore wiring.
func CurrentVersion() int { return fileVersion }

// Validation caps, enforced here as well as at the HTTP boundary so no
// caller can bypass them.
const (
	MaxTitleRunes      = 200
	MaxDescRunes       = 20000
	MaxDocRefs         = 20
	MaxDocRefLen       = 2048
	MaxTasksPerProject = 5000
	MaxDependsOn       = 20
	MaxStageRunes      = 100
	// MaxShiftDays bounds a schedule shift (±10 years) so a bad client
	// can't fling every bar out of any renderable range.
	MaxShiftDays = 3650
)

var (
	// ErrFutureVersion is returned when a project's task data was written
	// by a newer build than this one. The caller must refuse to serve
	// tasks for that project rather than risk rewriting data it doesn't
	// understand.
	ErrFutureVersion = errors.New("tasks: data written by a newer version")

	// ErrNotFound is returned for unknown tasks (→ 404).
	ErrNotFound = errors.New("tasks: not found")

	// ErrInvalid is returned for requests that violate a task invariant —
	// empty titles, unknown statuses, cap overruns (→ 400). Wrapped errors
	// carry the specific reason.
	ErrInvalid = errors.New("tasks: invalid request")
)

// Statuses double as the Kanban columns, in board order.
var Statuses = []string{"todo", "inprogress", "blocked", "done"}

var Priorities = []string{"low", "medium", "high", "urgent"}

func validStatus(s string) bool {
	for _, v := range Statuses {
		if s == v {
			return true
		}
	}
	return false
}

func validPriority(p string) bool {
	for _, v := range Priorities {
		if p == v {
			return true
		}
	}
	return false
}

// UserRef identifies a project member the way chat does: by OIDC sub, with
// name/email captured at write time for display without an APS round-trip.
type UserRef struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Task is one task. ID is "t<num>" with a per-project counter (displayed as
// "T-<num>"); Rank orders tasks within their status column on the Kanban
// board (floats leave headroom for midpoint inserts).
//
// The schedule fields (StartDate…Stage) are optional additions that ride on
// fileVersion 1: they're omitempty, so old files load with them unset and
// new files stay readable by older builds — with the accepted residual that
// an older binary's next rewrite silently drops them.
type Task struct {
	ID          string    `json:"id"`
	Num         int64     `json:"num"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	Priority    string    `json:"priority"`
	DueDate     string    `json:"dueDate,omitempty"` // YYYY-MM-DD; date-only avoids TZ drift
	Assignee    *UserRef  `json:"assignee,omitempty"`
	CreatedBy   UserRef   `json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	DocRefs     []string  `json:"docRefs"` // fls:doc?… tokens, rendered as document cards
	Rank        float64   `json:"rank"`

	// Schedule (Gantt view). StartDate/EndDate are set together or not at
	// all — a task is "scheduled" iff both are present. DueDate stays an
	// independent deadline. Progress 0 doubles as "unset" by design.
	StartDate string   `json:"startDate,omitempty"` // YYYY-MM-DD
	EndDate   string   `json:"endDate,omitempty"`   // YYYY-MM-DD; >= StartDate
	Progress  int      `json:"progress,omitempty"`  // 0..100
	Milestone bool     `json:"milestone,omitempty"` // scheduled with EndDate == StartDate
	DependsOn []string `json:"dependsOn,omitempty"` // finish-to-start predecessor task IDs (this project)
	Stage     string   `json:"stage,omitempty"`     // Gantt grouping; the stage bar is derived, never stored
}

// ProjectTask is a task annotated with its project, for cross-project
// listings ("my tasks") where the caller has no project context.
type ProjectTask struct {
	Task
	ProjectID   string `json:"projectId"`
	HubID       string `json:"hubId"`
	ProjectName string `json:"projectName"`
}

// Draft is the create payload. Zero-value Status/Priority default to
// "todo"/"medium".
type Draft struct {
	Title       string
	Description string
	Status      string
	Priority    string
	DueDate     string
	Assignee    *UserRef
	DocRefs     []string
	StartDate   string
	EndDate     string
	Progress    int
	Milestone   bool
	DependsOn   []string
	Stage       string
}

// Patch is the update payload: nil pointer = leave unchanged. JSON can't
// cheaply distinguish null from absent, so clearing the optional fields is
// explicit. ClearSchedule clears StartDate, EndDate and Milestone together —
// a one-sided schedule is an invalid state, so per-field clears would only
// manufacture 400s. Progress has no clear flag (0 == unset); DependsOn and
// Stage clear by setting empty (DocRefs/Description precedent).
type Patch struct {
	Title         *string
	Description   *string
	Status        *string
	Priority      *string
	DueDate       *string
	Assignee      *UserRef
	ClearAssignee bool
	ClearDueDate  bool
	DocRefs       *[]string
	Rank          *float64
	StartDate     *string
	EndDate       *string
	ClearSchedule bool
	Progress      *int
	Milestone     *bool
	DependsOn     *[]string
	Stage         *string
}

// projectFile mirrors tasks.json. The file self-describes hubId and
// projectName (captured from create requests, refreshed on writes) because
// the directory slug is not reversible to a URN and "my tasks" results must
// be navigable without APS calls — same reason chat's projectMeta stores
// ProjectID.
type projectFile struct {
	Version     int              `json:"version"`
	Schema      schemameta.Stamp `json:"schema"`
	ProjectID   string           `json:"projectId"`
	HubID       string           `json:"hubId"`
	ProjectName string           `json:"projectName"`
	NextTaskID  int64            `json:"nextTaskId"`
	Tasks       []*Task          `json:"tasks"`
}
