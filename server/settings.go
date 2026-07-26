package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schneik80/fusionlocalserver/config"
)

// defaultPort is the bind port when none is configured in server.json.
const defaultPort = 8080

// Settings holds web-server runtime preferences that a user can change at
// runtime — since hub isolation, only the listen port. It is stored
// separately from config.json — that file holds the APS identity
// (client_id/region) and has strict load rules — so persisting preferences
// never entangles with auth config, and the TUI ignores it entirely.
//
// The backup configuration used to live here; it is per-hub now
// (hubs/<slug>/backup.json — see hubstores.go). Old server.json files still
// carrying backup fields load fine (unknown JSON fields are ignored) and are
// migrated in the hub-layout migration.
type Settings struct {
	Port int `json:"port,omitempty"`
}

// settingsPath is ~/.config/fusionlocalserver/server.json. config.Dir creates the
// directory if needed.
func settingsPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server.json"), nil
}

// LoadSettings reads server.json. A missing file is not an error — it returns
// the zero Settings (Port 0 → caller falls back to defaultPort).
func LoadSettings() (Settings, error) {
	var s Settings
	path, err := settingsPath()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parsing %s: %w", path, err)
	}
	return s, nil
}

// SaveSettings writes server.json (0600, owner-only).
func SaveSettings(s Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// UpdateSettings applies mutate to the current persisted settings and saves
// the result — the load-modify-save cycle every writer must use so changing
// one preference (say, the port) never wipes the others (say, the backup
// config).
func UpdateSettings(mutate func(*Settings)) error {
	s, err := LoadSettings()
	if err != nil {
		return err
	}
	mutate(&s)
	return SaveSettings(s)
}
