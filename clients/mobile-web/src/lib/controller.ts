// The controller: owns the RemoteClient (relay link), feeds its notification
// stream into the AppStore, and exposes the handful of user actions the
// three screens need. Credential persistence is injected so this module
// stays pure TS (localStorage in the browser, anything else in tests).
// Ported from clients/mobile/src/lib/connection.ts with the native push and
// deep-link wiring removed; the browser client has no OS push tokens.

import {
  CLIENT_PROFILE_MOBILE_CHAT,
  Credentials,
  RemoteClient,
  pair as corePair,
} from "@wuu/remote-core";
import type { Thread, Turn } from "@wuu/protocol";

import { AppStore, type WorkspaceInfo } from "./store";
import { isThreadRunning } from "./threads";

export interface CredentialStore {
  load(): Promise<Credentials | null>;
  save(creds: Credentials): Promise<void>;
  clear(): Promise<void>;
  /** Unread cursors survive restarts (not secret; same storage for
   *  simplicity). Optional: in-memory stores may omit them. */
  loadLastViewed?(): Promise<Record<string, string> | null>;
  saveLastViewed?(lastViewed: Record<string, string>): Promise<void>;
}

type ThreadListResult = { threads: Thread[] };
type ThreadResult = { thread: Thread };
type ThreadStartResult = { thread: Thread };
type TurnResult = { turn: Turn };
type QueueResult = { queued: { id: string } };
type WorkspaceListResult = { workspaces: WorkspaceInfo[]; current?: string };

/** How long a brief link drop is allowed to last before the "重连中…" strip
 *  appears. Short enough that a real outage still surfaces within a second,
 *  long enough that foreground-return reattaches and wifi blips stay
 *  silent. */
const RECONNECT_GRACE_MS = 600;

export class WuuMobile {
  readonly store = new AppStore();
  private client: RemoteClient | null = null;
  private refreshTimer: ReturnType<typeof setTimeout> | null = null;
  private lastViewedTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private nextClientId = 0;

  constructor(private readonly credStore: CredentialStore) {
    this.store.onUnknownThread = () => this.scheduleThreadsRefresh();
    // Unread cursors persist (debounced) so a cold start doesn't mark every
    // conversation unread again.
    this.store.onLastViewedChanged = (lastViewed) => {
      if (!this.credStore.saveLastViewed) return;
      if (this.lastViewedTimer) clearTimeout(this.lastViewedTimer);
      this.lastViewedTimer = setTimeout(() => {
        this.lastViewedTimer = null;
        void this.credStore.saveLastViewed?.({ ...lastViewed }).catch(() => {});
      }, 500);
    };
  }

  /** True when stored credentials existed and the link is starting. */
  async startFromStoredCredentials(): Promise<boolean> {
    const creds = await this.credStore.load();
    if (!creds) return false;
    const lastViewed = await this.credStore.loadLastViewed?.().catch(() => null);
    if (lastViewed) this.store.seedLastViewed(lastViewed);
    this.start(creds);
    return true;
  }

  /** Completes pairing against a scanned/pasted URI, persists credentials,
   *  and brings the link up. */
  async pairWithUri(uri: string, deviceName: string): Promise<Credentials> {
    const creds = await corePair(uri.trim(), deviceName);
    await this.credStore.save(creds);
    this.start(creds);
    return creds;
  }

  async unpair(): Promise<void> {
    await this.credStore.clear();
    await this.client?.stop();
    this.client = null;
    this.store.resetServerState();
    this.store.setSyncError(null);
    this.store.setPhase("idle");
  }

  private start(creds: Credentials): void {
    this.store.setHostName(creds.host_name ?? "");
    this.store.setSyncError(null);
    this.store.setPhase("connecting");
    this.client = new RemoteClient(creds, {
      clientProfile: CLIENT_PROFILE_MOBILE_CHAT,
      onNotification: (method, params) => this.store.applyNotification(method, params),
      onState: (state) => this.store.setWorkdir(state.host?.workdir ?? ""),
      onAttach: (ev) => void this.onAttach(ev.resumed),
      onDetach: () => this.scheduleReconnecting(),
    });
    this.client.start();
  }

