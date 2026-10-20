package siwc

import (
	"context"
	"sync"
	"time"
)

type boundRequest struct {
	manager *Manager
	cancel  context.CancelFunc
}

var requests = struct {
	sync.Mutex
	byAccount map[string]map[*boundRequest]struct{}
}{byAccount: map[string]map[*boundRequest]struct{}{}}

// BindRequest binds an inference/model request to the selected session. Call
// release when it finishes, and use the returned context for AccessToken and
// HTTP I/O. SignOut cancels same-process contexts immediately; persisted
// revision changes cancel requests in other processes within 500 milliseconds.
func (m *Manager) BindRequest(ctx context.Context, id string) (context.Context, func(), error) {
	lock, err := m.accountLock(id)
	if err != nil {
		return nil, nil, err
	}
	defer lock.Release()
	r, err := m.registration(id)
	if err != nil {
		return nil, nil, err
	}
	c, err := m.readCredentials(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	if !hasScope(c.Scopes, directScope) {
		return nil, nil, ErrPlanDisabled
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil, ErrSignInRequired
	}
	requestCtx, cancel := context.WithCancel(ctx)
	binding := &boundRequest{manager: m, cancel: cancel}
	key := m.namespace + ":" + id
	requests.Lock()
	if requests.byAccount[key] == nil {
		requests.byAccount[key] = map[*boundRequest]struct{}{}
	}
	requests.byAccount[key][binding] = struct{}{}
	requests.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			requests.Lock()
			delete(requests.byAccount[key], binding)
			if len(requests.byAccount[key]) == 0 {
				delete(requests.byAccount, key)
			}
			requests.Unlock()
		})
	}
	// Wuu project windows use separate processes. Metadata revisions fence their
	// in-flight requests too, without exposing credentials to the watcher.
	go func() {
		defer release()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-requestCtx.Done():
				return
			case <-ticker.C:
				current, err := m.registration(id)
				if err != nil || current.Revision != r.Revision {
					return
				}
			}
		}
	}()
	return requestCtx, release, nil
}
func cancelAccountRequests(namespace, id string) {
	requests.Lock()
	defer requests.Unlock()
	key := namespace + ":" + id
	for request := range requests.byAccount[key] {
		request.cancel()
	}
	delete(requests.byAccount, key)
}
func cancelManagerRequests(manager *Manager) {
	requests.Lock()
	defer requests.Unlock()
	for key, bindings := range requests.byAccount {
		for request := range bindings {
			if request.manager == manager {
				request.cancel()
				delete(bindings, request)
			}
		}
		if len(bindings) == 0 {
			delete(requests.byAccount, key)
		}
	}
}
