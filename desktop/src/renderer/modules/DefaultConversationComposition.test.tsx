import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { Thread, ThreadItem } from "../../shared/protocol";
import { createConversationTestApplication } from "../ConversationControlsTestProvider";
import { PluginHost } from "../plugins/PluginHost";
import { ApplicationShellOutlet } from "../plugins/ApplicationShell";
import { activate } from "./DefaultConversationSurface";
import { activate as activatePreview, DefaultPreviewSurface } from "./DefaultPreviewSurface";
import * as Notices from "../Toast";
// @ts-expect-error Independently distributable JavaScript plugin; checked against the packed SDK separately.
import { activatePresentation as activateMetrics } from "../../../../examples/plugins/conversation-metrics/desktop.js";
import { PluginLifecycleCancellationError } from "../../shared/PluginLifecycle";
import { activate as activateComposer } from "./DefaultComposerSurface";
import { ApplicationComposerPresentation } from "../plugins/ApplicationComposerPresentation";
import { initialState, type AppState } from "../AppState";
import type { ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import { mockAnimationFrames } from "../AnimationFrameTestHarness";

let root: Root | undefined, container: HTMLDivElement | undefined, host: PluginHost | undefined;
afterEach(() => {
  act(() => root?.unmount()); container?.remove(); root = undefined;
  host?.unload("conversation-metrics"); host?.unload("peer-addon"); host?.unload("broken-addon"); host?.unload("wuu.default-preview"); host?.unload("wuu.default-composer"); host?.unload("side-extension"); host?.unload("alternate-conversation"); host?.unload("wuu.default-conversation"); host?.unload("wuu.default-shell"); host = undefined;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function mountConversation(items: ThreadItem[]) {
  const base = createConversationTestApplication();
  const thread: Thread = { id: "source", title: "Source", preview: "", cwd: "/repo", model_provider: "test", model: "test", status: "idle", created_at: "", updated_at: "", turns: [{ id: "turn", status: "completed", items_view: "full", duration_ms: 1200, input_tokens: 8, output_tokens: 0, items }] };
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
  vi.stubGlobal("wuu", { unsupportedMethods: [] });
  const base = createConversationTestApplication();
  const side = { ...base.getSnapshot().sideConversation, parentThreadId: "parent", entry: { open: true, summary: null, messages: [], draft: "", streaming: false }, turns: [{ id: "side-turn", duration_ms: 0, input_tokens: 3, status: "completed" as const, items_view: "full" as const, items: [user, { id: "side-artifact", type: "tool_call" as const, name: "present_artifact", status: "completed" as const, result_detail: { content: [{ type: "image" as const, mime_type: "image/png", name: "side.png", data: "c2lkZQ==", artifact: { placement: "inline" as const, ref: "side-image" } }] } }, { id: "side-answer", type: "agent_message" as const, text: "Independent side answer", status: "completed" as const, terminal: true }] }] };
  const snapshot: ApplicationSnapshotV1 = { ...base.getSnapshot(), sideConversation: side,
    tabs: { ...base.getSnapshot().tabs, items: [{ ...base.getSnapshot().tabs.items[0], id: "thread:parent", threadId: "parent" }] },
    previewWorkspace: { ...base.getSnapshot().previewWorkspace, tabs: [{ kind: "side-thread", id: "side-thread" }], activeTabId: "side-thread", conversationSelected: false } };
  const parent: Thread = { id: "parent", preview: "", cwd: "/repo", model_provider: "fixture", model: "fixture", status: "idle", created_at: "", updated_at: "", turns: [] };
  const state: AppState = { ...initialState, thread: parent, threads: [parent], activeSessionTabID: "thread:parent",
    sessionTabs: [{ id: "thread:parent", kind: "thread", threadID: "parent", title: "Parent", context: { kind: "no_project", cwd: "/repo" }, prompt: "", images: [], files: [] }] };
  const presentation = new ApplicationComposerPresentation({ getState: () => state, updateState: vi.fn(), subscribeState: () => () => {}, getPluginHost: () => host! });
  const setPreviewVisibility = vi.fn();
  host = new PluginHost({ react: React, application: { ...base, getSnapshot: () => snapshot, setPreviewVisibility,
    catalog: { ...base.catalog, listSkillsForThread: async () => ({ contextKey: "parent", skills: [] }) },
    createDesktopScope: () => base.desktop, createComposerPresentationScope: signal => presentation.createScope(signal) } });
  await host.activateGeneration({ pluginId: "alternate-conversation", generation: "test", register: api => { api.registerUIContribution({ id: "conversation", target: "wuu.default-shell/application.conversation", render: () => <div>Alternate conversation</div> }); } });
  await host.activateGeneration({ pluginId: "wuu.default-preview", generation: "test", register: activatePreview });
  await host.activateGeneration({ pluginId: "wuu.default-composer", generation: "test", register: activateComposer });
  await host.activateGeneration({ pluginId: "conversation-metrics", generation: "test", register: activateMetrics });
  const presented = vi.fn(({ context }: { context: Readonly<Record<string, unknown>> }) => <span data-side-message>{String(context.itemId)}</span>);
  await host.activateGeneration({ pluginId: "side-extension", generation: "test", register: api => { api.registerUIContribution({ id: "side-message", target: "wuu.default-preview/conversation.message.after", render: presented }); } });
  await host.activateGeneration({ pluginId: "wuu.default-shell", generation: "test", register: api => {
    api.registerUISlot({ id: "application.conversation", cardinality: "one", service: "ui.conversation" });
    api.registerUISlot({ id: "application.preview", cardinality: "one", service: "ui.preview" });
    api.registerApplicationShell({ id: "shell", render: ({ slots }) => <><slots.Slot name="wuu.default-shell/application.conversation" /><slots.Slot name="wuu.default-shell/application.preview" context={{ layout: { open: true, present: true, globalized: false, onToggleGlobalize: () => {}, pane: "primary" } }} /></> });
  } });
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<ApplicationShellOutlet host={host!} binding={{ pluginId: "wuu.default-shell", generation: "test", id: "shell" }} surfaceBindings={{ "ui.conversation": { pluginId: "alternate-conversation", generation: "test", id: "conversation" }, "ui.preview": { pluginId: "wuu.default-preview", generation: "test", id: "ui.preview" }, "ui.composer": { pluginId: "wuu.default-composer", generation: "test", id: "ui.composer" } }} />));
  expect(host.getUISlot("wuu.default-conversation/conversation.turn")).toBeUndefined();
  expect(container.textContent).toContain("Alternate conversation");
  expect(container.textContent).toContain("Independent side answer");
  expect(container.querySelector('[data-conversation-metrics-turn="side-turn"]')?.textContent).toBe("0 ms · 3 input tokens");
  await frames.flush();
  const sideComposer = container.querySelector(".side-thread-panel textarea");
  expect(sideComposer).not.toBeNull();
  expect(document.activeElement).toBe(sideComposer);
  expect(setPreviewVisibility).toHaveBeenLastCalledWith(true, expect.any(AbortSignal));
  expect(container.querySelector('img')?.getAttribute("src")).toBe("data:image/png;base64,c2lkZQ==");
  expect(presented.mock.calls.at(-1)?.[0].context).toMatchObject({ threadId: "side:parent", turnId: "side-turn", itemId: "side-answer" });
  await act(async () => host!.unload("alternate-conversation"));
  expect(container.textContent).toContain("Independent side answer");
  expect(container.querySelector('[data-conversation-metrics-turn="side-turn"]')?.textContent).toBe("0 ms · 3 input tokens");
  await act(async () => host!.unload("side-extension"));
  expect(container.querySelector("[data-side-message]")).toBeNull();
  expect(container.textContent).toContain("Independent side answer");
  expect(container.querySelector('[data-conversation-metrics-turn="side-turn"]')?.textContent).toBe("0 ms · 3 input tokens");
  await act(async () => root!.unmount()); root = undefined;
  expect(setPreviewVisibility).toHaveBeenLastCalledWith(false, expect.any(AbortSignal));
  presentation.dispose();
});


