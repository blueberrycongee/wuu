package appserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestResponsesFailureDiagnosticsSurviveThreadResume(t *testing.T) {
	for _, code := range []string{"server_error", "request_timeout", "custom_failure", "insufficient_quota", "response_too_large"} {
		t.Run(code, func(t *testing.T) {
			var requests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if code == "response_too_large" {
					fmt.Fprint(w, "data: ", strings.Repeat("x", providers.MaxStreamEventBytes+1), "\n\n")
					return
				}
				fmt.Fprintf(w, "data: {\"type\":\"error\",\"code\":%q,\"message\":\"Fixture failure\"}\n\n", code)
			}))
			defer upstream.Close()
			client, err := openai.New(openai.ClientConfig{BaseURL: upstream.URL, APIKey: "test", WireAPI: "responses", ResponsesTransport: providers.StreamTransportSSE})
			if err != nil {
				t.Fatal(err)
			}
			op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
			op.AttemptLimit = 2
			reliable := providers.NewReliableStreamClient(client, nil, providers.WithStreamRetryWait(func(context.Context, time.Duration) error { return nil }))
			ch, err := reliable.StreamChat(context.Background(), providers.ChatRequest{Operation: op, Model: "test", Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}}})
			if err != nil {
				t.Fatal(err)
			}
			var terminal error
			for event := range ch {
				if event.Type == providers.EventError {
					terminal = event.Error
				}
			}
			if terminal == nil {
				t.Fatal("expected terminal failure")
			}
			terminal = fmt.Errorf("stream request failed: %w", terminal)
			live := BuildTurnError(terminal, "compatible")
			wantCategory := "provider"
			if code == "response_too_large" {
				wantCategory = "local"
				if live.Recovery == nil || live.Recovery.FailureCategory != providers.FailureResponseTooLarge {
					t.Fatalf("lost local receive limit classification: %+v", live)
				}
			}
			if live.Recovery == nil || live.Recovery.AttemptCount != int(requests.Load()) || live.Recovery.SubmissionCount != int(requests.Load()) || live.Category != wantCategory || live.Code != code {
				t.Fatalf("live error = %+v", live)
			}
			wantStop, wantRetries := "non_retryable", 0
			if code == "server_error" || code == "request_timeout" {
				wantStop, wantRetries = "retry_limit", 1
			}
			if live.Recovery.StopReason != wantStop || live.Recovery.RetryCount != wantRetries {
				t.Fatalf("recovery = %+v", live.Recovery)
			}

			rt := newTestRuntime(t, &fakeClient{})
			sess, err := session.CreateWithMetadata(rt.SessionDir, session.NewID(), rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := appendChatMessage(rt.SessionDir, sess.ID, providers.ChatMessage{Role: "user", Content: "hello"}); err != nil {
				t.Fatal(err)
			}
			srv := New(rt, &lockedBuffer{})
			// The thread's selection may have changed while the failed turn ran.
			// Persist the immutable turn diagnostic, not the new selection.
			th := newThreadState(sess.ID, nil, "another-provider", rt.Model, rt.RootDir, true, time.Now().UTC())
			if err := srv.persistTurnTerminal(th, sess.ID+"-turn-0001", TurnKindUser, TurnStatusFailed, &live, time.Now().UTC(), nil, rt.ProviderName, rt.Model); err != nil {
				t.Fatal(err)
			}
			activity, err := session.LatestSubscriptionActivity(rt.SessionDir, []session.SubscriptionActivityKey{{Provider: rt.ProviderName}})
			if err != nil {
				t.Fatal(err)
			}
			if got := activity[session.SubscriptionActivityKey{Provider: rt.ProviderName}]; got.Status != "failed" || got.Model != rt.Model || got.UsageReported {
				t.Fatalf("recorded request = %+v", got)
			}
			out := &lockedBuffer{}
			reloaded := New(rt, out)
			if err := reloaded.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"resume","method":"thread/resume","params":{"session_id":%q}}`, sess.ID))); err != nil {
				t.Fatal(err)
			}
			result := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "resume")["result"])
			if len(result.Thread.Turns) != 1 || !reflect.DeepEqual(result.Thread.Turns[0].Error, &live) {
				t.Fatalf("resumed error differs from live diagnostic: %+v", result.Thread.Turns)
			}
		})
	}
}

func TestBuildTurnErrorDoesNotInferNetworkFromRequestWrapper(t *testing.T) {
	for _, err := range []error{
		errors.New("stream request failed: response stream error"),
		fmt.Errorf("stream request failed: %w", providers.NewProviderStreamError("custom_failure", "Diagnostic detail")),
	} {
		if got := BuildTurnError(err, "compatible"); got.Category == "network" {
			t.Fatalf("misleading classification: %+v", got)
		}
	}
}
