package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/notifications"
)

// Archive jobs are the server-side half of "download this Fusion design".
// POST /api/archives returns immediately; asking APS to generate the archive
// (pick a format → create a download → poll until it lands) then runs in a
// goroutine detached from the request, because generation takes minutes for a
// real assembly and no browser request should be held open that long. Jobs live
// in memory, scoped to the session that created them; the session pointer is
// held so a long poll can refresh its own APS token.
//
// Deliberately, a finished job stores APS's own URL for the download and NOT
// the bytes: nothing lands on this server's disk, there is no retention policy
// to get wrong, and re-downloading the same archive just re-signs a fresh S3
// url.
//
// The cost of that is a link with a lifetime. The "ready" notification is
// persisted per user, but the download URL it needs is here, in memory — so the
// link dies on restart and after archiveRetention. The bell checks the polled job
// list before offering the download rather than letting the user click into an
// error (see ArchiveReadyLink).
//
// Modelled on uploads.go; the manager, the status machine and the session
// scoping are deliberately the same shape, so the two job systems age together.

const (
	// archiveConcurrency bounds simultaneous generations across all sessions.
	// Lower than uploads: the work happens inside APS against a per-minute
	// cost quota, and queueing here is cheaper than being 429'd there.
	archiveConcurrency = 2
	// archiveJobTimeout is the hard cap on one job — sized for a large
	// assembly's generation, not for an API round-trip.
	archiveJobTimeout = 30 * time.Minute
	// archiveRetention keeps finished jobs listable before they are pruned.
	// Longer than uploads': a ready archive is something the user comes back
	// to, not something they watch land.
	archiveRetention = 2 * time.Hour

	// Poll cadence. Generation is minutes, so the first couple of polls are
	// quick (a small part can finish fast) and then it settles.
	archivePollMin = 2 * time.Second
	archivePollMax = 15 * time.Second
)

type archiveStatus string

const (
	archiveQueued    archiveStatus = "queued"
	archivePreparing archiveStatus = "preparing"
	archiveReady     archiveStatus = "ready"
	archiveError     archiveStatus = "error"
	archiveCanceled  archiveStatus = "canceled"
)

// terminal reports whether a status is final (the job will never change again).
func (st archiveStatus) terminal() bool {
	return st == archiveReady || st == archiveError || st == archiveCanceled
}

// archiveJob is one design's journey from "generate this" to a downloadable
// url. The immutable target fields are set at creation; everything the poll
// loop discovers is guarded by mu.
type archiveJob struct {
	ID          string
	SessionID   string
	UserKey     string // inbox key for the completion notification
	HubID       string
	DMProjectID string // project altId (DM id space)
	ProjectID   string // GraphQL project id, echoed back for cache invalidation
	ProjectName string
	ItemID      string // lineage urn
	DocName     string // the document's display name, for the file name and the bell
	CreatedAt   time.Time

	mu       sync.Mutex
	status   archiveStatus
	errMsg   string
	errCode  string // stable token the SPA localizes (see respond.go)
	fileType string // "f3z" / "f3d", decided by APS not by us
	fileName string
	// downloadURL is APS's own address for the finished download, taken from
	// the completion redirect. The storage urn behind it, and the signed url
	// behind that, both expire — this does not.
	downloadURL string
	finishedAt  time.Time
	cancelFn    context.CancelFunc
}

// setCancel installs the run context's cancel. If the job was canceled while
// still being scheduled, the fresh context is canceled immediately.
func (j *archiveJob) setCancel(fn context.CancelFunc) {
	j.mu.Lock()
	canceled := j.status == archiveCanceled
	j.cancelFn = fn
	j.mu.Unlock()
	if canceled {
		fn()
	}
}

func (j *archiveJob) setStatus(st archiveStatus) {
	j.mu.Lock()
	if !j.status.terminal() {
		j.status = st
	}
	j.mu.Unlock()
}

// setFormat records what APS agreed to build, as soon as it is known, so the
// list view can say "preparing F3Z" rather than just "preparing".
func (j *archiveJob) setFormat(fileType, fileName string) {
	j.mu.Lock()
	j.fileType, j.fileName = fileType, fileName
	j.mu.Unlock()
}

