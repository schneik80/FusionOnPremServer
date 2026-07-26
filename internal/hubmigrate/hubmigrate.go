// Package hubmigrate performs the one-time relocation of the pre-hub-isolation
// local-data layout into per-hub profile directories (hubs/<slug>/). Before
// hub isolation, every store rooted directly under the config dir keyed only
// by project slug:
//
//	<config>/{chat,tasks,production,whiteboards}/<projslug>/...
//	<config>/pins-<hubslug>.json
//	<config>/server.json           (carried the global backup config)
//
// Run moves each project directory under the profile of the hub its envelope
// file names, quarantines anything whose hub cannot be determined under
// hubs/_unassigned/ (data is never dropped), fans the legacy global backup
// configuration out into per-hub backup.json files, and finally writes the
// hubs/.migrated marker.
//
// Crash safety: every relocation is a single os.Rename (atomic within a
// filesystem — a crash leaves each directory wholly at the old or the new
// location, never torn), reruns skip work whose destination already exists,
// and the chat sibling resolution scans the ALREADY-migrated hubs tree in
// addition to this run's moves, so a crash between the store and chat phases
// still routes chat data to the right hub on the rerun. The .migrated marker
// is only a fast-exit optimization, written after a fully clean pass; any
// per-item failure leaves it absent so the next start retries.
package hubmigrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
	"github.com/schneik80/fusionlocalserver/internal/hubslug"
	"github.com/schneik80/fusionlocalserver/internal/schemameta"
)

// MarkerName is the fast-exit marker written under hubs/ after a clean pass.
const MarkerName = ".migrated"

// envStores are the three stores whose per-project envelope file self-
// describes the owning hub. Chat's meta.json has no hubId — chat dirs follow
// their siblings from these stores instead.
var envStores = []struct{ name, envelope string }{
	{"tasks", "tasks.json"},
	{"production", "production.json"},
	{"whiteboards", "whiteboards.json"},
}

// hubJSON mirrors the profile identity file the server writes
// (server.hubJSONFile) — same shape, kept in sync by the migration tests'
// assertions on the fields the server reads.
type hubJSON struct {
	HubID     string    `json:"hubId"`
	HubName   string    `json:"hubName"`
	CreatedAt time.Time `json:"createdAt"`
}

// backupJSON mirrors the server's per-hub backup configuration file
// (server.hubBackupConfig, version 1).
type backupJSON struct {
	Version       int              `json:"version"`
	Schema        schemameta.Stamp `json:"schema"`
	BackupDir     string           `json:"backupDir,omitempty"`
	BackupTime    string           `json:"backupTime,omitempty"`
	BackupEnabled bool             `json:"backupEnabled,omitempty"`
}

// errCollision marks a hub.json that already belongs to a DIFFERENT hub id
// (two ids sanitizing to one slug). The colliding project quarantines rather
// than merging into the wrong hub's profile.
var errCollision = errors.New("hubmigrate: hub slug collision")

type migrator struct {
	configDir string
	hubsDir   string
	log       *slog.Logger

	moved       int // directories/files relocated this run
	quarantined int // of those, into hubs/_unassigned/
	skipped     int // source AND destination existed; source left in place
	errs        []error
}

// Run relocates the pre-hub layout under configDir into hubs/<slug>/
// profiles. Idempotent and crash-safe (see the package comment); fast-exits
// when hubs/.migrated exists. A non-nil error means the pass was incomplete
// and will be retried on the next start — already-moved data stays moved.
func Run(configDir string, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	m := &migrator{
		configDir: configDir,
		hubsDir:   filepath.Join(configDir, "hubs"),
		log:       log,
	}
	marker := filepath.Join(m.hubsDir, MarkerName)
	if _, err := os.Stat(marker); err == nil {
		return nil
	}

	// Order matters: the three self-describing stores first (their placement
	// decides where chat siblings go), then chat, then pins, then the backup
	// config fan-out (which needs every profile directory to exist).
	for _, st := range envStores {
		m.relocateStore(st.name, st.envelope)
	}
	m.relocateChat()
	m.relocatePins()
	m.migrateBackupConfig()
	m.removeOldRoots()

	if err := errors.Join(m.errs...); err != nil {
		m.log.Warn("hub migration: pass incomplete — remaining items retry next start",
			"moved", m.moved, "quarantined", m.quarantined, "errors", len(m.errs))
		return err
	}
	if err := os.MkdirAll(m.hubsDir, 0700); err != nil {
		return fmt.Errorf("hubmigrate: creating hubs dir: %w", err)
	}
	if err := atomicfile.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600); err != nil {
		return fmt.Errorf("hubmigrate: writing marker: %w", err)
	}
	m.log.Info("hub layout migration complete",
		"moved", m.moved, "quarantined", m.quarantined, "skipped", m.skipped)
	return nil
}

