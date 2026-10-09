import { globalShortcut, type BrowserWindow } from "electron";
import type {
  DesktopQuickAccessSnapshot,
  DesktopQuickAccessUpdate,
  DesktopQuickAccessUpdateResult,
} from "../shared/protocol";
import { readDesktopSettings, writeDesktopSettings } from "./desktopSettings";
import type { WindowRegistry } from "./windowRegistry";

const DEFAULT_SHORTCUT = `${process.platform === "darwin" ? "Command" : "Control"}+Shift+Space`;

function normalizeShortcut(value: string): string | null {
  if (value === "") return "";
  const parts = value.split("+");
  const key = parts.pop();
  const order = ["Command", "Control", "Alt", "Shift", "Super"];
  if (!key || !/^(?:[A-Z0-9]|F(?:[1-9]|1[0-9]|2[0-4])|Space|Up|Down|Left|Right)$/.test(key)
    || parts.length === 0 || parts.some(part => !order.includes(part))
    || new Set(parts).size !== parts.length || !parts.some(part => part !== "Shift")) return null;
  return [...order.filter(part => parts.includes(part)), key].join("+");
}

/** Owns only the user-selected quick-access binding, never other shortcuts. */
export class DesktopQuickAccess {
  private registeredShortcut = "";
  private lastConversationWindow: BrowserWindow | null = null;

  constructor(
    private readonly windows: WindowRegistry,
    private readonly createMainWindow: () => void,
    private readonly changed: (snapshot: DesktopQuickAccessSnapshot) => void,
  ) {}

  start(): void {
    const shortcut = readDesktopSettings().quick_access_shortcut ?? "";
    if (shortcut && normalizeShortcut(shortcut) && this.register(shortcut)) {
      this.registeredShortcut = shortcut;
    }
  }

  snapshot(): DesktopQuickAccessSnapshot {
    const settings = readDesktopSettings();
    const shortcut = settings.quick_access_shortcut ?? "";
    return {
      shortcut,
      defaultShortcut: DEFAULT_SHORTCUT,
      shortcutStatus: !shortcut ? "disabled"
        : this.registeredShortcut === shortcut && globalShortcut.isRegistered(shortcut) ? "registered" : "unavailable",
      popOutAlwaysOnTop: settings.pop_out_always_on_top === true,
    };
  }

  attachWindow(window: BrowserWindow): void {
    if (this.windows.roleForWindow(window.webContents.id) === "popped-out") {
      window.setAlwaysOnTop(readDesktopSettings().pop_out_always_on_top === true);
    }
    if (window.isFocused() || !this.lastConversationWindow) this.lastConversationWindow = window;
    window.on("focus", () => { this.lastConversationWindow = window; });
    window.on("closed", () => {
      if (this.lastConversationWindow === window) this.lastConversationWindow = null;
    });
  }

  update(update: DesktopQuickAccessUpdate): DesktopQuickAccessUpdateResult {
    if (!update || typeof update !== "object" || Array.isArray(update)
      || (update.shortcut !== undefined && typeof update.shortcut !== "string")
      || (update.popOutAlwaysOnTop !== undefined && typeof update.popOutAlwaysOnTop !== "boolean")) {
      throw new Error("Invalid desktop quick-access settings");
    }
    const settings = readDesktopSettings();
    const nextShortcut = update.shortcut === undefined ? settings.quick_access_shortcut ?? "" : normalizeShortcut(update.shortcut);
    if (nextShortcut === null) return { snapshot: this.snapshot(), error: "invalid_shortcut" };
    const oldShortcut = this.registeredShortcut;
    const registerNext = Boolean(update.shortcut !== undefined && nextShortcut && nextShortcut !== oldShortcut);
    // Keep the working binding until both the replacement and persistence succeed.
    if (registerNext && !this.register(nextShortcut)) {
      return { snapshot: this.snapshot(), error: "unavailable" };
    }
    try {
      writeDesktopSettings({
        ...settings,
        quick_access_shortcut: nextShortcut,
        pop_out_always_on_top: update.popOutAlwaysOnTop ?? settings.pop_out_always_on_top ?? false,
      });
    } catch (error) {
      if (registerNext) globalShortcut.unregister(nextShortcut);
      throw error;
    }
    if (update.shortcut !== undefined) {
      if (oldShortcut && oldShortcut !== nextShortcut) globalShortcut.unregister(oldShortcut);
      this.registeredShortcut = nextShortcut;
    }
    const snapshot = this.snapshot();
    for (const window of this.windows.allWindows()) {
      if (!window.isDestroyed() && this.windows.roleForWindow(window.webContents.id) === "popped-out") {
        window.setAlwaysOnTop(snapshot.popOutAlwaysOnTop);
      }
    }
    this.changed(snapshot);
    return { snapshot };
  }

  dispose(): void {
    if (this.registeredShortcut) globalShortcut.unregister(this.registeredShortcut);
    this.registeredShortcut = "";
  }

  private register(shortcut: string): boolean {
    try {
      if (globalShortcut.isRegistered(shortcut)) return false;
      return globalShortcut.register(shortcut, () => {
        let window = this.lastConversationWindow;
        if (!window || window.isDestroyed()) window = this.windows.mainWindow();
        if (!window || window.isDestroyed()) {
          this.createMainWindow();
          window = this.windows.mainWindow();
        }
        if (!window || window.isDestroyed()) return;
        if (window.isMinimized()) window.restore();
        // Do not navigate, replace the draft, or move DOM focus out of a dialog.
        window.show();
        window.focus();
      });
    } catch {
      // The OS can reject otherwise valid accelerators on this desktop session.
      return false;
    }
  }
}
