// Package production persists per-project "production" data locally, the same
// posture as tasks and chat: projects and users are APS-side (referenced by
// URN / OIDC sub), the server stores only its own data under the config dir.
//
// The domain is a light MES / product tracker. A project holds Jobs; a Job is
// a graph (DAG) of Steps; each Step attaches version-pinned plan documents and
// declares placeholders for documents supplied per run. A Batch is a dated run
// of a Job: on creation it freezes a deep copy of the plan documents' pinned
// versions, then records Fulfillments as documents are supplied (browsed from
// the hub or uploaded). Batches are append-only history — editing or deleting
// plan elements never mutates a batch's frozen snapshot.
//
// Like tasks, a project's whole production set lives in one production.json
// rewritten atomically per mutation. MaxJobsPerProject and the per-job caps
// bound the file at human scale, so a full rewrite (and the cross-project scan
// the self-describing file format enables) stays cheap.
package production

import (
	"errors"
	"slices"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/schemameta"
)

// fileVersion 2 added the schema provenance stamp (v1→v2 backfill).
const fileVersion = 2

// CurrentVersion exposes the production.json schema version this build
// writes, for the backup verify/restore wiring.
func CurrentVersion() int { return fileVersion }

// Validation caps, enforced in the store as well as at the HTTP boundary so no
// caller can bypass them.
const (
	MaxJobsPerProject        = 500
	MaxStepsPerJob           = 200
	MaxEdgesPerJob           = 400
	MaxBatchesPerJob         = 500
	MaxPlanDocsPerStep       = 50
	MaxPlaceholdersPerStep   = 50
	MaxFulfillmentsPerBatch  = 1000
	MaxRefsPerBatch          = 50
	MaxRefLen                = 2048
	MaxNameRunes             = 200
	MaxDescRunes             = 20000
	MaxLabelRunes            = 200
	MaxDocSnapshotFieldRunes = 2048
)

var (
	// ErrFutureVersion is returned when a project's production data was
	// written by a newer build than this one. The caller must refuse to serve
	// that project rather than risk rewriting data it doesn't understand.
	ErrFutureVersion = errors.New("production: data written by a newer version")

	// ErrNotFound is returned for unknown jobs/steps/edges/etc (→ 404).
	ErrNotFound = errors.New("production: not found")

	// ErrInvalid is returned for requests that violate an invariant — empty
	// names, unknown enums, cap overruns, bad edges (→ 400). Wrapped errors
	// carry the specific reason.
	ErrInvalid = errors.New("production: invalid request")
)

// BatchKinds label a run's intent (prove-out vs production). Free-form batch
// names are separate; Kind drives the timeline lane coloring.
var BatchKinds = []string{"prove", "production"}

// BatchStatuses track a run's lifecycle.
var BatchStatuses = []string{"planned", "running", "complete"}

func validBatchKind(k string) bool   { return slices.Contains(BatchKinds, k) }
func validBatchStatus(s string) bool { return slices.Contains(BatchStatuses, s) }

// UserRef identifies a project member the way tasks/chat do: by OIDC sub, with
// name/email captured at write time for display without an APS round-trip.
type UserRef struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// DocSnapshot is a version-pinned reference to a Fusion Team document. Unlike
// the fls:doc token used elsewhere (which stores only a lineage urn and always
// follows the tip), a DocSnapshot freezes the exact version: VersionID +
// VersionNumber + RootComponentVersionID together let the UI render that
// specific version's thumbnail and badge. DMProjectID (altId) lets the doc
// card resolve without a second lookup. The server resolves these fields (see
// api.SnapshotDocVersion); the client never invents version urns.
type DocSnapshot struct {
	HubID                  string `json:"hubId"`                            // GraphQL hub id
	ItemID                 string `json:"itemId"`                           // item lineage urn
	Name                   string `json:"name"`                             // display name at pin time
	Kind                   string `json:"kind,omitempty"`                   // design|drawing|unknown (Item kind hint)
	VersionID              string `json:"versionId"`                        // DM version urn (…vf.…?version=N)
	VersionNumber          int    `json:"versionNumber"`                    // human version number
	RootComponentVersionID string `json:"rootComponentVersionId,omitempty"` // per-version thumbnail cvId
	DMProjectID            string `json:"dmProjectId,omitempty"`            // project altId
}

// PlanDoc is a version-pinned document attached to a Step as part of the plan.
type PlanDoc struct {
	ID      string      `json:"id"` // "pd<n>", per-job counter
	Doc     DocSnapshot `json:"doc"`
	AddedBy UserRef     `json:"addedBy"`
	AddedAt time.Time   `json:"addedAt"`
}

// Placeholder is a slot on a Step for a document that must be supplied per
// batch (e.g. "Setup 1 NC program"). Kind is an expected-kind hint; Required
// drives completeness indicators.
type Placeholder struct {
	ID       string `json:"id"` // "ph<n>", per-job counter
	Label    string `json:"label"`
	Kind     string `json:"kind,omitempty"`
	Required bool   `json:"required"`
}

