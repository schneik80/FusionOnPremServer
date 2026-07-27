package chat

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/internal/sse"
)

// fakeAuthorizer returns an Authorizer whose roster fetch is an in-memory
// function (no HTTP) — hub tests only need Entitled's decision logic.
func fakeAuthorizer(members []api.Member) *Authorizer {
	a := NewAuthorizer()
	a.fetch = func(ctx context.Context, token, projectID string) ([]api.Member, error) {
		return members, nil
	}
	return a
}

func newTestHub() *Hub {
	return NewHub(
		fakeAuthorizer([]api.Member{
			{UserID: "u-editor", Email: "e@x.io", Role: "EDITOR", Status: "ACTIVE"},
			{UserID: "u-member", Email: "m@x.io", Role: "EDITOR", Status: "ACTIVE"},
			{UserID: "u-admin", Email: "a@x.io", Role: "ADMINISTRATOR", Status: "ACTIVE"},
		}),
		func(projectID string) (int64, error) { return 7, nil },
	)
}

func recvFrame(t *testing.T, sub *Subscriber) Frame {
	t.Helper()
	select {
	case f := <-sub.Events():
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no frame within 2s")
		return Frame{}
	}
}

func TestHub_PublishDelivery(t *testing.T) {
	h := newTestHub()
	sub, replay, reset, err := h.Subscribe("p", "")
	if err != nil || reset || len(replay) != 0 {
		t.Fatalf("subscribe: replay=%d reset=%v err=%v", len(replay), reset, err)
	}
	if err := h.Publish("p", Event{Type: "message.created", V: 1, Data: map[string]any{"x": 1}}, Vis{}); err != nil {
		t.Fatal(err)
	}
	f := recvFrame(t, sub)
	if f.ID != "7-1" {
		t.Fatalf("frame id = %q, want 7-1", f.ID)
	}
	if string(f.Data) == "" || f.Data[0] != '{' {
		t.Fatalf("frame data = %q", f.Data)
	}
}

