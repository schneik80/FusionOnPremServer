package chat

import (
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/docref"
)

// DocRefHit is one channel whose messages reference a document — the chat half
// of the Where-Used graph's local sources. Hits are aggregated per channel
// (not per message) so a long conversation about one part contributes a single
// node with a count, and the most recent mention rides along as the excerpt.
//
// IsPrivate and Members are carried out of the store so the CALLER can apply
// the channel ACL: this type is the raw scan result and is not safe to return
// to a user unfiltered.
type DocRefHit struct {
	ProjectID   string
	ChannelID   string
	ChannelName string
	IsPrivate   bool
	IsArchived  bool
	Members     []ChannelMember
	Count       int
	LastSeq     int64
	LastAuthor  string
	LastBody    string
	LastAt      time.Time
}

// FindDocRefs returns every channel in the given projects holding at least one
// live message that references itemID.
//
// The scan is the MessageActivity pattern (chat has no index — a channel is an
// append-only log): read each channel log once, whole. What keeps it cheap is
// the prefilter — a log that doesn't contain the document's lineage key is
// rejected on a raw substring test, so no JSON is parsed for the overwhelming
// majority of channels. Only a log that survives it is replayed, and the
// replay is the real one: an edit that removed the token drops the message, a
// deleted message never counts.
//
// Scoping matches the sibling stores (caller passes the projects the user may
// see; empty set yields nothing), and per-project failures are skipped rather
// than failing the lookup. Channel metadata is read straight off disk instead
// of through Channels(), which would lazily CREATE a root channel in every
// project it touched — a read-only scan must not write.
func (s *Store) FindDocRefs(projectIDs []string, itemID string) ([]DocRefHit, error) {
	out := []DocRefHit{}
	if len(projectIDs) == 0 || itemID == "" {
		return out, nil
	}
	seen := make(map[string]struct{}, len(projectIDs))
	for _, pid := range projectIDs {
		if pid == "" {
			continue
		}
		if _, dup := seen[pid]; dup {
			continue
		}
		seen[pid] = struct{}{}

		channels, err := s.channelsFromDisk(pid)
		if err != nil {
			continue // no chat data for this project, or unreadable — skip
		}
		for _, ch := range channels {
			if ch == nil {
				continue
			}
			hit, ok := scanChannelLog(s.logPath(pid, ch.ID), itemID)
			if !ok {
				continue
			}
			hit.ProjectID = pid
			hit.ChannelID = ch.ID
			hit.ChannelName = ch.Name
			hit.IsPrivate = ch.IsPrivate
			hit.IsArchived = ch.ArchivedAt != nil
			hit.Members = append([]ChannelMember(nil), ch.Members...)
			out = append(out, hit)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].LastAt.After(out[j].LastAt)
	})
	return out, nil
}

// channelsFromDisk reads a project's channel list without going through the
// lazily-initialising project state — no root-channel creation, no event-epoch
// bump, no lock held for the length of a scan. A project with no meta.json has
// no chat data at all.
func (s *Store) channelsFromDisk(projectID string) ([]*Channel, error) {
	data, err := os.ReadFile(s.metaPath(projectID))
	if err != nil {
		return nil, err
	}
	var meta projectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta.Version > metaVersion {
		return nil, ErrFutureVersion
	}
	return meta.Channels, nil
}

// scanChannelLog replays one channel's log and reports its references to
// itemID. Returns ok=false when the channel doesn't reference the document —
// including the cheap case where the raw bytes never mention it, which is why
// nothing is unmarshalled before the prefilter.
func scanChannelLog(path, itemID string) (DocRefHit, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DocRefHit{}, false
	}
	if !docref.MayContain(data, itemID) {
		return DocRefHit{}, false
	}

	// live tracks the current state of every message in the log, because a
	// match may arrive by way of an edit long after the create that carries
	// the author and timestamp.
	type msgState struct {
		author  string
		body    string
		at      time.Time
		deleted bool
	}
	live := map[int64]*msgState{}
	order := make([]int64, 0, 64)

	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // torn tail or a record this build can't read — skip
		}
		switch rec.Op {
		case opCreate:
			live[rec.Seq] = &msgState{author: rec.AuthorName, body: rec.Body, at: rec.At}
			order = append(order, rec.Seq)
		case opEdit:
			if m := live[rec.Target]; m != nil {
				m.body = rec.Body
			}
		case opDelete:
			if m := live[rec.Target]; m != nil {
				m.deleted = true
			}
		}
	}

	hit := DocRefHit{}
	for _, seq := range order {
		m := live[seq]
		if m == nil || m.deleted {
			continue
		}
		n := docref.Count(m.body, itemID)
		if n == 0 {
			continue
		}
		hit.Count += n
		// order is ascending by seq, so the last match wins — the newest
		// mention is the one worth showing.
		hit.LastSeq, hit.LastAuthor, hit.LastBody, hit.LastAt = seq, m.author, excerpt(m.body), m.at
	}
	if hit.Count == 0 {
		return DocRefHit{}, false
	}
	return hit, true
}

// maxExcerptRunes bounds the message excerpt carried back to the graph's
// tooltip. Enough to recognise the conversation, far short of shipping a
// channel's contents through a relationship lookup.
const maxExcerptRunes = 160

// excerpt trims a message body to a single-line preview, cut on a rune
// boundary (never mid-grapheme-sequence at the byte level) with the fls:
// tokens left in place — the UI decides how to render them.
func excerpt(body string) string {
	s := strings.Join(strings.Fields(body), " ")
	n := 0
	for i := range s {
		n++
		if n > maxExcerptRunes {
			return s[:i] + "…"
		}
	}
	return s
}
