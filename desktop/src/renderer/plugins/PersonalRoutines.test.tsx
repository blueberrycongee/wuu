import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { PluginConversationAttachment, PluginConversationComposer, PluginConversationMessage, PluginConversationView } from "./PluginConversation";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { PluginViewContent, WorkbenchController } from "./Workbench";

const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/personal/desktop.js"), "utf8");
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
const main = { agent_id: "a-main", user_space_id: "local", name: "twin", kind: "main", main_session_id: "s-private", status: "active", revision: 1 };
const cleanups: Array<() => void> = [];
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); });

function routine(routineID: string, overrides: Record<string, unknown> = {}) {
  return {
    routine_id: routineID, agent_id: main.agent_id, title: routineID, kind: "reminder",
    status: "completed", revision: 2, config_revision: 1, run_at: "2026-10-19T10:00:00Z",
    binding: { binding_id: `binding-${routineID}`, revision: 1, status: "active" },
    ...overrides,
  };
}

async function mount(fixtures: { routines: Record<string, unknown>[]; tasks?: Record<string, unknown>[] }) {
  const invoke = vi.fn<NonNullable<PluginHostOptions["invokeRuntime"]>>(async ({ method }) => {
    if (method === "agents.ensure_default") return { agent: main };
    if (method === "agents.open") return { agent: main, session: { state: "completed" } };
    if (method === "agents.history") return { records: [{ seq: 1, role: "assistant", content: "Delivered one-time reminder" }] };
    if (method === "tasks.list") return { tasks: fixtures.tasks ?? [] };
    if (method === "routines.list") return { routines: fixtures.routines };
    if (method === "calendar.status") return {};
    if (method === "calendar.operations.list") return { operations: [] };
    throw new Error(`Unexpected runtime call: ${method}`);
  });
  const host = new PluginHost({ react: React, invokeRuntime: invoke, conversationComponents: { Composer: PluginConversationComposer, Message: PluginConversationMessage, View: PluginConversationView, Attachment: PluginConversationAttachment } });
  await host.activateGeneration({ pluginId: "personal", generation: "one", register: activate });
  const controller = new WorkbenchController(host);
  await controller.openPluginView("personal", "personal.home");
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const render = () => root.render(<PluginViewContent controller={controller} pluginId="personal" viewTypeId="personal.home" />);
  cleanups.push(() => { act(() => root.unmount()); controller.dispose(); host.unload("personal"); container.remove(); });
  await act(async () => render());
  return {
    container, invoke,
    expand: async () => { await act(async () => (container.querySelector('[data-action="show-more-activity"]') as HTMLButtonElement | null)?.click()); },
    publish: (event: unknown) => host.publishHostEvent(event),
    activateEntry: () => controller.openPluginView("personal", "personal.home"),
    remount: async () => { await act(async () => root.render(null)); await act(async () => render()); },
  };
}

it("hides settled one-time reminders without changing stored routines or conversation history", async () => {
  const visible = [
    routine("upcoming", { status: "active" }),
    routine("paused", { status: "paused" }),
    routine("stopped", { status: "stopped" }),
    routine("stopping", { status: "stopping", cancellation_pending: true }),
    routine("failed-reminder", { last_error: "Reminder reply was not confirmed: failed" }),
    routine("cancellation-pending", { cancellation_pending: true }),
    routine("binding-pending", { binding: { status: "pending" } }),
    routine("binding-unknown", { binding: { status: "unknown" } }),
    routine("recurring", { cron: "0 9 * * *", run_at: undefined, status: "active" }),
    routine("finished-calendar-watch", { cron: "*/5 * * * *", run_at: undefined }),
  ];
  const fixtures = { routines: [routine("delivered-reminder"), ...visible] };
  const stored = structuredClone(fixtures.routines);
  const ui = await mount(fixtures);
  await ui.expand();
  expect(Array.from(ui.container.querySelectorAll("[data-routine-id]"), element => element.getAttribute("data-routine-id"))).toEqual([...visible.filter(item => item.run_at).map(item => item.routine_id).sort(), ...visible.filter(item => !item.run_at).map(item => item.routine_id).sort()]);
  expect(ui.container.querySelectorAll("[data-routine-id]").length).toBe(visible.length);
  expect(ui.container.textContent).toContain("Delivered one-time reminder");
  await act(async () => (ui.container.querySelector('[data-routine-id="failed-reminder"]') as HTMLButtonElement).click());
  expect(ui.container.querySelector('[data-routine-detail="failed-reminder"] [role="alert"]')?.textContent).toContain("not confirmed");
  expect(fixtures.routines).toEqual(stored);
  expect(ui.invoke.mock.calls.map(([request]) => request.method).filter(method => method.startsWith("routines."))).toEqual(["routines.list"]);
});

