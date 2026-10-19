/// <reference types="electron-vite/node" />
import { app, BrowserWindow, ipcMain, nativeImage, screen, shell, systemPreferences, type IpcMainEvent, type IpcMainInvokeEvent, type NativeImage, type Rectangle } from "electron";
import appIcon from "../../../assets/app-icon-256.png?asset";
import { basename, dirname, join } from "node:path";
import type { SystemPermission, SystemPermissionsState, SystemPermissionPresentation, SystemPermissionGuideState } from "../shared/protocol";
import type { SystemPermissionGuideConfig } from "../shared/systemPermissionGuide";
import type { AppContextCapture } from "./appContextCapture";
import { appShellWebPreferences } from "./appShellGuards";
import { browserPiPScreenRect } from "./browserPiPPlacement";
import { getMessageFlowFontSize } from "./desktopSettings";
import { getMainLocale } from "./i18n";

const settingsURLs: Record<SystemPermission, string> = {
  screen: "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture",
  accessibility: "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
};

export class SystemPermissions {
  private guide?: BrowserWindow;
  private owner?: BrowserWindow;
  private timer?: ReturnType<typeof setTimeout>;
  private followTimer?: ReturnType<typeof setTimeout>;
  private generation = 0;
  private dark = false;
  private pending?: ReturnType<AppContextCapture["permissionStatus"]>;
  private config?: SystemPermissionGuideConfig;
  private presentation?: SystemPermissionPresentation;
  private settingsBounds?: Rectangle;
  private compactHeight = 0;
  private painted?: () => void;
  private finishMotion?: (complete: boolean) => void;
  private icon?: NativeImage;
  // The dragged bundle is chosen by the host, never by a renderer or web page.
  private readonly appBundle = dirname(dirname(dirname(process.execPath)));

  constructor(private readonly capture: AppContextCapture) {}

  start(): void {
    ipcMain.handle("wuu:permission-guide-config", event => {
      if (!this.isGuide(event)) throw new Error("Permission guide is not available from this window.");
      return this.config;
    });
    ipcMain.on("wuu:permission-guide-action", (event, action: unknown, height: unknown) => {
      const guide = this.guide;
      if (!guide || !this.isGuide(event)) return;
      if (action === "ready") this.painted?.();
      else if (action === "close") void this.returnToOwner(false);
      else if (action === "resize" && this.config?.phase !== "returning" && typeof height === "number" && Number.isFinite(height) && height > 0) {
        this.compactHeight = height * guide.webContents.getZoomFactor();
        // Measure the compact content during the morph, but let motion own bounds.
        if (this.config?.phase === "guiding" && this.settingsBounds) this.applyBounds(this.destination(this.settingsBounds));
      } else if (action === "reveal") shell.showItemInFolder(this.appBundle);
      else if (action === "drag" && this.icon && this.config?.phase === "guiding") {
        try { event.sender.startDrag({ file: this.appBundle, icon: this.icon }); }
        catch { guide.webContents.send("wuu:permission-guide-error"); }
      }
    });
  }

  private isGuide(event: IpcMainEvent | IpcMainInvokeEvent): boolean {
    return Boolean(this.guide && !this.guide.isDestroyed() && event.sender === this.guide.webContents && event.senderFrame === event.sender.mainFrame);
  }

  private read(): ReturnType<AppContextCapture["permissionStatus"]> {
    if (!this.pending) {
      this.pending = this.capture.permissionStatus().finally(() => { this.pending = undefined; });
    }
    return this.pending;
  }

  async state(): Promise<SystemPermissionsState> { return this.read(); }

  private parsePresentation(value: unknown): SystemPermissionPresentation | undefined {
    if (!value || typeof value !== "object") return;
    const input = value as SystemPermissionPresentation;
    if (typeof input.requestId !== "string" || !input.requestId || input.requestId.length > 128 || typeof input.reducedMotion !== "boolean") return;
    if (input.theme !== "light" && input.theme !== "dark") return;
    const rect = input.sourceRect;
    const valid = rect && [rect.x, rect.y, rect.width, rect.height].every(n => typeof n === "number" && Number.isFinite(n)) && rect.width > 0 && rect.height > 0;
    return { requestId: input.requestId, reducedMotion: input.reducedMotion, theme: input.theme, sourceRect: valid ? { ...rect } : null };
  }

