import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { PluginViewContent, WorkbenchController } from "./Workbench";
import * as Toast from "../Toast";
const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/automation-ui/desktop.js"), "utf8");
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
const task = { id: "one", title: "Review", prompt: "Review recent changes", cron: "0 9 * * 1-5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: false, workspace_mode: "worktree", next_run_at: "2026-09-16T01:00:00Z" };
const cleanups: Array<() => void> = [];
it("rejects a host without selected-slot targeting before registering presentation", async () => {
  await expect(activate({} as PluginGenerationApi)).rejects.toThrow("ui.selected-slot-target.v1");
});
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); Toast.clearToasts(); vi.restoreAllMocks(); vi.useRealTimers(); });
async function mount(options: { tasks?: typeof task[]; runs?: unknown[]; readError?: () => string | undefined; workspaceError?: () => string | undefined; listWorkspaces?: () => Promise<any>; mutate?: (input: any) => Promise<unknown> } = {}) {
  vi.useFakeTimers();
  const notices = vi.spyOn(Toast, "showToast");
  const invoke = vi.fn<NonNullable<PluginHostOptions["invokeRuntime"]>>(async ({ method, input, workspaceId }) => {
    const readError = options.readError?.();
    if (readError && (method === "automation.list" || method === "automation.run.list")) throw new Error(readError);
    if (method === "automation.list") return { tasks: workspaceId === "wuu" ? options.tasks ?? [task] : [], workspace: { id: workspaceId } };
    if (method === "automation.run.list") return { runs: options.runs || [] };
    return options.mutate ? options.mutate(input) : { ...task, ...(input as object), id: "one" };
  });
  const host = new PluginHost({ react: React, invokeRuntime: request => {
    expect(request).toMatchObject({ pluginId: "automation-ui", generation: "one", graphGeneration: "selected-graph", service: "automation.scheduler", method: "request" });
    const { method, input } = request.input as { method: string; input?: unknown };
    return invoke({ ...request, method, input });
  },
    listWorkspaces: async () => { if (options.listWorkspaces) return options.listWorkspaces(); const error = options.workspaceError?.(); if (error) throw new Error(error); return { activeWorkspaceId: "wuu", workspaces: [{ id: "wuu", name: "Wuu", root: "/wuu", available: true }, { id: "other", name: "Other", root: "/other", available: true }] }; },
    listThreads: async () => [{ id: "chat", title: "Existing investigation", pinned: true }],
  });
  await host.activateGeneration({ pluginId: "automation-ui", generation: "one", graphGeneration: "selected-graph", register: activate });
  const controller = new WorkbenchController(host);
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); controller.dispose(); host.disable("automation-ui"); container.remove(); });
  await act(async () => root.render(<PluginViewContent controller={controller} pluginId="automation-ui" viewTypeId="automation.catalog" />));
  const click = async (text: string) => {
    const button = [...container.querySelectorAll("button")].find(item => item.textContent === text || item.getAttribute("aria-label") === text);
    expect(button, text).toBeDefined(); await act(async () => button!.click());
  };
  const edit = async (selector: string, value: string) => {
    const input = container.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(selector)!;
    expect(input, selector).not.toBeNull();
    await act(async () => {
      const proto = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(input, value);
      input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
    });
  };
  const open = async () => { await act(async () => container.querySelector<HTMLButtonElement>(".plugin-automation-list button")!.click()); };
  return { container, invoke, click, edit, open, host, notices };
}
it("keeps presentation available with an explicit unavailable scheduler and recovers without fallback", async () => {
  let unavailable = true;
  const ui = await mount({ readError: () => unavailable ? "Selected automation.scheduler provider is unavailable" : undefined });
  expect(ui.container.querySelector('[role="alert"]')).not.toBeNull();
  expect(ui.container.textContent).not.toContain("automation.scheduler");
  expect(ui.notices).not.toHaveBeenCalled();
  expect(ui.container.querySelector<HTMLButtonElement>(".plugin-automation-create")?.disabled).toBe(true);
  expect(ui.container.querySelector(".plugin-automation-list")).toBeNull();
  unavailable = false;
  await act(async () => ui.container.querySelector<HTMLButtonElement>('[role="alert"] button')!.click());
  expect(ui.container.querySelector('[role="alert"]')).toBeNull();
  expect(ui.container.querySelector<HTMLButtonElement>(".plugin-automation-create")?.disabled).toBe(false);
  expect(ui.container.querySelector(".plugin-automation-list")?.textContent).toContain("Review");
});

