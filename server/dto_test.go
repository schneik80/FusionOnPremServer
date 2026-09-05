package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
)

// The History view groups its per-author tracks by createdById, so that field
// has to survive the domain → DTO → JSON trip. It is omitempty, which is the
// point of the second version here: an author without a resolvable id must
// leave the key off entirely rather than ship an empty string the client would
// then have to special-case.
func TestDetailsDTO_MapsVersionAuthorID(t *testing.T) {
	d := &api.ItemDetails{
		ID:   "urn:item:abc",
		Name: "Widget A",
		Versions: []api.VersionSummary{
			{
				Number:      2,
				CreatedOn:   time.Date(2024, 2, 20, 14, 0, 0, 0, time.UTC),
				CreatedBy:   "Grace Hopper",
				CreatedByID: "user-grace",
			},
			{
				Number:    1,
				CreatedOn: time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
				CreatedBy: "Ada Lovelace",
			},
		},
	}

	dto := detailsDTO(d)
	if len(dto.Versions) != 2 {
		t.Fatalf("len(Versions) = %d, want 2", len(dto.Versions))
	}
	if dto.Versions[0].CreatedByID != "user-grace" {
		t.Errorf("Versions[0].CreatedByID = %q, want %q", dto.Versions[0].CreatedByID, "user-grace")
	}
	if dto.Versions[1].CreatedByID != "" {
		t.Errorf("Versions[1].CreatedByID = %q, want empty", dto.Versions[1].CreatedByID)
	}

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshaling DTO: %v", err)
	}
	var round struct {
		Versions []map[string]any `json:"versions"`
	}
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("unmarshaling DTO: %v", err)
	}
	if got := round.Versions[0]["createdById"]; got != "user-grace" {
		t.Errorf("versions[0].createdById = %v, want %q", got, "user-grace")
	}
	if _, ok := round.Versions[1]["createdById"]; ok {
		t.Errorf("versions[1] carries createdById, want it omitted")
	}
}

// Slices are never nil on the wire — an empty history must marshal as [] so the
// client can map over it without a null guard.
func TestDetailsDTO_VersionsNeverNil(t *testing.T) {
	dto := detailsDTO(&api.ItemDetails{ID: "urn:item:empty"})
	if dto.Versions == nil {
		t.Fatal("Versions = nil, want an empty slice")
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshaling DTO: %v", err)
	}
	var round struct {
		Versions json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("unmarshaling DTO: %v", err)
	}
	if string(round.Versions) != "[]" {
		t.Errorf("versions = %s, want []", round.Versions)
	}
}

func TestItemHistoryDTO_ChangesNeverNil(t *testing.T) {
	b, err := json.Marshal(itemHistoryDTO(&api.ItemHistory{}))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"changes":[],"saves":[]}` {
		t.Errorf("empty history marshals as %s, want {\"changes\":[],\"saves\":[]}", b)
	}
}

func TestItemHistoryDTO_Maps(t *testing.T) {
	at := time.Date(2024, 2, 20, 13, 0, 0, 0, time.UTC)
	got := itemHistoryDTO(&api.ItemHistory{
		Changes: []api.HistoryChange{{
			Type: "PropertiesUpdatedHistoryChange", CreatedOn: at,
			CreatedBy: "Cyan Perry", CreatedByID: "user-cyan", Comment: "Category: Beverage",
		}},
		Saves: []api.HistorySave{{CreatedOn: at, Milestone: "Rev B", Revision: "Rev B"}},
	})
	if len(got.Saves) != 1 || got.Saves[0].Revision != "Rev B" || got.Saves[0].Milestone != "Rev B" || got.Saves[0].CreatedOn == "" {
		t.Errorf("Saves = %+v", got.Saves)
	}
	if len(got.Changes) != 1 {
		t.Fatalf("len = %d", len(got.Changes))
	}
	c := got.Changes[0]
	if c.Type != "PropertiesUpdatedHistoryChange" || c.CreatedBy != "Cyan Perry" ||
		c.CreatedByID != "user-cyan" || c.Comment != "Category: Beverage" || c.CreatedOn == "" {
		t.Errorf("mapped DTO = %+v", c)
	}
}
