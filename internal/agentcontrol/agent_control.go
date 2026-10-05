// Package agentcontrol wires the orchestration tools (spawn_agent,
// send_message, close_agent) to the underlying subagent and worktree
// subsystems.
//
// AgentControl is the shared control plane for one root agent tree. It
// owns the SubAgent Manager, Worktree Manager, thread registry, and
// event store, and exposes the API the toolkit uses to implement the
// orchestration tools.
package agentcontrol

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crypto/rand"
	"encoding/hex"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

// WorkerToolkitFactory builds a fresh ToolExecutor for a worker thread.
// The metadata argument contains the worker's canonical agent path, so
// orchestration tools inside that worker can resolve relative child paths.
type WorkerToolkitFactory func(rootDir string, wt WorkerType, meta agentthread.Metadata) (agent.ToolExecutor, error)

// WorkerSystemPromptFactory builds the base prompt for a registered worker.
// AgentControl still wraps it with the worker role and working-directory
// instructions.
type WorkerSystemPromptFactory func(rootDir string, wt WorkerType, meta agentthread.Metadata, isolation IsolationMode) (string, error)

// ParticipantStore persists participant identities used by restored runs. It is
// defined here (instead of importing internal/session) so agentcontrol
// stays decoupled from the session storage layer.
type ParticipantStore interface {
	Upsert(participant.Participant) error
}

// AgentControl owns the orchestration runtime for one wuu session.
type AgentControl struct {
	manager       *subagent.Manager
	workspaceMu   sync.RWMutex
	worktrees     *worktree.Manager // nil when workspace is not a git repo
	parentRepo    string            // absolute path to workspace root
	worktreeRoot  string            // workspace-state worktrees directory
	sessionID     string
	historyDir    string
	threadDir     string
	threads       *agentthread.Registry
	threadStore   *agentthread.Store
	harnessDir    string
	harnessStore  *harness.Store
	failureSink   FailureSink
	reportSink    ReportSink
	rootThreadID  string
	rootThreadDir string
	workerFact    WorkerToolkitFactory
	workerPrompt  WorkerSystemPromptFactory
	defaultSys    string           // base system prompt prefix added to every worker
	participants  ParticipantStore // optional; nil disables participant persistence
	maxParallel   int
	// treeFrozen gates nested-result wakes between turn/interrupt's
	// FreezeWorkerTree and the next user turn's ResolveFrozenWorkerTree.
	treeFrozen        atomic.Bool
	shutdownMu        sync.RWMutex
	stopping          bool
	spawnSlotMu       sync.Mutex
	spawnReservations map[*spawnSlotReservation]struct{}
	queueStartMu      sync.Mutex
	queueStarted      bool
	queueStopped      bool
	queueDrainMu      sync.Mutex
	queueMu           sync.Mutex
	queued            []preparedSpawn
	queueRetryMu      sync.Mutex
	queueRetrying     bool
	statusCh          chan subagent.Notification
	statusStop        chan struct{}
	statusDone        chan struct{}
	closeOnce         sync.Once

	workerTransitionMu                    sync.Mutex
	workerTransitions                     map[string]*sync.Mutex
	workerLeaseMu                         sync.Mutex
	workerLeases                          map[string]*workerExecutionLease
	workerTerminalFinalizerMu             sync.Mutex
	workerTerminalFinalizers              map[uint64]*workerTerminalFinalizer
	nextWorkerTerminalFinalizerID         uint64
	workerTerminalRecoveryOnce            sync.Once
	workerTerminalRecoveryMu              sync.Mutex
	workerTerminalRecovering              map[string]struct{}
	workerTerminalRecoveryWG              sync.WaitGroup
	workerTerminalYieldOnce               sync.Once
	workerTerminalYield                   chan struct{}
	workerReleaseHookMu                   sync.Mutex
	beforeWorkerTerminalTransitionForTest func(string)
	beforeWorkerTerminalRecoveryForTest   func(string)
	beforeWorkerExecutionReleaseForTest   func(string)
	beforeQueuedManagerSpawnForTest       func(string)
	afterReportClosingFollowupForTest     func(string)
	beforeNestedResultFollowupForTest     func(string) error
	nestedResultDeliveryWaitForTest       func(string)
	queuedSpawnAckHookMu                  sync.Mutex
	queuedSpawnAckForTest                 func(string) error
	queuedSpawnMarkFailureForTest         func(string) error
	queuedLaunchAckMu                     sync.Mutex
	queuedLaunchAcks                      map[string]struct{}

	resultDeliveriesMu sync.Mutex
	resultDeliveries   map[string]agentResultDelivery
	nestedDeliveryMu   sync.Mutex
	nestedDeliveries   map[string]*nestedResultDeliveryAttempt

	// reportNudgeMu guards reportNudged: run IDs that already received the
	// single mechanical agent_report closing turn, so a requires_report
	// worker is never nudged twice in one process lifetime.
	reportNudgeMu sync.Mutex
	reportNudged  map[string]struct{}

	// reportSettleMu guards reportUnsettled: requires_report runs STARTED BY
	// THIS PROCESS whose completion adjudication has not been recorded yet.
	// The manager flips a run's snapshot to completed before the (async)
	// notification consumer decides between "structured report already
	// filed", "start the one closing-turn nudge", and "synthesize a
	// final_text report" — so a parent's await polling raw snapshots could
	// slip through that window and walk away with a report-less result while
	// the closing turn is still being launched. Marked before manager.Spawn
	// (so no completion can be observed unmarked) and cleared by the consumer
	// once the terminal notification is durably recorded. Deliberately not
	// persisted: rehydrated or dormant runs from earlier processes must never
	// wait on a consumer that is not coming.
	reportSettleMu  sync.Mutex
	reportUnsettled map[string]struct{}

	participantBindingMu sync.Mutex
	participantBindings  map[string]string

	// workerProviderName is the provider name the AgentControl's worker
	// runtime is currently configured for. The model-pin resolver
	// (installed via SetModelPinClientResolver) uses it to decide
	// whether a queued spawn's pin targets the same provider (no fresh
	// client needed) or a different one (resolver MUST yield a fresh
	// client or fail).
	workerProviderNameMu sync.Mutex
	workerProviderName   string

	// modelPinResolver rebuilds the stream client for a queued spawn
	// whose raw pin targets a provider different from the worker
	// default. nil means no resolver is installed; in that case a
	// cross-provider pin restored from disk fails the spawn
	// explicitly instead of silently falling back to the default
	// client (which would route the request to the wrong provider).
	modelPinResolverMu sync.Mutex
	modelPinResolver   ModelPinClientResolver

	// modelAliasResolver resolves a configured spawn_agent.model alias into a
	// complete worker runtime. It is installed by appserver because only the
	// appserver layer holds the runtime config and provider factory. nil means
	// every alias is treated as unknown and falls back to the current worker
	// default (not an error).
	modelAliasResolverMu sync.Mutex
	modelAliasResolver   ModelAliasResolver

	// providerClientResolver rebuilds a stream client for a persisted provider
	// name. Used on cross-restart resume to recreate the client for a snapshot
	// that was started with an aliased runtime, without re-resolving the alias.
	providerClientResolverMu sync.Mutex
	providerClientResolver   ProviderClientResolver
}

// ModelPinClientResolver rebuilds the (model, client) pair for a queued
// spawn whose raw participant pin survived a process restart. The
// resolver owns the policy: bare-model / same-provider pins return
// (model, nil, nil), cross-provider pins return (model, freshClient, nil),
// and any error fails the queued spawn explicitly. Appserver
// (internal/appserver) is the typical owner of this callback because
// it already holds the runtime config + provider factory used to build
// the worker client.
type ModelPinClientResolver func(rawPin string) (modelOverride string, clientOverride providers.StreamClient, err error)

// AliasResolutionResult is returned by the model-alias resolver installed by
// appserver. Unknown aliases are not errors; callers fall back to the current
// worker default and record the fallback for diagnosis. Err is set only when a
// configured alias cannot be turned into a runtime (e.g. provider client
// build failed); that failure must fail the spawn rather than silently falling
// back to the default runtime.
type AliasResolutionResult struct {
	Found        bool
	Unknown      bool
	Err          error
	Runtime      subagent.WorkerRuntime
	ValidAliases []string
}

// ModelAliasResolver resolves a requested spawn_agent.model alias into a
// complete immutable worker runtime. Appserver installs this callback via
// AgentControl.SetModelAliasResolver.
type ModelAliasResolver func(alias string) AliasResolutionResult

// ProviderClientResolver rebuilds a stream client for a persisted provider
// name. Used on cross-restart resume to recreate the client for an aliased
// runtime snapshot, without re-resolving the alias.
type ProviderClientResolver func(providerName string) (providers.StreamClient, error)

// Config holds the dependencies needed to build an AgentControl.
type Config struct {
	// Client is the streaming LLM client every worker spawned by this
	// agent control runtime will share. It must be a StreamClient (not just a
	// Client) so workers run through the same streaming transport as
	// the interactive main agent.
	Client providers.StreamClient
	// ProviderName names the provider Client belongs to. It is stamped on
	// worker runners so worker-produced native state carries its provider
	// of origin, and it seeds the worker-provider identity used by
	// model-pin comparisons.
	ProviderName                   string
	DefaultModel                   string
	DefaultEffort                  string
	DefaultOptions                 map[string]any
	DefaultContextWindow           int
	DefaultMaxInputTokens          int
	DefaultOutputReserveTokens     int
	DefaultCompactThresholdTokens  int
	DefaultTemperature             float64
	DefaultCompactThresholdPct     float64
	DefaultCompactKeepRecentTokens int
	DefaultDisableAutoCompact      bool
	ParentRepo                     string // absolute path to the user's workspace
	WorktreeRoot                   string // workspace-state worktrees directory (only used when workspace is a git repo)
	HistoryDir                     string // session artifact workers directory
	ThreadDir                      string // session artifact threads directory
	HarnessDir                     string // session artifact harness directory
	FailureSink                    FailureSink
	ReportSink                     ReportSink
	SessionID                      string
	WorkerSysPrompt                string
	WorkerFactory                  WorkerToolkitFactory
	WorkerPrompt                   WorkerSystemPromptFactory
	// WorkerWakeAuthority reapplies the current thread authority to a
	// dormant worker's tool executor when a follow-up wakes it. Waking is an
	// execution admission: the woken turn must run under the permissions in
	// force now, not the ones captured at spawn. Nil keeps spawn-time
	// authority. Running workers keep their admitted snapshot either way.
	WorkerWakeAuthority func(agent.ToolExecutor)
	// ParticipantStore, when set, persists the ephemeral participant
	// identity created for each spawned worker. Optional: when nil,
	// participant IDs are still generated in-memory but not persisted.
	ParticipantStore  ParticipantStore
	MaxParallel       int
	InferenceJournal  providers.InferenceJournal
	ToolLedgerFactory func(ownerID string) (*toolledger.Ledger, error)
	OnSubagentStart   func(context.Context, string) error
	OnSubagentStop    func(context.Context, string) error
}

// New constructs an AgentControl. Worktree isolation is only available
// when the workspace is a git repository; inplace spawns and forks
// work regardless.
func New(cfg Config) (*AgentControl, error) {
	if cfg.Client == nil {
		return nil, errors.New("Client required")
	}
	if cfg.WorkerFactory == nil {
		return nil, errors.New("WorkerFactory required")
	}

	// Worktree manager is optional — only created when the workspace
	// is a git repo. Non-git workspaces can still spawn inplace
	// workers and fork agents; only isolation=worktree is unavailable.
	var wt *worktree.Manager
	if worktree.IsGitRepo(cfg.ParentRepo) {
		var err error
		wt, err = worktree.NewManager(cfg.ParentRepo, cfg.WorktreeRoot)
		if err != nil {
			return nil, fmt.Errorf("worktree manager: %w", err)
		}
	}

	mgr := subagent.NewManagerWithOptions(cfg.Client, cfg.DefaultModel, subagent.ManagerOptions{
		DefaultProviderName:     cfg.ProviderName,
		DefaultEffort:           cfg.DefaultEffort,
		DefaultProviderOptions:  cfg.DefaultOptions,
		ContextWindowOverride:   cfg.DefaultContextWindow,
		MaxInputTokens:          cfg.DefaultMaxInputTokens,
		OutputReserveTokens:     cfg.DefaultOutputReserveTokens,
		CompactThresholdTokens:  cfg.DefaultCompactThresholdTokens,
		Temperature:             cfg.DefaultTemperature,
		CompactThresholdPct:     cfg.DefaultCompactThresholdPct,
		CompactKeepRecentTokens: cfg.DefaultCompactKeepRecentTokens,
		DisableAutoCompact:      cfg.DefaultDisableAutoCompact,
		InferenceJournal:        cfg.InferenceJournal,
		ToolLedgerFactory:       cfg.ToolLedgerFactory,
		OnSubagentStart:         cfg.OnSubagentStart,
		OnSubagentStop:          cfg.OnSubagentStop,
	})
	threadRegistry := agentthread.NewRegistry()

	maxP := cfg.MaxParallel
	if maxP <= 0 {
		maxP = 5
	}
	harnessDir := strings.TrimSpace(cfg.HarnessDir)
	if harnessDir == "" && strings.TrimSpace(cfg.ThreadDir) != "" {
		harnessDir = filepath.Join(filepath.Dir(cfg.ThreadDir), "harness")
	}
	c := &AgentControl{
		manager:                  mgr,
		workerProviderName:       strings.TrimSpace(cfg.ProviderName),
		worktrees:                wt,
		parentRepo:               cfg.ParentRepo,
		worktreeRoot:             cfg.WorktreeRoot,
		sessionID:                cfg.SessionID,
		historyDir:               cfg.HistoryDir,
		threadDir:                cfg.ThreadDir,
		threads:                  threadRegistry,
		threadStore:              agentthread.NewStore(cfg.ThreadDir),
		harnessDir:               harnessDir,
		harnessStore:             harness.NewStore(harnessDir),
		failureSink:              cfg.FailureSink,
		reportSink:               cfg.ReportSink,
		workerFact:               cfg.WorkerFactory,
		workerPrompt:             cfg.WorkerPrompt,
		defaultSys:               cfg.WorkerSysPrompt,
		participants:             cfg.ParticipantStore,
		maxParallel:              maxP,
		workerTransitions:        make(map[string]*sync.Mutex),
		workerLeases:             make(map[string]*workerExecutionLease),
		workerTerminalFinalizers: make(map[uint64]*workerTerminalFinalizer),
		workerTerminalYield:      make(chan struct{}),
	}
	mgr.SetTerminalPrepareObserver(c.prepareWorkerTerminal)
	mgr.SetTerminalObserver(c.consumeWorkerTerminal)
	mgr.SetWakeAuthority(cfg.WorkerWakeAuthority)
	c.restoreAgentResultDeliveries()
	c.registerRootThread()
	if err := c.restoreQueuedSpawns(); err != nil {
		return nil, fmt.Errorf("restore queued spawns: %w", err)
	}
	statusCh := make(chan subagent.Notification, 64)
	mgr.Subscribe(statusCh)
	c.statusCh = statusCh
	c.statusStop = make(chan struct{})
	c.statusDone = make(chan struct{})
	go func() {
		defer close(c.statusDone)
		c.consumeWorkerStatus(statusCh)
	}()
	c.reconcileOrphanedHarnessTasks()
	return c, nil
}

// PrepareWorkspaceRebind builds any fallible workspace-specific state before
// the caller persists a session move. The returned commit function only swaps
// prepared in-memory state, so a successful durable update cannot leave the
// current turn on the old workspace.
func (c *AgentControl) PrepareWorkspaceRebind(parentRepo string) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	parentRepo = strings.TrimSpace(parentRepo)
	if parentRepo == "" {
		return nil, errors.New("parent repository is required")
	}
	abs, err := filepath.Abs(parentRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve parent repository: %w", err)
	}
	abs = filepath.Clean(abs)

	var manager *worktree.Manager
	if worktree.IsGitRepo(abs) {
		manager, err = worktree.NewManager(abs, c.worktreeRoot)
		if err != nil {
			return nil, fmt.Errorf("prepare worktree manager: %w", err)
		}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			c.workspaceMu.Lock()
			c.parentRepo = abs
			c.worktrees = manager
			c.workspaceMu.Unlock()
			c.updateRootThreadWorkspace(abs)
		})
	}, nil
}

func (c *AgentControl) workspaceSnapshot() (string, *worktree.Manager) {
	if c == nil {
		return "", nil
	}
	c.workspaceMu.RLock()
	defer c.workspaceMu.RUnlock()
	return c.parentRepo, c.worktrees
}

// ParentRepo returns the workspace used by future inplace workers and forks.
func (c *AgentControl) ParentRepo() string {
	parentRepo, _ := c.workspaceSnapshot()
	return parentRepo
}

func (c *AgentControl) updateRootThreadWorkspace(root string) {
	if c == nil || c.threads == nil || c.threadStore == nil {
		return
	}
	sessionID := strings.TrimSpace(c.sessionID)
	if sessionID == "" || sessionID == "session-pending" {
		return
	}
	model := ""
	if existing, ok := c.threads.Resolve(sessionID); ok {
		model = existing.Model
	}
	meta := c.threads.RegisterRoot(sessionID, sessionID, root, model, time.Now().UTC())
	_ = c.threadStore.UpsertThread(meta)
}

// Manager exposes the underlying subagent.Manager for advanced use
// (Subscribe, etc.).
func (c *AgentControl) Manager() *subagent.Manager {
	return c.manager
}

// UpdateWorkerDefaults changes the runtime defaults used by future worker
// spawns. Running workers keep their existing runners.
func (c *AgentControl) UpdateWorkerDefaults(client providers.StreamClient, defaultModel string, opts subagent.ManagerOptions) {
	if c == nil || c.manager == nil {
		return
	}
	if client != nil {
		// Keep the pin-comparison identity in lockstep with the new default
		// client so a pin naming the old provider is treated as
		// cross-provider (fresh client) rather than silently routed to the
		// new default.
		c.SetWorkerProviderName(opts.DefaultProviderName)
	}
	c.manager.UpdateDefaults(client, defaultModel, opts)
}

// Close stops AgentControl-owned background consumers. It does not cancel
// running workers; callers should StopAll first when they need cancellation.
func (c *AgentControl) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.queueStartMu.Lock()
		c.queueStopped = true
		c.queueStartMu.Unlock()
		if c.manager != nil && c.statusCh != nil {
			c.manager.Unsubscribe(c.statusCh)
		}
		if c.statusStop != nil {
			close(c.statusStop)
		}
		if c.statusDone != nil {
			<-c.statusDone
		}
	})
}

var errAgentControlStopping = errors.New("agent control is shutting down")

// BeginShutdown closes admission for every path that can start or extend a
// worker turn. It synchronizes with the final Manager.Spawn/Followup call, so a
// subsequent StopAll observes every turn admitted before shutdown and no turn
// can appear behind it.
func (c *AgentControl) BeginShutdown() {
	if c == nil {
		return
	}
	c.shutdownMu.Lock()
	c.stopping = true
	c.shutdownMu.Unlock()
	c.queueStartMu.Lock()
	c.queueStopped = true
	c.queueStartMu.Unlock()
}

func (c *AgentControl) isStopping() bool {
	if c == nil {
		return true
	}
	c.shutdownMu.RLock()
	stopping := c.stopping
	c.shutdownMu.RUnlock()
	return stopping
}

func (c *AgentControl) beginWorkerTurn() (func(), error) {
	if c == nil {
		return nil, errAgentControlStopping
	}
	c.shutdownMu.RLock()
	if c.stopping {
		c.shutdownMu.RUnlock()
		return nil, errAgentControlStopping
	}
	return c.shutdownMu.RUnlock, nil
}

// StartQueuedWork enables durable queue drains after the runtime has installed
// every dependency restored workers need, including model resolvers and
// reliable terminal finalizers. It is idempotent; New deliberately leaves the
// queue dormant so constructor callers cannot observe an incompletely wired
// worker.
func (c *AgentControl) StartQueuedWork() {
	if c == nil {
		return
	}
	if c.isStopping() {
		return
	}
	c.queueStartMu.Lock()
	if c.queueStarted || c.queueStopped {
		c.queueStartMu.Unlock()
		return
	}
	c.queueStarted = true
	c.queueStartMu.Unlock()
	go func() {
		c.replayPendingNestedAgentCompletions()
		c.maybeStartQueued(context.Background())
	}()
}

func (c *AgentControl) queuedWorkEnabled() bool {
	if c == nil || c.isStopping() {
		return false
	}
	c.queueStartMu.Lock()
	defer c.queueStartMu.Unlock()
	return c.queueStarted && !c.queueStopped
}

// SetWorkerExecutionReleaseHookForTest installs a synchronization hook that
// runs after terminal state is durable and immediately before the worker's
// cross-process execution lease is released.
func (c *AgentControl) SetWorkerExecutionReleaseHookForTest(hook func(string)) {
	if c == nil {
		return
	}
	c.workerReleaseHookMu.Lock()
	c.beforeWorkerExecutionReleaseForTest = hook
	c.workerReleaseHookMu.Unlock()
}

// SetWorkerTerminalTransitionHookForTest installs a synchronization hook at
// the start of reliable terminal consumption. Tests use it to hold a terminal
// generation across shutdown without weakening production ordering.
func (c *AgentControl) SetWorkerTerminalTransitionHookForTest(hook func(string)) {
	if c == nil {
		return
	}
	c.workerReleaseHookMu.Lock()
	c.beforeWorkerTerminalTransitionForTest = hook
	c.workerReleaseHookMu.Unlock()
}

// SetReportClosingFollowupHookForTest installs a synchronization hook after
// the requires_report closing turn is admitted but before the originating
// completion returns its explicit continued outcome.
func (c *AgentControl) SetReportClosingFollowupHookForTest(hook func(string)) {
	if c == nil {
		return
	}
	c.workerReleaseHookMu.Lock()
	c.afterReportClosingFollowupForTest = hook
	c.workerReleaseHookMu.Unlock()
}

// SetQueuedManagerSpawnHookForTest installs a synchronization hook immediately
// before a claimed queued launch commits to Manager.Spawn.
func (c *AgentControl) SetQueuedManagerSpawnHookForTest(hook func(string)) {
	if c == nil {
		return
	}
	c.workerReleaseHookMu.Lock()
	c.beforeQueuedManagerSpawnForTest = hook
	c.workerReleaseHookMu.Unlock()
}

// HarnessStore exposes the durable task graph store for tests and UI adapters.
func (c *AgentControl) HarnessStore() *harness.Store {
	if c == nil {
		return nil
	}
	return c.harnessStore
}

// Threads exposes the in-memory thread registry. Tests use it to assert
// thread state directly without round-tripping through the persisted store.
func (c *AgentControl) Threads() *agentthread.Registry {
	if c == nil {
		return nil
	}
	return c.threads
}

// SetSessionInfo updates the coordinator's session ID and history dir after the
// session runtime has assigned them, then restores durable work from the bound
// harness directory. Safe to call once at startup, before queue draining or
// terminal recovery begins.
func (c *AgentControl) SetSessionInfo(sessionID, historyDir string, threadDir ...string) error {
	if c == nil {
		return nil
	}
	c.sessionID = sessionID
	c.historyDir = historyDir
	if len(threadDir) > 0 && strings.TrimSpace(threadDir[0]) != "" {
		c.threadDir = strings.TrimSpace(threadDir[0])
		c.threadStore = agentthread.NewStore(c.threadDir)
	} else if strings.TrimSpace(historyDir) != "" {
		c.threadDir = filepath.Join(filepath.Dir(historyDir), "threads")
		c.threadStore = agentthread.NewStore(c.threadDir)
	}
	c.restoreAgentResultDeliveries()
	if c.threadDir != "" {
		c.setHarnessDir(filepath.Join(filepath.Dir(c.threadDir), "harness"))
	}
	// The legacy/root control is constructed before a concrete session ID is
	// available, so its initial harness path is empty. Restore only after the
	// real artifact directory is bound; otherwise CLI callers silently miss
	// queued launches persisted by an earlier process.
	if err := c.restoreQueuedSpawns(); err != nil {
		return fmt.Errorf("restore queued spawns: %w", err)
	}
	c.registerRootThread()
	// The harness dir may only become known here; reconcile the durable
	// task graph against the (possibly empty) set of live executors so
	// crash-orphaned tasks stop reporting running forever.
	c.reconcileOrphanedHarnessTasks()
	return nil
}

func (c *AgentControl) setHarnessDir(dir string) {
	c.harnessDir = strings.TrimSpace(dir)
	c.harnessStore = harness.NewStore(c.harnessDir)
}

// SessionID returns the bound session ID, or "session-pending" if
// SetSessionInfo hasn't been called yet.
func (c *AgentControl) SessionID() string {
	return c.sessionID
}

