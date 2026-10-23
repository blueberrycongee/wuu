package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

type modelTerminalDocuments struct {
	*persistence.Service
	entered  chan context.Context
	release  chan struct{}
	closed   chan struct{}
	closeCtx context.Context
}

func (p *modelTerminalDocuments) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method == "commit" {
		var transaction persistence.Transaction
		if err := json.Unmarshal(params.Params, &transaction); err != nil {
			return nil, err
		}
		for _, write := range transaction.Writes {
			if write.Namespace != "wuu.inference_attempts.v1" {
				continue
			}
			var attempt struct{ Phase string }
			if err := json.Unmarshal(write.Value, &attempt); err != nil {
				return nil, err
			}
			if attempt.Phase == "terminal" {
				select {
				case p.entered <- ctx:
				default:
				}
				<-p.release
				break
			}
		}
	}
	return p.Service.InvokeService(ctx, params)
}

func (p *modelTerminalDocuments) Close(ctx context.Context) error {
	p.closeCtx = ctx
	err := p.Service.Close(ctx)
	close(p.closed)
	return err
}

// Cancellation must retain the selected authority until all terminal writes
// finish, even when publication drops the last session owner in that window.
func TestModelInvocationRetainsCanceledTerminalAccounting(t *testing.T) {
	for _, method := range []string{"request", "call", "start"} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			started := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				started <- struct{}{}
				<-r.Context().Done()
			}))
			defer server.Close()
			root := t.TempDir()
			home, workspace := testWorkspaceState(t, root)
			dir := filepath.Join(home, "selected-documents")
			provider := &modelTerminalDocuments{Service: persistence.NewService("test.inference-documents", dir, "jsonl"),
				entered: make(chan context.Context, 1), release: make(chan struct{}), closed: make(chan struct{})}
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(provider.release) }) }
			defer unblock()
			modules := testStateModules()
			modules[1] = provider.ID()
			caller := &fakeServicePluginClient{id: "model-client", required: []pluginhost.ServiceRequirement{
				{Name: pluginhost.KernelModelRequestService, MajorVersion: 1, Required: true},
				{Name: pluginhost.KernelModelInvokeService, MajorVersion: 1, Required: true},
			}}
			modules = append(modules, kernelHostModule, caller.ID())
			cfg := config.Default()
			cfg.DefaultProvider = "fixture"
			cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: server.URL + "/v1", APIKey: "fixture", Model: "gpt-test"}}
			configPath := filepath.Join(home, "model-config.json")
			rawConfig, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, rawConfig, 0600); err != nil {
				t.Fatal(err)
			}
			factory := NativeProviderFactory{ID: provider.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return provider, nil }}
			callerFactory := NativeProviderFactory{ID: caller.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return caller, nil }}
			host, kernel, err := buildPluginHost(nil, root, "", home, workspace, nil, startPluginClient, nil, nil,
				&composition.Config{Version: composition.Version, Modules: &modules}, "", factory, callerFactory)
			if err != nil {
				t.Fatal(err)
			}
			generation := &PluginGeneration{id: "original-model-authority", host: host, state: kernel.state}
			generation.retain()
			kernel.bindStateGeneration(generation, nil)
			t.Cleanup(func() { _ = generation.close() })
			if err := host.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			kernel.bindModelSession(&Session{RootDir: root, HomeDir: home, ConfigPath: configPath, ConfigLoadMode: ConfigLoadFile}, host)
			unrelated := providers.NewInferenceOperation(providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
			if err := kernel.state.journal.ForOwner("unrelated").PrepareOperation(providers.InferenceOperationJournalRecord{
				Operation: unrelated, RequestHash: strings.Repeat("a", 64), At: time.Now(),
			}); err != nil {
				t.Fatal(err)
			}
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			params := pluginhost.ServiceInvokeParams{Caller: "model-client", Method: method,
				CallerScope: &pluginhost.ExecutionScope{ExecutionSnapshot: pluginhost.ExecutionSnapshot{ID: "model-caller", PluginID: "model-client"}, Context: callCtx}}
			params.Params = json.RawMessage(`{"request":{"version":1,"provider":"fixture","model":"gpt-test","messages":[{"role":"user","content":"hold"}]}}`)
			if method == "request" {
				params.Method = pluginhost.KernelServiceMethod
				params.Params = json.RawMessage(`{"messages":[{"role":"user","content":"hold"}]}`)
			}
			completed := make(chan error, 1)
			go func() {
				if method != "start" {
					service := pluginhost.KernelModelInvokeService
					if method == "request" {
						service = pluginhost.KernelModelRequestService
					}
					_, err := host.CallService(callCtx, caller.ID(), service, params.Method, params.Params)
					completed <- err
					return
				}
				invoker := &modelInvokeInvoker{parent: kernel, store: newModelInvocationStore()}
				raw, err := invoker.InvokeService(callCtx, params)
				if err != nil || method == "call" {
					completed <- err
					return
				}
				var handle pluginhost.ModelInvocationHandle
				if err := json.Unmarshal(raw, &handle); err != nil {
					completed <- err
					return
				}
				invoker.store.mu.Lock()
				job := invoker.store.live[handle.InvocationID]
				invoker.store.mu.Unlock()
				for {
					job.mu.Lock()
					done, changed, failure := job.done, job.changed, job.err
					job.mu.Unlock()
					if done {
						if failure == nil {
							completed <- nil
						} else {
							completed <- errors.New(failure.Message)
						}
						return
					}
					<-changed
				}
			}()
			select {
			case <-started:
			case err := <-completed:
				t.Fatalf("model returned before physical request: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("model did not submit")
			}
			host.StopBackground()
			if method == "start" {
				cancel()
			} else if outcome, retired := host.RetirePlugin(ctx, caller.ID(), context.Canceled); !retired || outcome.Err != nil {
				t.Fatalf("retire caller: %+v, %v", outcome, retired)
			}
			var terminalCtx context.Context
			select {
			case terminalCtx = <-provider.entered:
			case err := <-completed:
				t.Fatalf("cancellation skipped terminal attempt persistence: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("terminal attempt did not reach selected provider")
			}
			ownerReleased := make(chan struct{})
			go func() { generation.Release(); close(ownerReleased) }()
			select {
			case <-ownerReleased:
			case <-terminalCtx.Done():
			case <-time.After(10 * time.Second):
				t.Fatal("generation release stalled")
			}
			unblock()
			select {
			case err := <-completed:
				if err == nil {
					t.Error("canceled model request succeeded")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("model callback did not finish")
			}
			<-ownerReleased
			select {
			case <-provider.closed:
			case <-time.After(10 * time.Second):
				t.Fatal("completed model invocation retained its provider")
			}
			generation.revocationMu.Lock()
			closeErr := generation.closeErr
			generation.revocationMu.Unlock()
			if closeErr != nil {
				t.Errorf("generation cleanup failed: %v", closeErr)
			}
			if errors.Is(context.Cause(provider.closeCtx), context.DeadlineExceeded) {
				t.Error("model callback waited for its own registry shutdown deadline")
			}
			_, snapshot, err := persistence.Inspect(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			operations, workflows, submissions := 0, 0, 0
			for _, document := range snapshot.Documents {
				switch document.Namespace {
				case "wuu.inference_operations.v1", "wuu.inference_workflows.v1":
					var record struct{ OwnerID, Status string }
					if err := json.Unmarshal(document.Value, &record); err != nil {
						t.Fatal(err)
					}
					if record.OwnerID == "unrelated" {
						if record.Status != "active" {
							t.Errorf("shutdown invented an outcome for unrelated work: %+v", record)
						}
						continue
					}
					if record.OwnerID != "plugin:model-client" || record.Status != "canceled" {
						t.Errorf("terminal accounting lost: %s %s", document.Namespace, document.Value)
					}
					if document.Namespace == "wuu.inference_operations.v1" {
						operations++
					} else {
						workflows++
					}
				case "wuu.inference_submissions.v1":
					var receipt providers.InferenceSubmissionJournalRecord
					if err := json.Unmarshal(document.Value, &receipt); err != nil {
						t.Fatal(err)
					}
					if receipt.Outcome != providers.InferenceSubmissionFailed || receipt.FailureCategory != providers.FailureCanceled || receipt.ReportedUsage != nil || (receipt.CostState != providers.InferenceCostEstimated && receipt.CostState != providers.InferenceCostUnknownBillable) || receipt.CompletedAt.IsZero() {
						t.Errorf("cancellation lost physical request or invented reported usage: %+v", receipt)
					}
					submissions++
				}
			}
			if operations != 1 || workflows != 1 || submissions != 1 {
				t.Fatalf("durable model records = %d operations, %d workflows, %d submissions", operations, workflows, submissions)
			}
		})
	}
}

func TestStateConcurrentReleaseReportsFinalClose(t *testing.T) {
	for _, source := range []string{"invocation", "owned-invocation", "session"} {
		t.Run(source, func(t *testing.T) {
			documents, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer documents.Close()
			closeErr := errors.New("provider shutdown failed")
			for range 50 {
				client := &failingCloseClient{generationClient: &generationClient{id: "state-consumer"}, closeErr: closeErr}
				generation := &PluginGeneration{host: pluginhost.New(client), state: &runtimeState{
					status: pluginhost.StateActive, documents: documents, sessions: session.NewStore(context.Background(), documents),
				}}
				generation.retain()
				owner := &Session{pluginGeneration: generation}
				kernel := newKernelHostServices(nil, nil)
				if source == "owned-invocation" {
					kernel.bindStateGeneration(generation, owner)
				} else {
					kernel.bindStateGeneration(generation, nil)
				}
				capture := kernel.retainState
				if source == "session" {
					capture = owner.RetainState
				}
				captures := make([]*State, 16)
				for i := range captures {
					captures[i], err = capture()
					if err != nil {
						t.Fatal(err)
					}
				}
				generation.Release()
				start := make(chan struct{})
				results := make(chan error, len(captures))
				var done sync.WaitGroup
				for _, held := range captures {
					done.Add(1)
					go func() {
						defer done.Done()
						<-start
						results <- held.Close()
					}()
				}
				close(start)
				done.Wait()
				close(results)
				failures := 0
				for result := range results {
					if result != nil {
						if !errors.Is(result, closeErr) {
							t.Fatalf("unexpected cleanup error: %v", result)
						}
						failures++
					}
				}
				if failures != 1 {
					t.Fatalf("provider cleanup failure was reported by %d releases, want only the final owner", failures)
				}
			}
		})
	}
}

func TestInvocationStateRetainsOriginalAuthority(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule}
	original, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}})
	if err != nil {
		t.Fatal(err)
	}
	var completedContext context.Context
	router := NewPluginSessionRouter()
	router.Bind(nil, nil, func(ctx context.Context, _ string, _ pluginhost.SessionListParams) (pluginhost.SessionListResult, error) {
		completedContext = ctx
		captured, err := RetainInvocationState(ctx)
		if err != nil {
			return pluginhost.SessionListResult{}, err
		}
		defer captured.Close()
		if captured.Sessions != original.Sessions || captured.Host != original.Host {
			t.Fatal("kernel dispatch used another authority")
		}
		return pluginhost.SessionListResult{}, nil
	}, nil)
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(original.Generation, nil)
	service := newPluginHostServices(pluginpkg.Plugin{ID: "caller", SubjectID: "plugin:user:caller", Fingerprint: "test"}, t.TempDir(), t.TempDir(), router)
	service.activate()
	defer service.close()
	kernel.services["caller"] = service
	invoker := &kernelServiceInvoker{parent: kernel, method: pluginhost.HostServiceSessionList}
	if _, err := invoker.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "caller", Method: pluginhost.KernelServiceMethod, Params: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("kernel invocation state: %v", err)
	}
	if _, err := RetainInvocationState(completedContext); err == nil {
		t.Fatal("completed invocation admitted another state capture")
	}
	ctx := WithInvocationState(context.Background(), original)
	held, err := RetainInvocationState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if held.Sessions != original.Sessions || held.Host != original.Host || held.Generation != original.Generation {
		t.Fatal("invocation changed its captured authority")
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := held.Sessions.Create("late callback"); err != nil {
		t.Fatalf("invocation did not retain its provider: %v", err)
	}
	if _, err := RetainInvocationState(ctx); err == nil {
		t.Fatal("closed borrowed state was resurrected")
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := held.Sessions.Create("after close"); err == nil {
		t.Fatal("released state reopened storage")
	}
}

func TestInvocationStateUnavailableBeforeBindingAndAfterRetirement(t *testing.T) {
	if _, err := RetainInvocationState(context.Background()); err == nil {
		t.Fatal("missing invocation selected an implicit authority")
	}
	kernel := newKernelHostServices(nil, nil)
	if _, err := kernel.retainState(); err == nil {
		t.Fatal("preparing kernel exposed business state")
	}
	generation := &PluginGeneration{}
	kernel.bindStateGeneration(generation, nil)
	if _, err := kernel.retainState(); err == nil {
		t.Fatal("unowned candidate exposed business state")
	}
	generation.refs.Store(-1)
	if _, err := kernel.retainState(); err == nil {
		t.Fatal("retired generation was resurrected")
	}
}

func TestKernelInvocationReleasesStateAfterCallbackUnlock(t *testing.T) {
	modules := testStateModules()
	original, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}})
	if err != nil {
		t.Fatal(err)
	}
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(original.Generation, nil)
	original.Host.Add(kernel)
	router := NewPluginSessionRouter()
	router.Bind(nil, nil, func(ctx context.Context, _ string, _ pluginhost.SessionListParams) (pluginhost.SessionListResult, error) {
		binding, err := RetainInvocationState(ctx)
		if err != nil {
			return pluginhost.SessionListResult{}, err
		}
		// Publication releases the graph owner while this callback is active.
		if err := original.Close(); err != nil {
			return pluginhost.SessionListResult{}, err
		}
		if err := binding.Close(); err != nil {
			return pluginhost.SessionListResult{}, err
		}
		return pluginhost.SessionListResult{}, nil
	}, nil)
	service := newPluginHostServices(pluginpkg.Plugin{ID: "caller", SubjectID: "plugin:user:caller", Fingerprint: "test"}, t.TempDir(), t.TempDir(), router)
	service.activate()
	kernel.services["caller"] = service
	caller := &fakeServicePluginClient{id: "caller", active: true, required: []pluginhost.ServiceRequirement{{Name: pluginhost.KernelSessionListService, MajorVersion: 1, Required: true}}}
	original.Host.Add(caller)
	if conflicts := original.Host.ServiceRegistry().RegisterClients(kernel, caller); len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	if err := kernel.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := original.Host.CallService(context.Background(), caller.ID(), pluginhost.KernelSessionListService, pluginhost.KernelServiceMethod, json.RawMessage(`{}`))
		if err != nil {
			done <- err
		} else {
			done <- nil
		}
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("last callback release deadlocked kernel shutdown on its invocation lock")
	}
}

func TestOpenStateBindsNativeInvocationState(t *testing.T) {
	var retain func() (*State, error)
	modules := []string{sessionStateConsumer, persistence.JSONLModule, "example.consumer"}
	state, err := OpenState(context.Background(), StateOptions{
		RootDir: t.TempDir(), HomeDir: t.TempDir(),
		Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}},
		NativeProviders: []NativeProviderFactory{{ID: "example.consumer", Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			retain = input.RetainState
			return &fakeServicePluginClient{id: "example.consumer"}, nil
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	captured, err := retain()
	if err != nil {
		t.Fatalf("OpenState native capture: %v", err)
	}
	defer captured.Close()
	if captured.Sessions != state.Sessions || captured.Host != state.Host || captured.Generation != state.Generation {
		t.Fatal("native callback captured another graph")
	}
}
