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

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/chatgpt"
	"github.com/blueberrycongee/wuu/internal/providers/xaisub"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sidethread"
	"github.com/blueberrycongee/wuu/internal/siwc"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/tools"
)

var (
	errServerClosed        = errors.New("app-server is closed")
	errShutdown            = errors.New("app-server shutdown requested")
	errExecutionRunChanged = errors.New("thread execution belongs to a different Run")
)

type threadTurnCompletion struct {
	turnID        string
	done          chan struct{}
	durabilityErr error
}

func (c *threadTurnCompletion) failed() bool {
	if c == nil {
		return false
	}
	select {
	case <-c.done:
		return c.durabilityErr != nil
	default:
		return false
	}
}

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
	TitleRevision  int64
	ModelProvider  string
	Model          string
	ModelVariant   string
	ModelOptions   map[string]string
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
	ephemeralDriver          ephemeralDriverState
	execRuntime              *runtime.ThreadRuntime
	state                    *runtime.State
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
	runningModelRevision  uint64
	stepModelSelection    bool
	runningModel          string
	cancel                context.CancelFunc
	// The captured runner's immutable outcome is published by closing done.
	turnCompletion *threadTurnCompletion

	// idleWaiters are closed when this thread releases execution. Follow-up
	// turn/start after a visible final answer waits here instead of failing busy.
	idleWaiters []chan struct{}
	// completeAfterAnswerReadyCancel is set when a successor user turn arrives
	// after the current turn's answer is already visible. Cancelling leftover
	// provider work must complete that turn, not mark it interrupted.
	completeAfterAnswerReadyCancel bool
	executionLease                 *session.DocumentThreadExecutionLease
	pluginExecutionLease           *session.PluginGenerationLease
	pluginLeaseReleaseLoop         bool
	onPluginLeaseQuiescent         func()
	runtimePluginEpoch             uint64
	runtimePluginRevision          uint64
	admissionReserved              bool
	nativeHistoryMutation          bool
	pendingSteers                  []providers.ChatMessage
	nativeSteerInFlight            string
	nativeSteerUnsupported         bool
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
	state               *runtime.State
	modelState          *runtime.State
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
	personalActions   *personalActions
	clientInputMu     sync.Mutex
	clientInputOwner  string
	clientInputLease  *session.DocumentThreadExecutionLease
	trustedTransport  trustedTransportState
	sessionFederation *SessionFederation
	access            connectionAccess
	artifactReads     sync.Map
	rpcTiming         *rpcTimingRecorder
	rt                *runtime.Session
	out               io.Writer
	writeMu           sync.Mutex

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
	rendererClients             rendererClientScope
	projectRegistration         *projectServerRegistration

	// pushRegistrar is the host-side hook invoked by the device/push_*
	// methods. The desktop main pipeline leaves it nil so the methods
	// respond with "remote-only" errors; the remote-host package binds
	// a per-device registrar via WithPushRegistrar in RunStdioForDevice.
	pushRegistrar PushRegistrar
	remoteDevice  bool

	mu      sync.Mutex
	threads map[string]*threadState
	// Title attempts outlive cached thread snapshots and are deduplicated by ID.
	titleGenerations map[sessionCacheKey]bool

	runMu             sync.Mutex
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

	pluginClientMu       sync.Mutex
	pluginClientRequests map[pluginClientRequestKey]*pluginClientRequest

	pluginTurnUnbind   func()
	pluginTurnWaiters  pluginTurnWaitHub
	userQuestionUnbind func()
	userQuestionStop   chan struct{}
	userQuestionDone   chan struct{}

	rewriteChatHistoryForTest           func(string, string, []providers.ChatMessage) error
	afterLifecycleHistoryAppendForTest  func(threadID string)
	deleteSessionForTest                func(string) (session.Session, error)
	afterWorkerShutdownStopWavesForTest func()
	beforeRuntimeInstallForTest         func(*runtime.ThreadRuntime)
	shutdownDrainTimeoutForTest         time.Duration
	afterShutdownThreadReleaseForTest   func()
	beforeQueuedTurnBackgroundForTest   func()
	beforeQueuedTurnThreadLookupForTest func()

	codexModelsMu   sync.Mutex
	codexModelCache map[string]map[string]config.ProviderModelConfig

	chatGPTMu         sync.Mutex
	chatGPTManager    *siwc.Manager
	chatGPTBindings   map[string]*chatGPTBinding
	chatGPTHTTP       *http.Client
	chatGPTModelCache map[string][]chatgpt.ModelInfo

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
	participantSummaryCache map[sessionCacheKey]participant.Summary

	inferenceMaintenanceStop     chan struct{}
	inferenceMaintenanceDone     chan struct{}
	inferenceMaintenanceStopOnce sync.Once
	activityUnsubscribe          func()
	backgroundMu                 sync.Mutex
	backgroundWG                 sync.WaitGroup
	engineScopeMu                sync.Mutex
	engineScopes                 map[agentengine.EngineID]engineScope
	engineAuthMu                 sync.Mutex
	engineAuthCancels            map[string]context.CancelFunc
	sessionOperationCtx          context.Context
	sessionOperationCancel       context.CancelFunc
	packageDownloadCtx           context.Context
	packageDownloadCancel        context.CancelFunc
	closeOnce                    sync.Once
	closed                       atomic.Bool
	pluginGenerationMutation     atomic.Bool
	pluginGenerationRefreshing   atomic.Bool
	pluginAdditivePreparation    atomic.Pointer[pluginAdditivePreparation]
	pluginGenerationEpoch        atomic.Uint64
	pluginRuntimeRevision        atomic.Uint64
	pluginGenerationRefreshMu    sync.Mutex
	pluginRefreshFailure         pluginGenerationRefreshFailure
	configRefreshMu              sync.Mutex
	configFingerprint            string
	configRejectedSources        string
	providerSummariesMu          sync.Mutex
	lastProviderSummaries        []ProviderSummary
	pluginLifecycleReplay        pluginLifecycleReplayState
	refreshExtensionsForTest     func(config.Config) error
	refreshConfigForTest         func() error
	presenceLease                *session.AppServerPresenceLease
	bootOwner                    bool
	storageMaintenanceCancel     context.CancelFunc
	startupErr                   error

	controlLocksMu sync.Mutex
	controlLocks   map[string]*sessionControlLock

	stdioResumeMu   sync.Mutex
	stdioResumeTail map[string]*stdioResumeRequest
	// inboxMu orders deliveries into a session so pending input is admitted
	// in creation order.
	inboxMu    sync.Mutex
	sideTurnMu sync.Mutex
	sideTurns  map[string]*sideThreadTurn
}

