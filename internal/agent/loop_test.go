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

func TestRunToolLoop_EmptyAnswerIsError(t *testing.T) {
	step := &fakeStep{results: []StepResult{{Content: "  "}}}
	_, err := RunToolLoop(context.Background(), []providers.ChatMessage{userMsg("hi")}, LoopConfig{Model: "m"}, step)
	if err == nil || !strings.Contains(err.Error(), "empty answer") {
		t.Fatalf("expected empty-answer error, got %v", err)
	}
}
