import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { ApplicationSnapshotV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { Thread } from "../../shared/protocol";
import { createConversationTestApplication } from "../ConversationControlsTestProvider";
import { PluginHost } from "../plugins/PluginHost";
import { ApplicationShellOutlet } from "../plugins/ApplicationShell";
import { activate, DefaultConversationSurface } from "./DefaultConversationSurface";

vi.mock("../EmptyHomeOverview", () => ({ EmptyHomeOverview: () => <div data-overview /> }));
let root: Root | undefined, container: HTMLDivElement | undefined;
afterEach(() => { act(() => root?.unmount()); container?.remove(); root = undefined; });
function thread(id: string): Thread { return { id, title: id, preview: "", cwd: "/repo", model_provider: "test", model: "test", status: "idle", created_at: "", updated_at: "", turns: [{ id: `${id}-turn`, status: "completed", items_view: "full", items: [{ id: `${id}-user`, type: "user_message", text: `Question ${id}` }, { id: `${id}-answer`, type: "agent_message", text: `Answer ${id}`, status: "completed", terminal: true }] }] }; }
function fixture() {
  const preview = createConversationTestApplication(), one = thread("one"), two = thread("two");
  // Public histories stay frozen while the selected renderer edits by action.
  const freeze = (value: unknown): void => { if (value && typeof value === "object") { Object.values(value).forEach(freeze); Object.freeze(value); } };
  freeze(one); freeze(two);
  let snapshot: ApplicationSnapshotV1 = { ...preview.getSnapshot(), phase: "ready", conversation: { loading: false, threadId: one.id, turns: [] }, conversationWorkspace: { primaryThreadId: one.id, activePane: "primary", turnStreamStatus: {}, threads: [one, two] } };
  const listeners = new Set<() => void>();
  const invoke = vi.fn().mockResolvedValue({ accepted: true });
  const application = { ...preview, getSnapshot: () => snapshot, subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; }, invoke };
  container = document.createElement("div"); document.body.appendChild(container); root = createRoot(container);
  act(() => root!.render(<DefaultConversationSurface application={application} slots={{ Slot: ({ fallback }) => fallback }} />));
  return { invoke, get: () => snapshot, update: (value: ApplicationSnapshotV1) => act(() => { snapshot = value; listeners.forEach(listener => listener()); }) };
}
it("retains cached panes and routes message editing by explicit public identity", async () => {
  const h = fixture(), first = container!.querySelector('[data-thread-id="one"]');
  expect(container!.textContent).toContain("Answer one");
  h.update({ ...h.get(), conversationWorkspace: { ...h.get().conversationWorkspace, primaryThreadId: "two" } });
  expect(container!.querySelector('[data-thread-id="one"]')).toBe(first);
  expect(container!.querySelector('[data-thread-id="two"]')).not.toBeNull();
  await act(async () => container!.querySelector<HTMLButtonElement>('[data-thread-id="two"] .message-edit-button')!.click());
  expect(h.invoke).toHaveBeenCalledWith("history.begin-edit", { threadId: "two", turnId: "two-turn", itemId: "two-user" });
});
it("preserves split history editing, questions and closing the explicit secondary pane", async () => {
  const h = fixture(); h.update({ ...h.get(), conversationWorkspace: { ...h.get().conversationWorkspace, secondaryThreadId: "two" } });
  expect(container!.querySelectorAll(".conversation-split-pane")).toHaveLength(2);
  await act(async () => container!.querySelector<HTMLButtonElement>('[data-thread-id="two"] .message-edit-button')!.click());
  expect(h.invoke).toHaveBeenCalledWith("history.begin-edit", { threadId: "two", turnId: "two-turn", itemId: "two-user", pane: "secondary" });
  h.update({ ...h.get(), conversationActions: { ...h.get().conversationActions, questions: [{ request_id: "split-question", plugin_id: "ask-user", execution_id: "execution", thread_id: "two", turn_id: "two-turn", created_at: "2026-10-24T00:00:00Z", questions: [{ id: "choice", question: "Which path?", options: [{ label: "Safe" }], allow_custom: true }] }] } });
  expect(container!.querySelector('[data-thread-id="two"] .user-question-card')?.textContent).toContain("Which path?");
  await act(async () => container!.querySelector<HTMLButtonElement>('[data-thread-id="two"] .user-question-cancel')!.click());
  expect(h.invoke).toHaveBeenCalledWith("question.cancel", { requestId: "split-question" });
  await act(async () => container!.querySelector<HTMLButtonElement>(".conversation-split-close")!.click());
  expect(h.invoke).toHaveBeenCalledWith("conversation.close-pane", { pane: "secondary" });
});
it("keeps loading and empty state distinct from a failed runtime", () => {
  const h = fixture();
  h.update({ ...h.get(), phase: "starting", conversation: { ...h.get().conversation, loading: true } });
  expect(container!.querySelector('[data-wuu-component="launch-view"]')).not.toBeNull();
  h.update({ ...h.get(), phase: "ready", conversation: { loading: false, turns: [] }, conversationWorkspace: { activePane: "primary", turnStreamStatus: {}, threads: [] } });
  expect(container!.querySelector("[data-overview]")).not.toBeNull();
  h.update({ ...h.get(), phase: "error", conversation: { loading: false, turns: [], error: "Runtime unavailable" } });
  expect(container!.textContent).toContain("Runtime unavailable");
});
it("starts an explicit history edit before retrying a failed message", async () => {
  const h = fixture(), failed = thread("one");
  failed.turns[0] = { ...failed.turns[0], status: "failed", error: { message: "Request failed: HTTP 500 Internal Server Error" }, items: [failed.turns[0].items[0]] };
  h.update({ ...h.get(), conversationWorkspace: { ...h.get().conversationWorkspace, threads: [failed] } });
  const retry = container!.querySelector<HTMLButtonElement>(".turn-failure-recovery button");
  expect(retry).not.toBeNull();
  await act(async () => retry!.click());
  expect(h.invoke.mock.calls.map(call => call[0])).toEqual(["history.begin-edit", "history.update-edit", "history.submit-edit"]);
  expect(h.invoke).toHaveBeenLastCalledWith("history.submit-edit", { threadId: "one", turnId: "one-turn", itemId: "one-user" });
});
it("renders the canonical pending creation turn only for its tab owner", () => {
  const h = fixture(), pending = thread("pending").turns[0];
  h.update({ ...h.get(), submission: { ...h.get().submission, pendingCreations: [{ ownerTabId: h.get().tabs.activeTabId, operationID: "send", startedAtMs: 1, turn: { ...pending, status: "in_progress" } }] } });
  expect(container!.querySelector('[data-turn-id="pending-turn"]')).not.toBeNull();
  h.update({ ...h.get(), tabs: { ...h.get().tabs, activeTabId: "another-tab" } });
  expect(container!.querySelector('[data-turn-id="pending-turn"]')).toBeNull();
  expect(container!.querySelector('[data-turn-id="one-turn"]')).not.toBeNull();
});

