package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ItemDetails holds the rich metadata for a single item fetched from the API.
type ItemDetails struct {
	ID            string
	Name          string
	Typename      string // DesignItem | DrawingItem | ConfiguredDesignItem | BasicItem
	Size          string
	MimeType      string
	ExtensionType string
	FusionWebURL  string
	CreatedOn     time.Time
	CreatedBy     string
	ModifiedOn    time.Time
	ModifiedBy    string
	VersionNumber int
	// Design-specific (DesignItem / ConfiguredDesignItem)
	PartNumber  string
	PartDesc    string
	Material    string
	IsMilestone bool
	// Revision is the formal release revision of the tip (e.g. "B" for Rev B).
	// RESERVED — no API source today; populate it when release data becomes
	// available so the UI's document-state badge can show "Released - Rev X".
	Revision string
	// RootComponentVersionID is the id of tipRootComponentVersion — the
	// componentVersionId argument for the MFGDM component-version calls
	// (thumbnails today; a STEP derivative if one is ever added). Note that the
	// *native* archive path does not use it: api/archive.go works in the Data
	// Management id space, off the item's tip version urn.
	RootComponentVersionID string
	// Version history (most recent first)
	Versions []VersionSummary
}

// VersionSummary is one entry in the version history list.
type VersionSummary struct {
	Number    int
	CreatedOn time.Time
	CreatedBy string
	// CreatedByID is the APS user id of the author. Empty when the field could
	// not be resolved; the UI then groups its per-author history tracks by
	// display name instead.
	CreatedByID string
	Comment     string // version save comment (may be empty)
	// RootComponentVersionID is this version's root component version id — the
	// cvId used to fetch a per-version thumbnail. Empty when the field could not
	// be resolved (unmigrated design / partial GraphQL response).
	RootComponentVersionID string
	// IsMilestone marks this version as a milestone (the "release" lane in the
	// history graph). Defaults to false when the per-version field could not be
	// resolved.
	IsMilestone bool
	// Revision is the formal release revision (the "main" lane). RESERVED — no
	// API source exists today, so this is always empty.
	Revision string
	// PublicShare marks this version as having a public share (the rust-orange
	// "share" lane in the history graph). RESERVED — Fusion Team public shares
	// live in a separate service the MDM GraphQL doesn't expose, so this is
	// always false until a share source is wired in.
	PublicShare bool
}

// versionRowFields is the per-version selection every itemVersions query in
// this package shares — the details query, the activity twin, and the
// follow-on page query. One constant, so the three cannot drift apart again
// (they did: activity once selected no author id at all).
//
// itemVersions.results is typed ItemVersion (an interface); the per-version
// root component version (carrying isMilestone + the cvId for that version's
// thumbnail) lives on the concrete DesignItemVersion. Type-conditional, so
// non-design versions (drawings, etc.) simply omit it rather than erroring.
const versionRowFields = `
	versionNumber
	name
	createdOn
	createdBy { id userName firstName lastName }
	... on DesignItemVersion {
		rootComponentVersion {
			id
			isMilestone
		}
	}`

// itemVersionsNextQuery fetches the second and later pages of a version list.
// The first page rides inside the caller's own query (with the item fields),
// so the common short history still costs exactly one request.
//
// Pages are 50 rows: the ceiling the v2 API enforces on PaginationInput.limit
// elsewhere (occurrences, whereUsed), and — with the DesignItemVersion fragment
// on every row — comfortably under the 1000-point query-complexity cap. The
// PowerTools add-in found the internal endpoint's versions list accepts 100;
// nothing here needs to find out whether the public one does.
const itemVersionsNextQuery = `
	query GetItemVersionsNext($hubId: ID!, $itemId: ID!, $cursor: String!) {
		itemVersions(hubId: $hubId, itemId: $itemId, pagination: { cursor: $cursor, limit: 50 }) {
			pagination { cursor }
			results {` + versionRowFields + `
			}
		}
	}`

// rawVersion is one itemVersions row as the API returns it.
type rawVersion struct {
	VersionNumber        int     `json:"versionNumber"`
	Name                 string  `json:"name"`
	CreatedOn            string  `json:"createdOn"`
	CreatedBy            apiUser `json:"createdBy"`
	RootComponentVersion struct {
		ID          string `json:"id"`
		IsMilestone bool   `json:"isMilestone"`
	} `json:"rootComponentVersion"`
}

// rawVersionsPage is one page of itemVersions: the rows plus the cursor that
// says whether there is another.
type rawVersionsPage struct {
	Pagination struct {
		Cursor string `json:"cursor"`
	} `json:"pagination"`
	Results []rawVersion `json:"results"`
}