// SetWorkerProviderName records the name of the provider the
// AgentControl's worker runtime is currently configured for. The
// model-pin resolver (installed via SetModelPinClientResolver) consults
// it to decide whether a queued spawn's pin targets the same provider
// or a different one. Passing an empty string clears the binding.
func (c *AgentControl) SetWorkerProviderName(name string) {
	if c == nil {
		return
	}
	c.workerProviderNameMu.Lock()
	defer c.workerProviderNameMu.Unlock()
	c.workerProviderName = strings.TrimSpace(name)
}

// WorkerProviderName returns the provider name installed via
// SetWorkerProviderName, or "" when none is set.
func (c *AgentControl) WorkerProviderName() string {
	if c == nil {
		return ""
	}
	c.workerProviderNameMu.Lock()
	defer c.workerProviderNameMu.Unlock()
	return c.workerProviderName
}

// SetModelPinClientResolver installs the callback that rebuilds the
// stream client for a queued spawn whose raw pin targets a different
// provider. Appserver (internal/appserver) owns the resolver because it
// holds the runtime config + provider factory used to build the worker
// client. Passing nil removes any previously installed resolver; in
// that case a queued spawn restored with a cross-provider pin fails
// explicitly instead of silently using the worker default client.
//
// The resolver signature:
//   - bare-model / same-provider pin → (model, nil, nil)
//   - cross-provider pin with a working provider → (model, freshClient, nil)
//   - any error → fail the queued spawn with that error visible on the
//     thread + harness task (never silently fall back).
func (c *AgentControl) SetModelPinClientResolver(resolver ModelPinClientResolver) {
	if c == nil {
		return
	}
	c.modelPinResolverMu.Lock()
	defer c.modelPinResolverMu.Unlock()
	c.modelPinResolver = resolver
}

func (c *AgentControl) currentModelPinResolver() ModelPinClientResolver {
	if c == nil {
		return nil
	}
	c.modelPinResolverMu.Lock()
	defer c.modelPinResolverMu.Unlock()
	return c.modelPinResolver
}

// SetModelAliasResolver installs the callback that resolves a configured
// spawn_agent.model alias into a complete immutable worker runtime.
// Appserver is the typical owner because it holds the effective config and
// provider factory. Passing nil means aliases are always treated as unknown
// and fall back to the current worker default; that fallback is surfaced
// in diagnostics, not an error.
func (c *AgentControl) SetModelAliasResolver(resolver ModelAliasResolver) {
	if c == nil {
		return
	}
	c.modelAliasResolverMu.Lock()
	defer c.modelAliasResolverMu.Unlock()
	c.modelAliasResolver = resolver
}

func (c *AgentControl) currentModelAliasResolver() ModelAliasResolver {
	if c == nil {
		return nil
	}
	c.modelAliasResolverMu.Lock()
	defer c.modelAliasResolverMu.Unlock()
	return c.modelAliasResolver
}

// SetProviderClientResolver installs the callback that rebuilds a stream
// client for a persisted provider name. It is used when resuming a worker
// whose snapshot contains a resolved alias runtime, so the runtime can be
// reused exactly without re-resolving the alias. Passing nil means resume
// falls back to the manager's current default client.
func (c *AgentControl) SetProviderClientResolver(resolver ProviderClientResolver) {
	if c == nil {
		return
	}
	c.providerClientResolverMu.Lock()
	defer c.providerClientResolverMu.Unlock()
	c.providerClientResolver = resolver
}

func (c *AgentControl) currentProviderClientResolver() ProviderClientResolver {
	if c == nil {
		return nil
	}
	c.providerClientResolverMu.Lock()
	defer c.providerClientResolverMu.Unlock()
	return c.providerClientResolver
}

// pinProviderName returns the provider part of a raw participant pin
// ("p:model" → "p"). A bare model pin has no provider part and yields "".
// Mirrors appserver.parseParticipantModelPin so agentcontrol does not
// depend on the appserver package.
func pinProviderName(rawPin string) string {
	value := strings.TrimSpace(rawPin)
	idx := strings.Index(value, ":")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(value[:idx])
}

// pinTargetsDifferentProvider reports whether the raw participant pin
// (e.g. "alt:model") names a provider that is not the worker's current
// default provider. A bare model (no colon) and a same-provider pin
// both return false.
func pinTargetsDifferentProvider(rawPin, workerProvider string) bool {
	pinProvider := pinProviderName(rawPin)
	if pinProvider == "" {
		return false
	}
	workerProvider = strings.TrimSpace(workerProvider)
	if workerProvider == "" {
		return true
	}
	return pinProvider != workerProvider
}

// SpawnAdmissionRollback removes state prepared for a spawn that never became
// runnable. AgentControl wraps it so the implementation runs at most once.
type SpawnAdmissionRollback func() error

// SpawnAdmissionPrepare durably prepares caller-owned state after the final
// worker ID is allocated but before AgentControl creates any thread, harness,
// queue, worktree, lease, or manager state. The returned rollback is retained
// for queued spawns and runs if admission or launch later fails.
type SpawnAdmissionPrepare func(workerID string) (SpawnAdmissionRollback, error)

// SpawnRequest is the internal shape of a spawn_agent tool invocation
// after argument validation.
type SpawnRequest struct {
	Type          string
	TaskName      string
	ParticipantID string
	AgentProfile  string // optional durable memory profile to wake for this worker
	Description   string
	Prompt        string
	ParentID      string
	ParentPath    string
	BaseRepo      string // optional: chain off another worktree (worktree mode only)
	Synchronous   bool
	Timeout       time.Duration
	// WaitInterrupt lets a synchronous caller stop waiting without canceling
	// the spawned worker. It is used by turn steer to background foreground
	// subagent work while the parent continues.
	WaitInterrupt <-chan struct{}
	// Isolation overrides the worker type's DefaultIsolation when set.
	// Empty string means "use the type default". Use this from
	// spawn_agent to opt a normally-inplace worker into a worktree
	// (e.g. an explorer that needs to run a destructive script).
	Isolation string
	// ModelOverride and ClientOverride are internal-only spawn hooks used
	// when a per-participant model pin diverges from the runtime
	// worker default. ModelOverride replaces the model string for this
	// single run; ClientOverride, when non-nil, replaces the stream
	// client the runner would otherwise inherit from the worker
	// defaults. Both fields are set together when a named participant
	// pins a different provider, and ModelOverride alone when it pins
	// a model on the worker's current provider. The LLM-facing
	// spawn_agent tool MUST NOT expose either field.
	ModelOverride  string
	ClientOverride providers.StreamClient
	// ModelPin is the raw participant pin (e.g. "alt-provider:model" or
	// "bare-model"). It is persisted with queued spawns so the restore
	// path can rebuild ClientOverride via the registered
	// ModelPinClientResolver when the pin targets a different
	// provider. Empty pin means the spawn honors only ModelOverride
	// (or the worker default when ModelOverride is also empty). Like
	// ModelOverride and ClientOverride, this field is internal-only
	// and must not be exposed through the LLM-facing spawn_agent
	// tool.
	ModelPin string
	// ModelAlias is the configured alias requested by the caller (e.g.
	// "cheap" or "frontend"). It is resolved at admission into a
	// complete WorkerRuntime via the registered ModelAliasResolver.
	// An empty or unknown alias falls back to the current worker
	// default and records the fallback for diagnostics. The raw alias
	// string is persisted with queued spawns so it can be re-resolved
	// when the queued item actually launches.
	ModelAlias string
	// FileScopeRoots is an internal-only per-spawn file-tool whitelist
	// override. Nil means the worker inherits the factory default scope;
	// a non-nil slice (including an empty one, which clears the whitelist)
	// is applied to the freshly built worker toolkit when it supports
	// SetFileScopeRoots. Like ModelOverride it must never be exposed
	// through the LLM-facing spawn_agent tool.
	FileScopeRoots []string
	// AdmissionPrepare is an internal-only transactional hook for state that
	// must exist before the worker can become observable or runnable. It is not
	// exposed through the LLM-facing spawn_agent tool.
	AdmissionPrepare SpawnAdmissionPrepare
}

