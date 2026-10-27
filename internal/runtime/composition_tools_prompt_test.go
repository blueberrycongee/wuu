package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// The providers implement the public service boundary; the real runtime loop
// must carry their prompt, tool surface and execution result to the transport.
type scopeToolsPromptProvider struct {
	*runtimeServiceConsumer
	label string
}

func (p *scopeToolsPromptProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return append(promptservice.NewService().ProvidedServices(), toolservice.Descriptor())
}
func (p *scopeToolsPromptProvider) InvokeService(_ context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var result any
	if input.Service == promptservice.ServiceName {
		result = promptservice.Result{Sections: []promptservice.Section{}, Content: "scope-prompt:" + p.label}
	} else {
		switch input.Method {
		case "list":
			result = toolservice.ListResult{Catalog: []tools.DeferredToolCatalogEntry{}, Tools: []toolservice.Definition{{Name: "echo_" + p.label, Description: "Scoped echo", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "resolve":
			var call toolservice.CallRequest
			if err := json.Unmarshal(input.Params, &call); err != nil {
				return nil, err
			}
			found := call.Call.Name == "echo_"+p.label
			result = toolservice.ResolveResult{Found: found, Supports: found, Metadata: &toolservice.Metadata{ReadOnly: true, ConcurrencySafe: true, Risk: "low"}}
		case "execute":
			var call toolservice.CallRequest
			if err := json.Unmarshal(input.Params, &call); err != nil {
				return nil, err
			}
			if call.Call.Name != "echo_"+p.label {
				return nil, fmt.Errorf("wrong executor %s for %s", p.label, call.Call.Name)
			}
			result = toolservice.ExecutionResult{Result: toolresult.FromText("scope-executor:" + p.label)}
		case "finalize":
			var req toolservice.FinalizeRequest
			if err := json.Unmarshal(input.Params, &req); err != nil {
				return nil, err
			}
			result = toolservice.ExecutionResult{Result: *req.Result}
		case "prepare":
			return json.RawMessage(`{"messages":[]}`), nil
		case "freeze", "unfreeze":
			result = struct{}{}
		default:
			return nil, fmt.Errorf("unexpected method %s", input.Method)
		}
	}
	return json.Marshal(result)
}

func TestCompositionConcurrentToolsPromptOverrides(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entered := make(chan string, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	seen := map[string]int{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			http.Error(w, "decode", 400)
			return
		}
		label, toolResult := "", ""
		for _, message := range req.Messages {
			var content string
			_ = json.Unmarshal(message.Content, &content)
			if message.Role == "system" {
				label = strings.TrimPrefix(content, "scope-prompt:")
			}
			if message.Role == "tool" {
				toolResult = content
			}
		}
		if label != "alpha" && label != "beta" {
			t.Errorf("unexpected selected prompt: %+v", req.Messages)
			http.Error(w, "prompt", 400)
			return
		}
		if len(req.Tools) != 1 || req.Tools[0].Function.Name != "echo_"+label {
			t.Errorf("scope %s tools leaked: %+v", label, req.Tools)
			http.Error(w, "tools", 400)
			return
		}
		mu.Lock()
		seen[label]++
		mu.Unlock()
		delta := map[string]any{"content": "completed:" + label}
		finish := "stop"
		if toolResult == "" {
			entered <- label
			select {
			case <-release:
			case <-ctx.Done():
				http.Error(w, "barrier", 408)
				return
			}
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_" + label, "type": "function", "function": map[string]any{"name": "echo_" + label, "arguments": "{}"}}}}
			finish = "tool_calls"
		} else if toolResult != "scope-executor:"+label {
			t.Errorf("scope %s received wrong executor result %q", label, toolResult)
			http.Error(w, "result", 400)
			return
		}
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
	}))
	defer endpoint.Close()
	cfg := config.Default()
	cfg.DefaultProvider = "synthetic"
	cfg.Providers = map[string]config.ProviderConfig{"synthetic": {Type: "openai-compatible", WireAPI: "chat", BaseURL: endpoint.URL, APIKey: "synthetic", Model: "gpt-4o"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless"}
	var factories []NativeProviderFactory
	for _, label := range []string{"alpha", "beta"} {
		factories = append(factories, NativeProviderFactory{ID: "fixture." + label, Build: func(NativeProviderContext) (pluginhost.Client, error) {
			return &scopeToolsPromptProvider{runtimeServiceConsumer: &runtimeServiceConsumer{id: "fixture." + label, state: pluginhost.StatePrepared}, label: label}, nil
		}})
	}
	catalog := []pluginpkg.Plugin{}
	owner, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NativeProviders: factories, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Cleanup()
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(owner.pluginGeneration, owner)
	defer kernel.compositions.close()
	invoke := func(method string, input any) (json.RawMessage, error) {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		return kernel.compositions.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "controller", Method: method, Params: raw})
	}
	ids := map[string]string{}
	for _, label := range []string{"alpha", "beta"} {
		raw, err := invoke("create", pluginhost.CompositionCreate{Inherit: true, Selection: composition.Config{Version: composition.Version, Patches: []composition.Patch{{Modules: map[string]bool{"fixture." + label: true}}}, Services: []composition.Binding{{Service: promptservice.ServiceName, Major: 1, Provider: "fixture." + label}, {Service: toolservice.ServiceName, Major: 1, Provider: "fixture." + label}}}})
		if err != nil {
			t.Fatal(err)
		}
		var scope pluginhost.CompositionView
		if err := json.Unmarshal(raw, &scope); err != nil {
			t.Fatal(err)
		}
		ids[label] = scope.ID
	}
	type outcome struct {
		label  string
		result pluginhost.CompositionRunResult
		err    error
	}
	done := make(chan outcome, 2)
	for label, id := range ids {
		go func() {
			raw, err := invoke("run", pluginhost.CompositionRun{ID: id, Prompt: "Run the available tool, then finish."})
			var result pluginhost.CompositionRunResult
			if err == nil {
				err = json.Unmarshal(raw, &result)
			}
			done <- outcome{label, result, err}
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("both scopes did not reach inference concurrently")
		}
	}
	close(release)
	for range 2 {
		select {
		case out := <-done:
			if out.err != nil || out.result.Error != "" || out.result.Content != "completed:"+out.label || out.result.ID != ids[out.label] {
				t.Fatalf("scope result: %+v", out)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["alpha"] != 2 || seen["beta"] != 2 {
		t.Fatalf("expected one tool step and one completion per scope: %v", seen)
	}
}
