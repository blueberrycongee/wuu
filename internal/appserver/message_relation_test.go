package appserver

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestMessageRelationVisibleOutputMatchesHistoryAndDoesNotRebindEarlierRows(t *testing.T) {
	relation := &providers.MessageRelation{ReplyTo: &providers.MessageRef{SessionID: "main", Seq: 2}, Metadata: map[string]json.RawMessage{"task_links": json.RawMessage(`[{"task_id":"task","requirements_revision":3}]`)}}
	messages := []providers.ChatMessage{{Role: "assistant", Content: "old", Seq: 1}, {Role: "assistant", Content: "answer"}, {Role: "user", Content: "input"}}
	inheritMessageRelation(messages, relation)
	if messages[0].Relation != nil || messages[2].Relation != nil || !reflect.DeepEqual(messages[1].Relation, relation) {
		t.Fatalf("wrong inheritance: %+v", messages)
	}
	records := historyRecordsFromChatMessages(messages)
	persisted, err := persistedMessageFromHistoryRecord(records[1])
	if err != nil {
		t.Fatal(err)
	}
	restored := chatMessageFromPersistedMessage(persisted)
	item := chatMessageItem("item", restored)
	if !reflect.DeepEqual(item.Relation, relation) {
		t.Fatalf("history lost relation: %+v", item)
	}
	th := newThreadState("main", nil, "", "", "", false, time.Now())
	th.startTurnLocked("turn", providers.ChatMessage{Role: "user", Content: "wake"}, time.Now())
	batch := th.applyStreamEventWithRelationLocked("turn", providers.StreamEvent{Type: providers.EventContentDelta, Content: "answer"}, time.Now(), relation)
	found := false
	for _, notification := range batch {
		if started, ok := notification.params.(ItemStartedNotification); ok && started.Item.Type == ThreadItemAgentMessage {
			found = true
			if !reflect.DeepEqual(started.Item.Relation, relation) {
				t.Fatal("live relation missing")
			}
		}
	}
	if !found {
		t.Fatal("no visible output")
	}
}

type relationPreStepClient struct {
	pluginTurnLifecycleClient
	inputs chan pluginhost.AgentPreStepInput
}

func (c *relationPreStepClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityAgentPreStep, Kind: pluginhost.SeamTransform, Version: 1}}
}
func (c *relationPreStepClient) InvokeCapability(_ context.Context, p pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginhost.AgentPreStepInput
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	c.inputs <- input
	return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
}

