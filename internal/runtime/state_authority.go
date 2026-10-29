package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
	"github.com/fsnotify/fsnotify"
)

// StateAuthority admits the selected provider's explicit authority contract.
// Legacy host-journal mode owns the host's durable head; provider mode owns only
// a metadata identity pin, never business data or pending intent. One instance
// decorates one generation's selected invoker. Registry
// dispatch still owns permissions, provider identity and callback lifetimes.
// Its fence also protects complete checkpoint traversals, so waiters use their
// caller's cancellation budget rather than the short ordinary-transaction limit.
type StateAuthority struct {
	dir, id                             string
	mu                                  sync.Mutex
	preflightMu                         sync.Mutex
	provider                            pluginhost.ServiceInvoker
	exportCapabilityErr                 error
	checkpointCapability                bool
	authorityCapability                 bool
	authorityCapabilityErr              error
	authorityContract                   *persistence.Authority
	documents                           persistence.Documents
	prepared, admitted, closing, closed bool
	preparedCheckpoint                  persistence.Checkpoint
	preparedEpoch                       string
	member                              *storelock.Lock
	watcher                             *fsnotify.Watcher
	cancel                              context.CancelFunc
	done                                chan struct{}
	probeCleanups                       map[string]struct{}
	closeErr                            error
}

type authorityPending struct {
	Caller      string                  `json:"caller,omitempty"`
	Method      string                  `json:"method,omitempty"`
	Params      json.RawMessage         `json:"params,omitempty"`
	Transaction persistence.Transaction `json:"transaction"`
	Before      persistence.Checkpoint  `json:"before"`
	After       persistence.Checkpoint  `json:"after"`
}
type authorityRecord struct {
	Version    int                    `json:"version"`
	Checkpoint persistence.Checkpoint `json:"checkpoint"`
	Pending    *authorityPending      `json:"pending,omitempty"`
	Epoch      string                 `json:"epoch"`
}
type authorityEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	Digest  string          `json:"digest"`
}
type authorityLocked struct {
	dir    string
	record *authorityRecord
}
type authorityLockKey struct{}
type authorityPreflightKey struct{}
type authorityActivationKey struct{}
type authorityRawKey struct{}
type authorityStewardKey struct{}
type authorityRecoveryKey struct{}
type authorityMigrationKey struct{}
type authorityRequest struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
}
type authorityResponse struct {
	Epoch      string                 `json:"epoch"`
	Checkpoint persistence.Checkpoint `json:"checkpoint"`
	Done       bool                   `json:"done"`
	Error      string                 `json:"error,omitempty"`
}

func NewStateAuthority(wuuHome string) (*StateAuthority, error) {
	if !filepath.IsAbs(wuuHome) {
		return nil, errors.New("state authority requires an absolute resolved Wuu home")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	return &StateAuthority{dir: filepath.Join(wuuHome, "state-authority"), id: hex.EncodeToString(random[:])}, nil
}

type authorityInvoker struct {
	pluginhost.ServiceInvoker
	authority *StateAuthority
}

func (a *StateAuthority) Wrap(provider pluginhost.ServiceInvoker) pluginhost.ServiceInvoker {
	a.provider = provider
	a.exportCapabilityErr = fmt.Errorf("%w: provider %s requires paged export v2", persistence.ErrMigrationRequired, provider.ID())
	if declared, ok := provider.(interface {
		ProvidedServices() []pluginhost.ServiceDescriptor
	}); ok {
		for _, service := range declared.ProvidedServices() {
			major, valid := pluginhost.ServiceVersionMajor(service.Version)
			if service.Name != persistence.ServiceName || !valid || major != 1 {
				continue
			}
			for _, method := range service.Methods {
				if method.Name == "authority" {
					a.authorityCapability = true
					if method.InputSchema != persistence.AuthorityInputSchema || method.OutputSchema != persistence.AuthorityOutputSchema || !method.Preparation {
						a.authorityCapabilityErr = fmt.Errorf("%w: authority requires preparation-safe v1 schemas", persistence.ErrInvalid)
					}
				}
				if method.Name == "checkpoint" && method.InputSchema == persistence.CheckpointInputSchema && method.OutputSchema == persistence.CheckpointOutputSchema && method.Preparation {
					a.checkpointCapability = true
				}
				if method.Name == "export" && method.InputSchema == persistence.ExportInputSchema && method.OutputSchema == persistence.ExportOutputSchema && method.Preparation {
					a.exportCapabilityErr = nil
				}
			}
		}
	}
	return &authorityInvoker{ServiceInvoker: provider, authority: a}
}
func (w *authorityInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	raw, err := w.authority.invoke(ctx, params)
	if err != nil {
		return nil, authorityServiceError(err)
	}
	return raw, nil
}

// Preflight runs inside the state consumer Prepare callback and uses only
// candidate read/export. It uses an authenticated registry call; private coordination never
// substitutes a fake provider or manufactures another provider's callback scope.
func (a *StateAuthority) Preflight(ctx context.Context, documents persistence.Documents) error {
	a.preflightMu.Lock()
	defer a.preflightMu.Unlock()
	a.mu.Lock()
	if a.closed || a.closing {
		a.mu.Unlock()
		return persistence.ErrClosed
	}
	if a.admitted {
		a.mu.Unlock()
		return nil
	}
	a.documents = documents
	a.mu.Unlock()
	_, err := documents.Read(context.WithValue(ctx, authorityPreflightKey{}, a), persistence.Query{Namespace: "wuu.state.authority.v1", Keys: []string{}})
	return err
}

// Activate finalizes a previously verified candidate after the provider's
// effectful lifecycle starts, before migration, journals or consumer writes.
func (a *StateAuthority) Activate(ctx context.Context, documents persistence.Documents) error {
	a.preflightMu.Lock()
	defer a.preflightMu.Unlock()
	a.mu.Lock()
	if a.closed || a.closing {
		a.mu.Unlock()
		return persistence.ErrClosed
	}
	if a.admitted {
		a.mu.Unlock()
		return nil
	}
	if !a.prepared {
		a.mu.Unlock()
		return errors.New("state authority was not verified during preparation")
	}
	a.documents = documents
	a.mu.Unlock()
	_, err := documents.Read(context.WithValue(ctx, authorityActivationKey{}, a), persistence.Query{Namespace: "wuu.state.authority.v1", Keys: []string{}})
	return err
}

// Close releases membership only after pending admission probes have cleaned up.
// The runtime calls it after journal flush and before closing provider resources.
func (a *StateAuthority) Close(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		err := a.closeErr
		a.mu.Unlock()
		return err
	}
	a.closing = true
	cancel, done := a.cancel, a.done
	if cancel == nil {
		a.admitted = false
		a.closed = true
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	// The worker no longer owns a live callback after retirement. Finish only
	// its recorded probe cleanups using this lifecycle's authorized context,
	// while the original provider graph and authority membership remain alive.
	err := errors.Join(a.finishProbeCleanups(ctx), a.member.Release())
	a.mu.Lock()
	a.closeErr = err
	a.closed = true
	a.admitted = false
	a.mu.Unlock()
	return err
}

func (a *StateAuthority) finishProbeCleanups(ctx context.Context) error {
	a.mu.Lock()
	pending := make([]string, 0, len(a.probeCleanups))
	for id := range a.probeCleanups {
		pending = append(pending, id)
	}
	a.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	lock, err := storelock.AcquireUntilCanceled(ctx, a.dir)
	if err != nil {
		return err
	}
	defer lock.Release()
	record, err := a.readRecord()
	if err != nil || record == nil {
		if err == nil {
			err = persistence.ErrCorrupt
		}
		return err
	}
	cleanupCtx := context.WithValue(ctx, authorityLockKey{}, &authorityLocked{dir: a.dir, record: record})
	for _, id := range pending {
		// Read resolves an uncertain original cleanup through the durable head
		// before deciding whether a new retirement cleanup is needed. Never
		// change an earlier transaction's revision or replay a guessed outcome.
		view, err := a.documents.Read(cleanupCtx, persistence.Query{Namespace: "wuu.state.authority.v1", Keys: []string{id}})
		if err != nil {
			return err
		}
		if len(view.Documents) != 0 {
			_, err := a.documents.Commit(cleanupCtx, persistence.Transaction{ID: "state-admission-cleanup:" + id + ":retirement", ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: "wuu.state.authority.v1", Key: id, Delete: true}}})
			if err != nil {
				return err
			}
		}
		a.mu.Lock()
		delete(a.probeCleanups, id)
		a.mu.Unlock()
	}
	return nil
}

