package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Exercise the real ordered stdio scanner, request authorization, selected
// session provider, filesystem handlers, response guards, and shutdown drain.
// The provider gate models a slow known-worktree lookup without sleeps or a
// production-only filesystem hook. It never reports cancellation as completion.
func TestRunStdioWorkspaceReadDoesNotBlockControlAndDrains(t *testing.T) {
	for _, method := range []string{MethodWorkspaceDirectoryList, MethodWorkspaceFileRead, MethodWorkspaceFileResolve, MethodWorkspaceFileChunk} {
		t.Run(method, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			client := newBlockingStreamClient("done")
			rt.StreamRunner.Client = client
			attachTestModelGeneration(t, rt)
			workspace, err := resolvedWorkspaceResource(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			worktree := workspace.ID
			writeWorkspaceViewFile(t, worktree, "file.txt", "captured workspace contents")
			provider := session.NewService("workspace-stdio-session", rt.Documents)
			if err := provider.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = provider.Close(context.Background()) })
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var block atomic.Bool
			rt.Sessions = session.NewClient(context.Background(), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				if method == "List" && block.CompareAndSwap(true, false) {
					close(entered)
					<-release
				}
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "workspace-stdio", Method: method, Params: raw})
			})
			if _, err := rt.Sessions.CreateWithMetadata("workspace-thread", worktree); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.Sessions.CreateWithMetadata("control-thread", rt.RootDir); err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			server := New(rt, out)
			t.Cleanup(server.Close)
			t.Cleanup(unblock)
			t.Cleanup(func() { close(client.release) })
			dispatchPayload(t, server, "start", MethodTurnStart, TurnStartParams{ThreadID: "control-thread", Prompt: "hold until interrupted"})
			if response := responseByID(t, parseOutput(t, out.String()), "start"); response["error"] != nil {
				t.Fatal(response)
			}
			select {
			case <-client.started:
			case <-time.After(5 * time.Second):
				t.Fatal("control turn did not reach the model")
			}
			block.Store(true)
			params := workspaceViewParams{Root: worktree, Path: "file.txt", Reference: "file.txt"}
			if method == MethodWorkspaceDirectoryList {
				params.Path = ""
			}
			input := string(mustJSON(Request{ID: mustJSON("workspace"), Method: method, Params: mustJSON(params)})) + "\n"
			input += `{"id":"stop","method":"turn/interrupt","params":{"thread_id":"control-thread"}}` + "\n"
			scannerDone := make(chan struct{})
			var scannerErr error
			go func() {
				scannerErr = runStdioScanner(context.Background(), server, strings.NewReader(input))
				close(scannerDone)
			}()
			t.Cleanup(func() {
				unblock()
				select {
				case <-scannerDone:
				case <-time.After(5 * time.Second):
					t.Error("stdio scanner did not drain")
				}
			})
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("workspace request did not enter selected provider")
			}
			select {
			case <-scannerDone:
				if scannerErr != nil {
					t.Fatal(scannerErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("turn/interrupt remained behind a blocked workspace read in stdio dispatch")
			}
			messages := parseOutput(t, out.String())
			if response := responseByID(t, messages, "stop"); response["error"] != nil {
				t.Fatalf("control request failed: %+v", response)
			}
			for _, message := range messages {
				if message["id"] == "workspace" {
					t.Fatal("workspace responded before its provider physically returned")
				}
			}
			t.Log("turn/interrupt acknowledged while workspace provider remained blocked")
			closed := make(chan struct{})
			go func() { server.Close(); close(closed) }()
			<-server.nativeHistoryCtx.Done()
			select {
			case <-closed:
				t.Fatal("shutdown abandoned accepted workspace work")
			default:
			}
			unblock()
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown did not join workspace work after provider release")
			}
			response := responseByID(t, parseOutput(t, out.String()), "workspace")
			if response["error"] != nil {
				t.Fatalf("workspace request failed after release: %+v", response)
			}
			if got := response["result"].(map[string]any)["root"]; method != MethodWorkspaceFileChunk && got != worktree {
				t.Fatalf("workspace authority changed: %v", got)
			}
			server.access.mu.Lock()
			pending := len(server.access.pending)
			server.access.mu.Unlock()
			if pending != 0 {
				t.Fatalf("completed requests retained %d authorization records", pending)
			}
			t.Log("workspace response delivered and server-owned work drained after provider release")
		})
	}
}

