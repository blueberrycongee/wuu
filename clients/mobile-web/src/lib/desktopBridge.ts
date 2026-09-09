import {
  RemoteClient,
  pair,
  type Credentials,
  type HostState,
  type ServerRequest,
  type ServerRequestResult,
} from "@wuu/remote-core";
import type {
  ChannelRoomPreferences,
  DesktopPlatform,
  DesktopProject,
  ExtensionInventoryRecord,
  InitializeResult,
  LanguagePreference,
  MessageFlowFontSize,
  PluginConflictPreferences,
  ProjectListResult,
  RunningThreadSnapshot,
  RuntimeContext,
  Thread,
  ThreadResumeResult,
  ServerEvent,
  ThemePreference,
  VoiceInputSettings,
  WuuDesktopApi,
} from "@wuu/protocol";

export type WebConnectionSnapshot = {
  phase: "connecting" | "connected" | "reconnecting" | "restoring" | "error" | "disconnected";
  revision: number;
  error?: string;
};

type WorkspaceSnapshot = {
  current: string;
  current_id?: string;
  workspaces?: Array<{ id?: string; name: string; path: string }>;
};
type ThreadLocation = Pick<Thread, "id" | "cwd" | "workspace_id" | "worktree">;

type ServerEventListener = (event: ServerEvent) => void;
type RunningListener = (snapshot: RunningThreadSnapshot[]) => void;
type PreferenceListener<T> = (value: T) => void;

const THEME_KEY = "wuu.web.theme";
const LANGUAGE_KEY = "wuu.web.language";
const MESSAGE_SIZE_KEY = "wuu.web.message-size";
const CHANNEL_ROOM_PREFERENCES_KEY = "wuu.channels.roomPreferences";
const PLUGIN_CONFLICT_PREFERENCES_KEY = "wuu.web.plugin-conflict-preferences";
const DEFAULT_MESSAGE_SIZE = 16;
/** How long a brief link drop may last before the reconnect strip appears.
 *  Short enough that a real outage still surfaces within a second, long
 *  enough that returning from background and wifi blips stay silent. */
const RECONNECT_GRACE_MS = 600;

function basename(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, "");
  return trimmed.split(/[\\/]/).pop() || path || "Workspace";
}

function browserPlatform(): DesktopPlatform {
  const ua = navigator.userAgent;
  if (ua.includes("Windows")) return "win32";
  if (ua.includes("Macintosh") || ua.includes("Mac OS")) return "darwin";
  return "linux";
}

function storedTheme(): ThemePreference {
  const value = localStorage.getItem(THEME_KEY);
  return value === "light" || value === "dark" || value === "system" ? value : "system";
}

function storedLanguage(): LanguagePreference {
  const value = localStorage.getItem(LANGUAGE_KEY);
  return value === "zh-CN" || value === "en-US" || value === "system" ? value : "system";
}

function storedMessageSize(): MessageFlowFontSize {
  const value = Number(localStorage.getItem(MESSAGE_SIZE_KEY));
  return (Number.isFinite(value) ? value : DEFAULT_MESSAGE_SIZE) as MessageFlowFontSize;
}

function storedChannelRoomPreferences(): ChannelRoomPreferences {
  try {
    const parsed = JSON.parse(localStorage.getItem(CHANNEL_ROOM_PREFERENCES_KEY) ?? "null") as
      | Partial<ChannelRoomPreferences>
      | null;
    return {
      pinnedRoomIDs: Array.isArray(parsed?.pinnedRoomIDs) ? parsed.pinnedRoomIDs : [],
      archivedRoomIDs: Array.isArray(parsed?.archivedRoomIDs) ? parsed.archivedRoomIDs : [],
    };
  } catch {
    return { pinnedRoomIDs: [], archivedRoomIDs: [] };
  }
}

function storedPluginConflictPreferences(): PluginConflictPreferences {
  try {
    const parsed = JSON.parse(localStorage.getItem(PLUGIN_CONFLICT_PREFERENCES_KEY) ?? "null");
    return parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? parsed as PluginConflictPreferences
      : {};
  } catch {
    return {};
  }
}


export class UnavailableHostOperationError extends Error {
  readonly code = "host_operation_unavailable";
  constructor(readonly operation: keyof WuuDesktopApi) {
    super(`${operation} is unavailable in this browser host`);
    this.name = "UnavailableHostOperationError";
  }
}

// Keep host plugins manageable in the browser, without advertising desktop
// modules or package-asset icons that this host cannot load. Apply this to
// snapshots, updates and inventory notifications.
function webHostPayload<T>(payload: T): T {
  if (payload && typeof payload === "object" && "extension_inventory" in payload) {
    const source = payload as { extension_inventory?: unknown };
    return {
      ...(payload as object),
      extension_inventory: Array.isArray(source.extension_inventory)
        ? source.extension_inventory.map(sanitizeWebExtensionRecord)
        : [],
    } as T;
  }
  return payload;
}

function sanitizeWebExtensionRecord(record: unknown): unknown {
  if (!record || typeof record !== "object" || Array.isArray(record)) return record;
  const { desktop: _desktop, icon, ...manageable } = record as ExtensionInventoryRecord;
  const webIcon = sanitizeWebExtensionIcon(icon);
  return webIcon === undefined ? manageable : { ...manageable, icon: webIcon };
}

