package pins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/config"
	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
	"github.com/schneik80/fusionlocalserver/internal/hubslug"
	"github.com/schneik80/fusionlocalserver/internal/migrate"
	"github.com/schneik80/fusionlocalserver/internal/schemameta"
)

// fileVersion is the pins-file schema version. v0 was the historical bare
// []Pin array (no envelope at all); v1 wraps it in the standard
// version+schema envelope shared by every other store.
const fileVersion = 1

// CurrentVersion exposes the pins-file schema version this build writes,
// for the backup verify/restore wiring.
func CurrentVersion() int { return fileVersion }

// ErrFutureVersion is returned when a hub's pins were written by a newer
// build; the caller must refuse rather than risk rewriting them.
var ErrFutureVersion = errors.New("pins: data written by a newer version")

// pinsFile is the on-disk envelope (v1+).
type pinsFile struct {
	Version int              `json:"version"`
	Schema  schemameta.Stamp `json:"schema"`
	Pins    []Pin            `json:"pins"`
}

// registry migrates old pins files forward. v0→v1 wraps the legacy bare
// array in the envelope; Apply can't run on a bare array (Peek needs an
// object), so Load performs the wrap before consulting the registry — the
// step exists so the version math stays uniform if v2 ever lands.
var registry = newRegistry()

func newRegistry() *migrate.Registry {
	r := migrate.NewRegistry("pins", fileVersion)
	r.Register(0, func(raw map[string]any) (map[string]any, error) {
		if _, ok := raw["pins"]; !ok {
			raw["pins"] = []any{}
		}
		return raw, nil
	})
	return r
}

// FolderRef is a single hop in a folder ancestry chain (mirrors api.FolderRef).
type FolderRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Pin represents a bookmark. Two families share the record:
//
//   - APS items — a project, a folder, or a document. ProjectID and
//     FolderPath are captured at pin time so navigation doesn't require an
//     API call.
//   - Local records — a whiteboard, task, job, batch or chat channel owned
//     by one of the per-project stores. These carry no APS ancestry at all;
//     Ref holds the fls: token that already addresses them (the same token
//     the inline cards use), and ProjectID/ProjectName locate the store.
//
// HubID is retained on the record even though pins are stored per-hub on
// disk — the stored hub scope is the authority, but the field is useful for
// the legacy-file migration and for the cross-hub navigation safety check.
type Pin struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	HubID        string `json:"hub_id"`
	ProjectID    string `json:"project_id,omitempty"`
	ProjectAltID string `json:"project_alt_id,omitempty"`
	// FolderPath is the ancestor chain from project root to the item:
	//   - project: empty
	//   - folder:  root-to-leaf path including the folder itself
	//   - document: root-to-leaf path of the containing folder
	// Local records have none.
	FolderPath []FolderRef `json:"folder_path,omitempty"`
	// Ref is the fls: token addressing a LOCAL record, and is empty for APS
	// items. It is also that pin's ID — a token is already unique per object
	// per project — so Add/Remove/IsPinned keep keying on ID alone.
	Ref string `json:"ref,omitempty"`
	// ProjectName is the local record's project display hint, captured at pin
	// time. APS pins resolve their project name from the projects list; a
	// local pin has no such lookup and shows the hint.
	ProjectName string    `json:"project_name,omitempty"`
	PinnedAt    time.Time `json:"pinned_at"`
}

// pinsFileForHub returns the absolute path to the pins file for the given
// hub: hubs/<slug>/pins-<slug>.json under the config dir (the hub's profile
// directory). The hub ID is sanitized so that URN-format identifiers (which
// contain ':' and '/') produce filenames that round-trip cleanly across
// macOS, Linux, and Windows.
func pinsFileForHub(hubID string) (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(hubslug.ProfileDir(dir, hubID), "pins-"+sanitizeHubID(hubID)+".json"), nil
}

// sanitizeHubID maps a hub ID to a filesystem-safe slug. It delegates to the
// shared hubslug package (extracted from here, byte-identical); the pins
// tests keep their own copy of the table as a ratchet.
func sanitizeHubID(hubID string) string {
	return hubslug.Slug(hubID)
}

// Load reads pinned items for the given hub. Absent → empty. A legacy bare
// []Pin array (v0) is wrapped into the v1 envelope in memory — with a
// .v0.bak snapshot — and rewritten on the next Save. Corrupt files rename
// to .bak and start clean (store posture) rather than block startup; a
// future-version file refuses.
func Load(hubID string) ([]Pin, error) {
	path, err := pinsFileForHub(hubID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Pin{}, nil
	}
	if err != nil {
		return nil, err
	}
	// v0 files are a bare JSON array — wrap into the envelope shape first so
	// the shared registry (which reads {"version": N}) can take over.
	if isLegacyArray(data) {
		var ps []Pin
		if err := json.Unmarshal(data, &ps); err != nil {
			_ = os.Rename(path, path+".bak")
			return []Pin{}, nil
		}
		snap := path + ".v0.bak"
		if _, statErr := os.Stat(snap); errors.Is(statErr, os.ErrNotExist) {
			_ = atomicfile.WriteFile(snap, data, 0600)
		}
		if ps == nil {
			ps = []Pin{}
		}
		return ps, nil
	}
	migrated, _, err := registry.Apply(path, data)
	if err != nil {
		if errors.Is(err, migrate.ErrFutureVersion) {
			return nil, fmt.Errorf("%w: %s", ErrFutureVersion, path)
		}
		// Corrupt object — same recovery as the stores.
		_ = os.Rename(path, path+".bak")
		return []Pin{}, nil
	}
	var pf pinsFile
	if err := json.Unmarshal(migrated, &pf); err != nil {
		_ = os.Rename(path, path+".bak")
		return []Pin{}, nil
	}
	if pf.Pins == nil {
		pf.Pins = []Pin{}
	}
	return pf.Pins, nil
}

