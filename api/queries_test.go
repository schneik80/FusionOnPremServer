package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/schneik80/fusionlocalserver/internal/testutil"
)

func TestNavItemFromTypename(t *testing.T) {
	cases := []struct {
		name            string
		typename        string
		wantKind        string
		wantIsContainer bool
	}{
		{name: "DesignItem", typename: "DesignItem", wantKind: "design", wantIsContainer: false},
		{name: "ConfiguredDesignItem", typename: "ConfiguredDesignItem", wantKind: "configured", wantIsContainer: false},
		{name: "DrawingItem", typename: "DrawingItem", wantKind: "drawing", wantIsContainer: false},
		{name: "Folder", typename: "Folder", wantKind: "folder", wantIsContainer: true},
		{name: "unknown typename", typename: "MysteryType", wantKind: "unknown", wantIsContainer: false},
		{name: "empty typename", typename: "", wantKind: "unknown", wantIsContainer: false},
	}

	const (
		id   = "urn:test:item:123"
		name = "Test Name"
	)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := navItemFromTypename(id, name, tc.typename)
			if got.ID != id {
				t.Errorf("ID = %q, want %q", got.ID, id)
			}
			if got.Name != name {
				t.Errorf("Name = %q, want %q", got.Name, name)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.IsContainer != tc.wantIsContainer {
				t.Errorf("IsContainer = %v, want %v", got.IsContainer, tc.wantIsContainer)
			}
		})
	}
}

func TestGetHubs_Pagination(t *testing.T) {
	var calls atomic.Int32
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		n := calls.Add(1)
		switch n {
		case 1:
			if _, ok := req.Variables["cursor"]; ok {
				t.Errorf("first call should not include cursor variable, got %v", req.Variables)
			}
			return testutil.GraphQLResponse{Data: map[string]any{
				"hubs": map[string]any{
					"pagination": map[string]any{"cursor": "PAGE2"},
					"results": []map[string]any{
						{
							"id":           "h1",
							"name":         "Hub1",
							"fusionWebUrl": "https://example/h1",
							"alternativeIdentifiers": map[string]any{
								"dataManagementAPIHubId": "ah1",
							},
						},
						{
							"id":           "h2",
							"name":         "Hub2",
							"fusionWebUrl": "https://example/h2",
							"alternativeIdentifiers": map[string]any{
								"dataManagementAPIHubId": "ah2",
							},
						},
					},
				},
			}}
		case 2:
			if got, _ := req.Variables["cursor"].(string); got != "PAGE2" {
				t.Errorf("second call cursor = %v, want \"PAGE2\"", req.Variables["cursor"])
			}
			return testutil.GraphQLResponse{Data: map[string]any{
				"hubs": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results": []map[string]any{
						{
							"id":           "h3",
							"name":         "Hub3",
							"fusionWebUrl": "https://example/h3",
							"alternativeIdentifiers": map[string]any{
								"dataManagementAPIHubId": "ah3",
							},
						},
					},
				},
			}}
		default:
			t.Errorf("unexpected extra call #%d", n)
			return testutil.GraphQLResponse{Data: map[string]any{
				"hubs": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results":    []map[string]any{},
				},
			}}
		}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetHubs(context.Background(), "tok")
	if err != nil {
		t.Fatalf("GetHubs: %v", err)
	}
	if got, want := calls.Load(), int32(2); got != want {
		t.Errorf("call count = %d, want %d", got, want)
	}

	wantIDs := []string{"h1", "h2", "h3"}
	if len(got) != len(wantIDs) {
		t.Fatalf("len = %d, want %d (items=%+v)", len(got), len(wantIDs), got)
	}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Errorf("hubs[%d].ID = %q, want %q", i, got[i].ID, want)
		}
		if got[i].Kind != "hub" {
			t.Errorf("hubs[%d].Kind = %q, want \"hub\"", i, got[i].Kind)
		}
		if !got[i].IsContainer {
			t.Errorf("hubs[%d].IsContainer = false, want true", i)
		}
	}
}

