package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdminStatus(t *testing.T) {
	s := newTaskTestServer(t)
	s.startedAt = time.Now().Add(-90 * time.Second)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	var out AdminStatusDTO
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/status", editor, nil, &out); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if out.UptimeSeconds < 89 || out.StartedAt == "" || out.GoVersion == "" {
		t.Errorf("status DTO = %+v", out)
	}
	// Unauthenticated → 401.
	if code := chatDo(t, ts.URL, http.MethodGet, "/api/admin/status", nil, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d, want 401", code)
	}
}

func TestAdminLogTail(t *testing.T) {
	s := newTaskTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	// No config-dir log in tests → clean 404, not a 500.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/admin/log?tailBytes=1024", nil)
	req.AddCookie(editor)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusOK {
		t.Fatalf("log tail status = %d", res.StatusCode)
	}
	if res.StatusCode == http.StatusOK &&
		!strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("content type = %q", res.Header.Get("Content-Type"))
	}

	// Invalid tailBytes rejected.
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/admin/log?tailBytes=-5", nil)
	req2.AddCookie(editor)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode == http.StatusOK {
		t.Error("negative tailBytes accepted")
	}
}
