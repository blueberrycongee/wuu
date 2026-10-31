package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

type crashQueueEvidence struct {
	SessionDir    string                       `json:"session_dir"`
	RootDir       string                       `json:"root_dir"`
	StateDir      string                       `json:"state_dir"`
	Ack           pluginhost.SessionSendResult `json:"ack"`
	RequestID     string                       `json:"request_id"`
	HostRequestID string                       `json:"host_request_id"`
}

// Authenticated busy acceptance survives process loss without automatic execution.
// The only exiting process is this test helper; no installed Wuu is contacted.
func TestAuthenticatedClientBusyQueueSurvivesCrash(t *testing.T) {
	for _, attachment := range []string{"text", "attachment"} {
		t.Run(attachment, func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, "ack.json")
			cmd := exec.Command(os.Args[0], "-test.run=^TestAuthenticatedQueueCrashHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "WUU_RECEIPT_CRASH_ACK="+output, "WUU_RECEIPT_CRASH_MODE="+attachment, "TMPDIR="+dir)
			logs, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("helper failed: %v\n%s", err, logs)
			}
			raw, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			var evidence crashQueueEvidence
			if err := json.Unmarshal(raw, &evidence); err != nil {
				t.Fatal(err)
			}
			t.Logf("child acknowledged request=%s host_request=%s state=%s queue_id=%s and exited without Close", evidence.RequestID, evidence.HostRequestID, evidence.Ack.State, evidence.Ack.QueueID)
			client := &fakeClient{}
			rt := newTestRuntime(t, client)
			rt.SessionDir, rt.RootDir, rt.StateDir = evidence.SessionDir, evidence.RootDir, evidence.StateDir
			rebindPersonalTestDocuments(t, rt)
			rt.UserSpaceID, rt.ProductDomain = "local", "personal"
			rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
			attachTestModelGeneration(t, rt, &pluginClientRequestTestRuntime{id: "fixture"})
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			th, err := srv.ensureThreadLoaded(testRuntimeState(t, srv.rt), evidence.Ack.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := srv.loadHeldClientInputs(testRuntimeState(t, srv.rt), th.ID)
			if err != nil {
				t.Fatal(err)
			}
			history, err := loadChatMessages(rt.Sessions, th.ID)
			if err != nil {
				t.Fatal(err)
			}
			result, found := srv.findSessionInput(th, pluginSessionRequestClientID("fixture", evidence.HostRequestID))
			count := 0
			for _, msg := range history {
				if msg.ClientID == pluginSessionRequestClientID("fixture", evidence.HostRequestID) {
					count++
				}
			}
			assertFakeClientRequestCount(t, client, 0)
			t.Logf("fresh host: held=%d history_matches=%d request_lookup_found=%t request_state=%s provider_calls=0", len(recovered), count, found, result.State)
			if len(recovered) != 1 || count != 0 {
				t.Fatalf("queued ACK lost authenticated payload on restart: held=%d history_matches=%d", len(recovered), count)
			}
			if recovered[0].msg.ClientID != pluginSessionRequestClientID("fixture", evidence.HostRequestID) {
				t.Fatal("recovery changed stable client identity")
			}
			if attachment == "attachment" && len(recovered[0].msg.Files) != 1 {
				t.Fatal("recovery lost attachment")
			}
		})
	}
}

