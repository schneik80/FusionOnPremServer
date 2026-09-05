package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// HistoryChange is one entry of a design's history that did NOT produce a
// version: a property edited, a milestone marked, a part number set, a
// component changed. It has an author and an instant but no version number and
// nothing to show a thumbnail of. Field names mirror VersionSummary on purpose
// so the web view can put both on one day row and one author track.
type HistoryChange struct {
	// Type is the raw GraphQL __typename ("PropertiesUpdatedHistoryChange").
	// The client labels the ones it knows and de-camel-cases the rest; the
	// server never turns it into English.
	Type        string
	CreatedOn   time.Time
	CreatedBy   string // "First Last", else userName
	CreatedByID string
	Comment     string // the row's description ("Estimated Cost: 100")
}

// isSaveChange reports whether a history row is a save — a version being
// written — rather than an edit that made no version. Saves already come from
// itemVersions, with version numbers and thumbnails, so the history's own copy
// of them is not shown as a change: ModelWrittenHistoryChange for a design
// (one per version, confirmed by count on 2026-09-04: five for five versions),
// DrawingItemWrittenHistoryChange and BasicItemWrittenHistoryChange for the
// other item kinds. They are still *used* — see HistorySave.
func isSaveChange(typename string) bool {
	return strings.HasSuffix(typename, "WrittenHistoryChange")
}

// The two history rows that mark a version rather than edit anything.
// VersionCreatedHistoryChange is a MILESTONE (its id decodes to "…~milestone";
// its description is the milestone's name — "Milestone V2", "Item Update", or
// whatever the user typed). RevisionCreatedHistoryChange is a RELEASE; its
// description is the revision label ("1", "A", "Rev B") and it carries no
// author. Both are stamped with the exact timestamp of the save they mark.
const (
	milestoneChange = "VersionCreatedHistoryChange"
	releaseChange   = "RevisionCreatedHistoryChange"
)

// autoMilestonePrefixes are the names Fusion gives milestones it creates for
// itself. A VersionCreatedHistoryChange with one of these is a milestone; any
// other name is one the user typed, which makes it a release. (The PowerTools
// add-in's rule, is_release_name, applied to the same data.)
var autoMilestonePrefixes = []string{"Milestone ", "Item Update"}

