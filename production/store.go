package production

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
	"github.com/schneik80/fusionlocalserver/internal/migrate"

	"github.com/schneik80/fusionlocalserver/internal/schemameta"
)

// registry is the production migration table; steps register as the
// schema evolves past fileVersion 1.
var registry = newRegistry()

func newRegistry() *migrate.Registry {
	r := migrate.NewRegistry("production", fileVersion)
	// v1→v2: schema stamp joins the envelope; loader backfills it.
	r.Register(1, func(raw map[string]any) (map[string]any, error) { return raw, nil })
	return r
}

// Store owns all production persistence. One Store per server; all mutation of
// a project's data happens under that project's mutex, so the single process
// is the only writer (multi-process servers sharing a config dir are a
// documented non-goal). Every mutation rewrites production.json before
// returning, so disk always matches memory.
type Store struct {
	dir string // root directory, e.g. ~/.config/fusionlocalserver/production

	mu       sync.Mutex // guards projects map
	projects map[string]*projectState
}

// projectState is the in-memory copy of one project's production.json. mu
// serializes every read and write for the project.
type projectState struct {
	mu   sync.Mutex
	file *projectFile
}

// NewStore returns a Store rooted at dir, creating it if needed.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("production: creating store dir: %w", err)
	}
	return &Store{dir: dir, projects: make(map[string]*projectState)}, nil
}

// Reset drops all in-memory project state so the next access reloads from
// disk. Required after a backup restore replaces the files under a
// still-running process (the listener rebind does not recreate the store).
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects = make(map[string]*projectState)
}

// DeleteProject permanently removes one project's production data — jobs,
// steps, batches, the lot: the in-memory state is evicted and the project
// directory deleted. A missing directory is not an error (idempotent); the
// next access lazily recreates fresh state. Lock order is s.mu → ps.mu (the
// chat closeHandlesLocked order; no code path acquires s.mu while holding a
// project mutex), and holding the project mutex through the removal means no
// in-flight mutation can rewrite production.json mid-delete.
func (s *Store) DeleteProject(projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps, ok := s.projects[projectID]; ok {
		delete(s.projects, projectID)
		ps.mu.Lock()
		defer ps.mu.Unlock()
	}
	if err := os.RemoveAll(s.projectDir(projectID)); err != nil {
		return fmt.Errorf("production: deleting project data: %w", err)
	}
	return nil
}

// ---- reads ----

// ListJobs returns copies of a project's jobs, never nil, newest first.
func (s *Store) ListJobs(projectID string) ([]Job, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return nil, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := make([]Job, 0, len(ps.file.Jobs))
	for _, j := range ps.file.Jobs {
		out = append(out, *copyJob(j))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Num > out[j].Num })
	return out, nil
}

// GetJob returns one job (steps + edges + batches) by id.
func (s *Store) GetJob(projectID, jobID string) (Job, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Job{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	j := findJob(ps.file, jobID)
	if j == nil {
		return Job{}, fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	return *copyJob(j), nil
}

// GetBatch returns one batch within a job by id.
func (s *Store) GetBatch(projectID, jobID, batchID string) (Batch, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Batch{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	j := findJob(ps.file, jobID)
	if j == nil {
		return Batch{}, fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	b := findBatch(j, batchID)
	if b == nil {
		return Batch{}, fmt.Errorf("%w: batch %q", ErrNotFound, batchID)
	}
	return *copyBatch(b), nil
}

// Mine scans every project directory and returns the jobs this user is involved
// in — ones they created, or that carry a batch they created — annotated with
// their project. The production analogue of tasks.Mine, and the reason the
// project file self-describes hubId/projectName: the directory slug is not
// reversible to a URN, so a cross-project listing must be navigable without any
// APS call. Reads go straight to disk (mutations persist before returning and
// rewrites are atomic renames, so a concurrent read sees the old or the new
// file, never a torn one); unreadable, corrupt, or future-versioned files are
// skipped rather than failing the whole listing.
//
// Same policy as tasks: no per-project roster check (N projects would mean N
// APS calls). Work you created is always visible to you. The residual is that a
// user removed from a project keeps seeing their old jobs until those are
// deleted; every mutation still goes through per-project write authz.
func (s *Store) Mine(userID, email string) ([]ProjectJob, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ProjectJob{}, nil
		}
		return nil, fmt.Errorf("production: scanning store dir: %w", err)
	}
	out := []ProjectJob{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "production.json"))
		if err != nil {
			continue
		}
		var pf projectFile
		if err := json.Unmarshal(data, &pf); err != nil || pf.Version > fileVersion {
			continue
		}
		for _, j := range pf.Jobs {
			if j == nil || !jobInvolvesUser(j, userID, email) {
				continue
			}
			out = append(out, ProjectJob{
				Job:         copyJob(j),
				ProjectID:   pf.ProjectID,
				HubID:       pf.HubID,
				ProjectName: pf.ProjectName,
			})
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].ProjectName != out[k].ProjectName {
			return out[i].ProjectName < out[k].ProjectName
		}
		return out[i].Num > out[k].Num // newest job first within a project
	})
	return out, nil
}

