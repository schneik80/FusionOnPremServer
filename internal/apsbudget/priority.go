package apsbudget

import "context"

// Priority orders competing APS calls. Lower is more urgent.
//
//   - P0: the user is waiting on it — navigation, a details panel, a tab open.
//   - P1: per-row work for something on screen — classify, a thumbnail.
//   - P2: background — prefetch, warm-up, heavy walks, upload/archive jobs.
//
// The scheduler is strictly ordered: a P2 never dispatches while a P0 waits,
// and each class has its own reserve floor and maximum queue wait, so under
// pressure background work is refused quickly rather than delaying a click.
type Priority int

const (
	P0 Priority = iota
	P1
	P2
)

const numPriorities = 3

// clampPriority folds any out-of-range value onto the nearest class.
func clampPriority(p Priority) Priority {
	if p < P0 {
		return P0
	}
	if p > P2 {
		return P2
	}
	return p
}

type ctxKey int

const (
	priorityCtxKey ctxKey = iota
	subjectCtxKey
	labelCtxKey
	freshCtxKey
)

// WithPriority tags ctx with the priority every APS call made under it should
// carry. The server's auth middleware sets it from the route table or the
// X-FLS-Priority header; background jobs set it explicitly.
func WithPriority(ctx context.Context, p Priority) context.Context {
	return context.WithValue(ctx, priorityCtxKey, clampPriority(p))
}

// PriorityFrom returns the ctx priority, or P1 when none was set — the
// conservative middle for a call whose route nobody classified.
func PriorityFrom(ctx context.Context) Priority {
	if p, ok := ctx.Value(priorityCtxKey).(Priority); ok {
		return p
	}
	return P1
}

// WithSubject tags ctx with the calling user's stable identity (the OIDC
// subject). Coalescing keys include it so one user's authorised view is never
// served to another; a call without a subject is never coalesced.
func WithSubject(ctx context.Context, sub string) context.Context {
	return context.WithValue(ctx, subjectCtxKey, sub)
}

// SubjectFrom returns the ctx subject, or "" when none was set.
func SubjectFrom(ctx context.Context) string {
	s, _ := ctx.Value(subjectCtxKey).(string)
	return s
}

// WithLabel tags ctx with a human label (the request path) for metrics and -v
// logs only. It never influences scheduling.
func WithLabel(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, labelCtxKey, label)
}

// LabelFrom returns the ctx label, or "".
func LabelFrom(ctx context.Context) string {
	s, _ := ctx.Value(labelCtxKey).(string)
	return s
}

// WithFresh marks ctx as an explicit user refresh: the coalescing cache skips
// a stored hit (but still joins an in-flight fetch) so the user gets current
// data without the app spending twice on the same second.
func WithFresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshCtxKey, true)
}

// FreshFrom reports whether ctx carries the refresh mark.
func FreshFrom(ctx context.Context) bool {
	b, _ := ctx.Value(freshCtxKey).(bool)
	return b
}
