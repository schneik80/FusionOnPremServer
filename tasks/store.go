package tasks

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

// registry is the tasks migration table: Target tracks fileVersion; steps
// register here as the schema evolves.
var registry = newRegistry()

func newRegistry() *migrate.Registry {
	r := migrate.NewRegistry("tasks", fileVersion)
	// v1→v2: the schema stamp joins the envelope. Content is untouched —
	// the loader backfills the stamp (CreatedAt from file mtime) after
	// decode, where the file path is in scope.
	r.Register(1, func(raw map[string]any) (map[string]any, error) { return raw, nil })
	return r
}

// Store owns all task persistence. One Store per server; all mutation of a
// project's data happens under that project's mutex, so the single process
// is the only writer (multi-process servers sharing a config dir are a
// documented non-goal). Every mutation rewrites tasks.json before
// returning, so disk always matches memory and Mine can read files
// directly.
type Store struct {
	dir string // root directory, e.g. ~/.config/fusionlocalserver/tasks

	mu       sync.Mutex // guards projects map
	projects map[string]*projectState
}

// projectState is the in-memory copy of one project's tasks.json. mu
// serializes every read and write for the project.
type projectState struct {
	mu   sync.Mutex
	file *projectFile
}

// NewStore returns a Store rooted at dir, creating it if needed.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("tasks: creating store dir: %w", err)
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

// ---- reads ----

// List returns copies of a project's tasks, never nil.
func (s *Store) List(projectID string) ([]Task, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return nil, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := make([]Task, 0, len(ps.file.Tasks))
	for _, t := range ps.file.Tasks {
		out = append(out, copyTask(t))
	}
	return out, nil
}

// Get returns one task by id.
func (s *Store) Get(projectID, taskID string) (Task, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Task{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	t := findTask(ps.file, taskID)
	if t == nil {
		return Task{}, fmt.Errorf("%w: task %q", ErrNotFound, taskID)
	}
	return copyTask(t), nil
}

// ProjectInfo returns the hub id stored for a project (for handlers that
// need to resolve a task's hub without trusting the client).
func (s *Store) ProjectInfo(projectID string) (hubID, projectName string, err error) {
	ps, err := s.project(projectID)
	if err != nil {
		return "", "", err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.file.HubID, ps.file.ProjectName, nil
}

// Mine scans every project directory and returns tasks assigned to or
// created by the given user, matched by OIDC sub first and case-insensitive
// email as a fallback (sessions predating the sub claim — same rule as
// chat's matchesMember). Reads go straight to disk: mutations persist
// before returning and rewrites are atomic renames, so a concurrent read
// sees either the old or the new file, never a torn one. Unreadable,
// corrupt, or future-versioned files are skipped, not fatal — one bad
// project must not empty the user's whole task list.
func (s *Store) Mine(userID, email string) ([]ProjectTask, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ProjectTask{}, nil
		}
		return nil, fmt.Errorf("tasks: scanning store dir: %w", err)
	}
	out := []ProjectTask{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "tasks.json"))
		if err != nil {
			continue
		}
		var pf projectFile
		if err := json.Unmarshal(data, &pf); err != nil || pf.Version > fileVersion {
			continue
		}
		for _, t := range pf.Tasks {
			if t == nil || !matchesUser(t, userID, email) {
				continue
			}
			out = append(out, ProjectTask{
				Task:        copyTask(t),
				ProjectID:   pf.ProjectID,
				HubID:       pf.HubID,
				ProjectName: pf.ProjectName,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectName != out[j].ProjectName {
			return out[i].ProjectName < out[j].ProjectName
		}
		return out[i].Num < out[j].Num
	})
	return out, nil
}

func matchesUser(t *Task, userID, email string) bool {
	if t.Assignee != nil && matchesRef(*t.Assignee, userID, email) {
		return true
	}
	return matchesRef(t.CreatedBy, userID, email)
}

func matchesRef(ref UserRef, userID, email string) bool {
	if ref.ID != "" && ref.ID == userID {
		return true
	}
	return ref.Email != "" && email != "" && strings.EqualFold(ref.Email, email)
}

// ---- mutations ----

