package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestToolFlowExitTwoBlocksEvenWithJSON(t *testing.T) {
	for _, output := range []string{`{}`, `{"continue":true}`, `{"decision":"allow","reason":"policy denied"}`, `null`} {
		t.Run(output, func(t *testing.T) {
			inner := &stubExecutor{result: "must not execute"}
			d := NewDispatcher(NewRegistry(map[Event][]HookConfig{
				PreToolUse: {{Command: "printf '%s' '" + output + "'; echo 'policy denied' >&2; exit 2"}},
			}))
			executor := NewHookedExecutor(inner, d, "session", t.TempDir())
			_, err := executor.Execute(context.Background(), providers.ToolCall{Name: "bash", Arguments: `{}`})
			if !IsBlocked(err) || !strings.Contains(err.Error(), "policy denied") || len(inner.calls) != 0 {
				t.Fatalf("exit 2 must block with a reason: err=%v calls=%v", err, inner.calls)
			}
		})
	}
}

func TestToolFlowValidatorSeesRewrittenArguments(t *testing.T) {
	inner := &stubExecutor{result: "must not execute"}
	d := NewDispatcher(NewRegistry(map[Event][]HookConfig{
		PreToolUse: {
			{Command: `printf '%s' '{"updated_input":{"command":"restricted-operation"}}'`},
			{Command: `input=$(cat); case "$input" in *restricted-operation*) echo 'rewritten command denied' >&2; exit 2;; esac`},
		},
	}))
	executor := NewHookedExecutor(inner, d, "session", t.TempDir())
	_, err := executor.Execute(context.Background(), providers.ToolCall{Name: "bash", Arguments: `{"command":"original-operation"}`})
	if !IsBlocked(err) || len(inner.calls) != 0 {
		t.Fatalf("validator must inspect arguments that would execute: err=%v calls=%v", err, inner.calls)
	}
}

func TestToolFlowPostContextSurvivesLaterHooks(t *testing.T) {
	for _, finalHook := range []string{"true", "exit 1", `printf '%s' '{"additional_context":"last note","decision":"block"}'`} {
		t.Run(finalHook, func(t *testing.T) {
			inner := &stubExecutor{result: "original result"}
			d := NewDispatcher(NewRegistry(map[Event][]HookConfig{
				PostToolUse: {
					{Command: `printf '%s' '{"additional_context":"first note"}'`},
					{Command: `printf '%s' '{"additional_context":"second note"}'`},
					{Command: finalHook},
				},
			}))
			executor := NewHookedExecutor(inner, d, "session", t.TempDir())
			call := providers.ToolCall{ID: "call-1", Name: "read_file", Arguments: `{}`}
			result, err := executor.Execute(context.Background(), call)
			if err != nil || result != inner.result {
				t.Fatalf("post hooks must preserve tool outcome: result=%q err=%v", result, err)
			}
			want := "first note\n\nsecond note"
			if strings.Contains(finalHook, "last note") {
				want += "\n\nlast note"
			}
			if got := executor.TakeAdditionalContext(call); got != want {
				t.Fatalf("context = %q, want %q", got, want)
			}
			if got := executor.TakeAdditionalContext(call); got != "" {
				t.Fatalf("context consumed twice: %q", got)
			}
		})
	}
}

func TestToolFlowRichErrorUsesFailureHook(t *testing.T) {
	for _, toolErr := range []error{nil, errors.New("transport unavailable")} {
		t.Run(fmt.Sprint(toolErr), func(t *testing.T) {
			inputPath := t.TempDir() + "/event.json"
			result := toolresult.FromErrorText("service rejected request")
			inner := &richStubExecutor{stubExecutor: stubExecutor{err: toolErr}, result: result}
			d := NewDispatcher(NewRegistry(map[Event][]HookConfig{
				PostToolUse:        {{Command: fmt.Sprintf("echo unexpected > %q", inputPath)}},
				PostToolUseFailure: {{Command: fmt.Sprintf("cat > %q; exit 1", inputPath)}},
			}))
			executor := NewHookedExecutor(inner, d, "session", t.TempDir())
			got, err := executor.ExecuteResult(context.Background(), providers.ToolCall{Name: "service", Arguments: `{}`})
			if err != toolErr || got.JSONProjection() != result.JSONProjection() {
				t.Fatalf("failure hook changed tool outcome: result=%+v err=%v", got, err)
			}
			data, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			var input Input
			if err := json.Unmarshal(data, &input); err != nil {
				t.Fatalf("expected failure event, got %q: %v", data, err)
			}
			wantError := result.HookProjection()
			if toolErr != nil {
				wantError = toolErr.Error()
			}
			if input.Event != PostToolUseFailure || input.Error != wantError {
				t.Fatalf("unexpected failure payload: %+v", input)
			}
		})
	}
}
