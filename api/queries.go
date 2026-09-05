package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// pageSize is the per-page result count requested from the GraphQL API.
// APS rejects values ≥ 100 ("pagination must be between 0 and 100") and
// also enforces a 1000-point query-complexity cap that 200 results blew
// through with our field set. 50 is the original safe value used since
// the project's first version.
const pageSize = 50

// maxPages bounds a single allPages walk. A server that keeps handing back a
// cursor is a bug, not data, and the loop must not follow it forever; but the
// bound is an ERROR, never a silent stop — a truncated folder listing or
// version history that looks complete is exactly the failure this codebase
// refuses to ship (see "Never cap silently" in .claude/CLAUDE.md). At 50 rows a
// page this is 10,000 rows, far past any real container or history.
const maxPages = 200

// allPages calls the API repeatedly until no next-page cursor is returned,
// accumulating typed results across pages. It is parameterised on T so the
// extract callback can decode straight into the caller's value type — no
// intermediate json.RawMessage round-trip per page.
//
// queryFirst is used for the first call (no cursor argument).
// queryNext  is used for all subsequent calls ($cursor: String! is required).
// baseVars   is the base variable map (without cursor); copied each call.
// extract    receives the raw JSON data and returns the next cursor plus the
//
//	decoded slice of T for that page.
func allPages[T any](
	ctx context.Context,
	token string,
	queryFirst, queryNext string,
	baseVars map[string]any,
	extract func(json.RawMessage) (cursor string, batch []T, err error),
) ([]T, error) {
	return allPagesAt(ctx, graphqlEndpoint, token, queryFirst, queryNext, baseVars, extract)
}

// allPagesAt is allPages against an explicit endpoint — the v3 endpoint for
// the one query that needs it (api/history.go).
func allPagesAt[T any](
	ctx context.Context,
	endpoint, token string,
	queryFirst, queryNext string,
	baseVars map[string]any,
	extract func(json.RawMessage) (cursor string, batch []T, err error),
) ([]T, error) {
	var all []T
	var cursor string
	first := true

	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("pagination: still receiving a cursor after %d pages, refusing to loop", maxPages)
		}
		vars := make(map[string]any, len(baseVars)+1)
		for k, v := range baseVars {
			vars[k] = v
		}

		var q string
		if first {
			q = queryFirst
			first = false
		} else {
			q = queryNext
			vars["cursor"] = cursor
		}

		data, err := gqlQueryAt(ctx, endpoint, token, q, vars)
		if err != nil {
			return nil, err
		}

		next, batch, err := extract(data)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		cursor = next
		if cursor == "" {
			break
		}
	}
	return all, nil
}

// ---------------------------------------------------------------------------
// GetHubs
// ---------------------------------------------------------------------------

