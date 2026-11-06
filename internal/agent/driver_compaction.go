package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const driverCompactionNamespace = "wuu.driver_compactions.v1"

// DriverCompactionStore is the host-owned proposal archive over the selected
// document provider. Its captured binding stays alive until the turn drains.
type DriverCompactionStore struct {
	Documents persistence.Documents
	SessionID string
}

type driverCompactionRecord struct {
	ID, SessionID, DriverID, DriverVersion, ExecutionID string
	ModelKey, Status                                    string
	Attempt, SourceHead                                 int
	Budget                                              pluginprotocol.DriverCompactBudget
	Source, Messages                                    []providers.ChatMessage
	Failure                                             *pluginprotocol.DriverFailure
	ParentOperationID                                   string
}

func (s *DriverCompactionStore) update(ctx context.Context, driver, id string, change func(*driverCompactionRecord) error) (driverCompactionRecord, error) {
	var record driverCompactionRecord
	key := base64.RawURLEncoding.EncodeToString([]byte(s.SessionID)) + "/" + base64.RawURLEncoding.EncodeToString([]byte(driver)) + "/" + base64.RawURLEncoding.EncodeToString([]byte(id))
	err := persistence.Update(ctx, s.Documents, func(tx *persistence.Tx) error {
		record = driverCompactionRecord{}
		_, err := tx.Get(driverCompactionNamespace, key, &record)
		if err != nil {
			return err
		}
		if change == nil {
			return nil
		}
		if err := change(&record); err != nil {
			return err
		}
		return tx.Put(driverCompactionNamespace, key, record)
	})
	return record, err
}

func compactionResult(record driverCompactionRecord) pluginprotocol.DriverCompactionResult {
	return pluginprotocol.DriverCompactionResult{ID: record.ID, Status: record.Status, Attempt: record.Attempt, SourceHead: record.SourceHead, Failure: record.Failure}
}

func (g *streamTurnGateway) compactionModelKey() string {
	r := g.modelRunner()
	return fmt.Sprintf("%s/%s/%s/%d", r.ProviderName, r.Model, r.APIModel, g.modelRevision())
}

func (g *streamTurnGateway) compactionStore() (*DriverCompactionStore, error) {
	store := g.runner.DriverCompactions
	if store == nil || store.Documents == nil {
		return nil, errors.New("durable compaction proposal storage is unavailable")
	}
	if store.SessionID != loopdriver.ExecutionContextFromContext(g.ctx).SessionID {
		return nil, errors.New("compaction session scope does not match execution")
	}
	return store, nil
}

