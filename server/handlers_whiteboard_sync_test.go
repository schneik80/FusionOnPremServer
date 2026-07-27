package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// patchPost submits one patch and returns the status and decoded response.
func patchPost(t *testing.T, base, boardID string, cookie *http.Cookie, body string) (int, WhiteboardPatchDTO, string) {
	t.Helper()
	url := base + "/api/whiteboards/patch?projectId=" + wbProject + "&boardId=" + boardID
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out WhiteboardPatchDTO
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out, string(raw)
}

// TestWhiteboardPatch_AppliesAndBroadcasts is live editing in one test: one
// client's edit reaches the board's other viewer, carrying the revision and the
// sender's id (which the sender uses to ignore its own echo).
func TestWhiteboardPatch_AppliesAndBroadcasts(t *testing.T) {
	s := newWhiteboardTestServer(t)
	board := seedBoard(t, s)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	watcher := login(t, s, "u-manager", "Bob", "manager@x.io")
	editor := login(t, s, "u-editor", "Ada", "editor@x.io")

	ch, closeStream := openBoardSSE(t, ts.URL, boardEventsURL(board.ID), watcher)
	defer closeStream()
	waitEvent(t, ch, "peers", func(e sseEvent) bool { return strings.Contains(e.data, `"peers"`) })

	code, out, raw := patchPost(t, ts.URL, board.ID, editor,
		`{"clientId":"c1","seq":1,"baseRev":0,"put":{"shape:a":{"id":"shape:a","x":1}}}`)
	if code != http.StatusOK {
		t.Fatalf("patch = %d: %s", code, raw)
	}
	if out.Rev != 1 {
		t.Fatalf("rev = %d, want 1", out.Rev)
	}

	frame := waitEvent(t, ch, "patch", func(e sseEvent) bool { return strings.Contains(e.data, `"patch"`) })
	var ev struct {
		Data WhiteboardPatchEventDTO `json:"data"`
	}
	if err := json.Unmarshal([]byte(frame.data), &ev); err != nil {
		t.Fatalf("patch frame %s: %v", frame.data, err)
	}
	if ev.Data.Rev != 1 || ev.Data.ClientID != "c1" {
		t.Fatalf("frame = %+v, want rev 1 from c1", ev.Data)
	}
	if _, ok := ev.Data.Put["shape:a"]; !ok {
		t.Fatalf("frame did not carry the record: %+v", ev.Data)
	}
	// Durable: a client that blinks replays it rather than re-reading the doc.
	if frame.id == "" {
		t.Error("patch frame carried no id")
	}

	// The document endpoint now serves the LIVE state, not the last write.
	_, body, etag := docGet(t, ts.URL, "/api/whiteboards/doc?projectId="+wbProject+"&boardId="+board.ID, watcher)
	if !strings.Contains(body, "shape:a") {
		t.Errorf("doc GET did not reflect the live room: %s", body)
	}
	if etag != `W/"1"` {
		t.Errorf("etag = %q, want the live revision", etag)
	}
}

// TestWhiteboardPatch_ReadOnlyMemberRefused: the client gate is an affordance,
// this is the boundary.
func TestWhiteboardPatch_ReadOnlyMemberRefused(t *testing.T) {
	s := newWhiteboardTestServer(t)
	board := seedBoard(t, s)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	viewer := login(t, s, "u-viewer", "Vic", "viewer@x.io")

	code, _, _ := patchPost(t, ts.URL, board.ID, viewer,
		`{"clientId":"c1","seq":1,"baseRev":0,"put":{"shape:a":{}}}`)
	if code != http.StatusForbidden {
		t.Fatalf("viewer patch = %d, want 403", code)
	}
}

// TestWhiteboardPatch_RetryIsIdempotent: a lost response makes the client
// retry. Applying twice would undo whatever it changed in between.
func TestWhiteboardPatch_RetryIsIdempotent(t *testing.T) {
	s := newWhiteboardTestServer(t)
	board := seedBoard(t, s)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ada", "editor@x.io")

	first := `{"clientId":"c1","seq":1,"baseRev":0,"put":{"shape:a":{"v":1}}}`
	if code, out, raw := patchPost(t, ts.URL, board.ID, editor, first); code != http.StatusOK || out.Rev != 1 {
		t.Fatalf("first = %d rev %d: %s", code, out.Rev, raw)
	}
	// A newer edit lands from the same client.
	if code, _, _ := patchPost(t, ts.URL, board.ID, editor,
		`{"clientId":"c1","seq":2,"baseRev":1,"put":{"shape:a":{"v":2}}}`); code != http.StatusOK {
		t.Fatal("second patch failed")
	}
	// Now the retry of seq 1 arrives.
	code, out, raw := patchPost(t, ts.URL, board.ID, editor, first)
	if code != http.StatusOK || out.Rev != 2 {
		t.Fatalf("retry = %d rev %d, want 200 at rev 2: %s", code, out.Rev, raw)
	}
	_, body, _ := docGet(t, ts.URL, "/api/whiteboards/doc?projectId="+wbProject+"&boardId="+board.ID, editor)
	if !strings.Contains(body, `"v":2`) {
		t.Errorf("a retried patch clobbered the newer value: %s", body)
	}
}

