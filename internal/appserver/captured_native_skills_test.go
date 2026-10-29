package appserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A manually assembled session supplies an explicit prompt and skill snapshot;
// a newly admitted native toolkit must not invent a catalog correction.
func TestCapturedSessionNativeToolkitPreservesSkillSnapshot(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := rt.Sessions.CreateWithMetadata("captured-native-skills", rt.RootDir)
	require.NoError(t, err)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), sess.ID)
	require.NoError(t, err)
	thread, err := srv.ensureThreadRuntime(th)
	require.NoError(t, err)
	require.NotNil(t, thread.NativeToolkit())
	update, err := thread.NativeToolkit().PrepareModelStep(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, update, "the captured empty catalog has not changed")
}
