package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/claudeengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/externalengine"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
)

// WuuEngine is the built-in agent engine: the native StreamRunner loop that
// has always driven Wuu conversations. It implements agentengine.Factory so
// the app-server talks to it through the same seam it will use for external
// engines (Claude, Codex).
type WuuEngine struct {
	session *Session
}

// NewWuuEngine constructs the native engine provider for explicit registration.
func NewWuuEngine(session *Session) *WuuEngine {
	return &WuuEngine{session: session}
}

// Descriptor describes the built-in engine.
func (e *WuuEngine) Descriptor(context.Context) (agentengine.Descriptor, error) {
	return agentengine.Descriptor{
		ID:      agentengine.EngineWuu,
		Version: "1",
		Capabilities: []string{
			"native-tool-loop",
			"native-session-resume",
			"canonical-history", "host-managed-continuation", "host-managed-steering", "active-turn-steering",
		},
	}, nil
}

// Open creates a fresh thread runtime owned by the returned engine session.
// Close drains its turn and workers before releasing the provider generation.
func (e *WuuEngine) Open(ctx context.Context, req agentengine.OpenRequest) (agentengine.Session, error) {
	engine, _, err := e.OpenWithRuntime(ctx, req)
	return engine, err
}

// OpenWithRuntime exposes the owned native runtime for host-side configuration
// and observation. The returned engine alone owns its cleanup; callers must
// finish using the runtime before Close and must not release its generation.
func (e *WuuEngine) OpenWithRuntime(ctx context.Context, req agentengine.OpenRequest) (agentengine.Session, *ThreadRuntime, error) {
	if e == nil || e.session == nil {
		return nil, nil, errors.New("wuu engine is not bound to a runtime session")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	threadID := req.ThreadID
	if threadID == "" {
		threadID = req.SessionID
	}
	rootDir := req.RootDir
	if rootDir == "" {
		rootDir = e.session.RootDir
	}
	rt, err := e.session.NewThreadRuntimeForRoot(threadID, rootDir)
	if err != nil {
		return nil, nil, err
	}
	rt.EngineID = agentengine.EngineWuu
	return &wuuEngineSession{runner: rt.StreamRunner, owner: e.session, ownedRuntime: rt}, rt, nil
}

// Resume reopens an existing conversation with the same resource ownership
// as Open. Wuu restores context from canonical history.
func (e *WuuEngine) Resume(ctx context.Context, req agentengine.ResumeRequest) (agentengine.Session, error) {
	return e.Open(ctx, req.OpenRequest)
}

// SessionForThread binds the built-in engine to an existing thread runtime.
// The app-server keeps a cache of live runtimes and reuses them across turns;
// this is the live-path equivalent of Open.
func (e *WuuEngine) SessionForThread(rt *ThreadRuntime) agentengine.Session {
	if rt == nil || rt.StreamRunner == nil {
		return nil
	}
	return &wuuEngineSession{runner: rt.StreamRunner}
}

// EngineAvailable reports whether an engine id resolves in the registry.
// thread creation validates its engine binding against this; the static
// agentengine.CheckEngine only knows the built-in wuu engine.
func (s *Session) EngineAvailable(id agentengine.EngineID) bool {
	if s == nil || s.engines == nil {
		return false
	}
	_, ok := s.engines.Lookup(id)
	return ok
}

// Engines exposes the engine registry for settings-driven listing and
// enable/disable.
func (s *Session) Engines() *agentengine.Registry {
	if s == nil {
		return nil
	}
	return s.engines
}

// SetEnginesForTest installs a registry on runtimes constructed without NewSession.
func (s *Session) SetEnginesForTest(reg *agentengine.Registry) {
	if s == nil {
		return
	}
	s.engines = reg
}

// DefaultEngine returns the settings default for new threads. Reading the
// snapshot never waits for binary discovery or external engine registration.
func (s *Session) DefaultEngine() agentengine.EngineID {
	s.defaultEngineMu.RLock()
	defer s.defaultEngineMu.RUnlock()
	return s.defaultEngine
}

// RefreshEngineSettings reapplies persisted choices after binary discovery.
// Serialize the entire config snapshot/application with explicit settings edits
// so an older background refresh cannot undo a newer disable or binary change.
func (s *Session) RefreshEngineSettings() error {
	s.engineSettingsMu.Lock()
	defer s.engineSettingsMu.Unlock()
	return s.refreshEngineSettings()
}

// UpdateEngineSettings persists and applies an explicit settings edit without
// allowing a concurrent discovery refresh to publish an older snapshot.
func (s *Session) UpdateEngineSettings(update config.EnginesSettingsUpdate) error {
	s.engineSettingsMu.Lock()
	defer s.engineSettingsMu.Unlock()
	services, release, err := s.RetainStateServices()
	if err != nil {
		return err
	}
	defer release()
	view, err := services.Config.Read(context.Background())
	if err != nil {
		return err
	}
	source := view.Source
	if len(source) == 0 {
		source, err = json.Marshal(view.Config)
		if err != nil {
			return errors.New("selected configuration cannot be edited")
		}
	}
	next, err := config.UpdateEnginesSettingsSource(source, update)
	if err != nil {
		return err
	}
	view, err = services.Config.CompareExchange(context.Background(), view.Revision, next)
	if err != nil {
		return err
	}
	if view.ActivationError != "" {
		return errors.New("engine settings were saved, but the selected configuration could not be activated")
	}
	return s.applyEngineSettings(view.Config)
}

func (s *Session) refreshEngineSettings() error {
	cfg, _, err := s.LoadEffectiveConfig()
	if err != nil {
		return err
	}
	return s.applyEngineSettings(cfg)
}

func (s *Session) applyEngineSettings(cfg config.Config) error {
	if s.engines != nil && s.engines.IsResolved() {
		id := agentengine.EngineWuu
		if cfg.Engines != nil {
			id = agentengine.NormalizeEngineID(cfg.Engines.DefaultEngine)
		}
		s.defaultEngineMu.Lock()
		s.defaultEngine = id
		s.defaultEngineMu.Unlock()
		// Native and external providers read their selected configuration on binding.
		return nil
	}

	codexBinary, codexResolveErr := codexengine.ResolveBinary()
	codexEnabled := codexResolveErr == nil
	claudeBinary, claudeResolveErr := claudeengine.ResolveBinary()
	claudeEnabled := claudeResolveErr == nil
	defaultEngine := agentengine.EngineWuu
	if engineCfg := cfg.Engines; engineCfg != nil {
		if trimmed := strings.TrimSpace(engineCfg.DefaultEngine); trimmed != "" {
			defaultEngine = agentengine.NormalizeEngineID(trimmed)
		}
		if codexCfg := engineCfg.Codex; codexCfg != nil {
			enabled, explicit := codexCfg.EngineEnabled()
			if explicit {
				codexEnabled = enabled
			}
			if path := strings.TrimSpace(codexCfg.BinaryPath); path != "" {
				codexBinary = path
				if !explicit {
					codexEnabled = true
				}
			}
		}
		if claudeCfg := engineCfg.Claude; claudeCfg != nil {
			enabled, explicit := claudeCfg.EngineEnabled()
			if explicit {
				claudeEnabled = enabled
			}
			if path := strings.TrimSpace(claudeCfg.BinaryPath); path != "" {
				claudeBinary = path
				if !explicit {
					claudeEnabled = true
				}
			}
		}
	}
	s.RebuildCodexEngine(codexEnabled, codexBinary)
	s.RebuildClaudeEngine(claudeEnabled, claudeBinary)
	s.RebuildProtocolEngines(cfg.Engines)
	s.defaultEngineMu.Lock()
	s.defaultEngine = defaultEngine
	s.defaultEngineMu.Unlock()
	return nil
}

// RebuildCodexEngine re-registers the codex engine after a settings change
// (binary path override or enable/disable). Existing turn leases keep their
// old host alive; future bindings use the newly registered host.
func (s *Session) RebuildCodexEngine(enabled bool, binaryPath string) {
	if s == nil || s.engines == nil {
		return
	}
	if !enabled {
		s.engines.Unregister(agentengine.EngineID("codex"))
		return
	}
	host := codexengine.NewHost(binaryPath, s.RootDir)
	_ = s.engines.Register(codexengine.NewEngine(host))
}

// RebuildClaudeEngine re-registers the claude engine after a settings
// change (binary path override or enable/disable). The engine is
// stateless; only the resolved binary path matters.
func (s *Session) RebuildClaudeEngine(enabled bool, binaryPath string) {
	if s == nil || s.engines == nil {
		return
	}
	if !enabled {
		s.engines.Unregister(agentengine.EngineID("claude"))
		return
	}
	if err := s.engines.Register(claudeengine.NewEngine(binaryPath, s.RootDir)); err != nil {
		return
	}
}

// RebuildProtocolEngines applies settings to ACP and HTTP engines without
// starting agents. Running sessions retain their binding until the turn ends.
func (s *Session) RebuildProtocolEngines(cfg *config.EnginesConfig) {
	if s == nil || s.engines == nil {
		return
	}
	for _, entry := range enginecatalog.Entries() {
		if entry.Protocol != "acp" && entry.Protocol != "opencode" {
			continue
		}
		setting := cfg.Binary(entry.ID)
		if enabled, _ := setting.EngineEnabled(); !enabled {
			s.engines.Unregister(agentengine.EngineID(entry.ID))
			continue
		}
		override := ""
		if setting != nil {
			override = setting.BinaryPath
		}
		binary, err := entry.Resolve(override)
		if err != nil {
			s.engines.Unregister(agentengine.EngineID(entry.ID))
			continue
		}
		_ = s.engines.Register(externalengine.New(entry, binary, s.RootDir))
	}
}

// CodexHost returns the currently registered codex engine host, if any.
func (s *Session) CodexHost() *codexengine.Host {
	if s == nil || s.engines == nil {
		return nil
	}
	factory, _ := s.engines.Lookup(agentengine.EngineID("codex"))
	engine, ok := factory.(*codexengine.Engine)
	if !ok {
		return nil
	}
	return engine.Host()
}

// WuuEngine returns the registered native engine provider, or nil when it is
// absent or replaced. Lookup never constructs a substitute implementation.
func (s *Session) WuuEngine() agentengine.Factory {
	if s != nil && s.engines != nil {
		if f, ok := s.engines.Lookup(agentengine.EngineWuu); ok {
			return f
		}
	}
	return nil
}

// EngineSessionForThread returns the engine session bound to an existing
// thread runtime. The thread's persisted engine selects the implementation;
// a thread bound to an engine this build cannot host gets a session that
// fails every turn with an explicit error (history stays readable).
func (s *Session) EngineSessionForThread(ctx context.Context, rt *ThreadRuntime, binding agentengine.ThreadBinding) agentengine.Session {
	if rt == nil {
		return nil
	}
	id := agentengine.NormalizeEngineID(string(rt.EngineID))
	if s == nil || s.engines == nil {
		return &unavailableEngineSession{id: id}
	}
	if s.engines.IsResolved() {
		session, err := s.bindSelectedEngine(ctx, rt, binding, id)
		if err != nil {
			return agentengine.FailedSession(err)
		}
		return session
	}
	f, ok := s.engines.Lookup(id)
	if !ok {
		return &unavailableEngineSession{id: id}
	}
	if native, ok := f.(*WuuEngine); ok {
		return native.SessionForThread(rt)
	}
	tbf, ok := f.(agentengine.ThreadBoundFactory)
	if !ok {
		return &unavailableEngineSession{id: id}
	}
	sess, err := tbf.SessionForThread(ctx, binding)
	if err != nil {
		return agentengine.FailedSession(err)
	}
	if sess == nil {
		return &unavailableEngineSession{id: id}
	}
	return sess
}

// wuuEngineSession owns Open/Resume resources and borrows cached thread
// runtimes. Both paths stop admission and drain their active turn on Close.
type wuuEngineSession struct {
	mu           sync.Mutex
	runner       *agent.StreamRunner
	owner        *Session
	ownedRuntime *ThreadRuntime
	cancel       context.CancelFunc
	turnDone     chan struct{}
	closeDone    chan struct{}
	closeErr     error
}

func (w *wuuEngineSession) RunTurn(ctx context.Context, input agentengine.TurnInput, sink agentengine.EventSink) (agentengine.TurnResult, error) {
	if w == nil || w.runner == nil {
		return agentengine.TurnResult{}, errors.New("wuu engine session has no runner")
	}
	w.mu.Lock()
	if w.closeDone != nil {
		w.mu.Unlock()
		return agentengine.TurnResult{}, errors.New("wuu engine session is closed")
	}
	if w.turnDone != nil {
		w.mu.Unlock()
		return agentengine.TurnResult{}, errors.New("wuu engine session already has an active turn")
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	w.cancel, w.turnDone = cancel, done
	w.mu.Unlock()
	defer func() {
		cancel()
		w.mu.Lock()
		w.cancel, w.turnDone = nil, nil
		close(done)
		w.mu.Unlock()
	}()
	res, err := w.runner.RunWithCallback(ctx, input.History, agent.StreamCallback(sink))
	// Only a sole post-run cleanup failure can leave the reply successful.
	// Do not use errors.As: a joined execution or workflow failure must retain
	// its failed outcome even when receipt cleanup also failed.
	if receiptErr, ok := err.(*agent.ModelReceiptCompletionError); ok && res.DriverStatus == string(loopdriver.TerminalSucceeded) {
		res.ReceiptDiagnostic = receiptErr.Error()
		err = nil
	}
	return agentengine.TurnResult{Result: res}, err
}

func (w *wuuEngineSession) Interrupt(_ context.Context, _ string) error {
	if w == nil {
		return errors.New("wuu engine session is nil")
	}
	w.mu.Lock()
	cancel := w.cancel
	w.mu.Unlock()
	if cancel == nil {
		return agentengine.ErrNoActiveTurn
	}
	cancel()
	return nil
}

// Close stops admission and cancels the active turn. Caller cancellation ends
// the wait, while cleanup retains resources until all owned consumers finish.
func (w *wuuEngineSession) Close(ctx context.Context) error {
	w.mu.Lock()
	if w.closeDone == nil {
		w.closeDone = make(chan struct{})
		cancel, turnDone := w.cancel, w.turnDone
		go func() {
			if cancel != nil {
				cancel()
			}
			if turnDone != nil {
				<-turnDone
			}
			if rt := w.ownedRuntime; rt != nil {
				if control := rt.AgentControl; control != nil {
					control.BeginShutdown()
					control.StopAll()
					control.YieldWorkerTerminalFinalizations()
					// Physical execution cleanup outlives the Close caller's
					// deadline; provider leases cannot expire underneath it.
					_ = control.WaitOwnedWorkerExecutions(context.Background())
					w.closeErr = shutdownAndCleanupAgentControl(control)
				}
				w.owner.ReleasePluginGeneration(rt.PluginGeneration)
				rt.PluginGeneration = nil
			}
			close(w.closeDone)
		}()
	}
	done := w.closeDone
	w.mu.Unlock()
	select {
	case <-done:
		return w.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// EngineUnavailableSession returns an engine session that fails every turn
// with an explicit error naming the engine. It is the last-resort session the
// app-server uses when no engine implementation can be resolved at all, so a
// turn terminates with a clear error instead of panicking.
func EngineUnavailableSession(id agentengine.EngineID) agentengine.Session {
	return &unavailableEngineSession{id: id}
}

// unavailableEngineSession fails turns for threads bound to an engine this
// build cannot host. The failure is explicit so the UI can explain it instead
// of silently running the native loop.
type unavailableEngineSession struct {
	id agentengine.EngineID
}

func (u *unavailableEngineSession) RunTurn(context.Context, agentengine.TurnInput, agentengine.EventSink) (agentengine.TurnResult, error) {
	if u == nil {
		return agentengine.TurnResult{}, errors.New("engine session is nil")
	}
	return agentengine.TurnResult{}, fmt.Errorf("%w: %q is not available in this build", agentengine.ErrUnknownEngine, u.id)
}

func (u *unavailableEngineSession) Interrupt(context.Context, string) error {
	return agentengine.ErrNoActiveTurn
}

func (u *unavailableEngineSession) Close(context.Context) error {
	return nil
}
