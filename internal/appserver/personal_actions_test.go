package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// The production UserHost entry, real native file tools, disposable SQLite and
// fake model transport cover the security/recovery contract before wiring it.
// Failures: forged owner, default auto-off, stale file/symlink/task, replay,
// restart, unsafe path, reviewer outage, cancellation and Coding isolation.
func TestPersonalFileApprovalProductionEntry(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse(`{"outcome":"allow","reason":"exact requested file"}`)})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.WuuHome = t.TempDir()
	rt.RootDir = rt.UserBinding().Root(rt.WuuHome)
	rt.DataRoot = rt.UserBinding().State(rt.WuuHome)
	rt.SessionDir = filepath.Join(rt.DataRoot, "sessions")
	if err := os.MkdirAll(rt.RootDir, 0700); err != nil {
		t.Fatal(err)
	}
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "personal"}, SubjectID: "plugin:bundled:personal"}}
	rt.PluginHost = pluginhost.New(&pluginClientRequestTestRuntime{id: "personal"})
	var kitErr error
	rt.Toolkit, kitErr = tools.New(rt.RootDir)
	if kitErr != nil {
		t.Fatal(kitErr)
	}
	id := "personal-file-main"
	_, err := session.CreateInitialized(rt.SessionDir, session.Session{ID: id, UserSpaceID: "local", ProductDomain: "personal", Owner: "plugin:personal", Visibility: "plugin", AgentID: "main", CWD: rt.RootDir}, []session.HistoryRecord{{Role: "user", Origin: "user", Content: "Save my note to note.txt."}})
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewUserHost(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	owner, err := clientauthority.AdmitDesktop(context.Background(), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	kit, err := rt.Toolkit.CloneForRoot(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionID(id)
	call := providers.ToolCall{ID: "file-call", Name: "write_file", Arguments: `{"path":"note.txt","content":"saved once"}`}
	if _, err = kit.Execute(context.Background(), call); err == nil || !strings.Contains(err.Error(), "personal_action_pending") {
		t.Fatalf("must wait for manual owner: %v", err)
	}
	if _, err = os.Stat(filepath.Join(rt.RootDir, "note.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote before approval")
	}
	if _, err = host.personal.Call(context.Background(), "review.list", nil, ""); err == nil {
		t.Fatal("forged owner")
	}
	records, err := host.actions.engine.Records(context.Background(), "local")
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	r := records[0]
	command, _ := json.Marshal(map[string]any{"space_id": "local", "id": r.Action.ID, "digest": r.Action.Digest, "expected_revision": r.Revision, "choice": "approve_once"})
	if _, err = host.personal.Call(owner, "review.resolve", command, ""); err != nil {
		t.Fatal(err)
	}
	if err = host.actions.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(rt.RootDir, "note.txt"))
	if err != nil || string(data) != "saved once" {
		t.Fatalf("receipt did not continue original file action: %s %v", data, err)
	}
	r, err = host.actions.engine.GetExecution(context.Background(), r.Action.Key())
	if err != nil || r.Status != personalreview.Succeeded {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = host.personal.Call(owner, "review.resolve", command, ""); err != nil {
		t.Fatal("same decision must be idempotent", err)
	}
	if _, err = kit.Execute(context.Background(), call); err != nil {
		t.Fatal("completed exact replay should return receipt", err)
	}
	t.Log("real UserHost + Toolkit write_file + owner transport + durable engine; external account/network/model calls=0")
}

// Exercise the live worker suspension boundary: the original invocation must
// remain pending, then return its own successful native result/telemetry. Owner
// rejection and executor cancellation must not manufacture a successful result.
func TestPersonalWorkerApprovalKeepsOriginalInvocation(t *testing.T) {
	for _, outcome := range []string{"approve", "reject", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			f := newPersonalBoundaryFixture(t, nil)
			worker := "approval-worker"
			_, err := session.CreateInitialized(f.rt.SessionDir, session.Session{ID: worker, ParentID: f.id, AgentID: "main", UserSpaceID: f.rt.UserSpaceID, ProductDomain: "personal", Owner: "plugin:personal", Visibility: "plugin", CWD: f.rt.RootDir}, nil)
			if err != nil {
				t.Fatal(err)
			}
			prefix := "personal.v1." + personalHash([]byte(f.rt.UserSpaceID))[:28] + "."
			_, err = pluginsettings.UpdateState(f.rt.DataRoot, f.rt.RootDir, "plugin:bundled:personal", pluginsettings.ScopeUser, func(values map[string]string) error {
				agent, _ := json.Marshal(map[string]any{"agent_id": "main", "user_space_id": f.rt.UserSpaceID, "main_session_id": f.id, "status": "active"})
				task, _ := json.Marshal(map[string]any{"task_id": "approval-task", "agent_id": "main", "user_space_id": f.rt.UserSpaceID, "requirements_revision": 1, "state": "running", "goal": "Save requested note", "source": map[string]any{"session_id": f.id, "message_seq": f.sourceSeq, "message_binding": "host_turn"}, "execution": map[string]any{"session_id": worker, "request_id": "original-worker-turn", "requirements_revision": 1}})
				values[prefix+"agent.main"] = string(agent)
				values[prefix+"task.approval-task"] = string(task)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			kit, err := f.rt.Toolkit.CloneForRoot(f.rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			kit.SetSessionID(worker)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := kit.Execute(ctx, f.call()); done <- err }()
			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			var record personalreview.Record
		waitPending:
			for {
				select {
				case err := <-done:
					t.Fatalf("original invocation returned before decision: %v", err)
				case <-timer.C:
					t.Fatal("pending action not published")
				case <-tick.C:
					records, err := f.host.actions.engine.Records(context.Background(), f.rt.UserSpaceID)
					if err != nil {
						t.Fatal(err)
					}
					if len(records) == 1 && records[0].Status == personalreview.AwaitingConfirmation {
						record = records[0]
						break waitPending
					}
				}
			}
			if _, err := os.Stat(filepath.Join(f.rt.RootDir, "note.txt")); !os.IsNotExist(err) {
				t.Fatal("effect before approval")
			}
			// Publication and decision delivery are independent asynchronous phases.
			// Do not consume cancellation's deadline while publishing SQLite state.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(30 * time.Second)
			if outcome == "cancel" {
				cancel()
			} else {
				choice := "approve_once"
				if outcome == "reject" {
					choice = "reject"
				}
				command, _ := json.Marshal(map[string]any{"space_id": f.rt.UserSpaceID, "id": record.Action.ID, "digest": record.Action.Digest, "expected_revision": record.Revision, "choice": choice})
				if _, err = f.host.personal.Call(f.owner, "review.resolve", command, ""); err != nil {
					t.Fatal(err)
				}
				if err = f.host.actions.drain(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if (outcome == "approve") != (err == nil) {
					t.Fatalf("outcome=%s err=%v", outcome, err)
				}
			case <-timer.C:
				t.Fatal("original invocation did not resume")
			}
			records := kit.ToolTelemetry()
			if len(records) != 1 || records[0].CallID != f.call().ID || records[0].Success != (outcome == "approve") {
				t.Fatalf("original invocation evidence lost: %+v", records)
			}
			if outcome != "approve" {
				if _, err := os.Stat(filepath.Join(f.rt.RootDir, "note.txt")); !os.IsNotExist(err) {
					t.Fatal("effect after reject/cancel")
				}
			}
		})
	}
}

type personalReceiptServiceClient struct {
	pluginClientRequestTestRuntime
	calls int
}

func (c *personalReceiptServiceClient) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: "personal.agents", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "actions.sync", InputSchema: "personal.action.input.v1", OutputSchema: "personal.action.output.v1"}}}}
}
func (c *personalReceiptServiceClient) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (c *personalReceiptServiceClient) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	c.calls++
	return json.RawMessage(`{"accepted":true}`), nil
}

