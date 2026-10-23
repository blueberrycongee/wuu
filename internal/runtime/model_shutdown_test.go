package runtime

import (
	"bytes"
	"context"
	"encoding/json"
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
	host   *pluginhost.Host
	method string
}

func (c *shutdownModelCaller) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	service, method := pluginhost.KernelModelInvokeService, c.method
	input := json.RawMessage(`{"request":{"version":1,"provider":"fixture","model":"gpt-test","messages":[{"role":"user","content":"hold"}]}}`)
	if method == "request" {
		service, method = pluginhost.KernelModelRequestService, "call"
		input = json.RawMessage(`{"messages":[{"role":"user","content":"hold"}]}`)
	}
	result, err := c.host.ServiceRegistry().Call(ctx, c.ID(), pluginhost.ServiceCallParams{Service: service, Method: method, Params: input, ExecutionID: params.ExecutionID})
	if err != nil {
		return nil, err
	}
	if method == "start" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return result, nil
}

type shutdownModelDocuments struct {
	*persistence.Service
	barrierURL string
}

func (p *shutdownModelDocuments) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method == "commit" {
		var tx persistence.Transaction
		if err := json.Unmarshal(params.Params, &tx); err != nil {
			return nil, err
		}
		for _, write := range tx.Writes {
			if write.Namespace != "wuu.inference_attempts.v1" {
				continue
			}
			var attempt struct{ Phase string }
			if err := json.Unmarshal(write.Value, &attempt); err != nil {
				return nil, err
			}
			if attempt.Phase == "terminal" {
				response, err := http.Get(p.barrierURL)
				if err != nil {
					return nil, err
				}
				response.Body.Close()
				break
			}
		}
	}
	return p.Service.InvokeService(ctx, params)
}

// EOF must run cancellation and terminal persistence before the owning process
// exits. A live HTTP connection disappearing is not evidence of that drain.
func TestModelShutdownPersistsBeforeProcessExit(t *testing.T) {
	if root := os.Getenv("WUU_MODEL_SHUTDOWN_HELPER"); root != "" {
		modelShutdownProcess(t, root)
		os.Exit(0)
	}
	for _, method := range []string{"request", "call", "start", "retired-start"} {
		t.Run(method, func(t *testing.T) {
			root := t.TempDir()
			started, terminal := make(chan struct{}, 1), make(chan struct{}, 1)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/terminal" {
					terminal <- struct{}{}
					<-release
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				started <- struct{}{}
				<-r.Context().Done()
			}))
			defer server.Close()
			defer unblock()
			child := exec.Command(os.Args[0], "-test.run=^TestModelShutdownPersistsBeforeProcessExit$")
			child.Env = append(os.Environ(), "WUU_MODEL_SHUTDOWN_HELPER="+root, "WUU_MODEL_SHUTDOWN_URL="+server.URL, "WUU_MODEL_SHUTDOWN_METHOD="+method)
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
			unblock()
			select {
			case err := <-exited:
				if err != nil {
					t.Fatalf("cleanup failed: %v\n%s", err, output.String())
				}
			case <-time.After(15 * time.Second):
				t.Fatal("EOF did not finish after terminal persistence resumed")
			}
			_, snapshot, err := persistence.Inspect(context.Background(), filepath.Join(root, "documents"))
			if err != nil {
				t.Fatal(err)
			}
			operations, workflows, submissions := 0, 0, 0
			for _, document := range snapshot.Documents {
				switch document.Namespace {
				case "wuu.inference_operations.v1", "wuu.inference_workflows.v1":
					var record struct{ Status string }
					if err := json.Unmarshal(document.Value, &record); err != nil {
						t.Fatal(err)
					}
					if record.Status != "canceled" {
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
					if receipt.Outcome != providers.InferenceSubmissionFailed || receipt.FailureCategory != providers.FailureCanceled || receipt.CompletedAt.IsZero() || receipt.ReportedUsage != nil || (receipt.CostState != providers.InferenceCostEstimated && receipt.CostState != providers.InferenceCostUnknownBillable) {
						t.Errorf("EOF lost physical outcome or invented usage: %+v", receipt)
					}
					submissions++
				}
			}
			if operations != 1 || workflows != 1 || submissions != 1 {
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
	provider := &shutdownModelDocuments{Service: persistence.NewService("test.shutdown-documents", filepath.Join(root, "documents"), "jsonl"), barrierURL: os.Getenv("WUU_MODEL_SHUTDOWN_URL") + "/terminal"}
	caller := &shutdownModelCaller{fakeServicePluginClient: fakeServicePluginClient{id: "model-client", required: []pluginhost.ServiceRequirement{
		{Name: pluginhost.KernelModelInvokeService, MajorVersion: 1, Required: true},
		{Name: pluginhost.KernelModelRequestService, MajorVersion: 1, Required: true},
	}, provided: []pluginhost.ServiceDescriptor{{Name: "test.shutdown-model", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "run", InputSchema: "test.input", OutputSchema: "test.output"}}}}}, method: strings.TrimPrefix(os.Getenv("WUU_MODEL_SHUTDOWN_METHOD"), "retired-")}
	modules := testStateModules()
	modules[1] = provider.ID()
	modules = append(modules, kernelHostModule, caller.ID())
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
		&composition.Config{Version: composition.Version, Modules: &modules}, "",
		NativeProviderFactory{ID: provider.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return provider, nil }},
		NativeProviderFactory{ID: caller.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return caller, nil }})
	if err != nil {
		t.Fatal(err)
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
	if _, err := host.StartBackground(ctx, caller.ID(), pluginhost.BackgroundStartParams{Key: "shutdown", Service: "test.shutdown-model", Method: "run"}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(os.Getenv("WUU_MODEL_SHUTDOWN_METHOD"), "retired-") {
		host.StopBackground()
		owner.retiredPluginGenerations = map[*PluginGeneration]struct{}{generation: {}}
		owner.pluginGeneration, owner.PluginHost = nil, nil
		generation.Release()
	}
	if _, err := owner.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := captured.Sessions.Create("independently retained state"); err != nil {
		t.Fatal(err)
	}
	if err := captured.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "cleanup returned")
}
