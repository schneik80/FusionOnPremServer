package production

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/schneik80/fusionlocalserver/internal/docref"
)

// DocRefHit is one job or batch that references a document — the production
// half of the Where-Used graph's local sources. A hit with an empty BatchID is
// a plan-level reference (the document is pinned to a step of the job's plan);
// a hit with a BatchID is a run-level one (frozen plan copy, a fulfilment, or
// an attached fls:doc ref).
//
// Plan and batch hits are reported separately on purpose: a batch freezes its
// own copy of the plan, so "this drawing is pinned to job J-4" and "this
// drawing was used in run B-12" are different facts, and the graph lets the
// user switch either off.
type DocRefHit struct {
	ProjectID   string
	HubID       string
	ProjectName string
	JobID       string
	JobNum      int64
	JobName     string
	BatchID     string
	BatchNum    int64
	BatchName   string
	BatchKind   string
	// Via is how the document is referenced — "step" (pinned to a step's
	// plan), "fulfillment" (supplied into a run) or "reference" (an fls:doc
	// token attached to the run). It is an enum token the UI renders; Where
	// carries the step title when Via is "step" (user data, never translated).
	Via   string
	Where string
	Count int
}

// Via values. Reported in the order a hit is discovered, so a document pinned
// to a step and also supplied as a fulfilment reports "step".
const (
	ViaStep        = "step"
	ViaFulfillment = "fulfillment"
	ViaReference   = "reference"
)

// FindDocRefs returns every job and batch in the given projects that
// references itemID.
//
// Scoping matches ListForProjects exactly, for the same security reason: the
// caller passes the set of projects the user may see and an empty set yields
// nothing. Reads go straight to disk; unreadable, corrupt, or future-versioned
// files are skipped rather than failing the lookup. Cost is one file per
// project and no APS call — production pins record the lineage urn as a plain
// field, so matching never needs a version resolve.
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
		return nil, fmt.Errorf("production: scanning store dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "production.json"))
		if err != nil {
			continue
		}
		// Whole-file prefilter: a project that never pins this document is
		// rejected without unmarshalling it.
		if !docref.MayMention(data, itemID) {
			continue
		}
		var pf projectFile
		if err := json.Unmarshal(data, &pf); err != nil || pf.Version > fileVersion {
			continue
		}
		if _, ok := allow[pf.ProjectID]; !ok {
			continue
		}
		for _, j := range pf.Jobs {
			if j == nil {
				continue
			}
			base := DocRefHit{
				ProjectID:   pf.ProjectID,
				HubID:       pf.HubID,
				ProjectName: pf.ProjectName,
				JobID:       j.ID,
				JobNum:      j.Num,
				JobName:     j.Name,
			}
			// Plan level: version-pinned documents on the job's steps.
			plan := base
			for _, st := range j.Steps {
				if st == nil {
					continue
				}
				for _, pd := range st.PlanDocs {
					if pd.Doc.ItemID != itemID {
						continue
					}
					plan.Count++
					if plan.Via == "" {
						plan.Via, plan.Where = ViaStep, st.Title
					}
				}
			}
			if plan.Count > 0 {
				out = append(out, plan)
			}
			// Run level: each batch's frozen plan, its fulfilments, and any
			// fls:doc tokens attached to the run.
			for _, b := range j.Batches {
				if b == nil {
					continue
				}
				hit := base
				hit.BatchID, hit.BatchNum, hit.BatchName, hit.BatchKind = b.ID, b.Num, b.Name, b.Kind
				for _, bs := range b.Steps {
					for _, pd := range bs.PlanDocs {
						if pd.Doc.ItemID != itemID {
							continue
						}
						hit.Count++
						if hit.Via == "" {
							hit.Via, hit.Where = ViaStep, bs.Title
						}
					}
				}
				for _, f := range b.Fulfillments {
					if f.Doc.ItemID != itemID {
						continue
					}
					hit.Count++
					if hit.Via == "" {
						hit.Via = ViaFulfillment
					}
				}
				for _, ref := range b.Refs {
					if !docref.MatchesToken(ref, itemID) {
						continue
					}
					hit.Count++
					if hit.Via == "" {
						hit.Via = ViaReference
					}
				}
				if hit.Count > 0 {
					out = append(out, hit)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectName != out[j].ProjectName {
			return out[i].ProjectName < out[j].ProjectName
		}
		if out[i].JobNum != out[j].JobNum {
			return out[i].JobNum < out[j].JobNum
		}
		return out[i].BatchNum < out[j].BatchNum
	})
	return out, nil
}
