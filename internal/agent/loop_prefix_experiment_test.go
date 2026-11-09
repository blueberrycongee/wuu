package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Prefix-continuity experiment sweep.
//
// These tests drive whole simulated sessions (multiple RunToolLoop runs, each
// with multiple tool rounds) and analyze the byte-prefix relation between
// EVERY consecutive pair of provider requests in the session — within runs
// and across run boundaries. Scenarios where the design allows a divergence
// (history rewrite, per-request transform tail, prune-set growth) assert the
// break happens exactly where allowed and that the chain resumes afterwards.

// prefixBreak records that request index pair (i, i+1) diverged, and at which
// message index the first difference appears (-1 = the later request is
// shorter than the earlier one).
type prefixBreak struct {
	pair     int
	msgIndex int
}

func analyzePrefixChain(requests [][]providers.ChatMessage) []prefixBreak {
	var breaks []prefixBreak
	for i := 0; i+1 < len(requests); i++ {
		prev, next := requests[i], requests[i+1]
		if len(next) < len(prev) {
			breaks = append(breaks, prefixBreak{pair: i, msgIndex: -1})
			continue
		}
		diverged := false
		for j := range prev {
			if !reflect.DeepEqual(prev[j], next[j]) {
				breaks = append(breaks, prefixBreak{pair: i, msgIndex: j})
				diverged = true
				break
			}
		}
		_ = diverged
	}
	return breaks
}

