package whiteboards

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Room is one board's live document: the authoritative record map plus a
// revision bumped once per accepted patch. The doc-<id>.json file behind it is
// the durable copy, written on a debounce by the Rooms janitor.
//
// While a room exists it — not the file — is the truth. That is what lets two
// people edit at once: each patch is applied here in a defined order and
// re-broadcast, rather than each client writing its own whole document over
// whatever it last saw.
type Room struct {
	projectID string
	boardID   string

	records map[string]json.RawMessage
	schema  json.RawMessage // tldraw's schema stamp, carried verbatim
	rev     int64
	bytes   int64

	// tombs is delete-wins: record id → the revision at which it was removed.
	// Without it, LWW resurrects zombies — A deletes a shape, B (who hadn't
	// heard yet) drags it, and B's put brings it back from the dead.
	tombs map[string]int64
	// lastSeq dedups a client's retried POST. Re-applying a put the user has
	// since changed would silently undo their newer edit.
	lastSeq map[string]int64

	lastBy    UserRef
	dirty     bool
	savedRev  int64
	dirtyAt   time.Time
	lastTouch time.Time
	subs      int
}

// liveDoc is the on-disk shape of a tldraw document, as the room holds it.
type liveDoc struct {
	Store  map[string]json.RawMessage `json:"store"`
	Schema json.RawMessage            `json:"schema,omitempty"`
}

// newRoom builds a room from a stored document. A nil document is a board that
// has never been saved — an empty canvas, which is a legitimate starting state
// and not an error.
func newRoom(projectID, boardID string, doc []byte, rev int64, now time.Time) (*Room, error) {
	r := &Room{
		projectID: projectID,
		boardID:   boardID,
		records:   make(map[string]json.RawMessage),
		rev:       rev,
		savedRev:  rev,
		tombs:     make(map[string]int64),
		lastSeq:   make(map[string]int64),
		lastTouch: now,
	}
	if len(doc) == 0 {
		return r, nil
	}
	var ld liveDoc
	if err := json.Unmarshal(doc, &ld); err != nil {
		// Refuse rather than starting empty: an empty room would look like a
		// blank board and the first save would overwrite whatever the file
		// really held.
		return nil, fmt.Errorf("%w: stored document is unreadable: %v", ErrInvalid, err)
	}
	for id, rec := range ld.Store {
		r.records[id] = rec
		r.bytes += int64(len(rec) + len(id))
	}
	r.schema = ld.Schema
	return r, nil
}

// document serialises the room for persistence. Go sorts map keys when
// marshalling, so identical states produce identical bytes — which keeps the
// backup manifest's sha256 stable across a save that changed nothing.
func (r *Room) document() ([]byte, error) {
	return json.Marshal(liveDoc{Store: r.records, Schema: r.schema})
}

