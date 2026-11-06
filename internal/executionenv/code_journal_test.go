package executionenv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// This protocol peer deliberately ignores callback errors in the unknown case,
// as an older worker or guest catching an exception might do.
func TestCodeJournalTransportPeer(t *testing.T) {
	if os.Getenv("WUU_JOURNAL_TEST_PEER") != "1" {
		return
	}
	scan := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	var parentID, mode string
	var operations []string
	var replies []json.RawMessage
	var index int
	callback := func() {
		request := toolctx.CodeJournalRequest{Operation: operations[index], Fingerprint: "program-hash", Scope: "actor-scope", Index: index, Entry: json.RawMessage(`{"receipt":true}`)}
		if request.Operation == "complete" {
			request.State = map[string]string{}
		}
		var payload any = map[string]any{"callback_kind": "code_journal", "request": request}
		if mode == "tool" {
			payload = providers.ToolCall{ID: "child", Name: "read", Arguments: `{}`, Kind: "function"}
		}
		data, _ := json.Marshal(payload)
		_ = enc.Encode(Response{ID: fmt.Sprintf("callback-%d", index), ParentID: parentID, Method: "tool", Data: data})
	}
	for scan.Scan() {
		var req Request
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(2)
		}
		switch req.Method {
		case "initialize":
			capabilities := []string{CodeJournalCapability}
			if os.Getenv("WUU_JOURNAL_OLD_PEER") == "1" {
				capabilities = nil
			}
			data, _ := json.Marshal(map[string]any{"version": ProtocolVersion, "capabilities": capabilities})
			_ = enc.Encode(Response{ID: req.ID, Data: data})
		case "run_code":
			var input CodeRequest
			_ = json.Unmarshal(req.Data, &input)
			parentID, mode = req.ID, input.Program.Code
			if mode == "advertised" {
				value, _ := json.Marshal(input.CodeJournal)
				data, _ := json.Marshal(codemode.RunResult{Value: value})
				_ = enc.Encode(Response{ID: req.ID, Data: data})
				continue
			}
			operations, replies, index = []string{"open", "read", "append", "complete"}, nil, 0
			if mode != "journal" {
				operations = operations[:1]
			}
			callback()
		case "callback":
			var response Response
			_ = json.Unmarshal(req.Data, &response)
			if response.Error != "" && mode != "unknown" {
				_ = enc.Encode(Response{ID: parentID, Error: response.Error, ErrorKind: response.ErrorKind})
				continue
			}
			replies = append(replies, response.Data)
			index++
			if index < len(operations) {
				callback()
				continue
			}
			value, _ := json.Marshal(replies)
			data, _ := json.Marshal(codemode.RunResult{Value: value})
			_ = enc.Encode(Response{ID: parentID, Data: data})
		case "cancel":
			_ = enc.Encode(Response{ID: req.ID, Error: "cancelled"})
		}
	}
	os.Exit(0)
}

type journalTestExecutor struct {
	invoke func(context.Context, providers.ToolCall) (toolresult.Result, error)
}

func (e journalTestExecutor) Invoke(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if e.invoke == nil {
		return toolresult.Result{}, errors.New("unexpected tool invocation")
	}
	return e.invoke(ctx, call)
}

type journalTestScope struct {
	journalTestExecutor
	journal func(context.Context, toolctx.CodeJournalRequest) (toolctx.CodeJournalResponse, error)
}

