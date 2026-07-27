package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// openBoardSSE connects to a board's awareness stream and pumps parsed frames
// to a channel. Deliberately a separate reader from openSSE: that one is bound
// to chat's URL, and a shared helper would couple the two features' tests.
func openBoardSSE(t *testing.T, base, path string, cookie *http.Cookie) (<-chan sseEvent, func()) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("board events status = %d, want 200", resp.StatusCode)
	}
	ch := make(chan sseEvent, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if ev.data != "" || ev.event != "" {
					ch <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "id: "):
				ev.id = line[len("id: "):]
			case strings.HasPrefix(line, "event: "):
				ev.event = line[len("event: "):]
			case strings.HasPrefix(line, "data: "):
				ev.data = line[len("data: "):]
			}
		}
	}()
	t.Cleanup(func() { resp.Body.Close() })
	return ch, func() { resp.Body.Close() }
}

func boardEventsURL(boardID string) string {
	return "/api/whiteboards/events?projectId=" + wbProject + "&boardId=" + boardID
}

func seedBoard(t *testing.T, s *Server) whiteboards.Board {
	t.Helper()
	set := hubSet(t, s, testHubID)
	b, err := set.whiteboards.Create(wbProject, testHubID, "WB",
		whiteboards.Draft{Name: "Board"}, whiteboards.UserRef{ID: "u-editor"})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestWhiteboardEvents_DocChangedAndPeers is the awareness contract: a viewer
// with the board open learns that someone saved it, and sees who else is
// there — the two facts that turn "your save was refused" from a surprise into
// something the user watched happen.
func TestWhiteboardEvents_DocChangedAndPeers(t *testing.T) {
	s := newWhiteboardTestServer(t)
	board := seedBoard(t, s)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	watcher := login(t, s, "u-manager", "Bob", "manager@x.io")
	saver := login(t, s, "u-editor", "Ada", "editor@x.io")

	ch, closeStream := openBoardSSE(t, ts.URL, boardEventsURL(board.ID), watcher)
	defer closeStream()

	// Bob's own arrival is announced to Bob too — presence is a room fact.
	peers := waitEvent(t, ch, "peers", func(e sseEvent) bool {
		return strings.Contains(e.data, `"peers"`)
	})
	if !strings.Contains(peers.data, "Bob") {
		t.Fatalf("peers frame missing the subscriber: %s", peers.data)
	}
	if peers.id != "" {
		t.Errorf("peers frame carried id %q — presence must be ephemeral", peers.id)
	}

	// Ada saves; Bob hears about it, with the revision he needs to compare.
	docURL := "/api/whiteboards/doc?projectId=" + wbProject + "&boardId=" + board.ID
	if code, _, raw := docPut(t, ts.URL, docURL+"&baseRev=0", saver, `{"store":{"a":1}}`); code != http.StatusOK {
		t.Fatalf("save = %d: %s", code, raw)
	}
	changed := waitEvent(t, ch, "doc.changed", func(e sseEvent) bool {
		return strings.Contains(e.data, "doc.changed")
	})
	var ev struct {
		Type string `json:"type"`
		Data struct {
			Rev int64 `json:"rev"`
			By  struct {
				Name  string `json:"name"`
				Color string `json:"color"`
			} `json:"by"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(changed.data), &ev); err != nil {
		t.Fatalf("doc.changed body %s: %v", changed.data, err)
	}
	if ev.Data.Rev != 1 {
		t.Errorf("doc.changed rev = %d, want 1", ev.Data.Rev)
	}
	if ev.Data.By.Name != "Ada" {
		t.Errorf("doc.changed by = %q, want the saver's name", ev.Data.By.Name)
	}
	if ev.Data.By.Color == "" {
		t.Error("doc.changed carried no colour — the client can't attribute it consistently")
	}
	// It is durable: a reconnecting client must be able to replay it.
	if changed.id == "" {
		t.Error("doc.changed carried no id — it would never survive a reconnect")
	}
}

// TestWhiteboardEvents_ReadOnlyMemberStillReceives: a viewer must see the
// board change under them. Excluding them from the stream would leave the one
// person who cannot save staring at a document that silently went stale.
func TestWhiteboardEvents_ReadOnlyMemberStillReceives(t *testing.T) {
	s := newWhiteboardTestServer(t)
	board := seedBoard(t, s)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	viewer := login(t, s, "u-viewer", "Vic", "viewer@x.io")
	saver := login(t, s, "u-editor", "Ada", "editor@x.io")

	ch, closeStream := openBoardSSE(t, ts.URL, boardEventsURL(board.ID), viewer)
	defer closeStream()
	peers := waitEvent(t, ch, "peers", func(e sseEvent) bool { return strings.Contains(e.data, `"peers"`) })
	if !strings.Contains(peers.data, `"canWrite":false`) {
		t.Errorf("viewer should be listed as read-only: %s", peers.data)
	}

	docURL := "/api/whiteboards/doc?projectId=" + wbProject + "&boardId=" + board.ID
	if code, _, _ := docPut(t, ts.URL, docURL+"&baseRev=0", saver, `{"store":{}}`); code != http.StatusOK {
		t.Fatal("seed save failed")
	}
	waitEvent(t, ch, "doc.changed", func(e sseEvent) bool { return strings.Contains(e.data, "doc.changed") })
}

// TestWhiteboardEvents_RoomsAreScopedToOneBoard: two boards in one project are
// two rooms. Without the board in the room key, every board in a project would
// see every other board's saves.
func TestWhiteboardEvents_RoomsAreScopedToOneBoard(t *testing.T) {
	s := newWhiteboardTestServer(t)
	set := hubSet(t, s, testHubID)
	one := seedBoard(t, s)
	two, err := set.whiteboards.Create(wbProject, testHubID, "WB",
		whiteboards.Draft{Name: "Other"}, whiteboards.UserRef{ID: "u-editor"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	watcher := login(t, s, "u-manager", "Bob", "manager@x.io")
	saver := login(t, s, "u-editor", "Ada", "editor@x.io")

	ch, closeStream := openBoardSSE(t, ts.URL, boardEventsURL(one.ID), watcher)
	defer closeStream()
	waitEvent(t, ch, "peers", func(e sseEvent) bool { return strings.Contains(e.data, `"peers"`) })

	// Save the OTHER board.
	otherURL := "/api/whiteboards/doc?projectId=" + wbProject + "&boardId=" + two.ID
	if code, _, _ := docPut(t, ts.URL, otherURL+"&baseRev=0", saver, `{"store":{}}`); code != http.StatusOK {
		t.Fatal("save on the other board failed")
	}
	// Then this one, and assert the first frame seen is this board's.
	thisURL := "/api/whiteboards/doc?projectId=" + wbProject + "&boardId=" + one.ID
	if code, _, _ := docPut(t, ts.URL, thisURL+"&baseRev=0", saver, `{"store":{}}`); code != http.StatusOK {
		t.Fatal("save on this board failed")
	}
	changed := waitEvent(t, ch, "doc.changed", func(e sseEvent) bool {
		return strings.Contains(e.data, "doc.changed")
	})
	// Only one save reached this room, so the revision is this board's first.
	if !strings.Contains(changed.data, `"rev":1`) {
		t.Errorf("room leaked another board's events: %s", changed.data)
	}
}

// TestWhiteboardEvents_UnknownBoardIsNotFound: a stream on a board that
// doesn't exist would wait forever on a room nobody publishes to.
func TestWhiteboardEvents_UnknownBoardIsNotFound(t *testing.T) {
	s := newWhiteboardTestServer(t)
	seedBoard(t, s)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	cookie := login(t, s, "u-editor", "Ada", "editor@x.io")

	req, err := http.NewRequest(http.MethodGet, ts.URL+boardEventsURL("w999"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown board stream = %d, want 404", resp.StatusCode)
	}
}
