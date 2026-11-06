package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

// A real child process commits an effect, then receives SIGKILL before replying.
// The committed marker is the barrier; no timing assumption controls the crash.
func TestDurableLostReceiptWorker(t *testing.T) {
	if os.Getenv("WUU_DURABLE_LOST_RECEIPT_WORKER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(90)
	}
	var request executionenv.Request
	if json.Unmarshal(scanner.Bytes(), &request) != nil {
		os.Exit(91)
	}
	f, err := os.OpenFile(os.Getenv("WUU_DURABLE_EFFECT_MARKER"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		os.Exit(92)
	}
	if _, err = f.WriteString("effect\n"); err != nil {
		os.Exit(93)
	}
	if f.Sync() != nil || f.Close() != nil {
		os.Exit(94)
	}
	if os.Getenv("WUU_DURABLE_MALFORMED") == "1" {
		_, _ = os.Stdout.WriteString("not-json\n")
		os.Exit(0)
	}
	if os.Getenv("WUU_DURABLE_RETURN_ERROR") == "1" {
		_ = json.NewEncoder(os.Stdout).Encode(executionenv.Response{ID: request.ID, Error: "tool rejected operation"})
		os.Exit(0)
	}
	process, _ := os.FindProcess(os.Getpid())
	_ = process.Kill()
	os.Exit(95)
}

type durableLostReceiptExecutor struct{ client *executionenv.Client }

func (e *durableLostReceiptExecutor) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "write_file"}}
}
func (e *durableLostReceiptExecutor) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	data, err := e.client.Call(ctx, "execute", call)
	return string(data), err
}

// A lost receipt is unknown, not a failed result, and cannot trigger replay.
func TestDurableWorkerUnknownOutcomeContractProbe(t *testing.T) {
	for _, malformed := range []string{"0", "1"} {
		t.Run("malformed="+malformed, func(t *testing.T) {

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			evidence := os.Getenv("WUU_DURABLE_PROBE_EVIDENCE")
			if evidence == "" {
				evidence = t.TempDir()
			}
			marker := filepath.Join(evidence, "worker-effect-"+malformed+".txt")
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("use a fresh evidence directory")
			}
			c := executionenv.NewClient([]string{os.Args[0], "-test.run=^TestDurableLostReceiptWorker$"}, append(os.Environ(), "WUU_DURABLE_LOST_RECEIPT_WORKER=1", "WUU_DURABLE_EFFECT_MARKER="+marker, "WUU_DURABLE_MALFORMED="+malformed))
			defer func() { _ = c.Close() }()
			ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "synthetic-probe-session")
			if err != nil {
				t.Fatal(err)
			}
			runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: &durableLostReceiptExecutor{client: c}, Ledger: ledger, OperationID: "synthetic-probe-execution", StepIndex: 1})
			messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "synthetic-write", Name: "write_file", Arguments: `{}`}}, nil)
			if toolerrors.Kind(err) != toolerrors.UncertainEffect || len(messages) != 0 {
				t.Fatalf("unknown effect became ordinary receipt: messages=%+v err=%v", messages, err)
			}
			content, err := os.ReadFile(marker)
			if err != nil || string(content) != "effect\n" {
				t.Fatalf("effect evidence=%q err=%v", content, err)
			}
			pending, err := ledger.PendingProjection(ctx)
			if err != nil || len(pending) != 0 {
				t.Fatalf("unknown result projected: %+v err=%v", pending, err)
			}
			decision, err := runtime.ReplayDecision(ctx)
			if err != nil || decision.Action != toolledger.ReplayBlock || decision.Reason != toolledger.ReplayReasonInvocationUnknown {
				t.Fatalf("replay decision=%+v err=%v", decision, err)
			}
			messages, err = runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "synthetic-write", Name: "write_file", Arguments: `{}`}}, nil)
			if toolerrors.Kind(err) != toolerrors.UncertainEffect || len(messages) != 0 {
				t.Fatalf("repeat lost uncertainty: %+v %v", messages, err)
			}
			content, err = os.ReadFile(marker)
			if err != nil || string(content) != "effect\n" {
				t.Fatalf("effect repeated: %q %v", content, err)
			}
			runtime.Close()
			t.Log("receipt lost after fsync; effect_count=1; no failed receipt; replay blocked as unknown")
		})
	}
}

// A real worker-returned error is authoritative even if the worker exits next.
func TestDurableWorkerReturnedFailureSettles(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "effect")
	c := executionenv.NewClient([]string{os.Args[0], "-test.run=^TestDurableLostReceiptWorker$"}, append(os.Environ(), "WUU_DURABLE_LOST_RECEIPT_WORKER=1", "WUU_DURABLE_RETURN_ERROR=1", "WUU_DURABLE_EFFECT_MARKER="+marker))
	defer func() { _ = c.Close() }()
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "returned-error")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: &durableLostReceiptExecutor{client: c}, Ledger: ledger, OperationID: "returned-error-op"})
	defer runtime.Close()
	messages, err := runtime.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "call", Name: "write_file", Arguments: `{}`}}, nil)
	if err != nil || len(messages) != 1 {
		t.Fatalf("returned failure lost: %+v %v", messages, err)
	}
	pending, err := ledger.PendingProjection(context.Background())
	if err != nil || len(pending) != 1 || pending[0].State != toolledger.InvocationFailed {
		t.Fatalf("settlement=%+v err=%v", pending, err)
	}
	if err := ledger.MarkUnknown(context.Background(), pending[0].ID); err == nil {
		t.Fatal("settled result changed to unknown")
	}
}
