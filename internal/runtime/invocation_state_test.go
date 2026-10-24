package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
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

type modelReceiptStreamClient struct{ stream <-chan providers.StreamEvent }

func (c modelReceiptStreamClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream-only fixture")
}

func (c modelReceiptStreamClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	return c.stream, nil
}

type turnReceiptStreamClient struct {
	stream   chan providers.StreamEvent
	admitted chan providers.ChatRequest
}

func (c turnReceiptStreamClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream-only turn fixture")
}

func (c turnReceiptStreamClient) StreamChat(_ context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if _, err := request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "fixture", Protocol: "fixture", Transport: "http", Mode: "stream"}); err != nil {
		return nil, err
	}
	c.admitted <- request
	return c.stream, nil
}

type turnReceiptJournal struct {
	shutdownCheckpointJournal
	logicalDone chan struct{}
	once        sync.Once
}

func (j *turnReceiptJournal) CompleteOperation(record providers.InferenceOperationTerminalRecord) error {
	err := j.InferenceJournal.CompleteOperation(record)
	j.once.Do(func() { close(j.logicalDone) })
	return err
}

// Ordinary turns must retain their selected generation through physical receipt
// accounting, including after logical cancellation and publication of a successor.
func TestOrdinaryTurnReceiptsSurviveGenerationPublication(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("timeout_%t", timeout), func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			home, workspace := testWorkspaceState(t, root)
			owner := &Session{RootDir: root, WuuHome: home, HomeDir: home, StateDir: workspace}
			build := func(name string) (*PluginGeneration, *modelTerminalDocuments) {
				t.Helper()
				provider := &modelTerminalDocuments{Service: persistence.NewService("test.turn-documents", filepath.Join(root, "turn-documents"), "jsonl"), entered: make(chan context.Context, 1), release: make(chan struct{}), closed: make(chan struct{})}
				close(provider.release)
				modules := testStateModules()
				modules[1] = provider.ID()
				host, kernel, err := buildPluginHost(nil, root, "", home, workspace, nil, startPluginClient, nil, nil,
					&composition.Config{Version: composition.Version, Modules: &modules}, "",
					NativeProviderFactory{ID: provider.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return provider, nil }})
				if err != nil {
					t.Fatal(err)
				}
				generation := &PluginGeneration{id: name, host: host, state: kernel.state, hooks: hooks.NewDispatcher(nil)}
				kernel.bindStateGeneration(generation, owner)
				return generation, provider
			}
			original, documents := build("turn-original")
			original.retain()
			owner.pluginGeneration = original
			if err := original.host.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			physical := turnReceiptStreamClient{stream: make(chan providers.StreamEvent, 1), admitted: make(chan providers.ChatRequest, 1)}
			unblock := sync.OnceFunc(func() {
				physical.stream <- providers.StreamEvent{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 71, OutputTokens: 13}}
				close(physical.stream)
			})
			defer unblock()
			owner.StreamRunner = &agent.StreamRunner{Client: physical, ProviderName: "fixture", Model: "fixture", LoopDriver: agent.DefaultDriver{}, DisableAutoCompact: true}
			owner.applyPluginGeneration(original)
			t.Cleanup(func() { _, _ = owner.Cleanup() })
			captured, err := owner.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			defer captured.Close()
			runner := cloneStreamRunnerForThread(owner.StreamRunner, nil)
			journal := &turnReceiptJournal{shutdownCheckpointJournal: shutdownCheckpointJournal{original.state.journal.ForOwner("ordinary-turn")}, logicalDone: make(chan struct{})}
			runner.InferenceJournal = journal
			engine := &wuuEngineSession{runner: runner}
			returned := make(chan error, 1)
			go func() {
				_, turnErr := engine.RunTurn(ctx, agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "hold"}}}, nil)
				returned <- errors.Join(turnErr, captured.Close())
			}()
			var request providers.ChatRequest
			select {
			case request = <-physical.admitted:
			case err := <-returned:
				t.Fatalf("turn returned before physical admission: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("turn did not enter the native stream")
			}
			admitted := request.Execution.Snapshot().Submissions
			if len(admitted) != 1 {
				t.Fatalf("admitted physical submissions = %d", len(admitted))
			}
			replacement, _ := build("turn-replacement")
			if err := owner.ActivatePluginGeneration(replacement, nil); err != nil {
				t.Fatal(err)
			}
			if owner.pluginGeneration != replacement || captured.Generation != original {
				t.Fatal("publication did not preserve the turn's captured generation")
			}
			if err := engine.Interrupt(ctx, "cancel"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-journal.logicalDone:
			case <-time.After(10 * time.Second):
				t.Fatal("logical turn accounting did not finish after cancellation")
			}
			if !timeout {
				unblock()
			}
			select {
			case err := <-returned:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("ordinary turn lost cancellation: %v", err)
				}
				if timeout && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("incomplete turn cleanup = %v, want cleanup deadline", err)
				} else if !timeout && errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("completed receipt cleanup exceeded its deadline: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("ordinary turn cleanup did not honor its bounded wait")
			}
			if timeout {
				select {
				case <-documents.closed:
					t.Error("turn timeout released authority before the physical producer ended")
				default:
				}
				if _, err := captured.Documents.Read(ctx, persistence.Query{Namespace: "wuu.inference_submissions.v1"}); err != nil {
					t.Errorf("pending producer lost its original documents: %v", err)
				}
				unblock()
			}
			select {
			case <-documents.closed:
			case <-time.After(10 * time.Second):
				t.Fatal("physical completion did not release the retired generation")
			}
			_, snapshot, err := persistence.Inspect(ctx, filepath.Join(root, "turn-documents"))
			if err != nil {
				t.Fatal(err)
			}
			receiptCount := 0
			for _, document := range snapshot.Documents {
				if document.Namespace != "wuu.inference_submissions.v1" {
					continue
				}
				receiptCount++
				var receipt providers.InferenceSubmissionJournalRecord
				if err := json.Unmarshal(document.Value, &receipt); err != nil {
					t.Fatal(err)
				}
				if receipt.ID != admitted[0].ID || receipt.OperationID != request.Operation.ID || receipt.AttemptID != request.Attempt.ID {
					t.Errorf("receipt lost its admitted identity: %+v", receipt)
				}
				// Logical cancellation may checkpoint before the native observer's
				// terminal event. An aborted final checkpoint preserves that unknown.
				if !(timeout && receipt.Outcome == providers.InferenceSubmissionInFlight) && (receipt.Outcome != providers.InferenceSubmissionFailed || receipt.FailureCategory != providers.FailureCanceled) {
					t.Errorf("receipt lost cancellation or invented physical completion: %+v", receipt)
				}
				if !timeout && (receipt.ReportedUsage == nil || receipt.ReportedUsage.InputTokens != 71 || receipt.ReportedUsage.OutputTokens != 13) {
					t.Errorf("final required checkpoint lost late physical usage: %+v", receipt)
				}
				if timeout && receipt.ReportedUsage != nil {
					t.Errorf("aborted cleanup invented a successful final checkpoint: %+v", receipt)
				}
			}
			if receiptCount != 1 {
				t.Errorf("physical receipts = %d", receiptCount)
			}
			if _, err := replacement.state.documents.Read(ctx, persistence.Query{Namespace: "wuu.inference_submissions.v1"}); err != nil {
				t.Errorf("retiring the original generation closed replacement authority: %v", err)
			}
		})
	}
}

