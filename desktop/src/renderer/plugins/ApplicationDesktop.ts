import type { ApplicationDesktopControlsV1, ApplicationDesktopPetTargetV1, ApplicationDesktopContextMenuV1 } from "../../../../packages/plugin-sdk/src/ui-desktop";
import type { CodexPetCommand, CodexPetFeed, WuuDesktopApi } from "../../shared/protocol";
import type { ChromeOverlayApi, ChromeOverlayEvent } from "../../shared/chromeOverlay";

type DesktopBridge = WuuDesktopApi & { contextMenu?: ApplicationDesktopContextMenuV1; chromeOverlay?: ChromeOverlayApi; setActiveCUAThread?: (threadID?: string) => void };

/** Window authority and pet delivery live outside replaceable UI generations. */
export class ApplicationDesktop {
  private readonly bridge = window.wuu as DesktopBridge;
  private readonly scopes = new Set<() => void>();
  private nativeWrites: Promise<unknown> = Promise.resolve();
  private controlsOwner?: object;
  private cuaOwner?: object;
  private disposed = false;
  private petOff?: () => void;
  private petEpoch = 0;
  private readonly petPending = new Set<string>();
  private feedPending?: CodexPetFeed;
  private feedWriting = false;
  private feedKey?: string;
  private readonly popOut: ApplicationDesktopControlsV1["popOut"];

