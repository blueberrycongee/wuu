package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/xaisub"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sidethread"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/tools"
)

var (
	errServerClosed        = errors.New("app-server is closed")
	errShutdown            = errors.New("app-server shutdown requested")
	errExecutionRunChanged = errors.New("thread execution belongs to a different Run")
)

type threadState struct {
	ID         string
	Source     string
	Owner      string
	Visibility string
	// Instructions are create-time session instructions appended to every
	// refreshed runtime system prompt.
	Instructions string
	// ProjectID is the coordinator of a project's managed session.
	ProjectID   string
	ProjectRole string
	ParentID    string
	AgentPath   string
	History     []providers.ChatMessage
	// historyHeadSeq is the physical append-only session_messages head that
	// History was reconstructed through. It must not be derived from the
	// logical messages: a checkpoint may retain no records or only old seqs.
	historyHeadSeq int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastAccessedAt time.Time
	Title          string
	ModelProvider  string
	Model          string
	ModelVariant   string
	ModelEffort    string
	Speed          string
	PermissionMode string
	ApproveForMe   bool
	// EngineID is the agent engine this thread is bound to. It is fixed at
	// thread creation; the built-in engine is "wuu".
	EngineID string
	// EngineRef is the engine's native session reference for this thread
	// (for example a codex thread id), persisted when the engine creates it.
	EngineRef                string
	CWD                      string
	WorkspaceKind            WorkspaceKind
	ForkedFromID             string
	ForkedFromTurnID         string
	ForkedFromItemID         string
	WorktreePath             string
	WorktreeBaseHEAD         string
	WorktreeBaseRepo         string
	WorkspaceID              string
	PinnedAt                 *time.Time
	FolderID                 string
	ArchivedAt               *time.Time
	ArchiveReason            string
	Turns                    []Turn
	PersistHistory           bool
	ReadOnly                 bool
	Ephemeral                bool
	execRuntime              *runtime.ThreadRuntime
	pendingRuntimeReset      bool
	runtimeSelectionMutation bool
	runtimeSubscription      *threadRuntimeSubscription

	// streamMu keeps a live snapshot and its response ordered with stream
	// mutations and notifications. Acquire it before mu when both are needed.
	streamMu              sync.Mutex
	mu                    sync.Mutex
	running               bool
	currentTurn           string
	currentTurnKind       TurnKind
	currentExecutionRunID string
	currentTurnResumed    bool
	runningProviderName   string
	runningModel          string
	cancel                context.CancelFunc
	// idleWaiters are closed when this thread releases execution. Follow-up
	// turn/start after a visible final answer waits here instead of failing busy.
	idleWaiters []chan struct{}
	// completeAfterAnswerReadyCancel is set when a successor user turn arrives
	// after the current turn's answer is already visible. Cancelling leftover
	// provider work must complete that turn, not mark it interrupted.
	completeAfterAnswerReadyCancel bool
	executionLease                 *session.ThreadExecutionLease
	pluginExecutionLease           *session.PluginGenerationLease
	pluginLeaseReleaseLoop         bool
	onPluginLeaseQuiescent         func()
	runtimePluginEpoch             uint64
	runtimePluginRevision          uint64
	admissionReserved              bool
	pendingSteers                  []providers.ChatMessage
	pendingSteerControls           map[string]session.Control
	SessionControl                 *ThreadSessionControl
	steerWake                      chan struct{}
	steerWakeClosed                bool
	activeSteerDocument            *ActiveDocument
	activeSteerContextSet          bool
	steerDocumentOverrides         []activeDocumentOverride
	interrupting                   bool
	// Worker-tree freeze (turn/interrupt): while set, agent-completion drains
	// hold their pending synthetic turns. The next user-initiated turn folds
	// the whole-tree snapshot into its request (frozenTreeContext) and marks
	// the held completion results answered (frozenTreeResultIDs).
	workerTreeFrozen    bool
	frozenTreeContext   []agent.ContextSegment
	frozenTreeResultIDs []string

	nextItemIndex         int
	activeAgentItemID     string
	agentStream           *agentMessageStream
	activeReasoningItemID string
	toolItems             map[string]string
	streamText            map[string]*strings.Builder
	hiddenToolEvent       bool
}

type threadRuntimeSubscription struct {
	statusCh            chan subagent.Notification
	streamCh            chan subagent.StreamNotification
	processCh           chan process.Event
	processManager      *process.Manager
	terminalUnsubscribe func()
	done                chan struct{}
	wg                  sync.WaitGroup
	once                sync.Once
}

func (sub *threadRuntimeSubscription) stop() {
	if sub == nil {
		return
	}
	sub.once.Do(func() {
		close(sub.done)
	})
	sub.wg.Wait()
}

type backgroundLaunch struct {
	decision chan bool
	once     sync.Once
}

func (l *backgroundLaunch) Commit() {
	if l == nil {
		return
	}
	l.once.Do(func() { l.decision <- true })
}

func (l *backgroundLaunch) Cancel() {
	if l == nil {
		return
	}
	l.once.Do(func() { l.decision <- false })
}

type Server struct {
	rt      *runtime.Session
	out     io.Writer
	writeMu sync.Mutex

	settingsUsageMu    sync.Mutex
	settingsUsageCache *settingsUsageCacheEntry

	// clientCalls is the pending table for server-initiated requests over the
	// negotiated reverse-RPC channel. Keyed by the
	// "srv-<seq>" id the core mints; each value is a buffered(1) chan that the
	// scanner goroutine delivers exactly one clientResponse into. clientCallMu
	// guards both the map and clientCallSeq; see callClient for the strict
	// register/deliver/delete deadlock discipline these fields require.
	clientCallMu                sync.Mutex
	clientCalls                 map[string]chan clientResponse
	clientCallSeq               uint64
	clientMethods               map[string]struct{}
	deferredNotificationContent atomic.Bool

	// pushRegistrar is the host-side hook invoked by the device/push_*
	// methods. The desktop main pipeline leaves it nil so the methods
	// respond with "remote-only" errors; the remote-host package binds
	// a per-device registrar via WithPushRegistrar in RunStdioForDevice.
	pushRegistrar PushRegistrar

	mu      sync.Mutex
	threads map[string]*threadState

	runMu             sync.Mutex
	runStore          *execution.Store
	runs              map[string]*runTracker
	activeRunByThread map[string]string

	agentTerminalFinalizationMu sync.Mutex
	agentTerminalFinalizations  map[agentTerminalFinalizationKey]struct{}

	agentCompletionMu            sync.Mutex
	pendingAgentCompletionTurns  map[string][]agentCompletionTurn
	drainingAgentCompletionTurns map[string]bool

	queuedTurnMu                sync.Mutex
	pendingQueuedTurns          map[string][]queuedTurn
	claimedQueuedTurns          map[string]*queuedTurnClaim
	cancelledPendingSubmissions map[string]string
	drainingQueuedTurns         map[string]bool
	heldUserWorkMu              sync.Mutex

	pluginTurnUnbind   func()
	pluginTurnWaiters  pluginTurnWaitHub
	userQuestionUnbind func()
	userQuestionStop   chan struct{}
	userQuestionDone   chan struct{}

	rewriteChatHistoryForTest           func(string, string, []providers.ChatMessage) error
	afterLifecycleHistoryAppendForTest  func(threadID string)
	deleteSessionForTest                func(string) (session.Session, error)
	afterWorkerShutdownStopWavesForTest func()
	beforeQueuedTurnBackgroundForTest   func()

	codexModelsMu   sync.Mutex
	codexModelCache map[string]map[string]config.ProviderModelConfig

	engineModelCatalogMu         sync.Mutex
	codexEngineModelCatalogCache *codexEngineModelCatalogCacheEntry
	acpEngineModelCatalogCache   map[string]*codexEngineModelCatalogCacheEntry

	xaiLoginMu sync.Mutex
	xaiLogins  *xaisub.LoginHub

	modelCatalogHTTPClient *http.Client
	modelCatalogCachePath  string
	modelCatalogURL        string

	// participantSummaryCache memoizes participant store lookups keyed
	// by participant ID. Ephemeral participants are immutable after
	// creation, so entries never need invalidation; failed lookups are
	// not cached, so a late store write is picked up on the next
	// resolve.
	participantMu           sync.Mutex
	participantSummaryCache map[string]participant.Summary

	inferenceMaintenanceStop     chan struct{}
	inferenceMaintenanceDone     chan struct{}
	inferenceMaintenanceStopOnce sync.Once
	activityUnsubscribe          func()
	backgroundMu                 sync.Mutex
	backgroundWG                 sync.WaitGroup
	engineAuthMu                 sync.Mutex
	engineAuthCancels            map[string]context.CancelFunc
	closeOnce                    sync.Once
	closed                       atomic.Bool
	pluginGenerationMutation     atomic.Bool
	pluginGenerationEpoch        atomic.Uint64
	pluginRuntimeRevision        atomic.Uint64
	pluginGenerationRefreshMu    sync.Mutex
	configRefreshMu              sync.Mutex
	configFingerprint            string
	configRejectedSources        string
	providerSummariesMu          sync.Mutex
	lastProviderSummaries        []ProviderSummary
	pluginLifecycleReplayPending atomic.Bool
	refreshExtensionsForTest     func(config.Config) error
	refreshConfigForTest         func() error
	presenceLease                *session.AppServerPresenceLease
	bootOwner                    bool
	storageMaintenanceCancel     context.CancelFunc
	startupErr                   error

	// sideThreadStore persists side threads (1:<=1 binding per main
	// thread). Nil when SessionDir is unset; handleSideThreadOpen /
	// handleSideThreadGetHistory treat nil as the "feature off" path.
	sideThreadStore *sidethread.Store
	controlMu       sync.Mutex
	projectCreateMu sync.Mutex
	// inboxMu orders deliveries into a session so pending input is admitted
	// in creation order.
	inboxMu                sync.Mutex
	projectInboxMu         sync.Mutex
	projectCapacityRetries map[string]map[string]func() bool
	projectInboxDrains     map[string]*projectInboxDrain
	projectInboxAfterFunc  func(time.Duration, func()) func()
	sideTurnMu             sync.Mutex
	sideTurns              map[string]*sideThreadTurn
}

func New(rt *runtime.Session, out io.Writer) *Server {
	store, err := credentialstore.NewDesktopStore()
	if err != nil {
		providers.DebugLogf("desktop credential store: %v", err)
	}
	return NewWithCredentialStore(rt, out, store, http.DefaultClient)
}

