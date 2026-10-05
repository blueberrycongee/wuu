package sessiontrace

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestAppendTurnWritesAgentFriendlyEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-trace.jsonl")
	err := AppendTurn(path,
		TurnRecord{
			ThreadID:            "thread-1",
			TurnID:              "turn-1",
			Status:              "completed",
			ProviderName:        "openai",
			Model:               "gpt-test",
			APIModel:            "gpt-5-codex",
			ModelProfile:        NewModelProfileRecord("openai", "gpt-test", "gpt-5-codex"),
			InputTokens:         12,
			OutputTokens:        34,
			CacheCreationTokens: 8,
			CacheReadTokens:     5,
		},
		FinalRecord{
			Status:              "completed",
			CacheCreationTokens: 8,
			CacheReadTokens:     5,
			FinalAnswerPreview:  "done API_KEY=secret-value-1234567890",
		},
		[]tools.ToolInfo{{Name: "read_file", Kind: tools.ToolKindFile, Risk: tools.ToolRiskLow, ReadOnly: true}},
		[]tools.ToolExecutionRecord{{Name: "read_file", Kind: tools.ToolKindFile, Risk: tools.ToolRiskLow, Success: true, RawOutputBytes: 100}},
		[]RequestContextRecord{{
			StepIndex:                0,
			TransientMessages:        1,
			ContentBytes:             100,
			BlockKinds:               []string{"ENVIRONMENT", "TASK"},
			BlockKindCounts:          map[string]int{"ENVIRONMENT": 1, "TASK": 1},
			BlockKindBytes:           map[string]int{"ENVIRONMENT": 40, "TASK": 60},
			SegmentLifecycleCounts:   map[string]int{"request_only": 1},
			SegmentPlacementCounts:   map[string]int{"after_history": 1},
			SegmentCachePolicyCounts: map[string]int{"volatile": 1},
			MessageCount:             4,
			SystemMessages:           1,
			HiddenMessages:           2,
			ToolCount:                3,
			StablePrefix:             2,
			TurnPrefix:               3,
			DynamicBytes:             100,
			SystemBytes:              2048,
			StablePrefixBytes:        2300,
			TurnPrefixBytes:          2400,
			MessageBytes:             2600,
			ToolSchemaBytes:          4096,
			LoadableToolCount:        2,
			LoadableToolSchemaBytes:  1024,
			LoadableToolSurfaceHash:  "loadable-hash",
			SystemHash:               "sys-hash",
			StablePrefixHash:         "prefix-hash",
			TurnPrefixHash:           "turn-prefix-hash",
			ToolSurfaceHash:          "tool-hash",
			PromptCacheKey:           "thread-cache-key",
			InputTokens:              12,
			OutputTokens:             4,
			CacheCreationTokens:      6,
			CacheReadTokens:          2,
			SystemSections: []SystemSectionRecord{{
				Key:    "memory",
				Static: false,
				Bytes:  1024,
				Hash:   "memory-hash",
			}},
		}},
		[]ProviderStateRecord{{
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
			FullInputItems:         4,
			DeltaInputItems:        1,
		}},
		[]CompactRecord{{
			Reason:            "proactive",
			Status:            "failed",
			TokensBefore:      1234,
			LastResponseTotal: 1200,
			PendingDelta:      34,
			UsageAdjustment:   "request_shape_tail_rebase",
			MessagesBefore:    10,
			Error:             "compact failed API_KEY=secret-value-compact",
		}},
		[]BarrierToolBatchRejectionRecord{{
			StepIndex:     0,
			BarrierTool:   "barrier_tool",
			SiblingTools:  []string{"run_shell"},
			ToolCallCount: 2,
		}},
	)
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}

	events := readTraceEvents(t, path)
	if len(events) != 8 {
		t.Fatalf("expected 8 events, got %d: %+v", len(events), events)
	}
	wantTypes := []string{"turn", "context_requests", "provider_states", "compact_attempts", "barrier_tool_batch_rejected", "tool_inventory", "tool_records", "final"}
	for i, want := range wantTypes {
		if events[i].Type != want || events[i].ThreadID != "thread-1" || events[i].TurnID != "turn-1" {
			t.Fatalf("unexpected event %d: %+v", i, events[i])
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if !strings.Contains(string(raw), `"model_profile"`) ||
		!strings.Contains(string(raw), `"family":"codex"`) ||
		!strings.Contains(string(raw), `"default_write_mode":"exact_edit"`) {
		t.Fatalf("trace should include model profile metadata:\n%s", raw)
	}
	if strings.Contains(string(raw), "secret-value") || !strings.Contains(string(raw), "[REDACTED]") {
		t.Fatalf("trace should redact secret-like final previews:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"cache_creation_tokens":8`) || !strings.Contains(string(raw), `"cache_read_tokens":5`) {
		t.Fatalf("trace should include prompt cache usage:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"last_response_total":1200`) ||
		!strings.Contains(string(raw), `"pending_delta":34`) ||
		!strings.Contains(string(raw), `"usage_adjustment":"request_shape_tail_rebase"`) {
		t.Fatalf("trace should explain compact usage baseline:\n%s", raw)
	}
	for _, want := range []string{
		`"message_count":4`,
		`"system_messages":1`,
		`"hidden_messages":2`,
		`"tool_count":3`,
		`"stable_prefix":2`,
		`"turn_prefix":3`,
		`"dynamic_context_bytes":100`,
		`"block_kind_counts":{"ENVIRONMENT":1,"TASK":1}`,
		`"block_kind_bytes":{"ENVIRONMENT":40,"TASK":60}`,
		`"segment_lifecycle_counts":{"request_only":1}`,
		`"segment_placement_counts":{"after_history":1}`,
		`"segment_cache_policy_counts":{"volatile":1}`,
		`"system_bytes":2048`,
		`"stable_prefix_bytes":2300`,
		`"turn_prefix_bytes":2400`,
		`"message_bytes":2600`,
		`"tool_schema_bytes":4096`,
		`"loadable_tool_count":2`,
		`"loadable_tool_schema_bytes":1024`,
		`"loadable_tool_surface_hash":"loadable-hash"`,
		`"system_hash":"sys-hash"`,
		`"stable_prefix_hash":"prefix-hash"`,
		`"turn_prefix_hash":"turn-prefix-hash"`,
		`"tool_surface_hash":"tool-hash"`,
		`"prompt_cache_key":"thread-cache-key"`,
		`"input_tokens":12`,
		`"output_tokens":4`,
		`"cache_creation_tokens":6`,
		`"cache_read_tokens":2`,
		`"system_sections":[{"key":"memory","static":false,"bytes":1024,"hash":"memory-hash"}]`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("trace should include request shape field %s:\n%s", want, raw)
		}
	}
	for _, want := range []string{
		`"type":"provider_states"`,
		`"step_index":1`,
		`"protocol":"responses_websocket"`,
		`"transport":"websocket"`,
		`"replay_mode":"previous_response_id"`,
		`"previous_response_id_used":true`,
		`"connection_reused":true`,
		`"fallback_active":true`,
		`"fallback_reason":"websocket_failed_before_first_event"`,
		`"fallback_pin_status":"created"`,
		`"fallback_retry_after_ms":29950`,
		`"fallback_ttl_ms":30000`,
		`"full_input_items":4`,
		`"delta_input_items":1`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("trace should include provider state field %s:\n%s", want, raw)
		}
	}
	for _, want := range []string{
		`"type":"compact_attempts"`,
		`"reason":"proactive"`,
		`"status":"failed"`,
		`"tokens_before":1234`,
		`"messages_before":10`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("trace should include compact attempt field %s:\n%s", want, raw)
		}
	}
}

func TestReplayTraceSummarizesSessionEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-trace.jsonl")
	if err := AppendTurn(path,
		TurnRecord{ThreadID: "thread-1", TurnID: "turn-1", Status: "completed", ProviderName: "openai", Model: "gpt-test", APIModel: "gpt-5-codex", ModelProfile: NewModelProfileRecord("openai", "gpt-test", "gpt-5-codex")},
		FinalRecord{Status: "completed", FinalAnswerPreview: "done"},
		[]tools.ToolInfo{{Name: "grep", Kind: tools.ToolKindSearch, Risk: tools.ToolRiskLow, ReadOnly: true}},
		[]tools.ToolExecutionRecord{{
			Name:            "grep",
			ArgumentsSHA256: strings.Repeat("b", 64),
			Kind:            tools.ToolKindSearch,
			Risk:            tools.ToolRiskLow,
			PolicyAction:    tools.ToolPolicyAllow,
			Success:         true,
		}, {
			Name:            "grep",
			ArgumentsSHA256: strings.Repeat("b", 64),
			Kind:            tools.ToolKindSearch,
			Risk:            tools.ToolRiskLow,
			PolicyAction:    tools.ToolPolicyAllow,
			Success:         true,
		}, {
			Name:            "run_shell",
			ResultAction:    "restore",
			CallID:          "call-shell",
			Kind:            tools.ToolKindShell,
			Risk:            tools.ToolRiskHigh,
			PolicyAction:    tools.ToolPolicyDeny,
			PolicyReason:    "workspace boundary",
			ErrorKind:       "boundary_denied",
			ArgumentsSHA256: strings.Repeat("c", 64),
			RevisionBefore:  "rev-before",
			Success:         false,
		}, {
			Name:            "start_process",
			CallID:          "call-process",
			Kind:            tools.ToolKindProcess,
			Risk:            tools.ToolRiskHigh,
			PolicyAction:    tools.ToolPolicyDeny,
			PolicyReason:    "workspace boundary",
			ErrorKind:       "boundary_denied",
			ArgumentsSHA256: strings.Repeat("d", 64),
			RevisionBefore:  "rev-before",
			Success:         false,
		}},
		[]RequestContextRecord{{
			StepIndex:               0,
			TransientMessages:       2,
			ContentBytes:            240,
			BlockKinds:              []string{"ENVIRONMENT", "TOOL_POLICY"},
			ToolCount:               9,
			DynamicBytes:            120,
			SystemBytes:             1000,
			StablePrefixBytes:       900,
			TurnPrefixBytes:         100,
			MessageBytes:            300,
			ToolSchemaBytes:         400,
			LoadableToolCount:       3,
			LoadableToolSchemaBytes: 150,
			SystemHash:              "sys-hash",
			StablePrefixHash:        "stable-hash",
			TurnPrefixHash:          "turn-hash",
			ToolSurfaceHash:         "tool-hash",
			LoadableToolSurfaceHash: "loadable-hash",
			PromptCacheKey:          "prompt-cache-key",
			InputTokens:             10,
			OutputTokens:            2,
			CacheCreationTokens:     4,
			CacheReadTokens:         30,
		}},
		[]ProviderStateRecord{{
			StepIndex:              0,
			Provider:               "openai",
			Protocol:               "responses_websocket",
			ReplayMode:             "full_request",
			PreviousResponseIDUsed: false,
			InputItems:             4,
			FullInputItems:         4,
		}},
		[]CompactRecord{{
			Reason:         "proactive",
			Status:         "succeeded",
			TokensBefore:   2000,
			MessagesBefore: 8,
			MessagesAfter:  3,
		}},
	); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}

	summary, err := ReplayTrace(path)
	if err != nil {
		t.Fatalf("ReplayTrace: %v", err)
	}
	if summary.Mode != "session_trace_replay" || !summary.Complete || summary.EventCount != 7 {
		t.Fatalf("unexpected replay summary: %+v", summary)
	}
	if len(summary.CompactAttempts) != 1 ||
		summary.CompactAttempts[0].Status != "succeeded" ||
		summary.CompactAttempts[0].MessagesAfter != 3 {
		t.Fatalf("compact attempts missing from replay: %+v", summary.CompactAttempts)
	}
	if summary.LatestTurn == nil || summary.LatestTurn.ThreadID != "thread-1" || summary.LatestTurn.Model != "gpt-test" {
		t.Fatalf("latest turn missing: %+v", summary.LatestTurn)
	}
	if summary.LatestTurn.ModelProfile == nil ||
		summary.LatestTurn.ModelProfile.Family != "codex" ||
		summary.LatestTurn.ModelProfile.DefaultWriteMode != "exact_edit" {
		t.Fatalf("model profile missing from replay: %+v", summary.LatestTurn.ModelProfile)
	}
	if len(summary.ToolInventory) != 1 || summary.ToolInventory[0].Name != "grep" {
		t.Fatalf("tool inventory missing: %+v", summary.ToolInventory)
	}
	if len(summary.ContextRequests) != 1 ||
		summary.ContextRequests[0].TransientMessages != 2 ||
		!containsString(summary.ContextBlockKinds, "TOOL_POLICY") {
		t.Fatalf("context requests missing: %+v", summary)
	}
	if len(summary.ProviderStates) != 1 ||
		summary.ProviderStates[0].StepIndex != 0 ||
		summary.ProviderStates[0].Protocol != "responses_websocket" ||
		summary.ProviderStates[0].ReplayMode != "full_request" ||
		summary.ProviderStates[0].FullInputItems != 4 {
		t.Fatalf("provider states missing: %+v", summary.ProviderStates)
	}
	if len(summary.RequestSteps) != 1 {
		t.Fatalf("request steps missing: %+v", summary.RequestSteps)
	}
	step := summary.RequestSteps[0]
	if step.TurnID != "turn-1" ||
		step.StepIndex != 0 ||
		step.InputTokens != 10 ||
		step.OutputTokens != 2 ||
		step.CacheCreationTokens != 4 ||
		step.CacheReadTokens != 30 ||
		step.CacheHitRate != 0.75 ||
		step.DynamicBytes != 120 ||
		step.SystemBytes != 1000 ||
		step.ToolSchemaBytes != 400 ||
		step.LoadableToolSchemaBytes != 150 ||
		step.StablePrefixHash != "stable-hash" ||
		step.ToolSurfaceHash != "tool-hash" ||
		step.LoadableToolSurfaceHash != "loadable-hash" ||
		step.PromptCacheKey != "prompt-cache-key" ||
		step.ReplayMode != "full_request" ||
		step.Protocol != "responses_websocket" ||
		step.PreviousResponseIDUsed == nil ||
		*step.PreviousResponseIDUsed ||
		step.FullInputItems != 4 {
		t.Fatalf("request step summary missing joined detail: %+v", step)
	}
	if len(summary.ToolNames) != 4 || summary.ToolNames[0] != "grep" || summary.ToolNames[1] != "grep" || summary.ToolNames[2] != "run_shell" || summary.ToolNames[3] != "start_process" {
		t.Fatalf("tool records missing: %+v", summary.ToolNames)
	}
	if summary.ToolSummary == nil || summary.ToolSummary.Total != 4 || summary.ToolSummary.Succeeded != 2 || summary.ToolSummary.Failed != 2 {
		t.Fatalf("tool summary missing: %+v", summary.ToolSummary)
	}
	if summary.ToolSummary.ByKind[string(tools.ToolKindShell)] != 1 ||
		summary.ToolSummary.ByKind[string(tools.ToolKindProcess)] != 1 ||
		summary.ToolSummary.ByRisk[string(tools.ToolRiskHigh)] != 2 ||
		summary.ToolSummary.ByPolicyAction[string(tools.ToolPolicyDeny)] != 2 ||
		summary.ToolSummary.ByResultAction["run_shell:restore"] != 1 ||
		summary.ToolSummary.ByErrorKind["boundary_denied"] != 2 {
		t.Fatalf("tool summary dimensions missing: %+v", summary.ToolSummary)
	}
	if len(summary.ToolSummary.PolicyBlocks) != 2 {
		t.Fatalf("tool summary missing policy blocks: %+v", summary.ToolSummary.PolicyBlocks)
	}
	if block := summary.ToolSummary.PolicyBlocks[0]; block.ToolName != "run_shell" ||
		block.CallID != "call-shell" ||
		block.PolicyAction != string(tools.ToolPolicyDeny) ||
		block.PolicyReason != "workspace boundary" ||
		block.ErrorKind != "boundary_denied" ||
		block.ArgumentsSHA256 != strings.Repeat("c", 64) ||
		block.RevisionBefore != "rev-before" ||
		!strings.Contains(block.ModelNextAction, "workspace boundary") {
		t.Fatalf("deny policy block missing replay detail: %+v", block)
	}
	if block := summary.ToolSummary.PolicyBlocks[1]; block.ToolName != "start_process" ||
		block.PolicyAction != string(tools.ToolPolicyDeny) ||
		block.ErrorKind != "boundary_denied" ||
		!strings.Contains(block.ModelNextAction, "workspace boundary") {
		t.Fatalf("boundary policy block missing replay detail: %+v", block)
	}
	if len(summary.ToolSummary.RepeatedArguments) != 1 ||
		summary.ToolSummary.RepeatedArguments[0].ToolName != "grep" ||
		summary.ToolSummary.RepeatedArguments[0].ArgumentsSHA256 != strings.Repeat("b", 64) ||
		summary.ToolSummary.RepeatedArguments[0].Count != 2 {
		t.Fatalf("tool summary missing repeated arguments: %+v", summary.ToolSummary.RepeatedArguments)
	}
	if summary.Final == nil || summary.Final.Status != "completed" || summary.Final.FinalAnswerPreview != "done" {
		t.Fatalf("final summary missing: %+v", summary.Final)
	}
}

