package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// FolderRef is a single hop in an item's folder ancestry.
type FolderRef struct {
	ID   string
	Name string
}

// ItemLocation describes where an item lives — used to drive
// "Show in Location" navigation from the Uses / Where Used / Drawings
// tabs into the Contents column.
type ItemLocation struct {
	HubID        string
	ProjectID    string // GraphQL ID — used to find the row in m.cols[colProjects]
	ProjectAltID string // dataManagementAPIProjectId — needed for Fusion MCP integration
	ProjectName  string
	// FolderPath is the ancestor chain from the project root down to
	// the folder that directly contains the item. Empty when the item
	// sits in the project root.
	FolderPath []FolderRef
}

// locateDepth is how many parentFolder levels one LocateItem query nests.
// Well under the schema's depth limit of 20, and deeper than any real Fusion
// Team tree; the iterative walk below only continues past it.
const locateDepth = 8

// nestedParentFolder builds `parentFolder { id name parentFolder { … } }`
// n levels deep.
func nestedParentFolder(n int) string {
	if n <= 0 {
		return ""
	}
	return "parentFolder { id name " + nestedParentFolder(n-1) + " }"
}

// folderChain is the recursive shape the nested selection decodes into.
type folderChain struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	ParentFolder *folderChain `json:"parentFolder"`
}

// GetItemLocation looks up an item's project + folder ancestry. One query
// nests the parentFolder chain locateDepth levels deep — a single round trip
// for any real tree, where the old walk paid one per ancestor — and falls back
// to walking further only if the chain is still unfinished at that depth.
func GetItemLocation(ctx context.Context, token, hubID, itemID string) (*ItemLocation, error) {
	itemQ := `
		query LocateItem($hubId: ID!, $itemId: ID!) {
			item(hubId: $hubId, itemId: $itemId) {
				project {
					id name
					hub { id }
					alternativeIdentifiers { dataManagementAPIProjectId }
				}
				` + nestedParentFolder(locateDepth) + `
			}
		}`

	data, err := gqlQuery(ctx, token, itemQ, map[string]any{"hubId": hubID, "itemId": itemID})
	if err != nil {
		return nil, fmt.Errorf("locate item: %w", err)
	}

	var raw struct {
		Item struct {
			Project struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Hub  struct {
					ID string `json:"id"`
				} `json:"hub"`
				AlternativeIdentifiers struct {
					DataManagementAPIProjectID string `json:"dataManagementAPIProjectId"`
				} `json:"alternativeIdentifiers"`
			} `json:"project"`
			ParentFolder *folderChain `json:"parentFolder"`
		} `json:"item"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("locate item decode: %w", err)
	}
	if raw.Item.Project.ID == "" {
		return nil, fmt.Errorf("item %q has no project", itemID)
	}

	loc := &ItemLocation{
		HubID:        raw.Item.Project.Hub.ID,
		ProjectID:    raw.Item.Project.ID,
		ProjectName:  raw.Item.Project.Name,
		ProjectAltID: raw.Item.Project.AlternativeIdentifiers.DataManagementAPIProjectID,
	}

	// Unfold the nested chain leaf-first. If it is still going at the deepest
	// level we asked for, walk the rest one level per query (the old way).
	var ancestry []FolderRef
	var lastID string
	depth := 0
	for cur := raw.Item.ParentFolder; cur != nil && cur.ID != ""; cur = cur.ParentFolder {
		ancestry = append(ancestry, FolderRef{ID: cur.ID, Name: cur.Name})
		lastID = cur.ID
		depth++
	}
	if depth >= locateDepth && lastID != "" {
		const folderQ = `
			query GetFolderParent($hubId: ID!, $folderId: ID!) {
				folderByHubId(hubId: $hubId, folderId: $folderId) {
					parentFolder { id name }
				}
			}`
		cur := lastID
		// Cap iterations defensively — a malformed schema response with a
		// cycle would otherwise spin forever.
		for i := 0; cur != "" && i < 100; i++ {
			d, err := gqlQuery(ctx, token, folderQ, map[string]any{"hubId": hubID, "folderId": cur})
			if err != nil {
				return nil, fmt.Errorf("walk folder %q: %w", cur, err)
			}
			var r struct {
				FolderByHubId struct {
					ParentFolder struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"parentFolder"`
				} `json:"folderByHubId"`
			}
			if err := json.Unmarshal(d, &r); err != nil {
				return nil, fmt.Errorf("walk folder decode: %w", err)
			}
			cur = r.FolderByHubId.ParentFolder.ID
			if cur != "" {
				ancestry = append(ancestry, FolderRef{ID: cur, Name: r.FolderByHubId.ParentFolder.Name})
			}
		}
	}

	// Reverse leaf-first → root-first.
	for i, j := 0, len(ancestry)-1; i < j; i, j = i+1, j-1 {
		ancestry[i], ancestry[j] = ancestry[j], ancestry[i]
	}
	loc.FolderPath = ancestry
	return loc, nil
}