func (a *StateAuthority) rawClient(params pluginhost.ServiceInvokeParams) *persistence.Client {
	return persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		invoke := params
		invoke.Method = method
		invoke.Params = raw
		return a.invokeSelectedProvider(ctx, invoke)
	})
}
func (a *StateAuthority) invokeSelectedProvider(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	// Never forward a consumer-provided attestation. The opaque context key is
	// scoped to this captured authority and exists only around host coordination.
	params.StorageStewardship = nil
	if owner, _ := ctx.Value(authorityStewardKey{}).(*StateAuthority); owner == a && (params.Method == "read" || params.Method == "export" || params.Method == "checkpoint") {
		digest := sha256.Sum256(params.Params)
		params.StorageStewardship = &pluginhost.StorageStewardship{Version: 1, Authority: a.id, Provider: a.provider.ID(), Operation: "verify", InputDigest: hex.EncodeToString(digest[:]), InputJSON: string(params.Params)}
	}
	if pending, ok := ctx.Value(authorityRecoveryKey{}).(*authorityPending); ok && pending.Caller != "" {
		method := pending.Method
		if method == "" {
			method = "commit"
		}
		valid := params.Method == method
		if method == "commit" {
			match := authorityWireMatch{remaining: params.Params}
			valid = valid && persistence.WriteTransactionJSON(&match, pending.Transaction) == nil && len(match.remaining) == 0
		} else {
			valid = valid && method == "claim-namespaces" && bytes.Equal(params.Params, pending.Params)
		}
		if !valid {
			return nil, persistence.ErrCorrupt
		}
		digest := sha256.Sum256(params.Params)
		params.StorageStewardship = &pluginhost.StorageStewardship{Version: 1, Authority: a.id, Provider: a.provider.ID(), Operation: "recover", Consumer: pending.Caller, InputDigest: hex.EncodeToString(digest[:]), InputJSON: string(params.Params)}
	}
	if owner, _ := ctx.Value(authorityMigrationKey{}).(*StateAuthority); owner == a && (params.Method == "export" || params.Method == "checkpoint" || params.Method == "read") {
		digest := sha256.Sum256(params.Params)
		params.StorageStewardship = &pluginhost.StorageStewardship{Version: 1, Authority: a.id, Provider: a.provider.ID(), Operation: "migration", InputDigest: hex.EncodeToString(digest[:]), InputJSON: string(params.Params)}
	}
	ctx = context.WithValue(ctx, authorityMigrationKey{}, nil)
	ctx = context.WithValue(ctx, authorityRecoveryKey{}, nil)
	ctx = context.WithValue(ctx, authorityStewardKey{}, nil)
	// Only host coordination may reuse a held lock. Never pass it into provider
	// callbacks, which could otherwise bypass serialization recursively.
	ctx = context.WithValue(ctx, authorityLockKey{}, nil)
	ctx = context.WithValue(ctx, authorityPreflightKey{}, nil)
	ctx = context.WithValue(ctx, authorityActivationKey{}, nil)
	ctx = context.WithValue(ctx, authorityRawKey{}, a.dir)
	return a.provider.InvokeService(ctx, params)
}

