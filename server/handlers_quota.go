package server

import (
	"net/http"
	"strconv"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// QuotaDTO is GET /api/quota: the budget layer's state, for the SPA's slow-
// mode banner and for operators. Cheap — one mutex snapshot, no APS call.
type QuotaDTO struct {
	Level             string `json:"level"` // ok | slow | cooldown
	Throttled         bool   `json:"throttled"`
	CooldownUntil     string `json:"cooldownUntil,omitempty"`
	RestCooldownUntil string `json:"restCooldownUntil,omitempty"`
	RetryAfterMs      int64  `json:"retryAfterMs"`
	Capacity          int    `json:"capacity"`
	Available         int    `json:"available"`
	PointsUsedLastMin int    `json:"pointsUsedLastMinute"`
	InFlight          int    `json:"inFlight"`
	InFlightRest      int    `json:"inFlightRest"`
	Queued            struct {
		P0 int `json:"p0"`
		P1 int `json:"p1"`
		P2 int `json:"p2"`
	} `json:"queued"`
	Refused int64 `json:"refused"`
	Trips   int64 `json:"trips"`
	Cache   struct {
		Entries   int   `json:"entries"`
		Bytes     int64 `json:"bytes"`
		Hits      int64 `json:"hits"`
		Misses    int64 `json:"misses"`
		Coalesced int64 `json:"coalesced"`
	} `json:"cache"`
	// Enabled is false when no scheduler is installed (tests, pass-through).
	Enabled bool `json:"enabled"`
}

func apiBudget() *apsbudget.Scheduler { return api.Budget() }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func quotaDTO() QuotaDTO {
	var d QuotaDTO
	d.Level = "ok"
	b := apiBudget()
	if b == nil {
		return d
	}
	d.Enabled = true
	snap := b.Snapshot()
	d.Level, d.Throttled = snap.Level, snap.Throttled
	if !snap.CooldownUntil.IsZero() {
		d.CooldownUntil = fmtTime(snap.CooldownUntil)
	}
	if !snap.RESTCooldownUntil.IsZero() {
		d.RestCooldownUntil = fmtTime(snap.RESTCooldownUntil)
	}
	d.RetryAfterMs = snap.RetryAfter.Milliseconds()
	d.Capacity, d.Available, d.PointsUsedLastMin = snap.Capacity, snap.Available, snap.UsedLastMinute
	d.InFlight, d.InFlightRest = snap.InFlight, snap.InFlightREST
	d.Queued.P0, d.Queued.P1, d.Queued.P2 = snap.Queue[0], snap.Queue[1], snap.Queue[2]
	d.Refused, d.Trips = snap.Refused, snap.Trips
	cs := api.CacheStats()
	d.Cache.Entries, d.Cache.Bytes, d.Cache.Hits, d.Cache.Misses, d.Cache.Coalesced = cs.Entries, cs.Bytes, cs.Hits, cs.Misses, cs.Coalesced
	return d
}

// handleQuota is GET /api/quota. Session-only (bare prot): it describes the
// process, not a hub.
func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, quotaDTO())
}

// handleDebugQuotaCosts is GET /api/debug/quota-costs (-v only): every
// registered operation with its static estimate and what the gateway has
// actually charged, so the registry in api/cost.go can be calibrated.
func (s *Server) handleDebugQuotaCosts(w http.ResponseWriter, r *http.Request) {
	if !api.DebugEnabled() {
		http.NotFound(w, r)
		return
	}
	type row struct {
		Op         string                 `json:"op"`
		Registered int                    `json:"registered"`
		Effective  int                    `json:"effective"`
		Estimate   int                    `json:"estimate"`
		Measured   int                    `json:"measured"`
		CacheTTLMs int64                  `json:"cacheTtlMs"`
		Shared     bool                   `json:"shared"`
		Observed   *apsbudget.Observation `json:"observed,omitempty"`
	}
	reg := api.OpCosts()
	var rows []row
	for _, t := range api.Costs().Table() {
		rw := row{Op: t.Op, Registered: t.Registered, Effective: t.Effective, Observed: t.Observed}
		if c, ok := reg[t.Op]; ok {
			rw.Estimate, rw.Measured, rw.CacheTTLMs, rw.Shared = c.Estimate(), c.Measured, c.CacheTTL.Milliseconds(), c.Shared
		}
		rows = append(rows, rw)
	}
	if rows == nil {
		rows = []row{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ops": rows, "quota": quotaDTO()})
}
