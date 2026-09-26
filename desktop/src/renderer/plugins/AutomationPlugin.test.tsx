import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { PluginViewContent, WorkbenchController } from "./Workbench";
const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/automation/desktop.js"), "utf8");
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
const task = { id: "one", title: "Review", prompt: "Review recent changes", cron: "0 9 * * 1-5", timezone: "Asia/Shanghai", mode: "new_thread", recurring: true, paused: false, workspace_mode: "worktree", next_run_at: "2026-09-16T01:00:00Z" };
const cleanups: Array<() => void> = [];
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.useRealTimers(); });
async function mount(options: { tasks?: typeof task[]; runs?: unknown[]; mutate?: (input: any) => Promise<unknown> } = {}) {
  vi.useFakeTimers();
  const invoke = vi.fn<NonNullable<PluginHostOptions["invokeRuntime"]>>(async ({ method, input, workspaceId }) => {
    if (method === "automation.list") return { tasks: workspaceId === "wuu" ? options.tasks ?? [task] : [], workspace: { id: workspaceId } };
    if (method === "automation.run.list") return { runs: options.runs || [] };
    return options.mutate ? options.mutate(input) : { ...task, ...(input as object), id: "one" };
  });
  const host = new PluginHost({ react: React, invokeRuntime: invoke,
    listWorkspaces: async () => ({ activeWorkspaceId: "wuu", workspaces: [{ id: "wuu", name: "Wuu", root: "/wuu", available: true }, { id: "other", name: "Other", root: "/other", available: true }] }),
    listThreads: async () => [{ id: "chat", title: "Existing investigation", pinned: true }],
  });
  await host.activateGeneration({ pluginId: "automation", generation: "one", register: activate });
  const controller = new WorkbenchController(host);
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); controller.dispose(); host.disable("automation"); container.remove(); });
  await act(async () => root.render(<PluginViewContent controller={controller} pluginId="automation" viewTypeId="automation.catalog" />));
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
  return { container, invoke, click, edit, open };
}
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
  expect(ui.container.querySelector('[role="alert"]')!.textContent).toContain("Storage unavailable");
  expect(ui.container.querySelector("textarea")!.value).toBe("A revised prompt");
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "automation.update", input: expect.objectContaining({ schedule: "*/17 9-18 * * 1,3,5" }) }));
  fail = false; await ui.click("保存修改"); expect(ui.container.querySelector('[role="alert"]')).toBeNull();
});
it("opens a suggestion as a draft and creates only on submit", async () => {
  const ui = await mount({ tasks: [] });
  await act(async () => ui.container.querySelector<HTMLButtonElement>(".plugin-automation-suggestions button")!.click());
  expect(ui.invoke.mock.calls.every(([request]) => !request.method.includes("create"))).toBe(true);
  await act(async () => ui.container.querySelector<HTMLFormElement>("form")!.requestSubmit());
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "automation.create", input: expect.objectContaining({ durable: true, recurring: true, schedule: "0 9 * * 1-5" }) }));
});
it("keeps failed and running one-shot runs reachable with their error", async () => {
  const snapshot = { ...task, recurring: false };
  const ui = await mount({ tasks: [], runs: [
    { id: "run-failed", task_id: "one", status: "failed", error: "Provider unavailable", triggered_at: "2026-09-15T01:00:00Z", task: snapshot },
    { id: "run-running", task_id: "two", status: "running", triggered_at: "2026-09-15T02:00:00Z", task: { ...snapshot, id: "two", title: "Sweep" } },
  ] });
  const list = ui.container.querySelector(".plugin-automation-list")!;
  expect(list.textContent).toContain("Review");
  expect(list.textContent).toContain("Sweep");
  expect(list.textContent).toContain("失败");
  expect(list.textContent).toContain("运行中");
  const failedRow = [...list.querySelectorAll("button")].find(item => item.textContent!.includes("失败"))!;
  await act(async () => failedRow.click());
  const panel = ui.container.querySelector("aside")!;
  expect(panel.textContent).toContain("Provider unavailable");
  expect(panel.querySelector("fieldset")!.disabled).toBe(true);
});
it("leaves the completed filter to successful one-shot runs", async () => {
  const snapshot = { ...task, recurring: false };
  const ui = await mount({ tasks: [], runs: [
    { id: "run-failed", task_id: "one", status: "failed", error: "Provider unavailable", triggered_at: "2026-09-15T01:00:00Z", task: snapshot },
    { id: "run-done", task_id: "two", status: "completed", triggered_at: "2026-09-15T02:00:00Z", task: { ...snapshot, id: "two", title: "Swept" } },
  ] });
  await ui.click("已完成");
  const list = ui.container.querySelector(".plugin-automation-list")!;
  expect(list.textContent).not.toContain("Review");
  expect(list.textContent).toContain("Swept");
});
it("shows completed one-shot snapshots without treating recurring runs as finished tasks", async () => {
  const ui = await mount({ tasks: [], runs: [{ id: "run", task_id: "one", status: "completed", triggered_at: "2026-09-15T01:00:00Z", task: { ...task, recurring: false } }, { id: "recurring", task_id: "two", status: "completed", triggered_at: "2026-09-15T02:00:00Z", task: { ...task, id: "two", title: "Recurring" } }] });
  await ui.click("已完成"); await ui.open();
  expect(ui.container.querySelector("fieldset")!.disabled).toBe(true);
  expect(ui.container.querySelector(".plugin-automation-list")!.textContent).not.toContain("Recurring");
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
  await ui.edit('input[aria-label="搜索会话"]', "investigation");
  expect(ui.container.querySelectorAll('[role="option"]')).toHaveLength(1);
  await act(async () => ui.container.querySelector('input[aria-label="搜索会话"]')!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(ui.container.querySelector('[role="listbox"]')).toBeNull();
  expect(ui.container.querySelector("aside")).not.toBeNull();
  expect(document.activeElement?.getAttribute("aria-label")).toBe("运行于");
});

it("resizes the editor with pointer and keyboard, clamps bounds, and preserves width when reopened", async () => {
  const width = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(1000);
  const rect = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ width: 1000 } as DOMRect);
  cleanups.push(() => { width.mockRestore(); rect.mockRestore(); });
  const ui = await mount(); await ui.click("创建");
  let separator = ui.container.querySelector<HTMLElement>('[role="separator"]')!;
  separator.setPointerCapture = vi.fn(); separator.releasePointerCapture = vi.fn();
  const pointer = async (type: string, x: number) => {
    const event = new MouseEvent(type, { bubbles: true, button: 0, clientX: x });
    Object.defineProperty(event, "pointerId", { value: 1 });
    await act(async () => separator.dispatchEvent(event));
  };
  await pointer("pointerdown", 520); await pointer("pointermove", 420); await pointer("pointerup", 420);
  expect(separator.getAttribute("aria-valuenow")).toBe("580");
  await ui.click("关闭"); await ui.click("创建");
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
  await ui.click("创建");
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
  await ui.click("创建");
  await ui.click("关闭");
  await ui.click("创建");
  expect(body()?.getAttribute("data-closing")).toBeNull();
  expect(ui.container.querySelector("aside")?.hasAttribute("inert")).toBe(false);
});

it("closes the editor immediately when motion is reduced", async () => {
  const matchMedia = window.matchMedia;
  window.matchMedia = ((query: string) => ({
    matches: query.includes("prefers-reduced-motion"),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as typeof window.matchMedia;
  cleanups.push(() => { window.matchMedia = matchMedia; });
  const ui = await mount();
  await ui.click("创建");
  await ui.click("关闭");
  expect(ui.container.querySelector("aside")).toBeNull();
});

it("reveals saved execution overrides and preserves them when the settings are collapsed", async () => {
  const ui = await mount({ tasks: [{ ...task, recurring: false, timezone: "Pacific/Honolulu" }] });
  await ui.open();
  const settings = ui.container.querySelector<HTMLDetailsElement>(".plugin-automation-advanced")!;
  expect(settings.open).toBe(true);
  await act(async () => { settings.open = false; settings.dispatchEvent(new Event("toggle")); });
  await ui.edit("textarea", "Updated instructions"); await ui.click("保存修改");
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "automation.update", input: expect.objectContaining({ recurring: false, timezone: "Pacific/Honolulu", workspace: "worktree" }) }));
});
