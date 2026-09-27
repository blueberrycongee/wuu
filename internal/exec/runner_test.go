package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type fakeController struct {
	initResult appserver.InitializeResult
	thread     appserver.Thread
	run        appserver.Run
	events     []Notification
	block      bool

	startedThread   bool
	resumedThread   string
	forkedThread    string
	startedParams   appserver.RunStartParams
	interruptReason string
	shutdown        bool
}

type initializeErrorController struct {
	*fakeController
	err error
}

func (c *initializeErrorController) Initialize(context.Context) (appserver.InitializeResult, error) {
	return appserver.InitializeResult{}, c.err
}

func newFakeController(events ...Notification) *fakeController {
	return &fakeController{
		initResult: appserver.InitializeResult{
			ProtocolVersion: appserver.ProtocolVersion,
			Provider:        "test-provider",
			Model:           "test-model",
			WorkspaceRoot:   "/repo",
			Permissions: appserver.PermissionSummary{
				Mode: "standard",
			},
		},
		thread: appserver.Thread{ID: "thread-1", ModelProvider: "test-provider", Model: "test-model", CWD: "/repo"},
		run: appserver.Run{
			ID: "run-1", Status: execution.StatusRunning, ThreadID: "thread-1",
			Turns: []execution.TurnRef{{TurnID: "turn-1", ThreadID: "thread-1"}},
		},
		events: events,
	}
}

func (f *fakeController) Initialize(context.Context) (appserver.InitializeResult, error) {
	return f.initResult, nil
}

func (f *fakeController) StartThread(_ context.Context, ephemeral bool) (appserver.Thread, error) {
	f.startedThread = true
	f.thread.Ephemeral = ephemeral
	return f.thread, nil
}

func (f *fakeController) ResumeThread(_ context.Context, id string) (appserver.Thread, error) {
	f.resumedThread = id
	return f.thread, nil
}

func (f *fakeController) ForkThread(_ context.Context, id string) (appserver.Thread, error) {
	f.forkedThread = id
	f.thread.ID = "fork-thread-1"
	f.thread.ForkedFromID = id
	return f.thread, nil
}

func (f *fakeController) StartRun(_ context.Context, params appserver.RunStartParams) (appserver.Run, error) {
	f.startedParams = params
	return f.run, nil
}

func (f *fakeController) InterruptRun(_ context.Context, _ string, reason string) (appserver.Run, error) {
	f.interruptReason = reason
	return f.run, nil
}

func (f *fakeController) Shutdown(context.Context) error {
	f.shutdown = true
	return nil
}

// Notifications mirrors the in-process app-server: a turn/started for the
// Run's first turn, then the scripted events, then — unless the script
// already settles the Run — a synthesized completed run/updated carrying
// the last scripted turn's id and trace path.
func (f *fakeController) Notifications() <-chan Notification {
	if f.block {
		return make(chan Notification)
	}
	hasTurnStarted := false
	hasRunUpdated := false
	lastTurnID := "turn-1"
	lastTrace := ""
	for _, ev := range f.events {
		switch ev.Method {
		case appserver.NotificationTurnStarted:
			hasTurnStarted = true
		case appserver.NotificationRunUpdated:
			hasRunUpdated = true
		case appserver.NotificationTurnCompleted:
			var params appserver.TurnCompletedNotification
			if err := json.Unmarshal(ev.Params, &params); err == nil {
				lastTurnID = params.Turn.ID
				lastTrace = params.TracePath
			}
		}
	}
	events := make([]Notification, 0, len(f.events)+2)
	if !hasTurnStarted {
		events = append(events, notification(appserver.NotificationTurnStarted, appserver.TurnStartedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}}))
	}
	events = append(events, f.events...)
	if !hasRunUpdated {
		events = append(events, notification(appserver.NotificationRunUpdated, appserver.RunUpdatedNotification{Run: appserver.Run{
			ID: "run-1", ThreadID: "thread-1", Status: execution.StatusCompleted,
			Result: &execution.Result{FinalTurnID: lastTurnID, TracePath: lastTrace},
		}}))
	}
	ch := make(chan Notification, len(events))
	for _, ev := range events {
		ch <- ev
	}
	return ch
}

