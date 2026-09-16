package appserver

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestNativeToolReviewerAsksUserWhenUnsure(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserQuestions = pluginhost.NewUserQuestionBroker()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)

	result := make(chan approvefor.Decision, 1)
	errCh := make(chan error, 1)
	go func() {
		decision, err := nativeToolReviewer{server: srv}.Review(context.Background(), approvefor.Request{
			SessionID:      "wuu-thread",
			TurnID:         "wuu-turn",
			CallID:         "call-1",
			PermissionMode: "standard",
			Tool:           approvefor.Tool{Name: "bash", Kind: "shell", Risk: "high"},
			Arguments:      `{"command":"rm -rf build"}`,
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
		t.Fatal("unsure review did not publish a blocking question")
	}
	if pending.Questions[0].ID != approvefor.QuestionID {
		t.Fatalf("question = %+v", pending.Questions[0])
	}
	if err := rt.UserQuestions.Respond(pending.RequestID, pluginhost.UserQuestionAnswer{
		Answers: []pluginhost.UserQuestionAnswerItem{{
			ID: approvefor.QuestionID, Selected: []string{approvefor.AllowOnceLabel},
		}},
	}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	select {
	case err := <-errCh:
		t.Fatal(err)
	case decision := <-result:
		if decision.Outcome != approvefor.OutcomeAllow {
			t.Fatalf("decision = %+v", decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("review did not resume after the user allowed once")
	}
}

func TestParseNativeReviewResponse(t *testing.T) {
	outcome, reason, err := parseNativeReviewResponse("```json\n{\"outcome\":\"deny\",\"reason\":\"destructive\"}\n```")
	if err != nil || outcome != approvefor.OutcomeDeny || reason != "destructive" {
		t.Fatalf("got %s %q %v", outcome, reason, err)
	}
	outcome, _, err = parseNativeReviewResponse("not json")
	if err != nil || outcome != approvefor.OutcomeUnsure {
		t.Fatalf("invalid json should be unsure, got %s %v", outcome, err)
	}
}
