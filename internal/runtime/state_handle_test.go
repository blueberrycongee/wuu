package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// CLI state access must honor selection without model configuration. Retained
// requests must survive publication and become unavailable after final release.
func TestOpenStateSelectedProviderWithoutModel(t *testing.T) {
	for _, module := range []string{persistence.SQLiteModule, persistence.JSONLModule} {
		t.Run(module, func(t *testing.T) {
			modules := []string{sessionStateConsumer, module}
			cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}
			state, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: cfg})
			if err != nil {
				t.Fatal(err)
			}
			created, err := state.Sessions.CreateWithMetadata("captured", "/workspace")
			if err != nil {
				t.Fatal(err)
			}
			if got, ok, err := state.Sessions.Find(created.ID); err != nil || !ok || got.ID != created.ID {
				t.Fatalf("read selected state: %+v %v %v", got, ok, err)
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			if _, _, err := state.Sessions.Find(created.ID); err == nil {
				t.Fatal("closed state reopened an implicit backend")
			}
		})
	}
}

func TestOpenStateMissingSelectedProvider(t *testing.T) {
	modules := []string{sessionStateConsumer}
	_, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}})
	if err == nil {
		t.Fatal("missing provider used a default")
	}
}

func TestRetainedStateSurvivesGenerationPublication(t *testing.T) {
	ctx := context.Background()
	registry := pluginhost.NewServiceRegistry()
	owner := newRuntimeState(registry, "test")
	provider := persistence.NewService("example.state", t.TempDir(), "jsonl")
	host := pluginhost.New()
	host.Add(provider)
	host.Add(owner)
	host.AttachServiceRegistry(registry, registry.RegisterClients(provider, owner))
	owner.host = host
	if err := host.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	generation := &PluginGeneration{host: host, state: owner}
	generation.retain()
	runtime := &Session{PluginHost: host, Documents: owner.documents, Sessions: owner.sessions, pluginGeneration: generation}
	captured, err := runtime.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	generation.Release()
	host.StopBackground()
	if _, err := captured.Documents.Read(ctx, persistence.Query{Namespace: "test"}); err != nil {
		t.Fatal(err)
	}
	if captured.Host != host || captured.Generation != generation {
		t.Fatal("captured another generation")
	}
	if err := captured.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := captured.Documents.Read(ctx, persistence.Query{Namespace: "test"}); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("final release did not retire provider: %v", err)
	}
	if err := captured.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenStateAdmitsConfiguredNativeFactory(t *testing.T) {
	modules := []string{sessionStateConsumer, "example.documents"}
	home := t.TempDir()
	state, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: home, Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}, NativeProviders: []NativeProviderFactory{{ID: "example.documents", Build: func(input NativeProviderContext) (pluginhost.Client, error) {
		return persistence.NewService("example.documents", filepath.Join(input.WuuHome, "external-documents"), "jsonl"), nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := state.Sessions.Create("factory"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions", "documents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("implicit default backend: %v", err)
	}
}
