package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

type contextIndexConsumer struct{}

func (*contextIndexConsumer) ID() string { return runtime.StateServicesConsumer }
func (c *contextIndexConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (*contextIndexConsumer) Close(context.Context) error                      { return nil }
func (*contextIndexConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*contextIndexConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: pluginhost.KernelDataQueryService, MajorVersion: 2, Required: true}}
}

// The product summary must use selected process events even when a native
// trace exists. Closing that process must fail instead of reading the trace.
func TestContextCompositionUsesSelectedIndexProcess(t *testing.T) {
	if path := os.Getenv("WUU_CONTEXT_INDEX_FIXTURE"); path != "" {
		runContextIndexFixture(path)
		return
	}
	ctx := context.Background()
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = t.TempDir()
	if _, err := rt.Sessions.CreateWithMetadata("thread", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	native := sessiontrace.Path(statepath.SessionArtifactDir(rt.StateDir, "thread"))
	if err := sessiontrace.AppendTurn(native, sessiontrace.TurnRecord{ThreadID: "thread", TurnID: "native", Model: "native-model"}, sessiontrace.FinalRecord{}, nil, nil, []sessiontrace.RequestContextRecord{{InputTokens: 999, MessageBytes: 100}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := sessiontrace.TurnEvents(sessiontrace.TurnRecord{ThreadID: "thread", TurnID: "external", Model: "external-model"}, sessiontrace.FinalRecord{}, nil, nil, []sessiontrace.RequestContextRecord{{InputTokens: 37, OutputTokens: 4, MessageBytes: 100}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]pluginhost.DataEvent, len(raw))
	for i, event := range raw {
		events[i] = pluginhost.DataEvent(event)
	}
	payload, err := json.Marshal(pluginhost.DataQueryResult{Version: "2.0.0", ThreadID: "thread", Events: events})
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "response.json")
	if err := os.WriteFile(fixture, payload, 0600); err != nil {
		t.Fatal(err)
	}
	process, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: "external.context-index", Command: os.Args[0], Args: []string{"-test.run=^TestContextCompositionUsesSelectedIndexProcess$"}, Env: map[string]string{"WUU_CONTEXT_INDEX_FIXTURE": fixture}, PluginRoot: t.TempDir(), ProjectRoot: rt.RootDir, WuuHome: t.TempDir(), Timeout: 5 * time.Second, PrepareOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	consumer := &contextIndexConsumer{}
	registry, conflicts := pluginhost.BuildServiceRegistry(process, consumer)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host := pluginhost.New(process, consumer)
	host.AttachServiceRegistry(registry, conflicts)
	defer host.Close(ctx)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	rt.PluginHost = host
	out := &lockedBuffer{}
	srv := New(rt, out)
	request := func(id string) map[string]any {
		t.Helper()
		if err := srv.handleThreadContextComposition(Request{ID: json.RawMessage(`"` + id + `"`), Params: json.RawMessage(`{"thread_id":"thread"}`)}); err != nil {
			t.Fatal(err)
		}
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	response := request("selected")
	if response["error"] != nil {
		t.Fatalf("context: %+v", response)
	}
	result := remarshal[ThreadContextCompositionResult](t, response["result"])
	if !result.Available || result.TurnID != "external" || result.Model != "external-model" || result.PromptTokens != 37 || result.TracePath != "" {
		t.Fatalf("context did not use selected process: %+v", result)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if response := request("closed"); response["error"] == nil {
		t.Fatalf("closed index fell back to native trace: %+v", response)
	}
	preserved, err := sessiontrace.ReplayTrace(native)
	if err != nil || preserved.LatestTurn.TurnID != "native" {
		t.Fatalf("external selection touched legacy trace: %v", err)
	}
}

func runContextIndexFixture(path string) {
	payload, err := os.ReadFile(path)
	if err != nil {
		os.Exit(2)
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(3)
		}
		var result any = struct{}{}
		switch request.Method {
		case "initialize":
			result = pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion, ProvidedServices: []pluginhost.ServiceDescriptor{{Name: "host.data.query", Version: "2.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "call", InputSchema: "host.data.query.input.v2", OutputSchema: "host.data.query.output.v2"}, {Name: "append-turn", InputSchema: "host.data.query.append-turn.input.v2", OutputSchema: "host.data.query.append-turn.output.v2"}, {Name: "latest-context", InputSchema: "host.data.query.latest-context.input.v2", OutputSchema: "host.data.query.latest-context.output.v2"}}}}}
		case "service.invoke":
			var params pluginhost.ServiceInvokeParams
			if json.Unmarshal(request.Params, &params) != nil || params.Service != "host.data.query" || params.Method != "latest-context" {
				os.Exit(4)
			}
			result = json.RawMessage(payload)
		case "prepare", "activate", "service.changed", "execution.cancel", "shutdown":
		default:
			os.Exit(5)
		}
		if encoder.Encode(map[string]any{"id": request.ID, "result": result}) != nil {
			os.Exit(6)
		}
		if request.Method == "shutdown" {
			return
		}
	}
}
