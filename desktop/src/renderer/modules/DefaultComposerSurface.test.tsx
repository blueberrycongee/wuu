import * as Notices from "../Toast";
import { PluginActionNotStartedError, PluginLifecycleCancellationError } from "../../shared/PluginLifecycle";
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
import { clearToasts, ToastViewport } from "../Toast";
import { WuuUIRoot } from "../ui/layers/UILayerHost";
import { initialState, cloneSessionTabDraft, emptyComposerDraft, type AppState } from "../AppState";
import { ApplicationComposerRuntime } from "../plugins/ApplicationComposerRuntime";
import { ApplicationSubmission } from "../plugins/ApplicationSubmission";
import { ApplicationDraftResources } from "../plugins/ApplicationDrafts";
import { createComposerPendingState } from "../ComposerPendingState";
import { createSidebarThreadCache } from "../SidebarWorkspaceState";
import type { EngineListResult, InitializeResult, ThreadStartParams, WuuDesktopApi } from "../../shared/protocol";


let root: Root;
let container: HTMLDivElement;
afterEach(() => { act(() => clearToasts()); act(() => root?.unmount()); container?.remove(); vi.restoreAllMocks(); });
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
  const render = async (layout: DefaultComposerLayout = {}, selected = application) => { await act(async () => root.render(<WuuUIRoot><ToastViewport /><DefaultComposerSurface application={selected} slots={{ Slot: () => null }} {...layout} /></WuuUIRoot>)); };
  return { render, calls, application, update: async (change: (view: ApplicationSnapshotV1) => ApplicationSnapshotV1) => { await act(async () => { state = change(state); publish(); }); } };
}
describe("selected public composer continuity", () => {
  // Composer-owned effects must work without the default shell, respect the
  // source pane, and stop when this provider is no longer mounted.
  it.each(["primary", "secondary"] as const)("accepts response selections for its %s source without shell DOM conventions", async pane => {
    const fixture = setup();
    await fixture.update(view => ({ ...view,
      conversationWorkspace: { ...view.conversationWorkspace, [pane === "primary" ? "primaryThreadId" : "secondaryThreadId"]: "source-thread" },
      drafts: { ...view.drafts, split: pane === "secondary", [pane]: view.drafts.primary },
    }));
    await fixture.render({ pane });
    const selection = { id: "quote", text: "Answer", source: { thread_id: "source-thread", turn_id: "turn", item_id: "item", start_offset: 0, end_offset: 6 } };
    await act(async () => window.dispatchEvent(new CustomEvent("wuu:add-response-selection", { detail: selection })));
    expect(fixture.calls).toContainEqual({ action: "composer.checkpoint", input: expect.objectContaining({
      [pane]: expect.objectContaining({ ownerTabId: "draft-one", selections: [selection] }),
    }) });
    const count = fixture.calls.length;
    await act(async () => window.dispatchEvent(new CustomEvent("wuu:add-response-selection", { detail: { ...selection, source: { ...selection.source, thread_id: "other" } } })));
    expect(fixture.calls).toHaveLength(count);
  });

  it("refreshes its models on window focus only while mounted and settled", async () => {
    const fixture = setup();
    await fixture.render();
    fixture.calls.length = 0;
    await act(async () => window.dispatchEvent(new Event("focus")));
    expect(fixture.calls).toContainEqual({ action: "composer.refresh-engine-models", input: expect.objectContaining({ ownerTabId: "draft-one" }) });
    await act(async () => root.render(<div>Replacement composer</div>));
    fixture.calls.length = 0;
    await act(async () => window.dispatchEvent(new Event("focus")));
    expect(fixture.calls).toEqual([]);
  });

  it.each(["steer", "queue"] as const)("retries a %s only when retirement prevented it from starting", async mode => {
    const fixture = setup();
    const presentation = { ...fixture.application.composerPresentation.getSnapshot("draft-one"), running: true, turnRunning: true, canSteer: true };
    vi.spyOn(fixture.application.composerPresentation, "getSnapshot").mockReturnValue(presentation);
    await fixture.update(view => ({ ...view, drafts: { ...view.drafts, primary: { ...view.drafts.primary, text: "Follow up" } } }));
    const retired = { ...fixture.application, invoke: async (action: string, input: unknown) => {
      if (action === "composer.submit") throw new PluginActionNotStartedError();
      return (fixture.application.invoke as (action: string, input: unknown) => Promise<unknown>)(action, input);
    } } as ApplicationServiceV1;
    await fixture.render({}, retired);
    const editor = container.querySelector("textarea")!;
    await act(async () => editor.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", metaKey: mode === "queue", bubbles: true })));
    const invoke = vi.fn(fixture.application.invoke);
    await fixture.render({}, { ...fixture.application, invoke });
    const submits = invoke.mock.calls.filter(([action]) => action === "composer.submit");
    expect(submits).toHaveLength(1);
    expect(submits[0][1]).toMatchObject({ ownerTabId: "draft-one", mode, text: "Follow up" });
  });

  it.each(["owner", "text", "attachments", "already started"] as const)("does not replay a retired submission after %s changes", async changed => {
    const fixture = setup();
    const presentation = { ...fixture.application.composerPresentation.getSnapshot("draft-one"), running: true, turnRunning: true, canSteer: true };
    vi.spyOn(fixture.application.composerPresentation, "getSnapshot").mockReturnValue(presentation);
    await fixture.update(view => ({ ...view, drafts: { ...view.drafts, primary: { ...view.drafts.primary, text: "Original input" } } }));
    const retired = { ...fixture.application, invoke: async (action: string, input: unknown) => {
      if (action === "composer.submit") throw changed === "already started" ? new PluginLifecycleCancellationError() : new PluginActionNotStartedError();
      return (fixture.application.invoke as (action: string, input: unknown) => Promise<unknown>)(action, input);
    } } as ApplicationServiceV1;
    await fixture.render({}, retired);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-wuu-component="composer-send"]')!.click());
    if (changed === "text") {
      const editor = container.querySelector("textarea")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(editor, "Newer input");
        editor.dispatchEvent(new Event("input", { bubbles: true }));
      });
    }
    if (changed === "attachments") await fixture.update(view => ({ ...view, drafts: { ...view.drafts, primary: { ...view.drafts.primary, fileIds: ["new-file"] } } }));
    if (changed === "owner") await fixture.update(view => ({ ...view,
      tabs: { ...view.tabs, activeTabId: "other", items: [{ ...view.tabs.items[0], id: "other" }] },
      drafts: { ...view.drafts, activeTabId: "other", primary: { ...view.drafts.primary, ownerTabId: "other" } },
      composerRuntime: { owners: [{ ...view.composerRuntime.owners[0], ownerTabId: "other" }] },
    }));
    const invoke = vi.fn(fixture.application.invoke);
    await fixture.render({}, { ...fixture.application, invoke });
    expect(invoke.mock.calls.filter(([action]) => action === "composer.submit")).toHaveLength(0);
  });

  it.each(["queue", "steer"] as const)("announces a rejected %s and keeps its draft", async mode => {
    const fixture = setup();
    await fixture.update(view => ({ ...view, drafts: { ...view.drafts, primary: { ...view.drafts.primary, text: "Retained" } }, submission: { ...view.submission,
      operations: [{ operationID: "failed", messageID: "failed", ownerTabId: "draft-one", mode, phase: "sending" }],
    } }));
    await fixture.render();
    await fixture.update(view => ({ ...view, submission: { ...view.submission,
      operations: [{ ...view.submission.operations[0], phase: "failed", error: "Delivery failed" }],
    } }));
    expect(container.querySelector('[data-wuu-component="notice"]')?.getAttribute("role")).toBe("alert");
    expect(container.querySelector("textarea")?.value).toBe("Retained");
  });

  it("saves the latest text entered while its application provider is retiring", async () => {
    const fixture = setup();
    const retired = { ...fixture.application, invoke: async (action: string, input: unknown) => {
      if (action === "composer.set-draft") throw new PluginLifecycleCancellationError();
      return (fixture.application.invoke as (action: string, input: unknown) => Promise<unknown>)(action, input);
    } } as ApplicationServiceV1;
    const invoke = vi.fn(fixture.application.invoke);
    const successor = { ...fixture.application, invoke } as ApplicationServiceV1;
    await fixture.render({}, retired);
    const editor = container.querySelector("textarea")!;
    for (const text of ["first edit", "latest edit"]) await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(editor, text);
      editor.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await fixture.render({}, successor);
    expect(invoke).toHaveBeenCalledWith("composer.set-draft", { ownerTabId: "draft-one", text: "latest edit" });
    expect(container.querySelector("textarea")!.value).toBe("latest edit");
  });

  it.each(["owner", "workspace"])("does not carry an unsaved edit into a different %s", async changed => {
    const fixture = setup();
    const retired = { ...fixture.application, invoke: async (action: string, input: unknown) => {
      if (action === "composer.set-draft") throw new PluginLifecycleCancellationError();
      return (fixture.application.invoke as (action: string, input: unknown) => Promise<unknown>)(action, input);
    } } as ApplicationServiceV1;
    await fixture.render({}, retired);
    const editor = container.querySelector("textarea")!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(editor, "private draft");
      editor.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const nextOwner = changed === "owner" ? "draft-two" : "draft-one";
    await fixture.update(view => ({ ...view,
      tabs: { ...view.tabs, activeTabId: nextOwner, items: [{ ...view.tabs.items[0], id: nextOwner, workspaceRoot: changed === "workspace" ? "/other" : "/synthetic" }] },
      drafts: { ...view.drafts, activeTabId: nextOwner, primary: { ...view.drafts.primary, ownerTabId: nextOwner, text: "", revision: 1 } },
      composerRuntime: { owners: [{ ...view.composerRuntime.owners[0], ownerTabId: nextOwner }] },
    }));
    const invoke = vi.fn(fixture.application.invoke);
    await fixture.render({}, { ...fixture.application, invoke });
    expect(invoke.mock.calls.some(([action, input]) => action === "composer.set-draft" && (input as { text?: string }).text === "private draft")).toBe(false);
  });

  it("announces a failed send once, retains input, and clears its notice when retry starts", async () => {
    const toast = vi.spyOn(Notices, "showActionErrorToast");
    const fixture = setup();
    const operation = { operationID: "send-one", messageID: "send-one", ownerTabId: "draft-one", mode: "send" as const, phase: "sending" as const, draftPending: true };
    await fixture.update(view => ({ ...view, drafts: { ...view.drafts, primary: { ...view.drafts.primary, text: "Retained draft" } }, submission: { ...view.submission, operations: [operation] } }));
    await fixture.render();
    const editor = container.querySelector("textarea")!;
    expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
    expect(container.querySelector('[data-wuu-component="composer-send"]')?.getAttribute("data-wuu-state")).toBe("submitting");
    const error = "Error invoking remote method 'wuu:turn-start': Error: plugin generation refresh is waiting to retry";
    await fixture.update(view => ({ ...view, submission: { ...view.submission, operations: [{ ...operation, phase: "failed", draftPending: false, error }] } }));
    const notice = container.querySelector('[data-wuu-component="notice"]');
    expect(notice?.getAttribute("role")).toBe("alert");
    expect(notice?.textContent).not.toMatch(/wuu:|plugin generation|Error invoking/);
    expect(editor.value).toBe("Retained draft");
    expect(editor.disabled).toBe(false);
    expect(container.querySelector('[data-wuu-component="composer-send"]')?.getAttribute("data-wuu-state")).toBe("send");
    await fixture.update(view => ({ ...view }));
    expect(container.querySelector('[data-wuu-component="notice"]')).toBe(notice);
    await act(async () => notice!.querySelector<HTMLButtonElement>(".archive-tip-action")!.click());
    expect(fixture.calls.filter(call => (call as { action: string }).action === "composer.submit")).toHaveLength(1);
    await fixture.update(view => ({ ...view, submission: { ...view.submission, operations: [{ ...operation, operationID: "retry", messageID: "retry" }] } }));
    expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
    expect(toast).not.toHaveBeenCalled();
  });

  it.each(["preparing", "sending", "completed", "cancelled"] as const)("does not turn %s or unrelated operations into a global error", async phase => {
    const fixture = setup();
    const operation = { operationID: "operation", messageID: "operation", ownerTabId: "draft-one", mode: "send" as const, phase };
    await fixture.update(view => ({ ...view, submission: { ...view.submission, operations: [operation, { ...operation, operationID: "other", messageID: "other", ownerTabId: "other", phase: "failed", error: "Other conversation" }] } }));
    await fixture.render();
    expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
  });

  it("announces rejection when thread binding and first-send failure settle together", async () => {
    const fixture = setup();
    await fixture.render();
    const presentation = { ...fixture.application.composerPresentation.getSnapshot("draft-one"), threadId: "created" };
    vi.spyOn(fixture.application.composerPresentation, "getSnapshot").mockReturnValue(presentation);
    await fixture.update(view => ({ ...view,
      tabs: { ...view.tabs, activeTabId: "thread:created", items: [{ ...view.tabs.items[0], id: "thread:created", kind: "thread", threadId: "created" }] },
      drafts: { ...view.drafts, activeTabId: "thread:created", primary: { ...view.drafts.primary, ownerTabId: "thread:created", text: "First message" } },
      composerRuntime: { owners: [{ ...view.composerRuntime.owners[0], ownerTabId: "thread:created", threadId: "created" }] },
      submission: { ...view.submission, operations: [{ operationID: "failed", messageID: "failed", ownerTabId: "draft-one", threadId: "created", mode: "send", phase: "failed", error: "Unavailable" }] },
    }));
    expect(container.querySelector("textarea")?.value).toBe("First message");
    expect(container.querySelector('[data-wuu-component="notice"]')).not.toBeNull();
  });

  it("retires a send failure notice on navigation without replaying it on return", async () => {
    const fixture = setup();
    await fixture.render();
    await fixture.update(view => ({ ...view, submission: { ...view.submission, operations: [{ operationID: "failed", messageID: "failed", ownerTabId: "draft-one", mode: "send", phase: "failed", error: "Unavailable" }] } }));
    expect(container.querySelector('[data-wuu-component="notice"]')).not.toBeNull();
    const select = (ownerTabId: string) => fixture.update(view => ({ ...view,
      tabs: { ...view.tabs, activeTabId: ownerTabId, items: [{ ...view.tabs.items[0], id: ownerTabId }] },
      drafts: { ...view.drafts, activeTabId: ownerTabId, primary: { ...view.drafts.primary, ownerTabId } },
      composerRuntime: { owners: [{ ...view.composerRuntime.owners[0], ownerTabId }] },
    }));
    await select("other");
    expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
    await select("draft-one");
    expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
    await act(async () => root.render(null));
    await fixture.render();
    expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
  });

  it("expires send feedback without recreating it on state updates, but announces a new failure", async () => {
    vi.useFakeTimers();
    try {
      const fixture = setup();
      await fixture.render();
      const fail = (id: string) => fixture.update(view => ({ ...view, submission: { ...view.submission,
        operations: [{ operationID: id, messageID: id, ownerTabId: "draft-one", mode: "send", phase: "failed", error: "Unavailable" }],
      } }));
      await fail("first");
      expect(container.querySelector('[data-wuu-component="notice"]')).not.toBeNull();
      await act(async () => vi.advanceTimersByTime(6000));
      expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
      await fixture.update(view => ({ ...view }));
      expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
      await fail("second");
      expect(container.querySelector('[data-wuu-component="notice"]')).not.toBeNull();
      await fixture.update(view => ({ ...view, submission: { ...view.submission,
        operations: view.submission.operations.map(row => ({ ...row, phase: "completed", error: undefined })),
      } }));
      expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
    } finally { vi.useRealTimers(); }
  });

  it("keeps recovered drafts beside the composer without offering destructive notice dismissal", async () => {
    const fixture = setup();
    await fixture.update(view => ({ ...view, submission: { ...view.submission, failedDraftTabIds: ["draft-one"] } }));
    await fixture.render();
    const recovery = container.querySelector('[data-wuu-component="draft-recovery"]');
    expect(recovery).not.toBeNull();
    expect(container.querySelector<HTMLElement>(".composer")?.hidden).toBe(false);
    expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
    const actions = recovery!.querySelectorAll<HTMLButtonElement>("button");
    expect(actions).toHaveLength(1);
    await act(async () => actions[0].click());
    expect(fixture.calls).toContainEqual({action:"tab.select",input:{tabId:"draft-one"}});
    expect(fixture.calls.some((call: any) => call.action === "tab.close")).toBe(false);
  });

  it("does not duplicate the separate recovered-draft notice", async () => {
    const fixture = setup();
    await fixture.update(view => ({ ...view, submission: { ...view.submission,
      failedDraftTabIds: ["recovered"], operations: [{ operationID: "failed", messageID: "failed", ownerTabId: "draft-one", mode: "send", phase: "failed", error: "Unavailable" }],
    } }));
    await fixture.render();
    expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
  });

  it("closes the workspace picker before a provider-rebinding navigation settles", async () => {
    const fixture = setup();
    const presentation = { ...fixture.application.composerPresentation.getSnapshot("draft-one"), threadId: undefined };
    vi.spyOn(fixture.application.composerPresentation, "getSnapshot").mockReturnValue(presentation);
    await fixture.update(view => ({ ...view, workspaces: { workspaces: [{ id: "second", name: "Second workspace", root: "/second", available: true }] } }));
    let reject!: (error: unknown) => void;
    const original = fixture.application.invoke;
    vi.spyOn(fixture.application, "invoke").mockImplementation((action, input, options) => action === "workspace.retarget-draft"
      ? new Promise((_resolve, rejectRequest) => { reject = rejectRequest; }) : original(action, input, options));
    await fixture.render();
    const editor = container.querySelector("textarea")!;
    await act(async () => container.querySelector<HTMLButtonElement>('.composer-project-control button[aria-haspopup="menu"]')!.click());
    expect(document.querySelector(".composer-project-menu")).not.toBeNull();
    await act(async () => document.querySelector<HTMLButtonElement>('.composer-project-menu button[role="menuitemradio"][aria-label="Second workspace"]')!.click());
    expect(document.querySelector(".composer-project-menu")).toBeNull();
    expect(container.querySelector("textarea")).toBe(editor);
    await act(async () => reject(new PluginLifecycleCancellationError(new Error("Provider replaced"))));
    expect(document.querySelector(".composer-project-menu")).toBeNull();
  });

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
    const notice = vi.spyOn(Notices, "showErrorToast");
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
    expect(container.querySelector("[role=alert]")).toBeNull();
    expect(container.querySelector('[role="status"]')).not.toBeNull();
    expect(notice).not.toHaveBeenCalled();
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
    expect(container.querySelector<HTMLButtonElement>(".permission-menu-anchor button")!.disabled).toBe(true);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-wuu-component="composer-send"]')!.click());
    expect(fixture.calls).toContainEqual({ action: "conversation.interrupt", input: { threadId: "draft-one" } });
    await fixture.update(view => ({ ...view, conversationActions: { ...view.conversationActions, editing: undefined } }));
    expect(container.querySelector('[data-wuu-component="composer-send"]')?.getAttribute("data-wuu-state")).toBe("send");
  });
});


