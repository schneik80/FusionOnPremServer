package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseEvent is one parsed frame off the events stream. Pings (comment
// lines) are dropped by the reader.
type sseEvent struct {
	id    string
	event string // "" for unnamed data frames, "reset" for resync
	data  string
}

// openSSE connects to /api/chat/events and pumps parsed frames to a
// channel, which closes when the stream ends. The returned func closes the
// connection.
func openSSE(t *testing.T, base string, cookie *http.Cookie, lastEventID string) (<-chan sseEvent, func()) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+chatURL("/api/chat/events"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("events stream status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		resp.Body.Close()
		t.Fatalf("Content-Type = %q", ct)
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
			case strings.HasPrefix(line, ":"):
				// keepalive comment — ignore
			}
		}
	}()
	t.Cleanup(func() { resp.Body.Close() })
	return ch, func() { resp.Body.Close() }
}

// waitEvent pulls frames until match returns true, failing on timeout or
// stream end.
func waitEvent(t *testing.T, ch <-chan sseEvent, what string, match func(sseEvent) bool) sseEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed while waiting for %s", what)
			}
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func waitClosed(t *testing.T, ch <-chan sseEvent, what string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatalf("stream not closed: %s", what)
		}
	}
}

func TestSSE_DeliversMessagesLive(t *testing.T) {
	s, _ := newChatTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")
	member := login(t, s, "u-member", "Mel", "member@x.io")

	var list ChannelListDTO
	chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, &list)
	root := list.Channels[0].ID

	events, _ := openSSE(t, ts.URL, editor, "")
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/messages", "channelId", root), member,
		map[string]any{"body": "hello over sse", "clientMsgId": "cm-sse-1"}, nil)

	created := waitEvent(t, events, "message.created", func(ev sseEvent) bool {
		return strings.Contains(ev.data, `"message.created"`) && strings.Contains(ev.data, "hello over sse")
	})
	if created.id == "" {
		t.Fatal("data frame carries no SSE id")
	}
	waitEvent(t, events, "channel.activity", func(ev sseEvent) bool {
		return strings.Contains(ev.data, `"channel.activity"`)
	})
}

func TestSSE_ReplayAfterReconnect(t *testing.T) {
	s, _ := newChatTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")

	var list ChannelListDTO
	chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, &list)
	root := list.Channels[0].ID

	events, closeStream := openSSE(t, ts.URL, editor, "")
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/messages", "channelId", root), editor,
		map[string]any{"body": "before drop", "clientMsgId": "cm-r1"}, nil)
	first := waitEvent(t, events, "first message", func(ev sseEvent) bool {
		return strings.Contains(ev.data, "before drop")
	})
	closeStream()

	// Missed while disconnected.
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/messages", "channelId", root), editor,
		map[string]any{"body": "missed while away", "clientMsgId": "cm-r2"}, nil)

	replayed, _ := openSSE(t, ts.URL, editor, first.id)
	waitEvent(t, replayed, "replayed message", func(ev sseEvent) bool {
		return strings.Contains(ev.data, "missed while away")
	})
}

func TestSSE_StaleCursorGetsReset(t *testing.T) {
	s, _ := newChatTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")
	// Touch the project so the stream can subscribe.
	chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, nil)

	events, _ := openSSE(t, ts.URL, editor, "999-42") // epoch from "another run"
	waitEvent(t, events, "reset frame", func(ev sseEvent) bool { return ev.event == "reset" })
}

func TestSSE_PrivateChannelEventsFiltered(t *testing.T) {
	s, _ := newChatTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	manager := login(t, s, "u-manager", "Mia", "manager@x.io")
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")
	member := login(t, s, "u-member", "Mel", "member@x.io")

	var list ChannelListDTO
	chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), manager, nil, &list)
	root := list.Channels[0].ID

	editorEvents, _ := openSSE(t, ts.URL, editor, "")
	memberEvents, _ := openSSE(t, ts.URL, member, "")

	// Private channel + a message in it, then a public marker message.
	var priv ChannelDTO
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/channels"), manager,
		map[string]any{"name": "war-room", "isPrivate": true, "memberIds": []string{"u-member"}}, &priv)
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/messages", "channelId", priv.ID), manager,
		map[string]any{"body": "classified", "clientMsgId": "cm-p1"}, nil)
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/messages", "channelId", root), manager,
		map[string]any{"body": "public marker", "clientMsgId": "cm-pub"}, nil)

	// The ACL member sees the private channel + its message.
	waitEvent(t, memberEvents, "member sees channel.created", func(ev sseEvent) bool {
		return strings.Contains(ev.data, `"channel.created"`) && strings.Contains(ev.data, "war-room")
	})
	waitEvent(t, memberEvents, "member sees private message", func(ev sseEvent) bool {
		return strings.Contains(ev.data, "classified")
	})

	// The non-member editor's stream is ordered, so everything before the
	// public marker is exactly what they were entitled to — and none of it
	// may be the private channel's.
	marker := waitEvent(t, editorEvents, "public marker", func(ev sseEvent) bool {
		if strings.Contains(ev.data, "war-room") || strings.Contains(ev.data, "classified") || strings.Contains(ev.data, priv.ID) {
			t.Fatalf("non-member received a private-channel event: %s", ev.data)
		}
		return strings.Contains(ev.data, "public marker")
	})
	_ = marker
}

