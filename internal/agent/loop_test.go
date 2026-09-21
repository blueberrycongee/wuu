package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
)

// fakeStep is a programmable Step implementation for loop tests.
// Each Execute call pops the next entry from results / errs.
type fakeStep struct {
	results []StepResult
	errs    []error // optional, indexed parallel to results
	calls   []providers.ChatRequest
	idx     int
}

func (f *fakeStep) Execute(_ context.Context, req providers.ChatRequest) (StepResult, error) {
	f.calls = append(f.calls, req)
	if f.idx >= len(f.results) {
		return StepResult{}, errors.New("fakeStep: unexpected extra call")
	}
	r := f.results[f.idx]
	var err error
	if f.idx < len(f.errs) {
		err = f.errs[f.idx]
	}
	f.idx++
	return r, err
}

func TestRunToolLoopPreservesVisiblePartialAssistantOnStepError(t *testing.T) {
	streamErr := errors.New("stream interrupted")
	step := &fakeStep{
		results: []StepResult{{
			Content:           "partial answer",
			Phase:             providers.MessagePhaseFinalAnswer,
			ProviderItemID:    "incomplete-provider-item",
			ProviderItemModel: "provider-model",
			ReasoningContent:  "incomplete reasoning",
			ToolCalls: []providers.ToolCall{{
				ID:        "incomplete-call",
				Name:      "read_file",
				Arguments: `{"path":"README.md"}`,
			}},
		}},
		errs: []error{streamErr},
	}

	result, err := RunToolLoop(
		context.Background(),
		[]providers.ChatMessage{{Role: "user", Content: "hello"}},
		LoopConfig{Model: "test-model"},
		step,
	)
	if !errors.Is(err, streamErr) {
		t.Fatalf("error = %v, want %v", err, streamErr)
	}
	visible := visibleMessagesForTest(result.NewMessages)
	if len(visible) != 1 {
		t.Fatalf("new messages = %+v, want one partial assistant message", result.NewMessages)
	}
	partial := visible[0]
	if partial.Role != "assistant" || partial.Content != "partial answer" || partial.Phase != providers.MessagePhaseFinalAnswer {
		t.Fatalf("partial assistant = %+v", partial)
	}
	if partial.ProviderItemID != "" || partial.ProviderItemModel != "" || partial.ReasoningContent != "" || len(partial.ToolCalls) != 0 {
		t.Fatalf("partial assistant retained incomplete provider state: %+v", partial)
	}
}

func TestRunToolLoopBeforeRequestTransformsProviderRequest(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "done"}}}
	tools := &fakeLoopTools{defs: []providers.ToolDefinition{{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: map[string]any{"type": "object"},
	}}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hello"}}, LoopConfig{
		Model: "before",
		Tools: tools,
		BeforeRequest: func(_ context.Context, req *providers.ChatRequest) error {
			req.Model = "after"
			req.Temperature = 0.25
			req.Messages = append([]providers.ChatMessage{{Role: "system", Content: "plugin system"}}, req.Messages...)
			req.Tools[0].Description = "Plugin description"
			return nil
		},
	}, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 1 {
		t.Fatalf("calls = %d", len(step.calls))
	}
	request := step.calls[0]
	if request.Model != "after" || request.Temperature != 0.25 {
		t.Fatalf("request model/temperature = %q/%v", request.Model, request.Temperature)
	}
	if request.Messages[0].Role != "system" || request.Messages[0].Content != "plugin system" {
		t.Fatalf("messages = %+v", request.Messages)
	}
	if request.Tools[0].Description != "Plugin description" {
		t.Fatalf("tools = %+v", request.Tools)
	}
}

func TestRunToolLoopRejectsInvalidPluginRequestBeforeProvider(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "unused"}}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hello"}}, LoopConfig{
		Model: "model",
		BeforeRequest: func(_ context.Context, req *providers.ChatRequest) error {
			req.Messages = append(req.Messages, providers.ChatMessage{Role: "system", Content: "too late"})
			return nil
		},
	}, step)
	if err == nil || !strings.Contains(err.Error(), "invalid message sequence") {
		t.Fatalf("err = %v", err)
	}
	if len(step.calls) != 0 {
		t.Fatalf("provider called %d times", len(step.calls))
	}
}

func TestRunToolLoopRejectsForcedToolRemovedByPlugin(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "unused"}}}
	tools := &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "required", InputSchema: map[string]any{"type": "object"}}}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hello"}}, LoopConfig{
		Model:              "model",
		Tools:              tools,
		ForceToolFirstStep: "required",
		BeforeRequest: func(_ context.Context, req *providers.ChatRequest) error {
			req.Tools = nil
			return nil
		},
	}, step)
	if err == nil || !strings.Contains(err.Error(), "forces unavailable tool") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunToolLoopAllowsPreexistingUnavailableForcedTool(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "done"}}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hello"}}, LoopConfig{
		Model:              "model",
		ForceToolFirstStep: "required",
		BeforeRequest: func(_ context.Context, req *providers.ChatRequest) error {
			req.Temperature = 0.5
			return nil
		},
	}, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 1 {
		t.Fatalf("calls = %d", len(step.calls))
	}
	if step.calls[0].ForceToolName != "required" {
		t.Fatalf("request force tool = %q", step.calls[0].ForceToolName)
	}
}

// fakeLoopTools is a no-op ToolExecutor that records every call.
type fakeLoopTools struct {
	mu         sync.Mutex
	defs       []providers.ToolDefinition
	results    map[string]string // call.ID → JSON result
	discovered map[string][]providers.LoadableToolDefinition
	calls      []providers.ToolCall
	steps      []int
	err        error
}

func (f *fakeLoopTools) Definitions() []providers.ToolDefinition { return f.defs }
func (f *fakeLoopTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if stepIndex, ok := toolctx.StepIndex(ctx); ok {
		f.steps = append(f.steps, stepIndex)
	}
	if f.err != nil {
		return "", f.err
	}
	if r, ok := f.results[call.ID]; ok {
		return r, nil
	}
	return `{"ok":true}`, nil
}

func (f *fakeLoopTools) DiscoveredTools(call providers.ToolCall) []providers.LoadableToolDefinition {
	if f == nil || len(f.discovered) == 0 {
		return nil
	}
	return providers.CloneLoadableToolDefinitions(f.discovered[call.ID])
}

func (f *fakeLoopTools) recordedCalls() []providers.ToolCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]providers.ToolCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeLoopTools) recordedSteps() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.steps))
	copy(out, f.steps)
	return out
}

type contextLoopTools struct {
	defs []providers.ToolDefinition

	calls []providers.ToolCall
	last  string
}

func (f *contextLoopTools) Definitions() []providers.ToolDefinition { return f.defs }
func (f *contextLoopTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	f.calls = append(f.calls, call)
	f.last = "context for " + call.ID
	return `{"ok":"` + call.ID + `"}`, nil
}
func (f *contextLoopTools) LastAdditionalContext() string { return f.last }

type delayedConcurrentTools struct {
	call2Done chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	completed []string
}

func newDelayedConcurrentTools() *delayedConcurrentTools {
	return &delayedConcurrentTools{call2Done: make(chan struct{})}
}

func (f *delayedConcurrentTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read_file"}}
}

func (f *delayedConcurrentTools) ToolMetadata(_ providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true
}

func (f *delayedConcurrentTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	switch call.ID {
	case "call_1":
		select {
		case <-f.call2Done:
		case <-time.After(time.Second):
			return "", errors.New("call_1 timed out waiting for call_2")
		}
		f.recordCompleted(call.ID)
	case "call_2":
		f.recordCompleted(call.ID)
		f.closeOnce.Do(func() { close(f.call2Done) })
	default:
		f.recordCompleted(call.ID)
	}
	return `{"ok":"` + call.ID + `"}`, nil
}

func (f *delayedConcurrentTools) recordCompleted(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, id)
}

func (f *delayedConcurrentTools) completedOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.completed))
	copy(out, f.completed)
	return out
}

type argumentAwareMetadataTools struct{}

func (argumentAwareMetadataTools) Definitions() []providers.ToolDefinition { return nil }
func (argumentAwareMetadataTools) Execute(context.Context, providers.ToolCall) (string, error) {
	return `{"ok":true}`, nil
}
func (argumentAwareMetadataTools) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	if strings.Contains(call.Arguments, "safe") {
		return ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true
	}
	return ToolMetadata{ReadOnly: false, ConcurrencySafe: false}, true
}

func userMsg(content string) providers.ChatMessage {
	return providers.ChatMessage{Role: "user", Content: content}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsRoleContent(messages []providers.ChatMessage, role, content string) bool {
	for _, msg := range messages {
		if msg.Role == role && msg.Content == content {
			return true
		}
	}
	return false
}

func hiddenReminderForTest(block wuucontext.Block, ordinal int) providers.ChatMessage {
	rendered := wuucontext.CompileBlocks([]wuucontext.Block{block})
	return providers.ChatMessage{
		Role:    "user",
		Name:    wuucontext.SystemReminderBlockMessageName(block, ordinal),
		Content: "<system-reminder>\n" + rendered + "\n</system-reminder>",
		Hidden:  true,
	}
}

func countMessagesContaining(messages []providers.ChatMessage, needle string) int {
	count := 0
	for _, msg := range messages {
		if strings.Contains(msg.Content, needle) {
			count++
		}
	}
	return count
}

func TestPartitionToolCallsUsesCallArguments(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "safe_1", Name: "run_shell", Arguments: `{"kind":"safe"}`},
		{ID: "unsafe", Name: "run_shell", Arguments: `{"kind":"write"}`},
		{ID: "safe_2", Name: "run_shell", Arguments: `{"kind":"safe"}`},
	}

	batches := partitionToolCalls(argumentAwareMetadataTools{}, calls)
	if len(batches) != 3 {
		t.Fatalf("expected 3 batches, got %+v", batches)
	}
	if !batches[0].concurrent || batches[0].calls[0].ID != "safe_1" {
		t.Fatalf("first call should be concurrent based on arguments, got %+v", batches[0])
	}
	if batches[1].concurrent || batches[1].calls[0].ID != "unsafe" {
		t.Fatalf("second call should be serial based on arguments, got %+v", batches[1])
	}
	if !batches[2].concurrent || batches[2].calls[0].ID != "safe_2" {
		t.Fatalf("third call should be concurrent based on arguments, got %+v", batches[2])
	}
}

func TestRunToolLoop_SimpleAnswer(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "hello back"}}}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, LoopConfig{Model: "m"}, step)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if res.Content != "hello back" {
		t.Fatalf("got content %q", res.Content)
	}
	visible := visibleMessagesForTest(res.NewMessages)
	if len(visible) != 1 || visible[0].Role != "assistant" {
		t.Fatalf("unexpected new messages: %+v", res.NewMessages)
	}
}

func TestRunToolLoop_ForwardsNativeDeferredToolDiscovery(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, LoopConfig{
		Model:                       "m",
		NativeDeferredToolDiscovery: true,
	}, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected one call, got %d", len(step.calls))
	}
	if !step.calls[0].NativeDeferredToolDiscovery {
		t.Fatal("expected ChatRequest to carry NativeDeferredToolDiscovery")
	}
}

func TestRunToolLoop_BuildsCacheHintFromHistory(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "latest"},
	}
	var contexts []RequestContextInfo
	_, err := RunToolLoop(context.Background(), history, LoopConfig{
		Model:          "m",
		PromptCacheKey: "thread-cache-key",
		Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{
			Name:        "read_file",
			Description: "Read files",
			InputSchema: map[string]any{
				"type": "object",
			},
			CacheStable: true,
		}}},
		OnRequestContext: func(info RequestContextInfo) {
			contexts = append(contexts, info)
		},
	}, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected one call, got %d", len(step.calls))
	}
	hint := step.calls[0].CacheHint
	if hint == nil {
		t.Fatal("expected cache hint")
	}
	if !hint.StableSystem {
		t.Fatal("expected StableSystem=true")
	}
	if hint.StablePrefixMessages != 2 {
		t.Fatalf("expected stable prefix size 2, got %d", hint.StablePrefixMessages)
	}
	if hint.TurnPrefixMessages != 3 {
		t.Fatalf("expected turn prefix through latest user, got %d", hint.TurnPrefixMessages)
	}
	if hint.PromptCacheKey != "thread-cache-key" {
		t.Fatalf("expected thread prompt cache key, got %q", hint.PromptCacheKey)
	}
	if len(contexts) != 1 {
		t.Fatalf("expected one request shape observation, got %+v", contexts)
	}
	shape := contexts[0]
	if shape.StepIndex != 0 || shape.MessageCount != 4 || shape.SystemMessages != 1 || shape.StablePrefix != 2 || shape.TurnPrefix != 3 || shape.ToolCount != 1 {
		t.Fatalf("unexpected request shape: %+v", shape)
	}
	if shape.TransientMessages != 0 || shape.ContentBytes != 0 || shape.DynamicBytes != 0 || shape.HiddenMessages != 0 {
		t.Fatalf("request shape should not synthesize default dynamic context: %+v", shape)
	}
	if len(shape.SegmentLifecycleCounts) != 0 ||
		len(shape.SegmentPlacementCounts) != 0 ||
		len(shape.SegmentCachePolicyCounts) != 0 {
		t.Fatalf("request shape should not report synthetic context segments: %+v", shape)
	}
	for _, unwanted := range []string{"TASK", "CONSTRAINT_LEDGER"} {
		if containsString(shape.BlockKinds, unwanted) {
			t.Fatalf("request shape should not include dynamic block kind %s: %+v", unwanted, shape)
		}
	}
	if shape.SystemHash == "" || shape.StablePrefixHash == "" || shape.TurnPrefixHash == "" || shape.ToolSurfaceHash == "" {
		t.Fatalf("request shape missing hashes: %+v", shape)
	}
	if shape.SystemBytes == 0 || shape.StablePrefixBytes == 0 || shape.TurnPrefixBytes == 0 || shape.MessageBytes == 0 || shape.ToolSchemaBytes == 0 {
		t.Fatalf("request shape missing byte metrics: %+v", shape)
	}
	if shape.PromptCacheKey != "thread-cache-key" {
		t.Fatalf("request shape prompt cache key = %q", shape.PromptCacheKey)
	}
}