func New(rt *runtime.Session, out io.Writer, options ...ServerOption) *Server {
	sessionOperationCtx, sessionOperationCancel := context.WithCancel(context.Background())
	packageDownloadCtx, packageDownloadCancel := context.WithCancel(context.Background())
	s := &Server{
		rpcTiming:           newRPCTimingRecorder(),
		access:              connectionAccess{principal: principal.LocalOwner(), policy: access.PolicyFunc(rt.AuthorizeResource)},
		sessionOperationCtx: sessionOperationCtx, sessionOperationCancel: sessionOperationCancel,
		packageDownloadCtx: packageDownloadCtx, packageDownloadCancel: packageDownloadCancel,
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
		modelCatalogHTTPClient:       http.DefaultClient,
	}
	for _, option := range options {
		if err := option(s); err != nil {
			s.startupErr = err
			return s
		}
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

	binding, err := rt.RetainState()
	if err != nil {
		s.startupErr = err
		return s
	}
	defer binding.Close()
	if strings.TrimSpace(rt.SessionDir) != "" {
		if err := sidethread.NewStore(context.Background(), binding.Documents).ImportLegacy(filepath.Join(rt.SessionDir, "sidethreads"), bootOwner); err != nil {
			s.startupErr = fmt.Errorf("import legacy side threads: %w", err)
			return s
		}
	}
	if bootOwner {
		if err := s.recoverSideThreadsOnBoot(binding); err != nil {
			s.startupErr = err
			return s
		}
		s.settleOnBoot(binding)
	}
	if s.clientInputReceiptsEnabled() {
		if err := binding.Sessions.RecoverClientInputs(s.rt.SessionDir, s.clientInputScope("", "")); err != nil {
			s.startupErr = err
			return s
		}
	}
	if s.presenceLease != nil {
		if err := s.presenceLease.FinalizeStartup(); err != nil {
			// Retain the startup/exclusive lock until Close. Blocking a peer is
			// safer than letting it misclassify this live server as crashed.
			s.startupErr = fmt.Errorf("finalize app-server presence: %w", err)
			return s
		}
	}
	if rt != nil && rt.ActivityRegistry != nil {
		s.activityUnsubscribe = rt.ActivityRegistry.Subscribe(func(event activity.Event) {
			s.notifyActivityEvent(event)
		})
	}
	if rt != nil && rt.PluginSessionRouter != nil {
		unbindTurns := rt.PluginSessionRouter.BindExtended(
			s.createPluginSession, s.sendPluginSession, s.listPluginSessions, s.cancelPluginSession,
			s.inspectPluginSession, s.statusPluginWorkspace, s.applyPluginWorkspace, s.discardPluginWorkspace,
			func(ctx context.Context, pluginID string, p pluginhost.SessionControlParams) (pluginhost.SessionControlResult, error) {
				binding, err := runtime.RetainProductInvocationState(ctx)
				if err != nil {
					return pluginhost.SessionControlResult{}, err
				}
				defer binding.Close()
				return s.controlPluginSession(binding, ctx, pluginID, p)
			},
		)
		unbindAuthorization := rt.PluginSessionRouter.BindExecutionAuthorization(rt.HandleExecutionAuthorization)
		unbindSessionAccess := rt.PluginSessionRouter.BindSessionResourceAccess(func(ctx context.Context, pluginID, sessionID string, reference *pluginhost.ExecutionAuthorizationReference, operation string) (context.Context, error) {
			if strings.HasPrefix(operation, "personal/") {
				operation = strings.TrimPrefix(operation, "personal/")
				_, execution, known := personalhost.OperationAccess(operation)
				if !known {
					return ctx, errors.New("Personal operation is unavailable")
				}
				if reference != nil {
					resources := []access.Resource{{Kind: "user-space", ID: s.rt.UserBinding().UserSpaceID}}
					if execution {
						resources = append(resources, access.Resource{Kind: "session", ID: sessionID})
					}
					var err error
					ctx, err = rt.RestoreExecutionAuthorization(ctx, pluginID, *reference, sessionID, "personal/"+operation, resources...)
					if err != nil {
						return ctx, err
					}
				}
				return ctx, s.authorizePersonalOperation(ctx, operation, sessionID)
			}
			ctx, err := s.restoreUserSessionAuthorization(ctx, pluginID, reference, operation, sessionID)
			if err != nil {
				return ctx, err
			}
			return ctx, s.authorizeUserSessionOperation(ctx, operation, sessionID)
		})
		unbindProcesses := rt.PluginSessionRouter.BindProcessSessions(s.resolvePluginProcessSession)
		unbindClient := rt.PluginSessionRouter.BindClientInvalidation(s.invalidatePluginClient)
		s.pluginTurnUnbind = func() { unbindTurns(); unbindProcesses(); unbindClient(); unbindAuthorization(); unbindSessionAccess() }
		s.startBackgroundWithState(binding, s.replayPendingPluginTurnLifecycles)
	}
	s.startInferenceJournalMaintenance()
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
	binding, err := s.rt.RetainState()
	if err != nil {
		providers.DebugLogf("retain storage maintenance state: %v", err)
		return
	}
	defer binding.Close()
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return
	}
	redundant, err := binding.Sessions.WithContext(ctx).MaintainRedundantStorage()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		providers.DebugLogf("redundant session storage maintenance: %v", err)
	} else if redundant.ProjectedToolResultsCleared > 0 || redundant.SupersededCheckpointsDeleted > 0 || redundant.ToolMessageContentsCompacted > 0 || redundant.CheckpointsEncoded > 0 {
		providers.DebugLogf(
			"redundant session storage maintenance: projected_results=%d superseded_checkpoints=%d tool_message_projections=%d checkpoints_encoded=%d checkpoint_bytes_before=%d checkpoint_bytes_after=%d",
			redundant.ProjectedToolResultsCleared,
			redundant.SupersededCheckpointsDeleted,
			redundant.ToolMessageContentsCompacted,
			redundant.CheckpointsEncoded,
			redundant.CheckpointBytesBefore,
			redundant.CheckpointBytesAfter,
		)
	}
	result, err := binding.Sessions.WithContext(ctx).MaintainModelInputReceipts(time.Now().UTC())
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
	orphanWorktrees, err := s.cleanupOrphanWorktrees(binding)
	if err != nil {
		providers.DebugLogf("worktree storage maintenance: %v", err)
	} else if orphanWorktrees > 0 {
		providers.DebugLogf("worktree storage maintenance: removed %d orphan owner group(s)", orphanWorktrees)
	}
	if err := s.maintainInputAttachments(time.Now().UTC()); err != nil {
		providers.DebugLogf("input image storage maintenance: %v", err)
	}
}

