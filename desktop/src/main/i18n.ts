import {
  resolveAppLocale,
  type AppLocale,
  type LanguagePreference,
} from "../shared/protocol";

const zhCN = {
  activityHide: "隐藏预览，任务继续运行",
  activityExpand: "在侧边栏中打开",
  activityTakeover: "接管并暂停任务",
  activityRelease: "结束接管",
  activityStop: "停止任务",
  activityPreparing: "正在准备预览…",
  activityPaused: "预览已暂停",
  activityUnavailable: "预览暂不可用",
  activityPreviewRecovery: "请在对话中查看任务进度。",
  activityCaptureRecovery: "请确认目标窗口可用，并检查屏幕录制权限。",
  activityControlFailed: "操作未完成，请重试。",
  activityWaiting: "等待确认",
  activityControlled: "你正在控制",
  activityIdle: "发送新指令以继续",
  activityStopped: "任务已停止",
  activityCompleted: "任务已完成",
  activityChangingControl: "正在切换控制权…",
  activityConfirmationHint: "请在对话中查看待确认的请求。",
  activityBlank: "当前窗口没有可见内容。",
  activitySuspended: "当前窗口暂时无法捕获。",
  activityBrowser: "浏览器活动",
  activityComputer: "电脑活动",
  conversation: "对话",
  resize: "调整大小",
  pet: "桌宠",
  openConversation: "打开会话 · {title}",
  closePet: "关闭桌宠",
  petTalk: "和它说话",
  petShowApp: "打开 Wuu",
  petNewConversation: "新对话 · {workspace}",
  petNoWorkspace: "先在 Wuu 里打开一个工作区",
  petReplyTo: "回复 · {title}",
  petAnswer: "去回答",
  petCancelReply: "改为新对话",
  petMessage: "消息",
  petIdle: "空闲",
  petRunning: "进行中",
  petDone: "已完成",
  petFailed: "执行失败",
  petNeedsAnswer: "等待确认",
  petRetry: "重试",
  petSending: "发送中…",
  petSend: "发送",
  petNewChat: "新对话",
  petDismiss: "隐藏",
  petSendFailed: "发送失败",
  rendererRecoveryFailed: "此窗口无法恢复",
  rendererRecoveryDetail: "窗口连续加载失败。可以重新加载，或关闭后再打开。此窗口的终端已停止，未发送的草稿可能丢失；已保存的会话仍然保留。",
  reloadWindow: "重新加载",
  closeWindow: "关闭窗口",
  chooseExistingFolder: "使用现有文件夹",
  useFolder: "使用文件夹",
  createBlankProject: "新建空白工作区",
  createProject: "创建工作区",
  relocateWorkspace: "重新定位工作区",
  relocateHere: "定位到此文件夹",
  open: "打开",
  openInApplication: "在 {application} 中打开",
  openWith: "打开方式",
  copyPath: "复制路径",
  saveArtifactAs: "另存为…",
  exportTurnArtifacts: "导出本轮产物…",
  artifactEvidenceArchive: "Wuu 产物证据包",
  artifactEvidenceDescription: "保存本轮的 {count} 个产物快照及其来源标识和校验值。不会包含对话正文。",
  showInFinder: "在访达中显示",
  showInExplorer: "在资源管理器中显示",
  showInFileManager: "在文件管理器中显示",
  projectUnavailable:
    "工作区目录当前不可用：{path}。请恢复该目录，或从工作区菜单选择“重新定位…”。",
} as const;

export type MainTranslationKey = keyof typeof zhCN;

const enUS = {
  activityHide: "Hide preview; keep task running",
  activityExpand: "Open in the side panel",
  activityTakeover: "Take over and pause task",
  activityRelease: "Finish takeover",
  activityStop: "Stop task",
  activityPreparing: "Preparing preview…",
  activityPaused: "Preview paused",
  activityUnavailable: "Preview unavailable",
  activityPreviewRecovery: "Check the conversation for task progress.",
  activityCaptureRecovery: "Check that the target window is available and screen recording permission is enabled.",
  activityControlFailed: "Action failed. Please try again.",
  activityWaiting: "Waiting for confirmation",
  activityControlled: "You have control",
  activityIdle: "Send an instruction to continue",
  activityStopped: "Task stopped",
  activityCompleted: "Task completed",
  activityChangingControl: "Updating control…",
  activityConfirmationHint: "Review the confirmation request in the conversation.",
  activityBlank: "The current window has no visible content.",
  activitySuspended: "The window is not currently available for capture.",
  activityBrowser: "Browser activity",
  activityComputer: "Computer activity",
  conversation: "Conversation",
  resize: "Resize",
  pet: "Desktop pet",
  openConversation: "Open conversation · {title}",
  closePet: "Close desktop pet",
  petTalk: "Talk to pet",
  petShowApp: "Open Wuu",
  petNewConversation: "New conversation · {workspace}",
  petNoWorkspace: "Open a workspace in Wuu first",
  petReplyTo: "Reply · {title}",
  petAnswer: "Answer",
  petCancelReply: "Start a new conversation instead",
  petMessage: "Message",
  petIdle: "Idle",
  petRunning: "In progress",
  petDone: "Completed",
  petFailed: "Failed",
  petNeedsAnswer: "Waiting for confirmation",
  petRetry: "Retry",
  petSending: "Sending…",
  petSend: "Send",
  petNewChat: "New conversation",
  petDismiss: "Hide",
  petSendFailed: "Send failed",
  rendererRecoveryFailed: "This window could not recover",
  rendererRecoveryDetail: "The window repeatedly failed to load. Reload it or close and reopen it. Terminals in this window have stopped and unsent drafts may be lost; saved conversations remain available.",
  reloadWindow: "Reload",
  closeWindow: "Close window",
  chooseExistingFolder: "Use an existing folder",
  useFolder: "Use folder",
  createBlankProject: "Create a blank workspace",
  createProject: "Create workspace",
  relocateWorkspace: "Relocate workspace",
  relocateHere: "Use this folder",
  open: "Open",
  openInApplication: "Open in {application}",
  openWith: "Open With",
  copyPath: "Copy Path",
  saveArtifactAs: "Save As…",
  exportTurnArtifacts: "Export turn artifacts…",
  artifactEvidenceArchive: "Wuu artifact evidence",
  artifactEvidenceDescription: "Save {count} artifact snapshots from this turn with source identifiers and checksums. Conversation text is not included.",
  showInFinder: "Show in Finder",
  showInExplorer: "Show in Explorer",
  showInFileManager: "Show in File Manager",
  projectUnavailable:
    "The workspace folder is currently unavailable: {path}. Restore the folder or choose Relocate from the workspace menu.",
} as const satisfies Record<MainTranslationKey, string>;

export const MAIN_TRANSLATION_RESOURCES = {
  "zh-CN": zhCN,
  "en-US": enUS,
} satisfies Record<AppLocale, Record<MainTranslationKey, string>>;

let activeLocale: AppLocale = "zh-CN";

export function resolveMainLocale(
  preference: LanguagePreference,
  systemLocale: string,
): AppLocale {
  return resolveAppLocale(preference, systemLocale);
}

export function setMainLocale(locale: AppLocale): void {
  activeLocale = locale;
}

export function getMainLocale(): AppLocale {
  return activeLocale;
}

export function mainTranslate(
  key: MainTranslationKey,
  values: Record<string, string | number> = {},
  locale: AppLocale = activeLocale,
): string {
  return MAIN_TRANSLATION_RESOURCES[locale][key].replace(
    /\{(\w+)\}/g,
    (_, name: string) => String(values[name] ?? `{${name}}`),
  );
}
