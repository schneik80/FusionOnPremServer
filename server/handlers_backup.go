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
	"github.com/schneik80/fusionlocalserver/pins"
	"github.com/schneik80/fusionlocalserver/production"
	"github.com/schneik80/fusionlocalserver/tasks"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// Backup endpoints back the Settings console's Backups tool. Same gating
// posture as the rest of /api/admin: any authenticated session (single-user
// local server), destructive steps confirm in the UI. Backups are FULLY
// per-hub: each hub carries its own configuration (hubs/<slug>/backup.json —
// its own destination and schedule), its engine snapshots only that hub's
// stores + pins (plus the redacted global config pair), and its snapshot
// tree roots at <thatHub'sBackupDir>/<slug>/ — the slug subtree guards two
// hubs configured to one location from ever interleaving.

// BackupConfigDTO is one hub's backup configuration — both the GET/POST
// /api/admin/backups/config payload and the config half of
// GET /api/admin/backups. It always reads and writes the SESSION hub's file.
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

// backupConfigDTO renders a hub's backup config, applying the default time.
func backupConfigDTO(cfg hubBackupConfig) BackupConfigDTO {
	tm := cfg.BackupTime
	if tm == "" {
		tm = backup.DefaultTime
	}
	return BackupConfigDTO{BackupDir: cfg.BackupDir, BackupTime: tm, BackupEnabled: cfg.BackupEnabled}
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

// backupEngineFor builds the hub's snapshot engine from its own backup.json,
// on demand — there is no cached process-global engine. Returns (nil, nil)
// when the hub has no backup directory configured (callers answer 503, the
// existing posture). Sources are strictly allow-list AND strictly this hub's:
// the set's four store snapshots, the set's pins glob (set.root), and the
// redacted global config pair. The engine roots at <BackupDir>/<slug>/ so two
// hubs pointed at one location still land in disjoint trees.
func (s *Server) backupEngineFor(set *storeSet) (*backup.Engine, error) {
	cfg, err := loadHubBackupConfig(set.root)
	if err != nil {
		return nil, err
	}
	if cfg.BackupDir == "" {
		return nil, nil
	}
	srcs := []backup.Source{}
	if set.chat != nil {
		srcs = append(srcs, backup.StoreSource("chat", set.chat.Snapshot))
	}
	if set.tasks != nil {
		srcs = append(srcs, backup.StoreSource("tasks", set.tasks.Snapshot))
	}
	if set.production != nil {
		srcs = append(srcs, backup.StoreSource("production", set.production.Snapshot))
	}
	if set.whiteboards != nil {
		srcs = append(srcs, backup.StoreSource("whiteboards", set.whiteboards.Snapshot))
	}
	srcs = append(srcs, backup.PinsSource(set.root), backup.ConfigSource(s.hubs.configDir))
	return &backup.Engine{
		Dir:        filepath.Join(cfg.BackupDir, set.slug),
		Sources:    srcs,
		AppVersion: s.opts.Version,
	}, nil
}

// reqBackupEngine resolves the session hub's engine or writes the response
// itself: 503 when the hub has no backup directory configured, 500 on a
// config load failure.
func (s *Server) reqBackupEngine(w http.ResponseWriter, r *http.Request, set *storeSet) (*backup.Engine, bool) {
	eng, err := s.backupEngineFor(set)
	if err != nil {
		s.logger.Error("backup: config unavailable", "hub", set.hubID, "err", err)
		writeError(w, http.StatusInternalServerError, "backup configuration is unavailable")
		return nil, false
	}
	if eng == nil {
		writeError(w, http.StatusServiceUnavailable, "no backup directory configured")
		return nil, false
	}
	return eng, true
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

// runBackupScheduler fires each hub's daily backup at that hub's configured
// HH:MM local time. One loop computes the minimum next-fire time across every
// enabled hub profile on disk (a hub can have backups configured without
// having been opened this process run), sleeps until it, then runs every hub
// whose slot arrived — per-hub failures are logged and skipped, never
// stopping the other hubs. There is deliberately NO missed-window catch-up
// (settled decision): a server that was down at 03:30 backs up at the next
// 03:30, not at startup. _unassigned participates only if it carries its own
// enabled backup.json (default: none — quarantine is transient). Idles while
// nothing is enabled; a poke (config change) re-evaluates immediately; exits
// with ctx.
func (s *Server) runBackupScheduler(ctx context.Context) {
	for {
		now := time.Now()
		next := time.Time{}
		for _, slug := range s.hubs.diskHubSlugs() {
			cfg, err := loadHubBackupConfig(filepath.Join(s.hubs.configDir, "hubs", slug))
			if err != nil || !cfg.BackupEnabled || cfg.BackupDir == "" {
				continue
			}
			n := backup.NextRun(now, cfg.BackupTime)
			if next.IsZero() || n.Before(next) {
				next = n
			}
		}
		if next.IsZero() {
			select {
			case <-ctx.Done():
				return
			case <-s.backupPoke:
				continue
			}
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.backupPoke:
			timer.Stop()
			continue
		case <-timer.C:
			s.runDueHubBackups(now)
		}
	}
}

// runDueHubBackups runs a daily backup for every enabled hub whose scheduled
// time has arrived since `computedAt` (the instant the sleeping loop computed
// its timers). Each hub fails independently.
func (s *Server) runDueHubBackups(computedAt time.Time) {
	now := time.Now()
	for _, slug := range s.hubs.diskHubSlugs() {
		cfg, err := loadHubBackupConfig(filepath.Join(s.hubs.configDir, "hubs", slug))
		if err != nil || !cfg.BackupEnabled || cfg.BackupDir == "" {
			continue
		}
		if backup.NextRun(computedAt, cfg.BackupTime).After(now) {
			continue // this hub's slot hasn't arrived yet
		}
		set, serr := s.hubs.getBySlug(slug)
		if serr != nil {
			s.logger.Error("backup: scheduled backup skipped (hub unavailable)", "hub", slug, "err", serr)
			continue
		}
		eng, eerr := s.backupEngineFor(set)
		if eerr != nil || eng == nil {
			s.logger.Error("backup: scheduled backup skipped (engine unavailable)", "hub", slug, "err", eerr)
			continue
		}
		if m, rerr := eng.Run(backup.KindDaily); rerr != nil {
			s.logger.Error("backup: scheduled daily backup failed", "hub", slug, "err", rerr)
		} else {
			s.logger.Info("backup: scheduled daily backup complete",
				"hub", slug, "files", len(m.Files), "dir", eng.Dir)
		}
	}
}

// handleAdminBackups lists the session hub's snapshots plus its config.
func (s *Server) handleAdminBackups(w http.ResponseWriter, r *http.Request) {
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	cfg, err := loadHubBackupConfig(set.root)
	if err != nil {
		s.fail(w, r, fmt.Errorf("loading backup config: %w", err))
		return
	}
	out := BackupListDTO{Config: backupConfigDTO(cfg), Backups: []BackupSummaryDTO{}}
	if eng, eerr := s.backupEngineFor(set); eerr != nil {
		s.fail(w, r, fmt.Errorf("loading backup config: %w", eerr))
		return
	} else if eng != nil {
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

// handleAdminBackupRun performs a manual backup of the session hub now.
func (s *Server) handleAdminBackupRun(w http.ResponseWriter, r *http.Request) {
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	eng, ok := s.reqBackupEngine(w, r, set)
	if !ok {
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

// handleAdminBackupConfigGet returns the session hub's backup configuration.
func (s *Server) handleAdminBackupConfigGet(w http.ResponseWriter, r *http.Request) {
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	cfg, err := loadHubBackupConfig(set.root)
	if err != nil {
		s.fail(w, r, fmt.Errorf("loading backup config: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, backupConfigDTO(cfg))
}

// handleAdminBackupConfigSet validates and persists the SESSION HUB's backup
// configuration (hubs/<slug>/backup.json), then reschedules.
func (s *Server) handleAdminBackupConfigSet(w http.ResponseWriter, r *http.Request) {
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
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

	if err := saveHubBackupConfig(set.root, hubBackupConfig{
		BackupDir:     req.BackupDir,
		BackupTime:    req.BackupTime,
		BackupEnabled: req.BackupEnabled,
	}); err != nil {
		s.fail(w, r, fmt.Errorf("saving backup settings: %w", err))
		return
	}
	s.pokeBackupScheduler()
	s.logger.Info("backup: configuration updated", "hub", set.hubID,
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
// restore from — it must never point anywhere else. backupDir is already the
// session hub's OWN subtree (<configured dir>/<slug>), so containment here
// also means "inside this hub's tree" — a path into another hub's snapshots
// cannot resolve.
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
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	eng, ok := s.reqBackupEngine(w, r, set)
	if !ok {
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
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	eng, ok := s.reqBackupEngine(w, r, set)
	if !ok {
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
	// Store/pins files restore into the SESSION HUB's profile; the only
	// global write is the merged config.json (server.json is skipped inside
	// Restore). Another hub's tree is untouchable by construction.
	roots := backup.RestoreRoots{HubRoot: set.root, ConfigDir: s.hubs.configDir}
	if err := eng.Restore(snapDir, roots, expectedSchemaVersion); err != nil {
		s.logger.Error("backup: restore failed", "path", req.Path, "err", err)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("restore failed: %v", err))
		return
	}

	// Files are replaced; drop the SESSION HUB's in-memory store state before
	// anything can write stale data back over the restored files. Other hubs'
	// stores were not touched and keep their caches.
	if set.chat != nil {
		set.chat.Reset()
	}
	if set.tasks != nil {
		set.tasks.Reset()
	}
	if set.production != nil {
		set.production.Reset()
	}
	if set.whiteboards != nil {
		set.whiteboards.Reset()
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