func TestGetProjects_FiltersInactive(t *testing.T) {
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		if got, _ := req.Variables["hubId"].(string); got != "h1" {
			t.Errorf("hubId = %v, want \"h1\"", req.Variables["hubId"])
		}
		return testutil.GraphQLResponse{Data: map[string]any{
			"hub": map[string]any{
				"projects": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results": []map[string]any{
						{
							"id":            "p-active-lower",
							"name":          "ActiveLower",
							"projectStatus": "active",
							"projectType":   "FUSION",
							"alternativeIdentifiers": map[string]any{
								"dataManagementAPIProjectId": "ap1",
							},
						},
						{
							"id":            "p-inactive-upper",
							"name":          "InactiveUpper",
							"projectStatus": "INACTIVE",
							"projectType":   "FUSION",
							"alternativeIdentifiers": map[string]any{
								"dataManagementAPIProjectId": "ap2",
							},
						},
						{
							"id":            "p-inactive-mixed",
							"name":          "InactiveMixed",
							"projectStatus": "Inactive",
							"projectType":   "FUSION",
							"alternativeIdentifiers": map[string]any{
								"dataManagementAPIProjectId": "ap3",
							},
						},
						{
							"id":            "p-active-cap",
							"name":          "ActiveCap",
							"projectStatus": "Active",
							"projectType":   "FUSION",
							"alternativeIdentifiers": map[string]any{
								"dataManagementAPIProjectId": "ap4",
							},
						},
					},
				},
			},
		}}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetProjects(context.Background(), "tok", "h1")
	if err != nil {
		t.Fatalf("GetProjects: %v", err)
	}
	wantNames := []string{"ActiveLower", "ActiveCap"}
	if len(got) != len(wantNames) {
		t.Fatalf("len = %d, want %d (items=%+v)", len(got), len(wantNames), got)
	}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Errorf("projects[%d].Name = %q, want %q", i, got[i].Name, want)
		}
		if got[i].Kind != "project" {
			t.Errorf("projects[%d].Kind = %q, want \"project\"", i, got[i].Kind)
		}
		if !got[i].IsContainer {
			t.Errorf("projects[%d].IsContainer = false, want true", i)
		}
	}
}

func TestGetItems_TypenameMapping(t *testing.T) {
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		if got, _ := req.Variables["hubId"].(string); got != "h1" {
			t.Errorf("hubId = %v, want \"h1\"", req.Variables["hubId"])
		}
		if got, _ := req.Variables["folderId"].(string); got != "f1" {
			t.Errorf("folderId = %v, want \"f1\"", req.Variables["folderId"])
		}
		return testutil.GraphQLResponse{Data: map[string]any{
			"itemsByFolder": map[string]any{
				"pagination": map[string]any{"cursor": ""},
				"results": []map[string]any{
					{"__typename": "DesignItem", "id": "i1", "name": "Design"},
					{"__typename": "ConfiguredDesignItem", "id": "i2", "name": "Configured"},
					{"__typename": "DrawingItem", "id": "i3", "name": "Drawing"},
					{"__typename": "Folder", "id": "i4", "name": "SubFolder"},
					{"__typename": "MysteryItem", "id": "i5", "name": "Mystery"},
				},
			},
		}}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItems(context.Background(), "tok", "h1", "f1")
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}

	want := []struct {
		id          string
		kind        string
		isContainer bool
	}{
		{"i1", "design", false},
		{"i2", "configured", false},
		{"i3", "drawing", false},
		{"i4", "folder", true},
		{"i5", "unknown", false},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (items=%+v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].ID != w.id {
			t.Errorf("items[%d].ID = %q, want %q", i, got[i].ID, w.id)
		}
		if got[i].Kind != w.kind {
			t.Errorf("items[%d].Kind = %q, want %q", i, got[i].Kind, w.kind)
		}
		if got[i].IsContainer != w.isContainer {
			t.Errorf("items[%d].IsContainer = %v, want %v", i, got[i].IsContainer, w.isContainer)
		}
	}
}

