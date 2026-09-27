package providers

import (
	"context"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
)

func TestBuildCatwalkIndex_SkipsEmptyAndZero(t *testing.T) {
	idx := buildCatwalkIndex([]catwalk.Provider{
		{
			Name: "test",
			Models: []catwalk.Model{
				{ID: "good-model", ContextWindow: 200_000},
				{ID: "", ContextWindow: 100_000},    // empty id
				{ID: "no-window", ContextWindow: 0}, // zero window
				{ID: "negative", ContextWindow: -1}, // negative
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

func TestSetCatwalkSync_InstallsRemoteData(t *testing.T) {
	// Save and restore the package singleton so we don't pollute
	// other tests in the same run.
	t.Cleanup(func() { SetCatwalkSync(nil) })

	customWindow := 1234567
	client := &fakeCatwalkClient{response: []catwalk.Provider{{
		Name: "test",
		Models: []catwalk.Model{
			{ID: "wuu-test-only-model", ContextWindow: int64(customWindow)},
		},
	}}}
	s := NewCatwalkSync(CatwalkSyncConfig{Client: client})

	SetCatwalkSync(s)
	got := catwalkLookup("wuu-test-only-model")
	// catwalkSanityCap is 3M, our test value is well under it.
	if got != customWindow {
		t.Fatalf("expected %d from injected sync, got %d", customWindow, got)
	}
	if client.calls != 1 {
		t.Fatalf("expected sync to fetch once, got %d calls", client.calls)
	}
}

func TestRefreshCatwalkIndex_RebuildsAfterRemoteUpdate(t *testing.T) {
	t.Cleanup(func() { SetCatwalkSync(nil) })

	client := &fakeCatwalkClient{response: []catwalk.Provider{{
		Name: "test",
		Models: []catwalk.Model{
			{ID: "wuu-refresh-test", ContextWindow: 100_000},
		},
	}}}
	s := NewCatwalkSync(CatwalkSyncConfig{Client: client})
	SetCatwalkSync(s)

	if got := catwalkLookup("wuu-refresh-test"); got != 100_000 {
		t.Fatalf("first lookup: got %d, want 100000", got)
	}

	// Server returns a fresher value on the next fetch.
	client.response = []catwalk.Provider{{
		Name: "test",
		Models: []catwalk.Model{
			{ID: "wuu-refresh-test", ContextWindow: 200_000},
		},
	}}
	if err := RefreshCatwalkIndex(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := catwalkLookup("wuu-refresh-test"); got != 200_000 {
		t.Fatalf("after refresh: got %d, want 200000", got)
	}
}
