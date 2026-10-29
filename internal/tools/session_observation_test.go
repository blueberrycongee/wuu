package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionLineDirectHelperRequiresPreparedSettlement(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	sessionDir := filepath.Join(t.TempDir(), "session")
	kit, err := New(t.TempDir())
	require.NoError(t, err)
	kit.SetSessionID("session")
	kit.SetAgentIdentity("session", "root")
	require.NoError(t, kit.SetSessionDir(sessionDir))
	defer kit.CloseSessionArtifacts()
	content := []byte("first\nsecret\nthird\n")
	path := filepath.Join(sessionDir, "line.txt")
	require.NoError(t, os.WriteFile(path, content, 0600))
	sum := sha256.Sum256(content)
	token := encodeReadFileContinuation("$SESSION_DIR/line.txt", 2, 1, fmt.Sprintf("%x", sum))
	raw, err := json.Marshal(map[string]string{"continuation": token})
	require.NoError(t, err)
	call := providers.ToolCall{Name: "read_file", Arguments: string(raw)}
	restored, managed, line, err := kit.RestoreRemoteNativeRead(call)
	require.NoError(t, err)
	require.True(t, managed)
	require.True(t, line)
	count := 0
	kit.SetPermissionRequestHook(func(context.Context, *Toolkit, ToolInfo, providers.ToolCall) error {
		count++
		if count >= 2 {
			return errors.New("revoked")
		}
		return nil
	})
	actor, actorPath := kit.ExecutionActor()
	_, err = kit.ExecuteNativeSessionRead(context.Background(), actor, actorPath, kit.LookupTool("read_file"), call, restored)
	require.ErrorContains(t, err, "requires prepared settlement")
	require.False(t, kit.env.readStateStore().hasBeenRead(path), "a direct helper must not publish a line observation")
}

func TestSessionLineRootCloseFencesCommit(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	session := filepath.Join(t.TempDir(), "session")
	kit, err := New(t.TempDir())
	require.NoError(t, err)
	kit.SetSessionID("session")
	kit.SetAgentIdentity("session", "root")
	require.NoError(t, kit.SetSessionDir(session))
	content := []byte("first\nsecond\n")
	path := filepath.Join(session, "file")
	require.NoError(t, os.WriteFile(path, content, 0600))
	sum := sha256.Sum256(content)
	token := encodeReadFileContinuation("$SESSION_DIR/file", 2, 1, fmt.Sprintf("%x", sum))
	raw, err := json.Marshal(map[string]string{"continuation": token})
	require.NoError(t, err)
	call := providers.ToolCall{Name: "read_file", Arguments: string(raw)}
	restored, _, _, err := kit.RestoreRemoteNativeRead(call)
	require.NoError(t, err)
	actor, actorPath := kit.ExecutionActor()
	prepared, err := kit.PrepareSessionNativeRead(context.Background(), actor, actorPath, kit.LookupTool("read_file"), call, restored)
	require.NoError(t, err)
	batch, err := PrepareSessionReadBatch(context.Background(), []PreparedSessionRead{prepared})
	require.NoError(t, err)
	require.NoError(t, kit.CloseSessionArtifacts())
	require.ErrorContains(t, batch.Commit(context.Background()), "root is unavailable")
	require.NoError(t, batch.Abort(context.Background()))
	require.False(t, kit.env.readStateStore().hasBeenRead(path))
}

func TestSessionLineRootCloseWaitsForAdmittedCommit(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	session := filepath.Join(t.TempDir(), "session")
	kit, err := New(t.TempDir())
	require.NoError(t, err)
	kit.SetSessionID("session")
	kit.SetAgentIdentity("session", "root")
	require.NoError(t, kit.SetSessionDir(session))
	content := []byte("first\nsecond\n")
	require.NoError(t, os.WriteFile(filepath.Join(session, "file"), content, 0600))
	sum := sha256.Sum256(content)
	token := encodeReadFileContinuation("$SESSION_DIR/file", 2, 1, fmt.Sprintf("%x", sum))
	raw, err := json.Marshal(map[string]string{"continuation": token})
	require.NoError(t, err)
	call := providers.ToolCall{Name: "read_file", Arguments: string(raw)}
	restored, _, _, err := kit.RestoreRemoteNativeRead(call)
	require.NoError(t, err)
	actor, actorPath := kit.ExecutionActor()
	p, err := kit.PrepareSessionNativeRead(context.Background(), actor, actorPath, kit.LookupTool("read_file"), call, restored)
	require.NoError(t, err)
	batch, err := PrepareSessionReadBatch(context.Background(), []PreparedSessionRead{p})
	require.NoError(t, err)
	state, root := kit.env.readStateStore(), kit.env.sessionArtifactRoot
	state.mu.Lock()
	committed := make(chan error, 1)
	go func() { committed <- batch.Commit(context.Background()) }()
	deadline := time.Now().Add(5 * time.Second)
	locked := false
	for time.Now().Before(deadline) {
		if !root.lifecycle.TryLock() {
			locked = true
			break
		}
		root.lifecycle.Unlock()
		time.Sleep(time.Millisecond)
	}
	if !locked {
		state.mu.Unlock()
		t.Fatal("commit did not retain the root")
	}
	closed := make(chan error, 1)
	go func() { closed <- root.Close() }()
	select {
	case err := <-closed:
		state.mu.Unlock()
		t.Fatalf("root closed before commit: %v", err)
	default:
	}
	state.mu.Unlock()
	require.NoError(t, <-committed)
	require.NoError(t, <-closed)
}
