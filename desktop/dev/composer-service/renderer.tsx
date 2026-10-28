import * as React from "react";
import { PluginHost } from "../../src/renderer/plugins/PluginHost";
import { createUIComposition } from "../../src/renderer/plugins/ApplicationShell";
import { createRoot } from "react-dom/client";
import { createPreviewApplication } from "../../../packages/plugin-sdk/preview/application";
import { DefaultComposerSurface } from "../../src/renderer/modules/DefaultComposerSurface";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationComposerPresentationV1 } from "../../../packages/plugin-sdk/src/ui-composer-presentation";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { ToastViewport } from "../../src/renderer/Toast";
import "../../src/renderer/styles.css";

const base = createPreviewApplication();
const source = "draft-one";
let state = { ...base.getSnapshot(),
  tabs: { ...base.getSnapshot().tabs, activeTabId: source, canSubmit: true, items: [{ id: source, kind: "draft" as const, title: "Source", workspaceRoot: "/synthetic", project: false, draftText: "", hasAttachments: false }] },
  drafts: { ...base.getSnapshot().drafts, activeTabId: source, primary: { revision: 0, ownerTabId: source, text: "", imageIds: [], fileIds: [], selections: [] } },
  composerRuntime: { owners: [{ ownerTabId: source, cwd: "/synthetic", available: true, readOnly: false, busy: false, engineLocked: false, engineSelectionPending: false, permissionLocked: false,
    selection: { engine: "wuu", provider: "fixture", model: "Model", effort: "", variant: "", speed: "", modelOptions: {}, permissionMode: "standard", approveForMe: false },
    nativeDefaults: { model: "", effort: "", speed: "", modelOptions: {} }, worktree: { available: false, enabled: false, startBranch: "", branches: [] },
    engines: [], providers: [], providerModels: [], engineModelsLoading: false, engineModelsError: "", providerModelsLoading: false, providerModelsError: "", selectionError: "" }] },
};
const subscribers = new Set<() => void>();
const calls: unknown[] = [];
const publish = () => { state = { ...state, revision: state.revision + 1 }; subscribers.forEach(listener => listener()); };
let presentationView = Object.freeze({ runtimeMenu: null as "model" | null, ownerTabId: source, available: true, running: false, turnRunning: false, canSteer: false, readOnly: false, status: "Ready",
  configuration: { provider: "fixture", model: "Model", providers: [] }, queryHistory: [], skillRevision: "one", workspaceCapabilities: { create: false, open: false } });
const empty = Object.freeze([]);
const telemetry = Object.freeze({ tokensPerSecond: 0, source: "none" as const, inputTokens: 0, outputTokens: 0 });
const emptyStatus = Object.freeze({ items: empty });
const presentation: ApplicationComposerPresentationV1 = {
  getStatusSnapshot: () => emptyStatus, subscribeStatus: () => () => {},
  getSnapshot: () => presentationView, subscribe: listener => { subscribers.add(listener); return () => { subscribers.delete(listener); }; },
  setRuntimeMenu: (_ownerTabId, runtimeMenu) => { presentationView = Object.freeze({ ...presentationView, runtimeMenu }); publish(); }, getCommands: () => empty, executeCommand: async input => { calls.push(input); },
  getComposerInlineCompletionProviders: () => empty, subscribeComposerInlineCompletionProviders: () => () => {},
  getTelemetry: () => telemetry, subscribeTelemetry: () => () => {},
};
const environment = base.environment.getSnapshot(), fileSelection = base.fileSelection.getSnapshot({ ownerTabId: source });
const application = { ...base, composerPresentation: presentation, getSnapshot: () => state,
  subscribe: (listener: () => void) => { subscribers.add(listener); return () => { subscribers.delete(listener); }; },
  environment: { ...base.environment, getSnapshot: () => environment }, fileSelection: { ...base.fileSelection, getSnapshot: () => fileSelection },
  catalog: { ...base.catalog, listSkillsForThread: async () => ({ contextKey: "fixture", skills: [] }) },
  invoke: async (action: string, input: any) => {
    calls.push({ action, input });
    if (action === "composer.set-draft") { state = { ...state, drafts: { ...state.drafts, primary: { ...state.drafts.primary, text: input.text } } }; publish(); }
    if (action === "composer.submit") { state = { ...state, drafts: { ...state.drafts, primary: { ...state.drafts.primary, text: "", revision: state.drafts.primary.revision + 1 } } }; publish(); return { accepted: true }; }
    return { accepted: true };
  },
} as ApplicationServiceV1 & { composerPresentation: ApplicationComposerPresentationV1 };
Object.assign(window, { composerAcceptance: { calls, retire: () => { state = { ...state, drafts: { ...state.drafts, primary: { ...state.drafts.primary, ownerTabId: "closed" } } }; publish(); } } });
document.documentElement.style.setProperty("--conversation-message-font-size", "15px");
const host = new PluginHost({ react: React });
await host.activateGeneration({ pluginId: "wuu.default-composer", generation: "fixture", register(api) {
  api.registerUISlot({ id: "composer.above", cardinality: "many" });
  api.registerUISlot({ id: "composer.toolbar", cardinality: "many" });
  api.registerUIContribution({ id: "fixture-tool", title: "Fixture tool", target: "wuu.default-composer/composer.toolbar",
    render: () => <button type="button" className="composer-tool-button" data-public-tool onClick={() => calls.push({ action: "fixture-tool" })}>Tool</button> });
} });
const slots = createUIComposition(host);
createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><main style={{ padding: 24, maxWidth: 850, margin: "360px auto 0" }}><DefaultComposerSurface application={application} slots={slots} variant="hero" /></main><ToastViewport /></WuuUIRoot></I18nProvider>);