  /** Defer the "重连中…" phase by a short grace window: a transport drop
   *  that recovers within the window (foreground-return reattach, brief
   *  wifi blip) must not flash the strip. If attach lands first, we cancel
   *  the timer and never set the phase. */
  private scheduleReconnecting(): void {
    this.store.setSyncError(null);
    if (this.reconnectTimer) return;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.store.setPhase("reconnecting");
    }, RECONNECT_GRACE_MS);
  }

  private cancelReconnecting(): void {
    if (!this.reconnectTimer) return;
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
  }

  private async onAttach(resumed: boolean): Promise<void> {
    this.cancelReconnecting();
    this.store.setPhase("attached");
    try {
      if (!resumed) {
        // Fresh app-server connection: every server-state mirror is stale.
        this.store.resetServerState();
        await this.call("initialize");
      }
      await this.refreshWorkspaces();
      await this.refreshThreads();
      // The thread the user is looking at lost its full history
      // with the reset — re-resume it in place so the open chat refills.
      const active = this.store.getSnapshot().activeThreadId;
      if (!resumed && active) {
        await this.openThread(active);
      }
      this.store.setSyncError(null);
    } catch (error) {
      this.store.setSyncError(error instanceof Error ? error.message : String(error));
    }
  }

  /** Bounds the full operation, including both attach and RPC response. The
   *  core removes expired work so a late response cannot update stale UI. */
  private call<T>(method: string, params?: unknown, timeoutMs = 20_000): Promise<T> {
    const client = this.client;
    if (!client) return Promise.reject(new Error("未连接"));
    return client.call<T>(method, params, timeoutMs).catch((err: unknown) => {
      if (err instanceof Error && err.message === "attach timeout") {
        throw new Error("连接超时,请检查电脑端是否在线");
      }
      if (err instanceof Error && err.message.startsWith("rpc timeout:")) {
        throw new Error("请求超时,请稍后重试");
      }
      throw err instanceof Error ? err : new Error(String(err));
    });
  }

  async refreshThreads(): Promise<void> {
    const cwd = this.store.getSnapshot().activeWorkspacePath;
    const params = cwd ? { cwd } : undefined;
    const result = await this.call<ThreadListResult>("thread/list", params);
    this.store.setThreads(result.threads ?? []);
  }

  /** Pull the host's registered workspaces and keep the current selection
   *  pinned to the host's default workspace until the user chooses another. */
  async refreshWorkspaces(): Promise<void> {
    const result = await this.call<WorkspaceListResult>("workspace/list");
    const workspaces = result.workspaces ?? [];
    this.store.setWorkspaces(workspaces);
    const snapshot = this.store.getSnapshot();
    if (!snapshot.activeWorkspacePath) {
      const registered =
        workspaces.find((w) => w.path === result.current) ??
        workspaces.find((w) => w.path === snapshot.workdir) ??
        workspaces[0];
      const current = registered?.path || result.current || snapshot.workdir || "";
      if (current) this.store.setActiveWorkspacePath(current);
    }
  }

  /** Switch which registered workspace new conversations are created in. */
  async selectWorkspace(workspace: WorkspaceInfo): Promise<void> {
    this.closeThread();
    this.store.setActiveWorkspacePath(workspace.path);
    await this.refreshThreads();
  }

  private scheduleThreadsRefresh(): void {
    if (this.refreshTimer) return;
    this.refreshTimer = setTimeout(() => {
      this.refreshTimer = null;
      void this.refreshThreads().catch(() => {});
    }, 400);
  }

  /** thread/resume returns the full history in its result. */
  async openThread(threadId: string): Promise<void> {
    this.store.setActiveThread(threadId);
    const result = await this.call<ThreadResult>("thread/resume", { session_id: threadId });
    this.store.applyNotification("thread/resumed", result);
    this.store.setActiveThread(threadId); // re-advance unread cursor on fresh turns
  }

  closeThread(): void {
    const active = this.store.getSnapshot().activeThreadId;
    if (active) this.store.markViewed(active);
    this.store.setActiveThread(null);
  }

  /** Send, mirroring the desktop's chat semantics: turn/start when idle,
   *  turn/queue while the thread is mid-run (at-most-once either way). */
  async sendMessage(thread: Thread, text: string): Promise<void> {
    const prompt = text.trim();
    if (prompt === "") return;
    const clientId = `m-${Date.now()}-${++this.nextClientId}`;
    this.store.addPending({
      clientId,
      threadId: thread.id,
      text: prompt,
      atMs: Date.now(),
      queued: false,
    });
    try {
      if (isThreadRunning(thread)) {
        await this.call<QueueResult>("turn/queue", {
          thread_id: thread.id,
          prompt,
          images: [],
          files: [],
          client_id: clientId,
        });
        this.store.markPendingQueued(clientId);
      } else {
        const params: Record<string, unknown> = {
          thread_id: thread.id,
          prompt,
          images: [],
          files: [],
        };
        const result = await this.call<TurnResult>("turn/start", params);
        // Apply the result turn before dropping the pending bubble so the
        // sent message never flickers out while waiting for turn/started.
        if (result?.turn) {
          this.store.applyNotification("turn/started", { thread_id: thread.id, turn: result.turn });
        }
        this.store.removePending(clientId);
      }
    } catch (err) {
      this.store.removePending(clientId);
      throw err;
    }
  }

  async interrupt(threadId: string): Promise<void> {
    await this.call("turn/interrupt", { thread_id: threadId });
  }

  async togglePin(thread: Thread): Promise<void> {
    await this.call("thread/pin", { thread_id: thread.id, pinned: !thread.pinned });
    await this.refreshThreads();
  }

  /** Create a new conversation in the selected workspace. */
  async startThread(): Promise<Thread> {
    const snapshot = this.store.getSnapshot();
    const workspace = snapshot.workspaces.find((w) => w.path === snapshot.activeWorkspacePath);
    const params: Record<string, unknown> = {};
    if (snapshot.activeWorkspacePath) params.cwd = snapshot.activeWorkspacePath;
    if (workspace?.id) params.workspace_id = workspace.id;
    const result = await this.call<ThreadStartResult>("thread/start", params);
    await this.refreshThreads();
    return result.thread;
  }
}
