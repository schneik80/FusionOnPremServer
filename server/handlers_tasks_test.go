package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/internal/testutil"
)

const (
	taskTestProject  = "urn:project:tasks-1"
	taskTestProject2 = "urn:project:tasks-2"
)

// newTaskTestServer builds a Server with a real tasks store over a TempDir
// and the shared chat authorizer pointed at a fake APS roster (the chat
// fixture's cast: one VIEWER, one EDITOR, one MANAGER).
func newTaskTestServer(t *testing.T) *Server {
	t.Helper()
	roster := &fakeRoster{rows: []map[string]any{
		rosterRow("u-viewer", "viewer@x.io", "VIEWER"),
		rosterRow("u-editor", "editor@x.io", "EDITOR"),
		rosterRow("u-manager", "manager@x.io", "MANAGER"),
	}}
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: map[string]any{
			"project": map[string]any{
				"folderLevelProjectMembers": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results":    roster.snapshot(),
				},
			},
		}}
	})
	restore := api.SetGraphqlEndpointForTesting(srv.URL)
	t.Cleanup(restore)

	authz := chat.NewAuthorizer()
	return &Server{
		logger:    quietLogger(),
		clientID:  "test-client",
		sessions:  NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger()),
		pending:   NewPendingStore(pendingTTL),
		hubs:      testHubStores(t, authz),
		chatAuthz: authz,
		taskOpLim: chat.NewLimiter(50, 100), // roomy: tests mutate rapidly
	}
}

func taskURL(path string, kv ...string) string {
	q := "projectId=" + taskTestProject
	for i := 0; i+1 < len(kv); i += 2 {
		q += "&" + kv[i] + "=" + kv[i+1]
	}
	return path + "?" + q
}

func taskCreateBody(title string) map[string]any {
	return map[string]any{"hubId": "hub-1", "projectName": "Test Project", "title": title}
}

func TestTasks_RequiresSessionAndRole(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks"), nil, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", code)
	}

	outsider := login(t, s, "u-outsider", "Out Sider", "out@x.io")
	// The fake roster answers for any project, so an unlisted user is
	// "roster readable but not listed" → group-derived write. To get a
	// denial we need someone the roster lists as suspended.
	viewer := login(t, s, "u-viewer", "Vera Viewer", "viewer@x.io")

	// Viewer can read but not create.
	var list TaskListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks"), viewer, nil, &list); code != http.StatusOK {
		t.Fatalf("viewer list status = %d", code)
	}
	if list.Tasks == nil || list.Capabilities.Write {
		t.Fatalf("viewer list = %+v; want empty non-nil tasks and write=false", list)
	}
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), viewer, taskCreateBody("nope"), nil); code != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", code)
	}
	_ = outsider
}

func TestTasks_CreatePatchDeleteRoundTrip(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")
	manager := login(t, s, "u-manager", "Man Ager", "manager@x.io")

	// Create.
	body := taskCreateBody("Ship the tasks feature")
	body["assignee"] = map[string]any{"id": "u-editor", "name": "Ed Editor", "email": "editor@x.io"}
	body["docRefs"] = []string{"fls:doc?hubId=h&itemId=i1"}
	var created TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, body, &created); code != http.StatusCreated {
		t.Fatalf("create status = %d", code)
	}
	if created.ID != "t1" || created.Num != 1 || created.Status != "todo" || created.Priority != "medium" {
		t.Fatalf("created = %+v", created)
	}
	if created.HubID != "hub-1" || created.ProjectName != "Test Project" || created.ProjectID != taskTestProject {
		t.Fatalf("project annotation wrong: %+v", created)
	}
	if created.CreatedBy.ID != "u-editor" || created.CreatedBy.Name != "Ed Editor" {
		t.Fatalf("createdBy = %+v", created.CreatedBy)
	}

	// Validation bounces.
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, taskCreateBody(""), nil); code != http.StatusBadRequest {
		t.Fatalf("empty-title create status = %d, want 400", code)
	}
	noHub := map[string]any{"title": "x"}
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, noHub, nil); code != http.StatusBadRequest {
		t.Fatalf("missing-hub create status = %d, want 400", code)
	}

	// Patch: status change + clear assignee.
	patch := map[string]any{"status": "done", "clearAssignee": true}
	var updated TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPatch, taskURL("/api/tasks", "taskId", created.ID), editor, patch, &updated); code != http.StatusOK {
		t.Fatalf("patch status = %d", code)
	}
	if updated.Status != "done" || updated.Assignee != nil {
		t.Fatalf("patch not applied: %+v", updated)
	}

	// Unknown task → 404.
	if code := chatDo(t, ts.URL, http.MethodPatch, taskURL("/api/tasks", "taskId", "t99"), editor, patch, nil); code != http.StatusNotFound {
		t.Fatalf("patch missing status = %d, want 404", code)
	}
	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks/get", "taskId", "t99"), editor, nil, nil); code != http.StatusNotFound {
		t.Fatalf("get missing status = %d, want 404", code)
	}

	// Get.
	var got TaskDTO
	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks/get", "taskId", created.ID), editor, nil, &got); code != http.StatusOK {
		t.Fatalf("get status = %d", code)
	}
	if got.Title != "Ship the tasks feature" || len(got.DocRefs) != 1 {
		t.Fatalf("get = %+v", got)
	}

	// Delete: a second editor's task can't be deleted by a non-creator
	// editor, but a manager can.
	var second TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), manager, taskCreateBody("Manager's task"), &second); code != http.StatusCreated {
		t.Fatalf("manager create status = %d", code)
	}
	if code := chatDo(t, ts.URL, http.MethodDelete, taskURL("/api/tasks", "taskId", second.ID), editor, nil, nil); code != http.StatusForbidden {
		t.Fatalf("non-creator delete status = %d, want 403", code)
	}
	if code := chatDo(t, ts.URL, http.MethodDelete, taskURL("/api/tasks", "taskId", created.ID), manager, nil, nil); code != http.StatusOK {
		t.Fatalf("moderator delete status = %d", code)
	}
	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks/get", "taskId", created.ID), editor, nil, nil); code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", code)
	}
}