func TestSSE_RevocationClosesStream(t *testing.T) {
	s, roster := newChatTestServer(t)
	s.chatAuthz.SetTTLsForTesting(30*time.Millisecond, 30*time.Millisecond)
	s.chatKeepalive = 30 * time.Millisecond
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")
	chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, nil)

	events, _ := openSSE(t, ts.URL, editor, "")
	// Suspend the user (still on the roster, but INACTIVE) — the realistic
	// revocation the keepalive tick catches. A full project removal instead
	// makes the user's own token fail to read the roster, a transient-looking
	// error the tick deliberately rides out; and merely de-listing an
	// individual now reads as group-derived access, not a denial.
	roster.SetStatus("u-editor", "INACTIVE")
	waitClosed(t, events, "suspended user's stream")
}

func TestSSE_CloseAllDisconnectsStreams(t *testing.T) {
	s, _ := newChatTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")
	chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, nil)

	events, _ := openSSE(t, ts.URL, editor, "")
	hubSet(t, s, testHubID).chatHub.CloseAll()
	waitClosed(t, events, "stream after CloseAll")
}

func TestSSE_TypingFrameIsIDless(t *testing.T) {
	s, _ := newChatTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")
	member := login(t, s, "u-member", "Mel", "member@x.io")

	var list ChannelListDTO
	chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, &list)
	root := list.Channels[0].ID

	events, _ := openSSE(t, ts.URL, editor, "")
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/typing", "channelId", root), member, nil, nil)

	typing := waitEvent(t, events, "typing", func(ev sseEvent) bool {
		return strings.Contains(ev.data, `"typing"`)
	})
	if typing.id != "" {
		t.Fatalf("typing frame carries id %q — it must never advance Last-Event-ID", typing.id)
	}
	if !strings.Contains(typing.data, "Mel") {
		t.Fatalf("typing frame lacks the author name: %s", typing.data)
	}

	// A durable message afterwards still has an id (the writer branches
	// per frame, not per stream).
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/messages", "channelId", root), member,
		map[string]any{"body": "durable after typing", "clientMsgId": "cm-t1"}, nil)
	durable := waitEvent(t, events, "durable message", func(ev sseEvent) bool {
		return strings.Contains(ev.data, "durable after typing")
	})
	if durable.id == "" {
		t.Fatal("durable frame lost its id")
	}
}

func TestSSE_ReadUpdatedIsUserOnly(t *testing.T) {
	s, _ := newChatTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")
	manager := login(t, s, "u-manager", "Mia", "manager@x.io")

	var list ChannelListDTO
	chatDo(t, ts.URL, http.MethodGet, chatURL("/api/chat/channels"), editor, nil, &list)
	root := list.Channels[0].ID
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/messages", "channelId", root), editor,
		map[string]any{"body": "to be read", "clientMsgId": "cm-ru1"}, nil)

	editorEvents, _ := openSSE(t, ts.URL, editor, "")
	managerEvents, _ := openSSE(t, ts.URL, manager, "")

	// The editor marks the channel read in "another tab".
	chatDo(t, ts.URL, http.MethodPatch, chatURL("/api/chat/read", "channelId", root), editor,
		map[string]any{"lastReadSeq": 1}, nil)
	// Then posts a marker so the manager's (ordered) stream provably moved
	// past the point where the read.updated would have been.
	chatDo(t, ts.URL, http.MethodPost, chatURL("/api/chat/messages", "channelId", root), editor,
		map[string]any{"body": "marker after read", "clientMsgId": "cm-ru2"}, nil)

	waitEvent(t, editorEvents, "editor's own read.updated", func(ev sseEvent) bool {
		return strings.Contains(ev.data, `"read.updated"`)
	})
	waitEvent(t, managerEvents, "marker on manager stream", func(ev sseEvent) bool {
		if strings.Contains(ev.data, `"read.updated"`) {
			t.Fatalf("another user (even a moderator) received a user-only read.updated: %s", ev.data)
		}
		return strings.Contains(ev.data, "marker after read")
	})
}
