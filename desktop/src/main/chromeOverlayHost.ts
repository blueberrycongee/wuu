import { BrowserWindow, WebContentsView, net, session, type IpcMainEvent, type IpcMainInvokeEvent, type WebContents } from "electron";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { resolve, sep } from "node:path";
import type { ChromeOverlayCommand, ChromeOverlayEvent, ChromeOverlayRequest, ChromeOverlayRendererRequest, ChromeOverlaySurface } from "../shared/chromeOverlay";
import { CHROME_OVERLAY_VERSION } from "../shared/chromeOverlay";

export const chromeOverlayScheme = { scheme: "wuu-chrome", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } };
const ORIGIN = "wuu-chrome://ui";
const PARTITION = "wuu-chrome-overlays";
const OPENING_TIMEOUT_MS = 10_000;
type Surface = { owner: BrowserWindow; view: WebContentsView; kind: ChromeOverlaySurface; phase: ChromeOverlayRendererRequest["phase"]; request?: ChromeOverlayRequest; loaded: boolean; focusPending?: boolean; focusBefore?: WebContents; openingTimeout?: ReturnType<typeof setTimeout> };

/** Bounded, trusted UI views. Transparent pixels still hit-test inside these rectangles. */
export class ChromeOverlayHost {
  private surfaces = new Map<ChromeOverlaySurface, Surface>();
  private owner?: BrowserWindow;
  private initialized = false;
  private fullscreen = false;
  private focusSources = new Map<WebContents, () => void>();
  // Vite rewrites these existing imports to /@fs/. Keep the exception finite:
  // neither arbitrary checkout files nor arbitrary developer-machine files.
  private readonly devFiles: Set<string>;

  constructor(private readonly emit: (owner: BrowserWindow, event: ChromeOverlayEvent) => void,
    private readonly rendererRoot: string, private readonly preload: string, private readonly devURL?: string) {
    this.devFiles = new Set([
      "packages/protocol/src/index.ts",
      "assets/app-icon-source.json",
    ].map(file => resolve(rendererRoot, "../../..", file)));
  }

