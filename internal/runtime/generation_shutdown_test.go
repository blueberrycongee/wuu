package runtime

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// A timed-out public close must not remove files still used by a draining provider.
func TestGenerationShutdownRetainsSnapshotUntilDrain(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	root := t.TempDir()
	client := &generationClient{id: "draining", onClose: func() { close(entered); <-release }}
	host := pluginhost.New(client)
	generation := &PluginGeneration{id: "draining", host: host, ownedRoots: []string{root}}
	done := make(chan error, 1)
	go func() { done <- generation.close() }()
	<-entered
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close = %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("snapshot removed before drain: %v", err)
	}
	once.Do(func() { close(release) })
	<-host.ShutdownDone()
	// Repeated close waits for resource finalization after the host has drained.
	generation.close()
	generation.revocationMu.Lock()
	defer generation.revocationMu.Unlock()
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot remains after drain: %v", err)
	}
}
