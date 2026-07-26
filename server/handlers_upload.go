package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"time"
)

// maxUploadBytes caps one uploaded file. It bounds the temp spool on disk, not
// memory (the multipart body streams straight to the file).
const maxUploadBytes = 10 << 30 // 10 GiB

// reqSession resolves the caller's session. Handlers that start background work
// need the *Session itself (for token refresh across the job's lifetime), not
// just the request-scoped token requireAuth injected.
func (s *Server) reqSession(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return nil, false
	}
	sess, ok := s.sessions.Get(c.Value)
	if !ok {
		writeError(w, http.StatusUnauthorized, "session expired or unknown")
		return nil, false
	}
	return sess, true
}

// handleUploadCreate accepts one file and starts a background upload job for
// it. The multipart fields (hubId, dmProjectId, folderPath, plus the projectId/
// folderId cache echoes) must precede the file part so the target is known
// before the bytes stream in; the file is spooled to a temp file (never RAM)
// and the response returns as soon as the spool completes — the APS transfer
// then proceeds asynchronously and is observed via GET /api/uploads.
// POST /api/uploads  (multipart: hubId, dmProjectId, projectId?, folderId?, folderPath, ensureFolders?, file)
func (s *Server) handleUploadCreate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload")
		return
	}

	fields := map[string]string{}
	var (
		fileName string
		tmpPath  string
		size     int64
	)
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid upload")
			return
		}
		if part.FormName() != "file" {
			b, rerr := io.ReadAll(io.LimitReader(part, 1<<20))
			if rerr != nil {
				writeError(w, http.StatusBadRequest, "invalid upload")
				return
			}
			fields[part.FormName()] = string(b)
			continue
		}
		fileName = path.Base(part.FileName())
		tmp, terr := os.CreateTemp("", "fls-upload-*")
		if terr != nil {
			s.fail(w, r, terr)
			return
		}
		size, err = io.Copy(tmp, io.LimitReader(part, maxUploadBytes+1))
		tmp.Close()
		if err != nil {
			os.Remove(tmp.Name())
			writeError(w, http.StatusBadRequest, "upload interrupted")
			return
		}
		if size > maxUploadBytes {
			os.Remove(tmp.Name())
			writeError(w, http.StatusRequestEntityTooLarge, "file too large to upload")
			return
		}
		tmpPath = tmp.Name()
		break // file is the final part; the target fields are already parsed
	}

	hubID, dmProjectID := fields["hubId"], fields["dmProjectId"]
	if hubID == "" || dmProjectID == "" || tmpPath == "" {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
		writeError(w, http.StatusBadRequest, "hubId, dmProjectId and file are required")
		return
	}
	// The upload target must be the session hub (multipart fields are not
	// covered by requireHub's central query-param check).
	set, ok := reqStores(w, r)
	if !ok {
		os.Remove(tmpPath)
		return
	}
	if !hubMatches(w, set.hubID, hubID) {
		os.Remove(tmpPath)
		return
	}
	if fileName == "" || fileName == "." || fileName == "/" {
		os.Remove(tmpPath)
		writeError(w, http.StatusBadRequest, "file has no usable name")
		return
	}
	folderPath := []string{}
	if raw := fields["folderPath"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &folderPath); err != nil {
			os.Remove(tmpPath)
			writeError(w, http.StatusBadRequest, "folderPath must be a JSON array of folder names")
			return
		}
	}

	id, err := randToken(16)
	if err != nil {
		os.Remove(tmpPath)
		s.fail(w, r, err)
		return
	}
	job := &uploadJob{
		ID:          id,
		SessionID:   sess.ID,
		FileName:    fileName,
		Size:        size,
		HubID:       hubID,
		DMProjectID: dmProjectID,
		FolderPath:  folderPath,
		// Opt-in: create the folderPath if it doesn't exist yet (Production
		// files its uploads under Jobs/<job>/…). Absent → must-exist.
		EnsureFolders: fields["ensureFolders"] == "true",
		ProjectID:     fields["projectId"],
		FolderID:      fields["folderId"],
		CreatedAt:     time.Now(),
		tmpPath:       tmpPath,
		status:        uploadQueued,
	}
	s.uploads.add(job)
	go s.runUpload(job, sess)
	writeJSON(w, http.StatusAccepted, uploadJobDTO(job))
}

// handleUploadList returns the session's upload jobs (all states) in
// submission order.
// GET /api/uploads
func (s *Server) handleUploadList(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, uploadJobDTOs(s.uploads.listFor(sess.ID)))
}

// handleUploadCancel stops one job (queued or in flight) and returns the
// refreshed list. Canceling a job whose item/version creation already landed
// is a no-op — first terminal state wins.
// POST /api/uploads/cancel?id=<job id>
func (s *Server) handleUploadCancel(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	id, ok := reqParam(w, r, "id")
	if !ok {
		return
	}
	if job, ok := s.uploads.get(id, sess.ID); ok {
		job.cancel()
	}
	writeJSON(w, http.StatusOK, uploadJobDTOs(s.uploads.listFor(sess.ID)))
}

// handleUploadDismiss clears finished jobs from the list — the one named by id,
// or all of them when id is omitted — and returns the refreshed list.
// POST /api/uploads/dismiss?id=<job id, optional>
func (s *Server) handleUploadDismiss(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.reqSession(w, r)
	if !ok {
		return
	}
	s.uploads.dismiss(r.URL.Query().Get("id"), sess.ID)
	writeJSON(w, http.StatusOK, uploadJobDTOs(s.uploads.listFor(sess.ID)))
}
