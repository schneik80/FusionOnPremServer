package server

import (
	"net/http"
	"slices"
	"strings"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/chat"
)

// maxLocalRefs bounds one lookup's result list. A document referenced from
// more places than this is real (a shared fastener in a busy hub), and the
// graph can't usefully draw them anyway — so the list is cut and the response
// says it was cut. Never truncate silently.
const maxLocalRefs = 200

// localRefSources is every source the lookup can scan, and the default when
// the caller names none.
var localRefSources = []string{localRefTask, localRefChat, localRefWhiteboard, localRefJob, localRefBatch}

// handleLocalRefs is the local-store half of Where Used: which of OUR OWN
// records — tasks, chat channels, whiteboards, job plans, batch runs —
// reference a hub document (query: itemId, optional sources=a,b,c).
//
// Cost shape, and why this is one endpoint rather than five: it makes a SINGLE
// upstream call — api.GetProjects, which APS scopes to the projects the caller
// can access — and everything after it is a local file scan with a raw-bytes
// prefilter (internal/docref). No per-item fan-out, no APS call per hit. The
// client's checkboxes select sources, so an unchecked source costs nothing.
//
// Wiki pages are deliberately NOT a source. They are markdown files stored in
// Fusion Team, not a local store, so scanning them would mean a folder listing
// plus a download per page per project — exactly the per-row fan-out the APS
// quota punishes. Indexing them at publish time is the way in, if wanted.
//
// The accessible-project scope is a security requirement, the same one
// handleHubOverview documents: these scans read EVERYONE's records, so they
// must be restricted to the projects the caller's own token can see. Private
// chat channels are then filtered again by their ACL.
func (s *Server) handleLocalRefs(w http.ResponseWriter, r *http.Request) {
	itemID, ok := reqParam(w, r, "itemId")
	if !ok {
		return
	}
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
	sess, ok := sessionFromCtx(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	want := parseLocalRefSources(r.URL.Query().Get("sources"))
	if len(want) == 0 {
		writeJSON(w, http.StatusOK, LocalRefsDTO{Refs: []LocalRefDTO{}, Cap: maxLocalRefs})
		return
	}

	projects, err := api.GetProjects(ctx, token, set.hubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A local store keys its directory by whichever project id the create path
	// used, so match on BOTH the GraphQL node id and the data-management alt
	// id, and map both forms to the display name (handleHubOverview does the
	// same for the same reason).
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

	refs := []LocalRefDTO{}
	if slices.Contains(want, localRefTask) {
		hits, err := set.tasks.FindDocRefs(ids, itemID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		refs = append(refs, taskLocalRefDTOs(hits, set.hubID)...)
	}
	if slices.Contains(want, localRefChat) {
		hits, err := set.chat.FindDocRefs(ids, itemID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		refs = append(refs, chatLocalRefDTOs(visibleChatHits(hits, sess.Profile.Sub))...)
	}
	if slices.Contains(want, localRefWhiteboard) {
		hits, err := set.whiteboards.FindDocRefs(ids, itemID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		refs = append(refs, boardLocalRefDTOs(hits)...)
	}
	if slices.Contains(want, localRefJob) || slices.Contains(want, localRefBatch) {
		hits, err := set.production.FindDocRefs(ids, itemID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, d := range productionLocalRefDTOs(hits, set.hubID) {
			if slices.Contains(want, d.Kind) {
				refs = append(refs, d)
			}
		}
	}

	// The stores each record the project name they saw at write time; APS is
	// authoritative, so a rename converges here rather than in every file.
	for i := range refs {
		if n := names[refs[i].ProjectID]; n != "" {
			refs[i].ProjectName = n
		}
	}

	kept, truncated := capLocalRefs(refs)
	writeJSON(w, http.StatusOK, LocalRefsDTO{Refs: kept, Truncated: truncated, Cap: maxLocalRefs})
}

// capLocalRefs cuts the result list to maxLocalRefs, taking from each kind in
// turn rather than in scan order. A part discussed in 400 tasks would
// otherwise fill the cap before the first job or batch was reached, and the
// user would read "200 references, truncated" as "no production ties" — a
// worse failure than showing fewer of each. Within a kind the store's order
// (newest / lowest-numbered first) is preserved, and the kinds come back
// grouped so the graph still draws them in blocks.
func capLocalRefs(refs []LocalRefDTO) ([]LocalRefDTO, bool) {
	if len(refs) <= maxLocalRefs {
		return refs, false
	}
	byKind := map[string][]LocalRefDTO{}
	for _, r := range refs {
		byKind[r.Kind] = append(byKind[r.Kind], r)
	}
	taken := map[string][]LocalRefDTO{}
	for n := 0; n < maxLocalRefs; {
		progressed := false
		for _, kind := range localRefSources {
			pool := byKind[kind]
			if len(taken[kind]) >= len(pool) {
				continue
			}
			taken[kind] = append(taken[kind], pool[len(taken[kind])])
			progressed = true
			if n++; n == maxLocalRefs {
				break
			}
		}
		if !progressed {
			break
		}
	}
	out := make([]LocalRefDTO, 0, maxLocalRefs)
	for _, kind := range localRefSources {
		out = append(out, taken[kind]...)
	}
	return out, true
}

// visibleChatHits applies the private-channel ACL to a raw scan.
//
// Public channels ride on the accessible-project scope the caller already
// passed. A private channel is included only when the caller is on its member
// list — a purely local check, so a hub-wide lookup still costs zero APS
// calls. That is deliberately stricter than CanAccessChannel, which also lets
// a project Manager/Administrator into private channels they aren't a member
// of: honouring that here would mean a roster fetch per project with a private
// hit, which is exactly the per-project fan-out this endpoint refuses to do.
// A moderator therefore sees a private channel's references only from inside
// chat, never from the relationship graph — under-reporting, never leaking.
func visibleChatHits(hits []chat.DocRefHit, userID string) []chat.DocRefHit {
	out := hits[:0:0]
	for _, h := range hits {
		if h.IsPrivate && !chatMember(h.Members, userID) {
			continue
		}
		out = append(out, h)
	}
	return out
}

func chatMember(members []chat.ChannelMember, userID string) bool {
	if userID == "" {
		return false
	}
	for _, m := range members {
		if m.UserID == userID {
			return true
		}
	}
	return false
}

// parseLocalRefSources reads the comma-separated sources filter. An empty or
// absent value means every source; unknown names are ignored rather than
// rejected, so a newer client asking for a source this build lacks degrades to
// the sources it does have.
func parseLocalRefSources(v string) []string {
	if strings.TrimSpace(v) == "" {
		return localRefSources
	}
	out := make([]string, 0, len(localRefSources))
	for _, part := range strings.Split(v, ",") {
		name := strings.TrimSpace(part)
		if slices.Contains(localRefSources, name) && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}
