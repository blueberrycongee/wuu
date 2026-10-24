import type { ApplicationSettingsLayoutV1 } from "../../../packages/plugin-sdk/src/application-settings";
import type { ApplicationServiceV1, ApplicationSnapshotV1, UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";
import { AccountScreen } from "./AccountScreen";
import { focusComposerTextarea, isTouchWebShell } from "./ComposerFocus";
import { FileSelectionProvider } from "./FileSelectionContext";
import { usePageHistory } from "./PageHistory";
import { PageHistoryControls } from "./PageHistoryControls";
import { SideConversationAvailableContext } from "./ResponseSelection";
import { SettingsControlsProvider } from "./SettingsControlsContext";
/// <reference path="../shared/jsx-compat.d.ts" />

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type CSSProperties
} from "react";
import { type PageContextSnapshot } from "../shared/pageContext";
import type {
ResponseSelection,
RuntimeContext,
SideThreadSelection,
Thread
} from "../shared/protocol";
import { WEB_WORKSPACE_RIBBON_ID, WEB_WORKSPACE_TAB_LIMIT } from "../shared/webWorkspace";
import { observeAppearance } from "./AppearancePreferences";
import {
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  SIDEBAR_RIBBON_WIDTH,
  sidebarDrawerExitMs,
  sidebarMotionMs,
  useAppLayoutState,
  WORKSPACE_RIGHT_PANEL_MAX_WIDTH,
  WORKSPACE_RIGHT_PANEL_MIN_WIDTH
} from "./AppLayoutState";
import { ApplicationFirstRunOnboarding } from "./ApplicationFirstRunOnboarding";
import {
  isThreadRunning,
  latestTodoUpdateForThread,
  runtimeContextKey,
  sameRuntimeContext,
  workspacePanelContext
} from "./AppState";
import { useArtifactAutoPreview } from "./ArtifactAutoPreview";
import { ArtifactPreviewContext } from "./ArtifactPreviewContext";
import { BrowserPiPHostReporter } from "./BrowserPiPHostReporter";
import { isForegroundControlled } from "./BrowserVisibility";
import { createComposerControls } from "./ComposerControls";
import {
  isInsideFloatingMenu,
  type ComposerVariant
} from "./ComposerView";
import { ContextChatCapsule } from "./ContextChatCapsule";
import type { ContextCompositionEntry } from "./ContextCompositionCard";
import {
  useConversationScrollState
} from "./ConversationScrollState";
import { useConversationSearchNavigation } from "./ConversationSearchNavigation";
import { ConversationSearchOverlay } from "./ConversationSearchOverlay";
import { useConversationSearch } from "./ConversationSearchState";
import {
  ConversationSidePanels,
  ConversationTitleActions,
  ConversationTitleContent
} from "./ConversationShellRenderers";
import { setOpenThreadInSplitHandler } from "./ConversationSplitBridge";
import { ConversationStatusCluster } from "./ConversationStatusCluster";
import { useConversationSwitchReady } from "./ConversationSwitchReady";
import { createEnvironmentPanelActions } from "./EnvironmentActions";
import {
  type EnvironmentPanelMenu,
  type EnvironmentPanelMotionState,
} from "./EnvironmentPanel";
import {
  ENABLE_ACCOUNT,
  ENABLE_EMBEDDED_BROWSER,
  ENABLE_ENVIRONMENT_PANEL,
} from "./FeatureFlags";
import { CommitChangesDialog, PullRequestDialog } from "./GitDialogs";
import { HoverRevealScopeContext } from "./HoverReveal";
import { resolveLocalizedText, translateCurrent, useI18n } from "./i18n";
import { ImagePreviewProvider } from "./ImagePreview";
import { JumpToLatestPill } from "./JumpToLatestPill";
import { useApplicationNavigationState } from "./modules/ApplicationNavigationState";
import { motionDurationMs } from "./motion";
import { useNavigationRibbonPreferences } from "./NavigationRibbonPreferences";
import { ProjectActionsProvider } from "./ProjectActions";
import { isProjectCoordinator, PROJECT_SESSION_SOURCE, projectSessionsOf } from "./ProjectSessions";
import { ProjectStatusCapsule } from "./ProjectViews";
import {
  pullRequestUnavailableReason
} from "./RuntimeHelpers";
import type { SettingsPage } from "./SettingsView";
import { useSidebarDrawerState } from "./SidebarDrawerState";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";
import { SkillsCatalog, type SkillsCatalogSection } from "./SkillsCatalog";
import {
  baseThreadTitle,
  conversationHeadingTitle
} from "./ThreadTitles";
import { showErrorToast, showToast } from "./Toast";
import { TopNotice } from "./TopNotice";
import { CONTINUE_TURN_EVENT, OPEN_SETTINGS_EVENT, type ContinueTurnDetail, type OpenSettingsDetail } from "./TurnNotice";
import { UILayerPortal } from "./ui/layers/UILayerHost";
import { WebWorkspacePanel } from "./WebWorkspace";
import { releaseWindowResizeClass, WINDOW_RESIZING_CLASS } from "./WindowResizeState";
import { requestWorkspaceBrowserNavigation } from "./WorkspaceBrowserNavigation";
import {
  openWorkspaceBrowserOrExternal,
  workspaceBrowserFocusDecision,
  WorkspaceBrowserOpenContext,
  type WorkspaceBrowserOpenTarget,
} from "./WorkspaceBrowserOpen";
import { useWorkspaceToolState } from "./WorkspaceToolState";
import type { WorkspaceViewTab } from "./WorkspaceViewTabs";
import { CircleAlert } from "./WuuIcons";
import { WuuMascotRuntimeProvider } from "./WuuMascot";
export { SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS } from "./SidebarDrawerState";

const ENGINE_INVENTORY_STALE_MS = 2 * 60 * 1000;
// Globalized-sheet phases: docked (grid child) → arming (promoted to a
// full-window fixed sheet, teleported over its dock slot for one frame) →
// open (slid to cover the window) → exiting (sliding back to park) →
// docking (teleported back into the grid for one frame, no transition) →
// docked. Transitions retarget mid-flight, so rapid toggles stay continuous.
type WorkspaceSheetPhase = "docked" | "arming" | "open" | "exiting" | "docking";
const ENVIRONMENT_PANEL_WIDTH_PX = 280;
// The panel's width grows with the UI text size, like
// --environment-panel-width in conversation-shell.css; the reserved column
// adds the gap to the conversation.
const ENVIRONMENT_PANEL_WIDTH_PER_UI_PX = ENVIRONMENT_PANEL_WIDTH_PX / 14.5;
const ENVIRONMENT_PANEL_FLOW_GAP_PX = 44;
// The info panel docks beside the conversation only while the conversation
// pane keeps a readable column next to it: the reserved width, two 32px page
// insets and a 480px column, the width at which the composer's controls stop
// fitting. The window alone cannot decide this, because the sidebar and the
// right panel take their share first. A narrower pane shows it as an overlay.
const ENVIRONMENT_PANEL_ROOM_MIN_HEIGHT_PX = 680;

function environmentPanelFits(paneWidth: number): boolean {
  const uiSize = parseFloat(getComputedStyle(document.documentElement).fontSize) || 14.5;
  const panelWidth = Math.max(
    ENVIRONMENT_PANEL_WIDTH_PX,
    Math.ceil((uiSize * ENVIRONMENT_PANEL_WIDTH_PER_UI_PX) / 4) * 4,
  );
  return paneWidth >= panelWidth + ENVIRONMENT_PANEL_FLOW_GAP_PX + 2 * 32 + 480 &&
    window.innerHeight >= ENVIRONMENT_PANEL_ROOM_MIN_HEIGHT_PX;
}
type EnvironmentDialog = "commit" | "pull-request" | null;

type EngineRuntimeSelection = { model: string; effort: string; speed?: string; model_options?: Record<string, string> };

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


type MainComposerFocusRequest = {
  target: ComposerVariant;
  origin: Element | null;
  interactionVersion: number;
  matchesDestination?: (snapshot: ApplicationSnapshotV1) => boolean;
};

