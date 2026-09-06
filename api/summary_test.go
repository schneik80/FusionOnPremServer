package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetItemSummary_SelectsNoVersions pins the point of the query: one root
// field, no itemVersions page, and the fields a card renders mapped through.
func TestGetItemSummary_SelectsNoVersions(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"item":{"__typename":"DesignItem","id":"i1","name":"Bracket","lastModifiedOn":"2026-09-01T10:00:00Z","lastModifiedBy":{"firstName":"Ada","lastName":"L"},"tipVersion":{"versionNumber":7},"tipRootComponentVersion":{"id":"cv7","partNumber":"PN-1","materialName":"Steel","isMilestone":true}}}}`)
	}))
	t.Cleanup(srv.Close)
	swapEndpoint(t, srv.URL)

	d, err := GetItemSummary(context.Background(), "tok", "h", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "itemVersions") || strings.Contains(body, "createdBy") || strings.Contains(body, "fusionWebUrl") {
		t.Errorf("summary query must not select versions/creator/web url:\n%s", body)
	}
	if !strings.Contains(body, "GetItemSummary") {
		t.Errorf("operation must be named for the cost registry:\n%s", body)
	}
	if d.Name != "Bracket" || d.Typename != "DesignItem" || d.VersionNumber != 7 || d.RootComponentVersionID != "cv7" || d.PartNumber != "PN-1" || d.Material != "Steel" || !d.IsMilestone || d.ModifiedBy != "Ada L" {
		t.Errorf("summary = %+v", d)
	}
	if d.Versions == nil || len(d.Versions) != 0 {
		t.Errorf("versions must be empty, not nil: %v", d.Versions)
	}
}
