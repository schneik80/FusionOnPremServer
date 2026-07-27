package whiteboards

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// Rooms owns the live boards: which documents are currently being edited, and
// when they get written back to disk.
//
// Lifecycle: a room is created on first use (someone opened the board), kept
// while anyone is on it, and evicted after it has been quiet and empty for
// RoomIdleTTL. Persistence is debounced — a stroke is not a file write — with a
// ceiling so a continuously-drawn board still checkpoints.
//
// Lock order is rs.mu → room (rooms are only touched under rs.mu) → Store.mu →
// ps.mu. Nothing holding a project's mutex may call in here.
type Rooms struct {
	st  *Store
	now func() time.Time

	mu    sync.Mutex
	rooms map[string]*Room
}

const (
	// RoomSaveQuiet is how long a board must sit still before it is written.
	RoomSaveQuiet = 2 * time.Second
	// RoomSaveMax bounds how long unsaved work can exist while someone keeps
	// drawing, since the quiet timer alone would never fire.
	RoomSaveMax = 15 * time.Second
	// RoomIdleTTL is how long an empty room lingers before eviction. Long
	// enough that switching tabs and back doesn't reload a big document,
	// short enough that idle boards don't hold memory.
	RoomIdleTTL = 2 * time.Minute
	// MaxLiveRooms caps concurrent live boards. A room holds a whole document
	// resident (up to MaxSnapshotBytes), so this is the memory bound.
	MaxLiveRooms = 32
)

// ErrBusy is returned when MaxLiveRooms is reached (→ 503). The client falls
// back to reading the board rather than editing it live.
var ErrBusy = errors.New("whiteboards: too many boards are open for live editing")

// NewRooms wires a registry to its store. The store keeps a back-reference so
// deletes and resets can drop rooms without the caller remembering to.
func NewRooms(st *Store) *Rooms {
	rs := &Rooms{st: st, now: time.Now, rooms: make(map[string]*Room)}
	st.setRooms(rs)
	return rs
}

func roomKey(projectID, boardID string) string { return projectID + "\x00" + boardID }

// Open loads (or finds) a board's room and registers a subscriber. Release
// must follow, or the room never becomes idle.
func (rs *Rooms) Open(projectID, boardID string) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r, err := rs.roomLocked(projectID, boardID)
	if err != nil {
		return err
	}
	r.subs++
	r.lastTouch = rs.now()
	return nil
}

// Release drops a subscriber. The room stays warm until the janitor evicts it,
// so a reload or a tab switch doesn't re-read the document.
func (rs *Rooms) Release(projectID, boardID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if r, ok := rs.rooms[roomKey(projectID, boardID)]; ok {
		if r.subs > 0 {
			r.subs--
		}
		r.lastTouch = rs.now()
	}
}

// Apply folds a patch into a board and reports what to broadcast.
func (rs *Rooms) Apply(projectID, boardID string, req DocPatchRequest, by UserRef) (Applied, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r, err := rs.roomLocked(projectID, boardID)
	if err != nil {
		return Applied{}, err
	}
	return r.apply(req, by, rs.now())
}

// Replace swaps a live board's whole document (a full-document PUT arriving
// while people are editing). Returns the new revision. When no room is live
// this is not called at all — the store write stands on its own.
func (rs *Rooms) Replace(projectID, boardID string, doc []byte, by UserRef) (int64, bool, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r, ok := rs.rooms[roomKey(projectID, boardID)]
	if !ok {
		return 0, false, nil
	}
	rev, err := r.replace(doc, by, rs.now())
	return rev, true, err
}

// Snapshot returns a live board's current document and revision, for a client
// joining or resyncing. Falls back to the stored file when no room is live.
func (rs *Rooms) Snapshot(projectID, boardID string) ([]byte, int64, error) {
	rs.mu.Lock()
	r, ok := rs.rooms[roomKey(projectID, boardID)]
	if ok {
		doc, err := r.document()
		rev := r.rev
		rs.mu.Unlock()
		return doc, rev, err
	}
	rs.mu.Unlock()
	return rs.st.Document(projectID, boardID)
}

// Rev reports a board's live revision, or the stored one when it is not live.
func (rs *Rooms) Rev(projectID, boardID string) (int64, error) {
	rs.mu.Lock()
	if r, ok := rs.rooms[roomKey(projectID, boardID)]; ok {
		rev := r.rev
		rs.mu.Unlock()
		return rev, nil
	}
	rs.mu.Unlock()
	_, rev, err := rs.st.Document(projectID, boardID)
	return rev, err
}

