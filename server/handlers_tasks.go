package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/tasks"
)

// Task endpoints. Authorization reuses the chat authorizer verbatim — the
// caller's APS project role mapped to capabilities; there is no parallel
// permission system. CapPost ("write" here) covers create and edit — any
// editor edits any task, team-tracker semantics — and CapModerate (or
// being the creator) covers delete. The group-derived fallback applies
// unchanged: roster-unlisted users with project access get write, never
// moderate.
//
// The cross-project /api/tasks/mine listing deliberately skips per-project
// roster checks (N projects would mean N APS calls): tasks assigned to you
// or created by you are always visible to you, notification-style. The
// residual is that a user removed from a project keeps seeing titles of
// their old tasks until those are edited or deleted; every mutation still
// goes through per-project write authz.

// taskMaxBody caps every task request body (same 64 KiB cap as chat/pins).
const taskMaxBody = 64 << 10

// taskCtx is what every task handler resolves first: the caller's token,
// identity, display name, session id (the rate-limit key), and — except
// for /mine — the project in question.
type taskCtx struct {
	projectID string
	token     string
	id        chat.Identity
	name      string
	sessID    string
}

// taskSession gates a task request that has no project scope (/mine).
func (s *Server) taskSession(w http.ResponseWriter, r *http.Request) (taskCtx, bool) {
	if s.tasks == nil {
		writeError(w, http.StatusServiceUnavailable, "task storage is unavailable on this server")
		return taskCtx{}, false
	}
	tok, ok := s.token(r.Context(), w, r)
	if !ok {
		return taskCtx{}, false
	}
	sess, ok := sessionFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return taskCtx{}, false
	}
	name := sess.Profile.Name
	if name == "" {
		name = sess.Profile.Email
	}
	return taskCtx{
		token:  tok,
		id:     chat.Identity{UserID: sess.Profile.Sub, Email: sess.Profile.Email},
		name:   name,
		sessID: sess.ID,
	}, true
}

// taskReq gates a project-scoped task request: store available, session +
// token present, projectId given. Writes the error response itself when
// not ok.
func (s *Server) taskReq(w http.ResponseWriter, r *http.Request) (taskCtx, bool) {
	c, ok := s.taskSession(w, r)
	if !ok {
		return taskCtx{}, false
	}
	c.projectID, ok = reqParam(w, r, "projectId")
	if !ok {
		return taskCtx{}, false
	}
	return c, true
}

// taskCan enforces a capability, writing 403 (or the fetch failure) itself.
func (s *Server) taskCan(ctx context.Context, w http.ResponseWriter, r *http.Request, c taskCtx, cap chat.Capability) bool {
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

// taskError maps store errors onto the uniform envelope. Store sentinel
// texts are our own and safe to echo; raw I/O errors are not (they carry
// filesystem paths), so those log fully and answer generically.
func (s *Server) taskError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, tasks.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, tasks.ErrFutureVersion):
		s.logger.Error("tasks: refusing data from a newer version", "err", err)
		writeError(w, http.StatusServiceUnavailable, "task data on this server was written by a newer version")
	default:
		s.logger.Error("tasks: storage error", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "task storage error")
	}
}

func decodeTaskBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, taskMaxBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// taskUserIn is an assignee in a request body.
type taskUserIn struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (u *taskUserIn) ref() *tasks.UserRef {
	if u == nil {
		return nil
	}
	return &tasks.UserRef{ID: u.ID, Name: u.Name, Email: u.Email}
}

