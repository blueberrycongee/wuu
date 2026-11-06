package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

// A real plugin process is killed at the tool callback, after the preceding
// filesystem effect is settled. No sleeps or fabricated success receipts.
func TestDurableDriverReplayProcess(t *testing.T) {
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("requires independently packed durable driver fixture")
	}
	for _, scenario := range []string{"safe-read", "revoked-read", "undeclared-read", "lost-unsafe-write", "code-safe-read", "code-revoked-read", "code-lost-unsafe-write"} {
		t.Run(scenario, func(t *testing.T) {
			h := newDurableProcessHarness(t, fixture, "replay-"+scenario)
			code := strings.HasPrefix(scenario, "code-")
			baseScenario := strings.TrimPrefix(scenario, "code-")
			initial := []providers.ChatMessage{{Role: "user", Content: "Record one marker and then inspect it."}}
			store := sessionDriverCheckpointStore{sessions: testSessionStore(t, h.sessions), sessionID: h.sessionID}
			ledger, err := toolledger.New(testSessionDocuments(t, h.sessions), h.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			recorder := &durableCheckpointRecorder{CheckpointStore: store, directory: h.evidence}
			tools := &durableReplayTools{marker: durableMarkerTools{path: filepath.Join(h.root, "effects.txt")}, safe: baseScenario != "undeclared-read", loseWrite: baseScenario == "lost-unsafe-write", code: code}
			if code {
				tools.service = codemode.NewService(codemode.ServiceConfig{})
				defer func() { _ = tools.service.Close() }()
			}
			model := &durableReplayModel{code: code}
			runner := &agent.StreamRunner{Client: model, Model: "controlled-replay", Tools: tools, ToolLedger: ledger, DriverBindingStore: store, DriverCheckpointStore: recorder}
			attachWorkflowHistory(t, runner, h.sessions, h.sessionID, initial)
			acquire, stop, pid := h.start(t, 1)
			recorder.pid = pid
			runner.AcquireLoopDriver = acquire
			tools.interrupt = recorder.kill
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, runErr := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "replay-original"}), initial, nil)
			stop()
			if runErr == nil || !recorder.killed {
				t.Fatalf("tool boundary did not interrupt plugin: killed=%v err=%v", recorder.killed, runErr)
			}
			before, found, err := store.Load(ctx)
			if err != nil || !found {
				t.Fatalf("checkpoint lost: %v %v", found, err)
			}
			h.write(t, "checkpoint-before.json", before)
			if err := ledger.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			history, err := testSessionStore(t, h.sessions).LoadProviderHistorySnapshot(h.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			restored := workflowRecordsToMessages(t, history.Records)
			tools.interrupt = nil
			if code {
				_ = tools.service.Close()
				tools.service = codemode.NewService(codemode.ServiceConfig{})
			}
			if baseScenario == "revoked-read" {
				tools.safe = false
			}
			acquire, stop, _ = h.start(t, 2)
			runner.AcquireLoopDriver = acquire
			result, runErr := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "replay-resumed"}), restored, nil)
			stop()
			count := tools.marker.count(t)
			if baseScenario == "safe-read" {
				if runErr != nil || result.Content != "Marker recorded once and inspected." || count != 1 || tools.reads != 2 {
					t.Fatalf("safe recovery: result=%+v error=%v writes=%d reads=%d", result, runErr, count, tools.reads)
				}
			} else {
				expectedReads := 1
				if baseScenario == "lost-unsafe-write" {
					expectedReads = 0
				}
				if runErr == nil || count != 1 || tools.reads != expectedReads {
					t.Fatalf("unsafe recovery: result=%+v error=%v writes=%d reads=%d", result, runErr, count, tools.reads)
				}
			}
			receipts, err := testSessionDocuments(t, h.sessions).Read(ctx, persistence.Query{Namespace: toolstate.Invocations, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			expectedReceipts := 2
			if code && baseScenario != "lost-unsafe-write" {
				expectedReceipts = 3
			}
			if len(receipts.Documents) != expectedReceipts {
				t.Fatalf("recovery replaced immutable receipts: %d", len(receipts.Documents))
			}
			h.write(t, "receipts.json", receipts)
			h.write(t, "result.json", map[string]any{"scenario": scenario, "writes": count, "reads": tools.reads, "killed": recorder.killed, "answer": result.Content, "error": fmt.Sprint(runErr)})
			t.Logf("scenario=%s writes=%d reads=%d final=%q error=%v", scenario, count, tools.reads, result.Content, runErr)
		})
	}
}

