package production

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schneik80/fusionlocalserver/internal/migrate"
)

// Snapshot streams every project's production.json to visit for the backup
// engine, holding each project's mutex while that project's file is read so a
// backup never captures a mid-mutation file. rel paths are
// "production/<projectdir>/production.json". Project dirs whose file is
// unreadable or unrecognizable are skipped (one bad project must not abort
// the whole backup); an error from visit aborts the run.
func (s *Store) Snapshot(visit func(rel string, data []byte, schemaVersion int) error) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("production: scanning store dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(s.dir, e.Name(), "production.json")
		projectID, ok := peekProjectID(path)
		if !ok {
			continue
		}
		ps, perr := s.project(projectID)
		if perr != nil {
			continue
		}
		if err := snapshotOne(ps, path, "production/"+e.Name()+"/production.json", visit); err != nil {
			return err
		}
	}
	return nil
}

// snapshotOne reads one project's file under its lock and hands it to visit.
// A read error skips (nil); only visit errors propagate.
func snapshotOne(ps *projectState, path, rel string, visit func(rel string, data []byte, schemaVersion int) error) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	sv, _ := migrate.Peek(data)
	return visit(rel, data, sv)
}

// peekProjectID recovers the (unsanitized) project id a store file
// self-describes — the only way back from a slugged dir name to the URN the
// projects map is keyed by.
func peekProjectID(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var probe struct {
		ProjectID string `json:"projectId"`
	}
	if json.Unmarshal(data, &probe) != nil || probe.ProjectID == "" {
		return "", false
	}
	return probe.ProjectID, true
}
