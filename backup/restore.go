package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
)

// RestoreRoots names the two destinations a restore may write into. HubRoot
// is the hub profile directory (hubs/<slug>/ under the config dir) that every
// store/pins file restores into; ConfigDir is the config-dir root, used ONLY
// for the allow-listed config.json (server.json is skipped entirely). Keeping
// the roots explicit is what makes a restore structurally unable to write one
// hub's data into another hub's profile or into the global root.
type RestoreRoots struct {
	HubRoot   string
	ConfigDir string
}

// Restore copies a snapshot's files back into the given roots, replacing the
// live data. The sequence is deliberately conservative:
//
//  1. Validate: the manifest must load, no file may carry a schema version
//     newer than this build writes (`expected`, same hook as Verify), and
//     the snapshot's app version must not be newer than the running one
//     ("dev" and unparseable versions always pass — a dev build restores
//     anything, and version-string exotica must not brick a restore).
//  2. Read every file into memory first, re-checking each hash — a restore
//     never begins writing from a snapshot it can't fully read intact.
//  3. Take a pre-restore safety snapshot of the CURRENT data via e.Run;
//     if that fails, nothing has been touched and the restore aborts.
//  4. Write everything into configDir via atomic rename.
//
// Two files get special handling:
//   - config.json is MERGED: every field restores except client_secret,
//     which keeps the live value (backups blank it at capture time, so the
//     backup's copy is never authoritative; with no live file the blank
//     secret is written as-is).
//   - server.json is SKIPPED entirely. It holds live operational state —
//     the listen port and the backup dir/schedule — and restoring an old
//     copy could flip the port out from under the connected client or
//     silently repoint/disable backups. Operational settings are not data;
//     the file stays exactly as it is.
//
// The caller is responsible for evicting store caches and restarting the
// listener afterwards; Restore only moves bytes.
func (e *Engine) Restore(snapshotDir string, roots RestoreRoots, expected func(store, rel string) (int, bool)) error {
	m, err := ReadManifest(snapshotDir)
	if err != nil {
		return err
	}
	if snapshotNewerThanApp(m.AppVersion, e.AppVersion) {
		return fmt.Errorf("backup: snapshot was written by version %s, newer than the running %s — upgrade before restoring",
			m.AppVersion, e.AppVersion)
	}
	for _, f := range m.Files {
		if cur, known := expected(f.Store, f.Path); known && f.SchemaVersion > cur {
			return fmt.Errorf("backup: %s carries schema v%d, newer than this build's v%d — upgrade before restoring",
				f.Path, f.SchemaVersion, cur)
		}
	}

	// Read + hash-check everything up front so a corrupt or truncated
	// snapshot is refused before a single live byte changes.
	type pending struct {
		rel  string
		data []byte
	}
	files := make([]pending, 0, len(m.Files))
	for _, f := range m.Files {
		rel, rerr := safeRel(f.Path)
		if rerr != nil {
			return fmt.Errorf("backup: manifest entry %q: %w", f.Path, rerr)
		}
		if rel == "server.json" {
			continue // live operational state; see the doc comment
		}
		data, rerr := os.ReadFile(filepath.Join(snapshotDir, filepath.FromSlash(rel)))
		if rerr != nil {
			return fmt.Errorf("backup: snapshot file %s unreadable, refusing to restore: %w", rel, rerr)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			return fmt.Errorf("backup: snapshot file %s fails its checksum, refusing to restore (run verify for details)", rel)
		}
		if rel == "config.json" {
			if data, rerr = mergeConfigSecret(data, roots.ConfigDir); rerr != nil {
				return rerr
			}
		}
		files = append(files, pending{rel: rel, data: data})
	}

	// Safety net before any write: snapshot the data we are about to replace.
	if _, err := e.Run(KindPreRestore); err != nil {
		return fmt.Errorf("backup: pre-restore snapshot failed, refusing to restore: %w", err)
	}

	for _, f := range files {
		root := roots.HubRoot
		if f.rel == "config.json" {
			root = roots.ConfigDir // the one allow-listed global file
		}
		dest := filepath.Join(root, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return fmt.Errorf("backup: restoring %s: %w", f.rel, err)
		}
		if err := atomicfile.WriteFile(dest, f.data, 0600); err != nil {
			return fmt.Errorf("backup: restoring %s: %w", f.rel, err)
		}
	}
	return nil
}

// mergeConfigSecret rebuilds the config.json to restore: every field from
// the backup, but client_secret taken from the live file when one exists
// (the backup's copy was blanked at capture time). No live file → the
// backup's blank secret is written unchanged.
func mergeConfigSecret(backupData []byte, configDir string) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(backupData, &raw); err != nil {
		return nil, fmt.Errorf("backup: config.json in snapshot is not valid JSON: %w", err)
	}
	if live, err := os.ReadFile(filepath.Join(configDir, "config.json")); err == nil {
		var liveRaw map[string]any
		if json.Unmarshal(live, &liveRaw) == nil {
			if sec, ok := liveRaw["client_secret"]; ok {
				raw["client_secret"] = sec
			}
		}
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: re-encoding config.json: %w", err)
	}
	return out, nil
}

// snapshotNewerThanApp reports whether the snapshot's app version is
// strictly newer than the running one, semver-ishly: leading "v" tolerated,
// dot-separated numeric components, non-numeric suffixes ("-beta") cut the
// comparison short. "dev" or anything unparseable on either side compares
// as not-newer — the guard exists to stop obvious downgrades, not to make
// version-string trivia block a restore.
func snapshotNewerThanApp(snapVer, appVer string) bool {
	a, aok := parseVersion(snapVer)
	b, bok := parseVersion(appVer)
	if !aok || !bok {
		return false
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

// parseVersion extracts the numeric dot components of a version string.
func parseVersion(s string) ([]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if s == "" || s == "dev" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		i := 0
		for i < len(p) && p[i] >= '0' && p[i] <= '9' {
			i++
		}
		if i == 0 {
			return nil, false
		}
		n, err := strconv.Atoi(p[:i])
		if err != nil {
			return nil, false
		}
		out = append(out, n)
		if i != len(p) {
			break // "3-beta2" → compare up to the 3
		}
	}
	return out, true
}