export function App({ application, slots, shellPluginId }: {
  application: ApplicationServiceV1;
  slots: UICompositionV1;
  shellPluginId: string;
}): JSX.Element {
  const { t } = useI18n();
  const popOutInit = application.desktop.popOut.kind && application.desktop.popOut.context ? application.desktop.popOut : null;
  const poppedOutMode = Boolean(popOutInit?.kind && popOutInit.context);
  const recoveryNotices = useSyncExternalStore(application.subscribe, () => application.getSnapshot().drafts.recoveryNotices);
  const workspaceSnapshot = useSyncExternalStore(application.subscribe, () => application.getSnapshot().workspaces);
  const tabs = useSyncExternalStore(application.subscribe, () => application.getSnapshot().tabs);
  const configuration = useSyncExternalStore(application.subscribe, () => application.getSnapshot().settings.configuration);
  const initialized = useSyncExternalStore(application.subscribe, () => application.getSnapshot().navigationWorkspace.initialized);
  const environmentSnapshot = useSyncExternalStore(application.environment.subscribe, application.environment.getSnapshot);
  const gitStatus = environmentSnapshot.status;
  const activeContext = useMemo<RuntimeContext | undefined>(() => workspaceSnapshot.activeRoot
    ? workspaceSnapshot.activeWorkspaceId
      ? { kind: "project", project_id: workspaceSnapshot.activeWorkspaceId, cwd: workspaceSnapshot.activeRoot }
      : { kind: "no_project", cwd: workspaceSnapshot.activeRoot }
    : undefined, [workspaceSnapshot.activeRoot, workspaceSnapshot.activeWorkspaceId]);
  const projectAgentEnabled = configuration?.features?.project_agent === true;
  useEffect(() => {
    const addSelection = (event: Event) => {
      const selection = (event as CustomEvent<ResponseSelection>).detail;
      const snapshot = application.getSnapshot();
      const pane = snapshot.conversationWorkspace.primaryThreadId === selection.source.thread_id ? "primary"
        : snapshot.conversationWorkspace.secondaryThreadId === selection.source.thread_id ? "secondary" : undefined;
      if (!pane || snapshot.conversationWorkspace.threads.find(thread => thread.id === selection.source.thread_id)?.read_only) return;
      const draft = snapshot.drafts[pane];
      if (!draft.ownerTabId) return;
      void createComposerControls(application, { ownerTabId: draft.ownerTabId, pane })
        .setSelections([...draft.selections, selection]).then(() => {
          const owner = snapshot.drafts.split
            ? [...document.querySelectorAll<HTMLElement>(".conversation-split-pane[data-thread-id]")].find(element => element.dataset.threadId === selection.source.thread_id)
            : document.querySelector<HTMLElement>("[data-main-conversation-composer]");
          focusComposerTextarea(owner?.querySelector<HTMLTextAreaElement>("textarea") ?? null);
        }).catch(showErrorToast);
    };
    window.addEventListener("wuu:add-response-selection", addSelection);
    return () => window.removeEventListener("wuu:add-response-selection", addSelection);
  }, [application]);
  // Conversation notices share the latest settings navigation callback.
  useEffect(() => {
    const open = (event: Event): void => openSettingsPage((event as CustomEvent<OpenSettingsDetail>).detail.page);
    window.addEventListener(OPEN_SETTINGS_EVENT, open);
    return () => window.removeEventListener(OPEN_SETTINGS_EVENT, open);
  }, []);
  const conversationActions = useSyncExternalStore(application.subscribe, () => application.getSnapshot().conversationActions);
  const submissionView = useSyncExternalStore(application.subscribe, () => application.getSnapshot().submission);
  const conversationWorkspace = useSyncExternalStore(application.subscribe, () => application.getSnapshot().conversationWorkspace);
  const primarySource = conversationWorkspace.threads.find(thread => thread.id === conversationWorkspace.primaryThreadId);
  const secondarySource = conversationWorkspace.threads.find(thread => thread.id === conversationWorkspace.secondaryThreadId);
  // These display helpers read frozen snapshots; mutations use application actions.
  const primaryThread = primarySource as Thread | undefined;
  const secondaryThread = secondarySource as Thread | undefined;
  const activeThread = conversationWorkspace.activePane === "secondary" ? secondaryThread : primaryThread;
  const composerRuntimeView = useSyncExternalStore(application.subscribe, () => application.getSnapshot().composerRuntime);
  useEffect(() => {
    const ownerTabId = composerRuntimeView.activeOwnerTabId;
    if (!ownerTabId) return;
    const refresh = () => { void application.invoke("composer.refresh-engine-models", { ownerTabId }).catch(showErrorToast); };
    refresh();
    window.addEventListener("focus", refresh);
    return () => window.removeEventListener("focus", refresh);
  }, [application, composerRuntimeView.activeOwnerTabId]);
  const activities = useSyncExternalStore(application.subscribe, () => application.getSnapshot().activities);
  const appShellRef = useRef<HTMLDivElement>(null);
  const settingsShellRef = useRef<HTMLDivElement>(null);
  const settingsOpen = useSyncExternalStore(application.subscribe, () => application.getSnapshot().settings.open);
  const openSettingsPage = (page: SettingsPage): void => {
    void application.invoke("settings.open", { pageId: page }).catch(showErrorToast);
  };
  const setSettingsOpen = useCallback((open: boolean): void => {
    void application.invoke(open ? "settings.open" : "settings.close", {}).catch(showErrorToast);
  }, [application]);
  const ribbonWidth = useNavigationRibbonPreferences().width ?? SIDEBAR_RIBBON_WIDTH;
  const [nativeRibbonPresent, setNativeRibbonPresent] = useState(false);
  const [nativeRibbonExpanded, setNativeRibbonExpanded] = useState(false);
  const handleRibbonExpandedChange = useCallback((expanded: boolean) => {
    const shell = appShellRef.current;
    if (expanded && application.desktop.platform === "darwin"
      && shell?.querySelector(".section-sidebar-toggle") === document.activeElement) {
      // Release owned focus before hidden/inert can blur the covered toggle.
      // Focusing a ribbon item here would turn hover into retained keyboard focus.
      shell.focus({ preventScroll: true });
    }
    setNativeRibbonExpanded(expanded);
  }, []);
  const embeddedSettings = !poppedOutMode && nativeRibbonPresent;
  const [mainComposerFocusRequest, setMainComposerFocusRequest] =
    useState<MainComposerFocusRequest | null>(null);
  const userInteractionVersionRef = useRef(0);
  const {
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
    trackSidebarResizerPointer,
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
    collapsedSidebarWidth: SIDEBAR_RIBBON_WIDTH,
    additionalNavigationWidth: ribbonWidth - SIDEBAR_RIBBON_WIDTH,
    // The selected navigation surface owns its menus.
    onCloseWorkspaceMenu: () => {},
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
      // An exiting sheet is already promoted. Retarget its live transform;
      // arming would disable the transition and teleport it back to the dock.
      setWorkspaceSheetPhase(workspaceSheetPhase === "exiting" ? "open" : "arming");
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
    let raf = requestAnimationFrame(() => {
      raf = requestAnimationFrame(() => {
        setWorkspaceSheetPhase((current) => (current === from ? next : current));
      });
    });
    return () => cancelAnimationFrame(raf);
  }, [workspaceSheetPhase]);
  // The bell view is a sidebar-level navigation mode rather than sidebar-local
  // state so section changes and the account page retain the view the user left.
  // A manually expanded workspace owns the main stage, not the navigation
  // rail. Keep a docked sidebar docked; only an already-collapsed sidebar
  // remains a drawer while the workspace is expanded.
  const sidebarDrawerMode = sidebarCollapsed;
  const {
    sidebarDrawerPhase,
    sidebarHoverScope,
    sidebarHoverZoneRef,
    cancelSidebarDrawerOpen,
    openSidebarDrawer,
    openSidebarDrawerNow,
    scheduleSidebarDrawerOpen,
    closeSidebarDrawer,
    scheduleSidebarDrawerCloseFromPointerLeave,
  } = useSidebarDrawerState({
    appShellRef,
    sidebarSelector: settingsOpen ? ".settings-sidebar" : ".sidebar",
    sidebarCollapsed: sidebarDrawerMode && !poppedOutMode && (!settingsOpen || embeddedSettings),
    resizingSidebar,
    motionMs: sidebarDrawerExitMs,
    dockingMotionMs: sidebarMotionMs,
  });
  const sidebarDrawerVisible = sidebarDrawerPhase === "open";
  const nativeSection = useSyncExternalStore(application.subscribe, () => application.getSnapshot().navigation.section);
  const setNativeSection = useCallback((section: "conversations" | "catalog") => {
    void application.invoke("navigation.show-section", { section }).catch(showErrorToast);
  }, [application]);
  const navigationWorkspace = useSyncExternalStore(application.subscribe, () => application.getSnapshot().navigationWorkspace);
  const ribbonBrowser = { state: navigationWorkspace.browser };
  const activeWebsiteID = navigationWorkspace.activeWebsiteId;
  const setActiveWebsiteID = useCallback((id: string | undefined): void => {
    void application.invoke(id ? "navigation.reveal-browser" : "navigation.show-native", {}).catch(showErrorToast);
  }, [application]);
  const activeWebsite = ribbonBrowser.state?.tabs?.find((website) => website.id === ribbonBrowser.state?.activeTabId);
  const showingWebsite = Boolean(activeWebsiteID && activeWebsite);
  useEffect(() => {
    if (ribbonBrowser.state && !ribbonBrowser.state.tabs?.length) setActiveWebsiteID(undefined);
  }, [ribbonBrowser.state]);
  const catalogSection = useSyncExternalStore(application.subscribe, () => application.getSnapshot().navigation.catalogSection);
  const catalogPluginID = useSyncExternalStore(application.subscribe, () => application.getSnapshot().navigation.catalogPluginId);
  const setCatalogSection = useCallback((section: SkillsCatalogSection) => {
    void application.invoke("catalog.select-section", { section }).catch(showErrorToast);
  }, [application]);
  const setCatalogPluginID = useCallback((pluginId: string) => {
    void application.invoke("catalog.select-plugin", { pluginId }).catch(showErrorToast);
  }, [application]);
  const [catalogVisited, setCatalogVisited] = useState(false);
  const primaryPageView = navigationWorkspace.activePluginView;
  const showingPrimaryPluginView = primaryPageView !== undefined;
  const showingPluginWebPage = primaryPageView?.webPage === true;
  const showingWebPage = showingWebsite || showingPluginWebPage;
  const handledRegionRequest = useRef<number | undefined>(undefined);
  useEffect(() => {
    const request = navigationWorkspace.regionVisibilityRequest;
    if (!request || handledRegionRequest.current === request.id) return;
    handledRegionRequest.current = request.id;
    if (request.region === "primary" && rightPanelGlobalized) setRightPanelOpenWithMotion(false);
    if (request.region === "auxiliary") setRightPanelOpenWithMotion(true);
    if (request.region === "inspector") {
      setEnvironmentPanelDismissed(false);
      setEnvironmentPanelOpen(true);
    }
  }, [navigationWorkspace.regionVisibilityRequest, rightPanelGlobalized, setRightPanelOpenWithMotion]);
  const accountOpen = navigationWorkspace.accountOpen;
  const setAccountOpen = useCallback((open: boolean): void => {
    void application.invoke("navigation.show-account", { open }).catch(showErrorToast);
  }, [application]);
  const onboardingComplete = useSyncExternalStore(application.onboarding.subscribe, () => application.onboarding.getSnapshot().completed);

  const settingsInitialPage = useSyncExternalStore(application.subscribe, () => application.getSnapshot().settings.activePageId ?? "providers") as SettingsPage;
  const setSettingsInitialPage = useCallback((pageId: SettingsPage): void => {
    void application.invoke("settings.open", { pageId }).catch(showErrorToast);
  }, [application]);

  const {
    workspaceViewTabs,
    workspaceConversationActive,
    focusWorkspaceConversation,
    workspaceActiveViewTabID,
    workspaceActiveFileTabID,
    ensureWorkspaceToolTab,
    openWorkspaceTool,
    openWorkspaceArtifactTab,
    syncWorkspaceProjectTab,
    focusWorkspaceViewTab,
    closeWorkspaceViewTab,
    closeWorkspaceViewTabsWhere,
    toggleRightPanel,
  } = useWorkspaceToolState({
    application,
    rightPanelOpen,
    rightPanelGlobalized,
    setRightPanelOpenWithMotion,
  });
  const workspaceToolsCoverConversation = rightPanelGlobalized && !workspaceConversationActive;
  const [environmentPanelOpen, setEnvironmentPanelOpen] = useState(false);
  const [environmentPanelDismissed, setEnvironmentPanelDismissed] =
    useState(false);
  // Everything the window gives to other columns before the conversation.
  const environmentPanelPaneOffset =
    effectiveSidebarWidth + ribbonWidth - SIDEBAR_RIBBON_WIDTH +
    (rightPanelOpen && !rightPanelGlobalized ? clampedWorkspaceRightPanelWidth : 0);
  const [environmentPanelHasRoom, setEnvironmentPanelHasRoom] = useState(() =>
    typeof window === "undefined"
      ? false
      : environmentPanelFits(window.innerWidth - environmentPanelPaneOffset),
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
  const focusedWorkspace = navigationWorkspace.workspaces.find(workspace => workspace.id === navigationWorkspace.focusedWorkspaceId);
  const focusedWorkspaceContext: RuntimeContext | undefined = focusedWorkspace
    ? { kind: "project", project_id: focusedWorkspace.id, cwd: focusedWorkspace.path } : undefined;
  const setFocusedWorkspaceContext = useCallback((context: RuntimeContext | undefined): void => {
    void application.invoke("navigation.focus-workspace", { workspaceId: context?.kind === "project" ? context.project_id : undefined }).catch(showErrorToast);
  }, [application]);
  useEffect(() => {
    if (!navigationWorkspace.focusedWorkspaceId) return;
    closePrimaryPluginView();
    openWorkspaceTool("files");
  }, [navigationWorkspace.focusedWorkspaceId]);
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
    setActiveWebsiteID(undefined);
    void application.invoke("navigation.show-native", {}).catch(showErrorToast);
    setSettingsOpen(false);
    setNativeSection("conversations");
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
  const conversationDetails = useSyncExternalStore(application.subscribe, () => application.getSnapshot().conversationDetails);
  const contextCompositionEntries = useMemo(() => structuredClone(conversationDetails.filter(entry => entry.kind === "context")) as ContextCompositionEntry[], [conversationDetails]);
  const [settingsNoticeVisible, setSettingsNoticeVisible] = useState(false);
  const pendingFork = conversationActions.pendingFork;
  const cancelPendingFork = (): void => { void application.invoke("history.cancel-fork", {}).catch(showErrorToast); };
  const {
    pendingViewSwitch,
    visiblePendingThreadID,
    viewSwitchPending,
    viewContextSwitchPending,
    finishViewSwitch,
    cancelViewSwitch,
    isCurrentViewSwitchRequest,
    getCurrentViewSwitchRequestID,
  } = useApplicationNavigationState(application);
  const windowResizingRef = useRef(false);
  const environmentPanelHasRoomRef = useRef(environmentPanelHasRoom);
  const pendingEnvironmentPanelHasRoomRef = useRef<boolean | undefined>(
    undefined,
  );
  const environmentToggleRef = useRef<HTMLButtonElement>(null);
  const environmentPanelRef = useRef<HTMLDivElement>(null);
  const workspaceHasDirtyFilesRef = useRef(false);
  const lastFocusOutsideWorkspaceRef = useRef<{
    element: HTMLElement;
    owner: string;
  } | null>(null);
  const previousWorkspaceFocusModeRef = useRef({
    fullPanel: false,
    open: false,
  });
  const currentSessionTab = tabs.items.find(tab => tab.id === tabs.activeTabId);

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

  const workspaceFocusOwner = JSON.stringify([
    activeContext ? runtimeContextKey(activeContext) : "",
    activeContext?.cwd,
    tabs.activeTabId,
    activeThread?.id,
    activeThread?.cwd,
  ]);
  useLayoutEffect(() => {
    const handleFocusIn = (event: FocusEvent): void => {
      const target = event.target;
      const workspacePanel = appShellRef.current?.querySelector(".workspace-right-panel");
      if (
        target instanceof HTMLElement &&
        !workspacePanel?.contains(target) &&
        // Closing via window chrome must not replace the reading position's
        // focus target. When it opens the panel, the toggle is a valid owner.
        !(rightPanelOpen && target.closest('[data-wuu-component="right-sidebar-toggle"]'))
      ) {
        lastFocusOutsideWorkspaceRef.current = { element: target, owner: workspaceFocusOwner };
      }
    };
    document.addEventListener("focusin", handleFocusIn);
    return () => document.removeEventListener("focusin", handleFocusIn);
  }, [rightPanelOpen, workspaceFocusOwner]);

  useLayoutEffect(() => {
    const fullPanel = rightPanelOpen && rightPanelGlobalized;
    const previous = previousWorkspaceFocusModeRef.current;
    previousWorkspaceFocusModeRef.current = { fullPanel, open: rightPanelOpen };

    const sidebar = appShellRef.current?.querySelector<HTMLElement>(".sidebar");
    sidebar?.toggleAttribute("inert", fullPanel && sidebarDrawerMode && !sidebarDrawerVisible
      && !sidebar.querySelector(".sidebar-ribbon"));

    if (fullPanel && !previous.fullPanel) {
      appShellRef.current
        ?.querySelector<HTMLButtonElement>(
          '.workspace-right-panel [role="tab"][aria-selected="true"]',
        )
        ?.focus({ preventScroll: true });
      return;
    }
    if ((previous.fullPanel && !fullPanel) || (previous.open && !rightPanelOpen)) {
      if (viewSwitchPending) return;
      // An explicit return-to-conversation request owns focus over passive restoration.
      if (mainComposerFocusRequest?.interactionVersion === userInteractionVersionRef.current) return;
      const previousFocus = lastFocusOutsideWorkspaceRef.current;
      // Focus return must not reveal old history or follow a reused control into
      // a different conversation. Reading position remains owned by scrolling.
      if (
        previousFocus?.owner === workspaceFocusOwner &&
        previousFocus.element.isConnected &&
        !previousFocus.element.closest('[inert], [hidden], [aria-hidden="true"], .conversation-split-pane:not(.active)')
      ) {
        previousFocus.element.focus({ preventScroll: true });
        return;
      }
      appShellRef.current
        ?.querySelector<HTMLHeadingElement>(
          ".conversation-title-heading h1",
        )
        ?.focus({ preventScroll: true });
    }
  }, [rightPanelGlobalized, rightPanelOpen, sidebarDrawerMode, sidebarDrawerVisible, viewSwitchPending, workspaceFocusOwner, mainComposerFocusRequest]);

  // Workspace panel (file tree / file preview / terminal / review) root: follows the
  // active thread's own cwd when it differs from activeContext — the
  // main remaining case is a worktree-fork thread, whose cwd is a git
  // worktree directory distinct from the project root activeContext stays
  // pinned to.
  const conversationWorkspaceContext = useMemo(
    () => workspacePanelContext(activeContext, primaryThread),
    [activeContext, primaryThread],
  );
  const workspaceContext = focusedWorkspaceContext ?? conversationWorkspaceContext;
  const activeWorkspaceViewTab = workspaceActiveViewTabID
    ? workspaceViewTabs.find((tab) => tab.id === workspaceActiveViewTabID)
    : undefined;
  const workspaceSelectionEnabled =
    workspaceToolsCoverConversation &&
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
  const activeThreadID = activeThread?.id;
  const activeThreadCwd = activeThread?.cwd;
  const previousThreadWorkspace = useRef({ id: activeThreadID, cwd: activeThreadCwd });
  useEffect(() => {
    const previous = previousThreadWorkspace.current;
    previousThreadWorkspace.current = { id: activeThreadID, cwd: activeThreadCwd };
    if (activeThreadID && previous.id === activeThreadID && previous.cwd &&
      activeThreadCwd && previous.cwd !== activeThreadCwd) {
      showToast({
        message: translateCurrent("app.workspaceSwitched"),
        dedupeKey: `workspace-switched:${activeThreadID}:${activeThreadCwd}`,
      });
    }
  }, [activeThreadID, activeThreadCwd]);
  const environmentContext = workspacePanelContext(activeContext, activeThread);
  const sideSnapshot = useSyncExternalStore(application.subscribe, () => application.getSnapshot().sideConversation);
  const sideCommandID = sideSnapshot.commandId;
  const sideConversationAvailable = sideSnapshot.commands.length > 0;
  const sideThread = useMemo(() => ({
    entry: sideSnapshot.entry,
    open: () => { if (sideSnapshot.parentThreadId) void application.invoke("side.open", { parentThreadId: sideSnapshot.parentThreadId }).catch(showErrorToast); },
    close: () => { if (sideSnapshot.parentThreadId) void application.invoke("side.close", { parentThreadId: sideSnapshot.parentThreadId }).catch(showErrorToast); },
    setDraftSelection: (selection?: SideThreadSelection) => { if (sideSnapshot.parentThreadId) void application.invoke("side.set-selection", { parentThreadId: sideSnapshot.parentThreadId, selection }).catch(showErrorToast); },
  }), [application, sideSnapshot]);
  const pendingSideSelectionRef = useRef<{ threadID: string; selection: SideThreadSelection } | null>(null);
  useEffect(() => {
    const syncVisibleCUAThread = () => {
      application.desktop.setActiveCUAThread?.(activeThreadID);
    };
    syncVisibleCUAThread();
    window.addEventListener("focus", syncVisibleCUAThread);
    return () => window.removeEventListener("focus", syncVisibleCUAThread);
  }, [application, activeThreadID]);
  const activeBrowserActivity = useMemo(
    () =>
      ENABLE_EMBEDDED_BROWSER
        ? activities.filter(activity => activity.workdir === activeContext?.cwd && activity.thread_id === activeThreadID)
            .filter((activity) => activity.kind === "browser" && activity.state !== "stopped")
            .at(-1)
        : undefined,
    [activities, activeThreadID, activeContext?.cwd],
  );

  const mascotProviderNames = useMemo(() => configuration?.providers?.map(provider => provider.name), [configuration?.providers]);
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
    application,
    open: navigationWorkspace.searchOpen,
    onOpenChange: open => { void application.invoke("navigation.show-search", { open }).catch(showErrorToast); },
    onOpen: () => {
      setEnvironmentDialog(null);
      cancelPendingFork();
    },
    onSelect: closePrimaryPluginView,
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
    cancelPendingFork();
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
    settingsOpen || accountOpen || showingWebsite || nativeSection === "catalog" || showingPrimaryPluginView ||
    environmentDialog !== null ||
    Boolean(pendingFork) ||
    conversationSearch.open;
  const activeTodoUpdate = latestTodoUpdateForThread(activeThread);
  const splitConversation = Boolean(primaryThread && secondaryThread);

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

    const offWindowResizeState = application.desktop.onResizeState?.(
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
      offWindowResizeState?.();
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
    const update = (): void => {
      const nextHasRoom = environmentPanelFits(
        window.innerWidth - environmentPanelPaneOffset,
      );
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
    window.addEventListener("resize", update);
    // The panel widens with the UI text size, so a size change moves the fit.
    const stopObservingAppearance = observeAppearance(update);
    return () => {
      window.removeEventListener("resize", update);
      stopObservingAppearance();
    };
  }, [environmentPanelPaneOffset]);


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

  useEffect(() => {
    function handlePointerDown(event: PointerEvent): void {
      const target = event.target;
      if (!(target instanceof Node)) {
        return;
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
    environmentPanelHasRoom,
    environmentPanelMenu,
    environmentPanelOpen,
  ]);

  useEffect(() => {
    scheduleGitStatusRefresh(0);
  }, [
    activeContext?.kind,
    environmentContext?.cwd,
    workspaceSnapshot.activeWorkspaceId,
    activeThreadID,
  ]);

  useEffect(() => {
    function handleFocus(): void {
      scheduleGitStatusRefresh(0);
    }

    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, []);
  const showingSkillsCatalog = Boolean(
    initialized &&
    (nativeSection === "catalog" || currentSessionTab?.kind === "skills"),
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
  const windowTitle = poppedOutMode ? popOutWindowTitle
    : settingsOpen ? t("settings.navigation")
    : accountOpen ? "Wuu"
    : showingWebsite ? activeWebsite?.title || activeWebsite?.url || "Wuu"
    : activeTitle;
  useEffect(() => {
    document.title = windowTitle;
  }, [windowTitle]);
  const activePendingThreadCreation = submissionView.pendingCreations.find(
    (pending) => pending.ownerTabId === tabs.activeTabId,
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
    enabled: initialized
      && !poppedOutMode && !isTouchWebShell() && !activeThread?.read_only
      && !showingManagementCatalog && !emptyConversation && !browserOverlaySuppressed
      && !workspaceRightPanelAutoGlobalized
      && activeBrowserActivity?.state !== "foreground_controlled"
      && activeBrowserActivity?.state !== "user_controlled",
    panelOpen: rightPanelOpen,
    activeTabID: workspaceActiveViewTabID,
    onOpen: openWorkspaceArtifactTab,
  });
  const previewTargetThread = activeThread;
  const previewTargetContext = workspacePanelContext(activeContext, previewTargetThread);
  const activePreviewFile = activeWorkspaceViewTab?.kind === "file" &&
    activeWorkspaceViewTab.id === activeWorkspaceFileTabID &&
    sameRuntimeContext(activeWorkspaceViewTab.context, previewTargetContext) &&
    activeWorkspaceViewTab.context.cwd === previewTargetContext?.cwd
      ? activeWorkspaceViewTab : undefined;
  const activePreviewArtifact = activeWorkspaceViewTab?.kind === "artifact" &&
    activeWorkspaceViewTab.artifact.mimeType === "application/pdf" &&
    activeWorkspaceViewTab.artifact.uri?.startsWith("wuu-artifact:") &&
    activeWorkspaceViewTab.threadID === previewTargetThread?.id &&
    activeWorkspaceViewTab.cwd === previewTargetThread.cwd
      ? activeWorkspaceViewTab : undefined;
  // A docked preview sits beside the conversation, whose own input is in
  // reach; the document input appears once the preview takes the window.
  const documentComposerVisible = Boolean(
    initialized && workspaceToolsCoverConversation &&
    !splitConversation &&
    !showingManagementCatalog && !showingPrimaryPluginView &&
    (activePreviewFile || activePreviewArtifact),
  );
  const conversationVisible = !showingWebsite && !showingManagementCatalog && !showingPrimaryPluginView &&
    !settingsOpen && !accountOpen && !workspaceToolsCoverConversation;
  const mainConversationDockVisible =
    initialized &&
    !showingWebsite &&
    !splitConversation &&
    !showingManagementCatalog &&
    !workspaceToolsCoverConversation &&
    !settingsOpen &&
    !showingPrimaryPluginView;

  useEffect(() => {
    // Delivered snapshots may belong to either visible conversation pane.
    // Closing them on navigation also releases viewer resources.
    const isStaleTurnTab = (tab: WorkspaceViewTab): boolean => {
      if (tab.kind === "artifact") {
        return emptyConversation
          || ![primaryThread, secondaryThread].some((thread) =>
            thread?.id === tab.threadID && thread.cwd === tab.cwd);
      }
      return tab.kind === "diff" &&
        (!activeThreadID || tab.threadID !== activeThreadID || emptyConversation);
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
    workspaceActiveViewTabID,
    workspaceViewTabs,
    primaryThread,
    secondaryThread,
  ]);

  const conversationRestoring = pendingViewSwitch?.restoreRequestID !== undefined;
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
    jumpToLatest: jumpConversationToLatest,
    jumpToConversationMessage,
    captureConversationScrollIntent,
    settleConversationLayout,
    requestSubmittedQueryScroll,
    discardSubmittedMessage,
  } = useConversationScrollState({
    activeThreadID,
    primaryThreadID: primaryThread?.id,
    secondaryThreadID: secondaryThread?.id,
    activePane: conversationWorkspace.activePane,
    splitConversation,
    primaryTurns: primaryThread?.turns,
    secondaryTurns: secondaryThread?.turns,
    emptyConversation,
    initialized,
    nativeScrollBounce: application.desktop.platform === "darwin" && application.desktop.hostKind !== "web",
    running: isThreadRunning(activeThread),
  });
  const initialSubmissionIds = useRef(new Set(submissionView.operations.map(operation => operation.operationID)));
  const placedSubmissions = useRef(new Map<string, string>());
  useLayoutEffect(() => {
    const activeOperations = new Set(submissionView.operations.map(operation => operation.operationID));
    for (const id of placedSubmissions.current.keys()) if (!activeOperations.has(id)) placedSubmissions.current.delete(id);
    for (const operation of submissionView.operations) {
      if (initialSubmissionIds.current.has(operation.operationID)) continue;
      const placed = placedSubmissions.current.get(operation.operationID);
      if (operation.phase === "failed" || operation.phase === "cancelled") {
        if (placed) discardSubmittedMessage(placed);
        initialSubmissionIds.current.add(operation.operationID);
        placedSubmissions.current.delete(operation.operationID);
        continue;
      }
      if (placed) continue;
      if (operation.ownerTabId !== tabs.activeTabId && operation.threadId !== activeThreadID) {
        initialSubmissionIds.current.add(operation.operationID);
        continue;
      }
      const pending = submissionView.pendingCreations.find(creation => creation.operationID === operation.operationID);
      const thread = conversationWorkspace.threads.find(thread => thread.id === operation.threadId);
      const item = (pending ? [pending.turn] : thread?.turns ?? []).flatMap(turn => turn.items)
        .find(item => item.type === "user_message" && item.source_id === operation.messageID);
      if (!item) continue;
      placedSubmissions.current.set(operation.operationID, item.id);
      requestSubmittedQueryScroll(item.id, operation.messageID);
    }
  }, [submissionView, conversationWorkspace, tabs.activeTabId, activeThreadID, requestSubmittedQueryScroll, discardSubmittedMessage]);
  useConversationSwitchReady({
    visible: !settingsOpen && !accountOpen && !showingWebsite,
    pendingViewSwitch,
    activeThreadID,
    viewportRef: conversationScrollRef,
    settleLayout: settleConversationLayout,
    finishViewSwitch,
  });
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
      if (isTouchWebShell() || !conversationVisible) {
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
    [conversationPaneRef, conversationVisible],
  );
  const requestMainComposerFocus = useCallback(
    (
      target: ComposerVariant,
      origin: Element | null = document.activeElement,
      interactionVersion: number = userInteractionVersionRef.current,
      matchesDestination?: (snapshot: ApplicationSnapshotV1) => boolean,
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
  const publicComposerFocusRequest = useSyncExternalStore(application.subscribe,
    () => application.getSnapshot().navigationWorkspace.composerFocusRequest);
  const handledPublicComposerFocus = useRef(publicComposerFocusRequest?.id);
  useLayoutEffect(() => {
    const request = publicComposerFocusRequest;
    if (!request || handledPublicComposerFocus.current === request.id) return;
    handledPublicComposerFocus.current = request.id;
    const snapshot = application.getSnapshot();
    if (snapshot.tabs.activeTabId !== request.tabId) return;
    setRightPanelOpenWithMotion(false);
    if (isTouchWebShell()) return;
    const interactionVersion = userInteractionVersionRef.current;
    if (snapshot.drafts.split) {
      const frame = window.requestAnimationFrame(() => {
        if (application.getSnapshot().tabs.activeTabId !== request.tabId
          || userInteractionVersionRef.current !== interactionVersion) return;
        focusComposerTextarea(document.querySelector<HTMLTextAreaElement>(".conversation-split-pane.active textarea"));
      });
      return () => window.cancelAnimationFrame(frame);
    }
    requestMainComposerFocus("dock", document.activeElement, interactionVersion,
      current => current.tabs.activeTabId === request.tabId);
  }, [application, publicComposerFocusRequest, requestMainComposerFocus, setRightPanelOpenWithMotion]);

  useLayoutEffect(() => {
    if (!mainComposerFocusRequest) {
      return;
    }
    if (
      mainComposerFocusRequest.matchesDestination &&
      !mainComposerFocusRequest.matchesDestination(application.getSnapshot())
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
    tabs, conversationWorkspace,
  ]);
  const handleTurnCollapseComplete = useCallback(() => {
    scheduleStreamScroll();
  }, [scheduleStreamScroll]);

  // Captures the user's scroll position when they open the inline edit on a
  // historical message so cancel can put them back exactly where they were
  // instead of dropping them at the latest content via the resize observer.
  // Details actions on subagent completion messages split the conversation
  // and open the child session in the secondary pane. The App owns thread
  // state, so it registers the bridge handler once; message buttons call it.
  const handleOpenThreadInSplit = useStableCallback((threadId: string) => {
    void application.invoke("conversation.open-split", { threadId }).catch(showErrorToast);
  });
  useEffect(() => {
    setOpenThreadInSplitHandler(handleOpenThreadInSplit);
    return () => setOpenThreadInSplitHandler(undefined);
  }, [handleOpenThreadInSplit]);
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
  const rememberWorkspaceDirtyFiles = useStableCallback((dirty: boolean): void => {
    workspaceHasDirtyFilesRef.current = dirty;
  });
  const sidebarThreads = navigationWorkspace.threads;
  // The Environment panel's git actions (branch switch / commit / PR) mutate the
  // active session's working tree, so they are gated on that tree being busy —
  // not on any thread anywhere running. A worktree-fork thread running in its
  // own cwd, or a thread in another project, no longer blocks them.
  const environmentGitBusy = environmentSnapshot.actionBusy || viewContextSwitchPending;
  useEffect(() => {
    if (!configuration?.extension_inventory || sideConversationAvailable) return;
    pendingSideSelectionRef.current = null;
    if (sideThread.entry?.open) sideThread.close();
    if (workspaceViewTabs.some((tab) => tab.id === "side-thread")) closeWorkspaceViewTab("side-thread");
  }, [sideConversationAvailable, activeThreadID, workspaceViewTabs, configuration?.extension_inventory]);
  const sideThreadPanelVisible = Boolean(sideConversationAvailable && activeThreadID && rightPanelOpen &&
    !workspaceConversationActive && workspaceActiveViewTabID === "side-thread");
  useEffect(() => {
    if (sideThreadPanelVisible && !sideThread.entry?.open) sideThread.open();
  }, [sideThreadPanelVisible, sideThread.entry?.open, sideThread.open]);
  // The environment panel floats inside the conversation pane, so it can
  // coexist with the docked workspace right panel. Only the globalized
  // (full-window sheet) right panel blocks it, because that mode makes the
  // entire conversation pane inert.
  const environmentPanelCanShow = Boolean(
    ENABLE_ENVIRONMENT_PANEL &&
    initialized &&
    !poppedOutMode &&
    !workspaceToolsCoverConversation &&
    // The card describes the conversation's workspace; pages that replace the
    // conversation have nothing for it to describe.
    !showingManagementCatalog &&
    !showingPrimaryPluginView,
  );
  const environmentPanelTargetVisible =
    environmentPanelCanShow &&
    (environmentPanelOpen ||
      (environmentPanelHasRoom &&
        !environmentPanelDismissed &&
        !emptyConversation &&
        // Wait for repository detection so non-Git folders do not briefly
        // open and close the panel while their status is still unknown.
        gitStatus?.is_repo === true));
  const environmentPanelVisible = environmentPanelTargetVisible;
  const environmentPanelMotionState: EnvironmentPanelMotionState =
    environmentPanelVisible ? "open" : "closing";
  // The sidebar is the single conversation switcher. Session state remains
  // available for recovery and drafts, but the titlebar no longer renders a
  // growing tab strip.
  const sidebarVisible = !poppedOutMode;
  const sidebarToggleVisible = sidebarVisible;

  const shellClassName = `app-shell navigation-ribbon-layout${poppedOutMode ? " popped-out-shell" : ""}${sidebarDrawerMode ? " sidebar-collapsed" : ""}${
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
  }${rightPanelOpen ? " right-panel-open" : ""}${rightPanelGlobalized && rightPanelOpen ? " right-panel-globalized" : ""}${workspaceConversationActive ? " workspace-conversation-active" : ""}${resizingSplit ? " resizing-split" : ""}`;
  const shellStyle = {
    "--sidebar-width": `${effectiveSidebarWidth}px`,
    "--sidebar-ribbon-width": `${ribbonWidth}px`,
    "--sidebar-ribbon-compact-width": `${SIDEBAR_RIBBON_WIDTH}px`,
    "--sidebar-ribbon-extra": `${ribbonWidth - SIDEBAR_RIBBON_WIDTH}px`,
    "--sidebar-open-width": `${sidebarWidth}px`,
    "--workspace-sheet-left": `${effectiveSidebarWidth}px`,
    "--workspace-right-panel-width": `${clampedWorkspaceRightPanelWidth}px`,
    "--conversation-split-left": `${splitLeftPercent}%`,
  } as CSSProperties;
  const pullRequestDisabledReason = pullRequestUnavailableReason(
    gitStatus,
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

  // Read state follows visible completed content, not unrelated snapshot publications.
  const viewedThread = navigationWorkspace.threads.find(thread => thread.id === activeThreadID);
  const viewedTurnId = viewedThread?.latest_completed_turn_id;
  const viewedThreadStatus = viewedThread?.status;
  useEffect(() => {
    if (!conversationVisible || !activeThreadID || !viewedTurnId || viewedThreadStatus === "in_progress") return;
    void application.invoke("navigation.mark-viewed", { threadIds: [activeThreadID] }).catch(showErrorToast);
  }, [application, conversationVisible, activeThreadID, viewedTurnId, viewedThreadStatus]);

  function openSideThreadPanel(prompt?: string, commandID = sideCommandID): void {
    const parentThreadId = application.getSnapshot().sideConversation.parentThreadId;
    if (!parentThreadId) return;
    void application.invoke("side.open", { parentThreadId, commandId: commandID, prompt }).catch(showErrorToast);
  }

  function openSideThreadWithSelection(selection: SideThreadSelection): void {
    if (!activeThreadID || !sideConversationAvailable) return;
    openSideThreadPanel();
    sideThread.setDraftSelection(selection);
  }

  // Blocking questions stay in the conversation stream. Offers float above the composer.

  function closeWorkspaceMenus(): void {
    setEnvironmentPanelMenu(null);
    setSettingsOpen(false);
  }

  const scheduleGitStatusRefresh = (delayMs: number): void => application.environment.scheduleStatusRefresh(delayMs);
  const {
    toggleEnvironmentPanel,
    closeEnvironmentPanel,
  } = createEnvironmentPanelActions({
    setEnvironmentPanelOpen,
    setEnvironmentPanelDismissed,
    setEnvironmentPanelMenu,
    // Runtime menus are owned by the selected composer.
    closeRuntimeMenus: () => {},
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
  });
  const activateThread = useCallback((threadId: string): Promise<void> => application.invoke("thread.activate", { threadId }).catch(error => { showErrorToast(error); }), [application]);

  useConversationSearchNavigation({
    application,
    thread: activeThread,
    switching: viewSwitchPending,
    captureConversationScrollIntent,
    jumpToConversationMessage,
  });

  useEffect(() => {
    const ask = (event: Event) => {
      const selection = (event as CustomEvent<ResponseSelection>).detail;
      if (!sideConversationAvailable || !selection?.source?.thread_id) return;
      const reference: SideThreadSelection = { type: "response", response: selection };
      if (selection.source.thread_id === activeThreadID) {
        openSideThreadWithSelection(reference);
      } else {
        pendingSideSelectionRef.current = { threadID: selection.source.thread_id, selection: reference };
        void activateThread(selection.source.thread_id).catch(() => { pendingSideSelectionRef.current = null; });
      }
    };
    window.addEventListener("wuu:ask-side-selection", ask);
    return () => window.removeEventListener("wuu:ask-side-selection", ask);
  }, [activeThreadID, activateThread, sideThread.open, sideThread.setDraftSelection, sideConversationAvailable]);

  useEffect(() => {
    const pending = pendingSideSelectionRef.current;
    if (!pending || pending.threadID !== activeThreadID) return;
    pendingSideSelectionRef.current = null;
    openSideThreadWithSelection(pending.selection);
  }, [activeThreadID]);

  useEffect(() => {
    const api = application.webWorkspace;
    if (!api.capabilities.available) return;
    return api.onReveal((id) => {
      if (id === WEB_WORKSPACE_RIBBON_ID) { revealWebsite(); return; }
      // The native page belongs to this retained instance. Never open a new
      // plugin view to approximate its URL or lose its live document state.
      const panel = [...document.querySelectorAll<HTMLElement>(".web-workspace-panel")]
        .find((element) => element.dataset.websiteId === id);
      const instanceID = panel?.closest<HTMLElement>("[data-workbench-view-id]")?.dataset.workbenchViewId;
      const destination = application.getSnapshot().navigationWorkspace.destinations.find(view => view.instanceId === instanceID);
      if (!destination?.instanceId) return;
      setActiveWebsiteID(undefined);
      setAccountOpen(false);
      void application.invoke("navigation.open-plugin", { pluginId: destination.pluginId, viewTypeId: destination.view, instanceId: destination.instanceId }).catch(showErrorToast);
    });
  }, [application, revealWebsite]);

  // A failure notice continues an interrupted reply in its own conversation,
  // the same way the pet sends text: without touching the composer draft.
  const continueTurnHandlerRef = useRef<(detail: ContinueTurnDetail) => void>(() => undefined);
  continueTurnHandlerRef.current = ({ threadID, text, done }) => {
    void application.invoke("conversation.continue", { threadId: threadID, text }).then(result => result.accepted)
      .catch((error: unknown) => {
        showErrorToast(error);
        return false;
      })
      .then(done);
  };
  useEffect(() => {
    const handle = (event: Event): void => {
      event.preventDefault();
      continueTurnHandlerRef.current((event as CustomEvent<ContinueTurnDetail>).detail);
    };
    window.addEventListener(CONTINUE_TURN_EVENT, handle);
    return () => window.removeEventListener(CONTINUE_TURN_EVENT, handle);
  }, []);
  const selectSessionTab = (tabId: string): Promise<void> => application.invoke("tab.select", { tabId }).catch(error => { showErrorToast(error); });

  function closePrimaryPluginView(): void {
    setActiveWebsiteID(undefined);
    void application.invoke("navigation.show-native", {}).catch(showErrorToast);
    setSettingsOpen(false);
    setNativeSection("conversations");
  }

  function revealWebsite(): void {
    void application.invoke("navigation.show-native", {}).catch(showErrorToast);
    closeWorkspaceMenus();
    closeConversationSearch({ immediate: true });
    setEnvironmentDialog(null);
    cancelPendingFork();
    setSettingsOpen(false);
    setAccountOpen(false);
    closeSidebarDrawer();
    setActiveWebsiteID(WEB_WORKSPACE_RIBBON_ID);
  }

  async function createWebsiteTab(reopen = false): Promise<void> {
    await application.invoke(reopen ? "navigation.reopen-browser-tab" : "navigation.create-browser-tab", {});
    revealWebsite();
  }

  useEffect(() => {
    if (!ribbonBrowser.state) return;
    const onKeyDown = (event: KeyboardEvent): void => {
      const modifier = application.desktop.platform === "darwin" ? event.metaKey : event.ctrlKey;
      if (!modifier || event.altKey || event.defaultPrevented || event.key.toLowerCase() !== "t") return;
      // Retained extension WebPages keep their own browser shortcut contract.
      if ((event.target as Element | null)?.closest?.(".web-workspace-panel")) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      if ((ribbonBrowser.state?.tabs?.length ?? 0) >= WEB_WORKSPACE_TAB_LIMIT || (event.shiftKey && !ribbonBrowser.state?.canReopenTab)) return;
      void createWebsiteTab(event.shiftKey);
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [ribbonBrowser.state, createWebsiteTab]);

  function openSkillsTab(): void {
    if (!application.getSnapshot().workspaces.activeRoot) return;
    setActiveWebsiteID(undefined);
    void application.invoke("navigation.show-native", {}).catch(showErrorToast);
    setSettingsOpen(false);
    setCatalogVisited(true);
    setNativeSection("catalog");
    if (rightPanelGlobalized) setRightPanelOpenWithMotion(false);
  }

  const selectedProjectID = activeThread && isProjectCoordinator(activeThread)
    ? activeThread.id
    : activeThread?.source === PROJECT_SESSION_SOURCE ? activeThread.project_id : undefined;
  const selectedProject = sidebarThreads.find((thread) => thread.id === selectedProjectID);
  const selectedProjectTitle = selectedProject ? baseThreadTitle(selectedProject) : selectedProjectID;
  useEffect(() => {
    syncWorkspaceProjectTab(selectedProjectID && selectedProjectTitle
      ? { id: selectedProjectID, title: selectedProjectTitle }
      : undefined);
  }, [selectedProjectID, selectedProjectTitle, syncWorkspaceProjectTab]);
  const activeProjectSessions = useMemo(
    () => activeThread && isProjectCoordinator(activeThread) ? projectSessionsOf(activeThread.id, sidebarThreads) : [],
    [activeThread, sidebarThreads],
  );
  useEffect(() => { scheduleStreamScroll(); }, [conversationDetails, scheduleStreamScroll]);

  async function submitContextChat(text: string, page: PageContextSnapshot, context: RuntimeContext, sessionId?: string): Promise<{ sessionId: string; sent: boolean }> {
    return application.invoke("context-chat.submit", { text, page, context, sessionId });
  }

  const failedDraftTabID = submissionView.failedDraftTabIds.find((id) => tabs.items.some((tab) => tab.id === id));
  const failedDraftNotice = failedDraftTabID ? (
    <UILayerPortal layer="notice">
      <TopNotice
        key={failedDraftTabID}
        message={t(recoveryNotices.some(notice => notice.ownerTabId === failedDraftTabID)
          ? "composer.recoveredInterruptedDraft" : "composer.failedDraftSaved")}
        icon={CircleAlert}
        isError
        persistent
        action={{ label: t("composer.openFailedDraft"), onClick: () => {
          revealConversationFromFocusedWorkspace();
          void selectSessionTab(failedDraftTabID);
        } }}
        dismissAriaLabel={t("composer.discardFailedDraft")}
        onDismiss={() => {
          void application.invoke("tab.close", { tabId: failedDraftTabID }).catch(showErrorToast);
        }}
      />
    </UILayerPortal>
  ) : null;


  useEffect(() => {
    const back = (event: Event): void => {
      if (accountOpen) { event.preventDefault(); setAccountOpen(false); }
      else if (settingsOpen) { event.preventDefault(); setSettingsOpen(false); }
      else if (showingWebsite) { event.preventDefault(); setActiveWebsiteID(undefined); }
      else if (sidebarDrawerVisible) { event.preventDefault(); closeSidebarDrawer(); }
      else if (rightPanelOpen) { event.preventDefault(); setRightPanelOpenWithMotion(false); }
      else if (nativeSection === "catalog" && !showingPrimaryPluginView) { event.preventDefault(); setNativeSection("conversations"); }
    };
    window.addEventListener("wuu:workbench-back", back);
    return () => window.removeEventListener("wuu:workbench-back", back);
  }, [accountOpen, settingsOpen, showingWebsite, sidebarDrawerVisible, closeSidebarDrawer, rightPanelOpen, setRightPanelOpenWithMotion, nativeSection, showingPrimaryPluginView]);


  // This is a window history of existing destinations, never a second owner of
  // session content. Docked panels and split-pane focus are not page visits.
  const currentPage = !onboardingComplete || !initialized ? undefined
    : settingsOpen ? JSON.stringify(["settings", settingsInitialPage])
    : accountOpen ? JSON.stringify(["account", ""])
    : showingWebsite ? JSON.stringify(["website", activeWebsite?.id])
    : workspaceToolsCoverConversation && rightPanelOpen && workspaceActiveViewTabID
      ? JSON.stringify(["workspace", workspaceActiveViewTabID])
    : primaryPageView ? JSON.stringify(["plugin", primaryPageView.id])
    : showingSkillsCatalog ? JSON.stringify(["catalog", catalogSection])
    : tabs.activeTabId ? JSON.stringify(["session", tabs.activeTabId]) : undefined;
  const pageLayoutIntent = JSON.stringify([
    tabs.activeTabId, settingsOpen, settingsInitialPage, accountOpen, activeWebsiteID, activeWebsite?.id, nativeSection, catalogSection,
    primaryPageView?.id, workspaceActiveViewTabID, rightPanelOpen, rightPanelManualGlobalized,
  ]);
  const previousPageLayout = useRef({ intent: pageLayoutIntent, autoGlobalized: rightPanelAutoGlobalized });
  const layoutOnlyPageChange = previousPageLayout.current.intent === pageLayoutIntent
    && previousPageLayout.current.autoGlobalized !== rightPanelAutoGlobalized;
  useLayoutEffect(() => {
    previousPageLayout.current = { intent: pageLayoutIntent, autoGlobalized: rightPanelAutoGlobalized };
  }, [pageLayoutIntent, rightPanelAutoGlobalized]);
  const [historySessionRestore, setHistorySessionRestore] = useState<{
    id: string; origin: string | undefined; isCurrent: () => boolean; done: () => void;
    finished: boolean; viewRequestID: number;
  }>();
  useLayoutEffect(() => {
    const request = historySessionRestore;
    if (!request) return;
    const targetUnavailable = !isHistoryPageAvailable(JSON.stringify(["session", request.id]));
    if (targetUnavailable || !request.isCurrent() || !isCurrentViewSwitchRequest(request.viewRequestID)
      || (currentPage !== request.origin
      && currentPage !== JSON.stringify(["session", request.id]))) {
      // Settings/tool pages do not switch runtimes themselves. Re-select the
      // source while the pending context marker still exists: cancellation
      // alone would leave the main process on the abandoned target runtime.
      if (isCurrentViewSwitchRequest(request.viewRequestID)
        && (targetUnavailable || (currentPage && JSON.parse(currentPage)[0] !== "session"))) {
        if (pendingViewSwitch?.contextSwitching) void selectSessionTab(tabs.activeTabId);
        else cancelViewSwitch();
      }
      request.done();
      setHistorySessionRestore(undefined);
      return;
    }
    if (tabs.activeTabId === request.id) {
      closePrimaryPluginView();
      focusWorkspaceConversation();
      setSettingsOpen(false);
      setAccountOpen(false);
      request.done();
      setHistorySessionRestore(undefined);
    } else if (request.finished) {
      request.done();
      setHistorySessionRestore(undefined);
    }
  }, [historySessionRestore, tabs.activeTabId, tabs.items, navigationWorkspace.workspaces, currentPage, pendingViewSwitch]);
  function isHistoryPageAvailable(page: string): boolean {
    const [kind, id] = JSON.parse(page) as [string, string];
    if (kind === "settings") {
      if (id === "subscriptions" || id === "agents") return false;
      if (id === "remote") return application.settingsControls.capabilities.remote;
      if (id === "browser") return !poppedOutMode && application.settingsControls.capabilities.browser;
      if (id.startsWith("plugin-view:")) return application.settingsControls.getPages().some(page => `plugin-view:${page.pluginId}:${page.id}` === id);
      if (id.startsWith("plugin-settings:")) return configuration?.extension_inventory?.some(plugin =>
        `plugin-settings:${plugin.id}` === id && plugin.kind === "plugin" && plugin.enabled !== false
        && (plugin.approval_state === "official" || plugin.approval_state === "granted")
        && (plugin.contributions?.settings?.length ?? 0) > 0) ?? false;
      return true;
    }
    if (kind === "website") return !poppedOutMode && Boolean(ribbonBrowser.state?.tabs?.some(website => website.id === id));
    if (kind === "catalog") return Boolean(activeContext);
    if (kind === "account") return ENABLE_ACCOUNT && Boolean(application.desktop.account);
    if (kind === "workspace") {
      const tab = workspaceViewTabs.find(tab => tab.id === id);
      return Boolean(tab);
    }
    if (kind === "plugin") return navigationWorkspace.destinations.some(view => view.instanceId === id);
    const tab = tabs.items.find(tab => tab.id === id);
    if (!tab) return false;
    return !tab.workspaceId
      || navigationWorkspace.workspaces.some(project => project.id === tab.workspaceId && !project.missing);
  }
  const pageHistory = usePageHistory(currentPage, isHistoryPageAvailable, async (page, isCurrent) => {
    const [kind, id] = JSON.parse(page) as [string, string];
    if (kind === "settings") {
      setAccountOpen(false);
      setSettingsInitialPage(id as SettingsPage);
      setSettingsOpen(true);
      return;
    }
    if (kind === "account") { setSettingsOpen(false); setAccountOpen(true); return; }
    if (kind === "website") {
      const api = application.webWorkspace;
      if (!api.capabilities.available) return;
      try {
        // Replay only the existing native tab ID. Its own URL/back-forward
        // history stays owned by WebContents and onState remains authoritative.
        await api.selectTab(WEB_WORKSPACE_RIBBON_ID, id);
        if (isCurrent()) revealWebsite();
      } catch (error) {
        if (isCurrent()) showErrorToast(error);
      }
      return;
    }
    if (kind === "catalog") { setAccountOpen(false); setCatalogSection(id as SkillsCatalogSection); openSkillsTab(); return; }
    if (kind === "session") {
      await new Promise<void>((done) => {
        const activation = selectSessionTab(id);
        const request = { id, origin: currentPage, isCurrent, done, finished: false,
          viewRequestID: getCurrentViewSwitchRequestID() };
        setHistorySessionRestore(request);
        void activation.finally(() => {
          setHistorySessionRestore(current => current === request ? { ...current, finished: true } : current);
        });
      });
      return;
    } else if (kind === "workspace") {
      closePrimaryPluginView();
      focusWorkspaceViewTab(id);
      setRightPanelOpenWithMotion(true);
      setRightPanelManualGlobalized(true);
    } else if (kind === "plugin") {
      setActiveWebsiteID(undefined);
      setNativeSection("conversations");
      const destination = application.getSnapshot().navigationWorkspace.destinations.find(view => view.instanceId === id);
      if (!destination) return;
      await application.invoke("navigation.open-plugin", { pluginId: destination.pluginId, viewTypeId: destination.view, instanceId: id });
      focusWorkspaceConversation();
    }
    setSettingsOpen(false);
    setAccountOpen(false);
  }, layoutOnlyPageChange);

  const contextCapsule = !poppedOutMode && onboardingComplete && initialized && activeContext
    ? (
    // Restore visibility when the workbench experiment re-enables the floating context chat.
    <ContextChatCapsule context={activeContext} visible={false}
      source={showingWebsite && activeWebsite && !settingsOpen && !accountOpen
        ? { kind: "website", id: WEB_WORKSPACE_RIBBON_ID, tabId: activeWebsite.id, title: activeWebsite.title || activeWebsite.url }
        : { kind: "app", id: `wuu:${accountOpen ? "account" : settingsOpen ? "settings" : showingPrimaryPluginView ? "plugin" : nativeSection}`,
            title: `Wuu · ${accountOpen ? "Account" : settingsOpen ? "Settings" : showingPrimaryPluginView ? "Plugin" : nativeSection}` }}
      onSubmit={submitContextChat}
      onOpen={sessionId => { setAccountOpen(false); revealConversationFromFocusedWorkspace(); void activateThread(sessionId); }} />
  ) : null;

  if (ENABLE_ACCOUNT && accountOpen && application.desktop.account) {
    return <><AccountScreen pageHistory={isTouchWebShell() ? undefined : pageHistory} driver={application.desktop.account} openExternal={application.settingsControls.openExternal} onBack={() => setAccountOpen(false)} />{contextCapsule}</>;
  }


  if (!onboardingComplete) {
    return (
      <WuuMascotRuntimeProvider>
        <ApplicationFirstRunOnboarding application={application} />
      </WuuMascotRuntimeProvider>
    );
  }

  return (
    <WuuMascotRuntimeProvider
      provider={activeThread?.model_provider || configuration?.provider}
      providers={mascotProviderNames}
      model={activeThread?.model || configuration?.model}
    >
      <SideConversationAvailableContext.Provider value={sideConversationAvailable}>
      <FileSelectionProvider application={application} source={{ ownerTabId: application.getSnapshot().drafts.split
        ? application.getSnapshot().drafts[conversationWorkspace.activePane].ownerTabId ?? tabs.activeTabId
        : application.getSnapshot().drafts.activeTabId }}>
      {!settingsNoticeVisible && failedDraftNotice}
      <ImagePreviewProvider>
      <ProjectActionsProvider application={application}>
      <WorkspaceBrowserOpenContext.Provider value={poppedOutMode || isTouchWebShell() ? undefined : openWorkspaceBrowserURL}>
      <ArtifactPreviewContext.Provider value={poppedOutMode || isTouchWebShell() ? undefined : openWorkspaceArtifactTab}>
        <div
          ref={appShellRef}
          tabIndex={-1}
          className={shellClassName}
          style={shellStyle}
          data-wuu-component="app-shell"
          data-settings-open={settingsOpen ? "" : undefined}
          data-web-workspace-open={showingWebPage && !settingsOpen ? "" : undefined}
          data-wuu-sidebar-mode={sidebarDrawerVisible ? "drawer" : sidebarDrawerMode ? "collapsed" : "docked"}
        >
          <BrowserPiPHostReporter />
          <div className="workbench-window-chrome" aria-hidden="true" />
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
          <HoverRevealScopeContext.Provider value={sidebarHoverScope}>
            <slots.Slot name={`${shellPluginId}/application.navigation`} context={{
              workspaceSelectionEnabled,
              sidebarCollapsed,
              sidebarVisible: !sidebarDrawerMode || sidebarDrawerVisible,
              onPointerEnter: openSidebarDrawer,
              onPointerLeave: (event: React.PointerEvent<HTMLElement>) => scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent),
              onRibbonPresenceChange: setNativeRibbonPresent,
              onRibbonExpandedChange: handleRibbonExpandedChange,
            }} />
          </HoverRevealScopeContext.Provider>

          {/* Hidden rather than unmounted: inserting or removing a shell child
              ahead of the conversation makes sibling selectors restyle every
              rendered turn when the sidebar collapses or expands. */}
          <div
            className="sidebar-resizer"
            hidden={sidebarDrawerMode}
            inert={rightPanelOpen && rightPanelGlobalized && !settingsOpen}
            role="separator"
            aria-label={t("app.resizeSidebar")}
            aria-orientation="vertical"
            aria-valuemin={SIDEBAR_MIN_WIDTH}
            aria-valuemax={SIDEBAR_MAX_WIDTH}
            aria-valuenow={sidebarWidth}
            tabIndex={0}
            onPointerDown={startSidebarResize}
            onPointerEnter={trackSidebarResizerPointer}
            onPointerMove={trackSidebarResizerPointer}
            onDoubleClick={toggleSidebar}
            onKeyDown={handleSidebarSeparatorKey}
          />
      <ConversationSearchOverlay
        state={conversationSearch}
        results={conversationSearchResults}
        threads={navigationWorkspace.threads.filter(thread => !thread.parent_id && !thread.ephemeral)}
        projects={navigationWorkspace.workspaces}
        activeThreadID={activeThreadID}
        pendingThreadID={visiblePendingThreadID}
        dialogRef={conversationSearchRef}
        inputRef={conversationSearchInputRef}
        onClose={closeConversationSearch}
        onQueryChange={setConversationSearchQuery}
        onClearQuery={clearConversationSearchQuery}
        onKeyDown={handleConversationSearchKeyDown}
        onSelectIndex={setConversationSearchSelectedIndex}
        onSelectResult={selectConversationSearchResult}
      />
            </>
          ) : null}

      <main
        inert={workspaceToolsCoverConversation || settingsOpen || showingWebsite}
        data-wuu-component="conversation-pane"
        data-primary-plugin-view={showingPrimaryPluginView ? "" : undefined}
        data-primary-plugin-web-page={showingPluginWebPage ? "" : undefined}
        data-native-section={showingManagementCatalog ? "catalog" : "conversations"}
        className={`conversation-pane${environmentPanelVisible ? " environment-panel-visible" : ""}${
          environmentPanelReserved ? " environment-panel-reserved" : ""
        }`}
        ref={conversationPaneRef}
      >
        <header className="titlebar" data-wuu-component="conversation-titlebar">
          <div className="title-block">
            {/* The ribbon owns a shell-level toggle. A hidden legacy toggle
                would still activate the hover drawer's layout placeholder. */}
            {sidebarToggleVisible && sidebarDrawerMode && !rightPanelGlobalized && !nativeRibbonPresent ? (
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
                onClick={toggleSidebar}
                onPointerEnter={scheduleSidebarDrawerOpen}
                onPointerLeave={(event) =>
                  scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent)
                }
              >
                <SidePanelToggleIcon side="left" open={sidebarDrawerVisible} />
              </button>
            ) : null}
            <ConversationTitleContent application={application} slots={slots} slotOwner={shellPluginId} />
          </div>
          <ConversationTitleActions
            thread={activeThread}
            pluginPageVisible={showingPrimaryPluginView || showingManagementCatalog}
            environmentToggleRef={environmentToggleRef}
            environmentPanelVisible={environmentPanelVisible}
            onToggleEnvironmentPanel={toggleEnvironmentPanel}
            rightPanelOpen={rightPanelOpen}
            onToggleRightPanel={toggleRightPanel}
            showRightPanelToggle={false}
          />
        </header>
        <ConversationSidePanels
          application={application}
          slots={slots}
          slotOwner={shellPluginId}
          environment={application.environment}
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

        {initialized && (catalogVisited || showingSkillsCatalog) ? (
          <div
            className="catalog-section-pane skills-scroll-region"
            hidden={!showingManagementCatalog || showingPrimaryPluginView}
            inert={!showingManagementCatalog || showingPrimaryPluginView}
          >
            <SettingsControlsProvider controls={application.settingsControls}>
            <SkillsCatalog
              active={!settingsOpen && showingManagementCatalog && !showingPrimaryPluginView}
              section={catalogSection}
              onSectionChange={setCatalogSection}
              selectedPluginID={catalogPluginID}
              onSelectPlugin={setCatalogPluginID}
              application={application}
            />
            </SettingsControlsProvider>
          </div>
        ) : null}
        <slots.Slot name={`${shellPluginId}/application.conversation`} context={{
          scrollRef: conversationScrollRef,
          contentRef: scrollContentRef,
          splitPaneRefs,
          visible: conversationVisible,
          restoring: conversationRestoring,
          onScroll: handleConversationScroll,
          onStreamFrame: scheduleStreamScroll,
          onCollapseComplete: handleTurnCollapseComplete,
          splitLeftPercent,
          onSplitResizeStart: startSplitResize,
          onSplitSeparatorKey: handleSplitSeparatorKey,
          onSplitSeparatorDoubleClick: resetSplitPercent,
        }} />

        {initialized && !splitConversation && !workspaceToolsCoverConversation && !showingPrimaryPluginView ? <slots.Slot name={`${shellPluginId}/application.composer`} context={{ variant: "dock", pane: "primary", containerRef: dockComposerRef }} /> : null}


        <ConversationStatusCluster
          application={application}
          visible={mainConversationDockVisible}
          clusterRef={statusClusterRef}
          navigation={!emptyConversation ? (
            <JumpToLatestPill
              containerRef={conversationScrollRef}
              bottomAnchor={dockComposerNode}
              scopeKey={activeThreadID}
              onJump={jumpConversationToLatest}
              inline
            />
          ) : null}
          hostStatus={projectAgentEnabled && activeProjectSessions.length > 0 && activeThread ? (
            <ProjectStatusCapsule project={activeThread} sessions={activeProjectSessions} />
          ) : undefined}
          threadId={activeThreadID}
        />
      </main>

      <>
      {!poppedOutMode && (rightPanelOpen || rightPanelAnimating) ? (
        <div
          className="workspace-right-panel-resizer"
          inert={rightPanelGlobalized || settingsOpen}
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
        <slots.Slot name={`${shellPluginId}/application.preview`} context={{ layout: {
          covered: settingsOpen || showingWebsite,
          open: rightPanelOpen,
          present: rightPanelOpen || rightPanelAnimating,
          prewarm: initialized,
          onDirtyFileTabsChange: rememberWorkspaceDirtyFiles,
          globalized: rightPanelGlobalized,
          conversationTabVisible: rightPanelGlobalized,
          sheetPhase: workspaceSheetPhase,
          onToggleGlobalize: toggleWorkspacePanelGlobalized,
          canExitGlobalized: !rightPanelAutoGlobalized || workspaceRightPanelDockableWithoutSidebar,
          browserOverlaySuppressed,
          documentComposer: documentComposerVisible,
          pane: conversationWorkspace.activePane,
        } }} />
      )}
      </>


      {!settingsOpen && environmentDialog === "commit" ? (
        <CommitChangesDialog
          gitStatus={gitStatus}
          branch={gitStatus?.branch}
          onCancel={() => setEnvironmentDialog(null)}
          onCommit={input => application.environment.commit(input)}
          onGenerateMessage={input => application.environment.generateCommitMessage(input)}
        />
      ) : null}
      {!settingsOpen && environmentDialog === "pull-request" ? (
        <PullRequestDialog
          gitStatus={gitStatus}
          disabledReason={pullRequestDisabledReason}
          onCancel={() => setEnvironmentDialog(null)}
          onCreate={input => application.environment.createPullRequest(input)}
        />
      ) : null}
      <slots.Slot name={`${shellPluginId}/application.settings`} context={{ layout: {
        onNoticeVisibilityChange: setSettingsNoticeVisible,
        browserSettingsAllowed: !poppedOutMode,
        embedded: embeddedSettings,
        embeddedDrawerPhase: sidebarDrawerPhase,
        onSidebarPointerEnter: openSidebarDrawer,
        onSidebarPointerLeave: event => scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent),
        sidebarWidth,
        resizingSidebar,
        shellRef: settingsShellRef,
        sidebarCollapsed,
        sidebarAnimating,
        onToggleSidebar: toggleSidebar,
        onSidebarResizeStart: startSidebarResize,
        onSidebarResizerPointer: trackSidebarResizerPointer,
        onSidebarSeparatorKey: handleSidebarSeparatorKey,
      } satisfies ApplicationSettingsLayoutV1 }} />
      {!poppedOutMode ? (
        <section className="web-workspace-stage" hidden={!showingWebsite || settingsOpen} aria-label={activeWebsite?.title}>
          <WebWorkspacePanel site={ribbonBrowser.state ? { id: WEB_WORKSPACE_RIBBON_ID, url: "about:blank", title: activeWebsite?.title } : undefined} active={showingWebsite && !settingsOpen && !accountOpen} tabs resizeWithWindow />
        </section>
      ) : null}
      <slots.Slot name={`${shellPluginId}/workbench.layers`} context={{ settingsOnly: settingsOpen }} />
      {!poppedOutMode && !isTouchWebShell() ? <PageHistoryControls {...pageHistory}
        hidden={(showingWebPage && !settingsOpen) || (application.desktop.platform === "darwin" && nativeRibbonPresent && (
          (!(showingWebPage && !settingsOpen) && sidebarDrawerPhase !== "closed")
          // The expanded ribbon covers the collapsed and browser header lanes.
          // Docked content starts beyond it, so its history remains available.
          || (nativeRibbonExpanded && (sidebarCollapsed || (showingWebPage && !settingsOpen)))
        ))}
      /> : null}
      {contextCapsule}
      {/* macOS applies app regions in DOM order, so a later drag titlebar would
        * cover this no-drag control. Keep it as the shell's last child. */}
      {!poppedOutMode ? (
        <button
          hidden={application.desktop.platform === "darwin" && nativeRibbonPresent && nativeRibbonExpanded}
          inert={application.desktop.platform === "darwin" && nativeRibbonPresent && nativeRibbonExpanded}
          className="icon-button side-panel-toggle-button sidebar-toggle-button sidebar-collapse-toggle section-sidebar-toggle"
          data-wuu-component="sidebar-toggle"
          type="button"
          aria-label={t(!sidebarCollapsed || sidebarDrawerVisible ? "app.collapseLeftSidebar" : "app.expandLeftSidebar")}
          aria-expanded={!sidebarCollapsed || sidebarDrawerVisible}
          aria-pressed={!sidebarCollapsed || sidebarDrawerVisible}
          onClick={() => { closeSidebarDrawer(); toggleSidebar(); }}
          onPointerLeave={(event) => scheduleSidebarDrawerCloseFromPointerLeave(event.nativeEvent)}
        >
          <SidePanelToggleIcon side="left" open={!sidebarCollapsed || sidebarDrawerVisible} />
        </button>
      ) : null}
      {!poppedOutMode ? (
        <button
          className="icon-button side-panel-toggle-button shell-right-sidebar-toggle"
          data-wuu-component="right-sidebar-toggle"
          type="button"
          aria-label={t(rightPanelOpen ? "shell.closeRightSidebar" : "shell.openRightSidebar")}
          aria-expanded={rightPanelOpen}
          aria-pressed={rightPanelOpen}
          onClick={toggleRightPanel}
        >
          <SidePanelToggleIcon side="right" open={rightPanelOpen} />
        </button>
      ) : null}
      </div>
    </ArtifactPreviewContext.Provider>
    </WorkspaceBrowserOpenContext.Provider>
    </ProjectActionsProvider>
    </ImagePreviewProvider>
    </FileSelectionProvider>
    </SideConversationAvailableContext.Provider>
    </WuuMascotRuntimeProvider>
  );
}