it("does not toast a retired composer refresh after switching to the replacement owner", async () => {
  const notice = vi.spyOn(Notices, "showActionErrorToast").mockImplementation(() => undefined);
  const fixture = setup();
  let reject!: (error: unknown) => void;
  const invoke = fixture.application.invoke;
  vi.spyOn(fixture.application, "invoke").mockImplementation((action, input) => action === "composer.refresh-engine-models" && "ownerTabId" in input && input.ownerTabId === "draft-one"
    ? new Promise((_resolve, rejectRequest) => { reject = rejectRequest; })
    : invoke(action, input));
  await fixture.render();
  await fixture.update(view => ({ ...view,
    tabs: { ...view.tabs, activeTabId: "replacement", items: [{ ...view.tabs.items[0], id: "replacement" }] },
    drafts: { ...view.drafts, activeTabId: "replacement", primary: { ...view.drafts.primary, ownerTabId: "replacement", text: "Replacement draft", revision: 1 } },
    composerRuntime: { owners: [{ ...view.composerRuntime.owners[0], ownerTabId: "replacement" }] },
  }));
  await act(async () => reject(new PluginLifecycleCancellationError(new DOMException("signal is aborted without reason", "AbortError"))));
  expect(container.querySelector("textarea")?.value).toBe("Replacement draft");
  expect(notice).not.toHaveBeenCalled();
});

