package markdown

import (
	"strings"
	"sync"
	"testing"
)

// TestHighlightCache_ParityHitVsMiss asserts that for a variety of inputs the
// cached value returned on a hit is byte-for-byte equal to the value that
// would be produced by running chroma uncached. This is the single most
// important invariant — if it ever breaks, snapshot tests and rendered
// output would drift invisibly.
func TestHighlightCache_ParityHitVsMiss(t *testing.T) {
	cases := []struct{ name, lang, code string }{
		{"go_small", "go", "func main() { println(\"hi\") }"},
		{"python_multiline", "python", "def foo():\n    return 1\n"},
		{"rust_simple", "rust", "fn main() { let x = 1; }"},
		{"empty_lang_passthrough", "", "no highlight"},
		{"empty_code", "go", ""},
		{"moderate_js", "javascript", strings.Repeat("let x = 1;\n", 50)},
		{"unknown_lang_falls_back", "not-a-real-lang-xyz", "something"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ClearHighlightCache()
			miss := HighlightCode(tc.code, tc.lang)
			// Second call: either cache hit, or re-run fallback for uncachable inputs.
			hit := HighlightCode(tc.code, tc.lang)
			if miss != hit {
				t.Fatalf("cache produced different output:\n miss=%q\n hit =%q", miss, hit)
			}
		})
	}
}

// TestHighlightCache_PopulatesOnMiss confirms the cache actually stores
// entries (and isn't secretly always bypassed).
func TestHighlightCache_PopulatesOnMiss(t *testing.T) {
	ClearHighlightCache()
	if n := highlightCacheSingleton.Len(); n != 0 {
		t.Fatalf("pre-test cache not empty: %d", n)
	}
	_ = HighlightCode("func f() {}", "go")
	if n := highlightCacheSingleton.Len(); n != 1 {
		t.Fatalf("expected 1 cache entry after 1 highlight, got %d", n)
	}
	_ = HighlightCode("func f() {}", "go")
	if n := highlightCacheSingleton.Len(); n != 1 {
		t.Fatalf("second call of same key must not add entry, got %d", n)
	}
}

// TestHighlightCache_EmptyLangNotCached — pass-through path must not occupy
// cache slots.
func TestHighlightCache_EmptyLangNotCached(t *testing.T) {
	ClearHighlightCache()
	_ = HighlightCode("some text", "")
	_ = HighlightCode("some text", "   ")
	if n := highlightCacheSingleton.Len(); n != 0 {
		t.Fatalf("empty lang path must not cache, got %d entries", n)
	}
}

// TestHighlightCache_LangCaseInsensitive — `Go` and `go` should collapse
// to one entry; chroma's lexer lookup is case-insensitive.
func TestHighlightCache_LangCaseInsensitive(t *testing.T) {
	ClearHighlightCache()
	code := "func f() {}"
	_ = HighlightCode(code, "Go")
	first := highlightCacheSingleton.Len()
	_ = HighlightCode(code, "go")
	second := highlightCacheSingleton.Len()
	if first != 1 || second != 1 {
		t.Fatalf("case-insensitive key expected single entry: first=%d second=%d", first, second)
	}
}

// TestHighlightCache_LangCaseProducesRealHighlight guards against the
// failure mode where "Go" lookup misses in chroma and silently degrades to
// the fallback lexer — our cache would then serve fallback output for the
// lowercased "go" key too. We assert both paths yield real ANSI output
// (contains an escape sequence) and are byte-equal.
func TestHighlightCache_LangCaseProducesRealHighlight(t *testing.T) {
	ClearHighlightCache()
	code := "func main() { println(42) }"
	upper := HighlightCode(code, "Go")
	lower := HighlightCode(code, "go")
	if upper != lower {
		t.Fatalf("Go and go must produce identical output; upper=%q lower=%q", upper, lower)
	}
	if !strings.Contains(upper, "\x1b[") {
		t.Fatalf("expected ANSI escape in highlighted output; got %q", upper)
	}
	// Also verify neither result equals the raw (unhighlighted) source — if
	// chroma silently returned the fallback, we'd see the code back untouched.
	if upper == code {
		t.Fatalf("output equals raw code; chroma likely returned fallback instead of Go lexer")
	}
}

// TestHighlightCache_SkipsOversizeCode — very large blocks should bypass the
// cache entirely to bound memory.
func TestHighlightCache_SkipsOversizeCode(t *testing.T) {
	ClearHighlightCache()
	big := strings.Repeat("x = 1\n", highlightCacheMaxCode) // well above limit
	_ = HighlightCode(big, "python")
	if n := highlightCacheSingleton.Len(); n != 0 {
		t.Fatalf("oversize code must not cache, got %d entries (code len=%d)", n, len(big))
	}
}

