package server

import (
	"crypto/sha256"
	"sort"
	"sync"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/sse"
)

// The whiteboard fan-out: who has a board open, and when its document changed.
//
// It is a SEPARATE hub from chat's, keyed per board rather than per project.
// Sharing chat's would mean a busy board's traffic evicting chat events from
// the shared ring, handing every project subscriber a spurious resync — the
// cost of coupling two features' throughput.
//
// There is no per-frame visibility rule: a subscriber has already passed the
// project's CapRead at the endpoint, and a board carries no ACL of its own, so
// the entitlement story is the endpoint's check plus the keepalive revocation
// tick. Hence the empty visibility type — the hub carries it and nobody reads
// it, which is exactly what the shared hub's opaque V is for.
type wbVis struct{}

// whiteboardHub is the per-hub fan-out plus the presence registry that answers
// "who else has this board open".
type whiteboardHub struct {
	*sse.Hub[wbVis]

	mu    sync.Mutex
	rooms map[string]map[string]wbPeer // room key → subscription id → peer
}

// whiteboardRingConfig tunes the ring for board traffic: awareness events are
// sparse (a save, someone arriving), but they are only useful while fresh — a
// ten-minute-old "the board changed" is noise, since the client resyncs on
// reconnect anyway.
func whiteboardRingConfig() sse.Config {
	return sse.Config{RingCap: 256, RingTTL: 2 * time.Minute, SubBuf: 64}
}

func newWhiteboardHub() *whiteboardHub {
	// The epoch is fixed for the process. Unlike chat there is nothing durable
	// to derive it from — and nothing needs one: a restart drops every
	// subscriber, and a client reconnecting with a cursor from the previous run
	// is told to resync, which for a board means re-reading the document it was
	// going to re-read anyway.
	epoch := time.Now().UnixNano()
	return &whiteboardHub{
		Hub:   sse.NewHub[wbVis](func(string) (int64, error) { return epoch, nil }, whiteboardRingConfig()),
		rooms: make(map[string]map[string]wbPeer),
	}
}

// wbRoom keys a board's room. Board ids are only unique within a project, so
// the project id is part of the key — without it two projects' "w1" would
// share a room and leak each other's presence.
func wbRoom(projectID, boardID string) string { return projectID + "\x00" + boardID }

// wbPeer is one person on a board, as shown in the canvas header.
type wbPeer struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	CanWrite bool   `json:"canWrite"`
}

// join records a subscription's peer and returns the room's current roster.
func (h *whiteboardHub) join(room, subID string, p wbPeer) []wbPeer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[room] == nil {
		h.rooms[room] = make(map[string]wbPeer)
	}
	h.rooms[room][subID] = p
	return rosterLocked(h.rooms[room])
}

// leave drops a subscription and returns what remains.
func (h *whiteboardHub) leave(room, subID string) []wbPeer {
	h.mu.Lock()
	defer h.mu.Unlock()
	peers := h.rooms[room]
	if peers == nil {
		return []wbPeer{}
	}
	delete(peers, subID)
	if len(peers) == 0 {
		delete(h.rooms, room)
		return []wbPeer{}
	}
	return rosterLocked(peers)
}

// rosterLocked collapses subscriptions to people: one entry per user, however
// many tabs they have open, sorted for a stable avatar order.
func rosterLocked(peers map[string]wbPeer) []wbPeer {
	seen := make(map[string]struct{}, len(peers))
	out := make([]wbPeer, 0, len(peers))
	for _, p := range peers {
		key := p.UserID
		if key == "" {
			key = p.Name
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].UserID < out[j].UserID
	})
	return out
}

// publishPeers fans the roster out as an EPHEMERAL frame: presence is only
// true right now, so a replayed copy after a reconnect would assert that
// someone is present who left ten minutes ago.
func (h *whiteboardHub) publishPeers(room string, peers []wbPeer) {
	_ = h.PublishEphemeral(room, sse.Event{
		Type: "peers",
		V:    1,
		Data: map[string]any{"peers": peers},
	}, wbVis{})
}

// wbPalette is the cursor/avatar colour set — picked for legibility on both
// canvas backgrounds, and deliberately distinguishable from each other.
var wbPalette = []string{
	"#0696d7", "#e8734a", "#5f9b41", "#b4508f", "#d4a017",
	"#3b7dd8", "#c2553f", "#4aa3a3", "#8a5cd1", "#c9762f",
	"#4f7f8f", "#a14b6a",
}

// peerColor derives a stable colour from the user's OIDC sub. Server-side and
// deterministic, so the same person is the same colour for everyone looking at
// the board — a client-chosen colour would differ per viewer.
func peerColor(userID string) string {
	if userID == "" {
		return wbPalette[0]
	}
	sum := sha256.Sum256([]byte(userID))
	return wbPalette[int(sum[0])%len(wbPalette)]
}