it("waits for a confirmed repository before adding new Git chrome without blocking composer input", async () => {
  const fixture = setup();
  const initial = fixture.application.environment.getSnapshot();
  let environment = { ...initial, root: "/unknown", contextKey: "unknown", actionBusyPending: true,
    capabilities: { ...initial.capabilities, checkout: true }, status: undefined as typeof initial.status };
  vi.spyOn(fixture.application.environment, "getSnapshot").mockImplementation(() => environment);
  await fixture.render();
  expect(container.querySelector(".composer-git-controls")).toBeNull();
  expect(container.querySelector("textarea")!.disabled).toBe(false);
  environment = { ...environment, status: { is_repo: true, branch: "discovered", dirty_count: 0 } };
  await fixture.update(view => ({ ...view }));
  const branch = container.querySelector<HTMLButtonElement>(".composer-git-controls button");
  expect(branch).not.toBeNull();
  expect(branch!.closest("[inert]")).not.toBeNull();
  environment = { ...environment, actionBusyPending: false, status: { is_repo: false, dirty_count: 0 } };
  await fixture.update(view => ({ ...view }));
  expect(container.querySelector(".composer-git-controls")).toBeNull();
});

it.each([false, true])("keeps non-Git rail topology while status renews (different target=%s)", async differentTarget => {
  const fixture = setup();
  const initial = fixture.application.environment.getSnapshot();
  let environment = { ...initial, root: "/source-non-git", contextKey: "source-non-git", actionBusyPending: false,
    status: { is_repo: false, dirty_count: 0 } as typeof initial.status };
  vi.spyOn(fixture.application.environment, "getSnapshot").mockImplementation(() => environment);
  const rail = document.createElement("nav");
  await fixture.render({ runtimeControlsTarget: rail });
  container.append(rail);
  const controls = [...rail.querySelectorAll("button")];
  const editor = container.querySelector("textarea")!;
  expect(controls.length).toBeGreaterThan(0);
  expect(rail.querySelector(".composer-git-controls")).toBeNull();
  await fixture.update(view => ({ ...view, tabs: { ...view.tabs, pending: { kind: "tab", targetId: "next" } } }));
  environment = { ...environment, ...(differentTarget ? { root: "/destination-non-git", contextKey: "destination-non-git" } : {}),
    actionBusyPending: true, status: undefined };
  await fixture.update(view => ({ ...view, tabs: { ...view.tabs, pending: undefined } }));
  expect([...rail.querySelectorAll("button")]).toEqual(controls);
  expect(rail.querySelector(".composer-git-controls")).toBeNull();
  expect(container.querySelector("textarea")).toBe(editor);
  expect(editor.disabled).toBe(false);
  environment = { ...environment, actionBusyPending: false, status: { is_repo: false, dirty_count: 0 } };
  await fixture.update(view => ({ ...view }));
  expect([...rail.querySelectorAll("button")]).toEqual(controls);
  expect(rail.querySelector(".composer-git-controls")).toBeNull();
});

