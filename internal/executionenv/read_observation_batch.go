package executionenv

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"time"
)

type preparedReadBinding struct {
	Receipt                   string `json:"receipt"`
	Actor                     string `json:"actor"`
	PermissionMode            string `json:"permission_mode"`
	GitAttributionEnabled     bool   `json:"git_attribution_enabled,omitempty"`
	ShellEarlyHandoffDisabled bool   `json:"shell_early_handoff_disabled,omitempty"`
}
type preparedReadHandle struct {
	client                              *Client
	binding                             preparedReadBinding
	workerInstance, environmentIdentity string
}
type preparedReadGroup struct {
	client                                     *Client
	actor, workerInstance, environmentIdentity string
}

func (p PreparedRead) GroupKey() any {
	if p.handle == nil {
		return nil
	}
	return preparedReadGroup{p.handle.client, p.handle.binding.Actor, p.handle.workerInstance, p.handle.environmentIdentity}
}

// ReadBatchIdentity contains no bearer receipt or worker transport credential.
// It may be persisted in the invocation ledger before the commit decision.
type ReadBatchIdentity struct {
	OperationID         string `json:"operation_id"`
	WorkerInstance      string `json:"worker_instance"`
	Actor               string `json:"actor"`
	EnvironmentIdentity string `json:"environment_identity"`
}
type PreparedReadBatch struct {
	Identity ReadBatchIdentity
	client   *Client
}

func PrepareReadBatch(ctx context.Context, values []PreparedRead) (*PreparedReadBatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 || len(values) > 128 || values[0].handle == nil {
		return nil, errors.New("invalid prepared remote read batch")
	}
	first := values[0].handle
	if first.client == nil || first.workerInstance == "" || first.binding.Actor == "" || first.environmentIdentity == "" {
		return nil, errors.New("remote worker identity unavailable")
	}
	seen := make(map[string]struct{}, len(values))
	bindings := make([]preparedReadBinding, 0, len(values))
	for _, value := range values {
		h := value.handle
		if h == nil || h.client != first.client || h.workerInstance != first.workerInstance || h.environmentIdentity != first.environmentIdentity || h.binding.Actor != first.binding.Actor {
			return nil, errors.New("remote batch requires one worker and actor")
		}
		if _, exists := seen[h.binding.Receipt]; exists {
			return nil, errors.New("duplicate remote read receipt")
		}
		seen[h.binding.Receipt] = struct{}{}
		bindings = append(bindings, h.binding)
	}
	identity := ReadBatchIdentity{OperationID: rand.Text(), WorkerInstance: first.workerInstance, Actor: first.binding.Actor, EnvironmentIdentity: first.environmentIdentity}
	data, err := first.client.Call(ctx, "read/batch-prepare", struct {
		ReadBatchIdentity
		Receipts []preparedReadBinding `json:"receipts"`
	}{identity, bindings})
	if err == nil {
		var response struct {
			Prepared bool `json:"prepared"`
		}
		if json.Unmarshal(data, &response) != nil || !response.Prepared {
			err = errors.New("invalid remote batch preparation")
		}
	}
	if err != nil {
		// The worker may have reserved the batch before a lost or malformed
		// reply. Fence and release it if possible; otherwise bounded TTL applies.
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		orphan := &PreparedReadBatch{Identity: identity, client: first.client}
		if orphan.Abort(cleanup) == nil {
			_ = orphan.Release(cleanup)
		}
		cancel()
		return nil, err
	}
	return &PreparedReadBatch{Identity: identity, client: first.client}, nil
}
func (b *PreparedReadBatch) call(ctx context.Context, method string) (json.RawMessage, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("remote batch unavailable")
	}
	return b.client.Call(ctx, method, b.Identity)
}
func (b *PreparedReadBatch) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := b.call(ctx, "read/batch-commit")
	if err != nil {
		return errors.Join(ErrReadObservationCommitUnknown, err)
	}
	var result struct {
		Committed bool `json:"committed"`
	}
	if json.Unmarshal(data, &result) != nil || !result.Committed {
		return ErrReadObservationCommitUnknown
	}
	return nil
}
func (b *PreparedReadBatch) Abort(ctx context.Context) error {
	data, err := b.call(ctx, "read/batch-abort")
	if err != nil {
		return err
	}
	var result struct {
		Aborted bool `json:"aborted"`
	}
	if json.Unmarshal(data, &result) != nil || !result.Aborted {
		return errors.New("remote worker did not confirm batch abort")
	}
	return nil
}
func (b *PreparedReadBatch) Release(ctx context.Context) error {
	data, err := b.call(ctx, "read/batch-release")
	if err != nil {
		return err
	}
	var result struct {
		Released bool `json:"released"`
	}
	if json.Unmarshal(data, &result) != nil || !result.Released {
		return errors.New("remote worker did not confirm batch release")
	}
	return nil
}
func (b *PreparedReadBatch) Status(ctx context.Context) (string, error) {
	data, err := b.call(ctx, "read/batch-status")
	if err != nil {
		return "", err
	}
	var result struct {
		State string `json:"state"`
	}
	if json.Unmarshal(data, &result) != nil || result.State == "" {
		return "", errors.New("invalid remote batch status")
	}
	return result.State, nil
}

// CommitPreparedReadBatch is used only by callers that do not require durable
// recovery. Product settlement must stage Identity before calling Commit.
func CommitPreparedReadBatch(ctx context.Context, values []PreparedRead) error {
	batch, err := PrepareReadBatch(ctx, values)
	if err != nil {
		return err
	}
	return batch.Commit(ctx)
}

// RecoverReadBatch binds a non-bearer identity to the original worker instance.
func (e *Environment) RecoverReadBatch(ctx context.Context, identity ReadBatchIdentity) (*PreparedReadBatch, error) {
	if identity.EnvironmentIdentity != e.identity || identity.WorkerInstance == "" || identity.Actor == "" || identity.OperationID == "" {
		return nil, ErrReadObservationCommitUnknown
	}
	client, err := e.connect(ctx)
	if err != nil {
		return nil, errors.Join(ErrReadObservationCommitUnknown, err)
	}
	if client.workerInstance != identity.WorkerInstance {
		return nil, ErrReadObservationCommitUnknown
	}
	return &PreparedReadBatch{Identity: identity, client: client}, nil
}
