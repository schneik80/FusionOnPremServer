package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ItemDetails holds the rich metadata for a single item fetched from the API.
type ItemDetails struct {
	ID            string
	Name          string
	Typename      string // DesignItem | DrawingItem | ConfiguredDesignItem | BasicItem
	Size          string
	MimeType      string
	ExtensionType string
	FusionWebURL  string
	CreatedOn     time.Time
	CreatedBy     string
	ModifiedOn    time.Time
	ModifiedBy    string
	VersionNumber int
	// Design-specific (DesignItem / ConfiguredDesignItem)
	PartNumber  string
	PartDesc    string
	Material    string
	IsMilestone bool
	// Revision is the formal release revision of the tip (e.g. "B" for Rev B).
	// RESERVED — no API source today; populate it when release data becomes
	// available so the UI's document-state badge can show "Released - Rev X".
	Revision string
	// RootComponentVersionID is the id of tipRootComponentVersion — the
	// componentVersionId argument for the MFGDM component-version calls
	// (thumbnails today; a STEP derivative if one is ever added). Note that the
	// *native* archive path does not use it: api/archive.go works in the Data
	// Management id space, off the item's tip version urn.
	RootComponentVersionID string
	// Version history (most recent first)
	Versions []VersionSummary
}

// VersionSummary is one entry in the version history list.
type VersionSummary struct {
	Number    int
	CreatedOn time.Time
	CreatedBy string
	Comment   string // version save comment (may be empty)
	// RootComponentVersionID is this version's root component version id — the
	// cvId used to fetch a per-version thumbnail. Empty when the field could not
	// be resolved (unmigrated design / partial GraphQL response).
	RootComponentVersionID string
	// IsMilestone marks this version as a milestone (the "release" lane in the
	// history graph). Defaults to false when the per-version field could not be
	// resolved.
	IsMilestone bool
	// Revision is the formal release revision (the "main" lane). RESERVED — no
	// API source exists today, so this is always empty.
	Revision string
	// PublicShare marks this version as having a public share (the rust-orange
	// "share" lane in the history graph). RESERVED — Fusion Team public shares
	// live in a separate service the MDM GraphQL doesn't expose, so this is
	// always false until a share source is wired in.
	PublicShare bool
}

