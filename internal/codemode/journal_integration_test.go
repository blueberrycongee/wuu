package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"strings"
	"sync"
	"testing"
	"time"
)

type journalExecutor struct {
	mu          sync.Mutex
	start       toolctx.CodeJournalResponse
	fingerprint string
	entries     []json.RawMessage
	calls       []providers.ToolCall
	cancel      context.CancelFunc
	crashAt     int
	invoke      func(context.Context, providers.ToolCall) (toolresult.Result, error)
}

func (e *journalExecutor) CodeJournal(ctx context.Context, r toolctx.CodeJournalRequest) (toolctx.CodeJournalResponse, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch r.Operation {
	case "open":
		if e.fingerprint == "" {
			e.fingerprint = r.Fingerprint
			e.start.Start = r.Start
		} else if e.fingerprint != r.Fingerprint {
			return toolctx.CodeJournalResponse{}, errors.New("fingerprint changed")
		}
		out := e.start
		out.Length = len(e.entries)
		return out, nil
	case "read":
		return toolctx.CodeJournalResponse{Entry: e.entries[r.Index]}, nil
	case "append":
		if r.Index != len(e.entries) {
			return toolctx.CodeJournalResponse{}, errors.New("prefix changed")
		}
		e.entries = append(e.entries, append(json.RawMessage(nil), r.Entry...))
		if e.cancel != nil && len(e.entries) == e.crashAt {
			e.cancel()
			return toolctx.CodeJournalResponse{}, context.Canceled
		}
	case "complete":
		e.start.Outcome = r.Outcome
		e.start.State = r.State
	}
	return toolctx.CodeJournalResponse{}, nil
}
func (e *journalExecutor) Invoke(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	e.mu.Lock()
	e.calls = append(e.calls, call)
	handler := e.invoke
	e.mu.Unlock()
	if handler != nil {
		return handler(ctx, call)
	}
	return toolresult.FromText(call.Name), nil
}