// Host recovery is a kernel service call, not an undeclared self-dependency.
// A different provider advertising the same service must never receive it.
func TestPersonalReceiptKernelRouteIsBoundToPersonalProvider(t *testing.T) {
	for _, providerID := range []string{"personal", "unrelated"} {
		t.Run(providerID, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			provider := &personalReceiptServiceClient{pluginClientRequestTestRuntime: pluginClientRequestTestRuntime{id: providerID}}
			clients := []pluginhost.Client{provider}
			if providerID != "personal" {
				clients = append(clients, &pluginClientRequestTestRuntime{id: "personal"})
			}
			registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
			if len(conflicts) != 0 {
				t.Fatal(conflicts)
			}
			registry.Activate()
			rt.PluginHost = pluginhost.New(clients...)
			rt.PluginHost.AttachServiceRegistry(registry, nil)
			p := &personalActions{server: &Server{rt: rt}}
			err := p.productAction(context.Background(), "actions.sync", personalreview.Record{})
			if providerID == "personal" {
				if err != nil || provider.calls != 1 {
					t.Fatalf("host kernel route failed: calls=%d err=%v", provider.calls, err)
				}
			} else if err == nil || provider.calls != 0 {
				t.Fatalf("foreign service reached: calls=%d err=%v", provider.calls, err)
			}
		})
	}
}