func NewWithCredentialStore(rt *runtime.Session, out io.Writer, store credentialstore.Store, httpClient *http.Client) *Server {
	s := &Server{
		rt:      rt,
		out:     out,
		threads: make(map[string]*threadState),

		pendingAgentCompletionTurns:  make(map[string][]agentCompletionTurn),
		drainingAgentCompletionTurns: make(map[string]bool),
		pendingQueuedTurns:           make(map[string][]queuedTurn),
		claimedQueuedTurns:           make(map[string]*queuedTurnClaim),
		cancelledPendingSubmissions:  make(map[string]string),
		drainingQueuedTurns:          make(map[string]bool),
		codexModelCache:              make(map[string]map[string]config.ProviderModelConfig),
		inferenceMaintenanceStop:     make(chan struct{}),
		sideTurns:                    make(map[string]*sideThreadTurn),
		clientCalls:                  make(map[string]chan clientResponse),
		clientMethods:                make(map[string]struct{}),
		runs:                         make(map[string]*runTracker),
		activeRunByThread:            make(map[string]string),
		modelCatalogHTTPClient:       httpClient,
	}
	s.bindUserQuestions()
	if rt != nil {
		catalogHome := strings.TrimSpace(rt.WuuHome)
		if catalogHome == "" && strings.TrimSpace(rt.ConfigPath) != "" {
			catalogHome = filepath.Dir(rt.ConfigPath)
		}
		if catalogHome != "" {
			s.modelCatalogCachePath = filepath.Join(catalogHome, "modelcatalog.json")
		}
	}
	if rt != nil && strings.TrimSpace(rt.WuuHome) != "" {
		lease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(rt.WuuHome)
		if err != nil {
			s.startupErr = fmt.Errorf("acquire initial plugin generation: %w", err)
			return s
		}
		if !acquired {
			s.startupErr = errors.New("plugin packages are being changed by another app-server")
			return s
		}
		epoch := lease.Epoch()
		if epoch != rt.InitialPluginGenerationEpoch() {
			if err := s.refreshExtensions(s.currentExtensionConfig()); err != nil {
				_ = lease.Release()
				s.startupErr = fmt.Errorf("refresh initial plugin generation %d: %w", epoch, err)
				return s
			}
		}
		s.pluginGenerationEpoch.Store(epoch)
		if err := lease.Release(); err != nil {
			s.startupErr = fmt.Errorf("release initial plugin generation: %w", err)
			return s
		}
	}
	if s.modelCatalogCachePath != "" {
		if err := modelcatalog.LoadCache(s.modelCatalogCachePath); err != nil {
			providers.DebugLogf("model catalog cache: %v", err)
		}
	}
	bootOwner := false
	if rt != nil && strings.TrimSpace(rt.SessionDir) != "" {
		lease, first, err := session.AcquireAppServerPresence(rt.SessionDir)
		if err != nil {
			s.startupErr = fmt.Errorf("acquire app-server presence: %w", err)
			return s
		}
		s.presenceLease = lease
		bootOwner = first
		s.bootOwner = first
	}
	if rt != nil && strings.TrimSpace(rt.SessionDir) != "" {
		s.sideThreadStore = sidethread.NewStore(filepath.Join(rt.SessionDir, "sidethreads"))
		runStore, err := execution.NewStore(rt.SessionDir)
		if err != nil {
			s.startupErr = fmt.Errorf("open execution run store: %w", err)
			return s
		}
		s.runStore = runStore
	}
	if bootOwner {
		s.recoverSideThreadsOnBoot()
		s.settleOnBoot()
	}
	if s.presenceLease != nil {
		if err := s.presenceLease.FinalizeStartup(); err != nil {
			// Retain the startup/exclusive lock until Close. Blocking a peer is
			// safer than letting it misclassify this live server as crashed.
			s.startupErr = fmt.Errorf("finalize app-server presence: %w", err)
			return s
		}
	}
	if store != nil && rt != nil && rt.Toolkit != nil {
		if manager := rt.Toolkit.MCPManager(); manager != nil {
			manager.SetOAuthManager(mcp.NewOAuthManager(store, httpClient))
		}
	}
	if rt != nil && rt.ActivityRegistry != nil {
		s.activityUnsubscribe = rt.ActivityRegistry.Subscribe(func(event activity.Event) {
			s.notifyActivityEvent(event)
		})
	}
	if rt != nil && rt.PluginSessionRouter != nil {
		s.pluginTurnUnbind = rt.PluginSessionRouter.BindExtended(
			s.createPluginSession, s.sendPluginSession, s.listPluginSessions, s.cancelPluginSession,
			s.inspectPluginSession, s.statusPluginWorkspace, s.applyPluginWorkspace, s.discardPluginWorkspace,
			s.controlPluginSession,
		)
		s.startBackground(s.replayPendingPluginTurnLifecycles)
	}
	s.startInferenceJournalMaintenance()
	if rt != nil && rt.SessionDir != "" {
		s.startProjectRecovery()
	}
	s.startPluginGenerationWatch()
	s.startConfigWatch()
	return s
}

const sessionStorageMaintenanceInterval = 6 * time.Hour

func (s *Server) maintainSessionStorage(ctx context.Context) {
	ticker := time.NewTicker(sessionStorageMaintenanceInterval)
	defer ticker.Stop()
	for {
		s.maintainSessionStoragePass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) maintainSessionStoragePass(ctx context.Context) {
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return
	}
	redundant, err := session.MaintainRedundantStorage(ctx, s.rt.SessionDir)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		providers.DebugLogf("redundant session storage maintenance: %v", err)
	} else if redundant.ProjectedToolResultsCleared > 0 || redundant.SupersededCheckpointsDeleted > 0 || redundant.ToolMessageContentsCompacted > 0 {
		providers.DebugLogf(
			"redundant session storage maintenance: projected_results=%d superseded_checkpoints=%d tool_message_projections=%d",
			redundant.ProjectedToolResultsCleared,
			redundant.SupersededCheckpointsDeleted,
			redundant.ToolMessageContentsCompacted,
		)
	}
	result, err := session.MaintainModelInputReceipts(ctx, s.rt.SessionDir, time.Now().UTC())
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		providers.DebugLogf("session storage maintenance: %v", err)
	} else if result.Deleted > 0 || result.Compressed > 0 {
		providers.DebugLogf(
			"session storage maintenance: deleted=%d compressed=%d bytes_before=%d bytes_after=%d migration_done=%t",
			result.Deleted,
			result.Compressed,
			result.BytesBefore,
			result.BytesAfter,
			result.CompressionDone,
		)
	}
	cursors, err := tools.MaintainSearchCursorStorage(s.rt.StateDir, time.Now().UTC())
	if err != nil {
		providers.DebugLogf("search cursor storage maintenance: %v", err)
	} else if cursors.Deleted > 0 || cursors.Compressed > 0 {
		providers.DebugLogf(
			"search cursor storage maintenance: deleted=%d compressed=%d bytes_before=%d bytes_after=%d",
			cursors.Deleted,
			cursors.Compressed,
			cursors.BytesBefore,
			cursors.BytesAfter,
		)
	}
	orphanWorktrees, err := s.cleanupOrphanWorktrees()
	if err != nil {
		providers.DebugLogf("worktree storage maintenance: %v", err)
	} else if orphanWorktrees > 0 {
		providers.DebugLogf("worktree storage maintenance: removed %d orphan owner group(s)", orphanWorktrees)
	}
	if stateDir, err := s.workspaceStateDir(); err == nil {
		if err := maintainInputImageStorage(stateDir, time.Now().UTC()); err != nil {
			providers.DebugLogf("input image storage maintenance: %v", err)
		}
	}
}

// settleOnBoot reconciles orphaned provider operations without starting a turn.
// Recovery is best-effort so a transient journal error does not block startup.
func (s *Server) settleOnBoot() {
	if s == nil || s.rt == nil {
		return
	}
	now := time.Now().UTC()
	if s.rt.InferenceJournalRuntime != nil {
		recoveries, recoverErr := s.rt.InferenceJournalRuntime.ReconcileOrphans(now)
		if recoverErr != nil {
			providers.DebugLogf("settleOnBoot pass4 (inference journal): %v", recoverErr)
		} else if len(recoveries) > 0 {
			s.persistRecoveredTurnTerminals(recoveries, now)
			var safe, blocked, abandoned int
			for _, recovery := range recoveries {
				switch recovery.Action {
				case providers.RecoveryRescheduleSafe:
					safe++
				case providers.RecoveryBlockAmbiguous:
					blocked++
				default:
					abandoned++
				}
			}
			providers.DebugLogf("settleOnBoot pass4: inference operations recovered (safe=%d blocked=%d abandoned=%d)", safe, blocked, abandoned)
		}
		if pruned, pruneErr := s.rt.InferenceJournalRuntime.Prune(now); pruneErr != nil {
			providers.DebugLogf("settleOnBoot pass5 (inference journal retention): %v", pruneErr)
		} else if pruned > 0 {
			providers.DebugLogf("settleOnBoot pass5: pruned %d old inference operation(s)", pruned)
		}
		if s.runStore != nil {
			recovered, err := s.runStore.ReconcileOrphans(context.Background(), s.rt.InferenceJournalRuntime.RuntimeID(), now)
			if err != nil {
				providers.DebugLogf("settleOnBoot pass6 (execution runs): %v", err)
			} else if len(recovered) > 0 {
				providers.DebugLogf("settleOnBoot pass6: interrupted %d orphan execution run(s)", len(recovered))
			}
		}
	}
}

func (s *Server) persistRecoveredTurnTerminals(recoveries []session.InferenceCrashRecovery, now time.Time) {
	owners := make(map[string]struct{})
	for _, recovery := range recoveries {
		ownerID := strings.TrimSpace(recovery.OwnerID)
		if recovery.Kind == providers.InferenceOperationAgentRound && ownerID != "" {
			owners[ownerID] = struct{}{}
		}
	}
	for ownerID := range owners {
		loaded, err := s.loadPersistedThreadSnapshot(ownerID)
		if err != nil {
			if !errors.Is(err, session.ErrSessionNotFound) {
				providers.DebugLogf("settleOnBoot turn projection %q: %v", ownerID, err)
			}
			continue
		}
		turns := turnsFromPersistedHistory(ownerID, loaded.displayHistory, now, s.resolveParticipantSummary)
		if len(turns) == 0 {
			continue
		}
		turn := turns[len(turns)-1]
		if hasPersistedTurnTerminal(loaded.rawHistory, turn.ID) || turnHasFinalAnswer(turn) {
			continue
		}
		message := "execution interrupted because the previous app server exited"
		if err := session.AppendHistoryRecord(s.rt.SessionDir, ownerID, session.HistoryRecord{
			Role: "meta", Content: turnTerminalHistoryRecord, DisplayContent: message,
			ClientID: turn.ID, StopReason: string(TurnStatusInterrupted), At: now,
		}); err != nil {
			providers.DebugLogf("settleOnBoot persist interrupted turn %q: %v", ownerID, err)
		}
	}
}

func hasPersistedTurnTerminal(history []persistedMessage, turnID string) bool {
	turnID = strings.TrimSpace(turnID)
	for _, record := range history {
		if strings.EqualFold(strings.TrimSpace(record.Role), "meta") &&
			record.Content == turnTerminalHistoryRecord &&
			strings.TrimSpace(record.ClientID) == turnID {
			return true
		}
	}
	return false
}

func turnHasFinalAnswer(turn Turn) bool {
	for _, item := range turn.Items {
		if item.Type == ThreadItemAgentMessage && item.Terminal && strings.TrimSpace(item.Text) != "" {
			return true
		}
	}
	return false
}

func (s *Server) startInferenceJournalMaintenance() {
	if s == nil || s.rt == nil || s.rt.InferenceJournalRuntime == nil {
		return
	}
	journalRuntime := s.rt.InferenceJournalRuntime
	s.inferenceMaintenanceDone = make(chan struct{})
	go func() {
		defer close(s.inferenceMaintenanceDone)
		ticker := time.NewTicker(session.InferenceJournalRecoveryInterval())
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				recoveries, err := journalRuntime.ReconcileOrphans(now.UTC())
				if err != nil {
					providers.DebugLogf("inference journal maintenance: %v", err)
					continue
				}
				if len(recoveries) > 0 {
					s.persistRecoveredTurnTerminals(recoveries, now.UTC())
					providers.DebugLogf("inference journal maintenance: recovered %d orphan operation(s)", len(recoveries))
				}
			case <-s.inferenceMaintenanceStop:
				return
			}
		}
	}()
}

func (s *Server) stopInferenceJournalMaintenance() {
	if s == nil || s.inferenceMaintenanceStop == nil {
		return
	}
	s.inferenceMaintenanceStopOnce.Do(func() {
		close(s.inferenceMaintenanceStop)
	})
	if s.inferenceMaintenanceDone != nil {
		<-s.inferenceMaintenanceDone
	}
}

func (s *Server) startBackground(work func()) bool {
	if s == nil || work == nil {
		return false
	}
	s.backgroundMu.Lock()
	if s.closed.Load() {
		s.backgroundMu.Unlock()
		return false
	}
	s.backgroundWG.Add(1)
	s.backgroundMu.Unlock()
	go func() {
		defer s.backgroundWG.Done()
		work()
	}()
	return true
}

