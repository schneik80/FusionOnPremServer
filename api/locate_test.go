package api

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/schneik80/fusionlocalserver/internal/testutil"
)

// nestedChain builds the API's nested parentFolder shape leaf-first from a
// root→leaf list of (id, name) pairs.
func nestedChain(rootToLeaf [][2]string) map[string]any {
	var cur map[string]any
	for _, f := range rootToLeaf {
		cur = map[string]any{"id": f[0], "name": f[1], "parentFolder": cur}
	}
	return cur
}

// TestGetItemLocation_NestedChainIsOneCall: a three-deep ancestry comes back
// in the single LocateItem query (the nested parentFolder selection) and is
// returned root→leaf. One round trip, where the walk used to pay one per
// level.
func TestGetItemLocation_NestedChainIsOneCall(t *testing.T) {
	var calls atomic.Int32
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		if n := calls.Add(1); n != 1 {
			t.Fatalf("unexpected extra call: %d", n)
		}
		if !strings.Contains(req.Query, "LocateItem") || strings.Count(req.Query, "parentFolder") < locateDepth {
			t.Errorf("expected a LocateItem query nesting parentFolder %d deep, got %q", locateDepth, req.Query)
		}
		return testutil.GraphQLResponse{Data: map[string]any{
			"item": map[string]any{
				"project": map[string]any{
					"id":   "P1",
					"name": "RobotLab",
					"hub":  map[string]any{"id": "H1"},
					"alternativeIdentifiers": map[string]any{
						"dataManagementAPIProjectId": "a.proj1",
					},
				},
				"parentFolder": nestedChain([][2]string{{"F-root", "Top"}, {"F-mid", "Engineering"}, {"F-leaf", "Subassemblies"}}),
			},
		}}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemLocation(context.Background(), "tok", "H1", "I1")
	if err != nil {
		t.Fatalf("GetItemLocation: %v", err)
	}
	if got.HubID != "H1" || got.ProjectID != "P1" || got.ProjectName != "RobotLab" || got.ProjectAltID != "a.proj1" {
		t.Errorf("project metadata wrong: %+v", got)
	}
	want := []FolderRef{
		{ID: "F-root", Name: "Top"},
		{ID: "F-mid", Name: "Engineering"},
		{ID: "F-leaf", Name: "Subassemblies"},
	}
	if len(got.FolderPath) != len(want) {
		t.Fatalf("FolderPath len = %d, want %d (path=%+v)", len(got.FolderPath), len(want), got.FolderPath)
	}
	for i, w := range want {
		if got.FolderPath[i] != w {
			t.Errorf("FolderPath[%d] = %+v, want %+v", i, got.FolderPath[i], w)
		}
	}
}

// TestGetItemLocation_WalksPastNestedDepth: a tree deeper than the nested
// selection continues with the one-level-per-call walk from the deepest
// folder the chain reached, and still comes back root→leaf.
func TestGetItemLocation_WalksPastNestedDepth(t *testing.T) {
	var chain [][2]string
	for i := 0; i < locateDepth; i++ {
		chain = append(chain, [2]string{"F" + string(rune('a'+i)), "L" + string(rune('a'+i))})
	}
	// chain[0] is the deepest folder the nested selection reaches (its
	// parent, F-up, and F-up's parent, F-top, come from the walk).
	var calls atomic.Int32
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		switch n := calls.Add(1); n {
		case 1:
			return testutil.GraphQLResponse{Data: map[string]any{
				"item": map[string]any{
					"project":      map[string]any{"id": "P1", "name": "X", "hub": map[string]any{"id": "H1"}},
					"parentFolder": nestedChain(chain),
				},
			}}
		case 2:
			if got, _ := req.Variables["folderId"].(string); got != chain[0][0] {
				t.Errorf("call 2: folderId = %v, want %s", req.Variables["folderId"], chain[0][0])
			}
			return testutil.GraphQLResponse{Data: map[string]any{
				"folderByHubId": map[string]any{"parentFolder": map[string]any{"id": "F-up", "name": "Up"}},
			}}
		case 3:
			return testutil.GraphQLResponse{Data: map[string]any{
				"folderByHubId": map[string]any{"parentFolder": map[string]any{"id": "F-top", "name": "Top"}},
			}}
		case 4:
			return testutil.GraphQLResponse{Data: map[string]any{
				"folderByHubId": map[string]any{"parentFolder": nil},
			}}
		default:
			t.Fatalf("unexpected extra call: %d", n)
			return testutil.GraphQLResponse{}
		}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemLocation(context.Background(), "tok", "H1", "I1")
	if err != nil {
		t.Fatalf("GetItemLocation: %v", err)
	}
	if len(got.FolderPath) != locateDepth+2 {
		t.Fatalf("FolderPath len = %d, want %d: %+v", len(got.FolderPath), locateDepth+2, got.FolderPath)
	}
	if got.FolderPath[0].ID != "F-top" || got.FolderPath[1].ID != "F-up" || got.FolderPath[2].ID != chain[0][0] || got.FolderPath[len(got.FolderPath)-1].ID != chain[len(chain)-1][0] {
		t.Errorf("order wrong: %+v", got.FolderPath)
	}
}

// TestGetItemLocation_ProjectRootEmptyFolderPath confirms the empty-path
// case: an item that lives in the project root returns ItemLocation
// with an empty FolderPath (no walk queries fire).
func TestGetItemLocation_ProjectRootEmptyFolderPath(t *testing.T) {
	var calls atomic.Int32
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		calls.Add(1)
		return testutil.GraphQLResponse{Data: map[string]any{
			"item": map[string]any{
				"project": map[string]any{
					"id":                     "P1",
					"name":                   "Bare",
					"hub":                    map[string]any{"id": "H1"},
					"alternativeIdentifiers": map[string]any{"dataManagementAPIProjectId": "a.bare"},
				},
				"parentFolder": nil,
			},
		}}
	})
	swapEndpoint(t, srv.URL)

	got, err := GetItemLocation(context.Background(), "tok", "H1", "I1")
	if err != nil {
		t.Fatalf("GetItemLocation: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("call count = %d, want 1 (no folder walk for root item)", calls.Load())
	}
	if len(got.FolderPath) != 0 {
		t.Errorf("FolderPath = %+v, want empty", got.FolderPath)
	}
}
