package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// Whiteboard endpoints. Authorization reuses the chat authorizer verbatim (the
// caller's APS project role mapped to capabilities), exactly like tasks and
// production: CapRead to view, CapPost to create/rename/draw, CapModerate (or
// being the creator) to delete.
//
// The document endpoints are the odd ones out in this codebase: a tldraw
// document is megabytes of shapes, not a small JSON form, so they carry their
// own much larger body cap and pass the bytes through opaquely — the server
// stores and returns the document without parsing it beyond a validity check.

const (
	// whiteboardMaxBody caps the metadata requests (create/rename), matching
	// the 64 KiB used across the other features.
	whiteboardMaxBody = 64 << 10
	// whiteboardMaxDoc caps a document PUT. It mirrors the store's own limit;
	// the reader cap fails fast on the wire, the store's check is the one that
	// can't be bypassed.
	whiteboardMaxDoc = whiteboards.MaxSnapshotBytes
)

// codeWhiteboardStale is the machine code for a save based on a revision the
// board has already moved past. The client stops autosaving and offers reload
// or overwrite; it must never retry, which would discard the other save.
const codeWhiteboardStale = "whiteboard_stale"

// whiteboardCtx carries the caller plus the SESSION HUB's whiteboard store
// (from the requireHub choke point — never from any wire hub id).
type whiteboardCtx struct {
	projectID string
	token     string
	id        chat.Identity
	name      string
	sessID    string

	store *whiteboards.Store
	hubID string
}

func (s *Server) whiteboardSession(w http.ResponseWriter, r *http.Request) (whiteboardCtx, bool) {
	set, ok := reqStores(w, r)
	if !ok {
		return whiteboardCtx{}, false
	}
	tok, ok := s.token(r.Context(), w, r)
	if !ok {
		return whiteboardCtx{}, false
	}
	sess, ok := sessionFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return whiteboardCtx{}, false
	}
	name := sess.Profile.Name
	if name == "" {
		name = sess.Profile.Email
	}
	return whiteboardCtx{
		token:  tok,
		id:     chat.Identity{UserID: sess.Profile.Sub, Email: sess.Profile.Email},
		name:   name,
		sessID: sess.ID,
		store:  set.whiteboards,
		hubID:  set.hubID,
	}, true
}

func (s *Server) whiteboardReq(w http.ResponseWriter, r *http.Request) (whiteboardCtx, bool) {
	c, ok := s.whiteboardSession(w, r)
	if !ok {
		return whiteboardCtx{}, false
	}
	c.projectID, ok = reqParam(w, r, "projectId")
	if !ok {
		return whiteboardCtx{}, false
	}
	return c, true
}

func (s *Server) whiteboardCan(ctx context.Context, w http.ResponseWriter, r *http.Request, c whiteboardCtx, cap chat.Capability) bool {
	ok, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, cap)
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, safeErrorMessage(http.StatusForbidden))
		return false
	}
	return true
}

func (s *Server) whiteboardError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, whiteboards.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, whiteboards.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, whiteboards.ErrConflict):
		// The message names revisions, which mean nothing to a user; the code is
		// what the client renders (web/src/i18n/apiError.ts), and what tells it
		// to stop autosaving rather than retry.
		writeErrorCode(w, http.StatusConflict, codeWhiteboardStale,
			"this whiteboard changed since you opened it")
	case errors.Is(err, whiteboards.ErrFutureVersion):
		s.logger.Error("whiteboards: refusing data from a newer version", "err", err)
		writeError(w, http.StatusServiceUnavailable, "whiteboard data on this server was written by a newer version")
	default:
		s.logger.Error("whiteboards: storage error", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "whiteboard storage error")
	}
}

func (s *Server) whiteboardUser(c whiteboardCtx) whiteboards.UserRef {
	return whiteboards.UserRef{ID: c.id.UserID, Name: c.name, Email: c.id.Email}
}

func (s *Server) whiteboardResult(w http.ResponseWriter, r *http.Request, c whiteboardCtx, b whiteboards.Board, status int) {
	hubID, projectName, err := c.store.ProjectInfo(c.projectID)
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	writeJSON(w, status, whiteboardDTO(b, c.projectID, hubID, projectName))
}

// handleWhiteboardsList returns a project's boards plus the caller's caps.
func (s *Server) handleWhiteboardsList(w http.ResponseWriter, r *http.Request) {
	c, ok := s.whiteboardReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.whiteboardCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	list, err := c.store.List(c.projectID)
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	hubID, projectName, err := c.store.ProjectInfo(c.projectID)
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	// A failed probe is not "no permission" — see handleProdJobsList.
	caps := WhiteboardCapsDTO{}
	write, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, chat.CapPost)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	caps.Write = write
	moderate, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, chat.CapModerate)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	caps.Moderate = moderate

	out := WhiteboardListDTO{Whiteboards: make([]WhiteboardDTO, 0, len(list)), Capabilities: caps}
	for _, b := range list {
		out.Whiteboards = append(out.Whiteboards, whiteboardDTO(b, c.projectID, hubID, projectName))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWhiteboardCreate creates a board. hubId/projectName ride in the body so
// the project file self-describes for cross-project listings.
func (s *Server) handleWhiteboardCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.whiteboardReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.whiteboardCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	if !s.whiteboardOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		HubID       string `json:"hubId"`
		ProjectName string `json:"projectName"`
		Name        string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, whiteboardMaxBody)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.HubID == "" || in.ProjectName == "" {
		writeError(w, http.StatusBadRequest, "hubId and projectName are required")
		return
	}
	if !hubMatches(w, c.hubID, in.HubID) {
		return
	}
	b, err := c.store.Create(c.projectID, in.HubID, in.ProjectName, whiteboards.Draft{Name: in.Name}, s.whiteboardUser(c))
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, whiteboardDTO(b, c.projectID, in.HubID, in.ProjectName))
}

