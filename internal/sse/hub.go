// Package sse is the in-process server-sent-events fan-out shared by the
// features that push: one subscriber per open browser tab per room, one short
// ring buffer per room for reconnect replay.
//
// It was chat's (docs/chat/PLAN.md phase 2) and is hoisted here unchanged in
// behaviour so whiteboards can push too. Two properties are worth stating,
// because they are what make sharing it safe:
//
//   - The hub makes NO authorization decision. Visibility rides through it as
//     an opaque V, and the OWNING feature decides — chat by its channel ACL,
//     whiteboards by the project capability its endpoint already checked. Move
//     an entitlement rule in here and it stops being reviewable in one place.
//   - The store, not the hub, is the source of truth. Anything the ring cannot
//     replay is answered with a reset, and the client refetches over REST, so
//     losing hub state can never lose data.
//
// Event ids are "<epoch>-<seq>". seq increments per published event; epoch
// comes from the owning feature and changes every process run, which is how a
// reconnecting client's Last-Event-ID from before a restart is detected as
// unusable (parseable, wrong epoch → reset).
package sse

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event is the wire envelope every SSE event shares.
type Event struct {
	Type string `json:"type"`
	V    int    `json:"v"`
	Data any    `json:"data"`
}

// Frame is one rendered SSE event: the id to send, the JSON payload, and the
// visibility the writer must enforce before writing it to a given subscriber.
// A frame with an empty ID is ephemeral — see PublishEphemeral.
type Frame[V any] struct {
	ID   string
	Data []byte
	Vis  V
}

// Config tunes a hub's buffering. Features differ: chat's traffic is a message
// every few seconds, a whiteboard's is a burst per drag.
type Config struct {
	// RingCap and RingTTL bound the replay buffer — how far back a reconnect
	// can be served before it is told to resync instead.
	RingCap int
	RingTTL time.Duration
	// SubBuf is a subscriber's channel depth. A subscriber too slow to drain it
	// is closed rather than allowed to stall publishes — its EventSource
	// reconnects and resyncs through the ring (or a reset).
	SubBuf int
}

// DefaultConfig is chat's original tuning, and a sane starting point.
func DefaultConfig() Config {
	return Config{RingCap: 512, RingTTL: 10 * time.Minute, SubBuf: 64}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.RingCap <= 0 {
		c.RingCap = d.RingCap
	}
	if c.RingTTL <= 0 {
		c.RingTTL = d.RingTTL
	}
	if c.SubBuf <= 0 {
		c.SubBuf = d.SubBuf
	}
	return c
}

// Subscriber is one open event stream. Events arrive on Events(); Closed()
// fires when the hub disconnects the subscriber (shutdown, overflow).
type Subscriber[V any] struct {
	ch   chan Frame[V]
	quit chan struct{}
	once sync.Once
}

func (s *Subscriber[V]) Events() <-chan Frame[V] { return s.ch }
func (s *Subscriber[V]) Closed() <-chan struct{} { return s.quit }
func (s *Subscriber[V]) close()                  { s.once.Do(func() { close(s.quit) }) }

type ringEntry[V any] struct {
	seq  int64
	at   time.Time
	data []byte
	vis  V
}

type room[V any] struct {
	epoch int64
	seq   int64
	ring  []ringEntry[V]
	subs  map[*Subscriber[V]]struct{}
}

// Hub fans events out to the subscribers of a room. V is whatever the owning
// feature needs to decide who may see a frame; the hub only carries it.
type Hub[V any] struct {
	epoch func(room string) (int64, error)
	cfg   Config

	mu    sync.Mutex
	rooms map[string]*room[V]
}

// NewHub wires a hub to an epoch source (per room, stable for a process run).
func NewHub[V any](epoch func(room string) (int64, error), cfg Config) *Hub[V] {
	return &Hub[V]{epoch: epoch, cfg: cfg.withDefaults(), rooms: make(map[string]*room[V])}
}

// Publish renders ev once, appends it to the room's ring, and fans it out.
// Slow subscribers are disconnected rather than waited on. Publish never
// blocks on entitlement checks — those belong in each subscriber's writer.
func (h *Hub[V]) Publish(roomID string, ev Event, vis V) error {
	return h.publish(roomID, ev, vis, false)
}

// PublishEphemeral fans ev out WITHOUT touching the ring or the id sequence:
// the frame carries no id, so it never advances a client's Last-Event-ID and
// is never replayed after a reconnect. For state that is only meaningful right
// now — a typing indicator, a cursor — where a replayed copy would be a lie.
func (h *Hub[V]) PublishEphemeral(roomID string, ev Event, vis V) error {
	return h.publish(roomID, ev, vis, true)
}

