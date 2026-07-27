package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
	"github.com/schneik80/fusionlocalserver/internal/hubslug"
	"github.com/schneik80/fusionlocalserver/internal/schemameta"
	"github.com/schneik80/fusionlocalserver/notifications"
	"github.com/schneik80/fusionlocalserver/production"
	"github.com/schneik80/fusionlocalserver/tasks"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// storeSet is one hub's complete local-data world: the four stores rooted
// under the hub's profile directory (hubs/<slug>/) plus that hub's own SSE
// fan-out. Handlers only ever see the storeSet the session's hub resolved to,
// which is what makes cross-hub reads structurally impossible — there is no
// path from one set to another hub's files.
type storeSet struct {
	hubID   string
	hubName string
	slug    string
	root    string // <configDir>/hubs/<slug>

	chat    *chat.Store
	chatHub *chat.Hub // per-hub fan-out: events never cross hubs
	// whiteboardHub is the board-level fan-out (who has a board open, and when
	// its document changed). Its own hub rather than a chat event type: a busy
	// board would otherwise share chat's ring and evict chat's events, handing
	// every project subscriber a spurious resync. Per-hub for the same reason
	// chatHub is — events must never cross hubs.
	whiteboardHub *whiteboardHub
	tasks         *tasks.Store
	production    *production.Store
	whiteboards   *whiteboards.Store
	// notifications is the per-user inbox behind the app-chrome bell. Keyed
	// by OIDC sub (not project), but rooted in the hub profile like every
	// other store, so a user's inbox is per-hub — a mention in hub A is
	// invisible while the session is locked to hub B (the hub-isolation
	// invariant holds for notifications too).
	notifications *notifications.Store
}

// hubJSONFile identifies a profile directory: which hub the slug belongs to.
// It is the guard against lossy-slug collisions — two distinct hub ids that
// sanitize to the same slug must hard-refuse, never silently merge.
type hubJSONFile struct {
	HubID     string    `json:"hubId"`
	HubName   string    `json:"hubName"`
	CreatedAt time.Time `json:"createdAt"`
}

// hubEntry is one lazily-built storeSet. The once/err pair keeps store
// construction outside the map mutex while still building at most once.
type hubEntry struct {
	once sync.Once
	set  *storeSet
	err  error
}

// hubStores owns every hub's storeSet, keyed by slug and built lazily on
// first access. mu guards the map ONLY — it is never held across store or
// engine calls (construction runs under the entry's once, after mu is
// released).
type hubStores struct {
	configDir string
	authz     *chat.Authorizer

	mu   sync.Mutex
	sets map[string]*hubEntry // by slug
}

func newHubStores(configDir string, authz *chat.Authorizer) *hubStores {
	return &hubStores{configDir: configDir, authz: authz, sets: make(map[string]*hubEntry)}
}

// errHubCollision marks a slug-collision refusal (distinct hub ids mapping to
// one profile directory).
var errHubCollision = errors.New("hub slug collision")

// get returns (building if needed) the storeSet for hubID. hubName is stored
// into hub.json on first creation and refreshed when it changes.
func (h *hubStores) get(hubID, hubName string) (*storeSet, error) {
	if h == nil {
		return nil, errors.New("local storage is unavailable on this server")
	}
	if hubID == "" {
		return nil, errors.New("no hub id")
	}
	slug := hubslug.Slug(hubID)

	h.mu.Lock()
	e, ok := h.sets[slug]
	if !ok {
		e = &hubEntry{}
		h.sets[slug] = e
	}
	h.mu.Unlock()

	e.once.Do(func() {
		e.set, e.err = h.build(hubID, hubName, slug)
	})
	if e.err != nil {
		// Drop the failed entry so a later request may retry (transient IO);
		// a real collision will simply refuse again.
		h.mu.Lock()
		if h.sets[slug] == e {
			delete(h.sets, slug)
		}
		h.mu.Unlock()
		return nil, e.err
	}
	if e.set.hubID != hubID {
		// A different hub id sanitized to the same slug as an already-built
		// set — the in-memory face of the hub.json collision guard.
		return nil, fmt.Errorf("%w: %q and %q both map to profile %q", errHubCollision, e.set.hubID, hubID, slug)
	}
	return e.set, nil
}