it("keeps runtime control appearance while blocking interactions during navigation", async () => {
  const fixture = setup();
  const initialEnvironment = fixture.application.environment.getSnapshot();
  let environment = { ...initialEnvironment, contextKey: "source", root: "/source", actionBusyPending: false, capabilities: { ...initialEnvironment.capabilities, checkout: true },
    status: { is_repo: true, branch: "main", branches: ["main"], dirty_count: 0 } as typeof initialEnvironment.status };
  vi.spyOn(fixture.application.environment, "getSnapshot").mockImplementation(() => environment);
  const rail = document.createElement("nav"); container.append(rail);
  await fixture.render({ runtimeControlsTarget: rail });
  // The test portal is independent from the root's composer container.
  container.append(rail);
  const model = rail.querySelector<HTMLButtonElement>(".codex-runtime-trigger")!;
  const permission = rail.querySelector<HTMLButtonElement>(".permission-rail-button")!;
  expect(model.disabled).toBe(false); expect(permission.disabled).toBe(false);
  const branch = rail.querySelector<HTMLButtonElement>(".composer-git-controls button")!;
  expect(branch.disabled).toBe(false);
  await fixture.update(view => ({ ...view, tabs: { ...view.tabs, pending: { kind: "tab", targetId: "next" } }, composerRuntime: { owners: view.composerRuntime.owners.map(owner => ({ ...owner, busy: true })) } }));
  expect(rail.querySelector(".codex-runtime-trigger")).toBe(model);
  expect(model.disabled).toBe(false); expect(permission.disabled).toBe(false);
  expect(model.closest("[inert]")).not.toBeNull();
  await act(async () => { model.click(); permission.click(); });
  expect(document.querySelector("[data-floating-menu-owner=codex-runtime]")).toBeNull();
  expect(document.querySelector("[data-floating-menu-owner=composer-access]")).toBeNull();
  environment = { ...environment, contextKey: "destination", root: "/destination", actionBusyPending: true, status: undefined };
  await fixture.update(view => ({ ...view, tabs: { ...view.tabs, pending: undefined }, composerRuntime: { owners: view.composerRuntime.owners.map(owner => ({ ...owner, busy: false })) } }));
  expect(model.closest("[inert]")).toBeNull();
  expect(rail.querySelector(".composer-git-controls button")).toBe(branch);
  expect(branch.disabled).toBe(false); expect(branch.closest("[inert]")).not.toBeNull();
  expect(branch.getAttribute("aria-label")).not.toContain("main");
  await act(async () => branch.click());
  expect(document.querySelector("[data-floating-menu-owner=composer-branch]")).toBeNull();
  environment = { ...environment, actionBusyPending: false, status: { is_repo: true, branch: "destination", branches: ["destination"], dirty_count: 0 } };
  await fixture.update(view => ({ ...view }));
  expect(branch.closest("[inert]")).toBeNull();
  expect(branch.getAttribute("aria-label")).toContain("destination");
  await act(async () => model.click());
  expect(document.querySelector("[data-floating-menu-owner=codex-runtime]")).not.toBeNull();
});

