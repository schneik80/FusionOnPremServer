package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// ClassifyAssembly reports whether the design rooted at the given
// component-version id has at least one direct sub-component. The query
// asks the occurrences relationship for a single result; an empty
// response means the design is a part, any result means an assembly.
//
// Concurrency is bounded by the budget scheduler's P1 lane (the route is a
// per-row probe), which sees every session at once; a package semaphore on
// top of it only lowered throughput. Callers should pair the returned bool with the originating item id and a generation
// counter so late-arriving refinements after a folder change can be
// dropped on the floor.
func ClassifyAssembly(ctx context.Context, token, componentVersionID string) (bool, error) {
	if componentVersionID == "" {
		return false, fmt.Errorf("classify: empty componentVersionID")
	}
	const q = `
		query ClassifyAssembly($cv: ID!) {
			componentVersion(componentVersionId: $cv) {
				occurrences(pagination: { limit: 1 }) {
					results { id }
				}
			}
		}`
	data, err := gqlQuery(ctx, token, q, map[string]any{"cv": componentVersionID})
	if err != nil {
		return false, err
	}
	var r struct {
		ComponentVersion struct {
			Occurrences struct {
				Results []struct {
					ID string `json:"id"`
				} `json:"results"`
			} `json:"occurrences"`
		} `json:"componentVersion"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return false, fmt.Errorf("classify decode: %w", err)
	}
	return len(r.ComponentVersion.Occurrences.Results) > 0, nil
}
