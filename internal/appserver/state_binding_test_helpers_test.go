package appserver

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/titleservice"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// Embedded model fixtures still need a captured generation for receipt ownership.
func attachTestModelGeneration(t *testing.T, rt *runtime.Session, additional ...pluginhost.Client) {
	t.Helper()
	// These fixtures supply model transport directly, but their execution still
	// participates in the same selected prompt, context and title services.
	clients := []pluginhost.Client{promptservice.NewService(), titleservice.NewService(), &embeddedCompactionFixture{compactionservice.NewService()}, &embeddedModelConsumer{}, toolservice.NewService(), &embeddedToolsConsumer{}}
	clients = append(clients, additional...)
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	rt.PluginHost = pluginhost.New(clients...)
	rt.PluginHost.AttachServiceRegistry(registry, conflicts)
	if err := rt.PluginHost.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := rt.Cleanup(); err != nil {
			t.Error(err)
		}
	})
}

func attachTestHookGeneration(t *testing.T, rt *runtime.Session) {
	t.Helper()
	journal, err := rt.Sessions.NewInferenceJournalRuntime("appserver-hook-test")
	if err != nil {
		t.Fatal(err)
	}
	rt.InferenceJournalRuntime = journal
	t.Cleanup(func() { _ = journal.Close() })
	attachTestModelGeneration(t, rt)
}

func testRuntimeState(t *testing.T, rt *runtime.Session) *runtime.State {
	t.Helper()
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := binding.Close(); err != nil {
			t.Error(err)
		}
	})
	return binding
}

func newAppserverTestStore(t *testing.T, dir string) *session.Store {
	t.Helper()
	documents, err := persistence.Open(context.Background(), dir, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := documents.Close(); err != nil {
			t.Error(err)
		}
	})
	return session.NewStore(context.Background(), documents)
}

// Model transport comes from the embedded runner, rather than a host gateway.
type embeddedCompactionFixture struct{ *compactionservice.Service }

func (*embeddedCompactionFixture) RequiredServices() []pluginhost.ServiceRequirement { return nil }

type embeddedModelConsumer struct{ active atomic.Bool }

func (*embeddedModelConsumer) ID() string { return "wuu.session-execution" }
func (c *embeddedModelConsumer) Status() pluginhost.Status {
	state := pluginhost.StatePrepared
	if c.active.Load() {
		state = pluginhost.StateActive
	}
	return pluginhost.Status{ID: c.ID(), State: state}
}
func (*embeddedModelConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*embeddedModelConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{
		{Name: promptservice.ServiceName, MajorVersion: 1, Required: true}, {Name: compactionservice.ServiceName, MajorVersion: 1, Required: true}, {Name: titleservice.ServiceName, MajorVersion: 1, Required: true},
	}
}
func (c *embeddedModelConsumer) Activate(context.Context) error { c.active.Store(true); return nil }
func (c *embeddedModelConsumer) Close(context.Context) error    { c.active.Store(false); return nil }

// These fixtures provide their own toolkit and permission authority.
type embeddedToolsConsumer struct{ embeddedModelConsumer }

func (*embeddedToolsConsumer) ID() string { return "wuu.tool-execution" }
func (c *embeddedToolsConsumer) Status() pluginhost.Status {
	status := c.embeddedModelConsumer.Status()
	status.ID = c.ID()
	return status
}
func (*embeddedToolsConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: toolservice.ServiceName, MajorVersion: 1, Required: true}}
}
