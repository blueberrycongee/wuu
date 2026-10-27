package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// The selected proxy implements optional methods for every engine. That must
// not give its native steering RPC ownership of the host loop's pending input.
func TestSelectedHostLoopSteeringHasOneOwner(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var mu sync.Mutex
	var requests []string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Content any `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		raw, _ := json.Marshal(input.Messages)
		mu.Lock()
		requests = append(requests, string(raw))
		n := len(requests)
		mu.Unlock()
		if n == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	}))
	defer endpoint.Close()
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: endpoint.URL + "/v1", APIKey: "synthetic", Model: "fixture"}}
	catalog := []pluginpkg.Plugin{}
	rt, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	defer unblock()
	send := func(id, method string, params any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("%s: %v", method, response["error"])
		}
		return response
	}
	threadID := remarshal[ThreadStartResult](t, send("thread", "thread/start", map[string]any{})["result"]).Thread.ID
	if _, err := rt.Sessions.UpdateTitle(threadID, "steering fixture"); err != nil {
		t.Fatal(err)
	}
	started := remarshal[TurnStartResult](t, send("start", "turn/start", map[string]any{"thread_id": threadID, "prompt": "first"})["result"])
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("model not entered")
	}
	send("steer", "turn/steer", map[string]any{"thread_id": threadID, "expected_turn_id": started.Turn.ID, "prompt": "followup-exactly-once", "client_id": "ordinary-enter"})
	// Keep the physical request blocked while the competing RPC dispatcher has
	// time to wake. The host loop can consume this only at its next safe boundary.
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(out.String(), "does not support active-turn steering") {
		t.Fatalf("host input incorrectly dispatched to native RPC: %s", out.String())
	}
	unblock()
	deadline := time.Now().Add(20 * time.Second)
	for turnCompletedCountForThread(t, out, threadID) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if turnCompletedCountForThread(t, out, threadID) != 2 {
		t.Fatalf("host steering did not complete exactly two turns: %s", out.String())
	}
	th := srv.thread(threadID)
	th.mu.Lock()
	count := 0
	for _, msg := range th.History {
		if msg.ClientID == "ordinary-enter" {
			count++
		}
	}
	th.mu.Unlock()
	if count != 1 {
		t.Fatalf("canonical client ID count = %d", count)
	}
	mu.Lock()
	defer mu.Unlock()
	consumed := 0
	for _, req := range requests {
		consumed += strings.Count(req, "followup-exactly-once")
	}
	if consumed != 1 {
		t.Fatalf("model received followup %d times: %v", consumed, requests)
	}
	if strings.Contains(out.String(), `"delivery_unconfirmed":true`) {
		t.Fatalf("confirmed host input became uncertain: %s", out.String())
	}
}

// Native-protocol steering keeps its durable claim until positive acceptance or
// positive rejection. An ambiguous transport failure never becomes runnable.
func TestNativeSteeringDurableDeliveryEvidence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		uncertain bool
	}{
		{"accepted", nil, false},
		{"unsupported", agentengine.ErrSteerUnsupported, false},
		{"rejected", agentengine.ErrSteerRejected, false},
		{"transport-unknown", errors.New("connection lost after submit"), true},
		{"unsupported-text-is-not-proof", errors.New(agentengine.ErrSteerUnsupported.Error()), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			out := &lockedBuffer{}
			srv := New(rt, out)
			defer srv.Close()
			if err := srv.handleLine(context.Background(), []byte(`{"id":"new","method":"thread/start"}`)); err != nil {
				t.Fatal(err)
			}
			id := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "new")["result"]).Thread.ID
			th := srv.thread(id)
			message := providers.ChatMessage{Role: "user", Content: "follow up", ClientID: "native-input", Steered: true}
			if _, err := srv.appendHeldUserTurns(th.state, id, []queuedTurn{{id: message.ClientID, msg: message, origin: session.HeldUserWorkOriginSteer}}); err != nil {
				t.Fatal(err)
			}
			th.mu.Lock()
			th.currentTurn = "active"
			th.pendingSteers = []providers.ChatMessage{message}
			th.mu.Unlock()
			native := &steeringReceiptFixture{err: tc.err, called: make(chan struct{})}
			join := srv.startNativeSteering(context.Background(), th, "active", native)
			select {
			case <-native.called:
			case <-time.After(3 * time.Second):
				t.Fatal("native steering was not dispatched")
			}
			join()
			held, err := srv.loadHeldUserTurns(th.state, id)
			if err != nil {
				t.Fatal(err)
			}
			if tc.err == nil {
				if len(held) != 0 {
					t.Fatalf("accepted input retained: %+v", held)
				}
				th.mu.Lock()
				history := append([]providers.ChatMessage(nil), th.History...)
				th.mu.Unlock()
				count := 0
				for _, m := range history {
					if m.ClientID == message.ClientID && m.EngineTurnID == "native-turn" {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("accepted native history count=%d", count)
				}
			} else if len(held) != 1 || held[0].deliveryUnconfirmed != tc.uncertain {
				t.Fatalf("held evidence=%+v want uncertain=%t", held, tc.uncertain)
			}
			th.mu.Lock()
			pending := len(th.pendingSteers)
			unsupported := th.nativeSteerUnsupported
			th.currentTurn = ""
			th.mu.Unlock()
			if tc.name == "unsupported" {
				if !unsupported || pending != 1 {
					t.Fatalf("confirmed unsupported lost queued fallback: %t %d", unsupported, pending)
				}
			} else if pending != 0 {
				t.Fatalf("input remained automatically runnable: %d", pending)
			}
		})
	}
}

type steeringReceiptFixture struct {
	err    error
	called chan struct{}
}

func (s *steeringReceiptFixture) Steer(context.Context, providers.ChatMessage) (string, error) {
	close(s.called)
	if s.err != nil {
		return "", s.err
	}
	return "native-turn", nil
}
