package api

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/testutil"
)

func changeRow(typename, at, desc string, author map[string]any) map[string]any {
	row := map[string]any{"__typename": typename, "timestamp": at, "description": desc}
	if author != nil {
		row["author"] = author
	} else {
		row["author"] = nil // a release has no author — the API sends null
	}
	return row
}

func TestGetItemHistory_ChangesPagesAndOrders(t *testing.T) {
	ada := map[string]any{"id": "user-ada", "firstName": "Ada", "lastName": "Lovelace"}
	// A profile with no first/last name — the History view must still say who.
	bot := map[string]any{"id": "user-bot", "userName": "build-bot"}

	var requests int32
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		n := atomic.AddInt32(&requests, 1)
		// The per-author tracks key on the author id, and the userName is the
		// fallback name; a silently dropped field would regress both.
		if !strings.Contains(req.Query, "history(pagination") ||
			!strings.Contains(req.Query, "author { id userName firstName lastName }") {
			t.Errorf("request %d does not select the history rows:\n%s", n, req.Query)
		}
		switch n {
		case 1:
			if _, has := req.Variables["cursor"]; has {
				t.Errorf("page 1 carries a cursor: %v", req.Variables["cursor"])
			}
			return testutil.GraphQLResponse{Data: map[string]any{"item": map[string]any{
				"__typename": "DesignItem",
				"history": map[string]any{
					"pagination": map[string]any{"cursor": "H2"},
					"results": []any{
						changeRow("ModelWrittenHistoryChange", "2024-02-20T14:00:00.500Z", "User Saved", ada),
						changeRow("PropertiesUpdatedHistoryChange", "2024-02-20T13:00:00Z", "Category: Beverage", bot),
					},
				},
			}}}
		case 2:
			if req.Variables["cursor"] != "H2" {
				t.Errorf("page 2 cursor = %v, want H2", req.Variables["cursor"])
			}
			return testutil.GraphQLResponse{Data: map[string]any{"item": map[string]any{
				"__typename": "DesignItem",
				"history": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results": []any{
						// Out of order on purpose: newer than page 1's last row.
						changeRow("ComponentPrimaryHistoryChange", "2024-02-01T09:00:00Z", "", ada),
						changeRow("DrawingItemWrittenHistoryChange", "2024-01-25T09:00:00Z", "sheet", ada),
						changeRow("ModelWrittenHistoryChange", "2024-01-20T11:00:00Z", "User Saved", ada),
					},
				},
			}}}
		default:
			t.Errorf("unexpected request #%d", n)
			return testutil.GraphQLResponse{Status: 500}
		}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemHistory(context.Background(), "tok", "h1", "item-1")
	if err != nil {
		t.Fatalf("GetItemHistory: %v", err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
	// The *Written rows are saves, not changes; the rest are newest first.
	wantTypes := []string{"PropertiesUpdatedHistoryChange", "ComponentPrimaryHistoryChange"}
	if len(got.Changes) != len(wantTypes) {
		t.Fatalf("len(Changes) = %d, want %d: %+v", len(got.Changes), len(wantTypes), got.Changes)
	}
	for i, want := range wantTypes {
		if got.Changes[i].Type != want {
			t.Errorf("Changes[%d].Type = %q, want %q", i, got.Changes[i].Type, want)
		}
	}
	if got.Changes[0].CreatedBy != "build-bot" || got.Changes[0].CreatedByID != "user-bot" {
		t.Errorf("Changes[0] author = %q/%q, want the userName fallback build-bot/user-bot", got.Changes[0].CreatedBy, got.Changes[0].CreatedByID)
	}
	if got.Changes[0].Comment != "Category: Beverage" {
		t.Errorf("Changes[0].Comment = %q", got.Changes[0].Comment)
	}
	// Every *Written row is a save, newest first, so the client can join them
	// to itemVersions by position.
	if len(got.Saves) != 3 {
		t.Fatalf("len(Saves) = %d, want 3: %+v", len(got.Saves), got.Saves)
	}
	if !got.Saves[0].CreatedOn.Equal(time.Date(2024, 2, 20, 14, 0, 0, 500_000_000, time.UTC)) {
		t.Errorf("Saves[0].CreatedOn = %v", got.Saves[0].CreatedOn)
	}
}

func TestGetItemHistory_MarkersDecorateTheSaveTheyStamp(t *testing.T) {
	// The live shape (2026-09-04): a milestone / release row carries exactly
	// the timestamp of the save it marks; the release row has no author.
	ada := map[string]any{"id": "user-ada", "firstName": "Ada", "lastName": "Lovelace"}
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: map[string]any{"item": map[string]any{
			"__typename": "DesignItem",
			"history": map[string]any{
				"pagination": map[string]any{"cursor": ""},
				"results": []any{
					changeRow("ModelWrittenHistoryChange", "2024-08-02T23:30:18.921Z", "User Saved", ada),
					changeRow("RevisionCreatedHistoryChange", "2024-03-25T18:37:44.362Z", "1", nil),
					changeRow("ModelWrittenHistoryChange", "2024-03-25T18:37:44.362Z", "User Saved", ada),
					changeRow("VersionCreatedHistoryChange", "2024-03-25T18:24:30.794Z", "Item Update", ada),
					changeRow("ModelWrittenHistoryChange", "2024-03-25T18:24:30.794Z", "User Saved", ada),
					// A user-typed milestone name is a release. Stamped a
					// second after its save: no exact match, so it goes to
					// the newest save at or before it.
					changeRow("VersionCreatedHistoryChange", "2023-02-04T21:15:04.000Z", "Rev B", ada),
					changeRow("ModelWrittenHistoryChange", "2023-02-04T21:15:03.393Z", "User Saved", ada),
					changeRow("ModelWrittenHistoryChange", "2023-02-04T21:10:39.513Z", "Item created", ada),
				},
			},
		}}}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemHistory(context.Background(), "tok", "h1", "item-1")
	if err != nil {
		t.Fatalf("GetItemHistory: %v", err)
	}
	if len(got.Saves) != 5 {
		t.Fatalf("len(Saves) = %d, want 5", len(got.Saves))
	}
	want := []HistorySave{
		{Milestone: "", Revision: ""},
		{Milestone: "", Revision: "1"},
		{Milestone: "Item Update", Revision: ""},
		{Milestone: "Rev B", Revision: "Rev B"},
		{Milestone: "", Revision: ""},
	}
	for i, w := range want {
		if got.Saves[i].Milestone != w.Milestone || got.Saves[i].Revision != w.Revision {
			t.Errorf("Saves[%d] = {%q %q}, want {%q %q}", i, got.Saves[i].Milestone, got.Saves[i].Revision, w.Milestone, w.Revision)
		}
	}
	// Marker rows live on the dots they decorate, never as change rings too —
	// that drew a milestone twice on the same track at the same instant.
	if len(got.Changes) != 0 {
		t.Errorf("Changes = %+v, want none: marker rows are not changes", got.Changes)
	}
}

