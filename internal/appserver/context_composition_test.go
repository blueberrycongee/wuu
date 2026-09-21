package appserver

import "testing"

func TestLatestProviderRetainedContextTokensIgnoresLocalEstimate(t *testing.T) {
	s := &Server{threads: map[string]*threadState{}}
	thread := &threadState{ID: "s1", Turns: []Turn{
		{InputTokens: 10, OutputTokens: 2, ContextTokens: 12},
		{ContextTokens: 452352},
	}}
	s.threads["s1"] = thread

	if got := s.latestProviderRetainedContextTokens("s1"); got != 0 {
		t.Fatalf("local context marker = %d, want 0", got)
	}
	if got := s.latestRetainedContextTokens("s1"); got != 452352 {
		t.Fatalf("display retained context = %d, want the latest local marker", got)
	}

	thread.Turns[1].InputTokens = 400_000
	thread.Turns[1].CacheReadTokens = 50_000
	if got := s.latestProviderRetainedContextTokens("s1"); got != 452352 {
		t.Fatalf("provider-backed context = %d, want 452352", got)
	}
}
