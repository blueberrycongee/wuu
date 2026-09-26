package externalengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func testEngine(t *testing.T, scenario string) *Engine {
	t.Helper()
	return testEngineID(t, "test", scenario)
}

func testEngineID(t *testing.T, id, scenario string) *Engine {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "Test"
	if entry, ok := enginecatalog.Lookup(id); ok {
		name = entry.Name
	}
	return New(enginecatalog.Entry{ID: id, Name: name, Protocol: "acp", Args: []string{"-test.run=^TestACPHelper$", "--", "wuu-acp-helper", scenario}}, binary, t.TempDir())
}

func testBinding() agentengine.ThreadBinding {
	return agentengine.ThreadBinding{PermissionMode: "workspace", ThreadID: "host-thread"}
}
func testInput() agentengine.TurnInput {
	return agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "Please inspect the project"}}}
}

func TestACPStreamsAndResumesWithoutReplayingHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	engine := testEngine(t, "stream")
	binding := testBinding()
	var ref string
	binding.PersistRef = func(value string) error { ref = value; return nil }
	session, err := engine.SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	var events []providers.StreamEvent
	result, err := session.RunTurn(ctx, testInput(), func(event providers.StreamEvent) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	if ref != "native-session" || result.Result.Content != "Hello world" || len(result.Result.NewMessages) != 1 {
		t.Fatalf("ref=%q result=%+v", ref, result)
	}
	if result.Result.NewMessages[0].ReasoningContent != "Thinking" {
		t.Fatalf("missing reasoning: %+v", result)
	}
	var starts, ends, done int
	for _, event := range events {
		switch event.Type {
		case providers.EventToolUseStart:
			starts++
		case providers.EventToolUseEnd:
			ends++
			if event.ToolResult != "file contents" {
				t.Fatalf("result=%q", event.ToolResult)
			}
		case providers.EventDone:
			done++
		}
	}
	if starts != 1 || ends != 1 || done != 1 {
		t.Fatalf("starts=%d ends=%d done=%d", starts, ends, done)
	}
	binding.ExternalRef = ref
	session, err = engine.SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err = session.RunTurn(ctx, testInput(), nil)
	if err != nil || result.Result.Content != "Hello world" {
		t.Fatalf("replay leaked or resume failed: %+v %v", result, err)
	}
}

func TestACPFailureIsNeverSuccessfulCompletion(t *testing.T) {
	// load-error and no-load are absent on purpose: an agent that cannot resume
	// the saved session continues in a fresh one with a notice (see
	// TestACPUnresumableSessionStartsFreshAndSaysSo). A stream that desyncs
	// (load-malformed) stays a failure — the fallback cannot trust it.
	for _, scenario := range []string{"eof", "malformed", "version", "missing-stop", "limit", "load-malformed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			binding := testBinding()
			if scenario == "load-malformed" {
				binding.ExternalRef = "old-session"
			}
			input := testInput()
			session, err := testEngine(t, scenario).SessionForThread(ctx, binding)
			if err != nil {
				t.Fatal(err)
			}
			done := false
			result, err := session.RunTurn(ctx, input, func(event providers.StreamEvent) {
				if event.Type == providers.EventDone {
					done = true
				}
			})
			if err == nil || done || result.Result.FinishReason != providers.FinishReasonError {
				t.Fatalf("false success: %+v %v done=%v", result, err, done)
			}
		})
	}
}

func TestACPIgnoresStaleRPCResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := testEngine(t, "stale-id").SessionForThread(ctx, testBinding())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, testInput(), nil)
	if err != nil || result.Result.Content != "Hello world" {
		t.Fatalf("stale RPC result aborted the turn: %+v %v", result, err)
	}
}

func TestACPUnadvertisedImagesArePassedAsFilePaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binding := testBinding()
	binding.ThreadID = t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(acpImageDir(binding.ThreadID)) })
	input := testInput()
	input.History[0].Images = []providers.InputImage{{MediaType: "image/png", Data: "aGVsbG8="}}
	session, err := testEngine(t, "echo-prompt").SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	var prompt []map[string]string
	if err := json.Unmarshal([]byte(result.Result.Content), &prompt); err != nil {
		t.Fatalf("prompt %q: %v", result.Result.Content, err)
	}
	if len(prompt) != 1 || prompt[0]["type"] != "text" {
		t.Fatalf("prompt = %+v", prompt)
	}
	text := prompt[0]["text"]
	if !strings.Contains(text, "Please inspect the project") {
		t.Fatalf("user text missing: %q", text)
	}
	path := acpImagePathFromPrompt(t, text)
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("file %s = %q %v", path, got, err)
	}
}

