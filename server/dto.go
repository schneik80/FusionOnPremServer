package server

import (
	"time"

	"github.com/schneik80/fusionlocalserver/api"
)

// The DTOs below mirror the api.* result structs but carry explicit camelCase
// json tags (the api structs have none). Times are rendered as RFC3339 strings
// — empty when zero — so the frontend never has to special-case Go's zero time.
// Slice fields are always emitted as [] (never null) so the React client can
// map over them unconditionally.

// MetaDTO is the server self-description returned by GET /api/meta.
type MetaDTO struct {
	Version string `json:"version"`
	Region  string `json:"region"`
	// Port is the currently bound listen port. PortConfigurable reports whether
	// it can be changed at runtime via POST /api/settings/port (false in dev
	// mode).
	Port             int  `json:"port"`
	PortConfigurable bool `json:"portConfigurable"`
	// Debug is true when the server runs with -v (request tracing on). The web
	// UI uses it to reveal developer-only affordances (e.g. the version probe).
	Debug bool `json:"debug"`
	// Logo describes the configured sign-in logo, omitted when none is set. It
	// rides on this endpoint because /api/meta is the one description the SPA
	// can fetch before it has a session — which is exactly when the sign-in
	// screen needs to know whether to draw a logo or the built-in mark.
	Logo *LogoDTO `json:"logo,omitempty"`
}

