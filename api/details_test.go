package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/testutil"
)

func TestParseTime(t *testing.T) {
	mustParse := func(layout, s string) time.Time {
		t.Helper()
		v, err := time.Parse(layout, s)
		if err != nil {
			t.Fatalf("setup: parsing %q with %q: %v", s, layout, err)
		}
		return v
	}

	cases := []struct {
		name     string
		input    string
		wantZero bool
		want     time.Time
	}{
		{
			name:     "empty string returns zero time",
			input:    "",
			wantZero: true,
		},
		{
			name:  "RFC3339 UTC",
			input: "2024-01-15T10:30:45Z",
			want:  mustParse(time.RFC3339, "2024-01-15T10:30:45Z"),
		},
		{
			name:  "fractional Z fallback",
			input: "2024-01-15T10:30:45.123Z",
			want:  mustParse("2006-01-02T15:04:05.000Z", "2024-01-15T10:30:45.123Z"),
		},
		{
			name:  "RFC3339 with positive offset",
			input: "2024-01-15T10:30:45+02:00",
			want:  mustParse(time.RFC3339, "2024-01-15T10:30:45+02:00"),
		},
		{
			name:     "garbage returns zero time",
			input:    "garbage",
			wantZero: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTime(tc.input)
			if tc.wantZero {
				if !got.IsZero() {
					t.Errorf("parseTime(%q) = %v, want zero time", tc.input, got)
				}
				return
			}
			if !got.Equal(tc.want) {
				t.Errorf("parseTime(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestGetItemDetails_AllFields(t *testing.T) {
	raw, err := os.ReadFile("testdata/details_design.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshaling fixture: %v", err)
	}

	var sawHubID, sawItemID bool
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		// The per-author history tracks group by user id, so the query must ask
		// for it — a silently dropped `id` would fall back to name grouping and
		// merge two people who share a display name.
		if !strings.Contains(req.Query, "createdBy { id userName firstName lastName }") {
			t.Errorf("query does not select the author id and userName:\n%s", req.Query)
		}
		// A one-page history is one request: the first query carries the
		// first page and must not ask for a cursor it does not have.
		if _, has := req.Variables["cursor"]; has {
			t.Errorf("first request carries a cursor: %v", req.Variables["cursor"])
		}
		if v, ok := req.Variables["hubId"].(string); ok && v == "h1" {
			sawHubID = true
		} else {
			t.Errorf("Variables[hubId] = %v, want \"h1\"", req.Variables["hubId"])
		}
		if v, ok := req.Variables["itemId"].(string); ok && v == "item-1" {
			sawItemID = true
		} else {
			t.Errorf("Variables[itemId] = %v, want \"item-1\"", req.Variables["itemId"])
		}
		return testutil.GraphQLResponse{Data: data}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemDetails(context.Background(), "tok", "h1", "item-1")
	if err != nil {
		t.Fatalf("GetItemDetails: %v", err)
	}
	if !sawHubID || !sawItemID {
		t.Errorf("handler missed an assertion: hubId=%v itemId=%v", sawHubID, sawItemID)
	}

	if got.ID != "urn:item:abc" {
		t.Errorf("ID = %q, want %q", got.ID, "urn:item:abc")
	}
	if got.Name != "Widget A" {
		t.Errorf("Name = %q, want %q", got.Name, "Widget A")
	}
	if got.Typename != "DesignItem" {
		t.Errorf("Typename = %q, want %q", got.Typename, "DesignItem")
	}
	if got.Size != "12345678" {
		t.Errorf("Size = %q, want %q", got.Size, "12345678")
	}
	if got.MimeType != "application/vnd.autodesk.fusion360" {
		t.Errorf("MimeType = %q, want %q", got.MimeType, "application/vnd.autodesk.fusion360")
	}
	if got.ExtensionType != "Fusion360" {
		t.Errorf("ExtensionType = %q, want %q", got.ExtensionType, "Fusion360")
	}
	if got.FusionWebURL != "https://fusion.example/widget-a" {
		t.Errorf("FusionWebURL = %q, want %q", got.FusionWebURL, "https://fusion.example/widget-a")
	}
	if got.VersionNumber != 3 {
		t.Errorf("VersionNumber = %d, want 3", got.VersionNumber)
	}

	wantCreated := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	if !got.CreatedOn.Equal(wantCreated) {
		t.Errorf("CreatedOn = %v, want %v", got.CreatedOn, wantCreated)
	}
	wantModified := time.Date(2024, 2, 20, 14, 0, 0, 0, time.UTC)
	if !got.ModifiedOn.Equal(wantModified) {
		t.Errorf("ModifiedOn = %v, want %v", got.ModifiedOn, wantModified)
	}

	if got.CreatedBy != "Ada Lovelace" {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, "Ada Lovelace")
	}
	if got.ModifiedBy != "Grace Hopper" {
		t.Errorf("ModifiedBy = %q, want %q", got.ModifiedBy, "Grace Hopper")
	}
	if got.PartNumber != "WGT-001" {
		t.Errorf("PartNumber = %q, want %q", got.PartNumber, "WGT-001")
	}
	if got.PartDesc != "The widget A" {
		t.Errorf("PartDesc = %q, want %q", got.PartDesc, "The widget A")
	}
	if got.Material != "Aluminum 6061" {
		t.Errorf("Material = %q, want %q", got.Material, "Aluminum 6061")
	}
	if !got.IsMilestone {
		t.Errorf("IsMilestone = false, want true")
	}
	if got.RootComponentVersionID != "urn:cv:xyz" {
		t.Errorf("RootComponentVersionID = %q, want %q", got.RootComponentVersionID, "urn:cv:xyz")
	}

	if len(got.Versions) != 3 {
		t.Fatalf("len(Versions) = %d, want 3", len(got.Versions))
	}
	// Reversed: most-recent first.
	if got.Versions[0].Number != 3 {
		t.Errorf("Versions[0].Number = %d, want 3", got.Versions[0].Number)
	}
	if got.Versions[1].Number != 2 {
		t.Errorf("Versions[1].Number = %d, want 2", got.Versions[1].Number)
	}
	if got.Versions[2].Number != 1 {
		t.Errorf("Versions[2].Number = %d, want 1", got.Versions[2].Number)
	}
	if got.Versions[0].Comment != "third edit" {
		t.Errorf("Versions[0].Comment = %q, want %q", got.Versions[0].Comment, "third edit")
	}
	if got.Versions[0].CreatedBy != "Grace Hopper" {
		t.Errorf("Versions[0].CreatedBy = %q, want %q", got.Versions[0].CreatedBy, "Grace Hopper")
	}
	// The author id is what the History view's per-author tracks key on.
	if got.Versions[0].CreatedByID != "user-grace" {
		t.Errorf("Versions[0].CreatedByID = %q, want %q", got.Versions[0].CreatedByID, "user-grace")
	}
	if got.Versions[1].CreatedByID != "user-ada" {
		t.Errorf("Versions[1].CreatedByID = %q, want %q", got.Versions[1].CreatedByID, "user-ada")
	}

	// Per-version milestone + cvId. Reversed order: [0]=v3, [1]=v2, [2]=v1.
	// v3 has a non-milestone root component version.
	if got.Versions[0].RootComponentVersionID != "urn:cv:v3" {
		t.Errorf("Versions[0].RootComponentVersionID = %q, want %q", got.Versions[0].RootComponentVersionID, "urn:cv:v3")
	}
	if got.Versions[0].IsMilestone {
		t.Errorf("Versions[0].IsMilestone = true, want false")
	}
	// v2 is a milestone.
	if got.Versions[1].RootComponentVersionID != "urn:cv:v2" {
		t.Errorf("Versions[1].RootComponentVersionID = %q, want %q", got.Versions[1].RootComponentVersionID, "urn:cv:v2")
	}
	if !got.Versions[1].IsMilestone {
		t.Errorf("Versions[1].IsMilestone = false, want true")
	}
	// v1's rootComponentVersion is null (unmigrated / partial) — degrade to
	// empty id and not-a-milestone rather than erroring.
	if got.Versions[2].RootComponentVersionID != "" {
		t.Errorf("Versions[2].RootComponentVersionID = %q, want empty", got.Versions[2].RootComponentVersionID)
	}
	if got.Versions[2].IsMilestone {
		t.Errorf("Versions[2].IsMilestone = true, want false")
	}
}

func TestGetItemDetails_DrawingItem_NoComponentVersion(t *testing.T) {
	data := map[string]any{
		"item": map[string]any{
			"__typename":     "DrawingItem",
			"id":             "urn:item:dwg",
			"name":           "Sheet 1",
			"size":           "0",
			"mimeType":       "application/dwg",
			"extensionType":  "DrawingItem",
			"createdOn":      "2024-03-01T09:00:00Z",
			"createdBy":      map[string]any{"firstName": "X", "lastName": "Y"},
			"lastModifiedOn": "2024-03-02T09:00:00Z",
			"lastModifiedBy": map[string]any{"firstName": "X", "lastName": "Y"},
			"fusionWebUrl":   "https://example/dwg",
			"tipVersion":     map[string]any{"versionNumber": 1},
		},
		"itemVersions": map[string]any{"results": []any{
			// A version whose author carries no id — the id is optional in the
			// response and must degrade to empty, not to a placeholder.
			map[string]any{
				"versionNumber": 1,
				"name":          "sheet",
				"createdOn":     "2024-03-01T09:00:00Z",
				"createdBy":     map[string]any{"firstName": "X", "lastName": "Y"},
			},
		}},
	}

	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: data}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemDetails(context.Background(), "tok", "h1", "item-dwg")
	if err != nil {
		t.Fatalf("GetItemDetails: %v", err)
	}
	if got.Typename != "DrawingItem" {
		t.Errorf("Typename = %q, want %q", got.Typename, "DrawingItem")
	}
	if got.RootComponentVersionID != "" {
		t.Errorf("RootComponentVersionID = %q, want empty", got.RootComponentVersionID)
	}
	if got.PartNumber != "" {
		t.Errorf("PartNumber = %q, want empty", got.PartNumber)
	}
	if got.Material != "" {
		t.Errorf("Material = %q, want empty", got.Material)
	}
	if got.IsMilestone {
		t.Errorf("IsMilestone = true, want false")
	}
	if len(got.Versions) != 1 {
		t.Fatalf("len(Versions) = %d, want 1", len(got.Versions))
	}
	if got.Versions[0].CreatedByID != "" {
		t.Errorf("Versions[0].CreatedByID = %q, want empty", got.Versions[0].CreatedByID)
	}
	if got.Versions[0].CreatedBy != "X Y" {
		t.Errorf("Versions[0].CreatedBy = %q, want %q", got.Versions[0].CreatedBy, "X Y")
	}
}

func TestApiUser_FullName(t *testing.T) {
	cases := []struct {
		name     string
		first    string
		last     string
		userName string
		want     string
	}{
		{name: "both empty", first: "", last: "", want: ""},
		{name: "first only", first: "Ada", last: "", want: "Ada"},
		{name: "last only", first: "", last: "Lovelace", want: "Lovelace"},
		{name: "both present", first: "Ada", last: "Lovelace", want: "Ada Lovelace"},
		// A profile with no first or last name is still somebody: fall back to
		// the account name rather than a blank track.
		{name: "userName only", userName: "ada.l", want: "ada.l"},
		{name: "a real name beats the userName", first: "Ada", userName: "ada.l", want: "Ada"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := apiUser{First: tc.first, Last: tc.last, UserName: tc.userName}
			got := u.fullName()
			if got != tc.want {
				t.Errorf("apiUser{%q,%q,%q}.fullName() = %q, want %q", tc.first, tc.last, tc.userName, got, tc.want)
			}
		})
	}
}