  constructor(private readonly host?: {
    getPetState(): { ready: boolean; feed: CodexPetFeed };
    jumpToThread(threadId: string): Promise<void>;
    submitPetText(text: string, target: ApplicationDesktopPetTargetV1): Promise<boolean>;
  }) {
    const initial = this.available("popOutInit") ? this.bridge.popOutInit() : { kind: null, threadID: null, context: null };
    this.popOut = Object.freeze({ ...initial, context: initial.context && Object.freeze({ ...initial.context }) });
  }
  private available(key: keyof DesktopBridge): boolean {
    return typeof this.bridge[key] === "function" && !this.bridge.unsupportedMethods?.includes(key as keyof WuuDesktopApi);
  }
  private enqueue<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.nativeWrites.then(operation);
    this.nativeWrites = result.catch(() => {});
    return result;
  }
  createScope(signal: AbortSignal): ApplicationDesktopControlsV1 {
    if (this.disposed) throw new Error("Desktop service is disposed");
    signal.throwIfAborted();
    let active = true;
    const owner = {};
    const prefix = `${crypto.randomUUID()}:`;
    const overlays = new Map<string, number>();
    const menus = new Set<string>();
    const subscriptions = new Set<() => void>();
    const check = () => { signal.throwIfAborted(); if (!active || this.disposed) throw new Error("Desktop UI generation is retired"); };
    const call = async <T>(operation: () => Promise<T>): Promise<T> => { check(); const result = await operation(); check(); return result; };
    const subscribe = <T>(start: (listener: (value: T) => void) => () => void, listener: (value: T) => void): (() => void) => {
      check(); const stop = start(value => { if (active && !this.disposed && !signal.aborted) listener(value); });
      const off = () => { if (subscriptions.delete(off)) stop(); };
      subscriptions.add(off); return off;
    };
    const retire = () => {
      if (!active) return;
      active = false; signal.removeEventListener("abort", retire); this.scopes.delete(retire);
      for (const off of [...subscriptions]) off();
      for (const token of menus) void this.bridge.contextMenu!.dismiss(prefix + token).catch(error => console.error("[desktop] context menu cleanup failed", error));
      menus.clear();
      if (this.cuaOwner === owner) { this.cuaOwner = undefined; this.bridge.setActiveCUAThread?.(undefined); }
      void this.enqueue(async () => {
        if (this.controlsOwner === owner) { this.controlsOwner = undefined; await this.bridge.windowSetControls!({ visible: false, x: 0, y: 0 }).catch(() => {}); }
        await Promise.allSettled([...overlays].map(([token, generation]) => this.bridge.chromeOverlay!.hide(prefix + token, generation)));
        overlays.clear();
      }).catch(() => {});
    };
    this.scopes.add(retire); signal.addEventListener("abort", retire, { once: true });
    const overlay = this.bridge.chromeOverlay;
    const controls: ApplicationDesktopControlsV1 = {
      platform: this.bridge.platform, hostKind: this.bridge.hostKind ?? "desktop", popOut: this.popOut,
      capabilities: Object.freeze({ account: this.available("remoteAccount"), pets: this.available("updateCodexPetFeed"), nativeControls: this.available("windowSetControls"), chromeOverlay: Boolean(overlay), notifications: this.available("showSystemNotification") }),
      account: this.available("remoteAccount") ? (action, input) => call(() => this.bridge.remoteAccount!(action, input)) : undefined,
      openAccountWindow: this.available("openAccountWindow") ? () => call(() => this.bridge.openAccountWindow!()) : undefined,
      closeAccountWindow: this.available("closeAccountWindow") ? () => call(() => this.bridge.closeAccountWindow!()) : undefined,
      minimize: this.available("windowMinimize") ? () => call(() => this.bridge.windowMinimize!()) : undefined,
      close: this.available("windowClose") ? () => call(() => this.bridge.windowClose!()) : undefined,
      toggleMaximize: this.available("windowToggleMaximize") ? () => call(() => this.bridge.windowToggleMaximize!()) : undefined,
      toggleFullScreen: this.available("windowToggleFullScreen") ? () => call(() => this.bridge.windowToggleFullScreen!()) : undefined,
      isMaximized: this.available("windowIsMaximized") ? () => call(() => this.bridge.windowIsMaximized!()) : undefined,
      isFullScreen: this.available("windowIsFullScreen") ? () => call(() => this.bridge.windowIsFullScreen!()) : undefined,
      pointerPosition: this.available("windowPointerPosition") ? () => call(() => this.bridge.windowPointerPosition!()) : undefined,
      setControls: this.available("windowSetControls") ? state => call(() => this.enqueue(async () => { check(); this.controlsOwner = owner; await this.bridge.windowSetControls!({ ...state }); })) : undefined,
      onResizeState: this.available("onWindowResizeState") ? listener => subscribe(handler => this.bridge.onWindowResizeState(handler), listener) : undefined,
      onMaximizedChange: this.available("onWindowMaximizedChange") ? listener => subscribe(handler => this.bridge.onWindowMaximizedChange!(handler), listener) : undefined,
      onFullScreenChange: this.available("onWindowFullScreenChange") ? listener => subscribe(handler => this.bridge.onWindowFullScreenChange!(handler), listener) : undefined,
      onVisibilityChange: listener => subscribe(handler => { const changed = () => handler(document.visibilityState !== "hidden"); document.addEventListener("visibilitychange", changed); return () => document.removeEventListener("visibilitychange", changed); }, listener),
      showNotification: this.available("showSystemNotification") ? params => call(() => this.bridge.showSystemNotification(params)) : undefined,
      setActiveCUAThread: this.available("setActiveCUAThread") ? threadId => { check(); this.cuaOwner = owner; this.bridge.setActiveCUAThread!(threadId); } : undefined,
      contextMenu: this.bridge.contextMenu ? Object.freeze({
        show: (request: Parameters<ApplicationDesktopContextMenuV1["show"]>[0]) => call(async () => {
          menus.add(request.token);
          try { return await this.bridge.contextMenu!.show({ ...structuredClone(request), token: prefix + request.token }); }
          finally { menus.delete(request.token); }
        }),
        dismiss: async (token: string) => {
          if (!menus.delete(token)) return;
          await this.bridge.contextMenu!.dismiss(prefix + token);
        },
      }) : undefined,
      chromeOverlay: overlay ? Object.freeze({
        show: (request: Parameters<ChromeOverlayApi["show"]>[0]) => {
          const captured = structuredClone(request);
          return call(() => this.enqueue(async () => { check(); overlays.set(captured.token, captured.generation); await overlay.show({ ...captured, token: prefix + captured.token }); }));
        },
        hide: (token: string, generation: number) => call(() => this.enqueue(async () => { check(); await overlay.hide(prefix + token, generation); if ((overlays.get(token) ?? Infinity) <= generation) overlays.delete(token); })),
        onEvent: (listener: Parameters<ChromeOverlayApi["onEvent"]>[0]) => subscribe<ChromeOverlayEvent>(handler => overlay.onEvent(handler), event => {
          if (!event.token.startsWith(prefix)) return;
          const token = event.token.slice(prefix.length); const generation = overlays.get(token);
          if (generation === undefined) return;
          const lifecycle = ["dismiss", "pointer", "reveal"].includes(event.command.type);
          if (lifecycle ? event.generation > generation : event.generation !== generation) return;
          listener({ ...event, token });
        }),
      }) : undefined,
    };
    return Object.freeze(controls);
  }

  /** Called by the canonical host owner after state changes, never by a selected shell. */
  syncPet(): void {
    if (this.disposed || !this.host) return;
    const state = this.host.getPetState();
    if (state.ready && !this.petOff && this.available("onCodexPetCommand") && this.available("resolveCodexPetCommand")) {
      const epoch = ++this.petEpoch;
      this.petOff = this.bridge.onCodexPetCommand(command => { if (!this.disposed && epoch === this.petEpoch) this.petCommand(command); });
    } else if (!state.ready && this.petOff) this.stopPet();
    this.publishFeed(state.ready ? state.feed : { hints: [] });
  }
  private petCommand(command: CodexPetCommand): void {
    const captured = structuredClone(command);
    if (captured.kind === "jump") { void this.host!.jumpToThread(captured.thread_id).catch(() => {}); return; }
    if (this.petPending.has(captured.id)) return;
    this.petPending.add(captured.id);
    void Promise.resolve().then(() => {
      if (!this.petPending.has(captured.id)) return false;
      return this.host!.submitPetText(captured.text, captured.target);
    }).catch(() => false).then(ok => this.settlePet(captured.id, ok));
  }
  private settlePet(id: string, ok: boolean): void {
    if (this.petPending.delete(id)) this.bridge.resolveCodexPetCommand(id, { ok });
  }
  private stopPet(): void {
    this.petEpoch++; this.petOff?.(); this.petOff = undefined;
    for (const id of [...this.petPending]) this.settlePet(id, false);
  }
  private publishFeed(feed: CodexPetFeed): void {
    if (!this.available("updateCodexPetFeed")) return;
    const key = JSON.stringify(feed); if (key === this.feedKey) return;
    this.feedKey = key; this.feedPending = structuredClone(feed);
    if (!this.feedWriting) void this.writeFeed();
  }
  private async writeFeed(): Promise<void> {
    this.feedWriting = true;
    try {
      while (this.feedPending) {
        const feed = this.feedPending; this.feedPending = undefined;
        try { await this.bridge.updateCodexPetFeed(feed); } catch { this.feedKey = undefined; }
      }
    } finally { this.feedWriting = false; }
  }
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true; for (const retire of [...this.scopes]) retire();
    if (this.host) { this.stopPet(); this.publishFeed({ hints: [] }); }
  }
}