func TestRunDefaultStdoutOnlyFinalMessage(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationAgentMessageDelta, appserver.AgentMessageDeltaNotification{ThreadID: "thread-1", TurnID: "turn-1", Delta: "partial"}),
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "final answer", TracePath: "/trace.jsonl"}),
	)
	var stdout, stderr bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt:     "do work",
		Stdout:     &stdout,
		Stderr:     &stderr,
		Controller: controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stdout.String(); got != "final answer\n" {
		t.Fatalf("stdout = %q", got)
	}
	if strings.Contains(stdout.String(), "provider") || strings.Contains(stdout.String(), "trace_path") {
		t.Fatalf("stdout should contain only final answer, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "provider: test-provider") || !strings.Contains(stderr.String(), "trace_path: /trace.jsonl") {
		t.Fatalf("stderr missing run metadata: %q", stderr.String())
	}
	if !controller.startedThread || controller.startedParams.Prompt != "do work" || !controller.shutdown {
		t.Fatalf("controller did not run expected app-server path: %+v", controller)
	}
}

func TestRunJSONLEmitsResultWhenInitializeFails(t *testing.T) {
	controller := &initializeErrorController{
		fakeController: newFakeController(),
		err:            errors.New("initialize failed"),
	}
	var stdout bytes.Buffer

	err := Run(context.Background(), Options{
		Prompt:     "do work",
		JSON:       true,
		Stdout:     &stdout,
		Controller: controller,
	})
	if ExitCode(err) != ExitProtocol {
		t.Fatalf("exit code = %d, want %d: %v", ExitCode(err), ExitProtocol, err)
	}
	events := parseJSONLines(t, stdout.String())
	if len(events) != 1 || events[0]["type"] != "result" || events[0]["status"] != "failed" {
		t.Fatalf("events = %+v, want one failed result", events)
	}
}

func TestRunJSONLDoesNotReportCompletedBeforeLastMessageWrite(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "done"}),
	)
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	var stdout bytes.Buffer

	err := Run(context.Background(), Options{
		Prompt:            "do work",
		JSON:              true,
		Stdout:            &stdout,
		OutputLastMessage: filepath.Join(blocker, "result.md"),
		Controller:        controller,
	})
	if ExitCode(err) != ExitTurnFailed {
		t.Fatalf("exit code = %d, want %d: %v", ExitCode(err), ExitTurnFailed, err)
	}
	events := parseJSONLines(t, stdout.String())
	last := events[len(events)-1]
	if last["type"] != "result" || last["status"] != "failed" {
		t.Fatalf("last event = %+v, want failed result", last)
	}
}

func TestRunWaitsForAutomaticContinuation(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{
			ThreadID:                 "thread-1",
			Turn:                     appserver.Turn{ID: "turn-1"},
			Content:                  "waiting for the background process",
			AwaitingAutoContinuation: true,
		}),
		notification(appserver.NotificationTurnStarted, appserver.TurnStartedNotification{
			ThreadID: "thread-1",
			Turn:     appserver.Turn{ID: "turn-2"},
		}),
		notification(appserver.NotificationAgentMessageDelta, appserver.AgentMessageDeltaNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-2",
			Delta:    "continued",
		}),
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{
			ThreadID:  "thread-1",
			Turn:      appserver.Turn{ID: "turn-2"},
			Content:   "background work verified",
			TracePath: "/trace-2.jsonl",
		}),
	)
	var stdout, stderr bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt:     "run background work",
		Stdout:     &stdout,
		Stderr:     &stderr,
		Controller: controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stdout.String(); got != "background work verified\n" {
		t.Fatalf("stdout = %q", got)
	}
	if strings.Contains(stdout.String(), "waiting for the background process") {
		t.Fatalf("stdout exposed intermediate completion: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "trace_path: /trace-2.jsonl") {
		t.Fatalf("stderr missing continuation trace path: %q", stderr.String())
	}
}

