package backup

import (
	"regexp"
	"strconv"
	"time"
)

// DefaultTime is the scheduled daily backup time when none is configured:
// 03:30 local, chosen to dodge both midnight cron crowds and DST transition
// hours in most zones.
const DefaultTime = "03:30"

var hhmmRE = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// ValidTime reports whether hhmm is a well-formed 24-hour "HH:MM".
func ValidTime(hhmm string) bool {
	return hhmmRE.MatchString(hhmm)
}

// NextRun returns the next local occurrence of hhmm: today if that time is
// still ahead of now, otherwise tomorrow. There is deliberately no
// missed-window catch-up (settled decision): a server that was down at the
// scheduled time simply backs up at the next occurrence. An invalid hhmm
// falls back to DefaultTime.
func NextRun(now time.Time, hhmm string) time.Time {
	m := hhmmRE.FindStringSubmatch(hhmm)
	if m == nil {
		m = hhmmRE.FindStringSubmatch(DefaultTime)
	}
	hh, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	cand := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	if cand.After(now) {
		return cand
	}
	return cand.AddDate(0, 0, 1)
}
