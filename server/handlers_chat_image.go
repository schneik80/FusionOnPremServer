package server

import (
	"io"
	"net/http"
	"path"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
)

// chatImageMaxBytes caps a chat attachment image. Screenshots from the Fusion
// palette are downscaled PNGs well under 1 MiB; 8 MiB leaves room for pasted
// photos without letting the RAM-buffered upload grow unbounded.
const chatImageMaxBytes = 8 << 20

// Indirections over the APS calls, stubbed in tests (matching the fetchHubs
// seam style). Production code never reassigns them.
var (
	chatImageHubDMID = api.GetHubDataManagementID
	chatImageUpload  = api.UploadChatImage
)

// handleChatImageUpload stores a chat attachment image under Chat/images/ in
// the project (an ordinary Fusion Team item — access control, storage and
// retention are the project's) and returns its lineage urn. The message then
// references it with an fls:img token; bytes are served by the same
// tip-download endpoint wiki images use (GET /api/wiki/image).
// POST /api/chat/image?projectId=&hubId=&dmProjectId=  (multipart: file)
func (s *Server) handleChatImageUpload(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	// hubId in the query is validated centrally by requireHub; dmProjectId is
	// only used APS-side, where the caller's own token scopes what it reaches.
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
	// Attaching is posting: same capability the composer needs.
	if !s.chatCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	if !s.chatImgLim.Allow(c.sessID) {
		writeError(w, http.StatusTooManyRequests, safeErrorMessage(http.StatusTooManyRequests))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, chatImageMaxBytes+(64<<10))
	if err := r.ParseMultipartForm(chatImageMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()
	// Read one byte past the cap so an oversize file errors instead of being
	// silently truncated into a corrupt image.
	data, err := io.ReadAll(io.LimitReader(file, chatImageMaxBytes+1))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(data) > chatImageMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "image exceeds the size limit")
		return
	}
	name := path.Base(hdr.Filename)
	if name == "" || name == "." || name == "/" {
		name = "image"
	}

	dmHubID, err := chatImageHubDMID(ctx, c.token, hubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	itemID, err := chatImageUpload(ctx, c.token, dmHubID, dmProjectID, name, data)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ChatImageDTO{ItemID: itemID, Name: name})
}
