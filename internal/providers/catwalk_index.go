package providers

import (
	"strings"
	"sync"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

// catwalkIndex is a fast model-id → context-window lookup built from
// a snapshot of catwalk providers. Built once per snapshot and held
// behind an RWMutex so a future remote sync can swap it without
// taking writers off the critical path.
type catwalkIndex struct {
	// exact lowercase model id → context window
	exact map[string]int
	// list of (lowercase model id, window) pairs for substring fallback,
	// kept in insertion order so longer / more-specific ids that catwalk
	// happens to ship later can win deterministically.
	pairs []catwalkPair
}

type catwalkPair struct {
	id     string
	window int
}

// catwalkSanityCap is the upper bound we trust for any model's
// context window. Anything higher is treated as data corruption in
// catwalk and ignored — this filters out obvious aihubmix-style
// typos like "deepseek-v3 = 1638000" without us having to maintain a
// per-provider denylist.
//
// 3M is intentionally above the largest real-world window today
// (Gemini 2.5 Pro at 2M, Claude opus-4-6 at 1M, GPT-4.1 at 1M) but
// well below the absurd values aihubmix sometimes ships.
const catwalkSanityCap = 3_000_000

// buildCatwalkIndex flattens a slice of catwalk.Provider into a fast
// lookup. Empty slices are valid (yielding an empty index) so callers
// don't have to special-case missing data.
//
// Catwalk often ships the same model id under multiple providers
// with conflicting windows (e.g. one provider's "deepseek-v3" entry
// is 160k, another vendor's typo'd entry is 1.6M). We resolve those
// collisions by picking the MODE — the value the largest number of
// providers agree on. Ties are broken by insertion order, which means
// the first provider catwalk lists wins (charm.land's order is
// curated, so this is reasonable).
func buildCatwalkIndex(providers []catwalk.Provider) *catwalkIndex {
	// First pass: collect every observed window per (lowercased) id,
	// remembering insertion order so we can do tie-break later.
	type observation struct {
		windows []int
		first   int // first valid window seen (for ordering)
	}
	collected := make(map[string]*observation)
	order := make([]string, 0, 128)
	for _, p := range providers {
		for _, m := range p.Models {
			if m.ContextWindow <= 0 || int64(m.ContextWindow) > int64(catwalkSanityCap) {
				continue
			}
			id := strings.ToLower(strings.TrimSpace(m.ID))
			if id == "" {
				continue
			}
			obs, ok := collected[id]
			if !ok {
				obs = &observation{first: int(m.ContextWindow)}
				collected[id] = obs
				order = append(order, id)
			}
			obs.windows = append(obs.windows, int(m.ContextWindow))
		}
	}

	// Second pass: collapse each id to the modal window.
	idx := &catwalkIndex{
		exact: make(map[string]int, len(collected)),
		pairs: make([]catwalkPair, 0, len(collected)),
	}
	for _, id := range order {
		obs := collected[id]
		chosen := pickModeInt(obs.windows)
		idx.exact[id] = chosen
		idx.pairs = append(idx.pairs, catwalkPair{id: id, window: chosen})
	}
	return idx
}

// pickModeInt returns the most-common value in the slice. Ties are
// broken by first-seen order. For a single-element slice it returns
// that element directly.
func pickModeInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	if len(values) == 1 {
		return values[0]
	}
	counts := make(map[int]int, len(values))
	bestVal := values[0]
	bestCount := 0
	for _, v := range values {
		counts[v]++
		if counts[v] > bestCount {
			bestVal = v
			bestCount = counts[v]
		}
	}
	return bestVal
}

// lookup returns the catwalk-known context window for the given
// model name, or zero if no entry matches. Resolution order:
//
//  1. Exact match (lowercased, whitespace-trimmed).
//  2. Strip a single "vendor/" prefix and try exact match again
//     (handles OpenRouter-style namespaced ids).
//  3. Substring match in catwalk insertion order.
//  4. Reverse-substring (model id contains the input). Useful for
//     date-suffixed names like "claude-sonnet-4-5-20250929" that
//     don't appear verbatim in catwalk.
//
// Returns 0 on a miss so callers can fall through to the next layer
// in the resolution chain.
func (idx *catwalkIndex) lookup(model string) int {
	if idx == nil || model == "" {
		return 0
	}
	q := strings.ToLower(strings.TrimSpace(model))
	if q == "" {
		return 0
	}

	if w, ok := idx.exact[q]; ok {
		return w
	}
	if i := strings.LastIndex(q, "/"); i >= 0 {
		stripped := q[i+1:]
		if w, ok := idx.exact[stripped]; ok {
			return w
		}
	}
	// Substring direction A: catwalk id is contained in our query.
	// Walks pairs in insertion order so the first registered
	// matching id wins. Pick the LONGEST match to bias toward
	// specificity.
	bestLen := 0
	bestWin := 0
	for _, p := range idx.pairs {
		if len(p.id) <= bestLen {
			continue
		}
		if strings.Contains(q, p.id) {
			bestLen = len(p.id)
			bestWin = p.window
		}
	}
	if bestWin > 0 {
		return bestWin
	}
	// Substring direction B: our query is contained in a catwalk id.
	// Less common but catches cases where the user passes a short
	// alias and catwalk has the date-suffixed canonical form.
	for _, p := range idx.pairs {
		if strings.Contains(p.id, q) {
			return p.window
		}
	}
	return 0
}

// catwalkStore is the package-level holder for the active catwalk
// index. Initialized lazily from charm.land/catwalk's embedded
// snapshot on first lookup, so the very first ContextWindowFor call
// after process start always has data without any I/O.
//
// Future commits will add a remote sync path that can swap the
// stored index out atomically once a fresher snapshot is fetched.
var catwalkStore struct {
	mu    sync.RWMutex
	once  sync.Once
	index *catwalkIndex
}

// catwalkLookup returns the embedded catwalk window for model, or 0
// on a miss. Safe to call from any goroutine.
func catwalkLookup(model string) int {
	catwalkStore.once.Do(func() {
		catwalkStore.mu.Lock()
		defer catwalkStore.mu.Unlock()
		catwalkStore.index = buildCatwalkIndex(embedded.GetAll())
	})
	catwalkStore.mu.RLock()
	idx := catwalkStore.index
	catwalkStore.mu.RUnlock()
	return idx.lookup(model)
}
