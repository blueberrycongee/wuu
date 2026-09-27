package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
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
		StreamRunner: &agent.StreamRunner{},
		EngineID:     agentengine.EngineWuu,
	}
	sess := s.EngineSessionForThread(context.Background(), rt, agentengine.ThreadBinding{})
	if sess == nil {
		t.Fatal("wuu thread must resolve to an engine session")
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

func TestEngineSessionForThreadUsesRegisteredExternalFactory(t *testing.T) {
	registry := agentengine.NewRegistry()
	factory := &testThreadBoundEngine{id: agentengine.EngineID("claude")}
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
