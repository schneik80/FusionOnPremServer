package server

import (
	"encoding/json"
	"net/http"

	"github.com/schneik80/fusionlocalserver/pins"
)

// Pins are stored per-hub inside the hub's profile directory
// (hubs/<slug>/pins-<slug>.json). The hub is ALWAYS the session's selected
// hub (requireHub): a hubId query param, when the SPA still sends one, is
// centrally checked for equality and never trusted as scope. The mutate
// endpoints follow a Load -> mutate -> Save cycle that is not atomic on
// disk, so s.pinsMu serialises them to prevent a lost update when two
// clients pin concurrently. pins.Pin already carries JSON tags, so it
// doubles as the wire type for both responses and the POST body.

// handlePinsList -> pins.Load for the session hub.
func (s *Server) handlePinsList(w http.ResponseWriter, r *http.Request) {
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	s.pinsMu.Lock()
	ps, err := pins.Load(set.hubID)
	s.pinsMu.Unlock()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if ps == nil {
		ps = []pins.Pin{}
	}
	writeJSON(w, http.StatusOK, ps)
}

// handlePinsAdd validates and adds a pin (body: pin record). The body mirrors
// the TUI's pin capture — id, name, kind, project_id, project_alt_id,
// folder_path — so the bookmark stays navigable without an API call. A local
// record (whiteboard, task, job, batch, channel) instead carries its fls: ref
// as both id and address; pins.Validate enforces the split. The hub scope
// always comes from the session, never from the body.
func (s *Server) handlePinsAdd(w http.ResponseWriter, r *http.Request) {
	set, ok := reqStores(w, r)
	if !ok {
		return
	}

	var pin pins.Pin
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&pin); err != nil {
		writeError(w, http.StatusBadRequest, "invalid pin body")
		return
	}
	if err := pins.Validate(pin); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pin.HubID = set.hubID

	s.pinsMu.Lock()
	defer s.pinsMu.Unlock()

	ps, err := pins.Load(set.hubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ps = pins.Add(ps, pin)
	if err := pins.Save(set.hubID, ps); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

// handlePinsRemove -> Load + Remove + Save for the session hub (query: id).
func (s *Server) handlePinsRemove(w http.ResponseWriter, r *http.Request) {
	set, ok := reqStores(w, r)
	if !ok {
		return
	}
	id, ok := reqParam(w, r, "id")
	if !ok {
		return
	}

	s.pinsMu.Lock()
	defer s.pinsMu.Unlock()

	ps, err := pins.Load(set.hubID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ps = pins.Remove(ps, id)
	if err := pins.Save(set.hubID, ps); err != nil {
		s.fail(w, r, err)
		return
	}
	if ps == nil {
		ps = []pins.Pin{}
	}
	writeJSON(w, http.StatusOK, ps)
}