func (a *StateAuthority) invoke(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return nil, persistence.ErrClosed
	}
	if ctx.Value(authorityRawKey{}) == a.dir {
		return nil, errors.New("state provider recursively called its own authority")
	}
	if params.Method == "export" && params.Admission != nil {
		proof := params.Admission
		digest := sha256.Sum256(params.Params)
		if proof.Version == 1 && proof.Principal.Validate() == nil && proof.Action == "state/authority/export" && proof.InputJSON == string(params.Params) && proof.InputDigest == hex.EncodeToString(digest[:]) && ctx.Err() == nil {
			ctx = context.WithValue(ctx, authorityMigrationKey{}, a)
		}
	}
	client := a.rawClient(params)
	if ctx.Value(authorityPreflightKey{}) == a {
		revision, err := a.prepare(ctx, client)
		if err != nil {
			return nil, err
		}
		return json.Marshal(persistence.View{Revision: revision, Documents: []persistence.Document{}})
	}
	if ctx.Value(authorityActivationKey{}) == a {
		revision, err := a.activate(ctx, client)
		if err != nil {
			return nil, err
		}
		return json.Marshal(persistence.View{Revision: revision, Documents: []persistence.Document{}})
	}
	if _, err := a.selectAuthority(ctx, client); err != nil {
		return nil, err
	}
	if params.Method == "authority" {
		if err := decodeAuthorityRequest(params.Params, new(struct{})); err != nil {
			return nil, err
		}
		a.mu.Lock()
		contract := *a.authorityContract
		a.mu.Unlock()
		return json.Marshal(contract)
	}
	if params.Method == "export" {
		if _, err := persistence.DecodeExportRequest(params.Params); err != nil {
			return nil, err
		}
	} else if params.Method == "compact" || params.Method == "checkpoint" {
		if err := decodeAuthorityRequest(params.Params, new(struct{})); err != nil {
			return nil, err
		}
	}
	locked, _ := ctx.Value(authorityLockKey{}).(*authorityLocked)
	a.mu.Lock()
	cleanup := params.Method == "release-inspection" && pluginhost.InShutdownCleanup(ctx)
	allowed := a.admitted && !a.closed && (!a.closing || locked != nil || cleanup)
	lazyAdmission := !a.admitted && !a.closed && !a.closing && a.prepared && a.documents != nil && !pluginhost.IsServicePreparation(ctx)
	readOnly := !a.admitted && !a.closed && !a.closing && (params.Method == "read" || params.Method == "export" || params.Method == "checkpoint")
	a.mu.Unlock()
	if lazyAdmission {
		// Provider activation precedes every active consumer callback, but peer
		// consumers need not activate in session-consumer registration order.
		if err := a.admitActiveCall(ctx, client); err != nil {
			return nil, err
		}
		allowed = true
	}
	if !allowed {
		if readOnly {
			return a.readPrepared(ctx, client, params)
		}
		return nil, fmt.Errorf("%w: selected state provider has not been admitted", persistence.ErrMigrationRequired)
	}
	if a.providerOwned() {
		return a.invokeProviderAuthority(ctx, params)
	}
	if locked != nil && locked.dir == a.dir {
		return a.dispatch(ctx, client, locked, params)
	}
	lock, err := storelock.AcquireUntilCanceled(ctx, a.dir)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	record, err := a.readRecord()
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("%w: admitted authority head is missing", persistence.ErrCorrupt)
	}
	locked = &authorityLocked{dir: a.dir, record: record}
	return a.dispatch(ctx, client, locked, params)
}

func (a *StateAuthority) admitActiveCall(ctx context.Context, client *persistence.Client) error {
	a.preflightMu.Lock()
	defer a.preflightMu.Unlock()
	a.mu.Lock()
	if a.closed || a.closing {
		a.mu.Unlock()
		return persistence.ErrClosed
	}
	admitted := a.admitted
	a.mu.Unlock()
	if admitted {
		return nil
	}
	_, err := a.activate(ctx, client)
	return err
}

// Registry dispatch has already authorized the actual consumer's lifecycle
// callback. Every prepared reader verifies the candidate under the same rules;
// only Activate may recover pending writes or enroll a writable member.
func (a *StateAuthority) readPrepared(ctx context.Context, client *persistence.Client, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	a.preflightMu.Lock()
	defer a.preflightMu.Unlock()
	if a.providerOwned() {
		if _, err := a.prepareProviderAuthority(ctx, client); err != nil {
			return nil, err
		}
		return a.invokeSelectedProvider(ctx, params)
	}
	var query *persistence.ExportQuery
	if params.Method == "export" {
		var err error
		query, err = persistence.DecodeExportRequest(params.Params)
		if err != nil {
			return nil, err
		}
	}
	a.mu.Lock()
	prepared := a.prepared
	a.mu.Unlock()
	// Verify once before a prepared traversal. Its continuation pages are only
	// bounded reads pinned to the verified revision and provider generation;
	// the collecting caller validates the complete checkpoint before admission.
	if query == nil || !prepared || query.Section == "documents" && query.After == nil {
		if _, err := a.prepare(ctx, client); err != nil {
			return nil, err
		}
	}
	lock, err := storelock.AcquireUntilCanceled(ctx, a.dir)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	record, err := a.readRecord()
	if err != nil {
		return nil, err
	}
	var checkpoint persistence.Checkpoint
	if query == nil {
		checkpoint, err = a.preparedAuthorityCheckpoint(ctx, client, record)
		if err != nil {
			return nil, err
		}
	} else {
		a.mu.Lock()
		checkpoint = a.preparedCheckpoint
		a.mu.Unlock()
		if record != nil {
			if record.Pending == nil || checkpoint != record.Pending.After {
				checkpoint = record.Checkpoint
			}
		}
	}
	return readAuthority(ctx, client, checkpoint, params)
}

