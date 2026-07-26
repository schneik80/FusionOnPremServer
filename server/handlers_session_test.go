package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/auth"
)

// stubHubs points the session-hub membership check at a fixed hub list.
func stubHubs(t *testing.T, hubs ...api.NavItem) {
	t.Helper()
	prev := fetchHubs
	fetchHubs = func(ctx context.Context, token string) ([]api.NavItem, error) {
		return hubs, nil
	}
	t.Cleanup(func() { fetchHubs = prev })
}

// errBody decodes the uniform error envelope.
func errBody(t *testing.T, base, method, path string, cookie *http.Cookie, body any) (int, errorResponse) {
	t.Helper()
	var e errorResponse
	code := chatDo(t, base, method, path, cookie, body, &e)
	return code, e
}

func TestSessionHub_LockSwitchAndMembership(t *testing.T) {
	stubHubs(t,
		api.NavItem{ID: "hub-1", Name: "Hub One", Kind: "hub"},
		api.NavItem{ID: "hub-2", Name: "Hub Two", Kind: "hub"},
	)
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := loginNoHub(t, s, "u-editor", "Ed Editor", "editor@x.io")

	// Unauthenticated → 401.
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/session/hub", nil,
		map[string]string{"hubId": "hub-1"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated lock = %d, want 401", code)
	}

	// Before any lock, /api/auth/me carries no hub and data routes 409.
	var me AuthMeDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/auth/me", editor, nil, &me); code != http.StatusOK {
		t.Fatal("auth/me")
	}
	if me.SelectedHubID != "" {
		t.Errorf("pre-lock selectedHubId = %q, want empty", me.SelectedHubID)
	}
	code, e := errBody(t, ts.URL, http.MethodGet, taskURL("/api/tasks"), editor, nil)
	if code != http.StatusConflict || e.Code != codeHubNotSelected {
		t.Fatalf("pre-lock data route = %d %q, want 409 hub_not_selected", code, e.Code)
	}

	// Missing/empty hubId → 400.
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/session/hub", editor,
		map[string]string{}, nil); code != http.StatusBadRequest {
		t.Fatalf("empty hubId lock = %d, want 400", code)
	}

	// Non-member hub → 403, still not locked.
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/session/hub", editor,
		map[string]string{"hubId": "hub-evil"}, nil); code != http.StatusForbidden {
		t.Fatalf("non-member lock = %d, want 403", code)
	}
	if code, e = errBody(t, ts.URL, http.MethodGet, taskURL("/api/tasks"), editor, nil); code != http.StatusConflict || e.Code != codeHubNotSelected {
		t.Fatalf("after refused lock: %d %q, want 409", code, e.Code)
	}

	// Member hub → 200, AuthMe reflects it, data routes answer.
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/session/hub", editor,
		map[string]string{"hubId": "hub-1"}, &me); code != http.StatusOK {
		t.Fatalf("member lock = %d, want 200", code)
	}
	if me.SelectedHubID != "hub-1" || me.SelectedHubName != "Hub One" {
		t.Errorf("lock response = %+v", me)
	}
	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks"), editor, nil, nil); code != http.StatusOK {
		t.Fatalf("post-lock data route = %d, want 200", code)
	}

	// Create a task in hub-1, then RE-POST a different hub — the switch.
	var created TaskDTO
	if code := chatDo(t, ts.URL, http.MethodPost, taskURL("/api/tasks"), editor, taskCreateBody("in hub one"), &created); code != http.StatusCreated {
		t.Fatalf("create in hub-1 = %d", code)
	}
	if code := chatDo(t, ts.URL, http.MethodPost, "/api/session/hub", editor,
		map[string]string{"hubId": "hub-2"}, &me); code != http.StatusOK || me.SelectedHubID != "hub-2" {
		t.Fatalf("switch = %d, %+v", code, me)
	}
	// The replayed request now serves hub-2 only: the hub-1 task is invisible.
	var list TaskListDTO
	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks"), editor, nil, &list); code != http.StatusOK {
		t.Fatalf("post-switch list = %d", code)
	}
	if len(list.Tasks) != 0 {
		t.Errorf("hub-2 sees hub-1 tasks: %+v", list.Tasks)
	}
}

func TestSessionHub_FetchFailureFailsClosed(t *testing.T) {
	prev := fetchHubs
	fetchHubs = func(ctx context.Context, token string) ([]api.NavItem, error) {
		return nil, errors.New("HTTP 401 unauthorized")
	}
	t.Cleanup(func() { fetchHubs = prev })

	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := loginNoHub(t, s, "u-editor", "Ed", "editor@x.io")

	if code := chatDo(t, ts.URL, http.MethodPost, "/api/session/hub", editor,
		map[string]string{"hubId": "hub-1"}, nil); code == http.StatusOK {
		t.Fatal("hub lock succeeded although the membership check failed")
	}
}

