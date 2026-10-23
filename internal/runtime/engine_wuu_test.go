package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
)

func TestProtocolEngineRebuildUsesSettingsWithoutLaunchingAgents(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The test binary is executable but not an agent. Registration must only
	// inspect executable availability, never perform a protocol handshake.
	for _, entry := range enginecatalog.Entries() {
		if entry.Protocol == "acp" || entry.Protocol == "opencode" {
			t.Setenv("WUU_"+strings.ToUpper(entry.ID)+"_BINARY", binary)
		}
	}
	s := &Session{RootDir: t.TempDir(), engines: agentengine.NewRegistry()}
	s.RebuildProtocolEngines(nil)
	for _, entry := range enginecatalog.Entries() {
		if (entry.Protocol == "acp" || entry.Protocol == "opencode") && !s.EngineAvailable(agentengine.EngineID(entry.ID)) {
			t.Fatalf("detected engine %s unavailable", entry.ID)
		}
	}
	disabled := false
	cfg := &config.EnginesConfig{
		Cursor:   &config.EngineBinaryConfig{Enabled: &disabled},
		OpenCode: &config.EngineBinaryConfig{BinaryPath: filepath.Join(t.TempDir(), "missing-agent")},
	}
	s.RebuildProtocolEngines(cfg)
	if s.EngineAvailable("cursor") || s.EngineAvailable("opencode") || !s.EngineAvailable("hermes") {
		t.Fatal("rebuild did not respect disable/path override or changed an unrelated engine")
	}
	sess := s.EngineSessionForThread(context.Background(), &ThreadRuntime{EngineID: "cursor"}, agentengine.ThreadBinding{})
	if _, err := sess.RunTurn(context.Background(), agentengine.TurnInput{}, nil); !errors.Is(err, agentengine.ErrUnknownEngine) {
		t.Fatalf("disabled thread must fail rather than fall back: %v", err)
	}
	s.RebuildProtocolEngines(nil)
	if !s.EngineAvailable("cursor") || !s.EngineAvailable("opencode") {
		t.Fatal("restoring auto settings did not restore detection")
	}
}