// SpawnResult is what the spawn_agent tool returns to the model.
type SpawnResult struct {
	Action          string   `json:"action"`
	ResultID        string   `json:"result_id,omitempty"`
	AgentID         string   `json:"agent_id"`
	ParticipantID   string   `json:"-"`
	TaskName        string   `json:"task_name,omitempty"`
	AgentProfile    string   `json:"agent_profile,omitempty"`
	AgentPath       string   `json:"agent_path,omitempty"`
	Status          string   `json:"status"`
	Isolation       string   `json:"isolation"`               // "inplace" or "worktree"
	WorktreePath    string   `json:"worktree_path,omitempty"` // empty for inplace spawns
	Result          string   `json:"result,omitempty"`
	ResultPath      string   `json:"result_path,omitempty"`
	ResultBytes     int      `json:"result_bytes,omitempty"`
	ResultTruncated bool     `json:"result_truncated,omitempty"`
	Error           string   `json:"error,omitempty"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
	ResultConsumed  bool     `json:"result_consumed,omitempty"`
	ConsumedBy      string   `json:"consumed_by,omitempty"`
	Backgrounded    bool     `json:"backgrounded,omitempty"`
	NextSteps       []string `json:"next_steps,omitempty"`
	// ModelAlias records the requested configured alias, if any, for
	// provenance and diagnostics.
	ModelAlias string `json:"model_alias,omitempty"`
	// ModelAliasFallback is true when ModelAlias was non-empty but could
	// not be resolved to a configured alias. The worker ran with the
	// current worker default instead; this flag makes the fallback visible
	// without failing the spawn.
	ModelAliasFallback bool `json:"model_alias_fallback,omitempty"`
	// ResolvedProvider and ResolvedModel expose the runtime the worker
	// actually used. They are empty when the worker ran with the default
	// runtime and the alias was not resolved.
	ResolvedProvider string `json:"resolved_provider,omitempty"`
	ResolvedModel    string `json:"resolved_model,omitempty"`
	ResolvedAPIModel string `json:"resolved_api_model,omitempty"`
	// ValidAliases is populated for fallback spawns to help the caller
	// recover from a typo. It lists the currently configured alias names
	// but never includes provider credentials, endpoints, or raw config.
	ValidAliases []string `json:"valid_aliases,omitempty"`
}

type preparedSpawn struct {
	WorkerID      string
	ParticipantID string
	WorkerType    WorkerType
	ThreadMeta    agentthread.Metadata
	Description   string
	Prompt        string
	Isolation     IsolationMode
	BaseRepo      string
	BaseRevision  string
	IsFork        bool
	ForkMode      string
	ParentHistory []providers.ChatMessage
	// ModelOverride and ClientOverride carry a per-participant model
	// pin across the queue boundary so that even queued spawns honor
	// the pin once they dequeue.
	ModelOverride  string
	ClientOverride providers.StreamClient
	// ModelPin is the raw participant Model pin (e.g. "p:m" or "m").
	// It is persisted alongside ModelOverride so a queued spawn can
	// rebuild the ClientOverride on restart when the pin targets a
	// provider different from the worker default. The resolver
	// callback installed via SetModelPinClientResolver owns the
	// policy: bare-model / same-provider pins just change the model,
	// cross-provider pins MUST resolve to a fresh client — a nil
	// client or resolver error fails the spawn explicitly so a wrong
	// provider is never used.
	ModelPin string
	// ModelAlias is the configured alias requested for this spawn. It
	// is persisted so a queued spawn can re-resolve the alias at launch
	// time, including after restart recovery. The actual resolved
	// runtime is recomputed then; this field carries only the raw
	// requested alias string.
	ModelAlias string
	// AdmissionRollback is process-local and intentionally omitted from the
	// durable queue payload. Built-in durable admissions must provide their own
	// restart cleanup when a restored queued launch fails.
	AdmissionRollback SpawnAdmissionRollback
}

type queuedSpawnPayload struct {
	WorkerID      string                  `json:"worker_id"`
	ParticipantID string                  `json:"participant_id,omitempty"`
	WorkerType    string                  `json:"worker_type"`
	ThreadMeta    agentthread.Metadata    `json:"thread_meta"`
	Description   string                  `json:"description,omitempty"`
	Prompt        string                  `json:"prompt"`
	Isolation     string                  `json:"isolation"`
	BaseRepo      string                  `json:"base_repo,omitempty"`
	BaseRevision  string                  `json:"base_revision,omitempty"`
	IsFork        bool                    `json:"is_fork,omitempty"`
	ForkMode      string                  `json:"fork_mode,omitempty"`
	ParentHistory []providers.ChatMessage `json:"parent_history,omitempty"`
	// ModelOverride is persisted with the queued payload so the
	// per-participant model pin survives session restart. The
	// ClientOverride is intentionally NOT persisted — reconstructing a
	// provider client from the queue on its own is not safe, and a
	// restart that drops a per-participant pin falls back to the
	// worker default (matching the empty pin semantics).
	//
	// ModelPin is the raw participant pin (e.g. "p:m" or "m"). When
	// present, the restore path asks the registered
	// ModelPinClientResolver to rebuild the client: a cross-provider
	// pin MUST return a non-nil client or an error. A missing
	// resolver is treated as an error rather than a silent fallback,
	// because a queued spawn restored with no resolver and a
	// cross-provider pin would otherwise route to the wrong provider.
	ModelOverride string `json:"model_override,omitempty"`
	ModelPin      string `json:"model_pin,omitempty"`
	// ModelAlias is the configured alias requested for this queued spawn.
	// It is re-resolved at launch time using the current config, so only
	// the raw alias string is persisted.
	ModelAlias string `json:"model_alias,omitempty"`
}

func prepareSpawnAdmission(prepare SpawnAdmissionPrepare, workerID string) (SpawnAdmissionRollback, error) {
	if prepare == nil {
		return nil, nil
	}
	rollback, err := prepare(workerID)
	if err != nil {
		return nil, err
	}
	if rollback == nil {
		return nil, nil
	}
	var mu sync.Mutex
	done := false
	return func() error {
		mu.Lock()
		defer mu.Unlock()
		if done {
			return nil
		}
		if err := rollback(); err != nil {
			return err
		}
		done = true
		return nil
	}, nil
}

// Spawn launches a sub-agent. In synchronous mode it waits until the child
// finishes or the caller's context is cancelled; in async mode it returns
// immediately with status "running" and the agent_id the orchestrator can join
// later or receive via completion notification.
func (c *AgentControl) Spawn(ctx context.Context, req SpawnRequest) (spawnResult *SpawnResult, err error) {
	if c.isStopping() {
		return nil, errAgentControlStopping
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}

	// Resolve worker type (validates the name).
	wt, err := LookupWorkerType(req.Type)
	if err != nil {
		return nil, err
	}
	wtype := wt.Name

	workerID := newAgentControlWorkerID(wtype)
	taskName := req.TaskName
	agentProfile := strings.TrimSpace(req.AgentProfile)

	// Resolve effective isolation: caller override > type default.
	isolation, err := NormalizeIsolation(req.Isolation, wt)
	if err != nil {
		return nil, err
	}
	// BaseRepo only makes sense for chained worktree spawns.
	if isolation == IsolationInplace && strings.TrimSpace(req.BaseRepo) != "" {
		return nil, errors.New("base_repo is only supported with isolation=worktree")
	}
	// Resolve the requested model alias at admission for diagnostics. Queued
	// spawns re-resolve the alias at actual launch; direct spawns use this
	// runtime immediately. Unknown and omitted aliases use the current
	// manager defaults, but unknown aliases record ModelAliasFallback=true
	// and list valid aliases for diagnosis. The resolved runtime is
	// snapshotted at first run so the worker's runtime stays frozen across
	// followups and restart.
	aliasRuntime, aliasFallback, aliasValid, aliasErr := c.resolveModelAlias(req.ModelAlias)
	if aliasErr != nil {
		return nil, fmt.Errorf("model alias: %w", aliasErr)
	}
	var spawnRuntime *subagent.WorkerRuntime
	modelAliasFallback := false
	var modelAliasValid []string
	if aliasRuntime != nil {
		spawnRuntime = aliasRuntime
	} else if aliasFallback {
		// Unknown alias: fall back to current defaults and snapshot them so
		// the worker's runtime is frozen from its first run.
		defaults := c.manager.DefaultWorkerRuntime()
		spawnRuntime = &defaults
		modelAliasFallback = true
		modelAliasValid = aliasValid
	} else if strings.TrimSpace(req.ModelPin) == "" && strings.TrimSpace(req.ModelOverride) == "" && req.ClientOverride == nil {
		// Omitted alias with no legacy pin: snapshot current defaults so the
		// runtime is frozen from first run.
		defaults := c.manager.DefaultWorkerRuntime()
		spawnRuntime = &defaults
	}
	resolvedProvider := ""
	resolvedModel := ""
	resolvedAPIModel := ""
	if spawnRuntime != nil {
		resolvedProvider = strings.TrimSpace(spawnRuntime.Provider)
		resolvedModel = strings.TrimSpace(spawnRuntime.Model)
		resolvedAPIModel = strings.TrimSpace(spawnRuntime.APIModel)
		if resolvedAPIModel == "" {
			resolvedAPIModel = resolvedModel
		}
	}

	spawnSlot, admitted := c.tryReserveSpawnSlot(workerID)
	if !admitted && req.Synchronous {
		return nil, fmt.Errorf("max parallel sub-agents reached (%d). Wait for one to complete or use async spawn so the task can queue.", c.maxParallel)
	}
	if spawnSlot != nil {
		defer spawnSlot.releaseAndKickQueued()
	}
	queuedBaseRepo := strings.TrimSpace(req.BaseRepo)
	queuedBaseRevision := ""
	if !admitted && isolation == IsolationWorktree {
		resolved, resolveErr := c.resolveQueuedWorktreeBase(req.BaseRepo)
		if resolveErr != nil {
			return nil, resolveErr
		}
		queuedBaseRepo = resolved.Repo
		queuedBaseRevision = resolved.Revision
	}
	admissionRollback, err := prepareSpawnAdmission(req.AdmissionPrepare, workerID)
	if err != nil {
		return nil, fmt.Errorf("prepare spawn admission: %w", err)
	}
	retainAdmission := false
	defer func() {
		if !retainAdmission {
			if rollbackErr := c.rollbackSpawnAdmissionReliably(workerID, admissionRollback, c.isStopping); rollbackErr != nil {
				providers.DebugLogf("agentcontrol: abandon spawn admission rollback %s: %v", workerID, rollbackErr)
			}
		}
	}()

	if !admitted {
		releaseQueueAdmission, admissionErr := c.beginWorkerTurn()
		if admissionErr != nil {
			return nil, admissionErr
		}
		// Serialize registration and queue persistence with drains/cancellation.
		// Once the child thread becomes resolvable, StopFrom must either observe
		// its queued payload or wait until that payload is durable.
		c.queueDrainMu.Lock()
		threadMeta, err := c.registerChildThreadWithStatus(workerID, taskName, agentProfile, wtype, req.Prompt, agentthread.SourceThreadSpawn, "", req.ParentID, req.ParentPath, agentthread.StatusPending)
		if err != nil {
			c.queueDrainMu.Unlock()
			releaseQueueAdmission()
			return nil, err
		}
		participantID := strings.TrimSpace(req.ParticipantID)
		if participantID == "" {
			participantID = c.newEphemeralParticipant(threadMeta.TaskName, wt).ID
		}
		prepared := preparedSpawn{
			WorkerID:          workerID,
			ParticipantID:     participantID,
			WorkerType:        wt,
			ThreadMeta:        threadMeta,
			Description:       req.Description,
			Prompt:            req.Prompt,
			Isolation:         isolation,
			BaseRepo:          queuedBaseRepo,
			BaseRevision:      queuedBaseRevision,
			ModelOverride:     strings.TrimSpace(req.ModelOverride),
			ClientOverride:    req.ClientOverride,
			ModelPin:          strings.TrimSpace(req.ModelPin),
			ModelAlias:        strings.TrimSpace(req.ModelAlias),
			AdmissionRollback: admissionRollback,
		}
		c.recordHarnessTaskQueued(threadMeta, wtype, req.Prompt, isolation, queuedBaseRepo)
		if err := c.enqueuePreparedSpawn(prepared); err != nil {
			c.settleSpawnLaunchFailure(workerID, err)
			c.queueDrainMu.Unlock()
			releaseQueueAdmission()
			return nil, err
		}
		retainAdmission = true
		queuedResult := &SpawnResult{
			Action:             "spawn_agent",
			AgentID:            workerID,
			ParticipantID:      participantID,
			TaskName:           threadMeta.TaskName,
			AgentProfile:       threadMeta.AgentProfile,
			AgentPath:          threadMeta.Path,
			Status:             "queued",
			Isolation:          string(isolation),
			ModelAlias:         strings.TrimSpace(req.ModelAlias),
			ModelAliasFallback: modelAliasFallback,
			ResolvedProvider:   resolvedProvider,
			ResolvedModel:      resolvedModel,
			ResolvedAPIModel:   resolvedAPIModel,
			ValidAliases:       modelAliasValid,
			NextSteps:          spawnResultNextSteps("queued", false, string(isolation), threadMeta.Path),
		}
		c.queueDrainMu.Unlock()
		releaseQueueAdmission()
		go c.maybeStartQueued(context.Background())
		return queuedResult, nil
	}
	releaseDirectAdmission, admissionErr := c.beginWorkerTurn()
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer func() {
		if releaseDirectAdmission != nil {
			releaseDirectAdmission()
		}
	}()
	if _, err := c.acquireWorkerExecution(workerID); err != nil {
		return nil, err
	}
	releaseLeaseOnReturn := true
	defer func() {
		if releaseLeaseOnReturn {
			c.releaseWorkerExecution(workerID)
		}
	}()

	// 1. Determine the worker's working directory.
	//    - inplace: share the parent repo (no checkout cost)
	//    - worktree: `git worktree add --detach` based on parent HEAD
	parentRepo, worktrees := c.workspaceSnapshot()
	var (
		workerRoot  string
		worktreeRef *worktree.Worktree
	)
	if isolation == IsolationWorktree {
		if worktrees == nil {
			return nil, errors.New("isolation=worktree requires repository worktree support (this workspace does not support isolated worktrees)")
		}
		worktreeRef, err = worktrees.Create(c.sessionID, workerID, req.BaseRepo)
		if err != nil {
			return nil, fmt.Errorf("worktree create: %w", err)
		}
		workerRoot = worktreeRef.Path
	} else {
		workerRoot = parentRepo
	}

	// 2. Register the child thread before launch so the visible worker
	// ID, worktree ID, and thread path all point at the same task.
	threadMeta, err := c.registerChildThread(workerID, taskName, agentProfile, wtype, req.Prompt, agentthread.SourceThreadSpawn, "", req.ParentID, req.ParentPath)
	if err != nil {
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return nil, err
	}
	c.recordHarnessTaskStart(threadMeta, wtype, req.Prompt, workerRoot, isolation, req.BaseRepo)

	// Create the worker identity. Failure to persist never blocks the spawn.
	participantID := strings.TrimSpace(req.ParticipantID)
	if participantID == "" {
		participantID = c.newEphemeralParticipant(threadMeta.TaskName, wt).ID
	}

	// 3. Build worker's toolkit rooted at the chosen working directory.
	workerKit, err := c.workerFact(workerRoot, wt, threadMeta)
	if err != nil {
		c.settleSpawnLaunchFailure(workerID, fmt.Errorf("worker toolkit: %w", err))
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return nil, fmt.Errorf("worker toolkit: %w", err)
	}
	if req.FileScopeRoots != nil {
		if scoped, ok := workerKit.(interface{ SetFileScopeRoots([]string) }); ok {
			scoped.SetFileScopeRoots(req.FileScopeRoots)
		}
	}

	// 4. Compose system prompt: type-specific role + working dir + base prompt.
	sys, err := c.workerSystemPrompt(workerRoot, wt, threadMeta, isolation)
	if err != nil {
		c.settleSpawnLaunchFailure(workerID, fmt.Errorf("worker system prompt: %w", err))
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return nil, fmt.Errorf("worker system prompt: %w", err)
	}

	// 5. History path.
	historyPath := ""
	if c.historyDir != "" {
		historyPath = filepath.Join(c.historyDir, workerID+".json")
	}

	// 6. Spawn via manager using the ID already allocated by the
	// AgentControl. That keeps worktree paths, persisted thread
	// metadata, and visible agent IDs aligned.
	workerCtx := ctx
	if !req.Synchronous || req.WaitInterrupt != nil {
		workerCtx = context.WithoutCancel(ctx)
	}

	// Mark the report-settlement window before the run can start: from here
	// until the notification consumer records the terminal state, an await
	// must treat a completed snapshot of this run as still in flight.
	if wt.RequiresReport {
		c.markReportSettlementPending(workerID)
	}
	// ClientOverride is only ever built for a cross-provider pin, so the
	// pin's provider part names the provider that client belongs to.
	spawnProviderName := ""
	if req.ClientOverride != nil {
		spawnProviderName = pinProviderName(req.ModelPin)
	}
	sa, err := c.manager.Spawn(workerCtx, subagent.SpawnOptions{
		ID:                 workerID,
		ParticipantID:      participantID,
		Type:               wtype,
		TaskName:           threadMeta.TaskName,
		AgentProfile:       threadMeta.AgentProfile,
		AgentPath:          threadMeta.Path,
		ParentID:           threadMeta.ParentID,
		Description:        req.Description,
		Prompt:             req.Prompt,
		SystemPrompt:       sys,
		Toolkit:            workerKit,
		HistoryPath:        historyPath,
		WorkerRoot:         workerRoot,
		Model:              strings.TrimSpace(req.ModelOverride),
		ModelPin:           strings.TrimSpace(req.ModelPin),
		ModelAlias:         strings.TrimSpace(req.ModelAlias),
		ModelAliasFallback: modelAliasFallback,
		WorkerRuntime:      spawnRuntime,
		Client:             req.ClientOverride,
		ProviderName:       spawnProviderName,
	})
	if err != nil {
		c.clearReportSettlement(workerID)
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		c.settleSpawnLaunchFailure(workerID, fmt.Errorf("spawn: %w", err))
		return nil, fmt.Errorf("spawn: %w", err)
	}
	releaseDirectAdmission()
	releaseDirectAdmission = nil
	spawnSlot.releaseAndKickQueued()
	retainAdmission = true
	releaseLeaseOnReturn = false
	spawned := sa.Snapshot()

	result := &SpawnResult{
		Action:             "spawn_agent",
		AgentID:            spawned.ID,
		ParticipantID:      participantID,
		TaskName:           threadMeta.TaskName,
		AgentProfile:       threadMeta.AgentProfile,
		AgentPath:          threadMeta.Path,
		Status:             string(spawned.Status),
		Isolation:          string(isolation),
		ModelAlias:         strings.TrimSpace(req.ModelAlias),
		ModelAliasFallback: modelAliasFallback,
		ResolvedProvider:   resolvedProvider,
		ResolvedModel:      resolvedModel,
		ResolvedAPIModel:   resolvedAPIModel,
		ValidAliases:       modelAliasValid,
	}
	result.NextSteps = spawnResultNextSteps(result.Status, req.Synchronous, result.Isolation, result.AgentPath)
	if worktreeRef != nil {
		result.WorktreePath = worktreeRef.Path
	}

	if !req.Synchronous {
		return result, nil
	}

	// Synchronous mode: wait for completion. There is no hidden default
	// lifetime here; user stop, session shutdown, CLI/app timeout, or an
	// explicit request timeout owns cancellation.
	waitCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	var snap subagent.SubAgentSnapshot
	if req.WaitInterrupt == nil {
		snap, err = c.manager.Wait(waitCtx, spawned.ID)
	} else {
		type waitResult struct {
			snapshot subagent.SubAgentSnapshot
			err      error
		}
		waitDone := make(chan waitResult, 1)
		interruptibleWaitCtx, stopWait := context.WithCancel(waitCtx)
		defer stopWait()
		go func() {
			waited, waitErr := c.manager.Wait(interruptibleWaitCtx, spawned.ID)
			waitDone <- waitResult{snapshot: waited, err: waitErr}
		}()
		select {
		case waited := <-waitDone:
			snap, err = waited.snapshot, waited.err
		case <-req.WaitInterrupt:
			stopWait()
			waited := <-waitDone
			if ctx.Err() != nil {
				return nil, fmt.Errorf("wait: %w", ctx.Err())
			}
			snap = waited.snapshot
			if !subagent.IsTerminal(snap.Status) {
				result.Status = string(snap.Status)
				result.Backgrounded = true
				result.NextSteps = spawnResultNextSteps(result.Status, false, result.Isolation, result.AgentPath)
				return result, nil
			}
			err = nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("wait: %w", err)
	}
	result.Status = string(snap.Status)
	resultID, claimed, consumedBy, claimErr := c.claimAgentResultDelivery(snap, agentResultConsumerSpawnAgent)
	if claimErr != nil {
		return nil, fmt.Errorf("persist synchronous agent result delivery: %w", claimErr)
	}
	result.ResultID = resultID
	ref := c.AgentResultReference(snap)
	result.Result = ref.Preview
	result.ResultPath = ref.Path
	result.ResultBytes = ref.Bytes
	result.ResultTruncated = ref.Truncated
	if resultID != "" && !claimed {
		result.ResultConsumed = true
		result.ConsumedBy = consumedBy
		result.Result = ""
	}
	if snap.Error != nil {
		result.Error = snap.Error.Error()
	}
	if !snap.CompletedAt.IsZero() && !snap.StartedAt.IsZero() {
		result.DurationMS = snap.CompletedAt.Sub(snap.StartedAt).Milliseconds()
	}
	result.NextSteps = spawnResultNextSteps(result.Status, true, result.Isolation, result.AgentPath)

	return result, nil
}

// newEphemeralParticipant creates the participant identity for a
// freshly spawned worker and persists it through the configured
// ParticipantStore. Persistence failures are logged but never block a
// spawn; the in-memory identity is returned regardless.
func (c *AgentControl) newEphemeralParticipant(taskName string, wt WorkerType) participant.Participant {
	now := time.Now().UTC()
	p := participant.Participant{
		ID:        participant.NewID(),
		Kind:      participant.KindEphemeral,
		Name:      participant.DeriveEphemeralName(taskName, wt.Name),
		Role:      wt.Name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if c != nil && c.participants != nil {
		if err := c.participants.Upsert(p); err != nil {
			providers.DebugLogf("agentcontrol: persist participant %s: %v", p.ID, err)
		}
	}
	return p
}

func spawnResultNextSteps(status string, synchronous bool, isolation string, agentPath string) []string {
	pathHint := strings.TrimSpace(agentPath)
	if pathHint == "" {
		pathHint = "this agent"
	}
	worktreeHint := ""
	if IsolationMode(isolation) == IsolationWorktree {
		worktreeHint = " Inspect worktree_path and the worker's patch artifacts before merging or relying on file changes."
	}
	switch subagent.Status(strings.TrimSpace(status)) {
	case subagent.StatusQueued:
		return []string{
			"The worker is queued; do not spawn a duplicate for the same task unless requirements change.",
			"Continue non-overlapping local work when available; " + pathHint + " will resume you with a completion notification when it finishes.",
		}
	case subagent.StatusRunning:
		return []string{
			"Continue non-overlapping local work when available; the worker will send a background completion notification when it finishes.",
			"When the next step depends on this worker's output, end your turn and let its completion notification resume you rather than blocking on it." + worktreeHint,
		}
	case subagent.StatusCompleted:
		if synchronous {
			return []string{
				"Inspect the worker result and any agent_report artifacts before relying on the handoff.",
				"Record the handoff in the parent task or thread when this result belongs to a larger workflow." + worktreeHint,
			}
		}
		return []string{
			"Inspect the worker's completion notification and agent_report artifacts before relying on the handoff." + worktreeHint,
		}
	case subagent.StatusFailed:
		return []string{
			"Inspect error and any partial artifacts, then decide whether to retry with a narrower brief, rollback, or ask the user.",
		}
	default:
		return []string{
			"Inspect the worker status before deciding whether to continue local work, await the worker, retry, or close the task.",
		}
	}
}

// ForkRequest is the internal shape of a spawn_agent invocation where
// subagent_type is omitted. It always uses the default general-purpose
// agent definition, but isolation is still caller-selectable: a forked
// history and a worktree are orthogonal concerns.
type ForkRequest struct {
	TaskName     string
	AgentProfile string // optional durable memory profile to wake for this worker
	Description  string
	ForkMode     string
	ParentID     string
	ParentPath   string
	BaseRepo     string // optional: chain off another worktree (worktree mode only)
	// Isolation overrides the default agent type's DefaultIsolation
	// when set. Empty string means "use the type default".
	Isolation string
	// Prompt is what the worker sees as its FINAL user message,
	// appended to the inherited history. Callers should wrap any
	// role-override instructions in <system-reminder> tags so the
	// model treats them as authoritative over anything in the
	// inherited parent system prompt.
	Prompt      string
	Synchronous bool
	Timeout     time.Duration
	// ModelAlias is the configured alias requested by the caller (e.g.
	// "cheap" or "frontend"). It is resolved at admission into a
	// complete WorkerRuntime via the registered ModelAliasResolver.
	// An empty or unknown alias falls back to the current worker
	// default and records the fallback for diagnostics. The raw alias
	// string is persisted with queued forks so it can be re-resolved
	// when the queued item actually launches.
	ModelAlias string
	// ModelPin is separate internal participant plumbing. Public spawn_agent
	// calls never populate it; it is used only when ModelAlias is omitted.
	ModelPin string
}

// Fork launches a sub-agent that inherits a snapshot of the parent
// agent's conversation history. The worker's first request to the
// LLM provider replays the parent's history verbatim and adds the
// fork prompt as the final user message — preserving prompt-cache
// hits across the fork boundary.
//
// `parentHistory` MUST be a complete history with no dangling
// tool_use blocks: the caller is expected to have already stripped the
// in-flight spawn_agent
// assistant turn before passing it through.
func (c *AgentControl) Fork(ctx context.Context, req ForkRequest, parentHistory []providers.ChatMessage) (*SpawnResult, error) {
	if c.isStopping() {
		return nil, errAgentControlStopping
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	if len(parentHistory) == 0 {
		return nil, errors.New("spawn_agent fork: no parent history (only the main agent in an interactive session can fork)")
	}
	// Resolve the default agent type so the fork has the full tool set.
	wt, err := LookupWorkerType(DefaultSubagentType)
	if err != nil {
		return nil, err
	}

	workerID := newAgentControlWorkerID(wt.Name)
	taskName := req.TaskName
	agentProfile := strings.TrimSpace(req.AgentProfile)
	isolation, err := NormalizeIsolation(req.Isolation, wt)
	if err != nil {
		return nil, err
	}
	if isolation == IsolationInplace && strings.TrimSpace(req.BaseRepo) != "" {
		return nil, errors.New("base_repo is only supported with isolation=worktree")
	}
	// Resolve the requested model alias at admission for diagnostics. Queued
	// forks re-resolve the alias at actual launch; direct forks use this
	// runtime immediately. Unknown and omitted aliases use the current
	// manager defaults, but unknown aliases record ModelAliasFallback=true
	// and list valid aliases for diagnosis. The resolved runtime is
	// snapshotted at first run so the fork's runtime stays frozen across
	// followups and restart.
	aliasRuntime, aliasFallback, aliasValid, aliasErr := c.resolveModelAlias(req.ModelAlias)
	if aliasErr != nil {
		return nil, fmt.Errorf("model alias: %w", aliasErr)
	}
	var spawnRuntime *subagent.WorkerRuntime
	modelAliasFallback := false
	var modelAliasValid []string
	if aliasRuntime != nil {
		spawnRuntime = aliasRuntime
	} else if aliasFallback {
		defaults := c.manager.DefaultWorkerRuntime()
		spawnRuntime = &defaults
		modelAliasFallback = true
		modelAliasValid = aliasValid
	} else {
		// Omitted alias: snapshot current defaults so the runtime is frozen
		// from first run.
		defaults := c.manager.DefaultWorkerRuntime()
		spawnRuntime = &defaults
	}
	resolvedProvider := ""
	resolvedModel := ""
	resolvedAPIModel := ""
	if spawnRuntime != nil {
		resolvedProvider = strings.TrimSpace(spawnRuntime.Provider)
		resolvedModel = strings.TrimSpace(spawnRuntime.Model)
		resolvedAPIModel = strings.TrimSpace(spawnRuntime.APIModel)
		if resolvedAPIModel == "" {
			resolvedAPIModel = resolvedModel
		}
	}

	spawnSlot, admitted := c.tryReserveSpawnSlot(workerID)
	if !admitted {
		if req.Synchronous {
			return nil, fmt.Errorf("max parallel sub-agents reached (%d). Wait for one to complete or use async spawn so the task can queue.", c.maxParallel)
		}
		resolvedBaseRepo := strings.TrimSpace(req.BaseRepo)
		resolvedBaseRevision := ""
		if isolation == IsolationWorktree {
			resolved, resolveErr := c.resolveQueuedWorktreeBase(req.BaseRepo)
			if resolveErr != nil {
				return nil, resolveErr
			}
			resolvedBaseRepo = resolved.Repo
			resolvedBaseRevision = resolved.Revision
		}
		releaseQueueAdmission, admissionErr := c.beginWorkerTurn()
		if admissionErr != nil {
			return nil, admissionErr
		}
		// Keep thread registration and durable queue publication indivisible
		// from this process's drains and cancellation paths.
		c.queueDrainMu.Lock()
		threadMeta, err := c.registerChildThreadWithStatus(workerID, taskName, agentProfile, wt.Name, req.Prompt, agentthread.SourceThreadSpawn, req.ForkMode, req.ParentID, req.ParentPath, agentthread.StatusPending)
		if err != nil {
			c.queueDrainMu.Unlock()
			releaseQueueAdmission()
			return nil, err
		}
		prt := c.newEphemeralParticipant(threadMeta.TaskName, wt)
		prepared := preparedSpawn{
			WorkerID:      workerID,
			ParticipantID: prt.ID,
			WorkerType:    wt,
			ThreadMeta:    threadMeta,
			Description:   req.Description,
			Prompt:        req.Prompt,
			Isolation:     isolation,
			BaseRepo:      resolvedBaseRepo,
			BaseRevision:  resolvedBaseRevision,
			IsFork:        true,
			ForkMode:      req.ForkMode,
			ParentHistory: providers.CloneChatMessages(parentHistory),
			ModelAlias:    strings.TrimSpace(req.ModelAlias),
		}
		c.recordHarnessTaskQueued(threadMeta, wt.Name, req.Prompt, isolation, resolvedBaseRepo)
		if err := c.enqueuePreparedSpawn(prepared); err != nil {
			c.settleSpawnLaunchFailure(workerID, err)
			c.queueDrainMu.Unlock()
			releaseQueueAdmission()
			return nil, err
		}
		result := &SpawnResult{
			Action:             "spawn_agent",
			AgentID:            workerID,
			TaskName:           threadMeta.TaskName,
			AgentProfile:       threadMeta.AgentProfile,
			AgentPath:          threadMeta.Path,
			Status:             "queued",
			Isolation:          string(isolation),
			ModelAlias:         strings.TrimSpace(req.ModelAlias),
			ModelAliasFallback: modelAliasFallback,
			ResolvedProvider:   resolvedProvider,
			ResolvedModel:      resolvedModel,
			ResolvedAPIModel:   resolvedAPIModel,
			ValidAliases:       modelAliasValid,
			NextSteps:          spawnResultNextSteps("queued", false, string(isolation), threadMeta.Path),
		}
		c.queueDrainMu.Unlock()
		releaseQueueAdmission()
		go c.maybeStartQueued(context.Background())
		return result, nil
	}
	defer spawnSlot.releaseAndKickQueued()
	releaseDirectAdmission, admissionErr := c.beginWorkerTurn()
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer func() {
		if releaseDirectAdmission != nil {
			releaseDirectAdmission()
		}
	}()
	if _, err := c.acquireWorkerExecution(workerID); err != nil {
		return nil, err
	}
	releaseLeaseOnReturn := true
	defer func() {
		if releaseLeaseOnReturn {
			c.releaseWorkerExecution(workerID)
		}
	}()

	parentRepo, worktrees := c.workspaceSnapshot()
	var (
		workerRoot  string
		worktreeRef *worktree.Worktree
	)
	if isolation == IsolationWorktree {
		if worktrees == nil {
			return nil, errors.New("isolation=worktree requires repository worktree support (this workspace does not support isolated worktrees)")
		}
		worktreeRef, err = worktrees.Create(c.sessionID, workerID, req.BaseRepo)
		if err != nil {
			return nil, fmt.Errorf("worktree create: %w", err)
		}
		workerRoot = worktreeRef.Path
	} else {
		workerRoot = parentRepo
	}

	forkPrompt := req.Prompt
	if isolation == IsolationWorktree {
		forkPrompt = appendForkWorktreeReminder(forkPrompt, workerRoot, isolation)
	}

	threadMeta, err := c.registerChildThread(workerID, taskName, agentProfile, wt.Name, forkPrompt, agentthread.SourceThreadSpawn, req.ForkMode, req.ParentID, req.ParentPath)
	if err != nil {
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return nil, err
	}
	c.recordHarnessTaskStart(threadMeta, wt.Name, req.Prompt, workerRoot, isolation, req.BaseRepo)

	// Create the worker identity. Failure to persist never blocks the spawn.
	prt := c.newEphemeralParticipant(threadMeta.TaskName, wt)

	workerKit, err := c.workerFact(workerRoot, wt, threadMeta)
	if err != nil {
		c.settleSpawnLaunchFailure(workerID, fmt.Errorf("worker toolkit: %w", err))
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return nil, fmt.Errorf("worker toolkit: %w", err)
	}

	historyPath := ""
	if c.historyDir != "" {
		historyPath = filepath.Join(c.historyDir, workerID+".json")
	}

	initialHistory := providers.CloneChatMessages(parentHistory)
	sys, sysErr := c.workerSystemPrompt(workerRoot, wt, threadMeta, isolation)
	if sysErr != nil {
		c.settleSpawnLaunchFailure(workerID, fmt.Errorf("worker system prompt: %w", sysErr))
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return nil, fmt.Errorf("worker system prompt: %w", sysErr)
	}
	initialHistory = withInitialSystemPrompt(initialHistory, sys)

	// Forks keep the parent's conversation body but always replace the
	// system prompt so the worker prompt and worker toolkit come from
	// the same compiled model surface.
	workerCtx := ctx
	if !req.Synchronous {
		workerCtx = context.WithoutCancel(ctx)
	}

	sa, err := c.manager.Spawn(workerCtx, subagent.SpawnOptions{
		ID:                 workerID,
		ParticipantID:      prt.ID,
		Type:               wt.Name,
		TaskName:           threadMeta.TaskName,
		AgentProfile:       threadMeta.AgentProfile,
		AgentPath:          threadMeta.Path,
		ParentID:           threadMeta.ParentID,
		Description:        req.Description,
		Prompt:             forkPrompt,
		Toolkit:            workerKit,
		HistoryPath:        historyPath,
		WorkerRoot:         workerRoot,
		ModelAlias:         strings.TrimSpace(req.ModelAlias),
		ModelAliasFallback: modelAliasFallback,
		WorkerRuntime:      spawnRuntime,
		InitialHistory:     initialHistory,
	})
	if err != nil {
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		c.settleSpawnLaunchFailure(workerID, fmt.Errorf("spawn: %w", err))
		return nil, fmt.Errorf("spawn: %w", err)
	}
	releaseDirectAdmission()
	releaseDirectAdmission = nil
	spawnSlot.releaseAndKickQueued()
	releaseLeaseOnReturn = false
	spawned := sa.Snapshot()

	result := &SpawnResult{
		Action:             "spawn_agent",
		AgentID:            spawned.ID,
		TaskName:           threadMeta.TaskName,
		AgentProfile:       threadMeta.AgentProfile,
		AgentPath:          threadMeta.Path,
		Status:             string(spawned.Status),
		Isolation:          string(isolation),
		ModelAlias:         strings.TrimSpace(req.ModelAlias),
		ModelAliasFallback: modelAliasFallback,
		ResolvedProvider:   resolvedProvider,
		ResolvedModel:      resolvedModel,
		ResolvedAPIModel:   resolvedAPIModel,
		ValidAliases:       modelAliasValid,
	}
	result.NextSteps = spawnResultNextSteps(result.Status, req.Synchronous, result.Isolation, result.AgentPath)
	if worktreeRef != nil {
		result.WorktreePath = worktreeRef.Path
	}

	if !req.Synchronous {
		return result, nil
	}

	waitCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	snap, err := c.manager.Wait(waitCtx, spawned.ID)
	if err != nil {
		return nil, fmt.Errorf("wait: %w", err)
	}
	result.Status = string(snap.Status)
	ref := c.AgentResultReference(snap)
	result.Result = ref.Preview
	result.ResultPath = ref.Path
	result.ResultBytes = ref.Bytes
	result.ResultTruncated = ref.Truncated
	if snap.Error != nil {
		result.Error = snap.Error.Error()
	}
	if !snap.CompletedAt.IsZero() && !snap.StartedAt.IsZero() {
		result.DurationMS = snap.CompletedAt.Sub(snap.StartedAt).Milliseconds()
	}
	result.NextSteps = spawnResultNextSteps(result.Status, true, result.Isolation, result.AgentPath)
	return result, nil
}

// StopAll cancels every running or queued worker. Used for Ctrl+C handling.
func (c *AgentControl) StopAll() {
	if c == nil {
		return
	}
	// Issue the cancellation wave before waiting on queue storage or admission
	// compensation. A durable queue outage must not leave already-running
	// workers consuming provider calls indefinitely.
	c.manager.StopAll()
	c.queueDrainMu.Lock()
	defer c.queueDrainMu.Unlock()
	c.queueMu.Lock()
	queued := c.queued
	c.queued = nil
	c.queueMu.Unlock()
	now := time.Now().UTC()
	for _, prepared := range queued {
		cancelled, err := c.cancelQueuedSpawn(prepared, now)
		if err != nil {
			providers.DebugLogf("agentcontrol: cancel queued spawn %s: %v", prepared.WorkerID, err)
			c.requeuePreparedSpawn(prepared)
			continue
		}
		if !cancelled {
			continue
		}
	}
}

// Stop cancels a specific worker by ID, path, or task name. Returns false if not found.
func (c *AgentControl) Stop(target string) bool {
	return c.StopFrom(agentthread.RootPath, target)
}

func (c *AgentControl) StopFrom(currentPath, target string) bool {
	meta, ok := c.threads.ResolveFrom(currentPath, target)
	if !ok || meta.Path == agentthread.RootPath {
		return false
	}
	subtree := c.threads.Subtree(meta.ID)
	if len(subtree) == 0 {
		subtree = []agentthread.Metadata{meta}
	}
	targetIDs := make(map[string]struct{}, len(subtree))
	for _, node := range subtree {
		if node.Path != agentthread.RootPath {
			targetIDs[node.ID] = struct{}{}
		}
	}
	stopped := false
	now := time.Now().UTC()
	queuedHandled := make(map[string]bool)
	c.queueDrainMu.Lock()
	queued := c.takeQueuedSpawns(targetIDs)
	for _, prepared := range queued {
		cancelled, err := c.cancelQueuedSpawn(prepared, now)
		if err != nil {
			providers.DebugLogf("agentcontrol: cancel queued spawn %s: %v", prepared.WorkerID, err)
			c.requeuePreparedSpawn(prepared)
			queuedHandled[prepared.WorkerID] = true
			continue
		}
		if !cancelled {
			queuedHandled[prepared.WorkerID] = true
			continue
		}
		queuedHandled[prepared.WorkerID] = true
		stopped = true
	}
	c.queueDrainMu.Unlock()
	for _, node := range subtree {
		if node.Path == agentthread.RootPath || queuedHandled[node.ID] {
			continue
		}
		if c.manager.Stop(node.ID) {
			stopped = true
		}
		if closed, found := c.threads.UpdateEdgeStatus(node.ID, agentthread.EdgeClosed, now); found {
			_ = c.threadStore.RecordEdgeStatus(closed)
		}
	}
	return stopped
}

// List returns snapshots of all sub-agents in this session.
func (c *AgentControl) List() []subagent.SubAgentSnapshot {
	return c.ListFrom(agentthread.RootPath, "")
}

func (c *AgentControl) ListFrom(currentPath, pathPrefix string) []subagent.SubAgentSnapshot {
	list := c.manager.List()
	known := make(map[string]struct{}, len(list))
	for _, snap := range list {
		known[snap.ID] = struct{}{}
	}
	for _, snap := range c.queuedSnapshots() {
		if _, ok := known[snap.ID]; ok {
			continue
		}
		list = append(list, snap)
	}
	prefix := strings.TrimSpace(pathPrefix)
	if prefix == "" {
		return list
	}
	resolved, err := agentthread.ResolveAgentPath(agentthread.AgentPath(currentPath), prefix)
	if err != nil {
		if parsed, parseErr := agentthread.ParseAgentPath(prefix); parseErr == nil {
			resolved = parsed
		} else {
			return nil
		}
	}
	want := string(resolved)
	out := make([]subagent.SubAgentSnapshot, 0, len(list))
	for _, snap := range list {
		if snap.AgentPath == want || strings.HasPrefix(snap.AgentPath, want+"/") {
			out = append(out, snap)
		}
	}
	return out
}

func (c *AgentControl) queuedSnapshots() []subagent.SubAgentSnapshot {
	if c == nil || c.harnessStore == nil {
		return nil
	}
	tasks, err := c.harnessStore.ListTasks()
	if err != nil {
		return nil
	}
	out := make([]subagent.SubAgentSnapshot, 0)
	for _, task := range tasks {
		if task.Status != harness.TaskStatusQueued {
			continue
		}
		out = append(out, subagent.SubAgentSnapshot{
			ID:          task.ID,
			Type:        task.Role,
			TaskName:    task.Name,
			AgentPath:   task.Path,
			ParentID:    task.ParentID,
			Description: task.Name,
			Status:      subagent.StatusQueued,
			StartedAt:   task.StartedAt,
		})
	}
	return out
}

// SendMessage delivers a follow-up message to a specific sub-agent.
// Messages are queued while the worker is running and injected as
// user-role turns before the next model round.
func (c *AgentControl) SendMessage(ctx context.Context, target, message string) error {
	return c.SendMessageFrom(agentthread.RootPath, ctx, target, message)
}

func (c *AgentControl) SendMessageFrom(currentPath string, ctx context.Context, target, message string) error {
	if c.isStopping() {
		return errAgentControlStopping
	}
	id := c.resolveAgentIDFrom(currentPath, target)
	if id == "" {
		return errors.New("target is required")
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		return errors.New("message is required")
	}
	snap, newLease, unlockTransition, err := c.prepareWorkerFollowup(ctx, id)
	if err != nil {
		return err
	}
	defer unlockTransition()
	releaseNewLease := func() {
		if newLease {
			c.releaseWorkerExecution(id)
		}
	}
	// Queue-or-resume, identical to followup_task: a running target keeps
	// the message in its mailbox for its next model round without being
	// interrupted, while a terminal target (completed, failed, cancelled)
	// is revived in place with its full context plus this message.
	// send_message carries trigger_turn=false so the child reads it as
	// interim communication rather than a task hand-off.
	communication := newInterAgentCommunication(currentPath, snap.AgentPath, msg, false)
	releaseTurnAdmission, admissionErr := c.beginWorkerTurn()
	if admissionErr != nil {
		releaseNewLease()
		return admissionErr
	}
	resumed, err := c.manager.Followup(ctx, id, communication.String())
	releaseTurnAdmission()
	if err != nil {
		releaseNewLease()
		return err
	}
	if isFinalSubAgentStatus(snap.Status) {
		c.recordWorkerResumed(resumed)
	}
	_ = c.threadStore.RecordCommunication(id, communication)
	if meta, ok := c.threads.UpdateLastTaskMessage(id, msg, time.Now().UTC()); ok {
		_ = c.threadStore.UpsertThread(meta)
	}
	return nil
}

func (c *AgentControl) FollowupTask(ctx context.Context, target, message string) (subagent.SubAgentSnapshot, error) {
	return c.FollowupTaskFrom(agentthread.RootPath, ctx, target, message)
}

func (c *AgentControl) FollowupTaskFrom(currentPath string, ctx context.Context, target, message string) (subagent.SubAgentSnapshot, error) {
	if c.isStopping() {
		return subagent.SubAgentSnapshot{}, errAgentControlStopping
	}
	id := c.resolveAgentIDFrom(currentPath, target)
	if id == "" {
		return subagent.SubAgentSnapshot{}, errors.New("target is required")
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		return subagent.SubAgentSnapshot{}, errors.New("message is required")
	}
	current, newLease, unlockTransition, err := c.prepareWorkerFollowup(ctx, id)
	if err != nil {
		return subagent.SubAgentSnapshot{}, err
	}
	defer unlockTransition()
	releaseNewLease := func() {
		if newLease {
			c.releaseWorkerExecution(id)
		}
	}
	communication := newInterAgentCommunication(currentPath, current.AgentPath, msg, true)
	releaseTurnAdmission, admissionErr := c.beginWorkerTurn()
	if admissionErr != nil {
		releaseNewLease()
		return subagent.SubAgentSnapshot{}, admissionErr
	}
	snap, err := c.manager.Followup(ctx, id, communication.String())
	releaseTurnAdmission()
	if err != nil {
		releaseNewLease()
		return snap, err
	}
	if isFinalSubAgentStatus(current.Status) {
		c.recordWorkerResumed(snap)
	}
	_ = c.threadStore.RecordCommunication(id, communication)
	if meta, ok := c.threads.UpdateLastTaskMessage(id, msg, time.Now().UTC()); ok {
		_ = c.threadStore.UpsertThread(meta)
	}
	return snap, nil
}

func (c *AgentControl) Wait(ctx context.Context, target string) (subagent.SubAgentSnapshot, error) {
	return c.WaitFrom(agentthread.RootPath, ctx, target)
}

func (c *AgentControl) WaitFrom(currentPath string, ctx context.Context, target string) (subagent.SubAgentSnapshot, error) {
	id := c.resolveAgentIDFrom(currentPath, target)
	if id == "" {
		return subagent.SubAgentSnapshot{}, errors.New("target is required")
	}
	return c.manager.Wait(ctx, id)
}

const (
	WaitAgentSignalTimeout       = "timeout"
	WaitAgentSignalQueuedMessage = "queued_message"
	WaitAgentSignalCompleted     = "agent_completed"
	WaitAgentSignalFailed        = "agent_failed"
	WaitAgentSignalCancelled     = "agent_cancelled"
)

type WaitAgentSignal struct {
	Received            bool
	SignalType          string
	AgentID             string
	AgentPath           string
	TaskName            string
	ParentID            string
	Status              string
	Description         string
	PendingMessageCount int
}

func (c *AgentControl) WaitForAgentNotificationFrom(currentPath string, ctx context.Context) (WaitAgentSignal, error) {
	if c == nil || c.manager == nil {
		return WaitAgentSignal{}, errors.New("agent control not configured")
	}
	currentID := c.agentIDForPath(currentPath)
	if signal, ok := c.queuedMessageSignal(currentID); ok {
		return signal, nil
	}
	ch := make(chan subagent.Notification, 16)
	c.manager.Subscribe(ch)
	defer c.manager.Unsubscribe(ch)
	if signal, ok := c.queuedMessageSignal(currentID); ok {
		return signal, nil
	}
	for {
		select {
		case n := <-ch:
			if signal, ok := c.agentNotificationSignal(currentID, n); ok {
				return signal, nil
			}
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return WaitAgentSignal{SignalType: WaitAgentSignalTimeout}, nil
			}
			return WaitAgentSignal{}, ctx.Err()
		}
	}
}

func (c *AgentControl) WaitForMailboxUpdateFrom(currentPath string, ctx context.Context) (bool, error) {
	signal, err := c.WaitForAgentNotificationFrom(currentPath, ctx)
	if err != nil {
		return false, err
	}
	return signal.Received, nil
}

func (c *AgentControl) agentIDForPath(currentPath string) string {
	path := strings.TrimSpace(currentPath)
	if path == "" || path == agentthread.RootPath {
		return ""
	}
	if meta, ok := c.threads.ResolveFrom(path, path); ok && meta.Path != agentthread.RootPath {
		return meta.ID
	}
	return ""
}

func (c *AgentControl) queuedMessageSignal(currentID string) (WaitAgentSignal, bool) {
	if currentID == "" {
		return WaitAgentSignal{}, false
	}
	count := c.manager.PendingMessageCount(currentID)
	if count <= 0 {
		return WaitAgentSignal{}, false
	}
	signal := WaitAgentSignal{
		Received:            true,
		SignalType:          WaitAgentSignalQueuedMessage,
		AgentID:             currentID,
		PendingMessageCount: count,
	}
	if snap := c.snapshotByID(currentID); snap != nil {
		signal = waitAgentSignalFromSnapshot(WaitAgentSignalQueuedMessage, *snap)
		signal.PendingMessageCount = count
	}
	return signal, true
}

func (c *AgentControl) agentNotificationSignal(currentID string, n subagent.Notification) (WaitAgentSignal, bool) {
	if currentID == "" {
		if !isFinalSubAgentStatus(n.Status) {
			return WaitAgentSignal{}, false
		}
		parentID := strings.TrimSpace(n.Snapshot.ParentID)
		if parentID == "" || parentID == c.sessionID || parentID == c.rootThreadID {
			return waitAgentSignalFromSnapshot(waitAgentSignalTypeForStatus(n.Status), n.Snapshot), true
		}
		return WaitAgentSignal{}, false
	}
	if n.Snapshot.ID == currentID {
		if n.Snapshot.PendingMessageCount > 0 {
			signal := waitAgentSignalFromSnapshot(WaitAgentSignalQueuedMessage, n.Snapshot)
			signal.PendingMessageCount = n.Snapshot.PendingMessageCount
			return signal, true
		}
		if signal, ok := c.queuedMessageSignal(currentID); ok {
			return signal, true
		}
	}
	if strings.TrimSpace(n.Snapshot.ParentID) == currentID && isFinalSubAgentStatus(n.Status) {
		return waitAgentSignalFromSnapshot(waitAgentSignalTypeForStatus(n.Status), n.Snapshot), true
	}
	return WaitAgentSignal{}, false
}

func waitAgentSignalTypeForStatus(status subagent.Status) string {
	switch status {
	case subagent.StatusCompleted:
		return WaitAgentSignalCompleted
	case subagent.StatusFailed:
		return WaitAgentSignalFailed
	case subagent.StatusCancelled:
		return WaitAgentSignalCancelled
	default:
		return string(status)
	}
}

func waitAgentSignalFromSnapshot(signalType string, snap subagent.SubAgentSnapshot) WaitAgentSignal {
	return WaitAgentSignal{
		Received:    true,
		SignalType:  signalType,
		AgentID:     snap.ID,
		AgentPath:   snap.AgentPath,
		TaskName:    snap.TaskName,
		ParentID:    snap.ParentID,
		Status:      string(snap.Status),
		Description: snap.Description,
	}
}

// Subscribe forwards to the underlying manager so the UI can receive
// status notifications and publish mailbox messages.
func (c *AgentControl) Subscribe(ch chan<- subagent.Notification) {
	if c == nil || c.manager == nil {
		return
	}
	c.manager.Subscribe(ch)
}

func (c *AgentControl) Unsubscribe(ch chan<- subagent.Notification) {
	if c == nil || c.manager == nil {
		return
	}
	c.manager.Unsubscribe(ch)
}

func (c *AgentControl) SubscribeStream(ch chan<- subagent.StreamNotification) {
	if c == nil || c.manager == nil {
		return
	}
	c.manager.SubscribeStream(ch)
}

func (c *AgentControl) UnsubscribeStream(ch chan<- subagent.StreamNotification) {
	if c == nil || c.manager == nil {
		return
	}
	c.manager.UnsubscribeStream(ch)
}

func (c *AgentControl) registerRootThread() {
	if c == nil || c.threads == nil {
		return
	}
	sessionID := strings.TrimSpace(c.sessionID)
	if sessionID == "" || sessionID == "session-pending" {
		return
	}
	if c.rootThreadID == sessionID && c.rootThreadDir == c.threadDir {
		return
	}
	meta := c.threads.RegisterRoot(sessionID, sessionID, c.ParentRepo(), "", time.Now().UTC())
	_ = c.threadStore.UpsertThread(meta)
	c.rootThreadID = sessionID
	c.rootThreadDir = c.threadDir
}

func (c *AgentControl) workerSystemPrompt(rootDir string, wt WorkerType, meta agentthread.Metadata, isolation IsolationMode) (string, error) {
	base := ""
	if c != nil {
		base = c.defaultSys
	}
	if c != nil && c.workerPrompt != nil {
		customBase, err := c.workerPrompt(rootDir, wt, meta, isolation)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(customBase) != "" {
			base = customBase
		}
	}
	return composeWorkerSystemPrompt(base, wt, rootDir, isolation), nil
}

func withInitialSystemPrompt(history []providers.ChatMessage, systemPrompt string) []providers.ChatMessage {
	sys := strings.TrimSpace(systemPrompt)
	if sys == "" {
		return providers.CloneChatMessages(history)
	}
	out := make([]providers.ChatMessage, 0, len(history)+1)
	out = append(out, providers.ChatMessage{Role: "system", Content: sys})
	start := 0
	for start < len(history) && strings.TrimSpace(history[start].Role) == "system" {
		start++
	}
	out = append(out, providers.CloneChatMessages(history[start:])...)
	return out
}

func (c *AgentControl) registerChildThread(id, taskName, agentProfile, role, message string, source agentthread.SourceKind, forkMode, parentID, parentPath string) (agentthread.Metadata, error) {
	return c.registerChildThreadWithStatus(id, taskName, agentProfile, role, message, source, forkMode, parentID, parentPath, agentthread.StatusRunning)
}

func (c *AgentControl) registerChildThreadWithStatus(id, taskName, agentProfile, role, message string, source agentthread.SourceKind, forkMode, parentID, parentPath string, status agentthread.Status) (agentthread.Metadata, error) {
	if c == nil || c.threads == nil {
		return agentthread.Metadata{}, errors.New("thread registry is not configured")
	}
	c.registerRootThread()
	parentPath = strings.TrimSpace(parentPath)
	if parentPath == "" {
		parentPath = agentthread.RootPath
	}
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		parentID = c.sessionID
	}
	meta, err := c.threads.RegisterSpawn(agentthread.SpawnSpec{
		ID:              id,
		SessionID:       c.sessionID,
		ParentID:        parentID,
		ParentPath:      parentPath,
		TaskName:        taskName,
		AgentProfile:    strings.TrimSpace(agentProfile),
		Role:            role,
		LastTaskMessage: message,
		CWD:             c.ParentRepo(),
		SourceKind:      source,
		ForkMode:        strings.TrimSpace(forkMode),
		Status:          status,
		Now:             time.Now().UTC(),
	})
	if err != nil {
		return agentthread.Metadata{}, err
	}
	if err := c.threadStore.UpsertThread(meta); err != nil {
		return agentthread.Metadata{}, err
	}
	return meta, nil
}

func (c *AgentControl) recordHarnessTaskStart(meta agentthread.Metadata, role, intent, workerRoot string, isolation IsolationMode, baseRepo string) {
	if c == nil || c.harnessStore == nil {
		return
	}
	now := time.Now().UTC()
	_, existed := c.harnessTask(meta.ID)
	workspaceMode := harness.WorkspaceShared
	if isolation == IsolationWorktree {
		workspaceMode = harness.WorkspaceWorktree
	}
	runID := harnessRunID(meta.ID)
	task := harness.Task{
		ID:         meta.ID,
		SessionID:  c.sessionID,
		ParentID:   meta.ParentID,
		ParentPath: meta.Source.ParentPath,
		Path:       meta.Path,
		Name:       meta.TaskName,
		Role:       role,
		Intent:     intent,
		Workspace: harness.WorkspaceLease{
			Mode:      workspaceMode,
			Root:      workerRoot,
			BaseRepo:  strings.TrimSpace(baseRepo),
			CreatedAt: now,
		},
		Status:     harness.TaskStatusRunning,
		LastRunID:  runID,
		CardItemID: taskCardItemID(meta.ID),
		CreatedAt:  meta.CreatedAt,
		UpdatedAt:  now,
		StartedAt:  now,
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	run := harness.AgentRun{
		ID:        runID,
		TaskID:    meta.ID,
		AgentID:   meta.ID,
		Role:      role,
		Status:    harness.TaskStatusRunning,
		StartedAt: now,
	}
	_ = c.harnessStore.UpsertTask(task)
	_ = c.harnessStore.UpsertRun(run)
	eventType := harness.EventTaskCreated
	if existed {
		eventType = harness.EventTaskStatusChanged
	}
	_ = c.harnessStore.AppendEvent(harness.Event{
		Type:      eventType,
		TaskID:    meta.ID,
		RunID:     runID,
		AgentID:   meta.ID,
		Path:      meta.Path,
		Status:    string(harness.TaskStatusRunning),
		CreatedAt: now,
	})
	_ = c.harnessStore.AppendEvent(harness.Event{
		Type:      harness.EventWorkspaceAssigned,
		TaskID:    meta.ID,
		RunID:     runID,
		AgentID:   meta.ID,
		Path:      workerRoot,
		Status:    string(workspaceMode),
		CreatedAt: now,
	})
	_ = c.harnessStore.AppendEvent(harness.Event{
		Type:      harness.EventRunStarted,
		TaskID:    meta.ID,
		RunID:     runID,
		AgentID:   meta.ID,
		Path:      meta.Path,
		Status:    string(harness.TaskStatusRunning),
		CreatedAt: now,
	})
}

func (c *AgentControl) recordHarnessTaskQueued(meta agentthread.Metadata, role, intent string, isolation IsolationMode, baseRepo string) {
	if c == nil || c.harnessStore == nil {
		return
	}
	now := time.Now().UTC()
	workspaceMode := harness.WorkspaceShared
	if isolation == IsolationWorktree {
		workspaceMode = harness.WorkspaceWorktree
	}
	runID := harnessRunID(meta.ID)
	task := harness.Task{
		ID:         meta.ID,
		SessionID:  c.sessionID,
		ParentID:   meta.ParentID,
		ParentPath: meta.Source.ParentPath,
		Path:       meta.Path,
		Name:       meta.TaskName,
		Role:       role,
		Intent:     intent,
		Workspace: harness.WorkspaceLease{
			Mode:     workspaceMode,
			BaseRepo: strings.TrimSpace(baseRepo),
		},
		Status:     harness.TaskStatusQueued,
		LastRunID:  runID,
		CardItemID: taskCardItemID(meta.ID),
		CreatedAt:  meta.CreatedAt,
		UpdatedAt:  now,
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	_ = c.harnessStore.UpsertTask(task)
	_ = c.harnessStore.AppendEvent(harness.Event{
		Type:      harness.EventTaskCreated,
		TaskID:    meta.ID,
		RunID:     runID,
		AgentID:   meta.ID,
		Path:      meta.Path,
		Status:    string(harness.TaskStatusQueued),
		CreatedAt: now,
	})
	_ = c.harnessStore.AppendEvent(harness.Event{
		Type:      harness.EventTaskStatusChanged,
		TaskID:    meta.ID,
		RunID:     runID,
		AgentID:   meta.ID,
		Path:      meta.Path,
		Status:    string(harness.TaskStatusQueued),
		CreatedAt: now,
	})
}

func taskCardItemID(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	return "task-card-" + taskID
}

func (c *AgentControl) enqueuePreparedSpawn(prepared preparedSpawn) error {
	if c != nil && c.harnessStore != nil && c.harnessStore.Dir() != "" {
		payload, err := json.Marshal(queuedSpawnPayloadFromPrepared(prepared))
		if err != nil {
			return fmt.Errorf("persist queued spawn: %w", err)
		}
		if err := c.harnessStore.UpsertQueueItem(harness.QueueItem{
			ID:      prepared.WorkerID,
			TaskID:  prepared.WorkerID,
			Kind:    "agent_spawn",
			Payload: payload,
		}); err != nil {
			return fmt.Errorf("persist queued spawn: %w", err)
		}
	}
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	c.queued = append(c.queued, prepared)
	return nil
}

type queuedSpawnStartOutcome uint8

const (
	queuedSpawnStartUnknown queuedSpawnStartOutcome = iota
	queuedSpawnStarted
	queuedSpawnWorkerBusy
	queuedSpawnAlreadyClaimed
	queuedSpawnTerminalPending
	queuedSpawnRetryPending
	queuedSpawnFailed
	queuedSpawnCancelled
)

func (c *AgentControl) maybeStartQueued(ctx context.Context) {
	if c == nil || c.isStopping() || !c.queuedWorkEnabled() {
		return
	}
	c.queueDrainMu.Lock()
	defer c.queueDrainMu.Unlock()
	if !c.queuedWorkEnabled() {
		return
	}
	for {
		// Do not reserve scarce direct-spawn capacity for an empty drain. Queue
		// publication uses queueDrainMu too, so this check cannot miss an item
		// that should belong to the current drain.
		if !c.hasQueuedSpawns() {
			return
		}
		spawnSlot, admitted := c.tryReserveSpawnSlot("")
		if !admitted {
			return
		}
		prepared, ok := c.popQueuedSpawn()
		if !ok {
			spawnSlot.release()
			return
		}
		spawnSlot.bindWorker(prepared.WorkerID)
		outcome, err := func() (queuedSpawnStartOutcome, error) {
			defer spawnSlot.release()
			return c.startQueuedSpawn(ctx, prepared)
		}()
		switch outcome {
		case queuedSpawnStarted, queuedSpawnAlreadyClaimed, queuedSpawnFailed, queuedSpawnCancelled:
			if err != nil {
				providers.DebugLogf("agentcontrol: queued spawn %s failed: %v", prepared.WorkerID, err)
			}
			continue
		case queuedSpawnTerminalPending:
			// The durable terminal record supersedes the stale launch intent.
			// Drop this process's queue copy and wake terminal recovery after the
			// launcher has released its execution lease; recovery owns queue ack.
			c.scanPendingWorkerTerminals()
			continue
		case queuedSpawnWorkerBusy, queuedSpawnRetryPending:
			if err != nil {
				providers.DebugLogf("agentcontrol: queued spawn %s will retry: %v", prepared.WorkerID, err)
			}
			c.requeuePreparedSpawn(prepared)
			c.scheduleQueuedSpawnRetry()
			return
		default:
			// Preserve an item on an impossible/unknown launcher outcome. Dropping
			// durable intent here would be less recoverable than a delayed retry.
			providers.DebugLogf("agentcontrol: queued spawn %s returned unknown outcome %d: %v", prepared.WorkerID, outcome, err)
			c.requeuePreparedSpawn(prepared)
			c.scheduleQueuedSpawnRetry()
			return
		}
	}
}

func (c *AgentControl) requeuePreparedSpawn(prepared preparedSpawn) {
	if c == nil {
		return
	}
	c.queueMu.Lock()
	c.queued = append(c.queued, prepared)
	c.queueMu.Unlock()
}

const queuedSpawnLeaseRetryDelay = 200 * time.Millisecond

func (c *AgentControl) scheduleQueuedSpawnRetry() {
	if c == nil {
		return
	}
	c.queueRetryMu.Lock()
	if c.queueRetrying {
		c.queueRetryMu.Unlock()
		return
	}
	c.queueRetrying = true
	stop := c.statusStop
	c.queueRetryMu.Unlock()

	go func() {
		timer := time.NewTimer(queuedSpawnLeaseRetryDelay)
		defer timer.Stop()
		if stop == nil {
			<-timer.C
		} else {
			select {
			case <-timer.C:
			case <-stop:
				c.finishQueuedSpawnRetry()
				return
			}
			select {
			case <-stop:
				c.finishQueuedSpawnRetry()
				return
			default:
			}
		}
		c.finishQueuedSpawnRetry()
		c.maybeStartQueued(context.Background())
	}()
}

func (c *AgentControl) finishQueuedSpawnRetry() {
	c.queueRetryMu.Lock()
	c.queueRetrying = false
	c.queueRetryMu.Unlock()
}

func (c *AgentControl) popQueuedSpawn() (preparedSpawn, bool) {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	if len(c.queued) == 0 {
		return preparedSpawn{}, false
	}
	prepared := c.queued[0]
	c.queued[0] = preparedSpawn{}
	c.queued = c.queued[1:]
	return prepared, true
}

func (c *AgentControl) ackQueuedSpawn(workerID string) (bool, error) {
	if c == nil {
		return false, nil
	}
	c.queuedSpawnAckHookMu.Lock()
	hook := c.queuedSpawnAckForTest
	c.queuedSpawnAckHookMu.Unlock()
	if hook != nil {
		if err := hook(workerID); err != nil {
			return false, err
		}
	}
	if c.harnessStore == nil || c.harnessStore.Dir() == "" {
		return true, nil
	}
	return c.harnessStore.ClaimQueueItem(workerID)
}

func (c *AgentControl) beginQueuedLaunchAcknowledgement(workerID string) func() {
	if c == nil {
		return func() {}
	}
	workerID = strings.TrimSpace(workerID)
	c.queuedLaunchAckMu.Lock()
	if c.queuedLaunchAcks == nil {
		c.queuedLaunchAcks = make(map[string]struct{})
	}
	c.queuedLaunchAcks[workerID] = struct{}{}
	c.queuedLaunchAckMu.Unlock()
	return func() {
		c.queuedLaunchAckMu.Lock()
		delete(c.queuedLaunchAcks, workerID)
		c.queuedLaunchAckMu.Unlock()
	}
}

func (c *AgentControl) queuedLaunchAcknowledgementPending(workerID string) bool {
	if c == nil {
		return false
	}
	c.queuedLaunchAckMu.Lock()
	_, pending := c.queuedLaunchAcks[strings.TrimSpace(workerID)]
	c.queuedLaunchAckMu.Unlock()
	return pending
}

// Queue compensation retries are bounded: a persistently failing store must
// surface an error and defer to durable restart recovery instead of spinning
// while it holds queueDrainMu and the worker execution lease.
const (
	queuedSpawnAckRetryDelay      = 100 * time.Millisecond
	queuedSpawnStoreRetryAttempts = 50
)

// errQueuedSpawnYielded reports that a bounded compensation loop stopped early
// because durable state (queue tombstone or terminal record) owns recovery.
var errQueuedSpawnYielded = errors.New("queued spawn compensation yielded to durable recovery")

// retryQueuedSpawnStore retries op until it succeeds, canYield hands ownership
// to durable recovery, or the attempt cap is reached. It returns nil only on
// success; callers must surface the error and leave durable intent in place.
func (c *AgentControl) retryQueuedSpawnStore(label, workerID string, canYield func() bool, op func() error) error {
	var lastErr error
	for attempt := 1; attempt <= queuedSpawnStoreRetryAttempts; attempt++ {
		lastErr = op()
		if lastErr == nil {
			return nil
		}
		if attempt == 1 || attempt%10 == 0 {
			providers.DebugLogf("agentcontrol: %s %s attempt %d: %v", label, workerID, attempt, lastErr)
		}
		if canYield != nil && canYield() {
			providers.DebugLogf("agentcontrol: yield %s %s to durable recovery: %v", label, workerID, lastErr)
			return fmt.Errorf("%s %s: %w", label, workerID, errQueuedSpawnYielded)
		}
		time.Sleep(queuedSpawnAckRetryDelay)
	}
	return fmt.Errorf("%s %s failed after %d attempts: %w", label, workerID, queuedSpawnStoreRetryAttempts, lastErr)
}

func (c *AgentControl) markQueuedSpawnFailureReliably(workerID string, cause error) (durable bool, err error) {
	if c == nil || c.harnessStore == nil || c.harnessStore.Dir() == "" {
		return false, nil
	}
	errText := "queued spawn launch failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		errText = cause.Error()
	}
	var marked bool
	err = c.retryQueuedSpawnStore("mark queued spawn failing", workerID, c.isStopping, func() error {
		c.queuedSpawnAckHookMu.Lock()
		markHook := c.queuedSpawnMarkFailureForTest
		c.queuedSpawnAckHookMu.Unlock()
		if markHook != nil {
			return markHook(workerID)
		}
		var markErr error
		marked, markErr = c.harnessStore.MarkQueueItemFailing(workerID, errText)
		return markErr
	})
	if err != nil {
		return false, err
	}
	// Missing means the durable payload was already consumed. The worker
	// execution lease makes that impossible for a conforming contender,
	// but there is no recovery intent left to transition in either case.
	return marked, nil
}

func (c *AgentControl) acknowledgeQueuedTerminalReliably(workerID, intent string, intentDurable bool) error {
	return c.retryQueuedSpawnStore("acknowledge "+intent+" queued spawn", workerID, func() bool {
		return intentDurable && c.isStopping()
	}, func() error {
		_, err := c.ackQueuedSpawn(workerID)
		return err
	})
}

// acknowledgeLaunchedQueuedSpawnReliably is the successful-launch handoff.
// While the retained queue item is runnable the execution lease stays owned by
// the launched worker, so a bounded retry cannot expose a double launch: on
// exhaustion the terminal path or the next app-server consumes the item.
// During shutdown, Manager.StopAll can make the worker terminal while this
// loop owns the queue drain and lease-release barrier. Once both the live
// manager snapshot and the durable terminal intent prove that generation has
// stopped, it yields immediately: the retained queue item is superseded by the
// terminal record, and restart recovery consumes both without launching again.
func (c *AgentControl) acknowledgeLaunchedQueuedSpawnReliably(workerID string) error {
	err := c.retryQueuedSpawnStore("acknowledge launched queued spawn", workerID, func() bool {
		return c.queuedLaunchCanYieldToTerminalRecovery(workerID)
	}, func() error {
		_, ackErr := c.ackQueuedSpawn(workerID)
		return ackErr
	})
	if errors.Is(err, errQueuedSpawnYielded) {
		return nil
	}
	return err
}

func (c *AgentControl) queuedLaunchCanYieldToTerminalRecovery(workerID string) bool {
	if c == nil || !c.isStopping() || c.manager == nil {
		return false
	}
	worker := c.manager.Get(strings.TrimSpace(workerID))
	if worker == nil || !subagent.IsTerminal(worker.Snapshot().Status) {
		return false
	}
	pending, err := c.workerTerminalFinalizationPending(workerID)
	return err == nil && pending
}

func (c *AgentControl) cancelQueuedSpawn(prepared preparedSpawn, now time.Time) (bool, error) {
	if c == nil {
		return false, nil
	}
	workerID := prepared.WorkerID
	// Cancellation and launch use the same execution lease. This closes the
	// cross-process window where cancellation could either consume the durable
	// payload before rollback succeeds or race a launcher between settlement and
	// acknowledgement.
	acquired, err := c.acquireWorkerExecution(workerID)
	if err != nil {
		if errors.Is(err, errWorkerExecutionBusy) {
			return false, nil
		}
		return false, err
	}
	if !acquired {
		return false, nil
	}
	defer c.releaseWorkerExecution(workerID)
	cancellingDurable := false
	if c.harnessStore != nil && c.harnessStore.Dir() != "" {
		marked, err := c.harnessStore.MarkQueueItemCancelling(workerID)
		if err != nil {
			return false, fmt.Errorf("mark queued spawn cancelling: %w", err)
		}
		if !marked {
			return false, nil
		}
		cancellingDurable = true
	}
	if rollbackErr := c.rollbackQueuedTerminalReliably(prepared, cancellingDurable); rollbackErr != nil {
		// MarkQueueItemCancelling is the durable recovery authority. Leave that
		// tombstone and the prepared admission intact so a fresh process can
		// resume compensation instead of holding Close forever on an unavailable
		// external store.
		providers.DebugLogf("agentcontrol: cancel queued spawn %s left durable tombstone: %v", workerID, rollbackErr)
		return true, nil
	}
	c.settleQueuedSpawnCancellation(prepared, now)
	if ackErr := c.acknowledgeQueuedTerminalReliably(workerID, "cancellation", cancellingDurable); ackErr != nil {
		providers.DebugLogf("agentcontrol: cancelled queued spawn %s left durable tombstone: %v", workerID, ackErr)
	}
	return true, nil
}

// takeQueuedSpawns removes matching local payloads. Callers hold queueDrainMu,
// preserving the queueDrainMu -> queueMu order shared with queue drains.
func (c *AgentControl) takeQueuedSpawns(workerIDs map[string]struct{}) []preparedSpawn {
	if c == nil || len(workerIDs) == 0 {
		return nil
	}
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	removed := make([]preparedSpawn, 0, len(workerIDs))
	remaining := make([]preparedSpawn, 0, len(c.queued))
	for _, prepared := range c.queued {
		if _, ok := workerIDs[prepared.WorkerID]; ok {
			removed = append(removed, prepared)
			continue
		}
		remaining = append(remaining, prepared)
	}
	c.queued = remaining
	return removed
}

func (c *AgentControl) settleQueuedSpawnCancellation(prepared preparedSpawn, now time.Time) {
	if cancelled, ok := c.threads.UpdateStatus(prepared.WorkerID, agentthread.StatusCancelled, now); ok {
		_ = c.threadStore.RecordStatus(cancelled)
	}
	if closed, ok := c.threads.UpdateEdgeStatus(prepared.WorkerID, agentthread.EdgeClosed, now); ok {
		_ = c.threadStore.RecordEdgeStatus(closed)
	}
	if c.harnessStore != nil {
		_, _ = c.harnessStore.UpdateTaskStatus(prepared.WorkerID, harness.TaskStatusCancelled, now, 0, 0, "cancelled")
	}
}

func (c *AgentControl) rollbackQueuedTerminalReliably(prepared preparedSpawn, intentDurable bool) error {
	return c.rollbackPreparedSpawnAdmission(prepared, func() bool {
		return intentDurable && c.isStopping()
	})
}

func (c *AgentControl) rollbackPreparedSpawnAdmission(prepared preparedSpawn, canYield func() bool) error {
	rollback := prepared.AdmissionRollback
	return c.rollbackSpawnAdmissionReliably(prepared.WorkerID, rollback, canYield)
}

func (c *AgentControl) rollbackSpawnAdmissionReliably(workerID string, rollback SpawnAdmissionRollback, canYield func() bool) error {
	if rollback == nil {
		return nil
	}
	return c.retryQueuedSpawnStore("rollback spawn admission", workerID, canYield, rollback)
}

func queuedSpawnPayloadFromPrepared(prepared preparedSpawn) queuedSpawnPayload {
	return queuedSpawnPayload{
		WorkerID:      prepared.WorkerID,
		ParticipantID: prepared.ParticipantID,
		WorkerType:    prepared.WorkerType.Name,
		ThreadMeta:    prepared.ThreadMeta,
		Description:   prepared.Description,
		Prompt:        prepared.Prompt,
		Isolation:     string(prepared.Isolation),
		BaseRepo:      prepared.BaseRepo,
		BaseRevision:  prepared.BaseRevision,
		IsFork:        prepared.IsFork,
		ForkMode:      prepared.ForkMode,
		ParentHistory: providers.CloneChatMessages(prepared.ParentHistory),
		ModelOverride: prepared.ModelOverride,
		ModelPin:      prepared.ModelPin,
		ModelAlias:    prepared.ModelAlias,
	}
}

func preparedSpawnFromQueuedPayload(payload queuedSpawnPayload) (preparedSpawn, error) {
	wt, err := LookupWorkerType(payload.WorkerType)
	if err != nil {
		return preparedSpawn{}, err
	}
	isolation, err := NormalizeIsolation(payload.Isolation, wt)
	if err != nil {
		return preparedSpawn{}, err
	}
	workerID := strings.TrimSpace(payload.WorkerID)
	if workerID == "" {
		workerID = payload.ThreadMeta.ID
	}
	if workerID == "" {
		return preparedSpawn{}, errors.New("queued spawn worker_id is required")
	}
	if payload.ThreadMeta.ID == "" {
		payload.ThreadMeta.ID = workerID
	}
	return preparedSpawn{
		WorkerID:      workerID,
		ParticipantID: payload.ParticipantID,
		WorkerType:    wt,
		ThreadMeta:    payload.ThreadMeta,
		Description:   payload.Description,
		Prompt:        payload.Prompt,
		Isolation:     isolation,
		BaseRepo:      payload.BaseRepo,
		BaseRevision:  payload.BaseRevision,
		IsFork:        payload.IsFork,
		ForkMode:      payload.ForkMode,
		ParentHistory: providers.CloneChatMessages(payload.ParentHistory),
		ModelOverride: payload.ModelOverride,
		ModelPin:      payload.ModelPin,
		ModelAlias:    payload.ModelAlias,
	}, nil
}

func (c *AgentControl) restoreQueuedSpawns() error {
	if c == nil || c.harnessStore == nil || c.harnessStore.Dir() == "" {
		return nil
	}
	items, err := c.harnessStore.ListQueueItems()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Kind != "agent_spawn" {
			continue
		}
		var payload queuedSpawnPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			_ = c.harnessStore.DeleteQueueItem(item.ID)
			continue
		}
		prepared, err := preparedSpawnFromQueuedPayload(payload)
		if err != nil {
			_ = c.harnessStore.DeleteQueueItem(item.ID)
			continue
		}
		if err := c.threads.Restore(prepared.ThreadMeta); err != nil {
			return err
		}
		c.queueMu.Lock()
		c.queued = append(c.queued, prepared)
		c.queueMu.Unlock()
	}
	return nil
}

// resolveModelAlias resolves a configured model alias into a complete worker
// runtime. A blank alias returns nil and no fallback. A non-empty alias with
// no resolver or an unknown alias returns nil with fallback=true and the list
// of valid aliases for diagnostics. A resolver error for a configured alias
// is returned as an error so the spawn fails visibly instead of silently
// falling back to the worker default.
func (c *AgentControl) resolveModelAlias(alias string) (*subagent.WorkerRuntime, bool, []string, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, false, nil, nil
	}
	resolver := c.currentModelAliasResolver()
	if resolver == nil {
		return nil, true, nil, nil
	}
	res := resolver(alias)
	if res.Err != nil {
		return nil, false, nil, fmt.Errorf("resolve model alias %q: %w", alias, res.Err)
	}
	if res.Unknown {
		return nil, true, res.ValidAliases, nil
	}
	if !res.Found {
		return nil, true, res.ValidAliases, nil
	}
	runtime := res.Runtime.Clone()
	if strings.TrimSpace(runtime.Provider) == "" {
		return nil, false, nil, fmt.Errorf("resolve model alias %q: resolver returned an empty provider", alias)
	}
	if strings.TrimSpace(runtime.Model) == "" {
		return nil, false, nil, fmt.Errorf("resolve model alias %q: resolver returned an empty model", alias)
	}
	if strings.TrimSpace(runtime.APIModel) == "" {
		runtime.APIModel = strings.TrimSpace(runtime.Model)
	}
	if runtime.Client == nil {
		return nil, false, nil, fmt.Errorf("resolve model alias %q: resolver returned a nil client", alias)
	}
	return &runtime, false, nil, nil
}

// resolveSpawnModelPin turns a raw participant model pin into the concrete
// (model, client, provider) triple a spawn or resume should run with. It
// applies the cross-provider safety policy shared by queued-spawn restore
// and lazy-resume rehydration: an empty pin passes the given override
// through, a same-provider or bare-model pin only overrides the model, and
// a pin that targets a different provider MUST resolve to a fresh client
// via the registered resolver. A missing/erroring resolver, an empty
// resolved model, or a nil cross-provider client fails explicitly rather
// than silently using the worker default client. label names the caller
// for the error text. The returned provider names the provider of the
// returned client ("" when the client is nil or its provider is unknown)
// so the runner can stamp provenance onto the worker's native state.
func (c *AgentControl) resolveSpawnModelPin(label, modelOverride, rawPin string, clientOverride providers.StreamClient) (string, providers.StreamClient, string, error) {
	rawPin = strings.TrimSpace(rawPin)
	if rawPin == "" {
		return modelOverride, clientOverride, "", nil
	}
	resolver := c.currentModelPinResolver()
	if resolver == nil {
		return "", nil, "", fmt.Errorf("%s has model pin %q but no model-pin resolver is installed; refusing to fall back to the worker default client", label, rawPin)
	}
	resolvedModel, resolvedClient, resolveErr := resolver(rawPin)
	if resolveErr != nil {
		return "", nil, "", fmt.Errorf("%s model pin %q could not be resolved: %w", label, rawPin, resolveErr)
	}
	if strings.TrimSpace(resolvedModel) == "" {
		return "", nil, "", fmt.Errorf("%s model pin %q resolved to an empty model", label, rawPin)
	}
	if pinTargetsDifferentProvider(rawPin, c.WorkerProviderName()) {
		if resolvedClient == nil {
			return "", nil, "", fmt.Errorf("%s model pin %q targets a different provider but resolver returned no client; refusing to use the worker default client", label, rawPin)
		}
		clientOverride = resolvedClient
	}
	providerName := ""
	if clientOverride != nil {
		providerName = pinProviderName(rawPin)
	}
	return resolvedModel, clientOverride, providerName, nil
}

func (c *AgentControl) resolveQueuedWorktreeBase(baseRepo string) (worktree.ResolvedBase, error) {
	_, worktrees := c.workspaceSnapshot()
	if worktrees == nil {
		return worktree.ResolvedBase{}, errors.New("isolation=worktree requires repository worktree support (this workspace does not support isolated worktrees)")
	}
	resolved, err := worktrees.ResolveBase(baseRepo, "")
	if err != nil {
		return worktree.ResolvedBase{}, fmt.Errorf("resolve queued worktree base: %w", err)
	}
	return resolved, nil
}

func (c *AgentControl) startQueuedSpawn(ctx context.Context, prepared preparedSpawn) (outcome queuedSpawnStartOutcome, returnErr error) {
	acquired, err := c.acquireWorkerExecution(prepared.WorkerID)
	if err != nil {
		if errors.Is(err, errWorkerExecutionBusy) {
			return queuedSpawnWorkerBusy, nil
		}
		return queuedSpawnRetryPending, err
	}
	if !acquired {
		return queuedSpawnWorkerBusy, nil
	}
	releaseLeaseOnReturn := true
	defer func() {
		if releaseLeaseOnReturn {
			c.releaseWorkerExecution(prepared.WorkerID)
		}
	}()
	// Keep the durable payload until Manager.Spawn has published the running
	// worker. The execution lease serializes this non-consuming check with other
	// launchers and cancellation; a process crash anywhere in preparation leaves
	// the payload available for the next app-server to recover.
	if c.harnessStore != nil && c.harnessStore.Dir() != "" {
		item, exists, err := c.harnessStore.GetQueueItem(prepared.WorkerID)
		if err != nil {
			return queuedSpawnRetryPending, fmt.Errorf("inspect queued spawn: %w", err)
		}
		if !exists {
			return queuedSpawnAlreadyClaimed, nil
		}
		terminalPending, err := c.workerTerminalFinalizationPending(prepared.WorkerID)
		if err != nil {
			return queuedSpawnRetryPending, fmt.Errorf("inspect queued spawn terminal evidence: %w", err)
		}
		if terminalPending {
			// Terminal evidence is a stronger fact than stale launch intent. Yield
			// the execution lease so the terminal recovery scanner can finalize and
			// consume this queue item; never start a second worker generation.
			return queuedSpawnTerminalPending, nil
		}
		if item.State == harness.QueueItemStateCancelling {
			// Cancellation intent became durable before its compensation. Never
			// launch this payload: finish rollback and terminal settlement while
			// still owning the same cross-process execution lease, then ack it.
			if rollbackErr := c.rollbackQueuedTerminalReliably(prepared, true); rollbackErr != nil {
				return queuedSpawnCancelled, rollbackErr
			}
			c.settleQueuedSpawnCancellation(prepared, time.Now().UTC())
			if ackErr := c.acknowledgeQueuedTerminalReliably(prepared.WorkerID, "cancellation", true); ackErr != nil {
				return queuedSpawnCancelled, ackErr
			}
			return queuedSpawnCancelled, nil
		}
		if item.State == harness.QueueItemStateFailing {
			failure := errors.New(strings.TrimSpace(item.Error))
			if strings.TrimSpace(item.Error) == "" {
				failure = errors.New("queued spawn launch failed before restart")
			}
			if rollbackErr := c.rollbackQueuedTerminalReliably(prepared, true); rollbackErr != nil {
				return queuedSpawnFailed, errors.Join(failure, rollbackErr)
			}
			c.settleSpawnLaunchFailure(prepared.WorkerID, failure)
			if ackErr := c.acknowledgeQueuedTerminalReliably(prepared.WorkerID, "failure", true); ackErr != nil {
				return queuedSpawnFailed, errors.Join(failure, ackErr)
			}
			return queuedSpawnFailed, failure
		}
	}
	// From this point through launch failure settlement or successful queue
	// acknowledgement, this process owns both the durable payload and the
	// worker execution lease. Keep every terminal transition inside that lease
	// so another app-server cannot observe the payload between release and ack.
	defer func() {
		if returnErr == nil || outcome == queuedSpawnStarted || outcome == queuedSpawnAlreadyClaimed || outcome == queuedSpawnRetryPending {
			return
		}
		failingDurable, markErr := c.markQueuedSpawnFailureReliably(prepared.WorkerID, returnErr)
		if markErr != nil {
			// No terminal tombstone was committed. Preserve the original launch
			// payload and let a future owner retry instead of hanging shutdown or
			// compensating state that still has runnable intent.
			outcome = queuedSpawnRetryPending
			returnErr = errors.Join(returnErr, markErr)
			return
		}
		outcome = queuedSpawnFailed
		if rollbackErr := c.rollbackQueuedTerminalReliably(prepared, failingDurable); rollbackErr != nil {
			returnErr = errors.Join(returnErr, rollbackErr)
			return
		}
		c.settleSpawnLaunchFailure(prepared.WorkerID, returnErr)
		if ackErr := c.acknowledgeQueuedTerminalReliably(prepared.WorkerID, "failure", failingDurable); ackErr != nil {
			returnErr = errors.Join(returnErr, ackErr)
		}
	}()
	parentRepo, worktrees := c.workspaceSnapshot()
	workerRoot := parentRepo
	var worktreeRef *worktree.Worktree
	if prepared.Isolation == IsolationWorktree {
		if worktrees == nil {
			return queuedSpawnStartUnknown, errors.New("isolation=worktree requires repository worktree support (this workspace does not support isolated worktrees)")
		}
		worktreeRef, err = worktrees.OpenOrCreate(worktree.OpenOrCreateOptions{
			SessionID:    c.sessionID,
			WorkerID:     prepared.WorkerID,
			BaseRepo:     prepared.BaseRepo,
			BaseRevision: prepared.BaseRevision,
		})
		if err != nil {
			return queuedSpawnStartUnknown, fmt.Errorf("worktree create: %w", err)
		}
		workerRoot = worktreeRef.Path
	}
	if running, ok := c.threads.UpdateStatus(prepared.WorkerID, agentthread.StatusRunning, time.Now().UTC()); ok {
		_ = c.threadStore.RecordStatus(running)
	}
	c.recordHarnessTaskStart(prepared.ThreadMeta, prepared.WorkerType.Name, prepared.Prompt, workerRoot, prepared.Isolation, prepared.BaseRepo)
	workerKit, err := c.workerFact(workerRoot, prepared.WorkerType, prepared.ThreadMeta)
	if err != nil {
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return queuedSpawnStartUnknown, fmt.Errorf("worker toolkit: %w", err)
	}
	prompt := prepared.Prompt
	systemPrompt, err := c.workerSystemPrompt(workerRoot, prepared.WorkerType, prepared.ThreadMeta, prepared.Isolation)
	if err != nil {
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return queuedSpawnStartUnknown, fmt.Errorf("worker system prompt: %w", err)
	}
	var initialHistory []providers.ChatMessage
	if prepared.IsFork {
		initialHistory = providers.CloneChatMessages(prepared.ParentHistory)
		initialHistory = withInitialSystemPrompt(initialHistory, systemPrompt)
		systemPrompt = ""
		if prepared.Isolation == IsolationWorktree {
			prompt = appendForkWorktreeReminder(prompt, workerRoot, prepared.Isolation)
		}
	}
	historyPath := ""
	if c.historyDir != "" {
		historyPath = filepath.Join(c.historyDir, prepared.WorkerID+".json")
	}
	participantID := prepared.ParticipantID
	if strings.TrimSpace(participantID) == "" {
		// Legacy queued payloads (persisted before participant identity
		// existed) get a fresh participant at start time.
		participantID = c.newEphemeralParticipant(prepared.ThreadMeta.TaskName, prepared.WorkerType).ID
	}
	// Re-resolve the requested model alias at launch so settings changes affect
	// queued work before it starts. A found alias uses its runtime; an unknown
	// alias falls back to the current default runtime and records the fallback;
	// an omitted alias with no legacy pin snapshots the current default runtime.
	aliasRuntime, aliasFallback, _, aliasErr := c.resolveModelAlias(prepared.ModelAlias)
	if aliasErr != nil {
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return queuedSpawnStartUnknown, fmt.Errorf("model alias: %w", aliasErr)
	}
	var spawnRuntime *subagent.WorkerRuntime
	modelAliasFallback := false
	if aliasRuntime != nil {
		spawnRuntime = aliasRuntime
	} else if aliasFallback {
		defaults := c.manager.DefaultWorkerRuntime()
		spawnRuntime = &defaults
		modelAliasFallback = true
	} else if strings.TrimSpace(prepared.ModelPin) == "" && strings.TrimSpace(prepared.ModelOverride) == "" && prepared.ClientOverride == nil {
		defaults := c.manager.DefaultWorkerRuntime()
		spawnRuntime = &defaults
	}

	// Per-participant model pin restore. Queued spawns persist the raw
	// pin (e.g. "alt-provider:model") but not the stream client, so we must
	// rebuild the client for any cross-provider pin before the runner picks
	// one; otherwise the subagent.Manager would fall back to defaults.client
	// and route the request to the wrong provider.
	modelOverride, clientOverride, providerOverride, err := c.resolveSpawnModelPin(fmt.Sprintf("queued spawn %s", prepared.WorkerID), prepared.ModelOverride, prepared.ModelPin, prepared.ClientOverride)
	if err != nil {
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return queuedSpawnStartUnknown, err
	}
	// Same report-settlement window as the direct spawn path: mark before
	// the queued run can start so await never consumes an unadjudicated
	// completion.
	if prepared.WorkerType.RequiresReport {
		c.markReportSettlementPending(prepared.WorkerID)
	}
	func() {
		finishLaunchAcknowledgement := c.beginQueuedLaunchAcknowledgement(prepared.WorkerID)
		defer finishLaunchAcknowledgement()
		// A zero-latency worker can publish a terminal notification before
		// Manager.Spawn returns. Hold its lease-release barrier until the durable
		// payload is acknowledged, so another app-server cannot recover and run
		// the same item in that handoff window.
		unlockLeaseRelease := c.lockWorkerExecutionRelease(prepared.WorkerID)
		defer unlockLeaseRelease()
		c.workerReleaseHookMu.Lock()
		beforeManagerSpawn := c.beforeQueuedManagerSpawnForTest
		c.workerReleaseHookMu.Unlock()
		if hook := beforeManagerSpawn; hook != nil {
			hook(prepared.WorkerID)
		}
		releaseTurnAdmission, admissionErr := c.beginWorkerTurn()
		if admissionErr != nil {
			err = admissionErr
			return
		}
		_, err = c.manager.Spawn(context.WithoutCancel(ctx), subagent.SpawnOptions{
			ID:                 prepared.WorkerID,
			ParticipantID:      participantID,
			Type:               prepared.WorkerType.Name,
			TaskName:           prepared.ThreadMeta.TaskName,
			AgentProfile:       prepared.ThreadMeta.AgentProfile,
			AgentPath:          prepared.ThreadMeta.Path,
			ParentID:           prepared.ThreadMeta.ParentID,
			Description:        prepared.Description,
			Prompt:             prompt,
			SystemPrompt:       systemPrompt,
			Toolkit:            workerKit,
			HistoryPath:        historyPath,
			WorkerRoot:         workerRoot,
			InitialHistory:     initialHistory,
			Model:              modelOverride,
			ModelPin:           prepared.ModelPin,
			ModelAlias:         prepared.ModelAlias,
			ModelAliasFallback: modelAliasFallback,
			WorkerRuntime:      spawnRuntime,
			Client:             clientOverride,
			ProviderName:       providerOverride,
		})
		// Manager.Spawn publishes the worker synchronously. Close turn admission
		// before storage acknowledgement so BeginShutdown can acquire its writer
		// lock, stop the published worker, and provide the durable terminal proof
		// that lets an unavailable queue acknowledgement yield safely.
		releaseTurnAdmission()
		if err == nil {
			if ackErr := c.acknowledgeLaunchedQueuedSpawnReliably(prepared.WorkerID); ackErr != nil {
				// The worker is running and this process still owns its execution
				// lease, so the stale runnable item cannot double-launch; the
				// terminal path or the next app-server consumes it.
				providers.DebugLogf("agentcontrol: queued spawn %s launched without queue acknowledgement: %v", prepared.WorkerID, ackErr)
			}
		}
	}()
	if err != nil {
		c.clearReportSettlement(prepared.WorkerID)
		if errors.Is(err, errAgentControlStopping) {
			if worktreeRef != nil {
				_ = worktrees.Cleanup(worktreeRef)
			}
			return queuedSpawnRetryPending, err
		}
		if worktreeRef != nil {
			_ = worktrees.Cleanup(worktreeRef)
		}
		return queuedSpawnStartUnknown, fmt.Errorf("spawn: %w", err)
	}
	// Manager.Spawn inserts a StatusRunning entry synchronously. The reliable
	// acknowledgement above completed while both the execution lease and its
	// fast-terminal release barrier were held.
	releaseLeaseOnReturn = false
	return queuedSpawnStarted, nil
}

func (c *AgentControl) recordHarnessTaskFailure(taskID string, err error) {
	if c == nil || c.harnessStore == nil {
		return
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	now := time.Now().UTC()
	runID := harnessRunID(taskID)
	_, _ = c.harnessStore.UpdateTaskStatus(taskID, harness.TaskStatusFailed, now, 0, 0, errText)
	_, _ = c.harnessStore.UpdateRunStatus(runID, harness.TaskStatusFailed, now, 0, 0, errText)
	_ = c.harnessStore.AppendEvent(harness.Event{
		Type:      harness.EventTaskStatusChanged,
		TaskID:    taskID,
		RunID:     runID,
		AgentID:   taskID,
		Status:    string(harness.TaskStatusFailed),
		Message:   errText,
		CreatedAt: now,
	})
	_ = c.harnessStore.AppendEvent(harness.Event{
		Type:      harness.EventRunCompleted,
		TaskID:    taskID,
		RunID:     runID,
		AgentID:   taskID,
		Status:    string(harness.TaskStatusFailed),
		Message:   errText,
		CreatedAt: now,
	})
	_ = c.recordAgentFailure(AgentFailure{
		Source:    "harness_task",
		TaskID:    taskID,
		RunID:     runID,
		AgentID:   taskID,
		Outcome:   string(harness.TaskStatusFailed),
		Message:   errText,
		CreatedAt: now,
	})
}

// settleSpawnLaunchFailure closes every durable lifecycle record created
// before manager.Spawn. Without this compensation a launch error leaves the
// child thread and parent edge permanently active even though no executor
// exists, which in turn prevents the root thread from being deleted.
func (c *AgentControl) settleSpawnLaunchFailure(workerID string, err error) {
	if c == nil {
		return
	}
	now := time.Now().UTC()
	if failed, ok := c.threads.UpdateStatus(workerID, agentthread.StatusFailed, now); ok {
		_ = c.threadStore.RecordStatus(failed)
	}
	c.recordHarnessTaskFailure(workerID, err)
	if closed, ok := c.threads.UpdateEdgeStatus(workerID, agentthread.EdgeClosed, now); ok {
		_ = c.threadStore.RecordEdgeStatus(closed)
	}
}

func (c *AgentControl) recordHarnessStatus(n subagent.Notification) {
	if c == nil || c.harnessStore == nil {
		return
	}
	// Lifecycle is owned by runtime facts: a loop that ended without error is
	// completed, unconditionally. A missing agent_report is a report-quality
	// concern (surfaced as report metadata), never a lifecycle status. The
	// self-reported outcome is recorded as data elsewhere but never overrides
	// the observed run status here.
	status := harnessStatusFromSubAgent(n.Status)
	errText := ""
	if n.Snapshot.Error != nil {
		errText = n.Snapshot.Error.Error()
	}
	if task, ok := c.harnessTask(n.AgentID); ok {
		if isActiveHarnessStatus(status) && isTerminalHarnessStatus(task.Status) {
			return
		}
		if isTerminalHarnessStatus(status) && task.Status == status && task.InputTokens == n.Snapshot.InputTokens && task.OutputTokens == n.Snapshot.OutputTokens && strings.TrimSpace(task.Error) == strings.TrimSpace(errText) {
			c.recordTerminalHarnessArtifacts(n)
			return
		}
	}
	completedAt := n.Snapshot.CompletedAt
	if isFinalSubAgentStatus(n.Status) && completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	runID := harnessRunID(n.AgentID)
	if _, err := c.harnessStore.UpdateTaskStatus(n.AgentID, status, completedAt, n.Snapshot.InputTokens, n.Snapshot.OutputTokens, errText); err == nil {
		_ = c.harnessStore.AppendEvent(harness.Event{
			Type:      harness.EventTaskStatusChanged,
			TaskID:    n.AgentID,
			RunID:     runID,
			AgentID:   n.AgentID,
			Path:      n.Snapshot.AgentPath,
			Status:    string(status),
			Message:   errText,
			CreatedAt: time.Now().UTC(),
		})
	}
	if _, err := c.harnessStore.UpdateRunStatus(runID, status, completedAt, n.Snapshot.InputTokens, n.Snapshot.OutputTokens, errText); err == nil && isFinalSubAgentStatus(n.Status) {
		_ = c.harnessStore.AppendEvent(harness.Event{
			Type:      harness.EventRunCompleted,
			TaskID:    n.AgentID,
			RunID:     runID,
			AgentID:   n.AgentID,
			Path:      n.Snapshot.AgentPath,
			Status:    string(status),
			Message:   errText,
			CreatedAt: time.Now().UTC(),
		})
	}
	if isFinalSubAgentStatus(n.Status) {
		c.recordTerminalHarnessArtifacts(n)
	}
}

func (c *AgentControl) recordTerminalHarnessArtifacts(n subagent.Notification) {
	if c == nil || !isFinalSubAgentStatus(n.Status) {
		return
	}
	c.recordAgentResultArtifact(n.Snapshot)
	c.recordWorktreeArtifacts(n.Snapshot)
	if n.Status == subagent.StatusCompleted {
		c.synthesizeFinalTextReport(n.Snapshot)
	}
}

// synthesizeFinalTextReport gives a completed run that filed no structured
// agent_report a report anyway, built from the facts the runtime already owns
// (final text, changed files, artifacts). The report is tagged final_text so
// consumers can tell a synthesized handoff apart from a structured one, and it
// gives the run a durable report path just like agent_report would.
func (c *AgentControl) synthesizeFinalTextReport(snap subagent.SubAgentSnapshot) {
	if c == nil || c.harnessStore == nil {
		return
	}
	taskID := strings.TrimSpace(snap.ID)
	if taskID == "" {
		return
	}
	if _, ok, err := c.harnessStore.ReportForTask(taskID); err != nil || ok {
		// A structured report was filed (or the store errored); leave it alone.
		return
	}
	summary := strings.TrimSpace(snap.Result)
	if summary == "" {
		summary = "Worker completed without a final message."
	}
	task, _ := c.harnessTask(taskID)
	changed := c.worktreeChangedFiles(task)
	_, artifacts := c.harnessReportForTask(taskID)
	report := harness.Report{
		ID:           taskID + "-final-text-report",
		TaskID:       taskID,
		RunID:        harnessRunID(taskID),
		AgentID:      taskID,
		AgentPath:    strings.TrimSpace(snap.AgentPath),
		Kind:         harness.ReportKindFinalText,
		Outcome:      "completed",
		Summary:      summary,
		ChangedFiles: changed,
		Artifacts:    artifacts,
		RawResult:    snap.Result,
		SubmittedAt:  time.Now().UTC(),
	}
	if _, err := c.harnessStore.SubmitReport(report); err != nil {
		return
	}
}

// worktreeChangedFiles returns the git-observed changed files for a worktree
// task, best effort. Shared-workspace tasks return nil because the runtime does
// not attribute repo-wide edits to a single inplace worker.
func (c *AgentControl) worktreeChangedFiles(task harness.Task) []string {
	if task.Workspace.Mode != harness.WorkspaceWorktree || strings.TrimSpace(task.Workspace.Root) == "" {
		return nil
	}
	out, err := gitOutput(task.Workspace.Root, "status", "--porcelain")
	if err != nil {
		return nil
	}
	changed := make([]string, 0)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		changed = append(changed, fields[len(fields)-1])
	}
	if len(changed) == 0 {
		return nil
	}
	return changed
}

func (c *AgentControl) harnessReportForTask(taskID string) (string, []string) {
	if c == nil || c.harnessStore == nil {
		return "", nil
	}
	var taskArtifactPaths []string
	tasks, err := c.harnessStore.ListTasks()
	if err == nil {
		for _, task := range tasks {
			if task.ID == taskID {
				taskArtifactPaths = append(taskArtifactPaths, task.ArtifactPaths...)
				break
			}
		}
	}
	report, ok, err := c.harnessStore.ReportForTask(taskID)
	if err == nil && ok {
		paths := append([]string(nil), report.Artifacts...)
		reportPath := strings.TrimSpace(report.ReportPath)
		for _, path := range taskArtifactPaths {
			if path != "" && !stringSliceContains(paths, path) {
				paths = append(paths, path)
			}
		}
		if reportPath == "" {
			reportPath = resultArtifactPath(paths)
		}
		if reportPath != "" && !stringSliceContains(paths, reportPath) {
			paths = append(paths, reportPath)
		}
		return reportPath, paths
	}
	return resultArtifactPath(taskArtifactPaths), taskArtifactPaths
}

func (c *AgentControl) recordWorktreeArtifacts(snap subagent.SubAgentSnapshot) {
	if c == nil || c.harnessStore == nil {
		return
	}
	task, ok := c.harnessTask(snap.ID)
	if !ok || task.Workspace.Mode != harness.WorkspaceWorktree || strings.TrimSpace(task.Workspace.Root) == "" {
		return
	}
	root := task.Workspace.Root
	statusOut, err := gitOutput(root, "status", "--porcelain")
	if err != nil || strings.TrimSpace(statusOut) == "" {
		return
	}
	artifactDir := filepath.Join(c.harnessDir, "artifacts", snap.ID)
	if err := os.MkdirAll(artifactDir, securefs.DirMode); err != nil {
		return
	}
	statusPath := filepath.Join(artifactDir, "worktree-status.txt")
	if err := os.WriteFile(statusPath, []byte(statusOut), securefs.FileMode); err == nil {
		_ = c.harnessStore.AddArtifact(harness.Artifact{
			ID:        snap.ID + "-worktree-status",
			TaskID:    snap.ID,
			RunID:     harnessRunID(snap.ID),
			Kind:      harness.ArtifactEvidence,
			Path:      statusPath,
			Summary:   "worktree status",
			CreatedAt: time.Now().UTC(),
		})
	}
	patchOut, err := gitOutput(root, "diff", "--binary", "HEAD", "--")
	if err == nil && strings.TrimSpace(patchOut) != "" {
		patchPath := filepath.Join(artifactDir, "changes.patch")
		if err := os.WriteFile(patchPath, []byte(patchOut), securefs.FileMode); err == nil {
			_ = c.harnessStore.AddArtifact(harness.Artifact{
				ID:        snap.ID + "-patch",
				TaskID:    snap.ID,
				RunID:     harnessRunID(snap.ID),
				Kind:      harness.ArtifactPatch,
				Path:      patchPath,
				Summary:   "worktree diff against base HEAD",
				CreatedAt: time.Now().UTC(),
			})
		}
	}
	untracked, err := gitUntrackedFiles(root)
	if err != nil || len(untracked) == 0 {
		return
	}
	manifestPath := filepath.Join(artifactDir, "untracked-files.txt")
	if err := os.WriteFile(manifestPath, []byte(strings.Join(untracked, "\n")+"\n"), securefs.FileMode); err == nil {
		_ = c.harnessStore.AddArtifact(harness.Artifact{
			ID:        snap.ID + "-untracked-manifest",
			TaskID:    snap.ID,
			RunID:     harnessRunID(snap.ID),
			Kind:      harness.ArtifactManifest,
			Path:      manifestPath,
			Summary:   "untracked files created by worktree task",
			CreatedAt: time.Now().UTC(),
		})
	}
	archivePath := filepath.Join(artifactDir, "untracked-files.tar")
	if err := writeUntrackedArchive(root, archivePath, untracked); err == nil {
		_ = c.harnessStore.AddArtifact(harness.Artifact{
			ID:        snap.ID + "-untracked-archive",
			TaskID:    snap.ID,
			RunID:     harnessRunID(snap.ID),
			Kind:      harness.ArtifactArchive,
			Path:      archivePath,
			Summary:   "archive of untracked files created by worktree task",
			CreatedAt: time.Now().UTC(),
		})
	}
}

func (c *AgentControl) harnessTask(taskID string) (harness.Task, bool) {
	if c == nil || c.harnessStore == nil {
		return harness.Task{}, false
	}
	tasks, err := c.harnessStore.ListTasks()
	if err != nil {
		return harness.Task{}, false
	}
	for _, task := range tasks {
		if task.ID == taskID {
			return task, true
		}
	}
	return harness.Task{}, false
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func gitUntrackedFiles(dir string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(out, []byte{0})
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(string(part))
		if name == "" {
			continue
		}
		if filepath.IsAbs(name) || name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
			continue
		}
		files = append(files, filepath.ToSlash(name))
	}
	return files, nil
}

func writeUntrackedArchive(root, archivePath string, files []string) error {
	if err := os.MkdirAll(filepath.Dir(archivePath), securefs.DirMode); err != nil {
		return err
	}
	out, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, securefs.FileMode)
	if err != nil {
		return err
	}
	defer out.Close()
	tw := tar.NewWriter(out)
	defer tw.Close()
	for _, rel := range files {
		cleanRel := filepath.Clean(rel)
		if cleanRel == "." || filepath.IsAbs(cleanRel) || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
			continue
		}
		absPath := filepath.Join(root, cleanRel)
		info, err := os.Lstat(absPath)
		if err != nil || info.IsDir() {
			continue
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, _ = os.Readlink(absPath)
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(cleanRel)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		in, err := os.Open(absPath)
		if err != nil {
			return err
		}
		if _, err := io.Copy(tw, in); err != nil {
			in.Close()
			return err
		}
		if err := in.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (c *AgentControl) consumeWorkerStatus(ch <-chan subagent.Notification) {
	for {
		select {
		case n := <-ch:
			// Terminal bookkeeping is synchronous through the manager's reliable
			// observer. Re-consuming the lossy copy can race a report-closing
			// follow-up and incorrectly settle the original completion.
			if isFinalSubAgentStatus(n.Status) {
				continue
			}
			// Serialize active notifications with the reliable terminal observer.
			// Otherwise a delayed running notification can pass the terminal guard,
			// block on the harness store, and overwrite the terminal status after
			// the observer has already persisted the report and released awaiters.
			unlockTransition := c.lockWorkerTransition(n.AgentID)
			if _, err := c.consumeWorkerNotification(n); err != nil {
				providers.DebugLogf("agentcontrol: consume worker %s status %s: %v", n.AgentID, n.Status, err)
			}
			unlockTransition()
		case <-c.statusStop:
			return
		}
	}
}

func (c *AgentControl) consumeWorkerTerminal(n subagent.Notification) error {
	c.workerReleaseHookMu.Lock()
	beforeTransition := c.beforeWorkerTerminalTransitionForTest
	c.workerReleaseHookMu.Unlock()
	if beforeTransition != nil {
		beforeTransition(n.AgentID)
	}
	unlockTransition := c.lockWorkerTransition(n.AgentID)
	defer unlockTransition()
	if outcome := c.finalizeWorkerTerminalWithAck(n); outcome == workerTerminalContinued {
		return nil
	}
	c.workerReleaseHookMu.Lock()
	hook := c.beforeWorkerExecutionReleaseForTest
	c.workerReleaseHookMu.Unlock()
	if hook != nil {
		hook(n.AgentID)
	}
	c.releaseWorkerExecution(n.AgentID)
	c.cleanupRetiringWorkerTerminalFinalizers()
	return nil
}

func (c *AgentControl) consumeWorkerNotification(n subagent.Notification) (workerNotificationOutcome, error) {
	if c == nil || c.threads == nil {
		return workerNotificationSettled, nil
	}
	status := threadStatusFromSubAgent(n.Status)
	if current, ok := c.threads.Resolve(n.AgentID); ok {
		if isActiveAgentThreadStatus(status) && isFinalAgentThreadStatus(current.Status) {
			return workerNotificationSettled, nil
		}
	} else if isFinalSubAgentStatus(n.Status) {
		// A prepared terminal intent may be the only surviving authority after a
		// crash before the thread registry/store observed the child. Rebuild the
		// minimum metadata projection before replaying the terminal transaction.
		meta := metadataFromSnapshot(n.Snapshot)
		meta.SessionID = c.sessionID
		if strings.TrimSpace(meta.ID) == "" {
			meta.ID = strings.TrimSpace(n.AgentID)
		}
		if strings.TrimSpace(meta.Path) == "" {
			meta.Path = agentthread.RootPath + "/" + meta.ID
		}
		if err := c.threads.Restore(meta); err != nil {
			return workerNotificationSettled, fmt.Errorf("restore terminal worker thread: %w", err)
		}
		if err := c.threadStore.UpsertThread(meta); err != nil {
			return workerNotificationSettled, fmt.Errorf("persist restored terminal worker thread: %w", err)
		}
	}
	// A requires_report worker that completed without filing agent_report
	// gets one mechanical closing turn before its completion is recorded or
	// delivered. Returning here leaves the thread status untouched, so the
	// closing turn's own completion passes the dedupe guards above and
	// flows through this consumer normally.
	if n.Status == subagent.StatusCompleted && c.maybeNudgeReportClosing(n) {
		return workerNotificationContinued, nil
	}
	// waiting_children: a parent that produced its final message while
	// direct children are still live holds that result instead of delivering
	// it. Deferred keeps the durable terminal record, so recovery re-drives
	// the hold after a crash; in-process, each child's own delivery wakes
	// the parent through Followup and the integrated completion re-enters
	// here with no live children left.
	if n.Status == subagent.StatusCompleted && c.parkWaitingChildren(n) {
		return workerNotificationDeferred, nil
	}
	if isFinalSubAgentStatus(n.Status) {
		if err := c.ensureTerminalHarnessProjection(n); err != nil {
			return workerNotificationSettled, err
		}
	}
	c.recordHarnessStatus(n)
	if isFinalSubAgentStatus(n.Status) {
		task, ok := c.harnessTask(n.AgentID)
		want := harnessStatusFromSubAgent(n.Status)
		if c.harnessStore != nil && c.harnessStore.Dir() != "" && (!ok || task.Status != want) {
			return workerNotificationSettled, fmt.Errorf("terminal harness task %s status = %q, want %q", n.AgentID, task.Status, want)
		}
		if n.Status == subagent.StatusCompleted && c.harnessStore != nil && c.harnessStore.Dir() != "" {
			if _, ok, err := c.harnessStore.ReportForTask(n.AgentID); err != nil {
				return workerNotificationSettled, fmt.Errorf("read terminal report: %w", err)
			} else if !ok {
				return workerNotificationSettled, fmt.Errorf("terminal report for %s was not persisted", n.AgentID)
			}
		}
		// The terminal state (and, for completed runs, the structured or
		// synthesized report) is durably recorded: settlement is decided,
		// awaiting parents may consume this run now.
		c.clearReportSettlement(n.AgentID)
	}
	meta, ok := c.threads.UpdateStatus(n.AgentID, status, time.Now().UTC())
	if !ok {
		return workerNotificationSettled, fmt.Errorf("worker thread %s is not registered", n.AgentID)
	}
	if err := c.threadStore.RecordStatus(meta); err != nil {
		return workerNotificationSettled, fmt.Errorf("persist worker thread status: %w", err)
	}
	if isFinalSubAgentStatus(n.Status) {
		if err := c.commitDurablyAppliedNestedResults(n.AgentID); err != nil {
			return workerNotificationSettled, fmt.Errorf("commit nested results applied by %s: %w", n.AgentID, err)
		}
		if _, err := c.ensureAgentResultDelivery(n.Snapshot); err != nil {
			return workerNotificationSettled, fmt.Errorf("persist result-ready for %s: %w", n.AgentID, err)
		}
		deliveryCtx, cancelDelivery := context.WithTimeout(context.Background(), nestedResultDeliveryWait)
		delivered := c.deliverNestedResultToParent(deliveryCtx, n.Snapshot)
		cancelDelivery()
		if !delivered {
			if c.isRootChildSnapshot(n.Snapshot) {
				// Root results have no parent worker to claim them: the live
				// session consumes them through Wait or the recorded
				// communication, so the terminal settles now.
				if err := c.threadStore.RecordCommunication(c.rootThreadID, c.newAgentCompletionCommunication(n.Snapshot, agentthread.RootPath)); err != nil {
					return workerNotificationSettled, fmt.Errorf("persist root completion communication: %w", err)
				}
			} else if workerID := firstNonEmptyString(strings.TrimSpace(n.AgentID), strings.TrimSpace(n.Snapshot.ID)); workerTerminalFinalizationPath(c.harnessDir, workerID) != "" {
				// A busy parent (including one blocked in Wait on this very
				// terminal transition) cannot claim yet. Defer: the durable
				// terminal record and result-ready entry hand delivery to
				// terminal recovery instead of this observer spinning on it.
				return workerNotificationDeferred, nil
			}
		}
		go c.maybeStartQueued(context.Background())
	}
	return workerNotificationSettled, nil
}

// ensureTerminalHarnessProjection reconstructs task/run rows when a prepared
// terminal intent is the only surviving run authority. Queue admission and
// terminal preparation are durable before launch/snapshot publication, so a
// crash may legitimately leave the terminal record and queue payload without
// the later harness projection. Recovery must build that projection before it
// can apply the terminal status; otherwise it retries forever on a missing row.
func (c *AgentControl) ensureTerminalHarnessProjection(n subagent.Notification) error {
	if c == nil || c.harnessStore == nil || c.harnessStore.Dir() == "" {
		return nil
	}
	workerID := strings.TrimSpace(n.AgentID)
	if workerID == "" {
		workerID = strings.TrimSpace(n.Snapshot.ID)
	}
	meta, ok := c.threads.Resolve(workerID)
	if !ok {
		return fmt.Errorf("terminal worker thread %s is not registered", workerID)
	}

	role := strings.TrimSpace(n.Snapshot.Type)
	intent := strings.TrimSpace(n.Snapshot.Description)
	workerRoot := strings.TrimSpace(n.Snapshot.WorkerRoot)
	baseRepo := ""
	workspaceMode := harness.WorkspaceShared
	if item, exists, err := c.harnessStore.GetQueueItem(workerID); err != nil {
		return fmt.Errorf("read terminal worker queue projection: %w", err)
	} else if exists {
		var payload queuedSpawnPayload
		if err := json.Unmarshal(item.Payload, &payload); err == nil {
			role = firstNonEmptyString(role, payload.WorkerType)
			intent = firstNonEmptyString(intent, payload.Prompt)
			baseRepo = strings.TrimSpace(payload.BaseRepo)
			if IsolationMode(payload.Isolation) == IsolationWorktree {
				workspaceMode = harness.WorkspaceWorktree
				if workerRoot == "" && strings.TrimSpace(c.worktreeRoot) != "" {
					workerRoot = filepath.Join(c.worktreeRoot, c.sessionID, workerID)
				}
			}
		}
	}
	if workerRoot == "" {
		workerRoot = c.ParentRepo()
	}
	if role == "" {
		role = meta.Role
	}
	if intent == "" {
		intent = meta.LastTaskMessage
	}

	now := time.Now().UTC()
	startedAt := n.Snapshot.StartedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	runID := harnessRunID(workerID)
	if _, exists := c.harnessTask(workerID); !exists {
		task := harness.Task{
			ID:         workerID,
			SessionID:  c.sessionID,
			ParentID:   meta.ParentID,
			ParentPath: meta.Source.ParentPath,
			Path:       meta.Path,
			Name:       meta.TaskName,
			Role:       role,
			Intent:     intent,
			Workspace: harness.WorkspaceLease{
				Mode:      workspaceMode,
				Root:      workerRoot,
				BaseRepo:  baseRepo,
				CreatedAt: startedAt,
			},
			Status:     harness.TaskStatusRunning,
			LastRunID:  runID,
			CardItemID: taskCardItemID(workerID),
			CreatedAt:  startedAt,
			UpdatedAt:  now,
			StartedAt:  startedAt,
		}
		if err := c.harnessStore.UpsertTask(task); err != nil {
			return fmt.Errorf("restore terminal harness task: %w", err)
		}
	}
	runs, err := c.harnessStore.ListRuns()
	if err != nil {
		return fmt.Errorf("list terminal harness runs: %w", err)
	}
	for _, run := range runs {
		if run.ID == runID {
			return nil
		}
	}
	if err := c.harnessStore.UpsertRun(harness.AgentRun{
		ID:        runID,
		TaskID:    workerID,
		AgentID:   workerID,
		Role:      role,
		Model:     n.Snapshot.Model,
		Status:    harness.TaskStatusRunning,
		StartedAt: startedAt,
	}); err != nil {
		return fmt.Errorf("restore terminal harness run: %w", err)
	}
	return nil
}

func (c *AgentControl) recordWorkerResumed(snap subagent.SubAgentSnapshot) {
	if c == nil {
		return
	}
	switch snap.Status {
	case subagent.StatusRunning, subagent.StatusPending, subagent.StatusQueued:
	default:
		return
	}
	if meta, ok := c.threads.Resolve(snap.ID); ok {
		if workerType, err := LookupWorkerType(meta.Role); err == nil && workerType.RequiresReport {
			c.markReportSettlementPending(snap.ID)
		}
	}
	c.recordHarnessResume(snap)
	if meta, ok := c.threads.UpdateStatus(snap.ID, agentthread.StatusRunning, time.Now().UTC()); ok {
		_ = c.threadStore.RecordStatus(meta)
	}
}

func (c *AgentControl) recordHarnessResume(snap subagent.SubAgentSnapshot) {
	if c == nil || c.harnessStore == nil {
		return
	}
	now := time.Now().UTC()
	if task, ok := c.harnessTask(snap.ID); ok {
		task.Status = harness.TaskStatusRunning
		task.UpdatedAt = now
		task.CompletedAt = time.Time{}
		task.InputTokens = snap.InputTokens
		task.OutputTokens = snap.OutputTokens
		task.Error = ""
		_ = c.harnessStore.UpsertTask(task)
	}
	runID := harnessRunID(snap.ID)
	if runs, err := c.harnessStore.ListRuns(); err == nil {
		for _, run := range runs {
			if run.ID != runID {
				continue
			}
			run.Status = harness.TaskStatusRunning
			run.CompletedAt = time.Time{}
			run.InputTokens = snap.InputTokens
			run.OutputTokens = snap.OutputTokens
			run.Error = ""
			_ = c.harnessStore.UpsertRun(run)
			break
		}
	}
	_ = c.harnessStore.AppendEvent(harness.Event{
		Type:      harness.EventTaskStatusChanged,
		TaskID:    snap.ID,
		RunID:     runID,
		AgentID:   snap.ID,
		Path:      snap.AgentPath,
		Status:    string(harness.TaskStatusRunning),
		CreatedAt: now,
	})
}

// markReportSettlementPending records that a requires_report run owned by
// this process still awaits its completion adjudication. Set before the run
// (or its closing turn) starts so an awaiting parent can never observe a
// terminal snapshot in the window before the notification consumer decides
// what to do with it.
func (c *AgentControl) markReportSettlementPending(workerID string) {
	if c == nil {
		return
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return
	}
	c.reportSettleMu.Lock()
	if c.reportUnsettled == nil {
		c.reportUnsettled = make(map[string]struct{})
	}
	c.reportUnsettled[workerID] = struct{}{}
	c.reportSettleMu.Unlock()
}

// clearReportSettlement marks a run's completion adjudication as done:
// either its terminal notification was recorded (structured report accepted,
// synthesized final_text report written, or a failed/cancelled run recorded
// as such), or the run never started. Idempotent.
func (c *AgentControl) clearReportSettlement(workerID string) {
	if c == nil {
		return
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return
	}
	c.reportSettleMu.Lock()
	delete(c.reportUnsettled, workerID)
	c.reportSettleMu.Unlock()
}

// reportSettlementPending reports whether a requires_report run started by
// this process is still between "the manager flipped its snapshot terminal"
// and "the notification consumer recorded that terminal state". await treats
// a completed result in that window as still active. Runs from previous
// processes are never pending here, so cross-restart awaits settle
// immediately on the persisted facts.
func (c *AgentControl) reportSettlementPending(workerID string) bool {
	if c == nil {
		return false
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return false
	}
	c.reportSettleMu.Lock()
	_, pending := c.reportUnsettled[workerID]
	c.reportSettleMu.Unlock()
	return pending
}

// reportClosingNudge is the mechanical closing instruction sent to a
// requires_report worker that finished without filing agent_report. The
// accompanying turn pins its first request to agent_report via forced
// tool_choice, so compliance is a wire constraint rather than a memory test.
const reportClosingNudge = "You completed the task without submitting your structured handoff. " +
	"Call agent_report now with your outcome, summary, changed files, blockers, and evidence. " +
	"After the report is accepted, finish with a one-line confirmation."

// maybeNudgeReportClosing gives a requires_report worker that completed
// without filing agent_report ONE closing turn, with the request pinned to
// agent_report. It returns true when the closing turn was started — the
// caller then skips status recording and result delivery for this
// notification; the closing turn's own completion flows through normally.
// If the worker still files nothing, the second completion finds the run
// already nudged and completes with a synthesized final_text report —
// never a second nudge, never a lifecycle state.
func (c *AgentControl) maybeNudgeReportClosing(n subagent.Notification) bool {
	if c == nil || c.isStopping() || c.manager == nil || c.harnessStore == nil || c.threads == nil {
		return false
	}
	meta, ok := c.threads.Resolve(n.AgentID)
	if !ok {
		return false
	}
	wt, err := LookupWorkerType(meta.Role)
	if err != nil || !wt.RequiresReport {
		return false
	}
	if _, ok, err := c.harnessStore.ReportForTask(n.AgentID); err != nil || ok {
		return false
	}
	c.reportNudgeMu.Lock()
	if _, done := c.reportNudged[n.AgentID]; done {
		c.reportNudgeMu.Unlock()
		return false
	}
	if c.reportNudged == nil {
		c.reportNudged = make(map[string]struct{})
	}
	c.reportNudged[n.AgentID] = struct{}{}
	c.reportNudgeMu.Unlock()
	releaseTurnAdmission, admissionErr := c.beginWorkerTurn()
	if admissionErr != nil {
		return false
	}
	_, followupErr := c.manager.FollowupForcingTool(context.Background(), n.AgentID, reportClosingNudge, "agent_report")
	releaseTurnAdmission()
	if followupErr != nil {
		providers.DebugLogf("agentcontrol: report closing nudge for %s failed: %v", n.AgentID, followupErr)
		// The caller falls through to normal recording, which settles the run.
		return false
	}
	c.workerReleaseHookMu.Lock()
	afterFollowup := c.afterReportClosingFollowupForTest
	c.workerReleaseHookMu.Unlock()
	if afterFollowup != nil {
		afterFollowup(n.AgentID)
	}
	// The closing turn is in flight; keep (re-assert) the settlement window
	// until its own terminal notification is recorded.
	c.markReportSettlementPending(n.AgentID)
	return true
}

type nestedResultDeliveryAttempt struct {
	done      chan struct{}
	delivered bool
}

// nestedResultDeliveryWait bounds nested-result deliveries that run without a
// caller context, so a wedged parent terminal transition fails visibly instead
// of hanging its consumer; the unclaimed result-ready ledger entry survives
// the timeout for the next delivery owner.
const nestedResultDeliveryWait = 30 * time.Second

func (c *AgentControl) deliverNestedResultToParent(ctx context.Context, snap subagent.SubAgentSnapshot) (delivered bool) {
	if c == nil || c.isStopping() || c.manager == nil {
		return false
	}
	// A frozen tree must not wake parents: the result stays on the durable
	// ledger and the next user turn's snapshot becomes its consumer
	// (ResolveFrozenWorkerTree).
	if c.treeFrozen.Load() {
		return false
	}
	parentID := strings.TrimSpace(snap.ParentID)
	if parentID == "" || parentID == c.sessionID || parentID == c.rootThreadID {
		return false
	}
	stableResultID := agentResultDeliveryID(snap)
	if stableResultID == "" {
		return false
	}
	attempt, owner, alreadyDelivered := c.beginNestedResultDelivery(ctx, stableResultID)
	if alreadyDelivered {
		return true
	}
	if !owner {
		return false
	}
	defer func() {
		c.finishNestedResultDelivery(stableResultID, attempt, delivered)
	}()
	resultID, claimed, consumedBy, claimErr := c.claimAgentResultDelivery(snap, agentResultConsumerNestedPending)
	if claimErr != nil {
		providers.DebugLogf("agentcontrol: reserve nested result %s for parent inbox: %v", snap.ID, claimErr)
		return false
	}
	if resultID == "" {
		return false
	}
	if !claimed && consumedBy != agentResultConsumerNestedPending {
		// Another delivery path already committed the result. Match the old
		// single-consumer behavior and do not send a second copy to the parent.
		return consumedBy != ""
	}
	parentBefore, newParentLease, unlockTransition, leaseErr := c.prepareWorkerFollowup(ctx, parentID)
	if leaseErr != nil {
		providers.DebugLogf("agentcontrol: acquire parent worker %s for nested result: %v", parentID, leaseErr)
		return false
	}
	defer unlockTransition()
	releaseNewParentLease := func() {
		if newParentLease {
			c.releaseWorkerExecution(parentID)
		}
	}
	applied, appliedErr := c.parentPersistedRunContainsResult(parentID, resultID)
	if appliedErr != nil {
		releaseNewParentLease()
		providers.DebugLogf("agentcontrol: inspect parent %s durable inbox application: %v", parentID, appliedErr)
		return false
	}
	if applied {
		transitioned, currentConsumer, transitionErr := c.transitionAgentResultDeliveryConsumer(resultID, agentResultConsumerNestedPending, agentResultConsumerNestedFollowup)
		releaseNewParentLease()
		if transitionErr != nil {
			providers.DebugLogf("agentcontrol: commit nested result %s after parent application: %v", snap.ID, transitionErr)
			return false
		}
		return transitioned || currentConsumer == agentResultConsumerNestedFollowup
	}
	parentPath := parentPathForSnapshot(snap)
	if meta, ok := c.threads.Resolve(parentID); ok && strings.TrimSpace(meta.Path) != "" {
		parentPath = meta.Path
	}
	communication := c.newAgentCompletionCommunication(snap, parentPath)
	if _, err := c.threadStore.RecordResultCommunication(parentID, resultID, communication); err != nil {
		releaseNewParentLease()
		providers.DebugLogf("agentcontrol: persist nested result %s parent inbox: %v", snap.ID, err)
		return false
	}
	c.workerReleaseHookMu.Lock()
	beforeNestedFollowup := c.beforeNestedResultFollowupForTest
	c.workerReleaseHookMu.Unlock()
	if beforeNestedFollowup != nil {
		if err := beforeNestedFollowup(resultID); err != nil {
			releaseNewParentLease()
			providers.DebugLogf("agentcontrol: nested result %s followup gate: %v", snap.ID, err)
			return false
		}
	}
	releaseTurnAdmission, admissionErr := c.beginWorkerTurn()
	if admissionErr != nil {
		releaseNewParentLease()
		return false
	}
	resumed, err := c.manager.Followup(ctx, parentID, communication.String())
	releaseTurnAdmission()
	if err != nil {
		releaseNewParentLease()
	}
	if err == nil {
		if isFinalSubAgentStatus(parentBefore.Status) {
			c.recordWorkerResumed(resumed)
		}
	}
	return err == nil
}

func (c *AgentControl) beginNestedResultDelivery(ctx context.Context, resultID string) (*nestedResultDeliveryAttempt, bool, bool) {
	if c == nil || strings.TrimSpace(resultID) == "" {
		return nil, false, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resultID = strings.TrimSpace(resultID)
	for {
		if c.isStopping() {
			return nil, false, false
		}
		c.nestedDeliveryMu.Lock()
		if c.nestedDeliveries == nil {
			c.nestedDeliveries = make(map[string]*nestedResultDeliveryAttempt)
		}
		attempt := c.nestedDeliveries[resultID]
		if attempt == nil {
			attempt = &nestedResultDeliveryAttempt{done: make(chan struct{})}
			c.nestedDeliveries[resultID] = attempt
			c.nestedDeliveryMu.Unlock()
			return attempt, true, false
		}
		if attempt.delivered {
			c.nestedDeliveryMu.Unlock()
			return attempt, false, true
		}
		done := attempt.done
		c.nestedDeliveryMu.Unlock()
		c.workerReleaseHookMu.Lock()
		waitHook := c.nestedResultDeliveryWaitForTest
		c.workerReleaseHookMu.Unlock()
		if waitHook != nil {
			waitHook(resultID)
		}
		select {
		case <-done:
			// Success remains in the map and is returned above. Failure removes
			// the entry before closing done, so one waiter wins the next attempt.
		case <-ctx.Done():
			return nil, false, false
		}
	}
}

func (c *AgentControl) finishNestedResultDelivery(resultID string, attempt *nestedResultDeliveryAttempt, delivered bool) {
	if c == nil || attempt == nil {
		return
	}
	c.nestedDeliveryMu.Lock()
	defer c.nestedDeliveryMu.Unlock()
	resultID = strings.TrimSpace(resultID)
	if c.nestedDeliveries[resultID] != attempt {
		return
	}
	attempt.delivered = delivered
	if !delivered {
		delete(c.nestedDeliveries, resultID)
	}
	close(attempt.done)
}

// parkWaitingChildren holds a completed worker whose direct children are
// still live. Returns false when there is nothing to wait for, so the
// caller proceeds with normal terminal delivery.
func (c *AgentControl) parkWaitingChildren(n subagent.Notification) bool {
	if c == nil {
		return false
	}
	workerID := firstNonEmptyString(strings.TrimSpace(n.AgentID), strings.TrimSpace(n.Snapshot.ID))
	if workerID == "" || !c.hasLiveDirectChildren(workerID) {
		return false
	}
	if c.manager != nil {
		c.manager.MarkWaitingChildren(workerID)
	}
	if meta, ok := c.threads.UpdateStatus(workerID, agentthread.StatusWaitingChildren, time.Now().UTC()); ok {
		if err := c.threadStore.RecordStatus(meta); err != nil {
			providers.DebugLogf("agentcontrol: persist waiting_children thread status for %s: %v", workerID, err)
		}
	}
	return true
}

// hasLiveDirectChildren reports whether any direct child of the worker is
// still non-terminal on an open edge. waiting_children children count as
// live: their own held results have not integrated yet.
func (c *AgentControl) hasLiveDirectChildren(workerID string) bool {
	if c == nil || c.threads == nil {
		return false
	}
	for _, meta := range c.threads.List() {
		if strings.TrimSpace(meta.ParentID) != workerID {
			continue
		}
		if meta.Source.EdgeStatus == agentthread.EdgeClosed {
			continue
		}
		switch meta.Status {
		case agentthread.StatusPending, agentthread.StatusRunning, agentthread.StatusWaitingChildren:
			return true
		}
	}
	return false
}

func (c *AgentControl) isRootChildSnapshot(snap subagent.SubAgentSnapshot) bool {
	parentID := strings.TrimSpace(snap.ParentID)
	if parentID == "" || parentID == c.sessionID || parentID == c.rootThreadID {
		return true
	}
	// The root agent's toolkit identity is not always the session id (an
	// embedded toolkit may register the root as "root"). When the named
	// parent is not a worker or registered thread this control could ever
	// deliver to, a direct child of the root path completes to the root
	// thread instead of waiting forever on an impossible parent delivery.
	// A resolvable parent keeps the nested pending-retry semantics.
	if parentPathForSnapshot(snap) != agentthread.RootPath {
		return false
	}
	if c.manager != nil && c.manager.Get(parentID) != nil {
		return false
	}
	if c.threads != nil {
		if _, ok := c.threads.Resolve(parentID); ok {
			return false
		}
	}
	return true
}

func (c *AgentControl) newAgentCompletionCommunication(snap subagent.SubAgentSnapshot, recipientPath string) agentthread.InterAgentCommunication {
	reportPath, artifacts := c.harnessReportForTask(snap.ID)
	return newAgentCompletionCommunicationWithMessage(snap, recipientPath, c.agentMailboxMessageWithRefs(snap, reportPath, artifacts))
}

// AgentCompletionChatMessage returns the user-role handoff that should resume
// the recipient agent after a child agent finishes.
func (c *AgentControl) AgentCompletionChatMessage(snap subagent.SubAgentSnapshot, recipientPath string) providers.ChatMessage {
	if _, err := c.ensureAgentResultDelivery(snap); err != nil {
		providers.DebugLogf("agentcontrol: persist completion handoff result-ready for %s: %v", snap.ID, err)
	}
	reportPath, artifacts := c.harnessReportForTask(snap.ID)
	communication := newAgentCompletionCommunicationWithMessageAndTrigger(
		snap,
		recipientPath,
		c.agentMailboxMessageWithRefs(snap, reportPath, artifacts),
		true,
	)
	if warnings := c.CompletionOverlapWarnings(snap); len(warnings) > 0 {
		// The deleted await_agents tool used to surface changed-file overlaps
		// only when a parent explicitly joined multiple agents. Relocate that
		// value onto the completion wakeup so the resumed parent still sees
		// sibling agents that wrote the same files before it synthesizes.
		// Carry as a structured sibling on the envelope so the channel payload
		// stays a single JSON object (no text-after-JSON tail); see
		// agentthread.InterAgentCommunication.ChangedFileOverlap.
		communication.ChangedFileOverlap = warnings
	}
	return providers.ChatMessage{
		Role:    "user",
		Name:    wuucontext.AgentNotificationMessageName,
		Content: communication.String(),
	}
}

func (c *AgentControl) AgentMailboxMessage(snap subagent.SubAgentSnapshot) AgentMailboxMessage {
	reportPath, artifacts := c.harnessReportForTask(snap.ID)
	return c.agentMailboxMessageWithRefs(snap, reportPath, artifacts)
}

func (c *AgentControl) agentMailboxMessageWithRefs(snap subagent.SubAgentSnapshot, reportPath string, artifacts []string) AgentMailboxMessage {
	ref := c.AgentResultReference(snap)
	return NewAgentMailboxMessageWithReportAndResult(
		snap,
		c.sessionArtifactRef(reportPath),
		c.reportKindForTask(snap.ID),
		c.sessionArtifactRefs(artifacts),
		ref,
	)
}

// reportKindForTask reports how a run's report was produced: structured when
// the agent filed one via agent_report, otherwise final_text (a synthesized
// handoff, or none yet). Callers derive report_missing as kind != structured.
func (c *AgentControl) reportKindForTask(taskID string) harness.ReportKind {
	if report, ok := c.harnessReportDetailsForTask(taskID); ok {
		return harness.NormalizeReportKind(report.Kind)
	}
	return harness.ReportKindFinalText
}

func newAgentCompletionCommunicationWithMessage(snap subagent.SubAgentSnapshot, recipientPath string, message AgentMailboxMessage) agentthread.InterAgentCommunication {
	return newAgentCompletionCommunicationWithMessageAndTrigger(snap, recipientPath, message, false)
}

func newAgentCompletionCommunicationWithMessageAndTrigger(snap subagent.SubAgentSnapshot, recipientPath string, message AgentMailboxMessage, triggerTurn bool) agentthread.InterAgentCommunication {
	if strings.TrimSpace(recipientPath) == "" {
		recipientPath = agentthread.RootPath
	}
	content := agentthread.SubagentNotificationContent(snap.AgentPath, message)
	return agentthread.NewInterAgentCommunication(parseAgentPathOrRoot(snap.AgentPath), parseAgentPathOrRoot(recipientPath), content, triggerTurn)
}

func newInterAgentCommunication(authorPath, recipientPath, content string, triggerTurn bool) agentthread.InterAgentCommunication {
	return agentthread.NewInterAgentCommunication(
		parseAgentPathOrRoot(authorPath),
		parseAgentPathOrRoot(recipientPath),
		content,
		triggerTurn,
	)
}

func parseAgentPathOrRoot(path string) agentthread.AgentPath {
	parsed, err := agentthread.ParseAgentPath(path)
	if err != nil {
		return agentthread.RootAgentPath()
	}
	return parsed
}

func parentPathForSnapshot(snap subagent.SubAgentSnapshot) string {
	path := strings.TrimSpace(snap.AgentPath)
	if path == "" || path == agentthread.RootPath {
		return agentthread.RootPath
	}
	if idx := strings.LastIndex(path, "/"); idx > len("/root") {
		return path[:idx]
	}
	return agentthread.RootPath
}

func isFinalSubAgentStatus(status subagent.Status) bool {
	return subagent.IsTerminal(status)
}

func isActiveAgentThreadStatus(status agentthread.Status) bool {
	switch status {
	case agentthread.StatusPending, agentthread.StatusRunning:
		return true
	default:
		return false
	}
}

func isFinalAgentThreadStatus(status agentthread.Status) bool {
	switch status {
	case agentthread.StatusCompleted, agentthread.StatusFailed, agentthread.StatusCancelled:
		return true
	default:
		return false
	}
}

func (c *AgentControl) resolveAgentIDFrom(currentPath, target string) string {
	id := strings.TrimSpace(target)
	if id == "" {
		return ""
	}
	if c.manager.Get(id) != nil {
		return id
	}
	if c.threads != nil {
		if meta, ok := c.threads.ResolveFrom(currentPath, id); ok {
			return meta.ID
		}
	}
	return id
}

// rehydrateAgent lazily rebuilds a dormant sub-agent from its persisted
// snapshot so a follow-up can resume it after the live run was lost (e.g.
// across a process restart). It is only reached when a follow-up addresses
// an id the live manager no longer tracks; the startup sweep
// (reconcileOrphanedHarnessTasks) settles crash-orphaned records eagerly and
// rewrites their snapshots to interrupted, which this path resumes like any
// other terminal state.
//
// Returns (nil, nil) when no snapshot exists, so the caller reports the
// target as not found. A non-nil error means a snapshot exists but cannot
// be resumed (predates resume support, working directory gone, unknown
// worker type, model-pin resolution failed, etc.); resume never silently
// falls back to a different runtime.
func (c *AgentControl) rehydrateAgent(id string) (*subagent.SubAgent, error) {
	if c == nil || c.manager == nil || strings.TrimSpace(c.historyDir) == "" {
		return nil, nil
	}
	path := filepath.Join(c.historyDir, id+".json")
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	run, err := subagent.LoadPersistedRun(path)
	if err != nil {
		return nil, fmt.Errorf("cannot resume agent %q: reading snapshot: %w", id, err)
	}
	if run.Version < subagent.ResumeSnapshotVersion {
		return nil, fmt.Errorf("cannot resume agent %q: this snapshot predates resume support; re-spawn the task instead", id)
	}
	if !isFinalSubAgentStatus(run.Status) && run.Status != subagent.StatusWaitingChildren {
		return nil, fmt.Errorf("cannot resume agent %q: snapshot status %q is not resumable", id, run.Status)
	}
	workerRoot := strings.TrimSpace(run.CWD)
	if workerRoot == "" {
		return nil, fmt.Errorf("cannot resume agent %q: snapshot is missing its working directory", id)
	}
	if info, statErr := os.Stat(workerRoot); statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("cannot resume agent %q: working directory %q is no longer available (worktree cleaned up?); re-spawn the task instead", id, workerRoot)
	}
	wt, err := LookupWorkerType(run.Type)
	if err != nil {
		return nil, fmt.Errorf("cannot resume agent %q: %w", id, err)
	}
	meta := rehydratedThreadMeta(run)
	workerKit, err := c.workerFact(workerRoot, wt, meta)
	if err != nil {
		return nil, fmt.Errorf("cannot resume agent %q: worker toolkit: %w", id, err)
	}

	var runtime *subagent.WorkerRuntime
	var clientOverride providers.StreamClient
	var providerOverride string
	var modelOverride string
	if run.Runtime != nil {
		// The snapshot was started with an aliased/default runtime. Rebuild
		// only the client for the persisted provider; the rest of the runtime
		// must stay exactly as it was at first run. If the persisted provider
		// is non-empty, a missing/failing resolver must fail explicitly rather
		// than routing through the current default client, which would violate
		// the frozen runtime provenance. An empty persisted provider safely
		// falls back to the manager's current default client (no provider
		// identity to preserve).
		provider := strings.TrimSpace(run.Runtime.Provider)
		if provider != "" {
			resolver := c.currentProviderClientResolver()
			if resolver == nil {
				return nil, fmt.Errorf("cannot resume agent %q: snapshot has a frozen runtime for provider %q but no provider-client resolver is installed", id, provider)
			}
			rebuilt, resolveErr := resolver(provider)
			if resolveErr != nil {
				return nil, fmt.Errorf("cannot resume agent %q: rebuild client for provider %q: %w", id, provider, resolveErr)
			}
			if rebuilt == nil {
				return nil, fmt.Errorf("cannot resume agent %q: provider-client resolver returned nil client for provider %q", id, provider)
			}
			clientOverride = rebuilt
			providerOverride = provider
		}
		cloned := run.Runtime.Clone()
		runtime = &cloned
		runtime.Client = clientOverride
	} else {
		// Legacy participant model-pin path: rebuild the (model, client, provider)
		// triple the same way queued-spawn restore does.
		modelOverride, clientOverride, providerOverride, err = c.resolveSpawnModelPin(fmt.Sprintf("resumed agent %s", id), run.Model, run.ModelPin, nil)
		if err != nil {
			return nil, err
		}
	}

	sa, err := c.manager.Restore(subagent.RestoreOptions{
		Run:           run,
		Toolkit:       workerKit,
		Model:         modelOverride,
		Client:        clientOverride,
		ProviderName:  providerOverride,
		WorkerRuntime: runtime,
		HistoryPath:   path,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot resume agent %q: %w", id, err)
	}
	// Re-register the thread so status notifications and completion
	// deliveries for the resumed run have somewhere to land. Best-effort:
	// a missing registry never blocks the resume.
	if c.threads != nil {
		_ = c.threads.Restore(meta)
	}
	// A process killed between the run's terminal snapshot and the
	// notification consumer's recording (e.g. while a requires_report
	// closing turn was in flight) leaves the harness task stuck "running"
	// with no report. Reconcile the durable record with the snapshot's
	// terminal truth so the run stops tombstoning; for completed runs this
	// also synthesizes the final_text report the crash swallowed.
	c.reconcileRehydratedHarnessStatus(sa.Snapshot())
	return sa, nil
}

// reconcileRehydratedHarnessStatus settles the harness task/run records for
// a rehydrated run whose persisted snapshot is terminal but whose harness
// task never got its terminal recording (the recording process died first).
// It replays the missed terminal notification through the normal recording
// path, which is guarded against double-recording.
func (c *AgentControl) reconcileRehydratedHarnessStatus(snap subagent.SubAgentSnapshot) {
	if c == nil || c.harnessStore == nil || !isFinalSubAgentStatus(snap.Status) {
		return
	}
	task, ok := c.harnessTask(snap.ID)
	if !ok {
		return
	}
	if !isTerminalHarnessStatus(task.Status) {
		c.recordHarnessStatus(subagent.Notification{AgentID: snap.ID, Status: snap.Status, Snapshot: snap})
	}
	if _, err := c.ensureAgentResultDelivery(snap); err != nil {
		providers.DebugLogf("agentcontrol: reconcile rehydrated result-ready for %s: %v", snap.ID, err)
	}
}

// reconcileOrphanedHarnessTasks is the startup reconciliation sweep for the
// durable task graph (self-consistency invariant 2: every background task is
// observable and recoverable). A worker runs as a detached goroutine, so a
// crash between recordHarnessTaskStart and the terminal notification leaves
// its harness task pending/queued/running forever. On every (re)bind of the
// harness dir this sweep settles non-terminal tasks that have no live
// executor in this process:
//
//   - a task whose manager entry is live is left alone;
//   - a task whose manager entry (or persisted snapshot) is already
//     terminal replays the missed terminal recording — the run finished,
//     only the recording process died first (same reconciliation as the
//     lazy rehydrate path);
//   - a queued task whose spawn payload was restored stays queued —
//     maybeStartQueued still owns it;
//   - everything else is marked interrupted, preserving the original
//     timestamps and any recorded error.
func (c *AgentControl) reconcileOrphanedHarnessTasks() {
	if c == nil || c.harnessStore == nil || c.harnessStore.Dir() == "" {
		return
	}
	tasks, err := c.harnessStore.ListTasks()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, task := range tasks {
		if isTerminalHarnessStatus(task.Status) {
			// The terminal harness record may have won the race with the
			// result-ready event before the previous process exited. Rebuild
			// that independently durable delivery intent on every startup.
			var snap subagent.SubAgentSnapshot
			found := false
			if c.manager != nil {
				if sa := c.manager.Get(task.ID); sa != nil {
					snap = sa.Snapshot()
					found = isFinalSubAgentStatus(snap.Status)
				}
			}
			if !found && strings.TrimSpace(c.historyDir) != "" {
				if run, loadErr := subagent.LoadPersistedRun(filepath.Join(c.historyDir, task.ID+".json")); loadErr == nil && isFinalSubAgentStatus(run.Status) {
					snap = snapshotFromPersistedRun(run)
					found = true
				}
			}
			if found {
				if _, readyErr := c.ensureAgentResultDelivery(snap); readyErr != nil {
					providers.DebugLogf("agentcontrol: reconcile terminal result-ready for %s: %v", snap.ID, readyErr)
				}
			}
			continue
		}
		if c.manager != nil {
			if sa := c.manager.Get(task.ID); sa != nil {
				snap := sa.Snapshot()
				if !isFinalSubAgentStatus(snap.Status) {
					continue // live executor owns this task
				}
				// Terminal run, non-terminal record: the terminal
				// notification was never recorded. Replay it.
				c.recordHarnessStatus(subagent.Notification{AgentID: snap.ID, Status: snap.Status, Snapshot: snap})
				if _, err := c.ensureAgentResultDelivery(snap); err != nil {
					providers.DebugLogf("agentcontrol: reconcile result-ready for %s: %v", snap.ID, err)
				}
				continue
			}
		}
		if task.Status == harness.TaskStatusQueued && c.hasQueuedSpawn(task.ID) {
			continue
		}
		if c.workerExecutionOwned(task.ID) {
			continue
		}
		if err := c.reconcileUnownedHarnessTask(task, now); err != nil && !errors.Is(err, errWorkerExecutionBusy) {
			providers.DebugLogf("agentcontrol: reconcile orphan %s: %v", task.ID, err)
		}
	}
}

func (c *AgentControl) reconcileUnownedHarnessTask(task harness.Task, now time.Time) error {
	newLease, err := c.acquireWorkerExecution(task.ID)
	if err != nil {
		// A busy lease is positive evidence that another app-server still has
		// a live executor. Its snapshot and harness rows must remain untouched.
		return err
	}
	if newLease {
		defer c.releaseWorkerExecution(task.ID)
	}
	if strings.TrimSpace(c.historyDir) != "" {
		path := filepath.Join(c.historyDir, task.ID+".json")
		if run, loadErr := subagent.LoadPersistedRun(path); loadErr == nil && isFinalSubAgentStatus(run.Status) {
			// The worker finished and only terminal recording was lost.
			snap := snapshotFromPersistedRun(run)
			c.recordHarnessStatus(subagent.Notification{AgentID: task.ID, Status: run.Status, Snapshot: snap})
			if _, readyErr := c.ensureAgentResultDelivery(snap); readyErr != nil {
				return readyErr
			}
			return nil
		}
	}
	c.markHarnessTaskInterrupted(task, now)
	return nil
}

// snapshotFromPersistedRun projects a persisted run record onto the snapshot
// shape the recording paths consume.
func snapshotFromPersistedRun(run subagent.PersistedRun) subagent.SubAgentSnapshot {
	snap := subagent.SubAgentSnapshot{
		ID:                 run.ID,
		ParticipantID:      run.ParticipantID,
		Type:               run.Type,
		TaskName:           run.TaskName,
		AgentProfile:       run.AgentProfile,
		AgentPath:          run.AgentPath,
		ParentID:           run.ParentID,
		Description:        run.Description,
		WorkerRoot:         run.CWD,
		Model:              run.Model,
		ModelPin:           run.ModelPin,
		ModelAlias:         run.ModelAlias,
		ModelAliasFallback: run.ModelAliasFallback,
		ResolvedProvider:   providerNameFromPersistedRun(run),
		ResolvedModel:      resolvedModelFromPersistedRun(run),
		ResolvedAPIModel:   resolvedAPIModelFromPersistedRun(run),
		Status:             run.Status,
		StartedAt:          run.StartedAt,
		CompletedAt:        run.CompletedAt,
		Result:             run.Result,
	}
	if strings.TrimSpace(run.Error) != "" {
		snap.Error = errors.New(run.Error)
	}
	return snap
}

func providerNameFromPersistedRun(run subagent.PersistedRun) string {
	if run.Runtime != nil {
		return strings.TrimSpace(run.Runtime.Provider)
	}
	return ""
}

func resolvedModelFromPersistedRun(run subagent.PersistedRun) string {
	if run.Runtime != nil {
		return strings.TrimSpace(run.Runtime.Model)
	}
	return strings.TrimSpace(run.Model)
}

func resolvedAPIModelFromPersistedRun(run subagent.PersistedRun) string {
	if run.Runtime != nil {
		if api := strings.TrimSpace(run.Runtime.APIModel); api != "" {
			return api
		}
		return strings.TrimSpace(run.Runtime.Model)
	}
	return strings.TrimSpace(run.Model)
}

// markHarnessTaskInterrupted settles one orphaned task as interrupted. The
// original CreatedAt/StartedAt/token counts survive; an error already on the
// record is kept as the reason, otherwise the reconciliation reason is
// written. The matching worker snapshot (when one was persisted) is rewritten
// to interrupted too so a follow-up can resume the run from its history.
func (c *AgentControl) markHarnessTaskInterrupted(task harness.Task, now time.Time) {
	reason := strings.TrimSpace(task.Error)
	if reason == "" {
		reason = fmt.Sprintf("interrupted: no live executor for this task; the previous session exited while it was %s", task.Status)
	}
	completedAt := task.CompletedAt
	if completedAt.IsZero() {
		completedAt = now
	}
	runID := strings.TrimSpace(task.LastRunID)
	if runID == "" {
		runID = harnessRunID(task.ID)
	}
	_, _ = c.harnessStore.UpdateTaskStatus(task.ID, harness.TaskStatusInterrupted, completedAt, task.InputTokens, task.OutputTokens, reason)
	_, _ = c.harnessStore.UpdateRunStatus(runID, harness.TaskStatusInterrupted, completedAt, task.InputTokens, task.OutputTokens, reason)
	_ = c.harnessStore.AppendEvent(harness.Event{
		Type:      harness.EventTaskStatusChanged,
		TaskID:    task.ID,
		RunID:     runID,
		AgentID:   task.ID,
		Path:      task.Path,
		Status:    string(harness.TaskStatusInterrupted),
		Message:   reason,
		CreatedAt: now,
	})
	if strings.TrimSpace(c.historyDir) != "" {
		_, _ = subagent.MarkPersistedRunInterrupted(filepath.Join(c.historyDir, task.ID+".json"), reason, now)
	}
	c.markAgentThreadInterrupted(task.ID, now)
}

// markAgentThreadInterrupted settles the child-thread projection consumed by
// thread/list and thread/resume. After a process restart the old worker is not
// normally present in the fresh in-memory registry, so recovery must fall back
// to the durable thread store instead of leaving the UI-facing row running.
// agentthread has no interrupted status; failed is its terminal crash-recovery
// projection, matching threadStatusFromSubAgent(StatusInterrupted).
func (c *AgentControl) markAgentThreadInterrupted(workerID string, now time.Time) {
	if c == nil || strings.TrimSpace(workerID) == "" {
		return
	}
	if c.threads != nil {
		if meta, ok := c.threads.UpdateStatus(workerID, agentthread.StatusFailed, now); ok {
			if c.threadStore != nil {
				if err := c.threadStore.RecordStatus(meta); err != nil {
					providers.DebugLogf("agentcontrol: persist interrupted child thread %s: %v", workerID, err)
				}
			}
			return
		}
	}
	if c.threadStore == nil {
		return
	}
	threads, err := c.threadStore.ListThreads()
	if err != nil {
		providers.DebugLogf("agentcontrol: list child threads while reconciling %s: %v", workerID, err)
		return
	}
	for _, meta := range threads {
		if meta.ID != workerID {
			continue
		}
		if isFinalAgentThreadStatus(meta.Status) {
			return
		}
		meta.Status = agentthread.StatusFailed
		meta.UpdatedAt = now
		if err := c.threadStore.RecordStatus(meta); err != nil {
			providers.DebugLogf("agentcontrol: persist recovered child thread %s: %v", workerID, err)
		}
		return
	}
}

// hasQueuedSpawn reports whether the in-memory spawn queue still holds a
// prepared spawn for the given worker id.
func (c *AgentControl) hasQueuedSpawn(workerID string) bool {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	for _, prepared := range c.queued {
		if prepared.WorkerID == workerID {
			return true
		}
	}
	return false
}

// missingAgentError explains an unresolvable agent id. When the durable task
// record shows the run was interrupted with nothing resumable on disk, the
// caller gets that story instead of a bare not-found.
func (c *AgentControl) missingAgentError(id string) error {
	if task, ok := c.harnessTask(id); ok && task.Status == harness.TaskStatusInterrupted {
		return fmt.Errorf("agent %q was interrupted (%s) and left no resumable snapshot; re-spawn the task", id, strings.TrimSpace(task.Error))
	}
	return fmt.Errorf("agent %q not found", id)
}

// rehydratedThreadMeta rebuilds the thread metadata for a resumed run from
// its persisted snapshot, so the worker toolkit factory and status updates
// have the same placement the live run had.
func rehydratedThreadMeta(run subagent.PersistedRun) agentthread.Metadata {
	path := strings.TrimSpace(run.AgentPath)
	if path == "" {
		path = agentthread.RootPath
	}
	parentPath := parentPathForSnapshot(subagent.SubAgentSnapshot{AgentPath: run.AgentPath})
	created := run.StartedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	return agentthread.Metadata{
		ID:           run.ID,
		ParentID:     run.ParentID,
		Path:         path,
		TaskName:     run.TaskName,
		AgentProfile: run.AgentProfile,
		Role:         run.Type,
		CWD:          run.CWD,
		Model:        run.Model,
		Status:       threadStatusFromSubAgent(run.Status),
		CreatedAt:    created,
		UpdatedAt:    time.Now().UTC(),
		Source:       agentthread.Source{Kind: agentthread.SourceThreadSpawn, ParentPath: parentPath},
	}
}

func threadStatusFromSubAgent(status subagent.Status) agentthread.Status {
	switch status {
	case subagent.StatusPending:
		return agentthread.StatusPending
	case subagent.StatusQueued:
		return agentthread.StatusPending
	case subagent.StatusRunning:
		return agentthread.StatusRunning
	case subagent.StatusWaitingChildren:
		return agentthread.StatusWaitingChildren
	case subagent.StatusCompleted:
		return agentthread.StatusCompleted
	case subagent.StatusFailed:
		return agentthread.StatusFailed
	case subagent.StatusCancelled:
		return agentthread.StatusCancelled
	case subagent.StatusInterrupted:
		// agentthread has no interrupted status; failed is the closest
		// terminal mapping and keeps thread-side lifecycle checks working.
		return agentthread.StatusFailed
	default:
		return agentthread.Status(status)
	}
}

func harnessStatusFromSubAgent(status subagent.Status) harness.TaskStatus {
	switch status {
	case subagent.StatusPending:
		return harness.TaskStatusPending
	case subagent.StatusQueued:
		return harness.TaskStatusQueued
	case subagent.StatusRunning:
		return harness.TaskStatusRunning
	case subagent.StatusCompleted:
		return harness.TaskStatusCompleted
	case subagent.StatusFailed:
		return harness.TaskStatusFailed
	case subagent.StatusCancelled:
		return harness.TaskStatusCancelled
	case subagent.StatusInterrupted:
		return harness.TaskStatusInterrupted
	default:
		return harness.TaskStatus(status)
	}
}

func harnessRunID(taskID string) string {
	return strings.TrimSpace(taskID) + "-run-1"
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func resultArtifactPath(paths []string) string {
	for _, path := range paths {
		if filepath.Base(path) == "result.md" {
			return path
		}
	}
	return ""
}

// SystemPromptPreamble returns the instructions prepended to the
// main agent's system prompt. It teaches, in order:
//
//   - When the user's intent is unclear or the request depends on
//     information only they have, ask a clarifying question in your
//     reply before acting. Do not guess.
//   - Delegation rules (fresh subagents, fork spawns, communication planes,
//     honesty rules, failure handling) — but only AFTER alignment.
//
// There is NO separate "coordinator role" persona here. The main
// agent remains the user's coding agent; sub-agents are optional task
// tools for cases where delegation is worth the overhead.
func SystemPromptPreamble() string {
	return `You are wuu's main coding agent with access to an optional Agent tool. The main agent owns the user conversation, the final synthesis, and the decision about whether delegation is worth the overhead.

## Clarifying the request

When the user's intent is unclear, the task depends on requirements or tradeoffs only they can answer, or you would otherwise have to guess at something material, ask a clarifying question in your assistant reply before acting. Do not invent answers the user has not given you, and do not invoke a tool to surface the question — write it as plain text and let the user respond.

## Agent Tool

- spawn_agent — launch a child agent. Pass description and prompt. Specify subagent_type for a fresh specialized agent, or omit subagent_type to fork yourself with full conversation context.
- send_message — deliver a message to an existing child agent (queue-or-resume). Leave trigger_turn unset for an interim note; set trigger_turn=true to hand off a task that drives the target's next turn and returns its snapshot.
- close_agent — stop a running agent that is stuck or off-track.

The current child-agent roster and status is injected each turn as a <subagent_status> reminder; read it there instead of polling. Background agents resume you automatically when they finish, so you do not need to block waiting on them.

## Available Subagents

- general-purpose: broad code research, search, implementation, and multi-step tasks. Use this when you want a fresh agent with no inherited conversation context.
- worker: scoped implementation role; defaults to worktree isolation for edits.
- fork-self: omit subagent_type to fork yourself. Use when the child needs the current conversation context and you do not want intermediate tool output in your own context.

Agents execute tasks autonomously and return a structured handoff. The agent result is input for your own synthesis; do not forward it blindly.

## When to Use Agents

Do not spawn agents for trivial tasks you can handle yourself — reading a specific file, running a quick grep, or reporting a command output. Keep work local when the task is tightly coupled, small, or on the critical path. Spawn agents only when delegation materially improves the work: multi-file refactors, independent research across different areas, verification that benefits from a separate context, or work that can run in parallel.

Do not delegate work that blocks your immediate next step. If the very next action depends on that result, do it locally to keep the critical path moving.

Do not delegate understanding. Never hand off vague prompts like "based on your findings, fix the bug" or "based on the research, implement it." Read the findings yourself, decide what should happen, then give the agent a concrete brief.

## Concurrency

Launch independent agents in parallel whenever possible. Read-only or verification tasks can run freely in parallel. Write-heavy tasks should run one at a time per file set to avoid conflicts. When you split code-edit work, assign each agent clear files or modules and avoid overlapping ownership.

Fresh subagents run in the foreground by default so you can use their result immediately. Foreground child execution has no model-selected wait duration; it continues until the child finishes or the user/runtime cancels the turn. Set run_in_background=true only when you have genuinely independent or long-running work to do in parallel. Forks always run in the background. After spawning background agents, keep doing meaningful non-overlapping work when it exists. If there is no useful local work left, end your turn and let background completion notifications automatically resume you. Do not sleep, poll, or loop checking status.

When synthesis or integration depends on child outputs, end your turn and let their completion notifications resume you rather than blocking. Each notification carries the child's structured result, changed_files, and changed_file_overlap warnings when a sibling wrote the same files; reconcile overlaps before you merge.

## Working with Agent Results

Background completion notifications are internal agent handoffs, not new user requests. They may be encoded as structured inter-agent notifications with author, recipient, content, and trigger_turn fields. Treat content as the handoff payload, then synthesize and verify it yourself. When a background agent finishes, its result automatically arrives as a notification in your next turn.

Before launching follow-up work, read the returned content yourself and do your own synthesis. Agent output is not a substitute for your judgment.

## Writing Agent Prompts

Fresh subagent prompts must be self-contained. Include the task, background, role, identity or memory status, scope, non-goals, starting points, acceptance criteria, deliverables, reporting expectations, and constraints. For code-edit subtasks, explicitly name owned files or modules and nearby files or modules that are out of scope; split work so each agent has a disjoint write set.

Fork prompts can be shorter because the child inherits your context, but they still need a specific directive and scope. Do not re-explain all background in a fork; state what to do, what is out of scope, and what to report.

## Handling Worker Failures

When a worker reports failure, continue the same worker with send_message using trigger_turn=true — it has the full error context. If correction still fails, try a different approach or report to the user.

If a worker seems stuck, close it with close_agent and respawn with clearer instructions.
`
}

// CleanupSession removes all worktrees belonging to this session.
func (c *AgentControl) CleanupSession() error {
	_, worktrees := c.workspaceSnapshot()
	if worktrees == nil {
		return nil // non-git workspace, no worktrees to clean
	}
	return worktrees.CleanupSession(c.sessionID)
}

func appendForkWorktreeReminder(prompt, workerRoot string, isolation IsolationMode) string {
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n<system-reminder>\n")
	fmt.Fprintf(&b, "Your active working directory for this child task is: %s\n", workerRoot)
	fmt.Fprintf(&b, "Isolation mode: %s\n", isolation)
	b.WriteString("This overrides any inherited working-directory assumptions from the parent history.\n")
	b.WriteString("</system-reminder>")
	return b.String()
}

// composeWorkerSystemPrompt builds the system prompt for a worker.
// It prepends the worker type's role-specific prompt + a description
// of the working directory and isolation mode, then appends the base
// prompt (typically the main agent's instructions and skills).
func composeWorkerSystemPrompt(base string, wt WorkerType, workerRoot string, isolation IsolationMode) string {
	var b strings.Builder
	b.WriteString(wt.SystemPrompt)
	b.WriteString("\n\n")
	if wt.Role != "" || wt.ContextScope != "" || wt.OutputSchema != "" || len(wt.SuccessCriteria) > 0 {
		b.WriteString("## Role Contract\n")
		if wt.Role != "" {
			fmt.Fprintf(&b, "Role: %s\n", wt.Role)
		}
		if wt.ContextScope != "" {
			fmt.Fprintf(&b, "Context scope: %s\n", wt.ContextScope)
		}
		if wt.OutputSchema != "" {
			fmt.Fprintf(&b, "Output schema: %s\n", wt.OutputSchema)
		}
		if len(wt.SuccessCriteria) > 0 {
			b.WriteString("Success criteria:\n")
			for _, item := range wt.SuccessCriteria {
				fmt.Fprintf(&b, "- %s\n", item)
			}
		}
		b.WriteString("\n")
	}
	switch isolation {
	case IsolationWorktree:
		fmt.Fprintf(&b, "Your working directory is %s — an isolated worktree for this worker. ", workerRoot)
		b.WriteString("Edits you make stay sandboxed; the orchestrator will inspect the worktree after you finish. ")
	default: // inplace
		fmt.Fprintf(&b, "Your working directory is %s — the SHARED parent repository. ", workerRoot)
		b.WriteString("You are running inplace (no worktree isolation), so be especially careful: ")
		b.WriteString("read-only operations are safe, but any file you modify is visible to the orchestrator and other workers immediately. ")
	}
	b.WriteString("All file paths in your tools resolve relative to this directory. ")
	b.WriteString("Treat command execution as non-interactive when the active tool surface exposes it. Never rely on editors, pagers, password prompts, or confirmation dialogs. If command execution is unavailable under the active tool surface, report skipped command-based verification instead of inventing another path. Profile-specific tool-surface guidance tells you which command capability exists and how to use it. ")
	b.WriteString("You cannot spawn or manage other agents from this worker. If the task seems to require additional delegation, report that need in your final handoff so the parent can decide and coordinate.\n")
	if base != "" {
		b.WriteString("\n---\n\n")
		b.WriteString(base)
		b.WriteString("\n\n---\n\n")
		b.WriteString("Worker override: if any inherited text above describes the MAIN interactive agent as read-only, or says file changes / command execution must be delegated, ignore that text. It applies to the parent, not to you. If a tool is in your tool list, you may use it unless your task prompt explicitly forbids it.")
	}
	return b.String()
}

// newAgentControlWorkerID generates a worker ID. Mirrors subagent's
// scheme but is generated by AgentControl since worktree creation
// happens before subagent.Manager.Spawn.
func newAgentControlWorkerID(typ string) string {
	if typ == "" {
		typ = "agent"
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", typ, hex.EncodeToString(b))
}