func (g *streamTurnGateway) StartCompaction(ctx context.Context, params pluginprotocol.DriverCompactionParams) (pluginprotocol.DriverCompactionResult, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	if strings.TrimSpace(params.ID) == "" || len(params.ID) > 512 || params.Budget.Window != nil {
		return pluginprotocol.DriverCompactionResult{}, errors.New("invalid compaction proposal")
	}
	store, err := g.compactionStore()
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	execution := loopdriver.ExecutionContextFromContext(g.ctx)
	if g.compactions == nil {
		g.compactions = make(map[string]chan struct{})
	}
	previous, err := store.update(ctx, g.descriptor.ID, params.ID, nil)
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	if params.Resume && previous.ID == "" {
		return pluginprotocol.DriverCompactionResult{ID: params.ID, Status: "missing"}, nil
	}
	if previous.ID != "" && !params.Resume && !reflect.DeepEqual(previous.Budget, params.Budget) {
		return pluginprotocol.DriverCompactionResult{}, errors.New("compaction ID already has different intent")
	}
	if previous.ID != "" && previous.Status != "running" {
		return compactionResult(previous), nil
	}
	if _, running := g.compactions[params.ID]; running {
		return compactionResult(previous), nil
	}
	if previous.ID == "" {
		if params.Revision != g.revision || len(g.streamedBatches) != 0 {
			return pluginprotocol.DriverCompactionResult{}, errors.New("compaction source revision is stale or tools are still streaming")
		}
		if g.runner.ArchiveHistory == nil || g.runner.CommitFreshContext == nil {
			return pluginprotocol.DriverCompactionResult{}, errors.New("durable context replacement is unavailable")
		}
		if err := g.archivePending(ctx); err != nil {
			return pluginprotocol.DriverCompactionResult{}, err
		}
	}
	record, err := store.update(ctx, g.descriptor.ID, params.ID, func(record *driverCompactionRecord) error {
		if record.ID == "" {
			*record = driverCompactionRecord{ID: params.ID, SessionID: execution.SessionID, DriverID: g.descriptor.ID, DriverVersion: g.descriptor.Version, Source: providers.CloneChatMessages(g.activeHistory), SourceHead: g.archiveHead, ModelKey: g.compactionModelKey(), Budget: params.Budget}
		} else if record.DriverVersion != g.descriptor.Version || record.ModelKey != g.compactionModelKey() || !contextHasPrefix(g.activeHistory, record.Source) {
			record.Status = "stale"
			return nil
		} else if record.Status != "running" {
			return nil
		}
		record.ExecutionID, record.Status, record.Attempt = execution.ExecutionID, "running", record.Attempt+1
		return nil
	})
	if err != nil || record.Status != "running" {
		return compactionResult(record), err
	}
	if err := ctx.Err(); err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	// Clone configuration (never mutexes/usage), retain the selected model and
	// provider generation, and do not let the proposal mutate live turn state.
	snapshot := &streamTurnGateway{runner: cloneRunnerModelConfiguration(g.modelRunner()), client: g.client, onEvent: g.onEvent, archiveHead: record.SourceHead, lastModelOperationID: g.lastModelOperationID}
	workCtx, cancel := context.WithCancel(g.ctx)
	var stop func() bool
	if registry := snapshot.runner.CompactionRegistry; registry != nil {
		selected := registry.Resolve(nil)
		if selected != nil {
			snapshot.runner.CompactionRegistry = NewCompactionRegistry()
			snapshot.runner.CompactionRegistry.Default = selected
			if lifetime := registry.lifetime(selected.CompactionKey()); lifetime != nil {
				stop = context.AfterFunc(lifetime, cancel)
			}
		}
	}
	if g.policy != nil {
		policy := *g.policy
		snapshot.policy = &policy
	}
	done := make(chan struct{})
	g.compactions[record.ID] = done
	if g.compactionCancels == nil {
		g.compactionCancels = make(map[string]context.CancelFunc)
	}
	g.compactionCancels[record.ID] = cancel
	go func() {
		defer close(done)
		defer cancel()
		if stop != nil {
			defer stop()
		}
		messages, proposalErr := snapshot.proposeContext(workCtx, record.Source, record.Budget)
		if g.ctx.Err() != nil {
			return
		}
		_, _ = store.update(g.ctx, record.DriverID, record.ID, func(current *driverCompactionRecord) error {
			// A restarted execution may already own this pending inference. Its
			// predecessor is never allowed to publish over the newer claim.
			if current.ExecutionID != record.ExecutionID || current.Attempt != record.Attempt || current.Status != "running" {
				return nil
			}
			if proposalErr != nil {
				current.Status, current.Failure = "failed", loopdriver.FailureToWire(proposalErr)
			} else {
				current.Messages = providers.CloneChatMessages(messages)
				current.ParentOperationID = snapshot.proposedContextParentID
				current.Status = "completed"
				if hashMessagesForRequestShape(record.Source) == hashMessagesForRequestShape(messages) {
					current.Status = "unchanged"
				}
			}
			return nil
		})
	}()
	return compactionResult(record), nil
}