func TestWuuEngineSessionInterrupt(t *testing.T) {
	sess := &wuuEngineSession{}
	if err := sess.Interrupt(context.Background(), "stop"); !errors.Is(err, agentengine.ErrNoActiveTurn) {
		t.Fatalf("Interrupt without a turn = %v, want ErrNoActiveTurn", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess.mu.Lock()
	sess.cancel = cancel
	sess.mu.Unlock()
	if err := sess.Interrupt(context.Background(), "stop"); err != nil {
		t.Fatalf("Interrupt with an active turn: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("Interrupt must cancel the active turn context")
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineSessionForThreadUnavailable(t *testing.T) {
	s := &Session{engines: agentengine.NewRegistry()}
	rt := &ThreadRuntime{
		StreamRunner: &agent.StreamRunner{LoopDriver: loopdriver.DefaultDriver{}},
		EngineID:     agentengine.EngineWuu,
	}
	sess := s.EngineSessionForThread(context.Background(), rt, agentengine.ThreadBinding{})
	if sess == nil {
		t.Fatal("unavailable engine must still resolve to an error session")
	}
	if _, err := sess.RunTurn(context.Background(), agentengine.TurnInput{}, nil); !errors.Is(err, agentengine.ErrUnknownEngine) {
		t.Fatalf("unregistered wuu engine did not fail closed: %v", err)
	}
	if s.WuuEngine() != nil {
		t.Fatal("unregistered wuu engine was reconstructed")
	}
	if (&Session{}).EngineAvailable(agentengine.EngineWuu) {
		t.Fatal("absent registry reported a running engine")
	}
	rt.EngineID = agentengine.EngineID("claude")
	sess = s.EngineSessionForThread(context.Background(), rt, agentengine.ThreadBinding{})
	if sess == nil {
		t.Fatal("unavailable engine must still resolve to an error session")
	}
	if _, err := sess.RunTurn(context.Background(), agentengine.TurnInput{}, nil); !errors.Is(err, agentengine.ErrUnknownEngine) {
		t.Fatalf("RunTurn on unavailable engine = %v, want ErrUnknownEngine", err)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close on unavailable session: %v", err)
	}
	// A codex-bound runtime without a registered factory also fails closed.
	rt.EngineID = agentengine.EngineID("codex")
	sess = s.EngineSessionForThread(context.Background(), rt, agentengine.ThreadBinding{})
	if sess == nil {
		t.Fatal("unregistered engine must still resolve to an error session")
	}
	if _, err := sess.RunTurn(context.Background(), agentengine.TurnInput{}, nil); !errors.Is(err, agentengine.ErrUnknownEngine) {
		t.Fatalf("RunTurn on unregistered engine = %v, want ErrUnknownEngine", err)
	}
}

func TestSelectedEngineSurvivesUnavailableStartupAndRefresh(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	disabled := false
	off := &config.EngineBinaryConfig{Enabled: &disabled}
	cfg := config.Config{
		DefaultProvider: "fixture",
		Providers:       map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", Model: "fixture", BaseURL: "https://example.invalid/v1", APIKey: "fixture"}},
		Engines:         &config.EnginesConfig{DefaultEngine: "codex", Codex: off, Claude: off, Cursor: off, Devin: off, DSH: off, Grok: off, Hermes: off, Pi: off, OpenCode: off, Antigravity: off},
	}
	modules := []string{}
	cfg.Composition = &composition.Config{Version: composition.Version, Modules: &modules}
	root, home := t.TempDir(), t.TempDir()
	s, err := NewSession(Options{RootDir: root, HomeDir: home, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.Cleanup(); err != nil {
			t.Error(err)
		}
	})
	if s.DefaultEngine() != "codex" || s.EngineAvailable("codex") {
		t.Fatalf("startup changed unavailable selection: default=%q available=%v", s.DefaultEngine(), s.EngineAvailable("codex"))
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigPath, s.ConfigLoadMode = filepath.Join(root, "settings.json"), ConfigLoadFile
	if err := os.WriteFile(s.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshEngineSettings(); err != nil {
		t.Fatal(err)
	}
	if s.DefaultEngine() != "codex" || s.EngineAvailable("codex") {
		t.Fatalf("refresh changed unavailable selection: default=%q available=%v", s.DefaultEngine(), s.EngineAvailable("codex"))
	}
}

func TestEngineSessionForThreadUsesRegisteredExternalFactory(t *testing.T) {
	for _, id := range []agentengine.EngineID{"claude", agentengine.EngineWuu} {
		t.Run(string(id), func(t *testing.T) {
			registry := agentengine.NewRegistry()
			factory := &testThreadBoundEngine{id: id}
			if err := registry.Register(factory); err != nil {
				t.Fatalf("register external engine: %v", err)
			}
			s := &Session{engines: registry}
			rt := &ThreadRuntime{EngineID: factory.id}
			binding := agentengine.ThreadBinding{ThreadID: "thread-1", RootDir: "/workspace"}

			sess := s.EngineSessionForThread(context.Background(), rt, binding)
			if sess != factory.session {
				t.Fatalf("resolved session = %T, want registered external session", sess)
			}
			if factory.binding.ThreadID != binding.ThreadID || factory.binding.RootDir != binding.RootDir {
				t.Fatalf("binding = %+v, want %+v", factory.binding, binding)
			}
		})
	}
}

type testThreadBoundEngine struct {
	id      agentengine.EngineID
	binding agentengine.ThreadBinding
	session *testEngineSession
}

func (e *testThreadBoundEngine) Descriptor(context.Context) (agentengine.Descriptor, error) {
	return agentengine.Descriptor{ID: e.id, Version: "test"}, nil
}

func (e *testThreadBoundEngine) Open(context.Context, agentengine.OpenRequest) (agentengine.Session, error) {
	return nil, errors.New("unexpected Open")
}

func (e *testThreadBoundEngine) Resume(context.Context, agentengine.ResumeRequest) (agentengine.Session, error) {
	return nil, errors.New("unexpected Resume")
}

func (e *testThreadBoundEngine) SessionForThread(_ context.Context, binding agentengine.ThreadBinding) (agentengine.Session, error) {
	e.binding = binding
	if e.session == nil {
		e.session = &testEngineSession{}
	}
	return e.session, nil
}

type testEngineSession struct{}

func (*testEngineSession) RunTurn(context.Context, agentengine.TurnInput, agentengine.EventSink) (agentengine.TurnResult, error) {
	return agentengine.TurnResult{}, nil
}

func (*testEngineSession) Interrupt(context.Context, string) error { return nil }
func (*testEngineSession) Close(context.Context) error             { return nil }