it.each(["routine event", "notification", "reentry"])("removes newly completed reminders on %s and keeps them hidden after remount", async refresh => {
  const pending = routine("once", { status: "active", revision: 1 });
  const completed = routine("once");
  const fixtures = { routines: [pending] };
  const ui = await mount(fixtures);
  expect(ui.container.querySelector('[data-routine-id="once"]')).not.toBeNull();
  fixtures.routines = [completed];
  await act(async () => {
    if (refresh === "reentry") await ui.activateEntry();
    else ui.publish(refresh === "routine event" ? { type: "routine-state-changed" } : { kind: "notification", message: { method: "turn/completed", params: { thread_id: main.main_session_id } } });
  });
  expect(ui.container.querySelector(".mosaic-routines")).toBeNull();
  expect(ui.container.textContent).toContain("Delivered one-time reminder");

  fixtures.routines = [pending];
  await act(async () => ui.publish({ type: "routine-state-changed" }));
  expect(ui.container.querySelector(".mosaic-routines")).toBeNull();

  fixtures.routines = [completed];
  await ui.remount();
  expect(ui.container.querySelector(".mosaic-routines")).toBeNull();
  expect(fixtures.routines).toEqual([completed]);
  expect(ui.container.textContent).toContain("Delivered one-time reminder");
});

it("keeps completed task schedules visible until their current work and result delivery succeed", async () => {
  const cases = [
    { id: "delivered", state: "completed" },
    { id: "silent-success", state: "completed", delivery: { status: "suppressed" } },
    { id: "running", state: "running" },
    { id: "queued", state: "queued" },
    { id: "waiting-user", state: "waiting_user" },
    { id: "waiting-external", state: "waiting_external" },
    { id: "failed", state: "failed", error: "Execution failed" },
    { id: "stopped", state: "stopped", cancel_requested: true },
    { id: "reconciling", state: "completed", reconciliation_required: true },
    { id: "delivery-pending", state: "completed", delivery: { status: "pending" } },
    { id: "delivery-submitted", state: "completed", delivery: { status: "submitted" } },
    { id: "delivery-failed", state: "completed", delivery: { status: "failed" }, error: "Reply failed" },
    { id: "old-revision", state: "completed", routine_revision: 0 },
  ];
  const tasks = cases.map(({ id, ...overrides }) => ({
    task_id: `task-${id}`, agent_id: main.agent_id, routine_id: id, routine_revision: 1,
    title: `Work for ${id}`, revision: 1, requirements_revision: 1,
    delivery: { status: "delivered" }, result: { summary: `Result for ${id}`, requirements_revision: 1 },
    ...overrides,
  }));
  const fixtures = {
    routines: [...cases.map(({ id }) => routine(id, { kind: "task" })), routine("awaiting-task", { kind: "task" })],
    tasks,
  };
  const ui = await mount(fixtures);
  await ui.expand();
  const expected = [...cases.slice(2).map(({ id }) => id), "awaiting-task"].sort();
  expect(Array.from(ui.container.querySelectorAll("[data-routine-id]"), element => element.getAttribute("data-routine-id"))).toEqual(expected);
  expect(ui.container.querySelectorAll("[data-routine-id]").length).toBe(expected.length);
  await act(async () => (ui.container.querySelector('[data-action="show-more-outputs"]') as HTMLButtonElement | null)?.click());
  expect(ui.container.querySelector('[data-output-task-id="task-delivered"]')).not.toBeNull();

  fixtures.tasks = tasks.map(task => task.task_id === "task-running" ? { ...task, state: "completed", revision: 2 } : task);
  await act(async () => ui.publish({ kind: "notification", message: { method: "turn/completed", params: { thread_id: main.main_session_id } } }));
  expect(ui.container.querySelector('[data-routine-id="running"]')).toBeNull();
  expect(ui.container.querySelectorAll("[data-routine-id]").length).toBe(expected.length - 1);
});

