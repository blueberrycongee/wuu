import { webcrypto } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { desktopPluginHost } from "./DesktopPluginRuntime";
import { captureReadingAnchor, readingAnchorScrollTop } from "../ConversationReadingAnchor";
import { PluginConversationAttachment, PluginConversationComposer, PluginConversationMessage, PluginConversationView } from "./PluginConversation";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { PluginViewContent, WorkbenchController } from "./Workbench";
import { PersonalGenUISurface } from "./personal-genui/PersonalGenUISurface";
import { fixtures as genuiFixtures } from "../../../../plugins/personal/src/genui/fixtures";
import { initialState as genuiInitialState } from "../../../../plugins/personal/src/genui/schema";
import type { ActionResult, ActionRequest } from "../../../../plugins/personal/src/genui/service";
const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/personal/desktop.js"), "utf8");
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
const main = { agent_id: "a-main", user_space_id: "local", name: "Mosaic", kind: "main", main_session_id: "s-private", status: "active", revision: 2 };
const cleanups: Array<() => void> = [];
beforeEach(() => { window.localStorage.clear(); });
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.restoreAllMocks(); });
async function mount(mutate?: (method: string, input: any) => Promise<unknown>, mainAgent: typeof main & Record<string, unknown> = main, historyRecords?: unknown[], fixtures: { calendarView?: boolean; tasks?: unknown[]; memories?: unknown[]; open?: () => Promise<unknown>; history?: (input: any) => Promise<unknown>; historySearch?: boolean } = {}) {
  const invoke = vi.fn<NonNullable<PluginHostOptions["invokeRuntime"]>>(async ({ method, input }) => {
    if (method === "agents.ensure_default") return { agent: mainAgent };
    if (method === "agents.list") return { agents: [mainAgent], default_agent_id: mainAgent.agent_id };
    if (method === "agents.open") return fixtures.open?.() ?? { agent: mainAgent, session: { state: "completed" } };
    if (method === "agents.history") {
      if (fixtures.history) return fixtures.history(input);
      const records = historyRecords ?? [{ seq: 1, role: "assistant", content: `Hello ${(input as any).agent_id}` }];
      if (input && typeof input === "object" && "reply_seq" in input) {
        const roots = new Map<number, number>();
        for (const row of records as Array<{ seq: number; relation?: { reply_to?: { seq: number } } }>) roots.set(row.seq, roots.get(row.relation?.reply_to?.seq ?? 0) ?? row.seq);
        const root = roots.get(Number(input.reply_seq));
        return { reply_root_seq: root, records: records.filter(row => roots.get((row as { seq: number }).seq) === root), has_earlier: false };
      }
      return { records };
    }
    if (method === "agents.proposals") return { proposals: [] };
    if (method === "tasks.list") return { tasks: fixtures.tasks ?? [{ task_id: "t1", agent_id: "a-travel", title: "Plan a trip", goal: "Find a route", state: "running", revision: 3, requirements_revision: 1 }] };
    if (method === "memory.query") return { records: fixtures.memories ?? [] };
    if (method === "settings.get") return { settings: { revision: 1, notifications: true, quiet_hours: { enabled: false, start: "22:00", end: "08:00", timezone: "UTC" } } };
    return (await mutate?.(method, input)) ?? {};
  });
  // Personal hides search; search-contract fixtures opt into the shared surface without changing product defaults.
  const host = new PluginHost({ react: React, invokeRuntime: invoke, conversationComponents: { Composer: PluginConversationComposer, Message: PluginConversationMessage, View: fixtures.historySearch ? props => <PluginConversationView {...props} showHistorySearch /> : PluginConversationView, Attachment: PluginConversationAttachment } });
  await host.activateGeneration({ pluginId: "personal", generation: "one", register: activate });
  if (fixtures.calendarView) await host.activateGeneration({ pluginId: "personal-calendar", generation: "one", register: api => { api.registerViewType({ id: "personal-calendar.confirmation", title: "Calendar confirmation", defaultRegion: "overlay", persistence: "session", render: () => null }); } });
  const controller = new WorkbenchController(host);
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  let disposed = false;
  const dispose = () => { if (disposed) return; disposed = true; act(() => root.unmount()); controller.dispose(); host.unload("personal"); host.unload("personal-calendar"); container.remove(); };
  cleanups.push(dispose);
  await act(async () => root.render(<PluginViewContent controller={controller} pluginId="personal" viewTypeId="personal.home" />));
  const click = async (selector: string, scope: ParentNode = container) => { const button = (scope === container && selector.startsWith(".composer-") ? container.querySelector(".mosaic-replies") ?? scope : scope).querySelector<HTMLButtonElement>(selector); expect(button).not.toBeNull(); await act(async () => button!.click()); };
  const edit = async (value: string) => { const node = container.querySelector<HTMLTextAreaElement>(".mosaic-replies textarea") ?? container.querySelector("textarea")!; await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(node, value); node.dispatchEvent(new Event("input", { bubbles: true })); }); };
  return { container, invoke, click, edit, controller, dispose, publish: (event: unknown = { type: "routine-state-changed" }) => host.publishHostEvent(event), remount: async (between?: () => void) => { await act(async () => root.render(null)); await act(async () => between?.()); await act(async () => root.render(<PluginViewContent controller={controller} pluginId="personal" viewTypeId="personal.home" />)); }, settings: async () => { await act(async () => root.render(<PluginViewContent controller={controller} pluginId="personal" viewTypeId="personal.preferences" />)); }, activateEntry: () => controller.openPluginView("personal", "personal.home") };
}
it("preserves unsent text when sending fails and never substitutes a session ID for an agent", async () => {
  const ui = await mount(async method => { if (method === "agents.send") throw new Error("Host offline"); return {}; });
  await ui.edit("Remember this"); await ui.click('.composer-send-button');
  expect(ui.container.querySelector("textarea")!.value).toBe("Remember this");
  expect(ui.container.querySelector('[role="alert"]')?.textContent).toContain("Host offline");
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.send", input: expect.objectContaining({ agent_id: "a-main", prompt: "Remember this", request_id: expect.any(String) }) }));
});
it("shows a sent reply excerpt on the user message without repeating it on the assistant response", async () => {
  const relation = { reply_to: { session_id: "s-private", seq: 1 } };
  const ui = await mount(undefined, main, [{ seq: 1, role: "assistant", content: "Original source" }, { seq: 2, role: "user", content: "My follow-up", relation }, { seq: 3, role: "assistant", content: "Answer", relation }]);
  expect(ui.container.querySelector('[data-reply-reference]')?.textContent).toBe("Original source");
  expect(ui.container.querySelectorAll('[data-reply-reference]')).toHaveLength(1);
  expect(ui.container.querySelector('[data-reply-reference]')?.closest('[data-role="user"]')?.querySelector('.user-message')?.textContent).toContain("My follow-up");
  expect(ui.container.querySelector('[data-role="assistant"] [data-reply-reference]')).toBeNull();
  expect(ui.container.querySelector('[data-reply-reference]')?.closest('.bubble-message-content')).toBeNull();
  expect(ui.container.querySelector('[data-role="user"] [data-action="reply-message"]')).toBeNull();
  expect(ui.container.querySelectorAll('[data-role="assistant"] [data-action="reply-message"]')).toHaveLength(2);
  const reply = ui.container.querySelector<HTMLButtonElement>('[data-action="reply-message"]')!;
  expect(reply.getAttribute('aria-label')).toBeTruthy();
});
it("loads missing quoted sources once by exact scoped sequence and never renders foreign or tool sources", async () => {
  const relation = (session: string, seq: number) => ({ reply_to: { session_id: session, seq } });
  const rows = [{ seq: 20, role: "user", content: "First", relation: relation("s-private", 3) }, { seq: 21, role: "assistant", content: "Second", relation: relation("s-private", 3) }, { seq: 22, role: "user", content: "Foreign", relation: relation("other-session", 4) }, { seq: 23, role: "user", content: "Unavailable", relation: relation("s-private", 5) }];
  const history = vi.fn(async input => input.start_seq === 3 ? { records: [{ seq: 3, role: "assistant", content: "Older verified source" }] } : input.start_seq === 5 ? { records: [{ seq: 5, role: "tool", content: "Private tool body" }, { seq: 6, role: "assistant", content: "Wrong message" }] } : { records: rows });
  const ui = await mount(undefined, main, undefined, { history });
  await vi.waitFor(() => expect([...ui.container.querySelectorAll('[data-reply-reference]')].filter(node => node.textContent === "Older verified source")).toHaveLength(1));
  expect(history.mock.calls.filter(([input]) => input.start_seq === 3)).toHaveLength(1);
  expect(history).toHaveBeenCalledWith({ agent_id: "a-main", start_seq: 3, end_seq: 3 });
  expect(history.mock.calls.some(([input]) => input.start_seq === 4)).toBe(false);
  expect(ui.container.textContent).not.toContain("Private tool body"); expect(ui.container.textContent).not.toContain("Wrong message");
  expect(ui.container.querySelectorAll('[data-reply-reference][data-unavailable="true"]')).toHaveLength(2);
});
it("ignores an older source lookup after the displayed reply reference changes", async () => {
  let finish!: (value: unknown) => void;
  const pending = new Promise(resolve => { finish = resolve; });
  let source = 3;
  const ui = await mount(undefined, main, undefined, { history: async input => input.start_seq === 3 ? pending : input.start_seq === 4 ? { records: [{ seq: 4, role: "assistant", content: "Current source" }] } : { records: [{ seq: 20, role: "user", content: "Follow-up", relation: { reply_to: { session_id: "s-private", seq: source } } }] } });
  source = 4; await act(async () => ui.publish());
  await vi.waitFor(() => expect(ui.container.querySelector('[data-reply-reference]')?.textContent).toBe("Current source"));
  await act(async () => { finish({ records: [{ seq: 3, role: "assistant", content: "Stale source" }] }); await pending; });
  expect(ui.container.querySelector('[data-reply-reference]')?.textContent).toBe("Current source");
  expect(ui.container.textContent).not.toContain("Stale source");
});
it("keeps independent reply targets retryable with their drafts and files across close and reopen", async () => {
  attachmentBrowser();
  const link = { task_id: "t1", requirements_revision: 1, execution_session_id: "execution-one" };
  const ui = await mount(async method => { if (method === "agents.send") throw new Error("Lost admission response"); }, main,
    [{ seq: 7, role: "assistant", content: "First result", relation: { metadata: { task_links: [link] } } }, { seq: 8, role: "assistant", content: "Second result" }]);
  await ui.edit("Unrelated main draft");
  await chooseFiles(ui.container, [new File(["hello"], "main.txt", { type: "text/plain" })]);
  await vi.waitFor(() => expect(ui.container.querySelector(".composer-attachment-tray")).not.toBeNull());
  await ui.click('[data-action="reply-message"]'); await ui.edit("Please adjust this");
  expect(ui.container.querySelector('.mosaic-replies .composer-attachment-tray')).toBeNull();
  await chooseFiles(ui.container, [new File(["hello"], "reply.txt", { type: "text/plain" })]);
  await vi.waitFor(() => expect(ui.container.querySelector(".mosaic-replies .composer-attachment-tray")).not.toBeNull());
  await ui.click('.composer-send-button'); await ui.click('[data-action="close-replies"]'); await ui.remount(); await ui.click('[data-action="reply-message"]'); await ui.click('.composer-send-button');
  let sends = ui.invoke.mock.calls.filter(([request]) => request.method === "agents.send");
  expect(sends[0][0].input).toEqual(expect.objectContaining({ relation: { reply_to: { session_id: "s-private", seq: 7 }, metadata: { task_links: [link] } } }));
  expect(sends[1][0].input).toEqual(sends[0][0].input);
  expect(sends[0][0].userInput).toEqual(expect.objectContaining({ relation: (sends[0][0].input as any).relation }));
  await ui.click('[data-action="close-replies"]');
  await act(async () => (ui.container.querySelectorAll<HTMLButtonElement>('.mosaic-main [data-action="reply-message"]')[1]).click());
  expect(ui.container.querySelector<HTMLTextAreaElement>('.mosaic-replies textarea')!.value).toBe("");
  await ui.edit("Second follow-up"); await ui.click('.composer-send-button');
  sends = ui.invoke.mock.calls.filter(([request]) => request.method === "agents.send");
  expect((sends[2][0].input as any).request_id).not.toBe((sends[0][0].input as any).request_id);
  expect((sends[2][0].input as any).relation).toEqual({ reply_to: { session_id: "s-private", seq: 8 } });
  await ui.click('[data-action="close-replies"]');
  expect(ui.container.querySelector('textarea')!.value).toBe("Unrelated main draft");
  expect(ui.container.querySelector(".composer-attachment-tray")?.textContent).toContain("main.txt");
  await ui.click('[data-action="reply-message"]');
  expect(ui.container.querySelector<HTMLTextAreaElement>('.mosaic-replies textarea')!.value).toBe("Please adjust this");
  expect(ui.container.querySelector('.mosaic-replies .composer-attachment-tray')?.textContent).toContain("reply.txt");
});
it("keeps a newer task reply draft isolated when an earlier message reply settles", async () => {
  let finish!: (value: unknown) => void;
  const pending = new Promise(resolve => { finish = resolve; });
  const ui = await mount(async method => method === "agents.send" ? pending : {});
  await ui.edit("Main draft");
  await ui.click('[data-action="reply-message"]'); await ui.edit("First request"); await ui.click('.composer-send-button');
  await ui.click('[data-action="close-replies"]'); await ui.click('[data-task-id="t1"]'); await ui.click('[data-action="reply-task"]'); await ui.edit("New task draft");
  await act(async () => { finish({ state: "accepted" }); await pending; });
  expect(ui.container.querySelector<HTMLTextAreaElement>('.mosaic-replies textarea')!.value).toBe("New task draft");
  expect(ui.container.querySelector('.mosaic-replies')?.textContent).toContain("Plan a trip");
  await ui.click('.composer-send-button');
  const sends = ui.invoke.mock.calls.filter(([request]) => request.method === "agents.send");
  expect((sends[1][0].input as any).relation).toEqual({ metadata: { task_links: [{ task_id: "t1", requirements_revision: 1 }] } });
  await ui.click('[data-action="close-replies"]'); expect(ui.container.querySelector('textarea')!.value).toBe("Main draft");
});
it("shows a verified task source once while replying to the current task revision", async () => {
  const task = { task_id: "t1", title: "Task A", goal: "Current requirements", state: "running", revision: 4, requirements_revision: 2, source: { session_id: "s-private", message_seq: 1, message_binding: "host_turn" }, execution: { session_id: "execution-two" } };
  const ui = await mount(undefined, main, [{ seq: 1, role: "user", content: "Original request", relation: { metadata: { task_links: [{ task_id: "t1", requirements_revision: 1 }] } } }], { tasks: [task] });
  await ui.click('[data-task-id="t1"]'); await ui.click('[data-action="reply-task"]');
  expect(ui.container.querySelectorAll('.mosaic-replies [data-role="user"]')).toHaveLength(1);
  await ui.edit("Update the task"); await ui.click('.composer-send-button');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.send", input: expect.objectContaining({ relation: { metadata: { task_links: [{ task_id: "t1", requirements_revision: 2, execution_session_id: "execution-two" }] } } }) }));
});
it("does not surface an earlier reply failure in a different target's panel", async () => {
  let rejectSend!: (error: Error) => void;
  const pending = new Promise((_resolve, reject) => { rejectSend = reject; });
  const ui = await mount(async method => method === "agents.send" ? pending : {});
  await ui.click('[data-action="reply-message"]'); await ui.edit("First request"); await ui.click('.composer-send-button');
  await ui.click('[data-action="close-replies"]'); await ui.click('[data-task-id="t1"]'); await ui.click('[data-action="reply-task"]'); await ui.edit("Task draft");
  await act(async () => { rejectSend(new Error("Only first reply failed")); await pending.catch(() => {}); });
  expect(ui.container.querySelector('.mosaic-replies [role="alert"]')).toBeNull();
  expect(ui.container.querySelector<HTMLTextAreaElement>('.mosaic-replies textarea')!.value).toBe("Task draft");
  await ui.click('[data-action="close-replies"]'); await ui.click('[data-action="reply-message"]');
  expect(ui.container.querySelector('.mosaic-replies [role="alert"]')?.textContent).toContain("Only first reply failed");
});
it("shows reply attachment failures only in the captured target, including late read failures", async () => {
  attachmentBrowser();
  const ui = await mount(undefined, main, [{ seq: 1, role: "assistant", content: "Target A" }, { seq: 2, role: "assistant", content: "Target B" }]);
  await ui.click('[data-action="reply-message"]');
  await chooseFiles(ui.container, [new File([], "empty.txt", { type: "text/plain" })]);
  expect(ui.container.querySelector('.mosaic-replies [role="alert"]')).not.toBeNull();
  let reader!: FileReader;
  const read = vi.spyOn(FileReader.prototype, "readAsDataURL").mockImplementation(function (this: FileReader) { reader = this; });
  cleanups.push(() => read.mockRestore());
  await chooseFiles(ui.container, [new File(["hello"], "notes.txt", { type: "text/plain" })]);
  expect(ui.container.querySelector('.mosaic-replies [role="alert"]')).toBeNull();
  await ui.click('[data-action="close-replies"]');
  await act(async () => ui.container.querySelectorAll<HTMLButtonElement>('.mosaic-main [data-action="reply-message"]')[1].click());
  await act(async () => reader.onerror?.(new ProgressEvent("error") as ProgressEvent<FileReader>));
  expect(ui.container.querySelector('.mosaic-replies [role="alert"]')).toBeNull();
  expect(ui.container.querySelector('.mosaic-main [role="alert"]')).toBeNull();
  await ui.click('[data-action="close-replies"]'); await ui.click('[data-action="reply-message"]');
  expect(ui.container.querySelector('.mosaic-replies [role="alert"]')?.textContent).toContain("File read failed");
});
it("removes the background floating jump control while the Replies dialog is open", async () => {
  const ui = await mount();
  const viewport = ui.container.querySelector<HTMLElement>('.plugin-conversation-scroll')!;
  Object.defineProperties(viewport, { clientHeight: { configurable: true, value: 300 }, scrollHeight: { configurable: true, value: 2000 } });
  await act(async () => { viewport.scrollTop = 0; viewport.dispatchEvent(new Event("scroll")); });
  await vi.waitFor(() => expect(document.querySelector('[data-wuu-component="jump-to-latest"]')).not.toBeNull());
  await ui.click('[data-action="reply-message"]');
  expect(document.querySelector('[data-wuu-component="jump-to-latest"]')).toBeNull();
});
it("opens a quoted reply chain, focuses its composer and restores the trigger on Escape", async () => {
  const ref = (seq: number) => ({ reply_to: { session_id: "s-private", seq } });
  const ui = await mount(undefined, main, [{ seq: 1, role: "assistant", content: "Root" }, { seq: 2, role: "user", content: "First reply", relation: ref(1) }, { seq: 3, role: "assistant", content: "Nested reply", relation: ref(2) }, { seq: 4, role: "assistant", content: "Unrelated" }]);
  const trigger = ui.container.querySelector<HTMLButtonElement>('[data-reply-reference]')!;
  await ui.click('[data-reply-reference]');
  const panel = ui.container.querySelector('.mosaic-replies')!;
  expect(panel.textContent).toContain("Root"); expect(panel.textContent).toContain("First reply"); expect(panel.textContent).toContain("Nested reply"); expect(panel.textContent).not.toContain("Unrelated");
  expect(panel.querySelector('[data-role="user"] [data-action="reply-message"]')).toBeNull();
  expect(panel.querySelector('[data-action="reply-message"]')).toBeNull();
  await act(async () => ui.publish({ kind: "notification", message: { method: "item/started", params: { thread_id: "s-private", turn_id: "reply-live", item: { id: "answer", type: "agent_message", text: "Related live answer", relation: ref(1) } } } }));
  expect(panel.textContent).toContain("Related live answer");
  expect(document.activeElement).toBe(panel.querySelector('textarea'));
  expect(ui.container.querySelector('.mosaic-main')?.hasAttribute('inert')).toBe(true);
  await act(async () => panel.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(ui.container.querySelector('.mosaic-replies')).toBeNull(); await vi.waitFor(() => expect(document.activeElement).toBe(trigger));
});
it("opens later messages into the canonical discussion even when its ancestors are outside main history", async () => {
  const root = { seq: 1, role: "assistant", content: "Older discussion root" };
  const later = { seq: 120, role: "assistant", content: "Later discussion answer", relation: { reply_to: { session_id: "s-private", seq: 50 } } };
  const ui = await mount(undefined, main, undefined, { history: async input => input.reply_seq !== undefined
    ? { reply_root_seq: 1, records: [root, later], has_earlier: false }
    : { records: [later] } });
  await ui.click('[data-action="reply-message"]');
  expect(ui.container.querySelector('.mosaic-replies')?.textContent).toContain(root.content);
  expect(ui.container.querySelector('.mosaic-replies')?.textContent).toContain(later.content);
  expect(ui.container.querySelector('.mosaic-replies [data-action="reply-message"]')).toBeNull();
  await ui.edit("Keep my discussion draft");
  await ui.click('[data-action="close-replies"]'); await ui.click('[data-action="reply-message"]');
  expect(ui.container.querySelector<HTMLTextAreaElement>('.mosaic-replies textarea')?.value).toBe("Keep my discussion draft");
});
it("does not clear a re-edited draft even when its text matches the pending send", async () => {
  let finish!: (value: unknown) => void;
  const pending = new Promise(resolve => { finish = resolve; });
  const ui = await mount(async method => method === "agents.send" ? pending : {});
  await ui.edit("Same text"); await ui.click('.composer-send-button');
  await ui.edit("Changed draft"); await ui.edit("Same text");
  await act(async () => { finish({ state: "accepted" }); await pending; });
  expect(ui.container.querySelector('textarea')!.value).toBe("Same text");
});
it("offers live replies only after durable identity and carries terminal task relations", async () => {
  const ui = await mount(async method => { if (method === "agents.send") throw new Error("Keep draft"); }, main, [], { history: async input => input.reply_seq === 9 ? { reply_root_seq: 9, records: [{ seq: 9, role: "assistant", content: "Live result" }], has_earlier: false } : { records: [] } });
  await act(async () => ui.publish({ kind: "notification", message: { method: "item/agentMessage/delta", params: { thread_id: "s-private", turn_id: "live", item_id: "answer", delta: "Live result" } } }));
  expect(ui.container.querySelector('[data-action="reply-message"]')).toBeNull();
  const relation = { metadata: { task_links: [{ task_id: "t1", requirements_revision: 2 }] } };
  await act(async () => ui.publish({ kind: "notification", message: { method: "turn/completed", params: { thread_id: "s-private", turn: { id: "live", items_view: "full", items: [{ id: "answer", type: "agent_message", seq: 9, text: "Live result", relation }] } } } }));
  await ui.click('[data-action="reply-message"]');
  expect(ui.container.querySelector('.mosaic-replies [data-role="assistant"]')?.textContent).toContain("Live result");
  await ui.edit("Follow up"); await ui.click('.composer-send-button');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.send", input: expect.objectContaining({ relation: { reply_to: { session_id: "s-private", seq: 9 }, ...relation } }) }));
});
it("stops a task with its reviewed revision without stopping the agent's current reply", async () => {
  const ui = await mount(); await ui.click('[data-task-id="t1"]'); await ui.click('[data-action="stop-task"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "tasks.stop", input: { task_id: "t1", expected_revision: 3 } }));
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "agents.stop_reply")).toBe(false);
});

