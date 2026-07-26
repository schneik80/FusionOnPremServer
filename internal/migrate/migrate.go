// Package migrate is the shared schema-migration framework for the local
// JSON stores. Each store owns a Registry declaring its current (Target)
// version and one Step per upgrade hop; Apply chains v(n)→v(n+1) steps over
// the raw decoded document until it reaches Target, snapshotting the
// pre-migration bytes to <file>.v<N>.bak first so no upgrade can lose data.
//
// Steps operate on map[string]any rather than typed structs because an old
// file's shape, by definition, may not unmarshal into the current types.
// Newer-than-Target files always refuse with ErrFutureVersion — never
// rewrite what we don't understand (the guard every store already had).
package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
)

// ErrFutureVersion mirrors the stores' sentinel: the file was written by a
// newer build. Callers wrap it with their own store-specific sentinel.
var ErrFutureVersion = errors.New("migrate: data written by a newer version")

// Step upgrades a raw document from version n to n+1. It may mutate and
// return its argument.
type Step func(raw map[string]any) (map[string]any, error)

// Registry holds one store's migration table.
type Registry struct {
	Store  string // for error text, e.g. "tasks"
	Target int    // the version this build reads and writes
	steps  map[int]Step
}

// NewRegistry returns a registry with no steps (a v-Target-only store).
func NewRegistry(store string, target int) *Registry {
	return &Registry{Store: store, Target: target, steps: make(map[int]Step)}
}

// Register adds the step that upgrades from version `from` to `from+1`.
func (r *Registry) Register(from int, s Step) {
	if _, dup := r.steps[from]; dup {
		panic(fmt.Sprintf("migrate: duplicate step v%d for %s", from, r.Store))
	}
	r.steps[from] = s
}

// Peek reads just the version field from a store file's bytes. A missing or
// non-numeric version reports 0 — the pre-versioning convention (pins).
func Peek(data []byte) (int, error) {
	var probe struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return 0, fmt.Errorf("migrate: peeking version: %w", err)
	}
	if probe.Version == nil {
		return 0, nil
	}
	return *probe.Version, nil
}

// Apply migrates data (read from path — path is used for the snapshot) up
// to Target. Returns the (possibly rewritten) bytes and whether any step
// ran. The caller persists the result through its normal atomic save on
// first write; Apply itself only writes the pre-migration snapshot.
func (r *Registry) Apply(path string, data []byte) (out []byte, migrated bool, err error) {
	v, err := Peek(data)
	if err != nil {
		return nil, false, err
	}
	if v > r.Target {
		return nil, false, fmt.Errorf("%w: %s v%d > v%d", ErrFutureVersion, r.Store, v, r.Target)
	}
	if v == r.Target {
		return data, false, nil
	}
	// Snapshot the pre-migration bytes beside the file. Never overwrite an
	// existing snapshot for the same version — first capture wins.
	snap := fmt.Sprintf("%s.v%d.bak", path, v)
	if _, statErr := os.Stat(snap); errors.Is(statErr, os.ErrNotExist) {
		if werr := atomicfile.WriteFile(snap, data, 0600); werr != nil {
			return nil, false, fmt.Errorf("migrate: writing %s snapshot: %w", r.Store, werr)
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, fmt.Errorf("migrate: decoding %s v%d: %w", r.Store, v, err)
	}
	for cur := v; cur < r.Target; cur++ {
		step, ok := r.steps[cur]
		if !ok {
			return nil, false, fmt.Errorf("migrate: %s has no step v%d→v%d", r.Store, cur, cur+1)
		}
		raw, err = step(raw)
		if err != nil {
			return nil, false, fmt.Errorf("migrate: %s v%d→v%d: %w", r.Store, cur, cur+1, err)
		}
		raw["version"] = cur + 1
	}
	out, err = json.Marshal(raw)
	if err != nil {
		return nil, false, fmt.Errorf("migrate: re-encoding %s: %w", r.Store, err)
	}
	return out, true, nil
}