it("edits wall-clock schedules and moves a worktree task to an existing chat", async () => {
  const ui = await mount(); await ui.open();
  expect(ui.container.querySelector<HTMLInputElement>('input[type="time"]')!.value).toBe("09:00");
  expect(ui.container.querySelector(".plugin-automation-item-meta")!.textContent).toContain("09:00");
  await ui.edit('input[type="time"]', "08:15"); await ui.click("运行于");
  await act(async () => ui.container.querySelector<HTMLButtonElement>(".plugin-automation-picker-option:last-child")!.click());
  await ui.click("保存修改");
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "automation.update", workspaceId: "wuu", input: expect.objectContaining({ id: "one", schedule: "15 8 * * 1-5", workspace: "shared", mode: "thread_heartbeat", heartbeat_thread_id: "chat", workspace_root: "/wuu" }) }));
});
it("preserves a custom schedule and draft after save failure and allows retry", async () => {
  let fail = true;
  const ui = await mount({ tasks: [{ ...task, cron: "*/17 9-18 * * 1,3,5" }], mutate: async (input) => { if (fail) throw new Error("Storage unavailable"); return { ...task, ...input }; } });
  await ui.open(); await ui.edit("textarea", "A revised prompt"); await ui.click("保存修改");
  expect(ui.container.querySelector('[role="alert"]')).toBeNull();
  expect(ui.container.textContent).not.toContain("Storage unavailable");
  expect(ui.notices).toHaveBeenCalledWith(expect.objectContaining({ tone: "error" }));
  const firstKey = ui.notices.mock.calls.at(-1)![0].dedupeKey;
  await ui.click("保存修改");
  expect(ui.notices).toHaveBeenCalledTimes(2);
  expect(ui.notices.mock.calls.at(-1)![0].dedupeKey).not.toBe(firstKey);
  expect(ui.container.querySelector("textarea")!.value).toBe("A revised prompt");
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "automation.update", input: expect.objectContaining({ schedule: "*/17 9-18 * * 1,3,5" }) }));
  fail = false; await ui.click("保存修改"); expect(ui.container.querySelector('[role="alert"]')).toBeNull();
});
it("opens a new draft and creates only on submit", async () => {
  const ui = await mount({ tasks: [] });
  await ui.click("新建自动化");
  expect(ui.invoke.mock.calls.every(([request]) => !request.method.includes("create"))).toBe(true);
  await ui.edit('input[aria-label="名称"]', "Review");
  await ui.edit("textarea", "Review recent changes");
  await act(async () => ui.container.querySelector<HTMLFormElement>("form")!.requestSubmit());
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "automation.create", input: expect.objectContaining({ durable: true, recurring: true, schedule: "0 9 * * 1-5" }) }));
});
it("shows completed one-shot snapshots without treating recurring runs as finished tasks", async () => {
  const ui = await mount({ tasks: [], runs: [{ id: "run", task_id: "one", status: "completed", triggered_at: "2026-09-15T01:00:00Z", task: { ...task, recurring: false } }, { id: "recurring", task_id: "two", status: "completed", triggered_at: "2026-09-15T02:00:00Z", task: { ...task, id: "two", title: "Recurring" } }] });
  await ui.open();
  expect(ui.container.querySelector("fieldset")!.disabled).toBe(true);
  expect(ui.container.querySelector(".plugin-automation-list")!.textContent).not.toContain("Recurring");
});
it("pauses and resumes from the row switch without opening the editor", async () => {
  let paused = false;
  const ui = await mount({ mutate: async (input) => { paused = input.paused; return { ...task, paused }; } });
  const toggle = () => ui.container.querySelector<HTMLInputElement>('.plugin-automation-list input[type="checkbox"]')!;
  expect(toggle().checked).toBe(true);
  await act(async () => toggle().click());
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "automation.update", input: expect.objectContaining({ id: "one", paused: true }) }));
  expect(ui.container.querySelector("aside")).toBeNull();
});
it("closes the editor and clears workspace-local tasks when changing workspace", async () => {
  const ui = await mount(); await ui.open(); await ui.click("工作区"); await ui.click("Other");
  expect(ui.container.querySelector("aside")).toBeNull(); expect(ui.container.querySelector(".plugin-automation-list")).toBeNull();
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "automation.list", workspaceId: "other" }));
});

