import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { ApplicationShellOutlet } from "./ApplicationShell";

const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/goal-ui/desktop.js"), "utf8");
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

it("rejects a host without selected-slot targeting before registering presentation", async () => {
  await expect(activate({} as PluginGenerationApi)).rejects.toThrow("ui.selected-slot-target.v1");
});

afterEach(() => {
  for (const cleanup of cleanups.splice(0)) cleanup();
  vi.useRealTimers();
});

async function mount(invokeRuntime: InvokeRuntime) {
  vi.useFakeTimers();
  const host = new PluginHost({ react: React, invokeRuntime: request => {
    expect(request).toMatchObject({ pluginId: "goal-ui", service: "goal.control", method: "request" });
    const { method, input } = request.input as { method: string; input: unknown };
    return invokeRuntime({ ...request, method, input });
  } });

  await host.activateGeneration({ pluginId: "test-shell", generation: "one", register(api) {
    api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name="wuu.default-composer/composer.above" context={context} /> });
  } });
  let context: Record<string, unknown> = {};
  await host.activateGeneration({ pluginId: "wuu.default-composer", generation: "one", register(api) {
    api.registerUISlot({ id: "composer.above", cardinality: "many" });
    api.registerUIContribution({ id: "composer", target: "test-shell/composer", render: () => null });
  } });
  await host.activateGeneration({
    pluginId: "goal-ui",
    generation: "one",
    register: activate,
  });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => {
    act(() => root.unmount());
    host.disable("goal-ui");
    host.unload("wuu.default-composer");
    host.unload("test-shell");
    container.remove();
  });

  const render = async (threadId = "thread-one", extra: Record<string, unknown> = {}) => {
    await act(async () => {
      context = { threadId, mainConversation: true, ...extra };
      root.render(<ApplicationShellOutlet key={JSON.stringify(context)} host={host} binding={{ pluginId: "test-shell", generation: "one", id: "shell" }}
        surfaceBindings={{ "ui.composer": { pluginId: "wuu.default-composer", generation: "one", id: "composer" } }} />);
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

it("renders an unavailable service without crashing and recovers on the selected route", async () => {
  let available = false;
  const failure = new Error("Selected goal.control provider is unavailable");
  const ui = await mount(async () => {
    if (!available) throw failure;
    return { goal: activeGoal };
  });
  expect(ui.container.querySelector('[role="alert"]')?.textContent?.trim()).toBeTruthy();
  expect(ui.container.textContent).not.toContain(failure.message);
  expect(ui.host.getGenerationDiagnostics("goal-ui", "one")).toEqual(expect.arrayContaining([expect.objectContaining({ kind: "runtime", cause: failure })]));
  expect(ui.container.textContent).not.toContain(activeGoal.objective);
  available = true;
  await act(async () => vi.advanceTimersByTimeAsync(1500));
  expect(ui.container.querySelector('[role="alert"]')).toBeNull();
  expect(ui.container.textContent).toContain(activeGoal.objective);
});

it("hides an absent optional provider, stops polling, and recovers after the plugin inventory changes", async () => {
  let available = false;
  const invoke = vi.fn(async () => {
    if (!available) throw new Error("Error invoking remote method: no provider for service goal.control");
    return { goal: activeGoal };
  });
  const ui = await mount(invoke);
  expect(ui.container.textContent).toBe("");
  expect(invoke).toHaveBeenCalledTimes(1);
  await act(async () => vi.advanceTimersByTimeAsync(4500));
  expect(invoke).toHaveBeenCalledTimes(1);

  available = true;
  await act(async () => {
    ui.host.publishHostEvent({ kind: "notification", message: { method: "plugin/inventory/changed" } });
    await Promise.resolve();
    await Promise.resolve();
  });
  expect(ui.container.textContent).toContain(activeGoal.objective);
  expect(invoke).toHaveBeenCalledTimes(2);
  await act(async () => vi.advanceTimersByTimeAsync(1500));
  expect(invoke).toHaveBeenCalledTimes(3);
});

it("hides an open goal when its provider disappears and still reports unrelated provider errors", async () => {
  let mode: "available" | "missing" | "broken" = "available";
  const failure = new Error("no provider for service goal.control major version 1 (provided majors: [2])");
  const invoke = vi.fn(async () => {
    if (mode === "missing") throw new Error("no provider for service goal.control");
    if (mode === "broken") throw failure;
    return { goal: activeGoal };
  });
  const ui = await mount(invoke);
  await ui.expand();
  mode = "missing";
  await act(async () => vi.advanceTimersByTimeAsync(1500));
  expect(ui.container.textContent).toBe("");
  const calls = invoke.mock.calls.length;
  await act(async () => vi.advanceTimersByTimeAsync(3000));
  expect(invoke).toHaveBeenCalledTimes(calls);

  mode = "broken";
  await act(async () => {
    ui.host.publishHostEvent({ kind: "notification", message: { method: "plugin/inventory/changed" } });
    await Promise.resolve();
    await Promise.resolve();
  });
  expect(ui.container.querySelector('[role="alert"]')?.textContent?.trim()).toBeTruthy();
  expect(ui.container.textContent).not.toContain(failure.message);
  await act(async () => vi.advanceTimersByTimeAsync(1500));
  expect(invoke).toHaveBeenCalledTimes(calls + 2);
});

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

  act(() => ui.host.disable("goal-ui"));
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
  // Settled usage reaches the card, grouped like other counts in the locale.
  expect(ui.container.textContent).toContain("4,014");
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
  const failure = new Error("storage unavailable");
  await act(async () => pending.at(-1)!.reject(failure));
  expect(ui.container.querySelector('[role="alert"]')?.textContent?.trim()).toBeTruthy();
  expect(ui.container.textContent).not.toContain(failure.message);
  expect(ui.host.getGenerationDiagnostics("goal-ui", "one")).toEqual(expect.arrayContaining([expect.objectContaining({ kind: "runtime", cause: failure })]));
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

it("keeps action failures diagnostic-only while allowing an explicit retry", async () => {
  const failure = new Error("internal goal writer path /private/state failed");
  let rejected = true, goal = activeGoal;
  const invoke = vi.fn(async (request: Parameters<InvokeRuntime>[0]) => {
    if (request.method === "pause") {
      if (rejected) throw failure;
      goal = { ...goal, status: "paused" };
    }
    return { goal };
  });
  const ui = await mount(invoke);
  await ui.click("暂停");
  expect(ui.container.querySelector('[role="alert"]')?.textContent?.trim()).toBeTruthy();
  expect(ui.container.textContent).not.toContain(failure.message);
  expect(ui.host.getGenerationDiagnostics("goal-ui", "one")).toEqual(expect.arrayContaining([expect.objectContaining({ kind: "runtime", cause: failure })]));
  expect(ui.button("暂停").disabled).toBe(false);
  rejected = false;
  await ui.click("暂停");
  expect(ui.container.querySelector('[role="alert"]')).toBeNull();
  expect(invoke.mock.calls.filter(([request]) => request.method === "pause")).toHaveLength(2);
});
