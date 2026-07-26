package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schneik80/fusionlocalserver/chat"
)

func TestHubStores_LazyBuildLayoutAndReuse(t *testing.T) {
	dir := t.TempDir()
	hs := newHubStores(dir, chat.NewAuthorizer())
	t.Cleanup(hs.closeAll)

	// Nothing exists before the first get.
	if _, err := os.Stat(filepath.Join(dir, "hubs")); !os.IsNotExist(err) {
		t.Fatalf("hubs/ exists before any get: %v", err)
	}

	set, err := hs.get("urn:hub:a", "Hub A")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	root := filepath.Join(dir, "hubs", "urn_hub_a")
	if set.root != root || set.slug != "urn_hub_a" || set.hubID != "urn:hub:a" {
		t.Errorf("set = {root:%q slug:%q hubID:%q}", set.root, set.slug, set.hubID)
	}
	if set.chat == nil || set.tasks == nil || set.production == nil || set.whiteboards == nil || set.chatHub == nil {
		t.Fatal("set has nil stores")
	}

	// hub.json identifies the profile.
	data, err := os.ReadFile(filepath.Join(root, "hub.json"))
	if err != nil {
		t.Fatalf("hub.json: %v", err)
	}
	var f hubJSONFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f.HubID != "urn:hub:a" || f.HubName != "Hub A" || f.CreatedAt.IsZero() {
		t.Errorf("hub.json = %+v", f)
	}

	// Second get returns the SAME set (no rebuild).
	again, err := hs.get("urn:hub:a", "Hub A")
	if err != nil || again != set {
		t.Errorf("second get = %p, %v; want the cached %p", again, err, set)
	}

	// Two hubs → two disjoint sets and both slugs on disk.
	other, err := hs.get("urn:hub:b", "Hub B")
	if err != nil {
		t.Fatal(err)
	}
	if other == set || other.root == set.root || other.chatHub == set.chatHub {
		t.Error("distinct hubs share a store set")
	}
	slugs := hs.diskHubSlugs()
	if len(slugs) != 2 || slugs[0] != "urn_hub_a" || slugs[1] != "urn_hub_b" {
		t.Errorf("diskHubSlugs = %v", slugs)
	}
	if got := len(hs.snapshotAll()); got != 2 {
		t.Errorf("snapshotAll len = %d, want 2", got)
	}
}

func TestHubStores_SlugCollisionHardRefusal(t *testing.T) {
	dir := t.TempDir()
	hs := newHubStores(dir, chat.NewAuthorizer())
	t.Cleanup(hs.closeAll)

	// "hub:x" and "hub_x" sanitize to the same slug.
	if _, err := hs.get("hub:x", "First"); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.get("hub_x", "Impostor"); !errors.Is(err, errHubCollision) {
		t.Fatalf("in-memory collision err = %v, want errHubCollision", err)
	}

	// Cold-disk face of the same guard: a fresh hubStores over a profile
	// whose hub.json names a different hub refuses to open it.
	hs2 := newHubStores(dir, chat.NewAuthorizer())
	t.Cleanup(hs2.closeAll)
	if _, err := hs2.get("hub_x", "Impostor"); !errors.Is(err, errHubCollision) {
		t.Fatalf("on-disk collision err = %v, want errHubCollision", err)
	}
	// The rightful owner still opens.
	if _, err := hs2.get("hub:x", "First"); err != nil {
		t.Fatalf("rightful owner refused: %v", err)
	}
}

func TestHubStores_GetBySlug(t *testing.T) {
	dir := t.TempDir()
	hs := newHubStores(dir, chat.NewAuthorizer())
	t.Cleanup(hs.closeAll)

	if _, err := hs.get("hub-1", "Hub One"); err != nil {
		t.Fatal(err)
	}
	set, err := hs.getBySlug("hub-1")
	if err != nil || set.hubID != "hub-1" {
		t.Fatalf("getBySlug = %+v, %v", set, err)
	}
	// A profile dir without hub.json refuses.
	if err := os.MkdirAll(filepath.Join(dir, "hubs", "orphan"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.getBySlug("orphan"); err == nil {
		t.Fatal("getBySlug(orphan) succeeded, want refusal")
	}
}

func TestHubStores_NilSafe(t *testing.T) {
	var hs *hubStores
	if _, err := hs.get("h", "H"); err == nil {
		t.Error("nil hubStores get succeeded")
	}
	if got := hs.snapshotAll(); got != nil {
		t.Errorf("nil snapshotAll = %v", got)
	}
	if got := hs.diskHubSlugs(); got != nil {
		t.Errorf("nil diskHubSlugs = %v", got)
	}
}

func TestHubBackupConfig_RoundTripCorruptAndFuture(t *testing.T) {
	root := t.TempDir()

	// Absent → zero config, no error.
	cfg, err := loadHubBackupConfig(root)
	if err != nil || cfg.BackupDir != "" || cfg.BackupEnabled {
		t.Fatalf("absent = %+v, %v", cfg, err)
	}

	// Round trip, with version + schema stamped.
	in := hubBackupConfig{BackupDir: "/mnt/backups", BackupTime: "04:15", BackupEnabled: true}
	if err := saveHubBackupConfig(root, in); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadHubBackupConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackupDir != "/mnt/backups" || cfg.BackupTime != "04:15" || !cfg.BackupEnabled ||
		cfg.Version != hubBackupConfigVersion || cfg.Schema.CreatedAt.IsZero() {
		t.Errorf("round trip = %+v", cfg)
	}
	birth := cfg.Schema.CreatedAt

	// Rewrite preserves the birth stamp.
	if err := saveHubBackupConfig(root, hubBackupConfig{BackupDir: "/elsewhere"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = loadHubBackupConfig(root)
	if !cfg.Schema.CreatedAt.Equal(birth) {
		t.Errorf("birth stamp changed across saves: %v → %v", birth, cfg.Schema.CreatedAt)
	}

	// Corrupt → .bak + zero config.
	path := filepath.Join(root, hubBackupConfigFile)
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadHubBackupConfig(root)
	if err != nil || cfg.BackupDir != "" {
		t.Fatalf("corrupt = %+v, %v; want zero config", cfg, err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("corrupt file not preserved as .bak: %v", err)
	}

	// Future version → refusal.
	if err := os.WriteFile(path, []byte(`{"version": 99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHubBackupConfig(root); !errors.Is(err, errBackupCfgFuture) {
		t.Fatalf("future version err = %v, want errBackupCfgFuture", err)
	}
}
