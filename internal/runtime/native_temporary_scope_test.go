package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

// Observe the actual native service invocation context without exposing scope
// identities in the public plugin protocol. Calling the same service with that
// context after retirement must reject it, even while the generation is alive.
type temporaryScopeObserver struct {
	*toolservice.Service
	mu        sync.Mutex
	calls     []context.Context
	afterList func(context.Context)
}

func (o *temporaryScopeObserver) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if p.Method == "list" {
		o.mu.Lock()
		o.calls = append(o.calls, context.WithoutCancel(ctx))
		o.mu.Unlock()
	}
	result, err := o.Service.InvokeService(ctx, p)
	if p.Method == "list" && err == nil {
		o.mu.Lock()
		callback := o.afterList
		o.mu.Unlock()
		if callback != nil {
			callback(context.WithoutCancel(ctx))
		}
	}
	return result, err
}
func (o *temporaryScopeObserver) reset() { o.mu.Lock(); o.calls = nil; o.mu.Unlock() }
func (o *temporaryScopeObserver) snapshot() []context.Context {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]context.Context(nil), o.calls...)
}
func (o *temporaryScopeObserver) assertClosed(t *testing.T, ctx context.Context) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := o.Service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: toolservice.ServiceName, Method: "list", Params: json.RawMessage(`{}`)})
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "tool scope is closed")
	case <-time.After(5 * time.Second):
		t.Fatal("scope did not finish closing")
	}
}

type briefScopeModel struct{ fail bool }

func (m briefScopeModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}
func (m briefScopeModel) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	events := make(chan providers.StreamEvent, 2)
	if m.fail {
		events <- providers.StreamEvent{Type: providers.EventError, Error: errors.New("fixture model failed")}
	} else {
		events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "fixture brief"}
		events <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	}
	close(events)
	return events, nil
}

func newTemporaryScopeRuntime(t *testing.T) (*Session, *temporaryScopeObserver) {
	t.Helper()
	observer := &temporaryScopeObserver{Service: toolservice.NewService()}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "one"}}}
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"default_provider":"fixture","providers":{"fixture":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key":"fixture","model":"one"}}}`), 0600))
	factory := NativeProviderFactory{ID: toolservice.DefaultModule, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) { return observer, nil }}
	owner, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, ConfigPath: path, NonInteractive: true, NativeProviders: []NativeProviderFactory{factory}})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = owner.Cleanup() })
	observer.reset()
	return owner, observer
}

func TestModelPreflightRetiresTemporaryToolScope(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			owner, observer := newTemporaryScopeRuntime(t)
			if fail {
				owner.Documents = nil
			}
			type result struct {
				thread *ThreadRuntime
				err    error
			}
			done := make(chan result, 1)
			go func() {
				thread, err := owner.NewThreadRuntimeForRootModel("preflight", owner.RootDir, ThreadModelSelection{Provider: "fixture", Model: "two"})
				done <- result{thread, err}
			}()
			var built result
			select {
			case built = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("model preflight did not finish")
			}
			thread, err := built.thread, built.err
			if fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				defer owner.ReleasePluginGeneration(thread.PluginGeneration)
				defer thread.CloseArtifacts()
				defer thread.CloseNativeToolScope(context.Background())
			}
			calls := observer.snapshot()
			require.NotEmpty(t, calls)
			observer.assertClosed(t, calls[0])
		})
	}
}

func TestHandoffRetiresTemporaryToolScope(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			owner, observer := newTemporaryScopeRuntime(t)
			binding, err := owner.RetainState()
			require.NoError(t, err)
			defer binding.Close()
			owner.StreamRunner.WrapModelClient = nil
			binding.mu.Lock()
			binding.models.Main = briefScopeModel{fail: fail}
			binding.mu.Unlock()
			_, _, fork, err := owner.HandoffBriefForState(binding)
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() {
				_, err := fork(context.Background(), nil, agent.CompactionNotePlan{Prompt: "make brief"})
				done <- err
			}()
			select {
			case err := <-done:
				if fail {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("handoff did not finish")
			}
			calls := observer.snapshot()
			require.NotEmpty(t, calls)
			observer.assertClosed(t, calls[len(calls)-1])
			_, err = fork(context.Background(), nil, agent.CompactionNotePlan{Prompt: "second attempt"})
			require.ErrorContains(t, err, "already been used")
			require.Len(t, observer.snapshot(), len(calls))
		})
	}
}

