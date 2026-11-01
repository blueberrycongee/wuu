import { ipcRenderer } from "electron";
import type { RecoveryAction, RecoverySnapshot } from "../shared/desktopRecovery";

const copy = {
  en: {
    title: "Wuu Recovery",
    normalTitle: "The app didn't open normally",
    safeTitle: "Diagnostic mode is on",
    normalLead: "Saved conversations and drafts are untouched. Unsent input may be lost.",
    safeLead: "Plugins and the backend are not running. Your configuration is unchanged.",
    retryTitle: "Reload window", retry: "Reload", cancel: "Stop waiting",
    idle: "Reloads the app page with the current configuration.", loading: "Waiting for the app page…", loaded: "Page loaded. Check the app window.",
    failed: "The page didn't load.", cancelled: "Stopped waiting; a reload may still be running.", timeout: "Not loaded after 30 seconds. It may still be running.",
    limit: "Retry limit reached.", unavailable: "No normal app window is running.",
    restartTitle: "Restart", effectsNormal: "Skips plugins. Restart interrupts work.", effectsSafe: "A normal restart loads plugins and keeps your current configuration.",
    safe: "Diagnostic restart", normal: "Normal restart",
    diagnosticsTitle: "Diagnostic report", privacy: "Only version, platform and recovery mode. No logs or conversation content.",
    preview: "Preview report", hidePreview: "Hide preview", export: "Save…",
    exported: "Diagnostic report saved.", exportCancelled: "Save cancelled.", restartCancelled: "Restart cancelled.",
    actionFailed: "The action didn't finish. Your configuration is unchanged.", restarting: "Preparing to restart…",
  },
  zh: {
    title: "Wuu 恢复中心",
    normalTitle: "应用未能正常打开",
    safeTitle: "诊断模式已启动",
    normalLead: "已保存的会话和草稿不受影响。未发送的输入可能无法恢复。",
    safeLead: "插件与后端未启动。现有配置保持不变。",
    retryTitle: "重新加载窗口", retry: "重新加载", cancel: "停止等待",
    idle: "使用当前配置重新加载页面。", loading: "正在等待页面加载…", loaded: "页面已加载，请检查应用窗口。",
    failed: "页面未能加载。", cancelled: "已停止等待；重新加载可能仍在进行。", timeout: "页面 30 秒内未加载，可能仍在进行。",
    limit: "已达到重试上限。", unavailable: "没有正在运行的普通应用窗口。",
    restartTitle: "重启应用", effectsNormal: "诊断模式不加载插件。重启会中断任务。", effectsSafe: "正常重启会重新加载插件，并沿用当前配置。",
    safe: "以诊断模式重启", normal: "正常重启",
    diagnosticsTitle: "诊断报告", privacy: "仅含版本、平台与恢复模式，不含日志或会话内容。",
    preview: "预览报告", hidePreview: "收起预览", export: "保存…",
    exported: "诊断报告已保存。", exportCancelled: "已取消保存。", restartCancelled: "已取消重启。",
    actionFailed: "操作未完成，配置未修改。", restarting: "正在准备重启…",
  },
} as const;

type Outcome = "exported" | "exportCancelled" | "restartCancelled" | "actionFailed";
type NoticeTone = "muted" | "success" | "danger";

let current: RecoverySnapshot | undefined;
let previewOpen = false;
const element = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const text = (id: string, value: string) => { element(id).textContent = value; };
const button = (id: string) => element<HTMLButtonElement>(id);

function notice(value: string, tone: NoticeTone = "muted"): void {
  const node = element("notice");
  node.textContent = value;
  node.dataset.tone = tone;
}

function setPreview(open: boolean): void {
  previewOpen = open;
  element("preview").hidden = !open;
  const toggle = button("preview-toggle");
  toggle.setAttribute("aria-expanded", String(open));
  if (current) text("preview-toggle", copy[current.locale][open ? "hidePreview" : "preview"]);
}

function render(snapshot: RecoverySnapshot): void {
  current = snapshot;
  const c = copy[snapshot.locale];
  document.documentElement.lang = snapshot.locale === "zh" ? "zh-CN" : "en";
  document.title = c.title;
  text("title", snapshot.mode === "diagnostic" ? c.safeTitle : c.normalTitle);
  text("lead", snapshot.mode === "diagnostic" ? c.safeLead : c.normalLead);
  text("retry-title", c.retryTitle);
  text("retry-label", c.retry);
  text("cancel", c.cancel);
  text("restart-title", c.restartTitle);
  text("effects", snapshot.mode === "diagnostic" ? c.effectsSafe : c.effectsNormal);
  text("safe", c.safe);
  text("normal", c.normal);
  text("diagnostics-title", c.diagnosticsTitle);
  text("privacy", c.privacy);
  text("export", c.export);
  text("preview-toggle", previewOpen ? c.hidePreview : c.preview);
  text("preview", snapshot.diagnosticPreview);

  const phase = snapshot.retry.phase;
  const limited = snapshot.retry.attempts >= 3 && phase !== "loaded" && phase !== "loading";
  let statusText = !snapshot.retryAvailable ? c.unavailable : phase === "timed-out" ? c.timeout : c[phase];
  if (limited && snapshot.retryAvailable) statusText += snapshot.locale === "zh" ? c.limit : ` ${c.limit}`;
  text("status", statusText);
  element("status-dot").dataset.phase = phase;
  element("retry-row").hidden = !snapshot.retryAvailable;

  const exhausted = limited && snapshot.retryAvailable;
  const retry = button("retry");
  retry.className = exhausted ? "button secondary" : "button primary";
  retry.disabled = !snapshot.retryAvailable || snapshot.retry.pending || snapshot.retry.attempts >= 3 || snapshot.restarting;
  retry.dataset.pending = String(snapshot.retry.pending);
  button("cancel").hidden = phase !== "loading" || snapshot.restarting;
  const safe = button("safe");
  safe.className = exhausted && snapshot.mode !== "diagnostic" ? "button primary" : "button secondary";
  safe.hidden = snapshot.mode === "diagnostic";
  safe.disabled = snapshot.restarting;
  const normal = button("normal");
  normal.className = snapshot.mode === "diagnostic" ? "button primary" : "button secondary";
  normal.disabled = snapshot.restarting;
  button("export").disabled = snapshot.restarting;
  if (snapshot.restarting) notice(c.restarting);
}

async function action(value: RecoveryAction): Promise<void> {
  try {
    const reply = await ipcRenderer.invoke("wuu:recovery-action", value) as { snapshot: RecoverySnapshot; outcome?: Outcome };
    render(reply.snapshot);
    if (reply.outcome) {
      const c = copy[reply.snapshot.locale];
      const tone = reply.outcome === "exported" ? "success" : reply.outcome === "actionFailed" ? "danger" : "muted";
      notice(c[reply.outcome], tone);
    }
  } catch { if (current) notice(copy[current.locale].actionFailed, "danger"); }
}
window.addEventListener("DOMContentLoaded", () => {
  for (const [id, name] of Object.entries({ retry: "retry", cancel: "cancel", safe: "restart-safe", normal: "restart-normal", export: "export" } as const)) button(id).addEventListener("click", () => { void action(name); });
  button("preview-toggle").addEventListener("click", () => setPreview(!previewOpen));
  ipcRenderer.on("wuu:recovery-changed", (_event, value: RecoverySnapshot) => render(value));
  void action("snapshot");
});