it("supports keyboard selection and dismisses only the open menu with Escape", async () => {
  const ui = await mount(); await ui.open(); await ui.click("重复");
  const selected = ui.container.querySelector<HTMLButtonElement>('[role="option"][aria-selected="true"]')!;
  await act(async () => selected.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
  expect(document.activeElement?.textContent).toBe("每周");
  await act(async () => (document.activeElement as HTMLButtonElement).click());
  expect(ui.container.querySelector('[role="listbox"]')).toBeNull();
  expect(ui.container.querySelector('button[aria-label="星期"]')).not.toBeNull();
  await ui.click("运行于");
  await ui.edit('input[aria-label="搜索对话"]', "investigation");
  expect(ui.container.querySelectorAll('[role="option"]')).toHaveLength(1);
  await act(async () => ui.container.querySelector('input[aria-label="搜索对话"]')!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(ui.container.querySelector('[role="listbox"]')).toBeNull();
  expect(ui.container.querySelector("aside")).not.toBeNull();
  expect(document.activeElement?.getAttribute("aria-label")).toBe("运行于");

  await ui.edit("textarea", "Unsaved instructions");
  const trigger = ui.container.querySelector<HTMLElement>('summary[aria-label="更多操作"]')!;
  const more = trigger.closest("details")!;
  const openMore = async () => act(async () => { more.open = true; more.dispatchEvent(new Event("toggle")); });
  await openMore();
  await act(async () => more.querySelector("button")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(more.open).toBe(false);
  expect(document.activeElement).toBe(trigger);
  expect(ui.container.querySelector("textarea")!.value).toBe("Unsaved instructions");
  await openMore();
  await act(async () => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
  expect(more.open).toBe(false);
  expect(ui.container.querySelector("aside")).not.toBeNull();
});

it("resizes the editor with pointer and keyboard, clamps bounds, and preserves width when reopened", async () => {
  const width = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(1000);
  const rect = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ width: 1000 } as DOMRect);
  cleanups.push(() => { width.mockRestore(); rect.mockRestore(); });
  const ui = await mount(); await ui.click("新建自动化");
  let separator = ui.container.querySelector<HTMLElement>('[role="separator"]')!;
  separator.setPointerCapture = vi.fn(); separator.releasePointerCapture = vi.fn();
  const pointer = async (type: string, x: number) => {
    const event = new MouseEvent(type, { bubbles: true, button: 0, clientX: x });
    Object.defineProperty(event, "pointerId", { value: 1 });
    await act(async () => separator.dispatchEvent(event));
  };
  await pointer("pointerdown", 520); await pointer("pointermove", 420); await pointer("pointerup", 420);
  expect(separator.getAttribute("aria-valuenow")).toBe("580");
  await ui.click("关闭"); await ui.click("新建自动化");
  separator = ui.container.querySelector<HTMLElement>('[role="separator"]')!;
  expect(separator.getAttribute("aria-valuenow")).toBe("580");
  await act(async () => separator.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
  expect(separator.getAttribute("aria-valuenow")).toBe("720");
  await act(async () => separator.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true })));
  expect(separator.getAttribute("aria-valuenow")).toBe("720");
  await act(async () => separator.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true })));
  expect(separator.getAttribute("aria-valuenow")).toBe("350");
  expect(ui.invoke.mock.calls.every(([request]) => !request.method.includes("create"))).toBe(true);
});

it("keeps the editor mounted through the close motion and cancels it when reopened", async () => {
  const ui = await mount();
  await ui.click("新建自动化");
  // A new automation starts in its first field, not on the close button.
  expect(document.activeElement).toBe(ui.container.querySelector("form input"));
  const body = () => ui.container.querySelector(".plugin-automation-body");
  expect(body()?.getAttribute("data-panel")).toBe("true");
  expect(body()?.getAttribute("data-closing")).toBeNull();
  await ui.click("关闭");
  expect(ui.container.querySelector("aside")).not.toBeNull();
  expect(body()?.getAttribute("data-closing")).toBe("true");
  expect(ui.container.querySelector("aside")?.hasAttribute("inert")).toBe(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(219); });
  expect(ui.container.querySelector("aside")).not.toBeNull();
  await act(async () => { await vi.advanceTimersByTimeAsync(1); });
  expect(ui.container.querySelector("aside")).toBeNull();
  expect(body()?.getAttribute("data-panel")).toBe("false");
  await ui.click("新建自动化");
  await ui.click("关闭");
  await ui.click("新建自动化");
  expect(body()?.getAttribute("data-closing")).toBeNull();
  expect(ui.container.querySelector("aside")?.hasAttribute("inert")).toBe(false);
});

it("closes the editor immediately when motion is reduced", async () => {
  // Either reduced-motion source zeroes the host's panel exit token.
  document.documentElement.style.setProperty("--environment-panel-exit-duration", "0ms");
  cleanups.push(() => { document.documentElement.style.removeProperty("--environment-panel-exit-duration"); });
  const ui = await mount();
  await ui.click("新建自动化");
  await ui.click("关闭");
  expect(ui.container.querySelector("aside")).toBeNull();
});