it.each([false, true])("locks navigation without changing the session's readonly semantics (split=%s)", async split => {
  const fixture = setup();
  if (split) await fixture.update(view => ({ ...view, drafts: { ...view.drafts, split: true } }));
  await fixture.render();
  const editor = container.querySelector("textarea")!;
  const placeholder = editor.placeholder;
  await fixture.update(view => ({ ...view, tabs: { ...view.tabs, pending: { kind: "tab", targetId: "next", contentReady: false } } }));
  expect(editor.disabled).toBe(true);
  expect(editor.placeholder).toBe(placeholder);
  await fixture.update(view => ({ ...view, tabs: { ...view.tabs, pending: { kind: "tab", targetId: "next", contentReady: true } } }));
  expect(editor.disabled).toBe(true);
  await fixture.update(view => ({ ...view, tabs: { ...view.tabs, pending: undefined } }));
  expect(editor.disabled).toBe(false);
  expect(editor.placeholder).toBe(placeholder);
  const readonly = { ...fixture.application.composerPresentation.getSnapshot("draft-one"), readOnly: true };
  vi.spyOn(fixture.application.composerPresentation, "getSnapshot").mockReturnValue(readonly);
  await fixture.update(view => ({ ...view, tabs: { ...view.tabs, pending: undefined } }));
  expect(editor.disabled).toBe(true);
  expect(editor.placeholder).not.toBe(placeholder);
});