func decodeAuthorityRequest(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", persistence.ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%w: expected one request object", persistence.ErrInvalid)
	}
	return nil
}

func (a *StateAuthority) dispatch(ctx context.Context, client *persistence.Client, locked *authorityLocked, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := a.requireHostJournalBinding(); err != nil {
		return nil, err
	}
	if err := a.recoverPending(ctx, client, locked.record); err != nil {
		return nil, err
	}
	head := locked.record.Checkpoint
	switch params.Method {
	case "acquire-inspection", "release-inspection", "list-inspections", "reconcile-inspection", "inspection-topology":
		// Provider-owned guards are metadata, not a business mutation or journal entry.
		return a.invokeSelectedProvider(ctx, params)
	case "read", "export", "checkpoint":
		return readAuthority(ctx, client, head, params)
	case "claim-namespaces":
		return a.claimNamespaces(ctx, client, locked.record, params)
	case "commit":
		tx, err := persistence.DecodeTransaction(params.Params)
		if err != nil {
			return nil, err
		}
		receipt, err := a.commit(ctx, client, locked.record, tx, params.Params, params.Caller)
		if err != nil {
			return nil, err
		}
		return json.Marshal(receipt)
	case "compact":
		if err := persistence.VerifyCheckpoint(context.WithValue(ctx, authorityStewardKey{}, a), client, head); err != nil {
			return nil, err
		}
		result, err := client.Compact(ctx)
		if err != nil {
			return nil, err
		}
		if result.Revision != head.Revision {
			return nil, persistence.ErrBindingChanged
		}
		if err := persistence.VerifyCheckpoint(context.WithValue(ctx, authorityStewardKey{}, a), client, head); err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "import":
		return nil, fmt.Errorf("%w: stop all source writers and prepare the verified target before admission", persistence.ErrMigrationRequired)
	default:
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown state authority method"}
	}
}

func readAuthority(ctx context.Context, client *persistence.Client, checkpoint persistence.Checkpoint, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method == "checkpoint" {
		if err := authorityRevision(ctx, client, checkpoint.Revision); err != nil {
			return nil, err
		}
		return json.Marshal(checkpoint)
	}
	if params.Method == "read" {
		var query persistence.Query
		if err := decodeAuthorityRequest(params.Params, &query); err != nil {
			return nil, err
		}
		if query.AtRevision != nil && *query.AtRevision != checkpoint.Revision {
			return nil, persistence.ErrConflict
		}
		// Preserve selectors exactly: filling AtRevision here would incorrectly
		// admit cursor requests that omitted their required revision pin.
		view, err := client.Read(ctx, query)
		if err != nil {
			return nil, err
		}
		if view.Revision != checkpoint.Revision {
			return nil, persistence.ErrBindingChanged
		}
		return json.Marshal(view)
	}
	query, err := persistence.DecodeExportRequest(params.Params)
	if err != nil {
		return nil, err
	}
	if query != nil {
		if query.AtRevision != nil && *query.AtRevision != checkpoint.Revision {
			return nil, persistence.ErrConflict
		}
		page, encoded, err := client.ExportPageEncoded(ctx, *query)
		if err != nil {
			return nil, err
		}
		if page.Revision != checkpoint.Revision {
			return nil, persistence.ErrBindingChanged
		}
		return encoded, nil
	}
	snapshot, err := client.Export(ctx)
	if err != nil {
		return nil, err
	}
	actual, err := persistence.SnapshotCheckpoint(snapshot)
	if err != nil {
		return nil, err
	}
	if actual != checkpoint {
		return nil, persistence.ErrBindingChanged
	}
	return persistence.MarshalSnapshot(snapshot)
}
func authorityRevision(ctx context.Context, documents persistence.Documents, revision persistence.Revision) error {
	view, err := documents.Read(ctx, persistence.Query{Namespace: "wuu.state.authority.v1", Keys: []string{}, AtRevision: &revision})
	if err != nil {
		return err
	}
	if view.Revision != revision {
		return persistence.ErrBindingChanged
	}
	return nil
}
func (a *StateAuthority) commit(ctx context.Context, client *persistence.Client, record *authorityRecord, tx persistence.Transaction, encoded json.RawMessage, caller string) (persistence.Receipt, error) {
	if encoded != nil {
		match := authorityWireMatch{remaining: encoded}
		if err := persistence.WriteTransactionJSON(&match, tx); err != nil || len(match.remaining) != 0 {
			encoded = nil
		}
	}
	commit := func() (persistence.Receipt, error) {
		if encoded != nil {
			return client.CommitEncoded(ctx, encoded)
		}
		return client.Commit(ctx, tx)
	}
	if err := authorityRevision(ctx, client, record.Checkpoint.Revision); err != nil {
		return persistence.Receipt{}, err
	}
	if tx.ExpectedRevision != record.Checkpoint.Revision {
		// CAS cannot apply new intent at an old revision. Only an original durable
		// receipt can succeed, and that success does not advance the host head.
		receipt, err := commit()
		if err != nil {
			return receipt, err
		}
		if receipt.ID != tx.ID || receipt.Revision > record.Checkpoint.Revision {
			return persistence.Receipt{}, persistence.ErrCorrupt
		}
		return receipt, nil
	}
	after, err := persistence.NextCheckpoint(context.WithValue(ctx, authorityStewardKey{}, a), client, record.Checkpoint, tx)
	if err != nil {
		return persistence.Receipt{}, err
	}
	record.Pending = &authorityPending{Caller: caller, Transaction: tx, Before: record.Checkpoint, After: after}
	if err := a.writeRecord(record); err != nil {
		return persistence.Receipt{}, err
	}
	receipt, err := commit()
	if err != nil {
		if errors.Is(err, persistence.ErrConflict) || errors.Is(err, persistence.ErrOperationConflict) || errors.Is(err, persistence.ErrInvalid) || authorityKnownRejection(err) {
			record.Pending = nil
			err = errors.Join(err, a.writeRecord(record))
		}
		return persistence.Receipt{}, err
	}
	if receipt.ID != tx.ID || receipt.Revision != after.Revision {
		return persistence.Receipt{}, fmt.Errorf("%w: commit receipt disagrees with pending intent", persistence.ErrCorrupt)
	}
	record.Checkpoint = after
	record.Pending = nil
	if err := a.writeRecord(record); err != nil {
		return persistence.Receipt{}, err
	}
	return receipt, nil
}
func (a *StateAuthority) recoverPending(ctx context.Context, client *persistence.Client, record *authorityRecord) error {
	pending := record.Pending
	if pending == nil {
		return nil
	}
	actual, err := a.captureCheckpoint(ctx, client)
	if err != nil {
		return err
	}
	if actual == pending.Before {
		predicted, err := persistence.NextCheckpoint(context.WithValue(ctx, authorityStewardKey{}, a), client, pending.Before, pending.Transaction)
		if err != nil {
			return err
		}
		if predicted != pending.After {
			return persistence.ErrCorrupt
		}
		recovery := context.WithValue(ctx, authorityRecoveryKey{}, pending)
		if pending.Method == "claim-namespaces" {
			var request struct {
				Namespaces []string `json:"namespaces"`
			}
			if decodeAuthorityRequest(pending.Params, &request) != nil {
				return persistence.ErrCorrupt
			}
			if err := client.ClaimNamespaces(recovery, request.Namespaces); err != nil {
				return err
			}
		} else {
			receipt, err := client.Commit(recovery, pending.Transaction)
			if err != nil {
				return err
			}
			if receipt.ID != pending.Transaction.ID || receipt.Revision != pending.After.Revision {
				return persistence.ErrCorrupt
			}
		}
		actual, err = a.captureCheckpoint(ctx, client)
		if err != nil {
			return err
		}
	}
	if actual != pending.After {
		return fmt.Errorf("%w: selected source does not contain the durable pending checkpoint", persistence.ErrMigrationRequired)
	}
	record.Checkpoint = pending.After
	record.Pending = nil
	return a.writeRecord(record)
}

