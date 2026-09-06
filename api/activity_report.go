package api

import (
	"sort"
	"strings"
	"time"
)

// Scope is the hierarchy level a report is computed for.
type Scope string

const (
	ScopeHub     Scope = "hub"
	ScopeProject Scope = "project"
	ScopeFolder  Scope = "folder"
	ScopeDesign  Scope = "design"
)

// Bucket is the time-series granularity.
type Bucket string

const (
	BucketHour  Bucket = "hour"
	BucketDay   Bucket = "day"
	BucketMonth Bucket = "month"
	BucketYear  Bucket = "year"
)

// maxReportEvents caps the events list returned in a report so a pathological
// case can't produce an unbounded payload. The heat map buckets from this list,
// so the cap is high enough that a realistic design — or a large assembly's
// rolled-up child set — is rendered in full. TotalEvents always reflects the
// true (uncapped) count; EventsTruncated flags when the list was trimmed.
const maxReportEvents = 50000

// Contributor is one actor's rolled-up participation within a scope.
type Contributor struct {
	AccountID   string    `json:"accountId,omitempty"`
	DisplayName string    `json:"displayName"`
	Email       string    `json:"email,omitempty"`
	EventCount  int       `json:"eventCount"`
	FirstSeen   time.Time `json:"firstSeen,omitempty"`
	LastSeen    time.Time `json:"lastSeen,omitempty"`
}

// TimeBucket is one point on the activity time-series (bucket start + count).
type TimeBucket struct {
	Start time.Time `json:"start"`
	Count int       `json:"count"`
}

// ChildSummary rolls activity up by the immediate child level (projects within
// a hub, folders within a project, designs within a folder).
type ChildSummary struct {
	Type       string    `json:"type"`
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	EventCount int       `json:"eventCount"`
	LastChange time.Time `json:"lastChange,omitempty"`
}

// ActivityReport is the normalized, scope-aggregated result the API serves.
type ActivityReport struct {
	Scope     Scope  `json:"scope"`
	ScopeID   string `json:"scopeId,omitempty"`
	ScopeName string `json:"scopeName,omitempty"`
	HubID     string `json:"hubId,omitempty"`

	TotalEvents      int       `json:"totalEvents"`
	DesignCount      int       `json:"designCount"`
	VersionCount     int       `json:"versionCount"`
	ContributorCount int       `json:"contributorCount"`
	CreatedOn        time.Time `json:"createdOn,omitempty"`
	LastChange       time.Time `json:"lastChange,omitempty"`

	Bucket       Bucket         `json:"bucket"`
	Timeline     []TimeBucket   `json:"timeline"`
	Contributors []Contributor  `json:"contributors"`
	Children     []ChildSummary `json:"children"`

	Events          []ActivityEvent `json:"events"`
	EventsTruncated bool            `json:"eventsTruncated"`

	// ChildrenIncluded / ChildrenTotal describe a roll-up's child scope: how
	// many child documents were merged and how many the caller asked for.
	// When they differ the report is a visible partial (the UI says so and
	// offers the rest) — never a silent cap.
	ChildrenIncluded int `json:"childrenIncluded,omitempty"`
	ChildrenTotal    int `json:"childrenTotal,omitempty"`
}

// inScope reports whether an event belongs to the given scope+id.
// An empty id at hub scope matches everything (whole-feed report).
func inScope(e ActivityEvent, scope Scope, id string) bool {
	switch scope {
	case ScopeProject:
		return e.ProjectID == id
	case ScopeFolder:
		return e.FolderURN == id
	case ScopeDesign:
		return e.EntityID == id || e.LineageURN == id
	default: // hub
		return id == "" || e.HubID == id || e.HubForgeID == id
	}
}

// BuildReport filters events to scope+id and the optional [from,to] window
// (zero values = unbounded), then computes the aggregated report at the given
// bucket granularity. Bucket defaults to day when empty/unknown.
func BuildReport(events []ActivityEvent, scope Scope, id string, bucket Bucket, from, to time.Time) ActivityReport {
	if scope == "" {
		scope = ScopeHub
	}
	bucket = normalizeBucket(bucket)

	rep := ActivityReport{Scope: scope, ScopeID: id, Bucket: bucket}

	// Filter.
	filtered := make([]ActivityEvent, 0, len(events))
	for _, e := range events {
		if !inScope(e, scope, id) {
			continue
		}
		if !from.IsZero() && e.Timestamp.Before(from) {
			continue
		}
		if !to.IsZero() && e.Timestamp.After(to) {
			continue
		}
		filtered = append(filtered, e)
	}

	rep.TotalEvents = len(filtered)
	rep.ScopeName = scopeName(scope, id, filtered)
	rep.HubID = scopeHubID(filtered)

	// Aggregations.
	rep.Timeline = buildTimeline(filtered, bucket)
	rep.Contributors = buildContributors(filtered)
	rep.ContributorCount = len(rep.Contributors)
	rep.Children = buildChildren(filtered, scope)
	rep.DesignCount, rep.VersionCount = designAndVersionCounts(filtered)
	rep.CreatedOn, rep.LastChange = createdAndLastChange(filtered)

	// Recent events, newest first, capped.
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})
	if len(filtered) > maxReportEvents {
		rep.Events = filtered[:maxReportEvents]
		rep.EventsTruncated = true
	} else {
		rep.Events = filtered
	}
	if rep.Timeline == nil {
		rep.Timeline = []TimeBucket{}
	}
	if rep.Contributors == nil {
		rep.Contributors = []Contributor{}
	}
	if rep.Children == nil {
		rep.Children = []ChildSummary{}
	}
	if rep.Events == nil {
		rep.Events = []ActivityEvent{}
	}
	return rep
}