const fileDigest = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824";
async function chooseFiles(container: HTMLElement, files: File[]) {
  const input = (container.querySelector('.mosaic-replies') ?? container).querySelector<HTMLInputElement>('input[type="file"]'); expect(input).not.toBeNull();
  await act(async () => { Object.defineProperty(input!, "files", { configurable: true, value: files }); input!.dispatchEvent(new Event("change", { bubbles: true })); });
}
function attachmentBrowser() {
  Object.defineProperty(crypto, "subtle", { configurable: true, value: webcrypto.subtle });
  const create = vi.fn(() => "blob:verified-scoped-file"), revoke = vi.fn();
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: create });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revoke }); return { create, revoke };
}
it("keeps attachment-only failed sends retryable with exact proof bytes and references only in plugin input", async () => {
  attachmentBrowser(); const ui = await mount(async method => { if (method === "agents.send") throw new Error("Offline after admission"); });
  await chooseFiles(ui.container, [new File(["hello"], "notes.txt", { type: "text/plain" })]);
  await vi.waitFor(async () => { await act(async () => {}); expect(ui.container.querySelector('.composer-attachment-tray')).not.toBeNull(); });
  await ui.click('.composer-send-button'); await ui.remount(); await ui.click('.composer-send-button');
  const sends = ui.invoke.mock.calls.map(([request]) => request).filter(request => request.method === "agents.send" && (request.input as any).agent_id === "a-main"); expect(sends).toHaveLength(2);
  expect(sends[0].input).toEqual({ agent_id: "a-main", request_id: expect.any(String), prompt: "", attachments: [{ sha256: fileDigest, filename: "notes.txt", media_type: "text/plain" }] });
  expect(sends[0].userInput).toEqual({ requestId: (sends[0].input as any).request_id, prompt: "", attachments: [{ sha256: fileDigest, filename: "notes.txt", media_type: "text/plain", data: "aGVsbG8=" }] });
  expect(sends[1].input).toEqual(sends[0].input); expect(sends[1].userInput).toEqual(sends[0].userInput);
  expect(ui.container.querySelector('.composer-attachment-tray')?.textContent).toContain("notes.txt");
});
it("rejects oversized or too-many file selections before admitting attachment bytes", async () => {
  attachmentBrowser(); const ui = await mount();
  await chooseFiles(ui.container, [new File([new Uint8Array(20 * 1024 * 1024 + 1)], "oversized.bin")]);
  expect(ui.container.querySelector('[role="alert"]')).not.toBeNull(); expect(ui.container.querySelector('.composer-attachment-tray')).toBeNull();
  await chooseFiles(ui.container, Array.from({ length: 17 }, (_, i) => new File(["x"], `file${i}.txt`)));
  expect(ui.container.querySelector('.composer-attachment-tray')).toBeNull(); expect(ui.invoke.mock.calls.some(([request]) => request.method === "agents.send")).toBe(false);
});
it("loads raster reply attachments through verified scoped bytes and revokes the inline preview on close", async () => {
  const browser = attachmentBrowser();
  const attachment = { kind: "artifact", index: 0, name: "image.png", mime_type: "image/png", size: 5, sha256: fileDigest };
  const ui = await mount(async (method, input) => method === "agents.attachments.read" ? { data: "aGVsbG8=", offset: input.offset, total: 8, content_type: "image/png", name: "image.png", sha256: fileDigest } : {}, main, [{ seq: 7, role: "assistant", content: "Image result", attachments: [attachment] }]);
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "agents.attachments.read")).toBe(false);
  await ui.click('[data-action="reply-message"]');
  await vi.waitFor(() => expect(ui.container.querySelector('.mosaic-replies img')?.getAttribute('src')).toBe("blob:verified-scoped-file"));
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.attachments.read", input: { agent_id: "a-main", seq: 7, kind: "artifact", index: 0, sha256: fileDigest, offset: 0 } }));
  await ui.click('[data-action="close-replies"]'); expect(browser.revoke).toHaveBeenCalledWith("blob:verified-scoped-file");
});
it.each(["corrupt", "closed"])("never exposes an unverified or late inline reply image (%s)", async mode => {
  const browser = attachmentBrowser();
  let finish!: (value: unknown) => void;
  const pending = new Promise(resolve => { finish = resolve; });
  const ui = await mount(async method => method === "agents.attachments.read" ? pending : {}, main, [{ seq: 7, role: "assistant", content: "Image result", attachments: [{ kind: "artifact", index: 0, name: "image.png", mime_type: "image/png", size: 5, sha256: fileDigest }] }]);
  await ui.click('[data-action="reply-message"]');
  if (mode === "closed") await ui.click('[data-action="close-replies"]');
  await act(async () => { finish({ data: mode === "corrupt" ? "d29ybGQ=" : "aGVsbG8=", offset: 0, total: 8, content_type: "image/png", name: "image.png", sha256: fileDigest }); await pending; });
  if (mode === "corrupt") await vi.waitFor(() => expect(ui.container.querySelector('.mosaic-replies [role="alert"]')).not.toBeNull());
  expect(browser.create).not.toHaveBeenCalled(); expect(ui.container.querySelector('.mosaic-replies img')).toBeNull();
});
it("opens artifacts only through agent-scoped verified chunks and revokes the Blob on navigation", async () => {
  const browser = attachmentBrowser();
  const ui = await mount(async (method, input) => method === "agents.attachments.read" ? { data: input.offset === 0 ? "aGVs" : "bG8=", offset: input.offset, total: 8, content_type: "image/svg+xml", name: "diagram.svg", sha256: fileDigest } : {}, main,
    [{ seq: 7, role: "assistant", content: "Result", attachments: [{ kind: "artifact", index: 0, name: "diagram.svg", mime_type: "image/svg+xml", size: 5, sha256: fileDigest }] }]);
  await ui.click('[data-action="open-attachment"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.attachments.read", input: { agent_id: "a-main", seq: 7, kind: "artifact", index: 0, sha256: fileDigest, offset: 4 } }));
  await vi.waitFor(async () => { await act(async () => {}); expect(browser.create).toHaveBeenCalledWith(expect.any(Blob)); }); expect(ui.container.querySelector("img")).toBeNull();
  await ui.remount(); expect(browser.revoke).toHaveBeenCalledWith("blob:verified-scoped-file");
});
it("does not expose a Blob URL for corrupt attachment chunks", async () => {
  const browser = attachmentBrowser();
  const ui = await mount(async method => method === "agents.attachments.read" ? { data: "YmFk", offset: 0, total: 4, content_type: "text/plain", name: "notes.txt", sha256: fileDigest } : {}, main,
    [{ seq: 7, role: "user", attachments: [{ kind: "file", index: 0, name: "notes.txt", mime_type: "text/plain", size: 3, sha256: fileDigest }] }]);
  await ui.click('[data-action="open-attachment"]'); await vi.waitFor(async () => { await act(async () => {}); expect(ui.container.querySelector('[role="alert"]')).not.toBeNull(); }); expect(browser.create).not.toHaveBeenCalled(); expect(ui.container.querySelector('a[download]')).toBeNull(); expect(ui.container.querySelector('[role="alert"]')).not.toBeNull();
});

it("reads task result artifacts through the reviewed task execution rather than an agent session", async () => {
  attachmentBrowser();
  const ui = await mount(async (method, input) => {
    if (method === "tasks.history") return { records: [{ seq: 9, role: "tool", content: "Private internal tool body", attachments: [{ kind: "artifact", index: 0, name: "result.txt", mime_type: "text/plain", size: 5, sha256: fileDigest }] }] };
    if (method === "tasks.attachments.read") return { data: "aGVsbG8=", offset: input.offset, total: 8, content_type: "text/plain", name: "result.txt", sha256: fileDigest };
    return {};
  });
  await ui.click('[data-task-id="t1"]');
  const trace = ui.container.querySelector('[data-task-trace]'); expect(trace).not.toBeNull();
  expect(trace!.closest('.turn-process-fold')).not.toBeNull();
  expect(ui.container.textContent).not.toContain("Private internal tool body");
  await ui.click('[data-action="load-attachment"]');
  await vi.waitFor(async () => { await act(async () => {}); expect(ui.container.querySelector('a[download]')).not.toBeNull(); });
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "tasks.attachments.read", input: { task_id: "t1", requirements_revision: 1, seq: 9, kind: "artifact", index: 0, sha256: fileDigest, offset: 0 } }));
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "agents.attachments.read")).toBe(false);
});

it("refreshes a stopping routine after host events so settled cancellation can be resumed", async () => {
  let settled = false;
  const ui = await mount(async method => method === "routines.list" ? { routines: [{ routine_id:"routine-one",agent_id:"a-main",title:"Synthetic routine",status:settled?"paused":"stopping",cancellation_pending:!settled,revision:settled?3:2,binding:{} }] } : {});

  await ui.click('[data-routine-id="routine-one"]');
  expect(ui.container.querySelector<HTMLButtonElement>('[data-action="toggle-routine"]')?.disabled).toBe(true);
  settled = true; await act(async () => ui.publish());
  expect(ui.container.querySelector<HTMLButtonElement>('[data-action="toggle-routine"]')?.disabled).toBe(false);
});

