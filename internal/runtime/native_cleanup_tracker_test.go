package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeCleanupWaitRetainsPendingWorkAfterTimeout(t *testing.T) {
	client := &generationClient{id: "tracked-cleanup"}
	generation := testPluginGeneration("tracked-cleanup", client)
	owner := testGenerationSession(t, generation)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	owner.runNativeCleanup(func() { close(entered); <-release })
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, owner.waitNativeCleanups(ctx), context.DeadlineExceeded)
	cleanupDone := make(chan error, 1)
	go func() { _, err := owner.Cleanup(); cleanupDone <- err }()
	select {
	case <-cleanupDone:
		t.Fatal("session cleanup returned before owned work")
	case <-time.After(100 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case err := <-cleanupDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("session cleanup did not join completed work")
	}
	require.NoError(t, owner.waitNativeCleanups(context.Background()))
}
