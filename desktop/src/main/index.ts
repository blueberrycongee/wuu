import { readCatalogSkill } from "./remoteSkills";
import { RemoteAppServerBridge } from "./remoteAppServerBridge";
import { PhoneAccess, phonePairLink } from "./phoneAccess";
import {
  app,
  BrowserWindow,
  type BrowserWindowConstructorOptions,
  clipboard,
  dialog,
  ipcMain,
  type IpcMainInvokeEvent,
  Menu,
  nativeImage,
  nativeTheme,
  Notification,
  screen,
  session as electronSession,
  systemPreferences,
  type OpenDialogOptions,
  shell,
  WebContentsView,
} from "electron";
import { readFile, readdir, rm, stat } from "node:fs/promises";
import { dirname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import {
  APP_SERVER_PROTOCOL_VERSION,
  BROWSER_REVERSE_RPC_METHODS,
  MESSAGE_FLOW_FONT_SIZE_RANGE,
  isAppLocale,
  isLanguagePreference,
} from "../shared/protocol";
import type {
  ConfigAdvancedUpdateResult,
  ConfigGeneralUpdateResult,
  ConfigCodexModelsResult,
  ConfigModelCatalogRefreshResult,
  ConfigModelUpdateResult,
  EngineListResult,
  EngineUpdateParams,
  ExtensionCatalogRefreshResult,
  ExtensionPackageUpdateParams,
  ExtensionPackageUpdateResult,
  GitCommitParams,
  GitPullRequestParams,
  BuildInfoResult,
  ChannelAgentCreateParams,
  ChannelAgentCreateResult,
  ChannelAgentUpdateParams,
  ChannelAgentUpdateResult,
  ChannelAgentDeleteParams,
  ChannelAgentDeleteResult,
  ChannelSessionListParams,
  ChannelSessionListResult,
  ChannelSessionCreateParams,
  ChannelSessionRefParams,
  ChannelSessionSendParams,
  ChannelSessionResult,
  ChannelSessionReadResult,
  ChannelBootstrapResult,
  ChannelAgentListResult,
  ChannelAgentInsightsResult,
  ChannelAgentStartParams,
  ChannelAgentStartResult,
  ChannelAgentResetParams,
  ChannelAgentResetResult,
  ChannelAgentCreationResolveParams,
  ChannelAgentCreationResolveResult,
  ChannelMessageListParams,
  ChannelMessageListResult,
  ChannelMessageSendParams,
  ChannelMessageSendResult,
  ChannelRoomCreateParams,
  ChannelRoomCreateResult,
  ChannelDirectMessageOpenParams,
  ChannelDirectMessageOpenResult,
  ChannelRoomUpdateParams,
  ChannelRoomUpdateResult,
  ChannelRoomDeleteParams,
  ChannelRoomDeleteResult,
  ChannelRoomReadParams,
  ChannelRoomReadResult,
  ChannelRoomListResult,
  ChannelTaskCreateParams,
  ChannelTaskCreateResult,
  ChannelTaskUpdateParams,
  ChannelTaskUpdateResult,
  ChannelHumanMentionStatusResult,
  ChannelHumanMentionAckResult,
  ActivityActionResult,
  ActivityListResult,
  ActivityReleaseResult,
  ActivitySession,
  ActiveDocumentContext,
  CoreBuildInfo,
  DesktopBuildInfo,
  InputFile,
  CodexPetSettingsUpdate,
  InputImage,
  InitializeResult,
  InstructionsListResult,
  MCPListResult,
  MCPAuthFinishResult,
  MCPAuthRemoveResult,
  MCPAuthStartResult,
  MCPAuthStatusResult,
  AuthXAILoginStartResult,
  AuthXAILoginPollResult,
  MCPServerActionResult,
  ManagedProcessActionResult,
  ManagedProcessListResult,
  ManagedProcessReadParams,
  ManagedProcessReadResult,
  ManagedProcessWriteResult,
  RemoteControlSnapshot,
  RemoteControlStatus,
  ServerEvent,
  SkillContentParams,
  SkillContentResult,
  SkillListResult,
  RuntimeContext,
  RuntimeAdvancedSettingsUpdate,
  RuntimeGeneralSettingsUpdate,
  GitCommitMessageResult,
  SettingsUsageResponse,
  TerminalSessionStartParams,
  TextPolishResult,
  Thread,
  ThreadContextCompositionResult,
  ThreadEditMessageResult,
  ThreadForkResult,
  ThreadForkTarget,
  ThreadResumeResult,
  ThreadStartParams,
  Turn,
  UserQuestionAnswer,
  UserQuestionListResult,
  UserQuestionResolveResult,
  SystemNotificationParams,
  SystemNotificationResult,
  PopOutInitResult,
  PopOutSessionParams,
  PluginPackageInstallResult,
  PluginPackageRemoveResult,
  PluginDesktopModuleLoadResult,
  PluginDesktopModuleReadParams,
  PluginDesktopModuleReadResult,
  PluginIconLoadResult,
  PluginIconReadParams,
  PluginIconReadResult,
  PluginSettingGetParams,
  PluginIdentityParams,
  PluginSettingSetParams,
  PluginSettingResult,
  PluginDiagnosticsResult,
  PluginStorageGetParams,
  PluginStorageSetParams,
  PluginStorageResult,
  PluginClientRequestParams,
  PluginClientRequestResult,
  WorkspaceFileSaveParams,
  CodexPetHint,
  SideThreadOpenResult,
  SideThreadHistoryResult,
  SideThreadSendParams,
  SideThreadSendResult,
  VoiceInputSettings,
  VoiceInputSettingsSnapshot,
  ChannelRoomPreferences,
  VoicePermissionStatus,
} from "../shared/protocol";
import { AppServerClientPool } from "./appServerClients";
import { RendererServerEventBatcher } from "./rendererServerEventBatcher";
import { ObservationCoordinator } from "./cuaActivityWindows";
import { createObservationPiPFactory } from "./browserPiPWindow";
import { removeLegacyDesktopCliLink } from "./legacyCliLink";
import {
  defaultCodexPetsDir,
  ensureCodexPetsDir,
  legacyCodexPetsDir,
  loadCodexPetsSnapshot,
} from "./codexPets";
import { CodexPetWindowManager, type CodexPetRuntime } from "./codexPetWindow";
import { RemoteHostManager } from "./remoteControl";
import {
  getCodexPetSettings,
  getCodexPetScale,
  getCodexPetSize,
  getMessageFlowFontSize,
  getThemePreference,
  getLanguagePreference,
  getPluginConflictPreferences,
  getVoiceInputSettings,
  getChannelRoomPreferences,
  isOnboardingComplete,
  completeOnboarding,
  setCodexPetSettings,
  setMessageFlowFontSize,
  setThemePreference,
  setLanguagePreference,
  setPluginConflictPreference,
  setVoiceInputSettings,
  setChannelRoomPreferences,
  type MessageFlowFontSize,
  type ThemePreference,
  type LanguagePreference,
} from "./desktopSettings";
import { GitService } from "./gitService";
import { requestRemoteProjects } from "./remoteProjects";
import { requestRemoteTerminal } from "./remoteTerminal";
import { requestRemoteFiles } from "./remoteFiles";
import { requestRemoteGit } from "./remoteGit";
import { openExternalURL, wireExternalNavigationGuards } from "./externalNavigation";
import { ProjectManager, wuuHomePath } from "./projects";
import { mainTranslate, resolveMainLocale, setMainLocale } from "./i18n";
import { sideThreadEventFromServerEvent } from "./sideThreadEvents";
import { createSpeechRecognitionService } from "./speechRecognition";
import {
  registerRenderableFileProtocol,
  registerRenderableFileScheme,
} from "./renderableFileProtocol";
import {
  cachePluginDesktopModule,
  cachePluginIcon,
  registerPluginModuleProtocol,
  registerPluginModuleScheme,
} from "./pluginModuleProtocol";
import { TerminalSessionManager } from "./terminalSessions";
import { WorkspaceFileService } from "./workspaceFiles";
import {
  macWorkspaceItemMenuTemplate,
  macWorkspaceApplications,
  openMacWorkspaceItemWithApplication,
} from "./macWorkspaceApplications";

import {
  appShellWebPreferences,
  installProductionAppShellGuards,
  productionApplicationMenuTemplate,
} from "./appShellGuards";
import { createWindowRegistry, type WindowRegistry } from "./windowRegistry";
import {
  BrowserHostCoordinator,
  BROWSER_PARTITION,
  configureBrowserProxy,
  type BrowserHostWindowHandle,
  type BrowserParentWindowHandle,
  type BrowserViewHandle,
  defaultBrowserHostDeps,
  installBrowserSessionHandlers,
} from "./browserHostWindows";
import {
  computeDefaultMainWindowBounds,
  computeOnboardingWindowBounds,
  loadMainWindowBounds,
  saveMainWindowBounds,
} from "./windowState";
const __dirname = dirname(fileURLToPath(import.meta.url));
const DEV_CACHE_CLEANUP_THRESHOLD_BYTES = 512 * 1024 * 1024;
const DEV_CACHE_DIRECTORIES = ["Cache", "Code Cache", "GPUCache", "DawnCache"];
const DEFAULT_WINDOW_BACKGROUND = "#f6f6f4";
// Dark-theme counterpart, matching --paper in theme.css. Used on Windows
// where the window fill (not a vibrancy material) is what shows behind the
// transparent web layer — a dark-theme launch must not flash white.
const DARK_WINDOW_BACKGROUND = "#1d2024";
// Matches the renderer titlebar row (48px in the tabbed/popped-out states)
// so the overlay buttons center on the same strip the renderer draws.
const WINDOWS_TITLEBAR_OVERLAY_HEIGHT = 48;
const ENABLE_EMBEDDED_BROWSER =
  !app.isPackaged && process.env.WUU_ENABLE_BROWSER === "1";
const ENABLE_VOICE_INPUT =
  !app.isPackaged && process.env.VITE_ENABLE_VOICE_INPUT === "true";
if (process.argv.includes("--safe-mode")) {
  process.env.WUU_SAFE_MODE = "1";
}
registerRenderableFileScheme();
registerPluginModuleScheme();

let mainWindow: BrowserWindow | null = null;
// Live system notifications are kept referenced so the OS cannot collect
// them before the user acts on them (Electron retains only while referenced).
const activeSystemNotifications = new Set<Notification>();
const windowRegistry: WindowRegistry = createWindowRegistry();
const projectManager = new ProjectManager();
const speechRecognitionService = createSpeechRecognitionService({
  askForMicrophoneAccess: () =>
    systemPreferences.askForMediaAccess("microphone"),
});

// Build-time globals injected by electron.vite.config.ts. TypeScript
// doesn't know about them by default; declare them so we can reference
// them inside main. The "undefined" type keeps the same declaration
// valid for unit-test contexts where the define hasn't run.
declare const __DESKTOP_VERSION__: string | undefined;
declare const __DESKTOP_BUILD_DATE__: string | undefined;

const DESKTOP_BUILD_INFO: DesktopBuildInfo = {
  version:
    typeof __DESKTOP_VERSION__ === "string"
      ? __DESKTOP_VERSION__
      : "0.0.0-test",
  date:
    typeof __DESKTOP_BUILD_DATE__ === "string"
      ? __DESKTOP_BUILD_DATE__
      : "1970-01-01T00:00:00Z",
};

// Cached core build info. Populated on the first wuu:initialize call so
// the renderer can ask for build identity via wuu:build-info without
// racing the first initialize.
let cachedCoreBuildInfo: CoreBuildInfo | undefined;
let windowResizeEndTimer: NodeJS.Timeout | undefined;
let windowResizeState = false;
// Debounce timer for persisting the main window bounds on resize end. The
// 200ms delay matches windowResizeEndTimer so the two callbacks fire
// together — a single "user finished resizing" tick writes once.
let mainWindowBoundsSaveTimer: NodeJS.Timeout | undefined;
const appServerClientPool = new AppServerClientPool(
  () => projectManager.ensureRuntimeContext(),
  () => projectManager.activeWorkdir(),
  (event) => emitServerEvent(event),
);
// Cross-workdir running state (which sessions are actively turning in any
// workspace) is aggregated in the main process from each client's own turn
// lifecycle tracking and broadcast to the renderer on change. The renderer's
// sidebar uses it to show spinners for non-active workspaces without depending
// on event routing that is filtered to the active context.
appServerClientPool.setRunningThreadsChangedHandler((snapshot) =>
  broadcastToAll("wuu:running-threads-changed", snapshot),
);
// Owns every agent-driven embedded browser tab (hidden WebContentsView + CDP
// bridge). Reverse-RPC browser/* requests are intercepted in emitServerEvent
// (below) and answered here via the pool's single-shot reply channel. The view
// factory keeps real Electron out of unit tests.
const browserHostCoordinator = new BrowserHostCoordinator(
  windowRegistry,
  {
    respond: (id, result) => appServerClientPool.respondToServerRequest(id, result),
    reject: (id, message) => appServerClientPool.rejectServerRequest(id, message),
  },
  defaultBrowserHostDeps(
    () =>
      new BrowserWindow({
        show: false,
        skipTaskbar: true,
        frame: false,
        webPreferences: {
          contextIsolation: true,
          nodeIntegration: false,
          sandbox: true,
        },
      }) as unknown as BrowserHostWindowHandle,
    () =>
      new WebContentsView({
        webPreferences: {
          partition: BROWSER_PARTITION,
          contextIsolation: true,
          nodeIntegration: false,
        },
      }) as unknown as BrowserViewHandle,
  ),
  (workdir) => broadcastToAll("wuu:browser-invalidate", { workdir }),
);
// A workdir with a live agent tab counts as busy so pool idle-eviction cannot
// yank the page out mid user-takeover (isBusy only sees pending requests +
// running turns, not outstanding core→desktop browser work).
appServerClientPool.setWorkdirPinnedCheck((workdir) =>
  browserHostCoordinator.hasAgentTabs(workdir),
);
// Client teardown sink: destroy that workdir's views. Not server-exit driven —
// the disposing/eviction path suppresses server-exit, so this is the authority.
appServerClientPool.setClientTorndownHandler((workdir) => {
  browserHostCoordinator.onClientTorndown(workdir);
  observationCoordinator.dropWorkdir(workdir);
});
// The single observation surface for agent live activity (CUA native PiP +
// browser preview), fed by the same activity notifications the renderer uses.
// The factory picks the surface per activity kind; the coordinator owns
// lifecycle, single-instance discipline, and position memory.
const observationCoordinator = new ObservationCoordinator(
  windowRegistry,
  async (threadID): Promise<ActivitySession[]> => {
    const workdir = projectManager.activeWorkdir();
    if (!workdir) return [];
    const result = await appServerClientPool.requestForWorkdir<ActivityListResult>(
      workdir,
      "activity/list",
      { thread_id: threadID },
    );
    return result.activities ?? [];
  },
  createObservationPiPFactory({ browserHost: browserHostCoordinator, isPackaged: app.isPackaged }),
);
// The pet is a standalone always-on-top window owned by the main process, so
// it stays on the desktop when the main window is hidden or minimized. Its
// right-click menu disables the setting, which also tears the window down.
// The jump callback fires when the user clicks the pet's session bubble:
// the main window is brought forward and the renderer is told to switch
// to that thread via the `wuu:codex-pet-jump` broadcast.
const codexPetWindowManager = new CodexPetWindowManager(
  () => {
    updateCodexPetSettings({ enabled: false });
  },
  (hint) => {
    if (mainWindow && !mainWindow.isDestroyed()) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.show();
      mainWindow.focus();
    }
    broadcastToAll("wuu:codex-pet-jump", { thread_id: hint.thread_id });
  },
  (size) => {
    // Context-menu size change — push the choice into desktop-settings.json
    // and refresh the snapshot so the next sync re-stages the window with
    // the new size (CodexPetWindowManager already called applyView).
    updateCodexPetSettings({ size });
  },
  (scale) => {
    // Continuous-resize drag ended (commit=1) — persist the raw scale so
    // the pet reopens at exactly the size the user left it. Intermediate
    // per-frame scale updates never reach this callback.
    updateCodexPetSettings({ scale });
  },
  app.isPackaged,
);
const terminalSessionManager = new TerminalSessionManager(
  (windowID, event) => emitTerminalEvent(windowID, event),
);