// TestRequireHub_ChokeMatrix drives the 409/403 choke point across every
// route family: no selected hub → 409 hub_not_selected; a query-param hubId
// naming another hub → 403 hub_mismatch, refused centrally BEFORE any store
// or APS access (the APS-proxy rows would otherwise hit the roster stub and
// fail differently).
func TestRequireHub_ChokeMatrix(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/tasks?projectId=urn:p:1"},
		{http.MethodGet, "/api/tasks/mine"},
		{http.MethodGet, "/api/chat/channels?projectId=urn:p:1"},
		{http.MethodGet, "/api/production/jobs?projectId=urn:p:1"},
		{http.MethodGet, "/api/production/mine"},
		{http.MethodGet, "/api/whiteboards?projectId=urn:p:1"},
		{http.MethodGet, "/api/pins"},
		{http.MethodGet, "/api/uploads"},
		{http.MethodGet, "/api/admin/disk"},
		{http.MethodGet, "/api/admin/backups"},
		{http.MethodGet, "/api/admin/status"},
		{http.MethodGet, "/api/projects?hubId=hub-1"},
		{http.MethodGet, "/api/wiki/pages?hubId=hub-1&dmProjectId=d1"},
		{http.MethodGet, "/api/activity/report?hubId=hub-1&id=i1"},
		{http.MethodPost, "/api/settings/port"},
	}

	// No hub selected → 409 everywhere.
	bare := loginNoHub(t, s, "u-editor", "Ed", "editor@x.io")
	for _, rt := range routes {
		code, e := errBody(t, ts.URL, rt.method, rt.path, bare, nil)
		if code != http.StatusConflict || e.Code != codeHubNotSelected {
			t.Errorf("%s %s (no hub) = %d %q, want 409 hub_not_selected", rt.method, rt.path, code, e.Code)
		}
	}

	// Locked to hub-1, any wire hubId naming another hub → 403 hub_mismatch.
	locked := login(t, s, "u-editor", "Ed", "editor@x.io")
	mismatch := []struct{ method, path string }{
		{http.MethodGet, "/api/projects?hubId=hub-9"},
		{http.MethodGet, "/api/pins?hubId=hub-9"},
		{http.MethodGet, "/api/wiki/pages?hubId=hub-9&dmProjectId=d1"},
		{http.MethodGet, "/api/activity/report?hubId=hub-9&id=i1"},
		{http.MethodGet, "/api/items/details?hubId=hub-9&itemId=i1"},
		{http.MethodPost, "/api/activity/rollup"}, // body-hubId variant, below
	}
	for _, rt := range mismatch[:len(mismatch)-1] {
		code, e := errBody(t, ts.URL, rt.method, rt.path, locked, nil)
		if code != http.StatusForbidden || e.Code != codeHubMismatch {
			t.Errorf("%s %s (mismatch) = %d %q, want 403 hub_mismatch", rt.method, rt.path, code, e.Code)
		}
	}
	// Body-carried hubId: the rollup names hub-9 in its JSON body.
	code, e := errBody(t, ts.URL, http.MethodPost, "/api/activity/rollup", locked,
		map[string]any{"hubId": "hub-9", "itemId": "i1"})
	if code != http.StatusForbidden || e.Code != codeHubMismatch {
		t.Errorf("rollup body mismatch = %d %q, want 403 hub_mismatch", code, e.Code)
	}
	// A matching hubId sails through the gate (the task route proves it).
	if code := chatDo(t, ts.URL, http.MethodGet, taskURL("/api/tasks"), locked, nil, nil); code != http.StatusOK {
		t.Errorf("matching hub data route = %d, want 200", code)
	}
}

func TestSessionPersistence_SelectedHubSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	a := newPersistentStore(t, dir)
	sess, err := a.Create(
		&auth.TokenData{AccessToken: "AT", RefreshToken: "RT", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{Name: "Ada", Email: "ada@x.io"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !a.SetSelectedHub(sess.ID, "hub-7", "Hub Seven") {
		t.Fatal("SetSelectedHub reported missing session")
	}

	b := newPersistentStore(t, dir)
	got, ok := b.Get(sess.ID)
	if !ok {
		t.Fatal("session not restored")
	}
	hubID, hubName := got.SelectedHub()
	if hubID != "hub-7" || hubName != "Hub Seven" {
		t.Errorf("restored hub lock = %q/%q, want hub-7/Hub Seven", hubID, hubName)
	}
}

// TestWriteErrorCode locks the explicit-code envelope shape.
func TestWriteErrorCode(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErrorCode(rec, http.StatusConflict, codeHubNotSelected, "no hub selected for this session")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
	var e errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Code != "hub_not_selected" || e.Error == "" {
		t.Errorf("envelope = %+v", e)
	}
}