// handleTasksList returns a project's tasks plus the caller's capabilities.
func (s *Server) handleTasksList(w http.ResponseWriter, r *http.Request) {
	c, ok := s.taskReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.taskCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	list, err := s.tasks.List(c.projectID)
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	hubID, projectName, err := s.tasks.ProjectInfo(c.projectID)
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	// Same probe pattern as the chat channel list; both hit the cached roster,
	// so this costs nothing extra. A probe FAILURE is not "no permission",
	// though: silently reporting false would render the whole tab read-only
	// with no error shown, then flip back on the next poll. Fail the request
	// instead, matching taskCan's posture.
	caps := TaskCapsDTO{}
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
	out := TaskListDTO{Tasks: make([]TaskDTO, 0, len(list)), Capabilities: caps}
	for _, t := range list {
		out.Tasks = append(out.Tasks, taskDTO(t, c.projectID, hubID, projectName))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTaskGet returns one task (fls:task card hydration).
func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	c, ok := s.taskReq(w, r)
	if !ok {
		return
	}
	taskID, ok := reqParam(w, r, "taskId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.taskCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	t, err := s.tasks.Get(c.projectID, taskID)
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	hubID, projectName, err := s.tasks.ProjectInfo(c.projectID)
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskDTO(t, c.projectID, hubID, projectName))
}

// handleTaskCreate creates a task. hubId and projectName ride in the body
// (the frontend always has them from nav) so the project's task file can
// self-describe for cross-project listings.
func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.taskReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.taskCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	if !s.taskOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		HubID       string      `json:"hubId"`
		ProjectName string      `json:"projectName"`
		Title       string      `json:"title"`
		Description string      `json:"description"`
		Status      string      `json:"status"`
		Priority    string      `json:"priority"`
		DueDate     string      `json:"dueDate"`
		Assignee    *taskUserIn `json:"assignee"`
		DocRefs     []string    `json:"docRefs"`
		StartDate   string      `json:"startDate"`
		EndDate     string      `json:"endDate"`
		Progress    int         `json:"progress"`
		Milestone   bool        `json:"milestone"`
		DependsOn   []string    `json:"dependsOn"`
		Stage       string      `json:"stage"`
	}
	if !decodeTaskBody(w, r, &in) {
		return
	}
	if in.HubID == "" || in.ProjectName == "" {
		writeError(w, http.StatusBadRequest, "hubId and projectName are required")
		return
	}
	if in.Assignee != nil && in.Assignee.ID == "" {
		writeError(w, http.StatusBadRequest, "assignee id is required")
		return
	}
	t, err := s.tasks.Create(c.projectID, in.HubID, in.ProjectName, tasks.Draft{
		Title:       in.Title,
		Description: in.Description,
		Status:      in.Status,
		Priority:    in.Priority,
		DueDate:     in.DueDate,
		Assignee:    in.Assignee.ref(),
		DocRefs:     in.DocRefs,
		StartDate:   in.StartDate,
		EndDate:     in.EndDate,
		Progress:    in.Progress,
		Milestone:   in.Milestone,
		DependsOn:   in.DependsOn,
		Stage:       in.Stage,
	}, tasks.UserRef{ID: c.id.UserID, Name: c.name, Email: c.id.Email})
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, taskDTO(t, c.projectID, in.HubID, in.ProjectName))
}

