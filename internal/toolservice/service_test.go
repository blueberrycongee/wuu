package toolservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestNativeServiceUsesBoundToolkitAndLifecycle(t *testing.T) {
	ctx := context.Background()
	service := NewService()
	invoke := func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	}
	client := NewClient(invoke)
	if _, err := client.List(ctx); err == nil {
		t.Fatal("prepared provider accepted invocation")
	}
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.List(ctx); err == nil {
		t.Fatal("missing toolkit accepted")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("selected-native-result"), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithScope(ctx, NewScope(func(context.Context) (Backend, error) { return Backend{Executor: kit, Toolkit: kit}, nil }))
	view, err := client.List(ctx)
	if err != nil || len(view.Tools) == 0 {
		t.Fatalf("list: %+v %v", view, err)
	}
	call := providers.ToolCall{Name: "read_file", Arguments: `{"path":"sample.txt"}`}
	resolved, err := client.Resolve(ctx, call)
	if err != nil || !resolved.Found {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	result, err := client.ExecuteResult(ctx, call)
	if err != nil || result.TextProjection() == "" {
		t.Fatalf("execute: %+v %v", result, err)
	}
	kit.DisableTools("read_file")
	if _, err := client.ExecuteResult(ctx, call); err == nil {
		t.Fatal("native policy bypassed")
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.List(ctx); err == nil {
		t.Fatal("closed provider accepted invocation")
	}
}

func TestClientDoesNotFallbackOrRetry(t *testing.T) {
	sentinel := errors.New("selected provider unavailable")
	count := 0
	client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { count++; return nil, sentinel })
	if _, err := client.List(context.Background()); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := client.Resolve(context.Background(), providers.ToolCall{Name: "read_file"}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := client.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file"}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("unexpected retries: %d", count)
	}
}

func TestClientRejectsIncompleteEnvelopesAndCancellation(t *testing.T) {
	client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})
	if _, err := client.List(context.Background()); err == nil {
		t.Fatal("missing tools accepted")
	}
	if _, err := client.Resolve(context.Background(), providers.ToolCall{Name: "x"}); err == nil {
		t.Fatal("missing found accepted")
	}
	if _, err := client.ExecuteResult(context.Background(), providers.ToolCall{Name: "x"}); err == nil {
		t.Fatal("missing result accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client = NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Fatal("called cancelled provider")
		return nil, nil
	})
	if _, err := client.ExecuteResult(ctx, providers.ToolCall{Name: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeProviderOwnsLazyScopeAndCleanup(t *testing.T) {
	created, closed := 0, 0
	scope := NewScope(func(context.Context) (Backend, error) {
		created++
		kit, err := tools.New(t.TempDir())
		return Backend{Executor: kit, Toolkit: kit, Close: func(context.Context) error { closed++; return nil }}, err
	})
	ctx := WithScope(context.Background(), scope)
	if created != 0 {
		t.Fatal("scope eagerly initialized")
	}
	external := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"tools":[],"catalog":[],"catalog_prompt":"","system_fragment":""}`), nil
	})
	if _, err := external.List(ctx); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatal("external provider initialized native backend")
	}
	native := NewService()
	if err := native.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return native.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
	if _, err := client.List(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.List(ctx); err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("factory called %d times", created)
	}
	if err := native.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := native.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("cleanup called %d times", closed)
	}
}

func TestScopeInitializationDoesNotHoldProviderLock(t *testing.T) {
	native := NewService()
	if err := native.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	scope := NewScope(func(context.Context) (Backend, error) {
		if native.Status().State != pluginhost.StateActive {
			t.Fatal("provider not active during factory")
		}
		kit, err := tools.New(t.TempDir())
		return Backend{Executor: kit, Toolkit: kit}, err
	})
	ctx := WithScope(context.Background(), scope)
	if _, err := native.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "list", Params: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := native.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// A timed-out graph drain cancels executions but still calls provider.Close.
// A native factory may return later, so shutdown must retain cleanup ownership.
func TestCloseCancellationRetainsLateBackendCleanup(t *testing.T) {
	native := NewService()
	if err := native.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	cleaned := make(chan error, 1)
	var count atomic.Int32
	executor := &shutdownProbe{}
	scope := NewScope(func(context.Context) (Backend, error) {
		close(started)
		<-release
		return Backend{Executor: executor, Close: func(ctx context.Context) error {
			count.Add(1)
			cleaned <- ctx.Err()
			return nil
		}}, nil
	})
	invoked := make(chan error, 1)
	go func() {
		_, err := native.InvokeService(WithScope(context.Background(), scope), pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "list", Params: json.RawMessage(`{}`)})
		invoked <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	closed := make(chan error, 1)
	go func() { closed <- native.Close(ctx) }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled close blocked on factory")
	}
	// Let the late factory settle while Close is called concurrently again.
	go func() { closed <- native.Close(context.Background()) }()
	release <- struct{}{}
	select {
	case err := <-cleaned:
		if err != nil {
			t.Fatalf("late cleanup got cancelled context: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("late backend leaked")
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := <-invoked; err == nil {
		t.Fatal("late backend dispatched after stop")
	}
	if executor.calls.Load() != 0 {
		t.Fatal("closed backend used for tool listing")
	}
	if err := native.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatalf("cleanup count: %d", count.Load())
	}
}

type shutdownProbe struct{ calls atomic.Int32 }

func (p *shutdownProbe) Definitions() []providers.ToolDefinition { p.calls.Add(1); return nil }
func (p *shutdownProbe) Execute(context.Context, providers.ToolCall) (string, error) {
	p.calls.Add(1)
	return "", nil
}

func TestCloseRetainsCleanupFailureAcrossRepeatedCalls(t *testing.T) {
	native := NewService()
	if err := native.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	scope := NewScope(func(context.Context) (Backend, error) {
		return Backend{Executor: &shutdownProbe{}, Close: func(context.Context) error { panic("cleanup failure") }}, nil
	})
	if _, err := native.InvokeService(WithScope(context.Background(), scope), pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "list", Params: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	first := native.Close(context.Background())
	if first == nil {
		t.Fatal("cleanup panic was lost")
	}
	if second := native.Close(context.Background()); second == nil || second.Error() != first.Error() {
		t.Fatalf("cleanup outcome changed: %v / %v", first, second)
	}
}

func TestPreparePreservesNativeSkillAdmission(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSkillsWithAvailability([]skills.Skill{{Name: "original", Description: "original skill"}}, nil)
	kit.SetSkillRefresh(func(context.Context, bool) ([]skills.Skill, error) {
		return []skills.Skill{{Name: "replacement", Description: "replacement skill"}}, nil
	})
	native := NewService()
	ctx := context.Background()
	if err := native.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer native.Close(ctx)
	scope := NewScope(func(context.Context) (Backend, error) { return Backend{Executor: kit, Toolkit: kit}, nil })
	ctx = WithScope(ctx, scope)
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return native.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
	messages, err := client.Prepare(ctx, nil)
	if err != nil || len(messages) != 1 {
		t.Fatalf("prepare: %+v %v", messages, err)
	}
	if !messages[0].Hidden || !messages[0].ReadOnly || messages[0].Origin != "runtime" || messages[0].Cause != "skill_catalog" {
		t.Fatalf("lost admission metadata: %+v", messages[0])
	}
	repeated, err := client.Prepare(ctx, messages)
	if err != nil || len(repeated) != 0 {
		t.Fatalf("history did not suppress duplicate: %+v %v", repeated, err)
	}
	emptyScope := NewScope(func(context.Context) (Backend, error) { return Backend{Executor: &shutdownProbe{}}, nil })
	if messages, err := client.Prepare(WithScope(context.Background(), emptyScope), nil); err != nil || messages == nil || len(messages) != 0 {
		t.Fatalf("optional preparer must return explicit empty messages: %+v %v", messages, err)
	}
}

func TestPrepareUsesSelectedProviderAndRejectsIncompleteResponse(t *testing.T) {
	history := []providers.ChatMessage{{Role: "user", Content: "input", Hidden: true, ReadOnly: true, Origin: "runtime", Cause: "skill_catalog"}}
	client := NewClient(func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != "prepare" {
			t.Fatalf("unexpected method: %s", method)
		}
		var request struct {
			History []map[string]any `json:"history"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if len(request.History) != 1 || request.History[0]["cause"] != "skill_catalog" || request.History[0]["read_only"] != true {
			t.Fatalf("history wire: %s", raw)
		}
		return json.RawMessage(`{"messages":[{"role":"user","content":"external-owned context","hidden":true,"origin":"plugin","cause":"refresh"}]}`), nil
	})
	messages, err := client.Prepare(context.Background(), history)
	if err != nil || len(messages) != 1 || messages[0].Content != "external-owned context" || !messages[0].Hidden || messages[0].Origin != "plugin" {
		t.Fatalf("external prepare: %+v %v", messages, err)
	}
	for _, raw := range []string{`{}`, `{"messages":null}`} {
		client = NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		})
		if _, err := client.Prepare(context.Background(), history); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	sentinel := errors.New("selected prepare unavailable")
	client = NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, sentinel })
	if _, err := client.Prepare(context.Background(), history); !errors.Is(err, sentinel) {
		t.Fatalf("lost selected error: %v", err)
	}
}