func TestPluginRelationAdmissionDeliveryAndReload(t *testing.T) {
	client := &fakeClient{response: providersResponse("visible answer")}
	rt := newTestRuntime(t, client)
	owner := &relationPreStepClient{pluginTurnLifecycleClient: pluginTurnLifecycleClient{id: "fixture"}, inputs: make(chan pluginhost.AgentPreStepInput, 8)}
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: owner.id}}}
	attachTestModelGeneration(t, rt, owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionCreateParams{RequestID: "main", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	// Seed a real visible quote target and a hidden target that must be rejected.
	for _, msg := range []session.HistoryRecord{{Role: "assistant", Content: "quote"}, {Role: "user", Content: "hidden", Hidden: true}} {
		if err := rt.Sessions.AppendHistoryRecord(created.SessionID, msg); err != nil {
			t.Fatal(err)
		}
	}
	relation := &providers.MessageRelation{ReplyTo: &providers.MessageRef{SessionID: created.SessionID, Seq: 1}, Metadata: map[string]json.RawMessage{"task_links": json.RawMessage(`[{"task_id":"task","requirements_revision":2}]`)}}
	for _, ref := range []providers.MessageRef{{SessionID: "other", Seq: 1}, {SessionID: created.SessionID, Seq: 2}, {SessionID: created.SessionID, Seq: 999}} {
		bad := providers.CloneMessageRelation(relation)
		bad.ReplyTo = &ref
		if err := srv.validateMessageRelation(testRuntimeState(t, srv.rt), owner.id, created.SessionID, bad); err == nil {
			t.Fatalf("accepted invalid target %+v", ref)
		}
	}
	// A visible file produced by a tool is quotable, but raw tool logs are not.
	if err := rt.Sessions.AppendHistoryRecord(created.SessionID, session.HistoryRecord{Role: "tool", Content: "raw tool log"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.AppendHistoryRecord(created.SessionID, session.HistoryRecord{Role: "tool", Content: "file", ToolResult: json.RawMessage(`{"content":[{"type":"image","mime_type":"image/png","data":"aGk="}]}`)}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		seq     int
		allowed bool
	}{{3, false}, {4, true}} {
		ref := providers.CloneMessageRelation(relation)
		ref.ReplyTo.Seq = tc.seq
		err := srv.validateMessageRelation(testRuntimeState(t, srv.rt), owner.id, created.SessionID, ref)
		if (err == nil) != tc.allowed {
			t.Fatalf("tool quote %d: %v", tc.seq, err)
		}
	}
	proof := runtime.ClientUserInput{RequestID: "client", Prompt: " literal quote reply ", Relation: relation}
	ctx := runtime.WithClientUserInput(context.Background(), owner.id, proof)
	params := pluginhost.SessionSendParams{RequestID: "send", SessionID: created.SessionID, UserInputRequestID: proof.RequestID, Input: pluginhost.SessionInput{Prompt: proof.Prompt}}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), owner.id, params); err == nil {
		t.Fatal("omitted admitted relation accepted")
	}
	params.Relation = relation
	result, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), owner.id, params)
	if err != nil {
		t.Fatal(err)
	}
	waitForTurnCompletedForThread(t, out, result.SessionID)
	select {
	case input := <-owner.inputs:
		if input.SourceMessage == nil || input.SourceMessage.SessionID != created.SessionID || input.SourceMessage.Seq != 5 || !reflect.DeepEqual(input.MessageRelation, relation) {
			t.Fatalf("untrusted turn source: %+v", input)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing pre-step")
	}
	records, err := rt.Sessions.LoadHistoryRecords(created.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record.Role == "user" && record.Seq == 5 && (record.Content != proof.Prompt || !reflect.DeepEqual(record.Relation, relation)) {
			t.Fatalf("altered authenticated input: %+v", record)
		}
		if record.Role == "assistant" && record.Content == "visible answer" {
			found = true
			if !reflect.DeepEqual(record.Relation, relation) {
				t.Fatalf("assistant relation lost: %+v", record)
			}
		}
	}
	if !found {
		t.Fatal("missing output")
	}
	loaded, err := loadChatMessages(rt.Sessions, created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range loaded {
		if msg.Role == "assistant" && msg.Content == "visible answer" && !reflect.DeepEqual(chatMessageItem("restored", msg).Relation, relation) {
			t.Fatal("reload projection lost relation")
		}
	}
}

func TestPluginRelationQueuedTurnsKeepTheirOwnSource(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	client := &fakeClient{response: providersResponse("answer"), onChat: func(call int, _ providers.ChatRequest) {
		if call == 1 {
			close(started)
			<-release
		}
	}}
	rt := newTestRuntime(t, client)
	owner := &relationPreStepClient{pluginTurnLifecycleClient: pluginTurnLifecycleClient{id: "fixture"}, inputs: make(chan pluginhost.AgentPreStepInput, 8)}
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: owner.id}}}
	attachTestModelGeneration(t, rt, owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() { once.Do(func() { close(release) }); srv.Close() })
	created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionCreateParams{RequestID: "main", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	relations := []*providers.MessageRelation{{Metadata: map[string]json.RawMessage{"task": json.RawMessage(`"A"`)}}, {Metadata: map[string]json.RawMessage{"task": json.RawMessage(`"B"`)}}}
	for i, id := range []string{"A", "B"} {
		proof := runtime.ClientUserInput{RequestID: id, Prompt: id, Relation: relations[i]}
		ctx := runtime.WithClientUserInput(context.Background(), owner.id, proof)
		result, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), owner.id, pluginhost.SessionSendParams{RequestID: id, UserInputRequestID: id, SessionID: created.SessionID, Relation: relations[i], Input: pluginhost.SessionInput{Prompt: id}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("first turn missing")
			}
		} else if result.State != pluginhost.TurnLifecycleQueued {
			t.Fatalf("second input did not queue: %+v", result)
		}
	}
	once.Do(func() { close(release) })
	var inputs []pluginhost.AgentPreStepInput
	for len(inputs) < 2 {
		select {
		case input := <-owner.inputs:
			inputs = append(inputs, input)
		case <-time.After(3 * time.Second):
			t.Fatal("queued turn never started")
		}
	}
	// Each source is resolved by the admitted input, never the newest queued user.
	for i, input := range inputs {
		if input.SourceMessage == nil || input.SourceMessage.SessionID != created.SessionID || !reflect.DeepEqual(input.MessageRelation, relations[i]) {
			t.Fatalf("turn %d has wrong context: %+v", i, input)
		}
		records, err := rt.Sessions.LoadHistoryRecords(created.SessionID, false)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range records {
			if record.Seq == input.SourceMessage.Seq {
				found = true
				if record.Content != []string{"A", "B"}[i] {
					t.Fatalf("turn source swapped: %+v", record)
				}
			}
		}
		if !found {
			t.Fatal("source is not durable")
		}
	}
	if inputs[0].SourceMessage.Seq == inputs[1].SourceMessage.Seq {
		t.Fatal("queued inputs reused source")
	}
}
