package apsbudget

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.t }
func (f *fakeClock) add(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

// The fake clock starts at the real now: context deadlines are real time,
// and the pre-check compares the two.
func testConfig(clk *fakeClock) Config {
	c := DefaultConfig()
	c.FullQuota, c.Capacity, c.RefillPerSec = 1000, 800, 100
	c.MaxInFlight = 1
	c.MaxQueueWait = [numPriorities]time.Duration{0, 10 * time.Second, 2 * time.Second}
	c.DefaultCooldown, c.MaxCooldown = 4*time.Second, 10*time.Second
	c.Now = clk.now
	return c
}

func TestBucket(t *testing.T) {
	now := time.Unix(0, 0)
	b := newBucket(100, 10, now)
	b.take(90)
	if b.available() != 10 {
		t.Fatal(b.available())
	}
	b.refill(now.Add(5 * time.Second))
	if b.available() != 60 {
		t.Fatalf("refill = %v", b.available())
	}
	b.refill(now.Add(time.Hour))
	if b.available() != 100 {
		t.Fatal("must clamp at capacity")
	}
	b.take(50)
	b.correct(50, 20) // over-estimated → refund 30
	if b.available() != 80 {
		t.Fatal(b.available())
	}
	b.correct(20, 70) // under-estimated → charge 50
	if b.available() != 30 {
		t.Fatal(b.available())
	}
	b.setLevel(90) // never upward
	if b.available() != 30 {
		t.Fatal("setLevel must not raise")
	}
	b.setLevel(-500)
	if b.available() != -100 {
		t.Fatal("setLevel clamps at -capacity")
	}
	if d := b.deficitWait(0); d != 10*time.Second {
		t.Fatalf("deficitWait = %s", d)
	}
}

func TestEstimator(t *testing.T) {
	e := NewEstimator()
	if e.Points("X", 7) != 7 {
		t.Fatal("fallback")
	}
	e.Register("X", 120, false)
	if e.Points("X", 7) != 120 {
		t.Fatal("registered")
	}
	e.Observe("X", 231, "429", time.Unix(0, 0))
	if e.Points("X", 7) != 231 {
		t.Fatal("observation must win for a fixed-cost op")
	}
	e.Observe("X", 0, "429", time.Unix(0, 0)) // ignored
	// A paged op keeps its full-page estimate: one short page's exact cost
	// says nothing about the next long one.
	e.Register("P", 666, true)
	e.Observe("P", 120, "429", time.Unix(0, 0))
	if e.Points("P", 7) != 666 {
		t.Fatal("paged op must keep the registered estimate")
	}
	rows := e.Table()
	if len(rows) != 2 || rows[0].Op != "P" || rows[0].Effective != 666 || !rows[0].Paged || rows[1].Observed == nil || rows[1].Observed.N != 1 || rows[1].Effective != 231 {
		t.Fatalf("table = %+v", rows)
	}
	if OperationName("query GetHubs($x: ID) { hubs }") != "GetHubs" || OperationName("{ hubs }") != "" {
		t.Fatal("OperationName")
	}
}

// acquireAsync starts Acquire in a goroutine and reports its result.
func acquireAsync(s *Scheduler, ctx context.Context, r Request) (<-chan Release, <-chan error) {
	relc := make(chan Release, 1)
	errc := make(chan error, 1)
	go func() {
		rel, err := s.Acquire(ctx, r)
		if err != nil {
			errc <- err
			return
		}
		relc <- rel
	}()
	return relc, errc
}

func TestScheduler_PriorityOrder(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	s := New(testConfig(clk), nil)
	defer s.Close()
	ctx := context.Background()

	hold, err := s.Acquire(ctx, Request{Priority: P0, Cost: 10})
	if err != nil {
		t.Fatal(err)
	}
	// With MaxInFlight=1 these all queue; enqueue order P2, P1, P0.
	r2, e2 := acquireAsync(s, ctx, Request{Priority: P2, Cost: 10})
	time.Sleep(10 * time.Millisecond)
	r1, e1 := acquireAsync(s, ctx, Request{Priority: P1, Cost: 10})
	time.Sleep(10 * time.Millisecond)
	r0, e0 := acquireAsync(s, ctx, Request{Priority: P0, Cost: 10})
	time.Sleep(10 * time.Millisecond)
	if snap := s.Snapshot(); snap.Queue != [numPriorities]int{1, 1, 1} || snap.InFlight != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}

	next := func(want string, rc <-chan Release, ec <-chan error, others ...<-chan Release) Release {
		t.Helper()
		select {
		case rel := <-rc:
			for _, o := range others {
				select {
				case <-o:
					t.Fatalf("%s: a lower priority was dispatched too", want)
				default:
				}
			}
			return rel
		case err := <-ec:
			t.Fatalf("%s: %v", want, err)
		case <-time.After(time.Second):
			t.Fatalf("%s: not dispatched", want)
		}
		return nil
	}
	hold(0)
	rel0 := next("P0", r0, e0, r1, r2)
	rel0(0)
	rel1 := next("P1", r1, e1, r2)
	rel1(0)
	rel2 := next("P2", r2, e2)
	rel2(0)
	if snap := s.Snapshot(); snap.InFlight != 0 || snap.Available != 760 {
		t.Fatalf("after release: %+v", snap)
	}
}

