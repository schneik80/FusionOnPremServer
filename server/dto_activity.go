package server

import "github.com/schneik80/fusionlocalserver/api"

// Activity report DTOs mirror the api.ActivityReport tree with camelCase tags
// and RFC3339 time strings (empty when zero). Slices are always [] not null.

type ActorDTO struct {
	AccountID   string `json:"accountId,omitempty"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email,omitempty"`
}

type ContributorDTO struct {
	AccountID   string `json:"accountId,omitempty"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email,omitempty"`
	EventCount  int    `json:"eventCount"`
	FirstSeen   string `json:"firstSeen,omitempty"`
	LastSeen    string `json:"lastSeen,omitempty"`
}

type TimeBucketDTO struct {
	Start string `json:"start"`
	Count int    `json:"count"`
}

type ChildSummaryDTO struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	EventCount int    `json:"eventCount"`
	LastChange string `json:"lastChange,omitempty"`
}

type ActivityEventDTO struct {
	EntityType    string   `json:"entityType"`
	EntityID      string   `json:"entityId"`
	EntityName    string   `json:"entityName"`
	Timestamp     string   `json:"timestamp,omitempty"`
	Action        string   `json:"action"`
	Actor         ActorDTO `json:"actor"`
	VersionNumber int      `json:"versionNumber,omitempty"`
	ProjectID     string   `json:"projectId,omitempty"`
	ProjectName   string   `json:"projectName,omitempty"`
	FolderURN     string   `json:"folderUrn,omitempty"`
	LineageURN    string   `json:"lineageUrn,omitempty"`
	FileType      string   `json:"fileType,omitempty"`
	WebURL        string   `json:"webUrl,omitempty"`
	Views         int      `json:"views,omitempty"`
	Comments      int      `json:"comments,omitempty"`
	Likes         int      `json:"likes,omitempty"`
	Detail        string   `json:"detail,omitempty"`
}

type ActivityReportDTO struct {
	Scope            string             `json:"scope"`
	ScopeID          string             `json:"scopeId,omitempty"`
	ScopeName        string             `json:"scopeName,omitempty"`
	HubID            string             `json:"hubId,omitempty"`
	TotalEvents      int                `json:"totalEvents"`
	DesignCount      int                `json:"designCount"`
	VersionCount     int                `json:"versionCount"`
	ContributorCount int                `json:"contributorCount"`
	CreatedOn        string             `json:"createdOn,omitempty"`
	LastChange       string             `json:"lastChange,omitempty"`
	Bucket           string             `json:"bucket"`
	Timeline         []TimeBucketDTO    `json:"timeline"`
	Contributors     []ContributorDTO   `json:"contributors"`
	Children         []ChildSummaryDTO  `json:"children"`
	Events           []ActivityEventDTO `json:"events"`
	EventsTruncated  bool               `json:"eventsTruncated"`
	ChildrenIncluded int                `json:"childrenIncluded,omitempty"`
	ChildrenTotal    int                `json:"childrenTotal,omitempty"`
}

func actorDTO(a api.Actor) ActorDTO {
	return ActorDTO{AccountID: a.AccountID, DisplayName: a.DisplayName, Email: a.Email}
}

func activityReportDTO(r api.ActivityReport) ActivityReportDTO {
	dto := ActivityReportDTO{
		Scope:            string(r.Scope),
		ScopeID:          r.ScopeID,
		ScopeName:        r.ScopeName,
		HubID:            r.HubID,
		TotalEvents:      r.TotalEvents,
		ChildrenIncluded: r.ChildrenIncluded,
		ChildrenTotal:    r.ChildrenTotal,
		DesignCount:      r.DesignCount,
		VersionCount:     r.VersionCount,
		ContributorCount: r.ContributorCount,
		CreatedOn:        fmtTime(r.CreatedOn),
		LastChange:       fmtTime(r.LastChange),
		Bucket:           string(r.Bucket),
		Timeline:         make([]TimeBucketDTO, 0, len(r.Timeline)),
		Contributors:     make([]ContributorDTO, 0, len(r.Contributors)),
		Children:         make([]ChildSummaryDTO, 0, len(r.Children)),
		Events:           make([]ActivityEventDTO, 0, len(r.Events)),
		EventsTruncated:  r.EventsTruncated,
	}
	for _, b := range r.Timeline {
		dto.Timeline = append(dto.Timeline, TimeBucketDTO{Start: fmtTime(b.Start), Count: b.Count})
	}
	for _, c := range r.Contributors {
		dto.Contributors = append(dto.Contributors, ContributorDTO{
			AccountID:   c.AccountID,
			DisplayName: c.DisplayName,
			Email:       c.Email,
			EventCount:  c.EventCount,
			FirstSeen:   fmtTime(c.FirstSeen),
			LastSeen:    fmtTime(c.LastSeen),
		})
	}
	for _, c := range r.Children {
		dto.Children = append(dto.Children, ChildSummaryDTO{
			Type:       c.Type,
			ID:         c.ID,
			Name:       c.Name,
			EventCount: c.EventCount,
			LastChange: fmtTime(c.LastChange),
		})
	}
	for _, e := range r.Events {
		dto.Events = append(dto.Events, ActivityEventDTO{
			EntityType:    e.EntityType,
			EntityID:      e.EntityID,
			EntityName:    e.EntityName,
			Timestamp:     fmtTime(e.Timestamp),
			Action:        e.Action,
			Actor:         actorDTO(e.Actor),
			VersionNumber: e.VersionNumber,
			ProjectID:     e.ProjectID,
			ProjectName:   e.ProjectName,
			FolderURN:     e.FolderURN,
			LineageURN:    e.LineageURN,
			FileType:      e.FileType,
			WebURL:        e.WebURL,
			Views:         e.Views,
			Comments:      e.Comments,
			Likes:         e.Likes,
			Detail:        e.Detail,
		})
	}
	return dto
}