func (a *StateAuthority) writeRecord(record *authorityRecord) error {
	if err := securefs.WriteFileAtomicFunc(filepath.Join(a.dir, "head.json"), func(w io.Writer) error {
		return writeAuthorityEnvelope(w, record)
	}); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(a.dir, "initialized")); errors.Is(err, os.ErrNotExist) {
		if err := securefs.WriteFileAtomic(filepath.Join(a.dir, "initialized"), []byte("1\n")); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return a.syncDirectory()
}

func (a *StateAuthority) syncDirectory() error {
	for _, dir := range []string{a.dir, filepath.Dir(a.dir)} {
		file, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = errors.Join(file.Sync(), file.Close())
		if err != nil {
			return fmt.Errorf("authority metadata published; directory durability failed: %w", err)
		}
	}
	return nil
}

// A pending outcome is inspected, never replayed, during candidate preparation.
func (a *StateAuthority) preparedAuthorityCheckpoint(ctx context.Context, client *persistence.Client, record *authorityRecord) (persistence.Checkpoint, error) {
	actual, err := a.captureCheckpoint(ctx, client)
	if err != nil {
		return persistence.Checkpoint{}, err
	}
	if record == nil {
		return actual, nil
	}
	if actual == record.Checkpoint || record.Pending != nil && actual == record.Pending.After {
		return actual, nil
	}
	return persistence.Checkpoint{}, fmt.Errorf("%w: candidate checkpoint differs from the durable authority", persistence.ErrMigrationRequired)
}
func (a *StateAuthority) markPrepared(checkpoint persistence.Checkpoint, epoch string) {
	a.mu.Lock()
	a.prepared = true
	a.preparedCheckpoint = checkpoint
	a.preparedEpoch = epoch
	a.mu.Unlock()
}
func (a *StateAuthority) prepare(ctx context.Context, client *persistence.Client) (persistence.Revision, error) {
	if _, err := a.selectAuthority(ctx, client); err != nil {
		return 0, err
	}
	if a.providerOwned() {
		return a.prepareProviderAuthority(ctx, client)
	}
	if err := a.requireHostJournalBinding(); err != nil {
		return 0, err
	}
	if a.exportCapabilityErr != nil {
		return 0, a.exportCapabilityErr
	}
	lock, err := storelock.AcquireUntilCanceled(ctx, a.dir)
	if err != nil {
		return 0, err
	}
	record, err := a.readRecord()
	if err != nil {
		lock.Release()
		return 0, err
	}
	members, err := a.liveMembers()
	if err != nil {
		lock.Release()
		return 0, err
	}
	if len(members) == 0 {
		checkpoint, err := a.preparedAuthorityCheckpoint(ctx, client, record)
		if err != nil {
			lock.Release()
			return 0, err
		}
		epoch := ""
		if record != nil {
			epoch = record.Epoch
		}
		a.markPrepared(checkpoint, epoch)
		lock.Release()
		return checkpoint.Revision, nil
	}
	if record == nil {
		lock.Release()
		return 0, persistence.ErrCorrupt
	}
	lock.Release()
	proofEpoch, err := a.probeLiveAuthority(ctx, client, members)
	if err != nil {
		return 0, err
	}
	lock, err = storelock.AcquireUntilCanceled(ctx, a.dir)
	if err != nil {
		return 0, err
	}
	defer lock.Release()
	record, err = a.readRecord()
	if err != nil {
		return 0, err
	}
	if record == nil {
		return 0, persistence.ErrCorrupt
	}
	if record.Epoch != proofEpoch {
		return 0, fmt.Errorf("%w: authority changed during candidate probe", persistence.ErrBindingChanged)
	}
	checkpoint, err := a.preparedAuthorityCheckpoint(ctx, client, record)
	if err != nil {
		return 0, err
	}
	a.markPrepared(checkpoint, record.Epoch)
	return checkpoint.Revision, nil
}
func (a *StateAuthority) activate(ctx context.Context, client *persistence.Client) (persistence.Revision, error) {
	if _, err := a.selectAuthority(ctx, client); err != nil {
		return 0, err
	}
	if a.providerOwned() {
		return a.activateProviderAuthority(ctx, client)
	}
	if err := a.requireHostJournalBinding(); err != nil {
		return 0, err
	}
	lock, err := storelock.AcquireUntilCanceled(ctx, a.dir)
	if err != nil {
		return 0, err
	}
	defer lock.Release()
	if err := a.requireHostJournalBinding(); err != nil {
		return 0, err
	}
	record, err := a.readRecord()
	if err != nil {
		return 0, err
	}
	members, err := a.liveMembers()
	if err != nil {
		return 0, err
	}
	a.mu.Lock()
	expected, epoch := a.preparedCheckpoint, a.preparedEpoch
	a.mu.Unlock()
	if record == nil {
		if len(members) != 0 {
			return 0, persistence.ErrCorrupt
		}
		checkpoint, err := a.captureCheckpoint(ctx, client)
		if err != nil {
			return 0, err
		}
		if checkpoint != expected {
			return 0, persistence.ErrBindingChanged
		}
		record = &authorityRecord{Version: 1, Epoch: a.id, Checkpoint: checkpoint}
		if err := a.writeRecord(record); err != nil {
			return 0, err
		}
		// The initial anchor makes a lost first business commit distinguishable
		// from a different empty backend. No candidate data is written in Prepare.
		raw, _ := json.Marshal(a.id)
		_, err = a.commit(ctx, client, record, persistence.Transaction{ID: "state-authority-anchor:" + a.id, ExpectedRevision: checkpoint.Revision, Writes: []persistence.Write{{Namespace: "wuu.state.authority.v1", Key: "anchor", Value: raw}}}, nil, "")
		if err != nil {
			return 0, err
		}
	} else {
		if len(members) != 0 && record.Epoch != epoch {
			// Another candidate can become the live owner after Prepare. Prove
			// shared storage against that owner without holding its probe lock.
			lock.Release()
			proofEpoch, err := a.probeLiveAuthority(ctx, client, members)
			if err != nil {
				return 0, err
			}
			lock, err = storelock.AcquireUntilCanceled(ctx, a.dir)
			if err != nil {
				return 0, err
			}
			defer lock.Release()
			record, err = a.readRecord()
			if err != nil {
				return 0, err
			}
			if record == nil {
				return 0, persistence.ErrCorrupt
			}
			// Even if every member has exited, a second epoch change invalidates
			// this proof before the candidate can take over the durable head.
			if record.Epoch != proofEpoch {
				return 0, fmt.Errorf("%w: authority changed during candidate probe", persistence.ErrBindingChanged)
			}
			members, err = a.liveMembers()
			if err != nil {
				return 0, err
			}
		}
		if err := a.recoverPending(ctx, client, record); err != nil {
			return 0, err
		}
		if err := a.verifyCheckpoint(ctx, client, record.Checkpoint); err != nil {
			return 0, err
		}
		if len(members) == 0 {
			record.Epoch = a.id
			if err := a.writeRecord(record); err != nil {
				return 0, err
			}
		}
	}
	return record.Checkpoint.Revision, a.register()
}
func (a *StateAuthority) probeLiveAuthority(ctx context.Context, client *persistence.Client, members []string) (string, error) {
	var probeErrors []error
	for _, owner := range members {
		epoch, err := a.requestProbe(ctx, client, owner)
		if err == nil {
			return epoch, nil
		}
		if errors.Is(err, persistence.ErrMigrationRequired) || ctx.Err() != nil {
			return "", err
		}
		probeErrors = append(probeErrors, err)
	}
	return "", errors.Join(probeErrors...)
}
func (a *StateAuthority) liveMembers() ([]string, error) {
	root := filepath.Join(a.dir, "members")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var live []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		lock, free, err := storelock.TryAcquire(path)
		if err != nil {
			return nil, err
		}
		if free {
			lock.Release()
			continue
		}
		live = append(live, entry.Name())
	}
	return live, nil
}
func (a *StateAuthority) register() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := watcher.Add(a.dir); err != nil {
		watcher.Close()
		return err
	}
	member, free, err := storelock.TryAcquire(filepath.Join(a.dir, "members", a.id))
	if err != nil || !free {
		watcher.Close()
		if err == nil {
			err = errors.New("authority membership is already owned")
		}
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if a.closed || a.closing {
		a.mu.Unlock()
		cancel()
		watcher.Close()
		member.Release()
		return persistence.ErrClosed
	}
	a.member = member
	a.watcher = watcher
	a.cancel = cancel
	a.done = make(chan struct{})
	a.admitted = true
	a.mu.Unlock()
	go a.watchRequests(ctx)
	return nil
}
func authorityJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return securefs.WriteFileAtomic(path, raw)
}
func authorityReadJSON(path string, value any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}
func (a *StateAuthority) requestPath(prefix, id string) string {
	return filepath.Join(a.dir, prefix+"-"+id+".json")
}
func (a *StateAuthority) requestProbe(ctx context.Context, client *persistence.Client, owner string) (string, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return "", err
	}
	defer watcher.Close()
	if err := watcher.Add(a.dir); err != nil {
		return "", err
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	requestID := hex.EncodeToString(random[:])
	requestDir := filepath.Join(a.dir, "requests", requestID)
	lease, free, err := storelock.TryAcquire(requestDir)
	if err != nil {
		return "", err
	}
	if !free {
		return "", errors.New("authority admission is already in progress")
	}
	defer lease.Release()
	defer func() {
		for _, prefix := range []string{"request", "response", "ack"} {
			_ = os.Remove(a.requestPath(prefix, requestID))
		}
	}()
	if err := authorityJSON(a.requestPath("request", requestID), authorityRequest{ID: requestID, Owner: owner}); err != nil {
		return "", err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	acknowledged := false
	var verificationErr error
	for {
		var response authorityResponse
		err := authorityReadJSON(a.requestPath("response", requestID), &response)
		if err == nil {
			if response.Done {
				if verificationErr != nil {
					return "", verificationErr
				}
				if response.Error != "" {
					return "", errors.New(response.Error)
				}
				return response.Epoch, nil
			}
			if !acknowledged {
				verificationErr = a.verifyCheckpoint(ctx, client, response.Checkpoint)
				if err := authorityJSON(a.requestPath("ack", requestID), struct {
					Accepted bool `json:"accepted"`
				}{verificationErr == nil}); err != nil {
					return "", err
				}
				acknowledged = true
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case _, ok := <-watcher.Events:
			if !ok {
				return "", errors.New("authority admission watcher closed")
			}
		case err := <-watcher.Errors:
			if err != nil {
				return "", err
			}
		case <-ticker.C:
			lock, free, err := storelock.TryAcquire(filepath.Join(a.dir, "members", owner))
			if err != nil {
				return "", err
			}
			if free {
				lock.Release()
				return "", fmt.Errorf("%w: retained authority exited during admission; retry against its durable head", persistence.ErrClosed)
			}
		}
	}
}
func (a *StateAuthority) watchRequests(ctx context.Context) {
	defer func() {
		a.watcher.Close()
		a.mu.Lock()
		closing := a.closing
		if !closing {
			a.closed = true
			a.admitted = false
		}
		a.mu.Unlock()
		if !closing {
			a.member.Release()
		}
		close(a.done)
	}()
	for {
		entries, err := os.ReadDir(a.dir)
		if err == nil {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "request-") && strings.HasSuffix(entry.Name(), ".json") {
					var request authorityRequest
					if authorityReadJSON(filepath.Join(a.dir, entry.Name()), &request) == nil && request.Owner == a.id {
						a.serveProbe(ctx, request)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-a.watcher.Events:
			if !ok {
				return
			}
		case <-a.watcher.Errors:
		}
	}
}
func (a *StateAuthority) serveProbe(ctx context.Context, request authorityRequest) {
	var existing authorityResponse
	if authorityReadJSON(a.requestPath("response", request.ID), &existing) == nil && existing.Done {
		return
	}
	if len(request.ID) != 64 {
		return
	}
	if _, err := hex.DecodeString(request.ID); err != nil {
		return
	}
	requestDir := filepath.Join(a.dir, "requests", request.ID)
	lease, free, err := storelock.TryAcquire(requestDir)
	if err != nil {
		return
	}
	if free {
		lease.Release()
		return
	}
	lock, err := storelock.AcquireUntilCanceled(ctx, a.dir)
	if err != nil {
		return
	}
	defer lock.Release()
	record, err := a.readRecord()
	if err != nil || record == nil {
		if err == nil {
			err = persistence.ErrCorrupt
		}
		_ = authorityJSON(a.requestPath("response", request.ID), authorityResponse{Done: true, Error: err.Error()})
		return
	}
	locked := &authorityLocked{dir: a.dir, record: record}
	callCtx := context.WithValue(ctx, authorityLockKey{}, locked)
	view, err := a.documents.Read(callCtx, persistence.Query{Namespace: "wuu.state.authority.v1", Keys: []string{}})
	if err != nil {
		_ = authorityJSON(a.requestPath("response", request.ID), authorityResponse{Done: true, Error: err.Error()})
		return
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return
	}
	value, _ := json.Marshal(hex.EncodeToString(random[:]))
	tx := persistence.Transaction{ID: "state-admission-probe:" + request.ID, ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: "wuu.state.authority.v1", Key: request.ID, Value: value}}}
	// Retain cleanup ownership before dispatch: a lost reply can hide a
	// committed probe, and the durable head must resolve that outcome on Close.
	a.mu.Lock()
	if a.probeCleanups == nil {
		a.probeCleanups = make(map[string]struct{})
	}
	a.probeCleanups[request.ID] = struct{}{}
	a.mu.Unlock()
	if _, err := a.documents.Commit(callCtx, tx); err != nil {
		_ = authorityJSON(a.requestPath("response", request.ID), authorityResponse{Done: true, Error: err.Error()})
		return
	}
	probeErr := authorityJSON(a.requestPath("response", request.ID), authorityResponse{Epoch: record.Epoch, Checkpoint: record.Checkpoint})
	if probeErr == nil {
		probeErr = a.waitAcknowledgement(ctx, request)
	}
	// Cancellation cannot skip the durability barrier or cleanup of a probe that
	// was already committed. Runtime teardown keeps this provider alive until done.
	cleanupCtx := context.WithValue(context.WithoutCancel(ctx), authorityLockKey{}, locked)
	_, cleanupErr := a.documents.Commit(cleanupCtx, persistence.Transaction{ID: "state-admission-cleanup:" + request.ID, ExpectedRevision: record.Checkpoint.Revision, Writes: []persistence.Write{{Namespace: "wuu.state.authority.v1", Key: request.ID, Delete: true}}})
	if cleanupErr == nil {
		a.mu.Lock()
		delete(a.probeCleanups, request.ID)
		a.mu.Unlock()
	}
	response := authorityResponse{Epoch: record.Epoch, Checkpoint: record.Checkpoint, Done: true}
	if err := errors.Join(probeErr, cleanupErr); err != nil {
		response.Error = err.Error()
	}
	_ = authorityJSON(a.requestPath("response", request.ID), response)
}
func (a *StateAuthority) waitAcknowledgement(ctx context.Context, request authorityRequest) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := watcher.Add(a.dir); err != nil {
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var ack struct {
			Accepted bool `json:"accepted"`
		}
		if err := authorityReadJSON(a.requestPath("ack", request.ID), &ack); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		lease, free, err := storelock.TryAcquire(filepath.Join(a.dir, "requests", request.ID))
		if err != nil {
			return err
		}
		if free {
			lease.Release()
			return errors.New("authority candidate exited during admission")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-watcher.Events:
			if !ok {
				return errors.New("authority acknowledgement watcher closed")
			}
		case err := <-watcher.Errors:
			if err != nil {
				return err
			}
		case <-ticker.C:
		}
	}
}
func authorityServiceError(err error) error {
	var hostErr *pluginhost.HostServiceError
	if errors.As(err, &hostErr) {
		return hostErr
	}
	code := "state_io_error"
	switch {
	case errors.Is(err, persistence.ErrConflict):
		code = "state_conflict"
	case errors.Is(err, persistence.ErrOperationConflict):
		code = "state_operation_conflict"
	case errors.Is(err, persistence.ErrMigrationRequired):
		code = "state_migration_required"
	case errors.Is(err, persistence.ErrBindingChanged):
		code = "state_binding_changed"
	case errors.Is(err, persistence.ErrCorrupt):
		code = "state_corrupt"
	case errors.Is(err, persistence.ErrInvalid):
		code = "invalid_request"
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		code = "request_cancelled"
	}
	return &pluginhost.HostServiceError{Code: code, Message: err.Error()}
}

