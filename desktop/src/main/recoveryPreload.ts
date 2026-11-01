import { ipcRenderer } from "electron";
import type { RecoveryAction, RecoverySnapshot } from "../shared/desktopRecovery";

const copy = {
  en: {
    title: "Wuu Recovery", heading: "The application could not open", safeHeading: "Diagnostic mode is active", safeSummary: "Application plugins are not running. Your configuration is unchanged.",
    preserve: "Saved conversations and drafts are unchanged. Unsaved input may not be recoverable.",
    retryTitle: "Reload the application window", retry: "Try reloading", cancel: "Stop waiting", idle: "Reloading uses the same configuration; it does not repair a broken plugin.", loading: "Waiting for the application page to load…", loaded: "The page loaded. Check the application window; plugin health is not yet confirmed.", failed: "The page could not load.", cancelled: "Stopped waiting. The reload may still be running; no backend work was cancelled.", timeout: "The page did not load within 30 seconds. It may still be running.", limit: "Reload limit reached. Use diagnostic mode or close the application.", unavailable: "No normal application window is running in diagnostic mode.",
    restartTitle: "Restart the application", restartHelp: "Diagnostic mode opens only Recovery, without plugins. Normal restart uses your existing configuration.", safe: "Restart in diagnostic mode", normal: "Restart normally",
    diagnosticsTitle: "Diagnostic report", privacy: "Only app version, platform and recovery mode. No logs, settings or conversation content.", preview: "Preview report", export: "Save diagnostic report…", exported: "Diagnostic report saved.", exportCancelled: "Save cancelled.", restartCancelled: "Restart cancelled.", actionFailed: "The action could not finish. Your configuration was not changed.", restarting: "Preparing to restart…",
  },
  zh: {
    title: "Wuu 恢复中心", heading: "应用未能正常打开", safeHeading: "已进入诊断模式", safeSummary: "应用插件未启动，原配置保持不变。",
    preserve: "已保存的会话和草稿保持不变。未保存的输入可能无法恢复。",
    retryTitle: "重新加载应用窗口", retry: "尝试重新加载", cancel: "停止等待", idle: "重新加载仍使用原配置，不会修复损坏的插件。", loading: "正在等待应用页面加载…", loaded: "页面已加载，请检查应用窗口；这不表示插件已恢复正常。", failed: "页面未能加载。", cancelled: "已停止等待，重新加载可能仍在进行；没有取消后端任务。", timeout: "页面未在 30 秒内加载，操作可能仍在进行。", limit: "已达到重试上限，请使用诊断模式或退出应用。", unavailable: "诊断模式没有正在运行的普通应用窗口。",
    restartTitle: "重启应用", restartHelp: "诊断模式仅打开恢复中心，不启动插件。正常重启使用原配置。", safe: "以诊断模式重启", normal: "正常重启",
    diagnosticsTitle: "诊断报告", privacy: "仅包含应用版本、平台和恢复模式，不含日志、配置或会话内容。", preview: "预览报告", export: "保存诊断报告…", exported: "诊断报告已保存。", exportCancelled: "已取消保存。", restartCancelled: "已取消重启。", actionFailed: "操作未完成，配置未修改。", restarting: "正在准备重启…",
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
  for (const [id, value] of Object.entries({ title: snapshot.mode === "diagnostic" ? c.safeHeading : c.heading, summary: snapshot.mode === "diagnostic" ? c.safeSummary : "", preserve: c.preserve, "retry-title": c.retryTitle, retry: c.retry, cancel: c.cancel, "restart-title": c.restartTitle, "restart-help": c.restartHelp, safe: c.safe, normal: c.normal, "diagnostics-title": c.diagnosticsTitle, privacy: c.privacy, "preview-label": c.preview, export: c.export, preview: snapshot.diagnosticPreview })) text(id, value);
  const phase = snapshot.retry.phase;
  let status = !snapshot.retryAvailable ? c.unavailable : phase === "timed-out" ? c.timeout : c[phase];
  if (snapshot.retry.attempts >= 3 && !snapshot.retry.pending) status += ` ${c.limit}`;
  if (phase === "loading" && snapshot.retry.startedAt) status += ` ${Math.max(0, Math.floor((Date.now() - snapshot.retry.startedAt) / 1000))}s`;
  text("status", snapshot.restarting ? c.restarting : status);
  button("retry").disabled = !snapshot.retryAvailable || snapshot.retry.pending || snapshot.retry.attempts >= 3 || snapshot.restarting;
  button("cancel").disabled = phase !== "loading" || snapshot.restarting;
  button("safe").disabled = snapshot.mode === "diagnostic" || snapshot.restarting;
  button("normal").disabled = snapshot.restarting;
  // Emphasize the next available step without changing recovery or confirmation rules.
  const diagnosticSuggested = phase === "failed" || phase === "timed-out" || snapshot.retry.attempts >= 3 || !snapshot.retryAvailable;
  button("retry").classList.toggle("primary", !diagnosticSuggested && !button("retry").disabled);
  button("safe").classList.toggle("primary", diagnosticSuggested && !button("safe").disabled);
  button("normal").classList.toggle("primary", snapshot.mode === "diagnostic" && !snapshot.restarting);
  document.getElementById("status")!.dataset.tone = diagnosticSuggested && snapshot.mode !== "diagnostic" ? "warning" : "neutral";
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
