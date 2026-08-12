package server

import (
	"testing"
	"time"
)

// newTicket builds a live ticket for the given session.
func newTicket(id, sessionID string) *fusionTicket {
	return &fusionTicket{
		ID:          id,
		SessionID:   sessionID,
		UserKey:     "user-1",
		Action:      "open",
		FileID:      "urn:adsk.wipprod:dm.lineage:abc",
		DMProjectID: "a.YnVzaW5lc3M6YXV0b2Rlc2s4MDgzIzIwMjUwMjEzODc2NjAyNTMx",
		HubID:       "hub-1",
		ExpiresAt:   time.Now().Add(fusionTicketTTL),
	}
}

func TestTicket_RedeemOnce(t *testing.T) {
	s := newFusionTicketStore()
	s.add(newTicket("t1", "sess-1"))

	got, ok := s.redeem("t1")
	if !ok {
		t.Fatal("first redeem: want ok")
	}
	if got.FileID == "" || got.Action != "open" {
		t.Errorf("redeemed payload = %+v, want the action and file", got)
	}
	// A replayed fusionlocal:// URL must do nothing.
	if _, ok := s.redeem("t1"); ok {
		t.Error("second redeem: want refusal, got ok")
	}
}

func TestTicket_RedeemRejectsUnknownAndExpired(t *testing.T) {
	s := newFusionTicketStore()
	expired := newTicket("old", "sess-1")
	expired.ExpiresAt = time.Now().Add(-time.Second)
	s.add(expired)

	if _, ok := s.redeem("nope"); ok {
		t.Error("unknown ticket: want refusal")
	}
	if _, ok := s.redeem(""); ok {
		t.Error("empty ticket id: want refusal")
	}
	if _, ok := s.redeem("old"); ok {
		t.Error("expired ticket: want refusal")
	}
}

func TestTicket_ReportRequiresRedemption(t *testing.T) {
	s := newFusionTicketStore()
	s.add(newTicket("t1", "sess-1"))

	// The callback is unauthenticated, so it must not be usable to report on a
	// ticket the helper never collected.
	if _, ok := s.report("t1", false, "fusion_not_running"); ok {
		t.Fatal("report before redeem: want refusal, got ok")
	}

	if _, ok := s.redeem("t1"); !ok {
		t.Fatal("redeem: want ok")
	}
	if _, ok := s.report("t1", false, "fusion_not_running"); !ok {
		t.Fatal("report after redeem: want ok")
	}
	// And only once — a second callback cannot overwrite the outcome or emit a
	// second notification.
	if _, ok := s.report("t1", true, ""); ok {
		t.Error("second report: want refusal, got ok")
	}
}

func TestTicket_OutcomeIsSessionScoped(t *testing.T) {
	s := newFusionTicketStore()
	s.add(newTicket("t1", "sess-1"))

	if _, ok := s.outcome("t1", "sess-2"); ok {
		t.Error("another session read the outcome; want refusal")
	}
	got, ok := s.outcome("t1", "sess-1")
	if !ok {
		t.Fatal("owning session: want ok")
	}
	if got.Reported {
		t.Error("fresh ticket reports Reported = true, want false (pending)")
	}

	_, _ = s.redeem("t1")
	_, _ = s.report("t1", false, "fusion_wrong_hub")
	got, _ = s.outcome("t1", "sess-1")
	if !got.Reported || got.OK || got.ErrCode != "fusion_wrong_hub" {
		t.Errorf("outcome = %+v, want reported failure with the wrong-hub code", got)
	}
}

func TestTicket_SweepDropsExpiredButKeepsFreshOutcomes(t *testing.T) {
	s := newFusionTicketStore()

	s.add(newTicket("live", "sess-1"))

	s.add(newTicket("reported", "sess-1"))
	_, _ = s.redeem("reported")
	_, _ = s.report("reported", true, "")

	// Added last: add() sweeps before inserting, so an expired ticket added
	// first would already be gone and this would assert nothing.
	stale := newTicket("stale", "sess-1")
	stale.ExpiresAt = time.Now().Add(-time.Minute)
	s.add(stale)

	if n := s.sweep(time.Now()); n != 1 {
		t.Errorf("sweep removed %d, want 1 (only the expired, unredeemed ticket)", n)
	}
	if _, ok := s.outcome("live", "sess-1"); !ok {
		t.Error("live ticket was swept")
	}
	// A reported ticket must survive long enough for the SPA to read it.
	if _, ok := s.outcome("reported", "sess-1"); !ok {
		t.Error("freshly reported ticket was swept before the SPA could poll it")
	}

	// Once the outcome window passes, it goes too.
	if n := s.sweep(time.Now().Add(fusionOutcomeTTL + time.Minute)); n != 2 {
		t.Errorf("late sweep removed %d, want 2", n)
	}
}

func TestTicket_AddSweepsOpportunistically(t *testing.T) {
	// The janitor may never run (tests, hand-built servers), so add() must
	// keep the map from growing without bound on its own.
	s := newFusionTicketStore()
	for i := range 50 {
		dead := newTicket(string(rune('a'+i%26))+string(rune('a'+i/26)), "sess-1")
		dead.ExpiresAt = time.Now().Add(-time.Hour)
		s.add(dead)
	}
	s.add(newTicket("fresh", "sess-1"))

	s.mu.Lock()
	n := len(s.tickets)
	s.mu.Unlock()
	if n != 1 {
		t.Errorf("store holds %d tickets after adding one fresh among 50 dead, want 1", n)
	}
}