// reserveBackground registers shutdown ownership before a caller publishes a
// started turn. The work remains gated until Commit; Cancel releases the owned
// goroutine without running it. Callers should defer Cancel immediately.
func (s *Server) reserveBackground(work func()) (*backgroundLaunch, bool) {
	if work == nil {
		return nil, false
	}
	launch := &backgroundLaunch{decision: make(chan bool, 1)}
	if !s.startBackground(func() {
		if <-launch.decision {
			work()
		}
	}) {
		return nil, false
	}
	return launch, true
}

// Close synchronously stops work owned by this app-server connection. It does
// not return until locally admitted turns, workers, and their durable terminal
// finalizers have released execution ownership.…42540 tokens truncated…nfunc (s *Server) persistTurnTrace(threadRuntime *runtime.ThreadRuntime, runner *agent.StreamRunner, threadID string, turnRuntime turnRuntimeSnapshot, turn Turn, res agent.LoopResult, runErr error, toolRecordStart int, contextRequests []sessiontrace.RequestContextRecord, providerStates []sessiontrace.ProviderStateRecord, compactAttempts []sessiontrace.CompactRecord, barrierRejectionsArg ...[]sessiontrace.BarrierToolBatchRejectionRecord) (string, error) {
	if threadRuntime == nil || threadRuntime.Toolkit == nil {
		return "", nil
	}
	tracePath := sessiontrace.Path(threadRuntime.Toolkit.SessionDir())
	if strings.TrimSpace(tracePath) == "" {
		return "", nil
	}
	providerName := strings.TrimSpace(turnRuntime.ProviderName)
	if s != nil && s.rt != nil {
		providerName = firstNonEmpty(providerName, s.rt.ProviderName)
	}
	permissions := turnRuntime.permissions()
	model := ""
	apiModel := ""
	if runner != nil {
		model = runner.Model
		apiModel = runner.APIModel
	}
	modelBudget := threadRuntime.ModelBudget
	errorText := ""
	if runErr != nil {
		errorText = runErr.Error()
	}
	turnRecord := sessiontrace.TurnRecord{
		ThreadID:            threadID,
		TurnID:              turn.ID,
		Status:              string(turn.Status),
		ProviderName:        providerName,
		Model:               model,
		APIModel:            apiModel,
		ModelProfile:        sessiontrace.NewModelProfileRecordWithBudget(providerName, model, apiModel, modelBudget),
		PermissionMode:      permissions.Mode,
		StartedAt:           turn.StartedAt,
		CompletedAt:         turn.CompletedAt,
		DurationMS:          turn.DurationMS,
		InputTokens:         res.InputTokens,
		OutputTokens:        res.OutputTokens,
		CacheCreationTokens: res.CacheCreationTokens,
		CacheReadTokens:     res.CacheReadTokens,
		FinishReason:        string(res.FinishReason),
		StopReason:          res.StopReason,
		Truncated:           res.Truncated,
		HistoryRewritten:    res.HistoryRewritten,
		DriverID:            res.DriverID,
		DriverVersion:       res.DriverVersion,
		DriverContract:      res.DriverContractVersion,
		DriverStatus:        res.DriverStatus,
		DriverCheckpoint:    append(json.RawMessage(nil), res.DriverCheckpoint...),
		Error:               errorText,
	}
	finalRecord := sessiontrace.FinalRecord{
		Status:              string(turn.Status),
		InputTokens:         res.InputTokens,
		OutputTokens:        res.OutputTokens,
		CacheCreationTokens: res.CacheCreationTokens,
		CacheReadTokens:     res.CacheReadTokens,
		FinishReason:        string(res.FinishReason),
		StopReason:          res.StopReason,
		Truncated:           res.Truncated,
		FinalAnswerPreview:  res.Content,
		Error:               errorText,
	}
	records := threadRuntime.Toolkit.ToolTelemetry()
	if toolRecordStart > 0 && toolRecordStart < len(records) {
		records = records[toolRecordStart:]
	} else if toolRecordStart >= len(records) {
		records = nil
	}
	var barrierRejections []sessiontrace.BarrierToolBatchRejectionRecord
	if len(barrierRejectionsArg) > 0 {
		barrierRejections = barrierRejectionsArg[0]
	}
	if err := sessiontrace.AppendTurn(tracePath, turnRecord, finalRecord, threadRuntime.Toolkit.ToolInfos(), records, contextRequests, providerStates, compactAttempts, barrierRejections); err != nil {
		return "", err
	}
	return tracePath, nil
}

func compactRecord(info agent.CompactAttemptInfo) sessiontrace.CompactRecord {
	return sessiontrace.CompactRecord{
		Reason:            string(info.Reason),
		Status:            string(info.Status),
		TokensBefore:      info.TokensBefore,
		LastResponseTotal: info.LastResponseTotal,
		PendingDelta:      info.PendingDelta,
		UsageAdjustment:   string(info.UsageAdjustment),
		MessagesBefore:    info.MessagesBefore,
		MessagesAfter:     info.MessagesAfter,
		Error:             info.Error,
	}
}

func barrierToolBatchRejectionRecord(info agent.ToolBatchRejectionInfo) sessiontrace.BarrierToolBatchRejectionRecord {
	return sessiontrace.BarrierToolBatchRejectionRecord{
		StepIndex:     info.StepIndex,
		BarrierTool:   info.BarrierTool,
		SiblingTools:  append([]string(nil), info.SiblingTools...),
		ToolCallCount: info.ToolCallCount,
	}
}

func providerStateRecord(state *providers.ProviderStateSummary) sessiontrace.ProviderStateRecord {
	if state == nil {
		return sessiontrace.ProviderStateRecord{}
	}
	return sessiontrace.ProviderStateRecord{
		StepIndex:              state.StepIndex,
		Provider:               state.Provider,
		Protocol:               state.Protocol,
		Transport:              state.Transport,
		ReplayMode:             state.ReplayMode,
		PreviousResponseIDUsed: state.PreviousResponseIDUsed,
		ConnectionReused:       state.ConnectionReused,
		Diagnostic:             state.Diagnostic,
		TransportFailurePhase:  state.TransportFailurePhase,
		FallbackTransport:      state.FallbackTransport,
		EventsEmitted:          state.EventsEmitted,
		FallbackActive:         state.FallbackActive,
		FallbackReason:         state.FallbackReason,
		FallbackPinStatus:      state.FallbackPinStatus,
		FallbackRetryAfterMS:   state.FallbackRetryAfterMS,
		FallbackTTLMS:          state.FallbackTTLMS,
		InputItems:             state.InputItems,
		FullInputItems:         state.FullInputItems,
		DeltaInputItems:        state.DeltaInputItems,
	}
}

func attachUsageToLatestRequestContext(records []sessiontrace.RequestContextRecord, usage providers.TokenUsage) {
	if len(records) == 0 {
		return
	}
	record := &records[len(records)-1]
	record.InputTokens = usage.InputTokens
	record.OutputTokens = usage.OutputTokens
	record.CacheCreationTokens = usage.CacheCreationTokens
	record.CacheReadTokens = usage.CacheReadTokens
}

