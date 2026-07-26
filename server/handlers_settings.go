package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Configurable ports are restricted to the unprivileged range so a change can
// never require root to bind (and never collides with well-known ports).
const (
	minConfigurablePort = 1024
	maxConfigurablePort = 65535
)

// handleSetPort persists a new listen port (server.json) and triggers a
// listener rebind. The client must reconnect on the new port afterwards. Only
// available when the server owns the port (see Server.portConfigurable).
func (s *Server) handleSetPort(w http.ResponseWriter, r *http.Request) {
	if !s.portConfigurable {
		writeError(w, http.StatusConflict,
			"port is not configurable: the server is running in dev mode")
		return
	}

	var req SetPortRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Port < minConfigurablePort || req.Port > maxConfigurablePort {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("port must be between %d and %d", minConfigurablePort, maxConfigurablePort))
		return
	}

	// Unchanged → no-op. This also avoids the false "unavailable" the bind
	// pre-check below would report against our own live listener.
	if req.Port == s.currentPort() {
		writeJSON(w, http.StatusOK, SetPortResponse{Port: req.Port, Restarting: false})
		return
	}

	// Pre-check bindability so we reject a busy port with a clear 409 now,
	// rather than failing on rebind. Small TOCTOU window: the port could be
	// taken between here and the rebind, in which case the bind loop logs the
	// failure and the process exits.
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", req.Port))
	if err != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("port %d is unavailable: %v", req.Port, err))
		return
	}
	_ = ln.Close()

	if err := UpdateSettings(func(set *Settings) { set.Port = req.Port }); err != nil {
		s.fail(w, r, fmt.Errorf("saving port setting: %w", err))
		return
	}

	s.logger.Info("port change requested", "from", s.currentPort(), "to", req.Port)
	writeJSON(w, http.StatusOK, SetPortResponse{Port: req.Port, Restarting: true})

	// Rebind after the response has flushed. The graceful drain waits for this
	// connection, but the brief delay ensures the client has the JSON before
	// the listener begins closing.
	time.AfterFunc(500*time.Millisecond, s.requestRestart)
}