function sanitizeWebExtensionIcon(
  icon: unknown,
): ExtensionInventoryRecord["icon"] {
  // `name` icons map to host-owned public icons and render safely in a
  // browser; `path`/`light`/`dark` icons are package assets served by the
  // desktop host, so drop them instead of attempting an asset fetch.
  if (!icon || typeof icon !== "object" || Array.isArray(icon)) return undefined;
  const descriptor = icon as Record<string, unknown>;
  if (typeof descriptor.name === "string" && descriptor.name) {
    return { name: descriptor.name };
  }
  return undefined;
}

const unavailableWebMethods = [
  "createBlankProject",
  "chooseProjectFolder",
  "removeProject",
  "cleanupProjectState",
  "relocateProject",
  "listWorkspaceFiles",
  "writeWorkspaceFile",
  "startTerminalSession",
  "writeTerminalSession",
  "resizeTerminalSession",
  "stopTerminalSession",
  "getBuildInfo",
  "startSpeechRecognition",
  "stopSpeechRecognition",
  "installPluginPackage",
  "loadPluginDesktopModule",
  "loadPluginIcon",
  "readSkillContent",
  "getRemoteControlSnapshot",
  "setRemoteRelay",
  "setRemoteHostEnabled",
  "startRemotePairing",
  "removeRemoteDevice",
  "updateVoiceInputSettings",
  "openVoicePrivacySettings",
  "listCodexPets",
  "updateCodexPetSettings",
  "updateCodexPetRuntime",
  "updateCodexPetHints",
  "revealSession",
  "revealWorkspaceItem",
  "showWorkspaceItemMenu",
  "popOutSession",
  "popOutClosed",
] as const satisfies readonly (keyof WuuDesktopApi)[];

// An explicit, type-checked list keeps new protocol members from silently
// becoming pretend functions. Every unsupported action rejects consistently.
const unavailableWebActions = Object.fromEntries(
  unavailableWebMethods.map((method) => [method, async () => {
    throw new UnavailableHostOperationError(method);
  }]),
) as { [K in typeof unavailableWebMethods[number]]: (...args: unknown[]) => Promise<never> };

/** Browser host adapter for the shared desktop renderer. */
export class RemoteDesktopBridge {
  private readonly client: RemoteClient;
  private readonly serverListeners = new Set<ServerEventListener>();
  private readonly runningListeners = new Set<RunningListener>();
  private readonly themeListeners = new Set<PreferenceListener<ThemePreference>>();
  private readonly languageListeners = new Set<PreferenceListener<LanguagePreference>>();
  private readonly voiceListeners = new Set<PreferenceListener<VoiceInputSettings>>();
  private readonly pendingServerRequests = new Map<
    string,
    { resolve: (result: ServerRequestResult) => void }
  >();
  private hostState: HostState | null = null;
  private attachedOnce = false;
  private defaultWorkdir = "";
  private projects: DesktopProject[] = [];
  private readonly remoteWorkspaceIDs = new Set<string>();
  private activeContext: RuntimeContext | undefined;
  private readonly threadLocations = new Map<string, ThreadLocation>();
  private readonly questionWorkdirs = new Map<string, string>();
  private connection: WebConnectionSnapshot = { phase: "connecting", revision: 0 };
  private readonly connectionListeners = new Set<() => void>();
  private readonly restoreListeners = new Set<() => Promise<void>>();
  private attachSync: Promise<void> = Promise.resolve();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private needsRestore = false;
  private stopped = false;

  getConnectionSnapshot = (): WebConnectionSnapshot => this.connection;
  subscribeConnection = (listener: () => void): (() => void) => {
    this.connectionListeners.add(listener);
    return () => this.connectionListeners.delete(listener);
  };

  private setConnection(phase: WebConnectionSnapshot["phase"], error?: string): number {
    this.connection = { phase, revision: this.connection.revision + 1, ...(error ? { error } : {}) };
    for (const listener of this.connectionListeners) listener();
    return this.connection.revision;
  }

  private clearPendingRequests(): void {
    for (const pending of this.pendingServerRequests.values()) {
      pending.resolve({ error: { code: "connection_replaced", message: "The remote connection was replaced" } });
    }
    this.pendingServerRequests.clear();
  }

  private async synchronize(first: boolean, resumed = false): Promise<void> {
    // A resumed attach already replayed missed events on the same app-server
    // connection. Keep the workbench in place unless a previous fresh
    // connection still needs initialize + thread snapshots.
    if (!first && resumed && !this.needsRestore) {
      this.setConnection("connected");
      return;
    }
    const revision = this.setConnection(first ? "connecting" : "restoring");
    try {
      const workspace = await this.client.call<WorkspaceSnapshot>("workspace/list", { remote_delivery: 1 }, 30_000);
      if (this.connection.revision !== revision || this.stopped) return;
      if (!workspace.current) throw new Error("Remote host did not provide a workspace");
      this.updateWorkspaces(workspace);
      if (!first) await Promise.all([...this.restoreListeners].map((restore) => restore()));
      if (this.connection.revision !== revision || this.stopped) return;
      this.needsRestore = false;
      this.setConnection("connected");
    } catch (error) {
      if (this.connection.revision !== revision || this.stopped) return;
      this.setConnection("error", error instanceof Error ? error.message : String(error));
      throw error;
    }
  }