func TestReplayTraceAttributesToolOutputsToFollowingRequestStep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-trace.jsonl")
	now := time.Now()
	step0 := 0
	if err := AppendTurn(path,
		TurnRecord{
			ThreadID:     "thread-1",
			TurnID:       "turn-1",
			Status:       "completed",
			ProviderName: "openai",
			Model:        "gpt-test",
			StartedAt:    &now,
		},
		FinalRecord{Status: "completed"},
		nil,
		[]tools.ToolExecutionRecord{{
			Name:                "grep",
			StepIndex:           &step0,
			CallID:              "call-grep",
			Kind:                tools.ToolKindSearch,
			ResultAction:        "search",
			Success:             true,
			RawOutputBytes:      12000,
			ReturnedOutputBytes: 8000,
			ResultBudgeted:      true,
		}},
		[]RequestContextRecord{{
			StepIndex:   0,
			InputTokens: 100,
		}, {
			StepIndex:       1,
			InputTokens:     2200,
			CacheReadTokens: 4000,
		}},
		nil,
		nil,
	); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}

	summary, err := ReplayTrace(path)
	if err != nil {
		t.Fatalf("ReplayTrace: %v", err)
	}
	step := findRequestStep(summary.RequestSteps, "turn-1", 1)
	if step == nil {
		t.Fatalf("missing request step 1: %+v", summary.RequestSteps)
	}
	if step.PrecedingToolResultCount != 1 ||
		step.PrecedingToolResultRawBytes != 12000 ||
		step.PrecedingToolResultReturnedBytes != 8000 ||
		step.PrecedingToolResultBudgetedCount != 1 ||
		step.PrecedingToolResultMaxReturnedBytes != 8000 {
		t.Fatalf("missing preceding tool result attribution: %+v", step)
	}
}

func readTraceEvents(t *testing.T, path string) []Event {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode trace event: %v", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan trace: %v", err)
	}
	return events
}

func findRequestStep(steps []RequestStepSummary, turnID string, stepIndex int) *RequestStepSummary {
	for i := range steps {
		if steps[i].TurnID == turnID && steps[i].StepIndex == stepIndex {
			return &steps[i]
		}
	}
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
