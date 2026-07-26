package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Hub-identity hardening (phase H3): manifests carry Hub/HubSlug, restore
// refuses pre-v2, foreign-hub, and path-escaping manifests BEFORE any byte is
// written, and verify surfaces the same findings as report-level warnings.

// hubFiles is the minimal handmade-snapshot payload used across these tests.
var hubFiles = map[string]struct {
	data []byte
	sv   int
}{
	"tasks/p/tasks.json": {data: []byte(`{"version":2}`), sv: 2},
}

// assertNothingRestored proves a refusal happened before any write: no
// pre-restore safety snapshot was taken and the destination stayed empty.
func assertNothingRestored(t *testing.T, engineDir, hubRoot string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(engineDir, string(KindPreRestore))); !os.IsNotExist(err) {
		t.Error("pre-restore snapshot was taken despite refusal")
	}
	entries, err := os.ReadDir(hubRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("hub root not empty after refusal: %v", entries)
	}
}

func TestManifestStampsHubIdentity(t *testing.T) {
	e := &Engine{
		Dir:        t.TempDir(),
		Sources:    []Source{&fakeSource{name: "tasks", files: []fakeFile{{rel: "p/tasks.json", data: []byte(`{"version":2}`), sv: 2}}}},
		AppVersion: "1.0.0",
		Hub:        "b.hub.id.A",
		HubSlug:    "b_hub_id_a",
	}
	m, err := e.Run(KindManual)
	if err != nil {
		t.Fatal(err)
	}
	if m.ManifestVersion != 2 || m.Hub != "b.hub.id.A" || m.HubSlug != "b_hub_id_a" {
		t.Errorf("manifest identity = v%d %q/%q, want v2 with the engine's hub", m.ManifestVersion, m.Hub, m.HubSlug)
	}
	// The stamp survives the disk round trip.
	rt, err := ReadManifest(onlySnapshotDir(t, e.Dir, KindManual))
	if err != nil {
		t.Fatal(err)
	}
	if rt.Hub != "b.hub.id.A" || rt.HubSlug != "b_hub_id_a" {
		t.Errorf("re-read manifest identity = %q/%q", rt.Hub, rt.HubSlug)
	}
}

func TestRestoreRefusesPreHubIsolationManifest(t *testing.T) {
	root := t.TempDir()
	snapDir := writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
		m.ManifestVersion = 1 // a pre-H3 snapshot: no hub identity at all
		m.Hub, m.HubSlug = "", ""
	})
	hubRoot := t.TempDir()
	eng := &Engine{Dir: root, AppVersion: "1.2.3", Hub: "id-a", HubSlug: "slug-a"}
	err := eng.Restore(snapDir, RestoreRoots{HubRoot: hubRoot, ConfigDir: t.TempDir()}, restoreExpected)
	if err == nil || !strings.Contains(err.Error(), "predates hub isolation") {
		t.Fatalf("Restore v1 manifest: err = %v, want the 'predates hub isolation' refusal", err)
	}
	if !strings.Contains(err.Error(), "take a fresh backup") {
		t.Errorf("refusal message should tell the operator what to do: %v", err)
	}
	assertNothingRestored(t, root, hubRoot)
}

func TestRestoreRefusesFutureManifestVersion(t *testing.T) {
	root := t.TempDir()
	snapDir := writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
		m.ManifestVersion = ManifestVersion + 1
	})
	hubRoot := t.TempDir()
	eng := &Engine{Dir: root, AppVersion: "1.2.3"}
	err := eng.Restore(snapDir, RestoreRoots{HubRoot: hubRoot, ConfigDir: t.TempDir()}, restoreExpected)
	if err == nil || !strings.Contains(err.Error(), "newer than this build understands") {
		t.Fatalf("Restore v%d manifest: err = %v, want the future-version refusal", ManifestVersion+1, err)
	}
	assertNothingRestored(t, root, hubRoot)
}

func TestRestoreRefusesForeignHub(t *testing.T) {
	eng := func(dir string) *Engine {
		return &Engine{Dir: dir, AppVersion: "1.2.3", Hub: "id-a", HubSlug: "slug-a"}
	}

	// Branch 1: slug mismatch (a snapshot from another hub's tree).
	t.Run("slug mismatch", func(t *testing.T) {
		root := t.TempDir()
		snapDir := writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
			m.Hub, m.HubSlug = "id-b", "slug-b"
		})
		hubRoot := t.TempDir()
		err := eng(root).Restore(snapDir, RestoreRoots{HubRoot: hubRoot, ConfigDir: t.TempDir()}, restoreExpected)
		if err == nil || !strings.Contains(err.Error(), "cross-hub restore refused") {
			t.Fatalf("err = %v, want the foreign-hub refusal", err)
		}
		assertNothingRestored(t, root, hubRoot)
	})

	// Branch 2: slug matches but the raw hub id differs — the lossy-slug
	// collision case, refused on the raw id.
	t.Run("raw id mismatch", func(t *testing.T) {
		root := t.TempDir()
		snapDir := writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
			m.Hub, m.HubSlug = "id-b", "slug-a"
		})
		hubRoot := t.TempDir()
		err := eng(root).Restore(snapDir, RestoreRoots{HubRoot: hubRoot, ConfigDir: t.TempDir()}, restoreExpected)
		if err == nil || !strings.Contains(err.Error(), "cross-hub restore refused") {
			t.Fatalf("err = %v, want the foreign-hub refusal", err)
		}
		assertNothingRestored(t, root, hubRoot)
	})

	// Matching identity restores fine — the gate refuses foreigners, not us.
	t.Run("matching hub restores", func(t *testing.T) {
		root := t.TempDir()
		snapDir := writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
			m.Hub, m.HubSlug = "id-a", "slug-a"
		})
		hubRoot := t.TempDir()
		if err := eng(root).Restore(snapDir, RestoreRoots{HubRoot: hubRoot, ConfigDir: t.TempDir()}, restoreExpected); err != nil {
			t.Fatalf("matching-hub restore: %v", err)
		}
		if _, err := os.Stat(filepath.Join(hubRoot, "tasks", "p", "tasks.json")); err != nil {
			t.Errorf("matching-hub restore did not copy: %v", err)
		}
	})
}