func TestRunJSONLEmitsStableEvents(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationTurnEvent, appserver.TurnEventNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Event: appserver.StreamEventPayload{
				Type: providers.EventRequestContext,
				RequestContext: &providers.RequestContextSummary{
					StepIndex:                1,
					TransientMessages:        2,
					ContentBytes:             128,
					BlockKinds:               []string{"ENVIRONMENT", "TOOL_POLICY"},
					BlockKindCounts:          map[string]int{"ENVIRONMENT": 1, "TOOL_POLICY": 1},
					BlockKindBytes:           map[string]int{"ENVIRONMENT": 80, "TOOL_POLICY": 48},
					SegmentLifecycleCounts:   map[string]int{"request_only": 1},
					SegmentPlacementCounts:   map[string]int{"after_history": 1},
					SegmentCachePolicyCounts: map[string]int{"volatile": 1},
					MessageCount:             4,
					SystemMessages:           1,
					HiddenMessages:           2,
					ToolCount:                9,
					StablePrefix:             0,
					TurnPrefix:               1,
					DynamicBytes:             128,
					SystemBytes:              2048,
					StablePrefixBytes:        2048,
					TurnPrefixBytes:          2176,
					MessageBytes:             2304,
					ToolSchemaBytes:          4096,
					LoadableToolCount:        1,
					LoadableToolSchemaBytes:  512,
					LoadableToolSurfaceHash:  "loadable-hash",
					SystemHash:               "system-hash",
					StablePrefixHash:         "stable-hash",
					TurnPrefixHash:           "turn-hash",
					ToolSurfaceHash:          "tools-hash",
					PromptCacheKey:           "thread-1",
					SystemSections: []providers.SystemPromptSectionSummary{{
						Key:    "base",
						Static: true,
						Bytes:  512,
						Hash:   "base-hash",
					}},
				},
			},
		}),
		notification(appserver.NotificationTurnEvent, appserver.TurnEventNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Event: appserver.StreamEventPayload{
				Type: providers.EventProviderState,
				ProviderState: &providers.ProviderStateSummary{
					StepIndex:              1,
					Provider:               "openai",
					Protocol:               "responses_websocket",
					Transport:              "websocket",
					ReplayMode:             "previous_response_id",
					PreviousResponseIDUsed: true,
					ConnectionReused:       true,
					FallbackActive:         true,
					FallbackReason:         "websocket_failed_before_first_event",
					FallbackPinStatus:      "created",
					FallbackRetryAfterMS:   29950,
					FallbackTTLMS:          30000,
					InputItems:             1,
					FullInputItems:         3,
					DeltaInputItems:        1,
				},
			},
		}),
		notification(appserver.NotificationAgentMessageDelta, appserver.AgentMessageDeltaNotification{ThreadID: "thread-1", TurnID: "turn-1", Delta: "hello"}),
		notification(appserver.NotificationTurnUsage, appserver.TurnUsageNotification{ThreadID: "thread-1", TurnID: "turn-1", InputTokens: 3, OutputTokens: 4}),
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "hello", TracePath: "/trace.jsonl"}),
	)
	controller.initResult.MaxParallel = 7
	var stdout, stderr bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt:     "do work",
		JSON:       true,
		Stdout:     &stdout,
		Stderr:     &stderr,
		Controller: controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("json mode should not write diagnostics to stderr in fake run, got %q", stderr.String())
	}
	events := parseJSONLines(t, stdout.String())
	gotTypes := make([]string, 0, len(events))
	for _, event := range events {
		gotTypes = append(gotTypes, event["type"].(string))
	}
	wantTypes := []string{"session_configured", "thread_started", "turn_started", "request_context", "provider_state", "agent_message_delta", "usage_updated", "turn_completed", "result"}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types:\n got: %#v\nwant: %#v\njsonl:\n%s", gotTypes, wantTypes, stdout.String())
	}
	sessionConfigured := events[0]
	if sessionConfigured["max_parallel"] != float64(7) {
		t.Fatalf("session_configured missing max_parallel readback: %+v", sessionConfigured)
	}
	requestContext := events[3]
	if requestContext["tool_count"] != float64(9) ||
		requestContext["turn_prefix"] != float64(1) ||
		requestContext["dynamic_context_bytes"] != float64(128) ||
		requestContext["system_bytes"] != float64(2048) ||
		requestContext["stable_prefix_bytes"] != float64(2048) ||
		requestContext["turn_prefix_bytes"] != float64(2176) ||
		requestContext["message_bytes"] != float64(2304) ||
		requestContext["tool_schema_bytes"] != float64(4096) ||
		requestContext["loadable_tool_count"] != float64(1) ||
		requestContext["loadable_tool_schema_bytes"] != float64(512) ||
		requestContext["loadable_tool_surface_hash"] != "loadable-hash" ||
		requestContext["system_hash"] != "system-hash" ||
		requestContext["stable_prefix_hash"] != "stable-hash" ||
		requestContext["turn_prefix_hash"] != "turn-hash" ||
		requestContext["tool_surface_hash"] != "tools-hash" ||
		requestContext["prompt_cache_key"] != "thread-1" {
		t.Fatalf("unexpected request_context event: %+v", requestContext)
	}
	if !reflect.DeepEqual(requestContext["block_kind_counts"], map[string]any{"ENVIRONMENT": float64(1), "TOOL_POLICY": float64(1)}) ||
		!reflect.DeepEqual(requestContext["block_kind_bytes"], map[string]any{"ENVIRONMENT": float64(80), "TOOL_POLICY": float64(48)}) {
		t.Fatalf("unexpected request_context block metrics: %+v", requestContext)
	}
	if !reflect.DeepEqual(requestContext["segment_lifecycle_counts"], map[string]any{"request_only": float64(1)}) ||
		!reflect.DeepEqual(requestContext["segment_placement_counts"], map[string]any{"after_history": float64(1)}) ||
		!reflect.DeepEqual(requestContext["segment_cache_policy_counts"], map[string]any{"volatile": float64(1)}) {
		t.Fatalf("unexpected request_context segment policy metrics: %+v", requestContext)
	}
	sections, ok := requestContext["system_sections"].([]any)
	if !ok || len(sections) != 1 {
		t.Fatalf("request_context missing system_sections: %+v", requestContext)
	}
	section, ok := sections[0].(map[string]any)
	if !ok || section["key"] != "base" || section["static"] != true || section["bytes"] != float64(512) || section["hash"] != "base-hash" {
		t.Fatalf("unexpected system section: %+v", sections[0])
	}
	providerState := events[4]
	if providerState["step_index"] != float64(1) ||
		providerState["provider"] != "openai" ||
		providerState["protocol"] != "responses_websocket" ||
		providerState["transport"] != "websocket" ||
		providerState["replay_mode"] != "previous_response_id" ||
		providerState["previous_response_id_used"] != true ||
		providerState["connection_reused"] != true ||
		providerState["fallback_active"] != true ||
		providerState["fallback_reason"] != "websocket_failed_before_first_event" ||
		providerState["fallback_pin_status"] != "created" ||
		providerState["fallback_retry_after_ms"] != float64(29950) ||
		providerState["fallback_ttl_ms"] != float64(30000) ||
		providerState["input_items"] != float64(1) ||
		providerState["full_input_items"] != float64(3) ||
		providerState["delta_input_items"] != float64(1) {
		t.Fatalf("unexpected provider_state event: %+v", providerState)
	}
	result := events[len(events)-1]
	if result["status"] != "completed" || result["thread_id"] != "thread-1" || result["turn_id"] != "turn-1" || result["final_message"] != "hello" || result["trace_path"] != "/trace.jsonl" {
		t.Fatalf("unexpected result event: %+v", result)
	}
}