func TestRunToolLoop_RequestOnlyContextDoesNotChangeCachePrefix(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "latest"},
	}
	tools := &fakeLoopTools{defs: []providers.ToolDefinition{{
		Name:        "read_file",
		Description: "Read files",
		InputSchema: map[string]any{
			"type": "object",
		},
		CacheStable: true,
	}}}
	run := func(withRequestOnly bool) (RequestContextInfo, providers.ChatRequest) {
		t.Helper()
		step := &fakeStep{results: []StepResult{{Content: "ok"}}}
		var contexts []RequestContextInfo
		cfg := LoopConfig{
			Model:          "m",
			PromptCacheKey: "thread-cache-key",
			Tools:          tools,
			OnRequestContext: func(info RequestContextInfo) {
				contexts = append(contexts, info)
			},
		}
		if withRequestOnly {
			block := wuucontext.Block{
				Kind:    wuucontext.BlockEnvironment,
				Title:   "Runtime environment",
				Source:  "runtime.snapshot",
				Content: "# Environment\n- CWD: /tmp/project\n- Git: dirty",
			}
			cfg.BeforeRequestContext = func() []ContextSegment {
				return RequestOnlyContextBlocks([]wuucontext.Block{block})
			}
		}
		if _, err := RunToolLoop(context.Background(), history, cfg, step); err != nil {
			t.Fatal(err)
		}
		if len(contexts) != 1 {
			t.Fatalf("expected one request context observation, got %+v", contexts)
		}
		if len(step.calls) != 1 {
			t.Fatalf("expected one provider call, got %d", len(step.calls))
		}
		return contexts[0], step.calls[0]
	}

	baseline, baselineReq := run(false)
	dynamic, dynamicReq := run(true)

	if dynamic.StablePrefix != baseline.StablePrefix || dynamic.TurnPrefix != baseline.TurnPrefix {
		t.Fatalf("request-only context should not move cache prefixes: baseline=%+v dynamic=%+v", baseline, dynamic)
	}
	if dynamic.StablePrefixHash != baseline.StablePrefixHash {
		t.Fatalf("stable prefix hash changed after request-only context: %q -> %q", baseline.StablePrefixHash, dynamic.StablePrefixHash)
	}
	if dynamic.TurnPrefixHash != baseline.TurnPrefixHash {
		t.Fatalf("turn prefix hash changed after request-only context: %q -> %q", baseline.TurnPrefixHash, dynamic.TurnPrefixHash)
	}
	if dynamic.SystemHash != baseline.SystemHash {
		t.Fatalf("system hash changed after request-only context: %q -> %q", baseline.SystemHash, dynamic.SystemHash)
	}
	if dynamic.ToolSurfaceHash != baseline.ToolSurfaceHash {
		t.Fatalf("tool surface hash changed after request-only context: %q -> %q", baseline.ToolSurfaceHash, dynamic.ToolSurfaceHash)
	}
	if dynamic.PromptCacheKey != baseline.PromptCacheKey || dynamic.PromptCacheKey != "thread-cache-key" {
		t.Fatalf("prompt cache key changed: baseline=%q dynamic=%q", baseline.PromptCacheKey, dynamic.PromptCacheKey)
	}
	if dynamic.TransientMessages != 1 || dynamic.HiddenMessages != 1 || dynamic.DynamicBytes == 0 {
		t.Fatalf("request-only context should still be visible in telemetry as transient: %+v", dynamic)
	}
	if !containsString(dynamic.BlockKinds, string(wuucontext.BlockEnvironment)) {
		t.Fatalf("request-only typed block should be reported in telemetry: %+v", dynamic)
	}
	if dynamic.MessageCount <= baseline.MessageCount || dynamic.MessageBytes <= baseline.MessageBytes {
		t.Fatalf("dynamic request should be larger without changing cache prefix: baseline=%+v dynamic=%+v", baseline, dynamic)
	}
	if baselineReq.CacheHint == nil || dynamicReq.CacheHint == nil {
		t.Fatal("expected cache hints")
	}
	if dynamicReq.CacheHint.StablePrefixMessages != baselineReq.CacheHint.StablePrefixMessages ||
		dynamicReq.CacheHint.TurnPrefixMessages != baselineReq.CacheHint.TurnPrefixMessages ||
		dynamicReq.CacheHint.PromptCacheKey != baselineReq.CacheHint.PromptCacheKey {
		t.Fatalf("cache hint changed after request-only context: baseline=%+v dynamic=%+v", baselineReq.CacheHint, dynamicReq.CacheHint)
	}
}

func TestRunToolLoop_CompactIgnoresRequestOnlyContext(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	step := &fakeStep{
		results: []StepResult{{}, {Content: "ok"}},
		errs:    []error{overflow, nil},
	}
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("old request ", 100)},
		{Role: "assistant", Content: strings.Repeat("old answer ", 100)},
		{Role: "user", Content: "latest request"},
	}
	pluginBlock := wuucontext.Block{
		Kind:    wuucontext.BlockKind("PLUGIN_CONTINUATION"),
		Title:   "Plugin continuation",
		Source:  "plugin.test",
		Content: "Opaque continuation context",
	}
	var compactInput []providers.ChatMessage

	res, err := RunToolLoop(context.Background(), history, LoopConfig{
		Model: "m",
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextBlocks([]wuucontext.Block{pluginBlock})
		},
		Compact: func(_ context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactInput = providers.CloneChatMessages(messages)
			return []providers.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "system", Content: compact.BuildSummaryContent("older history")},
				{Role: "user", Content: "latest request"},
			}, nil
		},
	}, step)
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if res.Content != "ok" {
		t.Fatalf("unexpected content %q", res.Content)
	}
	if got := countMessagesContaining(step.calls[0].Messages, "Opaque continuation context"); got != 1 {
		t.Fatalf("first request should include request-only plugin context once, got %d in %+v", got, step.calls[0].Messages)
	}
	if got := countMessagesContaining(compactInput, "Opaque continuation context"); got != 0 {
		t.Fatalf("compact input must not include request-only plugin context, got %d in %+v", got, compactInput)
	}
	if got := countMessagesContaining(step.calls[1].Messages, "Opaque continuation context"); got != 1 {
		t.Fatalf("retry request should re-add request-only plugin context once, got %d in %+v", got, step.calls[1].Messages)
	}
	if got := countMessagesContaining(res.NewMessages, "Opaque continuation context"); got != 0 {
		t.Fatalf("request-only plugin context must not enter returned history, got %d in %+v", got, res.NewMessages)
	}
}

func TestRunToolLoop_ForceInitialCompactRunsBelowThreshold(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("old request ", 100)},
		{Role: "assistant", Content: strings.Repeat("old answer ", 100)},
		{Role: "user", Content: "/compact"},
	}
	compactCalls := 0
	var infos []CompactInfo
	res, err := RunToolLoop(context.Background(), history, LoopConfig{
		Model: "m",
		// No MaxContextTokens: the proactive threshold is zero, so only
		// the forced pass can trigger compaction.
		ForceInitialCompact: true,
		Compact: func(_ context.Context, _ []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalls++
			return []providers.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "system", Content: compact.BuildSummaryContent("older history")},
				{Role: "user", Content: "/compact"},
			}, nil
		},
		OnCompact: func(info CompactInfo) { infos = append(infos, info) },
	}, step)
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if compactCalls != 1 {
		t.Fatalf("compact calls = %d, want 1", compactCalls)
	}
	if len(infos) != 1 || infos[0].Reason != CompactReasonManual {
		t.Fatalf("OnCompact infos = %+v, want one manual entry", infos)
	}
	if !res.HistoryRewritten {
		t.Fatal("forced compact should mark history rewritten")
	}
	if got := countMessagesContaining(step.calls[0].Messages, "older history"); got != 1 {
		t.Fatalf("first request should already contain the compact summary, got %d in %+v", got, step.calls[0].Messages)
	}
}

func TestRunToolLoop_CompactOnlyStopsBeforeProviderRequest(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "should not be requested"}}}
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("old request ", 100)},
		{Role: "assistant", Content: strings.Repeat("old answer ", 100)},
	}
	compactCalls := 0
	res, err := RunToolLoop(context.Background(), history, LoopConfig{
		Model:               "m",
		ForceInitialCompact: true,
		CompactOnly:         true,
		Compact: func(_ context.Context, _ []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalls++
			return []providers.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "system", Content: compact.BuildSummaryContent("older history")},
			}, nil
		},
	}, step)
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if compactCalls != 1 {
		t.Fatalf("compact calls = %d, want 1", compactCalls)
	}
	if len(step.calls) != 0 {
		t.Fatalf("compact-only turn must not send a normal provider request, got %+v", step.calls)
	}
	if !res.HistoryRewritten {
		t.Fatal("compact-only turn should mark rewritten history when compaction changed messages")
	}
	if got := countMessagesContaining(res.NewMessages, "older history"); got != 1 {
		t.Fatalf("returned history should contain compact summary once, got %d in %+v", got, res.NewMessages)
	}
}

func TestRunToolLoop_ForceInitialCompactNoopReportsUnchanged(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "/compact"},
	}
	var attempts []CompactAttemptInfo
	var infos []CompactInfo
	res, err := RunToolLoop(context.Background(), history, LoopConfig{
		Model:               "m",
		ForceInitialCompact: true,
		Compact: func(_ context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			// Nothing worth folding: return the input unchanged.
			return messages, nil
		},
		OnCompact:        func(info CompactInfo) { infos = append(infos, info) },
		OnCompactAttempt: func(info CompactAttemptInfo) { attempts = append(attempts, info) },
	}, step)
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("no-op forced compact must not emit OnCompact, got %+v", infos)
	}
	if len(attempts) != 1 || attempts[0].Reason != CompactReasonManual || attempts[0].Status != CompactAttemptUnchanged {
		t.Fatalf("attempts = %+v, want one manual/unchanged entry", attempts)
	}
	if res.HistoryRewritten {
		t.Fatal("no-op forced compact must not mark history rewritten")
	}
}

func TestRunToolLoopRejectsExpandingCompactionReplacement(t *testing.T) {
	history := []providers.ChatMessage{{Role: "user", Content: "short history"}}
	var attempts []CompactAttemptInfo
	res, err := RunToolLoop(context.Background(), history, LoopConfig{
		Model:               "m",
		ForceInitialCompact: true,
		CompactOnly:         true,
		Compact: func(_ context.Context, _ []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return []providers.ChatMessage{{Role: "system", Content: compact.BuildSummaryContent(strings.Repeat("expanded ", 100))}}, nil
		},
		OnCompactAttempt: func(info CompactAttemptInfo) { attempts = append(attempts, info) },
	}, &fakeStep{})
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if res.HistoryRewritten {
		t.Fatal("expanding replacement must not rewrite history")
	}
	if len(attempts) != 1 || attempts[0].Status != CompactAttemptFailed || !strings.Contains(attempts[0].Error, "did not shrink") {
		t.Fatalf("attempts = %+v", attempts)
	}
}

func TestRequestContextInfoReportsLoadableToolSchemasSeparately(t *testing.T) {
	assembly := RequestAssembly{
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "find docs"},
			{
				Role:           "tool",
				Name:           "tool_search",
				ToolResultKind: providers.ToolCallKindToolSearch,
				Content:        `{"loadable_tools":[{"type":"function","name":"mcp_docs_search","description":"Search docs","input_schema":{"type":"object","properties":{"query":{"type":"string"}}},"defer_loading":true}]}`,
			},
		},
	}
	info := requestContextInfo(0, assembly, nil, nil, nil)
	if info.ToolSchemaBytes != 0 || info.ToolSurfaceHash != "" {
		t.Fatalf("loadable tools should not count as top-level tool schema: %+v", info)
	}
	if info.LoadableToolCount != 1 || info.LoadableToolSchemaBytes == 0 || info.LoadableToolSurfaceHash == "" {
		t.Fatalf("missing loadable tool request metrics: %+v", info)
	}
}

