package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A conversation must bind before inference, keep that identity after rebuilding
// its runtime or forking, and fail before effects if durable admission is lost.
func TestSessionDriverComposition(t *testing.T) {
	for _, name := range []string{"changed-default", "fork", "missing-session", "incompatible-checkpoint"} {
		t.Run(name, func(t *testing.T) {
			model := &defaultBindingModel{}
			s, _ := workerCompositionSession(t, false, NativeProviderFactory{ID: "test.session-model", Build: func(NativeProviderContext) (pluginhost.Client, error) {
				return &sessionModelAdapterFixture{nativeModelProvider: nativeModelProvider{state: pluginhost.StatePrepared}, model: model}, nil
			}})
			if (&modelAdapterClient{host: s.pluginGeneration.host, inner: model}).NativeCompactionAvailable() || model.calls != 0 {
				t.Fatal("unsupported selected model advertised compaction or initialized inference")
			}
			s.RootDir, s.StateDir, s.SessionDir = t.TempDir(), t.TempDir(), t.TempDir()
			s.StreamRunner = &agent.StreamRunner{Client: model, Model: "controlled"}
			id := "conversation"
			if name != "missing-session" {
				if _, err := s.Sessions.Create(id); err != nil {
					t.Fatal(err)
				}
			}
			if name == "incompatible-checkpoint" {
				if err := s.Sessions.SaveDriverCheckpoint(id, session.DriverCheckpointRecord{ContractVersion: 1, DriverID: "other-driver", DriverVersion: "1.0.0", State: []byte(`{}`)}); err != nil {
					t.Fatal(err)
				}
			}
			run := func() error {
				thread, err := s.NewThreadRuntime(id)
				if err != nil {
					return err
				}
				defer s.releasePluginGeneration(thread.PluginGeneration)
				_, err = thread.StreamRunner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: "Say hello"}}, nil)
				return err
			}
			err := run()
			if name == "missing-session" || name == "incompatible-checkpoint" {
				if err == nil || model.calls != 0 {
					t.Fatalf("durability failure admitted inference: calls=%d err=%v", model.calls, err)
				}
				return
			}
			if err != nil || model.calls != 1 {
				t.Fatalf("initial turn: calls=%d err=%v", model.calls, err)
			}
			if name == "fork" {
				if _, err := s.Sessions.CreateForkWithMetadata("fork", s.RootDir, session.ForkMetadata{ForkedFromID: id}); err != nil {
					t.Fatal(err)
				}
				id = "fork"
			}
			s.DriverProfile = "unavailable"
			if err := run(); err != nil || model.calls != 2 {
				t.Fatalf("saved conversation changed its driver: calls=%d err=%v", model.calls, err)
			}
		})
	}
}

func TestSessionDriverCheckpointRejectsMissingOwner(t *testing.T) {
	store := sessionDriverCheckpointStore{sessions: testSessionStore(t, t.TempDir()), sessionID: "missing"}
	err := store.Save(context.Background(), loopdriver.Checkpoint{ContractVersion: 1, DriverID: loopdriver.DefaultDriverID, DriverVersion: loopdriver.DefaultDriverVersion, State: []byte(`{}`)})
	if !errors.Is(err, session.ErrSessionNotFound) || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("checkpoint acknowledged a nonexistent owner: %v", err)
	}
}

// The fixture replaces the declared model provider, never a private client below
// the graph. This exercises the same session binding as an external adapter.
type sessionModelAdapterFixture struct {
	nativeModelProvider
	model *defaultBindingModel
}

func (f *sessionModelAdapterFixture) ID() string                                        { return "test.session-model" }
func (f *sessionModelAdapterFixture) Prepare(context.Context) error                     { return nil }
func (f *sessionModelAdapterFixture) Close(context.Context) error                       { return nil }
func (f *sessionModelAdapterFixture) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (f *sessionModelAdapterFixture) ModelAdapters() []pluginhost.ModelAdapterDescriptor {
	return []pluginhost.ModelAdapterDescriptor{{ID: nativeModelAdapter, Version: 1, Mode: "direct"}}
}
func (f *sessionModelAdapterFixture) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return json.Marshal(f.ModelAdapters()[0])
}
func (f *sessionModelAdapterFixture) InvokeCapability(context.Context, pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	f.model.calls++
	raw, err := json.Marshal(pluginprotocol.ModelAdapterOutput{Response: &pluginprotocol.ModelResponseV1{Content: "Hello", FinishReason: "stop"}})
	return pluginhost.CapabilityInvokeResult{Output: raw}, err
}
