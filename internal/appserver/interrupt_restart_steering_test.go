package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// interruptRestartFixtureEngine mimics an external engine that cannot accept
// input inside a running turn but resumes its native session for the next one.
type interruptRestartFixtureEngine struct {
	session *interruptRestartFixtureSession
}

func (e *interruptRestartFixtureEngine) Descriptor(context.Context) (agentengine.Descriptor, error) {
	requiresHostModel := false
	return agentengine.Descriptor{
		ID:                "interrupt-fixture",
		Version:           "1",
		RequiresHostModel: &requiresHostModel,
		Capabilities:      []string{"interrupt-restart-steering"},
	}, nil
}

func (e *interruptRestartFixtureEngine) Open(context.Context, agentengine.OpenRequest) (agentengine.Session, error) {
	return e.session, nil
}

func (e *interruptRestartFixtureEngine) Resume(context.Context, agentengine.ResumeRequest) (agentengine.Session, error) {
	return e.session, nil
}

func (e *interruptRestartFixtureEngine) SessionForThread(context.Context, agentengine.ThreadBinding) (agentengine.Session, error) {
	return e.session, nil
}

type interruptRestartFixtureSession struct {
	mu        sync.Mutex
	prompts   []string
	calls     int
	entered   chan struct{}
	enterOnce sync.Once
}

func (s *interruptRestartFixtureSession) RunTurn(ctx context.Context, input agentengine.TurnInput, sink agentengine.EventSink) (agentengine.TurnResult, error) {
	prompt := ""
	for i := len(input.History) - 1; i >= 0; i-- {
		if input.History[i].Role == "user" {
			prompt = input.History[i].Content
			break
		}
	}
	s.mu.Lock()
	s.prompts = append(s.prompts, prompt)
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		s.enterOnce.Do(func() { close(s.entered) })
		<-ctx.Done()
		return agentengine.TurnResult{}, ctx.Err()
	}
	message := providers.ChatMessage{Role: "assistant", Content: "done"}
	if sink != nil {
		sink(providers.StreamEvent{Type: providers.EventMessage, Message: &message})
	}
	return agentengine.TurnResult{Result: agent.LoopResult{
		Content:      "done",
		NewMessages:  []providers.ChatMessage{message},
		FinishReason: providers.FinishReasonStop,
		StopReason:   "stop",
	}}, nil
}

func (s *interruptRestartFixtureSession) Interrupt(context.Context, string) error { return nil }
func (s *interruptRestartFixtureSession) Close(context.Context) error             { return nil }

// interruptFixtureEngineProvider exposes the fixture engine through the same
// declared engine.* service boundary production providers use, so embedded
// registries can still answer descriptor calls.
type interruptFixtureEngineProvider struct{ embeddedEngineMetadata }

func (*interruptFixtureEngineProvider) ID() string { return "fixture.interrupt-engine" }
func (c *interruptFixtureEngineProvider) Status() pluginhost.Status {
	status := c.embeddedModelConsumer.Status()
	status.ID = c.ID()
	return status
}
func (c *interruptFixtureEngineProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	services := c.embeddedEngineMetadata.ProvidedServices()
	for i := range services {
		services[i].Name = "engine.interrupt-fixture"
	}
	return services
}

func TestServerSteerInterruptsAndResumesExternalTurn(t *testing.T) {
	client := &fakeClient{}
	repo := t.TempDir()
	rt := &runtime.Session{
		ProviderName:   "fake-provider",
		Model:          "fake-model",
		RootDir:        repo,
		ConfigPath:     filepath.Join(repo, ".wuu.json"),
		ConfigLoadMode: runtime.ConfigLoadFile,
		SessionDir:     filepath.Join(repo, "sessions"),
		HookDispatcher: hooks.NewDispatcher(nil),
		StreamRunner: &agent.StreamRunner{
			LoopDriver:   agent.DefaultDriver{},
			Client:       providers.AdaptStreamClient(client),
			Model:        "fake-model",
			SystemPrompt: "system",
			Tools:        fastToolExecutor{},
		},
	}
	fixture := &interruptRestartFixtureSession{entered: make(chan struct{})}
	fixtureEngine := &interruptRestartFixtureEngine{session: fixture}
	registerTestWuuEngine(t, rt, &interruptFixtureEngineProvider{embeddedEngineMetadata: embeddedEngineMetadata{factory: fixtureEngine}})
	registry := agentengine.NewRegistry()
	if err := registry.Register(rt.WuuEngine()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fixtureEngine); err != nil {
		t.Fatal(err)
	}
	rt.SetEnginesForTest(registry)
	journal, err := rt.Sessions.NewInferenceJournalRuntime("interrupt-restart-test")
	if err != nil {
		t.Fatalf("NewInferenceJournalRuntime: %v", err)
	}
	rt.InferenceJournalRuntime = journal
	defer journal.Close()

	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
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
	threadID := remarshal[ThreadStartResult](t, send("thread", "thread/start", map[string]any{"engine": "interrupt-fixture"})["result"]).Thread.ID
	started := remarshal[TurnStartResult](t, send("start", "turn/start", map[string]any{"thread_id": threadID, "prompt": "first"})["result"])
	select {
	case <-fixture.entered:
	case <-time.After(10 * time.Second):
		t.Fatalf("fixture turn did not start: %s", out.String())
	}
	steer := send("steer", "turn/steer", map[string]any{
		"thread_id":        threadID,
		"expected_turn_id": started.Turn.ID,
		"prompt":           "redirect-now",
		"client_id":        "steer-1",
	})
	if got := remarshal[TurnSteerResult](t, steer["result"]).Delivery; got != "interrupt_restart" {
		t.Fatalf("steer delivery = %q, want interrupt_restart", got)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		fixture.mu.Lock()
		calls := fixture.calls
		fixture.mu.Unlock()
		if calls == 2 && turnCompletedCountForThread(t, out, threadID) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resumed turn did not complete: %s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(out.String(), `"status":"interrupted"`) {
		t.Fatalf("first turn was not recorded as interrupted: %s", out.String())
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.prompts) != 2 || fixture.prompts[0] != "first" || fixture.prompts[1] != "redirect-now" {
		t.Fatalf("engine prompts = %v", fixture.prompts)
	}
	th := srv.thread(threadID)
	th.mu.Lock()
	defer th.mu.Unlock()
	var userTexts []string
	for _, msg := range th.History {
		if msg.Role == "user" && !msg.Hidden {
			userTexts = append(userTexts, msg.Content)
		}
	}
	if len(userTexts) != 2 || userTexts[0] != "first" || userTexts[1] != "redirect-now" {
		t.Fatalf("history user messages = %v", userTexts)
	}
	if len(th.pendingSteers) != 0 {
		t.Fatalf("steer input was not consumed: %+v", th.pendingSteers)
	}
}