// Step is a node in a Job's flow graph. X/Y are canvas positions in graph
// space (persisted so the layout survives reloads).
type Step struct {
	ID           string        `json:"id"` // "s<n>", per-job counter
	Num          int64         `json:"num"`
	Title        string        `json:"title"`
	Description  string        `json:"description,omitempty"`
	X            float64       `json:"x"`
	Y            float64       `json:"y"`
	PlanDocs     []PlanDoc     `json:"planDocs"`
	Placeholders []Placeholder `json:"placeholders"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
}

// Edge is a directed link between two Steps. The DAG is stored as a flat edge
// list on the Job (not child arrays on Step) so validation is trivial and the
// list maps 1:1 to the canvas's drawn paths.
type Edge struct {
	ID   string `json:"id"` // "e<n>", per-job counter
	From string `json:"from"`
	To   string `json:"to"`
}

// BatchStep is a frozen copy of one Step as it stood when the batch was
// created: identity (id/num/title), its version-pinned plan documents, and its
// placeholder slots. The batch record renders and validates against these —
// never the live plan — so later plan edits (deleting a step, adding a
// placeholder for the next run) cannot rewrite what an existing run recorded.
type BatchStep struct {
	StepID       string        `json:"stepId"`
	Num          int64         `json:"num"`
	Title        string        `json:"title"`
	PlanDocs     []PlanDoc     `json:"planDocs"`     // frozen copies (IDs match the plan's PlanDoc IDs)
	Placeholders []Placeholder `json:"placeholders"` // frozen copies
}

// Fulfillment is one supplied document in a batch: either filling a
// placeholder (PlaceholderID set) or an extra as-run artifact (IsAsRun, e.g.
// on-machine-modified NC code). The document is version-pinned like a PlanDoc.
type Fulfillment struct {
	ID            string      `json:"id"` // "f<n>", per-job counter
	StepID        string      `json:"stepId"`
	PlaceholderID string      `json:"placeholderId,omitempty"` // "" = extra as-run artifact
	Doc           DocSnapshot `json:"doc"`
	Source        string      `json:"source,omitempty"` // "hub" | "upload"
	IsAsRun       bool        `json:"isAsRun"`
	SuppliedBy    UserRef     `json:"suppliedBy"`
	SuppliedAt    time.Time   `json:"suppliedAt"`
}

// Batch is a dated run of a Job. Steps is the frozen plan at creation time;
// Fulfillments accrue as documents are supplied against those frozen steps.
type Batch struct {
	ID           string        `json:"id"` // "b<num>"
	Num          int64         `json:"num"`
	Name         string        `json:"name"`
	Kind         string        `json:"kind"` // prove | production
	RunAt        time.Time     `json:"runAt"`
	Status       string        `json:"status"` // planned | running | complete
	Steps        []BatchStep   `json:"steps"`
	Fulfillments []Fulfillment `json:"fulfillments"`
	// Refs are fls:task / fls:doc tokens attached to the run — related tasks
	// and wiki/hub documents, rendered as cards. Unlike Fulfillments these
	// are not version-pinned; they're live references.
	Refs      []string  `json:"refs"`
	CreatedBy UserRef   `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Job is one production process: a graph of Steps plus its Batches. The
// per-job counters keep every child id stable and human-diffable in the JSON
// (matching the tasks "t<num>" style).
type Job struct {
	ID           string    `json:"id"` // "j<num>", displayed "J-<num>"
	Num          int64     `json:"num"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	Steps        []*Step   `json:"steps"`
	Edges        []Edge    `json:"edges"`
	Batches      []*Batch  `json:"batches"`
	NextStepNum  int64     `json:"nextStepNum"`
	NextBatchNum int64     `json:"nextBatchNum"`
	NextChildNum int64     `json:"nextChildNum"` // shared counter for edges/plandocs/placeholders/fulfillments
	CreatedBy    UserRef   `json:"createdBy"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ProjectJob is a job annotated with its project, for the cross-project
// listing where the caller has no project context (Store.Mine →
// GET /api/production/mine). Parity with tasks' ProjectTask.
type ProjectJob struct {
	*Job
	ProjectID   string `json:"projectId"`
	HubID       string `json:"hubId"`
	ProjectName string `json:"projectName"`
}

// ---- draft / patch payloads ----

// JobDraft is the create-job payload.
type JobDraft struct {
	Name        string
	Description string
}

// JobPatch updates a job's name/description (nil = leave unchanged).
type JobPatch struct {
	Name        *string
	Description *string
}

// StepDraft is the create-step payload.
type StepDraft struct {
	Title       string
	Description string
	X           float64
	Y           float64
}

// StepPatch updates a step (nil = leave unchanged). X/Y are set together via
// Position for frequent drag saves.
type StepPatch struct {
	Title       *string
	Description *string
	Position    *Position
}

// Position is an explicit x/y pair so a drag save can update coordinates
// without touching the rest of the step.
type Position struct {
	X float64
	Y float64
}

// PlaceholderDraft / PlaceholderPatch mutate placeholder slots.
type PlaceholderDraft struct {
	Label    string
	Kind     string
	Required bool
}

type PlaceholderPatch struct {
	Label    *string
	Kind     *string
	Required *bool
}

// BatchDraft is the create-batch payload. RunAt defaults to now when zero.
type BatchDraft struct {
	Name  string
	Kind  string
	RunAt time.Time
}

// BatchPatch updates a batch (nil = leave unchanged).
type BatchPatch struct {
	Name   *string
	Kind   *string
	Status *string
	RunAt  *time.Time
}

// FulfillmentDraft supplies a document into a batch.
type FulfillmentDraft struct {
	StepID        string
	PlaceholderID string
	Doc           DocSnapshot
	Source        string
	IsAsRun       bool
}

// projectFile mirrors production.json. It self-describes hubId and projectName
// (captured from create requests, refreshed on writes) because the directory
// slug is not reversible to a URN — same reason tasks' projectFile does.
type projectFile struct {
	Version     int              `json:"version"`
	Schema      schemameta.Stamp `json:"schema"`
	ProjectID   string           `json:"projectId"`
	HubID       string           `json:"hubId"`
	ProjectName string           `json:"projectName"`
	NextJobNum  int64            `json:"nextJobNum"`
	Jobs        []*Job           `json:"jobs"`
}
