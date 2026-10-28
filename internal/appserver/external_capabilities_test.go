package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type externalReceiptAdmission struct {
	ctx     context.Context
	request providers.ChatRequest
}

type externalReceiptClient struct {
	admitted chan externalReceiptAdmission
	release  chan struct{}
	done     chan struct{}
}

func (c *externalReceiptClient) Chat(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
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
		defer close(c.done)
		defer func() { end(request.Execution.JournalError()) }()
		<-c.release
		submission.CompleteSuccess(&providers.TokenUsage{InputTokens: 19, OutputTokens: 5})
	}()
	c.admitted <- externalReceiptAdmission{ctx, request}
	return providers.ChatResponse{Content: `{"ok":true}`}, nil
}

type externalReceiptDocuments struct {
	*persistence.Service
	fail        bool
	closed      chan struct{}
	once        sync.Once
	logicalDone chan struct{}
	logicalOnce sync.Once
}

func (p *externalReceiptDocuments) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	terminal := false
	if params.Method == "commit" {
		var tx persistence.Transaction
		if err := json.Unmarshal(params.Params, &tx); err != nil {
			return nil, err
		}
		for _, write := range tx.Writes {
			if write.Namespace == "wuu.inference_operations.v1" {
				var operation struct{ Status string }
				if err := json.Unmarshal(write.Value, &operation); err != nil {
					return nil, err
				}
				terminal = operation.Status != "active"
			}
			if !p.fail || write.Namespace != "wuu.inference_submissions.v1" {
				continue
			}
			var receipt providers.InferenceSubmissionJournalRecord
			if err := json.Unmarshal(write.Value, &receipt); err != nil {
				return nil, err
			}
			if receipt.ReportedUsage != nil {
				return nil, &pluginhost.HostServiceError{Code: "receipt_checkpoint_rejected", Message: "external receipt rejected"}
			}
		}
	}
	result, err := p.Service.InvokeService(ctx, params)
	if terminal && err == nil {
		p.logicalOnce.Do(func() { close(p.logicalDone) })
	}
	return result, err
}

func (p *externalReceiptDocuments) Close(ctx context.Context) error {
	err := p.Service.Close(ctx)
	p.once.Do(func() { close(p.closed) })
	return err
}