func TestAuthenticatedQueueCrashHelper(t *testing.T) {
	output := os.Getenv("WUU_RECEIPT_CRASH_ACK")
	if output == "" {
		t.Skip("subprocess only")
	}
	client := &fakeClient{}
	admitted := os.Getenv("WUU_RECEIPT_CRASH_MODE") == "admitted"
	started := make(chan struct{})
	if admitted {
		client.onChat = func(_ int, _ providers.ChatRequest) { close(started); select {} }
	}
	rt := newTestRuntime(t, client)
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.StateDir = filepath.Join(filepath.Dir(output), "state")
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	attachTestModelGeneration(t, rt, &pluginClientRequestTestRuntime{id: "fixture"})
	srv := New(rt, &lockedBuffer{})
	created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCreateParams{RequestID: "create", Visibility: "plugin", ContextSource: "fresh", AgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	th := srv.thread(created.SessionID)
	th.mu.Lock()
	th.running = !admitted
	th.mu.Unlock()
	proof := runtime.ClientUserInput{RequestID: "client-request", Prompt: "synthetic immutable input"}
	params := pluginhost.SessionSendParams{RequestID: "host-request", UserInputRequestID: proof.RequestID, SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: proof.Prompt}}
	if os.Getenv("WUU_RECEIPT_CRASH_MODE") == "attachment" {
		raw := []byte("synthetic attachment bytes")
		digest := fmt.Sprintf("%x", sha256.Sum256(raw))
		proof.Attachments = []runtime.ClientInputAttachment{{SHA256: digest, Filename: "fixture.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString(raw)}}
		params.Input.Attachments = []pluginhost.SessionInputAttachment{{SHA256: digest, Filename: "fixture.txt", MediaType: "text/plain"}}
	}
	if err := runtime.ValidateClientUserInput(proof); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", params); err == nil {
		t.Fatal("input without proof admitted")
	}
	ctx := runtime.WithClientUserInput(context.Background(), "fixture", proof)
	ack, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), "fixture", params)
	expectedState := "queued"
	if admitted {
		expectedState = "running"
	}
	if err != nil || ack.State != expectedState || ack.QueueID == "" {
		t.Fatalf("queue: %+v %v", ack, err)
	}
	duplicate, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), "fixture", params)
	if err != nil || duplicate.QueueID != ack.QueueID {
		t.Fatalf("live duplicate changed identity: %+v %v", duplicate, err)
	}
	if admitted {
		<-started
		assertFakeClientRequestCount(t, client, 1)
	} else {
		assertFakeClientRequestCount(t, client, 0)
	}
	data, err := json.Marshal(crashQueueEvidence{SessionDir: rt.SessionDir, RootDir: rt.RootDir, StateDir: rt.StateDir, Ack: ack, RequestID: proof.RequestID, HostRequestID: params.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, data, 0600); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestAuthenticatedClientDuplicateIDRejectsChangedPayload(t *testing.T) {
	client := &fakeClient{}
	rt := newTestRuntime(t, client)
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	attachTestModelGeneration(t, rt, &pluginClientRequestTestRuntime{id: "fixture"})
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCreateParams{RequestID: "create", Visibility: "plugin", ContextSource: "fresh", AgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	th := srv.thread(created.SessionID)
	th.mu.Lock()
	th.running = true
	th.mu.Unlock()
	t.Cleanup(func() { th.mu.Lock(); th.running = false; th.mu.Unlock() })
	send := func(prompt, requestID string) (pluginhost.SessionSendResult, error) {
		proof := runtime.ClientUserInput{RequestID: requestID, Prompt: prompt}
		if err := runtime.ValidateClientUserInput(proof); err != nil {
			t.Fatal(err)
		}
		ctx := runtime.WithClientUserInput(context.Background(), "fixture", proof)
		return srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), "fixture", pluginhost.SessionSendParams{RequestID: "host-" + requestID, UserInputRequestID: requestID, SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: prompt}})
	}
	first, err := send("original payload", "one")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := send("original payload", "one")
	if err != nil || duplicate.QueueID != first.QueueID {
		t.Fatalf("same input not idempotent: %+v %v", duplicate, err)
	}
	other, err := send("original payload", "two")
	if err != nil || other.QueueID == first.QueueID {
		t.Fatalf("same text different ID collapsed: %+v %v", other, err)
	}
	changed, err := send("changed payload", "one")
	assertFakeClientRequestCount(t, client, 0)
	t.Logf("same-ID repeat queue=%s; different-ID same-text queue=%s; changed-payload response=%+v error=%v; provider_calls=0", duplicate.QueueID, other.QueueID, changed, err)
	if err == nil {
		t.Fatal("host acknowledged changed authenticated payload under an existing ID instead of rejecting it; Personal fingerprint validation currently masks this for agents.send")
	}
}