// Sweep is the janitor: persist what has settled, evict what nobody is using.
// One ticker drives every room, rather than a goroutine and timer per board.
func (rs *Rooms) Sweep() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	now := rs.now()
	for key, r := range rs.rooms {
		if r.dirty {
			quiet := now.Sub(r.lastTouch) >= RoomSaveQuiet
			overdue := !r.dirtyAt.IsZero() && now.Sub(r.dirtyAt) >= RoomSaveMax
			if quiet || overdue {
				rs.flushLocked(r)
			}
		}
		if r.subs == 0 && now.Sub(r.lastTouch) >= RoomIdleTTL {
			rs.flushLocked(r)
			if !r.dirty { // only drop once its work is safely on disk
				delete(rs.rooms, key)
			}
		}
	}
}

// FlushAll writes every dirty room. Called before a backup reads the files, and
// at shutdown/rebind — the moments when what is on disk has to be what is on
// screen.
func (rs *Rooms) FlushAll() {
	if rs == nil {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, r := range rs.rooms {
		rs.flushLocked(r)
	}
}

// DropAll discards every room WITHOUT writing. Exactly one caller wants this:
// a backup restore, which has just replaced the files underneath us. Flushing
// there would overwrite the restored board with the pre-restore state — the
// single most dangerous thing this type could do, which is why the store calls
// it from Reset rather than leaving it to each caller to remember.
func (rs *Rooms) DropAll() {
	if rs == nil {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.rooms = make(map[string]*Room)
}

// Drop discards one board's room without writing — the board is being deleted,
// so persisting it would resurrect the document file after its metadata is gone.
func (rs *Rooms) Drop(projectID, boardID string) {
	if rs == nil {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.rooms, roomKey(projectID, boardID))
}

// DropProject discards every room belonging to a project (project deletion).
func (rs *Rooms) DropProject(projectID string) {
	if rs == nil {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	prefix := projectID + "\x00"
	for key := range rs.rooms {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(rs.rooms, key)
		}
	}
}

// Live reports the number of rooms currently held, for tests and diagnostics.
func (rs *Rooms) Live() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return len(rs.rooms)
}

// roomLocked finds or builds a room. Called under rs.mu.
func (rs *Rooms) roomLocked(projectID, boardID string) (*Room, error) {
	key := roomKey(projectID, boardID)
	if r, ok := rs.rooms[key]; ok {
		return r, nil
	}
	if len(rs.rooms) >= MaxLiveRooms {
		// Try to make space from rooms nobody is on before refusing.
		rs.evictIdleLocked()
		if len(rs.rooms) >= MaxLiveRooms {
			return nil, ErrBusy
		}
	}
	doc, rev, err := rs.st.Document(projectID, boardID)
	if err != nil {
		return nil, err
	}
	r, err := newRoom(projectID, boardID, doc, rev, rs.now())
	if err != nil {
		return nil, err
	}
	rs.rooms[key] = r
	return r, nil
}

// evictIdleLocked drops the least recently touched unsubscribed rooms, flushing
// first. Called under rs.mu when the cap is reached.
func (rs *Rooms) evictIdleLocked() {
	type cand struct {
		key string
		at  time.Time
	}
	var idle []cand
	for key, r := range rs.rooms {
		if r.subs == 0 {
			idle = append(idle, cand{key, r.lastTouch})
		}
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].at.Before(idle[j].at) })
	for _, c := range idle {
		r := rs.rooms[c.key]
		rs.flushLocked(r)
		if !r.dirty {
			delete(rs.rooms, c.key)
		}
		if len(rs.rooms) < MaxLiveRooms {
			return
		}
	}
}

// flushLocked writes a dirty room through the store — the single document
// writer, so the atomic temp+rename, the size cap and the metadata stamp all
// still apply. A failed write leaves the room dirty to be retried; the user's
// work stays in memory and on their screen either way.
func (rs *Rooms) flushLocked(r *Room) {
	if !r.dirty {
		return
	}
	doc, err := r.document()
	if err != nil {
		return
	}
	if _, err := rs.st.saveSnapshotAtRev(r.projectID, r.boardID, doc, r.lastBy, r.rev); err != nil {
		return // stays dirty; Sweep will try again
	}
	r.dirty = false
	r.savedRev = r.rev
	r.dirtyAt = time.Time{}
}

// StartSweeper runs the janitor until stop is closed. One goroutine per hub,
// not per board.
func (rs *Rooms) StartSweeper(every time.Duration, stop <-chan struct{}) {
	if every <= 0 {
		every = time.Second
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				rs.FlushAll()
				return
			case <-t.C:
				rs.Sweep()
			}
		}
	}()
}