// Providers may maintain their committed checkpoint incrementally. This public
// capability is shared by native and external providers; full export remains
// available for explicit migration/audit and providers without the capability.
func (a *StateAuthority) captureCheckpoint(ctx context.Context, client *persistence.Client) (persistence.Checkpoint, error) {
	ctx = context.WithValue(ctx, authorityStewardKey{}, a)
	if a.checkpointCapability {
		return client.Checkpoint(ctx)
	}
	return persistence.CaptureCheckpoint(ctx, client)
}

func (a *StateAuthority) verifyCheckpoint(ctx context.Context, client *persistence.Client, expected persistence.Checkpoint) error {
	actual, err := a.captureCheckpoint(ctx, client)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("%w: candidate checkpoint differs from the durable authority", persistence.ErrMigrationRequired)
	}
	return nil
}

func authorityKnownRejection(err error) bool {
	var service *pluginhost.HostServiceError
	return errors.As(err, &service) && (service.Code == "namespace_ownership" || service.Code == "inspection_active" || service.Code == "inspection_conflict" || service.Code == "permission_denied" || service.Code == "method_not_found")
}
func (a *StateAuthority) claimNamespaces(ctx context.Context, client *persistence.Client, record *authorityRecord, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var request struct {
		Namespaces []string `json:"namespaces"`
	}
	if err := decodeAuthorityRequest(params.Params, &request); err != nil {
		return nil, err
	}
	tx, err := persistence.PrepareNamespaceClaim(context.WithValue(ctx, authorityStewardKey{}, a), client, params.Caller, request.Namespaces)
	if err != nil {
		return nil, err
	}
	if tx.ExpectedRevision != record.Checkpoint.Revision {
		return nil, persistence.ErrBindingChanged
	}
	if len(tx.Writes) == 0 {
		return a.invokeSelectedProvider(ctx, params)
	}
	after, err := persistence.NextCheckpoint(context.WithValue(ctx, authorityStewardKey{}, a), client, record.Checkpoint, tx)
	if err != nil {
		return nil, err
	}
	canonical, _ := json.Marshal(request)
	record.Pending = &authorityPending{Caller: params.Caller, Method: "claim-namespaces", Params: canonical, Transaction: tx, Before: record.Checkpoint, After: after}
	if err = a.writeRecord(record); err != nil {
		return nil, err
	}
	result, err := a.invokeSelectedProvider(ctx, params)
	if err != nil {
		if authorityKnownRejection(err) || errors.Is(err, persistence.ErrInvalid) || errors.Is(err, persistence.ErrConflict) {
			record.Pending = nil
			err = errors.Join(err, a.writeRecord(record))
		}
		return nil, err
	}
	if err = a.verifyCheckpoint(ctx, client, after); err != nil {
		return nil, err
	}
	record.Checkpoint = after
	record.Pending = nil
	return result, a.writeRecord(record)
}
