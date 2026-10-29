package appserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Reopening a test host must select the same document authority; assigning a
// legacy directory field alone must never reopen an implicit SQLite fallback.
func rebindPersonalTestDocuments(t *testing.T, rt *runtime.Session) {
	t.Helper()
	documents, err := persistence.Open(context.Background(), filepath.Join(rt.SessionDir, "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := documents.Close(); err != nil {
			t.Error(err)
		}
	})
	rt.Documents, rt.Sessions = documents, session.NewStore(context.Background(), documents)
}

func personalTestOwner(ctx context.Context) context.Context {
	return principal.WithContext(ctx, principal.LocalOwner())
}

// Select the same public settings service used by production runtimes, but omit
// unrelated credentials from these scoped-state fixtures.
type personalTestStateConsumer struct{ pluginClientRequestTestRuntime }

func (*personalTestStateConsumer) ID() string { return runtime.StateServicesConsumer }
func (*personalTestStateConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: pluginsettings.ServiceName, MajorVersion: 1, Required: true}}
}
func (*personalTestStateConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*personalTestStateConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: runtime.StateServicesConsumer, State: pluginhost.StateActive}
}

type personalTestSessionConsumer struct{ personalTestStateConsumer }

func (*personalTestSessionConsumer) ID() string { return runtime.SessionStateConsumer }
func (*personalTestSessionConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: runtime.SessionStateConsumer, State: pluginhost.StateActive}
}
func (*personalTestSessionConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: session.ServiceName, MajorVersion: 1, Required: true}, {Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}

func attachPersonalBoundaryGeneration(t *testing.T, rt *runtime.Session, decorateDocuments ...func(*persistence.Service) pluginhost.Client) *personalReceiptServiceClient {
	t.Helper()
	provider := &personalReceiptServiceClient{pluginClientRequestTestRuntime: pluginClientRequestTestRuntime{id: "personal"}}
	documents := persistence.NewService("fixture.documents", filepath.Join(rt.SessionDir, "documents"), "sqlite")
	var selectedDocuments pluginhost.Client = documents
	if len(decorateDocuments) > 0 {
		if len(decorateDocuments) != 1 {
			t.Fatal("Personal fixture requires at most one selected document provider decorator")
		}
		selectedDocuments = decorateDocuments[0](documents)
	}
	attachTestModelGeneration(t, rt,
		provider,
		NewPersonalActionsConsumer(),
		permissionpolicy.NewService(permissionpolicy.Module),
		pluginsettings.NewServiceWithStateRoot(pluginsettings.Module, rt.WuuHome, rt.ProductDataRoot(), rt.RootDir),
		&personalTestStateConsumer{},
		&personalTestSessionConsumer{},
		session.NewService("fixture.session-domain", rt.Documents),
		selectedDocuments,
	)
	return provider
}