func (e journalTestScope) CodeJournal(ctx context.Context, request toolctx.CodeJournalRequest) (toolctx.CodeJournalResponse, error) {
	return e.journal(ctx, request)
}
func journalTestEnvironment(t *testing.T, old bool) *Environment {
	t.Helper()
	t.Setenv("WUU_JOURNAL_TEST_PEER", "1")
	if old {
		t.Setenv("WUU_JOURNAL_OLD_PEER", "1")
	}
	e := NewEnvironment(Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestCodeJournalTransportPeer$"}, Workspace: t.TempDir()}, "journal-test", "thread", t.TempDir())
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestCodeJournalTransportBindsHostScope(t *testing.T) {
	e := journalTestEnvironment(t, false)
	var got []toolctx.CodeJournalRequest
	scope := journalTestScope{journal: func(_ context.Context, request toolctx.CodeJournalRequest) (toolctx.CodeJournalResponse, error) {
		got = append(got, request)
		return toolctx.CodeJournalResponse{Length: request.Index + 1, Entry: request.Entry}, nil
	}}
	result, err := e.RunCode(context.Background(), CodeRequest{Program: codemode.RunRequest{Code: "journal"}}, scope)
	if err != nil {
		t.Fatal(err)
	}
	var replies []toolctx.CodeJournalResponse
	if err := json.Unmarshal(result.Value, &replies); err != nil {
		t.Fatal(err)
	}
	for index, operation := range []string{"open", "read", "append", "complete"} {
		want := toolctx.CodeJournalRequest{Operation: operation, Fingerprint: "program-hash", Scope: "actor-scope", Index: index, Entry: json.RawMessage(`{"receipt":true}`)}
		if operation == "complete" {
			// Empty commits clear the last stored key; nil means no state commit.
			want.State = map[string]string{}
		}
		if len(got) != 4 || !reflect.DeepEqual(got[index], want) || len(replies) != 4 || replies[index].Length != index+1 || string(replies[index].Entry) != `{"receipt":true}` {
			t.Fatalf("journal transport changed request or response: requests=%+v replies=%+v", got, replies)
		}
	}
}

func TestCodeJournalTransportRejectsForgedCapability(t *testing.T) {
	e := journalTestEnvironment(t, false)
	var invoked atomic.Int32
	ordinary := journalTestExecutor{invoke: func(_ context.Context, call providers.ToolCall) (toolresult.Result, error) {
		invoked.Add(1)
		if call.Name != "read" || call.Kind != "function" {
			return toolresult.Result{}, fmt.Errorf("ordinary callback changed: %+v", call)
		}
		return toolresult.FromText("ordinary"), nil
	}}
	result, err := e.RunCode(context.Background(), CodeRequest{CodeJournal: true, Program: codemode.RunRequest{Code: "advertised"}}, ordinary)
	if err != nil || string(result.Value) != "false" {
		t.Fatalf("caller input granted capability: %s %v", result.Value, err)
	}
	_, err = e.RunCode(context.Background(), CodeRequest{CodeJournal: true, Program: codemode.RunRequest{Code: "spoof"}}, ordinary)
	if err == nil || !strings.Contains(err.Error(), "journal") || invoked.Load() != 0 {
		t.Fatalf("forged journal reached ordinary executor: %v count=%d", err, invoked.Load())
	}
	if _, err := e.RunCode(context.Background(), CodeRequest{Program: codemode.RunRequest{Code: "tool"}}, ordinary); err != nil || invoked.Load() != 1 {
		t.Fatalf("ordinary tool callback no longer works: %v count=%d", err, invoked.Load())
	}
}

func TestCodeJournalTransportRequiresWorkerCapability(t *testing.T) {
	e := journalTestEnvironment(t, true)
	scope := journalTestScope{journal: func(context.Context, toolctx.CodeJournalRequest) (toolctx.CodeJournalResponse, error) {
		t.Error("old worker reached journal")
		return toolctx.CodeJournalResponse{}, nil
	}}
	_, err := e.RunCode(context.Background(), CodeRequest{Program: codemode.RunRequest{Code: "journal"}}, scope)
	if err == nil || !strings.Contains(err.Error(), "update") {
		t.Fatalf("old worker silently accepted durable execution: %v", err)
	}
	e.client.mu.Lock()
	sequence := e.client.sequence
	e.client.mu.Unlock()
	if sequence != 1 {
		t.Fatalf("durable program reached unsupported worker: %d requests", sequence)
	}
	if _, err := e.RunCode(context.Background(), CodeRequest{Program: codemode.RunRequest{Code: "advertised"}}, journalTestExecutor{}); err != nil {
		t.Fatalf("old worker cannot run ordinary program: %v", err)
	}
}

func TestCodeJournalTransportPreservesUnknownAndCancellation(t *testing.T) {
	for _, cancelCall := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelCall), func(t *testing.T) {
			e := journalTestEnvironment(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			entered, stopped := make(chan struct{}), make(chan struct{})
			scope := journalTestScope{journal: func(ctx context.Context, _ toolctx.CodeJournalRequest) (toolctx.CodeJournalResponse, error) {
				close(entered)
				defer close(stopped)
				if cancelCall {
					<-ctx.Done()
					return toolctx.CodeJournalResponse{}, toolerrors.Unknown(ctx.Err())
				}
				return toolctx.CodeJournalResponse{}, toolerrors.Unknown(errors.New("journal commit receipt lost"))
			}}
			done := make(chan error, 1)
			go func() {
				_, err := e.RunCode(ctx, CodeRequest{Program: codemode.RunRequest{Code: "unknown"}}, scope)
				done <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("journal callback never arrived")
			}
			if cancelCall {
				cancel()
			}
			select {
			case err := <-done:
				if toolerrors.Kind(err) != toolerrors.UncertainEffect {
					t.Fatalf("journal uncertainty was erased: %v", err)
				}
				if cancelCall && !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation cause: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled journal transport did not return")
			}
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("host callback scope stayed alive")
			}
		})
	}
}