func TestAuthenticatedClientReceiptResumeAndCancel(t *testing.T) {
	for _, action := range []string{"resume", "attachment", "cancel", "takeover", "wrong_owner", "wrong_space", "resume_without_receipt", "coding"} {
		t.Run(action, func(t *testing.T) {
			client := &fakeClient{response: providersResponse("done")}
			rt := newTestRuntime(t, client)
			rt.UserSpaceID, rt.ProductDomain = "local", "personal"
			if action == "coding" {
				rt.ProductDomain = "coding"
			}
			rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
			attachTestModelGeneration(t, rt, &pluginClientRequestTestRuntime{id: "fixture"})
			srv := New(rt, &lockedBuffer{})
			created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCreateParams{RequestID: "create", Visibility: "plugin", ContextSource: "fresh", AgentID: "assistant"})
			if err != nil {
				t.Fatal(err)
			}
			th := srv.thread(created.SessionID)
			th.mu.Lock()
			th.running = true
			th.mu.Unlock()
			proof := runtime.ClientUserInput{RequestID: "client", Prompt: "immutable input"}
			params := pluginhost.SessionSendParams{RequestID: "host", UserInputRequestID: "client", SessionID: th.ID, Input: pluginhost.SessionInput{Prompt: proof.Prompt}}
			if action == "attachment" {
				raw := []byte("original persisted attachment")
				sum := fmt.Sprintf("%x", sha256.Sum256(raw))
				proof.Attachments = []runtime.ClientInputAttachment{{SHA256: sum, Filename: "original.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString(raw)}}
				params.Input.Attachments = []pluginhost.SessionInputAttachment{{SHA256: sum, Filename: "original.txt", MediaType: "text/plain"}}
			}
			ctx := runtime.WithClientUserInput(context.Background(), "fixture", proof)
			ack, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), "fixture", params)
			if err != nil || ack.Receipt == nil || ack.Receipt.AcceptedAt == "" || ack.Receipt.ReadAt != "" {
				t.Fatalf("durable ACK: %+v %v", ack, err)
			}
			th.mu.Lock()
			th.running = false
			th.mu.Unlock()
			srv.Close()
			out := &lockedBuffer{}
			srv = New(rt, out)
			t.Cleanup(srv.Close)
			inspected, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: th.ID, RequestID: "host"})
			if err != nil || inspected.Receipt == nil || inspected.Receipt.ExecutionState != "held" || inspected.Receipt.ReadAt != "" {
				t.Fatalf("held: %+v %v", inspected, err)
			}
			retry, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "fixture", proof)), "fixture", params)
			if err != nil || retry.Receipt == nil || retry.Receipt.ExecutionState != "held" {
				t.Fatalf("retry executed held: %+v %v", retry, err)
			}
			assertFakeClientRequestCount(t, client, 0)
			forgedResume := params
			forgedResume.ResumeAccepted = true
			if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "fixture", proof)), "fixture", forgedResume); err == nil {
				t.Fatal("ordinary retry proof resumed Held without receipt-bound continue intent")
			}
			assertFakeClientRequestCount(t, client, 0)
			if action == "resume_without_receipt" {
				params.ResumeAccepted = true
				if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "fixture", proof)), "fixture", params); err == nil {
					t.Fatal("ordinary send proof resumed Held without receipt-bound continue proof")
				}
				assertFakeClientRequestCount(t, client, 0)
				return
			}
			if action == "wrong_space" {
				rt.UserSpaceID = "other-space"
				if _, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: th.ID, RequestID: "host"}); err == nil {
					t.Fatal("foreign space receipt exposed")
				}
				if _, err := srv.cancelPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCancelParams{SessionID: th.ID, QueueID: ack.QueueID}); err == nil {
					t.Fatal("foreign space cancelled input")
				}
				params.ResumeAccepted = true
				params.ResumeReceiptID = ack.Receipt.ReceiptID
				params.Input = pluginhost.SessionInput{}
				resumeProof := runtime.ClientUserInput{RequestID: "client", ResumeReceiptID: ack.Receipt.ReceiptID}
				if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "fixture", resumeProof)), "fixture", params); err == nil {
					t.Fatal("foreign space resumed input")
				}
				assertFakeClientRequestCount(t, client, 0)
				return
			}
			if action == "wrong_owner" {
				if _, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "other", pluginhost.SessionInspectParams{SessionID: th.ID, RequestID: "host"}); err == nil {
					t.Fatal("foreign receipt exposed")
				}
				return
			}
			if action == "cancel" {
				result, err := srv.cancelPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCancelParams{SessionID: th.ID, QueueID: ack.QueueID})
				if err != nil || !result.Cancelled {
					t.Fatalf("cancel held: %+v %v", result, err)
				}
			}
			if action == "takeover" {
				if _, err := rt.Sessions.ChangeControl(th.ID, "other", session.ControlActive, 0); err != nil {
					t.Fatal(err)
				}
			}
			params.ResumeAccepted = true
			params.ResumeReceiptID = ack.Receipt.ReceiptID
			params.Input = pluginhost.SessionInput{}
			proof = runtime.ClientUserInput{RequestID: "client", ResumeReceiptID: ack.Receipt.ReceiptID}
			resumed, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "fixture", proof)), "fixture", params)
			if action == "takeover" {
				if err == nil {
					t.Fatal("resume ignored foreign control")
				}
				assertFakeClientRequestCount(t, client, 0)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if action == "cancel" {
				if resumed.Receipt == nil || resumed.Receipt.ExecutionState != "cancelled" {
					t.Fatalf("cancel revived: %+v", resumed)
				}
				assertFakeClientRequestCount(t, client, 0)
				return
			}
			waitForTurnCompletedForThread(t, out, th.ID)
			final, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: th.ID, RequestID: "host"})
			if err != nil || final.Receipt == nil || final.Receipt.ReadAt == "" || final.Receipt.MessageSeq == 0 || final.Receipt.HostTurnID == "" || final.Receipt.ReceiptID != ack.Receipt.ReceiptID {
				t.Fatalf("admitted identity: %+v %v", final, err)
			}
			assertFakeClientRequestCount(t, client, 1)
			if action == "attachment" {
				history, err := loadChatMessages(rt.Sessions, th.ID)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, m := range history {
					if m.ClientID == final.Receipt.ClientID {
						if len(m.Files) != 1 {
							t.Fatal("attachment lost")
						}
						data, err := os.ReadFile(m.Files[0].LocalPath)
						if err != nil || string(data) != "original persisted attachment" {
							t.Fatalf("changed original attachment: %s %v", data, err)
						}
						found = true
					}
				}
				if !found {
					t.Fatal("accepted history absent")
				}
			}
		})
	}
}