type modelReceiptCapabilityClient struct {
	fakeServicePluginClient
	capability pluginhost.CapabilityDescriptor
	adapters   []pluginhost.ModelAdapterDescriptor
	invoke     func(context.Context, pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error)
}

func (c *modelReceiptCapabilityClient) ProtocolVersion() int {
	return pluginhost.CapabilityProtocolVersion
}
func (c *modelReceiptCapabilityClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{c.capability}
}
func (c *modelReceiptCapabilityClient) ModelAdapters() []pluginhost.ModelAdapterDescriptor {
	return c.adapters
}
func (c *modelReceiptCapabilityClient) InvokeCapability(ctx context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	return c.invoke(ctx, params)
}

type modelReceiptContinuationClient struct{ scope *providers.ModelReceiptScope }

func (c modelReceiptContinuationClient) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	if providers.ModelReceiptScopeFromContext(ctx) != c.scope {
		return providers.ChatResponse{}, errors.New("direct continuation replaced its parent receipt scope")
	}
	_, end, err := providers.BeginModelReceipt(ctx)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	end(nil)
	return providers.ChatResponse{Content: "continued", FinishReason: providers.FinishReasonStop}, nil
}
func (c modelReceiptContinuationClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	return nil, errors.New("synchronous continuation fixture")
}

