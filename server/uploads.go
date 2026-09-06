package server

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// Upload jobs are the server-side half of the drag-and-drop upload feature.
// POST /api/uploads spools the browser's bytes to a temp file and returns
// immediately; the APS upload (folder resolve → storage → signed S3 → item or
// version) then runs in a goroutine detached from the request, so it continues
// while the user navigates the SPA — or closes the tab entirely. Jobs live in
// memory, scoped to the session that created them; the session pointer is held
// so a long upload can refresh its own APS token mid-flight.

const (
	// uploadConcurrency bounds simultaneous APS-side uploads across all
	// sessions; excess jobs queue in submission order.
	uploadConcurrency = 3
	// uploadJobTimeout is the hard cap on one job — sized for multi-GiB files
	// on a slow uplink, not for API round-trips.
	uploadJobTimeout = 4 * time.Hour
	// uploadRetention keeps finished jobs listable (for the job view) before
	// they are pruned.
	uploadRetention = time.Hour
)

type uploadStatus string

const (
	uploadQueued   uploadStatus = "queued"
	uploadActive   uploadStatus = "uploading"
	uploadDone     uploadStatus = "done"
	uploadError    uploadStatus = "error"
	uploadCanceled uploadStatus = "canceled"
)

// terminal reports whether a status is final (the job will never change again).
func (st uploadStatus) terminal() bool {
	return st == uploadDone || st == uploadError || st == uploadCanceled
}

// uploadJob is one file's journey to Fusion Team. The immutable target fields
// are set at creation; status/result are guarded by mu; bytesSent is atomic so
// the transport goroutine can bump it while list snapshots read it.
type uploadJob struct {
	ID          string
	SessionID   string
	FileName    string
	Size        int64
	HubID       string   // GraphQL hub id (resolved to the DM hub id at run time)
	DMProjectID string   // project altId (DM id space)
	FolderPath  []string // folder display names from project root; empty = root
	// EnsureFolders makes FolderPath create-or-reuse rather than must-exist.
	// Set by features that file their own uploads (Production → Jobs/<job>/…);
	// a browsing upload leaves it false so a stale path surfaces as an error
	// instead of silently creating a folder.
	EnsureFolders bool
	// Client-side echoes, passed back verbatim so the SPA can invalidate the
	// right react-query caches when the job lands.
	ProjectID string
	FolderID  string
	CreatedAt time.Time

	bytesSent atomic.Int64
	tmpPath   string

	mu         sync.Mutex
	status     uploadStatus
	errMsg     string
	itemID     string
	versionID  string
	finishedAt time.Time
	cancelFn   context.CancelFunc
}

// setCancel installs the run context's cancel. If the job was canceled while
// still being scheduled, the fresh context is canceled immediately.
func (j *uploadJob) setCancel(fn context.CancelFunc) {
	j.mu.Lock()
	canceled := j.status == uploadCanceled
	j.cancelFn = fn
	j.mu.Unlock()
	if canceled {
		fn()
	}
}

func (j *uploadJob) setStatus(st uploadStatus) {
	j.mu.Lock()
	if !j.status.terminal() {
		j.status = st
	}
	j.mu.Unlock()
}

// finish moves the job to a terminal state (first writer wins — a cancel that
// raced the completion keeps whichever landed first).
func (j *uploadJob) finish(st uploadStatus, errMsg, itemID, versionID string) {
	j.mu.Lock()
	if !j.status.terminal() {
		j.status = st
		j.errMsg = errMsg
		j.itemID = itemID
		j.versionID = versionID
		j.finishedAt = time.Now()
	}
	j.mu.Unlock()
}

