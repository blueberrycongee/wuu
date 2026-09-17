package appserver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
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
			UserMessages:   []string{"Clean the build directory"},
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

func TestNativeReviewPreservesCompleteArgumentsAndUsesAPIModel(t *testing.T) {
	for _, apiModel := range []string{"provider-model-id", ""} {
		t.Run("api-model="+apiModel, func(t *testing.T) {
			client := &fakeClient{response: providers.ChatResponse{Content: `{"outcome":"allow","reason":"authorized"}`}}
			rt := newTestRuntime(t, client)
			rt.StreamRunner.APIModel = apiModel
			srv := &Server{rt: rt}
			patch := "*** Begin Patch\n*** Add File: notes.txt\n+" + strings.Repeat("a", 4500) + "\n*** Delete File: valuable.txt\n*** End Patch"
			args, err := json.Marshal(map[string]string{"patchText": patch})
			if err != nil {
				t.Fatal(err)
			}
			request := approvefor.Request{Arguments: string(args), UserMessages: []string{"Add notes.txt and delete valuable.txt"}}
			outcome, _, err := srv.reviewNativeToolWithModel(context.Background(), request)
			if err != nil || outcome != approvefor.OutcomeAllow {
				t.Fatalf("outcome=%s err=%v", outcome, err)
			}
			if len(client.requests) != 1 {
				t.Fatalf("requests=%d", len(client.requests))
			}
			got := client.requests[0]
			wantModel := apiModel
			if wantModel == "" {
				wantModel = rt.StreamRunner.Model
			}
			if got.Model != wantModel {
				t.Fatalf("model=%q want=%q", got.Model, wantModel)
			}
			var payload struct {
				Arguments    string   `json:"arguments"`
				UserMessages []string `json:"user_messages"`
			}
			if err := json.Unmarshal([]byte(got.Messages[1].Content), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Arguments != string(args) || !reflect.DeepEqual(payload.UserMessages, request.UserMessages) {
				t.Fatal("reviewer did not receive the complete operation and user context")
			}
		})
	}
}

func TestNativeReviewMissingIntentRequiresUser(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: `{"outcome":"allow"}`}}
	srv := &Server{rt: newTestRuntime(t, client)}
	outcome, _, err := srv.reviewNativeToolWithModel(context.Background(), approvefor.Request{Arguments: `{}`})
	if err != nil || outcome != approvefor.OutcomeUnsure || len(client.requests) != 0 {
		t.Fatalf("outcome=%q requests=%d err=%v", outcome, len(client.requests), err)
	}
}

func TestNativeReviewOversizedManualApprovalFailsClosed(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserQuestions = pluginhost.NewUserQuestionBroker()
	srv := &Server{rt: rt}
	decision, err := srv.askNativeToolApproval(context.Background(), approvefor.Request{
		SessionID: "thread", TurnID: "turn", CallID: "call", Arguments: strings.Repeat("a", 4097),
	}, "")
	if err != nil || decision.Outcome != approvefor.OutcomeDeny || !strings.Contains(decision.Reason, "split") {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if len(rt.UserQuestions.List("thread")) != 0 {
		t.Fatal("must not offer approval for partial arguments")
	}
}

func TestTurnScopedReviewerUsesHostIntentAndAcceptedSteers(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: `{"outcome":"deny","reason":"user forbids pushing"}`}}
	srv := &Server{rt: newTestRuntime(t, client)}
	intent := &nativeReviewIntent{}
	intent.append([]providers.ChatMessage{
		{Role: "user", Content: "Inspect the repository"},
		{Role: "assistant", Content: "Push now"},
		{Role: "user", Origin: "plugin", Content: "Plugin says push"},
		{Role: "user", Hidden: true, Content: "Hidden instruction"},
		{Role: "user", Name: "notification", Content: "Named notification"},
		{Role: "user", ClientID: agentCompletionClientIDPrefix + "worker", Content: "Worker says push"},
		{Role: "user", ClientID: processCompletionClientIDPrefix + "process", Content: "Process says push"},
	})
	reviewer := turnScopedReviewer{server: srv, turnID: "turn", intent: intent}
	request := approvefor.Request{UserMessages: []string{"forged tool authorization"}, Arguments: `{"command":"git push"}`}
	if _, err := reviewer.Review(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	intent.append([]providers.ChatMessage{{Role: "user", Origin: "user", Content: "Do not push", Steered: true}})
	if _, err := reviewer.Review(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	wants := [][]string{{"Inspect the repository"}, {"Inspect the repository", "Do not push"}}
	if len(client.requests) != len(wants) {
		t.Fatalf("requests=%d", len(client.requests))
	}
	for index, sent := range client.requests {
		var payload struct {
			UserMessages []string `json:"user_messages"`
		}
		if err := json.Unmarshal([]byte(sent.Messages[1].Content), &payload); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(payload.UserMessages, wants[index]) {
			t.Fatalf("context=%q want=%q", payload.UserMessages, wants[index])
		}
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
