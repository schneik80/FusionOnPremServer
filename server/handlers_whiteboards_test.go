package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/internal/testutil"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

const wbProject = "urn:adsk.wipprod:dm.folder:proj/wb"

// newWhiteboardTestServer fixes a roster with two writers and one viewer, so a
// revision result is never really an authorization result in disguise — and so
// the read-only case tests a genuine VIEWER rather than an unlisted user (who
// would fall through to group-derived contributor access).
func newWhiteboardTestServer(t *testing.T) *Server {
	t.Helper()
	roster := &fakeRoster{rows: []map[string]any{
		rosterRow("u-editor", "editor@x.io", "EDITOR"),
		rosterRow("u-manager", "manager@x.io", "MANAGER"),
		rosterRow("u-viewer", "viewer@x.io", "VIEWER"),
	}}
	srv := testutil.GraphQLServer(t, func(req testutil.GraphQLRequest) testutil.GraphQLResponse {
		return testutil.GraphQLResponse{Data: map[string]any{
			"project": map[string]any{
				"folderLevelProjectMembers": map[string]any{
					"pagination": map[string]any{"cursor": ""},
					"results":    roster.snapshot(),
				},
			},
		}}
	})
	t.Cleanup(api.SetGraphqlEndpointForTesting(srv.URL))

	authz := chat.NewAuthorizer()
	return &Server{
		logger:             quietLogger(),
		clientID:           "test-client",
		sessions:           NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger()),
		pending:            NewPendingStore(pendingTTL),
		hubs:               testHubStores(t, authz),
		chatAuthz:          authz,
		whiteboardOpLim:    chat.NewLimiter(1e6, 1e6),
		whiteboardDocLim:   chat.NewLimiter(1e6, 1e6),
		whiteboardPatchLim: chat.NewLimiter(1e6, 1e6),
	}
}

// docPut PUTs a document and returns the status plus the decoded body.
func docPut(t *testing.T, base, path string, cookie *http.Cookie, doc string) (int, WhiteboardDTO, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, base+path, bytes.NewReader([]byte(doc)))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var out WhiteboardDTO
	_ = json.Unmarshal(body, &out)
	return res.StatusCode, out, string(body)
}

// docGet GETs a document and returns the status, the body and the ETag.
func docGet(t *testing.T, base, path string, cookie *http.Cookie) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body), res.Header.Get("ETag")
}

// TestWhiteboardDoc_RevisionGuard is the endpoint half of the anti-clobber
// story: the GET advertises a revision, a save based on it succeeds, and a
// second save based on the same (now stale) revision is refused with a code the
// client can act on rather than silently winning.
func TestWhiteboardDoc_RevisionGuard(t *testing.T) {
	s := newWhiteboardTestServer(t)
	set := hubSet(t, s, testHubID)
	board, err := set.whiteboards.Create(wbProject, testHubID, "WB",
		whiteboards.Draft{Name: "Board"}, whiteboards.UserRef{ID: "u-editor"})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	alice := login(t, s, "u-editor", "Ada", "editor@x.io")
	bob := login(t, s, "u-manager", "Bob", "manager@x.io")

	docURL := "/api/whiteboards/doc?projectId=" + wbProject + "&boardId=" + board.ID

	// An unsaved board reads back "null" at revision 0 — an empty canvas.
	code, body, etag := docGet(t, ts.URL, docURL, alice)
	if code != http.StatusOK || body != "null" {
		t.Fatalf("unsaved doc = %d %q", code, body)
	}
	if etag != `W/"0"` {
		t.Fatalf("etag = %q, want W/\"0\"", etag)
	}

	// Alice saves against the revision she loaded.
	code, dto, raw := docPut(t, ts.URL, docURL+"&baseRev=0", alice, `{"store":{"a":1}}`)
	if code != http.StatusOK {
		t.Fatalf("first save = %d: %s", code, raw)
	}
	if dto.DocRev != 1 {
		t.Fatalf("docRev after save = %d, want 1", dto.DocRev)
	}

	// Bob still holds revision 0 and must be refused, not silently win.
	code, _, raw = docPut(t, ts.URL, docURL+"&baseRev=0", bob, `{"store":{"b":2}}`)
	if code != http.StatusConflict {
		t.Fatalf("stale save = %d, want 409: %s", code, raw)
	}
	var errBody struct{ Code string }
	if err := json.Unmarshal([]byte(raw), &errBody); err != nil || errBody.Code != codeWhiteboardStale {
		t.Fatalf("409 body = %s, want code %q", raw, codeWhiteboardStale)
	}

	// Alice's document is intact.
	code, body, etag = docGet(t, ts.URL, docURL, bob)
	if code != http.StatusOK || body != `{"store":{"a":1}}` || etag != `W/"1"` {
		t.Fatalf("after refused save: %d %q etag=%q", code, body, etag)
	}

	// Bob reloads, then saves against the current revision.
	if code, _, raw = docPut(t, ts.URL, docURL+"&baseRev=1", bob, `{"store":{"b":2}}`); code != http.StatusOK {
		t.Fatalf("save at current revision = %d: %s", code, raw)
	}
}

