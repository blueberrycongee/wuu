package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func newCodeModeTestToolkit(t *testing.T) *Toolkit {
	t.Helper()
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The portable execution fixture opts out of OS confinement; sandbox tests cover confinement separately.
	kit.SetBoundary(UnconfinedBoundary())
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	service := codemode.NewService(codemode.ServiceConfig{})
	t.Cleanup(func() { _ = service.Close() })
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	return kit
}
func codeModeDefsToProviderDefs(defs []codemode.ToolDefinition) []providers.ToolDefinition {
	var out []providers.ToolDefinition
	for _, d := range defs {
		out = append(out, providers.ToolDefinition{Name: d.Name})
	}
	return out
}
func TestPTCGlobalSwitchAndExecutionBoundary(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.ConfigurePTC(kit.CodeModeService(), config.PTCConfig{Enabled: true})
	if !contains("run_code", kit.Definitions()) || contains("read_file", kit.Definitions()) {
		t.Fatal("PTC surface is not collapsed")
	}
	if _, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"README.md"}`}); err == nil {
		t.Fatal("model-direct leaf call bypassed PTC")
	}
	kit.ConfigurePTC(kit.CodeModeService(), config.PTCConfig{Enabled: false})
	kit.ConfigureSurfaceForProviderModel("anthropic", "claude-sonnet-4", true)
	if contains("run_code", kit.Definitions()) || !contains("read_file", kit.Definitions()) {
		t.Fatal("global opt-out ignored")
	}
	clone, err := kit.CloneForRoot(kit.RootDir())
	if err != nil {
		t.Fatal(err)
	}
	clone.ConfigureSurfaceForProviderModel("deepseek", "deepseek-v4", true)
	if contains("run_code", clone.Definitions()) || contains("run_code", kit.Definitions()) {
		t.Fatal("provider selection changed the global switch")
	}
}
func TestPTCNestedReadThroughRealNode(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	if err := os.WriteFile(filepath.Join(kit.RootDir(), "fixture.txt"), []byte("PTC_READ_OK"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	args, _ := json.Marshal(map[string]any{"code": `const result = await tools.read_file({path:"fixture.txt"}); console.log(result.content[0].text);`, "description": "Read directory"})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "outer", Name: "run_code", Arguments: string(args)}}, nil)
	if err != nil || len(messages) != 1 || !strings.Contains(messages[0].Content, "PTC_READ_OK") {
		t.Fatalf("nested execution: %+v %v", messages, err)
	}
}

func runPTCProgram(t *testing.T, kit *Toolkit, code string) toolresult.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	args, err := json.Marshal(map[string]any{"code": code, "description": "Exercise PTC execution"})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "program", Name: "run_code", Arguments: string(args)}}, nil)
	if err != nil || len(messages) != 1 || messages[0].ToolResult == nil {
		t.Fatalf("program execution: %+v %v", messages, err)
	}
	return *messages[0].ToolResult
}

func TestPTCFailureReportsNestedEffects(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.ConfigureSurfaceForProviderModel("anthropic", "claude-sonnet-4", true)
	kit.SetSessionID("failure-recovery")
	result := runPTCProgram(t, kit, `store("checkpoint", "before");`)
	if result.IsError {
		t.Fatal(result.TextProjection())
	}
	result = runPTCProgram(t, kit, `store("checkpoint", "after");
