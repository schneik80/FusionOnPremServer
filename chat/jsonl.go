package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// recordVersion is the current schema version of a message-log record. Like
// meta.json's version gate, a record written by a newer build makes the
// whole channel refuse to load rather than risk misreading it.
const recordVersion = 1

// Log record operations. A channel's msg-<id>.jsonl is an append-only event
// log: message state is derived by replaying it, so nothing is ever
// rewritten in place and a crash can at worst lose the final, partially
// written line (which replay detects and trims).
const (
	opCreate  = "create"
	opEdit    = "edit"
	opDelete  = "delete"
	opReact   = "react"
	opUnreact = "unreact"
)

// record is one line of a channel's message log. Fields are op-specific:
// create uses Seq/ThreadRoot/Author*/ClientMsgID/Body; edit and delete
// address Target; react/unreact address Target with UserID+Emoji.
type record struct {
	V           int       `json:"v"`
	Op          string    `json:"op"`
	Seq         int64     `json:"seq,omitempty"`
	Target      int64     `json:"target,omitempty"`
	ThreadRoot  int64     `json:"threadRoot,omitempty"`
	AuthorID    string    `json:"authorId,omitempty"`
	AuthorName  string    `json:"authorName,omitempty"`
	ClientMsgID string    `json:"clientMsgId,omitempty"`
	UserID      string    `json:"userId,omitempty"`
	Emoji       string    `json:"emoji,omitempty"`
	Body        string    `json:"body,omitempty"`
	At          time.Time `json:"at"`
}

// appendRecord writes rec as one newline-terminated JSON line in a single
// Write call, so concurrent readers never observe a torn line and a crash
// leaves at most one unterminated tail.
func appendRecord(f *os.File, rec record) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("chat: appending to %s: %w", f.Name(), err)
	}
	return nil
}

// replayLog reads every valid record from path. An absent file is an empty
// log. An unterminated or undecodable tail — the signature of a crash mid-
// append — is trimmed off the file so the next append starts a clean line;
// if valid records follow the bad line (true corruption, not a crash tail)
// the original is first preserved as .bak. Records from a newer build fail
// with ErrFutureVersion.
func replayLog(path string) ([]record, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat: reading %s: %w", path, err)
	}

	var recs []record
	validEnd := 0
	rest := data
	corrupt := false
	for len(rest) > 0 {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			// Unterminated tail: crash mid-append. Trim below.
			break
		}
		line := rest[:nl]
		rest = rest[nl+1:]
		if len(bytes.TrimSpace(line)) == 0 {
			validEnd = len(data) - len(rest)
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			corrupt = true
			break
		}
		if rec.V > recordVersion {
			return nil, fmt.Errorf("%w: %s record v%d > v%d", ErrFutureVersion, path, rec.V, recordVersion)
		}
		// Per-record migration seam: when recordVersion moves past 1, older
		// records upgrade here (migrateRecord), and the log is compacted —
		// rewritten in the new shape — only after the whole replay succeeds,
		// never mid-file. No steps exist at v1, so this is the identity.
		rec = migrateRecord(rec)
		recs = append(recs, rec)
		validEnd = len(data) - len(rest)
	}

	if validEnd < len(data) {
		if corrupt {
			// Something other than a crash tail — keep the evidence.
			_ = os.WriteFile(path+".bak", data, 0600)
		}
		if err := os.Truncate(path, int64(validEnd)); err != nil {
			return nil, fmt.Errorf("chat: trimming damaged tail of %s: %w", path, err)
		}
	}
	return recs, nil
}

// migrateRecord upgrades a decoded record from an older version to the
// current recordVersion. The identity while recordVersion == 1; when a v2
// record shape lands, the upgrade logic goes here and replayLog's caller
// compacts the file after a fully successful migrating replay.
func migrateRecord(rec record) record {
	return rec
}

// replayMessages folds a record log into message state: ordered messages,
// seq/clientMsgId indexes, the next free seq, and the derived thread
// counters (ReplyCount counts live replies; deleting a reply decrements it).
func replayMessages(recs []record) (msgs []*Message, bySeq map[int64]*Message, byClient map[string]*Message, nextSeq int64) {
	bySeq = make(map[int64]*Message)
	byClient = make(map[string]*Message)
	nextSeq = 1
	for _, rec := range recs {
		switch rec.Op {
		case opCreate:
			m := &Message{
				Seq:         rec.Seq,
				ThreadRoot:  rec.ThreadRoot,
				AuthorID:    rec.AuthorID,
				AuthorName:  rec.AuthorName,
				ClientMsgID: rec.ClientMsgID,
				Body:        rec.Body,
				CreatedAt:   rec.At,
				Reactions:   []Reaction{},
			}
			msgs = append(msgs, m)
			bySeq[m.Seq] = m
			if m.ClientMsgID != "" {
				byClient[m.ClientMsgID] = m
			}
			if m.Seq >= nextSeq {
				nextSeq = m.Seq + 1
			}
			if root := bySeq[m.ThreadRoot]; m.ThreadRoot != 0 && root != nil {
				root.ReplyCount++
				at := rec.At
				root.LastReplyAt = &at
			}
		case opEdit:
			if m := bySeq[rec.Target]; m != nil && m.DeletedAt == nil {
				m.Body = rec.Body
				at := rec.At
				m.EditedAt = &at
			}
		case opDelete:
			if m := bySeq[rec.Target]; m != nil && m.DeletedAt == nil {
				at := rec.At
				m.DeletedAt = &at
				m.Body = ""
				m.Reactions = []Reaction{}
				if root := bySeq[m.ThreadRoot]; m.ThreadRoot != 0 && root != nil && root.ReplyCount > 0 {
					root.ReplyCount--
				}
			}
		case opReact:
			if m := bySeq[rec.Target]; m != nil && m.DeletedAt == nil {
				if !hasReaction(m.Reactions, rec.UserID, rec.Emoji) {
					m.Reactions = append(m.Reactions, Reaction{UserID: rec.UserID, Emoji: rec.Emoji, At: rec.At})
				}
			}
		case opUnreact:
			if m := bySeq[rec.Target]; m != nil {
				m.Reactions = removeReaction(m.Reactions, rec.UserID, rec.Emoji)
			}
		}
	}
	return msgs, bySeq, byClient, nextSeq
}

func hasReaction(rs []Reaction, userID, emoji string) bool {
	for _, r := range rs {
		if r.UserID == userID && r.Emoji == emoji {
			return true
		}
	}
	return false
}

func removeReaction(rs []Reaction, userID, emoji string) []Reaction {
	out := rs[:0:0]
	for _, r := range rs {
		if r.UserID != userID || r.Emoji != emoji {
			out = append(out, r)
		}
	}
	if out == nil {
		return []Reaction{}
	}
	return out
}
