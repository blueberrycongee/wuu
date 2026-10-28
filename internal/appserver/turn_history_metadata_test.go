package appserver

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A shell replacement can load a fresh projection of the same durable thread.
// Its public turns must retain the metadata returned before replacement.
func TestThreadResumePreservesCompletedTurnMetadata(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providers.ChatResponse{Content: "durable answer", FinishReason: providers.FinishReasonStop}})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"create","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "create")["result"]).Thread.ID
	if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"start","method":"turn/start","params":{"thread_id":%q,"prompt":"remember this answer"}}`, threadID))); err != nil {
		t.Fatal(err)
	}
	waitForTurnCompletedCountForThread(t, out, threadID, 1)
	read := fmt.Sprintf(`{"id":"read","method":"thread/resume","params":{"session_id":%q,"response_only":true}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(read)); err != nil {
		t.Fatal(err)
	}
	before := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "read")["result"]).Thread.Turns
	if len(before) != 1 || before[0].StartedAt == nil || before[0].AnswerReadyAt == nil || before[0].Model == "" || before[0].ModelProvider == "" {
		t.Fatalf("live turn lacks metadata: %+v", before)
	}
	reloadOut := &lockedBuffer{}
	reloaded := New(rt, reloadOut)
	t.Cleanup(reloaded.Close)
	resume := fmt.Sprintf(`{"id":"resume","method":"thread/resume","params":{"session_id":%q,"response_only":true}}`, threadID)
	if err := reloaded.handleLine(context.Background(), []byte(resume)); err != nil {
		t.Fatal(err)
	}
	after := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, reloadOut.String()), "resume")["result"]).Thread.Turns
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("public turns changed after durable reload:\nbefore: %+v\nafter:  %+v", before, after)
	}
}

func TestTerminalHistoryPreservesCapturedTurnBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status TurnStatus
		answer bool
	}{
		{"completed", TurnStatusCompleted, true},
		{"completed_without_answer_event", TurnStatusCompleted, false},
		{"failed", TurnStatusFailed, false},
		{"interrupted_after_answer", TurnStatusInterrupted, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			binding := testRuntimeState(t, rt)
			sess, err := binding.Sessions.CreateWithMetadata("terminal-metadata", rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
			completed := started.Add(2 * time.Second)
			if err := binding.Sessions.AppendHistoryRecord(sess.ID, session.HistoryRecord{Role: "user", Content: "question", At: started.Add(20 * time.Millisecond)}); err != nil {
				t.Fatal(err)
			}
			th := newThreadState(sess.ID, nil, "captured-provider", "captured-model", rt.RootDir, true, started)
			th.state = binding
			turnID := sess.ID + "-turn-0001"
			th.startTurnLocked(turnID, providers.ChatMessage{Role: "user", Content: "question"}, started)
			if scenario.answer {
				th.applyMessageItemLocked(turnID, providers.ChatMessage{Role: "assistant", Content: "answer", Phase: providers.MessagePhaseFinalAnswer}, started.Add(time.Second))
			}
			// The next selection must not replace the already captured turn model.
			th.ModelProvider, th.Model = "next-provider", "next-model"
			if err := srv.persistTurnTerminal(th, turnID, TurnKindUser, scenario.status, nil, completed, nil, "captured-provider", "captured-model", providers.TokenUsage{}); err != nil {
				t.Fatal(err)
			}
			live := th.finishTurnLocked(turnID, scenario.status, nil, completed, "", "", false)
			loaded, err := srv.loadPersistedThreadState(binding, sess.ID, completed.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded.Turns) != 1 {
				t.Fatalf("restored turns = %+v", loaded.Turns)
			}
			restored := loaded.Turns[0]
			if restored.ModelProvider != live.ModelProvider || restored.Model != live.Model || !reflect.DeepEqual(restored.StartedAt, live.StartedAt) || !reflect.DeepEqual(restored.AnswerReadyAt, live.AnswerReadyAt) || !reflect.DeepEqual(restored.CompletedAt, live.CompletedAt) || !reflect.DeepEqual(restored.DurationMS, live.DurationMS) {
				t.Fatalf("captured turn boundaries changed:\nlive: %+v\nrestored: %+v", live, restored)
			}
		})
	}
}

func TestLegacyTerminalHistoryPreservesKnownModelWithoutInventingTimes(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	completed := started.Add(time.Second)
	for _, knownModel := range []bool{false, true} {
		t.Run(fmt.Sprint(knownModel), func(t *testing.T) {
			terminal := persistedMessage{Role: "meta", Content: turnTerminalHistoryRecord, StopReason: string(TurnStatusCompleted), At: completed}
			if knownModel {
				terminal.Provider, terminal.Model = "old-provider", "old-model"
			}
			turns := turnsFromPersistedHistory("legacy", []persistedMessage{{Role: "user", Content: "old question", At: started}, terminal}, completed.Add(time.Hour), nil)
			if len(turns) != 1 {
				t.Fatalf("turns = %+v", turns)
			}
			turn := turns[0]
			if turn.ModelProvider != terminal.Provider || turn.Model != terminal.Model || turn.StartedAt == nil || !turn.StartedAt.Equal(started) || turn.AnswerReadyAt != nil || turn.CompletedAt == nil || !turn.CompletedAt.Equal(completed) || turn.DurationMS == nil || *turn.DurationMS != 1000 {
				t.Fatalf("legacy metadata changed: %+v", turn)
			}
		})
	}
}
