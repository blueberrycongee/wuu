package tools

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
)

// A host receipt is scoped to the exact in-memory observation store. Losing
// that store makes recovery unknown, just as losing a worker receipt does.
type sessionReadCoordinator struct {
	mu       sync.Mutex
	instance string
	batches  map[string]*sessionReadBatch
}
type sessionReadBatch struct {
	identity  executionenv.ReadBatchIdentity
	tickets   []*ReadObservationTicket
	status    string
	expires   time.Time
	root      *sessionArtifactRoot
	actorPath string
}

func (r *readFileState) coordinator() *sessionReadCoordinator {
	r.localCoordinatorMu.Lock()
	defer r.localCoordinatorMu.Unlock()
	if r.localCoordinator == nil {
		r.localCoordinator = &sessionReadCoordinator{instance: "host-read-" + rand.Text(), batches: make(map[string]*sessionReadBatch)}
	}
	return r.localCoordinator
}
func (c *sessionReadCoordinator) prepare(ctx context.Context, state *readFileState, root *sessionArtifactRoot, actor, actorPath, session string, tickets []*ReadObservationTicket) (*executionenv.PreparedReadBatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if actor == "" || session == "" || root == nil {
		return nil, errors.New("session observation authority unavailable")
	}
	if err := ValidateReadObservationBatch(tickets); err != nil {
		return nil, err
	}
	for _, t := range tickets {
		if t.state != state || !t.HasNativeRead() {
			return nil, errors.New("invalid session observation participant")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for id, b := range c.batches {
		if !b.expires.After(now) {
			delete(c.batches, id)
		}
	}
	if len(c.batches) >= 1024 {
		return nil, errors.New("session observation receipt capacity reached")
	}
	id := executionenv.ReadBatchIdentity{OperationID: rand.Text(), WorkerInstance: c.instance, Actor: actor, EnvironmentIdentity: session}
	if _, exists := c.batches[id.OperationID]; exists {
		return nil, errors.New("session observation identity collision")
	}
	b := &sessionReadBatch{identity: id, tickets: append([]*ReadObservationTicket(nil), tickets...), status: "prepared", expires: now.Add(2 * time.Minute), root: root, actorPath: actorPath}
	c.batches[id.OperationID] = b
	return c.handle(id), nil
}
func (c *sessionReadCoordinator) handle(id executionenv.ReadBatchIdentity) *executionenv.PreparedReadBatch {
	op := func(ctx context.Context, action string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		b := c.batches[id.OperationID]
		if b == nil || b.identity != id || !b.expires.After(time.Now()) {
			return "", errors.New("session observation receipt unavailable")
		}
		switch action {
		case "status":
			return b.status, nil
		case "commit":
			if b.status == "committed" {
				return b.status, nil
			}
			if b.status != "prepared" {
				return "", errors.New("session observation is not committable")
			}
			b.root.lifecycle.RLock()
			defer b.root.lifecycle.RUnlock()
			if b.root.closed {
				return "", errors.New("session artifact root is unavailable")
			}
			if _, err := CommitReadObservationBatch(ctx, b.tickets); err != nil {
				return "", err
			}
			b.status = "committed"
			b.expires = time.Now().Add(10 * time.Minute)
		case "abort":
			if b.status == "committed" {
				return "", errors.New("session observation already committed")
			}
			b.status = "aborted"
			b.expires = time.Now().Add(10 * time.Minute)
		case "release":
			if b.status == "prepared" {
				return "", errors.New("session observation remains pending")
			}
			delete(c.batches, id.OperationID)
		}
		return b.status, nil
	}
	return executionenv.NewLocalReadBatch(id, executionenv.LocalReadBatchOperations{Commit: func(ctx context.Context) error { _, err := op(ctx, "commit"); return err }, Abort: func(ctx context.Context) error { _, err := op(ctx, "abort"); return err }, Release: func(ctx context.Context) error { _, err := op(ctx, "release"); return err }, Status: func(ctx context.Context) (string, error) { return op(ctx, "status") }})
}

// RecoverReadBatch accepts only the exact original in-memory store and actor.
// Other identities go to the selected remote backend, which authenticates its
// own worker identity.
type sessionReadRecoverer struct {
	state                     *readFileState
	remote                    executionenv.ReadBatchRecoverer
	actor, actorPath, session string
	root                      *sessionArtifactRoot
}

func (r sessionReadRecoverer) RecoverReadBatch(ctx context.Context, id executionenv.ReadBatchIdentity) (*executionenv.PreparedReadBatch, error) {
	if id.WorkerInstance == r.state.coordinator().instance {
		if id.Actor != r.actor || id.EnvironmentIdentity != r.session {
			return nil, errors.New("session observation authority changed")
		}
		c := r.state.coordinator()
		c.mu.Lock()
		b := c.batches[id.OperationID]
		valid := b != nil && b.root == r.root && b.actorPath == r.actorPath
		c.mu.Unlock()
		if !valid {
			return nil, errors.New("session observation root changed")
		}
		if _, err := r.root.root.Stat("."); err != nil {
			return nil, errors.New("session observation root unavailable")
		}
		batch := c.handle(id)
		if _, err := batch.Status(ctx); err != nil {
			return nil, err
		}
		return batch, nil
	}
	if r.remote == nil {
		return nil, errors.New("original observation owner unavailable")
	}
	return r.remote.RecoverReadBatch(ctx, id)
}
func (t *Toolkit) ObservationRecoverer() executionenv.ReadBatchRecoverer {
	if t == nil || t.env == nil {
		return nil
	}
	actor, actorPath := t.ExecutionActor()
	remote, _ := t.ExecutionEnvironment().(executionenv.ReadBatchRecoverer)
	return sessionReadRecoverer{state: t.env.readStateStore(), remote: remote, actor: actor, actorPath: actorPath, session: t.env.SessionID, root: t.env.sessionArtifactRoot}
}
