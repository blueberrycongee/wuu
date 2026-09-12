import { contextBridge, ipcRenderer } from "electron";
import {
  MESSAGE_FLOW_FONT_SIZE_RANGE,
  isLanguagePreference,
  type DesktopPlatform,
  type ChannelAgentCreateParams,
  type ChannelAgentUpdateParams,
  type ChannelAgentDeleteParams,
  type ChannelAgentStartParams,
  type ChannelAgentResetParams,
  type ChannelAgentCreationResolveParams,
  type ChannelMessageListParams,
  type ChannelMessageSendParams,
  type ChannelRoomCreateParams,
  type ChannelDirectMessageOpenParams,
  type ChannelRoomUpdateParams,
  type ChannelRoomDeleteParams,
  type ChannelRoomReadParams,
  type ChannelRoomPreferences,
  type ChannelTaskCreateParams,
  type ChannelTaskUpdateParams,
  type MessageFlowFontSize,
  type PopOutInitResult,
  type RemoteControlEvent,
  type RunningThreadSnapshot,
  type ServerEvent,
  type SideThreadEventEnvelope,
  type SideThreadSendParams,
  type SpeechRecognitionEvent,
  type ThreadStartParams,
  type ThreadForkTarget,
  type ThemePreference,
  type LanguagePreference,
  type WindowResizeState,
  type WuuDesktopApi,
  type VoiceInputSettings,
} from "../shared/protocol";

// Read the persisted theme preference synchronously so the very first
// paint carries the right data-theme — an async round-trip would flash
// the light theme for dark-mode users on every launch.
const initialThemePreference = ((): ThemePreference => {
  try {
    const value = ipcRenderer.sendSync("wuu:theme-preference-get-sync") as unknown;
    return value === "light" || value === "dark" || value === "system"
      ? value
      : "system";
  } catch {
    return "system";
  }
})();

const initialLanguagePreference = ((): LanguagePreference => {
  try {
    const value = ipcRenderer.sendSync("wuu:language-preference-get-sync") as unknown;
    return isLanguagePreference(value) ? value : "system";
  } catch {
    return "system";
  }
})();

const initialOnboardingComplete = (() => {
  try {
    return ipcRenderer.sendSync("wuu:onboarding-complete-get-sync") === true;
  } catch {
    // A missing handler means an older main process. Do not trap the renderer
    // behind a flow it cannot persist.
    return true;
  }
})();

const initialVoiceInputSettings = ((): VoiceInputSettings => {
  try {
    const value = ipcRenderer.sendSync(
      "wuu:voice-input-settings-get-sync",
    ) as Partial<VoiceInputSettings>;
    return {
      polish_enabled: value?.polish_enabled === true,
      language:
        value?.language === "zh-CN" || value?.language === "en-US"
          ? value.language
          : "system",
    };
  } catch {
    return { polish_enabled: false, language: "system" };
  }
})();

const initialChannelRoomPreferences = ((): ChannelRoomPreferences | undefined => {
  try {
    return ipcRenderer.sendSync("wuu:channel-room-preferences-get-sync") as
      | ChannelRoomPreferences
      | undefined;
  } catch {
    return undefined;
  }
})();

// Same FOUC-free story for the message-stream reading size: ask the
// main process synchronously and stamp --conversation-message-font-size
// on <html> before React mounts, so even the first render of the
// conversation pane carries the user's chosen size. The renderer
// defensively clamps non-finite or out-of-range values to the range
// defined in MESSAGE_FLOW_FONT_SIZE_RANGE so a corrupted settings file
// (or even an unset window.wuu) never crashes the first paint.
const initialMessageFlowFontSize: MessageFlowFontSize = ((): MessageFlowFontSize => {
  try {
    const value = ipcRenderer.sendSync(
      "wuu:message-flow-font-size-get-sync",
    ) as unknown;
    if (
      typeof value === "number" &&
      Number.isFinite(value) &&
      value >= MESSAGE_FLOW_FONT_SIZE_RANGE.min &&
      value <= MESSAGE_FLOW_FONT_SIZE_RANGE.max
    ) {
      return value;
    }
    return MESSAGE_FLOW_FONT_SIZE_RANGE.default;
  } catch {
    return MESSAGE_FLOW_FONT_SIZE_RANGE.default;
  }
})();

