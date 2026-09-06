package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/schneik80/fusionlocalserver/api"
)

func permsRequest(t *testing.T, folders int) *http.Request {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("/api/permissions/path?hubId=h&projectId=p")
	for i := 0; i < folders; i++ {
		sb.WriteString("&folderId=f")
		sb.WriteString(string(rune('a' + i%26)))
	}
	req := httptest.NewRequest(http.MethodGet, sb.String(), nil)
	return req.WithContext(context.WithValue(req.Context(), tokenCtxKey, "tok"))
}

// TestPermissionsPath_DepthCap: the repeated folderId list is bounded — each
// layer is two paginated APS calls, so an unbounded list was a quota
// amplifier.
func TestPermissionsPath_DepthCap(t *testing.T) {
	s := &Server{logger: quietLogger()}
	rec := httptest.NewRecorder()
	s.handlePermissionsPath(rec, permsRequest(t, maxPermissionsPathDepth+1))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body errorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Code != "path_too_deep" {
		t.Errorf("code = %q", body.Code)
	}
}

// TestPermissionsPath_RateLimitFailsWhole: a 429 on any layer fails the
// request as 429 (with Retry-After) instead of answering with silently
// empty layers.
func TestPermissionsPath_RateLimitFailsWhole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"errors":[{"message":"Query point value per minute quota exceeded with point value 60 and remaining quota 1."}]}`)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(api.SetGraphqlEndpointForTesting(srv.URL))

	s := &Server{logger: quietLogger()}
	rec := httptest.NewRecorder()
	s.handlePermissionsPath(rec, permsRequest(t, 2))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != "7" {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

// TestPermissionsPath_OtherErrorFlagsLayer: a non-quota upstream failure on
// one layer empties AND flags that layer; the rest of the path is served.
func TestPermissionsPath_OtherErrorFlagsLayer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "query FM") || strings.Contains(string(body), "query FG") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"errors":[{"message":"Forbidden","extensions":{"errorType":"FORBIDDEN"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"project":{"groups":{"results":[]},"folderLevelProjectMembers":{"results":[]},"members":{"results":[]}}}}`)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(api.SetGraphqlEndpointForTesting(srv.URL))

	s := &Server{logger: quietLogger()}
	rec := httptest.NewRecorder()
	s.handlePermissionsPath(rec, permsRequest(t, 1))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var layers []PermLayerDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &layers); err != nil {
		t.Fatal(err)
	}
	if len(layers) != 2 || !layers[1].Error || layers[1].Members == nil || layers[1].Groups == nil {
		t.Errorf("layers = %+v", layers)
	}
}
