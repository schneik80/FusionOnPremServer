package server

import (
	"github.com/schneik80/fusionlocalserver/production"
)

// Production DTOs. Every DTO maps a production store type to the wire with
// fmtTime timestamps and non-nil slices, the same shape rules as the task
// DTOs. A Job carries its full graph (steps, edges, batches) so a single
// GET /api/production/job hydrates the whole canvas.

// ProdUserDTO is a job/step/batch participant on the wire.
type ProdUserDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// ProdDocDTO is a version-pinned document reference on the wire.
type ProdDocDTO struct {
	HubID                  string `json:"hubId"`
	ItemID                 string `json:"itemId"`
	Name                   string `json:"name"`
	Kind                   string `json:"kind,omitempty"`
	VersionID              string `json:"versionId"`
	VersionNumber          int    `json:"versionNumber"`
	RootComponentVersionID string `json:"rootComponentVersionId,omitempty"`
	DMProjectID            string `json:"dmProjectId,omitempty"`
}

// ProdPlanDocDTO is a plan document attached to a step.
type ProdPlanDocDTO struct {
	ID      string      `json:"id"`
	Doc     ProdDocDTO  `json:"doc"`
	AddedBy ProdUserDTO `json:"addedBy"`
	AddedAt string      `json:"addedAt"`
}

// ProdPlaceholderDTO is a per-batch document slot on a step.
type ProdPlaceholderDTO struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind,omitempty"`
	Required bool   `json:"required"`
}

// ProdResultDTO is one outcome of a decision step. Color is a palette token
// (production.ResultColors), resolved to a theme-aware color by the client.
type ProdResultDTO struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Color string `json:"color"`
}

// ProdStepDTO is one flow node. Kind is always populated — the store treats a
// missing kind (pre-v3 files) as "step", and normalising here means no client
// needs its own `?? 'step'`.
type ProdStepDTO struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind"`
	Num          int64                `json:"num"`
	Title        string               `json:"title"`
	Description  string               `json:"description,omitempty"`
	X            float64              `json:"x"`
	Y            float64              `json:"y"`
	Results      []ProdResultDTO      `json:"results"`
	PlanDocs     []ProdPlanDocDTO     `json:"planDocs"`
	Placeholders []ProdPlaceholderDTO `json:"placeholders"`
	CreatedAt    string               `json:"createdAt"`
	UpdatedAt    string               `json:"updatedAt"`
}

// ProdEdgeDTO is one directed link between steps. fromResultId names the
// decision result the edge branches from, empty for a plain step→step link.
type ProdEdgeDTO struct {
	ID           string `json:"id"`
	From         string `json:"from"`
	FromResultID string `json:"fromResultId,omitempty"`
	To           string `json:"to"`
}

// ProdBatchStepDTO is a frozen step within a batch: identity, pinned plan
// documents, and placeholder slots as they stood at batch creation.
type ProdBatchStepDTO struct {
	StepID       string               `json:"stepId"`
	Kind         string               `json:"kind"`
	Num          int64                `json:"num"`
	Title        string               `json:"title"`
	PlanDocs     []ProdPlanDocDTO     `json:"planDocs"`
	Placeholders []ProdPlaceholderDTO `json:"placeholders"`
}

// ProdFulfillmentDTO is one supplied document in a batch.
type ProdFulfillmentDTO struct {
	ID            string      `json:"id"`
	StepID        string      `json:"stepId"`
	PlaceholderID string      `json:"placeholderId,omitempty"`
	Doc           ProdDocDTO  `json:"doc"`
	Source        string      `json:"source,omitempty"`
	IsAsRun       bool        `json:"isAsRun"`
	SuppliedBy    ProdUserDTO `json:"suppliedBy"`
	SuppliedAt    string      `json:"suppliedAt"`
}

// ProdBatchDTO is a dated run of a job.
type ProdBatchDTO struct {
	ID           string               `json:"id"`
	Num          int64                `json:"num"`
	Name         string               `json:"name"`
	Kind         string               `json:"kind"`
	RunAt        string               `json:"runAt"`
	Status       string               `json:"status"`
	Steps        []ProdBatchStepDTO   `json:"steps"`
	Fulfillments []ProdFulfillmentDTO `json:"fulfillments"`
	Refs         []string             `json:"refs"`
	HiddenSteps  []string             `json:"hiddenSteps"`
	CreatedBy    ProdUserDTO          `json:"createdBy"`
	CreatedAt    string               `json:"createdAt"`
	UpdatedAt    string               `json:"updatedAt"`
}

