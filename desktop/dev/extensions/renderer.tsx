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
  record("automation", "Automation", "clock", "Scheduled Agent prompts and recurring work."),
  record("memory", "Memory", "brain", "Durable user memory notebook and management view.", {
    contributions: { settings: [{ id: "autosave", type: "boolean", title: "自动整理记忆", description: "会话结束后整理可复用的偏好与经验。", default: true, scope: "user", apply: "live" }] },
  }),
  record("dream", "Dream", "moon", "Background consolidation of durable workspace memory.", { enabled: false, runtime_state: "stopped" }),
  record("cua-mac", "Computer Use for Mac", "layout-grid", "Control macOS apps through Accessibility, ScreenCaptureKit, and native input.", {
    runtime_state: "failed",
    last_error: "wuu-cua-mac exited: accessibility permission not granted",
    requested_permissions: ["accessibility.read", "accessibility.control", "screen.capture", "app.activate", "input.synthesize"],
  }),
  record("git-delivery", long ? "Git Delivery with an exceptionally long community plugin name" : "Git Delivery", "", long
    ? "Publishes finished work to a review branch, opens the pull request, and keeps its description in sync with the conversation summary."
    : "Publishes finished work to a review branch.", {
    package_source: "user",
    approval_state: "pending",
    runtime_state: "inactive",
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

window.wuu = {
  initialLanguagePreference: params.get("lang") === "en" ? "en-US" : "zh-CN",
  initialThemePreference: params.get("theme") === "dark" ? "dark" : "light",
  getThemePreference: async () => (params.get("theme") === "dark" ? "dark" : "light"),
  setThemePreference: async () => undefined,
  onThemePreferenceChange: () => () => undefined,
  getMessageFlowFontSize: async () => Number(params.get("size")) || 14.5,
  setMessageFlowFontSize: async () => undefined,
  listSkills: async () => ({ skills: structuredClone(skills) }),
  readSkillContent: async ({ name }: { name: string }) => ({ content: `# ${name}\n\n示例技能内容，仅用于预览。` }),
  getPluginSetting: async () => ({ value: true }),
  setPluginSetting: async () => ({ value: true }),
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
