package agent

import (
	"context"
	"sync"
)

// ToolExecutionGate bounds leaf executions and excludes unsafe writes from
// all other leaves, including dynamically submitted nested calls. Orchestrators
// do not acquire it: holding a leaf slot while awaiting children deadlocks at
// capacity. A waiting writer stops new readers from starving it indefinitely.
// One gate may be shared by runtimes whose work can outlive a model step.
type ToolExecutionGate struct {
	mu             sync.Mutex
	changed        chan struct{}
	active         int
	writer         bool
	waitingWriters int
	capacity       int
}

func NewToolExecutionGate(capacity int) *ToolExecutionGate {
	return &ToolExecutionGate{capacity: max(1, capacity), changed: make(chan struct{})}
}

func (g *ToolExecutionGate) acquire(ctx context.Context, shared bool) (func(), error) {
	g.mu.Lock()
	if !shared {
		g.waitingWriters++
	}
	for {
		if err := ctx.Err(); err != nil {
			if !shared {
				g.waitingWriters--
				g.signalLocked()
			}
			g.mu.Unlock()
			return nil, err
		}
		if (!shared && g.active == 0) || (shared && !g.writer && g.waitingWriters == 0 && g.active < g.capacity) {
			g.active++
			if !shared {
				g.waitingWriters--
				g.writer = true
			}
			g.mu.Unlock()
			return func() {
				g.mu.Lock()
				g.active--
				if !shared {
					g.writer = false
				}
				g.signalLocked()
				g.mu.Unlock()
			}, nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		g.mu.Lock()
	}
}

func (g *ToolExecutionGate) signalLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}
