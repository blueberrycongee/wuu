package runtime

import (
	"context"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// New executions must use the selected generation's configuration and auth
// clients. Publishing a replacement must not retarget an already captured one.
func TestGenerationModelBindingsFollowSelectedConfiguration(t *testing.T) {
	ctx := context.Background()
	server := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"fixture","choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, label)
		}))
	}
	oldServer, nextServer := server("old"), server("new")
	defer oldServer.Close()
	defer nextServer.Close()
	modules := []string{sessionStateConsumer, persistence.JSONLModule, kernelHostModule, loopdriver.DefaultDriverID, sessionExecutionConsumer, toolExecutionConsumer, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule}
	settings := func(url string) config.Config {
		return config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}, DefaultProvider: "test", Providers: map[string]config.ProviderConfig{"test": {Type: "openai-compatible", BaseURL: url, APIKey: "fixture", Model: "fixture"}}}
	}
	rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: settings(oldServer.URL), NoTools: true, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	oldState, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer oldState.Close()
	oldClient := rt.StreamRunner.Client
	candidate, err := rt.PreflightExtensions(settings(nextServer.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	request := providers.ChatRequest{Model: "fixture", Messages: []providers.ChatMessage{{Role: "user", Content: "which generation"}}}
	previous, err := oldClient.Chat(ctx, request)
	if err != nil || previous.Content != "old" {
		t.Fatalf("old client: %+v %v", previous, err)
	}
	current, err := rt.StreamRunner.Client.Chat(ctx, request)
	if err != nil || current.Content != "new" {
		t.Fatalf("current client: %+v %v", current, err)
	}
}

func TestGenerationModelBindingsKeepSelectedCredentials(t *testing.T) {
	ctx := context.Background()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, r.Header.Get("Authorization"))
	}))
	defer endpoint.Close()
	modules := []string{sessionStateConsumer, persistence.JSONLModule, kernelHostModule, loopdriver.DefaultDriverID, sessionExecutionConsumer, toolExecutionConsumer, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule}
	cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}, DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: endpoint.URL, Model: "fixture"}}}
	builds := 0
	factory := NativeProviderFactory{ID: credentialstore.FileModule, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		builds++
		store := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
		if err := authstorage.NewBound(ctx, store).Set("fixture", authstorage.Credentials{APIKey: fmt.Sprintf("generation-%d", builds)}); err != nil {
			return nil, err
		}
		return credentialstore.NewService(credentialstore.FileModule, func() (credentialstore.Store, error) { return store, nil }), nil
	}}
	rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, NoTools: true, NonInteractive: true, NativeProviders: []NativeProviderFactory{factory}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	old, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	client := rt.StreamRunner.Client
	candidate, err := rt.PreflightExtensions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	request := providers.ChatRequest{Model: "fixture", Messages: []providers.ChatMessage{{Role: "user", Content: "authority"}}}
	for _, check := range []struct {
		client providers.Client
		want   string
	}{{client, "Bearer generation-1"}, {rt.StreamRunner.Client, "Bearer generation-2"}} {
		response, err := check.client.Chat(ctx, request)
		if err != nil || response.Content != check.want {
			t.Fatalf("credential authority: got %q, error %v; want %q", response.Content, err, check.want)
		}
	}
}

func TestOpenStateUsesSelectedNativeCredentialMigration(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	legacy, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Set("fixture", authstorage.Credentials{APIKey: "legacy-secret"}); err != nil {
		t.Fatal(err)
	}
	modules := []string{sessionStateConsumer, persistence.JSONLModule, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, ArtifactModule, DataQueryModule}
	state, err := OpenState(ctx, StateOptions{RootDir: t.TempDir(), HomeDir: home, Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	services, err := state.Services()
	if err != nil {
		t.Fatal(err)
	}
	entry, err := authstorage.NewBound(ctx, services.Credentials).Get("fixture")
	if err != nil || entry.APIKey != "legacy-secret" {
		t.Fatalf("native migration did not feed selected service: %v", err)
	}
	if services.ChatGPT == nil || services.GenerationID == "" {
		t.Fatal("OpenState lost its generation service bindings")
	}
}