func (s *Server) enqueueAgentCompletionTurn(threadID, agentID, resultID string, msg providers.ChatMessage, snapshot *subagent.SubAgentSnapshot) {
	if s == nil || s.closed.Load() {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || !chatMessageHasUserPayload(msg) {
		return
	}
	th := s.thread(threadID)
	if th == nil || !canResumeAgentCompletionThread(th) {
		return
	}
	if strings.TrimSpace(msg.Role) == "" {
		msg.Role = "user"
	}

	s.agentCompletionMu.Lock()
	if s.closed.Load() {
		s.agentCompletionMu.Unlock()
		return
	}
	if s.pendingAgentCompletionTurns == nil {
		s.pendingAgentCompletionTurns = make(map[string][]agentCompletionTurn)
	}
	resultID = strings.TrimSpace(resultID)
	if resultID != "" {
		for _, pending := range s.pendingAgentCompletionTurns[threadID] {
			if strings.TrimSpace(pending.resultID) == resultID {
				s.agentCompletionMu.Unlock()
				return
			}
		}
	}
	s.pendingAgentCompletionTurns[threadID] = append(s.pendingAgentCompletionTurns[threadID], agentCompletionTurn{
		agentID:  strings.TrimSpace(agentID),
		resultID: resultID,
		msg:      msg,
		snapshot: cloneSubAgentSnapshot(snapshot),
	})
	s.agentCompletionMu.Unlock()

	s.kickAgentCompletionDrain(threadID)
}

func (s *Server) enqueueQueuedUserTurn(threadID string, entry queuedTurn) bool {
	return s.enqueueQueuedUserTurnWithPolicy(threadID, entry, false)
}

func (s *Server) enqueueRequeuedUserTurn(threadID string, entry queuedTurn) bool {
	return s.enqueueQueuedUserTurnWithPolicy(threadID, entry, true)
}

func (s *Server) enqueueQueuedUserTurnWithPolicy(threadID string, entry queuedTurn, supersedeCancellation bool) bool {
	if s == nil || s.closed.Load() {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	entry.id = strings.TrimSpace(entry.id)
	if threadID == "" || entry.id == "" || !chatMessageHasUserPayload(entry.msg) {
		return false
	}
	if strings.TrimSpace(entry.msg.Role) == "" {
		entry.msg.Role = "user"
	}
	// Keep an upstream request identity distinct from the local queue identity.
	// Plugin session sends use ClientID for idempotency across lifecycle replay;
	// replacing it with the queue ID makes the same request look new on retry.
	if strings.TrimSpace(entry.msg.ClientID) == "" {
		entry.msg.ClientID = entry.id
	}
	entry.msg.Steered = false

	s.queuedTurnMu.Lock()
	if s.closed.Load() {
		s.queuedTurnMu.Unlock()
		return false
	}
	if s.pendingQueuedTurns == nil {
		s.pendingQueuedTurns = make(map[string][]queuedTurn)
	}
	if supersedeCancellation {
		// A queue cancellation from a stale client belongs to this message's
		// previous delivery mode. An explicit steer -> queue transition is the
		// new authoritative intent for the stable id and supersedes it.
		key := queuedTurnClaimKey(threadID, entry.id)
		if s.cancelledPendingSubmissions[key] == session.HeldUserWorkOriginQueue {
			delete(s.cancelledPendingSubmissions, key)
		}
	}
	if s.isCancelledPendingSubmissionLocked(threadID, entry.id, session.HeldUserWorkOriginQueue) {
		s.queuedTurnMu.Unlock()
		return false
	}
	for _, pending := range s.pendingQueuedTurns[threadID] {
		if pending.id == entry.id {
			s.queuedTurnMu.Unlock()
			return false
		}
	}
	if claim := s.claimedQueuedTurns[queuedTurnClaimKey(threadID, entry.id)]; claim != nil && !claim.cancelled {
		s.queuedTurnMu.Unlock()
		return false
	}
	s.pendingQueuedTurns[threadID] = append(s.pendingQueuedTurns[threadID], entry)
	s.queuedTurnMu.Unlock()
	return true
}

func (s *Server) recordCancelledPendingSubmission(threadID, id, origin string) {
	key := queuedTurnClaimKey(threadID, id)
	if key == "\x00" {
		return
	}
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	if s.cancelledPendingSubmissions == nil {
		s.cancelledPendingSubmissions = make(map[string]string)
	}
	if len(s.cancelledPendingSubmissions) >= maxCancelledPendingSubmissions {
		clear(s.cancelledPendingSubmissions)
	}
	s.cancelledPendingSubmissions[key] = origin
}

func (s *Server) isCancelledPendingSubmission(threadID, id, origin string) bool {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	return s.isCancelledPendingSubmissionLocked(threadID, id, origin)
}

func (s *Server) isCancelledPendingSubmissionLocked(threadID, id, origin string) bool {
	key := queuedTurnClaimKey(threadID, id)
	return s.cancelledPendingSubmissions[key] == origin
}

func (s *Server) removeQueuedUserTurn(threadID, queueID string) (queuedTurn, bool) {
	threadID = strings.TrimSpace(threadID)
	queueID = strings.TrimSpace(queueID)
	if threadID == "" || queueID == "" {
		return queuedTurn{}, false
	}
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	pending := s.pendingQueuedTurns[threadID]
	next := pending[:0]
	var removed queuedTurn
	found := false
	for _, entry := range pending {
		if !found && entry.id == queueID {
			removed = entry
			found = true
			continue
		}
		next = append(next, entry)
	}
	if len(next) == 0 {
		delete(s.pendingQueuedTurns, threadID)
	} else {
		s.pendingQueuedTurns[threadID] = next
	}
	if !found {
		claim := s.claimedQueuedTurns[queuedTurnClaimKey(threadID, queueID)]
		if claim != nil && !claim.cancelled && !claim.committed {
			claim.cancelled = true
			removed = claim.entry
			found = true
		}
	}
	return removed, found
}

func (s *Server) replaceQueuedUserTurn(threadID, queueID string, msg providers.ChatMessage) (queuedTurn, bool) {
	threadID = strings.TrimSpace(threadID)
	queueID = strings.TrimSpace(queueID)
	if threadID == "" || queueID == "" || !chatMessageHasUserPayload(msg) {
		return queuedTurn{}, false
	}
	if strings.TrimSpace(msg.Role) == "" {
		msg.Role = "user"
	}
	msg.ClientID = queueID
	msg.Steered = false

	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	pending := s.pendingQueuedTurns[threadID]
	for index, entry := range pending {
		if entry.id != queueID {
			continue
		}
		updated := queuedTurn{id: queueID, msg: msg, snapshot: entry.snapshot}
		pending[index] = updated
		s.pendingQueuedTurns[threadID] = pending
		return updated, true
	}
	return queuedTurn{}, false
}

func (s *Server) findQueuedUserTurn(threadID, queueID string) (queuedTurn, bool) {
	threadID = strings.TrimSpace(threadID)
	queueID = strings.TrimSpace(queueID)
	if threadID == "" || queueID == "" {
		return queuedTurn{}, false
	}
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	for _, entry := range s.pendingQueuedTurns[threadID] {
		if entry.id == queueID {
			return entry, true
		}
	}
	claim := s.claimedQueuedTurns[queuedTurnClaimKey(threadID, queueID)]
	if claim != nil && !claim.cancelled {
		return claim.entry, true
	}
	return queuedTurn{}, false
}

func (s *Server) kickQueuedTurnDrain(threadID string) {
	s.tryDrainQueuedTurns(threadID, false)
}

func (s *Server) tryDrainQueuedTurns(threadID string, synchronous bool) (capacityFull bool) {
	if s == nil || s.closed.Load() {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	if s.activeExecutionRunID(threadID) != "" {
		return
	}

	s.queuedTurnMu.Lock()
	if s.closed.Load() {
		s.queuedTurnMu.Unlock()
		return
	}
	if len(s.pendingQueuedTurns[threadID]) == 0 || s.drainingQueuedTurns[threadID] {
		s.queuedTurnMu.Unlock()
		return
	}
	if s.drainingQueuedTurns == nil {
		s.drainingQueuedTurns = make(map[string]bool)
	}
	s.drainingQueuedTurns[threadID] = true
	s.queuedTurnMu.Unlock()

	if synchronous {
		return s.drainQueuedTurns(threadID)
	}
	_ = s.startBackground(func() { s.drainQueuedTurns(threadID) })
	return
}

func (s *Server) drainQueuedTurns(threadID string) (capacityFull bool) {
	if s == nil {
		return
	}
	if s.closed.Load() {
		entries := s.discardQueuedTurns(threadID)
		for _, entry := range entries {
			s.notifyPluginTurnDiscardedWithRetry(threadID, entry, "app-server closed before queued turn started", true)
		}
		s.clearQueuedTurnDrain(threadID)
		return
	}
	th := s.thread(threadID)
	if th == nil {
		discardedEntries := s.discardQueuedTurns(threadID)
		for _, entry := range discardedEntries {
			s.notifyPluginTurnDiscarded(threadID, entry, "thread no longer exists")
		}
		s.clearQueuedTurnDrain(threadID)
		return
	}
	if threadIsRunning(th) {
		s.clearQueuedTurnDrain(threadID)
		// Completion may have tried to kick the queue while this drain still
		// owned the marker and been rejected as a duplicate. Recheck after
		// releasing ownership so that interleaving cannot lose the only wake-up.
		if !threadIsRunning(th) {
			s.kickQueuedTurnDrain(threadID)
		}
		return
	}

	entry, ok := s.takeNextQueuedUserTurn(threadID)
	if !ok {
		s.clearQueuedTurnDrain(threadID)
		return
	}
	started, err := s.startQueuedTurn(context.Background(), threadID, entry)
	executionBusy := errors.Is(err, errThreadExecutionBusy)
	retryableAdmission := errors.Is(err, errRetryableTurnAdmission)
	capacityFull = errors.Is(err, session.ErrProjectWorkerCapacity)
	requeueCandidate := !started && (err == nil || executionBusy || retryableAdmission)
	cancelled := s.settleQueuedTurnClaim(threadID, entry, requeueCandidate)
	if errors.Is(err, errQueuedTurnCancelled) || cancelled {
		err = errQueuedTurnCancelled
		executionBusy = false
		retryableAdmission = false
		requeueCandidate = false
	}
	if err != nil && !executionBusy && !retryableAdmission && !errors.Is(err, errQueuedTurnCancelled) {
		providers.DebugLogf("start queued turn for thread %q: %v", threadID, err)
		if reference := entry.snapshot.PluginTurn; reference != nil {
			// Terminal observation: persist to the outbox and deliver in the
			// background. The drain must never synchronously re-enter a plugin
			// helper that may be blocked inside a host service call waiting on
			// this very drain (single-worker plugin processes).
			s.notifyPluginTurnLifecycleAsync(reference.PluginID, pluginhost.AgentTurnLifecycleInput{
				RequestID: reference.RequestID, State: pluginhost.TurnLifecycleFailed,
				ThreadID: threadID, QueueID: reference.QueueID, Error: err.Error(),
			})
		}
		if entry.snapshot.PluginTurn == nil {
			if holdErr := s.holdRejectedQueuedTurn(threadID, entry, err); holdErr == nil {
				err = nil
			} else {
				providers.DebugLogf("hold rejected queued turn for thread %q: %v", threadID, holdErr)
			}
		}
		if err != nil {
			_ = s.writeNotification(NotificationTurnDequeued, TurnDequeuedNotification{
				ThreadID: threadID,
				QueueID:  entry.id,
			})
		}
	}
	requeued := requeueCandidate && !cancelled
	s.clearQueuedTurnDrain(threadID)
	if requeued && (executionBusy || retryableAdmission) {
		if capacityFull && s.deferProjectCapacityRetry(threadID, "queued", func() bool { return s.tryDrainQueuedTurns(threadID, true) }) {
			return
		}
		s.scheduleThreadExecutionLeaseRetry(func() { s.kickQueuedTurnDrain(threadID) })
		return
	}
	if requeued || s.hasQueuedUserTurns(threadID) {
		s.kickQueuedTurnDrain(threadID)
	}
	return
}

func (s *Server) startThreadUserTurn(ctx context.Context, th *threadState, userMsg providers.ChatMessage, snapshot turnRuntimeSnapshot, failIfRunning bool, readOnlyPolicy turnReadOnlyPolicy) (startedThreadTurn, bool, error) {
	return s.startThreadUserTurnWithAdmission(ctx, th, userMsg, snapshot, failIfRunning, readOnlyPolicy, turnAdmissionHooks{})
}

// startThreadUserTurnWithAdmission owns every durable pre-turn side effect.
// It acquires the cross-process lease and refreshes disk state before running
// hooks, then keeps ownership through the user append and turn lifecycle.
func (s *Server) startThreadUserTurnWithAdmission(ctx context.Context, th *threadState, userMsg providers.ChatMessage, snapshot turnRuntimeSnapshot, failIfRunning bool, readOnlyPolicy turnReadOnlyPolicy, hooks turnAdmissionHooks) (startedThreadTurn, bool, error) {
	if th == nil {
		return startedThreadTurn{}, false, errors.New("thread is required")
	}
	if s == nil || s.closed.Load() {
		return startedThreadTurn{}, false, errServerClosed
	}
	if strings.TrimSpace(userMsg.Role) == "" {
		userMsg.Role = "user"
	}
	if !chatMessageHasUserPayload(userMsg) {
		return startedThreadTurn{}, false, nil
	}
	if failIfRunning {
		if err := s.waitAndHandoffAnswerReadyTurn(ctx, th); err != nil {
			return startedThreadTurn{}, false, err
		}
	}
	turnID := session.NewID()
	turnCtx, cancel := context.WithCancel(ctx)
	now := time.Now().UTC()

	th.mu.Lock()
	if s.closed.Load() {
		th.mu.Unlock()
		cancel()
		return startedThreadTurn{}, false, errServerClosed
	}
	if th.running {
		th.mu.Unlock()
		cancel()
		if failIfRunning {
			return startedThreadTurn{}, false, fmt.Errorf("thread %q already has a running turn", th.ID)
		}
		return startedThreadTurn{}, false, nil
	}
	if th.ReadOnly {
		switch readOnlyPolicy {
		case turnReadOnlyFail:
			th.mu.Unlock()
			cancel()
			return startedThreadTurn{}, false, errors.New("thread is read-only")
		case turnReadOnlySkip:
			th.mu.Unlock()
			cancel()
			return startedThreadTurn{}, false, nil
		}
	}
	acquired, err := s.tryAcquireThreadExecutionLeaseLocked(th)
	if err != nil {
		th.mu.Unlock()
		cancel()
		return startedThreadTurn{}, false, err
	}
	if !acquired {
		threadID := th.ID
		th.mu.Unlock()
		cancel()
		return startedThreadTurn{}, false, threadExecutionBusyError(threadID)
	}
	if err := s.refreshDurableThreadHistoryLocked(th); err != nil {
		th.releaseThreadExecutionLeaseLocked()
		th.mu.Unlock()
		cancel()
		return startedThreadTurn{}, false, err
	}
	if th.ReadOnly {
		switch readOnlyPolicy {
		case turnReadOnlyFail:
			th.releaseThreadExecutionLeaseLocked()
			th.mu.Unlock()
			cancel()
			return startedThreadTurn{}, false, errors.New("thread is read-only")
		case turnReadOnlySkip:
			th.releaseThreadExecutionLeaseLocked()
			th.mu.Unlock()
			cancel()
			return startedThreadTurn{}, false, nil
		}
	}
	th.mu.Unlock()

	abortAdmission := func() {
		th.mu.Lock()
		th.releaseThreadExecutionLeaseLocked()
		th.mu.Unlock()
		cancel()
	}
	if err := s.validateInboxInput(userMsg); err != nil {
		abortAdmission()
		return startedThreadTurn{}, false, err
	}
	if snapshot.Control != nil {
		if err := session.ValidateControl(s.rt.SessionDir, *snapshot.Control); err != nil {
			abortAdmission()
			return startedThreadTurn{}, false, err
		}
	}
	if userMsg.ClientID != "" && isGeneratedSessionInput(userMsg.Origin) {
		if _, found := s.findSessionInput(th, userMsg.ClientID); found {
			abortAdmission()
			return startedThreadTurn{}, false, errSessionInputApplied
		}
	}
	if hooks.afterLease != nil {
		if err := hooks.afterLease(th, &userMsg); err != nil {
			abortAdmission()
			return startedThreadTurn{}, false, err
		}
	}

	th.mu.Lock()
	threadID := th.ID
	threadCWD := th.CWD
	th.mu.Unlock()
	if !chatMessageHasUserPayload(userMsg) {
		abortAdmission()
		return startedThreadTurn{}, false, nil
	}
	if s.rt != nil && s.rt.HookDispatcher != nil {
		if _, err := s.rt.HookDispatcher.Dispatch(ctx, hookspkg.UserPromptSubmit, &hookspkg.Input{
			SessionID: threadID, CWD: threadCWD, Prompt: userMsg.Content,
		}); err != nil {
			abortAdmission()
			return startedThreadTurn{}, false, fmt.Errorf("user prompt hook: %w", err)
		}
	}

	th.mu.Lock()
	if s.closed.Load() {
		th.releaseThreadExecutionLeaseLocked()
		th.mu.Unlock()
		cancel()
		return startedThreadTurn{}, false, errServerClosed
	}
	// running cannot become true while executionLease is our local admission
	// reservation, but retain the guard so future non-turn writers fail closed.
	if th.running {
		th.releaseThreadExecutionLeaseLocked()
		th.mu.Unlock()
		cancel()
		if failIfRunning {
			return startedThreadTurn{}, false, fmt.Errorf("thread %q already has a running turn", th.ID)
		}
		return startedThreadTurn{}, false, nil
	}
	if th.ReadOnly {
		switch readOnlyPolicy {
		case turnReadOnlyFail:
			th.releaseThreadExecutionLeaseLocked()
			th.mu.Unlock()
			cancel()
			return startedThreadTurn{}, false, errors.New("thread is read-only")
		case turnReadOnlySkip:
			th.releaseThreadExecutionLeaseLocked()
			th.mu.Unlock()
			cancel()
			return startedThreadTurn{}, false, nil
		}
	}
	var commitAfterAppend func() error
	if hooks.beforeUserAppendLocked != nil {
		var err error
		commitAfterAppend, err = hooks.beforeUserAppendLocked(th)
		if err != nil {
			th.releaseThreadExecutionLeaseLocked()
			th.mu.Unlock()
			cancel()
			return startedThreadTurn{}, false, err
		}
	}
	var userMsgSeq int
	userAlreadyPersisted := false
	if clientID := strings.TrimSpace(userMsg.ClientID); clientID != "" {
		for _, existing := range th.History {
			if strings.TrimSpace(existing.ClientID) == clientID {
				userAlreadyPersisted = true
				userMsgSeq = existing.Seq
				break
			}
		}
	}
	if th.PersistHistory && !userAlreadyPersisted {
		seq, err := appendControlledChatMessage(s.rt.SessionDir, th.ID, userMsg, snapshot.Control, snapshot.ExecutionControlBaseline)
		if err != nil {
			th.releaseThreadExecutionLeaseLocked()
			th.mu.Unlock()
			cancel()
			return startedThreadTurn{}, false, err
		}
		userMsgSeq = seq
		th.historyHeadSeq = max(th.historyHeadSeq, seq)
	}
	userMsg.Seq = userMsgSeq
	if commitAfterAppend != nil {
		if err := commitAfterAppend(); err != nil {
			th.releaseThreadExecutionLeaseLocked()
			th.mu.Unlock()
			cancel()
			return startedThreadTurn{}, false, errors.Join(errRetryableTurnAdmission, err)
		}
	}
	history := cloneHistory(th.History)
	if !userAlreadyPersisted {
		history = append(history, userMsg)
	}
	th.History = history
	th.cancel = cancel
	var turn Turn
	resumed := false
	if userAlreadyPersisted {
		turn, resumed = th.resumePersistedUserTurnLocked(userMsg.ClientID, now)
	}
	if !resumed {
		if th.PersistHistory {
			// Persisted turns are reconstructed from conversation order after a
			// restart or cross-process refresh. Give the live turn that same stable
			// ID now so item/turn references returned to clients remain valid after
			// the next admission refresh.
			turnID = fmt.Sprintf("%s-turn-%04d", th.ID, len(th.Turns)+1)
		}
		turn = th.startTurnLocked(turnID, userMsg, now)
	} else {
		turnID = turn.ID
	}
	turnRuntime := turnRuntimeSnapshotLocked(th)
	if snapshot.hasPermissions() || snapshot.PermissionExplicit {
		turnRuntime = turnRuntime.withPermissions(snapshot.permissions())
		turnRuntime.PermissionExplicit = snapshot.PermissionExplicit
	}
	turnRuntime.ForceCompact = snapshot.ForceCompact
	turnRuntime.CompactOnly = snapshot.CompactOnly
	turnRuntime.HistoryBaselineSeq = th.historyHeadSeq
	turnRuntime.AgentCompletionResultIDs = append([]string(nil), snapshot.AgentCompletionResultIDs...)
	turnRuntime.ProcessCompletionIDs = append([]string(nil), snapshot.ProcessCompletionIDs...)
	// Completion results folded from a lifted tree freeze are answered by
	// this user turn (foldFrozenWorkerTree staged them under the same lock
	// discipline as the snapshot fields).
	if len(th.frozenTreeResultIDs) > 0 {
		turnRuntime.AgentCompletionResultIDs = append(turnRuntime.AgentCompletionResultIDs, th.frozenTreeResultIDs...)
		th.frozenTreeResultIDs = nil
	}
	turnRuntime.ExecutionRunID = snapshot.ExecutionRunID
	turnRuntime.PluginTurn = clonePluginTurnReference(snapshot.PluginTurn)
	th.currentExecutionRunID = turnRuntime.ExecutionRunID
	turnRuntime.RequestContext = cloneContextSegments(snapshot.RequestContext)
	th.mu.Unlock()
	s.noticeProjectUserMessage(th, userMsg)

	return startedThreadTurn{
		ctx:        turnCtx,
		cancel:     cancel,
		turnID:     turnID,
		turn:       turn,
		runtime:    turnRuntime,
		history:    history,
		admittedAt: now,
		userMsgSeq: userMsgSeq,
	}, true, nil
}

func (s *Server) startQueuedTurn(ctx context.Context, threadID string, entry queuedTurn) (bool, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, errors.New("thread_id is required")
	}
	if strings.TrimSpace(entry.msg.Role) == "" {
		entry.msg.Role = "user"
	}
	if !chatMessageHasUserPayload(entry.msg) {
		return false, nil
	}
	entry.id = strings.TrimSpace(entry.id)
	if entry.id == "" {
		entry.id = session.NewID()
	}
	if strings.TrimSpace(entry.msg.ClientID) == "" {
		entry.msg.ClientID = entry.id
	}
	entry.msg.Steered = false

	th := s.thread(threadID)
	if th == nil {
		return false, fmt.Errorf("thread %q not found", threadID)
	}
	var threadRuntime *runtime.ThreadRuntime
	// Permissions are re-resolved at start time, never trusted from the
	// queue-time snapshot: a permission change landing while the turn waited
	// in the queue must govern the turn that actually runs.
	permissions, err := s.resolveThreadTurnPermissions(th, nil)
	if err != nil {
		return false, err
	}

	snapshot := entry.snapshot.withPermissions(permissions)
	started, ok, err := s.startThreadUserTurnWithAdmission(
		ctx,
		th,
		entry.msg,
		snapshot,
		false,
		turnReadOnlyFail,
		turnAdmissionHooks{
			afterLease: func(admitted *threadState, _ *providers.ChatMessage) error {
				var runtimeErr error
				threadRuntime, runtimeErr = s.ensureThreadRuntimeAfterAdmission(admitted)
				if runtimeErr != nil {
					return runtimeErr
				}
				return gateAlreadyDeliveredCompletions(admitted.History, threadRuntime, snapshot.AgentCompletionResultIDs, snapshot.ProcessCompletionIDs)
			},
			beforeUserAppendLocked: func(_ *threadState) (func() error, error) {
				if err := s.commitQueuedTurnClaim(threadID, entry.id); err != nil {
					return nil, err
				}
				return nil, nil
			},
		},
	)
	if errors.Is(err, errAgentCompletionAlreadyDelivered) {
		return true, nil
	}
	if err != nil || !ok {
		return ok, err
	}
	if s.beforeQueuedTurnBackgroundForTest != nil {
		s.beforeQueuedTurnBackgroundForTest()
	}
	launch, accepted := s.reserveBackground(func() {
		s.runTurn(started.ctx, th, threadRuntime, started.turnID, started.runtime, started.history)
	})
	if !accepted {
		persistErr := s.abortStartedThreadTurnDurably(th, started, errServerClosed)
		return false, errors.Join(errServerClosed, persistErr)
	}
	defer launch.Cancel()

	if err := s.writeNotification(NotificationTurnStarted, TurnStartedNotification{
		ThreadID: threadID,
		Turn:     started.turn,
		QueueID:  entry.id,
	}); err != nil {
		return false, errors.Join(err, s.abortStartedThreadTurnDurably(th, started, err))
	}
	if len(entry.followups) > 0 {
		th.mu.Lock()
		if th.currentTurn == started.turnID {
			for _, followup := range entry.followups {
				followup.Steered = true
				th.pendingSteers = append(th.pendingSteers, followup)
			}
			th.signalSteerWakeLocked()
		}
		th.mu.Unlock()
	}
	if entry.resumeBrowser != nil {
		entry.resumeBrowser()
	}
	launch.Commit()
	if reference := started.runtime.PluginTurn; reference != nil {
		// Best-effort background observation. Plugin helpers are single-worker
		// processes; a synchronous call from the drain can re-enter a helper
		// that is blocked in a host service waiting on this drain.
		s.notifyPluginTurnLifecycleAsync(reference.PluginID, pluginhost.AgentTurnLifecycleInput{
			RequestID: reference.RequestID, State: pluginhost.TurnLifecycleRunning,
			ThreadID: threadID, TurnID: started.turnID, QueueID: reference.QueueID,
			StartedAt: &started.admittedAt,
		})
	}
	return true, nil
}

func gateAlreadyDeliveredCompletions(history []providers.ChatMessage, threadRuntime *runtime.ThreadRuntime, agentResultIDs, processIDs []string) error {
	agentResultIDs = uniqueSortedCompletionIDs(agentResultIDs)
	processIDs = uniqueSortedCompletionIDs(processIDs)
	if len(agentResultIDs) == 0 && len(processIDs) == 0 {
		return nil
	}
	if len(agentResultIDs) > 0 {
		if threadRuntime == nil || threadRuntime.AgentControl == nil {
			return errors.Join(errRetryableTurnAdmission, errors.New("agent completion control is unavailable"))
		}
		for _, resultID := range agentResultIDs {
			consumer, err := threadRuntime.AgentControl.AgentResultDeliveryConsumer(resultID)
			if err != nil {
				return errors.Join(errRetryableTurnAdmission, err)
			}
			if consumer != "" {
				return errAgentCompletionAlreadyDelivered
			}
			if agentCompletionMarkerAnswered(history, resultID) {
				claimed, consumedBy, err := threadRuntime.AgentControl.ClaimAgentResultDeliveryID(resultID, "auto_completion")
				if err != nil {
					return errors.Join(errRetryableTurnAdmission, err)
				}
				if !claimed && consumedBy == "" {
					return errors.Join(errRetryableTurnAdmission, fmt.Errorf("agent result delivery %q is unavailable", resultID))
				}
				return errAgentCompletionAlreadyDelivered
			}
		}
	}
	if len(processIDs) > 0 {
		if threadRuntime == nil || threadRuntime.ProcessManager == nil {
			return errors.Join(errRetryableTurnAdmission, errors.New("process completion manager is unavailable"))
		}
		for _, processID := range processIDs {
			pending, err := threadRuntime.ProcessManager.CompletionPending(processID)
			if err != nil {
				return errors.Join(errRetryableTurnAdmission, err)
			}
			if !pending {
				return errAgentCompletionAlreadyDelivered
			}
			if processCompletionMarkerAnswered(history, processID) {
				if _, err := threadRuntime.ProcessManager.MarkCompletionDelivered(processID, "history_answer"); err != nil {
					return errors.Join(errRetryableTurnAdmission, err)
				}
				return errAgentCompletionAlreadyDelivered
			}
		}
	}
	return nil
}

func (s *Server) takeNextQueuedUserTurn(threadID string) (queuedTurn, bool) {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	pending := s.pendingQueuedTurns[threadID]
	if len(pending) == 0 {
		return queuedTurn{}, false
	}
	index := 0
	for candidate := range pending {
		if pending[candidate].snapshot.PluginTurn == nil {
			index = candidate
			break
		}
	}
	entry := pending[index]
	if len(pending) == 1 {
		delete(s.pendingQueuedTurns, threadID)
	} else {
		next := append([]queuedTurn(nil), pending[:index]...)
		s.pendingQueuedTurns[threadID] = append(next, pending[index+1:]...)
	}
	if s.claimedQueuedTurns == nil {
		s.claimedQueuedTurns = make(map[string]*queuedTurnClaim)
	}
	s.claimedQueuedTurns[queuedTurnClaimKey(threadID, entry.id)] = &queuedTurnClaim{entry: entry}
	return entry, true
}

func queuedTurnClaimKey(threadID, queueID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(queueID)
}

// commitQueuedTurnClaim runs while the thread lock is held immediately before
// the user message append. A dequeue that wins first prevents the append;
// after this point the message is committed and cancellation is too late.
func (s *Server) commitQueuedTurnClaim(threadID, queueID string) error {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	claim := s.claimedQueuedTurns[queuedTurnClaimKey(threadID, queueID)]
	if claim == nil {
		return nil
	}
	if claim.cancelled {
		return errQueuedTurnCancelled
	}
	claim.committed = true
	return nil
}

// settleQueuedTurnClaim closes the temporary ownership gap between taking an
// entry and committing or putting it back. Claim removal and requeue happen
// under one lock, so cancellation can never miss both representations.
func (s *Server) settleQueuedTurnClaim(threadID string, entry queuedTurn, requeue bool) bool {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	key := queuedTurnClaimKey(threadID, entry.id)
	claim := s.claimedQueuedTurns[key]
	cancelled := claim != nil && claim.cancelled
	delete(s.claimedQueuedTurns, key)
	if requeue && !cancelled {
		existing := append([]queuedTurn(nil), s.pendingQueuedTurns[threadID]...)
		s.pendingQueuedTurns[threadID] = append([]queuedTurn{entry}, existing...)
	}
	return cancelled
}

func (s *Server) prependQueuedUserTurns(threadID string, entries []queuedTurn) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || len(entries) == 0 {
		return
	}
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	if s.closed.Load() {
		return
	}
	if s.pendingQueuedTurns == nil {
		s.pendingQueuedTurns = make(map[string][]queuedTurn)
	}
	existing := append([]queuedTurn(nil), s.pendingQueuedTurns[threadID]...)
	s.pendingQueuedTurns[threadID] = append(append([]queuedTurn(nil), entries...), existing...)
}

func (s *Server) hasQueuedUserTurns(threadID string) bool {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	return len(s.pendingQueuedTurns[threadID]) > 0
}

func (s *Server) hasQueuedUserWork(threadID string) bool {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	return len(s.pendingQueuedTurns[threadID]) > 0 || s.drainingQueuedTurns[threadID]
}

func (s *Server) discardQueuedUserTurns(threadID string) []string {
	return queuedTurnIDs(s.discardQueuedTurns(threadID))
}

func (s *Server) discardQueuedTurns(threadID string) []queuedTurn {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	entries := append([]queuedTurn(nil), s.pendingQueuedTurns[threadID]...)
	delete(s.pendingQueuedTurns, threadID)
	return entries
}

func (s *Server) discardQueuedUserWork(threadID string) []string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	queueIDs := queuedTurnIDs(s.pendingQueuedTurns[threadID])
	delete(s.pendingQueuedTurns, threadID)
	delete(s.drainingQueuedTurns, threadID)
	return queueIDs
}