function resolveTheme(preference: ThemePreference): "light" | "dark" {
  if (preference === "system") {
    return window.matchMedia?.("(prefers-color-scheme: dark)").matches
      ? "dark"
      : "light";
  }
  return preference;
}

try {
  document.documentElement.dataset.theme = initialOnboardingComplete
    ? resolveTheme(initialThemePreference)
    : "light";
} catch {
  // The renderer's theme controller re-applies on boot; losing the
  // preload stamp only costs a one-frame flash.
}

// Which OS chrome the window carries: macOS traffic lights top-left,
// Windows controls-overlay top-right. Window-chrome CSS keys off this
// stamp, so it must land before first paint like the theme stamp above.
const desktopPlatform: DesktopPlatform =
  process.platform === "darwin" || process.platform === "win32"
    ? process.platform
    : "linux";

try {
  document.documentElement.dataset.platform = desktopPlatform;
} catch {
  // main.tsx re-stamps on boot; losing the preload stamp only costs a
  // one-frame flash of the wrong corner reservation.
}

try {
  // Set --conversation-message-font-size as an inline custom property on
  // <html>. Inline wins over the `:root` declaration in conversation-shell.css
  // so first paint never blinks at the default size. main.tsx re-applies
  // for the live subscription (no-op for size — we never need to react to
  // external changes).
  document.documentElement.style.setProperty(
    "--conversation-message-font-size",
    `${initialMessageFlowFontSize}px`,
  );
} catch {
  // Same fall-back story as the theme stamp; dropping the inline value
  // only costs a one-frame flash at the default size.
}