  private async fetchDev(path: string): Promise<Response> {
    const response = await net.fetch(new URL(path, this.devURL!).href);
    if (path !== "/chrome-overlay.html") return response;
    // React Refresh adds a trusted inline preamble in development. Hash only
    // this fixed document's generated scripts; production keeps self-only CSP.
    let html = await response.text();
    const hashes = [...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi)]
      .filter(match => match[1].trim()).map(match => `'sha256-${createHash("sha256").update(match[1]).digest("base64")}'`);
    html = html.replace("script-src 'self'", `script-src 'self' ${hashes.join(" ")}`);
    return new Response(html, { status: response.status, headers: { "Content-Type": "text/html", "Cache-Control": "no-store" } });
  }

  private initialize(): void {
    if (this.initialized) return;
    this.initialized = true;
    const ui = session.fromPartition(PARTITION);
    ui.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
    ui.setPermissionCheckHandler(() => false);
    ui.setDevicePermissionHandler(() => false);
    ui.on("will-download", event => event.preventDefault());
    ui.protocol.handle("wuu-chrome", async request => {
      const url = new URL(request.url);
      if (url.protocol !== "wuu-chrome:" || url.hostname !== "ui" || url.username || url.password || url.port || request.method !== "GET") return new Response(null, { status: 403 });
      let path: string;
      try { path = decodeURIComponent(url.pathname); } catch { return new Response(null, { status: 400 }); }
      if (path.includes("\\") || path.includes("\0") || path.split("/").some(part => part === "." || part === "..")) return new Response(null, { status: 403 });
      if (/^\/plugin-icon\/[a-f0-9]{64}$/.test(path)) {
        const response = await net.fetch(`wuu-plugin://asset/${path.split("/").pop()}`);
        return new Response(await response.arrayBuffer(), { status: response.status, headers: { "Content-Type": response.headers.get("Content-Type") ?? "application/octet-stream" } });
      }
      if (path === "/chrome-overlay.html" || /^\/assets\/[\w.-]+\.(?:js|css|svg|png|webp|woff2?|ttf|otf)$/.test(path)) {
        if (this.devURL) return this.fetchDev(path);
        const file = resolve(this.rendererRoot, `.${path}`);
        if (!file.startsWith(resolve(this.rendererRoot) + sep)) return new Response(null, { status: 403 });
        try {
          const body = await readFile(file);
          const type = path.endsWith(".html") ? "text/html" : path.endsWith(".js") ? "text/javascript" : path.endsWith(".css") ? "text/css" : path.endsWith(".svg") ? "image/svg+xml" : "application/octet-stream";
          return new Response(body, { headers: { "Content-Type": type, "Cache-Control": "no-store" } });
        } catch { return new Response(null, { status: 404 }); }
      }
      const fsPath = path.startsWith("/@fs/") ? path.slice(5) : undefined;
      const allowedDevFile = fsPath !== undefined && this.devFiles.has(resolve(fsPath.startsWith("/") || /^[a-z]:/i.test(fsPath) ? fsPath : `/${fsPath}`));
      // The development server transpiles only this checkout's UI/source imports.
      if (this.devURL && path === "/assets/app-icon-source.json") return net.fetch(new URL(url.pathname + url.search, this.devURL).href);
      // Vite serves this linked UI dependency from its source tree in dev.
      // Keep package metadata, demos and other vendor directories inaccessible.
      const blobatarSource = /^\/vendor\/blobatar\/src\/(?:[\w-]+\/)*[\w-]+\.(?:ts|tsx|css)$/.test(path);
      if (this.devURL && (blobatarSource || allowedDevFile || path === "/@vite/client" || path === "/@react-refresh" || path === "/node_modules/vite/dist/client/env.mjs" || /^\/(?:src\/(?:renderer|shared)\/|node_modules\/.vite\/)/.test(path))) {
        return net.fetch(new URL(url.pathname + url.search, this.devURL).href);
      }
      return new Response(null, { status: 403 });
    });
  }

  prepare(owner: BrowserWindow): void {
    this.initialize(); this.observe(owner);
    if (!this.surfaces.has("account")) this.create(owner, "account");
    this.raise(owner);
  }

  show(owner: BrowserWindow, request: ChromeOverlayRequest): void {
    this.validate(request);
    if (!owner.isVisible() || owner.isMinimized() || owner.webContents.isDestroyed()) return;
    this.initialize();
    this.observe(owner);
    const kind = request.model.kind;
    let surface = this.surfaces.get(kind);
    const focusBefore = owner.contentView.children.map(child => (child as WebContentsView).webContents)
      .find(contents => contents && contents !== surface?.view.webContents && !contents.isDestroyed() && contents.isFocused()) ?? owner.webContents;
    if (!surface) surface = this.create(owner, kind);
    const previous = surface.request;
    if (previous?.token === request.token && previous.generation > request.generation) return;
    if (!previous || previous.token !== request.token) {
      surface.focusBefore = focusBefore;
      surface.focusPending = false;
      surface.phase = "prepare";
      surface.view.setVisible(false);
    }
    if (request.focus && (!previous || previous.token !== request.token || !previous.focus)) surface.focusPending = true;
    surface.request = request;
    if (previous?.token !== request.token) this.cancelOpening(surface);
    // Hidden Chromium renderers suspend rAF. Unthrottle only the finite paint
    // handshake; once presented (or dismissed), normal visibility throttling resumes.
    if (surface.phase !== "presented" && !this.fullscreen) {
      surface.view.webContents.setBackgroundThrottling(false);
      // A successful document load does not prove its JS mounted or acknowledged
      // the model. Bound the opening, not every streaming/geometry revision.
      surface.openingTimeout ??= setTimeout(() => this.fail(surface), OPENING_TIMEOUT_MS);
    }
    if (!this.layout(surface) && surface.loaded) surface.view.webContents.send("wuu:chrome-overlay-state", this.rendererRequest(surface));
    this.preserveOpeningFocus(surface);
    this.raise(owner);
  }

  hide(owner: BrowserWindow, token: string, generation: number): void {
    for (const surface of this.surfaces.values()) {
      if (surface.owner === owner && surface.request?.token === token && surface.request.generation === generation) this.dismiss(surface);
    }
  }

  private validate(request: ChromeOverlayRequest): void {
    if (!request || request.version !== CHROME_OVERLAY_VERSION || !["account", "capsule"].includes(request.model?.kind)
      || typeof request.token !== "string" || request.token.length > 160 || !Number.isSafeInteger(request.generation)
      || request.generation < 1 || !request.bounds || ![request.bounds.x, request.bounds.y, request.bounds.width, request.bounds.height].every(Number.isFinite)
      || request.bounds.width <= 0 || request.bounds.height <= 0 || request.bounds.width > 640
      || JSON.stringify(request).length > 512 * 1024) throw new Error("Invalid browser chrome surface.");
    if (request.model.kind === "capsule" && (typeof request.model.draft !== "string" || request.model.draft.length > 8000)) throw new Error("Invalid browser capsule draft.");
    if (request.model.kind === "account" && (!Array.isArray(request.model.panel.rows) || request.model.panel.rows.some(row => !["devices", "usage", "account", "settings", "providers", "logout"].includes(row.action)))) throw new Error("Invalid browser account actions.");
  }

  private create(owner: BrowserWindow, kind: ChromeOverlaySurface): Surface {
    const view = new WebContentsView({ webPreferences: { partition: PARTITION, preload: this.preload, sandbox: true, contextIsolation: true, nodeIntegration: false, webSecurity: true, backgroundThrottling: true } });
    const surface: Surface = { owner, view, kind, loaded: false, phase: "prepare" };
    this.surfaces.set(kind, surface);
    view.setVisible(false);
    view.setBackgroundColor("#00000000");
    owner.contentView.addChildView(view);
    const contents = view.webContents;
    contents.setWindowOpenHandler(() => ({ action: "deny" }));
    contents.on("will-navigate", event => event.preventDefault());
    contents.on("will-frame-navigate", event => event.preventDefault());
    contents.on("will-attach-webview", event => event.preventDefault());
    contents.on("render-process-gone", () => this.fail(surface));
    contents.on("did-fail-load", (_event, _code, _description, _url, mainFrame) => { if (mainFrame) this.fail(surface); });
    // Native-tree mutation in a blur callback can invalidate Chromium's focus
    // traversal. Defer dismissal, and never pull focus away from its new owner.
    contents.on("focus", () => this.preserveOpeningFocus(surface));
    contents.on("blur", () => queueMicrotask(() => {
      // Hidden views can blur during initialization, before the paint handshake
      // gives them keyboard focus. Only a presented surface can lose that focus.
      if (!surface.request || surface.phase !== "presented" || !surface.view.getVisible() || contents.isDestroyed() || contents.isFocused()) return;
      if (kind === "account") this.dismiss(surface, "outside");
    }));
    void contents.loadURL(`${ORIGIN}/chrome-overlay.html?surface=${kind}`).catch(() => this.fail(surface));
    return surface;
  }

  private trusted(event: IpcMainEvent | IpcMainInvokeEvent): Surface | undefined {
    const surface = [...this.surfaces.values()].find(value => value.view.webContents === event.sender);
    if (!surface || event.senderFrame !== event.sender.mainFrame
      || event.senderFrame.url !== `${ORIGIN}/chrome-overlay.html?surface=${surface.kind}`) return undefined;
    return surface;
  }

  ready(event: IpcMainInvokeEvent): ChromeOverlayRendererRequest | null {
    const surface = this.trusted(event);
    if (!surface) throw new Error("Untrusted browser chrome sender.");
    surface.loaded = true;
    return this.rendererRequest(surface);
  }

  prepared(event: IpcMainEvent, token: string, generation: number): void {
    const surface = this.trusted(event);
    if (!surface || surface.phase !== "prepare" || surface.request?.token !== token
      || surface.request.generation !== generation || !surface.owner.isVisible() || surface.owner.isMinimized() || this.fullscreen) return;
    // The renderer has committed the current model hidden and inert. Chromium
    // delivers a viewport only after the native view becomes visible.
    this.layout(surface);
    surface.phase = "layout";
    surface.view.setVisible(true);
    this.preserveOpeningFocus(surface);
    surface.view.webContents.send("wuu:chrome-overlay-state", this.rendererRequest(surface));
  }

  private preserveOpeningFocus(surface: Surface): void {
    if (!surface.request || surface.phase === "presented" || !surface.owner.isFocused()
      || !surface.view.webContents.isFocused()) return;
    const target = surface.focusBefore;
    if (target && !target.isDestroyed()) target.focus();
  }

  painted(event: IpcMainEvent, token: string, generation: number, viewport: { width: number; height: number }): void {
    const surface = this.trusted(event);
    if (!surface) return;
    const request = surface.request;
    if (!request || surface.phase === "prepare" || request.token !== token || request.generation !== generation || !surface.owner.isVisible() || surface.owner.isMinimized() || this.fullscreen) return;
    // A native resize/zoom can overtake the renderer's acknowledgement. Publish
    // the new viewport and keep the existing opening budget until it paints.
    if (this.layout(surface)) return;
    const target = this.rendererRequest(surface)!.bounds;
    if (!viewport || !Number.isFinite(viewport.width) || !Number.isFinite(viewport.height) || viewport.width <= 0 || viewport.height <= 0
      || Math.abs(viewport.width - target.width) > 1 || Math.abs(viewport.height - target.height) > 1) return;
    this.cancelOpening(surface);
    const opening = surface.phase !== "presented";
    surface.phase = "presented";
    surface.view.webContents.setBackgroundThrottling(true);
    // Model and viewport updates keep the existing native stacking. Newly
    // attached website views already raise chrome through prepare().
    if (opening) this.raise(surface.owner);
    if (surface.focusPending) { surface.focusPending = false; surface.view.webContents.focus(); }
  }

  command(event: IpcMainEvent, message: ChromeOverlayEvent): void {
    const surface = this.trusted(event);
    if (!surface) return;
    const request = surface.request;
    if (!request || !message || request.token !== message.token) return;
    const command = message.command;
    if (!command || typeof command.type !== "string") return;
    if (surface.phase !== "presented" || !surface.view.getVisible()) return;
    const lifecycle = command.type === "dismiss";
    if (!Number.isSafeInteger(message.generation) || message.generation < 1
      || (lifecycle ? message.generation > request.generation : message.generation !== request.generation)) return;
    if (command.type === "dismiss") {
      if (["escape", "tab"].includes(command.reason)) this.dismiss(surface, command.reason, true);
      return;
    }
    if (request.model.kind === "account") {
      if (command.type !== "account-action" || !request.model.panel.rows.some(row => row.action === command.action && !row.disabled)) return;
    } else if (request.model.kind === "capsule") {
      if (command.type === "capsule-collapse" || command.type === "capsule-draft" || command.type === "capsule-send") {
        if (typeof command.text !== "string" || command.text.length > 8000) return;
        if (command.type === "capsule-send" && request.model.busy) return;
        if (command.type === "capsule-collapse" && surface.owner.isFocused() && surface.view.webContents.isFocused() && surface.focusBefore && !surface.focusBefore.isDestroyed()) surface.focusBefore.focus();
      } else if (command.type !== "capsule-expand" && command.type !== "capsule-open" && command.type !== "capsule-menu") return;
    }
    this.emitCurrent(surface, command);
  }

  private emitCurrent(surface: Surface, command: ChromeOverlayCommand): void {
    const request = surface.request;
    if (request) this.emit(surface.owner, { token: request.token, generation: request.generation, command });
  }

  private dismiss(surface: Surface, reason?: Extract<ChromeOverlayCommand, { type: "dismiss" }>["reason"], restoreFocus = false): void {
    // Native teardown can deliver blur before the owner's JS closed callback.
    if (surface.owner.isDestroyed() || this.surfaces.get(surface.kind) !== surface) return;
    this.cancelOpening(surface);
    surface.focusPending = false;
    const focused = !surface.view.webContents.isDestroyed() && surface.view.webContents.isFocused();
    if (reason) this.emitCurrent(surface, { type: "dismiss", reason });
    surface.request = undefined;
    surface.phase = "prepare";
    surface.view.setVisible(false);
    if (!surface.view.webContents.isDestroyed()) {
      surface.view.webContents.setBackgroundThrottling(true);
      surface.view.webContents.send("wuu:chrome-overlay-state", null);
    }
    let returnTo = surface.kind === "account" ? surface.owner.webContents : surface.focusBefore;
    if (!returnTo || returnTo.isDestroyed() || (returnTo !== surface.owner.webContents
      && !surface.owner.contentView.children.some(child => child.getVisible() && (child as WebContentsView).webContents === returnTo))) returnTo = surface.owner.webContents;
    // Hiding the focused child can leave the window with no keyboard owner.
    // Never steal focus after an outside click or window deactivation.
    if (focused && (restoreFocus || reason !== "owner") && surface.owner.isFocused() && surface.owner.isVisible() && !surface.owner.isMinimized() && !returnTo.isDestroyed()) returnTo.focus();

  }

  /** Called after a new website view is attached; never restack over HTML fullscreen. */
  raise(owner: BrowserWindow): void {
    // Keyboard and accessibility activation can enter the capsule without a
    // pointer hover. Track the last actual page/shell focus throughout its life.
    const chrome = [...this.surfaces.values()].map(surface => surface.view.webContents);
    for (const contents of [owner.webContents, ...owner.contentView.children.map(child => (child as WebContentsView).webContents)]) {
      if (!contents || contents.isDestroyed() || chrome.includes(contents) || this.focusSources.has(contents)) continue;
      const focus = () => {
        const account = this.surfaces.get("account");
        if (account?.request && account.phase !== "presented" && contents !== account.focusBefore) this.dismiss(account, "outside");
        const capsule = this.surfaces.get("capsule");
        if (capsule?.owner === owner) capsule.focusBefore = contents;
      };
      const pointer = (_event: Electron.Event, mouse: Electron.MouseInputEvent) => {
        const account = this.surfaces.get("account");
        if (account?.request && (mouse.type === "mouseDown" || mouse.type === "mouseWheel")) this.dismiss(account, "outside");
      };
      this.focusSources.set(contents, () => {
        contents.removeListener("focus", focus);
        contents.removeListener("before-mouse-event", pointer);
      });
      contents.on("focus", focus);
      contents.on("before-mouse-event", pointer);
      contents.once("destroyed", () => this.focusSources.delete(contents));
      if (contents.isFocused()) focus();
    }
    const overlays = (["capsule", "account"] as const).map(kind => this.surfaces.get(kind))
      .filter((surface): surface is Surface => Boolean(surface?.owner === owner && surface.request && surface.phase === "presented" && surface.view.getVisible()));
    const children = owner.contentView.children;
    if (overlays.every((surface, index) => children[children.length - overlays.length + index] === surface.view)) return;
    // Reattaching a macOS native view can clear its sibling's keyboard focus.
    // Model/paint updates usually need no stacking change at all.
    const focused = [owner.webContents, ...children.map(child => (child as WebContentsView).webContents)]
      .find(contents => contents && !contents.isDestroyed() && contents.isFocused());
    for (const surface of overlays) owner.contentView.addChildView(surface.view);
    if (focused && owner.isFocused()) focused.focus();
  }

  close(owner: BrowserWindow): void {
    for (const surface of this.surfaces.values()) if (surface.owner === owner) {
      if (surface.kind === "capsule" && surface.phase === "presented") { this.cancelOpening(surface); surface.view.setVisible(false); }
      else this.dismiss(surface, "owner");
    }
  }

  setFullscreen(owner: BrowserWindow, fullscreen: boolean): void {
    this.fullscreen = fullscreen;
    if (fullscreen) this.close(owner); else this.restoreCapsule(owner);
  }

  private restoreCapsule(owner: BrowserWindow): void {
    const surface = this.surfaces.get("capsule");
    if (!this.fullscreen && surface?.request && surface.phase === "presented" && surface.loaded && owner.isVisible() && !owner.isMinimized()) {
      this.cancelOpening(surface);
      this.layout(surface); surface.view.setVisible(true); this.raise(owner);
    }
  }

  private rendererRequest(surface: Surface): ChromeOverlayRendererRequest | null {
    if (!surface.request) return null;
    const bounds = surface.view.getBounds(), zoom = surface.view.webContents.getZoomFactor();
    // The renderer must acknowledge the actual viewport, not the unclipped
    // requested rectangle (which may extend past a small owner window).
    return { ...surface.request, phase: surface.phase, bounds: { ...surface.request.bounds, width: bounds.width / zoom, height: bounds.height / zoom } };
  }

  private layout(surface: Surface): boolean {
    const request = surface.request;
    if (!request) return false;
    const zoom = surface.owner.webContents.getZoomFactor(), [width, height] = surface.owner.getContentSize();
    const x = Math.max(0, Math.min(width, Math.round(request.bounds.x * zoom))), y = Math.max(0, Math.min(height, Math.round(request.bounds.y * zoom)));
    const zoomChanged = surface.view.webContents.getZoomFactor() !== zoom;
    if (zoomChanged) surface.view.webContents.setZoomFactor(zoom);
    const bounds = { x, y, width: Math.max(0, Math.min(width - x, Math.ceil(request.bounds.width * zoom))), height: Math.max(0, Math.min(height - y, Math.ceil(request.bounds.height * zoom))) };
    const previous = surface.view.getBounds();
    const boundsChanged = Object.keys(bounds).some(key => previous[key as keyof typeof bounds] !== bounds[key as keyof typeof bounds]);
    if (boundsChanged) surface.view.setBounds(bounds);
    // Every native geometry writer refreshes the renderer target, including
    // persistent-capsule resize/restore paths that do not originate in show().
    if ((boundsChanged || zoomChanged) && surface.loaded) surface.view.webContents.send("wuu:chrome-overlay-state", this.rendererRequest(surface));
    return boundsChanged || zoomChanged;
  }

  private observe(owner: BrowserWindow): void {
    if (this.owner === owner) return;
    this.owner = owner;
    const close = () => this.close(owner);
    owner.webContents.on("before-input-event", (event, input) => {
      const account = this.surfaces.get("account");
      if (input.type === "keyDown" && input.key === "Escape" && account?.request && account.phase !== "presented") {
        event.preventDefault(); this.dismiss(account, "escape", true);
      }
    });
    owner.on("blur", close).on("hide", close).on("minimize", close);
    owner.on("resize", () => {
      for (const surface of this.surfaces.values()) if (surface.owner === owner) {
        // Persistent chrome must keep its native visibility and keyboard owner
        // during live resize; hiding and restoring it interrupts both.
        if (surface.kind === "capsule") {
          if (surface.view.getVisible()) this.layout(surface);
        } else this.dismiss(surface, "owner");
      }
    });
    owner.on("focus", () => this.restoreCapsule(owner)).on("show", () => this.restoreCapsule(owner)).on("restore", () => this.restoreCapsule(owner));
    owner.webContents.on("did-start-navigation", (_event, _url, inPlace, mainFrame) => { if (mainFrame && !inPlace) { close(); for (const value of this.surfaces.values()) if (value.kind === "capsule") this.dismiss(value); } });
    owner.webContents.on("render-process-gone", () => { close(); for (const value of this.surfaces.values()) if (value.kind === "capsule") this.dismiss(value); });
    owner.once("closed", () => {
      for (const surface of [...this.surfaces.values()]) this.remove(surface);
      for (const [contents, cleanup] of this.focusSources) if (!contents.isDestroyed()) cleanup();
      this.focusSources.clear();
      this.owner = undefined;
    });
  }

  private fail(surface: Surface): void {
    // A retired renderer can report more than one failure. Never revoke its
    // replacement when an old load rejection or process callback arrives late.
    if (this.surfaces.get(surface.kind) !== surface) return;
    this.dismiss(surface, "failed");
    this.remove(surface);
  }

  private remove(surface: Surface): void {
    this.cancelOpening(surface);
    // Revoke the surface before closing contents can enqueue blur/load callbacks.
    surface.request = undefined;
    this.surfaces.delete(surface.kind);
    if (!surface.owner.isDestroyed()) surface.owner.contentView.removeChildView(surface.view);
    if (!surface.view.webContents.isDestroyed()) surface.view.webContents.close();
  }

  private cancelOpening(surface: Surface): void {
    clearTimeout(surface.openingTimeout);
    surface.openingTimeout = undefined;
  }
}