  retryRestore = async (): Promise<void> => {
    if (!this.client.isAttached() || this.connection.phase === "restoring") return;
    this.needsRestore = true;
    await this.synchronize(false, false);
  };

  private scheduleReconnecting(): void {
    if (this.reconnectTimer) return;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.stopped) this.setConnection("reconnecting");
    }, RECONNECT_GRACE_MS);
  }

  private cancelReconnecting(): void {
    if (!this.reconnectTimer) return;
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
  }

  private readonly attachmentReads = new Map<string, Promise<string>>();
  private readonly attachmentCache = new Map<string, string>();
  private attachmentCacheSize = 0;
  private readonly previewReads = new Map<string, Promise<string>>();
  private previewActive = 0;
  private readonly previewWaiters: Array<() => void> = [];

  private readAttachmentPreview(ref: string): Promise<string> {
    const cached = this.previewReads.get(ref);
    if (cached) return cached;
    const read = (async () => {
      if (this.previewActive >= 2) await new Promise<void>(resolve => this.previewWaiters.push(resolve));
      else this.previewActive++;
      try {
        const result = await this.call<{data: string; total: number; content_type: string}>("remote/attachment/preview", { ref, thread_id: this.attachmentThreadID(ref) });
        if (result.content_type !== "image/jpeg" || result.total !== result.data.length || result.total > 128 * 1024) throw new Error("Invalid attachment preview");
        return `data:image/jpeg;base64,${result.data}`;
      } finally {
        const next = this.previewWaiters.shift();
        if (next) next(); else this.previewActive--;
      }
    })();
    this.previewReads.set(ref, read);
    void read.then(() => {
      if (this.previewReads.size > 48) this.previewReads.delete(this.previewReads.keys().next().value!);
    }, () => { if (this.previewReads.get(ref) === read) this.previewReads.delete(ref); });
    return read;
  }

  private readAttachment(ref: string): Promise<string> {
    const method = ref.startsWith("content:") ? "remote/content/read" : "remote/attachment/read";
    const retained = this.attachmentCache.get(ref);
    if (retained) {
      this.attachmentCache.delete(ref); this.attachmentCache.set(ref, retained);
      return Promise.resolve(retained);
    }
    const cached = this.attachmentReads.get(ref);
    if (cached) return cached;
    const read = (async () => {
      const first = await this.call<{ data: string; total: number; offset: number }>(method, { ref, offset: 0, thread_id: this.attachmentThreadID(ref) });
      if (!Number.isSafeInteger(first.total) || first.total < 0 || first.total > 64 * 1024 * 1024 || first.offset !== 0 || !first.data.length) throw new Error("Invalid attachment response");
      const chunks = [first.data];
      // Small independently encrypted chunks let control RPCs and live text
      // interleave with an explicitly requested image transfer.
      for (let offset = first.data.length; offset < first.total;) {
        const offsets: number[] = [];
        for (let n = 0; n < 4 && offset < first.total; n++, offset += first.data.length) offsets.push(offset);
        chunks.push(...await Promise.all(offsets.map(async position => {
          const chunk = await this.call<typeof first>(method, { ref, offset: position, thread_id: this.attachmentThreadID(ref) });
          if (chunk.total !== first.total || chunk.offset !== position || chunk.data.length !== Math.min(first.data.length, first.total - position)) throw new Error("Attachment changed during download");
          return chunk.data;
        })));
      }
      return chunks.join("");
    })();
    this.attachmentReads.set(ref, read);
    void read.then(data => {
      this.attachmentReads.delete(ref);
      if (data.length > 16 * 1024 * 1024) return;
      this.attachmentCache.set(ref, data); this.attachmentCacheSize += data.length;
      while (this.attachmentCacheSize > 16 * 1024 * 1024) {
        const key = this.attachmentCache.keys().next().value!;
        this.attachmentCacheSize -= this.attachmentCache.get(key)!.length;
        this.attachmentCache.delete(key);
      }
    }, () => this.attachmentReads.delete(ref));
    return read;
  }

  private attachmentThreadID(ref: string): string | undefined {
    if (!/^(thread|content):/.test(ref)) return undefined;
    const decoded = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(ref.slice(ref.indexOf(":") + 1).replace(/-/g, "+").replace(/_/g, "/")), char => char.charCodeAt(0))));
    return typeof decoded?.[0] === "string" ? decoded[0] : undefined;
  }

  private readonly projectID: string;

  readonly api: WuuDesktopApi;

  constructor(credentials: Credentials) {
    this.projectID = `remote:${credentials.host_pub}`;
    this.client = new RemoteClient(credentials, {
      // Do not use mobile_chat: the shared workbench needs the full event
      // stream, including tools, activities, usage, and lifecycle events.
      onNotification: (method, params, workdir) => {
        params = webHostPayload(params);
        this.recordThreadLocations(params);
        this.recordQuestionLocations(params, workdir || this.eventWorkdir(params));
        this.emitServerEvent({
          workdir: workdir || this.eventWorkdir(params),
          kind: "notification",
          message: { method, params },
        });
      },
      onServerRequest: (request) => this.handleServerRequest(request),
      onState: (state) => {
        this.hostState = state;
        this.emitRunning();
      },
      onAttach: ({ resumed }) => {
        this.cancelReconnecting();
        const first = !this.attachedOnce;
        this.attachedOnce = true;
        if (!resumed) {
          this.clearPendingRequests();
          this.needsRestore = true;
        }
        this.attachSync = this.synchronize(first, resumed);
        // connect() awaits the first sync; later failures are visible through
        // the connection snapshot and an explicit retry, never a page reload.
        void this.attachSync.catch(() => {});
      },
      onDetach: () => {
        if (this.stopped) return;
        // Keep a live workbench up through a brief drop so returning from
        // background does not flash the reconnect strip. In-flight RPCs still
        // fail because the revision moves. A drop that starts from any other
        // phase is already visible, so surface reconnecting immediately.
        if (this.connection.phase === "connected") {
          this.setConnection("connected");
          this.scheduleReconnecting();
          return;
        }
        this.cancelReconnecting();
        this.setConnection("reconnecting");
      },
    });
    this.api = this.createApi();
  }

  static async pair(uri: string, deviceName: string): Promise<Credentials> {
    return pair(uri, deviceName);
  }

  async connect(): Promise<void> {
    this.client.start();
    await this.client.waitAttached(30_000);
    await this.attachSync;
    this.hostState = this.client.latestState();
  }

  async disconnect(): Promise<void> {
    this.stopped = true;
    this.cancelReconnecting();
    this.setConnection("disconnected");
    this.clearPendingRequests();
    await this.client.stop();
  }

  wake(): void {
    if (!this.stopped) this.client.wake();
  }

  install(): void {
    window.wuu = this.api;
  }

  private assertAvailable(restorationRead = false): void {
    // Do not queue UI actions while offline: a delayed send after reconnect
    // would execute an instruction the user already saw fail.
    if (this.stopped || !this.client.isAttached()) throw new Error("Remote host is disconnected");
    if (this.connection.phase !== "connected" && !restorationRead) {
      throw new Error("Remote workspace is restoring; retry after it reconnects");
    }
  }

  private async call<T>(method: string, params?: unknown): Promise<T> {
    this.assertAvailable([
      "initialize", "workspace/list", "thread/list", "thread/listAll", "thread/listArchived", "thread/resume", "user-question/list",
    ].includes(method));
    const revision = this.connection.revision;
    const workdir = this.requestWorkdir(params);
    const result = await this.client.call<T>(method, params, 30_000, workdir);
    if (this.stopped || this.connection.revision !== revision) {
      throw new Error("Remote connection changed while the request was in flight");
    }
    this.recordThreadLocations(result);
    this.recordQuestionLocations(result, workdir);
    return webHostPayload(result);
  }

  private requestWorkdir(params: unknown): string {
    const input = params && typeof params === "object" ? params as Record<string, unknown> : {};
    if (typeof input.request_id === "string" && this.questionWorkdirs.has(input.request_id)) return this.questionWorkdirs.get(input.request_id)!;
    const id = input.thread_id ?? input.session_id ?? input.main_thread_id;
    const thread = typeof id === "string" ? this.threadLocations.get(id) : undefined;
    if (thread) return this.threadWorkdir(thread);
    const project = this.projects.find(project => project.id === input.workspace_id || project.path === input.cwd);
    return project?.path || this.workdir();
  }

  private recordQuestionLocations(value: unknown, workdir: string): void {
    if (!value || typeof value !== "object") return;
    const payload = value as { request?: { request_id?: string }; questions?: Array<{ request_id?: string }> };
    for (const request of [...(payload.questions ?? []), payload.request]) {
      if (request?.request_id) this.questionWorkdirs.set(request.request_id, workdir);
    }
  }

  private recordThreadLocations(value: unknown): void {
    if (!value || typeof value !== "object") return;
    const payload = value as { thread?: ThreadLocation; threads?: ThreadLocation[] };
    for (const thread of [...(Array.isArray(payload.threads) ? payload.threads : []), payload.thread]) {
      if (thread && typeof thread.id === "string" && typeof thread.cwd === "string") {
        this.threadLocations.set(thread.id, thread);
      }
    }
  }

  private threadWorkdir(thread: ThreadLocation): string {
    if (this.activeContext?.kind === "no_project" && this.activeContext.cwd === thread.cwd) return thread.cwd;
    return this.projects.find((project) => project.id === thread.workspace_id)?.path
      || thread.worktree?.base_repo || thread.cwd;
  }

  private eventWorkdir(params: unknown): string {
    const payload = params && typeof params === "object" ? params as { thread_id?: string; thread?: ThreadLocation } : undefined;
    const thread = payload?.thread ?? (payload?.thread_id ? this.threadLocations.get(payload.thread_id) : undefined);
    return thread ? this.threadWorkdir(thread) : this.defaultWorkdir;
  }

  private updateWorkspaces(snapshot: WorkspaceSnapshot): void {
    if (!snapshot.current) throw new Error("Remote host did not provide a workspace");
    this.defaultWorkdir = snapshot.current;
    const now = new Date().toISOString();
    const previous = new Map(this.projects.map((project) => [project.id, project]));
    this.remoteWorkspaceIDs.clear();
    for (const workspace of snapshot.workspaces ?? []) if (workspace.id) this.remoteWorkspaceIDs.add(workspace.id);
    if (snapshot.current_id) this.remoteWorkspaceIDs.add(snapshot.current_id);
    const workspaces = [...(snapshot.workspaces ?? [])];
    if (!workspaces.some((workspace) => workspace.path === snapshot.current || (snapshot.current_id && workspace.id === snapshot.current_id))) {
      workspaces.unshift({ id: snapshot.current_id || this.projectID, name: basename(snapshot.current), path: snapshot.current });
    }
    this.projects = workspaces.map((workspace) => {
      const id = workspace.id || `${this.projectID}:${workspace.path}`;
      return { id, name: workspace.name, path: workspace.path,
        created_at: previous.get(id)?.created_at ?? now, updated_at: now };
    });
    if (!this.activeContext) {
      const project = this.projects.find((project) => project.id === snapshot.current_id || project.path === snapshot.current)!;
      this.activeContext = { kind: "project", project_id: project.id, cwd: project.path };
    } else if (this.activeContext.kind === "project") {
      const projectID = this.activeContext.project_id;
      const selected = this.projects.find((project) => project.id === projectID);
      if (selected) this.activeContext = { kind: "project", project_id: selected.id, cwd: selected.path };
      // Removing a project on the desktop must not silently move a browser's
      // open draft to a different directory.
      else {
        const old = previous.get(this.activeContext.project_id);
        if (old) this.projects.push({ ...old, missing: true });
      }
    }
  }

  private workdir(): string {
    return this.activeContext?.cwd || this.defaultWorkdir || this.hostState?.host.workdir || "";
  }

  private projectState(): ProjectListResult {
    return {
      projects: this.projects,
      active_project_id: this.activeContext?.kind === "project" ? this.activeContext.project_id : undefined,
      active_context: this.activeContext,
    };
  }

  private runningSnapshot(): RunningThreadSnapshot[] {
    return (this.hostState?.running ?? []).map((turn) => ({
      workdir: this.threadLocations.has(turn.thread_id)
        ? this.threadWorkdir(this.threadLocations.get(turn.thread_id)!) : this.defaultWorkdir,
      thread_id: turn.thread_id,
    }));
  }

  private emitRunning(): void {
    const snapshot = this.runningSnapshot();
    for (const listener of this.runningListeners) listener(snapshot);
  }

  private emitServerEvent(event: ServerEvent): void {
    for (const listener of this.serverListeners) listener(event);
  }

  private handleServerRequest(request: ServerRequest): Promise<ServerRequestResult> {
    return new Promise((resolve) => {
      const id = String(request.id);
      this.pendingServerRequests.set(id, { resolve });
      this.emitServerEvent({
        workdir: this.eventWorkdir(request.params),
        kind: "server-request",
        message: { id, method: request.method, params: request.params },
      });
    });
  }

  private createApi(): WuuDesktopApi {
    const initialVoiceInputSettings: VoiceInputSettings = {
      polish_enabled: false,
      language: "system",
    };
    const target: WuuDesktopApi = {
      ...unavailableWebActions,
      hostKind: "web",
      onRuntimeRestore: (listener) => {
        this.restoreListeners.add(listener);
        return () => this.restoreListeners.delete(listener);
      },
      unsupportedMethods: unavailableWebMethods,
      platform: browserPlatform(),
      initialThemePreference: storedTheme(),
      initialLanguagePreference: storedLanguage(),
      initialSystemLocale: navigator.language,
      initialVoiceInputSettings,
      initialChannelRoomPreferences: storedChannelRoomPreferences(),
      initialMessageFlowFontSize: storedMessageSize(),
      popOutInit: () => ({ kind: null, threadID: null, context: null }),

      gitStatus: (root) => this.call("workspace/git/status", { root: root || this.workdir() }),
      gitActionBusy: (root) => this.call("desktop/git/busy", { root: root || this.workdir() }),
      checkoutGitBranch: (branch, root) => this.call("desktop/git/checkout", { branch, root: root || this.workdir() }),
      createCheckoutGitBranch: (branch, root) => this.call("desktop/git/create-branch", { branch, root: root || this.workdir() }),
      commitGitChanges: (params, root) => this.call("desktop/git/commit", { params, root: root || this.workdir() }),
      generateCommitMessage: (params, root) => this.call("desktop/git/commit-message", { params, root: root || this.workdir() }),
      createPullRequest: (params, root) => this.call("desktop/git/create-pr", { params, root: root || this.workdir() }),
      listGitChanges: (root) => this.call("workspace/git/changes", { root: root || this.workdir() }),
      readGitFileDiff: (path, root) => this.call("workspace/git/diff", { path, root: root || this.workdir() }),
      listWorkspaceDirectory: (path, root) => this.call("workspace/directory/list", { path, root: root || this.workdir() }),
      readWorkspaceFile: (path, root) => this.call("workspace/file/read", { path, root: root || this.workdir() }),
      resolveWorkspaceFileReference: (reference, root) => this.call("workspace/file/resolve", { reference, root: root || this.workdir() }),
      listProjects: async () => {
        this.updateWorkspaces(await this.call<WorkspaceSnapshot>("workspace/list"));
        return this.projectState();
      },
      selectProject: async (id) => {
        const project = this.projects.find((candidate) => candidate.id === id);
        if (!project) throw new Error("Unknown remote workspace");
        this.activeContext = { kind: "project", project_id: project.id, cwd: project.path };
        return this.projectState();
      },
      selectNoProject: async (fresh, cwd) => {
        if (fresh || !cwd || ![...this.threadLocations.values()].some((thread) => thread.cwd === cwd)) {
          throw new UnavailableHostOperationError("selectNoProject");
        }
        this.activeContext = { kind: "no_project", cwd };
        return this.projectState();
      },

      initialize: async () => {
        const result = await this.call<InitializeResult>("initialize", {
          client: { name: "wuu-web", version: "0.1" },
        });
        return {
          ...result,
          features: { ...result.features, browser: false },
        };
      },
      listThreads: (cwd?: string) => this.call("thread/list", { cwd: cwd || this.workdir(), summary_only: true }),
      listAllThreads: () => this.call("thread/listAll", { summary_only: true }),
      listArchivedThreads: () => this.call("thread/listArchived", { summary_only: true }),
      loadEarlierThreadHistory: async (threadID: string, cursor: string) => {
        const params = { thread_id: threadID, cursor };
        const result = await this.call("remote/history/read", params);
        this.emitServerEvent({ workdir: this.requestWorkdir(params), kind: "notification", message: { method: "thread/historyLoaded", params: result } });
      },
      readRemoteAttachment: (ref: string) => this.readAttachment(ref),
      readRemoteAttachmentPreview: (ref: string) => this.readAttachmentPreview(ref),
      readRemoteItem: async (ref: string) => {
        const data = await this.readAttachment(ref);
        return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(data), char => char.charCodeAt(0))));
      },
      resumeThread: async (sessionId?: string) => {
        const params = { session_id: sessionId ?? "", response_only: true };
        const result = await this.call<ThreadResumeResult>("thread/resume", params);
        // Preserve renderer and pending-message synchronization at the response boundary.
        this.emitServerEvent({ workdir: this.requestWorkdir(params), kind: "notification", message: { method: "thread/resumed", params: result } });
        return result;
      },
      startThread: (params = {}) => this.call("thread/start", {
        ...params,
        cwd: params.cwd || this.workdir(),
        workspace_id: params.workspace_id && this.remoteWorkspaceIDs.has(params.workspace_id)
          ? params.workspace_id : this.activeContext?.kind === "project" && this.remoteWorkspaceIDs.has(this.activeContext.project_id)
            ? this.activeContext.project_id : undefined,
      }),
      searchThreads: (query: string, limit?: number) =>
        this.call("thread/search", { query, limit }),
      getThreadPreview: (threadId: string, limit?: number) =>
        this.call("thread/preview", { thread_id: threadId, limit }),
      pinThread: (threadId: string, pinned: boolean) =>
        this.call("thread/pin", { thread_id: threadId, pinned }),
      renameThread: (threadId: string, title: string) =>
        this.call("thread/rename", { thread_id: threadId, title }),
      archiveThread: (threadId: string, archived: boolean, force?: boolean) =>
        this.call("thread/archive", { thread_id: threadId, archived, force }),
      deleteThread: (threadId: string) => this.call("thread/delete", { thread_id: threadId }),
      compactThread: (threadId: string) => this.call("thread/compact/start", { thread_id: threadId }),

      listChannelRooms: () => this.call("channel/room/list"),
      listNamedAgents: () => this.call("channel/agent/list"),

      startTurn: (threadId, prompt, images, files, permissionMode, activeDocument, contentParts) =>
        this.call("turn/start", {
          thread_id: threadId,
          prompt,
          images: images ?? [],
          files: files ?? [],
          ...(permissionMode === undefined ? {} : { permission_mode: permissionMode }),
          ...(activeDocument === undefined ? {} : { active_document: activeDocument }),
          ...(contentParts === undefined ? {} : { content_parts: contentParts }),
        }),
      queueTurn: (threadId, prompt, images, clientId, files, permissionMode, activeDocument, contentParts) =>
        this.call("turn/queue", {
          thread_id: threadId,
          prompt,
          images: images ?? [],
          files: files ?? [],
          client_id: clientId,
          ...(permissionMode === undefined ? {} : { permission_mode: permissionMode }),
          ...(activeDocument === undefined ? {} : { active_document: activeDocument }),
          ...(contentParts === undefined ? {} : { content_parts: contentParts }),
        }),
      updateQueuedTurn: (threadId, queueId, prompt, images, files, contentParts) =>
        this.call("turn/update-queued", {
          thread_id: threadId,
          queue_id: queueId,
          prompt,
          images: images ?? [],
          files: files ?? [],
          content_parts: contentParts,
        }),
      dequeueTurn: (threadId, queueId) =>
        this.call("turn/dequeue", { thread_id: threadId, queue_id: queueId }),
      steerTurn: (threadId, expectedTurnId, prompt, images, clientId, files, activeDocument, contentParts) =>
        this.call("turn/steer", {
          thread_id: threadId,
          expected_turn_id: expectedTurnId,
          prompt,
          images: images ?? [],
          files: files ?? [],
          client_id: clientId,
          active_document: activeDocument,
          content_parts: contentParts,
        }),
      unsteerTurn: (threadId, steerId) =>
        this.call("turn/unsteer", { thread_id: threadId, steer_id: steerId }),
      requeueTurn: (threadId, steerId) =>
        this.call("turn/requeue", { thread_id: threadId, steer_id: steerId }),
      interruptTurn: (threadId) => this.call("turn/interrupt", { thread_id: threadId }),

      listUserQuestions: (threadId?: string) =>
        this.call("user-question/list", threadId ? { thread_id: threadId } : undefined),
      answerUserQuestion: (requestId, answer) =>
        this.call("user-question/respond", { request_id: requestId, answer }),
      cancelUserQuestion: (requestId) =>
        this.call("user-question/cancel", { request_id: requestId }),

      onServerEvent: (listener: ServerEventListener) => {
        this.serverListeners.add(listener);
        return () => this.serverListeners.delete(listener);
      },
      getRunningThreadsSnapshot: async () => this.runningSnapshot(),
      onRunningThreadsChanged: (listener: RunningListener) => {
        this.runningListeners.add(listener);
        return () => this.runningListeners.delete(listener);
      },
      respondToServerRequest: async (id: string, result: unknown) => {
        this.assertAvailable();
        const pending = this.pendingServerRequests.get(id);
        this.pendingServerRequests.delete(id);
        pending?.resolve({ result });
      },
      rejectServerRequest: async (id: string, message: string) => {
        this.assertAvailable();
        const pending = this.pendingServerRequests.get(id);
        this.pendingServerRequests.delete(id);
        pending?.resolve({ error: { code: "rejected", message } });
      },

      listEngines: () => this.call("engine/list"),
      refreshModelCatalog: () => this.call("config/model-catalog/refresh"),
      listMCPServers: () => this.call("mcp/list"),
      startXAILogin: () => this.call("auth/xai/login/start"),
      listSkills: () => this.call("skill/list"),
      listInstructionFiles: () => this.call("instructions/list"),
      getNamedAgentInsights: () => this.call("channel/agent/insights"),
      bootstrapChannels: () => this.call("channel/bootstrap"),
      getChannelHumanMentionStatus: () => this.call("channel/human-mention/status"),
      ackChannelHumanMentions: () => this.call("channel/human-mention/ack"),
      getSessionOrganization: () => this.call("sessionOrganization/list"),
      getSettingsUsage: () => this.call("settings/usage"),
      refreshExtensionCatalog: () => this.call("extension/catalog/refresh"),
      updateAdvancedSettings: (params) => this.call("config/advanced/update", params),
      updateGeneralSettings: (params) => this.call("config/general/update", params),
      updateEngines: (params) => this.call("engine/update", params),
      updateExtensionPackage: (params) => this.call("extension/package/update", params),
      getPluginSetting: (params) => this.call("plugin/setting/get", params),
      setPluginSetting: (params) => this.call("plugin/setting/set", params),
      getPluginDiagnostics: (params) => this.call("plugin/diagnostics/list", params),
      getPluginStorage: (params) => this.call("plugin/storage/get", params),
      setPluginStorage: (params) => this.call("plugin/storage/set", params),
      requestPluginRuntime: (params) => this.call("plugin/client/request", params),
      createNamedAgent: (params) => this.call("channel/agent/create", params),
      updateNamedAgent: (params) => this.call("channel/agent/update", params),
      deleteNamedAgent: (params) => this.call("channel/agent/delete", params),
      startNamedAgent: (params) => this.call("channel/agent/start", params),
      resetNamedAgent: (params) => this.call("channel/agent/reset", params),
      resolveChannelAgentCreation: (params) => this.call("channel/agent-creation/resolve", params),
      createChannelRoom: (params) => this.call("channel/room/create", params),
      openChannelDirectMessage: (params) => this.call("channel/direct-message/open", params),
      updateChannelRoom: (params) => this.call("channel/room/update", params),
      deleteChannelRoom: (params) => this.call("channel/room/delete", params),
      markChannelRoomRead: (params) => this.call("channel/room/read", params),
      listChannelMessages: (params) => this.call("channel/message/list", params),
      sendChannelMessage: (params) => this.call("channel/message/send", params),
      createChannelTask: (params) => this.call("channel/task/create", params),
      updateChannelTask: (params) => this.call("channel/task/update", params),
      readManagedProcess: (params) => this.call("process/read", params),
      holdUserQuestion: (request_id) => this.call("user-question/hold", { request_id }),
      loadCodexModels: (provider) => this.call("config/codex/models", { provider }),
      removePluginPackage: (id) => this.call("plugin/package/remove", { id }),
      connectMCPServer: (name) => this.call("mcp/connect", { name }),
      disconnectMCPServer: (name) => this.call("mcp/disconnect", { name }),
      refreshMCPServer: (name) => this.call("mcp/refresh", { name }),
      startMCPAuth: (name) => this.call("mcp/auth/start", { name }),
      getMCPAuthStatus: (name) => this.call("mcp/auth/status", { name }),
      removeMCPAuth: (name) => this.call("mcp/auth/remove", { name }),
      pollXAILogin: (login_id) => this.call("auth/xai/login/poll", { login_id }),
      cancelXAILogin: (login_id) => this.call("auth/xai/login/cancel", { login_id }),
      listActivities: (thread_id) => this.call("activity/list", { thread_id }),
      getThreadContextComposition: (thread_id) => this.call("thread/context-composition", { thread_id }),
      polishText: (text) => this.call("text/polish", { text }),
      createSessionFolder: (name) => this.call("sessionFolder/create", { name }),
      reorderSessionFolders: (ids) => this.call("sessionFolder/reorder", { ids }),
      deleteSessionFolder: (id) => this.call("sessionFolder/delete", { id }),
      listManagedProcesses: (thread_id) => this.call("process/list", { thread_id }),
      takeoverActivity: (thread_id, activity_id) => this.call("activity/takeover", { thread_id, activity_id }),
      releaseActivity: (thread_id, activity_id) => this.call("activity/release", { thread_id, activity_id }),
      stopActivity: (thread_id, activity_id) => this.call("activity/stop", { thread_id, activity_id }),
      updateRuntimeSettings: (provider, model, effort, connection, variant, permissionMode, threadId) =>
        this.call("config/model/update", {
          ...(provider ? { provider } : {}),
          ...(model ? { model } : {}),
          ...(threadId ? { thread_id: threadId } : {}),
          ...connection,
          ...(effort === undefined ? {} : { effort }),
          ...(variant === undefined ? {} : { variant }),
          ...(permissionMode === undefined ? {} : { permission_mode: permissionMode }),
        }),
      removeProvider: (provider, options) => this.call("config/provider/remove", {
        provider, fallback_provider: options?.fallbackProvider, fallback_model: options?.fallbackModel,
      }),
      finishMCPAuth: (name, state, code) => this.call("mcp/auth/finish", { name, state, code }),
      forkThread: (thread_id, turn_id, item_id, mode, target) =>
        this.call("thread/fork", { thread_id, turn_id, item_id, mode, target }),
      editThreadMessage: (thread_id, turn_id, item_id) =>
        this.call("thread/edit-message", { thread_id, turn_id, item_id }),
      renameSessionFolder: (id, name) => this.call("sessionFolder/update", { id, name }),
      updateThreadOrganization: (thread_id, folder_id) =>
        this.call("thread/organization/update", { thread_id, folder_id }),
      writeManagedProcess: (thread_id, process_id, input) =>
        this.call("process/write", { thread_id, process_id, input }),
      resizeManagedProcess: (thread_id, process_id, cols, rows) =>
        this.call("process/resize", { thread_id, process_id, cols, rows }),
      stopManagedProcess: (thread_id, process_id) =>
        this.call("process/stop", { thread_id, process_id }),
      // Native event sources do not exist in a browser. These subscriptions
      // are inert; their actions are explicitly unavailable below.
      onSpeechRecognitionEvent: () => () => {},
      onRemoteControlEvent: () => () => {},
      onCodexPetJumpRequest: () => () => {},
      onTerminalEvent: () => () => {},
      onWindowResizeState: () => () => {},
      getThemePreference: async () => storedTheme(),
      setThemePreference: async (theme: ThemePreference) => {
        localStorage.setItem(THEME_KEY, theme);
        for (const listener of this.themeListeners) listener(theme);
        return { ok: true, theme };
      },
      onThemePreferenceChange: (listener: PreferenceListener<ThemePreference>) => {
        this.themeListeners.add(listener);
        return () => this.themeListeners.delete(listener);
      },
      getLanguagePreference: async () => storedLanguage(),
      setLanguagePreference: async (language: LanguagePreference) => {
        localStorage.setItem(LANGUAGE_KEY, language);
        for (const listener of this.languageListeners) listener(language);
        return { ok: true, language };
      },
      onLanguagePreferenceChange: (listener: PreferenceListener<LanguagePreference>) => {
        this.languageListeners.add(listener);
        return () => this.languageListeners.delete(listener);
      },
      getVoiceInputSettings: async () => ({
        settings: initialVoiceInputSettings,
        microphone_permission: "unavailable",
        speech_permission: "unavailable",
      }),
      onVoiceInputSettingsChange: (listener: PreferenceListener<VoiceInputSettings>) => {
        this.voiceListeners.add(listener);
        return () => this.voiceListeners.delete(listener);
      },
      getMessageFlowFontSize: async () => storedMessageSize(),
      setMessageFlowFontSize: async (fontSize: MessageFlowFontSize) => {
        localStorage.setItem(MESSAGE_SIZE_KEY, String(fontSize));
        return { ok: true, fontSize };
      },
      updateChannelRoomPreferences: async (preferences: ChannelRoomPreferences) => {
        localStorage.setItem(CHANNEL_ROOM_PREFERENCES_KEY, JSON.stringify(preferences));
        return preferences;
      },
      getPluginConflictPreferences: async () => storedPluginConflictPreferences(),
      setPluginConflictPreference: async (key: string, pluginId: string) => {
        const preferences = { ...storedPluginConflictPreferences(), [key]: pluginId };
        localStorage.setItem(PLUGIN_CONFLICT_PREFERENCES_KEY, JSON.stringify(preferences));
        return preferences;
      },
      openExternal: async (url: string) => {
        const parsed = new URL(url);
        if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
          throw new Error("Only HTTP and HTTPS links can be opened");
        }
        window.open(parsed.href, "_blank", "noopener,noreferrer");
      },
      showSystemNotification: async ({ title, body }: { title: string; body: string }) => {
        if ("Notification" in window && Notification.permission === "granted") {
          new Notification(title, { body });
          return { shown: true };
        }
        return { shown: false };
      },
    };

    return target;
  }
}