func TestACPUnadvertisedImageOnlyPromptStillIncludesAPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binding := testBinding()
	binding.ThreadID = t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(acpImageDir(binding.ThreadID)) })
	input := testInput()
	input.History[0].Content = ""
	input.History[0].Images = []providers.InputImage{{MediaType: "image/png", Data: "aGVsbG8="}}
	session, err := testEngine(t, "echo-prompt").SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	var prompt []map[string]string
	if err := json.Unmarshal([]byte(result.Result.Content), &prompt); err != nil {
		t.Fatalf("prompt %q: %v", result.Result.Content, err)
	}
	if len(prompt) != 1 || prompt[0]["type"] != "text" {
		t.Fatalf("prompt = %+v", prompt)
	}
	path := acpImagePathFromPrompt(t, prompt[0]["text"])
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("file %s = %q %v", path, got, err)
	}
}

func TestACPAdvertisedImageCapabilityStillUsesFilePaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binding := testBinding()
	binding.ThreadID = t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(acpImageDir(binding.ThreadID)) })
	input := testInput()
	input.History[0].Images = []providers.InputImage{{MediaType: "image/png", Data: "aGVsbG8="}}
	session, err := testEngine(t, "echo-prompt-images").SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	var prompt []map[string]string
	if err := json.Unmarshal([]byte(result.Result.Content), &prompt); err != nil {
		t.Fatalf("prompt %q: %v", result.Result.Content, err)
	}
	if len(prompt) != 1 || prompt[0]["type"] != "text" {
		t.Fatalf("prompt = %+v", prompt)
	}
	path := acpImagePathFromPrompt(t, prompt[0]["text"])
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("file %s = %q %v", path, got, err)
	}
}

func TestACPInvalidImageDataFailsTheTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	input := testInput()
	input.History[0].Images = []providers.InputImage{{MediaType: "image/png", Data: "not-base64"}}
	session, err := testEngine(t, "echo-prompt").SessionForThread(ctx, testBinding())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.RunTurn(ctx, input, nil); err == nil {
		t.Fatal("invalid image data succeeded")
	}
}

func acpImagePathFromPrompt(t *testing.T, text string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		path, ok := strings.CutPrefix(line, "- ")
		if !ok || !strings.Contains(path, string(os.PathSeparator)) {
			continue
		}
		return path
	}
	t.Fatalf("no image path in %q", text)
	return ""
}

func TestACPUnresumableSessionStartsFreshAndSaysSo(t *testing.T) {
	for _, scenario := range []string{"load-error", "no-load"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			binding := testBinding()
			binding.ExternalRef = "old-session"
			var persisted string
			binding.PersistRef = func(value string) error { persisted = value; return nil }
			session, err := testEngine(t, scenario).SessionForThread(ctx, binding)
			if err != nil {
				t.Fatal(err)
			}
			done := false
			result, err := session.RunTurn(ctx, testInput(), func(event providers.StreamEvent) {
				if event.Type == providers.EventDone {
					done = true
				}
			})
			if err != nil || !done {
				t.Fatalf("turn did not recover: %+v %v done=%v", result, err, done)
			}
			content := result.Result.Content
			if !strings.Contains(content, "could not resume") || !strings.HasSuffix(content, "Hello world") {
				t.Fatalf("fallback was not disclosed ahead of the answer: %q", content)
			}
			// The unreachable reference has to be replaced, or every later turn
			// repeats the fallback.
			if persisted != "native-session" {
				t.Fatalf("persisted ref = %q", persisted)
			}
		})
	}
}

