package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/testutil"
)

func TestDesignEventsFromDetails(t *testing.T) {
	mk := func(s string) time.Time {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("bad time %q: %v", s, err)
		}
		return tm
	}
	d := &ItemDetails{
		ID:            "urn:adsk.wipprod:dm.lineage:abc",
		Name:          "Cylinder Body",
		ExtensionType: "f3d",
		FusionWebURL:  "https://autodesk8083.autodesk360.com/g/data/xyz",
		CreatedOn:     mk("2026-05-18T14:52:43Z"),
		CreatedBy:     "Kevin Schneider",
		Versions: []VersionSummary{
			// GetItemDetails returns most-recent first.
			{Number: 3, CreatedOn: mk("2026-05-22T18:09:36Z"), CreatedBy: "Kevin Schneider", Comment: "User Saved"},
			{Number: 2, CreatedOn: mk("2026-05-20T19:46:34Z"), CreatedBy: "Kevin Schneider IMA"},
			{Number: 1, CreatedOn: mk("2026-05-18T14:52:43Z"), CreatedBy: "Kevin Schneider", Comment: "Item created"},
		},
	}

	events := designEventsFromDetails(d, "hub-gql-id")
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}

	// Every event carries the lineage urn (so design-scope filtering matches on
	// EntityID or LineageURN) and the GraphQL hub id.
	for _, e := range events {
		if e.EntityType != "design" {
			t.Errorf("entityType = %q, want design", e.EntityType)
		}
		if e.EntityID != d.ID || e.LineageURN != d.ID {
			t.Errorf("ids = %q/%q, want %q", e.EntityID, e.LineageURN, d.ID)
		}
		if e.HubID != "hub-gql-id" {
			t.Errorf("hubId = %q, want hub-gql-id", e.HubID)
		}
		if e.Source != "graphql" {
			t.Errorf("source = %q, want graphql", e.Source)
		}
		if !e.CreatedOn.Equal(d.CreatedOn) {
			t.Errorf("createdOn = %v, want %v", e.CreatedOn, d.CreatedOn)
		}
	}

	// v1 is "created"; later versions are "updated".
	byVer := map[int]ActivityEvent{}
	for _, e := range events {
		byVer[e.VersionNumber] = e
	}
	if byVer[1].Action != ActionCreated {
		t.Errorf("v1 action = %q, want %q", byVer[1].Action, ActionCreated)
	}
	if byVer[3].Action != ActionUpdated {
		t.Errorf("v3 action = %q, want %q", byVer[3].Action, ActionUpdated)
	}

	// Feeding the events through BuildReport produces a coherent design report:
	// one design, version count = tip version, two distinct contributors.
	rep := BuildReport(events, ScopeDesign, d.ID, BucketDay, time.Time{}, time.Time{})
	if rep.DesignCount != 1 {
		t.Errorf("designCount = %d, want 1", rep.DesignCount)
	}
	if rep.VersionCount != 3 {
		t.Errorf("versionCount = %d, want 3 (tip)", rep.VersionCount)
	}
	if rep.ContributorCount != 2 {
		t.Errorf("contributorCount = %d, want 2", rep.ContributorCount)
	}
	if rep.ScopeName != "Cylinder Body" {
		t.Errorf("scopeName = %q, want Cylinder Body", rep.ScopeName)
	}
	if !rep.CreatedOn.Equal(mk("2026-05-18T14:52:43Z")) {
		t.Errorf("createdOn = %v, want item creation", rep.CreatedOn)
	}
	if !rep.LastChange.Equal(mk("2026-05-22T18:09:36Z")) {
		t.Errorf("lastChange = %v, want newest version time", rep.LastChange)
	}
	if rep.TotalEvents != 3 {
		t.Errorf("totalEvents = %d, want 3", rep.TotalEvents)
	}
}

func TestDesignEventsFromDetails_Nil(t *testing.T) {
	if got := designEventsFromDetails(nil, "h"); got != nil {
		t.Errorf("nil details should yield nil events, got %v", got)
	}
}

func TestGetDesignActivity_PaginatesVersions(t *testing.T) {
	// The activity twin shares the paged version fetch, so a long history is
	// reported in full — one event per version across every page.
	var requests int32
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		switch atomic.AddInt32(&requests, 1) {
		case 1:
			return testutil.GraphQLResponse{Data: map[string]any{
				"item": map[string]any{"__typename": "DesignItem", "id": "urn:item:long", "name": "Long"},
				"itemVersions": map[string]any{
					"pagination": map[string]any{"cursor": "P2"},
					"results":    []any{versionRow(1, "a"), versionRow(2, "b")},
				},
			}}
		default:
			if req.Variables["cursor"] != "P2" {
				t.Errorf("cursor = %v, want P2", req.Variables["cursor"])
			}
			return testutil.GraphQLResponse{Data: map[string]any{
				"itemVersions": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results":    []any{versionRow(3, "c")},
				},
			}}
		}
	})
	swapEndpoint(t, srv.URL)

	events, err := GetDesignActivity(context.Background(), "tok", "h1", "item-long")
	if err != nil {
		t.Fatalf("GetDesignActivity: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	seen := map[int]bool{}
	for _, e := range events {
		if e.EntityID != "urn:item:long" || e.EntityName != "Long" {
			t.Errorf("event lost its item: %+v", e)
		}
		seen[e.VersionNumber] = true
	}
	for _, n := range []int{1, 2, 3} {
		if !seen[n] {
			t.Errorf("no event for version %d", n)
		}
	}
}
