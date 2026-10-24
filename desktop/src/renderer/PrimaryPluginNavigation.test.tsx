import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { ApplicationSnapshotV1 } from "../../../packages/plugin-sdk/src/ui";
import type { HeaderSnapshotV1, PresentationHost } from "../shared/workbench";
import { ConversationTitleContent } from "./ConversationShellRenderers";
import { createConversationTestApplication } from "./ConversationControlsTestProvider";
import { PluginHost } from "./plugins/PluginHost";
import { ApplicationShellOutlet } from "./plugins/ApplicationShell";
import { translateCurrent as t } from "./i18n";

let container: HTMLDivElement, root: Root | undefined, host: PluginHost;
afterEach(() => { act(() => root?.unmount()); root = undefined; container?.remove(); host?.unload("shell"); host?.unload("header"); });

async function fixture() {
  const base = createConversationTestApplication();
  const thread = { id: "source", title: "Original conversation", preview: "", cwd: "/repo", model_provider: "test", model: "test", status: "idle" as const, created_at: "", updated_at: "", turns: [] };
  let snapshot: ApplicationSnapshotV1 = { ...base.getSnapshot(), tabs: { ...base.getSnapshot().tabs, activeTabId: "source-tab", items: [{ id: "source-tab", kind: "thread", title: thread.title, threadId: thread.id, workspaceRoot: "/repo", project: false, draftText: "Unsent draft", hasAttachments: false }] }, conversationWorkspace: { ...base.getSnapshot().conversationWorkspace, primaryThreadId: thread.id, threads: [thread] }, navigationWorkspace: { ...base.getSnapshot().navigationWorkspace, activeContextKind: "project", selectedWorkspaceId: "project" } };
  const listeners = new Set<() => void>();
  const publish = (next: ApplicationSnapshotV1) => { snapshot = next; listeners.forEach(listener => listener()); };
  const invoke = vi.fn().mockImplementation(async action => { if (action === "workspace.start-draft") return { activated: true, tabId: "created-draft" }; if (action === "navigation.show-native" || action === "navigation.close-plugin") publish({ ...snapshot, navigationWorkspace: { ...snapshot.navigationWorkspace, activePluginView: undefined } }); });
  const application = { ...base, getSnapshot: () => snapshot, subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; }, invoke,
    createDesktopScope: () => base.desktop, setPreviewVisibility: () => {}, createComposerPresentationScope: () => base.composerPresentation };
  host = new PluginHost({ react: React, application, createLegacyViewHost: () => ({ getStorage: async () => null, setStorage: async () => {}, getSetting: async () => undefined, executeCommand: async () => undefined, openView: async () => {}, closeView: async () => {} }) });
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => {
    for (const id of ["conversation.header", "workspace.header"] as const) api.registerUISlot({ id, cardinality: "many" });
    for (const id of ["header.conversation", "header.workspace"] as const) api.registerUISlot({ id, cardinality: "one" });
    api.registerApplicationShell({ id: "shell", render: ({ application, slots }) => <ConversationTitleContent application={application} slots={slots} slotOwner="shell" /> });
  } });
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "one", id: "shell" }} />));
  return { invoke, get: () => snapshot, update: (next: ApplicationSnapshotV1) => act(() => publish(next)) };
}

it("creates a draft in the captured workspace and renames the explicit thread", async () => {
  const h = await fixture();
  await act(async () => container.querySelector<HTMLButtonElement>(".session-tab-new")!.click());
  expect(h.invoke).toHaveBeenCalledWith("workspace.start-draft", { workspaceId: "project" }, { signal: expect.any(AbortSignal) });
  expect(h.invoke.mock.calls.filter(([action]) => action === "navigation.focus-composer")).toEqual([["navigation.focus-composer", { tabId: "created-draft" }, { signal: expect.any(AbortSignal) }]]);
  act(() => container.querySelector<HTMLButtonElement>(".conversation-title-rename")!.click());
  const input = container.querySelector<HTMLInputElement>("input")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "Renamed");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
  expect(h.invoke).toHaveBeenCalledWith("thread.rename", { threadId: "source", title: "Renamed" }, { signal: expect.any(AbortSignal) });
});

it("does not reclaim focus when the user moves it while draft creation is pending", async () => {
  const h = await fixture();
  let finish!: (result: { activated: boolean; tabId: string }) => void;
  h.invoke.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  const button = container.querySelector<HTMLButtonElement>(".session-tab-new")!;
  act(() => { button.focus(); button.click(); });
  act(() => container.querySelector<HTMLButtonElement>(".conversation-title-rename")!.focus());
  await act(async () => finish({ activated: true, tabId: "created-draft" }));
  expect(h.invoke.mock.calls.filter(([action]) => action === "navigation.focus-composer")).toEqual([]);
});