// Create validates the draft and appends a new task. hubID and projectName
// self-describe the project file for cross-project listings; they refresh
// on every create so renames converge.
func (s *Store) Create(projectID, hubID, projectName string, d Draft, createdBy UserRef) (Task, error) {
	if d.Status == "" {
		d.Status = "todo"
	}
	if d.Priority == "" {
		d.Priority = "medium"
	}
	d.Title = strings.TrimSpace(d.Title)
	d.Stage = strings.TrimSpace(d.Stage)
	if d.Milestone && d.StartDate != "" {
		// A milestone is a point in time; the form only asks for one date.
		d.EndDate = d.StartDate
	}
	if err := validateFields(d.Title, d.Description, d.Status, d.Priority, d.DueDate, d.DocRefs,
		scheduleFields{d.StartDate, d.EndDate, d.Progress, d.Milestone, d.Stage}); err != nil {
		return Task{}, err
	}
	ps, err := s.project(projectID)
	if err != nil {
		return Task{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if len(ps.file.Tasks) >= MaxTasksPerProject {
		return Task{}, fmt.Errorf("%w: project already has %d tasks", ErrInvalid, MaxTasksPerProject)
	}
	deps := normalizeDependsOn(d.DependsOn)
	// A brand-new task can't be on anyone's dependsOn yet, so existence
	// checks alone make a cycle impossible here.
	if err := validateDependsOn(ps.file, "", deps); err != nil {
		return Task{}, err
	}
	prev := cloneFile(ps.file)
	now := time.Now().UTC()
	t := &Task{
		ID:          fmt.Sprintf("t%d", ps.file.NextTaskID),
		Num:         ps.file.NextTaskID,
		Title:       d.Title,
		Description: d.Description,
		Status:      d.Status,
		Priority:    d.Priority,
		DueDate:     d.DueDate,
		Assignee:    copyRef(d.Assignee),
		CreatedBy:   createdBy,
		CreatedAt:   now,
		UpdatedAt:   now,
		DocRefs:     normalizeDocRefs(d.DocRefs),
		Rank:        maxRankInStatus(ps.file, d.Status) + 1024,
		StartDate:   d.StartDate,
		EndDate:     d.EndDate,
		Progress:    d.Progress,
		Milestone:   d.Milestone,
		DependsOn:   deps,
		Stage:       d.Stage,
	}
	ps.file.NextTaskID++
	ps.file.HubID = hubID
	ps.file.ProjectName = projectName
	ps.file.Tasks = append(ps.file.Tasks, t)
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file = prev
		return Task{}, err
	}
	return copyTask(t), nil
}

// Update applies a patch to one task. A status change without an explicit
// rank appends the task to its new column (list-view edits don't know about
// board order).
func (s *Store) Update(projectID, taskID string, p Patch) (Task, error) {
	if p.Title != nil {
		*p.Title = strings.TrimSpace(*p.Title)
	}
	ps, err := s.project(projectID)
	if err != nil {
		return Task{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	t := findTask(ps.file, taskID)
	if t == nil {
		return Task{}, fmt.Errorf("%w: task %q", ErrNotFound, taskID)
	}
	title, desc, status, priority, due := t.Title, t.Description, t.Status, t.Priority, t.DueDate
	if p.Title != nil {
		title = *p.Title
	}
	if p.Description != nil {
		desc = *p.Description
	}
	if p.Status != nil {
		status = *p.Status
	}
	if p.Priority != nil {
		priority = *p.Priority
	}
	if p.DueDate != nil {
		due = *p.DueDate
	}
	if p.ClearDueDate {
		due = ""
	}
	docRefs := t.DocRefs
	if p.DocRefs != nil {
		docRefs = *p.DocRefs
	}
	start, end, progress, milestone, stage := t.StartDate, t.EndDate, t.Progress, t.Milestone, t.Stage
	if p.StartDate != nil {
		start = *p.StartDate
	}
	if p.EndDate != nil {
		end = *p.EndDate
	}
	if p.Progress != nil {
		progress = *p.Progress
	}
	if p.Milestone != nil {
		milestone = *p.Milestone
	}
	if p.Stage != nil {
		stage = strings.TrimSpace(*p.Stage)
	}
	if p.ClearSchedule {
		start, end, milestone = "", "", false
	}
	if milestone && start != "" {
		end = start
	}
	deps := t.DependsOn
	if p.DependsOn != nil {
		deps = normalizeDependsOn(*p.DependsOn)
	}
	if err := validateFields(title, desc, status, priority, due, docRefs,
		scheduleFields{start, end, progress, milestone, stage}); err != nil {
		return Task{}, err
	}
	if p.DependsOn != nil {
		if err := validateDependsOn(ps.file, t.ID, deps); err != nil {
			return Task{}, err
		}
	}
	prev := cloneFile(ps.file)
	t.Title, t.Description, t.Priority, t.DueDate = title, desc, priority, due
	t.StartDate, t.EndDate, t.Progress, t.Milestone, t.Stage = start, end, progress, milestone, stage
	if p.DependsOn != nil {
		t.DependsOn = deps
	}
	if p.DocRefs != nil {
		t.DocRefs = normalizeDocRefs(docRefs)
	}
	if p.ClearAssignee {
		t.Assignee = nil
	} else if p.Assignee != nil {
		t.Assignee = copyRef(p.Assignee)
	}
	if status != t.Status {
		t.Status = status
		if p.Rank == nil {
			t.Rank = maxRankInStatus(ps.file, status) + 1024
		}
	}
	if p.Rank != nil {
		t.Rank = *p.Rank
	}
	t.UpdatedAt = time.Now().UTC()
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file = prev
		return Task{}, err
	}
	return copyTask(t), nil
}

// Delete removes a task outright. Dangling fls:task tokens in chat logs or
// wiki pages render as a designed "task not found" card, so a tombstone
// buys nothing the file format needs.
func (s *Store) Delete(projectID, taskID string) error {
	ps, err := s.project(projectID)
	if err != nil {
		return err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	idx := -1
	for i, t := range ps.file.Tasks {
		if t.ID == taskID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: task %q", ErrNotFound, taskID)
	}
	prev := cloneFile(ps.file)
	ps.file.Tasks = append(ps.file.Tasks[:idx], ps.file.Tasks[idx+1:]...)
	// Prune the deleted ID from every remaining dependsOn in the same
	// rewrite, so "every dependency exists" stays true in the file and no
	// reader ever has to defend against dangling IDs.
	for _, t := range ps.file.Tasks {
		if len(t.DependsOn) == 0 {
			continue
		}
		kept := t.DependsOn[:0]
		for _, dep := range t.DependsOn {
			if dep != taskID {
				kept = append(kept, dep)
			}
		}
		if len(kept) == 0 {
			t.DependsOn = nil
		} else {
			t.DependsOn = kept
		}
	}
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file = prev
		return err
	}
	return nil
}

// ShiftTasks moves every listed task's schedule by days (negative = earlier)
// in one atomic rewrite — the Gantt's stage-bar drag, where per-task PATCHes
// would burst the rate limit and could partially fail. Due dates are
// deadlines, not plans, and do not move. All-or-nothing: any unknown or
// unscheduled task rejects the whole shift.
func (s *Store) ShiftTasks(projectID string, taskIDs []string, days int) ([]Task, error) {
	if days == 0 || days < -MaxShiftDays || days > MaxShiftDays {
		return nil, fmt.Errorf("%w: shift must be within ±%d days and not zero", ErrInvalid, MaxShiftDays)
	}
	ids := normalizeDependsOn(taskIDs) // same trim+dedupe semantics
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: no tasks to shift", ErrInvalid)
	}
	ps, err := s.project(projectID)
	if err != nil {
		return nil, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	shifted := make([]*Task, 0, len(ids))
	for _, id := range ids {
		t := findTask(ps.file, id)
		if t == nil {
			return nil, fmt.Errorf("%w: task %q", ErrNotFound, id)
		}
		if t.StartDate == "" {
			return nil, fmt.Errorf("%w: task %q is not scheduled", ErrInvalid, id)
		}
		shifted = append(shifted, t)
	}
	prev := cloneFile(ps.file)
	now := time.Now().UTC()
	for _, t := range shifted {
		t.StartDate = shiftDay(t.StartDate, days)
		t.EndDate = shiftDay(t.EndDate, days)
		t.UpdatedAt = now
	}
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file = prev
		return nil, err
	}
	out := make([]Task, 0, len(shifted))
	for _, t := range shifted {
		out = append(out, copyTask(t))
	}
	return out, nil
}

// shiftDay moves a YYYY-MM-DD date by whole days in UTC (date-only math —
// no wall-clock zone can make a day disappear).
func shiftDay(day string, days int) string {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return d.AddDate(0, 0, days).Format("2006-01-02")
}

// ---- validation ----

// scheduleFields is the schedule slice of a task for validation: dates are
// both-or-neither, milestone implies a one-day schedule.
type scheduleFields struct {
	start     string
	end       string
	progress  int
	milestone bool
	stage     string
}

func validateFields(title, desc, status, priority, due string, docRefs []string, sched scheduleFields) error {
	if title == "" {
		return fmt.Errorf("%w: title is empty", ErrInvalid)
	}
	if utf8.RuneCountInString(title) > MaxTitleRunes {
		return fmt.Errorf("%w: title exceeds %d characters", ErrInvalid, MaxTitleRunes)
	}
	if utf8.RuneCountInString(desc) > MaxDescRunes {
		return fmt.Errorf("%w: description exceeds %d characters", ErrInvalid, MaxDescRunes)
	}
	if !validStatus(status) {
		return fmt.Errorf("%w: unknown status %q", ErrInvalid, status)
	}
	if !validPriority(priority) {
		return fmt.Errorf("%w: unknown priority %q", ErrInvalid, priority)
	}
	if due != "" {
		if _, err := time.Parse("2006-01-02", due); err != nil {
			return fmt.Errorf("%w: due date must be YYYY-MM-DD", ErrInvalid)
		}
	}
	if len(docRefs) > MaxDocRefs {
		return fmt.Errorf("%w: at most %d attached documents", ErrInvalid, MaxDocRefs)
	}
	for _, ref := range docRefs {
		// Attachments are document cards plus production cross-links (a task can
		// reference a job or a batch). All are compact fls: card tokens.
		ok := strings.HasPrefix(ref, "fls:doc?") ||
			strings.HasPrefix(ref, "fls:job?") ||
			strings.HasPrefix(ref, "fls:batch?")
		if !ok || len(ref) > MaxDocRefLen {
			return fmt.Errorf("%w: attachments must be fls:doc, fls:job or fls:batch tokens", ErrInvalid)
		}
	}
	if (sched.start == "") != (sched.end == "") {
		return fmt.Errorf("%w: startDate and endDate must be set together", ErrInvalid)
	}
	if sched.start != "" {
		if _, err := time.Parse("2006-01-02", sched.start); err != nil {
			return fmt.Errorf("%w: start date must be YYYY-MM-DD", ErrInvalid)
		}
		if _, err := time.Parse("2006-01-02", sched.end); err != nil {
			return fmt.Errorf("%w: end date must be YYYY-MM-DD", ErrInvalid)
		}
		// Both dates share one format, so string order is date order.
		if sched.end < sched.start {
			return fmt.Errorf("%w: end date is before start date", ErrInvalid)
		}
	}
	if sched.progress < 0 || sched.progress > 100 {
		return fmt.Errorf("%w: progress must be between 0 and 100", ErrInvalid)
	}
	if sched.milestone {
		if sched.start == "" {
			return fmt.Errorf("%w: a milestone needs a schedule", ErrInvalid)
		}
		if sched.end != sched.start {
			return fmt.Errorf("%w: a milestone's end date must equal its start date", ErrInvalid)
		}
	}
	if utf8.RuneCountInString(sched.stage) > MaxStageRunes {
		return fmt.Errorf("%w: stage exceeds %d characters", ErrInvalid, MaxStageRunes)
	}
	return nil
}

// validateDependsOn checks a task's (already normalized) predecessor list
// against the project file: caller holds the project lock. Every ID must
// name another existing task, and following dependsOn edges from the new
// list must not lead back to the task itself.
func validateDependsOn(pf *projectFile, selfID string, deps []string) error {
	if len(deps) > MaxDependsOn {
		return fmt.Errorf("%w: at most %d dependencies", ErrInvalid, MaxDependsOn)
	}
	for _, dep := range deps {
		if dep == selfID {
			return fmt.Errorf("%w: a task cannot depend on itself", ErrInvalid)
		}
		if findTask(pf, dep) == nil {
			return fmt.Errorf("%w: dependency %q not found", ErrInvalid, dep)
		}
	}
	if dependsOnCycle(pf, selfID, deps) {
		return fmt.Errorf("%w: dependencies would form a cycle", ErrInvalid)
	}
	return nil
}

// dependsOnCycle reports whether following dependsOn edges — with selfID's
// edges replaced by newDeps — leads back to selfID. Iterative DFS; the
// project cap bounds the walk at human scale.
func dependsOnCycle(pf *projectFile, selfID string, newDeps []string) bool {
	seen := map[string]bool{selfID: true}
	stack := append([]string(nil), newDeps...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == selfID {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if t := findTask(pf, id); t != nil {
			stack = append(stack, t.DependsOn...)
		}
	}
	return false
}

// normalizeDependsOn trims, drops empties and dedupes while preserving
// order (normalizeDocRefs sibling).
func normalizeDependsOn(deps []string) []string {
	out := make([]string, 0, len(deps))
	seen := make(map[string]bool, len(deps))
	for _, d := range deps {
		if d = strings.TrimSpace(d); d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func normalizeDocRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

func maxRankInStatus(pf *projectFile, status string) float64 {
	max := 0.0
	for _, t := range pf.Tasks {
		if t.Status == status && t.Rank > max {
			max = t.Rank
		}
	}
	return max
}

// ---- copies ----

func findTask(pf *projectFile, taskID string) *Task {
	for _, t := range pf.Tasks {
		if t.ID == taskID {
			return t
		}
	}
	return nil
}

func copyTask(t *Task) Task {
	out := *t
	out.Assignee = copyRef(t.Assignee)
	out.DocRefs = append([]string(nil), t.DocRefs...)
	if out.DocRefs == nil {
		out.DocRefs = []string{}
	}
	// DependsOn stays nil when empty (omitempty keeps old files byte-stable);
	// the DTO layer guarantees [] on the wire.
	out.DependsOn = append([]string(nil), t.DependsOn...)
	return out
}

func copyRef(r *UserRef) *UserRef {
	if r == nil {
		return nil
	}
	c := *r
	return &c
}

// cloneFile deep-copies a project file so a failed save can roll the
// in-memory state back. Task counts are capped at human scale, so the copy
// is cheap relative to the rewrite it accompanies.
func cloneFile(pf *projectFile) *projectFile {
	out := *pf
	out.Tasks = make([]*Task, len(pf.Tasks))
	for i, t := range pf.Tasks {
		c := copyTask(t)
		out.Tasks[i] = &c
	}
	return &out
}

// ---- persistence ----

// project returns the cached state for a project, loading tasks.json on
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
	return filepath.Join(s.projectDir(projectID), "tasks.json")
}

// loadFile reads a project's tasks.json. Absent → fresh empty file. Newer
// version → ErrFutureVersion (never rewrite what we don't understand).
// Corrupt → rename to .bak and start clean rather than block tasks for the
// whole project (chat.loadMeta precedent).
func (s *Store) loadFile(projectID string) (*projectFile, error) {
	path := s.filePath(projectID)
	fresh := &projectFile{
		Version:    fileVersion,
		Schema:     schemameta.New(),
		ProjectID:  projectID,
		NextTaskID: 1,
		Tasks:      []*Task{},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fresh, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tasks: reading %s: %w", path, err)
	}
	// Migrate older files forward before decoding into the current struct
	// (registry has no steps while fileVersion is the floor; the hook is
	// where every future upgrade lands). Future versions refuse.
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
	if pf.Tasks == nil {
		pf.Tasks = []*Task{}
	}
	if pf.NextTaskID < 1 {
		pf.NextTaskID = 1
	}
	// v1→v2 backfill: a freshly migrated file carries no stamp yet; its
	// birthdate is best-approximated by the file's mtime.
	if pf.Schema.CreatedAt.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			pf.Schema = schemameta.Backfill(info.ModTime())
		} else {
			pf.Schema = schemameta.New()
		}
	}
	return &pf, nil
}

// saveFile atomically rewrites tasks.json (temp file + rename, 0600), so a
// crash mid-write can never leave a half-written file behind.
func (s *Store) saveFile(projectID string, pf *projectFile) error {
	dir := s.projectDir(projectID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("tasks: creating project dir: %w", err)
	}
	pf.Schema.Touch()
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(s.filePath(projectID), data, 0600)
}

// sanitizeID maps a URN-format identifier to a filesystem-safe slug: any
// character outside [A-Za-z0-9_.\-] becomes '_', capped at 120 chars —
// copied verbatim from chat.sanitizeID so both stores age identically on
// disk.
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