async function showProjectDirectoryDialog(
  options: OpenDialogOptions,
): Promise<string | undefined> {
  const focusedWindow = BrowserWindow.getFocusedWindow();
  const result = focusedWindow && !focusedWindow.isDestroyed()
    ? await dialog.showOpenDialog(focusedWindow, options)
    : await dialog.showOpenDialog(options);
  if (result.canceled) {
    return undefined;
  }
  return result.filePaths[0];
}

function runtimeContextForEvent(event: IpcMainInvokeEvent): RuntimeContext {
  return (
    windowRegistry.runtimeContextForWindow(event.sender.id) ??
    projectManager.ensureRuntimeContext()
  );
}

function appServerRequest<T>(
  event: IpcMainInvokeEvent,
  method: string,
  params?: unknown,
): Promise<T> {
  const context = windowRegistry.runtimeContextForWindow(event.sender.id);
  return context
    ? appServerClientPool.requestInContext<T>(context, method, params)
    : appServerClientPool.request<T>(method, params);
}

function runtimeContextForWorkspaceID(workspaceID: string): RuntimeContext {
  const id = workspaceID.trim();
  if (!id) {
    throw new Error("workspace id is required");
  }
  const project = projectManager.list().projects.find(
    (candidate) => candidate.id === id,
  );
  if (!project) {
    throw new Error(`workspace ${id} is not registered`);
  }
  if (project.missing) {
    throw new Error(`workspace ${project.name} is unavailable`);
  }
  return { kind: "project", project_id: project.id, cwd: project.path };
}

function codexPetDirs(): string[] {
  const primaryPetsDir = defaultCodexPetsDir(wuuHomePath());
  ensureCodexPetsDir(primaryPetsDir);
  return [primaryPetsDir, legacyCodexPetsDir()];
}

function codexPetsSnapshot() {
  return loadCodexPetsSnapshot({
    petsDirs: codexPetDirs(),
    settings: getCodexPetSettings(),
  });
}

function updateCodexPetSettings(update: CodexPetSettingsUpdate) {
  const current = getCodexPetSettings();
  const requested = {
    enabled: update.enabled ?? current.enabled,
    selected_id: update.selected_id ?? current.selected_id,
    size: update.size ?? current.size,
    // 显式选 size 档位意味着放弃之前拖出来的连续 scale——两者同时在场时
    // scale 优先，所以这里必须把 scale 清掉档位才生效。
    scale: update.size !== undefined ? undefined : update.scale ?? current.scale,
  };
  const snapshot = loadCodexPetsSnapshot({
    petsDirs: codexPetDirs(),
    settings: requested,
  });
  setCodexPetSettings({
    enabled: snapshot.enabled,
    selected_id: snapshot.selected_id,
    size: requested.size,
    scale: requested.scale,
  });
  codexPetWindowManager.sync(snapshot);
  return snapshot;
}

function gitServiceForEvent(event: IpcMainInvokeEvent): GitService {
  return gitServiceForContext(() => runtimeContextForEvent(event));
}

function gitServiceForContext(getContext: () => RuntimeContext): GitService {
  return new GitService(
    getContext,
    () => appServerClientPool.runningThreadCwds(),
    () => {
      const context = getContext();
      return appServerClientPool.threadCwdsForWorkdir(context.cwd);
    },
    async (context, input) => {
      const result = await appServerClientPool.requestInContext<GitCommitMessageResult>(
        context,
        "git/commit-message",
        input,
      );
      return result.message ?? "";
    },
  );
}

function workspaceFilesForEvent(event: IpcMainInvokeEvent): WorkspaceFileService {
  return new WorkspaceFileService(() => runtimeContextForEvent(event));
}

const rendererServerEventBatcher = new RendererServerEventBatcher((event) => {
  broadcastToAll("wuu:server-event", event);
});

function emitServerEvent(event: ServerEvent): void {
  remoteAppServerBridge.publish(event);
  // Intercept core→desktop browser/* requests BEFORE broadcastToAll: the
  // renderer auto-rejects every server-request ("unsupported server request"),
  // and server-request routes are single-shot, so letting the renderer race
  // would reject the route before the main-process handler can answer it. Cheap
  // prefix test on the hot stdout path.
  if (
    ENABLE_EMBEDDED_BROWSER &&
    event.kind === "server-request" &&
    event.message.method.startsWith("browser/")
  ) {
    void browserHostCoordinator.handleServerRequest(event);
    return;
  }
  // A crashed core (server-exit that still emits, i.e. not the disposing path)
  // tears down that workdir's browser views here. markServerExit alone would
  // only stop a late reply from respawning the core, leaking the hidden
  // WebContentsViews — and because a workdir with live tabs is pinned
  // non-evictable, the dead client would never reach disposeClient's teardown.
  // onClientTorndown destroys the views (clearing the pin), marks the workdir
  // down so respondSafe drops late replies, and broadcasts invalidation.
  // Idempotent with a later disposeClient.
  if (event.kind === "server-exit") {
    browserHostCoordinator.onClientTorndown(event.workdir);
    observationCoordinator.dropWorkdir(event.workdir);
  }
  observationCoordinator.handleServerEvent(event);
  const sideThreadEvent = sideThreadEventFromServerEvent(event);
  if (sideThreadEvent) {
    broadcastToAll("wuu:side-thread-event", sideThreadEvent);
  }
  rendererServerEventBatcher.enqueue(event);
}

function broadcastToAll(channel: string, payload: unknown): void {
  for (const window of windowRegistry.allWindows()) {
    if (window.isDestroyed() || window.webContents.isDestroyed()) {
      continue;
    }
    window.webContents.send(channel, payload);
  }
}

function emitTerminalEvent(
  windowID: number,
  event: Parameters<TerminalSessionManager["emit"]>[1],
): void {
  const window = windowRegistry.windowForID(windowID);
  if (
    !window ||
    window.isDestroyed() ||
    window.webContents.isDestroyed()
  ) {
    return;
  }
  window.webContents.send("wuu:terminal-event", event);
}

function unregisterWindow(windowID: number): void {
  terminalSessionManager.stopForOwner(windowID);
  windowRegistry.unregisterWindow(windowID);
}

// Remote-control host: one machine-global daemon serving paired phones,
// independent of the per-workdir app-server pool. Events (pairing URI,
// paired, exit) fan out to every window; the settings panel re-pulls its
// snapshot on each one.
const remoteTerminals = new TerminalSessionManager((owner,event)=>remoteAppServerBridge.notifyPeer(owner,"desktop/terminal/event",event));
const remoteAppServerBridge = new RemoteAppServerBridge(async (workdir, method, params, reply, peerID) => {
  // Keep the desktop's reverse-RPC capabilities: a phone attachment must not
  // disable browser tools on the shared execution service.
  if (method === "initialize") params = {
    protocol_version: APP_SERVER_PROTOCOL_VERSION,
    client: { name: "wuu-desktop", version: DESKTOP_BUILD_INFO.version },
    capabilities: { reverse_rpc: { methods: [...BROWSER_REVERSE_RPC_METHODS] } },
  };
  if (method === "shutdown") throw new Error("Remote clients cannot shut down the shared execution service");
  if (method.startsWith("desktop/projects/")) return requestRemoteProjects(projectManager,method,params);
  const cwd = resolve(workdir);
  const state = projectManager.list();
  const project = state.projects.find(project => resolve(project.path) === cwd && !project.missing);
  let context: RuntimeContext;
  if (project) context = { kind: "project", project_id: project.id, cwd: project.path };
  else if (state.projects.some(project=>appServerClientPool.threadCwdsForWorkdir(project.path).some(path=>resolve(path)===cwd))) context={kind:"no_project",cwd};
  else if (state.active_context?.kind === "no_project" && resolve(state.active_context.cwd) === cwd) context = state.active_context;
  else if (cwd.startsWith(resolve(wuuHomePath(), "scratch") + sep)) context = { kind: "no_project", cwd };
  else throw new Error("Unknown or unavailable remote workspace");
  if (method.startsWith("desktop/terminal/")) return requestRemoteTerminal(remoteTerminals,context,peerID,method,params);
  if (method === "desktop/skill/content") return readCatalogSkill(await appServerClientPool.requestInContext<SkillListResult>(context,"skill/list"),params as SkillContentParams);
  if (method.startsWith("desktop/file/")) return requestRemoteFiles(context,method,params);
  if (method.startsWith("desktop/git/")) return requestRemoteGit(gitServiceForContext(() => context), method, params);
  return appServerClientPool.requestInContext(context, method, params, reply);
}, peerID=>remoteTerminals.stopForOwner(peerID));