func TestRequestContextInfoReadsNativeToolSearchToolsShape(t *testing.T) {
	assembly := RequestAssembly{
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "find docs"},
			{
				Role:           "tool",
				Name:           "tool_search",
				ToolResultKind: providers.ToolCallKindToolSearch,
				Content:        `{"tools":[{"type":"function","name":"mcp_docs_search","description":"Search docs","input_schema":{"type":"object","properties":{"query":{"type":"string"}}},"defer_loading":true}]}`,
			},
		},
	}
	info := requestContextInfo(0, assembly, nil, nil, nil)
	if info.LoadableToolCount != 1 || info.LoadableToolSchemaBytes == 0 || info.LoadableToolSurfaceHash == "" {
		t.Fatalf("missing native loadable tool request metrics: %+v", info)
	}
}

func TestRunToolLoop_CompactRewritePromotesSummaryIntoCacheHint(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "c1", Name: "t", Arguments: `{}`}}, Usage: &providers.TokenUsage{InputTokens: 1300}},
		{Content: "done"},
	}}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 1300})
	cfg := LoopConfig{
		Model: "m",
		Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}},
		Compact: func(_ context.Context, _ []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return []providers.ChatMessage{
				{Role: "system", Content: "[Conversation summary]\nOlder turns were compacted."},
				{Role: "user", Content: "latest ask"},
			}, nil
		},
		MaxContextTokens: 1000,
		DefaultMaxTokens: 100,
		UsageTracker:     tracker,
	}

	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "old ask"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "latest ask"},
	}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected 2 step calls, got %d", len(step.calls))
	}
	secondHint := step.calls[1].CacheHint
	if secondHint == nil {
		t.Fatal("expected cache hint after compact")
	}
	if !secondHint.HasCompactSummary {
		t.Fatal("expected compact summary flag after rewrite")
	}
	if secondHint.StablePrefixMessages != 0 {
		t.Fatalf("expected current turn to remain volatile after rewrite, got %d", secondHint.StablePrefixMessages)
	}
	if secondHint.TurnPrefixMessages != 1 {
		t.Fatalf("expected turn prefix through current ask after rewrite, got %d", secondHint.TurnPrefixMessages)
	}
	if !secondHint.StableSystem {
		t.Fatal("expected summary system message to stay cacheable")
	}
	if secondHint.PromptCacheKey == "" {
		t.Fatal("expected prompt cache key after compact")
	}
	if step.calls[1].Messages[0].Content != "[Conversation summary]\nOlder turns were compacted." {
		t.Fatalf("expected compact summary at request root, got %+v", step.calls[1].Messages[0])
	}
}

func TestRunToolLoop_ToolCallThenAnswer(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "c1", Name: "run_shell", Arguments: `{}`}}},
		{Content: "tool said ok, here is your answer"},
	}}
	tools := &fakeLoopTools{
		defs:    []providers.ToolDefinition{{Name: "run_shell"}},
		results: map[string]string{"c1": `{"ok":true}`},
	}
	cfg := LoopConfig{Model: "m", Tools: tools}

	var seenCalls []providers.ToolCall
	cfg.OnToolResult = func(call providers.ToolCall, _ string) {
		seenCalls = append(seenCalls, call)
	}

	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("do thing")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "tool said ok, here is your answer" {
		t.Fatalf("got %q", res.Content)
	}
	if len(tools.calls) != 1 || tools.calls[0].ID != "c1" {
		t.Fatalf("unexpected tool calls: %+v", tools.calls)
	}
	if steps := tools.recordedSteps(); len(steps) != 1 || steps[0] != 0 {
		t.Fatalf("tool execution steps = %+v, want [0]", steps)
	}
	if len(seenCalls) != 1 {
		t.Fatalf("expected OnToolResult to fire once, got %d", len(seenCalls))
	}
	roles := []string{}
	for _, m := range visibleMessagesForTest(res.NewMessages) {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "assistant,tool,assistant" {
		t.Fatalf("unexpected message order: %v", roles)
	}
}

func TestRunToolLoop_AppendsToolResultsBeforeRequestOnlyHookContext(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{}`},
			{ID: "call_2", Name: "grep", Arguments: `{}`},
			{ID: "call_3", Name: "read_file", Arguments: `{}`},
		}},
		{Content: "done"},
	}}
	tools := &contextLoopTools{defs: []providers.ToolDefinition{
		{Name: "read_file"},
		{Name: "grep"},
	}}
	var contexts []RequestContextInfo

	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("inspect")}, LoopConfig{
		Model: "m",
		Tools: tools,
		OnRequestContext: func(info RequestContextInfo) {
			contexts = append(contexts, info)
		},
	}, step)
	if err != nil {
		t.Fatal(err)
	}
	if err := providers.ValidateToolCallHistory(res.NewMessages); err != nil {
		t.Fatalf("expected valid returned message sequence, got %v: %+v", err, res.NewMessages)
	}
	visible := visibleMessagesForTest(res.NewMessages)
	roles := make([]string, 0, len(visible))
	for _, msg := range visible {
		roles = append(roles, msg.Role)
	}
	if got, want := strings.Join(roles, ","), "assistant,tool,tool,tool,assistant"; got != want {
		t.Fatalf("unexpected returned message order: got %s want %s", got, want)
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(step.calls))
	}
	if err := providers.ValidateToolCallHistory(step.calls[1].Messages); err != nil {
		t.Fatalf("expected valid second request sequence, got %v: %+v", err, step.calls[1].Messages)
	}
	visibleRequest := visibleMessagesForTest(step.calls[1].Messages)
	requestRoles := make([]string, 0, len(visibleRequest))
	for _, msg := range visibleRequest {
		requestRoles = append(requestRoles, msg.Role)
	}
	if got, want := strings.Join(requestRoles, ","), "user,assistant,tool,tool,tool"; got != want {
		t.Fatalf("unexpected second request order: got %s want %s", got, want)
	}
	for i, wantID := range []string{"call_1", "call_2", "call_3"} {
		msg := visibleRequest[2+i]
		if msg.Role != "tool" || msg.ToolCallID != wantID {
			t.Fatalf("tool result %d: got %+v want call_id %s", i, msg, wantID)
		}
	}
	if got := countMessagesContaining(step.calls[1].Messages, "[ADDITIONAL_CONTEXT]"); got != 3 {
		t.Fatalf("expected three request-only hook context blocks, got %d in %+v", got, step.calls[1].Messages)
	}
	if got := countMessagesContaining(res.NewMessages, "[ADDITIONAL_CONTEXT]"); got != 0 {
		t.Fatalf("hook context must not enter durable returned history, got %d in %+v", got, res.NewMessages)
	}
	if len(contexts) != 2 {
		t.Fatalf("expected two request context observations, got %+v", contexts)
	}
	if containsString(contexts[0].BlockKinds, string(wuucontext.BlockAdditionalContext)) {
		t.Fatalf("first request should not include post-tool hook context: %+v", contexts[0])
	}
	if !containsString(contexts[1].BlockKinds, string(wuucontext.BlockAdditionalContext)) || contexts[1].TransientMessages != 3 {
		t.Fatalf("second request should expose hook context as request-only additional context: %+v", contexts[1])
	}
}

func TestRunToolLoop_FiltersLegacyHookContextHistory(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "done"}}}
	history := []providers.ChatMessage{
		userMsg("inspect"),
		{Role: "user", Content: "[Hook context for read_file]: stale plugin note"},
	}

	res, err := RunToolLoop(context.Background(), history, LoopConfig{Model: "m"}, step)
	if err != nil {
		t.Fatal(err)
	}
	if got := countMessagesContaining(step.calls[0].Messages, "[Hook context for"); got != 0 {
		t.Fatalf("legacy hook context should be filtered before request, got %d in %+v", got, step.calls[0].Messages)
	}
	if got := countMessagesContaining(res.NewMessages, "[Hook context for"); got != 0 {
		t.Fatalf("legacy hook context should not be returned as durable history, got %d in %+v", got, res.NewMessages)
	}
}

func TestRunToolLoop_ConcurrentToolCompletionDoesNotReorderProviderMessages(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{}`},
			{ID: "call_2", Name: "read_file", Arguments: `{}`},
		}},
		{Content: "done"},
	}}
	tools := newDelayedConcurrentTools()

	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("inspect")}, LoopConfig{
		Model: "m",
		Tools: tools,
	}, step)
	if err != nil {
		t.Fatal(err)
	}
	if err := providers.ValidateToolCallHistory(res.NewMessages); err != nil {
		t.Fatalf("expected valid returned message sequence, got %v: %+v", err, res.NewMessages)
	}
	if got := strings.Join(tools.completedOrder(), ","); got != "call_2,call_1" {
		t.Fatalf("test did not simulate out-of-order completion: got %s", got)
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(step.calls))
	}
	var gotToolIDs []string
	for _, msg := range step.calls[1].Messages {
		if msg.Role == "tool" {
			gotToolIDs = append(gotToolIDs, msg.ToolCallID)
		}
	}
	if got, want := strings.Join(gotToolIDs, ","), "call_1,call_2"; got != want {
		t.Fatalf("provider request tool order changed with completion order: got %s want %s", got, want)
	}
}

func TestRunToolLoop_OutputTruncationCompletesTurn(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{Content: "part1 ", Truncated: true, StopReason: "length"},
	}}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("write story")}, LoopConfig{Model: "m"}, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "part1 " {
		t.Fatalf("expected first partial content, got %q", res.Content)
	}
	if res.FinishReason != providers.FinishReasonLength || res.StopReason != "length" || !res.Truncated {
		t.Fatalf("expected length finish metadata, got reason=%q stop=%q truncated=%v", res.FinishReason, res.StopReason, res.Truncated)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected 1 step call, got %d", len(step.calls))
	}
	visible := visibleMessagesForTest(res.NewMessages)
	if len(visible) != 1 {
		t.Fatalf("expected one assistant message, got %+v", res.NewMessages)
	}
	msg := visible[0]
	if msg.FinishReason != providers.FinishReasonLength || msg.StopReason != "length" || !msg.Truncated {
		t.Fatalf("expected assistant message finish metadata, got %+v", msg)
	}
}

func TestRunToolLoop_MaxTokensStopReasonNormalizesLength(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{Content: "x", Truncated: true, StopReason: "max_tokens"},
	}}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("loop")}, LoopConfig{Model: "m"}, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "x" {
		t.Fatalf("expected partial content, got %q", res.Content)
	}
	if res.FinishReason != providers.FinishReasonLength || res.StopReason != "max_tokens" || !res.Truncated {
		t.Fatalf("expected max_tokens to normalize to length, got reason=%q stop=%q truncated=%v", res.FinishReason, res.StopReason, res.Truncated)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected 1 step call, got %d", len(step.calls))
	}
}

func TestRunToolLoop_KimiMessageSizeOverflowAutoCompactsOnce(t *testing.T) {
	body := "total message size 2306631 exceeds limit 2097152"
	overflow := &providers.HTTPError{StatusCode: 400, Body: body, ContextOverflow: providers.DetectContextOverflow(body)}
	if !overflow.ContextOverflow {
		t.Fatal("Kimi message-size response was not classified as context overflow")
	}
	step := &fakeStep{results: []StepResult{{}, {Content: "ok"}}, errs: []error{overflow, nil}}
	compactCalled := 0
	compactFn := func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
		compactCalled++
		return msgs[len(msgs)-1:], nil
	}
	cfg := LoopConfig{Model: "m", Compact: compactFn}

	history := []providers.ChatMessage{
		userMsg("old"),
		{Role: "assistant", Content: "old answer"},
		userMsg("big"),
	}
	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if res.Content != "ok" {
		t.Fatalf("expected ok, got %q", res.Content)
	}
	if compactCalled != 1 {
		t.Fatalf("expected compact called once, got %d", compactCalled)
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected initial request plus one transformed retry, got %d calls", len(step.calls))
	}
}

func TestRunToolLoop_ContextOverflowStopsWhenCompactUnchanged(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	step := &fakeStep{results: []StepResult{{}}, errs: []error{overflow}}
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "oversized fresh prompt"},
	}
	compactCalled := 0
	var attempts []CompactAttemptInfo
	cfg := LoopConfig{
		Model: "m",
		Compact: func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return msgs, nil
		},
		OnCompactAttempt: func(info CompactAttemptInfo) {
			attempts = append(attempts, info)
		},
	}

	_, err := RunToolLoop(context.Background(), history, cfg, step)
	if err == nil || !providers.IsContextOverflow(err) {
		t.Fatalf("expected original context overflow, got %v", err)
	}
	if compactCalled != 1 {
		t.Fatalf("expected one reactive compact attempt, got %d", compactCalled)
	}
	if len(step.calls) != 1 {
		t.Fatalf("unchanged compact should not retry the same overflowing request, got %d calls", len(step.calls))
	}
	if len(attempts) != 1 || attempts[0].Reason != CompactReasonOverflow || attempts[0].Status != CompactAttemptUnchanged {
		t.Fatalf("expected unchanged overflow attempt, got %+v", attempts)
	}
}