type modelReceiptHeldClient struct {
	started chan struct{}
	release chan struct{}
	done    chan struct{}
}

func (c modelReceiptHeldClient) Chat(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
	_, end, err := providers.BeginModelReceipt(ctx)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	submission, err := request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "fixture", Protocol: "fixture", Transport: "http", Mode: "chat"})
	if err != nil {
		end(nil)
		return providers.ChatResponse{}, err
	}
	go func() {
		defer end(nil)
		defer close(c.done)
		<-c.release
		submission.ObserveUsage(&providers.TokenUsage{InputTokens: 71, OutputTokens: 13})
		submission.CompleteFailure(providers.NormalizeFailure(context.Canceled))
	}()
	close(c.started)
	<-ctx.Done()
	return providers.ChatResponse{}, ctx.Err()
}

func (c modelReceiptHeldClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	return nil, errors.New("synchronous held receipt fixture")
}

func TestModelReceiptOwnerRetainsAuthorityAfterTimeout(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("timeout_%t", timeout), func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			home, workspace := testWorkspaceState(t, root)
			provider := &modelTerminalDocuments{Service: persistence.NewService("test.receipt-documents", filepath.Join(root, "documents"), "jsonl"), entered: make(chan context.Context, 1), release: make(chan struct{}), closed: make(chan struct{})}
			close(provider.release)
			caller := &modelReceiptCapabilityClient{
				fakeServicePluginClient: fakeServicePluginClient{id: "receipt-middleware", required: []pluginhost.ServiceRequirement{{Name: pluginhost.KernelModelInvokeService, MajorVersion: 1, Required: true}}},
				capability:              pluginhost.CapabilityDescriptor{ID: pluginhost.CapabilityModelMiddleware, Kind: "transform", Version: 1, ErrorPolicy: pluginhost.ErrorPolicyPropagate},
			}
			modules := testStateModules()
			modules[1] = provider.ID()
			modules = append(modules, kernelHostModule, caller.ID())
			host, kernel, err := buildPluginHost(nil, root, "", home, workspace, nil, startPluginClient, nil, nil,
				&composition.Config{Version: composition.Version, Modules: &modules}, "",
				NativeProviderFactory{ID: provider.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return provider, nil }},
				NativeProviderFactory{ID: caller.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return caller, nil }})
			if err != nil {
				t.Fatal(err)
			}
			generation := &PluginGeneration{id: "held-receipt", host: host, state: kernel.state}
			generation.retain()
			owner := &Session{RootDir: root, HomeDir: home, PluginHost: host, pluginGeneration: generation}
			kernel.bindStateGeneration(generation, owner)
			if err := host.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			kernel.bindModelSession(owner, host)
			t.Cleanup(func() { _, _ = owner.Cleanup() })
			physical := modelReceiptHeldClient{started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
			var once sync.Once
			unblock := func() { once.Do(func() { close(physical.release) }) }
			defer unblock()
			caller.invoke = func(callCtx context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
				var input pluginhost.ModelMiddlewareInput
				if err := json.Unmarshal(params.Input, &input); err != nil {
					return pluginhost.CapabilityInvokeResult{}, err
				}
				raw, err := json.Marshal(pluginhost.ModelInvokeParams{ContinuationID: input.ContinuationID, Request: input.Request})
				if err != nil {
					return pluginhost.CapabilityInvokeResult{}, err
				}
				_, callErr := host.ServiceRegistry().Call(callCtx, caller.ID(), pluginhost.ServiceCallParams{Service: pluginhost.KernelModelInvokeService, Method: "call", Params: raw, ExecutionID: params.ExecutionID})
				return pluginhost.CapabilityInvokeResult{}, callErr
			}
			capability, ok := host.Capability(caller.ID(), pluginhost.CapabilityModelMiddleware)
			if !ok {
				t.Fatal("middleware capability unavailable")
			}
			ctx = providers.WithInferenceJournal(ctx, shutdownCheckpointJournal{kernel.state.journal.ForOwner("receipt-fixture")})
			request, err := providers.EnsureInferenceAttemptContext(ctx, providers.ChatRequest{Provider: "fixture", Model: "fixture"}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
			if err != nil {
				t.Fatal(err)
			}
			client := &modelMiddlewareClient{host: host, capability: capability, next: physical}
			returned := make(chan error, 1)
			go func() { _, err := client.Chat(ctx, request); returned <- err }()
			select {
			case <-physical.started:
			case err := <-returned:
				t.Fatalf("model returned before physical admission: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("model did not enter the physical client")
			}
			admitted := request.Execution.Snapshot().Submissions
			if len(admitted) != 1 {
				t.Fatalf("admitted physical submissions = %d", len(admitted))
			}
			cleaned := make(chan error, 1)
			go func() { _, err := owner.Cleanup(); cleaned <- err }()
			select {
			case err := <-returned:
				if err == nil {
					t.Error("canceled model succeeded")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("logical cancellation waited for the physical receipt")
			}
			if !timeout {
				unblock()
			}
			select {
			case err := <-cleaned:
				if timeout && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("incomplete producer cleanup = %v, want shared shutdown deadline", err)
				} else if !timeout && err != nil {
					t.Errorf("completed producer cleanup = %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("cleanup did not honor its bounded wait")
			}
			if timeout {
				select {
				case <-provider.closed:
					t.Error("timeout released the authority while the producer could still write")
				default:
				}
				if _, err := kernel.state.documents.Read(ctx, persistence.Query{Namespace: "wuu.inference_submissions.v1"}); err != nil {
					t.Errorf("retained producer lost its selected documents: %v", err)
				}
				unblock()
			}
			select {
			case <-physical.done:
			case <-time.After(10 * time.Second):
				t.Fatal("physical producer did not finish after release")
			}
			select {
			case <-provider.closed:
			case <-time.After(10 * time.Second):
				t.Fatal("actual producer completion did not release its authority")
			}
			_, snapshot, err := persistence.Inspect(context.Background(), filepath.Join(root, "documents"))
			if err != nil {
				t.Fatal(err)
			}
			receiptCount := 0
			for _, document := range snapshot.Documents {
				if document.Namespace != "wuu.inference_submissions.v1" {
					continue
				}
				receiptCount++
				var receipt providers.InferenceSubmissionJournalRecord
				if err := json.Unmarshal(document.Value, &receipt); err != nil {
					t.Fatal(err)
				}
				if receipt.ID != admitted[0].ID || receipt.OperationID != request.Operation.ID || receipt.AttemptID != request.Attempt.ID {
					t.Errorf("final checkpoint changed the admitted submission identity: %+v", receipt)
				}
				if timeout {
					if receipt.Outcome != providers.InferenceSubmissionInFlight || receipt.ReportedUsage != nil {
						t.Errorf("aborted checkpoint claimed completed evidence: %+v", receipt)
					}
				} else if receipt.Outcome != providers.InferenceSubmissionFailed || receipt.FailureCategory != providers.FailureCanceled || receipt.ReportedUsage == nil || receipt.ReportedUsage.InputTokens != 71 || receipt.ReportedUsage.OutputTokens != 13 {
					t.Errorf("final required checkpoint lost physical evidence: %+v", receipt)
				}
			}
			if receiptCount != 1 {
				t.Errorf("physical receipts = %d", receiptCount)
			}
		})
	}
}

func TestDirectModelContinuationDoesNotJoinOrSealParentReceiptScope(t *testing.T) {
	ctx, receipts := providers.NewModelReceiptScope(context.Background())
	parentCtx, endParent, err := providers.BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer endParent(nil)
	defer receipts.Seal()
	caller := &modelReceiptCapabilityClient{
		fakeServicePluginClient: fakeServicePluginClient{id: "receipt-middleware", required: []pluginhost.ServiceRequirement{{Name: pluginhost.KernelModelInvokeService, MajorVersion: 1, Required: true}}},
		capability:              pluginhost.CapabilityDescriptor{ID: pluginhost.CapabilityModelMiddleware, Kind: "transform", Version: 1, ErrorPolicy: pluginhost.ErrorPolicyPropagate},
	}
	root := t.TempDir()
	home, workspace := testWorkspaceState(t, root)
	modules := append(testStateModules(), kernelHostModule, caller.ID())
	host, kernel, err := buildPluginHost(nil, root, "", home, workspace, nil, startPluginClient, nil, nil,
		&composition.Config{Version: composition.Version, Modules: &modules}, "",
		NativeProviderFactory{ID: caller.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return caller, nil }})
	if err != nil {
		t.Fatal(err)
	}
	generation := &PluginGeneration{id: "receipt-continuation", host: host, state: kernel.state}
	generation.retain()
	owner := &Session{RootDir: root, HomeDir: home, PluginHost: host, pluginGeneration: generation}
	kernel.bindStateGeneration(generation, owner)
	t.Cleanup(func() {
		if _, err := owner.Cleanup(); err != nil {
			t.Error(err)
		}
	})
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	kernel.bindModelSession(owner, host)
	caller.invoke = func(callCtx context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
		var input pluginhost.ModelMiddlewareInput
		if err := json.Unmarshal(params.Input, &input); err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		raw, err := json.Marshal(pluginhost.ModelInvokeParams{ContinuationID: input.ContinuationID, Request: input.Request})
		if err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		result, callErr := host.ServiceRegistry().Call(callCtx, caller.ID(), pluginhost.ServiceCallParams{Service: pluginhost.KernelModelInvokeService, Method: "call", Params: raw, ExecutionID: params.ExecutionID})
		if callErr != nil {
			return pluginhost.CapabilityInvokeResult{}, callErr
		}
		var response pluginhost.ModelInvokeResult
		if err := json.Unmarshal(result, &response); err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		output, err := json.Marshal(pluginhost.ModelAdapterOutput{Response: &response.Response})
		return pluginhost.CapabilityInvokeResult{Output: output}, err
	}
	capability, ok := host.Capability(caller.ID(), pluginhost.CapabilityModelMiddleware)
	if !ok {
		t.Fatal("middleware capability unavailable")
	}
	request, err := providers.EnsureInferenceAttemptContext(parentCtx, providers.ChatRequest{Provider: "fixture", Model: "fixture"}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	client := &modelMiddlewareClient{host: host, capability: capability, next: modelReceiptContinuationClient{scope: receipts}}
	finished := make(chan error, 1)
	go func() {
		_, err := client.Chat(parentCtx, request)
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("direct continuation waited for its still-running parent receipt")
	}
	_, end, err := providers.BeginModelReceipt(ctx)
	if err != nil {
		t.Fatalf("direct continuation sealed its parent scope: %v", err)
	}
	end(nil)
	endParent(nil)
	receipts.Seal()
	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := receipts.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
}

func TestDirectAdapterReceiptScopeRetainsCanceledCallback(t *testing.T) {
	modelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, receipts := providers.NewModelReceiptScope(modelCtx)
	caller := &modelReceiptCapabilityClient{
		fakeServicePluginClient: fakeServicePluginClient{id: "receipt-adapter"},
		capability:              pluginhost.CapabilityDescriptor{ID: pluginhost.CapabilityModelAdapter, Kind: "transform", Version: 1, ErrorPolicy: pluginhost.ErrorPolicyPropagate},
		adapters:                []pluginhost.ModelAdapterDescriptor{{ID: "test.receipt", Version: 1, Mode: "direct"}},
	}
	host := pluginhost.New(caller)
	defer host.Close(context.Background())
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	accepted := make(chan error, 1)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	caller.invoke = func(callCtx context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
		accepted <- host.EmitModelEvent(callCtx, caller.ID(), params.ExecutionID, pluginhost.ModelStreamEventV1{Type: "usage", Usage: &pluginhost.ModelRequestUsage{InputTokens: 71, OutputTokens: 13}})
		<-release
		return pluginhost.CapabilityInvokeResult{}, callCtx.Err()
	}
	request, err := providers.EnsureInferenceAttemptContext(ctx, providers.ChatRequest{Provider: "fixture", Model: "fixture", Adapter: "test.receipt"}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	client := &modelAdapterClient{host: host}
	logical, err := client.StreamChat(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("direct adapter did not emit its usage receipt")
	}
	cancel()
	receipts.Seal()
	logicalDone := make(chan struct{})
	go func() {
		for range logical {
		}
		close(logicalDone)
	}()
	select {
	case <-logicalDone:
	case <-time.After(10 * time.Second):
		t.Error("logical cancellation waited for the direct adapter callback")
	}
	select {
	case <-receipts.Done():
		t.Error("logical cancellation declared the direct adapter callback complete")
	default:
	}
	unblock()
	waitCtx, stopWait := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopWait()
	if err := receipts.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-logicalDone:
	case <-waitCtx.Done():
		t.Fatal("logical relay retained the completed direct adapter")
	}
	submissions := request.Execution.Snapshot().Submissions
	if len(submissions) != 1 || submissions[0].Outcome != providers.InferenceSubmissionFailed || submissions[0].FailureCategory != providers.FailureCanceled || submissions[0].ReportedUsage == nil || submissions[0].ReportedUsage.InputTokens != 71 || submissions[0].ReportedUsage.OutputTokens != 13 {
		t.Errorf("direct adapter cancellation lost validated receipts: %+v", submissions)
	}
}

// Logical cancellation/error can finish before the native adapter's observer.
// Its tail must remain joined, retain late usage, and ignore events after done.
func TestNativeModelReceiptScopeJoinsObserverTail(t *testing.T) {
	for _, terminal := range []string{"cancel", "error", "done"} {
		t.Run(terminal, func(t *testing.T) {
			modelCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, scope := providers.NewModelReceiptScope(modelCtx)
			request, err := providers.EnsureInferenceAttemptContext(ctx, providers.ChatRequest{Provider: "fixture", Model: "fixture"}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "fixture", Protocol: "fixture", Transport: "http", Mode: "stream"}); err != nil {
				t.Fatal(err)
			}
			physical := make(chan providers.StreamEvent)
			var closeOnce sync.Once
			closePhysical := func() { closeOnce.Do(func() { close(physical) }) }
			defer closePhysical()
			client := &modelAdapterClient{inner: modelReceiptStreamClient{stream: physical}}
			logical, err := client.StreamChat(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if terminal == "cancel" {
				cancel()
			} else {
				event := providers.StreamEvent{Type: providers.EventDone}
				if terminal == "error" {
					event = providers.StreamEvent{Type: providers.EventError, Error: context.Canceled}
				}
				select {
				case physical <- event:
				case <-time.After(10 * time.Second):
					t.Fatal("native observer did not accept the terminal event")
				}
			}
			logicalDone := make(chan struct{})
			go func() {
				for range logical {
				}
				close(logicalDone)
			}()
			select {
			case <-logicalDone:
			case <-time.After(10 * time.Second):
				t.Fatal("logical result waited for the still-open physical tail")
			}
			scope.Seal()
			select {
			case <-scope.Done():
				t.Error("logical stream closure declared the native receipt observer complete")
			default:
			}
			expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer stop()
			if err := scope.Wait(expired); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("pending receipt wait = %v, want owner deadline", err)
			}
			usage := &providers.TokenUsage{InputTokens: 71, OutputTokens: 13}
			select {
			case physical <- providers.StreamEvent{Type: providers.EventUsage, Usage: usage}:
			case <-time.After(10 * time.Second):
				t.Fatal("native receipt tail stopped accepting buffered evidence")
			}
			closePhysical()
			waitCtx, stopWait := context.WithTimeout(context.Background(), 10*time.Second)
			defer stopWait()
			if err := scope.Wait(waitCtx); err != nil {
				t.Fatal(err)
			}
			receipts := request.Execution.Snapshot().Submissions
			if len(receipts) != 1 {
				t.Fatalf("physical submissions = %d", len(receipts))
			}
			if terminal == "done" {
				if receipts[0].Outcome != providers.InferenceSubmissionSucceeded || receipts[0].ReportedUsage != nil {
					t.Errorf("events after done changed the final receipt: %+v", receipts[0])
				}
			} else if receipts[0].Outcome != providers.InferenceSubmissionFailed || receipts[0].FailureCategory != providers.FailureCanceled || receipts[0].ReportedUsage == nil || *receipts[0].ReportedUsage != *usage {
				t.Errorf("late usage changed the terminal outcome or was lost: %+v", receipts[0])
			}
		})
	}
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

func TestStateModelReceiptsRetainAuthorityAndReplaceExpiredParent(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule, "wuu.session.default"}
	original, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}})
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	admittedCtx, cancelAdmission := context.WithCancel(WithInvocationState(context.Background(), original))
	defer cancelAdmission()
	parentCtx, parent := providers.NewModelReceiptScope(admittedCtx)
	parent.Seal()
	ctx, complete, err := original.BeginModelReceipts(parentCtx)
	if err != nil {
		t.Fatalf("independent captured-state work rejected an expired parent: %v", err)
	}
	if providers.ModelReceiptScopeFromContext(ctx) == parent {
		t.Fatal("independent captured-state work inherited its expired parent scope")
	}
	cancelAdmission()
	original.Generation.modelMu.Lock()
	var completionContext context.Context
	for owner := range original.Generation.modelOwners {
		completionContext = owner.completionCtx
	}
	original.Generation.modelMu.Unlock()
	if completionContext == nil || completionContext.Err() != nil {
		t.Fatal("caller cancellation revoked receipt completion")
	}
	retained, err := RetainInvocationState(completionContext)
	if err != nil {
		t.Fatalf("receipt completion lost admitted invocation authority: %v", err)
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	retained, err = RetainInvocationState(completionContext)
	if err != nil {
		t.Fatalf("receipt owner depended on the caller's released capture: %v", err)
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := original.Documents.Read(completionContext, persistence.Query{Namespace: "receipt-test"}); err != nil {
		t.Fatalf("receipt owner did not retain its captured authority: %v", err)
	}
	if err := complete(); err != nil {
		t.Fatal(err)
	}
	if _, err := RetainInvocationState(completionContext); err == nil {
		t.Fatal("finished receipt context admitted new state work")
	}
	if _, err := original.Documents.Read(context.Background(), persistence.Query{Namespace: "receipt-test"}); err == nil {
		t.Fatal("completed receipt owner retained its authority")
	}
	if _, _, err := original.BeginModelReceipts(context.Background()); err == nil {
		t.Fatal("closed captured State admitted another inference")
	}
	unboundClosed := false
	unbound := &State{close: func() error { unboundClosed = true; return nil }}
	if _, _, err := unbound.BeginModelReceipts(context.Background()); err == nil {
		t.Fatal("generationless State silently admitted unowned inference")
	}
	if unboundClosed {
		t.Fatal("rejected inference released the caller's generationless State")
	}
}

type settlementAdmissionJournal struct {
	providers.InferenceJournal
	writes int
	final  bool
}

func (j *settlementAdmissionJournal) UpsertSubmission(ctx context.Context, record providers.InferenceSubmissionJournalRecord) error {
	if j.final {
		_, end, err := providers.BeginModelReceipt(ctx)
		if err != nil {
			return err
		}
		defer end(nil)
		held, err := RetainInvocationState(ctx)
		if err != nil {
			return err
		}
		defer held.Close()
	}
	j.writes++
	return j.InferenceJournal.UpsertSubmission(ctx, record)
}

func TestReceiptSettlementCheckpointsWithLiveAdmission(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule, "wuu.session.default"}
	state, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx, complete, err := state.BeginModelReceipts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	journal := &settlementAdmissionJournal{InferenceJournal: shutdownCheckpointJournal{state.InferenceJournalRuntime.ForOwner("settlement")}}
	request, err := providers.EnsureInferenceAttemptContext(providers.WithInferenceJournal(ctx, journal), providers.ChatRequest{Model: "fixture", Messages: []providers.ChatMessage{{Role: "user", Content: "fixture"}}}, providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Transport: "http", Mode: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	submission.CompleteFailure(providers.NormalizedFailure{Category: providers.FailureCanceled})
	if err := request.Attempt.Complete(providers.InferenceOutcomeCanceled, providers.NormalizedFailure{Category: providers.FailureCanceled}); err != nil {
		t.Fatal(err)
	}
	if err := request.Execution.Complete(providers.InferenceOutcomeCanceled, providers.NormalizedFailure{Category: providers.FailureCanceled}); err != nil {
		t.Fatal(err)
	}
	before := journal.writes
	journal.final = true
	if err := complete(); err != nil {
		t.Fatalf("final settlement failed: %v", err)
	}
	if journal.writes != before+1 {
		t.Fatalf("final submission checkpoint writes = %d, want %d", journal.writes, before+1)
	}
}

// ProcessClient admits external requests through BeginModelReceipt.
func TestReceiptSettlementRetainsAuthorityUntilExternalRequestDrains(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule, "wuu.session.default"}
	state, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx, complete, err := state.BeginModelReceipts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var owner *modelInvocationOwner
	state.Generation.modelMu.Lock()
	for candidate := range state.Generation.modelOwners {
		owner = candidate
	}
	state.Generation.modelMu.Unlock()
	owner.receipts.Seal()
	if _, _, err := providers.BeginModelReceipt(ctx); !errors.Is(err, providers.ErrModelReceiptScopeClosed) {
		t.Fatalf("sealed turn admitted work: %v", err)
	}
	settlementCtx, end, err := providers.BeginModelReceipt(owner.completionCtx)
	if err != nil {
		t.Fatalf("external settlement rejected before dispatch: %v", err)
	}
	defer end(nil)
	owner.abort(context.DeadlineExceeded)
	if err := complete(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("settlement timeout lost: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	held, err := RetainInvocationState(settlementCtx)
	if err != nil {
		t.Fatalf("pending settlement lost generation authority: %v", err)
	}
	_ = held.Close()
	end(nil)
	state.Generation.modelInvocations.Wait()
	if _, err := RetainInvocationState(settlementCtx); err == nil {
		t.Fatal("completed settlement retained generation authority")
	}
	if _, _, err := providers.BeginModelReceipt(owner.completionCtx); !errors.Is(err, providers.ErrModelReceiptScopeClosed) {
		t.Fatalf("completed settlement admitted work: %v", err)
	}
}

func TestInvocationStateRetainsOriginalAuthority(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule, "wuu.session.default"}
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
	modules := []string{sessionStateConsumer, persistence.JSONLModule, "example.consumer", "wuu.session.default"}
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