func queuedTurnIDs(entries []queuedTurn) []string {
	if len(entries) == 0 {
		return nil
	}
	queueIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if id := strings.TrimSpace(entry.id); id != "" {
			queueIDs = append(queueIDs, id)
		}
	}
	return queueIDs
}

func (s *Server) notifyQueuedTurnsDequeued(threadID string, queueIDs []string) {
	for _, queueID := range queueIDs {
		_ = s.writeNotification(NotificationTurnDequeued, TurnDequeuedNotification{
			ThreadID: threadID,
			QueueID:  queueID,
		})
	}
}

func (s *Server) clearQueuedTurnDrain(threadID string) {
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	delete(s.drainingQueuedTurns, threadID)
}

func (s *Server) kickAgentCompletionDrain(threadID string) {
	s.tryDrainAgentCompletionTurns(threadID, false)
}

func (s *Server) tryDrainAgentCompletionTurns(threadID string, synchronous bool) (capacityFull bool) {
	if s == nil || s.closed.Load() {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}

	s.agentCompletionMu.Lock()
	if s.closed.Load() {
		s.agentCompletionMu.Unlock()
		return
	}
	if len(s.pendingAgentCompletionTurns[threadID]) == 0 || s.drainingAgentCompletionTurns[threadID] {
		s.agentCompletionMu.Unlock()
		return
	}
	if s.drainingAgentCompletionTurns == nil {
		s.drainingAgentCompletionTurns = make(map[string]bool)
	}
	s.drainingAgentCompletionTurns[threadID] = true
	s.agentCompletionMu.Unlock()

	if synchronous {
		return s.drainAgentCompletionTurns(threadID)
	}
	_ = s.startBackground(func() { s.drainAgentCompletionTurns(threadID) })
	return
}