func TestAcceptedClientDrainRejectsRevocation(t *testing.T) {
	for _, mode := range []string{"control", "archived", "plugin_removed", "cancelled", "stale_revision"} {
		t.Run(mode, func(t *testing.T) {
			client := &fakeClient{}
			rt := newTestRuntime(t, client)
			rt.UserSpaceID, rt.ProductDomain = "local", "personal"
			rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
			attachTestModelGeneration(t, rt, &pluginClientRequestTestRuntime{id: "fixture"})
			srv := New(rt, &lockedBuffer{})
			defer srv.Close()
			created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCreateParams{RequestID: "create", Visibility: "plugin", ContextSource: "fresh", AgentID: "assistant"})
			if err != nil {
				t.Fatal(err)
			}
			th := srv.thread(created.SessionID)
			th.mu.Lock()
			th.running = true
			th.mu.Unlock()
			proof := runtime.ClientUserInput{RequestID: "client", Prompt: "immutable"}
			params := pluginhost.SessionSendParams{RequestID: "host", UserInputRequestID: "client", SessionID: th.ID, Input: pluginhost.SessionInput{Prompt: proof.Prompt}}
			ack, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "fixture", proof)), "fixture", params)
			if err != nil {
				t.Fatal(err)
			}
			row, _, err := rt.Sessions.ReadClientInput(srv.clientInputScope(th.ID, "fixture"), "host")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "control":
				_, err = rt.Sessions.ChangeControl(th.ID, "foreign", session.ControlActive, 0)
			case "archived":
				_, err = rt.Sessions.UpdateArchived(th.ID, true)
			case "plugin_removed":
				rt.ActivePlugins = nil
			case "cancelled":
				_, err = srv.cancelPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCancelParams{SessionID: th.ID, QueueID: ack.QueueID})
			case "stale_revision":
				_, err = rt.Sessions.ChangeClientInput(row, "held", "", nil, nil, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			th.mu.Lock()
			th.running = false
			th.mu.Unlock()
			srv.tryDrainQueuedTurns(testRuntimeState(t, srv.rt), th.ID, true)
			history, err := loadChatMessages(rt.Sessions, th.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range history {
				if m.ClientID == row.Receipt.ClientID {
					t.Fatal("revoked input entered history")
				}
			}
			assertFakeClientRequestCount(t, client, 0)
		})
	}
}

