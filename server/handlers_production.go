package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/notifications"
	"github.com/schneik80/fusionlocalserver/production"
)

// Production endpoints — the light MES / job & batch tracker. Authorization
// reuses the chat authorizer verbatim (the caller's APS project role mapped to
// capabilities), exactly like tasks: CapRead to view, CapPost to create/edit
// jobs/steps/edges/placeholders, CapModerate (or being the creator) to delete
// a job. There is no parallel permission system.
//
// Plan documents and fulfillments are version-pinned server-side: the client
// sends a document reference (hubId/itemId/dmProjectId) and the server resolves
// the exact tip version (SnapshotDocVersion) before storing it, so a client can
// never forge a version. Creating a batch freezes a deep copy of the plan
// documents' pinned versions; batches are append-only history.

// prodMaxBody caps every production request body (same 64 KiB cap as tasks).
const prodMaxBody = 64 << 10

// prodCtx is what every production handler resolves first: the caller's token,
// identity, display name, session id (rate-limit key), the SESSION HUB's
// production store (from the requireHub choke point — never from any wire hub
// id), and the project.
type prodCtx struct {
	projectID string
	token     string
	id        chat.Identity
	name      string
	sessID    string

	store *production.Store
	hubID string
	// notif is the session hub's per-user notification store, so a batch
	// change can drop an inbox entry for the job's owner.
	notif *notifications.Store
}

// prodSession gates a production request with no project scope (/mine). The
// hub's store set comes from requireHub, so /mine scopes to the session hub
// structurally.
func (s *Server) prodSession(w http.ResponseWriter, r *http.Request) (prodCtx, bool) {
	set, ok := reqStores(w, r)
	if !ok {
		return prodCtx{}, false
	}
	tok, ok := s.token(r.Context(), w, r)
	if !ok {
		return prodCtx{}, false
	}
	sess, ok := sessionFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return prodCtx{}, false
	}
	name := sess.Profile.Name
	if name == "" {
		name = sess.Profile.Email
	}
	return prodCtx{
		token:  tok,
		id:     chat.Identity{UserID: sess.Profile.Sub, Email: sess.Profile.Email},
		name:   name,
		sessID: sess.ID,
		store:  set.production,
		hubID:  set.hubID,
		notif:  set.notifications,
	}, true
}

// emitProdBatchChange notifies a job's owner that one of its batches changed
// (created, or advanced through the run timeline). Best-effort and after the
// write; the actor is never notified about their own action. No APS call —
// the job owner is captured locally in Job.CreatedBy.
func (s *Server) emitProdBatchChange(c prodCtx, jobID string, b production.Batch) {
	if c.notif == nil {
		return
	}
	job, err := c.store.GetJob(c.projectID, jobID)
	if err != nil {
		return
	}
	owner := job.CreatedBy.ID
	if owner == "" || owner == c.id.UserID {
		return
	}
	hubID, projectName, _ := c.store.ProjectInfo(c.projectID)
	subject := job.Name
	if b.Name != "" {
		subject = job.Name + " · " + b.Name
	}
	if _, _, err := c.notif.Add(owner, notifications.Notification{
		Kind:        notifications.KindProduction,
		HubID:       hubID,
		ProjectID:   c.projectID,
		ProjectName: projectName,
		Actor:       &notifications.UserRef{ID: c.id.UserID, Name: c.name, Email: c.id.Email},
		Subject:     subject,
	}); err != nil {
		s.logger.Error("notifications: production emit failed", "owner", owner, "err", err)
	}
}

// prodReq gates a project-scoped production request.
func (s *Server) prodReq(w http.ResponseWriter, r *http.Request) (prodCtx, bool) {
	c, ok := s.prodSession(w, r)
	if !ok {
		return prodCtx{}, false
	}
	c.projectID, ok = reqParam(w, r, "projectId")
	if !ok {
		return prodCtx{}, false
	}
	return c, true
}

// prodCan enforces a capability, writing 403 (or the fetch failure) itself.
func (s *Server) prodCan(ctx context.Context, w http.ResponseWriter, r *http.Request, c prodCtx, cap chat.Capability) bool {
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

// prodWrite enforces CapPost plus the per-session write rate limit.
func (s *Server) prodWrite(ctx context.Context, w http.ResponseWriter, r *http.Request, c prodCtx) bool {
	if !s.prodCan(ctx, w, r, c, chat.CapPost) {
		return false
	}
	if !s.prodOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return false
	}
	return true
}

