import { ipcRenderer } from "electron";
import type { RecoveryAction, RecoverySnapshot } from "../shared/desktopRecovery";

const copy = {
  en: {
    title: "Wuu Recovery", normalSummary: "The application could not open normally. This recovery window works independently of its plugins.", safeSummary: "Diagnostic mode: no application plugins or backend were started. Your normal configuration has not been changed.",
    preserve: "Saved conversations and drafts are left untouched. Unsaved input in an unresponsive window may not be recoverable. An unavailable storage provider is not replaced with an empty store.",
    retryTitle: "Reload the application window", retry: "Try reloading", cancel: "Stop waiting", idle: "Reloading uses the same configuration; it does not repair a broken plugin.", loading: "Waiting for the application page to load…", loaded: "The page loaded. Check the application window; plugin health is not yet confirmed.", failed: "The page could not load. Try diagnostic mode instead.", cancelled: "Stopped waiting. The reload may still be running; no backend work was cancelled.", timeout: "The page did not load within 30 seconds. It may still be running.", limit: "Reload limit reached. Use diagnostic mode or close the application.", unavailable: "No normal application window is running in diagnostic mode.",
    restartTitle: "Restart deliberately", safe: "Restart in diagnostic mode", normal: "Restart normally", effects: "Restart may interrupt running work. External actions may already have happened. Recovery does not replay turns or tools. Normal startup uses your existing configuration; it is not a last-known-good rollback.",
    diagnosticsTitle: "Export a minimal diagnostic report", privacy: "Only app version, platform and recovery mode are included. Logs, settings, credentials, paths and conversation content are excluded. Review the exact report before saving.", preview: "Preview report", export: "Save diagnostic report…", exported: "Diagnostic report saved.", exportCancelled: "Save cancelled.", restartCancelled: "Restart cancelled.", actionFailed: "The action could not finish. Your configuration was not changed.", restarting: "Preparing to restart…",
  },
  zh: {
    title: "Wuu 恢复中心", normalSummary: "应用未能正常打开。恢复中心独立于应用插件运行。", safeSummary: "诊断模式：未启动应用插件或后端，正常配置没有被修改。",
    preserve: "已保存的会话和草稿保持不变。无响应窗口中尚未保存的输入可能无法恢复。存储提供者不可用时，不会替换成空存储。",
    retryTitle: "重新加载应用窗口", retry: "尝试重新加载", cancel: "停止等待", idle: "重新加载仍使用原配置，不会修复损坏的插件。", loading: "正在等待应用页面加载…", loaded: "页面已加载，请检查应用窗口；这不表示插件已恢复正常。", failed: "页面未能加载，请尝试诊断模式。", cancelled: "已停止等待，重新加载可能仍在进行；没有取消后端任务。", timeout: "页面未在 30 秒内加载，操作可能仍在进行。", limit: "已达到重试上限，请使用诊断模式或退出应用。", unavailable: "诊断模式没有正在运行的普通应用窗口。",
    restartTitle: "确认后重启", safe: "以诊断模式重启", normal: "正常重启", effects: "重启可能中断正在运行的任务，外部操作可能已经发生。恢复中心不会重放会话或工具操作。正常启动使用原配置，并非回滚到上次可用状态。",
    diagnosticsTitle: "导出最小诊断报告", privacy: "仅包含应用版本、平台和恢复模式，不包含日志、配置、凭证、路径或会话内容。保存前请预览完整报告。", preview: "预览报告", export: "保存诊断报告…", exported: "诊断报告已保存。", exportCancelled: "已取消保存。", restartCancelled: "已取消重启。", actionFailed: "操作未完成，配置未修改。", restarting: "正在准备重启…",
  },
};

let current: RecoverySnapshot | undefined;
const text = (id: string, value: string) => { document.getElementById(id)!.textContent = value; };
const button = (id: string) => document.getElementById(id) as HTMLButtonElement;
function render(snapshot: RecoverySnapshot): void {
  current = snapshot;
  const c = copy[snapshot.locale];
  document.documentElement.lang = snapshot.locale === "zh" ? "zh-CN" : "en";
  document.title = c.title;
  for (const [id, value] of Object.entries({ title: c.title, summary: snapshot.mode === "diagnostic" ? c.safeSummary : c.normalSummary, preserve: c.preserve, "retry-title": c.retryTitle, retry: c.retry, cancel: c.cancel, "restart-title": c.restartTitle, safe: c.safe, normal: c.normal, effects: c.effects, "diagnostics-title": c.diagnosticsTitle, privacy: c.privacy, "preview-label": c.preview, export: c.export, preview: snapshot.diagnosticPreview })) text(id, value);
  const phase = snapshot.retry.phase;
  let status = !snapshot.retryAvailable ? c.unavailable : phase === "timed-out" ? c.timeout : c[phase];
  if (snapshot.retry.attempts >= 3 && !snapshot.retry.pending) status += ` ${c.limit}`;
  if (phase === "loading" && snapshot.retry.startedAt) status += ` ${Math.max(0, Math.floor((Date.now() - snapshot.retry.startedAt) / 1000))}s`;
  text("status", snapshot.restarting ? c.restarting : status);
  button("retry").disabled = !snapshot.retryAvailable || snapshot.retry.pending || snapshot.retry.attempts >= 3 || snapshot.restarting;
  button("cancel").disabled = phase !== "loading" || snapshot.restarting;
  button("safe").disabled = snapshot.mode === "diagnostic" || snapshot.restarting;
  button("normal").disabled = snapshot.restarting;
}
async function action(value: RecoveryAction): Promise<void> {
  try {
    const reply = await ipcRenderer.invoke("wuu:recovery-action", value) as { snapshot: RecoverySnapshot; outcome?: "exported" | "exportCancelled" | "restartCancelled" | "actionFailed" };
    render(reply.snapshot);
    if (reply.outcome) text("notice", copy[reply.snapshot.locale][reply.outcome]);
  } catch { if (current) text("notice", copy[current.locale].actionFailed); }
}
window.addEventListener("DOMContentLoaded", () => {
  for (const [id, name] of Object.entries({ retry: "retry", cancel: "cancel", safe: "restart-safe", normal: "restart-normal", export: "export" } as const)) button(id).addEventListener("click", () => { void action(name); });
  ipcRenderer.on("wuu:recovery-changed", (_event, value: RecoverySnapshot) => render(value));
  void action("snapshot");
  setInterval(() => { if (current?.retry.phase === "loading") render(current); }, 1000);
});
