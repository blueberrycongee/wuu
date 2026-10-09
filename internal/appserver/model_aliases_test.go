package appserver

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelroles"
)

func TestResolveSubagentModelAliasBuildsCompleteRuntime(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Temperature = 0.3
	rt.StreamRunner.CompactThresholdPct = 0.55
	rt.StreamRunner.CompactKeepRecentTokens = 12_000
	rt.StreamRunner.DisableAutoCompact = true
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://main.example.test/v1",
      "model": "main-model"
    },
    "ui": {
      "type": "openai-compatible",
      "base_url": "https://ui.example.test/v1",
      "api_key": "test-key",
      "model": "ui-default",
      "models": {
        "frontend-model": {
          "id": "frontend-api-model",
          "limit": {"context": 128000, "input": 96000, "output": 16000},
          "variants": {"high": {"reasoningEffort": "high"}}
        }
      }
    }
  },
  "agent": {
    "model_aliases": {
      "frontend": {
        "provider": "ui",
        "model": "frontend-model",
        "variant": "high"
      }
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	srv := New(rt, &lockedBuffer{})

	result := srv.resolveSubagentModelAlias("frontend")
	if result.Err != nil {
		t.Fatalf("resolve alias: %v", result.Err)
	}
	if !result.Found || result.Unknown {
		t.Fatalf("unexpected resolution flags: %+v", result)
	}
	resolved := result.Runtime
	if resolved.Provider != "ui" || resolved.Model != "frontend-model" || resolved.APIModel != "frontend-api-model" {
		t.Fatalf("unexpected resolved identity: %+v", resolved)
	}
	if resolved.Client == nil {
		t.Fatal("resolved alias did not build a provider client")
	}
	if resolved.ProviderOptions["reasoningEffort"] != "high" {
		t.Fatalf("provider options = %#v", resolved.ProviderOptions)
	}
	if resolved.Variant != "high" {
		t.Fatalf("resolved variant = %q, want high", resolved.Variant)
	}
	if resolved.ContextWindow == 0 || resolved.MaxInputTokens == 0 || resolved.OutputReserveTokens == 0 {
		t.Fatalf("resolved budget is incomplete: %+v", resolved)
	}
	if resolved.Temperature != 0.3 || resolved.CompactThresholdPct != 0.55 || resolved.CompactKeepRecentTokens != 12_000 || !resolved.DisableAutoCompact {
		t.Fatalf("runtime behavior was not snapshotted: %+v", resolved)
	}
}

func TestResolveSubagentModelAliasUnknownFallsBackWithSortedNames(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://main.example.test/v1",
      "model": "main-model"
    }
  },
  "agent": {
    "model_aliases": {
      " review ": {"provider": "main", "model": "review-model"},
      "cheap": {"provider": "main", "model": "cheap-model"}
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	srv := New(rt, &lockedBuffer{})

	result := srv.resolveSubagentModelAlias("missing")
	if result.Err != nil || result.Found || !result.Unknown {
		t.Fatalf("unexpected unknown resolution: %+v", result)
	}
	if !reflect.DeepEqual(result.ValidAliases, []string{"cheap", "review"}) {
		t.Fatalf("valid aliases = %#v", result.ValidAliases)
	}
}

func TestResolveSubagentModelAliasUsesVerificationCapabilityRole(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {
    "fake-provider": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "test-key",
      "model": "fake-model"
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg := config.Config{
		DefaultProvider: "fake-provider",
		Providers: map[string]config.ProviderConfig{
			"fake-provider": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "test-key", Model: "fake-model"},
		},
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{})
	if err != nil {
		t.Fatalf("resolve roles: %v", err)
	}
	rt.ModelRoles = roles
	srv := New(rt, &lockedBuffer{})

	result := srv.resolveSubagentModelAlias("@verification")
	if result.Err != nil || !result.Found || result.Unknown {
		t.Fatalf("unexpected verification resolution: %+v", result)
	}
	if result.Runtime.Provider != rt.ModelRoles.Verification.Provider || result.Runtime.Model != rt.ModelRoles.Verification.Model {
		t.Fatalf("verification runtime = %+v, role = %+v", result.Runtime, rt.ModelRoles.Verification)
	}
}

// The public worker capability must preserve its configured model and effort.
func TestResolveSubagentModelAliasUsesWorkerRole(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	cfg := config.Config{DefaultProvider: "fake-provider", Providers: map[string]config.ProviderConfig{"fake-provider": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "test-key", Model: "main-model"}}, Agent: config.AgentConfig{ModelRoles: config.ModelRolesConfig{Worker: config.ModelRoleConfig{Provider: "fake-provider", Model: "worker-model", Effort: "xhigh"}}}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rt.ModelRoles = roles
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	resolved := srv.resolveSubagentModelAlias("@worker")
	if resolved.Err != nil || !resolved.Found || resolved.Runtime.Model != "worker-model" || resolved.Runtime.Effort != "xhigh" {
		t.Fatalf("worker role = %+v", resolved)
	}
}
