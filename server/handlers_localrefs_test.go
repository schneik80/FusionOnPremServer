package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/schneik80/fusionlocalserver/production"
	"github.com/schneik80/fusionlocalserver/tasks"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

const (
	lrItem  = "urn:adsk.wipprod:dm.lineage:hC6k4hndRWaeIVhIjvHu8w"
	lrToken = "fls:doc?hubId=b.1&itemId=urn%3Aadsk.wipprod%3Adm.lineage%3AhC6k4hndRWaeIVhIjvHu8w&name=bracket&kind=design"
)

// localRefsPath builds the request URL — the item id is a urn full of ':' and
// so must ride in an ESCAPED query param, never a path segment.
func localRefsPath(sources string) string {
	q := url.Values{"itemId": {lrItem}}
	if sources != "" {
		q.Set("sources", sources)
	}
	return "/api/items/local-refs?" + q.Encode()
}

// seedLocalRefs fills one project with a reference of every kind, so a lookup
// against lrItem finds: 1 task, 1 chat channel, 1 whiteboard, 1 job plan, and
// 1 batch (the batch freezes the plan on creation).
func seedLocalRefs(t *testing.T, set *storeSet, projectID, projectName string) {
	t.Helper()
	if _, err := set.tasks.Create(projectID, testHubID, projectName,
		tasks.Draft{Title: "Fix the bracket", DocRefs: []string{lrToken}}, tasks.UserRef{ID: "u-1", Name: "Ada"}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	ch, err := set.chat.EnsureRoot(projectID)
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, _, err := set.chat.CreateMessage(projectID, ch.ID, "u-1", "Ada", projectID+"-m1",
		"can someone check "+lrToken, 0); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	board, err := set.whiteboards.Create(projectID, testHubID, projectName,
		whiteboards.Draft{Name: "Layout"}, whiteboards.UserRef{ID: "u-1", Name: "Ada"})
	if err != nil {
		t.Fatalf("seed board: %v", err)
	}
	if _, err := set.whiteboards.SaveSnapshot(projectID, board.ID,
		[]byte(`{"shapes":[{"props":{"token":"`+lrToken+`"}}]}`), whiteboards.UserRef{ID: "u-1", Name: "Ada"}, 0, true); err != nil {
		t.Fatalf("seed board doc: %v", err)
	}
	prodMe := production.UserRef{ID: "u-1", Name: "Ada"}
	job, err := set.production.CreateJob(projectID, testHubID, projectName, production.JobDraft{Name: "Mill"}, prodMe)
	if err != nil {
		t.Fatalf("seed job: %v", err)
	}
	j, err := set.production.CreateStep(projectID, job.ID, production.StepDraft{Title: "Setup 1"})
	if err != nil {
		t.Fatalf("seed step: %v", err)
	}
	if _, err := set.production.AttachPlanDoc(projectID, job.ID, j.Steps[0].ID,
		production.DocSnapshot{HubID: testHubID, ItemID: lrItem, Name: "bracket", VersionID: lrItem + "?version=1", VersionNumber: 1},
		prodMe); err != nil {
		t.Fatalf("seed plan doc: %v", err)
	}
	if _, err := set.production.CreateBatch(projectID, job.ID,
		production.BatchDraft{Name: "Run 1", Kind: "prove"}, prodMe); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
}

func kindsOf(refs []LocalRefDTO) map[string]int {
	out := map[string]int{}
	for _, r := range refs {
		out[r.Kind]++
	}
	return out
}

// TestLocalRefs_AllKindsAndScoping is the security case first: local data in a
// project the caller's token cannot see (Ghost) must never surface, exactly as
// the hub overview scopes its aggregates. It also asserts every source is
// found and that tokens ride along for the kinds that have one.
func TestLocalRefs_AllKindsAndScoping(t *testing.T) {
	s := newHubTestServer(t)
	set := hubSet(t, s, testHubID)
	seedLocalRefs(t, set, "a.p1", "Alpha")
	seedLocalRefs(t, set, "a.p3", "Ghost") // not in the accessible list

	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	cookie := login(t, s, "u-1", "Ada", "ada@x.io")

	var out LocalRefsDTO
	if code := chatDo(t, ts.URL, http.MethodGet, localRefsPath(""), cookie, nil, &out); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if out.Truncated {
		t.Error("five refs must not truncate")
	}
	got := kindsOf(out.Refs)
	for _, kind := range []string{localRefTask, localRefChat, localRefWhiteboard, localRefJob, localRefBatch} {
		if got[kind] != 1 {
			t.Errorf("%s refs = %d, want 1 (Ghost excluded): %+v", kind, got[kind], out.Refs)
		}
	}
	for _, r := range out.Refs {
		if r.ProjectID != "a.p1" {
			t.Errorf("leaked project %q", r.ProjectID)
		}
		if r.ProjectName != "Alpha" {
			t.Errorf("%s: project name = %q, want the APS name", r.Kind, r.ProjectName)
		}
		switch r.Kind {
		case localRefTask, localRefJob, localRefBatch:
			if r.Token == "" {
				t.Errorf("%s ref has no fls: token to open", r.Kind)
			}
		case localRefChat:
			if r.Detail == "" || r.Author != "Ada" {
				t.Errorf("chat ref missing excerpt/author: %+v", r)
			}
		}
	}
}

// TestLocalRefs_SourcesFilter is what the checkboxes buy: an unchecked source
// is never scanned, which is how turning one off makes the lookup cheaper.
func TestLocalRefs_SourcesFilter(t *testing.T) {
	s := newHubTestServer(t)
	seedLocalRefs(t, hubSet(t, s, testHubID), "a.p1", "Alpha")

	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	cookie := login(t, s, "u-1", "Ada", "ada@x.io")

	var out LocalRefsDTO
	if code := chatDo(t, ts.URL, http.MethodGet, localRefsPath("task,batch"), cookie, nil, &out); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	got := kindsOf(out.Refs)
	if len(out.Refs) != 2 || got[localRefTask] != 1 || got[localRefBatch] != 1 {
		t.Fatalf("filtered refs = %+v, want one task and one batch", out.Refs)
	}

	// A filter naming only sources this build doesn't have returns nothing
	// rather than 400ing or silently falling back to everything.
	out = LocalRefsDTO{}
	if code := chatDo(t, ts.URL, http.MethodGet, localRefsPath("wiki"), cookie, nil, &out); code != http.StatusOK {
		t.Fatalf("unknown-source status = %d", code)
	}
	if len(out.Refs) != 0 {
		t.Fatalf("unknown source returned %+v", out.Refs)
	}
}

// TestLocalRefs_CapIsSharedFairly: one noisy kind must not crowd the others
// out of the capped list, or "truncated" would read as "no production ties".
func TestLocalRefs_CapIsSharedFairly(t *testing.T) {
	refs := make([]LocalRefDTO, 0, maxLocalRefs*3)
	for i := 0; i < maxLocalRefs*2; i++ {
		refs = append(refs, LocalRefDTO{Kind: localRefTask, Key: "task:" + string(rune('a'+i%26))})
	}
	for i := 0; i < 5; i++ {
		refs = append(refs, LocalRefDTO{Kind: localRefBatch, Key: "batch"})
	}
	kept, truncated := capLocalRefs(refs)
	if !truncated {
		t.Fatal("want truncated")
	}
	if len(kept) != maxLocalRefs {
		t.Fatalf("kept %d, want %d", len(kept), maxLocalRefs)
	}
	got := kindsOf(kept)
	if got[localRefBatch] != 5 {
		t.Errorf("batches kept = %d, want all 5 — a noisy kind crowded them out", got[localRefBatch])
	}
	if got[localRefTask] != maxLocalRefs-5 {
		t.Errorf("tasks kept = %d, want the rest of the cap", got[localRefTask])
	}
	// Under the cap nothing is touched at all.
	if kept, truncated := capLocalRefs(refs[:3]); truncated || len(kept) != 3 {
		t.Errorf("small list = %d refs, truncated=%v", len(kept), truncated)
	}
}

// TestLocalRefs_PrivateChannelsNeedMembership is the second authorization
// layer: project access alone does not expose a private channel's references.
func TestLocalRefs_PrivateChannelsNeedMembership(t *testing.T) {
	s := newHubTestServer(t)
	set := hubSet(t, s, testHubID)
	ch, err := set.chat.CreateChannel("a.p1", "secret", "", "u-9", true, []string{"u-9"})
	if err != nil {
		t.Fatalf("seed private channel: %v", err)
	}
	if _, _, err := set.chat.CreateMessage("a.p1", ch.ID, "u-9", "Grace", "m1", "see "+lrToken, 0); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	var out LocalRefsDTO
	outsider := login(t, s, "u-1", "Ada", "ada@x.io")
	if code := chatDo(t, ts.URL, http.MethodGet, localRefsPath("chat"), outsider, nil, &out); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(out.Refs) != 0 {
		t.Fatalf("non-member saw a private channel's references: %+v", out.Refs)
	}

	out = LocalRefsDTO{}
	member := login(t, s, "u-9", "Grace", "grace@x.io")
	if code := chatDo(t, ts.URL, http.MethodGet, localRefsPath("chat"), member, nil, &out); code != http.StatusOK {
		t.Fatalf("member status = %d", code)
	}
	if len(out.Refs) != 1 || out.Refs[0].Name != "secret" {
		t.Fatalf("member should see the private channel, got %+v", out.Refs)
	}
}
