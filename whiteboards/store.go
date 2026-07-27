package whiteboards

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

// registry is the whiteboards migration table (metadata file only; tldraw
// doc files are opaque snapshots and version with tldraw itself).
var registry = newRegistry()

func newRegistry() *migrate.Registry {
	r := migrate.NewRegistry("whiteboards", fileVersion)
	// v1→v2: schema stamp joins the envelope; loader backfills it.
	r.Register(1, func(raw map[string]any) (map[string]any, error) { return raw, nil })
	return r
}

// Store owns all whiteboard persistence. One Store per server; all mutation of
// a project's data happens under that project's mutex, so the single process is
// the only writer (multi-process servers sharing a config dir are a documented
// non-goal).
type Store struct {
	dir string // root, e.g. ~/.config/fusionlocalserver/whiteboards

	mu       sync.Mutex // guards projects map
	projects map[string]*projectState
}

// projectState is the in-memory copy of one project's whiteboards.json. mu
// serialises every read and write for the project — including the document
// files, so a board can't be deleted while its snapshot is being written.
type projectState struct {
	mu   sync.Mutex
	file *projectFile
}

// NewStore returns a Store rooted at dir, creating it if needed.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("whiteboards: creating store dir: %w", err)
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

// DeleteProject permanently removes one project's whiteboard data — metadata
// and every board document: the in-memory state is evicted and the project
// directory deleted. A missing directory is not an error (idempotent); the
// next access lazily recreates fresh state. Lock order is s.mu → ps.mu (the
// chat closeHandlesLocked order; no code path acquires s.mu while holding a
// project mutex), and holding the project mutex through the removal means no
// in-flight autosave can rewrite a document mid-delete.
func (s *Store) DeleteProject(projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps, ok := s.projects[projectID]; ok {
		delete(s.projects, projectID)
		ps.mu.Lock()
		defer ps.mu.Unlock()
	}
	if err := os.RemoveAll(s.projectDir(projectID)); err != nil {
		return fmt.Errorf("whiteboards: deleting project data: %w", err)
	}
	return nil
}

// ---- reads ----

// List returns copies of a project's board metadata, newest first. It never
// touches the document files, so listing stays cheap however large the boards.
func (s *Store) List(projectID string) ([]Board, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return nil, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := make([]Board, 0, len(ps.file.Boards))
	for _, b := range ps.file.Boards {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Num > out[j].Num })
	return out, nil
}