it("shows execution overrides in place and preserves them on save", async () => {
  const ui = await mount({ tasks: [{ ...task, recurring: false, timezone: "Pacific/Honolulu" }] });
  await ui.open();
  expect(ui.container.querySelector(".plugin-automation-detail-body")?.textContent).toContain("仅执行一次");
  expect(ui.container.querySelector<HTMLInputElement>("input[value='Pacific/Honolulu']")).not.toBeNull();
  await ui.edit("textarea", "Updated instructions"); await ui.click("保存修改");
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "automation.update", input: expect.objectContaining({ recurring: false, timezone: "Pacific/Honolulu", workspace: "worktree" }) }));
});

it("does not notify from an automation action after its generation retires", async () => {
  let reject!: (reason: Error) => void;
  const ui = await mount({ mutate: () => new Promise((_resolve, no) => { reject = no; }) });
  await ui.open(); await ui.edit("textarea", "Keep my draft"); await ui.click("保存修改");
  await act(async () => { ui.host.disable("automation-ui"); reject(new Error("late private failure")); });
  expect(ui.notices).not.toHaveBeenCalled();
});

it("retains stale schedules during repeated refresh failures without background notices", async () => {
  let unavailable = false;
  const ui = await mount({ readError: () => unavailable ? "scheduler read failed" : undefined });
  unavailable = true;
  await act(async () => vi.advanceTimersByTimeAsync(30000));
  expect(ui.container.querySelector('[role="alert"]')).not.toBeNull();
  expect(ui.container.querySelector(".plugin-automation-list")?.textContent).toContain("Review");
  expect(ui.container.querySelector<HTMLInputElement>('.plugin-automation-list input[type="checkbox"]')?.disabled).toBe(true);
  expect(ui.notices).not.toHaveBeenCalled();
  expect(ui.host.getGenerationDiagnostics("automation-ui", "one")).toEqual(expect.arrayContaining([expect.objectContaining({ kind: "runtime" })]));
  unavailable = false;
  await act(async () => ui.container.querySelector<HTMLButtonElement>('[role="alert"] button')!.click());
  expect(ui.container.querySelector('[role="alert"]')).toBeNull();
  expect(ui.container.querySelector<HTMLInputElement>('.plugin-automation-list input[type="checkbox"]')?.disabled).toBe(false);
});
it("keeps workspace discovery failure recoverable rather than presenting an empty catalog", async () => {
  let unavailable = true;
  const ui = await mount({ workspaceError: () => unavailable ? "workspace storage unreachable" : undefined });
  expect(ui.container.querySelector('[role="alert"]')).not.toBeNull();
  expect(ui.container.querySelector(".plugin-automation-empty")).toBeNull();
  expect(ui.container.querySelector("details:not([open])")?.textContent).toContain("workspace storage unreachable");
  unavailable = false;
  await act(async () => ui.container.querySelector<HTMLButtonElement>('[role="alert"] button')!.click());
  expect(ui.container.querySelector('[role="alert"]')).toBeNull();
  expect(ui.container.querySelector(".plugin-automation-list")?.textContent).toContain("Review");
});

it.each(["failure", "success"])("ignores an older workspace retry %s after a newer retry restores the catalog", async outcome => {
  const pending: Array<{ resolve: (value: unknown) => void; reject: (reason: Error) => void }> = [];
  let calls = 0;
  const ui = await mount({ listWorkspaces: () => {
    if (++calls === 1) return Promise.reject(new Error("offline"));
    return new Promise((resolve, reject) => pending.push({ resolve, reject }));
  } });
  const retry = ui.container.querySelector<HTMLButtonElement>('[role="alert"] button')!;
  await act(async () => { retry.click(); retry.click(); });
  expect(pending).toHaveLength(2);
  expect(retry.disabled).toBe(true);
  const workspaces = [{ id: "wuu", name: "Wuu", root: "/wuu", available: true }, { id: "other", name: "Other", root: "/other", available: true }];
  await act(async () => pending[1].resolve({ activeWorkspaceId: "other", workspaces }));
  expect(ui.container.querySelector('[role="alert"]')).toBeNull();
  expect(ui.container.querySelector(".plugin-automation-workspace-picker")?.textContent).toContain("Other");
  await act(async () => {
    if (outcome === "failure") pending[0].reject(new Error("old workspace failure"));
    else pending[0].resolve({ activeWorkspaceId: "wuu", workspaces });
  });
  expect(ui.container.querySelector('[role="alert"]')).toBeNull();
  expect(ui.container.querySelector(".plugin-automation-workspace-picker")?.textContent).toContain("Other");
});