func TestRunStdioWorkspaceReadPreservesRequestAndAuthority(t *testing.T) {
	for _, outcome := range []string{"allowed", "revoked", "cancelled", "duplicate-id", "duplicate-unknown", "duplicate-invalid-params", "duplicate-invalid-root", "duplicate-transport"} {
		t.Run(outcome, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			workspace, err := resolvedWorkspaceResource(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			worktree := workspace.ID
			writeWorkspaceViewFile(t, worktree, "private.txt", "private workspace contents")
			provider := session.NewService("workspace-permission-session", rt.Documents)
			if err := provider.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = provider.Close(context.Background()) })
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var block atomic.Bool
			rt.Sessions = session.NewClient(context.Background(), func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				if method == "List" && block.CompareAndSwap(true, false) {
					close(entered)
					<-release
				}
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "workspace-permission", Method: method, Params: raw})
			})
			if _, err := rt.Sessions.CreateWithMetadata("workspace-thread", worktree); err != nil {
				t.Fatal(err)
			}
			caller, err := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "test", Subject: "workspace-reader"})
			if err != nil {
				t.Fatal(err)
			}
			var revoked atomic.Bool
			var ownerChecks atomic.Int64
			policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
				// The local pipe is an owner, unlike this request. A missing
				// pending scope must not silently borrow that ambient authority.
				if r.Owner {
					ownerChecks.Add(1)
					return access.Decision{Allow: true}, nil
				}
				if r.Principal != caller.Identity() || revoked.Load() {
					return access.Decision{}, nil
				}
				for _, resource := range r.Resources {
					if resource.Kind != "workspace" || resource.ID != worktree {
						return access.Decision{}, nil
					}
				}
				return access.Decision{Allow: true}, nil
			})
			out := &lockedBuffer{}
			server := New(rt, out, WithAccessPolicy(policy))
			t.Cleanup(server.Close)
			t.Cleanup(unblock)
			block.Store(true)
			input := string(mustJSON(Request{ID: mustJSON("private"), Method: MethodWorkspaceFileRead, Params: mustJSON(workspaceViewParams{Root: worktree, Path: "private.txt"})})) + "\n"
			duplicate := strings.HasPrefix(outcome, "duplicate-")
			if duplicate {
				request := Request{ID: mustJSON("private"), Method: MethodWorkspaceDirectoryList, Params: mustJSON(workspaceViewParams{Root: worktree})}
				switch outcome {
				case "duplicate-unknown":
					request.Method = "test/unknown"
				case "duplicate-invalid-params":
					request.Params = json.RawMessage(`[]`)
				case "duplicate-invalid-root":
					request.Params = mustJSON(workspaceViewParams{Root: worktree + "/missing"})
				case "duplicate-transport":
					request.Method = "transport/dispatch"
				}
				input += string(mustJSON(request)) + "\n"
			}
			ctx, cancel := context.WithCancel(principal.WithContext(context.Background(), caller))
			defer cancel()
			scannerDone := make(chan struct{})
			var scannerErr error
			go func() {
				scannerErr = runStdioScanner(ctx, server, strings.NewReader(input))
				close(scannerDone)
			}()
			t.Cleanup(func() {
				unblock()
				select {
				case <-scannerDone:
				case <-time.After(5 * time.Second):
					t.Error("stdio scanner did not drain")
				}
			})
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("workspace did not enter provider")
			}
			select {
			case <-scannerDone:
				if duplicate {
					if scannerErr == nil || !strings.Contains(scannerErr.Error(), "request id is already in flight") {
						t.Errorf("duplicate request replaced accepted identity: %v", scannerErr)
					}
				} else if scannerErr != nil {
					t.Fatal(scannerErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stdio scanner remained blocked")
			}
			// Retire the publishing runtime while the accepted read is still
			// physically waiting. Only its captured State may keep the original
			// generation alive; a successor has no knowledge of this worktree.
			originalHost := rt.PluginHost
			server.rt = newTestRuntime(t, &fakeClient{})
			if _, err := rt.Cleanup(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-originalHost.ShutdownDone():
				t.Fatal("accepted workspace read lost its retained authority")
			default:
			}
			rejectAfterWait := outcome == "revoked" || outcome == "cancelled" || duplicate && outcome != "duplicate-id"
			revoked.Store(rejectAfterWait && outcome != "cancelled")
			if outcome == "cancelled" {
				cancel()
			}
			unblock()
			server.Close()
			select {
			case <-originalHost.ShutdownDone():
			default:
				t.Fatal("completed workspace read leaked its retired authority")
			}
			messages := parseOutput(t, out.String())
			if ownerChecks.Load() != 0 {
				t.Error("request borrowed the connection owner's authority")
			}
			response := responseByID(t, messages, "private")
			if len(messages) != 1 {
				t.Errorf("accepted read did not produce exactly one response: %+v", messages)
			}
			if rejectAfterWait {
				if response["error"] == nil || response["result"] != nil || strings.Contains(out.String(), "private workspace contents") {
					t.Fatalf("revoked request leaked workspace contents: %+v", response)
				}
			} else if response["error"] != nil || response["result"].(map[string]any)["text"] != "private workspace contents" {
				t.Fatalf("original scoped principal lost after dispatch: %+v", response)
			}
		})
	}
}
