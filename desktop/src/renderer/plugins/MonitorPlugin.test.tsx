import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";

const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/monitor/desktop.js"), "utf8");
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))();
const cleanups: Array<() => void> = [];
afterEach(() => { for (const cleanup of cleanups.splice(0)) cleanup(); vi.useRealTimers(); });
const task = { id: "task", thread_id: "a", description: "Real monitor", status: "running", lines: 1 };
function pending() { let resolve!: (value: unknown) => void, reject!: (error: unknown) => void; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
async function mount() {
  vi.useFakeTimers();
  const lifetime = new AbortController(), calls: Array<ReturnType<typeof pending> & { input: any; signal: AbortSignal }> = [], notices: Array<{ message: string; dispose: ReturnType<typeof vi.fn> }> = [];
  let contribution: any;
  const cleanup: Array<() => void> = [];
  await activate({ react: React, signal: lifetime.signal,
    ui: { Button: (props: any) => React.createElement("button", props), ComposerDrawer: ({ children, summary, notice, expanded, onExpandedChange }: any) => React.createElement("section", null, React.createElement("button", { onClick: () => onExpandedChange(!expanded), "aria-label": "Toggle monitors" }, summary), notice, children) },
    invokeRuntime: vi.fn((_method: string, input: any, options: { signal: AbortSignal }) => { const call = { ...pending(), input, signal: options?.signal }; calls.push(call); return call.promise; }),
    showNotice: vi.fn(({ message }: any) => { const notice = { message, dispose: vi.fn() }; notices.push(notice); return notice; }),
    registerLocale() {}, registerStyle() {}, registerCleanup(fn: () => void) { cleanup.push(fn); }, onHostEvent() {},
    registerUIContribution(value: any) { contribution = value; },
  });
  const container = document.createElement("div"), root = createRoot(container);
  const translate = (key: string) => key;
  const render = async (threadId: string) => { await act(async () => { root.render(contribution.render({ context: { threadId, mainConversation: true }, translate, locale: "en-US" })); }); };
  cleanups.push(() => { act(() => root.unmount()); cleanup.forEach(fn => fn()); });
  await render("a");
  const click = async (label: string) => { await act(async () => { const button = [...container.querySelectorAll("button")].find(node => node.getAttribute("aria-label") === label || node.textContent?.includes(label)); expect(button).toBeDefined(); button!.click(); }); };
  return { container, calls, notices, render, lifetime, click };
}
it.each(["resolve", "reject"] as const)("fences stale %s across A → B → A without blocking a new request", async outcome => {
  const ui = await mount();
  await ui.render("b"); await ui.render("a");
  expect(ui.calls).toHaveLength(3);
  await act(async () => { if (outcome === "resolve") ui.calls[0].resolve({ tasks: [task] }); else ui.calls[0].reject(new Error("old request failed")); });
  expect(ui.container.textContent).toBe(""); expect(ui.notices).toHaveLength(0);
  await act(async () => { ui.calls[2].resolve({ tasks: [task] }); });
  expect(ui.container.querySelector("section")).not.toBeNull();
});
it("retires mounted views immediately on generation abort before delayed cleanup", async () => {
  const ui = await mount();
  await act(async () => { ui.calls[0].resolve({ tasks: [task] }); });
  expect(ui.container.querySelector("section")).not.toBeNull();
  await act(async () => { ui.lifetime.abort(); });
  expect(ui.container.textContent).toBe("");
  await act(async () => { vi.advanceTimersByTime(2000); });
  expect(ui.calls).toHaveLength(1);
});
it("keeps current errors out of an empty drawer, deduplicates notices, and recovers", async () => {
  const ui = await mount();
  await act(async () => { ui.calls[0].reject(new Error("private raw transport error")); });
  expect(ui.container.textContent).toBe(""); expect(ui.notices).toHaveLength(1);
  expect(ui.notices[0].message).not.toContain("private raw");
  await act(async () => { vi.advanceTimersByTime(1000); });
  await act(async () => { ui.calls[1].reject(new Error("private raw transport error")); });
  expect(ui.notices).toHaveLength(1);
  await act(async () => { vi.advanceTimersByTime(1000); });
  await act(async () => { ui.calls[2].resolve({ tasks: [task] }); });
  expect(ui.notices[0].dispose).toHaveBeenCalledOnce(); expect(ui.container.querySelector("section")).not.toBeNull();
});
it("does not show expected typed scope retirement as a failure", async () => {
  const ui = await mount();
  await act(async () => { ui.calls[0].reject(Object.assign(new Error("retired"), { name: "AbortError", code: "WUU_PLUGIN_SCOPE_RETIRED" })); });
  expect(ui.container.textContent).toBe(""); expect(ui.notices).toHaveLength(0);
});

it.each(["resolve", "reject"] as const)("fences late output and stop %s when the session view is replaced", async outcome => {
  const ui = await mount();
  await act(async () => { ui.calls[0].resolve({ tasks: [task] }); });
  await ui.click("Toggle monitors"); await ui.click("Real monitor");
  const read = ui.calls.find(call => call.input.action === "read")!;
  await ui.click("monitor.stop: Real monitor");
  const stop = ui.calls.find(call => call.input.action === "stop")!;
  await ui.render("b");
  expect(read.signal.aborted).toBe(true); expect(stop.signal.aborted).toBe(true);
  await act(async () => {
    if (outcome === "resolve") { read.resolve({ output: "stale output", next_offset: 12 }); stop.resolve({}); }
    else { read.reject(new Error("old read")); stop.reject(new Error("old stop")); }
  });
  expect(ui.notices).toHaveLength(0); expect(ui.container.textContent).toBe("");
  expect(ui.calls.filter(call => call.input.action === "list" && call.input.thread_id === "a")).toHaveLength(1);
});
it("retains task failure details and current output, and allows retrying a failed stop", async () => {
  const ui = await mount();
  await act(async () => { ui.calls[0].resolve({ tasks: [{ ...task, error: "process failure details" }] }); });
  await ui.click("Toggle monitors"); await ui.click("Real monitor");
  const read = ui.calls.find(call => call.input.action === "read")!;
  await act(async () => { read.resolve({ output: "useful process output", next_offset: 21 }); });
  expect(ui.container.textContent).toContain("process failure details"); expect(ui.container.textContent).toContain("useful process output");
  await ui.click("monitor.stop: Real monitor");
  await act(async () => { ui.calls.at(-1)!.reject(new Error("transport detail")); });
  expect(ui.notices).toHaveLength(1); expect(ui.container.textContent).not.toContain("transport detail");
  await ui.click("monitor.stop: Real monitor");
  await act(async () => { ui.calls.at(-1)!.resolve({}); });
  expect(ui.notices[0].dispose).toHaveBeenCalledOnce();
  expect(ui.calls.at(-1)!.input.action).toBe("list");
});
