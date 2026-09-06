package server

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/schneik80/fusionlocalserver/api"
)

// MemberDTO is an individual user with a role + status on a project or folder.
type MemberDTO struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email,omitempty"`
	Role   string `json:"role"`
	Status string `json:"status,omitempty"`
}

// PermLayerDTO is one layer of the access path (the project, or a folder) with
// the groups and individual members granted there.
type PermLayerDTO struct {
	Type    string            `json:"type"` // "project" | "folder"
	ID      string            `json:"id"`
	Name    string            `json:"name,omitempty"`
	Groups  []ProjectGroupDTO `json:"groups"`
	Members []MemberDTO       `json:"members"`
	// Error is true when this layer could not be fetched (an upstream
	// failure other than a rate limit, which fails the whole request). An
	// empty layer with Error=false genuinely has no grants; the explorer
	// must never present a failed fetch as "nobody has access".
	Error bool `json:"error,omitempty"`
}

// maxPermissionsPathDepth caps the repeated folderId parameter: a real
// ancestry is a handful deep, and each layer costs two paginated APS calls,
// so an unbounded list was an authenticated quota amplifier.
const maxPermissionsPathDepth = 16

func groupDTOs(gs []api.ProjectGroup) []ProjectGroupDTO {
	out := make([]ProjectGroupDTO, 0, len(gs))
	for _, g := range gs {
		out = append(out, ProjectGroupDTO{ID: g.ID, Name: g.Name, Role: g.Role})
	}
	return out
}

func memberDTOs(ms []api.Member) []MemberDTO {
	out := make([]MemberDTO, 0, len(ms))
	for _, m := range ms {
		out = append(out, MemberDTO{UserID: m.UserID, Name: m.Name, Email: m.Email, Role: m.Role, Status: m.Status})
	}
	return out
}