// ProdJobDTO is a job with its full graph.
type ProdJobDTO struct {
	ID          string         `json:"id"`
	Num         int64          `json:"num"`
	ProjectID   string         `json:"projectId"`
	HubID       string         `json:"hubId"`
	ProjectName string         `json:"projectName"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Steps       []ProdStepDTO  `json:"steps"`
	Edges       []ProdEdgeDTO  `json:"edges"`
	Batches     []ProdBatchDTO `json:"batches"`
	CreatedBy   ProdUserDTO    `json:"createdBy"`
	CreatedAt   string         `json:"createdAt"`
	UpdatedAt   string         `json:"updatedAt"`
}

// ProdCapsDTO tells the SPA what the caller may do, so it can disable
// affordances for read-only roles (TaskCapsDTO precedent).
type ProdCapsDTO struct {
	Write    bool `json:"write"`
	Moderate bool `json:"moderate"`
}

// ProdJobSummaryDTO is a job WITHOUT its graph — counts instead of the steps,
// edges and batches. The project list polls every 15s while the tab is open and
// only ever renders these fields; shipping each job's full batch history
// (frozen plan snapshots and all) on every poll, alongside the selected job's
// own fetch, was pure duplicate bandwidth, parse and server-side marshal work.
type ProdJobSummaryDTO struct {
	ID               string      `json:"id"`
	Num              int64       `json:"num"`
	ProjectID        string      `json:"projectId"`
	HubID            string      `json:"hubId"`
	ProjectName      string      `json:"projectName"`
	Name             string      `json:"name"`
	Description      string      `json:"description,omitempty"`
	StepCount        int         `json:"stepCount"`
	BatchCount       int         `json:"batchCount"`
	ActiveBatchCount int         `json:"activeBatchCount"` // planned | running
	CreatedBy        ProdUserDTO `json:"createdBy"`
	CreatedAt        string      `json:"createdAt"`
	UpdatedAt        string      `json:"updatedAt"`
}

// ProdJobListDTO is GET /api/production/jobs.
type ProdJobListDTO struct {
	Jobs         []ProdJobSummaryDTO `json:"jobs"`
	Capabilities ProdCapsDTO         `json:"capabilities"`
}

func prodJobSummaryDTO(j production.Job, projectID, hubID, projectName string) ProdJobSummaryDTO {
	active := 0
	for _, b := range j.Batches {
		if b.Status == "planned" || b.Status == "running" {
			active++
		}
	}
	return ProdJobSummaryDTO{
		ID:               j.ID,
		Num:              j.Num,
		ProjectID:        projectID,
		HubID:            hubID,
		ProjectName:      projectName,
		Name:             j.Name,
		Description:      j.Description,
		StepCount:        len(j.Steps),
		BatchCount:       len(j.Batches),
		ActiveBatchCount: active,
		CreatedBy:        prodUserDTO(j.CreatedBy),
		CreatedAt:        fmtTime(j.CreatedAt),
		UpdatedAt:        fmtTime(j.UpdatedAt),
	}
}

// MyProductionDTO is GET /api/production/mine — the caller's jobs across every
// project on this server. Each job carries its own projectId/hubId/projectName,
// so the cross-project screen can act on one without extra lookups.
type MyProductionDTO struct {
	Jobs []ProdJobDTO `json:"jobs"`
}

func prodUserDTO(r production.UserRef) ProdUserDTO {
	return ProdUserDTO{ID: r.ID, Name: r.Name, Email: r.Email}
}

func prodDocDTO(d production.DocSnapshot) ProdDocDTO {
	return ProdDocDTO{
		HubID:                  d.HubID,
		ItemID:                 d.ItemID,
		Name:                   d.Name,
		Kind:                   d.Kind,
		VersionID:              d.VersionID,
		VersionNumber:          d.VersionNumber,
		RootComponentVersionID: d.RootComponentVersionID,
		DMProjectID:            d.DMProjectID,
	}
}

func prodPlanDocDTOs(docs []production.PlanDoc) []ProdPlanDocDTO {
	out := make([]ProdPlanDocDTO, 0, len(docs))
	for _, pd := range docs {
		out = append(out, ProdPlanDocDTO{
			ID:      pd.ID,
			Doc:     prodDocDTO(pd.Doc),
			AddedBy: prodUserDTO(pd.AddedBy),
			AddedAt: fmtTime(pd.AddedAt),
		})
	}
	return out
}

func prodPlaceholderDTOs(phs []production.Placeholder) []ProdPlaceholderDTO {
	out := make([]ProdPlaceholderDTO, 0, len(phs))
	for _, ph := range phs {
		out = append(out, ProdPlaceholderDTO{
			ID:       ph.ID,
			Label:    ph.Label,
			Kind:     ph.Kind,
			Required: ph.Required,
		})
	}
	return out
}

func prodResultDTOs(rs []production.DecisionResult) []ProdResultDTO {
	out := make([]ProdResultDTO, 0, len(rs))
	for _, r := range rs {
		out = append(out, ProdResultDTO{ID: r.ID, Label: r.Label, Color: r.Color})
	}
	return out
}

// prodStepKind is where a missing kind becomes "step" on the wire. Files
// written before schema v3 have no kind field and the migration deliberately
// does not rewrite them (migrate.Apply never persists), so this is the single
// place both the migrated and the raw-scanned read paths converge.
func prodStepKind(k string) string {
	if k == "" {
		return "step"
	}
	return k
}

func prodStepDTO(st *production.Step) ProdStepDTO {
	return ProdStepDTO{
		ID:           st.ID,
		Kind:         prodStepKind(st.Kind),
		Num:          st.Num,
		Title:        st.Title,
		Description:  st.Description,
		X:            st.X,
		Y:            st.Y,
		Results:      prodResultDTOs(st.Results),
		PlanDocs:     prodPlanDocDTOs(st.PlanDocs),
		Placeholders: prodPlaceholderDTOs(st.Placeholders),
		CreatedAt:    fmtTime(st.CreatedAt),
		UpdatedAt:    fmtTime(st.UpdatedAt),
	}
}

func prodBatchDTO(b *production.Batch) ProdBatchDTO {
	out := ProdBatchDTO{
		ID:           b.ID,
		Num:          b.Num,
		Name:         b.Name,
		Kind:         b.Kind,
		RunAt:        fmtTime(b.RunAt),
		Status:       b.Status,
		Steps:        make([]ProdBatchStepDTO, 0, len(b.Steps)),
		Fulfillments: make([]ProdFulfillmentDTO, 0, len(b.Fulfillments)),
		Refs:         append([]string{}, b.Refs...),
		HiddenSteps:  append([]string{}, b.HiddenSteps...),
		CreatedBy:    prodUserDTO(b.CreatedBy),
		CreatedAt:    fmtTime(b.CreatedAt),
		UpdatedAt:    fmtTime(b.UpdatedAt),
	}
	for _, bs := range b.Steps {
		out.Steps = append(out.Steps, ProdBatchStepDTO{
			StepID:       bs.StepID,
			Kind:         prodStepKind(bs.Kind),
			Num:          bs.Num,
			Title:        bs.Title,
			PlanDocs:     prodPlanDocDTOs(bs.PlanDocs),
			Placeholders: prodPlaceholderDTOs(bs.Placeholders),
		})
	}
	for _, f := range b.Fulfillments {
		out.Fulfillments = append(out.Fulfillments, ProdFulfillmentDTO{
			ID:            f.ID,
			StepID:        f.StepID,
			PlaceholderID: f.PlaceholderID,
			Doc:           prodDocDTO(f.Doc),
			Source:        f.Source,
			IsAsRun:       f.IsAsRun,
			SuppliedBy:    prodUserDTO(f.SuppliedBy),
			SuppliedAt:    fmtTime(f.SuppliedAt),
		})
	}
	return out
}

func prodJobDTO(j production.Job, projectID, hubID, projectName string) ProdJobDTO {
	out := ProdJobDTO{
		ID:          j.ID,
		Num:         j.Num,
		ProjectID:   projectID,
		HubID:       hubID,
		ProjectName: projectName,
		Name:        j.Name,
		Description: j.Description,
		Steps:       make([]ProdStepDTO, 0, len(j.Steps)),
		Edges:       make([]ProdEdgeDTO, 0, len(j.Edges)),
		Batches:     make([]ProdBatchDTO, 0, len(j.Batches)),
		CreatedBy:   prodUserDTO(j.CreatedBy),
		CreatedAt:   fmtTime(j.CreatedAt),
		UpdatedAt:   fmtTime(j.UpdatedAt),
	}
	for _, st := range j.Steps {
		out.Steps = append(out.Steps, prodStepDTO(st))
	}
	for _, e := range j.Edges {
		out.Edges = append(out.Edges, ProdEdgeDTO{
			ID:           e.ID,
			From:         e.From,
			FromResultID: e.FromResultID,
			To:           e.To,
		})
	}
	for _, b := range j.Batches {
		out.Batches = append(out.Batches, prodBatchDTO(b))
	}
	return out
}
