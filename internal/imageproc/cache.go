package imageproc

import (
	"container/list"
	"crypto/sha1"
	"sync"
)

// defaultCacheSize matches Codex's BlockingLruCache default of 32 entries.
// The cap is on entry count, not bytes: each result can be several hundred
// KB after resize, so the working-set ceiling is in the low tens of MB.
const defaultCacheSize = 32

// cacheKey identifies a cached encoding operation. Encoding is deterministic
// given the input bytes and Mode, so sha1 of the input combined with the
// mode is sufficient to memoize a *Result across calls.
type cacheKey struct {
	digest [sha1.Size]byte
	mode   Mode
}

// resultCache is a thread-safe LRU keyed by (sha1(input), Mode). It uses
// container/list for LRU ordering and a map for O(1) lookup, mirroring the
// shape of Codex's BlockingLruCache without the blocking semantics (Go
// callers mutate only under the mutex, so we don't need per-key
// notification).
//
// Callers must treat the cached *Result as read-only: the same pointer is
// handed out on every hit, so mutating Bytes or the dimensions fields
// would corrupt the cache.
type resultCache struct {
	mu      sync.Mutex
	maxSize int
	ll      *list.List
	items   map[cacheKey]*list.Element
}

type cacheEntry struct {
	key    cacheKey
	result *Result
}

var sharedCache = newResultCache(defaultCacheSize)

func newResultCache(maxSize int) *resultCache {
	if maxSize <= 0 {
		maxSize = defaultCacheSize
	}
	return &resultCache{
		maxSize: maxSize,
		ll:      list.New(),
		items:   make(map[cacheKey]*list.Element, maxSize),
	}
}

func (c *resultCache) get(key cacheKey) (*Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*cacheEntry).result, true
}

func (c *resultCache) put(key cacheKey, result *Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*cacheEntry).result = result
		return
	}
	el := c.ll.PushFront(&cacheEntry{key: key, result: result})
	c.items[key] = el
	for c.ll.Len() > c.maxSize {
		back := c.ll.Back()
		if back == nil {
			return
		}
		c.ll.Remove(back)
		delete(c.items, back.Value.(*cacheEntry).key)
	}
}

// resetCache replaces the shared cache with a fresh instance. Exported for
// tests; not used in production.
func resetCache() {
	sharedCache = newResultCache(defaultCacheSize)
}