func (h *Hub[V]) publish(roomID string, ev Event, vis V, ephemeral bool) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	r, err := h.roomLocked(roomID)
	if err != nil {
		return err
	}
	f := Frame[V]{Data: data, Vis: vis}
	if !ephemeral {
		r.seq++
		now := time.Now()
		r.ring = append(r.ring, ringEntry[V]{seq: r.seq, at: now, data: data, vis: vis})
		for len(r.ring) > 0 && (len(r.ring) > h.cfg.RingCap || now.Sub(r.ring[0].at) > h.cfg.RingTTL) {
			r.ring = r.ring[1:]
		}
		f.ID = EventID(r.epoch, r.seq)
	}
	for sub := range r.subs {
		select {
		case sub.ch <- f:
		default:
			delete(r.subs, sub)
			sub.close()
		}
	}
	return nil
}

// Subscribe registers a stream for the room. lastEventID (may be empty) is the
// client's SSE cursor: when it parses to the current epoch and the ring still
// covers everything after it, the missed frames are returned for replay;
// otherwise reset=true tells the caller to instruct a full REST resync. Replay
// frames still need per-frame entitlement filtering by the caller.
func (h *Hub[V]) Subscribe(roomID, lastEventID string) (sub *Subscriber[V], replay []Frame[V], reset bool, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, err := h.roomLocked(roomID)
	if err != nil {
		return nil, nil, false, err
	}
	sub = &Subscriber[V]{ch: make(chan Frame[V], h.cfg.SubBuf), quit: make(chan struct{})}
	r.subs[sub] = struct{}{}

	if lastEventID == "" {
		return sub, nil, false, nil
	}
	epoch, seq, ok := ParseEventID(lastEventID)
	switch {
	case !ok, epoch != r.epoch, seq > r.seq:
		return sub, nil, true, nil
	case seq == r.seq:
		return sub, nil, false, nil
	case len(r.ring) == 0 || r.ring[0].seq > seq+1:
		// The events after seq are (partly) gone from the ring.
		return sub, nil, true, nil
	}
	for _, e := range r.ring {
		if e.seq > seq {
			replay = append(replay, Frame[V]{ID: EventID(r.epoch, e.seq), Data: e.data, Vis: e.vis})
		}
	}
	return sub, replay, false, nil
}

// Unsubscribe drops the subscriber (idempotent; also safe after CloseAll).
func (h *Hub[V]) Unsubscribe(roomID string, sub *Subscriber[V]) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[roomID]; ok {
		delete(r.subs, sub)
	}
	sub.close()
}

// Subscribers reports how many streams are open on a room. For features that
// show who is present; not load-bearing for delivery.
func (h *Hub[V]) Subscribers(roomID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[roomID]; ok {
		return len(r.subs)
	}
	return 0
}

// CloseAll disconnects every subscriber (server drain/rebind). The hub stays
// usable — a rebind serves new subscriptions immediately after.
func (h *Hub[V]) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.rooms {
		for sub := range r.subs {
			sub.close()
		}
		r.subs = make(map[*Subscriber[V]]struct{})
	}
}

// roomLocked resolves per-room state, pulling the epoch from the owning
// feature on first touch. Called under h.mu.
func (h *Hub[V]) roomLocked(roomID string) (*room[V], error) {
	if r, ok := h.rooms[roomID]; ok {
		return r, nil
	}
	epoch, err := h.epoch(roomID)
	if err != nil {
		return nil, err
	}
	r := &room[V]{epoch: epoch, subs: make(map[*Subscriber[V]]struct{})}
	h.rooms[roomID] = r
	return r, nil
}

// EventID renders an SSE id. Exported because handlers log and test them.
func EventID(epoch, seq int64) string {
	return fmt.Sprintf("%d-%d", epoch, seq)
}

// ParseEventID splits an SSE id back into its epoch and sequence.
func ParseEventID(id string) (epoch, seq int64, ok bool) {
	dash := strings.IndexByte(id, '-')
	if dash <= 0 || dash == len(id)-1 {
		return 0, 0, false
	}
	epoch, err1 := strconv.ParseInt(id[:dash], 10, 64)
	seq, err2 := strconv.ParseInt(id[dash+1:], 10, 64)
	return epoch, seq, err1 == nil && err2 == nil
}
