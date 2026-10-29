package appserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

type blockedRetirementService struct {
	*toolservice.Service
	entered, release chan struct{}
	once             sync.Once
}

func (s *blockedRetirementService) Close(ctx context.Context) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.Service.Close(ctx)
}

func TestServerCloseJoinsRetiredNativeGeneration(t *testing.T) {
	for _, timedOut := range []bool{false, true} {
		t.Run(map[bool]string{false: "drained", true: "bounded"}[timedOut], func(t *testing.T) {
			service := &blockedRetirementService{Service: toolservice.NewService(), entered: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(service.release) })
			builds := 0
			factory := runtime.NativeProviderFactory{ID: toolservice.DefaultModule, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
				builds++
				if builds == 1 {
					return service, nil
				}
				return toolservice.NewService(), nil
			}}
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}}
			owner, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, NonInteractive: true, NativeProviders: []runtime.NativeProviderFactory{factory}})
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = owner.Cleanup() })
			thread, err := owner.NewThreadRuntime("cleanup-join")
			require.NoError(t, err)
			srv := New(owner, &lockedBuffer{})
			if timedOut {
				srv.shutdownDrainTimeoutForTest = 60 * time.Millisecond
			} else {
				srv.shutdownDrainTimeoutForTest = 5 * time.Second
			}
			candidate, err := owner.PreflightExtensions(cfg)
			require.NoError(t, err)
			require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
			srv.releaseDetachedThreadRuntime(detachedThreadRuntime{runtime: thread})
			srv.retirementMu.Lock()
			var retired <-chan struct{}
			for done := range srv.retirements {
				retired = done
				break
			}
			srv.retirementMu.Unlock()
			require.NotNil(t, retired, "retirement is tracked before shutdown")
			select {
			case <-service.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("retirement did not begin")
			}
			closed := make(chan struct{})
			go func() { srv.Close(); close(closed) }()
			if timedOut {
				select {
				case <-closed:
				case <-time.After(2 * time.Second):
					t.Fatal("server close exceeded bounded wait")
				}
				releaseOnce.Do(func() { close(service.release) })
				select {
				case <-retired:
				case <-time.After(5 * time.Second):
					t.Fatal("timed-out cleanup did not finish")
				}
			} else {
				select {
				case <-closed:
					t.Fatal("server close returned before retired generation cleanup")
				case <-time.After(2 * time.Second):
				}
				releaseOnce.Do(func() { close(service.release) })
				select {
				case <-closed:
				case <-time.After(5 * time.Second):
					t.Fatal("server close did not join retirement")
				}
			}
		})
	}
}
