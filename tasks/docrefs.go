package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/schneik80/fusionlocalserver/internal/docref"
)

// DocRefHit is one task that references a document — the task half of the
// document Where-Used graph's local sources. Count is how many references the
// task holds (attached doc cards plus any inline in its description), so the
// graph can label a node without listing them.
type DocRefHit struct {
	ProjectID   string
	HubID       string
	ProjectName string
	TaskID      string
	Num         int64
	Title       string
	Status      string
	Count       int
}

// FindDocRefs returns every task in the given projects that references itemID.
//
// Scoping matches ListForProjects exactly, for the same security reason: the
// caller passes the set of projects the user may see and an empty set yields
// nothing (never "all projects"). Reads go straight to disk; unreadable,
// corrupt, or future-versioned files are skipped rather than failing the whole
// lookup. Cost is one small file per project — the same scan the hub dashboard
// already runs — with no APS call anywhere.
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
		return nil, fmt.Errorf("tasks: scanning store dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "tasks.json"))
		if err != nil {
			continue
		}
		// Whole-file prefilter: a project whose tasks never mention this
		// document is rejected without unmarshalling it.
		if !docref.MayContain(data, itemID) {
			continue
		}
		var pf projectFile
		if err := json.Unmarshal(data, &pf); err != nil || pf.Version > fileVersion {
			continue
		}
		if _, ok := allow[pf.ProjectID]; !ok {
			continue
		}
		for _, t := range pf.Tasks {
			if t == nil {
				continue
			}
			n := docref.Count(t.Description, itemID)
			for _, ref := range t.DocRefs {
				if docref.MatchesToken(ref, itemID) {
					n++
				}
			}
			if n == 0 {
				continue
			}
			out = append(out, DocRefHit{
				ProjectID:   pf.ProjectID,
				HubID:       pf.HubID,
				ProjectName: pf.ProjectName,
				TaskID:      t.ID,
				Num:         t.Num,
				Title:       t.Title,
				Status:      t.Status,
				Count:       n,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectName != out[j].ProjectName {
			return out[i].ProjectName < out[j].ProjectName
		}
		return out[i].Num < out[j].Num
	})
	return out, nil
}
