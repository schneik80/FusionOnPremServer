package api

import (
	"encoding/json"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// OpCost is what the budget layer knows about one GraphQL operation, keyed by
// its operation name (every routine query declares one: `query GetHubs …`).
//
// Points are MEASURED wherever the server log has shown a real 429 for the
// route (1854 of them by 2026-09-07; the rejected query's exact cost is in
// the message). Two things those numbers taught:
//
//   - Most connections are charged per RETURNED row: an activity report with
//     50 versions cost 122, a where-used page of 50 cost 666, an occurrences
//     page of 50 cost 916, and a details query 211 for a short history but
//     436 for a long one. So a paged op's Measured is its cost at a full
//     page (Limit rows) and ActualForRows scales it by the rows a response
//     really carried, which is what the scheduler reconciles the bucket with.
//   - A few are charged on the REQUESTED page regardless: GetHubs cost 311
//     for a hub list of four. Those are flagged ByLimit and never scaled.
//
// The old documented formula ("1 per object field × page size") was wrong
// in both directions; Estimate() below keeps the calibrated static model
// only as the fallback for an op nobody has measured yet.
type OpCost struct {
	Roots     int  // root Query fields (10 each)
	Fixed     int  // fields outside any results page
	RowFields int  // fields per results row (static model only)
	Limit     int  // page size the query text hardcodes; 0 = not paginated
	Measured  int  // exact cost at a full page (paged) or per call (fixed); 0 = unmeasured
	ByLimit   bool // charged on the requested page, not the returned rows

	// CacheTTL is how long an identical (op, vars, subject) answer is served
	// from the coalescing cache; 0 = never. Seconds, by design: the cache
	// collapses concurrent and near-concurrent duplicates, it is not a
	// stale-while-revalidate store.
	CacheTTL time.Duration
	// Shared drops the subject from the cache key. Only for ACL-free facts of
	// a hub the caller is already locked to (a DM id, a thumbnail's status) —
	// listings, details, members and anything else a user's role filters
	// must stay per-subject.
	Shared bool
}

// Estimate is the calibrated static model, the fallback for an unmeasured op.
func (c OpCost) Estimate() int {
	return 10*c.Roots + c.Fixed + (c.RowFields+1)*c.Limit
}

// Points is the admission estimate: the measured full-page cost, else the
// static model.
func (c OpCost) Points() int {
	if c.Measured > 0 {
		return c.Measured
	}
	return c.Estimate()
}

// base is the part of the cost that does not scale with rows.
func (c OpCost) base() int { return 10*c.Roots + c.Fixed }

// ActualForRows is what the gateway most likely charged for a response that
// carried rows result rows: the base plus the per-row share of the full-page
// cost. For an unpaged or ByLimit op it is Points() regardless of rows. The
// scheduler refunds the difference from the admission estimate.
func (c OpCost) ActualForRows(rows int) int {
	if c.Limit <= 0 || c.ByLimit {
		return c.Points()
	}
	full := c.Points()
	b := c.base()
	if full <= b {
		return full
	}
	if rows < 0 {
		rows = 0
	}
	if rows > c.Limit {
		rows = c.Limit
	}
	return b + (full-b)*rows/c.Limit
}

const (
	ttlListing = 15 * time.Second
	ttlScope   = 30 * time.Second
	ttlProbe   = 5 * time.Second
)

// opCosts is the registry. A query missing here is charged formulaFallback
// and never cached — add it when you add the query.
var opCosts = map[string]OpCost{
	// Navigation (api/queries.go). Hubs are charged on the requested page:
	// a list of four cost 311 every time.
	"GetHubs":             {Roots: 1, Fixed: 1, RowFields: 5, Limit: 50, Measured: 311, ByLimit: true, CacheTTL: ttlScope},
	"GetHubsNext":         {Roots: 1, Fixed: 1, RowFields: 5, Limit: 50, Measured: 311, ByLimit: true, CacheTTL: ttlScope},
	"GetProjects":         {Roots: 1, Fixed: 2, RowFields: 7, Limit: 50, Measured: 366, CacheTTL: ttlScope},
	"GetProjectsNext":     {Roots: 1, Fixed: 2, RowFields: 7, Limit: 50, Measured: 366, CacheTTL: ttlScope},
	"GetFolders":          {Roots: 1, Fixed: 1, RowFields: 2, Limit: 50, CacheTTL: ttlListing},
	"GetFoldersNext":      {Roots: 1, Fixed: 1, RowFields: 2, Limit: 50, CacheTTL: ttlListing},
	"GetProjectItems":     {Roots: 1, Fixed: 1, RowFields: 6, Limit: 50, Measured: 261, CacheTTL: ttlListing},
	"GetProjectItemsNext": {Roots: 1, Fixed: 1, RowFields: 6, Limit: 50, Measured: 261, CacheTTL: ttlListing},
	"GetItems":            {Roots: 1, Fixed: 1, RowFields: 6, Limit: 50, Measured: 261, CacheTTL: ttlListing},
	"GetItemsNext":        {Roots: 1, Fixed: 1, RowFields: 6, Limit: 50, Measured: 261, CacheTTL: ttlListing},

	// Details / versions (api/details.go). Details cost 211 for a short
	// history and 436 for a long one: the version rows are cheap (~2 each),
	// the item block is most of it.
	"GetItemDetails":      {Roots: 2, Fixed: 30, RowFields: 11, Limit: 50, Measured: 436, CacheTTL: ttlListing},
	"GetItemSummary":      {Roots: 1, Fixed: 27, Measured: 60, CacheTTL: ttlListing},
	"GetItemVersionsNext": {Roots: 1, Fixed: 1, RowFields: 11, Limit: 50, Measured: 120, CacheTTL: ttlListing},
	"DesignActivity":      {Roots: 2, Fixed: 12, RowFields: 11, Limit: 50, Measured: 122, CacheTTL: ttlScope},
	"ChildActivity":       {Roots: 2, Fixed: 4, RowFields: 7, Limit: 20, Measured: 60, CacheTTL: ttlScope},
	"ChildActivityNext":   {Roots: 1, Fixed: 1, RowFields: 7, Limit: 20, Measured: 50, CacheTTL: ttlScope},

	// History (v3, api/history.go). Unmeasured: the v3 gateway's weights
	// have never appeared in a 429 message.
	"GetItemHistory":     {Roots: 1, Fixed: 2, RowFields: 7, Limit: 50, CacheTTL: ttlListing},
	"GetItemHistoryNext": {Roots: 1, Fixed: 2, RowFields: 7, Limit: 50, CacheTTL: ttlListing},

	// References (api/refs.go, api/occurrences.go). Occurrences are the
	// most expensive rows in the app (~18 each); where-used ~13.
	"GetOccurrences":           {Roots: 1, Fixed: 2, RowFields: 12, Limit: 50, Measured: 916, CacheTTL: ttlListing},
	"GetOccurrencesNext":       {Roots: 1, Fixed: 2, RowFields: 12, Limit: 50, Measured: 916, CacheTTL: ttlListing},
	"GetWhereUsed":             {Roots: 1, Fixed: 2, RowFields: 10, Limit: 50, Measured: 666, CacheTTL: ttlListing},
	"GetWhereUsedNext":         {Roots: 1, Fixed: 2, RowFields: 10, Limit: 50, Measured: 666, CacheTTL: ttlListing},
	"GetDrawingSource":         {Roots: 1, Fixed: 13, CacheTTL: ttlListing},
	"GetDrawingsForDesign":     {Roots: 1, Fixed: 4, RowFields: 8, Limit: 60, Measured: 576, CacheTTL: ttlListing},
	"GetDrawingsForDesignNext": {Roots: 1, Fixed: 4, RowFields: 8, Limit: 60, Measured: 576, CacheTTL: ttlListing},
	"AllOccurrences":           {Roots: 1, Fixed: 2, RowFields: 9, Limit: 50, Measured: 266, CacheTTL: ttlListing},
	"AllOccurrencesNext":       {Roots: 1, Fixed: 2, RowFields: 9, Limit: 50, Measured: 266, CacheTTL: ttlListing},

	// Locate (api/locate.go): the whole nested chain is a handful of points.
	"LocateItem":      {Roots: 1, Fixed: 26, Measured: 14, CacheTTL: ttlListing},
	"GetFolderParent": {Roots: 1, Fixed: 3, Measured: 11, CacheTTL: ttlListing},

	// Per-row probes (api/thumbnail.go, api/classify.go). Shared: a
	// thumbnail's status/URL and an assembly's shape carry no ACL dimension
	// beyond the hub lock requireHub already enforces, and the thumbnail
	// cache has always been process-wide.
	"GetThumbnail":         {Roots: 1, Fixed: 3, Measured: 20, CacheTTL: ttlProbe, Shared: true},
	"ClassifyAndThumbnail": {Roots: 1, Fixed: 5, Measured: 26, CacheTTL: ttlProbe, Shared: true},
	"ClassifyAssembly":     {Roots: 1, Fixed: 2, Measured: 16, CacheTTL: ttlProbe, Shared: true},

	// Properties (api/properties.go, api/customprops.go).
	"GetPhysicalProperties": {Roots: 1, Fixed: 38, Measured: 28, CacheTTL: ttlListing},
	"GetCustomProperties":   {Roots: 1, Fixed: 2, RowFields: 2, Limit: 50, Measured: 15, CacheTTL: ttlListing},

	// Permissions (api/permissions.go). A seven-member roster cost 66, which
	// the static model reproduces (10 + 1 + 7×8); larger ones scale by row.
	"GetProjectGroups":     {Roots: 1, Fixed: 1, RowFields: 3, Limit: 50, CacheTTL: ttlScope},
	"GetProjectGroupsNext": {Roots: 1, Fixed: 1, RowFields: 3, Limit: 50, CacheTTL: ttlScope},
	"PM":                   {Roots: 1, Fixed: 1, RowFields: 8, Limit: 50, CacheTTL: ttlScope},
	"PMN":                  {Roots: 1, Fixed: 1, RowFields: 8, Limit: 50, CacheTTL: ttlScope},
	"FM":                   {Roots: 1, Fixed: 1, RowFields: 8, Limit: 50, CacheTTL: ttlScope},
	"FMN":                  {Roots: 1, Fixed: 1, RowFields: 8, Limit: 50, CacheTTL: ttlScope},
	"FG":                   {Roots: 1, Fixed: 1, RowFields: 3, Limit: 50, CacheTTL: ttlScope},
	"FGN":                  {Roots: 1, Fixed: 1, RowFields: 3, Limit: 50, CacheTTL: ttlScope},
	"GetGroupMembers":      {Roots: 1, Fixed: 1, RowFields: 8, Limit: 50, CacheTTL: ttlScope},
	"GetGroupMembersNext":  {Roots: 1, Fixed: 1, RowFields: 8, Limit: 50, CacheTTL: ttlScope},

	// Wiki (api/wiki.go). The DM id of a hub is immutable and ACL-free.
	"HubDMID": {Roots: 1, Fixed: 2, Measured: 11, CacheTTL: 12 * time.Hour, Shared: true},
}

// formulaFallback is charged for a query the registry does not know (debug
// probes, introspection): one root plus a modest page.
const formulaFallback = 60

// OpCosts returns a copy of the registry for the calibration endpoint.
func OpCosts() map[string]OpCost {
	out := make(map[string]OpCost, len(opCosts))
	for k, v := range opCosts {
		out[k] = v
	}
	return out
}

// costFor resolves the operation name and cost metadata for a query text.
func costFor(query string) (op string, c OpCost, known bool) {
	op = apsbudget.OperationName(query)
	if op == "" {
		return "", OpCost{}, false
	}
	c, known = opCosts[op]
	return op, c, known
}

// countResults sums the lengths of every "results" array in a GraphQL data
// payload — the rows a paged response actually carried, at any depth (the
// drawings query nests one page inside another). Used to reconcile the
// admission estimate with what the gateway charged.
func countResults(data json.RawMessage) int {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return 0
	}
	return countResultsIn(v)
}

func countResultsIn(v any) int {
	n := 0
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if arr, ok := child.([]any); ok && k == "results" {
				n += len(arr)
			}
			n += countResultsIn(child)
		}
	case []any:
		for _, child := range t {
			n += countResultsIn(child)
		}
	}
	return n
}