// apply folds one client's patch into the room and returns what to broadcast.
// Conflict policy is last-write-wins per record, with three exceptions:
//
//   - a put for a record deleted at a revision the sender hadn't seen is
//     REFUSED (tombstones above);
//   - removing a shape also removes the bindings that reference it, and the
//     cascade is broadcast so peers converge;
//   - a patch touching page/document records requires an exactly current base
//     revision, because racing those breaks the whole document rather than one
//     shape.
//
// Callers hold no lock; Rooms serialises access.
func (r *Room) apply(req DocPatchRequest, by UserRef, now time.Time) (Applied, error) {
	if err := req.validate(); err != nil {
		return Applied{}, err
	}
	// A retried POST (the response was lost, not the request) must not apply
	// twice. Answering with the current revision lets the client move on.
	if last, ok := r.lastSeq[req.ClientID]; ok && req.Seq <= last {
		return Applied{Rev: r.rev}, nil
	}
	if req.BaseRev > r.rev {
		// The client claims a revision this room has never reached — a restart
		// or a restore beneath it. It must resync; applying would be guesswork.
		return Applied{}, fmt.Errorf("%w: patch is based on revision %d, ahead of %d",
			ErrConflict, req.BaseRev, r.rev)
	}
	for id := range req.Put {
		if isStructural(id) && req.BaseRev != r.rev {
			return Applied{}, fmt.Errorf("%w: %q needs the current revision", ErrConflict, id)
		}
	}
	for _, id := range req.Remove {
		if isStructural(id) && req.BaseRev != r.rev {
			return Applied{}, fmt.Errorf("%w: %q needs the current revision", ErrConflict, id)
		}
	}

	out := DocPatch{Put: make(map[string]json.RawMessage), Remove: nil}
	var rejected []string

	// Removes first, so a patch that deletes and re-adds within one diff ends
	// up with the record present rather than tombstoned.
	removedShapes := make(map[string]struct{})
	for _, id := range req.Remove {
		if rec, ok := r.records[id]; ok {
			r.bytes -= int64(len(rec) + len(id))
			delete(r.records, id)
		}
		r.tombs[id] = r.rev + 1
		out.Remove = append(out.Remove, id)
		if isShape(id) {
			removedShapes[id] = struct{}{}
		}
	}
	// Cascade: bindings that reference a removed shape go too. The originating
	// client's own diff is already self-consistent (tldraw removes bindings in
	// the same transaction), so this only fires on the race — but on that race
	// it is the difference between converging and every peer force-resyncing.
	if len(removedShapes) > 0 {
		for id, rec := range r.records {
			if !isBinding(id) || !bindingTouches(rec, removedShapes) {
				continue
			}
			r.bytes -= int64(len(rec) + len(id))
			delete(r.records, id)
			r.tombs[id] = r.rev + 1
			out.Remove = append(out.Remove, id)
		}
	}

	for id, rec := range req.Put {
		if tomb, dead := r.tombs[id]; dead && req.BaseRev < tomb {
			// Deleted after this client last heard: refuse rather than
			// resurrect. The client removes it locally on the way back.
			rejected = append(rejected, id)
			continue
		}
		if prev, ok := r.records[id]; ok {
			r.bytes -= int64(len(prev) + len(id))
		}
		delete(r.tombs, id) // a live record is no longer dead
		r.records[id] = rec
		r.bytes += int64(len(rec) + len(id))
		out.Put[id] = rec
	}

	if r.bytes > MaxSnapshotBytes {
		// Refusing here rather than at save time means the room never reaches a
		// state it cannot persist.
		return Applied{}, fmt.Errorf("%w: board would exceed %d bytes", ErrInvalid, MaxSnapshotBytes)
	}

	r.lastSeq[req.ClientID] = req.Seq
	if len(out.Put) == 0 && len(out.Remove) == 0 {
		// Everything was rejected; nothing changed, so nothing to broadcast and
		// no revision to burn.
		return Applied{Rev: r.rev, Rejected: rejected}, nil
	}
	r.rev++
	r.lastBy = by
	r.dirty = true
	if r.dirtyAt.IsZero() {
		r.dirtyAt = now
	}
	r.lastTouch = now
	r.trimTombs()
	sort.Strings(out.Remove)
	return Applied{Rev: r.rev, Patch: out, Rejected: rejected}, nil
}

// replace swaps the whole document — the full-document PUT arriving while a
// room is live (an import, or a user's acknowledged overwrite). It bumps the
// revision like any other change so subscribers know to resync, rather than
// leaving the room and the file disagreeing about what the board contains.
func (r *Room) replace(doc []byte, by UserRef, now time.Time) (int64, error) {
	var ld liveDoc
	if err := json.Unmarshal(doc, &ld); err != nil {
		return 0, fmt.Errorf("%w: document is unreadable", ErrInvalid)
	}
	r.records = make(map[string]json.RawMessage, len(ld.Store))
	r.bytes = 0
	for id, rec := range ld.Store {
		r.records[id] = rec
		r.bytes += int64(len(rec) + len(id))
	}
	r.schema = ld.Schema
	// Every previous record id is now suspect, so drop the tombstones: they
	// describe a document that no longer exists.
	r.tombs = make(map[string]int64)
	r.rev++
	r.lastBy = by
	r.dirty = true
	if r.dirtyAt.IsZero() {
		r.dirtyAt = now
	}
	r.lastTouch = now
	return r.rev, nil
}

// trimTombs forgets the oldest deletions once there are too many to be worth
// remembering. At worst a very stale client resurrects a shape — exactly what
// would happen with no tombstones at all, so the failure mode never gets worse
// than the behaviour this replaced.
func (r *Room) trimTombs() {
	if len(r.tombs) <= MaxTombstones {
		return
	}
	type tomb struct {
		id  string
		rev int64
	}
	all := make([]tomb, 0, len(r.tombs))
	for id, rev := range r.tombs {
		all = append(all, tomb{id, rev})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].rev < all[j].rev })
	for _, t := range all[:len(all)-MaxTombstones] {
		delete(r.tombs, t.id)
	}
}

func isShape(id string) bool   { return len(id) > 6 && id[:6] == "shape:" }
func isBinding(id string) bool { return len(id) > 8 && id[:8] == "binding:" }
