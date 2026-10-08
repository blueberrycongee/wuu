import { useLayoutEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import type {
  ExtensionInventoryRecord,
  ExtensionPackageUpdateParams,
  SkillSummary,
  WuuDesktopApi,
} from "../../src/shared/protocol";
import { SkillsCatalog } from "../../src/renderer/SkillsCatalog";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";

// Isolated visual fixture: synthetic skills and plugin packages covering each
// status tone. No Go process, saved desktop preferences, or plugin runtime.
const params = new URLSearchParams(location.search);
const empty = params.has("empty");
const long = params.has("long");

const skills: SkillSummary[] = empty ? [] : [
  { name: "browser", source: "bundled", user_invocable: false, disable_model_invoke: false, description: "嵌入式浏览器自动化：在后台的隐藏网页宿主里导航、观察并操作网页。" },
  { name: "pptx-generator", source: "bundled", user_invocable: true, disable_model_invoke: false, description: "Generate, edit, and read PowerPoint presentations." },
  { name: "skill-creator", source: "bundled", user_invocable: true, disable_model_invoke: false, description: "Guide for creating effective skills." },
  { name: "cua-mac", source: "plugin:cua-mac", user_invocable: false, disable_model_invoke: false, description: "Observe and control native macOS apps through Accessibility, ScreenCaptureKit, and native input." },
  { name: "code-review", source: "user", user_invocable: true, disable_model_invoke: false, description: "按仓库约定审查当前改动，只报告有证据的缺陷。" },
  {
    name: long ? "release-notes-with-an-exceptionally-long-skill-name" : "release-notes",
    source: "project",
    user_invocable: true,
    disable_model_invoke: false,
    description: long
      ? "根据合并记录整理面向用户的发布说明，区分新增、变更与修复，并核对每一条都能追溯到具体提交，避免把内部重构写进公开说明。"
      : "根据合并记录整理面向用户的发布说明。",
  },
];

const record = (
  id: string,
  name: string,
  icon: string,
  description: string,
  extra: Partial<ExtensionInventoryRecord> = {},
): ExtensionInventoryRecord => ({
  id,
  name,
  description,
  icon: icon ? { name: icon } : undefined,
  kind: "plugin",
  state: "active",
  fingerprint: `sha256:${id.padEnd(12, "0")}9f2c4e71a0b3d58e6f1c`,
  package_source: "bundled",
  approval_state: "official",
  runtime_state: "active",
  enabled: true,
  provenance: { kind: "plugin", source: "bundled", scope: "bundled", official: true, plugin_id: id, path: `/Applications/Wuu.app/Contents/Resources/plugins/${id}` },
  ...extra,
});

const initialPlugins: ExtensionInventoryRecord[] = empty ? [] : [
  record("automation", "Automation", "clock", "Schedule prompts and recurring Agent work.", {
    developer: "Wuu",
    long_description: "Runs saved prompts on a schedule, either as new conversations or by waking an existing one, so recurring checks and reports happen without you.",
  }),
  record("ask-user", "Ask User", "message-square", "Offer focused questions without pausing a task.", { developer: "Wuu", enabled: false, runtime_state: "stopped" }),
  record("memory", "Memory", "brain", "Remember durable preferences, feedback, references, and lessons.", {
    developer: "Wuu",
    contributions: {
      settings: [
        { id: "autosave", type: "boolean", title: "自动整理记忆", description: "会话结束后整理可复用的偏好与经验。", default: true, scope: "user", apply: "live" },
        { id: "recall", type: "enum", title: "召回方式", enum: ["相关时", "每轮"], default: "相关时", scope: "workspace", apply: "live" },
        { id: "limit", type: "number", title: "最多保留条目", default: 200, scope: "user", apply: "restart" },
      ],
      settings_pages: [{ id: "memory", view: "memory.settings", title: "Memory" }],
    },
  }),
  record("dream", "Dream", "moon", "Consolidate durable workspace knowledge in the background.", { developer: "Wuu", enabled: false, runtime_state: "stopped" }),
  record("todo", "TODO", "list-todo", "Track a visible task checklist.", { developer: "Wuu" }),
  record("goal", "Goal", "check-circle", "Continue toward a persistent goal.", {
    developer: "Wuu",
    contributions: { commands: [{ id: "goal", title: "Set a goal", kind: "prompt_template" }] },
  }),
  record("cua-mac", "Computer Use for Mac", "layout-grid", "Observe and operate Mac apps with native mouse and keyboard control.", {
    developer: "Wuu",
    long_description: "Uses macOS Accessibility for semantic control, ScreenCaptureKit for observation, and native input when an app offers no accessible controls.",
    runtime_state: "failed",
    last_error: "wuu-cua-mac exited: accessibility permission not granted",
    requested_permissions: ["accessibility.read", "accessibility.control", "screen.capture", "app.activate", "input.synthesize"],
  }),
  record("paper-atelier", "Paper Atelier", "", "A cream-paper appearance for pastel wallpapers.", {
    package_source: "user",
    approval_state: "granted",
    developer: "Community",
    provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: "paper-atelier", path: "~/.wuu/plugins/paper-atelier" },
    contributions: { themes: [{ id: "paper", name: "Paper", base: "light", tokens: {} }] },
  }),
  record("git-delivery", long ? "Git Delivery with an exceptionally long community plugin name" : "Git Delivery", "", long
    ? "Publishes finished work to a review branch, opens the pull request, and keeps its description in sync with the conversation summary."
    : "Publishes finished work to a review branch.", {
    package_source: "user",
    approval_state: "pending",
    runtime_state: "inactive",
    enabled: false,
    provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: "git-delivery", path: "~/.wuu/plugins/git-delivery" },
    requested_permissions: ["files.read", "process.spawn", "network.connect", "session.read"],
  }),
  record("developer-loop", "Developer Loop", "terminal", "Runs the local build and test loop after each edit.", {
    package_source: "dev",
    approval_state: "granted",
    grant_scope: "project",
    provenance: { kind: "plugin", source: "dev", scope: "project", plugin_id: "developer-loop", path: "~/code/developer-loop" },
    requested_permissions: ["files.read", "files.write", "process.spawn", "shell.env"],
    pending_update: { version: "0.4.0", fingerprint: "sha256:next", active_fingerprint: "sha256:current" },
  }),
];