// LogoDTO describes the stored sign-in logo without its bytes. Version is the
// short content hash: it is the cache key the client puts in the image URL, so
// a new logo is a new URL and no stale copy can survive.
type LogoDTO struct {
	Version     string `json:"version"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	// Width/Height are the intrinsic pixel size, omitted when unknown. The
	// client uses them to reserve the right box while the image loads; it must
	// still lay out correctly without them.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// logoDTO renders stored logo metadata for the wire. nil in, nil out — "no
// logo configured" travels as an absent field.
func logoDTO(m *LogoMeta) *LogoDTO {
	if m == nil {
		return nil
	}
	return &LogoDTO{
		Version:     m.Version(),
		ContentType: m.ContentType,
		Size:        m.Size,
		Width:       m.Width,
		Height:      m.Height,
	}
}

// SetPortRequest is the POST /api/settings/port body.
type SetPortRequest struct {
	Port int `json:"port"`
}

// SetPortResponse acknowledges a port change. Restarting is always true on
// success — the listener rebinds, so the client must reconnect on the new port.
type SetPortResponse struct {
	Port       int  `json:"port"`
	Restarting bool `json:"restarting"`
}

// ItemDTO mirrors api.NavItem — a navigable node (hub/project/folder/design/…).
type ItemDTO struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	AltID              string `json:"altId,omitempty"`
	WebURL             string `json:"webUrl,omitempty"`
	IsContainer        bool   `json:"isContainer"`
	ComponentVersionID string `json:"componentVersionId,omitempty"`
	Subtype            string `json:"subtype,omitempty"`
	// ModifiedOn is the item's last-modified time (RFC3339); set for items, empty
	// for folders. Drives the Project Dashboard's recently-modified list.
	ModifiedOn string `json:"modifiedOn,omitempty"`
	// Slug is the short hub identifier (e.g. "imallc") used by the activity feed
	// endpoint. Populated for hubs only; derived from AltID / WebURL.
	Slug string `json:"slug,omitempty"`
}

// ResolveProjectDTO answers GET /api/resolve/project: the GraphQL ids for a
// hub/project pair named by Data Management ids. SessionHubID is the session's
// current hub lock ("" when none) so the embed client can decide auto-lock vs
// hub-switch consent in one round trip.
type ResolveProjectDTO struct {
	HubID        string `json:"hubId"`
	HubName      string `json:"hubName"`
	HubAltID     string `json:"hubAltId"`
	ProjectID    string `json:"projectId"`
	ProjectName  string `json:"projectName"`
	ProjectAltID string `json:"projectAltId"`
	SessionHubID string `json:"sessionHubId"`
}

// ContentsDTO is the combined folders+items payload for GET /api/projects/contents.
type ContentsDTO struct {
	Folders []ItemDTO `json:"folders"`
	Items   []ItemDTO `json:"items"`
}

// VersionDTO mirrors api.VersionSummary — one row of an item's version history.
type VersionDTO struct {
	Number                 int    `json:"number"`
	CreatedOn              string `json:"createdOn,omitempty"`
	CreatedBy              string `json:"createdBy,omitempty"`
	CreatedByID            string `json:"createdById,omitempty"`
	Comment                string `json:"comment,omitempty"`
	RootComponentVersionID string `json:"rootComponentVersionId,omitempty"`
	IsMilestone            bool   `json:"isMilestone"`
	Revision               string `json:"revision,omitempty"`
	PublicShare            bool   `json:"publicShare"`
}

// HistoryChangeDTO mirrors api.HistoryChange — one edit that made no version.
// Field names match VersionDTO so the web view lays both on one timeline;
// `type` is the raw GraphQL typename, labelled client-side.
type HistoryChangeDTO struct {
	Type        string `json:"type"`
	CreatedOn   string `json:"createdOn,omitempty"`
	CreatedBy   string `json:"createdBy,omitempty"`
	CreatedByID string `json:"createdById,omitempty"`
	Comment     string `json:"comment,omitempty"`
}

// HistorySaveDTO mirrors api.HistorySave — one save as the history records
// it, newest first, with the markers the history attached to it. The web view
// joins these to the version list by position.
type HistorySaveDTO struct {
	CreatedOn string `json:"createdOn,omitempty"`
	Milestone string `json:"milestone,omitempty"`
	Revision  string `json:"revision,omitempty"`
}

// ItemHistoryDTO is the GET /api/items/history response.
type ItemHistoryDTO struct {
	Changes []HistoryChangeDTO `json:"changes"`
	Saves   []HistorySaveDTO   `json:"saves"`
}

func itemHistoryDTO(h *api.ItemHistory) ItemHistoryDTO {
	out := ItemHistoryDTO{Changes: []HistoryChangeDTO{}, Saves: []HistorySaveDTO{}}
	if h == nil {
		return out
	}
	for _, c := range h.Changes {
		out.Changes = append(out.Changes, HistoryChangeDTO{
			Type:        c.Type,
			CreatedOn:   fmtTime(c.CreatedOn),
			CreatedBy:   c.CreatedBy,
			CreatedByID: c.CreatedByID,
			Comment:     c.Comment,
		})
	}
	for _, s := range h.Saves {
		out.Saves = append(out.Saves, HistorySaveDTO{CreatedOn: fmtTime(s.CreatedOn), Milestone: s.Milestone, Revision: s.Revision})
	}
	return out
}

// DetailsDTO mirrors api.ItemDetails — the rich metadata for one item.
type DetailsDTO struct {
	ID                     string       `json:"id"`
	Name                   string       `json:"name"`
	Typename               string       `json:"typename"`
	Size                   string       `json:"size,omitempty"`
	MimeType               string       `json:"mimeType,omitempty"`
	ExtensionType          string       `json:"extensionType,omitempty"`
	FusionWebURL           string       `json:"fusionWebUrl,omitempty"`
	CreatedOn              string       `json:"createdOn,omitempty"`
	CreatedBy              string       `json:"createdBy,omitempty"`
	ModifiedOn             string       `json:"modifiedOn,omitempty"`
	ModifiedBy             string       `json:"modifiedBy,omitempty"`
	VersionNumber          int          `json:"versionNumber"`
	PartNumber             string       `json:"partNumber,omitempty"`
	PartDesc               string       `json:"partDesc,omitempty"`
	Material               string       `json:"material,omitempty"`
	IsMilestone            bool         `json:"isMilestone"`
	Revision               string       `json:"revision,omitempty"`
	RootComponentVersionID string       `json:"rootComponentVersionId,omitempty"`
	Versions               []VersionDTO `json:"versions"`
}

// ComponentRefDTO mirrors api.ComponentRef — a row in the Uses / Where Used tab.
type ComponentRefDTO struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	PartNumber     string `json:"partNumber,omitempty"`
	PartDesc       string `json:"partDesc,omitempty"`
	Material       string `json:"material,omitempty"`
	DesignItemID   string `json:"designItemId,omitempty"`
	DesignItemName string `json:"designItemName,omitempty"`
	FusionWebURL   string `json:"fusionWebUrl,omitempty"`
}

// BOMRowDTO mirrors api.BOMRow — one line of a design's bill of materials.
// quantity is the occurrence count (v2 has no explicit quantity field).
type BOMRowDTO struct {
	ComponentVersionID string `json:"componentVersionId"`
	Name               string `json:"name"`
	PartNumber         string `json:"partNumber,omitempty"`
	PartDesc           string `json:"partDesc,omitempty"`
	Material           string `json:"material,omitempty"`
	Quantity           int    `json:"quantity"`
}

// ProjectGroupDTO mirrors api.ProjectGroup — a group with access to a project
// and its role, shown in the Permissions tab.
type ProjectGroupDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// GroupMemberDTO mirrors api.GroupMember — a user in a group (only listable
// with hub-admin access).
type GroupMemberDTO struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email,omitempty"`
	Status string `json:"status,omitempty"`
}

