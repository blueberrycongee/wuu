package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestThreadDeleteReleasesCodeModeStateCapacity(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	t.Setenv("WUU_HOME", t.TempDir())
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	service := codemode.NewService(codemode.ServiceConfig{})
	t.Cleanup(func() { _ = service.Close() })
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	rt.Toolkit, rt.CodeMode = kit, service
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeUnconfined}
	rt.PermissionModeExplicit = true
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	requestIndex := 0
	rpc := func(method string, params any) map[string]any {
		t.Helper()
		requestIndex++
		id := fmt.Sprintf("scope-%d", requestIndex)
		dispatchPayload(t, srv, id, method, params)
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("%s failed: %+v", method, response["error"])
		}
		return response
	}
	start := func() string {
		t.Helper()
		response := rpc(MethodThreadStart, ThreadStartParams{})
		return remarshal[ThreadStartResult](t, response["result"]).Thread.ID
	}
	run := func(id, code string) (toolresult.Result, error) {
		t.Helper()
		threadRuntime, err := srv.ensureThreadRuntime(srv.thread(id))
		if err != nil {
			t.Fatal(err)
		}
		args, _ := json.Marshal(map[string]any{"code": code, "description": "Verify conversation state lifecycle"})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		turn := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: threadRuntime.Toolkit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		defer turn.Cancel()
		messages, err := turn.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: fmt.Sprintf("code-%d", requestIndex), Name: "run_code", Arguments: string(args)}}, nil)
		if err != nil || len(messages) != 1 || messages[0].ToolResult == nil {
			t.Fatalf("execute code-mode program: %+v, %v", messages, err)
		}
		return *messages[0].ToolResult, nil
	}
	ids := make([]string, 0, codemode.MaxStateScopes)
	for i := 0; i < codemode.MaxStateScopes; i++ {
		var id string
		if i == 1 {
			response := rpc(MethodThreadFork, ThreadForkParams{ThreadID: ids[0]})
			id = remarshal[ThreadForkResult](t, response["result"]).Thread.ID
		} else {
			id = start()
		}
		ids = append(ids, id)
		result, err := run(id, `store("marker", "retained"); return load("marker");`)
		if err != nil || result.IsError || result.TextProjection() != `"retained"` {
			t.Fatalf("seed thread %d: %+v, %v", i, result, err)
		}
	}

	// Archiving hides a resumable conversation; rebuilding its runtime must
	// preserve its state rather than silently freeing a live conversation's slot.
	retained := ids[len(ids)-1]
	rpc(MethodThreadArchive, ThreadArchiveParams{ThreadID: retained, Archived: true})
	rpc(MethodThreadArchive, ThreadArchiveParams{ThreadID: retained, Archived: false})
	srv.releaseThreadRuntime(srv.thread(retained))
	result, err := run(retained, `return load("marker");`)
	if err != nil || result.IsError || result.TextProjection() != `"retained"` {
		t.Fatalf("archive or runtime rebuild discarded state: %+v, %v", result, err)
	}

	fresh := start()
	result, err = run(fresh, `store("marker", "new"); return load("marker");`)
	if err != nil || !result.IsError || !strings.Contains(result.TextProjection(), "scope limit") {
		t.Fatalf("expected the live conversations to fill the scope budget: %+v, %v", result, err)
	}
	// The first conversation has naturally left the eight-entry runtime cache.
	// Permanent deletion must reclaim its state even without an in-memory owner.
	deleted := ids[0]
	if srv.thread(deleted) != nil {
		t.Fatal("fixture did not evict the deleted conversation runtime")
	}
	rpc(MethodThreadDelete, ThreadDeleteParams{ThreadID: deleted})
	if _, exists, err := session.Find(rt.SessionDir, deleted); err != nil || exists {
		t.Fatalf("permanent delete did not remove the session: exists=%v, err=%v", exists, err)
	}
	result, err = run(fresh, `store("marker", "new"); return load("marker");`)
	if err != nil || result.IsError || result.TextProjection() != `"new"` {
		t.Fatalf("permanently deleted conversation retained code-mode capacity: %+v, %v", result, err)
	}
	// A fork is a separate conversation even though its parent was deleted.
	rpc(MethodThreadResume, ThreadResumeParams{SessionID: ids[1], ResponseOnly: true})
	result, err = run(ids[1], `return load("marker");`)
	if err != nil || result.IsError || result.TextProjection() != `"retained"` {
		t.Fatalf("parent deletion discarded independent fork state: %+v, %v", result, err)
	}

	srv.deleteSessionForTest = func(string) (session.Session, error) {
		return session.Session{}, errors.New("synthetic delete failure")
	}
	dispatchPayload(t, srv, "delete-failure", MethodThreadDelete, ThreadDeleteParams{ThreadID: fresh})
	if responseByID(t, parseOutput(t, out.String()), "delete-failure")["error"] == nil {
		t.Fatal("delete failure was not returned")
	}
	result, err = run(fresh, `return load("marker");`)
	if err != nil || result.IsError || result.TextProjection() != `"new"` {
		t.Fatalf("failed deletion discarded live state: %+v, %v", result, err)
	}
}
