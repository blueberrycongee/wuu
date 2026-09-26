package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Exercise the edit/reload/fork contract through RPC and real SQLite storage.
// Compaction must release provider context without retracting the older branch.
func TestEditedBranchSurvivesReloadAndFork(t *testing.T) {
	for _, scenario := range []struct {
		name               string
		editFirst, compact bool
	}{
		{name: "first", editFirst: true}, {name: "second"}, {name: "after_compaction", compact: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client := &fakeClient{response: providersResponse("replacement answer")}
			rt := newTestRuntime(t, client)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			sess, err := session.CreateWithMetadata(rt.SessionDir, "edited-branch", rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			original := []providers.ChatMessage{
				{Role: "user", Content: "kept prompt"}, {Role: "assistant", Content: "kept answer", Phase: providers.MessagePhaseFinalAnswer},
				{Role: "user", Content: "obsolete prompt"}, {Role: "assistant", Content: "obsolete answer", Phase: providers.MessagePhaseFinalAnswer},
			}
			if err := rewriteChatHistory(rt.SessionDir, sess.ID, original); err != nil {
				t.Fatal(err)
			}
			archived, err := session.LoadHistoryRecords(rt.SessionDir, sess.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.compact {
				history, err := loadChatMessages(rt.SessionDir, sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := rewriteChatHistoryAtBaseline(rt.SessionDir, sess.ID, history[2:], 4); err != nil {
					t.Fatal(err)
				}
			}
			rpc := func(id, method string, params any) map[string]any {
				t.Helper()
				payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
				if err != nil {
					t.Fatal(err)
				}
				if err := srv.handleLine(context.Background(), payload); err != nil {
					t.Fatal(err)
				}
				response := responseByID(t, parseOutput(t, out.String()), id)
				if response["error"] != nil {
					t.Fatalf("%s: %+v", method, response["error"])
				}
				return response
			}
			resume := func(id string) Thread {
				return remarshal[ThreadResumeResult](t, rpc(id, MethodThreadResume, map[string]any{"session_id": sess.ID, "response_only": true})["result"]).Thread
			}
			assertTexts := func(label string, thread Thread, want []string) {
				t.Helper()
				var got []string
				for _, turn := range thread.Turns {
					for _, item := range turn.Items {
						if item.Type == ThreadItemUserMessage || item.Type == ThreadItemAgentMessage {
							got = append(got, item.Text)
						}
					}
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s history = %v, want %v", label, got, want)
				}
			}
			cold := func() { srv.mu.Lock(); delete(srv.threads, sess.ID); srv.mu.Unlock() }
			current := resume("initial")
			targetTurn := 1
			if scenario.editFirst {
				targetTurn = 0
			}
			var kept []string
			if !scenario.editFirst {
				kept = []string{"kept prompt", "kept answer"}
			}
			for cycle := 0; cycle < 2; cycle++ {
				prefix := fmt.Sprintf("cycle-%d-", cycle)
				turn := current.Turns[targetTurn]
				rpc(prefix+"edit", MethodThreadEditMessage, ThreadEditMessageParams{ThreadID: sess.ID, TurnID: turn.ID, ItemID: turn.Items[0].ID})
				cold()
				assertTexts("immediate reload", resume(prefix+"empty"), kept)
				rpc(prefix+"replacement", MethodTurnStart, TurnStartParams{ThreadID: sess.ID, Prompt: "replacement prompt"})
				waitForTurnCompletedCountForThread(t, out, sess.ID, cycle+1)
				cold()
				current = resume(prefix + "reload")
				want := append(slices.Clone(kept), "replacement prompt", "replacement answer")
				assertTexts("replacement reload", current, want)
				targetTurn = 0
				for i, turn := range current.Turns {
					for _, item := range turn.Items {
						if item.Type == ThreadItemUserMessage && item.Text == "replacement prompt" {
							targetTurn = i
						}
					}
				}
				answerTurn, answer := finalAnswerItemForForkTest(t, current.Turns, "replacement answer")
				fork := remarshal[ThreadForkResult](t, rpc(prefix+"fork", MethodThreadFork, ThreadForkParams{ThreadID: sess.ID, TurnID: answerTurn.ID, ItemID: answer.ID, Target: &ThreadForkTarget{Seq: answer.Seq, Type: answer.Type, SourceID: answer.SourceID}, Mode: "local"})["result"]).Thread
				assertTexts("fork", fork, want)
				rpc(prefix+"followup", MethodTurnStart, TurnStartParams{ThreadID: fork.ID, Prompt: "continue"})
				waitForTurnCompletedCountForThread(t, out, fork.ID, 1)
				client.mu.Lock()
				request := client.requests[len(client.requests)-1]
				client.mu.Unlock()
				var got []string
				for _, message := range request.Messages {
					if message.Role == "user" || message.Role == "assistant" {
						got = append(got, message.Content)
					}
				}
				if expected := append(slices.Clone(want), "continue"); !slices.Equal(got, expected) {
					t.Errorf("fork provider history = %v, want %v", got, expected)
				}
				if scenario.compact {
					history, head, err := loadProviderPersistedMessages(rt.SessionDir, sess.ID, false)
					if err != nil {
						t.Fatal(err)
					}
					active := chatMessagesFromPersistedMessages(history)
					if err := rewriteChatHistoryAtBaseline(rt.SessionDir, sess.ID, active[len(active)-2:], head); err != nil {
						t.Fatal(err)
					}
					cold()
					current = resume(prefix + "compacted")
					assertTexts("compacted reload", current, want)
				}
			}
			raw, err := session.LoadHistoryRecords(rt.SessionDir, sess.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) < len(archived) || !reflect.DeepEqual(raw[:len(archived)], archived) {
				t.Fatal("editing changed physical audit records")
			}
		})
	}
}
