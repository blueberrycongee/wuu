import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ChannelRoom, InitializeResult, NamedAgent, ServerEvent, Thread, WuuDesktopApi } from "../shared/protocol";
import { translateCurrent as t } from "./i18n";

vi.mock("@xterm/xterm", () => ({ Terminal: vi.fn() }));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: vi.fn() }));
vi.mock("./WorkspaceMonacoEditor", () => ({ WorkspaceMonacoEditor: () => null }));
import { App } from "./App";

let container: HTMLDivElement;
let root: Root;
let agents: NamedAgent[];
let rooms: ChannelRoom[];
let eventListeners: Set<(event: ServerEvent) => void>;
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

async function confirmModel(): Promise<void> {
  await click(t("agentOnboarding.useModel"));
}

async function enterName(value: string): Promise<void> {
  const input = document.querySelector<HTMLTextAreaElement>(".channel-composer textarea");
  expect(input).toBeTruthy();
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, value);
    input!.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function sendName(): Promise<void> {
  const send = document.querySelector<HTMLButtonElement>(".composer-send-button");
  expect(send).toBeTruthy();
  await act(async () => { send!.click(); });
}

async function startNewAgent(): Promise<void> {
  const create = document.querySelector<HTMLButtonElement>("#channel-recipient-create-agent");
  expect(create).toBeTruthy();
  await act(async () => { create!.click(); });
}

