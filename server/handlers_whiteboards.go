package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

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
	doc, err := c.store.Document(c.projectID, boardID)
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if doc == nil {
		w.Write([]byte("null"))
		return
	}
	w.Write(doc) // stored verbatim; the server never reinterprets a document
}

// handleWhiteboardDocPut stores a board's tldraw document (the canvas
// autosaves). The body is passed through opaquely — the store validates it is
// JSON and within the size cap, but nothing here parses tldraw's schema.
func (s *Server) handleWhiteboardDocPut(w http.ResponseWriter, r *http.Request) {
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
	doc, err := io.ReadAll(http.MaxBytesReader(w, r.Body, whiteboardMaxDoc))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "whiteboard document is too large")
		return
	}
	b, err := c.store.SaveSnapshot(c.projectID, boardID, doc, s.whiteboardUser(c))
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	s.whiteboardResult(w, r, c, b, http.StatusOK)
}
