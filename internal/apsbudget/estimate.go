package apsbudget

import (
	"regexp"
	"sort"
	"sync"
	"time"
)

// Estimator answers "how many points will this operation cost?" for the
// scheduler, and remembers what the gateway actually charged so the answer
// improves over time.
//
// Precedence: an exact observation (a 429 message names the rejected query's
// cost; the debug probe measures it; extensions.pointValue reports it) beats
// the value registered from the calibrated static model (api/cost.go).
type Estimator struct {
	mu         sync.Mutex
	registered map[string]int
	paged      map[string]bool
	observed   map[string]*Observation
}

// Observation is what the gateway told us about one operation's cost.
type Observation struct {
	Last, Max, N int
	Source       string // "429" | "probe" | "pointValue"
	At           time.Time
}

// OpCostRow is one line of the calibration table (GET /api/debug/quota-costs).
type OpCostRow struct {
	Op         string       `json:"op"`
	Registered int          `json:"registered"`
	Effective  int          `json:"effective"`
	Paged      bool         `json:"paged"`
	Observed   *Observation `json:"observed,omitempty"`
}

func NewEstimator() *Estimator {
	return &Estimator{registered: map[string]int{}, paged: map[string]bool{}, observed: map[string]*Observation{}}
}

// Register records the admission estimate for op. paged says the op's cost
// scales with the rows a page returns: an exact observation of one page is
// then recorded for the table but does not replace the full-page estimate
// (a short page would make the next long one under-admitted).
func (e *Estimator) Register(op string, points int, paged bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.registered[op] = points
	e.paged[op] = paged
}

// Points returns the effective estimate for op: for a fixed-cost op the last
// exact observation if there is one; else the registered value; else
// fallback.
func (e *Estimator) Points(op string, fallback int) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if o, ok := e.observed[op]; ok && o.Last > 0 && !e.paged[op] {
		return o.Last
	}
	if p, ok := e.registered[op]; ok && p > 0 {
		return p
	}
	return fallback
}

// Observe records an exact cost the gateway reported for op.
func (e *Estimator) Observe(op string, actual int, source string, now time.Time) {
	if op == "" || actual <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	o := e.observed[op]
	if o == nil {
		o = &Observation{}
		e.observed[op] = o
	}
	o.Last, o.Source, o.At = actual, source, now
	o.N++
	if actual > o.Max {
		o.Max = actual
	}
}

// Table lists every known op, sorted, for the calibration endpoint.
func (e *Estimator) Table() []OpCostRow {
	e.mu.Lock()
	defer e.mu.Unlock()
	names := map[string]struct{}{}
	for k := range e.registered {
		names[k] = struct{}{}
	}
	for k := range e.observed {
		names[k] = struct{}{}
	}
	rows := make([]OpCostRow, 0, len(names))
	for op := range names {
		row := OpCostRow{Op: op, Registered: e.registered[op], Paged: e.paged[op]}
		row.Effective = row.Registered
		if o := e.observed[op]; o != nil {
			cp := *o
			row.Observed = &cp
			if !e.paged[op] {
				row.Effective = o.Last
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Op < rows[j].Op })
	return rows
}

var opNameRe = regexp.MustCompile(`^\s*(?:query|mutation)\s+([A-Za-z_]\w*)`)

// OperationName extracts the operation name from a GraphQL document, or ""
// for an anonymous one. Only a fallback for raw strings the registry does not
// know (debug probes); routine queries are declared as ops.
func OperationName(query string) string {
	if m := opNameRe.FindStringSubmatch(query); m != nil {
		return m[1]
	}
	return ""
}
