import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ChannelRoom, InitializeResult, NamedAgent, WuuDesktopApi } from "../shared/protocol";
import { translateCurrent as t } from "./i18n";

vi.mock("@xterm/xterm", () => ({ Terminal: vi.fn() }));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: vi.fn() }));
vi.mock("./WorkspaceMonacoEditor", () => ({ WorkspaceMonacoEditor: () => null }));
import { App } from "./App";

let container: HTMLDivElement;
let root: Root;
let agents: NamedAgent[];
let rooms: ChannelRoom[];
const workspace = "/tmp/wuu-agent-onboarding-test";
const initialized: InitializeResult = {
  protocol_version: "wuu-app-server/v0.1", provider: "byok", model: "reasoner",
  workspace_root: workspace, permissions: { mode: "standard" },
  providers: [{ name: "byok", type: "openai-compatible", model: "reasoner", api_key_configured: true,
    models: [{ id: "reasoner", display_name: "Reasoner", supported_efforts: ["low", "high"], default_effort: "high" }] }],
};

async function click(label: string): Promise<void> {
  const button = [...document.querySelectorAll<HTMLButtonElement>("button")].find(
    (item) => item.getAttribute("aria-label") === label || item.textContent?.trim() === label,
  );
  expect(button, `Button ${label}`).toBeTruthy();
  await act(async () => { button!.click(); });
}

async function enterName(value: string): Promise<void> {
  const input = document.querySelector<HTMLInputElement>('[role="dialog"] input[type="text"], [role="dialog"] input:not([type])');
  expect(input).toBeTruthy();
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input!.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

beforeEach(() => {
  agents = [];
  rooms = [];
  window.localStorage.clear();
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  Object.defineProperty(window, "matchMedia", { configurable: true, value: vi.fn((media: string) => ({
    matches: false, media, onchange: null, addEventListener: vi.fn(), removeEventListener: vi.fn(),
    addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
  })) });
  Object.defineProperty(document, "elementFromPoint", { configurable: true, value: () => null });
  const projectState = { projects: [], active_context: { kind: "no_project", cwd: workspace } };
  const api = {
    initialize: vi.fn().mockResolvedValue(initialized),
    listProjects: vi.fn().mockResolvedValue(projectState), selectNoProject: vi.fn().mockResolvedValue(projectState),
    listThreads: vi.fn().mockResolvedValue({ threads: [] }), listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({ is_repo: false, dirty_count: 0, files: [] }),
    onServerEvent: vi.fn(() => () => {}), onWindowResizeState: vi.fn(() => () => {}), onTerminalEvent: vi.fn(() => () => {}),
    listChannelRooms: vi.fn(async () => ({ rooms })), listNamedAgents: vi.fn(async () => ({ agents })),
    listChannelMessages: vi.fn().mockResolvedValue({ messages: [], responses: [] }),
    markChannelRoomRead: vi.fn().mockResolvedValue({}),
    listChannelTasks: vi.fn().mockResolvedValue({ tasks: [] }),
    listChannelSessions: vi.fn().mockResolvedValue({ sessions: [] }),
    createNamedAgent: vi.fn(async (params) => {
      const agent = { id: "new-agent", ...params, created_at: "2026-09-12T00:00:00Z" } as NamedAgent;
      agents = [agent];
      return { agent };
    }),
    openChannelDirectMessage: vi.fn(async () => {
      const room = { id: "new-dm", name: "Research", kind: "dm", members: [{ member_type: "agent", member_id: "new-agent" }], created_at: "2026-09-12T00:00:00Z" } as ChannelRoom;
      rooms = [room];
      return { room };
    }),
  } as unknown as WuuDesktopApi;
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => { root.unmount(); });
  container.remove();
  vi.unstubAllGlobals();
});

it("opens a newly created identity's conversation before the next directory refresh", async () => {
  await act(async () => { root.render(<App />); });
  await click("collaboration");
  await click(t("channels.newAgent"));
  await enterName("Research");
  await click(t("agentOnboarding.continue"));
  await click(t("agentOnboarding.create"));
  expect(window.wuu.createNamedAgent).toHaveBeenCalledTimes(1);
  expect(window.wuu.openChannelDirectMessage).toHaveBeenCalledWith({ agent_id: "new-agent" });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(container.querySelector(".channel-room-header")?.textContent).toContain("Research");
  expect(container.querySelector(".collaboration-contact-row.active")?.textContent).toContain("Research");
});

it("preserves the agent draft across provider settings and returns to the model step", async () => {
  await act(async () => { root.render(<App />); });
  await click("collaboration");
  await click(t("channels.newAgent"));
  await enterName("Research");
  await click(t("agentOnboarding.continue"));
  await click(t("agentOnboarding.manageProviders"));
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  await act(async () => { window.dispatchEvent(new Event("wuu:workbench-back")); });
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain("Research");
  await click(t("agentOnboarding.create"));
  expect(window.wuu.createNamedAgent).toHaveBeenCalledWith(expect.objectContaining({ name: "Research", provider_override: "byok", model_override: "reasoner" }));
});


it("keeps Harness environment reservations out of Collaboration and restores the open panel on return", async () => {
  const matchMedia = window.matchMedia;
  vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
    ...matchMedia(query), matches: query.includes("min-width: 1320px"),
  }));
  await act(async () => { root.render(<App />); });
  await click(t("shell.showEnvironmentInfo"));
  expect(container.querySelector("main.environment-panel-reserved")).toBeTruthy();
  expect(container.querySelector("main.environment-panel-visible")).toBeTruthy();
  await click("collaboration");
  expect(container.querySelector("main.environment-panel-visible, main.environment-panel-reserved, main.side-thread-panel-visible")).toBeNull();
  await click("harness");
  expect(container.querySelector("main.environment-panel-visible")).toBeTruthy();
});