func newExternalReceiptFixture(t *testing.T, event hooks.Event, fail bool) (*runtime.State, *runtime.ThreadRuntime, *externalReceiptClient, *externalReceiptDocuments, string) {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	store := filepath.Join(root, "documents")
	provider := &externalReceiptDocuments{Service: persistence.NewService("test.external-documents", store, "jsonl"), fail: fail, closed: make(chan struct{}), logicalDone: make(chan struct{})}
	modules := []string{"wuu.session-state", provider.ID(), runtime.StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, runtime.ArtifactModule, runtime.DataQueryModule, "wuu.session.default"}
	binding, err := runtime.OpenState(context.Background(), runtime.StateOptions{RootDir: root, HomeDir: home, Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}, NativeProviders: []runtime.NativeProviderFactory{{ID: provider.ID(), Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) { return provider, nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close() })
	client := &externalReceiptClient{admitted: make(chan externalReceiptAdmission, 1), release: make(chan struct{}), done: make(chan struct{})}
	registry := hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{event: {{Type: "prompt", Prompt: "$ARGUMENTS"}}})
	registry.SetModelClient(hooks.NewProviderModelClient(client, "fixture", nil))
	dispatcher := hooks.NewDispatcher(registry)
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionID("external-receipts")
	kit.SetBoundary(tools.UnconfinedBoundary())
	kit.SetPermissionRequestHook(func(ctx context.Context, active *tools.Toolkit, _ tools.ToolInfo, call providers.ToolCall) error {
		_, err := dispatcher.Dispatch(ctx, hooks.PermissionRequest, &hooks.Input{SessionID: active.SessionID(), CWD: active.RootDir(), ToolName: call.Name, ToolInput: json.RawMessage(call.Arguments)})
		return err
	})
	if event != hooks.PostToolUseFailure {
		if err := os.WriteFile(filepath.Join(root, "value.txt"), []byte("capability completed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	thread := &runtime.ThreadRuntime{Toolkit: kit, CapabilityTools: hooks.NewHookedExecutor(kit, dispatcher, kit.SessionID(), kit.RootDir()), PluginGeneration: binding.Generation, Documents: binding.Documents, Sessions: binding.Sessions}
	return binding, thread, client, provider, store
}

type externalReceiptResponse struct {
	Result mcp.CallToolResult `json:"result"`
	Error  *mcp.RPCError      `json:"error"`
	err    error
}

func callExternalReceiptTool(bridge *externalCapabilities) <-chan externalReceiptResponse {
	returned := make(chan externalReceiptResponse, 1)
	go func() {
		client := http.Client{Timeout: 10 * time.Second}
		response, err := client.Post(bridge.Binding().URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":"receipt","method":"tools/call","params":{"name":"read_file","arguments":{"path":"value.txt"}}}`))
		var result externalReceiptResponse
		if err != nil {
			result.err = err
		} else {
			result.err = json.NewDecoder(response.Body).Decode(&result)
			_ = response.Body.Close()
		}
		returned <- result
	}()
	return returned
}

func TestExternalCapabilitiesFinishPromptReceiptsBeforeMCPResult(t *testing.T) {
	for _, event := range []hooks.Event{hooks.PreToolUse, hooks.PermissionRequest, hooks.PostToolUse, hooks.PostToolUseFailure} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail_%t", event, fail), func(t *testing.T) {
				binding, thread, client, provider, store := newExternalReceiptFixture(t, event, fail)
				bridge, err := startExternalCapabilities(context.Background(), binding, thread)
				if err != nil {
					t.Fatal(err)
				}
				defer bridge.Close()
				unblock := sync.OnceFunc(func() { close(client.release) })
				defer unblock()
				returned := callExternalReceiptTool(bridge)
				var admission externalReceiptAdmission
				select {
				case admission = <-client.admitted:
				case <-time.After(10 * time.Second):
					t.Fatal("MCP hook did not reach prompt model")
				}
				if providers.ModelReceiptScopeFromContext(admission.ctx) == nil || providers.InferenceJournalFromContext(admission.ctx) == nil {
					t.Error("external hook has no captured receipt owner/journal")
				}
				admitted := admission.request.Execution.Snapshot().Submissions
				if len(admitted) != 1 {
					t.Fatalf("physical admissions = %d", len(admitted))
				}
				var response externalReceiptResponse
				early := false
				// Release physical usage only after the logical operation is durable.
				// This distinguishes late receipt cleanup from ordinary Chat completion.
				select {
				case <-provider.logicalDone:
				case response = <-returned:
					early = true
					t.Error("MCP response returned before logical accounting and held physical receipts")
				case <-time.After(10 * time.Second):
					t.Fatal("hook logical accounting did not finish")
				}
				unblock()
				if !early {
					select {
					case response = <-returned:
					case <-time.After(10 * time.Second):
						t.Fatal("MCP tools/call did not finish")
					}
				}
				if response.err != nil || response.Error != nil {
					t.Fatalf("MCP transport failed: %+v", response)
				}
				var texts []string
				for _, content := range response.Result.Content {
					texts = append(texts, content.Text)
				}
				text := strings.Join(texts, "\n")
				if fail && (!response.Result.IsError || !strings.Contains(text, "external receipt rejected")) {
					t.Errorf("MCP response swallowed required receipt error: %+v", response.Result)
				}
				if !fail && event != hooks.PostToolUseFailure && response.Result.IsError {
					t.Errorf("successful capability reported failure: %+v", response.Result)
				}
				if event != hooks.PostToolUseFailure && !strings.Contains(text, "capability completed") {
					t.Errorf("receipt failure discarded completed tool content: %+v", response.Result)
				}
				select {
				case <-client.done:
				case <-time.After(10 * time.Second):
					t.Fatal("physical receipt did not finish")
				}
				if !fail {
					_, snapshot, err := persistence.Inspect(context.Background(), store)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, document := range snapshot.Documents {
						if document.Namespace != "wuu.inference_submissions.v1" {
							continue
						}
						var receipt providers.InferenceSubmissionJournalRecord
						if err := json.Unmarshal(document.Value, &receipt); err != nil {
							t.Fatal(err)
						}
						if receipt.ID == admitted[0].ID && receipt.OperationID == admission.request.Operation.ID && receipt.AttemptID == admission.request.Attempt.ID {
							found = receipt.ReportedUsage != nil && receipt.ReportedUsage.InputTokens == 19 && receipt.ReportedUsage.OutputTokens == 5
						}
					}
					if !found {
						t.Error("MCP result preceded durable late physical usage")
					}
				}
			})
		}
	}
}