func TestRunResumeLastUsesResumePath(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "continued"}),
	)
	var stdout, stderr bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt:     "continue",
		ResumeLast: true,
		Stdout:     &stdout,
		Stderr:     &stderr,
		Controller: controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if controller.startedThread {
		t.Fatal("resume should not start a new thread")
	}
	if controller.resumedThread != "" {
		t.Fatalf("resume last should pass empty thread id, got %q", controller.resumedThread)
	}
}

func TestRunForkUsesForkPath(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "fork-thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "forked"}),
	)
	var stdout bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt:     "continue from fork",
		ForkID:     "source-thread",
		JSON:       true,
		Stdout:     &stdout,
		Controller: controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if controller.startedThread || controller.resumedThread != "" {
		t.Fatalf("fork should not start or resume: started=%v resumed=%q", controller.startedThread, controller.resumedThread)
	}
	if controller.forkedThread != "source-thread" {
		t.Fatalf("forkedThread = %q", controller.forkedThread)
	}
	events := parseJSONLines(t, stdout.String())
	if got := events[1]["type"]; got != "thread_forked" {
		t.Fatalf("second event = %v, want thread_forked\n%s", got, stdout.String())
	}
}

func TestRunPassesAttachmentsToRunStart(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "done"}),
	)
	var stdout bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt: "inspect",
		Attachments: Attachments{
			Images: []appserver.TurnStartImage{{MediaType: "image/png", Data: "image-data"}},
			Files:  []appserver.TurnStartFile{{MediaType: "application/pdf", Data: "file-data", Filename: "report.pdf"}},
		},
		Stdout:     &stdout,
		Controller: controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	images := controller.startedParams.Images
	if len(images) != 1 || images[0].MediaType != "image/png" || images[0].Data != "image-data" {
		t.Fatalf("images not passed to run/start: %+v", images)
	}
	files := controller.startedParams.Files
	if len(files) != 1 || files[0].MediaType != "application/pdf" || files[0].Filename != "report.pdf" {
		t.Fatalf("files not passed to run/start: %+v", files)
	}
}

