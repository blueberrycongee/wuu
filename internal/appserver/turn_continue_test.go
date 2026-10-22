package appserver

import (
	"context"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Exercise the public admission path: continuation has no user payload and
// must remain fenced after completion, cross-process refresh and restart.
func TestTurnContinueAdmissionAndRestart(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "continued"}}
	rt := newTestRuntime(t, client)
	sess, err := session.CreateWithMetadata(rt.SessionDir, "continue-admission", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendChatMessages(rt.SessionDir, sess.ID, []providers.ChatMessage{
		{Role: "user", Content: "original task"}, {Role: "assistant", Content: "completed progress"},
	}); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	source := sess.ID + "-turn-0001"
	submit := func(s *Server, id, after string) {
		t.Helper()
		if err := s.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":%q,"method":"turn/start","params":{"thread_id":%q,"resume_after_turn_id":%q}}`, id, sess.ID, after))); err != nil {
			t.Fatal(err)
		}
	}
	external := acquireExternalThreadLease(t, rt.SessionDir, sess.ID)
	submit(srv, "busy", source)
	assertCrossServerBusyResponse(t, out, "busy")
	if err := external.Release(); err != nil {
		t.Fatal(err)
	}
	submit(srv, "continue", source)
	response := responseByID(t, parseOutput(t, out.String()), "continue")
	if response["error"] != nil {
		t.Fatalf("continuation rejected: %+v", response)
	}
	result := remarshal[TurnStartResult](t, response["result"])
	if result.Turn.ID == source || len(result.Turn.Items) != 0 {
		t.Fatalf("not an empty new turn: %+v", result.Turn)
	}
	waitForTurnCompletedForThread(t, out, sess.ID)
	waitForThreadLeaseRelease(t, rt.SessionDir, sess.ID)
	submit(srv, "duplicate", source)
	if responseByID(t, parseOutput(t, out.String()), "duplicate")["error"] == nil {
		t.Fatal("late duplicate admitted")
	}
	assertFakeClientRequestCount(t, client, 1)
	records, err := session.LoadHistoryRecords(rt.SessionDir, sess.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	users := 0
	for _, record := range records {
		if record.Role == "user" {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("duplicated task: %+v", records)
	}
	reloadedOut := &lockedBuffer{}
	reloaded := New(rt, reloadedOut)
	t.Cleanup(reloaded.Close)
	th, err := reloaded.ensureThreadLoaded(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Turns) != 2 || th.Turns[1].ID != result.Turn.ID || th.Turns[1].Status != TurnStatusCompleted {
		t.Fatalf("restart lost boundary: %+v", th.Turns)
	}
	submit(reloaded, "restart-duplicate", source)
	if responseByID(t, parseOutput(t, reloadedOut.String()), "restart-duplicate")["error"] == nil {
		t.Fatal("restart admitted stale continuation")
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	req := client.requests[0]
	if len(req.Messages) < 2 || req.Messages[len(req.Messages)-1].Content != "completed progress" {
		t.Fatalf("lost active history: %+v", req.Messages)
	}
}

func TestTurnContinueRecoversLedgerAndRejectsUnknownEffect(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint("unknown=", unknown), func(t *testing.T) {
			client := &fakeClient{response: providers.ChatResponse{Content: "finished"}}
			rt := newTestRuntime(t, client)
			sess, err := session.CreateWithMetadata(rt.SessionDir, session.NewID(), rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			if err := appendChatMessages(rt.SessionDir, sess.ID, []providers.ChatMessage{{Role: "user", Content: "write once"}, {Role: "assistant", Content: "partial must stay display only", Cause: providers.IncompleteStreamFragment}}); err != nil {
				t.Fatal(err)
			}
			ledger, err := toolledger.New(rt.SessionDir, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			batch, err := ledger.BeginBatch(context.Background(), "failed-operation", 0)
			if err != nil {
				t.Fatal(err)
			}
			invocation, err := ledger.Prepare(context.Background(), batch, providers.ToolCall{ID: "effect-1", Name: "write_file", Arguments: `{"path":"done.txt"}`}, toolledger.ReplayAtMostOnce)
			if err != nil {
				t.Fatal(err)
			}
			if err := ledger.FinalizeBatch(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
			if err := ledger.Start(context.Background(), invocation.ID); err != nil {
				t.Fatal(err)
			}
			if !unknown {
				if err := ledger.Settle(context.Background(), invocation.ID, toolresult.FromText("already written")); err != nil {
					t.Fatal(err)
				}
			}
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"continue","method":"turn/start","params":{"thread_id":%q,"resume_after_turn_id":%q}}`, sess.ID, sess.ID+"-turn-0001"))); err != nil {
				t.Fatal(err)
			}
			reply := responseByID(t, parseOutput(t, out.String()), "continue")
			if unknown {
				if reply["error"] == nil {
					t.Fatal("unknown at-most-once effect resumed")
				}
				assertFakeClientRequestCount(t, client, 0)
				return
			}
			if reply["error"] != nil {
				t.Fatalf("settled effect blocked: %+v", reply)
			}
			waitForTurnCompletedForThread(t, out, sess.ID)
			waitForThreadLeaseRelease(t, rt.SessionDir, sess.ID)
			client.mu.Lock()
			defer client.mu.Unlock()
			req := client.requests[0]
			toolResults, users := 0, 0
			for _, msg := range req.Messages {
				if msg.Role == "user" {
					users++
				}
				if msg.Cause == providers.IncompleteStreamFragment || strings.Contains(msg.Content, "partial must") {
					t.Fatalf("fragment leaked: %+v", req.Messages)
				}
				if msg.Role == "tool" && msg.ToolCallID == "effect-1" && msg.Content == "already written" {
					toolResults++
				}
			}
			if users != 1 || toolResults != 1 {
				t.Fatalf("continuation lost effect or duplicated task: %+v", req.Messages)
			}
		})
	}
}

type stopContinueClient struct {
	calls   atomic.Int32
	started chan struct{}
}

func (c *stopContinueClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}
func (c *stopContinueClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent)
	n := c.calls.Add(1)
	go func() {
		defer close(ch)
		if n == 1 {
			close(c.started)
			<-ctx.Done()
			return
		}
		select {
		case ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "continued after stop"}:
		case <-ctx.Done():
			return
		}
		select {
		case ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}
func TestTurnContinueOnlyExplicitlyAfterStop(t *testing.T) {
	client := &stopContinueClient{started: make(chan struct{})}
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	sess, err := session.CreateWithMetadata(rt.SessionDir, session.NewID(), rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	request := func(id, method, extra string) {
		t.Helper()
		if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":%q,"method":%q,"params":{"thread_id":%q%s}}`, id, method, sess.ID, extra))); err != nil {
			t.Fatal(err)
		}
	}
	request("start", "turn/start", `,"prompt":"long task"`)
	select {
	case <-client.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first request did not start")
	}
	request("stop", "turn/interrupt", "")
	waitForThreadLeaseRelease(t, rt.SessionDir, sess.ID)
	th, err := srv.ensureThreadLoaded(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	th.mu.Lock()
	running := th.running
	source := th.Turns[len(th.Turns)-1]
	th.mu.Unlock()
	if running || source.Status != TurnStatusInterrupted || client.calls.Load() != 1 {
		t.Fatalf("stop auto-resumed: running=%v turn=%+v calls=%d", running, source, client.calls.Load())
	}
	request("continue", "turn/start", fmt.Sprintf(`,"resume_after_turn_id":%q`, source.ID))
	waitForTurnCompletedForThread(t, out, sess.ID)
	waitForThreadLeaseRelease(t, rt.SessionDir, sess.ID)
	if client.calls.Load() != 2 {
		t.Fatalf("requests=%d", client.calls.Load())
	}
}

func TestTurnContinueBoundarySurvivesHistoryRewrite(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := session.CreateWithMetadata(rt.SessionDir, session.NewID(), rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendChatMessages(rt.SessionDir, sess.ID, []providers.ChatMessage{{Role: "user", Content: "original"}, {Role: "assistant", Content: "first"}}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, sess.ID, session.HistoryRecord{Role: "meta", Content: turnContinueHistoryRecord, ClientID: sess.ID + "-turn-0002"}); err != nil {
		t.Fatal(err)
	}
	if err := appendChatMessages(rt.SessionDir, sess.ID, []providers.ChatMessage{{Role: "assistant", Content: "resumed"}, {Role: "user", Content: "follow up"}, {Role: "assistant", Content: "last"}}); err != nil {
		t.Fatal(err)
	}
	history, err := loadChatMessages(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteChatHistory(rt.SessionDir, sess.ID, history); err != nil {
		t.Fatal(err)
	}
	rows, err := loadPersistedMessages(rt.SessionDir, sess.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	turns := turnsFromPersistedHistory(sess.ID, rows, time.Now().UTC(), nil)
	if len(turns) != 3 || len(turns[1].Items) != 1 || turns[1].Items[0].Text != "resumed" || len(turns[2].Items) != 2 || turns[2].Items[0].Text != "follow up" {
		t.Fatalf("rewrite reordered continuation boundary: %+v", turns)
	}
}