func normalizeBucket(b Bucket) Bucket {
	switch b {
	case BucketHour, BucketDay, BucketMonth, BucketYear:
		return b
	default:
		return BucketDay
	}
}

// bucketStart truncates a timestamp to the start of its bucket (UTC).
func bucketStart(t time.Time, b Bucket) time.Time {
	t = t.UTC()
	switch b {
	case BucketHour:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
	case BucketMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	case BucketYear:
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	default: // day
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
}

func buildTimeline(events []ActivityEvent, b Bucket) []TimeBucket {
	counts := make(map[time.Time]int)
	for _, e := range events {
		if e.Timestamp.IsZero() {
			continue
		}
		counts[bucketStart(e.Timestamp, b)]++
	}
	out := make([]TimeBucket, 0, len(counts))
	for start, n := range counts {
		out = append(out, TimeBucket{Start: start, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

func buildContributors(events []ActivityEvent) []Contributor {
	idx := make(map[string]*Contributor)
	for _, e := range events {
		key := e.Actor.AccountID
		if key == "" {
			key = e.Actor.DisplayName
		}
		if key == "" {
			continue
		}
		c := idx[key]
		if c == nil {
			c = &Contributor{
				AccountID:   e.Actor.AccountID,
				DisplayName: e.Actor.DisplayName,
				Email:       e.Actor.Email,
			}
			idx[key] = c
		}
		c.EventCount++
		if !e.Timestamp.IsZero() {
			if c.FirstSeen.IsZero() || e.Timestamp.Before(c.FirstSeen) {
				c.FirstSeen = e.Timestamp
			}
			if e.Timestamp.After(c.LastSeen) {
				c.LastSeen = e.Timestamp
			}
		}
	}
	out := make([]Contributor, 0, len(idx))
	for _, c := range idx {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EventCount != out[j].EventCount {
			return out[i].EventCount > out[j].EventCount
		}
		return out[i].DisplayName < out[j].DisplayName
	})
	return out
}

// buildChildren rolls up by the immediate child level below scope.
func buildChildren(events []ActivityEvent, scope Scope) []ChildSummary {
	var childType string
	keyOf := func(e ActivityEvent) (id, name string) { return "", "" }
	switch scope {
	case ScopeHub:
		childType = "project"
		keyOf = func(e ActivityEvent) (string, string) { return e.ProjectID, e.ProjectName }
	case ScopeProject:
		childType = "folder"
		keyOf = func(e ActivityEvent) (string, string) { return e.FolderURN, folderShortName(e.FolderURN) }
	case ScopeFolder:
		childType = "design"
		keyOf = func(e ActivityEvent) (string, string) { return e.EntityID, e.EntityName }
	default: // design has no children
		return nil
	}

	idx := make(map[string]*ChildSummary)
	var order []string
	for _, e := range events {
		id, name := keyOf(e)
		if id == "" {
			continue
		}
		c := idx[id]
		if c == nil {
			c = &ChildSummary{Type: childType, ID: id, Name: name}
			idx[id] = c
			order = append(order, id)
		}
		if c.Name == "" {
			c.Name = name
		}
		c.EventCount++
		if e.Timestamp.After(c.LastChange) {
			c.LastChange = e.Timestamp
		}
	}
	out := make([]ChildSummary, 0, len(order))
	for _, id := range order {
		out = append(out, *idx[id])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EventCount != out[j].EventCount {
			return out[i].EventCount > out[j].EventCount
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// designAndVersionCounts returns the number of distinct designs (by lineage)
// and the sum of each design's highest observed version number.
func designAndVersionCounts(events []ActivityEvent) (designs, versions int) {
	maxVer := make(map[string]int)
	for _, e := range events {
		if e.EntityType != "design" {
			continue
		}
		key := e.LineageURN
		if key == "" {
			key = e.EntityID
		}
		if key == "" {
			continue
		}
		if e.VersionNumber > maxVer[key] {
			maxVer[key] = e.VersionNumber
		} else if _, ok := maxVer[key]; !ok {
			maxVer[key] = e.VersionNumber
		}
	}
	for _, v := range maxVer {
		versions += v
	}
	return len(maxVer), versions
}

func createdAndLastChange(events []ActivityEvent) (created, last time.Time) {
	for _, e := range events {
		if !e.CreatedOn.IsZero() {
			if created.IsZero() || e.CreatedOn.Before(created) {
				created = e.CreatedOn
			}
		}
		if e.Timestamp.After(last) {
			last = e.Timestamp
		}
	}
	return created, last
}

func scopeName(scope Scope, id string, events []ActivityEvent) string {
	if len(events) == 0 {
		return ""
	}
	switch scope {
	case ScopeProject:
		return events[0].ProjectName
	case ScopeDesign:
		return events[0].EntityName
	case ScopeFolder:
		return folderShortName(id)
	default: // hub
		return events[0].HubName
	}
}

func scopeHubID(events []ActivityEvent) string {
	for _, e := range events {
		if e.HubID != "" {
			return e.HubID
		}
	}
	return ""
}

// folderShortName renders a usable label from a folder URN (the feed carries no
// folder display name). e.g. "urn:adsk.wipprod:fs.folder:co.lS9N44…" -> "folder co.lS9N44…".
func folderShortName(urn string) string {
	if urn == "" {
		return ""
	}
	if i := strings.LastIndex(urn, ":"); i >= 0 && i+1 < len(urn) {
		short := urn[i+1:]
		if len(short) > 12 {
			short = short[:12] + "…"
		}
		return "folder " + short
	}
	return urn
}