func TestRunTurnErrorReturnsExitCodeOne(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationTurnError, appserver.TurnErrorNotification{ThreadID: "thread-1", TurnID: "turn-1", Error: "model failed"}),
		notification(appserver.NotificationRunUpdated, appserver.RunUpdatedNotification{Run: appserver.Run{
			ID: "run-1", ThreadID: "thread-1", Status: execution.StatusFailed,
			Error: &execution.Error{Code: "turn_failed", Category: "unknown", Message: "model failed"},
		}}),
	)
	var stdout bytes.Buffer

	err := Run(context.Background(), Options{
		Prompt:     "do work",
		JSON:       true,
		Stdout:     &stdout,
		Controller: controller,
	})
	if ExitCode(err) != ExitTurnFailed {
		t.Fatalf("ExitCode = %d, err=%v", ExitCode(err), err)
	}
	events := parseJSONLines(t, stdout.String())
	result := events[len(events)-1]
	if result["status"] != "failed" || result["error"] != "model failed" {
		t.Fatalf("unexpected failure result: %+v", result)
	}
}

func TestRunTurnErrorWithInterruptedStatusEmitsTurnInterrupted(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationTurnError, appserver.TurnErrorNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Error:    "interrupted",
			Turn:     appserver.Turn{ID: "turn-1", Status: appserver.TurnStatusInterrupted},
		}),
		notification(appserver.NotificationRunUpdated, appserver.RunUpdatedNotification{Run: appserver.Run{
			ID: "run-1", ThreadID: "thread-1", Status: execution.StatusInterrupted,
			Error: &execution.Error{Code: "interrupted", Category: "cancelled", Message: "interrupted"},
		}}),
	)
	var stdout bytes.Buffer

	err := Run(context.Background(), Options{
		Prompt:     "do work",
		JSON:       true,
		Stdout:     &stdout,
		Controller: controller,
	})
	if ExitCode(err) != ExitInterrupted {
		t.Fatalf("ExitCode = %d, err=%v", ExitCode(err), err)
	}
	events := parseJSONLines(t, stdout.String())
	var sawInterrupted bool
	for _, ev := range events {
		if ev["type"] == "turn_failed" {
			t.Fatalf("interrupted turn must not emit turn_failed: %+v", ev)
		}
		if ev["type"] == "turn_interrupted" {
			sawInterrupted = true
		}
	}
	if !sawInterrupted {
		t.Fatalf("expected a turn_interrupted event, got %+v", events)
	}
	result := events[len(events)-1]
	if result["status"] != "interrupted" {
		t.Fatalf("unexpected result status: %+v", result)
	}
}

func TestRunFailedErrorPrefersStructuredCategory(t *testing.T) {
	tests := []struct {
		name     string
		category string
		message  string
		want     int
	}{
		{name: "local tool error mentioning provider", category: "local", message: "tool failed contacting provider", want: ExitToolFailed},
		{name: "provider error mentioning tool", category: "provider", message: "tool execution failed upstream", want: ExitProviderModelError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			controller := newFakeController(
				notification(appserver.NotificationTurnError, appserver.TurnErrorNotification{
					ThreadID: "thread-1", TurnID: "turn-1", Error: tc.message, Category: tc.category,
				}),
				notification(appserver.NotificationRunUpdated, appserver.RunUpdatedNotification{Run: appserver.Run{
					ID: "run-1", ThreadID: "thread-1", Status: execution.StatusFailed,
					Error: &execution.Error{Code: "turn_failed", Category: tc.category, Message: tc.message},
				}}),
			)
			var stdout bytes.Buffer
			err := Run(context.Background(), Options{Prompt: "do work", JSON: true, Stdout: &stdout, Controller: controller})
			if ExitCode(err) != tc.want {
				t.Fatalf("ExitCode = %d, want %d: %v", ExitCode(err), tc.want, err)
			}
		})
	}
}

