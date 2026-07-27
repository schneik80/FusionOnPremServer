package server

import (
	"encoding/json"

	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// WhiteboardUserDTO is a board participant (creator / last editor) on the wire.
type WhiteboardUserDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// WhiteboardDTO is one board's metadata. The tldraw document is deliberately
// absent — it is fetched separately when a board is opened, so listing a
// project's boards never ships megabytes of shapes.
type WhiteboardDTO struct {
	ID            string            `json:"id"`
	Num           int64             `json:"num"`
	ProjectID     string            `json:"projectId"`
	HubID         string            `json:"hubId"`
	ProjectName   string            `json:"projectName"`
	Name          string            `json:"name"`
	CreatedBy     WhiteboardUserDTO `json:"createdBy"`
	CreatedAt     string            `json:"createdAt"`
	UpdatedAt     string            `json:"updatedAt"`
	UpdatedBy     WhiteboardUserDTO `json:"updatedBy"`
	SnapshotBytes int64             `json:"snapshotBytes"`
	// DocRev is the document's save counter. The canvas sends the revision it
	// loaded back with each save so a stale save is refused rather than
	// silently overwriting whoever saved in between.
	DocRev int64 `json:"docRev"`
}

// WhiteboardCapsDTO tells the SPA what the caller may do, so it can present a
// read-only canvas rather than let edits bounce off a 403.
type WhiteboardCapsDTO struct {
	Write    bool `json:"write"`
	Moderate bool `json:"moderate"`
}

// WhiteboardListDTO is GET /api/whiteboards.
type WhiteboardListDTO struct {
	Whiteboards  []WhiteboardDTO   `json:"whiteboards"`
	Capabilities WhiteboardCapsDTO `json:"capabilities"`
}

func whiteboardUserDTO(r whiteboards.UserRef) WhiteboardUserDTO {
	return WhiteboardUserDTO{ID: r.ID, Name: r.Name, Email: r.Email}
}

func whiteboardDTO(b whiteboards.Board, projectID, hubID, projectName string) WhiteboardDTO {
	return WhiteboardDTO{
		ID:            b.ID,
		Num:           b.Num,
		ProjectID:     projectID,
		HubID:         hubID,
		ProjectName:   projectName,
		Name:          b.Name,
		CreatedBy:     whiteboardUserDTO(b.CreatedBy),
		CreatedAt:     fmtTime(b.CreatedAt),
		UpdatedAt:     fmtTime(b.UpdatedAt),
		UpdatedBy:     whiteboardUserDTO(b.UpdatedBy),
		SnapshotBytes: b.SnapshotBytes,
		DocRev:        b.DocRev,
	}
}

// WhiteboardPatchDTO answers a patch submission: where the board is now, and
// which of the client's records were refused (a shape it tried to update that
// someone else had already deleted). The client drops those locally.
type WhiteboardPatchDTO struct {
	Rev      int64    `json:"rev"`
	Rejected []string `json:"rejected"`
}

// WhiteboardPatchEventDTO is a patch as it reaches the other clients. Put
// carries whole records, opaque to the server; Remove may be longer than what
// the sender asked for, because deleting a shape takes its bindings with it.
type WhiteboardPatchEventDTO struct {
	Rev      int64                      `json:"rev"`
	ClientID string                     `json:"clientId"`
	Put      map[string]json.RawMessage `json:"put,omitempty"`
	Remove   []string                   `json:"remove"`
}
