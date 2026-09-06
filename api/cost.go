package api

import (
	"time"

	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// OpCost is what the budget layer knows about one GraphQL operation, keyed by
// its operation name (every routine query declares one: `query GetHubs …`).
//
// Points follow the CALIBRATED model, not the documented one. The repo holds
// three real measurements for GetDrawingsForDesign (refs.go: 50×50 = 23066,
// 10×10 = 1026, 10×5 ≈ 510) and the documented "1 per object field × page"
// formula is off by 3–5×; a model where every selected field costs 1 per row
// it is evaluated on, plus 1 per row, and a nested page is charged again per
// parent row, reproduces all three within 0.01 %:
//
//	Estimate = 10×Roots + Fixed + (RowFields+1)×Limit
//
// Measured, when non-zero, is an exact value from GET /api/debug/cost-probe
// or a 429 message and wins over the estimate. Observations at runtime
// (extensions.pointValue, 429s) win over both — see apsbudget.Estimator.
type OpCost struct {
	Roots     int // root Query fields (10 each)
	Fixed     int // fields outside any results page
	RowFields int // fields per results row
	Limit     int // page size the query text hardcodes; 0 = not paginated
	Measured  int // exact cost, 0 = unmeasured

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

// Estimate is the calibrated static cost.
func (c OpCost) Estimate() int {
	return 10*c.Roots + c.Fixed + (c.RowFields+1)*c.Limit
}

// Points is the registry's best static answer.
func (c OpCost) Points() int {
	if c.Measured > 0 {
		return c.Measured
	}
	return c.Estimate()
}

const (
	ttlListing = 15 * time.Second
	ttlScope   = 30 * time.Second
	ttlProbe   = 5 * time.Second
)

// opCosts is the registry. A query missing here is charged formulaFallback
// and never cached — add it when you add the query.
var opCosts = map[string]OpCost{
	// Navigation (api/queries.go).
	"GetHubs":             {Roots: 1, Fixed: 1, RowFields: 5, Limit: 50, CacheTTL: ttlScope},
	"GetHubsNext":         {Roots: 1, Fixed: 1, RowFields: 5, Limit: 50, CacheTTL: ttlScope},
	"GetProjects":         {Roots: 1, Fixed: 2, RowFields: 7, Limit: 50, CacheTTL: ttlScope},
	"GetProjectsNext":     {Roots: 1, Fixed: 2, RowFields: 7, Limit: 50, CacheTTL: ttlScope},
	"GetFolders":          {Roots: 1, Fixed: 1, RowFields: 2, Limit: 50, CacheTTL: ttlListing},
	"GetFoldersNext":      {Roots: 1, Fixed: 1, RowFields: 2, Limit: 50, CacheTTL: ttlListing},
	"GetProjectItems":     {Roots: 1, Fixed: 1, RowFields: 6, Limit: 50, CacheTTL: ttlListing},
	"GetProjectItemsNext": {Roots: 1, Fixed: 1, RowFields: 6, Limit: 50, CacheTTL: ttlListing},
	"GetItems":            {Roots: 1, Fixed: 1, RowFields: 6, Limit: 50, CacheTTL: ttlListing},
	"GetItemsNext":        {Roots: 1, Fixed: 1, RowFields: 6, Limit: 50, CacheTTL: ttlListing},

	// Details / versions (api/details.go).
	"GetItemDetails":      {Roots: 2, Fixed: 30, RowFields: 11, Limit: 50, CacheTTL: ttlListing},
	"GetItemSummary":      {Roots: 1, Fixed: 27, CacheTTL: ttlListing},
	"GetItemVersionsNext": {Roots: 1, Fixed: 1, RowFields: 11, Limit: 50, CacheTTL: ttlListing},
	"DesignActivity":      {Roots: 2, Fixed: 12, RowFields: 11, Limit: 50, CacheTTL: ttlScope},
	"ChildActivity":       {Roots: 2, Fixed: 4, RowFields: 7, Limit: 20, CacheTTL: ttlScope},
	"ChildActivityNext":   {Roots: 1, Fixed: 1, RowFields: 7, Limit: 20, CacheTTL: ttlScope},

	// History (v3, api/history.go).
	"GetItemHistory":     {Roots: 1, Fixed: 2, RowFields: 7, Limit: 50, CacheTTL: ttlListing},
	"GetItemHistoryNext": {Roots: 1, Fixed: 2, RowFields: 7, Limit: 50, CacheTTL: ttlListing},

	// References (api/refs.go, api/bom.go).
	"GetOccurrences":           {Roots: 1, Fixed: 2, RowFields: 12, Limit: 50, CacheTTL: ttlListing},
	"GetOccurrencesNext":       {Roots: 1, Fixed: 2, RowFields: 12, Limit: 50, CacheTTL: ttlListing},
	"GetWhereUsed":             {Roots: 1, Fixed: 2, RowFields: 10, Limit: 50, CacheTTL: ttlListing},
	"GetWhereUsedNext":         {Roots: 1, Fixed: 2, RowFields: 10, Limit: 50, CacheTTL: ttlListing},
	"GetDrawingSource":         {Roots: 1, Fixed: 13, CacheTTL: ttlListing},
	"GetDrawingsForDesign":     {Roots: 1, Fixed: 4, RowFields: 8, Limit: 60, Measured: 510, CacheTTL: ttlListing},
	"GetDrawingsForDesignNext": {Roots: 1, Fixed: 4, RowFields: 8, Limit: 60, Measured: 510, CacheTTL: ttlListing},
	"AllOccurrences":           {Roots: 1, Fixed: 2, RowFields: 9, Limit: 50, CacheTTL: ttlListing},
	"AllOccurrencesNext":       {Roots: 1, Fixed: 2, RowFields: 9, Limit: 50, CacheTTL: ttlListing},

	// Locate (api/locate.go).
	"LocateItem":      {Roots: 1, Fixed: 26, CacheTTL: ttlListing}, // 8 nested parentFolder levels
	"GetFolderParent": {Roots: 1, Fixed: 3, CacheTTL: ttlListing},

	// Per-row probes (api/thumbnail.go, api/classify.go). Shared: a
	// thumbnail's status/URL and an assembly's shape carry no ACL dimension
	// beyond the hub lock requireHub already enforces, and the thumbnail
	// cache has always been process-wide.
	"GetThumbnail":         {Roots: 1, Fixed: 3, CacheTTL: ttlProbe, Shared: true},
	"ClassifyAndThumbnail": {Roots: 1, Fixed: 5, CacheTTL: ttlProbe, Shared: true},
	"ClassifyAssembly":     {Roots: 1, Fixed: 2, CacheTTL: ttlProbe, Shared: true},

	// Properties (api/properties.go, api/customprops.go).
	"GetPhysicalProperties": {Roots: 1, Fixed: 38, CacheTTL: ttlListing},
	"GetCustomProperties":   {Roots: 1, Fixed: 2, RowFields: 2, Limit: 50, CacheTTL: ttlListing},

	// Permissions (api/permissions.go).
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
	"HubDMID": {Roots: 1, Fixed: 2, CacheTTL: 12 * time.Hour, Shared: true},
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