await tools.write_file({path:"effect.txt", content:"PRIVATE_EFFECT_MARKER"});
try { await tools.read_file({path:"missing.txt"}); } catch {}
throw new TypeError("bad value");`)
	text := result.TextProjection()
	t.Log(text)
	if !result.IsError {
		t.Fatalf("failed script reported success: %s", text)
	}
	for _, want := range []string{"TypeError: bad value", "1 succeeded", "1 failed", `"write_file": succeeded`, `"read_file": failed`, "not rolled back"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing recovery diagnostic %q in %s", want, text)
		}
	}
	if strings.Contains(text, "PRIVATE_EFFECT_MARKER") {
		t.Fatal("unselected tool content leaked into failure observation")
	}
	data, err := os.ReadFile(filepath.Join(kit.RootDir(), "effect.txt"))
	if err != nil || string(data) != "PRIVATE_EFFECT_MARKER" {
		t.Fatalf("completed write was lost: %q %v", data, err)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	result = runPTCProgram(t, kit, `return load("checkpoint");`)
	if result.IsError || result.TextProjection() != `before` {
		t.Fatalf("failed state committed or recovery diagnostic leaked into success: %+v", result)
	}
}

func TestPTCStateOwnerFollowsConversationLifetime(t *testing.T) {
	parent := newCodeModeTestToolkit(t)
	parent.SetSessionID("parent")
	parent.SetAgentIdentity("parent", "root")
	clone := func(sessionID, actorID, root, owner string) *Toolkit {
		t.Helper()
		kit, err := parent.CloneForRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		kit.SetSessionID(sessionID)
		kit.SetAgentIdentity(actorID, "root")
		if owner != "" {
			kit.SetCodeModeStateOwner(owner)
		}
		return kit
	}
	worker := clone("parent", "worker", t.TempDir(), "")
	oldSide := clone("old-side", "old-side", parent.RootDir(), "parent")
	newSide := clone("new-side", "new-side", parent.RootDir(), "parent")
	// Rebinding an inherited side-owner override to an independent session must
	// replace the owner, just as a new conversation/fork runtime does.
	fork, err := oldSide.CloneForRoot(parent.RootDir())
	if err != nil {
		t.Fatal(err)
	}
	fork.SetSessionID("fork")
	fork.SetAgentIdentity("fork", "root")
	for _, kit := range []*Toolkit{parent, worker, oldSide, newSide, fork} {
		if result := runPTCProgram(t, kit, `store("saved", 1);`); result.IsError {
			t.Fatalf("seed state: %+v", result)
		}
	}
	parent.CodeModeService().ForgetOwner("parent")
	for _, kit := range []*Toolkit{parent, worker, oldSide, newSide} {
		result := runPTCProgram(t, kit, `return typeof load("saved");`)
		if result.IsError || result.TextProjection() != `undefined` {
			t.Fatalf("deleted owner retained actor/root state: %+v", result)
		}
	}
	result := runPTCProgram(t, fork, `return load("saved");`)
	if result.IsError || result.TextProjection() != "1" {
		t.Fatalf("independent fork lost its state: %+v", result)
	}
}

func TestPTCProgramReviewBeforeNestedEffects(t *testing.T) {
	// Denial and missing-reviewer cases must stop before execution. An allow
	// must execute the program without exempting nested commands from review.
	for _, tc := range []struct {
		name, outcome, code string
		wantCalls           int
		wantError           bool
	}{
		{"denied", approvefor.OutcomeDeny, `await tools.bash({command: 'printf done > marker.txt'})`, 1, true},
		{"missing reviewer", "", `await tools.bash({command: 'printf done > marker.txt'})`, 0, true},
		{"allowed nested", approvefor.OutcomeAllow, `await tools.bash({command: 'printf done > marker.txt'})`, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.wantError && !processsandbox.Supported() {
				t.Skip("successful standard-mode execution requires a filesystem sandbox")
			}
			kit := newCodeModeTestToolkit(t)
			kit.SetBoundary(StandardBoundary())
			kit.SetPermissionMode("standard")
			kit.SetApproveForMe(true)
			reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: tc.outcome, Reason: "PTC review fixture"}}
			if tc.outcome != "" {
				kit.SetReviewer(reviewer)
			}
			result := runPTCProgram(t, kit, tc.code)
			if result.IsError != tc.wantError || len(reviewer.requests) != tc.wantCalls {
				t.Fatalf("review calls=%d result=%+v", len(reviewer.requests), result)
			}
			if tc.wantError {
				if !strings.Contains(result.TextProjection(), "blocked by approve for me") {
					t.Fatalf("program failed outside the review gate: %s", result.TextProjection())
				}
				if _, err := os.Stat(filepath.Join(kit.RootDir(), "marker.txt")); !os.IsNotExist(err) {
					t.Fatalf("unapproved program had filesystem effects: %v", err)
				}
				return
			}
			var reviewed struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(reviewer.requests[0].Arguments), &reviewed); err != nil {
				t.Fatal(err)
			}
			if reviewer.requests[0].Tool.Name != "run_code" || reviewed.Code != tc.code {
				t.Fatalf("reviewer did not receive the program: %+v", reviewer.requests[0])
			}
			data, err := os.ReadFile(filepath.Join(kit.RootDir(), "marker.txt"))
			if err != nil || string(data) != "done" {
				t.Fatalf("approved program did not complete: %q %v", data, err)
			}
		})
	}
}

func TestPTCResultOutputDoesNotRepeatOrQuoteToolViews(t *testing.T) {
	for _, code := range []string{
		`text(await tools.read_file({path:"evidence.txt"}));`,
		`console.log(await tools.read_file({path:"evidence.txt"}));`,
		`return await tools.read_file({path:"evidence.txt"});`,
	} {
		kit := newCodeModeTestToolkit(t)
		if err := os.WriteFile(filepath.Join(kit.RootDir(), "evidence.txt"), []byte("UNIQUE_PTC_EVIDENCE"), 0600); err != nil {
			t.Fatal(err)
		}
		result := runPTCProgram(t, kit, code)
		view := result.TextProjection()
		if result.IsError || strings.Count(view, "UNIQUE_PTC_EVIDENCE") != 1 {
			t.Fatalf("lost or repeated evidence: %+v", result)
		}
		var observation map[string]any
		if err := json.Unmarshal([]byte(view), &observation); err != nil {
			t.Fatalf("tool view acquired JSON string quoting: %q: %v", view, err)
		}
	}
}

func TestPTCReadOnlyProgramKeepsSandbox(t *testing.T) {
	if !processsandbox.Supported() {
		t.Skip("read-only execution requires a filesystem sandbox")
	}
	kit := newCodeModeTestToolkit(t)
	kit.SetBoundary(ReadOnlyBoundary())
	kit.SetPermissionMode("read_only")
	kit.SetApproveForMe(true)
	if err := os.WriteFile(filepath.Join(kit.RootDir(), "input.txt"), []byte("PTC_READ_ONLY"), 0600); err != nil {
		t.Fatal(err)
	}
	result := runPTCProgram(t, kit, `console.log((await tools.read_file({path:'input.txt'})).content[0].text);
