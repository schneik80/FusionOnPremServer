package server

import (
	"net/http"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
)

// hubOverviewWindowDays bounds the collaboration-pulse and chat-traffic
// lookback. 90 days covers a quarter of activity while keeping the chat log
// scan and the response payload small.
const hubOverviewWindowDays = 90

// handleHubOverview builds the hub dashboard's aggregate snapshot. It makes a
// SINGLE upstream call — api.GetProjects, which APS scopes to the projects the
// caller can access — then aggregates THIS hub's local stores (tasks,
// production, chat) restricted to that accessible set. There is no per-project
// fan-out: everything after the one GetProjects call is a local file scan.
//
// The access scoping is a security requirement, not an optimization. The
// cross-project /mine endpoints skip authorization because they only ever
// reveal the caller's OWN items; this endpoint returns aggregates over
// EVERYONE's items (counts, contributor names, per-project activity), so it
// must not surface a project the caller isn't a member of. GetProjects is the
// authoritative accessible set (the same "your token sees it or it doesn't"
// trust the Dashboard/Wiki tabs already run on), and the local scans are
// scoped to it. The session's store-set (never a client-supplied hub id) keeps
// the whole thing inside the locked hub.
func (s *Server) handleHubOverview(w http.ResponseWriter, r *http.Request) {
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}

	projects, err := api.GetProjects(ctx, token, set.hubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Accessible-project scope: a local store keys its directory by whichever
	// project id the create path used, so match on BOTH the GraphQL node id and
	// the data-management alt id, and map both forms to the display name.
	names := make(map[string]string, len(projects)*2)
	ids := make([]string, 0, len(projects)*2)
	for _, p := range projects {
		if p.ID != "" {
			names[p.ID] = p.Name
			ids = append(ids, p.ID)
		}
		if p.AltID != "" {
			names[p.AltID] = p.Name
			ids = append(ids, p.AltID)
		}
	}

	tasksList, err := set.tasks.ListForProjects(ids)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	jobs, err := set.production.ListForProjects(ids)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	now := time.Now()
	chatDays, err := set.chat.MessageActivity(ids, now.AddDate(0, 0, -hubOverviewWindowDays))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, hubOverviewDTO(
		set.hubID, set.hubName, now, hubOverviewWindowDays, len(projects),
		names, tasksList, jobs, chatDays,
	))
}
