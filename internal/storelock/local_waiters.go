package storelock

import "sync"

// Local releases wake contenders immediately. The OS lock remains authoritative,
// including for aliases and other processes, which still use the retry timer.
var localWaiters = struct {
	sync.Mutex
	paths map[string]*lockWaiters
}{paths: make(map[string]*lockWaiters)}

type lockWaiters struct {
	path     string
	refs     int
	released chan struct{}
}

func retainLockWaiters(path string) *lockWaiters {
	localWaiters.Lock()
	defer localWaiters.Unlock()
	waiters := localWaiters.paths[path]
	if waiters == nil {
		waiters = &lockWaiters{path: path, released: make(chan struct{})}
		localWaiters.paths[path] = waiters
	}
	waiters.refs++
	return waiters
}

func (w *lockWaiters) nextRelease() <-chan struct{} {
	localWaiters.Lock()
	defer localWaiters.Unlock()
	return w.released
}

func (w *lockWaiters) release(unlocked bool) {
	localWaiters.Lock()
	defer localWaiters.Unlock()
	w.refs--
	if w.refs == 0 {
		delete(localWaiters.paths, w.path)
		return
	}
	if unlocked {
		close(w.released)
		w.released = make(chan struct{})
	}
}
