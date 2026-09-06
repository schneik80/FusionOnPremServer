package api

import (
	"context"
	"strings"
	"testing"

	"github.com/schneik80/fusionlocalserver/internal/testutil"
)

func TestClassifyAssembly_Assembly(t *testing.T) {
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		if !strings.Contains(req.Query, "occurrences(pagination") {
			t.Errorf("query missing occurrences field: %q", req.Query)
		}
		if !strings.Contains(req.Query, "limit: 1") {
			t.Errorf("query should request limit:1, got: %q", req.Query)
		}
		if got, _ := req.Variables["cv"].(string); got != "urn:cv:asm" {
			t.Errorf("cv variable = %v, want urn:cv:asm", req.Variables["cv"])
		}
		return testutil.GraphQLResponse{Data: map[string]any{
			"componentVersion": map[string]any{
				"occurrences": map[string]any{
					"results": []map[string]any{
						{"id": "occ-1"},
					},
				},
			},
		}}
	})
	swapEndpoint(t, srv.URL)

	got, err := ClassifyAssembly(context.Background(), "tok", "urn:cv:asm")
	if err != nil {
		t.Fatalf("ClassifyAssembly: %v", err)
	}
	if !got {
		t.Errorf("expected isAssembly=true for non-empty occurrences, got false")
	}
}

func TestClassifyAssembly_Part(t *testing.T) {
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: map[string]any{
			"componentVersion": map[string]any{
				"occurrences": map[string]any{
					"results": []map[string]any{}, // empty = part
				},
			},
		}}
	})
	swapEndpoint(t, srv.URL)

	got, err := ClassifyAssembly(context.Background(), "tok", "urn:cv:part")
	if err != nil {
		t.Fatalf("ClassifyAssembly: %v", err)
	}
	if got {
		t.Errorf("expected isAssembly=false for empty occurrences, got true")
	}
}

func TestClassifyAssembly_EmptyComponentVersionID(t *testing.T) {
	// No GraphQL server registered — if we issued a request, it'd hit
	// the live endpoint (or fail DNS). The empty-id check must short-circuit.
	_, err := ClassifyAssembly(context.Background(), "tok", "")
	if err == nil {
		t.Errorf("expected error for empty componentVersionID, got nil")
	}
	if !strings.Contains(err.Error(), "empty componentVersionID") {
		t.Errorf("error = %q, want it to mention empty componentVersionID", err.Error())
	}
}

func TestClassifyAssembly_GraphQLError(t *testing.T) {
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{
			Errors: []string{"Requested resource not found."},
		}
	})
	swapEndpoint(t, srv.URL)

	_, err := ClassifyAssembly(context.Background(), "tok", "urn:cv:missing")
	if err == nil {
		t.Fatalf("expected error from GraphQL errors[], got nil")
	}
	if !strings.Contains(err.Error(), "Requested resource not found") {
		t.Errorf("error = %q, expected GraphQL message verbatim", err.Error())
	}
}

// The package-level classify semaphore is gone: per-row probes are bounded by
// the budget scheduler's P1 lane (internal/apsbudget), whose cancellation and
// release semantics are covered there.
