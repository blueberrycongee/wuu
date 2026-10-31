package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalhost"
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
	rebindPersonalTestDocuments(t, rt)
	if err := os.MkdirAll(rt.RootDir, 0700); err != nil {
		t.Fatal(err)
	}
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "personal"}, SubjectID: "plugin:bundled:personal"}}
	attachPersonalBoundaryGeneration(t, rt)
	var kitErr error
	rt.Toolkit, kitErr = tools.New(rt.RootDir)
	if kitErr != nil {
		t.Fatal(kitErr)
	}
	id := "personal-file-main"
	_, err := rt.Sessions.CreateInitialized(session.Session{ID: id, UserSpaceID: "local", ProductDomain: "personal", Owner: "plugin:personal", Visibility: "plugin", AgentID: "main", CWD: rt.RootDir}, []session.HistoryRecord{{Role: "user", Origin: "user", Content: "Save my note to note.txt."}})
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewUserHost(rt, personalhost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	owner, err := clientauthority.AdmitDesktop(personalTestOwner(context.Background()), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	kit, err := rt.Toolkit.CloneForRoot(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionID(id)
	call := providers.ToolCall{ID: "file-call", Name: "write_file", Arguments: `{"path":"note.txt","content":"saved once"}`}
	if _, err = kit.Execute(owner, call); err == nil || !strings.Contains(err.Error(), "personal_action_pending") {
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
	if _, err = kit.Execute(owner, call); err != nil {
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
			_, err := f.rt.Sessions.CreateInitialized(session.Session{ID: worker, ParentID: f.id, AgentID: "main", UserSpaceID: f.rt.UserSpaceID, ProductDomain: "personal", Owner: "plugin:personal", Visibility: "plugin", CWD: f.rt.RootDir}, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = pluginsettings.UpdateState(f.rt.DataRoot, f.rt.RootDir, "plugin:bundled:personal", pluginsettings.ScopeUser, func(values map[string]string) error {
				facts := personalExecutionFacts{SessionID: worker, AgentID: "main", MainSessionID: f.id, Active: true, Task: &personalTaskFacts{ID: "approval-task", Revision: 1, RunID: "original-worker-turn", Active: true, Goal: "Save requested note", SourceSessionID: f.id, SourceMessageSeq: f.sourceSeq}}
				raw, _ := json.Marshal(facts)
				values["fixture.facts."+worker] = string(raw)
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
			ctx, cancel := context.WithCancel(f.owner)
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
	calls atomic.Int32
	facts func(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error)
}

func (c *personalReceiptServiceClient) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: "personal.agents", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "observe-execution", InputSchema: "personal.execution-facts.input.v1", OutputSchema: "personal.execution-facts.output.v1"}, {Name: "legacy-memory", InputSchema: "personal.legacy-memory.input.v1", OutputSchema: "personal.legacy-memory.output.v1"}, {Name: "actions.sync", InputSchema: "personal.action.input.v1", OutputSchema: "personal.action.output.v1"}, {Name: "actions.resume", InputSchema: "personal.action.input.v1", OutputSchema: "personal.action.output.v1"}}}}
}
func (c *personalReceiptServiceClient) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (c *personalReceiptServiceClient) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method == "observe-execution" || params.Method == "legacy-memory" {
		return c.facts(ctx, params)
	}
	c.calls.Add(1)
	return json.RawMessage(`{"accepted":true}`), nil
}

// Recovery follows the selected service, while the receipt's durable session
// ownership prevents retargeting old actions to a different provider.
func TestPersonalReceiptRouteUsesSelectedProviderAndRetainsOwnerBinding(t *testing.T) {
	for _, providerID := range []string{"personal", "replacement"} {
		t.Run(providerID, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			provider := &personalReceiptServiceClient{pluginClientRequestTestRuntime: pluginClientRequestTestRuntime{id: providerID}}
			attachTestModelGeneration(t, rt, provider, NewPersonalActionsConsumer())
			p := &personalActions{server: newOwnerHandlerFixture(rt, &lockedBuffer{})}
			for _, owner := range []string{providerID, "other-owner"} {
				metadata, err := rt.Sessions.CreateInitialized(session.Session{ID: "receipt-" + owner, Owner: "plugin:" + owner, UserSpaceID: "receipt-space", ProductDomain: "personal", Visibility: "plugin"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				record := personalreview.Record{Action: personalreview.Action{Proposal: personalreview.Proposal{Scope: personalreview.Scope{SessionID: metadata.ID, SpaceID: metadata.UserSpaceID}}}}
				before := provider.calls.Load()
				err = p.productAction(personalTestOwner(context.Background()), "actions.sync", record)
				if owner == providerID {
					if err != nil || provider.calls.Load() != before+1 {
						t.Fatalf("selected provider was not called: calls=%d err=%v", provider.calls.Load(), err)
					}
				} else if err == nil || provider.calls.Load() != before {
					t.Fatalf("foreign receipt reached selected provider: calls=%d err=%v", provider.calls.Load(), err)
				}
			}
		})
	}
}
