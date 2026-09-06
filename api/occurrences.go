package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// occurrenceRow is one instance in a design's flattened structure
// (componentVersion.allOccurrences): the component version it places and, for
// a Fusion design, the design item that owns that component.
type occurrenceRow struct {
	ComponentVersion struct {
		ID                string `json:"id"`
		Name              string `json:"name"`
		PartNumber        string `json:"partNumber"`
		PartDesc          string `json:"partDescription"`
		Material          string `json:"materialName"`
		DesignItemVersion struct {
			Item struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"item"`
		} `json:"designItemVersion"`
	} `json:"componentVersion"`
}

// allOccurrences walks a component version's ENTIRE structure — every
// instance at every depth — in one paginated query. It is the one source for
// both the bill of materials (GetBOM: group by component, count) and the
// descendant enumeration (GetAllDescendants: distinct designs), so the two
// share the same operation name and therefore the same coalesced pages: an
// Activity roll-up and a BOM on the same design within the cache window cost
// one walk.
//
// It replaced a breadth-first walk over `occurrences` that spawned one
// paginated query per node (up to 20 000 of them) and skipped per-node
// errors — a rate limit mid-walk used to return a silently shorter tree.
// This walk is sequential and fails loudly.
func allOccurrences(ctx context.Context, token, componentVersionID string) ([]occurrenceRow, error) {
	// The v2 API caps PaginationInput.limit at 50 (same as occurrences /
	// whereUsed); pagination walks the rest of a large assembly.
	const rowFields = `componentVersion { id name partNumber partDescription materialName designItemVersion { item { id name } } }`
	const qFirst = `
		query AllOccurrences($cvId: ID!) {
			componentVersion(componentVersionId: $cvId) {
				allOccurrences(pagination: { limit: 50 }) {
					pagination { cursor }
					results { ` + rowFields + ` }
				}
			}
		}`
	const qNext = `
		query AllOccurrencesNext($cvId: ID!, $cursor: String!) {
			componentVersion(componentVersionId: $cvId) {
				allOccurrences(pagination: { cursor: $cursor, limit: 50 }) {
					pagination { cursor }
					results { ` + rowFields + ` }
				}
			}
		}`
	return allPages(ctx, token, qFirst, qNext, map[string]any{"cvId": componentVersionID}, func(data json.RawMessage) (string, []occurrenceRow, error) {
		var r struct {
			ComponentVersion struct {
				AllOccurrences struct {
					Pagination struct {
						Cursor string `json:"cursor"`
					} `json:"pagination"`
					Results []occurrenceRow `json:"results"`
				} `json:"allOccurrences"`
			} `json:"componentVersion"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", nil, fmt.Errorf("all occurrences: %w", err)
		}
		return r.ComponentVersion.AllOccurrences.Pagination.Cursor, r.ComponentVersion.AllOccurrences.Results, nil
	})
}
