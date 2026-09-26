import { act, createRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { DesktopProject, InitializeResult } from "../shared/protocol";
import { AppSidebar } from "./AppSidebar";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import type { NavigationSnapshotV1 } from "../shared/workbench";
import {
  initialState,
  SCRATCH_PSEUDO_PROJECT_ID,
  type AppState,
  type ThreadSummary,
} from "./AppState";

let container: HTMLDivElement;
let root: Root | null = null;
let unreadViewOpen = false;
let attentionStickyIDs = new Set<string>();

beforeEach(() => {
  window.localStorage.removeItem("wuu.desktop.sidebarFunctionalGroupOrder");
  unreadViewOpen = false;
  attentionStickyIDs = new Set();
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  window.localStorage.removeItem("wuu.desktop.sidebarFunctionalGroupOrder");
  act(() => root?.unmount());
  desktopPluginHost.unload("test:app-sidebar-navigation");
  root = null;
  container.remove();
  vi.useRealTimers();
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
    id: SCRATCH_PSEUDO_PROJECT_ID,
    name: "对话",
    path: "",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
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
  expandedSidebarSectionIDs?: Set<string>;
  workspaceThreadsByWorkspaceID?: Record<string, ThreadSummary[]>;
  activeThreadID?: string;
  pendingThreadID?: string;
  onSelectThread?: (threadID: string) => void;
  onSelectWorkspaceThread?: (workspaceID: string, threadID: string) => void;
  sectionOrder?: string[];
  state?: AppState;
  collapsedSidebarSectionIDs?: Set<string>;
  onCreateProject?: (workspaceID?: string) => void;
  onAdoptIntoProject?: (projectID: string, threadID: string) => void;
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
    sectionOrder = [SCRATCH_PSEUDO_PROJECT_ID, "project-1", "project-2"],
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
    onCreateProject,
    onAdoptIntoProject,
  } = options;
  return (
    <AppSidebar
      state={state}
      sidebarWorkspaces={sidebarWorkspaces}
      pinnedThreads={[]}
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
      onStartNewThread={() => {}}
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
      onTogglePinned={() => {}}
      onArchiveThread={() => {}}
      onDeleteThread={() => {}}
      onRenameThread={() => {}}
      onToggleWorkspaceMenu={() => {}}
      onCreateWorkspace={() => {}}
      onOpenWorkspaceFolder={() => {}}
      onToggleSidebarSectionCollapsed={() => {}}
      onStartNewThreadInWorkspace={() => {}}
      onSelectWorkspaceThread={onSelectWorkspaceThread}
      onRemoveWorkspace={() => {}}
      onRelocateWorkspace={() => {}}
      onCreateProject={onCreateProject}
      onAdoptIntoProject={onAdoptIntoProject}
      onOpenSettings={() => {}}
    />
  );
}

function renderSidebar(options: RenderOptions = {}): void {
  act(() => {
    root ??= createRoot(container);
    root.render(<SidebarHarness options={options} />);
  });
}

describe("AppSidebar layout", () => {
  it.each([
    { saved: ["workspace", "pinned"], expected: ["projects", "folders", "workspace", "pinned"] },
    // Projects take the place a saved order gave Collaboration.
    { saved: ["folders", "collaboration", "workspace", "pinned"], expected: ["folders", "projects", "workspace", "pinned"] },
  ])("restores group order without resetting existing preferences: $saved", ({ saved, expected }) => {
    window.localStorage.setItem("wuu.desktop.sidebarFunctionalGroupOrder", JSON.stringify(saved));
    const order = () => [...container.querySelectorAll<HTMLElement>(".sidebar-main > .sidebar-functional-group")]
      .map((element) => element.dataset.functionalGroupId ?? element.dataset.sectionId);
    renderSidebar();
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
    expect(container.querySelector("#sidebar-unread-heading")?.nextElementSibling?.textContent).toContain(unread.title);
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
    expect(container.querySelector("#sidebar-recent-heading")?.nextElementSibling?.textContent).toContain(unread.title);
    expect(container.querySelector('[aria-current="page"] .thread-row-title')?.textContent).toBe(unread.title);
    expect(container.querySelector(".sidebar-unread-view")?.textContent).not.toContain(idle.title);
    expect(container.querySelector<HTMLElement>(".sidebar-notifications-button")?.dataset.hasUnread).toBeUndefined();
  });

  it("lets a navigation presenter replace the complete production sidebar root", async () => {
    let snapshot: NavigationSnapshotV1 | undefined;
    await desktopPluginHost.activateGeneration({
      pluginId: "test:app-sidebar-navigation",
      generation: "one",
      register(api) {
        api.registerPresenter({
          id: "sidebar",
          target: "navigation.primary",
          render: ({ snapshot: nextSnapshot }) => {
            snapshot = nextSnapshot as NavigationSnapshotV1;
            return <main data-custom-sidebar-root>custom</main>;
          },
        });
      },
    });

    renderSidebar();

    expect(container.querySelector("[data-custom-sidebar-root]")?.textContent).toBe("custom");
    expect(container.querySelector("aside.sidebar")).toBeNull();
    expect(snapshot?.nodes.map(({ id }) => id)).toEqual([
      "command:new-conversation",
      "command:search-conversations",
      "command:skills",
      "section:workspace",
      `project:${SCRATCH_PSEUDO_PROJECT_ID}`,
      "project:project-1",
      "project:project-2",
      "command:settings",
    ]);
  });

  it("keeps a fork in its owning project while the scratch cache catches up", () => {
    const fork: ThreadSummary = {
      id: "fork-thread", title: "Forked conversation", cwd: "/repo/wuu/.worktrees/topic",
      workspace_id: "project-1", status: "idle", pinned: false, archived: false,
      created_at: "2026-09-17T00:00:00Z", updated_at: "2026-09-17T00:00:00Z",
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0,
    };
    const select = vi.fn();
    renderSidebar({
      activeThreadID: fork.id,
      expandedSidebarSectionIDs: new Set([SCRATCH_PSEUDO_PROJECT_ID, "project-1"]),
      workspaceThreadsByWorkspaceID: {
        [SCRATCH_PSEUDO_PROJECT_ID]: [{ ...fork, workspace_id: undefined }],
        "project-1": [fork],
      },
      onSelectWorkspaceThread: select,
    });
    const rows = [...container.querySelectorAll<HTMLButtonElement>(".thread-row-main")]
      .filter((row) => row.textContent?.includes(fork.title!));
    expect(rows).toHaveLength(1);
    act(() => rows[0].click());
    expect(select).toHaveBeenCalledWith("project-1", fork.id);
  });

  // The failure cases these guard: a project or its sessions also crowd the
  // workspace list, a session of an archived project disappears, the project
  // row hides pending reviews, and the Projects entries do nothing.
  function sidebarThread(id: string, title: string, overrides: Partial<ThreadSummary> = {}): ThreadSummary {
    return {
      id, title, cwd: "/repo/wuu", workspace_id: "project-1", status: "idle", pinned: false, archived: false,
      created_at: "2026-09-17T00:00:00Z", updated_at: "2026-09-17T00:00:00Z",
      preview: "", model_provider: "test", model: "test", turns: [], turn_count: 0, ...overrides,
    };
  }

  function projectsGroup(): HTMLElement {
    const group = container.querySelector<HTMLElement>('.sidebar-functional-group[data-functional-group-id="projects"]');
    if (!group) throw new Error("Projects group not rendered");
    return group;
  }

  it("lists projects apart from their workspace with their pending reviews", () => {
    renderSidebar({
      expandedSidebarSectionIDs: new Set(["project-1"]),
      workspaceThreadsByWorkspaceID: {
        "project-1": [
          sidebarThread("coordinator", "Search overhaul", { source: "project", pending_candidates: 2 }),
          sidebarThread("session", "Paginate results", { source: "project-session", project_id: "coordinator", status: "in_progress" }),
          sidebarThread("orphan", "Orphaned session", { source: "project-session", project_id: "archived-project" }),
          sidebarThread("chat", "Ordinary conversation"),
        ],
      },
    });

    const projectRow = projectsGroup().querySelector<HTMLElement>(".project-thread-row");
    expect(projectRow?.textContent).toContain("Search overhaul");
    expect(projectRow?.querySelector(".project-thread-pending")?.textContent).toBe("2");
    expect(projectRow?.classList.contains("running")).toBe(true);
    const workspace = container.querySelector<HTMLElement>('section[data-section-id="project-1"]');
    const workspaceTitles = [...workspace!.querySelectorAll(".thread-row-title")].map((title) => title.textContent);
    expect(workspaceTitles.sort()).toEqual(["Ordinary conversation", "Orphaned session"]);
  });

  it("opens a project draft and adopts a conversation dropped on a project", () => {
    const create = vi.fn();
    const adopt = vi.fn();
    renderSidebar({
      expandedSidebarSectionIDs: new Set(["project-1"]),
      workspaceThreadsByWorkspaceID: {
        "project-1": [
          sidebarThread("coordinator", "Search overhaul", { source: "project" }),
          sidebarThread("chat", "Ordinary conversation"),
        ],
      },
      onCreateProject: create,
      onAdoptIntoProject: adopt,
    });

    act(() => projectsGroup().querySelector<HTMLButtonElement>('button[aria-label="新建项目"]')!.click());
    expect(create).toHaveBeenCalledTimes(1);

    const conversation = [...container.querySelectorAll<HTMLElement>(".thread-row")]
      .find((row) => row.textContent?.includes("Ordinary conversation"))!;
    const projectRow = projectsGroup().querySelector<HTMLElement>(".project-thread-row")!;
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
    drag(projectRow, "dragover");
    expect(projectRow.classList.contains("drop-active")).toBe(true);
    drag(projectRow, "drop");
    expect(adopt).toHaveBeenCalledWith("coordinator", "chat");
  });

  it("offers a first project when there is none", () => {
    const create = vi.fn();
    renderSidebar({ onCreateProject: create });
    const first = projectsGroup().querySelector<HTMLButtonElement>(".project-new-item");
    expect(first?.textContent).toBe("新建项目");
    act(() => first!.click());
    expect(create).toHaveBeenCalledTimes(1);
  });

  it("renders only scratch and projects in the workspace order", () => {
    renderSidebar({
      sectionOrder: ["project-2", SCRATCH_PSEUDO_PROJECT_ID, "project-1"],
    });

    const sections = Array.from(
      container.querySelectorAll<HTMLElement>(
        ".sidebar-functional-group-body > section[data-section-id]",
      ),
    );
    expect(sections.map((section) => section.dataset.sectionId)).toEqual([
      "project-2",
      SCRATCH_PSEUDO_PROJECT_ID,
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
