package appserver

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Exercise separate session authorities: raw source locators cannot read the
// destination store, and only a locally authenticated, signed immutable export
// crosses the boundary. The receiving host owns attachment materialization.
func TestDesktopSessionTransportTwoHostsPreserveOriginalAndRejectForgery(t *testing.T) {
	sourceRT := newTestRuntime(t, &fakeClient{})
	sourceRT.UserSpaceID, sourceRT.ProductDomain = "local", "personal"
	source := New(sourceRT, &lockedBuffer{})
	defer source.Close()
	targetModel := &fakeClient{response: providersResponse("done")}
	targetRT := newTestRuntime(t, targetModel)
	targetRT.StreamRunner.Tools = newBlockingToolExecutor()
	attachTestModelGeneration(t, targetRT)
	output := &lockedBuffer{}
	target := New(targetRT, output)
	defer target.Close()
	sourceOwner, admitErr := clientauthority.AdmitDesktop(principal.WithContext(context.Background(), principal.LocalOwner()), sourceRT.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if admitErr != nil {
		t.Fatal(admitErr)
	}
	sourceCtx := pluginInvocationContext(t, sourceRT, sourceOwner)
	targetCtx := pluginInvocationContext(t, targetRT, principal.WithContext(context.Background(), principal.LocalOwner()))
	src, e := source.createPluginSession(sourceCtx, "fixture", pluginhost.SessionCreateParams{RequestID: "source", Visibility: "plugin", ContextSource: "fresh"})
	if e != nil {
		t.Fatal(e)
	}
	dst, e := target.createPluginSession(targetCtx, "fixture", pluginhost.SessionCreateParams{RequestID: "existing-coding", Visibility: "user", ContextSource: "fresh", ToolPolicy: &pluginhost.SessionToolPolicy{Deny: []string{"*"}}})
	if e != nil {
		t.Fatal(e)
	}
	original := "  用户原话\nInspect only; do not deploy.  "
	raw, _ := json.Marshal([]providers.InputFile{{Filename: "notes.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString([]byte("source bytes")), LocalPath: "/source-only/not-valid"}})
	if e = sourceRT.Sessions.AppendHistoryRecord(src.SessionID, session.HistoryRecord{Role: "user", Origin: "user", DisplayContent: original, Content: "source-expanded", Files: raw}); e != nil {
		t.Fatal(e)
	}
	params, _ := json.Marshal(pluginhost.SessionSendParams{SessionID: dst.SessionID, RequestID: "same-send", Input: pluginhost.SessionInput{Prompt: "Agent task instruction", Handoff: &pluginhost.SessionHandoffInput{SourceMessages: []providers.MessageRef{{SessionID: src.SessionID, Seq: 1}}}}})
	request := pluginhost.SessionRouteRequest{Version: 1, Operation: "send", OperationID: "same-send", Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "desktop:" + target.desktopSessionIdentity().HostID, SessionID: dst.SessionID}, Params: params}
	prepared, release, e := source.prepareDesktopSessionRoute(sourceCtx, "fixture", "fixture.transport", request)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	// One caller request cannot be retargeted to a different logical authority,
	// even if both targets are legitimate local-owner contexts.
	retargeted := request
	retargeted.Locator.ExecutionHost = "desktop-project:another-project"
	if _, cleanup, err := source.prepareDesktopSessionRoute(sourceCtx, "fixture", "fixture.transport", retargeted); err == nil {
		cleanup()
		t.Fatal("same request identity retargeted to another host")
	}
	if _, cleanup, err := source.prepareDesktopSessionRoute(sourceCtx, "fixture", "fixture.transport", request); err != nil {
		t.Fatalf("same logical send cannot retry export: %v", err)
	} else {
		cleanup()
	}
	targetIdentity := target.desktopSessionIdentity()
	exported, e := source.exportDesktopSession(sourceCtx, prepared.TransportTicket.ID, &targetIdentity)
	if e != nil {
		t.Fatal(e)
	}
	trusted := source.desktopSessionIdentity()
	receipt, e := target.dispatchDesktopSession(targetCtx, desktopSessionImport{Export: exported, TrustedSource: trusted})
	if e != nil {
		t.Fatal(e)
	}
	waitForMethod(t, output, NotificationTurnCompleted)
	targetModel.mu.Lock()
	toolCount := len(targetModel.requests[0].Tools)
	targetModel.mu.Unlock()
	if toolCount != 0 {
		t.Fatalf("handoff widened target deny policy: tools=%d", toolCount)
	}
	retry, e := target.dispatchDesktopSession(targetCtx, desktopSessionImport{Export: exported, TrustedSource: trusted})
	var initialResult, retryResult pluginhost.SessionSendResult
	_ = json.Unmarshal(receipt.Result, &initialResult)
	_ = json.Unmarshal(retry.Result, &retryResult)
	if e != nil || retryResult.TurnID != initialResult.TurnID {
		t.Fatalf("retry changed receipt: %+v %+v %v", receipt, retry, e)
	}
	history, e := loadChatMessages(targetRT.Sessions, dst.SessionID)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, message := range history {
		if message.Origin == "plugin" {
			count++
			if strings.Contains(message.Content, "/source-only/") || !strings.Contains(message.Content, "用户原话") || !message.ReadOnly || len(message.Files) != 1 {
				t.Fatalf("lost provenance: %+v", message)
			}
			bytes, e := os.ReadFile(message.Files[0].LocalPath)
			if e != nil || string(bytes) != "source bytes" {
				t.Fatalf("invalid target material: %q %v", bytes, e)
			}
		}
	}
	if count != 1 {
		t.Fatalf("deliveries=%d", count)
	}
	forged := exported
	forged.Payload = strings.Replace(exported.Payload, "Inspect only", "Deploy now", 1)
	if _, e = target.dispatchDesktopSession(targetCtx, desktopSessionImport{Export: forged, TrustedSource: trusted}); e == nil {
		t.Fatal("forged original accepted")
	}
	stranger := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	defer stranger.Close()
	if _, e = stranger.dispatchDesktopSession(pluginInvocationContext(t, stranger.rt, targetCtx), desktopSessionImport{Export: exported, TrustedSource: trusted}); e == nil {
		t.Fatal("target replacement accepted old ticket")
	}
	for _, field := range []string{"execution_authorization", "control_revision", "resume_accepted", "user_input_request_id", "permission_mode", "provider", "tool_policy"} {
		var payload desktopSessionPayload
		_ = json.Unmarshal([]byte(exported.Payload), &payload)
		var args map[string]any
		_ = json.Unmarshal(payload.Request.Params, &args)
		switch field {
		case "execution_authorization":
			args[field] = map[string]string{"provider": "forged", "id": "forged"}
		case "control_revision":
			args[field] = 1
		case "resume_accepted":
			args[field] = true
		case "tool_policy":
			args[field] = map[string]any{"allow": []string{"*"}}
		default:
			args[field] = "forged"
		}
		payload.Request.Params, _ = json.Marshal(args)
		raw, _ := json.Marshal(payload)
		changed := desktopSessionExport{Version: 1, Payload: string(raw), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(source.desktopSessions.key, raw))}
		if _, e = target.dispatchDesktopSession(targetCtx, desktopSessionImport{Export: changed, TrustedSource: trusted}); e == nil {
			t.Fatalf("sender authority override admitted: %s", field)
		}
	}
	release()
	if _, e = source.exportDesktopSession(sourceCtx, prepared.TransportTicket.ID); e == nil {
		t.Fatal("expired source ticket remained readable")
	}
}

// UserHost reverse RPC remains per-connection and method-specific; an observer
// cannot receive tickets or settle a request belonging to the local desktop.
func TestUserHostDesktopBridgeIsNarrowAndConnectionBound(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	host, err := NewUserHost(rt, personalhost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	in, write := io.Pipe()
	out := &lockedBuffer{}
	done := make(chan error, 1)
	owner, admitErr := clientauthority.AdmitDesktop(principal.WithContext(context.Background(), principal.LocalOwner()), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if admitErr != nil {
		t.Fatal(admitErr)
	}
	go func() { done <- host.Serve(owner, in, out, nil) }()
	defer func() { write.Close(); <-done }()
	fmt.Fprintln(write, `{"id":"init","method":"initialize","params":{"capabilities":{"reverse_rpc":{"methods":["session/desktop/dispatch","browser/cdp"]}}}}`)
	if response := waitForResponseByID(t, out, "init"); response["error"] != nil {
		t.Fatal(response)
	}
	if host.server.supportsClientMethod("browser/cdp") || host.server.supportsClientMethod(desktopSessionDispatchMethod) {
		t.Fatal("user host enabled shared reverse RPC")
	}
	host.mu.Lock()
	var live context.Context
	for _, c := range host.connections {
		live = c.ctx
	}
	host.mu.Unlock()
	result := make(chan error, 1)
	go func() {
		raw, err := host.callDesktopSession(live, map[string]any{"version": 1, "ticket_id": "opaque", "source_host": "source"})
		if err == nil && string(raw) != `{"ok":true}` {
			err = fmt.Errorf("bad receipt: %s", raw)
		}
		result <- err
	}()
	frames := waitForMethod(t, out, desktopSessionDispatchMethod)
	var id []byte
	for _, frame := range frames {
		if frame["method"] == desktopSessionDispatchMethod {
			id, _ = json.Marshal(frame["id"])
		}
	}
	peerInput, peerWrite := io.Pipe()
	peerOutput := &lockedBuffer{}
	peerDone := make(chan error, 1)
	peerOwner, admitErr := clientauthority.AdmitDesktop(principal.WithContext(context.Background(), principal.LocalOwner()), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if admitErr != nil {
		t.Fatal(admitErr)
	}
	go func() { peerDone <- host.Serve(peerOwner, peerInput, peerOutput, nil) }()
	defer func() { peerWrite.Close(); <-peerDone }()
	fmt.Fprintln(peerWrite, `{"id":"peer-init","method":"initialize","params":{"capabilities":{"reverse_rpc":{"methods":["session/desktop/dispatch"]}}}}`)
	waitForResponseByID(t, peerOutput, "peer-init")
	fmt.Fprintf(peerWrite, "{\"id\":%s,\"result\":{\"forged\":true}}\n", id)
	fmt.Fprintln(peerWrite, `{"id":"peer-fence","method":"initialize"}`)
	waitForResponseByID(t, peerOutput, "peer-fence")
	select {
	case err := <-result:
		t.Fatalf("other connection settled source request: %v", err)
	default:
	}
	if strings.Contains(peerOutput.String(), `"ticket_id":"opaque"`) {
		t.Fatal("ticket was broadcast to another connection")
	}
	fmt.Fprintf(write, "{\"id\":%s,\"result\":{\"ok\":true}}\n", id)
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = host.callDesktopSession(ctx, map[string]any{}); err == nil {
		t.Fatal("cancelled dispatch admitted")
	}
}

func TestDesktopSessionTransportAdmissionRejectsAbsentAndOtherOwnerConnections(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	s := New(rt, &lockedBuffer{})
	defer s.Close()
	request := pluginhost.SessionRouteRequest{Version: 1, Operation: "list", OperationID: "list", Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "desktop"}, Params: json.RawMessage(`{"scope":"shared"}`)}
	if _, _, err := s.prepareDesktopSessionRoute(pluginInvocationContext(t, rt, context.Background()), "fixture", "transport", request); err == nil {
		t.Fatal("ambient local owner borrowed live desktop authority")
	}
	admitted := func() context.Context {
		ctx, err := clientauthority.AdmitDesktop(principal.WithContext(context.Background(), principal.LocalOwner()), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
		if err != nil {
			t.Fatal(err)
		}
		return pluginInvocationContext(t, rt, ctx)
	}
	source := admitted()
	p, release, err := s.prepareDesktopSessionRoute(source, "fixture", "transport", request)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = s.exportDesktopSession(admitted(), p.TransportTicket.ID); err == nil {
		t.Fatal("different admitted owner connection exported ticket")
	}
	if _, err = s.dispatchDesktopTicket(source, "different-provider", p.TransportTicket.ID); err == nil {
		t.Fatal("unselected provider dispatched ticket")
	}
	peer, _ := principal.OwnerConnection(principal.LocalOwner().Identity(), func() bool { return true })
	if _, err = s.exportDesktopSession(principal.WithContext(source, peer), p.TransportTicket.ID); err == nil {
		t.Fatal("owner-shaped remote principal gained local transport authority")
	}
	if _, err = s.exportDesktopSession(source, p.TransportTicket.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopSessionTransportCancellationDoesNotRepeatOrStopUnrelatedTurn(t *testing.T) {
	sourceRT := newTestRuntime(t, &fakeClient{})
	sourceRT.UserSpaceID, sourceRT.ProductDomain = "local", "personal"
	source := New(sourceRT, &lockedBuffer{})
	defer source.Close()
	owner, err := clientauthority.AdmitDesktop(personalTestOwner(context.Background()), sourceRT.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	sourceCtx := pluginInvocationContext(t, sourceRT, owner)
	src, err := source.createPluginSession(sourceCtx, "fixture", pluginhost.SessionCreateParams{RequestID: "source", Visibility: "plugin", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	if err = sourceRT.Sessions.AppendHistoryRecord(src.SessionID, session.HistoryRecord{Role: "user", Origin: "user", Content: "exact cancellation fixture"}); err != nil {
		t.Fatal(err)
	}
	model := newBlockingStreamClient("unrelated work completed")
	targetRT := newTestRuntime(t, &fakeClient{})
	targetRT.StreamRunner.Client = providers.AdaptStreamClient(model)
	attachTestModelGeneration(t, targetRT)
	target := New(targetRT, &lockedBuffer{})
	defer target.Close()
	defer close(model.release)
	targetCtx := pluginInvocationContext(t, targetRT, personalTestOwner(context.Background()))
	dst, err := target.createPluginSession(targetCtx, "other", pluginhost.SessionCreateParams{RequestID: "existing", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := target.sendPluginSession(targetCtx, "other", pluginhost.SessionSendParams{RequestID: "unrelated", SessionID: dst.SessionID, Input: pluginhost.SessionInput{Prompt: "keep running"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.started:
	case <-time.After(3 * time.Second):
		t.Fatal("target model did not start")
	}
	identity := target.desktopSessionIdentity()
	locator := pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "desktop-project:fixture", SessionID: dst.SessionID}
	export := func(op, id string, params any) desktopSessionExport {
		t.Helper()
		raw, _ := json.Marshal(params)
		prepared, release, e := source.prepareDesktopSessionRoute(sourceCtx, "fixture", "transport", pluginhost.SessionRouteRequest{Version: 1, Operation: op, OperationID: id, Locator: locator, Params: raw})
		if e != nil {
			t.Fatal(e)
		}
		defer release()
		value, e := source.exportDesktopSession(sourceCtx, prepared.TransportTicket.ID, &identity)
		if e != nil {
			t.Fatal(e)
		}
		return value
	}
	sentExport := export("send", "message", pluginhost.SessionSendParams{SessionID: dst.SessionID, RequestID: "message", Input: pluginhost.SessionInput{Prompt: "new instruction", Handoff: &pluginhost.SessionHandoffInput{SourceMessages: []providers.MessageRef{{SessionID: src.SessionID, Seq: 1}}}}})
	deliver := func(value desktopSessionExport) (pluginhost.SessionRouteResult, error) {
		return target.dispatchDesktopSession(targetCtx, desktopSessionImport{Export: value, TrustedSource: source.desktopSessionIdentity()})
	}
	sent, err := deliver(sentExport)
	if err != nil {
		t.Fatal(err)
	}
	var receipt pluginhost.SessionSendResult
	_ = json.Unmarshal(sent.Result, &receipt)
	if receipt.QueueID == "" {
		t.Fatalf("not queued: %s", sent.Result)
	}
	foreign := export("cancel", "foreign-cancel", pluginhost.SessionCancelParams{SessionID: dst.SessionID, TurnID: unrelated.TurnID})
	if _, err = deliver(foreign); err == nil {
		t.Fatal("cancelled unrelated target work")
	}
	cancelled, err := deliver(export("cancel", "cancel-message", pluginhost.SessionCancelParams{SessionID: dst.SessionID, QueueID: receipt.QueueID}))
	if err != nil {
		t.Fatal(err)
	}
	var cancelResult pluginhost.SessionCancelResult
	_ = json.Unmarshal(cancelled.Result, &cancelResult)
	if !cancelResult.Cancelled {
		t.Fatalf("not cancelled: %s", cancelled.Result)
	}
	retry, err := deliver(sentExport)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(retry.Result, &receipt)
	if receipt.State != "discarded" {
		t.Fatalf("cancelled message replayed: %s", retry.Result)
	}
	status, err := target.inspectPluginSession(targetCtx, "other", pluginhost.SessionInspectParams{SessionID: dst.SessionID, RequestID: "unrelated"})
	if err != nil || status.Turn == nil || status.Turn.State != "running" {
		t.Fatalf("unrelated work stopped: %+v %v", status, err)
	}
}
