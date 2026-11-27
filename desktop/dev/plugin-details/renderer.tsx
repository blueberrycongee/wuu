import { useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import type { ExtensionInventoryRecord, MCPServerStatus, SkillSummary } from "../../src/shared/protocol";
import { SkillsCatalog } from "../../src/renderer/SkillsCatalog";
import { createPreviewApplication } from "../../../packages/plugin-sdk/preview/application";
import type { ApplicationCatalogV1 } from "../../../packages/plugin-sdk/src/ui-catalog";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import "../../src/renderer/styles.css";

// Production components with in-memory data only. Query parameters cover
// theme, size, long text, enabled/off/approval/failed/minimal states, and
// Host-shaped dependency diagnostics (issue=<kind>). Refresh simulates remediation.
const params = new URLSearchParams(location.search);
const scenario = params.get("state") ?? "enabled";
const long = params.has("long");
const issueKinds = ["missing_requirement", "version_mismatch", "conflict", "optional_missing", "optional_version_mismatch", "optional_cycle"] as const;
const issueKind = issueKinds.find(kind => kind === params.get("issue"));
const blockingDependency = issueKind === "missing_requirement" || issueKind === "version_mismatch";
const minimal = scenario === "minimal";
const settings: Record<string, boolean | string | number> = { enabled: true, name: "每周阅读摘要", limit: 10 };
const running = scenario !== "off" && scenario !== "failed" && scenario !== "approval" && !blockingDependency;
const plugin: ExtensionInventoryRecord = {
  id: "preview.reading", kind: "plugin", name: long ? "Reading companion for research and long-form documents" : "阅读助手",
  description: minimal ? undefined : "收集阅读资料，整理重点，让每一次阅读都有所积累。",
  long_description: minimal ? undefined : "将对话中的阅读材料整理为笔记，保留关键观点与原文链接，方便回顾和继续讨论。\n可以通过侧栏查看阅读清单，也可以使用命令快速整理当前内容。",
  developer: minimal ? undefined : "Wuu Preview",
  icon: { name: "book-open" }, package_source: minimal ? "bundled" : "user", fingerprint: "preview",
  provenance: minimal
    ? { kind: "plugin", source: "bundled", scope: "user", plugin_id: "preview.reading", official: true, path: "/cache/bundled-generations/ab12cd34ef/preview/plugin.json" }
    : { kind: "plugin", source: "user", scope: "user", plugin_id: "preview.reading", path: "~/.wuu/plugins/reading-companion" },
  state: scenario === "approval" ? "pending" : "granted",
  approval_state: scenario === "approval" ? "pending" : "granted",
  grant_scope: scenario === "approval" || minimal ? undefined : "user",
  enabled: scenario !== "off", runtime_state: scenario === "failed" ? "failed" : blockingDependency ? "inactive" : "active",
  activation_issues: issueKind ? [{ kind: issueKind, related_plugin_id: long ? "research-library-provider-with-a-long-package-identifier" : "reading-library" }] : [],
  last_error: scenario === "failed" ? "The reading service could not connect. Check the connection and try again." : undefined,
  requested_permissions: ["files.read", "network.connect"],
  conversation_tools: running && !minimal ? ["reading.notes.save", "reading.sources.open"] : [],
  dependencies: minimal ? [] : [{ id: "memory", version: ">=0.1.0" }, { id: "monitor", optional: true }],
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
const mcpServers: MCPServerStatus[] = minimal ? [] : [
  { name: "library-catalog", state: "ready", connected: true, tool_count: 2, configuration_enabled: true, editable: false, plugin_id: "preview.reading", resource_url: "https://catalog.example.invalid/mcp" },
];
document.documentElement.dataset.theme = params.get("theme") ?? "light";
applyMessageFlowFontSize(Number(params.get("size")) || 15);
startFocusModality();

function Fixture(): JSX.Element {
  const [record, setRecord] = useState(plugin);
  const application = useMemo<ReturnType<typeof createPreviewApplication>>(() => {
    const base = createPreviewApplication();
    const noDiagnostics = Object.freeze([]);
    const snapshot = { contextKey: "plugin-details-preview", inventory: [record],
      capabilities: { install: false, update: true, remove: false } };
    const catalog: ApplicationCatalogV1 = {
      ...base.catalog,
      getSnapshot: () => snapshot, listSkills: async () => skills,
      listSkillsForThread: async ({threadId}) => ({contextKey:snapshot.contextKey, threadId, skills}),
      readSkill: async () => ({content: "# Reading notes\n\nSummarize the material and keep links to its sources."}),
      update: async ({ action }) => setRecord(current => {
        const enabled = action !== "disable";
        return { ...current, enabled, approval_state: "granted", state: "granted",
          runtime_state: enabled ? "active" : "stopped",
          conversation_tools: enabled && !minimal ? ["reading.notes.save", "reading.sources.open"] : [] };
      }),
      refresh: async () => { setRecord(current => ({ ...current, runtime_state: "active", last_error: undefined, activation_issues: [] })); return skills; },
      loadIcon: async () => { throw new Error("File icons are not available in this preview"); },
    };
    return { ...base, catalog, settingsControls: {...base.settingsControls,
      listMCPServers: async () => ({ servers: mcpServers }),
      getConflicts: () => noDiagnostics, getDiagnostics: () => noDiagnostics,
      getPluginSetting: async ({id, key}) => ({id, key, scope: "user", value: settings[key]}),
      setPluginSetting: async ({id, key, value}) => ({id, key, scope: "user", value: settings[key] = value}),
      getPluginDiagnostics: async ({id}) => ({id, diagnostics: []}),
    } };
  }, [record]);
  return <main className="skills-scroll-region" style={{ height: "100vh", overflow: "auto" }}>
    <SkillsCatalog
      application={application}
      openPluginRequest={{ id: record.id, request: 1 }}
    />
  </main>;
}

createRoot(document.getElementById("root")!).render(<I18nProvider preferenceStore={{get: () => params.get("locale") === "en" ? "en-US" : "zh-CN", set: async () => {}, subscribe: () => () => {}}}><WuuUIRoot><Fixture /></WuuUIRoot></I18nProvider>);