it("prioritizes real schedules before the five-row activity limit, deduplicates their runs, and expands ordinary activity", async () => {
  const tasks = Array.from({ length: 7 }, (_, index) => ({ task_id: `ordinary-${index}`, title: index === 0 ? "定时 is only a word" : `Ordinary ${index}`, state: ["waiting_user", "running", "queued", "stopping"][index % 4], updated_at: `2026-10-${20 - index}T10:00:00Z` }));
  const ui = await mount({ routines: [routine("later", { status: "paused", binding: { status: "active", next_run_at: "2026-10-24T10:00:00Z" } }), routine("earlier", { status: "active", binding: { status: "active", next_run_at: "2026-10-23T10:00:00Z" } }), routine("settled")], tasks: [...tasks, { task_id: "run-earlier", title: "Duplicate run", routine_id: "earlier", routine_revision: 1, state: "running" }] });
  const recent = () => ui.container.querySelector('[data-panel-section="recent"]')!;
  const order = () => Array.from(recent().querySelectorAll('[data-routine-id],[data-task-id]'), node => node.getAttribute('data-routine-id') || node.getAttribute('data-task-id'));
  expect(order()).toEqual(["earlier", "later", "ordinary-0", "ordinary-1", "ordinary-2"]);
  expect(ui.container.querySelector('[data-task-id="run-earlier"]')).toBeNull();
  expect(ui.container.querySelector('[data-routine-id="settled"]')).toBeNull();
  await act(async () => (recent().querySelector('[data-action="show-more-activity"]') as HTMLButtonElement).click());
  expect(order()).toEqual(["earlier", "later", ...tasks.map(task => task.task_id)]);
});

it("keeps ordinary activity chronological when no schedules exist", async () => {
  const ui = await mount({ routines: [], tasks: [{ task_id: "older", title: "Older", state: "waiting_user", updated_at: "2026-10-19" }, { task_id: "newer", title: "Newer", state: "waiting_user", updated_at: "2026-10-20" }] });
  expect(Array.from(ui.container.querySelectorAll('[data-panel-section="recent"] [data-task-id]'), node => node.getAttribute('data-task-id'))).toEqual(["newer", "older"]);
});

it("keeps stopped routines inspectable without offering an edit that cannot be saved", async () => {
  const ui = await mount({ routines: [routine("stopped", { status: "stopped", timezone: "UTC", prompt: "Preserved instruction" })] });
  await act(async () => ui.container.querySelector<HTMLButtonElement>('[data-action="toggle-personal-panel"]')!.click());
  await act(async () => ui.container.querySelector<HTMLButtonElement>('[data-routine-id="stopped"]')!.click());
  expect(ui.container.querySelector('[data-routine-detail]')).not.toBeNull();
  const edit = ui.container.querySelector<HTMLButtonElement>('[data-action="edit-routine"]')!;
  expect(edit.disabled).toBe(true);
  await act(async () => edit.click());
  expect(ui.container.querySelector('#mosaic-routine-editor')).toBeNull();
  expect(ui.container.querySelector<HTMLButtonElement>('[data-action="routine-history"]')!.disabled).toBe(false);
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "routines.update")).toBe(false);
});