// versionRow builds one itemVersions row for the paging tests.
func versionRow(n int, id string) map[string]any {
	return map[string]any{
		"versionNumber": n,
		"name":          "save " + id,
		"createdOn":     "2024-01-15T10:30:45Z",
		"createdBy":     map[string]any{"id": id, "firstName": "U", "lastName": id},
	}
}

func TestGetItemDetails_PaginatesVersions(t *testing.T) {
	// The item rides on the first page only; the follow-on page query must not
	// select it, and the merged list must be newest-first across the boundary.
	// Pages arrive as the live API sends them — newest first (the 2026-09-04
	// probe: 65 → 16, cursor, then the rest) — and the order is imposed by
	// version number regardless.
	var requests int32
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		n := atomic.AddInt32(&requests, 1)
		switch n {
		case 1:
			if _, has := req.Variables["cursor"]; has {
				t.Errorf("page 1 carries a cursor: %v", req.Variables["cursor"])
			}
			return testutil.GraphQLResponse{Data: map[string]any{
				"item": map[string]any{
					"__typename": "DesignItem",
					"id":         "urn:item:long",
					"name":       "Long History",
					"createdBy":  map[string]any{"id": "user-ada", "firstName": "Ada", "lastName": "Lovelace"},
					"tipVersion": map[string]any{"versionNumber": 3},
				},
				"itemVersions": map[string]any{
					"pagination": map[string]any{"cursor": "PAGE-2"},
					"results":    []any{versionRow(3, "c"), versionRow(2, "b")},
				},
			}}
		case 2:
			if req.Variables["cursor"] != "PAGE-2" {
				t.Errorf("page 2 cursor = %v, want PAGE-2", req.Variables["cursor"])
			}
			if strings.Contains(req.Query, "item(") {
				t.Errorf("page 2 re-selects the item:\n%s", req.Query)
			}
			if !strings.Contains(req.Query, "createdBy { id userName firstName lastName }") {
				t.Errorf("page 2 dropped the shared version row selection:\n%s", req.Query)
			}
			return testutil.GraphQLResponse{Data: map[string]any{
				"itemVersions": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results":    []any{versionRow(1, "a")},
				},
			}}
		default:
			t.Errorf("unexpected request #%d", n)
			return testutil.GraphQLResponse{Status: 500}
		}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemDetails(context.Background(), "tok", "h1", "item-long")
	if err != nil {
		t.Fatalf("GetItemDetails: %v", err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
	// The item survived the page walk.
	if got.ID != "urn:item:long" || got.Name != "Long History" || got.VersionNumber != 3 {
		t.Errorf("item fields lost across pages: %+v", got)
	}
	if got.CreatedBy != "Ada Lovelace" {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, "Ada Lovelace")
	}
	// Every page, newest first.
	if len(got.Versions) != 3 {
		t.Fatalf("len(Versions) = %d, want 3", len(got.Versions))
	}
	for i, want := range []int{3, 2, 1} {
		if got.Versions[i].Number != want {
			t.Errorf("Versions[%d].Number = %d, want %d", i, got.Versions[i].Number, want)
		}
	}
	if got.Versions[0].CreatedByID != "c" {
		t.Errorf("Versions[0].CreatedByID = %q, want %q", got.Versions[0].CreatedByID, "c")
	}
}

func TestGetItemDetails_UserNameFallback(t *testing.T) {
	data := map[string]any{
		"item": map[string]any{
			"__typename": "DesignItem",
			"id":         "urn:item:svc",
			"name":       "Bot Design",
			"createdBy":  map[string]any{"id": "user-bot", "userName": "build-bot"},
		},
		"itemVersions": map[string]any{
			"pagination": map[string]any{"cursor": ""},
			"results": []any{
				map[string]any{
					"versionNumber": 1,
					"createdOn":     "2024-01-15T10:30:45Z",
					"createdBy":     map[string]any{"id": "user-bot", "userName": "build-bot"},
				},
			},
		},
	}
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: data}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemDetails(context.Background(), "tok", "h1", "item-svc")
	if err != nil {
		t.Fatalf("GetItemDetails: %v", err)
	}
	if got.CreatedBy != "build-bot" {
		t.Errorf("CreatedBy = %q, want the userName fallback %q", got.CreatedBy, "build-bot")
	}
	if len(got.Versions) != 1 || got.Versions[0].CreatedBy != "build-bot" {
		t.Errorf("Versions = %+v, want one version by %q", got.Versions, "build-bot")
	}
}
