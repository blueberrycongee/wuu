import type {
  ApplicationBrowserV1, BrowserActivityIdentityV1, BrowserOperationOptionsV1,
  BrowserSurfaceHandleV1, BrowserTargetV1, BrowserDockTargetV1,
} from "../../../../packages/plugin-sdk/src/ui-browser";
import type { ActivitySession, WuuDesktopApi } from "../../shared/protocol";
import { parseLinkTarget } from "../LinkTargets";
import { workspaceBrowserOpenIntent, workspaceBrowserFocusDecision, type WorkspaceBrowserFocusState } from "../WorkspaceBrowserOpen";
import { consumeWorkspaceBrowserNavigation, requestWorkspaceBrowserNavigation } from "../WorkspaceBrowserNavigation";

type BrowserHost = Partial<Pick<WuuDesktopApi, "browserSurface" | "browserCommand" | "onBrowserSurface"
  | "onBrowserTabAdopted" | "onBrowserUserInput" | "reportBrowserBounds" | "suppressBrowserOverlay"
  | "takeoverActivity" | "openExternal" | "unsupportedMethods" | "onBrowserDock" | "onBrowserInvalidate">>;

/** Owns renderer presentation leases; native pages stay in the existing browser coordinator. */
export class ApplicationBrowser implements ApplicationBrowserV1 {
  private disposed = false;
  private readonly subscriptions = new Set<() => void>();
  private readonly leases = new Map<string, BrowserSurfaceHandleV1>();
  readonly available: boolean;
  constructor(private readonly host: { api: BrowserHost; onActivityChanged: (activity: ActivitySession) => void;
    workspaceNavigation?: { enabled: boolean; getFocusState: () => WorkspaceBrowserFocusState; openBrowser: (activate: boolean) => void | Promise<void> };
  }) {
    const required = ["browserSurface", "browserCommand", "onBrowserSurface", "onBrowserTabAdopted",
      "onBrowserUserInput", "reportBrowserBounds", "suppressBrowserOverlay", "onBrowserDock", "onBrowserInvalidate"] as const;
    this.available = required.every(name => typeof host.api[name] === "function" && !host.api.unsupportedMethods?.includes(name));
  }

  private check(options?: BrowserOperationOptionsV1): void {
    if (this.disposed || options?.signal?.aborted) throw new DOMException("Browser operation cancelled", "AbortError");
  }

