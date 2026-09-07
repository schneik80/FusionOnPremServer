package apsbudget

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

// Config tunes the scheduler. DefaultConfig is what the server runs with;
// tests shrink it.
type Config struct {
	// FullQuota is what APS grants per minute (6000 points for a standard
	// app). Capacity is the share this process lets itself spend; the gap is
	// headroom for other clients on the same client_id and for estimate
	// error. RefillPerSec is Capacity/60 unless set.
	FullQuota    float64
	Capacity     float64
	RefillPerSec float64

	// MaxInFlight bounds concurrent GraphQL round trips; MaxInFlightREST the
	// Data Management / OSS ones.
	MaxInFlight     int
	MaxInFlightREST int

	// ReserveFloor[p] is the fraction of Capacity that must remain in the
	// bucket AFTER a priority-p request takes its points. P0 may drain the
	// bucket; P2 may not touch the last third, so a click always finds
	// something left.
	ReserveFloor [numPriorities]float64

	// MaxQueueWait[p] bounds how long a priority-p request may sit queued; 0
	// means "only the caller's deadline". Beyond it the request is refused
	// immediately with a RateLimitError{Queued:true} rather than parked.
	MaxQueueWait [numPriorities]time.Duration

	// DefaultCooldown applies after a real 429 without Retry-After; repeats
	// inside a minute escalate ×1.5 up to MaxCooldown.
	DefaultCooldown time.Duration
	MaxCooldown     time.Duration

	// SlowWait is the queue wait beyond which the snapshot reports "slow";
	// SlowWindow how long that report lingers.
	SlowWait   time.Duration
	SlowWindow time.Duration

	Now func() time.Time
}

// DefaultConfig returns the production tuning.
func DefaultConfig() Config {
	return Config{
		FullQuota: 6000,
		// The whole quota, not a share of it: the static estimates are only
		// calibrated at the drawings query, and a conservative bucket on top
		// of conservative estimates starved user-blocking calls into 504s in
		// the first real run. Headroom for background work comes from the
		// reserve floors; a real 429 resyncs the bucket to the truth.
		Capacity:        6000,
		MaxInFlight:     12,
		MaxInFlightREST: 6,
		ReserveFloor:    [numPriorities]float64{0, 0.10, 0.35},
		MaxQueueWait:    [numPriorities]time.Duration{0, 10 * time.Second, 2 * time.Second},
		DefaultCooldown: 20 * time.Second,
		MaxCooldown:     60 * time.Second,
		SlowWait:        500 * time.Millisecond,
		SlowWindow:      5 * time.Second,
	}
}

