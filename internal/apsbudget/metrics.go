package apsbudget

import "time"

// Snapshot is the scheduler's state at one instant, for GET /api/quota, the
// throttle response headers and the -v summary line.
type Snapshot struct {
	// Level is "ok", "slow" (something waited noticeably in the last few
	// seconds, or the bucket is low) or "cooldown" (a real 429 is being
	// honoured). The SPA's banner keys off it.
	Level     string
	Throttled bool
	// CooldownUntil is zero when no cooldown is active on the GraphQL lane.
	CooldownUntil     time.Time
	RESTCooldownUntil time.Time
	// RetryAfter is the longer of the two remaining cooldowns.
	RetryAfter time.Duration

	Capacity       int
	Available      int
	UsedLastMinute int
	InFlight       int
	InFlightREST   int
	Queue          [numPriorities]int
	Refused        int64 // scheduler refusals since start (Queued errors)
	Trips          int64 // real upstream 429s since start
}

// spendRing is a 60-slot per-second ring of points spent, for UsedLastMinute.
type spendRing struct {
	slots   [60]int
	lastSec int64
}

func (r *spendRing) advance(now time.Time) {
	sec := now.Unix()
	if r.lastSec == 0 {
		r.lastSec = sec
		return
	}
	for r.lastSec < sec {
		r.lastSec++
		r.slots[r.lastSec%60] = 0
	}
}

func (r *spendRing) add(now time.Time, points int) {
	r.advance(now)
	r.slots[now.Unix()%60] += points
}

func (r *spendRing) sum(now time.Time) int {
	r.advance(now)
	total := 0
	for _, v := range r.slots {
		total += v
	}
	return total
}