func TestUnusedHandoffDoesNotInitializeTemporaryToolScope(t *testing.T) {
	owner, observer := newTemporaryScopeRuntime(t)
	binding, err := owner.RetainState()
	require.NoError(t, err)
	defer binding.Close()
	_, _, fork, err := owner.HandoffBriefForState(binding)
	require.NoError(t, err)
	require.NotNil(t, fork)
	require.Empty(t, observer.snapshot())
}

// A selected provider can return its list while a previously admitted native
// request is still completing. Hold the real toolkit's skill refresh to make
// that overlap deterministic, including when it ignores cancellation.
func TestTemporaryEntryRetiresWhileNativePreparationIsRunning(t *testing.T) {
	for _, entry := range []string{"preflight", "handoff"} {
		t.Run(entry, func(t *testing.T) {
			owner, observer := newTemporaryScopeRuntime(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			owner.Toolkit.SetSkillsWithAvailability(nil, nil)
			owner.Toolkit.SetSkillRefresh(func(context.Context, bool) ([]skills.Skill, error) { close(entered); <-release; return nil, nil })
			nativeDone := make(chan error, 1)
			var started atomic.Bool
			observer.mu.Lock()
			observer.afterList = func(ctx context.Context) {
				if !started.CompareAndSwap(false, true) {
					return
				}
				go func() {
					_, err := observer.Service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: toolservice.ServiceName, Method: "prepare", Params: json.RawMessage(`{"history":[]}`)})
					nativeDone <- err
				}()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
				}
			}
			observer.mu.Unlock()
			generation := owner.pluginGeneration
			done := make(chan error, 1)
			if entry == "preflight" {
				go func() {
					thread, err := owner.NewThreadRuntimeForRootModel("temporary-blocked", owner.RootDir, ThreadModelSelection{Provider: "fixture", Model: "two"})
					if err == nil {
						_ = thread.CloseNativeToolScope(context.Background())
						_ = thread.CloseArtifacts()
						owner.ReleasePluginGeneration(thread.PluginGeneration)
					}
					done <- err
				}()
			} else {
				owner.StreamRunner.WrapModelClient = nil
				binding, err := owner.RetainState()
				require.NoError(t, err)
				binding.mu.Lock()
				binding.models.Main = briefScopeModel{}
				binding.mu.Unlock()
				_, _, fork, err := owner.HandoffBriefForState(binding)
				require.NoError(t, err)
				go func() {
					_, err := fork(context.Background(), nil, agent.CompactionNotePlan{Prompt: "brief"})
					_ = binding.Close()
					done <- err
				}()
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("background native preparation did not start")
			}
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("entry blocked waiting for native preparation")
			}
			calls := observer.snapshot()
			require.NotEmpty(t, calls)
			observer.assertClosed(t, calls[0])
			owner.pluginGenerationMu.Lock()
			owner.pluginGeneration = nil
			owner.pluginGenerationMu.Unlock()
			require.NoError(t, owner.releasePluginGeneration(generation))
			require.Greater(t, generation.refs.Load(), int32(0), "generation must remain alive while preparation is running")
			releaseOnce.Do(func() { close(release) })
			select {
			case <-nativeDone:
			case <-time.After(5 * time.Second):
				t.Fatal("native preparation did not finish")
			}
			require.Eventually(t, func() bool {
				reports, err := owner.PluginGenerationRevocations(0)
				if err != nil {
					return false
				}
				for _, report := range reports {
					if report.GenerationID == generation.id {
						return true
					}
				}
				return false
			}, 5*time.Second, 10*time.Millisecond)
		})
	}
}