async function changeField(container: ParentNode, selector: string, value: string) {
  const node = container.querySelector<HTMLInputElement | HTMLSelectElement>(selector); expect(node).not.toBeNull();
  await act(async () => { Object.getOwnPropertyDescriptor(node!.tagName === "SELECT" ? HTMLSelectElement.prototype : HTMLInputElement.prototype,"value")!.set!.call(node,value); node!.dispatchEvent(new Event("input",{bubbles:true})); node!.dispatchEvent(new Event("change",{bubbles:true})); });
}
it("searches only the main conversation and marks partial history results without exposing session controls", async () => {
  const ui=await mount(async method=>method==="agents.search"?{session_id:"private-search-session",matches:[{seq:20,role:"assistant",excerpt:"Remembered Kyoto"},{seq:21,role:"tool",excerpt:"Hidden process data"}],has_more:true,budget_exhausted:true,next_cursor:"cursor"}:{}, main, undefined, { historySearch: true });
  await ui.click('[data-action="search-history"]'); await changeField(document, '[data-agent-history-query]',"Kyoto"); await ui.click('[data-action="search-agent-history"]', document);
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({method:"agents.search",input:expect.objectContaining({agent_id:"a-main",query:"Kyoto"})}));
  expect(document.querySelector('[data-history-search-results]')?.textContent).toContain("Remembered Kyoto");
  expect(document.querySelector('[data-history-search-partial]')).not.toBeNull();
  expect(document.body.textContent).not.toContain("Hidden process data"); expect(document.body.textContent).not.toContain("private-search-session");
  await ui.click('[data-action="close-history-search"]', document); expect(document.body.textContent).not.toContain("Remembered Kyoto");
});
it("saves reversible notification grouping without altering reply or quiet-hours settings", async () => {
  const ui=await mount(async method=>method==="settings.update"?{settings:{revision:2,notifications:true,notification_batch_seconds:300,quiet_hours:{enabled:false,start:"22:00",end:"08:00",timezone:"UTC"}}}:{});
  await ui.settings(); await changeField(ui.container,'[data-notification-batch]',"300"); await ui.click('[data-action="save-notifications"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({method:"settings.update",input:{expected_revision:1,notifications:true,notification_batch_seconds:300,quiet_hours:{enabled:false,start:"22:00",end:"08:00",timezone:"UTC"}}}));
});
it("pages timestamp-bounded agent history and reads the original hit by exact owned sequence", async () => {
  const ui = await mount(async (method, input) => method === "agents.search" ? input.cursor ? { matches: [{ seq: 21, role: "user", excerpt: "Kyoto legacy" }], has_more: false } : { matches: [{ seq: 20, role: "assistant", excerpt: "Kyoto excerpt", at: "2026-10-18T09:00:00Z" }], has_more: true, next_cursor: "page-two" } : {}, main,
    [{ seq: 20, role: "assistant", content: "The original Kyoto message", at: "2026-10-18T09:00:00Z" }], { historySearch: true });
  await ui.click('[data-action="search-history"]');
  await changeField(document, '[data-agent-history-query]', "Kyoto");
  await changeField(document, '[data-history-date="from"]', "2026-10-18");
  await changeField(document, '[data-history-date="until"]', "2026-10-18");
  await ui.click('[data-action="search-agent-history"]', document);
  const bounds = { agent_id: "a-main", query: "Kyoto", limit: 25, time_min: new Date(2026, 9, 18).toISOString(), time_max: new Date(2026, 9, 19).toISOString() };
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.search", input: bounds }));
  await ui.click('[data-action="more-agent-history"]', document);
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.search", input: { ...bounds, cursor: "page-two" } }));
  expect(document.querySelector('[data-history-search-results]')?.textContent).toContain("Kyoto excerpt");
  expect(document.querySelector('[data-history-timestamp="unknown"]')).not.toBeNull();
  await ui.click('[data-history-hit="20"]', document);
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.history", input: { agent_id: "a-main", start_seq: 20, end_seq: 20 } }));
  expect(document.querySelector('[data-history-original="20"]')?.textContent).toContain("The original Kyoto message");
  await changeField(document, '[data-history-date="from"]', "2026-10-19");
  expect(document.querySelector('[data-history-original]')).toBeNull();
  expect(document.querySelector<HTMLButtonElement>('[data-action="search-agent-history"]')!.disabled).toBe(true);
});
it("does not pass off a different history record as the searched original", async () => {
  const ui = await mount(async method => method === "agents.search" ? { matches: [{ seq: 80, role: "assistant", excerpt: "Old match" }] } : {}, main, undefined, { historySearch: true });
  await ui.click('[data-action="search-history"]');
  await changeField(document, '[data-agent-history-query]', "Old"); await ui.click('[data-action="search-agent-history"]', document); await ui.click('[data-history-hit="80"]', document);
  expect(document.querySelector('[data-history-original]')).toBeNull();
  expect(document.querySelector('[data-history-original-error]')).not.toBeNull();
});

// Regression: switching named agents must never replace their draft or dispatch
// a follow-up using the previous conversation's identity.

it("uses native controls without exposing personal messages or drafts to Coding extension providers", async () => {
  const message = vi.fn(() => <span>Coding replacement</span>), completion = vi.fn(() => null), surface = vi.fn((_context, fallback) => fallback);
  await desktopPluginHost.activateGeneration({ pluginId: "test:coding-observers", generation: "one", register(api) {
    api.registerRenderer({ id: "messages", category: "message", match: "text/markdown", render: message });
    api.registerSurface("conversation.message", { id: "surface", mode: "replace", render: surface });
    api.registerComposerInlineCompletionProvider({ id: "drafts", provideCompletion: completion });
  } });
  cleanups.push(() => act(() => desktopPluginHost.unload("test:coding-observers")));
  const ui = await mount(undefined, main, [{ seq: 1, role: "user", content: "Private user context" }, { seq: 2, role: "assistant", content: "Private assistant context" }]);
  await ui.edit("Private draft");
  await act(async () => ui.container.querySelector("textarea")!.focus());
  expect(ui.container.querySelector('.mosaic .split-composer [data-wuu-component="composer-input"]')).not.toBeNull();
  expect(ui.container.querySelector(".mosaic .user-message")).not.toBeNull();
  expect(ui.container.textContent).toContain("Private assistant context");
  expect(message).not.toHaveBeenCalled(); expect(completion).not.toHaveBeenCalled(); expect(surface).not.toHaveBeenCalled();
});

it("shows genuine user messages even when their exact text resembles internal envelopes", async () => {
  const literals = ['<process_notification>example</process_notification>', '<subagent_notification>example</subagent_notification>', '{"author":"/root/a","recipient":"/root/b"}', '  Personal identity revision 12\nexact user text  '];
  const ui = await mount(undefined, main, literals.map((content, index) => ({ seq: index + 1, role: "user", content })));
  expect(ui.container.querySelectorAll('[data-wuu-component="plugin-ui-conversation-message"] .user-message')).toHaveLength(literals.length);
  expect(ui.container.textContent).toContain('"/root/a"');
  expect(ui.container.textContent).toContain("example");
  expect(ui.container.textContent).toContain('Personal identity revision 12');
});
it("preserves later drafts and panel selection when an earlier send settles", async () => {
  let resolveSend!: (value: unknown) => void;
  const pending = new Promise(resolve => { resolveSend = resolve; });
  const ui = await mount(async method => method === "agents.send" ? pending : {});
  const conversation = ui.container.querySelector(".plugin-conversation");
  await ui.edit("First request"); await ui.click('.composer-send-button');
  await ui.edit("A later draft"); await ui.click('[data-task-id="t1"]');
  await act(async () => { resolveSend({ state: "accepted" }); await pending; });
  expect(ui.container.querySelector(".plugin-conversation")).toBe(conversation);
  expect(ui.container.querySelector("textarea")!.value).toBe("A later draft");
  expect(ui.container.querySelector('[data-task-detail="t1"]')).not.toBeNull();
  expect(ui.invoke.mock.calls.filter(([request]) => request.method === "agents.send")).toHaveLength(1);
});it("submits the exact canonical long-paste text through the native folded composer", async () => {
  const ui = await mount(async method => { if (method === "agents.send") throw new Error("Keep proof for retry"); return {}; });
  const text = `  ${Array.from({ length: 25 }, (_, index) => `Private source line ${index}.`).join("\n")}\n `;
  const event = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", { value: { items: [], getData: (type: string) => type === "text/plain" ? text : "" } });
  await act(async () => ui.container.querySelector("textarea")!.dispatchEvent(event));
  expect(ui.container.querySelector(".composer-collapsed-prompt-card")).not.toBeNull();
  await ui.edit("\nFollow up exactly "); await ui.click('.composer-send-button');
  const request = ui.invoke.mock.calls.map(([request]) => request).find(request => request.method === "agents.send")!;
  expect((request.input as any).prompt).toBe(`${text}\nFollow up exactly `);
  expect(request.userInput?.prompt).toBe(`${text}\nFollow up exactly `);
  expect(ui.container.querySelector(".composer-collapsed-prompt-card")).not.toBeNull();
});

// Twin hides persistent actions, and revealing time must never manufacture a
// timestamp for legacy or still-streaming messages.
it.each(["2026-10-18T09:00:00Z", undefined])("reveals only persisted message times without Coding action rows (%s)", async at => {
  const content = "  Answer **exactly**\nwith trailing space  ";
  const ui = await mount(undefined, main, [{ seq: 1, role: "assistant", content, ...(at ? { at } : {}) }]);
  const message = ui.container.querySelector('[data-wuu-component="plugin-ui-conversation-message"][data-role="assistant"]')!;
  expect(message.querySelector('[data-wuu-component="message-actions"]')).toBeNull();
  expect(message.querySelector("strong")?.textContent).toBe("exactly");
  const time = message.querySelector("time");
  if (at) {
    expect(time?.getAttribute("datetime")).toBe(at);
    expect(time?.getAttribute("aria-hidden")).toBe("true");
  } else expect(time).toBeNull();
  await ui.click('.bubble-time-toggle');
  expect(ui.container.querySelector('.bubble-time-toggle')?.getAttribute('aria-pressed')).toBe('true');
  if (at) expect(time?.getAttribute("aria-hidden")).toBe("false");
  await act(async () => ui.container.querySelector('.bubble-time-toggle')!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(ui.container.querySelector('.bubble-time-toggle')?.getAttribute('aria-pressed')).toBe('false');
  if (at) expect(time?.getAttribute("aria-hidden")).toBe("true");
});

// Search is transient UI: dismissal restores keyboard focus without resetting
// the underlying conversation draft or its digest-bound attachments.
it("opens history from the header and restores focus and draft attachments after Escape or close", async () => {
  attachmentBrowser();
  const ui = await mount(async method => { if (method === "agents.send") throw new Error("Keep draft for retry"); return {}; }, main, undefined, { historySearch: true });
  const prompt = "  Keep this draft\n ";
  await ui.edit(prompt);
  await chooseFiles(ui.container, [new File(["hello"], "notes.txt", { type: "text/plain" })]);
  await vi.waitFor(() => expect(ui.container.querySelector(".composer-attachment-tray")?.textContent).toContain("notes.txt"));
  expect(document.querySelector('[data-agent-history-query]')).toBeNull();
  const trigger = ui.container.querySelector<HTMLButtonElement>('header [data-action="search-history"]');
  expect(trigger).not.toBeNull();
  for (const dismiss of ["escape", "close"]) {
    await act(async () => trigger!.focus());
    await ui.click('[data-action="search-history"]');
    const input = document.querySelector<HTMLInputElement>('[data-agent-history-query]');
    expect(input).not.toBeNull();
    await vi.waitFor(() => expect(document.activeElement).toBe(input));
    await changeField(document, '[data-agent-history-query]', "A private query");
    if (dismiss === "escape") await act(async () => input!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    else await ui.click('[data-action="close-history-search"]', document);
    await vi.waitFor(() => {
      expect(document.querySelector('[data-agent-history-query]')).toBeNull();
      expect(document.activeElement).toBe(trigger);
    });
    expect(ui.container.querySelector("textarea")!.value).toBe(prompt);
    expect(ui.container.querySelector(".composer-attachment-tray")?.textContent).toContain("notes.txt");
  }
  await ui.click(".composer-send-button");
  const sent = ui.invoke.mock.calls.map(([request]) => request).find(request => request.method === "agents.send")!;
  expect(sent.input).toEqual({ agent_id: "a-main", request_id: expect.any(String), prompt, attachments: [{ sha256: fileDigest, filename: "notes.txt", media_type: "text/plain" }] });
  expect(sent.userInput).toEqual({ requestId: (sent.input as any).request_id, prompt, attachments: [{ sha256: fileDigest, filename: "notes.txt", media_type: "text/plain", data: "aGVsbG8=" }] });
});

it("discards a late history response after closing and reopening search", async () => {
  let resolveSearch!: (value: unknown) => void;
  const pending = new Promise(resolve => { resolveSearch = resolve; });
  const ui = await mount(async method => method === "agents.search" ? pending : {}, main, undefined, { historySearch: true });
  await ui.click('[data-action="search-history"]');
  await changeField(document, '[data-agent-history-query]', "Old query");
  await ui.click('[data-action="search-agent-history"]', document);
  await ui.click('[data-action="close-history-search"]', document);
  await ui.click('[data-action="search-history"]');
  await act(async () => { resolveSearch({ matches: [{ seq: 42, role: "assistant", excerpt: "Dismissed private result" }] }); await pending; });
  expect(document.body.textContent).not.toContain("Dismissed private result");
  expect(document.querySelector('[data-history-search-results]')).toBeNull();
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.search", input: expect.objectContaining({ agent_id: "a-main", query: "Old query" }) }));
});

// Returning before the intervening agent loads must not expose an empty ready
// tree, because that would replace the saved reading position with its top.

// Real message DOM carries nested native user markers. Reading anchors must
// still cover assistant bodies and survive content inserted above either role.
it.each([{ role: "assistant", top: 500 }, { role: "user", top: 1050 }])("preserves a $role reading anchor when older content is prepended", async ({ top }) => {
  const ui = await mount(undefined, main, [
    { seq: 1, role: "user", content: "Earlier question" },
    { seq: 2, role: "assistant", content: "A long answer that spans the viewport" },
    { seq: 3, role: "user", content: "A later user question" },
  ]);
  const viewport = ui.container.querySelector<HTMLElement>('[role="log"]')!.parentElement!;
  Object.defineProperties(viewport, { clientHeight: { configurable: true, value: 300 }, scrollHeight: { configurable: true, value: 2000 } });
  viewport.scrollTop = top;
  const rect = (offset: number, height: number) => ({ x: 0, y: offset, top: offset, bottom: offset + height, left: 0, right: 600, width: 600, height, toJSON: () => ({}) });
  viewport.getBoundingClientRect = () => rect(100, 300);
  let prependedHeight = 0;
  const bounds = [[0, 100], [100, 900], [1000, 200]];
  const messages = ui.container.querySelectorAll<HTMLElement>('[data-wuu-component="plugin-ui-conversation-message"]');
  messages.forEach((message, index) => {
    const [offset, height] = bounds[index];
    message.getBoundingClientRect = () => rect(100 + offset + prependedHeight - viewport.scrollTop, height);
    message.querySelectorAll<HTMLElement>("[data-turn-id]").forEach(nested => {
      nested.getBoundingClientRect = () => rect(120 + offset + prependedHeight - viewport.scrollTop, height - 40);
    });
  });
  const anchor = captureReadingAnchor(viewport);
  expect(anchor).toBeDefined();
  prependedHeight = 250;
  expect(readingAnchorScrollTop(viewport, anchor!)).toBe(top + 250);
  viewport.scrollTop = top + 250;
  expect(captureReadingAnchor(viewport)).toEqual(anchor);
});

it("keeps one main conversation and its draft while right-panel task and output details change", async () => {
  const ui = await mount(undefined, main, undefined, { tasks: [{ task_id: "t1", agent_id: "historical-worker", title: "Verified result", state: "completed", revision: 3, requirements_revision: 1, result: { summary: "Ready" } }] });
  const conversation = ui.container.querySelector(".plugin-conversation");
  const textarea = ui.container.querySelector("textarea");
  await ui.edit("Keep this draft");
  await ui.click('[data-task-id="t1"]');
  expect(ui.container.querySelector(".plugin-conversation")).toBe(conversation);
  expect(ui.container.querySelector("textarea")).toBe(textarea);
  await ui.click('[data-action="close-detail"]');
  await ui.click('[data-output-task-id="t1"]');
  expect(ui.container.querySelector(".plugin-conversation")).toBe(conversation);
  expect(textarea!.value).toBe("Keep this draft");
  await ui.click('[data-action="open-main-chat"]');
  expect(document.activeElement).toBe(textarea);
  expect(ui.container.querySelectorAll("textarea")).toHaveLength(1);
  expect(ui.container.querySelector(".plugin-conversation")).toBe(conversation);
  expect(textarea!.value).toBe("Keep this draft");
  expect(ui.invoke.mock.calls.filter(([request]) => request.method === "agents.open")).toHaveLength(1);
  expect(ui.invoke.mock.calls.filter(([request]) => request.method === "agents.open").every(([request]) => (request.input as any).agent_id === "a-main")).toBe(true);
  expect(ui.container.querySelector('[data-agent-id], [role="tablist"], [data-section="memory"]')).toBeNull();
});
it("keeps task history in one native process fold without detail action rows or hidden context", async () => {
  const surface = vi.fn((_context, fallback) => fallback);
  await desktopPluginHost.activateGeneration({ pluginId: "test:task-detail-observer", generation: "one", register(api) {
    api.registerSurface("conversation.message", { id: "surface", mode: "replace", render: surface });
  } });
  cleanups.push(() => act(() => desktopPluginHost.unload("test:task-detail-observer")));
  const result = "A **verified result** with [a local file](file:///private/result.txt)";
  const ui = await mount(async method => method === "tasks.history" ? { records: [
    { seq: 1, role: "user", read_only: true, content: "Internal task context", attachments: [{ kind: "file", index: 0, name: "private-context.txt", sha256: fileDigest }] },
    { seq: 2, role: "assistant", hidden: true, content: "Hidden bookkeeping" },
    { seq: 3, role: "assistant", content: "A **public update**", at: "2026-10-19T08:00:00Z" },
  ] } : {}, main, undefined, { tasks: [{ task_id: "t1", title: "Research result", goal: "Find the source", state: "completed", revision: 3, requirements_revision: 1, progress: "Checked the source", result: { summary: result, verified_at: "2026-10-19T08:00:00Z" } }] });
  await ui.click('[data-task-id="t1"]');
  const detail = ui.container.querySelector('[data-task-detail="t1"]')!;
  expect(detail.querySelector('[data-role="user"]')?.textContent).toContain("Find the source");
  expect(detail.querySelector('.turn-answer-body strong')?.textContent).toBe("verified result");
  expect(detail.querySelector('a[href^="file:"]')).toBeNull();
  const process = detail.querySelector<HTMLElement>('[aria-expanded="false"][aria-controls$="-process-fold-body"]');
  expect(process).not.toBeNull();
  await act(async () => process!.click());
  expect(process!.getAttribute("aria-expanded")).toBe("true");
  expect(detail.querySelector('.turn-process-entry .agent-text')?.textContent).toContain("Checked the source");
  expect(detail.querySelector("time")).toBeNull();
  expect(detail.querySelector('.message-actions')).toBeNull();
  expect(detail.querySelectorAll('.turn-process-fold')).toHaveLength(1);
  expect(detail.querySelector('summary')).toBeNull();
  const trace = detail.querySelector('[data-task-trace]')!;
  expect(trace.closest('.turn-process-fold')).not.toBeNull();
  expect(trace.querySelector("strong")?.textContent).toBe("public update");
  expect(detail.textContent).not.toContain("Internal task context");
  expect(detail.textContent).not.toContain("Hidden bookkeeping");
  expect(detail.textContent).not.toContain("private-context.txt");
  expect(ui.container.querySelector('.mosaic-main .message-copy-button')).toBeNull();
  expect(surface).not.toHaveBeenCalled();
});
it("keeps requirement history selection inside the native fold and ignores a late previous revision", async () => {
  let resolvePrevious!: (value: unknown) => void;
  const previous = new Promise(resolve => { resolvePrevious = resolve; });
  const ui = await mount(async (method, input) => method === "tasks.history" ? input.requirements_revision === 1 ? previous : { records: [{ seq: 5, role: "assistant", content: "Current reviewed history" }] } : {}, main, undefined, {
    tasks: [{ task_id: "t1", title: "Revised task", state: "completed", revision: 5, requirements_revision: 2, requirements: [{ revision: 1 }, { revision: 2 }], result: { summary: "Reviewed result" } }],
  });
  await ui.click('[data-task-id="t1"]');
  await ui.click('[data-task-detail] [aria-controls$="-process-fold-body"]');
  const detail = ui.container.querySelector('[data-task-detail]')!;
  expect(detail.querySelector('select')?.closest('.turn-process-fold')).not.toBeNull();
  await changeField(detail, 'select', "1");
  await changeField(detail, 'select', "2");
  await act(async () => resolvePrevious({ records: [{ seq: 6, role: "assistant", content: "Late previous history" }] }));
  expect(detail.querySelector('[data-task-trace]')?.textContent).toContain("Current reviewed history");
  expect(detail.textContent).not.toContain("Late previous history");
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "tasks.history", input: { task_id: "t1", requirements_revision: 1 } }));
});
it("refreshes the open process history when the existing task refresh reports a new revision", async () => {
  let history: unknown[] = [];
  const tasks = [{ task_id: "t1", title: "In-flight task", state: "running", revision: 1, requirements_revision: 1 }];
  const ui = await mount(async method => method === "tasks.history" ? { records: history } : {}, main, undefined, { tasks });
  await ui.click('[data-task-id="t1"]');
  history = [{ seq: 2, role: "assistant", content: "New published progress" }];
  tasks[0] = { ...tasks[0], revision: 2 };
  await act(async () => ui.publish());
  expect(ui.container.querySelector('[data-task-trace]')?.textContent).toContain("New published progress");
});
it("restores the same main conversation draft after leaving and reopening its view", async () => {
  const ui = await mount();
  await ui.edit("A durable route draft");
  await ui.remount();
  expect(ui.container.querySelector("textarea")!.value).toBe("A durable route draft");
  expect(ui.container.querySelector(".plugin-conversation")?.getAttribute("data-conversation-id")).toBe("s-private");
  expect(ui.invoke.mock.calls.filter(([request]) => request.method === "agents.ensure_default")).toHaveLength(2);
});

