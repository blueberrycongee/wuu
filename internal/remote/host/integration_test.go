package host_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/appserver"
	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/remote/phone"
	"github.com/blueberrycongee/wuu/internal/remote/relay"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// gatedClient is a deterministic provider: each Chat call waits for one
// release token, then returns a fixed reply. It lets the test decide exactly
// when a turn completes (in particular: after the phone has gone offline).
type gatedClient struct {
	release chan struct{}
}

func (c *gatedClient) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	select {
	case <-c.release:
		return providers.ChatResponse{Content: "remote turn done"}, nil
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
}

type recordingPusher struct {
	mu     sync.Mutex
	events []relay.PushEvent
}

func (p *recordingPusher) Push(_ context.Context, ev relay.PushEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev)
}

func (p *recordingPusher) hints() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.events))
	for _, ev := range p.events {
		out = append(out, ev.Hint)
	}
	return out
}

func newRemoteTestRuntime(t *testing.T, client providers.Client) *runtime.Session {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	kit, err := tools.New(root)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	return &runtime.Session{
		ProviderName:   "fake-provider",
		Model:          "fake-model",
		RootDir:        root,
		ConfigPath:     filepath.Join(root, ".wuu.json"),
		SessionDir:     filepath.Join(root, ".wuu-state", "sessions"),
		HookDispatcher: hooks.NewDispatcher(nil),
		Toolkit:        kit,
		StreamRunner: &agent.StreamRunner{
			LoopDriver: loopdriver.DefaultDriver{},

			Client:       providers.AdaptStreamClient(client),
			Model:        "fake-model",
			SystemPrompt: "system prompt",
		},
	}
}

