package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// handleUses is polymorphic, mirroring the TUI's Uses tab:
//   - designs: occurrences of the component version (query: cvId)
//   - drawings: the source design the drawing was made from
//     (query: hubId, drawingItemId)
//
// Both shapes return a list of ComponentRef rows.
func (s *Server) handleUses(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cvID := q.Get("cvId")
	hubID := q.Get("hubId")
	drawingItemID := q.Get("drawingItemId")

	if cvID == "" && (hubID == "" || drawingItemID == "") {
		writeError(w, http.StatusBadRequest, "provide either cvId, or both hubId and drawingItemId")
		return
	}

	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}

	var (
		refs []api.ComponentRef
		err  error
	)
	if cvID != "" {
		refs, err = api.GetOccurrences(ctx, token, cvID)
	} else {
		refs, err = api.GetDrawingSource(ctx, token, hubID, drawingItemID)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, componentRefDTOs(refs))
}

// handleDescendants returns every distinct descendant component of a design
// (the recursive occurrence tree), deduped by owning design lineage. Backs the
// Activity tab's "roll up child changes", which needs all child documents.
func (s *Server) handleDescendants(w http.ResponseWriter, r *http.Request) {
	cvID, ok := reqParam(w, r, "cvId")
	if !ok {
		return
	}
	// A deep assembly's occurrence walk can far exceed the default per-call
	// timeout, so give it a generous dedicated budget to enumerate in full.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	refs, err := api.GetAllDescendants(ctx, token, cvID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, componentRefDTOs(refs))
}

// handleWhereUsed -> api.GetWhereUsed (query: cvId).
func (s *Server) handleWhereUsed(w http.ResponseWriter, r *http.Request) {
	cvID, ok := reqParam(w, r, "cvId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	refs, err := api.GetWhereUsed(ctx, token, cvID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, componentRefDTOs(refs))
}

// handleBOM -> api.GetBOM (query: cvId). Flat bill of materials; quantity is
// the occurrence count.
func (s *Server) handleBOM(w http.ResponseWriter, r *http.Request) {
	cvID, ok := reqParam(w, r, "cvId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	rows, err := api.GetBOM(ctx, token, cvID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, bomRowDTOs(rows))
}

// handleDrawings -> api.GetDrawingsForDesign (query: hubId, designItemId).
func (s *Server) handleDrawings(w http.ResponseWriter, r *http.Request) {
	hubID, ok := reqParam(w, r, "hubId")
	if !ok {
		return
	}
	designItemID, ok := reqParam(w, r, "designItemId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	refs, err := api.GetDrawingsForDesign(ctx, token, hubID, designItemID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, drawingRefDTOs(refs))
}

// handleClassify -> api.ClassifyAndThumbnail (query: cvId). The frontend fires
// one request per design row after a folder loads to upgrade the icon to
// assembly/part. The combined query also returns the thumbnail status/URL on
// the same componentVersion object, so we warm the thumbnail cache (and
// background-prefetch the bytes) off the same round trip — opening the design
// later then serves its thumbnail straight from cache. Concurrency is bounded
// inside the api package (classifySem caps at 8).
func (s *Server) handleClassify(w http.ResponseWriter, r *http.Request) {
	cvID, ok := reqParam(w, r, "cvId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	isAssembly, thumbStatus, thumbURL, err := api.ClassifyAndThumbnail(ctx, token, cvID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	s.thumbs.putMeta(cvID, thumbStatus, thumbURL)
	if thumbStatus == api.ThumbnailStatusSuccess && thumbURL != "" {
		s.warmThumbnail(cvID, thumbURL)
	}

	subtype := "part"
	if isAssembly {
		subtype = "assembly"
	}
	writeJSON(w, http.StatusOK, ClassifyDTO{
		ComponentVersionID: cvID,
		IsAssembly:         isAssembly,
		Subtype:            subtype,
	})
}

// handleThumbnail -> thumbnail generation status (query: cvId). The frontend
// polls this while PENDING. Cached terminal statuses (SUCCESS/FAILED) are
// served without an APS round trip — usually already warmed by classify — but
// PENDING is always re-queried so polling can observe it turning SUCCESS.
func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	cvID, ok := reqParam(w, r, "cvId")
	if !ok {
		return
	}
	if status, url, _, ok := s.thumbs.meta(cvID); ok &&
		(status == api.ThumbnailStatusSuccess || status == api.ThumbnailStatusFailed) {
		writeJSON(w, http.StatusOK, ThumbnailDTO{Status: status, SignedURL: url})
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	status, signedURL, err := api.GetThumbnail(ctx, token, cvID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.thumbs.putMeta(cvID, status, signedURL)
	writeJSON(w, http.StatusOK, ThumbnailDTO{Status: status, SignedURL: signedURL})
}

// handleThumbnailImage streams the thumbnail PNG itself (query: cvId), serving
// the browser a same-origin image instead of handing it the cross-origin APS
// signed URL. Bytes are cached (usually pre-warmed by classify), so repeat
// views and other clients are served from memory. A not-yet-ready or absent
// thumbnail returns 404 so the <img> onError can hide it.
func (s *Server) handleThumbnailImage(w http.ResponseWriter, r *http.Request) {
	cvID, ok := reqParam(w, r, "cvId")
	if !ok {
		return
	}
	if png, ctype, ok := s.thumbs.image(cvID); ok {
		writeImage(w, png, ctype)
		return
	}

	ctx, cancel := s.reqCtx(r)
	defer cancel()

	status, url, fresh, ok := s.thumbs.meta(cvID)
	// A cached terminal FAILED means there's no image to fetch — short-circuit
	// with 404 rather than re-spending an APS round trip to confirm.
	if ok && status == api.ThumbnailStatusFailed {
		writeError(w, http.StatusNotFound, "thumbnail not available")
		return
	}
	if !(ok && fresh && status == api.ThumbnailStatusSuccess) {
		token, ok := s.token(ctx, w, r)
		if !ok {
			return
		}
		st, u, err := api.GetThumbnail(ctx, token, cvID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.thumbs.putMeta(cvID, st, u)
		status, url = st, u
	}
	if status != api.ThumbnailStatusSuccess || url == "" {
		writeError(w, http.StatusNotFound, "thumbnail not available")
		return
	}

	png, ctype, err := api.FetchThumbnailImage(ctx, url)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.thumbs.putImage(cvID, png, ctype)
	writeImage(w, png, ctype)
}

// warmThumbnail fetches and caches a thumbnail's bytes in the background,
// bounded by warmSem. Best-effort: if the warmer is saturated or the fetch
// fails, the image proxy fetches on demand instead.
func (s *Server) warmThumbnail(cvID, url string) {
	if _, _, ok := s.thumbs.image(cvID); ok {
		return // already have the bytes
	}
	select {
	case s.warmSem <- struct{}{}:
	default:
		return // warmer busy; the proxy will fetch on demand
	}
	go func() {
		defer func() { <-s.warmSem }()
		ctx, cancel := context.WithTimeout(apsbudget.WithPriority(context.Background(), apsbudget.P2), 20*time.Second)
		defer cancel()
		png, ctype, err := api.FetchThumbnailImage(ctx, url)
		if err != nil {
			s.logger.Debug("thumbnail warm failed", "cvId", cvID, "err", err)
			return
		}
		s.thumbs.putImage(cvID, png, ctype)
	}()
}

// writeImage writes image bytes with a private caching header (it's the
// authenticated user's data, not shareable by intermediaries).
func writeImage(w http.ResponseWriter, data []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
