package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

type partialOutputExecutor struct {
	remote, nested, fail, finalizerFail, commitFail bool
	committed, aborted                              atomic.Int32
	entered                                         chan struct{}
}

var identicalMedia = toolresult.ContentPart{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: "eA==", Name: "same.png"}

func (*partialOutputExecutor) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "parent"}, {Name: "inner"}, {Name: "remote"}, {Name: "ordinary"}}
}
func (*partialOutputExecutor) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{Orchestrator: call.Name == "parent" || call.Name == "inner", ConcurrencySafe: true}, true
}
func (*partialOutputExecutor) Execute(context.Context, providers.ToolCall) (string, error) {
	return "", errors.New("rich path required")
}
func (e *partialOutputExecutor) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	switch call.Name {
	case "ordinary":
		return toolresult.Result{Content: []toolresult.ContentPart{identicalMedia}}, nil
	case "remote":
		err := toolctx.RegisterSettlement(ctx, func(context.Context) error {
			if e.commitFail {
				return errors.New("controlled commit failure")
			}
			e.committed.Add(1)
			return nil
		}, func() { e.aborted.Add(1) })
		if err != nil {
			return toolresult.Result{}, err
		}
		return toolresult.Result{Content: []toolresult.ContentPart{{Type: "text", Text: "remote SECRET"}, identicalMedia}}, nil
	case "inner":
		n, ok := toolctx.Nested(ctx)
		if !ok {
			return toolresult.Result{}, errors.New("missing nested scope")
		}
		return n.Invoke(ctx, providers.ToolCall{ID: "remote-inner", Name: "remote", Arguments: `{}`})
	case "parent":
		n, ok := toolctx.Nested(ctx)
		if !ok {
			return toolresult.Result{}, errors.New("missing nested scope")
		}
		ordinary, err := n.Invoke(ctx, providers.ToolCall{ID: "ordinary", Name: "ordinary", Arguments: `{}`})
		if err != nil {
			return toolresult.Result{}, err
		}
		output := toolresult.Result{Content: []toolresult.ContentPart{{Type: "text", Text: "ORDINARY LOG"}}}
		output.Content = append(output.Content, ordinary.Content...)
		if e.remote {
			name := "remote"
			if e.nested {
				name = "inner"
			}
			child, err := n.Invoke(ctx, providers.ToolCall{ID: "transaction", Name: name, Arguments: `{}`})
			if err != nil {
				return toolresult.Result{}, err
			}
			output.Content[0].Text += " remote SECRET"
			output.Content = append(output.Content, child.Content[1:]...)
		}
		output.IsError = e.fail
		if e.entered != nil {
			close(e.entered)
			<-ctx.Done()
			return output, ctx.Err()
		}
		return output, nil
	}
	return toolresult.Result{}, fmt.Errorf("unknown call %s", call.Name)
}
func (e *partialOutputExecutor) FinalizeToolOutcome(call providers.ToolCall, result toolresult.Result) ToolFinalization {
	if call.Name == "parent" && e.finalizerFail {
		result.IsError = true
		result.Content[0].Text += " finalizer SECRET"
	}
	return ToolFinalization{Result: result}
}
func TestFailedRemotePTCSuppressesOnlyUncommittedMediaAndText(t *testing.T) {
	for _, tc := range []struct {
		name                                            string
		remote, nested, fail, finalizerFail, commitFail bool
		wantImages                                      int
		wantText                                        bool
		wantErr                                         bool
	}{
		{name: "ordinary-failure", fail: true, wantImages: 1, wantText: true},
		{name: "mixed-failure", remote: true, fail: true, wantImages: 1},
		{name: "nested-failure", remote: true, nested: true, fail: true, wantImages: 1},
		{name: "finalizer-failure", remote: true, finalizerFail: true, wantImages: 1},
		{name: "committed-success", remote: true, wantImages: 2, wantText: true},
		{name: "commit-failure", remote: true, commitFail: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &partialOutputExecutor{remote: tc.remote, nested: tc.nested, fail: tc.fail, finalizerFail: tc.finalizerFail, commitFail: tc.commitFail}
			ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "thread")
			require.NoError(t, err)
			run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: tc.name})
			defer run.Close()
			messages, err := run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "parent", Name: "parent", Arguments: `{}`}}, nil)
			if tc.wantErr {
				if err == nil {
					require.Len(t, messages, 1)
					require.True(t, messages[0].ToolResult.IsError)
					require.NotContains(t, messages[0].Content, "SECRET")
				}
				return
			}
			require.NoError(t, err)
			require.Len(t, messages, 1)
			require.NotNil(t, messages[0].ToolResult)
			output := messages[0].ToolResult
			count := 0
			for _, part := range output.Content {
				if part.Type == toolresult.ContentTypeImage {
					count++
				}
			}
			require.Equal(t, tc.wantImages, count, "ordinary identical media must survive while provisional media is removed")
			if tc.wantText {
				require.Contains(t, output.TextProjection(), "ORDINARY LOG")
			} else {
				require.NotContains(t, output.TextProjection(), "SECRET")
				require.NotContains(t, output.TextProjection(), "ORDINARY LOG")
			}
			if tc.remote && (tc.fail || tc.finalizerFail) {
				require.Greater(t, executor.aborted.Load(), int32(0))
				pending, err := ledger.PendingProjection(context.Background())
				require.NoError(t, err)
				require.Len(t, pending, 1)
				require.NotContains(t, pending[0].Result.JSONProjection(), "SECRET", "recovery must not project the aborted parent output")
			} else if tc.remote && !tc.commitFail {
				require.Equal(t, int32(1), executor.committed.Load())
			}
			if !tc.remote {
				require.True(t, strings.Contains(output.TextProjection(), "ORDINARY LOG"))
			}
		})
	}
}

func TestCanceledRemotePTCDiscardsPendingOutput(t *testing.T) {
	executor := &partialOutputExecutor{remote: true, entered: make(chan struct{})}
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "thread")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "cancel", RunContext: ctx})
	done := make(chan []providers.ChatMessage, 1)
	go func() {
		messages, _ := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "parent", Arguments: `{}`}}, nil)
		done <- messages
	}()
	select {
	case <-executor.entered:
	case <-time.After(10 * time.Second):
		cancel()
		run.Close()
		t.Fatal("remote child did not complete")
	}
	cancel()
	messages := <-done
	run.Close()
	for _, message := range messages {
		require.NotContains(t, message.Content, "SECRET")
	}
	require.Greater(t, executor.aborted.Load(), int32(0))
	pending, err := ledger.PendingProjection(context.Background())
	require.NoError(t, err)
	for _, invocation := range pending {
		require.NotContains(t, invocation.Result.JSONProjection(), "SECRET")
	}
}