func TestAdmittedClientCrashRemainsUnconfirmedWithoutReplay(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "ack.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAuthenticatedQueueCrashHelper$")
	cmd.Env = append(os.Environ(), "WUU_RECEIPT_CRASH_ACK="+output, "WUU_RECEIPT_CRASH_MODE=admitted", "TMPDIR="+dir)
	if logs, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, logs)
	}
	var evidence crashQueueEvidence
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	rt := newTestRuntime(t, client)
	rt.SessionDir, rt.RootDir, rt.StateDir = evidence.SessionDir, evidence.RootDir, evidence.StateDir
	rebindPersonalTestDocuments(t, rt)
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	attachTestModelGeneration(t, rt, &pluginClientRequestTestRuntime{id: "fixture"})
	srv := New(rt, &lockedBuffer{})
	defer srv.Close()
	result, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: evidence.Ack.SessionID, RequestID: evidence.HostRequestID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt == nil || result.Receipt.ExecutionState != "unconfirmed" || result.Receipt.MessageSeq == 0 {
		t.Fatalf("lost execution ambiguity: %+v", result)
	}
	proof := runtime.ClientUserInput{RequestID: evidence.RequestID, ResumeReceiptID: result.Receipt.ReceiptID}
	resumed, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "fixture", proof)), "fixture", pluginhost.SessionSendParams{RequestID: evidence.HostRequestID, UserInputRequestID: evidence.RequestID, SessionID: evidence.Ack.SessionID, ResumeAccepted: true, ResumeReceiptID: proof.ResumeReceiptID})
	if err != nil || resumed.Receipt == nil || resumed.Receipt.ExecutionState != "unconfirmed" {
		t.Fatalf("unsafe replay: %+v %v", resumed, err)
	}
	assertFakeClientRequestCount(t, client, 0)
}

func TestAuthenticatedClientTerminalReceiptUsesExecutedTurnIdentity(t *testing.T) {
	for _, outcome := range []string{"completed", "failed", "cancel_running"} {
		t.Run(outcome, func(t *testing.T) {
			client := &fakeClient{response: providersResponse("done")}
			entered, release := make(chan struct{}), make(chan struct{})
			if outcome == "failed" {
				client.err = errors.New("synthetic provider failure")
			}
			if outcome == "cancel_running" {
				client.onChat = func(_ int, _ providers.ChatRequest) { close(entered); <-release }
			}
			rt := newTestRuntime(t, client)
			rt.UserSpaceID, rt.ProductDomain = "local", "personal"
			rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
			attachTestModelGeneration(t, rt, &pluginClientRequestTestRuntime{id: "fixture"})
			out := &lockedBuffer{}
			srv := New(rt, out)
			defer srv.Close()
			created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCreateParams{RequestID: "create", Visibility: "plugin", ContextSource: "fresh", AgentID: "assistant"})
			if err != nil {
				t.Fatal(err)
			}
			proof := runtime.ClientUserInput{RequestID: "client", Prompt: "execute exactly once"}
			ack, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "fixture", proof)), "fixture", pluginhost.SessionSendParams{RequestID: "host", UserInputRequestID: "client", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: proof.Prompt}})
			if err != nil {
				t.Fatal(err)
			}
			var actualTurn string
			for _, m := range waitForMethod(t, out, NotificationTurnStarted) {
				if m["method"] == NotificationTurnStarted {
					event := remarshal[TurnStartedNotification](t, m["params"])
					if event.ThreadID == created.SessionID {
						actualTurn = event.Turn.ID
					}
				}
			}
			if outcome == "cancel_running" {
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					close(release)
					t.Fatal("provider did not start")
				}
				cancelled, err := srv.cancelPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionCancelParams{SessionID: created.SessionID, TurnID: actualTurn})
				close(release)
				if err != nil || !cancelled.Cancelled {
					t.Fatalf("cancel: %+v %v", cancelled, err)
				}
			}
			if outcome == "completed" {
				waitForTurnCompletedForThread(t, out, created.SessionID)
			} else {
				waitForMethod(t, out, NotificationTurnError)
			}
			want := outcome
			if outcome == "cancel_running" {
				want = "interrupted"
			}
			result, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: "host", Wait: "terminal", TimeoutMS: 1000})
			if err != nil {
				t.Fatal(err)
			}
			if ack.Receipt == nil || ack.Receipt.HostTurnID != actualTurn || ack.TurnID != actualTurn || result.Receipt == nil || result.Receipt.HostTurnID != actualTurn || result.Receipt.ExecutionState != want || result.Receipt.ReceiptState != "in_history" {
				t.Fatalf("receipt/execution mismatch: actual=%s ack=%+v final=%+v want=%s", actualTurn, ack.Receipt, result.Receipt, want)
			}
			srv.Close()
			srv = New(rt, &lockedBuffer{})
			defer srv.Close()
			restarted, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: "host"})
			if err != nil || restarted.Receipt == nil || restarted.Receipt.ExecutionState != want || restarted.Receipt.HostTurnID != actualTurn {
				t.Fatalf("terminal lost on restart: %+v %v", restarted, err)
			}
			assertFakeClientRequestCount(t, client, 1)
		})
	}
}
