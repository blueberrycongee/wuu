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

func TestPreparedNativeReadObservationStagesUntilCommit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\n"), 0600))
	info, err := os.Stat(path)
	require.NoError(t, err)
	var state readFileState
	ticket, err := state.capturePathObservation(context.Background(), path, info)
	require.NoError(t, err)
	entry := ReadFileEntry{ContentSHA256: "snapshot", Offset: 2, Limit: 1}
	require.NoError(t, ticket.stageNativeRead(path, entry, info))
	require.False(t, state.hasBeenRead(path), "preparation must not publish observation")
	changed, err := ticket.Commit(context.Background())
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, entry, state.snapshot()[path])
	newer, err := state.capturePathObservation(context.Background(), path, info)
	require.NoError(t, err)
	require.NoError(t, newer.stageNativeRead(path, entry, info))
	fresh := ReadFileEntry{ContentSHA256: "newer"}
	state.record(path, fresh)
	changed, err = newer.Commit(context.Background())
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, fresh, state.snapshot()[path], "later observations must survive")
}

func TestNativeAndExtensionObservationBatchesFailClosedOnSameFile(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	require.NoError(t, os.WriteFile(a, []byte("a"), 0600))
	require.NoError(t, os.WriteFile(b, []byte("b"), 0600))
	ai, err := os.Stat(a)
	require.NoError(t, err)
	bi, err := os.Stat(b)
	require.NoError(t, err)
	var state readFileState
	old := ReadFileEntry{ContentSHA256: "old"}
	state.record(a, old)
	state.record(b, old)
	native, err := state.capturePathObservation(context.Background(), a, ai)
	require.NoError(t, err)
	require.NoError(t, native.stageNativeRead(a, ReadFileEntry{ContentSHA256: "native"}, ai))
	conflict, err := state.capturePathObservation(context.Background(), a, ai)
	require.NoError(t, err)
	_, err = CommitReadObservationBatch(context.Background(), []*ReadObservationTicket{native, conflict})
	require.ErrorContains(t, err, "conflicting")
	require.Equal(t, old, state.snapshot()[a])
	other, err := state.capturePathObservation(context.Background(), b, bi)
	require.NoError(t, err)
	changed, err := CommitReadObservationBatch(context.Background(), []*ReadObservationTicket{native, other})
	require.NoError(t, err)
	require.Equal(t, []bool{true, true}, changed)
	require.Equal(t, "native", state.snapshot()[a].ContentSHA256)
	require.False(t, state.hasBeenRead(b))
}

func TestPreparedNativeObservationPreservesLaterAlias(t *testing.T) {
	root := t.TempDir()
	path, alias := filepath.Join(root, "file"), filepath.Join(root, "alias")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0600))
	require.NoError(t, os.Link(path, alias))
	info, err := os.Stat(path)
	require.NoError(t, err)
	var state readFileState
	old := ReadFileEntry{ContentSHA256: "old"}
	state.record(path, old)
	ticket, err := state.capturePathObservation(context.Background(), path, info)
	require.NoError(t, err)
	require.NoError(t, ticket.stageNativeRead(path, ReadFileEntry{ContentSHA256: "staged"}, info))
	later := ReadFileEntry{ContentSHA256: "later"}
	state.record(alias, later)
	changed, err := ticket.Commit(context.Background())
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, old, state.snapshot()[path])
	require.Equal(t, later, state.snapshot()[alias])
}