// A skill file opens with its own title and summary; `long` makes it scroll.
const skillSection = "## 步骤\n\n1. 读取改动涉及的文件和调用方。\n2. 对照仓库约定逐项检查。\n3. 只报告能复现或有证据的问题。\n";
const skillContent = [
  "---\nname: code-review\n---",
  "# 代码审查",
  "按仓库约定审查当前改动，只报告有证据的缺陷。",
  "## 何时使用\n\n- 提交前检查自己的改动\n- 审查他人的分支",
  ...Array.from({ length: long ? 6 : 1 }, () => skillSection),
].join("\n\n");

let skillLoadFailed = false;

window.wuu = {
  initialLanguagePreference: params.get("lang") === "en" ? "en-US" : "zh-CN",
  initialThemePreference: params.get("theme") === "dark" ? "dark" : "light",
  getThemePreference: async () => (params.get("theme") === "dark" ? "dark" : "light"),
  setThemePreference: async () => undefined,
  onThemePreferenceChange: () => () => undefined,
  getMessageFlowFontSize: async () => Number(params.get("size")) || 14.5,
  setMessageFlowFontSize: async () => undefined,
  // `error` fails the first skill load, as a restarting core would.
  listSkills: async () => {
    if (params.has("error") && !skillLoadFailed) {
      skillLoadFailed = true;
      throw new Error("The Wuu core is restarting.");
    }
    return { skills: structuredClone(skills) };
  },
  readSkillContent: async () => ({ content: skillContent }),
  getPluginSetting: async ({ key }: { key: string }) => ({ value: ({ autosave: true, recall: "相关时", limit: 200 } as Record<string, boolean | string | number>)[key] }),
  setPluginSetting: async ({ value }: { value: boolean | string | number }) => ({ value }),
  getPluginDiagnostics: async () => ({ diagnostics: [] }),
  unsupportedMethods: [],
} as unknown as WuuDesktopApi;

startFocusModality();

function Fixture(): JSX.Element {
  const [plugins, setPlugins] = useState(initialPlugins);
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = params.get("theme") || "light";
    document.documentElement.dataset.platform = "darwin";
    applyMessageFlowFontSize(Number(params.get("size")) || 14.5);
  }, []);
  const update = async ({ id, action }: ExtensionPackageUpdateParams) => {
    setPlugins((current) => current.map((item) => {
      if (item.id !== id) return item;
      if (action === "disable") return { ...item, enabled: false, runtime_state: "stopped" };
      if (action === "enable") return { ...item, enabled: true, runtime_state: "active" };
      if (action === "grant") return { ...item, approval_state: "granted", runtime_state: "active" };
      if (action === "promote_update") return { ...item, pending_update: undefined };
      return item;
    }));
  };
  return (
    <div className="scroll-region skills-scroll-region" style={{ height: "100vh" }}>
      <div className="scroll-region-content">
        <SkillsCatalog
          extensionInventory={plugins}
          onRefreshCatalog={async () => structuredClone(skills)}
          onUpdateExtensionPackage={update}
          onInstallPluginPackage={async () => undefined}
          onRemovePluginPackage={async () => undefined}
        />
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><Fixture /></WuuUIRoot></I18nProvider>);