func (s *Server) drainAgentCompletionTurns(threadID string) (capacityFull bool) {
	if s == nil {
		return
	}
	if s.closed.Load() {
		s.discardPendingAgentCompletionTurns(threadID)
		s.clearAgentCompletionDrain(threadID)
		return
	}
	th := s.thread(threadID)
	if th == nil || !canResumeAgentCompletionThread(th) {
		s.discardPendingAgentCompletionTurns(threadID)
		s.clearAgentCompletionDrain(threadID)
		return
	}
	if threadIsRunning(th) {
		s.clearAgentCompletionDrain(threadID)
		// Match the queued-user drain handshake: a completion kick can race with
		// this owner releasing its marker, so recheck after the release before
		// allowing the wake-up to disappear.
		if !threadIsRunning(th) {
			s.kickAgentCompletionDrain(threadID)
		}
		return
	}
	// User-authored work wins over automatic completion wakeups. The user turn
	// will kick this drain again after it reaches a terminal state.
	if s.hasQueuedUserWork(threadID) {
		s.clearAgentCompletionDrain(threadID)
		s.kickQueuedTurnDrain(threadID)
		return
	}
	// A frozen tree holds its pending completion turns: the next user turn
	// consumes them as part of the whole-tree snapshot instead of synthetic
	// turns waking a frozen orchestration (turn/interrupt tree freeze).
	th.mu.Lock()
	frozen := th.workerTreeFrozen
	th.mu.Unlock()
	if frozen {
		s.clearAgentCompletionDrain(threadID)
		return
	}

	pending := s.takePendingAgentCompletionTurns(threadID)
	if len(pending) == 0 {
		s.clearAgentCompletionDrain(threadID)
		return
	}
	// One durable result id per synthetic wakeup gives retries a stable
	// idempotency key independent of process-local batching boundaries.
	current := pending[:1]
	if len(pending) > 1 {
		s.prependPendingAgentCompletionTurns(threadID, pending[1:])
	}

	started, err := s.startSyntheticTurn(context.Background(), threadID, combineAgentCompletionMessages(current), current)
	executionBusy := errors.Is(err, errThreadExecutionBusy)
	retryableAdmission := errors.Is(err, errRetryableTurnAdmission)
	capacityFull = errors.Is(err, session.ErrProjectWorkerCapacity)
	if err != nil && !executionBusy {
		providers.DebugLogf("start agent completion turn for thread %q: %v", threadID, err)
	}
	requeued := false
	if !started && (err == nil || executionBusy || retryableAdmission) {
		s.prependPendingAgentCompletionTurns(threadID, current)
		requeued = true
	}
	s.clearAgentCompletionDrain(threadID)
	if requeued && (executionBusy || retryableAdmission) {
		if capacityFull && s.deferProjectCapacityRetry(threadID, "completion", func() bool { return s.tryDrainAgentCompletionTurns(threadID, true) }) {
			return
		}
		s.scheduleThreadExecutionLeaseRetry(func() { s.kickAgentCompletionDrain(threadID) })
		return
	}
	// Admission can consume an already delivered result without starting a turn.
	// In that case no runTurn will wake the remaining completion queue.
	if requeued || s.hasQueuedAgentCompletionWork(threadID) {
		s.kickAgentCompletionDrain(threadID)
	}
	return
}

func (s *Server) startSyntheticTurn(ctx context.Context, threadID string, userMsg providers.ChatMessage, pending []agentCompletionTurn) (bool, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false, errors.New("thread_id is required")
	}
	if strings.TrimSpace(userMsg.Role) == "" {
		userMsg.Role = "user"
	}
	if !chatMessageHasUserPayload(userMsg) {
		return false, nil
	}

	th := s.thread(threadID)
	if th == nil {
		return false, fmt.Errorf("thread %q not found", threadID)
	}
	if !canResumeAgentCompletionThread(th) {
		return false, nil
	}
	var threadRuntime *runtime.ThreadRuntime
	pending = cloneAgentCompletionTurns(pending)
	completionResultIDs := make([]string, 0, len(pending))
	processCompletionIDs := make([]string, 0, len(pending))
	for _, turn := range pending {
		if resultID := strings.TrimSpace(turn.resultID); resultID != "" {
			completionResultIDs = append(completionResultIDs, resultID)
		}
		if processID := strings.TrimSpace(turn.processID); processID != "" {
			processCompletionIDs = append(processCompletionIDs, processID)
		}
	}

	started, ok, err := s.startThreadUserTurnWithAdmission(
		ctx,
		th,
		userMsg,
		turnRuntimeSnapshot{AgentCompletionResultIDs: completionResultIDs, ProcessCompletionIDs: processCompletionIDs, ExecutionRunID: s.activeExecutionRunID(threadID)},
		false,
		turnReadOnlySkip,
		turnAdmissionHooks{
			afterLease: func(admitted *threadState, admittedMsg *providers.ChatMessage) error {
				var err error
				threadRuntime, err = s.ensureThreadRuntimeAfterAdmission(admitted)
				if err != nil {
					return err
				}
				if err := gateAlreadyDeliveredCompletions(admitted.History, threadRuntime, completionResultIDs, processCompletionIDs); err != nil {
					return err
				}
				*admittedMsg = combineAgentCompletionMessages(pending)
				if clientID := agentCompletionClientID(pending); clientID != "" {
					admittedMsg.ClientID = clientID
				} else if clientID := processCompletionClientID(processCompletionIDs); clientID != "" {
					admittedMsg.ClientID = clientID
				}
				return nil
			},
		},
	)
	if errors.Is(err, errAgentCompletionAlreadyDelivered) {
		return true, nil
	}
	if err != nil || !ok {
		return ok, err
	}
	if err := s.attachExecutionTurn(started.runtime.ExecutionRunID, threadID, started.turnID, started.admittedAt); err != nil {
		persistErr := s.abortStartedThreadTurnDurably(th, started, err)
		return false, errors.Join(err, persistErr)
	}

	_ = s.writeNotification(NotificationTurnStarted, TurnStartedNotification{
		ThreadID: threadID,
		Turn:     started.turn,
	})
	if !s.startBackground(func() {
		s.runTurn(started.ctx, th, threadRuntime, started.turnID, started.runtime, started.history)
	}) {
		// The synthetic completion message was already appended durably at
		// admission; a memory-only rollback would leave an orphan user
		// message with no terminal meta after restart, and its completion
		// result could be consumed twice. Record the terminal projection
		// exactly like a rejected ordinary user turn.
		persistErr := s.abortStartedThreadTurnDurably(th, started, errServerClosed)
		return false, errors.Join(errServerClosed, persistErr)
	}
	return true, nil
}

