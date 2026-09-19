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
  rendererRecoveryFailed: "此窗口无法恢复",
  rendererRecoveryDetail: "窗口连续加载失败。可以重新加载，或关闭后再打开。此窗口的终端已停止，未发送的草稿可能丢失；已保存的会话仍然保留。",
  reloadWindow: "重新加载",
  closeWindow: "关闭窗口",
  chooseExistingFolder: "使用现有文件夹",
  useFolder: "使用文件夹",
  createBlankProject: "新建空白项目",
  createProject: "创建项目",
  relocateWorkspace: "重新定位工作区",
  relocateHere: "定位到此文件夹",
  open: "打开",
  openInApplication: "在 {application} 中打开",
  openWith: "打开方式",
  copyPath: "复制路径",
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
  rendererRecoveryFailed: "This window could not recover",
  rendererRecoveryDetail: "The window repeatedly failed to load. Reload it or close and reopen it. Terminals in this window have stopped and unsent drafts may be lost; saved conversations remain available.",
  reloadWindow: "Reload",
  closeWindow: "Close window",
  chooseExistingFolder: "Use an existing folder",
  useFolder: "Use folder",
  createBlankProject: "Create a blank project",
  createProject: "Create project",
  relocateWorkspace: "Relocate workspace",
  relocateHere: "Use this folder",
  open: "Open",
  openInApplication: "Open in {application}",
  openWith: "Open With",
  copyPath: "Copy Path",
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