// A committed delivery lost before VM receipt is replayed, not re-executed.
// The deterministic clock/random and initial state must survive a fresh service.
func TestNodeDurableJournalReplaysDeliveryBeforeNewEffects(t *testing.T) {
	executor := &journalExecutor{crashAt: 3}
	ctx, cancel := context.WithCancel(context.Background())
	executor.cancel = cancel
	request := RunRequest{Code: `const r=Math.random(); const t=Date.now(); await tools.write({r,t}); await tools.read({r,t}); store("saved",{r,t}); return {r,t};`, Tools: []ToolDefinition{{Name: "write"}, {Name: "read"}}}
	opts := RunOptions{Executor: executor, StateScope: "scope", StateOwner: "owner"}
	first, err := nodeService(t).Run(ctx, request, opts)
	if err == nil && first.Error == "" {
		t.Fatalf("expected interrupted run: %+v", first)
	}
	executor.mu.Lock()
	executor.cancel = nil
	callsBefore := len(executor.calls)
	executor.mu.Unlock()
	if callsBefore != 1 {
		t.Fatalf("write not the only effect before interruption: %d", callsBefore)
	}
	second, err := nodeService(t).Run(context.Background(), request, opts)
	if err != nil || second.Error != "" {
		t.Fatalf("resume %+v %v", second, err)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.calls) != 2 || executor.calls[0].Name != "write" || executor.calls[1].Name != "read" || executor.calls[0].Arguments != executor.calls[1].Arguments {
		t.Fatalf("replayed effects or changed clock/random: %+v", executor.calls)
	}
	if len(executor.start.Outcome) == 0 || executor.start.State[`"saved"`] != string(second.Value) {
		t.Fatalf("outcome/state not committed: %+v", executor.start)
	}
}
func TestNodeDurableJournalRejectsChangedCodeBeforeEffects(t *testing.T) {
	executor := &journalExecutor{crashAt: 3}
	ctx, cancel := context.WithCancel(context.Background())
	executor.cancel = cancel
	request := RunRequest{Code: `await tools.write({}); return 1;`, Tools: []ToolDefinition{{Name: "write"}}}
	opts := RunOptions{Executor: executor}
	_, _ = nodeService(t).Run(ctx, request, opts)
	executor.cancel = nil
	request.Code = `await tools.write({changed:true}); return 2;`
	result, err := nodeService(t).Run(context.Background(), request, opts)
	if err == nil && !strings.Contains(result.Error, "changed") {
		t.Fatalf("changed program accepted: %+v %v", result, err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("effect after divergence: %+v", executor.calls)
	}
}

func TestNodeDurableJournalKeepsParallelDeliveryOrder(t *testing.T) {
	for _, crashAt := range []int{3, 4} {
		t.Run(fmt.Sprint(crashAt), func(t *testing.T) {
			executor := &journalExecutor{crashAt: crashAt}
			ctx, cancel := context.WithCancel(context.Background())
			executor.cancel = cancel
			executor.invoke = func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
				if call.Name == "slow" {
					<-ctx.Done()
					return toolresult.Result{}, ctx.Err()
				}
				return toolresult.FromText(call.Name), nil
			}
			request := RunRequest{Code: `const slow=tools.slow({}); const fast=tools.fast({}); const winner=await Promise.race([slow,fast]); await tools.write({winner}); await Promise.all([slow,fast]); return winner;`, Tools: []ToolDefinition{{Name: "slow"}, {Name: "fast"}, {Name: "write"}}}
			_, _ = nodeService(t).Run(ctx, request, RunOptions{Executor: executor})
			executor.mu.Lock()
			executor.cancel = nil
			executor.invoke = nil
			executor.mu.Unlock()
			result, err := nodeService(t).Run(context.Background(), request, RunOptions{Executor: executor})
			if err != nil || result.Error != "" || !strings.Contains(string(result.Value), "fast") {
				t.Fatalf("parallel replay %+v %v", result, err)
			}
			executor.mu.Lock()
			defer executor.mu.Unlock()
			writes, fast := 0, 0
			for _, call := range executor.calls {
				if call.Name == "write" {
					writes++
					if !strings.Contains(call.Arguments, "fast") {
						t.Fatalf("changed delivery won race: %+v", call)
					}
				}
				if call.Name == "fast" {
					fast++
				}
			}
			if writes != 1 || fast != 1 {
				t.Fatalf("repeated completed effects: %+v", executor.calls)
			}
		})
	}
}
func TestNodeDurableJournalRejectsDivergenceAndExpiredDeadline(t *testing.T) {
	for _, scenario := range []string{"runtime", "calls", "early-finish", "deadline", "catalog"} {
		t.Run(scenario, func(t *testing.T) {
			executor := &journalExecutor{crashAt: 3}
			ctx, cancel := context.WithCancel(context.Background())
			executor.cancel = cancel
			request := RunRequest{Code: `await tools.write({}); await tools.read({}); return 1;`, Tools: []ToolDefinition{{Name: "write"}, {Name: "read"}}}
			opts := RunOptions{Executor: executor}
			_, _ = nodeService(t).Run(ctx, request, opts)
			executor.cancel = nil
			switch scenario {
			case "runtime":
				executor.entries[0] = json.RawMessage(`{"Kind":"runtime","Runtime":"changed"}`)
			case "calls":
				executor.entries[1] = json.RawMessage(`{"Kind":"step","Calls":[{"type":"call","id":1,"name":"write","args":{"changed":true}}],"Issued":1}`)
			case "early-finish":
				executor.entries = append(executor.entries, json.RawMessage(`{"Kind":"step","Done":{"type":"done","value":1},"Issued":1}`))
			case "deadline":
				var start journalStart
				_ = json.Unmarshal(executor.start.Start, &start)
				start.Deadline = time.Now().Add(-time.Minute).UnixMilli()
				executor.start.Start, _ = json.Marshal(start)
			case "catalog":
				request.Tools[1].Description = "changed"
			}
			result, err := nodeService(t).Run(context.Background(), request, opts)
			if err == nil && result.Error == "" {
				t.Fatalf("accepted %s: %+v", scenario, result)
			}
			if len(executor.calls) != 1 {
				t.Fatalf("effect after %s: %+v", scenario, executor.calls)
			}
		})
	}
}