it("does not request composer focus when draft creation did not activate an owner", async () => {
  const h = await fixture();
  h.invoke.mockResolvedValueOnce({ activated: false });
  await act(async () => container.querySelector<HTMLButtonElement>(".session-tab-new")!.click());
  expect(h.invoke.mock.calls.filter(([action]) => action === "navigation.focus-composer")).toEqual([]);
});

it("uses published closeability and restores native heading focus through public navigation", async () => {
  const h = await fixture();
  const native = h.get();
  h.update({ ...native, navigationWorkspace: { ...native.navigationWorkspace, activePluginView: { id: "declared-view", pluginId: "extension", title: "Automation", webPage: false, closable: false } } });
  expect(container.querySelector("h1")?.textContent).toBe("Automation");
  expect(container.querySelector('[data-wuu-component="primary-view-close"]')).toBeNull();
  expect(container.querySelector(".conversation-title-rename")).toBeNull();
  await act(async () => container.querySelector<HTMLButtonElement>('[data-wuu-component="primary-view-back"]')!.click());
  expect(h.invoke).toHaveBeenCalledWith("navigation.show-native", {}, { signal: expect.any(AbortSignal) });
  expect(document.activeElement).toBe(container.querySelector("h1"));
  expect(h.get().tabs.items[0].draftText).toBe("Unsent draft");
  h.update({ ...native, navigationWorkspace: { ...native.navigationWorkspace, activePluginView: { id: "extra-instance", pluginId: "extension", title: "Detail", webPage: false, closable: true } } });
  await act(async () => container.querySelector<HTMLButtonElement>('[data-wuu-component="primary-view-close"]')!.click());
  expect(h.invoke).toHaveBeenCalledWith("navigation.close-plugin", { viewId: "extra-instance" }, { signal: expect.any(AbortSignal) });
  expect(container.querySelector("h1")?.textContent).toBe("Original conversation");
});

it("renders legacy header slots and presenters through the shell's actual composition", async () => {
  const h = await fixture();
  let header: HeaderSnapshotV1 | undefined, presentation: PresentationHost | undefined, context: unknown;
  await act(async () => host.activateGeneration({ pluginId: "header", generation: "one", register: api => {
    api.registerSlot("workspace.header", { id: "before", render: props => { context = props; return <span>Legacy header</span>; } });
    api.registerPresenter({ id: "title", target: "header.workspace", render: props => { header = props.snapshot as HeaderSnapshotV1; presentation = props.host; return <h1>{header.title}</h1>; } });
  } }));
  const current = h.get();
  h.update({ ...current, navigationWorkspace: { ...current.navigationWorkspace, activePluginView: { id: "view", pluginId: "extension", title: "Automation", webPage: false, closable: false } } });
  expect(container.textContent).toContain("Legacy header");
  expect(container.querySelector("h1")?.textContent).toBe("Automation");
  expect(header).toMatchObject({ scope: "workspace", title: "Automation", canNavigateBack: true });
  expect(header?.tabs).toBeUndefined();
  expect(context).toMatchObject({ viewId: "view", threadId: "source", ownerTabId: "source-tab", hasSessionTabs: false, tabCount: 0 });
  expect(presentation?.actions).toContain("header.navigate-back");
  await act(async () => presentation!.invoke("header.navigate-back"));
  expect(h.invoke).toHaveBeenCalledWith("navigation.show-native", expect.any(Object), { signal: expect.any(AbortSignal) });
  expect(container.querySelector("h1")?.textContent).toBe("Original conversation");
  await act(async () => host.unload("header"));
  expect(container.querySelector('button[aria-label="' + t("tabs.newConversation") + '"]')).not.toBeNull();
});

it("replaces the native conversation title with an existing registered header presenter", async () => {
  await fixture();
  let snapshot: HeaderSnapshotV1 | undefined;
  await act(async () => host.activateGeneration({ pluginId: "header", generation: "one", register: api => {
    api.registerPresenter({ id: "conversation", target: "header.conversation", render: props => {
      snapshot = props.snapshot as HeaderSnapshotV1;
      return <div data-conversation-header>{snapshot.title}</div>;
    } });
  } }));
  expect(container.querySelector("[data-conversation-header]")?.textContent).toBe("Original conversation");
  expect(container.querySelector(".conversation-title-heading")).toBeNull();
  expect(snapshot).toMatchObject({ contractVersion: 1, scope: "conversation", title: "Original conversation" });
  await act(async () => host.unload("header"));
  expect(container.querySelector(".conversation-title-heading h1")?.textContent).toBe("Original conversation");
});