// isReleaseName reports whether a milestone name is a user-typed revision.
func isReleaseName(name string) bool {
	if name == "" {
		return false
	}
	for _, prefix := range autoMilestonePrefixes {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// HistorySave is one save as the history records it, NEWEST FIRST, carrying
// the markers the history attached to it. The history has no version numbers;
// the client joins these to itemVersions by position (both lists are newest
// first and there is one of these per version), which is what lights up the
// release fill and the milestone ring on a save's dot.
type HistorySave struct {
	CreatedOn time.Time
	// Milestone is the milestone's name, "" when the save is not one.
	Milestone string
	// Revision is the release label, "" when the save is not released.
	Revision string
}

// ItemHistory is what the v3 history yields for one item.
type ItemHistory struct {
	Changes []HistoryChange
	Saves   []HistorySave
}

// GetItemHistory returns a design's history from the v3 endpoint: the edits
// that made no version (newest first) and, separately, its saves with their
// milestone / release markers (newest first).
//
// This is the one query the app sends to v3. The v2 schema has no history at
// all — probed 2026-09-04: no `history` on DesignItem or Component, no `model`
// root, no HistoryChange type — while v3 hangs `history` straight off the item
// at the same item(hubId, itemId) root, and answers with the same user ids v2
// uses (verified on the same probe). Pages are 50 rows: a limit of 100 does
// not error, it returns `item: null`, which would read as an empty history.
//
// A non-design item yields empty lists and no error; a hub that is not
// Collaborative Editing yields an error, and the caller's saves-only history
// stands.
func GetItemHistory(ctx context.Context, token, hubID, itemID string) (*ItemHistory, error) {
	const rows = `pagination { cursor } results { __typename timestamp description author { id userName firstName lastName } }`
	const qFirst = `
		query GetItemHistory($hubId: ID!, $itemId: ID!) {
			item(hubId: $hubId, itemId: $itemId) {
				__typename
				... on DesignItem { history(pagination: { limit: 50 }) { ` + rows + ` } }
				... on ConfiguredDesignItem { history(pagination: { limit: 50 }) { ` + rows + ` } }
				... on DrawingItem { history(pagination: { limit: 50 }) { ` + rows + ` } }
			}
		}`
	const qNext = `
		query GetItemHistoryNext($hubId: ID!, $itemId: ID!, $cursor: String!) {
			item(hubId: $hubId, itemId: $itemId) {
				__typename
				... on DesignItem { history(pagination: { cursor: $cursor, limit: 50 }) { ` + rows + ` } }
				... on ConfiguredDesignItem { history(pagination: { cursor: $cursor, limit: 50 }) { ` + rows + ` } }
				... on DrawingItem { history(pagination: { cursor: $cursor, limit: 50 }) { ` + rows + ` } }
			}
		}`

	type rawChange struct {
		Typename    string  `json:"__typename"`
		Timestamp   string  `json:"timestamp"`
		Description string  `json:"description"`
		Author      apiUser `json:"author"`
	}

	all, err := allPagesAt(ctx, graphqlEndpointV3, token, qFirst, qNext,
		map[string]any{"hubId": hubID, "itemId": itemID},
		func(data json.RawMessage) (string, []rawChange, error) {
			var r struct {
				Item struct {
					History struct {
						Pagination struct {
							Cursor string `json:"cursor"`
						} `json:"pagination"`
						Results []rawChange `json:"results"`
					} `json:"history"`
				} `json:"item"`
			}
			if err := json.Unmarshal(data, &r); err != nil {
				return "", nil, fmt.Errorf("decode: %w", err)
			}
			return r.Item.History.Pagination.Cursor, r.Item.History.Results, nil
		})
	if err != nil {
		return nil, fmt.Errorf("item history: %w", err)
	}

	out := &ItemHistory{Changes: make([]HistoryChange, 0, len(all)), Saves: make([]HistorySave, 0, len(all))}
	type marker struct {
		at        time.Time
		milestone string
		revision  string
	}
	var markers []marker
	for _, c := range all {
		at := parseTime(c.Timestamp)
		switch {
		case isSaveChange(c.Typename):
			out.Saves = append(out.Saves, HistorySave{CreatedOn: at})
			continue
		case c.Typename == milestoneChange:
			m := marker{at: at, milestone: c.Description}
			if isReleaseName(c.Description) {
				m.revision = c.Description
			}
			markers = append(markers, m)
		case c.Typename == releaseChange:
			markers = append(markers, marker{at: at, revision: c.Description})
		}
		// A marker row is represented by the ring / fill on the save it
		// decorates, and only there. Drawing it as a change ring too put a
		// second marker on the same track at the same instant — redundant
		// on a real document (2026-09-05). (The PowerTools palette does show
		// them as rings, but its milestone data comes from elsewhere.)
		if c.Typename == milestoneChange || c.Typename == releaseChange {
			continue
		}
		out.Changes = append(out.Changes, HistoryChange{
			Type:        c.Typename,
			CreatedOn:   at,
			CreatedBy:   c.Author.fullName(),
			CreatedByID: c.Author.ID,
			Comment:     c.Description,
		})
	}
	// Newest first; the API's order is not relied on.
	sort.SliceStable(out.Changes, func(i, j int) bool { return out.Changes[i].CreatedOn.After(out.Changes[j].CreatedOn) })
	sort.SliceStable(out.Saves, func(i, j int) bool { return out.Saves[i].CreatedOn.After(out.Saves[j].CreatedOn) })

	// A marker decorates the save it is stamped with — the same instant to the
	// millisecond in every row seen so far — or, failing an exact match, the
	// latest save at or before it: a milestone marks the version that existed
	// when it was set.
	for _, m := range markers {
		i := saveIndexFor(out.Saves, m.at)
		if i < 0 {
			continue
		}
		if m.milestone != "" {
			out.Saves[i].Milestone = m.milestone
		}
		if m.revision != "" {
			out.Saves[i].Revision = m.revision
		}
	}
	return out, nil
}

// saveIndexFor picks the save (newest-first list) a marker at `at` belongs to:
// the one at exactly that instant, else the newest save at or before it, else -1.
func saveIndexFor(saves []HistorySave, at time.Time) int {
	if at.IsZero() {
		return -1
	}
	for i, s := range saves {
		if s.CreatedOn.Equal(at) {
			return i
		}
	}
	for i, s := range saves {
		if !s.CreatedOn.After(at) {
			return i
		}
	}
	return -1
}
