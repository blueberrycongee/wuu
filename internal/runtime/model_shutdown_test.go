package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type shutdownModelCaller struct {
	fakeServicePluginClient
	host                         *pluginhost.Host
	method                       string
	doneURL                      string
	waitForScopeCancellation     bool
	mu                           sync.Mutex
	parentContext                context.Context
	parentCanceledBeforeTerminal bool
}

func (c *shutdownModelCaller) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if c.waitForScopeCancellation {
		c.mu.Lock()
		c.parentContext = ctx
		c.mu.Unlock()
	}
	service, method := pluginhost.KernelModelInvokeService, c.method
	input := json.RawMessage(`{"request":{"version":1,"provider":"fixture","model":"gpt-test","messages":[{"role":"user","content":"hold"}]}}`)
	if method == "request" {
		service, method = pluginhost.KernelModelRequestService, "call"
		input = json.RawMessage(`{"messages":[{"role":"user","content":"hold"}]}`)
	}
	result, err := c.host.ServiceRegistry().Call(ctx, c.ID(), pluginhost.ServiceCallParams{Service: service, Method: method, Params: input, ExecutionID: params.ExecutionID})
	if c.waitForScopeCancellation {
		<-ctx.Done()
		return result, errors.Join(err, ctx.Err())
	}
	if err != nil {
		return nil, err
	}
	if method == "start" {
		if c.doneURL != "" {
			var handle pluginhost.ModelInvocationHandle
			if err := json.Unmarshal(result, &handle); err != nil {
				return nil, err
			}
			cursor := 0
			for {
				raw, _ := json.Marshal(pluginhost.ModelInvocationReadParams{InvocationID: handle.InvocationID, Cursor: cursor, WaitMS: 1000})
				result, err := c.host.ServiceRegistry().Call(ctx, c.ID(), pluginhost.ServiceCallParams{Service: service, Method: "read", Params: raw, ExecutionID: params.ExecutionID})
				if err != nil {
					return nil, err
				}
				var read pluginhost.ModelInvocationReadResult
				if err := json.Unmarshal(result, &read); err != nil {
					return nil, err
				}
				cursor = read.Cursor
				if read.Done {
					if read.Error == nil {
						return nil, fmt.Errorf("expected model rejection")
					}
					response, err := http.Get(c.doneURL)
					if err != nil {
						return nil, err
					}
					response.Body.Close()
					break
				}
			}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return result, nil
}

type shutdownModelDocuments struct {
	*persistence.Service
	barrierURL            string
	failTerminal          bool
	beforeAttempt         chan struct{}
	failSubmission        string
	submissionFailureOnce sync.Once
	foregroundParent      *shutdownModelCaller
}

func (p *shutdownModelDocuments) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method == "commit" {
		var tx persistence.Transaction
		if err := json.Unmarshal(params.Params, &tx); err != nil {
			return nil, err
		}
		if p.failSubmission != "" {
			for _, write := range tx.Writes {
				if write.Namespace != "wuu.inference_submissions.v1" {
					continue
				}
				var receipt providers.InferenceSubmissionJournalRecord
				if err := json.Unmarshal(write.Value, &receipt); err != nil {
					return nil, err
				}
				if receipt.Outcome == providers.InferenceSubmissionInFlight {
					continue
				}
				// Reject every terminal retry, so duplicate observations cannot
				// repair a lost batch merely by winning the ticker race.
				var barrierErr error
				p.submissionFailureOnce.Do(func() {
					response, err := http.Get(p.barrierURL)
					if err != nil {
						barrierErr = err
						return
					}
					response.Body.Close()
				})
				if barrierErr != nil {
					return nil, barrierErr
				}
				return nil, &pluginhost.HostServiceError{Code: p.failSubmission, Message: "injected model submission persistence failure"}
			}
		}
		for _, write := range tx.Writes {
			terminal := false
			if p.beforeAttempt != nil && write.Namespace == "wuu.inference_operations.v1" {
				var operation struct{ Status string }
				if err := json.Unmarshal(write.Value, &operation); err != nil {
					return nil, err
				}
				if operation.Status == "active" {
					result, err := p.Service.InvokeService(ctx, params)
					if err != nil {
						return nil, err
					}
					response, err := http.Get(strings.TrimSuffix(p.barrierURL, "/terminal") + "/prepared")
					if err != nil {
						return nil, err
					}
					response.Body.Close()
					<-p.beforeAttempt
					return result, nil
				}
				terminal = true
			} else if write.Namespace == "wuu.inference_attempts.v1" {
				var attempt struct{ Phase string }
				if err := json.Unmarshal(write.Value, &attempt); err != nil {
					return nil, err
				}
				terminal = attempt.Phase == "terminal"
			} else {
				continue
			}
			if terminal {
				if p.foregroundParent != nil {
					p.foregroundParent.mu.Lock()
					if p.foregroundParent.parentContext.Err() != nil {
						p.foregroundParent.parentCanceledBeforeTerminal = true
					}
					p.foregroundParent.mu.Unlock()
				}
				response, err := http.Get(p.barrierURL)
				if err != nil {
					return nil, err
				}
				response.Body.Close()
				if p.failTerminal {
					return nil, &pluginhost.HostServiceError{Code: "state_io_error", Message: "injected model terminal persistence failure"}
				}
				break
			}
		}
	}
	return p.Service.InvokeService(ctx, params)
}

