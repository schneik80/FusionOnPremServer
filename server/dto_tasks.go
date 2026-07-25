package server

import (
	"github.com/schneik80/fusionlocalserver/tasks"
)

// TaskUserDTO is a task participant (assignee / creator) on the wire.
type TaskUserDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// TaskDTO is a task on the wire. Every task carries its project identity
// (projectId/hubId/projectName) so the cross-project "my tasks" list and
// the fls:task card can act on a task without extra lookups; in
// project-scoped responses the fields are simply uniform.
type TaskDTO struct {
	ID          string       `json:"id"`
	Num         int64        `json:"num"`
	ProjectID   string       `json:"projectId"`
	HubID       string       `json:"hubId"`
	ProjectName string       `json:"projectName"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Status      string       `json:"status"`
	Priority    string       `json:"priority"`
	DueDate     string       `json:"dueDate,omitempty"`
	Assignee    *TaskUserDTO `json:"assignee,omitempty"`
	CreatedBy   TaskUserDTO  `json:"createdBy"`
	CreatedAt   string       `json:"createdAt"`
	UpdatedAt   string       `json:"updatedAt"`
	DocRefs     []string     `json:"docRefs"`
	Rank        float64      `json:"rank"`
	StartDate   string       `json:"startDate,omitempty"`
	EndDate     string       `json:"endDate,omitempty"`
	Progress    int          `json:"progress,omitempty"`
	Milestone   bool         `json:"milestone,omitempty"`
	DependsOn   []string     `json:"dependsOn"` // always [], never null (slices-never-nil)
	Stage       string       `json:"stage,omitempty"`
}

// TaskCapsDTO tells the SPA what the caller may do with this project's
// tasks, so it can disable the create/edit affordances for read-only roles
// instead of letting a write bounce off the 403 (ChatCapsDTO precedent).
type TaskCapsDTO struct {
	Write    bool `json:"write"`
	Moderate bool `json:"moderate"`
}

// TaskListDTO is GET /api/tasks.
type TaskListDTO struct {
	Tasks        []TaskDTO   `json:"tasks"`
	Capabilities TaskCapsDTO `json:"capabilities"`
}

// MyTasksDTO is GET /api/tasks/mine.
type MyTasksDTO struct {
	Tasks []TaskDTO `json:"tasks"`
}

func taskUserDTO(r tasks.UserRef) TaskUserDTO {
	return TaskUserDTO{ID: r.ID, Name: r.Name, Email: r.Email}
}

func taskDTO(t tasks.Task, projectID, hubID, projectName string) TaskDTO {
	dto := TaskDTO{
		ID:          t.ID,
		Num:         t.Num,
		ProjectID:   projectID,
		HubID:       hubID,
		ProjectName: projectName,
		Title:       t.Title,
		Description: t.Description,
		Status:      t.Status,
		Priority:    t.Priority,
		DueDate:     t.DueDate,
		CreatedBy:   taskUserDTO(t.CreatedBy),
		CreatedAt:   fmtTime(t.CreatedAt),
		UpdatedAt:   fmtTime(t.UpdatedAt),
		DocRefs:     t.DocRefs,
		Rank:        t.Rank,
		StartDate:   t.StartDate,
		EndDate:     t.EndDate,
		Progress:    t.Progress,
		Milestone:   t.Milestone,
		DependsOn:   t.DependsOn,
		Stage:       t.Stage,
	}
	if dto.DocRefs == nil {
		dto.DocRefs = []string{}
	}
	if dto.DependsOn == nil {
		dto.DependsOn = []string{}
	}
	if t.Assignee != nil {
		a := taskUserDTO(*t.Assignee)
		dto.Assignee = &a
	}
	return dto
}