// TestWhiteboardDoc_ForceOverwrites: the user was shown the conflict and chose
// to overwrite. It must take effect, and it must still advance the revision.
func TestWhiteboardDoc_ForceOverwrites(t *testing.T) {
	s := newWhiteboardTestServer(t)
	set := hubSet(t, s, testHubID)
	board, err := set.whiteboards.Create(wbProject, testHubID, "WB",
		whiteboards.Draft{Name: "Board"}, whiteboards.UserRef{ID: "u-editor"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	alice := login(t, s, "u-editor", "Ada", "editor@x.io")
	bob := login(t, s, "u-manager", "Bob", "manager@x.io")
	docURL := "/api/whiteboards/doc?projectId=" + wbProject + "&boardId=" + board.ID

	if code, _, raw := docPut(t, ts.URL, docURL+"&baseRev=0", alice, `{"store":{"a":1}}`); code != http.StatusOK {
		t.Fatalf("seed save = %d: %s", code, raw)
	}
	code, dto, raw := docPut(t, ts.URL, docURL+"&baseRev=0&force=1", bob, `{"store":{"b":2}}`)
	if code != http.StatusOK {
		t.Fatalf("forced save = %d: %s", code, raw)
	}
	if dto.DocRev != 2 {
		t.Fatalf("forced save docRev = %d, want 2", dto.DocRev)
	}
	if _, body, _ := docGet(t, ts.URL, docURL, alice); body != `{"store":{"b":2}}` {
		t.Fatalf("force did not take: %s", body)
	}
}

// TestWhiteboardDoc_BaseRevIsRequired: a save with no revision must be
// rejected rather than treated as unconditional — an unguarded save is exactly
// the bug this endpoint now prevents.
func TestWhiteboardDoc_BaseRevIsRequired(t *testing.T) {
	s := newWhiteboardTestServer(t)
	set := hubSet(t, s, testHubID)
	board, err := set.whiteboards.Create(wbProject, testHubID, "WB",
		whiteboards.Draft{Name: "Board"}, whiteboards.UserRef{ID: "u-editor"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	alice := login(t, s, "u-editor", "Ada", "editor@x.io")
	docURL := "/api/whiteboards/doc?projectId=" + wbProject + "&boardId=" + board.ID

	for _, q := range []string{"", "&baseRev=", "&baseRev=abc", "&baseRev=-1"} {
		if code, _, _ := docPut(t, ts.URL, docURL+q, alice, `{"store":{}}`); code != http.StatusBadRequest {
			t.Errorf("baseRev %q = %d, want 400", q, code)
		}
	}
}

// TestWhiteboardDoc_ReadOnlyMemberCannotSave: the revision guard is not an
// authorization substitute — a viewer is still refused before it is reached.
func TestWhiteboardDoc_ReadOnlyMemberCannotSave(t *testing.T) {
	s := newWhiteboardTestServer(t)
	set := hubSet(t, s, testHubID)
	board, err := set.whiteboards.Create(wbProject, testHubID, "WB",
		whiteboards.Draft{Name: "Board"}, whiteboards.UserRef{ID: "u-editor"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	viewer := login(t, s, "u-viewer", "Vic", "viewer@x.io")
	docURL := "/api/whiteboards/doc?projectId=" + wbProject + "&boardId=" + board.ID

	if code, _, _ := docGet(t, ts.URL, docURL, viewer); code != http.StatusOK {
		t.Fatalf("viewer should be able to read the board")
	}
	if code, _, _ := docPut(t, ts.URL, docURL+"&baseRev=0", viewer, `{"store":{}}`); code != http.StatusForbidden {
		t.Fatalf("viewer save = %d, want 403", code)
	}
}
