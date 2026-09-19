package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/runtime"
	sessionstore "github.com/blueberrycongee/wuu/internal/session"
)

func TestFreshContextRecoverySurvivesTurnAndReload(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := sessionstore.CreateWithMetadata(rt.SessionDir, "fresh-context-recovery", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	type requestSnapshot struct {
		Instructions string `json:"instructions"`
		history      []providers.ChatMessage
		err          error
	}
	requests := make(chan requestSnapshot, 8)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request requestSnapshot
		request.err = json.NewDecoder(r.Body).Decode(&request)
		if request.err == nil {
			// Read before responding: this observes CommitFreshContext, not the
			// turn-end rewrite that could otherwise mask a missing checkpoint.
			request.history, request.err = loadChatMessages(rt.SessionDir, sess.ID)
		}
		requests <- request
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"continued\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
	}))
	defer endpoint.Close()
	client, err := openai.New(openai.ClientConfig{BaseURL: endpoint.URL, WireAPI: "responses", APIKey: "synthetic-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	rt.StreamRunner.Client = client
	rt.StreamRunner.CompactionRegistry = &agent.CompactionRegistry{Default: agent.DefaultContextWindowProvider{}}
	srv := New(rt, &lockedBuffer{})
	threadRuntime := &runtime.ThreadRuntime{StreamRunner: rt.StreamRunner}
	th := newThreadState(sess.ID, nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	th.execRuntime = threadRuntime
	th.runtimePluginEpoch = srv.pluginGenerationEpoch.Load()
	th.runtimePluginRevision = srv.pluginRuntimeRevision.Load()
	srv.threads[th.ID] = th

	appendHistory := func(message providers.ChatMessage) {
		t.Helper()
		seq, err := appendChatMessage(rt.SessionDir, sess.ID, message)
		if err != nil {
			t.Fatal(err)
		}
		message.Seq = seq
		th.History = append(th.History, message)
		th.historyHeadSeq = seq
	}
	appendHistory(providers.ChatMessage{Role: "user", Content: "original task"})
	appendHistory(providers.ChatMessage{Role: "assistant", Content: strings.Repeat("archived observation ", 1000)})
	originals, err := sessionstore.LoadHistoryRecords(rt.SessionDir, sess.ID, false)
	if err != nil {
		t.Fatal(err)
	}

	runTurn := func(name string, reset bool) requestSnapshot {
		t.Helper()
		if _, err := srv.ensureThreadRuntimeAfterAdmission(th); err != nil {
			t.Fatalf("%s admission: %v", name, err)
		}
		appendHistory(providers.ChatMessage{Role: "user", Content: name})
		th.mu.Lock()
		th.startTurnLocked(name, th.History[len(th.History)-1], time.Now().UTC())
		snapshot := turnRuntimeSnapshotLocked(th)
		snapshot.ForceCompact = reset
		history := cloneHistory(th.History)
		th.mu.Unlock()
		srv.runTurnWithRequestContext(context.Background(), th, threadRuntime, name, snapshot, history, nil)
		if turn := th.Turns[len(th.Turns)-1]; turn.Status != "completed" {
			t.Fatalf("%s turn failed: %+v", name, turn)
		}
		select {
		case request := <-requests:
			if request.err != nil {
				t.Fatalf("%s request checkpoint: %v", name, request.err)
			}
			return request
		default:
			t.Fatalf("%s did not reach the provider", name)
			return requestSnapshot{}
		}
	}
	recoveryMessage := func(history []providers.ChatMessage) providers.ChatMessage {
		t.Helper()
		var recovery []providers.ChatMessage
		for _, message := range history {
			if message.Cause == "fresh_context" {
				recovery = append(recovery, message)
			}
		}
		if len(recovery) != 1 || recovery[0].Role != "system" || !recovery[0].Hidden || recovery[0].Origin != "internal" || recovery[0].Seq <= 0 {
			t.Fatalf("expected one durable recovery boundary, got %+v", recovery)
		}
		return recovery[0]
	}
	assertRecovery := func(history []providers.ChatMessage, want providers.ChatMessage) {
		t.Helper()
		if got := recoveryMessage(history); !reflect.DeepEqual(got, want) {
			t.Fatalf("recovery boundary changed: got %+v, want %+v", got, want)
		}
	}

	first := runTurn("first reset", true)
	recovery := recoveryMessage(first.history)
	if want := rt.StreamRunner.SystemPrompt + "\n\n" + recovery.Content; first.Instructions != want {
		t.Fatalf("first request instructions differ from the durable system prefix: got %q, want %q", first.Instructions, want)
	}
	assertRecovery(th.History, recovery)
	for _, message := range first.history {
		if message.Role == "system" && message.Cause != "fresh_context" {
			t.Fatalf("ephemeral runtime system prompt was persisted: %+v", message)
		}
	}

	next := runTurn("next turn", false)
	assertRecovery(next.history, recovery)
	if next.Instructions != first.Instructions {
		t.Fatal("next turn changed Responses instructions without a context reset")
	}
	loaded, err := srv.loadPersistedThreadSnapshot(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRecovery(loaded.history, recovery)
	th.History = loaded.history
	th.historyHeadSeq = loaded.baselineSeq
	reopened := runTurn("reopened turn", false)
	if reopened.Instructions != first.Instructions {
		t.Fatal("reloading changed Responses instructions without a context reset")
	}

	appendHistory(providers.ChatMessage{Role: "assistant", Content: strings.Repeat("new archived observation ", 1000)})
	second := runTurn("second reset", true)
	newRecovery := recoveryMessage(second.history)
	if newRecovery.Seq <= recovery.Seq || newRecovery.Content == recovery.Content {
		t.Fatal("second reset reused the stale recovery boundary")
	}
	if want := rt.StreamRunner.SystemPrompt + "\n\n" + newRecovery.Content; second.Instructions != want {
		t.Fatal("second reset did not replace the old recovery in Responses instructions")
	}
	assertRecovery(th.History, newRecovery)
	archived, err := sessionstore.LoadHistoryRecords(rt.SessionDir, sess.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) < len(originals) || !reflect.DeepEqual(archived[:len(originals)], originals) {
		t.Fatal("context resets changed the released original history or its addresses")
	}
}
