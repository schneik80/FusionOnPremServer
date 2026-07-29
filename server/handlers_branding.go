package server

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
)

// logoCSP locks the logo response down to nothing. It matters for SVG: an
// uploaded SVG is a document, and served from our own origin someone who
// navigates straight at this URL would render it as one. The app-wide CSP
// already blocks inline script, but this response has no reason to be able to
// load or run anything at all, so it says so.
const logoCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// handleBrandingLogo streams the configured sign-in logo. PUBLIC BY NECESSITY:
// the sign-in screen renders before there is a session, so this cannot require
// one. Nothing confidential belongs in the logo (see branding.go).
//
// Caching is keyed on the content hash. A request carrying the current version
// in ?v= is answered immutable — that URL's bytes can never change, because
// changing the logo changes the hash and therefore the URL. Anything else gets
// a revalidating response with the same ETag, so a stale client still costs
// one 304 rather than a full body.
// GET /api/branding/logo?v=<version>
func (s *Server) handleBrandingLogo(w http.ResponseWriter, r *http.Request) {
	meta, data, ok := s.logo.get()
	if !ok {
		writeError(w, http.StatusNotFound, "no sign-in logo is configured")
		return
	}
	h := w.Header()
	h.Set("Content-Type", meta.ContentType)
	h.Set("ETag", `"`+meta.Version()+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", logoCSP)
	if r.URL.Query().Get("v") == meta.Version() {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "public, no-cache")
	}
	// ServeContent handles conditional requests (the ETag above) and ranges.
	http.ServeContent(w, r, "logo"+meta.Ext, meta.UpdatedAt, bytes.NewReader(data))
}

// handleBrandingLogoSet stores an uploaded logo. Authenticated, matching the
// posture of the other server-wide setting (the listen port): any signed-in
// user can change it, and the change is visible to everyone the server serves,
// including on the sign-in screen before they log in.
// POST /api/branding/logo  (multipart: file)
func (s *Server) handleBrandingLogoSet(w http.ResponseWriter, r *http.Request) {
	// Bound the whole request, not just the part: a multipart body can carry
	// any number of fields, and the cap has to hold before anything is read.
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+(1<<16))
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "logo_unsupported", "missing or unreadable file")
		return
	}
	defer file.Close()

	data, err := readLogoUpload(file)
	if err != nil {
		writeErrorCode(w, http.StatusRequestEntityTooLarge, "logo_too_large",
			fmt.Sprintf("the logo must be %d bytes or smaller", maxLogoBytes))
		return
	}
	if len(data) > maxLogoBytes {
		writeErrorCode(w, http.StatusRequestEntityTooLarge, "logo_too_large",
			fmt.Sprintf("the logo must be %d bytes or smaller", maxLogoBytes))
		return
	}

	meta, err := SaveLogo(data)
	if errors.Is(err, errUnsupportedLogo) {
		writeErrorCode(w, http.StatusBadRequest, "logo_unsupported",
			"the file is not a PNG, JPEG, GIF, WebP or SVG image")
		return
	}
	if err != nil {
		s.fail(w, r, fmt.Errorf("saving sign-in logo: %w", err))
		return
	}
	s.logo.set(meta, data)
	writeJSON(w, http.StatusOK, logoDTO(&meta))
}

// handleBrandingLogoDelete removes the logo; the sign-in screen falls back to
// the built-in mark. Deleting when none is set succeeds.
// DELETE /api/branding/logo
func (s *Server) handleBrandingLogoDelete(w http.ResponseWriter, r *http.Request) {
	if err := DeleteLogo(); err != nil {
		s.fail(w, r, fmt.Errorf("removing sign-in logo: %w", err))
		return
	}
	s.logo.clear()
	w.WriteHeader(http.StatusNoContent)
}
