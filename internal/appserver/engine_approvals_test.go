package appserver

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestEngineApprovalUsesConversationQuestionBroker(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserQuestions = pluginhost.NewUserQuestionBroker()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)

	result := make(chan agentengine.ApprovalDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decision, err := srv.requestEngineApproval(context.Background(), "wuu-thread", "wuu-turn", agentengine.ApprovalRequest{
			Kind:     agentengine.ApprovalCommandExecution,
			EngineID: agentengine.EngineID("codex"),
			ItemID:   "item-1",
			Command:  "git status",
			CWD:      "/workspace",
			Reason:   "inspect the repository",
		})
		if err != nil {
			errCh <- err
			return
		}
		result <- decision
	}()

	var pending pluginhost.UserQuestionRequest
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		questions := rt.UserQuestions.List("wuu-thread")
		if len(questions) == 1 {
			pending = questions[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pending.RequestID == "" {
		t.Fatal("approval did not publish a conversation question")
	}
	if pending.TurnID != "wuu-turn" || pending.CallID != "item-1" {
		t.Fatalf("approval owner = %+v", pending)
	}
	question := pending.Questions[0]
	if question.Detail == "" || len(question.Options) != 3 {
		t.Fatalf("approval details/options = %+v", question)
	}
	if err := rt.UserQuestions.Respond(pending.RequestID, pluginhost.UserQuestionAnswer{
		Answers: []pluginhost.UserQuestionAnswerItem{{
			ID: engineApprovalQuestionID(agentengine.ApprovalCommandExecution), Selected: []string{engineApprovalAllowSession},
		}},
	}); err != nil {
		t.Fatalf("respond to approval: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatal(err)
	case decision := <-result:
		if decision != agentengine.ApprovalAcceptForSession {
			t.Fatalf("decision = %q", decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval did not resume after the broker response")
	}
}

func TestEngineApprovalFailsClosedWithoutBroker(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserQuestions = nil
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	decision, err := srv.requestEngineApproval(context.Background(), "thread", "turn", agentengine.ApprovalRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if decision != agentengine.ApprovalDecline {
		t.Fatalf("decision = %q, want decline", decision)
	}
}