const remoteHostManager = new RemoteHostManager({
  appServerEndpoint: () => remoteAppServerBridge.currentEndpoint(),
  onEvent: (event) => broadcastToAll("wuu:remote-event", event),
});

const phoneAccess = new PhoneAccess(remoteHostManager,
  app.isPackaged ? join(process.resourcesPath, "mobile-web") : resolve(__dirname, "../../build/mobile-web"),
  () => broadcastToAll("wuu:remote-event", { kind: "host-log", message: "Phone access changed" }), remoteAppServerBridge);

async function remoteControlSnapshot(workdir: string): Promise<RemoteControlSnapshot> {
  let status: RemoteControlStatus | null = null;
  let statusError = "";
  try {
    status = await remoteHostManager.status(workdir);
  } catch (err) {
    statusError = err instanceof Error ? err.message : String(err);
  }
  return {
    status,
    status_error: statusError || phoneAccess.error() || undefined,
    host_running: remoteHostManager.isRunning(),
    host_enabled: phoneAccess.enabled(),
    pair_uri: phonePairLink(phoneAccess.url(), remoteHostManager.currentPairUri()),
    web_url: phoneAccess.url(),
  };
}

function setWindowResizeState(resizing: boolean): void {
  if (windowResizeState === resizing) {
    return;
  }
  windowResizeState = resizing;
  broadcastToAll("wuu:window-resize-state", { resizing });
}

function scheduleWindowResizeEnd(delay = 140): void {
  if (windowResizeEndTimer) {
    clearTimeout(windowResizeEndTimer);
  }
  windowResizeEndTimer = setTimeout(() => {
    windowResizeEndTimer = undefined;
    setWindowResizeState(false);
  }, delay);
}

function scheduleMainWindowBoundsSave(win: BrowserWindow, delay = 200): void {
  if (mainWindowBoundsSaveTimer) {
    clearTimeout(mainWindowBoundsSaveTimer);
  }
  mainWindowBoundsSaveTimer = setTimeout(() => {
    mainWindowBoundsSaveTimer = undefined;
    persistMainWindowBoundsNow(win);
  }, delay);
}

function persistMainWindowBoundsNow(win: BrowserWindow): void {
  if (win.isDestroyed()) return;
  // Maximised / fullscreen bounds are full-rect; restoring to them on the
  // next launch would skip the user's normal layout. Skip the write so the
  // last non-maximised bounds stay saved.
  if (win.isMinimized()) return;
  if (win.isMaximized() || win.isFullScreen()) return;
  saveMainWindowBounds(win.getBounds());
}

function loadRenderer(window: BrowserWindow): void {
  if (!app.isPackaged) {
    window.webContents.on("console-message", (_event, _level, message) => {
      if (message) {
        console.error(`[renderer] ${message}`);
      }
    });
    window.webContents.on("preload-error", (_event, preloadPath, error) => {
      console.error(`[preload] ${preloadPath}: ${error.message}`);
    });
  }

  const devRendererURL = !app.isPackaged ? process.env.ELECTRON_RENDERER_URL : undefined;
  const rendererPath = join(__dirname, "../renderer/index.html");
  const rendererURL = devRendererURL ?? pathToFileURL(rendererPath).toString();
  wireExternalNavigationGuards(window, {
    rendererURL,
    openExternal: openExternalNavigation,
  });

  if (devRendererURL) {
    window.webContents.on(
      "did-fail-load",
      (_event, errorCode, errorDescription, validatedURL, isMainFrame) => {
        if (!isMainFrame || errorCode === -3) return;
        console.error(
          `[renderer] failed to load ${validatedURL || devRendererURL}: ${errorDescription} (${errorCode})`,
        );
        app.quit();
      },
    );
    void window.loadURL(devRendererURL).catch(() => {
      // did-fail-load logs the Chromium error and shuts down the stale dev host.
    });
  } else {
    void window.loadFile(rendererPath);
  }
}

function openExternalNavigation(rawURL: unknown): Promise<boolean> {
  return openExternalURL(rawURL, (url) => shell.openExternal(url));
}

function mainWindowMaterialOptions(): Pick<
  BrowserWindowConstructorOptions,
  "backgroundColor" | "transparent" | "vibrancy" | "visualEffectState"
> {
  if (process.platform !== "darwin") {
    return { backgroundColor: windowBackgroundColor() };
  }
  return {
    backgroundColor: "#00000000",
    transparent: true,
    vibrancy: "under-window",
    visualEffectState: "active",
  };
}

function resolvedThemeIsDark(): boolean {
  const preference = getThemePreference();
  if (preference === "system") return nativeTheme.shouldUseDarkColors;
  return preference === "dark";
}

// Keep Chromium's internal color scheme (used by the built-in PDF viewer,
// form controls, scrollbars, and DevTools) in lockstep with the app-global
// theme preference. Without this the PDF preview follows the OS theme even
// when the user has pinned Wuu to light/dark, producing the black PDF chrome
// seen on dark-mode macOS while the rest of the app stays light.
function syncNativeThemeSource(): void {
  const preference = getThemePreference();
  nativeTheme.themeSource = preference === "system" ? "system" : preference;
}

// Pre-paint window fill. macOS keeps the fixed light color (the vibrancy
// material paints over it); elsewhere the fill IS the visible backdrop, so
// it follows the stored theme.
function windowBackgroundColor(): string {
  if (process.platform === "darwin") return DEFAULT_WINDOW_BACKGROUND;
  return resolvedThemeIsDark() ? DARK_WINDOW_BACKGROUND : DEFAULT_WINDOW_BACKGROUND;
}

// The window-chrome contract per platform: macOS hides the titlebar and
// leaves the traffic lights over the renderer's drag strip (top-left);
// Windows and Linux hide it and let Chromium draw min/max/close as a
// controls overlay (top-right — the renderer reserves that corner through
// the --window-controls-inset-* variables). Other platforms keep the
// native frame, which needs no in-page reservation at all.
//
// Linux WCO has had DE/Wayland regressions in Electron; set
// WUU_LINUX_NATIVE_CHROME=1 to force the previous native-frame path.
function usesWindowControlsOverlay(): boolean {
  if (process.platform === "win32") return true;
  if (process.platform === "linux") {
    return process.env.WUU_LINUX_NATIVE_CHROME !== "1";
  }
  return false;
}

function windowFrameOptions(): Pick<
  BrowserWindowConstructorOptions,
  "titleBarStyle" | "trafficLightPosition" | "titleBarOverlay" | "autoHideMenuBar"
> {
  if (process.platform === "darwin") {
    return {
      titleBarStyle: "hiddenInset",
      trafficLightPosition: { x: 18, y: 15 },
    };
  }
  if (usesWindowControlsOverlay()) {
    return {
      titleBarStyle: "hidden",
      titleBarOverlay: nonMacTitleBarOverlay(),
      // Linux otherwise shows an always-visible in-window menu strip under
      // the (now hidden) system titlebar; Alt still reveals the menu.
      ...(process.platform === "linux" ? { autoHideMenuBar: true } : {}),
    };
  }
  return {};
}

function nonMacTitleBarOverlay(): Electron.TitleBarOverlay {
  const dark = resolvedThemeIsDark();
  return {
    // Track the themed window fill so the button strip reads as part of
    // the titlebar; symbol colors mirror the --ink text tokens.
    color: dark ? DARK_WINDOW_BACKGROUND : DEFAULT_WINDOW_BACKGROUND,
    symbolColor: dark ? "#e4e6e8" : "#1f2328",
    height: WINDOWS_TITLEBAR_OVERLAY_HEIGHT,
  };
}

// The theme preference is app-global state owned by the main process.
// Every themed content window (main + pop-outs) registers here; a theme
// change — explicit preference or an OS dark-mode flip while on
// "system" — re-pushes the native chrome (Win/Linux controls overlay,
// non-macOS window background fill) to all of them, and the new
// preference is broadcast so each renderer re-applies data-theme.
// macOS skips both: its vibrancy material and transparent fill are
// theme-independent.
const themedChromeWindows = new Set<BrowserWindow>();

function registerThemedChromeWindow(win: BrowserWindow): void {
  themedChromeWindows.add(win);
  win.on("closed", () => {
    themedChromeWindows.delete(win);
  });
}

function syncThemedWindowChrome(): void {
  if (process.platform === "darwin") return;
  const background = windowBackgroundColor();
  const overlay = usesWindowControlsOverlay() ? nonMacTitleBarOverlay() : undefined;
  for (const win of themedChromeWindows) {
    if (win.isDestroyed()) continue;
    // Windows redraws the controls overlay only when told to, and every
    // platform keeps the creation-time backgroundColor until re-set —
    // both must follow the theme or the window frame lags the content.
    win.setBackgroundColor(background);
    if (overlay) {
      win.setTitleBarOverlay(overlay);
    }
  }
}

function broadcastThemePreference(): void {
  broadcastToAll("wuu:theme-preference-changed", getThemePreference());
}

function broadcastLanguagePreference(): void {
  broadcastToAll("wuu:language-preference-changed", getLanguagePreference());
}

function broadcastVoiceInputSettings(): void {
  broadcastToAll("wuu:voice-input-settings-changed", getVoiceInputSettings());
}

function microphonePermissionStatus(): VoicePermissionStatus {
  if (process.platform !== "darwin") return "unavailable";
  const status = systemPreferences.getMediaAccessStatus("microphone");
  if (status === "not-determined") return "not_determined";
  if (
    status === "granted" ||
    status === "denied" ||
    status === "restricted" ||
    status === "unknown"
  ) {
    return status;
  }
  return "unknown";
}

function syncThemeAcrossWindows(): void {
  syncNativeThemeSource();
  syncThemedWindowChrome();
  broadcastThemePreference();
}

type PopOutWindowParams =
  | {
      kind: "thread";
      threadID: string;
      context: RuntimeContext;
      sourceWindow?: BrowserWindow | null;
    }
  | {
      kind: "draft";
      context: RuntimeContext;
      sourceWindow?: BrowserWindow | null;
    };

