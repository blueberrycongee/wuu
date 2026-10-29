package tools

import (
	"context"
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
