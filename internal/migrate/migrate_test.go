package migrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
)

func testRegistry() *Registry {
	r := NewRegistry("test", 3)
	r.Register(0, func(raw map[string]any) (map[string]any, error) {
		raw["a"] = true
		return raw, nil
	})
	r.Register(1, func(raw map[string]any) (map[string]any, error) {
		raw["b"] = true
		return raw, nil
	})
	r.Register(2, func(raw map[string]any) (map[string]any, error) {
		raw["c"] = true
		return raw, nil
	})
	return r
}

func TestApplyChainsSteps(t *testing.T) {
	r := testRegistry()
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	src := []byte(`{"version": 0, "keep": "me"}`)
	if err := os.WriteFile(path, src, 0600); err != nil {
		t.Fatal(err)
	}

	out, migrated, err := r.Apply(path, src)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != float64(3) || got["a"] != true || got["b"] != true || got["c"] != true {
		t.Errorf("chained result = %v", got)
	}
	if got["keep"] != "me" {
		t.Errorf("existing data lost: %v", got)
	}
	// Pre-migration snapshot captured the ORIGINAL bytes.
	snap, err := os.ReadFile(path + ".v0.bak")
	if err != nil {
		t.Fatalf("no snapshot: %v", err)
	}
	if string(snap) != string(src) {
		t.Errorf("snapshot = %s", snap)
	}
}

func TestApplyMidChain(t *testing.T) {
	r := testRegistry()
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	src := []byte(`{"version": 2}`)
	out, migrated, err := r.Apply(path, src)
	if err != nil || !migrated {
		t.Fatalf("Apply: %v migrated=%v", err, migrated)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["version"] != float64(3) || got["c"] != true || got["a"] != nil {
		t.Errorf("mid-chain result = %v", got)
	}
	if _, err := os.Stat(path + ".v2.bak"); err != nil {
		t.Error("expected .v2.bak snapshot")
	}
}

func TestApplyAtTargetIsNoop(t *testing.T) {
	r := testRegistry()
	src := []byte(`{"version": 3, "x": 1}`)
	out, migrated, err := r.Apply(filepath.Join(t.TempDir(), "d.json"), src)
	if err != nil {
		t.Fatal(err)
	}
	if migrated || string(out) != string(src) {
		t.Errorf("noop expected, migrated=%v out=%s", migrated, out)
	}
}

func TestApplyFutureVersionRefused(t *testing.T) {
	r := testRegistry()
	_, _, err := r.Apply(filepath.Join(t.TempDir(), "d.json"), []byte(`{"version": 9}`))
	if !errors.Is(err, ErrFutureVersion) {
		t.Errorf("err = %v, want ErrFutureVersion", err)
	}
}

func TestApplyMissingStepFails(t *testing.T) {
	r := NewRegistry("gappy", 2)
	r.Register(0, func(raw map[string]any) (map[string]any, error) { return raw, nil })
	// No step 1→2.
	_, _, err := r.Apply(filepath.Join(t.TempDir(), "d.json"), []byte(`{"version": 0}`))
	if err == nil {
		t.Fatal("expected missing-step error")
	}
}

func TestPeek(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`{"version": 2}`, 2},
		{`{"pins": []}`, 0}, // pre-versioning file
		{`{}`, 0},
	}
	for _, c := range cases {
		got, err := Peek([]byte(c.in))
		if err != nil || got != c.want {
			t.Errorf("Peek(%s) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	if _, err := Peek([]byte(`not json`)); err == nil {
		t.Error("expected error for non-JSON")
	}
}

func TestSnapshotNotOverwritten(t *testing.T) {
	r := testRegistry()
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	first := []byte(`{"version": 0, "origin": "first"}`)
	if _, _, err := r.Apply(path, first); err != nil {
		t.Fatal(err)
	}
	// A second apply of different v0 bytes must not clobber the snapshot.
	if _, _, err := r.Apply(path, []byte(`{"version": 0, "origin": "second"}`)); err != nil {
		t.Fatal(err)
	}
	snap, _ := os.ReadFile(path + ".v0.bak")
	var got map[string]any
	_ = json.Unmarshal(snap, &got)
	if got["origin"] != "first" {
		t.Errorf("snapshot clobbered: %v", got)
	}
}

func TestAtomicfileWriteAndPerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := atomicfile.WriteFile(path, []byte(`{"ok":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != `{"ok":true}` {
		t.Fatalf("read back: %s %v", data, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("perm = %v", info.Mode().Perm())
	}
	// Overwrite works and leaves no temp litter.
	if err := atomicfile.WriteFile(path, []byte(`{"v":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp litter: %v", entries)
	}
}