// versionsNewestFirst maps the rows of every page to VersionSummary, most
// recent first. The order is imposed here, by version number, not inherited:
// this code once reversed the list on the belief that APS returns oldest-first,
// and the 2026-09-04 probe (docs/history/STATUS.md) showed a paginated
// itemVersions answering newest-first (65 → 16, then a cursor). Sorting is
// correct whichever way a page arrives. Each design version's
// rootComponentVersion carries the per-version cvId (for that version's
// thumbnail) and its milestone flag; non-design versions leave these
// empty/false.
func versionsNewestFirst(rows []rawVersion) []VersionSummary {
	out := make([]VersionSummary, 0, len(rows))
	for _, v := range rows {
		out = append(out, VersionSummary{
			Number:                 v.VersionNumber,
			Comment:                v.Name,
			CreatedOn:              parseTime(v.CreatedOn),
			CreatedBy:              v.CreatedBy.fullName(),
			CreatedByID:            v.CreatedBy.ID,
			RootComponentVersionID: v.RootComponentVersion.ID,
			IsMilestone:            v.RootComponentVersion.IsMilestone,
			// Revision: reserved, no API source today.
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out
}

// itemWithVersions runs qFirst — a query selecting `item` plus the FIRST page
// of `itemVersions(pagination: { limit: 50 }) { pagination { cursor } results
// { versionRowFields } }` — and then pages the rest of the versions with
// itemVersionsNextQuery. The item is decoded (into I) from whichever page
// carries it, which is only the first; the follow-on query does not select it.
//
// This is the fix for the long-standing "itemVersions is not paginated" gap:
// a history longer than one page used to be silently truncated by APS before
// the UI ever saw it, and a day-by-day History view makes that far more
// visible than the old strip did. `what` prefixes errors ("item details").
func itemWithVersions[I any](ctx context.Context, token, hubID, itemID, qFirst, what string) (I, []VersionSummary, error) {
	var item I
	rows, err := allPages(ctx, token, qFirst, itemVersionsNextQuery,
		map[string]any{"hubId": hubID, "itemId": itemID},
		func(data json.RawMessage) (string, []rawVersion, error) {
			var r struct {
				Item         *I              `json:"item"`
				ItemVersions rawVersionsPage `json:"itemVersions"`
			}
			if err := json.Unmarshal(data, &r); err != nil {
				return "", nil, fmt.Errorf("decode: %w", err)
			}
			if r.Item != nil {
				item = *r.Item
			}
			return r.ItemVersions.Pagination.Cursor, r.ItemVersions.Results, nil
		})
	if err != nil {
		return item, nil, fmt.Errorf("%s: %w", what, err)
	}
	return item, versionsNewestFirst(rows), nil
}

// rawDetailsItem is the `item` selection of GetItemDetails as the API returns it.
type rawDetailsItem struct {
	Typename      string  `json:"__typename"`
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Size          string  `json:"size"`
	MimeType      string  `json:"mimeType"`
	ExtensionType string  `json:"extensionType"`
	FusionWebURL  string  `json:"fusionWebUrl"`
	CreatedOn     string  `json:"createdOn"`
	CreatedBy     apiUser `json:"createdBy"`
	ModifiedOn    string  `json:"lastModifiedOn"`
	ModifiedBy    apiUser `json:"lastModifiedBy"`
	TipVersion    struct {
		VersionNumber int `json:"versionNumber"`
	} `json:"tipVersion"`
	TipRootComponentVersion struct {
		ID          string `json:"id"`
		PartNumber  string `json:"partNumber"`
		PartDesc    string `json:"partDescription"`
		Material    string `json:"materialName"`
		IsMilestone bool   `json:"isMilestone"`
	} `json:"tipRootComponentVersion"`
}

// GetItemDetails fetches rich metadata for a single item plus its complete
// version list — every page of it, newest first.
func GetItemDetails(ctx context.Context, token, hubID, itemID string) (*ItemDetails, error) {
	const qFirst = `
		query GetItemDetails($hubId: ID!, $itemId: ID!) {
			item(hubId: $hubId, itemId: $itemId) {
				__typename
				id
				name
				size
				mimeType
				extensionType
				createdOn
				createdBy  { id userName firstName lastName }
				lastModifiedOn
				lastModifiedBy { id userName firstName lastName }
				... on DesignItem {
					fusionWebUrl
					tipVersion { versionNumber }
					tipRootComponentVersion {
						id
						partNumber
						partDescription
						materialName
						isMilestone
					}
				}
				... on DrawingItem {
					fusionWebUrl
					tipVersion { versionNumber }
				}
				... on ConfiguredDesignItem {
					fusionWebUrl
					tipVersion { versionNumber }
				}
			}
			itemVersions(hubId: $hubId, itemId: $itemId, pagination: { limit: 50 }) {
				pagination { cursor }
				results {` + versionRowFields + `
				}
			}
		}`

	item, versions, err := itemWithVersions[rawDetailsItem](ctx, token, hubID, itemID, qFirst, "item details")
	if err != nil {
		return nil, err
	}

	return &ItemDetails{
		ID:                     item.ID,
		Name:                   item.Name,
		Typename:               item.Typename,
		Size:                   item.Size,
		MimeType:               item.MimeType,
		ExtensionType:          item.ExtensionType,
		FusionWebURL:           item.FusionWebURL,
		CreatedOn:              parseTime(item.CreatedOn),
		CreatedBy:              item.CreatedBy.fullName(),
		ModifiedOn:             parseTime(item.ModifiedOn),
		ModifiedBy:             item.ModifiedBy.fullName(),
		VersionNumber:          item.TipVersion.VersionNumber,
		PartNumber:             item.TipRootComponentVersion.PartNumber,
		PartDesc:               item.TipRootComponentVersion.PartDesc,
		Material:               item.TipRootComponentVersion.Material,
		IsMilestone:            item.TipRootComponentVersion.IsMilestone,
		RootComponentVersionID: item.TipRootComponentVersion.ID,
		Versions:               versions,
	}, nil
}

// apiUser is a helper for deserialising User objects.
type apiUser struct {
	ID       string `json:"id"`
	UserName string `json:"userName"`
	First    string `json:"firstName"`
	Last     string `json:"lastName"`
}

// fullName is "First Last", whichever of the two exists, else the account's
// userName, else "". The userName fallback is what keeps a track from being a
// blank avatar: a service account or an unfinished profile carries no first
// or last name, and the History view's per-author tracks and hover cards
// still need to say who.
func (u apiUser) fullName() string {
	name := u.First
	if u.Last != "" {
		if name != "" {
			name += " "
		}
		name += u.Last
	}
	if name == "" {
		return u.UserName
	}
	return name
}

// parseTime parses an ISO-8601 / RFC-3339 timestamp returned by the API.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, _ = time.Parse("2006-01-02T15:04:05.000Z", s)
	}
	return t
}