// TestHighlightCache_BoundaryExactlyAtLimit — a block exactly at the limit
// is allowed in; one byte over is skipped. Guards against off-by-one.
func TestHighlightCache_BoundaryExactlyAtLimit(t *testing.T) {
	ClearHighlightCache()
	atLimit := strings.Repeat("x", highlightCacheMaxCode)
	overLimit := strings.Repeat("x", highlightCacheMaxCode+1)
	_ = HighlightCode(atLimit, "text")
	if n := highlightCacheSingleton.Len(); n != 1 {
		t.Fatalf("code at limit should cache: got %d entries", n)
	}
	_ = HighlightCode(overLimit, "text")
	if n := highlightCacheSingleton.Len(); n != 1 {
		t.Fatalf("code above limit must not cache: got %d entries (expected 1)", n)
	}
}

// TestHighlightLRU_Evicts verifies oldest entries are evicted at capacity.
func TestHighlightLRU_Evicts(t *testing.T) {
	lru := newHighlightLRU(3)
	lru.Put(highlightKey{lang: "a", code: "1"}, "va")
	lru.Put(highlightKey{lang: "b", code: "2"}, "vb")
	lru.Put(highlightKey{lang: "c", code: "3"}, "vc")
	lru.Put(highlightKey{lang: "d", code: "4"}, "vd") // evicts a
	if _, ok := lru.Get(highlightKey{lang: "a", code: "1"}); ok {
		t.Fatal("oldest entry (a) should have been evicted")
	}
	if v, ok := lru.Get(highlightKey{lang: "d", code: "4"}); !ok || v != "vd" {
		t.Fatalf("newest entry (d) should be present: ok=%v v=%q", ok, v)
	}
}

// TestHighlightLRU_MoveToFrontOnGet — recently accessed entries are kept.
func TestHighlightLRU_MoveToFrontOnGet(t *testing.T) {
	lru := newHighlightLRU(2)
	a := highlightKey{lang: "a", code: "1"}
	b := highlightKey{lang: "b", code: "2"}
	c := highlightKey{lang: "c", code: "3"}
	lru.Put(a, "va")
	lru.Put(b, "vb")
	_, _ = lru.Get(a) // a becomes MRU; b is now LRU
	lru.Put(c, "vc")  // should evict b, not a
	if _, ok := lru.Get(a); !ok {
		t.Fatal("a was recently accessed; should not have been evicted")
	}
	if _, ok := lru.Get(b); ok {
		t.Fatal("b should have been evicted (was LRU)")
	}
}

// TestHighlightLRU_UpdateExistingKey replacing an existing key's value does
// not grow the cache nor change eviction order incorrectly.
func TestHighlightLRU_UpdateExistingKey(t *testing.T) {
	lru := newHighlightLRU(2)
	k := highlightKey{lang: "go", code: "x"}
	lru.Put(k, "v1")
	lru.Put(k, "v2") // overwrite, not grow
	if lru.Len() != 1 {
		t.Fatalf("overwrite should not grow cache, got %d", lru.Len())
	}
	if v, _ := lru.Get(k); v != "v2" {
		t.Fatalf("overwrite value wrong, got %q want v2", v)
	}
}

// TestHighlightCache_ConcurrentSafe — no data race, and a shared hot key
// collapses to at most one entry even under concurrent misses.
func TestHighlightCache_ConcurrentSafe(t *testing.T) {
	ClearHighlightCache()
	code := "fn x() { 42 }"
	const goroutines = 32
	const iters = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = HighlightCode(code, "rust")
			}
		}()
	}
	wg.Wait()
	if n := highlightCacheSingleton.Len(); n != 1 {
		t.Fatalf("expected exactly 1 entry for shared hot key under concurrency, got %d", n)
	}
}

// TestHighlightCache_LangSpecificity — same code, different langs must keep
// separate entries and distinct outputs.
func TestHighlightCache_LangSpecificity(t *testing.T) {
	ClearHighlightCache()
	code := "x = 1\n"
	goOut := HighlightCode(code, "go")
	pyOut := HighlightCode(code, "python")
	if n := highlightCacheSingleton.Len(); n != 2 {
		t.Fatalf("expected 2 entries for 2 langs, got %d", n)
	}
	// Chroma output differs by language; we don't assert a specific byte
	// pattern (library version could drift) but the two should differ.
	if goOut == pyOut {
		t.Fatalf("different langs produced identical highlight — likely a cache key bug")
	}
}

// BenchmarkHighlightCode_Miss sets a baseline for the cost of a single
// uncached chroma run.
func BenchmarkHighlightCode_Miss(b *testing.B) {
	code := "func example() int { return 42 }\n"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ClearHighlightCache()
		_ = HighlightCode(code, "go")
	}
}

// BenchmarkHighlightCode_Hit measures the hot path where the cache is
// primed. This is the speedup the streaming markdown renderer will see
// on every re-commit of an already-seen code block.
func BenchmarkHighlightCode_Hit(b *testing.B) {
	code := "func example() int { return 42 }\n"
	ClearHighlightCache()
	_ = HighlightCode(code, "go") // prime
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = HighlightCode(code, "go")
	}
}
