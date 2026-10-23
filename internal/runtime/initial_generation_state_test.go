package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type initialStateConsumer struct {
	fakeServicePluginClient
	retain         func() (*State, error)
	captured       *State
	activationErr  error
	failActivation bool
	stopSnapshot   chan struct{}
	snapshotDone   sync.WaitGroup
}

func (c *initialStateConsumer) Prepare(context.Context) error {
	if state, err := c.retain(); err == nil {
		_ = state.Close()
		return errors.New("preparation admitted state capture")
	}
	return nil
}

func (c *initialStateConsumer) Activate(context.Context) error {
	c.captured, c.activationErr = c.retain()
	if c.activationErr != nil {
		return c.activationErr
	}
	if c.failActivation {
		return errors.New("activation failed after capturing state")
	}
	c.snapshotDone.Add(1)
	go func() {
		defer c.snapshotDone.Done()
		for {
			select {
			case <-c.stopSnapshot:
				return
			default:
				_, _ = c.captured.Services()
			}
		}
	}()
	c.active = true
	return nil
}

func TestInitialGenerationStateCaptureSurvivesConstruction(t *testing.T) {
	for _, constructor := range []string{"OpenState", "NewSession"} {
		for _, fail := range []bool{false, true} {
			name := constructor
			if fail {
				name += "/failure"
			} else {
				name += "/success"
			}
			t.Run(name, func(t *testing.T) {
				modules := append(testStateModules(), kernelHostModule, "example.initial-state")
				cfg := config.Default()
				cfg.Composition = &composition.Config{Version: composition.Version, Modules: &modules}
				var consumer *initialStateConsumer
				factory := NativeProviderFactory{ID: "example.initial-state", Build: func(input NativeProviderContext) (pluginhost.Client, error) {
					consumer = &initialStateConsumer{fakeServicePluginClient: fakeServicePluginClient{
						id: "example.initial-state", required: []pluginhost.ServiceRequirement{{Name: pluginhost.KernelSessionListService, MajorVersion: 1, Required: true}},
					}, retain: input.RetainState, failActivation: fail && constructor == "OpenState", stopSnapshot: make(chan struct{})}
					return consumer, nil
				}}
				root, home := t.TempDir(), t.TempDir()
				var state *State
				var owner *Session
				var err error
				if constructor == "OpenState" {
					state, err = OpenState(context.Background(), StateOptions{RootDir: root, HomeDir: home, Config: cfg, NativeProviders: []NativeProviderFactory{factory}})
					if state != nil {
						t.Cleanup(func() { _ = state.Close() })
					}
				} else {
					provider := ""
					if fail {
						provider = "missing-initial-provider"
					}
					owner, err = NewSession(Options{RootDir: root, HomeDir: home, Config: cfg, ProviderName: provider, NonInteractive: true, NoTools: true, NativeProviders: []NativeProviderFactory{factory}})
					if owner != nil {
						t.Cleanup(func() { _, _ = owner.Cleanup() })
					}
				}
				if consumer == nil || consumer.captured == nil {
					t.Fatalf("activation did not capture state: constructor=%v consumer=%+v", err, consumer)
				}
				close(consumer.stopSnapshot)
				consumer.snapshotDone.Wait()
				defer consumer.captured.Close()
				if consumer.activationErr != nil {
					t.Fatal(consumer.activationErr)
				}
				if (err != nil) != fail {
					t.Fatalf("constructor error=%v, want failure=%v", err, fail)
				}
				captured := consumer.captured
				if !fail {
					if owner != nil {
						if owner.pluginGeneration != captured.Generation || owner.Sessions != captured.Sessions || owner.PluginHost != captured.Host {
							t.Fatal("constructor replaced the activation generation")
						}
						if _, err := owner.Cleanup(); err != nil {
							t.Fatal(err)
						}
					} else {
						if state.Generation != captured.Generation || state.Sessions != captured.Sessions || state.Host != captured.Host {
							t.Fatal("constructor replaced the activation generation")
						}
						if err := state.Close(); err != nil {
							t.Fatal(err)
						}
					}
				}
				if _, err := captured.Sessions.Create("retained activation state"); err != nil {
					t.Fatalf("constructor released a retained provider: %v", err)
				}
				if err := captured.Close(); err != nil {
					t.Fatal(err)
				}
				if owner != nil {
					reports, err := owner.PluginGenerationRevocations(10)
					if err != nil || len(reports) != 1 || reports[0].GenerationID != captured.Generation.id {
						t.Fatalf("early capture skipped final retirement report: %+v %v", reports, err)
					}
				}
				if _, err := captured.Sessions.List(1); err == nil {
					t.Fatal("last capture leaked its provider")
				}
			})
		}
	}
}
