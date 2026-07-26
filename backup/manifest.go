package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReadManifest loads and decodes a snapshot dir's manifest.json. It is the
// read half of what Run writes; verify/restore (phase 3c) build on it.
func ReadManifest(snapDir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(snapDir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("backup: reading manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("backup: decoding manifest: %w", err)
	}
	if m.ManifestVersion > ManifestVersion {
		return nil, fmt.Errorf("backup: manifest v%d is newer than this build understands (v%d)",
			m.ManifestVersion, ManifestVersion)
	}
	if m.Files == nil {
		m.Files = []ManifestFile{}
	}
	return &m, nil
}
