import {
  resolveAppLocale,
  type AppLocale,
  type LanguagePreference,
} from "../shared/protocol";

const zhCN = {
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
  petInputPlaceholder: "让 Wuu 做点什么…",
  petNeedsAnswer: "它在等你回答，点「去回答」处理",
  petSending: "发送中…",
  petSend: "发送",
  petNewChat: "新对话",
  petDismiss: "隐藏",
  petSendFailed: "没发出去，打开 Wuu 再试一次",
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
  showInFinder: "在访达中显示",
  showInExplorer: "在资源管理器中显示",
  showInFileManager: "在文件管理器中显示",
  projectUnavailable:
    "工作区目录当前不可用：{path}。请恢复该目录，或从工作区菜单选择“重新定位…”。",
} as const;

export type MainTranslationKey = keyof typeof zhCN;

const enUS = {
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
  petInputPlaceholder: "Ask Wuu to do something…",
  petNeedsAnswer: "Waiting for your answer · use Answer",
  petSending: "Sending…",
  petSend: "Send",
  petNewChat: "New conversation",
  petDismiss: "Hide",
  petSendFailed: "Couldn't send. Open Wuu and try again.",
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
