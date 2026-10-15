import type { Rectangle } from "electron";
import type { ActivitySession } from "../shared/protocol";
import { ActivityPiPSurface, type ActivityPiPSurfaceDeps } from "./activityPiPWindow";
import { pipContainRect, type ActivityPiPContent, type ActivityPiPContentContext } from "./activityPiPContent";
import type { BrowserHostCoordinator } from "./browserHostWindows";
import type { BrowserPiPHostWindow, ObservationPiPFactory } from "./cuaActivityWindows";
import { NativePiPContent } from "./nativePiPContent";
import { resolveCUAFrameHelper } from "./cuaFrameStreams";

export { browserPiPOverlayHTML } from "./activityPiPChrome";
export { pipContainRect } from "./activityPiPContent";
export type { BrowserPiPWindowHandle, BrowserPiPOverlayHandle } from "./activityPiPWindow";

export type BrowserPiPTabHost = Pick<BrowserHostCoordinator,
  | "tabBounds" | "tabSurfaceMeta" | "mountTabOnWindow" | "unmountTabIfOwner"
  | "relayoutMountedTab" | "captureMountedTab" | "addInteractionListener" | "addTabClosedListener"
  | "addNavigateListener" | "addTabReparentedListener"
>;

export function pipHostname(url: string): string {
  const match = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\/([^/?#]+)/.exec(url);
  return match?.[1] ?? (url.trim() || "about:blank");
}

// Browser adapter mounts the existing live tab. It never owns an OS window and
// never captures screenshots or forwards pointer input into the watched page.
export class BrowserPiPContent implements ActivityPiPContent {
  private mounted = false;
  private announced = false;
  private visible = false;
  private stopped = false;
  private restoreBounds: Rectangle | undefined;
  private viewport: { width: number; height: number } | undefined;
  private bounds: Rectangle;
  private readonly unsubs: Array<() => void> = [];
  private readonly workdir: string;
  private readonly tabID: string;

  constructor(
    private activity: ActivitySession,
    private readonly host: BrowserPiPTabHost,
    private readonly context: ActivityPiPContentContext,
  ) {
    this.workdir = activity.workdir;
    this.tabID = activity.target!.trim();
    this.bounds = context.window.getBounds();
  }

  start(): void {
    this.unsubs.push(
      this.host.addTabClosedListener((workdir, tabID) => {
        if (this.matches(workdir, tabID)) this.context.onGone();
      }),
      this.host.addInteractionListener((workdir, tabID, hint) => {
        if (this.matches(workdir, tabID)) return this.context.onInteraction(hint);
      }),
      this.host.addNavigateListener((workdir, tabID, url) => {
        if (!this.matches(workdir, tabID)) return;
        this.context.setLabel(pipHostname(url));
        this.layout(this.bounds);
      }),
      this.host.addTabReparentedListener((workdir, tabID, parent) => {
        if (!this.matches(workdir, tabID) || !this.mounted) return;
        if (parent !== this.context.window.contentView) {
          this.mounted = false;
          this.restoreBounds = undefined;
          this.context.setMounted(false);
        }
      }),
    );
    this.refresh();
  }

  setVisible(visible: boolean): void {
    this.visible = visible;
    if (visible) this.mount();
    else this.unmount();
  }

  updateActivity(activity: ActivitySession): void {
    this.activity = activity;
    if (this.visible) this.mount();
  }

  refresh(): void {
    this.context.setLabel(pipHostname(this.host.tabSurfaceMeta(this.workdir, this.tabID)?.url ?? ""));
    this.context.setMounted(this.mounted);
  }

  layout(bounds: Rectangle): void {
    this.bounds = bounds;
    if (!this.mounted) return;
    const fit = this.fit();
    this.host.relayoutMountedTab(this.workdir, this.tabID, this.context.window.contentView,
      { x: fit.x, y: fit.y, width: fit.width, height: fit.height }, fit.scale);
  }

  snapshot(): Promise<string | undefined> {
    if (!this.mounted) return Promise.resolve(undefined);
    return this.host.captureMountedTab(this.workdir, this.tabID, this.context.window.contentView);
  }

  stop(onStopped: () => void): void {
    this.stopped = true;
    for (const unsub of this.unsubs.splice(0)) unsub();
    this.unmount();
    onStopped();
  }

  private mount(): void {
    const win = this.context.window;
    if (this.stopped || win.isDestroyed() || this.mounted) return;
    const bounds = this.host.tabBounds(this.workdir, this.tabID);
    if (!bounds) {
      if (this.activity.state !== "starting") this.context.onGone();
      return;
    }
    if (bounds.width > 0 && bounds.height > 0) {
      this.viewport = { width: bounds.width, height: bounds.height };
      this.context.setViewport(this.viewport);
    }
    const fit = this.fit();
    const restore = this.host.mountTabOnWindow(this.workdir, this.tabID, win,
      { x: fit.x, y: fit.y, width: fit.width, height: fit.height }, fit.scale);
    if (!restore) {
      if (this.activity.state !== "starting") this.context.onGone();
      return;
    }
    this.restoreBounds = restore;
    this.mounted = true;
    this.refresh();
    if (!this.announced) {
      this.announced = true;
      this.context.onEvent({ event: "ready" });
    }
  }

  private unmount(): void {
    if (!this.mounted) return;
    this.mounted = false;
    const restore = this.restoreBounds;
    this.restoreBounds = undefined;
    const win = this.context.window;
    if (!win.isDestroyed() && restore) {
      this.host.unmountTabIfOwner(this.workdir, this.tabID, win.contentView, restore);
    }
    this.context.setMounted(false);
  }

  private fit(): ReturnType<typeof pipContainRect> {
    const viewport = this.viewport ?? this.bounds;
    return pipContainRect(viewport.width, viewport.height, this.bounds.width, this.bounds.height);
  }

  private matches(workdir: string, tabID: string): boolean {
    return !this.stopped && workdir === this.workdir && tabID === this.tabID;
  }
}

// Retained for existing browser-only integrations; production uses the same host
// with either adapter via createObservationPiPFactory below.
export class BrowserPiPSurface extends ActivityPiPSurface {
  constructor(deps: Omit<ActivityPiPSurfaceDeps, "createContent"> & {
    host: BrowserPiPTabHost; workdir: string; tabID: string;
  }) {
    super({ ...deps, createContent: (activity, context) => new BrowserPiPContent(activity, deps.host, context) });
  }
}

export function createObservationPiPFactory(deps: {
  browserHost: BrowserHostCoordinator;
  isPackaged: boolean;
  parent?: () => BrowserPiPHostWindow | null | undefined;
}): ObservationPiPFactory {
  return (activity, _key, sink, bounds) => new ActivityPiPSurface({
    activity, bounds: bounds(), sink, isPackaged: deps.isPackaged, parent: deps.parent,
    createContent: (next, context) => {
      if (next.kind === "browser") return new BrowserPiPContent(next, deps.browserHost, context);
      const helper = resolveCUAFrameHelper();
      return helper ? new NativePiPContent(next, context, { helper, isPackaged: deps.isPackaged }) : undefined;
    },
  });
}
