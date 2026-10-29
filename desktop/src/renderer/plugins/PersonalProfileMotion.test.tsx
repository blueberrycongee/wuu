import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PluginConversationComposer, PluginConversationMessage, PluginConversationView } from "./PluginConversation";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";
import { PluginViewContent } from "./Workbench";
import { selectPersonalTestApplication } from "./PersonalApplication.test-support";

// Exercise the shipped plugin rather than a parallel panel implementation.
const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/personal/desktop.js"), "utf8");
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
const cleanups: Array<() => void> = [];
const triggerSelector = '[data-action="toggle-personal-panel"]';
beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { cleanups.splice(0).reverse().forEach(cleanup => cleanup()); vi.useRealTimers(); vi.restoreAllMocks(); });

async function mount({ systemReduced = false, appReduced = false, failUpdate = false, saveGate, ensureGate, staleOpen = false }: { systemReduced?: boolean; appReduced?: boolean; failUpdate?: boolean; saveGate?: Promise<void>; ensureGate?: Promise<void>; staleOpen?: boolean } = {}) {
  const priorMotion = document.documentElement.getAttribute("data-appearance-motion");
  document.documentElement.setAttribute("data-appearance-motion", appReduced ? "reduce" : "full");
  cleanups.push(() => { if (priorMotion === null) document.documentElement.removeAttribute("data-appearance-motion"); else document.documentElement.setAttribute("data-appearance-motion", priorMotion); });
  vi.spyOn(window, "matchMedia").mockImplementation(query => ({ media: query, matches: systemReduced && query === "(prefers-reduced-motion: reduce)", onchange: null, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() }));
  const agent = { agent_id: "profile-main", user_space_id: "local", name: "Mosaic", kind: "main", main_session_id: "profile-private", status: "active", revision: 1 };
  const initialAgent = { ...agent };
  const updates: unknown[] = [];
  let ensureCount = 0;
  const host = new PluginHost({ react: React, conversationComponents: { Composer: PluginConversationComposer, Message: PluginConversationMessage, View: PluginConversationView }, invokeRuntime: async ({ method, input }) => {
    if (method === "agents.update") { updates.push(input); if (saveGate) await saveGate; if (failUpdate) throw new Error("storage unavailable"); const data = input as { name: string; expected_revision: number }; expect(data.expected_revision).toBe(agent.revision); agent.name = data.name; agent.revision++; return { agent: { ...agent } }; }
    if (method === "agents.ensure_default") { if (ensureGate && ensureCount++ > 0) { await ensureGate; return { agent: initialAgent, session: { state: "completed" } }; } return { agent, session: { state: "completed" } }; }
    if (method === "agents.open") return { agent: staleOpen ? initialAgent : agent, session: { state: "completed" } };
    if (method === "agents.list") return { agents: [agent], default_agent_id: agent.agent_id };
    if (method === "agents.history" || method === "tasks.history") return { records: [] };
    if (method === "tasks.list") return { tasks: [{ task_id: "profile-task", agent_id: agent.agent_id, title: "Plan a trip", goal: "Find a route", state: "running", revision: 1, requirements_revision: 1 }] };
    return {};
  } });
  await host.activateGeneration({ pluginId: "personal", generation: "profile-motion", register: activate });
  const controller = await selectPersonalTestApplication(host), container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); controller.dispose(); host.unload("personal"); host.unload("test.application"); container.remove(); });
  await act(async () => root.render(<PluginViewContent controller={controller} pluginId="personal" viewTypeId="personal.home" />));
  const panel = () => container.querySelector<HTMLElement>('[data-personal-panel]')!;
  const trigger = () => container.querySelector<HTMLButtonElement>(triggerSelector)!;
  const click = async (selector: string) => { const node = container.querySelector<HTMLElement>(selector); expect(node).not.toBeNull(); await act(async () => node!.click()); };
  const escape = async () => act(async () => { panel().dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
  const remount = async () => act(async () => root.render(<PluginViewContent key="remount" controller={controller} pluginId="personal" viewTypeId="personal.home" />));
  return { container, panel, trigger, click, escape, updates, remount };
}
async function advance(milliseconds: number) { await act(async () => { vi.advanceTimersByTime(milliseconds); }); }
async function open(ui: Awaited<ReturnType<typeof mount>>) { await ui.click(triggerSelector); await advance(350); expect(ui.panel().dataset.panelPhase).toBe("open"); }

it("uses a pure-avatar trigger while keeping its name and status accessible", async () => {
  const ui = await mount();
  const identity = ui.container.querySelector<HTMLButtonElement>('.mosaic-identity-label');
  expect(ui.trigger().textContent?.trim()).toBe("");
  expect(identity?.textContent).toBe("Mosaic");
  expect(identity?.dataset.action).toBe("rename-personal");
  expect(ui.container.querySelector('[data-action="search-history"]')).toBeNull();
  expect(ui.trigger().querySelector(".mosaic-avatar")).not.toBeNull();
  expect(ui.trigger().getAttribute("aria-label")).toContain("Mosaic");
  expect(ui.trigger().getAttribute("aria-label")).toMatch(/Ready|已就绪/);
  expect(ui.trigger().getAttribute("aria-haspopup")).toBe("dialog");
  expect(ui.trigger().getAttribute("aria-controls")).toBe(ui.panel().id);
  expect(ui.panel().hidden).toBe(true);
});

it("focuses the dialog itself and hides the duplicate top trigger without rendering a close X", async () => {
  const ui = await mount(); await ui.click(triggerSelector);
  expect(ui.panel().hidden).toBe(false);
  expect(ui.panel().tabIndex).toBe(-1);
  expect(document.activeElement).toBe(ui.panel());
  expect(ui.panel().getAttribute("role")).toBe("dialog");
  expect(ui.panel().getAttribute("aria-label")).toMatch(/^(About twin|关于 twin)$/);
  expect(ui.trigger().getAttribute("aria-hidden")).toBe("true");
  expect(ui.trigger().hasAttribute("inert")).toBe(true);
  expect(ui.trigger().getAttribute("aria-expanded")).toBe("true");
  expect(ui.panel().querySelector('[data-action="close-personal-panel"]')).toBeNull();
  expect(ui.panel().textContent).not.toContain("×");
  await advance(350); expect(ui.trigger().getAttribute("aria-hidden")).toBe("true");
  expect(ui.trigger().hasAttribute("inert")).toBe(true);
  expect(ui.trigger().getAttribute("aria-expanded")).toBe("true");
});

it("retains the closing panel for 300ms but removes interaction and accessibility immediately", async () => {
  const ui = await mount(); await open(ui); const panel = ui.panel(); await ui.escape();
  expect(ui.panel()).toBe(panel);
  expect(panel.dataset.panelPhase).toBe("closing");
  expect(panel.hidden).toBe(false);
  expect(panel.getAttribute("aria-hidden")).toBe("true");
  expect(panel.hasAttribute("inert")).toBe(true);
  expect(ui.trigger().getAttribute("aria-hidden")).not.toBe("true");
  expect(ui.trigger().hasAttribute("inert")).toBe(false);
  await advance(299); expect(panel.hidden).toBe(false);
  await advance(1); expect(panel.hidden).toBe(true);
  expect(panel.dataset.panelPhase).toBe("closed");
  expect(document.activeElement).toBe(ui.trigger());
});

it("does not let an interrupted close timer hide or steal focus from a reopened panel", async () => {
  const ui = await mount(); await open(ui); await ui.escape(); await advance(150); await ui.click(triggerSelector);
  expect(ui.panel().hidden).toBe(false);
  expect(ui.panel().getAttribute("aria-hidden")).not.toBe("true");
  expect(ui.panel().hasAttribute("inert")).toBe(false);
  expect(document.activeElement).toBe(ui.panel());
  await advance(150); // First close's former deadline.
  expect(ui.panel().hidden).toBe(false);
  await advance(350);
  expect(ui.panel().dataset.panelPhase).toBe("open");
  expect(document.activeElement).toBe(ui.panel());
  expect(ui.trigger().getAttribute("aria-hidden")).toBe("true");
  expect(ui.trigger().hasAttribute("inert")).toBe(true);
  expect(ui.trigger().getAttribute("aria-expanded")).toBe("true");
});

it("retains selected detail during exit instead of flashing the profile overview", async () => {
  const ui = await mount(); await open(ui); await ui.click('[data-task-id="profile-task"]');
  const detail = ui.panel().querySelector('[data-task-detail="profile-task"]'); expect(detail).not.toBeNull();
  await act(async () => { ui.container.querySelector(".mosaic-main")!.dispatchEvent(new Event("pointerdown", { bubbles: true })); });
  expect(ui.panel().dataset.panelPhase).toBe("closing");
  expect(ui.panel().querySelector('[data-task-detail="profile-task"]')).toBe(detail);
  expect(ui.panel().querySelector(".mosaic-profile-head")).toBeNull();
  await advance(299); expect(ui.panel().querySelector('[data-task-detail="profile-task"]')).toBe(detail);
  await advance(1); expect(ui.panel().hidden).toBe(true);
  await open(ui);
  expect(ui.panel().querySelector('[data-task-detail]')).toBeNull();
  expect(ui.panel().querySelector(".mosaic-profile-head")).not.toBeNull();
});

it.each([{ systemReduced: true, appReduced: false }, { systemReduced: false, appReduced: true }])("closes immediately with reduced motion (%j)", async options => {
  const ui = await mount(options); await open(ui); await ui.escape();
  expect(ui.panel().hidden).toBe(true);
  expect(ui.panel().dataset.panelPhase).toBe("closed");
  expect(ui.trigger().getAttribute("aria-hidden")).not.toBe("true");
  expect(ui.trigger().hasAttribute("inert")).toBe(false);
});

it("cancels pending focus restoration when closing and reopening before the next frame", async () => {
  const ui = await mount(); await open(ui);
  await ui.escape(); await ui.click(triggerSelector);
  expect(ui.panel().dataset.panelVisible).toBe("true");
  await advance(350);
  expect(ui.panel().dataset.panelPhase).toBe("open");
  expect(ui.panel().hidden).toBe(false);
  expect(document.activeElement).toBe(ui.panel());
});

it("does not let an unfinished entrance reopen a panel closed before its first animation frame", async () => {
  const ui = await mount(); await ui.click(triggerSelector); await ui.escape();
  expect(ui.panel().dataset.panelPhase).toBe("closing");
  await advance(350);
  expect(ui.panel().dataset.panelPhase).toBe("closed");
  expect(ui.panel().hidden).toBe(true);
  expect(ui.panel().hasAttribute("inert")).toBe(true);
});

it("settles an in-flight exit when app motion preference changes to reduce", async () => {
  const ui = await mount(); await open(ui); await ui.escape();
  expect(ui.panel().hidden).toBe(false);
  await act(async () => { document.documentElement.dataset.appearanceMotion = "reduce"; await Promise.resolve(); });
  expect(ui.panel().hidden).toBe(true);
  await advance(350);
  expect(ui.panel().dataset.panelPhase).toBe("closed");
});

it("keeps synthetic slow-motion exit retention synchronized with the duration token", async () => {
  const ui = await mount();
  // JSDOM does not inherit custom properties; the browser fixture sets this on the root.
  ui.panel().style.setProperty("--mosaic-profile-motion-duration", "2400ms");
  await ui.click(triggerSelector); await advance(2500); await ui.escape();
  await advance(2399); expect(ui.panel().hidden).toBe(false);
  await advance(1); expect(ui.panel().hidden).toBe(true);
});

async function nameValue(ui: Awaited<ReturnType<typeof mount>>, value: string) {
  const input = ui.container.querySelector<HTMLInputElement>(".mosaic-name-input")!;
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value); input.dispatchEvent(new Event("input", { bubbles: true })); });
}
it("renames through the persistent runtime contract and updates every visible identity as plain text", async () => {
  const ui = await mount(); await ui.click('.mosaic-identity-label');
  expect(document.activeElement).toBe(ui.container.querySelector('.mosaic-name-input'));
  expect(ui.container.querySelector<HTMLButtonElement>('.mosaic-name-save')!.disabled).toBe(true);
  await nameValue(ui, '  <My twin>  '); await ui.click('.mosaic-name-save');
  expect(ui.updates).toEqual([{ agent_id: 'profile-main', expected_revision: 1, name: '<My twin>' }]);
  expect(ui.container.querySelector('.mosaic-name-dialog')).toBeNull();
  expect(ui.trigger().getAttribute('aria-label')).toContain('<My twin>');
  expect(ui.container.querySelector('.mosaic-identity-label strong')?.textContent).toBe('<My twin>');
  expect(ui.container.querySelector('.mosaic-profile-name')?.textContent).toBe('<My twin>');
  expect(ui.container.querySelector('my')).toBeNull();
});
it("does not persist blank, overlong or cancelled names", async () => {
  const ui = await mount(); await ui.click('.mosaic-identity-label');
  for (const value of ['   ', '🙂'.repeat(65)]) { await nameValue(ui, value); expect(ui.container.querySelector<HTMLButtonElement>('.mosaic-name-save')!.disabled).toBe(true); }
  await nameValue(ui, 'Cancelled'); await ui.click('.mosaic-name-close');
  expect(ui.updates).toHaveLength(0); expect(ui.trigger().getAttribute('aria-label')).toContain('Mosaic');
});
it("keeps the entered name and prior identity when the runtime save fails", async () => {
  const ui = await mount({ failUpdate: true }); await ui.click('.mosaic-identity-label'); await nameValue(ui, 'Retry me'); await ui.click('.mosaic-name-save');
  expect(ui.container.querySelector('[role="alert"]')?.textContent).toContain('storage unavailable');
  expect(ui.container.querySelector<HTMLInputElement>('.mosaic-name-input')!.value).toBe('Retry me');
  expect(ui.trigger().getAttribute('aria-label')).toContain('Mosaic');
  expect(ui.container.querySelector<HTMLButtonElement>('.mosaic-name-save')!.disabled).toBe(false);
});

