// Package backup is the local-data backup engine: it streams every allow-listed
// data file (the four project stores, pins, redacted config) into a timestamped
// snapshot directory under a user-chosen backup root, writes a manifest with
// per-file SHA-256 hashes, and keeps a GFS retention ladder (7 daily / 4 weekly
// / 12 monthly). manual/ and pre-restore/ snapshots are never pruned.
//
// Sources are allow-list only: the engine backs up exactly what a Source hands
// it, and no Source ever reads sessions.enc, session.key, TLS keys, or logs —
// secrets stay out of backups by construction, not by filtering.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
)

// Kind is a snapshot tier. daily snapshots are made by the scheduler and feed
// the weekly/monthly promotions; manual and pre-restore snapshots are
// user/restore-triggered and exempt from pruning.
type Kind string

const (
	KindDaily      Kind = "daily"
	KindWeekly     Kind = "weekly"
	KindMonthly    Kind = "monthly"
	KindManual     Kind = "manual"
	KindPreRestore Kind = "pre-restore"
)

// tsLayout names snapshot directories: UTC, second resolution, and — crucially
// — lexicographic order equals chronological order.
const tsLayout = "20060102-150405"

// ManifestVersion gates manifest.json the way store file versions gate data
// files; a future restore refuses manifests it doesn't understand. v2 added
// the Hub/HubSlug identity fields — restore refuses v1 manifests outright,
// because a pre-hub-isolation snapshot cannot prove which hub it belongs to.
const ManifestVersion = 2

// Source is one provider of backup files. Snapshot must call visit once per
// file with a slash-separated path relative to the snapshot root, the file's
// full contents, and its schema version (0 when the file has none). Any error
// returned by visit must be propagated unchanged; any error returned by
// Snapshot aborts (and removes) the whole run.
type Source interface {
	Name() string
	Snapshot(visit func(rel string, data []byte, schemaVersion int) error) error
}

// ManifestFile is one backed-up file's manifest entry.
type ManifestFile struct {
	Path          string `json:"path"`  // slash-separated, relative to the snapshot dir
	Store         string `json:"store"` // the Source name that produced it
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	SchemaVersion int    `json:"schemaVersion"`
}

