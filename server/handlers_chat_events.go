package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/schneik80/fusionlocalserver/chat"
)

// handleChatEvents is GET /api/chat/events?projectId= — the SSE stream
// carrying every chat event for one project, multiplexed and filtered
// per-subscriber (docs/chat/PLAN.md phase 2). Data frames are unnamed (the
// client's EventSource.onmessage) and carry the {type, v, data} envelope;
// a named "reset" frame tells the client its Last-Event-ID cursor can't be
// resumed (server restarted, or the ring aged out) and it must refetch over
// REST before trusting the live stream again.
//
// The periodic ping doubles as the revocation check: when the caller's
// project role lapses from the authorizer's cache and no longer grants
// read, the stream closes — the SSE analog of the design doc's
// node.Unsubscribe on project removal.
func (s *Server) handleChatEvents(w http.ResponseWriter, r *http.Request) {
	c, ok := s.chatReq(w, r)
	if !ok {
		return
	}
	// Bound only the initial capability check — the stream itself lives on
	// r.Context(), not the 30s handler timeout.
	{
		ctx, cancel := s.reqCtx(r)
		allowed := s.chatCan(ctx, w, r, c, chat.CapRead)
		cancel()
		if !allowed {
			return
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	sub, replay, reset, err := c.hub.Subscribe(c.projectID, r.Header.Get("Last-Event-ID"))
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	defer c.hub.Unsubscribe(c.projectID, sub)

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
			if !s.writeEntitledFrame(r.Context(), write, c, f) {
				return
			}
		}
	}
	// Confirm liveness immediately so EventSource fires `open` even on a
	// quiet channel.
	if !write(": connected\n\n") {
		return
	}

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
			if !s.writeEntitledFrame(r.Context(), write, c, f) {
				return
			}
		case <-tick.C:
			// Revocation: a role lapse (removed from the project, dropped
			// below read) tears the stream down. A roster fetch error is
			// NOT a revocation — the stream rides out APS blips.
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

// writeEntitledFrame writes one frame if this subscriber may see it,
// reporting false when the connection is done. Frames the subscriber isn't
// entitled to are silently skipped; an entitlement check error skips the
// frame too (fail closed) without killing the stream.
func (s *Server) writeEntitledFrame(ctx context.Context, write func(string, ...any) bool, c chatCtx, f chat.Frame) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	entitled, err := c.hub.Entitled(checkCtx, c.token, c.id, c.projectID, f)
	cancel()
	if err != nil || !entitled {
		return err == nil || ctx.Err() == nil
	}
	if f.ID == "" {
		// Ephemeral frame (typing): no id, so it never advances the
		// client's Last-Event-ID and is never replayed.
		return write("data: %s\n\n", f.Data)
	}
	return write("id: %s\ndata: %s\n\n", f.ID, f.Data)
}