// TestWhiteboardPatch_RejectedRecordsComeBack: a shape someone else deleted
// cannot be revived by a client that hadn't heard yet, and it is told which
// records were refused so it can drop them locally.
func TestWhiteboardPatch_RejectedRecordsComeBack(t *testing.T) {
	s := newWhiteboardTestServer(t)
	board := seedBoard(t, s)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	a := login(t, s, "u-editor", "Ada", "editor@x.io")
	b := login(t, s, "u-manager", "Bob", "manager@x.io")

	patchPost(t, ts.URL, board.ID, a, `{"clientId":"a","seq":1,"baseRev":0,"put":{"shape:x":{"v":1}}}`)
	patchPost(t, ts.URL, board.ID, a, `{"clientId":"a","seq":2,"baseRev":1,"remove":["shape:x"]}`)

	// Bob is still at revision 1 and pushes his drag of the deleted shape.
	code, out, raw := patchPost(t, ts.URL, board.ID, b,
		`{"clientId":"b","seq":1,"baseRev":1,"put":{"shape:x":{"v":2}}}`)
	if code != http.StatusOK {
		t.Fatalf("patch = %d: %s", code, raw)
	}
	if len(out.Rejected) != 1 || out.Rejected[0] != "shape:x" {
		t.Fatalf("rejected = %v, want shape:x", out.Rejected)
	}
	_, body, _ := docGet(t, ts.URL, "/api/whiteboards/doc?projectId="+wbProject+"&boardId="+board.ID, a)
	if strings.Contains(body, "shape:x") {
		t.Errorf("deleted shape was resurrected: %s", body)
	}
}

// TestWhiteboardPatch_RefusesNonDocumentRecordsAndFutureRevisions covers the
// two rejections a client must handle differently: a bad request it should
// never have sent, and a resync it must act on.
func TestWhiteboardPatch_RefusesNonDocumentRecordsAndFutureRevisions(t *testing.T) {
	s := newWhiteboardTestServer(t)
	board := seedBoard(t, s)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ada", "editor@x.io")

	// Session state must never reach the shared document.
	if code, _, _ := patchPost(t, ts.URL, board.ID, editor,
		`{"clientId":"c1","seq":1,"baseRev":0,"put":{"camera:page":{"x":1}}}`); code != http.StatusBadRequest {
		t.Errorf("camera record = %d, want 400", code)
	}
	// A revision the board never had means the client's view is unusable.
	code, _, raw := patchPost(t, ts.URL, board.ID, editor,
		`{"clientId":"c1","seq":1,"baseRev":99,"put":{"shape:a":{}}}`)
	if code != http.StatusConflict {
		t.Fatalf("future baseRev = %d, want 409: %s", code, raw)
	}
	var errBody struct{ Code string }
	if err := json.Unmarshal([]byte(raw), &errBody); err != nil || errBody.Code != codeWhiteboardResync {
		t.Fatalf("409 body = %s, want code %q", raw, codeWhiteboardResync)
	}
}

// TestWhiteboardPatch_FullDocumentPutLandsInTheRoom: a whole-document write
// while people are editing must go through the room, or the next patch would
// be applied to the old document and quietly write it back over this one.
func TestWhiteboardPatch_FullDocumentPutLandsInTheRoom(t *testing.T) {
	s := newWhiteboardTestServer(t)
	board := seedBoard(t, s)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ada", "editor@x.io")

	docURL := "/api/whiteboards/doc?projectId=" + wbProject + "&boardId=" + board.ID
	patchPost(t, ts.URL, board.ID, editor, `{"clientId":"c1","seq":1,"baseRev":0,"put":{"shape:a":{"v":1}}}`)

	if code, _, raw := docPut(t, ts.URL, docURL+"&baseRev=1&force=1", editor,
		`{"store":{"shape:z":{"v":9}}}`); code != http.StatusOK {
		t.Fatalf("document put = %d: %s", code, raw)
	}
	_, body, _ := docGet(t, ts.URL, docURL, editor)
	if strings.Contains(body, "shape:a") || !strings.Contains(body, "shape:z") {
		t.Fatalf("the room kept serving the pre-PUT document: %s", body)
	}
	// And the room's revision moved, so peers know to re-read.
	if code, out, _ := patchPost(t, ts.URL, board.ID, editor,
		`{"clientId":"c1","seq":2,"baseRev":2,"put":{"shape:q":{}}}`); code != http.StatusOK || out.Rev != 3 {
		t.Fatalf("patch after replace = %d rev %d, want 200 rev 3", code, out.Rev)
	}
}
