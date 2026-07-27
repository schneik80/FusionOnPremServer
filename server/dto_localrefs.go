package server

import (
	"net/url"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/production"
	"github.com/schneik80/fusionlocalserver/tasks"
	"github.com/schneik80/fusionlocalserver/whiteboards"
)

// Local reference kinds, mirrored by the Where-Used graph's source checkboxes.
const (
	localRefTask       = "task"
	localRefChat       = "chat"
	localRefWhiteboard = "whiteboard"
	localRefJob        = "job"
	localRefBatch      = "batch"
)

// LocalRefDTO is one place in OUR OWN data that references a hub document: a
// task, a chat channel, a whiteboard, a job's plan, or a batch. It is the
// local-store sibling of ComponentRefDTO (which describes an APS parent
// assembly), shaped for the same relationship graph.
//
// Hits are aggregated per container — one node per channel, not per message —
// with Count carrying the number of references inside it, so a long
// conversation about one part is a single node rather than forty.
type LocalRefDTO struct {
	Kind        string `json:"kind"` // task|chat|whiteboard|job|batch
	Key         string `json:"key"`  // stable node key, unique across kinds
	Name        string `json:"name"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	// Token is the fls: token for the referencing record when one exists
	// (task/job/batch), so clicking the node opens the same dialog the card
	// does. Chat channels and whiteboards have no token scheme and are not
	// navigable from the graph.
	Token string `json:"token,omitempty"`
	Count int    `json:"count"`
	// Detail is free text from the user's own data (a chat excerpt, a step
	// title) — never translated. Via is an enum token the client renders.
	Detail string `json:"detail,omitempty"`
	Via    string `json:"via,omitempty"`
	Author string `json:"author,omitempty"`
	At     string `json:"at,omitempty"`
	// Status is the task's status or the batch's kind — enum tokens, rendered
	// client-side.
	Status string `json:"status,omitempty"`
}

// LocalRefsDTO is the response envelope. Truncated says the cap cut the list
// short, so the UI can say so rather than silently under-report.
type LocalRefsDTO struct {
	Refs      []LocalRefDTO `json:"refs"`
	Truncated bool          `json:"truncated"`
	Cap       int           `json:"cap"`
}

func taskLocalRefDTOs(hits []tasks.DocRefHit, hubID string) []LocalRefDTO {
	out := make([]LocalRefDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, LocalRefDTO{
			Kind:        localRefTask,
			Key:         localRefTask + ":" + h.ProjectID + ":" + h.TaskID,
			Name:        h.Title,
			ProjectID:   h.ProjectID,
			ProjectName: h.ProjectName,
			Token: flsToken("fls:task?", url.Values{
				"hubId":     {hubID},
				"projectId": {h.ProjectID},
				"taskId":    {h.TaskID},
				"title":     {h.Title},
			}),
			Count:  h.Count,
			Status: h.Status,
		})
	}
	return out
}

// chatLocalRefDTOs leaves ProjectName empty — chat's store, unlike its
// siblings, doesn't record it. The handler fills every DTO's project name from
// the authoritative APS listing.
func chatLocalRefDTOs(hits []chat.DocRefHit) []LocalRefDTO {
	out := make([]LocalRefDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, LocalRefDTO{
			Kind:      localRefChat,
			Key:       localRefChat + ":" + h.ProjectID + ":" + h.ChannelID,
			Name:      h.ChannelName,
			ProjectID: h.ProjectID,
			Count:     h.Count,
			Detail:    h.LastBody,
			Author:    h.LastAuthor,
			At:        fmtTime(h.LastAt),
		})
	}
	return out
}

func boardLocalRefDTOs(hits []whiteboards.DocRefHit) []LocalRefDTO {
	out := make([]LocalRefDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, LocalRefDTO{
			Kind:        localRefWhiteboard,
			Key:         localRefWhiteboard + ":" + h.ProjectID + ":" + h.BoardID,
			Name:        h.BoardName,
			ProjectID:   h.ProjectID,
			ProjectName: h.ProjectName,
			Count:       h.Count,
			Author:      h.UpdatedBy,
			At:          fmtTime(h.UpdatedAt),
		})
	}
	return out
}

// productionLocalRefDTOs splits production hits into their two kinds: a hit
// with no batch is the job's plan, one with a batch is that run.
func productionLocalRefDTOs(hits []production.DocRefHit, hubID string) []LocalRefDTO {
	out := make([]LocalRefDTO, 0, len(hits))
	for _, h := range hits {
		base := url.Values{
			"hubId":       {hubID},
			"projectId":   {h.ProjectID},
			"projectName": {h.ProjectName},
			"jobId":       {h.JobID},
			"jobName":     {h.JobName},
		}
		d := LocalRefDTO{
			Kind:        localRefJob,
			Key:         localRefJob + ":" + h.ProjectID + ":" + h.JobID,
			Name:        h.JobName,
			ProjectID:   h.ProjectID,
			ProjectName: h.ProjectName,
			Token:       flsToken("fls:job?", base),
			Count:       h.Count,
			Detail:      h.Where,
			Via:         h.Via,
		}
		if h.BatchID != "" {
			base.Set("batchId", h.BatchID)
			base.Set("batchName", h.BatchName)
			d.Kind = localRefBatch
			d.Key = localRefBatch + ":" + h.ProjectID + ":" + h.JobID + ":" + h.BatchID
			d.Name = h.BatchName
			d.Token = flsToken("fls:batch?", base)
			d.Status = h.BatchKind
		}
		out = append(out, d)
	}
	return out
}

// flsToken builds one of the app's fls: pseudo-URL tokens. Parameter order
// differs from the web encoder's (url.Values sorts keys) — the parsers read by
// name, so only the values matter.
func flsToken(prefix string, v url.Values) string { return prefix + v.Encode() }
