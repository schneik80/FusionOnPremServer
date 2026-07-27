package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/internal/testutil"
	"github.com/schneik80/fusionlocalserver/production"
	"github.com/schneik80/fusionlocalserver/tasks"
)

// newHubTestServer builds a Server whose upstream GraphQL is faked to return a
// fixed accessible-project list (Alpha + Bravo). The hub-overview handler makes
// exactly one upstream call — GetProjects — so that is all the fake answers.
func newHubTestServer(t *testing.T) *Server {
	t.Helper()
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: map[string]any{
			"hub": map[string]any{
				"projects": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results": []map[string]any{
						{"id": "a.p1", "name": "Alpha", "alternativeIdentifiers": map[string]any{"dataManagementAPIProjectId": "b.p1"}},
						{"id": "a.p2", "name": "Bravo", "alternativeIdentifiers": map[string]any{"dataManagementAPIProjectId": "b.p2"}},
					},
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
	}
}

// TestHubOverview_ScopesToAccessibleProjects is the security case: a project
// with local data (tasks, a job, chat) that is NOT in the accessible list from
// GetProjects must be excluded from every aggregate. It also checks the status
// counts, overdue tally, and pipeline counts on the accessible projects.
func TestHubOverview_ScopesToAccessibleProjects(t *testing.T) {
	s := newHubTestServer(t)
	set := hubSet(t, s, testHubID)

	me := tasks.UserRef{ID: "u-1", Name: "Ada", Email: "ada@x.io"}
	mustTask := func(pid, projectName, status, due string) {
		t.Helper()
		if _, err := set.tasks.Create(pid, testHubID, projectName,
			tasks.Draft{Title: "task", Status: status, DueDate: due}, me); err != nil {
			t.Fatalf("seed task in %s: %v", pid, err)
		}
	}
	mustTask("a.p1", "Alpha", "todo", "")
	mustTask("a.p1", "Alpha", "inprogress", "2020-01-01") // past due, not done → overdue
	mustTask("a.p1", "Alpha", "done", "")
	mustTask("a.p2", "Bravo", "todo", "")
	mustTask("a.p3", "Ghost", "todo", "") // inaccessible → must be excluded

	prodMe := production.UserRef{ID: "u-1", Name: "Ada", Email: "ada@x.io"}
	job, err := set.production.CreateJob("a.p1", testHubID, "Alpha", production.JobDraft{Name: "Job"}, prodMe)
	if err != nil {
		t.Fatalf("seed job: %v", err)
	}
	b1, err := set.production.CreateBatch("a.p1", job.ID,
		production.BatchDraft{Name: "run1", Kind: "production", RunAt: time.Now()}, prodMe)
	if err != nil {
		t.Fatalf("seed batch1: %v", err)
	}
	if _, err := set.production.CreateBatch("a.p1", job.ID,
		production.BatchDraft{Name: "run2", Kind: "prove", RunAt: time.Now()}, prodMe); err != nil {
		t.Fatalf("seed batch2: %v", err)
	}
	running := "running"
	if _, err := set.production.UpdateBatch("a.p1", job.ID, b1.ID, production.BatchPatch{Status: &running}); err != nil {
		t.Fatalf("update batch status: %v", err)
	}
	if _, err := set.production.CreateJob("a.p3", testHubID, "Ghost", production.JobDraft{Name: "GhostJob"}, prodMe); err != nil {
		t.Fatalf("seed ghost job: %v", err)
	}

	seedMsgs := func(pid string, n int) {
		t.Helper()
		ch, err := set.chat.EnsureRoot(pid)
		if err != nil {
			t.Fatalf("ensure root %s: %v", pid, err)
		}
		for i := 0; i < n; i++ {
			if _, _, err := set.chat.CreateMessage(pid, ch.ID, "u-1", "Ada", fmt.Sprintf("%s-%d", pid, i), "hello", 0); err != nil {
				t.Fatalf("seed msg in %s: %v", pid, err)
			}
		}
	}
	seedMsgs("a.p1", 2)
	seedMsgs("a.p3", 5) // inaccessible → must be excluded

	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	cookie := login(t, s, "u-1", "Ada", "ada@x.io")

	var out HubOverviewDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/hub/overview", cookie, nil, &out); code != http.StatusOK {
		t.Fatalf("overview status = %d", code)
	}

	if out.ProjectCount != 2 {
		t.Errorf("projectCount = %d, want 2", out.ProjectCount)
	}
	if out.Tasks.Total != 4 {
		t.Errorf("tasks.total = %d, want 4 (Ghost excluded)", out.Tasks.Total)
	}
	if out.Tasks.Todo != 2 || out.Tasks.InProgress != 1 || out.Tasks.Done != 1 {
		t.Errorf("task status counts = %+v", out.Tasks)
	}
	if out.Tasks.Overdue != 1 {
		t.Errorf("overdue = %d, want 1", out.Tasks.Overdue)
	}
	if out.Tasks.Open != 3 {
		t.Errorf("open = %d, want 3", out.Tasks.Open)
	}
	if out.Production.Jobs != 1 || out.Production.Batches != 2 {
		t.Errorf("production = %+v, want 1 job / 2 batches (Ghost excluded)", out.Production)
	}
	if out.Production.Running != 1 || out.Production.Planned != 1 {
		t.Errorf("production status = %+v, want 1 running / 1 planned", out.Production)
	}
	if out.Chat.Total != 2 {
		t.Errorf("chat.total = %d, want 2 (Ghost excluded)", out.Chat.Total)
	}
	for _, p := range out.Projects {
		if p.ProjectID == "a.p3" || p.ProjectName == "Ghost" {
			t.Errorf("inaccessible project leaked into pulse: %+v", p)
		}
	}
	if len(out.Pulse) == 0 {
		t.Errorf("pulse is empty; expected local activity from the accessible projects")
	}
}
