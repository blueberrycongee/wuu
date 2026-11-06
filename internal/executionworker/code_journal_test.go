package executionworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

type remoteJournalScope struct {
	mu      sync.Mutex
	ledger  *toolledger.Ledger
	parent  toolledger.Invocation
	effects string
	crash   string
	cancel  context.CancelFunc
}

func (s *remoteJournalScope) CodeJournal(ctx context.Context, request toolctx.CodeJournalRequest) (toolctx.CodeJournalResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var entry struct{ Kind string }
	_ = json.Unmarshal(request.Entry, &entry)
	interrupt := s.cancel != nil && request.Operation == "append" && entry.Kind == "delivery"
	if interrupt && s.crash == "before_delivery_commit" {
		s.cancel()
		return toolctx.CodeJournalResponse{}, toolerrors.Unknown(errors.New("lost delivery before commit"))
	}
	response, err := s.ledger.CodeJournal(ctx, s.parent.ID, s.parent.Attempt, request)
	if err == nil && interrupt {
		s.cancel()
		return toolctx.CodeJournalResponse{}, toolerrors.Unknown(errors.New("lost delivery after commit"))
	}
	return response, err
}
func (s *remoteJournalScope) Invoke(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	child, err := s.ledger.PrepareScopedChild(ctx, s.parent.ID, s.parent.Attempt, call, toolledger.ReplayAtMostOnce)
	if err != nil {
		return toolresult.Result{}, err
	}
	if child.State == toolledger.InvocationSucceeded || child.State == toolledger.InvocationFailed {
		return child.Result, nil
	}
	if err := s.ledger.StartAttempt(ctx, child.ID, child.Attempt); err != nil {
		return toolresult.Result{}, err
	}
	file, err := os.OpenFile(s.effects, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return toolresult.Result{}, err
	}
	_, writeErr := fmt.Fprintln(file, call.Name+" "+call.Arguments)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return toolresult.Result{}, err
	}
	result := toolresult.FromText(call.Name)
	if err := s.ledger.SettleAttempt(ctx, child.ID, child.Attempt, result); err != nil {
		return toolresult.Result{}, err
	}
	return result, nil
}

