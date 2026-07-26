package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/backup"
	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/config"
	"github.com/schneik80/fusionlocalserver/pins"
	"github.com/schneik80/fusionlocalserver/production"
	"github.com/schneik80/fusionlocalserver/tasks"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// Backup endpoints back the Settings console's Backups tool. Same gating
// posture as the rest of /api/admin: any authenticated session (single-user
// local server), destructive steps confirm in the UI.

// BackupConfigDTO is the backup configuration slice of server.json — both the
// GET/POST /api/admin/backups/config payload and the config half of
// GET /api/admin/backups.
type BackupConfigDTO struct {
	BackupDir     string `json:"backupDir"`
	BackupTime    string `json:"backupTime"`
	BackupEnabled bool   `json:"backupEnabled"`
}

// BackupSummaryDTO is one snapshot row in the Backups table.
type BackupSummaryDTO struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	CreatedAt  string `json:"createdAt"`
	AppVersion string `json:"appVersion"`
	FileCount  int    `json:"fileCount"`
	TotalBytes int64  `json:"totalBytes"`
	Warning    string `json:"warning,omitempty"`
}

// BackupListDTO is GET /api/admin/backups: current config + all snapshots,
// newest first.
type BackupListDTO struct {
	Config  BackupConfigDTO    `json:"config"`
	Backups []BackupSummaryDTO `json:"backups"`
}

// FsDirsDTO is GET /api/admin/fs/dirs — one directory level for the backup
// folder picker. Dirs only, never files.
type FsDirsDTO struct {
	Path   string   `json:"path"`
	Parent string   `json:"parent"`
	Dirs   []string `json:"dirs"`
}

// backupConfigFromSettings extracts the config DTO, applying the default time.
func backupConfigFromSettings(set Settings) BackupConfigDTO {
	tm := set.BackupTime
	if tm == "" {
		tm = backup.DefaultTime
	}
	return BackupConfigDTO{BackupDir: set.BackupDir, BackupTime: tm, BackupEnabled: set.BackupEnabled}
}

func backupSummaryDTO(s backup.Summary) BackupSummaryDTO {
	return BackupSummaryDTO{
		Path:       s.Path,
		Kind:       string(s.Kind),
		CreatedAt:  fmtTime(s.CreatedAt),
		AppVersion: s.AppVersion,
		FileCount:  s.FileCount,
		TotalBytes: s.TotalBytes,
		Warning:    s.Warning,
	}
}

// backupEngine returns the current engine (nil when no backup dir is set).
func (s *Server) backupEngine() *backup.Engine {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	return s.backups
}

// reloadBackupEngine rebuilds the engine from the persisted settings —
// called at startup and after every config change. Sources are strictly
// allow-list: the four store snapshots, the pins glob, and the redacted
// config pair. Nothing else under the config dir is ever read.
func (s *Server) reloadBackupEngine() {
	set, err := LoadSettings()
	if err != nil {
		s.logger.Warn("backup: settings load failed; engine disabled", "err", err)
		set = Settings{}
	}
	var eng *backup.Engine
	if set.BackupDir != "" {
		if dir, derr := config.Dir(); derr != nil {
			s.logger.Warn("backup: disabled (config dir unavailable)", "err", derr)
		} else {
			srcs := []backup.Source{}
			if s.chat != nil {
				srcs = append(srcs, backup.StoreSource("chat", s.chat.Snapshot))
			}
			if s.tasks != nil {
				srcs = append(srcs, backup.StoreSource("tasks", s.tasks.Snapshot))
			}
			if s.production != nil {
				srcs = append(srcs, backup.StoreSource("production", s.production.Snapshot))
			}
			if s.whiteboards != nil {
				srcs = append(srcs, backup.StoreSource("whiteboards", s.whiteboards.Snapshot))
			}
			srcs = append(srcs, backup.PinsSource(dir), backup.ConfigSource(dir))
			eng = &backup.Engine{Dir: set.BackupDir, Sources: srcs, AppVersion: s.opts.Version}
		}
	}
	s.backupMu.Lock()
	s.backups = eng
	s.backupMu.Unlock()
}

// pokeBackupScheduler wakes the scheduler to recompute its timer. Coalescing
// (cap-1 buffer) — a pending poke is as good as two.
func (s *Server) pokeBackupScheduler() {
	if s.backupPoke == nil {
		return
	}
	select {
	case s.backupPoke <- struct{}{}:
	default:
	}
}