func (g *streamTurnGateway) ReadCompaction(ctx context.Context, id string, wait bool) (pluginprotocol.DriverCompactionResult, error) {
	callCtx, leave, err := g.enter(ctx)
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	store, err := g.compactionStore()
	if err != nil {
		leave()
		return pluginprotocol.DriverCompactionResult{}, err
	}
	done := g.compactions[id]
	record, err := store.update(callCtx, g.descriptor.ID, id, nil)
	leave()
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	if record.ID == "" {
		return pluginprotocol.DriverCompactionResult{}, errors.New("compaction proposal not found")
	}
	if wait && done != nil && record.Status == "running" {
		select {
		case <-ctx.Done():
			return pluginprotocol.DriverCompactionResult{}, ctx.Err()
		case <-g.ctx.Done():
			return pluginprotocol.DriverCompactionResult{}, g.ctx.Err()
		case <-done:
		}
		return g.ReadCompaction(ctx, id, false)
	}
	if done != nil && record.Status == "running" {
		select {
		case <-done:
			return pluginprotocol.DriverCompactionResult{}, errors.New("compaction result could not be durably committed")
		default:
		}
	}
	return compactionResult(record), nil
}

func contextHasPrefix(messages, prefix []providers.ChatMessage) bool {
	if len(messages) < len(prefix) || CompactionHistoryHash(messages[:len(prefix)]) != CompactionHistoryHash(prefix) {
		return false
	}
	for i := range prefix {
		if messages[i].Seq != prefix[i].Seq || messages[i].ToolInvocationID != prefix[i].ToolInvocationID {
			return false
		}
	}
	return true
}

func (g *streamTurnGateway) CancelCompaction(ctx context.Context, id string) (pluginprotocol.DriverCompactionResult, error) {
	callCtx, leave, err := g.enter(ctx)
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	store, err := g.compactionStore()
	if err != nil {
		leave()
		return pluginprotocol.DriverCompactionResult{}, err
	}
	prior, err := store.update(callCtx, g.descriptor.ID, id, nil)
	if err != nil {
		leave()
		return pluginprotocol.DriverCompactionResult{}, err
	}
	if prior.ID == "" {
		leave()
		return pluginprotocol.DriverCompactionResult{ID: id, Status: "missing"}, nil
	}
	record, err := store.update(callCtx, g.descriptor.ID, id, func(record *driverCompactionRecord) error {
		if record.ID == "" {
			return errors.New("compaction proposal not found")
		}
		if record.Status == "running" || record.Status == "completed" {
			record.Status = "canceled"
		}
		return nil
	})
	done := g.compactions[id]
	if err == nil {
		if cancel := g.compactionCancels[id]; cancel != nil {
			cancel()
		}
	}
	leave()
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	if done != nil {
		select {
		case <-ctx.Done():
			return pluginprotocol.DriverCompactionResult{}, ctx.Err()
		case <-g.ctx.Done():
			return pluginprotocol.DriverCompactionResult{}, g.ctx.Err()
		case <-done:
		}
	}
	return compactionResult(record), nil
}

func (g *streamTurnGateway) ApplyCompaction(ctx context.Context, id string) (pluginprotocol.DriverCompactionResult, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	store, err := g.compactionStore()
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	record, err := store.update(ctx, g.descriptor.ID, id, nil)
	if err != nil {
		return pluginprotocol.DriverCompactionResult{}, err
	}
	if record.ID == "" {
		return pluginprotocol.DriverCompactionResult{}, errors.New("compaction proposal not found")
	}
	if record.Status != "completed" {
		return compactionResult(record), nil
	}
	status := "stale"
	if record.DriverVersion == g.descriptor.Version && record.ModelKey == g.compactionModelKey() && contextHasPrefix(g.activeHistory, record.Source) {
		messages := append(providers.CloneChatMessages(record.Messages), providers.CloneChatMessages(g.activeHistory[len(record.Source):])...)
		if _, err := g.replaceContextLocked(ctx, loopdriver.ContextReplacement{Revision: g.revision, Messages: messages}); err != nil {
			return pluginprotocol.DriverCompactionResult{}, err
		}
		g.nextModelParentID = record.ParentOperationID
		status = "applied"
	}
	record, err = store.update(ctx, g.descriptor.ID, id, func(current *driverCompactionRecord) error {
		if current.Status == "completed" {
			current.Status = status
		}
		return nil
	})
	result := compactionResult(record)
	result.ContextRevision = g.revision
	return result, err
}