// build constructs the profile directory, validates/writes hub.json, and
// opens the four stores. Runs outside hubStores.mu (under the entry's once).
func (h *hubStores) build(hubID, hubName, slug string) (*storeSet, error) {
	root := filepath.Join(h.configDir, "hubs", slug)
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("creating hub profile: %w", err)
	}
	if err := ensureHubJSON(root, hubID, hubName); err != nil {
		return nil, err
	}

	cs, err := chat.NewStore(filepath.Join(root, "chat"))
	if err != nil {
		return nil, fmt.Errorf("chat store: %w", err)
	}
	ts, err := tasks.NewStore(filepath.Join(root, "tasks"))
	if err != nil {
		cs.Close()
		return nil, fmt.Errorf("tasks store: %w", err)
	}
	ps, err := production.NewStore(filepath.Join(root, "production"))
	if err != nil {
		cs.Close()
		return nil, fmt.Errorf("production store: %w", err)
	}
	ws, err := whiteboards.NewStore(filepath.Join(root, "whiteboards"))
	if err != nil {
		cs.Close()
		return nil, fmt.Errorf("whiteboards store: %w", err)
	}
	ns, err := notifications.NewStore(filepath.Join(root, "notifications"))
	if err != nil {
		cs.Close()
		return nil, fmt.Errorf("notifications store: %w", err)
	}
	return &storeSet{
		hubID:         hubID,
		hubName:       hubName,
		slug:          slug,
		root:          root,
		chat:          cs,
		chatHub:       chat.NewHub(h.authz, cs.EventEpoch),
		whiteboardHub: newWhiteboardHub(),
		tasks:         ts,
		production:    ps,
		whiteboards:   ws,
		notifications: ns,
	}, nil
}

// ensureHubJSON validates an existing hub.json against hubID (hard refusal on
// mismatch — the slug-collision guard) or writes a fresh one. A corrupt file
// is renamed to .bak and rewritten (store posture: it cannot prove a
// collision, and refusing would brick the hub).
func ensureHubJSON(root, hubID, hubName string) error {
	path := filepath.Join(root, "hub.json")
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var f hubJSONFile
		if jerr := json.Unmarshal(data, &f); jerr != nil {
			_ = os.Rename(path, path+".bak")
			break // rewrite below
		}
		if f.HubID != "" && f.HubID != hubID {
			return fmt.Errorf("%w: profile %s belongs to hub %q, refusing to open it for hub %q",
				errHubCollision, root, f.HubID, hubID)
		}
		if f.HubID == hubID && (hubName == "" || f.HubName == hubName) {
			return nil // up to date
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("reading hub.json: %w", err)
	}
	f := hubJSONFile{HubID: hubID, HubName: hubName, CreatedAt: time.Now().UTC()}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, out, 0600)
}

// readHubJSON loads hubs/<slug>/hub.json (for getBySlug and the scheduler).
func (h *hubStores) readHubJSON(slug string) (hubJSONFile, error) {
	var f hubJSONFile
	data, err := os.ReadFile(filepath.Join(h.configDir, "hubs", slug, "hub.json"))
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, err
	}
	return f, nil
}

// getBySlug resolves a profile directory already on disk into its storeSet
// via hub.json. Profiles without a readable hub id refuse.
func (h *hubStores) getBySlug(slug string) (*storeSet, error) {
	if h == nil {
		return nil, errors.New("local storage is unavailable on this server")
	}
	f, err := h.readHubJSON(slug)
	if err != nil {
		return nil, fmt.Errorf("profile %q has no readable hub.json: %w", slug, err)
	}
	if f.HubID == "" {
		return nil, fmt.Errorf("profile %q has no hub id", slug)
	}
	return h.get(f.HubID, f.HubName)
}

