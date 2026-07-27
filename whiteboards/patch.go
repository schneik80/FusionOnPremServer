package whiteboards

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A tldraw document is `{"store": {"<recordId>": {…}}, "schema": {…}}`, and a
// tldraw RecordsDiff is therefore nothing more than a set of puts and removes
// keyed by record id. That is the whole reason live editing can work here: the
// server applies patches to a map of OPAQUE JSON and never parses tldraw's
// schema, keeping the promise the document endpoint already makes.
//
// The wire form folds the diff's `added` and `updated` into one `put` map and
// drops the `from` half of each update: applying a diff only ever writes the
// new value, so carrying the old one would double the bytes and invite a class
// of "did we send the right `from`" bugs for nothing.

// DocPatch caps. A board is capped at MaxSnapshotBytes as a whole; these bound a
// single exchange so one enormous paste can't stall every subscriber on the
// board (tldraw stores pasted images as base64 data URLs inside the document,
// so a screenshot really is megabytes of record).
const (
	MaxPatchBytes      = 4 << 20 // one patch on the wire
	MaxRecordBytes     = 1 << 20 // one record within it
	MaxRecordsPerPatch = 2000
	// MaxTombstones bounds the delete-wins memory. Beyond it the oldest are
	// forgotten, which at worst lets a very stale client resurrect a shape —
	// the pre-tombstone behaviour, not something worse.
	MaxTombstones = 4096
)

// docRecordPrefixes is the document scope as it may cross the wire. Session
// records (instance, camera, pointer, page state) and presence are per-user
// view state: a client that sent one would move every peer's camera or paint a
// cursor into the saved document, so they are refused here rather than trusted
// to the client's listener filter.
var docRecordPrefixes = []string{"document:", "page:", "shape:", "asset:", "binding:"}

func isDocRecord(id string) bool {
	for _, p := range docRecordPrefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// isStructural reports whether a record id names something whose loss or
// duplication breaks the whole document rather than one shape. These are rare
// and user-initiated, so they get a stricter concurrency rule (see Room.Apply).
func isStructural(id string) bool {
	return strings.HasPrefix(id, "page:") || strings.HasPrefix(id, "document:")
}

// DocPatch is one RecordsDiff as it crosses the wire, already folded: added ∪
// updated(to) → Put, removed keys → Remove.
type DocPatch struct {
	Put    map[string]json.RawMessage `json:"put,omitempty"`
	Remove []string                   `json:"remove,omitempty"`
}

func (p DocPatch) empty() bool { return len(p.Put) == 0 && len(p.Remove) == 0 }

// DocPatchRequest is a client's submission. ClientID identifies the browser tab
// (not the user — one person may have two open), Seq is that client's own
// counter so a retried POST is recognised, and BaseRev is the revision the
// client had applied when it built the diff.
type DocPatchRequest struct {
	ClientID string `json:"clientId"`
	Seq      int64  `json:"seq"`
	BaseRev  int64  `json:"baseRev"`
	DocPatch
}

// Applied is the result of accepting a patch: the room's new revision and the
// patch to broadcast, which may be LARGER than what arrived (a shape removal
// drags its bindings with it) or smaller (a put refused by a tombstone).
type Applied struct {
	Rev      int64
	Patch    DocPatch
	Rejected []string
}

// validate checks a submission before it can touch a room: known record kinds,
// sane sizes, no empty ids. It deliberately does not look at record CONTENT —
// that is tldraw's business, and the client is the only thing that understands
// it.
func (r DocPatchRequest) validate() error {
	if r.ClientID == "" {
		return fmt.Errorf("%w: clientId is required", ErrInvalid)
	}
	if r.BaseRev < 0 {
		return fmt.Errorf("%w: baseRev must not be negative", ErrInvalid)
	}
	if r.DocPatch.empty() {
		return fmt.Errorf("%w: patch is empty", ErrInvalid)
	}
	if len(r.Put)+len(r.Remove) > MaxRecordsPerPatch {
		return fmt.Errorf("%w: patch touches more than %d records", ErrInvalid, MaxRecordsPerPatch)
	}
	total := 0
	for id, rec := range r.Put {
		if err := checkRecordID(id); err != nil {
			return err
		}
		if len(rec) > MaxRecordBytes {
			return fmt.Errorf("%w: record %q exceeds %d bytes", ErrInvalid, id, MaxRecordBytes)
		}
		if !json.Valid(rec) {
			return fmt.Errorf("%w: record %q is not valid JSON", ErrInvalid, id)
		}
		total += len(rec) + len(id)
	}
	for _, id := range r.Remove {
		if err := checkRecordID(id); err != nil {
			return err
		}
		total += len(id)
	}
	if total > MaxPatchBytes {
		return fmt.Errorf("%w: patch exceeds %d bytes", ErrInvalid, MaxPatchBytes)
	}
	return nil
}

func checkRecordID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty record id", ErrInvalid)
	}
	if !isDocRecord(id) {
		return fmt.Errorf("%w: %q is not a document record", ErrInvalid, id)
	}
	return nil
}

// bindingEnds is the one place the server looks INSIDE a record, and only at
// binding records: a binding names the two shapes it joins, and when a shape
// goes its bindings must go with it or peers are left with an arrow pointing at
// nothing — which tldraw's own validation then rejects, forcing a full resync.
// A deliberate, minimal concession to tldraw's schema; nothing else is decoded.
type bindingEnds struct {
	FromID string `json:"fromId"`
	ToID   string `json:"toId"`
}

func bindingTouches(rec json.RawMessage, shapeIDs map[string]struct{}) bool {
	var b bindingEnds
	if err := json.Unmarshal(rec, &b); err != nil {
		return false
	}
	if b.FromID != "" {
		if _, ok := shapeIDs[b.FromID]; ok {
			return true
		}
	}
	if b.ToID != "" {
		if _, ok := shapeIDs[b.ToID]; ok {
			return true
		}
	}
	return false
}
