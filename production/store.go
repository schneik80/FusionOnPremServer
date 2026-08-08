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
	// v2→v3: decision steps, result-bound edges and per-run hidden steps join
	// the shape. Nothing to rewrite — every added field's zero value is
	// already its legacy meaning ("" kind reads as "step", a nil result list
	// is no results, an empty FromResultID is a plain edge, nothing hidden).
	//
	// The version still has to move. migrate.Apply does not persist what it
	// migrates, so an older binary would decode a v3 file into its own structs,
	// silently drop the new fields, and erase them for every job in the project
	// on its next save. At v3 that binary refuses the file instead.
	r.Register(2, func(raw map[string]any) (map[string]any, error) { return raw, nil })
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

// ListForProjects returns every job in the given projects, annotated with its
// project, for hub-wide aggregation (the dashboard overview). The production
// analogue of tasks.ListForProjects: NO user filter — the caller passes the
// projects the user is authorized to see, and scoping to that set is what
// keeps the hub roll-up from surfacing another project's jobs. An empty id set
// yields no jobs. Reads go straight to disk like Mine; unreadable, corrupt, or
// future-versioned files are skipped rather than failing the whole listing.
func (s *Store) ListForProjects(projectIDs []string) ([]ProjectJob, error) {
	if len(projectIDs) == 0 {
		return []ProjectJob{}, nil
	}
	allow := make(map[string]struct{}, len(projectIDs))
	for _, id := range projectIDs {
		if id != "" {
			allow[id] = struct{}{}
		}
	}
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
		if _, ok := allow[pf.ProjectID]; !ok {
			continue
		}
		for _, j := range pf.Jobs {
			if j == nil {
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
		return out[i].Num > out[k].Num
	})
	return out, nil
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

// DuplicateJob copies a job's whole plan — steps, edges, placeholders and
// version-pinned plan documents — into a new job. Batches are NOT copied: a
// run belongs to the job it ran under, and forging its provenance would also
// double every run-level hit in the Where-Used scan.
//
// Child ids are preserved verbatim. They are job-scoped ("per-job counters",
// see the Job doc comment), so there is no uniqueness reason to renumber, and
// renumbering would mean remapping Edge.From/To as well. The child counters
// come along for the same reason: resetting NextChildNum to 1 would mint a
// second "e1" into a job that already has one, and DeleteEdge matches by id.
//
// Read and write happen in one mutate closure: composing GetJob + CreateJob +
// N x CreateStep would race a concurrent delete of the source and would write
// the file N times for one logical operation.
func (s *Store) DuplicateJob(projectID, hubID, projectName, jobID string, createdBy UserRef) (Job, error) {
	var created *Job
	err := s.mutate(projectID, func(pf *projectFile) error {
		if len(pf.Jobs) >= MaxJobsPerProject {
			return fmt.Errorf("%w: project already has %d jobs", ErrInvalid, MaxJobsPerProject)
		}
		var src *Job
		for _, j := range pf.Jobs {
			if j.ID == jobID {
				src = j
				break
			}
		}
		if src == nil {
			return fmt.Errorf("%w: job %q", ErrNotFound, jobID)
		}
		now := time.Now().UTC()
		dup := copyJob(src)
		dup.ID = fmt.Sprintf("j%d", pf.NextJobNum)
		dup.Num = pf.NextJobNum
		dup.Name = copyName(src.Name)
		dup.Batches = []*Batch{}
		dup.NextBatchNum = 1
		dup.CreatedBy = createdBy
		dup.CreatedAt = now
		dup.UpdatedAt = now
		pf.NextJobNum++
		pf.HubID = hubID
		pf.ProjectName = projectName
		pf.Jobs = append(pf.Jobs, dup)
		created = copyJob(dup) // copy under lock; see jobMutation
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return *created, nil
}

// copySuffix marks a duplicated job. Not translated: the store has no locale,
// and the name is user-editable the moment the copy lands.
const copySuffix = " (copy)"

// copyName appends the copy suffix, trimming the base by RUNES first so a
// name already at MaxNameRunes produces a valid copy instead of failing
// validation — refusing to duplicate a job because its name is long would be
// a rotten trade.
func copyName(name string) string {
	room := MaxNameRunes - utf8.RuneCountInString(copySuffix)
	r := []rune(name)
	if len(r) > room {
		r = r[:room]
	}
	return string(r) + copySuffix
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
	if d.Kind == "" {
		d.Kind = "step"
	}
	if !validStepKind(d.Kind) {
		return Job{}, fmt.Errorf("%w: unknown step kind %q", ErrInvalid, d.Kind)
	}
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		if len(j.Steps) >= MaxStepsPerJob {
			return fmt.Errorf("%w: job already has %d steps", ErrInvalid, MaxStepsPerJob)
		}
		now := time.Now().UTC()
		st := &Step{
			ID:           fmt.Sprintf("s%d", j.NextStepNum),
			Kind:         d.Kind,
			Num:          j.NextStepNum,
			Title:        d.Title,
			Description:  d.Description,
			X:            d.X,
			Y:            d.Y,
			Results:      []DecisionResult{},
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
//
// fromResultID routes the edge out of one of a decision's results. It is
// REQUIRED when `from` is a decision and must be empty otherwise: an unbound
// branch off a decision has no port to leave from and no defined meaning, and
// allowing both shapes on one node would make the duplicate key ambiguous.
// The result must belong to `from` — child ids are unique job-wide, so a
// result id copied from a different decision would otherwise resolve.
func (s *Store) AddEdge(projectID, jobID, from, fromResultID, to string) (Job, error) {
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		if from == to {
			return fmt.Errorf("%w: an edge cannot loop a step to itself", ErrInvalid)
		}
		src := findStep(j, from)
		if src == nil || findStep(j, to) == nil {
			return fmt.Errorf("%w: both steps must exist", ErrInvalid)
		}
		if IsDecision(src.Kind) {
			if fromResultID == "" {
				return fmt.Errorf("%w: an edge from a decision must name the result it branches on", ErrInvalid)
			}
			if findResult(src, fromResultID) == nil {
				return fmt.Errorf("%w: result %q is not on step %q", ErrInvalid, fromResultID, from)
			}
		} else if fromResultID != "" {
			return fmt.Errorf("%w: only a decision step has results to branch on", ErrInvalid)
		}
		// Two results of one decision may legitimately converge on the same
		// step, so the identity of an edge includes the result it leaves from.
		for _, e := range j.Edges {
			if e.From == from && e.FromResultID == fromResultID && e.To == to {
				return fmt.Errorf("%w: that edge already exists", ErrInvalid)
			}
		}
		if len(j.Edges) >= MaxEdgesPerJob {
			return fmt.Errorf("%w: job already has %d edges", ErrInvalid, MaxEdgesPerJob)
		}
		// Adding from→to creates a cycle iff `to` can already reach `from`.
		// The result binding is irrelevant here: every result leaves the same
		// node, so reachability is the same graph either way.
		if reaches(j, to, from) {
			return fmt.Errorf("%w: that edge would create a cycle", ErrInvalid)
		}
		j.Edges = append(j.Edges, Edge{
			ID:           fmt.Sprintf("e%d", j.NextChildNum),
			From:         from,
			FromResultID: fromResultID,
			To:           to,
		})
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
		if IsDecision(st.Kind) {
			return fmt.Errorf("%w: a decision step carries no documents", ErrInvalid)
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

// ---- decision result mutations ----

// AddResult appends an outcome to a decision step. Results are the branch
// points of the graph, so each one becomes an out-port that edges leave from.
func (s *Store) AddResult(projectID, jobID, stepID string, d ResultDraft) (Job, error) {
	d.Label = strings.TrimSpace(d.Label)
	if err := validateLabel(d.Label); err != nil {
		return Job{}, err
	}
	if d.Color == "" {
		d.Color = ResultColors[0]
	}
	if !validResultColor(d.Color) {
		return Job{}, fmt.Errorf("%w: unknown result color %q", ErrInvalid, d.Color)
	}
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		st := findStep(j, stepID)
		if st == nil {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		if !IsDecision(st.Kind) {
			return fmt.Errorf("%w: only a decision step has results", ErrInvalid)
		}
		if len(st.Results) >= MaxResultsPerStep {
			return fmt.Errorf("%w: step already has %d results", ErrInvalid, MaxResultsPerStep)
		}
		st.Results = append(st.Results, DecisionResult{
			ID:    fmt.Sprintf("dr%d", j.NextChildNum),
			Label: d.Label,
			Color: d.Color,
		})
		j.NextChildNum++
		st.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// UpdateResult patches a result's label or color.
func (s *Store) UpdateResult(projectID, jobID, stepID, resultID string, p ResultPatch) (Job, error) {
	if p.Label != nil {
		*p.Label = strings.TrimSpace(*p.Label)
		if err := validateLabel(*p.Label); err != nil {
			return Job{}, err
		}
	}
	if p.Color != nil && !validResultColor(*p.Color) {
		return Job{}, fmt.Errorf("%w: unknown result color %q", ErrInvalid, *p.Color)
	}
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		st := findStep(j, stepID)
		if st == nil {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		r := findResult(st, resultID)
		if r == nil {
			return fmt.Errorf("%w: result %q", ErrNotFound, resultID)
		}
		if p.Label != nil {
			r.Label = *p.Label
		}
		if p.Color != nil {
			r.Color = *p.Color
		}
		st.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// RemoveResult drops an outcome and CASCADES to the edges bound to it, the
// same sweep DeleteStep does for its incident edges.
//
// Leaving them would be unrecoverable through the UI: an edge naming a dead
// result has no port to anchor to, so the canvas cannot draw it — but it still
// counts for cycle detection and the duplicate check, so the user would be
// told a new edge "would create a cycle" by an edge they can neither see nor
// delete.
func (s *Store) RemoveResult(projectID, jobID, stepID, resultID string) (Job, error) {
	return s.jobMutation(projectID, jobID, func(j *Job) error {
		st := findStep(j, stepID)
		if st == nil {
			return fmt.Errorf("%w: step %q", ErrNotFound, stepID)
		}
		idx := -1
		for i, r := range st.Results {
			if r.ID == resultID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: result %q", ErrNotFound, resultID)
		}
		st.Results = append(st.Results[:idx], st.Results[idx+1:]...)
		kept := j.Edges[:0]
		for _, e := range j.Edges {
			if !(e.From == stepID && e.FromResultID == resultID) {
				kept = append(kept, e)
			}
		}
		j.Edges = kept
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
		if IsDecision(st.Kind) {
			// A placeholder on a routing node would put a slot into every
			// batch's completeness count that no run can ever fill.
			return fmt.Errorf("%w: a decision step carries no document slots", ErrInvalid)
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
				Kind:         stepKind(st.Kind),
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
			HiddenSteps:  []string{},
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

// HideBatchStep collapses one frozen step in the run view — typically the
// branch a decision did not take. Idempotent, like AddBatchRef.
//
// This is presentation metadata on the Batch, NOT a field on the frozen
// BatchStep: the snapshot stays a pure record of what the plan was, and
// nothing derived from the run (completeness, fulfillment validation,
// Where-Used) consults the hidden list.
func (s *Store) HideBatchStep(projectID, jobID, batchID, stepID string) (Batch, error) {
	return s.setBatchStepHidden(projectID, jobID, batchID, stepID, true)
}

// ShowBatchStep un-hides a frozen step. Missing is not an error — the caller
// wants it visible, and it already is.
func (s *Store) ShowBatchStep(projectID, jobID, batchID, stepID string) (Batch, error) {
	return s.setBatchStepHidden(projectID, jobID, batchID, stepID, false)
}

func (s *Store) setBatchStepHidden(projectID, jobID, batchID, stepID string, hidden bool) (Batch, error) {
	var updated *Batch
	err := s.jobMutationErr(projectID, jobID, func(j *Job) error {
		b := findBatch(j, batchID)
		if b == nil {
			return fmt.Errorf("%w: batch %q", ErrNotFound, batchID)
		}
		// Validate against the FROZEN steps, not the live plan: a step deleted
		// from the plan is still part of this run and still hideable.
		if findBatchStep(b, stepID) == nil {
			return fmt.Errorf("%w: step %q is not part of this run", ErrNotFound, stepID)
		}
		idx := -1
		for i, id := range b.HiddenSteps {
			if id == stepID {
				idx = i
				break
			}
		}
		switch {
		case hidden && idx < 0:
			b.HiddenSteps = append(b.HiddenSteps, stepID)
		case !hidden && idx >= 0:
			b.HiddenSteps = append(b.HiddenSteps[:idx], b.HiddenSteps[idx+1:]...)
		default:
			updated = copyBatch(b) // already in the wanted state
			return nil
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

func findResult(st *Step, resultID string) *DecisionResult {
	for i := range st.Results {
		if st.Results[i].ID == resultID {
			return &st.Results[i]
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
	out.Steps = make([]*Step, 0, len(j.Steps))
	for _, st := range j.Steps {
		// A hand-edited or badly-restored file can carry "steps": [null];
		// copyStep dereferences, so drop the hole rather than panic the
		// handler. FindDocRefs already guards this on its own read path.
		if st == nil {
			continue
		}
		out.Steps = append(out.Steps, copyStep(st))
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
	// UpdateResult writes through a *DecisionResult into this slice, so an
	// aliased copy would let a failed mutation's rollback snapshot carry the
	// edit it was supposed to undo. DecisionResult is all value types, so the
	// slice copy is the whole deep copy.
	out.Results = append([]DecisionResult(nil), st.Results...)
	if out.Results == nil {
		out.Results = []DecisionResult{}
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
	out.HiddenSteps = append([]string(nil), b.HiddenSteps...)
	if out.HiddenSteps == nil {
		out.HiddenSteps = []string{}
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
