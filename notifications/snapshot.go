package notifications

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schneik80/fusionlocalserver/internal/migrate"
)

// Snapshot streams every user's inbox file to visit for the backup engine,
// holding each user's mutex while that file is read so a backup never
// captures a mid-mutation file. rel paths are "notifications/<userkey>.json".
// Files that are unreadable or don't self-describe a user key are skipped
// (one bad file must not abort the whole backup); an error from visit aborts.
func (s *Store) Snapshot(visit func(rel string, data []byte, schemaVersion int) error) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("notifications: scanning store dir: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(s.dir, name)
		userKey, ok := peekUserKey(path)
		if !ok {
			continue
		}
		us, perr := s.user(userKey)
		if perr != nil {
			continue
		}
		if err := snapshotOne(us, path, "notifications/"+name, visit); err != nil {
			return err
		}
	}
	return nil
}

// snapshotOne reads one user's file under its lock and hands it to visit. A
// read error skips (nil); only visit errors propagate.
func snapshotOne(us *userState, path, rel string, visit func(rel string, data []byte, schemaVersion int) error) error {
	us.mu.Lock()
	defer us.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	sv, _ := migrate.Peek(data)
	return visit(rel, data, sv)
}

// peekUserKey recovers the (unsanitized) user key a file self-describes — the
// way back from a slugged filename to the OIDC sub the users map is keyed by.
func peekUserKey(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var probe struct {
		UserKey string `json:"userKey"`
	}
	if json.Unmarshal(data, &probe) != nil || probe.UserKey == "" {
		return "", false
	}
	return probe.UserKey, true
}