func TestRestoreRefusesPathEscapingManifests(t *testing.T) {
	for _, escape := range []string{
		"../evil.json",
		"/abs/evil.json",
		"tasks/../../evil.json",
		"..",
	} {
		t.Run(escape, func(t *testing.T) {
			root := t.TempDir()
			snapDir := writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
				m.Files = append(m.Files, ManifestFile{
					Path: escape, Store: "tasks",
					SHA256: strings.Repeat("0", 64), Size: 4, SchemaVersion: 2,
				})
			})
			hubRoot := t.TempDir()
			eng := &Engine{Dir: root, AppVersion: "1.2.3"}
			err := eng.Restore(snapDir, RestoreRoots{HubRoot: hubRoot, ConfigDir: t.TempDir()}, restoreExpected)
			if err == nil || !strings.Contains(err.Error(), "escape") {
				t.Fatalf("Restore with path %q: err = %v, want an escape refusal", escape, err)
			}
			// Refused before a single write: the good entry was NOT restored
			// either, no pre-restore snapshot, and nothing landed outside.
			assertNothingRestored(t, root, hubRoot)
			if _, serr := os.Stat(filepath.Join(filepath.Dir(hubRoot), "evil.json")); !os.IsNotExist(serr) {
				t.Error("escaped file exists outside the hub root")
			}
		})
	}
}

func TestListWarnsOnPreHubIsolationSnapshots(t *testing.T) {
	root := t.TempDir()
	writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
		m.ManifestVersion = 1
	})
	eng := &Engine{Dir: root}
	sums, err := eng.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("List = %d rows, want 1", len(sums))
	}
	if !strings.Contains(sums[0].Warning, "predates hub isolation") {
		t.Errorf("v1 row warning = %q, want a 'predates hub isolation' notice", sums[0].Warning)
	}
	// The row still renders its manifest facts — listed, not hidden.
	if sums[0].FileCount != 1 || sums[0].CreatedAt.IsZero() {
		t.Errorf("v1 row lost its manifest facts: %+v", sums[0])
	}
}

func TestEngineVerifyWarnsOnHubFindings(t *testing.T) {
	eng := func(dir string) *Engine {
		return &Engine{Dir: dir, AppVersion: "1.2.3", Hub: "id-a", HubSlug: "slug-a"}
	}

	t.Run("pre-v2 manifest", func(t *testing.T) {
		root := t.TempDir()
		snapDir := writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
			m.ManifestVersion = 1
		})
		rep, err := eng(root).Verify(snapDir, restoreExpected)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(rep.Warning, "predates hub isolation") {
			t.Errorf("warning = %q", rep.Warning)
		}
		if !rep.OK {
			t.Error("file-level OK should be unaffected by the hub warning")
		}
	})

	t.Run("foreign hub", func(t *testing.T) {
		root := t.TempDir()
		snapDir := writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
			m.Hub, m.HubSlug = "id-b", "slug-b"
		})
		rep, err := eng(root).Verify(snapDir, restoreExpected)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(rep.Warning, "not this session's hub") {
			t.Errorf("warning = %q", rep.Warning)
		}
		if rep.Hub != "id-b" || rep.HubSlug != "slug-b" {
			t.Errorf("report identity = %q/%q", rep.Hub, rep.HubSlug)
		}
	})

	t.Run("own snapshot is clean", func(t *testing.T) {
		root := t.TempDir()
		snapDir := writeHandmadeSnapshot(t, root, hubFiles, "1.0.0", func(m *Manifest) {
			m.Hub, m.HubSlug = "id-a", "slug-a"
		})
		rep, err := eng(root).Verify(snapDir, restoreExpected)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Warning != "" || !rep.OK {
			t.Errorf("own snapshot: warning = %q, ok = %v", rep.Warning, rep.OK)
		}
	})
}

// TestManifestJSONShape pins the wire names of the identity fields — the SPA
// and the on-disk format both depend on them.
func TestManifestJSONShape(t *testing.T) {
	m := Manifest{ManifestVersion: 2, Hub: "h", HubSlug: "s"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"manifestVersion":2`, `"hub":"h"`, `"hubSlug":"s"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("manifest JSON %s lacks %s", data, want)
		}
	}
}