// relocateStore moves every <configDir>/<store>/<projslug>/ into the profile
// of the hub its envelope names; unreadable envelopes or empty hub ids
// quarantine under _unassigned (never dropped).
func (m *migrator) relocateStore(store, envelope string) {
	root := filepath.Join(m.configDir, store)
	entries, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.errs = append(m.errs, fmt.Errorf("hubmigrate: reading %s: %w", root, err))
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue // stray files stay put; the nonempty root then survives removeOldRoots, visibly
		}
		src := filepath.Join(root, e.Name())
		hubID, hubName := peekEnvelope(filepath.Join(src, envelope))
		slug := hubslug.Unassigned
		if hubID != "" {
			s := hubslug.Slug(hubID)
			switch err := m.seedHubJSON(s, hubID, hubName); {
			case err == nil:
				slug = s
			case errors.Is(err, errCollision):
				// Deliberate quarantine: the profile belongs to another hub id.
				m.log.Warn("hub migration: slug collision — quarantining project data",
					"store", store, "project", e.Name(), "hubId", hubID, "err", err)
			default:
				// Transient failure (IO): leave the directory at the old root so
				// the next start retries, rather than stranding it in quarantine.
				m.errs = append(m.errs, err)
				continue
			}
		}
		m.moveInto(src, slug, store, e.Name())
	}
}

// relocateChat routes each chat project dir to the hub any migrated sibling
// (tasks/production/whiteboards dir of the same project slug) lives under.
// The sibling map is built by scanning the hubs/ tree AFTER the three store
// relocations, so it covers both this run's moves and dirs moved by an
// earlier crashed run — the crash-rerun correctness requirement.
func (m *migrator) relocateChat() {
	root := filepath.Join(m.configDir, "chat")
	entries, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.errs = append(m.errs, fmt.Errorf("hubmigrate: reading %s: %w", root, err))
		}
		return
	}
	siblings := m.siblingMap()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug, ok := siblings[e.Name()]
		if !ok {
			slug = hubslug.Unassigned
		}
		m.moveInto(filepath.Join(root, e.Name()), slug, "chat", e.Name())
	}
}

// siblingMap maps project slug → hub slug from the migrated tree
// (hubs/*/{tasks,production,whiteboards}/<projslug>). A real hub always
// beats _unassigned; between two real hubs the first (ReadDir order, i.e.
// lexicographic) wins.
func (m *migrator) siblingMap() map[string]string {
	sib := map[string]string{}
	hubs, err := os.ReadDir(m.hubsDir)
	if err != nil {
		return sib
	}
	for _, h := range hubs {
		if !h.IsDir() || strings.HasPrefix(h.Name(), ".") {
			continue
		}
		for _, st := range envStores {
			projs, err := os.ReadDir(filepath.Join(m.hubsDir, h.Name(), st.name))
			if err != nil {
				continue
			}
			for _, p := range projs {
				if !p.IsDir() {
					continue
				}
				cur, ok := sib[p.Name()]
				if !ok || (cur == hubslug.Unassigned && h.Name() != hubslug.Unassigned) {
					sib[p.Name()] = h.Name()
				}
			}
		}
	}
	return sib
}

// relocatePins moves each <configDir>/pins-<slug>.json into hubs/<slug>/
// (the slug is authoritative — it IS the filename), seeding hub.json from
// the pin entries' hub_id when the profile lacks one.
func (m *migrator) relocatePins() {
	matches, err := filepath.Glob(filepath.Join(m.configDir, "pins-*.json"))
	if err != nil {
		return // only fails on a bad pattern, which this is not
	}
	for _, src := range matches {
		base := filepath.Base(src)
		slug := strings.TrimSuffix(strings.TrimPrefix(base, "pins-"), ".json")
		if slug == "" || strings.HasPrefix(slug, ".") {
			continue
		}
		profile := filepath.Join(m.hubsDir, slug)
		dest := filepath.Join(profile, base)
		if _, err := os.Lstat(dest); err == nil {
			m.skipped++
			m.log.Warn("hub migration: pins destination already exists; leaving source in place",
				"src", src, "dest", dest)
			continue
		}
		// A pins-only profile has no envelope to identify the hub; the pin
		// entries carry hub_id. Only seed when it round-trips to this slug.
		if _, err := os.Stat(filepath.Join(profile, "hub.json")); errors.Is(err, os.ErrNotExist) {
			if hubID := peekPinsHubID(src); hubID != "" && hubslug.Slug(hubID) == slug {
				if serr := m.seedHubJSON(slug, hubID, ""); serr != nil {
					m.log.Warn("hub migration: could not seed hub.json from pins", "slug", slug, "err", serr)
				}
			}
		}
		if err := os.MkdirAll(profile, 0700); err != nil {
			m.errs = append(m.errs, fmt.Errorf("hubmigrate: creating profile %s: %w", profile, err))
			continue
		}
		if err := moveFile(src, dest); err != nil {
			m.errs = append(m.errs, fmt.Errorf("hubmigrate: moving %s: %w", src, err))
			continue
		}
		m.moved++
	}
}

