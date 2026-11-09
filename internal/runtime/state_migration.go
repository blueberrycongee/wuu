package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// ExportStateForMigration exports the complete selected source, including owned
// namespaces, only after current owner/resource admission. The source provider
// must refuse export while unresolved effect inspections exist. Import remains
// an explicit stopped-writer operation; this method never switches authority.
func (s *Session) ExportStateForMigration(ctx context.Context) (persistence.Snapshot, error) {
	caller, ok := principal.FromContext(ctx)
	if !ok || !caller.IsOwner() {
		return persistence.Snapshot{}, errors.New("state migration requires a verified owner")
	}
	state, err := s.executionState(ctx)
	if err != nil {
		return persistence.Snapshot{}, err
	}
	defer state.Close()
	if state.Host == nil || state.Host.ServiceRegistry() == nil {
		return persistence.Snapshot{}, errors.New("selected state authority unavailable")
	}
	provider, ok := state.Host.ServiceRegistry().ProviderPluginIDFor(sessionStateConsumer, persistence.ServiceName, 1)
	if !ok {
		return persistence.Snapshot{}, errors.New("selected state authority unavailable")
	}
	if err = s.checkGrantAdmission(ctx, caller, "state/authority/export", []access.Resource{{Kind: "state-provider", ID: provider}}); err != nil {
		return persistence.Snapshot{}, err
	}
	documents := persistence.NewClient(func(call context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != "export" {
			return nil, errors.New("migration source client is export-only")
		}
		if err := caller.Check(call); err != nil {
			return nil, err
		}
		proof := pluginhost.ServiceAdmission{Version: 1, Principal: caller.Identity(), Action: "state/authority/export", Authority: caller.DurableAuthority(), InputDigest: executionauth.RawRequestDigest(raw)}
		result, callErr := state.Host.CallService(pluginhost.WithServiceAdmission(call, sessionStateConsumer, persistence.ServiceName, method, proof), sessionStateConsumer, persistence.ServiceName, method, raw)
		if callErr != nil {
			return nil, callErr
		}
		return result, nil
	})
	return documents.Export(ctx)
}
