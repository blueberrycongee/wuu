package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestBuildFreshContextPreservesLatestAuthorization(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "investigate only", Seq: 1},
		{Role: "assistant", Content: strings.Repeat("investigation details ", 3000), Seq: 2},
	}
	progress := []providers.ChatMessage{
		{Role: "user", Content: "implement the fix", Seq: 3},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "edit", Name: "apply_patch", Arguments: `{}`}}, Seq: 4},
		{Role: "tool", ToolCallID: "edit", Content: "fix applied and tested", Seq: 5},
		{Role: "assistant", Content: "Recovered latest authorization and completed implementation.", Seq: 6},
	}
	history = append(history, progress...)
	got, err := buildFreshContext(history, 6, 100, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !reflect.DeepEqual(got[2], progress[0]) {
		t.Fatal("latest authorization lost during transition")
	}
	if !deferRepeatedContextTransition(got, 100, 8000) {
		t.Fatal("immediate repeated reset was not deferred")
	}
}

type contextRequestResultTools struct {
	contextSwitchTools
	result toolresult.Result
	err    error
}

func (t contextRequestResultTools) ExecuteResult(context.Context, providers.ToolCall) (toolresult.Result, error) {
	return t.result, t.err
}

func TestRunToolLoopRequiresAcceptedContextRequest(t *testing.T) {
	for _, mode := range []string{"accepted", "denied", "execution-error", "declined", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			tools := contextRequestResultTools{result: toolresult.FromText(`{"requested":true}`)}
			switch mode {
			case "denied":
				tools.result.IsError = true
			case "execution-error":
				tools.err = errors.New("permission denied")
			case "declined":
				tools.result = toolresult.FromText(`{"requested":false}`)
			case "malformed":
				tools.result = toolresult.FromText("not a control result")
			}
			archives, builds := 0, 0
			step := &contextSwitchStep{}
			_, err := RunToolLoop(context.Background(), []providers.ChatMessage{{Role: "user", Content: "task"}}, LoopConfig{
				Model: "test", MaxSteps: 2, Tools: tools,
				ArchiveHistory: func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
					archives++
					return HistoryArchive{}, nil
				},
				FreshContext: func(context.Context, []providers.ChatMessage, int, int, int) ([]providers.ChatMessage, error) {
					builds++
					return nil, ErrFreshContextNotSmaller
				},
			}, step)
			want := 0
			if mode == "accepted" {
				want = 1
			}
			if err != nil || archives != want || builds != want {
				t.Fatalf("archives=%d builds=%d want=%d err=%v", archives, builds, want, err)
			}
		})
	}
}

func TestRunToolLoopCapacityRecoveryBypassesVoluntaryResetGuard(t *testing.T) {
	history := []providers.ChatMessage{{Role: "system", Content: compact.BuildSummaryContent("current progress")}, {Role: "user", Content: "continue"}}
	attempts := 0
	// Start at the final-answer phase: the host must attempt capacity recovery
	// before that answer, without any model request to reset the small window.
	step := &contextSwitchStep{requests: []providers.ChatRequest{{}}}
	_, err := RunToolLoop(context.Background(), history, LoopConfig{
		Model: "test", MaxSteps: 1, FreshContextTokens: 4000, CompactThresholdTokens: 1,
		ArchiveHistory: func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
			return HistoryArchive{}, nil
		},
		FreshContext: func(context.Context, []providers.ChatMessage, int, int, int) ([]providers.ChatMessage, error) {
			attempts++
			return nil, ErrFreshContextNotSmaller
		},
	}, step)
	if err != nil || attempts != 1 {
		t.Fatalf("forced attempts=%d err=%v", attempts, err)
	}
}

func TestRunToolLoopDefersRepeatedResetUntilWindowGrows(t *testing.T) {
	for _, large := range []bool{false, true} {
		history := []providers.ChatMessage{
			{Role: "system", Cause: "fresh_context", Content: "Recover prior work from persistent notes and history."},
			{Role: "user", Content: "latest instruction"},
			{Role: "assistant", Content: "recovered progress"},
		}
		if large {
			history[2].Content = strings.Repeat("recovered progress ", 4000)
		}
		attempts := 0
		step := &contextSwitchStep{}
		_, err := RunToolLoop(context.Background(), history, LoopConfig{
			Model: "test", MaxSteps: 2, Tools: contextSwitchTools{}, FreshContextTokens: 4000,
			ArchiveHistory: func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
				attempts++
				return HistoryArchive{}, nil
			},
			FreshContext: func(context.Context, []providers.ChatMessage, int, int, int) ([]providers.ChatMessage, error) {
				return nil, ErrFreshContextNotSmaller
			},
		}, step)
		want := 0
		if large {
			want = 1
		}
		if err != nil || attempts != want {
			t.Fatalf("large=%v attempts=%d err=%v", large, attempts, err)
		}
		if !large {
			found := false
			for _, message := range step.requests[1].Messages {
				found = found || message.Content == "recovered progress"
			}
			if !found {
				t.Fatal("repeated reset discarded recovered progress")
			}
		}
	}
}
