package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ComponentRef describes a ComponentVersion shown in the Uses or
// Where Used tab. For Uses (occurrences), this is a sub-component used
// by the current design. For Where Used, this is a parent component
// that consumes the current design.
type ComponentRef struct {
	ID             string // ComponentVersion.id
	Name           string
	PartNumber     string
	PartDesc       string
	Material       string
	DesignItemID   string // owning DesignItem.id, if exposed
	DesignItemName string // owning DesignItem.name, if exposed
	FusionWebURL   string // owning DesignItem.fusionWebUrl, if exposed
}

// DrawingRef describes a DrawingVersion shown in the Drawings tab.
// DrawingItemID is the parent DrawingItem.id — needed to drive
// Show-In-Location navigation from this row to the Contents column.
type DrawingRef struct {
	ID            string
	Name          string
	DrawingItemID string
	ModifiedOn    time.Time
	ModifiedBy    string
	FusionWebURL  string
}

// GetOccurrences returns the immediate sub-component versions referenced
// by the given component version (the "Uses" relationship).
func GetOccurrences(ctx context.Context, token, componentVersionID string) ([]ComponentRef, error) {
	const qFirst = `
		query GetOccurrences($cvId: ID!) {
			componentVersion(componentVersionId: $cvId) {
				occurrences(pagination: { limit: 50 }) {
					pagination { cursor }
					results {
						id
						componentVersion {
							id name partNumber partDescription materialName
							designItemVersion { item { id name fusionWebUrl } }
						}
					}
				}
			}
		}`
	const qNext = `
		query GetOccurrencesNext($cvId: ID!, $cursor: String!) {
			componentVersion(componentVersionId: $cvId) {
				occurrences(pagination: { cursor: $cursor, limit: 50 }) {
					pagination { cursor }
					results {
						id
						componentVersion {
							id name partNumber partDescription materialName
							designItemVersion { item { id name fusionWebUrl } }
						}
					}
				}
			}
		}`

	type occResult struct {
		ID               string `json:"id"`
		ComponentVersion struct {
			ID                string `json:"id"`
			Name              string `json:"name"`
			PartNumber        string `json:"partNumber"`
			PartDesc          string `json:"partDescription"`
			Material          string `json:"materialName"`
			DesignItemVersion struct {
				Item struct {
					ID           string `json:"id"`
					Name         string `json:"name"`
					FusionWebURL string `json:"fusionWebUrl"`
				} `json:"item"`
			} `json:"designItemVersion"`
		} `json:"componentVersion"`
	}

	all, err := allPages(ctx, token, qFirst, qNext, map[string]any{"cvId": componentVersionID}, func(data json.RawMessage) (string, []occResult, error) {
		var r struct {
			ComponentVersion struct {
				Occurrences struct {
					Pagination struct {
						Cursor string `json:"cursor"`
					} `json:"pagination"`
					Results []occResult `json:"results"`
				} `json:"occurrences"`
			} `json:"componentVersion"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", nil, fmt.Errorf("occurrences: %w", err)
		}
		return r.ComponentVersion.Occurrences.Pagination.Cursor, r.ComponentVersion.Occurrences.Results, nil
	})
	if err != nil {
		return nil, err
	}

	refs := make([]ComponentRef, len(all))
	for i, o := range all {
		refs[i] = ComponentRef{
			ID:             o.ComponentVersion.ID,
			Name:           o.ComponentVersion.Name,
			PartNumber:     o.ComponentVersion.PartNumber,
			PartDesc:       o.ComponentVersion.PartDesc,
			Material:       o.ComponentVersion.Material,
			DesignItemID:   o.ComponentVersion.DesignItemVersion.Item.ID,
			DesignItemName: o.ComponentVersion.DesignItemVersion.Item.Name,
			FusionWebURL:   o.ComponentVersion.DesignItemVersion.Item.FusionWebURL,
		}
	}
	return refs, nil
}

// GetAllDescendants returns every distinct design anywhere below the given
// component version — the Activity tab's roll-up scope. It is one paginated
// allOccurrences walk (shared with GetBOM, see occurrences.go), deduplicated
// by design item so an instance placed a hundred times is one descendant. A
// component with no owning design item (a component created in place) is
// keyed by its own version id. Any error — a rate limit above all — fails the
// call rather than returning a silently shorter list.
func GetAllDescendants(ctx context.Context, token, rootCvID string) ([]ComponentRef, error) {
	all, err := allOccurrences(ctx, token, rootCvID)
	if err != nil {
		return nil, fmt.Errorf("descendants: %w", err)
	}
	seen := make(map[string]struct{}, len(all))
	out := make([]ComponentRef, 0, len(all))
	for _, o := range all {
		cv := o.ComponentVersion
		key := cv.DesignItemVersion.Item.ID
		if key == "" {
			key = cv.ID
		}
		if key == "" || key == rootCvID {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ComponentRef{
			ID:             cv.ID,
			Name:           cv.Name,
			PartNumber:     cv.PartNumber,
			PartDesc:       cv.PartDesc,
			Material:       cv.Material,
			DesignItemID:   cv.DesignItemVersion.Item.ID,
			DesignItemName: cv.DesignItemVersion.Item.Name,
		})
	}
	return out, nil
}

// GetDrawingSource returns the source design(s) referenced by the given
// drawing item. For drawings, "Uses" means the design the drawing was
// made from — the tip drawing version's componentVersion's owning
// DesignItem. Most drawings reference exactly one design, so the
// result list typically has a single entry; it is returned as a slice
// so the Uses tab renderer can stay polymorphic across DesignItems
// (occurrences) and DrawingItems (sources).
func GetDrawingSource(ctx context.Context, token, hubID, drawingItemID string) ([]ComponentRef, error) {
	const q = `
		query GetDrawingSource($hubId: ID!, $itemId: ID!) {
			item(hubId: $hubId, itemId: $itemId) {
				... on DrawingItem {
					tipDrawingVersion {
						componentVersion {
							id name partNumber partDescription materialName
							designItemVersion {
								item { id name fusionWebUrl }
							}
						}
					}
				}
			}
		}`

	data, err := gqlQuery(ctx, token, q, map[string]any{"hubId": hubID, "itemId": drawingItemID})
	if err != nil {
		return nil, fmt.Errorf("drawing source: %w", err)
	}

	var raw struct {
		Item struct {
			TipDrawingVersion struct {
				ComponentVersion struct {
					ID                string `json:"id"`
					Name              string `json:"name"`
					PartNumber        string `json:"partNumber"`
					PartDesc          string `json:"partDescription"`
					Material          string `json:"materialName"`
					DesignItemVersion struct {
						Item struct {
							ID           string `json:"id"`
							Name         string `json:"name"`
							FusionWebURL string `json:"fusionWebUrl"`
						} `json:"item"`
					} `json:"designItemVersion"`
				} `json:"componentVersion"`
			} `json:"tipDrawingVersion"`
		} `json:"item"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("drawing source decode: %w", err)
	}

	cv := raw.Item.TipDrawingVersion.ComponentVersion
	// If the drawing has no tipDrawingVersion (rare; uninitialized
	// drawing), or the source DesignItem is missing, return an empty
	// list so the UI shows the tab's empty state.
	if cv.DesignItemVersion.Item.ID == "" && cv.ID == "" {
		return nil, nil
	}
	return []ComponentRef{{
		ID:             cv.ID,
		Name:           cv.Name,
		PartNumber:     cv.PartNumber,
		PartDesc:       cv.PartDesc,
		Material:       cv.Material,
		DesignItemID:   cv.DesignItemVersion.Item.ID,
		DesignItemName: cv.DesignItemVersion.Item.Name,
		FusionWebURL:   cv.DesignItemVersion.Item.FusionWebURL,
	}}, nil
}

// GetWhereUsed returns the component versions that reference the given
// component version (the reverse-lookup "Where Used" relationship).
func GetWhereUsed(ctx context.Context, token, componentVersionID string) ([]ComponentRef, error) {
	const qFirst = `
		query GetWhereUsed($cvId: ID!) {
			componentVersion(componentVersionId: $cvId) {
				whereUsed(pagination: { limit: 50 }) {
					pagination { cursor }
					results {
						id name partNumber partDescription materialName
						designItemVersion { item { id name fusionWebUrl } }
					}
				}
			}
		}`
	const qNext = `
		query GetWhereUsedNext($cvId: ID!, $cursor: String!) {
			componentVersion(componentVersionId: $cvId) {
				whereUsed(pagination: { cursor: $cursor, limit: 50 }) {
					pagination { cursor }
					results {
						id name partNumber partDescription materialName
						designItemVersion { item { id name fusionWebUrl } }
					}
				}
			}
		}`

	type cvResult struct {
		ID                string `json:"id"`
		Name              string `json:"name"`
		PartNumber        string `json:"partNumber"`
		PartDesc          string `json:"partDescription"`
		Material          string `json:"materialName"`
		DesignItemVersion struct {
			Item struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				FusionWebURL string `json:"fusionWebUrl"`
			} `json:"item"`
		} `json:"designItemVersion"`
	}

	all, err := allPages(ctx, token, qFirst, qNext, map[string]any{"cvId": componentVersionID}, func(data json.RawMessage) (string, []cvResult, error) {
		var r struct {
			ComponentVersion struct {
				WhereUsed struct {
					Pagination struct {
						Cursor string `json:"cursor"`
					} `json:"pagination"`
					Results []cvResult `json:"results"`
				} `json:"whereUsed"`
			} `json:"componentVersion"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", nil, fmt.Errorf("whereUsed: %w", err)
		}
		return r.ComponentVersion.WhereUsed.Pagination.Cursor, r.ComponentVersion.WhereUsed.Results, nil
	})
	if err != nil {
		return nil, err
	}

	// APS returns one ComponentVersion per *version* of each parent design
	// that references this component, so a parent design with N saved
	// versions shows up N times. The user just wants to know which
	// designs reference theirs, not the per-version history — collapse
	// to one entry per DesignItem, keeping the first seen. Refs with no
	// DesignItemID (orphan component versions) are passed through so we
	// don't accidentally collapse all of them into a single row.
	seen := make(map[string]bool, len(all))
	refs := make([]ComponentRef, 0, len(all))
	for _, c := range all {
		diid := c.DesignItemVersion.Item.ID
		if diid != "" {
			if seen[diid] {
				continue
			}
			seen[diid] = true
		}
		refs = append(refs, ComponentRef{
			ID:             c.ID,
			Name:           c.Name,
			PartNumber:     c.PartNumber,
			PartDesc:       c.PartDesc,
			Material:       c.Material,
			DesignItemID:   diid,
			DesignItemName: c.DesignItemVersion.Item.Name,
			FusionWebURL:   c.DesignItemVersion.Item.FusionWebURL,
		})
	}
	return refs, nil
}

// GetDrawingsForDesign returns the unique drawing items that reference
// any version of the given design item.
//
// Rationale: in Fusion, a drawing references a specific *version* of a
// component. When the design is saved, the tip-root component version
// changes but the drawing keeps pointing at the older one — so the
// naive `componentVersion(tipRoot).drawingVersions` query returns
// empty for designs that have been edited since their drawing was
// created. The correct path is rooted at the DesignItem: walk every
// design version, collect each version's drawingItemVersions, then
// dedupe by the drawing's lineage URN so the user sees one row per
// unique drawing rather than one row per drawing-version-per-design-
// version.
//
// Pagination on versions is required because APS caps query
// complexity at 1000 points; the original limit-50×limit-50 form
// scored ~23000 and was rejected, and even 10×10 came in at 1026.
// The current 10 × 5 layout (10 design versions per round trip, up
// to 5 drawing-item-versions per design version) lands around 510
// points, well under the cap. Five drawings per design version
// covers the realistic case (most designs have 1–2 drawings); a
// design with >5 distinct drawings on the *same* version would lose
// the overflow on that version, but other versions of the design
// will still surface the same drawings via dedup.
//
// hubID is required because the underlying `item` query takes a hubId
// argument. Returns drawings sorted by latest modification timestamp
// (most recent first).
func GetDrawingsForDesign(ctx context.Context, token, hubID, designItemID string) ([]DrawingRef, error) {
	const qFirst = `
		query GetDrawingsForDesign($hubId: ID!, $itemId: ID!) {
			item(hubId: $hubId, itemId: $itemId) {
				... on DesignItem {
					versions(pagination: { limit: 10 }) {
						pagination { cursor }
						results {
							drawingItemVersions(pagination: { limit: 5 }) {
								results {
									lastModifiedOn
									lastModifiedBy { firstName lastName }
									item { id name fusionWebUrl }
								}
							}
						}
					}
				}
			}
		}`
	const qNext = `
		query GetDrawingsForDesignNext($hubId: ID!, $itemId: ID!, $cursor: String!) {
			item(hubId: $hubId, itemId: $itemId) {
				... on DesignItem {
					versions(pagination: { cursor: $cursor, limit: 10 }) {
						pagination { cursor }
						results {
							drawingItemVersions(pagination: { limit: 5 }) {
								results {
									lastModifiedOn
									lastModifiedBy { firstName lastName }
									item { id name fusionWebUrl }
								}
							}
						}
					}
				}
			}
		}`

	type divResult struct {
		ModifiedOn string  `json:"lastModifiedOn"`
		ModifiedBy apiUser `json:"lastModifiedBy"`
		Item       struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			FusionWebURL string `json:"fusionWebUrl"`
		} `json:"item"`
	}

	type accum struct {
		ref     DrawingRef
		modTime time.Time
	}
	byLineage := make(map[string]*accum)

	var cursor string
	first := true
	for {
		vars := map[string]any{"hubId": hubID, "itemId": designItemID}
		q := qFirst
		if !first {
			q = qNext
			vars["cursor"] = cursor
		}
		first = false

		data, err := gqlQuery(ctx, token, q, vars)
		if err != nil {
			return nil, fmt.Errorf("drawings for design: %w", err)
		}

		var raw struct {
			Item struct {
				Versions struct {
					Pagination struct {
						Cursor string `json:"cursor"`
					} `json:"pagination"`
					Results []struct {
						DrawingItemVersions struct {
							Results []divResult `json:"results"`
						} `json:"drawingItemVersions"`
					} `json:"results"`
				} `json:"versions"`
			} `json:"item"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("drawings for design decode: %w", err)
		}

		// Walk every design-version's drawing list on this page.
		// Dedupe by drawing lineage URN, keeping the most-recently-
		// modified version so the row's metadata reflects the latest
		// activity on that drawing.
		for _, v := range raw.Item.Versions.Results {
			for _, d := range v.DrawingItemVersions.Results {
				lineage := d.Item.ID
				if lineage == "" {
					continue
				}
				modTime := parseTime(d.ModifiedOn)
				cur, exists := byLineage[lineage]
				if !exists || modTime.After(cur.modTime) {
					byLineage[lineage] = &accum{
						modTime: modTime,
						ref: DrawingRef{
							Name:          d.Item.Name,
							DrawingItemID: lineage,
							ModifiedOn:    modTime,
							ModifiedBy:    d.ModifiedBy.fullName(),
							FusionWebURL:  d.Item.FusionWebURL,
						},
					}
				}
			}
		}

		cursor = raw.Item.Versions.Pagination.Cursor
		if cursor == "" {
			break
		}
	}

	refs := make([]DrawingRef, 0, len(byLineage))
	for _, a := range byLineage {
		refs = append(refs, a.ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].ModifiedOn.After(refs[j].ModifiedOn)
	})
	return refs, nil
}