// migrateBackupConfig fans the legacy global backup fields out of server.json
// into every real hub profile's backup.json (each hub keeps the old behavior
// until its admin retargets it), then rewrites server.json without them.
// _unassigned gets none — quarantine is transient and unconfigured by design.
// server.json is only rewritten after every fan-out write succeeded, so a
// crash mid-fan-out retries with the fields still present.
func (m *migrator) migrateBackupConfig() {
	path := filepath.Join(m.configDir, "server.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		m.errs = append(m.errs, fmt.Errorf("hubmigrate: reading server.json: %w", err))
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		m.log.Warn("hub migration: server.json is not valid JSON; leaving it untouched")
		return
	}
	if raw["backupDir"] == nil && raw["backupTime"] == nil && raw["backupEnabled"] == nil {
		return // already migrated (or never configured)
	}
	var legacy struct {
		Port          int    `json:"port"`
		BackupDir     string `json:"backupDir"`
		BackupTime    string `json:"backupTime"`
		BackupEnabled bool   `json:"backupEnabled"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		m.log.Warn("hub migration: server.json backup fields unreadable; leaving it untouched", "err", err)
		return
	}

	if legacy.BackupDir != "" || legacy.BackupTime != "" || legacy.BackupEnabled {
		hubs, rerr := os.ReadDir(m.hubsDir)
		if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			m.errs = append(m.errs, fmt.Errorf("hubmigrate: reading hubs dir: %w", rerr))
			return
		}
		for _, h := range hubs {
			if !h.IsDir() || strings.HasPrefix(h.Name(), ".") || h.Name() == hubslug.Unassigned {
				continue
			}
			bpath := filepath.Join(m.hubsDir, h.Name(), "backup.json")
			if _, serr := os.Stat(bpath); serr == nil {
				continue // this hub already has its own config; never overwrite
			}
			cfg := backupJSON{
				Version:       1,
				Schema:        schemameta.New(),
				BackupDir:     legacy.BackupDir,
				BackupTime:    legacy.BackupTime,
				BackupEnabled: legacy.BackupEnabled,
			}
			out, merr := json.MarshalIndent(cfg, "", "  ")
			if merr != nil {
				m.errs = append(m.errs, merr)
				return
			}
			if werr := atomicfile.WriteFile(bpath, out, 0600); werr != nil {
				m.errs = append(m.errs, fmt.Errorf("hubmigrate: writing %s: %w", bpath, werr))
				return // keep server.json's fields so the fan-out retries
			}
		}
	}

	// All fan-out writes done — retire the global fields, preserving the port.
	slim := struct {
		Port int `json:"port,omitempty"`
	}{Port: legacy.Port}
	out, merr := json.MarshalIndent(slim, "", "  ")
	if merr != nil {
		m.errs = append(m.errs, merr)
		return
	}
	if err := atomicfile.WriteFile(path, out, 0600); err != nil {
		m.errs = append(m.errs, fmt.Errorf("hubmigrate: rewriting server.json: %w", err))
	}
}

// removeOldRoots removes the now-empty legacy store roots. Plain os.Remove is
// the safety: a root still holding anything (a stray file, a dir whose move
// failed) refuses to go, keeping the leftovers visible for the retry.
func (m *migrator) removeOldRoots() {
	for _, name := range []string{"chat", "tasks", "production", "whiteboards"} {
		_ = os.Remove(filepath.Join(m.configDir, name))
	}
}

// moveInto relocates src to hubs/<hubSlug>/<store>/<projSlug>. An existing
// destination means an earlier run (or hand copy) already placed data there:
// the source is left in place and warned about, never merged or overwritten.
func (m *migrator) moveInto(src, hubSlug, store, projSlug string) {
	dest := filepath.Join(m.hubsDir, hubSlug, store, projSlug)
	if _, err := os.Lstat(dest); err == nil {
		m.skipped++
		m.log.Warn("hub migration: destination already exists; leaving source in place",
			"src", src, "dest", dest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		m.errs = append(m.errs, fmt.Errorf("hubmigrate: creating %s: %w", filepath.Dir(dest), err))
		return
	}
	if err := MoveDir(src, dest); err != nil {
		m.errs = append(m.errs, fmt.Errorf("hubmigrate: moving %s: %w", src, err))
		return
	}
	m.moved++
	if hubSlug == hubslug.Unassigned {
		m.quarantined++
		m.log.Warn("hub migration: owning hub unknown — quarantined",
			"store", store, "project", projSlug, "dest", dest)
	}
}

// seedHubJSON creates or validates hubs/<slug>/hub.json for hubID — the same
// {hubId, hubName, createdAt} shape and collision posture as the server's
// ensureHubJSON. An existing file naming a DIFFERENT hub id returns
// errCollision (never merge); a corrupt file renames to .bak and is rewritten.
func (m *migrator) seedHubJSON(slug, hubID, hubName string) error {
	root := filepath.Join(m.hubsDir, slug)
	path := filepath.Join(root, "hub.json")
	createdAt := time.Now().UTC()
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var f hubJSON
		if json.Unmarshal(data, &f) != nil {
			_ = os.Rename(path, path+".bak") // rewrite below (store posture)
			break
		}
		if f.HubID != "" && f.HubID != hubID {
			return fmt.Errorf("%w: profile %q belongs to hub %q, not %q", errCollision, slug, f.HubID, hubID)
		}
		if f.HubID == hubID && (hubName == "" || f.HubName == hubName) {
			return nil // up to date
		}
		if hubName == "" {
			hubName = f.HubName
		}
		if !f.CreatedAt.IsZero() {
			createdAt = f.CreatedAt // refresh, don't re-birth
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("hubmigrate: reading %s: %w", path, err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return fmt.Errorf("hubmigrate: creating profile %s: %w", root, err)
	}
	out, err := json.MarshalIndent(hubJSON{HubID: hubID, HubName: hubName, CreatedAt: createdAt}, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(path, out, 0600); err != nil {
		return fmt.Errorf("hubmigrate: writing %s: %w", path, err)
	}
	return nil
}

// peekEnvelope reads the hub identity a store envelope self-describes.
// Any failure (missing, unreadable, corrupt) yields "", "" — the caller
// quarantines rather than guessing.
func peekEnvelope(path string) (hubID, hubName string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var probe struct {
		HubID   string `json:"hubId"`
		HubName string `json:"hubName"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return "", ""
	}
	return probe.HubID, probe.HubName
}