// handleWhiteboardUpdate renames a board.
func (s *Server) handleWhiteboardUpdate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.whiteboardReq(w, r)
	if !ok {
		return
	}
	boardID, ok := reqParam(w, r, "boardId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.whiteboardCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	if !s.whiteboardOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		Name *string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, whiteboardMaxBody)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	b, err := c.store.Update(c.projectID, boardID, whiteboards.Patch{Name: in.Name})
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	s.whiteboardResult(w, r, c, b, http.StatusOK)
}

// handleWhiteboardDelete removes a board and its document — moderators or the
// board's creator, the same bar as task/job delete.
func (s *Server) handleWhiteboardDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := s.whiteboardReq(w, r)
	if !ok {
		return
	}
	boardID, ok := reqParam(w, r, "boardId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.whiteboardCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	b, err := c.store.Get(c.projectID, boardID)
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	mod, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, chat.CapModerate)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !mod && b.CreatedBy.ID != c.id.UserID {
		writeError(w, http.StatusForbidden, "only the whiteboard's creator or a project moderator can delete it")
		return
	}
	if err := c.store.Delete(c.projectID, boardID); err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleWhiteboardDocGet streams a board's stored tldraw document. An unsaved
// board answers "null" — an empty canvas, which the client opens fresh.
//
// The body stays a verbatim tldraw snapshot, so the revision rides in an ETag
// header rather than wrapping the document in an envelope: the client hands the
// body straight to loadSnapshot, and the server keeps its promise never to
// reinterpret a document.
func (s *Server) handleWhiteboardDocGet(w http.ResponseWriter, r *http.Request) {
	c, ok := s.whiteboardReq(w, r)
	if !ok {
		return
	}
	boardID, ok := reqParam(w, r, "boardId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.whiteboardCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	// Through the room when one is live, so a client joining a board people are
	// already editing gets what is on their screens rather than the last
	// debounced write — which would leave it applying patches to a document
	// that is seconds behind.
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	doc, rev, err := set.whiteboardRooms.Snapshot(c.projectID, boardID)
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("ETag", whiteboardETag(rev))
	if doc == nil {
		w.Write([]byte("null"))
		return
	}
	w.Write(doc) // stored verbatim; the server never reinterprets a document
}

// whiteboardETag formats a document revision as an entity tag. Weak, because it
// tags the revision rather than the exact bytes — two saves that happened to
// produce identical documents are still distinct revisions, which is what the
// conflict check needs.
func whiteboardETag(rev int64) string { return `W/"` + strconv.FormatInt(rev, 10) + `"` }

// handleWhiteboardDocPut stores a board's tldraw document (the canvas
// autosaves). The body is passed through opaquely — the store validates it is
// JSON and within the size cap, but nothing here parses tldraw's schema.
//
// baseRev is the revision the client loaded (the ETag from the GET). A save
// based on a revision the board has moved past is refused with 409 — the guard
// against two people on one board overwriting each other. force=1 is the user's
// acknowledged "overwrite anyway" after being shown that conflict.
//
// Unlike the metadata routes this one is rate limited on its own budget: an
// autosaving canvas legitimately saves far more often than a person renames a
// board, but a 24 MiB body left unmetered is a free way to thrash the disk.
func (s *Server) handleWhiteboardDocPut(w http.ResponseWriter, r *http.Request) {
	c, ok := s.whiteboardReq(w, r)
	if !ok {
		return
	}
	boardID, ok := reqParam(w, r, "boardId")
	if !ok {
		return
	}
	baseRev, ok := reqRevParam(w, r, "baseRev")
	if !ok {
		return
	}
	force := r.URL.Query().Get("force") == "1"
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.whiteboardCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	if !s.whiteboardDocLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, "too many whiteboard saves; slow down")
		return
	}
	doc, err := io.ReadAll(http.MaxBytesReader(w, r.Body, whiteboardMaxDoc))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "whiteboard document is too large")
		return
	}
	b, err := c.store.SaveSnapshot(c.projectID, boardID, doc, s.whiteboardUser(c), baseRev, force)
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	// If the board is being edited live, the room — not the file — is the
	// authority, so a whole-document write has to land there too. Otherwise the
	// next patch would be applied to a record map that still held the old
	// document, and the room would quietly write it back over this one.
	if set, ok := storesFromCtx(r.Context()); ok {
		if rev, live, rerr := set.whiteboardRooms.Replace(c.projectID, boardID, doc, s.whiteboardUser(c)); rerr == nil && live {
			b.DocRev = rev
		}
		// Tell whoever else has this board open. For a live board this is their
		// cue to re-read: a wholesale replacement is not expressible as a patch.
		s.publishDocChanged(set, c, boardID, b.DocRev)
	}
	s.whiteboardResult(w, r, c, b, http.StatusOK)
}

// reqRevParam reads a required non-negative integer query parameter. A save
// with no revision is rejected rather than treated as unconditional: silently
// saving without the guard is the very bug this endpoint now prevents.
func reqRevParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw, ok := reqParam(w, r, name)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return v, true
}
