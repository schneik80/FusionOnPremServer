package server

import (
	"strings"

	"github.com/schneik80/fusionlocalserver/api"
)

// WikiPageDTO mirrors api.WikiPage — one published markdown page in a project's
// Wiki folder. title is the display name with its .md extension stripped, which
// is what the sidebar shows; name is the underlying file name.
type WikiPageDTO struct {
	ItemID     string `json:"itemId"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	TipVersion string `json:"tipVersion,omitempty"`
	ModifiedOn string `json:"modifiedOn,omitempty"`
	ModifiedBy string `json:"modifiedBy,omitempty"`
}

// WikiPageContentDTO is the markdown body of a single page (GET /api/wiki/page).
// versionId names the version the body came from when the caller asked for a
// specific one; it is empty for the tip.
type WikiPageContentDTO struct {
	ItemID    string `json:"itemId"`
	VersionID string `json:"versionId,omitempty"`
	Markdown  string `json:"markdown"`
}

// WikiVersionDTO mirrors api.WikiVersion — one entry of a page's history
// (GET /api/wiki/versions), newest first.
type WikiVersionDTO struct {
	VersionID string `json:"versionId"`
	Number    int    `json:"number"`
	CreatedOn string `json:"createdOn,omitempty"`
	CreatedBy string `json:"createdBy,omitempty"`
}

func wikiVersionDTOs(vs []api.WikiVersion) []WikiVersionDTO {
	out := make([]WikiVersionDTO, 0, len(vs))
	for _, v := range vs {
		out = append(out, WikiVersionDTO{VersionID: v.VersionID, Number: v.Number, CreatedOn: v.CreatedOn, CreatedBy: v.CreatedBy})
	}
	return out
}

// WikiRestoreRequest is the POST /api/wiki/restore body: make versionId the
// page's newest version again. baseVersion is the tip the caller saw when it
// chose to restore (stale → 409, like publish); force overrides.
type WikiRestoreRequest struct {
	HubID       string `json:"hubId"`
	DMProjectID string `json:"dmProjectId"`
	ItemID      string `json:"itemId"`
	VersionID   string `json:"versionId"`
	BaseVersion string `json:"baseVersion"`
	Force       bool   `json:"force"`
}

// WikiRestoreResult is the restore's answer: the page with its new tip, plus
// the restored markdown so a linked draft can adopt it without a second
// download.
type WikiRestoreResult struct {
	Page     WikiPageDTO `json:"page"`
	Markdown string      `json:"markdown"`
}

// WikiPublishRequest is the POST /api/wiki/publish body. itemId is the known
// lineage urn of a linked draft (empty for a page never published from here);
// baseVersion is the tip the draft was based on, for stale-overwrite detection;
// force overrides a detected conflict.
type WikiPublishRequest struct {
	HubID       string `json:"hubId"`
	DMProjectID string `json:"dmProjectId"`
	ItemID      string `json:"itemId"`
	Slug        string `json:"slug"`
	Markdown    string `json:"markdown"`
	BaseVersion string `json:"baseVersion"`
	Force       bool   `json:"force"`
}

// WikiImageResult is returned by POST /api/wiki/image after an upload — the
// stored image's lineage urn (referenced via GET /api/wiki/image) and file name.
type WikiImageResult struct {
	ItemID string `json:"itemId"`
	Name   string `json:"name"`
}

// WikiRenameRequest is the POST /api/wiki/rename body. itemId is the published
// page's lineage urn; oldSlug locates its images subfolder to rename alongside.
type WikiRenameRequest struct {
	HubID       string `json:"hubId"`
	DMProjectID string `json:"dmProjectId"`
	ItemID      string `json:"itemId"`
	OldSlug     string `json:"oldSlug"`
	NewSlug     string `json:"newSlug"`
}

// wikiTitle drops a trailing .md (any case) so the sidebar shows a clean title.
func wikiTitle(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".md") {
		return name[:len(name)-3]
	}
	return name
}

func wikiPageDTO(p api.WikiPage) WikiPageDTO {
	return WikiPageDTO{
		ItemID:     p.ItemID,
		Name:       p.Name,
		Title:      wikiTitle(p.Name),
		TipVersion: p.TipVersion,
		ModifiedOn: p.ModifiedOn,
		ModifiedBy: p.ModifiedBy,
	}
}

func wikiPageDTOs(ps []api.WikiPage) []WikiPageDTO {
	out := make([]WikiPageDTO, 0, len(ps))
	for _, p := range ps {
		out = append(out, wikiPageDTO(p))
	}
	return out
}
