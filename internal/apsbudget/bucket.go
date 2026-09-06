package apsbudget

import "time"

// bucket is a continuously refilling token bucket denominated in query points.
//
// APS meters a fixed per-minute window whose boundary we cannot see, so the
// bucket refills continuously at capacity/60 per second instead: a smooth
// model that never lets a burst exceed the window and self-corrects from the
// `remaining quota` a real 429 reports (setLevel). Tokens may go negative —
// an under-estimate has already been spent upstream, so the deficit must be
// paid back before anything else goes out.
type bucket struct {
	capacity  float64
	tokens    float64
	refillPer float64 // tokens per second
	last      time.Time
}

func newBucket(capacity, refillPerSec float64, now time.Time) *bucket {
	return &bucket{capacity: capacity, tokens: capacity, refillPer: refillPerSec, last: now}
}

// refill credits the elapsed time since the last refill.
func (b *bucket) refill(now time.Time) {
	if !now.After(b.last) {
		return
	}
	b.tokens += now.Sub(b.last).Seconds() * b.refillPer
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = now
}

func (b *bucket) available() float64 { return b.tokens }

// take withdraws n points; the caller has already decided the withdrawal is
// allowed, so this may leave the bucket negative after a correction.
func (b *bucket) take(n float64) { b.tokens -= n }

// correct reconciles an estimated withdrawal with the actual cost the gateway
// reported: an over-estimate is refunded, an under-estimate charged.
func (b *bucket) correct(estimated, actual int) {
	if actual <= 0 || estimated == actual {
		return
	}
	b.tokens += float64(estimated - actual)
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
}

// setLevel resyncs the bucket to a level the gateway reported, but only ever
// downwards: the gateway's number is the truth about what is left, and an
// upward resync would let a burst through on a stale reading.
func (b *bucket) setLevel(level float64) {
	if level < b.tokens {
		b.tokens = level
	}
	if b.tokens < -b.capacity {
		b.tokens = -b.capacity
	}
}

// deficitWait is how long until the bucket holds at least want tokens.
func (b *bucket) deficitWait(want float64) time.Duration {
	if b.tokens >= want {
		return 0
	}
	if b.refillPer <= 0 {
		return time.Hour
	}
	return time.Duration((want - b.tokens) / b.refillPer * float64(time.Second))
}