// peekPinsHubID recovers the hub id a pins file's entries carry (the
// filename is a slug, not reversible). First non-empty wins; unreadable or
// empty yields "". Mirrors server.peekPinsHubID.
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

// MoveDir relocates a directory: one atomic os.Rename on the common path,
// with a copy-then-remove fallback when src and dst sit on different
// filesystems (EXDEV). The fallback copies file-by-file through atomicfile
// and removes the source only after the whole tree copied — a crash can
// leave a partial destination, but the complete source is still there and
// data is never lost. Exported for the server's quarantine-adoption path,
// which shares the same posture.
func MoveDir(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if cerr := copyTree(src, dst); cerr != nil {
		return cerr
	}
	return os.RemoveAll(src)
}

// copyTree copies a directory tree, dirs 0700 and files through atomicfile
// with their source permissions. Non-regular files (symlinks, devices) are
// refused — the stores never create them, so one appearing is a red flag.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("hubmigrate: refusing to copy non-regular file %s", path)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		perm := fs.FileMode(0600)
		if info, ierr := d.Info(); ierr == nil {
			perm = info.Mode().Perm()
		}
		return atomicfile.WriteFile(target, data, perm)
	})
}

// moveFile relocates one file: rename, or read+atomic-write+remove-source-
// last across filesystems.
func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	data, rerr := os.ReadFile(src)
	if rerr != nil {
		return rerr
	}
	perm := fs.FileMode(0600)
	if info, serr := os.Stat(src); serr == nil {
		perm = info.Mode().Perm()
	}
	if werr := atomicfile.WriteFile(dst, data, perm); werr != nil {
		return werr
	}
	return os.Remove(src)
}