func TestRunToolLoop_ContextOverflowForceTrimsWhenCompactUnchanged(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	step := &fakeStep{results: []StepResult{{}, {Content: "ok"}}, errs: []error{overflow, nil}}
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		userMsg("old"),
		{Role: "assistant", Content: "old answer"},
		userMsg("latest"),
	}
	compactCalled := 0
	cfg := LoopConfig{
		Model: "m",
		Compact: func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return msgs, nil
		},
	}

	result, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatalf("expected force-trim recovery to succeed, got %v", err)
	}
	if compactCalled != 1 {
		t.Fatalf("expected one reactive compact attempt, got %d", compactCalled)
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected compact-unchanged overflow to retry a trimmed request, got %d calls", len(step.calls))
	}
	if !result.HistoryRewritten {
		t.Fatal("expected force-trim to rewrite history")
	}
	if len(step.calls[1].Messages) >= len(history) {
		t.Fatalf("trimmed retry still had %d messages", len(step.calls[1].Messages))
	}
}

func TestRunToolLoop_ContextOverflowOnlyRetriesOnce(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	step := &fakeStep{results: []StepResult{{}, {}}, errs: []error{overflow, overflow}}
	cfg := LoopConfig{Model: "m", Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) { return m[1:], nil }}
	history := []providers.ChatMessage{
		userMsg("old"),
		{Role: "assistant", Content: "old answer"},
		userMsg("big"),
	}

	_, err := RunToolLoop(context.Background(), history, cfg, step)
	if err == nil {
		t.Fatal("expected second overflow to surface")
	}
	if !providers.IsContextOverflow(err) {
		t.Fatalf("expected context-overflow error, got %v", err)
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected one retry after changed compact, got %d calls", len(step.calls))
	}
}

func TestRunToolLoop_MaxStepsExceeded(t *testing.T) {
	step := &fakeStep{results: []StepResult{{ToolCalls: []providers.ToolCall{{ID: "a", Name: "t", Arguments: `{}`}}}}}
	cfg := LoopConfig{Model: "m", Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}}, MaxSteps: 1}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("loop")}, cfg, step)
	if err == nil {
		t.Fatal("expected max-steps error")
	}
	if !strings.Contains(err.Error(), "max steps exceeded") {
		t.Fatalf("got %v", err)
	}
}

func TestRunToolLoop_ZeroMaxStepsIsUnlimited(t *testing.T) {
	const rounds = 12
	results := make([]StepResult, 0, rounds+1)
	for i := 0; i < rounds; i++ {
		results = append(results, StepResult{ToolCalls: []providers.ToolCall{{ID: fmt.Sprintf("c%d", i), Name: "t", Arguments: `{}`}}})
	}
	results = append(results, StepResult{Content: "all done"})

	step := &fakeStep{results: results}
	cfg := LoopConfig{Model: "m", Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}}}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("long")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "all done" {
		t.Fatalf("got %q", res.Content)
	}
}

