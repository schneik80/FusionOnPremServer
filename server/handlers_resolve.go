package server

import (
	"net/http"

	"github.com/schneik80/fusionlocalserver/api"
)

// fetchProjects is the indirection over api.GetProjects, stubbed in tests
// (matching the fetchHubs seam in handlers_session.go). Production code never
// reassigns it.
var fetchProjects = api.GetProjects

// handleResolveProject is GET /api/resolve/project?dmHubId=&dmProjectId=: map
// the Data Management ids a Fusion add-in can read from its Python API
// (app.data.activeHub.id, dataFile.parentProject.id — the DTOs' altId space)
// to the GraphQL ids every data route here is keyed by. Deliberately behind
// bare requireAuth, not requireHub: the embed page must resolve the document's
// hub before a hub is locked, and before deciding whether to ask the user for
// hub-switch consent — exactly the states requireHub refuses. Membership is
// still enforced the same way as the hub lock itself: both lookups run with
// the caller's own token, so an id outside their reach is simply not found.
func (s *Server) handleResolveProject(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	tok, ok := s.token(r.Context(), w, r)
	if !ok {
		return
	}
	dmHubID, ok := reqParam(w, r, "dmHubId")
	if !ok {
		return
	}
	dmProjectID, ok := reqParam(w, r, "dmProjectId")
	if !ok {
		return
	}

	ctx, cancel := s.reqCtx(r)
	defer cancel()
	hubs, err := fetchHubs(ctx, tok)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	hub, found := matchByAltOrID(hubs, dmHubID)
	if !found {
		writeErrorCode(w, http.StatusNotFound, "hub_not_found", "no accessible hub matches this id")
		return
	}

	projects, err := fetchProjects(ctx, tok, hub.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	project, found := matchByAltOrID(projects, dmProjectID)
	if !found {
		writeErrorCode(w, http.StatusNotFound, "project_not_found", "no project in this hub matches this id")
		return
	}

	sessionHubID, _ := sess.SelectedHub()
	writeJSON(w, http.StatusOK, ResolveProjectDTO{
		HubID:        hub.ID,
		HubName:      hub.Name,
		HubAltID:     hub.AltID,
		ProjectID:    project.ID,
		ProjectName:  project.Name,
		ProjectAltID: project.AltID,
		SessionHubID: sessionHubID,
	})
}

// matchByAltOrID finds the item whose AltID (the DM id Fusion reports) equals
// key, falling back to the GraphQL ID so callers that already hold one still
// resolve.
func matchByAltOrID(items []api.NavItem, key string) (api.NavItem, bool) {
	for _, it := range items {
		if it.AltID == key {
			return it, true
		}
	}
	for _, it := range items {
		if it.ID == key {
			return it, true
		}
	}
	return api.NavItem{}, false
}
