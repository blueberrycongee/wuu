import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { PluginViewContent, WorkbenchController } from "./Workbench";

const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/personal-calendar/desktop.js"), "utf8");
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
const proposal = {
  operation_id: "calendar-op", revision: 3, status: "proposed", kind: "reschedule", preview_digest: "a".repeat(64),
  user_space_id: "local", agent_id: "main", session_id: "scoped-session", task_id: "task-one", requirements_revision: 2,
  account_id: "account-one", calendar_id: "calendar-one", event_id: "event-one", original_event_etag: "etag-one",
  fields: { summary: "Planning lunch", location: "Garden Cafe", start: { date_time: "2026-11-01T12:00:00+00:00", timezone: "UTC" }, end: { date_time: "2026-11-01T13:00:00+00:00", timezone: "UTC" }, attendees: [{ email: "friend@example.test", optional: false }], send_updates: "all" },
  original_event: { id: "event-one", etag: "etag-one", fields: { summary: "Original lunch", location: "Old Cafe", start: { date: "2026-10-31", timezone: "UTC" }, end: { date: "2026-11-01", timezone: "UTC" }, attendees: [], send_updates: "none" } },
  availability: { checked_at: "2026-10-18T10:00:00Z", calendar_id: "calendar-one", conflicting_event_ids: ["conflict-one"] },
  notification_notice: "Invitations will be sent to all attendees", reconciliation_required: false, cancel_requested: false,
};
const cleanups: Array<() => void> = [];
afterEach(() => cleanups.splice(0).forEach(fn => fn()));

async function mount(readOnly = false, respond?: (method: string, input: any) => unknown) {
  let operation: any = structuredClone(proposal);
  const invoke = vi.fn<NonNullable<PluginHostOptions["invokeRuntime"]>>(async ({ method, input }) => {
    if (method === "calendar.status") return { state: readOnly ? "unconnected" : "ready", read_only: readOnly, freshness: "empty", account_id: readOnly ? undefined : "account-one" };
    if (method === "calendar.operations.list") return { operations: [operation] };
    const response = respond?.(method, input);
    if (response) operation = response;
    return response ?? operation;
  });
  const host = new PluginHost({ react: React, invokeRuntime: invoke });
  await host.activateGeneration({ pluginId: "personal-calendar", generation: "one", register: activate });
  const controller = new WorkbenchController(host), container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); controller.dispose(); host.unload("personal-calendar"); container.remove(); });
  await act(async () => root.render(<PluginViewContent controller={controller} pluginId="personal-calendar" viewTypeId="personal-calendar.operations" />));
  const click = async (selector: string) => { const element = container.querySelector<HTMLElement>(selector); expect(element).not.toBeNull(); await act(async () => element!.click()); };
  await click('[data-operation-id="calendar-op"]');
  return { container, invoke, click, replace: (next: any) => { operation = next; } };
}

it("keeps old proposals inspectable while an unconnected read-only host cannot execute them", async () => {
  const ui = await mount(true);
  expect(ui.container.querySelector('[data-calendar-state="unconnected"]')).not.toBeNull();
  expect(ui.container.textContent).toContain(proposal.fields.summary);
  expect(ui.container.querySelector<HTMLButtonElement>('[data-action="confirm"]')?.disabled).toBe(true);
  expect(ui.invoke.mock.calls.every(([request]) => request.pluginId === "personal-calendar" && request.userSpaceId === "local" && request.productDomain === "personal")).toBe(true);
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "calendar.operations.confirm")).toBe(false);
});

it("requires review of the exact immutable preview before direct-client confirmation", async () => {
  const ui = await mount();
  const preview = ui.container.querySelector('[data-calendar-preview]')!;
  for (const value of ["Garden Cafe", "friend@example.test", "Original lunch", "etag-one", "conflict-one", "scoped-session", "task-one", proposal.notification_notice]) expect(preview.textContent).toContain(value);
  expect(ui.container.querySelector<HTMLButtonElement>('[data-action="confirm"]')?.disabled).toBe(true);
  await ui.click('[data-action="review-preview"]'); await ui.click('[data-action="confirm"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ pluginId: "personal-calendar", method: "calendar.operations.confirm", input: { operation_id: "calendar-op", expected_revision: 3, preview_digest: proposal.preview_digest } }));
});

it("invalidates review when refresh returns a changed preview or revision", async () => {
  const ui = await mount(); await ui.click('[data-action="review-preview"]');
  ui.replace({ ...proposal, revision: 4, preview_digest: "b".repeat(64) });
  await ui.click('[data-action="refresh"]');
  expect(ui.container.querySelector<HTMLInputElement>('[data-action="review-preview"]')?.checked).toBe(false);
  expect(ui.container.querySelector<HTMLButtonElement>('[data-action="confirm"]')?.disabled).toBe(true);
});

it("keeps cancellation after possible dispatch uncertain and reconciles without another write", async () => {
  const uncertain = { ...proposal, revision: 5, status: "uncertain", reconciliation_required: true };
  const ui = await mount(false, (method) => method === "calendar.operations.cancel" ? { ...uncertain, revision: 6, cancel_requested: true, error_code: "cancellation_requires_reconciliation" } : uncertain);
  ui.replace(uncertain); await ui.click('[data-action="refresh"]');
  await ui.click('[data-action="cancel"]'); await ui.click('[data-action="confirm-cancel"]');
  expect(ui.container.querySelector('[data-operation-status="uncertain"]')).not.toBeNull();
  expect(ui.container.querySelector('[data-operation-status="completed"]')).toBeNull();
  await ui.click('[data-action="reconcile"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "calendar.operations.reconcile", input: { operation_id: "calendar-op" } }));
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "calendar.operations.confirm")).toBe(false);
});
