package markdown

import (
	"container/list"
	"sync"
)

const (
	// highlightCacheCap bounds total cached entries. Each entry is one
	// (lang, code) → ANSI string pair; ANSI output is typically ~3× the
	// input code size. At 1024 entries × avg 2 KiB output ≈ 2 MiB.
	//
	// Known limitation: the cache only shrinks when a new Put evicts the
	// LRU tail. An idle long-running process keeps whatever peak size it
	// reached; there is no TTL or periodic compaction. Acceptable because
	// typical peak stays under ~10 MiB. If memory sensitivity changes,
	// switch to a byte-bounded LRU and add a time-based reaper.
	highlightCacheCap = 1024

	// highlightCacheMaxCode skips caching for code blocks larger than 64 KiB.
	// A single 1 MiB block would otherwise hog the cap and produce a ~3 MiB
	// cache entry with poor reuse probability. Such blocks fall through to
	// uncached chroma on every call, which means in a streaming context the
	// same huge block pays its full chroma cost every commit — accepted
	// tradeoff for rare inputs.
	highlightCacheMaxCode = 64 * 1024
)

// highlightKey uses the raw code string as part of the map key. Go's map
// hashes strings internally, so we avoid an explicit hash (and collision
// handling) at the cost of retaining the code bytes inside the key. Combined
// with highlightCacheMaxCode this stays bounded.
type highlightKey struct {
	lang string
	code string
}

type highlightEntry struct {
	key   highlightKey
	value string
}

// highlightLRU is a small mutex-guarded LRU. A concurrent double-miss on the
// same key is benign: both callers will run chroma, the last write wins, and
// chroma is deterministic so the cached value is identical either way. We
// accept the duplicated compute on races to keep the lock simple.
type highlightLRU struct {
	mu    sync.Mutex
	items map[highlightKey]*list.Element
	order *list.List
	cap   int
}

func newHighlightLRU(cap int) *highlightLRU {
	return &highlightLRU{
		items: make(map[highlightKey]*list.Element, cap),
		order: list.New(),
		cap:   cap,
	}
}

func (c *highlightLRU) Get(k highlightKey) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[k]; ok {
		c.order.MoveToFront(e)
		return e.Value.(*highlightEntry).value, true
	}
	return "", false
}

func (c *highlightLRU) Put(k highlightKey, v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[k]; ok {
		c.order.MoveToFront(e)
		e.Value.(*highlightEntry).value = v
		return
	}
	e := c.order.PushFront(&highlightEntry{key: k, value: v})
	c.items[k] = e
	if c.order.Len() > c.cap {
		back := c.order.Back()
		if back != nil {
			c.order.Remove(back)
			delete(c.items, back.Value.(*highlightEntry).key)
		}
	}
}

func (c *highlightLRU) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[highlightKey]*list.Element, c.cap)
	c.order.Init()
}

func (c *highlightLRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

var highlightCacheSingleton = newHighlightLRU(highlightCacheCap)

// ClearHighlightCache empties the process-global highlight cache. Intended
// for tests; callers in production code should not need this because the
// cache keys include the code content and chroma is deterministic.
//
// Test hygiene: when a test asserts entry counts or LRU ordering, always
// call ClearHighlightCache at the top. Other test files in this package
// may exercise HighlightCode without clearing and would otherwise leave
// residual entries affecting your counters.
func ClearHighlightCache() {
	highlightCacheSingleton.Clear()
}
