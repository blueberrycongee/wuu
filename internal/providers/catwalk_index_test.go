package providers

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

func TestBuildCatwalkIndex_SkipsEmptyAndZero(t *testing.T) {
	idx := buildCatwalkIndex([]catwalk.Provider{
		{
			Name: "test",
			Models: []catwalk.Model{
				{ID: "good-model", ContextWindow: 200_000},
				{ID: "", ContextWindow: 100_000},        // empty id
				{ID: "no-window", ContextWindow: 0},     // zero window
				{ID: "negative", ContextWindow: -1},     // negative
			},
		},
	})
	if got := idx.lookup("good-model"); got != 200_000 {
		t.Fatalf("good-model: got %d, want 200000", got)
	}
	if got := idx.lookup("no-window"); got != 0 {
		t.Fatalf("no-window should be missing, got %d", got)
	}
	if got := idx.lookup("negative"); got != 0 {
		t.Fatalf("negative window should be skipped, got %d", got)
	}
}

func TestCatwalkIndex_LookupResolution(t *testing.T) {
	idx := buildCatwalkIndex([]catwalk.Provider{
		{
			Name: "test",
			Models: []catwalk.Model{
				{ID: "claude-sonnet-4-5", ContextWindow: 200_000},
				{ID: "claude-sonnet-4-6", ContextWindow: 1_000_000},
				{ID: "gpt-4o", ContextWindow: 128_000},
			},
		},
	})

	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"exact match", "claude-sonnet-4-5", 200_000},
		{"vendor prefix stripped", "anthropic/claude-sonnet-4-5", 200_000},
		{"case insensitive", "GPT-4O", 128_000},
		{"longest substring wins", "claude-sonnet-4-6-20251111", 1_000_000},
		{"unknown returns 0", "no-such-model", 0},
		{"empty returns 0", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := idx.lookup(tc.query)
			if got != tc.want {
				t.Fatalf("lookup(%q) = %d, want %d", tc.query, got, tc.want)
			}
		})
	}
}

func TestCatwalkIndex_NilSafe(t *testing.T) {
	var idx *catwalkIndex
	if got := idx.lookup("anything"); got != 0 {
		t.Fatalf("nil index lookup should return 0, got %d", got)
	}
}

func TestCatwalkLookup_PackageSingletonHasData(t *testing.T) {
	// Sanity check: the package-level singleton initialized from
	// catwalk's embedded snapshot should know about at least one
	// well-known canonical model id. Bound check is wide so a future
	// catwalk model rename doesn't break this test brittlely.
	got := catwalkLookup("gpt-4o")
	if got == 0 {
		t.Fatal("expected catwalk to know about gpt-4o, got 0")
	}
	if got < 50_000 || got > 500_000 {
		t.Fatalf("gpt-4o window out of plausible range: %d", got)
	}
}

func TestCatwalkLookup_HandlesEmptyEmbedded(t *testing.T) {
	// If catwalk's embedded data is unexpectedly empty (shouldn't
	// happen in practice, but defensive), buildCatwalkIndex must not
	// panic and lookup must return 0.
	idx := buildCatwalkIndex(nil)
	if got := idx.lookup("gpt-4o"); got != 0 {
		t.Fatalf("nil providers slice: got %d, want 0", got)
	}
	idx2 := buildCatwalkIndex([]catwalk.Provider{})
	if got := idx2.lookup("gpt-4o"); got != 0 {
		t.Fatalf("empty providers slice: got %d, want 0", got)
	}
}

// Sanity-check that we're actually pulling something non-trivial
// from the embedded snapshot — failing this would mean catwalk's
// embedded.GetAll() returned nothing, which usually means the
// dependency was removed accidentally.
func TestCatwalkEmbedded_HasProviders(t *testing.T) {
	providers := embedded.GetAll()
	if len(providers) == 0 {
		t.Fatal("expected charm.land/catwalk embedded providers to be non-empty")
	}
}