func (c Config) normalized() Config {
	if c.FullQuota <= 0 {
		c.FullQuota = 6000
	}
	if c.Capacity <= 0 {
		c.Capacity = c.FullQuota
	}
	if c.RefillPerSec <= 0 {
		c.RefillPerSec = c.Capacity / 60
	}
	if c.MaxInFlight <= 0 {
		c.MaxInFlight = 12
	}
	if c.MaxInFlightREST <= 0 {
		c.MaxInFlightREST = 6
	}
	if c.DefaultCooldown <= 0 {
		c.DefaultCooldown = 20 * time.Second
	}
	if c.MaxCooldown < c.DefaultCooldown {
		c.MaxCooldown = c.DefaultCooldown
	}
	if c.SlowWait <= 0 {
		c.SlowWait = 500 * time.Millisecond
	}
	if c.SlowWindow <= 0 {
		c.SlowWindow = 5 * time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// Request describes one upstream round trip to be admitted.
type Request struct {
	Lane     Lane
	Priority Priority
	Op       string
	Cost     int // estimated points; ignored on the REST lane
	Label    string
}

// Release must be called exactly once when the round trip ends. actual is the
// cost the gateway reported (0 when unknown) and reconciles the estimate.
type Release func(actual int)

type waiter struct {
	req        Request
	seq        uint64
	enqueued   time.Time
	ready      chan struct{}
	dispatched bool
	cancelled  bool
	index      int
}

// waitQueue is a min-heap by (priority, seq): strict priority, FIFO within.
type waitQueue []*waiter

func (q waitQueue) Len() int { return len(q) }
func (q waitQueue) Less(i, j int) bool {
	if q[i].req.Priority != q[j].req.Priority {
		return q[i].req.Priority < q[j].req.Priority
	}
	return q[i].seq < q[j].seq
}
func (q waitQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i]; q[i].index = i; q[j].index = j }
func (q *waitQueue) Push(x any)   { w := x.(*waiter); w.index = len(*q); *q = append(*q, w) }
func (q *waitQueue) Pop() any {
	old := *q
	n := len(old)
	w := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return w
}

type laneState struct {
	queue         waitQueue
	inFlight      int
	max           int
	cooldownUntil time.Time
	lastTrip      time.Time
	lastCooldown  time.Duration
	timer         *time.Timer
}

// Scheduler admits APS round trips against a points budget, in priority
// order, with a per-lane cooldown after real 429s. One instance per process;
// every session shares it because APS meters the app, not the user.
//
// Lock order: a feature semaphore (fan-out bounds in api/) is taken BEFORE
// Acquire and a slot is held for exactly one HTTP round trip — never across
// pages, never while acquiring anything else. That keeps nesting acyclic.
type Scheduler struct {
	cfg Config
	est *Estimator

	mu      sync.Mutex
	bucket  *bucket
	lanes   [2]laneState
	seq     uint64
	spend   spendRing
	slowAt  time.Time
	refused int64
	trips   int64
	closed  bool
}

// New builds a scheduler. est may be nil (no cost observations).
func New(cfg Config, est *Estimator) *Scheduler {
	cfg = cfg.normalized()
	now := cfg.Now()
	s := &Scheduler{cfg: cfg, est: est, bucket: newBucket(cfg.Capacity, cfg.RefillPerSec, now)}
	s.lanes[LaneGraphQL].max = cfg.MaxInFlight
	s.lanes[LaneREST].max = cfg.MaxInFlightREST
	return s
}

// Close stops the wake-up timers. Queued waiters are refused.
func (s *Scheduler) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for i := range s.lanes {
		if t := s.lanes[i].timer; t != nil {
			t.Stop()
		}
		for _, w := range s.lanes[i].queue {
			if !w.dispatched {
				w.cancelled = true
				close(w.ready)
			}
		}
		s.lanes[i].queue = nil
	}
}

// Acquire waits for a slot for r, honouring ctx, or refuses immediately with
// a *RateLimitError{Queued:true} when the wait would exceed what the request
// can bear. On success the returned Release must be called once.
func (s *Scheduler) Acquire(ctx context.Context, r Request) (Release, error) {
	r.Priority = clampPriority(r.Priority)
	if r.Lane == LaneREST {
		r.Cost = 0
	}
	if r.Cost < 0 {
		r.Cost = 0
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, &RateLimitError{Lane: r.Lane, Queued: true, RetryAfter: s.cfg.DefaultCooldown}
	}
	now := s.cfg.Now()
	s.bucket.refill(now)

	// Pre-check: would the wait exceed the caller's patience? Refuse now with
	// a real countdown rather than parking the request into a 504.
	allowed := s.cfg.MaxQueueWait[r.Priority]
	if dl, ok := ctx.Deadline(); ok {
		if rem := dl.Sub(now); allowed == 0 || rem < allowed {
			allowed = rem
		}
	}
	expected := s.expectedWaitLocked(now, r)
	if allowed > 0 && expected > allowed {
		s.refused++
		s.slowAt = now
		s.mu.Unlock()
		return nil, &RateLimitError{Lane: r.Lane, Queued: true, RetryAfter: expected}
	}

	s.seq++
	w := &waiter{req: r, seq: s.seq, enqueued: now, ready: make(chan struct{}, 1)}
	heap.Push(&s.lanes[r.Lane].queue, w)
	s.kickLocked(r.Lane, now)
	s.mu.Unlock()

	// The timeout fires a little BEFORE the caller's deadline, so a request
	// that could not be served in time is refused with a typed countdown
	// (HTTP 429 + Retry-After) rather than dying as a 504 when its context
	// expires a moment later.
	var timeout <-chan time.Time
	if allowed > 0 {
		margin := allowed / 10
		if margin > time.Second {
			margin = time.Second
		}
		t := time.NewTimer(allowed - margin)
		defer t.Stop()
		timeout = t.C
	}

	select {
	case <-w.ready:
	case <-ctx.Done():
		if s.abandon(w) {
			return nil, ctx.Err()
		}
	case <-timeout:
		if s.abandon(w) {
			s.mu.Lock()
			s.refused++
			s.slowAt = s.cfg.Now()
			exp := s.expectedWaitLocked(s.cfg.Now(), r)
			s.mu.Unlock()
			return nil, &RateLimitError{Lane: r.Lane, Queued: true, RetryAfter: exp}
		}
	}
	if w.cancelled {
		return nil, &RateLimitError{Lane: r.Lane, Queued: true, RetryAfter: s.cfg.DefaultCooldown}
	}
	return s.releaseFor(w), nil
}