type durableReplayTools struct {
	code            bool
	service         *codemode.Service
	marker          durableMarkerTools
	safe, loseWrite bool
	reads           int
	interrupt       func() error
}

func (t *durableReplayTools) Definitions() []providers.ToolDefinition {
	definitions := []providers.ToolDefinition{{Name: "record_effect", InputSchema: map[string]any{"type": "object"}}, {Name: "inspect_effect", ReplaySafe: t.safe, InputSchema: map[string]any{"type": "object"}}}
	if t.code {
		definitions = append(definitions, providers.ToolDefinition{Name: "run_code", ReplaySafe: true})
	}
	return definitions
}
func (t *durableReplayTools) ToolMetadata(call providers.ToolCall) (agent.ToolMetadata, bool) {
	return agent.ToolMetadata{Orchestrator: call.Name == "run_code", ReadOnly: call.Name == "inspect_effect", ReplaySafe: call.Name == "run_code" || call.Name == "inspect_effect" && t.safe}, true
}
func (t *durableReplayTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	if call.Name == "run_code" {
		var request codemode.RunRequest
		if err := json.Unmarshal([]byte(call.Arguments), &request); err != nil {
			return "", err
		}
		nested, _ := toolctx.Nested(ctx)
		response, err := t.service.Run(ctx, request, codemode.RunOptions{Executor: nested, StateScope: "process-test", StateOwner: "session"})
		if err != nil {
			return "", err
		}
		if response.Error != "" {
			return "", errors.New(response.Error)
		}
		return string(response.Value), nil
	}
	if call.Name == "record_effect" {
		result, err := t.marker.Execute(ctx, call)
		if err != nil {
			return "", err
		}
		if t.loseWrite && t.interrupt != nil {
			if err := t.interrupt(); err != nil {
				return "", err
			}
			return "", toolerrors.Unknown(errors.New("lost write receipt after filesystem effect"))
		}
		return result, nil
	}
	if call.Name != "inspect_effect" {
		return "", errors.New("unexpected tool")
	}
	t.reads++
	if t.interrupt != nil {
		if err := t.interrupt(); err != nil {
			return "", err
		}
		return "", toolerrors.Unknown(errors.New("lost read receipt"))
	}
	data, err := os.ReadFile(t.marker.path)
	return string(data), err
}

type durableReplayModel struct{ code bool }

func (*durableReplayModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream only")
}
func (m *durableReplayModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stream := make(chan providers.StreamEvent, 5)
	results := 0
	for _, message := range request.Messages {
		if message.Role == "tool" && message.ToolInvocationID != "" {
			results++
		}
	}
	expectedResults := 2
	if m.code {
		expectedResults = 1
	}
	if results == expectedResults {
		stream <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Marker recorded once and inspected."}
		stream <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	} else {
		calls := []providers.ToolCall{{ID: "write", Name: "record_effect", Arguments: `{}`}, {ID: "read", Name: "inspect_effect", Arguments: `{}`}}
		if m.code {
			request, _ := json.Marshal(codemode.RunRequest{Code: `await tools.record_effect({}); await tools.inspect_effect({}); return "inspected";`, Tools: []codemode.ToolDefinition{{Name: "record_effect"}, {Name: "inspect_effect"}}})
			calls = []providers.ToolCall{{ID: "program", Name: "run_code", Arguments: string(request)}}
		}
		for _, call := range calls {
			stream <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}
			stream <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call}
		}
		stream <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
	}
	close(stream)
	return stream, nil
}
