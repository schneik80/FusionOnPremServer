package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
)

// maxInlineFile bounds a full (non-range) file body the preview viewer will pull
// inline. Video and PDF fetch with a Range header and stream in windows, so this
// only caps the whole-object case (an image or text file loaded in one shot).
// Beyond it the handler replies 413 and the UI offers a plain download instead.
const maxInlineFile = 200 << 20 // 200 MiB

// fileStreamTimeout bounds a whole file transfer. Unlike the 30s handlerTimeout
// (sized for quick GraphQL/DM round-trips), a preview may stream hundreds of MiB
// of video, so it gets a download-appropriate cap. It still aborts if the client
// disconnects (the request context is the parent).
const fileStreamTimeout = 15 * time.Minute

// handleFile streams an uploaded file item's tip bytes for in-browser preview.
// It forwards the request's Range header to OSS so video/PDF can seek, mirroring
// the upstream 206 + Content-Range. Served same-origin (carries the session
// cookie), so the signed S3 url never reaches the browser.
// download=1 serves the same bytes as an attachment (the data card's download
// action) and skips the inline preview cap.
// GET /api/items/file?dmProjectId=<altId>&itemId=<lineage urn>[&download=1]
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	dmProjectID, ok := reqParam(w, r, "dmProjectId")
	if !ok {
		return
	}
	itemID, ok := reqParam(w, r, "itemId")
	if !ok {
		return
	}
	// A file transfer can far outlast a normal API call, so bound it with the
	// download timeout, not the 30s handler timeout, or a large video would be
	// cut off mid-stream.
	ctx, cancel := context.WithTimeout(r.Context(), fileStreamTimeout)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}

	resp, storedName, err := api.OpenFile(ctx, token, dmProjectID, itemID, r.Header.Get("Range"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer resp.Body.Close()

	// Content-Type is derived from the file name, and the response carries
	// X-Content-Type-Options: nosniff, so it must be right or the browser won't
	// render an <img>/<video>. Prefer the caller-supplied name (the item name,
	// always carrying the extension) — a stored version name can drop it.
	name := r.URL.Query().Get("name")
	if name == "" {
		name = storedName
	}

	// download=1 asks for the bytes as a file rather than a preview: it is a
	// deliberate user action (the data card's download button), so the inline
	// cap — which exists to stop a viewer pulling hundreds of MiB it can't show
	// — does not apply, and the response is an attachment.
	download := r.URL.Query().Get("download") == "1"

	// A whole-object response over the inline cap: don't pump hundreds of MiB
	// through the viewer — tell the client to download it instead. (Range
	// responses are windows, so they're never capped here.)
	if !download && resp.StatusCode == http.StatusOK && resp.ContentLength > maxInlineFile {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large to preview")
		return
	}

	h := w.Header()
	// Prefer the name-derived type (correct video/pdf/text labelling); fall back
	// to whatever S3 recorded only when the extension is unknown.
	ct := api.ContentTypeForName(name)
	if ct == "application/octet-stream" {
		if up := resp.Header.Get("Content-Type"); up != "" {
			ct = up
		}
	}
	h.Set("Content-Type", ct)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "private, max-age=3600")
	disposition := "inline"
	if download {
		disposition = "attachment"
	}
	h.Set("Content-Disposition", disposition+dispositionFilename(name))
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		h.Set("Content-Length", cl)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		h.Set("Content-Range", cr)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// dispositionFilename renders the "; filename=..." parameter for a
// Content-Disposition header, stripping characters that would break the quoted
// string (a stored file name is user-controlled).
func dispositionFilename(name string) string {
	name = strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(name)
	if name == "" {
		return ""
	}
	return `; filename="` + name + `"`
}