func TestACPFailureQuotesEngineStderrWithoutSecrets(t *testing.T) {
	previous := acpSessionTimeout
	acpSessionTimeout = 300 * time.Millisecond
	t.Cleanup(func() { acpSessionTimeout = previous })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := testEngine(t, "stderr-stall").SessionForThread(ctx, testBinding())
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.RunTurn(ctx, testInput(), nil)
	if err == nil {
		t.Fatal("wedged session reported success")
	}
	message := err.Error()
	if !strings.Contains(message, "provider not configured") {
		t.Fatalf("engine stderr missing from the failure: %v", err)
	}
	for _, secret := range []string{"SECRET-CODE-123456", "sk-abcdefghijklmnopqrstuvwxyz0123456789ABCDEF"} {
		if strings.Contains(message, secret) {
			t.Fatalf("credential reached the transcript: %v", err)
		}
	}
	if !strings.Contains(message, "<redacted>") {
		t.Fatalf("redaction did not run: %v", err)
	}
}

func TestACPUnconfinedSelectsAgentUnattendedMode(t *testing.T) {
	for _, testCase := range []struct {
		mode string
		want string
	}{
		{mode: "unconfined", want: "mode=bypass"},
		// Standard maps onto the prompting choice, not accept-edits.
		{mode: "standard", want: "mode=ask"},
		// Unknown host values leave the agent's own policy alone rather
		// than guessing a stricter one it may not implement.
		{mode: "workspace", want: "mode="},
	} {
		t.Run(testCase.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			binding := testBinding()
			binding.PermissionMode = testCase.mode
			session, err := testEngine(t, "unattended-mode").SessionForThread(ctx, binding)
			if err != nil {
				t.Fatal(err)
			}
			result, err := session.RunTurn(ctx, testInput(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Result.Content != testCase.want {
				t.Fatalf("agent mode = %q, want %q", result.Result.Content, testCase.want)
			}
		})
	}
}

func TestACPHostPermissionModesMapOntoAdvertisedChoices(t *testing.T) {
	for _, testCase := range []struct {
		mode string
		want string
	}{
		{mode: "standard", want: "mode=agent"},
		{mode: "read_only", want: "mode=read-only"},
		{mode: "unconfined", want: "mode=agent-full-access"},
	} {
		t.Run(testCase.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			binding := testBinding()
			binding.PermissionMode = testCase.mode
			session, err := testEngine(t, "codex-mode").SessionForThread(ctx, binding)
			if err != nil {
				t.Fatal(err)
			}
			result, err := session.RunTurn(ctx, testInput(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Result.Content != testCase.want {
				t.Fatalf("agent mode = %q, want %q", result.Result.Content, testCase.want)
			}
		})
	}
}

func TestACPReadOnlyFailsWhenAgentHasNoDistinctReadOnlyMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binding := testBinding()
	binding.PermissionMode = "read_only"
	session, err := testEngine(t, "unattended-mode").SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.RunTurn(ctx, testInput(), nil)
	if err == nil || !strings.Contains(err.Error(), "does not enforce Wuu's read-only boundary") {
		t.Fatalf("read_only error = %v", err)
	}
}

func TestACPStderrTailKeepsRecentLinesOnly(t *testing.T) {
	var tail stderrTail
	for i := 0; i < stderrTailLines+4; i++ {
		if _, err := tail.Write([]byte(fmt.Sprintf("line-%d\n", i))); err != nil {
			t.Fatal(err)
		}
	}
	// A partial line must not be reported until it is terminated.
	if _, err := tail.Write([]byte("half")); err != nil {
		t.Fatal(err)
	}
	summary := tail.Summary()
	if strings.Contains(summary, "line-3") || strings.Contains(summary, "half") {
		t.Fatalf("tail kept too much: %q", summary)
	}
	if !strings.HasSuffix(summary, fmt.Sprintf("line-%d", stderrTailLines+3)) {
		t.Fatalf("tail dropped the newest line: %q", summary)
	}
	if lines := strings.Count(summary, "\n") + 1; lines != stderrTailLines {
		t.Fatalf("lines = %d, want %d", lines, stderrTailLines)
	}
}

func TestACPPermissionsFailClosedAndDoNotPersistGrants(t *testing.T) {
	for _, decision := range []agentengine.ApprovalDecision{agentengine.ApprovalAccept, agentengine.ApprovalAcceptForSession, agentengine.ApprovalDecline, agentengine.ApprovalCancel, "no-handler"} {
		t.Run(string(decision), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			binding := testBinding()
			if decision != "no-handler" {
				binding.RequestApproval = func(_ context.Context, req agentengine.ApprovalRequest) (agentengine.ApprovalDecision, error) {
					if req.EngineID != "test" || req.Kind != agentengine.ApprovalCommandExecution || req.ItemID != "exec-1" {
						t.Errorf("bad approval: %+v", req)
					}
					return decision, nil
				}
			}
			session, err := testEngine(t, "permission").SessionForThread(ctx, binding)
			if err != nil {
				t.Fatal(err)
			}
			result, err := session.RunTurn(ctx, testInput(), nil)
			if err != nil {
				t.Fatal(err)
			}
			want := "deny"
			if decision == agentengine.ApprovalAccept || decision == agentengine.ApprovalAcceptForSession {
				want = "once"
			}
			if decision == agentengine.ApprovalCancel {
				want = "cancelled"
			}
			if result.Result.Content != want {
				t.Fatalf("got %q want %q", result.Result.Content, want)
			}
		})
	}
}

