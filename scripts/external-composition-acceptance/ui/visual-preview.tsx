import * as React from "react";
import { createRoot } from "react-dom/client";
import { PluginHost } from "../../../desktop/src/renderer/plugins/PluginHost";
import { ApplicationShellOutlet } from "../../../desktop/src/renderer/plugins/ApplicationShell";
import { WorkbenchController } from "../../../desktop/src/renderer/plugins/Workbench";
import { createPluginTranslator } from "../../../desktop/src/renderer/plugins/pluginI18n";
import { startFocusModality } from "../../../desktop/src/renderer/FocusModality";
import { I18nProvider } from "../../../desktop/src/renderer/i18n";
import { applyMessageFlowFontSize } from "../../../desktop/src/renderer/MessageFlowFontSizeSection";
import { activate as activateGoal } from "../../../internal/plugin/bundled/goal-ui/desktop.js";
import { activate as activateAutomation } from "../../../internal/plugin/bundled/automation-ui/desktop.js";
import "../../../desktop/src/renderer/styles.css";

// This browser fixture renders the shipped feature modules and host primitives.
// Its synthetic services are deliberately separate from the real-core gate in run.mjs.
const params = new URLSearchParams(location.search);
const scenario = params.get("scenario") ?? "populated";
const long = scenario === "long";
const empty = scenario === "empty";
const workspace = { id: "visual-workspace", root: "/synthetic/workspace", name: "Preview workspace", available: true };
const viewContext = Object.freeze({});
const context = Object.freeze({ threadId: "visual-thread", mainConversation: true, viewTypeId: "automation.catalog", viewContext });
const tasks = Array.from({ length: empty ? 0 : long ? 24 : 4 }, (_, index) => ({
  id: `task-${index}`, title: long ? `Task ${index + 1} — Review extremely long release notes and repeated accessibility findings across every independently supplied workspace surface` : ["Review release notes", "Check accessibility", "Summarize open issues", "Prepare weekly update"][index],
  prompt: "Review the synthetic workspace, record concrete findings, and preserve all existing user work.\n".repeat(long ? 8 : 1),
  cron: "0 9 * * 1-5", timezone: "UTC", recurring: true, durable: true, paused: index === 2,
  mode: "new_thread", workspace_mode: "shared", next_run_at: `2026-10-29T${String(9 + index % 12).padStart(2, "0")}:00:00Z`,
}));
const runs = tasks.slice(0, 2).map((task, index) => ({ id: `run-${index}`, task_id: task.id, task,
  status: index ? "failed" : "running", triggered_at: "2026-10-28T09:00:00Z", error: index ? "Synthetic task failure for visual state coverage" : undefined,
}));
let unavailable = false;
const requests: Array<{ service?: string; method: string; input: unknown; unavailable: boolean }> = [];
const host = new PluginHost({ react: React,
  listWorkspaces: async () => ({ activeWorkspaceId: workspace.id, workspaces: [workspace] }),
  listThreads: async () => [{ id: "visual-thread", title: "Preview conversation", workspaceRoot: workspace.root }],
  invokeRuntime: async ({ service, method, input }) => {
    requests.push({ service, method, input, unavailable });
    if (unavailable) throw new Error(`Selected ${service} provider is unavailable; no fallback provider is active.`);
    if (method !== "request") throw new Error(`Unexpected transport method: ${method}`);
    const request = input as { method: string };
    if (service === "goal.control" && request.method === "get_goal") return { goal: empty ? null : {
      objective: long ? "Verify the complete independent composition with a very long objective, readable large text, retained service state, and no hidden mutation fallback. ".repeat(4) : "Verify independent feature UI composition",
      status: "paused", tokens_used: 15320, time_used_seconds: 125,
    } };
    if (service === "automation.scheduler" && request.method === "automation.list") return { workspace, tasks };
    if (service === "automation.scheduler" && request.method === "automation.run.list") return { runs };
    throw new Error(`Unexpected preview mutation: ${service}/${request.method}`);
  },
});
const controller = new WorkbenchController(host);
const generation = "visual-preview";
await host.activateGeneration({ pluginId: "external-surfaces", generation, register(api) {
  api.registerUISlot({ id: "composer-host", cardinality: "one", service: "ui.composer" });
  api.registerUISlot({ id: "navigation-host", cardinality: "one", service: "ui.navigation" });
  api.registerUISlot({ id: "composer.above", cardinality: "many" });
  api.registerUISlot({ id: "sidebar.primary", cardinality: "many" });
  api.registerUIContribution({ id: "composer", target: "external-surfaces/composer-host", render: ({ slots: { Slot } }) =>
    <section data-external-composer aria-label="Independent composer"><Slot name="external-surfaces/composer.above" context={context} /></section> });
  api.registerUIContribution({ id: "navigation", target: "external-surfaces/navigation-host", render: ({ slots: { Slot } }) =>
    <aside data-external-navigation aria-label="Independent navigation"><Slot name="external-surfaces/sidebar.primary" context={context} /></aside> });
  api.registerApplicationShell({ id: "shell", render: ({ slots: { Slot } }) => <main data-external-shell>
    <header data-preview-heading>Independent feature UI · {scenario}</header>
    <Slot name="external-surfaces/navigation-host" />
    <div data-preview-main><Slot name="external-surfaces/composer-host" /><section data-preview-catalog><Catalog /></section></div>
  </main> });
} });
await host.activateGeneration({ pluginId: "goal-ui", generation, register: activateGoal });
await host.activateGeneration({ pluginId: "automation-ui", generation, register: activateAutomation });
const view = host.getViewTypes().find(item => item.id === "automation.catalog")!;
function Catalog() {
  const View = view.render;
  return <View context={viewContext} locale="en-US" translate={createPluginTranslator(host, "en-US")} host={controller.createRendererHostAPI(view.pluginId, view.generation)} />;
}
document.documentElement.dataset.theme = params.get("theme") ?? "light";
applyMessageFlowFontSize(Number(params.get("font") ?? 15));
startFocusModality();
const root = createRoot(document.getElementById("root")!);
root.render(<I18nProvider preferenceStore={{ get: () => "en-US", set: async () => {}, subscribe: () => () => {} }}><ApplicationShellOutlet host={host} binding={{ pluginId: "external-surfaces", generation, id: "shell" }}
  surfaceBindings={{ "ui.composer": { pluginId: "external-surfaces", generation, id: "composer" }, "ui.navigation": { pluginId: "external-surfaces", generation, id: "navigation" } }} /></I18nProvider>);
Object.assign(window, { featureUI: {
  ready: true, scenario, taskCount: tasks.length, requests,
  disableProviders() {
    unavailable = true;
    host.publishHostEvent({ message: { method: "thread/resumed" } });
    document.dispatchEvent(new Event("visibilitychange"));
  },
  diagnostics: () => ["external-surfaces", "goal-ui", "automation-ui"].flatMap(id => host.getGenerationDiagnostics(id, generation).map(({ kind, message }) => ({ pluginId: id, kind, message }))),
} });
