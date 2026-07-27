package whiteboards

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schneik80/fusionlocalserver/internal/migrate"
)

// Snapshot streams every project's whiteboard files (whiteboards.json plus
// one doc-<id>.json per board) to visit for the backup engine, holding each
// project's mutex while that project's files are read so a backup never
// captures a mid-autosave document. rel paths are
// "whiteboards/<projectdir>/<file>"; .bak/.tmp/.vN.bak siblings are excluded
// by the file allow-list. Project dirs whose metadata is unreadable or
// unrecognizable are skipped; an error from visit aborts.
func (s *Store) Snapshot(visit func(rel string, data []byte, schemaVersion int) error) error {
	// Write out anything being edited right now, so a backup captures what is
	// on screen rather than a state up to RoomSaveMax stale. Before the walk
	// and outside any project lock — flushing takes those locks itself.
	s.liveRooms().FlushAll()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("whiteboards: scanning store dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.dir, e.Name())
		projectID, ok := peekProjectID(filepath.Join(dir, "whiteboards.json"))
		if !ok {
			continue
		}
		ps, perr := s.project(projectID)
		if perr != nil {
			continue
		}
		if err := snapshotProject(ps, dir, "whiteboards/"+e.Name(), visit); err != nil {
			return err
		}
	}
	return nil
}

// snapshotProject reads one project's files under its lock. Read errors skip
// the file; only visit errors propagate.
func snapshotProject(ps *projectState, dir, relPrefix string, visit func(rel string, data []byte, schemaVersion int) error) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		isDoc := strings.HasPrefix(name, "doc-") && strings.HasSuffix(name, ".json")
		isEnvelope := name == "whiteboards.json"
		if !isDoc && !isEnvelope {
			continue // allow-list only: .bak/.tmp/.vN.bak and strays stay out
		}
		data, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			continue
		}
		sv := 1 // tldraw documents are opaque snapshots; they version with tldraw
		if isEnvelope {
			sv, _ = migrate.Peek(data)
		}
		if err := visit(relPrefix+"/"+name, data, sv); err != nil {
			return err
		}
	}
	return nil
}

// peekProjectID recovers the (unsanitized) project id whiteboards.json
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