func TestRunExitCodePreservesStableExitClassification(t *testing.T) {
	t.Run("trusts persisted exit code", func(t *testing.T) {
		run := appserver.Run{Status: execution.StatusFailed, Result: &execution.Result{ExitCode: ExitPermissionDenied}}
		if got := runExitCode(run); got != ExitPermissionDenied {
			t.Fatalf("runExitCode = %d, want %d", got, ExitPermissionDenied)
		}
	})
	tests := []struct {
		category string
		want     int
	}{
		{category: "provider", want: ExitProviderModelError},
		{category: "local", want: ExitToolFailed},
		{category: "permission_denied", want: ExitPermissionDenied},
	}
	for _, tc := range tests {
		run := appserver.Run{Status: execution.StatusFailed, Error: &execution.Error{Category: tc.category, Message: "failed"}}
		if got := runExitCode(run); got != tc.want {
			t.Fatalf("category %q exit code = %d, want %d", tc.category, got, tc.want)
		}
	}
}

func TestRunTimeoutInterruptsAndReturnsExitCodeFour(t *testing.T) {
	controller := newFakeController()
	controller.block = true
	var stdout bytes.Buffer

	err := Run(context.Background(), Options{
		Prompt:     "do work",
		JSON:       true,
		Timeout:    20 * time.Millisecond,
		Stdout:     &stdout,
		Controller: controller,
	})
	if ExitCode(err) != ExitTimeout {
		t.Fatalf("ExitCode = %d, err=%v", ExitCode(err), err)
	}
	if controller.interruptReason != "timeout" {
		t.Fatalf("interrupt reason = %q", controller.interruptReason)
	}
	if !controller.shutdown {
		t.Fatal("controller was not shutdown")
	}
	events := parseJSONLines(t, stdout.String())
	types := eventTypes(events)
	for _, want := range []string{"turn_interrupted", "result"} {
		if !containsString(types, want) {
			t.Fatalf("missing %s in events %#v\n%s", want, types, stdout.String())
		}
	}
	result := events[len(events)-1]
	if result["status"] != "timeout" || result["error"] != "timeout" {
		t.Fatalf("unexpected timeout result: %+v", result)
	}
}

func TestClassifySetupErrorReturnsProviderModelExit(t *testing.T) {
	err := classifySetupError(fmt.Errorf("no API key found for provider %q", "test"))
	if ExitCode(err) != ExitProviderModelError {
		t.Fatalf("ExitCode = %d, err=%v", ExitCode(err), err)
	}
}

