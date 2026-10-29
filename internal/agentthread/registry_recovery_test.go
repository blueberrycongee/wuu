package agentthread

import (
	"sync"
	"testing"
	"time"
)

func TestConditionalRecoveryUpdatePreservesConcurrentTerminalStatus(t *testing.T) {
	for i := 0; i < 1000; i++ {
		r := NewRegistry()
		if err := r.Restore(Metadata{ID: "child", Path: "/root/child", Status: StatusRunning}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; r.UpdateStatus("child", StatusCompleted, time.Now()) }()
		go func() { defer wg.Done(); <-start; r.UpdateStatusIfNonterminal("child", StatusFailed, time.Now()) }()
		close(start)
		wg.Wait()
		meta, ok := r.Resolve("child")
		if !ok || meta.Status != StatusCompleted {
			t.Fatalf("terminal status lost: %+v", meta)
		}
	}
}
