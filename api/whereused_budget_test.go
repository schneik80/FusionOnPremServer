package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/schneik80/fusionlocalserver/internal/apsbudget"
)

// TestGetWhereUsed_ThroughBudgetAndCache runs the two-page where-used walk
// the way the server does — scheduler installed, subject on the context,
// coalescing on — from two callers at once, and checks both get the full,
// deduplicated parent list and that the pages were fetched once.
func TestGetWhereUsed_ThroughBudgetAndCache(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		op := apsbudget.OperationName(req.Query)
		mu.Lock()
		calls[op]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		row := func(cv, item string) string {
			return `{"id":"` + cv + `","name":"N-` + cv + `","partNumber":"","partDescription":"","materialName":"","designItemVersion":{"item":{"id":"` + item + `","name":"Item ` + item + `","fusionWebUrl":""}}}`
		}
		switch op {
		case "GetWhereUsed":
			_, _ = io.WriteString(w, `{"data":{"componentVersion":{"whereUsed":{"pagination":{"cursor":"c2"},"results":[`+row("cv1", "A")+`,`+row("cv2", "A")+`]}}}}`)
		case "GetWhereUsedNext":
			if c, _ := req.Variables["cursor"].(string); c != "c2" {
				t.Errorf("cursor = %v", req.Variables["cursor"])
			}
			_, _ = io.WriteString(w, `{"data":{"componentVersion":{"whereUsed":{"pagination":{"cursor":null},"results":[`+row("cv3", "B")+`]}}}}`)
		default:
			t.Errorf("unexpected op %q", op)
			http.Error(w, "unexpected", 400)
		}
	}))
	t.Cleanup(srv.Close)
	swapEndpoint(t, srv.URL)
	sched := apsbudget.New(apsbudget.DefaultConfig(), costs)
	t.Cleanup(sched.Close)
	t.Cleanup(SetBudgetForTesting(sched))

	ctx := apsbudget.WithPriority(apsbudget.WithSubject(context.Background(), "user-wu"), apsbudget.P0)
	var wg sync.WaitGroup
	results := make([][]ComponentRef, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = GetWhereUsed(ctx, "tok", "cv-focus-"+t.Name())
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		var items []string
		for _, r := range results[i] {
			items = append(items, r.DesignItemID)
		}
		if strings.Join(items, ",") != "A,B" {
			t.Errorf("caller %d: parents = %v, want A,B (deduped by design item)", i, items)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["GetWhereUsed"] != 1 || calls["GetWhereUsedNext"] != 1 {
		t.Errorf("upstream calls = %v, want one per page", calls)
	}
}