it("loads earlier messages until the Personal conversation can scroll", async () => {
  const clientHeight = vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("plugin-conversation-scroll") ? 700 : 0;
  });
  const scrollHeight = vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("plugin-conversation-scroll") ? Math.max(700, this.querySelectorAll(".plugin-conversation-message").length * 240) : 0;
  });
  cleanups.push(() => { clientHeight.mockRestore(); scrollHeight.mockRestore(); });
  const history = vi.fn(async input => input.end_seq === undefined
    ? { records: [{ seq: 5, role: "user", content: "Recent question" }], first_seq: 5, has_earlier: true }
    : { records: [
        { seq: 1, role: "user", content: "Older question" },
        { seq: 2, role: "assistant", content: "Older answer" },
        { seq: 3, role: "user", content: "Another older question" },
      ], first_seq: 1, has_earlier: false });
  const ui = await mount(undefined, main, undefined, { history });
  await vi.waitFor(() => expect(ui.container.textContent).toContain("Older answer"));
  expect(history).toHaveBeenCalledWith({ agent_id: "a-main", end_seq: 4 });
  expect(ui.container.querySelector('[data-action="load-earlier-messages"]')).toBeNull();
});

it("keeps earlier Personal history automatic instead of showing a control behind the identity header", async () => {
  const clientHeight = vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("plugin-conversation-scroll") ? 700 : 0;
  });
  const scrollHeight = vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("plugin-conversation-scroll") ? 1200 : 0;
  });
  cleanups.push(() => { clientHeight.mockRestore(); scrollHeight.mockRestore(); });
  const ui = await mount(undefined, main, [{ seq: 5, role: "assistant", content: "Recent answer" }], { history: async () => ({ records: [{ seq: 5, role: "assistant", content: "Recent answer" }], first_seq: 5, has_earlier: true }) });
  expect(ui.container.querySelector('[data-action="load-earlier-messages"]')).toBeNull();
  await ui.click('[data-action="reply-message"]');
  expect(ui.container.querySelector('.mosaic-replies .plugin-conversation-flow > .plugin-ui-button')).toBeNull();
});

it("recovers the first visible conversation through empty bounded history pages without repeating recovery on refresh", async () => {
  const reads: Array<number | undefined> = [];
  const ui = await mount(undefined, main, undefined, { history: async input => {
    reads.push(input.end_seq);
    const end = input.end_seq ?? 652;
    return end > 52 ? { records: [], first_seq: end - 199, has_earlier: true }
      : { records: [{ seq: 1, role: "user", content: "Original question" }, { seq: 2, role: "assistant", content: "Original reply" }], first_seq: 1, has_earlier: false };
  } });
  expect(ui.container.textContent).toContain("Original question");
  expect(ui.container.textContent).toContain("Original reply");
  expect(reads).toEqual([undefined, 452, 252, 52]);
  await act(async () => ui.publish());
  expect(reads).toEqual([undefined, 452, 252, 52, undefined]);
  expect(ui.container.textContent).toContain("Original reply");
});

it("finishes recovery of an entirely hidden transcript once and leaves ordinary refresh bounded", async () => {
  const reads: Array<number | undefined> = [];
  const ui = await mount(undefined, main, undefined, { history: async input => {
    reads.push(input.end_seq);
    return input.end_seq === 52 ? { records: [], first_seq: 1, has_earlier: false }
      : { records: [], first_seq: input.end_seq === 252 ? 53 : 253, has_earlier: true };
  } });
  expect(reads).toEqual([undefined, 252, 52]);
  expect(ui.container.querySelector("textarea")!.disabled).toBe(false);
  await act(async () => ui.publish());
  expect(reads).toEqual([undefined, 252, 52, undefined]);
});

it("resumes an interrupted history recovery at its pending boundary and ignores the superseded page", async () => {
  const reads: Array<number | undefined> = [];
  let release!: (value: unknown) => void;
  const pending = new Promise(resolve => { release = resolve; });
  let olderReads = 0;
  const ui = await mount(undefined, main, undefined, { history: async input => {
    reads.push(input.end_seq);
    if (input.end_seq === undefined) return { records: [], first_seq: 453, has_earlier: true };
    if (input.end_seq === 452) return { records: [], first_seq: 253, has_earlier: true };
    if (++olderReads === 1) return pending;
    return { records: [{ seq: 112, role: "assistant", content: "Recovered reply" }], first_seq: 53, has_earlier: true };
  } });
  expect(reads).toEqual([undefined, 452, 252]);
  await act(async () => ui.publish());
  expect(reads).toEqual([undefined, 452, 252, undefined, 252]);
  expect(ui.container.textContent).toContain("Recovered reply");
  expect(ui.container.querySelector("textarea")!.disabled).toBe(false);
  await act(async () => release({ records: [], first_seq: 53, has_earlier: true }));
  expect(reads).toEqual([undefined, 452, 252, undefined, 252]);
  expect(ui.container.textContent).toContain("Recovered reply");
});

it("discards a pending history recovery when a newer read has visible messages", async () => {
  let release!: (value: unknown) => void;
  const pending = new Promise(resolve => { release = resolve; });
  let fresh = false;
  const reads: Array<number | undefined> = [];
  const ui = await mount(undefined, main, undefined, { history: async input => {
    reads.push(input.end_seq);
    if (fresh) return { records: [{ seq: 253, role: "assistant", content: "Newest persisted reply" }], first_seq: 203, has_earlier: true };
    return input.end_seq === undefined ? { records: [], first_seq: 53, has_earlier: true } : pending;
  } });
  expect(reads).toEqual([undefined, 52]);
  fresh = true;
  await act(async () => ui.publish());
  expect(ui.container.textContent).toContain("Newest persisted reply");
  expect(ui.container.querySelector("textarea")!.disabled).toBe(false);
  await act(async () => release({ records: [{ seq: 2, role: "assistant", content: "Superseded old page" }], first_seq: 1, has_earlier: false }));
  expect(ui.container.textContent).not.toContain("Superseded old page");
  expect(reads).toEqual([undefined, 52, undefined]);
});

it("does not continue or report a failed history recovery after leaving and reopening the conversation", async () => {
  let reject!: (cause: Error) => void;
  const pending = new Promise((_, fail) => { reject = fail; });
  let reopened = false;
  const reads: Array<number | undefined> = [];
  const ui = await mount(undefined, main, undefined, { history: async input => {
    reads.push(input.end_seq);
    if (reopened) return { records: [{ seq: 2, role: "assistant", content: "Reopened conversation" }], first_seq: 1, has_earlier: false };
    return input.end_seq === undefined ? { records: [], first_seq: 53, has_earlier: true } : pending;
  } });
  expect(reads).toEqual([undefined, 52]);
  await ui.remount(() => { reopened = true; });
  await act(async () => reject(new Error("Stale history failure")));
  expect(reads).toEqual([undefined, 52, undefined]);
  expect(ui.container.textContent).toContain("Reopened conversation");
  expect(ui.container.querySelector('[role="alert"]')).toBeNull();
});

it("renders only the main session stream and ignores late deltas after completion", async () => {
  let records: unknown[] = [];
  const ui = await mount(undefined, main, undefined, { history: async () => ({ records }) });
  const emit = async (method: string, params: Record<string, unknown>) => act(async () => ui.publish({ kind: "notification", message: { method, params: { thread_id: "s-private", turn_id: "turn-one", ...params } } }));
  await emit("item/agentMessage/delta", { item_id: "answer", delta: "First live words" });
  await vi.waitFor(() => expect(ui.container.textContent).toContain("First live words"));
  await emit("item/agentMessage/delta", { thread_id: "coding-private", item_id: "foreign", delta: "Never show Coding" });
  expect(ui.container.textContent).not.toContain("Never show Coding");
  records = [{ seq: 8, role: "assistant", content: "First live words", provider_item_id: "provider-one" }];
  await emit("item/completed", { item: { id: "answer", type: "agent_message", seq: 8, source_id: "provider-one", text: "First live words", status: "completed", terminal: true } });
  await emit("item/agentMessage/delta", { item_id: "answer", delta: " stale duplicate" });
  expect(ui.container.textContent).not.toContain("stale duplicate");
  expect(ui.container.querySelectorAll('[data-role="assistant"]')).toHaveLength(1);
});

it("shows decisions in the main conversation and answers through the Personal host context", async () => {
  const prior = window.wuu;
  const question = { request_id: "permission-one", plugin_id: "personal", execution_id: "exec", thread_id: "s-private", turn_id: "turn-one", created_at: "2026-10-19T08:00:00Z", questions: [{ id: "decision", header: "Choose a destination", question: "Which folder should I use?", options: [{ label: "Travel", description: "Use the travel folder" }] }] };
  let visible = [question, { ...question, request_id: "background-question", thread_id: "hidden-task" }];
  const list = vi.fn().mockImplementation(async () => ({ questions: visible }));
  let onEvent: (event: any) => void = () => {};
  const answer = vi.fn().mockResolvedValue({ request_id: "permission-one", resolved: true });
  Object.defineProperty(window, "wuu", { configurable: true, value: { ...prior, listUserQuestions: list, answerUserQuestion: answer, onServerEvent: vi.fn(handler => { onEvent = handler; return () => {}; }) } });
  cleanups.push(() => Object.defineProperty(window, "wuu", { configurable: true, value: prior }));
  const ui = await mount();
  const context = { kind: "user", user_space_id: "local", product_domain: "personal", cwd: "" };
  expect(list).toHaveBeenCalledWith(undefined, context);
  expect(ui.container.querySelectorAll('.plugin-conversation .user-question-card')).toHaveLength(2);
  expect(ui.container.querySelector('.mosaic-sidebar .user-question-card')).toBeNull();
  await act(async () => onEvent({ kind: "notification", message: { method: "user-question/requested", params: { request: { ...question, request_id: "coding-question", thread_id: "coding", questions: [{ id: "foreign", question: "Do not show Coding" }] } } } }));
  expect(ui.container.textContent).not.toContain("Do not show Coding");
  await ui.click('.user-question-option'); await ui.click('.user-question-submit');
  expect(answer).toHaveBeenCalledWith("permission-one", { answers: [{ id: "decision", selected: ["Travel"] }] }, context);
  expect(ui.container.querySelectorAll('.user-question-card')).toHaveLength(1);
  visible = [];
  await act(async () => onEvent({ kind: "notification", message: { method: "user-question/resolved", params: { request_id: "background-question" } } }));
  expect(ui.container.querySelector('.user-question-card')).toBeNull();
});

it("keeps receiving the main stream while its view is temporarily hidden", async () => {
  const ui = await mount(undefined, main, []);
  const event = (delta: string) => ({ kind: "notification", message: { method: "item/agentMessage/delta", params: { thread_id: "s-private", turn_id: "live-turn", item_id: "live-answer", delta } } });
  await act(async () => ui.publish(event("Before leaving. ")));
  await ui.remount(() => ui.publish(event("While hidden.")));
  await vi.waitFor(() => expect(ui.container.textContent).toContain("Before leaving. While hidden."));
});

