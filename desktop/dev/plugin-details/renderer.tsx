import { useState } from "react";
import { createRoot } from "react-dom/client";
import type { ExtensionInventoryRecord, SkillSummary, WuuDesktopApi } from "../../src/shared/protocol";
import { SkillsCatalog } from "../../src/renderer/SkillsCatalog";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import "../../src/renderer/styles.css";

// Production components with in-memory data only. Query parameters cover
// theme, size, long text, and enabled/off/approval/failed/minimal states.
const params = new URLSearchParams(location.search);
const scenario = params.get("state") ?? "enabled";
const long = params.has("long");
const minimal = scenario === "minimal";
const settings: Record<string, boolean | string | number> = { enabled: true, name: "每周阅读摘要", limit: 10 };
const plugin: ExtensionInventoryRecord = {
  id: "preview.reading", kind: "plugin", name: long ? "Reading companion for research and long-form documents" : "阅读助手",
  description: minimal ? undefined : "收集阅读资料，整理重点，让每一次阅读都有所积累。",
  long_description: minimal ? undefined : "将对话中的阅读材料整理为笔记，保留关键观点与原文链接，方便回顾和继续讨论。\n可以通过侧栏查看阅读清单，也可以使用命令快速整理当前内容。",
  developer: minimal ? undefined : "Wuu Preview",
  icon: { name: "book-open" }, package_source: "user", fingerprint: "preview",
  provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: "preview.reading" },
  state: scenario === "approval" ? "pending" : "granted",
  approval_state: scenario === "approval" ? "pending" : "granted",
  enabled: scenario !== "off", runtime_state: scenario === "failed" ? "failed" : "active",
  last_error: scenario === "failed" ? "The reading service could not connect. Check the connection and try again." : undefined,
  requested_permissions: ["files.read", "network.connect"],
  contributions: minimal ? {} : {
    navigation: [{ id: "reading", view: "reading.list", title: long ? "A reading list with a very long title that must wrap without overlapping adjacent content" : "阅读清单" }],
    commands: [{ id: "reading.summarize", kind: "runtime_action", title: "整理当前阅读内容" }],
    settings: [
      { id: "enabled", type: "boolean", title: "自动整理", description: "阅读结束后，将重点保存到笔记。", default: true, scope: "user", apply: "live" },
      { id: "name", type: "string", title: "笔记名称", default: "每周阅读摘要", scope: "user", apply: "live" },
      { id: "limit", type: "number", title: "保留篇数", default: 10, scope: "user", apply: "live" },
    ],
  },
};
const skills: SkillSummary[] = minimal ? [] : [{
  name: "reading-notes", description: "整理材料并提取关键观点。", source: "plugin:preview.reading", user_invocable: true, disable_model_invoke: false,
}];
window.wuu = {
  initialLanguagePreference: params.get("locale") === "en" ? "en-US" : "zh-CN",
  listSkills: async () => ({ skills }),
  getPluginDiagnostics: async () => ({ diagnostics: [] }),
  getPluginSetting: async ({ key }: { key: string }) => ({ value: settings[key] }),
  setPluginSetting: async ({ key, value }: { key: string; value: boolean | string | number }) => ({ value: settings[key] = value }),
  readSkillContent: async () => ({ content: "# Reading notes\n\nSummarize the material and keep links to its sources." }),
} as unknown as WuuDesktopApi;
document.documentElement.dataset.theme = params.get("theme") ?? "light";
applyMessageFlowFontSize(Number(params.get("size")) || 15);
startFocusModality();

function Fixture(): JSX.Element {
  const [record, setRecord] = useState(plugin);
  return <main className="skills-scroll-region" style={{ height: "100vh", overflow: "auto" }}>
    <SkillsCatalog
      extensionInventory={[record]}
      openPluginRequest={{ id: record.id, request: 1 }}
      onUpdateExtensionPackage={async ({ action }) => setRecord(current => ({ ...current, enabled: action !== "disable", approval_state: "granted", state: "granted" }))}
      onRefreshCatalog={async () => { setRecord(current => ({ ...current, runtime_state: "active", last_error: undefined })); return skills; }}
    />
  </main>;
}

createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><Fixture /></WuuUIRoot></I18nProvider>);