// jobInvolvesUser reports whether the user created the job or any of its runs.
func jobInvolvesUser(j *Job, userID, email string) bool {
	if matchesRef(j.CreatedBy, userID, email) {
		return true
	}
	for _, b := range j.Batches {
		if b != nil && matchesRef(b.CreatedBy, userID, email) {
			return true
		}
	}
	return false
}

// matchesRef matches by OIDC sub first, falling back to a case-insensitive
// email for sessions predating the sub claim — the same rule as tasks/chat.
func matchesRef(ref UserRef, userID, email string) bool {
	if ref.ID != "" && ref.ID == userID {
		return true
	}
	return ref.Email != "" && email != "" && strings.EqualFold(ref.Email, email)
}

// ProjectInfo returns the hub id and name stored for a project (so handlers
// can resolve a job's hub without trusting the client).
func (s *Store) ProjectInfo(projectID string) (hubID, projectName string, err error) {
	ps, err := s.project(projectID)
	if err != nil {
		return "", "", err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.file.HubID, ps.file.ProjectName, nil
}

// ---- mutation plumbing ----

// mutate runs fn under the project lock with a whole-file clone/rollback guard:
// any error from fn or from the save restores the pre-mutation state. fn must
// both validate and apply; validating before touching pf keeps a failed
// mutation side-effect free even before the rollback.
//
// Reserved for mutations that restructure the file itself — creating or
// deleting a job — which are human-paced and rare. Everything job-internal uses
// mutateJob, whose rollback snapshot is scoped to the one job it touches.
func (s *Store) mutate(projectID string, fn func(pf *projectFile) error) error {
	ps, err := s.project(projectID)
	if err != nil {
		return err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	prev := cloneFile(ps.file)
	if err := fn(ps.file); err != nil {
		ps.file = prev
		return err
	}
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file = prev
		return err
	}
	return nil
}

// ---- job mutations ----

// CreateJob validates the draft and appends a new job. hubID and projectName
// self-describe the file and refresh on every create so renames converge.
func (s *Store) CreateJob(projectID, hubID, projectName string, d JobDraft, createdBy UserRef) (Job, error) {
	d.Name = strings.TrimSpace(d.Name)
	if err := validateName(d.Name); err != nil {
		return Job{}, err
	}
	if err := validateDesc(d.Description); err != nil {
		return Job{}, err
	}
	var created *Job
	err := s.mutate(projectID, func(pf *projectFile) error {
		if len(pf.Jobs) >= MaxJobsPerProject {
			return fmt.Errorf("%w: project already has %d jobs", ErrInvalid, MaxJobsPerProject)
		}
		now := time.Now().UTC()
		j := &Job{
			ID:           fmt.Sprintf("j%d", pf.NextJobNum),
			Num:          pf.NextJobNum,
			Name:         d.Name,
			Description:  d.Description,
			Steps:        []*Step{},
			Edges:        []Edge{},
			Batches:      []*Batch{},
			NextStepNum:  1,
			NextBatchNum: 1,
			NextChildNum: 1,
			CreatedBy:    createdBy,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		pf.NextJobNum++
		pf.HubID = hubID
		pf.ProjectName = projectName
		pf.Jobs = append(pf.Jobs, j)
		created = copyJob(j) // copy under lock; see jobMutation
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return *created, nil
}

// UpdateJob patches a job's name/description.
func (s *Store) UpdateJob(projectID, jobID string, p JobPatch) (Job, error) {
	if p.Name != nil {
		*p.Name = strings.TrimSpace(*p.Name)
		if err := validateName(*p.Name); err != nil {
			return Job{}, err
		}
	}
	if p.Description != nil {
		if err := validateDesc(*p.Description); err != nil {
			return Job{}, err
		}
	}
	// Job-scoped (the name/description save on every blur), so it takes the
	// cheap rollback path rather than cloning the whole project file.
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		if p.Name != nil {
			j.Name = *p.Name
		}
		if p.Description != nil {
			j.Description = *p.Description
		}
		return nil
	})
}

// DeleteJob removes a job and everything under it.
func (s *Store) DeleteJob(projectID, jobID string) error {
	return s.mutate(projectID, func(pf *projectFile) error {
		idx := -1
		for i, j := range pf.Jobs {
			if j.ID == jobID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: job %q", ErrNotFound, jobID)
		}
		pf.Jobs = append(pf.Jobs[:idx], pf.Jobs[idx+1:]...)
		return nil
	})
}

// ---- step mutations ----

// CreateStep adds a step to a job's flow.
func (s *Store) CreateStep(projectID, jobID string, d StepDraft) (Job, error) {
	d.Title = strings.TrimSpace(d.Title)
	if err := validateName(d.Title); err != nil {
		return Job{}, err
	}
	if err := validateDesc(d.Description); err != nil {
		return Job{}, err
	}
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		if len(j.Steps) >= MaxStepsPerJob {
			return fmt.Errorf("%w: job already has %d steps", ErrInvalid, MaxStepsPerJob)
		}
		now := time.Now().UTC()
		st := &Step{
			ID:           fmt.Sprintf("s%d", j.NextStepNum),
			Num:          j.NextStepNum,
			Title:        d.Title,
			Description:  d.Description,
			X:            d.X,
			Y:            d.Y,
			PlanDocs:     []PlanDoc{},
			Placeholders: []Placeholder{},
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		j.NextStepNum++
		j.Steps = append(j.Steps, st)
		return nil
	})
}

// UpdateStep patches a step's title/description and/or canvas position.
func (s *Store) UpdateStep(projectID, jobID, stepID string, p StepPatch) (Job, error) {
	if p.Title != nil {
		*p.Title = strings.TrimSpace(*p.Title)
		if err := validateName(*p.Title); err != nil {
			return Job{}, err
		}
	}
	if p.Description != nil {
		if err := validateDesc(*p.Description); err != nil {
			return Job{}, err
		}
	}
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		st := findStep(j, stepID)
		if st == nil {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		if p.Title != nil {
			st.Title = *p.Title
		}
		if p.Description != nil {
			st.Description = *p.Description
		}
		if p.Position != nil {
			st.X = p.Position.X
			st.Y = p.Position.Y
		}
		st.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// DeleteStep removes a step from the plan and drops its incident edges.
// Existing batch snapshots keep their frozen copies (append-only history).
func (s *Store) DeleteStep(projectID, jobID, stepID string) (Job, error) {
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		idx := -1
		for i, st := range j.Steps {
			if st.ID == stepID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		j.Steps = append(j.Steps[:idx], j.Steps[idx+1:]...)
		kept := j.Edges[:0]
		for _, e := range j.Edges {
			if e.From != stepID && e.To != stepID {
				kept = append(kept, e)
			}
		}
		j.Edges = kept
		return nil
	})
}

// ---- edge mutations ----

// AddEdge links two steps. Rejects self-loops, duplicates, unknown endpoints,
// and any edge that would introduce a cycle (the graph stays a DAG).
func (s *Store) AddEdge(projectID, jobID, from, to string) (Job, error) {
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		if from == to {
			return fmt.Errorf("%w: an edge cannot loop a step to itself", ErrInvalid)
		}
		if findStep(j, from) == nil || findStep(j, to) == nil {
			return fmt.Errorf("%w: both steps must exist", ErrInvalid)
		}
		for _, e := range j.Edges {
			if e.From == from && e.To == to {
				return fmt.Errorf("%w: that edge already exists", ErrInvalid)
			}
		}
		if len(j.Edges) >= MaxEdgesPerJob {
			return fmt.Errorf("%w: job already has %d edges", ErrInvalid, MaxEdgesPerJob)
		}
		// Adding from→to creates a cycle iff `to` can already reach `from`.
		if reaches(j, to, from) {
			return fmt.Errorf("%w: that edge would create a cycle", ErrInvalid)
		}
		j.Edges = append(j.Edges, Edge{ID: fmt.Sprintf("e%d", j.NextChildNum), From: from, To: to})
		j.NextChildNum++
		return nil
	})
}

