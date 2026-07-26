package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// FileResult is one file's verification outcome. A file is fully OK when it
// is present, its bytes hash to the manifest's SHA-256, its content parses
// as the JSON shape its name implies, its recorded schema version is not
// newer than what this build writes, and Detail is empty (Detail marks
// structural findings like a file the manifest doesn't know about).
type FileResult struct {
	Path      string `json:"path"`
	HashOK    bool   `json:"hashOK"`
	ParseOK   bool   `json:"parseOK"`
	VersionOK bool   `json:"versionOK"`
	Missing   bool   `json:"missing"`
	Detail    string `json:"detail,omitempty"`
}

// ok reports whether this file passed every check.
func (f FileResult) ok() bool {
	return !f.Missing && f.HashOK && f.ParseOK && f.VersionOK && f.Detail == ""
}

// VerifyReport is the outcome of verifying one snapshot: per-file results
// plus the manifest header, OK only when every file is fully OK. Hub/HubSlug
// echo the manifest's identity stamp (v2; empty on v1 manifests); Warning is
// a report-level finding — a snapshot that verifies byte-clean but would
// still be refused by restore (pre-v2, or stamped for a different hub).
type VerifyReport struct {
	Path            string       `json:"path"`
	CreatedAt       time.Time    `json:"createdAt"`
	Kind            Kind         `json:"kind"`
	ManifestVersion int          `json:"manifestVersion"`
	Hub             string       `json:"hub,omitempty"`
	HubSlug         string       `json:"hubSlug,omitempty"`
	Warning         string       `json:"warning,omitempty"`
	Files           []FileResult `json:"files"`
	OK              bool         `json:"ok"`
}

// Verify checks a snapshot directory against its manifest: every manifest
// entry must exist, re-hash to its recorded SHA-256, parse (JSON envelopes
// via json.Valid, JSONL logs line by line), and carry a schema version no
// newer than what this build writes for that file — `expected` maps a
// (source name, rel path) pair to the current version, reporting ok=false
// for files it has no schema authority over (which then always pass the
// version check). Files present in the snapshot but absent from the
// manifest are flagged too: a backup with unexplained extras is not a
// backup you can trust blindly.
func Verify(snapshotDir string, expected func(store, rel string) (int, bool)) (*VerifyReport, error) {
	m, err := ReadManifest(snapshotDir)
	if err != nil {
		return nil, err
	}
	rep := &VerifyReport{
		Path:            snapshotDir,
		CreatedAt:       m.CreatedAt,
		Kind:            m.Kind,
		ManifestVersion: m.ManifestVersion,
		Hub:             m.Hub,
		HubSlug:         m.HubSlug,
		Files:           []FileResult{},
	}

	inManifest := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		inManifest[f.Path] = true
		rep.Files = append(rep.Files, verifyFile(snapshotDir, f, expected))
	}

	// Extra files (anything on disk the manifest doesn't list, manifest.json
	// aside) fail the snapshot: they were not written by the engine.
	extras, err := extraFiles(snapshotDir, inManifest)
	if err != nil {
		return nil, err
	}
	rep.Files = append(rep.Files, extras...)

	rep.OK = true
	for _, f := range rep.Files {
		if !f.ok() {
			rep.OK = false
			break
		}
	}
	return rep, nil
}

// Verify (the Engine method) runs the package-level Verify and additionally
// surfaces the hub-identity findings CheckRestorable would refuse on, as a
// report-level Warning: a pre-hub-isolation manifest, or a snapshot stamped
// for a different hub than this engine. File-level OK is untouched — the
// bytes may be pristine; they are just not restorable HERE.
func (e *Engine) Verify(snapshotDir string, expected func(store, rel string) (int, bool)) (*VerifyReport, error) {
	rep, err := Verify(snapshotDir, expected)
	if err != nil {
		return nil, err
	}
	switch {
	case rep.ManifestVersion < 2:
		rep.Warning = "this backup predates hub isolation and cannot be restored; take a fresh backup"
	case rep.HubSlug != e.HubSlug || (rep.Hub != "" && e.Hub != "" && rep.Hub != e.Hub):
		rep.Warning = fmt.Sprintf("snapshot belongs to hub %q (profile %q), not this session's hub — restore will refuse it",
			rep.Hub, rep.HubSlug)
	}
	return rep, nil
}

// verifyFile runs all per-file checks for one manifest entry.
func verifyFile(snapshotDir string, mf ManifestFile, expected func(store, rel string) (int, bool)) FileResult {
	res := FileResult{Path: mf.Path}

	rel, err := safeRel(mf.Path)
	if err != nil {
		res.Detail = "invalid path in manifest"
		return res
	}
	data, err := os.ReadFile(filepath.Join(snapshotDir, filepath.FromSlash(rel)))
	if err != nil {
		res.Missing = true
		return res
	}

	sum := sha256.Sum256(data)
	res.HashOK = hex.EncodeToString(sum[:]) == mf.SHA256
	res.ParseOK = parseCheck(mf.Path, data)

	res.VersionOK = true
	if cur, known := expected(mf.Store, mf.Path); known && mf.SchemaVersion > cur {
		res.VersionOK = false
		res.Detail = fmt.Sprintf("schema v%d is newer than this build's v%d", mf.SchemaVersion, cur)
	}
	return res
}

// parseCheck validates a file's content by its name: .jsonl logs must be
// valid JSON on every non-empty line (the chat append-log invariant), .json
// files must be one valid JSON document (envelopes and opaque tldraw
// documents alike — json.Valid is the right bar for both). Anything else
// passes; the engine only writes JSON today.
func parseCheck(rel string, data []byte) bool {
	switch {
	case strings.HasSuffix(rel, ".jsonl"):
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			if !json.Valid([]byte(line)) {
				return false
			}
		}
		return true
	case strings.HasSuffix(rel, ".json"):
		return json.Valid(data)
	default:
		return true
	}
}

// extraFiles walks the snapshot and returns a failing FileResult for every
// file the manifest doesn't list (manifest.json itself excepted).
func extraFiles(snapshotDir string, inManifest map[string]bool) ([]FileResult, error) {
	out := []FileResult{}
	err := filepath.WalkDir(snapshotDir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(snapshotDir, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if rel == "manifest.json" || inManifest[rel] {
			return nil
		}
		out = append(out, FileResult{Path: rel, Detail: "not in manifest"})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("backup: scanning snapshot: %w", err)
	}
	return out, nil
}

// safeRel normalizes a manifest path and rejects anything that could point
// outside the snapshot (absolute paths, .. traversal) — the same rule the
// engine enforces when writing, re-checked here because a manifest is data
// read back from disk, not something we trust by construction.
func safeRel(rel string) (string, error) {
	clean := path.Clean(filepath.ToSlash(rel))
	if clean == "." || clean == "" || path.IsAbs(clean) ||
		clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path escapes the snapshot directory")
	}
	return clean, nil
}