func canResumeAgentCompletionThread(th *threadState) bool {
	if th == nil {
		return false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	return !th.ReadOnly
}

func threadIsRunning(th *threadState) bool {
	if th == nil {
		return false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	return th.running
}

// waitAndHandoffAnswerReadyTurn lets a user follow-up take the execution slot
// once the current turn's final answer is visible. Leftover provider cleanup
// is cancelled and completed, not treated as a busy rejection.
func (s *Server) waitAndHandoffAnswerReadyTurn(ctx context.Context, th *threadState) error {
	if s == nil || th == nil {
		return errors.New("thread is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if s.closed.Load() {
			return errServerClosed
		}
		th.mu.Lock()
		if !th.running {
			th.mu.Unlock()
			return nil
		}
		if !currentTurnIsAnswerReadyLocked(th) {
			th.mu.Unlock()
			return nil
		}
		if !th.interrupting {
			th.completeAfterAnswerReadyCancel = true
		}
		cancel := th.cancel
		waiter := th.addIdleWaiterLocked()
		th.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-waiter:
		}
	}
}

func (s *Server) takePendingAgentCompletionTurns(threadID string) []agentCompletionTurn {
	s.agentCompletionMu.Lock()
	defer s.agentCompletionMu.Unlock()
	if s.closed.Load() {
		return nil
	}
	pending := cloneAgentCompletionTurns(s.pendingAgentCompletionTurns[threadID])
	delete(s.pendingAgentCompletionTurns, threadID)
	return pending
}

func (s *Server) prependPendingAgentCompletionTurns(threadID string, turns []agentCompletionTurn) {
	if len(turns) == 0 {
		return
	}
	s.agentCompletionMu.Lock()
	defer s.agentCompletionMu.Unlock()
	if s.closed.Load() {
		return
	}
	if s.pendingAgentCompletionTurns == nil {
		s.pendingAgentCompletionTurns = make(map[string][]agentCompletionTurn)
	}
	existing := cloneAgentCompletionTurns(s.pendingAgentCompletionTurns[threadID])
	s.pendingAgentCompletionTurns[threadID] = append(cloneAgentCompletionTurns(turns), existing...)
}

func (s *Server) discardPendingAgentCompletionTurns(threadID string) {
	s.agentCompletionMu.Lock()
	defer s.agentCompletionMu.Unlock()
	delete(s.pendingAgentCompletionTurns, threadID)
}

func (s *Server) clearAgentCompletionDrain(threadID string) {
	s.agentCompletionMu.Lock()
	defer s.agentCompletionMu.Unlock()
	delete(s.drainingAgentCompletionTurns, threadID)
}

func (s *Server) hasQueuedAgentCompletionWork(threadID string) bool {
	s.agentCompletionMu.Lock()
	defer s.agentCompletionMu.Unlock()
	return len(s.pendingAgentCompletionTurns[threadID]) > 0 || s.drainingAgentCompletionTurns[threadID]
}

func combineAgentCompletionMessages(turns []agentCompletionTurn) providers.ChatMessage {
	if len(turns) == 0 {
		return providers.ChatMessage{Role: "user", ReadOnly: true}
	}
	if len(turns) == 1 {
		msg := turns[0].msg
		msg.ReadOnly = true
		return msg
	}
	contents := make([]string, 0, len(turns))
	name := ""
	for _, turn := range turns {
		msg := turn.msg
		if name == "" {
			name = strings.TrimSpace(msg.Name)
		}
		if content := strings.TrimSpace(msg.Content); content != "" {
			contents = append(contents, content)
		}
	}
	return providers.ChatMessage{
		Role: "user",
		Name: name,
		// A merged completion is generated evidence even when its parts have
		// different (or legacy missing) names/origins and lose their envelopes.
		ReadOnly: true,
		Content:  strings.Join(contents, "\n\n"),
	}
}

const (
	agentCompletionClientIDPrefix       = "wuu-agent-completion:"
	agentCompletionAnswerClientIDPrefix = "wuu-agent-completion-answer:"
)

func agentCompletionClientID(turns []agentCompletionTurn) string {
	ids := make([]string, 0, len(turns))
	seen := make(map[string]bool, len(turns))
	for _, turn := range turns {
		id := strings.TrimSpace(turn.resultID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return agentCompletionClientIDPrefix + strings.Join(ids, ",")
}

func agentCompletionClientIDForResult(resultID string) string {
	resultID = strings.TrimSpace(resultID)
	if resultID == "" {
		return ""
	}
	return agentCompletionClientIDPrefix + resultID
}

func agentCompletionResultIDs(clientID string) []string {
	clientID = strings.TrimSpace(clientID)
	if !strings.HasPrefix(clientID, agentCompletionClientIDPrefix) {
		return nil
	}
	raw := strings.TrimPrefix(clientID, agentCompletionClientIDPrefix)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if id := strings.TrimSpace(part); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func agentCompletionAnswerResultIDs(clientID string) []string {
	return completionReceipts(clientID).Agents
}

func splitAgentCompletionResultIDs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if id := strings.TrimSpace(part); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// markAgentCompletionAnswer stamps the successful final assistant row with a
// durable outcome marker. A streamed partial assistant from a cancelled or
// failed turn never receives this marker, so restart recovery cannot mistake
// visible partial text for a completed consumption of the child result.
func markAgentCompletionAnswer(res *agent.LoopResult, resultIDs []string) bool {
	return markCompletionAnswer(res, completionAnswerReceipts{Agents: resultIDs})
}

func agentCompletionMarkerAnswered(history []providers.ChatMessage, resultID string) bool {
	resultID = strings.TrimSpace(resultID)
	if resultID == "" {
		return false
	}
	marker := -1
	for i, msg := range history {
		for _, id := range agentCompletionResultIDs(msg.ClientID) {
			if id == resultID {
				marker = i
				break
			}
		}
	}
	if marker < 0 {
		return false
	}
	for _, msg := range history[marker+1:] {
		for _, answeredID := range agentCompletionAnswerResultIDs(msg.ClientID) {
			if answeredID == resultID {
				return true
			}
		}
		if strings.EqualFold(strings.TrimSpace(msg.Role), "user") &&
			!msg.Hidden && !msg.Steered && !compact.IsInternalContextMessage(msg) {
			return false
		}
	}
	return false
}

func cloneAgentCompletionTurns(turns []agentCompletionTurn) []agentCompletionTurn {
	if len(turns) == 0 {
		return nil
	}
	msgs := make([]providers.ChatMessage, 0, len(turns))
	for _, turn := range turns {
		msgs = append(msgs, turn.msg)
	}
	msgs = cloneHistory(msgs)
	out := make([]agentCompletionTurn, 0, len(turns))
	for i, turn := range turns {
		out = append(out, agentCompletionTurn{
			agentID:   turn.agentID,
			resultID:  turn.resultID,
			processID: turn.processID,
			msg:       msgs[i],
			snapshot:  cloneSubAgentSnapshot(turn.snapshot),
		})
	}
	return out
}

func cloneSubAgentSnapshot(snapshot *subagent.SubAgentSnapshot) *subagent.SubAgentSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	return &clone
}

func queuedTurnSummary(threadID string, entry queuedTurn) QueuedTurn {
	preview := strings.TrimSpace(chatMessageDisplayContent(entry.msg))
	if preview == "" && len(entry.msg.Images) > 0 {
		if len(entry.msg.Images) == 1 {
			preview = "[Image #1]"
		} else {
			preview = fmt.Sprintf("[%d images]", len(entry.msg.Images))
		}
	}
	if preview == "" && len(entry.msg.Files) > 0 {
		if len(entry.msg.Files) == 1 {
			preview = filePreview(entry.msg.Files[0], 1)
		} else {
			preview = fmt.Sprintf("[%d files]", len(entry.msg.Files))
		}
	}
	return QueuedTurn{
		ID:         entry.id,
		ThreadID:   threadID,
		Preview:    preview,
		ImageCount: len(entry.msg.Images),
		FileCount:  len(entry.msg.Files),
	}
}

func chatMessageHasUserPayload(msg providers.ChatMessage) bool {
	return strings.TrimSpace(msg.Content) != "" || len(msg.Images) > 0 || len(msg.Files) > 0
}

func queuedTurnsFromSteers(msgs []providers.ChatMessage) []queuedTurn {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]queuedTurn, 0, len(msgs))
	for _, msg := range msgs {
		id := strings.TrimSpace(msg.ClientID)
		if id == "" {
			id = session.NewID()
		}
		msg.ClientID = id
		msg.Steered = false
		snapshot := turnRuntimeSnapshot{}
		if pluginID, requestID, ok := pluginSessionRequestFromClientID(id); ok {
			snapshot.PluginTurn = &pluginTurnReference{PluginID: pluginID, RequestID: requestID}
		}
		if ids := agentCompletionResultIDs(id); len(ids) > 0 {
			snapshot.AgentCompletionResultIDs = ids
		}
		out = append(out, queuedTurn{id: id, msg: msg, snapshot: snapshot})
	}
	return out
}

func coalescedQueuedTurnsFromSteers(msgs []providers.ChatMessage) []queuedTurn {
	queued := queuedTurnsFromSteers(msgs)
	if len(queued) < 2 {
		return queued
	}
	out := make([]queuedTurn, 0, len(queued))
	for index := 0; index < len(queued); {
		entry := queued[index]
		reference := entry.snapshot.PluginTurn
		if reference == nil {
			out = append(out, entry)
			index++
			continue
		}
		next := index + 1
		for next < len(queued) {
			candidate := queued[next].snapshot.PluginTurn
			if candidate == nil || candidate.PluginID != reference.PluginID {
				break
			}
			entry.followups = append(entry.followups, queued[next].msg)
			next++
		}
		out = append(out, entry)
		index = next
	}
	return out
}

func filterConsumedAgentCompletionSteers(steers []providers.ChatMessage, control *agentcontrol.AgentControl) []providers.ChatMessage {
	if len(steers) == 0 || control == nil {
		return steers
	}
	out := make([]providers.ChatMessage, 0, len(steers))
	for _, steer := range steers {
		ids := agentCompletionResultIDs(steer.ClientID)
		if len(ids) == 0 {
			out = append(out, steer)
			continue
		}
		consumed := false
		for _, id := range ids {
			consumer, _ := control.AgentResultDeliveryConsumer(id)
			if consumer != "" {
				consumed = true
				break
			}
		}
		if !consumed {
			out = append(out, steer)
		}
	}
	return out
}

func (s *Server) persistTurnResultLocked(th *threadState, res agent.LoopResult, rewriteHistory bool, providerName, model string, historyBaselineSeq int) error {
	if !th.PersistHistory {
		return nil
	}
	indexHistory := th.History
	if rewriteHistory {
		rewriteBaselineSeq := max(historyBaselineSeq, res.HistoryArchiveHeadSeq)
		if res.DurableMessagesTracked && len(res.DurableNewMessages) > 0 {
			seqs, endSeq, err := appendChatMessagesReturningSeqs(s.rt.SessionDir, th.ID, res.DurableNewMessages)
			if err != nil {
				return err
			}
			th.History = applyPersistedHistorySeqs(th.History, res.DurableNewMessages, seqs)
			if endSeq > rewriteBaselineSeq {
				rewriteBaselineSeq = endSeq
			}
		}
		if err := rewriteChatHistoryAtBaseline(s.rt.SessionDir, th.ID, th.History, rewriteBaselineSeq); err != nil {
			return err
		}
		// The transaction may have merged meta tail records that arrived while
		// the model ran. Count the committed history rather than
		// overwriting the session index from the turn's pre-merge snapshot.
		if committedRecords, committedHeadSeq, err := loadProviderPersistedMessages(s.rt.SessionDir, th.ID, false); err != nil {
			return err
		} else {
			committed := chatMessagesFromPersistedMessages(committedRecords)
			indexHistory = committed
			th.History = cloneHistory(committed)
			th.historyHeadSeq = committedHeadSeq
		}
	} else {
		messagesToAppend := res.NewMessages
		if res.DurableMessagesTracked {
			messagesToAppend = res.DurableNewMessages
		}
		if err := appendChatMessages(s.rt.SessionDir, th.ID, messagesToAppend); err != nil {
			return err
		}
	}
	if err := appendTokenUsage(s.rt.SessionDir, th.ID, providerName, model, providers.TokenUsage{
		InputTokens:         res.InputTokens,
		OutputTokens:        res.OutputTokens,
		CacheCreationTokens: res.CacheCreationTokens,
		CacheReadTokens:     res.CacheReadTokens,
	}, res.ContextTokens); err != nil {
		return err
	}
	if err := session.UpdateIndex(s.rt.SessionDir, th.ID, persistableMessageCount(indexHistory), threadPreview(indexHistory)); err != nil {
		return err
	}
	s.invalidateSettingsUsage()
	return nil
}

func applyPersistedHistorySeqs(messages, persisted []providers.ChatMessage, seqs []int) []providers.ChatMessage {
	if len(messages) == 0 || len(persisted) == 0 || len(seqs) == 0 {
		return messages
	}
	out := cloneHistory(messages)
	searchFrom := 0
	for persistedIndex, persistedMessage := range persisted {
		if persistedIndex >= len(seqs) || seqs[persistedIndex] <= 0 {
			continue
		}
		persistedMessage.Seq = 0
		for messageIndex := searchFrom; messageIndex < len(out); messageIndex++ {
			candidate := providers.CloneChatMessage(out[messageIndex])
			candidate.Seq = 0
			if !reflect.DeepEqual(candidate, persistedMessage) {
				continue
			}
			out[messageIndex].Seq = seqs[persistedIndex]
			searchFrom = messageIndex + 1
			break
		}
	}
	return out
}

func (s *Server) persistFailedTurnResultLocked(th *threadState, res agent.LoopResult, rewriteHistory bool, providerName, model string, historyBaselineSeq int) error {
	if th.PersistHistory {
		return s.persistTurnResultLocked(th, res, rewriteHistory, providerName, model, historyBaselineSeq)
	}
	if err := appendTokenUsage(s.rt.SessionDir, th.ID, providerName, model, providers.TokenUsage{
		InputTokens:         res.InputTokens,
		OutputTokens:        res.OutputTokens,
		CacheCreationTokens: res.CacheCreationTokens,
		CacheReadTokens:     res.CacheReadTokens,
	}, res.ContextTokens); err != nil {
		return err
	}
	s.invalidateSettingsUsage()
	return nil
}

type settingsUsageCacheEntry struct {
	response  SettingsUsageResponse
	zone      string
	expiresAt time.Time
}

// usageLocation resolves the IANA time zone a usage request buckets days in.
// Empty means UTC; an unknown zone is an error rather than a silent UTC
// fallback.
func usageLocation(zone string) (*time.Location, error) {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone: %w", err)
	}
	return loc, nil
}