// NamedPropertyDTO mirrors api.NamedProperty — one custom/standard property
// (name + display value) shown in the Details Properties tab.
type NamedPropertyDTO struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DrawingRefDTO mirrors api.DrawingRef — a row in the Drawings tab.
type DrawingRefDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DrawingItemID string `json:"drawingItemId"`
	ModifiedOn    string `json:"modifiedOn,omitempty"`
	ModifiedBy    string `json:"modifiedBy,omitempty"`
	FusionWebURL  string `json:"fusionWebUrl,omitempty"`
}

// FolderRefDTO mirrors api.FolderRef — one hop in a folder ancestry chain.
type FolderRefDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// LocationDTO mirrors api.ItemLocation — where an item lives, for Show-in-Location.
type LocationDTO struct {
	HubID        string         `json:"hubId"`
	ProjectID    string         `json:"projectId"`
	ProjectAltID string         `json:"projectAltId,omitempty"`
	ProjectName  string         `json:"projectName"`
	FolderPath   []FolderRefDTO `json:"folderPath"`
}

// ClassifyDTO is the GET /api/items/classify result for one design row.
type ClassifyDTO struct {
	ComponentVersionID string `json:"componentVersionId"`
	IsAssembly         bool   `json:"isAssembly"`
	Subtype            string `json:"subtype"` // "assembly" | "part"
}

// ThumbnailDTO is the GET /api/items/thumbnail result. Generation is async, so
// status is "PENDING" | "SUCCESS" | "FAILED"; signedUrl is populated only once
// status is SUCCESS. The frontend polls while status is PENDING.
type ThumbnailDTO struct {
	Status    string `json:"status"`
	SignedURL string `json:"signedUrl,omitempty"`
}

// MeasureDTO is one physical-property value (display string + unit).
type MeasureDTO struct {
	Display string `json:"display,omitempty"`
	Units   string `json:"units,omitempty"`
}

// PhysicalPropertiesDTO mirrors api.PhysicalProperties — the GET
// /api/items/properties result. Status is "COMPLETED" | "FAILED" | (computing).
type PhysicalPropertiesDTO struct {
	Status     string     `json:"status"`
	Area       MeasureDTO `json:"area"`
	Volume     MeasureDTO `json:"volume"`
	Mass       MeasureDTO `json:"mass"`
	Density    MeasureDTO `json:"density"`
	BBoxLength MeasureDTO `json:"bboxLength"`
	BBoxWidth  MeasureDTO `json:"bboxWidth"`
	BBoxHeight MeasureDTO `json:"bboxHeight"`
}

// ---------------------------------------------------------------------------
// Mappers
// ---------------------------------------------------------------------------

func measureDTO(m api.Measure) MeasureDTO {
	return MeasureDTO{Display: m.Display, Units: m.Units}
}

func physicalPropertiesDTO(p *api.PhysicalProperties) PhysicalPropertiesDTO {
	return PhysicalPropertiesDTO{
		Status:     p.Status,
		Area:       measureDTO(p.Area),
		Volume:     measureDTO(p.Volume),
		Mass:       measureDTO(p.Mass),
		Density:    measureDTO(p.Density),
		BBoxLength: measureDTO(p.BBoxLength),
		BBoxWidth:  measureDTO(p.BBoxWidth),
		BBoxHeight: measureDTO(p.BBoxHeight),
	}
}