func TestHub_ReplayGapAndEpochRules(t *testing.T) {
	h := newTestHub()
	for i := 0; i < 3; i++ {
		if err := h.Publish("p", Event{Type: "message.created", V: 1, Data: i}, Vis{}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name       string
		lastID     string
		wantReplay int
		wantReset  bool
	}{
		{"resume mid-stream", "7-1", 2, false},
		{"current cursor", "7-3", 0, false},
		{"stale epoch", "6-2", 0, true},
		{"future seq", "7-99", 0, true},
		{"garbage", "nonsense", 0, true},
		{"no cursor", "", 0, false},
	}
	for _, c := range cases {
		sub, replay, reset, err := h.Subscribe("p", c.lastID)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(replay) != c.wantReplay || reset != c.wantReset {
			t.Errorf("%s: replay=%d reset=%v, want %d/%v", c.name, len(replay), reset, c.wantReplay, c.wantReset)
		}
		h.Unsubscribe("p", sub)
	}
}

func TestHub_RingOverflowForcesReset(t *testing.T) {
	h := newTestHub()
	for i := 0; i < sse.DefaultConfig().RingCap+50; i++ {
		if err := h.Publish("p", Event{Type: "e", V: 1, Data: i}, Vis{}); err != nil {
			t.Fatal(err)
		}
	}
	// Seq 1 fell out of the ring: a cursor there can't be replayed.
	_, replay, reset, err := h.Subscribe("p", "7-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reset || len(replay) != 0 {
		t.Fatalf("overflowed ring: replay=%d reset=%v, want reset", len(replay), reset)
	}
	// A cursor still inside the ring replays fine.
	_, replay, reset, _ = h.Subscribe("p", sse.EventID(7, int64(sse.DefaultConfig().RingCap+40)))
	if reset || len(replay) != 10 {
		t.Fatalf("in-ring cursor: replay=%d reset=%v, want 10/false", len(replay), reset)
	}
}

func TestHub_EntitledVisibility(t *testing.T) {
	h := newTestHub()
	ctx := context.Background()
	private := Channel{ID: "c2", Name: "secret", IsPrivate: true,
		Members: []ChannelMember{{UserID: "u-member"}}}

	pub := Frame{Vis: Vis{}}
	priv := Frame{Vis: Vis{Private: true, Channel: private}}
	privExtra := Frame{Vis: Vis{Private: true, Channel: private, ExtraUserIDs: []string{"u-editor"}}}

	cases := []struct {
		name string
		id   Identity
		f    Frame
		want bool
	}{
		{"public frame, anyone", Identity{UserID: "u-editor"}, pub, true},
		{"private, non-member editor", Identity{UserID: "u-editor"}, priv, false},
		{"private, ACL member", Identity{UserID: "u-member"}, priv, true},
		{"private, project admin", Identity{UserID: "u-admin"}, priv, true},
		{"private, extra user", Identity{UserID: "u-editor"}, privExtra, true},
	}
	for _, c := range cases {
		got, err := h.Entitled(ctx, "tok", c.id, "p", c.f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHub_EphemeralFramesAreIDlessAndNeverReplayed(t *testing.T) {
	h := newTestHub()
	sub, _, _, err := h.Subscribe("p", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Publish("p", Event{Type: "message.created", V: 1, Data: 1}, Vis{}); err != nil {
		t.Fatal(err)
	}
	durable := recvFrame(t, sub)
	if durable.ID == "" {
		t.Fatal("durable frame lost its id")
	}
	if err := h.PublishEphemeral("p", Event{Type: "typing", V: 1, Data: 2}, Vis{}); err != nil {
		t.Fatal(err)
	}
	eph := recvFrame(t, sub)
	if eph.ID != "" {
		t.Fatalf("ephemeral frame has id %q, want none", eph.ID)
	}

	// Resuming from the durable frame's id replays nothing: the ephemeral
	// publish neither entered the ring nor advanced the sequence.
	_, replay, reset, err := h.Subscribe("p", durable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset || len(replay) != 0 {
		t.Fatalf("after ephemeral publish: replay=%d reset=%v, want 0/false", len(replay), reset)
	}
}

func TestHub_UserOnlyVisibility(t *testing.T) {
	h := newTestHub()
	ctx := context.Background()
	f := Frame{Vis: Vis{UserOnly: true, ExtraUserIDs: []string{"u-member"}}}
	emailF := Frame{Vis: Vis{UserOnly: true, ExtraUserIDs: []string{"m@x.io"}}}

	cases := []struct {
		name string
		id   Identity
		f    Frame
		want bool
	}{
		{"targeted user", Identity{UserID: "u-member"}, f, true},
		{"other user", Identity{UserID: "u-editor"}, f, false},
		{"project admin still excluded", Identity{UserID: "u-admin"}, f, false},
		{"email-keyed target", Identity{Email: "M@X.IO"}, emailF, true},
		{"email-keyed, other user", Identity{UserID: "u-editor", Email: "e@x.io"}, emailF, false},
	}
	for _, c := range cases {
		got, err := h.Entitled(ctx, "tok", c.id, "p", c.f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHub_SlowSubscriberDisconnected(t *testing.T) {
	h := newTestHub()
	sub, _, _, err := h.Subscribe("p", "")
	if err != nil {
		t.Fatal(err)
	}
	// Never drain: the buffer fills, then the next publish must evict.
	for i := 0; i < sse.DefaultConfig().SubBuf+1; i++ {
		if err := h.Publish("p", Event{Type: "e", V: 1, Data: i}, Vis{}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-sub.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("slow subscriber was not disconnected")
	}
}

func TestHub_CloseAllThenReuse(t *testing.T) {
	h := newTestHub()
	subs := make([]*Subscriber, 3)
	for i := range subs {
		s, _, _, err := h.Subscribe(fmt.Sprintf("p%d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		subs[i] = s
	}
	h.CloseAll()
	for i, s := range subs {
		select {
		case <-s.Closed():
		default:
			t.Fatalf("subscriber %d still open after CloseAll", i)
		}
	}
	// The hub must accept new subscribers afterwards (port rebind case).
	fresh, _, _, err := h.Subscribe("p0", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Publish("p0", Event{Type: "e", V: 1, Data: nil}, Vis{}); err != nil {
		t.Fatal(err)
	}
	recvFrame(t, fresh)
}