function createPopOutWindow(params: PopOutWindowParams): BrowserWindow {
  // Plan §2.2 #1 (vs Reviewer S1): cursor position is read by main
  // process via `screen.getCursorScreenPoint()` so the preload bridge
  // never sees the `screen` Electron API. Combined with `getBounds()`
  // we compute the new window position without exposing platform APIs
  // to the renderer.
  // Cast: some Electron typings bundle cursor methods on the namespace
  // value rather than the `Screen` interface; both exist at runtime.
  const cursor = (screen as unknown as { getCursorScreenPoint(): { x: number; y: number } }).getCursorScreenPoint();
  const display = (screen as unknown as { getDisplayNearestPoint(point: { x: number; y: number }): { workArea: { x: number; y: number; width: number; height: number } } }).getDisplayNearestPoint(cursor);
  const workArea = display.workArea;
  const sourceBounds = params.sourceWindow?.isDestroyed()
    ? undefined
    : params.sourceWindow?.getBounds();
  const winWidth = Math.max(
    720,
    Math.min(sourceBounds?.width ?? 800, workArea.width),
  );
  const winHeight = Math.max(
    560,
    Math.min(sourceBounds?.height ?? 600, workArea.height),
  );
  const x = Math.max(
    workArea.x,
    Math.min(cursor.x - winWidth / 2, workArea.x + workArea.width - winWidth),
  );
  const y = Math.max(
    workArea.y,
    Math.min(cursor.y - 20, workArea.y + workArea.height - winHeight),
  );

  const placeholderTitle =
    params.kind === "thread"
      ? `wuu · ${params.threadID.slice(0, 8)}`
      : `wuu · ${mainTranslate("conversation")}`;
  const win = new BrowserWindow({
    width: winWidth,
    height: winHeight,
    x,
    y,
    ...windowFrameOptions(),
    backgroundColor: windowBackgroundColor(),
    title: placeholderTitle,
    webPreferences: {
      preload: join(__dirname, "../preload/index.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      webviewTag: true,
      scrollBounce: process.platform === "darwin",
      // Enables Chromium's built-in PDF viewer for workspace PDF previews.
      plugins: true,
      ...appShellWebPreferences(app.isPackaged),
    },
  });
  const windowID = win.webContents.id;
  registerThemedChromeWindow(win);

  windowRegistry.registerWindow(win, "popped-out", {
    workdir: params.context.cwd,
    runtimeContext: params.context,
    threadID: params.kind === "thread" ? params.threadID : undefined,
  });
  windowRegistry.attachResizeHandlers(win, () => {
    setWindowResizeState(true);
    scheduleWindowResizeEnd();
  });
  if (params.kind === "thread") {
    windowRegistry.setThreadWindow(params.threadID, windowID);
  }
  win.on("closed", () => {
    unregisterWindow(windowID);
  });

  loadRenderer(win);
  return win;
}

function createAccountWindow(context: RuntimeContext): void {
  const existing = windowRegistry.allWindows().find(win =>
    !win.isDestroyed() && windowRegistry.roleForWindow(win.webContents.id) === "account");
  if (existing) {
    if (existing.isMinimized()) existing.restore();
    existing.show(); existing.focus();
    return;
  }
  const { workArea } = screen.getDisplayNearestPoint(screen.getCursorScreenPoint());
  const win = new BrowserWindow({
    width: Math.min(640, workArea.width),
    height: Math.min(780, workArea.height),
    minWidth: Math.min(440, workArea.width),
    minHeight: Math.min(560, workArea.height),
    show: false,
    ...windowFrameOptions(),
    backgroundColor: windowBackgroundColor(),
    title: "Wuu",
    webPreferences: {
      preload: join(__dirname, "../preload/index.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      additionalArguments: ["--wuu-account-window"],
      ...appShellWebPreferences(app.isPackaged),
    },
  });
  const id = win.webContents.id;
  windowRegistry.registerWindow(win, "account", { runtimeContext: context, workdir: context.cwd });
  registerThemedChromeWindow(win);
  win.once("ready-to-show", () => { win.show(); win.focus(); });
  win.on("closed", () => unregisterWindow(id));
  loadRenderer(win);
}

function createWindow(): void {
  const primaryDisplay = screen.getPrimaryDisplay();
  const onboardingComplete = isOnboardingComplete();
  const restoredBounds = onboardingComplete
    ? loadMainWindowBounds(screen.getAllDisplays())
    : null;
  const defaultSize = onboardingComplete
    ? computeDefaultMainWindowBounds(primaryDisplay.workArea)
    : computeOnboardingWindowBounds(primaryDisplay.workArea);
  const width = restoredBounds?.width ?? defaultSize.width;
  const height = restoredBounds?.height ?? defaultSize.height;

  const windowOptions: BrowserWindowConstructorOptions = {
    width,
    height,
    resizable: onboardingComplete,
    ...windowFrameOptions(),
    ...mainWindowMaterialOptions(),
    webPreferences: {
      preload: join(__dirname, "../preload/index.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      webviewTag: true,
      // AppKit owns trackpad contact, resistance, and release on macOS.
      // Individual conversation scrollers opt in with overscroll-behavior.
      scrollBounce: process.platform === "darwin",
      // Enables Chromium's built-in PDF viewer for workspace PDF previews.
      plugins: true,
      ...appShellWebPreferences(app.isPackaged),
    },
  };
  if (restoredBounds) {
    windowOptions.x = restoredBounds.x;
    windowOptions.y = restoredBounds.y;
  }

  mainWindow = new BrowserWindow(windowOptions);
  registerThemedChromeWindow(mainWindow);

  windowRegistry.registerWindow(mainWindow, "main");
  const win = mainWindow;
  const windowID = win.webContents.id;

  windowRegistry.attachResizeHandlers(win, () => {
    setWindowResizeState(true);
    scheduleWindowResizeEnd();
    scheduleMainWindowBoundsSave(win);
  });
  win.on("close", () => {
    // Last-write-wins: cancel any pending debounce and persist synchronously
    // before the window is destroyed.
    if (mainWindowBoundsSaveTimer) {
      clearTimeout(mainWindowBoundsSaveTimer);
      mainWindowBoundsSaveTimer = undefined;
    }
    persistMainWindowBoundsNow(win);
  });
  win.on("closed", () => {
    if (windowResizeEndTimer) {
      clearTimeout(windowResizeEndTimer);
      windowResizeEndTimer = undefined;
    }
    if (mainWindowBoundsSaveTimer) {
      clearTimeout(mainWindowBoundsSaveTimer);
      mainWindowBoundsSaveTimer = undefined;
    }
    windowResizeState = false;
    observationCoordinator.setActiveThread(undefined);
    unregisterWindow(windowID);
    mainWindow = null;
  });
  loadRenderer(win);
}

async function clearOversizedDevCaches(): Promise<void> {
  if (app.isPackaged || process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP === "1") {
    return;
  }
  const userData = app.getPath("userData");
  const totalBytes = await cacheDirectoriesSize(userData, DEV_CACHE_DIRECTORIES);
  if (totalBytes < DEV_CACHE_CLEANUP_THRESHOLD_BYTES) {
    return;
  }
  try {
    await Promise.all([
      electronSession.defaultSession.clearCache(),
      electronSession.fromPartition("persist:wuu-browser").clearCache(),
    ]);
    await Promise.all(
      DEV_CACHE_DIRECTORIES.map((dir) =>
        rm(join(userData, dir), { recursive: true, force: true }),
      ),
    );
    console.info(
      `[desktop] cleared oversized dev cache (${Math.round(totalBytes / 1024 / 1024)} MB)`,
    );
  } catch (error) {
    console.warn(
      `[desktop] failed to clear oversized dev cache: ${error instanceof Error ? error.message : String(error)}`,
    );
  }
}

async function cacheDirectoriesSize(root: string, names: string[]): Promise<number> {
  let total = 0;
  for (const name of names) {
    total += await directorySize(join(root, name));
  }
  return total;
}

async function directorySize(path: string): Promise<number> {
  let info;
  try {
    info = await stat(path);
  } catch {
    return 0;
  }
  if (!info.isDirectory()) {
    return info.size;
  }
  let total = 0;
  let entries;
  try {
    entries = await readdir(path, { withFileTypes: true });
  } catch {
    return 0;
  }
  for (const entry of entries) {
    total += await directorySize(join(path, entry.name));
  }
  return total;
}

app.whenReady().then(async () => {
  setMainLocale(resolveMainLocale(getLanguagePreference(), app.getLocale()));
  installProductionAppShellGuards({
    isPackaged: app.isPackaged,
    setApplicationMenu: () => {
      Menu.setApplicationMenu(
        Menu.buildFromTemplate(productionApplicationMenuTemplate(process.platform)),
      );
    },
    onWebContentsCreated: (listener) => {
      app.on("web-contents-created", (_event, contents) => {
        listener({
          onBeforeInputEvent: (handler) => {
            contents.on("before-input-event", (event, value) => handler(event, value));
          },
          onDevToolsOpened: (handler) => {
            contents.on("devtools-opened", handler);
          },
          closeDevTools: () => contents.closeDevTools(),
        });
      });
    },
  });
  await clearOversizedDevCaches();
  await removeLegacyDesktopCliLink().catch(() => false);
  projectManager.load();
  registerRenderableFileProtocol(wuuHomePath());
  registerPluginModuleProtocol();
  // Sort permission/download traffic on the shared browser partition by
  // webContents ownership: only agent-driven views are denied sensitive
  // capabilities, the user's own <webview> on the same partition is untouched.
  if (ENABLE_EMBEDDED_BROWSER) {
    const browserSession = electronSession.fromPartition(BROWSER_PARTITION);
    try {
      if (await configureBrowserProxy(browserSession)) {
        console.info("[desktop] embedded browser proxy enabled via WUU_BROWSER_PROXY");
      }
    } catch (error) {
      // A stale or unavailable proxy must not prevent the desktop shell from
      // starting. The browser falls back to its normal connection behavior.
      console.warn(
        `[desktop] failed to configure embedded browser proxy: ${error instanceof Error ? error.message : String(error)}`,
      );
    }
    installBrowserSessionHandlers(browserSession, browserHostCoordinator);
  }
  // Pick up the user's chosen size before the pet window is created, so the
  // initial BrowserWindow and the inline data: URL are sized right the first
  // time. setSize is a no-op when the persisted size equals the default. A
  // persisted continuous scale (edge-drag resize) overrides the preset — it
  // must be applied after setSize, which clears any scale override.
  codexPetWindowManager.setSize(getCodexPetSize());
  const persistedPetScale = getCodexPetScale();
  if (persistedPetScale !== undefined) {
    codexPetWindowManager.setScale(persistedPetScale);
  }
  codexPetWindowManager.sync(codexPetsSnapshot());

  ipcMain.handle(
    "wuu:pop-out-session",
    (event, params: PopOutSessionParams) => {
      const context = params?.context ?? runtimeContextForEvent(event);
      const sourceWindow = BrowserWindow.fromWebContents(event.sender);
      if (params?.kind === "draft") {
        const win = createPopOutWindow({
          kind: "draft",
          context,
          sourceWindow,
        });
        return { windowID: win.webContents.id };
      }
      const threadID =
        typeof params?.threadID === "string" ? params.threadID.trim() : "";
      if (!threadID) {
        throw new Error("threadID is required");
      }
      const existingWindowID = windowRegistry.threadHostWindowID(threadID);
      const existing = windowRegistry.popOutWindowForThread(threadID);
      if (existing && !existing.isDestroyed() && !existing.webContents.isDestroyed()) {
        if (existing.isMinimized()) {
          existing.restore();
        }
        existing.show();
        existing.focus();
        return { windowID: existing.webContents.id };
      }
      if (existing) {
        windowRegistry.clearThreadWindow(threadID);
        if (existingWindowID !== undefined) {
          unregisterWindow(existingWindowID);
        }
      }
      const win = createPopOutWindow({
        kind: "thread",
        threadID,
        context,
        sourceWindow,
      });
      return { windowID: win.webContents.id };
    },
  );
  ipcMain.handle(
    "wuu:pop-out-closed",
    (_event, params: { threadID: string }) => {
      const win = windowRegistry.popOutWindowForThread(params.threadID);
      windowRegistry.clearThreadWindow(params.threadID);
      if (win && !win.isDestroyed()) win.close();
      return { ok: true };
    },
  );
  // Sync bootstrap for popped-out windows. This returns only the
  // main-process-owned window identity; conversation data loads through
  // normal async IPC after React starts. M1 parity: keep this channel
  // mirrored in preload.ts and always set event.returnValue.
  ipcMain.on(
    "wuu:pop-out-init",
    (event) => {
      const threadID = windowRegistry.threadForWindow(event.sender.id);
      const context = windowRegistry.runtimeContextForWindow(event.sender.id);
      event.returnValue = {
        kind: context ? (threadID ? "thread" : "draft") : null,
        threadID: threadID ?? null,
        context: context ?? null,
      } satisfies PopOutInitResult;
    },
  );
  ipcMain.handle("wuu:cua-active-thread", (event, threadID?: string) => {
    const senderWindow = BrowserWindow.fromWebContents(event.sender);
    if (!senderWindow || senderWindow.isDestroyed()) return;
    // Renderer focus and React mount are independent. Rejecting the initial
    // thread sync while focus is still settling permanently suppresses PiP for
    // that session because no later focus event is guaranteed.
    observationCoordinator.setActiveThread(threadID);
  });
  ipcMain.handle("wuu:running-threads-snapshot", () =>
    appServerClientPool.runningThreadsSnapshot(),
  );
  ipcMain.handle("wuu:project-list", () => {
    const result = projectManager.list();
    const contexts: RuntimeContext[] = [];
    if (result.active_context) {
      contexts.push(result.active_context);
      appServerClientPool.noteContextUsed(result.active_context);
    }
    for (const project of result.projects) {
      if (project.missing || project.id === result.active_project_id) {
        continue;
      }
      contexts.push({
        kind: "project",
        project_id: project.id,
        cwd: project.path,
      });
    }
    // Project runtimes take seconds to cold-start. Fill the small client pool
    // while the user is still reading the current project so switching among
    // the pooled projects does not begin that startup work after the click.
    // prewarmContexts fills in MRU order (most recently used first) so the
    // pool stays warm for the projects the user actually switches between.
    appServerClientPool.prewarmContexts(contexts);
    return result;
  });
  ipcMain.handle(
    "wuu:project-select",
    (_event, projectIDToSelect: string) => {
      const result = projectManager.select(projectIDToSelect);
      // Kick the target runtime's spawn off the moment the user clicks,
      // before the renderer's follow-up listThreads/thread-start requests
      // arrive, so the cold-start wait overlaps with the IPC round-trips
      // instead of being serialized after them.
      if (result.active_context) {
        appServerClientPool.noteContextUsed(result.active_context);
        appServerClientPool.prewarmContexts([result.active_context]);
      }
      return result;
    },
  );
  ipcMain.handle("wuu:project-remove", (_event, projectIDToRemove: string) =>
    projectManager.remove(projectIDToRemove),
  );
  ipcMain.handle(
    "wuu:project-cleanup-state",
    (_event, projectId: string, projectPath: string) => {
      // The removed workspace may still have a pooled app server; dispose
      // it first so nothing recreates runtime state during the cleanup.
      if (typeof projectPath === "string" && projectPath.trim() !== "") {
        appServerClientPool.disposeWorkdirClient(projectPath);
      }
      return appServerClientPool.request<{
        state_dir: string;
        removed: boolean;
        data_archived: boolean;
      }>("workspace/state/cleanup", {
        workspace_id: projectId,
      });
    },
  );
  ipcMain.handle(
    "wuu:project-select-none",
    (_event, fresh?: boolean, cwd?: string) => {
      const result = projectManager.selectNoProject(Boolean(fresh), cwd);
      if (result.active_context) {
        appServerClientPool.noteContextUsed(result.active_context);
        appServerClientPool.prewarmContexts([result.active_context]);
      }
      return result;
    },
  );
  ipcMain.handle("wuu:git-status", (event, root?: string) =>
    gitServiceForEvent(event).status({}, root),
  );
  ipcMain.handle("wuu:git-changes", (event, root?: string) =>
    gitServiceForEvent(event).changes(root),
  );
  ipcMain.handle("wuu:git-file-diff", (event, path: string, root?: string) =>
    gitServiceForEvent(event).fileDiff(path, root),
  );
  ipcMain.handle("wuu:git-action-busy", (event, root?: string) =>
    gitServiceForEvent(event).actionBusy(root),
  );
  ipcMain.handle("wuu:git-checkout-branch", (event, branch: string, root?: string) =>
    gitServiceForEvent(event).checkoutBranch(branch, root),
  );
  ipcMain.handle("wuu:git-create-checkout-branch", (event, branch: string, root?: string) =>
    gitServiceForEvent(event).createCheckoutBranch(branch, root),
  );
  ipcMain.handle("wuu:git-commit", (event, params: GitCommitParams, root?: string) =>
    gitServiceForEvent(event).commit(params ?? {}, root),
  );
  ipcMain.handle(
    "wuu:git-commit-message",
    (event, params: GitCommitParams, root?: string) =>
      gitServiceForEvent(event).commitMessage(params ?? {}, root),
  );
  ipcMain.handle("wuu:git-create-pr", (event, params: GitPullRequestParams, root?: string) =>
    gitServiceForEvent(event).createPullRequest(params ?? {}, root),
  );
  ipcMain.handle("wuu:file-tree-list", (event, root?: string) =>
    workspaceFilesForEvent(event).fileTreeList(root),
  );
  ipcMain.handle(
    "wuu:file-directory-list",
    (event, path?: string, root?: string) =>
      workspaceFilesForEvent(event).directoryList(path, root),
  );
  ipcMain.handle("wuu:file-read", (event, path: string, root?: string) =>
    workspaceFilesForEvent(event).readFile(path, root),
  );
  ipcMain.handle(
    "wuu:file-write",
    (event, params: WorkspaceFileSaveParams, root?: string) =>
      workspaceFilesForEvent(event).writeFile(params, root),
  );
  ipcMain.handle(
    "wuu:file-reference-resolve",
    (event, reference: string, root?: string) =>
      workspaceFilesForEvent(event).resolveFileReference(reference, root),
  );
  ipcMain.handle(
    "wuu:terminal-start",
    (event, params?: TerminalSessionStartParams) =>
      terminalSessionManager.startInContext(
        runtimeContextForEvent(event),
        params,
        event.sender.id,
      ),
  );
  ipcMain.handle("wuu:terminal-write", (event, id: string, data: string) =>
    terminalSessionManager.write(id, data, event.sender.id),
  );
  ipcMain.handle(
    "wuu:terminal-resize",
    (event, id: string, cols: number, rows: number) =>
      terminalSessionManager.resize(id, cols, rows, event.sender.id),
  );
  ipcMain.handle("wuu:terminal-stop", (event, id: string) =>
    terminalSessionManager.stop(id, event.sender.id),
  );
  ipcMain.handle("wuu:managed-process-list", (event, threadId: string) =>
    appServerRequest<ManagedProcessListResult>(event, "process/list", { thread_id: threadId }),
  );
  ipcMain.handle("wuu:managed-process-read", (event, params: ManagedProcessReadParams) =>
    appServerRequest<ManagedProcessReadResult>(event, "process/read", params),
  );
  ipcMain.handle(
    "wuu:managed-process-write",
    (event, threadId: string, processId: string, input: string) =>
      appServerRequest<ManagedProcessWriteResult>(event, "process/write", {
        thread_id: threadId,
        process_id: processId,
        input,
      }),
  );
  ipcMain.handle(
    "wuu:managed-process-resize",
    (event, threadId: string, processId: string, cols: number, rows: number) =>
      appServerRequest<ManagedProcessActionResult>(event, "process/resize", {
        thread_id: threadId,
        process_id: processId,
        cols,
        rows,
      }),
  );
  ipcMain.handle("wuu:managed-process-stop", (event, threadId: string, processId: string) =>
    appServerRequest<ManagedProcessActionResult>(event, "process/stop", {
      thread_id: threadId,
      process_id: processId,
    }),
  );
  ipcMain.handle("wuu:project-choose-folder", async () => {
    const projectPath = await showProjectDirectoryDialog({
      title: mainTranslate("chooseExistingFolder"),
      buttonLabel: mainTranslate("useFolder"),
      properties: ["openDirectory"],
    });
    if (!projectPath) {
      return projectManager.list();
    }
    return projectManager.add(projectPath);
  });
  ipcMain.handle("wuu:project-create-blank", async () => {
    const projectPath = await showProjectDirectoryDialog({
      title: mainTranslate("createBlankProject"),
      buttonLabel: mainTranslate("createProject"),
      properties: ["openDirectory", "createDirectory"],
    });
    if (!projectPath) {
      return projectManager.list();
    }
    return projectManager.add(projectPath);
  });
  ipcMain.handle(
    "wuu:project-relocate",
    async (_event, projectIDToRelocate: string) => {
      const projectPath = await showProjectDirectoryDialog({
        title: mainTranslate("relocateWorkspace"),
        buttonLabel: mainTranslate("relocateHere"),
        properties: ["openDirectory"],
      });
      if (!projectPath) {
        return projectManager.list();
      }
      return projectManager.relocate(projectIDToRelocate, projectPath);
    },
  );
  ipcMain.handle("wuu:initialize", async (event) => {
    const result = await appServerRequest<InitializeResult>(event, "initialize", {
      protocol_version: APP_SERVER_PROTOCOL_VERSION,
      client: { name: "wuu-desktop", version: DESKTOP_BUILD_INFO.version },
      capabilities: {
        reverse_rpc: { methods: [...BROWSER_REVERSE_RPC_METHODS] },
      },
    });
    if (result.core) {
      cachedCoreBuildInfo = result.core;
    }
    return result;
  });
  ipcMain.handle("wuu:user-question-list", (event, threadId?: string) =>
    appServerRequest<UserQuestionListResult>(event, "user-question/list", {
      thread_id: threadId,
    }));
  ipcMain.handle(
    "wuu:user-question-answer",
    (event, requestId: string, answer: UserQuestionAnswer) =>
      appServerRequest<UserQuestionResolveResult>(event, "user-question/respond", {
        request_id: requestId,
        answer,
      }),
  );
  ipcMain.handle("wuu:user-question-cancel", (event, requestId: string) =>
    appServerRequest<UserQuestionResolveResult>(event, "user-question/cancel", {
      request_id: requestId,
    }));
  ipcMain.handle("wuu:user-question-hold", (event, requestId: string) =>
    appServerRequest<UserQuestionResolveResult>(event, "user-question/hold", {
      request_id: requestId,
    }));
  ipcMain.handle(
    "wuu:system-notification",
    (event, params: SystemNotificationParams): SystemNotificationResult => {
      // macOS first: the notification stack differs per platform (app bundle,
      // permission model, toast routing). Windows/Linux land separately.
      if (process.platform !== "darwin") {
        return { shown: false };
      }
      const win = BrowserWindow.fromWebContents(event.sender);
      // The in-app surface already shows the event while the window has
      // focus; only reach out of the app when the user is elsewhere.
      if (win && !win.isDestroyed() && win.isFocused()) {
        return { shown: false };
      }
      const notification = new Notification({
        title: params.title,
        body: params.body,
      });
      activeSystemNotifications.add(notification);
      notification.on("close", () => {
        activeSystemNotifications.delete(notification);
      });
      notification.on("click", () => {
        if (win && !win.isDestroyed()) {
          if (win.isMinimized()) {
            win.restore();
          }
          win.show();
          win.focus();
        }
        activeSystemNotifications.delete(notification);
      });
      notification.show();
      return { shown: true };
    },
  );
  ipcMain.handle("wuu:build-info", (): BuildInfoResult => ({
    core: cachedCoreBuildInfo,
    desktop: DESKTOP_BUILD_INFO,
  }));
  ipcMain.handle("wuu:text-polish", (event, text: string) =>
    appServerRequest<TextPolishResult>(event, "text/polish", { text }),
  );
  ipcMain.handle("wuu:speech-start", (event, locale: string) => {
    if (!ENABLE_VOICE_INPUT) {
      return { ok: false as const, error: "platform_unsupported" as const };
    }
    return speechRecognitionService.start(locale, (payload) => {
      if (!event.sender.isDestroyed()) {
        event.sender.send("wuu:speech-event", payload);
      }
    });
  });
  ipcMain.handle("wuu:speech-stop", () => {
    speechRecognitionService.stop();
    return { ok: true as const };
  });
  ipcMain.handle("wuu:open-external", async (_event, url: string) => {
    await openExternalNavigation(url);
  });
  ipcMain.handle("wuu:config-codex-models", (event, provider?: string) =>
    appServerRequest<ConfigCodexModelsResult>(event, "config/codex/models", {
      provider: provider ?? "",
    }),
  );
  ipcMain.handle("wuu:config-model-catalog-refresh", (event) =>
    appServerRequest<ConfigModelCatalogRefreshResult>(
      event,
      "config/model-catalog/refresh",
    ),
  );
  ipcMain.handle(
    "wuu:config-model-update",
    (
      event,
      provider?: string,
      model?: string,
      effort?: string,
      connection?: {
        base_url?: string;
        api_key?: string;
        auth_token?: string;
        type?: string;
        create_provider?: boolean;
        remove_model?: string;
        reuse_codex_credentials?: boolean;
      },
      variant?: string,
      permissionMode?: string,
      threadID?: string,
    ) =>
      appServerRequest<ConfigModelUpdateResult>(event, "config/model/update", {
        // Omitted provider/model are inherited from the target thread, so
        // their empties are dropped instead of sent. Effort/variant/permission
        // forward whenever explicitly provided: an explicit empty variant is
        // the reset-to-model-default signal (the server clears the stored
        // selection), so it must not be truthy-dropped.
        ...(provider ? { provider } : {}),
        ...(model ? { model } : {}),
        ...(threadID ? { thread_id: threadID } : {}),
        ...(connection?.base_url === undefined
          ? {}
          : { base_url: connection.base_url }),
        ...(connection?.api_key === undefined
          ? {}
          : { api_key: connection.api_key }),
        ...(connection?.auth_token === undefined
          ? {}
          : { auth_token: connection.auth_token }),
        ...(connection?.type === undefined ? {} : { type: connection.type }),
        ...(connection?.create_provider ? { create_provider: true } : {}),
        ...(connection?.remove_model ? { remove_model: connection.remove_model } : {}),
        ...(connection?.reuse_codex_credentials === undefined
          ? {}
          : { reuse_codex_credentials: connection.reuse_codex_credentials }),
        ...(effort === undefined ? {} : { effort }),
        ...(variant === undefined ? {} : { variant }),
        ...(permissionMode === undefined
          ? {}
          : { permission_mode: permissionMode }),
      }),
  );
  ipcMain.handle(
    "wuu:config-advanced-update",
    (event, settings: RuntimeAdvancedSettingsUpdate) =>
      appServerRequest<ConfigAdvancedUpdateResult>(
        event,
        "config/advanced/update",
        settings ?? {},
      ),
  );
  ipcMain.handle(
    "wuu:config-general-update",
    (event, settings: RuntimeGeneralSettingsUpdate) =>
      appServerRequest<ConfigGeneralUpdateResult>(
        event,
        "config/general/update",
        settings ?? {},
      ),
  );
  ipcMain.handle("wuu:engines-list", (event) =>
    appServerRequest<EngineListResult>(event, "engine/list"),
  );
  ipcMain.handle("wuu:engines-update", (event, params: EngineUpdateParams) =>
    appServerRequest<EngineListResult>(event, "engine/update", params ?? {}),
  );
  ipcMain.handle(
    "wuu:extension-package-update",
    (event, params: ExtensionPackageUpdateParams) =>
      appServerRequest<ExtensionPackageUpdateResult>(
        event,
        "extension/package/update",
        params,
      ),
  );
  ipcMain.handle("wuu:extension-catalog-refresh", (event) =>
    appServerRequest<ExtensionCatalogRefreshResult>(event, "extension/catalog/refresh"),
  );
  ipcMain.handle("wuu:plugin-package-install", async (event) => {
    const context = runtimeContextForEvent(event);
    const packagePath = await showProjectDirectoryDialog({
      properties: ["openFile", "openDirectory"],
      filters: [{ name: "Wuu Plugin Package", extensions: ["zip"] }],
    });
    if (!packagePath) {
      return undefined;
    }
    return appServerClientPool.requestInContext<PluginPackageInstallResult>(
      context,
      "plugin/package/install",
      { path: packagePath },
    );
  });
  ipcMain.handle("wuu:plugin-package-remove", (event, id: string) =>
    appServerRequest<PluginPackageRemoveResult>(event, "plugin/package/remove", { id }),
  );
  ipcMain.handle(
    "wuu:plugin-desktop-module-load",
    async (event, params: PluginDesktopModuleReadParams): Promise<PluginDesktopModuleLoadResult> => {
      const module = await appServerRequest<PluginDesktopModuleReadResult>(
        event,
        "plugin/desktop-module/read",
        params,
      );
      return cachePluginDesktopModule(module);
    },
  );
  ipcMain.handle(
    "wuu:plugin-icon-load",
    async (event, params: PluginIconReadParams): Promise<PluginIconLoadResult> => {
      const icon = await appServerRequest<PluginIconReadResult>(
        event,
        "plugin/icon/read",
        params,
      );
      return cachePluginIcon(icon);
    },
  );
  ipcMain.handle("wuu:plugin-setting-get", (event, params: PluginSettingGetParams) =>
    appServerRequest<PluginSettingResult>(event, "plugin/setting/get", params));
  ipcMain.handle("wuu:plugin-setting-set", (event, params: PluginSettingSetParams) =>
    appServerRequest<PluginSettingResult>(event, "plugin/setting/set", params));
  ipcMain.handle("wuu:plugin-diagnostics-list", (event, params: PluginIdentityParams) =>
    appServerRequest<PluginDiagnosticsResult>(event, "plugin/diagnostics/list", params));
  ipcMain.handle("wuu:plugin-storage-get", (event, params: PluginStorageGetParams) =>
    appServerRequest<PluginStorageResult>(event, "plugin/storage/get", params));
  ipcMain.handle("wuu:plugin-storage-set", (event, params: PluginStorageSetParams) =>
    appServerRequest<PluginStorageResult>(event, "plugin/storage/set", params));
  ipcMain.handle("wuu:plugin-runtime-request", (event, params: PluginClientRequestParams) => {
    const workspaceID = params.workspace_id?.trim();
    if (!workspaceID) {
      return appServerRequest<PluginClientRequestResult>(event, "plugin/client/request", params);
    }
    const { workspace_id: _workspaceID, ...request } = params;
    return appServerClientPool.requestInContext<PluginClientRequestResult>(
      runtimeContextForWorkspaceID(workspaceID),
      "plugin/client/request",
      request,
    );
  });
  ipcMain.handle(
    "wuu:config-provider-remove",
    (
      event,
      provider: string,
      options?: { fallbackProvider?: string; fallbackModel?: string },
    ) =>
      appServerRequest<ConfigModelUpdateResult>(
        event,
        "config/provider/remove",
        {
          provider,
          ...(options?.fallbackProvider
            ? { fallback_provider: options.fallbackProvider }
            : {}),
          ...(options?.fallbackModel
            ? { fallback_model: options.fallbackModel }
            : {}),
        },
      ),
  );
  ipcMain.handle("wuu:skill-list", (event) =>
    appServerRequest(event, "skill/list"),
  );
  ipcMain.handle("wuu:skill-content", async (event, params: SkillContentParams): Promise<SkillContentResult> => {
    return readCatalogSkill(await appServerRequest<SkillListResult>(event, "skill/list"),params);
  });
  ipcMain.handle("wuu:channel-continuity", (event, params) => appServerRequest(event, "channel/continuity", params));
  ipcMain.handle("wuu:channel-session-list", (event, params: ChannelSessionListParams) =>
    appServerRequest<ChannelSessionListResult>(event, "channel/session/list", params),
  );
  ipcMain.handle("wuu:channel-session-create", (event, params: ChannelSessionCreateParams) =>
    appServerRequest<ChannelSessionResult>(event, "channel/session/create", params),
  );
  ipcMain.handle("wuu:channel-session-read", (event, params: ChannelSessionRefParams & { requestId?: string }) =>
    appServerClientPool.requestForSession<ChannelSessionReadResult>(
      runtimeContextForEvent(event), params.sessionRef, "channel/session/read", params,
      (response, workdir) => {
        // Put the snapshot on the same ordered channel as subsequent deltas.
        // The invoke promise can resolve after later stdout notifications.
        if (params.requestId && !response.error && !event.sender.isDestroyed()) {
          event.sender.send("wuu:server-event", {
            kind: "notification", workdir,
            message: { method: "channel/session/snapshot", params: { request_id: params.requestId, result: response.result } },
          } satisfies ServerEvent);
        }
      },
    ),
  );
  ipcMain.handle("wuu:channel-session-send", (event, params: ChannelSessionSendParams) =>
    appServerRequest<ChannelSessionResult>(event, "channel/session/send", params),
  );
  ipcMain.handle("wuu:channel-session-stop", (event, params: ChannelSessionRefParams) =>
    appServerRequest<ChannelSessionResult>(event, "channel/session/stop", params),
  );
  ipcMain.handle("wuu:channel-session-resume", (event, params: ChannelSessionRefParams) =>
    appServerRequest<ChannelSessionResult>(event, "channel/session/resume", params),
  );
  ipcMain.handle("wuu:channel-agent-list", (event) =>
    appServerRequest<ChannelAgentListResult>(event, "channel/agent/list"),
  );
  ipcMain.handle("wuu:channel-agent-insights", (event) =>
    appServerRequest<ChannelAgentInsightsResult>(event, "channel/agent/insights"),
  );
  ipcMain.handle("wuu:channel-bootstrap", (event) =>
    appServerRequest<ChannelBootstrapResult>(event, "channel/bootstrap"),
  );
  ipcMain.handle("wuu:channel-agent-create", (event, params: ChannelAgentCreateParams) =>
    appServerRequest<ChannelAgentCreateResult>(event, "channel/agent/create", params),
  );
  ipcMain.handle("wuu:channel-agent-update", (event, params: ChannelAgentUpdateParams) =>
    appServerRequest<ChannelAgentUpdateResult>(event, "channel/agent/update", params),
  );
  ipcMain.handle("wuu:channel-agent-delete", (event, params: ChannelAgentDeleteParams) =>
    appServerRequest<ChannelAgentDeleteResult>(event, "channel/agent/delete", params),
  );
  ipcMain.handle("wuu:channel-agent-start", (event, params: ChannelAgentStartParams) =>
    appServerRequest<ChannelAgentStartResult>(event, "channel/agent/start", params),
  );
  ipcMain.handle("wuu:channel-agent-reset", (event, params: ChannelAgentResetParams) =>
    appServerRequest<ChannelAgentResetResult>(event, "channel/agent/reset", params),
  );
  ipcMain.handle("wuu:channel-agent-creation-resolve", (event, params: ChannelAgentCreationResolveParams) =>
    appServerRequest<ChannelAgentCreationResolveResult>(event, "channel/agent-creation/resolve", params),
  );
  ipcMain.handle("wuu:channel-room-list", (event) =>
    appServerRequest<ChannelRoomListResult>(event, "channel/room/list"),
  );
  ipcMain.handle("wuu:channel-room-create", (event, params: ChannelRoomCreateParams) =>
    appServerRequest<ChannelRoomCreateResult>(event, "channel/room/create", params),
  );
  ipcMain.handle("wuu:channel-direct-message-open", (event, params: ChannelDirectMessageOpenParams) =>
    appServerRequest<ChannelDirectMessageOpenResult>(event, "channel/direct-message/open", params),
  );
  ipcMain.handle("wuu:channel-room-update", (event, params: ChannelRoomUpdateParams) =>
    appServerRequest<ChannelRoomUpdateResult>(event, "channel/room/update", params),
  );
  ipcMain.handle("wuu:channel-room-delete", (event, params: ChannelRoomDeleteParams) =>
    appServerRequest<ChannelRoomDeleteResult>(event, "channel/room/delete", params),
  );
  ipcMain.handle("wuu:channel-room-read", (event, params: ChannelRoomReadParams) =>
    appServerRequest<ChannelRoomReadResult>(event, "channel/room/read", params),
  );
  ipcMain.handle("wuu:channel-message-list", (event, params: ChannelMessageListParams) =>
    appServerRequest<ChannelMessageListResult>(event, "channel/message/list", params),
  );
  ipcMain.handle("wuu:channel-message-send", (event, params: ChannelMessageSendParams) =>
    appServerRequest<ChannelMessageSendResult>(event, "channel/message/send", params),
  );
  ipcMain.handle("wuu:channel-task-create", (event, params: ChannelTaskCreateParams) =>
    appServerRequest<ChannelTaskCreateResult>(event, "channel/task/create", params),
  );
  ipcMain.handle("wuu:channel-task-update", (event, params: ChannelTaskUpdateParams) =>
    appServerRequest<ChannelTaskUpdateResult>(event, "channel/task/update", params),
  );
  ipcMain.handle("wuu:channel-human-mention-status", (event) =>
    appServerRequest<ChannelHumanMentionStatusResult>(event, "channel/human-mention/status"),
  );
  ipcMain.handle("wuu:channel-human-mention-ack", (event) =>
    appServerRequest<ChannelHumanMentionAckResult>(event, "channel/human-mention/ack"),
  );
  ipcMain.handle("wuu:codex-pets-list", () => {
    const snapshot = codexPetsSnapshot();
    // Sync the pet window from the renderer-side list call so a failed
    // startup sync (e.g. data: URL ready-to-show that never fires) can
    // be recovered by re-opening the snapshot via SettingsView, not only
    // by toggling the enabled flag. `sync` is idempotent: if the window
    // already shows the same pet, applyView short-circuits.
    codexPetWindowManager.sync(snapshot);
    return snapshot;
  });
  ipcMain.handle(
    "wuu:codex-pets-update",
    (_event, settings: CodexPetSettingsUpdate) =>
      updateCodexPetSettings(settings ?? {}),
  );
  ipcMain.handle(
    "wuu:codex-pet-runtime",
    (_event, runtime: CodexPetRuntime) =>
      codexPetWindowManager.setRuntime(
        runtime ?? { running: false, status: "" },
      ),
  );
  ipcMain.handle(
    "wuu:codex-pet-hints",
    (_event, hints: CodexPetHint[] | null) =>
      codexPetWindowManager.setHints(hints ?? []),
  );
  ipcMain.handle("wuu:settings-usage", (event) =>
    appServerRequest<SettingsUsageResponse>(event, "settings/usage"),
  );
  ipcMain.handle("wuu:mcp-list", (event) =>
    appServerRequest<MCPListResult>(event, "mcp/list"),
  );
  ipcMain.handle("wuu:mcp-connect", (event, name: string) =>
    appServerRequest<MCPServerActionResult>(event, "mcp/connect", { name }),
  );
  ipcMain.handle("wuu:mcp-disconnect", (event, name: string) =>
    appServerRequest<MCPServerActionResult>(event, "mcp/disconnect", { name }),
  );
  ipcMain.handle("wuu:mcp-refresh", (event, name: string) =>
    appServerRequest<MCPServerActionResult>(event, "mcp/refresh", { name }),
  );
  ipcMain.handle("wuu:mcp-auth-start", (event, name: string) =>
    appServerRequest<MCPAuthStartResult>(event, "mcp/auth/start", { name }),
  );
  ipcMain.handle("wuu:mcp-auth-status", (event, name: string) =>
    appServerRequest<MCPAuthStatusResult>(event, "mcp/auth/status", { name }),
  );
  ipcMain.handle("wuu:mcp-auth-finish", (event, name: string, state: string, code: string) =>
    appServerRequest<MCPAuthFinishResult>(event, "mcp/auth/finish", { name, state, code }),
  );
  ipcMain.handle("wuu:auth-xai-login-start", (event) =>
    appServerRequest<AuthXAILoginStartResult>(event, "auth/xai/login/start"),
  );
  ipcMain.handle("wuu:auth-xai-login-poll", (event, loginId: string) =>
    appServerRequest<AuthXAILoginPollResult>(event, "auth/xai/login/poll", {
      login_id: loginId,
    }),
  );
  ipcMain.handle("wuu:auth-xai-login-cancel", (event, loginId: string) =>
    appServerRequest<{ ok: boolean }>(event, "auth/xai/login/cancel", {
      login_id: loginId,
    }),
  );
  ipcMain.handle("wuu:mcp-auth-remove", (event, name: string) =>
    appServerRequest<MCPAuthRemoveResult>(event, "mcp/auth/remove", { name }),
  );
  ipcMain.handle("wuu:activity-list", (event, threadId: string) =>
    appServerRequest<ActivityListResult>(event, "activity/list", { thread_id: threadId }),
  );
  ipcMain.handle("wuu:activity-takeover", (event, threadId: string, activityId: string) =>
    appServerRequest<ActivityActionResult>(event, "activity/takeover", { thread_id: threadId, activity_id: activityId }),
  );
  ipcMain.handle("wuu:activity-release", (event, threadId: string, activityId: string) =>
    appServerRequest<ActivityReleaseResult>(event, "activity/release", { thread_id: threadId, activity_id: activityId }),
  );
  ipcMain.handle("wuu:activity-stop", (event, threadId: string, activityId: string) =>
    appServerRequest<ActivityActionResult>(event, "activity/stop", { thread_id: threadId, activity_id: activityId }),
  );
  ipcMain.handle("wuu:side-thread-open", (event, mainThreadId: string) =>
    appServerRequest<SideThreadOpenResult>(event, "sideThread/open", { main_thread_id: mainThreadId }),
  );
  ipcMain.handle("wuu:side-thread-history", (event, mainThreadId: string) =>
    appServerRequest<SideThreadHistoryResult>(event, "sideThread/getHistory", { main_thread_id: mainThreadId }),
  );
  ipcMain.handle(
    "wuu:side-thread-send",
    (event, params: SideThreadSendParams) =>
      appServerRequest<SideThreadSendResult>(event, "sideThread/sendMessage", params),
  );
  ipcMain.handle("wuu:side-thread-reset", (event, mainThreadId: string) =>
    appServerRequest<{ ok: boolean }>(event, "sideThread/reset", { main_thread_id: mainThreadId }),
  );
  ipcMain.handle("wuu:side-thread-interrupt", (event, mainThreadId: string) =>
    appServerRequest<{ ok: boolean }>(event, "sideThread/interrupt", { main_thread_id: mainThreadId }),
  );
  ipcMain.handle(
    "wuu:thread-start",
    (event, params?: ThreadStartParams) =>
      appServerRequest<{ thread: Thread }>(event, "thread/start", params ?? {}),
  );
  ipcMain.handle("wuu:thread-resume", (event, sessionId?: string) =>
    rendererServerEventBatcher.resolveSnapshot(
      appServerRequest<ThreadResumeResult>(event, "thread/resume", {
        session_id: sessionId ?? "",
      }),
    ),
  );
  ipcMain.handle(
    "wuu:thread-fork",
    (
      event,
      threadId: string,
      turnId?: string,
      itemId?: string,
      mode?: "local" | "worktree",
      target?: ThreadForkTarget,
    ) =>
      appServerRequest<ThreadForkResult>(event, "thread/fork", {
        thread_id: threadId,
        turn_id: turnId ?? "",
        item_id: itemId ?? "",
        ...(target ? { target } : {}),
        ...(mode ? { mode } : {}),
      }),
  );
  ipcMain.handle(
    "wuu:thread-edit-message",
    (event, threadId: string, turnId: string, itemId: string) =>
      appServerRequest<ThreadEditMessageResult>(event, "thread/edit-message", {
        thread_id: threadId,
        turn_id: turnId,
        item_id: itemId,
      }),
  );
  ipcMain.handle("wuu:thread-context-composition", (event, threadId: string) =>
    appServerRequest<ThreadContextCompositionResult>(event, "thread/context-composition", {
      thread_id: threadId,
    }),
  );
  ipcMain.handle("wuu:instructions-list", (event) =>
    appServerRequest<InstructionsListResult>(event, "instructions/list"),
  );
  ipcMain.handle("wuu:account-window-open", event => {
    createAccountWindow(runtimeContextForEvent(event));
  });
  ipcMain.handle("wuu:account-window-close", event => {
    if (windowRegistry.roleForWindow(event.sender.id) !== "account") return;
    BrowserWindow.fromWebContents(event.sender)?.close();
    mainWindow?.show(); mainWindow?.focus();
  });
  ipcMain.handle("wuu:remote-account", (event, action: string, input: Record<string,string>) => {
    const workdir = runtimeContextForEvent(event).cwd;
    return phoneAccess.run(async () => {
      if (['login','register','logout','password'].includes(action)) await phoneAccess.stop();
      const result = await remoteHostManager.account(workdir, action, input);
      if (action === 'github-poll' && result.username) {
        await phoneAccess.stop();
        await phoneAccess.setEnabled(workdir, true);
        const window = BrowserWindow.fromWebContents(event.sender);
        window?.show(); window?.focus();
      }
      if (['login','register'].includes(action)) await phoneAccess.setEnabled(workdir, true);
      if (['logout','password'].includes(action)) await phoneAccess.setEnabled(workdir, false);
      if (['login', 'register', 'logout', 'password', 'revoke'].includes(action) || (action === 'github-poll' && result.username)) {
        for (const win of windowRegistry.allWindows()) {
          if (!win.isDestroyed()) win.webContents.send("wuu:account-changed");
        }
      }
      return result;
    });
  });
  ipcMain.handle("wuu:remote-snapshot", (event) =>
    remoteControlSnapshot(runtimeContextForEvent(event).cwd),
  );
  ipcMain.handle("wuu:remote-relay-set", async (event, relayUrl: string) => {
    const workdir = runtimeContextForEvent(event).cwd;
    await remoteHostManager.setRelay(workdir, String(relayUrl));
    return remoteControlSnapshot(workdir);
  });
  ipcMain.handle("wuu:remote-host-set", (event, enabled: boolean) => {
    const workdir = runtimeContextForEvent(event).cwd;
    return phoneAccess.run(async () => {
      await phoneAccess.setEnabled(workdir, enabled);
      return remoteControlSnapshot(workdir);
    });
  });
  ipcMain.handle("wuu:remote-pairing-start", (event) => {
    const workdir = runtimeContextForEvent(event).cwd;
    return phoneAccess.run(async () => {
      await phoneAccess.openPairing(workdir);
      return remoteControlSnapshot(workdir);
    });
  });
  ipcMain.handle("wuu:remote-device-remove", async (event, fingerprintOrPub: string) => {
    const workdir = runtimeContextForEvent(event).cwd;
    return phoneAccess.run(async () => {
      await remoteHostManager.removeDevice(workdir, String(fingerprintOrPub));
      return remoteControlSnapshot(workdir);
    });
  });
  ipcMain.handle("wuu:theme-preference-get", () => getThemePreference());
  ipcMain.on("wuu:onboarding-complete-get-sync", (event) => {
    event.returnValue = isOnboardingComplete();
  });
  ipcMain.handle("wuu:onboarding-complete", () => {
    completeOnboarding();
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.setResizable(true);
    }
    return { ok: true as const };
  });
  ipcMain.handle("wuu:language-preference-get", () => getLanguagePreference());
  ipcMain.handle("wuu:plugin-conflict-preferences-get", () => getPluginConflictPreferences());
  ipcMain.handle("wuu:plugin-conflict-preference-set", (_event, key: string, pluginId: string) =>
    setPluginConflictPreference(String(key), String(pluginId)));
  ipcMain.on("wuu:voice-input-settings-get-sync", (event) => {
    event.returnValue = getVoiceInputSettings();
  });
  ipcMain.on("wuu:channel-room-preferences-get-sync", (event) => {
    event.returnValue = getChannelRoomPreferences();
  });
  ipcMain.handle(
    "wuu:channel-room-preferences-set",
    (_event, preferences: ChannelRoomPreferences): ChannelRoomPreferences =>
      setChannelRoomPreferences(preferences),
  );
  ipcMain.handle(
    "wuu:voice-input-settings-get",
    async (): Promise<VoiceInputSettingsSnapshot> => ({
      settings: getVoiceInputSettings(),
      microphone_permission: microphonePermissionStatus(),
      speech_permission: ENABLE_VOICE_INPUT
        ? await speechRecognitionService.permissionStatus()
        : "unavailable",
    }),
  );
  ipcMain.handle(
    "wuu:voice-input-settings-set",
    (_event, settings: VoiceInputSettings): VoiceInputSettings => {
      const next: VoiceInputSettings = {
        polish_enabled: settings?.polish_enabled === true,
        language: isAppLocale(settings?.language) ? settings.language : "system",
      };
      setVoiceInputSettings(next);
      broadcastVoiceInputSettings();
      return next;
    },
  );
  ipcMain.handle(
    "wuu:voice-input-open-privacy-settings",
    async (_event, permission: "microphone" | "speech") => {
      if (!ENABLE_VOICE_INPUT || process.platform !== "darwin") {
        throw new Error("Voice privacy settings are available only on macOS");
      }
      const pane =
        permission === "speech"
          ? "Privacy_SpeechRecognition"
          : "Privacy_Microphone";
      await shell.openExternal(
        `x-apple.systempreferences:com.apple.preference.security?${pane}`,
      );
      return { ok: true as const };
    },
  );
  ipcMain.on("wuu:language-preference-get-sync", (event) => {
    event.returnValue = getLanguagePreference();
  });
  ipcMain.handle("wuu:language-preference-set", (_event, language: LanguagePreference) => {
    const next = isLanguagePreference(language) ? language : "system";
    setLanguagePreference(next);
    setMainLocale(resolveMainLocale(next, app.getLocale()));
    codexPetWindowManager.refreshLocale();
    broadcastLanguagePreference();
    return { ok: true, language: next };
  });
  // Synchronous variant used by the preload script so the first paint
  // already carries the persisted theme (no light-mode flash on boot).
  ipcMain.on("wuu:theme-preference-get-sync", (event) => {
    event.returnValue = getThemePreference();
  });
  ipcMain.handle("wuu:theme-preference-set", (_event, theme: ThemePreference) => {
    const valid: ThemePreference[] = ["system", "light", "dark"];
    const next = valid.includes(theme) ? theme : "system";
    setThemePreference(next);
    syncThemeAcrossWindows();
    return { ok: true, theme: next };
  });
  // "system" preference: OS dark-mode flips arrive here, not through the
  // preference IPC. They go through the same multi-window sync so every
  // window's content, background, and native chrome move together.
  nativeTheme.on("updated", syncThemeAcrossWindows);
  ipcMain.on("wuu:message-flow-font-size-get-sync", (event) => {
    // Sync partner of getMessageFlowFontSize — preload needs the value
    // before first paint to stamp --conversation-message-font-size on
    // <html> and avoid a flash of the default size.
    event.returnValue = getMessageFlowFontSize();
  });
  ipcMain.handle("wuu:message-flow-font-size-get", () =>
    getMessageFlowFontSize(),
  );
  ipcMain.handle(
    "wuu:message-flow-font-size-set",
    (_event, fontSize: unknown) => {
      // Mirror the renderer's clamp: keep the IPC boundary honest about
      // the same 13-20 window so a corrupted renderer can't widen it.
      const valid =
        typeof fontSize === "number" &&
        Number.isFinite(fontSize) &&
        fontSize >= MESSAGE_FLOW_FONT_SIZE_RANGE.min &&
        fontSize <= MESSAGE_FLOW_FONT_SIZE_RANGE.max;
      const next: MessageFlowFontSize = valid
        ? (fontSize as MessageFlowFontSize)
        : MESSAGE_FLOW_FONT_SIZE_RANGE.default;
      setMessageFlowFontSize(next);
      return { ok: true, fontSize: next };
    },
  );
  ipcMain.handle("wuu:thread-list", (event, cwd?: string) =>
    appServerRequest<{ threads: Thread[] }>(
      event,
      "thread/list",
      typeof cwd === "string" && cwd.length > 0 ? { cwd } : undefined,
    ),
  );
  ipcMain.handle("wuu:thread-list-archived", (event) =>
    appServerRequest<{ threads: Thread[] }>(event, "thread/listArchived"),
  );
  ipcMain.handle("wuu:thread-list-all", (event) =>
    appServerRequest<{ threads: Thread[] }>(event, "thread/listAll"),
  );
  ipcMain.handle("wuu:thread-search", (event, query: string, limit?: number) =>
    appServerRequest(event, "thread/search", {
      query: query ?? "",
      limit: typeof limit === "number" ? limit : undefined,
    }),
  );
  ipcMain.handle(
    "wuu:thread-preview",
    (event, threadId: string, limit?: number) =>
      appServerRequest(event, "thread/preview", {
        thread_id: threadId,
        limit: typeof limit === "number" ? limit : undefined,
      }),
  );
  ipcMain.handle(
    "wuu:thread-pin",
    (event, threadId: string, pinned: boolean) =>
      appServerRequest<{ thread: Thread }>(event, "thread/pin", {
        thread_id: threadId,
        pinned,
      }),
  );
  ipcMain.handle("wuu:session-organization-list", (event) =>
    appServerRequest(event, "sessionOrganization/list"),
  );
  ipcMain.handle("wuu:session-folder-create", (event, name: string) =>
    appServerRequest(event, "sessionFolder/create", { name }),
  );
  ipcMain.handle("wuu:session-folder-update", (event, id: string, name: string) =>
    appServerRequest(event, "sessionFolder/update", { id, name }),
  );
  ipcMain.handle("wuu:session-folder-reorder", (event, ids: string[]) =>
    appServerRequest(event, "sessionFolder/reorder", { ids }),
  );
  ipcMain.handle("wuu:session-folder-delete", (event, id: string) =>
    appServerRequest(event, "sessionFolder/delete", { id }),
  );
  ipcMain.handle(
    "wuu:thread-organization-update",
    (event, threadId: string, folderId?: string) =>
      appServerRequest<{ thread: Thread }>(event, "thread/organization/update", {
        thread_id: threadId,
        ...(folderId !== undefined ? { folder_id: folderId } : {}),
      }),
  );
  ipcMain.handle(
    "wuu:thread-archive",
    (event, threadId: string, archived: boolean, force?: boolean) =>
      appServerRequest<{ thread: Thread }>(event, "thread/archive", {
        thread_id: threadId,
        archived,
        force: force === true ? true : undefined,
      }),
  );
  ipcMain.handle("wuu:thread-delete", (event, threadId: string) =>
    appServerRequest<{ thread_id: string }>(event, "thread/delete", {
      thread_id: threadId,
    }),
  );
  ipcMain.handle("wuu:thread-compact-start", (event, threadId: string) =>
    appServerRequest<{ turn: Turn }>(event, "thread/compact/start", {
      thread_id: threadId,
    }),
  );
  ipcMain.handle(
    "wuu:thread-rename",
    (event, threadId: string, title: string) =>
      appServerRequest<{ thread: Thread }>(event, "thread/rename", {
        thread_id: threadId,
        title,
      }),
  );
  ipcMain.handle("wuu:reveal-session", (_event, _threadId: string) => {
    // The session's data lives in the user-level sessions dir (a single
    // shared SQLite file). Reveal that dir in the OS file browser so
    // the user can inspect the database.
    return shell.openPath(join(wuuHomePath(), "sessions"));
  });
  ipcMain.handle(
    "wuu:file-show-in-folder",
    (_event, path: string) => {
      // The path comes from `listWorkspaceDirectory`, so it's already a
      // workspace item the renderer is allowed to know about. We hand
      // it straight to the OS file browser — Finder highlights the row,
      // Explorer opens its parent, file managers open the folder — so
      // the user can do anything with the item beyond the in-app tree.
      // `showItemInFolder` returns void; we run it for its side effect.
      shell.showItemInFolder(path);
    },
  );
  ipcMain.handle("wuu:file-show-menu", async (event, path: string) => {
    if (process.platform !== "darwin") {
      return { action: "none" } as const;
    }
    const owner = BrowserWindow.fromWebContents(event.sender);
    if (!owner || owner.isDestroyed()) {
      return { action: "none" } as const;
    }

    const associations = await macWorkspaceApplications(path).catch(() => ({
      defaultApplication: undefined,
      applications: [],
    }));
    const iconApplications = associations.defaultApplication
      ? [associations.defaultApplication, ...associations.applications]
      : associations.applications;
    const iconEntries = iconApplications.map((application) => {
      if (!application.iconPng) return [application.path, undefined] as const;
      const icon = nativeImage.createFromBuffer(
        Buffer.from(application.iconPng, "base64"),
        { scaleFactor: 2 },
      );
      return [application.path, icon.isEmpty() ? undefined : icon] as const;
    });
    const icons = new Map(iconEntries);
    return new Promise<{ action: "none" }>((resolve) => {
      const template = macWorkspaceItemMenuTemplate({
        associations,
        icons,
        labels: {
          open: mainTranslate("open"),
          openInApplication: (application) => mainTranslate("openInApplication", { application }),
          openWith: mainTranslate("openWith"),
          copyPath: mainTranslate("copyPath"),
        },
        onOpenDefault: () => { void shell.openPath(path); },
        onOpenWith: (application) => {
          void openMacWorkspaceItemWithApplication(path, application.path).catch((error) => {
            console.error("[desktop] failed to open workspace item with application", error);
          });
        },
        onCopyPath: () => clipboard.writeText(path),
      });
      Menu.buildFromTemplate(template).popup({
        window: owner,
        callback: () => resolve({ action: "none" }),
      });
    });
  });
  ipcMain.handle(
    "wuu:turn-start",
    (
      event,
      threadId: string,
      prompt: string,
      images?: InputImage[],
      files?: InputFile[],
      permissionMode?: string,
      activeDocument?: ActiveDocumentContext,
      contentParts?: import("../shared/protocol").MessageContentPart[],
    ) =>
      appServerRequest<{ turn: Turn }>(event, "turn/start", {
        thread_id: threadId,
        prompt,
        images: images ?? [],
        files: files ?? [],
        ...(permissionMode === undefined ? {} : { permission_mode: permissionMode }),
        ...(activeDocument === undefined ? {} : { active_document: activeDocument }),
        ...(contentParts === undefined ? {} : { content_parts: contentParts }),
      }),
  );
  ipcMain.handle(
    "wuu:turn-queue",
    (
      event,
      threadId: string,
      prompt: string,
      images?: InputImage[],
      clientId?: string,
      files?: InputFile[],
      permissionMode?: string,
      activeDocument?: ActiveDocumentContext,
      contentParts?: import("../shared/protocol").MessageContentPart[],
    ) =>
      appServerRequest(event, "turn/queue", {
        thread_id: threadId,
        prompt,
        images: images ?? [],
        files: files ?? [],
        client_id: clientId,
        ...(permissionMode === undefined ? {} : { permission_mode: permissionMode }),
        ...(activeDocument === undefined ? {} : { active_document: activeDocument }),
        ...(contentParts === undefined ? {} : { content_parts: contentParts }),
      }),
  );
  ipcMain.handle(
    "wuu:turn-update-queued",
    (
      event,
      threadId: string,
      queueId: string,
      prompt: string,
      images?: InputImage[],
      files?: InputFile[],
      contentParts?: import("../shared/protocol").MessageContentPart[],
    ) =>
      appServerRequest(event, "turn/update-queued", {
        thread_id: threadId,
        queue_id: queueId,
        prompt,
        images: images ?? [],
        files: files ?? [],
        ...(contentParts === undefined ? {} : { content_parts: contentParts }),
      }),
  );
  ipcMain.handle(
    "wuu:turn-dequeue",
    (event, threadId: string, queueId: string) =>
      appServerRequest<{ ok: boolean }>(event, "turn/dequeue", {
        thread_id: threadId,
        queue_id: queueId,
      }),
  );
  ipcMain.handle(
    "wuu:turn-steer",
    (
      event,
      threadId: string,
      expectedTurnId: string,
      prompt: string,
      images?: InputImage[],
      clientId?: string,
      files?: InputFile[],
      activeDocument?: ActiveDocumentContext,
      contentParts?: import("../shared/protocol").MessageContentPart[],
    ) =>
      appServerRequest(event, "turn/steer", {
        thread_id: threadId,
        expected_turn_id: expectedTurnId,
        prompt,
        images: images ?? [],
        files: files ?? [],
        client_id: clientId,
        ...(activeDocument === undefined ? {} : { active_document: activeDocument }),
        ...(contentParts === undefined ? {} : { content_parts: contentParts }),
      }),
  );
  ipcMain.handle("wuu:turn-unsteer", (event, threadId: string, steerId: string) =>
    appServerRequest<{ ok: boolean }>(event, "turn/unsteer", {
      thread_id: threadId,
      steer_id: steerId,
    }),
  );
  ipcMain.handle("wuu:turn-requeue", (event, threadId: string, steerId: string) =>
    appServerRequest(event, "turn/requeue", {
      thread_id: threadId,
      steer_id: steerId,
    }),
  );
  ipcMain.handle("wuu:turn-interrupt", (event, threadId: string) =>
    appServerRequest<{ ok: boolean }>(event, "turn/interrupt", {
      thread_id: threadId,
    }),
  );
  ipcMain.handle(
    "wuu:respond-server-request",
    (_event, id: string, result: unknown) => {
      appServerClientPool.respondToServerRequest(id, result);
    },
  );
  ipcMain.handle(
    "wuu:reject-server-request",
    (_event, id: string, message: string) => {
      appServerClientPool.rejectServerRequest(id, message);
    },
  );
  // Embedded browser: the renderer reports the on-screen bounds of the host div
  // while an agent view is taken over (rAF-polled — pure motion isn't caught by
  // ResizeObserver), so main can position the reparented WebContentsView. The
  // target window is derived from event.sender, not trusted from the payload.
  ipcMain.handle(
    "wuu:browser-report-bounds",
    (
      event,
      payload: {
        workdir: string;
        tabID: string;
        rect: { x: number; y: number; width: number; height: number };
      },
    ) => {
      if (!ENABLE_EMBEDDED_BROWSER) return { ok: false };
      const senderWindow = BrowserWindow.fromWebContents(event.sender);
      if (!senderWindow || senderWindow.isDestroyed()) return { ok: false };
      browserHostCoordinator.reportBounds(
        payload.workdir,
        payload.tabID,
        senderWindow as unknown as BrowserParentWindowHandle,
        payload.rect,
      );
      return { ok: true };
    },
  );
  // Renderer full-window overlay/modal appeared over the agent view — hide the
  // WebContentsView so it does not paint over the dialog, restore when clear.
  ipcMain.handle(
    "wuu:browser-overlay-suppress",
    (_event, payload: { workdir: string; tabID: string; suppressed: boolean }) => {
      if (!ENABLE_EMBEDDED_BROWSER) return { ok: false };
      browserHostCoordinator.setOverlaySuppressed(
        payload.workdir,
        payload.tabID,
        payload.suppressed === true,
      );
      return { ok: true };
    },
  );
  syncNativeThemeSource();
  createWindow();
  void phoneAccess.run(() => phoneAccess.restore(projectManager.ensureRuntimeContext().cwd));

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on("before-quit", () => {
  speechRecognitionService.stop();
  terminalSessionManager.cleanup();
  // Destroy every agent view + the hidden host window before the pool shuts
  // down so no WebContentsView leaks past quit.
  browserHostCoordinator.destroyAll();
  appServerClientPool.shutdown();
  // SIGTERM goes out synchronously; the daemon's own signal handling shuts
  // the relay connection down cleanly.
  void phoneAccess.shutdown();
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
