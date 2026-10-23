package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	modules := []string{sessionStateConsumer, persistence.JSONLModule}
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
	invoker := &kernelServiceInvoker{parent: kernel, method: pluginhost.HostServiceSessionList}
	done := make(chan error, 1)
	go func() {
		_, err := invoker.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "caller", Method: pluginhost.KernelServiceMethod, Params: json.RawMessage(`{}`)})
		done <- err
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