// format reads back what setFormat recorded. The download handler runs on a
// request goroutine while the poll loop may still be writing, so these are read
// under the lock like every other mutable field.
func (j *archiveJob) format() (fileType, fileName string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.fileType, j.fileName
}

// finish moves the job to a terminal state (first writer wins — a cancel that
// raced completion keeps whichever landed first). Reports whether this call is
// the one that settled it, so only one caller emits a notification.
func (j *archiveJob) finish(st archiveStatus, errMsg, errCode, downloadURL string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status.terminal() {
		return false
	}
	j.status = st
	j.errMsg = errMsg
	j.errCode = errCode
	j.downloadURL = downloadURL
	j.finishedAt = time.Now()
	return true
}

// cancel requests the job stop: terminal jobs are left alone, a queued or
// running job flips to canceled and has its context torn down.
func (j *archiveJob) cancel() {
	j.mu.Lock()
	fn := j.cancelFn
	if !j.status.terminal() {
		j.status = archiveCanceled
		j.finishedAt = time.Now()
	}
	j.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// ready reports the download url of a finished job, or false if it is not
// ready yet.
func (j *archiveJob) ready() (downloadURL string, ok bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status != archiveReady {
		return "", false
	}
	return j.downloadURL, true
}

// archiveManager is the in-memory job registry plus the concurrency gate.
type archiveManager struct {
	mu    sync.Mutex
	jobs  map[string]*archiveJob
	order []string // insertion order, for stable listings
	sem   chan struct{}
}

func newArchiveManager(concurrency int) *archiveManager {
	return &archiveManager{
		jobs: make(map[string]*archiveJob),
		sem:  make(chan struct{}, concurrency),
	}
}

func (m *archiveManager) add(j *archiveJob) {
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.order = append(m.order, j.ID)
	m.mu.Unlock()
}

// get returns the job only if it belongs to the session — one user's archives
// are invisible (and undownloadable) to another.
func (m *archiveManager) get(id, sessionID string) (*archiveJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.SessionID != sessionID {
		return nil, false
	}
	return j, true
}

// activeFor reports whether the session already has a live job for an item, so
// the UI can disable a second click rather than queueing a duplicate
// generation against the APS quota.
func (m *archiveManager) activeFor(sessionID, itemID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.SessionID != sessionID || j.ItemID != itemID {
			continue
		}
		j.mu.Lock()
		live := !j.status.terminal()
		j.mu.Unlock()
		if live {
			return true
		}
	}
	return false
}

