import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createPreviewApplication } from "../../../../packages/plugin-sdk/preview/application";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationComposerPresentationV1 } from "../../../../packages/plugin-sdk/src/ui-composer-presentation";
import { PluginHost } from "../plugins/PluginHost";
import type { HostApplicationService } from "../plugins/HostApplicationService";
import { ApplicationShellOutlet } from "../plugins/ApplicationShell";
import { activate as activateComposer } from "./DefaultComposerSurface";
import { DefaultComposerSurface, type DefaultComposerLayout } from "./DefaultComposerSurface";
import { WuuUIRoot } from "../ui/layers/UILayerHost";

let root: Root;
let container: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); container?.remove(); });
function setup() {
  const base = createPreviewApplication();
  const source = "draft-one";
  let state: ApplicationSnapshotV1 = { ...base.getSnapshot(),
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
  let presentationView = Object.freeze({ runtimeMenu: null as "model" | null, ownerTabId: source, threadId: source, available: true, running: false, turnRunning: false, canSteer: false, readOnly: false, status: "Ready",
    configuration: { provider: "fixture", model: "Model", providers: [] }, queryHistory: [], skillRevision: "one", workspaceCapabilities: { create: false, open: false } });
  const empty = Object.freeze([]);
  const telemetry = Object.freeze({ tokensPerSecond: 0, source: "none" as const, inputTokens: 0, outputTokens: 0 });
  const emptyStatus = Object.freeze({ items: empty });
  const presentation: ApplicationComposerPresentationV1 = {
    getStatusSnapshot: () => emptyStatus, subscribeStatus: () => () => {},
    getSnapshot: () => presentationView, subscribe: listener => { subscribers.add(listener); return () => { subscribers.delete(listener); }; },
    setRuntimeMenu: (_ownerTabId: string, runtimeMenu: "model" | null) => { presentationView = Object.freeze({ ...presentationView, runtimeMenu }); publish(); }, getCommands: () => empty, executeCommand: async input => { calls.push(input); },
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
      if (action === "draft.start") return { tabId: "new-draft" };
      if (action === "tab.new") return { tabId: "recovery-draft" };
      if (action === "composer.set-draft") { state = { ...state, drafts: { ...state.drafts, primary: { ...state.drafts.primary, text: input.text } } }; publish(); }
      if (action === "composer.submit") { state = { ...state, drafts: { ...state.drafts, primary: { ...state.drafts.primary, text: "", revision: state.drafts.primary.revision + 1 } } }; publish(); return { accepted: true }; }
      return { accepted: true };
    },
  } as ApplicationServiceV1;

  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  const render = async (layout: DefaultComposerLayout = {}) => { await act(async () => root.render(<WuuUIRoot><DefaultComposerSurface application={application} slots={{ Slot: () => null }} {...layout} /></WuuUIRoot>)); };
  return { render, calls, application, update: async (change: (view: ApplicationSnapshotV1) => ApplicationSnapshotV1) => { await act(async () => { state = change(state); publish(); }); } };
}
describe("selected public composer continuity", () => {
  it("keeps secondary split input source-bound without adding runtime controls", async () => {
    const fixture = setup();
    await fixture.update(view => ({ ...view,
      tabs: { ...view.tabs, activePane: "secondary", items: [...view.tabs.items, { ...view.tabs.items[0], id: "secondary", threadId: "other", kind: "thread" }] },
      drafts: { ...view.drafts, split: true, activePane: "secondary", secondary: { ...view.drafts.primary, ownerTabId: "secondary", text: "other query" } },
      composerRuntime: { ...view.composerRuntime, owners: [...view.composerRuntime.owners, { ...view.composerRuntime.owners[0], ownerTabId: "secondary", threadId: "other" }] },
    }));
    await fixture.render({ pane: "secondary", ownerTabId: "secondary" });
    expect(container.querySelector(".codex-runtime-trigger")).toBeNull();
    expect(container.querySelector(".permission-chip")).toBeNull();
    const editor = container.querySelector("textarea")!;
    expect(editor.value).toBe("other query");
    await act(async () => editor.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(fixture.calls).toContainEqual(expect.objectContaining({ action: "composer.submit", input: expect.objectContaining({ ownerTabId: "secondary", text: "other query" }) }));
  });
  it("selects, replaces, and disables the side composer through the preview service slot", async () => {
    const fixture = setup();
    await fixture.update(view => ({ ...view,
      tabs: { ...view.tabs, items: view.tabs.items.map(tab => ({ ...tab, kind: "thread", threadId: "parent" })) },
      sideConversation: { ...view.sideConversation, parentThreadId: "parent", entry: { open: true, summary: null, messages: [], draft: "side question", streaming: false } },
    }));
    const publicPresentation = fixture.application.composerPresentation;
    const host = new PluginHost({ react: React, application: { ...fixture.application,
      createComposerPresentationScope: () => publicPresentation,
      createDesktopScope: () => fixture.application.desktop,
    } as unknown as HostApplicationService });
    await host.activateGeneration({ pluginId: "wuu.default-preview", generation: "one", register: api => {
      api.registerUISlot({ id: "application.composer", cardinality: "one", service: "ui.composer" });
      api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name="wuu.default-preview/application.composer" context={{ side: { parentThreadId: "parent" } }} /> });
    } });
    await host.activateGeneration({ pluginId: "composer", manifestId: "wuu.default-composer", generation: "one", register: activateComposer });
    await host.activateGeneration({ pluginId: "side-extension", generation: "one", register: api => {
      api.registerUIContribution({ id: "side", target: "wuu.default-composer/composer.above", render: ({ context }) => <span data-side-context>{String(context.mainConversation)}</span> });
    } });
    const show = async (pluginId: string) => { await act(async () => root.render(<WuuUIRoot><ApplicationShellOutlet host={host}
      binding={{ pluginId: "wuu.default-preview", generation: "one", id: "shell" }}
      surfaceBindings={{ "ui.composer": { pluginId, generation: "one", id: "ui.composer" } }} /></WuuUIRoot>)); };
    await show("composer");
    expect(container.querySelector("textarea")?.value).toBe("side question");
    expect(container.querySelector("[data-side-context]")?.textContent).toBe("false");
    await act(async () => container.querySelector<HTMLButtonElement>('[data-wuu-component="composer-send"]')!.click());
    expect(fixture.calls).toContainEqual({ action: "side.send", input: { parentThreadId: "parent", text: "side question" } });
    await act(async () => { await host.activateGeneration({ pluginId: "alternative", generation: "one", register: api => {
      api.registerUIContribution({ id: "ui.composer", target: "wuu.default-preview/application.composer", render: ({ context }) => <button data-side-replacement>{(context.side as { parentThreadId: string }).parentThreadId}</button> });
    } }); });
    await show("alternative");
    expect(container.querySelector("textarea")).toBeNull();
    expect(container.querySelector("[data-side-replacement]")?.textContent).toBe("parent");
    await act(async () => host.unload("alternative"));
    expect(container.querySelector("textarea")).toBeNull();
    expect(container.querySelector("[role=alert]")?.textContent).toContain("Selected UI surface is unavailable");
    await act(async () => root.render(null));
    host.unload("side-extension"); host.unload("composer"); host.unload("wuu.default-preview");
  });
  it("retains an open runtime picker when its shell remounts", async () => {
    const fixture = setup(); await fixture.render();
    const trigger = container.querySelector<HTMLButtonElement>(".codex-runtime-trigger")!;
    await act(async () => trigger.click());
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    await act(async () => root.render(null));
    await fixture.render();
    expect(trigger.isConnected).toBe(false);
    expect(container.querySelector(".codex-runtime-trigger")?.getAttribute("aria-expanded")).toBe("true");
    await act(async () => container.querySelector<HTMLButtonElement>(".codex-runtime-trigger")!.click());
    expect(container.querySelector(".codex-runtime-trigger")?.getAttribute("aria-expanded")).toBe("false");
  });
  it("requests focus for the draft created by /new", async () => {
    const fixture = setup(); await fixture.render();
    await fixture.update(view => ({ ...view, drafts: { ...view.drafts, primary: { ...view.drafts.primary, text: "/new", revision: 1 } } }));
    await act(async () => container.querySelector("textarea")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(fixture.calls).toContainEqual({ action: "draft.start", input: {} });
    expect(fixture.calls).toContainEqual({ action: "navigation.focus-composer", input: { tabId: "new-draft" } });
  });
  it("preserves the focused input across draft-to-thread binding and sends to the new owner", async () => {
    const fixture = setup(); await fixture.render();
    const editor = container.querySelector("textarea")!; editor.focus();
    await fixture.update(view => ({ ...view,
      tabs: { ...view.tabs, activeTabId: "thread-one", items: view.tabs.items.map(tab => ({ ...tab, id: "thread-one", kind: "thread", threadId: "thread-one" })) },
      drafts: { ...view.drafts, activeTabId: "thread-one", primary: { ...view.drafts.primary, ownerTabId: "thread-one", text: "next query", revision: 1 } },
      composerRuntime: { owners: view.composerRuntime.owners.map(owner => ({ ...owner, ownerTabId: "thread-one" })) },
    }));
    expect(container.querySelector("textarea")).toBe(editor);
    expect(document.activeElement).toBe(editor);
    await act(async () => editor.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(fixture.calls).toContainEqual(expect.objectContaining({ action: "composer.submit", input: expect.objectContaining({ ownerTabId: "thread-one" }) }));
  });
  it("shows submitting while a history edit is awaiting acknowledgement", async () => {
    const fixture = setup(); await fixture.render();
    await fixture.update(view => ({ ...view, tabs: { ...view.tabs, items: view.tabs.items.map(tab => ({ ...tab, kind: "thread", threadId: "draft-one" })) }, conversationActions: { ...view.conversationActions, editing: { threadId: "draft-one", turnId: "turn", itemId: "item", draft: { text: "edited", imageIds: [], fileIds: [] }, submitting: true } } }));
    expect(container.querySelector('[data-wuu-component="composer-send"]')?.getAttribute("data-wuu-state")).toBe("submitting");
    expect(container.querySelector<HTMLButtonElement>(".permission-chip")!.disabled).toBe(true);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-wuu-component="composer-send"]')!.click());
    expect(fixture.calls).toContainEqual({ action: "conversation.interrupt", input: { threadId: "draft-one" } });
    await fixture.update(view => ({ ...view, conversationActions: { ...view.conversationActions, editing: undefined } }));
    expect(container.querySelector('[data-wuu-component="composer-send"]')?.getAttribute("data-wuu-state")).toBe("send");
  });
});