func TestErrorJSONIncludesActionableEnvelope(t *testing.T) {
	raw := errorJSON(errors.New(`tool "write_file" denied by workspace boundary: error_kind=boundary_denied model_next_action="ask user to add workspace"`))
	var parsed struct {
		OK              bool     `json:"ok"`
		Error           string   `json:"error"`
		ErrorKind       string   `json:"error_kind"`
		NextSuggestions []string `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("parse errorJSON: %v\n%s", err, raw)
	}
	if parsed.OK || parsed.Error == "" || parsed.ErrorKind != "boundary_denied" {
		t.Fatalf("unexpected error envelope: %+v", parsed)
	}
	if !strings.Contains(strings.Join(parsed.NextSuggestions, " "), "workspace boundary") {
		t.Fatalf("boundary error should guide the model: %+v", parsed.NextSuggestions)
	}

	raw = errorJSON(errors.New(`edit failed: error_kind=anchor_not_found safe_retry="read the target range and retry"`))
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("parse safe_retry errorJSON: %v\n%s", err, raw)
	}
	if parsed.ErrorKind != "anchor_not_found" || !strings.Contains(strings.Join(parsed.NextSuggestions, " "), "safe_retry") {
		t.Fatalf("safe_retry error should preserve kind and guidance: %+v", parsed)
	}
}

func TestRunToolLoop_OnUsageReceivesPerCall(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "done", Usage: &providers.TokenUsage{InputTokens: 10, OutputTokens: 5, CacheCreationTokens: 7, CacheReadTokens: 3}}}}
	var seenIn, seenOut int
	var seenFull providers.TokenUsage
	cfg := LoopConfig{
		Model: "m",
		OnUsage: func(in, out int) {
			seenIn += in
			seenOut += out
		},
		OnTokenUsage: func(usage providers.TokenUsage) {
			seenFull.InputTokens += usage.InputTokens
			seenFull.OutputTokens += usage.OutputTokens
			seenFull.CacheCreationTokens += usage.CacheCreationTokens
			seenFull.CacheReadTokens += usage.CacheReadTokens
		},
	}
	res, _ := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if seenIn != 10 || seenOut != 5 {
		t.Fatalf("OnUsage missed: in=%d out=%d", seenIn, seenOut)
	}
	if seenFull.InputTokens != 10 || seenFull.OutputTokens != 5 || seenFull.CacheCreationTokens != 7 || seenFull.CacheReadTokens != 3 {
		t.Fatalf("OnTokenUsage missed: %+v", seenFull)
	}
	if res.InputTokens != 10 || res.OutputTokens != 5 || res.CacheCreationTokens != 7 || res.CacheReadTokens != 3 {
		t.Fatalf("LoopResult totals wrong: %+v", res)
	}
}

func TestRunToolLoop_ProactiveCompactTriggers(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "compacted answer"}}}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 950, OutputTokens: 0})
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "old ask"},
		{Role: "assistant", Content: "old answer"},
		userMsg("hi"),
	}

	compactCalled := 0
	var callbackOrder []string
	compactFn := func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
		compactCalled++
		callbackOrder = append(callbackOrder, "compact")
		return []providers.ChatMessage{{Role: "user", Content: "summary"}}, nil
	}
	var compactInfos []CompactInfo
	cfg := LoopConfig{
		Model:            "m",
		Compact:          compactFn,
		MaxContextTokens: 1000,
		DefaultMaxTokens: 100,
		OnCompactStart: func(reason CompactReason) {
			callbackOrder = append(callbackOrder, "start:"+string(reason))
		},
		OnCompact: func(info CompactInfo) {
			callbackOrder = append(callbackOrder, "completed:"+string(info.Reason))
			compactInfos = append(compactInfos, info)
		},
		UsageTracker: tracker,
	}

	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if compactCalled != 1 {
		t.Fatalf("expected 1 proactive compact, got %d", compactCalled)
	}
	if len(compactInfos) != 1 {
		t.Fatalf("expected 1 OnCompact callback, got %d", len(compactInfos))
	}
	if compactInfos[0].Reason != CompactReasonProactive {
		t.Fatalf("expected proactive reason, got %q", compactInfos[0].Reason)
	}
	if compactInfos[0].MessagesAfter >= compactInfos[0].MessagesBefore {
		t.Fatalf("expected MessagesAfter < MessagesBefore, got %+v", compactInfos[0])
	}
	if compactInfos[0].MessagesBefore != 3 || compactInfos[0].MessagesAfter != 1 {
		t.Fatalf("notice message counts = %d → %d, want 3 → 1", compactInfos[0].MessagesBefore, compactInfos[0].MessagesAfter)
	}
	if compactInfos[0].TokensAfter <= 0 || compactInfos[0].TokensAfter >= compactInfos[0].TokensBefore {
		t.Fatalf("notice token counts should report a smaller non-zero replacement, got %+v", compactInfos[0])
	}
	wantOrder := []string{"start:proactive", "compact", "completed:proactive"}
	if strings.Join(callbackOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("compact callback order = %v, want %v", callbackOrder, wantOrder)
	}
	if res.Content != "compacted answer" {
		t.Fatalf("expected compacted answer, got %q", res.Content)
	}
	if !res.HistoryRewritten {
		t.Fatal("expected history rewritten after proactive compact")
	}
	visible := visibleMessagesForTest(res.NewMessages)
	if len(visible) != 2 {
		t.Fatalf("expected full compacted history snapshot, got %d messages", len(res.NewMessages))
	}
	if visible[0].Role != "user" || visible[0].Content != "summary" {
		t.Fatalf("expected compacted snapshot to start with summary message, got %+v", visible[0])
	}
	if visible[1].Role != "assistant" || visible[1].Content != "compacted answer" {
		t.Fatalf("expected compacted answer in snapshot, got %+v", visible[1])
	}
}

func TestCompactNoticeMessageCountExcludesPreservedSystemScaffolding(t *testing.T) {
	messages := []providers.ChatMessage{
		{Role: "system", Content: "base system prompt"},
		{Role: "system", Content: "discovered tool context"},
		{Role: "user", Content: "request"},
		{Role: "assistant", Content: "response"},
		{Role: "user", Content: "hidden bookkeeping", Hidden: true},
	}
	replacement := []providers.ChatMessage{
		messages[0],
		messages[1],
		{Role: "system", Content: compact.BuildSummaryContent("continuation note"), Hidden: true},
	}

	if got := compactNoticeMessageCount(messages); got != 2 {
		t.Fatalf("pre-compaction notice count = %d, want 2", got)
	}
	if got := compactNoticeMessageCount(replacement); got != 1 {
		t.Fatalf("note replacement notice count = %d, want 1", got)
	}
}

func TestRunToolLoop_ProactivelyCompactsMidTurnBeforeNextRequest(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "c1", Name: "t", Arguments: `{}`}}, Usage: &providers.TokenUsage{InputTokens: 1300, OutputTokens: 0}},
		{Content: "ok"},
	}}
	compactCalled := 0
	var attempts []CompactAttemptInfo
	cfg := LoopConfig{
		Model: "m",
		Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}},
		Compact: func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			if !containsRoleContent(msgs, "tool", `{"ok":true}`) {
				t.Fatalf("mid-turn compact should include completed tool result, got %+v", msgs)
			}
			return []providers.ChatMessage{{Role: "user", Content: "summary"}}, nil
		},
		MaxContextTokens: 1000,
		DefaultMaxTokens: 100,
		OnCompactAttempt: func(info CompactAttemptInfo) {
			attempts = append(attempts, info)
		},
	}

	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "ok" {
		t.Fatalf("unexpected content %q", res.Content)
	}
	if compactCalled != 1 {
		t.Fatalf("mid-turn tool results should trigger one proactive compact, got %d calls", compactCalled)
	}
	if len(attempts) != 1 || attempts[0].Reason != CompactReasonProactive || attempts[0].Status != CompactAttemptSucceeded {
		t.Fatalf("expected one successful proactive compact attempt, got %+v", attempts)
	}
	if !res.HistoryRewritten {
		t.Fatal("mid-turn proactive compact should rewrite history")
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(step.calls))
	}
	visibleRequest := visibleMessagesForTest(step.calls[1].Messages)
	if len(visibleRequest) != 1 || visibleRequest[0].Role != "user" || visibleRequest[0].Content != "summary" {
		t.Fatalf("second request should use compacted history, got %+v", visibleRequest)
	}
}

func TestRunToolLoop_PreRequestCompactUsesLocalEstimateWithoutGroundTruth(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("seed ", 80)},
		{Role: "assistant", Content: strings.Repeat("seed ", 80)},
		{Role: "user", Content: strings.Repeat("seed ", 80)},
		{Role: "assistant", Content: strings.Repeat("seed ", 80)},
	}
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	compactCalled := 0
	cfg := LoopConfig{
		Model: "m",
		Compact: func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return msgs[:2], nil
		},
		MaxContextTokens: 10,
		DefaultMaxTokens: 1,
	}

	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalled != 1 {
		t.Fatalf("expected pre-request compact from local estimate, got %d", compactCalled)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected one provider call, got %d", len(step.calls))
	}
	if len(visibleMessagesForTest(step.calls[0].Messages)) != 2 {
		t.Fatalf("expected compacted history to be sent, got %d messages", len(step.calls[0].Messages))
	}
	if res.Content != "ok" {
		t.Fatalf("unexpected content %q", res.Content)
	}
	if !res.HistoryRewritten {
		t.Fatal("expected history rewrite after estimate-based compact")
	}
}

func TestRunToolLoop_PreRequestCompactSkipsFreshPrompt(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("fresh prompt ", 1000)},
	}
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 950})
	compactCalled := 0
	var attempts []CompactAttemptInfo
	cfg := LoopConfig{
		Model: "m",
		Compact: func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return msgs, nil
		},
		MaxContextTokens: 1000,
		DefaultMaxTokens: 100,
		UsageTracker:     tracker,
		OnCompactAttempt: func(info CompactAttemptInfo) {
			attempts = append(attempts, info)
		},
	}

	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "ok" {
		t.Fatalf("unexpected content %q", res.Content)
	}
	if compactCalled != 0 {
		t.Fatalf("fresh prompt should not trigger no-op compact, got %d calls", compactCalled)
	}
	if len(attempts) != 0 {
		t.Fatalf("fresh prompt should not emit compact attempts, got %+v", attempts)
	}
	if res.HistoryRewritten {
		t.Fatal("fresh prompt should not rewrite history")
	}
}

func TestRunToolLoop_PreRequestCompactUsesSharedUsageTracker(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "follow up"},
	}
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 950})
	tracker.RecordPendingMessages(history[len(history)-1:])

	compactCalled := 0
	cfg := LoopConfig{
		Model: "m",
		Compact: func(_ context.Context, _ []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return []providers.ChatMessage{
				{Role: "system", Content: "[Conversation summary]\nOlder turns"},
				{Role: "user", Content: "follow up"},
			}, nil
		},
		MaxContextTokens: 1000,
		DefaultMaxTokens: 100,
		UsageTracker:     tracker,
	}

	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalled != 1 {
		t.Fatalf("expected one pre-request compact, got %d", compactCalled)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected one provider call, got %d", len(step.calls))
	}
	if got := step.calls[0].Messages[0].Content; got != "[Conversation summary]\nOlder turns" {
		t.Fatalf("expected compacted request root, got %q", got)
	}
	if !res.HistoryRewritten {
		t.Fatal("expected history rewrite after pre-request compact")
	}
}

func TestRunToolLoop_ProactiveCompactDisabledWhenNoWindow(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "done", Usage: &providers.TokenUsage{InputTokens: 1_000_000, OutputTokens: 0}}}}
	compactCalled := 0
	cfg := LoopConfig{Model: "m", Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) {
		compactCalled++
		return m, nil
	}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalled != 0 {
		t.Fatalf("proactive compact should be disabled, but ran %d times", compactCalled)
	}
}

func TestRunToolLoop_ProactiveCompactRespectsCustomThreshold(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 600})
	compactCalled := 0
	cfg := LoopConfig{Model: "m", Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) {
		compactCalled++
		return []providers.ChatMessage{{Role: "user", Content: "sum"}}, nil
	}, MaxContextTokens: 1000, CompactThresholdPct: 0.5, UsageTracker: tracker}
	history := []providers.ChatMessage{
		userMsg("old"),
		{Role: "assistant", Content: "old answer"},
		userMsg("hi"),
	}
	_, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalled != 1 {
		t.Fatalf("expected proactive compact at 50%% threshold, got %d", compactCalled)
	}
}

func TestProactiveCompactThresholdReservesOutputHeadroom(t *testing.T) {
	cfg := LoopConfig{Model: "claude-sonnet-4-6", MaxContextTokens: 64_000}
	if got, want := proactiveCompactThreshold(cfg), 48_000; got != want {
		t.Fatalf("expected output-reserved threshold %d, got %d", want, got)
	}

	cfg = LoopConfig{Model: "gpt-5", MaxContextTokens: 400_000}
	if got, want := proactiveCompactThreshold(cfg), 368_000; got != want {
		t.Fatalf("expected full max-output-reserved threshold %d, got %d", want, got)
	}

	cfg = LoopConfig{Model: "claude-sonnet-4-6", MaxContextTokens: 64_000, CompactThresholdPct: 0.5}
	if got, want := proactiveCompactThreshold(cfg), 32_000; got != want {
		t.Fatalf("expected custom lower threshold %d, got %d", want, got)
	}

	cfg = LoopConfig{Model: "brand-new-model", MaxContextTokens: 1_000_000, OutputReserveTokens: 128_000}
	if got, want := proactiveCompactThreshold(cfg), 872_000; got != want {
		t.Fatalf("expected explicit output-reserved threshold %d, got %d", want, got)
	}
}

func TestProactiveCompactThresholdRespectsInputLimit(t *testing.T) {
	cfg := LoopConfig{Model: "gpt-5.5", MaxContextTokens: 1_048_576, MaxInputTokens: 272_000}
	if got, want := proactiveCompactThreshold(cfg), 252_000; got != want {
		t.Fatalf("expected input-limited threshold %d, got %d", want, got)
	}

	cfg = LoopConfig{Model: "brand-new-model", MaxContextTokens: 1_000_000, MaxInputTokens: 272_000, OutputReserveTokens: 128_000}
	if got, want := proactiveCompactThreshold(cfg), 252_000; got != want {
		t.Fatalf("expected explicit input-limited threshold %d, got %d", want, got)
	}
}

func TestRunToolLoop_ProactiveCompactFailureEmitsAttempt(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 1300})
	compactErr := errors.New("compact provider unavailable")
	var attempts []CompactAttemptInfo
	var compactInfos []CompactInfo
	cfg := LoopConfig{
		Model: "m",
		Compact: func(context.Context, []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return nil, compactErr
		},
		MaxContextTokens: 1000,
		DefaultMaxTokens: 100,
		OnCompactAttempt: func(info CompactAttemptInfo) { attempts = append(attempts, info) },
		OnCompact:        func(info CompactInfo) { compactInfos = append(compactInfos, info) },
		UsageTracker:     tracker,
	}

	history := []providers.ChatMessage{
		userMsg("old"),
		{Role: "assistant", Content: "old answer"},
		userMsg("hi"),
	}
	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if res.Content != "ok" {
		t.Fatalf("expected model to continue after proactive compact failure, got %q", res.Content)
	}
	if len(compactInfos) != 0 {
		t.Fatalf("failed compact must not emit success callback: %+v", compactInfos)
	}
	if len(attempts) != 1 ||
		attempts[0].Reason != CompactReasonProactive ||
		attempts[0].Status != CompactAttemptFailed ||
		attempts[0].LastResponseTotal != 1300 ||
		attempts[0].PendingDelta != 0 ||
		attempts[0].UsageAdjustment != UsageAdjustmentProviderResponse ||
		!strings.Contains(attempts[0].Error, compactErr.Error()) {
		t.Fatalf("expected proactive failed attempt, got %+v", attempts)
	}
}

func TestRunToolLoop_ProactiveCompactDoesNotLoopOnNoOpCompact(t *testing.T) {
	step := &fakeStep{results: []StepResult{{ToolCalls: []providers.ToolCall{{ID: "c1", Name: "t", Arguments: `{}`}}, Usage: &providers.TokenUsage{InputTokens: 1300}}, {ToolCalls: []providers.ToolCall{{ID: "c2", Name: "t", Arguments: `{}`}}, Usage: &providers.TokenUsage{InputTokens: 1300}}, {Content: "done"}}}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 1300})
	compactCalled := 0
	cfg := LoopConfig{Model: "m", Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}}, Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) {
		compactCalled++
		return m, nil
	}, MaxContextTokens: 1000, DefaultMaxTokens: 100, UsageTracker: tracker}
	history := []providers.ChatMessage{
		userMsg("old"),
		{Role: "assistant", Content: "old answer"},
		userMsg("hi"),
	}
	_, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalled != 1 {
		t.Fatalf("expected one first-step compact attempt, got %d", compactCalled)
	}
}

func TestRunToolLoop_OverflowCompactIgnoresIdleToolRuntime(t *testing.T) {
	cause := &providers.HTTPError{
		StatusCode:      400,
		Body:            "400 Bad Request: Failed to start sampling: [input_too_large] The prompt is too long for this model's context window (500855 tokens > 500000 tokens)",
		ContextOverflow: true,
	}
	overflow := &providers.StreamRecoveryError{
		Cause: cause,
		Recovery: providers.StreamRecoveryInfo{
			AttemptCount:    1,
			RetryCount:      0,
			MaxAttempts:     11,
			SubmissionCount: 1,
			StopReason:      "non_retryable",
			FailureCategory: providers.FailureContextOverflow,
		},
	}
	idleRuntime := NewTurnToolRuntime(ToolRuntimeConfig{})
	step := &fakeStep{
		results: []StepResult{{ToolRuntime: idleRuntime}, {Content: "ok"}},
		errs:    []error{overflow, nil},
	}
	var infos []CompactInfo
	cfg := LoopConfig{Model: "m", Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) {
		return m[len(m)-1:], nil
	}, OnCompact: func(info CompactInfo) { infos = append(infos, info) }}
	history := []providers.ChatMessage{
		userMsg("old"),
		{Role: "assistant", Content: "old answer"},
		userMsg("big"),
	}
	_, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Reason != CompactReasonOverflow {
		t.Fatalf("expected overflow compact despite idle tool runtime, got %+v", infos)
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected overflow plus recovered retry, got %d calls", len(step.calls))
	}
}

func TestRunToolLoop_UndercountedLocalUsageShrinksBeforeRequest(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "review the change"},
	}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("c%d", i)
		history = append(history,
			providers.ChatMessage{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID: id, Name: "bash", Arguments: `{"command":"` + strings.Repeat("x", 4000) + `"}`,
			}}},
			providers.ChatMessage{Role: "tool", Name: "bash", ToolCallID: id, Content: strings.Repeat("y", 4000)},
		)
	}
	history = append(history, userMsg("合并了吗"))
	tools := &fakeLoopTools{defs: []providers.ToolDefinition{{
		Name:        "bash",
		Description: strings.Repeat("schema ", 800),
	}}}
	messagesOnly := localRequestEstimate(history, LoopConfig{})
	withSchema := localRequestEstimate(history, LoopConfig{Tools: tools})
	if withSchema <= messagesOnly {
		t.Fatalf("tool schemas should increase the outbound estimate: messages=%d withSchema=%d", messagesOnly, withSchema)
	}
	tracker := NewUsageTracker()
	tracker.RecordPendingMessages([]providers.ChatMessage{{Role: "user", Content: "short"}})
	if tracker.EstimateCurrent() >= messagesOnly {
		t.Fatalf("fixture is not an undercount: tracker=%d messages=%d", tracker.EstimateCurrent(), messagesOnly)
	}
	step := &fakeStep{results: []StepResult{{Content: "merged"}}}
	cfg := LoopConfig{
		Model:                  "grok-4.6",
		Tools:                  tools,
		UsageTracker:           tracker,
		CompactThresholdTokens: messagesOnly + 1,
		FreshContextTokens:     200_000,
		ArchiveHistory: func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
			return HistoryArchive{HeadSeq: 40}, nil
		},
		FreshContext: func(_ context.Context, messages []providers.ChatMessage, head, fixed, target int) ([]providers.ChatMessage, error) {
			return buildFreshContext(messages, head, fixed, target)
		},
	}
	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HistoryRewritten {
		t.Fatal("expected a fresh window before the provider request")
	}
	if len(step.calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(step.calls))
	}
	if got := len(step.calls[0].Messages); got >= len(history) {
		t.Fatalf("provider saw %d messages from a %d-message history", got, len(history))
	}
}

func TestRunToolLoop_UndercountedLocalUsageCompactsBeforeRequest(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "system", Content: "system"},
		userMsg("review the change"),
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("c%d", i)
		history = append(history,
			providers.ChatMessage{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID: id, Name: "bash", Arguments: `{"command":"` + strings.Repeat("x", 4000) + `"}`,
			}}},
			providers.ChatMessage{Role: "tool", Name: "bash", ToolCallID: id, Content: strings.Repeat("y", 4000)},
		)
	}
	history = append(history, userMsg("合并了吗"))
	tracker := NewUsageTracker()
	tracker.RecordPendingMessages([]providers.ChatMessage{{Role: "user", Content: "short"}})
	step := &fakeStep{results: []StepResult{{Content: "merged"}}}
	compactCalled := 0
	cfg := LoopConfig{
		Model:        "grok-4.6",
		UsageTracker: tracker,
		Compact: func(context.Context, []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return []providers.ChatMessage{{Role: "user", Content: "summary"}}, nil
		},
		MaxContextTokens: 1000,
		DefaultMaxTokens: 100,
	}
	cfg.CompactThresholdTokens = localRequestEstimate(history, cfg)
	if tracker.EstimateCurrent() >= cfg.CompactThresholdTokens {
		t.Fatalf("fixture is not an undercount: tracker=%d threshold=%d", tracker.EstimateCurrent(), cfg.CompactThresholdTokens)
	}
	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalled != 1 || !res.HistoryRewritten {
		t.Fatalf("compact=%d rewritten=%v", compactCalled, res.HistoryRewritten)
	}
	if len(step.calls) != 1 || len(step.calls[0].Messages) >= len(history) {
		t.Fatalf("provider saw the unrepaired history: calls=%d", len(step.calls))
	}
}

func TestRunToolLoop_ProviderUsageIsNotReplacedByLocalEstimate(t *testing.T) {
	history := []providers.ChatMessage{{Role: "system", Content: "system"}, userMsg("review")}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("c%d", i)
		history = append(history,
			providers.ChatMessage{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID: id, Name: "bash", Arguments: `{"command":"` + strings.Repeat("x", 4000) + `"}`,
			}}},
			providers.ChatMessage{Role: "tool", Name: "bash", ToolCallID: id, Content: strings.Repeat("y", 4000)},
		)
	}
	tracker := NewUsageTracker()
	tracker.RecordResponse(&providers.TokenUsage{InputTokens: 100, OutputTokens: 20})
	full := localRequestEstimate(history, LoopConfig{})
	if tracker.EstimateCurrent() >= full {
		t.Fatalf("fixture does not separate provider usage from the local estimate: provider=%d local=%d", tracker.EstimateCurrent(), full)
	}
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	compactCalled := 0
	cfg := LoopConfig{
		Model: "grok-4.6",
		Compact: func(context.Context, []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return history[:1], nil
		},
		UsageTracker:           tracker,
		CompactThresholdTokens: full,
		MaxContextTokens:       full + 20_000,
	}
	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalled != 0 || res.HistoryRewritten {
		t.Fatalf("provider baseline was replaced: compact=%d rewritten=%v", compactCalled, res.HistoryRewritten)
	}
	if len(step.calls) != 1 || len(step.calls[0].Messages) != len(history) {
		t.Fatalf("request = %d calls / %d messages, want 1 call and %d messages", len(step.calls), len(step.calls[0].Messages), len(history))
	}
}

func TestRunToolLoop_UnreportedAssistantCountsTowardNextEstimate(t *testing.T) {
	history := []providers.ChatMessage{userMsg("hi")}
	tracker := NewUsageTracker()
	payload := strings.Repeat("token ", 300)
	step := &fakeStep{results: []StepResult{{Content: payload}}}
	cfg := LoopConfig{Model: "m", UsageTracker: tracker, MaxSteps: 1}
	if _, err := RunToolLoop(context.Background(), history, cfg, step); err != nil {
		t.Fatal(err)
	}
	base := localRequestEstimate(history, cfg)
	if got := tracker.EstimateCurrent(); got <= base {
		t.Fatalf("estimate = %d, want it to include the assistant message above %d", got, base)
	}
}

func TestRunToolLoop_ReportedUsageDoesNotDoubleCountAssistant(t *testing.T) {
	history := []providers.ChatMessage{userMsg("hi")}
	tracker := NewUsageTracker()
	step := &fakeStep{results: []StepResult{{
		Content: strings.Repeat("token ", 300),
		Usage:   &providers.TokenUsage{InputTokens: 40, OutputTokens: 10},
	}}}
	cfg := LoopConfig{Model: "m", UsageTracker: tracker, MaxSteps: 1}
	if _, err := RunToolLoop(context.Background(), history, cfg, step); err != nil {
		t.Fatal(err)
	}
	if got := tracker.EstimateCurrent(); got != 50 {
		t.Fatalf("estimate = %d, want provider total 50", got)
	}
}

func TestRunToolLoop_OverflowCompactFiresOnCompactCallback(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	step := &fakeStep{results: []StepResult{{}, {Content: "ok"}}, errs: []error{overflow, nil}}
	var infos []CompactInfo
	cfg := LoopConfig{Model: "m", Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) {
		return m[len(m)-1:], nil
	}, OnCompact: func(info CompactInfo) { infos = append(infos, info) }}
	history := []providers.ChatMessage{
		userMsg("old"),
		{Role: "assistant", Content: "old answer"},
		userMsg("big"),
	}
	_, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Reason != CompactReasonOverflow {
		t.Fatalf("expected one overflow OnCompact, got %+v", infos)
	}
}

func TestRunToolLoop_CompactLifecycleCallbacksWrapProactiveAndReactive(t *testing.T) {
	tests := []struct {
		name   string
		reason CompactReason
		step   *fakeStep
		cfg    LoopConfig
	}{
		{
			name: "proactive", reason: CompactReasonProactive,
			step: &fakeStep{results: []StepResult{{Content: "ok"}}},
			cfg: LoopConfig{MaxContextTokens: 1000, DefaultMaxTokens: 100, UsageTracker: func() *UsageTracker {
				tracker := NewUsageTracker()
				tracker.RecordResponse(&providers.TokenUsage{InputTokens: 950})
				return tracker
			}()},
		},
		{
			name: "reactive", reason: CompactReasonOverflow,
			step: &fakeStep{results: []StepResult{{}, {Content: "ok"}}, errs: []error{
				&providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}, nil,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var events []string
			tt.cfg.Model = "m"
			tt.cfg.Compact = func(context.Context, []providers.ChatMessage) ([]providers.ChatMessage, error) {
				events = append(events, "compact")
				return []providers.ChatMessage{userMsg("summary")}, nil
			}
			tt.cfg.BeforeCompact = func(_ context.Context, reason CompactReason) error {
				events = append(events, "pre:"+string(reason))
				return nil
			}
			tt.cfg.AfterCompact = func(_ context.Context, reason CompactReason, compactErr error) error {
				if compactErr != nil {
					t.Fatalf("compact error passed to post hook: %v", compactErr)
				}
				events = append(events, "post:"+string(reason))
				return nil
			}
			_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("old"), {Role: "assistant", Content: "answer"}, userMsg("new")}, tt.cfg, tt.step)
			if err != nil {
				t.Fatalf("RunToolLoop: %v", err)
			}
			want := []string{"pre:" + string(tt.reason), "compact", "post:" + string(tt.reason)}
			if strings.Join(events, ",") != strings.Join(want, ",") {
				t.Fatalf("lifecycle events = %v, want %v", events, want)
			}
		})
	}
}

func TestRunToolLoop_OverflowCompactFailureEmitsAttempt(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	step := &fakeStep{results: []StepResult{{}}, errs: []error{overflow}}
	compactErr := errors.New("compact failed")
	var attempts []CompactAttemptInfo
	cfg := LoopConfig{
		Model: "m",
		Compact: func(context.Context, []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return nil, compactErr
		},
		OnCompactAttempt: func(info CompactAttemptInfo) { attempts = append(attempts, info) },
	}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("big")}, cfg, step)
	if !errors.Is(err, overflow) {
		t.Fatalf("expected original overflow error, got %v", err)
	}
	if len(attempts) != 1 ||
		attempts[0].Reason != CompactReasonOverflow ||
		attempts[0].Status != CompactAttemptFailed ||
		!strings.Contains(attempts[0].Error, compactErr.Error()) {
		t.Fatalf("expected overflow failed attempt, got %+v", attempts)
	}
}

func TestRunToolLoop_BeforeStepInjectsMessages(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	injected := false
	cfg := LoopConfig{Model: "m", BeforeStep: func() []providers.ChatMessage {
		if injected {
			return nil
		}
		injected = true
		return []providers.ChatMessage{{Role: "user", Content: "follow-up"}}
	}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected one step call, got %d", len(step.calls))
	}
	msgs := step.calls[0].Messages
	visible := visibleMessagesForTest(msgs)
	if len(visible) != 2 {
		t.Fatalf("expected injected message in request, got %d messages", len(msgs))
	}
	if visible[1].Role != "user" || visible[1].Content != "follow-up" {
		t.Fatalf("unexpected injected message: %+v", visible[1])
	}
}

func TestRunToolLoop_BeforeRequestContextAppendsHiddenMessages(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	reminder := wuucontext.FormatSystemReminderBlocks(wuucontext.Block{
		Kind:    wuucontext.BlockEnvironment,
		Title:   "Runtime environment",
		Source:  "test",
		Content: "# Environment\n- CWD: /tmp/project",
	})
	var contexts []RequestContextInfo
	cfg := LoopConfig{
		Model: "m",
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextMessages([]providers.ChatMessage{{
				Role:    "user",
				Name:    wuucontext.SystemReminderMessageName,
				Content: reminder,
			}})
		},
		OnRequestContext: func(info RequestContextInfo) {
			contexts = append(contexts, info)
		},
	}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected one step call, got %d", len(step.calls))
	}
	msgs := step.calls[0].Messages
	if len(msgs) != 2 || msgs[1].Content != reminder || !msgs[1].Hidden {
		t.Fatalf("expected request-only context message, got %+v", msgs)
	}
	if len(res.NewMessages) != 1 {
		t.Fatalf("expected only durable assistant reply, got %+v", res.NewMessages)
	}
	if res.NewMessages[0].Hidden || res.NewMessages[0].Content != "ok" {
		t.Fatalf("expected visible assistant reply, got %+v", res.NewMessages[0])
	}
	if len(contexts) != 1 {
		t.Fatalf("expected one request context summary, got %+v", contexts)
	}
	if contexts[0].StepIndex != 0 || contexts[0].TransientMessages != 1 || contexts[0].ContentBytes == 0 {
		t.Fatalf("unexpected request context metadata: %+v", contexts[0])
	}
	if !containsString(contexts[0].BlockKinds, string(wuucontext.BlockEnvironment)) {
		t.Fatalf("request context missing environment block: %+v", contexts[0])
	}
	if containsString(contexts[0].BlockKinds, "TASK") ||
		containsString(contexts[0].BlockKinds, "CONSTRAINT_LEDGER") {
		t.Fatalf("single-turn request should not synthesize task contract: %+v", contexts[0])
	}
	if len(contexts[0].BlockKinds) != 1 {
		t.Fatalf("unexpected request context block kinds: %+v", contexts[0])
	}
	if contexts[0].SegmentLifecycleCounts[string(ContextSegmentRequestOnly)] != 1 ||
		contexts[0].SegmentPlacementCounts[string(ContextSegmentAfterHistory)] != 1 ||
		contexts[0].SegmentCachePolicyCounts[string(ContextSegmentVolatile)] != 1 {
		t.Fatalf("unexpected request context segment policy metrics: %+v", contexts[0])
	}
}

func TestRequestOnlyContextBlocksOwnTypedBlockProjection(t *testing.T) {
	block := wuucontext.Block{
		Kind:    wuucontext.BlockEnvironment,
		Title:   "Runtime environment",
		Source:  "runtime.snapshot",
		Content: "# Environment\n- CWD: /tmp/project",
	}
	segments := RequestOnlyContextBlocks([]wuucontext.Block{block})
	if len(segments) != 1 {
		t.Fatalf("expected one context segment, got %+v", segments)
	}
	segment := segments[0]
	if segment.Lifecycle != ContextSegmentRequestOnly || segment.Placement != ContextSegmentAfterHistory || segment.CachePolicy != ContextSegmentVolatile || segment.Durable || segment.VisibleInUI {
		t.Fatalf("unexpected segment policy: %+v", segment)
	}
	if len(segment.Blocks) != 1 || segment.Blocks[0].Kind != wuucontext.BlockEnvironment {
		t.Fatalf("segment should retain typed blocks: %+v", segment)
	}
	if len(segment.Messages) != 1 {
		t.Fatalf("segment should include provider projection: %+v", segment)
	}
	msg := segment.Messages[0]
	if msg.Role != "user" || !msg.Hidden || !wuucontext.IsSystemReminder(msg.Name, msg.Content) || !strings.Contains(msg.Content, "[ENVIRONMENT]") {
		t.Fatalf("unexpected provider projection: %+v", msg)
	}
}

func TestRunToolLoop_TypedRequestOnlyBlocksStayOutOfDurableHistory(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	block := wuucontext.Block{
		Kind:    wuucontext.BlockEnvironment,
		Title:   "Runtime environment",
		Source:  "runtime.snapshot",
		Content: "# Environment\n- CWD: /tmp/project",
	}
	var contexts []RequestContextInfo
	cfg := LoopConfig{
		Model: "m",
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextBlocks([]wuucontext.Block{block})
		},
		OnRequestContext: func(info RequestContextInfo) {
			contexts = append(contexts, info)
		},
	}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected one provider call, got %d", len(step.calls))
	}
	request := step.calls[0].Messages
	if len(request) != 2 || !request[1].Hidden || !strings.Contains(request[1].Content, "[ENVIRONMENT]") {
		t.Fatalf("typed block should render once as request-only provider context: %+v", request)
	}
	if len(res.NewMessages) != 1 || res.NewMessages[0].Hidden || strings.Contains(res.NewMessages[0].Content, "[ENVIRONMENT]") {
		t.Fatalf("typed request context should not enter durable new messages: %+v", res.NewMessages)
	}
	if len(contexts) != 1 || !containsString(contexts[0].BlockKinds, string(wuucontext.BlockEnvironment)) {
		t.Fatalf("request shape should include typed block kind: %+v", contexts)
	}
	if contexts[0].SegmentLifecycleCounts[string(ContextSegmentRequestOnly)] != 1 ||
		contexts[0].SegmentPlacementCounts[string(ContextSegmentAfterHistory)] != 1 ||
		contexts[0].SegmentCachePolicyCounts[string(ContextSegmentVolatile)] != 1 {
		t.Fatalf("request shape should include typed block segment policy: %+v", contexts[0])
	}
}

func TestRunToolLoop_RetainedRequestOnlyContextCountsInUsageBaseline(t *testing.T) {
	requestOnly := []providers.ChatMessage{{
		Role:    "user",
		Content: strings.Repeat("dynamic context ", 40),
		Hidden:  true,
	}}
	step := &fakeStep{results: []StepResult{{
		Content: "ok",
		Usage:   &providers.TokenUsage{InputTokens: 1000, OutputTokens: 25},
	}}}
	tracker := NewUsageTracker()
	cfg := LoopConfig{
		Model:        "m",
		UsageTracker: tracker,
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextMessages(requestOnly)
		},
	}

	if _, err := RunToolLoop(context.Background(), []providers.ChatMessage{{Role: "system", Content: "sys"}}, cfg, step); err != nil {
		t.Fatal(err)
	}

	// Request-only context is retained in the provider transcript and re-sent
	// on every later request of the run, so the compaction estimate must keep
	// the provider-reported total intact instead of subtracting it.
	if got := tracker.LastResponseTotal(); got != 1025 {
		t.Fatalf("retained request-only context must count in usage baseline: got %d, want 1025", got)
	}
	if got := tracker.PendingDelta(); got != 0 {
		t.Fatalf("request-only context should not remain pending, got %d", got)
	}
}

func TestRunToolLoop_RequestOnlyContextNotTrackedOnRequestError(t *testing.T) {
	requestOnly := []providers.ChatMessage{{
		Role:    "user",
		Content: strings.Repeat("dynamic context ", 40),
		Hidden:  true,
	}}
	step := &fakeStep{
		results: []StepResult{{}},
		errs:    []error{errors.New("boom")},
	}
	tracker := NewUsageTracker()
	cfg := LoopConfig{
		Model:        "m",
		UsageTracker: tracker,
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextMessages(requestOnly)
		},
	}

	history := []providers.ChatMessage{{Role: "system", Content: "sys"}}
	if _, err := RunToolLoop(context.Background(), history, cfg, step); err == nil {
		t.Fatal("expected request error")
	}
	// The durable transcript is reconciled before the send. Request-only
	// context is not part of that total and must not be committed when the
	// request fails.
	if got, want := tracker.PendingDelta(), localRequestEstimate(history, cfg); got != want {
		t.Fatalf("pending delta = %d, want durable estimate %d", got, want)
	}
	if got := tracker.LastResponseTotal(); got != 0 {
		t.Fatalf("request-only context should not create a response baseline on error, got %d", got)
	}
}

func TestRunToolLoop_SplitHiddenContextSkipsUnchangedBlocks(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	activeFiles := wuucontext.Block{
		Kind:    wuucontext.BlockActiveFiles,
		Title:   "Active files",
		Source:  "runtime.active_files",
		Content: "files:\n- go.mod",
	}
	oldEnv := wuucontext.Block{
		Kind:    wuucontext.BlockEnvironment,
		Title:   "Runtime environment",
		Source:  "runtime.snapshot",
		Content: "# Environment\n- CWD: /tmp/project\n- Git status: clean",
	}
	newEnv := oldEnv
	newEnv.Content = "# Environment\n- CWD: /tmp/project\n- Git status: 1 changed file"

	cfg := LoopConfig{
		Model: "m",
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextMessages([]providers.ChatMessage{
				hiddenReminderForTest(activeFiles, 0),
				hiddenReminderForTest(newEnv, 0),
			})
		},
	}
	history := []providers.ChatMessage{
		userMsg("hi"),
		hiddenReminderForTest(activeFiles, 0),
		hiddenReminderForTest(oldEnv, 0),
	}
	res, err := RunToolLoop(context.Background(), history, cfg, step)
	if err != nil {
		t.Fatal(err)
	}

	request := step.calls[0].Messages
	if got := countMessagesContaining(request, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("unchanged active-files block should not be re-appended, got %d active-files messages in %+v", got, request)
	}
	if got := countMessagesContaining(request, "Git status: clean"); got != 0 {
		t.Fatalf("stale boundary environment block should be filtered before request, got %d in %+v", got, request)
	}
	if got := countMessagesContaining(request, "1 changed file"); got != 1 {
		t.Fatalf("changed environment block should be appended once, got %d in %+v", got, request)
	}
	for _, msg := range res.NewMessages {
		if strings.Contains(msg.Content, "[ACTIVE_FILES]") {
			t.Fatalf("unchanged active-files block should not be returned as a new message: %+v", res.NewMessages)
		}
	}
}

func TestRunToolLoop_SplitHiddenContextDoesNotReappendStableBlocksBetweenToolSteps(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
		{Content: "ok"},
	}}
	activeFiles := wuucontext.Block{
		Kind:    wuucontext.BlockActiveFiles,
		Title:   "Active files",
		Source:  "runtime.active_files",
		Content: "files:\n- go.mod",
	}
	contextCalls := 0
	var contexts []RequestContextInfo
	cfg := LoopConfig{
		Model: "m",
		Tools: &fakeLoopTools{
			defs:    []providers.ToolDefinition{{Name: "read_file"}},
			results: map[string]string{"call_1": `{"content":"hello"}`},
		},
		BeforeRequestContext: func() []ContextSegment {
			contextCalls++
			env := wuucontext.Block{
				Kind:    wuucontext.BlockEnvironment,
				Title:   "Runtime environment",
				Source:  "runtime.snapshot",
				Content: fmt.Sprintf("# Environment\n- State: step %d", contextCalls),
			}
			return RequestOnlyContextMessages([]providers.ChatMessage{
				hiddenReminderForTest(activeFiles, 0),
				hiddenReminderForTest(env, 0),
			})
		},
		OnRequestContext: func(info RequestContextInfo) {
			contexts = append(contexts, info)
		},
	}

	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("inspect the repo")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "ok" {
		t.Fatalf("unexpected content %q", res.Content)
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(step.calls))
	}
	if len(contexts) != 2 {
		t.Fatalf("expected two context observations, got %+v", contexts)
	}
	if !containsString(contexts[0].BlockKinds, string(wuucontext.BlockActiveFiles)) {
		t.Fatalf("first request should include active-files context: %+v", contexts[0])
	}
	if containsString(contexts[1].BlockKinds, string(wuucontext.BlockActiveFiles)) {
		t.Fatalf("unchanged active-files block should not be re-injected on the second round: %+v", contexts[1])
	}
	if !containsString(contexts[1].BlockKinds, string(wuucontext.BlockEnvironment)) {
		t.Fatalf("second request should refresh changed environment: %+v", contexts[1])
	}

	first := step.calls[0].Messages
	second := step.calls[1].Messages
	if err := providers.ValidateToolCallHistory(second); err != nil {
		t.Fatalf("second request must keep provider-valid tool history: %v\n%+v", err, second)
	}
	if len(second) < len(first) || !reflect.DeepEqual(first, second[:len(first)]) {
		t.Fatalf("second request must extend the first request prefix:\nfirst=%+v\nsecond=%+v", first, second)
	}
	// The retained copy from round one is the only copy: an unchanged stable
	// block must appear exactly once per request, not once per round.
	if got := countMessagesContaining(first, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("first request should carry the active-files block once, got %d in %+v", got, first)
	}
	if got := countMessagesContaining(second, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("unchanged active-files block duplicated across rounds: got %d in %+v", got, second)
	}
	if got := countMessagesContaining(second, "State: step 1"); got != 1 {
		t.Fatalf("second request should retain the first environment snapshot once, got %d in %+v", got, second)
	}
	if got := countMessagesContaining(second, "State: step 2"); got != 1 {
		t.Fatalf("second request should append the changed environment snapshot once, got %d in %+v", got, second)
	}
}

func TestRunToolLoop_RefreshesHiddenModelContextBetweenToolSteps(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
		{ToolCalls: []providers.ToolCall{{ID: "call_2", Name: "grep", Arguments: `{"query":"cache"}`}}},
		{Content: "ok"},
	}}

	contextCalls := 0
	cfg := LoopConfig{
		Model: "m",
		Tools: &fakeLoopTools{
			defs: []providers.ToolDefinition{{Name: "read_file"}, {Name: "grep"}},
			results: map[string]string{
				"call_1": `{"content":"hello"}`,
				"call_2": `{"matches":[]}`,
			},
		},
		BeforeRequestContext: func() []ContextSegment {
			contextCalls++
			block := wuucontext.Block{
				Kind:    wuucontext.BlockEnvironment,
				Title:   "Runtime environment",
				Source:  "runtime.snapshot",
				Content: fmt.Sprintf("# Environment\n- State: step %d", contextCalls),
			}
			return RequestOnlyContextMessages([]providers.ChatMessage{hiddenReminderForTest(block, 0)})
		},
	}

	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("inspect the repo")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "ok" {
		t.Fatalf("unexpected content %q", res.Content)
	}
	if len(step.calls) != 3 {
		t.Fatalf("expected three provider calls, got %d", len(step.calls))
	}
	first := step.calls[0].Messages
	if got := countMessagesContaining(first, "State: step 1"); got != 1 {
		t.Fatalf("first request should include first context once, got %d in %+v", got, first)
	}

	second := step.calls[1].Messages
	if err := providers.ValidateToolCallHistory(second); err != nil {
		t.Fatalf("second request must keep provider-valid tool history: %v\n%+v", err, second)
	}
	if len(second) < len(first) || !reflect.DeepEqual(first, second[:len(first)]) {
		t.Fatalf("second request must extend the first request prefix:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if got := countMessagesContaining(second, "State: step 1"); got != 1 {
		t.Fatalf("second request should retain prior request-only context for cache continuity, got %d in %+v", got, second)
	}
	if got := countMessagesContaining(second, "State: step 2"); got != 1 {
		t.Fatalf("second request should include latest request-only context once, got %d in %+v", got, second)
	}
	third := step.calls[2].Messages
	if err := providers.ValidateToolCallHistory(third); err != nil {
		t.Fatalf("third request must keep provider-valid tool history: %v\n%+v", err, third)
	}
	if len(third) < len(second) || !reflect.DeepEqual(second, third[:len(second)]) {
		t.Fatalf("third request must extend the second request prefix:\nsecond=%+v\nthird=%+v", second, third)
	}
	if got := countMessagesContaining(third, "State: step 3"); got != 1 {
		t.Fatalf("third request should include latest request-only context once, got %d in %+v", got, third)
	}
	if got := countMessagesContaining(second, "[TASK]"); got != 0 {
		t.Fatalf("single-directive tool loop should not synthesize task block, got %d in %+v", got, second)
	}
	if got := countMessagesContaining(second, "[CONSTRAINT_LEDGER]"); got != 0 {
		t.Fatalf("single-directive tool loop should not synthesize constraint ledger, got %d in %+v", got, second)
	}
	if len(res.NewMessages) != 5 {
		t.Fatalf("expected only durable assistant/tool/final messages, got %+v", res.NewMessages)
	}
	for _, msg := range res.NewMessages {
		if msg.Hidden {
			t.Fatalf("request-only context should not be returned as durable history: %+v", res.NewMessages)
		}
	}
}

func TestRunToolLoop_RetainedContextExtendsPriorRunRequestPrefix(t *testing.T) {
	activeFiles := wuucontext.Block{
		Kind:    wuucontext.BlockActiveFiles,
		Title:   "Active files",
		Source:  "runtime.active_files",
		Content: "files:\n- go.mod",
	}
	// Context is unchanged across turns, so the new turn re-affirms it and it
	// is spliced back at its recorded position for byte-level continuity.
	makeCfg := func(retained *RetainedRequestContextState) LoopConfig {
		return LoopConfig{
			Model: "m",
			Tools: &fakeLoopTools{
				defs: []providers.ToolDefinition{{Name: "read_file"}},
				results: map[string]string{
					"call_1": `{"content":"hello"}`,
					"call_2": `{"content":"world"}`,
				},
			},
			BeforeRequestContext: func() []ContextSegment {
				env := wuucontext.Block{
					Kind:    wuucontext.BlockEnvironment,
					Title:   "Runtime environment",
					Source:  "runtime.snapshot",
					Content: "# Environment\n- State: steady",
				}
				return RequestOnlyContextBlocks([]wuucontext.Block{activeFiles, env})
			},
			RetainedRequestContext: retained,
		}
	}

	step1 := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
		{Content: "done"},
	}}
	history1 := []providers.ChatMessage{userMsg("first ask")}
	res1, err := RunToolLoop(context.Background(), history1, makeCfg(nil), step1)
	if err != nil {
		t.Fatal(err)
	}
	if res1.RetainedRequestContext == nil {
		t.Fatal("run with request-only context should return retained state for the next run")
	}
	run1Last := step1.calls[len(step1.calls)-1].Messages

	history2 := append(append(append([]providers.ChatMessage(nil), history1...), res1.NewMessages...), userMsg("second ask"))
	step2 := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "call_2", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
		{Content: "ok"},
	}}
	res2, err := RunToolLoop(context.Background(), history2, makeCfg(res1.RetainedRequestContext), step2)
	if err != nil {
		t.Fatal(err)
	}

	run2First := step2.calls[0].Messages
	if len(run2First) < len(run1Last) || !reflect.DeepEqual(run1Last, run2First[:len(run1Last)]) {
		t.Fatalf("first request of run 2 must byte-extend the last request of run 1:\nrun1last=%+v\nrun2first=%+v", run1Last, run2First)
	}
	// The unchanged blocks are retained (spliced), not re-emitted: exactly one
	// copy each across both runs.
	if got := countMessagesContaining(run2First, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("unchanged stable block should appear exactly once across runs, got %d in %+v", got, run2First)
	}
	if got := countMessagesContaining(run2First, "State: steady"); got != 1 {
		t.Fatalf("unchanged env snapshot should be retained exactly once, got %d in %+v", got, run2First)
	}
	if res2.RetainedRequestContext == nil {
		t.Fatal("run 2 should hand retained state forward for run 3")
	}
}

func TestRunToolLoop_StaleRetainedContextFallsBackToFreshTranscript(t *testing.T) {
	block := wuucontext.Block{
		Kind:    wuucontext.BlockEnvironment,
		Title:   "Runtime environment",
		Source:  "runtime.snapshot",
		Content: "# Environment\n- State: turn 1",
	}
	cfg := LoopConfig{
		Model: "m",
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextBlocks([]wuucontext.Block{block})
		},
	}
	step1 := &fakeStep{results: []StepResult{{Content: "done"}}}
	history1 := []providers.ChatMessage{userMsg("first ask")}
	res1, err := RunToolLoop(context.Background(), history1, cfg, step1)
	if err != nil {
		t.Fatal(err)
	}
	if res1.RetainedRequestContext == nil {
		t.Fatal("expected retained state from run 1")
	}

	// The durable history was rewritten between runs (edit, fork, external
	// compaction) — the fingerprint no longer matches, so the state must be
	// dropped and the run must inject fresh context without duplicates.
	rewritten := []providers.ChatMessage{
		userMsg("first ask (edited)"),
		{Role: "assistant", Content: "done"},
		userMsg("second ask"),
	}
	cfg2 := cfg
	cfg2.RetainedRequestContext = res1.RetainedRequestContext
	step2 := &fakeStep{results: []StepResult{{Content: "ok"}}}
	if _, err := RunToolLoop(context.Background(), rewritten, cfg2, step2); err != nil {
		t.Fatal(err)
	}
	request := step2.calls[0].Messages
	if got := countMessagesContaining(request, "State: turn 1"); got != 1 {
		t.Fatalf("stale retained state must be dropped and context injected fresh exactly once, got %d in %+v", got, request)
	}
	if got := countMessagesContaining(request, "first ask (edited)"); got != 1 {
		t.Fatalf("rewritten history must be used as-is, got %d in %+v", got, request)
	}
}

func TestRunToolLoop_BeforeRequestTransformStaysRequestScoped(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
		{ToolCalls: []providers.ToolCall{{ID: "call_2", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
		{Content: "ok"},
	}}
	cfg := LoopConfig{
		Model: "m",
		Tools: &fakeLoopTools{
			defs: []providers.ToolDefinition{{Name: "read_file"}},
			results: map[string]string{
				"call_1": `{"content":"hello"}`,
				"call_2": `{"content":"world"}`,
			},
		},
		BeforeRequest: func(_ context.Context, req *providers.ChatRequest) error {
			req.Messages = append(req.Messages, providers.ChatMessage{
				Role:    "user",
				Content: "per-request plugin injection",
				Hidden:  true,
			})
			return nil
		},
	}

	if _, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("go")}, cfg, step); err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 3 {
		t.Fatalf("expected three provider calls, got %d", len(step.calls))
	}
	// The transform runs on an isolated per-request copy: its output must
	// never be folded back into the transcript, or each round would compound
	// the previous rounds' injections.
	for i, call := range step.calls {
		if got := countMessagesContaining(call.Messages, "per-request plugin injection"); got != 1 {
			t.Fatalf("request %d should carry exactly one per-request injection, got %d in %+v", i+1, got, call.Messages)
		}
	}
}

func TestRunToolLoop_FiltersStaleInternalContextWithoutTaskContract(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	reminder := wuucontext.FormatSystemReminderBlocks(wuucontext.Block{
		Kind:    wuucontext.BlockEnvironment,
		Source:  "test",
		Content: "ignore me",
	})
	history := []providers.ChatMessage{
		userMsg("first request"),
		{
			Role:    "user",
			Name:    wuucontext.SystemReminderMessageName,
			Content: reminder,
		},
		{
			Role:    "user",
			Name:    wuucontext.TaskContractMessageName,
			Content: "hidden task contract should not echo",
			Hidden:  true,
		},
		userMsg("second request"),
	}

	var contexts []RequestContextInfo
	_, err := RunToolLoop(context.Background(), history, LoopConfig{
		Model: "m",
		BeforeRequestContext: func() []ContextSegment {
			return RequestOnlyContextMessages([]providers.ChatMessage{{
				Role:    "user",
				Name:    wuucontext.SystemReminderMessageName,
				Content: reminder,
			}})
		},
		OnRequestContext: func(info RequestContextInfo) {
			contexts = append(contexts, info)
		},
	}, step)
	if err != nil {
		t.Fatal(err)
	}
	msgs := step.calls[0].Messages
	if len(msgs) != 3 {
		t.Fatalf("expected stale internal context to be replaced before request, got %+v", msgs)
	}
	for _, msg := range msgs[:len(msgs)-1] {
		if msg.Name == wuucontext.SystemReminderMessageName ||
			msg.Name == wuucontext.TaskContractMessageName {
			t.Fatalf("stale model context should not remain in request history: %+v", msgs)
		}
	}
	env := msgs[len(msgs)-1]
	if env.Name != wuucontext.SystemReminderMessageName || !env.Hidden {
		t.Fatalf("expected refreshed request-only context after durable history, got %+v", msgs)
	}
	if len(contexts) != 1 {
		t.Fatalf("expected one request context summary, got %+v", contexts)
	}
	for _, unexpected := range []string{"[TASK]", "[CONSTRAINT_LEDGER]"} {
		if got := countMessagesContaining(msgs, unexpected); got != 0 {
			t.Fatalf("default request should not synthesize %s, got %+v", unexpected, msgs)
		}
		if containsString(contexts[0].BlockKinds, strings.Trim(unexpected, "[]")) {
			t.Fatalf("request telemetry should not report %s: %+v", unexpected, contexts[0])
		}
	}
}

func TestRunToolLoop_EmptyAnswerWithoutStopReasonIsError(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "  "}}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, LoopConfig{Model: "m"}, step)
	if err == nil || !IsEmptyAnswer(err) {
		t.Fatalf("expected EmptyAnswerError, got %v", err)
	}
}

func TestRunToolLoop_EmptyAnswerCarriesStopReason(t *testing.T) {
	step := &fakeStep{results: []StepResult{{StopReason: "unexpected_stop"}}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, LoopConfig{Model: "m"}, step)
	if err == nil || !IsEmptyAnswer(err) {
		t.Fatalf("expected EmptyAnswerError, got %v", err)
	}
	var emptyErr *EmptyAnswerError
	if !errors.As(err, &emptyErr) || emptyErr.StopReason != "unexpected_stop" {
		t.Fatalf("expected original stop reason, got %+v", emptyErr)
	}
}

func TestRunToolLoop_EmptyAnswerWithNaturalStopReasonSucceeds(t *testing.T) {
	for _, stop := range []string{"end_turn", "completed", "stop"} {
		t.Run(stop, func(t *testing.T) {
			step := &fakeStep{results: []StepResult{{Content: "  ", StopReason: stop, Usage: &providers.TokenUsage{OutputTokens: 4}}}}
			res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, LoopConfig{Model: "m"}, step)
			if err != nil || res.FinishReason != providers.FinishReasonStop || res.StopReason != stop {
				t.Fatalf("expected normal empty completion, got %+v, %v", res, err)
			}
			if res.Content != "" || len(res.DurableNewMessages) != 0 || len(step.calls) != 1 || res.OutputTokens != 4 {
				t.Fatalf("empty completion retried, lost usage or fabricated text: calls=%d result=%+v", len(step.calls), res)
			}
		})
	}
}

func TestRunToolLoop_EmptyCompletionAfterToolResults(t *testing.T) {
	tools := &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "read_file"}}, results: map[string]string{"inspect": "review result"}}
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "inspect", Name: "read_file", Arguments: `{}`}}},
		{StopReason: "completed"},
	}}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("Review this delivery")}, LoopConfig{Model: "m", Tools: tools}, step)
	if err != nil || len(step.calls) != 2 || len(res.DurableNewMessages) != 2 || res.Content != "" {
		t.Fatalf("tool work did not settle normally: calls=%d result=%+v err=%v", len(step.calls), res, err)
	}
	last := step.calls[1].Messages[len(step.calls[1].Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "inspect" {
		t.Fatalf("completion skipped the tool result: %+v", last)
	}
}

func TestRunToolLoop_ReasoningOnlyAnswerStillPersistsAssistantMessage(t *testing.T) {
	step := &fakeStep{results: []StepResult{{
		Content:          " ",
		ReasoningContent: "inspect repo before reply",
		StopReason:       "end_turn",
	}}}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, LoopConfig{Model: "m"}, step)
	if err != nil {
		t.Fatalf("expected reasoning-only completion to succeed, got %v", err)
	}
	visible := visibleMessagesForTest(res.NewMessages)
	if len(visible) != 1 {
		t.Fatalf("expected reasoning-only assistant message to persist, got %+v", res.NewMessages)
	}
	if got := visible[0].ReasoningContent; got != "inspect repo before reply" {
		t.Fatalf("unexpected reasoning content: %q", got)
	}
}

func TestRunToolLoop_RejectsProviderToolCallWithoutID(t *testing.T) {
	step := &fakeStep{results: []StepResult{{ToolCalls: []providers.ToolCall{{ID: "", Name: "t", Arguments: `{}`}}}}}
	_, err := RunToolLoop(context.Background(), nil, LoopConfig{Model: "m"}, step)
	if err == nil {
		t.Fatal("expected invalid tool_call error")
	}
	if !strings.Contains(err.Error(), "provider returned invalid tool_calls") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunToolLoop_RejectsDuplicateProviderToolCallIDs(t *testing.T) {
	step := &fakeStep{results: []StepResult{{ToolCalls: []providers.ToolCall{
		{ID: "call_1", Name: "a", Arguments: `{}`},
		{ID: "call_1", Name: "b", Arguments: `{}`},
	}}}}
	_, err := RunToolLoop(context.Background(), nil, LoopConfig{Model: "m"}, step)
	if err == nil {
		t.Fatal("expected duplicate tool_call id error")
	}
	if !strings.Contains(err.Error(), "provider returned invalid tool_calls") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunToolLoop_ReturnsInvalidToolArgumentsToModel(t *testing.T) {
	step := &fakeStep{results: []StepResult{{ToolCalls: []providers.ToolCall{
		{ID: "call_1", Name: "update_todo", Arguments: `{"todos": `},
	}}, {Content: "recovered"}}}
	tools := &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "update_todo"}}}
	var persisted []providers.ChatMessage
	result, err := RunToolLoop(context.Background(), nil, LoopConfig{
		Model: "m",
		Tools: tools,
		OnMessage: func(msg providers.ChatMessage) {
			persisted = append(persisted, msg)
		},
	}, step)
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if result.Content != "recovered" {
		t.Fatalf("unexpected result content: %q", result.Content)
	}
	if len(persisted) != 3 {
		t.Fatalf("expected assistant, tool, final assistant messages, got %+v", persisted)
	}
	if len(persisted[0].ToolCalls) != 1 || persisted[0].ToolCalls[0].Arguments != `{"todos": ` {
		t.Fatalf("invalid tool call should be persisted for pairing, got %+v", persisted[0])
	}
	if persisted[1].Role != "tool" || persisted[1].ToolCallID != "call_1" || !strings.Contains(persisted[1].Content, `"error_kind":"invalid_tool_arguments"`) {
		t.Fatalf("expected invalid tool arguments result, got %+v", persisted[1])
	}
	if calls := tools.recordedCalls(); len(calls) != 0 {
		t.Fatalf("invalid tool call must not reach the executor, got %+v", calls)
	}
	if len(step.calls) != 2 {
		t.Fatalf("expected model retry after tool error, got %d calls", len(step.calls))
	}
	retryMessages := step.calls[1].Messages
	if len(retryMessages) != 2 || len(retryMessages[0].ToolCalls) != 1 || retryMessages[1].ToolCallID != "call_1" {
		t.Fatalf("retry should include paired invalid tool call and error result, got %+v", retryMessages)
	}
}
