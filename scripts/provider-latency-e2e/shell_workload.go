package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// The delay represents a quiet build. The append detects accidental reruns;
// output before and after handoff checks that promotion preserves the stream.
const shellFixture = "printf 'started\\n' >> launches.txt\nprintf 'BUILD_STARTED\\n'\nsleep %g\nprintf '73\\n' > result.txt\nprintf 'BUILD_OK:73\\n'\n"
const shellProgram = `const results = await Promise.all([tools.bash({command:"sh workload.sh",timeout_seconds:60}),tools.read_file({path:"marker.txt"})]); for (const result of results) text(result);`
const completionProgram = `text(await tools.read_file({path:"result.txt"}));`
const firstPrompt = "Call run_code once with exactly this source:\n" + shellProgram + "\nThen reply with exactly READ_OK:<value from marker.txt>, with no other text. This only acknowledges the file read; it does not claim the build finished. Do not wait for the command or call any other tools."
const finalPrompt = "The command has completed. Call run_code once with exactly this source:\n" + completionProgram + "\nThen reply with exactly DONE_OK:<value from result.txt>, with no other text. Do not run the command again."

func runShellWorkload(pc config.ProviderConfig, provider, label string, repeats int, timeout, delay time.Duration, build map[string]string) bool {
	emit(map[string]any{"kind": "run", "utc": time.Now().UTC().Format(time.RFC3339), "label": label,
		"provider": provider, "model": pc.Model, "transport": pc.StreamTransport, "build": build,
		"fixture_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf(shellFixture, delay.Seconds())+firstPrompt+finalPrompt))),
		"repeats":        repeats, "timeout_ms": timeout.Milliseconds(), "shell_delay_ms": delay.Milliseconds(), "effort": "low",
		"scope": "real provider, agent loop, PTC, tool gate, bash, read_file and process completion; excludes desktop delivery/rendering",
		"cost":  "unknown; token usage retained", "workload": "shell-read-completion"})
	passed := true
	for repeat := 1; repeat <= repeats; repeat++ {
		if !shellConversation(pc, provider, repeat, timeout, delay) {
			passed = false
		}
	}
	return passed
}

