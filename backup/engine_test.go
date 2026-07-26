package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeSource is a scripted Source for engine tests.
type fakeSource struct {
	name  string
	files []fakeFile
	err   error
}

type fakeFile struct {
	rel  string
	data []byte
	sv   int
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Snapshot(visit func(rel string, data []byte, schemaVersion int) error) error {
	for _, ff := range f.files {
		if err := visit(ff.rel, ff.data, ff.sv); err != nil {
			return err
		}
	}
	return f.err
}

func TestRunManualWritesFilesAndManifest(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{
		Dir: dir,
		Sources: []Source{
			&fakeSource{name: "tasks", files: []fakeFile{
				{rel: "tasks/proj-a/tasks.json", data: []byte(`{"version":2}`), sv: 2},
				{rel: "tasks/proj-b/tasks.json", data: []byte(`{"version":2,"tasks":[]}`), sv: 2},
			}},
			&fakeSource{name: "chat", files: []fakeFile{
				{rel: "chat/proj-a/msg-c1.jsonl", data: []byte{}, sv: 1}, // empty file still recorded
			}},
		},
		AppVersion: "test-1.0",
	}

	m, err := e.Run(KindManual)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m.Kind != KindManual || m.ManifestVersion != ManifestVersion || m.AppVersion != "test-1.0" {
		t.Errorf("manifest header = %+v", m)
	}
	if len(m.Files) != 3 {
		t.Fatalf("manifest files = %d, want 3", len(m.Files))
	}

	// Exactly one manual snapshot dir exists.
	entries, err := os.ReadDir(filepath.Join(dir, "manual"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("manual tier entries = %v, %v", entries, err)
	}
	snapDir := filepath.Join(dir, "manual", entries[0].Name())

	// Every manifest entry's file exists with matching bytes, size and hash.
	byPath := map[string]ManifestFile{}
	for _, f := range m.Files {
		byPath[f.Path] = f
	}
	for path, mf := range byPath {
		data, err := os.ReadFile(filepath.Join(snapDir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("reading backed-up %s: %v", path, err)
		}
		if int64(len(data)) != mf.Size {
			t.Errorf("%s size = %d, manifest says %d", path, len(data), mf.Size)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != mf.SHA256 {
			t.Errorf("%s sha256 = %s, manifest says %s", path, got, mf.SHA256)
		}
	}
	if byPath["tasks/proj-a/tasks.json"].SchemaVersion != 2 {
		t.Errorf("schemaVersion = %d, want 2", byPath["tasks/proj-a/tasks.json"].SchemaVersion)
	}
	if mf, ok := byPath["chat/proj-a/msg-c1.jsonl"]; !ok || mf.Size != 0 || mf.SchemaVersion != 1 {
		t.Errorf("empty jsonl entry = %+v, ok=%v", mf, ok)
	}

	// The on-disk manifest round-trips.
	rm, err := ReadManifest(snapDir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if len(rm.Files) != 3 || rm.Kind != KindManual {
		t.Errorf("re-read manifest = %+v", rm)
	}

	// List shows it, newest first, with size totals.
	sums, err := e.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sums) != 1 {
		t.Fatalf("List = %d entries, want 1", len(sums))
	}
	s := sums[0]
	wantBytes := int64(len(`{"version":2}`) + len(`{"version":2,"tasks":[]}`))
	if s.Kind != KindManual || s.FileCount != 3 || s.TotalBytes != wantBytes || s.Warning != "" {
		t.Errorf("summary = %+v", s)
	}
	if s.Path != "manual/"+entries[0].Name() {
		t.Errorf("summary path = %q", s.Path)
	}
}

func TestRunAbortsAndRemovesPartialDirOnSourceError(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{
		Dir: dir,
		Sources: []Source{
			&fakeSource{name: "ok", files: []fakeFile{{rel: "ok/file.json", data: []byte("{}")}}},
			&fakeSource{name: "bad", err: errors.New("disk went away")},
		},
	}
	if _, err := e.Run(KindManual); err == nil {
		t.Fatal("Run succeeded despite source error")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "manual"))
	if err == nil && len(entries) > 0 {
		t.Errorf("partial snapshot dir left behind: %v", entries)
	}
}

func TestRunRejectsEscapingPaths(t *testing.T) {
	e := &Engine{
		Dir:     t.TempDir(),
		Sources: []Source{&fakeSource{name: "evil", files: []fakeFile{{rel: "../outside.json", data: []byte("{}")}}}},
	}
	if _, err := e.Run(KindManual); err == nil {
		t.Fatal("escaping rel path accepted")
	}
}

func TestListToleratesMissingManifest(t *testing.T) {
	dir := t.TempDir()
	// A snapshot dir with no manifest (e.g. an interrupted pre-engine copy).
	if err := os.MkdirAll(filepath.Join(dir, "daily", "20260101-030000"), 0700); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Dir: dir}
	sums, err := e.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sums) != 1 || sums[0].Warning == "" {
		t.Errorf("List = %+v, want one warned entry", sums)
	}
	if sums[0].CreatedAt.IsZero() {
		t.Error("CreatedAt not recovered from dir name")
	}
}

// TestGFSRetention simulates 40 consecutive daily runs with a controlled
// clock and asserts the 7/4/12 ladder plus one-per-ISO-week weekly and
// one-per-month monthly promotion.
func TestGFSRetention(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{
		Dir: dir,
		Sources: []Source{&fakeSource{name: "tasks", files: []fakeFile{
			{rel: "tasks/p/tasks.json", data: []byte(`{"version":2}`), sv: 2},
		}}},
		AppVersion: "sim",
	}

	start := time.Date(2026, 1, 5, 3, 30, 0, 0, time.UTC) // a Monday
	var days []time.Time
	for i := 0; i < 40; i++ {
		now := start.AddDate(0, 0, i)
		days = append(days, now)
		if _, err := e.runAt(KindDaily, now); err != nil {
			t.Fatalf("day %d runAt: %v", i, err)
		}
	}

	daily, _ := e.tierSnapshots(KindDaily)
	weekly, _ := e.tierSnapshots(KindWeekly)
	monthly, _ := e.tierSnapshots(KindMonthly)

	if len(daily) != GFS.Daily {
		t.Errorf("daily retained = %d, want %d", len(daily), GFS.Daily)
	}
	if len(weekly) != GFS.Weekly {
		t.Errorf("weekly retained = %d, want %d", len(weekly), GFS.Weekly)
	}
	// 40 days from Jan 5 spans January and February → 2 distinct months.
	wantMonths := map[string]bool{}
	for _, d := range days {
		wantMonths[d.Format("200601")] = true
	}
	if len(monthly) != len(wantMonths) {
		t.Errorf("monthly retained = %d, want %d (one per month)", len(monthly), len(wantMonths))
	}
	if len(monthly) > GFS.Monthly {
		t.Errorf("monthly exceeds cap: %d > %d", len(monthly), GFS.Monthly)
	}

	// Weekly entries are one per ISO week.
	seenWeeks := map[string]bool{}
	for _, name := range weekly {
		ts, ok := parseTS(name)
		if !ok {
			t.Fatalf("weekly dir %q is not a timestamp", name)
		}
		y, w := ts.ISOWeek()
		key := fmt.Sprintf("%d-%d", y, w)
		if seenWeeks[key] {
			t.Errorf("two weekly snapshots in ISO week %s", key)
		}
		seenWeeks[key] = true
	}
	// Monthly entries are one per calendar month.
	seenMonths := map[string]bool{}
	for _, name := range monthly {
		ts, _ := parseTS(name)
		key := ts.Format("200601")
		if seenMonths[key] {
			t.Errorf("two monthly snapshots in month %s", key)
		}
		seenMonths[key] = true
	}

	// The retained dailies are the newest 7.
	newestDaily, _ := parseTS(daily[0])
	if !newestDaily.Equal(days[39].Truncate(time.Second)) {
		t.Errorf("newest daily = %v, want %v", newestDaily, days[39])
	}
	oldestDaily, _ := parseTS(daily[len(daily)-1])
	if !oldestDaily.Equal(days[33].Truncate(time.Second)) {
		t.Errorf("oldest daily = %v, want %v", oldestDaily, days[33])
	}

	// A weekly promotion is a real directory copy: it still opens after its
	// source daily has been pruned.
	if _, err := ReadManifest(filepath.Join(dir, "weekly", weekly[len(weekly)-1])); err != nil {
		t.Errorf("oldest weekly manifest unreadable: %v", err)
	}
}

// TestPruneNeverTouchesManualOrPreRestore floods the manual and pre-restore
// tiers past every cap and asserts Prune leaves them alone.
func TestPruneNeverTouchesManualOrPreRestore(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{Dir: dir}
	for _, kind := range []Kind{KindManual, KindPreRestore} {
		for i := 0; i < 15; i++ {
			ts := time.Date(2026, 1, 1+i, 12, 0, 0, 0, time.UTC).Format(tsLayout)
			if err := os.MkdirAll(filepath.Join(dir, string(kind), ts), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := e.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	for _, kind := range []Kind{KindManual, KindPreRestore} {
		entries, err := os.ReadDir(filepath.Join(dir, string(kind)))
		if err != nil || len(entries) != 15 {
			t.Errorf("%s tier = %d entries after prune, want 15 (err %v)", kind, len(entries), err)
		}
	}
}

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("test", 3600)
	base := time.Date(2026, 7, 26, 12, 0, 0, 0, loc)

	// Still ahead today.
	got := NextRun(base, "15:30")
	want := time.Date(2026, 7, 26, 15, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("ahead-of-now = %v, want %v", got, want)
	}

	// Already passed → tomorrow.
	got = NextRun(base, "03:30")
	want = time.Date(2026, 7, 27, 3, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("passed = %v, want %v", got, want)
	}

	// Exactly now → tomorrow (not an immediate re-fire).
	got = NextRun(base, "12:00")
	want = time.Date(2026, 7, 27, 12, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("exactly-now = %v, want %v", got, want)
	}

	// Midnight edge: at 00:00 sharp, a 00:00 schedule means tomorrow.
	midnight := time.Date(2026, 7, 26, 0, 0, 0, 0, loc)
	got = NextRun(midnight, "00:00")
	want = time.Date(2026, 7, 27, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("midnight = %v, want %v", got, want)
	}
	// …and one nanosecond before midnight, 00:00 is (just) ahead — but that
	// candidate is "today" relative to the pre-midnight date.
	justBefore := time.Date(2026, 7, 26, 23, 59, 59, 0, loc)
	got = NextRun(justBefore, "00:00")
	want = time.Date(2026, 7, 27, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("just-before-midnight = %v, want %v", got, want)
	}

	// Invalid falls back to the default 03:30.
	got = NextRun(base, "25:99")
	want = time.Date(2026, 7, 27, 3, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("invalid hhmm = %v, want default-time fallback %v", got, want)
	}
}

func TestValidTime(t *testing.T) {
	for _, ok := range []string{"00:00", "03:30", "23:59", "19:05"} {
		if !ValidTime(ok) {
			t.Errorf("ValidTime(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "3:30", "24:00", "12:60", "12:5", "ab:cd", "12:30:00"} {
		if ValidTime(bad) {
			t.Errorf("ValidTime(%q) = true", bad)
		}
	}
}
