package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteObservationCommitIsVersionedAndActorScoped(t *testing.T) {
	var first, other readFileState
	same := ReadFileEntry{ContentSHA256: "same"}
	first.record("/worker/a", same)
	first.record("/worker/other", same)
	other.record("/worker/a", same)
	aborted := first.prepareInvalidation([]string{"/worker/a"})
	_ = aborted // failed/cancelled provider never commits
	require.True(t, first.hasBeenRead("/worker/a"))
	canceled := first.prepareInvalidation([]string{"/worker/a"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := canceled.commit(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, first.hasBeenRead("/worker/a"))
	// An identical second read is a new revision, not the captured observation.
	stale := first.prepareInvalidation([]string{"/worker/a"})
	first.record("/worker/a", same)
	changed, err := stale.commit(context.Background())
	require.NoError(t, err)
	require.False(t, changed)
	require.True(t, first.hasBeenRead("/worker/a"))
	current := first.prepareInvalidation([]string{"/worker/a"})
	changed, err = current.commit(context.Background())
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, first.hasBeenRead("/worker/a"))
	require.True(t, first.hasBeenRead("/worker/other"))
	require.True(t, other.hasBeenRead("/worker/a"))
}

func TestRemoteObservationCaptureBoundsAndCancellation(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(original, []byte("x"), 0600))
	info, err := os.Stat(original)
	require.NoError(t, err)
	var state readFileState
	for i := 0; i <= maxObservationAliases; i++ {
		path := filepath.Join(root, fmt.Sprint(i))
		require.NoError(t, os.Link(original, path))
		state.record(path, ReadFileEntry{})
	}
	_, err = state.capturePathObservation(context.Background(), original, info)
	require.ErrorContains(t, err, "alias limit")
	canceled, stop := context.WithCancel(context.Background())
	stop()
	_, err = state.capturePathObservation(canceled, original, info)
	require.ErrorIs(t, err, context.Canceled)
	var overloaded readFileState
	for i := 0; i <= maxObservationScan; i++ {
		overloaded.record(fmt.Sprintf("missing-%d", i), ReadFileEntry{})
	}
	_, err = overloaded.capturePathObservation(context.Background(), original, info)
	require.ErrorContains(t, err, "scan limit")
}
