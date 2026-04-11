package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
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
	if err != nil {
		return StepResult{}, err
	}
	return r, nil
}

// fakeLoopTools is a no-op ToolExecutor that records every call.
type fakeLoopTools struct {
	defs    []providers.ToolDefinition
	results map[string]string // call.ID → JSON result
	calls   []providers.ToolCall
	err     error
}

func (f *fakeLoopTools) Definitions() []providers.ToolDefinition { return f.defs }
func (f *fakeLoopTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	f.calls = append(f.calls, call)
	if f.err != nil {
		return "", f.err
	}
	if r, ok := f.results[call.ID]; ok {
		return r, nil
	}
	return `{"ok":true}`, nil
}

func userMsg(content string) providers.ChatMessage {
	return providers.ChatMessage{Role: "user", Content: content}
}

func TestRunToolLoop_SimpleAnswer(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{Content: "hello back"},
	}}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, LoopConfig{Model: "m"}, step)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if res.Content != "hello back" {
		t.Fatalf("got content %q", res.Content)
	}
	// One assistant message added.
	if len(res.NewMessages) != 1 || res.NewMessages[0].Role != "assistant" {
		t.Fatalf("unexpected new messages: %+v", res.NewMessages)
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
	if len(seenCalls) != 1 {
		t.Fatalf("expected OnToolResult to fire once, got %d", len(seenCalls))
	}
	// New messages should include: assistant(toolCall), tool(result), assistant(final).
	roles := []string{}
	for _, m := range res.NewMessages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "assistant,tool,assistant" {
		t.Fatalf("unexpected message order: %v", roles)
	}
}

func TestRunToolLoop_TruncationRecovery(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{Content: "part1 ", Truncated: true, StopReason: "length"},
		{Content: "part2 ", Truncated: true, StopReason: "max_tokens"},
		{Content: "done."},
	}}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("write story")}, LoopConfig{Model: "m"}, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "part1 part2 done." {
		t.Fatalf("expected concatenated content, got %q", res.Content)
	}
	// 3 step calls; the 2nd and 3rd should each see a continue prompt
	// in their messages.
	if len(step.calls) != 3 {
		t.Fatalf("expected 3 step calls, got %d", len(step.calls))
	}
	final := step.calls[2].Messages
	continues := 0
	for _, m := range final {
		if m.Role == "user" && m.Content == truncationContinuePrompt {
			continues++
		}
	}
	if continues != 2 {
		t.Fatalf("expected 2 continue prompts in final request, got %d", continues)
	}
}

func TestRunToolLoop_TruncationCappedReturnsPartial(t *testing.T) {
	results := make([]StepResult, 0, maxTruncationRecoveries+1)
	for i := 0; i <= maxTruncationRecoveries; i++ {
		results = append(results, StepResult{Content: "x", Truncated: true, StopReason: "length"})
	}
	step := &fakeStep{results: results}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("loop")}, LoopConfig{Model: "m"}, step)
	if err != nil {
		t.Fatal(err)
	}
	// 3 buffered + 1 final = "xxxx".
	if res.Content != "xxxx" {
		t.Fatalf("got %q", res.Content)
	}
}

func TestRunToolLoop_ContextOverflowAutoCompact(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	step := &fakeStep{
		results: []StepResult{{}, {Content: "ok"}},
		errs:    []error{overflow, nil},
	}
	compactCalled := 0
	compactFn := func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
		compactCalled++
		// Pretend we summarized: keep the last message only.
		return msgs[len(msgs)-1:], nil
	}
	cfg := LoopConfig{Model: "m", Compact: compactFn}

	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("big")}, cfg, step)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if res.Content != "ok" {
		t.Fatalf("expected ok, got %q", res.Content)
	}
	if compactCalled != 1 {
		t.Fatalf("expected compact called once, got %d", compactCalled)
	}
}

func TestRunToolLoop_ContextOverflowOnlyRetriesOnce(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	step := &fakeStep{
		results: []StepResult{{}, {}},
		errs:    []error{overflow, overflow}, // both attempts overflow
	}
	cfg := LoopConfig{Model: "m", Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) { return m, nil }}

	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("big")}, cfg, step)
	if err == nil {
		t.Fatal("expected second overflow to surface")
	}
	if !providers.IsContextOverflow(err) {
		t.Fatalf("expected context-overflow error, got %v", err)
	}
}

func TestRunToolLoop_MaxStepsExceeded(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{ToolCalls: []providers.ToolCall{{ID: "a", Name: "t", Arguments: `{}`}}},
	}}
	cfg := LoopConfig{
		Model:    "m",
		Tools:    &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}},
		MaxSteps: 1,
	}
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
		results = append(results, StepResult{
			ToolCalls: []providers.ToolCall{{ID: "c", Name: "t", Arguments: `{}`}},
		})
	}
	results = append(results, StepResult{Content: "all done"})

	step := &fakeStep{results: results}
	cfg := LoopConfig{
		Model: "m",
		Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}},
		// MaxSteps: 0 — unlimited
	}
	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("long")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "all done" {
		t.Fatalf("got %q", res.Content)
	}
}