// TestRemoteControlEndToEnd drives the full main chain with real components
// on localhost: blind relay, host with a live app-server runtime, and a
// phone client.
//
//  1. pair through the relay (QR uri -> offer -> answer)
//  2. attach and run a real turn from the phone, streaming events back
//  3. drop the phone mid-turn; the turn finishes while detached, the host
//     spools the events and asks the relay for a content-free push
//  4. reconnect: the session resumes and replays exactly the missed lines
func TestRemoteControlEndToEnd(t *testing.T) {
	testCtx, cancelTest := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancelTest()

	// Relay.
	pusher := &recordingPusher{}
	reg, err := relay.OpenRegistry("")
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	relaySrv := relay.New(relay.Options{
		Registry:        reg,
		Pusher:          pusher,
		Logf:            t.Logf,
		PushMinInterval: 10 * time.Millisecond,
	})
	ts := httptest.NewServer(relaySrv.Handler())
	defer ts.Close()
	relayURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/connect"

	// Host with a real runtime over a gated fake provider.
	gate := &gatedClient{release: make(chan struct{}, 8)}
	rt := newRemoteTestRuntime(t, gate)
	store, err := host.LoadOrCreateStore(filepath.Join(t.TempDir(), "remote.json"), "test-host")
	if err != nil {
		t.Fatalf("LoadOrCreateStore: %v", err)
	}
	if err := store.SetRelayURL(relayURL); err != nil {
		t.Fatalf("SetRelayURL: %v", err)
	}
	uriCh := make(chan string, 1)
	h, err := host.New(host.Options{
		Runtime: rt,
		Store:   store,
		Logf:    t.Logf,
		Pairing: &host.PairingConfig{
			Once:  true,
			OnURI: func(uri string) { uriCh <- uri },
		},
		ReconnectMin:    50 * time.Millisecond,
		PushMinInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("host.New: %v", err)
	}
	hostCtx, cancelHost := context.WithCancel(testCtx)
	defer cancelHost()
	hostDone := make(chan struct{})
	go func() {
		defer close(hostDone)
		_ = h.Run(hostCtx)
	}()
	uri := <-uriCh

	// A published URI must be usable on the first attempt.
	pairCtx, cancelPair := context.WithTimeout(testCtx, 5*time.Second)
	creds, err := phone.Pair(pairCtx, uri, "test-phone")
	cancelPair()
	if err != nil {
		t.Fatalf("pairing after readiness failed: %v", err)
	}
	if creds.HostName != "test-host" {
		t.Fatalf("paired host name = %q", creds.HostName)
	}
	if len(store.Devices()) != 1 || store.Devices()[0].Name != "test-phone" {
		t.Fatalf("host store devices = %+v", store.Devices())
	}

	// Connect and attach.
	client, err := phone.NewClient(creds, phone.Options{Logf: t.Logf, ReconnectMin: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	phase1Ctx, cancelPhase1 := context.WithCancel(testCtx)
	phase1Done := make(chan struct{})
	go func() {
		defer close(phase1Done)
		_ = client.Run(phase1Ctx)
	}()
	attachCtx, cancelAttach := context.WithTimeout(testCtx, 20*time.Second)
	if err := client.WaitAttached(attachCtx); err != nil {
		t.Fatalf("WaitAttached: %v", err)
	}
	cancelAttach()
	first := <-client.AttachEvents()
	if first.Resumed {
		t.Fatalf("first attach must not be resumed")
	}

	proto := client.Protocol()
	callCtx, cancelCall := context.WithTimeout(testCtx, 30*time.Second)
	defer cancelCall()

	var init appserver.InitializeResult
	if err := proto.Call(callCtx, appserver.MethodInitialize, nil, &init); err != nil {
		t.Fatalf("initialize over remote channel: %v", err)
	}
	if init.ProtocolVersion == "" {
		t.Fatalf("initialize returned empty protocol version")
	}

	var started appserver.ThreadStartResult
	if err := proto.Call(callCtx, appserver.MethodThreadStart, appserver.ThreadStartParams{}, &started); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	tid := started.Thread.ID

	// Turn 1: completes while the phone is attached and streaming.
	gate.release <- struct{}{}
	var turn1 appserver.TurnStartResult
	if err := proto.Call(callCtx, appserver.MethodTurnStart, appserver.TurnStartParams{ThreadID: tid, Prompt: "hello from the phone"}, &turn1); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	waitTurnCompleted(t, testCtx, proto.Notifications(), tid, "turn 1")

	// The host state snapshot reached the phone at attach time.
	if st, ok := client.LatestState(); !ok || st.Host.Name != "test-host" {
		t.Fatalf("latest state = %+v, %v", st, ok)
	}

	// The turn is persisted in the shared session store: the desktop would
	// see exactly this thread.
	records, err := session.LoadHistoryRecords(rt.SessionDir, tid, false)
	if err != nil {
		t.Fatalf("LoadHistoryRecords: %v", err)
	}
	foundPrompt := false
	for _, r := range records {
		if strings.Contains(r.Content, "hello from the phone") {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("phone prompt not persisted; records=%d", len(records))
	}

	// Turn 2: starts attached, finishes while the phone is away.
	var turn2 appserver.TurnStartResult
	if err := proto.Call(callCtx, appserver.MethodTurnStart, appserver.TurnStartParams{ThreadID: tid, Prompt: "work while I am gone"}, &turn2); err != nil {
		t.Fatalf("turn/start 2: %v", err)
	}

	// Phone drops off the network mid-turn.
	cancelPhase1()
	<-phase1Done
	// Give the relay/host a moment to observe the disconnect (presence).
	time.Sleep(750 * time.Millisecond)

	// The agent finishes while nobody is watching.
	gate.release <- struct{}{}

	// The host must ask the relay for a content-free push.
	pushDeadline := time.Now().Add(15 * time.Second)
	for {
		if containsHint(pusher.hints(), "agent_done") {
			break
		}
		if time.Now().After(pushDeadline) {
			t.Fatalf("no agent_done push while detached; hints=%v", pusher.hints())
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Reconnect with the same client state: the host resumes the session
	// and replays the missed lines, including turn 2's completion.
	phase2Ctx, cancelPhase2 := context.WithCancel(testCtx)
	defer cancelPhase2()
	phase2Done := make(chan struct{})
	go func() {
		defer close(phase2Done)
		_ = client.Run(phase2Ctx)
	}()
	defer func() {
		cancelPhase2()
		<-phase2Done
	}()

	reattachCtx, cancelReattach := context.WithTimeout(testCtx, 20*time.Second)
	if err := client.WaitAttached(reattachCtx); err != nil {
		t.Fatalf("WaitAttached after reconnect: %v", err)
	}
	cancelReattach()
	second := <-client.AttachEvents()
	if !second.Resumed {
		t.Fatalf("second attach should resume, got %+v", second)
	}
	if client.Protocol() != proto {
		t.Fatalf("resumed attach must keep the protocol client")
	}
	waitTurnCompleted(t, testCtx, proto.Notifications(), tid, "turn 2 (replayed)")
}

func waitTurnCompleted(t *testing.T, ctx context.Context, notes <-chan wuuexec.Notification, threadID, label string) {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("%s: context done before turn completed", label)
		case note, ok := <-notes:
			if !ok {
				t.Fatalf("%s: notifications closed before turn completed", label)
			}
			switch note.Method {
			case appserver.NotificationTurnCompleted:
				var done appserver.TurnCompletedNotification
				if err := json.Unmarshal(note.Params, &done); err != nil {
					t.Fatalf("%s: decode turn/completed: %v", label, err)
				}
				if done.ThreadID == threadID {
					return
				}
			case appserver.NotificationTurnError:
				var terr appserver.TurnErrorNotification
				_ = json.Unmarshal(note.Params, &terr)
				if terr.ThreadID == threadID {
					t.Fatalf("%s: turn error: %s", label, terr.Error)
				}
			}
		}
	}
}

func containsHint(hints []string, want string) bool {
	for _, h := range hints {
		if h == want {
			return true
		}
	}
	return false
}
