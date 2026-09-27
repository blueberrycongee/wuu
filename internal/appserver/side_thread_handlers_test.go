package appserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/contextbudget"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sidethread"
)

type sideThreadTestClient struct {
	mu       sync.Mutex
	requests []providers.ChatRequest
	response string
	err      error
	partial  string
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (c *sideThreadTestClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	c.record(req)
	return providers.ChatResponse{Content: c.response}, c.err
}

func (c *sideThreadTestClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.record(req)
	if c.err != nil {
		return nil, c.err
	}
	ch := make(chan providers.StreamEvent, 3)
	go func() {
		defer close(ch)
		if c.partial != "" {
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: c.partial}
		}
		if c.started != nil {
			c.once.Do(func() { close(c.started) })
		}
		if c.release != nil {
			select {
			case <-c.release:
			case <-ctx.Done():
				ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
				return
			}
		}
		if c.response != "" {
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: c.response}
		}
		ch <- providers.StreamEvent{Type: providers.EventDone}
	}()
	return ch, nil
}

func (c *sideThreadTestClient) record(req providers.ChatRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cloned := req
	cloned.Messages = providers.CloneChatMessages(req.Messages)
	cloned.Tools = append([]providers.ToolDefinition(nil), req.Tools...)
	c.requests = append(c.requests, cloned)
}

func (c *sideThreadTestClient) lastRequest(t *testing.T) providers.ChatRequest {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("provider did not receive a request")
	}
	return c.requests[len(c.requests)-1]
}

func newSideThreadServer(t *testing.T, client providers.StreamClient) (*Server, *runtime.Session, *lockedBuffer) {
	t.Helper()
	root := t.TempDir()
	if client == nil {
		client = &sideThreadTestClient{response: "side answer"}
	}
	rt := &runtime.Session{
		ProviderName: "test-provider",
		Model:        "test-model",
		RootDir:      root,
		SessionDir:   filepath.Join(root, "sessions"),
		StreamRunner: &agent.StreamRunner{
			Client:       client,
			Model:        "test-model",
			SystemPrompt: "main coding-agent prompt",
		},
	}
	out := &lockedBuffer{}
	s := &Server{
		rt:              rt,
		out:             out,
		threads:         make(map[string]*threadState),
		sideThreadStore: sidethread.NewStore(filepath.Join(rt.SessionDir, "sidethreads")),
		sideTurns:       make(map[string]*sideThreadTurn),
	}
	t.Cleanup(func() {
		s.Close()
		s.backgroundWG.Wait()
	})
	return s, rt, out
}

func createSideThreadMain(t *testing.T, s *Server, id string) {
	t.Helper()
	if _, err := session.CreateWithMetadata(s.rt.SessionDir, id, s.rt.RootDir); err != nil {
		t.Fatalf("create main thread: %v", err)
	}
}

func waitForSideThreadStatus(t *testing.T, store *sidethread.Store, mainID string, want sidethread.Status) *sidethread.SideThread {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, err := store.Load(mainID)
		if err == nil && st.Status == want {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	st, err := store.Load(mainID)
	t.Fatalf("side thread status did not become %q: state=%+v err=%v", want, st, err)
	return nil
}

func waitForSideThreadDelta(t *testing.T, out *lockedBuffer, text string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), `"type":"delta"`) && strings.Contains(out.String(), text) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("side thread delta %q was not emitted:\n%s", text, out.String())
}

func TestOpenSideThreadIsLazyButRequiresMainThread(t *testing.T) {
	s, _, _ := newSideThreadServer(t, nil)
	createSideThreadMain(t, s, "main_lazy")
	res, err := s.openSideThread("main_lazy")
	if err != nil {
		t.Fatalf("openSideThread: %v", err)
	}
	if res.Summary != nil {
		t.Fatalf("lazy open materialized a record: %+v", res.Summary)
	}
	if _, err := s.openSideThread("missing"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("missing main thread error = %v", err)
	}
}

