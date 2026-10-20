import { BrowserWindow, type Session } from "electron";
import type { BrowserExtensionManager } from "./browserExtensions";

/** Extension-owned settings share browser storage, but never receive the Wuu bridge. */
export class BrowserExtensionWindows {
  private readonly windows = new Map<string, BrowserWindow>();

  constructor(private readonly session: Session, private readonly manager: BrowserExtensionManager) {
    session.extensions.on("extension-unloaded", (_event, extension) => {
      this.windows.get(extension.id)?.destroy();
    });
  }

  async openOptions(id: string, parent: BrowserWindow): Promise<BrowserWindow> {
    const url = await this.manager.optionsURL(id);
    const extensionId = new URL(url).hostname;
    if (parent.isDestroyed()) throw new Error("The owning Wuu window is closed.");
    const existing = this.windows.get(extensionId);
    if (existing && !existing.isDestroyed()) {
      if (existing.isMinimized()) existing.restore();
      existing.show();
      existing.focus();
      return existing;
    }
    const window = new BrowserWindow({
      parent, width: 800, height: 640, minWidth: 420, minHeight: 320, show: false,
      autoHideMenuBar: true,
      webPreferences: {
        session: this.session, sandbox: true, contextIsolation: true,
        nodeIntegration: false, webviewTag: false,
      },
    });
    this.windows.set(extensionId, window);
    const close = () => { if (!window.isDestroyed()) window.destroy(); };
    parent.once("closed", close);
    window.once("closed", () => {
      this.windows.delete(extensionId);
      parent.removeListener("closed", close);
    });
    const allowNavigation = (target: string): boolean => {
      try {
        const parsed = new URL(target);
        return parsed.protocol === "chrome-extension:" && parsed.hostname === extensionId;
      } catch { return false; }
    };
    window.webContents.on("will-navigate", (event, target) => {
      if (!allowNavigation(target)) event.preventDefault();
    });
    window.webContents.on("will-redirect", (event, target) => {
      if (!allowNavigation(target)) event.preventDefault();
    });
    window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    try {
      await window.loadURL(url);
      if (window.isDestroyed()) throw new Error("The extension settings window closed while loading.");
      window.show();
      return window;
    } catch (error) {
      close();
      throw error;
    }
  }
}
