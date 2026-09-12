import { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ChannelRoom, DesktopProject, InitializeResult } from "../shared/protocol";
import { AppSidebar } from "./AppSidebar";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import type { NavigationSnapshotV1 } from "../shared/workbench";
import {
  initialState,
  SCRATCH_PSEUDO_PROJECT_ID,
  type AppState,
} from "./AppState";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
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

const sidebarProjects: DesktopProject[] = [
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
  sectionOrder?: string[];
  state?: AppState;
  groupChatEnabled?: boolean;
  channelRooms?: ChannelRoom[];
  pinnedChannelRooms?: ChannelRoom[];
  activeChannelRoomID?: string;
  activeChannelSection?: "rooms" | "agents" | "tasks" | null;
  collapsedSidebarSectionIDs?: Set<string>;
  onSelectChannelRoom?: (roomID: string) => void;
  onToggleChannelRoomPinned?: (room: ChannelRoom) => void;
  onArchiveChannelRoom?: (room: ChannelRoom) => void;
  onOpenChannelAgents?: () => void;
  onOpenChannelTasks?: () => void;
}

function renderSidebar({
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
  groupChatEnabled = false,
  channelRooms = [],
  pinnedChannelRooms = [],
  activeChannelRoomID,
  activeChannelSection = null,
  collapsedSidebarSectionIDs = new Set(),
  onSelectChannelRoom,
  onToggleChannelRoomPinned,
  onArchiveChannelRoom,
  onOpenChannelAgents,
  onOpenChannelTasks,
}: RenderOptions = {}): void {
  act(() => {
    root = createRoot(container);
    root.render(
      <AppSidebar
        state={state}
        sidebarProjects={sidebarProjects}
        pinnedThreads={[]}
        activeThreadID={undefined}
        pendingThreadID={undefined}
        pendingProjectID={undefined}
        collapsedSidebarSectionIDs={collapsedSidebarSectionIDs}
        expandedSidebarSectionIDs={new Set()}
        projectThreadsByProjectID={{}}
        projectMenuOpen={false}
        projectMenuRef={createRef<HTMLDivElement>()}
        searchOpen={false}
        sectionOrder={sectionOrder}
        onStartNewThread={() => {}}
        onOpenSkillsTab={() => {}}
        groupChatEnabled={groupChatEnabled}
        channelRooms={channelRooms}
        pinnedChannelRooms={pinnedChannelRooms}
        activeChannelRoomID={activeChannelRoomID}
        activeChannelSection={activeChannelSection}
        onSelectChannelRoom={onSelectChannelRoom}
        onToggleChannelRoomPinned={onToggleChannelRoomPinned}
        onArchiveChannelRoom={onArchiveChannelRoom}
        onOpenChannelAgents={onOpenChannelAgents}
        onOpenChannelTasks={onOpenChannelTasks}
        onOpenChannels={() => {}}
        onMarkThreadsViewed={() => {}}
        onToggleConversationSearch={() => {}}
        onSelectThread={() => {}}
        onTogglePinned={() => {}}
        onArchiveThread={() => {}}
        onDeleteThread={() => {}}
        onRenameThread={() => {}}
        onToggleProjectMenu={() => {}}
        onCreateProject={() => {}}
        onOpenProjectFolder={() => {}}
        onToggleSidebarSectionCollapsed={() => {}}
        onStartNewThreadForProject={() => {}}
        onSelectProjectThread={() => {}}
        onRemoveProject={() => {}}
        onRelocateProject={() => {}}
        onOpenSettings={() => {}}
      />,
    );
  });
}

describe("AppSidebar layout", () => {
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

  it("hides group chat unless the frontend flag is enabled", () => {
    renderSidebar();

    expect(container.textContent).not.toContain("群聊");
  });

  it("replaces the legacy group chat nav item with the 协作 section", () => {
    renderSidebar({ groupChatEnabled: true });

    const navLabels = Array.from(container.querySelectorAll(".nav-item")).map((item) => item.textContent);
    expect(navLabels).not.toContain("群聊");
    expect(container.querySelector(".channel-mention-badge")).toBeNull();
  });

  it("keeps primary actions outside the scrollable sidebar list", () => {
    renderSidebar();

    const content = container.querySelector(".sidebar-content");
    const primaryNav = container.querySelector(".primary-nav");
    const scrollRegion = container.querySelector(".sidebar-main");

    expect(primaryNav?.parentElement).toBe(content);
    expect(scrollRegion?.contains(primaryNav)).toBe(false);
    expect(scrollRegion?.querySelector(".project-section")).not.toBeNull();
  });

  it("keeps the workspace add action visible and collaboration controls out of Harness", () => {
    renderSidebar({ groupChatEnabled: true });

    const workspaceAction = container.querySelector<HTMLButtonElement>(
      '[aria-label="添加工作区"]',
    );
    const collaborationAction = container.querySelector<HTMLButtonElement>(
      '[aria-label="新建频道"]',
    );
    expect(workspaceAction).not.toBeNull();
    expect(collaborationAction).toBeNull();
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

    expect(
      container.querySelector('[data-functional-group-id="workspace"] .thread-list-collapse'),
    ).toBeNull();
    expect(container.querySelector('[aria-label="展开工作区"]')).not.toBeNull();
  });
});
