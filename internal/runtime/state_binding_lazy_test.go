package runtime

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

type metadataSessionDomain struct{ calls atomic.Int32 }

func (*metadataSessionDomain) ID() string { return "metadata.sessions" }
func (*metadataSessionDomain) Status() pluginhost.Status {
	return pluginhost.Status{ID: "metadata.sessions", State: pluginhost.StateActive}
}
func (*metadataSessionDomain) Close(context.Context) error                       { return nil }
func (*metadataSessionDomain) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*metadataSessionDomain) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: session.ServiceName, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "Find", InputSchema: session.ServiceName + ".Find.input.v1", OutputSchema: session.ServiceName + ".Find.output.v1"}}}}
}
func (s *metadataSessionDomain) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	s.calls.Add(1)
	return json.RawMessage(`{"values":[{"id":"metadata","entries":0},true]}`), nil
}

func TestMetadataStateActivationDoesNotRequireInferenceJournal(t *testing.T) {
	registry := pluginhost.NewServiceRegistry()
	domain := &metadataSessionDomain{}
	documents := persistence.NewService("metadata.documents", t.TempDir(), "jsonl")
	state := newRuntimeState(registry, "metadata-only")
	host := pluginhost.New(state, domain, documents)
	host.AttachServiceRegistry(registry, registry.RegisterClients(state, domain, documents))
	defer host.Close(context.Background())
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if domain.calls.Load() != 0 || state.journal == nil || state.journal.RuntimeID() != "" {
		t.Fatal("metadata activation performed inference initialization")
	}
	if got, found, err := state.sessions.Find("metadata"); err != nil || !found || got.ID != "metadata" {
		t.Fatalf("selected metadata access failed: %+v %v %v", got, found, err)
	}
	journal := state.journal.ForOwner("owner")
	if domain.calls.Load() != 1 {
		t.Fatal("owner lookup initialized inference")
	}
	if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{}); err == nil {
		t.Fatal("unsupported inference silently succeeded")
	}
	if state.journal.RuntimeID() != "" {
		t.Fatal("failed inference initialization fabricated runtime identity")
	}
}