// prodError maps store errors onto the uniform envelope.
func (s *Server) prodError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, production.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, production.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, production.ErrFutureVersion):
		s.logger.Error("production: refusing data from a newer version", "err", err)
		writeError(w, http.StatusServiceUnavailable, "production data on this server was written by a newer version")
	default:
		s.logger.Error("production: storage error", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "production storage error")
	}
}

func decodeProdBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, prodMaxBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// prodJobResult writes a single job, re-fetching the project's hub/name.
func (s *Server) prodJobResult(w http.ResponseWriter, r *http.Request, c prodCtx, j production.Job, status int) {
	hubID, projectName, err := c.store.ProjectInfo(c.projectID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	writeJSON(w, status, prodJobDTO(j, c.projectID, hubID, projectName))
}

// ---- jobs ----

// handleProdJobsList returns a project's jobs plus the caller's capabilities.
func (s *Server) handleProdJobsList(w http.ResponseWriter, r *http.Request) {
	c, ok := s.prodReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.prodCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	jobs, err := c.store.ListJobs(c.projectID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	hubID, projectName, err := c.store.ProjectInfo(c.projectID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	// Probe the caller's write capabilities so the SPA can disable affordances
	// rather than let a write bounce off a 403. A probe FAILURE is not "no
	// permission" — silently reporting false would render the whole tab
	// read-only with no error shown, and flip back on the next poll. Fail the
	// request instead, matching prodCan's posture; these hit the same cached
	// roster the CapRead check above just populated, so an error here is real.
	caps := ProdCapsDTO{}
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
	out := ProdJobListDTO{Jobs: make([]ProdJobSummaryDTO, 0, len(jobs)), Capabilities: caps}
	for _, j := range jobs {
		out.Jobs = append(out.Jobs, prodJobSummaryDTO(j, c.projectID, hubID, projectName))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleProdMine lists the caller's jobs across every project on this server —
// jobs they created, or that carry a run they created. Like /api/tasks/mine it
// deliberately skips per-project roster checks (N projects would mean N APS
// calls); see the Store.Mine policy note.
func (s *Server) handleProdMine(w http.ResponseWriter, r *http.Request) {
	c, ok := s.prodSession(w, r)
	if !ok {
		return
	}
	mine, err := c.store.Mine(c.id.UserID, c.id.Email)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	out := MyProductionDTO{Jobs: make([]ProdJobDTO, 0, len(mine))}
	for _, pj := range mine {
		out.Jobs = append(out.Jobs, prodJobDTO(*pj.Job, pj.ProjectID, pj.HubID, pj.ProjectName))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleProdJobGet returns one job with its full graph.
func (s *Server) handleProdJobGet(w http.ResponseWriter, r *http.Request) {
	c, ok := s.prodReq(w, r)
	if !ok {
		return
	}
	jobID, ok := reqParam(w, r, "jobId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.prodCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	j, err := c.store.GetJob(c.projectID, jobID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

// handleProdJobCreate creates a job. hubId and projectName ride in the body so
// the project file self-describes for potential cross-project listings.
func (s *Server) handleProdJobCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.prodReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.prodWrite(ctx, w, r, c) {
		return
	}
	var in struct {
		HubID       string `json:"hubId"`
		ProjectName string `json:"projectName"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	if in.HubID == "" || in.ProjectName == "" {
		writeError(w, http.StatusBadRequest, "hubId and projectName are required")
		return
	}
	if !hubMatches(w, c.hubID, in.HubID) {
		return
	}
	j, err := c.store.CreateJob(c.projectID, in.HubID, in.ProjectName, production.JobDraft{
		Name:        in.Name,
		Description: in.Description,
	}, production.UserRef{ID: c.id.UserID, Name: c.name, Email: c.id.Email})
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, prodJobDTO(j, c.projectID, in.HubID, in.ProjectName))
}

// handleProdJobUpdate patches a job's name/description.
func (s *Server) handleProdJobUpdate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.prodReq(w, r)
	if !ok {
		return
	}
	jobID, ok := reqParam(w, r, "jobId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.prodWrite(ctx, w, r, c) {
		return
	}
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	j, err := c.store.UpdateJob(c.projectID, jobID, production.JobPatch{Name: in.Name, Description: in.Description})
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

// handleProdJobDelete removes a job — moderators or the job's creator, the
// same bar as task delete.
func (s *Server) handleProdJobDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := s.prodReq(w, r)
	if !ok {
		return
	}
	jobID, ok := reqParam(w, r, "jobId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.prodCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	j, err := c.store.GetJob(c.projectID, jobID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	mod, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, chat.CapModerate)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !mod && j.CreatedBy.ID != c.id.UserID {
		writeError(w, http.StatusForbidden, "only the job's creator or a project moderator can delete it")
		return
	}
	if err := c.store.DeleteJob(c.projectID, jobID); err != nil {
		s.prodError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// prodJobScope resolves the caller, requires jobId, and enforces the write
// capability + rate limit — the common preamble for every step/edge/placeholder
// mutation. Returns the context and jobId when ok.
func (s *Server) prodJobScope(w http.ResponseWriter, r *http.Request) (prodCtx, string, context.Context, context.CancelFunc, bool) {
	c, ok := s.prodReq(w, r)
	if !ok {
		return prodCtx{}, "", nil, nil, false
	}
	jobID, ok := reqParam(w, r, "jobId")
	if !ok {
		return prodCtx{}, "", nil, nil, false
	}
	ctx, cancel := s.reqCtx(r)
	if !s.prodWrite(ctx, w, r, c) {
		cancel()
		return prodCtx{}, "", nil, nil, false
	}
	return c, jobID, ctx, cancel, true
}

// ---- steps ----

func (s *Server) handleProdStepCreate(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	var in struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		X           float64 `json:"x"`
		Y           float64 `json:"y"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	j, err := c.store.CreateStep(c.projectID, jobID, production.StepDraft{
		Title:       in.Title,
		Description: in.Description,
		X:           in.X,
		Y:           in.Y,
	})
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

func (s *Server) handleProdStepUpdate(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	stepID, ok := reqParam(w, r, "stepId")
	if !ok {
		return
	}
	var in struct {
		Title       *string  `json:"title"`
		Description *string  `json:"description"`
		X           *float64 `json:"x"`
		Y           *float64 `json:"y"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	patch := production.StepPatch{Title: in.Title, Description: in.Description}
	// x and y move together; require both when either is present.
	if in.X != nil || in.Y != nil {
		if in.X == nil || in.Y == nil {
			writeError(w, http.StatusBadRequest, "x and y must be sent together")
			return
		}
		patch.Position = &production.Position{X: *in.X, Y: *in.Y}
	}
	j, err := c.store.UpdateStep(c.projectID, jobID, stepID, patch)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

func (s *Server) handleProdStepDelete(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	stepID, ok := reqParam(w, r, "stepId")
	if !ok {
		return
	}
	j, err := c.store.DeleteStep(c.projectID, jobID, stepID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

// ---- edges ----

func (s *Server) handleProdEdgeCreate(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	j, err := c.store.AddEdge(c.projectID, jobID, in.From, in.To)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

func (s *Server) handleProdEdgeDelete(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	edgeID, ok := reqParam(w, r, "edgeId")
	if !ok {
		return
	}
	j, err := c.store.DeleteEdge(c.projectID, jobID, edgeID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

// ---- placeholders ----

func (s *Server) handleProdPlaceholderCreate(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	stepID, ok := reqParam(w, r, "stepId")
	if !ok {
		return
	}
	var in struct {
		Label    string `json:"label"`
		Kind     string `json:"kind"`
		Required bool   `json:"required"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	j, err := c.store.AddPlaceholder(c.projectID, jobID, stepID, production.PlaceholderDraft{
		Label:    in.Label,
		Kind:     in.Kind,
		Required: in.Required,
	})
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

func (s *Server) handleProdPlaceholderUpdate(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	stepID, ok := reqParam(w, r, "stepId")
	if !ok {
		return
	}
	placeholderID, ok := reqParam(w, r, "placeholderId")
	if !ok {
		return
	}
	var in struct {
		Label    *string `json:"label"`
		Kind     *string `json:"kind"`
		Required *bool   `json:"required"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	j, err := c.store.UpdatePlaceholder(c.projectID, jobID, stepID, placeholderID, production.PlaceholderPatch{
		Label:    in.Label,
		Kind:     in.Kind,
		Required: in.Required,
	})
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

func (s *Server) handleProdPlaceholderDelete(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	stepID, ok := reqParam(w, r, "stepId")
	if !ok {
		return
	}
	placeholderID, ok := reqParam(w, r, "placeholderId")
	if !ok {
		return
	}
	j, err := c.store.RemovePlaceholder(c.projectID, jobID, stepID, placeholderID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

// ---- document snapshots (shared by plan docs and fulfillments) ----

// prodDocIn is the client's document reference. The server resolves the version
// pin from it (SnapshotDocVersion). VersionID is optional and only honored when
// it belongs to the item's lineage — the upload path passes the version urn the
// upload just created so the pin records THAT version, not whatever the tip is
// by the time the request lands; a foreign version urn is rejected.
type prodDocIn struct {
	HubID       string `json:"hubId"`
	ItemID      string `json:"itemId"`
	DMProjectID string `json:"dmProjectId"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	VersionID   string `json:"versionId"`
}

// resolveSnapshot turns a client document reference into a version-pinned
// DocSnapshot, writing the error response itself when it can't.
func (s *Server) resolveSnapshot(ctx context.Context, w http.ResponseWriter, r *http.Request, c prodCtx, in prodDocIn) (production.DocSnapshot, bool) {
	if in.HubID == "" || in.ItemID == "" || in.DMProjectID == "" {
		writeError(w, http.StatusBadRequest, "hubId, itemId and dmProjectId are required")
		return production.DocSnapshot{}, false
	}
	// The referenced document must live in the session hub — checked before
	// any APS call so a foreign-hub reference costs nothing upstream.
	if !hubMatches(w, c.hubID, in.HubID) {
		return production.DocSnapshot{}, false
	}
	snap, err := api.SnapshotDocVersion(ctx, c.token, in.HubID, in.DMProjectID, in.ItemID, in.VersionID)
	if err != nil {
		s.fail(w, r, err)
		return production.DocSnapshot{}, false
	}
	return production.DocSnapshot{
		HubID:                  in.HubID,
		ItemID:                 in.ItemID,
		Name:                   in.Name,
		Kind:                   in.Kind,
		VersionID:              snap.VersionID,
		VersionNumber:          snap.VersionNumber,
		RootComponentVersionID: snap.RootComponentVersionID,
		DMProjectID:            in.DMProjectID,
	}, true
}

func (s *Server) prodUser(c prodCtx) production.UserRef {
	return production.UserRef{ID: c.id.UserID, Name: c.name, Email: c.id.Email}
}

func (s *Server) prodBatchResult(w http.ResponseWriter, b production.Batch, status int) {
	writeJSON(w, status, prodBatchDTO(&b))
}

// ---- plan documents ----

func (s *Server) handleProdPlanDocCreate(w http.ResponseWriter, r *http.Request) {
	c, jobID, ctx, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	stepID, ok := reqParam(w, r, "stepId")
	if !ok {
		return
	}
	var in prodDocIn
	if !decodeProdBody(w, r, &in) {
		return
	}
	doc, ok := s.resolveSnapshot(ctx, w, r, c, in)
	if !ok {
		return
	}
	j, err := c.store.AttachPlanDoc(c.projectID, jobID, stepID, doc, s.prodUser(c))
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

func (s *Server) handleProdPlanDocDelete(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	stepID, ok := reqParam(w, r, "stepId")
	if !ok {
		return
	}
	planDocID, ok := reqParam(w, r, "planDocId")
	if !ok {
		return
	}
	j, err := c.store.RemovePlanDoc(c.projectID, jobID, stepID, planDocID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodJobResult(w, r, c, j, http.StatusOK)
}

// ---- batches ----

func (s *Server) handleProdBatchCreate(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	var in struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		RunAt string `json:"runAt"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	var runAt time.Time
	if in.RunAt != "" {
		t, err := time.Parse(time.RFC3339, in.RunAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "runAt must be an RFC3339 timestamp")
			return
		}
		runAt = t
	}
	b, err := c.store.CreateBatch(c.projectID, jobID, production.BatchDraft{
		Name:  in.Name,
		Kind:  in.Kind,
		RunAt: runAt,
	}, s.prodUser(c))
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	// A new run on someone else's job notifies its owner.
	s.emitProdBatchChange(c, jobID, b)
	s.prodBatchResult(w, b, http.StatusCreated)
}

func (s *Server) handleProdBatchGet(w http.ResponseWriter, r *http.Request) {
	c, ok := s.prodReq(w, r)
	if !ok {
		return
	}
	jobID, ok := reqParam(w, r, "jobId")
	if !ok {
		return
	}
	batchID, ok := reqParam(w, r, "batchId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.prodCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	b, err := c.store.GetBatch(c.projectID, jobID, batchID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodBatchResult(w, b, http.StatusOK)
}

func (s *Server) handleProdBatchUpdate(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	batchID, ok := reqParam(w, r, "batchId")
	if !ok {
		return
	}
	var in struct {
		Name   *string `json:"name"`
		Kind   *string `json:"kind"`
		Status *string `json:"status"`
		RunAt  *string `json:"runAt"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	patch := production.BatchPatch{Name: in.Name, Kind: in.Kind, Status: in.Status}
	if in.RunAt != nil {
		t, err := time.Parse(time.RFC3339, *in.RunAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "runAt must be an RFC3339 timestamp")
			return
		}
		patch.RunAt = &t
	}
	b, err := c.store.UpdateBatch(c.projectID, jobID, batchID, patch)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	// A batch advancing through the run timeline (a status change) notifies
	// the job's owner; renaming or rescheduling it does not.
	if in.Status != nil {
		s.emitProdBatchChange(c, jobID, b)
	}
	s.prodBatchResult(w, b, http.StatusOK)
}

// handleProdBatchDelete removes a batch — moderators or the batch's creator,
// the same bar as job delete.
func (s *Server) handleProdBatchDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := s.prodReq(w, r)
	if !ok {
		return
	}
	jobID, ok := reqParam(w, r, "jobId")
	if !ok {
		return
	}
	batchID, ok := reqParam(w, r, "batchId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.prodCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	b, err := c.store.GetBatch(c.projectID, jobID, batchID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	mod, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, chat.CapModerate)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !mod && b.CreatedBy.ID != c.id.UserID {
		writeError(w, http.StatusForbidden, "only the batch's creator or a project moderator can delete it")
		return
	}
	if err := c.store.DeleteBatch(c.projectID, jobID, batchID); err != nil {
		s.prodError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ---- fulfillments ----

func (s *Server) handleProdFulfillmentCreate(w http.ResponseWriter, r *http.Request) {
	c, jobID, ctx, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	batchID, ok := reqParam(w, r, "batchId")
	if !ok {
		return
	}
	var in struct {
		StepID        string `json:"stepId"`
		PlaceholderID string `json:"placeholderId"`
		HubID         string `json:"hubId"`
		ItemID        string `json:"itemId"`
		DMProjectID   string `json:"dmProjectId"`
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		VersionID     string `json:"versionId"`
		Source        string `json:"source"`
		IsAsRun       bool   `json:"isAsRun"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	doc, ok := s.resolveSnapshot(ctx, w, r, c, prodDocIn{
		HubID:       in.HubID,
		ItemID:      in.ItemID,
		DMProjectID: in.DMProjectID,
		Name:        in.Name,
		Kind:        in.Kind,
		VersionID:   in.VersionID,
	})
	if !ok {
		return
	}
	b, err := c.store.AddFulfillment(c.projectID, jobID, batchID, production.FulfillmentDraft{
		StepID:        in.StepID,
		PlaceholderID: in.PlaceholderID,
		Doc:           doc,
		Source:        in.Source,
		IsAsRun:       in.IsAsRun,
	}, s.prodUser(c))
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodBatchResult(w, b, http.StatusOK)
}

func (s *Server) handleProdFulfillmentDelete(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	batchID, ok := reqParam(w, r, "batchId")
	if !ok {
		return
	}
	fulfillmentID, ok := reqParam(w, r, "fulfillmentId")
	if !ok {
		return
	}
	b, err := c.store.RemoveFulfillment(c.projectID, jobID, batchID, fulfillmentID)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodBatchResult(w, b, http.StatusOK)
}

// ---- batch references (related tasks / documents) ----

func (s *Server) handleProdBatchRefAdd(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	batchID, ok := reqParam(w, r, "batchId")
	if !ok {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !decodeProdBody(w, r, &in) {
		return
	}
	b, err := c.store.AddBatchRef(c.projectID, jobID, batchID, in.Token)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodBatchResult(w, b, http.StatusOK)
}

func (s *Server) handleProdBatchRefDelete(w http.ResponseWriter, r *http.Request) {
	c, jobID, _, cancel, ok := s.prodJobScope(w, r)
	if !ok {
		return
	}
	defer cancel()
	batchID, ok := reqParam(w, r, "batchId")
	if !ok {
		return
	}
	token, ok := reqParam(w, r, "token")
	if !ok {
		return
	}
	b, err := c.store.RemoveBatchRef(c.projectID, jobID, batchID, token)
	if err != nil {
		s.prodError(w, r, err)
		return
	}
	s.prodBatchResult(w, b, http.StatusOK)
}