it("traps keyboard focus in the rename dialog while a save is pending and restores input on failure", async () => {
  let release!: () => void; const saveGate = new Promise<void>(resolve => { release = resolve; });
  const ui = await mount({ saveGate, failUpdate: true }); await ui.click('.mosaic-identity-label'); await nameValue(ui, 'Pending'); await ui.click('.mosaic-name-save');
  const dialog = ui.container.querySelector<HTMLElement>('.mosaic-name-dialog')!;
  expect(document.activeElement).toBe(dialog);
  for (const shiftKey of [false, true]) { const event = new KeyboardEvent('keydown', { key:'Tab', shiftKey, bubbles:true, cancelable:true }); await act(async () => dialog.dispatchEvent(event)); expect(event.defaultPrevented).toBe(true); expect(document.activeElement).toBe(dialog); }
  await act(async () => { release(); });
  expect(document.activeElement).toBe(ui.container.querySelector('.mosaic-name-input'));
  expect(ui.container.querySelector('[role="alert"]')).not.toBeNull();
});
it("rejects late older initialization and polling snapshots after a successful rename", async () => {
  let release!: () => void; const ensureGate = new Promise<void>(resolve => { release = resolve; });
  const ui = await mount({ ensureGate, staleOpen: true });
  await ui.remount();
  await ui.click('.mosaic-identity-label'); await nameValue(ui, 'Newest'); await ui.click('.mosaic-name-save');
  expect(ui.trigger().getAttribute('aria-label')).toContain('Newest');
  await act(async () => { release(); });
  expect(ui.trigger().getAttribute('aria-label')).toContain('Newest');
  expect(ui.container.querySelector('.mosaic-profile-name')?.textContent).toBe('Newest');
});
