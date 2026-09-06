package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/auth"
	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

func TestRequestPriority(t *testing.T) {
	cases := []struct {
		path, header, param string
		want                apsbudget.Priority
	}{
		{"/api/projects/contents", "", "", apsbudget.P0},
		{"/api/items/classify", "", "", apsbudget.P1},
		{"/api/activity/rollup", "", "", apsbudget.P1},
		{"/api/activity/rollup", "2", "", apsbudget.P2},
		{"/api/items/classify", "0", "", apsbudget.P0},
		{"/api/items/thumbnail/image", "", "2", apsbudget.P2},
		{"/api/items/thumbnail/image", "bogus", "x", apsbudget.P1},
		{"/api/debug/quota-costs", "", "", apsbudget.P2},
		{"/api/not/in/table", "", "", apsbudget.P0},
	}
	for _, c := range cases {
		url := c.path
		if c.param != "" {
			url += "?p=" + c.param
		}
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if c.header != "" {
			req.Header.Set(priorityHeader, c.header)
		}
		if got := requestPriority(req); got != c.want {
			t.Errorf("%s header=%q p=%q → %d, want %d", c.path, c.header, c.param, got, c.want)
		}
	}
}

// TestRequireAuth_BudgetContextAndThrottleHeader: the auth middleware is the
// choke point that tags every data request for the budget layer and reports
// the throttle level back.
func TestRequireAuth_BudgetContextAndThrottleHeader(t *testing.T) {
	sched := apsbudget.New(apsbudget.DefaultConfig(), nil)
	t.Cleanup(sched.Close)
	t.Cleanup(api.SetBudgetForTesting(sched))

	s := newAuthTestServer()
	sess, _ := s.sessions.Create(
		&auth.TokenData{AccessToken: "tok-123", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{Sub: "oidc-sub-1"},
	)
	var gotSub, gotLabel string
	var gotPrio apsbudget.Priority
	h := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSub = apsbudget.SubjectFrom(r.Context())
		gotPrio = apsbudget.PriorityFrom(r.Context())
		gotLabel = apsbudget.LabelFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/items/classify?cvId=x", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if gotSub != "oidc-sub-1" || gotPrio != apsbudget.P1 || gotLabel != "/api/items/classify" {
		t.Errorf("ctx = sub %q prio %d label %q", gotSub, gotPrio, gotLabel)
	}
	if got := rec.Header().Get(throttleHeader); got != "ok" {
		t.Errorf("%s = %q, want ok", throttleHeader, got)
	}
	if rec.Header().Get(throttleUntilHeader) != "" {
		t.Error("no until header when ok")
	}

	// In cooldown the headers say so, with a deadline.
	sched.Trip(apsbudget.LaneGraphQL, 30*time.Second, 0, false)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get(throttleHeader); got != "cooldown" {
		t.Errorf("%s = %q, want cooldown", throttleHeader, got)
	}
	if rec.Header().Get(throttleUntilHeader) == "" {
		t.Error("until header missing in cooldown")
	}

	// An identity-less session still gets a private subject.
	anon, _ := s.sessions.Create(&auth.TokenData{AccessToken: "t", ExpiresAt: time.Now().Add(time.Hour)}, auth.UserProfile{})
	req = httptest.NewRequest(http.MethodGet, "/api/hubs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: anon.ID})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if gotSub != "sess:"+anon.ID {
		t.Errorf("anonymous subject = %q", gotSub)
	}
}

func TestQuotaDTO(t *testing.T) {
	t.Cleanup(api.SetBudgetForTesting(nil))
	if d := quotaDTO(); d.Enabled || d.Level != "ok" {
		t.Errorf("pass-through dto = %+v", d)
	}
	sched := apsbudget.New(apsbudget.DefaultConfig(), nil)
	t.Cleanup(sched.Close)
	api.UseBudget(sched)
	sched.Trip(apsbudget.LaneGraphQL, 10*time.Second, 69, true)

	s := &Server{logger: quietLogger()}
	rec := httptest.NewRecorder()
	s.handleQuota(rec, httptest.NewRequest(http.MethodGet, "/api/quota", nil))
	var d QuotaDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if !d.Enabled || d.Level != "cooldown" || !d.Throttled || d.CooldownUntil == "" || d.RetryAfterMs <= 0 || d.Trips != 1 || d.Capacity != 4800 {
		t.Errorf("dto = %+v", d)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("quota must not be cached")
	}
}