  update(presentation: unknown): void {
    const next = this.parsePresentation(presentation);
    if (!next || next.requestId !== this.presentation?.requestId) return;
    this.presentation = next;
    if (this.config) {
      if (this.config.phase === "source") {
        const source = this.sourceBounds();
        if (source) this.applyBounds(source);
        else this.guide?.hide();
      }
      this.config.reducedMotion = next.reducedMotion;
      if (this.config.theme !== next.theme) {
        this.config.theme = next.theme;
        this.guide?.setBackgroundColor(next.theme === "dark" ? "#191919" : "#ffffff");
      }
      this.sendConfig();
      if (next.reducedMotion) this.finishMotion?.(true);
    }
  }

  cancel(requestId: unknown): void {
    if (requestId === undefined || requestId === this.presentation?.requestId) this.close();
  }

  refreshAppearance(dark: boolean): void {
    this.dark = dark;
    if (!this.config) return;
    const theme = this.presentation?.theme ?? (dark ? "dark" : "light");
    this.config = { ...this.config, theme, locale: getMainLocale(), fontSize: getMessageFlowFontSize() };
    this.guide?.setBackgroundColor(theme === "dark" ? "#191919" : "#ffffff");
    this.sendConfig();
  }

  private sourceBounds(): Rectangle | undefined {
    const rect = this.presentation?.sourceRect;
    const owner = this.owner;
    if (!rect || !owner || owner.isDestroyed()) return;
    const content = owner.getContentBounds();
    const zoom = owner.webContents.getZoomFactor();
    if (rect.x < 0 || rect.y < 0 || (rect.x + rect.width) * zoom > content.width + 1 || (rect.y + rect.height) * zoom > content.height + 1) return;
    return browserPiPScreenRect(rect, content, zoom);
  }

  private sendConfig(): void {
    this.guide?.webContents.send("wuu:permission-guide-config-changed", this.config);
  }

  private notify(phase: SystemPermissionGuideState["phase"], granted = this.config?.granted ?? false): void {
    if (this.presentation && this.owner && !this.owner.isDestroyed() && !this.owner.webContents.isDestroyed()) {
      this.owner.webContents.send("wuu:system-permission-guide-changed", { requestId: this.presentation.requestId, phase, granted } satisfies SystemPermissionGuideState);
    }
  }

  private ownerNavigated = (details: Electron.Event<Electron.WebContentsDidStartNavigationEventParams>): void => {
    if (details.isMainFrame && !details.isSameDocument) this.close();
  };