beforeEach(() => {
  eventListeners = new Set();
  agents = [];
  rooms = [];
  window.localStorage.clear();
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  Object.defineProperty(window, "matchMedia", { configurable: true, value: vi.fn((media: string) => ({
    matches: media.includes("prefers-reduced-motion"), media, onchange: null, addEventListener: vi.fn(), removeEventListener: vi.fn(),
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
    onServerEvent: vi.fn((listener) => { eventListeners.add(listener); return () => eventListeners.delete(listener); }), onWindowResizeState: vi.fn(() => () => {}), onTerminalEvent: vi.fn(() => () => {}),
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
    openChannelDirectMessage: vi.fn(async (params: Parameters<WuuDesktopApi["openChannelDirectMessage"]>[0]) => {
      const room = { id: "new-dm", name: "Research", kind: "dm", onboarding: params.onboarding, members: [{ member_type: "agent", member_id: "new-agent" }], created_at: "2026-09-12T00:00:00Z" } as ChannelRoom;
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
  const item = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')]
    .find(button => button.textContent?.trim() === action);
  expect(item).toBeTruthy();
  await act(async () => item!.click());
}

async function selectSidebarConversation(name: string): Promise<void> {
  const row = [...container.querySelectorAll<HTMLButtonElement>(".collaboration-contact-row")]
    .find(button => button.querySelector("strong")?.textContent === name);
  expect(row).toBeTruthy();
  await act(async () => row!.click());
}

it("opens managed sessions through their agent conversation without duplicating workspace or pinned entries", async () => {
  agents = [{ id: "manager", name: "Research", avatar_key: "abstract-1", memory_dir: "/preview", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  rooms = [{ id: "manager-dm", name: "Research", kind: "dm", created_by: "human", created_at: agents[0].created_at,
    members: [{ room_id: "manager-dm", member_type: "agent", member_id: "manager", joined_at: agents[0].created_at }] }];
  const thread = (id: string, control?: Thread["session_control"]): Thread => ({
    id, title: id, preview: id, cwd: workspace, workspace_kind: "scratch",
    model_provider: "byok", model: "reasoner", status: "idle", turns: [],
    created_at: "2026-09-12T00:00:00Z", updated_at: "2026-09-12T00:00:00Z", session_control: control,
  });
  const managed = ["active", "paused", "taken_over"].map(state => thread(`managed-${state}`, {
    manager_id: "manager", manager_name: "Research", state: state as "active" | "paused" | "taken_over", revision: 1,
  }));
  managed[0].pinned = true;
  const ordinary = thread("ordinary");
  const unknown = thread("unavailable-manager", { manager_id: "deleted-agent", manager_name: "Former agent", state: "active", revision: 1 });
  vi.mocked(window.wuu.listThreads).mockResolvedValue({ threads: [...managed, ordinary, unknown] });
  window.wuu.resumeThread = vi.fn().mockResolvedValue({ thread: managed[0] });
  await act(async () => { root.render(<App />); });
  const sidebar = container.querySelector('[data-wuu-component="app-sidebar"]') ?? container.querySelector(".sidebar");
  for (const item of managed) {
    const buttons = [...sidebar!.querySelectorAll<HTMLButtonElement>("button.thread-row-main")].filter(button => button.textContent?.includes(item.title!));
    expect(buttons).toHaveLength(0);
  }
  await selectSidebarConversation("Research");
  const showAll = container.querySelector<HTMLButtonElement>(".managed-agent-work-pill");
  expect(showAll).toBeTruthy();
  await act(async () => showAll!.click());
  const results = [...container.querySelectorAll<HTMLButtonElement>(".managed-session-result")];
  expect(results).toHaveLength(managed.length);
  for (const item of managed) expect(results.filter(button => button.textContent?.includes(item.title!))).toHaveLength(1);
  expect(results.some(button => button.textContent?.includes("ordinary") || button.textContent?.includes("unavailable-manager"))).toBe(false);
  const select = results.find(button => button.textContent?.includes("managed-active"))!;
  await act(async () => select.click());
  expect(window.wuu.resumeThread).toHaveBeenCalledWith("managed-active");
  await act(async () => { for (const onEvent of eventListeners) onEvent({ kind: "notification", workdir: workspace, message: {
    method: "thread/updated", params: { thread: { ...managed[0], session_control: undefined } },
  } }); });
  const released = [...sidebar!.querySelectorAll<HTMLButtonElement>("button.thread-row-main")].filter(button => button.textContent?.includes("managed-active"));
  expect(released).toHaveLength(1);
  await selectSidebarConversation("Research");
  if (!container.querySelector(".managed-session-result")) {
    await act(async () => container.querySelector<HTMLButtonElement>(".managed-agent-work-pill")!.click());
  }
  const remaining = [...container.querySelectorAll<HTMLButtonElement>(".managed-session-result")];
  expect(remaining).toHaveLength(managed.length - 1);
  expect(remaining.some(button => button.textContent?.includes("managed-active"))).toBe(false);
});

it("keeps newly created managed work reachable after a delayed workspace list", async () => {
  agents = [{ id: "manager", name: "Research", avatar_key: "abstract-1", memory_dir: "/preview", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  rooms = [{ id: "manager-dm", name: "Research", kind: "dm", created_by: "human", created_at: agents[0].created_at,
    members: [{ room_id: "manager-dm", member_type: "agent", member_id: "manager", joined_at: agents[0].created_at }] }];
  const target = {
    id: "beta", name: "Beta", path: "/tmp/wuu-managed-navigation-test",
    created_at: agents[0].created_at, updated_at: agents[0].created_at,
  };
  vi.mocked(window.wuu.listProjects).mockResolvedValue({
    projects: [target], active_context: { kind: "no_project", cwd: workspace },
  });
  let resolveList!: (result: { threads: Thread[] }) => void;
  vi.mocked(window.wuu.listThreads).mockImplementation(async (cwd) => {
    if (cwd === target.path) return new Promise(resolve => { resolveList = resolve; });
    return { threads: [] };
  });
  const created: Thread = {
    id: "new-managed-work", title: "Review navigation", preview: "Review navigation", source: "collaboration",
    cwd: target.path, workspace_id: target.id, workspace_kind: "project",
    model_provider: "byok", model: "reasoner", status: "idle", turns: [],
    created_at: agents[0].created_at, updated_at: agents[0].created_at,
  };
  const managed: Thread = { ...created,
    session_control: { manager_id: "manager", manager_name: "Research", state: "active", revision: 1 },
  };
  window.wuu.resumeThread = vi.fn().mockResolvedValue({ thread: managed });
  window.wuu.selectProject = vi.fn().mockResolvedValue({
    projects: [target], active_context: { kind: "project", project_id: target.id, cwd: target.path },
  });
  await act(async () => { root.render(<App />); });
  await click(t("threadSidebar.expandProject", { name: target.name, unread: "" }));
  expect(window.wuu.listThreads).toHaveBeenCalledWith(target.path);
  await selectSidebarConversation("Research");

  const workspaceRows = () => [...container.querySelectorAll<HTMLButtonElement>("button.thread-row-main")]
    .filter(button => button.textContent?.includes(created.title!));
  // Creation precedes the control update; only the latter moves the session into its manager's work.
  await act(async () => {
    for (const onEvent of eventListeners) onEvent({ kind: "notification", workdir: target.path, message: {
      method: "thread/started", params: { thread: created },
    } });
  });
  expect(workspaceRows()).toHaveLength(1);
  await act(async () => {
    for (const onEvent of eventListeners) onEvent({ kind: "notification", workdir: target.path, message: {
      method: "thread/updated", params: { thread: managed },
    } });
  });
  expect(workspaceRows()).toHaveLength(0);
  const showAll = container.querySelector<HTMLButtonElement>(".managed-agent-work-pill");
  expect(showAll).toBeTruthy();
  await act(async () => showAll!.click());
  const results = () => [...container.querySelectorAll<HTMLButtonElement>(".managed-session-result")]
    .filter(button => button.textContent?.includes(created.title!));
  expect(results()).toHaveLength(1);

  await act(async () => { resolveList({ threads: [] }); });
  expect(results()).toHaveLength(1);
  await act(async () => results()[0].click());
  expect(window.wuu.resumeThread).toHaveBeenCalledWith(created.id);
});

it("pins a new Agent without navigation, persists hiding, and restores its DM from settings", async () => {
  agents = [{ id: "new-agent", name: "Research", avatar_key: "abstract-1", memory_dir: "/preview", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  rooms = [{ id: "general", name: "General", kind: "channel", members: [], created_by: "human", created_at: agents[0].created_at }];
  vi.mocked(window.wuu.openChannelDirectMessage).mockImplementation(async () => {
    const room = { id: "new-dm", name: "Research", kind: "dm", members: [{ member_type: "agent", member_id: "new-agent" }], created_at: agents[0].created_at } as ChannelRoom;
    rooms = [...rooms, room];
    return { room };
  });
  await act(async () => { root.render(<App />); });
  await selectSidebarConversation("General");
  await manageSidebarRow("Research", t("sidebar.pin"));
  expect(window.wuu.openChannelDirectMessage).toHaveBeenCalledExactlyOnceWith({ agent_id: "new-agent" });
  expect(container.querySelector(".channel-room-settings-name")?.textContent).toBe("General");
  expect(JSON.parse(localStorage.getItem("wuu.channels.roomPreferences")!).pinnedRoomIDs).toEqual(["new-dm"]);
  expect(container.querySelector('[data-wuu-component="collaboration-sidebar"] nav')?.textContent).not.toContain("Research");
  expect(container.querySelector('[data-functional-group-id="pinned"]')?.textContent).toContain("Research");
  await manageSidebarRow("Research", t("channels.hideConversation"));
  expect(JSON.parse(localStorage.getItem("wuu.channels.roomPreferences")!)).toMatchObject({ pinnedRoomIDs: [], archivedRoomIDs: ["new-dm"] });
  expect(container.querySelector('[data-wuu-component="collaboration-sidebar"] nav')?.textContent).not.toContain("Research");
  expect(container.querySelector('[data-functional-group-id="pinned"]')?.textContent).not.toContain("Research");
  await click(t("account.menu"));
  await click(t("sidebar.settings"));
  await act(async () => { await vi.dynamicImportSettled(); });
  await click(t("settings.archive"));
  await click(t("settings.restoreRoom", { title: "Research" }));
  expect(JSON.parse(localStorage.getItem("wuu.channels.roomPreferences")!).archivedRoomIDs).toEqual([]);
  await act(async () => { window.dispatchEvent(new Event("wuu:workbench-back")); });
  expect(container.querySelector('[data-wuu-component="collaboration-sidebar"] nav')?.textContent).toContain("Research");
});

it.each(["agent", "group"])("confirms context deletion and removes only the requested %s", async (target) => {
  agents = [{ id: "new-agent", name: "Research", avatar_key: "abstract-1", memory_dir: "/preview", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  rooms = [{ id: "general", name: "General", kind: "channel", members: [], created_by: "human", created_at: agents[0].created_at }];
  let finishDelete!: () => void;
  window.wuu.deleteNamedAgent = vi.fn(() => new Promise<{ deleted: boolean }>((resolve) => {
    finishDelete = () => { agents = []; resolve({ deleted: true }); };
  }));
  window.wuu.deleteChannelRoom = vi.fn(async () => { rooms = []; return { deleted: true }; });
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  await act(async () => { root.render(<App />); });
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
    expect(container.querySelector('[data-wuu-component="collaboration-sidebar"] nav')?.textContent).not.toContain(name);
    await act(async () => finishDelete());
  } else {
    expect(window.wuu.deleteChannelRoom).toHaveBeenCalledExactlyOnceWith({ room_id: "general" });
    expect(window.wuu.deleteNamedAgent).not.toHaveBeenCalled();
  }
  expect(container.querySelector('[data-wuu-component="collaboration-sidebar"] nav')?.textContent).not.toContain(name);
  confirm.mockRestore();
});

it.each(["success", "cleanup-failed", "rejected"])("keeps managed work out of the workspace while reconciling agent deletion: %s", async (outcome) => {
  agents = [{ id: "manager", name: "Research", avatar_key: "abstract-1", memory_dir: "/preview", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  const oldAgents = agents;
  const managed: Thread = {
    id: "managed", title: "Managed history", preview: "", cwd: workspace, workspace_kind: "scratch",
    model_provider: "byok", model: "reasoner", status: "idle", turns: [], pinned: true,
    created_at: agents[0].created_at, updated_at: agents[0].created_at, latest_completed_turn_id: "unread-turn",
    session_control: { manager_id: "manager", manager_name: "Research", state: "paused", revision: 1 },
  };
  const takenOver: Thread = { ...managed, id: "taken-over", title: "My continued work", pinned: false,
    session_control: { manager_id: "manager", manager_name: "Research", state: "taken_over", revision: 2 } };
  vi.mocked(window.wuu.listThreads).mockResolvedValue({ threads: [managed, takenOver] });
  window.wuu.resumeThread = vi.fn(async (id) => ({ thread: id === takenOver.id ? takenOver : managed }));
  let finishDelete!: () => void;
  window.wuu.deleteNamedAgent = vi.fn(() => new Promise<{ deleted: boolean }>((resolve, reject) => {
    finishDelete = () => {
      if (outcome !== "rejected") agents = [];
      if (outcome === "success") resolve({ deleted: true });
      else reject(new Error(outcome));
    };
  }));
  vi.spyOn(window, "confirm").mockReturnValue(true);
  vi.useFakeTimers();
  try {
    await act(async () => root.render(<App />));
    await manageSidebarRow("Research", t("channels.deleteAgent"));
    const sidebar = container.querySelector('[data-wuu-component="app-sidebar"]') ?? container.querySelector(".sidebar");
    expect(sidebar?.textContent).not.toContain("Managed history");
    expect(sidebar?.textContent).toContain("My continued work");
    expect(container.querySelector('[data-wuu-component="collaboration-sidebar"] nav')?.textContent).not.toContain("Research");
    // A directory response admitted during deletion still describes the old identity.
    let finishPoll!: (result: { agents: NamedAgent[] }) => void;
    vi.mocked(window.wuu.listNamedAgents).mockImplementationOnce(() => new Promise(resolve => { finishPoll = resolve; }));
    await act(async () => document.dispatchEvent(new Event("visibilitychange")));
    expect(finishPoll).toBeTypeOf("function");
    await act(async () => finishDelete());
    await act(async () => finishPoll({ agents: oldAgents }));
    const contacts = container.querySelector('[data-wuu-component="collaboration-sidebar"] nav')?.textContent ?? "";
    expect(contacts.includes("Research")).toBe(outcome === "rejected");
    expect(sidebar?.textContent).not.toContain("Managed history");
  } finally {
    vi.useRealTimers();
  }
});

it("opens a newly created identity's conversation before the next directory refresh", async () => {
  await act(async () => { root.render(<App />); });
  await click(t("channels.newConversation"));
  expect(container.querySelector("#channel-recipient-create-agent")).toBeTruthy();
  await startNewAgent();
  expect(container.querySelector(".collaboration-contact-row.active .agent-avatar-mark")).toBeTruthy();
  expect(window.wuu.createNamedAgent).not.toHaveBeenCalled();
  await confirmModel();
  await enterName("Research");
  // Setup controls must not become transcript content that disappears on handoff.
  const modelMessage = container.querySelector('[data-message-id="model"]')!.textContent;
  const nameMessage = container.querySelector('[data-message-id="name"]')!.textContent;
  await sendName();
  expect(window.wuu.createNamedAgent).toHaveBeenCalledTimes(1);
  expect(window.wuu.openChannelDirectMessage).toHaveBeenCalledWith(expect.objectContaining({ agent_id: "new-agent" }));
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(container.querySelector(".channel-room-header")?.textContent).toContain("Research");
  expect(container.querySelector('[data-message-id="model"]')?.textContent).toBe(modelMessage);
  expect(container.querySelector('[data-message-id="name"]')?.textContent).toBe(nameMessage);
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
  for (let index = 1; index <= 2; index += 1) {
    await click(t("channels.newConversation"));
    await startNewAgent();
    await confirmModel();
    await enterName(t("channels.newAgent"));
    await sendName();
    expect(window.wuu.openChannelDirectMessage).toHaveBeenLastCalledWith(expect.objectContaining({ agent_id: `agent-${index}` }));
  }
  expect(agents.map((agent) => agent.name)).toEqual([t("channels.newAgent"), t("channels.newAgent")]);
  expect(new Set(rooms.map((room) => room.id)).size).toBe(2);
  const requests = vi.mocked(window.wuu.createNamedAgent).mock.calls.map(([params]) => params.request_id);
  expect(new Set(requests).size).toBe(2);
});

it("preserves the agent draft across provider settings and returns to the model step", async () => {
  await act(async () => { root.render(<App />); });
  await click(t("channels.newConversation"));
  expect(container.querySelector("#channel-recipient-create-agent")).toBeTruthy();
  await startNewAgent();
  expect(container.querySelector(".collaboration-contact-row.active .agent-avatar-mark")).toBeTruthy();
  expect(window.wuu.createNamedAgent).not.toHaveBeenCalled();
  await confirmModel();
  await enterName("Research");
  await click(t("slash.model.title"));
  await click(t("agentOnboarding.manageProviders"));
  await act(async () => { await vi.dynamicImportSettled(); });
  expect(container.querySelector('[data-wuu-component="settings-shell"]')).toBeTruthy();
  expect(container.querySelector(".settings-provider-card")?.textContent).toContain("reasoner");
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  await act(async () => { window.dispatchEvent(new Event("wuu:workbench-back")); });
  await confirmModel();
  expect(container.querySelector<HTMLTextAreaElement>(".channel-composer textarea")?.value).toBe("Research");
  await sendName();
  expect(window.wuu.createNamedAgent).toHaveBeenCalledWith(expect.objectContaining({ name: "Research", provider_override: "byok", model_override: "reasoner" }));
});


it("switches between direct conversations and Harness without replacing the sidebar", async () => {
  agents = [{ id: "ada", name: "Ada", memory_dir: "", avatar_key: "abstract-1", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  const group: ChannelRoom = { id: "general", kind: "channel", name: "General", members: [], created_by: "human", created_at: agents[0].created_at };
  const dm: ChannelRoom = { ...group, id: "ada-dm", kind: "dm", name: "Ada", members: [{ member_type: "agent", member_id: "ada", room_id: "ada-dm", joined_at: group.created_at }] };
  rooms = [group, dm];
  localStorage.setItem("wuu.channels.roomPreferences", JSON.stringify({ pinnedRoomIDs: [], archivedRoomIDs: [], selectedRoomID: dm.id }));
  await act(async () => root.render(<App />));
  const sidebar = container.querySelector('[data-wuu-component="sidebar"]');
  await selectSidebarConversation("Ada");
  expect(container.querySelector(".channel-room-header")?.textContent).toContain("Ada");
  await click(t("sidebar.newConversation"));
  expect(container.querySelector(".channel-room-header")).toBeNull();
  expect(container.querySelector('[data-wuu-component="sidebar"]')).toBe(sidebar);
  await selectSidebarConversation("Ada");
  expect(container.querySelector(".channel-room-header")?.textContent).toContain("Ada");
  await act(async () => root.unmount());
  rooms = [group];
  root = createRoot(container);
  await act(async () => root.render(<App />));
  await selectSidebarConversation("General");
  expect(container.querySelector(".channel-room-header")?.textContent).toContain("General");
});

it("hides the previous room while opening a DM and ignores a late DM selection", async () => {
  agents = [{ id: "ada", name: "Ada", memory_dir: "", avatar_key: "abstract-1", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  const group: ChannelRoom = { id: "general", kind: "channel", name: "General", members: [], created_by: "human", created_at: agents[0].created_at };
  rooms = [group];
  vi.mocked(window.wuu.listChannelMessages).mockImplementation(async ({ room_id }) => ({ messages: [{ id: "public", room_id, seq: 1, kind: "text", author_type: "human", author_id: "human", body: "Only in General", created_at: group.created_at }] }));
  let finish!: (result: { room: ChannelRoom }) => void;
  vi.mocked(window.wuu.openChannelDirectMessage).mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  await act(async () => root.render(<App />));
  await selectSidebarConversation("General");
  expect(container.querySelector('[role="log"]')?.textContent).toContain("Only in General");
  await click(t("channels.newConversation"));
  await act(async () => container.querySelector<HTMLButtonElement>("#channel-recipient-ada")!.click());
  expect(container.querySelector('[role="log"]')?.textContent ?? "").not.toContain("Only in General");
  const groupRow = [...container.querySelectorAll<HTMLButtonElement>(".collaboration-contact-row")].find(row => row.querySelector("strong")?.textContent === "General");
  expect(groupRow).toBeTruthy();
  await act(async () => groupRow!.click());
  await act(async () => finish({ room: { ...group, id: "ada-dm", name: "Ada", kind: "dm", members: [{ member_type: "agent", member_id: "ada", room_id: "ada-dm", joined_at: group.created_at }] } }));
  expect(container.querySelector(".channel-room-header")?.textContent).toContain("General");
});

it("does not let an older directory poll erase a newly opened DM", async () => {
  vi.useFakeTimers();
  try {
    agents = [{ id: "ada", name: "Ada", memory_dir: "", avatar_key: "abstract-1", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
    const group: ChannelRoom = { id: "general", kind: "channel", name: "General", members: [], created_by: "human", created_at: agents[0].created_at };
    rooms = [group];
    await act(async () => root.render(<App />));
    await selectSidebarConversation("General");
    let finishPoll!: (result: { rooms: ChannelRoom[] }) => void;
    vi.mocked(window.wuu.listChannelRooms).mockImplementationOnce(() => new Promise(resolve => { finishPoll = resolve; }));
    await act(async () => vi.advanceTimersByTime(2_000));
    const dm: ChannelRoom = { ...group, id: "ada-dm", name: "Ada", kind: "dm", members: [{ member_type: "agent", member_id: "ada", room_id: "ada-dm", joined_at: group.created_at }] };
    vi.mocked(window.wuu.openChannelDirectMessage).mockResolvedValue({ room: dm });
    await click(t("channels.newConversation"));
    await act(async () => container.querySelector<HTMLButtonElement>("#channel-recipient-ada")!.click());
    await act(async () => finishPoll({ rooms: [group] }));
    expect(container.querySelector(".channel-room-header")?.textContent).toContain("Ada");
    expect(JSON.parse(localStorage.getItem("wuu.channels.roomPreferences")!).selectedRoomID).toBe("ada-dm");
  } finally {
    vi.useRealTimers();
  }
});

it("opens an existing agent from the recipient picker before its new DM is in the directory", async () => {
  agents = [{ id: "existing-agent", name: "Ada", memory_dir: "", avatar_key: "abstract-1", autostart: true, created_at: "2026-09-12T00:00:00Z" }];
  vi.mocked(window.wuu.openChannelDirectMessage).mockImplementation(async () => {
    const room = { id: "ada-dm", kind: "dm", name: "Ada", members: [{ member_type: "agent", member_id: "existing-agent" }], created_at: "2026-09-12T00:00:00Z" } as ChannelRoom;
    rooms = [room];
    return { room };
  });
  await act(async () => { root.render(<App />); });
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
  await click(t("channels.newConversation"));
  await startNewAgent();
  await confirmModel();
  await enterName("Unfinished");
  const avatar = container.querySelector(".collaboration-contact-row.active .agent-avatar-mark")?.outerHTML;
  await click(t("channels.newConversation"));
  expect(container.querySelector('[data-wuu-component="agent-onboarding"]')).toBeNull();
  await click("Unfinished");
  expect(container.querySelector<HTMLTextAreaElement>(".channel-composer textarea")?.value).toBe("Unfinished");
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
  await click(t("channels.newConversation"));
  expect(container.querySelector("main.environment-panel-visible, main.environment-panel-reserved, main.side-thread-panel-visible")).toBeNull();
  await click(t("sidebar.newConversation"));
  expect(container.querySelector("main.environment-panel-visible")).toBeTruthy();
});
