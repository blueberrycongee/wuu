package tools

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestNativeSearchContinuationRechecksPolicy(t *testing.T) {
	kit, err := New(t.TempDir())
	require.NoError(t, err)
	kit.SetSessionID("thread")
	kit.SetAgentIdentity("actor", "/root")
	authorizer := &recordingAuthorizer{decision: AuthorizationDecision{Outcome: "deny", Reason: "policy revoked"}}
	kit.SetAuthorizer(authorizer)
	call := providers.ToolCall{Name: "web_search", Arguments: `{"query":"abc"}`}
	_, err = kit.ContinueNativeWebSearch(context.Background(), kit.LookupTool("web_search"), call)
	require.ErrorContains(t, err, "policy revoked")
	require.Len(t, authorizer.requests, 1)
	require.Equal(t, "thread", authorizer.requests[0].SessionID)
	require.Equal(t, "actor", authorizer.requests[0].ActorID)
	require.Equal(t, call.Arguments, authorizer.requests[0].Arguments)
	_, ok := kit.WebEvidenceContextBlock()
	require.False(t, ok)
}

func TestNativeSearchContinuationControlledSearchIsIsolated(t *testing.T) {
	kit, err := New(t.TempDir())
	require.NoError(t, err)
	kit.SetSessionID("thread")
	kit.SetAgentIdentity("actor", "/root")
	call := providers.ToolCall{Name: "web_search", Arguments: `{"query":"controlled"}`}
	calls := 0
	result, err := kit.continueNativeWebSearch(context.Background(), kit.LookupTool("web_search"), call, func(_ context.Context, query string, limit int) ([]searchResult, error) {
		calls++
		require.Equal(t, "controlled", query)
		require.Equal(t, 10, limit)
		return []searchResult{{Title: "fake", URL: "https://example.test", Snippet: "controlled"}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Contains(t, result.TextProjection(), "controlled")
	_, ok := kit.WebEvidenceContextBlock()
	require.False(t, ok)
}

func TestNativeSearchContinuationCanceledBeforeDispatch(t *testing.T) {
	kit, err := New(t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err = kit.continueNativeWebSearch(ctx, kit.LookupTool("web_search"), providers.ToolCall{Name: "web_search", Arguments: `{"query":"cancelled"}`}, func(context.Context, string, int) ([]searchResult, error) { calls++; return nil, nil })
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls)
}
