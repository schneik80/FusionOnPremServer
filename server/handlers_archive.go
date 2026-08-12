package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
)

// archiveMaxBody caps the create-archive request body.
const archiveMaxBody = 16 << 10

// archiveStreamTimeout bounds serving one finished archive to the browser. An
// F3Z of a large assembly is hundreds of MiB, so this is generous — but it is
// still bounded, unlike the job that produced it.
const archiveStreamTimeout = 15 * time.Minute

// archiveCreateReq is the create-job body. dmProjectId is the project altId
// (DM id space) every byte-level APS endpoint needs; itemId is the lineage urn.
//
// versionId is optional and pins the archive to one version — a production
// card asks for the version it froze, the details header omits it and gets the
// tip. It is only ever accepted for a version of itemId's own lineage.
type archiveCreateReq struct {
	HubID       string `json:"hubId"`
	DMProjectID string `json:"dmProjectId"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ItemID      string `json:"itemId"`
	VersionID   string `json:"versionId"`
	Name        string `json:"name"`
}

// handleArchiveCreate starts a background archive job for one document and
// returns the session's refreshed job list. The APS work — resolve the target
// version, ask which native formats it can produce, generate, poll — happens
// in a goroutine detached from this request, because it takes minutes.
// POST /api/archives
func (s *Server) handleArchiveCreate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	var in archiveCreateReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, archiveMaxBody)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// The hub rides in the body, which requireHub's central query-param check
	// does not cover — so it is compared to the session hub explicitly.
	if in.HubID != "" && !hubMatches(w, set.hubID, in.HubID) {
		return
	}
	if in.DMProjectID == "" || in.ItemID == "" {
		writeError(w, http.StatusBadRequest, "dmProjectId and itemId are required")
		return
	}
	// A client may name a version, but only one of the document it is asking
	// about — the same guard the production version pin applies. Without it a
	// crafted body would archive any version urn the session can reach under
	// another document's name.
	if in.VersionID != "" && !api.VersionBelongsToItem(in.VersionID, in.ItemID) {
		writeError(w, http.StatusBadRequest, "versionId does not belong to the document")
		return
	}
	// One live job per document version per session. A second click would spend
	// the APS cost quota generating a byte-identical archive.
	if s.archives.activeFor(sess.ID, in.ItemID, in.VersionID) {
		writeErrorCode(w, http.StatusConflict, "archive_already_running",
			"an archive of this document is already being prepared")
		return
	}

	id, err := randToken(16)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	job := &archiveJob{
		ID:          id,
		SessionID:   sess.ID,
		UserKey:     notifUserKey(chat.Identity{UserID: sess.Profile.Sub, Email: sess.Profile.Email}),
		HubID:       set.hubID,
		DMProjectID: in.DMProjectID,
		ProjectID:   in.ProjectID,
		ProjectName: in.ProjectName,
		ItemID:      in.ItemID,
		VersionID:   in.VersionID,
		DocName:     in.Name,
		CreatedAt:   time.Now(),
		status:      archiveQueued,
	}
	s.archives.add(job)
	go s.runArchive(job, sess, set.notifications)
	writeJSON(w, http.StatusAccepted, archiveJobDTOs(s.archives.listFor(sess.ID)))
}

// handleArchiveList returns the session's archive jobs (all states) in
// submission order.
// GET /api/archives
func (s *Server) handleArchiveList(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, archiveJobDTOs(s.archives.listFor(sess.ID)))
}

// handleArchiveCancel stops one job (queued or generating) and returns the
// refreshed list. APS keeps generating on its side — we simply stop waiting.
// POST /api/archives/cancel?id=<job id>
func (s *Server) handleArchiveCancel(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	id, ok := reqParam(w, r, "id")
	if !ok {
		return
	}
	if job, ok := s.archives.get(id, sess.ID); ok {
		job.cancel()
	}
	writeJSON(w, http.StatusOK, archiveJobDTOs(s.archives.listFor(sess.ID)))
}

// handleArchiveDismiss clears finished jobs — the one named by id, or all of
// them when id is omitted — and returns the refreshed list.
// POST /api/archives/dismiss?id=<job id, optional>
func (s *Server) handleArchiveDismiss(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	s.archives.dismiss(r.URL.Query().Get("id"), sess.ID)
	writeJSON(w, http.StatusOK, archiveJobDTOs(s.archives.listFor(sess.ID)))
}

// handleArchiveFile streams a finished archive to the browser. The job holds
// only the APS download id, so the signed url is re-resolved on every request:
// signatures expire in minutes, and the url never leaves this process (it is a
// bearer credential for the object — the same reason handleFile proxies rather
// than redirects).
// GET /api/archives/file?id=<job id>
func (s *Server) handleArchiveFile(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	id, ok := reqParam(w, r, "id")
	if !ok {
		return
	}
	job, ok := s.archives.get(id, sess.ID)
	if !ok {
		writeError(w, http.StatusNotFound, "no such archive")
		return
	}
	downloadURL, ready := job.ready()
	if !ready {
		writeErrorCode(w, http.StatusConflict, "archive_not_ready",
			"this archive is not ready yet")
		return
	}

	// An archive transfer far outlasts a normal API call, so it gets the
	// download-appropriate cap rather than the 30s handler timeout — same
	// reasoning as handleFile.
	ctx, cancel := context.WithTimeout(r.Context(), archiveStreamTimeout)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	resp, target, err := api.OpenArchive(ctx, token, downloadURL)
	if err != nil {
		// Log the cause unconditionally. The friendly codes below deliberately
		// say nothing about what actually failed, and the first time this went
		// wrong in the field the log said nothing either — a 410 that means
		// "expired" and a 410 that means "we asked the wrong URL" look
		// identical to the user, so the operator has to be able to tell.
		s.logger.Error("archive download failed",
			"job", job.ID, "doc", job.DocName, "err", err)
		// The generated download is not kept forever on the APS side. Say so
		// specifically, so the SPA can offer to re-run the job rather than
		// showing a generic upstream failure.
		if isGoneUpstream(err) {
			writeErrorCode(w, http.StatusGone, "archive_expired",
				"this archive has expired; generate it again")
			return
		}
		s.fail(w, r, err)
		return
	}
	defer resp.Body.Close()

	fileType, name := job.format()
	// APS is the authority on what it produced, and it does not always produce
	// what was asked for: a version offering f3z has been seen returning a
	// download whose format.fileType is f3d. Naming that file .f3z would hand
	// the browser a bare design inside a name that promises a zip container,
	// which is a file Fusion may refuse to open. Correct the job too, so the
	// list view and the bell agree with what was actually saved.
	if built := target.FileType; built != "" && built != fileType {
		// Log every source it was decided from, not just the verdict. The
		// first version of this compared only attributes.format.fileType,
		// which was ABSENT for the download that prompted the fix — so the
		// rename silently did not fire and the file went out named .f3z while
		// Autodesk's own web download of the same design gave .f3d.
		s.logger.Warn("archive: naming from the format APS built, not the one requested",
			"job", job.ID, "doc", job.DocName,
			"requested", fileType, "built", built,
			"fromLink", target.LinkType, "declared", target.Declared,
			"fromObjectKey", target.ObjectType)
		name = archiveFileName(job.DocName, job.versionNum(), built)
		job.setFormat(built, name)
	}
	if name == "" {
		name = target.Name
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", "attachment"+dispositionFilename(name))
	// Never cached: the bytes are fine, but the URL is only meaningful while
	// the in-memory job exists, and a cached 200 would outlive it.
	h.Set("Cache-Control", "no-store")
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		h.Set("Content-Length", cl)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

// isGoneUpstream reports whether an APS error is a "that no longer exists"
// answer rather than a transport failure. The api layer returns plain error
// chains carrying the upstream status, so this matches on that text — the same
// approach statusForError already takes.
func isGoneUpstream(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "HTTP 404") || strings.Contains(msg, "HTTP 410")
}

// sanitizeDownloadName reduces a document name to something safe as a file
// name on every supported OS. Document names are user data and may contain
// path separators, control characters or nothing usable at all.
func sanitizeDownloadName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ', r == '.':
			b.WriteRune(r)
		default:
			// Everything else — separators, quotes, controls, and non-ASCII —
			// collapses to an underscore rather than being dropped, so two
			// differently-named documents never produce the same file name.
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), " .")
	if len(out) > 120 {
		out = strings.TrimRight(out[:120], " .")
	}
	return out
}
