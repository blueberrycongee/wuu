package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// TestServerThreadForkPreservesPreCheckpointPastAndSelectedAnswer reproduces the
// fork regression where the active provider checkpoint was preferred over the
// durable transcript. Forking a positional target (turn-0002/item-2) must keep
// the pre-checkpoint past and stop exactly at the selected answer, not swallow
// the later subtask query and its read_file result.
func TestServerThreadForkPreservesPreCheckpointPastAndSelectedAnswer(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	sourceID := "20260910-010827-checkpoint-fork-source"
	if _, err := session.CreateWithMetadata(rt.SessionDir, sourceID, rt.RootDir); err != nil {
		t.Fatalf("create source session: %v", err)
	}

	// The durable transcript has a completed older turn (pre-checkpoint past) and
	// a recent turn whose answer is the fork target. The subtask query and tool
	// result come after that answer and must not leak into the fork.
	full := []providers.ChatMessage{
		{Seq: 1, Role: "user", Content: "older prompt"},
		{Seq: 2, Role: "assistant", Content: "older answer", Phase: providers.MessagePhaseFinalAnswer},
		{Seq: 3, Role: "user", Content: "recent prompt"},
		{Seq: 4, Role: "assistant", Content: "selected answer", Phase: providers.MessagePhaseFinalAnswer, ProviderItemID: "answer-item"},
		{Seq: 5, Role: "user", Content: "subtask update query", Steered: true},
		{Seq: 6, Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call-read", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
		{Seq: 7, Role: "tool", ToolCallID: "call-read", Name: "read_file", Content: "file contents"},
	}
	if err := rewriteChatHistory(rt.SessionDir, sourceID, full); err != nil {
		t.Fatalf("write source durable history: %v", err)
	}
	// The provider checkpoint keeps only the active model context; the older turn
	// survives only in the durable transcript.
	if err := rewriteChatHistoryAtBaseline(rt.SessionDir, sourceID, full[2:], 2); err != nil {
		t.Fatalf("write provider checkpoint: %v", err)
	}

	display, err := loadPersistedMessages(rt.SessionDir, sourceID, true)
	if err != nil {
		t.Fatalf("load source display history: %v", err)
	}
	turns := turnsFromPersistedHistory(sourceID, display, time.Unix(0, 0).UTC(), nil)
	targetTurn, targetItem := finalAnswerItemForForkTest(t, turns, "selected answer")
	turnID, itemID := targetTurn.ID, targetItem.ID
	if !strings.HasSuffix(turnID, "-turn-0002") || !strings.HasSuffix(itemID, "-item-2") {
		t.Fatalf("expected positional turn-0002/item-2 target, got %s/%s", turnID, itemID)
	}

	payload, err := json.Marshal(map[string]any{
		"id":     "fork-checkpoint-regression",
		"method": MethodThreadFork,
		"params": ThreadForkParams{
			ThreadID: sourceID,
			TurnID:   turnID,
			ItemID:   itemID,
			Mode:     "local",
		},
	})
	if err != nil {
		t.Fatalf("marshal fork request: %v", err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("thread/fork: %v", err)
	}

	response := responseByID(t, parseOutput(t, out.String()), "fork-checkpoint-regression")
	if response["error"] != nil {
		t.Fatalf("thread/fork returned error: %+v", response["error"])
	}
	fork := remarshal[ThreadForkResult](t, response["result"]).Thread

	history, err := loadChatMessages(rt.SessionDir, fork.ID)
	if err != nil {
		t.Fatalf("load fork history: %v", err)
	}
	var got []string
	for _, message := range visibleMessagesForTest(history) {
		got = append(got, message.Content)
	}
	want := []string{"older prompt", "older answer", "recent prompt", "selected answer"}
	if !slices.Equal(got, want) {
		t.Fatalf("fork history = %v, want %v", got, want)
	}
}

// TestServerThreadForkLiveAnswerDoesNotDuplicateArchivedToolPrefix covers the
// live-answer fallback: a running turn may already have archived its admitted
// user message and a tool call/result into the durable transcript, while the
// final answer is still in-memory. Materializing the fork must not emit the
// archived tool pair twice.
func TestServerThreadForkLiveAnswerDoesNotDuplicateArchivedToolPrefix(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	sourceID := "20260910-010827-live-prefix-source"
	if _, err := session.CreateWithMetadata(rt.SessionDir, sourceID, rt.RootDir); err != nil {
		t.Fatalf("create source session: %v", err)
	}

	// Durable transcript already contains the older turn plus the live turn's
	// user message and an archived read_file call/result. The final answer is
	// only in the in-memory turn below.
	durable := []providers.ChatMessage{
		{Seq: 1, Role: "user", Content: "older prompt"},
		{Seq: 2, Role: "assistant", Content: "older answer", Phase: providers.MessagePhaseFinalAnswer},
		{Seq: 3, Role: "user", Content: "inspect it"},
		{Seq: 4, Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call-live", Name: "read_file", Arguments: `{"path":"a.txt"}`}}},
		{Seq: 5, Role: "tool", ToolCallID: "call-live", Name: "read_file", Content: "live contents"},
	}
	if err := rewriteChatHistory(rt.SessionDir, sourceID, durable); err != nil {
		t.Fatalf("write source durable history: %v", err)
	}

	liveTurnID := sourceID + "-turn-0002"
	th := &threadState{
		ID:      sourceID,
		running: true,
		// Active model context has the same partial live turn but no final answer.
		History: []providers.ChatMessage{
			{Role: "user", Content: "inspect it"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call-live", Name: "read_file", Arguments: `{"path":"a.txt"}`}}},
			{Role: "tool", ToolCallID: "call-live", Name: "read_file", Content: "live contents"},
		},
		ModelProvider:  "fake-provider",
		Model:          "fake-model",
		EngineID:       "wuu",
		CWD:            rt.RootDir,
		PersistHistory: true,
		CreatedAt:      time.Unix(0, 0).UTC(),
		UpdatedAt:      time.Unix(0, 0).UTC(),
		LastAccessedAt: time.Unix(0, 0).UTC(),
		toolItems:      make(map[string]string),
		Turns: []Turn{
			{
				ID:     sourceID + "-turn-0001",
				Status: TurnStatusCompleted,
				Items: []ThreadItem{
					{ID: sourceID + "-turn-0001-item-1", Type: ThreadItemUserMessage, Status: ThreadItemStatusCompleted, Text: "older prompt"},
					{ID: sourceID + "-turn-0001-item-2", Type: ThreadItemAgentMessage, Status: ThreadItemStatusCompleted, Terminal: true, Text: "older answer"},
				},
			},
			{
				ID:     liveTurnID,
				Status: TurnStatusInProgress,
				Items: []ThreadItem{
					{ID: liveTurnID + "-item-1", Seq: 3, Type: ThreadItemUserMessage, Status: ThreadItemStatusCompleted, Text: "inspect it"},
					{ID: liveTurnID + "-item-2", SourceID: "call-live", Type: ThreadItemToolCall, Status: ThreadItemStatusCompleted, Name: "read_file", Arguments: `{"path":"a.txt"}`, Result: "live contents"},
					{ID: liveTurnID + "-item-3", SourceID: "msg-live", Type: ThreadItemAgentMessage, Status: ThreadItemStatusCompleted, Terminal: true, Text: "done"},
				},
			},
		},
	}
	srv.mu.Lock()
	srv.threads[sourceID] = th
	srv.mu.Unlock()

	payload, err := json.Marshal(map[string]any{
		"id":     "fork-live-prefix",
		"method": MethodThreadFork,
		"params": ThreadForkParams{
			ThreadID: sourceID,
			TurnID:   liveTurnID,
			ItemID:   liveTurnID + "-item-3",
			Mode:     "local",
		},
	})
	if err != nil {
		t.Fatalf("marshal fork request: %v", err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("thread/fork: %v", err)
	}

	response := responseByID(t, parseOutput(t, out.String()), "fork-live-prefix")
	if response["error"] != nil {
		t.Fatalf("thread/fork returned error: %+v", response["error"])
	}
	fork := remarshal[ThreadForkResult](t, response["result"]).Thread

	history, err := loadChatMessages(rt.SessionDir, fork.ID)
	if err != nil {
		t.Fatalf("load fork history: %v", err)
	}
	visible := visibleMessagesForTest(history)
	if len(visible) != 6 {
		t.Fatalf("fork history has %d visible messages, want 6: %+v", len(visible), visible)
	}
	if visible[0].Role != "user" || visible[0].Content != "older prompt" {
		t.Fatalf("fork lost pre-checkpoint user message: %+v", visible[0])
	}
	if visible[1].Role != "assistant" || visible[1].Content != "older answer" {
		t.Fatalf("fork lost pre-checkpoint answer: %+v", visible[1])
	}
	if visible[2].Role != "user" || visible[2].Content != "inspect it" {
		t.Fatalf("fork lost live user message: %+v", visible[2])
	}
	if visible[3].Role != "assistant" || len(visible[3].ToolCalls) != 1 || visible[3].ToolCalls[0].ID != "call-live" {
		t.Fatalf("fork tool call is wrong: %+v", visible[3])
	}
	if visible[4].Role != "tool" || visible[4].ToolCallID != "call-live" || visible[4].Content != "live contents" {
		t.Fatalf("fork tool result is wrong: %+v", visible[4])
	}
	if visible[5].Role != "assistant" || visible[5].Content != "done" || visible[5].Phase != providers.MessagePhaseFinalAnswer {
		t.Fatalf("fork final answer is wrong: %+v", visible[5])
	}
}

func TestServerThreadForkPublishesPersistedWorkspaceIdentity(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WorkspaceID = "project-stable"
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	sourceID := "fork-workspace-source"
	// A local fork can live outside the project's root, including in a worktree.
	sourceCWD := t.TempDir()
	if _, err := session.CreateWithMetadata(rt.SessionDir, sourceID, sourceCWD); err != nil {
		t.Fatal(err)
	}
	if _, err := session.SetWorkspaceID(rt.SessionDir, sourceID, rt.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	history := []providers.ChatMessage{
		{Seq: 1, Role: "user", Content: "prompt"},
		{Seq: 2, Role: "assistant", Content: "answer", Phase: providers.MessagePhaseFinalAnswer},
	}
	if err := rewriteChatHistory(rt.SessionDir, sourceID, history); err != nil {
		t.Fatal(err)
	}
	display, err := loadPersistedMessages(rt.SessionDir, sourceID, true)
	if err != nil {
		t.Fatal(err)
	}
	turns := turnsFromPersistedHistory(sourceID, display, time.Now().UTC(), nil)
	turn, item := finalAnswerItemForForkTest(t, turns, "answer")
	payload, err := json.Marshal(map[string]any{
		"id": "fork-workspace", "method": MethodThreadFork,
		"params": ThreadForkParams{ThreadID: sourceID, TurnID: turn.ID, ItemID: item.ID, Mode: "local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	messages := parseOutput(t, out.String())
	response := responseByID(t, messages, "fork-workspace")
	if response["error"] != nil {
		t.Fatalf("fork failed: %+v", response["error"])
	}
	fork := remarshal[ThreadForkResult](t, response["result"]).Thread
	started := remarshal[ThreadStartedNotification](t, notificationByMethod(t, messages, NotificationThreadStarted)["params"]).Thread
	for _, snapshot := range []Thread{fork, started} {
		if snapshot.WorkspaceID != rt.WorkspaceID || snapshot.CWD != sourceCWD {
			t.Fatalf("fork snapshot lost workspace identity: id=%s workspace=%q cwd=%q", snapshot.ID, snapshot.WorkspaceID, snapshot.CWD)
		}
	}
}

// interleavedThinkingForkClient streams one completed turn whose middle model
// step carries two thinking blocks, then blocks every later request so the
// next turn stays running.
type interleavedThinkingForkClient struct {
	firstText string
	mu        sync.Mutex
	calls     int
	blocked   chan struct{}
	once      sync.Once
}

func (c *interleavedThinkingForkClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, fmt.Errorf("unexpected non-streaming request")
}

func (c *interleavedThinkingForkClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.mu.Lock()
	call := c.calls
	c.calls++
	c.mu.Unlock()
	ch := make(chan providers.StreamEvent, 16)
	think := func(text string) {
		ch <- providers.StreamEvent{Type: providers.EventThinkingDelta, Content: text}
		ch <- providers.StreamEvent{Type: providers.EventThinkingDone}
	}
	useTool := func(id string) {
		tool := providers.ToolCall{ID: id, Name: "echo_tool", Arguments: `{}`}
		ch <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: id, Name: tool.Name}}
		ch <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &tool}
		ch <- providers.StreamEvent{Type: providers.EventDone, StopReason: "tool_use"}
	}
	switch call {
	case 0:
		think("plan the inspection")
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: c.firstText}
		useTool("call-a")
	case 1:
		think("first interleaved block")
		think("second interleaved block")
		useTool("call-b")
	case 2:
		think("summarize")
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Final answer for turn one."}
		ch <- providers.StreamEvent{Type: providers.EventDone, StopReason: "end_turn"}
	default:
		c.once.Do(func() { close(c.blocked) })
		go func() {
			defer close(ch)
			<-ctx.Done()
			ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
		}()
		return ch, nil
	}
	close(ch)
	return ch, nil
}

// TestServerThreadForkLiveAnswerAfterInterleavedThinking reproduces "fork target
// not found" for an answer the client saw streamed live. One provider message
// with several thinking blocks yields more live reasoning rows than its
// restored projection, so positional item ids diverge; fork must still resolve
// the answer from the identity carried by the completed turn.
func TestServerThreadForkLiveAnswerAfterInterleavedThinking(t *testing.T) {
	for _, firstText := range []string{"I will inspect it.", "Final answer for turn one."} {
		t.Run(firstText, func(t *testing.T) {
			client := &interleavedThinkingForkClient{firstText: firstText, blocked: make(chan struct{})}
			rt := newTestRuntime(t, &fakeClient{})
			rt.StreamRunner.Client = providers.AdaptStreamClient(client)
			rt.StreamRunner.Tools = fastToolExecutor{}
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)

			if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
				t.Fatalf("thread/start: %v", err)
			}
			threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
			if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"turn-1","method":"turn/start","params":{"thread_id":%q,"prompt":"inspect"}}`, threadID))); err != nil {
				t.Fatalf("turn/start: %v", err)
			}
			completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, waitForMethod(t, out, NotificationTurnCompleted), NotificationTurnCompleted)["params"])
			turn, answer := finalAnswerItemForForkTest(t, []Turn{completed.Turn}, "Final answer for turn one.")
			if answer.Seq <= 0 {
				t.Fatalf("completed answer has no durable identity: %+v", answer)
			}
			sourceHistory, err := loadChatMessages(rt.SessionDir, threadID)
			if err != nil {
				t.Fatalf("load completed source history: %v", err)
			}

			// The user forks the earlier answer while the next turn is still running.
			if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"turn-2","method":"turn/start","params":{"thread_id":%q,"prompt":"next"}}`, threadID))); err != nil {
				t.Fatalf("turn/start 2: %v", err)
			}
			<-client.blocked
			payload, err := json.Marshal(map[string]any{
				"id":     "fork",
				"method": MethodThreadFork,
				"params": ThreadForkParams{
					ThreadID: threadID,
					TurnID:   turn.ID,
					ItemID:   answer.ID,
					Mode:     "local",
					Target:   &ThreadForkTarget{Seq: answer.Seq, Type: answer.Type, SourceID: answer.SourceID},
				},
			})
			if err != nil {
				t.Fatalf("marshal fork request: %v", err)
			}
			if err := srv.handleLine(context.Background(), payload); err != nil {
				t.Fatalf("thread/fork: %v", err)
			}
			response := responseByID(t, parseOutput(t, out.String()), "fork")
			if response["error"] != nil {
				t.Fatalf("thread/fork returned error: %+v", response["error"])
			}
			fork := remarshal[ThreadForkResult](t, response["result"]).Thread
			history, err := loadChatMessages(rt.SessionDir, fork.ID)
			if err != nil {
				t.Fatalf("load fork history: %v", err)
			}
			visible := visibleMessagesForTest(history)
			want := visibleMessagesForTest(sourceHistory)
			// A fork owns new durable addresses and execution ledger entries, but the
			// message contents, reasoning blocks and tool call/result pairs must survive.
			for _, messages := range [][]providers.ChatMessage{visible, want} {
				for i := range messages {
					messages[i].Seq = 0
					messages[i].ToolInvocationID = ""
				}
			}
			if !reflect.DeepEqual(visible, want) {
				t.Fatalf("fork changed completed history:\n got %+v\nwant %+v", visible, want)
			}
			last := visible[len(visible)-1]
			if last.Role != "assistant" || last.Content != "Final answer for turn one." {
				t.Fatalf("fork must end at the selected answer, got %+v", last)
			}
			for _, msg := range visible {
				if msg.Role == "user" && msg.Content == "next" {
					t.Fatalf("fork included the later running turn: %+v", visible)
				}
			}
		})
	}
}