// GetItemDetails fetches rich metadata for a single item plus its version list.
func GetItemDetails(ctx context.Context, token, hubID, itemID string) (*ItemDetails, error) {
	const q = `
		query GetItemDetails($hubId: ID!, $itemId: ID!) {
			item(hubId: $hubId, itemId: $itemId) {
				__typename
				id
				name
				size
				mimeType
				extensionType
				createdOn
				createdBy  { firstName lastName }
				lastModifiedOn
				lastModifiedBy { firstName lastName }
				... on DesignItem {
					fusionWebUrl
					tipVersion { versionNumber }
					tipRootComponentVersion {
						id
						partNumber
						partDescription
						materialName
						isMilestone
					}
				}
				... on DrawingItem {
					fusionWebUrl
					tipVersion { versionNumber }
				}
				... on ConfiguredDesignItem {
					fusionWebUrl
					tipVersion { versionNumber }
				}
			}
			itemVersions(hubId: $hubId, itemId: $itemId) {
				results {
					versionNumber
					name
					createdOn
					createdBy { firstName lastName }
					# itemVersions.results is typed ItemVersion (an interface); the
					# per-version root component version (carrying isMilestone + the
					# cvId for that version's thumbnail) lives on the concrete
					# DesignItemVersion. Type-conditional, so non-design versions
					# (drawings, etc.) simply omit it rather than erroring.
					... on DesignItemVersion {
						rootComponentVersion {
							id
							isMilestone
						}
					}
				}
			}
		}`

	data, err := gqlQuery(ctx, token, q, map[string]any{"hubId": hubID, "itemId": itemID})
	if err != nil {
		return nil, fmt.Errorf("item details: %w", err)
	}

	var raw struct {
		Item struct {
			Typename      string  `json:"__typename"`
			ID            string  `json:"id"`
			Name          string  `json:"name"`
			Size          string  `json:"size"`
			MimeType      string  `json:"mimeType"`
			ExtensionType string  `json:"extensionType"`
			FusionWebURL  string  `json:"fusionWebUrl"`
			CreatedOn     string  `json:"createdOn"`
			CreatedBy     apiUser `json:"createdBy"`
			ModifiedOn    string  `json:"lastModifiedOn"`
			ModifiedBy    apiUser `json:"lastModifiedBy"`
			TipVersion    struct {
				VersionNumber int `json:"versionNumber"`
			} `json:"tipVersion"`
			TipRootComponentVersion struct {
				ID          string `json:"id"`
				PartNumber  string `json:"partNumber"`
				PartDesc    string `json:"partDescription"`
				Material    string `json:"materialName"`
				IsMilestone bool   `json:"isMilestone"`
			} `json:"tipRootComponentVersion"`
		} `json:"item"`
		ItemVersions struct {
			Results []struct {
				VersionNumber        int     `json:"versionNumber"`
				Name                 string  `json:"name"`
				CreatedOn            string  `json:"createdOn"`
				CreatedBy            apiUser `json:"createdBy"`
				RootComponentVersion struct {
					ID          string `json:"id"`
					IsMilestone bool   `json:"isMilestone"`
				} `json:"rootComponentVersion"`
			} `json:"results"`
		} `json:"itemVersions"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("item details decode: %w", err)
	}

	d := &ItemDetails{
		ID:                     raw.Item.ID,
		Name:                   raw.Item.Name,
		Typename:               raw.Item.Typename,
		Size:                   raw.Item.Size,
		MimeType:               raw.Item.MimeType,
		ExtensionType:          raw.Item.ExtensionType,
		FusionWebURL:           raw.Item.FusionWebURL,
		CreatedOn:              parseTime(raw.Item.CreatedOn),
		CreatedBy:              raw.Item.CreatedBy.fullName(),
		ModifiedOn:             parseTime(raw.Item.ModifiedOn),
		ModifiedBy:             raw.Item.ModifiedBy.fullName(),
		VersionNumber:          raw.Item.TipVersion.VersionNumber,
		PartNumber:             raw.Item.TipRootComponentVersion.PartNumber,
		PartDesc:               raw.Item.TipRootComponentVersion.PartDesc,
		Material:               raw.Item.TipRootComponentVersion.Material,
		IsMilestone:            raw.Item.TipRootComponentVersion.IsMilestone,
		RootComponentVersionID: raw.Item.TipRootComponentVersion.ID,
	}

	// Versions — most recent first. Each design version's rootComponentVersion
	// carries the per-version cvId (for that version's thumbnail) and its
	// milestone flag; non-design versions leave these empty/false.
	for i := len(raw.ItemVersions.Results) - 1; i >= 0; i-- {
		v := raw.ItemVersions.Results[i]
		d.Versions = append(d.Versions, VersionSummary{
			Number:                 v.VersionNumber,
			Comment:                v.Name,
			CreatedOn:              parseTime(v.CreatedOn),
			CreatedBy:              v.CreatedBy.fullName(),
			RootComponentVersionID: v.RootComponentVersion.ID,
			IsMilestone:            v.RootComponentVersion.IsMilestone,
			// Revision: reserved, no API source today.
		})
	}

	return d, nil
}

// apiUser is a helper for deserialising User objects.
type apiUser struct {
	First string `json:"firstName"`
	Last  string `json:"lastName"`
}

func (u apiUser) fullName() string {
	name := u.First
	if u.Last != "" {
		if name != "" {
			name += " "
		}
		name += u.Last
	}
	return name
}

// parseTime parses an ISO-8601 / RFC-3339 timestamp returned by the API.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, _ = time.Parse("2006-01-02T15:04:05.000Z", s)
	}
	return t
}
