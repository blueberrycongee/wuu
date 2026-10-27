package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

// The declaration is immutable for this captured provider generation. A provider
// must not repoint a live binding to a reset/copied dataset behind its consumers.
func (a *StateAuthority) selectAuthority(ctx context.Context, client *persistence.Client) (persistence.Authority, error) {
	if a.authorityCapabilityErr != nil {
		return persistence.Authority{}, a.authorityCapabilityErr
	}
	contract := persistence.LegacyAuthority()
	if a.authorityCapability {
		var err error
		contract, err = client.Authority(ctx)
		if err != nil {
			return persistence.Authority{}, err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.authorityContract != nil && *a.authorityContract != contract {
		return persistence.Authority{}, fmt.Errorf("%w: authority identity or configured topology changed within an active binding", persistence.ErrBindingChanged)
	}
	a.authorityContract = &contract
	return contract, nil
}

func (a *StateAuthority) providerOwned() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.authorityContract != nil && a.authorityContract.Mode == persistence.ProviderAuthority
}

func (a *StateAuthority) providerBinding() (*persistence.Authority, error) {
	raw, err := os.ReadFile(filepath.Join(a.dir, "provider.json"))
	if errors.Is(err, os.ErrNotExist) {
		if _, markerErr := os.Stat(filepath.Join(a.dir, "provider-initialized")); markerErr == nil {
			return nil, fmt.Errorf("%w: provider authority identity pin was removed", persistence.ErrCorrupt)
		} else if !errors.Is(markerErr, os.ErrNotExist) {
			return nil, markerErr
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var contract persistence.Authority
	if err := decodeAuthorityRequest(raw, &contract); err != nil {
		return nil, errors.Join(persistence.ErrCorrupt, err)
	}
	if err := contract.Validate(); err != nil || contract.Mode != persistence.ProviderAuthority {
		return nil, fmt.Errorf("%w: invalid provider authority identity pin", persistence.ErrCorrupt)
	}
	return &contract, nil
}

func (a *StateAuthority) requireHostJournalBinding() error {
	contract, err := a.providerBinding()
	if err != nil {
		return err
	}
	if contract != nil {
		return fmt.Errorf("%w: this home uses provider-owned authority %q; stop its writers and explicitly migrate before selecting a host journal", persistence.ErrMigrationRequired, contract.StoreID)
	}
	return nil
}

// A pin contains only authority metadata. A normal remote reopen does not scan,
// mirror, or pin the current data revision: other authorized writers may advance
// it. Crossing from the legacy journal instead verifies all data and receipts
// after its writers stop, and refuses to guess an unresolved commit outcome.
func (a *StateAuthority) verifyProviderBinding(ctx context.Context, client *persistence.Client, contract persistence.Authority) error {
	existing, err := a.providerBinding()
	if err != nil {
		return err
	}
	if existing != nil {
		if *existing != contract {
			return fmt.Errorf("%w: selected provider differs from pinned authority %q", persistence.ErrMigrationRequired, existing.StoreID)
		}
		return nil
	}
	record, err := a.readRecord()
	if err != nil {
		return err
	}
	if record == nil {
		return nil
	}
	members, err := a.liveMembers()
	if err != nil {
		return err
	}
	if len(members) != 0 {
		return fmt.Errorf("%w: stop all host-journal writers before transferring authority", persistence.ErrMigrationRequired)
	}
	if record.Pending != nil {
		return fmt.Errorf("%w: reopen the original host-journal provider and resolve its pending transaction before transferring authority", persistence.ErrMigrationRequired)
	}
	if a.exportCapabilityErr != nil {
		return a.exportCapabilityErr
	}
	// Full verification is deliberate at this one-time migration boundary; a
	// provider's self-reported checkpoint alone is not a transfer audit.
	return persistence.VerifyCheckpoint(ctx, client, record.Checkpoint)
}

func (a *StateAuthority) prepareProviderAuthority(ctx context.Context, client *persistence.Client) (persistence.Revision, error) {
	contract, err := a.selectAuthority(ctx, client)
	if err != nil {
		return 0, err
	}
	lock, err := storelock.AcquireUntilCanceled(ctx, a.dir)
	if err != nil {
		return 0, err
	}
	defer lock.Release()
	if err := a.verifyProviderBinding(ctx, client, contract); err != nil {
		return 0, err
	}
	view, err := client.Read(ctx, persistence.Query{Namespace: "wuu.state.authority.v1", Keys: []string{}})
	if err != nil {
		return 0, err
	}
	a.markPrepared(persistence.Checkpoint{Revision: view.Revision}, "")
	return view.Revision, nil
}

func (a *StateAuthority) activateProviderAuthority(ctx context.Context, client *persistence.Client) (persistence.Revision, error) {
	contract, err := a.selectAuthority(ctx, client)
	if err != nil {
		return 0, err
	}
	lock, err := storelock.AcquireUntilCanceled(ctx, a.dir)
	if err != nil {
		return 0, err
	}
	defer lock.Release()
	if err := a.verifyProviderBinding(ctx, client, contract); err != nil {
		return 0, err
	}
	view, err := client.Read(ctx, persistence.Query{Namespace: "wuu.state.authority.v1", Keys: []string{}})
	if err != nil {
		return 0, err
	}
	if err := authorityJSON(filepath.Join(a.dir, "provider.json"), contract); err != nil {
		return 0, err
	}
	if err := securefs.WriteFileAtomic(filepath.Join(a.dir, "provider-initialized"), []byte("1\n")); err != nil {
		return 0, err
	}
	if err := a.syncDirectory(); err != nil {
		return 0, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.closing {
		return 0, persistence.ErrClosed
	}
	a.admitted = true
	return view.Revision, nil
}

func (a *StateAuthority) invokeProviderAuthority(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	// Import requires the provider's own stopped-writer administration. An active
	// runtime must never replace its dataset by importing through a consumer call.
	if params.Method == "import" {
		return nil, fmt.Errorf("%w: stop provider writers and use its explicit verified transfer procedure", persistence.ErrMigrationRequired)
	}
	a.mu.Lock()
	contract := *a.authorityContract
	a.mu.Unlock()
	pin, err := a.providerBinding()
	if err != nil {
		return nil, err
	}
	if pin == nil || *pin != contract {
		return nil, fmt.Errorf("%w: provider authority identity pin changed", persistence.ErrBindingChanged)
	}
	return a.invokeSelectedProvider(ctx, params)
}
