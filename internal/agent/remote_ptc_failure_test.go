package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	remote, nested, fail, finalizerFail, commitFail, omitRemoteMedia, derivedMedia bool
	echo                                                                           bool
	committed, aborted                                                             atomic.Int32
	entered                                                                        chan struct{}
	childComplete, parentRelease                                                   chan struct{}
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
	case "derived":
		return toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte(call.Arguments))}}}, nil
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
			if e.derivedMedia {
				derived, err := n.Invoke(ctx, providers.ToolCall{ID: "derived", Name: "derived", Arguments: fmt.Sprintf(`{"value":%q}`, child.TextProjection())})
				if err != nil {
					return toolresult.Result{}, err
				}
				output.Content = append(output.Content, derived.Content...)
			}
			output.Content[0].Text += " remote SECRET"
			if !e.omitRemoteMedia {
				output.Content = append(output.Content, child.Content[1:]...)
			}
			if e.echo {
				if _, err := n.Invoke(ctx, providers.ToolCall{ID: "echo", Name: "ordinary", Arguments: `{"value":"remote SECRET"}`}); err != nil {
					return toolresult.Result{}, err
				}
			}
			if e.childComplete != nil {
				close(e.childComplete)
				<-e.parentRelease
			}
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
	if call.Name == "remote" {
		return ToolFinalization{Result: result, AdditionalContext: "remote SECRET context"}
	}
	if call.Name == "ordinary" {
		return ToolFinalization{Result: result, AdditionalContext: "ordinary context"}
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
		{name: "mixed-failure", remote: true, fail: true, wantImages: 0},
		{name: "nested-failure", remote: true, nested: true, fail: true, wantImages: 0},
		{name: "finalizer-failure", remote: true, finalizerFail: true, wantImages: 0},
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
			require.Equal(t, tc.wantImages, count, "failed remote parents suppress all media, while ordinary and successful parents retain it")
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

func TestRemoteNestedProjectionWaitsForParentSettlement(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			executor := &partialOutputExecutor{remote: true, nested: true, echo: true, fail: fail, childComplete: make(chan struct{}), parentRelease: make(chan struct{})}
			ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "thread")
			require.NoError(t, err)
			run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "projection"})
			defer run.Close()
			var mu sync.Mutex
			var details []string
			run.SetResultCallback(func(call providers.ToolCall, result toolresult.Result) {
				mu.Lock()
				defer mu.Unlock()
				details = append(details, call.Arguments+result.TextProjection())
			})
			done := make(chan error, 1)
			go func() {
				_, err := run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "parent", Name: "parent", Arguments: `{}`}}, nil)
				done <- err
			}()
			select {
			case <-executor.childComplete:
			case <-time.After(10 * time.Second):
				close(executor.parentRelease)
				t.Fatal("nested read did not complete")
			}
			mu.Lock()
			before, _ := json.Marshal(details)
			mu.Unlock()
			pending, _ := json.Marshal(run.TakeRequestContextSegments())
			close(executor.parentRelease)
			require.NotContains(t, string(before), "SECRET", "nested result must wait for settlement")
			require.NotContains(t, string(pending), "SECRET", "nested context must wait for settlement")
			require.NoError(t, <-done)
			mu.Lock()
			detail, _ := json.Marshal(details)
			mu.Unlock()
			segments, _ := json.Marshal(run.TakeRequestContextSegments())
			if fail {
				require.NotContains(t, string(detail), "SECRET")
				require.NotContains(t, string(segments), "SECRET")
				require.NotContains(t, string(segments), "ordinary context")
			} else {
				require.Contains(t, string(detail), "SECRET")
				require.Contains(t, string(segments), "SECRET")
			}
		})
	}
}

func TestOrdinaryNestedProjectionRemainsAvailableAfterParentCompletion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			executor := &partialOutputExecutor{fail: fail}
			ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "thread")
			require.NoError(t, err)
			run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "ordinary"})
			defer run.Close()
			var mu sync.Mutex
			var names []string
			run.SetResultCallback(func(call providers.ToolCall, _ toolresult.Result) {
				mu.Lock()
				defer mu.Unlock()
				names = append(names, call.Name)
			})
			_, err = run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "parent", Name: "parent", Arguments: `{}`}}, nil)
			require.NoError(t, err)
			mu.Lock()
			got := append([]string(nil), names...)
			mu.Unlock()
			require.Contains(t, got, "ordinary")
			segments, _ := json.Marshal(run.TakeRequestContextSegments())
			require.Contains(t, string(segments), "ordinary context")
		})
	}
}

func TestAcceptedNestedContextWaitsForProjectionDelivery(t *testing.T) {
	root := &toolRun{state: toolRunDone, done: make(chan struct{})}
	close(root.done)
	segment := postToolAdditionalContextSegment("child", "ordinary context")
	child := &toolRun{parent: root, state: toolRunDone, deferredProjection: true, requestContext: &segment}
	run := &TurnToolRuntime{runs: []*toolRun{root, child}}
	require.Empty(t, run.TakeRequestContextSegments())
	child.mu.Lock()
	child.projectionDelivered = true
	child.mu.Unlock()
	segments, _ := json.Marshal(run.TakeRequestContextSegments())
	require.Contains(t, string(segments), "ordinary context")
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

func TestFailedRemotePTCSuppressesIdenticalOrdinaryMediaWhenRemoteMediaWasNotAdmitted(t *testing.T) {
	executor := &partialOutputExecutor{remote: true, fail: true, omitRemoteMedia: true}
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "thread")
	require.NoError(t, err)
	run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "omitted-media"})
	defer run.Close()
	messages, err := run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "parent", Name: "parent", Arguments: `{}`}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Len(t, messages[0].ToolResult.Content, 1, "all media is suppressed after remote exposure")
}

func TestFailedRemotePTCSuppressesMediaDerivedByOrdinaryChild(t *testing.T) {
	executor := &partialOutputExecutor{remote: true, fail: true, derivedMedia: true}
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "thread")
	require.NoError(t, err)
	run := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "derived-media"})
	defer run.Close()
	messages, err := run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "parent", Name: "parent", Arguments: `{}`}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Len(t, messages[0].ToolResult.Content, 1, "ordinary media derived from remote text is not safe on failure")
}
