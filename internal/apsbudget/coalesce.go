package apsbudget

import (
	"container/list"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// Cache is the coalescing layer: a singleflight so N concurrent identical
// fetches become one upstream call, plus a short-TTL LRU so the near-
// concurrent ones (react-query double fires, a second tab, a 2 s poll) hit
// too. It is NOT a stale-while-revalidate cache — TTLs are seconds, chosen
// per operation in api/budget.go.
//
// Keys always carry the caller's subject unless the operation is registered
// Shared: two users of one hub see different projects, folders and members,
// and serving one user's bytes to another would be a data leak.
type Cache[V any] struct {
	maxEntries int
	maxBytes   int64
	now        func() time.Time
	size       func(V) int64

	mu    sync.Mutex
	lru   *list.List
	index map[string]*list.Element
	bytes int64
	sf    singleflight.Group

	hits, misses, coalesced atomic.Int64
}

type entry[V any] struct {
	key     string
	val     V
	bytes   int64
	expires time.Time
}

// CacheStats is the cache's contribution to the quota snapshot.
type CacheStats struct {
	Entries   int
	Bytes     int64
	Hits      int64
	Misses    int64
	Coalesced int64
}

// NewCache builds a cache bounded by entries and bytes. size reports an
// entry's weight (nil = 0, so only maxEntries applies). now may be nil.
func NewCache[V any](maxEntries int, maxBytes int64, size func(V) int64, now func() time.Time) *Cache[V] {
	if now == nil {
		now = time.Now
	}
	if size == nil {
		size = func(V) int64 { return 0 }
	}
	return &Cache[V]{maxEntries: maxEntries, maxBytes: maxBytes, now: now, size: size,
		lru: list.New(), index: map[string]*list.Element{}}
}

// RawSize is the size func for json.RawMessage values.
func RawSize(v json.RawMessage) int64 { return int64(len(v)) }

// Do returns the cached value for key when fresh, else runs fetch exactly once
// for all concurrent callers of key and stores the result for ttl. A ctx
// marked WithFresh skips a stored hit but still joins an in-flight fetch.
//
// The leader runs under a context detached from the caller's cancellation
// (values — priority, subject — are kept) with its own timeout, so a follower
// is not failed because the leader's tab navigated away. Each caller still
// gives up on its own ctx. Errors are never stored.
func (c *Cache[V]) Do(ctx context.Context, key string, ttl time.Duration, fetch func(context.Context) (V, error)) (V, error) {
	if ttl <= 0 || key == "" {
		return fetch(ctx)
	}
	if !FreshFrom(ctx) {
		if v, ok := c.get(key); ok {
			c.hits.Add(1)
			return v, nil
		}
	}
	c.misses.Add(1)
	leader := false
	ch := c.sf.DoChan(key, func() (any, error) {
		leader = true
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		v, err := fetch(lctx)
		if err == nil {
			c.put(key, v, ttl)
		}
		return v, err
	})
	select {
	case res := <-ch:
		if res.Shared && !leader {
			c.coalesced.Add(1)
		}
		if res.Err != nil {
			var zero V
			return zero, res.Err
		}
		return res.Val.(V), nil
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
}

func (c *Cache[V]) get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.index[key]
	if !ok {
		var zero V
		return zero, false
	}
	e := el.Value.(*entry[V])
	if !c.now().Before(e.expires) {
		c.removeLocked(el)
		var zero V
		return zero, false
	}
	c.lru.MoveToFront(el)
	return e.val, true
}

func (c *Cache[V]) put(key string, v V, ttl time.Duration) {
	n := c.size(v)
	if c.maxBytes > 0 && n > c.maxBytes/32 {
		return // a single huge value is returned but never stored
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		c.removeLocked(el)
	}
	e := &entry[V]{key: key, val: v, bytes: n, expires: c.now().Add(ttl)}
	c.index[key] = c.lru.PushFront(e)
	c.bytes += n
	for c.lru.Len() > 0 && ((c.maxEntries > 0 && c.lru.Len() > c.maxEntries) || (c.maxBytes > 0 && c.bytes > c.maxBytes)) {
		c.removeLocked(c.lru.Back())
	}
}

func (c *Cache[V]) removeLocked(el *list.Element) {
	e := el.Value.(*entry[V])
	delete(c.index, e.key)
	c.bytes -= e.bytes
	c.lru.Remove(el)
}

// InvalidateSubject drops every entry whose key was built for sub, so the
// author of a write sees it on their next fetch.
func (c *Cache[V]) InvalidateSubject(sub string) {
	if sub == "" {
		return
	}
	needle := "|" + sub + "|"
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.lru.Front(); el != nil; {
		next := el.Next()
		if strings.Contains(el.Value.(*entry[V]).key, needle) {
			c.removeLocked(el)
		}
		el = next
	}
}

// Stats reports counters for the quota snapshot.
func (c *Cache[V]) Stats() CacheStats {
	c.mu.Lock()
	n, b := c.lru.Len(), c.bytes
	c.mu.Unlock()
	return CacheStats{Entries: n, Bytes: b, Hits: c.hits.Load(), Misses: c.misses.Load(), Coalesced: c.coalesced.Load()}
}

// Key builds a coalescing key. subject is "" for a Shared op; vars are
// serialised with sorted keys so map order never splits a hit.
func Key(endpoint, op, subject string, vars map[string]any) string {
	var b strings.Builder
	b.WriteString(endpoint)
	b.WriteString("|")
	b.WriteString(op)
	b.WriteString("|")
	b.WriteString(subject)
	b.WriteString("|")
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		if s, ok := vars[k].(string); ok {
			b.WriteString(s)
		} else if raw, err := json.Marshal(vars[k]); err == nil {
			b.Write(raw)
		}
		b.WriteString(";")
	}
	return b.String()
}
