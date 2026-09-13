import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { PluginSlot } from "./PluginSlot";

const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/goal/desktop.js"), "utf8");
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
type InvokeRuntime = NonNullable<PluginHostOptions["invokeRuntime"]>;
type Goal = { id: string; objective: string; status: string; tokens_used: number; time_used_seconds: number };

const activeGoal: Goal = {
  id: "goal-one",
  objective: "Finish the task",
  status: "active",
  tokens_used: 12,
  time_used_seconds: 2,
};
const cleanups: Array<() => void> = [];

afterEach(() => {
  for (const cleanup of cleanups.splice(0)) cleanup();
  vi.useRealTimers();
});

async function mount(invokeRuntime: InvokeRuntime) {
  vi.useFakeTimers();
  const host = new PluginHost({ react: React, invokeRuntime });
  await host.activateGeneration({
    pluginId: "goal",
    generation: "one",
    contributions: {
      slots: [{ id: "goal-controls", target: "composer.above", order: 20, title: "目标" }],
      surfaces: [],
      presenters: [],
    },
    register: activate,
  });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => {
    act(() => root.unmount());
    host.disable("goal");
    container.remove();
  });

  const render = async (threadId = "thread-one", extra: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(<PluginSlot host={host} id="composer.above" context={{ threadId, mainConversation: true, ...extra }} />);
      await Promise.resolve();
      await Promise.resolve();
    });
  };
  const button = (label: string) => {
    const result = [...container.querySelectorAll("button")].find((node) => (node.getAttribute("aria-label") || node.textContent) === label);
    expect(result).toBeDefined();
    return result!;
  };
  const click = async (label: string) => {
    await act(async () => {
      button(label).click();
      await Promise.resolve();
      await Promise.resolve();
    });
  };
  const expand = async () => {
    await act(async () => {
      container.querySelector<HTMLButtonElement>("button[aria-expanded]")!.click();
      await Promise.resolve();
    });
  };
  const fill = async (value: string) => {
    const input = container.querySelector<HTMLTextAreaElement>("textarea")!;
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!;
    await act(async () => {
      setter.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  };

  await render();
  return { host, container, render, button, click, expand, fill };
}

it("exposes live controls above the composer and removes them with the plugin", async () => {
  let goal = activeGoal;
  const invoke = vi.fn(async (request: Parameters<InvokeRuntime>[0]) => {
    expect(request.input).toMatchObject({ thread_id: "thread-one" });
    if (request.method === "pause") goal = { ...goal, status: "paused" };
    if (request.method === "resume") goal = { ...goal, status: "active" };
    return { goal };
  });
  const ui = await mount(invoke);

  expect(ui.container.textContent).toContain("Finish the task");
  expect(ui.container.querySelector("textarea")).toBeNull();
  await ui.click("暂停");
  expect(invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "pause", input: { thread_id: "thread-one" } }));
  await ui.click("继续");
  expect(invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "resume", input: { thread_id: "thread-one" } }));

  act(() => ui.host.disable("goal"));
  expect(ui.container.textContent).toBe("");
  const calls = invoke.mock.calls.length;
  await vi.advanceTimersByTimeAsync(5000);
  expect(invoke).toHaveBeenCalledTimes(calls);
});

it("creates a new goal after completion and hides controls after ending it", async () => {
  let goal: Goal | null = { ...activeGoal, status: "complete" };
  const invoke = vi.fn(async (request: Parameters<InvokeRuntime>[0]) => {
    const input = request.input as { objective?: string };
    if (request.method === "create_goal") goal = { ...activeGoal, objective: input.objective! };
    if (request.method === "clear") goal = null;
    return { goal };
  });
  const ui = await mount(invoke);

  expect(ui.container.querySelector("textarea")).toBeNull();
  await ui.expand();
  expect(document.activeElement).toBe(ui.container.querySelector("textarea"));
  expect(ui.button("开始目标").disabled).toBe(true);
  await ui.fill("  Ship the mobile UI  ");
  await ui.click("开始目标");
  expect(invoke).toHaveBeenCalledWith(expect.objectContaining({
    method: "create_goal",
    input: { thread_id: "thread-one", objective: "Ship the mobile UI" },
  }));
  expect(ui.container.textContent).toContain("Ship the mobile UI");
  expect(ui.container.querySelector("textarea")).toBeNull();
  expect(document.activeElement).toBe(ui.container.querySelector("button[aria-expanded]"));

  await ui.click("结束目标");
  expect(invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "clear", input: { thread_id: "thread-one" } }));
  expect(ui.container.textContent).toBe("");
});

it("refreshes completed usage when settlement arrives after the terminal event", async () => {
  let goal = { ...activeGoal, status: "complete", tokens_used: 0, time_used_seconds: 0 };
  const ui = await mount(async () => ({ goal }));
  await ui.expand();
  act(() => ui.host.publishHostEvent({ kind: "notification", message: { method: "turn/completed" } }));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
    await Promise.resolve();
  });
  expect(ui.container.textContent).toContain("0 tokens");
  goal = { ...goal, tokens_used: 4014, time_used_seconds: 20 };
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1500);
    await Promise.resolve();
  });
  expect(ui.container.textContent).toContain("4014 tokens");
});

it("discards stale reads and shows runtime errors even when collapsed", async () => {
  const pending: Array<{ resolve: (value: unknown) => void; reject: (error: Error) => void }> = [];
  const ui = await mount(() => new Promise((resolve, reject) => pending.push({ resolve, reject })));
  await act(async () => vi.advanceTimersByTimeAsync(1500));
  await act(async () => pending[1].resolve({ goal: { ...activeGoal, objective: "Current goal" } }));
  await act(async () => pending[0].resolve({ goal: { ...activeGoal, objective: "Stale goal" } }));
  expect(ui.container.textContent).toContain("Current goal");
  expect(ui.container.textContent).not.toContain("Stale goal");

  await act(async () => vi.advanceTimersByTimeAsync(1500));
  await act(async () => pending.at(-1)!.reject(new Error("storage unavailable")));
  expect(ui.container.querySelector('[role="alert"]')?.textContent).toContain("storage unavailable");
});

it("scopes the direct entry to an editable main conversation", async () => {
  const invoke = vi.fn(async (request: Parameters<InvokeRuntime>[0]) => ({
    goal: (request.input as { thread_id: string }).thread_id === "thread-two" ? activeGoal : { ...activeGoal, status: "complete" },
  }));
  const ui = await mount(invoke);
  await ui.expand();
  await ui.fill("Draft for thread one");

  await ui.render("thread-two", { readOnly: true });
  expect(ui.container.querySelector("textarea")).toBeNull();
  expect(ui.button("暂停").disabled).toBe(true);
  expect(ui.button("结束目标").disabled).toBe(true);
  await ui.render("thread-side", { mainConversation: false });
  expect(ui.container.textContent).toBe("");
});


it("keeps ordinary conversations empty until a goal is created", async () => {
  let goal: Goal | null = null;
  const ui = await mount(async () => ({ goal }));
  expect(ui.container.textContent).toBe("");
  expect(ui.container.querySelector("section")).toBeNull();

  goal = activeGoal;
  await act(async () => {
    ui.host.publishHostEvent({ kind: "notification", message: { method: "turn/completed" } });
    await Promise.resolve();
    await Promise.resolve();
  });
  expect(ui.container.textContent).toContain(activeGoal.objective);
  expect(ui.button("结束目标").disabled).toBe(false);
});
