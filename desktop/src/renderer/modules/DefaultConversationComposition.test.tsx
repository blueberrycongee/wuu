import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { Thread, ThreadItem } from "../../shared/protocol";
import { createConversationTestApplication } from "../ConversationControlsTestProvider";
import { PluginHost } from "../plugins/PluginHost";
import { ApplicationShellOutlet } from "../plugins/ApplicationShell";
import { activate } from "./DefaultConversationSurface";
import { activate as activatePreview } from "./DefaultPreviewSurface";
import type { ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import { mockAnimationFrames } from "../AnimationFrameTestHarness";

let root: Root | undefined, container: HTMLDivElement | undefined, host: PluginHost | undefined;
afterEach(() => {
  act(() => root?.unmount()); container?.remove(); root = undefined;
  host?.unload("wuu.default-preview"); host?.unload("side-extension"); host?.unload("alternate-conversation"); host?.unload("wuu.default-conversation"); host?.unload("wuu.default-shell"); host = undefined;
  vi.restoreAllMocks();
});

async function mountConversation(items: ThreadItem[]) {
  const base = createConversationTestApplication();
  const thread: Thread = { id: "source", title: "Source", preview: "", cwd: "/repo", model_provider: "test", model: "test", status: "idle", created_at: "", updated_at: "", turns: [{ id: "turn", status: "completed", items_view: "full", items }] };
  const snapshot = { ...base.getSnapshot(), phase: "ready" as const, conversation: { loading: false, threadId: thread.id, turns: [] }, conversationWorkspace: { ...base.getSnapshot().conversationWorkspace, primaryThreadId: thread.id, threads: [thread] } };
  const openURL = vi.fn().mockResolvedValue(undefined);
  const openFile = vi.fn().mockResolvedValue(undefined);
  const resolveResourceURL = vi.fn(() => "https://assets.example.test/diagram.png");
  const application = { ...base, getSnapshot: () => snapshot, createDesktopScope: () => base.desktop,
    setPreviewVisibility: vi.fn(), createComposerPresentationScope: () => base.composerPresentation,
    browser: { ...base.browser, openURL }, previewContent: { ...base.previewContent, openFile, resolveResourceURL } };
  host = new PluginHost({ react: React, application });
  await host.activateGeneration({ pluginId: "wuu.default-conversation", generation: "test", register: activate });
  await host.activateGeneration({ pluginId: "wuu.default-shell", generation: "test", register: api => {
    api.registerUISlot({ id: "application.conversation", cardinality: "one", service: "ui.conversation" });
    api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name="wuu.default-shell/application.conversation" /> });
  } });
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<ApplicationShellOutlet host={host!} binding={{ pluginId: "wuu.default-shell", generation: "test", id: "shell" }} surfaceBindings={{ "ui.conversation": { pluginId: "wuu.default-conversation", generation: "test", id: "ui.conversation" } }} />));
  return { openURL, openFile, resolveResourceURL };
}
const user: ThreadItem = { id: "user", type: "user_message", text: "Show the result", status: "completed" };

it("renders a delivered artifact through the selected surface's declared fallback", async () => {
  await mountConversation([user, { id: "artifact", type: "tool_call", name: "present_artifact", status: "completed", result_detail: { content: [{ type: "image", mime_type: "image/png", name: "plot.png", data: "aW1hZ2U=", artifact: { ref: "plot", placement: "inline", sha256: "image" } }] } }, { id: "answer", type: "agent_message", text: "Here is the plot", status: "completed", terminal: true }]);
  expect(container!.querySelector('img')?.getAttribute("src"), container!.textContent).toBe("data:image/png;base64,aW1hZ2U=");
});

it("opens Markdown web links with the selected generation's public browser service", async () => {
  const h = await mountConversation([user, { id: "answer", type: "agent_message", text: "[Visit](https://example.test)", status: "completed", terminal: true }]);
  const anchor = container!.querySelector<HTMLAnchorElement>("a.rich-web-link");
  expect(anchor, container!.textContent).not.toBeNull();
  await act(async () => anchor!.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true })));
  expect(h.openURL).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ url: "https://example.test/" }), { signal: expect.any(AbortSignal) });
});

it("resolves Markdown images through the selected generation's public file service", async () => {
  const h = await mountConversation([user, { id: "answer", type: "agent_message", text: "![Diagram](./diagram.png)", status: "completed", terminal: true }]);
  expect(h.resolveResourceURL).toHaveBeenCalledWith({ source: "./diagram.png", cwd: "/repo" });
  expect(container!.querySelector('img')?.getAttribute("src")).toBe("https://assets.example.test/diagram.png");
});