it("does not let a completed item's local ID suppress a later turn", async () => {
  const ui = await mount(undefined, main, []);
  await act(async () => ui.publish({ kind: "notification", message: { method: "item/completed", params: { thread_id: "s-private", turn_id: "earlier", item: { id: "answer", type: "agent_message", seq: 1, text: "Earlier answer", status: "completed" } } } }));
  await act(async () => ui.publish({ kind: "notification", message: { method: "item/agentMessage/delta", params: { thread_id: "s-private", turn_id: "later", item_id: "answer", delta: "Later answer" } } }));
  await vi.waitFor(() => expect(ui.container.textContent).toContain("Later answer"));
  expect(ui.container.textContent).toContain("Earlier answer");
});

it("retains confirmed queued admission across route changes and clears it when the same queue starts", async () => {
  const ui = await mount(async (method, input) => method === "agents.send" ? { state: "queued", queue_id: "queue-one", receipt: receiptFixture(input.request_id, {execution_state:"queued",queue_id:"queue-one"}) } : {});
  await ui.edit("Another request while busy"); await ui.click('.composer-send-button');
  expect(ui.container.querySelector('[data-queued-count="1"]')).not.toBeNull();
  await ui.remount();
  expect(ui.container.querySelector('[data-queued-count="1"]')).not.toBeNull();
  await act(async () => ui.publish({ kind: "notification", message: { method: "turn/started", params: { thread_id: "s-private", queue_id: "queue-one", turn: { id: "next" } } } }));
  expect(ui.container.querySelector('[data-queued-count="1"]')).toBeNull();
  expect(ui.invoke.mock.calls.filter(([request]) => request.method === "agents.send")).toHaveLength(1);
});

it("settles two consecutive streamed turns by their durable sequence without matching text", async () => {
  let records: unknown[] = [];
  const ui = await mount(undefined, main, undefined, { history: async () => ({ records }) });
  const publish = async (turnID: string, method: string, params: Record<string, unknown>) => act(async () => ui.publish({ kind: "notification", message: { method, params: { thread_id: "s-private", turn_id: turnID, ...params } } }));
  for (const [index, seq] of [10, 20].entries()) {
    const turnID = `terminal-turn-${index}`;
    await publish(turnID, "item/agentMessage/delta", { item_id: "answer", delta: "Identical answer" });
    await publish(turnID, "item/completed", { item: { id: "answer", type: "agent_message", text: "Identical answer", status: "completed" } });
    records = [...records, { seq, role: "assistant", content: "Identical answer" }];
    await publish(turnID, "turn/completed", { turn: { id: turnID, status: "completed", items: [{ id: "answer", type: "agent_message", seq, text: "Identical answer", status: "completed" }] } });
    expect(ui.container.querySelectorAll('[data-role="assistant"]')).toHaveLength(index + 1);
    await publish(turnID, "item/agentMessage/delta", { item_id: "answer", delta: "stale" });
    expect(ui.container.textContent).not.toContain("stale");
  }
});

it.each(["failed", "interrupted"])("settles persisted partial output after a %s turn without orphaning replaced fragments", async status => {
  let records: unknown[] = [];
  const ui = await mount(undefined, main, undefined, { history: async () => ({ records }) });
  const publish = async (method: string, params: Record<string, unknown>) => act(async () => ui.publish({ kind: "notification", message: { method, params: { thread_id: "s-private", turn_id: "partial-turn", ...params } } }));
  await publish("item/agentMessage/delta", { item_id: "fragment", delta: "Superseded fragment" });
  await publish("item/removed", { item_id: "fragment" });
  expect(ui.container.textContent).not.toContain("Superseded fragment");
  await publish("item/agentMessage/delta", { item_id: "answer", delta: "Retained partial answer" });
  records = [{ seq: 30, role: "assistant", content: "Retained partial answer" }];
  await publish("turn/error", { error: "Turn stopped", turn: { id: "partial-turn", status, items: [{ id: "answer", type: "agent_message", seq: 30, text: "Retained partial answer", status: "completed" }] } });
  expect(ui.container.querySelectorAll('[data-role="assistant"]')).toHaveLength(1);
  expect(ui.container.textContent).toContain("Retained partial answer");
  await publish("item/completed", { item: { id: "answer", type: "agent_message", text: "Retained partial answer", status: "completed" } });
  expect(ui.container.querySelectorAll('[data-role="assistant"]')).toHaveLength(1);
});

it("freezes a failed stop target across a successor turn and route changes", async () => {
  let currentTurn = "turn-a", attempts = 0;
  const ui = await mount(async method => { if (method === "agents.stop_reply" && ++attempts === 1) throw new Error("Stop response lost"); return { cancelled: false }; }, main, [], { open: async () => ({ agent: main, session: { state: "running" }, turn: { state: "running", turn_id: currentTurn } }) });
  await ui.click('.composer-stop-button');
  expect(ui.container.querySelector('[data-wuu-state="retry"]')).not.toBeNull();
  currentTurn = "turn-b";
  await act(async () => ui.publish({ kind: "notification", message: { method: "turn/started", params: { thread_id: "s-private", turn: { id: currentTurn } } } }));
  await ui.remount();
  await ui.click('.composer-stop-button');
  const stops = ui.invoke.mock.calls.filter(([request]) => request.method === "agents.stop_reply");
  expect(stops).toHaveLength(2);
  expect(stops.every(([request]) => (request.input as any).turn_id === "turn-a")).toBe(true);
});

it("guards same-render stop clicks and does not retry successful cancellation after refresh failure", async () => {
  let finishStop: () => void = () => {}, stopped = false;
  const ui = await mount(async method => { if (method === "agents.stop_reply") { await new Promise<void>(resolve => { finishStop = resolve; }); stopped = true; return { cancelled: true }; } return {}; }, main, [], { open: async () => { if (stopped) throw new Error("Refresh unavailable"); return { agent: main, session: { state: "running" }, turn: { state: "running", turn_id: "turn-a" } }; } });
  const stop = ui.container.querySelector<HTMLButtonElement>('.composer-stop-button')!;
  await act(async () => { stop.click(); stop.click(); });
  expect(ui.invoke.mock.calls.filter(([request]) => request.method === "agents.stop_reply")).toHaveLength(1);
  await act(async () => finishStop());
  expect(ui.container.querySelector('[data-wuu-state="retry"]')).toBeNull();
  expect(ui.container.querySelector('[role="alert"]')?.textContent).toContain("Refresh unavailable");
});

it("does not cancel an unknown current turn while send admission is unresolved", async () => {
  let finishSend: () => void = () => {};
  const ui = await mount(async method => { if (method === "agents.send") await new Promise<void>(resolve => { finishSend = resolve; }); return {}; });
  await ui.edit("Start a request"); await ui.click('.composer-send-button');
  const stop = ui.container.querySelector<HTMLButtonElement>('.composer-stop-button');
  if (stop) await act(async () => stop.click());
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "agents.stop_reply")).toBe(false);
  await act(async () => finishSend());
});

it.each([true, false])("holds and submits a custom Personal offer (reply_to_tool=%s) without consuming the composer draft", async replyToTool => {
  const prior = window.wuu;
  const relation = { metadata: { task_links: [{ task_id: "t1", requirements_revision: 1, question_request_id: "offer-one" }] } };
  const question = { relation, request_id: "offer-one", plugin_id: "personal", execution_id: "exec", thread_id: "hidden-task", turn_id: "turn-offer", created_at: "2026-10-19T08:00:00Z", mode: "offer", reply_to_tool: replyToTool, expires_at: new Date(Date.now() + 60_000).toISOString(), questions: [{ id: "destination", header: "Choose a destination", question: "Which folder?", allow_custom: true, options: [{ label: "Travel", description: "Travel folder" }] }] };
  const hold = vi.fn().mockResolvedValue({ request_id: question.request_id, resolved: false });
  const answer = vi.fn().mockResolvedValue({ request_id: question.request_id, resolved: true });
  const cancel = vi.fn().mockResolvedValue({ request_id: question.request_id, resolved: true });
  Object.defineProperty(window, "wuu", { configurable: true, value: { ...prior, listUserQuestions: vi.fn().mockResolvedValue({ questions: [question] }), answerUserQuestion: answer, cancelUserQuestion: cancel, holdUserQuestion: hold, onServerEvent: vi.fn(() => () => {}) } });
  cleanups.push(() => Object.defineProperty(window, "wuu", { configurable: true, value: prior }));
  attachmentBrowser(); const ui = await mount();
  await ui.edit("Preserve unrelated draft"); await chooseFiles(ui.container, [new File(["hello"], "draft.txt", { type: "text/plain" })]);
  await vi.waitFor(() => expect(ui.container.querySelector('.composer-attachment-tray')?.textContent).toContain("draft.txt"));
  await ui.click('.user-question-offer-custom');
  const context = { kind: "user", user_space_id: "local", product_domain: "personal", cwd: "" };
  expect(hold).toHaveBeenCalledWith("offer-one", context);
  expect(ui.container.querySelector('.user-question-card')).not.toBeNull();
  const input = ui.container.querySelector<HTMLInputElement>('.user-question-offer-input')!;
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "Projects"); input.dispatchEvent(new Event("input", { bubbles: true })); });
  await ui.click('.user-question-submit');
  if (replyToTool) {
    expect(answer).toHaveBeenCalledWith("offer-one", { answers: [{ id: "destination", selected: [], custom: "Projects" }] }, context);
    expect(ui.invoke.mock.calls.some(([request]) => request.method === "agents.send")).toBe(false);
  } else {
    expect(cancel).toHaveBeenCalledWith("offer-one", context);
    const admission = ui.invoke.mock.calls.find(([request]) => request.method === "agents.send")![0];
    expect(admission.input).toEqual({ agent_id: "a-main", request_id: "question-offer-one", prompt: "Projects", relation });
    expect(admission).toEqual(expect.objectContaining({ userInput: { requestId: "question-offer-one", prompt: "Projects", relation } }));
  }
  expect(ui.container.querySelector('.user-question-card')).toBeNull();
  expect(ui.container.querySelector('textarea')!.value).toBe("Preserve unrelated draft");
  expect(ui.container.querySelector('.composer-attachment-tray')?.textContent).toContain("draft.txt");
});

it.each([{ operations: [] }, { operations: [{ operation_id: "done", status: "completed" }, { operation_id: "cancelled", status: "cancelled" }] }])("keeps ordinary chat free of calendar entry points when no pending operation exists (%j)", async ({ operations }) => {
  const ui = await mount(async method => {
    if (method === "calendar.status") return { state: "unconnected", read_only: true };
    if (method === "calendar.operations.list") return { operations };
    return {};
  }, main, undefined, { calendarView: true });
  expect(ui.container.querySelector('[data-calendar-proposals]')).toBeNull();
  expect(ui.container.querySelector('[data-action="review-calendar"], [data-calendar-read-only]')).toBeNull();
  expect(ui.controller.getSnapshot().views.some(view => view.region === "overlay")).toBe(false);
  expect(ui.invoke.mock.calls.some(([request]) => /calendar\.operations\.(confirm|cancel)/.test(request.method))).toBe(false);
});

it("opens a selected Calendar proposal as a contextual overlay without changing the main conversation", async () => {
  const ui = await mount(async method => {
    if (method === "calendar.status") return { state: "unconnected", read_only: true };
    if (method === "calendar.operations.list") return { operations: [{ operation_id: "calendar-a", status: "proposed", fields: { summary: "Synthetic calendar proposal" } }, { operation_id: "calendar-b", status: "uncertain", fields: { summary: "Synthetic unconfirmed outcome" } }] };
    return {};
  }, main, undefined, { calendarView: true });
  await ui.edit("Keep the main draft");
  const conversation = ui.container.querySelector('.plugin-conversation');
  expect(ui.container.querySelector('[data-calendar-read-only], [data-action="review-calendar"]')).toBeNull();
  expect(ui.container.querySelector('.plugin-conversation [data-calendar-operation="calendar-a"]')).not.toBeNull();
  expect(ui.controller.getSnapshot().views.some(view => view.region === "overlay")).toBe(false);
  for (const operationId of ["calendar-a", "calendar-b"]) {
    await ui.click(`[data-calendar-operation="${operationId}"]`);
    const overlay = ui.controller.getSnapshot().views.find(view => view.region === "overlay")!;
    expect(overlay).toEqual(expect.objectContaining({ viewTypeId: "personal-calendar.confirmation", context: { operationId }, persistence: "session" }));
    expect(ui.container.querySelector('.plugin-conversation')).toBe(conversation);
    expect(ui.container.querySelector('textarea')!.value).toBe("Keep the main draft");
    await act(async () => ui.controller.closeView(overlay.id));
  }
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "calendar.operations.list", input: { agent_id: "a-main" } }));
  expect(ui.invoke.mock.calls.some(([request]) => /calendar\.operations\.(confirm|cancel)/.test(request.method))).toBe(false);
});

it("keeps non-blocking offer option admission retryable by question ID when broker resolution fails", async () => {
  const prior = window.wuu;
  const question = { request_id: "offer-retry", plugin_id: "personal", execution_id: "exec", thread_id: "hidden-task", turn_id: "turn-offer", created_at: "2026-10-19T08:00:00Z", mode: "offer", questions: [{ id: "destination", question: "Which folder?", options: [{ label: "Travel" }] }] };
  const cancel = vi.fn().mockRejectedValueOnce(new Error("Resolution response lost")).mockResolvedValue({ resolved: true });
  Object.defineProperty(window, "wuu", { configurable: true, value: { ...prior, listUserQuestions: vi.fn().mockResolvedValue({ questions: [question] }), cancelUserQuestion: cancel, onServerEvent: vi.fn(() => () => {}) } });
  cleanups.push(() => Object.defineProperty(window, "wuu", { configurable: true, value: prior }));
  const ui = await mount();
  await ui.click('.user-question-option');
  expect(ui.container.querySelector('.user-question-card [role="alert"]')?.textContent).toContain("Resolution response lost");
  await ui.click('.user-question-option');
  const sends = ui.invoke.mock.calls.filter(([request]) => request.method === "agents.send");
  expect(sends).toHaveLength(2);
  expect(sends.map(([request]) => request.input)).toEqual([1, 2].map(() => ({ agent_id: "a-main", request_id: "question-offer-retry", prompt: "Which folder?\nTravel" })));
  expect(ui.container.querySelector('.user-question-card')).toBeNull();
});

it("presents the known replacement-stop progress consistently while preserving ordinary task updates", async () => {
  const internalProgress = "Previous execution is still stopping; replacement dispatch is held until cancellation is confirmed";
  const ordinaryProgress = "Checked two schedules; waiting for the third response";
  const ui = await mount(undefined, main, undefined, { tasks: [{ task_id: "replacement", title: "Applying revised request", state: "waiting_external", progress: internalProgress, revision: 4, requirements_revision: 2 }, { task_id: "ordinary", title: "Review schedules", state: "running", progress: ordinaryProgress, revision: 2, requirements_revision: 1 }] });
  const visibleProgress = ui.container.querySelector('[data-task-id="replacement"] .mosaic-task-meta')!.textContent;
  expect(visibleProgress).not.toBe(internalProgress);
  expect(ui.container.querySelector('[data-task-id="ordinary"]')?.textContent).toContain(ordinaryProgress);
  await ui.click('[data-task-id="replacement"]');
  expect(ui.container.querySelector('[data-task-detail] .turn-process-entry .agent-text')?.textContent).toBe(visibleProgress);
  expect(ui.container.textContent).not.toContain(internalProgress);
});

