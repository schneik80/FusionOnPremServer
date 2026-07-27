package whiteboards

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/docref"
)

// DocRefHit is one board carrying app cards that reference a document — the
// whiteboard half of the Where-Used graph's local sources. Count is how many
// cards on the board point at the document.
type DocRefHit struct {
	ProjectID   string
	HubID       string
	ProjectName string
	BoardID     string
	BoardNum    int64
	BoardName   string
	Count       int
	UpdatedAt   time.Time
	UpdatedBy   string
}

// FindDocRefs returns every board in the given projects whose canvas holds an
// fls:doc card for itemID.
//
// This is the most expensive of the local scans and the reason the prefilter
// exists: a board document runs to megabytes (MaxSnapshotBytes) and is stored
// whole, so a hub-wide lookup would otherwise mean parsing hundreds of
// megabytes of tldraw JSON. Instead each document is read once and rejected on
// a raw substring test (docref.MayContain — sequential I/O, no unmarshal);
// only a document that survives it is decoded and counted. Boards are visited
// newest-first so a truncating caller keeps the most recently touched ones.
//
// Scoping matches the sibling stores: the caller passes the projects the user
// may see, an empty set yields nothing, and unreadable or future-versioned
// files are skipped rather than failing the lookup.
func (s *Store) FindDocRefs(projectIDs []string, itemID string) ([]DocRefHit, error) {
	out := []DocRefHit{}
	if len(projectIDs) == 0 || itemID == "" {
		return out, nil
	}
	allow := make(map[string]struct{}, len(projectIDs))
	for _, id := range projectIDs {
		if id != "" {
			allow[id] = struct{}{}
		}
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("whiteboards: scanning store dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "whiteboards.json"))
		if err != nil {
			continue
		}
		var pf projectFile
		if err := json.Unmarshal(meta, &pf); err != nil || pf.Version > fileVersion {
			continue
		}
		if _, ok := allow[pf.ProjectID]; !ok {
			continue
		}
		boards := append([]*Board(nil), pf.Boards...)
		sort.Slice(boards, func(i, j int) bool {
			if boards[i] == nil || boards[j] == nil {
				return boards[j] == nil
			}
			return boards[i].UpdatedAt.After(boards[j].UpdatedAt)
		})
		for _, b := range boards {
			if b == nil {
				continue
			}
			doc, err := os.ReadFile(s.snapshotPath(pf.ProjectID, b.ID))
			if err != nil {
				continue // board never saved, or unreadable — not a failure
			}
			if !docref.MayContain(doc, itemID) {
				continue
			}
			n := docref.Count(string(doc), itemID)
			if n == 0 {
				continue // key matched by coincidence; no real token
			}
			out = append(out, DocRefHit{
				ProjectID:   pf.ProjectID,
				HubID:       pf.HubID,
				ProjectName: pf.ProjectName,
				BoardID:     b.ID,
				BoardNum:    b.Num,
				BoardName:   b.Name,
				Count:       n,
				UpdatedAt:   b.UpdatedAt,
				UpdatedBy:   b.UpdatedBy.Name,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectName != out[j].ProjectName {
			return out[i].ProjectName < out[j].ProjectName
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}