func TestScheduler_ReserveFloorsAndRefusal(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	cfg := testConfig(clk)
	cfg.MaxInFlight = 8
	s := New(cfg, nil)
	defer s.Close()
	ctx := context.Background()

	// Drain to 200 of 800 (25 %).
	rel, err := s.Acquire(ctx, Request{Priority: P0, Cost: 600})
	if err != nil {
		t.Fatal(err)
	}
	rel(0)
	// P2 needs 35 % (280) left after taking: 200-10 < 280 → wait would be
	// (290-200)/100 = 0.9 s < 2 s max, so it queues rather than refuses.
	// Make the deficit large instead: cost 400 → wait 4.8 s > 2 s → refused.
	_, err = s.Acquire(ctx, Request{Priority: P2, Cost: 400})
	var rl *RateLimitError
	if !errors.As(err, &rl) || !rl.Queued || rl.RetryAfter <= 0 {
		t.Fatalf("P2 must be refused with a countdown, got %v", err)
	}
	// P0 may drain the bucket: same cost is admitted immediately.
	rel, err = s.Acquire(ctx, Request{Priority: P0, Cost: 200})
	if err != nil {
		t.Fatal(err)
	}
	rel(0)
	snap := s.Snapshot()
	if snap.Refused != 1 || snap.Level != "slow" {
		t.Fatalf("snapshot = %+v", snap)
	}
	// Deadline shorter than the wait → refused immediately, not parked.
	dctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = s.Acquire(dctx, Request{Priority: P0, Cost: 700})
	if !errors.As(err, &rl) || !rl.Queued {
		t.Fatalf("deadline pre-check: %v", err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("pre-check must be immediate")
	}
}

func TestScheduler_TripCooldownAndResync(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	s := New(testConfig(clk), nil)
	defer s.Close()
	ctx := context.Background()

	s.Trip(LaneGraphQL, 3*time.Second, 69, true)
	snap := s.Snapshot()
	if snap.Level != "cooldown" || snap.RetryAfter != 3*time.Second || snap.Trips != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	// remaining 69 of a 1000 quota with 800 capacity → level 69-200 = -131.
	if snap.Available != -131 {
		t.Fatalf("available = %d, want -131", snap.Available)
	}
	// P2 is refused at once (3 s cooldown > 2 s max wait) …
	_, err := s.Acquire(ctx, Request{Priority: P2, Cost: 1})
	var rl *RateLimitError
	if !errors.As(err, &rl) || !rl.Queued {
		t.Fatalf("P2 during cooldown: %v", err)
	}
	// … while a P0 waits it out: advance the clock past the cooldown and
	// the deficit, then tick.
	rc, ec := acquireAsync(s, ctx, Request{Priority: P0, Cost: 10})
	select {
	case <-rc:
		t.Fatal("P0 dispatched during cooldown")
	case err := <-ec:
		t.Fatalf("P0 refused: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	clk.add(5 * time.Second) // cooldown over, bucket at -131+500 = 369
	s.tick(LaneGraphQL)
	select {
	case rel := <-rc:
		rel(0)
	case err := <-ec:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("P0 not dispatched after cooldown")
	}
	// REST lane is independent.
	s.Trip(LaneREST, 0, 0, false)
	if snap := s.Snapshot(); snap.RESTCooldownUntil.IsZero() || snap.RetryAfter != 4*time.Second {
		t.Fatalf("rest cooldown: %+v", snap)
	}
	// Escalation on a repeat trip without Retry-After: 4 s → 6 s.
	s.Trip(LaneREST, 0, 0, false)
	if snap := s.Snapshot(); snap.RetryAfter != 6*time.Second {
		t.Fatalf("escalated cooldown = %s", snap.RetryAfter)
	}
}

func TestScheduler_CancelledWaiterIsSkipped(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	s := New(testConfig(clk), nil)
	defer s.Close()
	ctx := context.Background()
	hold, _ := s.Acquire(ctx, Request{Priority: P0, Cost: 1})
	cctx, cancel := context.WithCancel(ctx)
	_, ec := acquireAsync(s, cctx, Request{Priority: P0, Cost: 1})
	time.Sleep(10 * time.Millisecond)
	rc2, ec2 := acquireAsync(s, ctx, Request{Priority: P1, Cost: 1})
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-ec; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	hold(0)
	select {
	case rel := <-rc2:
		rel(0)
	case err := <-ec2:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("P1 stuck behind a cancelled P0")
	}
	if snap := s.Snapshot(); snap.InFlight != 0 || snap.Queue != [numPriorities]int{} {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestScheduler_ReleaseCorrectsAndObserves(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	est := NewEstimator()
	s := New(testConfig(clk), est)
	defer s.Close()
	rel, err := s.Acquire(context.Background(), Request{Priority: P0, Cost: 100, Op: "GetX"})
	if err != nil {
		t.Fatal(err)
	}
	rel(40)
	rel(40) // idempotent
	snap := s.Snapshot()
	if snap.Available != 760 || snap.UsedLastMinute != 40 || snap.InFlight != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if est.Points("GetX", 0) != 0 {
		t.Fatal("the scheduler reconciles the bucket; observing is the transport's job")
	}
}

// TestScheduler_QueuedAheadCountsAndRefusesBeforeDeadline: with the bucket
// nearly empty, a second P0 sees the first one's cost in its expected wait;
// and a queued P0 whose wait outgrows its deadline is refused with a typed
// countdown before the context itself expires.
func TestScheduler_QueuedAheadCountsAndRefusesBeforeDeadline(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	cfg := testConfig(clk)
	cfg.MaxInFlight = 8
	s := New(cfg, nil)
	defer s.Close()
	ctx := context.Background()

	// Drain to ~0 of 800.
	rel, err := s.Acquire(ctx, Request{Priority: P0, Cost: 800})
	if err != nil {
		t.Fatal(err)
	}
	rel(0)
	// First P0 needs 300 → 3 s of refill: admissible under a 10 s deadline.
	d1, cancel1 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel1()
	rc1, ec1 := acquireAsync(s, d1, Request{Priority: P0, Cost: 300})
	time.Sleep(20 * time.Millisecond)
	// Second P0 needs 300 more → 6 s including the one ahead: refused at once
	// against a 4 s deadline, with the honest countdown.
	d2, cancel2 := context.WithTimeout(ctx, 4*time.Second)
	defer cancel2()
	_, err = s.Acquire(d2, Request{Priority: P0, Cost: 300})
	var rl *RateLimitError
	if !errors.As(err, &rl) || !rl.Queued || rl.RetryAfter < 5*time.Second {
		t.Fatalf("second P0 must be refused with the queued-ahead wait, got %v", err)
	}
	// A third P0 that fits its deadline at enqueue time but is then starved
	// (the clock never advances → no refill) is refused just BEFORE its
	// deadline, as a typed error, not as context.DeadlineExceeded.
	d3, cancel3 := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel3()
	start := time.Now()
	_, err = s.Acquire(d3, Request{Priority: P1, Cost: 1})
	if !errors.As(err, &rl) || !rl.Queued {
		t.Fatalf("starved waiter: %v (after %s)", err, time.Since(start))
	}
	if errors.Is(err, context.DeadlineExceeded) || time.Since(start) >= 600*time.Millisecond {
		t.Fatalf("must refuse before the deadline: %v after %s", err, time.Since(start))
	}
	// Let the first one through by refilling.
	clk.add(5 * time.Second)
	s.tick(LaneGraphQL)
	select {
	case r := <-rc1:
		r(0)
	case err := <-ec1:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("first P0 not dispatched")
	}
}