it("uses the selected composer for both split owners and removes it when disabled", async () => {
  const base = createConversationTestApplication(), one = thread("one"), two = thread("two");
  const snapshot: ApplicationSnapshotV1 = { ...base.getSnapshot(), phase: "ready",
    conversationWorkspace: { primaryThreadId: one.id, secondaryThreadId: two.id, activePane: "primary", turnStreamStatus: {}, threads: [one, two] },
    drafts: { ...base.getSnapshot().drafts, split: true, primary: { ...base.getSnapshot().drafts.primary, ownerTabId: "owner-one" }, secondary: { ...base.getSnapshot().drafts.secondary, ownerTabId: "owner-two" } } };
  const application = { ...base, getSnapshot: () => snapshot, createDesktopScope: () => base.desktop,
    setPreviewVisibility: () => {}, createComposerPresentationScope: () => base.composerPresentation };
  const host = new PluginHost({ react: React, application });
  try {
    await host.activateGeneration({ pluginId: "wuu.default-conversation", generation: "test", register: activate });
    await host.activateGeneration({ pluginId: "shell", generation: "test", register: api => {
      api.registerUISlot({ id: "application.conversation", cardinality: "one", service: "ui.conversation" });
      api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name="shell/application.conversation" /> });
    } });
    for (const id of ["first", "replacement"]) await host.activateGeneration({ pluginId: id, generation: "test", register: api => {
      api.registerUIContribution({ id: "composer", target: "shell/application.composer", render: ({ context }) =>
        <div data-selected-composer={id} data-pane={String(context.pane)} data-owner={String(context.ownerTabId)} data-variant={String(context.variant)} /> });
    } });
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    const render = (pluginId: string) => act(async () => root!.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "test", id: "shell" }}
      surfaceBindings={{ "ui.conversation": { pluginId: "wuu.default-conversation", generation: "test", id: "ui.conversation" }, "ui.composer": { pluginId, generation: "test", id: "composer" } }} />));
    await render("first");
    expect([...container.querySelectorAll('[data-selected-composer="first"]')].map(node => [node.getAttribute("data-pane"), node.getAttribute("data-owner"), node.getAttribute("data-variant")])).toEqual([
      ["primary", "owner-one", "dock"], ["secondary", "owner-two", "dock"],
    ]);
    await render("replacement");
    expect(container.querySelectorAll('[data-selected-composer="replacement"]')).toHaveLength(2);
    expect(container.querySelector('[data-selected-composer="first"]')).toBeNull();
    await act(async () => host.unload("replacement"));
    expect(container.querySelectorAll('[role="alert"]')).toHaveLength(2);
    expect(container.querySelector('[data-selected-composer]')).toBeNull();
    expect(container.querySelector("textarea")).toBeNull();
    expect(container.textContent).toContain("Answer one");
    expect(container.textContent).toContain("Answer two");
  } finally {
    act(() => root?.unmount()); root = undefined;
    for (const id of ["first", "replacement", "shell", "wuu.default-conversation"]) host.unload(id);
  }
});