// cancel requests the job stop: terminal jobs are left alone, a queued or
// running job flips to canceled and has its context torn down.
func (j *uploadJob) cancel() {
	j.mu.Lock()
	fn := j.cancelFn
	if !j.status.terminal() {
		j.status = uploadCanceled
		j.finishedAt = time.Now()
	}
	j.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// uploadManager is the in-memory job registry plus the concurrency gate.
type uploadManager struct {
	mu    sync.Mutex
	jobs  map[string]*uploadJob
	order []string // insertion order, for stable listings
	sem   chan struct{}
}

func newUploadManager(concurrency int) *uploadManager {
	return &uploadManager{
		jobs: make(map[string]*uploadJob),
		sem:  make(chan struct{}, concurrency),
	}
}

func (m *uploadManager) add(j *uploadJob) {
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.order = append(m.order, j.ID)
	m.mu.Unlock()
}

// get returns the job only if it belongs to the session — one user's jobs are
// invisible (and uncancelable) to another.
func (m *uploadManager) get(id, sessionID string) (*uploadJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.SessionID != sessionID {
		return nil, false
	}
	return j, true
}

// listFor returns the session's jobs in submission order, pruning any that
// finished more than uploadRetention ago.
func (m *uploadManager) listFor(sessionID string) []*uploadJob {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.order[:0]
	var out []*uploadJob
	for _, id := range m.order {
		j := m.jobs[id]
		j.mu.Lock()
		stale := j.status.terminal() && now.Sub(j.finishedAt) > uploadRetention
		j.mu.Unlock()
		if stale {
			delete(m.jobs, id)
			continue
		}
		kept = append(kept, id)
		if j.SessionID == sessionID {
			out = append(out, j)
		}
	}
	m.order = kept
	return out
}

// dismiss removes finished jobs from the session's list: the one given by id,
// or every terminal job when id is empty. Active jobs are never dismissed.
func (m *uploadManager) dismiss(id, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.order[:0]
	for _, jid := range m.order {
		j := m.jobs[jid]
		j.mu.Lock()
		done := j.status.terminal()
		j.mu.Unlock()
		if j.SessionID == sessionID && done && (id == "" || jid == id) {
			delete(m.jobs, jid)
			continue
		}
		kept = append(kept, jid)
	}
	m.order = kept
}

// runUpload executes one job end to end. It owns the temp file and always
// removes it. sess is held (not just a token) so the job can refresh the APS
// token however long the transfer takes.
func (s *Server) runUpload(job *uploadJob, sess *Session) {
	ctx, cancel := context.WithTimeout(apsbudget.WithPriority(context.Background(), apsbudget.P2), uploadJobTimeout)
	job.setCancel(cancel)
	defer cancel()
	defer os.Remove(job.tmpPath)

	// Wait for an upload slot; canceling a queued job aborts the wait.
	select {
	case s.uploads.sem <- struct{}{}:
		defer func() { <-s.uploads.sem }()
	case <-ctx.Done():
		job.finish(uploadCanceled, "", "", "")
		return
	}
	job.setStatus(uploadActive)

	tokenFn := func(c context.Context) (string, error) { return s.sessionToken(c, sess) }
	itemID, versionID, err := s.uploadJobRun(ctx, tokenFn, job)
	if err != nil {
		if ctx.Err() != nil {
			job.finish(uploadCanceled, "", "", "")
		} else {
			job.finish(uploadError, s.jobErrorMessage(err), "", "")
		}
		s.logger.Error("upload failed", "file", job.FileName, "job", job.ID, "err", err)
		return
	}
	job.finish(uploadDone, "", itemID, versionID)
	// The folder listing this user cached seconds ago no longer has the new
	// item; drop their coalesced answers so the next fetch shows it.
	if sess != nil && sess.Profile.Sub != "" {
		api.InvalidateSubject(sess.Profile.Sub)
	}
	s.logger.Info("upload complete", "file", job.FileName, "job", job.ID, "item", itemID)
}

// uploadJobRun is the happy-path body of a job: resolve ids, open the spool,
// push the bytes.
func (s *Server) uploadJobRun(ctx context.Context, tokenFn api.TokenSource, job *uploadJob) (string, string, error) {
	token, err := tokenFn(ctx)
	if err != nil {
		return "", "", err
	}
	dmHubID, err := api.GetHubDataManagementID(ctx, token, job.HubID)
	if err != nil {
		return "", "", err
	}
	resolve := api.ResolveFolderPath
	if job.EnsureFolders {
		resolve = api.EnsureFolderPath
	}
	folderID, err := resolve(ctx, token, dmHubID, job.DMProjectID, job.FolderPath)
	if err != nil {
		return "", "", err
	}
	f, err := os.Open(job.tmpPath)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	return api.UploadFileToFolder(ctx, tokenFn, job.DMProjectID, folderID, job.FileName, f, job.Size,
		func(d int64) { job.bytesSent.Add(d) })
}

// jobErrorMessage mirrors s.fail's redaction for the job DTO: category only by
// default, full detail appended under -v.
func (s *Server) jobErrorMessage(err error) string {
	msg := safeErrorMessage(statusForError(err))
	if s.opts.Verbose {
		msg += ": " + err.Error()
	}
	return msg
}
