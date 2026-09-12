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
  const input = document.querySelector<HTMLInputElement>('[name="agent-name"]');
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
    getBuildInfo: vi.fn().mockResolvedValue({ desktop: { version: "2026.9.1", build_date: "2026-09-12" } }),
    listMCPServers: vi.fn().mockResolvedValue({ servers: [] }),
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

async function manageSidebarRow(name: string, action: string): Promise<void> {
  const row = Array.from(container.querySelectorAll<HTMLButtonElement>(".collaboration-contact-row")).find(button => button.querySelector("strong")?.textContent === name)!;
  expect(row).toBeTruthy();
  act(() => row.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 50, clientY: 100 })));
  await click(action);
}

it("pins a new Agent without navigation, persists hiding, and restores its DM from settings", async () => {
  agents = [{ id: "new-agent", name: "Research", avatar_key: "abstract-1", memory_dir: "/preview", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  rooms = [{ id: "general", name: "General", kind: "channel", members: [], created_by: "human", created_at: agents[0].created_at }];
  vi.mocked(window.wuu.openChannelDirectMessage).mockImplementation(async () => {
    const room = { id: "new-dm", name: "Research", kind: "dm", members: [{ member_type: "agent", member_id: "new-agent" }], created_at: agents[0].created_at } as ChannelRoom;
    rooms = [...rooms, room];
    return { room };
  });
  await act(async () => { root.render(<App />); });
  await click("collaboration");
  await manageSidebarRow("Research", t("sidebar.pin"));
  expect(window.wuu.openChannelDirectMessage).toHaveBeenCalledExactlyOnceWith({ agent_id: "new-agent" });
  expect(container.querySelector(".channel-room-settings-name")?.textContent).toBe("General");
  expect(JSON.parse(localStorage.getItem("wuu.channels.roomPreferences")!).pinnedRoomIDs).toEqual(["new-dm"]);
  await manageSidebarRow("Research", t("channels.hideConversation"));
  expect(JSON.parse(localStorage.getItem("wuu.channels.roomPreferences")!)).toEqual({ pinnedRoomIDs: [], archivedRoomIDs: ["new-dm"] });
  expect(container.querySelector(".collaboration-sidebar nav")?.textContent).not.toContain("Research");
  await click(t("account.menu"));
  await click(t("sidebar.settings"));
  await act(async () => { await vi.dynamicImportSettled(); });
  await click(t("settings.archive"));
  await click(t("settings.restoreRoom", { title: "Research" }));
  expect(JSON.parse(localStorage.getItem("wuu.channels.roomPreferences")!).archivedRoomIDs).toEqual([]);
  await act(async () => { window.dispatchEvent(new Event("wuu:workbench-back")); });
  expect(container.querySelector(".collaboration-sidebar nav")?.textContent).toContain("Research");
});

it.each(["agent", "group"])("confirms context deletion and removes only the requested %s", async (target) => {
  agents = [{ id: "new-agent", name: "Research", avatar_key: "abstract-1", memory_dir: "/preview", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  rooms = [{ id: "general", name: "General", kind: "channel", members: [], created_by: "human", created_at: agents[0].created_at }];
  window.wuu.deleteNamedAgent = vi.fn(async () => { agents = []; return { deleted: true }; });
  window.wuu.deleteChannelRoom = vi.fn(async () => { rooms = []; return { deleted: true }; });
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  await act(async () => { root.render(<App />); });
  await click("collaboration");
  const name = target === "agent" ? "Research" : "General";
  const label = t(target === "agent" ? "channels.deleteAgent" : "channels.deleteRoom");
  await manageSidebarRow(name, label);
  expect(window.wuu.deleteNamedAgent).not.toHaveBeenCalled();
  expect(window.wuu.deleteChannelRoom).not.toHaveBeenCalled();
  confirm.mockReturnValue(true);
  await manageSidebarRow(name, label);
  if (target === "agent") {
    expect(window.wuu.deleteNamedAgent).toHaveBeenCalledExactlyOnceWith({ agent_id: "new-agent" });
    expect(window.wuu.deleteChannelRoom).not.toHaveBeenCalled();
  } else {
    expect(window.wuu.deleteChannelRoom).toHaveBeenCalledExactlyOnceWith({ room_id: "general" });
    expect(window.wuu.deleteNamedAgent).not.toHaveBeenCalled();
  }
  expect(container.querySelector(".collaboration-sidebar nav")?.textContent).not.toContain(name);
  confirm.mockRestore();
});

it("opens a newly created identity's conversation before the next directory refresh", async () => {
  await act(async () => { root.render(<App />); });
  await click("collaboration");
  await click(t("channels.newConversation"));
  expect(container.querySelector("#channel-recipient-create-agent")).toBeTruthy();
  await click(t("channels.newAgent"));
  expect(container.querySelector(".collaboration-contact-row.active .agent-avatar-mark")).toBeTruthy();
  expect(window.wuu.createNamedAgent).not.toHaveBeenCalled();
  await enterName("Research");
  await click(t("agentOnboarding.startChat"));
  expect(window.wuu.createNamedAgent).toHaveBeenCalledTimes(1);
  expect(window.wuu.openChannelDirectMessage).toHaveBeenCalledWith({ agent_id: "new-agent" });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(container.querySelector(".channel-room-header")?.textContent).toContain("Research");
  expect(container.querySelector(".collaboration-contact-row.active")?.textContent).toContain("Research");
});

it("creates independent conversations for consecutive agents with the default display name", async () => {
  vi.mocked(window.wuu.createNamedAgent).mockImplementation(async (params) => {
    const agent = { ...params, id: `agent-${agents.length + 1}`, memory_dir: "", autostart: true, created_at: "2026-09-12T00:00:00Z" } as NamedAgent;
    agents = [...agents, agent];
    return { agent };
  });
  vi.mocked(window.wuu.openChannelDirectMessage).mockImplementation(async ({ agent_id }) => {
    const room = { id: `dm-${agent_id}`, kind: "dm", name: agents.find((agent) => agent.id === agent_id)!.name,
      members: [{ member_type: "agent", member_id: agent_id }], created_at: "2026-09-12T00:00:00Z" } as ChannelRoom;
    rooms = [...rooms, room];
    return { room };
  });
  await act(async () => { root.render(<App />); });
  await click("collaboration");
  for (let index = 1; index <= 2; index += 1) {
    await click(t("channels.newConversation"));
    await act(async () => { container.querySelector<HTMLButtonElement>("#channel-recipient-create-agent")!.click(); });
    await click(t("agentOnboarding.startChat"));
    expect(window.wuu.openChannelDirectMessage).toHaveBeenLastCalledWith({ agent_id: `agent-${index}` });
  }
  expect(agents.map((agent) => agent.name)).toEqual([t("channels.newAgent"), t("channels.newAgent")]);
  expect(new Set(rooms.map((room) => room.id)).size).toBe(2);
  const requests = vi.mocked(window.wuu.createNamedAgent).mock.calls.map(([params]) => params.request_id);
  expect(new Set(requests).size).toBe(2);
});

it("preserves the agent draft across provider settings and returns to the model step", async () => {
  await act(async () => { root.render(<App />); });
  await click("collaboration");
  await click(t("channels.newConversation"));
  expect(container.querySelector("#channel-recipient-create-agent")).toBeTruthy();
  await click(t("channels.newAgent"));
  expect(container.querySelector(".collaboration-contact-row.active .agent-avatar-mark")).toBeTruthy();
  expect(window.wuu.createNamedAgent).not.toHaveBeenCalled();
  await enterName("Research");
  await click(t("agentOnboarding.manageProviders"));
  await act(async () => { await vi.dynamicImportSettled(); });
  expect(container.querySelector('[data-wuu-component="settings-shell"]')).toBeTruthy();
  expect(container.querySelector(".settings-provider-card")?.textContent).toContain("reasoner");
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  await act(async () => { window.dispatchEvent(new Event("wuu:workbench-back")); });
  expect(document.querySelector('[data-wuu-component="agent-onboarding"]')?.textContent).toContain("Research");
  await click(t("agentOnboarding.startChat"));
  expect(window.wuu.createNamedAgent).toHaveBeenCalledWith(expect.objectContaining({ name: "Research", provider_override: "byok", model_override: "reasoner" }));
});


it("opens an existing agent from the recipient picker before its new DM is in the directory", async () => {
  agents = [{ id: "existing-agent", name: "Ada", memory_dir: "", avatar_key: "abstract-1", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  vi.mocked(window.wuu.openChannelDirectMessage).mockImplementation(async () => {
    const room = { id: "ada-dm", kind: "dm", name: "Ada", members: [{ member_type: "agent", member_id: "existing-agent" }], created_at: "2026-09-12T00:00:00Z" } as ChannelRoom;
    rooms = [room];
    return { room };
  });
  await act(async () => { root.render(<App />); });
  await click("collaboration");
  await click(t("channels.newConversation"));
  await act(async () => { container.querySelector<HTMLButtonElement>("#channel-recipient-existing-agent")!.click(); });
  expect(window.wuu.openChannelDirectMessage).toHaveBeenCalledExactlyOnceWith({ agent_id: "existing-agent" });
  expect(container.querySelector(".channel-room-header")?.textContent).toContain("Ada");
  expect(container.querySelector(".collaboration-contact-row.active")?.textContent).toContain("Ada");
  expect(container.querySelector(".channel-recipient-picker")).toBeNull();
  expect(window.wuu.createNamedAgent).not.toHaveBeenCalled();
});

it("keeps the unfinished identity and avatar when navigating away and back", async () => {
  await act(async () => { root.render(<App />); });
  await click("collaboration");
  await click(t("channels.newConversation"));
  await click(t("channels.newAgent"));
  await enterName("Unfinished");
  const avatar = container.querySelector(".collaboration-contact-row.active .agent-avatar-mark")?.outerHTML;
  await click(t("channels.manageAgents"));
  expect(container.querySelector('[data-wuu-component="agent-onboarding"]')).toBeNull();
  await click("Unfinished");
  expect(container.querySelector<HTMLInputElement>('[name="agent-name"]')?.value).toBe("Unfinished");
  expect(container.querySelector(".collaboration-contact-row.active .agent-avatar-mark")?.outerHTML).toBe(avatar);
  expect(window.wuu.createNamedAgent).not.toHaveBeenCalled();
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
