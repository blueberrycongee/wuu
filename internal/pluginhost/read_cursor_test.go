package pluginhost

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadCursorIsBoundToOwnerAndGeneration(t *testing.T) {
	first, other := New(), New()
	t.Cleanup(func() { _ = first.Close(context.Background()); _ = other.Close(context.Background()) })
	state := ReadCursorState{SessionID: "session", ActorID: "actor", ActorPath: "/root/actor", CWD: "/workspace", PluginID: "reader", ToolID: "read", Path: "notes.txt", Offset: 3, Limit: 7, TotalLines: 20, PluginCursor: "provider-private-state"}
	token, err := first.IssueReadCursor(state)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, "wuu-read-v1:"))
	require.NotContains(t, token, state.PluginCursor)
	actual, err := first.ResolveReadCursor(token, state.ReadCursorOwner())
	require.NoError(t, err)
	require.Equal(t, state, actual)
	_, err = other.ResolveReadCursor(token, state.ReadCursorOwner())
	require.Error(t, err, "a new generation cannot consume an old provider cursor")
	for _, mutate := range []func(*ReadCursorOwner){
		func(o *ReadCursorOwner) { o.SessionID = "other" }, func(o *ReadCursorOwner) { o.ActorID = "other" },
		func(o *ReadCursorOwner) { o.ActorPath = "/root/other" }, func(o *ReadCursorOwner) { o.CWD = "/other" },
		func(o *ReadCursorOwner) { o.PluginID = "other" }, func(o *ReadCursorOwner) { o.ToolID = "other" },
	} {
		owner := state.ReadCursorOwner()
		mutate(&owner)
		_, err := first.ResolveReadCursor(token, owner)
		require.Error(t, err)
	}
	_, err = first.ResolveReadCursor("wuu-read-v1:forged", state.ReadCursorOwner())
	require.Error(t, err)
	require.NoError(t, first.Close(context.Background()))
	_, err = first.ResolveReadCursor(token, state.ReadCursorOwner())
	require.Error(t, err)
}

func TestReadCursorExpiryBoundsAndRetirement(t *testing.T) {
	host := New()
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	now := time.Now()
	host.readCursors.clock = func() time.Time { return now }
	state := ReadCursorState{SessionID: "s", CWD: "/workspace", PluginID: "reader", ToolID: "read", Path: "f", Offset: 2, TotalLines: 3, PluginCursor: "private"}
	token, err := host.IssueReadCursor(state)
	require.NoError(t, err)
	second, err := host.IssueReadCursor(state)
	require.NoError(t, err)
	require.NotEqual(t, token, second, "each token needs a fresh nonce")
	tampered := []byte(token)
	tampered[len(tampered)/2] ^= 1
	_, err = host.ResolveReadCursor(string(tampered), state.ReadCursorOwner())
	require.Error(t, err)
	_, err = host.ResolveReadCursor("wuu-read-v1:"+strings.Repeat("x", maxReadCursorTokenBytes), state.ReadCursorOwner())
	require.Error(t, err)
	oversized := state
	oversized.PluginCursor = strings.Repeat("x", maxPluginReadCursorBytes+1)
	_, err = host.IssueReadCursor(oversized)
	require.Error(t, err)
	now = now.Add(readCursorLifetime + time.Nanosecond)
	_, err = host.ResolveReadCursor(token, state.ReadCursorOwner())
	require.Error(t, err)
	token, err = host.IssueReadCursor(state)
	require.NoError(t, err)
	host.retireReadCursors("reader")
	_, err = host.ResolveReadCursor(token, state.ReadCursorOwner())
	require.Error(t, err)
	_, err = host.IssueReadCursor(state)
	require.Error(t, err, "retired owner must not issue new handles")
}
