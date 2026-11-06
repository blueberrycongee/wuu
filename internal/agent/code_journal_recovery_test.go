package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type journalRecoveryTools struct {
	service          *codemode.Service
	path             string
	lost, safe, deny bool
	reads            int
	journal          bool
}

func (t *journalRecoveryTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "run_code", ReplaySafe: true}, {Name: "write"}, {Name: "read", ReplaySafe: t.safe}}
}
func (t *journalRecoveryTools) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{Orchestrator: call.Name == "run_code", ReplaySafe: call.Name == "run_code" || call.Name == "read" && t.safe}, true
}
func (t *journalRecoveryTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	result, err := t.ExecuteResult(ctx, call)
	return result.TextProjection(), err
}
func (t *journalRecoveryTools) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	switch call.Name {
	case "run_code":
		var request codemode.RunRequest
		if err := json.Unmarshal([]byte(call.Arguments), &request); err != nil {
			return toolresult.Result{}, err
		}
		nested, _ := toolctx.Nested(ctx)
		_, t.journal = nested.(toolctx.CodeJournal)
		response, err := t.service.Run(ctx, request, codemode.RunOptions{Executor: nested, StateScope: "scope", StateOwner: "session"})
		if err != nil {
			return toolresult.Result{}, err
		}
		if response.Error != "" {
			return toolresult.FromErrorText(response.Error), nil
		}
		return toolresult.FromText(string(response.Value)), nil
	case "write":
		file, err := os.OpenFile(t.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return toolresult.Result{}, err
		}
		defer file.Close()
		_, err = file.WriteString("effect\n")
		return toolresult.FromText("written"), err
	case "read":
		if t.deny {
			return toolresult.FromErrorText("current policy denied"), nil
		}
		t.reads++
		if t.lost {
			return toolresult.Result{}, toolerrors.Unknown(errors.New("lost nested read result"))
		}
		return toolresult.FromText("read"), nil
	}
	return toolresult.Result{}, errors.New("unknown tool")
}

// This is the real QuickJS + host tool pipeline: settled unsafe writes, child
// idempotency keys, current authorization, and parent/child interruption survive
// a new service and gateway. A catch in guest code cannot erase unknown effects.
func TestDurableCodeJournalRecoversNestedLedger(t *testing.T) {
	for _, scenario := range []string{"safe", "revoked", "unsafe", "policy-denied"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			documents := agentTestDocuments(t, t.TempDir())
			ledger, _ := toolledger.New(documents, "session")
			tools := &journalRecoveryTools{service: codemode.NewService(codemode.ServiceConfig{}), path: filepath.Join(t.TempDir(), "effects"), safe: scenario != "unsafe", lost: true}
			defer func() { _ = tools.service.Close() }()
			request := codemode.RunRequest{Code: `await tools.write({}); try { await tools.read({}); } catch { return "caught"; } return "recovered";`, Tools: []codemode.ToolDefinition{{Name: "write"}, {Name: "read"}}}
			encoded, _ := json.Marshal(request)
			calls := []providers.ToolCall{{ID: "program", Name: "run_code", Arguments: string(encoded)}}
			history := []providers.ChatMessage{{Role: "user", Content: "run"}, {Role: "assistant", ToolCalls: calls}}
			runner := &StreamRunner{Tools: tools, ToolLedger: ledger, DriverCheckpointStore: recoveryCheckpointStore{}}
			first := newStreamTurnGateway(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "session", ExecutionID: "original"}), runner, loopdriver.PersistedInput{Messages: history}, nil)
			first.descriptor = loopdriver.Descriptor{ID: "durable", Version: "1", Capabilities: []string{"tool_recovery"}}
			_, err := first.CallTools(ctx, loopdriver.ToolBatch{ID: "batch", Calls: calls, Messages: history})
			if toolerrors.Kind(err) != toolerrors.UncertainEffect {
				t.Fatalf("guest catch erased uncertainty: %v", err)
			}
			first.close()
			_ = tools.service.Close()
			if err := ledger.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			tools.service = codemode.NewService(codemode.ServiceConfig{})
			tools.lost = false
			if scenario == "revoked" {
				tools.safe = false
			}
			if scenario == "policy-denied" {
				tools.deny = true
			}
			resumed := newStreamTurnGateway(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "session", ExecutionID: "new"}), runner, loopdriver.PersistedInput{Messages: history}, nil)
			defer resumed.close()
			resumed.descriptor = first.descriptor
			resumed.resumed = true
			messages, err := resumed.RecoverTools(ctx, loopdriver.ToolRecovery{OriginalExecutionID: "original", BatchID: "batch", Calls: calls})
			if scenario == "safe" || scenario == "policy-denied" {
				if err != nil || len(messages) != 1 {
					t.Fatalf("recover %+v %v", messages, err)
				}
				expected := "recovered"
				reads := 2
				if scenario == "policy-denied" {
					expected = "caught"
					reads = 1
				}
				if !strings.Contains(messages[0].Content, expected) || tools.reads != reads {
					t.Fatalf("result %+v reads=%d", messages, tools.reads)
				}
				if err := ledger.RequireSettled(ctx); err != nil {
					t.Fatalf("child remains unresolved: %v", err)
				}
			} else if err == nil || tools.reads != 1 {
				t.Fatalf("unsafe retry %+v %v reads=%d", messages, err, tools.reads)
			}
			content, err := os.ReadFile(tools.path)
			if err != nil || string(content) != "effect\n" {
				t.Fatalf("write repeated: %q %v", content, err)
			}
		})
	}
}

// A checkpoint store is available in ordinary desktop sessions too. It must
// not silently opt those sessions into a durable program state machine.
func TestDriverCodeJournalRequiresExplicitRecoveryCapability(t *testing.T) {
	ctx := context.Background()
	ledger, _ := toolledger.New(agentTestDocuments(t, t.TempDir()), "session")
	tools := &journalRecoveryTools{service: codemode.NewService(codemode.ServiceConfig{}), path: filepath.Join(t.TempDir(), "effect"), safe: true}
	defer tools.service.Close()
	request, _ := json.Marshal(codemode.RunRequest{Code: `return 1;`})
	calls := []providers.ToolCall{{ID: "program", Name: "run_code", Arguments: string(request)}}
	history := []providers.ChatMessage{{Role: "user", Content: "run"}, {Role: "assistant", ToolCalls: calls}}
	runner := &StreamRunner{Tools: tools, ToolLedger: ledger, DriverCheckpointStore: recoveryCheckpointStore{}}
	gateway := newStreamTurnGateway(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "session", ExecutionID: "ordinary"}), runner, loopdriver.PersistedInput{Messages: history}, nil)
	defer gateway.close()
	gateway.descriptor = loopdriver.Descriptor{ID: "default", Version: "1"}
	if _, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: "batch", Calls: calls, Messages: history}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.RecoverBatch(ctx, "ordinary/batch", "default@1", calls, []toolledger.ReplayPolicy{toolledger.ReplayRepeatable}); err == nil {
		t.Fatal("ordinary desktop admitted a full recovery batch")
	}
	if tools.journal {
		t.Fatal("ordinary checkpointed driver received a durable code journal")
	}
}