// runBackupScheduler fires a daily backup at the configured HH:MM local time,
// then recomputes the next occurrence. There is deliberately NO missed-window
// catch-up (settled decision): a server that was down at 03:30 backs up at
// the next 03:30, not at startup. Idles while disabled/unconfigured; a poke
// (config change) re-evaluates immediately; exits with ctx.
func (s *Server) runBackupScheduler(ctx context.Context) {
	for {
		set, err := LoadSettings()
		if err != nil || !set.BackupEnabled || s.backupEngine() == nil {
			select {
			case <-ctx.Done():
				return
			case <-s.backupPoke:
				continue
			}
		}
		next := backup.NextRun(time.Now(), set.BackupTime)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.backupPoke:
			timer.Stop()
			continue
		case <-timer.C:
			eng := s.backupEngine()
			if eng == nil {
				continue
			}
			if m, rerr := eng.Run(backup.KindDaily); rerr != nil {
				s.logger.Error("backup: scheduled daily backup failed", "err", rerr)
			} else {
				s.logger.Info("backup: scheduled daily backup complete",
					"files", len(m.Files), "dir", eng.Dir)
			}
		}
	}
}

// handleAdminBackups lists every snapshot plus the current config.
func (s *Server) handleAdminBackups(w http.ResponseWriter, r *http.Request) {
	set, err := LoadSettings()
	if err != nil {
		s.fail(w, r, fmt.Errorf("loading settings: %w", err))
		return
	}
	out := BackupListDTO{Config: backupConfigFromSettings(set), Backups: []BackupSummaryDTO{}}
	if eng := s.backupEngine(); eng != nil {
		sums, lerr := eng.List()
		if lerr != nil {
			s.fail(w, r, fmt.Errorf("listing backups: %w", lerr))
			return
		}
		for _, sum := range sums {
			out.Backups = append(out.Backups, backupSummaryDTO(sum))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminBackupRun performs a manual backup now.
func (s *Server) handleAdminBackupRun(w http.ResponseWriter, r *http.Request) {
	eng := s.backupEngine()
	if eng == nil {
		writeError(w, http.StatusServiceUnavailable, "no backup directory configured")
		return
	}
	m, err := eng.Run(backup.KindManual)
	if err != nil {
		s.logger.Error("backup: manual backup failed", "err", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("backup failed: %v", err))
		return
	}
	s.logger.Info("backup: manual backup complete", "files", len(m.Files), "dir", eng.Dir)
	var total int64
	for _, f := range m.Files {
		total += f.Size
	}
	writeJSON(w, http.StatusOK, BackupSummaryDTO{
		Path:       string(m.Kind) + "/" + m.CreatedAt.Format("20060102-150405"),
		Kind:       string(m.Kind),
		CreatedAt:  fmtTime(m.CreatedAt),
		AppVersion: m.AppVersion,
		FileCount:  len(m.Files),
		TotalBytes: total,
	})
}

// handleAdminBackupConfigGet returns the persisted backup configuration.
func (s *Server) handleAdminBackupConfigGet(w http.ResponseWriter, r *http.Request) {
	set, err := LoadSettings()
	if err != nil {
		s.fail(w, r, fmt.Errorf("loading settings: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, backupConfigFromSettings(set))
}

// handleAdminBackupConfigSet validates and persists the backup configuration,
// then rebuilds the engine and reschedules.
func (s *Server) handleAdminBackupConfigSet(w http.ResponseWriter, r *http.Request) {
	var req BackupConfigDTO
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.BackupTime == "" {
		req.BackupTime = backup.DefaultTime
	}
	if !backup.ValidTime(req.BackupTime) {
		writeError(w, http.StatusBadRequest, "backupTime must be HH:MM (24-hour)")
		return
	}
	req.BackupDir = strings.TrimSpace(req.BackupDir)
	if req.BackupDir == "" {
		if req.BackupEnabled {
			writeError(w, http.StatusBadRequest, "a backup directory is required to enable backups")
			return
		}
	} else {
		if !filepath.IsAbs(req.BackupDir) {
			writeError(w, http.StatusBadRequest, "backupDir must be an absolute path")
			return
		}
		req.BackupDir = filepath.Clean(req.BackupDir)
		if err := os.MkdirAll(req.BackupDir, 0700); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("backup directory is not creatable: %v", err))
			return
		}
		// Write probe: MkdirAll succeeding doesn't prove an existing dir is
		// writable (read-only mounts, permissions).
		probe, err := os.CreateTemp(req.BackupDir, ".fls-write-probe-*")
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("backup directory is not writable: %v", err))
			return
		}
		probe.Close()
		_ = os.Remove(probe.Name())
	}

	if err := UpdateSettings(func(set *Settings) {
		set.BackupDir = req.BackupDir
		set.BackupTime = req.BackupTime
		set.BackupEnabled = req.BackupEnabled
	}); err != nil {
		s.fail(w, r, fmt.Errorf("saving backup settings: %w", err))
		return
	}
	s.reloadBackupEngine()
	s.pokeBackupScheduler()
	s.logger.Info("backup: configuration updated",
		"dir", req.BackupDir, "time", req.BackupTime, "enabled", req.BackupEnabled)
	writeJSON(w, http.StatusOK, req)
}

// ---- verify + restore (phase 3c) ----

// BackupVerifyRequest is POST /api/admin/backups/verify: the snapshot to
// check, as the backup-dir-relative path List() reported (e.g.
// "manual/20260726-033000").
type BackupVerifyRequest struct {
	Path string `json:"path"`
}

// BackupFileResultDTO mirrors backup.FileResult for one file.
type BackupFileResultDTO struct {
	Path      string `json:"path"`
	HashOK    bool   `json:"hashOK"`
	ParseOK   bool   `json:"parseOK"`
	VersionOK bool   `json:"versionOK"`
	Missing   bool   `json:"missing"`
	Detail    string `json:"detail,omitempty"`
}

// BackupVerifyReportDTO is the verify response: manifest header + per-file
// results. Path echoes the request's backup-dir-relative path.
type BackupVerifyReportDTO struct {
	Path      string                `json:"path"`
	Kind      string                `json:"kind"`
	CreatedAt string                `json:"createdAt"`
	OK        bool                  `json:"ok"`
	Files     []BackupFileResultDTO `json:"files"`
}

// BackupRestoreRequest is POST /api/admin/backups/restore. Confirm must
// equal the snapshot's timestamp directory name — the typed confirmation is
// enforced server-side too, so no client bug can restore unconfirmed.
type BackupRestoreRequest struct {
	Path    string `json:"path"`
	Confirm string `json:"confirm"`
}

// BackupRestoreResponse mirrors SetPortResponse's restart contract: the
// client shows a reconnect screen and reloads.
type BackupRestoreResponse struct {
	Restarting bool `json:"restarting"`
}

// expectedSchemaVersion maps a manifest entry (source name + rel path) to
// the schema version this build currently writes for that file — the hook
// backup.Verify/Restore use to refuse data from a newer build. Chat splits
// per basename (meta/cursors/message-log versions advance independently);
// whiteboard doc-*.json files are opaque tldraw documents with no schema
// authority here, and config/server.json carry no version at all — those
// report ok=false and are exempt from the version check.
func expectedSchemaVersion(store, rel string) (int, bool) {
	base := path.Base(rel)
	switch store {
	case "chat":
		switch {
		case base == "meta.json":
			return chat.MetaVersion(), true
		case base == "cursors.json":
			return chat.CursorsVersion(), true
		case strings.HasPrefix(base, "msg-") && strings.HasSuffix(base, ".jsonl"):
			return chat.RecordVersion(), true
		}
	case "tasks":
		if base == "tasks.json" {
			return tasks.CurrentVersion(), true
		}
	case "production":
		if base == "production.json" {
			return production.CurrentVersion(), true
		}
	case "whiteboards":
		if base == "whiteboards.json" {
			return whiteboards.CurrentVersion(), true
		}
	case "pins":
		if strings.HasPrefix(base, "pins-") && strings.HasSuffix(base, ".json") {
			return pins.CurrentVersion(), true
		}
	}
	return 0, false
}

// snapshotDirForPath resolves a backup-dir-relative snapshot path, refusing
// anything that escapes the backup directory (absolute paths, ..
// traversal). The path is client input naming a directory we will read and
// restore from — it must never point anywhere else.
func snapshotDirForPath(backupDir, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(rel) || path.IsAbs(filepath.ToSlash(rel)) {
		return "", errors.New("path must be relative to the backup directory")
	}
	full := filepath.Join(backupDir, filepath.FromSlash(rel))
	r, err := filepath.Rel(backupDir, full)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the backup directory")
	}
	return full, nil
}

// handleAdminBackupVerify re-checks one snapshot against its manifest:
// hashes, parseability, schema versions, missing and stray files.
func (s *Server) handleAdminBackupVerify(w http.ResponseWriter, r *http.Request) {
	eng := s.backupEngine()
	if eng == nil {
		writeError(w, http.StatusServiceUnavailable, "no backup directory configured")
		return
	}
	var req BackupVerifyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	snapDir, err := snapshotDirForPath(eng.Dir, req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rep, err := backup.Verify(snapDir, expectedSchemaVersion)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("verify failed: %v", err))
		return
	}
	out := BackupVerifyReportDTO{
		Path:      req.Path,
		Kind:      string(rep.Kind),
		CreatedAt: fmtTime(rep.CreatedAt),
		OK:        rep.OK,
		Files:     make([]BackupFileResultDTO, 0, len(rep.Files)),
	}
	for _, f := range rep.Files {
		out.Files = append(out.Files, BackupFileResultDTO{
			Path:      f.Path,
			HashOK:    f.HashOK,
			ParseOK:   f.ParseOK,
			VersionOK: f.VersionOK,
			Missing:   f.Missing,
			Detail:    f.Detail,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminBackupRestore restores one snapshot over the live data:
// typed-confirmation check, engine.Restore (validate → pre-restore snapshot
// → copy), then store-cache eviction and a listener restart. The restart
// reuses handleSetPort's mechanism — reply first, rebind ~0.5s later — and
// matters beyond UX symmetry: the rebind loop does NOT recreate the stores,
// so the explicit Reset() calls here are what stops a stale in-memory cache
// from rewriting pre-restore data. Pins have no cache (Load per request)
// and sessions.enc is never in a backup, so neither needs touching.
func (s *Server) handleAdminBackupRestore(w http.ResponseWriter, r *http.Request) {
	eng := s.backupEngine()
	if eng == nil {
		writeError(w, http.StatusServiceUnavailable, "no backup directory configured")
		return
	}
	var req BackupRestoreRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	snapDir, err := snapshotDirForPath(eng.Dir, req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Confirm == "" || req.Confirm != filepath.Base(snapDir) {
		writeError(w, http.StatusBadRequest, "confirmation does not match the snapshot name")
		return
	}
	dir, err := config.Dir()
	if err != nil {
		s.fail(w, r, fmt.Errorf("resolving config dir: %w", err))
		return
	}
	if err := eng.Restore(snapDir, dir, expectedSchemaVersion); err != nil {
		s.logger.Error("backup: restore failed", "path", req.Path, "err", err)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("restore failed: %v", err))
		return
	}

	// Files are replaced; drop every store's in-memory state before anything
	// can write stale data back over the restored files.
	if s.chat != nil {
		s.chat.Reset()
	}
	if s.tasks != nil {
		s.tasks.Reset()
	}
	if s.production != nil {
		s.production.Reset()
	}
	if s.whiteboards != nil {
		s.whiteboards.Reset()
	}

	s.logger.Info("backup: restore complete — restarting listener", "path", req.Path)
	writeJSON(w, http.StatusOK, BackupRestoreResponse{Restarting: true})

	// Rebind after the response has flushed (handleSetPort precedent).
	time.AfterFunc(500*time.Millisecond, s.requestRestart)
}

// handleAdminFsDirs lists the subdirectories of one directory for the backup
// folder picker. Directories only — files are never listed — and dot-prefixed
// (hidden) dirs are excluded. An empty path starts at the user's home.
func (s *Server) handleAdminFsDirs(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			s.fail(w, r, fmt.Errorf("resolving home dir: %w", err))
			return
		}
		p = home
	}
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		writeError(w, http.StatusBadRequest, "path must be absolute")
		return
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot list directory: %v", err))
		return
	}
	dirs := []string{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirs = append(dirs, e.Name())
	}
	sort.Strings(dirs)
	parent := filepath.Dir(p)
	if parent == p {
		parent = "" // filesystem root — nothing above
	}
	writeJSON(w, http.StatusOK, FsDirsDTO{Path: p, Parent: parent, Dirs: dirs})
}