// snapshotAll returns every successfully-built storeSet. Only the map read is
// under mu.
func (h *hubStores) snapshotAll() []*storeSet {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	entries := make([]*hubEntry, 0, len(h.sets))
	for _, e := range h.sets {
		entries = append(entries, e)
	}
	h.mu.Unlock()
	out := make([]*storeSet, 0, len(entries))
	for _, e := range entries {
		// Only include fully-built sets; an in-flight once has not published
		// set/err yet and is skipped rather than waited on.
		if e.set != nil && e.err == nil {
			out = append(out, e.set)
		}
	}
	return out
}

// diskHubSlugs lists every hub profile directory under hubs/, built or not —
// the scheduler's view (a hub can have backups configured without having been
// touched this process run).
func (h *hubStores) diskHubSlugs() []string {
	if h == nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(h.configDir, "hubs"))
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() && e.Name() != "" && e.Name()[0] != '.' {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// closeAll closes every built set's SSE hub and chat store. Called at
// shutdown; fans out per hub so no single hub can stall the others' close.
func (h *hubStores) closeAll() {
	for _, set := range h.snapshotAll() {
		if set.chatHub != nil {
			set.chatHub.CloseAll()
		}
		if set.whiteboardHub != nil {
			set.whiteboardHub.CloseAll()
		}
		if set.chat != nil {
			set.chat.Close()
		}
	}
}

// ---- per-hub backup configuration (hubs/<slug>/backup.json) ----

// hubBackupConfigVersion versions backup.json like every other store file; a
// file written by a newer build refuses to load.
const hubBackupConfigVersion = 1

const hubBackupConfigFile = "backup.json"

// errBackupCfgFuture is returned when a hub's backup.json was written by a
// newer build.
var errBackupCfgFuture = errors.New("backup configuration was written by a newer version")

// hubBackupConfig is one hub's backup configuration: each client hub backs up
// to its own destination on its own schedule. BackupDir empty means backups
// are unconfigured for this hub.
type hubBackupConfig struct {
	Version       int              `json:"version"`
	Schema        schemameta.Stamp `json:"schema"`
	BackupDir     string           `json:"backupDir,omitempty"`
	BackupTime    string           `json:"backupTime,omitempty"`
	BackupEnabled bool             `json:"backupEnabled,omitempty"`
}

// loadHubBackupConfig reads hubs/<slug>/backup.json. Absent → zero config;
// corrupt → renamed to .bak and zero config (store posture); future version →
// error (never rewrite what a newer build owns).
func loadHubBackupConfig(root string) (hubBackupConfig, error) {
	var cfg hubBackupConfig
	path := filepath.Join(root, hubBackupConfigFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		_ = os.Rename(path, path+".bak")
		return hubBackupConfig{}, nil
	}
	if cfg.Version > hubBackupConfigVersion {
		return hubBackupConfig{}, fmt.Errorf("%w: %s is v%d, this build writes v%d",
			errBackupCfgFuture, path, cfg.Version, hubBackupConfigVersion)
	}
	return cfg, nil
}

// saveHubBackupConfig writes the hub's backup.json atomically, preserving the
// schema birth stamp across rewrites.
func saveHubBackupConfig(root string, cfg hubBackupConfig) error {
	path := filepath.Join(root, hubBackupConfigFile)
	cfg.Version = hubBackupConfigVersion
	if prev, err := loadHubBackupConfig(root); err == nil && !prev.Schema.CreatedAt.IsZero() {
		cfg.Schema = prev.Schema
	}
	if cfg.Schema.CreatedAt.IsZero() {
		cfg.Schema = schemameta.New()
	}
	cfg.Schema.Touch()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	return atomicfile.WriteFile(path, data, 0600)
}