func (p *shutdownModelDocuments) Close(ctx context.Context) error {
	err := p.Service.Close(ctx)
	if os.Getenv("WUU_MODEL_SHUTDOWN_FAIL_CLOSE") == "true" {
		return errors.Join(err, errors.New("injected model authority release failure"))
	}
	return err
}

type shutdownModelPreIntentDocuments struct {
	pluginhost.ServiceInvoker
	barrierURL        string
	barrierOnce       sync.Once
	waitForRootCancel bool
	entered           chan struct{}
	canceled          chan struct{}
	canceledOnce      sync.Once
}

func (p *shutdownModelPreIntentDocuments) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method == "commit" {
		var tx persistence.Transaction
		if err := json.Unmarshal(params.Params, &tx); err != nil {
			return nil, err
		}
		for _, write := range tx.Writes {
			if write.Namespace != "wuu.inference_submissions.v1" {
				continue
			}
			var receipt providers.InferenceSubmissionJournalRecord
			if err := json.Unmarshal(write.Value, &receipt); err != nil {
				return nil, err
			}
			if receipt.Outcome == providers.InferenceSubmissionInFlight {
				continue
			}
			var barrierErr error
			p.barrierOnce.Do(func() {
				response, err := http.Get(p.barrierURL)
				if err != nil {
					barrierErr = err
					return
				}
				response.Body.Close()
				if p.waitForRootCancel {
					close(p.entered)
				}
			})
			if barrierErr != nil {
				return nil, barrierErr
			}
			if p.waitForRootCancel {
				<-ctx.Done()
				fmt.Fprintf(os.Stderr, "original terminal callback canceled by host: %v\n", ctx.Err())
				p.canceledOnce.Do(func() { close(p.canceled) })
				return p.ServiceInvoker.InvokeService(ctx, params)
			}
			// Registry admission and callback ownership are real. Cancel before
			// the selected authority can persist an intent for this transaction.
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			result, err := p.ServiceInvoker.InvokeService(canceled, params)
			fmt.Fprintf(os.Stderr, "pre-intent submission cancellation: %v\n", err)
			return result, err
		}
	}
	return p.ServiceInvoker.InvokeService(ctx, params)
}

// Required checkpoints must remain correct even when advisory telemetry drops
// every observation. This also isolates the original required callback's scope.
type shutdownCheckpointJournal struct{ providers.InferenceJournal }

func (shutdownCheckpointJournal) RecordSubmissionProgress(providers.InferenceSubmissionJournalRecord) {
}