func formatBreaks(requests [][]providers.ChatMessage, breaks []prefixBreak) string {
	var b strings.Builder
	for _, brk := range breaks {
		fmt.Fprintf(&b, "requests %d→%d diverge at message %d", brk.pair, brk.pair+1, brk.msgIndex)
		if brk.msgIndex >= 0 && brk.msgIndex < len(requests[brk.pair]) {
			prev := requests[brk.pair][brk.msgIndex]
			fmt.Fprintf(&b, "\n  prev: role=%s name=%s content=%.80q", prev.Role, prev.Name, prev.Content)
			if brk.msgIndex < len(requests[brk.pair+1]) {
				next := requests[brk.pair+1][brk.msgIndex]
				fmt.Fprintf(&b, "\n  next: role=%s name=%s content=%.80q", next.Role, next.Name, next.Content)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// sessionSim drives a multi-run conversation the way a long-lived runner
// does: history accumulates NewMessages, retained request-context state is
// handed from each run to the next.
type sessionSim struct {
	t        *testing.T
	history  []providers.ChatMessage
	retained *RetainedRequestContextState
	requests [][]providers.ChatMessage
}

func (s *sessionSim) runTurn(prompt string, step *fakeStep, mutate func(cfg *LoopConfig)) LoopResult {
	s.t.Helper()
	s.history = append(s.history, userMsg(prompt))
	cfg := LoopConfig{Model: "m", RetainedRequestContext: s.retained}
	if mutate != nil {
		mutate(&cfg)
	}
	res, err := RunToolLoop(context.Background(), s.history, cfg, step)
	if err != nil {
		s.t.Fatalf("turn %q: %v", prompt, err)
	}
	for _, call := range step.calls {
		s.requests = append(s.requests, call.Messages)
	}
	if res.HistoryRewritten {
		s.history = append([]providers.ChatMessage(nil), res.NewMessages...)
	} else {
		s.history = append(s.history, res.NewMessages...)
	}
	s.retained = res.RetainedRequestContext
	return res
}

func experimentTools() *fakeLoopTools {
	return &fakeLoopTools{
		defs: []providers.ToolDefinition{{Name: "read_file"}},
		results: map[string]string{
			"call_1": `{"content":"one"}`,
			"call_2": `{"content":"two"}`,
			"call_3": `{"content":"three"}`,
			"call_4": `{"content":"four"}`,
			"call_5": `{"content":"five"}`,
			"call_6": `{"content":"six"}`,
		},
	}
}

func toolRoundSteps(callIDs ...string) []StepResult {
	steps := make([]StepResult, 0, len(callIDs)+1)
	for _, id := range callIDs {
		steps = append(steps, StepResult{ToolCalls: []providers.ToolCall{{ID: id, Name: "read_file", Arguments: `{"path":"x"}`}}})
	}
	return append(steps, StepResult{Content: "ok"})
}

// stableContext returns request-only context whose content never changes, so
// it is affirmed and retained across every turn boundary.
func stableContext() func() []ContextSegment {
	return func() []ContextSegment {
		return RequestOnlyContextBlocks([]wuucontext.Block{
			{
				Kind:    wuucontext.BlockActiveFiles,
				Title:   "Active files",
				Source:  "runtime.active_files",
				Content: "files:\n- go.mod",
			},
			{
				Kind:    wuucontext.BlockEnvironment,
				Title:   "Runtime environment",
				Source:  "runtime.snapshot",
				Content: "# Environment\n- State: steady",
			},
		})
	}
}

// Scenario 1: steady multi-turn session whose request-only context is
// unchanged across turns. Because the new turn re-affirms it byte-identically,
// it is spliced back at its recorded position: every request in the session —
// across every turn boundary — byte-extends its predecessor.
func TestPrefixExperiment_SteadySessionNeverDiverges(t *testing.T) {
	sim := &sessionSim{t: t}
	turnCalls := [][]string{{"call_1", "call_2"}, {"call_3"}, {"call_4", "call_5"}, {"call_6"}}
	for turn := 1; turn <= 4; turn++ {
		sim.runTurn(fmt.Sprintf("ask %d", turn), &fakeStep{results: toolRoundSteps(turnCalls[turn-1]...)}, func(cfg *LoopConfig) {
			cfg.Tools = experimentTools()
			cfg.BeforeRequestContext = stableContext()
		})
	}
	if len(sim.requests) != 10 {
		t.Fatalf("expected 10 provider requests, got %d", len(sim.requests))
	}
	if breaks := analyzePrefixChain(sim.requests); len(breaks) != 0 {
		t.Fatalf("steady session with unchanged context must keep an unbroken prefix chain:\n%s", formatBreaks(sim.requests, breaks))
	}
	// The unchanged blocks are retained, not re-emitted: exactly one copy
	// survives across the whole session.
	final := sim.requests[len(sim.requests)-1]
	if got := countMessagesContaining(final, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("stable block duplicated: %d copies in final request", got)
	}
	if got := countMessagesContaining(final, "State: steady"); got != 1 {
		t.Fatalf("stable env snapshot should appear exactly once, got %d", got)
	}
}

// Scenario 2: the context snapshot changes on every round and continues
// changing in the next turn. Typed blocks are ordered updates, so prior
// versions remain in the cache prefix while only the latest version applies.
func TestPrefixExperiment_PerRoundChangingContextNeverDivergesAcrossTurns(t *testing.T) {
	sim := &sessionSim{t: t}
	contextCalls := 0
	perRound := func() []ContextSegment {
		contextCalls++
		return RequestOnlyContextBlocks([]wuucontext.Block{{
			Kind:    wuucontext.BlockEnvironment,
			Title:   "Runtime environment",
			Source:  "runtime.snapshot",
			Content: fmt.Sprintf("# Environment\n- State: step %d", contextCalls),
		}})
	}
	sim.runTurn("ask 1", &fakeStep{results: toolRoundSteps("call_1", "call_2")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = perRound
	})
	sim.runTurn("ask 2", &fakeStep{results: toolRoundSteps("call_3")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = perRound
	})
	if breaks := analyzePrefixChain(sim.requests); len(breaks) != 0 {
		t.Fatalf("per-round changing typed context must append across turns:\n%s", formatBreaks(sim.requests, breaks))
	}
}

// Scenario 3: post-tool hook context (one-shot request-only segments emitted
// by tool execution) rides the transcript append-only within a run.
func TestPrefixExperiment_PostToolHookContextNeverDivergesWithinRun(t *testing.T) {
	sim := &sessionSim{t: t}
	sim.runTurn("ask 1", &fakeStep{results: toolRoundSteps("call_1", "call_2", "call_3")}, func(cfg *LoopConfig) {
		cfg.Tools = &contextLoopTools{defs: []providers.ToolDefinition{{Name: "read_file"}}}
	})
	if breaks := analyzePrefixChain(sim.requests); len(breaks) != 0 {
		t.Fatalf("post-tool hook context must stay append-only within a run:\n%s", formatBreaks(sim.requests, breaks))
	}
	final := sim.requests[len(sim.requests)-1]
	if got := countMessagesContaining(final, "context for call_1"); got != 1 {
		t.Fatalf("hook context for call_1 should be retained exactly once, got %d", got)
	}
}

// Scenario 4: a per-request BeforeRequest transform that appends one tail
// message. Transforms are request-scoped, so consecutive requests may only
// diverge at the transform's own tail position — everything before it must
// stay byte-stable.
func TestPrefixExperiment_TransformTailIsOnlyDivergencePoint(t *testing.T) {
	sim := &sessionSim{t: t}
	transform := func(_ context.Context, req *providers.ChatRequest) error {
		req.Messages = append(req.Messages, providers.ChatMessage{Role: "user", Content: "per-request injection", Hidden: true})
		return nil
	}
	for turn := 1; turn <= 2; turn++ {
		calls := []string{"call_1", "call_2"}
		if turn == 2 {
			calls = []string{"call_3"}
		}
		sim.runTurn(fmt.Sprintf("ask %d", turn), &fakeStep{results: toolRoundSteps(calls...)}, func(cfg *LoopConfig) {
			cfg.Tools = experimentTools()
			cfg.BeforeRequest = transform
		})
	}
	breaks := analyzePrefixChain(sim.requests)
	if len(breaks) != len(sim.requests)-1 {
		t.Fatalf("every consecutive pair should diverge exactly at the transform tail, got %d breaks for %d pairs:\n%s",
			len(breaks), len(sim.requests)-1, formatBreaks(sim.requests, breaks))
	}
	for _, brk := range breaks {
		wantIdx := len(sim.requests[brk.pair]) - 1
		if brk.msgIndex != wantIdx {
			t.Fatalf("divergence must be confined to the transform tail (message %d), got message %d:\n%s",
				wantIdx, brk.msgIndex, formatBreaks(sim.requests, breaks))
		}
	}
}

// Scenario 5: a mid-run overflow compaction rewrites history. The chain must
// break exactly once — at the compaction retry — and resume unbroken after.
func TestPrefixExperiment_OverflowCompactBreaksOnceThenResumes(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	sim := &sessionSim{t: t}
	step := &fakeStep{
		results: []StepResult{
			{ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"x"}`}}},
			{}, // round 2: overflow error
			{ToolCalls: []providers.ToolCall{{ID: "call_2", Name: "read_file", Arguments: `{"path":"x"}`}}},
			{Content: "ok"},
		},
		errs: []error{nil, overflow, nil, nil},
	}
	sim.runTurn("ask 1", step, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = stableContext()
		cfg.Compact = func(_ context.Context, msgs []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return []providers.ChatMessage{userMsg("summary of everything so far")}, nil
		}
	})
	// Follow-up turn after the rewrite, unchanged context: must extend the
	// rewritten transcript with no new break.
	sim.runTurn("ask 2", &fakeStep{results: toolRoundSteps("call_3")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = stableContext()
	})
	breaks := analyzePrefixChain(sim.requests)
	if len(breaks) != 1 {
		t.Fatalf("expected exactly one prefix break (the compaction retry), got %d:\n%s", len(breaks), formatBreaks(sim.requests, breaks))
	}
	if breaks[0].pair != 1 {
		t.Fatalf("the break must be at the overflow retry (pair 1), got pair %d:\n%s", breaks[0].pair, formatBreaks(sim.requests, breaks))
	}
	// The retried request must still carry the (re-emitted) context blocks.
	retry := sim.requests[2]
	if got := countMessagesContaining(retry, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("retry after compaction should re-inject the stable block once, got %d", got)
	}
}

// may break for that rewrite, but the compacted continuation must establish a
// stable prefix that subsequent rounds and turns extend normally.
func TestPrefixExperiment_ChangedContextRefreshesAcrossTurns(t *testing.T) {
	sim := &sessionSim{t: t}
	makeCtx := func(state string) func() []ContextSegment {
		return func() []ContextSegment {
			return RequestOnlyContextBlocks([]wuucontext.Block{
				{
					Kind:    wuucontext.BlockActiveFiles,
					Title:   "Active files",
					Source:  "runtime.active_files",
					Content: "files:\n- go.mod",
				},
				{
					Kind:    wuucontext.BlockEnvironment,
					Title:   "Runtime environment",
					Source:  "runtime.snapshot",
					Content: "# Environment\n- State: " + state,
				},
			})
		}
	}
	sim.runTurn("ask 1", &fakeStep{results: toolRoundSteps("call_1")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = makeCtx("alpha")
	})
	sim.runTurn("ask 2", &fakeStep{results: toolRoundSteps("call_2")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = makeCtx("beta")
	})
	if breaks := analyzePrefixChain(sim.requests); len(breaks) != 0 {
		t.Fatalf("changed typed context must supersede without breaking the prefix:\n%s", formatBreaks(sim.requests, breaks))
	}
	turn2 := sim.requests[len(sim.requests)-1]
	if got := countMessagesContaining(turn2, "State: alpha"); got != 1 {
		t.Fatalf("the prior update should remain once in the stable prefix, got %d", got)
	}
	if got := countMessagesContaining(turn2, "State: beta"); got != 1 {
		t.Fatalf("fresh context must be present exactly once, got %d copies of 'beta'", got)
	}
	envName := wuucontext.SystemReminderBlockMessageName(wuucontext.Block{
		Kind: wuucontext.BlockEnvironment, Title: "Runtime environment", Source: "runtime.snapshot",
	}, 0)
	latest := latestMessageWithName(turn2, envName)
	if !strings.Contains(latest.Content, "status: active") || !strings.Contains(latest.Content, "State: beta") {
		t.Fatalf("latest environment update must be active beta, got %+v", latest)
	}
	// The unchanged active-files block is still retained (single copy).
	if got := countMessagesContaining(turn2, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("unchanged block should stay retained once, got %d", got)
	}
}

func TestPrefixExperiment_DisappearingContextAppendsInactiveUpdate(t *testing.T) {
	sim := &sessionSim{t: t}
	block := wuucontext.Block{
		Kind: wuucontext.BlockToolPolicy, Title: "Ultra mode", Source: "ultra", Content: "Ultra is enabled.",
	}
	sim.runTurn("ask 1", &fakeStep{results: toolRoundSteps("call_1")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = func() []ContextSegment { return RequestOnlyContextBlocks([]wuucontext.Block{block}) }
	})
	sim.runTurn("ask 2", &fakeStep{results: toolRoundSteps("call_2")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
	})
	if breaks := analyzePrefixChain(sim.requests); len(breaks) != 0 {
		t.Fatalf("removing typed context must append an inactive update without breaking the prefix:\n%s", formatBreaks(sim.requests, breaks))
	}
	final := sim.requests[len(sim.requests)-1]
	name := wuucontext.SystemReminderBlockMessageName(block, 0)
	latest := latestMessageWithName(final, name)
	if !isInactiveRequestContextMessage(latest) {
		t.Fatalf("latest Ultra update must explicitly deactivate the prior policy, got %+v", latest)
	}
	if got := countMessagesContaining(final, "status: inactive"); got != 1 {
		t.Fatalf("inactive update must be emitted once, got %d", got)
	}
}

func TestPrefixExperiment_ContextReactivatesAfterInactiveUpdate(t *testing.T) {
	sim := &sessionSim{t: t}
	block := wuucontext.Block{
		Kind: wuucontext.BlockToolPolicy, Title: "Ultra mode", Source: "ultra", Content: "Ultra is enabled.",
	}
	withBlock := func() []ContextSegment {
		return RequestOnlyContextBlocks([]wuucontext.Block{block})
	}
	sim.runTurn("ask 1", &fakeStep{results: toolRoundSteps("call_1")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = withBlock
	})
	sim.runTurn("ask 2", &fakeStep{results: toolRoundSteps("call_2")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
	})
	block.Content = "Ultra is enabled again."
	sim.runTurn("ask 3", &fakeStep{results: toolRoundSteps("call_3")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = withBlock
	})

	if breaks := analyzePrefixChain(sim.requests); len(breaks) != 0 {
		t.Fatalf("reactivating typed context must append without breaking the prefix:\n%s", formatBreaks(sim.requests, breaks))
	}
	final := sim.requests[len(sim.requests)-1]
	name := wuucontext.SystemReminderBlockMessageName(block, 0)
	latest := latestMessageWithName(final, name)
	if !strings.Contains(latest.Content, "status: active") || !strings.Contains(latest.Content, "Ultra is enabled again.") {
		t.Fatalf("latest Ultra update must reactivate the policy, got %+v", latest)
	}
	if got := countMessagesContaining(final, "status: inactive"); got != 1 {
		t.Fatalf("reactivation must retain exactly one inactive transition, got %d", got)
	}
}

func latestMessageWithName(messages []providers.ChatMessage, name string) providers.ChatMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Name == name {
			return messages[i]
		}
	}
	return providers.ChatMessage{}
}

func TestPrefixExperiment_MidHistoryEditFallsBackSafely(t *testing.T) {
	sim := &sessionSim{t: t}
	sim.runTurn("ask 1", &fakeStep{results: toolRoundSteps("call_1")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = stableContext()
	})
	// Simulate an external edit: strip the assistant tool round out of the
	// durable history (e.g. a user deleted a message from the transcript).
	edited := make([]providers.ChatMessage, 0, len(sim.history))
	for _, msg := range sim.history {
		if msg.Role == "tool" {
			continue
		}
		edited = append(edited, msg)
	}
	sim.history = providers.CloneChatMessages(edited)
	// Drop the now-orphaned assistant tool call as a real editor would.
	filtered := sim.history[:0]
	for _, msg := range sim.history {
		if len(msg.ToolCalls) > 0 {
			continue
		}
		filtered = append(filtered, msg)
	}
	sim.history = filtered

	sim.runTurn("ask 2", &fakeStep{results: toolRoundSteps("call_2")}, func(cfg *LoopConfig) {
		cfg.Tools = experimentTools()
		cfg.BeforeRequestContext = stableContext()
	})
	// Turn 2's requests must carry each block exactly once (fresh emission,
	// no splice, no duplicates) and chain among themselves.
	turn2First := sim.requests[len(sim.requests)-2]
	if got := countMessagesContaining(turn2First, "[ACTIVE_FILES]"); got != 1 {
		t.Fatalf("fallback should inject the stable block exactly once, got %d in %+v", got, turn2First)
	}
	turn2 := sim.requests[len(sim.requests)-2:]
	if breaks := analyzePrefixChain(turn2); len(breaks) != 0 {
		t.Fatalf("post-fallback rounds must chain:\n%s", formatBreaks(turn2, breaks))
	}
}
