package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/personalhost"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type attentionCall struct {
	input  pluginhost.SessionAttentionInput
	result chan bool
}
type attentionPolicyClient struct {
	pluginClientRequestTestRuntime
	calls chan attentionCall
}

func (c *attentionPolicyClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilitySessionAttention, Kind: "decision", Version: 1}}
}
func (c *attentionPolicyClient) InvokeCapability(ctx context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginhost.SessionAttentionInput
	if err := json.Unmarshal(params.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	call := attentionCall{input: input, result: make(chan bool, 1)}
	select {
	case c.calls <- call:
	case <-ctx.Done():
		return pluginhost.CapabilityInvokeResult{}, ctx.Err()
	}
	select {
	case allowed := <-call.result:
		raw, _ := json.Marshal(pluginhost.SessionAttentionOutput{Allowed: allowed})
		return pluginhost.CapabilityInvokeResult{Output: raw}, nil
	case <-ctx.Done():
		return pluginhost.CapabilityInvokeResult{}, ctx.Err()
	}
}

func TestUserHostAttentionIsOwnerPolicyBoundAndDoesNotBlockClients(t *testing.T) {
	policy := &attentionPolicyClient{pluginClientRequestTestRuntime: pluginClientRequestTestRuntime{id: "fixture"}, calls: make(chan attentionCall, 4)}
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("visible reply")})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	attachTestModelGeneration(t, rt, policy)
	host, err := NewUserHost(rt, personalhost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	in, writer := io.Pipe()
	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- host.Serve(principal.WithContext(context.Background(), principal.LocalOwner()), in, out, nil)
	}()
	connectionClosed := false
	defer func() {
		if !connectionClosed {
			writer.Close()
			<-done
		}
	}()
	fmt.Fprintln(writer, `{"id":"init","method":"initialize"}`)
	waitForResponseByID(t, out, "init")
	main, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), "fixture", pluginhost.SessionCreateParams{RequestID: "attention-main", Visibility: "plugin", ContextSource: "fresh", AgentID: "agent-main"})
	if err != nil {
		t.Fatal(err)
	}
	send := func(id string) pluginhost.SessionSendResult {
		t.Helper()
		sent, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), "fixture", pluginhost.SessionSendParams{RequestID: id, SessionID: main.SessionID, Input: pluginhost.SessionInput{Prompt: "finish"}})
		if err != nil {
			t.Fatal(err)
		}
		return sent
	}
	first := send("quiet-completion")
	var quiet attentionCall
	select {
	case quiet = <-policy.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("host did not consult owner notification policy")
	}
	if quiet.input.SessionID != main.SessionID || quiet.input.AgentID != "agent-main" || quiet.input.TurnID != first.TurnID || quiet.input.Outcome != "completed" || quiet.input.MessageSeq < 1 || quiet.input.UserSpaceID != "local" || quiet.input.ProductDomain != "personal" {
		t.Fatalf("untrusted policy input: %+v", quiet.input)
	}
	fmt.Fprintln(writer, `{"id":"responsive","method":"initialize"}`)
	waitForResponseByID(t, out, "responsive")
	// A preference changed while this reply was completing. Deny now; the
	// persisted reply remains readable and does not falsely become a push.
	quiet.result <- false
	second := send("allowed-completion")
	var allowed attentionCall
	select {
	case allowed = <-policy.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("next policy was not admitted")
	}
	allowed.result <- true
	// Delivery and every replay recheck the owning preference. Continue serving
	// that selected policy after the explicitly controlled admission above.
	rechecked := make(chan struct{})
	go func() {
		defer close(rechecked)
		for range 2 {
			call := <-policy.calls
			call.result <- true
		}
	}()
	messages := waitForMethod(t, out, NotificationUserAttention)
	count := 0
	for _, message := range messages {
		if message["method"] != NotificationUserAttention {
			continue
		}
		count++
		raw, _ := json.Marshal(message["params"])
		var params UserAttentionNotification
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatal(err)
		}
		if params.TurnID != second.TurnID || params.UserLocation.AgentID != "agent-main" {
			t.Fatalf("denied/stale completion leaked attention: %+v", params)
		}
	}
	if count != 1 {
		t.Fatalf("attention count: %d", count)
	}
	<-rechecked
	writer.Close()
	<-done
	connectionClosed = true
	// A host-lifetime subscriber starts after completion without any phone
	// attached; its bounded replay must still expose the admitted locator.
	subscribed := make(chan UserAttentionNotification, 1)
	subscriptionCtx, cancelSubscription := context.WithCancel(context.Background())
	subscriptionDone := make(chan error, 1)
	go func() {
		subscriptionDone <- host.SubscribeUserAttention(personalTestOwner(subscriptionCtx), func(event UserAttentionNotification) { subscribed <- event })
	}()
	var replay attentionCall
	select {
	case replay = <-policy.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("buffered attention did not recheck current policy")
	}
	replay.result <- true
	select {
	case event := <-subscribed:
		if event.TurnID != second.TurnID {
			t.Fatalf("wrong replay: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("host-lifetime attention lost before subscriber")
	}
	if err := host.SubscribeUserAttention(personalTestOwner(context.Background()), func(UserAttentionNotification) {}); err == nil {
		t.Fatal("competing attention subscriber accepted")
	}
	cancelSubscription()
	<-subscriptionDone
	// An admitted locator waiting for relay startup does not freeze old
	// preferences: replay must apply a newly disabled notification policy.
	third := send("replay-policy-change")
	initial := <-policy.calls
	initial.result <- true
	deniedReplayCtx, cancelDeniedReplay := context.WithCancel(context.Background())
	deniedReplayDone := make(chan error, 1)
	replayed := make(chan UserAttentionNotification, 2)
	go func() {
		deniedReplayDone <- host.SubscribeUserAttention(personalTestOwner(deniedReplayCtx), func(event UserAttentionNotification) { replayed <- event })
	}()
	var changed attentionCall
	select {
	case changed = <-policy.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("replay policy change was not checked")
	}
	if changed.input.TurnID != third.TurnID {
		t.Fatal("wrong preference replay")
	}
	changed.result <- false
	cancelDeniedReplay()
	<-deniedReplayDone
	select {
	case event := <-replayed:
		t.Fatalf("changed policy replayed: %+v", event)
	default:
	}
	// A buffered callback returning after retirement cannot regain admission.
	fourth := send("retired-completion")
	initial = <-policy.calls
	initial.result <- true
	retiredCtx, cancelRetired := context.WithCancel(context.Background())
	retiredDone := make(chan error, 1)
	go func() {
		retiredDone <- host.SubscribeUserAttention(personalTestOwner(retiredCtx), func(event UserAttentionNotification) { replayed <- event })
	}()
	var retired attentionCall
	select {
	case retired = <-policy.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("retirement replay policy missing")
	}
	if retired.input.TurnID != fourth.TurnID {
		t.Fatal("wrong retirement callback")
	}
	rt.PluginHost.RetirePlugin(context.Background(), "fixture", fmt.Errorf("synthetic disable"))
	retired.result <- true
	cancelRetired()
	<-retiredDone
	select {
	case event := <-replayed:
		t.Fatalf("retired generation replayed: %+v", event)
	default:
	}
	// Close joins admitted policy work after revocation, without leaving a
	// callback capable of publishing to a future connection.
	host.Close()
}
