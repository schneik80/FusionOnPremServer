package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
)

// handleActivityReport -> api.GetDesignActivity + api.BuildReport. Design scope
// only (the notifications feed is first-party-gated for this app's token).
//
// Query params:
//   - hubId  (required) the GraphQL hub id (urn:adsk.ace:prod.scope:…)
//   - id     (required) the item/lineage id (urn:adsk.wipprod:dm.lineage:…)
//   - scope  optional; if given must be "design"
//   - bucket hour | day | month | year (default: day)
//   - from   lower time bound (RFC3339 or epoch ms), optional
//   - to     upper time bound (RFC3339 or epoch ms), optional
func (s *Server) handleActivityReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Design scope only. Activity is sourced from the Manufacturing Data Model
	// GraphQL — the Fusion Team notifications feed is first-party-gated (returns
	// HTTP 500 for this app's token), so hub/project/folder reports are not
	// offered. hubId is the GraphQL hub id and id the item/lineage id, the same
	// pair the Details endpoint uses.
	if scope := q.Get("scope"); scope != "" && scope != string(api.ScopeDesign) {
		writeError(w, http.StatusBadRequest, "only scope=design is supported")
		return
	}

	hubID, ok := reqParam(w, r, "hubId")
	if !ok {
		return
	}
	itemID := q.Get("id")
	if itemID == "" {
		writeError(w, http.StatusBadRequest, "missing required query parameter: id (the item/lineage id)")
		return
	}
	from, ok := parseQueryTime(w, q.Get("from"), "from")
	if !ok {
		return
	}
	to, ok := parseQueryTime(w, q.Get("to"), "to")
	if !ok {
		return
	}

	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}

	events, err := api.GetDesignActivity(ctx, token, hubID, itemID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rep := api.BuildReport(events, api.ScopeDesign, itemID, api.Bucket(q.Get("bucket")), from, to)
	writeJSON(w, http.StatusOK, activityReportDTO(rep))
}

// maxRollupChildren bounds the child fan-out of a single rollup request. Real
// assemblies are far smaller; the cap exists only to keep one authenticated
// request from amplifying into an unbounded number of APS GraphQL calls.
const maxRollupChildren = 2000

// handleActivityRollup merges a design's activity with all of its child
// documents' activity, server-side. Body: { hubId, itemId, childItemIds[] } —
// the caller enumerates descendants (GET /api/items/descendants) and passes the
// distinct child lineage ids here. The fan-out runs with bounded concurrency and
// a generous timeout so even a large assembly rolls up completely in one call.
func (s *Server) handleActivityRollup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HubID        string   `json:"hubId"`
		ItemID       string   `json:"itemId"`
		ChildItemIDs []string `json:"childItemIds"`
	}
	// Cap the body and the child fan-out: each child id becomes a GraphQL call to
	// APS under the user's token (5-min timeout below), so an unbounded list is an
	// authenticated amplifier. The ceilings are well above any real assembly.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.HubID == "" || body.ItemID == "" {
		writeError(w, http.StatusBadRequest, "hubId and itemId are required")
		return
	}
	if len(body.ChildItemIDs) > maxRollupChildren {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("too many child items: %d (max %d)", len(body.ChildItemIDs), maxRollupChildren))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	// The rollup's hub rides in the BODY, so the session-hub equality is
	// enforced here (before the APS fan-out) rather than by requireHub's
	// central query-param check.
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	if !hubMatches(w, set.hubID, body.HubID) {
		return
	}

	events, err := api.RollUpDesignActivity(ctx, token, body.HubID, body.ItemID, body.ChildItemIDs)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Aggregate across every merged event (scope=hub + empty id matches all),
	// then label the report as the parent design's.
	rep := api.BuildReport(events, api.ScopeHub, "", api.BucketDay, time.Time{}, time.Time{})
	rep.Scope = api.ScopeDesign
	rep.ScopeID = body.ItemID
	writeJSON(w, http.StatusOK, activityReportDTO(rep))
}

// parseQueryTime parses an optional time query param accepting either RFC3339
// or epoch-milliseconds. Empty yields the zero time (unbounded). On a malformed
// value it writes a 400 and reports ok=false.
func parseQueryTime(w http.ResponseWriter, v, name string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms).UTC(), true
	}
	writeError(w, http.StatusBadRequest, "invalid time for "+name+": use RFC3339 or epoch milliseconds")
	return time.Time{}, false
}