  async open(permission: unknown, owner: BrowserWindow, dark: boolean, presentation?: unknown): Promise<void> {
    if (permission !== "screen" && permission !== "accessibility") throw new Error("Unknown system permission.");
    this.close();
    const generation = this.generation;
    this.dark = dark;
    this.owner = owner;
    this.presentation = this.parsePresentation(presentation);
    // Invalidate even while the initial native permission query is in flight.
    owner.once("closed", this.close);
    owner.webContents.on("did-start-navigation", this.ownerNavigated);
    owner.webContents.once("render-process-gone", this.close);
    owner.webContents.once("destroyed", this.close);
    try {
      const status = await this.read();
      if (generation !== this.generation || owner.isDestroyed()) return;
      if (!status.available) throw new Error("System permissions require the macOS capture helper.");
      if (status[permission]) { this.notify("closed", true); this.presentation = undefined; this.close(); return; }
      if (!this.appBundle.endsWith(".app")) throw new Error("Open Wuu from its macOS application bundle to grant access.");
      const appImage = nativeImage.createFromPath(appIcon);
      this.icon = appImage.resize({ width: 64, height: 64 });
      const source = this.sourceBounds();
      this.config = {
        permission, appName: basename(this.appBundle, ".app"), appIcon: appImage.toDataURL(),
        locale: getMainLocale(), theme: this.presentation?.theme ?? (this.dark ? "dark" : "light"), fontSize: getMessageFlowFontSize(),
        reducedMotion: this.presentation?.reducedMotion ?? systemPreferences.getAnimationSettings().prefersReducedMotion,
        phase: source ? "source" : "guiding", granted: false,
      };
      this.compactHeight = 108 * this.config.fontSize / 15 * owner.webContents.getZoomFactor();
      this.settingsBounds = this.capture.permissionGuideWindow() ?? undefined;
      const bounds = source ?? this.destination(this.settingsBounds ?? owner.getBounds());
      // Keep this unparented: a native child can raise its owner over Settings.
      const guide = new BrowserWindow({
        x: Math.round(bounds.x), y: Math.round(bounds.y), width: Math.round(bounds.width), height: Math.round(bounds.height),
        show: false, frame: false, resizable: false, minimizable: false, maximizable: false,
        fullscreenable: false, skipTaskbar: true, alwaysOnTop: true,
        backgroundColor: this.config.theme === "dark" ? "#191919" : "#ffffff",
        webPreferences: {
          preload: join(__dirname, "../preload/permissionGuide.cjs"),
          contextIsolation: true, nodeIntegration: false, sandbox: true,
          zoomFactor: owner.webContents.getZoomFactor(),
          ...appShellWebPreferences(app.isPackaged),
        },
      });
      this.guide = guide;
      guide.on("closed", () => { if (this.guide === guide) this.close(); });
      guide.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
      guide.webContents.on("will-navigate", event => event.preventDefault());
      guide.webContents.on("render-process-gone", this.close);
      guide.webContents.on("before-input-event", (_event, input) => { if (input.key === "Escape") void this.returnToOwner(false); });
      let paintTimeout: ReturnType<typeof setTimeout>;
      let markPainted: () => void;
      const painted = new Promise<void>((resolve, reject) => {
        markPainted = resolve;
        this.painted = resolve;
        paintTimeout = setTimeout(() => reject(new Error("Permission guide did not become ready.")), 5_000);
      });
      try {
        await Promise.all([painted, process.env.ELECTRON_RENDERER_URL
          ? guide.loadURL(new URL("permission-guide.html", process.env.ELECTRON_RENDERER_URL).href)
          : guide.loadFile(join(__dirname, "../renderer/permission-guide.html"))]);
      } finally {
        clearTimeout(paintTimeout!);
        if (this.painted === markPainted!) this.painted = undefined;
      }
      if (generation !== this.generation) return;
      const currentSource = this.sourceBounds();
      if (source && currentSource) {
        this.applyBounds(currentSource);
        guide.showInactive();
        this.notify("lifting");
      } else if (this.config.phase === "source") {
        this.config.phase = "guiding";
        this.sendConfig();
      }
      // Dragging into Settings avoids an eager screen-capture request in the
      // long-lived Electron process. Authorization still belongs to macOS.
      await shell.openExternal(settingsURLs[permission]);
      if (generation !== this.generation) return;
      void this.follow(generation);
      void this.poll(permission, generation);
    } catch (error) {
      if (generation !== this.generation) return;
      this.close();
      throw error;
    }
  }

  private destination(bounds: Rectangle): Rectangle {
    const area = screen.getDisplayMatching(bounds).workArea;
    const scale = (this.config?.fontSize ?? 15) / 15 * (this.guide?.webContents.getZoomFactor() ?? this.owner?.webContents.getZoomFactor() ?? 1);
    const width = Math.min(Math.round(Math.min(530 * scale, bounds.width * 0.74)), area.width - 24);
    const height = Math.min(Math.ceil(this.compactHeight), area.height - 24);
    return { width, height,
      x: Math.round(Math.max(area.x + 12, Math.min(bounds.x + bounds.width - width - 12, area.x + area.width - width - 12))),
      y: Math.round(Math.max(area.y + 12, Math.min(bounds.y + bounds.height - height - 12, area.y + area.height - height - 12))),
    };
  }

  private applyBounds(bounds: Rectangle): void {
    if (!this.guide) return;
    const next = { x: Math.round(bounds.x), y: Math.round(bounds.y), width: Math.round(bounds.width), height: Math.round(bounds.height) };
    const current = this.guide.getBounds();
    if (current.x !== next.x || current.y !== next.y || current.width !== next.width || current.height !== next.height) this.guide.setBounds(next, false);
  }

  private animateTo(destination: Rectangle | (() => Rectangle | undefined), generation: number): Promise<boolean> {
    this.finishMotion?.(false);
    const start = this.guide!.getBounds();
    return new Promise(resolve => {
      const began = performance.now();
      let frame: ReturnType<typeof setTimeout>;
      const finish = (complete: boolean): void => {
        clearTimeout(frame);
        this.finishMotion = undefined;
        const target = typeof destination === "function" ? destination() : destination;
        if (complete && generation === this.generation && target) this.applyBounds(target);
        resolve(complete && generation === this.generation);
      };
      this.finishMotion = finish;
      const tick = (): void => {
        if (generation !== this.generation) { finish(false); return; }
        const target = typeof destination === "function" ? destination() : destination;
        if (!target) { finish(true); return; }
        const progress = this.config?.reducedMotion ? 1 : Math.min(1, (performance.now() - began) / 420);
        if (progress === 1) { finish(true); return; }
        const eased = 1 - (1 - progress) ** 3;
        this.applyBounds({ x: start.x + (target.x - start.x) * eased, y: start.y + (target.y - start.y) * eased,
          width: start.width + (target.width - start.width) * eased, height: start.height + (target.height - start.height) * eased });
        frame = setTimeout(tick, 16);
      };
      tick();
    });
  }