func TestTasks_Mine(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")
	manager := login(t, s, "u-manager", "Man Ager", "manager@x.io")

	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, taskCreateBody("mine by creation"), nil); code != http.StatusCreated {
		t.Fatal("create 1")
	}
	assigned := taskCreateBody("assigned to editor")
	assigned["assignee"] = map[string]any{"id": "u-editor", "email": "editor@x.io"}
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), manager, assigned, nil); code != http.StatusCreated {
		t.Fatal("create 2")
	}
	// A second project, unrelated to the editor.
	other := "/api/tasks?projectId=" + taskTestProject2
	if code := chatDo(t, ts.URL, http.MethodPost, other, manager, taskCreateBody("not editor's"), nil); code != http.StatusCreated {
		t.Fatal("create 3")
	}

	var mine MyTasksDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/tasks/mine", editor, nil, &mine); code != http.StatusOK {
		t.Fatalf("mine status = %d", code)
	}
	if len(mine.Tasks) != 2 {
		t.Fatalf("mine len = %d, want 2: %+v", len(mine.Tasks), mine.Tasks)
	}
	for _, task := range mine.Tasks {
		if task.ProjectID != taskTestProject || task.HubID != "hub-1" || task.ProjectName != "Test Project" {
			t.Fatalf("mine annotation wrong: %+v", task)
		}
	}

	var mgrMine MyTasksDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/tasks/mine", manager, nil, &mgrMine); code != http.StatusOK {
		t.Fatalf("manager mine status = %d", code)
	}
	if len(mgrMine.Tasks) != 2 {
		t.Fatalf("manager mine len = %d, want 2 (created two)", len(mgrMine.Tasks))
	}
}

func TestTasks_StoreUnavailable(t *testing.T) {
	s := newTaskTestServer(t)
	s.hubs = nil // config dir unavailable at startup — no hub store sets
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks"), editor, nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("list status = %d, want 503", code)
	}
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/tasks/mine", editor, nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("mine status = %d, want 503", code)
	}
}

func TestTasks_ScheduleRoundTrip(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	// Create with a full schedule.
	body := taskCreateBody("planned work")
	body["startDate"] = "2026-08-03"
	body["endDate"] = "2026-08-07"
	body["progress"] = 40
	body["stage"] = "Design"
	var created TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, body, &created); code != http.StatusCreated {
		t.Fatalf("create status = %d", code)
	}
	if created.StartDate != "2026-08-03" || created.EndDate != "2026-08-07" ||
		created.Progress != 40 || created.Stage != "Design" {
		t.Fatalf("schedule fields: %+v", created)
	}
	if created.DependsOn == nil || len(created.DependsOn) != 0 {
		t.Fatalf("dependsOn = %#v, want non-nil empty", created.DependsOn)
	}

	// A successor depending on it.
	dep := taskCreateBody("follow-up")
	dep["startDate"] = "2026-08-10"
	dep["endDate"] = "2026-08-12"
	dep["dependsOn"] = []string{created.ID}
	var second TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, dep, &second); code != http.StatusCreated {
		t.Fatalf("dep create status = %d", code)
	}
	if len(second.DependsOn) != 1 || second.DependsOn[0] != created.ID {
		t.Fatalf("dependsOn = %v", second.DependsOn)
	}

	// clearSchedule unschedules.
	var updated TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPatch, taskURL("/api/tasks", "taskId", created.ID),
		editor, map[string]any{"clearSchedule": true}, &updated); code != http.StatusOK {
		t.Fatalf("clear status = %d", code)
	}
	if updated.StartDate != "" || updated.EndDate != "" || updated.Milestone {
		t.Fatalf("schedule not cleared: %+v", updated)
	}

	// Milestone patch on the successor collapses end to start.
	ms := map[string]any{"milestone": true, "startDate": "2026-08-10", "endDate": "2026-08-10"}
	if code := chatDo(t, ts.URL, http.MethodPatch, taskURL("/api/tasks", "taskId", second.ID), editor, ms, &updated); code != http.StatusOK {
		t.Fatalf("milestone patch status = %d", code)
	}
	if !updated.Milestone || updated.EndDate != "2026-08-10" {
		t.Fatalf("milestone patch: %+v", updated)
	}
}

