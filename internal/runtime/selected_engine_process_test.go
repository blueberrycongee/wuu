package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Real SDK processes replace engine.wuu without native module privileges. The
// public ThreadBoundFactory contract preserves every input and callback.
func TestSelectedEngineProcess(t *testing.T) {
	fixture := os.Getenv("WUU_ENGINE_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/engine-provider-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"independent", "delegate"} {
		t.Run(mode, func(t *testing.T) { t.Parallel(); testSelectedEngineProcess(t, fixture, node, mode) })
	}
}
func testSelectedEngineProcess(t *testing.T, fixture, node, mode string) {
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		modelCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"external-engine-ok\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, home := t.TempDir(), t.TempDir()
	plugin := testRuntimePlugin("external-engine")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"engine.ts", mode}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL + "/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{"wuu.engines.native": false, plugin.ID: true}}}, Services: []composition.Binding{{Service: "engine.wuu", Major: 1, Provider: plugin.ID}}}}
	raw, _ := json.Marshal(cfg)
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, ConfigPath: path, ConfigLoadMode: ConfigLoadFile, DiscoveredPlugins: &plugins, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := owner.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := owner.Sessions.CreateWithMetadata("engine-thread", root); err != nil {
		t.Fatal(err)
	}
	factory, ok := owner.Engines().Lookup("wuu")
	if !ok {
		t.Fatal("replacement engine was not selected")
	}
	bound, ok := factory.(agentengine.ThreadBoundFactory)
	if !ok {
		t.Fatal("public thread binding unavailable")
	}
	effort, speed := "high", "fast"
	if mode == "delegate" {
		effort, speed = "", ""
	}
	settlementStarted, releaseSettlement := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseSettlement) }) }
	reference, turn := "", ""
	approved, asked := false, false
	session, err := bound.SessionForThread(ctx, agentengine.ThreadBinding{ThreadID: "engine-thread", RootDir: root, Model: "binding-model", Effort: effort, Speed: speed, ModelOptions: map[string]string{"detail": "full"}, PermissionMode: "default", Instructions: "binding-instructions", MCPServers: []agentengine.MCPServer{{Name: "fixture", URL: "http://127.0.0.1:1/mcp"}}, ExternalRef: "prior-reference", PersistRef: func(ref string) error { reference = ref; return nil }, PersistTurnRef: func(ref string, pending bool) error {
		if !pending {
			if ref == "canceled-confirmed" {
				close(settlementStarted)
				<-releaseSettlement
			}
			turn = ref
		}
		return nil
	}, RequestApproval: func(context.Context, agentengine.ApprovalRequest) (agentengine.ApprovalDecision, error) {
		approved = true
		return agentengine.ApprovalAccept, nil
	}, RequestUserInput: func(context.Context, agentengine.UserInputRequest) (map[string][]string, error) {
		asked = true
		return map[string][]string{"question": {"answer"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release(); _ = session.Close(context.Background()) }()
	var content string
	result, err := session.RunTurn(ctx, agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "fixture"}}}, func(event providers.StreamEvent) {
		if event.Type == providers.EventContentDelta {
			content += event.Content
		}
	})
	if err != nil || result.Result.Content != "external-engine-ok" || content != "external-engine-ok" {
		t.Fatalf("external run: result=%+v content=%q err=%v", result, content, err)
	}
	if reference != "confirmed-reference" || turn != "confirmed-turn" || !approved || !asked {
		t.Fatalf("lost callbacks: ref=%q turn=%q approval=%t input=%t", reference, turn, approved, asked)
	}
	expectedCalls := int32(0)
	if mode == "delegate" {
		expectedCalls = 1
	}
	if modelCalls.Load() != expectedCalls {
		t.Fatalf("model calls=%d want %d", modelCalls.Load(), expectedCalls)
	}
	entered := make(chan struct{})
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = session.RunTurn(ctx, agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "block"}}}, func(event providers.StreamEvent) {
			if event.Content == "blocking" {
				close(entered)
			}
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("blocking engine did not enter")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	_ = session.Close(canceled)
	finalClose := make(chan error, 1)
	go func() { finalClose <- session.Close(ctx) }()
	select {
	case <-settlementStarted:
	case err := <-finalClose:
		release()
		t.Fatalf("provider closed without late terminal settlement: %v", err)
	case <-ctx.Done():
		release()
		t.Fatal("late confirmed terminal receipt was rejected after cancellation")
	}
	select {
	case err := <-finalClose:
		release()
		t.Fatalf("close acknowledged before terminal persistence drained: %v", err)
	default:
	}
	release()
	if err := <-finalClose; err != nil {
		t.Fatalf("canceled first close leaked session: %v", err)
	}
	if turn != "canceled-confirmed" {
		t.Fatalf("late durable receipt lost: %q", turn)
	}
	select {
	case <-runDone:
	case <-ctx.Done():
		t.Fatal("run did not drain")
	}
	if _, err := session.RunTurn(ctx, agentengine.TurnInput{}, nil); err == nil {
		t.Fatal("closed engine admitted work")
	}
	t.Log("external SDK engine replaced native engine.wuu; full binding and callback authority preserved; canceled-first-close drained")
}
