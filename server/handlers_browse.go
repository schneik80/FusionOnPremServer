package server

import (
	"net/http"

	"github.com/schneik80/fusionlocalserver/api"
)

// handleBrowseContents lists one folder of a project for the in-place hub
// browser (overlay document/folder picker), straight from the Data Management
// API — the GraphQL items listing misses DM-created content (wiki image
// folders, for one). An omitted folderId lists the project root.
// GET /api/browse/contents?hubId=<graphql hub id>&dmProjectId=<project altId>[&folderId=<dm folder urn>]
func (s *Server) handleBrowseContents(w http.ResponseWriter, r *http.Request) {
	hubID, ok := reqParam(w, r, "hubId")
	if !ok {
		return
	}
	dmProjectID, ok := reqParam(w, r, "dmProjectId")
	if !ok {
		return
	}
	folderID := r.URL.Query().Get("folderId")
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	// The DM hub id is only needed to resolve the project root; subfolder
	// listings skip the extra GraphQL round-trip.
	dmHubID := ""
	if folderID == "" {
		var err error
		dmHubID, err = s.hubDMID(ctx, r, token, hubID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	items, err := api.BrowseFolder(ctx, token, dmHubID, dmProjectID, folderID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemDTOs(items))
}