// Real worker and interpreter processes are replaced, and both durable storage
// backends are reopened after a settled effect loses its delivery receipt.
func TestWorkerDurableJournalRestart(t *testing.T) {
	for _, backend := range []string{"jsonl", "sqlite"} {
		for _, crash := range []string{"before_delivery_commit", "after_delivery_commit"} {
			t.Run(backend+"/"+crash, func(t *testing.T) {
				evidenceDir := os.Getenv("WUU_DURABLE_EVIDENCE")
				t.Setenv("WUU_HOME", t.TempDir())
				t.Setenv("HOME", t.TempDir())
				t.Setenv("WUU_EXECUTION_TEST_WORKER", "1")
				root, state, database := t.TempDir(), t.TempDir(), t.TempDir()
				profile := executionenv.Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestWorkerTransportProcess$"}, Workspace: root}
				newEnvironment := func() *executionenv.Environment {
					e := executionenv.NewEnvironment(profile, "durable-worker", "journal-session", state)
					t.Cleanup(func() { _ = e.Close() })
					return e
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				store, err := persistence.Open(ctx, database, backend)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				ledger, err := toolledger.New(store, "owner")
				if err != nil {
					t.Fatal(err)
				}
				call := providers.ToolCall{ID: "program", Name: "run_code", Arguments: `{}`}
				parents, err := ledger.AdmitBatch(ctx, "operation/batch", "driver@1", 1, []providers.ToolCall{call}, []toolledger.ReplayPolicy{toolledger.ReplayRepeatable})
				if err != nil {
					t.Fatal(err)
				}
				if err := ledger.StartAttempt(ctx, parents[0].ID, parents[0].Attempt); err != nil {
					t.Fatal(err)
				}
				firstCtx, stop := context.WithCancel(ctx)
				defer stop()
				scope := &remoteJournalScope{ledger: ledger, parent: parents[0], effects: filepath.Join(t.TempDir(), "effects.log"), crash: crash, cancel: stop}
				type journalEvidence struct {
					Start       json.RawMessage              `json:"start"`
					Length      int                          `json:"length"`
					Outcome     json.RawMessage              `json:"outcome,omitempty"`
					Checkpoints []map[string]json.RawMessage `json:"checkpoints"`
					Receipts    []toolstate.Invocation       `json:"receipts"`
					State       []json.RawMessage            `json:"state"`
				}
				// Capture persisted fixture records, excluding workspace scope paths,
				// transport credentials and interpreter identity strings.
				snapshot := func() journalEvidence {
					t.Helper()
					read := func(query persistence.Query) []persistence.Document {
						t.Helper()
						view, err := store.Read(ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						if view.NextCursor != "" {
							t.Fatal("journal fixture evidence was truncated")
						}
						return view.Documents
					}
					var out journalEvidence
					rows := read(persistence.Query{Namespace: "wuu.code_journals.v1", Keys: []string{scope.parent.ID}})
					if len(rows) != 1 {
						t.Fatalf("expected one persisted program journal, got %d", len(rows))
					}
					if err := json.Unmarshal(rows[0].Value, &out); err != nil {
						t.Fatal(err)
					}
					for _, row := range read(persistence.Query{Namespace: "wuu.code_journal_entries.v1", Prefix: scope.parent.ID + "/"}) {
						var entry map[string]json.RawMessage
						if err := json.Unmarshal(row.Value, &entry); err != nil {
							t.Fatal(err)
						}
						delete(entry, "Runtime")
						out.Checkpoints = append(out.Checkpoints, entry)
					}
					for _, row := range read(persistence.Query{Namespace: toolstate.Invocations}) {
						var receipt toolstate.Invocation
						if err := json.Unmarshal(row.Value, &receipt); err != nil {
							t.Fatal(err)
						}
						if receipt.ID == scope.parent.ID || receipt.ParentInvocationID == scope.parent.ID {
							out.Receipts = append(out.Receipts, receipt)
						}
					}
					for _, row := range read(persistence.Query{Namespace: "wuu.code_stores.v1"}) {
						out.State = append(out.State, row.Value)
					}
					if out.Length != len(out.Checkpoints) {
						t.Fatalf("journal length=%d but persisted checkpoints=%d", out.Length, len(out.Checkpoints))
					}
					return out
				}
				request := executionenv.CodeRequest{Actor: "actor", PermissionMode: "unconfined", Program: codemode.RunRequest{Code: `const r=Math.random(); const t=Date.now(); await tools.write({r,t}); await tools.read({r,t}); store("saved",{r,t}); return {r,t};`, Tools: []codemode.ToolDefinition{{Name: "write"}, {Name: "read"}}}}
				first := newEnvironment()
				_, firstErr := first.RunCode(firstCtx, request, scope)
				if toolerrors.Kind(firstErr) != toolerrors.UncertainEffect {
					t.Fatalf("interrupted remote journal lost uncertainty: %v", firstErr)
				}
				if err := first.Close(); err != nil {
					t.Fatal(err)
				}
				effects, err := os.ReadFile(scope.effects)
				if err != nil || strings.Count(string(effects), "\n") != 1 {
					t.Fatalf("first attempt effects=%s err=%v", effects, err)
				}
				beforeEffects := append([]byte(nil), effects...)
				var before journalEvidence
				if evidenceDir != "" {
					before = snapshot()
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				store, err = persistence.Open(ctx, database, backend)
				if err != nil {
					t.Fatal(err)
				}
				ledger, err = toolledger.New(store, "owner")
				if err != nil {
					t.Fatal(err)
				}
				if err := ledger.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
				parents, err = ledger.RecoverBatch(ctx, "operation/batch", "driver@1", []providers.ToolCall{call}, []toolledger.ReplayPolicy{toolledger.ReplayRepeatable})
				if err != nil {
					t.Fatal(err)
				}
				if err := ledger.StartAttempt(ctx, parents[0].ID, parents[0].Attempt); err != nil {
					t.Fatal(err)
				}
				scope = &remoteJournalScope{ledger: ledger, parent: parents[0], effects: scope.effects}
				second := newEnvironment()
				result, err := second.RunCode(ctx, request, scope)
				if err != nil || result.Error != "" {
					t.Fatalf("remote restart: %+v %v", result, err)
				}
				effects, err = os.ReadFile(scope.effects)
				lines := strings.Split(strings.TrimSpace(string(effects)), "\n")
				if err != nil || len(lines) != 2 || !strings.HasPrefix(lines[0], "write ") || !strings.HasPrefix(lines[1], "read ") || strings.TrimPrefix(lines[0], "write ") != strings.TrimPrefix(lines[1], "read ") {
					t.Fatalf("replayed effect or changed random/time: %s %v", effects, err)
				}
				replayed, err := second.RunCode(ctx, request, scope)
				if err != nil || replayed.Error != "" || string(replayed.Value) != string(result.Value) {
					t.Fatalf("recorded outcome not reused: %+v %v", replayed, err)
				}
				after, err := os.ReadFile(scope.effects)
				if err != nil || string(after) != string(effects) {
					t.Fatalf("completed journal re-executed effect: %s %v", after, err)
				}
				if evidenceDir != "" {
					final := snapshot()
					if string(before.Start) != string(final.Start) || before.Length >= final.Length || !reflect.DeepEqual(before.Checkpoints, final.Checkpoints[:before.Length]) {
						t.Fatal("persisted journal start or recorded prefix changed on recovery")
					}
					var recorded codemode.RunResult
					if err := json.Unmarshal(final.Outcome, &recorded); err != nil || recorded.Error != "" || string(recorded.Value) != string(result.Value) {
						t.Fatalf("persisted outcome differs from returned result: %+v %v", recorded, err)
					}
					if len(before.Receipts) != 2 || len(final.Receipts) != 3 || len(final.State) != 1 {
						t.Fatalf("unexpected durable receipt/state counts: before=%d after=%d state=%d", len(before.Receipts), len(final.Receipts), len(final.State))
					}
					for _, receipt := range final.Receipts {
						if receipt.ParentInvocationID != "" && (receipt.Attempt != 1 || receipt.State != string(toolledger.InvocationSucceeded)) {
							t.Fatalf("nested effect receipt was replayed or not settled: %+v", receipt)
						}
					}
					var saved struct{ Values map[string]string }
					if err := json.Unmarshal(final.State[0], &saved); err != nil || saved.Values[`"saved"`] != string(result.Value) {
						t.Fatalf("persisted saved state differs from returned result: %+v %v", saved, err)
					}
					dir := filepath.Join(evidenceDir, "remote-code-journal-"+backend+"-"+crash)
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					for name, body := range map[string][]byte{"effects-before-restart.txt": beforeEffects, "effects.txt": effects} {
						if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
							t.Fatal(err)
						}
					}
					for name, value := range map[string]any{
						"before-restart.json": before,
						"after-restart.json":  final,
						"results.json":        map[string]any{"interrupted_error_kind": toolerrors.Kind(firstErr), "resumed": result, "replayed": replayed, "persisted": recorded},
						"assertions.json": map[string]any{"backend": backend, "fault": crash, "parent_attempt": parents[0].Attempt, "effects_before_restart": 1, "effects_after_restart": 2, "checkpoint_prefix_length": before.Length, "completed_checkpoint_length": final.Length,
							"assertions": map[string]bool{"unknown_preserved": true, "remote_worker_restarted": true, "database_reopened": true, "recorded_prefix_preserved": true, "write_executed_once": true, "read_executed_once": true, "child_attempts_unchanged": true, "random_and_time_unchanged": true, "completed_outcome_reused": true, "saved_state_persisted": true}},
					} {
						body, err := json.MarshalIndent(value, "", "  ")
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, name), append(body, '\n'), 0600); err != nil {
							t.Fatal(err)
						}
					}
					t.Logf("persisted evidence: %s", dir)
				}
				t.Logf("backend=%s fault=%s parent_attempt=%d remote_worker_restarted=true disk_reopened=true effects=write,read exact_random_and_time=true outcome_reused=true", backend, crash, parents[0].Attempt)
			})
		}
	}
}
