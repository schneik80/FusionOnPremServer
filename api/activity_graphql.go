package api

import (
	"context"
	"fmt"
	"sync"
)

// rollupFanout bounds concurrent per-document activity fetches during a child
// roll-up — enough to be fast on a large assembly without hammering the APS
// gateway (which would rate-limit and cause retries/incompleteness).
const rollupFanout = 12

// RollUpDesignActivity fetches the parent design's activity plus every supplied
// child document's activity (concurrently, bounded) and returns the merged event
// set. The parent fetch must succeed; a child whose activity errors is logged and
// skipped rather than failing the whole roll-up. Doing the fan-out server-side —
// instead of the browser firing one request per descendant — keeps a large
// assembly's roll-up reliable and complete.
func RollUpDesignActivity(ctx context.Context, token, hubID, parentItemID string, childItemIDs []string) ([]ActivityEvent, error) {
	ids := append([]string{parentItemID}, childItemIDs...)
	type result struct {
		events []ActivityEvent
		err    error
	}
	results := make([]result, len(ids))

	var wg sync.WaitGroup
	sem := make(chan struct{}, rollupFanout)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			evs, err := GetDesignActivity(ctx, token, hubID, id)
			results[i] = result{evs, err}
		}(i, id)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var all []ActivityEvent
	for i, r := range results {
		if r.err != nil {
			if i == 0 {
				return nil, fmt.Errorf("rollup parent activity: %w", r.err)
			}
			dbgLog("rollup: activity(%s) failed: %v", ids[i], r.err)
			continue
		}
		all = append(all, r.events...)
	}
	return all, nil
}

// GraphQL-backed activity acquisition. The first-party Fusion Team notifications
// feed (api/activity.go) rejects this app's token with HTTP 500, so design-scope
// activity is sourced from the Manufacturing Data Model GraphQL instead — the
// same itemVersions history the Details panel already uses, under the token that
// works everywhere else. Each version becomes one ActivityEvent, so the existing
// BuildReport aggregation/DTO/UI layers consume it unchanged.
//
// Unlike the feed, GraphQL ids are the DM/GraphQL ids the browser nav uses:
// hubID is the GraphQL hub id (not the feed slug) and itemID is the lineage urn.

// GetDesignActivity fetches a single design's version history via GraphQL and
// normalizes each version into an ActivityEvent. hubID is the GraphQL hub id and
// itemID is the item (lineage) id, exactly as the Details endpoint takes them.
//
// It uses a lean query — base item fields plus itemVersions — deliberately
// NOT requesting tipRootComponentVersion property fields. Those properties fail
// with "Individual property data … not yet available in the Fusion Cloud
// Information Model" for items not yet migrated, which would nullify the whole
// query; activity reports don't need them. The version rows themselves are the
// shared versionRowFields selection, paged like the Details query, so a long
// history is reported in full rather than cut at APS's first page.
func GetDesignActivity(ctx context.Context, token, hubID, itemID string) ([]ActivityEvent, error) {
	// Deliberately minimal — the report doesn't need fusionWebUrl, so it is not
	// requested. (gqlQuery now surfaces partial data when a leaf field like
	// item.fusionWebUrl flakily 500s, so requesting it would be safe; it is just
	// unused here.)
	const qFirst = `
		query DesignActivity($hubId: ID!, $itemId: ID!) {
			item(hubId: $hubId, itemId: $itemId) {
				__typename
				id
				name
				extensionType
				createdOn
				createdBy { id userName firstName lastName }
			}
			itemVersions(hubId: $hubId, itemId: $itemId, pagination: { limit: 50 }) {
				pagination { cursor }
				results {` + versionRowFields + `
				}
			}
		}`

	type rawActivityItem struct {
		Typename      string  `json:"__typename"`
		ID            string  `json:"id"`
		Name          string  `json:"name"`
		ExtensionType string  `json:"extensionType"`
		CreatedOn     string  `json:"createdOn"`
		CreatedBy     apiUser `json:"createdBy"`
	}

	item, versions, err := itemWithVersions[rawActivityItem](ctx, token, hubID, itemID, qFirst, "design activity")
	if err != nil {
		return nil, err
	}

	d := &ItemDetails{
		ID:            item.ID,
		Name:          item.Name,
		Typename:      item.Typename,
		ExtensionType: item.ExtensionType,
		CreatedOn:     parseTime(item.CreatedOn),
		CreatedBy:     item.CreatedBy.fullName(),
		Versions:      versions,
	}
	return designEventsFromDetails(d, hubID), nil
}

// designEventsFromDetails maps a design's version list to activity events. It is
// pure (no I/O) so the normalization is unit-testable. Events carry the lineage
// urn and the GraphQL hub id so inScope(design)/aggregation match on either id.
func designEventsFromDetails(d *ItemDetails, hubID string) []ActivityEvent {
	if d == nil {
		return nil
	}
	events := make([]ActivityEvent, 0, len(d.Versions))
	for _, v := range d.Versions {
		action := ActionUpdated
		if v.Number <= 1 {
			action = ActionCreated
		}
		events = append(events, ActivityEvent{
			EntityType:    "design",
			EntityID:      d.ID,
			EntityName:    d.Name,
			Timestamp:     v.CreatedOn,
			Action:        action,
			Actor:         Actor{DisplayName: v.CreatedBy},
			VersionNumber: v.Number,
			HubID:         hubID,
			LineageURN:    d.ID,
			FileType:      d.ExtensionType,
			WebURL:        d.FusionWebURL,
			CreatedOn:     d.CreatedOn,
			Owner:         Actor{DisplayName: d.CreatedBy},
			Detail:        v.Comment,
			Source:        "graphql",
		})
	}
	return events
}
