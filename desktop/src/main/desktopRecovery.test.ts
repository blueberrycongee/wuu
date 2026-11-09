import { EventEmitter } from "node:events";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocked = vi.hoisted(() => ({ handlers: new Map<string, (...args: any[]) => any>(), windows: [] as any[], save: vi.fn(), confirm: vi.fn(), relaunch: vi.fn(), quit: vi.fn(), write: vi.fn(), events: new Map<string, () => void>() }));
vi.mock("electron", () => ({
  app: { getLocale: () => "en", getVersion: () => "2026.10.19", once: (name: string, handler: () => void) => mocked.events.set(name, handler), relaunch: mocked.relaunch, quit: mocked.quit },
  ipcMain: { handle: (name: string, handler: (...args: any[]) => any) => mocked.handlers.set(name, handler), removeHandler: (name: string) => mocked.handlers.delete(name) },
  dialog: { showSaveDialog: mocked.save, showMessageBox: mocked.confirm },
  BrowserWindow: class extends EventEmitter {
    options: unknown; dead = false;
    webContents = Object.assign(new EventEmitter(), { mainFrame: {}, send: vi.fn(), isDestroyed: () => false, setWindowOpenHandler: vi.fn(), session: { setPermissionRequestHandler: vi.fn(), setPermissionCheckHandler: vi.fn(), webRequest: { onBeforeRequest: vi.fn() } } });
    loadURL = vi.fn(async () => {}); show = vi.fn(); focus = vi.fn(); isDestroyed = () => this.dead;
    constructor(options: unknown) { super(); this.options = options; mocked.windows.push(this); }
  },
}));
vi.mock("node:fs/promises", () => ({ writeFile: mocked.write }));
import { createDesktopRecovery } from "./desktopRecovery";

beforeEach(() => { vi.useFakeTimers(); vi.clearAllMocks(); mocked.windows.length = 0; mocked.handlers.clear(); mocked.events.clear(); });
afterEach(() => { mocked.events.get("will-quit")?.(); vi.useRealTimers(); });
const invoke = (action: unknown, event?: unknown) => {
  const window = mocked.windows[0];
  return mocked.handlers.get("wuu:recovery-action")!(event ?? { sender: window.webContents, senderFrame: window.webContents.mainFrame }, action);
};

describe("independent recovery window", () => {
  it("starts with no backend or config dependency, locks down the renderer, and deduplicates open", async () => {
    const recovery = createDesktopRecovery({ preloadPath: "/bundle/recovery.cjs", mode: "diagnostic", shutdown: vi.fn() });
    await Promise.all([recovery.open(), recovery.open()]);
    expect(mocked.windows).toHaveLength(1);
    expect(mocked.windows[0].options.webPreferences).toMatchObject({ sandbox: true, contextIsolation: true, nodeIntegration: false, webviewTag: false });
    expect((await invoke("snapshot")).snapshot).toMatchObject({ mode: "diagnostic", retryAvailable: false });
    await invoke("retry");
    expect((await invoke("snapshot")).snapshot.retry.attempts).toBe(0);
    await expect(invoke("snapshot", { sender: {}, senderFrame: {} })).rejects.toThrow("Recovery window required");
    await expect(invoke("snapshot", { sender: mocked.windows[0].webContents, senderFrame: {} })).rejects.toThrow("Recovery window required");
    await expect(invoke("arbitrary-rpc")).rejects.toThrow("Unsupported recovery action");
  });

  it("exports exactly the preview with exclusive-create semantics and no supplied filesystem path", async () => {
    const recovery = createDesktopRecovery({ preloadPath: "/bundle/recovery.cjs", mode: "diagnostic", shutdown: vi.fn() });
    await recovery.open();
    const preview = (await invoke("snapshot")).snapshot.diagnosticPreview;
    mocked.save.mockResolvedValue({ canceled: false, filePath: "/chosen/report.json" });
    expect((await invoke("export")).outcome).toBe("exported");
    expect(mocked.write).toHaveBeenCalledWith("/chosen/report.json", preview, { encoding: "utf8", mode: 0o600, flag: "wx" });
    mocked.write.mockRejectedValueOnce(new Error("private-path token=secret"));
    expect((await invoke("export")).outcome).toBe("actionFailed");
  });

  it("does not restart, shut down, or change launch mode without explicit confirmation", async () => {
    const shutdown = vi.fn();
    const recovery = createDesktopRecovery({ preloadPath: "/bundle/recovery.cjs", mode: "normal", shutdown });
    await recovery.open();
    mocked.confirm.mockResolvedValue({ response: 0 });
    expect((await invoke("restart-safe")).outcome).toBe("restartCancelled");
    expect(shutdown).not.toHaveBeenCalled();
    expect(mocked.relaunch).not.toHaveBeenCalled();
    expect(mocked.quit).not.toHaveBeenCalled();
  });

  it("does not relaunch when shutdown fails, and does not leak its raw failure", async () => {
    const recovery = createDesktopRecovery({ preloadPath: "/bundle/recovery.cjs", mode: "normal", shutdown: async () => { throw new Error("secret token"); } });
    await recovery.open();
    mocked.confirm.mockResolvedValue({ response: 1 });
    const result = await invoke("restart-safe");
    expect(result.outcome).toBe("actionFailed");
    expect(JSON.stringify(result)).not.toContain("secret token");
    expect(mocked.relaunch).not.toHaveBeenCalled();
  });
});

it("restores the current process launch mode if native relaunch fails", async () => {
  const previous = process.env.WUU_SAFE_MODE;
  process.env.WUU_SAFE_MODE = "0";
  try {
    const shutdown = vi.fn(async () => {});
    const recovery = createDesktopRecovery({ preloadPath: "/bundle/recovery.cjs", mode: "normal", shutdown });
    await recovery.open();
    mocked.confirm.mockResolvedValue({ response: 1 });
    mocked.relaunch.mockImplementationOnce(() => { throw new Error("native relaunch failed"); });
    expect((await invoke("restart-safe")).outcome).toBe("actionFailed");
    expect(shutdown).toHaveBeenCalledOnce();
    expect(process.env.WUU_SAFE_MODE).toBe("0");
    expect(mocked.quit).not.toHaveBeenCalled();
  } finally { if (previous === undefined) delete process.env.WUU_SAFE_MODE; else process.env.WUU_SAFE_MODE = previous; }
});
