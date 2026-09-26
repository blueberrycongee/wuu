import { forgetLocalTurnTiming } from "./LocalTurnTiming";
import { subscribeServerEvents } from "./ServerEvents";
import { PhoneNavigationContext } from "./PhoneNavigationContext";
import { AccountScreen } from "./AccountScreen";
import { hostSupports } from "./HostCapabilities";
import { isTouchWebShell } from "./ComposerFocus";
import { useSidebarTouchGesture } from "./SidebarTouchGesture";
import { readThreadReadState, writeThreadReadState } from "./ThreadReadState";
/// <reference path="../shared/jsx-compat.d.ts" />

import {
  type CSSProperties,
  type RefObject,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  ActivitySession,
  BrowserDockTarget,
  Agent,
  DesktopProject,
  EngineInfo,
  EngineListResult,
  EngineUpdateParams,
  ExtensionPackageUpdateParams,
  InitializeResult,
  InputFile,
  InputImage,
  MessageContentPart,
  PopOutInitResult,
  PluginPackageInstallResult,
  PluginPackageRemoveResult,
  RuntimeContext,
  RunningThreadSnapshot,
  ServerEvent,
  SkillSummary,
  Thread,
  ThreadItem,
  ThreadStartParams,
  Turn,
  UserQuestionAnswer,
  UserQuestionRequest,
} from "../shared/protocol";
import {
  OPTIMISTIC_TURN_ID_PREFIX,
  awaitComposerImages,
  createComposerMessage,
  createOptimisticCompactTurn,
  createOptimisticTurn,
  dropOptimisticTurn,
  failOptimisticCompactTurn,
  inputFilesFromComposer,
  inputImagesFromComposer,
  interruptOptimisticTurn,
  isOptimisticTurnInterrupted,
  replaceOptimisticTurn,
  threadHasAcceptedComposerMessage,
  type QueuedComposerMessage,
} from "./ComposerMessages";
import {
  greetingFor,
  useCurrentHour,
  type GreetingContext,
} from "./greetings";
import {
  Composer,
  FloatingMenuPortal,
  isInsideFloatingMenu,
  type CodexModelLoadState,
  type CodexRuntimeMenu,
  type ComposerVariant,
  type PermissionMode,
} from "./ComposerView";
import {
  QueryHistoryPopover,
  type QueryHistoryEntry,
} from "./QueryHistoryPopover";
import { QueryHistoryRail } from "./QueryHistoryRail";
import { UserQuestionCard } from "./UserQuestionCard";
import { ConversationSearchOverlay } from "./ConversationSearchOverlay";
import {
  useConversationScrollState,
  type ConversationScrollSnapshot,
} from "./ConversationScrollState";
import { PullToNewSession } from "./PullToNewSession";
import { useConversationSearch } from "./ConversationSearchState";
import {
  SideThreadPanel,
  type SideThreadPanelHandle,
} from "./SideThreadPanel";
import { SideThreadComposer } from "./SideThreadComposer";
import { ConversationForkDialog } from "./ConversationForkDialog";
import { firstUserMessageText } from "./TurnViewHelpers";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import { AppSidebar } from "./AppSidebar";
import {
  type EnvironmentPanelMenu,
  type EnvironmentPanelMotionState,
} from "./EnvironmentPanel";
import { createEnvironmentActions } from "./EnvironmentActions";
import { useGitActionBusy } from "./GitActionBusy";
import {
  activeTodoUpdateForThread,
  activeSessionTab,
  activeThreadForState,
  activeThreadIDForState,
  activeTurnAcceptsSteering,
  activeTurnIsAnswerReady,
  activeTurnForThread,
  latestContextUsageForThread,
  activeTurnIDForThread,
  bindActiveSessionTabToThread,
  cloneSessionTabDraft,
  composerDraftHasContent,
  conversationPaneThreadsByID,
  createDraftSessionTab,
  emptyComposerDraft,
  ensureSessionTab,
  handleStreamingNotification,
  isCoalescedBackgroundThreadEvent,
  initialSplitComposerDrafts,
  initialState,
  isAnyThreadRunning,
  isStateActiveThreadRunning,
  isThreadExecuting,
  isThreadRunning,
  isThreadUnread,
  latestTodoUpdateForThread,
  markThreadSummariesViewed,
  markThreadTurnsViewed,
  pinnedThreadSummaries,
  presentationRunningThreadIDs,
  queryTextForUserItem,
  SCRATCH_PSEUDO_PROJECT_ID,
  scratchThreadSummaries,
  queryTextsForThread,
  requestedHandoffIntentForThread,
  reduceServerEvent,
  reconcileListedThreadState,
  resolveComposerRunningAction,
  resolveThreadRuntimeContext,
  requireThread,
  runtimeContextKey,
  sameRuntimeContext,
  serverEventShouldRefreshGit,
  serverEventTargetsActiveContext,
  sessionTabForLoadedRuntime,
  setThreadForPane,
  sortThreads,
  summarizeWorkspaceThreadsForSidebar,
  summarizeThreadsForSidebar,
  threadBelongsToWorkspace,
  threadForTab,
  threadForPane,
  threadSessionTabID,
  turnStreamStatusForThread,
  updateThreadByID,
  upsertThread,
  upsertTurn,
  withExtensionInventoryForContext,
  withLoadedRuntimeSessionTab,
  workspacePanelContext,
  type AppState,
  type ComposerDraftState,
  type ConversationPaneID,
  type SessionTab,
  type ThreadSummary,
} from "./AppState";
import {
  rightPanelMotionMs,
  sidebarDrawerExitMs,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  sidebarMotionMs,
  WORKSPACE_RIGHT_PANEL_MAX_WIDTH,
  WORKSPACE_RIGHT_PANEL_MIN_WIDTH,
  useAppLayoutState,
} from "./AppLayoutState";
import { CommitChangesDialog, PullRequestDialog } from "./GitDialogs";
import { motionDurationMs } from "./motion";
import type { ContextCompositionEntry } from "./ContextCompositionCard";
import type { InstructionFilesEntry } from "./InstructionFilesCard";
import {
  rememberedEngineRuntime,
  resolveDraftEngineMemory,
  writeDraftEngineMemory,
} from "./DraftEngineMemory";
import {
  FirstRunOnboarding,
  hasOnboardingProvider as hasReadyProvider,
} from "./FirstRunOnboarding";
import {
  EmptyConversationHome,
  RuntimeLoading,
} from "./LoadingViews";
import { EmptyHomeOverview } from "./EmptyHomeOverview";
import { WuuMascotRuntimeProvider } from "./WuuMascot";
import { deriveActiveSessionHints } from "./activeSessionHint";
import {
  providerModelContextWindow,
  pullRequestUnavailableReason,
} from "./RuntimeHelpers";
import type { SettingsPage } from "./SettingsView";
import {
  ENABLE_CONVERSATION_TURN_RAIL,
  ENABLE_EMBEDDED_BROWSER,
  ENABLE_ACCOUNT,
} from "./FeatureFlags";
import { ArchiveTip } from "./ArchiveTip";
import { TopNotice } from "./TopNotice";
import { UILayerPortal } from "./ui/layers/UILayerHost";
import { showErrorToast, showToast } from "./Toast";
import { setOpenThreadInSplitHandler } from "./ConversationSplitBridge";
import { CircleAlert, RefreshCw } from "./WuuIcons";
import type {
} from "../shared/protocol";
import { useSettingsRuntimeState } from "./SettingsRuntimeState";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";
import { JumpToLatestPill } from "./JumpToLatestPill";
import { BrowserPiPHostReporter } from "./BrowserPiPHostReporter";
import { ConversationStatusCluster } from "./ConversationStatusCluster";
import { externalAgentActivityStore } from "./ExternalAgentActivityStore";
import { SkillsCatalog } from "./SkillsCatalog";
import { userVisibleThreads } from "./SkillsAssistant";
import { isForegroundControlled, useBrowserVisibility } from "./BrowserVisibility";
import { useSideThreadController } from "./SideThreadController";
import {
  isCancellationMessage,
  rawErrorMessage,
  statusMessageForError,
} from "./UserFacingErrors";
import { scrollToUserMessage, TurnView } from "./TurnView";
import { ConversationTurnRail } from "./ConversationTurnRail";
import {
  WorkspaceRightPanel,
} from "./WorkspacePanels";
import { WorkspaceDocumentTurnDock } from "./WorkspaceDocumentTurnDock";
import { useWorkspaceToolState } from "./WorkspaceToolState";
import type { WorkspaceViewTab } from "./WorkspaceViewTabs";
import { ImagePreviewProvider } from "./ImagePreview";
import { ArtifactPreviewContext } from "./ArtifactPreviewContext";
import { useArtifactAutoPreview } from "./ArtifactAutoPreview";
import {
  openWorkspaceBrowserOrExternal,
  workspaceBrowserFocusDecision,
  WorkspaceBrowserOpenContext,
  type WorkspaceBrowserOpenTarget,
} from "./WorkspaceBrowserOpen";
import { requestWorkspaceBrowserNavigation } from "./WorkspaceBrowserNavigation";
import {
  desktopPluginHost,
  desktopWorkbenchController,
  useDesktopPluginRuntime,
} from "./plugins/DesktopPluginRuntime";
import { DesktopWorkbench } from "./plugins";
import { usePrimaryPluginViewCover } from "./PrimaryPluginViewCover";
import { releaseWindowResizeClass, WINDOW_RESIZING_CLASS } from "./WindowResizeState";
import { useComposerDraftState } from "./ComposerDraftState";
import { useComposerPendingState } from "./ComposerPendingState";
import { useSidebarDrawerState } from "./SidebarDrawerState";
import { useSidebarWorkspaceState } from "./SidebarWorkspaceState";
import { useViewSwitchState } from "./ViewSwitchState";
import { turnTelemetryStore } from "./TurnTelemetryStore";
import {
  activitiesForThread,
  clearActivitiesForWorkdir,
  emptyActivitySessions,
  mergeActivityList,
  reduceActivitySessionEvent,
  serverEventCarriesActivitySessionUpdate,
} from "./ActivitySessions";
import {
  loadPopOutRuntime,
  loadRuntime,
  loadRuntimeRestore,
  loadThreadListRefresh,
  applyRuntimeRestore,
  selectRuntimeContext,
} from "./RuntimeLoadState";
import { createWorkspaceRuntimeActions } from "./WorkspaceRuntimeActions";
import { createWorkspaceActions } from "./WorkspaceActions";
import { createSessionTabActions } from "./SessionTabActions";
import { createThreadActivationActions } from "./ThreadActivationActions";
import { createThreadMutationActions } from "./ThreadMutationActions";
import {
  conversationHeadingTitle,
  customDraftConversationTitle,
} from "./ThreadTitles";
import { createRuntimeSettingsActions } from "./RuntimeSettingsActions";
import { createConversationPaneActions } from "./ConversationPaneActions";
import {
  createConversationHistoryActions,
  type HistoryMessageEditState,
  type PendingForkState,
} from "./ConversationHistoryActions";
import { localizedText, resolveLocalizedText, translateCurrent, useI18n } from "./i18n";
import { CachedConversationPanes } from "./CachedConversationPanes";
import {
  retainCachedConversationPaneThreads,
  selectCachedConversationPaneIDs,
} from "./ConversationPaneCache";
import {
  ConversationSidePanels,
  ConversationSplitLayoutRenderer,
  ConversationTitleActions,
  ConversationTitleContent,
  SettingsShellRenderer,
} from "./ConversationShellRenderers";
import {
  runtimeViewForConversation,
  runtimeViewForSession,
} from "./SessionRuntimeState";
export { SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS } from "./SidebarDrawerState";

const ENGINE_INVENTORY_STALE_MS = 6 * 60 * 60 * 1000;
// Globalized-sheet phases: docked (grid child) → arming (promoted to a
// full-window fixed sheet, teleported over its dock slot for one frame) →
// open (slid to cover the window) → exiting (sliding back to park) →
// docking (teleported back into the grid for one frame, no transition) →
// docked. Transitions retarget mid-flight, so rapid toggles stay continuous.
type WorkspaceSheetPhase = "docked" | "arming" | "open" | "exiting" | "docking";
const ENVIRONMENT_PANEL_WIDTH_PX = 328;
const ENVIRONMENT_PANEL_WIDTH_CSS = `${ENVIRONMENT_PANEL_WIDTH_PX}px`;
// Cap on the number of bars rendered in the always-visible rail. The
// rail is a thin at-a-glance index; if there are more queries than fit,
// we collapse the tail into a single bar.
const QUERY_HISTORY_RAIL_MAX_BARS = 20;
type EnvironmentDialog = "commit" | "pull-request" | null;
/**
 * True when a turn/start failure means the user has no usable model
 * configuration (no provider with a key, or model roles unresolved).
 * The Go side raises these from modelroles resolution; they map to the
 * "configure a model provider" onboarding toast instead of the composer
 * status row.
 */
function isNoModelConfiguredError(message: string): boolean {
  const lower = message.toLowerCase();
  return (
    lower.includes("main model is required") ||
    lower.includes("has no model") ||
    lower.includes("has no provider") ||
    lower.includes("not found in providers") ||
    lower.includes("model is required")
  );
}

type EngineRuntimeSelection = { model: string; effort: string };

function defaultEngineRuntimeSelection(engine?: EngineInfo): EngineRuntimeSelection {
  const model = engine?.models?.find((item) => item.is_default) ?? engine?.models?.[0];
  const efforts = model?.supported_efforts ?? [];
  const effort = model?.default_effort && efforts.includes(model.default_effort)
    ? model.default_effort
    : efforts.includes("medium")
      ? "medium"
      : efforts[0] ?? "";
  return { model: model?.id ?? "", effort };
}

function useStableCallback<T extends (...args: any[]) => any>(callback: T): T {
  const callbackRef = useRef(callback);
  useLayoutEffect(() => {
    callbackRef.current = callback;
  });
  return useCallback(
    ((...args: Parameters<T>): ReturnType<T> => callbackRef.current(...args)) as T,
    [],
  );
}

function readPopOutInit(): PopOutInitResult | null {
  try {
    const init = window.wuu.popOutInit();
    return init.kind && init.context ? init : null;
  } catch {
    return null;
  }
}

type MainComposerFocusRequest = {
  target: ComposerVariant;
  origin: Element | null;
  interactionVersion: number;
  matchesDestination?: (state: AppState) => boolean;
};

function formatUserQuestionSteerPrompt(
  request: UserQuestionRequest,
  answer: UserQuestionAnswer,
): string {
  return request.questions.map((question) => {
    const item = answer.answers.find((entry) => entry.id === question.id);
    const parts = [...(item?.selected ?? [])];
    if (item?.custom?.trim()) parts.push(item.custom.trim());
    return `${question.question}\n${parts.join(", ") || "(no answer)"}`;
  }).join("\n\n");
}

