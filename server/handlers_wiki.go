package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"

	"github.com/schneik80/fusionlocalserver/api"
)

// Wiki handlers serve the project-scoped markdown pages stored in a project-root
// "Wiki" folder. They take the GraphQL hub id (translated to the DM hub id here)
// and the project's data-management id (its altId), matching how the rest of the
// app addresses Data-Management resources (see handleDrawingPreview). Phase 1 is
// read-only; publishing is added in Phase 2.

// handleWikiPages lists a project's published wiki pages.
// GET /api/wiki/pages?hubId=<graphql hub id>&dmProjectId=<project altId>
func (s *Server) handleWikiPages(w http.ResponseWriter, r *http.Request) {
	hubID, ok := reqParam(w, r, "hubId")
	if !ok {
		return
	}
	dmProjectID, ok := reqParam(w, r, "dmProjectId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	dmHubID, err := api.GetHubDataManagementID(ctx, token, hubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pages, err := api.ListWikiPages(ctx, token, dmHubID, dmProjectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wikiPageDTOs(pages))
}

// handleWikiPage returns the markdown body of a single published page.
// GET /api/wiki/page?dmProjectId=<project altId>&itemId=<item lineage urn>
func (s *Server) handleWikiPage(w http.ResponseWriter, r *http.Request) {
	dmProjectID, ok := reqParam(w, r, "dmProjectId")
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
	md, err := api.DownloadWikiPage(ctx, token, dmProjectID, itemID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, WikiPageContentDTO{ItemID: itemID, Markdown: md})
}

// handleWikiPublish uploads a page's markdown to the project's Wiki folder as
// "<slug>.md" (creating the folder/item on first publish, or a new version), and
// returns the resulting page. A 409 means the page moved upstream since the draft
// was opened — the client can retry with force=true to overwrite.
// POST /api/wiki/publish  (JSON WikiPublishRequest)
func (s *Server) handleWikiPublish(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	var req WikiPublishRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid publish body")
		return
	}
	if req.HubID == "" || req.DMProjectID == "" || req.Slug == "" {
		writeError(w, http.StatusBadRequest, "hubId, dmProjectId and slug are required")
		return
	}
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	if !hubMatches(w, set.hubID, req.HubID) {
		return
	}
	dmHubID, err := api.GetHubDataManagementID(ctx, token, req.HubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := api.PublishWikiPage(ctx, token, dmHubID, req.DMProjectID, req.ItemID, req.Slug, req.Markdown, req.BaseVersion, req.Force)
	if err != nil {
		if errors.Is(err, api.ErrWikiConflict) {
			writeError(w, http.StatusConflict, "this page changed upstream; refresh or overwrite")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wikiPageDTO(page))
}

// handleWikiRename renames a published page's file to "<newSlug>.md" (and its
// images subfolder to match). The lineage id is unchanged, so a linked draft
// keeps its baseItemId/baseVersion.
// POST /api/wiki/rename  (JSON WikiRenameRequest)
func (s *Server) handleWikiRename(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	var req WikiRenameRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid rename body")
		return
	}
	if req.HubID == "" || req.DMProjectID == "" || req.ItemID == "" || req.NewSlug == "" {
		writeError(w, http.StatusBadRequest, "hubId, dmProjectId, itemId and newSlug are required")
		return
	}
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	if !hubMatches(w, set.hubID, req.HubID) {
		return
	}
	dmHubID, err := api.GetHubDataManagementID(ctx, token, req.HubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := api.RenameWikiPage(ctx, token, dmHubID, req.DMProjectID, req.ItemID, req.OldSlug, req.NewSlug); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wikiPageDTO(api.WikiPage{ItemID: req.ItemID, Name: req.NewSlug + ".md"}))
}

// handleWikiImageUpload stores an uploaded image under Wiki/<slug>/images/ and
// returns its lineage urn for referencing via GET /api/wiki/image.
// POST /api/wiki/image  (multipart: hubId, dmProjectId, slug, file)
func (s *Server) handleWikiImageUpload(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload")
		return
	}
	hubID := r.FormValue("hubId")
	dmProjectID := r.FormValue("dmProjectId")
	slug := r.FormValue("slug")
	if hubID == "" || dmProjectID == "" || slug == "" {
		writeError(w, http.StatusBadRequest, "hubId, dmProjectId and slug are required")
		return
	}
	// The multipart hubId must be the session hub (query-param hub ids are
	// checked centrally in requireHub; form fields are checked here).
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	if !hubMatches(w, set.hubID, hubID) {
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 32<<20))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := path.Base(hdr.Filename)
	if name == "" || name == "." || name == "/" {
		name = "image"
	}
	dmHubID, err := api.GetHubDataManagementID(ctx, token, hubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	itemID, err := api.UploadWikiImage(ctx, token, dmHubID, dmProjectID, slug, name, data)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, WikiImageResult{ItemID: itemID, Name: name})
}

// handleWikiImage streams a wiki image (or other asset) item's tip bytes. It is
// the src target for images embedded in page markdown, so it's fetched as a
// same-origin subresource carrying the session cookie.
// GET /api/wiki/image?dmProjectId=<altId>&itemId=<lineage urn>
func (s *Server) handleWikiImage(w http.ResponseWriter, r *http.Request) {
	dmProjectID, ok := reqParam(w, r, "dmProjectId")
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
	data, ct, err := api.DownloadWikiAsset(ctx, token, dmProjectID, itemID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(data)
}