func TestACPRefPersistenceFailureStopsBeforePrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binding := testBinding()
	binding.PersistRef = func(string) error { return errors.New("disk unavailable") }
	session, err := testEngine(t, "stream").SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, testInput(), nil)
	if err == nil || !strings.Contains(err.Error(), "disk unavailable") || result.Result.Content != "" {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestACPCancelAndCloseStopQuietAgent(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		t.Run(fmt.Sprint(closeSession), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			session, err := testEngine(t, "quiet").SessionForThread(ctx, testBinding())
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				_, err := session.RunTurn(ctx, testInput(), func(event providers.StreamEvent) {
					if event.Type == providers.EventContentDelta {
						close(started)
					}
				})
				finished <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if closeSession {
				err = session.Close(ctx)
			} else {
				err = session.Interrupt(ctx, "user")
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("cancellation hung")
			}
		})
	}
}

// TestACPHelper is an isolated fake process, not a model connection. Requests
// drive it deterministically; it never reads user settings or credentials.
func TestACPHelper(t *testing.T) {
	args := os.Args
	if len(args) < 2 || args[len(args)-2] != "wuu-acp-helper" {
		return
	}
	scenario := args[len(args)-1]
	scanner := bufio.NewScanner(os.Stdin)
	write := func(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }
	update := func(kind string, fields map[string]any) {
		fields["sessionUpdate"] = kind
		write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "native-session", "update": fields}})
	}
	text := func(value string) {
		update("agent_message_chunk", map[string]any{"content": map[string]string{"type": "text", "text": value}})
	}
	var promptID json.RawMessage
	selectedModel, selectedEffort, selectedMode := "", "", ""
	selectedSpeed := "off"
	speedSessions := map[string]map[string]string{}
	defaultSpeed := strings.TrimPrefix(scenario, "speed-reset-")
	nextSession := 0
	for scanner.Scan() {
		var msg rpcMessage
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			os.Exit(2)
		}
		if strings.HasPrefix(scenario, "speed-reset-") && msg.Method != "initialize" {
			var params struct {
				SessionID string `json:"sessionId"`
				ConfigID  string `json:"configId"`
				Value     string `json:"value"`
			}
			if json.Unmarshal(msg.Params, &params) != nil {
				os.Exit(2)
			}
			id := params.SessionID
			switch msg.Method {
			case "session/new":
				for {
					nextSession++
					id = fmt.Sprintf("session-%d", nextSession)
					if speedSessions[id] == nil {
						break
					}
				}
				otherSpeed := "on"
				if defaultSpeed == "on" {
					otherSpeed = "off"
				}
				speedSessions[id] = map[string]string{"model": "model-a", "fast-mode": otherSpeed, "reasoning_effort": "low"}
			case "session/load":
				data, err := os.ReadFile(filepath.Join(".", id+".json"))
				if err != nil {
					os.Exit(2)
				}
				if json.Unmarshal(data, &speedSessions) != nil {
					os.Exit(2)
				}
			case "session/set_config_option":
				speedSessions[id][params.ConfigID] = params.Value
				if params.ConfigID == "model" {
					speedSessions[id]["fast-mode"] = defaultSpeed
				}
			case "session/prompt":
				data, _ := json.Marshal(speedSessions)
				if os.WriteFile(filepath.Join(".", id+".json"), data, 0600) != nil {
					os.Exit(2)
				}
				content, _ := json.Marshal(speedSessions[id])
				write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": id, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": string(content)}}}})
				write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]string{"stopReason": "end_turn"}})
				continue
			}
			state := speedSessions[id]
			result := speedACPSessionResult(state["model"], "fast-mode", state["fast-mode"])
			result["sessionId"] = id
			result["configOptions"] = append(result["configOptions"].([]any), map[string]any{"id": "reasoning_effort", "category": "thought_level", "type": "select", "currentValue": state["reasoning_effort"], "options": []any{map[string]string{"value": "low"}, map[string]string{"value": "high"}}})
			write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
			continue
		}
		result := any(map[string]any{})
		switch msg.Method {
		case "initialize":
			version := 1
			if scenario == "version" {
				version = 2
			}
			caps := map[string]any{"loadSession": scenario != "no-load"}
			if scenario == "echo-prompt-images" {
				caps["promptCapabilities"] = map[string]any{"image": true}
			}
			result = map[string]any{"protocolVersion": version, "agentCapabilities": caps}
		case "session/new":
			if scenario == "speed" || scenario == "speed-catalog" {
				result = speedACPSessionResult("model-a", "fast-mode", selectedSpeed)
				break
			}
			if scenario == "grok" {
				result = grokACPSessionResult()
				break
			}
			if scenario == "unattended-mode" {
				result = map[string]any{
					"sessionId": "native-session",
					"configOptions": []any{
						map[string]any{
							"id": "mode", "category": "mode", "type": "select", "currentValue": "accept-edits",
							"options": []any{
								map[string]any{"value": "accept-edits", "name": "Code"},
								map[string]any{"value": "ask", "name": "Ask"},
								map[string]any{"value": "bypass", "name": "Bypass Permissions"},
							},
						},
					},
				}
				break
			}
			if scenario == "codex-mode" {
				selectedMode = "read-only"
				result = map[string]any{
					"sessionId": "native-session",
					"configOptions": []any{
						map[string]any{
							"id": "mode", "category": "mode", "type": "select", "currentValue": "read-only",
							"options": []any{
								map[string]any{"value": "read-only", "name": "Read Only"},
								map[string]any{"value": "agent", "name": "Agent"},
								map[string]any{"value": "agent-full-access", "name": "Agent (full access)"},
							},
						},
					},
				}
				break
			}
			if scenario == "stderr-stall" {
				// The engine explains itself on stderr and then never answers,
				// which is what an unconfigured agent looks like from the wire.
				fmt.Fprintln(os.Stderr, "provider not configured: run `agent model`")
				fmt.Fprintln(os.Stderr, "login: https://agent.example/auth?code=SECRET-CODE-123456\u0026state=xyz")
				fmt.Fprintln(os.Stderr, "token sk-abcdefghijklmnopqrstuvwxyz0123456789ABCDEF")
				continue
			}
			result = map[string]any{"sessionId": "native-session"}
		case "session/set_model":
			var params struct {
				ModelID string `json:"modelId"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			selectedModel = params.ModelID
			result = map[string]any{}
		case "session/set_config_option":
			if scenario == "speed" || scenario == "speed-catalog" {
				var params struct {
					ConfigID string
					Value    string
				}
				_ = json.Unmarshal(msg.Params, &params)
				if params.ConfigID == "model" {
					selectedModel = params.Value
				}
				// Changing the model changes the native speed option ID.
				if params.ConfigID == "fast_mode" {
					selectedSpeed = params.Value
				}
				result = speedACPSessionResult(selectedModel, "fast_mode", selectedSpeed)
				if scenario == "speed-catalog" && selectedModel == "model-b" {
					result.(map[string]any)["configOptions"] = result.(map[string]any)["configOptions"].([]any)[:1]
				}
				break
			}
			var params struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			switch params.ConfigID {
			case "model":
				selectedModel = params.Value
			case "reasoning_effort":
				selectedEffort = params.Value
			case "mode":
				selectedMode = params.Value
			}
		case "session/load":
			if scenario == "load-error" {
				write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32000, "message": "session not found"}})
				continue
			}
			if scenario == "load-malformed" {
				fmt.Println(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"native-session","update":{`)
			}
			text("old replay")
		case "session/prompt":
			if scenario == "speed" || scenario == "speed-catalog" {
				text(selectedSpeed)
				result = map[string]string{"stopReason": "end_turn"}
				break
			}
			promptID = msg.ID
			if strings.HasPrefix(scenario, "echo-prompt") {
				var params struct {
					Prompt json.RawMessage `json:"prompt"`
				}
				_ = json.Unmarshal(msg.Params, &params)
				text(string(params.Prompt))
				result = map[string]string{"stopReason": "end_turn"}
				break
			}
			if scenario == "prompt-stall" {
				write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
					"sessionId": "native-session",
					"update":    map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{}},
				}})
				continue
			}
			if scenario == "prompt-alive" {
				write(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/session_notification", "params": map[string]any{
					"sessionId": "native-session",
					"update":    map[string]any{"sessionUpdate": "queue_ack"},
				}})
				time.Sleep(400 * time.Millisecond)
				text("pong")
				result = map[string]string{"stopReason": "end_turn"}
				break
			}
			if scenario == "prompt-complete-hang" || scenario == "prompt-complete-stale" {
				var prompt struct {
					Meta struct {
						PromptID string `json:"promptId"`
					} `json:"_meta"`
				}
				_ = json.Unmarshal(msg.Params, &prompt)
				if scenario == "prompt-complete-stale" {
					write(map[string]any{"jsonrpc": "2.0", "method": "x.ai/session/prompt_complete", "params": map[string]any{"sessionId": "native-session", "promptId": "stale-p0", "stopReason": "cancelled"}})
				}
				text("pong")
				if scenario == "prompt-complete-hang" {
					write(map[string]any{"jsonrpc": "2.0", "method": "x.ai/session/prompt_complete", "params": map[string]any{"sessionId": "native-session", "promptId": prompt.Meta.PromptID, "stopReason": "end_turn"}})
					continue
				}
				result = map[string]string{"stopReason": "end_turn"}
				break
			}
			if scenario == "grok" {
				text(strings.TrimSpace(selectedModel + " " + selectedEffort))
				result = map[string]string{"stopReason": "end_turn"}
				break
			}
			if scenario == "unattended-mode" || scenario == "codex-mode" {
				text("mode=" + selectedMode)
				result = map[string]string{"stopReason": "end_turn"}
				break
			}
			if scenario == "env" {
				text(os.Getenv("HOME"))
				result = map[string]string{"stopReason": "end_turn"}
				break
			}
			if scenario == "eof" {
				os.Exit(0)
			}
			if scenario == "malformed" {
				fmt.Println("not JSON")
				continue
			}
			if scenario == "stale-id" {
				write(map[string]any{"jsonrpc": "2.0", "id": "unrelated", "result": map[string]string{"stopReason": "end_turn"}})
			}
			if scenario == "permission" {
				write(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "session/request_permission", "params": map[string]any{"sessionId": "native-session", "toolCall": map[string]string{"toolCallId": "exec-1", "title": "Run command", "kind": "execute"}, "options": []any{map[string]string{"optionId": "forever", "kind": "allow_always"}, map[string]string{"optionId": "once", "kind": "allow_once"}, map[string]string{"optionId": "deny", "kind": "reject_once"}}}})
				continue
			}
			text("Hello ")
			if scenario == "quiet" {
				continue
			}
			update("agent_thought_chunk", map[string]any{"content": map[string]string{"type": "text", "text": "Thinking"}})
			update("tool_call", map[string]any{"toolCallId": "read-1", "title": "Read file", "status": "in_progress", "rawInput": map[string]string{"path": "README.md"}})
			update("tool_call_update", map[string]any{"toolCallId": "read-1", "status": "completed", "content": []any{map[string]any{"type": "content", "content": map[string]string{"type": "text", "text": "file contents"}}}})
			text("world")
			stop := "end_turn"
			if scenario == "limit" {
				stop = "max_tokens"
			}
			if scenario == "missing-stop" {
				stop = ""
			}
			result = map[string]string{"stopReason": stop}
		case "session/cancel":
			os.Exit(0)
		case "":
			var reply struct {
				Outcome struct {
					Outcome  string `json:"outcome"`
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			}
			if json.Unmarshal(msg.Result, &reply) != nil {
				os.Exit(3)
			}
			answer := reply.Outcome.OptionID
			if reply.Outcome.Outcome == "cancelled" {
				answer = "cancelled"
			}
			text(answer)
			write(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]string{"stopReason": "end_turn"}})
			continue
		}
		write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}
	os.Exit(0)
}

func TestACPChildInheritsParentEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := testEngine(t, "env").SessionForThread(ctx, testBinding())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, testInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.Content != home {
		t.Fatalf("ACP child HOME = %q, want inherited %q", result.Result.Content, home)
	}
}

func grokACPSessionResult() map[string]any {
	return map[string]any{
		"sessionId": "native-session",
		"models": map[string]any{
			"currentModelId": "grok-4.6",
			"availableModels": []any{
				map[string]any{"modelId": "grok-4.6", "name": "Grok 4.6"},
				map[string]any{"modelId": "grok-4.5", "name": "Grok 4.5"},
			},
		},
		"configOptions": []any{
			map[string]any{
				"id": "model", "category": "model", "type": "select", "currentValue": "grok-4.6",
				"options": []any{
					map[string]any{"value": "grok-4.6", "name": "Grok 4.6"},
					map[string]any{"value": "grok-4.5", "name": "Grok 4.5"},
				},
			},
			map[string]any{
				"id": "reasoning_effort", "category": "thought_level", "type": "select", "currentValue": "high",
				"options": []any{
					map[string]any{"value": "low", "name": "Low"},
					map[string]any{"value": "medium", "name": "Medium"},
					map[string]any{"value": "high", "name": "High"},
				},
			},
		},
	}
}

func TestACPGrokPromptCompleteSettlesHungPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := testEngineID(t, "grok", "prompt-complete-hang").SessionForThread(ctx, testBinding())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, testInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.Content != "pong" || result.Result.StopReason != "end_turn" {
		t.Fatalf("hung prompt settlement = %+v", result)
	}
}

func TestACPIgnoresStaleGrokPromptComplete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := testEngineID(t, "grok", "prompt-complete-stale").SessionForThread(ctx, testBinding())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, testInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.Content != "pong" || result.Result.StopReason != "end_turn" {
		t.Fatalf("stale completion overrode the live turn: %+v", result)
	}
}

func TestACPGrokPromptStallSurfacesWedgedAgent(t *testing.T) {
	previous := grokPromptStall
	grokPromptStall = 200 * time.Millisecond
	t.Cleanup(func() { grokPromptStall = previous })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := testEngineID(t, "grok", "prompt-stall").SessionForThread(ctx, testBinding())
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.RunTurn(ctx, testInput(), nil)
	if err == nil || !strings.Contains(err.Error(), "did not respond to the prompt") {
		t.Fatalf("stall error = %v", err)
	}
}

func TestACPGrokPromptStallClearsOnExtensionNotification(t *testing.T) {
	previous := grokPromptStall
	grokPromptStall = 200 * time.Millisecond
	t.Cleanup(func() { grokPromptStall = previous })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := testEngineID(t, "grok", "prompt-alive").SessionForThread(ctx, testBinding())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, testInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.Content != "pong" {
		t.Fatalf("extension ack was treated as silence: %+v", result)
	}
}