// Manifest is manifest.json at a snapshot dir's root: what was backed up,
// when, by which app version — everything verify/restore needs. Hub and
// HubSlug (v2) stamp WHOSE data the snapshot holds: Hub is the raw hub id
// and HubSlug is the profile directory slug. Restore refuses any manifest
// whose identity doesn't match the engine's.
type Manifest struct {
	ManifestVersion int            `json:"manifestVersion"`
	AppVersion      string         `json:"appVersion"`
	Hub             string         `json:"hub,omitempty"`
	HubSlug         string         `json:"hubSlug,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
	Kind            Kind           `json:"kind"`
	Files           []ManifestFile `json:"files"`
}

// Summary is one snapshot's List() row, cheap enough to render in a table
// without opening any data files (it reads only the manifest).
type Summary struct {
	Path       string    `json:"path"` // relative to Dir, e.g. "daily/20260726-033000"
	Kind       Kind      `json:"kind"`
	CreatedAt  time.Time `json:"createdAt"`
	AppVersion string    `json:"appVersion"`
	FileCount  int       `json:"fileCount"`
	TotalBytes int64     `json:"totalBytes"`
	// Warning marks a snapshot whose manifest is missing or unreadable; the
	// entry is still listed so the operator can see (and clean up) the dir.
	Warning string `json:"warning,omitempty"`
}

// Engine writes and manages snapshots under Dir. All fields are set at
// construction; the engine itself is stateless between calls. Hub/HubSlug
// name the ONE hub this engine snapshots and restores: every manifest it
// writes is stamped with them, and Restore refuses a snapshot stamped for a
// different hub — the engine is structurally single-hub.
type Engine struct {
	Dir        string
	Sources    []Source
	AppVersion string
	Hub        string // raw hub id
	HubSlug    string // hub profile directory slug
}

// Run creates one snapshot of the given kind. Daily runs additionally promote
// into the weekly/monthly tiers and prune the GFS ladder.
func (e *Engine) Run(kind Kind) (*Manifest, error) {
	return e.runAt(kind, time.Now())
}

// runAt is Run with an injectable clock (tests simulate multi-day schedules).
func (e *Engine) runAt(kind Kind, now time.Time) (*Manifest, error) {
	if e.Dir == "" {
		return nil, errors.New("backup: no backup directory configured")
	}
	snapDir, err := e.newSnapshotDir(kind, now)
	if err != nil {
		return nil, err
	}

	m := &Manifest{
		ManifestVersion: ManifestVersion,
		AppVersion:      e.AppVersion,
		Hub:             e.Hub,
		HubSlug:         e.HubSlug,
		CreatedAt:       now.UTC(),
		Kind:            kind,
		Files:           []ManifestFile{},
	}
	for _, src := range e.Sources {
		serr := src.Snapshot(func(rel string, data []byte, schemaVersion int) error {
			cleanRel := path.Clean(filepath.ToSlash(rel))
			if cleanRel == "." || cleanRel == "" || path.IsAbs(cleanRel) ||
				cleanRel == ".." || strings.HasPrefix(cleanRel, "../") {
				return fmt.Errorf("invalid backup path %q", rel)
			}
			dest := filepath.Join(snapDir, filepath.FromSlash(cleanRel))
			if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return err
			}
			if err := atomicfile.WriteFile(dest, data, 0600); err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			m.Files = append(m.Files, ManifestFile{
				Path:          cleanRel,
				Store:         src.Name(),
				SHA256:        hex.EncodeToString(sum[:]),
				Size:          int64(len(data)),
				SchemaVersion: schemaVersion,
			})
			return nil
		})
		if serr != nil {
			// A partial snapshot is worse than none: it would list as a valid
			// backup while silently missing a store. Remove the whole dir.
			_ = os.RemoveAll(snapDir)
			return nil, fmt.Errorf("backup: source %s: %w", src.Name(), serr)
		}
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		_ = os.RemoveAll(snapDir)
		return nil, fmt.Errorf("backup: encoding manifest: %w", err)
	}
	if err := atomicfile.WriteFile(filepath.Join(snapDir, "manifest.json"), data, 0600); err != nil {
		_ = os.RemoveAll(snapDir)
		return nil, fmt.Errorf("backup: writing manifest: %w", err)
	}

	if kind == KindDaily {
		if err := e.Promote(now); err != nil {
			return m, fmt.Errorf("backup: promoting: %w", err)
		}
		if err := e.Prune(); err != nil {
			return m, fmt.Errorf("backup: pruning: %w", err)
		}
	}
	return m, nil
}

// newSnapshotDir creates <Dir>/<kind>/<timestamp>/, suffixing -2, -3, … in the
// (rare) case two same-kind runs land within the same second.
func (e *Engine) newSnapshotDir(kind Kind, now time.Time) (string, error) {
	base := filepath.Join(e.Dir, string(kind), now.UTC().Format(tsLayout))
	dir := base
	for i := 2; ; i++ {
		// Any Stat error — not just ErrNotExist — ends the probe: a broken
		// destination (e.g. a path component that is a regular file returns
		// ENOTDIR) would otherwise suffix forever, and MkdirAll below reports
		// the real error either way.
		if _, err := os.Stat(dir); err != nil {
			break
		}
		dir = fmt.Sprintf("%s-%d", base, i)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("backup: creating snapshot dir: %w", err)
	}
	return dir, nil
}

// List returns a summary of every snapshot across all tiers, newest first.
// Snapshots with a missing or corrupt manifest are listed with a Warning
// rather than hidden or fatal.
func (e *Engine) List() ([]Summary, error) {
	out := []Summary{}
	if e.Dir == "" {
		return out, nil
	}
	for _, kind := range []Kind{KindDaily, KindWeekly, KindMonthly, KindManual, KindPreRestore} {
		tier := filepath.Join(e.Dir, string(kind))
		entries, err := os.ReadDir(tier)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("backup: listing %s: %w", tier, err)
		}
		for _, en := range entries {
			if !en.IsDir() {
				continue
			}
			sum := Summary{Path: string(kind) + "/" + en.Name(), Kind: kind}
			if ts, ok := parseTS(en.Name()); ok {
				sum.CreatedAt = ts
			}
			var m Manifest
			data, rerr := os.ReadFile(filepath.Join(tier, en.Name(), "manifest.json"))
			if rerr != nil || json.Unmarshal(data, &m) != nil {
				sum.Warning = "manifest missing or unreadable"
			} else {
				sum.CreatedAt = m.CreatedAt
				sum.AppVersion = m.AppVersion
				sum.FileCount = len(m.Files)
				for _, f := range m.Files {
					sum.TotalBytes += f.Size
				}
				// Pre-hub-isolation snapshots still LIST (the operator should
				// see them), but they carry a Warning: restore will refuse
				// them because they cannot prove which hub they belong to.
				if m.ManifestVersion < 2 {
					sum.Warning = "predates hub isolation — not restorable"
				}
			}
			out = append(out, sum)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// parseTS recovers the creation time from a snapshot dir name (tolerating the
// same-second -N suffix). Non-timestamp dirs report ok=false and are never
// pruned or promoted over.
func parseTS(name string) (time.Time, bool) {
	if len(name) > len(tsLayout) {
		name = name[:len(tsLayout)]
	}
	ts, err := time.Parse(tsLayout, name)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