  private async returnToOwner(granted: boolean): Promise<void> {
    if (!this.config || this.config.phase === "returning") return;
    const generation = this.generation;
    clearTimeout(this.timer);
    clearTimeout(this.followTimer);
    this.finishMotion?.(false);
    this.config = { ...this.config, phase: "returning", granted };
    this.sendConfig();
    this.notify("returning");
    const owner = this.owner;
    if (owner && !owner.isDestroyed()) { owner.show(); owner.focus(); }
    const source = this.sourceBounds();
    if (source && this.guide?.isVisible()) {
      if (!await this.animateTo(() => this.sourceBounds(), generation)) return;
    }
    if (generation === this.generation) this.close();
  }

  private async follow(generation: number): Promise<void> {
    if (generation !== this.generation || !this.guide || !this.config || this.config.phase === "returning") return;
    // Geometry uses public window metadata in-process, independently of the
    // slower fresh-process permission check. Keep tracking during the lift too.
    this.followTimer = setTimeout(() => void this.follow(generation), 16);
    try {
      this.settingsBounds = this.capture.permissionGuideWindow() ?? undefined;
      if (!this.settingsBounds) { this.guide.hide(); return; }
      const source = this.sourceBounds();
      if (this.config.phase === "source" && source) {
        this.applyBounds(source);
        this.guide.showInactive();
        this.config.phase = "lifting";
        this.sendConfig();
        if (!await this.animateTo(() => this.settingsBounds ? this.destination(this.settingsBounds) : undefined, generation)
          || generation !== this.generation || this.config?.phase !== "lifting") return;
        this.config.phase = "guiding";
        this.sendConfig();
        this.notify("guiding");
      } else if (this.config.phase !== "lifting") {
        const entering = this.config.phase !== "guiding" || !this.guide.isVisible();
        if (this.config.phase === "source") {
          this.config.phase = "guiding";
          this.sendConfig();
        }
        this.applyBounds(this.destination(this.settingsBounds));
        if (entering) { this.guide.showInactive(); this.notify("guiding"); }
      }
    } catch {
      if (generation !== this.generation) return;
      // Unknown native window state must not leave a stale always-on-top guide.
      this.settingsBounds = undefined;
      if (this.guide?.isVisible()) {
        this.guide.hide();
        this.guide.webContents.send("wuu:permission-guide-error");
      }
    }
  }

  private async poll(permission: SystemPermission, generation: number): Promise<void> {
    try {
      const state = await this.read();
      if (generation !== this.generation || !this.guide || this.config?.phase === "returning") return;
      if (state[permission]) { await this.returnToOwner(true); return; }
    } catch {
      if (generation !== this.generation) return;
      this.guide?.webContents.send("wuu:permission-guide-error");
    }
    if (generation === this.generation && this.config?.phase !== "returning") this.timer = setTimeout(() => void this.poll(permission, generation), 500);
  }

  close = (): void => {
    this.generation += 1;
    clearTimeout(this.timer);
    this.timer = undefined;
    clearTimeout(this.followTimer);
    this.followTimer = undefined;
    this.finishMotion?.(false);
    this.painted?.();
    this.notify("closed");
    if (this.owner && !this.owner.isDestroyed()) {
      this.owner.removeListener("closed", this.close);
      this.owner.webContents.removeListener("did-start-navigation", this.ownerNavigated);
      this.owner.webContents.removeListener("render-process-gone", this.close);
      this.owner.webContents.removeListener("destroyed", this.close);
    }
    this.owner = undefined;
    this.presentation = undefined;
    const guide = this.guide;
    this.guide = undefined;
    this.config = undefined;
    this.settingsBounds = undefined;
    this.icon = undefined;
    if (guide && !guide.isDestroyed()) guide.destroy();
  };
}
