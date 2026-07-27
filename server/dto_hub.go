package server

import (
	"sort"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/chat"
	"github.com/schneik80/fusionlocalserver/production"
	"github.com/schneik80/fusionlocalserver/tasks"
)

// Hub-overview aggregation caps. The pulse cycler and the contributor list are
// bounded so the response stays small regardless of hub size; the KPI
// ProjectCount is always the full accessible count, so nothing is silently
// hidden — only the per-project detail is capped.
const (
	maxHubPulseProjects = 24
	maxHubContributors  = 8
)

// HubOverviewDTO is the hub dashboard's aggregate snapshot. Every field is a
// COUNT or a per-day bucket derived from the hub's LOCAL stores (tasks,
// production, chat) — never raw item bodies — scoped to the projects the
// caller can access. The collaboration "pulse" (Projects/Pulse) is built from
// local event timestamps only; there is no APS design activity here (that
// stays on the metered per-design endpoints, which never fan out on load).
type HubOverviewDTO struct {
	HubID        string               `json:"hubId"`
	HubName      string               `json:"hubName"`
	GeneratedAt  string               `json:"generatedAt"`
	WindowDays   int                  `json:"windowDays"`
	ProjectCount int                  `json:"projectCount"`
	Tasks        HubTaskStatsDTO      `json:"tasks"`
	Production   HubProdStatsDTO      `json:"production"`
	Chat         HubChatStatsDTO      `json:"chat"`
	Contributors []HubContributorDTO  `json:"contributors"`
	Projects     []HubProjectPulseDTO `json:"projects"`
	Pulse        []HubDayCountDTO     `json:"pulse"`
}

// HubTaskStatsDTO is hub-wide task counts by Kanban status plus derived
// Open (total − done) and Overdue (a live, non-done task past its due date).
type HubTaskStatsDTO struct {
	Total      int `json:"total"`
	Todo       int `json:"todo"`
	InProgress int `json:"inprogress"`
	Blocked    int `json:"blocked"`
	Done       int `json:"done"`
	Open       int `json:"open"`
	Overdue    int `json:"overdue"`
}

// HubProdStatsDTO is hub-wide production counts: jobs and their batches by
// run status (planned/running/complete).
type HubProdStatsDTO struct {
	Jobs     int `json:"jobs"`
	Batches  int `json:"batches"`
	Planned  int `json:"planned"`
	Running  int `json:"running"`
	Complete int `json:"complete"`
}

// HubChatStatsDTO is total messages in the window plus a per-day series.
type HubChatStatsDTO struct {
	Total int              `json:"total"`
	Days  []HubDayCountDTO `json:"days"`
}

// HubContributorDTO is one person's local-authorship tally across the hub
// (tasks created + jobs/batches created). Name is user data — rendered as
// typed, never translated.
type HubContributorDTO struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// HubDayCountDTO is one day's event tally (day is YYYY-MM-DD, UTC).
type HubDayCountDTO struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// HubProjectPulseDTO is one project's local-activity timeline, for the
// heatmap cycler. Total is the window sum; Days is the per-day series.
type HubProjectPulseDTO struct {
	ProjectID   string           `json:"projectId"`
	ProjectName string           `json:"projectName"`
	Total       int              `json:"total"`
	Days        []HubDayCountDTO `json:"days"`
}

// projPulse accumulates one project's daily local-event counts while building
// the overview.
type projPulse struct {
	name string
	days map[string]int
}

// contribAgg accumulates one person's authorship count while building the
// overview (keyed by OIDC sub, falling back to a folded display name).
type contribAgg struct {
	id    string
	name  string
	count int
}