func shellConversation(pc config.ProviderConfig, provider string, repeat int, timeout, delay time.Duration) bool {
	root, err := os.MkdirTemp("", "wuu-shell-e2e-")
	if err != nil {
		panic("fixture directory unavailable")
	}
	defer os.RemoveAll(root)
	for path, content := range map[string]string{"workload.sh": fmt.Sprintf(shellFixture, delay.Seconds()), "marker.txt": "42\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0600); err != nil {
			panic("fixture write failed")
		}
	}
	kit, err := tools.New(root)
	if err != nil {
		panic("toolkit setup failed")
	}
	kit.SetStateDir(filepath.Join(root, "state"))
	kit.SetSessionDir(filepath.Join(root, "session"))
	kit.SetSessionID("shell-e2e")
	// The allowlist below admits only these synthetic operations. No model-
	// supplied command or arbitrary JavaScript can execute in unconfined mode.
	kit.SetBoundary(tools.UnconfinedBoundary())
	kit.SetToolPolicy(func(name string) bool { return name == "run_code" || name == "bash" || name == "read_file" })
	kit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, _ tools.ToolInfo, call providers.ToolCall) error {
		allowed := false
		switch call.Name {
		case "run_code":
			var args struct {
				Input string `json:"input"`
			}
			code := strings.TrimSpace(call.Arguments)
			if json.Unmarshal([]byte(call.Arguments), &args) == nil {
				code = strings.TrimSpace(args.Input)
			}
			allowed = code == shellProgram || code == completionProgram
		case "bash":
			var args map[string]any
			allowed = json.Unmarshal([]byte(call.Arguments), &args) == nil && len(args) == 2 && args["command"] == "sh workload.sh" && args["timeout_seconds"] == float64(60)
		case "read_file":
			var args map[string]any
			allowed = json.Unmarshal([]byte(call.Arguments), &args) == nil && len(args) == 1 && (args["path"] == "marker.txt" || args["path"] == "result.txt")
		}
		if !allowed {
			return errors.New("operation outside synthetic fixture")
		}
		return nil
	})
	kit.ConfigureSurfaceForProviderModel(provider, pc.Model, true)
	service := codemode.NewService(codemode.ServiceConfig{})
	defer service.Close()
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	manager, err := proc.NewManager(root, filepath.Join(root, "processes"))
	if err != nil {
		panic("process manager setup failed")
	}
	defer func() {
		processes, _ := manager.List()
		for _, process := range processes {
			if process.Status == proc.StatusRunning {
				_, _ = manager.Stop(process.ID)
			}
		}
	}()
	kit.SetProcessManager(manager)
	events := make(chan proc.Event, 32)
	manager.Subscribe(events)
	defer manager.Unsubscribe(events)
	client, err := providerfactory.BuildStreamClient(pc, provider)
	if err != nil {
		panic("client setup failed")
	}
	runner := agent.StreamRunner{Client: client, Tools: kit, ProviderName: provider, Model: pc.Model,
		Effort: "low", PromptCacheKey: fmt.Sprintf("shell-e2e-%d", time.Now().UnixNano()),
		SystemPrompt: "Execute the exact evaluation program supplied by the user, without edits. Then return only the requested answer. Never rerun a command or poll a background process.",
		MaxSteps:     3, DisableAutoCompact: true}
	started := time.Now()
	var mu sync.Mutex
	promotedID := ""
	toolErrors := 0
	var results []map[string]any
	callback := func(e providers.StreamEvent) {
		if e.Type != providers.EventToolUseEnd || e.ToolCall == nil || e.ToolResultDetail == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if e.ToolResultDetail.IsError {
			toolErrors++
		}
		results = append(results, map[string]any{"name": e.ToolCall.Name, "settled_ms": time.Since(started).Milliseconds(), "error": e.ToolResultDetail.IsError})
		if e.ToolCall.Name == "bash" {
			var data struct {
				ID string `json:"promoted_process_id"`
			}
			if json.Unmarshal(e.ToolResultDetail.StructuredContent, &data) == nil {
				promotedID = data.ID
			}
		}
	}
	history := []providers.ChatMessage{{Role: "user", Content: firstPrompt}}
	m := &measurement{start: started, Submissions: make(map[string]bool)}
	runner.InferenceJournal = m
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	res, callErr := runner.RunWithCallback(ctx, history, callback)
	cancel()
	readyMS := time.Since(started).Milliseconds()
	mu.Lock()
	id := promotedID
	mu.Unlock()
	firstOK := callErr == nil && strings.TrimSpace(res.Content) == "READ_OK:42" && !res.Truncated
	completionOK := firstOK
	if id != "" {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
	waitCompletion:
		for {
			select {
			case event := <-events:
				if event.Process.ID == id && event.Cause == proc.EventCauseNaturalExit {
					completionOK = event.Process.ExitCode == 0 && event.Process.RootThreadID == "shell-e2e"
					break waitCompletion
				}
			case <-ctx.Done():
				completionOK = false
				break waitCompletion
			}
		}
		output, truncated, readErr := manager.ReadOutput(id, 4096)
		completionOK = completionOK && readErr == nil && !truncated && strings.Contains(output, "BUILD_STARTED") && strings.Contains(output, "BUILD_OK:73")
	}
	completedMS := time.Since(started).Milliseconds()
	history = append(history, res.NewMessages...)
	history = append(history, providers.ChatMessage{Role: "user", Content: finalPrompt})
	finalOK := false
	if firstOK && completionOK {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		final, finalErr := runner.RunWithCallback(ctx, history, callback)
		cancel()
		finalOK = finalErr == nil && strings.TrimSpace(final.Content) == "DONE_OK:73" && !final.Truncated
		res.InputTokens += final.InputTokens
		res.OutputTokens += final.OutputTokens
		res.CacheReadTokens += final.CacheReadTokens
	}
	launches, readErr := os.ReadFile(filepath.Join(root, "launches.txt"))
	once := readErr == nil && string(launches) == "started\n"
	var records []map[string]any
	counts := map[string]int{}
	for _, record := range kit.ToolTelemetry() {
		counts[record.Name]++
		// Decode the optional field to keep this runner buildable on both
		// revisions, including a baseline without queue telemetry.
		data, _ := json.Marshal(record)
		var fields map[string]any
		_ = json.Unmarshal(data, &fields)
		records = append(records, map[string]any{"name": record.Name, "started_ms": record.StartedAt.Sub(started).Milliseconds(),
			"duration_ms": record.DurationMS, "queue_wait_ms": fields["queue_wait_ms"], "success": record.Success})
	}
	mu.Lock()
	defer mu.Unlock()
	passed := firstOK && finalOK && completionOK && once && m.Requests == 4 && toolErrors == 0 && counts["bash"] == 1 && counts["read_file"] == 2 && counts["run_code"] == 2
	failure := ""
	if callErr != nil {
		failure = string(providers.NormalizeFailure(callErr).Category)
	} else if !passed {
		failure = "evaluation_contract"
	}
	emit(map[string]any{"kind": "conversation", "repeat": repeat, "passed": passed, "failure_category": failure,
		"ready_ms": readyMS, "completion_observed_ms": completedMS, "total_ms": time.Since(started).Milliseconds(),
		"promoted": id != "", "completion_ok": completionOK, "launched_once": once, "first_answer_ok": firstOK, "final_answer_ok": finalOK,
		"first_answer_contains_marker": strings.Contains(res.Content, "READ_OK:42"), "first_answer_truncated": res.Truncated,
		"tool_errors": toolErrors, "tool_results": results, "tool_records": records, "submissions": m.Requests,
		"input_tokens": res.InputTokens, "output_tokens": res.OutputTokens, "cache_read_tokens": res.CacheReadTokens})
	return passed
}
