package server

import (
	"context"
	"sync"
	"time"
)

// A Fusion action (Open / Insert) has to cross a trust boundary the rest of
// this app never does: the browser cannot reach the user's own machine, so a
// native helper app does the work. Everything about that handoff is built
// around one problem — a URL scheme is a PUBLIC entry point. Any web page in
// any browser can navigate to fusionlocal://…, so the URL itself must not be
// the authority for what happens.
//
// Hence a ticket. The SPA (authenticated, hub-locked) asks the server to mint
// one; the ticket holds the action and the document, is bound to that session,
// expires in two minutes, and can be redeemed exactly once. The URL handed to
// the OS carries only the ticket id. A ticket that leaks is worth one action,
// for two minutes, on a document its owner could already open — and a
// fabricated fusionlocal:// URL redeems to nothing at all.
//
// Tickets are in memory and never persisted: one is useless within minutes of
// being minted, so surviving a restart has no value.

const (
	// fusionTicketTTL bounds how long a minted ticket can be redeemed. Long
	// enough for a cold helper launch (including the OS's "open this app?"
	// prompt), short enough that a leaked URL is close to worthless.
	fusionTicketTTL = 2 * time.Minute

	// fusionOutcomeTTL is how long a redeemed ticket's outcome stays readable
	// by the SPA that started it. The SPA polls for a few seconds after
	// launching, so this only needs to outlive a slow Fusion.
	fusionOutcomeTTL = 5 * time.Minute

	// fusionTicketSweep is the janitor cadence — the same
	// don't-grow-without-bound posture as the session store's.
	fusionTicketSweep = time.Minute
)

// fusionTicket is one grant to perform one action on one document.
type fusionTicket struct {
	ID        string
	SessionID string
	UserKey   string // inbox key, for the failure notification
	Action    string
	FileID    string // lineage urn — what Fusion opens or inserts
	// DMProjectID lets the helper check that Fusion is signed in to the same
	// hub before acting, rather than opening a document from the wrong place.
	DMProjectID string
	ProjectID   string
	ProjectName string
	DocName     string
	HubID       string
	ExpiresAt   time.Time

	// Redeemed is set when the helper collects the payload; a second attempt
	// is refused. Outcome fields are filled by the helper's callback.
	Redeemed   bool
	Reported   bool
	OK         bool
	ErrCode    string
	ReportedAt time.Time
}

// expired reports whether a ticket may no longer be redeemed. A reported
// ticket lingers past its TTL only so the SPA can read the outcome.
func (t *fusionTicket) expired(now time.Time) bool { return now.After(t.ExpiresAt) }

// prunable reports whether a ticket can be dropped entirely: unredeemed and
// expired, or reported long enough ago that nobody is still polling.
func (t *fusionTicket) prunable(now time.Time) bool {
	if t.Reported {
		return now.Sub(t.ReportedAt) > fusionOutcomeTTL
	}
	return t.expired(now)
}

// fusionTicketStore holds live tickets by id.
type fusionTicketStore struct {
	mu      sync.Mutex
	tickets map[string]*fusionTicket
}

func newFusionTicketStore() *fusionTicketStore {
	return &fusionTicketStore{tickets: map[string]*fusionTicket{}}
}

// add stores a freshly minted ticket, sweeping dead ones on the way through so
// the map cannot grow without bound even if the janitor never runs (tests, or
// a server that is only ever hand-built).
func (s *fusionTicketStore) add(t *fusionTicket) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, existing := range s.tickets {
		if existing.prunable(now) {
			delete(s.tickets, id)
		}
	}
	s.tickets[t.ID] = t
}

// redeem hands the payload to the helper and marks the ticket used. The second
// call for the same id fails: a replayed fusionlocal:// URL does nothing.
func (s *fusionTicketStore) redeem(id string) (fusionTicket, bool) {
	if id == "" {
		return fusionTicket{}, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	if !ok || t.Redeemed || t.expired(now) {
		return fusionTicket{}, false
	}
	t.Redeemed = true
	return *t, true
}

// report records the helper's outcome. Only a redeemed ticket can be reported
// on, and only once — so the callback endpoint, which has no session cookie,
// cannot be used to write arbitrary outcomes or to spam notifications.
func (s *fusionTicketStore) report(id string, ok bool, errCode string) (fusionTicket, bool) {
	if id == "" {
		return fusionTicket{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, found := s.tickets[id]
	if !found || !t.Redeemed || t.Reported {
		return fusionTicket{}, false
	}
	t.Reported = true
	t.OK = ok
	t.ErrCode = errCode
	t.ReportedAt = time.Now()
	return *t, true
}

// outcome reports what happened to a ticket, for the SPA that started it.
// Scoped to the originating session so one user cannot watch another's.
func (s *fusionTicketStore) outcome(id, sessionID string) (fusionTicket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	if !ok || t.SessionID != sessionID {
		return fusionTicket{}, false
	}
	return *t, true
}

// StartJanitor drops dead tickets periodically until ctx is cancelled,
// mirroring the session store's janitor. add() also sweeps opportunistically,
// so this only matters for a server that mints a burst and then goes quiet.
func (s *fusionTicketStore) StartJanitor(ctx context.Context) {
	go func() {
		t := time.NewTicker(fusionTicketSweep)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweep(time.Now())
			}
		}
	}()
}

// sweep drops dead tickets. Called on a timer by the janitor.
func (s *fusionTicketStore) sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, t := range s.tickets {
		if t.prunable(now) {
			delete(s.tickets, id)
			n++
		}
	}
	return n
}