// abandon marks w cancelled unless it was dispatched in the meantime; returns
// true when the caller must give up (not dispatched).
func (s *Scheduler) abandon(w *waiter) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.dispatched {
		// Dispatched while we were leaving: the slot is ours to return.
		return false
	}
	w.cancelled = true
	if w.index >= 0 && w.index < len(s.lanes[w.req.Lane].queue) && s.lanes[w.req.Lane].queue[w.index] == w {
		heap.Remove(&s.lanes[w.req.Lane].queue, w.index)
	}
	s.kickLocked(w.req.Lane, s.cfg.Now())
	return true
}

func (s *Scheduler) releaseFor(w *waiter) Release {
	var once sync.Once
	return func(actual int) {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			now := s.cfg.Now()
			s.lanes[w.req.Lane].inFlight--
			if w.req.Lane == LaneGraphQL {
				s.bucket.correct(w.req.Cost, actual)
				if actual > 0 {
					s.spend.add(now, actual-w.req.Cost)
				}
			}
			s.kickLocked(w.req.Lane, now)
		})
	}
}

// expectedWaitLocked estimates how long r would wait if enqueued now: the
// lane cooldown, or the bucket refill needed to clear its reserve floor
// AFTER everything already queued at its priority or higher has taken its
// points. Without the queued-ahead term a burst of P0s each looked
// admissible and the last of them outlived its deadline into a 504.
func (s *Scheduler) expectedWaitLocked(now time.Time, r Request) time.Duration {
	ln := &s.lanes[r.Lane]
	wait := time.Duration(0)
	if ln.cooldownUntil.After(now) {
		wait = ln.cooldownUntil.Sub(now)
	}
	if r.Lane == LaneGraphQL {
		ahead := 0
		for _, w := range ln.queue {
			if !w.cancelled && !w.dispatched && w.req.Priority <= r.Priority {
				ahead += w.req.Cost
			}
		}
		want := float64(r.Cost+ahead) + s.cfg.ReserveFloor[r.Priority]*s.cfg.Capacity
		if d := s.bucket.deficitWait(want); d > wait {
			wait = d
		}
	}
	return wait
}

