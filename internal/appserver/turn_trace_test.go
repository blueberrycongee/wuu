package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestPersistTurnTraceWritesSessionArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	kit, err := tools.New(root)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	sessionDir := filepath.Join(t.TempDir(), "session-artifacts")
	kit.SetSessionDir(sessionDir)
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		ID:        "call-old-read",
		Name:      "read_file",
		Arguments: `{"path":"target.txt"}`,
	}); err != nil {
		t.Fatalf("old read_file: %v", err)
	}
	toolRecordStart := len(kit.ToolTelemetry())
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		ID:        "call-new-read",
		Name:      "read_file",
		Arguments: `{"path":"target.txt"}`,
	}); err != nil {
		t.Fatalf("new read_file: %v", err)
	}

	startedAt := time.Now().UTC().Add(-time.Second)
	completedAt := time.Now().UTC()
	duration := int64(1000)
	srv := &Server{rt: &runtime.Session{ProviderName: "global-provider"}}
	runner := &agent.StreamRunner{Model: "gpt-test", APIModel: "gpt-test-api"}
	tracePath, err := srv.persistTurnTrace(&runtime.ThreadRuntime{Toolkit: kit}, runner, "thread-1", turnRuntimeSnapshot{
		ProviderName:   "openai",
		PermissionMode: config.PermissionModeUnconfined,
	}, Turn{
		ID:          "turn-1",
		Status:      TurnStatusCompleted,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		DurationMS:  &duration,
	}, agent.LoopResult{
		Content:      "done",
		InputTokens:  10,
		OutputTokens: 20,
	}, nil, toolRecordStart, []sessiontrace.RequestContextRecord{{
		StepIndex:         0,
		TransientMessages: 2,
		ContentBytes:      256,
		BlockKinds:        []string{"ENVIRONMENT", "TASK"},
	}}, []sessiontrace.ProviderStateRecord{{
		StepIndex:              1,
		Provider:               "openai",
		Protocol:               "responses_websocket",
		Transport:              "websocket",
		ReplayMode:             "previous_response_id",
		PreviousResponseIDUsed: true,
		ConnectionReused:       true,
		FallbackActive:         true,
		FallbackReason:         "websocket_failed_before_first_event",
		InputItems:             1,
		FullInputItems:         4,
		DeltaInputItems:        1,
	}}, []sessiontrace.CompactRecord{{
		Reason:            "proactive",
		Status:            "failed",
		TokensBefore:      252001,
		LastResponseTotal: 240000,
		PendingDelta:      12001,
		UsageAdjustment:   "provider_response",
		MessagesBefore:    42,
	}}, []sessiontrace.BarrierToolBatchRejectionRecord{{
		StepIndex:     0,
		BarrierTool:   "barrier_tool",
		SiblingTools:  []string{"run_shell"},
		ToolCallCount: 2,
	}})
	if err != nil {
		t.Fatalf("persistTurnTrace: %v", err)
	}
	if tracePath != sessiontrace.Path(sessionDir) {
		t.Fatalf("trace path = %q, want %q", tracePath, sessiontrace.Path(sessionDir))
	}

	data, err := os.ReadFile(sessiontrace.Path(sessionDir))
	if err != nil {
		t.Fatalf("read session trace: %v", err)
	}
	trace := string(data)
	for _, want := range []string{`"type":"turn"`, `"type":"context_requests"`, `"type":"provider_states"`, `"type":"compact_attempts"`, `"type":"barrier_tool_batch_rejected"`, `"type":"tool_inventory"`, `"type":"tool_records"`, `"type":"final"`, `"provider_name":"openai"`, `"model":"gpt-test"`, `"permission_mode":"unconfined"`, `"model_profile"`, `"family":"gpt"`, `"default_write_mode":"patch"`, `"name":"read_file"`, `"ENVIRONMENT"`, `"step_index":1`, `"transport":"websocket"`, `"connection_reused":true`, `"fallback_reason":"websocket_failed_before_first_event"`, `"previous_response_id_used":true`, `"tokens_before":252001`, `"last_response_total":240000`, `"pending_delta":12001`, `"usage_adjustment":"provider_response"`, `"barrier_tool":"barrier_tool"`, `"sibling_tools":["run_shell"]`, `"tool_call_count":2`} {
		if !strings.Contains(trace, want) {
			t.Fatalf("session trace missing %s:\n%s", want, trace)
		}
	}
	if strings.Contains(trace, "call-old-read") || !strings.Contains(trace, "call-new-read") {
		t.Fatalf("session trace should include only this turn's tool records:\n%s", trace)
	}
}