func TestGetItemHistory_NonDesignHasNoHistory(t *testing.T) {
	// A BasicItem matches no fragment, so `history` is simply absent — empty
	// lists, not an error, and never nil.
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: map[string]any{"item": map[string]any{"__typename": "BasicItem"}}}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemHistory(context.Background(), "tok", "h1", "item-pdf")
	if err != nil {
		t.Fatalf("GetItemHistory: %v", err)
	}
	if got.Changes == nil || len(got.Changes) != 0 || got.Saves == nil || len(got.Saves) != 0 {
		t.Errorf("got %#v, want empty non-nil slices", got)
	}
}

func TestIsSaveChange(t *testing.T) {
	for typename, want := range map[string]bool{
		"ModelWrittenHistoryChange":       true,
		"DrawingItemWrittenHistoryChange": true,
		"BasicItemWrittenHistoryChange":   true,
		"PropertiesUpdatedHistoryChange":  false,
		"VersionCreatedHistoryChange":     false,
		"":                                false,
	} {
		if got := isSaveChange(typename); got != want {
			t.Errorf("isSaveChange(%q) = %v, want %v", typename, got, want)
		}
	}
}

func TestIsReleaseName(t *testing.T) {
	for name, want := range map[string]bool{
		"":             false,
		"Milestone V7": false,
		"Item Update":  false,
		"A":            true,
		"Rev B":        true,
		"Prototype":    true,
	} {
		if got := isReleaseName(name); got != want {
			t.Errorf("isReleaseName(%q) = %v, want %v", name, got, want)
		}
	}
}