// handlePermissionsPath returns the access at each layer of a document's path:
// the project (groups + folder-level project members), then each folder
// (members + groups), in root→leaf order. The frontend resolves the per-principal
// inheritance/override cascade from these layers. Query params: hubId, projectId,
// and repeated folderId (+ optional projectName / folderName) in root→leaf order.
// Per-layer fetch errors yield an empty part rather than failing the whole call.
func (s *Server) handlePermissionsPath(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hubID := q.Get("hubId")
	projectID := q.Get("projectId")
	if hubID == "" || projectID == "" {
		writeError(w, http.StatusBadRequest, "hubId and projectId are required")
		return
	}
	folderIDs := q["folderId"]
	folderNames := q["folderName"]
	// layers=leaf answers with ONLY the deepest layer (the current folder,
	// or the project at the root): what the project dashboard's People &
	// groups widget shows. It is two paginated calls instead of 2+2N — the
	// explorer, which draws the whole path, asks for all.
	leafOnly := q.Get("layers") == "leaf"
	if leafOnly && len(folderIDs) > 0 {
		last := len(folderIDs) - 1
		folderIDs = folderIDs[last:]
		if len(folderNames) > last {
			folderNames = folderNames[last:]
		} else {
			folderNames = nil
		}
	}
	if len(folderIDs) > maxPermissionsPathDepth {
		writeErrorCode(w, http.StatusBadRequest, "path_too_deep",
			fmt.Sprintf("at most %d folderId values (got %d)", maxPermissionsPathDepth, len(folderIDs)))
		return
	}

	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}

	// In leaf mode with a folder, the project layer is not fetched at all.
	withProject := !leafOnly || len(folderIDs) == 0
	n := len(folderIDs)
	if withProject {
		n++
	}
	layers := make([]PermLayerDTO, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	offset := 0
	if withProject {
		offset = 1
	}

	// fetchLayer runs the layer's two fetches through the shared fan-out
	// bound and records the first error. An error empties the layer AND
	// flags it, so the explorer shows "could not be loaded" rather than an
	// empty (wrong) grant list.
	fetchLayer := func(i int, groups func() ([]api.ProjectGroup, error), members func() ([]api.Member, error), build func([]api.ProjectGroup, []api.Member) PermLayerDTO) {
		defer wg.Done()
		var g []api.ProjectGroup
		var m []api.Member
		var gErr, mErr error
		var w2 sync.WaitGroup
		w2.Add(2)
		go func() { defer w2.Done(); g, gErr = api.WithFanout(ctx, groups) }()
		go func() { defer w2.Done(); m, mErr = api.WithFanout(ctx, members) }()
		w2.Wait()
		layers[i] = build(g, m)
		if gErr != nil || mErr != nil {
			errs[i] = errors.Join(gErr, mErr)
			layers[i].Error = true
			layers[i].Groups, layers[i].Members = []ProjectGroupDTO{}, []MemberDTO{}
		}
	}

	// Project layer: groups + folder-level project members.
	if withProject {
		wg.Add(1)
		go fetchLayer(0,
			func() ([]api.ProjectGroup, error) { return api.GetProjectGroups(ctx, token, projectID) },
			func() ([]api.Member, error) { return api.GetProjectMembers(ctx, token, projectID) },
			func(g []api.ProjectGroup, m []api.Member) PermLayerDTO {
				return PermLayerDTO{Type: "project", ID: projectID, Name: q.Get("projectName"), Groups: groupDTOs(g), Members: memberDTOs(m)}
			})
	}

	// Folder layers: members + groups.
	for i, fid := range folderIDs {
		name := ""
		if i < len(folderNames) {
			name = folderNames[i]
		}
		wg.Add(1)
		go fetchLayer(i+offset,
			func() ([]api.ProjectGroup, error) { return api.GetFolderGroups(ctx, token, hubID, fid) },
			func() ([]api.Member, error) { return api.GetFolderMembers(ctx, token, hubID, fid) },
			func(g []api.ProjectGroup, m []api.Member) PermLayerDTO {
				return PermLayerDTO{Type: "folder", ID: fid, Name: name, Groups: groupDTOs(g), Members: memberDTOs(m)}
			})
	}
	wg.Wait()

	// A rate limit anywhere fails the whole request: the SPA then shows the
	// slow-mode state with a real countdown instead of a half-empty path.
	for i, err := range errs {
		if err != nil {
			if api.IsRateLimited(err) {
				s.fail(w, r, err)
				return
			}
			s.logger.Debug("permissions layer failed", "layer", i, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, layers)
}

// handleProjectGroups -> api.GetProjectGroups (query: projectId). The groups
// (and roles) with access to the item's project — the Permissions tab.
func (s *Server) handleProjectGroups(w http.ResponseWriter, r *http.Request) {
	projectID, ok := reqParam(w, r, "projectId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	groups, err := api.GetProjectGroups(ctx, token, projectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]ProjectGroupDTO, len(groups))
	for i, g := range groups {
		out[i] = ProjectGroupDTO{ID: g.ID, Name: g.Name, Role: g.Role}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGroupMembers -> api.GetGroupMembers (query: hubId, groupId). Listing
// members needs hub-admin access; a 403 here is expected for ordinary users
// and the SPA shows it as "no permission" rather than an error.
func (s *Server) handleGroupMembers(w http.ResponseWriter, r *http.Request) {
	hubID, ok := reqParam(w, r, "hubId")
	if !ok {
		return
	}
	groupID, ok := reqParam(w, r, "groupId")
	if !ok {
		return
	}
	ctx, cancel := s.reqCtx(r)
	defer cancel()
	token, ok := s.token(ctx, w, r)
	if !ok {
		return
	}
	members, err := api.GetGroupMembers(ctx, token, hubID, groupID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]GroupMemberDTO, len(members))
	for i, m := range members {
		out[i] = GroupMemberDTO{UserID: m.UserID, Name: m.Name, Email: m.Email, Status: m.Status}
	}
	writeJSON(w, http.StatusOK, out)
}