// Get returns one board's metadata.
func (s *Store) Get(projectID, boardID string) (Board, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return Board{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	b := findBoard(ps.file, boardID)
	if b == nil {
		return Board{}, fmt.Errorf("%w: whiteboard %q", ErrNotFound, boardID)
	}
	return *b, nil
}

// ProjectInfo returns the hub id and name stored for a project.
func (s *Store) ProjectInfo(projectID string) (hubID, projectName string, err error) {
	ps, err := s.project(projectID)
	if err != nil {
		return "", "", err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.file.HubID, ps.file.ProjectName, nil
}

// Document returns a board's stored tldraw document and the revision it is at.
// A board that has never been saved returns nil with no error — the client then
// starts an empty canvas, which is the correct initial state rather than an
// error case. (Named Document rather than Snapshot so the backup engine's
// Snapshot(visit) method can carry the store-uniform name.)
//
// The document and its revision are read under one lock hold: a caller that
// fetched them separately could load one save's bytes and the next save's
// revision, and would then believe its stale document was current.
func (s *Store) Document(projectID, boardID string) ([]byte, int64, error) {
	ps, err := s.project(projectID)
	if err != nil {
		return nil, 0, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	b := findBoard(ps.file, boardID)
	if b == nil {
		return nil, 0, fmt.Errorf("%w: whiteboard %q", ErrNotFound, boardID)
	}
	data, err := os.ReadFile(s.snapshotPath(projectID, boardID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, b.DocRev, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("whiteboards: reading document: %w", err)
	}
	return data, b.DocRev, nil
}

// ---- mutations ----

// Create adds a board. hubID/projectName self-describe the file, refreshed on
// every create so renames converge (the tasks precedent).
func (s *Store) Create(projectID, hubID, projectName string, d Draft, createdBy UserRef) (Board, error) {
	d.Name = strings.TrimSpace(d.Name)
	if err := validateName(d.Name); err != nil {
		return Board{}, err
	}
	ps, err := s.project(projectID)
	if err != nil {
		return Board{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if len(ps.file.Boards) >= MaxBoardsPerProject {
		return Board{}, fmt.Errorf("%w: project already has %d whiteboards", ErrInvalid, MaxBoardsPerProject)
	}
	prev := cloneFile(ps.file)
	now := time.Now().UTC()
	b := &Board{
		ID:        fmt.Sprintf("w%d", ps.file.NextBoardID),
		Num:       ps.file.NextBoardID,
		Name:      d.Name,
		CreatedBy: createdBy,
		CreatedAt: now,
		UpdatedAt: now,
		UpdatedBy: createdBy,
	}
	ps.file.NextBoardID++
	ps.file.HubID = hubID
	ps.file.ProjectName = projectName
	ps.file.Boards = append(ps.file.Boards, b)
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file = prev
		return Board{}, err
	}
	return *b, nil
}

// Update renames a board.
func (s *Store) Update(projectID, boardID string, p Patch) (Board, error) {
	if p.Name != nil {
		*p.Name = strings.TrimSpace(*p.Name)
		if err := validateName(*p.Name); err != nil {
			return Board{}, err
		}
	}
	ps, err := s.project(projectID)
	if err != nil {
		return Board{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	b := findBoard(ps.file, boardID)
	if b == nil {
		return Board{}, fmt.Errorf("%w: whiteboard %q", ErrNotFound, boardID)
	}
	prev := cloneFile(ps.file)
	if p.Name != nil {
		b.Name = *p.Name
	}
	b.UpdatedAt = time.Now().UTC()
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file = prev
		return Board{}, err
	}
	return *b, nil
}

// Delete removes a board and its document. The metadata write happens first:
// if the document file can't be removed we still return success, since an
// orphaned document nobody can reach is harmless, whereas leaving the board
// listed after the user deleted it is not.
func (s *Store) Delete(projectID, boardID string) error {
	ps, err := s.project(projectID)
	if err != nil {
		return err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	idx := -1
	for i, b := range ps.file.Boards {
		if b.ID == boardID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: whiteboard %q", ErrNotFound, boardID)
	}
	prev := cloneFile(ps.file)
	ps.file.Boards = append(ps.file.Boards[:idx], ps.file.Boards[idx+1:]...)
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file = prev
		return err
	}
	_ = os.Remove(s.snapshotPath(projectID, boardID))
	return nil
}

// SaveSnapshot writes a board's tldraw document and stamps the board's
// updated-by/at. The document is written atomically (temp + rename) like every
// other file here, so an autosave interrupted mid-write can never truncate the
// user's board.
//
// baseRev is the revision the caller loaded. It must still be the board's
// current revision or the save is refused with ErrConflict — without that check
// two people on one board each PUT their whole local document and the later
// save discards the earlier one's work entirely. force skips the check, for the
// user who has been shown the conflict and chose to overwrite anyway; it is a
// deliberate, acknowledged act, never a retry path.
//
// The check happens here rather than in the handler because only here is it
// under the project lock that also serialises the write — a handler-side
// compare would leave a window between reading the revision and writing.
func (s *Store) SaveSnapshot(projectID, boardID string, doc []byte, by UserRef, baseRev int64, force bool) (Board, error) {
	if len(doc) == 0 {
		return Board{}, fmt.Errorf("%w: empty document", ErrInvalid)
	}
	if len(doc) > MaxSnapshotBytes {
		return Board{}, fmt.Errorf("%w: document exceeds %d bytes", ErrInvalid, MaxSnapshotBytes)
	}
	if !json.Valid(doc) {
		return Board{}, fmt.Errorf("%w: document is not valid JSON", ErrInvalid)
	}
	ps, err := s.project(projectID)
	if err != nil {
		return Board{}, err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	b := findBoard(ps.file, boardID)
	if b == nil {
		return Board{}, fmt.Errorf("%w: whiteboard %q", ErrNotFound, boardID)
	}
	if !force && baseRev != b.DocRev {
		return Board{}, fmt.Errorf("%w: board is at revision %d, save was based on %d",
			ErrConflict, b.DocRev, baseRev)
	}
	if err := s.writeSnapshot(projectID, boardID, doc); err != nil {
		return Board{}, err
	}
	prev := cloneFile(ps.file)
	b.UpdatedAt = time.Now().UTC()
	b.UpdatedBy = by
	b.SnapshotBytes = int64(len(doc))
	b.DocRev++
	if err := s.saveFile(projectID, ps.file); err != nil {
		ps.file = prev
		return Board{}, err
	}
	return *b, nil
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

func findBoard(pf *projectFile, boardID string) *Board {
	for _, b := range pf.Boards {
		if b.ID == boardID {
			return b
		}
	}
	return nil
}

func cloneFile(pf *projectFile) *projectFile {
	out := *pf
	out.Boards = make([]*Board, len(pf.Boards))
	for i, b := range pf.Boards {
		c := *b
		out.Boards[i] = &c
	}
	return &out
}

// ---- persistence ----

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
	return filepath.Join(s.projectDir(projectID), "whiteboards.json")
}

// snapshotPath is one board's document. boardID is server-generated ("w<n>")
// and sanitised anyway, so it can never escape the project directory.
func (s *Store) snapshotPath(projectID, boardID string) string {
	return filepath.Join(s.projectDir(projectID), "doc-"+sanitizeID(boardID)+".json")
}

// loadFile reads whiteboards.json. Absent → fresh. Newer version →
// ErrFutureVersion (never rewrite what we don't understand). Corrupt → rename
// to .bak and start clean, so one bad file doesn't block the whole project.
func (s *Store) loadFile(projectID string) (*projectFile, error) {
	path := s.filePath(projectID)
	fresh := &projectFile{
		Version:     fileVersion,
		Schema:      schemameta.New(),
		ProjectID:   projectID,
		NextBoardID: 1,
		Boards:      []*Board{},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fresh, nil
	}
	if err != nil {
		return nil, fmt.Errorf("whiteboards: reading %s: %w", path, err)
	}
	// Migrate older files forward before decoding (no steps registered
	// while fileVersion is the floor); future versions refuse.
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
	if pf.Boards == nil {
		pf.Boards = []*Board{}
	}
	if pf.NextBoardID < 1 {
		pf.NextBoardID = 1
	}
	if pf.Schema.CreatedAt.IsZero() {
		if info, statErr := os.Stat(path); statErr == nil {
			pf.Schema = schemameta.Backfill(info.ModTime())
		} else {
			pf.Schema = schemameta.New()
		}
	}
	return &pf, nil
}

func (s *Store) saveFile(projectID string, pf *projectFile) error {
	pf.Schema.Touch()
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	return s.writeAtomic(projectID, s.filePath(projectID), "whiteboards-*.tmp", data)
}

func (s *Store) writeSnapshot(projectID, boardID string, doc []byte) error {
	return s.writeAtomic(projectID, s.snapshotPath(projectID, boardID), "doc-*.tmp", doc)
}

// writeAtomic writes via a temp file + rename (0600), so a crash mid-write can
// never leave a half-written file behind — the difference between a whiteboard
// and a truncated whiteboard.
func (s *Store) writeAtomic(projectID, path, pattern string, data []byte) error {
	_ = pattern // retained in the signature for call-site clarity
	dir := s.projectDir(projectID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("whiteboards: creating project dir: %w", err)
	}
	return atomicfile.WriteFile(path, data, 0600)
}

// sanitizeID maps a URN-format identifier to a filesystem-safe slug — copied
// verbatim from tasks/chat/production so all four stores age identically on
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
