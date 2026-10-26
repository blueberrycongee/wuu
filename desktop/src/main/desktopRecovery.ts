import { app, BrowserWindow, dialog, ipcMain, type IpcMainInvokeEvent, type MenuItemConstructorOptions } from "electron";
import { writeFile } from "node:fs/promises";
import type { RecoveryMode, RecoverySnapshot } from "../shared/desktopRecovery";
import { recoveryPage } from "./recoveryPage";
import { RecoveryRetry, recoveryDiagnostics, recoveryLaunchArguments } from "./desktopRecoveryState";

type RecoveryOptions = {
  preloadPath: string;
  mode: RecoveryMode;
  reload?: () => Promise<void>;
  shutdown: () => Promise<void>;
};

export function createDesktopRecovery(options: RecoveryOptions) {
  let window: BrowserWindow | undefined;
  let opening: Promise<void> | undefined;
  let exporting = false;
  let restarting = false;
  let ready = false;
  let fallbackShown = false;
  const locale = (): "zh" | "en" => app.getLocale().startsWith("zh") ? "zh" : "en";
  const label = () => locale() === "zh" ? "打开恢复中心…" : "Open Recovery Center…";
  const diagnosticPreview = () => JSON.stringify(recoveryDiagnostics({ version: app.getVersion(), platform: process.platform, mode: options.mode }), null, 2) + "\n";
  const snapshot = (): RecoverySnapshot => ({ mode: options.mode, locale: locale(), retryAvailable: Boolean(options.reload), retry: retry.snapshot(), restarting, diagnosticPreview: diagnosticPreview() });
  const changed = () => {
    if (window && !window.isDestroyed() && !window.webContents.isDestroyed()) window.webContents.send("wuu:recovery-changed", snapshot());
  };
  const retry = new RecoveryRetry(() => options.reload!(), changed);

  async function exportReport(): Promise<"exported" | "exportCancelled" | "actionFailed"> {
    if (exporting) return "exportCancelled";
    exporting = true;
    try {
      const choice = await dialog.showSaveDialog({ title: locale() === "zh" ? "保存最小诊断报告" : "Save minimal diagnostic report", defaultPath: "wuu-recovery.json", filters: [{ name: "JSON", extensions: ["json"] }] });
      if (choice.canceled || !choice.filePath) return "exportCancelled";
      // Never overwrite an existing file, including config or business data.
      await writeFile(choice.filePath, diagnosticPreview(), { encoding: "utf8", mode: 0o600, flag: "wx" });
      return "exported";
    } catch { return "actionFailed"; }
    finally { exporting = false; }
  }

  async function nativeFallback(): Promise<void> {
    if (fallbackShown) return;
    fallbackShown = true;
    const zh = locale() === "zh";
    const { response } = await dialog.showMessageBox({ type: "warning", title: "Wuu", message: zh ? "恢复页面未能打开" : "The recovery page could not open", detail: (zh ? "以下是将导出的完整报告，不会修改配置或会话。" : "The complete report below can be exported. Configuration and conversations remain unchanged.") + "\n\n" + diagnosticPreview(), buttons: [zh ? "导出诊断" : "Export diagnostics", zh ? "关闭" : "Close"], defaultId: 1, cancelId: 1, noLink: true });
    if (response === 0) await exportReport();
  }

  async function restart(mode: RecoveryMode): Promise<"restartCancelled" | "actionFailed" | undefined> {
    if (restarting) return;
    restarting = true; changed();
    const zh = locale() === "zh";
    const previousSafeMode = process.env.WUU_SAFE_MODE;
    let changedLaunchMode = false;
    try {
      const { response } = await dialog.showMessageBox({ type: "warning", title: "Wuu", message: mode === "diagnostic" ? (zh ? "以诊断模式重启？" : "Restart in diagnostic mode?") : (zh ? "正常重启？" : "Restart normally?"), detail: zh ? "这可能中断正在运行的任务；外部操作的结果可能仍未知。已保存的数据和配置保持不变，不会由恢复中心重新提交任务。" : "This may interrupt running work; results of external actions may remain unknown. Saved data and configuration are preserved. Recovery will not resubmit tasks.", buttons: [zh ? "取消" : "Cancel", zh ? "重启" : "Restart"], defaultId: 0, cancelId: 0, noLink: true });
      if (response !== 1) return "restartCancelled";
      await options.shutdown();
      // Explicit launch overlay only. No user config or plugin policy is edited.
      changedLaunchMode = true;
      if (mode === "diagnostic") process.env.WUU_SAFE_MODE = "1";
      else delete process.env.WUU_SAFE_MODE;
      app.relaunch({ args: recoveryLaunchArguments(process.argv.slice(1), mode) });
      app.quit();
      return;
    } catch {
      if (changedLaunchMode) {
        if (previousSafeMode === undefined) delete process.env.WUU_SAFE_MODE;
        else process.env.WUU_SAFE_MODE = previousSafeMode;
      }
      return "actionFailed";
    }
    finally { restarting = false; changed(); }
  }

  function authorize(event: IpcMainInvokeEvent): void {
    if (!window || window.isDestroyed() || event.sender !== window.webContents || event.senderFrame !== window.webContents.mainFrame) throw new Error("Recovery window required");
  }
  ipcMain.handle("wuu:recovery-action", async (event, action: unknown) => {
    authorize(event);
    let outcome: "exported" | "exportCancelled" | "restartCancelled" | "actionFailed" | undefined;
    switch (action) {
      case "snapshot": ready = true; break;
      case "retry": if (options.reload && !restarting) retry.start(); break;
      case "cancel": retry.cancel(); break;
      case "export": outcome = await exportReport(); break;
      case "restart-safe": outcome = await restart("diagnostic"); break;
      case "restart-normal": outcome = await restart("normal"); break;
      default: throw new Error("Unsupported recovery action");
    }
    return { snapshot: snapshot(), ...(outcome ? { outcome } : {}) };
  });

  function open(): Promise<void> {
    if (opening) return opening;
    if (window && !window.isDestroyed()) { window.show(); window.focus(); return Promise.resolve(); }
    ready = false;
    fallbackShown = false;
    window = new BrowserWindow({ title: "Wuu Recovery", width: 760, height: 820, minWidth: 360, minHeight: 480, show: true, webPreferences: { preload: options.preloadPath, sandbox: true, contextIsolation: true, nodeIntegration: false, webviewTag: false, partition: "wuu-recovery", devTools: false } });
    const target = window;
    target.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    target.webContents.on("will-navigate", event => event.preventDefault());
    target.webContents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
    target.webContents.session.setPermissionCheckHandler(() => false);
    target.webContents.session.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*", "file://*/*"] }, (_details, callback) => callback({ cancel: true }));
    target.webContents.on("render-process-gone", () => { void nativeFallback(); });
    const timer = setTimeout(() => { if (!ready && !target.isDestroyed()) void nativeFallback(); }, 5_000);
    target.once("closed", () => { clearTimeout(timer); if (window === target) window = undefined; });
    opening = target.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(recoveryPage)}`).catch(() => nativeFallback()).finally(() => { opening = undefined; });
    return opening;
  }
  app.once("will-quit", () => { retry.dispose(); ipcMain.removeHandler("wuu:recovery-action"); });
  return { open, snapshot, menuItem: (): MenuItemConstructorOptions => ({ id: "wuu-recovery", label: label(), click: () => { void open(); } }) };
}