func TestTasks_ScheduleRejected(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	bad := []map[string]any{
		{"startDate": "2026-08-07", "endDate": "2026-08-03"}, // end < start
		{"startDate": "2026-08-03"},                          // one-sided
		{"startDate": "2026-08-03", "endDate": "2026-08-07", "progress": 101},
		{"dependsOn": []string{"t999"}}, // unknown dep
	}
	for i, extra := range bad {
		body := taskCreateBody("x")
		for k, v := range extra {
			body[k] = v
		}
		if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, body, nil); code != http.StatusBadRequest {
			t.Errorf("case %d: status = %d, want 400", i, code)
		}
	}

	// Cycle: t1 <- t2, then patch t1 to depend on t2.
	var t1, t2 TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, taskCreateBody("one"), &t1); code != http.StatusCreated {
		t.Fatal("create t1")
	}
	b2 := taskCreateBody("two")
	b2["dependsOn"] = []string{t1.ID}
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, b2, &t2); code != http.StatusCreated {
		t.Fatal("create t2")
	}
	cyc := map[string]any{"dependsOn": []string{t2.ID}}
	if code := chatDo(t, ts.URL, http.MethodPatch, taskURL("/api/tasks", "taskId", t1.ID), editor, cyc, nil); code != http.StatusBadRequest {
		t.Fatalf("cycle status = %d, want 400", code)
	}

	// Delete prunes the dangling dependency on the next read.
	if code := chatDo(t, ts.URL, http.MethodDelete, taskURL("/api/tasks", "taskId", t1.ID), editor, nil, nil); code != http.StatusOK {
		t.Fatal("delete t1")
	}
	var got TaskDTO
	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks/get", "taskId", t2.ID), editor, nil, &got); code != http.StatusOK {
		t.Fatal("get t2")
	}
	if len(got.DependsOn) != 0 {
		t.Fatalf("dangling dep survived delete: %v", got.DependsOn)
	}
}

func TestTasks_Shift(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")
	viewer := login(t, s, "u-viewer", "Vera Viewer", "viewer@x.io")

	mk := func(title, start, end string) TaskDTO {
		body := taskCreateBody(title)
		body["startDate"] = start
		body["endDate"] = end
		var out TaskDTO
		if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, body, &out); code != http.StatusCreated {
			t.Fatalf("create %s status = %d", title, code)
		}
		return out
	}
	t1 := mk("a", "2026-08-03", "2026-08-07")
	t2 := mk("b", "2026-08-05", "2026-08-10")

	var shifted struct {
		Tasks []TaskDTO `json:"tasks"`
	}
	body := map[string]any{"taskIds": []string{t1.ID, t2.ID}, "days": 3}
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks/shift"), editor, body, &shifted); code != http.StatusOK {
		t.Fatalf("shift status = %d", code)
	}
	if len(shifted.Tasks) != 2 {
		t.Fatalf("shifted %d tasks, want 2", len(shifted.Tasks))
	}
	if shifted.Tasks[0].StartDate != "2026-08-06" || shifted.Tasks[1].EndDate != "2026-08-13" {
		t.Fatalf("shift wrong: %+v", shifted.Tasks)
	}

	// Read-only role can't shift.
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks/shift"), viewer, body, nil); code != http.StatusForbidden {
		t.Fatalf("viewer shift status = %d, want 403", code)
	}
	// Unscheduled member rejects the whole batch.
	var plain TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, taskCreateBody("no dates"), &plain); code != http.StatusCreated {
		t.Fatal("create plain")
	}
	bad := map[string]any{"taskIds": []string{t1.ID, plain.ID}, "days": 1}
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks/shift"), editor, bad, nil); code != http.StatusBadRequest {
		t.Fatalf("unscheduled member status = %d, want 400", code)
	}
	// Zero days is invalid.
	zero := map[string]any{"taskIds": []string{t1.ID}, "days": 0}
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks/shift"), editor, zero, nil); code != http.StatusBadRequest {
		t.Fatalf("zero-days status = %d, want 400", code)
	}
}