func TestFinalizeRunsOnceAfterSuccessOrNormalizedFailure(t *testing.T) {
	probe := &finalizationProbe{}
	native := NewService()
	ctx := context.Background()
	if err := native.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer native.Close(ctx)
	ctx = WithScope(ctx, NewScope(func(context.Context) (Backend, error) { return Backend{Executor: probe}, nil }))
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return native.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
	call := providers.ToolCall{Name: "probe", ID: "call"}
	result, err := client.ExecuteResult(ctx, call)
	if err != nil || probe.count != 0 {
		t.Fatalf("execute finalized prematurely: %d %v", probe.count, err)
	}
	settled, err := client.Finalize(ctx, call, result)
	if err != nil || probe.count != 1 || settled.Result.ModelText == nil || *settled.Result.ModelText != "settled" {
		t.Fatalf("finalize: %+v %v count=%d", settled, err, probe.count)
	}
	probe.fail = true
	if _, err = client.ExecuteResult(ctx, call); err == nil {
		t.Fatal("missing execution error")
	}
	if probe.count != 1 {
		t.Fatal("error path finalized before normalization")
	}
	settled, err = client.Finalize(ctx, call, toolresult.FromErrorText("normalized error"))
	if err != nil || !settled.Result.IsError || probe.count != 2 {
		t.Fatalf("normalized failure: %+v %v count=%d", settled, err, probe.count)
	}
	failed := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("selected finalizer unavailable")
	})
	if _, err := failed.Finalize(ctx, call, result); err == nil {
		t.Fatal("selected finalization failure hidden")
	}
}