export function App(): JSX.Element {
  const phoneNavigation = useContext(PhoneNavigationContext);
  const { locale, t } = useI18n();
  const [popOutInit] = useState<PopOutInitResult | null>(() => readPopOutInit());
  const poppedOutMode = Boolean(popOutInit?.kind && popOutInit.context);
  const [state, setState] = useState<AppState>(() => ({
    ...initialState,
    lastViewedTurnByThreadID: readThreadReadState(),
  }));
  useEffect(() => {
    writeThreadReadState(state.lastViewedTurnByThreadID);
  }, [state.lastViewedTurnByThreadID]);
  const [userQuestions, setUserQuestions] = useState<UserQuestionRequest[]>([]);
  const resolvedUserQuestionIDsRef = useRef(new Set<string>());
  const userQuestionApiAvailable =
    typeof window.wuu.listUserQuestions === "function" &&
    typeof window.wuu.answerUserQuestion === "function" &&
    typeof window.wuu.cancelUserQuestion === "function";
  useDesktopPluginRuntime(state.initialized?.extension_inventory);
  const {
    prompt,
    promptRevision,
    setPrompt,
    setPromptFromInput,
    composerImages,
    setComposerImages,
    composerFiles,
    setComposerFiles,
    splitComposerDrafts,
    setSplitComposerDrafts,
    attachComposerAttachmentFiles,
    removeComposerImage,
    removeComposerFile,
    setSplitComposerPrompt,
    attachSplitComposerAttachmentFiles,
    removeSplitComposerImage,
    removeSplitComposerFile,
    moveSplitDraftToGlobalComposer,
    currentPrimaryComposerDraft,
    restorePrimaryComposerDraft,
  } = useComposerDraftState();
  const [historyMessageEdit, setHistoryMessageEdit] =
    useState<HistoryMessageEditState | undefined>(undefined);
  const composerDraftsRef = useRef({ primary: currentPrimaryComposerDraft, split: splitComposerDrafts });
  composerDraftsRef.current = { primary: currentPrimaryComposerDraft, split: splitComposerDrafts };
  const [activitySessions, setActivitySessions] = useState(emptyActivitySessions);
  const [workspaceMenuOpen, setWorkspaceMenuOpen] = useState(false);
  const closeWorkspaceMenu = useCallback(() => setWorkspaceMenuOpen(false), []);
  const appShellRef = useRef<HTMLDivElement>(null);
  const settingsShellRef = useRef<HTMLDivElement>(null);
  const [mainComposerFocusRequest, setMainComposerFocusRequest] =
    useState<MainComposerFocusRequest | null>(null);
  const userInteractionVersionRef = useRef(0);
  const {
    compactNavigation,
    sidebarWidth,
    sidebarCollapsed,
    resizingSidebar,
    sidebarAnimating,
    clampedWorkspaceRightPanelWidth,
    resizingRightPanel,
    rightPanelOpen,
    rightPanelAnimating,
    effectiveSidebarWidth,
    workspaceRightPanelAutoGlobalized,
    workspaceRightPanelDockableWithoutSidebar,
    setRightPanelOpenWithMotion,
    animateRightPanelLayout,
    animateSidebarLayout,
    startSidebarResize,
    startRightPanelResize,
    handleRightPanelSeparatorKey,
    resetWorkspaceRightPanelWidth,
    toggleSidebar,
    handleSidebarSeparatorKey,
    splitLeftPercent,
    resizingSplit,
    startSplitResize,
    handleSplitSeparatorKey,
    resetSplitPercent,
  } = useAppLayoutState({
    layoutRootRef: appShellRef,
    settingsLayoutRootRef: settingsShellRef,
    onCloseWorkspaceMenu: closeWorkspaceMenu,
  });
  const [rightPanelManualGlobalized, setRightPanelManualGlobalized] =
    useState(false);
  const rightPanelAutoGlobalized =
    rightPanelOpen && workspaceRightPanelAutoGlobalized;
  const rightPanelGlobalized =
    rightPanelOpen &&
    (rightPanelManualGlobalized || rightPanelAutoGlobalized);
  const [workspaceSheetPhase, setWorkspaceSheetPhase] =
    useState<WorkspaceSheetPhase>(rightPanelGlobalized ? "open" : "docked");
  useLayoutEffect(() => {
    if (rightPanelGlobalized) {
      if (workspaceSheetPhase === "open" || workspaceSheetPhase === "arming") {
        return undefined;
      }
      setWorkspaceSheetPhase("arming");
      return undefined;
    }
    if (workspaceSheetPhase === "docked") {
      return undefined;
    }
    if (workspaceSheetPhase === "arming") {
      // Interrupted before the slide even started: demote instantly.
      setWorkspaceSheetPhase("docked");
      return undefined;
    }
    if (workspaceSheetPhase === "docking") {
      return undefined;
    }
    setWorkspaceSheetPhase("exiting");
    const timer = window.setTimeout(
      () => setWorkspaceSheetPhase("docking"),
      motionDurationMs("--sheet-exit-duration", 220),
    );
    return () => window.clearTimeout(timer);
  }, [rightPanelGlobalized, workspaceSheetPhase]);
  useEffect(() => {
    if (workspaceSheetPhase !== "arming" && workspaceSheetPhase !== "docking") {
      return undefined;
    }
    // Double rAF: arming lets the parked transform commit before retargeting
    // to open (so the enter transition has a start value); docking lets the
    // grid snap commit transition-free before the data attribute clears.
    const next = workspaceSheetPhase === "arming" ? "open" : "docked";
    const from = workspaceSheetPhase;
    const raf = requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        setWorkspaceSheetPhase((current) => (current === from ? next : current));
      });
    });
    return () => cancelAnimationFrame(raf);
  }, [workspaceSheetPhase]);
  // The bell view is a sidebar-level navigation mode rather than sidebar-local
  // state: settings and account replace the whole workbench tree, so the flag
  // has to live above it for the user to come back to the view they left.
  const [unreadViewOpen, setUnreadViewOpen] = useState(false);
  const [attentionStickyIDs, setAttentionStickyIDs] = useState<Set<string>>(() => new Set());
  // A manually expanded workspace owns the main stage, not the navigation
  // rail. Keep a docked sidebar docked; only an already-collapsed or compact
  // sidebar remains a drawer while the workspace is expanded.
  const sidebarDrawerMode = compactNavigation || sidebarCollapsed;
  const {
    sidebarDrawerPhase,
    sidebarHoverZoneRef,
    cancelSidebarDrawerOpen,
    openSidebarDrawer,
    openSidebarDrawerNow,
    scheduleSidebarDrawerOpen,
    closeSidebarDrawer,
    scheduleSidebarDrawerCloseFromPointerLeave,
  } = useSidebarDrawerState({
    appShellRef,
    sidebarCollapsed: sidebarDrawerMode,
    resizingSidebar,
    motionMs: sidebarDrawerExitMs,
    dockingMotionMs: sidebarMotionMs,
  });
  const sidebarDrawerVisible = sidebarDrawerPhase === "open";
  const toggleSessionSwitcher = useCallback((): void => {
    if (!compactNavigation) {
      toggleSidebar();
      return;
    }
    if (sidebarDrawerVisible) {
      closeSidebarDrawer();
      return;
    }
    openSidebarDrawerNow();
  }, [
    closeSidebarDrawer,
    compactNavigation,
    openSidebarDrawerNow,
    sidebarDrawerVisible,
    toggleSidebar,
  ]);
  const closeCompactSessionSwitcher = useCallback((): void => {
    if (compactNavigation) {
      closeSidebarDrawer();
    }
  }, [closeSidebarDrawer, compactNavigation]);
  const {
    collapsedSidebarSectionIDs,
    expandedSidebarSectionIDs,
    loadingWorkspaceThreadIDs,
    workspaceThreadsByWorkspaceID,
    cachedScratchThreads,
    sidebarSectionOrder,
    setSidebarSectionOrder,
    cacheSidebarThreads,
    updateCachedSidebarThread,
    updateCachedSidebarThreadPinned,
    removeCachedSidebarThread,
    syncSidebarServerEvent,
    toggleSidebarSectionCollapsed,
  } = useSidebarWorkspaceState({
    // Let the visible workspace finish booting before background catalogs
    // compete for the same remote connection.
    backgroundLoadingEnabled: Boolean(state.initialized) || state.status !== "connecting",
    projects: state.projects,
    threads: state.threads,
    activeContext: state.activeContext,
    activeWorkspaceID: state.activeProjectId,
    setStatus: (status) =>
      setState((current) => ({
        ...current,
        status,
      })),
  });
  const syncSidebarServerEventStable = useStableCallback(syncSidebarServerEvent);
  // Settings and account pages unmount the sidebar; keep manual folds here.
  const [collapsedFolderIDs, setCollapsedFolderIDs] = useState<Set<string>>(() => new Set());
  const [runtimeMenuOpen, setRuntimeMenuOpen] = useState(false);
  const [accessMenuOpen, setAccessMenuOpen] = useState(false);
  const [codexRuntimeMenu, setCodexRuntimeMenu] =
    useState<CodexRuntimeMenu>(null);
  const [codexModels, setCodexModels] = useState<CodexModelLoadState>({
    loading: false,
    error: "",
    models: [],
  });
  const [branchMenuOpen, setBranchMenuOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [accountOpen, setAccountOpen] = useState(false);
  useSidebarTouchGesture(
    appShellRef,
    Boolean(state.initialized) && compactNavigation && !poppedOutMode && !settingsOpen && !accountOpen,
    sidebarDrawerPhase,
    openSidebarDrawerNow,
    closeSidebarDrawer,
  );
  const [onboardingComplete, setOnboardingComplete] = useState(
    () => window.wuu?.initialOnboardingComplete ?? true,
  );

  const [settingsInitialPage, setSettingsInitialPage] =
    useState<SettingsPage>("providers");
  const {
    settingsUsage,
    settingsUsageLoading,
    settingsUsageError,
    codexPets,
    codexPetsLoading,
    codexPetsError,
    refreshCodexPets,
    updateCodexPets,
  } = useSettingsRuntimeState({ settingsOpen });

  const [workspaceFilter, setWorkspaceFilter] = useState("");
  const {
    workspaceViewTabs,
    workspaceActiveViewTabID,
    workspaceActiveFileTabID,
    ensureWorkspaceToolTab,
    openWorkspaceTool,
    openWorkspacePluginTool,
    openWorkspaceDiffTab,
    openWorkspaceFileTab,
    openWorkspaceArtifactTab,
    showWorkspaceToolPicker,
    focusWorkspaceViewTab,
    closeWorkspaceViewTab,
    closeWorkspaceViewTabsWhere,
    reorderWorkspaceViewTabs,
    toggleRightPanel,
  } = useWorkspaceToolState({
    rightPanelOpen,
    setRightPanelOpenWithMotion,
  });
  const [environmentPanelOpen, setEnvironmentPanelOpen] = useState(false);
  const [environmentPanelDismissed, setEnvironmentPanelDismissed] =
    useState(false);
  const [environmentPanelHasRoom, setEnvironmentPanelHasRoom] = useState(() =>
    typeof window === "undefined"
      ? false
      : window.matchMedia("(min-width: 1320px) and (min-height: 680px)")
          .matches,
  );
  const [environmentPanelMounted, setEnvironmentPanelMounted] = useState(false);
  const [environmentPanelClosing, setEnvironmentPanelClosing] = useState(false);
  const [environmentPanelReserved, setEnvironmentPanelReserved] =
    useState(false);
  const [environmentPanelMenu, setEnvironmentPanelMenu] =
    useState<EnvironmentPanelMenu>(null);
  const [rightPanelFilePath, setRightPanelFilePath] = useState<
    string | undefined
  >(undefined);
  const [focusedWorkspaceContext, setFocusedWorkspaceContext] =
    useState<RuntimeContext | undefined>(undefined);
  useEffect(() => {
    if (!rightPanelOpen) {
      setFocusedWorkspaceContext(undefined);
    }
  }, [rightPanelOpen]);
  // Manual focus is user intent; automatic focus is derived independently
  // from current layout capacity above. Closing the workspace clears only the
  // manual request, while resizing can freely enter/leave automatic focus.
  useEffect(() => {
    if (!rightPanelOpen) {
      setRightPanelManualGlobalized(false);
    }
  }, [rightPanelOpen]);
  const toggleWorkspacePanelGlobalized = useCallback((): void => {
    animateRightPanelLayout();
    // The under-stage sidebar column collapses/restores while the sheet
    // covers it; give that structural change its shared motion too.
    animateSidebarLayout();
    if (!rightPanelGlobalized) {
      setRightPanelManualGlobalized(true);
      return;
    }
    setRightPanelManualGlobalized(false);
    if (
      rightPanelAutoGlobalized &&
      workspaceRightPanelDockableWithoutSidebar &&
      !sidebarCollapsed
    ) {
      toggleSidebar();
    }
  }, [
    animateRightPanelLayout,
    animateSidebarLayout,
    rightPanelAutoGlobalized,
    rightPanelGlobalized,
    sidebarCollapsed,
    toggleSidebar,
    workspaceRightPanelDockableWithoutSidebar,
  ]);
  const revealConversationFromFocusedWorkspace = useCallback((): void => {
    desktopWorkbenchController.deactivateRegion("primary");
    if (!rightPanelGlobalized) {
      return;
    }
    setRightPanelManualGlobalized(false);
    if (!rightPanelAutoGlobalized) {
      return;
    }
    if (!workspaceRightPanelDockableWithoutSidebar) {
      setRightPanelOpenWithMotion(false);
      return;
    }
    if (!sidebarCollapsed) {
      toggleSidebar();
    }
  }, [
    rightPanelAutoGlobalized,
    rightPanelGlobalized,
    setRightPanelOpenWithMotion,
    sidebarCollapsed,
    toggleSidebar,
    workspaceRightPanelDockableWithoutSidebar,
  ]);
  const [environmentDialog, setEnvironmentDialog] =
    useState<EnvironmentDialog | null>(null);
  const [contextCompositionEntries, setContextCompositionEntries] = useState<
    ContextCompositionEntry[]
  >([]);
  const [instructionFilesEntries, setInstructionFilesEntries] = useState<
    InstructionFilesEntry[]
  >([]);
  const [archiveTip, setArchiveTip] = useState<{
    threadID: string;
    threadTitle: string;
    errorMessage?: string;
    // Present when the archive failed with a running-turn rejection: the tip
    // offers the force escape hatch and retries with this summary.
    forceRetryThread?: ThreadSummary;
  } | null>(null);
  // Cross-workdir running threads aggregated by the main process. While a
  // non-active workspace's turn events are filtered out of renderer state, the
  // host still tracks which sessions are turning; this set drives accurate
  // sidebar spinners for every workspace. It only ever marks threads running —
  // completion is delivered by the same aggregate broadcast, so a thread never
  // sticks as running.
  const [crossWorkdirRunningThreadIDs, setCrossWorkdirRunningThreadIDs] =
    useState<ReadonlySet<string>>(() => new Set());
  useEffect(() => {
    // Defensive optional calls: renderer tests stub window.wuu with partial
    // mocks that predate this API; the real preload always provides both.
    let disposed = false;
    const applyRunningSnapshot = (snapshot: RunningThreadSnapshot[]): void => {
      if (disposed) return;
      const runningIDs = new Set(snapshot.map((item) => item.thread_id));
      setCrossWorkdirRunningThreadIDs(
        runningIDs,
      );
      const current = appStateRef.current;
      const hasStaleRunningTurn = [
        current.thread,
        current.secondaryThread,
        ...current.threads,
      ].some(
        (thread) =>
          thread && isThreadRunning(thread) && !runningIDs.has(thread.id),
      );
      if (!hasStaleRunningTurn) {
        return;
      }
      // The main process is authoritative for execution ownership. If its
      // snapshot says a loaded thread is idle while the renderer still has an
      // in-progress turn, repair immediately instead of waiting for the
      // throttled cross-process discovery refresh.
      void loadThreadListRefresh(current).then((listed) => {
        if (disposed) return;
        setState((state) => state.activeContext === current.activeContext
          ? reconcileListedThreadState(state, listed)
          : state);
      }).catch(() => {
        // A later running snapshot or ordinary refresh retries.
      });
    };
    void window.wuu.getRunningThreadsSnapshot?.().then(applyRunningSnapshot);
    const unsubscribe = window.wuu.onRunningThreadsChanged?.((snapshot) => {
      applyRunningSnapshot(snapshot);
    });
    return () => {
      disposed = true;
      unsubscribe?.();
    };
  }, []);
  // Archive is now a single-click action (the previous two-step "click again
  // to confirm" pattern was too easy to misfire). Success and failure feedback
  // lives in `archiveTip` above; the underlying IPC still goes through
  // `window.wuu.archiveThread(id, true)`.
  const dismissArchiveTip = useCallback(() => {
    setArchiveTip(null);
  }, []);
  const [modelCatalogTip, setModelCatalogTip] = useState<{
    message: string;
    isError: boolean;
  } | null>(null);
  const dismissModelCatalogTip = useCallback(() => {
    setModelCatalogTip(null);
  }, []);
  // Agent engine inventory is session-scoped and shared by the composer and
  // settings. Settings must never throw away a usable snapshot just because
  // its page remounted; refreshes replace the snapshot only after they finish.
  // A six-hour freshness window avoids repeatedly starting the Codex
  // app-server while still allowing a long-idle app to discover CLI changes.
  const [engineInventory, setEngineInventory] = useState<EngineListResult | undefined>();
  const [engineInventoryError, setEngineInventoryError] = useState("");
  const engineInventoryRef = useRef<EngineListResult | undefined>(undefined);
  const engineInventoryFetchedAtRef = useRef(0);
  const engineInventoryRefreshRef = useRef<Promise<EngineListResult | undefined> | null>(null);
  const engineInventoryRequestRef = useRef(0);
  const storeEngineInventory = useCallback((next: EngineListResult, request: number) => {
    if (request !== engineInventoryRequestRef.current) return;
    engineInventoryRef.current = next;
    engineInventoryFetchedAtRef.current = Date.now();
    setEngineInventory(next);
    setEngineInventoryError("");
  }, []);

  const refreshEngineInventory = useCallback((force = false): Promise<EngineListResult | undefined> => {
    const cached = engineInventoryRef.current;
    const fresh = cached !== undefined
      && Date.now() - engineInventoryFetchedAtRef.current < ENGINE_INVENTORY_STALE_MS;
    if (!force && fresh) return Promise.resolve(cached);
    if (engineInventoryRefreshRef.current) return engineInventoryRefreshRef.current;
    // Focused renderer tests and older preload bridges can expose only a
    // partial desktop API. Preserve the previous best-effort degradation
    // rather than making engine discovery block the rest of the shell.
    if (typeof window.wuu.listEngines !== "function") return Promise.resolve(cached);

    const request = engineInventoryRequestRef.current + 1;
    engineInventoryRequestRef.current = request;
    const pending = window.wuu.listEngines()
      .then((next) => {
        storeEngineInventory(next, request);
        return next;
      })
      .catch((error: unknown) => {
        if (request === engineInventoryRequestRef.current) {
          setEngineInventoryError(error instanceof Error ? error.message : String(error));
        }
        return engineInventoryRef.current;
      })
      .finally(() => {
        if (engineInventoryRefreshRef.current === pending) {
          engineInventoryRefreshRef.current = null;
        }
      });
    engineInventoryRefreshRef.current = pending;
    return pending;
  }, [storeEngineInventory]);

  const updateEngineInventory = useCallback(async (params: EngineUpdateParams) => {
    const request = engineInventoryRequestRef.current + 1;
    engineInventoryRequestRef.current = request;
    const next = await window.wuu.updateEngines(params);
    storeEngineInventory(next, request);
    return next;
  }, [storeEngineInventory]);

  useEffect(() => {
    void refreshEngineInventory();
  }, [refreshEngineInventory]);
  useEffect(() => {
    const refreshAfterLongIdle = () => {
      void refreshEngineInventory();
    };
    window.addEventListener("focus", refreshAfterLongIdle);
    return () => window.removeEventListener("focus", refreshAfterLongIdle);
  }, [refreshEngineInventory]);
  // When the user clicks "分叉" on a non-latest user message, the fork
  // picker dialog asks whether to stay local or fork into a new worktree.
  // Holding the source thread snapshot in state lets the dialog callback
  // resolve the same data the user clicked, regardless of subsequent
  // thread updates.
  const [pendingFork, setPendingFork] =
    useState<PendingForkState | undefined>(undefined);
  const {
    pendingViewSwitch,
    visiblePendingThreadID,
    visiblePendingWorkspaceID,
    viewSwitchPending,
    submissionTargetPending,
    viewContextSwitchPending,
    beginViewSwitch,
    beginInstantThreadSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    isCurrentViewSwitchRequest,
  } = useViewSwitchState();
  const queryHistoryRailRef = useRef<HTMLDivElement | null>(null);
  const [queryHistoryOpen, setQueryHistoryOpen] = useState(false);
  const queryHistoryCloseTimerRef = useRef<number | undefined>(undefined);
  const windowResizingRef = useRef(false);
  const environmentPanelHasRoomRef = useRef(environmentPanelHasRoom);
  const pendingEnvironmentPanelHasRoomRef = useRef<boolean | undefined>(
    undefined,
  );
  const gitRefreshTimerRef = useRef<number | undefined>(undefined);
  const gitRefreshInFlightRef = useRef(false);
  const gitRefreshQueuedRef = useRef(false);
  const workspaceMenuRef = useRef<HTMLDivElement>(null);
  const runtimeMenuRef = useRef<HTMLDivElement>(null);
  const accessMenuRef = useRef<HTMLDivElement>(null);
  const codexRuntimeRef = useRef<HTMLDivElement>(null);
  const environmentToggleRef = useRef<HTMLButtonElement>(null);
  const environmentPanelRef = useRef<HTMLDivElement>(null);
  const appStateRef = useRef<AppState>(initialState);
  const runningThreadReconcileInFlightRef = useRef("");
  const workspaceHasDirtyFilesRef = useRef(false);
  const lastFocusOutsideWorkspaceRef = useRef<HTMLElement | null>(null);
  const previousWorkspaceFocusModeRef = useRef({
    fullPanel: false,
    open: false,
  });
  const {
    pendingComposerMessagesByThread,
    pendingComposerMessagesByThreadRef,
    pendingComposerMessagesForThread: pendingComposerMessagesForActiveThread,
    updateThreadPendingComposerMessages,
    clearThreadPendingComposerMessages,
    removePendingComposerMessageByID,
    syncPendingComposerMessagesFromServerEvent,
    reconcilePendingComposerMessagesForState,
    seedHeldComposerMessages,
    enqueueComposerMessage,
    removeQueuedMessage,
    removeGuideMessage,
    editQueuedMessage,
    editGuideMessage,
    guideQueuedMessage,
    threadHasPendingComposerMessages,
  } = useComposerPendingState({
    getAppState: () => appStateRef.current,
    getPrimaryComposerDraft: currentPrimaryComposerDraft,
    restoreComposerDraftForThread: (threadID, draft) => {
      if ((activeThreadIDForState(appStateRef.current) === threadID || appStateRef.current.activeSessionTabID === threadID)) {
        restorePrimaryComposerDraft(draft);
        return;
      }
      setState((current) => ({
        ...current,
        sessionTabs: current.sessionTabs.map((tab) =>
          tab.kind === "thread" && tab.threadID === threadID
            ? {
                ...tab,
                prompt: draft.prompt,
                images: draft.images.map((image) => ({ ...image })),
                files: draft.files.map((file) => ({ ...file })),
              }
            : tab,
        ),
      }));
    },
    setStatus: (status) =>
      setState((current) => ({
        ...current,
        status,
      })),
    sendComposerMessageToThread,
  });
  const runtimeVariantByModelRef = useRef(new Map<string, string>());
  const cachedThreadPaneHistoryRef = useRef<string[]>([]);
  const cachedConversationPaneThreadsRef = useRef(new Map<string, Thread>());
  const draftSessionTabCounterRef = useRef(0);
  const currentSessionTab = activeSessionTab(state);

  const currentSkillsTabID =
    currentSessionTab?.kind === "skills" ? currentSessionTab.id : undefined;

  useEffect(() => {
    const handleBeforeUnload = (event: BeforeUnloadEvent): void => {
      if (!workspaceHasDirtyFilesRef.current) {
        return;
      }
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", handleBeforeUnload);
    return () => {
      window.removeEventListener("beforeunload", handleBeforeUnload);
    };
  }, []);

  useEffect(() => {
    const handleFocusIn = (event: FocusEvent): void => {
      const target = event.target;
      const workspacePanel = appShellRef.current?.querySelector(".workspace-right-panel");
      if (target instanceof HTMLElement && !workspacePanel?.contains(target)) {
        lastFocusOutsideWorkspaceRef.current = target;
      }
    };
    document.addEventListener("focusin", handleFocusIn);
    return () => document.removeEventListener("focusin", handleFocusIn);
  }, []);

  useLayoutEffect(() => {
    const fullPanel = rightPanelOpen && rightPanelGlobalized;
    const previous = previousWorkspaceFocusModeRef.current;
    previousWorkspaceFocusModeRef.current = { fullPanel, open: rightPanelOpen };

    appShellRef.current
      ?.querySelector<HTMLElement>(".sidebar")
      ?.toggleAttribute(
        "inert",
        fullPanel && sidebarDrawerMode && !sidebarDrawerVisible,
      );

    if (fullPanel && !previous.fullPanel) {
      appShellRef.current
        ?.querySelector<HTMLButtonElement>(
          '.workspace-right-panel [role="tab"][aria-selected="true"]',
        )
        ?.focus();
      return;
    }
    if ((previous.fullPanel && !fullPanel) || (previous.open && !rightPanelOpen)) {
      const previousFocus = lastFocusOutsideWorkspaceRef.current;
      if (previousFocus?.isConnected && !previousFocus.closest("[inert]")) {
        previousFocus.focus();
        return;
      }
      appShellRef.current
        ?.querySelector<HTMLHeadingElement>(
          ".conversation-title-heading h1",
        )
        ?.focus();
    }
  }, [rightPanelGlobalized, rightPanelOpen, sidebarDrawerMode, sidebarDrawerVisible]);

  // Workspace panel (file tree / file preview / terminal / review) root: follows the
  // active thread's own cwd when it differs from state.activeContext — the
  // main remaining case is a worktree-fork thread, whose cwd is a git
  // worktree directory distinct from the project root activeContext stays
  // pinned to.
  const conversationWorkspaceContext = useMemo(
    () => workspacePanelContext(state.activeContext, state.thread),
    [state.activeContext, state.thread],
  );
  const workspaceContext = focusedWorkspaceContext ?? conversationWorkspaceContext;
  const activeWorkspaceViewTab = workspaceActiveViewTabID
    ? workspaceViewTabs.find((tab) => tab.id === workspaceActiveViewTabID)
    : undefined;
  const workspaceSelectionEnabled =
    rightPanelGlobalized &&
    (activeWorkspaceViewTab?.kind === "files" ||
      activeWorkspaceViewTab?.kind === "file");
  const activeWorkspaceFileTab = workspaceActiveFileTabID
    ? workspaceViewTabs.find((tab) => tab.id === workspaceActiveFileTabID)
    : undefined;
  const activeWorkspaceFileTabID =
    activeWorkspaceFileTab?.kind === "file" &&
    sameRuntimeContext(activeWorkspaceFileTab.context, workspaceContext)
      ? activeWorkspaceFileTab.id
      : undefined;
  const activeWorkspaceFile =
    activeWorkspaceFileTab?.kind === "file" && activeWorkspaceFileTabID
      ? activeWorkspaceFileTab.path
      : undefined;
  const activeThread = activeThreadForState(state);
  const activeThreadID = activeThread?.id;
  const activeThreadRunning = isThreadRunning(activeThread);
  const activeThreadHasRunningTurn = activeThread?.turns.some(turn => turn.status === "in_progress") ?? false;
  useEffect(() => {
    const contextCwd = state.activeContext?.cwd;
    const executorRunning = activeThreadID !== undefined && crossWorkdirRunningThreadIDs.has(activeThreadID);
    if (
      !activeThreadID ||
      !contextCwd ||
      (!activeThreadRunning && !executorRunning) ||
      (activeThreadHasRunningTurn && executorRunning)
    ) {
      return undefined;
    }

    // The aggregate main-process snapshot is independent of renderer server
    // events. Repair both a missed start and a missed completion. An owner
    // resume includes the new input and live items; a workspace list alone may
    // only know that another process holds the execution lease.
    const key = `${contextCwd}\u0000${activeThreadID}`;
    let disposed = false;
    const timer = window.setTimeout(() => {
      if (runningThreadReconcileInFlightRef.current === key) {
        return;
      }
      runningThreadReconcileInFlightRef.current = key;
      void window.wuu.resumeThread(activeThreadID)
        .then(({ thread }) => {
          if (disposed) {
            return;
          }
          setState((current) => {
            if (
              current.activeContext?.cwd !== contextCwd ||
              activeThreadIDForState(current) !== activeThreadID
            ) {
              return current;
            }
            if (!thread) return current;
            return reconcileListedThreadState(current, upsertThread(current.threads, thread));
          });
        })
        .catch(() => {
          // The focus/visibility heartbeat below retries transient failures.
        })
        .finally(() => {
          if (runningThreadReconcileInFlightRef.current === key) {
            runningThreadReconcileInFlightRef.current = "";
          }
        });
    }, 200);
    return () => {
      disposed = true;
      window.clearTimeout(timer);
    };
  }, [
    activeThreadID,
    activeThreadRunning,
    activeThreadHasRunningTurn,
    crossWorkdirRunningThreadIDs,
    state.activeContext?.cwd,
  ]);
  // Draft engine for the next brand-new conversation. Empty means "follow the
  // settings default". Unlike the fallback chain, an explicit "wuu" selection
  // is stored as-is so it overrides an external default engine. Engine
  // binding is a per-thread creation decision, so the draft resets whenever
  // the active thread changes, then re-seeds from the last composer pick so
  // working in an external agent does not mean re-selecting it for every new
  // session (see DraftEngineMemory).
  const [draftEngine, setDraftEngine] = useState<string>("");
  const [draftEngineRuntime, setDraftEngineRuntime] = useState<EngineRuntimeSelection>({
    model: "",
    effort: "",
  });
  // Switching the parent engine in the picker must not discard that engine's
  // child model/effort choice when the user switches back within the draft.
  const draftEngineRuntimeByID = useRef<Record<string, EngineRuntimeSelection>>({});
  const [draftPermissionMode, setDraftPermissionMode] = useState<PermissionMode | "">("");
  const draftEngineSeed = useRef<{ threadID?: string; done: boolean }>({
    threadID: activeThreadID,
    done: false,
  });
  useEffect(() => {
    if (draftEngineSeed.current.threadID !== activeThreadID) {
      draftEngineSeed.current = { threadID: activeThreadID, done: false };
      setDraftEngine("");
      setDraftEngineRuntime({ model: "", effort: "" });
      setDraftPermissionMode("");
    }
    // Only a brand-new conversation seeds from memory. An open thread is
    // already bound to its engine, and a seeded draft would otherwise win the
    // fallback chain for a thread that carries no explicit engine_id.
    if (activeThreadID || draftEngineSeed.current.done) return;
    // The inventory arrives asynchronously, so a remembered external engine
    // can only be validated once it lands; until then the seed stays pending
    // and retries. An explicit pick in the meantime wins and ends the retry.
    const remembered = resolveDraftEngineMemory(engineInventory);
    if (!remembered) {
      draftEngineSeed.current.done = engineInventory !== undefined;
      return;
    }
    draftEngineSeed.current.done = true;
    setDraftEngine(remembered.engine);
    setDraftEngineRuntime({ model: remembered.model, effort: remembered.effort });
    draftEngineRuntimeByID.current[remembered.engine] = {
      model: remembered.model,
      effort: remembered.effort,
    };
    setDraftPermissionMode(remembered.engine === "wuu" ? "" : "unconfined");
  }, [activeThreadID, engineInventory]);
  const selectDraftEngine = useCallback((id: string) => {
    const runtime = draftEngineRuntimeByID.current[id]
      ?? (id === "wuu"
        ? { model: "", effort: "" }
        : rememberedEngineRuntime(id, engineInventory)
          ?? defaultEngineRuntimeSelection(
              engineInventory?.engines.find((engine) => engine.id === id),
            ));
    draftEngineSeed.current.done = true;
    setDraftEngine(id);
    setDraftPermissionMode(id === "wuu" ? "" : "unconfined");
    setDraftEngineRuntime(runtime);
    draftEngineRuntimeByID.current[id] = runtime;
    writeDraftEngineMemory({ engine: id, ...runtime });
  }, [engineInventory]);
  useEffect(() => {
    let current = true;
    if (!activeThreadID || !userQuestionApiAvailable) {
      setUserQuestions([]);
      return () => { current = false; };
    }
    void window.wuu.listUserQuestions(activeThreadID).then((result) => {
      if (current) {
        setUserQuestions(result.questions.filter(
          (request) => !resolvedUserQuestionIDsRef.current.has(request.request_id),
        ));
      }
    }).catch(() => {
      if (current) setUserQuestions([]);
    });
    return () => { current = false; };
  }, [activeThreadID, userQuestionApiAvailable]);
  useEffect(() => {
    desktopPluginHost.setActiveConversationThread(activeThreadID);
    return () => desktopPluginHost.setActiveConversationThread(undefined);
  }, [activeThreadID]);
  const activeTabKind = activeSessionTab(state)?.kind;
  const environmentContext = workspacePanelContext(state.activeContext, activeThread);
  const sideThread = useSideThreadController({
    activeThreadId: activeThreadID,
    activeContext: state.activeContext,
  });
  const sideThreadPanelRef = useRef<SideThreadPanelHandle>(null);
  const activeTurn = activeTurnForThread(activeThread);
  useEffect(() => {
    const syncVisibleCUAThread = () => {
      (window.wuu as typeof window.wuu & { setActiveCUAThread?: (threadID?: string) => void })
        .setActiveCUAThread?.(activeThreadID);
    };
    syncVisibleCUAThread();
    window.addEventListener("focus", syncVisibleCUAThread);
    return () => window.removeEventListener("focus", syncVisibleCUAThread);
  }, [activeThreadID]);
  const activeBrowserActivity = useMemo(
    () =>
      ENABLE_EMBEDDED_BROWSER
        ? activitiesForThread(activitySessions, state.activeContext?.cwd, activeThreadID)
            .filter((activity) => activity.kind === "browser" && activity.state !== "stopped")
            .at(-1)
        : undefined,
    [activitySessions, activeThreadID, state.activeContext?.cwd],
  );

  useEffect(() => {
    const workdir = state.activeContext?.cwd;
    if (!workdir || !activeThreadID || typeof window.wuu.listActivities !== "function") {
      return undefined;
    }
    let cancelled = false;
    void window.wuu.listActivities(activeThreadID).then((result) => {
      if (!cancelled) {
        setActivitySessions((current) =>
          mergeActivityList(current, workdir, activeThreadID, result.activities ?? []),
        );
      }
    }).catch(() => {
      // Live notifications still populate the panel when the initial list
      // races app-server startup; ordinary app status handles transport errors.
    });
    return () => {
      cancelled = true;
    };
  }, [activeThreadID, state.activeContext?.cwd]);

  function mergeActivityResponse(activity: ActivitySession): void {
    setActivitySessions((current) =>
      mergeActivityList(current, activity.workdir, activity.thread_id, [activity]),
    );
  }

  async function pauseBrowserTask(): Promise<void> {
    if (!activeBrowserActivity) {
      return;
    }
    try {
      const result = await window.wuu.takeoverActivity(
        activeBrowserActivity.thread_id,
        activeBrowserActivity.id,
      );
      mergeActivityResponse(result.activity);
    } catch (error) {
      showErrorToast(error, t("app.browserPauseFailed"));
    }
  }

  const sessionRuntime = useMemo(
    () => runtimeViewForSession(state.initialized, activeThread),
    [state.initialized, activeThread],
  );
  const mascotProviderNames = useMemo(
    () => state.initialized?.providers?.map((provider) => provider.name),
    [state.initialized?.providers],
  );
  const [mascotRuntimePreview, setMascotRuntimePreview] = useState<{
    provider: string;
    model: string;
  } | null>(null);
  const mascotRuntimePreviewRequestRef = useRef(0);
  const visibleConversationRuntime = useMemo(
    () => runtimeViewForConversation(state.initialized, activeThread, activeTurn),
    [state.initialized, activeThread, activeTurn],
  );
  const currentConversationThreadsByID = useMemo(
    () =>
      conversationPaneThreadsByID(
        state.threads,
        state.thread,
        state.secondaryThread,
    ),
    [state.threads, state.thread, state.secondaryThread],
  );
  // A runtime switch replaces state.threads with the target workspace. Keep
  // snapshots only for panes admitted by the bounded cache below so an open
  // source-workspace pane stays mounted without retaining every known thread.
  const availableConversationThreadsByID = useMemo(() => {
    const available = new Map(cachedConversationPaneThreadsRef.current);
    for (const [threadID, thread] of currentConversationThreadsByID) {
      available.set(threadID, thread);
    }
    return available;
  }, [currentConversationThreadsByID]);
  const availableConversationThreadsByIDRef = useRef(
    availableConversationThreadsByID,
  );
  availableConversationThreadsByIDRef.current = availableConversationThreadsByID;
  // Per-thread keep-alive for the main conversation pane. Recently visited
  // conversations stay mounted within both a pane-count cap and an estimated
  // rendered-turn budget. This avoids the old three-tab performance cliff
  // without allowing many long conversations to retain unbounded DOM.
  //
  // Crucially we derive the cache synchronously from the active thread
  // via useMemo, not via useState + useEffect. The async effect path
  // rendered once with the new activeThreadID but the stale cache (no
  // pane for the new thread) and then a second time with the cache
  // updated — the "two flickers" the user saw. Computing the cache from
  // state in the same render closes that empty frame.
  const cachedThreadPaneIDs = useMemo(() => {
    const activeID = state.thread?.id;
    // Sidebar navigation does not keep a tab strip of open conversations, but
    // recently visited panes still stay mounted within the existing count and
    // render-weight caps. Unmounting the outgoing pane on every click rebuilt
    // the markdown tree after paint and flashed the incoming session.
    const openThreadIDs = new Set(cachedThreadPaneHistoryRef.current);
    if (activeID) {
      openThreadIDs.add(activeID);
    }
    const next = selectCachedConversationPaneIDs({
      activeThreadID: activeID,
      previousThreadIDs: cachedThreadPaneHistoryRef.current,
      openThreadIDs,
      threadsByID: availableConversationThreadsByIDRef.current,
    });
    cachedThreadPaneHistoryRef.current = next;
    return next;
  }, [state.thread?.id]);
  const cachedConversationThreadsByID = useMemo(
    () =>
      retainCachedConversationPaneThreads({
        threadIDs: cachedThreadPaneIDs,
        currentThreadsByID: currentConversationThreadsByID,
        previousThreadsByID: cachedConversationPaneThreadsRef.current,
      }),
    [cachedThreadPaneIDs, currentConversationThreadsByID],
  );
  cachedConversationPaneThreadsRef.current = cachedConversationThreadsByID;
  const openTurnFileDiffPanel = useStableCallback(
    (threadID: string, selection: TurnFileDiffSelection) => {
      openWorkspaceDiffTab({ threadID, path: selection.path, selection });
      setRightPanelOpenWithMotion(true);
      closeEnvironmentPanel({ dismissed: true });
    },
  );
  const activePendingComposerMessages = pendingComposerMessagesForActiveThread(
    activeThreadID,
  );
  const queuedMessages = activePendingComposerMessages.queued;
  const guideMessages = activePendingComposerMessages.guides;
  // Self-healing reconciliation for pending composer messages: once a queued
  // or guide send materializes as a real user_message turn item, drop it from
  // the composer queue strip / chat "发送中…" bubble even if the live
  // turn/started (or item/completed) removal notification was missed — e.g. it
  // got gated out of the renderer because the thread was backgrounded when the
  // event arrived (serverEventTargetsActiveContext filter). Keying off the
  // authoritative thread turns means a message that already went out can never
  // stay stuck as "排队中".
  useEffect(() => {
    // Use the state snapshot that produced this render so reconciliation stays
    // tied to the thread that actually materialized the queued message.
    reconcilePendingComposerMessagesForState(state);
  }, [
    pendingComposerMessagesByThread,
    state.thread,
    state.secondaryThread,
    state.threads,
  ]);
  const {
    conversationSearch,
    conversationSearchResults,
    conversationSearchRef,
    conversationSearchInputRef,
    toggleConversationSearch,
    closeConversationSearch,
    selectConversationSearchResult,
    handleConversationSearchKeyDown,
    setConversationSearchQuery,
    clearConversationSearchQuery,
    setConversationSearchSelectedIndex,
  } = useConversationSearch({
    activeContext: state.activeContext,
    getAppState: () => appStateRef.current,
    cacheThreads: cacheSidebarThreads,
    onOpen: () => {
      setWorkspaceMenuOpen(false);
      setRuntimeMenuOpen(false);
      setAccessMenuOpen(false);
      setBranchMenuOpen(false);
      setCodexRuntimeMenu(null);
      setEnvironmentDialog(null);
      setPendingFork(undefined);
    },
    onSelectThread: (threadID) => void activateThread(threadID),
  });

  // Cmd/Ctrl+P toggles the conversation search overlay. Mirrors the
  // "Quick Open / Go to file" convention from VS Code, Sublime, and
  // JetBrains — semantically "navigate to a thing by name" rather than
  // Cmd+F's "find text in current view". preventDefault stops the
  // browser's Print dialog. Works from anywhere in the app, including
  // while typing in the chat composer.
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent): void {
      if (
        (event.metaKey || event.ctrlKey) &&
        !event.shiftKey &&
        !event.altKey &&
        event.key.toLowerCase() === "p"
      ) {
        event.preventDefault();
        toggleConversationSearch();
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [toggleConversationSearch]);

  function openEnvironmentDialog(dialog: EnvironmentDialog): void {
    closeConversationSearch({ immediate: true });
    setPendingFork(undefined);
    setEnvironmentDialog(dialog);
  }
  // A visible agent browser view is a main-owned WebContentsView that floats
  // above the DOM; any full-window overlay would occlude it, so we tell main to
  // hide the view while one is open. This covers the common full-window
  // surfaces: settings (which replaces the whole app), the commit / PR
  // dialogs, the fork dialog, and the conversation search overlay. TODO: other
  // ad-hoc modals/portals (e.g. participant panels) are not yet
  // enumerated here; extend this predicate as more full-window overlays land.
  const browserOverlaySuppressed =
    settingsOpen || accountOpen ||
    environmentDialog !== null ||
    Boolean(pendingFork) ||
    conversationSearch.open;
  useBrowserVisibility({
    onInvalidateWorkdir: (workdir) =>
      setActivitySessions((current) =>
        clearActivitiesForWorkdir(current, workdir),
      ),
  });
  const [browserDockTarget, setBrowserDockTarget] = useState<BrowserDockTarget>();
  const [pendingBrowserDock, setPendingBrowserDock] = useState<{
    target: BrowserDockTarget;
    ready: boolean;
  }>();
  const openWorkspaceBrowserRef = useRef(openWorkspaceTool);
  openWorkspaceBrowserRef.current = openWorkspaceTool;
  const activeTodoUpdate = latestTodoUpdateForThread(activeThread);
  const activeContextKey = state.activeContext
    ? runtimeContextKey(state.activeContext)
    : "";
  const forkWorktreeDisabledReason =
    state.gitStatus?.is_repo === false
      ? t("app.worktreeRequiresGit")
      : undefined;
  const splitConversation = Boolean(state.thread && state.secondaryThread);

  // Past-query popover control. The rail beside the scrollbar is the hover
  // target; we close on a short delay so the user can travel from the rail
  // into the floating list without it snapping shut.
  function openQueryHistory(): void {
    if (activeThreadReadOnly || pastQueries.length === 0) {
      return;
    }
    cancelQueryHistoryClose();
    setQueryHistoryOpen(true);
  }

  function scheduleQueryHistoryClose(): void {
    cancelQueryHistoryClose();
    queryHistoryCloseTimerRef.current = window.setTimeout(() => {
      queryHistoryCloseTimerRef.current = undefined;
      setQueryHistoryOpen(false);
    }, 200);
  }

  function cancelQueryHistoryClose(): void {
    if (queryHistoryCloseTimerRef.current !== undefined) {
      window.clearTimeout(queryHistoryCloseTimerRef.current);
      queryHistoryCloseTimerRef.current = undefined;
    }
  }

  function handleQueryHistorySelect(entry: QueryHistoryEntry): void {
    cancelQueryHistoryClose();
    setQueryHistoryOpen(false);
    // Stop auto-follow before we jump — otherwise the next stream tick
    // would drag the scroll position back to the bottom and undo the
    // jump before the user even registers it happened.
    disableConversationAutoFollow();
    scrollToUserMessage(entry.turnID, entry.itemID);
  }

  useEffect(() => {
    return () => {
      cancelQueryHistoryClose();
    };
  }, []);

  useEffect(() => {
    const root = document.documentElement;
    let resizeEndTimer: number | undefined;
    let resizing = false;
    let lastResizeWidth = window.innerWidth;

    function setResizeState(nextResizing: boolean): void {
      if (resizing === nextResizing) {
        return;
      }
      resizing = nextResizing;
      windowResizingRef.current = nextResizing;
      if (nextResizing) {
        root.classList.add(WINDOW_RESIZING_CLASS);
      } else {
        // Apply frozen composer/scroll/footer geometry while transitions are
        // still off. Waiting until after the class drops left a delayed jump
        // once the window chrome was already still.
        releaseWindowResizeClass(WINDOW_RESIZING_CLASS);
      }
      if (
        !nextResizing &&
        pendingEnvironmentPanelHasRoomRef.current !== undefined
      ) {
        const pendingHasRoom = pendingEnvironmentPanelHasRoomRef.current;
        pendingEnvironmentPanelHasRoomRef.current = undefined;
        if (environmentPanelHasRoomRef.current !== pendingHasRoom) {
          environmentPanelHasRoomRef.current = pendingHasRoom;
          setEnvironmentPanelHasRoom(pendingHasRoom);
        }
      }
    }

    function scheduleResizeEnd(delay = 140): void {
      if (resizeEndTimer !== undefined) {
        window.clearTimeout(resizeEndTimer);
      }
      resizeEndTimer = window.setTimeout(() => {
        resizeEndTimer = undefined;
        setResizeState(false);
      }, delay);
    }

    function handleWindowResize(): void {
      const nextWidth = window.innerWidth;
      // Keyboard/browser chrome height-only changes must not enter the desktop
      // window-resize suppression: it drops sidebar material and pauses layout
      // animations for the settle window even though the width never changed.
      if (isTouchWebShell() && nextWidth === lastResizeWidth) {
        return;
      }
      lastResizeWidth = nextWidth;
      setResizeState(true);
      scheduleResizeEnd();
    }

    const offWindowResizeState = window.wuu.onWindowResizeState(
      ({ resizing: nextResizing }) => {
        if (nextResizing) {
          setResizeState(true);
          scheduleResizeEnd();
          return;
        }
        scheduleResizeEnd(40);
      },
    );

    window.addEventListener("resize", handleWindowResize);
    return () => {
      offWindowResizeState();
      window.removeEventListener("resize", handleWindowResize);
      if (resizeEndTimer !== undefined) {
        window.clearTimeout(resizeEndTimer);
      }
      windowResizingRef.current = false;
      pendingEnvironmentPanelHasRoomRef.current = undefined;
      setResizeState(false);
    };
  }, []);

  useEffect(() => {
    const query = window.matchMedia(
      "(min-width: 1320px) and (min-height: 680px)",
    );
    const update = (): void => {
      const nextHasRoom = query.matches;
      if (
        windowResizingRef.current ||
        document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)
      ) {
        pendingEnvironmentPanelHasRoomRef.current = nextHasRoom;
        return;
      }
      pendingEnvironmentPanelHasRoomRef.current = undefined;
      environmentPanelHasRoomRef.current = nextHasRoom;
      setEnvironmentPanelHasRoom(nextHasRoom);
    };
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);

  useLayoutEffect(() => {
    appStateRef.current = state;
  }, [state]);

  useEffect(() => {
    const markUserInteraction = (): void => {
      userInteractionVersionRef.current += 1;
    };
    document.addEventListener("pointerdown", markUserInteraction, true);
    document.addEventListener("keydown", markUserInteraction, true);
    return () => {
      document.removeEventListener("pointerdown", markUserInteraction, true);
      document.removeEventListener("keydown", markUserInteraction, true);
    };
  }, []);

  const pendingBackgroundThreadEventsRef = useRef<ServerEvent[]>([]);
  const backgroundThreadEventTimerRef = useRef<number | undefined>(undefined);
  const flushBackgroundThreadEventsRef = useRef<() => void>(() => {});
  flushBackgroundThreadEventsRef.current = () => {
    if (backgroundThreadEventTimerRef.current !== undefined) {
      window.clearTimeout(backgroundThreadEventTimerRef.current);
      backgroundThreadEventTimerRef.current = undefined;
    }
    const pending = pendingBackgroundThreadEventsRef.current;
    if (pending.length === 0) {
      return;
    }
    pendingBackgroundThreadEventsRef.current = [];
    setState((current) => pending.reduce(reduceServerEvent, current));
  };
  useLayoutEffect(() => {
    // Opening a session has to see the tool rows that were still queued.
    flushBackgroundThreadEventsRef.current();
  }, [activeThreadID, state.secondaryThread?.id]);

  useEffect(() => {
    let mounted = true;
    const off = subscribeServerEvents((event) => {
      if (!mounted) {
        return;
      }
      // Token speed and live context telemetry are high-rate supporting UI.
      // Keep them outside App state so provider events cannot re-render the
      // full desktop tree merely to advance the composer meters.
      turnTelemetryStore.ingest(event);
      externalAgentActivityStore.ingest(event);
      if (serverEventCarriesActivitySessionUpdate(event)) {
        setActivitySessions((current) => reduceActivitySessionEvent(current, event));
      }
      if (event.kind === "notification" && event.message.method === "user-question/requested") {
        const request = (event.message.params as { request?: UserQuestionRequest } | undefined)?.request;
        if (request) {
          resolvedUserQuestionIDsRef.current.delete(request.request_id);
          setUserQuestions((current) => [
            ...current.filter((item) => item.request_id !== request.request_id),
            request,
          ]);
          if (typeof window.wuu.showSystemNotification === "function") {
            void window.wuu.showSystemNotification({
              title: translateCurrent("notification.questionTitle"),
              body: translateCurrent("notification.questionBody"),
            });
          }
        }
      }
      if (event.kind === "notification" && event.message.method === "user-question/resolved") {
        const requestID = (event.message.params as { request_id?: string } | undefined)?.request_id;
        if (requestID) {
          if (resolvedUserQuestionIDsRef.current.size >= 256) {
            resolvedUserQuestionIDsRef.current.clear();
          }
          resolvedUserQuestionIDsRef.current.add(requestID);
          setUserQuestions((current) => current.filter((item) => item.request_id !== requestID));
        }
      }
      if (event.kind === "notification" && event.message.method === "turn/completed") {
        const params = event.message.params as { thread_id?: string } | undefined;
        const threadID = typeof params?.thread_id === "string" ? params.thread_id : undefined;
        const thread = threadID
          ? appStateRef.current.threads.find((item) => item.id === threadID)
          : undefined;
        // Main-thread turns only: child/subagent completions stay quiet, and
        // ephemeral threads never reach user-facing history. The main process
        // still suppresses the notification when the window has focus.
        if (
          thread &&
          !thread.parent_id &&
          !thread.ephemeral &&
          typeof window.wuu.showSystemNotification === "function"
        ) {
          void window.wuu.showSystemNotification({
            title: translateCurrent("notification.turnCompletedTitle"),
            body: translateCurrent("notification.turnCompletedBody"),
          });
        }
      }
      // All app-server clients share this event channel. Keep folded workspace
      // snapshots live so expanding a workspace only reveals state; it never
      // needs to wait for a status refresh first.
      syncSidebarServerEventStable(event);
      // Queue state belongs to the composer message, not to whichever workdir
      // is currently visible. Process these low-rate lifecycle events before
      // active-context filtering so background turns cannot leave a phantom
      // queued message behind.
      syncPendingComposerMessagesFromServerEvent(event);
      if (!serverEventTargetsActiveContext(event, appStateRef.current)) {
        return;
      }
      const handling = handleStreamingNotification(event, appStateRef.current);
      if (handling === "stream" || handling === "stream-state") {
        // The first visible delta still needs to mount and reveal the live
        // surface. Subsequent text commits call onStreamFrame after the
        // throttled StreamTextStore publication; scrolling here as well would
        // schedule two layout passes for every provider delta, including
        // passes before the DOM contains the new text.
        if (handling === "stream-state") {
          scheduleStreamScroll();
        }
      }
      if (handling === "stream") {
        return;
      }
      if (handling === "background-stream") {
        return;
      }
      if (handling === "skip") {
        return;
      }
      // A background ACP session emits a tool row for every harness call.
      // Fold those into one update so the open conversation is not reconciled
      // on each of them. The sort key of a running session does not change.
      if (isCoalescedBackgroundThreadEvent(event, appStateRef.current)) {
        pendingBackgroundThreadEventsRef.current.push(event);
        if (backgroundThreadEventTimerRef.current === undefined) {
          backgroundThreadEventTimerRef.current = window.setTimeout(() => {
            backgroundThreadEventTimerRef.current = undefined;
            flushBackgroundThreadEventsRef.current();
          }, 150);
        }
        return;
      }
      flushBackgroundThreadEventsRef.current();
      if (serverEventShouldRefreshGit(event)) {
        scheduleGitStatusRefresh(600);
      }
      setState((current) => reduceServerEvent(current, event));
    });

    void (async () => {
      try {
        if (popOutInit?.kind && popOutInit.context) {
          const loadedState = await loadPopOutRuntime(popOutInit);
          if (!mounted) {
            return;
          }
          const { heldComposerMessages, ...runtimeAppState } = loadedState;
          setState((current) => ({ ...current, ...runtimeAppState }));
          if (loadedState.thread) {
            seedHeldComposerMessages(loadedState.thread.id, heldComposerMessages ?? []);
          }
          return;
        }
        const listedWorkspaces = await window.wuu.listProjects();
        const runtimeState = listedWorkspaces.active_context
          ? listedWorkspaces
          : await window.wuu.selectNoProject(false);
        const loadedState = await loadRuntime(runtimeState);
        if (!mounted) {
          return;
        }
        const { heldComposerMessages, ...runtimeAppState } = loadedState;
        setState((current) =>
          withLoadedRuntimeSessionTab(current, runtimeAppState),
        );
        if (loadedState.thread) {
          seedHeldComposerMessages(loadedState.thread.id, heldComposerMessages ?? []);
        }
      } catch (error) {
        if (!mounted) {
          return;
        }
        setState((current) => ({
          ...current,
          status: error instanceof Error ? error.message : t("runtime.startFailed"),
        }));
      }
    })();

    return () => {
      mounted = false;
      off();
      if (backgroundThreadEventTimerRef.current !== undefined) {
        window.clearTimeout(backgroundThreadEventTimerRef.current);
        backgroundThreadEventTimerRef.current = undefined;
      }
      pendingBackgroundThreadEventsRef.current = [];
      if (gitRefreshTimerRef.current !== undefined) {
        window.clearTimeout(gitRefreshTimerRef.current);
        gitRefreshTimerRef.current = undefined;
      }
    };
  }, [popOutInit]);

  useEffect(() => {
    let mounted = true;
    const unsubscribe = window.wuu.onRuntimeRestore?.(async () => {
      const snapshot = await loadRuntimeRestore(appStateRef.current);
      const questions = await window.wuu.listUserQuestions();
      if (!mounted) return;
      setState((current) => applyRuntimeRestore(current, snapshot));
      for (const resumed of snapshot.resumed) {
        syncPendingComposerMessagesFromServerEvent({
          workdir: resumed.thread.cwd,
          kind: "notification",
          message: { method: "thread/resumed", params: resumed },
        });
      }
      setUserQuestions(questions.questions.filter(
        (request) => !resolvedUserQuestionIDsRef.current.has(request.request_id),
      ));
    });
    return () => { mounted = false; unsubscribe?.(); };
  }, []);

  // Cross-process session pickup: sessions started from a paired phone are
  // written to the shared store by the remote host process, which emits no
  // events this renderer's app-server can forward. Re-list on window focus /
  // visibility regain plus a slow heartbeat so the sidebar converges to the
  // shared store when the user returns to the desktop. Failures stay silent:
  // the next trigger retries.
  useEffect(() => {
    const REFRESH_MIN_INTERVAL_MS = 10_000;
    const HEARTBEAT_MS = 60_000;
    let lastRefreshAt = 0;
    let disposed = false;

    const refreshThreads = async (): Promise<void> => {
      const now = Date.now();
      if (now - lastRefreshAt < REFRESH_MIN_INTERVAL_MS) {
        return;
      }
      lastRefreshAt = now;
      try {
        const snapshot = appStateRef.current;
        const listed = await loadThreadListRefresh(snapshot);
        if (disposed) {
          return;
        }
        setState((current) => {
          if (!current.initialized || current.activeContext !== snapshot.activeContext) {
            return current;
          }
          // Besides sidebar pickup, this is the durable repair path when a
          // renderer misses turn/completed during a tab/workspace transition.
          // Reconcile the visible panes and composer running flag as one state
          // update instead of refreshing only the sidebar copy.
          return reconcileListedThreadState(current, listed);
        });
      } catch {
        // Transient listing failure; do not surface into app status.
      }
    };

    const onVisibility = (): void => {
      if (document.visibilityState === "visible") {
        void refreshThreads();
      }
    };
    const onFocus = (): void => {
      void refreshThreads();
    };
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("focus", onFocus);
    const heartbeat = window.setInterval(() => {
      if (document.visibilityState === "visible") {
        void refreshThreads();
      }
    }, HEARTBEAT_MS);

    return () => {
      disposed = true;
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("focus", onFocus);
      window.clearInterval(heartbeat);
    };
  }, []);

  useEffect(() => {
    function handlePointerDown(event: PointerEvent): void {
      const target = event.target;
      if (!(target instanceof Node)) {
        return;
      }
      if (workspaceMenuOpen && !workspaceMenuRef.current?.contains(target)) {
        setWorkspaceMenuOpen(false);
      }
      if (
        (runtimeMenuOpen || branchMenuOpen) &&
        !runtimeMenuRef.current?.contains(target) &&
        !isInsideFloatingMenu(target, "composer-runtime")
      ) {
        setRuntimeMenuOpen(false);
        setBranchMenuOpen(false);
      }
      if (
        accessMenuOpen &&
        !accessMenuRef.current?.contains(target) &&
        !isInsideFloatingMenu(target, "composer-access")
      ) {
        setAccessMenuOpen(false);
      }
      if (
        codexRuntimeMenu &&
        !codexRuntimeRef.current?.contains(target) &&
        !isInsideFloatingMenu(target, "codex-runtime")
      ) {
        setCodexRuntimeMenu(null);
      }
      const environmentPanelClickOutside =
        !environmentToggleRef.current?.contains(target) &&
        !isInsideFloatingMenu(target, "conversation-actions") &&
        !environmentPanelRef.current?.contains(target);
      if (environmentPanelClickOutside) {
        if (environmentPanelMenu) {
          setEnvironmentPanelMenu(null);
        }
        if (environmentPanelOpen && !environmentPanelHasRoom) {
          closeEnvironmentPanel();
        }
      }
    }

    window.addEventListener("pointerdown", handlePointerDown);
    return () => window.removeEventListener("pointerdown", handlePointerDown);
  }, [
    accessMenuOpen,
    branchMenuOpen,
    codexRuntimeMenu,
    environmentPanelHasRoom,
    environmentPanelMenu,
    environmentPanelOpen,
    workspaceMenuOpen,
    runtimeMenuOpen,
  ]);

  useEffect(() => {
    scheduleGitStatusRefresh(0);
  }, [
    state.activeContext?.kind,
    environmentContext?.cwd,
    state.activeProjectId,
    activeThreadID,
  ]);

  useEffect(() => {
    function handleFocus(): void {
      scheduleGitStatusRefresh(0);
    }

    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, []);

  const activeWorkspace = useMemo(
    () =>
      state.projects.find((project) => project.id === state.activeProjectId),
    [state.activeProjectId, state.projects],
  );
  const showingSkillsCatalog = Boolean(
    state.initialized &&
    currentSessionTab?.kind === "skills",
  );
  const showingManagementCatalog = showingSkillsCatalog;
  const activeTitle = showingSkillsCatalog
    ? t("skills.title")
    : conversationHeadingTitle(
      activeThread,
      currentSessionTab?.kind === "draft" ? currentSessionTab.title : undefined,
      t("tabs.newConversation"),
    );
  const popOutWindowTitle =
    popOutInit?.kind === "thread"
      ? activeThread?.title?.trim() ||
        resolveLocalizedText(activeThread?.preview?.trim() ?? "") ||
        t("tabs.newConversation")
      : t("tabs.newConversation");
  useEffect(() => {
    if (!poppedOutMode) {
      return;
    }
    document.title = `wuu · ${popOutWindowTitle}`;
  }, [poppedOutMode, popOutWindowTitle]);
  const currentHour = useCurrentHour();
  const greetingContext: GreetingContext =
    state.activeContext?.kind === "project"
      ? {
          kind: "workspace",
          workspaceName: activeWorkspace?.name ?? t("greeting.workspaceFallback"),
        }
      : { kind: "wuu" };
  const emptyThreadTitle = greetingFor(currentHour, greetingContext);
  type TurnAdmission = {
    thread?: Thread;
    ready: Promise<Thread | undefined>;
    resolve: (thread: Thread | undefined) => void;
    stopRequested: boolean;
    sent: boolean;
    cancelled: Promise<undefined>;
    cancelPreparation: () => void;
  };
  const turnAdmissionsRef = useRef(new Map<string, TurnAdmission>());
  const queueLanesRef = useRef(new Map<string, { tail: Promise<void>; stopVersion: number }>());
  const stopDeliveredRef = useRef(new Map<string, symbol>());
  const stopTargetsRef = useRef(new Map<string, string>());
  const stopRequestsRef = useRef<Record<string, "pending" | "retry">>({});
  const [stopRequests, setStopRequests] = useState(stopRequestsRef.current);
  function setStopRequest(threadID: string, phase?: "pending" | "retry"): void {
    const next = { ...stopRequestsRef.current };
    if (phase) next[threadID] = phase;
    else {
      delete next[threadID];
      stopDeliveredRef.current.delete(threadID);
      stopTargetsRef.current.delete(threadID);
    }
    stopRequestsRef.current = next;
    setStopRequests(next);
  }
  useEffect(() => {
    for (const threadID of Object.keys(stopRequestsRef.current)) {
      const thread = threadForTab(state, threadID);
      const target = stopTargetsRef.current.get(threadID);
      const targetEnded = thread?.turns.some((turn) => turn.status !== "in_progress" && (
        turn.id === target || turn.items.some((item) => item.type === "user_message"
          && item.source_id && `${OPTIMISTIC_TURN_ID_PREFIX}${item.source_id}` === target)
      ));
      if (thread && !isThreadRunning(thread) && !turnAdmissionsRef.current.has(threadID)
        && (stopRequestsRef.current[threadID] !== "retry" || targetEnded)) {
        setStopRequest(threadID);
      } else if (thread && stopRequestsRef.current[threadID] === "pending" && thread.turns.some(
        (turn) => turn.status === "in_progress" && !turn.answer_ready_at && !turn.id.startsWith(OPTIMISTIC_TURN_ID_PREFIX),
      )) {
        void interruptAcceptedThread(threadID);
      }
    }
  }, [state, stopRequests]);

  async function requestThreadStop(thread: Thread): Promise<void> {
    const admission = turnAdmissionsRef.current.get(thread.id);
    if (admission) {
      admission.stopRequested = true;
      if (!admission.sent) admission.cancelPreparation();
    }
    const lane = queueLanesRef.current.get(thread.id);
    if (lane) lane.stopVersion += 1;
    if (stopRequestsRef.current[thread.id] === "pending") return;
    const target = thread.turns.at(-1);
    if (target) stopTargetsRef.current.set(thread.id, target.id);
    setStopRequest(thread.id, "pending");
    // Preparation can be cancelled locally. In-flight admission is stopped as
    // soon as its real turn is known; never mark it terminal on user intent.
    if (admission && !thread.turns.some((turn) =>
      turn.status === "in_progress" && !turn.answer_ready_at && !turn.id.startsWith(OPTIMISTIC_TURN_ID_PREFIX))) return;
    await interruptAcceptedThread(thread.id);
  }

  async function interruptAcceptedThread(threadID: string): Promise<void> {
    if (stopDeliveredRef.current.has(threadID)) return;
    const delivery = Symbol();
    stopDeliveredRef.current.set(threadID, delivery);
    try {
      const result = await window.wuu.interruptTurn(threadID);
      if (!result.ok) throw new Error(t("composer.stopUnconfirmed"));
    } catch (error) {
      if (stopDeliveredRef.current.get(threadID) !== delivery) return;
      stopDeliveredRef.current.delete(threadID);
      setStopRequest(threadID, "retry");
      showErrorToast(error);
    }
  }

  type PendingThreadCreation = {
    sessionTabID: string;
    context: RuntimeContext;
    turn: Turn;
    cancel: () => void;
  };
  const pendingThreadCreationsRef = useRef(new Map<string, PendingThreadCreation>());
  const [pendingThreadCreations, setPendingThreadCreations] = useState<PendingThreadCreation[]>([]);
  function clearPendingThreadCreation(turnID: string): void {
    for (const [tabID, pending] of pendingThreadCreationsRef.current) {
      if (pending.turn.id !== turnID) continue;
      pendingThreadCreationsRef.current.delete(tabID);
      setPendingThreadCreations([...pendingThreadCreationsRef.current.values()]);
      return;
    }
  }
  const [failedDraftTabIDs, setFailedDraftTabIDs] = useState<string[]>([]);
  useEffect(() => {
    setFailedDraftTabIDs((ids) => ids.includes(state.activeSessionTabID)
      ? ids.filter((id) => id !== state.activeSessionTabID)
      : ids);
  }, [state.activeSessionTabID]);
  const activePendingThreadCreation = pendingThreadCreations.find(
    (pending) => pending.sessionTabID === state.activeSessionTabID,
  );
  const activePendingNewThreadTurn = activePendingThreadCreation?.turn;
  const turns = activeThread?.turns ?? [];
  const activeContextCompositionEntries = activeThreadID
    ? contextCompositionEntries.filter((entry) => entry.threadID === activeThreadID)
    : [];
  const emptyConversation =
    !showingManagementCatalog &&
    !activePendingNewThreadTurn &&
    turns.length === 0 &&
    activeContextCompositionEntries.length === 0;

  useArtifactAutoPreview({
    thread: activeThread,
    enabled: Boolean(state.initialized)
      && !poppedOutMode && !isTouchWebShell() && !activeThread?.read_only
      && !showingManagementCatalog && !emptyConversation && !browserOverlaySuppressed
      && !workspaceRightPanelAutoGlobalized
      && activeBrowserActivity?.state !== "foreground_controlled"
      && activeBrowserActivity?.state !== "user_controlled",
    panelOpen: rightPanelOpen,
    activeTabID: workspaceActiveViewTabID,
    onOpen: openWorkspaceArtifactTab,
  });

  // Past user queries for the input-box hover popover. We collect them
  // in turn order, oldest first, so the popover mirrors the order in
  // which the user asked them. Empty / handoff / image-only items are
  // skipped — they have nothing to show in a quick-jump list.
  const pastQueries = useMemo<QueryHistoryEntry[]>(() => {
    const entries: QueryHistoryEntry[] = [];
    for (const turn of turns) {
      for (const item of turn.items) {
        const text = queryTextForUserItem(item);
        if (!text) {
          continue;
        }
        entries.push({ turnID: turn.id, itemID: item.id, text });
      }
    }
    return entries;
  }, [turns]);
  const showingPrimaryPluginView = usePrimaryPluginViewCover();
  const mainConversationDockVisible =
    Boolean(state.initialized) &&
    !splitConversation &&
    !showingManagementCatalog &&
    !rightPanelGlobalized &&
    !showingPrimaryPluginView;

  // The account-based phone app keeps visible session navigation alongside swipes.
  const composerNavigation = !phoneNavigation && compactNavigation && isTouchWebShell() &&
    mainConversationDockVisible && !poppedOutMode;

  useEffect(() => {
    // Delivered snapshots may belong to either visible conversation pane.
    // Closing them on navigation also releases viewer resources.
    const isStaleTurnTab = (tab: WorkspaceViewTab): boolean => {
      if (tab.kind === "artifact") {
        return showingManagementCatalog || emptyConversation
          || ![state.thread, state.secondaryThread].some((thread) =>
            thread?.id === tab.threadID && thread.cwd === tab.cwd);
      }
      return tab.kind === "diff" &&
        (!activeThreadID || tab.threadID !== activeThreadID || showingManagementCatalog || emptyConversation);
    };
    if (!workspaceViewTabs.some(isStaleTurnTab)) {
      return;
    }
    const activeTab = workspaceViewTabs.find((tab) => tab.id === workspaceActiveViewTabID);
    const closingActiveTurnTab = Boolean(activeTab && isStaleTurnTab(activeTab));
    closeWorkspaceViewTabsWhere(isStaleTurnTab);
    if (closingActiveTurnTab) {
      setRightPanelOpenWithMotion(false);
    }
  }, [
    activeThreadID,
    closeWorkspaceViewTabsWhere,
    emptyConversation,
    showingManagementCatalog,
    workspaceActiveViewTabID,
    workspaceViewTabs,
    state.thread,
    state.secondaryThread,
  ]);

  const {
    conversationScrollRef,
    scrollContentRef,
    splitPaneRefs,
    conversationPaneRef,
    dockComposerRef,
    dockComposerNode,
    statusClusterRef,
    scheduleStreamScroll,
    handleConversationScroll,
    enableConversationAutoFollow,
    disableConversationAutoFollow,
    captureConversationScrollPosition,
    restoreConversationScrollPosition,
    requestSubmittedQueryScroll,
    acknowledgeSubmittedMessage,
    discardSubmittedMessage,
  } = useConversationScrollState({
    activeThreadID,
    activePane: state.activePane,
    splitConversation,
    primaryTurns: state.thread?.turns,
    secondaryTurns: state.secondaryThread?.turns,
    emptyConversation,
    initialized: Boolean(state.initialized),
    running: isStateActiveThreadRunning(state),
  });
  const activeManagementTabID = showingManagementCatalog
    ? currentSessionTab?.id
    : undefined;
  useLayoutEffect(() => {
    if (!activeManagementTabID) {
      return;
    }
    // Catalogs reuse the conversation scroll viewport. Reset it when entering
    // a catalog tab so the previous conversation's offset cannot hide the
    // catalog title above the visible area.
    const scrollRegion = conversationScrollRef.current;
    if (scrollRegion) {
      scrollRegion.scrollTop = 0;
    }
  }, [activeManagementTabID, conversationScrollRef]);
  const conversationRailScrollContainer = useCallback((): HTMLElement | null => {
    if (splitConversation) {
      return splitPaneRefs.current[state.activePane] ?? null;
    }
    return conversationScrollRef.current;
  }, [conversationScrollRef, splitConversation, splitPaneRefs, state.activePane]);
  const focusMainComposer = useCallback(
    (
      target: ComposerVariant,
      origin: Element | null,
      interactionVersion: number,
    ): boolean => {
      // Touch/mobile must never autofocus the composer from a render effect: the
      // focus is outside the user gesture and would leave a caret without a
      // software keyboard. Actual taps focus natively, so only desktop keeps the
      // programmatic restore path.
      if (isTouchWebShell()) {
        return false;
      }
      // Empty and populated sessions share the same bottom composer.
      const visibleTarget = target === "hero" ? "dock" : target;
      const composer = conversationPaneRef.current?.querySelector<HTMLElement>(
        `[data-main-conversation-composer="${visibleTarget}"]`,
      );
      const textarea = composer?.querySelector<HTMLTextAreaElement>("textarea");
      if (!textarea || textarea.disabled) {
        return false;
      }

      const activeElement = document.activeElement;
      if (
        userInteractionVersionRef.current === interactionVersion &&
        (activeElement === document.body || activeElement === origin)
      ) {
        textarea.focus();
      }
      return true;
    },
    [conversationPaneRef],
  );
  const requestMainComposerFocus = useCallback(
    (
      target: ComposerVariant,
      origin: Element | null = document.activeElement,
      interactionVersion: number = userInteractionVersionRef.current,
      matchesDestination?: (state: AppState) => boolean,
    ): MainComposerFocusRequest => {
      const request = {
        target,
        origin,
        interactionVersion,
        matchesDestination,
      };
      setMainComposerFocusRequest(request);
      return request;
    },
    [],
  );
  const cancelMainComposerFocusRequest = useCallback(
    (request: MainComposerFocusRequest): void => {
      setMainComposerFocusRequest((current) =>
        current === request ? null : current,
      );
    },
    [],
  );

  useLayoutEffect(() => {
    if (!mainComposerFocusRequest) {
      return;
    }
    if (
      mainComposerFocusRequest.matchesDestination &&
      !mainComposerFocusRequest.matchesDestination(state)
    ) {
      return;
    }
    if (
      !focusMainComposer(
        mainComposerFocusRequest.target,
        mainComposerFocusRequest.origin,
        mainComposerFocusRequest.interactionVersion,
      )
    ) {
      return;
    }
    setMainComposerFocusRequest((current) =>
      current === mainComposerFocusRequest ? null : current,
    );
  }, [
    emptyConversation,
    focusMainComposer,
    mainComposerFocusRequest,
    mainConversationDockVisible,
    state,
  ]);
  const handleTurnCollapseComplete = useCallback(() => {
    scheduleStreamScroll();
  }, [scheduleStreamScroll]);

  // Captures the user's scroll position when they open the inline edit on a
  // historical message so cancel can put them back exactly where they were
  // instead of dropping them at the latest content via the resize observer.
  const preEditScrollSnapshotRef = useRef<ConversationScrollSnapshot | undefined>(
    undefined,
  );
  const rememberConversationScrollForEdit = useCallback((): void => {
    preEditScrollSnapshotRef.current = captureConversationScrollPosition();
  }, [captureConversationScrollPosition]);
  const restoreConversationScrollForEdit = useCallback((): void => {
    const snapshot = preEditScrollSnapshotRef.current;
    preEditScrollSnapshotRef.current = undefined;
    if (!snapshot) {
      return;
    }
    restoreConversationScrollPosition(snapshot);
  }, [restoreConversationScrollPosition]);
  const canEditCachedThreadMessage = useStableCallback((thread: Thread) =>
    canShowHistoryEditButton(thread),
  );
  const handleCachedPaneForkMessage = useStableCallback(
    (thread: Thread, turnID: string, itemID: string) => {
      void forkThreadFromMessage(thread, turnID, itemID);
    },
  );
  const handleCachedPaneEditMessage = useStableCallback(
    (thread: Thread, turnID: string, item: ThreadItem) => {
      startEditingThreadMessageFromHistory(thread, turnID, item);
    },
  );
  const handleCachedPaneCancelEditMessage = useStableCallback(() => {
    cancelEditingThreadMessage();
  });
  const handleCachedPaneSubmitEditMessage = useStableCallback(
    (
      thread: Thread,
      turnID: string,
      item: ThreadItem,
      text: string,
      images: InputImage[],
      files: InputFile[],
      contentParts?: MessageContentPart[],
    ) => {
      return submitEditedThreadMessageFromHistory(
        thread,
        turnID,
        item,
        text,
        images,
        files,
        contentParts,
      );
    },
  );
  // Stable identities for every remaining CachedConversationPanes
  // callback prop. The component is React.memo'd; a single freshly
  // created arrow prop defeats the bailout and re-renders the full
  // cached turn lists on EVERY App state change — that full re-render
  // is the sidebar click lag (collapse a section → conversation pane
  // re-renders for nothing).
  const handleCachedPaneDismissContextComposition = useStableCallback(
    (id: string) => {
      dismissContextCompositionEntry(id);
    },
  );
  const handleCachedPaneDismissInstructions = useStableCallback((id: string) => {
    dismissInstructionFilesEntry(id);
  });
  const handleCachedPaneOpenAgent = useStableCallback((agent: Agent) => {
    void selectChildAgent(agent);
  });
  // Details actions on subagent completion messages split the conversation
  // and open the child session in the secondary pane. The App owns thread
  // state, so it registers the bridge handler once; message buttons call it.
  const handleOpenThreadInSplit = useStableCallback((threadID: string) => {
    void (async () => {
      try {
        const thread = requireThread(
          await window.wuu.resumeThread(threadID),
          t("thread.childResumeMissing"),
        );
        setState((current) => ({
          ...current,
          secondaryThread: thread,
          activePane: current.activePane === "secondary" ? "secondary" : "primary",
          threads: upsertThread(current.threads, thread),
        }));
      } catch (error) {
        showErrorToast(
          error instanceof Error ? error.message : t("thread.childLoadFailed"),
        );
      }
    })();
  });
  useEffect(() => {
    setOpenThreadInSplitHandler(handleOpenThreadInSplit);
    return () => setOpenThreadInSplitHandler(undefined);
  }, [handleOpenThreadInSplit]);
  const handleCachedPaneOpenFileDiff = useStableCallback(
    (thread: Thread, selection: TurnFileDiffSelection) => {
      openTurnFileDiffPanel(thread.id, selection);
    },
  );
  const openWorkspaceBrowser = useStableCallback((target: WorkspaceBrowserOpenTarget): void => {
    requestWorkspaceBrowserNavigation(target);
    const focus = workspaceBrowserFocusDecision({
      rightPanelOpen,
      activeTabID: workspaceActiveViewTabID,
      browserForegroundOccupied: isForegroundControlled(activeBrowserActivity),
    });
    if (!focus.stealFocus) {
      ensureWorkspaceToolTab("browser");
      return;
    }
    openWorkspaceTool("browser");
  });
  const openWorkspaceBrowserURL = useStableCallback((
    url: string,
    modifiers?: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; button?: number },
  ): void => {
    openWorkspaceBrowserOrExternal(url, modifiers, openWorkspaceBrowser);
  });
  const openWorkspaceFile = useStableCallback((path: string): void => {
    // Stamp the same derived context the workspace panel's file tree/preview
    // are rooted at (workspacePanelContext), not the raw activeContext — for
    // a worktree-fork thread these differ, and activeWorkspaceFile's match
    // above must be comparing against the same context or the tab silently
    // stops highlighting/previewing once opened.
    const context = workspacePanelContext(
      appStateRef.current.activeContext,
      appStateRef.current.thread,
    );
    if (!context) {
      return;
    }
    openWorkspaceFileTab({ context, path });
  });
  const openWorkspaceFileForThread = useStableCallback((thread: Thread, path: string): void => {
    const context = workspacePanelContext(appStateRef.current.activeContext, thread);
    if (!context) {
      return;
    }
    openWorkspaceFileTab({ context, path });
  });
  const rememberWorkspaceDirtyFiles = useStableCallback((dirty: boolean): void => {
    workspaceHasDirtyFilesRef.current = dirty;
  });
  const sidebarWorkspaceThreadsByWorkspaceID = workspaceThreadsByWorkspaceID;
  const sidebarThreads = useMemo(() => {
    const byID = new Map<string, Thread>();
    for (const thread of cachedScratchThreads) {
      byID.set(thread.id, thread);
    }
    for (const threads of Object.values(sidebarWorkspaceThreadsByWorkspaceID)) {
      for (const thread of threads) {
        byID.set(thread.id, thread);
      }
    }
    for (const thread of state.threads) {
      byID.set(thread.id, thread);
    }
    return sortThreads([...byID.values()]);
  }, [
    cachedScratchThreads,
    sidebarWorkspaceThreadsByWorkspaceID,
    state.threads,
  ]);
  const visibleRunningThreadIDs = useMemo(
    () => presentationRunningThreadIDs(
      [state.thread, state.secondaryThread, ...sidebarThreads],
      crossWorkdirRunningThreadIDs,
    ),
    [crossWorkdirRunningThreadIDs, sidebarThreads, state.secondaryThread, state.thread],
  );
  const sidebarWorkspaceThreadSummariesByWorkspaceID = useMemo(
    () => summarizeWorkspaceThreadsForSidebar(
      sidebarWorkspaceThreadsByWorkspaceID,
      state.threads,
      visibleRunningThreadIDs,
    ),
    [
      sidebarWorkspaceThreadsByWorkspaceID,
      state.threads,
      visibleRunningThreadIDs,
    ],
  );
  const sidebarThreadSummaries = useMemo(
    () => summarizeThreadsForSidebar(sidebarThreads, visibleRunningThreadIDs),
    [sidebarThreads, visibleRunningThreadIDs],
  );
  const sidebarPinnedThreads = useMemo(
    () => pinnedThreadSummaries(sidebarThreadSummaries),
    [sidebarThreadSummaries],
  );
  const sidebarScratchThreads = useMemo(
    () => scratchThreadSummaries(sidebarThreadSummaries, state.projects),
    [sidebarThreadSummaries, state.projects],
  );
  // The scratch pseudo project lives at the top of the sidebar tree. It is
  // a synthetic DesktopProject (id = SCRATCH_PSEUDO_PROJECT_ID) whose
  // threads are the scratch conversations pulled out of
  // sidebarThreadSummaries above. path is intentionally "" — ThreadSidebar
  // special-cases the scratch pseudo id and skips its cwd-path filter.
  const scratchPseudoWorkspace = useMemo<DesktopProject>(
    () => ({
      id: SCRATCH_PSEUDO_PROJECT_ID,
      name: t("sidebar.conversations"),
      path: "",
      created_at: new Date(0).toISOString(),
      updated_at: new Date(0).toISOString(),
    }),
    [t],
  );
  const sidebarWorkspaces = useMemo<DesktopProject[]>(
    () => [scratchPseudoWorkspace, ...state.projects],
    [scratchPseudoWorkspace, state.projects],
  );
  const sidebarThreadsByWorkspaceID = useMemo(
    () => ({
      [SCRATCH_PSEUDO_PROJECT_ID]: sidebarScratchThreads,
      ...sidebarWorkspaceThreadSummariesByWorkspaceID,
    }),
    [sidebarScratchThreads, sidebarWorkspaceThreadSummariesByWorkspaceID],
  );
  const activeThreadReadOnly = Boolean(activeThread?.read_only);
  const activeThreadIsRunning = isStateActiveThreadRunning(state);
  const activeThreadCanSteer = activeTurnAcceptsSteering(activeThread);
  const activeThreadAnswerReady = activeTurnIsAnswerReady(activeThread);
  const composerTurnRunning = activeThreadIsRunning && !activeThreadAnswerReady;
  const activeThreadStreamStatus = activeThreadAnswerReady
    ? undefined
    : turnStreamStatusForThread(state, activeThread);
  const anyThreadIsRunning = isAnyThreadRunning(state) || viewContextSwitchPending;
  const runningThreadKey = useMemo(() => {
    const running = new Set<string>();
    for (const thread of [state.thread, state.secondaryThread, ...state.threads]) {
      if (thread?.cwd && isThreadRunning(thread)) {
        running.add(`${thread.id}\0${thread.cwd}`);
      }
    }
    if (state.running && activeThread?.cwd) {
      running.add(`${activeThread.id}\0${activeThread.cwd}`);
    }
    return [...running].sort().join("\x01");
  }, [
    activeThread?.cwd,
    activeThread?.id,
    state.running,
    state.secondaryThread,
    state.thread,
    state.threads,
  ]);
  const activeWorkingTreeBusy = useGitActionBusy(
    environmentContext,
    runningThreadKey,
  );
  // The Environment panel's git actions (branch switch / commit / PR) mutate the
  // active session's working tree, so they are gated on that tree being busy —
  // not on any thread anywhere running. A worktree-fork thread running in its
  // own cwd, or a thread in another project, no longer blocks them.
  const environmentGitBusy = activeWorkingTreeBusy || viewContextSwitchPending;
  // The desktop pet lives in its own always-on-top window owned by the main
  // process; the renderer only feeds it the session runtime so its sprite
  // state tracks what the app is doing.
  useEffect(() => {
    const api = window.wuu as Partial<typeof window.wuu>;
    if (typeof api.updateCodexPetRuntime !== "function" || !hostSupports("updateCodexPetRuntime")) {
      return;
    }
    void api
      .updateCodexPetRuntime({
        running: anyThreadIsRunning,
        status: resolveLocalizedText(state.status),
      })
      .catch(() => undefined);
  }, [anyThreadIsRunning, locale, state.status]);
  // The pet bubble is a lightweight hint of the most relevant session.
  // Re-derive whenever the thread state changes and push the result to
  // the main process, which keeps the always-on-top pet window in sync.
  // See ./activeSessionHint for the priority logic.
  // `unreadThreadIDs` lets an idle thread outrank a plain idle one when its
  // latest completed turn has not been viewed yet, so a finished-but-unread
  // conversation still surfaces in the bubble.
  const unreadThreadIDs = useMemo(() => {
    const ids = new Set<string>();
    for (const thread of [state.thread, state.secondaryThread, ...state.threads]) {
      if (!thread?.id) continue;
      if (
        isThreadUnread(
          thread,
          state.lastViewedTurnByThreadID[thread.id],
        )
      ) {
        ids.add(thread.id);
      }
    }
    return ids;
  }, [
    state.thread,
    state.secondaryThread,
    state.threads,
    state.lastViewedTurnByThreadID,
  ]);
  useEffect(() => {
    const api = window.wuu as Partial<typeof window.wuu>;
    if (typeof api.updateCodexPetHints !== "function" || !hostSupports("updateCodexPetHints")) return;
    const hints = deriveActiveSessionHints({
      thread: state.thread ?? undefined,
      secondaryThread: state.secondaryThread ?? undefined,
      threads: state.threads,
      unreadThreadIDs,
    });
    void api.updateCodexPetHints(hints).catch(() => undefined);
  }, [
    state.thread,
    state.secondaryThread,
    state.threads,
    unreadThreadIDs,
  ]);
  const runningProviderNames = useMemo(() => {
    const names = new Set<string>();
    for (const thread of [state.thread, state.secondaryThread, ...state.threads]) {
      const provider = thread?.model_provider.trim();
      if (provider && isThreadRunning(thread)) {
        names.add(provider);
      }
    }
    return Array.from(names);
  }, [state.thread, state.secondaryThread, state.threads]);
  const sideThreadPanelVisible = Boolean(activeThreadID && sideThread.entry?.open);
  useEffect(() => {
    if (!sideThreadPanelVisible || isTouchWebShell()) {
      return undefined;
    }
    const frame = window.requestAnimationFrame(() => {
      sideThreadPanelRef.current?.focusComposer();
    });
    return () => window.cancelAnimationFrame(frame);
  }, [sideThreadPanelVisible]);
  // The environment panel floats inside the conversation pane, so it can
  // coexist with the docked workspace right panel. Only the globalized
  // (full-window sheet) right panel blocks it, because that mode makes the
  // entire conversation pane inert.
  const environmentPanelCanShow = Boolean(
    state.initialized &&
    !poppedOutMode &&
    !rightPanelGlobalized &&
    !sideThreadPanelVisible,
  );
  const environmentPanelTargetVisible =
    environmentPanelCanShow &&
    (environmentPanelOpen ||
      (environmentPanelHasRoom &&
        !environmentPanelDismissed &&
        !emptyConversation));
  const environmentPanelVisible = environmentPanelTargetVisible;
  const environmentPanelMotionState: EnvironmentPanelMotionState =
    environmentPanelVisible ? "open" : "closing";
  // The sidebar is the single conversation switcher. Session state remains
  // available for recovery and drafts, but the titlebar no longer renders a
  // growing tab strip.
  const sidebarVisible = !poppedOutMode;
  const sidebarToggleVisible = sidebarVisible;

  useEffect(() => {
    if (sideThread.entry?.open && environmentPanelOpen) {
      sideThread.close();
    }
  }, [environmentPanelOpen, sideThread.close, sideThread.entry?.open]);

  const shellClassName = `app-shell${poppedOutMode ? " popped-out-shell" : ""}${compactNavigation ? " compact-navigation" : ""}${sidebarDrawerMode ? " sidebar-collapsed" : ""}${
    sidebarDrawerMode && sidebarDrawerVisible ? " sidebar-drawer-open" : ""
  }${
    sidebarDrawerMode &&
    !(!sidebarCollapsed && rightPanelGlobalized) &&
    sidebarDrawerPhase === "closing"
      ? " sidebar-drawer-closing"
      : ""
  }${
    !sidebarDrawerMode && sidebarDrawerPhase === "docking"
      ? " sidebar-drawer-docking"
      : ""
  }${
    sidebarAnimating ? " sidebar-animating" : ""
  }${rightPanelAnimating ? " right-panel-animating" : ""}${resizingSidebar ? " resizing-sidebar" : ""}${
    resizingRightPanel ? " resizing-right-panel" : ""
  }${rightPanelOpen ? " right-panel-open" : ""}${rightPanelGlobalized && rightPanelOpen ? " right-panel-globalized" : ""}${resizingSplit ? " resizing-split" : ""}`;
  const shellStyle = {
    "--sidebar-width": `${effectiveSidebarWidth}px`,
    "--sidebar-open-width": `${sidebarWidth}px`,
    "--workspace-sheet-left": `${sidebarDrawerMode ? 0 : effectiveSidebarWidth}px`,
    "--workspace-right-panel-width": `${clampedWorkspaceRightPanelWidth}px`,
    "--side-thread-width": `${sideThread.width}px`,
    "--conversation-split-left": `${splitLeftPercent}%`,
    "--environment-panel-width": ENVIRONMENT_PANEL_WIDTH_CSS,
    "--environment-panel-reserved-width": "372px",
    "--environment-panel-edge-gap": "18px",
  } as CSSProperties;
  const pullRequestDisabledReason = pullRequestUnavailableReason(
    state.gitStatus,
  );
  useLayoutEffect(() => {
    if (environmentPanelVisible) {
      setEnvironmentPanelMounted(true);
      setEnvironmentPanelClosing(false);
      setEnvironmentPanelReserved(environmentPanelHasRoom);
      scheduleGitStatusRefresh(0);
      return;
    }
    if (!environmentPanelMounted) {
      setEnvironmentPanelReserved(false);
      return;
    }

    setEnvironmentPanelClosing(true);
    const timer = window.setTimeout(() => {
      setEnvironmentPanelMounted(false);
      setEnvironmentPanelClosing(false);
      setEnvironmentPanelReserved(false);
    }, motionDurationMs("--environment-panel-exit-duration", 220));
    return () => window.clearTimeout(timer);
  }, [
    environmentPanelHasRoom,
    environmentPanelMounted,
    environmentPanelVisible,
  ]);

  useEffect(() => {
    if (!environmentPanelVisible && environmentPanelMenu) {
      setEnvironmentPanelMenu(null);
    }
  }, [environmentPanelMenu, environmentPanelVisible]);

  const handleCloseFilePreview = useCallback((): void => {
    setRightPanelFilePath(undefined);
    setEnvironmentPanelMenu(null);
  }, []);

  // Mark the active thread's latest completed turn as viewed so the sidebar
  // and session tab strip stop showing the "has-unread" dot. This effect is
  // the single source of truth for advancing `lastViewedTurnByThreadID`; any
  // state change that re-renders the conversation (tab switch, new turn for
  // the active thread) reaches here. Mid-stream turns stay deferred until
  // the visible answer is ready, matching the tab/sidebar unread boundary.
  useEffect(() => {
    const tab = state.sessionTabs.find(
      (candidate) => candidate.id === state.activeSessionTabID,
    );
    if (tab?.kind !== "thread") return;
    const thread = threadForTab(state, tab.threadID);
    if (!thread) return;
    setState((current) => {
      const next = markThreadTurnsViewed(current, thread.id);
      return next === current ? current : next;
    });
  }, [state.activeSessionTabID, state.thread, state.threads]);

  function openSideThreadPanel(prompt?: string): void {
    if (!activeThreadID) {
      return;
    }
    if (!sideThread.entry?.open) {
      setEnvironmentPanelOpen(false);
      setEnvironmentPanelDismissed(true);
      setEnvironmentPanelMenu(null);
      sideThread.open();
    }
    const trimmed = prompt?.trim();
    if (trimmed) {
      sideThread.sendMessage(trimmed);
    }
  }

  // Blocking questions stay in the conversation stream. Offers float above the composer.
  const pendingUserQuestion = userQuestionApiAvailable
    ? userQuestions.find((request) => request.thread_id === activeThreadID && request.mode !== "offer")
    : undefined;
  const pendingUserQuestionOffer = userQuestionApiAvailable
    ? userQuestions.find((request) => request.thread_id === activeThreadID && request.mode === "offer")
    : undefined;

  const answerUserQuestion = useCallback(
    async (requestID: string, answer: UserQuestionAnswer): Promise<void> => {
      await window.wuu.answerUserQuestion(requestID, answer);
      setUserQuestions((current) =>
        current.filter((request) => request.request_id !== requestID),
      );
    },
    [],
  );

  const cancelUserQuestion = useCallback(
    async (requestID: string): Promise<void> => {
      await window.wuu.cancelUserQuestion(requestID);
      setUserQuestions((current) =>
        current.filter((request) => request.request_id !== requestID),
      );
    },
    [],
  );

  const holdUserQuestion = useCallback(
    async (requestID: string): Promise<void> => {
      if (typeof window.wuu.holdUserQuestion !== "function") return;
      await window.wuu.holdUserQuestion(requestID);
      setUserQuestions((current) => current.map((request) =>
        request.request_id === requestID ? { ...request, expires_at: undefined } : request,
      ));
    },
    [],
  );

  function renderComposer(variant: ComposerVariant): JSX.Element {
    const telemetryTurnID = activeThread
      ? activeTurnIDForThread(activeThread)
      : undefined;
    // Drives the composer context meter. Existing threads use the latest
    // known usage; a brand-new session falls back to the current runtime
    // window so the meter can render at 0% before the first turn.
    const conversationRuntime = visibleConversationRuntime;
    const modelContextWindow = providerModelContextWindow(
      state.initialized,
      conversationRuntime?.provider,
      conversationRuntime?.model,
    );
    const fallbackContextWindow =
      modelContextWindow ??
      (!activeThread
        ? state.initialized?.advanced_settings?.context_window_tokens
        : undefined);
    const rawContextUsage = latestContextUsageForThread(state, activeThread, {
      model: conversationRuntime?.model,
      contextWindowTokens: fallbackContextWindow,
    });
    // Live telemetry can still carry a ceiling from the previous workspace
    // runtime. Once the exact conversation model is known, reuse the same
    // provider-summary ceiling the backend budgeted with (including channel
    // clamps such as Codex subscription input caps).
    const contextUsage =
      rawContextUsage && modelContextWindow
        ? { ...rawContextUsage, window: modelContextWindow }
        : rawContextUsage;
    const streamStatus = activeThreadStreamStatus;
    const defaultEngine = engineInventory?.settings?.default_engine ?? "";
    const effectiveEngine =
      (activeThread?.engine_id ?? "") || draftEngine || defaultEngine || "wuu";
    const effectiveEngineInfo = engineInventory?.engines.find(
      (engine) => engine.id === effectiveEngine,
    );
    const defaultEngineRuntime = defaultEngineRuntimeSelection(effectiveEngineInfo);
    const effectiveEngineRuntime = activeThread && effectiveEngine !== "wuu"
      ? {
          model: activeThread.model,
          effort: activeThread.model_effort ?? activeThread.model_variant ?? "",
        }
      : {
          model: draftEngineRuntime.model || defaultEngineRuntime.model,
          effort: draftEngineRuntime.effort || defaultEngineRuntime.effort,
        };
    const composerPermissionMode =
      activeThread?.permission_mode
      || (!activeThread && effectiveEngine !== "wuu"
        ? draftPermissionMode || "unconfined"
        : conversationRuntime?.permissions?.mode);
    const composerRuntime = conversationRuntime && composerPermissionMode
      ? {
          ...conversationRuntime,
          permissions: { ...conversationRuntime.permissions, mode: composerPermissionMode },
        }
      : conversationRuntime;
    return (
      <>
      <Composer
        canSelectWorkspace={!composerNavigation && !activeThread && !activePendingNewThreadTurn}
        hideExpandButton={composerNavigation}
        topAccessory={pendingUserQuestionOffer ? (
          <UserQuestionCard
            request={pendingUserQuestionOffer}
            onAnswer={async (answer) => {
              const prompt = formatUserQuestionSteerPrompt(pendingUserQuestionOffer, answer);
              await answerUserQuestion(pendingUserQuestionOffer.request_id, answer);
              await sendPrompt("steer", prompt);
            }}
            onCancel={() => cancelUserQuestion(pendingUserQuestionOffer.request_id)}
            onHold={() => holdUserQuestion(pendingUserQuestionOffer.request_id)}
            onCustom={async (text) => {
              if (text?.trim()) {
                await sendPrompt("steer", text.trim(), undefined, pendingUserQuestionOffer.request_id);
                return;
              }
              requestMainComposerFocus(variant === "hero" ? "hero" : "dock");
            }}
          />
        ) : undefined}
        variant={variant}
        mainConversation
        containerRef={variant === "dock" ? dockComposerRef : undefined}
        prompt={prompt}
        promptRevision={promptRevision}
        setPrompt={setPromptFromInput}
        files={composerFiles}
        images={composerImages}
        queuedMessages={activePendingThreadCreation
          ? pendingComposerMessagesForActiveThread(activePendingThreadCreation.sessionTabID).queued
          : queuedMessages}
        guideMessages={guideMessages}
        sendDisabled={submissionTargetPending || Boolean(activeThread && stopRequests[activeThread.id])}
        stopState={activeThread ? stopRequests[activeThread.id] : undefined}
        running={
          Boolean(activePendingThreadCreation) ||
          (!activeThreadReadOnly && composerTurnRunning) ||
          viewContextSwitchPending
        }
        runtimeControlsDisabled={
          Boolean(activePendingThreadCreation) ||
          (!activeThreadReadOnly && activeThreadIsRunning) ||
          viewContextSwitchPending
        }
        telemetryTurnID={telemetryTurnID}
        contextUsage={contextUsage}
        status={
          activeThreadReadOnly
            ? activeThreadIsRunning
              ? t("app.childTaskRunning")
              : t("app.childTaskReadOnly")
            : state.status
        }
        statusLiveProgress={
          false
        }
        readOnly={activeThreadReadOnly}
        initialized={composerRuntime}
        engines={engineInventory?.engines}
        activeEngine={effectiveEngine !== "wuu" ? effectiveEngine : ""}
        engineLocked={Boolean(activeThread)}
        engineModel={effectiveEngineRuntime.model}
        engineEffort={effectiveEngineRuntime.effort}
        onSelectEngine={selectDraftEngine}
        onSelectEngineModel={(model, effort) => {
          const runtime = { model, effort };
          setDraftEngineRuntime(runtime);
          draftEngineRuntimeByID.current[effectiveEngine] = runtime;
          // Only a new conversation writes the memory: for an existing thread
          // the engine is already bound and the picker is locked.
          if (!activeThread) {
            writeDraftEngineMemory({ engine: effectiveEngine, model, effort });
          }
        }}
        onSelectEngineEffort={(effort) => {
          setDraftEngineRuntime((current) => {
            const runtime = { ...current, effort };
            draftEngineRuntimeByID.current[effectiveEngine] = runtime;
            return runtime;
          });
          if (!activeThread) {
            writeDraftEngineMemory({
              engine: effectiveEngine,
              model: effectiveEngineRuntime.model,
              effort,
            });
          }
        }}
        gitStatus={state.gitStatus}
        branchPickerDisabled={viewContextSwitchPending}
        projects={state.projects}
        activeContext={state.activeContext}
        activeWorkspace={activeWorkspace}
        compactDisabledReason={
          !activeThread
            ? t("app.openConversationFirst")
            : activeThread.engine_id && activeThread.engine_id !== "wuu"
              ? t("slash.compact.externalEngineUnavailable")
              : undefined
        }
        sideThreadDisabledReason={
          !activeThread ? t("app.sendMessageFirst") : undefined
        }
        handoffDisabledReason={
          !activeThread
            ? t("app.openConversationFirst")
            : activeThread.engine_id && activeThread.engine_id !== "wuu"
              ? t("slash.compact.externalEngineUnavailable")
              : undefined
        }
        codexModels={codexModels}
        codexRuntimeMenu={codexRuntimeMenu}
        codexRuntimeRef={codexRuntimeRef}
        menuOpen={runtimeMenuOpen}
        accessMenuOpen={accessMenuOpen}
        branchMenuOpen={branchMenuOpen}
        menuRef={runtimeMenuRef}
        accessMenuRef={accessMenuRef}
        workspaceFilter={workspaceFilter}
        setWorkspaceFilter={setWorkspaceFilter}
        onToggleMenu={() => {
          setAccessMenuOpen(false);
          setBranchMenuOpen(false);
          setCodexRuntimeMenu(null);
          setRuntimeMenuOpen((open) => !open);
        }}
        onToggleAccessMenu={() => {
          setRuntimeMenuOpen(false);
          setBranchMenuOpen(false);
          setCodexRuntimeMenu(null);
          setAccessMenuOpen((open) => !open);
        }}
        onToggleBranchMenu={() => {
          if (!branchMenuOpen) scheduleGitStatusRefresh(0);
          setRuntimeMenuOpen(false);
          setAccessMenuOpen(false);
          setCodexRuntimeMenu(null);
          setBranchMenuOpen((open) => !open);
        }}
        onToggleCodexRuntimeMenu={(menu) => {
          toggleCodexRuntimeMenu(menu);
          // Revalidate only after the shared session snapshot becomes stale;
          // opening this frequently-used picker must not restart detection.
          void refreshEngineInventory();
        }}
        onSelectRuntimeModel={async (provider, model, variant) => {
          const request = mascotRuntimePreviewRequestRef.current + 1;
          mascotRuntimePreviewRequestRef.current = request;
          setMascotRuntimePreview({ provider, model });
          const committed = await selectRuntimeModel(provider, model, variant);
          if (mascotRuntimePreviewRequestRef.current === request) {
            setMascotRuntimePreview(null);
          }
          return committed;
        }}
        onSelectRuntimeEffort={(nextVariant) =>
          selectRuntimeEffort(nextVariant)
        }
        onSelectPermissionMode={(mode, approveForMe) => {
          if (!activeThread && effectiveEngine !== "wuu") {
            setDraftPermissionMode(mode);
            setAccessMenuOpen(false);
            return;
          }
          void selectPermissionMode(mode, approveForMe);
        }}
        onOpenSettings={() => {
          closeWorkspaceMenus();
          setSettingsInitialPage("providers");
          setSettingsOpen(true);
        }}
        onOpenSkillsCatalog={openSkillsTab}
        onSelectWorkspace={(id) => void selectWorkspaceForNewThread(id)}
        onSelectNoProject={() => void useNoProject(false)}
        onSelectGitBranch={checkoutBranch}
        onCreateGitBranch={async (branch) => {
          await createAndCheckoutBranch(branch);
          setBranchMenuOpen(false);
        }}
        onCreateWorkspace={() => void createBlankProject()}
        onOpenWorkspace={() => void chooseProjectFolder()}
        onStartNewThread={startNewThreadWithComposerFocus}
        onHandoffSession={handoffActiveThread}
        onOpenSideThread={openSideThreadPanel}
        onOpenWorkspaceTool={openWorkspaceTool}
        onOpenContextComposition={openContextComposition}
        onCompactContext={() => void compactActiveThread()}
        onOpenInstructions={openInstructions}
        onPasteAttachmentFiles={(files) => void attachComposerAttachmentFiles(files)}
        onRemoveFile={removeComposerFile}
        onRemoveImage={removeComposerImage}
        onRemoveQueuedMessage={removeQueuedMessage}
        onRemoveGuideMessage={removeGuideMessage}
        onGuideQueuedMessage={(id) => {
          if (!activeThread || !stopRequestsRef.current[activeThread.id]) void guideQueuedMessage(id);
        }}
        onEditQueuedMessage={(id) => void editQueuedMessage(id)}
        onEditGuideMessage={(id) => void editGuideMessage(id)}
        onSend={(promptOverride, contentParts) => sendPrompt("queue", promptOverride, contentParts, pendingUserQuestionOffer?.request_id)}
        onSteer={
          activeThreadIsRunning && activeThread && activeThreadCanSteer
            ? (promptOverride, contentParts) => sendPrompt("steer", promptOverride, contentParts, pendingUserQuestionOffer?.request_id)
            : undefined
        }
        onQueue={
          (activePendingThreadCreation || (activeThreadIsRunning && activeThread))
            ? (promptOverride, contentParts) => sendPrompt("queue", promptOverride, contentParts, pendingUserQuestionOffer?.request_id)
            : undefined
        }
        onInterrupt={() => {
          if (activePendingThreadCreation) activePendingThreadCreation.cancel();
          else void interrupt();
        }}
        queryHistorySessionID={activeThread?.id ?? currentSessionTab?.id}
        queryHistory={queryTextsForThread(activeThread)}
        requestedHandoffIntent={requestedHandoffIntentForThread(activeThread)}
      />
      </>
    );
  }

  function openProviderSettings(): void {
    closeWorkspaceMenus();
    setSettingsInitialPage("providers");
    setSettingsOpen(true);
  }

  function showNoModelConfiguredToast(): void {
    showToast({
      message: t("composer.noModelConfigured"),
      tone: "error",
      dedupeKey: "composer:no-model-configured",
      action: {
        label: t("common.goConfigure"),
        onClick: openProviderSettings,
      },
    });
  }

  function openArchiveSettings(): void {
    // Used by both the sidebar entry (when one is added later) and the
    // archive-tip toast: always jump the Settings shell to the Archive page,
    // even if Settings was already open on a different tab.
    setWorkspaceMenuOpen(false);
    setRuntimeMenuOpen(false);
    setCodexRuntimeMenu(null);
    setSettingsInitialPage("archive");
    setSettingsOpen(true);
  }

  async function refreshModelCatalog(): Promise<void> {
    setModelCatalogTip(null);
    try {
      const result = await window.wuu.refreshModelCatalog();
      setState((current) =>
        current.initialized
          ? {
              ...current,
              initialized: { ...current.initialized, providers: result.providers },
            }
          : current,
      );
      for (const provider of result.providers) {
        if (["openai-codex", "codex-subscription", "chatgpt-codex"].includes(provider.type.replaceAll("_", "-").toLowerCase())) {
          await loadCodexModelsForProvider(provider.name, true);
        }
      }
      setModelCatalogTip({
        message: t("settings.modelCatalogUpdated", { count: result.model_count }),
        isError: false,
      });
    } catch {
      setModelCatalogTip({
        message: t("settings.modelCatalogUpdateFailed"),
        isError: true,
      });
    }
  }

  function closeWorkspaceMenus(): void {
    setWorkspaceMenuOpen(false);
    setRuntimeMenuOpen(false);
    setAccessMenuOpen(false);
    setCodexRuntimeMenu(null);
    setBranchMenuOpen(false);
    setEnvironmentPanelMenu(null);
    setSettingsOpen(false);
    setWorkspaceFilter("");
  }

  const {
    checkoutBranch,
    scheduleGitStatusRefresh,
    createAndCheckoutBranch,
    commitEnvironmentChanges,
    generateEnvironmentCommitMessage,
    createEnvironmentPullRequest,
    toggleEnvironmentPanel,
    openEnvironmentPanel,
    closeEnvironmentPanel,
  } = createEnvironmentActions({
    getAppState: () => appStateRef.current,
    getEnvironmentRoot: () =>
      workspacePanelContext(
        appStateRef.current.activeContext,
        activeThreadForState(appStateRef.current),
      )?.cwd,
    setAppState: setState,
    closeWorkspaceMenus,
    setEnvironmentPanelOpen,
    setEnvironmentPanelDismissed,
    setEnvironmentPanelMenu,
    closeRuntimeMenus: () => {
      setRuntimeMenuOpen(false);
      setAccessMenuOpen(false);
      setBranchMenuOpen(false);
      setCodexRuntimeMenu(null);
    },
    getEnvironmentPanelVisible: () => environmentPanelVisible,
    environmentPanelContainsActiveElement: () => {
      const activeElement = document.activeElement;
      return (
        activeElement instanceof HTMLElement &&
        environmentPanelRef.current?.contains(activeElement) === true
      );
    },
    focusEnvironmentToggle: () =>
      environmentToggleRef.current?.focus({ preventScroll: true }),
    gitRefreshTimerRef,
    gitRefreshInFlightRef,
    gitRefreshQueuedRef,
  });

  function canShowHistoryEditButton(thread: Thread): boolean {
    return (
      !thread.read_only &&
      !isThreadRunning(thread) &&
      !threadHasPendingComposerMessages(thread.id)
    );
  }

  function restoreSessionTabComposerDraft(tab: SessionTab): void {
    restorePrimaryComposerDraft(cloneSessionTabDraft(tab));
    setSplitComposerDrafts(initialSplitComposerDrafts());
  }

  function restoreLoadedRuntimeComposerDraft(
    loadedState: Partial<AppState>,
    carryDraft?: ComposerDraftState,
  ): void {
    const context = loadedState.activeContext;
    if (!context) {
      return;
    }
    // When a draft is being carried across the switch (see
    // applyLoadedRuntimeWithDraftCarry), the composer should keep showing
    // exactly what the user had typed rather than whatever the target
    // context's own tab already held.
    if (carryDraft) {
      restorePrimaryComposerDraft(carryDraft);
      setSplitComposerDrafts(initialSplitComposerDrafts());
      return;
    }
    restoreSessionTabComposerDraft(
      sessionTabForLoadedRuntime(
        appStateRef.current.sessionTabs,
        context,
        loadedState.thread,
      ),
    );
  }

  function nextDraftSessionTab(context: RuntimeContext): SessionTab {
    draftSessionTabCounterRef.current += 1;
    return createDraftSessionTab(
      `draft:${Date.now()}:${draftSessionTabCounterRef.current}`,
      context,
    );
  }

  const {
    selectWorkspaceForNewThread,
    startNewThreadInWorkspace,
    createBlankProject,
    chooseProjectFolder,
    removeProject,
    relocateProject,
    useNoProject,
  } = createWorkspaceRuntimeActions({
    getAppState: () => appStateRef.current,
    setAppState: setState,
    getPrimaryComposerDraft: currentPrimaryComposerDraft,
    restorePrimaryComposerDraft,
    clearPrimaryComposerDraft: () =>
      restorePrimaryComposerDraft(emptyComposerDraft()),
    restoreLoadedRuntimeComposerDraft,
    nextDraftSessionTab,
    isDraftPending: (tabID) => pendingThreadCreationsRef.current.has(tabID),
    closeWorkspaceMenus,
    
    beginViewSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    loadRuntime,
  });

  const {
    selectThread,
    selectWorkspaceThread,
    activateThread,
    selectChildAgent,
  } = createThreadActivationActions({
    getAppState: () => appStateRef.current,
    setAppState: setState,
    getActiveThreadID: () => activeThreadID,
    getPendingViewSwitch: () => pendingViewSwitch,
    getPrimaryComposerDraft: currentPrimaryComposerDraft,
    restorePrimaryComposerDraft,
    resetSplitComposerDrafts: () =>
      setSplitComposerDrafts(initialSplitComposerDrafts()),
    getSidebarThreads: () => sidebarThreads,
    getSidebarWorkspaceThreadsByWorkspaceID: () =>
      sidebarWorkspaceThreadsByWorkspaceID,
    getRunningThreadIDs: () => crossWorkdirRunningThreadIDs,
    
    beginViewSwitch,
    beginInstantThreadSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    isCurrentViewSwitchRequest,
    selectRuntimeContext,
  });

  useEffect(() => {
    const subscribe = window.wuu.onBrowserDock;
    if (typeof subscribe !== "function") return undefined;
    return subscribe((payload) => {
      if (!payload || [payload.thread_id, payload.workdir, payload.tabID]
        .some((value) => typeof value !== "string" || !value.trim())) return;
      setPendingBrowserDock({ target: payload, ready: false });
      revealConversationFromFocusedWorkspace();
      void activateThread(payload.thread_id).then(() => {
        setPendingBrowserDock((current) => current?.target === payload
          ? { target: payload, ready: true } : current);
      });
    });
  }, [activateThread, revealConversationFromFocusedWorkspace]);

  useEffect(() => {
    if (!pendingBrowserDock?.ready) return;
    const { target } = pendingBrowserDock;
    setPendingBrowserDock(undefined);
    // Activation may fail or be superseded. Wait for its committed state before
    // opening the panel, and never substitute the previously active session.
    if (activeThreadID !== target.thread_id || state.activeContext?.cwd !== target.workdir) return;
    setBrowserDockTarget(target);
    openWorkspaceBrowserRef.current("browser");
  }, [pendingBrowserDock, activeThreadID, state.activeContext?.cwd]);

  // The pet bubble click sends a `wuu:codex-pet-jump` event from main;
  // bring the conversation forward and switch to the target thread.
  // Placed here (after `activateThread` is destructured) to avoid TDZ.
  useEffect(() => {
    const api = window.wuu as Partial<typeof window.wuu>;
    if (typeof api.onCodexPetJumpRequest !== "function") return;
    return api.onCodexPetJumpRequest((event) => {
      revealConversationFromFocusedWorkspace();
      void activateThread(event.thread_id);
    });
  }, [activateThread, revealConversationFromFocusedWorkspace]);

  const {
    startNewThread,
    selectSessionTab,
  } = createSessionTabActions({
    getAppState: () => appStateRef.current,
    setAppState: setState,
    getPrimaryComposerDraft: currentPrimaryComposerDraft,
    restorePrimaryComposerDraft,
    clearPrimaryComposerDraft: () =>
      restorePrimaryComposerDraft(emptyComposerDraft()),
    resetSplitComposerDrafts: () =>
      setSplitComposerDrafts(initialSplitComposerDrafts()),
    getCrossWorkspaceThreads: () => sidebarThreads,
    getRunningThreadIDs: () => crossWorkdirRunningThreadIDs,
    nextDraftSessionTab,
    isDraftPending: (tabID) => pendingThreadCreationsRef.current.has(tabID),
    selectThread,
    beginViewSwitch,
    beginInstantThreadSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    loadRuntime,
    selectRuntimeContext,
  });

  function closePrimaryPluginView(): void {
    desktopWorkbenchController.deactivateRegion("primary");
  }

  function focusHeroAfter(
    action: Promise<void | boolean>,
    origin: Element | null,
    matchesDestination: (state: AppState) => boolean,
  ): void {
    const interactionVersion = userInteractionVersionRef.current;
    void action.then((succeeded) => {
      if (succeeded === false) {
        return;
      }
      requestMainComposerFocus(
        "hero",
        origin,
        interactionVersion,
        (current) =>
          !current.thread &&
          !current.secondaryThread &&
          activeSessionTab(current)?.kind === "draft" &&
          matchesDestination(current),
      );
    });
  }

  function startNewThreadWithComposerFocus(): void {
    const origin = document.activeElement;
    const context = appStateRef.current.activeContext;
    focusHeroAfter(
      startNewThread(),
      origin,
      (current) => sameRuntimeContext(current.activeContext, context),
    );
  }

  async function handoffActiveThread(input: { provider: string; model: string; effort?: string; intent: string }): Promise<void> {
    const currentState = appStateRef.current;
    const source = activeThreadForState(currentState);
    const activeContext = currentState.activeContext;
    if (!source || !activeContext) {
      showErrorToast(t("app.openConversationFirst"));
      return;
    }
    try {
      const thread = requireThread(
        await window.wuu.startThread({
          provider: input.provider,
          model: input.model,
          effort: input.effort,
          handoff: {
            request_id: crypto.randomUUID?.() ?? Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, "0")).join(""),
            revision: 1,
            parent_session_id: source.id,
            intent: input.intent,
          },
        }),
        "thread/start did not return a handoff session",
      );
      appStateRef.current = {
        ...setThreadForPane(appStateRef.current, "primary", thread),
        activePane: "primary",
        allowThreadAutoActivation: true,
        sessionTabs: bindActiveSessionTabToThread(
          appStateRef.current.sessionTabs,
          appStateRef.current.activeSessionTabID,
          thread,
          activeContext,
        ),
        activeSessionTabID: threadSessionTabID(thread.id),
        threads: upsertThread(appStateRef.current.threads, thread),
      };
      setState((current) => ({
        ...setThreadForPane(current, "primary", thread),
        activePane: "primary",
        allowThreadAutoActivation: true,
        sessionTabs: bindActiveSessionTabToThread(
          current.sessionTabs,
          current.activeSessionTabID,
          thread,
          activeContext,
        ),
        activeSessionTabID: threadSessionTabID(thread.id),
        threads: upsertThread(current.threads, thread),
      }));
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t("handoff.card.unavailable"));
    }
  }

  function trySkillFromCatalog(skill: { name: string }): void {
    const origin = document.activeElement;
    const context = appStateRef.current.activeContext;
    if (!context) {
      return;
    }
    revealConversationFromFocusedWorkspace();
    void startNewThread().then(() => {
      setComposerImages([]);
      setComposerFiles([]);
      setPrompt(`/${skill.name} `);
      requestMainComposerFocus("hero", origin);
    });
  }

  async function updateExtensionPackage(
    update: ExtensionPackageUpdateParams,
  ): Promise<void> {
    const context = appStateRef.current.activeContext;
    const result = await window.wuu.updateExtensionPackage(update);
    setState((current) => withExtensionInventoryForContext(current, context, result.extension_inventory));
  }

  async function refreshExtensionCatalog(): Promise<SkillSummary[] | undefined> {
    const context = appStateRef.current.activeContext;
    const result = await window.wuu.refreshExtensionCatalog();
    if (!sameRuntimeContext(appStateRef.current.activeContext, context)) {
      return undefined;
    }
    setState((current) => withExtensionInventoryForContext(current, context, result.extension_inventory));
    return result.skills;
  }

  async function installPluginPackage(): Promise<PluginPackageInstallResult | undefined> {
    const context = appStateRef.current.activeContext;
    const result = await window.wuu.installPluginPackage();
    if (!result || !sameRuntimeContext(appStateRef.current.activeContext, context)) {
      return undefined;
    }
    setState((current) =>
      withExtensionInventoryForContext(current, context, result.extension_inventory),
    );
    return result;
  }

  async function removePluginPackage(
    id: string,
  ): Promise<PluginPackageRemoveResult | undefined> {
    const context = appStateRef.current.activeContext;
    const result = await window.wuu.removePluginPackage(id);
    if (!sameRuntimeContext(appStateRef.current.activeContext, context)) {
      return undefined;
    }
    setState((current) =>
      withExtensionInventoryForContext(current, context, result.extension_inventory),
    );
    return result;
  }

  function startNewThreadInWorkspaceWithComposerFocus(id: string): void {
    const origin = document.activeElement;
    focusHeroAfter(
      id === SCRATCH_PSEUDO_PROJECT_ID
        ? useNoProject(true)
        : startNewThreadInWorkspace(id),
      origin,
      (current) =>
        id === SCRATCH_PSEUDO_PROJECT_ID
          ? current.activeContext?.kind === "no_project"
          : current.activeContext?.kind === "project" &&
            current.activeProjectId === id,
    );
  }

  const {
    toggleThreadPinned,
    renameThread,
    archiveThread,
    unarchiveThread,
    deleteThread,
  } = createThreadMutationActions({
    getAppState: () => appStateRef.current,
    setAppState: setState,
    getActiveThreadID: () => activeThreadID,
    nextDraftSessionTab,
    clearPrimaryComposerDraft: () =>
      restorePrimaryComposerDraft(emptyComposerDraft()),
    resetSplitComposerDrafts: () =>
      setSplitComposerDrafts(initialSplitComposerDrafts()),
    updateCachedSidebarThread,
    updateCachedSidebarThreadPinned,
    removeCachedSidebarThread,
    clearThreadPendingComposerMessages,
  });

  function commitConversationTitle(nextTitle: string): void {
    const trimmed = nextTitle.trim();
    if (!trimmed) return;
    const current = appStateRef.current;
    const thread = activeThreadForState(current);
    if (thread && !thread.read_only && !thread.ephemeral) {
      void renameThread(thread, trimmed);
      return;
    }
    const tab = activeSessionTab(current);
    if (tab?.kind !== "draft" || tab.title.trim() === trimmed) return;
    setState((state) => {
      const next = {
        ...state,
        sessionTabs: state.sessionTabs.map((item) =>
          item.id === tab.id && item.kind === "draft" ? { ...item, title: trimmed } : item,
        ),
      };
      appStateRef.current = next;
      return next;
    });
  }

  const {
    updateRuntimeSettings,
    updateProviderSettings,
    updateAdvancedSettings,
    updateGeneralSettings,
    removeProvider,
    toggleCodexRuntimeMenu,
    loadCodexModelsForProvider,
    selectRuntimeModel,
    selectRuntimeEffort,
    selectPermissionMode,
    interrupt,
    interruptPane,
  } = createRuntimeSettingsActions({
    getAppState: () => appStateRef.current,
    setAppState: setState,
    getViewContextSwitchPending: () => viewContextSwitchPending,
    getCodexModels: () => codexModels,
    setCodexModels,
    setRuntimeMenuOpen,
    setAccessMenuOpen,
    setBranchMenuOpen,
    setCodexRuntimeMenu,
    clearThreadPendingComposerMessages,
    requestThreadStop,
    variantByModel: runtimeVariantByModelRef.current,
  });

  const {
    openSkillsTab,
    dismissContextCompositionEntry,
    dismissInstructionFilesEntry,
    openInstructions,
    openContextComposition,
  } = createWorkspaceActions({
    getAppState: () => appStateRef.current,
    setAppState: setState,
    getActiveTitle: () => activeTitle,
    getPrimaryComposerDraft: currentPrimaryComposerDraft,
    setSplitComposerDrafts,
    setPrompt,
    setComposerImages,
    setComposerFiles,
    
    cancelViewSwitch,
    setContextCompositionEntries,
    setInstructionFilesEntries,
    scheduleStreamScroll,
    closeWorkspaceMenus,
    setSettingsInitialPage,
    setSettingsOpen,
  });

  const { activateConversationPane, closeConversationPane } = createConversationPaneActions({
    setAppState: setState,
    moveSplitDraftToGlobalComposer,
  });

  const {
    choosePendingFork,
    forkThreadFromMessage,
    startEditingThreadMessageFromHistory,
    cancelEditingThreadMessage,
    submitEditedThreadMessageFromHistory,
  } = createConversationHistoryActions({
    appStateRef,
    setAppState: setState,
    getPendingFork: () => pendingFork,
    setPendingFork,
    setHistoryMessageEdit,
    
    getPrompt: () => currentPrimaryComposerDraft().prompt,
    getComposerImages: () => composerImages,
    getComposerFiles: () => composerFiles,
    getSplitComposerDrafts: () => splitComposerDrafts,
    setPrompt,
    setComposerImages,
    setComposerFiles,
    setSplitComposerDrafts,
    restorePrimaryComposerDraft,
    closeConversationSearch,
    clearEnvironmentDialog: () => setEnvironmentDialog(null),
    scheduleGitStatusRefresh,
    disableConversationAutoFollow,
    enableConversationAutoFollow,
    rememberConversationScrollForEdit,
    restoreConversationScrollForEdit,
    threadHasPendingComposerMessages,
    sendComposerMessageToThread,
    worktreeForkNonGitReason: t("app.worktreeRequiresGit"),
  });

  function sendPrompt(
    runningAction: "queue" | "steer" = "queue",
    promptOverride?: string,
    contentParts?: MessageContentPart[],
    consumedOfferID?: string,
    pane?: ConversationPaneID,
  ): boolean {
    if (submissionTargetPending) {
      return false;
    }
    const draft = pane ? splitComposerDrafts[pane] : currentPrimaryComposerDraft();
    const draftMessage = createComposerMessage(
      promptOverride ?? draft.prompt,
      draft.images,
      draft.files,
      contentParts,
    );
    const activeDocumentPath = pane ? undefined : activeWorkspaceFile;
    const message =
      draftMessage && activeDocumentPath
        ? { ...draftMessage, activeDocument: { path: activeDocumentPath } }
        : draftMessage;
    const currentState = appStateRef.current;
    const targetPane = pane ?? currentState.activePane;
    const targetThread = threadForPane(currentState, targetPane);
    if (targetThread?.read_only) {
      setState((current) => ({
        ...current,
        status: localizedText("app.childTaskReadOnly"),
      }));
      return false;
    }
    if (
      !message || !currentState.activeContext || !currentState.initialized ||
      (pane && !targetThread) || (targetThread && stopRequestsRef.current[targetThread.id])
    ) {
      return false;
    }
    if (!targetThread && (draftEngine || engineInventory?.settings?.default_engine || "wuu") === "wuu"
      && !hasReadyProvider(currentState.initialized.providers)) {
      showNoModelConfiguredToast();
      return false;
    }
    if (consumedOfferID && pendingUserQuestionOffer?.request_id === consumedOfferID) {
      void cancelUserQuestion(consumedOfferID).catch(() => undefined);
    }
    let focusRequest: MainComposerFocusRequest | undefined;
    if (!pane && emptyConversation) {
      const activeElement = document.activeElement;
      if (
        activeElement === document.body ||
        activeElement?.closest("[data-main-conversation-composer]")
      ) {
        focusRequest = requestMainComposerFocus("dock", activeElement);
      }
    }
    // Capture the destination and install pending state before preparation yields.
    let submittedThread = targetThread;
    const submissionKey = targetThread?.id ?? currentState.activeSessionTabID;
    const admission = turnAdmissionsRef.current.get(submissionKey);
    const busy = targetThread && isThreadRunning(targetThread) && !activeTurnIsAnswerReady(targetThread);
    const operation = admission || (!busy && queueLanesRef.current.has(submissionKey))
      ? queueComposerMessage(message, targetThread, admission)
      : busy
      ? resolveComposerRunningAction(runningAction, targetThread) === "steer"
        ? steerComposerMessage(message, targetThread)
        : queueComposerMessage(message, targetThread)
      : sendComposerMessage(message, targetThread, targetPane, (thread) => { submittedThread = thread; });
    if (pane) {
      setSplitComposerDrafts((current) => ({ ...current, [pane]: emptyComposerDraft() }));
    } else {
      restorePrimaryComposerDraft(emptyComposerDraft());
    }
    const sessionTabID = currentState.activeSessionTabID;
    void operation.then((sent) => {
      if (sent) return;
      submittedThread ??= admission?.thread;
      const latest = appStateRef.current;
      const recoveryDraft = { prompt: message.text, images: message.images, files: message.files };
      const stillTarget = submittedThread
        ? threadForPane(latest, targetPane)?.id === submittedThread.id
        : latest.activeSessionTabID === sessionTabID;
      const latestDraft = pane ? composerDraftsRef.current.split[pane] : composerDraftsRef.current.primary();
      if (stillTarget && !composerDraftHasContent(latestDraft)) {
        if (pane) {
          setSplitComposerDrafts((current) => ({ ...current, [pane]: recoveryDraft }));
        } else {
          restorePrimaryComposerDraft(recoveryDraft);
        }
      } else if (submittedThread) {
        // Keep failed input in its conversation without replacing a newer draft.
        const failedTurn: Turn = {
          ...createOptimisticTurn(message, Date.now()),
          status: "failed",
          error: { message: t("composer.sendFailed") },
        };
        const threadID = submittedThread.id;
        const preserve = (state: AppState) => updateThreadByID(state, threadID, (thread) => upsertTurn(thread, failedTurn));
        appStateRef.current = preserve(appStateRef.current);
        setState(preserve);
      } else {
        // Thread creation failed before there was a conversation to retain the
        // input. A separate draft must not replace newer work in the source tab.
        const recoveryTab = createDraftSessionTab(`draft:recovery:${message.id}`, currentState.activeContext!, recoveryDraft);
        const preserve = (state: AppState): AppState => ({
          ...state,
          // Keep the newer tab as the workspace's default draft.
          sessionTabs: [recoveryTab, ...state.sessionTabs],
        });
        appStateRef.current = preserve(appStateRef.current);
        setState(preserve);
        setFailedDraftTabIDs((ids) => [...ids, recoveryTab.id]);
      }
      if (stillTarget && focusRequest) {
        cancelMainComposerFocusRequest(focusRequest);
        requestMainComposerFocus("hero", focusRequest.origin, focusRequest.interactionVersion);
      }
    });
    return true;
  }

  async function compactActiveThread(): Promise<void> {
    if (viewSwitchPending) {
      return;
    }
    const currentState = appStateRef.current;
    const targetThread = activeThreadForState(currentState);
    if (!currentState.activeContext || !currentState.initialized) {
      return;
    }
    if (!targetThread) {
      setState((current) => ({
        ...current,
        status: localizedText("app.openConversationFirst"),
      }));
      return;
    }
    if (targetThread.read_only) {
      setState((current) => ({
        ...current,
        status: localizedText("app.childTaskReadOnly"),
      }));
      return;
    }
    if (isStateActiveThreadRunning(currentState)) {
      setState((current) => ({
        ...current,
        status: localizedText("app.currentTaskRunning"),
      }));
      return;
    }

    enableConversationAutoFollow();
    appStateRef.current = {
      ...currentState,
      running: true,
      status: localizedText("app.compactingContext"),
    };
    setState((current) => ({
      ...current,
      running: true,
      status: localizedText("app.compactingContext"),
    }));

    const optimisticTurn = createOptimisticCompactTurn(Date.now());
    const optimisticTurnID = optimisticTurn.id;
    appStateRef.current = updateThreadByID(
      appStateRef.current,
      targetThread.id,
      (thread) => upsertTurn(thread, optimisticTurn),
      { running: true, status: localizedText("app.compactingContext") },
    );
    setState((current) =>
      updateThreadByID(
        current,
        targetThread.id,
        (thread) => upsertTurn(thread, optimisticTurn),
        { running: true, status: localizedText("app.compactingContext") },
      ),
    );

    try {
      const result = await window.wuu.compactThread(targetThread.id);
      appStateRef.current = updateThreadByID(
        appStateRef.current,
        targetThread.id,
        (thread) =>
          replaceOptimisticTurn(
            thread,
            optimisticTurnID,
            result.turn,
            upsertTurn,
          ),
        { running: true, status: localizedText("app.compactingContext") },
      );
      setState((current) =>
        updateThreadByID(
          current,
          targetThread.id,
          (thread) =>
            replaceOptimisticTurn(
              thread,
              optimisticTurnID,
              result.turn,
              upsertTurn,
            ),
          { running: true, status: localizedText("app.compactingContext") },
        ),
      );
    } catch (error) {
      const rawMessage = rawErrorMessage(error, t("composer.compactFailed"));
      const errorMessage = statusMessageForError(rawMessage, t("composer.compactFailed"));
      const failedTurn = failOptimisticCompactTurn(
        optimisticTurn,
        rawMessage,
        Date.now(),
      );
      appStateRef.current = {
        ...updateThreadByID(
          appStateRef.current,
          targetThread.id,
          (thread) =>
            replaceOptimisticTurn(
              thread,
              optimisticTurnID,
              failedTurn,
              upsertTurn,
            ),
        ),
        running: false,
        status: errorMessage,
      };
      setState((current) =>
        updateThreadByID(
          current,
          targetThread.id,
          (thread) =>
            replaceOptimisticTurn(
              thread,
              optimisticTurnID,
              failedTurn,
              upsertTurn,
            ),
          { running: false, status: errorMessage },
        ),
      );
    }
  }

  async function queueComposerMessage(
    message: QueuedComposerMessage,
    targetThread = activeThreadForState(appStateRef.current),
    admission = targetThread ? turnAdmissionsRef.current.get(targetThread.id) : undefined,
  ): Promise<boolean> {
    const currentState = appStateRef.current;
    const queueKey = targetThread?.id ?? currentState.activeSessionTabID;
    const pendingKey = () => admission?.thread?.id ?? queueKey;
    const text = message.text.trim();
    const imageCount = message.images.length;
    const files = inputFilesFromComposer(message.files);
    if (
      (!text && imageCount === 0 && files.length === 0) ||
      (!targetThread && !admission) ||
      targetThread?.read_only ||
      !currentState.activeContext ||
      !currentState.initialized ||
      submissionTargetPending
    ) {
      return false;
    }
    enqueueComposerMessage(queueKey, {
      ...message,
      operationState: "preparing",
    });
    const lane = queueLanesRef.current.get(queueKey) ?? { tail: Promise.resolve(), stopVersion: 0 };
    // Stop holds messages already waiting; later user sends keep their order
    // behind them without inheriting that earlier stop request.
    const stopVersion = lane.stopVersion;
    const previous = lane.tail;
    let release!: () => void;
    const tail = new Promise<void>((resolve) => { release = resolve; });
    lane.tail = tail;
    queueLanesRef.current.set(queueKey, lane);
    try {
      await previous;
      if (admission) targetThread = await admission.ready;
      if (!targetThread) {
        const stillPending = Boolean(pendingComposerMessagesByThreadRef.current[pendingKey()]?.queued.some(
          (candidate) => candidate.id === message.id,
        ));
        removePendingComposerMessageByID(pendingKey(), message.id, "queue");
        return !stillPending;
      }
      const targetContext = resolveThreadRuntimeContext(targetThread, currentState.projects);
      const encodedImages = await awaitComposerImages(message.images);
      if (
        !pendingComposerMessagesByThreadRef.current[targetThread.id]?.queued.some(
          (candidate) => candidate.id === message.id,
        )
      ) {
        return true;
      }
      updateThreadPendingComposerMessages(targetThread.id, (previous) => ({
        ...previous,
        queued: previous.queued.map((candidate) =>
          candidate.id === message.id
            ? { ...candidate, operationState: "sending" }
            : candidate,
        ),
      }));
      const images = inputImagesFromComposer(encodedImages);
      const result = await window.wuu.queueTurn(
        targetThread.id,
        text,
        images,
        message.id,
        files,
        targetThread.permission_mode,
        message.activeDocument,
        message.contentParts,
        targetContext,
        lane.stopVersion !== stopVersion || Boolean(admission?.stopRequested),
      );
      updateThreadPendingComposerMessages(targetThread.id, (previous) => ({
        ...previous,
        queued: previous.queued.map((candidate) =>
          candidate.id === message.id
            ? {
                ...candidate,
                id: result.queued.id || message.id,
                images: encodedImages,
                operationState: undefined,
              }
            : candidate,
        ),
      }));
      return true;
    } catch (error) {
      const stillPending = Boolean(
        pendingComposerMessagesByThreadRef.current[pendingKey()]?.queued.some(
          (candidate) => candidate.id === message.id,
        ),
      );
      removePendingComposerMessageByID(pendingKey(), message.id, "queue");
      if (stillPending) discardSubmittedMessage(message.id);
      if (stillPending) {
        setState((current) => ({
          ...current,
          status:
            activeThreadIDForState(current) === pendingKey()
              ? error instanceof Error ? error.message : t("app.queueFailed")
              : current.status,
        }));
      }
      return !stillPending;
    } finally {
      release();
      if (lane.tail === tail) {
        queueLanesRef.current.delete(queueKey);
        if (admission?.thread) queueLanesRef.current.delete(admission.thread.id);
      }
    }
  }

  async function steerComposerMessage(
    message: QueuedComposerMessage,
    targetThread = activeThreadForState(appStateRef.current),
  ): Promise<boolean> {
    const currentState = appStateRef.current;
    const targetContext = targetThread ? resolveThreadRuntimeContext(targetThread, currentState.projects) : undefined;
    const text = message.text.trim();
    const files = inputFilesFromComposer(message.files);
    const turnID = targetThread ? activeTurnIDForThread(targetThread) : undefined;
    if (
      (!text && message.images.length === 0 && files.length === 0) ||
      !targetThread ||
      targetThread.read_only ||
      !turnID ||
      !currentState.activeContext ||
      !currentState.initialized ||
      submissionTargetPending
    ) {
      return false;
    }
    updateThreadPendingComposerMessages(targetThread.id, (previous) => ({
      ...previous,
      guides: [
        ...previous.guides,
        { ...message, origin: "steer", operationState: "preparing" },
      ],
    }));
    try {
      const encodedImages = await awaitComposerImages(message.images);
      if (
        !pendingComposerMessagesByThreadRef.current[targetThread.id]?.guides.some(
          (candidate) => candidate.id === message.id,
        )
      ) {
        return true;
      }
      updateThreadPendingComposerMessages(targetThread.id, (previous) => ({
        ...previous,
        guides: previous.guides.map((candidate) =>
          candidate.id === message.id
            ? { ...candidate, operationState: "sending" }
            : candidate,
        ),
      }));
      await window.wuu.steerTurn(
        targetThread.id,
        turnID,
        text,
        inputImagesFromComposer(encodedImages),
        message.id,
        files,
        message.activeDocument,
        message.contentParts,
        targetContext,
      );
      updateThreadPendingComposerMessages(targetThread.id, (previous) => ({
        ...previous,
        guides: previous.guides.map((candidate) =>
          candidate.id === message.id
            ? { ...candidate, images: encodedImages, operationState: undefined }
            : candidate,
        ),
      }));
      return true;
    } catch (error) {
      const stillPending = Boolean(
        pendingComposerMessagesByThreadRef.current[targetThread.id]?.guides.some(
          (candidate) => candidate.id === message.id,
        ),
      );
      removePendingComposerMessageByID(targetThread.id, message.id, "guide");
      if (stillPending) discardSubmittedMessage(message.id);
      if (stillPending) {
        setState((current) => ({
          ...current,
          status:
            activeThreadIDForState(current) === targetThread.id
              ? error instanceof Error ? error.message : t("composer.guideFailed")
              : current.status,
        }));
      }
      return !stillPending;
    }
  }

  async function sendComposerMessage(
    message: QueuedComposerMessage,
    targetThread = activeThreadForState(appStateRef.current),
    targetPane = appStateRef.current.activePane,
    onThreadCreated?: (thread: Thread) => void,
  ): Promise<boolean> {
    // Captured before any await: on a brand-new conversation the thread
    // itself is created over IPC first, and the optimistic turn's live
    // timer must count from the user's click, not from when that
    // round-trip finishes.
    const sendClickedAtMs = Date.now();
    const currentState = appStateRef.current;
    const text = message.text.trim();
    const imageCount = message.images.length;
    const files = inputFilesFromComposer(message.files);
    if (
      (!text && imageCount === 0 && files.length === 0) ||
      !currentState.activeContext ||
      !currentState.initialized ||
      targetThread?.read_only ||
      submissionTargetPending ||
      (targetThread && isThreadRunning(targetThread) &&
        !activeTurnIsAnswerReady(targetThread))
    ) {
      return false;
    }
    const activeContext = targetThread
      ? resolveThreadRuntimeContext(targetThread, currentState.projects)
      : currentState.activeContext;
    const newThreadEngine =
      (targetThread?.engine_id ?? "")
      || draftEngine
      || engineInventory?.settings?.default_engine
      || "wuu";
    if (!targetThread && newThreadEngine === "wuu" && !hasReadyProvider(currentState.initialized?.providers)) {
      showNoModelConfiguredToast();
      return false;
    }
    const defaultExternalRuntime = defaultEngineRuntimeSelection(
      engineInventory?.engines.find((engine) => engine.id === newThreadEngine),
    );
    const newThreadEngineRuntime = {
      model: draftEngineRuntime.model || defaultExternalRuntime.model,
      effort: draftEngineRuntime.effort || defaultExternalRuntime.effort,
    };
    let resolveAdmission!: TurnAdmission["resolve"];
    let cancelPreparation!: () => void;
    const admission: TurnAdmission = {
      thread: targetThread,
      ready: new Promise((resolve) => { resolveAdmission = resolve; }),
      resolve: (thread) => resolveAdmission(thread),
      stopRequested: false,
      sent: false,
      cancelled: new Promise((resolve) => { cancelPreparation = () => resolve(undefined); }),
      cancelPreparation: () => cancelPreparation(),
    };
    const admissionKey = targetThread?.id ?? currentState.activeSessionTabID;
    turnAdmissionsRef.current.set(admissionKey, admission);
    const optimisticTurn = createOptimisticTurn(message, sendClickedAtMs);
    const previousTurnIDs = new Set(targetThread?.turns.map((turn) => turn.id));
    if (!targetThread || activeThreadIDForState(currentState) === targetThread.id) {
      requestSubmittedQueryScroll(optimisticTurn.items[0].id);
      appStateRef.current = { ...currentState, running: true, status: localizedText("app.sendingRequest") };
      setState((current) => ({ ...current, running: true, status: localizedText("app.sendingRequest") }));
    }
    let optimisticTurnID: string | undefined;
    let optimisticThreadID: string | undefined;
    // Creation belongs to the draft, independently of background list refreshes.
    let creationCancelled = false;
    const cancelledCreation = !targetThread ? new Promise<never>((_, reject) => {
      pendingThreadCreationsRef.current.set(currentState.activeSessionTabID, {
        sessionTabID: currentState.activeSessionTabID,
        context: activeContext,
        turn: optimisticTurn,
        cancel: () => {
          creationCancelled = true;
          admission.stopRequested = true;
          reject(new Error("Thread creation cancelled"));
        },
      });
      setPendingThreadCreations([...pendingThreadCreationsRef.current.values()]);
    }) : undefined;
    try {
      let thread =
        targetThread ??
        requireThread(
          await Promise.race([window.wuu.startThread({
            ...(draftEngine ? { engine: draftEngine } : {}),
            ...(newThreadEngine !== "wuu"
              ? {
                  ...newThreadEngineRuntime,
                  permission_mode: draftPermissionMode || "unconfined",
                } satisfies ThreadStartParams
              : {
                  provider: currentState.initialized?.provider,
                  model: currentState.initialized?.model,
                  effort: currentState.initialized?.variant || currentState.initialized?.effort,
                  permission_mode: currentState.initialized?.permissions?.mode,
                  approve_for_me: currentState.initialized?.permissions?.approve_for_me,
                } satisfies ThreadStartParams),
          }, activeContext).then(async (result) => {
            if (creationCancelled && result.thread) {
              // No turn was submitted to this newly created session. A late
              // response must not leave an empty conversation after Stop.
              try {
                const threadID = result.thread.id;
                await window.wuu.deleteThread(threadID);
                removeCachedSidebarThread(threadID);
                setState((current) => ({
                  ...current,
                  threads: current.threads.filter((thread) => thread.id !== threadID),
                }));
              } catch (error) {
                showErrorToast(error);
              }
            }
            return result;
          }), cancelledCreation!]),
          "thread/start did not return a thread",
        );
      if (!targetThread && !creationCancelled) {
        const draftTab = currentState.sessionTabs.find(
          (tab) => tab.id === currentState.activeSessionTabID,
        );
        const customTitle = customDraftConversationTitle(
          draftTab?.kind === "draft" ? draftTab.title : undefined,
          t("tabs.newConversation"),
        );
        // Set the user's name before the first turn so a generated title
        // cannot replace a name they already chose.
        if (customTitle) {
          try {
            const renamed = await window.wuu.renameThread(thread.id, customTitle);
            if (renamed.thread) {
              thread = renamed.thread;
              updateCachedSidebarThread(thread);
            }
          } catch (error) {
            showErrorToast(error, t("thread.rename.failed"));
          }
        }
      }
      admission.thread = thread;
      if (!targetThread) {
        turnAdmissionsRef.current.set(thread.id, admission);
        turnAdmissionsRef.current.delete(admissionKey);
        const lane = queueLanesRef.current.get(admissionKey);
        if (lane) {
          queueLanesRef.current.set(thread.id, lane);
          queueLanesRef.current.delete(admissionKey);
        }
        const pending = pendingComposerMessagesByThreadRef.current[admissionKey];
        if (pending) {
          updateThreadPendingComposerMessages(thread.id, (previous) => ({
            ...previous, queued: [...previous.queued, ...pending.queued],
          }));
          clearThreadPendingComposerMessages(admissionKey);
        }
        onThreadCreated?.(thread);
        const adoptThread = (current: AppState): AppState => {
          const stillTarget = current.activeSessionTabID === currentState.activeSessionTabID
            && sameRuntimeContext(current.activeContext, activeContext);
          return {
            ...(stillTarget ? setThreadForPane(current, targetPane, thread) : current),
            sessionTabs: bindActiveSessionTabToThread(current.sessionTabs, currentState.activeSessionTabID, thread, activeContext),
            activeSessionTabID: stillTarget ? threadSessionTabID(thread.id) : current.activeSessionTabID,
            threads: upsertThread(current.threads, thread),
          };
        };
        appStateRef.current = adoptThread(appStateRef.current);
        setState(adoptThread);
      }
      optimisticTurnID = optimisticTurn.id;
      optimisticThreadID = thread.id;
      appStateRef.current = updateThreadByID(
        appStateRef.current,
        thread.id,
        (currentThread) => upsertTurn(currentThread, optimisticTurn),
      );
      setState((current) =>
        updateThreadByID(
          current,
          thread.id,
          (currentThread) => upsertTurn(currentThread, optimisticTurn),
        ),
      );
      clearPendingThreadCreation(optimisticTurn.id);
      const encodedImages = await Promise.race([awaitComposerImages(message.images), admission.cancelled]);
      if (!encodedImages || admission.stopRequested) {
        const settle = (current: AppState) => updateThreadByID(current, thread.id,
          (value) => interruptOptimisticTurn(value, optimisticTurn.id, Date.now()),
          activeThreadIDForState(current) === thread.id ? { running: false } : {});
        appStateRef.current = settle(appStateRef.current);
        setState(settle);
        return true;
      }
      admission.sent = true;
      const images = inputImagesFromComposer(encodedImages);
      const result = await window.wuu.startTurn(
        thread.id,
        text,
        images,
        files,
        thread.permission_mode || (!targetThread
          ? currentState.initialized.permissions?.mode : undefined),
        message.activeDocument,
        message.contentParts,
        activeContext,
        message.id,
      );
      const acceptedTurn = result.turn;
      const acceptedMessage = acceptedTurn.items.find(item => item.type === "user_message");
      if (acceptedMessage) acknowledgeSubmittedMessage(optimisticTurn.items[0].id, acceptedMessage.id);
      const accept = (current: AppState) => updateThreadByID(
        current, thread.id,
        (currentThread) => replaceOptimisticTurn(currentThread, optimisticTurn.id, acceptedTurn, upsertTurn),
      );
      appStateRef.current = accept(appStateRef.current);
      setState(accept);
      if (admission.stopRequested && isThreadRunning(threadForTab(appStateRef.current, thread.id))) {
        await interruptAcceptedThread(thread.id);
      }
    } catch (error) {
      admission.stopRequested = true;
      const rawMessage = rawErrorMessage(error, t("composer.sendFailed"));
      const errorMessage = statusMessageForError(rawMessage, t("composer.sendFailed"));
      const noModelConfigured = isNoModelConfiguredError(rawMessage);
      const currentThread = appStateRef.current.threads.find(
        (candidate) => candidate.id === optimisticThreadID,
      );
      const interrupted =
        isCancellationMessage(rawMessage.toLowerCase()) ||
        isOptimisticTurnInterrupted(currentThread, optimisticTurnID);
      const alreadyAccepted = threadHasAcceptedComposerMessage(
        currentThread,
        message,
        optimisticTurnID,
        previousTurnIDs,
      );
      const keepAcceptedTurn = alreadyAccepted && !interrupted;
      if (!interrupted && !keepAcceptedTurn) discardSubmittedMessage(optimisticTurn.items[0].id);
      const settle = (current: AppState): AppState => {
        const next = optimisticTurnID && optimisticThreadID
          ? updateThreadByID(
              current,
              optimisticThreadID,
              (currentThread) =>
                interrupted
                  ? interruptOptimisticTurn(currentThread, optimisticTurnID, Date.now())
                  : keepAcceptedTurn
                    ? currentThread
                    : dropOptimisticTurn(currentThread, optimisticTurnID),
            )
          : current;
        const stillTarget = optimisticThreadID
          ? activeThreadIDForState(current) === optimisticThreadID
          : current.activeSessionTabID === currentState.activeSessionTabID;
        return stillTarget ? {
          ...next,
          running: keepAcceptedTurn,
          status: noModelConfigured || interrupted || keepAcceptedTurn ? "" : errorMessage,
        } : next;
      };
      appStateRef.current = settle(appStateRef.current);
      setState(settle);
      clearPendingThreadCreation(optimisticTurn.id);
      if (!optimisticThreadID) forgetLocalTurnTiming(optimisticTurn.id);
      if (noModelConfigured) {
        showNoModelConfiguredToast();
      }
      if (admission.sent && optimisticThreadID && stopRequestsRef.current[optimisticThreadID]) {
        await interruptAcceptedThread(optimisticThreadID!);
      }
      return !creationCancelled && (interrupted || keepAcceptedTurn);
    } finally {
      admission.resolve(admission.thread);
      turnAdmissionsRef.current.delete(admissionKey);
      if (admission.thread) {
        turnAdmissionsRef.current.delete(admission.thread.id);
        // Re-evaluate a locally cancelled preparation with no server event.
        setStopRequests({ ...stopRequestsRef.current });
      }
    }
    return true;
  }

  async function sendComposerMessageToThread(
    message: QueuedComposerMessage,
    targetThread: Thread,
  ): Promise<boolean> {
    return sendComposerMessage(message, targetThread);
  }

  const failedDraftTabID = failedDraftTabIDs[0];
  const failedDraftNotice = failedDraftTabID ? (
    <UILayerPortal layer="notice">
      <TopNotice
        key={failedDraftTabID}
        message={t("composer.failedDraftSaved")}
        icon={CircleAlert}
        isError
        persistent
        action={{ label: t("composer.openFailedDraft"), onClick: () => { void selectSessionTab(failedDraftTabID); } }}
        dismissAriaLabel={t("composer.discardFailedDraft")}
        onDismiss={() => {
          setFailedDraftTabIDs((ids) => ids.filter((id) => id !== failedDraftTabID));
          setState((current) => ({ ...current, sessionTabs: current.sessionTabs.filter((tab) => tab.id !== failedDraftTabID) }));
        }}
      />
    </UILayerPortal>
  ) : null;

  const archiveTipNode = archiveTip ? (
    <UILayerPortal layer="notice">
      <ArchiveTip
        threadTitle={archiveTip.threadTitle}
        errorMessage={archiveTip.errorMessage}
        onViewArchive={() => {
          dismissArchiveTip();
          openArchiveSettings();
        }}
        onForceArchive={
          archiveTip.forceRetryThread
            ? () => {
                const target = archiveTip.forceRetryThread;
                if (!target) {
                  dismissArchiveTip();
                  return;
                }
                void archiveThread(target, { force: true }).then((outcome) => {
                  setArchiveTip({
                    threadID: target.id,
                    threadTitle:
                      target.title?.trim() || t("app.thisConversation"),
                    errorMessage: outcome.ok ? undefined : outcome.error,
                    forceRetryThread:
                      !outcome.ok && outcome.forceRetryable ? target : undefined,
                  });
                });
              }
            : undefined
        }
        onDismiss={dismissArchiveTip}
      />
    </UILayerPortal>
  ) : null;

  const modelCatalogTipNode = modelCatalogTip ? (
    <UILayerPortal layer="notice">
      <TopNotice
        message={modelCatalogTip.message}
        icon={modelCatalogTip.isError ? CircleAlert : RefreshCw}
        onDismiss={dismissModelCatalogTip}
        isError={modelCatalogTip.isError}
        dismissAriaLabel={t("common.closeNotice")}
      />
    </UILayerPortal>
  ) : null;

  useEffect(() => {
    const back = (event: Event): void => {
      if (accountOpen) { event.preventDefault(); setAccountOpen(false); }
      else if (settingsOpen) { event.preventDefault(); setSettingsOpen(false); }
      else if (sidebarDrawerVisible) { event.preventDefault(); closeSidebarDrawer(); }
      else if (rightPanelOpen) { event.preventDefault(); setRightPanelOpenWithMotion(false); }
    };
    window.addEventListener("wuu:workbench-back", back);
    return () => window.removeEventListener("wuu:workbench-back", back);
  }, [accountOpen, settingsOpen, sidebarDrawerVisible, closeSidebarDrawer, rightPanelOpen, setRightPanelOpenWithMotion]);

  if (ENABLE_ACCOUNT && accountOpen && window.wuu?.remoteAccount) {
    return <AccountScreen driver={window.wuu.remoteAccount} onBack={() => setAccountOpen(false)} />;
  }

  if (settingsOpen) {
    return (
      <>
        {archiveTipNode}
        {modelCatalogTipNode}
        {!archiveTip && !modelCatalogTip && failedDraftNotice}
        <SettingsShellRenderer
          initialized={state.initialized}
          initialPage={settingsInitialPage}
          running={viewContextSwitchPending}
          runningProviderNames={runningProviderNames}
          usage={settingsUsage}
          usageLoading={settingsUsageLoading}
          usageError={settingsUsageError}
          engineInventory={engineInventory}
          engineInventoryError={engineInventoryError}
          codexPets={codexPets}
          codexPetsLoading={codexPetsLoading}
          codexPetsError={codexPetsError}
          sidebarWidth={sidebarWidth}
          resizingSidebar={resizingSidebar}
          shellRef={settingsShellRef}
          // The settings rail reuses the main sidebar's state and handlers
          // wholesale — same persisted width, same collapse flag, same
          // drag-to-collapse resize session, same toggle motion — so both
          // shells behave identically. The drawer controller runs
          // independently inside the settings shell (its own ref + phase)
          // so settings navigation can stay visible until pointer exit.
          sidebarCollapsed={sidebarCollapsed}
          sidebarAnimating={sidebarAnimating}
          onToggleSidebar={toggleSidebar}
          onBack={() => {
            setSettingsOpen(false);
          }}
          onSave={updateProviderSettings}
          onRemoveProvider={removeProvider}
          onRefreshModelCatalog={refreshModelCatalog}
          onRefreshEngineInventory={() => refreshEngineInventory(true)}
          onUpdateEngineInventory={updateEngineInventory}
          onAdvancedSave={updateAdvancedSettings}
          onGeneralSave={updateGeneralSettings}
          onCodexPetsRefresh={refreshCodexPets}
          onCodexPetsUpdate={updateCodexPets}
          onSidebarResizeStart={startSidebarResize}
          onSidebarSeparatorKey={handleSidebarSeparatorKey}
          archivedThreads={state.threads
            .filter((thread) => thread.archived)
            .map((thread) => {
              const project = state.projects.find((candidate) =>
                threadBelongsToWorkspace(thread, candidate),
              );
              return {
                ...thread,
                archive_project_id: project?.id ?? "",
                archive_project_name: project?.name ?? t("appState.noWorkspace"),
              };
            })}
          onUnarchiveThread={(thread) => void unarchiveThread(thread)}
        />
      </>
    );
  }

  if (!onboardingComplete) {
    return (
      <WuuMascotRuntimeProvider>
        <FirstRunOnboarding
          inventory={state.initialized?.extension_inventory}
          providers={state.initialized?.providers}
          engines={engineInventory}
          onUpdateExtensionPackage={updateExtensionPackage}
          onSaveProvider={async (provider, model, connection) => {
            await updateRuntimeSettings(provider, model, undefined, connection, undefined);
          }}
          onUpdateEngines={updateEngineInventory}
          onComplete={async () => {
            if (!window.wuu?.completeOnboarding) {
              throw new Error(t("onboarding.finishFailed"));
            }
            await window.wuu.completeOnboarding();
            setOnboardingComplete(true);
          }}
        />
      </WuuMascotRuntimeProvider>
    );
  }

  const conversationTitleEditable =
    !showingSkillsCatalog &&
    !showingPrimaryPluginView &&
    ((activeThread !== undefined && !activeThread.read_only && !activeThread.ephemeral) ||
      currentSessionTab?.kind === "draft");

  return (
    <WuuMascotRuntimeProvider
      provider={mascotRuntimePreview?.provider ?? sessionRuntime?.provider}
      providers={mascotProviderNames}
      model={mascotRuntimePreview?.model ?? sessionRuntime?.model}
    >
      {archiveTipNode}
      {modelCatalogTipNode}
      {!archiveTip && !modelCatalogTip && failedDraftNotice}
      <ImagePreviewProvider>
      <WorkspaceBrowserOpenContext.Provider value={poppedOutMode || isTouchWebShell() ? undefined : openWorkspaceBrowserURL}>
      <ArtifactPreviewContext.Provider value={poppedOutMode || isTouchWebShell() ? undefined : openWorkspaceArtifactTab}>
        <div
          ref={appShellRef}
          className={shellClassName}
          style={shellStyle}
          data-wuu-component="app-shell"
          data-wuu-sidebar-mode={sidebarDrawerVisible ? "drawer" : sidebarDrawerMode ? "collapsed" : "docked"}
        >
          <BrowserPiPHostReporter />
          {!poppedOutMode ? (
            <>
          <div
            ref={sidebarHoverZoneRef}
            className="sidebar-hover-zone"
            aria-hidden="true"
            onPointerEnter={scheduleSidebarDrawerOpen}
            onPointerLeave={cancelSidebarDrawerOpen}
          />
          {rightPanelGlobalized && sidebarDrawerMode && sidebarToggleVisible ? (
            <div className="globalized-sidebar-toggle-region">
              <button
                className="icon-button side-panel-toggle-button sidebar-toggle-button globalized-sidebar-toggle"
                data-wuu-component="sidebar-toggle"
                type="button"
                aria-label={t(
                  sidebarDrawerVisible
                    ? "app.collapseLeftSidebar"
                    : "app.expandLeftSidebar",
                )}
                aria-pressed={sidebarDrawerVisible}
                onClick={sidebarDrawerVisible ? closeSidebarDrawer : openSidebarDrawerNow}
                onPointerEnter={scheduleSidebarDrawerOpen}
                onPointerLeave={(event) =>
                  scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent)
                }
              >
                <SidePanelToggleIcon side="left" open={sidebarDrawerVisible} />
              </button>
            </div>
          ) : null}
          <AppSidebar
            onToggleSidebar={sidebarDrawerMode ? undefined : toggleSessionSwitcher}
            sidebarCollapsed={sidebarCollapsed}
            sidebarVisible={!sidebarDrawerMode || sidebarDrawerVisible}
            mobileNavigation={compactNavigation && isTouchWebShell()}
            drawerVisible={sidebarDrawerVisible}
            onNavigateAway={closeCompactSessionSwitcher}
            state={state}
            sidebarWorkspaces={sidebarWorkspaces}
            pendingConversations={pendingThreadCreations.map((pending) => ({
              id: pending.sessionTabID,
              context: pending.context,
              title: pending.turn.items[0].text || t("tabs.newConversation"),
            }))}
            onSelectPendingConversation={(tabID) => {
              closePrimaryPluginView();
              closeCompactSessionSwitcher();
              void selectSessionTab(tabID);
            }}
            activeWorkspaceID={
              workspaceSelectionEnabled && workspaceContext?.kind === "project"
                ? workspaceContext.project_id
                : undefined
            }
            pinnedThreads={sidebarPinnedThreads}
            activeThreadID={activeThreadID}
            pendingThreadID={visiblePendingThreadID}
            pendingWorkspaceID={visiblePendingWorkspaceID}
            collapsedSidebarSectionIDs={collapsedSidebarSectionIDs}
            collapsedFolderIDs={collapsedFolderIDs}
            setCollapsedFolderIDs={setCollapsedFolderIDs}
            expandedSidebarSectionIDs={expandedSidebarSectionIDs}
            loadingWorkspaceThreadIDs={loadingWorkspaceThreadIDs}
            workspaceThreadsByWorkspaceID={sidebarThreadsByWorkspaceID}
            workspaceMenuOpen={workspaceMenuOpen}
            workspaceMenuRef={workspaceMenuRef}
            searchOpen={conversationSearch.open}
            sectionOrder={sidebarSectionOrder}
            onStartNewThread={() => {
              closePrimaryPluginView();
              revealConversationFromFocusedWorkspace();
              closeCompactSessionSwitcher();
              startNewThreadWithComposerFocus();
            }}
            onOpenSkillsTab={() => {
              closePrimaryPluginView();
              closeCompactSessionSwitcher();
              openSkillsTab();
            }}
            onMarkThreadsViewed={(threads) => {
              setState((current) => markThreadSummariesViewed(current, threads));
            }}
            unreadViewOpen={unreadViewOpen}
            onToggleUnreadView={() => setUnreadViewOpen((open) => !open)}
            attentionStickyIDs={attentionStickyIDs}
            onAttentionStickyIDsChange={setAttentionStickyIDs}
            onToggleConversationSearch={toggleConversationSearch}
            onSelectThread={(id) => {
              closePrimaryPluginView();
              revealConversationFromFocusedWorkspace();
              closeCompactSessionSwitcher();
              void activateThread(id);
            }}
            onTogglePinned={(thread) => void toggleThreadPinned(thread)}
            onArchiveThread={(thread) => {
              const archivedTitle =
                thread.title?.trim() || t("app.thisConversation");
              void archiveThread(thread).then((outcome) => {
                setArchiveTip({
                  threadID: thread.id,
                  threadTitle: archivedTitle,
                  errorMessage: outcome.ok ? undefined : outcome.error,
                  forceRetryThread:
                    !outcome.ok && outcome.forceRetryable ? thread : undefined,
                });
              });
            }}
            onDeleteThread={(thread) => void deleteThread(thread)}
            onRenameThread={(thread, title) => void renameThread(thread, title)}
            onToggleWorkspaceMenu={() => setWorkspaceMenuOpen((open) => !open)}
            onCreateWorkspace={() => void createBlankProject()}
            onOpenWorkspaceFolder={() => void chooseProjectFolder()}
            onToggleSidebarSectionCollapsed={toggleSidebarSectionCollapsed}
            onFocusWorkspace={
              workspaceSelectionEnabled
                ? (id) => {
                    const project = state.projects.find((item) => item.id === id);
                    if (!project || project.missing) {
                      return;
                    }
                    closePrimaryPluginView();
                    setFocusedWorkspaceContext({
                      kind: "project",
                      project_id: project.id,
                      cwd: project.path,
                    });
                    closeCompactSessionSwitcher();
                    openWorkspaceTool("files");
                  }
                : undefined
            }
            onStartNewThreadInWorkspace={(id) => {
              closePrimaryPluginView();
              revealConversationFromFocusedWorkspace();
              closeCompactSessionSwitcher();
              startNewThreadInWorkspaceWithComposerFocus(id);
            }}
            onSelectWorkspaceThread={(workspaceID, threadID) => {
              closePrimaryPluginView();
              revealConversationFromFocusedWorkspace();
              closeCompactSessionSwitcher();
              void selectWorkspaceThread(workspaceID, threadID);
            }}
            onRemoveWorkspace={(id) => void removeProject(id)}
            onRelocateWorkspace={(id) => void relocateProject(id)}
            onReorderSections={setSidebarSectionOrder}
            onPointerEnter={openSidebarDrawer}
            onPointerLeave={(event) =>
              scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent)
            }
            onOpenAccount={ENABLE_ACCOUNT ? () => {
                if (window.wuu.openAccountWindow) void window.wuu.openAccountWindow().catch(error => showErrorToast(error));
                else setAccountOpen(true);
              } : undefined}
            onOpenSettings={(page = "providers") => {
              setWorkspaceMenuOpen(false);
              setRuntimeMenuOpen(false);
              setCodexRuntimeMenu(null);
              setSettingsInitialPage(page);
              setSettingsOpen(true);
            }}
          />

          {compactNavigation ? (
            <button
              className="compact-session-switcher-backdrop"
              type="button"
              aria-label={t("app.collapseLeftSidebar")}
              onClick={closeSidebarDrawer}
            />
          ) : null}

          {sidebarDrawerMode ? null : (
            <div
              className="sidebar-resizer"
              inert={rightPanelOpen && rightPanelGlobalized}
              role="separator"
              aria-label={t("app.resizeSidebar")}
              aria-orientation="vertical"
              aria-valuemin={SIDEBAR_MIN_WIDTH}
              aria-valuemax={SIDEBAR_MAX_WIDTH}
              aria-valuenow={sidebarWidth}
              tabIndex={0}
              onPointerDown={startSidebarResize}
              onDoubleClick={toggleSidebar}
              onKeyDown={handleSidebarSeparatorKey}
            />
          )}
      <ConversationSearchOverlay
        state={conversationSearch}
        results={conversationSearchResults}
        threads={userVisibleThreads(state.threads)}
        projects={state.projects}
        activeThreadID={activeThreadID}
        pendingThreadID={visiblePendingThreadID}
        dialogRef={conversationSearchRef}
        inputRef={conversationSearchInputRef}
        onClose={closeConversationSearch}
        onQueryChange={setConversationSearchQuery}
        onClearQuery={clearConversationSearchQuery}
        onKeyDown={handleConversationSearchKeyDown}
        onSelectIndex={setConversationSearchSelectedIndex}
        onSelectResult={(result) => {
          closePrimaryPluginView();
          return selectConversationSearchResult(result);
        }}
      />
            </>
          ) : null}

      <main
        inert={rightPanelOpen && rightPanelGlobalized}
        data-wuu-component="conversation-pane"
        data-primary-plugin-view={showingPrimaryPluginView ? "" : undefined}
        data-composer-navigation={composerNavigation || undefined}
        className={`conversation-pane${environmentPanelVisible ? " environment-panel-visible" : ""}${
          environmentPanelReserved ? " environment-panel-reserved" : ""
        }${
          sideThreadPanelVisible ? " side-thread-panel-visible" : ""
        }`}
        ref={conversationPaneRef}
      >
        {composerNavigation ? <div aria-hidden="true" /> : (
        <header className="titlebar" data-wuu-component="conversation-titlebar">
          <div className="title-block">
            {sidebarToggleVisible && sidebarDrawerMode && !rightPanelGlobalized ? (
              <button
                className="icon-button side-panel-toggle-button sidebar-toggle-button sidebar-collapse-toggle"
                data-wuu-component="sidebar-toggle"
                type="button"
                aria-label={t(
                  sidebarDrawerVisible
                    ? "app.collapseLeftSidebar"
                    : "app.expandLeftSidebar",
                )}
                aria-pressed={sidebarDrawerVisible}
                onClick={toggleSessionSwitcher}
                onPointerEnter={scheduleSidebarDrawerOpen}
                onPointerLeave={(event) =>
                  scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent)
                }
              >
                <SidePanelToggleIcon side="left" open={sidebarDrawerVisible} />
              </button>
            ) : null}
            <ConversationTitleContent
              state={state}
              runningThreadIDs={visibleRunningThreadIDs}
              pendingSwitchThreadID={visiblePendingThreadID}
              activeTitle={activeTitle}
              onStartNewThread={startNewThreadWithComposerFocus}
              onRenameTitle={conversationTitleEditable ? commitConversationTitle : undefined}
              titleEditKey={activeThread?.id ?? currentSessionTab?.id}
            />
          </div>
          <ConversationTitleActions
            state={state}
            compactNavigation={compactNavigation}
            onStartNewThread={startNewThreadWithComposerFocus}
            environmentToggleRef={environmentToggleRef}
            environmentPanelVisible={environmentPanelVisible}
            onToggleEnvironmentPanel={toggleEnvironmentPanel}
            rightPanelOpen={rightPanelOpen}
            onToggleRightPanel={toggleRightPanel}
          />
        </header>

        )}
        {/* Unmount the hidden rail so compact scrolling does not measure turns
            or update navigation state for controls that cannot be used. */}
        {ENABLE_CONVERSATION_TURN_RAIL && !compactNavigation ? (
          <ConversationTurnRail
            turns={turns}
            activeTurnID={turns[turns.length - 1]?.id}
            scrollContainerRef={conversationScrollRef}
            getScrollContainer={conversationRailScrollContainer}
            onWheelScrollAway={disableConversationAutoFollow}
            onDragScrollAway={disableConversationAutoFollow}
            onSelectQueryHistory={handleQueryHistorySelect}
          />
        ) : null}

        <ConversationSidePanels
          state={state}
          environmentPanelVisible={environmentPanelVisible}
          environmentPanelMounted={environmentPanelMounted}
          environmentPanelRef={environmentPanelRef}
          environmentPanelClosing={environmentPanelClosing}
          environmentPanelMotionState={environmentPanelMotionState}
          activeTodoUpdate={activeTodoUpdate}
          environmentPanelMenu={environmentPanelMenu}
          environmentGitBusy={environmentGitBusy}
          pullRequestDisabledReason={pullRequestDisabledReason}
          onSetEnvironmentPanelMenu={setEnvironmentPanelMenu}
          onCloseEnvironmentPanel={() =>
            closeEnvironmentPanel({ dismissed: true })
          }
          onSelectBranch={async (branch) => {
            try {
              await checkoutBranch(branch);
            } catch (error) {
              showErrorToast(error, t("git.checkoutFailed"));
            }
          }}
          onCreateBranch={(branch) => createAndCheckoutBranch(branch)}
          onOpenReview={() => {
            openWorkspaceTool("review");
            closeEnvironmentPanel({ dismissed: true });
          }}
          onOpenCommit={() => openEnvironmentDialog("commit")}
          onOpenPullRequest={() => openEnvironmentDialog("pull-request")}
          rightPanelFilePath={rightPanelFilePath}
          onCloseFilePreview={handleCloseFilePreview}
          switchLoadingVisible={pendingViewSwitch?.visible === true}
        />

        {sideThreadPanelVisible && activeThreadID && sideThread.entry ? (
          <SideThreadPanel
            ref={sideThreadPanelRef}
            entry={sideThread.entry}
            mainThreadId={activeThreadID}
            width={sideThread.width}
            cwd={activeThread?.cwd ?? state.activeContext?.cwd}
            onOpenFile={openWorkspaceFile}
            composer={
              <SideThreadComposer
                draft={sideThread.entry.draft}
                running={sideThread.entry.streaming}
                disabledReason={sideThread.sendDisabledReason}
                queryHistorySessionID={
                  sideThread.entry.summary?.side_thread_id ?? `side:${activeThreadID}`
                }
                queryHistory={sideThread.entry.messages
                  .filter((message) => message.role === "user")
                  .map((message) => message.text)}
                onChangeDraft={sideThread.setDraft}
                onSend={sideThread.sendMessage}
                onInterrupt={sideThread.interrupt}
                onReset={sideThread.reset}
              />
            }
            onClose={sideThread.close}
            onResizeStart={sideThread.startResize}
            onChangeDraft={sideThread.setDraft}
          />
        ) : null}

        {state.initialized ? (
          <div
            data-pip-anchor-host="conversation"
            className={`scroll-region${emptyConversation ? " empty-scroll-region" : ""}${
              splitConversation ? " split-scroll-region" : ""
            }${showingManagementCatalog ? " skills-scroll-region" : ""}`}
            inert={showingPrimaryPluginView}
            onScroll={(event) => handleConversationScroll(event.currentTarget)}
            ref={conversationScrollRef}
          >
            <div ref={scrollContentRef} className="scroll-region-content">
              {showingSkillsCatalog ? (
              <SkillsCatalog
                activeContext={state.activeContext}
                extensionInventory={state.initialized?.extension_inventory}
                onTrySkill={trySkillFromCatalog}
                onRefreshCatalog={refreshExtensionCatalog}
                onUpdateExtensionPackage={updateExtensionPackage}
                onInstallPluginPackage={installPluginPackage}
                onRemovePluginPackage={removePluginPackage}
              />
            ) : (
              <>
                {!activeThreadReadOnly ? (
                  <QueryHistoryRail
                    entries={pastQueries}
                    maxBars={QUERY_HISTORY_RAIL_MAX_BARS}
                    active={queryHistoryOpen}
                    railRef={queryHistoryRailRef}
                    onHoverStart={openQueryHistory}
                    onHoverEnd={scheduleQueryHistoryClose}
                  />
                ) : null}
                {splitConversation && state.thread && state.secondaryThread ? (
                  <ConversationSplitLayoutRenderer
                    state={state}
                    primaryThread={state.thread}
                    secondaryThread={state.secondaryThread}
                    splitLeftPercent={splitLeftPercent}
                    splitComposerDrafts={splitComposerDrafts}
                    splitPaneRefs={splitPaneRefs}
                    stopRequests={stopRequests}
                    viewSwitchPending={submissionTargetPending}
                    historyMessageEdit={historyMessageEdit}
                    onSplitResizeStart={startSplitResize}
                    onSplitSeparatorDoubleClick={resetSplitPercent}
                    onSplitSeparatorKey={handleSplitSeparatorKey}
                    onActivatePane={activateConversationPane}
                    onClosePane={closeConversationPane}
                    onConversationScroll={handleConversationScroll}
                    onSetPrompt={setSplitComposerPrompt}
                    onPasteAttachmentFiles={(pane, files) =>
                      void attachSplitComposerAttachmentFiles(pane, files)
                    }
                    onRemoveFile={removeSplitComposerFile}
                    onRemoveImage={removeSplitComposerImage}
                    onSend={(pane, promptOverride, contentParts) =>
                      sendPrompt("queue", promptOverride, contentParts, undefined, pane)
                    }
                    onInterrupt={(pane) => void interruptPane(pane)}
                    onForkMessage={(thread, turnID, itemID) =>
                      void forkThreadFromMessage(thread, turnID, itemID)
                    }
                    onOpenFile={openWorkspaceFileForThread}
                    onOpenURL={openWorkspaceBrowserURL}
                    onOpenAgent={(agent) => void selectChildAgent(agent)}
                    canEditThreadMessage={canShowHistoryEditButton}
                    onEditMessage={startEditingThreadMessageFromHistory}
                    onCancelEditMessage={cancelEditingThreadMessage}
                    onSubmitEditMessage={(
                      thread,
                      turnID,
                      item,
                      text,
                      images,
                      files,
                      contentParts,
                      pane,
                    ) =>
                      submitEditedThreadMessageFromHistory(
                        thread,
                        turnID,
                        item,
                        text,
                        images,
                        files,
                        contentParts,
                        pane,
                      )
                    }
                    onStreamFrame={scheduleStreamScroll}
                    onOpenFileDiff={openTurnFileDiffPanel}
                    pendingUserQuestion={pendingUserQuestion}
                    onAnswerUserQuestion={answerUserQuestion}
                    onCancelUserQuestion={cancelUserQuestion}
                  />
                ) : activePendingNewThreadTurn ? (
                  <div className="conversation-width session-flow">
                    <TurnView
                      turn={activePendingNewThreadTurn}
                      cwd={state.activeContext?.cwd}
                      onStreamFrame={scheduleStreamScroll}
                      isLatestTurn
                    />
                  </div>
                ) : emptyConversation ? (
              showingPrimaryPluginView ? null : (
              <EmptyConversationHome
                title={emptyThreadTitle}
                // A draft lowers the greeting mascot’s gaze toward the composer.
                activity={
                  prompt.trim().length > 0 || composerImages.length > 0 || composerFiles.length > 0
                    ? "compose"
                    : "idle"
                }
              >
                <EmptyHomeOverview />
              </EmptyConversationHome>
              )
            ) : (
              <CachedConversationPanes
                threadIDs={cachedThreadPaneIDs}
                threadsByID={cachedConversationThreadsByID}
                activeThreadID={activeThreadID}
                activeContextCwd={state.activeContext?.cwd}
                contextCompositionEntries={contextCompositionEntries}
                instructionFilesEntries={instructionFilesEntries}
                historyMessageEdit={historyMessageEdit}
                onStreamFrame={scheduleStreamScroll}
                onCollapseComplete={handleTurnCollapseComplete}
                onDismissContextComposition={
                  handleCachedPaneDismissContextComposition
                }
                onDismissInstructions={handleCachedPaneDismissInstructions}
                canEditThreadMessage={canEditCachedThreadMessage}
                onForkMessage={handleCachedPaneForkMessage}
                onOpenFile={openWorkspaceFileForThread}
                onOpenURL={openWorkspaceBrowserURL}
                onOpenAgent={handleCachedPaneOpenAgent}
                onEditMessage={handleCachedPaneEditMessage}
                onCancelEditMessage={handleCachedPaneCancelEditMessage}
                onSubmitEditMessage={handleCachedPaneSubmitEditMessage}
                turnStreamStatus={state.turnStreamStatus}
                onOpenFileDiff={handleCachedPaneOpenFileDiff}
                pendingUserQuestion={pendingUserQuestion}
                onAnswerUserQuestion={answerUserQuestion}
                onCancelUserQuestion={cancelUserQuestion}
              />
            )}
              </>
            )}
            </div>
          </div>
        ) : (
          <RuntimeLoading
            status={resolveLocalizedText(state.status)}
          />
        )}

        {compactNavigation && isTouchWebShell() && mainConversationDockVisible &&
        !emptyConversation && !splitConversation && !showingManagementCatalog &&
        activeThreadID && state.activeContext ? (
          <PullToNewSession
            key={activeThreadID}
            containerRef={conversationScrollRef}
            contentRef={scrollContentRef}
            bottomAnchor={dockComposerNode}
            onNewSession={startNewThreadWithComposerFocus}
          />
        ) : null}
        {mainConversationDockVisible ? renderComposer("dock") : null}


        <ConversationStatusCluster
          host={desktopPluginHost}
          visible={mainConversationDockVisible}
          clusterRef={statusClusterRef}
          navigation={!emptyConversation ? (
            <JumpToLatestPill
              containerRef={conversationScrollRef}
              bottomAnchor={dockComposerNode}
              scopeKey={activeThreadID}
              inline
            />
          ) : null}
          threadId={activeThreadID}
          todoUpdate={activeTodoUpdateForThread(activeThread)}
          onOpenSession={handleOpenThreadInSplit}
        />
      </main>

      <>
      {!poppedOutMode && (rightPanelOpen || rightPanelAnimating) ? (
        <div
          className="workspace-right-panel-resizer"
          inert={rightPanelGlobalized}
          role="separator"
          aria-label={t("app.resizeRightSidebar")}
          aria-orientation="vertical"
          aria-valuemin={WORKSPACE_RIGHT_PANEL_MIN_WIDTH}
          aria-valuemax={WORKSPACE_RIGHT_PANEL_MAX_WIDTH}
          aria-valuenow={clampedWorkspaceRightPanelWidth}
          tabIndex={0}
          onPointerDown={startRightPanelResize}
          onDoubleClick={resetWorkspaceRightPanelWidth}
          onKeyDown={handleRightPanelSeparatorKey}
        />
      ) : null}
      {poppedOutMode ? null : (
        <WorkspaceRightPanel
          compactNavigation={compactNavigation}
          open={rightPanelOpen}
          present={rightPanelOpen || rightPanelAnimating}
          prewarm={Boolean(state.initialized)}
          tabs={workspaceViewTabs}
          activeTabID={workspaceActiveViewTabID}
          activeFileTabID={activeWorkspaceFileTabID}
          activeContext={state.activeContext}
          workspaceContext={workspaceContext}
          terminalThread={activeThread}
          gitStatus={state.gitStatus}
          selectedFilePath={activeWorkspaceFile}
          onSelectTab={focusWorkspaceViewTab}
          onOpenTool={openWorkspaceTool}
          onOpenPluginTool={openWorkspacePluginTool}
          onShowTools={showWorkspaceToolPicker}
          onCloseTab={closeWorkspaceViewTab}
          onDirtyFileTabsChange={rememberWorkspaceDirtyFiles}
          onReorderTabs={reorderWorkspaceViewTabs}
          onOpenFile={openWorkspaceFile}
          onClose={() => setRightPanelOpenWithMotion(false)}
          globalized={rightPanelGlobalized}
          sheetPhase={workspaceSheetPhase}
          onToggleGlobalize={toggleWorkspacePanelGlobalized}
          canExitGlobalized={
            !rightPanelAutoGlobalized ||
            workspaceRightPanelDockableWithoutSidebar
          }
          browserActivity={activeBrowserActivity}
          browserDockTarget={browserDockTarget}
          browserOverlaySuppressed={browserOverlaySuppressed}
          onBrowserUserInteraction={pauseBrowserTask}
          focusedComposer={
            rightPanelGlobalized && activeWorkspaceFileTabID
              ? (
                  <WorkspaceDocumentTurnDock
                    key={activeThreadID ?? state.activeSessionTabID}
                    cwd={activeThread?.cwd ?? state.activeContext?.cwd}
                    onOpenFile={openWorkspaceFile}
                    waitingQuery={
                      activeThreadIsRunning
                        ? firstUserMessageText(activeTurnForThread(activeThread))
                        : undefined
                    }
                    turns={
                      activePendingNewThreadTurn
                        ? [...turns, activePendingNewThreadTurn]
                        : turns
                    }
                  >
                    {renderComposer("document")}
                  </WorkspaceDocumentTurnDock>
                )
              : undefined
          }
          fileRefreshKey={
            activeThreadIsRunning ? "running" : activeThread?.updated_at
          }
          pluginHost={desktopPluginHost}
          workbenchController={desktopWorkbenchController}
        />
      )}
      </>

      {environmentDialog === "commit" ? (
        <CommitChangesDialog
          gitStatus={state.gitStatus}
          branch={state.gitStatus?.branch}
          onCancel={() => setEnvironmentDialog(null)}
          onCommit={commitEnvironmentChanges}
          onGenerateMessage={generateEnvironmentCommitMessage}
        />
      ) : null}
      {environmentDialog === "pull-request" ? (
        <PullRequestDialog
          gitStatus={state.gitStatus}
          disabledReason={pullRequestDisabledReason}
          onCancel={() => setEnvironmentDialog(null)}
          onCreate={createEnvironmentPullRequest}
        />
      ) : null}
      {pendingFork ? (
        <ConversationForkDialog
          worktreeDisabledReason={forkWorktreeDisabledReason}
          onCancel={() => setPendingFork(undefined)}
          onChoose={choosePendingFork}
        />
      ) : null}
      {queryHistoryOpen &&
      !activeThreadReadOnly &&
      pastQueries.length > 0 ? (
        <FloatingMenuPortal
          anchorRef={queryHistoryRailRef}
          owner="composer-query-history"
          placement="middle"
          align="right"
          crossAxisOffset={-8}
          width={ENVIRONMENT_PANEL_WIDTH_PX}
        >
          <div
            onMouseEnter={cancelQueryHistoryClose}
            onMouseLeave={scheduleQueryHistoryClose}
            style={{
              width: `min(${ENVIRONMENT_PANEL_WIDTH_CSS}, calc(100vw - 32px))`,
            }}
          >
            <QueryHistoryPopover
              entries={pastQueries}
              onSelect={handleQueryHistorySelect}
            />
          </div>
        </FloatingMenuPortal>
      ) : null}
      <DesktopWorkbench
        host={desktopPluginHost}
        controller={desktopWorkbenchController}
        inventory={state.initialized?.extension_inventory}
        services={{
          getSetting: async (pluginId, generation, key) => {
            if (!window.wuu?.getPluginSetting) throw new Error("Plugin settings service is unavailable");
            return (await window.wuu.getPluginSetting({ id: pluginId, fingerprint: generation, key })).value;
          },
          getStorage: async (pluginId, generation, key, scope) => {
            if (!window.wuu?.getPluginStorage) throw new Error("Plugin storage service is unavailable");
            return (await window.wuu.getPluginStorage({ id: pluginId, fingerprint: generation, key, scope })).value;
          },
          setStorage: async (pluginId, generation, key, value, scope) => {
            if (!window.wuu?.setPluginStorage) throw new Error("Plugin storage service is unavailable");
            await window.wuu.setPluginStorage({ id: pluginId, fingerprint: generation, key, value, scope });
          },
          openSettings: () => {
            setSettingsInitialPage("providers");
            setSettingsOpen(true);
          },
          disablePlugin: async (pluginId) => {
            await updateExtensionPackage({ id: pluginId, action: "disable" });
          },
          reportError: (pluginId, generation, error) => {
            console.error(`Plugin view ${pluginId}@${generation} failed to render`, error);
          },
          requestRegionVisible: (region) => {
            if (region === "primary" && rightPanelGlobalized) setRightPanelOpenWithMotion(false);
            if (region === "auxiliary") setRightPanelOpenWithMotion(true);
          },
        }}
      />
      </div>
    </ArtifactPreviewContext.Provider>
    </WorkspaceBrowserOpenContext.Provider>
    </ImagePreviewProvider>
    </WuuMascotRuntimeProvider>
  );
}
