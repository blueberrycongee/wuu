package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// Production-boundary acceptance uses the real UserHost, local native tools,
// durable stores and source-history resolver. Plugin admission, source messages
// and model transport are synthetic fixtures; business-plugin continuation is
// tested separately. No external account, paid model or installed app is used.
type personalBoundaryFixture struct {
	t           *testing.T
	rt          *runtime.Session
	host        *UserHost
	kit         *tools.Toolkit
	client      *fakeClient
	id          string
	owner       context.Context
	cancelOwner context.CancelFunc
	sourceSeq   int
}

func newPersonalBoundaryFixture(t *testing.T, client *fakeClient) *personalBoundaryFixture {
	t.Helper()
	if client == nil {
		client = &fakeClient{response: providersResponse(`{"outcome":"allow","reason":"synthetic exact requested note"}`)}
	}
	rt := newTestRuntime(t, client)
	rt.UserSpaceID, rt.ProductDomain = "boundary-space", "personal"
	rt.WuuHome = t.TempDir()
	rt.RootDir = rt.UserBinding().Root(rt.WuuHome)
	rt.DataRoot = rt.UserBinding().State(rt.WuuHome)
	rt.SessionDir = filepath.Join(rt.DataRoot, "sessions")
	rt.StreamRunner.ProviderName = rt.ProviderName
	if err := os.MkdirAll(rt.RootDir, 0700); err != nil {
		t.Fatal(err)
	}
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "personal"}, SubjectID: "plugin:bundled:personal"}}
	rt.PluginHost = pluginhost.New(&pluginClientRequestTestRuntime{id: "personal"})
	var err error
	rt.Toolkit, err = tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	f := &personalBoundaryFixture{t: t, rt: rt, client: client, id: "boundary-main"}
	_, err = session.CreateInitialized(rt.SessionDir, session.Session{ID: f.id, UserSpaceID: rt.UserSpaceID, ProductDomain: "personal", Owner: "plugin:personal", Visibility: "plugin", AgentID: "main", CWD: rt.RootDir}, []session.HistoryRecord{{Role: "user", Origin: "user", Content: "Save my requested note to note.txt with exactly the text saved once."}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := session.ReadHistoryPage(context.Background(), rt.SessionDir, f.id, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range page.Records {
		if record.Role == "user" && record.Origin == "user" {
			f.sourceSeq = record.Seq
		}
	}
	if f.sourceSeq == 0 {
		t.Fatal("source message missing")
	}
	f.openHost()
	t.Cleanup(func() {
		f.cancelOwner()
		if err := f.host.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *personalBoundaryFixture) openHost() {
	f.t.Helper()
	var err error
	f.host, err = NewUserHost(f.rt)
	if err != nil {
		f.t.Fatal(err)
	}
	f.kit, err = f.rt.Toolkit.CloneForRoot(f.rt.RootDir)
	if err != nil {
		f.t.Fatal(err)
	}
	f.kit.SetSessionID(f.id)
	f.reconnectOwner()
}
func (f *personalBoundaryFixture) reconnectOwner() {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.cancelOwner = cancel
	bootstrap, _ := json.Marshal(map[string]any{"version": 1, "user_space_id": f.rt.UserSpaceID, "product_domain": "personal"})
	owner, err := clientauthority.AdmitDesktop(ctx, f.rt.UserBinding(), strings.NewReader(string(bootstrap)))
	if err != nil {
		f.t.Fatal(err)
	}
	f.owner = clientauthority.BindConnection(owner)
}
func (f *personalBoundaryFixture) sourceContext() context.Context {
	return loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: f.id, ExecutionID: "synthetic-live-turn", SourceMessage: &providers.MessageRef{SessionID: f.id, Seq: f.sourceSeq}})
}
func (f *personalBoundaryFixture) call() providers.ToolCall {
	return providers.ToolCall{ID: "bounded-file-call", Name: "write_file", Arguments: `{"path":"note.txt","content":"saved once"}`}
}
func (f *personalBoundaryFixture) pending(ctx context.Context) personalreview.Record {
	f.t.Helper()
	if _, err := f.kit.Execute(ctx, f.call()); err == nil || !strings.Contains(err.Error(), "personal_action_pending") {
		f.t.Fatalf("expected pending action, got %v", err)
	}
	records, err := f.host.actions.engine.Records(context.Background(), f.rt.UserSpaceID)
	if err != nil || len(records) != 1 {
		f.t.Fatalf("records=%d err=%v", len(records), err)
	}
	return records[0]
}
func (f *personalBoundaryFixture) command(record personalreview.Record) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"space_id": record.Action.Scope.SpaceID, "id": record.Action.ID, "digest": record.Action.Digest, "expected_revision": record.Revision, "choice": "approve_once"})
	return raw
}
func (f *personalBoundaryFixture) snapshot() personalreview.OwnerView {
	f.t.Helper()
	raw, err := f.host.personal.Call(f.owner, "review.list", nil, "")
	if err != nil {
		f.t.Fatal(err)
	}
	var view personalreview.OwnerView
	if err := json.Unmarshal(raw, &view); err != nil {
		f.t.Fatal(err)
	}
	return view
}
func (f *personalBoundaryFixture) enableAuto() personalreview.OwnerView {
	f.t.Helper()
	before := f.snapshot()
	if before.Settings == nil {
		f.t.Fatal("missing explicit Personal settings")
	}
	raw, _ := json.Marshal(map[string]any{"enabled": true, "expected_revision": before.Settings.Revision})
	response, err := f.host.personal.Call(f.owner, "review.settings", raw, "")
	if err != nil {
		f.t.Fatal(err)
	}
	var after personalreview.OwnerView
	if err := json.Unmarshal(response, &after); err != nil {
		f.t.Fatal(err)
	}
	if after.Settings == nil || !after.Settings.AutoReviewEnabled {
		f.t.Fatal("opt-in not persisted")
	}
	if err := f.host.actions.drain(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	return after
}
func (f *personalBoundaryFixture) current(key personalreview.Key) personalreview.Record {
	f.t.Helper()
	r, err := f.host.actions.engine.GetExecution(context.Background(), key)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}
func (f *personalBoundaryFixture) assertContents(path, want string) {
	f.t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		f.t.Fatalf("file %s=%q err=%v want=%q", path, got, err, want)
	}
}

