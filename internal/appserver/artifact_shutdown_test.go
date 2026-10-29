package appserver

import (
	"testing"
	"time"
)

func TestArtifactReleaseWaitsForWorkerWithoutBlockingShutdown(t *testing.T) {
	workerStarted := make(chan struct{})
	finishWorker := make(chan struct{})
	released := make(chan struct{})
	returned := make(chan struct{})
	go func() {
		finishArtifactsAfterWorkers(func() error { close(workerStarted); <-finishWorker; return nil }, func() error { close(released); return nil })
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked on worker")
	}
	<-workerStarted
	select {
	case <-released:
		t.Fatal("released while worker was active")
	default:
	}
	close(finishWorker)
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("did not release after worker finished")
	}
}
