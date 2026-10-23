package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	modules := []string{sessionStateConsumer, persistence.JSONLModule, kernelHostModule, loopdriver.DefaultDriverID, sessionExecutionConsumer, toolExecutionConsumer, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule}
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