// handleTaskUpdate patches a task: absent fields stay untouched; clearing
// the optional assignee/dueDate is explicit (JSON null vs absent is not
// worth distinguishing on the wire).
func (s *Server) handleTaskUpdate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.taskReq(w, r)
	if !ok {
		return
	}
	taskID, ok := reqParam(w, r, "taskId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.taskCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	if !s.taskOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		Title         *string     `json:"title"`
		Description   *string     `json:"description"`
		Status        *string     `json:"status"`
		Priority      *string     `json:"priority"`
		DueDate       *string     `json:"dueDate"`
		Assignee      *taskUserIn `json:"assignee"`
		ClearAssignee bool        `json:"clearAssignee"`
		ClearDueDate  bool        `json:"clearDueDate"`
		DocRefs       *[]string   `json:"docRefs"`
		Rank          *float64    `json:"rank"`
		StartDate     *string     `json:"startDate"`
		EndDate       *string     `json:"endDate"`
		ClearSchedule bool        `json:"clearSchedule"`
		Progress      *int        `json:"progress"`
		Milestone     *bool       `json:"milestone"`
		DependsOn     *[]string   `json:"dependsOn"`
		Stage         *string     `json:"stage"`
	}
	if !decodeTaskBody(w, r, &in) {
		return
	}
	if in.Assignee != nil && in.Assignee.ID == "" {
		writeError(w, http.StatusBadRequest, "assignee id is required")
		return
	}
	t, err := s.tasks.Update(c.projectID, taskID, tasks.Patch{
		Title:         in.Title,
		Description:   in.Description,
		Status:        in.Status,
		Priority:      in.Priority,
		DueDate:       in.DueDate,
		Assignee:      in.Assignee.ref(),
		ClearAssignee: in.ClearAssignee,
		ClearDueDate:  in.ClearDueDate,
		DocRefs:       in.DocRefs,
		Rank:          in.Rank,
		StartDate:     in.StartDate,
		EndDate:       in.EndDate,
		ClearSchedule: in.ClearSchedule,
		Progress:      in.Progress,
		Milestone:     in.Milestone,
		DependsOn:     in.DependsOn,
		Stage:         in.Stage,
	})
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	hubID, projectName, err := s.tasks.ProjectInfo(c.projectID)
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskDTO(t, c.projectID, hubID, projectName))
}

// handleTaskDelete removes a task — moderators or the task's creator, the
// same bar as chat's channel admin (chatModOrCreator).
func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := s.taskReq(w, r)
	if !ok {
		return
	}
	taskID, ok := reqParam(w, r, "taskId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	// Read access first: outsiders get the same 403 they'd get anywhere.
	if !s.taskCan(ctx, w, r, c, chat.CapRead) {
		return
	}
	t, err := s.tasks.Get(c.projectID, taskID)
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	mod, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, chat.CapModerate)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !mod && t.CreatedBy.ID != c.id.UserID {
		writeError(w, http.StatusForbidden, "only the task's creator or a project moderator can delete it")
		return
	}
	if err := s.tasks.Delete(c.projectID, taskID); err != nil {
		s.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleTasksShift moves a set of scheduled tasks by whole days in one
// atomic store rewrite — the Gantt's stage-bar drag. One request instead of
// N PATCHes: the per-session op limiter (burst 10) would 429 a larger stage,
// and a partial failure would tear the stage apart.
func (s *Server) handleTasksShift(w http.ResponseWriter, r *http.Request) {
	c, ok := s.taskReq(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	if !s.taskCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	if !s.taskOpLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}
	var in struct {
		TaskIDs []string `json:"taskIds"`
		Days    int      `json:"days"`
	}
	if !decodeTaskBody(w, r, &in) {
		return
	}
	shifted, err := s.tasks.ShiftTasks(c.projectID, in.TaskIDs, in.Days)
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	hubID, projectName, err := s.tasks.ProjectInfo(c.projectID)
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	out := struct {
		Tasks []TaskDTO `json:"tasks"`
	}{Tasks: make([]TaskDTO, 0, len(shifted))}
	for _, t := range shifted {
		out.Tasks = append(out.Tasks, taskDTO(t, c.projectID, hubID, projectName))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTasksMine lists the caller's tasks across every project on this
// server (assigned to or created by them; see the policy note at the top
// of this file).
func (s *Server) handleTasksMine(w http.ResponseWriter, r *http.Request) {
	c, ok := s.taskSession(w, r)
	if !ok {
		return
	}
	mine, err := s.tasks.Mine(c.id.UserID, c.id.Email)
	if err != nil {
		s.taskError(w, r, err)
		return
	}
	out := MyTasksDTO{Tasks: make([]TaskDTO, 0, len(mine))}
	for _, pt := range mine {
		out.Tasks = append(out.Tasks, taskDTO(pt.Task, pt.ProjectID, pt.HubID, pt.ProjectName))
	}
	writeJSON(w, http.StatusOK, out)
}
