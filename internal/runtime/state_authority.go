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

// StateAuthority owns the host's durable head independently of replaceable
// storage. One instance decorates one generation's selected invoker. Registry
// dispatch still owns permissions, provider identity and callback lifetimes.
type StateAuthority struct {
	dir, id                             string
	mu                                  sync.Mutex
	preflightMu                         sync.Mutex
	provider                            pluginhost.ServiceInvoker
	documents                           persistence.Documents
	prepared, admitted, closing, closed bool
	preparedCheckpoint                  persistence.Checkpoint
	preparedEpoch                       string
	member                              *storelock.Lock
	watcher                             *fsnotify.Watcher
	cancel                              context.CancelFunc
	done                                chan struct{}
}

type authorityPending struct {
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
		a.mu.Unlock()
		return nil
	}
	a.closing = true
	cancel, done := a.cancel, a.done
	if cancel == nil {
		a.closed = true
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *StateAuthority) rawClient(params pluginhost.ServiceInvokeParams) *persistence.Client {
	return persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		invoke := params
		invoke.Method = method
		invoke.Params = raw
		// Only host coordination may reuse a held lock. It is never inherited by a
		// provider callback, which could otherwise bypass serialization recursively.
		ctx = context.WithValue(ctx, authorityLockKey{}, nil)
		ctx = context.WithValue(ctx, authorityPreflightKey{}, nil)
		ctx = context.WithValue(ctx, authorityActivationKey{}, nil)
		ctx = context.WithValue(ctx, authorityRawKey{}, a.dir)
		return a.provider.InvokeService(ctx, invoke)
	})
}
func (a *StateAuthority) invoke(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if ctx.Value(authorityRawKey{}) == a.dir {
		return nil, errors.New("state provider recursively called its own authority")
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
	if params.Method == "export" || params.Method == "compact" {
		if err := decodeAuthorityRequest(params.Params, new(struct{})); err != nil {
			return nil, err
		}
	}
	locked, _ := ctx.Value(authorityLockKey{}).(*authorityLocked)
	a.mu.Lock()
	allowed := a.admitted && !a.closed && (!a.closing || locked != nil)
	lazyAdmission := !a.admitted && !a.closed && !a.closing && a.prepared && a.documents != nil && !pluginhost.IsServicePreparation(ctx)
	readOnly := !a.admitted && !a.closed && !a.closing && (params.Method == "read" || params.Method == "export")
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
	if locked != nil && locked.dir == a.dir {
		return a.dispatch(ctx, client, locked, params)
	}
	lock, err := storelock.AcquireContext(ctx, a.dir)
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
	if _, err := a.prepare(ctx, client); err != nil {
		return nil, err
	}
	lock, err := storelock.AcquireContext(ctx, a.dir)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	record, err := a.readRecord()
	if err != nil {
		return nil, err
	}
	checkpoint, err := preparedAuthorityCheckpoint(ctx, client, record)
	if err != nil {
		return nil, err
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
	if err := a.recoverPending(ctx, client, locked.record); err != nil {
		return nil, err
	}
	head := locked.record.Checkpoint
	switch params.Method {
	case "read", "export":
		return readAuthority(ctx, client, head, params)
	case "commit":
		var tx persistence.Transaction
		if err := decodeAuthorityRequest(params.Params, &tx); err != nil {
			return nil, err
		}
		receipt, err := a.commit(ctx, client, locked.record, tx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(receipt)
	case "compact":
		if err := persistence.VerifyCheckpoint(ctx, client, head); err != nil {
			return nil, err
		}
		result, err := client.Compact(ctx)
		if err != nil {
			return nil, err
		}
		if result.Revision != head.Revision {
			return nil, persistence.ErrBindingChanged
		}
		if err := persistence.VerifyCheckpoint(ctx, client, head); err != nil {
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
	return json.Marshal(snapshot)
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
func (a *StateAuthority) commit(ctx context.Context, client *persistence.Client, record *authorityRecord, tx persistence.Transaction) (persistence.Receipt, error) {
	if err := authorityRevision(ctx, client, record.Checkpoint.Revision); err != nil {
		return persistence.Receipt{}, err
	}
	if tx.ExpectedRevision != record.Checkpoint.Revision {
		// CAS cannot apply new intent at an old revision. Only an original durable
		// receipt can succeed, and that success does not advance the host head.
		receipt, err := client.Commit(ctx, tx)
		if err != nil {
			return receipt, err
		}
		if receipt.ID != tx.ID || receipt.Revision > record.Checkpoint.Revision {
			return persistence.Receipt{}, persistence.ErrCorrupt
		}
		return receipt, nil
	}
	after, err := persistence.NextCheckpoint(ctx, client, record.Checkpoint, tx)
	if err != nil {
		return persistence.Receipt{}, err
	}
	record.Pending = &authorityPending{Transaction: tx, Before: record.Checkpoint, After: after}
	if err := a.writeRecord(record); err != nil {
		return persistence.Receipt{}, err
	}
	receipt, err := client.Commit(ctx, tx)
	if err != nil {
		if errors.Is(err, persistence.ErrConflict) || errors.Is(err, persistence.ErrOperationConflict) || errors.Is(err, persistence.ErrInvalid) {
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
	actual, err := persistence.CaptureCheckpoint(ctx, client)
	if err != nil {
		return err
	}
	if actual == pending.Before {
		predicted, err := persistence.NextCheckpoint(ctx, client, pending.Before, pending.Transaction)
		if err != nil {
			return err
		}
		if predicted != pending.After {
			return persistence.ErrCorrupt
		}
		receipt, err := client.Commit(ctx, pending.Transaction)
		if err != nil {
			return err
		}
		if receipt.ID != pending.Transaction.ID || receipt.Revision != pending.After.Revision {
			return persistence.ErrCorrupt
		}
		actual, err = persistence.CaptureCheckpoint(ctx, client)
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

func (a *StateAuthority) readRecord() (*authorityRecord, error) {
	raw, err := os.ReadFile(filepath.Join(a.dir, "head.json"))
	if errors.Is(err, os.ErrNotExist) {
		if _, markerErr := os.Stat(filepath.Join(a.dir, "initialized")); markerErr == nil {
			return nil, fmt.Errorf("%w: authority checkpoint was removed", persistence.ErrCorrupt)
		} else if !errors.Is(markerErr, os.ErrNotExist) {
			return nil, markerErr
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var envelope authorityEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, persistence.ErrCorrupt
	}
	hash := sha256.Sum256(envelope.Payload)
	if hex.EncodeToString(hash[:]) != envelope.Digest {
		return nil, fmt.Errorf("%w: authority head checksum", persistence.ErrCorrupt)
	}
	var record authorityRecord
	if err := json.Unmarshal(envelope.Payload, &record); err != nil || record.Version != 1 {
		return nil, persistence.ErrCorrupt
	}
	for _, digest := range []string{record.Checkpoint.DocumentsDigest, record.Checkpoint.ReceiptsDigest} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return nil, persistence.ErrCorrupt
		}
	}
	if record.Checkpoint.Version != 1 {
		return nil, persistence.ErrCorrupt
	}
	if pending := record.Pending; pending != nil && (pending.Before != record.Checkpoint || pending.After.Revision != pending.Before.Revision+1 || pending.Transaction.ExpectedRevision != pending.Before.Revision) {
		return nil, persistence.ErrCorrupt
	}
	return &record, nil
}
func (a *StateAuthority) writeRecord(record *authorityRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(payload)
	raw, err := json.Marshal(authorityEnvelope{Payload: payload, Digest: hex.EncodeToString(hash[:])})
	if err != nil {
		return err
	}
	if err := securefs.WriteFileAtomic(filepath.Join(a.dir, "head.json"), raw); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(a.dir, "initialized")); errors.Is(err, os.ErrNotExist) {
		if err := securefs.WriteFileAtomic(filepath.Join(a.dir, "initialized"), []byte("1\n")); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for _, dir := range []string{a.dir, filepath.Dir(a.dir)} {
		file, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = errors.Join(file.Sync(), file.Close())
		if err != nil {
			return fmt.Errorf("authority head published; directory durability failed: %w", err)
		}
	}
	return nil
}

// A pending outcome is inspected, never replayed, during candidate preparation.
func preparedAuthorityCheckpoint(ctx context.Context, client *persistence.Client, record *authorityRecord) (persistence.Checkpoint, error) {
	actual, err := persistence.CaptureCheckpoint(ctx, client)
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
	lock, err := storelock.AcquireContext(ctx, a.dir)
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
		checkpoint, err := preparedAuthorityCheckpoint(ctx, client, record)
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
	var proofEpoch string
	var probeErrors []error
	for _, owner := range members {
		epoch, err := a.requestProbe(ctx, client, owner)
		if err == nil {
			proofEpoch = epoch
			break
		}
		if errors.Is(err, persistence.ErrMigrationRequired) || ctx.Err() != nil {
			return 0, err
		}
		probeErrors = append(probeErrors, err)
	}
	if proofEpoch == "" {
		return 0, errors.Join(probeErrors...)
	}
	lock, err = storelock.AcquireContext(ctx, a.dir)
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
	checkpoint, err := preparedAuthorityCheckpoint(ctx, client, record)
	if err != nil {
		return 0, err
	}
	a.markPrepared(checkpoint, record.Epoch)
	return checkpoint.Revision, nil
}
func (a *StateAuthority) activate(ctx context.Context, client *persistence.Client) (persistence.Revision, error) {
	lock, err := storelock.AcquireContext(ctx, a.dir)
	if err != nil {
		return 0, err
	}
	defer lock.Release()
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
		checkpoint, err := persistence.CaptureCheckpoint(ctx, client)
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
		_, err = a.commit(ctx, client, record, persistence.Transaction{ID: "state-authority-anchor:" + a.id, ExpectedRevision: checkpoint.Revision, Writes: []persistence.Write{{Namespace: "wuu.state.authority.v1", Key: "anchor", Value: raw}}})
		if err != nil {
			return 0, err
		}
	} else {
		if len(members) != 0 && record.Epoch != epoch {
			return 0, fmt.Errorf("%w: live authority changed after candidate preparation", persistence.ErrBindingChanged)
		}
		if err := a.recoverPending(ctx, client, record); err != nil {
			return 0, err
		}
		if err := persistence.VerifyCheckpoint(ctx, client, record.Checkpoint); err != nil {
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
				verificationErr = persistence.VerifyCheckpoint(ctx, client, response.Checkpoint)
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
		a.member.Release()
		a.mu.Lock()
		a.closed = true
		a.admitted = false
		a.mu.Unlock()
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
	lock, err := storelock.AcquireContext(ctx, a.dir)
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
