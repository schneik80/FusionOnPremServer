package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/schneik80/fusionlocalserver/api"
)

// stubProjects points the resolve endpoint's project listing at a fixed set,
// recording the hub it was asked for.
func stubProjects(t *testing.T, askedHub *string, projects ...api.NavItem) {
	t.Helper()
	prev := fetchProjects
	fetchProjects = func(ctx context.Context, token, hubID string) ([]api.NavItem, error) {
		if askedHub != nil {
			*askedHub = hubID
		}
		return projects, nil
	}
	t.Cleanup(func() { fetchProjects = prev })
}

func resolveURL(dmHubID, dmProjectID string) string {
	q := url.Values{}
	if dmHubID != "" {
		q.Set("dmHubId", dmHubID)
	}
	if dmProjectID != "" {
		q.Set("dmProjectId", dmProjectID)
	}
	return "/api/resolve/project?" + q.Encode()
}

func TestResolveProject_MapsDMIdsToGraphQLIds(t *testing.T) {
	stubHubs(t,
		api.NavItem{ID: "urn:hub:1", Name: "Hub One", Kind: "hub", AltID: "b.hub-dm-1"},
		api.NavItem{ID: "urn:hub:2", Name: "Hub Two", Kind: "hub", AltID: "b.hub-dm-2"},
	)
	var askedHub string
	stubProjects(t, &askedHub,
		api.NavItem{ID: "urn:proj:1", Name: "Widget", Kind: "project", AltID: "a.proj-dm-1"},
		api.NavItem{ID: "urn:proj:2", Name: "Gadget", Kind: "project", AltID: "a.proj-dm-2"},
	)
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	// Unauthenticated → 401.
	if code := chatDo(t, ts.URL, http.MethodGet, resolveURL("b.hub-dm-1", "a.proj-dm-1"), nil, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated resolve = %d, want 401", code)
	}

	// No hub lock: still resolves (the endpoint is deliberately pre-hub) and
	// reports the empty session lock.
	bare := loginNoHub(t, s, "u-editor", "Ed", "editor@x.io")
	var out ResolveProjectDTO
	if code := chatDo(t, ts.URL, http.MethodGet, resolveURL("b.hub-dm-2", "a.proj-dm-2"), bare, nil, &out); code != http.StatusOK {
		t.Fatalf("pre-hub resolve = %d, want 200", code)
	}
	if out.HubID != "urn:hub:2" || out.ProjectID != "urn:proj:2" || out.SessionHubID != "" {
		t.Errorf("pre-hub resolve = %+v", out)
	}
	if askedHub != "urn:hub:2" {
		t.Errorf("projects listed for hub %q, want the resolved GraphQL id urn:hub:2", askedHub)
	}

	// Locked session: full mapping plus the session hub for the consent logic.
	locked := login(t, s, "u-editor", "Ed", "editor@x.io")
	if code := chatDo(t, ts.URL, http.MethodGet, resolveURL("b.hub-dm-1", "a.proj-dm-1"), locked, nil, &out); code != http.StatusOK {
		t.Fatalf("locked resolve = %d, want 200", code)
	}
	want := ResolveProjectDTO{
		HubID: "urn:hub:1", HubName: "Hub One", HubAltID: "b.hub-dm-1",
		ProjectID: "urn:proj:1", ProjectName: "Widget", ProjectAltID: "a.proj-dm-1",
		SessionHubID: testHubID,
	}
	if out != want {
		t.Errorf("resolve = %+v, want %+v", out, want)
	}
}

func TestResolveProject_GraphQLIdFallback(t *testing.T) {
	stubHubs(t, api.NavItem{ID: "urn:hub:1", Name: "Hub One", Kind: "hub", AltID: "b.hub-dm-1"})
	stubProjects(t, nil, api.NavItem{ID: "urn:proj:1", Name: "Widget", Kind: "project", AltID: "a.proj-dm-1"})
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	cookie := loginNoHub(t, s, "u-editor", "Ed", "editor@x.io")

	// Callers already holding GraphQL ids resolve too.
	var out ResolveProjectDTO
	if code := chatDo(t, ts.URL, http.MethodGet, resolveURL("urn:hub:1", "urn:proj:1"), cookie, nil, &out); code != http.StatusOK {
		t.Fatalf("graphql-id resolve = %d, want 200", code)
	}
	if out.ProjectID != "urn:proj:1" {
		t.Errorf("resolve = %+v", out)
	}
}

func TestResolveProject_NotFoundAndBadRequest(t *testing.T) {
	stubHubs(t, api.NavItem{ID: "urn:hub:1", Name: "Hub One", Kind: "hub", AltID: "b.hub-dm-1"})
	stubProjects(t, nil, api.NavItem{ID: "urn:proj:1", Name: "Widget", Kind: "project", AltID: "a.proj-dm-1"})
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	cookie := loginNoHub(t, s, "u-editor", "Ed", "editor@x.io")

	code, e := errBody(t, ts.URL, http.MethodGet, resolveURL("b.hub-unknown", "a.proj-dm-1"), cookie, nil)
	if code != http.StatusNotFound || e.Code != "hub_not_found" {
		t.Errorf("unknown hub = %d %q, want 404 hub_not_found", code, e.Code)
	}
	code, e = errBody(t, ts.URL, http.MethodGet, resolveURL("b.hub-dm-1", "a.proj-unknown"), cookie, nil)
	if code != http.StatusNotFound || e.Code != "project_not_found" {
		t.Errorf("unknown project = %d %q, want 404 project_not_found", code, e.Code)
	}
	if code := chatDo(t, ts.URL, http.MethodGet, resolveURL("", "a.proj-dm-1"), cookie, nil, nil); code != http.StatusBadRequest {
		t.Errorf("missing dmHubId = %d, want 400", code)
	}
	if code := chatDo(t, ts.URL, http.MethodGet, resolveURL("b.hub-dm-1", ""), cookie, nil, nil); code != http.StatusBadRequest {
		t.Errorf("missing dmProjectId = %d, want 400", code)
	}
}

func TestResolveProject_UpstreamFailureFailsClosed(t *testing.T) {
	prev := fetchHubs
	fetchHubs = func(ctx context.Context, token string) ([]api.NavItem, error) {
		return nil, errors.New("HTTP 502 bad gateway")
	}
	t.Cleanup(func() { fetchHubs = prev })
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	cookie := loginNoHub(t, s, "u-editor", "Ed", "editor@x.io")

	if code := chatDo(t, ts.URL, http.MethodGet, resolveURL("b.hub-dm-1", "a.proj-dm-1"), cookie, nil, nil); code == http.StatusOK {
		t.Fatal("resolve succeeded although the hub listing failed")
	}
}
