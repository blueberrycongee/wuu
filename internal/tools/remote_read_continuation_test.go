package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

type readContinuationRemote struct{ requests []executionenv.ToolRequest }

func (r *readContinuationRemote) Root() string     { return "/remote-root" }
func (r *readContinuationRemote) Backend() string  { return "command" }
func (r *readContinuationRemote) Identity() string { return "identity" }
func (r *readContinuationRemote) ExecuteUnobservedRead(ctx context.Context, req executionenv.ToolRequest) (toolresult.Result, error) {
	return r.Execute(ctx, req)
}
func (r *readContinuationRemote) Execute(_ context.Context, req executionenv.ToolRequest) (toolresult.Result, error) {
	r.requests = append(r.requests, req)
	return toolresult.FromText("remote-only"), nil
}

func TestRemoteReadOnlyContinuationRechecksOriginalPolicy(t *testing.T) {
	for _, name := range []string{"read_file", "grep"} {
		t.Run(name, func(t *testing.T) {
			kit, err := New(t.TempDir())
			require.NoError(t, err)
			kit.SetSessionID("thread")
			kit.SetAgentIdentity("worker", "/root/worker")
			kit.SetPermissionMode("read_only")
			remote := &readContinuationRemote{}
			kit.SetExecutionEnvironment(remote)
			authorizer := &recordingAuthorizer{decision: AuthorizationDecision{Outcome: "deny", Reason: "revoked"}}
			kit.SetAuthorizer(authorizer)
			args := `{"path":"shared.txt"}`
			if name == "grep" {
				args = `{"pattern":"needle"}`
			}
			call := providers.ToolCall{ID: "call", Name: name, Arguments: args}
			original := kit.LookupTool(name)
			_, err = kit.ContinueRemoteReadOnly(context.Background(), original, call)
			require.ErrorContains(t, err, "revoked")
			require.Empty(t, remote.requests)
			require.Equal(t, "worker", authorizer.requests[0].ActorID)
			require.Equal(t, "/remote-root", authorizer.requests[0].CWD)
			authorizer.decision = AuthorizationDecision{Outcome: "allow"}
			result, err := kit.ContinueRemoteReadOnly(context.Background(), original, call)
			require.NoError(t, err)
			require.Equal(t, "remote-only", result.TextProjection())
			require.Len(t, remote.requests, 1)
			require.Equal(t, call, remote.requests[0].Call)
			require.Equal(t, "worker", remote.requests[0].Actor)
			require.Equal(t, "read_only", remote.requests[0].PermissionMode)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = kit.ContinueRemoteReadOnly(ctx, original, call)
			require.True(t, errors.Is(err, context.Canceled))
			require.Len(t, remote.requests, 1)
		})
	}
}

func TestUnobservedReadSerializesNativeObservation(t *testing.T) {
	var state readFileState
	release, err := state.beginUnobservedRead()
	require.NoError(t, err)
	require.False(t, state.mu.TryLock(), "native observation write lock must be held out until read completes")
	started, finished := make(chan struct{}), make(chan struct{})
	go func() { close(started); state.record("native", ReadFileEntry{ContentSHA256: "new"}); close(finished) }()
	<-started
	select {
	case <-finished:
		t.Fatal("native observation committed while unobserved read was admitted")
	default:
	}
	release()
	<-finished
	require.True(t, state.hasBeenRead("native"))
	_, err = state.beginUnobservedRead()
	require.ErrorContains(t, err, "existing native read observation")
	require.True(t, state.hasBeenRead("native"))
}
