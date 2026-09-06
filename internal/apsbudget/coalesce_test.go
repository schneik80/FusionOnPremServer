package apsbudget

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCache_CoalescesSameKey(t *testing.T) {
	c := NewCache[json.RawMessage](16, 0, RawSize, nil)
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(context.Context) (json.RawMessage, error) {
		calls.Add(1)
		<-release
		return json.RawMessage(`{"a":1}`), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.Do(context.Background(), "k", time.Second, fetch)
			if err != nil || string(v) != `{"a":1}` {
				t.Errorf("got %s %v", v, err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("fetch called %d times, want 1", calls.Load())
	}
	// Now a stored hit.
	if _, err := c.Do(context.Background(), "k", time.Second, func(context.Context) (json.RawMessage, error) {
		t.Fatal("must not fetch on a fresh hit")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	st := c.Stats()
	if st.Hits != 1 || st.Coalesced != 4 || st.Entries != 1 {
		t.Errorf("stats = %+v", st)
	}
}

func TestCache_DifferentSubjectsDoNotShare(t *testing.T) {
	c := NewCache[json.RawMessage](16, 0, RawSize, nil)
	var calls atomic.Int32
	fetch := func(context.Context) (json.RawMessage, error) { calls.Add(1); return json.RawMessage(`1`), nil }
	ka := Key("ep", "GetProjects", "user-a", map[string]any{"hubId": "h"})
	kb := Key("ep", "GetProjects", "user-b", map[string]any{"hubId": "h"})
	if ka == kb {
		t.Fatal("keys must differ by subject")
	}
	_, _ = c.Do(context.Background(), ka, time.Second, fetch)
	_, _ = c.Do(context.Background(), kb, time.Second, fetch)
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	// Shared op: no subject in the key → one fetch for both.
	ks := Key("ep", "HubDMID", "", map[string]any{"hubId": "h"})
	_, _ = c.Do(context.Background(), ks, time.Second, fetch)
	_, _ = c.Do(context.Background(), ks, time.Second, fetch)
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
	// Map order never splits a key.
	if Key("e", "o", "s", map[string]any{"a": "1", "b": "2"}) != Key("e", "o", "s", map[string]any{"b": "2", "a": "1"}) {
		t.Fatal("key depends on map order")
	}
}

func TestCache_TTLExpiryAndFresh(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewCache[json.RawMessage](16, 0, RawSize, func() time.Time { return now })
	var calls atomic.Int32
	fetch := func(context.Context) (json.RawMessage, error) { calls.Add(1); return json.RawMessage(`1`), nil }
	_, _ = c.Do(context.Background(), "k", 10*time.Second, fetch)
	_, _ = c.Do(context.Background(), "k", 10*time.Second, fetch)
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
	_, _ = c.Do(WithFresh(context.Background()), "k", 10*time.Second, fetch)
	if calls.Load() != 2 {
		t.Fatalf("fresh must bypass the stored hit; calls = %d", calls.Load())
	}
	now = now.Add(11 * time.Second)
	_, _ = c.Do(context.Background(), "k", 10*time.Second, fetch)
	if calls.Load() != 3 {
		t.Fatalf("expired entry must refetch; calls = %d", calls.Load())
	}
}

func TestCache_ErrorNotStored_LeaderCancelSurvives(t *testing.T) {
	c := NewCache[json.RawMessage](16, 0, RawSize, nil)
	var calls atomic.Int32
	boom := errors.New("boom")
	fail := func(context.Context) (json.RawMessage, error) { calls.Add(1); return nil, boom }
	if _, err := c.Do(context.Background(), "k", time.Second, fail); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), "k", time.Second, fail); !errors.Is(err, boom) || calls.Load() != 2 {
		t.Fatalf("error must not be cached; calls=%d err=%v", calls.Load(), err)
	}

	// Leader's ctx is cancelled mid-flight; the follower still gets data.
	lctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	fetch := func(ctx context.Context) (json.RawMessage, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
			return json.RawMessage(`"ok"`), nil
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = c.Do(lctx, "slow", time.Second, fetch) }()
	<-started
	cancel()
	v, err := c.Do(context.Background(), "slow", time.Second, fetch)
	wg.Wait()
	if err != nil || string(v) != `"ok"` {
		t.Fatalf("follower got %s %v", v, err)
	}
}

func TestCache_LRUAndInvalidate(t *testing.T) {
	c := NewCache[json.RawMessage](2, 0, RawSize, nil)
	fetch := func(v string) func(context.Context) (json.RawMessage, error) {
		return func(context.Context) (json.RawMessage, error) { return json.RawMessage(v), nil }
	}
	_, _ = c.Do(context.Background(), Key("e", "o", "u1", nil), time.Minute, fetch(`1`))
	_, _ = c.Do(context.Background(), Key("e", "p", "u1", nil), time.Minute, fetch(`2`))
	_, _ = c.Do(context.Background(), Key("e", "q", "u2", nil), time.Minute, fetch(`3`))
	if st := c.Stats(); st.Entries != 2 {
		t.Fatalf("entries = %d, want 2 after eviction", st.Entries)
	}
	if _, ok := c.get(Key("e", "o", "u1", nil)); ok {
		t.Fatal("oldest entry must have been evicted")
	}
	c.InvalidateSubject("u1")
	if _, ok := c.get(Key("e", "p", "u1", nil)); ok {
		t.Fatal("u1 entries must be gone")
	}
	if _, ok := c.get(Key("e", "q", "u2", nil)); !ok {
		t.Fatal("u2 entry must survive")
	}
}