func TestACPDiscoversGrokModelsFromSessionNew(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	catalog, err := testEngine(t, "grok").DiscoverCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	models := catalog.Models
	if len(models) != 2 || models[0].ID != "grok-4.6" || !models[0].IsDefault || models[1].ID != "grok-4.5" {
		t.Fatalf("models = %+v", models)
	}
	if models[0].DisplayName != "Grok 4.6" || models[0].DefaultEffort != "high" {
		t.Fatalf("grok-4.6 = %+v", models[0])
	}
	if len(catalog.Modes) != 2 || catalog.Modes[0].Mode != "standard" || catalog.Modes[0].ID != "" || catalog.Modes[1].Mode != "unconfined" {
		t.Fatalf("modes = %+v", catalog.Modes)
	}
}

func TestACPSelectsAdvertisedGrokModelAndEffort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binding := testBinding()
	binding.Model, binding.Effort = "grok-4.5", "medium"
	session, err := testEngine(t, "grok").SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, testInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.Content != "grok-4.5 medium" {
		t.Fatalf("selection = %q, want grok-4.5 medium", result.Result.Content)
	}
}

func TestACPRejectsUnadvertisedGrokModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binding := testBinding()
	binding.Model = "not-a-grok-model"
	session, err := testEngine(t, "grok").SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.RunTurn(ctx, testInput(), nil)
	if err == nil || !strings.Contains(err.Error(), "not-a-grok-model") {
		t.Fatalf("unadvertised model error = %v", err)
	}
}