const api: WuuDesktopApi = {
  setSessionInspectorExpansion: (params) => ipcRenderer.invoke("wuu:session-inspector-expansion", params),
  initialOnboardingComplete,
  completeOnboarding: () => ipcRenderer.invoke("wuu:onboarding-complete"),
  platform: desktopPlatform,
  listProjects: () => ipcRenderer.invoke("wuu:project-list"),
  createBlankProject: () => ipcRenderer.invoke("wuu:project-create-blank"),
  chooseProjectFolder: () => ipcRenderer.invoke("wuu:project-choose-folder"),
  selectProject: (projectId: string) =>
    ipcRenderer.invoke("wuu:project-select", projectId),
  removeProject: (projectId: string) =>
    ipcRenderer.invoke("wuu:project-remove", projectId),
  cleanupProjectState: (projectId: string, projectPath: string) =>
    ipcRenderer.invoke("wuu:project-cleanup-state", projectId, projectPath),
  relocateProject: (projectId: string) =>
    ipcRenderer.invoke("wuu:project-relocate", projectId),
  selectNoProject: (fresh?: boolean, cwd?: string) =>
    ipcRenderer.invoke("wuu:project-select-none", fresh, cwd),
  gitStatus: (root?: string) => ipcRenderer.invoke("wuu:git-status", root),
  listGitChanges: (root?: string) => ipcRenderer.invoke("wuu:git-changes", root),
  readGitFileDiff: (path: string, root?: string) =>
    ipcRenderer.invoke("wuu:git-file-diff", path, root),
  gitActionBusy: (root?: string) => ipcRenderer.invoke("wuu:git-action-busy", root),
  checkoutGitBranch: (branch: string, root?: string) =>
    ipcRenderer.invoke("wuu:git-checkout-branch", branch, root),
  createCheckoutGitBranch: (branch: string, root?: string) =>
    ipcRenderer.invoke("wuu:git-create-checkout-branch", branch, root),
  commitGitChanges: (params, root?: string) =>
    ipcRenderer.invoke("wuu:git-commit", params, root),
  generateCommitMessage: (params, root?: string) =>
    ipcRenderer.invoke("wuu:git-commit-message", params, root),
  createPullRequest: (params, root?: string) =>
    ipcRenderer.invoke("wuu:git-create-pr", params, root),
  listWorkspaceFiles: (root?: string) =>
    ipcRenderer.invoke("wuu:file-tree-list", root),
  listWorkspaceDirectory: (path?: string, root?: string) =>
    ipcRenderer.invoke("wuu:file-directory-list", path, root),
  readWorkspaceFile: (path: string, root?: string) =>
    ipcRenderer.invoke("wuu:file-read", path, root),
  writeWorkspaceFile: (params, root?: string) =>
    ipcRenderer.invoke("wuu:file-write", params, root),
  resolveWorkspaceFileReference: (reference: string, root?: string) =>
    ipcRenderer.invoke("wuu:file-reference-resolve", reference, root),
  startTerminalSession: (params) =>
    ipcRenderer.invoke("wuu:terminal-start", params),
  writeTerminalSession: (id: string, data: string) =>
    ipcRenderer.invoke("wuu:terminal-write", id, data),
  resizeTerminalSession: (id: string, cols: number, rows: number) =>
    ipcRenderer.invoke("wuu:terminal-resize", id, cols, rows),
  stopTerminalSession: (id: string) =>
    ipcRenderer.invoke("wuu:terminal-stop", id),
  listManagedProcesses: (threadId: string) =>
    ipcRenderer.invoke("wuu:managed-process-list", threadId),
  readManagedProcess: (params) =>
    ipcRenderer.invoke("wuu:managed-process-read", params),
  writeManagedProcess: (threadId: string, processId: string, input: string) =>
    ipcRenderer.invoke("wuu:managed-process-write", threadId, processId, input),
  resizeManagedProcess: (threadId: string, processId: string, cols: number, rows: number) =>
    ipcRenderer.invoke("wuu:managed-process-resize", threadId, processId, cols, rows),
  stopManagedProcess: (threadId: string, processId: string) =>
    ipcRenderer.invoke("wuu:managed-process-stop", threadId, processId),
  initialize: () => ipcRenderer.invoke("wuu:initialize"),
  listUserQuestions: (threadId?: string) =>
    ipcRenderer.invoke("wuu:user-question-list", threadId),
  answerUserQuestion: (requestId, answer) =>
    ipcRenderer.invoke("wuu:user-question-answer", requestId, answer),
  cancelUserQuestion: (requestId) =>
    ipcRenderer.invoke("wuu:user-question-cancel", requestId),
  holdUserQuestion: (requestId) =>
    ipcRenderer.invoke("wuu:user-question-hold", requestId),
  showSystemNotification: (params) =>
    ipcRenderer.invoke("wuu:system-notification", params),
  getBuildInfo: () => ipcRenderer.invoke("wuu:build-info"),
  polishText: (text: string) => ipcRenderer.invoke("wuu:text-polish", text),
  startSpeechRecognition: (locale: string) =>
    ipcRenderer.invoke("wuu:speech-start", locale),
  stopSpeechRecognition: () => ipcRenderer.invoke("wuu:speech-stop"),
  onSpeechRecognitionEvent: (
    handler: (event: SpeechRecognitionEvent) => void,
  ) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: SpeechRecognitionEvent,
    ) => handler(payload);
    ipcRenderer.on("wuu:speech-event", listener);
    return () => ipcRenderer.removeListener("wuu:speech-event", listener);
  },
  loadCodexModels: (provider?: string) =>
    ipcRenderer.invoke("wuu:config-codex-models", provider),
  refreshModelCatalog: () =>
    ipcRenderer.invoke("wuu:config-model-catalog-refresh"),
  updateRuntimeSettings: (
    provider?: string,
    model?: string,
    effort?: string,
    connection?: Parameters<WuuDesktopApi["updateRuntimeSettings"]>[3],
    variant?: string,
    permissionMode?: string,
    threadId?: string,
  ) =>
    ipcRenderer.invoke(
      "wuu:config-model-update",
      provider,
      model,
      effort,
      connection,
      variant,
      permissionMode,
      threadId,
    ),
  removeProvider: (
    provider: string,
    options?: { fallbackProvider?: string; fallbackModel?: string },
  ) => ipcRenderer.invoke("wuu:config-provider-remove", provider, options),
  updateAdvancedSettings: (settings) =>
    ipcRenderer.invoke("wuu:config-advanced-update", settings),
  updateGeneralSettings: (settings) =>
    ipcRenderer.invoke("wuu:config-general-update", settings),
  listEngines: () => ipcRenderer.invoke("wuu:engines-list"),
  updateEngines: (params) => ipcRenderer.invoke("wuu:engines-update", params),
  updateExtensionPackage: (params) =>
    ipcRenderer.invoke("wuu:extension-package-update", params),
  refreshExtensionCatalog: () =>
    ipcRenderer.invoke("wuu:extension-catalog-refresh"),
  installPluginPackage: () => ipcRenderer.invoke("wuu:plugin-package-install"),
  removePluginPackage: (id: string) =>
    ipcRenderer.invoke("wuu:plugin-package-remove", id),
  loadPluginDesktopModule: (params) =>
    ipcRenderer.invoke("wuu:plugin-desktop-module-load", params),
  loadPluginIcon: (params) => ipcRenderer.invoke("wuu:plugin-icon-load", params),
  getPluginSetting: (params) => ipcRenderer.invoke("wuu:plugin-setting-get", params),
  setPluginSetting: (params) => ipcRenderer.invoke("wuu:plugin-setting-set", params),
  getPluginDiagnostics: (params) => ipcRenderer.invoke("wuu:plugin-diagnostics-list", params),
  getPluginStorage: (params) => ipcRenderer.invoke("wuu:plugin-storage-get", params),
  setPluginStorage: (params) => ipcRenderer.invoke("wuu:plugin-storage-set", params),
  requestPluginRuntime: (params) => ipcRenderer.invoke("wuu:plugin-runtime-request", params),
  listSkills: () => ipcRenderer.invoke("wuu:skill-list"),
  readSkillContent: (params) => ipcRenderer.invoke("wuu:skill-content", params),
  listChannelSessions: (params) => ipcRenderer.invoke("wuu:channel-session-list", params),
  createChannelSession: (params) => ipcRenderer.invoke("wuu:channel-session-create", params),
  readChannelSession: (params) => ipcRenderer.invoke("wuu:channel-session-read", params),
  sendChannelSession: (params) => ipcRenderer.invoke("wuu:channel-session-send", params),
  stopChannelSession: (params) => ipcRenderer.invoke("wuu:channel-session-stop", params),
  resumeChannelSession: (params) => ipcRenderer.invoke("wuu:channel-session-resume", params),
  listNamedAgents: () => ipcRenderer.invoke("wuu:channel-agent-list"),
  getNamedAgentInsights: () => ipcRenderer.invoke("wuu:channel-agent-insights"),
  bootstrapChannels: () => ipcRenderer.invoke("wuu:channel-bootstrap"),
  createNamedAgent: (params: ChannelAgentCreateParams) =>
    ipcRenderer.invoke("wuu:channel-agent-create", params),
  updateNamedAgent: (params: ChannelAgentUpdateParams) =>
    ipcRenderer.invoke("wuu:channel-agent-update", params),
  deleteNamedAgent: (params: ChannelAgentDeleteParams) =>
    ipcRenderer.invoke("wuu:channel-agent-delete", params),
  startNamedAgent: (params: ChannelAgentStartParams) =>
    ipcRenderer.invoke("wuu:channel-agent-start", params),
  resetNamedAgent: (params: ChannelAgentResetParams) =>
    ipcRenderer.invoke("wuu:channel-agent-reset", params),
  resolveChannelAgentCreation: (params: ChannelAgentCreationResolveParams) =>
    ipcRenderer.invoke("wuu:channel-agent-creation-resolve", params),
  listChannelRooms: () => ipcRenderer.invoke("wuu:channel-room-list"),
  createChannelRoom: (params: ChannelRoomCreateParams) =>
    ipcRenderer.invoke("wuu:channel-room-create", params),
  openChannelDirectMessage: (params: ChannelDirectMessageOpenParams) =>
    ipcRenderer.invoke("wuu:channel-direct-message-open", params),
  updateChannelRoom: (params: ChannelRoomUpdateParams) =>
    ipcRenderer.invoke("wuu:channel-room-update", params),
  deleteChannelRoom: (params: ChannelRoomDeleteParams) =>
    ipcRenderer.invoke("wuu:channel-room-delete", params),
  markChannelRoomRead: (params: ChannelRoomReadParams) =>
    ipcRenderer.invoke("wuu:channel-room-read", params),
  listChannelMessages: (params: ChannelMessageListParams) =>
    ipcRenderer.invoke("wuu:channel-message-list", params),
  sendChannelMessage: (params: ChannelMessageSendParams) =>
    ipcRenderer.invoke("wuu:channel-message-send", params),
  createChannelTask: (params: ChannelTaskCreateParams) =>
    ipcRenderer.invoke("wuu:channel-task-create", params),
  updateChannelTask: (params: ChannelTaskUpdateParams) =>
    ipcRenderer.invoke("wuu:channel-task-update", params),
  getChannelHumanMentionStatus: () =>
    ipcRenderer.invoke("wuu:channel-human-mention-status"),
  ackChannelHumanMentions: () =>
    ipcRenderer.invoke("wuu:channel-human-mention-ack"),
  getSettingsUsage: () => ipcRenderer.invoke("wuu:settings-usage"),
  listMCPServers: () => ipcRenderer.invoke("wuu:mcp-list"),
  connectMCPServer: (name: string) => ipcRenderer.invoke("wuu:mcp-connect", name),
  disconnectMCPServer: (name: string) =>
    ipcRenderer.invoke("wuu:mcp-disconnect", name),
  refreshMCPServer: (name: string) => ipcRenderer.invoke("wuu:mcp-refresh", name),
  startMCPAuth: (name: string) => ipcRenderer.invoke("wuu:mcp-auth-start", name),
  getMCPAuthStatus: (name: string) => ipcRenderer.invoke("wuu:mcp-auth-status", name),
  finishMCPAuth: (name: string, state: string, code: string) =>
    ipcRenderer.invoke("wuu:mcp-auth-finish", name, state, code),
  removeMCPAuth: (name: string) => ipcRenderer.invoke("wuu:mcp-auth-remove", name),
  startXAILogin: () => ipcRenderer.invoke("wuu:auth-xai-login-start"),
  pollXAILogin: (loginId: string) => ipcRenderer.invoke("wuu:auth-xai-login-poll", loginId),
  cancelXAILogin: (loginId: string) => ipcRenderer.invoke("wuu:auth-xai-login-cancel", loginId),
  listActivities: (threadId: string) => ipcRenderer.invoke("wuu:activity-list", threadId),
  takeoverActivity: (threadId: string, activityId: string) =>
    ipcRenderer.invoke("wuu:activity-takeover", threadId, activityId),
  releaseActivity: (threadId: string, activityId: string) =>
    ipcRenderer.invoke("wuu:activity-release", threadId, activityId),
  stopActivity: (threadId: string, activityId: string) =>
    ipcRenderer.invoke("wuu:activity-stop", threadId, activityId),
  listCodexPets: () => ipcRenderer.invoke("wuu:codex-pets-list"),
  updateCodexPetSettings: (settings) =>
    ipcRenderer.invoke("wuu:codex-pets-update", settings),
  updateCodexPetRuntime: (runtime) =>
    ipcRenderer.invoke("wuu:codex-pet-runtime", runtime),
  updateCodexPetHints: (hints) =>
    ipcRenderer.invoke("wuu:codex-pet-hints", hints),
  startThread: (params?: ThreadStartParams) =>
    ipcRenderer.invoke("wuu:thread-start", params),
  resumeThread: (sessionId?: string) =>
    ipcRenderer.invoke("wuu:thread-resume", sessionId),
  forkThread: (
    threadId: string,
    turnId?: string,
    itemId?: string,
    mode?: "local" | "worktree",
    target?: ThreadForkTarget,
  ) => ipcRenderer.invoke("wuu:thread-fork", threadId, turnId, itemId, mode, target),
  editThreadMessage: (threadId: string, turnId: string, itemId: string) =>
    ipcRenderer.invoke("wuu:thread-edit-message", threadId, turnId, itemId),
  getThreadContextComposition: (threadId: string) =>
    ipcRenderer.invoke("wuu:thread-context-composition", threadId),
  listInstructionFiles: () => ipcRenderer.invoke("wuu:instructions-list"),
  isAccountWindow: process.argv.includes("--wuu-account-window"),
  openAccountWindow: () => ipcRenderer.invoke("wuu:account-window-open"),
  closeAccountWindow: () => ipcRenderer.invoke("wuu:account-window-close"),
  remoteAccount: (action, input) => ipcRenderer.invoke("wuu:remote-account", action, input),
  getRemoteControlSnapshot: () => ipcRenderer.invoke("wuu:remote-snapshot"),
  setRemoteRelay: (relayUrl: string) =>
    ipcRenderer.invoke("wuu:remote-relay-set", relayUrl),
  setRemoteHostEnabled: (enabled: boolean) =>
    ipcRenderer.invoke("wuu:remote-host-set", enabled),
  startRemotePairing: () => ipcRenderer.invoke("wuu:remote-pairing-start"),
  removeRemoteDevice: (fingerprintOrPub: string) =>
    ipcRenderer.invoke("wuu:remote-device-remove", fingerprintOrPub),
  onRemoteControlEvent: (handler: (event: RemoteControlEvent) => void) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: RemoteControlEvent,
    ) => handler(payload);
    ipcRenderer.on("wuu:remote-event", listener);
    return () => ipcRenderer.removeListener("wuu:remote-event", listener);
  },
  initialThemePreference,
  initialLanguagePreference,
  initialVoiceInputSettings,
  initialChannelRoomPreferences,
  initialSystemLocale: Intl.DateTimeFormat().resolvedOptions().locale,
  getLanguagePreference: () => ipcRenderer.invoke("wuu:language-preference-get"),
  getPluginConflictPreferences: () => ipcRenderer.invoke("wuu:plugin-conflict-preferences-get"),
  setPluginConflictPreference: (key: string, pluginId: string) =>
    ipcRenderer.invoke("wuu:plugin-conflict-preference-set", key, pluginId),
  setLanguagePreference: (language: LanguagePreference) =>
    ipcRenderer.invoke("wuu:language-preference-set", language),
  onLanguagePreferenceChange: (handler: (language: LanguagePreference) => void) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: unknown,
    ) => {
      if (isLanguagePreference(payload)) {
        handler(payload);
      }
    };
    ipcRenderer.on("wuu:language-preference-changed", listener);
    return () =>
      ipcRenderer.removeListener("wuu:language-preference-changed", listener);
  },
  getVoiceInputSettings: () =>
    ipcRenderer.invoke("wuu:voice-input-settings-get"),
  updateVoiceInputSettings: (settings: VoiceInputSettings) =>
    ipcRenderer.invoke("wuu:voice-input-settings-set", settings),
  updateChannelRoomPreferences: (preferences: ChannelRoomPreferences) =>
    ipcRenderer.invoke("wuu:channel-room-preferences-set", preferences),
  onVoiceInputSettingsChange: (
    handler: (settings: VoiceInputSettings) => void,
  ) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: VoiceInputSettings,
    ) => handler(payload);
    ipcRenderer.on("wuu:voice-input-settings-changed", listener);
    return () =>
      ipcRenderer.removeListener("wuu:voice-input-settings-changed", listener);
  },
  openVoicePrivacySettings: (permission: "microphone" | "speech") =>
    ipcRenderer.invoke("wuu:voice-input-open-privacy-settings", permission),
  initialMessageFlowFontSize,
  getThemePreference: () => ipcRenderer.invoke("wuu:theme-preference-get"),
  setThemePreference: (theme: ThemePreference) =>
    ipcRenderer.invoke("wuu:theme-preference-set", theme),
  onThemePreferenceChange: (handler: (theme: ThemePreference) => void) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: unknown,
    ) => {
      if (payload === "light" || payload === "dark" || payload === "system") {
        handler(payload);
      }
    };
    ipcRenderer.on("wuu:theme-preference-changed", listener);
    return () =>
      ipcRenderer.removeListener("wuu:theme-preference-changed", listener);
  },
  getMessageFlowFontSize: () =>
    ipcRenderer.invoke("wuu:message-flow-font-size-get"),
  setMessageFlowFontSize: (fontSize: MessageFlowFontSize) =>
    ipcRenderer.invoke("wuu:message-flow-font-size-set", fontSize),
  listThreads: (cwd?: string) => ipcRenderer.invoke("wuu:thread-list", cwd),
  listAllThreads: () => ipcRenderer.invoke("wuu:thread-list-all"),
  listArchivedThreads: () =>
    ipcRenderer.invoke("wuu:thread-list-archived"),
  searchThreads: (query: string, limit?: number) =>
    ipcRenderer.invoke("wuu:thread-search", query, limit),
  getThreadPreview: (
    threadId: string,
    limit?: number,
  ): Promise<import("../shared/protocol").ThreadPreviewResult> =>
    ipcRenderer.invoke("wuu:thread-preview", threadId, limit),
  pinThread: (threadId: string, pinned: boolean) =>
    ipcRenderer.invoke("wuu:thread-pin", threadId, pinned),
  getSessionOrganization: () => ipcRenderer.invoke("wuu:session-organization-list"),
  createSessionFolder: (name: string) => ipcRenderer.invoke("wuu:session-folder-create", name),
  renameSessionFolder: (id: string, name: string) => ipcRenderer.invoke("wuu:session-folder-update", id, name),
  reorderSessionFolders: (ids: string[]) => ipcRenderer.invoke("wuu:session-folder-reorder", ids),
  deleteSessionFolder: (id: string) => ipcRenderer.invoke("wuu:session-folder-delete", id),
  updateThreadOrganization: (threadId: string, folderId?: string) =>
    ipcRenderer.invoke("wuu:thread-organization-update", threadId, folderId),
  archiveThread: (threadId: string, archived: boolean, force?: boolean) =>
    ipcRenderer.invoke("wuu:thread-archive", threadId, archived, force),
  deleteThread: (threadId: string) =>
    ipcRenderer.invoke("wuu:thread-delete", threadId),
  compactThread: (threadId: string) =>
    ipcRenderer.invoke("wuu:thread-compact-start", threadId),
  renameThread: (threadId: string, title: string) =>
    ipcRenderer.invoke("wuu:thread-rename", threadId, title),
  revealSession: (threadId: string) =>
    ipcRenderer.invoke("wuu:reveal-session", threadId),
  revealWorkspaceItem: (path: string) =>
    ipcRenderer.invoke("wuu:file-show-in-folder", path),
  showWorkspaceItemMenu: (path: string) =>
    ipcRenderer.invoke("wuu:file-show-menu", path),
  openExternal: (url: string) =>
    ipcRenderer.invoke("wuu:open-external", url),
  startTurn: (threadId: string, prompt: string, images, files, permissionMode, activeDocument, contentParts) =>
    ipcRenderer.invoke("wuu:turn-start", threadId, prompt, images, files, permissionMode, activeDocument, contentParts),
  queueTurn: (threadId: string, prompt: string, images, clientId, files, permissionMode, activeDocument, contentParts) =>
    ipcRenderer.invoke("wuu:turn-queue", threadId, prompt, images, clientId, files, permissionMode, activeDocument, contentParts),
  updateQueuedTurn: (threadId: string, queueId: string, prompt: string, images, files, contentParts) =>
    ipcRenderer.invoke("wuu:turn-update-queued", threadId, queueId, prompt, images, files, contentParts),
  dequeueTurn: (threadId: string, queueId: string) =>
    ipcRenderer.invoke("wuu:turn-dequeue", threadId, queueId),
  steerTurn: (threadId: string, expectedTurnId: string, prompt: string, images, clientId, files, activeDocument, contentParts) =>
    ipcRenderer.invoke(
      "wuu:turn-steer",
      threadId,
      expectedTurnId,
      prompt,
      images,
      clientId,
      files,
      activeDocument,
      contentParts,
    ),
  unsteerTurn: (threadId: string, steerId: string) =>
    ipcRenderer.invoke("wuu:turn-unsteer", threadId, steerId),
  requeueTurn: (threadId: string, steerId: string) =>
    ipcRenderer.invoke("wuu:turn-requeue", threadId, steerId),
  interruptTurn: (threadId: string) =>
    ipcRenderer.invoke("wuu:turn-interrupt", threadId),
  respondToServerRequest: (id: string, result: unknown) =>
    ipcRenderer.invoke("wuu:respond-server-request", id, result),
  rejectServerRequest: (id: string, message: string) =>
    ipcRenderer.invoke("wuu:reject-server-request", id, message),
  onServerEvent: (handler: (event: ServerEvent) => void) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: ServerEvent,
    ) => handler(payload);
    ipcRenderer.on("wuu:server-event", listener);
    return () => ipcRenderer.removeListener("wuu:server-event", listener);
  },
  onRunningThreadsChanged: (
    handler: (snapshot: RunningThreadSnapshot[]) => void,
  ) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: RunningThreadSnapshot[],
    ) => handler(payload);
    ipcRenderer.on("wuu:running-threads-changed", listener);
    return () =>
      ipcRenderer.removeListener("wuu:running-threads-changed", listener);
  },
  getRunningThreadsSnapshot: () =>
    ipcRenderer.invoke("wuu:running-threads-snapshot"),
  // 桌宠气泡点击跳转：主进程把当前 thread_id 广播到所有窗口，渲染进程
  // 据此把主窗口前置并切换 thread。
  onCodexPetJumpRequest: (
    handler: (event: { thread_id: string }) => void,
  ) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: { thread_id: string },
    ) => handler(payload);
    ipcRenderer.on("wuu:codex-pet-jump", listener);
    return () =>
      ipcRenderer.removeListener("wuu:codex-pet-jump", listener);
  },
  onTerminalEvent: (handler) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: Parameters<typeof handler>[0],
    ) => handler(payload);
    ipcRenderer.on("wuu:terminal-event", listener);
    return () => ipcRenderer.removeListener("wuu:terminal-event", listener);
  },
  onWindowResizeState: (handler: (state: WindowResizeState) => void) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: WindowResizeState,
    ) => handler(payload);
    ipcRenderer.on("wuu:window-resize-state", listener);
    return () =>
      ipcRenderer.removeListener("wuu:window-resize-state", listener);
  },
  popOutSession: (params) =>
    ipcRenderer.invoke("wuu:pop-out-session", params),
  popOutClosed: (params) =>
    ipcRenderer.invoke("wuu:pop-out-closed", params),
  // Requests are keyed by the owning main thread. The main process derives
  // wuu:side-thread-event from app-server sideThread/event notifications.
  openSideThread: (mainThreadId: string) =>
    ipcRenderer.invoke("wuu:side-thread-open", mainThreadId),
  getSideThreadHistory: (mainThreadId: string) =>
    ipcRenderer.invoke("wuu:side-thread-history", mainThreadId),
  sendSideThreadMessage: (params: SideThreadSendParams) =>
    ipcRenderer.invoke("wuu:side-thread-send", params),
  interruptSideThread: (mainThreadId: string) =>
    ipcRenderer.invoke("wuu:side-thread-interrupt", mainThreadId),
  resetSideThread: (mainThreadId: string) =>
    ipcRenderer.invoke("wuu:side-thread-reset", mainThreadId),
  onSideThreadEvent: (handler: (envelope: SideThreadEventEnvelope) => void) => {
    const listener = (
      _event: Electron.IpcRendererEvent,
      payload: Parameters<typeof handler>[0],
    ) => handler(payload);
    ipcRenderer.on("wuu:side-thread-event", listener);
    return () =>
      ipcRenderer.removeListener("wuu:side-thread-event", listener);
  },
  // Sync bootstrap for popped-out windows. This returns window-owned
  // identity only; thread data loads through the normal async IPC path.
  // SendSync must be paired with an ipcMain.on handler that sets
  // `event.returnValue` (the parity test enforces both sides).
  popOutInit: () =>
    ipcRenderer.sendSync("wuu:pop-out-init") as PopOutInitResult,
};

