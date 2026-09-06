package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// The budget layer, as seen from api: every GraphQL round trip (gqlQueryAt)
// and every Data Management / OSS one (dmDo, doREST) acquires a slot from the
// process-wide scheduler, and GraphQL answers pass through the coalescing
// cache. Nothing in api touches httpClient for APS outside those two paths.
//
// budget is nil until server.Run calls UseBudget — tests and the CLI probes
// then run as pass-through — so no test needs a scheduler to exercise a
// query.
var (
	budget *apsbudget.Scheduler
	costs  = apsbudget.NewEstimator()
	// gqlCache coalesces identical GraphQL answers: 1024 entries / 32 MiB.
	gqlCache = apsbudget.NewCache[json.RawMessage](1024, 32<<20, apsbudget.RawSize, nil)
)

// RateLimitError is re-exported so handlers can errors.As without importing
// the internal package.
type RateLimitError = apsbudget.RateLimitError

func init() {
	for op, c := range opCosts {
		costs.Register(op, c.Points())
	}
}

// UseBudget installs the scheduler every APS call goes through. Call once at
// startup, like SetRegion.
func UseBudget(s *apsbudget.Scheduler) { budget = s }

// Budget returns the installed scheduler (nil when running pass-through).
func Budget() *apsbudget.Scheduler { return budget }

// Costs returns the estimator (registered + observed costs per operation).
func Costs() *apsbudget.Estimator { return costs }

// CacheStats reports the GraphQL coalescing cache counters.
func CacheStats() apsbudget.CacheStats { return gqlCache.Stats() }

// InvalidateSubject drops every cached GraphQL answer built for sub, so the
// author of a write (wiki publish, upload) sees it on the next fetch.
func InvalidateSubject(sub string) { gqlCache.InvalidateSubject(sub) }

// SetBudgetForTesting swaps the scheduler and returns a restore func. The
// cooldown is global state, so a test that trips it must restore.
func SetBudgetForTesting(s *apsbudget.Scheduler) (restore func()) {
	prev := budget
	budget = s
	return func() { budget = prev }
}

// acquire takes a scheduler slot for one round trip, or returns nil release
// when no scheduler is installed. The RateLimitError it may return is the
// scheduler's own refusal (Queued), already typed for the handler.
func acquire(ctx context.Context, lane apsbudget.Lane, op string, cost int) (apsbudget.Release, error) {
	if budget == nil {
		return func(int) {}, nil
	}
	return budget.Acquire(ctx, apsbudget.Request{
		Lane:     lane,
		Priority: apsbudget.PriorityFrom(ctx),
		Op:       op,
		Cost:     cost,
		Label:    apsbudget.LabelFrom(ctx),
	})
}

// trip records a real upstream 429 with the scheduler.
func trip(rl *apsbudget.RateLimitError, op string) {
	if rl.PointValue > 0 && op != "" {
		costs.Observe(op, rl.PointValue, "429", time.Now())
	}
	if budget != nil {
		budget.Trip(rl.Lane, rl.RetryAfter, rl.Remaining, rl.Remaining > 0 || rl.PointValue > 0)
	}
}

// doREST is the metered form of client.Do for the few REST calls that do not
// go through dmDo (the archive job's no-redirect poll). The request's own
// context carries the priority.
func doREST(client *http.Client, req *http.Request) (*http.Response, error) {
	rel, err := acquire(req.Context(), apsbudget.LaneREST, "", 0)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	rel(0)
	if err == nil && resp.StatusCode == http.StatusTooManyRequests {
		// Let the caller read the body; but record the trip so the lane
		// cools down. RetryAfter from the header only.
		rl := &apsbudget.RateLimitError{Lane: apsbudget.LaneREST}
		if d, ok := apsbudget.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			rl.RetryAfter = d
		}
		trip(rl, "")
	}
	return resp, err
}
