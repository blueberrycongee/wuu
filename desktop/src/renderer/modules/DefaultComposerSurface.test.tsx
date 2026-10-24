import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { createPreviewApplication } from "../../../../packages/plugin-sdk/preview/application";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationComposerPresentationV1 } from "../../../../packages/plugin-sdk/src/ui-composer-presentation";
import { DefaultComposerSurface } from "./DefaultComposerSurface";
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
  const presentationView = Object.freeze({ ownerTabId: source, threadId: source, available: true, running: false, turnRunning: false, canSteer: false, readOnly: false, status: "Ready",
    configuration: { provider: "fixture", model: "Model", providers: [] }, queryHistory: [], skillRevision: "one", workspaceCapabilities: { create: false, open: false } });
  const empty = Object.freeze([]);
  const telemetry = Object.freeze({ tokensPerSecond: 0, source: "none" as const, inputTokens: 0, outputTokens: 0 });
  const emptyStatus = Object.freeze({ items: empty });
  const presentation: ApplicationComposerPresentationV1 = {
    getStatusSnapshot: () => emptyStatus, subscribeStatus: () => () => {},
    getSnapshot: () => presentationView, subscribe: () => () => {}, getCommands: () => empty, executeCommand: async input => { calls.push(input); },
    getComposerInlineCompletionProviders: () => empty, subscribeComposerInlineCompletionProviders: () => () => {}, getContributions: () => empty,
    getTelemetry: () => telemetry, subscribeTelemetry: () => () => {},
  };
  const environment = base.environment.getSnapshot(), fileSelection = base.fileSelection.getSnapshot({ ownerTabId: source });
  const application = { ...base, composerPresentation: presentation, getSnapshot: () => state,
    subscribe: (listener: () => void) => { subscribers.add(listener); return () => { subscribers.delete(listener); }; },
    environment: { ...base.environment, getSnapshot: () => environment }, fileSelection: { ...base.fileSelection, getSnapshot: () => fileSelection },
    catalog: { ...base.catalog, listSkillsForThread: async () => ({ contextKey: "fixture", skills: [] }) },
    invoke: async (action: string, input: any) => {
      calls.push({ action, input });
      if (action === "tab.new") return { tabId: "new-draft" };
      if (action === "composer.set-draft") { state = { ...state, drafts: { ...state.drafts, primary: { ...state.drafts.primary, text: input.text } } }; publish(); }
      if (action === "composer.submit") { state = { ...state, drafts: { ...state.drafts, primary: { ...state.drafts.primary, text: "", revision: state.drafts.primary.revision + 1 } } }; publish(); return { accepted: true }; }
      return { accepted: true };
    },
  } as ApplicationServiceV1;

  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  const render = async () => { await act(async () => root.render(<WuuUIRoot><DefaultComposerSurface application={application} /></WuuUIRoot>)); };
  return { render, calls, application, update: async (change: (view: ApplicationSnapshotV1) => ApplicationSnapshotV1) => { await act(async () => { state = change(state); publish(); }); } };
}
describe("selected public composer continuity", () => {
  it("requests focus for the draft created by /new", async () => {
    const fixture = setup(); await fixture.render();
    await fixture.update(view => ({ ...view, drafts: { ...view.drafts, primary: { ...view.drafts.primary, text: "/new", revision: 1 } } }));
    await act(async () => container.querySelector("textarea")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
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
    await fixture.update(view => ({ ...view, conversationActions: { ...view.conversationActions, editing: { threadId: "draft-one", turnId: "turn", itemId: "item", draft: { text: "edited", imageIds: [], fileIds: [] }, submitting: true } } }));
    expect(container.querySelector('[data-wuu-component="composer-send"]')?.getAttribute("data-wuu-state")).toBe("submitting");
    await fixture.update(view => ({ ...view, conversationActions: { ...view.conversationActions, editing: undefined } }));
    expect(container.querySelector('[data-wuu-component="composer-send"]')?.getAttribute("data-wuu-state")).toBe("send");
  });
});