func TestRunJSONLEmitsWorkEventFamilies(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationReasoningDelta, appserver.ReasoningDeltaNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Delta:    "hidden provider reasoning delta",
		}),
		notification(appserver.NotificationReasoningReplace, appserver.ReasoningReplaceNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Text:     "hidden provider reasoning final",
		}),
		notification(appserver.NotificationAgentMessageDelta, appserver.AgentMessageDeltaNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Delta:    "visible answer",
		}),
		notification(appserver.NotificationItemStarted, appserver.ItemStartedNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Item: appserver.ThreadItem{
				ID:        "item-draft",
				Type:      appserver.ThreadItemToolCall,
				Name:      "read_file",
				Arguments: `{"path":"partial`,
			},
		}),
		notification(appserver.NotificationItemRemoved, appserver.ItemRemovedNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			ItemID:   "item-draft",
		}),
		notification(appserver.NotificationItemStarted, appserver.ItemStartedNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Item: appserver.ThreadItem{
				ID:        "item-bash",
				Type:      appserver.ThreadItemToolCall,
				Name:      "bash",
				Arguments: `{"command":"go test ./..."}`,
			},
		}),
		notification(appserver.NotificationToolCallOutput, appserver.ToolCallOutputNotification{ThreadID: "thread-1", TurnID: "turn-1", ItemID: "item-bash", Delta: "ok\n"}),
		notification(appserver.NotificationItemCompleted, appserver.ItemCompletedNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Item: appserver.ThreadItem{
				ID:     "item-bash",
				Type:   appserver.ThreadItemToolCall,
				Name:   "bash",
				Status: appserver.ThreadItemStatusCompleted,
			},
		}),
		notification(appserver.NotificationItemCompleted, appserver.ItemCompletedNotification{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Item: appserver.ThreadItem{
				ID:     "item-write",
				Type:   appserver.ThreadItemToolCall,
				Name:   "write_file",
				Status: appserver.ThreadItemStatusCompleted,
				Result: `{"action":"create","path":"notes.txt","new_file_sha":"sha256:abc","workspace_revision":"fs:worktree:1"}`,
			},
		}),
		notification(appserver.NotificationAgentUpdated, appserver.AgentUpdatedNotification{
			ThreadID: "thread-1",
			Agent:    appserver.Agent{ID: "agent-1", Type: "subagent", TaskName: "worker", Status: "running"},
		}),
		notification(appserver.NotificationAgentUpdated, appserver.AgentUpdatedNotification{
			ThreadID: "thread-1",
			Agent:    appserver.Agent{ID: "agent-1", Type: "subagent", TaskName: "worker", Status: "completed", Result: "done"},
		}),
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: "done"}),
	)
	var stdout bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt:     "do work",
		JSON:       true,
		Stdout:     &stdout,
		Controller: controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := parseJSONLines(t, stdout.String())
	types := eventTypes(events)
	for _, want := range []string{"agent_message_delta", "tool_started", "tool_removed", "tool_output_delta", "tool_completed", "command_started", "command_output_delta", "command_completed", "file_changed", "subagent_started", "subagent_completed"} {
		if !containsString(types, want) {
			t.Fatalf("missing %s in events %#v\n%s", want, types, stdout.String())
		}
	}
	for _, forbidden := range []string{"reasoning_delta", "reasoning_final", "hidden provider reasoning"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("JSONL leaked provider reasoning %q:\n%s", forbidden, stdout.String())
		}
	}
	agentMessage := firstEventOfType(t, events, "agent_message_delta")
	if agentMessage["delta"] != "visible answer" {
		t.Fatalf("unexpected agent_message_delta: %+v", agentMessage)
	}
	commandStarted := firstEventOfType(t, events, "command_started")
	if commandStarted["command"] != "go test ./..." {
		t.Fatalf("command_started missing command: %+v", commandStarted)
	}
	fileChanged := firstEventOfType(t, events, "file_changed")
	if fileChanged["path"] != "notes.txt" || fileChanged["action"] != "create" || fileChanged["new_file_sha"] != "sha256:abc" {
		t.Fatalf("unexpected file_changed: %+v", fileChanged)
	}
}

func TestEmitItemStartedRedactsToolArguments(t *testing.T) {
	var stdout bytes.Buffer
	state := runState{}
	emitItemStarted(Options{JSON: true, Stdout: &stdout}, appserver.ItemStartedNotification{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Item: appserver.ThreadItem{
			ID:        "item-bash",
			Type:      appserver.ThreadItemToolCall,
			Name:      "bash",
			Arguments: `{"command":"curl -H 'Authorization: Bearer secret-token-value' example.com"}`,
		},
	}, &state)

	events := parseJSONLines(t, stdout.String())
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		if strings.Contains(string(encoded), "secret-token-value") {
			t.Fatalf("secret leaked in event: %s", encoded)
		}
	}
	toolStarted := firstEventOfType(t, events, "tool_started")
	if !strings.Contains(toolStarted["arguments"].(string), "[REDACTED]") {
		t.Fatalf("tool arguments were not redacted: %+v", toolStarted)
	}
}