func speedACPSessionResult(model, speedID, speed string) map[string]any {
	return map[string]any{"sessionId": "native-session", "configOptions": []any{
		map[string]any{"id": "model", "category": "model", "type": "select", "currentValue": model, "options": []any{map[string]any{"value": "model-a"}, map[string]any{"value": "model-b"}}},
		map[string]any{"id": speedID, "name": "Fast mode", "category": "model_config", "type": "select", "currentValue": speed, "options": []any{map[string]any{"group": "speed", "name": "Speed", "options": []any{map[string]any{"value": "off"}, map[string]any{"value": "on"}}}}},
	}}
}

func TestACPSpeedUsesRefreshedConfigAfterModelChange(t *testing.T) {
	for _, speed := range []string{"fast", "standard"} {
		t.Run(speed, func(t *testing.T) {
			binding := testBinding()
			binding.Model = "model-b"
			binding.Speed = speed
			sess, err := testEngine(t, "speed").SessionForThread(context.Background(), binding)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := sess.RunTurn(ctx, testInput(), nil)
			if err != nil {
				t.Fatal(err)
			}
			want := "off"
			if speed == "fast" {
				want = "on"
			}
			if result.Result.Content != want {
				t.Fatalf("native speed = %q, want %q", result.Result.Content, want)
			}
		})
	}
}

func TestACPRejectsUnadvertisedSpeed(t *testing.T) {
	binding := testBinding()
	binding.Speed = "fast"
	sess, err := testEngine(t, "normal").SessionForThread(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = sess.RunTurn(ctx, testInput(), nil)
	if err == nil || !strings.Contains(err.Error(), "speed") {
		t.Fatalf("unadvertised speed = %v", err)
	}
}

func TestACPCatalogReportsSpeedPerModel(t *testing.T) {
	catalog, err := testEngine(t, "speed-catalog").DiscoverCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 || !catalog.Models[0].FastMode || catalog.Models[1].FastMode {
		t.Fatalf("wrong per-model speed capabilities: %+v", catalog.Models)
	}
}

func TestACPSpeedResetRestoresModelDefaultAfterResume(t *testing.T) {
	for _, defaultSpeed := range []string{"on", "off"} {
		t.Run(defaultSpeed, func(t *testing.T) {
			engine := testEngine(t, "speed-reset-"+defaultSpeed)
			binding := testBinding()
			binding.Model, binding.Effort = "model-b", "high"
			binding.PersistRef = func(ref string) error { binding.ExternalRef = ref; return nil }
			override, wantOverride := "fast", "on"
			if defaultSpeed == "on" {
				override, wantOverride = "standard", "off"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for _, requested := range []string{override, ""} {
				binding.Speed = requested
				sess, err := engine.SessionForThread(ctx, binding)
				if err != nil {
					t.Fatal(err)
				}
				result, err := sess.RunTurn(ctx, testInput(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := sess.Close(ctx); err != nil {
					t.Fatal(err)
				}
				var native map[string]string
				if err := json.Unmarshal([]byte(result.Result.Content), &native); err != nil {
					t.Fatal(err)
				}
				want := wantOverride
				if requested == "" {
					want = defaultSpeed
				}
				if native["fast-mode"] != want || native["model"] != "model-b" || native["reasoning_effort"] != "high" {
					t.Fatalf("speed %q native selection = %v, want speed %q with original model and effort", requested, native, want)
				}
				if binding.ExternalRef != "session-1" {
					t.Fatalf("default probe replaced the saved session: %q", binding.ExternalRef)
				}
			}
		})
	}
}