it("scopes unconfirmed delivery to its conversation and clears the drawer when its authoritative rows resolve", async () => {
  const fixture = setup();
  const first = fixture.application.composerPresentation.getSnapshot("draft-one");
  const second = Object.freeze({ ...first, ownerTabId: "thread-b", threadId: "thread-b", running: true, turnRunning: true });
  vi.spyOn(fixture.application.composerPresentation, "getSnapshot").mockImplementation(owner => owner === "thread-b" ? second : first);
  const row = { id: "unknown-a", text: "Only conversation A", preview: "Only conversation A", fullPreview: "Only conversation A", imageIds: [], fileIds: [], held: true, deliveryUnconfirmed: true };
  await fixture.update(view => ({ ...view, composerPending: [{ threadId: "draft-one", queued: [row], guides: [] }],
    tabs: { ...view.tabs, items: [...view.tabs.items, { ...view.tabs.items[0], id: "thread-b", kind: "thread", threadId: "thread-b" }] },
    composerRuntime: { owners: [...view.composerRuntime.owners, { ...view.composerRuntime.owners[0], ownerTabId: "thread-b", threadId: "thread-b" }] },
  }));
  await fixture.render();
  expect(container.querySelector('[data-wuu-component="composer-pending"]')).not.toBeNull();
  expect(container.textContent).toContain(row.text);
  const select = async (id: string) => fixture.update(view => ({ ...view,
    tabs: { ...view.tabs, activeTabId: id }, drafts: { ...view.drafts, activeTabId: id, primary: { ...view.drafts.primary, ownerTabId: id } },
  }));
  await select("thread-b");
  expect(container.querySelector('[data-wuu-component="composer-pending"]')).toBeNull();
  expect(container.textContent).not.toContain(row.text);
  await select("draft-one");
  const pending = container.querySelector('[data-wuu-component="composer-pending"]');
  expect(pending).not.toBeNull();
  // Returning to an unresolved owner preserves the data; disclosure remains usable.
  if (!container.querySelector(".composer-queue-row")) await act(async () => pending!.querySelector<HTMLButtonElement>("button")!.click());
  expect(container.textContent).toContain(row.text);
  await fixture.update(view => ({ ...view, composerPending: [{ threadId: "draft-one", queued: [], guides: [] }] }));
  expect(container.querySelector('[data-wuu-component="composer-pending"]')).toBeNull();
});

