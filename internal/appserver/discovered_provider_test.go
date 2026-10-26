package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/runtime"
)

func setupDiscoveredGrokProvider(t *testing.T) *runtime.Session {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{})
	writeTwoProviderSelectionConfig(t, rt.ConfigPath)
	grokHome := t.TempDir()
	t.Setenv("GROK_HOME", grokHome)
	if err := os.WriteFile(filepath.Join(grokHome, "auth.json"), []byte(`{"https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828":{"key":"local-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return rt
}

func TestConversationSelectsDiscoveredProviderWithoutChangingDefaults(t *testing.T) {
	for _, entry := range []string{"existing conversation", "new conversation"} {
		t.Run(entry, func(t *testing.T) {
			rt := setupDiscoveredGrokProvider(t)
			bindConfigSourceForTest(t, rt)
			before, _, err := rt.LoadEffectiveConfig()
			if err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			srv := New(rt, out)
			defer srv.Close()
			start := `{"id":"start","method":"thread/start","params":{}}`
			if entry == "new conversation" {
				start = `{"id":"start","method":"thread/start","params":{"provider":"grok-build","model":"grok-4.6","effort":"xhigh"}}`
			}
			if err := srv.handleLine(context.Background(), []byte(start)); err != nil {
				t.Fatal(err)
			}
			response := responseByID(t, parseOutput(t, out.String()), "start")
			if response["error"] != nil {
				t.Fatalf("start response = %+v", response)
			}
			thread := remarshal[ThreadStartResult](t, response["result"]).Thread
			if entry == "existing conversation" {
				request := fmt.Sprintf(`{"id":"select","method":"config/model/update","params":{"thread_id":%q,"provider":"grok-build","model":"grok-4.6","variant":"xhigh"}}`, thread.ID)
				if err := srv.handleLine(context.Background(), []byte(request)); err != nil {
					t.Fatal(err)
				}
				response = responseByID(t, parseOutput(t, out.String()), "select")
				if response["error"] != nil {
					t.Fatalf("select response = %+v", response)
				}
			}
			built, err := srv.ensureThreadRuntime(srv.thread(thread.ID))
			if err != nil {
				t.Fatalf("build selected runtime: %v", err)
			}
			if built.StreamRunner.ProviderName != "grok-build" || built.StreamRunner.Model != "grok-4.6" || built.StreamRunner.Variant != "xhigh" {
				t.Fatalf("selected runtime used a different model: %+v", built.Selection)
			}
			saved, ok, err := rt.Sessions.Find(thread.ID)
			if err != nil || !ok || saved.Provider != "grok-build" || saved.Model != "grok-4.6" {
				t.Fatalf("selected session = %+v, found=%v, err=%v", saved, ok, err)
			}
			after, _, err := rt.LoadEffectiveConfig()
			if err != nil {
				t.Fatal(err)
			}
			if provider := after.Providers["grok-build"]; !provider.ReuseGrokCredentials || provider.WireAPI != "chat" {
				t.Fatalf("discovered connection was not registered: %+v", provider)
			}
			delete(after.Providers, "grok-build")
			if !reflect.DeepEqual(before, after) || rt.ProviderName != "fake-provider" || rt.Model != "fake-model" {
				t.Fatal("conversation selection changed workspace configuration or runtime defaults")
			}
			// Rebuilding from the saved pin must not depend on the picker having
			// discovered the provider in this server process.
			restarted := New(rt, &lockedBuffer{})
			defer restarted.Close()
			resume := fmt.Sprintf(`{"id":"resume","method":"thread/resume","params":{"session_id":%q}}`, thread.ID)
			if err := restarted.handleLine(context.Background(), []byte(resume)); err != nil {
				t.Fatal(err)
			}
			rebuilt, err := restarted.ensureThreadRuntime(restarted.thread(thread.ID))
			if err != nil {
				t.Fatalf("rebuild selected runtime: %v", err)
			}
			if rebuilt.StreamRunner.ProviderName != "grok-build" || rebuilt.StreamRunner.Model != "grok-4.6" {
				t.Fatal("resuming the conversation lost its selected model")
			}
		})
	}
}

func TestConversationRejectsUndiscoveredProvider(t *testing.T) {
	rt := setupDiscoveredGrokProvider(t)
	t.Setenv("GROK_HOME", t.TempDir())
	bindConfigSourceForTest(t, rt)
	before, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "start")
	thread := remarshal[ThreadStartResult](t, response["result"]).Thread
	params, err := json.Marshal(ConfigModelUpdateParams{ThreadID: thread.ID, Provider: "grok-build", Model: "grok-4.6"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleConfigModelUpdate(Request{ID: json.RawMessage(`"select"`), Params: params}); err != nil {
		t.Fatal(err)
	}
	response = responseByID(t, parseOutput(t, out.String()), "select")
	if response["error"] == nil {
		t.Fatal("selected an unavailable provider")
	}
	after, err := os.ReadFile(rt.ConfigPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("unavailable provider changed configuration: %v", err)
	}
}

// A next-step intent may choose only an already configured connection. Picker
// discovery must not write config before the retained runtime rejects it.
func TestNextStepSelectionDoesNotRegisterDiscoveredConnection(t *testing.T) {
	rt := setupDiscoveredGrokProvider(t)
	bindConfigSourceForTest(t, rt)
	before, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "start")
	threadID := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
	th := srv.thread(threadID)
	active, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	previous, _, err := rt.Sessions.Find(threadID)
	if err != nil {
		t.Fatal(err)
	}
	th.mu.Lock()
	th.running = true
	th.mu.Unlock()
	defer func() { th.mu.Lock(); th.running = false; th.mu.Unlock() }()
	params, _ := json.Marshal(ConfigModelUpdateParams{ThreadID: threadID, Provider: "grok-build", Model: "grok-4.6", ApplyAt: "next_step"})
	if err := srv.handleConfigModelUpdate(Request{ID: json.RawMessage(`"select"`), Params: params}); err != nil {
		t.Fatal(err)
	}
	response = responseByID(t, parseOutput(t, out.String()), "select")
	if response["error"] == nil {
		t.Error("next-step request accepted an unconfigured connection")
	}
	after, err := os.ReadFile(rt.ConfigPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("next-step selection registered a connection before rejecting: %v", err)
	}
	saved, _, err := rt.Sessions.Find(threadID)
	if err != nil || saved.ModelSelectionRevision != previous.ModelSelectionRevision || saved.Provider != previous.Provider {
		t.Fatalf("rejected connection changed durable selection: %+v %v", saved, err)
	}
	th.mu.Lock()
	same := th.execRuntime == active
	th.mu.Unlock()
	if !same {
		t.Fatal("rejected connection detached the active runtime")
	}
}