func TestRunIgnoresChildTurnCompletionUntilRootCompletes(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationAgentUpdated, appserver.AgentUpdatedNotification{
			ThreadID: "thread-1",
			Agent:    appserver.Agent{ID: "agent-1", Type: "general-purpose", TaskName: "worker", Status: "running"},
		}),
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{
			ThreadID: "agent-1",
			Turn:     appserver.Turn{ID: "agent-turn-1"},
			Content:  "child result",
		}),
		notification(appserver.NotificationAgentUpdated, appserver.AgentUpdatedNotification{
			ThreadID: "thread-1",
			Agent:    appserver.Agent{ID: "agent-1", Type: "general-purpose", TaskName: "worker", Status: "completed", Result: "child result"},
		}),
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{
			ThreadID:  "thread-1",
			Turn:      appserver.Turn{ID: "turn-1"},
			Content:   "parent result",
			TracePath: "/trace.jsonl",
		}),
	)
	var stdout bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt:     "spawn worker",
		JSON:       true,
		Stdout:     &stdout,
		Controller: controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := parseJSONLines(t, stdout.String())
	types := eventTypes(events)
	if !containsString(types, "subagent_completed") {
		t.Fatalf("missing subagent_completed in %#v\n%s", types, stdout.String())
	}
	result := events[len(events)-1]
	if result["type"] != "result" || result["thread_id"] != "thread-1" || result["turn_id"] != "turn-1" || result["final_message"] != "parent result" {
		t.Fatalf("exec should finish on root turn, got %+v\n%s", result, stdout.String())
	}
}

func TestRunIgnoresChildTurnErrorUntilRootCompletes(t *testing.T) {
	controller := newFakeController(
		notification(appserver.NotificationTurnError, appserver.TurnErrorNotification{
			ThreadID: "agent-1",
			TurnID:   "agent-turn-1",
			Error:    "child failed",
			Turn:     appserver.Turn{ID: "agent-turn-1"},
		}),
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{
			ThreadID:  "thread-1",
			Turn:      appserver.Turn{ID: "turn-1"},
			Content:   "parent handled child failure",
			TracePath: "/trace.jsonl",
		}),
	)
	var stdout bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt:     "await worker",
		JSON:       true,
		Stdout:     &stdout,
		Controller: controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := parseJSONLines(t, stdout.String())
	types := eventTypes(events)
	if !containsString(types, "turn_failed") {
		t.Fatalf("missing child turn_failed in %#v\n%s", types, stdout.String())
	}
	result := events[len(events)-1]
	if result["type"] != "result" || result["status"] != "completed" || result["final_message"] != "parent handled child failure" {
		t.Fatalf("exec should let root turn decide after child error, got %+v\n%s", result, stdout.String())
	}
}

func TestRunOutputSchemaEmitsStructuredResult(t *testing.T) {
	schemaPath := writeExecSchema(t, `{
		"type": "object",
		"required": ["summary"],
		"properties": {
			"summary": {"type": "string"}
		},
		"additionalProperties": false
	}`)
	controller := newFakeController(
		notification(appserver.NotificationTurnCompleted, appserver.TurnCompletedNotification{ThreadID: "thread-1", Turn: appserver.Turn{ID: "turn-1"}, Content: `{"summary":"done"}`, TracePath: "/trace.jsonl"}),
	)
	var stdout bytes.Buffer

	if err := Run(context.Background(), Options{
		Prompt:           "summarize",
		OutputSchemaPath: schemaPath,
		JSON:             true,
		Stdout:           &stdout,
		Controller:       controller,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The schema travels to the app-server untouched; prompt injection and
	// retries happen inside the Run, not in the exec client.
	if controller.startedParams.Prompt != "summarize" {
		t.Fatalf("exec should forward the prompt untouched, got %q", controller.startedParams.Prompt)
	}
	if !strings.Contains(string(controller.startedParams.OutputSchema), `"summary"`) {
		t.Fatalf("run/start missing output schema: %q", controller.startedParams.OutputSchema)
	}
	events := parseJSONLines(t, stdout.String())
	result := events[len(events)-1]
	structured, ok := result["structured_result"].(map[string]any)
	if !ok || structured["summary"] != "done" {
		t.Fatalf("structured_result = %+v", result["structured_result"])
	}
}

func notification(method string, params any) Notification {
	data, err := json.Marshal(params)
	if err != nil {
		panic(err)
	}
	return Notification{Method: method, Params: data}
}

func parseJSONLines(t *testing.T, text string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var events []map[string]any
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func eventTypes(events []map[string]any) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		if typ, _ := event["type"].(string); typ != "" {
			out = append(out, typ)
		}
	}
	return out
}

func firstEventOfType(t *testing.T, events []map[string]any, typ string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event["type"] == typ {
			return event
		}
	}
	t.Fatalf("event %s not found in %+v", typ, events)
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeExecSchema(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return path
}
