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

// A second connection can cache a legitimate target before another connection
// edits it. Durable branch state must win over every cached-history fallback.
func TestEditedBranchRejectsForkFromStaleConnection(t *testing.T) {
	for _, wholeBranch := range []bool{false, true} {
		t.Run(fmt.Sprintf("whole_branch_%t", wholeBranch), func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			writerOut, readerOut := &lockedBuffer{}, &lockedBuffer{}
			writer, reader := New(rt, writerOut), New(rt, readerOut)
			t.Cleanup(writer.Close)
			t.Cleanup(reader.Close)
			sess, err := session.CreateWithMetadata(rt.SessionDir, "stale-fork", rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			initial := []providers.ChatMessage{{Role: "user", Content: "kept prompt"}, {Role: "assistant", Content: "kept answer", Phase: providers.MessagePhaseFinalAnswer}, {Role: "user", Content: "obsolete prompt"}, {Role: "assistant", Content: "obsolete answer", Phase: providers.MessagePhaseFinalAnswer}}
			if err := rewriteChatHistory(rt.SessionDir, sess.ID, initial); err != nil {
				t.Fatal(err)
			}
			rpc := func(server *Server, out *lockedBuffer, id, method string, params any) map[string]any {
				t.Helper()
				payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
				if err != nil {
					t.Fatal(err)
				}
				if err := server.handleLine(context.Background(), payload); err != nil {
					t.Fatal(err)
				}
				return responseByID(t, parseOutput(t, out.String()), id)
			}
			response := rpc(reader, readerOut, "cached", MethodThreadResume, map[string]any{"session_id": sess.ID, "response_only": true})
			if response["error"] != nil {
				t.Fatal(response["error"])
			}
			cached := remarshal[ThreadResumeResult](t, response["result"]).Thread
			oldTurn, oldAnswer := finalAnswerItemForForkTest(t, cached.Turns, "obsolete answer")
			params := ThreadForkParams{ThreadID: sess.ID, TurnID: oldTurn.ID, ItemID: oldAnswer.ID, Target: &ThreadForkTarget{Seq: oldAnswer.Seq, Type: oldAnswer.Type, SourceID: oldAnswer.SourceID}, Mode: "local"}
			// The same cached target is valid before the edit.
			control := rpc(reader, readerOut, "control", MethodThreadFork, params)
			if control["error"] != nil {
				t.Fatalf("unedited fork: %v", control["error"])
			}
			editTurn := cached.Turns[1]
			if wholeBranch {
				editTurn = cached.Turns[0]
			}
			edited := rpc(writer, writerOut, "edit", MethodThreadEditMessage, ThreadEditMessageParams{ThreadID: sess.ID, TurnID: editTurn.ID, ItemID: editTurn.Items[0].ID})
			if edited["error"] != nil {
				t.Fatal(edited["error"])
			}
			stale := rpc(reader, readerOut, "stale", MethodThreadFork, params)
			if stale["error"] == nil {
				t.Errorf("retracted target fork succeeded: %+v", stale["result"])
			}
			// Whole-thread forks have no target to validate and must also ignore cache.
			fork := rpc(reader, readerOut, "current", MethodThreadFork, ThreadForkParams{ThreadID: sess.ID, Mode: "local"})
			if fork["error"] != nil {
				t.Fatal(fork["error"])
			}
			forked := remarshal[ThreadForkResult](t, fork["result"]).Thread
			history, err := loadChatMessages(rt.SessionDir, forked.ID)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, message := range history {
				if message.Role == "user" || message.Role == "assistant" {
					got = append(got, message.Content)
				}
			}
			var want []string
			if !wholeBranch {
				want = []string{"kept prompt", "kept answer"}
			}
			if !slices.Equal(got, want) {
				t.Errorf("whole fork history = %v, want %v", got, want)
			}
		})
	}
}