type finalizationProbe struct {
	count int
	fail  bool
}

func (*finalizationProbe) Definitions() []providers.ToolDefinition { return nil }
func (p *finalizationProbe) Execute(context.Context, providers.ToolCall) (string, error) {
	if p.fail {
		return "", errors.New("execution failed")
	}
	return "raw", nil
}
func (p *finalizationProbe) FinalizeToolResult(_ providers.ToolCall, result toolresult.Result) toolresult.Result {
	p.count++
	text := "settled"
	result.ModelText = &text
	return result
}

// A service invocation can overlap other calls even if its caller later
// projects results serially. A shared last-context slot supplies no ownership.
type legacyServiceContextTools struct{ reads atomic.Int32 }

func (*legacyServiceContextTools) Definitions() []providers.ToolDefinition { return nil }
func (*legacyServiceContextTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	return call.ID, nil
}
func (e *legacyServiceContextTools) LastAdditionalContext() string {
	e.reads.Add(1)
	return "another invocation's context"
}

func TestNativeServiceDoesNotAttributeSharedLegacyContext(t *testing.T) {
	service := NewService()
	if err := service.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	executor := &legacyServiceContextTools{}
	ctx := WithScope(context.Background(), NewScope(func(context.Context) (Backend, error) { return Backend{Executor: executor}, nil }))
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
	result, err := client.Execute(ctx, providers.ToolCall{ID: "A", Name: "echo", Arguments: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if result.AdditionalContext != "" || executor.reads.Load() != 0 {
		t.Fatalf("service attributed a shared slot to A: context=%q reads=%d", result.AdditionalContext, executor.reads.Load())
	}
}
