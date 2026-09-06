package apsbudget

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseQuotaMessage(t *testing.T) {
	msg := "Query point value per minute quota exceeded with point value 231 and remaining quota 69. Please try again later."
	pv, rem, ok := ParseQuotaMessage(msg)
	if !ok || pv != 231 || rem != 69 {
		t.Fatalf("got (%d,%d,%v), want (231,69,true)", pv, rem, ok)
	}
	if _, _, ok := ParseQuotaMessage(`{"developerMessage":"Quota limit exceeded."}`); ok {
		t.Fatal("non-MDM body must not parse")
	}
}

func TestParseTooComplex(t *testing.T) {
	msg := "Query point value 1231 exceeds maximum allowed query point value 1000. To reduce point value, consider setting a lower pagination limit or reducing the number of fields requested."
	p, mx, ok := ParseTooComplex(msg)
	if !ok || p != 1231 || mx != 1000 {
		t.Fatalf("got (%d,%d,%v)", p, mx, ok)
	}
	if _, _, ok := ParseTooComplex("Current query complexity of 76 exceeds the limit of 75."); ok {
		t.Fatal("complexity message is a different error and must not parse")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"42", 42 * time.Second, true},
		{" 3 ", 3 * time.Second, true},
		{"0", 0, true},
		{"-1", 0, false},
		{"", 0, false},
		{"soon", 0, false},
		{now.Add(25 * time.Second).Format(time.RFC1123), 25 * time.Second, true},
		{now.Add(-25 * time.Second).Format(time.RFC1123), 0, true},
	}
	for _, c := range cases {
		got, ok := ParseRetryAfter(c.in, now)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseRetryAfter(%q) = (%s,%v), want (%s,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestRateLimitError_Text(t *testing.T) {
	e := &RateLimitError{RetryAfter: 42 * time.Second, PointValue: 231, Remaining: 69, Upstream: "body"}
	msg := e.Error()
	for _, want := range []string{"HTTP 429 rate limited", "Retry-After: 42s", "point value 231", "body"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q lacks %q", msg, want)
		}
	}
	var rl *RateLimitError
	if !errors.As(error(e), &rl) {
		t.Fatal("errors.As must find the typed error")
	}
	q := &RateLimitError{RetryAfter: 5 * time.Second, Queued: true, Lane: LaneGraphQL}
	if !strings.Contains(q.Error(), "[local graphql budget]") {
		t.Errorf("queued text = %q", q.Error())
	}
}

func TestPriorityContext(t *testing.T) {
	ctx := t.Context()
	if PriorityFrom(ctx) != P1 {
		t.Fatal("absent priority must default to P1")
	}
	if PriorityFrom(WithPriority(ctx, P2)) != P2 || PriorityFrom(WithPriority(ctx, 9)) != P2 || PriorityFrom(WithPriority(ctx, -3)) != P0 {
		t.Fatal("priority not carried or clamped")
	}
	if SubjectFrom(ctx) != "" || SubjectFrom(WithSubject(ctx, "sub-1")) != "sub-1" {
		t.Fatal("subject not carried")
	}
	if FreshFrom(ctx) || !FreshFrom(WithFresh(ctx)) {
		t.Fatal("fresh not carried")
	}
}