func TestGetItems_RequestsTipRootComponentVersion(t *testing.T) {
	// The async classifier needs tipRootComponentVersion.id per design
	// row to issue its occurrences probe without a second round-trip.
	// This test pins the inline fragment into the request so a future
	// query refactor can't silently strip it.
	var sawFragment bool
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		if strings.Contains(req.Query, "... on DesignItem") &&
			strings.Contains(req.Query, "tipRootComponentVersion") {
			sawFragment = true
		}
		return testutil.GraphQLResponse{Data: map[string]any{
			"itemsByFolder": map[string]any{
				"pagination": map[string]any{"cursor": ""},
				"results":    []map[string]any{},
			},
		}}
	})
	swapEndpoint(t, srv.URL)

	if _, err := GetItems(context.Background(), "tok", "h1", "f1"); err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if !sawFragment {
		t.Errorf("query did not include ... on DesignItem { tipRootComponentVersion { id } }")
	}
}

func TestGetItems_PopulatesComponentVersionID(t *testing.T) {
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: map[string]any{
			"itemsByFolder": map[string]any{
				"pagination": map[string]any{"cursor": ""},
				"results": []map[string]any{
					// Design with tipRoot present.
					{
						"__typename":              "DesignItem",
						"id":                      "i1",
						"name":                    "WithRoot",
						"tipRootComponentVersion": map[string]any{"id": "urn:cv:1"},
					},
					// Design without tipRoot (milestone-less, mid-translation, etc.)
					{
						"__typename": "DesignItem",
						"id":         "i2",
						"name":       "NoRoot",
					},
					// Drawing: inline fragment doesn't apply.
					{"__typename": "DrawingItem", "id": "i3", "name": "Drawing"},
				},
			},
		}}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItems(context.Background(), "tok", "h", "f")
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (%+v)", len(got), got)
	}
	if got[0].ComponentVersionID != "urn:cv:1" {
		t.Errorf("design[0].ComponentVersionID = %q, want urn:cv:1", got[0].ComponentVersionID)
	}
	if got[1].ComponentVersionID != "" {
		t.Errorf("design[1].ComponentVersionID = %q, want empty (no tipRoot)", got[1].ComponentVersionID)
	}
	if got[2].ComponentVersionID != "" {
		t.Errorf("drawing.ComponentVersionID = %q, want empty", got[2].ComponentVersionID)
	}
	// Designs start with empty Subtype — the async classifier fills
	// it in later. Drawings, by contrast, are sub-typed synchronously
	// from the filename extension. The test row "Drawing" has no
	// extension so it falls through to the default "dwg".
	if got[0].Subtype != "" || got[1].Subtype != "" {
		t.Errorf("designs[].Subtype = %q/%q before classification, want empty", got[0].Subtype, got[1].Subtype)
	}
	if got[2].Subtype != "dwg" {
		t.Errorf("drawing.Subtype = %q, want \"dwg\" (default)", got[2].Subtype)
	}
}

