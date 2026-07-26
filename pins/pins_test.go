package pins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTempConfigDir points the config.Dir-backed pins helpers at a
// temp dir for the duration of the test. The package picks up the home
// directory via os.UserHomeDir, so we override HOME (and USERPROFILE on
// Windows runners) and t.Cleanup restores them automatically.
func withTempConfigDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows fallback used by os.UserHomeDir
	// Create the expected nested config dir so the pins helpers don't
	// have to MkdirAll on every call — matches what config.Dir does.
	dir := filepath.Join(home, ".config", "fusionlocalserver")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("setup config dir: %v", err)
	}
	return dir
}

// pinsPath is the hub's pins file inside its profile directory. The hub ids
// used by these tests are already slug-safe, so the slug equals the id.
func pinsPath(dir, hub string) string {
	return filepath.Join(dir, "hubs", hub, "pins-"+hub+".json")
}

// writePinsFile writes raw bytes at the hub's pins path, creating the profile
// directory (tests seed files before any Save has created it).
func writePinsFile(t *testing.T, dir, hub string, data []byte) string {
	t.Helper()
	path := pinsPath(dir, hub)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir profile: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write pins file: %v", err)
	}
	return path
}

func TestSanitizeHubID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"alnum passthrough", "abcXYZ123", "abcXYZ123"},
		{"empty becomes unset", "", "_unset"},
		{"URN colons replaced", "urn:adsk.ace:prod.scope:abc", "urn_adsk.ace_prod.scope_abc"},
		{"slashes replaced", "a/b/c", "a_b_c"},
		{"spaces and special chars replaced", "hello world! @#", "hello_world____"},
		{"dot/dash/underscore preserved", "ab.cd-ef_gh", "ab.cd-ef_gh"},
		{"capped at 120 chars", strings.Repeat("x", 200), strings.Repeat("x", 120)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeHubID(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeHubID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLoad_AbsentFileReturnsEmpty(t *testing.T) {
	withTempConfigDir(t)
	got, err := Load("urn:adsk.ace:prod.scope:abc")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %+v", got)
	}
}

func TestLoad_CorruptFileReturnsEmpty(t *testing.T) {
	dir := withTempConfigDir(t)
	// File exists but isn't valid JSON.
	writePinsFile(t, dir, "hub_a", []byte("{not json"))
	got, err := Load("hub_a")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty on corrupt, got %+v", got)
	}
}

