/// <reference types="electron-vite/node" />
import { app, BrowserWindow, ipcMain, nativeImage, screen, shell, type IpcMainEvent, type IpcMainInvokeEvent, type NativeImage } from "electron";
import appIcon from "../../../assets/app-icon-256.png?asset";
import { basename, dirname, join } from "node:path";
import type { SystemPermission, SystemPermissionsState } from "../shared/protocol";
import type { SystemPermissionGuideConfig } from "../shared/systemPermissionGuide";
import type { AppContextCapture } from "./appContextCapture";
import { appShellWebPreferences } from "./appShellGuards";
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
  private generation = 0;
  private pending?: ReturnType<AppContextCapture["permissionStatus"]>;
  private config?: SystemPermissionGuideConfig;
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
      if (action === "close") this.close();
      else if (action === "resize" && typeof height === "number" && Number.isFinite(height) && height > 0) {
        const bounds = guide.getBounds();
        const area = screen.getDisplayMatching(bounds).workArea;
        const nextHeight = Math.min(Math.ceil(height), area.height - 24);
        guide.setBounds({ ...bounds, height: nextHeight,
          y: Math.max(area.y + 12, Math.min(bounds.y, area.y + area.height - nextHeight - 12)) });
      } else if (action === "reveal") shell.showItemInFolder(this.appBundle);
      else if (action === "drag" && this.icon) {
        try { event.sender.startDrag({ file: this.appBundle, icon: this.icon }); }
        catch { this.guide?.webContents.send("wuu:permission-guide-error"); }
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

  async state(): Promise<SystemPermissionsState> { return (await this.read()).state; }

  async open(permission: unknown, owner: BrowserWindow, dark: boolean): Promise<void> {
    if (permission !== "screen" && permission !== "accessibility") throw new Error("Unknown system permission.");
    this.close();
    const generation = this.generation;
    const status = await this.read();
    if (generation !== this.generation || owner.isDestroyed()) return;
    if (!status.state.available) throw new Error("System permissions require the macOS capture helper.");
    if (status.state[permission]) return;
    if (!this.appBundle.endsWith(".app")) throw new Error("Open Wuu from its macOS application bundle to grant access.");
    const icon = nativeImage.createFromPath(appIcon).resize({ width: 64, height: 64 });
    this.icon = icon;
    this.config = {
      permission, appName: basename(this.appBundle, ".app"), appIcon: icon.toDataURL(),
      locale: getMainLocale(), theme: dark ? "dark" : "light", fontSize: getMessageFlowFontSize(),
    };
    const scale = this.config.fontSize / 15;
    const area = screen.getDisplayMatching(owner.getBounds()).workArea;
    const width = Math.min(Math.round(420 * scale), area.width - 24);
    const height = Math.min(Math.round(188 * scale), area.height - 24);
    const guide = new BrowserWindow({
      width, height, x: area.x + Math.round((area.width - width) / 2), y: area.y + area.height - height - 16,
      show: false, frame: false, resizable: false, minimizable: false, maximizable: false,
      fullscreenable: false, skipTaskbar: true, alwaysOnTop: true,
      backgroundColor: dark ? "#191919" : "#ffffff",
      webPreferences: {
        preload: join(__dirname, "../preload/permissionGuide.cjs"),
        contextIsolation: true, nodeIntegration: false, sandbox: true,
        ...appShellWebPreferences(app.isPackaged),
      },
    });
    this.guide = guide;
    this.owner = owner;
    owner.once("closed", this.close);
    owner.webContents.once("did-start-navigation", this.close);
    owner.webContents.once("render-process-gone", this.close);
    guide.on("closed", () => { if (this.guide === guide) this.close(); });
    guide.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    guide.webContents.on("will-navigate", event => event.preventDefault());
    guide.webContents.on("render-process-gone", this.close);
    guide.webContents.on("before-input-event", (_event, input) => { if (input.key === "Escape") this.close(); });
    try {
      if (process.env.ELECTRON_RENDERER_URL) await guide.loadURL(new URL("permission-guide.html", process.env.ELECTRON_RENDERER_URL).href);
      else await guide.loadFile(join(__dirname, "../renderer/permission-guide.html"));
      if (generation !== this.generation) return;
      // Dragging into Settings avoids an eager screen-capture request in the
      // long-lived Electron process. Authorization still belongs to macOS.
      await shell.openExternal(settingsURLs[permission]);
      if (generation !== this.generation) return;
      guide.showInactive();
      void this.poll(permission, generation);
    } catch (error) {
      if (generation === this.generation) this.close();
      throw error;
    }
  }

  private async poll(permission: SystemPermission, generation: number): Promise<void> {
    try {
      const result = await this.read();
      if (generation !== this.generation || !this.guide) return;
      if (result.state[permission]) {
        const owner = this.owner;
        this.close();
        if (owner && !owner.isDestroyed()) { owner.show(); owner.focus(); }
        return;
      }
      if (result.settingsWindow) {
        const bounds = result.settingsWindow;
        const area = screen.getDisplayMatching(bounds).workArea;
        const [width, height] = this.guide.getSize();
        // Prefer below Settings so the grant controls and authentication
        // sheet remain usable. Small displays fall back inside its lower edge.
        const x = Math.max(area.x + 12, Math.min(bounds.x + bounds.width - width, area.x + area.width - width - 12));
        const y = Math.max(area.y + 12, Math.min(bounds.y + bounds.height + 8, area.y + area.height - height - 12));
        this.guide.setPosition(Math.round(x), Math.round(y), false);
      }
      if (result.settingsVisible) this.guide.showInactive();
      else this.guide.hide();
    } catch {
      if (generation !== this.generation) return;
      this.guide?.webContents.send("wuu:permission-guide-error");
    }
    if (generation === this.generation) this.timer = setTimeout(() => void this.poll(permission, generation), 1_000);
  }

  close = (): void => {
    this.generation += 1;
    if (this.timer) clearTimeout(this.timer);
    this.timer = undefined;
    if (this.owner && !this.owner.isDestroyed()) {
      this.owner.removeListener("closed", this.close);
      this.owner.webContents.removeListener("did-start-navigation", this.close);
      this.owner.webContents.removeListener("render-process-gone", this.close);
    }
    this.owner = undefined;
    const guide = this.guide;
    this.guide = undefined;
    this.config = undefined;
    this.icon = undefined;
    if (guide && !guide.isDestroyed()) guide.destroy();
  };
}