func TestKindFromExtension(t *testing.T) {
	cases := map[string]string{
		"Assembly.f3d":    "design",
		"Plan-A1.f2d":     "drawing",
		"BlankTitle.f2t":  "drawing",
		"PowerStage.fsch": "schematic",
		"MainBoard.fbrd":  "pcb",
		"RobotECAD.fprj":  "ecad",
		"Untitled":        "",       // no extension
		"Folder/name.f3d": "design", // last . wins, path separators ignored
		"weird/name":      "",
		"MixedCase.F3D":   "design", // case-insensitive
		"":                "",
	}
	for name, want := range cases {
		if got := kindFromExtension(name); got != want {
			t.Errorf("kindFromExtension(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDrawingSubtypeFromExtension(t *testing.T) {
	cases := map[string]string{
		"Plan-A1.f2d":  "dwg",
		"Standard.f2t": "template",
		"STANDARD.F2T": "template", // case-insensitive
		"Untitled":     "dwg",      // default
		"weird.txt":    "dwg",      // unknown extensions still get "dwg"
	}
	for name, want := range cases {
		if got := drawingSubtypeFromExtension(name); got != want {
			t.Errorf("drawingSubtypeFromExtension(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestNavItemFromResult_ExtensionRefinement(t *testing.T) {
	cases := []struct {
		name        string
		it          itemResult
		wantKind    string
		wantSubtype string
	}{
		{
			name:        "drawing → dwg via .f2d",
			it:          itemResult{Typename: "DrawingItem", ID: "1", Name: "Plan-A1.f2d"},
			wantKind:    "drawing",
			wantSubtype: "dwg",
		},
		{
			name:        "drawing → template via .f2t",
			it:          itemResult{Typename: "DrawingItem", ID: "2", Name: "BlankTitle.f2t"},
			wantKind:    "drawing",
			wantSubtype: "template",
		},
		{
			name:        "unknown typename + .fsch → schematic",
			it:          itemResult{Typename: "MysteryItem", ID: "3", Name: "Power.fsch"},
			wantKind:    "schematic",
			wantSubtype: "",
		},
		{
			name:        "unknown typename + .fbrd → pcb",
			it:          itemResult{Typename: "MysteryItem", ID: "4", Name: "MainBoard.fbrd"},
			wantKind:    "pcb",
			wantSubtype: "",
		},
		{
			name:        "unknown typename + .fprj → ecad",
			it:          itemResult{Typename: "MysteryItem", ID: "5", Name: "Robot.fprj"},
			wantKind:    "ecad",
			wantSubtype: "",
		},
		{
			name:        "design stays empty subtype (classifier fills later)",
			it:          itemResult{Typename: "DesignItem", ID: "6", Name: "Assembly.f3d"},
			wantKind:    "design",
			wantSubtype: "",
		},
		{
			name:        "typename wins when recognised even if extension differs",
			it:          itemResult{Typename: "ConfiguredDesignItem", ID: "7", Name: "Variant.f3d"},
			wantKind:    "configured",
			wantSubtype: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := navItemFromResult(tc.it)
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Subtype != tc.wantSubtype {
				t.Errorf("Subtype = %q, want %q", got.Subtype, tc.wantSubtype)
			}
		})
	}
}

func TestExtOf(t *testing.T) {
	cases := map[string]string{
		"file.txt":          ".txt",
		"file.tar.gz":       ".gz",
		"noext":             "",
		"":                  "",
		"a/b/c.f3d":         ".f3d",
		"weird/no.ext/name": "", // ext lookup stops at separator
		".hidden":           ".hidden",
	}
	for in, want := range cases {
		if got := extOf(in); got != want {
			t.Errorf("extOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAllPages_RefusesRunawayCursor(t *testing.T) {
	// An upstream that never stops handing back a cursor must produce an
	// error, not an infinite loop and not a silently truncated list.
	var requests int32
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		atomic.AddInt32(&requests, 1)
		return testutil.GraphQLResponse{Data: map[string]any{"cursor": "again"}}
	})
	swapEndpoint(t, srv.URL)

	const q = `query($cursor: String) { x }`
	_, err := allPages(context.Background(), "tok", q, q, nil,
		func(data json.RawMessage) (string, []int, error) {
			var r struct {
				Cursor string `json:"cursor"`
			}
			if err := json.Unmarshal(data, &r); err != nil {
				return "", nil, err
			}
			return r.Cursor, []int{1}, nil
		})
	if err == nil {
		t.Fatal("allPages returned no error for a cursor that never ends")
	}
	if !strings.Contains(err.Error(), "pages") {
		t.Errorf("error = %q, want it to name the page bound", err)
	}
	if int(requests) != maxPages {
		t.Errorf("requests = %d, want exactly maxPages (%d)", requests, maxPages)
	}
}