func TestSave_LoadRoundTrip(t *testing.T) {
	withTempConfigDir(t)
	const hubID = "urn:adsk.ace:prod.scope:test"
	in := []Pin{
		{ID: "urn:item:1", Name: "Design Alpha", Kind: "design", HubID: hubID, ProjectID: "p1", ProjectAltID: "ap1", PinnedAt: time.Now().UTC().Truncate(time.Second)},
		{ID: "urn:item:2", Name: "Folder Beta", Kind: "folder", HubID: hubID, FolderPath: []FolderRef{{ID: "f1", Name: "Beta"}}, PinnedAt: time.Now().UTC().Truncate(time.Second)},
	}
	if err := Save(hubID, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(hubID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("len = %d, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i].ID != in[i].ID || got[i].Name != in[i].Name || got[i].Kind != in[i].Kind {
			t.Errorf("pin[%d] = %+v, want %+v", i, got[i], in[i])
		}
	}
}

func TestSave_PerHubIsolation(t *testing.T) {
	withTempConfigDir(t)
	hubA := "hub-a"
	hubB := "hub-b"
	if err := Save(hubA, []Pin{{ID: "urn:1", Name: "A1", Kind: "design", HubID: hubA}}); err != nil {
		t.Fatalf("save A: %v", err)
	}
	if err := Save(hubB, []Pin{{ID: "urn:2", Name: "B1", Kind: "design", HubID: hubB}}); err != nil {
		t.Fatalf("save B: %v", err)
	}
	gotA, _ := Load(hubA)
	gotB, _ := Load(hubB)
	if len(gotA) != 1 || gotA[0].Name != "A1" {
		t.Errorf("hubA pins = %+v, want one A1", gotA)
	}
	if len(gotB) != 1 || gotB[0].Name != "B1" {
		t.Errorf("hubB pins = %+v, want one B1", gotB)
	}
}

func TestSave_FileMode0600(t *testing.T) {
	dir := withTempConfigDir(t)
	if err := Save("hub", []Pin{{ID: "x", Name: "x", Kind: "design", HubID: "hub"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(pinsPath(dir, "hub"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Mode bits matter for token-adjacent files. Check the low 9 bits.
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file mode = %#o, want 0600", perm)
	}
}

func TestAdd_DedupesByID(t *testing.T) {
	ps := []Pin{{ID: "x", Name: "First", Kind: "design", HubID: "h"}}
	ps = Add(ps, Pin{ID: "x", Name: "Second", Kind: "design", HubID: "h"})
	if len(ps) != 1 {
		t.Fatalf("len = %d, want 1 after duplicate Add", len(ps))
	}
	if ps[0].Name != "First" {
		t.Errorf("Name = %q, want First (existing wins on dedup)", ps[0].Name)
	}
}

func TestAdd_PrependsNew(t *testing.T) {
	ps := []Pin{{ID: "a", Name: "A", Kind: "design", HubID: "h"}}
	ps = Add(ps, Pin{ID: "b", Name: "B", Kind: "design", HubID: "h"})
	if len(ps) != 2 {
		t.Fatalf("len = %d, want 2", len(ps))
	}
	if ps[0].ID != "b" {
		t.Errorf("most recent should be first, got order %v", []string{ps[0].ID, ps[1].ID})
	}
	if ps[0].PinnedAt.IsZero() {
		t.Errorf("PinnedAt should be set after Add")
	}
}

func TestRemove_ByID(t *testing.T) {
	ps := []Pin{
		{ID: "a", Kind: "design", HubID: "h"},
		{ID: "b", Kind: "design", HubID: "h"},
		{ID: "c", Kind: "design", HubID: "h"},
	}
	got := Remove(ps, "b")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, p := range got {
		if p.ID == "b" {
			t.Errorf("Remove kept the target ID")
		}
	}
}

func TestIsPinnable(t *testing.T) {
	cases := map[string]bool{
		"project":    true,
		"folder":     true,
		"design":     true,
		"drawing":    true,
		"configured": true,
		"hub":        false,
		"":           false,
		"unknown":    false,
	}
	for kind, want := range cases {
		if got := IsPinnable(kind); got != want {
			t.Errorf("IsPinnable(%q) = %v, want %v", kind, got, want)
		}
	}
}

// ---- v1 envelope + migration posture ----

func TestSaveWritesV1Envelope(t *testing.T) {
	dir := withTempConfigDir(t)
	hub := "hubV1"
	if err := Save(hub, []Pin{{ID: "p1", Name: "One", Kind: "design", HubID: hub}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(pinsPath(dir, hub))
	if err != nil {
		t.Fatal(err)
	}
	var pf struct {
		Version int `json:"version"`
		Schema  struct {
			CreatedAt        time.Time `json:"createdAt"`
			UpdatedByVersion string    `json:"updatedByVersion"`
		} `json:"schema"`
		Pins []Pin `json:"pins"`
	}
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatalf("not an envelope: %s", data)
	}
	if pf.Version != fileVersion || len(pf.Pins) != 1 || pf.Schema.CreatedAt.IsZero() || pf.Schema.UpdatedByVersion == "" {
		t.Errorf("envelope = %+v", pf)
	}
	got, err := Load(hub)
	if err != nil || len(got) != 1 || got[0].ID != "p1" {
		t.Errorf("Load = %v, %v", got, err)
	}
}

func TestLoadLegacyArrayMigrates(t *testing.T) {
	dir := withTempConfigDir(t)
	hub := "hubLegacy"
	legacy := `[{"id":"p1","name":"Old","kind":"design","hub_id":"hubLegacy","pinned_at":"2024-01-02T03:04:05Z"}]`
	path := writePinsFile(t, dir, hub, []byte(legacy))
	got, err := Load(hub)
	if err != nil || len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("Load legacy = %v, %v", got, err)
	}
	// Snapshot of the pre-envelope bytes exists.
	snap, err := os.ReadFile(path + ".v0.bak")
	if err != nil || string(snap) != legacy {
		t.Errorf("v0 snapshot missing/wrong: %v", err)
	}
	// Next save rewrites in the envelope; reload still works.
	if err := Save(hub, got); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"version": 1`) {
		t.Errorf("not upgraded on save: %s", data)
	}
	again, err := Load(hub)
	if err != nil || len(again) != 1 {
		t.Errorf("reload = %v, %v", again, err)
	}
}

func TestLoadFutureVersionRefused(t *testing.T) {
	dir := withTempConfigDir(t)
	hub := "hubFuture"
	writePinsFile(t, dir, hub, []byte(`{"version": 99, "pins": []}`))
	if _, err := Load(hub); err == nil {
		t.Fatal("expected ErrFutureVersion")
	}
}

func TestLoadCorruptRecovers(t *testing.T) {
	dir := withTempConfigDir(t)
	hub := "hubCorrupt"
	path := writePinsFile(t, dir, hub, []byte(`{"version": 1, "pins": [truncated`))
	got, err := Load(hub)
	if err != nil || len(got) != 0 {
		t.Fatalf("Load corrupt = %v, %v", got, err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Error("corrupt file not preserved as .bak")
	}
}

func TestSavePreservesBirthStamp(t *testing.T) {
	dir := withTempConfigDir(t)
	hub := "hubStamp"
	if err := Save(hub, []Pin{{ID: "p1", HubID: hub}}); err != nil {
		t.Fatal(err)
	}
	path := pinsPath(dir, hub)
	var first struct {
		Schema struct {
			CreatedAt time.Time `json:"createdAt"`
		} `json:"schema"`
	}
	data, _ := os.ReadFile(path)
	_ = json.Unmarshal(data, &first)

	time.Sleep(5 * time.Millisecond)
	if err := Save(hub, []Pin{{ID: "p1", HubID: hub}, {ID: "p2", HubID: hub}}); err != nil {
		t.Fatal(err)
	}
	var second struct {
		Schema struct {
			CreatedAt time.Time `json:"createdAt"`
			UpdatedAt time.Time `json:"updatedAt"`
		} `json:"schema"`
	}
	data, _ = os.ReadFile(path)
	_ = json.Unmarshal(data, &second)
	if !second.Schema.CreatedAt.Equal(first.Schema.CreatedAt) {
		t.Errorf("birth stamp changed across saves: %v → %v", first.Schema.CreatedAt, second.Schema.CreatedAt)
	}
	if !second.Schema.UpdatedAt.After(second.Schema.CreatedAt) {
		t.Errorf("updatedAt not touched: %+v", second.Schema)
	}
}