// EOF must run cancellation and terminal persistence before the owning process
// exits. A live HTTP connection disappearing is not evidence of that drain.
// Terminal persistence failures must reach root cleanup, while an ordinary
// model rejection and cancellation with durable accounting are not close errors.
func TestModelShutdownPersistsBeforeProcessExit(t *testing.T) {
	if root := os.Getenv("WUU_MODEL_SHUTDOWN_HELPER"); root != "" {
		modelShutdownProcess(t, root)
		os.Exit(0)
	}
	for _, scenario := range []struct {
		name, method                                                         string
		failTerminal, rejectModel, terminalTimeout, failClose, beforeAttempt bool
		failSubmission                                                       string
		cancelBeforeIntent                                                   bool
		foreground, rootCancelBeforeIntent                                   bool
	}{
		{name: "request", method: "request"},
		{name: "call", method: "call"},
		{name: "start", method: "start"},
		{name: "retired-start", method: "retired-start"},
		{name: "request-terminal-failure", method: "request", failTerminal: true},
		{name: "call-terminal-failure", method: "call", failTerminal: true},
		{name: "start-terminal-failure", method: "start", failTerminal: true},
		{name: "retired-start-terminal-failure", method: "retired-start", failTerminal: true},
		{name: "retired-start-submission-canceled", method: "retired-start", failSubmission: "request_cancelled"},
		{name: "retired-start-submission-failure", method: "retired-start", failSubmission: "state_io_error"},
		{name: "retired-start-pre-intent-canceled", method: "retired-start", cancelBeforeIntent: true},
		{name: "foreground-request", method: "request", foreground: true},
		{name: "foreground-call", method: "call", foreground: true},
		{name: "foreground-request-authority-failure", method: "request", foreground: true, failClose: true},
		{name: "foreground-call-authority-failure", method: "call", foreground: true, failClose: true},
		{name: "retired-start-root-callback-timeout", method: "retired-start", rootCancelBeforeIntent: true, terminalTimeout: true},
		{name: "start-model-rejection", method: "start", rejectModel: true},
		{name: "start-terminal-timeout", method: "start", terminalTimeout: true},
		{name: "start-authority-release-failure", method: "start", failClose: true},
		{name: "request-before-attempt", method: "request", beforeAttempt: true},
		{name: "call-before-attempt", method: "call", beforeAttempt: true},
		{name: "request-before-attempt-terminal-failure", method: "request", beforeAttempt: true, failTerminal: true},
		{name: "call-before-attempt-terminal-failure", method: "call", beforeAttempt: true, failTerminal: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			started, terminal, rejected := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1)
			release := make(chan struct{})
			var once, terminalOnce sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/prepared" {
					started <- struct{}{}
					return
				}
				if r.URL.Path == "/rejected" {
					rejected <- struct{}{}
					return
				}
				if r.URL.Path == "/terminal" {
					terminalOnce.Do(func() { close(terminal) })
					if scenario.rootCancelBeforeIntent {
						return
					}
					<-release
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				started <- struct{}{}
				if scenario.rejectModel {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"injected model rejection","type":"invalid_request_error"}}`)
					return
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			defer unblock()
			child := exec.Command(os.Args[0], "-test.run=^TestModelShutdownPersistsBeforeProcessExit$")
			child.Env = append(os.Environ(), "WUU_MODEL_SHUTDOWN_HELPER="+root, "WUU_MODEL_SHUTDOWN_URL="+server.URL, "WUU_MODEL_SHUTDOWN_METHOD="+scenario.method, fmt.Sprintf("WUU_MODEL_SHUTDOWN_FAIL_TERMINAL=%t", scenario.failTerminal), fmt.Sprintf("WUU_MODEL_SHUTDOWN_REJECT=%t", scenario.rejectModel), fmt.Sprintf("WUU_MODEL_SHUTDOWN_FAIL_CLOSE=%t", scenario.failClose))
			child.Env = append(child.Env, fmt.Sprintf("WUU_MODEL_SHUTDOWN_BEFORE_ATTEMPT=%t", scenario.beforeAttempt))
			child.Env = append(child.Env, "WUU_MODEL_SHUTDOWN_FAIL_SUBMISSION="+scenario.failSubmission)
			child.Env = append(child.Env, fmt.Sprintf("WUU_MODEL_SHUTDOWN_CANCEL_BEFORE_INTENT=%t", scenario.cancelBeforeIntent))
			child.Env = append(child.Env, fmt.Sprintf("WUU_MODEL_SHUTDOWN_FOREGROUND=%t", scenario.foreground), fmt.Sprintf("WUU_MODEL_SHUTDOWN_ROOT_CANCEL_BEFORE_INTENT=%t", scenario.rootCancelBeforeIntent))
			stdin, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			var output bytes.Buffer
			child.Stdout, child.Stderr = &output, &output
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer child.Process.Kill()
			exited := make(chan error, 1)
			go func() { exited <- child.Wait() }()
			select {
			case <-started:
			case err := <-exited:
				t.Fatalf("child exited before model submission: %v\n%s", err, output.String())
			case <-time.After(15 * time.Second):
				t.Fatal("child did not submit a model request")
			}
			if scenario.rejectModel {
				unblock()
				select {
				case <-rejected:
				case err := <-exited:
					t.Fatalf("child exited before observing model rejection: %v\n%s", err, output.String())
				case <-time.After(15 * time.Second):
					t.Fatal("model rejection did not reach the caller")
				}
			}
			if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-terminal:
			case err := <-exited:
				t.Fatalf("EOF returned before canceled terminal accounting: %v\n%s", err, output.String())
			case <-time.After(15 * time.Second):
				t.Fatal("EOF did not cancel the admitted model invocation")
			}
			if !scenario.terminalTimeout {
				unblock()
			}
			select {
			case err := <-exited:
				if scenario.foreground && strings.Contains(output.String(), "foreground parent canceled before terminal accounting") {
					t.Errorf("root canceled the foreground parent before its model settled:\n%s", output.String())
				}
				if scenario.rootCancelBeforeIntent && !strings.Contains(output.String(), "original terminal callback canceled by host: context canceled") {
					t.Errorf("root did not cancel the original admitted callback:\n%s", output.String())
				}
				if scenario.failSubmission != "" || scenario.cancelBeforeIntent {
					t.Logf("submission persistence child result: %v\n%s", err, output.String())
				}
				if scenario.terminalTimeout || scenario.failClose {
					want := "drain model invocations: context deadline exceeded"
					if scenario.failClose {
						want = "injected model authority release failure"
					}
					if err == nil || !strings.Contains(output.String(), "root cleanup:") || !strings.Contains(output.String(), want) {
						t.Errorf("root cleanup lost completion failure: %v\n%s", err, output.String())
					}
				} else if scenario.failTerminal || scenario.failSubmission != "" || scenario.cancelBeforeIntent {
					want := "injected model terminal persistence failure"
					if scenario.failSubmission != "" {
						want = "injected model submission persistence failure"
					} else if scenario.cancelBeforeIntent {
						want = "context canceled"
					}
					if err == nil || !strings.Contains(output.String(), "root cleanup:") || !strings.Contains(output.String(), want) {
						t.Errorf("root cleanup lost terminal persistence failure: %v\n%s", err, output.String())
					}
				} else if err != nil {
					t.Fatalf("cleanup failed: %v\n%s", err, output.String())
				}
			case <-time.After(15 * time.Second):
				t.Fatal("EOF did not finish after terminal persistence resumed")
			}
			_, snapshot, err := persistence.Inspect(context.Background(), filepath.Join(root, "documents"))
			if err != nil {
				t.Fatal(err)
			}
			if scenario.beforeAttempt {
				for _, document := range snapshot.Documents {
					if document.Namespace == "wuu.inference_attempts.v1" || document.Namespace == "wuu.inference_submissions.v1" {
						t.Fatalf("cancellation before attempt created %s: %s", document.Namespace, document.Value)
					}
				}
			}
			if scenario.failSubmission != "" || scenario.cancelBeforeIntent {
				submissions := 0
				for _, document := range snapshot.Documents {
					if document.Namespace != "wuu.inference_submissions.v1" {
						continue
					}
					var receipt providers.InferenceSubmissionJournalRecord
					if err := json.Unmarshal(document.Value, &receipt); err != nil {
						t.Fatal(err)
					}
					if receipt.Outcome != providers.InferenceSubmissionInFlight || !receipt.CompletedAt.IsZero() || receipt.FailureCategory != "" || receipt.ReportedUsage != nil {
						t.Errorf("rejected submission commit invented a receipt: %+v", receipt)
					}
					submissions++
				}
				if submissions != 1 {
					t.Errorf("rejected terminal commit left %d submissions, want the admitted receipt", submissions)
				}
			}
			if scenario.failTerminal || scenario.terminalTimeout || scenario.failSubmission != "" || scenario.cancelBeforeIntent {
				for _, document := range snapshot.Documents {
					if document.Namespace == "wuu.inference_operations.v1" {
						var record struct{ Status string }
						if err := json.Unmarshal(document.Value, &record); err != nil {
							t.Fatal(err)
						}
						if record.Status != "active" {
							t.Errorf("failed persistence invented terminal accounting: %s", document.Value)
						}
					}
				}
				return
			}
			wantStatus := "canceled"
			if scenario.rejectModel {
				wantStatus = "failed"
			}
			operations, workflows, submissions := 0, 0, 0
			for _, document := range snapshot.Documents {
				switch document.Namespace {
				case "wuu.inference_operations.v1", "wuu.inference_workflows.v1":
					var record struct{ Status string }
					if err := json.Unmarshal(document.Value, &record); err != nil {
						t.Fatal(err)
					}
					if record.Status != wantStatus {
						t.Errorf("EOF lost terminal accounting: %s %s", document.Namespace, document.Value)
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
					if receipt.Outcome != providers.InferenceSubmissionFailed || (scenario.rejectModel && receipt.FailureCategory == providers.FailureCanceled || !scenario.rejectModel && receipt.FailureCategory != providers.FailureCanceled) || receipt.CompletedAt.IsZero() || receipt.ReportedUsage != nil || (receipt.CostState != providers.InferenceCostEstimated && receipt.CostState != providers.InferenceCostUnknownBillable) {
						t.Errorf("EOF lost physical outcome or invented usage: %+v", receipt)
					}
					submissions++
				}
			}
			wantSubmissions := 1
			if scenario.beforeAttempt {
				wantSubmissions = 0
			}
			if operations != 1 || workflows != 1 || submissions != wantSubmissions {
				t.Fatalf("records: %d operations, %d workflows, %d submissions", operations, workflows, submissions)
			}
		})
	}
}

func modelShutdownProcess(t *testing.T, root string) {
	ctx := context.Background()
	home := filepath.Join(root, "home")
	workspace, err := resolveWorkspaceStateDir(home, "", root)
	if err != nil {
		t.Fatal(err)
	}
	provider := &shutdownModelDocuments{Service: persistence.NewService("test.shutdown-documents", filepath.Join(root, "documents"), "jsonl"), barrierURL: os.Getenv("WUU_MODEL_SHUTDOWN_URL") + "/terminal", failTerminal: os.Getenv("WUU_MODEL_SHUTDOWN_FAIL_TERMINAL") == "true"}
	provider.failSubmission = os.Getenv("WUU_MODEL_SHUTDOWN_FAIL_SUBMISSION")
	if os.Getenv("WUU_MODEL_SHUTDOWN_BEFORE_ATTEMPT") == "true" {
		provider.beforeAttempt = make(chan struct{})
	}
	caller := &shutdownModelCaller{fakeServicePluginClient: fakeServicePluginClient{id: "model-client", required: []pluginhost.ServiceRequirement{
		{Name: pluginhost.KernelModelInvokeService, MajorVersion: 1, Required: true},
		{Name: pluginhost.KernelModelRequestService, MajorVersion: 1, Required: true},
	}, provided: []pluginhost.ServiceDescriptor{{Name: "test.shutdown-model", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "run", InputSchema: "test.input", OutputSchema: "test.output"}}}}}, method: strings.TrimPrefix(os.Getenv("WUU_MODEL_SHUTDOWN_METHOD"), "retired-")}
	if os.Getenv("WUU_MODEL_SHUTDOWN_REJECT") == "true" {
		caller.doneURL = os.Getenv("WUU_MODEL_SHUTDOWN_URL") + "/rejected"
	}
	foreground := os.Getenv("WUU_MODEL_SHUTDOWN_FOREGROUND") == "true"
	caller.waitForScopeCancellation = foreground
	if foreground {
		provider.foregroundParent = caller
	}
	modules := testStateModules()
	modules[1] = provider.ID()
	modules = append(modules, kernelHostModule, caller.ID())
	factories := []NativeProviderFactory{
		{ID: provider.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return provider, nil }},
		{ID: caller.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return caller, nil }},
	}
	foregroundOwner := &fakeServicePluginClient{id: "foreground-owner", required: []pluginhost.ServiceRequirement{{Name: "test.shutdown-model", MajorVersion: 1, Required: true}}}
	if foreground {
		modules = append(modules, foregroundOwner.ID())
		factories = append(factories, NativeProviderFactory{ID: foregroundOwner.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return foregroundOwner, nil }})
	}
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: os.Getenv("WUU_MODEL_SHUTDOWN_URL") + "/v1", APIKey: "fixture", Model: "gpt-test"}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	host, kernel, err := buildPluginHost(nil, root, "", home, workspace, nil, startPluginClient, nil, nil,
		&composition.Config{Version: composition.Version, Modules: &modules}, "", factories...)
	if err != nil {
		t.Fatal(err)
	}
	var preIntentAuthority *StateAuthority
	var preIntentGate *shutdownModelPreIntentDocuments
	rootCancelBeforeIntent := os.Getenv("WUU_MODEL_SHUTDOWN_ROOT_CANCEL_BEFORE_INTENT") == "true"
	if os.Getenv("WUU_MODEL_SHUTDOWN_CANCEL_BEFORE_INTENT") == "true" || rootCancelBeforeIntent {
		if err := host.ServiceRegistry().WrapServiceInvoker(persistence.ServiceName, 1, func(invoker pluginhost.ServiceInvoker) pluginhost.ServiceInvoker {
			preIntentAuthority = invoker.(*authorityInvoker).authority
			preIntentGate = &shutdownModelPreIntentDocuments{ServiceInvoker: invoker, barrierURL: provider.barrierURL, waitForRootCancel: rootCancelBeforeIntent, entered: make(chan struct{}), canceled: make(chan struct{})}
			return preIntentGate
		}); err != nil {
			t.Fatal(err)
		}
	}
	generation := &PluginGeneration{id: "shutdown-model-generation", host: host, state: kernel.state}
	generation.retain()
	owner := &Session{RootDir: root, WuuHome: home, HomeDir: home, ConfigPath: configPath, ConfigLoadMode: ConfigLoadFile, PluginHost: host, pluginGeneration: generation, Documents: kernel.state.documents, Sessions: kernel.state.sessions, InferenceJournalRuntime: kernel.state.journal}
	kernel.bindStateGeneration(generation, owner)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	kernel.bindModelSession(owner, host)
	owner.Documents, owner.Sessions, owner.InferenceJournalRuntime = kernel.state.documents, kernel.state.sessions, kernel.state.journal
	shadow, err := owner.cloneForThreadModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shadow.Cleanup(); err != nil {
		t.Fatal(err)
	}
	captured, err := owner.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	caller.host = host
	foregroundDone := make(chan struct{})
	if rootCancelBeforeIntent {
		ctx = providers.WithInferenceJournal(ctx, shutdownCheckpointJournal{owner.InferenceJournalRuntime.ForOwner("plugin:" + caller.ID())})
	}
	if foreground {
		go func() {
			_, _ = host.CallService(ctx, foregroundOwner.ID(), "test.shutdown-model", "run", json.RawMessage(`{}`))
			close(foregroundDone)
		}()
	} else if _, err := host.StartBackground(ctx, caller.ID(), pluginhost.BackgroundStartParams{Key: "shutdown", Service: "test.shutdown-model", Method: "run"}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
	if provider.beforeAttempt != nil {
		host.StopBackground()
		host.CancelExecutions(context.Canceled)
		close(provider.beforeAttempt)
	}
	if strings.HasPrefix(os.Getenv("WUU_MODEL_SHUTDOWN_METHOD"), "retired-") {
		host.StopBackground()
		owner.retiredPluginGenerations = map[*PluginGeneration]struct{}{generation: {}}
		owner.pluginGeneration, owner.PluginHost = nil, nil
		generation.Release()
	}
	if rootCancelBeforeIntent {
		select {
		case <-preIntentGate.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("terminal checkpoint did not enter its original callback")
		}
	}
	if os.Getenv("WUU_MODEL_SHUTDOWN_FAIL_CLOSE") == "true" {
		if err := captured.Close(); err != nil {
			t.Fatal(err)
		}
	}
	_, cleanupErr := owner.Cleanup()
	if rootCancelBeforeIntent {
		select {
		case <-preIntentGate.canceled:
		case <-time.After(10 * time.Second):
			t.Fatal("root cleanup did not cancel the original terminal callback")
		}
	}
	if foreground {
		caller.mu.Lock()
		earlyCancellation := caller.parentCanceledBeforeTerminal
		caller.mu.Unlock()
		if earlyCancellation {
			fmt.Fprintln(os.Stderr, "foreground parent canceled before terminal accounting")
		}
		select {
		case <-foregroundDone:
		case <-time.After(10 * time.Second):
			t.Fatal("foreground parent did not finish after root cleanup")
		}
	}
	if cleanupErr != nil {
		t.Fatalf("root cleanup: %v", cleanupErr)
	}
	if preIntentAuthority != nil {
		record, err := preIntentAuthority.readRecord()
		if err != nil {
			t.Fatal(err)
		}
		if record.Pending != nil {
			t.Fatal("pre-intent cancellation left a recoverable pending transaction")
		}
		fmt.Fprintln(os.Stdout, "pre-intent cancellation left no pending intent")
	}
	if os.Getenv("WUU_MODEL_SHUTDOWN_FAIL_TERMINAL") == "true" || provider.failSubmission != "" || preIntentAuthority != nil {
		fmt.Fprintln(os.Stdout, "root cleanup returned without terminal persistence error")
		return
	}
	if _, err := captured.Sessions.Create("independently retained state"); err != nil {
		t.Fatal(err)
	}
	if err := captured.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "cleanup returned")
}
