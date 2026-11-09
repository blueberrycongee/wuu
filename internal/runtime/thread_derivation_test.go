package runtime

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
)

// DeriveThreadModel is the single source of truth for a conversation's derived
// model state. It must follow the selection: an empty selection resolves the
// workspace default, a pinned selection resolves that conversation's own
// provider, worker provider, and (smaller) budget — never the workspace's.
func TestDeriveThreadModelFollowsSelection(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "p0",
		Providers: map[string]config.ProviderConfig{
			"p0": {Type: "openai-compatible", BaseURL: "https://p0.test/v1", APIKey: "k", Model: "m0", ContextWindow: 400000},
			"p1": {Type: "openai-compatible", BaseURL: "https://p1.test/v1", APIKey: "k", Model: "m1", ContextWindow: 100000},
		},
	}
	s := &Session{ProviderName: "p0", Model: "m0"}

	base, err := s.DeriveThreadModel(cfg, ThreadModelSelection{})
	if err != nil {
		t.Fatalf("derive workspace default: %v", err)
	}
	if base.Provider != "p0" || base.WorkerProvider != "p0" {
		t.Fatalf("empty selection should resolve workspace default p0: %+v", base)
	}
	if base.WorkerClient == nil {
		t.Fatal("workspace derivation built no worker client")
	}

	pinned, err := s.DeriveThreadModel(cfg, ThreadModelSelection{Provider: "p1", Model: "m1"})
	if err != nil {
		t.Fatalf("derive pinned p1: %v", err)
	}
	if pinned.Provider != "p1" || pinned.WorkerProvider != "p1" {
		t.Fatalf("pinned selection should resolve p1 (its own worker role), got %+v", pinned)
	}
	if pinned.WorkerClient == nil {
		t.Fatal("pinned derivation built no worker client")
	}
	if pinned.ModelBudget.ContextWindowTokens <= 0 || pinned.ModelBudget.ContextWindowTokens >= base.ModelBudget.ContextWindowTokens {
		t.Fatalf("pinned p1 window (%d) should be smaller than workspace p0 window (%d)",
			pinned.ModelBudget.ContextWindowTokens, base.ModelBudget.ContextWindowTokens)
	}
	if pinned.WorkerModelBudget.ContextWindowTokens >= base.WorkerModelBudget.ContextWindowTokens {
		t.Fatalf("pinned worker window (%d) should be smaller than workspace worker window (%d)",
			pinned.WorkerModelBudget.ContextWindowTokens, base.WorkerModelBudget.ContextWindowTokens)
	}
}

// A selection pinning a provider that is not in config surfaces an error so
// callers can decide to fall back (advanced update skips the thread; the main
// path heals) rather than silently using workspace state.
func TestDeriveThreadModelUnknownProviderErrors(t *testing.T) {
	cfg := config.Config{
		DefaultProvider: "p0",
		Providers: map[string]config.ProviderConfig{
			"p0": {Type: "openai-compatible", BaseURL: "https://p0.test/v1", APIKey: "k", Model: "m0"},
		},
	}
	s := &Session{ProviderName: "p0", Model: "m0"}
	if _, err := s.DeriveThreadModel(cfg, ThreadModelSelection{Provider: "ghost", Model: "m"}); err == nil {
		t.Fatal("expected an error deriving an unknown provider")
	}
}
