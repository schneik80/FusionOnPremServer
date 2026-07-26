package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/schneik80/fusionlocalserver/config"
)

// Admin endpoints back the Settings console's Uptime and Logs tools. Gating
// is the standard authenticated session (single-user local server posture —
// a settled product decision); destructive admin operations layer typed
// confirmations in the UI, not extra server-side roles.

// logTailDefault/-Max bound GET /api/admin/log reads: the UI wants the
// recent tail, not a 10 MB download (that's what ?download=1 is for).
const (
	logTailDefault = 64 << 10  // 64 KiB
	logTailMax     = 512 << 10 // 512 KiB
)

// AdminStatusDTO is GET /api/admin/status.
type AdminStatusDTO struct {
	StartedAt      string `json:"startedAt"`
	UptimeSeconds  int64  `json:"uptimeSeconds"`
	RequestsServed int64  `json:"requestsServed"`
	Version        string `json:"version"`
	GoVersion      string `json:"goVersion"`
	LogPath        string `json:"logPath"`
	LogSizeBytes   int64  `json:"logSizeBytes"`
}

// handleAdminStatus reports process uptime and basic counters.
func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	out := AdminStatusDTO{
		StartedAt:      s.startedAt.UTC().Format(time.RFC3339),
		UptimeSeconds:  int64(time.Since(s.startedAt).Seconds()),
		RequestsServed: s.requestCount.Load(),
		Version:        s.opts.Version,
		GoVersion:      runtime.Version(),
	}
	if dir, err := config.Dir(); err == nil {
		path := filepath.Join(dir, logFileName)
		out.LogPath = path
		if info, err := os.Stat(path); err == nil {
			out.LogSizeBytes = info.Size()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminLog serves the server log: by default the last tailBytes of
// the current file as text/plain (aligned to a line start), or the whole
// file as an attachment with ?download=1.
func (s *Server) handleAdminLog(w http.ResponseWriter, r *http.Request) {
	dir, err := config.Dir()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	path := filepath.Join(dir, logFileName)
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "no server log file exists yet")
		return
	}
	defer f.Close()

	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf("attachment; filename=%q", "server-"+time.Now().UTC().Format("20060102-150405")+".log"))
		http.ServeContent(w, r, "", time.Time{}, f)
		return
	}

	info, err := f.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tail := int64(logTailDefault)
	if v := r.URL.Query().Get("tailBytes"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "tailBytes must be a positive integer")
			return
		}
		if n > logTailMax {
			n = logTailMax
		}
		tail = n
	}
	offset := info.Size() - tail
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, 0); err != nil {
		s.fail(w, r, err)
		return
	}
	buf := make([]byte, info.Size()-offset)
	n, _ := f.Read(buf)
	buf = buf[:n]
	// A mid-line start reads as corruption in a log viewer; skip to the
	// first line boundary unless we're at the true start of the file.
	if offset > 0 {
		for i, b := range buf {
			if b == '\n' {
				buf = buf[i+1:]
				break
			}
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(buf)
}