await tools.bash({command:'printf forbidden > marker.txt'});`)
	if !result.IsError || !strings.Contains(result.TextProjection(), "PTC_READ_ONLY") {
		t.Fatalf("read-only program failed to read or was allowed to write: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(kit.RootDir(), "marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only program wrote a file: %v", err)
	}
}

func contains(name string, defs []providers.ToolDefinition) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

func TestPTCDiscoveryThenExactInvocation(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	if err := os.WriteFile(filepath.Join(kit.RootDir(), "fixture.txt"), []byte("DISCOVERED_READ_OK"), 0600); err != nil {
		t.Fatal(err)
	}
	result := runPTCProgram(t, kit, `const page = await searchTools('read_file');
const name = page.tools.find(t => t.name === 'read_file').name;
const detail = await describeTool(name);
if (!detail.input_schema.properties.path) throw new Error('missing schema');
return (await tools[name]({path:'fixture.txt'})).content[0].text;`)
	if result.IsError || !strings.Contains(result.TextProjection(), "DISCOVERED_READ_OK") {
		t.Fatalf("discovery and invocation failed: %+v", result)
	}
}

func TestPTCExplicitTimeoutValidation(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	for _, tc := range []struct {
		name      string
		timeout   int
		wantError bool
	}{
		{"zero", 0, true},
		{"negative", -1, true},
		{"over ten minutes", 601000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
			defer runtime.Cancel()
			args, _ := json.Marshal(map[string]any{"code": "return true;", "description": "Check explicit timeout", "timeout_ms": tc.timeout})
			messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "program", Name: "run_code", Arguments: string(args)}}, nil)
			if err != nil || len(messages) != 1 || messages[0].ToolResult == nil {
				t.Fatalf("program execution: %+v %v", messages, err)
			}
			result := messages[0].ToolResult
			if result.IsError != tc.wantError || (tc.wantError && !strings.Contains(result.TextProjection(), "timeout_ms")) {
				t.Fatalf("explicit timeout result=%+v", result)
			}
		})
	}
}

func TestPTCSurfaceSummaryKeepsNestedCapabilitiesReachable(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	surface := kit.ActiveSurface()
	if !surface.HasAvailableCapability(capability.CapabilityCommandBash) {
		t.Fatal("nested command capability lost")
	}
	for _, direct := range surface.VisibleCapabilities() {
		if direct == capability.CapabilityCommandBash {
			t.Fatal("nested command reported as direct")
		}
	}
	summary := surface.Summarize()
	if summary.NestedCapabilityMap["bash"] != string(capability.CapabilityCommandBash) || summary.EditPrimitive != "edit_file" {
		t.Fatalf("snapshot lost nested mapping: %+v", summary)
	}
	if _, ok := summary.ToolCapabilityMap["read_file"]; ok {
		t.Fatal("snapshot advertises direct read")
	}
}

func TestPTCStateFollowsConversationActorAndWorkspace(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetSessionID("conversation-a")
	kit.SetAgentIdentity("main", "/root")
	root := kit.RootDir()
	seed := runPTCProgram(t, kit, `store("checkpoint",{done:2});`)
	if seed.IsError {
		t.Fatal(seed.TextProjection())
	}
	check := func(k *Toolkit, code, want string) {
		t.Helper()
		result := runPTCProgram(t, k, code)
		if result.IsError || result.TextProjection() != want {
			t.Fatalf("state=%q error=%v want=%q", result.TextProjection(), result.IsError, want)
		}
	}
	clone, err := kit.CloneForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	check(clone, `return load("checkpoint");`, `{"done":2}`)
	clone.SetSessionID("conversation-b")
	check(clone, `return typeof load("checkpoint");`, `undefined`)
	clone.SetSessionID("conversation-a")
	clone.SetAgentIdentity("child", "/root/child")
	check(clone, `return typeof load("checkpoint");`, `undefined`)
	kit.SetRootDir(t.TempDir())
	check(kit, `return typeof load("checkpoint");`, `undefined`)
	kit.SetRootDir(root)
	kit.ConfigurePTC(kit.CodeModeService(), config.PTCConfig{Enabled: true})
	kit.ConfigurePTC(kit.CodeModeService(), config.PTCConfig{Enabled: false})
	kit.ConfigureSurfaceForProviderModel("anthropic", "claude-sonnet-4", true)
	if contains("run_code", kit.Definitions()) {
		t.Fatal("global opt-out ignored")
	}
	kit.ConfigurePTC(kit.CodeModeService(), config.PTCConfig{Enabled: true})
	kit.ConfigureSurfaceForProviderModel("deepseek", "deepseek-v4", true)
	check(kit, `return load("checkpoint");`, `{"done":2}`)
	if err := kit.CodeModeService().Close(); err != nil {
		t.Fatal(err)
	}
	fresh := codemode.NewService(codemode.ServiceConfig{})
	defer fresh.Close()
	kit.ConfigurePTC(fresh, config.PTCConfig{Enabled: true})
	check(kit, `return typeof load("checkpoint");`, `undefined`)
}

func TestPTCBackgroundProcessesSurviveProgramsAndReturnThroughTools(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetSessionID("background-conversation")
	manager, err := proc.NewManager(kit.RootDir(), filepath.Join(t.TempDir(), "processes"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.CleanupSession()
	kit.SetProcessManager(manager)
	events := make(chan proc.Event, 32)
	manager.Subscribe(events)
	defer manager.Unsubscribe(events)
	started := runPTCProgram(t, kit, `const ids=[]; for(const name of ["one","two"]) { const r=await tools.bash({command:"read line; echo DONE_$line",run_in_background:true}); ids.push(JSON.parse(r.content[0].text).id); } store("processes",ids); return ids;`)
	if started.IsError {
		t.Fatal(started.TextProjection())
	}
	var ids []string
	if err := json.Unmarshal([]byte(started.TextProjection()), &ids); err != nil || len(ids) != 2 {
		t.Fatalf("start=%q %v", started.TextProjection(), err)
	}
	for _, id := range ids {
		p, err := manager.Get(id)
		if err != nil || p.Status != proc.StatusRunning || p.RootThreadID != "background-conversation" {
			t.Fatalf("process lost owner/lifetime: %+v %v", p, err)
		}
	}
	written := runPTCProgram(t, kit, `await Promise.all(load("processes").map((id,i)=>tools.process({action:"write",process_id:id,input:String(i+1)+"\n"})));`)
	if written.IsError {
		t.Fatal(written.TextProjection())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := map[string]bool{}
	for len(finished) < 2 {
		select {
		case event := <-events:
			if event.Cause == proc.EventCauseNaturalExit {
				finished[event.Process.ID] = true
			}
		case <-ctx.Done():
			t.Fatal("managed processes never completed")
		}
	}
	read := runPTCProgram(t, kit, `const results=await Promise.all(load("processes").map(id=>tools.process({action:"read",process_id:id,wait_ms:30000}))); for(const r of results)console.log(r.content[0].text);`)
	if read.IsError || (!strings.Contains(read.TextProjection(), "DONE_1") || !strings.Contains(read.TextProjection(), "DONE_2")) {
		t.Fatalf("process observation=%+v", read)
	}
}

func TestPTCLocalCancelThenNextProgram(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetSessionID("same-session")
	if err := os.WriteFile(filepath.Join(kit.RootDir(), "ready.txt"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	ready := make(chan struct{})
	var once sync.Once
	runtime.SetResultCallback(func(call providers.ToolCall, result toolresult.Result) {
		if call.Name == "read_file" {
			once.Do(func() { close(ready) })
		}
	})
	args, _ := json.Marshal(map[string]any{"code": `await tools.read_file({path:"ready.txt"}); for(;;){}`, "description": "Wait for cancel"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "first", Name: "run_code", Arguments: string(args)}}, nil)
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("first program not ready")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("canceled call did not return")
	}
	result := runPTCProgram(t, kit, `return "next";`)
	if result.IsError {
		t.Fatal(result.TextProjection())
	}
}

func TestPTCStateUsesBoundExecutionWorkspace(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetSessionID("worktree-state")
	first, second := t.TempDir(), t.TempDir()
	run := func(root, code, want string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ctx = toolctx.WithWorktreeBinding(ctx, kit.RootDir(), root)
		runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx})
		defer runtime.Cancel()
		args, _ := json.Marshal(map[string]any{"code": code, "description": "Use workspace checkpoint"})
		messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "program", Name: "run_code", Arguments: string(args)}}, nil)
		if err != nil || len(messages) != 1 || messages[0].ToolResult == nil || messages[0].ToolResult.IsError || messages[0].Content != want {
			t.Fatalf("workspace state=%+v %v", messages, err)
		}
	}
	run(first, `store("checkpoint",1); return load("checkpoint");`, `1`)
	run(second, `return typeof load("checkpoint");`, `undefined`)
	run(first, `return load("checkpoint");`, `1`)
}

func TestPTCRepeatedProgramAdvancesCheckpoint(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetSessionID("stateful-program")
	for i := 1; i <= 4; i++ {
		result := runPTCProgram(t, kit, `store("page", (load("page") ?? 0) + 1); return load("page");`)
		if result.IsError || result.TextProjection() != fmt.Sprint(i) {
			t.Fatalf("checkpoint step %d: %+v", i, result)
		}
	}
}

func TestPTCRepeatedProgramKeepsLeafObservations(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	if err := os.WriteFile(filepath.Join(kit.RootDir(), "fixture.txt"), []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		result := runPTCProgram(t, kit, `await tools.read_file({path:"fixture.txt"}); return true;`)
		if result.IsError {
			t.Fatalf("read %d: %+v", i, result)
		}
	}
	var reads []ToolExecutionRecord
	for _, record := range kit.ToolTelemetry() {
		if record.Name == "read_file" {
			reads = append(reads, record)
		}
	}
	if len(reads) != 3 || !reads[2].Success || reads[2].ResultAction != "read_unchanged" {
		t.Fatalf("repeated leaf observation must retain its successful unchanged result: %+v", reads)
	}
	kit.DisableTools("read_file")
	if result := runPTCProgram(t, kit, `await tools.read_file({path:"fixture.txt"}); return true;`); !result.IsError {
		t.Fatalf("repetition must not bypass disabled leaf tools: %+v", result)
	}
}

func TestPTCRepeatedProgramAllowsLeafPolling(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	kit := newCodeModeTestToolkit(t)
	for i := 0; i < 4; i++ {
		result := runPTCProgram(t, kit, `await tools.process({action:"list"}); return true;`)
		if result.IsError {
			t.Fatalf("poll %d: %+v", i, result)
		}
	}
}

func TestPTCRepeatedReadObservesCurrentState(t *testing.T) {
	for _, scenario := range []string{"dirty tracked file", "retry missing ignored file", "intervening observations"} {
		t.Run(scenario, func(t *testing.T) {
			kit := newCodeModeTestToolkit(t)
			root := kit.RootDir()
			path := filepath.Join(root, "fixture.txt")
			mustWriteFile(t, path, "initial")
			if scenario == "retry missing ignored file" {
				mustWriteFile(t, filepath.Join(root, ".gitignore"), "fixture.txt\n")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			runGitFixture(t, root, "init", "-q")
			runGitFixture(t, root, "add", ".")
			runGitFixture(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
			const program = `const result = await tools.read_file({path:"fixture.txt"}); return result.content[0].text;`
			for attempt := 1; attempt <= 3; attempt++ {
				want := "initial"
				if scenario == "dirty tracked file" || (scenario == "retry missing ignored file" && attempt == 3) {
					want = fmt.Sprintf("version-%d", attempt)
					mustWriteFile(t, path, want)
				}
				result := runPTCProgram(t, kit, program)
				if scenario == "retry missing ignored file" && attempt < 3 {
					if !result.IsError {
						t.Fatalf("missing file attempt %d succeeded: %+v", attempt, result)
					}
					continue
				}
				if result.IsError || ((scenario != "intervening observations" || attempt == 1) && !strings.Contains(result.TextProjection(), want)) {
					t.Fatalf("attempt %d must read current content %q: %+v", attempt, want, result)
				}
				if scenario == "intervening observations" && attempt < 3 {
					other := runPTCProgram(t, kit, `await tools.list_files({}); return true;`)
					if other.IsError {
						t.Fatalf("intervening observation failed: %+v", other)
					}
				}
			}
		})
	}
}

func TestPTCVerificationRetryObservesChangedFile(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	root := kit.RootDir()
	kit.SetSessionDir(t.TempDir())
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "node_modules/\n")
	mustWriteFile(t, filepath.Join(root, "status.txt"), "initial")
	runGitFixture(t, root, "init", "-q")
	runGitFixture(t, root, "add", ".")
	runGitFixture(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
	runner := filepath.Join(root, "node_modules", ".bin", "vitest")
	mustWriteFile(t, runner, "#!/bin/sh\nif test \"$(cat status.txt)\" = green; then echo PASS; exit 0; else echo FAIL; exit 1; fi\n")
	if err := os.Chmod(runner, 0755); err != nil {
		t.Fatal(err)
	}
	const program = `const result = await tools.bash({command:"npx vitest --run"}); return JSON.parse(result.content[0].text).exit_code;`
	for attempt, state := range []string{"first-failure", "second-failure", "green"} {
		mustWriteFile(t, filepath.Join(root, "status.txt"), state)
		result := runPTCProgram(t, kit, program)
		if attempt < 2 {
			if result.IsError || result.TextProjection() != "1" {
				t.Fatalf("verification %d must return failing exit code: %+v", attempt+1, result)
			}
			continue
		}
		if result.IsError || result.TextProjection() != "0" {
			t.Fatalf("verification must execute after the repair: %+v", result)
		}
	}
}

type codeModeStatusProbe struct{ status string }

func (*codeModeStatusProbe) Name() string { return "mcp_test_job_status" }
func (p *codeModeStatusProbe) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: p.Name(), InputSchema: map[string]any{"type": "object"}}
}
func (p *codeModeStatusProbe) Execute(context.Context, string) (string, error) {
	return p.status, nil
}
func (*codeModeStatusProbe) IsReadOnly() bool        { return true }
func (*codeModeStatusProbe) IsConcurrencySafe() bool { return true }

func TestPTCRepeatedPollingObservesExternalState(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	probe := &codeModeStatusProbe{}
	kit.registry = NewRegistry(append(kit.registry.All(), probe)...)
	for _, status := range []string{"queued", "running", "completed"} {
		probe.status = status
		result := runPTCProgram(t, kit, `const result = await tools.mcp_test_job_status({}); return result.content[0].text;`)
		if result.IsError || result.TextProjection() != status {
			t.Fatalf("poll must observe external state %q: %+v", status, result)
		}
	}
}

func TestPTCEditingSurvivesModelSwitchAndClone(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	for _, model := range []string{"gpt-5-codex", "claude-sonnet-4-5", "portable-coder", "gpt-5.5"} {
		kit.ConfigureSurfaceForProviderModel("custom", model, true)
		clone, err := kit.CloneForRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []*Toolkit{kit, clone} {
			result := runPTCProgram(t, target, `
const created = await tools.write_file({path:"ptc.txt", content:"before"});
if (created.is_error) throw new Error(JSON.stringify(created));
const read = await tools.read_file({path:"ptc.txt"});
if (!read.content[0].text.includes("before")) throw new Error("read failed");
const edited = await tools.edit_file({path:"ptc.txt", old_text:"before", new_text:"after"});
if (edited.is_error) throw new Error(JSON.stringify(edited));
const checked = await tools.bash({command:"test $(cat ptc.txt) = after && printf PTC_EDIT_OK"});
console.log(checked.model_text ?? checked.content[0].text);
`)
			if result.IsError || !strings.Contains(result.TextProjection(), "PTC_EDIT_OK") {
				t.Fatalf("%s: %s", model, result.TextProjection())
			}
			if got := mustReadFile(t, filepath.Join(target.RootDir(), "ptc.txt")); got != "after" {
				t.Fatalf("%s edited content=%q", model, got)
			}
		}
	}
}