// settleOnBoot reconciles orphaned provider operations without starting a turn.
// Recovery is best-effort so a transient journal error does not block startup.
func (s *Server) settleOnBoot(binding *runtime.State) {
	if s == nil || s.rt == nil {
		return
	}
	now := time.Now().UTC()
	if binding.InferenceJournalRuntime != nil {
		recoveries, recoverErr := binding.InferenceJournalRuntime.ReconcileOrphans(now)
		if recoverErr != nil {
			providers.DebugLogf("settleOnBoot pass4 (inference journal): %v", recoverErr)
		} else if len(recoveries) > 0 {
			s.persistRecoveredTurnTerminals(binding, recoveries, now)
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
		if pruned, pruneErr := binding.InferenceJournalRuntime.Prune(now); pruneErr != nil {
			providers.DebugLogf("settleOnBoot pass5 (inference journal retention): %v", pruneErr)
		} else if pruned > 0 {
			providers.DebugLogf("settleOnBoot pass5: pruned %d old inference operation(s)", pruned)
		}
		if runStore, err := execution.NewStore(binding.Documents); recoverErr == nil && err == nil {
			recovered, err := runStore.ReconcileOrphans(context.Background(), binding.InferenceJournalRuntime.RuntimeID(), now)
			if err != nil {
				providers.DebugLogf("settleOnBoot pass6 (execution runs): %v", err)
			} else if len(recovered) > 0 {
				providers.DebugLogf("settleOnBoot pass6: interrupted %d orphan execution run(s)", len(recovered))
			}
		}
	}
}

func (s *Server) persistRecoveredTurnTerminals(binding *runtime.State, recoveries []session.InferenceCrashRecovery, now time.Time) {
	owners := make(map[string]struct{})
	for _, recovery := range recoveries {
		ownerID := strings.TrimSpace(recovery.OwnerID)
		if recovery.Kind == providers.InferenceOperationAgentRound && ownerID != "" {
			owners[ownerID] = struct{}{}
		}
	}
	for ownerID := range owners {
		loaded, err := s.loadPersistedThreadSnapshot(binding, ownerID)
		if err != nil {
			if !errors.Is(err, session.ErrSessionNotFound) {
				providers.DebugLogf("settleOnBoot turn projection %q: %v", ownerID, err)
			}
			continue
		}
		turns := turnsFromPersistedHistory(ownerID, loaded.displayHistory, now, s.participantResolver(binding))
		if len(turns) == 0 {
			continue
		}
		turn := turns[len(turns)-1]
		if hasPersistedTurnTerminal(loaded.rawHistory, turn.ID) || turnHasFinalAnswer(turn) {
			continue
		}
		message := "execution interrupted because the previous app server exited"
		if err := binding.Sessions.AppendHistoryRecord(ownerID, session.HistoryRecord{
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
	if s == nil || s.rt == nil {
		return
	}
	s.inferenceMaintenanceDone = make(chan struct{})
	go func() {
		defer close(s.inferenceMaintenanceDone)
		ticker := time.NewTicker(session.InferenceJournalRecoveryInterval())
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				func() {
					binding, err := s.rt.RetainState()
					if err != nil {
						providers.DebugLogf("retain journal maintenance state: %v", err)
						return
					}
					defer binding.Close()
					if binding.InferenceJournalRuntime == nil {
						return
					}
					recoveries, err := binding.InferenceJournalRuntime.ReconcileOrphans(now.UTC())
					if err != nil {
						providers.DebugLogf("inference journal maintenance: %v", err)
						return
					}
					if len(recoveries) > 0 {
						s.persistRecoveredTurnTerminals(binding, recoveries, now.UTC())
					}
				}()
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
		defer s.rpcTiming.flushResponses()
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
// finalizers have released execution ownership. The shared runtime.Session
// remains owned by the caller and may be cleaned up after Close returns.
func (s *Server) Close() {
	if s == nil {
		return
	}
	defer s.rpcTiming.flushResponses()
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.packageDownloadCancel != nil {
			s.packageDownloadCancel()
		}
		s.cancelPluginAdditivePreparation()
		s.closeRendererClients()
		s.closeChatGPTLogins()
		if s.sessionOperationCancel != nil {
			s.sessionOperationCancel()
		}
		s.cancelPluginClientRequests()
		s.cancelArtifactReads()
		s.closeProjectInboxDrains()
		s.cancelEngineAuth("")
		if s.storageMaintenanceCancel != nil {
			s.storageMaintenanceCancel()
			s.storageMaintenanceCancel = nil
		}
		if s.pluginTurnUnbind != nil {
			s.pluginTurnUnbind()
			s.pluginTurnUnbind = nil
		}
		if s.userQuestionUnbind != nil {
			s.userQuestionUnbind()
			s.userQuestionUnbind = nil
		}
		if s.userQuestionStop != nil {
			close(s.userQuestionStop)
			<-s.userQuestionDone
			s.userQuestionStop = nil
			s.userQuestionDone = nil
		}
		s.cancelSideThreads()
		// Synchronize with startBackground so no new owned goroutine can be
		// added after the shutdown waiter begins.
		s.backgroundMu.Lock()
		s.backgroundMu.Unlock()

		s.mu.Lock()
		closingThreads := s.threads
		threads := make([]*threadState, 0, len(closingThreads))
		for _, th := range closingThreads {
			if th != nil {
				threads = append(threads, th)
			}
		}
		s.threads = make(map[string]*threadState)
		s.mu.Unlock()

		// Close worker-turn admission before the first cancellation wave. BeginShutdown
		// synchronizes with any Manager.Spawn/Followup already at its commit point,
		// so StopAll cannot miss a worker that appears immediately behind it.
		controls := make(map[*agentcontrol.AgentControl]struct{})
		collectThreadAgentControls(threads, controls)
		for control := range controls {
			control.BeginShutdown()
		}
		// Cancellation is asynchronous, so issue it to all threads first instead
		// of serializing shutdown behind one provider.
		for _, th := range threads {
			th.mu.Lock()
			cancel := th.cancel
			th.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
		stops := make(map[*agentcontrol.AgentControl]<-chan struct{})
		for control := range controls {
			startShutdownStop(control, stops)
		}

		s.stopInferenceJournalMaintenance()

		// Stop every browser activity this process owns BEFORE dropping the
		// activity subscription below. Stop emits an EventStopped that
		// notifyActivityEvent forwards to the desktop so it can tear down the
		// backing WebContentsView; stdout is still writable here. Ordered after
		// unsubscribe the event would have no listener and the desktop would
		// leak a hidden view plus a ghost activity in the UI.
		s.stopBrowserActivitiesAndEmit()

		if s.activityUnsubscribe != nil {
			s.activityUnsubscribe()
			s.activityUnsubscribe = nil
		}

		// Release any in-flight server-initiated calls. Turn-context
		// cancellation above already unblocks callClient waiters via ctx.Done;
		// this is the belt-and-suspenders sweep for calls whose ctx outlives
		// Close. Delivery is non-blocking (buffered chans) so closeOnce can
		// never wedge the process on a shutdown drain.
		s.failPendingClientCalls()

		s.queuedTurnMu.Lock()
		queuedOnClose := make(map[string][]queuedTurn, len(s.pendingQueuedTurns))
		for threadID, entries := range s.pendingQueuedTurns {
			queuedOnClose[threadID] = append([]queuedTurn(nil), entries...)
		}
		clear(s.pendingQueuedTurns)
		// Claimed inputs belong to their drain until settlement. Preserve its
		// commit/cancellation evidence across shutdown so an explicit dequeue
		// cannot become a replayable shutdown discard while its receipt is
		// still being persisted. The owning drain removes the claim.
		clear(s.cancelledPendingSubmissions)
		clear(s.drainingQueuedTurns)
		s.queuedTurnMu.Unlock()
		for threadID, entries := range queuedOnClose {
			th := closingThreads[threadID]
			if th == nil || th.state == nil {
				providers.DebugLogf("cannot persist queued shutdown lifecycle for thread %q: captured state is unavailable", threadID)
				continue
			}
			for _, entry := range entries {
				s.notifyPluginTurnDiscardedWithRetry(th.state, threadID, entry, "app-server closed before queued turn started", true)
			}
		}
		s.agentCompletionMu.Lock()
		clear(s.pendingAgentCompletionTurns)
		clear(s.drainingAgentCompletionTurns)
		s.agentCompletionMu.Unlock()
		s.waitForOwnedShutdown(threads, controls, stops)
		s.interruptAttachedRunsOnClose()
		for _, th := range threads {
			s.releaseThreadAfterShutdown(th, stops)
		}
		s.releaseEngineScopes()
		s.clientInputMu.Lock()
		if s.clientInputLease != nil {
			_ = s.clientInputLease.Release()
			s.clientInputLease = nil
		}
		clientInputOwner := s.clientInputOwner
		s.clientInputMu.Unlock()
		if s.clientInputReceiptsEnabled() && clientInputOwner != "" {
			scope := s.clientInputScope("", "")
			scope.LiveOwner = clientInputOwner
			if binding, err := s.rt.RetainState(); err == nil {
				if err := binding.Sessions.RecoverClientInputs(s.rt.SessionDir, scope); err != nil {
					providers.DebugLogf("recover accepted inputs on close: %v", err)
				}
				_ = binding.Close()
			}
		}
		s.releasePresence()
	})
}

// ownedShutdownDrainTimeout bounds Close's wait for owned turns, workers, and
// their terminal finalizers. A wedged execution then surfaces as a loud log
// and a proceeding shutdown instead of a process that can never exit; durable
// terminal records and execution leases keep the drained state recoverable.
const ownedShutdownDrainTimeout = time.Minute

// A queue launch may be blocked in storage while holding queueDrainMu. StopAll
// cancels active workers first, then serializes queue compensation on that
// mutex; keep that compensation owned after bounded Close returns.
func startShutdownStop(control *agentcontrol.AgentControl, stops map[*agentcontrol.AgentControl]<-chan struct{}) {
	if control == nil || stops[control] != nil {
		return
	}
	done := make(chan struct{})
	stops[control] = done
	go func() {
		defer close(done)
		var work sync.WaitGroup
		work.Add(2)
		go func() { defer work.Done(); control.StopAll() }()
		go func() { defer work.Done(); control.YieldWorkerTerminalFinalizations() }()
		work.Wait()
	}()
}

func shutdownStopsActive(stops map[*agentcontrol.AgentControl]<-chan struct{}) bool {
	for _, done := range stops {
		select {
		case <-done:
		default:
			return true
		}
	}
	return false
}

func (s *Server) releaseThreadAfterShutdown(th *threadState, stops map[*agentcontrol.AgentControl]<-chan struct{}) {
	if th == nil {
		return
	}
	th.mu.Lock()
	var control *agentcontrol.AgentControl
	if th.execRuntime != nil {
		control = th.execRuntime.AgentControl
	}
	binding := th.state
	th.mu.Unlock()
	finish := func() {
		if done := stops[control]; done != nil {
			<-done
		}
		if control != nil {
			_ = control.WaitOwnedWorkerExecutions(context.Background())
		}
		s.releaseThreadRuntime(th)
		if binding != nil {
			_ = binding.Close()
		}
		if s.afterShutdownThreadReleaseForTest != nil {
			s.afterShutdownThreadReleaseForTest()
		}
	}
	if done := stops[control]; done != nil {
		select {
		case <-done:
			if control == nil || !control.HasOwnedWorkerExecutions() {
				finish()
				return
			}
		default:
		}
	}
	if control != nil && control.HasOwnedWorkerExecutions() {
		go finish()
		return
	}
	if stops[control] != nil {
		go finish()
		return
	}
	finish()
}

func (s *Server) waitForOwnedShutdown(threads []*threadState, controls map[*agentcontrol.AgentControl]struct{}, stops map[*agentcontrol.AgentControl]<-chan struct{}) {
	if s == nil {
		return
	}
	drainTimeout := ownedShutdownDrainTimeout
	if s.shutdownDrainTimeoutForTest > 0 {
		drainTimeout = s.shutdownDrainTimeoutForTest
	}
	deadline := time.Now().Add(drainTimeout)
	// Drain/title/side workers can still have admitted a turn immediately before
	// Close marked the server closed. Wait for those launchers first, then for
	// every turn/worker lease this Server owns to be released by its normal
	// terminal path. External owners are deliberately absent from these local
	// snapshots, so shutdown never waits for unrelated app-server processes.
	background := make(chan struct{})
	go func() {
		s.backgroundWG.Wait()
		close(background)
	}()
	select {
	case <-background:
	case <-time.After(drainTimeout):
		log.Printf("wuu: shutdown drain timed out after %s: owned background goroutines still running", drainTimeout)
	}
	// A launcher already inside startBackground may have attached a thread
	// runtime after the first shutdown snapshot. Once all launchers and turns
	// have stopped, collect those late local controls and cancel their workers
	// before waiting for the final execution leases.
	collectThreadAgentControls(threads, controls)
	for control := range controls {
		control.BeginShutdown()
		startShutdownStop(control, stops)
	}
	if s.afterWorkerShutdownStopWavesForTest != nil {
		s.afterWorkerShutdownStopWavesForTest()
	}
	for shutdownExecutionActive(threads, controls) || shutdownStopsActive(stops) {
		if !time.Now().Before(deadline) {
			log.Printf("wuu: shutdown drain timed out after %s: releasing with owned executions still active", drainTimeout)
			forceReleaseAbandonedThreadExecutions(threads)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// forceReleaseAbandonedThreadExecutions breaks the execution leases a timed-out
// drain would otherwise abandon. In a process-exit shutdown the OS reclaims
// the flocks anyway, but an embedded host (remote device sessions) keeps the
// process alive: a stuck turn goroutine would hold its same-process lock
// forever and every successor app-server would see the thread as busy.
// Releasing the lease makes the durable state read exactly like a crashed
// owner — the turn meta still says running, so the successor's normal
// crash-recovery settles it. The stuck goroutine's own finalizer becomes a
// no-op: releaseTurnExecutionLocked ignores a turn id that no longer matches.
func forceReleaseAbandonedThreadExecutions(threads []*threadState) {
	for _, th := range threads {
		if th == nil {
			continue
		}
		th.mu.Lock()
		if th.executionLease != nil {
			log.Printf("wuu: shutdown abandoning turn %q on thread %q: force-releasing its execution lease for successor recovery", th.currentTurn, th.ID)
			th.releaseTurnExecutionLocked(th.currentTurn)
		}
		th.mu.Unlock()
	}
}

func collectThreadAgentControls(threads []*threadState, controls map[*agentcontrol.AgentControl]struct{}) {
	for _, th := range threads {
		if th == nil {
			continue
		}
		th.mu.Lock()
		threadRuntime := th.execRuntime
		th.mu.Unlock()
		if threadRuntime != nil && threadRuntime.AgentControl != nil {
			controls[threadRuntime.AgentControl] = struct{}{}
		}
	}
}

func (s *Server) releasePresence() {
	if s == nil || s.presenceLease == nil {
		return
	}
	lease := s.presenceLease
	s.presenceLease = nil
	if err := lease.Release(); err != nil {
		providers.DebugLogf("release app-server presence: %v", err)
	}
}

func shutdownExecutionActive(threads []*threadState, controls map[*agentcontrol.AgentControl]struct{}) bool {
	for _, th := range threads {
		if th == nil {
			continue
		}
		th.mu.Lock()
		owned := th.executionLease != nil || th.admissionReserved
		th.mu.Unlock()
		if owned {
			return true
		}
	}
	for control := range controls {
		if control != nil && control.HasOwnedWorkerExecutions() {
			return true
		}
	}
	return false
}

func RunStdio(ctx context.Context, rt *runtime.Session, in io.Reader, out io.Writer, options ...ServerOption) error {
	return runStdio(ctx, rt, in, out, nil, false, options...)
}

// RunStdioForDevice runs the protocol loop for a remote device session,
// binding the device/push_* methods to the transport's per-device registrar.
// Local desktop sessions call RunStdio, which leaves the registrar nil so
// those methods fail explicitly instead of parking tokens nowhere.
func RunStdioForDevice(ctx context.Context, rt *runtime.Session, in io.Reader, out io.Writer, registrar PushRegistrar, options ...ServerOption) error {
	return runStdio(ctx, rt, in, out, registrar, true, options...)
}

func runStdio(ctx context.Context, rt *runtime.Session, in io.Reader, out io.Writer, registrar PushRegistrar, remoteDevice bool, options ...ServerOption) error {
	if rt == nil {
		return errors.New("runtime session is required")
	}
	if caller, ok := principal.FromContext(ctx); ok {
		options = append(options, WithPrincipal(caller))
	}
	if _, authenticated := principal.FromContext(ctx); !remoteDevice && !authenticated {
		options = append(options, WithTrustedLocalTransport())
	}
	s := New(rt, out, options...)
	s.pushRegistrar = registrar
	s.remoteDevice = remoteDevice
	defer s.Close()
	if s.startupErr != nil {
		return s.startupErr
	}
	if s.bootOwner && strings.TrimSpace(rt.WuuHome) != "" {
		maintenanceCtx, cancel := context.WithCancel(context.Background())
		s.storageMaintenanceCancel = cancel
		if !s.startBackground(func() {
			s.maintainSessionStorage(maintenanceCtx)
		}) {
			cancel()
			s.storageMaintenanceCancel = nil
		}
	}
	return runStdioScanner(ctx, s, in)
}

func (s *Server) handleLine(ctx context.Context, raw []byte, readAt ...time.Time) error {
	if s == nil {
		return errors.New("app-server is required")
	}
	if s.startupErr != nil {
		return s.startupErr
	}
	defer s.rpcTiming.flushResponses()
	handlerStarted := s.rpcTiming.now()
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return s.writeResponse(nil, nil, fmt.Errorf("parse request: %w", err))
	}
	var received time.Time
	if len(readAt) != 0 {
		received = readAt[0]
	}
	req.timing = s.rpcTiming.begin(req, handlerStarted, received)
	defer req.timing.mark("handler_end")
	// A line with an id but no method is the desktop client's Response to a
	// server-initiated request (browser/*). Route it to the waiting caller
	// before the method switch, otherwise it falls through to default and gets
	// answered with an "unknown method" error, silently dropping the reply.
	if req.Method == "" && len(req.ID) > 0 {
		s.deliverClientResponse(raw)
		return nil
	}
	// Ingress dispatch is ordered, but admitted handlers may still be running.
	// Reject a reused ID before any early error response can consume the first
	// request's pending principal/scope and make it fall back to the pipe owner.
	// Reverse client responses above belong to a separate pending table.
	s.access.mu.Lock()
	_, pending := s.access.pending[string(req.ID)]
	s.access.mu.Unlock()
	if pending {
		return errors.New("request id is already in flight")
	}

	if strings.HasPrefix(req.Method, "transport/") {
		return s.handleTrustedTransport(ctx, req)
	}
	ctx = principal.WithContext(ctx, s.Principal(ctx))
	var effectErr error
	ctx, effectErr = requestEffectContext(ctx, req)
	if effectErr != nil {
		return s.writeResponse(req.ID, nil, effectErr)
	}
	if err := s.authorizeRequest(ctx, req); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.trackAuthorizedRequest(ctx, req); err != nil {
		return err
	}
	ctx = s.captureAuthorizedResponse(ctx, req)
	if _, ordered := ctx.Value(stdioResumeDispatchKey{}).(bool); ordered {
		if deferred, err := s.deferStdioResume(ctx, req); deferred {
			return err
		}
	}
	return s.dispatchAuthorizedRequest(ctx, req)
}

func (s *Server) dispatchAuthorizedRequest(ctx context.Context, req Request) error {
	switch req.Method {
	case "session/federation":
		return s.handleSessionFederation(ctx, req)
	case MethodStateInspectionsList, MethodStateInspectionsReconcile:
		return s.handleStateInspections(ctx, req)
	case MethodInitialize:
		return s.handleInitialize(req)
	case MethodConfigRead:
		return s.handleConfigRead(req)
	case MethodConfigModelUpdate:
		return s.handleConfigModelUpdate(req)
	case MethodConfigAdvancedUpdate:
		return s.handleConfigAdvancedUpdate(req)
	case MethodConfigGeneralUpdate:
		return s.handleConfigGeneralUpdate(req)
	case MethodEngineList:
		// Engine discovery asks the external Codex host for its model list and
		// can take several seconds. The composer refreshes this inventory when
		// its model picker opens; running it on the serial stdio loop would make
		// the model/effort update clicked next wait behind discovery.
		if !s.startBackground(func() {
			if err := s.handleEngineList(req); err != nil {
				log.Printf("wuu: engine/list: %v", err)
			}
		}) {
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return nil
	case MethodEngineUpdate:
		return s.handleEngineUpdate(req)
	case MethodEngineAuthMethods, MethodEngineAuthenticate, MethodEngineAuthCancel:
		return s.handleEngineAuth(ctx, req)
	case MethodExtensionCatalogRefresh:
		return s.handleExtensionCatalogRefresh(req)
	case MethodExtensionPackageUpdate:
		return s.handleExtensionPackageUpdate(req)
	case MethodPluginPackageInspect, MethodPluginPackageInstall:
		return s.handlePluginPackageSource(ctx, req)
	case MethodPluginPackageRemove:
		return s.handlePluginPackageRemove(req)
	case MethodPluginDesktopModuleRead:
		return s.handlePluginDesktopModuleRead(req)
	case MethodPluginDesktopActivation:
		return s.handlePluginDesktopActivation(req)
	case MethodPluginIconRead:
		return s.handlePluginIconRead(req)
	case "plugin/skin-image/read":
		return s.handlePluginImageRead(req, true)
	case MethodPluginSettingGet:
		return s.handlePluginSettingGet(req)
	case MethodPluginSettingSet:
		return s.handlePluginSettingSet(req)
	case MethodPluginDiagnosticsList:
		return s.handlePluginDiagnosticsList(req)
	case MethodPluginGenerationDiagnosticsList:
		return s.handlePluginGenerationDiagnosticsList(req)
	case MethodPluginRegistryIntrospect:
		return s.handlePluginRegistryIntrospect(req)
	case MethodPluginExecutionsList:
		return s.handlePluginExecutionsList(req)
	case MethodPluginStorageGet:
		return s.handlePluginStorageGet(req)
	case MethodPluginStorageSet:
		return s.handlePluginStorageSet(req)
	case MethodPluginClientRequest:
		return s.handlePluginClientRequest(ctx, req)
	case MethodPluginClientCancel:
		return s.handlePluginClientCancel(ctx, req)
	case MethodUserQuestionList:
		return s.handleUserQuestionList(req)
	case MethodUserQuestionRespond:
		return s.handleUserQuestionRespond(req)
	case MethodUserQuestionCancel:
		return s.handleUserQuestionCancel(req)
	case MethodUserQuestionHold:
		return s.handleUserQuestionHold(req)
	case MethodConfigCodexCredentials:
		return s.handleConfigCodexCredentials(req)
	case MethodConfigCodexModels:
		// Model discovery performs an external Codex request. Keep it off the
		// serial stdio dispatch loop so unrelated local mutations, especially a
		// model selection made from the same menu, are not queued behind network
		// latency. Response writes and the model cache are independently locked.
		if !s.startBackground(func() {
			if err := s.handleConfigCodexModels(ctx, req); err != nil {
				log.Printf("wuu: config/codex/models: %v", err)
			}
		}) {
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return nil
	case "auth/chatgpt/login/start", "auth/chatgpt/login/poll", "auth/chatgpt/login/cancel", "auth/chatgpt/accounts", "auth/chatgpt/signout", "auth/chatgpt/models":
		return s.handleAuthChatGPT(ctx, req)
	case MethodAuthXAILoginStart:
		return s.handleAuthXAILoginStart(ctx, req)
	case MethodAuthXAILoginPoll:
		return s.handleAuthXAILoginPoll(ctx, req)
	case MethodAuthXAILoginCancel:
		return s.handleAuthXAILoginCancel(req)
	case MethodConfigCatalogProviders:
		return s.handleConfigModelCatalogProviders(req)
	case MethodConfigCatalogRefresh:
		if !s.startBackground(func() {
			if err := s.handleConfigModelCatalogRefresh(ctx, req); err != nil {
				log.Printf("wuu: config/model-catalog/refresh: %v", err)
			}
		}) {
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return nil
	case MethodConfigProviderRemove:
		return s.handleConfigProviderRemove(req)
	case MethodSkillList:
		return s.handleSkillList(ctx, req)
	case MethodThreadStart:
		return s.handleThreadStart(req)
	case MethodThreadResume:
		return s.handleThreadResume(req)
	case "thread/activity/read":
		return s.handleThreadActivityRead(req)
	case "thread/history/read":
		return s.handleThreadHistoryRead(req)
	case "message/image/read":
		return s.handleMarkdownImageRead(ctx, req)
	case "thread/artifact/resolve", "thread/artifact/read", "thread/artifact/cancel":
		return s.handleThreadArtifactRead(ctx, req)
	case "thread/attachment/read", "thread/content/read":
		return s.handleThreadAttachmentRead(ctx, req)
	case MethodThreadFork, MethodThreadEditMessage:
		if req.Method == MethodThreadFork && s.rt != nil && s.rt.UserBinding().IsUser() {
			return s.writeResponse(req.ID, nil, errors.New("user hosts fork execution through the named-agent session service"))
		}
		binding, stateErr := s.rt.RetainState()
		if stateErr != nil {
			return s.writeResponse(req.ID, nil, stateErr)
		}
		defer binding.Close()
		handle := s.handleThreadForkForState
		if req.Method == MethodThreadEditMessage {
			handle = s.handleThreadEditMessageForState
		}
		var params struct {
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(req.Params, &params) == nil && s.rt != nil {
			metadata, found, err := binding.Sessions.Find(strings.TrimSpace(params.ThreadID))
			if err == nil && found && metadata.EngineRef != "" && agentengine.NormalizeEngineID(metadata.EngineID) != agentengine.EngineWuu {
				// A native history RPC can wait on another process. Keep unrelated
				// conversation reads and Stop requests off that wait; the handler's
				// per-thread mutation lease still serializes its own conversation.
				if !s.startBackgroundWithState(binding, func(binding *runtime.State) {
					operationCtx, cancel := context.WithCancel(ctx)
					stop := context.AfterFunc(s.sessionOperationCtx, cancel)
					defer stop()
					defer cancel()
					if err := handle(binding, operationCtx, req); err != nil {
						log.Printf("wuu: %s: %v", req.Method, err)
					}
				}) {
					return s.writeResponse(req.ID, nil, errServerClosed)
				}
				return nil
			}
		}
		return handle(binding, ctx, req)
	case MethodThreadContextComposition:
		return s.handleThreadContextComposition(req)
	case MethodInstructionsList:
		return s.handleInstructionsList(req)
	case MethodSideThreadOpen:
		return s.handleSideThreadOpen(req)
	case MethodSideThreadGetHistory:
		return s.handleSideThreadGetHistory(req)
	case MethodSideThreadSend:
		return s.handleSideThreadSendMessage(req)
	case MethodSideThreadInterrupt:
		return s.handleSideThreadInterrupt(req)
	case MethodSideThreadReset:
		return s.handleSideThreadReset(req)
	case MethodThreadList:
		return s.handleThreadList(req)
	case MethodThreadListAll:
		return s.handleThreadListAll(req)
	case MethodThreadListArchived:
		return s.handleThreadListArchived(req)
	case MethodThreadSearch:
		return s.handleThreadSearch(req)
	case MethodThreadPreview:
		return s.handleThreadPreview(req)
	case "session/share/preview", "session/share/create", "session/share/read", "session/share/attachment/read", "session/share/revoke":
		return s.handleSessionShare(ctx, req)
	case "thread/textSnapshot":
		return s.handleThreadTextSnapshot(req)
	case "thread/control/return":
		return s.handleThreadControl(ctx, req)
	case "thread/control/take":
		return s.handleThreadTakeControl(req)
	case MethodProjectWork, MethodProjectSession:
		binding, err := s.rt.RetainState()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		defer binding.Close()
		if req.Method == MethodProjectWork {
			return s.handleProjectWork(binding, req)
		}
		return s.handleProjectSession(binding, req)
	case MethodThreadPin:
		return s.handleThreadPin(req)
	case MethodThreadOrganizationUpdate:
		return s.handleThreadOrganizationUpdate(req)
	case MethodSessionOrganizationList:
		return s.handleSessionOrganizationList(req)
	case MethodSessionFolderCreate:
		return s.handleOrganizationGroupCreate(req)
	case MethodSessionFolderUpdate:
		return s.handleOrganizationGroupUpdate(req)
	case MethodSessionFolderReorder:
		return s.handleOrganizationGroupReorder(req)
	case MethodSessionFolderDelete:
		return s.handleOrganizationGroupDelete(req)
	case MethodThreadArchive:
		return s.handleThreadArchive(ctx, req)
	case MethodThreadCompactStart:
		return s.handleThreadCompactStart(ctx, req)
	case MethodThreadRename:
		return s.handleThreadRename(req)
	case MethodThreadDelete:
		return s.handleThreadDelete(req)
	case MethodWorkspaceGitStatus, MethodWorkspaceGitChanges, MethodWorkspaceGitDiff:
		if !s.startBackground(func() {
			if err := s.handleWorkspaceGit(ctx, req); err != nil {
				log.Printf("wuu: workspace git view: %v", err)
			}
		}) {
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return nil
	case MethodWorkspaceDirectoryList, MethodWorkspaceFileRead, MethodWorkspaceFileResolve, MethodWorkspaceFileChunk:
		binding, err := s.rt.RetainState()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		defer binding.Close()
		// Workspace views can wait on providers or the filesystem after
		// authorization. Keep that work off the serial stdio reader and retain
		// the admitted authority until physical completion. Shutdown owns this
		// worker through the existing background drain.
		if !s.startBackgroundWithState(binding, func(binding *runtime.State) {
			if err := s.handleWorkspaceView(binding, req); err != nil {
				log.Printf("wuu: %s: %v", req.Method, err)
			}
		}) {
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return nil
	case MethodWorkspaceList:
		return s.handleWorkspaceList(req)
	case MethodWorkspaceStateCleanup:
		return s.handleWorkspaceStateCleanup(req)
	case MethodThreadRegenerateTitle:
		// Title generation is a synchronous LLM call. Keep it off the
		// serial stdio dispatch loop so unrelated requests are not queued
		// behind provider latency; response writes are independently locked.
		if !s.startBackground(func() {
			if err := s.handleThreadRegenerateTitle(ctx, req); err != nil {
				log.Printf("wuu: thread/regenerate-title: %v", err)
			}
		}) {
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return nil
	case MethodTextPolish:
		// Same reasoning as title regeneration: a synchronous LLM call must
		// not stall the serial dispatch loop.
		if !s.startBackground(func() {
			if err := s.handleTextPolish(req); err != nil {
				log.Printf("wuu: text/polish: %v", err)
			}
		}) {
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return nil
	case MethodGitCommitMessage:
		// Same reasoning as title regeneration: a synchronous LLM call must
		// not stall the serial dispatch loop.
		if !s.startBackground(func() {
			if err := s.handleGitCommitMessage(req); err != nil {
				log.Printf("wuu: git/commit-message: %v", err)
			}
		}) {
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return nil
	case MethodTurnStart:
		return s.handleTurnStart(ctx, req)
	case MethodTurnQueue:
		return s.handleTurnQueue(ctx, req)
	case MethodTurnUpdateQueued:
		return s.handleTurnUpdateQueued(ctx, req)
	case MethodTurnDequeue:
		return s.handleTurnDequeue(req)
	case MethodTurnSteer:
		return s.handleTurnSteer(ctx, req)
	case MethodTurnUnsteer:
		return s.handleTurnUnsteer(req)
	case MethodTurnRequeue:
		return s.handleTurnRequeue(req)
	case MethodTurnInterrupt:
		return s.handleTurnInterrupt(req)
	case MethodRunStart:
		return s.handleRunStart(ctx, req)
	case MethodRunInterrupt:
		return s.handleRunInterrupt(ctx, req)
	case MethodProcessList:
		return s.handleProcessList(req)
	case MethodProcessRead:
		return s.handleProcessRead(ctx, req)
	case MethodProcessWrite:
		return s.handleProcessWrite(req)
	case MethodProcessResize:
		return s.handleProcessResize(req)
	case MethodProcessStop:
		return s.handleProcessStop(req)
	case MethodProcessCancelNotifications:
		return s.handleProcessCancelNotifications(req)
	case MethodMCPList:
		return s.handleMCPList(req)
	case MethodMCPDefinitionRead:
		return s.handleMCPDefinitionRead(ctx, req)
	case MethodMCPConfigure:
		return s.handleMCPConfigure(ctx, req)
	case MethodMCPAuthCancel:
		return s.handleMCPAuthCancel(ctx, req)
	case MethodMCPConnect:
		return s.handleMCPConnect(ctx, req)
	case MethodMCPDisconnect:
		return s.handleMCPDisconnect(req)
	case MethodMCPRefresh:
		return s.handleMCPRefresh(ctx, req)
	case MethodMCPAuthStart:
		return s.handleMCPAuthStart(ctx, req)
	case MethodMCPAuthStatus:
		return s.handleMCPAuthStatus(ctx, req)
	case MethodMCPAuthFinish:
		return s.handleMCPAuthFinish(ctx, req)
	case MethodMCPAuthRemove:
		return s.handleMCPAuthRemove(ctx, req)
	case MethodActivityList:
		return s.handleActivityList(req)
	case MethodActivityTakeover:
		return s.handleActivityTakeover(req)
	case MethodActivityRelease:
		return s.handleActivityRelease(req)
	case MethodActivityStop:
		return s.handleActivityStop(req)
	case MethodShutdown:
		if err := s.writeResponse(req.ID, OKResult{OK: true}, nil); err != nil {
			return err
		}
		s.Close()
		return errShutdown
	case MethodSettingsUsage:
		return s.handleSettingsUsage(req)
	case MethodUsageOverview:
		// Every visit to the empty conversation home requests this summary.
		// Keep the store read off the serial stdio loop so a prompt sent right
		// away does not wait behind it.
		if !s.startBackground(func() {
			if err := s.handleUsageOverview(req); err != nil {
				log.Printf("wuu: usage/overview: %v", err)
			}
		}) {
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return nil
	case MethodDevicePushRegister:
		return s.handleDevicePushRegister(req)
	case MethodDevicePushUnregister:
		return s.handleDevicePushUnregister(req)
	default:
		return s.writeResponse(req.ID, nil, fmt.Errorf("unknown method %q", req.Method))
	}
}

func (s *Server) thread(id string) *threadState {
	s.mu.Lock()
	th := s.threads[id]
	s.mu.Unlock()
	if th == nil {
		return nil
	}
	th.mu.Lock()
	th.LastAccessedAt = time.Now().UTC()
	th.mu.Unlock()
	return th
}

func sanitizeStreamEvent(ev providers.StreamEvent) StreamEventPayload {
	out := StreamEventPayload{
		Type:      ev.Type,
		Content:   ev.Content,
		Truncated: ev.Truncated,
	}
	if ev.ModelStep != nil {
		copy := *ev.ModelStep
		out.ModelStep = &copy
	}
	if ev.Message != nil && !ev.Message.Hidden {
		out.Message = ev.Message
	}
	if ev.ToolCall != nil {
		out.ToolCall = ev.ToolCall
	}
	if ev.ToolResult != "" {
		out.ToolResult = ev.ToolResult
	}
	if ev.ToolResultDetail != nil {
		detail := ev.ToolResultDetail.Clone()
		out.ToolResultDetail = &detail
	}
	if ev.TodoUpdate != nil {
		out.TodoUpdate = ev.TodoUpdate
	}
	if ev.AgentActivity != nil {
		activity := *ev.AgentActivity
		out.AgentActivity = &activity
	}
	if ev.Lifecycle != nil {
		out.Lifecycle = sanitizeStreamLifecycle(ev.Lifecycle)
	}
	if ev.RequestContext != nil {
		out.RequestContext = ev.RequestContext
	}
	if ev.ProviderState != nil {
		out.ProviderState = ev.ProviderState
	}
	if ev.Usage != nil {
		out.Usage = ev.Usage
	}
	if ev.StopReason != "" {
		out.StopReason = ev.StopReason
	}
	if ev.FinishReason != "" {
		out.FinishReason = string(ev.FinishReason)
	}
	if ev.Error != nil {
		out.Error = ev.Error.Error()
	}
	return out
}

func sanitizeStreamLifecycle(lifecycle *providers.StreamLifecycle) *StreamLifecyclePayload {
	if lifecycle == nil {
		return nil
	}
	payload := &StreamLifecyclePayload{
		Phase:           string(lifecycle.Phase),
		OperationID:     lifecycle.OperationID,
		OperationKind:   string(lifecycle.OperationKind),
		WorkloadProfile: string(lifecycle.WorkloadProfile),
		PayloadVersion:  lifecycle.PayloadVersion,
		AttemptID:       lifecycle.AttemptID,
		Attempt:         lifecycle.Attempt,
		MaxAttempts:     lifecycle.MaxAttempts,
		SubmissionID:    lifecycle.SubmissionID,
		SubmissionCount: lifecycle.SubmissionCount,
		RetryCount:      lifecycle.RetryCount,
		MaxRetries:      lifecycle.MaxRetries,
		RetryInMS:       durationMilliseconds(lifecycle.RetryIn),
		ElapsedMS:       durationMilliseconds(lifecycle.Elapsed),
		Reason:          lifecycle.Reason,
		FailureCategory: lifecycle.FailureCategory,
		RecoveryAction:  lifecycle.RecoveryAction,
		BudgetDimension: lifecycle.BudgetDimension,
		ReplayReason:    lifecycle.ReplayReason,
		ResetPartial:    lifecycle.ResetPartial,
	}
	if lifecycle.Workflow.WorkflowID != "" {
		workflow := lifecycle.Workflow
		payload.Workflow = &WorkflowSnapshotPayload{
			ID: workflow.WorkflowID, Operations: workflow.Operations,
			Attempts: workflow.Attempts, Submissions: workflow.Submissions,
			TransportSwitches:          workflow.TransportSwitches,
			CredentialRefreshes:        workflow.CredentialRefreshes,
			PayloadTransforms:          workflow.PayloadTransforms,
			ChildOperations:            workflow.ChildOperations,
			RecoveryWaitMS:             workflow.RecoveryWaitMillis,
			KnownSubmissions:           workflow.KnownSubmissions,
			EstimatedSubmissions:       workflow.EstimatedSubmissions,
			UnknownBillableSubmissions: workflow.UnknownBillableSubmissions,
			KnownInputTokens:           workflow.KnownUsage.InputTokens,
			KnownOutputTokens:          workflow.KnownUsage.OutputTokens,
			EstimatedInputTokens:       workflow.EstimatedUsage.InputTokens,
			EstimatedOutputTokens:      workflow.EstimatedUsage.OutputTokens,
		}
	}
	return payload
}

func durationMilliseconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}

func decodeParams(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

func (s *Server) writeResponse(id json.RawMessage, result any, err error) error {
	started := s.rpcTiming.now()
	resp := Response{ID: id, Result: result}
	if err != nil {
		resp.Result = nil
		resp.Error = &ResponseError{
			Code:    "error",
			Message: err.Error(),
		}
	}
	var denied *access.Denied
	if errors.As(err, &denied) {
		resp.Error.Code = "access_denied"
	}
	writeErr := s.writeJSON(resp)
	s.rpcTiming.responseWritten(id, started, writeErr)
	return writeErr
}

func (s *Server) writeNotification(method string, params any) error {
	if s.deferredNotificationContent.Load() {
		switch payload := params.(type) {
		case TurnCompletedNotification:
			payload.Turn = notificationTurn(payload.ThreadID, payload.Turn)
			params = payload
		case TurnErrorNotification:
			payload.Turn = notificationTurn(payload.ThreadID, payload.Turn)
			params = payload
		case ThreadStartedNotification:
			payload.Thread = notificationThread(payload.Thread)
			params = payload
		case ThreadResumedNotification:
			payload.Thread = notificationThread(payload.Thread)
			params = payload
		case ThreadUpdatedNotification:
			payload.Thread = notificationThread(payload.Thread)
			params = payload
		}
	}
	return s.writeJSON(Notification{
		Method: method,
		Params: params,
	})
}

func (s *Server) writeJSON(v any) error {
	return s.writeJSONContext(context.Background(), v)
}

func (s *Server) writeJSONContext(ctx context.Context, v any) error {
	// A shared host is an internal fanout, not an authenticated recipient.
	// Its private sink establishes a fresh guard for each real connection.
	if notification, ok := v.(Notification); ok {
		if sink, ok := s.out.(interface{ writeRecipientNotification(Notification) error }); ok {
			return sink.writeRecipientNotification(notification)
		}
	}
	var allowed bool
	var guard func() error
	v, guard, allowed = s.authorizeOutbound(ctx, v)
	if !allowed {
		return nil
	}
	var transportErr error
	v, transportErr = s.wrapTrustedTransportResponse(v, guard)
	if transportErr != nil {
		return transportErr
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// Order the authority check with takeover/release notifications. An old
	// cancelled request must not be written after a fresh control grant.
	if err := activity.ControlErr(ctx); err != nil {
		return err
	}
	if guarded, ok := s.out.(GuardedWriter); ok {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		_, err = guarded.WriteAuthorized(raw, guard)
		return err
	}
	enc := json.NewEncoder(s.out)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write app-server message: %w", err)
	}
	return nil
}