// DeleteEdge removes an edge by id.
func (s *Store) DeleteEdge(projectID, jobID, edgeID string) (Job, error) {
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		idx := -1
		for i, e := range j.Edges {
			if e.ID == edgeID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: edge %q", ErrNotFound, edgeID)
		}
		j.Edges = append(j.Edges[:idx], j.Edges[idx+1:]...)
		return nil
	})
}

// ---- plan-doc mutations ----

// AttachPlanDoc pins a version-resolved document to a step. The DocSnapshot
// arrives already resolved from the handler (server-side version lookup).
func (s *Store) AttachPlanDoc(projectID, jobID, stepID string, doc DocSnapshot, addedBy UserRef) (Job, error) {
	if err := validateSnapshot(doc); err != nil {
		return Job{}, err
	}
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		st := findStep(j, stepID)
		if st == nil {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		if len(st.PlanDocs) >= MaxPlanDocsPerStep {
			return fmt.Errorf("%w: step already has %d plan documents", ErrInvalid, MaxPlanDocsPerStep)
		}
		st.PlanDocs = append(st.PlanDocs, PlanDoc{
			ID:      fmt.Sprintf("pd%d", j.NextChildNum),
			Doc:     doc,
			AddedBy: addedBy,
			AddedAt: time.Now().UTC(),
		})
		j.NextChildNum++
		st.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// RemovePlanDoc detaches a plan document from a step.
func (s *Store) RemovePlanDoc(projectID, jobID, stepID, planDocID string) (Job, error) {
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		st := findStep(j, stepID)
		if st == nil {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		idx := -1
		for i, pd := range st.PlanDocs {
			if pd.ID == planDocID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: plan document %q", ErrNotFound, planDocID)
		}
		st.PlanDocs = append(st.PlanDocs[:idx], st.PlanDocs[idx+1:]...)
		st.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// ---- placeholder mutations ----

// AddPlaceholder declares a per-batch document slot on a step.
func (s *Store) AddPlaceholder(projectID, jobID, stepID string, d PlaceholderDraft) (Job, error) {
	d.Label = strings.TrimSpace(d.Label)
	if err := validateLabel(d.Label); err != nil {
		return Job{}, err
	}
	if err := validateShortField("kind", d.Kind); err != nil {
		return Job{}, err
	}
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		st := findStep(j, stepID)
		if st == nil {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		if len(st.Placeholders) >= MaxPlaceholdersPerStep {
			return fmt.Errorf("%w: step already has %d placeholders", ErrInvalid, MaxPlaceholdersPerStep)
		}
		st.Placeholders = append(st.Placeholders, Placeholder{
			ID:       fmt.Sprintf("ph%d", j.NextChildNum),
			Label:    d.Label,
			Kind:     d.Kind,
			Required: d.Required,
		})
		j.NextChildNum++
		st.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// UpdatePlaceholder patches a placeholder slot.
func (s *Store) UpdatePlaceholder(projectID, jobID, stepID, placeholderID string, p PlaceholderPatch) (Job, error) {
	if p.Label != nil {
		*p.Label = strings.TrimSpace(*p.Label)
		if err := validateLabel(*p.Label); err != nil {
			return Job{}, err
		}
	}
	if p.Kind != nil {
		if err := validateShortField("kind", *p.Kind); err != nil {
			return Job{}, err
		}
	}
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		st := findStep(j, stepID)
		if st == nil {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		ph := findPlaceholder(st, placeholderID)
		if ph == nil {
			return fmt.Errorf("%w: placeholder %q", ErrNotFound, placeholderID)
		}
		if p.Label != nil {
			ph.Label = *p.Label
		}
		if p.Kind != nil {
			ph.Kind = *p.Kind
		}
		if p.Required != nil {
			ph.Required = *p.Required
		}
		st.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// RemovePlaceholder removes a placeholder slot.
func (s *Store) RemovePlaceholder(projectID, jobID, stepID, placeholderID string) (Job, error) {
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		st := findStep(j, stepID)
		if st == nil {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		idx := -1
		for i, ph := range st.Placeholders {
			if ph.ID == placeholderID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: placeholder %q", ErrNotFound, placeholderID)
		}
		st.Placeholders = append(st.Placeholders[:idx], st.Placeholders[idx+1:]...)
		st.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// ---- batch mutations ----

// CreateBatch starts a run of a job. This is where the plan freezes: every
// live PlanDoc's DocSnapshot is deep-copied into the batch so later plan edits
// never change what this run recorded.
func (s *Store) CreateBatch(projectID, jobID string, d BatchDraft, createdBy UserRef) (Batch, error) {
	d.Name = strings.TrimSpace(d.Name)
	if err := validateName(d.Name); err != nil {
		return Batch{}, err
	}
	if d.Kind == "" {
		d.Kind = "prove"
	}
	if !validBatchKind(d.Kind) {
		return Batch{}, fmt.Errorf("%w: unknown batch kind %q", ErrInvalid, d.Kind)
	}
	if d.RunAt.IsZero() {
		d.RunAt = time.Now().UTC()
	}
	var created *Batch
	err := s.jobMutationErr(projectID, jobID, func(j *Job) error {
		if len(j.Batches) >= MaxBatchesPerJob {
			return fmt.Errorf("%w: job already has %d batches", ErrInvalid, MaxBatchesPerJob)
		}
		now := time.Now().UTC()
		// Freeze the plan: deep-copy every step's identity, pinned plan
		// documents, and placeholder slots. The batch renders and validates
		// against this copy, never the live graph.
		frozen := []BatchStep{}
		for _, st := range j.Steps {
			bs := BatchStep{
				StepID:       st.ID,
				Num:          st.Num,
				Title:        st.Title,
				PlanDocs:     append([]PlanDoc{}, st.PlanDocs...),
				Placeholders: append([]Placeholder{}, st.Placeholders...),
			}
			frozen = append(frozen, bs)
		}
		b := &Batch{
			ID:           fmt.Sprintf("b%d", j.NextBatchNum),
			Num:          j.NextBatchNum,
			Name:         d.Name,
			Kind:         d.Kind,
			RunAt:        d.RunAt.UTC(),
			Status:       "planned",
			Steps:        frozen,
			Fulfillments: []Fulfillment{},
			Refs:         []string{},
			CreatedBy:    createdBy,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		j.NextBatchNum++
		j.Batches = append(j.Batches, b)
		created = copyBatch(b) // copy under lock; see jobMutation
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	return *created, nil
}

// UpdateBatch patches a batch's name/kind/status/runAt. Frozen snapshots and
// fulfillments are never touched here.
func (s *Store) UpdateBatch(projectID, jobID, batchID string, p BatchPatch) (Batch, error) {
	if p.Name != nil {
		*p.Name = strings.TrimSpace(*p.Name)
		if err := validateName(*p.Name); err != nil {
			return Batch{}, err
		}
	}
	if p.Kind != nil && !validBatchKind(*p.Kind) {
		return Batch{}, fmt.Errorf("%w: unknown batch kind %q", ErrInvalid, *p.Kind)
	}
	if p.Status != nil && !validBatchStatus(*p.Status) {
		return Batch{}, fmt.Errorf("%w: unknown batch status %q", ErrInvalid, *p.Status)
	}
	var updated *Batch
	err := s.jobMutationErr(projectID, jobID, func(j *Job) error {
		b := findBatch(j, batchID)
		if b == nil {
			return fmt.Errorf("%w: batch %q", ErrNotFound, batchID)
		}
		if p.Name != nil {
			b.Name = *p.Name
		}
		if p.Kind != nil {
			b.Kind = *p.Kind
		}
		if p.Status != nil {
			b.Status = *p.Status
		}
		if p.RunAt != nil {
			b.RunAt = p.RunAt.UTC()
		}
		b.UpdatedAt = time.Now().UTC()
		updated = copyBatch(b) // copy under lock; see jobMutation
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	return *updated, nil
}

// DeleteBatch removes a batch outright.
func (s *Store) DeleteBatch(projectID, jobID, batchID string) error {
	_, err := s.jobMutation(projectID, jobID, func(j *Job) error {
		idx := -1
		for i, b := range j.Batches {
			if b.ID == batchID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: batch %q", ErrNotFound, batchID)
		}
		j.Batches = append(j.Batches[:idx], j.Batches[idx+1:]...)
		return nil
	})
	return err
}

// ---- fulfillment mutations ----

// AddFulfillment supplies a version-pinned document into a batch — filling a
// placeholder (PlaceholderID set) or recording an as-run artifact. The step
// and placeholder are validated against the batch's FROZEN plan, not the live
// graph: a run's record stays writable even after the plan step is deleted,
// and slots that didn't exist when the batch froze can't be filled on it.
func (s *Store) AddFulfillment(projectID, jobID, batchID string, d FulfillmentDraft, suppliedBy UserRef) (Batch, error) {
	if err := validateSnapshot(d.Doc); err != nil {
		return Batch{}, err
	}
	if err := validateShortField("source", d.Source); err != nil {
		return Batch{}, err
	}
	if d.StepID == "" {
		return Batch{}, fmt.Errorf("%w: stepId is required", ErrInvalid)
	}
	var updated *Batch
	err := s.jobMutationErr(projectID, jobID, func(j *Job) error {
		b := findBatch(j, batchID)
		if b == nil {
			return fmt.Errorf("%w: batch %q", ErrNotFound, batchID)
		}
		bs := findBatchStep(b, d.StepID)
		if bs == nil {
			return fmt.Errorf("%w: step %q in this batch", ErrNotFound, d.StepID)
		}
		if d.PlaceholderID != "" {
			if !batchStepHasPlaceholder(bs, d.PlaceholderID) {
				return fmt.Errorf("%w: placeholder %q in this batch", ErrNotFound, d.PlaceholderID)
			}
			// One document per slot: replacing means removing the existing
			// fulfillment first, so the record never hides a duplicate.
			for _, f := range b.Fulfillments {
				if f.StepID == d.StepID && f.PlaceholderID == d.PlaceholderID {
					return fmt.Errorf("%w: that placeholder is already fulfilled — remove the existing document first", ErrInvalid)
				}
			}
		}
		if len(b.Fulfillments) >= MaxFulfillmentsPerBatch {
			return fmt.Errorf("%w: batch already has %d supplied documents", ErrInvalid, MaxFulfillmentsPerBatch)
		}
		b.Fulfillments = append(b.Fulfillments, Fulfillment{
			ID:            fmt.Sprintf("f%d", j.NextChildNum),
			StepID:        d.StepID,
			PlaceholderID: d.PlaceholderID,
			Doc:           d.Doc,
			Source:        d.Source,
			IsAsRun:       d.IsAsRun,
			SuppliedBy:    suppliedBy,
			SuppliedAt:    time.Now().UTC(),
		})
		j.NextChildNum++
		b.UpdatedAt = time.Now().UTC()
		updated = copyBatch(b) // copy under lock; see jobMutation
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	return *updated, nil
}

// RemoveFulfillment removes a supplied document from a batch.
func (s *Store) RemoveFulfillment(projectID, jobID, batchID, fulfillmentID string) (Batch, error) {
	var updated *Batch
	err := s.jobMutationErr(projectID, jobID, func(j *Job) error {
		b := findBatch(j, batchID)
		if b == nil {
			return fmt.Errorf("%w: batch %q", ErrNotFound, batchID)
		}
		idx := -1
		for i, f := range b.Fulfillments {
			if f.ID == fulfillmentID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: fulfillment %q", ErrNotFound, fulfillmentID)
		}
		b.Fulfillments = append(b.Fulfillments[:idx], b.Fulfillments[idx+1:]...)
		b.UpdatedAt = time.Now().UTC()
		updated = copyBatch(b) // copy under lock; see jobMutation
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	return *updated, nil
}

// AddBatchRef attaches a related task or document token (fls:task / fls:doc)
// to a batch. Idempotent: an already-present token is a no-op success.
func (s *Store) AddBatchRef(projectID, jobID, batchID, token string) (Batch, error) {
	token = strings.TrimSpace(token)
	if err := validateRefToken(token); err != nil {
		return Batch{}, err
	}
	var updated *Batch
	err := s.jobMutationErr(projectID, jobID, func(j *Job) error {
		b := findBatch(j, batchID)
		if b == nil {
			return fmt.Errorf("%w: batch %q", ErrNotFound, batchID)
		}
		for _, r := range b.Refs {
			if r == token {
				updated = copyBatch(b) // already present — return current state
				return nil
			}
		}
		if len(b.Refs) >= MaxRefsPerBatch {
			return fmt.Errorf("%w: batch already has %d references", ErrInvalid, MaxRefsPerBatch)
		}
		b.Refs = append(b.Refs, token)
		b.UpdatedAt = time.Now().UTC()
		updated = copyBatch(b) // copy under lock; see jobMutation
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	return *updated, nil
}

// RemoveBatchRef detaches a reference token from a batch.
func (s *Store) RemoveBatchRef(projectID, jobID, batchID, token string) (Batch, error) {
	var updated *Batch
	err := s.jobMutationErr(projectID, jobID, func(j *Job) error {
		b := findBatch(j, batchID)
		if b == nil {
			return fmt.Errorf("%w: batch %q", ErrNotFound, batchID)
		}
		idx := -1
		for i, r := range b.Refs {
			if r == token {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: reference not found", ErrNotFound)
		}
		b.Refs = append(b.Refs[:idx], b.Refs[idx+1:]...)
		b.UpdatedAt = time.Now().UTC()
		updated = copyBatch(b) // copy under lock; see jobMutation
		return nil
	})
	if err != nil {
		return Batch{}, err
	}
	return *updated, nil
}

// validateRefToken accepts only the compact card tokens the batch renders —
// tasks and documents — bounded in length.
func validateRefToken(token string) error {
	if token == "" {
		return fmt.Errorf("%w: reference token is empty", ErrInvalid)
	}
	if len(token) > MaxRefLen {
		return fmt.Errorf("%w: reference token too long", ErrInvalid)
	}
	if !strings.HasPrefix(token, "fls:task?") && !strings.HasPrefix(token, "fls:doc?") {
		return fmt.Errorf("%w: references must be fls:task or fls:doc tokens", ErrInvalid)
	}
	return nil
}

// mutateJob runs fn against one job under the project lock, with the
// clone/rollback guard scoped to THAT job. Every step/edge/placeholder/plan-doc/
// batch/fulfillment/ref write goes through here, and those are the hot paths —
// a canvas drag PATCHes on every node release, a description saves on every
// blur. Cloning the whole project file for each of those would be O(project)
// (all jobs x their batches x snapshots x fulfillments) to undo a single-field
// change, so only the touched job is snapshotted; on failure its slot in the
// Jobs slice is restored. fn is expected to validate before it mutates.
//
// It bumps UpdatedAt so any change touches the job's clock, and takes the
// caller's copy inside the lock so nobody observes a concurrently-mutated job.
func (s *Store) mutateJob(projectID, jobID string, fn func(j *Job) error) (*Job, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return nil, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()

	idx := -1
	for i, j := range ps.file.Jobs {
		if j.ID == jobID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	j := ps.file.Jobs[idx]
	prev := copyJob(j) // rollback snapshot: this job only

	if err := fn(j); err != nil {
		ps.file.Jobs[idx] = prev
		return nil, err
	}
	j.UpdatedAt = time.Now().UTC()
	snapshot := copyJob(j)
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file.Jobs[idx] = prev
		return nil, err
	}
	return snapshot, nil
}

// jobMutation runs fn against a job and returns a fresh copy of it.
func (s *Store) jobMutation(projectID, jobID string, fn func(j *Job) error) (Job, error) {
	snapshot, err := s.mutateJob(projectID, jobID, fn)
	if err != nil {
		return Job{}, err
	}
	return *snapshot, nil
}

// jobMutationErr is jobMutation for callers that return something other than
// the job (batches): it runs fn against the job and only reports the error.
func (s *Store) jobMutationErr(projectID, jobID string, fn func(j *Job) error) error {
	_, err := s.mutateJob(projectID, jobID, fn)
	return err
}

// ---- validation ----

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalid)
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return fmt.Errorf("%w: name exceeds %d characters", ErrInvalid, MaxNameRunes)
	}
	return nil
}

func validateLabel(label string) error {
	if label == "" {
		return fmt.Errorf("%w: label is empty", ErrInvalid)
	}
	if utf8.RuneCountInString(label) > MaxLabelRunes {
		return fmt.Errorf("%w: label exceeds %d characters", ErrInvalid, MaxLabelRunes)
	}
	return nil
}

// validateShortField caps optional free-text hints (placeholder Kind,
// fulfillment Source) that have no other validation — nothing persisted may
// be bounded only by the HTTP body cap.
func validateShortField(name, v string) error {
	if utf8.RuneCountInString(v) > MaxLabelRunes {
		return fmt.Errorf("%w: %s exceeds %d characters", ErrInvalid, name, MaxLabelRunes)
	}
	return nil
}

func validateDesc(desc string) error {
	if utf8.RuneCountInString(desc) > MaxDescRunes {
		return fmt.Errorf("%w: description exceeds %d characters", ErrInvalid, MaxDescRunes)
	}
	return nil
}

// validateSnapshot checks a version-pinned document reference. ItemID and
// VersionID are the two fields a snapshot cannot omit (they are the pin).
func validateSnapshot(d DocSnapshot) error {
	if strings.TrimSpace(d.ItemID) == "" {
		return fmt.Errorf("%w: document itemId is required", ErrInvalid)
	}
	if strings.TrimSpace(d.VersionID) == "" {
		return fmt.Errorf("%w: document versionId is required", ErrInvalid)
	}
	for _, f := range []string{d.HubID, d.ItemID, d.Name, d.Kind, d.VersionID, d.RootComponentVersionID, d.DMProjectID} {
		if utf8.RuneCountInString(f) > MaxDocSnapshotFieldRunes {
			return fmt.Errorf("%w: document reference field too long", ErrInvalid)
		}
	}
	return nil
}

// reaches reports whether target is reachable from start by following edges.
func reaches(j *Job, start, target string) bool {
	if start == target {
		return true
	}
	seen := map[string]bool{start: true}
	stack := []string{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range j.Edges {
			if e.From != cur || seen[e.To] {
				continue
			}
			if e.To == target {
				return true
			}
			seen[e.To] = true
			stack = append(stack, e.To)
		}
	}
	return false
}

// ---- lookups ----

func findJob(pf *projectFile, jobID string) *Job {
	for _, j := range pf.Jobs {
		if j.ID == jobID {
			return j
		}
	}
	return nil
}

func findStep(j *Job, stepID string) *Step {
	for _, st := range j.Steps {
		if st.ID == stepID {
			return st
		}
	}
	return nil
}

func findPlaceholder(st *Step, placeholderID string) *Placeholder {
	for i := range st.Placeholders {
		if st.Placeholders[i].ID == placeholderID {
			return &st.Placeholders[i]
		}
	}
	return nil
}

func findBatch(j *Job, batchID string) *Batch {
	for _, b := range j.Batches {
		if b.ID == batchID {
			return b
		}
	}
	return nil
}

func findBatchStep(b *Batch, stepID string) *BatchStep {
	for i := range b.Steps {
		if b.Steps[i].StepID == stepID {
			return &b.Steps[i]
		}
	}
	return nil
}

func batchStepHasPlaceholder(bs *BatchStep, placeholderID string) bool {
	for _, ph := range bs.Placeholders {
		if ph.ID == placeholderID {
			return true
		}
	}
	return false
}

// ---- deep copies (all return non-nil slices so DTOs marshal []) ----

func copyJob(j *Job) *Job {
	out := *j
	out.Steps = make([]*Step, len(j.Steps))
	for i, st := range j.Steps {
		out.Steps[i] = copyStep(st)
	}
	out.Edges = append([]Edge(nil), j.Edges...)
	if out.Edges == nil {
		out.Edges = []Edge{}
	}
	out.Batches = make([]*Batch, len(j.Batches))
	for i, b := range j.Batches {
		out.Batches[i] = copyBatch(b)
	}
	return &out
}

func copyStep(st *Step) *Step {
	out := *st
	out.PlanDocs = append([]PlanDoc(nil), st.PlanDocs...)
	if out.PlanDocs == nil {
		out.PlanDocs = []PlanDoc{}
	}
	out.Placeholders = append([]Placeholder(nil), st.Placeholders...)
	if out.Placeholders == nil {
		out.Placeholders = []Placeholder{}
	}
	return &out
}

func copyBatch(b *Batch) *Batch {
	out := *b
	out.Steps = make([]BatchStep, len(b.Steps))
	for i, bs := range b.Steps {
		c := bs
		c.PlanDocs = append([]PlanDoc{}, bs.PlanDocs...)
		c.Placeholders = append([]Placeholder{}, bs.Placeholders...)
		out.Steps[i] = c
	}
	out.Fulfillments = append([]Fulfillment(nil), b.Fulfillments...)
	if out.Fulfillments == nil {
		out.Fulfillments = []Fulfillment{}
	}
	out.Refs = append([]string(nil), b.Refs...)
	if out.Refs == nil {
		out.Refs = []string{}
	}
	return &out
}

// cloneFile deep-copies a project file so a failed save can roll the in-memory
// state back. Counts are capped at human scale, so the copy is cheap relative
// to the rewrite it accompanies.
func cloneFile(pf *projectFile) *projectFile {
	out := *pf
	out.Jobs = make([]*Job, len(pf.Jobs))
	for i, j := range pf.Jobs {
		out.Jobs[i] = copyJob(j)
	}
	return &out
}

// ---- persistence ----

// project returns the cached state for a project, loading production.json on
// first touch.
func (s *Store) project(projectID string) (*projectState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps, ok := s.projects[projectID]; ok {
		return ps, nil
	}
	pf, err := s.loadFile(projectID)
	if err != nil {
		return nil, err
	}
	ps := &projectState{file: pf}
	s.projects[projectID] = ps
	return ps, nil
}

func (s *Store) projectDir(projectID string) string {
	return filepath.Join(s.dir, sanitizeID(projectID))
}

func (s *Store) filePath(projectID string) string {
	return filepath.Join(s.projectDir(projectID), "production.json")
}

// loadFile reads a project's production.json. Absent → fresh empty file.
// Newer version → ErrFutureVersion (never rewrite what we don't understand).
// Corrupt → rename to .bak and start clean rather than block the whole
// project (tasks.loadFile / chat.loadMeta precedent).
func (s *Store) loadFile(projectID string) (*projectFile, error) {
	path := s.filePath(projectID)
	fresh := &projectFile{
		Version:    fileVersion,
		Schema:     schemameta.New(),
		ProjectID:  projectID,
		NextJobNum: 1,
		Jobs:       []*Job{},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fresh, nil
	}
	if err != nil {
		return nil, fmt.Errorf("production: reading %s: %w", path, err)
	}
	// Migrate older files forward before decoding (registry has no steps
	// while fileVersion is the floor); future versions refuse. Counter
	// repair below still runs AFTER migration — repairs are for value
	// drift, migrations are for shape changes.
	data, _, err = registry.Apply(path, data)
	if err != nil {
		if errors.Is(err, migrate.ErrFutureVersion) {
			return nil, fmt.Errorf("%w: %s", ErrFutureVersion, err)
		}
		_ = os.Rename(path, path+".bak")
		return fresh, nil
	}
	var pf projectFile
	if err := json.Unmarshal(data, &pf); err != nil {
		_ = os.Rename(path, path+".bak")
		return fresh, nil
	}
	if pf.Jobs == nil {
		pf.Jobs = []*Job{}
	}
	if pf.NextJobNum < 1 {
		pf.NextJobNum = 1
	}
	// Repair any child counters that predate a field or were zeroed.
	for _, j := range pf.Jobs {
		if j.NextStepNum < 1 {
			j.NextStepNum = 1
		}
		if j.NextBatchNum < 1 {
			j.NextBatchNum = 1
		}
		if j.NextChildNum < 1 {
			j.NextChildNum = 1
		}
	}
	// v1→v2 backfill: birthdate approximated by file mtime.
	if pf.Schema.CreatedAt.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			pf.Schema = schemameta.Backfill(info.ModTime())
		} else {
			pf.Schema = schemameta.New()
		}
	}
	return &pf, nil
}

// saveFile atomically rewrites production.json (temp file + rename, 0600), so
// a crash mid-write can never leave a half-written file behind.
func (s *Store) saveFile(projectID string, pf *projectFile) error {
	dir := s.projectDir(projectID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("production: creating project dir: %w", err)
	}
	pf.Schema.Touch()
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(s.filePath(projectID), data, 0600)
}

// sanitizeID maps a URN-format identifier to a filesystem-safe slug: any
// character outside [A-Za-z0-9_.\-] becomes '_', capped at 120 chars — copied
// verbatim from tasks.sanitizeID / chat.sanitizeID so all three stores age
// identically on disk.
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