it("does not report retired preview cleanup as an error when its presentation stays mounted", async () => {
  const base = createConversationTestApplication();
  const notice = vi.spyOn(Notices, "showActionErrorToast").mockImplementation(() => undefined);
  let retired = false;
  const previous = { ...base, invoke: async () => { if (retired) throw new PluginLifecycleCancellationError(new Error("Rebound provider")); } } as typeof base;
  const next = { ...base, invoke: vi.fn(async () => undefined) } as unknown as typeof base;
  const signal = new AbortController().signal;
  const context = { layout: { open: false, present: false, globalized: false, onToggleGlobalize() {}, pane: "primary" } };
  const slots = { Slot: () => null };
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<DefaultPreviewSurface application={previous} slots={slots} context={context} signal={signal} locale="en" translate={key => key} />));
  retired = true;
  await act(async () => root!.render(<DefaultPreviewSurface application={next} slots={slots} context={context} signal={signal} locale="en" translate={key => key} />));
  expect(signal.aborted).toBe(false);
  expect(next.invoke).toHaveBeenCalledWith("preview.set-visibility", { open: false });
  expect(notice).not.toHaveBeenCalled();
});


// Public addon failure cases: absent slots, wrapper-induced native remount,
// one addon hiding another, mutable payloads, error propagation, and stale UI
// after disable/re-enable. These exercise the selected production surface.
it.each([1, 2])("adds independent turn and tool contributions without remounting native content with %i tool calls", async count => {
  await mountConversation([user,
    ...Array.from({ length: count }, (_, index): ThreadItem => ({ id: `tool-${index}`, type: "tool_call", name: "exec", arguments: '{"command":"echo result"}', result: "reported result", status: "completed" })),
    { id: "answer", type: "agent_message", text: "The answer remains mounted.", status: "completed", terminal: true },
  ]);
  const nativeTurn = container!.querySelector(".turn");
  await act(async () => { container!.querySelector<HTMLButtonElement>(".turn-process-toggle")!.click(); });
  const fold = container!.querySelector<HTMLDetailsElement>(".process-surface-fold")!;
  expect(fold, container!.textContent).not.toBeNull();
  await act(async () => { fold.open = true; fold.dispatchEvent(new Event("toggle")); });
  if (count > 1) {
    await act(async () => {
      const tool = container!.querySelector<HTMLDetailsElement>(".program-tool-record")!;
      tool.open = true; tool.dispatchEvent(new Event("toggle"));
    });
  }
  const nativeDetails = container!.querySelector(".program-tool-details");
  expect(nativeDetails).not.toBeNull();
  await act(async () => { await host!.activateGeneration({ pluginId: "conversation-metrics", generation: "one", register: activateMetrics }); });
  expect(container!.querySelector("[data-conversation-metrics-turn]")?.textContent).toBe("1200 ms · 8 input tokens · 0 output tokens");
  expect(container!.querySelector("[data-conversation-metrics-tool]")).not.toBeNull();
  expect(container!.querySelector(".turn")).toBe(nativeTurn);
  expect(container!.querySelector(".program-tool-details")).toBe(nativeDetails);
  expect(fold.open).toBe(true);
  const action = container!.querySelector<HTMLButtonElement>("[data-conversation-metrics-tool] button")!;
  await act(async () => action.click());
  expect(action.getAttribute("aria-expanded")).toBe("true");
  expect(container!.querySelector("[data-conversation-metrics-tool] pre")?.textContent).toBe("reported result");
  await act(async () => { await host!.activateGeneration({ pluginId: "peer-addon", generation: "one", register: api => {
    for (const target of ["turn", "tool-activity"]) api.registerUIContribution({ id: target, order: 10,
      target: `wuu.default-conversation/conversation.${target}.after`, render: ({ context }) => {
        expect(Object.isFrozen(context)).toBe(true);
        expect(Object.isFrozen(context.snapshot)).toBe(true);
        return <span data-peer={target}>Peer annotation</span>;
      } });
  } }); });
  expect(container!.querySelector('[data-peer="turn"]')!.compareDocumentPosition(container!.querySelector("[data-conversation-metrics-turn]")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(container!.querySelector('[data-peer="tool-activity"]')!.compareDocumentPosition(container!.querySelector("[data-conversation-metrics-tool]")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  const peer = container!.querySelector('[data-peer="turn"]');
  const logging = vi.spyOn(console, "error").mockImplementation(() => {});
  try {
    await act(async () => { await host!.activateGeneration({ pluginId: "broken-addon", generation: "one", register: api => {
      api.registerUIContribution({ id: "broken", target: "wuu.default-conversation/conversation.turn.after", render: () => { throw new Error("Synthetic addon render failure"); } });
    } }); });
    expect(host!.getGenerationDiagnostics("broken-addon", "one").some(row => row.kind === "render")).toBe(true);
    expect(container!.querySelector(".turn")).toBe(nativeTurn);
    expect(container!.querySelector('[data-peer="turn"]')).toBe(peer);
    expect(action.getAttribute("aria-expanded")).toBe("true");
    await act(async () => { host!.unload("conversation-metrics"); });
    expect(container!.querySelector("[data-conversation-metrics-turn]")).toBeNull();
    expect(container!.querySelector("[data-conversation-metrics-tool]")).toBeNull();
    expect(container!.querySelector('[data-peer="turn"]')).toBe(peer);
    expect(container!.querySelector(".turn")).toBe(nativeTurn);
    expect(container!.querySelector(".program-tool-details")).toBe(nativeDetails);
    expect(fold.open).toBe(true);
    await act(async () => { await host!.activateGeneration({ pluginId: "conversation-metrics", generation: "two", register: activateMetrics }); });
    expect(container!.querySelectorAll("[data-conversation-metrics-turn]")).toHaveLength(1);
    expect(container!.querySelector("[data-conversation-metrics-tool] button")?.getAttribute("aria-expanded")).toBe("false");
    expect(container!.querySelector(".program-tool-details")).toBe(nativeDetails);
  } finally { logging.mockRestore(); }
});
