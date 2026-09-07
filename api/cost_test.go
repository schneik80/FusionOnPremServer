package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

func TestOpCost_ActualForRows(t *testing.T) {
	occ := opCosts["GetOccurrences"] // measured 916 at 50 rows, base 12
	if occ.ActualForRows(50) != 916 || occ.ActualForRows(0) != 12 {
		t.Errorf("full/empty page: %d / %d", occ.ActualForRows(50), occ.ActualForRows(0))
	}
	if got := occ.ActualForRows(16); got < 290 || got > 310 {
		t.Errorf("16 rows = %d, want ~301", got)
	}
	if occ.ActualForRows(500) != 916 {
		t.Error("rows above the page size clamp")
	}
	hubs := opCosts["GetHubs"] // charged on the requested page
	if hubs.ActualForRows(4) != 311 {
		t.Errorf("ByLimit op must not scale: %d", hubs.ActualForRows(4))
	}
	fixed := opCosts["GetThumbnail"]
	if fixed.ActualForRows(0) != 20 || fixed.ActualForRows(9) != 20 {
		t.Error("fixed op ignores rows")
	}
	for op, c := range opCosts {
		if c.Points() <= 0 || c.Points() >= 1000 {
			t.Errorf("%s: points = %d", op, c.Points())
		}
	}
}

func TestCountResults(t *testing.T) {
	data := json.RawMessage(`{"item":{"versions":{"pagination":{"cursor":null},"results":[{"n":1,"drawingItemVersions":{"results":[{},{}]}},{"n":2}]}},"other":{"results":[]}}`)
	if got := countResults(data); got != 4 {
		t.Errorf("countResults = %d, want 4", got)
	}
	if countResults(json.RawMessage(`null`)) != 0 || countResults(json.RawMessage(`{`)) != 0 {
		t.Error("degenerate payloads count 0")
	}
}

// TestGqlQuery_ReconcilesByRows: a 16-row occurrences page is admitted at
// the full-page estimate and refunded to what the gateway charges for 16
// rows once the response is in.
func TestGqlQuery_ReconcilesByRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rows := ""
		for i := 0; i < 16; i++ {
			if i > 0 {
				rows += ","
			}
			rows += `{"componentVersion":{"id":"cv"}}`
		}
		_, _ = io.WriteString(w, `{"data":{"componentVersion":{"occurrences":{"pagination":{"cursor":null},"results":[`+rows+`]}}}}`)
	}))
	t.Cleanup(srv.Close)
	swapEndpoint(t, srv.URL)
	cfg := apsbudget.DefaultConfig()
	sched := apsbudget.New(cfg, costs)
	t.Cleanup(sched.Close)
	t.Cleanup(SetBudgetForTesting(sched))

	if _, err := gqlQuery(context.Background(), "tok", "query GetOccurrences($cvId: ID!) { componentVersion { occurrences { results { componentVersion { id } } } } }", map[string]any{"cvId": "x"}); err != nil {
		t.Fatal(err)
	}
	snap := sched.Snapshot()
	want := opCosts["GetOccurrences"].ActualForRows(16)
	if snap.UsedLastMinute != want || snap.Available != int(cfg.Capacity)-want {
		t.Errorf("used = %d available = %d, want %d spent", snap.UsedLastMinute, snap.Available, want)
	}
}