func TestExternalCapabilitiesReuseNativeReceiptScope(t *testing.T) {
	binding, thread, client, _, _ := newExternalReceiptFixture(t, hooks.PostToolUse, false)
	ctx, finish, err := agent.BeginModelReceipts(context.Background(), binding.BeginModelReceipts)
	if err != nil {
		t.Fatal(err)
	}
	ctx = providers.WithInferenceJournal(ctx, binding.InferenceJournalRuntime.ForOwner(thread.Toolkit.SessionID()))
	bridge, err := startExternalCapabilities(ctx, binding, thread)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	unblock := sync.OnceFunc(func() { close(client.release) })
	defer unblock()
	returned := callExternalReceiptTool(bridge)
	var admission externalReceiptAdmission
	select {
	case admission = <-client.admitted:
	case <-time.After(10 * time.Second):
		t.Fatal("hook did not start")
	}
	if providers.ModelReceiptScopeFromContext(admission.ctx) != providers.ModelReceiptScopeFromContext(ctx) {
		t.Error("external call replaced the native turn receipt owner")
	}
	select {
	case response := <-returned:
		if response.err != nil || response.Error != nil || response.Result.IsError {
			t.Errorf("nested MCP result: %+v", response)
		}
	case <-time.After(time.Second):
		t.Error("external call waited on its live parent scope")
	}
	if _, end, err := providers.BeginModelReceipt(ctx); err != nil {
		t.Errorf("external call sealed its parent: %v", err)
	} else {
		end(nil)
	}
	unblock()
	if err := finish(); err != nil {
		t.Fatal(err)
	}
}

func TestExternalCapabilitiesCloseRetainsAdmittedReceipts(t *testing.T) {
	binding, thread, client, provider, _ := newExternalReceiptFixture(t, hooks.PostToolUse, false)
	bridge, err := startExternalCapabilities(context.Background(), binding, thread)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	unblock := sync.OnceFunc(func() { close(client.release) })
	defer unblock()
	returned := callExternalReceiptTool(bridge)
	select {
	case <-client.admitted:
	case <-time.After(10 * time.Second):
		t.Fatal("hook did not start")
	}
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { bridge.Close(); close(closed) }()
	<-bridge.ctx.Done()
	select {
	case <-provider.closed:
		t.Error("captured authority closed before physical receipt tail")
	default:
	}
	request := httptest.NewRequest(http.MethodPost, bridge.Binding().URL, strings.NewReader(`{"jsonrpc":"2.0","id":"late","method":"ping"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	bridge.ServeHTTP(response, request)
	if response.Code != http.StatusGone {
		t.Errorf("late request after revocation status = %d", response.Code)
	}
	unblock()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("bridge.Close did not join admitted cleanup")
	}
	select {
	case <-client.done:
	case <-time.After(10 * time.Second):
		t.Fatal("physical receipt did not end")
	}
	select {
	case <-provider.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("captured authority was not released")
	}
	select {
	case response := <-returned:
		if response.err != nil && errors.Is(response.err, context.DeadlineExceeded) {
			t.Error(response.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP call did not exit after Close")
	}
}
