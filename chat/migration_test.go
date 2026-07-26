package chat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const chatMigProj = "urn:proj:chatmig"

const chatMetaGoldenV1 = `{
  "version": 1,
  "projectId": "urn:proj:chatmig",
  "eventEpoch": 4,
  "nextChannelId": 2,
  "channels": [
    {"id": "c1", "name": "general", "topic": "", "createdBy": "u1",
     "createdAt": "2025-04-01T00:00:00Z", "isRoot": true}
  ]
}`

const chatCursorsGoldenV1 = `{
  "version": 1,
  "cursors": {"u1": {"c1": 7}}
}`

func TestChatMigrationV1ToV2(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, sanitizeID(chatMigProj))
	if err := os.MkdirAll(pdir, 0700); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(pdir, "meta.json")
	if err := os.WriteFile(metaPath, []byte(chatMetaGoldenV1), 0600); err != nil {
		t.Fatal(err)
	}
	cursorsPath := filepath.Join(pdir, "cursors.json")
	if err := os.WriteFile(cursorsPath, []byte(chatCursorsGoldenV1), 0600); err != nil {
		t.Fatal(err)
	}
	s := newStoreAt(t, dir)

	chans, err := s.Channels(chatMigProj)
	if err != nil || len(chans) != 1 || chans[0].Name != "general" {
		t.Fatalf("Channels after migration = %v, %v", chans, err)
	}
	if _, err := os.Stat(metaPath + ".v1.bak"); err != nil {
		t.Errorf("no meta pre-migration snapshot: %v", err)
	}
	// Loading a project bumps EventEpoch and persists — the write carries the
	// v2 envelope + stamp.
	data, _ := os.ReadFile(metaPath)
	var meta struct {
		Version    int   `json:"version"`
		EventEpoch int64 `json:"eventEpoch"`
		Schema     struct {
			CreatedByVersion string `json:"createdByVersion"`
		} `json:"schema"`
	}
	_ = json.Unmarshal(data, &meta)
	if meta.Version != 2 || meta.Schema.CreatedByVersion != "pre-schema" {
		t.Errorf("persisted meta = %+v", meta)
	}
	if meta.EventEpoch <= 4 {
		t.Errorf("event epoch not bumped: %d", meta.EventEpoch)
	}

	// Cursors migrate on their lazy first touch and keep their data: with
	// lastRead 7 persisted in the golden fixture, unreads for u1 count from
	// seq 7, so an empty channel reports zero unread.
	unreads, err := s.Unreads(chatMigProj, "u1", chans)
	if err != nil || len(unreads) != 1 || unreads[0].LastReadSeq != 7 {
		t.Fatalf("Unreads after migration = %+v, %v", unreads, err)
	}
}

func TestChatMigrationFutureRefused(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, sanitizeID(chatMigProj))
	_ = os.MkdirAll(pdir, 0700)
	_ = os.WriteFile(filepath.Join(pdir, "meta.json"),
		[]byte(`{"version": 99, "eventEpoch": 1, "nextChannelId": 1, "channels": []}`), 0600)
	s := newStoreAt(t, dir)
	if _, err := s.Channels(chatMigProj); !errors.Is(err, ErrFutureVersion) {
		t.Errorf("err = %v, want ErrFutureVersion", err)
	}
}
