package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

type recoveryCheckpointStore struct{}

func (recoveryCheckpointStore) Load(context.Context) (loopdriver.Checkpoint, bool, error) {
	return loopdriver.Checkpoint{}, false, nil
}
func (recoveryCheckpointStore) Save(context.Context, loopdriver.Checkpoint) error { return nil }

type exactRecoveryTools struct {
	path                  string
	safe, unknown, denied bool
	reads, checks         int
}

func (t *exactRecoveryTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "write", InputSchema: map[string]any{"type": "object"}}, {Name: "read", ReplaySafe: t.safe, InputSchema: map[string]any{"type": "object"}}}
}
func (t *exactRecoveryTools) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{ReadOnly: call.Name == "read", ReplaySafe: t.safe && call.Name == "read"}, true
}
func (t *exactRecoveryTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	t.checks++
	if t.denied {
		return "", errors.New("current policy denied")
	}
	if call.Name == "write" {
		f, err := os.OpenFile(t.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return "", err
		}
		defer f.Close()
		_, err = f.WriteString("effect\n")
		return "written once", err
	}
	t.reads++
	if t.unknown {
		return "", toolerrors.Unknown(errors.New("worker lost result"))
	}
	return "recovered read", nil
}

// A real file mutation settled before a lost read must remain exactly once.
// Revoked declarations and revoked current policy must not repeat an effect.
func TestDriverExactRecoveryMixedBatch(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, scenario := range []string{"safe", "revoked", "unsafe", "policy-denied", "changed-arguments", "missing-admission", "not-resumed"} {
			t.Run(scenario+map[bool]string{false: "/native", true: "/selected"}[selected], func(t *testing.T) {
				ctx := context.Background()
				documents := agentTestDocuments(t, t.TempDir())
				ledger, _ := toolledger.New(documents, "session")
				tools := &exactRecoveryTools{path: filepath.Join(t.TempDir(), "effects"), safe: scenario != "unsafe", unknown: true}
				runner := &StreamRunner{Tools: tools, ToolLedger: ledger, DriverCheckpointStore: recoveryCheckpointStore{}}
				if selected {
					runner.SelectedToolBatch = toolbatch.V1Adapter{}
				}
				calls := []providers.ToolCall{{ID: "write-1", Name: "write", Arguments: `{}`}, {ID: "read-1", Name: "read", Arguments: `{}`}}
				history := []providers.ChatMessage{{Role: "user", Content: "write once and read"}, {Role: "assistant", ToolCalls: calls}}
				first := newStreamTurnGateway(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "session", ExecutionID: "original"}), runner, loopdriver.PersistedInput{Messages: history}, nil)
				first.descriptor = loopdriver.Descriptor{ID: "durable", Version: "1", Capabilities: []string{"tool_recovery"}}
				if _, err := first.CallTools(ctx, loopdriver.ToolBatch{ID: "batch", Calls: calls, Messages: history}); toolerrors.Kind(err) != toolerrors.UncertainEffect {
					t.Fatalf("lost result not uncertain: %v", err)
				}
				first.close()
				if err := ledger.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
				resumed := newStreamTurnGateway(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "session", ExecutionID: "resumed"}), runner, loopdriver.PersistedInput{Messages: history}, nil)
				defer resumed.close()
				resumed.descriptor = first.descriptor
				resumed.resumed = scenario != "not-resumed"
				tools.unknown = false
				if scenario == "revoked" {
					tools.safe = false
				}
				if scenario == "policy-denied" {
					tools.denied = true
				}
				operation := "original"
				recoveryCalls := providers.CloneToolCalls(calls)
				if scenario == "changed-arguments" {
					recoveryCalls[1].Arguments = `{"changed":true}`
				}
				if scenario == "missing-admission" {
					operation = "missing"
				}
				if _, err := resumed.CallTools(ctx, loopdriver.ToolBatch{ID: "new-batch", Calls: []providers.ToolCall{{ID: "new", Name: "read", Arguments: `{}`}}}); err == nil {
					t.Fatal("new batch bypassed unresolved effect")
				}
				result, err := resumed.RecoverTools(ctx, loopdriver.ToolRecovery{OriginalExecutionID: operation, BatchID: "batch", Calls: recoveryCalls})
				shouldRecover := scenario == "safe" || scenario == "policy-denied"
				if shouldRecover {
					if err != nil || len(result) != 2 {
						t.Fatalf("recovery=%+v err=%v", result, err)
					}
					if result[0].Content != "written once" || result[0].ToolInvocationID == "" {
						t.Fatalf("settled write receipt lost: %+v", result)
					}
					if scenario == "safe" && tools.reads != 2 {
						t.Fatalf("safe read not repeated: %d", tools.reads)
					}
					if scenario == "policy-denied" && (tools.reads != 1 || !strings.Contains(result[1].Content, "current policy denied")) {
						t.Fatalf("current authorization skipped: reads=%d result=%+v", tools.reads, result)
					}
				} else if err == nil || tools.reads != 1 {
					t.Fatalf("unsafe recovery: reads=%d result=%+v err=%v", tools.reads, result, err)
				}
				content, readErr := os.ReadFile(tools.path)
				if readErr != nil || string(content) != "effect\n" {
					t.Fatalf("write repeated: %q %v", content, readErr)
				}
			})
		}
	}
}