// Invoked by the packed-SDK minimal acceptance runner with a real app-server.
// Only unrelated presentation services use the preview fixture: the default
// surface, runtime selection, submission, backend inventory and turns are real.
it.skipIf(!process.env.WUU_MINIMAL_GUI_ENDPOINT)("sends through the default composer using the selected real engine capability", async () => {
  const endpoint = process.env.WUU_MINIMAL_GUI_ENDPOINT!;
  const request = async <T,>(method: string, params: unknown = {}): Promise<T> => {
    const response = await fetch(endpoint, { method: "POST", body: JSON.stringify({ method, params }) });
    const result = await response.json() as { result: T; error?: string };
    if (result.error) throw new Error(result.error);
    return result.result;
  };
  const initialized = await request<InitializeResult>("fixture/initialize");
  expect(initialized.providers ?? []).toEqual([]);
  const context = { kind: "no_project" as const, cwd: initialized.workspace_root };
  const source = "draft-one";
  const draft = (prompt: string) => ({ id: source, kind: "draft" as const, context, title: "", createdAt: 1, prompt, images: [], files: [] });
  let state: AppState = { ...initialState, initialized, activeContext: context, activeSessionTabID: source, sessionTabs: [draft("default composer hello")] };
  const oldAPI = window.wuu;
  const starts: ThreadStartParams[] = [];
  window.wuu = {
    listEngines: () => request<EngineListResult>("engine/list"),
    startThread: (params = {}) => { starts.push(params); return request("thread/start", params); },
    startTurn: (thread_id, prompt, _images, _files, permission_mode, _document, content_parts, _context, client_id) => request("turn/start", { thread_id, prompt, permission_mode, content_parts, client_id }),
  } as WuuDesktopApi;
  const runtime = new ApplicationComposerRuntime({ getState: () => state, updateState: change => { state = typeof change === "function" ? change(state) : change; }, changed: () => {}, isNavigationPending: () => false });
  const getDraft = (id: string) => { const tab = state.sessionTabs.find(tab => tab.id === id); return tab ? cloneSessionTabDraft(tab) : emptyComposerDraft(); };
  const setDraft = (id: string, value: ReturnType<typeof emptyComposerDraft>) => { state = { ...state, sessionTabs: state.sessionTabs.map(tab => tab.id === id && tab.kind !== "skills" ? { ...tab, ...value } : tab) }; };
  const pending = createComposerPendingState({ getAppState: () => state, getComposerDraftForThread: getDraft, restoreComposerDraftForThread: setDraft, preserveFailedComposerMessage: () => {}, setStatus: () => {} });
  const resources = new ApplicationDraftResources(() => {});
  const submission = new ApplicationSubmission({ getState: () => state, updateState: change => { state = change(state); }, changed: () => {}, isNavigationPending: () => false,
    getDraft, setDraft, pending, resources, getRuntime: owner => runtime.getSubmissionRuntime(owner), sidebarCache: createSidebarThreadCache(() => [], () => {}) });
  const fixture = setup();
  const originalInvoke = fixture.application.invoke;
  const results: { accepted: boolean }[] = [];
  vi.spyOn(fixture.application, "invoke").mockImplementation(async (action, input, options) => {
    if (action === "composer.refresh-engine-models") return runtime.invoke(action, input as { ownerTabId: string; force?: boolean }) as never;
    if (action === "composer.submit") { const result = submission.submit(input as Parameters<ApplicationSubmission["submit"]>[0]); results.push(result); return result as never; }
    return originalInvoke(action, input, options);
  });
  const presentation = { ...fixture.application.composerPresentation.getSnapshot(source), threadId: undefined, configuration: initialized };
  vi.spyOn(fixture.application.composerPresentation, "getSnapshot").mockReturnValue(presentation);
  const project = async () => fixture.update(view => ({ ...view,
    tabs: { ...view.tabs, items: [{ ...view.tabs.items[0], title: "", workspaceRoot: context.cwd }] },
    drafts: { ...view.drafts, primary: { ...view.drafts.primary, text: getDraft(source).prompt } }, composerRuntime: runtime.getSnapshot(),
  }));
  try {
    await runtime.invoke("composer.refresh-engine-models", { ownerTabId: source });
    expect(runtime.getSubmissionRuntime(source)).toMatchObject({ pending: false, requiresHostModel: false, params: { engine: "wuu", model: "" } });
    await project(); await fixture.render();
    const editor = container.querySelector("textarea")!;
    await act(async () => editor.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(results).toEqual([expect.objectContaining({ accepted: true })]);
    await vi.waitFor(() => expect(submission.getSnapshot().operations[0]?.phase).toBe("completed"));
    expect(starts).toHaveLength(1);
    expect(starts[0]).toMatchObject({ engine: "wuu", model: "" });
    expect(starts[0]).not.toHaveProperty("provider");
    const completed = await request<{ method: string; params: unknown }>("fixture/terminal", { turn: state.thread!.turns.at(-1) });
    expect(completed.method).toBe("turn/completed");
    expect(JSON.stringify(completed)).toContain("first:default composer hello");

    // Rebind the selected provider while a fresh draft and an earlier backend
    // run are alive. The same owner must observe the new requirement, not fall
    // back to another engine or clear the input.
    state = { ...state, thread: undefined, activeSessionTabID: source, sessionTabs: [draft("retained across replacement")] };
    await project();
    const replacement = await request<Pick<InitializeResult, "extension_inventory">>("fixture/promote");
    state = { ...state, initialized: { ...initialized, extension_inventory: replacement.extension_inventory } };
    expect(runtime.getSubmissionRuntime(source).pending).toBe(true);
    await project();
    await vi.waitFor(() => expect(runtime.getSubmissionRuntime(source)).toMatchObject({ pending: false, requiresHostModel: true, params: { engine: "wuu" } }));
    await project();
    expect(container.querySelector("textarea")).toBe(editor);
    expect(editor.value).toBe("retained across replacement");
    await act(async () => editor.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(results.at(-1)).toEqual({ accepted: false });
    expect(starts).toHaveLength(1);
    expect(getDraft(source).prompt).toBe("retained across replacement");
  } finally { submission.dispose(); runtime.dispose(); window.wuu = oldAPI; }
}, 30_000);
