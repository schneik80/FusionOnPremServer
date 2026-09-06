// Package apsbudget is the shared quota layer every Autodesk Platform Services
// call passes through: a points token bucket, a priority scheduler, a cooldown
// driven by real 429s, and a short-TTL coalescing cache. See
// docs/quota/STATUS.md for the design and the APS facts it rests on.
//
// The package imports nothing from api or server; api routes its transport
// through it and server reads its snapshot.
package apsbudget

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Lane separates the two upstream quota regimes. The Manufacturing Data Model
// GraphQL gateway meters query points per minute per app; the Data Management
// / OSS REST endpoints meter requests per minute per endpoint. They cool down
// independently and only the GraphQL lane spends points.
type Lane int

const (
	LaneGraphQL Lane = iota
	LaneREST
)

func (l Lane) String() string {
	if l == LaneREST {
		return "rest"
	}
	return "graphql"
}

// RateLimitError is the typed form of an upstream 429 (or of the scheduler's
// own refusal when the wait it would impose exceeds what the caller can bear).
// Handlers map it to HTTP 429 with a Retry-After header and a retryAfterMs body
// field; nothing else in the app should string-match on "429".
type RateLimitError struct {
	// RetryAfter is how long the caller should wait before trying again: the
	// upstream Retry-After header when present, else the scheduler's cooldown.
	RetryAfter time.Duration
	Lane       Lane
	// PointValue and Remaining are parsed from the MDM 429 message
	// ("…point value 231 and remaining quota 69…"); zero when absent.
	PointValue int
	Remaining  int
	// Queued is true when the refusal came from our own scheduler (the budget
	// is spent or cooling down), not from an APS response. It is expected
	// behaviour in slow mode and logs at Info, not Error.
	Queued bool
	// Upstream is the raw upstream body, kept for -v diagnostics only.
	Upstream string
}

func (e *RateLimitError) Error() string {
	// The prefix is load-bearing: api/client_test.go asserts it and it is what
	// operators grep for in logs.
	var b strings.Builder
	b.WriteString("HTTP 429 rate limited")
	if e.RetryAfter > 0 {
		fmt.Fprintf(&b, " (Retry-After: %s)", e.RetryAfter.Round(time.Second))
	}
	if e.Queued {
		fmt.Fprintf(&b, " [local %s budget]", e.Lane)
	}
	if e.PointValue > 0 || e.Remaining > 0 {
		fmt.Fprintf(&b, " [point value %d, remaining %d]", e.PointValue, e.Remaining)
	}
	if e.Upstream != "" {
		b.WriteString(": ")
		b.WriteString(e.Upstream)
	}
	return b.String()
}

// QueryTooComplexError is the deterministic 400 the MDM gateway returns when a
// single query's point value exceeds the per-query cap (1000). It is a code
// bug — the query must select fewer fields or a smaller page — never load, so
// it is never retried and never trips a cooldown.
type QueryTooComplexError struct {
	Op     string
	Points int
	Max    int
	Body   string
}

func (e *QueryTooComplexError) Error() string {
	return fmt.Sprintf("query %q too complex: %d points exceeds the per-query maximum of %d", e.Op, e.Points, e.Max)
}

// quotaMessageRe matches the MDM per-minute quota sentence. Both numbers are
// exact: the point value is the cost of the rejected query, the remaining quota
// is the bucket level the gateway saw. Together they are the only budget
// telemetry MDM exposes today.
// The gateway has been seen phrasing it both "point value 231" and "point
// value of 26" (server.log, 2026-09-05), so the "of" is optional.
var quotaMessageRe = regexp.MustCompile(`point value (?:of )?(\d+) and remaining quota (\d+)`)

// ParseQuotaMessage extracts point value and remaining quota from an MDM 429
// message. ok is false when the sentence is absent.
func ParseQuotaMessage(msg string) (pointValue, remaining int, ok bool) {
	m := quotaMessageRe.FindStringSubmatch(msg)
	if m == nil {
		return 0, 0, false
	}
	pv, err1 := strconv.Atoi(m[1])
	rem, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return pv, rem, true
}

// tooComplexRe matches the per-query cap rejection.
var tooComplexRe = regexp.MustCompile(`[Qq]uery point value (\d+) exceeds maximum allowed query point value (\d+)`)

// ParseTooComplex extracts the rejected query's cost and the cap. ok is false
// when the sentence is absent.
func ParseTooComplex(msg string) (points, max int, ok bool) {
	m := tooComplexRe.FindStringSubmatch(msg)
	if m == nil {
		return 0, 0, false
	}
	p, err1 := strconv.Atoi(m[1])
	mx, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return p, mx, true
}

// ParseRetryAfter reads an HTTP Retry-After value: either delay-seconds or an
// HTTP-date. ok is false for an empty or unparseable header. A date in the
// past yields zero with ok=true (the wait is over).
func ParseRetryAfter(h string, now time.Time) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := parseHTTPDate(h); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

func parseHTTPDate(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("not an HTTP date: %q", s)
}
