package server

import (
	"net/http"
	"sync"

	"github.com/schneik80/fusionlocalserver/api"
)

// handleMeta describes the running server: version, region, and the listen
// port (plus whether it can be changed at runtime).
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	var logo *LogoDTO
	if meta, _, ok := s.logo.get(); ok {
		logo = logoDTO(&meta)
	}
	writeJSON(w, http.StatusOK, MetaDTO{
		Version:          s.opts.Version,
		Region:           regionLabel(s.region),
		Port:             s.currentPort(),
		PortConfigurable: s.portConfigurable,
		Debug:            api.DebugEnabled(),
		Logo:             logo,
	})
}

// handleHubs -> api.GetHubs.
func (s *Server) handleHubs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	hubs, err := api.GetHubs(ctx, token)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemDTOs(hubs))
}

// handleProjects -> api.GetProjects (query: hubId).
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	hubID, ok := reqParam(w, r, "hubId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	projects, err := api.GetProjects(ctx, token, hubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemDTOs(projects))
}

// handleProjectContents fetches a project's root folders and loose items
// concurrently (query: projectId), returning {folders, items}.
func (s *Server) handleProjectContents(w http.ResponseWriter, r *http.Request) {
	projectID, ok := reqParam(w, r, "projectId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}

	var (
		folders, items []api.NavItem
		ferr, ierr     error
		wg             sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		folders, ferr = api.GetFolders(ctx, token, projectID)
	}()
	go func() {
		defer wg.Done()
		items, ierr = api.GetProjectItems(ctx, token, projectID)
	}()
	wg.Wait()

	if ferr != nil {
		s.fail(w, r, ferr)
		return
	}
	if ierr != nil {
		s.fail(w, r, ierr)
		return
	}
	writeJSON(w, http.StatusOK, ContentsDTO{
		Folders: itemDTOs(folders),
		Items:   itemDTOs(items),
	})
}

// handleFolderContents -> api.GetItems (query: hubId, folderId).
func (s *Server) handleFolderContents(w http.ResponseWriter, r *http.Request) {
	hubID, ok := reqParam(w, r, "hubId")
	if !ok {
		return
	}
	folderID, ok := reqParam(w, r, "folderId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	items, err := api.GetItems(ctx, token, hubID, folderID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemDTOs(items))
}

// handleItemDetails -> api.GetItemDetails (query: hubId, itemId).
func (s *Server) handleItemDetails(w http.ResponseWriter, r *http.Request) {
	hubID, ok := reqParam(w, r, "hubId")
	if !ok {
		return
	}
	itemID, ok := reqParam(w, r, "itemId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	d, err := api.GetItemDetails(ctx, token, hubID, itemID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detailsDTO(d))
}

// handleItemLocation -> api.GetItemLocation (query: hubId, itemId).
func (s *Server) handleItemLocation(w http.ResponseWriter, r *http.Request) {
	hubID, ok := reqParam(w, r, "hubId")
	if !ok {
		return
	}
	itemID, ok := reqParam(w, r, "itemId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	loc, err := api.GetItemLocation(ctx, token, hubID, itemID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, locationDTO(loc))
}