// isLegacyArray reports whether the file is the pre-envelope bare array:
// its first non-whitespace byte is '['.
func isLegacyArray(data []byte) bool {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

// Save writes the pin list for the given hub atomically (0600) in the v1
// envelope, stamping schema provenance. Legacy v0 files upgrade here on
// their first post-update write.
func Save(hubID string, ps []Pin) error {
	path, err := pinsFileForHub(hubID)
	if err != nil {
		return err
	}
	if ps == nil {
		ps = []Pin{}
	}
	pf := pinsFile{Version: fileVersion, Pins: ps}
	// Preserve the birth stamp across rewrites when the file already has one.
	if prev, err := os.ReadFile(path); err == nil && !isLegacyArray(prev) {
		var old pinsFile
		if json.Unmarshal(prev, &old) == nil && !old.Schema.CreatedAt.IsZero() {
			pf.Schema = old.Schema
		}
	}
	if pf.Schema.CreatedAt.IsZero() {
		if info, err := os.Stat(path); err == nil {
			pf.Schema = schemameta.Backfill(info.ModTime())
		} else {
			pf.Schema = schemameta.New()
		}
	}
	pf.Schema.Touch()
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	// The hub's profile directory may not exist yet (first pin before any
	// other store touched this hub).
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return atomicfile.WriteFile(path, data, 0600)
}

// IsPinned reports whether the given item ID is in the pin list.
func IsPinned(ps []Pin, id string) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}

// Add prepends p to ps unless its ID is already present.
func Add(ps []Pin, p Pin) []Pin {
	if IsPinned(ps, p.ID) {
		return ps
	}
	p.PinnedAt = time.Now()
	return append([]Pin{p}, ps...)
}

// Remove returns a new slice with the item matching id omitted.
func Remove(ps []Pin, id string) []Pin {
	out := ps[:0:0]
	for _, p := range ps {
		if p.ID != id {
			out = append(out, p)
		}
	}
	return out
}

// RefPrefix is the scheme every local record's Ref token carries. The web
// side owns the per-scheme grammar (components/*card/*ref.ts); the store only
// needs to know a local pin is addressed by a token, not by an APS URN.
const RefPrefix = "fls:"

// IsPinnable reports whether an item of the given kind may be pinned.
// Hubs are excluded; projects, folders, document kinds (including drawings,
// configured designs, and Fusion Electronics types) and the local-record
// kinds are allowed.
func IsPinnable(kind string) bool {
	switch kind {
	case "project", "folder",
		"design", "drawing", "configured",
		"schematic", "pcb", "ecad":
		return true
	}
	return IsLocalKind(kind)
}

// IsLocalKind reports whether kind names a record owned by one of the local
// per-project stores rather than an APS item. These are addressed by an fls:
// token instead of a URN + folder ancestry.
func IsLocalKind(kind string) bool {
	switch kind {
	case "whiteboard", "task", "job", "batch", "channel":
		return true
	}
	return false
}

// Validation errors. They surface to the SPA as 400 invalid_request, whose
// code is deliberately absent from the client catalog so these specific
// messages show verbatim rather than being flattened (see server/respond.go).
var (
	ErrNoID          = errors.New("pin id is required")
	ErrNotPinnable   = errors.New("items of this kind cannot be pinned")
	ErrRefRequired   = errors.New("a local pin requires an fls: ref and a project id")
	ErrRefUnexpected = errors.New("only local records may carry a ref")
	ErrRefMismatch   = errors.New("a local pin's id must be its ref")
)

// Validate checks a pin submitted by a client. The two families are checked
// against each other rather than independently: an APS pin must NOT carry a
// ref (it would go unread and imply a navigation the dialog can't perform),
// and a local pin must carry one — plus the project id that locates its
// store — because there is no other way to find the record again.
func Validate(p Pin) error {
	if p.ID == "" {
		return ErrNoID
	}
	if !IsPinnable(p.Kind) {
		return fmt.Errorf("%w: %s", ErrNotPinnable, p.Kind)
	}
	if !IsLocalKind(p.Kind) {
		if p.Ref != "" {
			return ErrRefUnexpected
		}
		return nil
	}
	if p.ProjectID == "" || !strings.HasPrefix(p.Ref, RefPrefix) {
		return ErrRefRequired
	}
	// The token IS the identity. Letting the two diverge would let one object
	// be pinned twice under different ids, and would leave Remove(id) unable
	// to say which record it dropped.
	if p.ID != p.Ref {
		return ErrRefMismatch
	}
	return nil
}
