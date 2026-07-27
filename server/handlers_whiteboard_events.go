package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/internal/sse"
)

// handleWhiteboardEvents is GET /api/whiteboards/events?projectId&boardId —
// the board's awareness stream. It carries two things:
//
//   - doc.changed (durable): someone saved this board. A canvas that is behind
//     learns immediately instead of finding out when its own next save is
//     refused, which is the difference between "someone else is working here"
//     and "your last few minutes are in limbo".
//   - peers (ephemeral): who has the board open, published whenever the roster
//     changes. Ephemeral because presence is only true right now — a replayed
//     copy would assert someone is here who left.
//
// The shape is handleChatEvents': the capability check is bounded by the
// handler timeout and then explicitly cancelled so the stream lives on
// r.Context(), and the keepalive tick doubles as the revocation check, so a
// member who loses project access mid-session has their stream torn down
// rather than keeping a window onto the board.
//
// There is no per-frame entitlement filter, unlike chat: a board carries no
// ACL of its own, so passing CapRead for the project at subscribe time (plus
// the revocation tick) IS the rule.
func (s *Server) handleWhiteboardEvents(w http.ResponseWriter, r *http.Request) {
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
	{
		ctx, cancel := s.reqCtx(r)
		allowed := s.whiteboardCan(ctx, w, r, c, chat.CapRead)
		cancel()
		if !allowed {
			return
		}
	}
	// The board must exist — otherwise a typo silently opens a stream on a
	// room nobody will ever publish to, and the client waits forever.
	if _, _, err := c.store.Document(c.projectID, boardID); err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// Whether this subscriber may draw decides which avatar the others see, not
	// what this stream delivers: a read-only member still receives every event.
	canWrite := false
	{
		ctx, cancel := s.reqCtx(r)
		allowed, err := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, chat.CapPost)
		cancel()
		if err == nil {
			canWrite = allowed
		}
	}

	hub := set.whiteboardHub
	room := wbRoom(c.projectID, boardID)
	sub, replay, reset, err := hub.Subscribe(room, r.Header.Get("Last-Event-ID"))
	if err != nil {
		s.whiteboardError(w, r, err)
		return
	}
	defer hub.Unsubscribe(room, sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(format string, args ...any) bool {
		if _, werr := fmt.Fprintf(w, format, args...); werr != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if reset {
		if !write("event: reset\ndata: {}\n\n") {
			return
		}
	} else {
		for _, f := range replay {
			if !writeFrame(write, f) {
				return
			}
		}
	}
	if !write(": connected\n\n") {
		return
	}

	// Announce arrival, and departure however this handler returns. The
	// subscription id is the pointer identity of the subscriber, so two tabs of
	// one session are two subscriptions (and collapse to one avatar in the
	// roster, which dedupes by user).
	subID := fmt.Sprintf("%p", sub)
	me := wbPeer{UserID: c.id.UserID, Name: c.name, Color: peerColor(c.id.UserID), CanWrite: canWrite}
	hub.publishPeers(room, hub.join(room, subID, me))
	defer func() { hub.publishPeers(room, hub.leave(room, subID)) }()

	keepalive := s.chatKeepalive
	if keepalive <= 0 {
		keepalive = 25 * time.Second
	}
	tick := time.NewTicker(keepalive)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.Closed():
			return
		case f := <-sub.Events():
			if !writeFrame(write, f) {
				return
			}
		case <-tick.C:
			// Revocation: a role lapse tears the stream down. A roster fetch
			// error is NOT a revocation — the stream rides out APS blips.
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			allowed, aerr := s.chatAuthz.Can(ctx, c.token, c.id, c.projectID, chat.CapRead)
			cancel()
			if aerr == nil && !allowed {
				return
			}
			if !write(": ping\n\n") {
				return
			}
		}
	}
}

// writeFrame writes one frame, reporting false when the connection is done. A
// frame with no id is ephemeral: omitting the id line is what keeps it out of
// the client's Last-Event-ID and therefore out of any replay.
func writeFrame(write func(string, ...any) bool, f sse.Frame[wbVis]) bool {
	if f.ID == "" {
		return write("data: %s\n\n", f.Data)
	}
	return write("id: %s\ndata: %s\n\n", f.ID, f.Data)
}

// publishDocChanged tells a board's other viewers that its document moved on.
// Best-effort like chat's publish: the save has already succeeded, and a
// client that misses this still finds out when its own save is refused.
func (s *Server) publishDocChanged(set *storeSet, c whiteboardCtx, boardID string, rev int64) {
	if set.whiteboardHub == nil {
		return
	}
	_ = set.whiteboardHub.Publish(wbRoom(c.projectID, boardID), sse.Event{
		Type: "doc.changed",
		V:    1,
		Data: map[string]any{
			"rev": rev,
			"by":  wbPeer{UserID: c.id.UserID, Name: c.name, Color: peerColor(c.id.UserID), CanWrite: true},
		},
	}, wbVis{})
}