// fmtTime renders a timestamp as RFC3339, or "" when zero.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func itemDTO(n api.NavItem) ItemDTO {
	d := ItemDTO{
		ID:                 n.ID,
		Name:               n.Name,
		Kind:               n.Kind,
		AltID:              n.AltID,
		WebURL:             n.WebURL,
		IsContainer:        n.IsContainer,
		ComponentVersionID: n.ComponentVersionID,
		Subtype:            n.Subtype,
		ModifiedOn:         fmtTime(n.ModifiedOn),
	}
	if n.Kind == "hub" {
		d.Slug = api.HubSlug(n.AltID, n.WebURL)
	}
	return d
}

func itemDTOs(ns []api.NavItem) []ItemDTO {
	out := make([]ItemDTO, 0, len(ns))
	for _, n := range ns {
		out = append(out, itemDTO(n))
	}
	return out
}

func detailsDTO(d *api.ItemDetails) DetailsDTO {
	dto := DetailsDTO{
		ID:                     d.ID,
		Name:                   d.Name,
		Typename:               d.Typename,
		Size:                   d.Size,
		MimeType:               d.MimeType,
		ExtensionType:          d.ExtensionType,
		FusionWebURL:           d.FusionWebURL,
		CreatedOn:              fmtTime(d.CreatedOn),
		CreatedBy:              d.CreatedBy,
		ModifiedOn:             fmtTime(d.ModifiedOn),
		ModifiedBy:             d.ModifiedBy,
		VersionNumber:          d.VersionNumber,
		PartNumber:             d.PartNumber,
		PartDesc:               d.PartDesc,
		Material:               d.Material,
		IsMilestone:            d.IsMilestone,
		Revision:               d.Revision,
		RootComponentVersionID: d.RootComponentVersionID,
		Versions:               make([]VersionDTO, 0, len(d.Versions)),
	}
	for _, v := range d.Versions {
		dto.Versions = append(dto.Versions, VersionDTO{
			Number:                 v.Number,
			CreatedOn:              fmtTime(v.CreatedOn),
			CreatedBy:              v.CreatedBy,
			CreatedByID:            v.CreatedByID,
			Comment:                v.Comment,
			RootComponentVersionID: v.RootComponentVersionID,
			IsMilestone:            v.IsMilestone,
			Revision:               v.Revision,
			PublicShare:            v.PublicShare,
		})
	}
	return dto
}

func componentRefDTOs(refs []api.ComponentRef) []ComponentRefDTO {
	out := make([]ComponentRefDTO, 0, len(refs))
	for _, r := range refs {
		out = append(out, ComponentRefDTO{
			ID:             r.ID,
			Name:           r.Name,
			PartNumber:     r.PartNumber,
			PartDesc:       r.PartDesc,
			Material:       r.Material,
			DesignItemID:   r.DesignItemID,
			DesignItemName: r.DesignItemName,
			FusionWebURL:   r.FusionWebURL,
		})
	}
	return out
}

func bomRowDTOs(rows []api.BOMRow) []BOMRowDTO {
	out := make([]BOMRowDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, BOMRowDTO{
			ComponentVersionID: r.ComponentVersionID,
			Name:               r.Name,
			PartNumber:         r.PartNumber,
			PartDesc:           r.PartDesc,
			Material:           r.Material,
			Quantity:           r.Quantity,
		})
	}
	return out
}

func drawingRefDTOs(refs []api.DrawingRef) []DrawingRefDTO {
	out := make([]DrawingRefDTO, 0, len(refs))
	for _, r := range refs {
		out = append(out, DrawingRefDTO{
			ID:            r.ID,
			Name:          r.Name,
			DrawingItemID: r.DrawingItemID,
			ModifiedOn:    fmtTime(r.ModifiedOn),
			ModifiedBy:    r.ModifiedBy,
			FusionWebURL:  r.FusionWebURL,
		})
	}
	return out
}

func locationDTO(loc *api.ItemLocation) LocationDTO {
	dto := LocationDTO{
		HubID:        loc.HubID,
		ProjectID:    loc.ProjectID,
		ProjectAltID: loc.ProjectAltID,
		ProjectName:  loc.ProjectName,
		FolderPath:   make([]FolderRefDTO, 0, len(loc.FolderPath)),
	}
	for _, f := range loc.FolderPath {
		dto.FolderPath = append(dto.FolderPath, FolderRefDTO{ID: f.ID, Name: f.Name})
	}
	return dto
}