it("persists reactions without sending text and shares updates/removal with the reply view", async () => {
  let revision = 0;
  const ui = await mount(async (method, input) => method === "agents.react" ? { session_id: "s-private", message_seq: input.message_seq, reactions: { owner: input.emoji ?? undefined, revision: ++revision } } : {}, main, [{ seq: 1, role: "user", content: "Done", reactions: { agent: "✅", revision: 1 } }, { seq: 2, role: "assistant", content: "Got it", reaction_eligible: true }]);
  expect(ui.container.querySelector('[data-role="user"] [data-reaction-badge]')?.textContent).toBe("✅");
  await ui.click('[data-action="react-message"]'); await ui.click('[data-emoji="❤️"]', document);
  expect(ui.container.querySelector('[data-role="assistant"] [data-reaction-badge]')?.textContent).toBe("❤️");
  await ui.click('[data-action="reply-message"]');
  expect(ui.container.querySelector('.mosaic-replies [data-reaction-badge]')?.textContent).toBe("❤️");
  await ui.click('.mosaic-replies [data-action="react-message"]'); await ui.click('[data-emoji="❤️"]', document);
  expect(ui.container.querySelector('[data-role="assistant"] [data-reaction-badge]')).toBeNull();
  expect(ui.invoke.mock.calls.filter(([request]) => request.method === "agents.react").map(([request]) => request.input)).toEqual([
    expect.objectContaining({ agent_id: "a-main", session_id: "s-private", message_seq: 2, emoji: "❤️", expected_revision: 0, request_id: expect.any(String) }),
    expect.objectContaining({ message_seq: 2, emoji: null, expected_revision: 1 })]);
  expect(ui.invoke.mock.calls.some(([request]) => ["agents.send", "tasks.start", "tasks.revise"].includes(request.method))).toBe(false);
});
it("rolls back failed reactions and keeps newer revisions when old history refreshes arrive", async () => {
  let fail = false;
  const ui = await mount(async (method, input) => { if (method === "agents.react") { if (fail) throw new Error("Reaction offline"); return { session_id: "s-private", message_seq: 1, reactions: { owner: input.emoji, revision: 2 } }; } return {}; }, main, [{ seq: 1, role: "assistant", content: "Hello", reaction_eligible: true, reactions: { owner: "👍", revision: 1 } }]);
  await ui.click('[data-action="react-message"]'); await ui.click('[data-emoji="❤️"]', document);
  await act(async () => ui.publish());
  expect(ui.container.querySelector('[data-reaction-badge]')?.textContent).toBe("❤️");
  fail = true; await ui.click('[data-action="react-message"]'); await ui.click('[data-emoji="✅"]', document);
  expect(ui.container.querySelector('[data-reaction-badge]')?.textContent).toBe("❤️");
  expect(ui.container.querySelector('[role="alert"]')?.textContent).toContain("Reaction offline");
});
it("refreshes older loaded reactions from scoped invalidations and rejects other plugin generations", async () => {
  let state = { agent: "✅", revision: 1 };
  const ui = await mount(async (method, input) => method === "agents.reactions" ? {session_id: "s-private", records: input.message_seqs.map((seq: number) => ({ seq, reactions: state }))} : {}, main, [{seq: 1, role: "user", content: "Done", reactions: state}]);
  const event = (fingerprint: string) => ({kind:"notification",message:{method:"plugin/client/invalidated",params:{plugin_id:"personal",fingerprint,user_space_id:"local",product_domain:"personal",topic:"reactions"}}});
  state = {agent:"❤️",revision:2}; await act(async () => ui.publish(event("wrong")));
  expect(ui.container.querySelector('[data-reaction-badge]')?.textContent).toBe("✅");
  await act(async () => ui.publish(event("one")));
  expect(ui.container.querySelector('[data-reaction-badge]')?.textContent).toBe("❤️");
});
it("never offers mutation controls for ineligible or unverified persisted records", async () => {
  const ui = await mount(undefined, main, [{seq:1,role:"assistant",content:"Truncated",reaction_eligible:false},{seq:2,role:"assistant",content:"Unverified"}]);
  expect(ui.container.querySelector('[data-action="react-message"]')).toBeNull();
});
it("refreshes history without resending an accepted attachment-only message", async () => {
  attachmentBrowser(); let accepted = false, failHistory = true;
  const ui = await mount(async (method, input) => { if (method === "agents.send") { accepted = true; return { state: "queued", receipt: receiptFixture(input.request_id) }; } }, main, undefined, { history: async () => { if (accepted && failHistory) throw new Error("History unavailable"); return { records: [] }; } });
  await chooseFiles(ui.container, [new File(["hello"], "notes.txt", { type: "text/plain" })]);
  await vi.waitFor(() => expect(ui.container.querySelector('.composer-attachment-tray')).not.toBeNull());
  await ui.click('.composer-send-button');
  expect(ui.container.querySelector('.composer-attachment-tray')).toBeNull();
  expect(ui.container.querySelector('textarea')!.value).toBe("");
  expect(ui.container.querySelector('[data-send-refresh-error]')).not.toBeNull();
  failHistory = false; await ui.click('[data-send-refresh-error] button');
  expect(ui.invoke.mock.calls.filter(([request]) => request.method === "agents.send")).toHaveLength(1);
  expect(ui.container.querySelector('[data-send-refresh-error]')).toBeNull();
});
it("keeps newer quoted drafts and files separate from accepted sends when refresh fails", async () => {
  attachmentBrowser(); let finish!: (value: unknown) => void, accepted = false;
  const pending = new Promise(resolve => { finish = resolve; });
  const records = [{ seq: 1, role: "assistant", content: "Quoted source" }];
  const ui = await mount(async method => method === "agents.send" ? pending : {}, main, records, { history: async input => { if (accepted && input.reply_seq === undefined) throw new Error("History unavailable"); return { records, reply_root_seq: 1, has_earlier: false }; } });
  await ui.edit("Independent main draft"); await ui.click('[data-action="reply-message"]'); await ui.edit("Accepted reply");
  await ui.click('.composer-send-button'); await ui.edit("Newer reply draft");
  await chooseFiles(ui.container.querySelector('.mosaic-replies')!, [new File(["hello"], "new.txt", { type: "text/plain" })]);
  await vi.waitFor(() => expect(ui.container.querySelector('.mosaic-replies .composer-attachment-tray')).not.toBeNull());
  await act(async () => { accepted = true; finish({ state: "queued", receipt:receiptFixture((ui.invoke.mock.calls.find(([r])=>r.method==="agents.send")![0].input as any).request_id) }); await pending; });
  expect(ui.container.querySelector('.mosaic-replies .mosaic-reply-status[role="alert"]')).toBeNull();
  expect(ui.container.querySelector<HTMLTextAreaElement>('.mosaic-replies textarea')!.value).toBe("Newer reply draft");
  expect(ui.container.querySelector('.mosaic-replies .composer-attachment-tray')?.textContent).toContain("new.txt");
  expect(ui.container.querySelector('[data-send-refresh-error]')?.closest('[inert]')).toBeNull();
  const sends = ui.invoke.mock.calls.filter(([request]) => request.method === "agents.send");
  expect(sends).toHaveLength(1); expect(sends[0][0].input).toEqual(expect.objectContaining({ prompt: "Accepted reply", relation: { reply_to: { session_id: "s-private", seq: 1 } } }));
  await ui.click('[data-action="close-replies"]');
  expect(ui.container.querySelector('textarea')!.value).toBe("Independent main draft");
  await ui.click('[data-action="reply-message"]');
  expect(ui.container.querySelector<HTMLTextAreaElement>('.mosaic-replies textarea')!.value).toBe("Newer reply draft");
});

it("recovers main, message and task drafts after a fresh plugin generation without sending", async () => {
  const first = await mount();
  await first.edit("Main recovery");
  await first.click('[data-action="reply-message"]'); await first.edit("Message recovery");
  await first.click('[data-action="close-replies"]'); await first.click('[data-task-id="t1"]');
  await first.click('[data-action="reply-task"]'); await first.edit("Task recovery"); first.dispose();
  const next = await mount();
  expect(next.container.querySelector('textarea')!.value).toBe("Main recovery");
  await next.click('[data-action="reply-message"]');
  expect(next.container.querySelector('.mosaic-replies textarea')).toHaveProperty("value", "Message recovery");
  await next.click('[data-action="close-replies"]'); await next.click('[data-task-id="t1"]'); await next.click('[data-action="reply-task"]');
  expect(next.container.querySelector('.mosaic-replies textarea')).toHaveProperty("value", "Task recovery");
  expect(next.invoke.mock.calls.some(([r]) => r.method === "agents.send")).toBe(false);
  next.dispose();
  const other = await mount(undefined, { ...main, main_session_id: "other-session" });
  expect(other.container.querySelector('textarea')!.value).toBe("");
});
it("reuses uncertain text admission after restart and clears accepted or explicitly discarded recovery", async () => {
  const first = await mount(async method => { if (method === "agents.send") throw new Error("Lost response"); });
  await first.edit("Retry after restart"); await first.click('.composer-send-button');
  const original = first.invoke.mock.calls.find(([r]) => r.method === "agents.send")![0].input;
  first.dispose();
  const next = await mount(async (method,input)=>method==="agents.send"?{state:"queued",receipt:receiptFixture(input.request_id)}:{}); await next.click('.composer-send-button');
  expect(next.invoke.mock.calls.find(([r]) => r.method === "agents.send")![0].input).toEqual(original);
  next.dispose(); const accepted = await mount(); expect(accepted.container.querySelector('textarea')!.value).toBe("");
  await accepted.edit("Discard me"); await accepted.edit(""); accepted.dispose();
  const cleared = await mount(); expect(cleared.container.querySelector('textarea')!.value).toBe("");
});
it("requires attachment reselection after restart and never stores attachment bytes or source excerpts", async () => {
  attachmentBrowser();
  const first = await mount(undefined, main, [{ seq: 1, role: "assistant", content: "Private original excerpt" }]);
  await first.click('[data-action="reply-message"]'); await first.edit("Text survives");
  await chooseFiles(first.container, [new File(["sensitive file bytes"], "secret.txt", { type: "text/plain" })]);
  await vi.waitFor(() => expect(first.container.querySelector('.composer-attachment-tray')).not.toBeNull());
  const stored = Object.values(localStorage).join("");
  expect(stored).not.toContain("c2Vuc2l0aXZlIGZpbGUgYnl0ZXM="); expect(stored).not.toContain("Private original excerpt");
  first.dispose(); const next = await mount(); await next.click('[data-action="reply-message"]');
  expect(next.container.querySelector('.mosaic-replies textarea')).toHaveProperty("value", "Text survives");
  expect(next.container.querySelector('.mosaic-replies [data-draft-attachments-missing]')).not.toBeNull();
  expect(next.container.querySelector('.mosaic-replies .composer-attachment-tray')).toBeNull();
  expect(next.container.querySelector('.mosaic-replies .composer-send-button')).toHaveProperty("disabled", true);
  await next.click('[data-action="continue-without-attachments"]', next.container.querySelector('.mosaic-replies')!);
  await next.click('.composer-send-button');
  expect(next.invoke.mock.calls.find(([r]) => r.method === "agents.send")![0].input).not.toHaveProperty("attachments");
});
it("keeps live text usable and reports storage failures, then retries saving", async () => {
  const ui = await mount();
  const failure = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("Quota exceeded"); });
  await ui.edit("Keep live text");
  expect(ui.container.querySelector('textarea')!.value).toBe("Keep live text");
  expect(ui.container.querySelector('[data-draft-storage-error]')).not.toBeNull();
  failure.mockRestore(); await ui.click('[data-action="retry-draft-save"]'); ui.dispose();
  const next = await mount(); expect(next.container.querySelector('textarea')!.value).toBe("Keep live text");
});
it("does not revive expired, malformed, or oversized drafts", async () => {
  const first = await mount(); await first.edit("Expire this"); first.dispose();
  const key = Object.keys(localStorage).find(key => key.startsWith("wuu.personal.draftRecovery.v1:"))!;
  expect(key).toBeTruthy();
  const value = JSON.parse(localStorage.getItem(key)!); value.updatedAt = Date.now() - 25 * 60 * 60 * 1000;
  localStorage.setItem(key, JSON.stringify(value));
  const expired = await mount(); expect(expired.container.querySelector('textarea')!.value).toBe(""); expired.dispose();
  localStorage.setItem(key, "broken JSON"); const broken = await mount(); expect(broken.container.querySelector('textarea')!.value).toBe("");
  await broken.edit("x".repeat(33 * 1024));
  expect(broken.container.querySelector('[data-draft-storage-error]')).not.toBeNull();
  broken.dispose(); const oversized = await mount(); expect(oversized.container.querySelector('textarea')!.value).toBe("");
});
it("allows explicit source retry after a transient failure without reopening the plugin", async () => {
  let attempts = 0;
  const rows = [{ seq: 20, role: "user", content: "Follow-up", relation: { reply_to: { session_id: "s-private", seq: 3 } } }];
  const ui = await mount(undefined, main, undefined, { history: async input => {
    if (input.start_seq === 3) { if (++attempts === 1) throw new Error("Offline"); return { records: [{ seq: 3, role: "assistant", content: "Recovered original" }] }; }
    return { records: rows };
  } });
  await vi.waitFor(() => expect(ui.container.querySelector('[data-reply-reference]')?.getAttribute('data-unavailable')).toBe('true'));
  await ui.click('[data-reply-reference]');
  await vi.waitFor(() => expect(ui.container.querySelector('[data-reply-reference]')?.textContent).toBe("Recovered original"));
  expect(attempts).toBe(2);
});
it("retries empty source lookups after view reentry while retaining successful sources", async () => {
  let attempts = 0;
  const rows = [{ seq: 20, role: "user", content: "Follow-up", relation: { reply_to: { session_id: "s-private", seq: 3 } } }];
  const ui = await mount(undefined, main, undefined, { history: async input => input.start_seq === 3 ? (++attempts === 1 ? { records: [] } : { records: [{ seq: 3, role: "assistant", content: "Now available" }] }) : { records: rows } });
  await ui.remount(); await vi.waitFor(() => expect(ui.container.querySelector('[data-reply-reference]')?.textContent).toBe("Now available"));
  await ui.remount(); expect(attempts).toBe(2);
});
it("recovers an unread saved draft when storage returns without replacing it with empty input", async () => {
  const first = await mount(); await first.edit("Previously saved input"); first.dispose();
  const read = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("Storage unavailable"); });
  const next = await mount();
  expect(next.container.querySelector('[data-draft-storage-error]')).not.toBeNull();
  read.mockRestore(); await next.click('[data-action="retry-draft-save"]');
  expect(next.container.querySelector('textarea')!.value).toBe("Previously saved input");
});
it("keeps the explicit reply target when recovery storage cannot be read", async () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("Storage unavailable"); });
  const ui = await mount(); await ui.click('[data-action="reply-message"]'); await ui.edit("Reply despite storage failure");
  await ui.click('.composer-send-button');
  expect(ui.invoke.mock.calls.find(([r]) => r.method === "agents.send")![0].input).toMatchObject({ relation: { reply_to: { session_id: "s-private", seq: 1 } } });
});
it("never deletes saved drafts when retention cleanup cannot read storage", async () => {
  const first = await mount(); await first.edit("Keep this stored draft");
  const other = await mount(undefined, { ...main, main_session_id: "second-session" });
  const read = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("Temporary read failure"); });
  await other.edit("Another stored draft"); read.mockRestore(); first.dispose(); other.dispose();
  const restored = await mount(); expect(restored.container.querySelector('textarea')!.value).toBe("Keep this stored draft");
});

const receiptFixture = (request = "client", overrides: Record<string, unknown> = {}) => ({ request_id: request, host_request_id: `host-${request}`, receipt_id: `receipt-${request}`, client_id: `client-${request}`, queue_id: `queue-${request}`, session_id: main.main_session_id, plugin_id: "personal", receipt_state: "accepted", execution_state: "held", accepted_at: "2026-10-23T08:00:00Z", revision: 2, ...overrides });
it("keeps acceptance unknown until request status proves durable delivery", async () => {
 let recovered = false;
 const ui = await mount(async (method, input) => {
  if (method === "agents.send") throw new Error("ACK lost");
  if (method === "agents.messages.status") return recovered ? { receipt: receiptFixture(input.request_id) } : {};
  return {};
 });
 await ui.edit("Keep this request");await ui.click(".composer-send-button");
 expect(ui.container.querySelector("textarea")!.value).toBe("Keep this request");
 expect(ui.container.querySelector('[data-receipt-state="unknown"]')).not.toBeNull();
 const first = ui.invoke.mock.calls.find(([r]) => r.method === "agents.send")![0].input;
 recovered=true;await ui.click(".composer-send-button");
 expect(ui.container.querySelector("textarea")!.value).toBe("");
 expect(ui.invoke.mock.calls.filter(([r]) => r.method === "agents.send")).toHaveLength(1);
 expect(ui.invoke.mock.calls.filter(([r]) => r.method === "agents.messages.status").at(-1)![0].input).toEqual(expect.objectContaining({request_id:(first as any).request_id}));
});
it("restores Held inputs by exact identity and resumes only on an explicit action", async () => {
 const receipt=receiptFixture();let state="held";
 const ui = await mount(async method => {if(method==="agents.messages.resume") {state="running";return {receipt:{...receipt,execution_state:state,revision:3}};}return {};},main,[],{open:async()=>({agent:main,session:{state:"completed"},pending_inputs:state==="held"?[{receipt,prompt:"Saved input",attachments:[{filename:"note.txt",media_type:"text/plain",sha256:"a".repeat(64)}]}]:[]})});
 expect(ui.container.textContent).toContain("Saved input");expect(ui.container.textContent).toContain("note.txt");
 expect(ui.invoke.mock.calls.some(([r])=>r.method==="agents.send"||r.method==="agents.messages.resume")).toBe(false);
 await ui.click('[data-action="resume-accepted-input"]');
 expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({method:"agents.messages.resume",input:expect.objectContaining({receipt_id:receipt.receipt_id,request_id:receipt.request_id}),userInput:{requestId:receipt.request_id,prompt:"",resumeReceiptId:receipt.receipt_id}}));
});
it("keeps delivered execution failures distinct and ignores older receipt revisions", async () => {
 const receipt=receiptFixture("same",{receipt_state:"in_history",execution_state:"failed",message_seq:2,hostturn_id:"turn",revision:5,error_stage:"execution"});let older=false;
 const ui=await mount(undefined,main,undefined,{history:async()=>({records:[{seq:2,role:"user",content:"Saved",receipt:older?{...receipt,execution_state:"running",revision:3}:receipt}]})});
 expect(ui.container.querySelector('[data-receipt-state="failed"]')?.textContent).toContain("已送达");
 older=true;await act(async()=>ui.publish());
 expect(ui.container.querySelector('[data-receipt-state="failed"]')).not.toBeNull();
 expect(ui.container.textContent).not.toContain("Seen");
});

