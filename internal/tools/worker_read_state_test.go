package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkerReadStateSharesActorAdmissionAndKeepsClonesIsolated(t *testing.T) {
	root := t.TempDir()
	first, err := New(root)
	require.NoError(t, err)
	second, err := New(root)
	require.NoError(t, err)
	other, err := New(root)
	require.NoError(t, err)
	first.SetSessionID("thread")
	first.SetAgentIdentity("actor", "/root/actor")
	second.SetSessionID("thread")
	second.SetAgentIdentity("actor", "/root/actor")
	require.NoError(t, second.ShareWorkerReadState(first))
	other.SetSessionID("other")
	other.SetAgentIdentity("actor", "/root/actor")
	require.Error(t, other.ShareWorkerReadState(first))
	state := first.env.readStateStore()
	release, err := state.beginUnobservedRead()
	require.NoError(t, err)
	require.Same(t, state, second.env.readStateStore())
	require.False(t, second.env.readStateStore().mu.TryLock(), "other permission modes must serialize native observations")
	done := make(chan struct{})
	go func() { second.env.RecordRead("a", ReadFileEntry{ContentSHA256: "new"}); close(done) }()
	release()
	<-done
	require.True(t, first.env.HasBeenRead("a"))
	require.False(t, other.env.HasBeenRead("a"))
	_, err = state.beginUnobservedRead()
	require.ErrorContains(t, err, "existing native read observation")
	clone, err := second.CloneForRoot(t.TempDir())
	require.NoError(t, err)
	require.False(t, clone.env.HasBeenRead("a"), "ordinary clones retain independent read state")
}
