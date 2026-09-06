package api

import (
	"context"
	"errors"

	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// fanoutSem bounds the goroutines a server-side walk spawns (descendants,
// activity roll-up, the permissions path) across EVERY request, not per call:
// N concurrent walks used to mean N×12 in flight. The scheduler bounds the
// round trips themselves; this bounds the goroutines waiting on it.
//
// Lock order (see apsbudget.Scheduler): a fanoutSem slot is taken BEFORE the
// scheduler slot inside gqlQueryAt and released after the round trip; a
// scheduler slot is never held while waiting for fanoutSem. No cycle.
var fanoutSem = make(chan struct{}, 12)

// acquireFanout takes a fan-out slot or returns ctx.Err().
func acquireFanout(ctx context.Context) (release func(), err error) {
	select {
	case fanoutSem <- struct{}{}:
		return func() { <-fanoutSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// IsRateLimited reports whether err wraps the typed 429. A fan-out that
// skips per-node errors must NOT skip this one: a silently shorter tree is
// the "never cap silently" violation, and every further node would only
// spend more of a quota that is already gone.
func IsRateLimited(err error) bool {
	var rl *apsbudget.RateLimitError
	return errors.As(err, &rl)
}

// WithFanout runs fetch under the shared fan-out bound, for handlers that
// spawn one goroutine per layer/child.
func WithFanout[T any](ctx context.Context, fetch func() (T, error)) (T, error) {
	release, err := acquireFanout(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	defer release()
	return fetch()
}
