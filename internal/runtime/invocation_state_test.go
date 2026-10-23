package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestInvocationStateRetainsOriginalAuthority(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule}
	original, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}})
	if err != nil {
		t.Fatal(err)
	}
	router := NewPluginSessionRouter()
	router.Bind(nil, nil, func(ctx context.Context, _ string, _ pluginhost.SessionListParams) (pluginhost.SessionListResult, error) {
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
	if _, err := kernel.retainInvocationState(); err == nil {
		t.Fatal("preparing kernel exposed business state")
	}
	generation := &PluginGeneration{}
	kernel.bindStateGeneration(generation, nil)
	if _, err := kernel.retainInvocationState(); err == nil {
		t.Fatal("unowned candidate exposed business state")
	}
	generation.refs.Store(-1)
	if _, err := kernel.retainInvocationState(); err == nil {
		t.Fatal("retired generation was resurrected")
	}
}