func GetHubs(ctx context.Context, token string) ([]NavItem, error) {
	const qFirst = `
		query GetHubs {
			hubs(pagination: { limit: 50 }) {
				pagination { cursor }
				results {
					id name fusionWebUrl
					alternativeIdentifiers { dataManagementAPIHubId }
				}
			}
		}`
	const qNext = `
		query GetHubsNext($cursor: String!) {
			hubs(pagination: { cursor: $cursor, limit: 50 }) {
				pagination { cursor }
				results {
					id name fusionWebUrl
					alternativeIdentifiers { dataManagementAPIHubId }
				}
			}
		}`

	type hubResult struct {
		ID                     string `json:"id"`
		Name                   string `json:"name"`
		FusionWebURL           string `json:"fusionWebUrl"`
		AlternativeIdentifiers struct {
			DataManagementAPIHubID string `json:"dataManagementAPIHubId"`
		} `json:"alternativeIdentifiers"`
	}

	all, err := allPages(ctx, token, qFirst, qNext, nil, func(data json.RawMessage) (string, []hubResult, error) {
		var r struct {
			Hubs struct {
				Pagination struct {
					Cursor string `json:"cursor"`
				} `json:"pagination"`
				Results []hubResult `json:"results"`
			} `json:"hubs"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", nil, fmt.Errorf("hubs: %w", err)
		}
		return r.Hubs.Pagination.Cursor, r.Hubs.Results, nil
	})
	if err != nil {
		return nil, err
	}

	items := make([]NavItem, len(all))
	for i, h := range all {
		items[i] = NavItem{
			ID:          h.ID,
			Name:        h.Name,
			Kind:        "hub",
			AltID:       h.AlternativeIdentifiers.DataManagementAPIHubID,
			WebURL:      h.FusionWebURL,
			IsContainer: true,
		}
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// GetProjects
// ---------------------------------------------------------------------------

func GetProjects(ctx context.Context, token, hubID string) ([]NavItem, error) {
	const qFirst = `
		query GetProjects($hubId: ID!) {
			hub(hubId: $hubId) {
				projects(pagination: { limit: 50 }) {
					pagination { cursor }
					results {
						id name fusionWebUrl projectStatus projectType
						alternativeIdentifiers { dataManagementAPIProjectId }
					}
				}
			}
		}`
	const qNext = `
		query GetProjectsNext($hubId: ID!, $cursor: String!) {
			hub(hubId: $hubId) {
				projects(pagination: { cursor: $cursor, limit: 50 }) {
					pagination { cursor }
					results {
						id name fusionWebUrl projectStatus projectType
						alternativeIdentifiers { dataManagementAPIProjectId }
					}
				}
			}
		}`

	type projectResult struct {
		ID                     string `json:"id"`
		Name                   string `json:"name"`
		FusionWebURL           string `json:"fusionWebUrl"`
		ProjectStatus          string `json:"projectStatus"`
		ProjectType            string `json:"projectType"`
		AlternativeIdentifiers struct {
			DataManagementAPIProjectID string `json:"dataManagementAPIProjectId"`
		} `json:"alternativeIdentifiers"`
	}

	all, err := allPages(ctx, token, qFirst, qNext, map[string]any{"hubId": hubID}, func(data json.RawMessage) (string, []projectResult, error) {
		var r struct {
			Hub struct {
				Projects struct {
					Pagination struct {
						Cursor string `json:"cursor"`
					} `json:"pagination"`
					Results []projectResult `json:"results"`
				} `json:"projects"`
			} `json:"hub"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", nil, fmt.Errorf("projects: %w", err)
		}
		return r.Hub.Projects.Pagination.Cursor, r.Hub.Projects.Results, nil
	})
	if err != nil {
		return nil, err
	}

	items := make([]NavItem, 0, len(all))
	for _, p := range all {
		if strings.EqualFold(p.ProjectStatus, "inactive") {
			continue
		}
		items = append(items, NavItem{
			ID:          p.ID,
			Name:        p.Name,
			Kind:        "project",
			AltID:       p.AlternativeIdentifiers.DataManagementAPIProjectID,
			WebURL:      p.FusionWebURL,
			IsContainer: true,
		})
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// GetFolders
// ---------------------------------------------------------------------------

func GetFolders(ctx context.Context, token, projectID string) ([]NavItem, error) {
	const qFirst = `
		query GetFolders($projectId: ID!) {
			foldersByProject(projectId: $projectId, pagination: { limit: 50 }) {
				pagination { cursor }
				results { id name }
			}
		}`
	const qNext = `
		query GetFoldersNext($projectId: ID!, $cursor: String!) {
			foldersByProject(projectId: $projectId, pagination: { cursor: $cursor, limit: 50 }) {
				pagination { cursor }
				results { id name }
			}
		}`

	type folderResult struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	all, err := allPages(ctx, token, qFirst, qNext, map[string]any{"projectId": projectID}, func(data json.RawMessage) (string, []folderResult, error) {
		var r struct {
			FoldersByProject struct {
				Pagination struct {
					Cursor string `json:"cursor"`
				} `json:"pagination"`
				Results []folderResult `json:"results"`
			} `json:"foldersByProject"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", nil, fmt.Errorf("folders: %w", err)
		}
		return r.FoldersByProject.Pagination.Cursor, r.FoldersByProject.Results, nil
	})
	if err != nil {
		return nil, err
	}

	items := make([]NavItem, len(all))
	for i, f := range all {
		items[i] = NavItem{ID: f.ID, Name: f.Name, Kind: "folder", IsContainer: true}
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// GetProjectItems
// ---------------------------------------------------------------------------

func GetProjectItems(ctx context.Context, token, projectID string) ([]NavItem, error) {
	const qFirst = `
		query GetProjectItems($projectId: ID!) {
			itemsByProject(projectId: $projectId, pagination: { limit: 50 }) {
				pagination { cursor }
				results {
					__typename id name lastModifiedOn
					... on DesignItem { tipRootComponentVersion { id } }
				}
			}
		}`
	const qNext = `
		query GetProjectItemsNext($projectId: ID!, $cursor: String!) {
			itemsByProject(projectId: $projectId, pagination: { cursor: $cursor, limit: 50 }) {
				pagination { cursor }
				results {
					__typename id name lastModifiedOn
					... on DesignItem { tipRootComponentVersion { id } }
				}
			}
		}`

	all, err := allPages(ctx, token, qFirst, qNext, map[string]any{"projectId": projectID}, func(data json.RawMessage) (string, []itemResult, error) {
		var r struct {
			ItemsByProject struct {
				Pagination struct {
					Cursor string `json:"cursor"`
				} `json:"pagination"`
				Results []itemResult `json:"results"`
			} `json:"itemsByProject"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", nil, fmt.Errorf("project items: %w", err)
		}
		return r.ItemsByProject.Pagination.Cursor, r.ItemsByProject.Results, nil
	})
	if err != nil {
		return nil, err
	}

	items := make([]NavItem, len(all))
	for i, it := range all {
		items[i] = navItemFromResult(it)
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// GetItems
// ---------------------------------------------------------------------------

func GetItems(ctx context.Context, token, hubID, folderID string) ([]NavItem, error) {
	const qFirst = `
		query GetItems($hubId: ID!, $folderId: ID!) {
			itemsByFolder(hubId: $hubId, folderId: $folderId, pagination: { limit: 50 }) {
				pagination { cursor }
				results {
					__typename id name lastModifiedOn
					... on DesignItem { tipRootComponentVersion { id } }
				}
			}
		}`
	const qNext = `
		query GetItemsNext($hubId: ID!, $folderId: ID!, $cursor: String!) {
			itemsByFolder(hubId: $hubId, folderId: $folderId, pagination: { cursor: $cursor, limit: 50 }) {
				pagination { cursor }
				results {
					__typename id name lastModifiedOn
					... on DesignItem { tipRootComponentVersion { id } }
				}
			}
		}`

	all, err := allPages(ctx, token, qFirst, qNext, map[string]any{"hubId": hubID, "folderId": folderID}, func(data json.RawMessage) (string, []itemResult, error) {
		var r struct {
			ItemsByFolder struct {
				Pagination struct {
					Cursor string `json:"cursor"`
				} `json:"pagination"`
				Results []itemResult `json:"results"`
			} `json:"itemsByFolder"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", nil, fmt.Errorf("items: %w", err)
		}
		return r.ItemsByFolder.Pagination.Cursor, r.ItemsByFolder.Results, nil
	})
	if err != nil {
		return nil, err
	}

	items := make([]NavItem, len(all))
	for i, it := range all {
		items[i] = navItemFromResult(it)
	}
	return items, nil
}

// itemResult is the shared row shape returned by itemsByFolder and
// itemsByProject. The DesignItem inline fragment fills in TipRoot;
// drawings, configured designs, and folders leave it nil.
type itemResult struct {
	Typename   string `json:"__typename"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	ModifiedOn string `json:"lastModifiedOn"`
	TipRoot    *struct {
		ID string `json:"id"`
	} `json:"tipRootComponentVersion,omitempty"`
}

// navItemFromResult maps a raw items-list row to a NavItem. Three
// signals are combined:
//
//  1. GraphQL __typename — authoritative when APS recognises the row
//     (DesignItem, DrawingItem, ConfiguredDesignItem, Folder).
//  2. Filename extension — refines drawings into dwg/template and
//     covers Fusion Electronics types that may not be exposed as
//     distinct typenames (or whose typenames we haven't confirmed
//     because APS production disables __schema introspection).
//  3. tipRootComponentVersion.id — captured per design row so the
//     async assembly classifier can probe occurrences without a
//     second round-trip.
func navItemFromResult(it itemResult) NavItem {
	n := navItemFromTypename(it.ID, it.Name, it.Typename)

	// If the typename mapping fell through to "unknown", try to
	// recover a Kind from the filename extension. This is how
	// Electronics items (schematic/PCB/ECAD project) are identified
	// today — their APS typenames aren't documented and full schema
	// introspection is blocked, so the file extension is the most
	// reliable signal we have.
	if n.Kind == "unknown" {
		if k := kindFromExtension(it.Name); k != "" {
			n.Kind = k
		}
	}

	// Drawings refine into "dwg" vs "template" via filename
	// extension (.f2d vs .f2t). APS reports both as DrawingItem so
	// the typename alone is insufficient.
	if n.Kind == "drawing" {
		n.Subtype = drawingSubtypeFromExtension(it.Name)
	}

	if n.Kind == "design" && it.TipRoot != nil {
		n.ComponentVersionID = it.TipRoot.ID
	}
	n.ModifiedOn = parseTime(it.ModifiedOn)
	return n
}

// navItemFromTypename maps a GraphQL __typename to a NavItem.
func navItemFromTypename(id, name, typename string) NavItem {
	kind := "unknown"
	isContainer := false
	switch typename {
	case "DesignItem":
		kind = "design"
	case "ConfiguredDesignItem":
		kind = "configured"
	case "DrawingItem":
		kind = "drawing"
	case "Folder":
		kind = "folder"
		isContainer = true
	}
	return NavItem{ID: id, Name: name, Kind: kind, IsContainer: isContainer}
}

// kindFromExtension maps a Fusion file extension to a NavItem.Kind.
// Returns "" when the extension isn't recognised so the caller can
// leave the kind as whatever the typename mapping produced.
//
// The Fusion Electronics extensions (.fsch, .fbrd, .fprj) are a
// best-effort mapping — they're the formats Fusion writes to disk on
// export, but APS may surface electronics rows under a different
// extension when stored in the cloud. If electronics items land as
// "unknown" in the wild, this is the table to update.
func kindFromExtension(name string) string {
	switch strings.ToLower(extOf(name)) {
	case ".f3d":
		return "design"
	case ".f2d", ".f2t":
		return "drawing"
	case ".fsch":
		return "schematic"
	case ".fbrd":
		return "pcb"
	case ".fprj":
		return "ecad"
	}
	return ""
}

// drawingSubtypeFromExtension distinguishes a Fusion drawing template
// (.f2t) from a regular drawing (.f2d). Anything else gets the
// "dwg" subtype — drawings without an extension are vanishingly rare
// and "dwg" is the right default.
func drawingSubtypeFromExtension(name string) string {
	if strings.EqualFold(extOf(name), ".f2t") {
		return "template"
	}
	return "dwg"
}

// extOf returns the lowercased filename extension (including the dot)
// or "" when name has none. We don't use path/filepath.Ext because the
// APS item.name field can contain slashes / colons in legitimate names
// without those being path separators.
func extOf(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		switch name[i] {
		case '.':
			return name[i:]
		case '/', '\\':
			return ""
		}
	}
	return ""
}
