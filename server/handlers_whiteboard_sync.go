package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/internal/sse"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// codeWhiteboardResync tells a client its patch could not be placed — it is
// working from a revision this board never had, or has drifted too far. The
// client re-reads the document and starts again from the revision it gets.
const codeWhiteboardResync = "whiteboard_resync"

// handleWhiteboardPatch is POST /api/whiteboards/patch?projectId&boardId — one
// client's tldraw RecordsDiff, folded to puts and removes.
//
// This is what makes two people editing one board work: instead of each canvas
// writing its whole document over whatever it last saw, every change is applied
// to one authoritative record map in a defined order and re-broadcast to the
// others. The server still never parses tldraw's schema — a diff is set/set/
// delete on a map of opaque JSON — with one narrow exception for bindings,
// documented in whiteboards/patch.go.
func (s *Server) handleWhiteboardPatch(w http.ResponseWriter, r *http.Request) {
	c, ok := s.whiteboardReq(w, r)
	if !ok {
		return
	}
	boardID, ok := reqParam(w, r, "boardId")
	if !ok {
		return
	}
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	// The client gate is an affordance; this is the boundary. A read-only
	// member receives every patch and may send none.
	if !s.whiteboardCan(ctx, w, r, c, chat.CapPost) {
		return
	}
	if !s.whiteboardPatchLim.Allow(c.sessID + "\x00" + boardID) {
		writeError(w, http.StatusTooManyRequests, "too many whiteboard edits; slow down")
		return
	}

	var req whiteboards.DocPatchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, whiteboards.MaxPatchBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid patch body")
		return
	}

	applied, err := set.whiteboardRooms.Apply(c.projectID, boardID, req, s.whiteboardUser(c))
	if err != nil {
		s.whiteboardSyncError(w, r, err)
		return
	}
	// Fan out only when something actually changed: a retry, or a patch whose
	// every record was refused, leaves the board where it was.
	if !patchEmpty(applied.Patch) {
		s.publishPatch(set, c, boardID, req.ClientID, applied)
	}
	writeJSON(w, http.StatusOK, WhiteboardPatchDTO{
		Rev:      applied.Rev,
		Rejected: nonNil(applied.Rejected),
	})
}

// publishPatch broadcasts an accepted patch to the board's room. Durable (it
// carries an SSE id), so a client that blinks can replay it rather than
// re-reading the whole document.
func (s *Server) publishPatch(set *storeSet, c whiteboardCtx, boardID, clientID string, applied whiteboards.Applied) {
	if set.whiteboardHub == nil {
		return
	}
	_ = set.whiteboardHub.Publish(wbRoom(c.projectID, boardID), sse.Event{
		Type: "patch",
		V:    1,
		Data: WhiteboardPatchEventDTO{
			Rev: applied.Rev,
			// The sender's own id rides along so it can ignore the echo: it has
			// already applied these changes, and re-applying them would undo any
			// newer local edit made in between.
			ClientID: clientID,
			Put:      applied.Patch.Put,
			Remove:   nonNil(applied.Patch.Remove),
		},
	}, wbVis{})
}

// whiteboardSyncError maps the room's failures. A conflict here is NOT the
// document endpoint's stale-save (which asks the user to choose); it means the
// client's view is unusable and it must re-read, so it gets its own code.
func (s *Server) whiteboardSyncError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, whiteboards.ErrConflict):
		writeErrorCode(w, http.StatusConflict, codeWhiteboardResync,
			"this whiteboard has moved on; reload it")
	case errors.Is(err, whiteboards.ErrBusy):
		writeError(w, http.StatusServiceUnavailable, "too many whiteboards are open for live editing")
	default:
		s.whiteboardError(w, r, err)
	}
}

func patchEmpty(p whiteboards.DocPatch) bool { return len(p.Put) == 0 && len(p.Remove) == 0 }

// nonNil keeps slices out of the JSON as `null` — the DTO convention.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