func (s *Server) invalidateSettingsUsage() {
	if s == nil {
		return
	}
	s.settingsUsageMu.Lock()
	s.settingsUsageCache = nil
	s.settingsUsageMu.Unlock()
}

// handleSettingsUsage returns the aggregated token usage snapshot for
// the desktop settings page. The snapshot always covers the full
// token_usage trail — every row, including zero-At legacy imports, so
// long-running sessions and migrated history contribute their real
// totals.
func (s *Server) handleSettingsUsage(req Request) error {
	var params SettingsUsageQuery
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	loc, err := usageLocation(params.TimeZone)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	sessDir := s.rt.SessionDir
	now := time.Now().UTC()
	s.settingsUsageMu.Lock()
	defer s.settingsUsageMu.Unlock()
	if cached := s.settingsUsageCache; cached != nil && cached.zone == loc.String() && now.Before(cached.expiresAt) {
		return s.writeResponse(req.ID, cached.response, nil)
	}

	scan, err := insight.CollectUsageScan(sessDir)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("collect usage: %w", err))
	}
	rows := scan.TokenRows

	metrics, days := aggregateUsageRows(rows, loc)

	response := SettingsUsageResponse{
		TotalSessions:   countUsageSessions(rows),
		GeneratedAt:     now.Format(time.RFC3339Nano),
		Metrics:         metrics,
		ModelBreakdowns: buildUsageModelBreakdowns(rows),
		SkillUsage:      scan.Skills,
		Days:            days,
	}
	// Usage analytics is an approximate convenience view, not a live meter.
	// Keep the full-history scan out of the normal interaction path for two hours.
	s.settingsUsageCache = &settingsUsageCacheEntry{response: response, zone: loc.String(), expiresAt: now.Add(2 * time.Hour)}
	return s.writeResponse(req.ID, response, nil)
}

// handleUsageOverview returns the empty conversation home's usage summary.
// Unlike handleSettingsUsage it reads only token_usage rows, so it needs no
// cache, and it buckets days in the caller's time zone so they line up with
// the calendar the desktop draws.
func (s *Server) handleUsageOverview(req Request) error {
	var params UsageOverviewParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	loc, err := usageLocation(params.TimeZone)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	rows, err := session.ListTokenUsage(s.rt.SessionDir)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("collect usage: %w", err))
	}
	metrics, days := aggregateUsageRows(rows, loc)
	return s.writeResponse(req.ID, UsageOverviewResponse{
		TotalSessions: countUsageSessions(rows),
		Metrics:       metrics,
		Days:          days,
	}, nil)
}

// countUsageSessions returns the number of distinct session IDs present
// in the token_usage trail.
func countUsageSessions(rows []insight.TokenUsageRow) int {
	seen := make(map[string]struct{})
	for _, r := range rows {
		seen[r.SessionID] = struct{}{}
	}
	return len(seen)
}

func buildUsageModelBreakdowns(rows []insight.TokenUsageRow) []insight.ModelUsage {
	buckets := make(map[string]*insight.ModelUsage)
	sessionsByBucket := make(map[string]map[string]struct{})
	for _, r := range rows {
		key := r.Provider + "|" + r.Model
		bucket, ok := buckets[key]
		if !ok {
			bucket = &insight.ModelUsage{Provider: r.Provider, Model: r.Model}
			buckets[key] = bucket
		}
		bucket.InputTokens += r.InputTokens
		bucket.OutputTokens += r.OutputTokens
		bucket.CacheCreationTokens += r.CacheCreationTokens
		bucket.CacheReadTokens += r.CacheReadTokens
		if r.SessionID != "" {
			seen := sessionsByBucket[key]
			if seen == nil {
				seen = make(map[string]struct{})
				sessionsByBucket[key] = seen
			}
			seen[r.SessionID] = struct{}{}
		}
	}

	breakdowns := make([]insight.ModelUsage, 0, len(buckets))
	for key, bucket := range buckets {
		// token_usage rows double as context-size markers (compaction
		// checkpoints, turns whose provider reported no usage): their token
		// sums are all zero. A bucket made only of such rows carries no
		// spend signal and would render as a meaningless 0/0 card.
		if bucket.TotalContextTokens() == 0 {
			continue
		}
		bucket.Sessions = len(sessionsByBucket[key])
		breakdowns = append(breakdowns, *bucket)
	}
	sort.Slice(breakdowns, func(i, j int) bool {
		return breakdowns[i].TotalContextTokens() > breakdowns[j].TotalContextTokens()
	})
	return breakdowns
}

// aggregateUsageRows is the single source of truth for the desktop
// usage views' metrics and daily series. It never reads from session
// metadata — only the per-row token_usage trail — so the headline
// numbers and the heatmap stay numerically consistent. Days and the date
// range are calendar dates in loc.
func aggregateUsageRows(rows []insight.TokenUsageRow, loc *time.Location) (SettingsUsageMetrics, []SettingsUsageDay) {
	metrics := SettingsUsageMetrics{}
	type dayBucket struct {
		input, output, cacheRead, cacheCreation int
		turns                                   int
	}
	daysByDate := make(map[string]*dayBucket)

	var minAt, maxAt time.Time
	for _, r := range rows {
		metrics.InputTokens += r.InputTokens
		metrics.OutputTokens += r.OutputTokens
		metrics.CacheReadTokens += r.CacheReadTokens
		metrics.CacheCreationTokens += r.CacheCreationTokens
		metrics.Turns++
		if !r.At.IsZero() {
			if minAt.IsZero() || r.At.Before(minAt) {
				minAt = r.At
			}
			if r.At.After(maxAt) {
				maxAt = r.At
			}
			date := r.At.In(loc).Format("2006-01-02")
			bucket, ok := daysByDate[date]
			if !ok {
				bucket = &dayBucket{}
				daysByDate[date] = bucket
			}
			bucket.input += r.InputTokens
			bucket.output += r.OutputTokens
			bucket.cacheRead += r.CacheReadTokens
			bucket.cacheCreation += r.CacheCreationTokens
			bucket.turns++
		}
	}

	metrics.PromptTokens = metrics.InputTokens + metrics.CacheReadTokens
	metrics.ContextTokens = metrics.InputTokens + metrics.CacheReadTokens + metrics.OutputTokens
	if metrics.PromptTokens > 0 {
		metrics.CacheHitRate = float64(metrics.CacheReadTokens) / float64(metrics.PromptTokens)
	}
	if !minAt.IsZero() {
		metrics.DateRange = [2]string{minAt.In(loc).Format("2006-01-02"), maxAt.In(loc).Format("2006-01-02")}
		metrics.ActiveDays = len(daysByDate)
	}

	days := make([]SettingsUsageDay, 0, len(daysByDate))
	for date, b := range daysByDate {
		prompt := b.input + b.cacheRead
		var rate float64
		if prompt > 0 {
			rate = float64(b.cacheRead) / float64(prompt)
		}
		days = append(days, SettingsUsageDay{
			Date:                date,
			InputTokens:         b.input,
			OutputTokens:        b.output,
			CacheCreationTokens: b.cacheCreation,
			CacheReadTokens:     b.cacheRead,
			CacheHitRate:        rate,
			Turns:               b.turns,
		})
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })

	return metrics, days
}

// truncateUsageTitle shortens a session's first user message down to a
// reasonable card headline; the desktop may trim further before display.
func truncateUsageTitle(s string) string {
	const max = 60
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// stopHookMessage provides a single-line, bounded snippet of the turn's final
// assistant message for Stop hooks that surface it to the user (for example a
// desktop notification). Collapsing whitespace keeps the payload easy to embed
// in plain-text commands without exposing raw formatting or a full response.
func stopHookMessage(res agent.LoopResult) string {
	const max = 200
	text := strings.Join(strings.Fields(strings.TrimSpace(res.Content)), " ")
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max-1]) + "…"
}

func requestContextSystemSections(sections []agent.SystemPromptSectionInfo) []sessiontrace.SystemSectionRecord {
	if len(sections) == 0 {
		return nil
	}
	out := make([]sessiontrace.SystemSectionRecord, 0, len(sections))
	for _, section := range sections {
		out = append(out, sessiontrace.SystemSectionRecord{
			Key:    section.Key,
			Static: section.Static,
			Bytes:  section.Bytes,
			Hash:   section.Hash,
		})
	}
	return out
}

func cloneStringIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