it("keeps preview-owned side turns and artifacts when an alternate conversation is selected", async () => {
  const frames = mockAnimationFrames();
  const base = createConversationTestApplication();
  const side = { ...base.getSnapshot().sideConversation, parentThreadId: "parent", entry: { open: true, summary: null, messages: [], draft: "", streaming: false }, turns: [{ id: "side-turn", status: "completed" as const, items_view: "full" as const, items: [user, { id: "side-artifact", type: "tool_call" as const, name: "present_artifact", status: "completed" as const, result_detail: { content: [{ type: "image" as const, mime_type: "image/png", name: "side.png", data: "c2lkZQ==", artifact: { placement: "inline" as const, ref: "side-image" } }] } }, { id: "side-answer", type: "agent_message" as const, text: "Independent side answer", status: "completed" as const, terminal: true }] }] };
  const snapshot: ApplicationSnapshotV1 = { ...base.getSnapshot(), sideConversation: side,
    previewWorkspace: { ...base.getSnapshot().previewWorkspace, tabs: [{ kind: "side-thread", id: "side-thread" }], activeTabId: "side-thread", conversationSelected: false } };
  const setPreviewVisibility = vi.fn();
  host = new PluginHost({ react: React, application: { ...base, getSnapshot: () => snapshot, setPreviewVisibility,
    createDesktopScope: () => base.desktop, createComposerPresentationScope: () => base.composerPresentation } });
  await host.activateGeneration({ pluginId: "alternate-conversation", generation: "test", register: api => { api.registerUIContribution({ id: "conversation", target: "wuu.default-shell/application.conversation", render: () => <div>Alternate conversation</div> }); } });
  await host.activateGeneration({ pluginId: "wuu.default-preview", generation: "test", register: activatePreview });
  const presented = vi.fn(({ context }: { context: Readonly<Record<string, unknown>> }) => <span data-side-message>{String(context.itemId)}</span>);
  await host.activateGeneration({ pluginId: "side-extension", generation: "test", register: api => { api.registerUIContribution({ id: "side-message", target: "wuu.default-preview/conversation.message.after", render: presented }); } });
  await host.activateGeneration({ pluginId: "wuu.default-shell", generation: "test", register: api => {
    api.registerUISlot({ id: "application.conversation", cardinality: "one", service: "ui.conversation" });
    api.registerUISlot({ id: "application.preview", cardinality: "one", service: "ui.preview" });
    api.registerApplicationShell({ id: "shell", render: ({ slots }) => <><slots.Slot name="wuu.default-shell/application.conversation" /><slots.Slot name="wuu.default-shell/application.preview" context={{ layout: { open: true, present: true, globalized: false, onToggleGlobalize: () => {}, pane: "primary" } }} /></> });
  } });
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<ApplicationShellOutlet host={host!} binding={{ pluginId: "wuu.default-shell", generation: "test", id: "shell" }} surfaceBindings={{ "ui.conversation": { pluginId: "alternate-conversation", generation: "test", id: "conversation" }, "ui.preview": { pluginId: "wuu.default-preview", generation: "test", id: "ui.preview" } }} />));
  expect(host.getUISlot("wuu.default-conversation/conversation.turn")).toBeUndefined();
  expect(container.textContent).toContain("Alternate conversation");
  expect(container.textContent).toContain("Independent side answer");
  await frames.flush();
  const sideComposer = container.querySelector(".side-thread-panel textarea");
  expect(sideComposer).not.toBeNull();
  expect(document.activeElement).toBe(sideComposer);
  expect(setPreviewVisibility).toHaveBeenLastCalledWith(true, expect.any(AbortSignal));
  expect(container.querySelector('img')?.getAttribute("src")).toBe("data:image/png;base64,c2lkZQ==");
  expect(presented.mock.calls.at(-1)?.[0].context).toMatchObject({ threadId: "side:parent", turnId: "side-turn", itemId: "side-answer" });
  await act(async () => host!.unload("alternate-conversation"));
  expect(container.textContent).toContain("Independent side answer");
  await act(async () => host!.unload("side-extension"));
  expect(container.querySelector("[data-side-message]")).toBeNull();
  expect(container.textContent).toContain("Independent side answer");
  await act(async () => root!.unmount()); root = undefined;
  expect(setPreviewVisibility).toHaveBeenLastCalledWith(false, expect.any(AbortSignal));
});