func TestPersonalActionOwnerReconnectConcurrentClickAndReplay(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	r := f.pending(context.Background())
	command := f.command(r)
	oldOwner := f.owner
	f.cancelOwner()
	f.reconnectOwner()
	if _, err := f.host.personal.Call(context.WithoutCancel(oldOwner), "review.resolve", command, ""); err == nil {
		t.Fatal("disconnected transport retained owner authority")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.host.personal.Call(f.owner, "review.resolve", command, ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := f.host.actions.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.current(r.Action.Key()); got.Status != personalreview.Succeeded {
		t.Fatalf("reconnected owner failed exact continuation: %+v", got)
	}
	path := filepath.Join(f.rt.RootDir, "note.txt")
	f.assertContents(path, "saved once")
	events, err := f.host.actions.engine.Audit(f.owner, r.Action.Key())
	if err != nil {
		t.Fatal(err)
	}
	claims := 0
	for _, event := range events {
		if event.Status == personalreview.Executing {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("concurrent clicks produced %d dispatch claims", claims)
	}
	if err := os.WriteFile(path, []byte("later user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.personal.Call(f.owner, "review.resolve", command, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.kit.Execute(context.Background(), f.call()); err != nil {
		t.Fatal(err)
	}
	f.assertContents(path, "later user edit")
}

func TestPersonalActionChangedContentAndSymlinkCannotUseOldApproval(t *testing.T) {
	for _, change := range []string{"content", "symlink"} {
		t.Run(change, func(t *testing.T) {
			if change == "symlink" && goruntime.GOOS == "windows" {
				t.Skip("symlink permission is platform-specific")
			}
			f := newPersonalBoundaryFixture(t, nil)
			path := filepath.Join(f.rt.RootDir, "note.txt")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			r := f.pending(context.Background())
			target := path
			if change == "content" {
				if err := os.WriteFile(path, []byte("intervening user edit"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				target = filepath.Join(t.TempDir(), "outside.txt")
				if err := os.WriteFile(target, []byte("intervening user edit"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			_, _ = f.host.personal.Call(f.owner, "review.resolve", f.command(r), "")
			if err := f.host.actions.drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.assertContents(target, "intervening user edit")
			if got := f.current(r.Action.Key()); got.Status != personalreview.Denied && got.Status != personalreview.Stale {
				t.Fatalf("changed resource must terminate the old action: %+v", got)
			}
		})
	}
}

func TestPersonalAutoReviewSettingsAreExplicitScopedAndDoNotChangeCoding(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	codingBinding := userspace.Binding{UserSpaceID: f.rt.UserSpaceID, ProductDomain: "coding"}
	codingRoot := codingBinding.Root(f.rt.WuuHome)
	if err := os.MkdirAll(codingRoot, 0700); err != nil {
		t.Fatal(err)
	}
	codingSessionDir := filepath.Join(codingBinding.State(f.rt.WuuHome), "sessions")
	_, err := session.CreateInitialized(codingSessionDir, session.Session{ID: "coding-conversation", UserSpaceID: f.rt.UserSpaceID, ProductDomain: "coding", CWD: codingRoot, Provider: "coding-provider", Model: "coding-model", ApproveForMe: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	codingKit, err := tools.New(codingRoot)
	if err != nil {
		t.Fatal(err)
	}
	codingKit.SetApproveForMe(true)
	codingCall := providers.ToolCall{Name: "write_file", Arguments: `{"path":"coding.txt","content":"ordinary Coding path"}`}
	_, codingBefore := codingKit.Execute(context.Background(), codingCall)
	if codingBefore == nil || !strings.Contains(codingBefore.Error(), "review_failed") {
		t.Fatalf("Coding fixture did not retain its review requirement: %v", codingBefore)
	}
	before := f.snapshot()
	if before.Settings == nil || before.Settings.AutoReviewEnabled {
		t.Fatal("default was not manual")
	}
	after := f.enableAuto()
	if after.Settings.Revision != before.Settings.Revision+1 {
		t.Fatal("opt-in revision did not advance")
	}
	stale, _ := json.Marshal(map[string]any{"enabled": false, "expected_revision": before.Settings.Revision})
	if _, err := f.host.personal.Call(f.owner, "review.settings", stale, ""); !errors.Is(err, personalreview.ErrConflict) {
		t.Fatalf("stale settings accepted: %v", err)
	}
	if !codingKit.ApproveForMe() {
		t.Fatal("Personal opt-in changed Coding switch")
	}
	codingSession, found, err := session.Find(codingSessionDir, "coding-conversation")
	if err != nil || !found || !codingSession.ApproveForMe || codingSession.Provider != "coding-provider" || codingSession.Model != "coding-model" {
		t.Fatalf("Personal settings changed persisted Coding selection: %+v %v", codingSession, err)
	}
	_, codingAfter := codingKit.Execute(context.Background(), codingCall)
	if codingAfter == nil || codingAfter.Error() != codingBefore.Error() {
		t.Fatalf("Personal opt-in changed Coding review behavior: before=%v after=%v", codingBefore, codingAfter)
	}
	codingDefault, err := tools.New(codingRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codingDefault.Execute(context.Background(), codingCall); err != nil {
		t.Fatalf("Personal gate leaked into an unchanged default Coding toolkit: %v", err)
	}
	f.assertContents(filepath.Join(codingRoot, "coding.txt"), "ordinary Coding path")
	other, err := f.host.actions.store.GetSettings(context.Background(), "other-space")
	if err != nil || other.AutoReviewEnabled {
		t.Fatalf("Personal opt-in crossed spaces: %+v %v", other, err)
	}
	events, err := f.host.actions.store.SettingsEvents(context.Background(), f.rt.UserSpaceID)
	if err != nil || len(events) != 1 || events[0].Actor != f.host.actions.ownerID() {
		t.Fatalf("missing stable owner audit: %+v %v", events, err)
	}
}

func TestPersonalAutoReviewProductionEntryUsesTrustedSourceAndNativeModelRequest(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	f.enableAuto()
	if _, err := f.kit.Execute(f.sourceContext(), f.call()); err != nil {
		t.Fatalf("authorized scoped auto review did not execute: %v", err)
	}
	f.assertContents(filepath.Join(f.rt.RootDir, "note.txt"), "saved once")
	f.client.mu.Lock()
	requests := append([]providers.ChatRequest(nil), f.client.requests...)
	f.client.mu.Unlock()
	if len(requests) < 2 {
		t.Fatalf("assessment and execution require fresh native review, got %d requests", len(requests))
	}
	for _, request := range requests {
		if request.Provider != f.rt.ProviderName || request.Model != f.rt.Model || len(request.Tools) != 0 || len(request.Messages) != 2 {
			t.Fatalf("wrong native model boundary: provider=%s model=%s tools=%d messages=%d", request.Provider, request.Model, len(request.Tools), len(request.Messages))
		}
		var payload struct {
			UserMessages []string          `json:"user_messages"`
			Conversation []json.RawMessage `json:"conversation"`
			Arguments    string            `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.UserMessages) != 1 || !strings.Contains(payload.UserMessages[0], "Save my requested note") || len(payload.Conversation) != 0 || !strings.Contains(payload.Arguments, "note.txt") {
			t.Fatalf("host provenance lost: %+v", payload)
		}
	}
	t.Log("production UserHost native file gate -> host source resolver -> real modelrequest -> fake model transport -> real isolated file; no live provider/account")
}

func TestPersonalAutoReviewMissingSourceAndOutageFallBackToManual(t *testing.T) {
	for _, scenario := range []string{"missing-source", "model-outage"} {
		t.Run(scenario, func(t *testing.T) {
			client := &fakeClient{response: providersResponse(`{"outcome":"allow","reason":"synthetic"}`)}
			if scenario == "model-outage" {
				client.err = errors.New("synthetic model unavailable")
			}
			f := newPersonalBoundaryFixture(t, client)
			f.enableAuto()
			ctx := context.Background()
			if scenario == "model-outage" {
				ctx = f.sourceContext()
			}
			r := f.pending(ctx)
			if r.Status != personalreview.AwaitingConfirmation {
				t.Fatalf("manual fallback unavailable: %+v", r)
			}
			if _, err := os.Stat(filepath.Join(f.rt.RootDir, "note.txt")); !os.IsNotExist(err) {
				t.Fatal("fallback already wrote the target")
			}
			client.mu.Lock()
			calls := len(client.requests)
			client.mu.Unlock()
			if scenario == "missing-source" && calls != 0 || scenario == "model-outage" && calls == 0 {
				t.Fatalf("unexpected model calls for %s: %d", scenario, calls)
			}
			if _, err := f.host.personal.Call(f.owner, "review.resolve", f.command(r), ""); err != nil {
				t.Fatal(err)
			}
			if err := f.host.actions.drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.assertContents(filepath.Join(f.rt.RootDir, "note.txt"), "saved once")
		})
	}
}

func TestPersonalPendingActionSurvivesHostRestartAndOriginalOwnerReconnect(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	r := f.pending(context.Background())
	command := f.command(r)
	f.cancelOwner()
	if err := f.host.Close(); err != nil {
		t.Fatal(err)
	}
	// Reuse only the persisted host runtime binding; a new UserHost creates a
	// new coordinator, engine and toolkit dispatch cache from the durable ledger.
	f.openHost()
	view := f.snapshot()
	if len(view.Requests) != 1 || view.Requests[0].Status != personalreview.AwaitingConfirmation || view.Requests[0].Action.Digest != r.Action.Digest {
		t.Fatalf("pending approval lost at restart: %+v", view)
	}
	if _, err := f.host.personal.Call(f.owner, "review.resolve", command, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.host.actions.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertContents(filepath.Join(f.rt.RootDir, "note.txt"), "saved once")
	if got := f.current(r.Action.Key()); got.Status != personalreview.Succeeded {
		t.Fatalf("restart did not continue exact original action: %+v", got)
	}
}

func TestPersonalEditFileApprovalDispatchesFrozenEditOnce(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	path := filepath.Join(f.rt.RootDir, "note.txt")
	if err := os.WriteFile(path, []byte("original section\nunchanged section\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.kit.Execute(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"note.txt"}`}); err != nil {
		t.Fatal(err)
	}
	call := providers.ToolCall{ID: "edit-call", Name: "edit_file", Arguments: `{"path":"note.txt","edits":[{"old_text":"original section","new_text":"approved section"}]}`}
	if _, err := f.kit.Execute(context.Background(), call); err == nil || !strings.Contains(err.Error(), "personal_action_pending") {
		t.Fatalf("edit bypassed review: %v", err)
	}
	records, err := f.host.actions.engine.Records(context.Background(), f.rt.UserSpaceID)
	if err != nil || len(records) != 1 || records[0].Action.Operation != "edit_file" {
		t.Fatalf("missing exact edit proposal: %+v %v", records, err)
	}
	f.assertContents(path, "original section\nunchanged section\n")
	if _, err := f.host.personal.Call(f.owner, "review.resolve", f.command(records[0]), ""); err != nil {
		t.Fatal(err)
	}
	if err := f.host.actions.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertContents(path, "approved section\nunchanged section\n")
	if _, err := f.kit.Execute(context.Background(), call); err != nil {
		t.Fatal("exact completed edit should return original receipt", err)
	}
	f.assertContents(path, "approved section\nunchanged section\n")
}

func TestPersonalApprovedActionCannotDispatchAfterProductDisabled(t *testing.T) {
	for _, mode := range []string{"safe-mode", "plugin-disabled"} {
		t.Run(mode, func(t *testing.T) {
			f := newPersonalBoundaryFixture(t, nil)
			r := f.pending(context.Background())
			// Hold the coordinator's dispatch drain while the real owner endpoint
			// persists approval; then model a catalog transition before dispatch.
			f.host.actions.drainMu.Lock()
			_, err := f.host.personal.Call(f.owner, "review.resolve", f.command(r), "")
			if err != nil {
				f.host.actions.drainMu.Unlock()
				t.Fatal(err)
			}
			if got := f.current(r.Action.Key()); got.Status != personalreview.Approved {
				f.host.actions.drainMu.Unlock()
				t.Fatalf("approval was not durably queued: %+v", got)
			}
			plugins := f.rt.ActivePlugins
			if mode == "safe-mode" {
				f.rt.SafeMode = true
			} else {
				f.rt.ActivePlugins = nil
			}
			f.host.actions.drainMu.Unlock()
			if err := f.host.actions.drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(f.rt.RootDir, "note.txt")); !os.IsNotExist(err) {
				t.Fatal("disabled product performed approved file effect")
			}
			got := f.current(r.Action.Key())
			if got.Status != personalreview.Denied && got.Status != personalreview.Stale {
				t.Fatalf("disabled product did not invalidate action: %+v", got)
			}
			f.host.actions.drainMu.Lock()
			f.rt.SafeMode = false
			f.rt.ActivePlugins = plugins
			f.host.actions.drainMu.Unlock()
			_, _ = f.host.personal.Call(f.owner, "review.resolve", f.command(r), "")
			if err := f.host.actions.drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(f.rt.RootDir, "note.txt")); !os.IsNotExist(err) {
				t.Fatal("reenabling product revived the old approved action")
			}
		})
	}
}
