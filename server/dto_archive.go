package server

// ArchiveJobDTO is the wire shape of one archive job (GET /api/archives and
// every write's response). hubId/projectId/itemId are echoed back from
// submission so the SPA can match a job to the document it is looking at.
// fileType is empty until APS has told us which native format this version can
// actually be produced in — the UI shows "preparing" until then.
type ArchiveJobDTO struct {
	ID          string `json:"id"`
	DocName     string `json:"docName"`
	FileName    string `json:"fileName,omitempty"`
	FileType    string `json:"fileType,omitempty"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	ErrorCode   string `json:"errorCode,omitempty"`
	HubID       string `json:"hubId,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	DMProjectID string `json:"dmProjectId,omitempty"`
	ItemID      string `json:"itemId,omitempty"`
	CreatedOn   string `json:"createdOn"`
}

// archiveJobDTO snapshots a job's current state.
func archiveJobDTO(j *archiveJob) ArchiveJobDTO {
	j.mu.Lock()
	status, errMsg, errCode := j.status, j.errMsg, j.errCode
	fileType, fileName := j.fileType, j.fileName
	j.mu.Unlock()
	return ArchiveJobDTO{
		ID:          j.ID,
		DocName:     j.DocName,
		FileName:    fileName,
		FileType:    fileType,
		Status:      string(status),
		Error:       errMsg,
		ErrorCode:   errCode,
		HubID:       j.HubID,
		ProjectID:   j.ProjectID,
		DMProjectID: j.DMProjectID,
		ItemID:      j.ItemID,
		CreatedOn:   fmtTime(j.CreatedAt),
	}
}

func archiveJobDTOs(jobs []*archiveJob) []ArchiveJobDTO {
	out := make([]ArchiveJobDTO, len(jobs))
	for i, j := range jobs {
		out[i] = archiveJobDTO(j)
	}
	return out
}