(api as WuuDesktopApi & { setActiveCUAThread: (threadID?: string) => void }).setActiveCUAThread =
  (threadID?: string) => { void ipcRenderer.invoke("wuu:cua-active-thread", threadID); };

// Embedded browser takeover surface. These are added via cast (not in the
// frozen WuuDesktopApi) so the renderer can position/hide an agent's
// WebContentsView and drop ghost activity UI on core teardown. The invoke
// channels are paired with ipcMain.handle in index.ts (IpcChannelParity).
type BrowserBoundsRect = { x: number; y: number; width: number; height: number };
type BrowserTakeoverApi = {
  reportBrowserBounds: (workdir: string, tabID: string, rect: BrowserBoundsRect) => void;
  suppressBrowserOverlay: (workdir: string, tabID: string, suppressed: boolean) => void;
  onBrowserInvalidate: (handler: (payload: { workdir: string }) => void) => () => void;
};
const browserApi = api as WuuDesktopApi & BrowserTakeoverApi;
browserApi.reportBrowserBounds = (workdir, tabID, rect) => {
  void ipcRenderer.invoke("wuu:browser-report-bounds", { workdir, tabID, rect });
};
browserApi.suppressBrowserOverlay = (workdir, tabID, suppressed) => {
  void ipcRenderer.invoke("wuu:browser-overlay-suppress", { workdir, tabID, suppressed });
};
browserApi.onBrowserInvalidate = (handler) => {
  const listener = (_event: Electron.IpcRendererEvent, payload: { workdir: string }) =>
    handler(payload);
  ipcRenderer.on("wuu:browser-invalidate", listener);
  return () => ipcRenderer.removeListener("wuu:browser-invalidate", listener);
};

contextBridge.exposeInMainWorld("wuu", api);

// Account changes are shared by the setup window and every workbench.
ipcRenderer.on("wuu:account-changed", () => {
  window.dispatchEvent(new Event("wuu:account-changed"));
});
