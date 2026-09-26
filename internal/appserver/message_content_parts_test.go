package appserver

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestUserMessageFromPromptPreservesValidContentParts(t *testing.T) {
	prompt := "pasted body\nregular text"
	parts := []providers.MessageContentPart{
		{Type: "pasted_text", Text: "pasted body\n"},
		{Type: "text", Text: "regular text"},
	}

	msg, err := userMessageFromPrompt(prompt, nil, nil, parts)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != prompt {
		t.Fatalf("Content = %q, want %q", msg.Content, prompt)
	}
	if len(msg.ContentParts) != 2 || msg.ContentParts[0].Type != "pasted_text" {
		t.Fatalf("ContentParts = %#v, want structured pasted and text parts", msg.ContentParts)
	}
}

func TestUserMessageFromPromptRejectsMismatchedContentParts(t *testing.T) {
	msg, err := userMessageFromPrompt(
		"canonical prompt",
		nil,
		nil,
		[]providers.MessageContentPart{{Type: "pasted_text", Text: "different text"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ContentParts) != 0 {
		t.Fatalf("ContentParts = %#v, want no presentation metadata for mismatched content", msg.ContentParts)
	}
}

func TestServerContentPartsSurviveResumeAndFork(t *testing.T) {
	pasted := "  " + strings.Repeat("source line\n", 80)
	parts := []providers.MessageContentPart{
		{Type: "pasted_text", Text: pasted, Title: "Source notes"},
		{Type: "text", Text: "Follow-up question.  "},
	}
	for _, tc := range []struct {
		name        string
		prompt      string
		input, want []providers.MessageContentPart
	}{
		{name: "pasted_and_text", prompt: pasted + "Follow-up question.  ", input: parts, want: parts},
		{name: "plain_text", prompt: "Ordinary question."},
		{name: "mismatched_parts", prompt: "Canonical question.", input: parts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeClient{response: providersResponse("answer")}
			rt := newTestRuntime(t, client)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			rpc := func(id, method string, params any) any {
				t.Helper()
				raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
				if err != nil {
					t.Fatal(err)
				}
				if err := srv.handleLine(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
				response := responseByID(t, parseOutput(t, out.String()), id)
				if response["error"] != nil {
					t.Fatalf("%s: %+v", method, response["error"])
				}
				return response["result"]
			}
			prompt := strings.TrimSpace(tc.prompt)
			checkItem := func(stage string, turns []Turn) {
				t.Helper()
				for _, turn := range turns {
					for _, item := range turn.Items {
						if item.Type != ThreadItemUserMessage {
							continue
						}
						if item.Text != prompt {
							t.Errorf("%s text = %q, want %q", stage, item.Text, prompt)
						}
						if !slices.Equal(item.ContentParts, tc.want) {
							t.Errorf("%s content parts = %#v, want %#v", stage, item.ContentParts, tc.want)
						}
						t.Logf("%s: body intact=%v, parts=%d", stage, item.Text == prompt, len(item.ContentParts))
						return
					}
				}
				t.Fatalf("%s: no user message", stage)
			}
			checkStored := func(stage, threadID string) {
				t.Helper()
				records, err := session.LoadHistoryRecords(rt.SessionDir, threadID, false)
				if err != nil {
					t.Fatal(err)
				}
				for _, record := range records {
					if record.Role != "user" {
						continue
					}
					var stored []providers.MessageContentPart
					if len(record.ContentParts) > 0 {
						if err := json.Unmarshal(record.ContentParts, &stored); err != nil {
							t.Fatal(err)
						}
					}
					if record.Content != prompt {
						t.Errorf("%s stored text = %q, want %q", stage, record.Content, prompt)
					}
					if !slices.Equal(stored, tc.want) {
						t.Errorf("%s stored content parts = %#v, want %#v", stage, stored, tc.want)
					}
					t.Logf("%s: body intact=%v, parts=%d", stage, record.Content == prompt, len(stored))
					return
				}
				t.Fatalf("%s: no stored user message", stage)
			}
			started := remarshal[ThreadStartResult](t, rpc("start", MethodThreadStart, nil))
			threadID := started.Thread.ID
			rpc("turn", MethodTurnStart, TurnStartParams{ThreadID: threadID, Prompt: tc.prompt, ContentParts: tc.input})
			messages := waitForTurnCompletedForThread(t, out, threadID)
			completed := notificationsByMethod(messages, NotificationTurnCompleted)
			turn := remarshal[TurnCompletedNotification](t, completed[len(completed)-1]["params"]).Turn
			if turn.Status != TurnStatusCompleted {
				t.Fatalf("turn status = %s", turn.Status)
			}
			checkItem("live", []Turn{turn})
			client.mu.Lock()
			requests := append([]providers.ChatRequest(nil), client.requests...)
			client.mu.Unlock()
			found := false
			for _, request := range requests {
				for _, msg := range request.Messages {
					if msg.Role == "user" && msg.Content == prompt {
						found = true
					}
				}
			}
			if !found {
				t.Fatal("provider did not receive the complete canonical prompt")
			}
			t.Log("provider: complete canonical prompt received")
			checkStored("SQLite", threadID)
			srv.Close()
			out = &lockedBuffer{}
			srv = New(rt, out)
			t.Cleanup(srv.Close)
			resumed := remarshal[ThreadResumeResult](t, rpc("resume", MethodThreadResume, ThreadResumeParams{SessionID: threadID}))
			checkItem("resume", resumed.Thread.Turns)
			restoredTurn := resumed.Thread.Turns[0]
			lastItem := restoredTurn.Items[len(restoredTurn.Items)-1]
			fork := remarshal[ThreadForkResult](t, rpc("fork", MethodThreadFork, ThreadForkParams{
				ThreadID: threadID, TurnID: restoredTurn.ID, ItemID: lastItem.ID,
			}))
			checkItem("fork", fork.Thread.Turns)
			checkStored("fork SQLite", fork.Thread.ID)
			srv.Close()
			out = &lockedBuffer{}
			srv = New(rt, out)
			t.Cleanup(srv.Close)
			forkResume := remarshal[ThreadResumeResult](t, rpc("fork-resume", MethodThreadResume, ThreadResumeParams{SessionID: fork.Thread.ID}))
			checkItem("fork resume", forkResume.Thread.Turns)
		})
	}
}