func TestOpenRecoversSideTurnAfterRemoteOwnerCrash(t *testing.T) {
	s, rt, _ := newSideThreadServer(t, nil)
	createSideThreadMain(t, s, "main_lazy_recovery")
	lease, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, sideThreadExecutionLeasePrefix+"main_lazy_recovery")
	if err != nil || !acquired {
		t.Fatalf("acquire owner lease: acquired=%t err=%v", acquired, err)
	}
	if _, err := s.sideThreadStore.BeginTurn("main_lazy_recovery", "status?", "user", "assistant"); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}

	live, err := s.openSideThread("main_lazy_recovery")
	if err != nil {
		t.Fatalf("open with live owner: %v", err)
	}
	if live.Summary == nil || live.Summary.Status != string(sidethread.StatusRunning) {
		t.Fatalf("live owner was recovered prematurely: %+v", live.Summary)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release crashed owner lease: %v", err)
	}

	recovered, err := s.openSideThread("main_lazy_recovery")
	if err != nil {
		t.Fatalf("open after owner crash: %v", err)
	}
	if recovered.Summary == nil || recovered.Summary.Status != string(sidethread.StatusInterrupted) {
		t.Fatalf("stale running state was not recovered: %+v", recovered.Summary)
	}
}

func TestGetSideThreadHistoryReturnsPersistedMessages(t *testing.T) {
	s, _, _ := newSideThreadServer(t, nil)
	createSideThreadMain(t, s, "main_history")
	if err := s.sideThreadStore.Save(&sidethread.SideThread{
		SideThreadID: "side-history",
		MainThreadID: "main_history",
		Status:       sidethread.StatusCompleted,
		Messages: []sidethread.Message{{
			ID: "message-1", SideThreadID: "side-history", Role: sidethread.RoleUser, Text: "进度?",
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := s.getSideThreadHistory("main_history")
	if err != nil {
		t.Fatalf("getSideThreadHistory: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "进度?" {
		t.Fatalf("history mismatch: %+v", res.Messages)
	}
}

func TestSendSideThreadMessageRunsReadOnlyModelAndPersistsReply(t *testing.T) {
	client := &sideThreadTestClient{response: "The main task is still running."}
	s, _, out := newSideThreadServer(t, client)
	createSideThreadMain(t, s, "main_send")
	res, err := s.sendSideThreadMessage("main_send", "What is the status?")
	if err != nil {
		t.Fatalf("sendSideThreadMessage: %v", err)
	}
	if res.UserMessageID == "" || res.Summary.SideThreadID == "" {
		t.Fatalf("incomplete send result: %+v", res)
	}
	st := waitForSideThreadStatus(t, s.sideThreadStore, "main_send", sidethread.StatusCompleted)
	// FinishTurn persists the terminal state before notifications are emitted.
	// Wait for the owned background turn so assertions never race that final
	// message/status delivery.
	s.backgroundWG.Wait()
	if len(st.Messages) != 2 {
		t.Fatalf("messages=%d want user+assistant", len(st.Messages))
	}
	if got := st.Messages[1]; got.Role != sidethread.RoleAssistant || got.Status != sidethread.AssistantCompleted || got.Text != client.response {
		t.Fatalf("assistant message mismatch: %+v", got)
	}
	if got := st.Messages[1].Items; len(got) != 1 || got[0].Type != string(ThreadItemAgentMessage) || got[0].Text != client.response {
		t.Fatalf("assistant canonical items mismatch: %+v", got)
	}
	req := client.lastRequest(t)
	if len(req.Tools) != 0 {
		t.Fatalf("side runner exposed write-capable tools: %+v", req.Tools)
	}
	if req.Messages[len(req.Messages)-1].Content != "What is the status?" {
		t.Fatalf("side prompt missing from request: %+v", req.Messages)
	}
	output := out.String()
	for _, want := range []string{`"method":"sideThread/event"`, `"type":"items"`, `"type":"delta"`, `"type":"message"`, `"status":"completed"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("notification output missing %s:\n%s", want, output)
		}
	}
}

func TestSideThreadItemProjectorPreservesCanonicalToolFlow(t *testing.T) {
	now := time.Now().UTC()
	projector := newSideThreadItemProjector("side-items", "turn-items", "test-provider", "test-model", t.TempDir(), now)
	call := providers.ToolCall{ID: "call-read", Name: "read_file", Arguments: `{"path":"README.md"}`}

	items, changed := projector.apply(providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}, now)
	if !changed || len(items) != 1 || items[0].Type != ThreadItemToolCall || items[0].Status != ThreadItemStatusInProgress {
		t.Fatalf("tool start items = %+v, changed=%v", items, changed)
	}
	items, changed = projector.apply(providers.StreamEvent{
		Type:       providers.EventToolUseEnd,
		ToolCall:   &call,
		ToolResult: "read result",
	}, now.Add(time.Millisecond))
	if !changed || len(items) != 1 || items[0].Result != "read result" || items[0].Status != ThreadItemStatusCompleted {
		t.Fatalf("tool result items = %+v, changed=%v", items, changed)
	}
	items, changed = projector.apply(providers.StreamEvent{
		Type:    providers.EventContentDelta,
		Content: "final answer",
		Phase:   providers.MessagePhaseFinalAnswer,
	}, now.Add(2*time.Millisecond))
	if !changed || len(items) != 2 || items[1].Type != ThreadItemAgentMessage || items[1].Text != "final answer" {
		t.Fatalf("answer items = %+v, changed=%v", items, changed)
	}

	items, changed = projector.apply(providers.StreamEvent{
		Type:    providers.EventMessage,
		Message: &providers.ChatMessage{Role: "assistant", Content: "final answer"},
	}, now.Add(2500*time.Microsecond))
	if !changed {
		t.Fatalf("complete assistant message should change items, got %+v", items)
	}

	items = projector.finish(TurnStatusCompleted, nil, now.Add(3*time.Millisecond))
	if items[1].Status != ThreadItemStatusCompleted || !items[1].Terminal {
		t.Fatalf("finished answer item = %+v", items[1])
	}
	stored := sideThreadStoredItems(items)
	if got := sideThreadWireItem(stored[0]); !reflect.DeepEqual(got, items[0]) {
		t.Fatalf("wire round trip = %+v, want %+v", got, items[0])
	}
}

func TestSideThreadSendWritesResponseBeforeEvents(t *testing.T) {
	s, _, out := newSideThreadServer(t, &sideThreadTestClient{response: "instant"})
	createSideThreadMain(t, s, "main_response_order")
	if err := s.handleLine(context.Background(), []byte(`{"id":"side-send","method":"sideThread/sendMessage","params":{"main_thread_id":"main_response_order","prompt":"status?"}}`)); err != nil {
		t.Fatalf("handleLine: %v", err)
	}
	s.backgroundWG.Wait()
	messages := parseOutput(t, out.String())
	if len(messages) == 0 || messages[0]["id"] != "side-send" || messages[0]["method"] != nil {
		t.Fatalf("first wire message is not the send response: %+v", messages)
	}
}

func TestSideThreadSendRejectsAttachments(t *testing.T) {
	s, _, out := newSideThreadServer(t, nil)
	createSideThreadMain(t, s, "main_text_only")
	if err := s.handleLine(context.Background(), []byte(`{"id":"side-attachment","method":"sideThread/sendMessage","params":{"main_thread_id":"main_text_only","prompt":"status?","images":[{"media_type":"image/png","data":"AA=="}]}}`)); err != nil {
		t.Fatalf("handleLine: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "side-attachment")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), `unknown field "images"`) {
		t.Fatalf("attachment must be rejected instead of silently ignored: %+v", response)
	}
	if _, err := s.sideThreadStore.Load("main_text_only"); !errors.Is(err, sidethread.ErrNotFound) {
		t.Fatalf("rejected attachment created side history: %v", err)
	}
}

func TestFairTokenAllocationsAreDeterministicAndBounded(t *testing.T) {
	texts := []string{"a", "b", "c"}
	for range 100 {
		got := fairTokenAllocations(texts, 2)
		if len(got) != 3 || got[0] != 1 || got[1] != 1 || got[2] != 0 {
			t.Fatalf("allocation = %v, want [1 1 0]", got)
		}
		if got[0]+got[1]+got[2] > 2 {
			t.Fatalf("allocation exceeds budget: %v", got)
		}
	}
}

func TestSideThreadSendRejectsConcurrentServers(t *testing.T) {
	client := &sideThreadTestClient{response: "done", started: make(chan struct{}), release: make(chan struct{})}
	owner, rt, _ := newSideThreadServer(t, client)
	createSideThreadMain(t, owner, "main_concurrent")
	if _, err := owner.sendSideThreadMessage("main_concurrent", "first"); err != nil {
		t.Fatalf("owner send: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("owner provider request did not start")
	}

	peerClient := &sideThreadTestClient{response: "must not run"}
	peer := &Server{
		rt: &runtime.Session{
			ProviderName: "test-provider",
			Model:        "test-model",
			RootDir:      rt.RootDir,
			SessionDir:   rt.SessionDir,
			StreamRunner: &agent.StreamRunner{Client: peerClient, Model: "test-model"},
		},
		out:             &lockedBuffer{},
		threads:         make(map[string]*threadState),
		sideThreadStore: sidethread.NewStore(filepath.Join(rt.SessionDir, "sidethreads")),
		sideTurns:       make(map[string]*sideThreadTurn),
	}
	if _, err := peer.sendSideThreadMessage("main_concurrent", "second"); !errors.Is(err, errSideThreadExecutionBusy) {
		t.Fatalf("contending send error = %v, want busy", err)
	}
	close(client.release)
	waitForSideThreadStatus(t, owner.sideThreadStore, "main_concurrent", sidethread.StatusCompleted)
	peerClient.mu.Lock()
	requestCount := len(peerClient.requests)
	peerClient.mu.Unlock()
	if requestCount != 0 {
		t.Fatalf("contending server reached provider %d time(s)", requestCount)
	}
}

func TestInterruptSideThreadCancelsAndPersistsPartialReply(t *testing.T) {
	client := &sideThreadTestClient{partial: "partial progress", started: make(chan struct{}), release: make(chan struct{})}
	s, _, out := newSideThreadServer(t, client)
	createSideThreadMain(t, s, "main_interrupt")
	if _, err := s.sendSideThreadMessage("main_interrupt", "status?"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	waitForSideThreadDelta(t, out, "partial progress")
	res, err := s.interruptSideThread("main_interrupt")
	if err != nil || !res.Ok {
		t.Fatalf("interrupt: result=%+v err=%v", res, err)
	}
	s.backgroundWG.Wait()
	st := waitForSideThreadStatus(t, s.sideThreadStore, "main_interrupt", sidethread.StatusInterrupted)
	if got := st.Messages[len(st.Messages)-1]; got.Status != sidethread.AssistantInterrupted || got.Text != "partial progress" {
		t.Fatalf("assistant was not interrupted: %+v", got)
	}
}

func TestResetSideThreadClearsHistoryAndNotifies(t *testing.T) {
	s, _, out := newSideThreadServer(t, nil)
	createSideThreadMain(t, s, "main_reset")
	if err := s.sideThreadStore.Save(&sidethread.SideThread{
		SideThreadID: "side-reset",
		MainThreadID: "main_reset",
		Status:       sidethread.StatusCompleted,
		Messages: []sidethread.Message{{
			ID: "message-1", SideThreadID: "side-reset", Role: sidethread.RoleUser, Text: "进度?",
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := s.resetSideThread("main_reset")
	if err != nil || !res.Ok {
		t.Fatalf("resetSideThread: result=%+v err=%v", res, err)
	}
	if _, err := s.sideThreadStore.Load("main_reset"); !errors.Is(err, sidethread.ErrNotFound) {
		t.Fatalf("record survived reset: %v", err)
	}
	output := out.String()
	for _, want := range []string{`"method":"sideThread/event"`, `"type":"reset"`, `"side_thread_id":"side-reset"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("notification output missing %s:\n%s", want, output)
		}
	}
	// A second reset finds nothing to clear and stays idempotent.
	res, err = s.resetSideThread("main_reset")
	if err != nil || !res.Ok {
		t.Fatalf("reset of absent record: result=%+v err=%v", res, err)
	}
}

func TestResetSideThreadCancelsRunningTurn(t *testing.T) {
	client := &sideThreadTestClient{partial: "partial progress", started: make(chan struct{}), release: make(chan struct{})}
	s, _, out := newSideThreadServer(t, client)
	createSideThreadMain(t, s, "main_reset_running")
	if _, err := s.sendSideThreadMessage("main_reset_running", "status?"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	waitForSideThreadDelta(t, out, "partial progress")
	res, err := s.resetSideThread("main_reset_running")
	if err != nil || !res.Ok {
		t.Fatalf("resetSideThread: result=%+v err=%v", res, err)
	}
	s.backgroundWG.Wait()
	// The interrupted turn's finish lands on the deleted record and must not
	// resurrect it.
	if _, err := s.sideThreadStore.Load("main_reset_running"); !errors.Is(err, sidethread.ErrNotFound) {
		t.Fatalf("record resurrected after reset during run: %v", err)
	}
}

func TestRemoteInterruptCancelsOwningSideThreadProcess(t *testing.T) {
	client := &sideThreadTestClient{partial: "partial progress", started: make(chan struct{}), release: make(chan struct{})}
	owner, rt, ownerOut := newSideThreadServer(t, client)
	createSideThreadMain(t, owner, "main_remote_interrupt")
	if _, err := owner.sendSideThreadMessage("main_remote_interrupt", "status?"); err != nil {
		t.Fatalf("owner send: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("owner provider request did not start")
	}
	waitForSideThreadDelta(t, ownerOut, "partial progress")

	peer := &Server{
		rt:              rt,
		out:             &lockedBuffer{},
		threads:         make(map[string]*threadState),
		sideThreadStore: sidethread.NewStore(filepath.Join(rt.SessionDir, "sidethreads")),
		sideTurns:       make(map[string]*sideThreadTurn),
	}
	res, err := peer.interruptSideThread("main_remote_interrupt")
	if err != nil || !res.Ok {
		t.Fatalf("peer interrupt: result=%+v err=%v", res, err)
	}
	owner.backgroundWG.Wait()
	close(client.release)

	st := waitForSideThreadStatus(t, owner.sideThreadStore, "main_remote_interrupt", sidethread.StatusInterrupted)
	if got := st.Messages[len(st.Messages)-1]; got.Status != sidethread.AssistantInterrupted || got.Text != "partial progress" {
		t.Fatalf("remote interrupt did not preserve the pre-interrupt partial reply: %+v", got)
	}
}

func TestSideThreadSnapshotIncludesLiveMainTurn(t *testing.T) {
	client := &sideThreadTestClient{response: "live status"}
	s, _, _ := newSideThreadServer(t, client)
	createSideThreadMain(t, s, "main_live")
	now := time.Now().UTC()
	history := []providers.ChatMessage{{Role: "system", Content: "main prompt"}, {Role: "user", Content: "LATEST_MAIN_OBJECTIVE"}}
	th := newThreadState("main_live", history, "live-provider", "live-model", s.rt.RootDir, true, now)
	th.running = true
	th.currentTurn = "turn-live"
	th.runningProviderName = "live-provider"
	th.runningModel = "live-model"
	th.Turns = []Turn{{
		ID: "turn-live", Status: TurnStatusInProgress, ItemsView: TurnItemsViewFull,
		Items: []ThreadItem{
			{ID: "user", Type: ThreadItemUserMessage, Status: ThreadItemStatusCompleted, Text: "LATEST_MAIN_OBJECTIVE"},
			{ID: "tool", Type: ThreadItemToolCall, Status: ThreadItemStatusInProgress, Name: "run_shell", Arguments: "go test ./...", Result: "LIVE_TOOL_STATE"},
		},
	}}
	s.threads["main_live"] = th
	if _, err := s.sendSideThreadMessage("main_live", "what is happening?"); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitForSideThreadStatus(t, s.sideThreadStore, "main_live", sidethread.StatusCompleted)
	requestText := requestMessagesText(client.lastRequest(t).Messages)
	for _, want := range []string{"LATEST_MAIN_OBJECTIVE", "LIVE_TOOL_STATE", "run_shell", "State: running", "live-provider/live-model"} {
		if !strings.Contains(requestText, want) {
			t.Fatalf("live snapshot missing %q:\n%s", want, requestText)
		}
	}
}

func TestSideThreadContextUsesResolvedModelBudget(t *testing.T) {
	client := &sideThreadTestClient{response: "bounded"}
	s, rt, _ := newSideThreadServer(t, client)
	rt.StreamRunner.MaxInputTokens = 600
	createSideThreadMain(t, s, "main_long")
	now := time.Now().UTC()
	history := []providers.ChatMessage{{Role: "system", Content: "main prompt"}}
	for range 100 {
		history = append(history, providers.ChatMessage{Role: "user", Content: strings.Repeat("old context ", 200)})
	}
	history = append(history, providers.ChatMessage{Role: "user", Content: "LATEST_LONG_MAIN_MARKER"})
	th := newThreadState("main_long", history, "provider", "model", rt.RootDir, true, now)
	th.running = true
	th.currentTurn = "turn-long"
	th.Turns = []Turn{{ID: "turn-long", Status: TurnStatusInProgress, ItemsView: TurnItemsViewFull, Items: []ThreadItem{{
		ID: "tool", Type: ThreadItemToolCall, Status: ThreadItemStatusInProgress, Name: "run_shell", Result: "LATEST_LIVE_TOOL_MARKER",
	}}}}
	s.threads["main_long"] = th
	if _, err := s.sendSideThreadMessage("main_long", "LATEST_SIDE_PROMPT_MARKER"); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitForSideThreadStatus(t, s.sideThreadStore, "main_long", sidethread.StatusCompleted)
	req := client.lastRequest(t)
	if estimated := contextbudget.EstimateMessagesTokens(req.Messages); estimated > rt.StreamRunner.MaxInputTokens {
		t.Fatalf("request estimate %d exceeds input budget %d", estimated, rt.StreamRunner.MaxInputTokens)
	}
	text := requestMessagesText(req.Messages)
	for _, marker := range []string{"LATEST_LONG_MAIN_MARKER", "LATEST_LIVE_TOOL_MARKER", "LATEST_SIDE_PROMPT_MARKER"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("bounded request dropped %q:\n%s", marker, text)
		}
	}
}

func TestSideThreadContextBoundsUnknownModelBudget(t *testing.T) {
	history := make([]providers.ChatMessage, 0, 102)
	for range 100 {
		history = append(history, providers.ChatMessage{Role: "user", Content: strings.Repeat("unknown old context ", 500)})
	}
	history = append(history, providers.ChatMessage{Role: "user", Content: "UNKNOWN_LATEST_MAIN_MARKER"})
	snapshot := sideThreadMainSnapshot{
		capturedAt: time.Now().UTC(),
		history:    history,
		running:    true,
		currentTurn: &Turn{Status: TurnStatusInProgress, Items: []ThreadItem{{
			Type: ThreadItemToolCall, Status: ThreadItemStatusInProgress, Name: "run_shell", Result: "UNKNOWN_LIVE_MARKER",
		}}},
	}
	messages := []sidethread.Message{
		{ID: "user", SideThreadID: "side", Role: sidethread.RoleUser, Text: "UNKNOWN_SIDE_MARKER"},
		{ID: "assistant", SideThreadID: "side", Role: sidethread.RoleAssistant, Status: sidethread.AssistantStreaming},
	}
	requestHistory, err := buildSideThreadRunHistory(&agent.StreamRunner{SystemPrompt: "read-only side chat"}, snapshot, messages, "assistant")
	if err != nil {
		t.Fatalf("buildSideThreadRunHistory: %v", err)
	}
	if estimated := contextbudget.EstimateMessagesTokens(requestHistory); estimated > unknownSideThreadInputBudget {
		t.Fatalf("unknown-model request estimate %d exceeds fallback budget %d", estimated, unknownSideThreadInputBudget)
	}
	text := requestMessagesText(requestHistory)
	for _, marker := range []string{"UNKNOWN_LATEST_MAIN_MARKER", "UNKNOWN_LIVE_MARKER", "UNKNOWN_SIDE_MARKER"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("unknown-model budget dropped %q", marker)
		}
	}
}

func TestSideThreadContextPreservesEntireActivePrompt(t *testing.T) {
	prompt := strings.Repeat("active prompt context ", 120) + "ACTIVE_PROMPT_TAIL_MARKER"
	snapshot := sideThreadMainSnapshot{
		capturedAt: time.Now().UTC(),
		history: []providers.ChatMessage{
			{Role: "user", Content: strings.Repeat("older main context ", 2_000)},
		},
	}
	messages := []sidethread.Message{
		{ID: "user", SideThreadID: "side", Role: sidethread.RoleUser, Text: prompt},
		{ID: "assistant", SideThreadID: "side", Role: sidethread.RoleAssistant, Status: sidethread.AssistantStreaming},
	}
	history, err := buildSideThreadRunHistory(&agent.StreamRunner{SystemPrompt: "read-only side chat", MaxInputTokens: 800}, snapshot, messages, "assistant")
	if err != nil {
		t.Fatalf("buildSideThreadRunHistory: %v", err)
	}
	if got := history[len(history)-1].Content; got != prompt {
		t.Fatalf("active prompt was truncated: got tail=%q want tail marker", got[max(0, len(got)-40):])
	}
}

func TestSideThreadSendRejectsOversizedPromptBeforePersisting(t *testing.T) {
	s, rt, _ := newSideThreadServer(t, nil)
	rt.StreamRunner.MaxInputTokens = 200
	createSideThreadMain(t, s, "main_oversized_prompt")

	_, err := s.sendSideThreadMessage("main_oversized_prompt", strings.Repeat("oversized active prompt ", 500))
	if err == nil || !strings.Contains(err.Error(), "prompt requires") {
		t.Fatalf("oversized send error = %v", err)
	}
	if _, err := s.sideThreadStore.Load("main_oversized_prompt"); !errors.Is(err, sidethread.ErrNotFound) {
		t.Fatalf("rejected prompt changed durable side history: %v", err)
	}
}

func TestSideThreadSendRunnerValidationDoesNotPersist(t *testing.T) {
	s, rt, _ := newSideThreadServer(t, nil)
	rt.StreamRunner = nil
	createSideThreadMain(t, s, "main_missing_runner")

	if _, err := s.sendSideThreadMessage("main_missing_runner", "status?"); err == nil || !strings.Contains(err.Error(), "stream runner") {
		t.Fatalf("missing runner error = %v", err)
	}
	if _, err := s.sideThreadStore.Load("main_missing_runner"); !errors.Is(err, sidethread.ErrNotFound) {
		t.Fatalf("runner validation failure changed durable side history: %v", err)
	}
}

func TestSideThreadRejectedPromptLeavesExistingHistoryUnchanged(t *testing.T) {
	s, rt, _ := newSideThreadServer(t, nil)
	rt.StreamRunner.MaxInputTokens = 200
	createSideThreadMain(t, s, "main_existing_oversized")
	if err := s.sideThreadStore.Save(&sidethread.SideThread{
		SideThreadID: "existing-side",
		MainThreadID: "main_existing_oversized",
		Status:       sidethread.StatusCompleted,
		Messages: []sidethread.Message{{
			ID: "existing-user", SideThreadID: "existing-side", Role: sidethread.RoleUser, Text: "existing history",
		}},
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	before, err := s.sideThreadStore.Load("main_existing_oversized")
	if err != nil {
		t.Fatalf("load before: %v", err)
	}

	if _, err := s.sendSideThreadMessage("main_existing_oversized", strings.Repeat("oversized active prompt ", 500)); err == nil {
		t.Fatal("oversized prompt unexpectedly succeeded")
	}
	after, err := s.sideThreadStore.Load("main_existing_oversized")
	if err != nil {
		t.Fatalf("load after: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected prompt mutated existing history:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestSideThreadSendPreservesLongAcceptedPromptEndToEnd(t *testing.T) {
	client := &sideThreadTestClient{response: "accepted"}
	s, rt, _ := newSideThreadServer(t, client)
	rt.StreamRunner.MaxInputTokens = 1200
	createSideThreadMain(t, s, "main_long_prompt")
	prompt := strings.Repeat("active prompt context ", 120) + "ACTIVE_PROMPT_TAIL_MARKER"

	if _, err := s.sendSideThreadMessage("main_long_prompt", prompt); err != nil {
		t.Fatalf("send long prompt: %v", err)
	}
	st := waitForSideThreadStatus(t, s.sideThreadStore, "main_long_prompt", sidethread.StatusCompleted)
	if len(st.Messages) < 1 || st.Messages[0].Text != prompt {
		t.Fatalf("durable user prompt was truncated: %+v", st.Messages)
	}
	request := client.lastRequest(t)
	if got := request.Messages[len(request.Messages)-1].Content; got != prompt {
		t.Fatalf("provider prompt was truncated: %q", got)
	}
}

func TestAbortAcceptedSideThreadRollsBackAndSurfacesCause(t *testing.T) {
	s, _, _ := newSideThreadServer(t, nil)
	createSideThreadMain(t, s, "main_abort")
	if _, err := s.sideThreadStore.BeginTurn("main_abort", "status?", "user-1", "assistant-1"); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}

	err := s.abortAcceptedSideThread("main_abort", "user-1", "assistant-1", errServerClosed)
	if !errors.Is(err, errServerClosed) {
		t.Fatalf("abort error = %v, want errServerClosed", err)
	}
	if _, loadErr := s.sideThreadStore.Load("main_abort"); !errors.Is(loadErr, sidethread.ErrNotFound) {
		t.Fatalf("never-launched turn left a durable record: %v", loadErr)
	}
}

func TestSideThreadPersistFailureSurfacesExplicitFailure(t *testing.T) {
	client := &sideThreadTestClient{response: "completed answer", started: make(chan struct{}), release: make(chan struct{})}
	s, rt, out := newSideThreadServer(t, client)
	createSideThreadMain(t, s, "main_persist_failure")
	if _, err := s.sendSideThreadMessage("main_persist_failure", "status?"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	// Corrupt the durable record so FinishTurn cannot commit the reply.
	recordPath := filepath.Join(rt.SessionDir, "sidethreads", "main_persist_failure.json")
	if err := os.WriteFile(recordPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatalf("corrupt record: %v", err)
	}
	close(client.release)
	s.backgroundWG.Wait()

	output := out.String()
	for _, want := range []string{`"type":"message"`, `"type":"error"`, `"status":"failed"`, "persist side thread reply", "completed answer"} {
		if !strings.Contains(output, want) {
			t.Fatalf("persist failure output missing %s:\n%s", want, output)
		}
	}
	if strings.Contains(output, `"status":"interrupted"`) {
		t.Fatalf("completed reply was settled as an interruption:\n%s", output)
	}
}

func requestMessagesText(messages []providers.ChatMessage) string {
	var body strings.Builder
	for _, message := range messages {
		body.WriteString(message.Content)
		body.WriteByte('\n')
	}
	return body.String()
}

// A side chat must answer with the model its conversation is locked to, not the
// workspace default (which drifts when another session switches model). An
// empty selection keeps the workspace clone; a pinned selection repins the
// side runner to that model.
func TestNewSideThreadRunnerInheritsThreadModel(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeSelectionTestConfig(t, rt.ConfigPath)

	base, err := rt.NewSideThreadRunner("side-default", rt.RootDir, runtime.ThreadModelSelection{})
	if err != nil {
		t.Fatalf("NewSideThreadRunner default: %v", err)
	}
	if base.Model != "fake-model" {
		t.Fatalf("default side runner model = %q, want fake-model", base.Model)
	}

	pinned, err := rt.NewSideThreadRunner("side-pinned", rt.RootDir, runtime.ThreadModelSelection{
		Provider: "fake-provider",
		Model:    "pinned-model",
	})
	if err != nil {
		t.Fatalf("NewSideThreadRunner pinned: %v", err)
	}
	if pinned.Model != "pinned-model" {
		t.Fatalf("pinned side runner model = %q, want pinned-model", pinned.Model)
	}
}

// mainThreadModelSelection sources the conversation's pinned model from the
// cached thread when present and otherwise from the persisted session row, so
// a side chat opened on an idle thread still inherits the conversation's model.
func TestMainThreadModelSelectionPrefersCacheThenSession(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	srv := New(rt, &lockedBuffer{})
	cachedRoot := t.TempDir()

	if _, err := session.CreateWithMetadata(rt.SessionDir, "sel-thread", rt.RootDir); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := session.SetRuntimeSelection(rt.SessionDir, "sel-thread", session.RuntimeSelection{
		Provider: "fake-provider",
		Model:    "persisted-model",
	}); err != nil {
		t.Fatalf("set runtime selection: %v", err)
	}

	// Not cached: fall back to the persisted session row.
	if got := srv.mainThreadModelSelection("sel-thread"); got.Model != "persisted-model" {
		t.Fatalf("uncached selection model = %q, want persisted-model", got.Model)
	}
	if got := srv.mainThreadRoot("sel-thread"); got != rt.RootDir {
		t.Fatalf("uncached root = %q, want persisted %q", got, rt.RootDir)
	}

	// Cached thread state wins over the persisted row.
	th := newThreadState("sel-thread", nil, "fake-provider", "cached-model", cachedRoot, true, time.Now().UTC())
	srv.mu.Lock()
	srv.threads[th.ID] = th
	srv.mu.Unlock()
	if got := srv.mainThreadModelSelection("sel-thread"); got.Model != "cached-model" {
		t.Fatalf("cached selection model = %q, want cached-model", got.Model)
	}
	if got := srv.mainThreadRoot("sel-thread"); got != cachedRoot {
		t.Fatalf("cached root = %q, want %q", got, cachedRoot)
	}

	// Unknown thread yields an empty (workspace-default) selection.
	if got := srv.mainThreadModelSelection("missing-thread"); got.Provider != "" || got.Model != "" {
		t.Fatalf("unknown thread selection = %+v, want empty", got)
	}
}