it("reconciles remote cancellation by request without erasing other Held inputs", async () => {
 const cancelled=receiptFixture("cancel-me"), kept=receiptFixture("keep-me");let remote=false;
 const ui=await mount(async(method,input)=>method==="agents.messages.status"?{receipt:input.request_id==="cancel-me"?{...cancelled,execution_state:"cancelled",revision:3}:kept}:{},main,[],{open:async()=>({agent:main,session:{state:"completed"},pending_inputs:(remote?[kept]:[cancelled,kept]).map(receipt=>({receipt,prompt:receipt.request_id}))})});
 expect(ui.container.querySelectorAll('[data-receipt-state="held"]')).toHaveLength(2);
 remote=true;await act(async()=>ui.publish());
 await vi.waitFor(()=>expect(ui.container.querySelector('[data-receipt-id="receipt-cancel-me"]')).toHaveProperty("dataset.receiptState","cancelled"));
 expect(ui.container.querySelector('[data-receipt-id="receipt-keep-me"]')).toHaveProperty("dataset.receiptState","held");
 expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({method:"agents.messages.status",input:expect.objectContaining({request_id:"cancel-me"})}));
 expect(ui.invoke.mock.calls.some(([r])=>r.method==="agents.messages.resume"||r.method==="agents.send")).toBe(false);
});
it("keeps an omitted pending receipt until status proves a newer terminal state", async () => {
 const receipt=receiptFixture("missing");let remote=false,available=false;
 const ui=await mount(async method=>{if(method==="agents.messages.status"){if(!available)throw new Error("status offline");return {receipt:{...receipt,execution_state:"failed",revision:4,receipt_state:"in_history",message_seq:9,hostturn_id:"actual"}};}return {};},main,[{seq:9,role:"user",content:"same request",receipt}],{open:async()=>({agent:main,session:{state:"completed"},pending_inputs:remote?[]:[{receipt,prompt:"same request"}]})});
 remote=true;await act(async()=>ui.publish());expect(ui.container.querySelector('[data-receipt-id="receipt-missing"]')).toHaveProperty("dataset.receiptState","held");
 available=true;await act(async()=>ui.publish());await vi.waitFor(()=>expect(ui.container.querySelector('[data-receipt-id="receipt-missing"]')).toHaveProperty("dataset.receiptState","failed"));
});

it("converges a peer cancellation without dropping other pending inputs", async () => {
 const first=receiptFixture("first"),other=receiptFixture("other");let cancelled=false;
 const ui=await mount(async(method,input)=>method==="agents.messages.status"?{receipt:input.request_id==="first"?{...first,execution_state:cancelled?"cancelled":"held",revision:cancelled?3:2}:other}:{},main,[],{open:async()=>({agent:main,session:{state:"completed"},pending_inputs:[...(!cancelled?[{receipt:first,prompt:"Cancelled on peer"}]:[]),{receipt:other,prompt:"Keep this separate"}]})});
 expect(ui.container.querySelectorAll('[data-receipt-state="held"]')).toHaveLength(2);
 cancelled=true;await act(async()=>ui.publish());
 await vi.waitFor(()=>expect(ui.container.querySelector('[data-receipt-id="receipt-first"]')).toHaveProperty("dataset.receiptState","cancelled"));
 expect(ui.container.querySelector('[data-receipt-id="receipt-other"]')).toHaveProperty("dataset.receiptState","held");
 expect(ui.container.textContent).toContain("Keep this separate");
 expect(ui.invoke.mock.calls.filter(([r])=>r.method==="agents.messages.status").map(([r])=>(r.input as any).request_id)).toContain("first");
 expect(ui.invoke.mock.calls.some(([r])=>["agents.send","agents.messages.resume","agents.messages.cancel"].includes(r.method))).toBe(false);
});

it("publishes new history and other terminal receipts when one status lookup fails", async () => {
 const failing=receiptFixture("offline"),cancelled=receiptFixture("peer");let refresh=false;
 const ui=await mount(async(method,input)=>{
  if(method!=="agents.messages.status")return {};
  if(input.request_id==="offline")throw new Error("one receipt offline");
  return {receipt:{...cancelled,execution_state:"cancelled",revision:3}};
 },main,[],{open:async()=>({agent:main,session:{state:"completed"},pending_inputs:refresh?[]:[failing,cancelled].map(receipt=>({receipt,prompt:receipt.request_id}))}),history:async()=>({records:refresh?[{seq:7,role:"assistant",content:"New history must publish"}]:[]})});
 refresh=true;await act(async()=>ui.publish());
 expect(ui.container.textContent).toContain("New history must publish");
 expect(ui.container.querySelector('[data-receipt-id="receipt-offline"]')).toHaveProperty("dataset.receiptState","held");
 expect(ui.container.querySelector('[data-receipt-id="receipt-peer"]')).toHaveProperty("dataset.receiptState","cancelled");
 expect(ui.invoke.mock.calls.some(([r])=>r.method==="agents.send"||r.method==="agents.messages.resume")).toBe(false);
});

it("keeps reply Sending, unknown and Held actionable in their own discussion", async () => {
 let reject!: (reason: Error) => void;const pending=new Promise((_,fail)=>{reject=fail;});
 let first=true,current:any;const records:any[]=[{seq:1,role:"assistant",content:"Discussion one"},{seq:9,role:"assistant",content:"Discussion two"}];
 const ui=await mount(async(method,input)=>{
  if(method==="agents.send") {if(first){first=false;return pending;}current=receiptFixture(input.request_id);return {receipt:current};}
  if(method==="agents.messages.status")return {receipt:current};
  if(method==="agents.messages.resume"){current={...current,execution_state:"queued",revision:3};return {receipt:current};}
  if(method==="agents.messages.cancel"){current={...current,execution_state:"cancelled",revision:4};return {receipt:current};}
  return {};
 },main,records);
 await ui.click('[data-action="reply-message"]');await ui.edit("Only first discussion");await ui.click('.composer-send-button');
 expect(ui.container.querySelector('.mosaic-replies [data-receipt-state="sending"]')).not.toBeNull();
 await act(async()=>{reject(new Error("ACK unknown"));await pending.catch(()=>{});});
 expect(ui.container.querySelector('.mosaic-replies [data-receipt-state="unknown"]')).not.toBeNull();
 await ui.click('[data-action="close-replies"]');await ui.remount();
 await act(async()=>ui.container.querySelectorAll<HTMLButtonElement>('.mosaic-main [data-action="reply-message"]')[1].click());
 expect(ui.container.querySelector('.mosaic-replies')!.textContent).not.toContain("Only first discussion");
 await ui.click('[data-action="close-replies"]');await ui.click('[data-action="reply-message"]');
 expect(ui.container.querySelector('.mosaic-replies [data-receipt-state="unknown"]')).not.toBeNull();
 await ui.click('.composer-send-button');
 expect(ui.container.querySelector('.mosaic-replies [data-receipt-state="held"]')).not.toBeNull();
 const sends=ui.invoke.mock.calls.filter(([r])=>r.method==="agents.send");expect((sends[1][0].input as any).request_id).toBe((sends[0][0].input as any).request_id);
 await ui.click('.mosaic-replies [data-action="resume-accepted-input"]');
 expect(ui.container.querySelector('.mosaic-replies [data-receipt-state="queued"]')).not.toBeNull();
 await ui.click('.mosaic-replies [data-action="cancel-accepted-input"]');
 expect(ui.container.querySelector('.mosaic-replies [data-receipt-state="cancelled"]')).not.toBeNull();
 expect(ui.invoke.mock.calls.find(([r])=>r.method==="agents.messages.resume")![0].userInput).toEqual({requestId:current.request_id,prompt:"",resumeReceiptId:current.receipt_id});
});

it("restores only related peer Held inputs in a reply and deduplicates history admission", async () => {
 const ref=(seq:number)=>({reply_to:{session_id:main.main_session_id,seq}});
 const own=receiptFixture("own"),other=receiptFixture("other"),plain=receiptFixture("plain");let admitted=false;
 const root={seq:1,role:"assistant",content:"Canonical discussion"},child={seq:8,role:"assistant",content:"Later answer",relation:ref(1)};
 const ui=await mount(undefined,main,[],{open:async()=>({agent:main,session:{state:"completed"},pending_inputs:admitted?[]:[{receipt:own,prompt:"Related peer input",relation:ref(8)},{receipt:other,prompt:"Other discussion",relation:ref(99)},{receipt:plain,prompt:"Unrelated main input"}]}),history:async input=>({reply_root_seq:1,records:input.reply_seq!==undefined?[root,child,...(admitted?[{seq:10,role:"user",content:"Related peer input",relation:ref(8),receipt:{...own,receipt_state:"in_history",message_seq:10,hostturn_id:"actual",execution_state:"completed",revision:4}}]:[])]:[child,...(admitted?[{seq:10,role:"user",content:"Related peer input",relation:ref(8),receipt:{...own,receipt_state:"in_history",message_seq:10,hostturn_id:"actual",execution_state:"completed",revision:4}}]:[])],has_earlier:false})});
 await ui.click('[data-action="reply-message"]');const panel=()=>ui.container.querySelector('.mosaic-replies')!;
 expect(panel().textContent).toContain("Related peer input");expect(panel().textContent).not.toContain("Other discussion");expect(panel().textContent).not.toContain("Unrelated main input");
 expect(panel().querySelectorAll('[data-receipt-state="held"]')).toHaveLength(1);
 admitted=true;await act(async()=>ui.publish());
 await vi.waitFor(()=>expect(panel().querySelectorAll('[data-receipt-id="receipt-own"]')).toHaveLength(1));
 expect(panel().querySelector('[data-receipt-id="receipt-own"]')).toHaveProperty("dataset.receiptState","completed");
});

it("limits task reply pending receipts to the current task revision and execution", async () => {
 const link={task_id:"t1",requirements_revision:2,execution_session_id:"run-two"};
 const task={task_id:"t1",title:"Current task",goal:"Review",state:"running",revision:3,requirements_revision:2,execution:{session_id:"run-two"}};
 const rows=[{id:"current",link},{id:"old-revision",link:{...link,requirements_revision:1}},{id:"old-execution",link:{...link,execution_session_id:"run-one"}},{id:"other-task",link:{...link,task_id:"t2"}}];
 const ui=await mount(undefined,main,[],{tasks:[task],open:async()=>({agent:main,session:{state:"completed"},pending_inputs:rows.map(({id,link})=>({receipt:receiptFixture(id),prompt:id,relation:{metadata:{task_links:[link]}}}))})});
 await ui.click('[data-task-id="t1"]');await ui.click('[data-action="reply-task"]');
 const panel=ui.container.querySelector('.mosaic-replies')!;
 expect(panel.querySelector('[data-receipt-id="receipt-current"]')).not.toBeNull();
 expect(panel.querySelectorAll('[data-receipt-id]')).toHaveLength(1);
 expect(panel.querySelector('[data-action="resume-accepted-input"]')).not.toBeNull();
 expect(panel.querySelector('[data-action="cancel-accepted-input"]')).not.toBeNull();
});

it("does not let uncertain draft status failure suppress a peer terminal state or history", async () => {
 const peer=receiptFixture("peer");let refresh=false;
 const ui=await mount(async(method,input)=>{
  if(method==="agents.send")throw new Error("transport unknown");
  if(method==="agents.messages.status") {if(input.request_id==="peer")return {receipt:{...peer,execution_state:"cancelled",revision:3}};throw new Error("draft status offline");}
  return {};
 },main,[],{open:async()=>({agent:main,session:{state:"completed"},pending_inputs:refresh?[]:[{receipt:peer,prompt:"Peer pending"}]}),history:async()=>({records:refresh?[{seq:7,role:"assistant",content:"History after draft lookup failed"}]:[]})});
 await ui.edit("Unconfirmed draft");await ui.click('.composer-send-button');refresh=true;await act(async()=>ui.publish());
 expect(ui.container.textContent).toContain("History after draft lookup failed");
 expect(ui.container.querySelector('[data-receipt-id="receipt-peer"]')).toHaveProperty("dataset.receiptState","cancelled");
 expect(ui.container.querySelector('textarea')!.value).toBe("Unconfirmed draft");
 expect(ui.container.querySelector('[data-receipt-state="unknown"]')).not.toBeNull();
 expect(ui.invoke.mock.calls.filter(([r])=>r.method==="agents.send")).toHaveLength(1);
});

it("renders Personal interactive parts with scoped actions, preserves prose, and keeps local edits quiet", async () => {
  const spec = genuiFixtures.checklist;
  let snapshot: ActionResult = { surface: { schemaVersion: 1, surfaceId: "fixture-surface", messageId: "fixture-message", spec, state: genuiInitialState(spec), revision: 0, submitted: false }, acceptedRevision: 0 };
  const observer = vi.fn(() => <p>Coding observer</p>);
  await desktopPluginHost.activateGeneration({ pluginId: "test:genui-observer", generation: "one", register(api) { api.registerRenderer({ id: "observer", category: "message", match: "text/markdown", render: observer }); } });
  cleanups.push(() => act(() => desktopPluginHost.unload("test:genui-observer")));
  const ui = await mount(async (method, input) => {
    if (method === "genui.action") {
      const payload = input as { agent_id: string; request: ActionRequest };
      expect(payload.agent_id).toBe(main.agent_id);
      expect(payload.request.state.items).toEqual(expect.arrayContaining([expect.objectContaining({ done: true })]));
      snapshot = { surface: { ...snapshot.surface, state: payload.request.state, revision: 1 }, acceptedRevision: 1 };
      return snapshot;
    }
    return {};
  }, main, [{ seq: 2, role: "assistant", content: spec.fallbackText, ui_text: "Here is your editable draft.", ui_parts: [snapshot] }]);
  expect(ui.container.textContent).toContain("Here is your editable draft.");
  expect(ui.container.querySelectorAll(".personal-genui")).toHaveLength(1);
  await ui.click('[data-genui-node="done"]');
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "genui.action")).toBe(false);
  await act(async () => { const button = ui.container.querySelector<HTMLButtonElement>('[data-genui-node="save"]')!; button.click(); button.click(); });
  expect(ui.invoke.mock.calls.filter(([request]) => request.method === "genui.action")).toHaveLength(1);
  expect(ui.container.textContent).toContain("选择已保存");
  expect(observer).not.toHaveBeenCalled();
});

