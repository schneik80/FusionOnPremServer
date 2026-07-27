package sse

import (
	"errors"
	"testing"
	"time"
)

// vis is a stand-in for a feature's visibility type: the hub must carry it
// untouched and never look inside it.
type vis struct{ tag string }

func newHub(t *testing.T, cfg Config) *Hub[vis] {
	t.Helper()
	return NewHub[vis](func(string) (int64, error) { return 7, nil }, cfg)
}

func publishN(t *testing.T, h *Hub[vis], room string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := h.Publish(room, Event{Type: "e", V: 1, Data: i}, vis{}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSubscribe_ReplayResetAndCursor covers the reconnect contract: a fresh
// subscriber gets nothing, a cursor inside the ring replays exactly what it
// missed, a cursor at the head replays nothing, and anything the ring can no
// longer cover resets instead of silently skipping events.
func TestSubscribe_ReplayResetAndCursor(t *testing.T) {
	h := newHub(t, Config{RingCap: 8, SubBuf: 8})
	publishN(t, h, "r", 5)

	if _, replay, reset, err := h.Subscribe("r", ""); err != nil || reset || len(replay) != 0 {
		t.Fatalf("fresh subscribe: replay=%d reset=%v err=%v", len(replay), reset, err)
	}
	if _, replay, reset, _ := h.Subscribe("r", EventID(7, 3)); reset || len(replay) != 2 {
		t.Fatalf("mid-ring cursor: replay=%d reset=%v, want 2/false", len(replay), reset)
	}
	if _, replay, reset, _ := h.Subscribe("r", EventID(7, 5)); reset || len(replay) != 0 {
		t.Fatalf("head cursor: replay=%d reset=%v, want 0/false", len(replay), reset)
	}
	// A different epoch is a restarted server: the cursor parses but means
	// nothing, so the client must resync rather than resume.
	if _, _, reset, _ := h.Subscribe("r", EventID(6, 3)); !reset {
		t.Error("stale epoch should force a reset")
	}
	// A cursor ahead of the hub (restore, rollback) is equally unusable.
	if _, _, reset, _ := h.Subscribe("r", EventID(7, 99)); !reset {
		t.Error("cursor ahead of the hub should force a reset")
	}
	if _, _, reset, _ := h.Subscribe("r", "nonsense"); !reset {
		t.Error("unparseable cursor should force a reset")
	}
}

// TestRingOverflowForcesReset: once events have aged out of the ring, a client
// holding a cursor behind them must be told to resync — replaying only the
// surviving tail would leave it silently missing the rest.
func TestRingOverflowForcesReset(t *testing.T) {
	h := newHub(t, Config{RingCap: 10, SubBuf: 64})
	publishN(t, h, "r", 60)

	if _, replay, reset, _ := h.Subscribe("r", EventID(7, 1)); !reset || len(replay) != 0 {
		t.Fatalf("overflowed cursor: replay=%d reset=%v, want reset", len(replay), reset)
	}
	if _, replay, reset, _ := h.Subscribe("r", EventID(7, 55)); reset || len(replay) != 5 {
		t.Fatalf("in-ring cursor: replay=%d reset=%v, want 5/false", len(replay), reset)
	}
}

// TestRingTTLExpiry: the ring is bounded by age as well as count, so a client
// away longer than the TTL resyncs rather than replaying stale frames.
func TestRingTTLExpiry(t *testing.T) {
	h := newHub(t, Config{RingCap: 1000, RingTTL: time.Nanosecond, SubBuf: 8})
	publishN(t, h, "r", 3)
	time.Sleep(2 * time.Millisecond)
	publishN(t, h, "r", 1) // the trim runs on publish
	if _, _, reset, _ := h.Subscribe("r", EventID(7, 1)); !reset {
		t.Error("cursor older than the ring TTL should force a reset")
	}
}

// TestPublishEphemeral: no id, never ringed, never replayed. A cursor is
// unaffected by ephemeral traffic, which is what keeps a cursor or typing
// indicator from being re-delivered as though it were current.
func TestPublishEphemeral(t *testing.T) {
	h := newHub(t, Config{RingCap: 8, SubBuf: 8})
	sub, _, _, err := h.Subscribe("r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.PublishEphemeral("r", Event{Type: "cursor", V: 1}, vis{}); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-sub.Events():
		if f.ID != "" {
			t.Errorf("ephemeral frame carried id %q", f.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("ephemeral frame not delivered")
	}
	// It left no trace in the ring, so a fresh cursor still sees an empty room.
	if _, replay, reset, _ := h.Subscribe("r", ""); reset || len(replay) != 0 {
		t.Errorf("ephemeral publish touched the ring: replay=%d reset=%v", len(replay), reset)
	}
}

// TestSlowSubscriberDisconnected: a subscriber that stops draining is dropped
// rather than allowed to stall every publisher. Its EventSource reconnects and
// resyncs, so the cost is a resync, not a hang.
func TestSlowSubscriberDisconnected(t *testing.T) {
	h := newHub(t, Config{RingCap: 64, SubBuf: 4})
	sub, _, _, err := h.Subscribe("r", "")
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, h, "r", 6) // never drained
	select {
	case <-sub.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("slow subscriber was not disconnected")
	}
}

// TestVisRidesThroughUntouched is the property that lets two features share
// this hub: the visibility value is carried, not interpreted.
func TestVisRidesThroughUntouched(t *testing.T) {
	h := newHub(t, Config{RingCap: 8, SubBuf: 8})
	sub, _, _, err := h.Subscribe("r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Publish("r", Event{Type: "e", V: 1}, vis{tag: "secret"}); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-sub.Events():
		if f.Vis.tag != "secret" {
			t.Errorf("vis = %+v, want it carried through", f.Vis)
		}
	case <-time.After(time.Second):
		t.Fatal("frame not delivered")
	}
	// Replay carries it too, or a reconnect would see frames it shouldn't.
	if _, replay, _, _ := h.Subscribe("r", EventID(7, 0)); len(replay) != 1 || replay[0].Vis.tag != "secret" {
		t.Errorf("replayed vis lost: %+v", replay)
	}
}

// TestRoomsAreIsolated: publishing to one room never reaches another's
// subscribers. Whiteboards key rooms per board and chat per project, so this
// is the boundary both rely on.
func TestRoomsAreIsolated(t *testing.T) {
	h := newHub(t, Config{RingCap: 8, SubBuf: 8})
	a, _, _, err := h.Subscribe("room-a", "")
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, h, "room-b", 3)
	select {
	case f := <-a.Events():
		t.Fatalf("room-a saw room-b's frame: %s", f.Data)
	case <-time.After(50 * time.Millisecond):
	}
	if n := h.Subscribers("room-a"); n != 1 {
		t.Errorf("Subscribers(room-a) = %d, want 1", n)
	}
	if n := h.Subscribers("room-c"); n != 0 {
		t.Errorf("Subscribers on an unknown room = %d, want 0", n)
	}
}

// TestCloseAllThenReuse: a drain disconnects everyone but leaves the hub
// usable, because a port rebind serves new subscriptions immediately after.
func TestCloseAllThenReuse(t *testing.T) {
	h := newHub(t, Config{RingCap: 8, SubBuf: 8})
	sub, _, _, err := h.Subscribe("r", "")
	if err != nil {
		t.Fatal(err)
	}
	h.CloseAll()
	select {
	case <-sub.Closed():
	case <-time.After(time.Second):
		t.Fatal("CloseAll did not close the subscriber")
	}
	h.Unsubscribe("r", sub) // idempotent after CloseAll
	if _, _, _, err := h.Subscribe("r", ""); err != nil {
		t.Fatalf("hub unusable after CloseAll: %v", err)
	}
}

// TestEpochErrorPropagates: a room whose epoch source fails must not be served
// with a made-up epoch, or a client's cursor would be validated against it.
func TestEpochErrorPropagates(t *testing.T) {
	boom := errors.New("epoch unavailable")
	h := NewHub[vis](func(string) (int64, error) { return 0, boom }, DefaultConfig())
	if _, _, _, err := h.Subscribe("r", ""); !errors.Is(err, boom) {
		t.Errorf("Subscribe error = %v, want the epoch error", err)
	}
	if err := h.Publish("r", Event{Type: "e"}, vis{}); !errors.Is(err, boom) {
		t.Errorf("Publish error = %v, want the epoch error", err)
	}
}

// TestConfigDefaults: a zero-value config is the chat tuning, so a caller that
// omits a field gets a working hub rather than a zero-capacity one.
func TestConfigDefaults(t *testing.T) {
	h := newHub(t, Config{})
	publishN(t, h, "r", 1)
	if _, _, reset, _ := h.Subscribe("r", EventID(7, 0)); reset {
		t.Error("zero-value config produced an unusable ring")
	}
}
