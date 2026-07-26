package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/schneik80/fusionlocalserver/internal/migrate"
)

// funcSource adapts a name + snapshot func into a Source.
type funcSource struct {
	name string
	snap func(visit func(rel string, data []byte, schemaVersion int) error) error
}

func (f *funcSource) Name() string { return f.name }

func (f *funcSource) Snapshot(visit func(rel string, data []byte, schemaVersion int) error) error {
	return f.snap(visit)
}

// StoreSource wraps a store's Snapshot method (chat/tasks/production/
// whiteboards all expose the same signature) as a backup Source.
func StoreSource(name string, snap func(visit func(rel string, data []byte, schemaVersion int) error) error) Source {
	return &funcSource{name: name, snap: snap}
}

// PinsSource backs up the per-hub pin files (pins-<hubslug>.json) at the
// config dir root. Allow-list by glob: .bak/.tmp siblings don't match, and
// nothing else in the config dir is ever read.
func PinsSource(configDir string) Source {
	return &funcSource{name: "pins", snap: func(visit func(rel string, data []byte, schemaVersion int) error) error {
		matches, err := filepath.Glob(filepath.Join(configDir, "pins-*.json"))
		if err != nil {
			return err
		}
		sort.Strings(matches)
		for _, p := range matches {
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				continue // a vanished/unreadable pins file skips, not aborts
			}
			// Legacy bare-array pins (v0) fail Peek and report version 0 —
			// exactly the pre-envelope convention.
			sv, _ := migrate.Peek(data)
			if err := visit(filepath.Base(p), data, sv); err != nil {
				return err
			}
		}
		return nil
	}}
}

// ConfigSource backs up config.json (parsed, client_secret blanked,
// re-marshaled — the raw bytes are never copied, so the secret cannot leak
// into a backup) and server.json as-is. Strictly allow-list: no other file
// under the config dir is read, which is what keeps sessions.enc,
// session.key, tls-*.pem and server.log out of backups by construction.
func ConfigSource(configDir string) Source {
	return &funcSource{name: "config", snap: func(visit func(rel string, data []byte, schemaVersion int) error) error {
		if data, err := os.ReadFile(filepath.Join(configDir, "config.json")); err == nil {
			var raw map[string]any
			if json.Unmarshal(data, &raw) == nil {
				if _, ok := raw["client_secret"]; ok {
					raw["client_secret"] = ""
				}
				if out, merr := json.MarshalIndent(raw, "", "  "); merr == nil {
					if err := visit("config.json", out, 0); err != nil {
						return err
					}
				}
			}
			// Unparseable config.json is skipped: re-marshaling is the only
			// safe path, and copying bytes we can't inspect could leak the
			// secret.
		}
		if data, err := os.ReadFile(filepath.Join(configDir, "server.json")); err == nil {
			if err := visit("server.json", data, 0); err != nil {
				return err
			}
		}
		return nil
	}}
}
