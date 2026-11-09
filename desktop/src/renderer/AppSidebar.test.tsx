import { act, createRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { DesktopProject, InitializeResult } from "../shared/protocol";
import { AppSidebar } from "./AppSidebar";
import { DesktopControlsProvider } from "./DesktopControlsContext";
import type { ApplicationDesktopControlsV1 } from "../../../packages/plugin-sdk/src/ui-desktop";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { DesktopWorkbench, WorkbenchController } from "./plugins/Workbench";
import {
  initialState,
  type AppState,
  type ThreadSummary,
} from "./AppState";

let container: HTMLDivElement;
let root: Root | null = null;
let unreadViewOpen = false;
let attentionStickyIDs = new Set<string>();
const desktop: ApplicationDesktopControlsV1 = { hostKind: "desktop", popOut: { kind: null, threadID: null, context: null },
  capabilities: { account: false, pets: false, nativeControls: false, chromeOverlay: false, notifications: false },
  onVisibilityChange: () => () => {} };

beforeEach(() => {
  window.wuu = { ...window.wuu, reportPluginDesktopActivation: vi.fn().mockResolvedValue(undefined) };
  window.localStorage.removeItem("wuu.desktop.sidebarFunctionalGroupOrder");
  window.localStorage.removeItem("wuu.desktop.recentThreadArrivalOrder.v1");
  window.localStorage.removeItem("wuu.desktop.sidebarThreadOrderByWorkspace");
  window.localStorage.removeItem("wuu.desktop.sidebarRecentView.v1");
  unreadViewOpen = false;
  attentionStickyIDs = new Set();
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  window.localStorage.removeItem("wuu.desktop.sidebarFunctionalGroupOrder");
  window.localStorage.removeItem("wuu.desktop.recentThreadArrivalOrder.v1");
  window.localStorage.removeItem("wuu.desktop.sidebarThreadOrderByWorkspace");
  window.localStorage.removeItem("wuu.desktop.sidebarRecentView.v1");
  act(() => root?.unmount());
  desktopPluginHost.unload("test:app-sidebar-navigation");
  desktopPluginHost.unload("test:app-sidebar-view");
  desktopPluginHost.unload("test:app-sidebar-other");
  root = null;
  container.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function initialized(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "test",
    model: "test-model",
    workspace_root: "/repo",
  };
}

const sidebarWorkspaces: DesktopProject[] = [
  {
    id: "project-1",
    name: "wuu",
    path: "/repo/wuu",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "project-2",
    name: "interview",
    path: "/repo/interview",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
];

interface RenderOptions {
  onFocusWorkspace?: (id: string) => void;
  workbenchController?: WorkbenchController;
  expandedSidebarSectionIDs?: Set<string>;
  pinnedThreads?: ThreadSummary[];
  onTogglePinned?: (thread: ThreadSummary) => void;
  workspaceThreadsByWorkspaceID?: Record<string, ThreadSummary[]>;
  activeThreadID?: string;
  pendingThreadID?: string;
  onSelectThread?: (threadID: string) => void;
  onSelectWorkspaceThread?: (workspaceID: string, threadID: string) => void;
  sectionOrder?: string[];
  state?: AppState;
  collapsedSidebarSectionIDs?: Set<string>;
  onStartNewThread?: () => void;
}

// The bell view is driven by App-owned state (it has to survive faces that
// replace the whole workbench, such as settings), so the harness owns the flag
// here and hands AppSidebar the same controlled props it gets in production.
function SidebarHarness({ options }: { options: RenderOptions }): JSX.Element {
  const [collapsedFolderIDs, setCollapsedFolderIDs] = useState<Set<string>>(() => new Set());
  const {
    expandedSidebarSectionIDs = new Set(),
    workspaceThreadsByWorkspaceID = {},
    activeThreadID,
    pendingThreadID,
    onSelectThread = () => {},
    onSelectWorkspaceThread = () => {},
    sectionOrder = ["project-1", "project-2"],
    state = {
      ...initialState,
      initialized: initialized(),
      activeContext: {
        kind: "project",
        project_id: "project-1",
        cwd: "/repo/wuu",
      },
    },
    collapsedSidebarSectionIDs = new Set(),
    pinnedThreads = [],
    onTogglePinned = () => {},
    onStartNewThread = () => {},
  } = options;
  return (
    <DesktopControlsProvider controls={desktop}><AppSidebar
      workbenchController={options.workbenchController}
      initialized={Boolean(state.initialized)}
      activeContextKind={state.activeContext?.kind === "user" ? undefined : state.activeContext?.kind}
      activeSessionTabID={state.activeSessionTabID}
      selectedWorkspaceID={state.activeProjectId}
      onFocusWorkspace={options.onFocusWorkspace}
      extensionInventory={state.initialized?.extension_inventory}
      lastViewedTurnByThreadID={state.lastViewedTurnByThreadID}
      catalogActive={state.sessionTabs.some(tab => tab.id === state.activeSessionTabID && tab.kind === "skills")}
      sidebarWorkspaces={sidebarWorkspaces}
      pinnedThreads={pinnedThreads}
      activeThreadID={activeThreadID}
      pendingThreadID={pendingThreadID}
      pendingWorkspaceID={undefined}
      collapsedSidebarSectionIDs={collapsedSidebarSectionIDs}
      collapsedFolderIDs={collapsedFolderIDs}
      setCollapsedFolderIDs={setCollapsedFolderIDs}
      expandedSidebarSectionIDs={expandedSidebarSectionIDs}
      workspaceThreadsByWorkspaceID={workspaceThreadsByWorkspaceID}
      workspaceMenuOpen={false}
      workspaceMenuRef={createRef<HTMLDivElement>()}
      searchOpen={false}
      sectionOrder={sectionOrder}
      onStartNewThread={onStartNewThread}
      onOpenSkillsTab={() => {}}
      onMarkThreadsViewed={() => {}}
      unreadViewOpen={unreadViewOpen}
      onToggleUnreadView={() => {
        unreadViewOpen = !unreadViewOpen;
        renderSidebar(options);
      }}
      attentionStickyIDs={attentionStickyIDs}
      onAttentionStickyIDsChange={(ids) => {
        if (ids === attentionStickyIDs) return;
        attentionStickyIDs = ids;
        renderSidebar(options);
      }}
      onToggleConversationSearch={() => {}}
      onSelectThread={onSelectThread}
      onTogglePinned={onTogglePinned}
      onArchiveThread={() => {}}
      onDeleteThread={() => {}}
      onRenameThread={() => {}}
      onToggleWorkspaceMenu={() => {}}
      onCreateWorkspace={() => {}}
      onOpenWorkspaceFolder={() => {}}
      onToggleSidebarSectionCollapsed={() => {}}
      onLoadWorkspaceThreads={async () => {}}
      onStartNewThreadInWorkspace={() => {}}
      onSelectWorkspaceThread={onSelectWorkspaceThread}
      onRemoveWorkspace={() => {}}
      onRelocateWorkspace={() => {}}
      onOpenSettings={() => {}}
    /></DesktopControlsProvider>
  );
}

function renderSidebar(options: RenderOptions = {}): void {
  act(() => {
    root ??= createRoot(container);
    root.render(<SidebarHarness options={options} />);
  });
}

describe("AppSidebar layout", () => {
  it("composes contextual sidebar content only for the active owner and removes it on unload", async () => {
    const contexts: Readonly<Record<string, unknown>>[] = [];
    await desktopPluginHost.activateGeneration({ graphGeneration: "renderer-test-graph", pluginId: "test:app-sidebar-navigation", manifestId: "wuu.default-navigation", generation: "one", register(api) {
      api.registerUISlot({ id: "sidebar.primary", cardinality: "many" });
      api.registerUISlot({ id: "sidebar.footer", cardinality: "many" });
    } });
    await desktopPluginHost.activateGeneration({ graphGeneration: "renderer-test-graph", pluginId: "test:app-sidebar-view", generation: "one", register(api) {
      api.registerViewType({ id: "context.view", title: "Context view", render: () => null });
      api.registerUIContribution({ id: "context-sidebar", target: "wuu.default-navigation/sidebar.primary", render: ({ context }) => {
        contexts.push(context);
        return <div data-owner-sidebar>{context.viewTypeId ? "contextual" : "general"}</div>;
      } });
      api.registerUIContribution({ id: "footer", target: "wuu.default-navigation/sidebar.footer", render: ({ context }) => <div data-sidebar-footer>{String(context.initialized)}</div> });
    } });
    await desktopPluginHost.activateGeneration({ graphGeneration: "renderer-test-graph", pluginId: "test:app-sidebar-other", generation: "one", register(api) {
      api.registerUIContribution({ id: "other-sidebar", target: "wuu.default-navigation/sidebar.primary", render: ({ context }) => <div data-other-sidebar>{context.viewTypeId ? "leaked" : "general"}</div> });
    } });
    const controller = new WorkbenchController(desktopPluginHost, {}, { getItem: () => null, setItem: () => {}, removeItem: () => {} });
    try {
      renderSidebar({ workbenchController: controller });
      expect(container.querySelector("[data-owner-sidebar]")?.textContent).toBe("general");
      expect(container.querySelector("[data-sidebar-footer]")?.textContent).toBe("true");
      expect(contexts.at(-1)).toMatchObject({ initialized: true, hasActiveThread: false, workspaceCount: 2 });
      let viewID!: string;
      await act(async () => { viewID = await controller.openPluginView("test:app-sidebar-view", "context.view", { region: "primary" }); });
      const contextual = container.querySelector(".sidebar-section-scroll [data-owner-sidebar]");
      expect(contextual?.textContent).toBe("contextual");
      expect(container.querySelector(".sidebar-section-scroll [data-other-sidebar]")).toBeNull();
      const context = contexts.find(item => item.viewTypeId === "context.view");
      expect(context?.viewContext).toBe(controller.getSnapshot().views.find(view => view.id === viewID)?.context);
      act(() => desktopPluginHost.unload("test:app-sidebar-view"));
      expect(container.querySelector("[data-owner-sidebar]")).toBeNull();
      expect(container.querySelector("[data-sidebar-footer]")).toBeNull();
    } finally { controller.dispose(); }
  });

  it("opens a recent conversation across workspaces and removes archived or pinned entries", () => {
    const thread = (id: string, day: number, extra: Partial<ThreadSummary> = {}): ThreadSummary => ({
      id, title: id, cwd: "/repo/wuu", status: "idle", source: "desktop", pinned: false, archived: false,
      created_at: "2026-09-01T00:00:00Z", updated_at: `2026-09-${day}T00:00:00Z`,
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0, ...extra,
    });
    const older = thread("older", 11);
    const newer = thread("newer", 12);
    const select = vi.fn();
    const startNew = vi.fn();
    const options: RenderOptions = {
      onSelectThread: select,
      onStartNewThread: startNew,
      activeThreadID: newer.id,
      workspaceThreadsByWorkspaceID: {
        "project-1": [older, thread("archived", 14, { archived: true })],
        "project-2": [newer, older, thread("pinned", 15, { pinned: true })],
      },
    };
    renderSidebar(options);
    const recent = () => container.querySelector('[data-sidebar-recents]')!;
    const titles = () => [...recent().querySelectorAll('.thread-row-title')].map(row => row.textContent);
    expect(titles()).toEqual(["newer", "older"]);
    expect(recent().querySelector('.thread-row.active')?.textContent).toContain("newer");
    act(() => recent().querySelector<HTMLButtonElement>('.thread-row-main')!.click());
    expect(select).toHaveBeenCalledWith(newer.id);
    const startConversation = recent().querySelector<HTMLButtonElement>('.sidebar-recents-new-thread')!;
    expect(startConversation).not.toBeNull();
    act(() => startConversation.click());
    expect(startNew).toHaveBeenCalledTimes(1);
    renderSidebar({ ...options, workspaceThreadsByWorkspaceID: {
      "project-1": [older], "project-2": [{ ...newer, archived: true }],
    } });
    expect(titles()).toEqual(["older"]);
  });

  it.each(["reload", "late catalog"])("keeps newly created Recent conversations reachable after %s with more than 50 older rows", (refresh) => {
    window.localStorage.removeItem("wuu.desktop.sidebarThreadOrderByWorkspace");
  window.localStorage.removeItem("wuu.desktop.sidebarRecentView.v1");
    const old = Array.from({ length: 60 }, (_, index): ThreadSummary => ({
      id: `old-${index}`, title: `Older conversation ${index}`, cwd: "/repo/wuu", workspace_id: "project-1",
      status: "in_progress", created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-01T00:00:00Z",
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0,
    }));
    const fresh: ThreadSummary = { ...old[0], id: "new", title: "Newly created conversation", status: "idle",
      created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" };
    const render = (threads: ThreadSummary[]) => renderSidebar({ activeThreadID: fresh.id,
      workspaceThreadsByWorkspaceID: { "project-1": threads } });
    const visible = () => [...container.querySelectorAll('[data-sidebar-recents] .thread-row-title')].map(row => row.textContent);
    if (refresh === "reload") render(old);
    render(refresh === "reload" ? [...old, fresh] : [fresh]);
    expect(visible()).toContain(fresh.title);
    if (refresh === "reload") {
      act(() => root?.unmount()); root = null;
      render([]);
    }
    render([...old, fresh]);
    expect(visible()).toContain(fresh.title);
  });

  it("migrates manual Recent order and does not rewrite it for unchanged activity", () => {
    const rows = ["first", "second"].map((id): ThreadSummary => ({ id, title: id, cwd: "/repo/wuu", workspace_id: "project-1",
      status: "idle", created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-01T00:00:00Z",
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0 }));
    window.localStorage.setItem("wuu.desktop.sidebarThreadOrderByWorkspace", JSON.stringify({ __wuu_recent_threads__: ["second", "first"] }));
    const writes = vi.spyOn(Storage.prototype, "setItem");
    const render = (threads: ThreadSummary[]) => renderSidebar({ workspaceThreadsByWorkspaceID: { "project-1": threads } });
    const titles = () => [...container.querySelectorAll('[data-sidebar-recents] .thread-row-title')].map(row => row.textContent);
    try {
      render(rows);
      expect(titles()).toEqual(["second", "first"]);
      const saved = window.localStorage.getItem("wuu.desktop.recentThreadArrivalOrder.v1");
      writes.mockClear();
      render(rows.map(row => ({ ...row, updated_at: "2026-09-01T00:00:00Z" })));
      expect(writes.mock.calls.filter(([key]) => key === "wuu.desktop.recentThreadArrivalOrder.v1")).toHaveLength(0);
      act(() => root?.unmount()); root = null;
      render([]);
      expect(window.localStorage.getItem("wuu.desktop.recentThreadArrivalOrder.v1")).toBe(saved);
      render([rows[0], { ...rows[1], archived: true }]);
      expect(titles()).toEqual(["first"]);
    } finally { writes.mockRestore(); }
  });

  it.each(["pointer", "keyboard"])("keeps newly visited conversations in Recent across repeated navigation during a %s hold", (interaction) => {
    const thread = (id: string, day: number): ThreadSummary => ({
      id, title: id, cwd: "/repo/wuu", workspace_id: "project-1", status: "idle",
      created_at: `2026-09-${day}T00:00:00Z`, updated_at: `2026-09-${day}T00:00:00Z`,
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0,
    });
    const older = thread("older", 11);
    const first = thread("first new conversation", 12);
    const second = thread("second new conversation", 13);
    const background = thread("background arrival", 14);
    const render = (threads: ThreadSummary[], activeThreadID?: string, pendingThreadID?: string) => renderSidebar({
      activeThreadID, pendingThreadID, expandedSidebarSectionIDs: new Set(["project-1"]),
      workspaceThreadsByWorkspaceID: { "project-1": threads },
    });
    const recent = () => container.querySelector('[data-sidebar-recents]')!;
    const titles = () => [...recent().querySelectorAll('.thread-row-title')].map(row => row.textContent);
    render([older], older.id);
    if (interaction === "pointer") {
      const enter = new MouseEvent("pointerover", { bubbles: true });
      Object.defineProperty(enter, "pointerType", { value: "mouse" });
      act(() => recent().dispatchEvent(enter));
    } else {
      const row = recent().querySelector<HTMLButtonElement>(".thread-row-main")!;
      act(() => { row.focus(); row.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })); });
    }
    render([older, first], first.id);
    expect(titles()).toEqual([first.title, older.title]);
    render([older, first, second, background], first.id, second.id);
    expect(titles()).toEqual([second.title, first.title, older.title]);
    render([older, first, second, background], second.id);
    expect(titles()).toEqual([second.title, first.title, older.title]);
    // Starting another draft or opening a workspace must not hide visited rows.
    render([older, first, second, background]);
    expect(titles()).toEqual([second.title, first.title, older.title]);
    const renamed = { ...first, title: "Renamed conversation", updated_at: "2026-09-20T00:00:00Z" };
    render([background, second, renamed, older]);
    // Accessible names update immediately while the visible title animates.
    expect([...recent().querySelectorAll(".thread-row-main")].map(row => row.getAttribute("aria-label")))
      .toEqual([second.title, renamed.title, older.title].map(title => expect.stringContaining(title!)));
    // Deleting an admitted row must win over the retained interaction snapshot.
    render([older, renamed, background]);
    expect(recent().textContent).not.toContain(second.title);
    render([background, renamed, older]);
    expect(recent().textContent).not.toContain(second.title);
    // Authoritative removal still wins, including for rows added during the hold.
    render([older, { ...renamed, archived: true }, { ...second, pinned: true }, background]);
    expect(titles()).toEqual([older.title]);
    if (interaction === "pointer") {
      act(() => recent().dispatchEvent(new MouseEvent("pointerout", { bubbles: true, relatedTarget: document.body })));
    } else {
      act(() => (document.activeElement as HTMLElement).blur());
    }
    expect(titles()).toEqual([background.title, older.title]);
    render([background]);
    expect(titles()).toEqual([background.title]);
  });

  it("selects only the clicked occurrence when a project and Recent show the same conversation", () => {
    const thread = { id: "shared", title: "Shared conversation", cwd: "/repo/wuu", workspace_id: "project-1",
      status: "idle", preview: "", model_provider: "test", model: "test", turn_count: 0, turns: [], created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" } as ThreadSummary;
    const options: RenderOptions = { activeThreadID: thread.id, expandedSidebarSectionIDs: new Set(["project-1"]),
      workspaceThreadsByWorkspaceID: { "project-1": [thread] }, state: { ...initialState, initialized: initialized(),
        activeProjectId: "project-1", activeContext: { kind: "project", project_id: "project-1", cwd: "/repo/wuu" } } };
    renderSidebar(options);
    const selected = () => container.querySelectorAll('.sidebar-main [aria-current="page"]');
    expect(selected()).toHaveLength(1);
    expect(selected()[0].closest('[data-sidebar-recents]')).not.toBeNull();
    act(() => container.querySelector<HTMLButtonElement>('.project-thread-list .thread-row-main')!.click());
    expect(selected()).toHaveLength(1);
    expect(selected()[0].closest('.project-thread-list')).not.toBeNull();
    act(() => container.querySelector<HTMLButtonElement>('[data-sidebar-recents] .thread-row-main')!.click());
    expect(selected()).toHaveLength(1);
    expect(selected()[0].closest('[data-sidebar-recents]')).not.toBeNull();
    renderSidebar({ ...options, onFocusWorkspace: () => {} });
    expect(selected()).toHaveLength(1);
    expect(selected()[0].classList.contains('project-row')).toBe(true);
  });

  it("sorts and filters Recent without losing its remembered order", () => {
    const thread = (id: string, workspace: string, created: string, updated: string): ThreadSummary => ({
      id, title: id, cwd: `/repo/${workspace === "project-1" ? "wuu" : "interview"}`, workspace_id: workspace,
      status: "idle", created_at: `2026-08-0${created}T00:00:00Z`, updated_at: `2026-09-0${updated}T00:00:00Z`,
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0,
    });
    const rows = { "project-1": [thread("alpha", "project-1", "1", "3"), thread("gamma", "project-1", "3", "2")],
      "project-2": [thread("beta", "project-2", "2", "1")] };
    window.localStorage.setItem("wuu.desktop.sidebarThreadOrderByWorkspace", JSON.stringify({ __wuu_recent_threads__: ["beta", "alpha", "gamma"] }));
    const render = () => renderSidebar({ workspaceThreadsByWorkspaceID: rows });
    const titles = () => [...container.querySelectorAll('[data-sidebar-recents] .thread-row-title')].map(row => row.textContent);
    const trigger = () => container.querySelector<HTMLButtonElement>(".sidebar-recents-view-trigger")!;
    const choose = (submenu: string, value: string) => {
      if (trigger().getAttribute("aria-expanded") !== "true") act(() => trigger().click());
      act(() => document.querySelector<HTMLButtonElement>(`[data-submenu="${submenu}"]`)!.click());
      act(() => document.querySelector<HTMLButtonElement>(`.sidebar-view-submenu [role="menuitemradio"][data-value="${value}"]`)!.click());
    };
    render();
    expect(titles()).toEqual(["beta", "alpha", "gamma"]);

    choose("sort", "created");
    expect(titles()).toEqual(["gamma", "beta", "alpha"]);
    choose("workspace", "project-1");
    choose("sort", "manual");
    expect(titles()).toEqual(["alpha", "gamma"]);

    act(() => root?.unmount()); root = null;
    render();
    expect(titles()).toEqual(["alpha", "gamma"]);
    expect(trigger().dataset.filtered).toBeDefined();
    act(() => trigger().click());
    act(() => document.querySelector<HTMLButtonElement>(".sidebar-view-menu-reset")!.click());
    expect(titles()).toEqual(["beta", "alpha", "gamma"]);
    expect(trigger().dataset.filtered).toBeUndefined();
  });

  it("orders running and unread conversations at the top of Recent", () => {
    const thread = (id: string, title: string, overrides: Partial<ThreadSummary> = {}): ThreadSummary => ({
      id, title, cwd: "/repo/wuu", workspace_id: "project-1", status: "idle",
      pinned: false, archived: false,
      created_at: "2026-09-15T00:00:00Z", updated_at: "2026-09-15T00:00:00Z",
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 1,
      latest_completed_turn_id: "turn-1", ...overrides,
    });
    const running = thread("running-session", "Running reply", {
      status: "in_progress", updated_at: "2026-09-15T00:00:00Z", turn_count: 0,
      latest_completed_turn_id: undefined,
    });
    const unread = thread("unread-session", "Unread reply", {
      updated_at: "2026-09-16T00:00:00Z",
    });
    const settled = thread("settled-session", "Settled reply", {
      updated_at: "2026-09-17T00:00:00Z",
    });
    renderSidebar({
      workspaceThreadsByWorkspaceID: { "project-1": [settled, unread, running] },
      state: {
        ...initialState,
        initialized: initialized(),
        activeContext: { kind: "project", project_id: "project-1", cwd: "/repo/wuu" },
        lastViewedTurnByThreadID: { [settled.id]: "turn-1" },
      },
    });

    expect(container.querySelector('[data-conversation-section="running"]')).toBeNull();
    expect(container.querySelector('[data-conversation-section="unread"]')).toBeNull();
    const recent = container.querySelector<HTMLElement>("[data-sidebar-recents]");
    const titles = [...(recent?.querySelectorAll(".thread-row-title") ?? [])]
      .map((title) => title.textContent);
    expect(titles).toEqual([running.title, unread.title, settled.title]);
    expect(recent?.querySelectorAll(".thread-row.has-unread")).toHaveLength(1);
  });

  it.each([
    { saved: ["workspace", "pinned"], expected: ["folders", "workspace", "pinned"] },
    // Retired groups are ignored without resetting remaining preferences.
    { saved: ["folders", "collaboration", "workspace", "pinned"], expected: ["folders", "workspace", "pinned"] },
  ])("restores group order without resetting existing preferences: $saved", async ({ saved, expected }) => {
    // Empty groups have no heading; populate both optional groups to inspect their saved order.
    vi.stubGlobal("wuu", {
      ...window.wuu,
      getSessionOrganization: async () => ({
        organization: { folders: [{ id: "folder-1", name: "Design" }] },
      }),
    });
    window.localStorage.setItem("wuu.desktop.sidebarFunctionalGroupOrder", JSON.stringify(saved));
    const order = () => [...container.querySelectorAll<HTMLElement>(".sidebar-main > .sidebar-functional-group")]
      .map((element) => element.dataset.functionalGroupId ?? element.dataset.sectionId);
    // Pinned shows only once it holds something, so give it a session.
    const pinned: ThreadSummary = {
      id: "pinned-1", title: "Pinned session", cwd: "/repo/wuu", workspace_id: "project-1",
      status: "idle", pinned: true, archived: false,
      created_at: "2026-09-17T00:00:00Z", updated_at: "2026-09-17T00:00:00Z",
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0,
    };
    await act(async () => {
      renderSidebar({ pinnedThreads: [pinned] });
    });
    expect(order()).toEqual(expected);
  });

  it("keeps the current session in sync between the workspace and bell views", () => {
    const threads: ThreadSummary[] = ["First session", "Second session"].map((title, index) => ({
      id: `running-${index}`, title, cwd: "/repo/wuu", workspace_id: "project-1",
      status: "in_progress", pinned: false, archived: false,
      created_at: "2026-09-17T00:00:00Z", updated_at: "2026-09-17T00:00:00Z",
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0,
    }));
    const onSelectThread = vi.fn();
    const options: RenderOptions = {
      activeThreadID: threads[0].id,
      expandedSidebarSectionIDs: new Set(["project-1"]),
      workspaceThreadsByWorkspaceID: { "project-1": threads },
      onSelectThread,
    };
    const currentTitles = () => [...container.querySelectorAll('[aria-current="page"] .thread-row-title')]
      .map((element) => element.textContent);
    renderSidebar(options);
    expect(currentTitles()).toEqual([threads[0].title]);

    const bell = container.querySelector<HTMLButtonElement>(".sidebar-notifications-button")!;
    act(() => bell.click());
    expect(currentTitles()).toEqual([threads[0].title]);

    const target = container.querySelector<HTMLButtonElement>(`button[aria-label="${threads[1].title}"]`)!;
    act(() => target.click());
    expect(onSelectThread).toHaveBeenCalledWith(threads[1].id);
    renderSidebar({ ...options, pendingThreadID: threads[1].id });
    expect(currentTitles()).toEqual([threads[0].title]);

    renderSidebar({ ...options, activeThreadID: threads[1].id });
    expect(currentTitles()).toEqual([threads[1].title]);
    expect(target.getAttribute("aria-busy")).toBe("true");
    act(() => bell.click());
    expect(currentTitles()).toEqual([threads[1].title]);
  });

  it("keeps an opened unread session in the attention view after it is marked read", () => {
    const unread: ThreadSummary = {
      id: "unread-session",
      title: "Unread session",
      cwd: "/repo/wuu",
      workspace_id: "project-1",
      status: "idle",
      pinned: false,
      archived: false,
      created_at: "2026-09-17T00:00:00Z",
      updated_at: "2026-09-17T00:00:00Z",
      preview: "",
      model_provider: "test",
      model: "test",
      turns: [{ id: "turn-1", status: "completed" }],
      turn_count: 1,
    };
    const idle: ThreadSummary = {
      ...unread,
      id: "idle-session",
      title: "Idle session",
      status: "idle",
      updated_at: "2026-09-18T00:00:00Z",
      turns: [],
      turn_count: 0,
    };
    const options: RenderOptions = {
      expandedSidebarSectionIDs: new Set(["project-1"]),
      workspaceThreadsByWorkspaceID: { "project-1": [unread, idle] },
    };
    renderSidebar(options);
    act(() => container.querySelector<HTMLButtonElement>(".sidebar-notifications-button")!.click());
    expect(container.querySelector('[aria-labelledby="sidebar-unread-heading"] .sidebar-unread-list')?.textContent).toContain(unread.title);
    expect(container.querySelector("#sidebar-recent-heading")).toBeNull();

    renderSidebar({
      ...options,
      activeThreadID: unread.id,
      state: {
        ...initialState,
        initialized: initialized(),
        activeContext: {
          kind: "project",
          project_id: "project-1",
          cwd: "/repo/wuu",
        },
        lastViewedTurnByThreadID: { [unread.id]: "turn-1" },
      },
    });
    expect(container.querySelector("#sidebar-unread-heading")).toBeNull();
    expect(container.querySelector('[aria-labelledby="sidebar-recent-heading"] .sidebar-unread-list')?.textContent).toContain(unread.title);
    expect(container.querySelector('[aria-current="page"] .thread-row-title')?.textContent).toBe(unread.title);
    expect(container.querySelector(".sidebar-unread-view")?.textContent).not.toContain(idle.title);
    expect(container.querySelector<HTMLElement>(".sidebar-notifications-button")?.dataset.hasUnread).toBeUndefined();
  });

  // The failure cases these guard: a project or its sessions also crowd the
  // workspace list, a missing project's sessions become inaccessible, and the
  // Projects entries do nothing.
  function sidebarThread(id: string, title: string, overrides: Partial<ThreadSummary> = {}): ThreadSummary {
    return {
      id, title, cwd: "/repo/wuu", workspace_id: "project-1", status: "idle", pinned: false, archived: false,
      created_at: "2026-09-17T00:00:00Z", updated_at: "2026-09-17T00:00:00Z",
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0, ...overrides,
    };
  }

  it("offers the empty Pinned group as a drop target only while a conversation is dragged", () => {
    const onTogglePinned = vi.fn();
    renderSidebar({
      expandedSidebarSectionIDs: new Set(["project-1"]),
      workspaceThreadsByWorkspaceID: { "project-1": [sidebarThread("chat", "Ordinary conversation")] },
      onTogglePinned,
    });
    const pinnedGroup = () => container.querySelector<HTMLElement>('[data-functional-group-id="pinned"]');
    expect(pinnedGroup()).toBeNull();

    const conversation = [...container.querySelectorAll<HTMLElement>(".thread-row")]
      .find((row) => row.textContent?.includes("Ordinary conversation"))!;
    const data = new Map<string, string>();
    const transfer = {
      get types() { return [...data.keys()]; },
      setData: (type: string, value: string) => { data.set(type, value); },
      getData: (type: string) => data.get(type) ?? "",
      setDragImage: () => {},
      effectAllowed: "",
      dropEffect: "",
    };
    const drag = (target: HTMLElement, type: string) => {
      const event = new Event(type, { bubbles: true, cancelable: true });
      Object.defineProperty(event, "dataTransfer", { value: transfer });
      act(() => { target.dispatchEvent(event); });
    };
    drag(conversation, "dragstart");
    const heading = pinnedGroup()?.querySelector<HTMLElement>(".sidebar-functional-heading");
    expect(heading).toBeTruthy();
    drag(heading!, "dragover");
    drag(heading!, "drop");
    drag(conversation, "dragend");
    expect(onTogglePinned).toHaveBeenCalledWith(expect.objectContaining({ id: "chat" }));
  });


  it("keeps retired project history accessible as ordinary conversations", () => {
    renderSidebar({});
    expect(container.querySelector('[data-functional-group-id="projects"]')).toBeNull();
    renderSidebar({
      workspaceThreadsByWorkspaceID: {
        "project-1": [sidebarThread("old-project", "Existing project", { source: "project", read_only: true })],
      },
    });
    expect(container.querySelector(".sidebar-recent-group")?.textContent ?? container.textContent).toContain("Existing project");
  });

  it("renders projects in the workspace order", () => {
    renderSidebar({
      sectionOrder: ["project-2", "project-1"],
    });

    const sections = Array.from(
      container.querySelectorAll<HTMLElement>(
        ".sidebar-functional-group-body > section[data-section-id]",
      ),
    );
    expect(sections.map((section) => section.dataset.sectionId)).toEqual([
      "project-2",
      "project-1",
    ]);
  });

  it("keeps workspace rows mounted through the shared collapse motion", () => {
    vi.useFakeTimers();
    renderSidebar();

    const workspaceToggle = container.querySelector<HTMLButtonElement>(
      '[aria-label="收起工作区"]',
    );
    expect(workspaceToggle).not.toBeNull();
    expect(
      container.querySelector('[data-functional-group-id="workspace"] .thread-list-collapse'),
    ).not.toBeNull();

    act(() => {
      workspaceToggle?.click();
    });

    const collapsingBody = container.querySelector(
      '[data-functional-group-id="workspace"] .thread-list-collapse',
    );
    expect(collapsingBody?.getAttribute("data-state")).toBe("closing");
    expect(container.querySelector(".sidebar-functional-group-body > section[data-section-id]")).not.toBeNull();

    act(() => {
      vi.runAllTimers();
    });

    expect(container.querySelector(".sidebar-functional-group-body > section[data-section-id]")).toBeNull();
    expect(container.querySelector('[aria-label="展开工作区"]')).not.toBeNull();
  });
});