// kickLocked dispatches from the head of lane's queue while the head can go,
// and otherwise arms a timer for the earliest moment it could.
func (s *Scheduler) kickLocked(lane Lane, now time.Time) {
	ln := &s.lanes[lane]
	if ln.timer != nil {
		ln.timer.Stop()
		ln.timer = nil
	}
	s.bucket.refill(now)
	for ln.queue.Len() > 0 {
		w := ln.queue[0]
		if w.cancelled {
			heap.Pop(&ln.queue)
			continue
		}
		if ln.inFlight >= ln.max {
			return // a Release will kick again
		}
		var wait time.Duration
		if ln.cooldownUntil.After(now) {
			wait = ln.cooldownUntil.Sub(now)
		} else if lane == LaneGraphQL {
			want := float64(w.req.Cost) + s.cfg.ReserveFloor[w.req.Priority]*s.cfg.Capacity
			wait = s.bucket.deficitWait(want)
		}
		if wait > 0 {
			if s.closed {
				return
			}
			ln.timer = time.AfterFunc(wait, func() { s.tick(lane) })
			return
		}
		heap.Pop(&ln.queue)
		ln.inFlight++
		if lane == LaneGraphQL {
			s.bucket.take(float64(w.req.Cost))
			s.spend.add(now, w.req.Cost)
		}
		if waited := now.Sub(w.enqueued); waited >= s.cfg.SlowWait {
			s.slowAt = now
		}
		w.dispatched = true
		w.ready <- struct{}{}
	}
}

// tick is the timer callback: re-evaluate lane's head.
func (s *Scheduler) tick(lane Lane) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.kickLocked(lane, s.cfg.Now())
}

// Trip records a real upstream 429 on lane: start (or extend) its cooldown
// from Retry-After, else the default, escalating on repeats within a minute;
// and resync the bucket to the remaining quota the gateway reported.
func (s *Scheduler) Trip(lane Lane, retryAfter time.Duration, remaining int, hasRemaining bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()
	ln := &s.lanes[lane]
	d := retryAfter
	if d <= 0 {
		d = s.cfg.DefaultCooldown
		if !ln.lastTrip.IsZero() && now.Sub(ln.lastTrip) < time.Minute && ln.lastCooldown > 0 {
			d = time.Duration(float64(ln.lastCooldown) * 1.5)
		}
	}
	if d > s.cfg.MaxCooldown {
		d = s.cfg.MaxCooldown
	}
	until := now.Add(d)
	if until.After(ln.cooldownUntil) {
		ln.cooldownUntil = until
	}
	ln.lastTrip, ln.lastCooldown = now, d
	s.trips++
	s.slowAt = now
	if lane == LaneGraphQL && hasRemaining {
		// The gateway's remaining is against FullQuota; our bucket models
		// only Capacity of it, so the headroom is already spent from our
		// point of view.
		s.bucket.setLevel(float64(remaining) - (s.cfg.FullQuota - s.cfg.Capacity))
	}
	s.kickLocked(lane, now)
}

// Snapshot reports the current state.
func (s *Scheduler) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()
	s.bucket.refill(now)
	var snap Snapshot
	snap.Capacity = int(s.cfg.Capacity)
	snap.Available = int(s.bucket.available())
	snap.UsedLastMinute = s.spend.sum(now)
	snap.InFlight = s.lanes[LaneGraphQL].inFlight
	snap.InFlightREST = s.lanes[LaneREST].inFlight
	snap.Refused = s.refused
	snap.Trips = s.trips
	for i := range s.lanes {
		for _, w := range s.lanes[i].queue {
			if !w.cancelled && !w.dispatched {
				snap.Queue[w.req.Priority]++
			}
		}
	}
	if u := s.lanes[LaneGraphQL].cooldownUntil; u.After(now) {
		snap.CooldownUntil = u
		snap.RetryAfter = u.Sub(now)
	}
	if u := s.lanes[LaneREST].cooldownUntil; u.After(now) {
		snap.RESTCooldownUntil = u
		if d := u.Sub(now); d > snap.RetryAfter {
			snap.RetryAfter = d
		}
	}
	switch {
	case snap.RetryAfter > 0:
		snap.Level = "cooldown"
	case now.Sub(s.slowAt) < s.cfg.SlowWindow, float64(snap.Available) < 0.25*s.cfg.Capacity:
		snap.Level = "slow"
	default:
		snap.Level = "ok"
	}
	snap.Throttled = snap.Level != "ok"
	return snap
}
