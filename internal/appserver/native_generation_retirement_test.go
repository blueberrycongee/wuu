package appserver

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

type observedGenerationToolService struct {
	*toolservice.Service
	closed chan struct{}
	once   sync.Once
}

func (s *observedGenerationToolService) Close(ctx context.Context) error {
	err := s.Service.Close(ctx)
	s.once.Do(func() { close(s.closed) })
	return err
}

func TestDetachedRuntimeKeepsGenerationUntilToolReturns(t *testing.T) {
	service := &observedGenerationToolService{Service: toolservice.NewService(), closed: make(chan struct{})}
	var built int
	factory := runtime.NativeProviderFactory{ID: toolservice.DefaultModule, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		built++
		if built == 1 {
			return service, nil
		}
		return &observedGenerationToolService{Service: toolservice.NewService(), closed: make(chan struct{})}, nil
	}}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("retained"), 0600))
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}}
	owner, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, NonInteractive: true, NativeProviders: []runtime.NativeProviderFactory{factory}})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = owner.Cleanup() })
	thread, err := owner.NewThreadRuntime("retirement-test")
	require.NoError(t, err)
	require.NotNil(t, thread.PluginGeneration)
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(resume) })
	thread.Toolkit.SetPermissionRequestHook(func(context.Context, *tools.Toolkit, tools.ToolInfo, providers.ToolCall) error {
		close(entered)
		<-resume
		return nil
	})
	ctx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "retirement-test", ExecutionID: "turn"})
	done := make(chan error, 1)
	toolFinished := make(chan struct{})
	go func() {
		defer close(toolFinished)
		_, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "held", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	// Publication drops the session's old-generation reference while this
	// thread still owns its admitted call and the original provider.
	candidate, err := owner.PreflightExtensions(cfg)
	require.NoError(t, err)
	require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
	retired := make(chan struct{})
	go func() {
		releaseDetachedThreadRuntime(detachedThreadRuntime{runtime: thread, mainDone: toolFinished})
		close(retired)
	}()
	select {
	case <-retired:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime retirement blocked")
	}
	select {
	case <-service.closed:
		t.Fatal("generation closed while tool was running")
	case <-time.After(100 * time.Millisecond):
	}
	once.Do(func() { close(resume) })
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not finish")
	}
	select {
	case <-service.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("generation not closed after tool finished")
	}
}