// hubOverviewDTO folds the hub's local store data into the dashboard snapshot.
// names maps every accessible project id (BOTH the GraphQL node id and the
// data-management alt id) to its display name, so any project — even one seen
// only through chat — can be labelled. projectCount is the full accessible
// count (the KPI). All slices are non-nil (repo convention).
func hubOverviewDTO(hubID, hubName string, now time.Time, windowDays, projectCount int,
	names map[string]string, tasksList []tasks.ProjectTask, jobs []production.ProjectJob,
	chatDays []chat.ProjectDayCount) HubOverviewDTO {

	windowStart := now.AddDate(0, 0, -windowDays)
	today := now.UTC().Format("2006-01-02")
	inWindow := func(t time.Time) bool { return !t.IsZero() && !t.Before(windowStart) }
	nameFor := func(pid, fallback string) string {
		if n := names[pid]; n != "" {
			return n
		}
		if fallback != "" {
			return fallback
		}
		return pid
	}

	pulse := map[string]*projPulse{}
	addPulse := func(pid, name, day string, n int) {
		if day == "" || n <= 0 {
			return
		}
		p := pulse[pid]
		if p == nil {
			p = &projPulse{name: name, days: map[string]int{}}
			pulse[pid] = p
		}
		if p.name == "" {
			p.name = name
		}
		p.days[day] += n
	}

	contribs := map[string]*contribAgg{}
	credit := func(id, name string) {
		if id == "" && name == "" {
			return
		}
		key := id
		if key == "" {
			key = "n:" + strings.ToLower(name)
		}
		c := contribs[key]
		if c == nil {
			c = &contribAgg{id: id, name: name}
			contribs[key] = c
		}
		if c.name == "" {
			c.name = name
		}
		c.count++
	}

	// Tasks.
	var ts HubTaskStatsDTO
	for _, pt := range tasksList {
		ts.Total++
		switch pt.Status {
		case "todo":
			ts.Todo++
		case "inprogress":
			ts.InProgress++
		case "blocked":
			ts.Blocked++
		case "done":
			ts.Done++
		}
		if pt.Status != "done" && pt.DueDate != "" && pt.DueDate < today {
			ts.Overdue++
		}
		credit(pt.CreatedBy.ID, pt.CreatedBy.Name)
		when := pt.UpdatedAt
		if when.Before(pt.CreatedAt) {
			when = pt.CreatedAt
		}
		if inWindow(when) {
			addPulse(pt.ProjectID, nameFor(pt.ProjectID, pt.ProjectName), when.UTC().Format("2006-01-02"), 1)
		}
	}
	ts.Open = ts.Total - ts.Done

	// Production.
	var ps HubProdStatsDTO
	for _, pj := range jobs {
		ps.Jobs++
		credit(pj.CreatedBy.ID, pj.CreatedBy.Name)
		for _, b := range pj.Batches {
			if b == nil {
				continue
			}
			ps.Batches++
			switch b.Status {
			case "planned":
				ps.Planned++
			case "running":
				ps.Running++
			case "complete":
				ps.Complete++
			}
			credit(b.CreatedBy.ID, b.CreatedBy.Name)
			if inWindow(b.RunAt) {
				addPulse(pj.ProjectID, nameFor(pj.ProjectID, pj.ProjectName), b.RunAt.UTC().Format("2006-01-02"), 1)
			}
		}
	}

	// Chat (already per-project-per-day, already window-filtered by the scan).
	var cs HubChatStatsDTO
	chatByDay := map[string]int{}
	for _, cd := range chatDays {
		addPulse(cd.ProjectID, nameFor(cd.ProjectID, ""), cd.Day, cd.Count)
		chatByDay[cd.Day] += cd.Count
		cs.Total += cd.Count
	}
	cs.Days = sortedDayCounts(chatByDay)

	// Per-project pulse → sorted, capped list; hub roll-up = the merge of all.
	hubByDay := map[string]int{}
	projects := make([]HubProjectPulseDTO, 0, len(pulse))
	for pid, p := range pulse {
		total := 0
		for day, n := range p.days {
			total += n
			hubByDay[day] += n
		}
		projects = append(projects, HubProjectPulseDTO{
			ProjectID:   pid,
			ProjectName: p.name,
			Total:       total,
			Days:        sortedDayCounts(p.days),
		})
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Total != projects[j].Total {
			return projects[i].Total > projects[j].Total
		}
		return projects[i].ProjectName < projects[j].ProjectName
	})
	if len(projects) > maxHubPulseProjects {
		projects = projects[:maxHubPulseProjects]
	}

	// Contributors → sorted, capped list.
	contribList := make([]HubContributorDTO, 0, len(contribs))
	for _, c := range contribs {
		contribList = append(contribList, HubContributorDTO{ID: c.id, Name: c.name, Count: c.count})
	}
	sort.Slice(contribList, func(i, j int) bool {
		if contribList[i].Count != contribList[j].Count {
			return contribList[i].Count > contribList[j].Count
		}
		return contribList[i].Name < contribList[j].Name
	})
	if len(contribList) > maxHubContributors {
		contribList = contribList[:maxHubContributors]
	}

	return HubOverviewDTO{
		HubID:        hubID,
		HubName:      hubName,
		GeneratedAt:  fmtTime(now),
		WindowDays:   windowDays,
		ProjectCount: projectCount,
		Tasks:        ts,
		Production:   ps,
		Chat:         cs,
		Contributors: contribList,
		Projects:     projects,
		Pulse:        sortedDayCounts(hubByDay),
	}
}

// sortedDayCounts turns a day→count map into a slice sorted ascending by day
// (never nil).
func sortedDayCounts(m map[string]int) []HubDayCountDTO {
	out := make([]HubDayCountDTO, 0, len(m))
	for day, n := range m {
		out = append(out, HubDayCountDTO{Day: day, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}
