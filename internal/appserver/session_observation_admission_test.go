package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestThreadAdmissionRecoversSessionObservationFromOriginalStore(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	kit, err := tools.New(rt.RootDir)
	require.NoError(t, err)
	rt.Toolkit, rt.StreamRunner.Tools = kit, kit
	sess, err := rt.Sessions.CreateWithMetadata("session-observation", rt.RootDir)
	require.NoError(t, err)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), sess.ID)
	require.NoError(t, err)
	thread, err := srv.ensureThreadRuntime(th)
	require.NoError(t, err)
	content := []byte("first\nsecond\n")
	require.NoError(t, os.WriteFile(filepath.Join(thread.Toolkit.SessionDir(), "file.txt"), content, 0600))
	sum := sha256.Sum256(content)
	raw, err := json.Marshal(map[string]any{"path": "$SESSION_DIR/file.txt", "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", sum)})
	require.NoError(t, err)
	args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
	require.NoError(t, err)
	call := providers.ToolCall{Name: "read_file", Arguments: string(args)}
	restored, _, _, err := thread.Toolkit.RestoreRemoteNativeRead(call)
	require.NoError(t, err)
	actor, actorPath := thread.Toolkit.ExecutionActor()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prepared, err := thread.Toolkit.PrepareSessionNativeRead(ctx, actor, actorPath, thread.Toolkit.LookupTool("read_file"), call, restored)
	require.NoError(t, err)
	localBatch, err := tools.PrepareSessionReadBatch(ctx, []tools.PreparedSessionRead{prepared})
	require.NoError(t, err)
	ledger, err := toolledger.New(rt.Documents, sess.ID)
	require.NoError(t, err)
	group, err := ledger.BeginBatch(ctx, "turn", 0)
	require.NoError(t, err)
	inv, err := ledger.Prepare(ctx, group, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
	require.NoError(t, err)
	require.NoError(t, ledger.FinalizeBatch(ctx, group))
	require.NoError(t, ledger.Start(ctx, inv.ID))
	id := localBatch.Identity
	require.NoError(t, ledger.StageObservation(ctx, inv.ID, toolledger.ObservationIntent{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity, OwnerKind: id.OwnerKind}, toolresult.FromText("committed result")))
	require.NoError(t, localBatch.Commit(ctx))
	th.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
	th.mu.Unlock()
	require.NoError(t, err)
	require.True(t, acquired)
	defer func() { th.mu.Lock(); th.releaseThreadExecutionLeaseLocked(); th.mu.Unlock() }()
	admitted, err := srv.ensureThreadRuntimeAfterAdmission(ctx, th)
	require.NoError(t, err)
	require.Same(t, thread, admitted)
	projection, err := ledger.PendingProjection(ctx)
	require.ErrorContains(t, err, "original observation authorization is unavailable")
	require.Empty(t, projection)
	require.NoError(t, ledger.ValidateContinuation(ctx))
	_, exists := thread.Toolkit.ActiveFilesContextBlock()
	require.True(t, exists)
}