it("requires an explicit reload after a GenUI revision conflict and renders fallback for unsupported parts", async () => {
  const spec = genuiFixtures.calculator;
  const snapshot: ActionResult = { surface: { schemaVersion: 1, surfaceId: "conflict-surface", messageId: "message-conflict", spec, state: genuiInitialState(spec), revision: 0, submitted: false }, acceptedRevision: 0 };
  const ui = await mount(async method => {
    if (method === "genui.action") throw new Error("genui_revision_conflict");
    if (method === "genui.read") return { ...snapshot, surface: { ...snapshot.surface, revision: 1, state: { count: 10, price: 20 } }, acceptedRevision: 1 };
    return {};
  }, main, [{ seq: 2, role: "assistant", content: spec.fallbackText, ui_text: "", ui_parts: [snapshot] }]);
  await ui.click('[data-genui-node="save"]');
  expect(ui.container.querySelector('[role="alert"]')?.textContent).toContain("另一处已更新");
  const reload = [...ui.container.querySelectorAll<HTMLButtonElement>(".personal-genui button")].find(button => button.textContent === "载入已保存选择")!;
  await act(async () => reload.click());
  expect(ui.container.querySelector('[data-genui-node="sum"]')?.textContent).toBe("200");
  const bad = structuredClone(snapshot); Reflect.set(bad.surface.spec, "schemaVersion", 99);
  const fallback = await mount(undefined, main, [{ seq: 3, role: "assistant", content: spec.fallbackText, ui_parts: [bad] }]);
  expect(fallback.container.querySelectorAll(".personal-genui")).toHaveLength(0);
  expect(fallback.container.textContent).toContain(spec.fallbackText);
});

it("binds GenUI submit to authenticated durable input and resumes held state only after an explicit continue", async () => {
  const spec = genuiFixtures.calculator;
  let snapshot: ActionResult = { surface: { schemaVersion: 1, surfaceId: "submit-surface", messageId: "message-submit", spec, state: genuiInitialState(spec), revision: 0, submitted: false }, acceptedRevision: 0 };
  const ui = await mount(async (method, input) => {
    if (method === "genui.action") {
      const request = (input as { request: ActionRequest }).request;
      snapshot = { surface: { ...snapshot.surface, revision: 1, submitted: true, state: request.state }, acceptedRevision: 1, delivery: "pending", submission: { requestId: request.eventId, prompt: "Synthetic explicit input" } };
      return snapshot;
    }
    if (method === "genui.retry") {
      snapshot = input.receipt_id ? { ...snapshot, delivery: "accepted", submission: undefined } : { ...snapshot, delivery: "held", submission: { requestId: snapshot.submission!.requestId, prompt: "", resumeReceiptId: "receipt-held" } };
      if (input.receipt_id) delete snapshot.submission;
      return snapshot;
    }
    if (method === "genui.read") return snapshot;
    return {};
  }, main, [{ seq: 2, role: "assistant", content: spec.fallbackText, ui_parts: [snapshot] }]);
  await act(async () => { const button = ui.container.querySelector<HTMLButtonElement>('[data-genui-node="submit"]')!; button.click(); button.click(); });
  const first = ui.invoke.mock.calls.filter(([r]) => r.method === "genui.retry");
  expect(first).toHaveLength(1);
  expect(first[0][0]).toEqual(expect.objectContaining({ productDomain: "personal", userSpaceId: "local", userInput: { requestId: expect.any(String), prompt: "Synthetic explicit input" } }));
  expect(ui.container.textContent).toContain("尚未运行");
  const refresh = [...ui.container.querySelectorAll<HTMLButtonElement>(".personal-genui button")].find(b => b.textContent === "刷新状态")!;
  await act(async () => refresh.click());
  expect(ui.invoke.mock.calls.filter(([r]) => r.method === "genui.retry")).toHaveLength(1);
  const resume = [...ui.container.querySelectorAll<HTMLButtonElement>(".personal-genui button")].find(b => b.textContent === "继续文字解释")!;
  await act(async () => resume.click());
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "genui.retry", input: { agent_id: main.agent_id, surface_id: "submit-surface", receipt_id: "receipt-held" }, userInput: { requestId: first[0][0].userInput!.requestId, prompt: "", resumeReceiptId: "receipt-held" } }));
  expect(ui.container.textContent).toContain("选择已由当前对话接收");
});

it("updates same-key GenUI authority through Personal polling without discarding a dirty draft", async () => {
  const spec = genuiFixtures.calculator;
  let snapshot: ActionResult = { surface: { schemaVersion: 1, surfaceId: "poll-surface", messageId: "poll-message", spec, state: genuiInitialState(spec), revision: 0, submitted: false }, acceptedRevision: 0 };
  const history = async () => ({ records: [{ seq: 2, role: "assistant", content: spec.fallbackText, ui_parts: [snapshot] }] });
  const ui = await mount(async (method, input) => {
    if (method === "genui.action") { expect(input.request.expectedRevision).toBe(2); expect(input.request.state.count).toBe(8); return { surface: { ...snapshot.surface, revision: 3, state: input.request.state }, acceptedRevision: 3 }; }
    return {};
  }, main, [], { history });
  const element = ui.container.querySelector('.personal-genui');
  snapshot = { surface: { ...snapshot.surface, revision: 1, state: { count: 10, price: 20 } }, acceptedRevision: 1 };
  await act(async () => ui.publish());
  expect(ui.container.querySelector('.personal-genui')).toBe(element);
  expect(ui.container.querySelector('[data-genui-node="sum"]')?.textContent).toBe("200");
  await act(async () => { const slider = ui.container.querySelector('input[type="range"]')!; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(slider, "8"); slider.dispatchEvent(new Event("input", { bubbles: true })); slider.dispatchEvent(new Event("change", { bubbles: true })); });
  snapshot = { surface: { ...snapshot.surface, revision: 2, state: { count: 9, price: 40 } }, acceptedRevision: 2 };
  await act(async () => ui.publish());
  expect(ui.container.querySelector('input[type="range"]')).toHaveProperty("value", "8");
  expect(ui.container.querySelector('[data-genui-node="save"]')).toHaveProperty("disabled", true);
  expect(ui.container.querySelector('[data-genui-conflict]')).not.toBeNull();
  await ui.click('[data-action="keep-genui-draft"]');
  await ui.click('[data-genui-node="save"]');
  snapshot = { surface: { ...snapshot.surface, revision: 1, state: { count: 1, price: 10 } }, acceptedRevision: 1 };
  await act(async () => ui.publish());
  expect(ui.container.querySelector('input[type="range"]')).toHaveProperty("value", "8");
});

it("exposes same-revision held receipts from polling and never regresses to older delivery status", async () => {
  const spec = genuiFixtures.calculator;
  let snapshot: ActionResult = { surface: { schemaVersion: 1, surfaceId: "held-poll", messageId: "held-message", spec, state: genuiInitialState(spec), revision: 1, submitted: true }, acceptedRevision: 1, delivery: "pending", deliveryRevision: 0, submission: { requestId: "held-event", prompt: "Synthetic input" } };
  const ui = await mount(undefined, main, [], { history: async () => ({ records: [{ seq: 2, role: "assistant", content: spec.fallbackText, ui_parts: [snapshot] }] }) });
  const pending = structuredClone(snapshot);
  snapshot = { ...snapshot, delivery: "held", deliveryRevision: 2, submission: { requestId: "held-event", prompt: "", resumeReceiptId: "receipt-held" } };
  await act(async () => ui.publish());
  expect(ui.container.querySelector('[data-action="retry-genui"]')?.textContent).toContain("继续");
  snapshot = pending; await act(async () => ui.publish());
  expect(ui.container.querySelector('[data-action="retry-genui"]')?.textContent).toContain("继续");
  snapshot = { surface: snapshot.surface, acceptedRevision: 1, delivery: "unconfirmed", deliveryRevision: 3 };
  await act(async () => ui.publish());
  expect(ui.container.querySelector('[data-action="retry-genui"]')).toBeNull();
  expect(ui.invoke.mock.calls.filter(([r]) => r.method === "genui.retry")).toHaveLength(0);
});

it("keeps dirty GenUI choices visible when another client submits and surfaces Held separately", async () => {
  const spec = genuiFixtures.checklist;
  let snapshot: ActionResult = { surface: { schemaVersion: 1, surfaceId: "draft-held", messageId: "draft-message", spec, state: genuiInitialState(spec), revision: 0, submitted: false }, acceptedRevision: 0 };
  const ui = await mount(undefined, main, [], { history: async () => ({ records: [{ seq: 2, role: "assistant", content: spec.fallbackText, ui_parts: [snapshot] }] }) });
  await ui.click('[data-genui-node="done"]');
  snapshot = { ...snapshot, surface: { ...snapshot.surface, revision: 1, submitted: true }, acceptedRevision: 1, delivery: "held", deliveryRevision: 2, submission: { requestId: "peer-event", prompt: "", resumeReceiptId: "peer-receipt" } };
  await act(async () => ui.publish());
  expect(ui.container.querySelector('input[type="checkbox"]')).toHaveProperty("checked", true);
  expect(ui.container.querySelector('[data-genui-conflict]')).not.toBeNull();
  expect(ui.container.querySelector('[data-action="keep-genui-draft"]')).toBeNull();
  expect(ui.container.querySelector('[data-action="retry-genui"]')).not.toBeNull();
  await ui.click('[data-action="use-genui-authority"]');
  expect(ui.container.querySelector('input[type="checkbox"]')).toHaveProperty("checked", false);
});

it("recovers an exceptional visible expression on the same surface while preserving usable inputs", async () => {
  const spec = structuredClone(genuiFixtures.calculator); spec.fields.count = { type: "number", scope: "persistent", initial: 0, min: 0, max: 20 };
  spec.tree = { id: "error-root", type: "stack", children: [spec.tree, { id: "conditional-error", type: "text", value: "computed detail", visible: { op: "gt", args: [{ op: "div", args: [1, { get: "count" }] }, 0] } }] };
  let snapshot: ActionResult = { surface: { schemaVersion: 1, surfaceId: "visible-error", messageId: "visible-message", spec, state: genuiInitialState(spec), revision: 0, submitted: false }, acceptedRevision: 0 };
  const ui = await mount(undefined, main, [], { history: async () => ({ records: [{ seq: 2, role: "assistant", content: spec.fallbackText, ui_parts: [snapshot] }] }) });
  expect(ui.container.querySelector('input[type="range"]')).not.toBeNull();
  snapshot = { ...snapshot, surface: { ...snapshot.surface, revision: 1, state: { count: 1, price: 80 } }, acceptedRevision: 1 };
  await act(async () => ui.publish());
  expect(ui.container.textContent).toContain("computed detail");
});


it("recovers the GenUI error boundary after a corrected same-ID snapshot", async () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  const spec = genuiFixtures.calculator;
  let snapshot: ActionResult = { surface: { schemaVersion: 1, surfaceId: "boundary-same", messageId: "boundary-message", spec, state: { count: "invalid", price: 80 }, revision: 0, submitted: false }, acceptedRevision: 0 };
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container); cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  const transport = { dispatch: async () => snapshot, read: async () => snapshot, retry: async () => snapshot };
  await act(async () => root.render(<PersonalGenUISurface initial={snapshot} transport={transport} />));
  expect(container.textContent).toContain(spec.fallbackText); expect(container.querySelector('input[type="range"]')).toBeNull();
  snapshot = { ...snapshot, surface: { ...snapshot.surface, state: { count: 4, price: 80 } } };
  await act(async () => root.render(<PersonalGenUISurface initial={snapshot} transport={transport} />));
  expect(container.querySelector('input[type="range"]')).not.toBeNull();
});

it("mounts connection and review controls in existing preferences without implying configured authority", async () => {
  const view = await mount(async method => {
    if (method === "connections.status") return { ok: true, value: { product_domain: "personal", provider: "google-calendar", user_space_id: "local", state: "unavailable", selected_calendars: [], error_code: "oauth_client_not_configured" } };
    if (method === "review.list") return { ok: true, value: { schema: "personal-review.v1", kind: "unavailable", reason: "Production authorization resolver is unavailable" } };
  });
  await view.settings();
  expect(view.container.querySelector('[data-personal-connections]')).not.toBeNull();
  expect(view.container.querySelector('.personal-review')).not.toBeNull();
  expect(view.container.textContent).toContain("Production authorization resolver is unavailable");
  expect(view.container.querySelector('[data-review-choice]')).toBeNull();
  expect(view.container.querySelector('[data-action="save-notifications"]')).not.toBeNull();
});

it("keeps the Calendar CAS error and stale result visible through the shipped settings bridge", async () => {
  let calls=0;
  const connection={product_domain:"personal",provider:"google-calendar",user_space_id:"local",account_id:"account",connection_id:"connection",selection_revision:"1",state:"connected",selected_calendars:["primary"]};
  const view=await mount(async(method,input)=>{
    if(method==="connections.status")return {ok:true,value:connection};
    if(method==="connections.calendars")return {ok:true,value:[{id:"primary",summary:"Synthetic",timeZone:"UTC"}]};
    if(method==="review.list")return {ok:true,value:{schema:"personal-review.v1",kind:"unavailable",reason:"Synthetic"}};
    if(method==="connections.agenda")return {state:++calls===1?"ready":"error",freshness:calls===1?"fresh":"stale",error_code:calls===1?undefined:"provider_unavailable",read_only:true,source:{...(input as object),provider:"google-calendar",read_only:true,user_space_id:"local",account_id:"account",connection_id:"connection",selection_revision:"1",fetched_at:new Date().toISOString()},events:[{id:"event",status:"confirmed",summary:"Previously fetched appointment",all_day:true}],cancelled_events:[]};
  });
  await view.settings();
  const ask=async()=>{await act(async()=>{view.container.querySelector('form.gcal-agenda-form')!.dispatchEvent(new Event("submit",{bubbles:true,cancelable:true}));await new Promise(resolve=>setTimeout(resolve,0));});};
  await ask();expect(view.container.textContent).toContain("Previously fetched appointment");
  await ask();expect(view.container.textContent).toContain("Previously fetched appointment");
  expect(view.container.querySelector('.gcal-notice[role="alert"]')?.textContent).toContain("过期");
});

it("keeps old authorization diagnostics separate from the new Calendar connection in shipped settings", async () => {
  const view=await mount(async method=>{
    if(method==="connections.status")return {ok:true,value:{product_domain:"personal",provider:"google-calendar",user_space_id:"local",account_id:"new-account",connection_id:"new-connection",selection_revision:"2",state:"connected",selected_calendars:["new-calendar"],authorization_diagnostics:[{attempt_id:"old-attempt",error_code:"credential_cleanup_failed"}]}};
    if(method==="connections.calendars")return {ok:true,value:[{id:"new-calendar",summary:"Current calendar",timeZone:"UTC"}]};
    if(method==="review.list")return {ok:true,value:{schema:"personal-review.v1",kind:"unavailable",reason:"Synthetic"}};
  });
  await view.settings();
  expect(view.container.querySelector('form.gcal-agenda-form')).not.toBeNull();
  expect(view.container.textContent).toContain("Current calendar");
  const diagnostic=view.container.querySelector('[data-calendar-attempt-diagnostic="old-attempt"]');
  expect(diagnostic).not.toBeNull();
  expect(diagnostic?.getAttribute('role')).toBe('alert');
  expect(view.container.querySelector('.gcal-connection [role="alert"]')).toBeNull();
});

it("keeps the current Calendar grant usable when an older authorization promise fails cleanup", async () => {
  // This test leaves both lifetime signals live; jsdom lacks Chromium's any().
  const previousAny=Object.getOwnPropertyDescriptor(AbortSignal,'any');
  Object.defineProperty(AbortSignal,'any',{configurable:true,value:(signals:AbortSignal[])=>signals[0]});
  cleanups.push(()=>{if(previousAny)Object.defineProperty(AbortSignal,'any',previousAny);else Reflect.deleteProperty(AbortSignal,'any');});
  let connected=false,finish: ((value:unknown)=>void)|undefined;
  const view=await mount(async method=>{
    if(method==="connections.status")return {ok:true,value:{product_domain:"personal",provider:"google-calendar",user_space_id:"local",account_id:"new-account",connection_id:"new-connection",selection_revision:"2",state:connected?"connected":"unconnected",selected_calendars:connected?["new-calendar"]:[]}};
    if(method==="connections.calendars")return {ok:true,value:[{id:"new-calendar",summary:"Current calendar",timeZone:"UTC"}]};
    if(method==="connections.start")return new Promise(resolve=>{finish=resolve;});
    if(method==="review.list")return {ok:true,value:{schema:"personal-review.v1",kind:"unavailable",reason:"Synthetic"}};
  });
  await view.settings();
  await act(async()=>{Array.from(view.container.querySelectorAll('button')).find(button=>button.textContent==='连接 Google Calendar')!.click();});
  expect(finish).toBeDefined();
  await act(async()=>{connected=true;finish!({ok:false,error:{code:'credential_cleanup_failed'}});});
  expect(view.container.querySelector('form.gcal-agenda-form')).not.toBeNull();
  const notices=Array.from(view.container.querySelectorAll('.gcal-notice[role="alert"]'));
  expect(notices.length).toBeGreaterThan(0);
  expect(notices.every(notice=>!notice.textContent?.includes('连接已停用'))).toBe(true);
});