func TestRunToolLoop_OnUsageReceivesPerCall(t *testing.T) {
	step := &fakeStep{results: []StepResult{
		{Content: "done", Usage: &providers.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	}}
	var seenIn, seenOut int
	cfg := LoopConfig{
		Model: "m",
		OnUsage: func(in, out int) {
			seenIn += in
			seenOut += out
		},
	}
	res, _ := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if seenIn != 10 || seenOut != 5 {
		t.Fatalf("OnUsage missed: in=%d out=%d", seenIn, seenOut)
	}
	if res.InputTokens != 10 || res.OutputTokens != 5 {
		t.Fatalf("LoopResult totals wrong: %+v", res)
	}
}

func TestRunToolLoop_ProactiveCompactTriggers(t *testing.T) {
	// First step reports a usage that exceeds the threshold (90% of
	// 1000 = 900). The loop should call cfg.Compact and the second
	// step should run on the compacted history.
	step := &fakeStep{results: []StepResult{
		{
			ToolCalls: []providers.ToolCall{{ID: "c1", Name: "t", Arguments: `{}`}},
			Usage:     &providers.TokenUsage{InputTokens: 950, OutputTokens: 0},
		},
		{Content: "compacted answer"},
	}}
	tools := &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}}

	compactCalled := 0
	compactFn := func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
		compactCalled++
		// Return strictly fewer messages than input to satisfy the
		// "compact actually shrunk the history" guard.
		return []providers.ChatMessage{{Role: "user", Content: "summary"}}, nil
	}
	var compactInfos []CompactInfo
	cfg := LoopConfig{
		Model:            "m",
		Tools:            tools,
		Compact:          compactFn,
		MaxContextTokens: 1000,
		// Default threshold = 0.9 → 900 tokens.
		OnCompact: func(info CompactInfo) { compactInfos = append(compactInfos, info) },
	}

	res, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
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
	if res.Content != "compacted answer" {
		t.Fatalf("expected compacted answer, got %q", res.Content)
	}
}

func TestRunToolLoop_ProactiveCompactDisabledWhenNoWindow(t *testing.T) {
	// MaxContextTokens=0 disables proactive compact entirely; even a
	// huge usage doesn't trigger.
	step := &fakeStep{results: []StepResult{
		{Content: "done", Usage: &providers.TokenUsage{InputTokens: 1_000_000, OutputTokens: 0}},
	}}
	compactCalled := 0
	cfg := LoopConfig{
		Model: "m",
		Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return m, nil
		},
		// MaxContextTokens left zero
	}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalled != 0 {
		t.Fatalf("proactive compact should be disabled, but ran %d times", compactCalled)
	}
}

func TestRunToolLoop_ProactiveCompactRespectsCustomThreshold(t *testing.T) {
	// 50% of 1000 = 500. First step reports 600 → should compact.
	step := &fakeStep{results: []StepResult{
		{
			ToolCalls: []providers.ToolCall{{ID: "c", Name: "t", Arguments: `{}`}},
			Usage:     &providers.TokenUsage{InputTokens: 600},
		},
		{Content: "ok"},
	}}
	compactCalled := 0
	cfg := LoopConfig{
		Model: "m",
		Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}},
		Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return []providers.ChatMessage{{Role: "user", Content: "sum"}}, nil
		},
		MaxContextTokens:    1000,
		CompactThresholdPct: 0.5,
	}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalled != 1 {
		t.Fatalf("expected proactive compact at 50%% threshold, got %d", compactCalled)
	}
}

func TestRunToolLoop_ProactiveCompactDoesNotLoopOnNoOpCompact(t *testing.T) {
	// If Compact returns the same number of messages (no shrinkage),
	// the loop must NOT trigger another proactive compact next round.
	// Otherwise we'd thrash forever.
	step := &fakeStep{results: []StepResult{
		{
			ToolCalls: []providers.ToolCall{{ID: "c1", Name: "t", Arguments: `{}`}},
			Usage:     &providers.TokenUsage{InputTokens: 950},
		},
		{
			ToolCalls: []providers.ToolCall{{ID: "c2", Name: "t", Arguments: `{}`}},
			Usage:     &providers.TokenUsage{InputTokens: 950},
		},
		{Content: "done"},
	}}
	compactCalled := 0
	cfg := LoopConfig{
		Model: "m",
		Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}},
		Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) {
			compactCalled++
			return m, nil // no-op: same length
		},
		MaxContextTokens: 1000,
	}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	// Should be called each round it's eligible, but the no-op result
	// is rejected — no infinite loop.
	if compactCalled < 1 {
		t.Fatalf("expected at least one compact attempt, got %d", compactCalled)
	}
}

func TestRunToolLoop_OverflowCompactFiresOnCompactCallback(t *testing.T) {
	// Reactive overflow should also call the OnCompact callback
	// (with reason=overflow).
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	step := &fakeStep{
		results: []StepResult{{}, {Content: "ok"}},
		errs:    []error{overflow, nil},
	}
	var infos []CompactInfo
	cfg := LoopConfig{
		Model: "m",
		Compact: func(_ context.Context, m []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return m[len(m)-1:], nil
		},
		OnCompact: func(info CompactInfo) { infos = append(infos, info) },
	}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("big")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Reason != CompactReasonOverflow {
		t.Fatalf("expected one overflow OnCompact, got %+v", infos)
	}
}

func TestRunToolLoop_BeforeStepInjectsMessages(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "ok"}}}
	injected := false
	cfg := LoopConfig{
		Model: "m",
		BeforeStep: func() []providers.ChatMessage {
			if injected {
				return nil
			}
			injected = true
			return []providers.ChatMessage{{Role: "user", Content: "follow-up"}}
		},
	}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, cfg, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(step.calls) != 1 {
		t.Fatalf("expected one step call, got %d", len(step.calls))
	}
	msgs := step.calls[0].Messages
	if len(msgs) != 2 {
		t.Fatalf("expected injected message in request, got %d messages", len(msgs))
	}
	if msgs[1].Role != "user" || msgs[1].Content != "follow-up" {
		t.Fatalf("unexpected injected message: %+v", msgs[1])
	}
}

func TestRunToolLoop_EmptyAnswerIsError(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "  "}}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, LoopConfig{Model: "m"}, step)
	if err == nil || !strings.Contains(err.Error(), "empty answer") {
		t.Fatalf("expected empty-answer error, got %v", err)
	}
}
