package appserver

import (
	"strings"
	"sync"
)

type pluginTurnWaitKey struct {
	pluginID  string
	requestID string
}

// pluginTurnWaitHub bridges durable lifecycle persistence to callers waiting
// in host.session.inspect. It is intentionally owned by the app-server rather
// than a plugin callback: plugin helpers process requests serially, so waiting
// for the callback that is queued behind the inspect call would deadlock.
type pluginTurnWaitHub struct {
	mu      sync.Mutex
	nextID  uint64
	waiters map[pluginTurnWaitKey]map[uint64]chan struct{}
}

func (h *pluginTurnWaitHub) subscribe(pluginID, requestID string) (<-chan struct{}, func()) {
	key := pluginTurnWaitKey{pluginID: strings.TrimSpace(pluginID), requestID: strings.TrimSpace(requestID)}
	ready := make(chan struct{})

	h.mu.Lock()
	if h.waiters == nil {
		h.waiters = make(map[pluginTurnWaitKey]map[uint64]chan struct{})
	}
	h.nextID++
	id := h.nextID
	group := h.waiters[key]
	if group == nil {
		group = make(map[uint64]chan struct{})
		h.waiters[key] = group
	}
	group[id] = ready
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			if group := h.waiters[key]; group != nil {
				delete(group, id)
				if len(group) == 0 {
					delete(h.waiters, key)
				}
			}
			h.mu.Unlock()
		})
	}
	return ready, unsubscribe
}

func (h *pluginTurnWaitHub) notify(pluginID, requestID string) {
	key := pluginTurnWaitKey{pluginID: strings.TrimSpace(pluginID), requestID: strings.TrimSpace(requestID)}
	h.mu.Lock()
	group := h.waiters[key]
	delete(h.waiters, key)
	for _, ready := range group {
		close(ready)
	}
	h.mu.Unlock()
}
