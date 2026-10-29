package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/defaultpolicy"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// The independent fixture is installed from the public SDK by the shared
// external-composition acceptance runner. It replaces policy, not the loop,
// model transport, session admission, or tool consumers under examination.
func TestDefaultPolicyExternalComposition(t *testing.T) {
	fixture := os.Getenv("WUU_EXTERNAL_COMPOSITION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/external-composition-acceptance/run.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"stop", "error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var calls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				content, end := "external policy retained this output", "false"
				if n > 1 {
					content, end = "unexpected default continuation", "true"
				}
				item := fmt.Sprintf(`{"id":"msg_%d","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q}]}`, n, content)
				response := fmt.Sprintf(`{"id":"resp_%d","status":"completed","end_turn":%s,"output":[%s],"usage":{"input_tokens":7,"output_tokens":3}}`, n, end, item)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_%d\",\"output_index\":0,\"content_index\":0,\"delta\":%q}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", n, content, response)
			}))
			defer endpoint.Close()
			provider := testRuntimePlugin("independent.agent-policy")
			provider.Root, provider.Runtime.Command = fixture, node
			provider.Runtime.Args = []string{"policy.ts"}
			provider.Runtime.Env = map[string]string{"WUU_ACCEPTANCE_POLICY_MODE": mode}
			catalog := []pluginpkg.Plugin{provider}
			cfg := config.Default()
			disabledSkills := false
			cfg.Skills.Enabled = &disabledSkills
			cfg.DefaultProvider = "fixture"
			cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", WireAPI: "responses", StreamTransport: "http", BaseURL: endpoint.URL, APIKey: "synthetic", Model: "gpt-test"}}
			cfg.Agent.MaxSteps = 3
			cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{defaultpolicy.Module: false, provider.ID: true, credentialstore.OSModule: false, credentialstore.FileModule: true}, Services: []composition.Binding{{Service: pluginprotocol.AgentPolicyService, Major: 1, Provider: provider.ID}}}}}
			owner, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true, NoTools: true})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Cleanup()
			host := owner.pluginGeneration.host
			seen := map[string]bool{}
			for _, status := range host.Statuses() {
				seen[status.ID] = true
			}
			if seen[defaultpolicy.Module] || !seen[loopdriver.DefaultDriverID] || !seen[sessionExecutionConsumer] || !seen[toolExecutionConsumer] {
				t.Fatalf("replacement changed retained consumers or loaded the default provider: %v", seen)
			}
			run := func(id string) (string, error) {
				if _, err := owner.Sessions.Create(id); err != nil {
					return "", err
				}
				thread, err := owner.NewThreadRuntime(id)
				if err != nil {
					return "", err
				}
				defer owner.releasePluginGeneration(thread.PluginGeneration)
				result, err := thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Continue working until the selected policy stops you."}}, nil)
				return result.Content, err
			}
			content, runErr := run("selected-policy")
			if mode == "error" {
				if runErr == nil || !strings.Contains(runErr.Error(), "selected policy unavailable") || calls.Load() != 0 {
					t.Fatalf("failed policy fell back: content=%q requests=%d err=%v", content, calls.Load(), runErr)
				}
				return
			}
			if runErr != nil || content != "external policy retained this output" || calls.Load() != 1 {
				t.Fatalf("external continuation decision ignored: content=%q requests=%d err=%v", content, calls.Load(), runErr)
			}
			decision, err := owner.StreamRunner.SelectedPolicy.Evaluate(ctx, pluginprotocol.AgentPolicyInput{Phase: "schedule"})
			if err != nil || decision.Action != "serial" {
				t.Fatalf("selected scheduling decision=%+v err=%v", decision, err)
			}
			retired, found := host.RetirePlugin(ctx, provider.ID, errors.New("selected provider disabled"))
			if !found || retired.Err != nil {
				t.Fatalf("retire provider: found=%v err=%v", found, retired.Err)
			}
			if _, err := run("after-removal"); err == nil || calls.Load() != 1 {
				t.Fatalf("removed policy silently restored native behavior: requests=%d err=%v", calls.Load(), err)
			}
			t.Log("external public-SDK policy stopped a real HTTP continuation; native policy absent; loop/model/session/tool consumers retained; disabling provider stopped model admission")
		})
	}
}