  acquireSurface(source: BrowserTargetV1): BrowserSurfaceHandleV1 {
    this.check();
    if (!this.available) throw new Error("Browser surface unavailable");
    if (!source.workdir || !source.tabID) throw new Error("Browser surface requires a workspace and tab");
    const target = Object.freeze({ ...source });
    const key = JSON.stringify([target.workdir, target.tabID]);
    this.leases.get(key)?.dispose();
    const api = this.host.api;
    let active = true;
    const subscriptions = new Set<() => void>();
    const check = (options?: BrowserOperationOptionsV1) => {
      this.check(options);
      if (!active) throw new DOMException("Browser surface released", "AbortError");
    };
    const run = async <T>(operation: () => Promise<T>, options?: BrowserOperationOptionsV1): Promise<T> => {
      check(options); const value = await operation(); check(options); return value;
    };
    const subscribe = <T>(source: (listener: (value: T) => void) => () => void,
      matches: (value: T) => boolean, listener: (value: Readonly<T>) => void): (() => void) => {
      check();
      let subscribed = true;
      const nativeOff = source(value => {
        if (active && subscribed && !this.disposed && matches(value)) listener(Object.freeze({ ...value }));
      });
      const off = () => { if (!subscribed) return; subscribed = false; subscriptions.delete(off); nativeOff(); };
      subscriptions.add(off); return off;
    };
    const matches = (value: { workdir: string; tabID: string }) => value.workdir === target.workdir && value.tabID === target.tabID;
    const lease: BrowserSurfaceHandleV1 = {
      target,
      read: options => run(async () => {
        const value = await api.browserSurface!(target.workdir, target.tabID);
        return value && matches(value) ? Object.freeze({ ...value }) : null;
      }, options),
      command: (input, options) => run(async () => {
        const value = await api.browserCommand!({ ...target, command: input.command, url: input.url });
        return value && matches(value) ? Object.freeze({ ...value }) : null;
      }, options),
      onSurface: listener => subscribe(api.onBrowserSurface!, matches, listener),
      onTabAdopted: listener => subscribe(api.onBrowserTabAdopted!, value => value.workdir === target.workdir && value.openerTabID === target.tabID, listener),
      onUserInput: listener => subscribe(api.onBrowserUserInput!, matches, listener),
      reportBounds: (rect, force) => { if (active && !this.disposed) api.reportBrowserBounds!(target.workdir, target.tabID, rect && { ...rect }, force); },
      suppressOverlay: suppressed => { if (active && !this.disposed) api.suppressBrowserOverlay!(target.workdir, target.tabID, suppressed); },
      takeOver: async (activity: BrowserActivityIdentityV1, inputGeneration, options) => {
        const result = await run(async () => {
          if (!api.takeoverActivity) throw new Error("Browser takeover unavailable");
          if (activity.thread_id !== target.threadID) throw new Error("Browser activity does not belong to the surface thread");
          return api.takeoverActivity(activity.thread_id, activity.id, inputGeneration, activity.execution_id);
        }, options);
        this.host.onActivityChanged(result.activity);
      },
      dispose: () => {
        if (!active) return;
        active = false;
        for (const off of [...subscriptions]) off();
        // Only this lease may park its page; an old React cleanup cannot hide a replacement.
        if (this.leases.get(key) === lease) {
          this.leases.delete(key);
          api.reportBrowserBounds!(target.workdir, target.tabID, null);
          api.suppressBrowserOverlay!(target.workdir, target.tabID, false);
        }
      },
    };
    this.leases.set(key, lease);
    return lease;
  }

  private listen<T>(subscribe: ((listener: (value: T) => void) => () => void) | undefined, listener: (value: Readonly<T>) => void): () => void {
    this.check();
    if (!subscribe) throw new Error("Browser events unavailable");
    let active = true;
    const nativeOff = subscribe(value => { if (active && !this.disposed) listener(Object.freeze({ ...value })); });
    const off = () => { if (!active) return; active = false; this.subscriptions.delete(off); nativeOff(); };
    this.subscriptions.add(off); return off;
  }

  onDock(listener: (target: BrowserDockTargetV1) => void): () => void { return this.listen(this.host.api.onBrowserDock, listener); }
  onInvalidate(listener: (event: { readonly workdir: string }) => void): () => void { return this.listen(this.host.api.onBrowserInvalidate, listener); }

  async openURL(input: Parameters<ApplicationBrowserV1["openURL"]>[0], options?: BrowserOperationOptionsV1): Promise<void> {
    this.check(options);
    const parsed = parseLinkTarget(input.url);
    if (parsed.kind !== "external") throw new TypeError("Unsupported browser URL");
    const intent = workspaceBrowserOpenIntent(parsed.url, input.modifiers);
    if (!intent.target || intent.external || this.host.workspaceNavigation?.enabled === false) {
      await this.openExternal(intent.target?.url ?? parsed.url, options); return;
    }
    const navigation = this.host.workspaceNavigation;
    if (!navigation) throw new Error("Embedded browser navigation unavailable");
    const focus = workspaceBrowserFocusDecision(navigation.getFocusState());
    requestWorkspaceBrowserNavigation(intent.target);
    await navigation.openBrowser(focus.stealFocus);
    this.check(options);
  }

  async openExternal(url: string, options?: BrowserOperationOptionsV1): Promise<void> {
    this.check(options);
    if (!this.host.api.openExternal) throw new Error("External browser unavailable");
    await this.host.api.openExternal(url);
    this.check(options);
  }

  consumeNavigation(requestID: number): void { this.check(); consumeWorkspaceBrowserNavigation(requestID); }

  dispose(): void {
    if (this.disposed) return;
    for (const lease of [...this.leases.values()]) lease.dispose();
    for (const off of [...this.subscriptions]) off();
    this.disposed = true;
  }
}