// listFor returns the session's jobs in submission order, pruning any that
// finished more than archiveRetention ago.
func (m *archiveManager) listFor(sessionID string) []*archiveJob {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.order[:0]
	var out []*archiveJob
	for _, id := range m.order {
		j := m.jobs[id]
		j.mu.Lock()
		stale := j.status.terminal() && now.Sub(j.finishedAt) > archiveRetention
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
func (m *archiveManager) dismiss(id, sessionID string) {
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

// errNoArchiveFormat marks the one failure worth explaining precisely: APS
// will not build a native archive of this version at all.
var errNoArchiveFormat = errors.New("no native archive format available for this version")

// runArchive executes one job end to end: wait for a slot, resolve the tip
// version, pick a format, kick off generation, poll to completion. notif may be
// nil (no local stores) — the job still runs, it just cannot announce itself.
func (s *Server) runArchive(job *archiveJob, sess *Session, notif *notifications.Store) {
	ctx, cancel := context.WithTimeout(context.Background(), archiveJobTimeout)
	job.setCancel(cancel)
	defer cancel()

	// Wait for a generation slot; canceling a queued job aborts the wait.
	select {
	case s.archives.sem <- struct{}{}:
		defer func() { <-s.archives.sem }()
	case <-ctx.Done():
		job.finish(archiveCanceled, "", "", "")
		return
	}
	job.setStatus(archivePreparing)

	downloadURL, err := s.archiveJobRun(ctx, sess, job)
	if err != nil {
		if ctx.Err() != nil {
			job.finish(archiveCanceled, "", "", "")
			return
		}
		code := "upstream_failed"
		if errors.Is(err, errNoArchiveFormat) {
			code = "archive_format_unavailable"
		}
		if job.finish(archiveError, s.jobErrorMessage(err), code, "") {
			s.emitArchiveResult(notif, job, false)
		}
		s.logger.Error("archive failed", "doc", job.DocName, "job", job.ID, "err", err)
		return
	}
	if job.finish(archiveReady, "", "", downloadURL) {
		s.emitArchiveResult(notif, job, true)
	}
	s.logger.Info("archive ready", "doc", job.DocName, "job", job.ID, "format", job.fileType)
}

// archiveJobRun is the happy-path body: tip version → offered formats → the
// generation job → the finished download id.
func (s *Server) archiveJobRun(ctx context.Context, sess *Session, job *archiveJob) (string, error) {
	token, err := s.sessionToken(ctx, sess)
	if err != nil {
		return "", err
	}
	versionURN, err := api.GetItemTipVersion(ctx, token, job.DMProjectID, job.ItemID)
	if err != nil {
		return "", err
	}
	formats, err := api.DownloadFormats(ctx, token, job.DMProjectID, versionURN)
	if err != nil {
		return "", err
	}
	// APS decides F3Z vs F3D, not the file extension: a design with external
	// references can only be produced as F3Z, and asking for the wrong one
	// fails with an opaque 400.
	fileType, ok := api.PickArchiveFormat(formats)
	if !ok {
		return "", errNoArchiveFormat
	}
	job.setFormat(fileType, archiveFileName(job.DocName, fileType))

	apsJobIDs, err := api.CreateDownload(ctx, token, job.DMProjectID, versionURN, fileType)
	if err != nil {
		return "", err
	}
	// APS answers with a list. It has only ever been one job in practice — an
	// F3Z already bundles a design's references into a single file — but if it
	// is ever more, polling only the first would hand back one piece of a set
	// and call it the archive. Say so rather than guessing at the semantics.
	if len(apsJobIDs) > 1 {
		s.logger.Warn("archive: APS started more than one job; downloading the first",
			"job", job.ID, "doc", job.DocName, "apsJobs", len(apsJobIDs))
	}
	apsJobID := apsJobIDs[0]

	delay := archivePollMin
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
		// Re-read the token each poll: a 30-minute job outlives an APS access
		// token, and sessionToken refreshes it under the session's own lock.
		token, err = s.sessionToken(ctx, sess)
		if err != nil {
			return "", err
		}
		downloadURL, done, perr := api.PollDownloadJob(ctx, token, job.DMProjectID, apsJobID)
		if perr != nil {
			return "", perr
		}
		if done {
			return downloadURL, nil
		}
		if delay < archivePollMax {
			delay *= 2
			if delay > archivePollMax {
				delay = archivePollMax
			}
		}
	}
}

// emitArchiveResult drops the job's outcome in the requester's inbox. Best
// effort and after the fact — an inbox failure must never change the job.
func (s *Server) emitArchiveResult(notif *notifications.Store, job *archiveJob, ok bool) {
	if notif == nil || job.UserKey == "" {
		return
	}
	n := notifications.Notification{
		Kind:        notifications.KindArchiveFailed,
		HubID:       job.HubID,
		ProjectID:   job.ProjectID,
		ProjectName: job.ProjectName,
		Subject:     job.DocName,
	}
	if ok {
		n.Kind = notifications.KindArchive
		// The click-through target. Resolved by the bell, never rendered as a
		// card, so it is not part of the fls: card-token allow-list.
		n.Ref = "fls:archive?id=" + job.ID
	}
	if _, _, err := notif.Add(job.UserKey, n); err != nil {
		s.logger.Error("notifications: archive emit failed", "job", job.ID, "err", err)
	}
}

// archiveFileName builds the name the browser saves under. The document name is
// user data and may contain anything, so it is sanitized here rather than
// trusted into a Content-Disposition header.
func archiveFileName(docName, fileType string) string {
	base := sanitizeDownloadName(docName)
	if base == "" {
		base = "design"
	}
	return base + "." + fileType
}
