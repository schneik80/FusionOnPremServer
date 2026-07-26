package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/config"
)

// Data endpoints back the Settings console's Data tool: disk usage across
// the local per-project stores, per-project app-data deletion, and
// allow-listed stale-artifact cleanup. Same gating posture as the rest of
// /api/admin: any authenticated session; the destructive delete carries a
// typed confirmation in the UI.

// dataAppStores lists the four per-project app stores in display order, each
// with the envelope file its project dirs self-describe themselves through
// (sanitizeID slugs are not reversible — the envelope is the only way back
// to the project URN and name).
var dataAppStores = []struct{ name, envelope string }{
	{"chat", "meta.json"},
	{"tasks", "tasks.json"},
	{"production", "production.json"},
	{"whiteboards", "whiteboards.json"},
}

// staleCleanupAge is how old a store .bak/.tmp file must be before cleanup
// prunes it — recent ones may still be wanted for hand recovery.
const staleCleanupAge = 30 * 24 * time.Hour

// DiskProjectDTO is one project's (or, for the pins pseudo-store, one hub
// file's) slice of a store's disk usage. The identity fields come from the
// self-describing envelope; an unreadable envelope leaves only the dir slug.
type DiskProjectDTO struct {
	Dir         string `json:"dir"`
	ProjectID   string `json:"projectId,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	HubID       string `json:"hubId,omitempty"`
	Bytes       int64  `json:"bytes"`
}

// DiskStoreDTO is one store's total plus its per-project breakdown.
type DiskStoreDTO struct {
	Name     string           `json:"name"`
	Bytes    int64            `json:"bytes"`
	Projects []DiskProjectDTO `json:"projects"`
}

// DiskUsageDTO is GET /api/admin/disk: the four app stores plus the pins
// pseudo-store, with everything else under the config dir (server.log,
// sessions, TLS material, stale artifacts) lumped into otherBytes.
type DiskUsageDTO struct {
	Stores     []DiskStoreDTO `json:"stores"`
	OtherBytes int64          `json:"otherBytes"`
	TotalBytes int64          `json:"totalBytes"`
}

// ProjectDataDeleteDTO is DELETE /api/admin/projects/data: per requested app,
// whether its data is now gone (true also when there was nothing to delete —
// the operation is idempotent).
type ProjectDataDeleteDTO struct {
	Deleted map[string]bool `json:"deleted"`
}

// CleanupResultDTO is POST /api/admin/cleanup.
type CleanupResultDTO struct {
	Removed    []string `json:"removed"`
	BytesFreed int64    `json:"bytesFreed"`
}

// dirSizeBytes sums the file sizes under root (0 when absent). Sizes only —
// contents are never read.
func dirSizeBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // unreadable entries just don't count
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// probeEnvelope fills a project row's identity fields from its store
// envelope file — the one file whose contents the disk walk reads. Any
// failure leaves the row as the bare dir slug (still listed, still sized).
func probeEnvelope(path string, p *DiskProjectDTO) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var probe struct {
		ProjectID   string `json:"projectId"`
		HubID       string `json:"hubId"`
		ProjectName string `json:"projectName"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return
	}
	p.ProjectID, p.HubID, p.ProjectName = probe.ProjectID, probe.HubID, probe.ProjectName
}

