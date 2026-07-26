package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// GFS is the grandfather-father-son retention ladder: how many snapshots each
// managed tier keeps. manual/ and pre-restore/ are outside the ladder and are
// never pruned.
var GFS = struct {
	Daily, Weekly, Monthly int
}{Daily: 7, Weekly: 4, Monthly: 12}

// Promote copies the newest daily snapshot into weekly/ when now's ISO week
// has no weekly entry yet, and into monthly/ when now's calendar month has no
// monthly entry yet. Promotion is a directory copy (not hardlinks), so pruning
// a daily can never hollow out the weekly/monthly that was born from it.
func (e *Engine) Promote(now time.Time) error {
	newest, ok := e.newestSnapshot(KindDaily)
	if !ok {
		return nil
	}
	src := filepath.Join(e.Dir, string(KindDaily), newest)

	wantYear, wantWeek := now.UTC().ISOWeek()
	haveWeekly, err := e.tierHas(KindWeekly, func(ts time.Time) bool {
		y, w := ts.ISOWeek()
		return y == wantYear && w == wantWeek
	})
	if err != nil {
		return err
	}
	if !haveWeekly {
		if err := copyDir(src, filepath.Join(e.Dir, string(KindWeekly), newest)); err != nil {
			return fmt.Errorf("promoting to weekly: %w", err)
		}
	}

	wantMonth := now.UTC().Format("200601")
	haveMonthly, err := e.tierHas(KindMonthly, func(ts time.Time) bool {
		return ts.Format("200601") == wantMonth
	})
	if err != nil {
		return err
	}
	if !haveMonthly {
		if err := copyDir(src, filepath.Join(e.Dir, string(KindMonthly), newest)); err != nil {
			return fmt.Errorf("promoting to monthly: %w", err)
		}
	}
	return nil
}

// Prune enforces the GFS ladder: newest GFS.Daily dailies, GFS.Weekly
// weeklies, GFS.Monthly monthlies; everything older is removed. Only dirs
// whose names parse as snapshot timestamps are counted or removed, and the
// manual/ and pre-restore/ tiers are never touched.
func (e *Engine) Prune() error {
	for kind, keep := range map[Kind]int{
		KindDaily:   GFS.Daily,
		KindWeekly:  GFS.Weekly,
		KindMonthly: GFS.Monthly,
	} {
		names, err := e.tierSnapshots(kind)
		if err != nil {
			return err
		}
		if len(names) <= keep {
			continue
		}
		// tierSnapshots sorts newest first; everything past keep goes.
		for _, name := range names[keep:] {
			if err := os.RemoveAll(filepath.Join(e.Dir, string(kind), name)); err != nil {
				return fmt.Errorf("pruning %s/%s: %w", kind, name, err)
			}
		}
	}
	return nil
}

// tierSnapshots lists a tier's snapshot dir names, newest first. Names that
// don't parse as timestamps are excluded (they are not ours to manage).
func (e *Engine) tierSnapshots(kind Kind) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(e.Dir, string(kind)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing %s tier: %w", kind, err)
	}
	names := []string{}
	for _, en := range entries {
		if !en.IsDir() {
			continue
		}
		if _, ok := parseTS(en.Name()); !ok {
			continue
		}
		names = append(names, en.Name())
	}
	// Timestamp names sort lexicographically = chronologically; reverse for
	// newest-first.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

// newestSnapshot returns the most recent snapshot dir name in a tier.
func (e *Engine) newestSnapshot(kind Kind) (string, bool) {
	names, err := e.tierSnapshots(kind)
	if err != nil || len(names) == 0 {
		return "", false
	}
	return names[0], true
}

// tierHas reports whether any snapshot in the tier satisfies match (judged by
// the creation time encoded in its dir name).
func (e *Engine) tierHas(kind Kind, match func(time.Time) bool) (bool, error) {
	names, err := e.tierSnapshots(kind)
	if err != nil {
		return false, err
	}
	for _, name := range names {
		if ts, ok := parseTS(name); ok && match(ts) {
			return true, nil
		}
	}
	return false, nil
}

// copyDir recursively copies a snapshot directory. Plain file copies only —
// snapshots contain no symlinks or special files by construction.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0700); err != nil {
		return err
	}
	for _, en := range entries {
		s := filepath.Join(src, en.Name())
		d := filepath.Join(dst, en.Name())
		if en.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