// peekPinsHubID recovers the hub id a pins file's entries carry (the file
// name is a slug, not reversible). First non-empty wins; an unreadable or
// empty file yields "".
func peekPinsHubID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var probe struct {
		Pins []struct {
			HubID string `json:"hub_id"`
		} `json:"pins"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return ""
	}
	for _, p := range probe.Pins {
		if p.HubID != "" {
			return p.HubID
		}
	}
	return ""
}

// handleAdminDisk walks config.Dir() and reports per-store, per-project
// sizes. Only envelope JSONs are ever read for content; everything else is
// os.Stat/WalkDir sizes.
func (s *Server) handleAdminDisk(w http.ResponseWriter, r *http.Request) {
	dir, err := config.Dir()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := DiskUsageDTO{Stores: []DiskStoreDTO{}}
	var accounted int64
	for _, st := range dataAppStores {
		row := DiskStoreDTO{Name: st.name, Projects: []DiskProjectDTO{}}
		root := filepath.Join(dir, st.name)
		entries, rerr := os.ReadDir(root)
		if rerr == nil {
			for _, e := range entries {
				if e.IsDir() {
					p := DiskProjectDTO{Dir: e.Name(), Bytes: dirSizeBytes(filepath.Join(root, e.Name()))}
					probeEnvelope(filepath.Join(root, e.Name(), st.envelope), &p)
					row.Projects = append(row.Projects, p)
					row.Bytes += p.Bytes
				} else if info, ierr := e.Info(); ierr == nil {
					row.Bytes += info.Size() // strays directly under the store root
				}
			}
		}
		accounted += row.Bytes
		out.Stores = append(out.Stores, row)
	}

	// Pins: one file per hub directly under the config dir — a pseudo-store
	// with per-hub rows and no project fields.
	pinsRow := DiskStoreDTO{Name: "pins", Projects: []DiskProjectDTO{}}
	if matches, gerr := filepath.Glob(filepath.Join(dir, "pins-*.json")); gerr == nil {
		for _, m := range matches {
			info, serr := os.Stat(m)
			if serr != nil || info.IsDir() {
				continue
			}
			pinsRow.Projects = append(pinsRow.Projects, DiskProjectDTO{
				Dir:   filepath.Base(m),
				HubID: peekPinsHubID(m),
				Bytes: info.Size(),
			})
			pinsRow.Bytes += info.Size()
		}
	}
	accounted += pinsRow.Bytes
	out.Stores = append(out.Stores, pinsRow)

	out.TotalBytes = dirSizeBytes(dir)
	if out.OtherBytes = out.TotalBytes - accounted; out.OtherBytes < 0 {
		out.OtherBytes = 0 // files changed between walks; never report negative
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminProjectDataDelete deletes the named apps' data for one project.
// The whole apps list validates before anything deletes, so a typo can't
// half-delete; each store's DeleteProject is idempotent, so a missing dir
// still reports true.
func (s *Server) handleAdminProjectDataDelete(w http.ResponseWriter, r *http.Request) {
	projectID, ok := reqParam(w, r, "projectId")
	if !ok {
		return
	}
	appsParam, ok := reqParam(w, r, "apps")
	if !ok {
		return
	}

	deleters := map[string]func(string) error{}
	if s.chat != nil {
		deleters["chat"] = s.chat.DeleteProject
	}
	if s.tasks != nil {
		deleters["tasks"] = s.tasks.DeleteProject
	}
	if s.production != nil {
		deleters["production"] = s.production.DeleteProject
	}
	if s.whiteboards != nil {
		deleters["whiteboards"] = s.whiteboards.DeleteProject
	}

	known := map[string]bool{"chat": true, "tasks": true, "production": true, "whiteboards": true}
	apps := []string{}
	seen := map[string]bool{}
	for _, a := range strings.Split(appsParam, ",") {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			continue
		}
		if !known[a] {
			writeError(w, http.StatusBadRequest, "unknown app: "+a)
			return
		}
		if _, ok := deleters[a]; !ok {
			writeError(w, http.StatusServiceUnavailable, "the "+a+" store is unavailable")
			return
		}
		seen[a] = true
		apps = append(apps, a)
	}
	if len(apps) == 0 {
		writeError(w, http.StatusBadRequest, "apps must name at least one of chat, tasks, production, whiteboards")
		return
	}

	deleted := map[string]bool{}
	for _, a := range apps {
		if err := deleters[a](projectID); err != nil {
			s.fail(w, r, err)
			return
		}
		deleted[a] = true
	}
	s.logger.Info("admin: project app data deleted", "projectId", projectID, "apps", strings.Join(apps, ","))
	writeJSON(w, http.StatusOK, ProjectDataDeleteDTO{Deleted: deleted})
}

// handleAdminCleanup removes the allow-listed stale artifacts from the config
// dir root — tokens.json, models/ (dir), debug.log, server-restart.log;
// nothing else, ever — and prunes .bak/.vN.bak/.tmp files older than 30 days
// across the four store dirs (suffix allow-list only). Live data, sessions,
// TLS material and the server log are never candidates.
func (s *Server) handleAdminCleanup(w http.ResponseWriter, r *http.Request) {
	dir, err := config.Dir()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := CleanupResultDTO{Removed: []string{}}

	for _, name := range []string{"tokens.json", "debug.log", "server-restart.log"} {
		path := filepath.Join(dir, name)
		info, serr := os.Stat(path)
		if serr != nil || info.IsDir() {
			continue
		}
		if os.Remove(path) == nil {
			out.Removed = append(out.Removed, name)
			out.BytesFreed += info.Size()
		}
	}
	modelsDir := filepath.Join(dir, "models")
	if info, serr := os.Stat(modelsDir); serr == nil && info.IsDir() {
		size := dirSizeBytes(modelsDir)
		if os.RemoveAll(modelsDir) == nil {
			out.Removed = append(out.Removed, "models/")
			out.BytesFreed += size
		}
	}

	cutoff := time.Now().Add(-staleCleanupAge)
	for _, st := range dataAppStores {
		root := filepath.Join(dir, st.name)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil || d.IsDir() {
				return nil //nolint:nilerr // unreadable entries are skipped, not fatal
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".bak") && !strings.HasSuffix(name, ".tmp") {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil || info.ModTime().After(cutoff) {
				return nil
			}
			if os.Remove(path) == nil {
				if rel, rerr := filepath.Rel(dir, path); rerr == nil {
					out.Removed = append(out.Removed, filepath.ToSlash(rel))
				}
				out.BytesFreed += info.Size()
			}
			return nil
		})
	}

	if len(out.Removed) > 0 {
		s.logger.Info("admin: cleanup removed stale artifacts",
			"count", len(out.Removed), "bytes", out.BytesFreed)
	}
	writeJSON(w, http.StatusOK, out)
}
