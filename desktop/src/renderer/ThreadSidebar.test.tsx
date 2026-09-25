import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ThreadContextMenu } from "./ThreadContextMenu";
import { PinnedThreadList, ProjectGroup, ProjectList } from "./ThreadSidebar";
import type { DesktopProject, Thread } from "../shared/protocol";
import { SCRATCH_PSEUDO_PROJECT_ID, summarizeThreadsForSidebar } from "./AppState";
import { setActiveLocale } from "./i18n";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  document.body
    .querySelectorAll(".thread-row-context-menu")
    .forEach((menu) => menu.remove());
  delete (window as unknown as { wuu?: unknown }).wuu;
  setActiveLocale("zh-CN");
});

function changeInput(input: HTMLInputElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  setter?.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

describe("ThreadContextMenu", () => {
  function renderMenu(): { onSelect: ReturnType<typeof vi.fn>; onClose: ReturnType<typeof vi.fn> } {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    act(() => {
      root = createRoot(container);
      root.render(
        <ThreadContextMenu
          x={10}
          y={20}
          items={[{ label: "复制 thread ID", onSelect }]}
          onClose={onClose}
        />
      );
    });
    return { onSelect, onClose };
  }

  it("invokes onSelect and onClose when an item is clicked", () => {
    const { onSelect, onClose } = renderMenu();
    const button = document.body.querySelector(
      ".thread-row-context-menu button",
    ) as HTMLButtonElement | null;
    expect(button).not.toBeNull();
    act(() => {
      button!.click();
    });
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes when Escape is pressed", () => {
    const { onClose } = renderMenu();
    act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe("ProjectList", () => {
  function makeProject(id: string, name: string, path: string): DesktopProject {
    return {
      id,
      name,
      path,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    };
  }

  function makeProjectThread(
    id: string,
    cwd: string,
    title: string,
    turns: Array<{
      id: string;
      status: "completed" | "in_progress" | "failed" | "interrupted";
    }> = [],
    overrides: Partial<Thread> = {},
  ): Thread {
    return {
      id,
      preview: title,
      title,
      model_provider: "openai",
      model: "gpt-4",
      cwd,
      workspace_kind: "project",
      status: "idle",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      turns: turns.map((turn) => ({
        id: turn.id,
        items: [],
        items_view: "full" as const,
        status: turn.status,
      })),
      ...overrides,
    };
  }

  it("can show session lists for multiple expanded projects", () => {
    const projects = [
      makeProject("project-1", "wuu", "/repo/wuu"),
      makeProject("project-2", "interview", "/repo/interview"),
    ];
    act(() => {
      root = createRoot(container);
      root.render(
        <ProjectList
          projects={projects}
          activeID="project-1"
          pendingProjectID={undefined}
          expandedSidebarSectionIDs={new Set(["project-1", "project-2"])}
          threadsByProjectID={{
            "project-1": summarizeThreadsForSidebar([
              makeProjectThread("thread-wuu", "/repo/wuu", "Wuu session"),
            ]),
            "project-2": summarizeThreadsForSidebar([
              makeProjectThread(
                "thread-wrong-project",
                "/repo/wuu",
                "Wrong duplicate",
              ),
              makeProjectThread(
                "thread-interview",
                "/repo/interview",
                "Interview session",
              ),
            ]),
          }}
          activeThreadID={undefined}
          pendingThreadID={undefined}
          
          lastViewedTurnByThreadID={{}}
          scratchPseudoProjectID={SCRATCH_PSEUDO_PROJECT_ID}
          scratchPseudoActive={false}
          onToggleSidebarSectionCollapsed={() => {}}
          onStartNewThread={() => {}}
          onSelectThread={() => {}}
          onToggleThreadPinned={() => {}}
          onArchiveThread={() => {}}
          onDeleteThread={() => {}}
          
        />,
      );
    });

    const projectRows = container.querySelectorAll(".project-row");
    expect(projectRows[0]?.getAttribute("aria-expanded")).toBe("true");
    expect(projectRows[1]?.getAttribute("aria-expanded")).toBe("true");
    expect(container.textContent).toContain("Wuu session");
    expect(container.textContent).toContain("Interview session");
    expect(container.textContent).not.toContain("Wrong duplicate");
  });

  it("keeps the parent thread spinning while a direct child agent runs", () => {
    const [thread] = summarizeThreadsForSidebar([
      makeProjectThread("group-1", "/repo/wuu", "Group work", [], {
        child_agents: [
          {
            id: "agent-running",
            parent_id: "group-1",
            status: "running",
            task_name: "review",
          },
        ],
      }),
    ]);

    act(() => {
      root = createRoot(container);
      root.render(
        <PinnedThreadList
          threads={[thread]}
          activeID={undefined}
          pendingThreadID={undefined}
          
          lastViewedTurnByThreadID={{}}
          onSelect={() => {}}
          onTogglePinned={() => {}}
          onArchive={() => {}}
          onDelete={() => {}}
          
        />,
      );
    });

    const row = container.querySelector(".thread-row");
    expect(row?.classList.contains("running")).toBe(true);
    expect(row?.querySelector(".thread-row-spinner")).not.toBeNull();
    expect(
      row?.querySelector(".thread-row-main")?.getAttribute("aria-label"),
    ).toContain("响应中");
  });

  it("opens the rename dialog from a double-click and saves through the sidebar owner", () => {
    const [thread] = summarizeThreadsForSidebar([
      makeProjectThread("thread-rename", "/repo/wuu", "Old title"),
    ]);
    const onRename = vi.fn();

    act(() => {
      root = createRoot(container);
      root.render(
        <PinnedThreadList
          threads={[thread]}
          activeID={undefined}
          pendingThreadID={undefined}
          
          lastViewedTurnByThreadID={{}}
          onSelect={() => {}}
          onTogglePinned={() => {}}
          onArchive={() => {}}
          onDelete={() => {}}
          onRename={onRename}
          
        />,
      );
    });

    const button = container.querySelector(".thread-row-main");
    expect(button).not.toBeNull();
    act(() => {
      button?.dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    });

    const overlay = document.body.querySelector(
      ".app-modal-backdrop.conversation-search-overlay.sidebar-name-dialog-overlay",
    );
    expect(overlay).not.toBeNull();
    expect(container.querySelector(".sidebar-name-dialog")).toBeNull();
    const dialog = document.body.querySelector(".sidebar-name-dialog");
    expect(dialog?.getAttribute("role")).toBe("dialog");
    expect(dialog?.firstElementChild?.classList.contains("sidebar-name-dialog-header")).toBe(true);
    expect(dialog?.querySelector(".sidebar-name-dialog-title")?.textContent).toBe("重命名对话");

    const input = document.body.querySelector<HTMLInputElement>(
      ".sidebar-name-dialog-input",
    );
    expect(input?.value).toBe("Old title");
    const field = dialog?.querySelector(".sidebar-name-dialog-field");
    expect(field?.querySelector(".sidebar-name-dialog-label")?.textContent).toBe("会话标题");
    expect(field?.contains(input)).toBe(true);
    const actions = dialog?.lastElementChild;
    expect(actions?.classList.contains("sidebar-name-dialog-actions")).toBe(true);
    expect(actions?.classList.contains("conversation-search-status")).toBe(false);
    expect(actions?.textContent?.replace(/\s/g, "")).toBe("取消保存");

    act(() => {
      changeInput(input!, "New title");
    });
    const save = Array.from(document.body.querySelectorAll("button")).find(
      (el) => el.textContent === "保存",
    );
    expect(save).not.toBeUndefined();
    act(() => {
      save?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    expect(onRename).toHaveBeenCalledWith(thread, "New title");
    expect(document.body.querySelector(".sidebar-name-dialog")).toBeNull();
  });

  it("opens the same rename dialog from the context menu instead of window.prompt", () => {
    const [thread] = summarizeThreadsForSidebar([
      makeProjectThread("thread-rename", "/repo/wuu", "Old title"),
    ]);
    const onRename = vi.fn();
    const originalPrompt = window.prompt;
    window.prompt = vi.fn();

    try {
      act(() => {
        root = createRoot(container);
        root.render(
          <PinnedThreadList
            threads={[thread]}
            activeID={undefined}
            pendingThreadID={undefined}
            
            lastViewedTurnByThreadID={{}}
            onSelect={() => {}}
            onTogglePinned={() => {}}
            onArchive={() => {}}
            onDelete={() => {}}
            onRename={onRename}
            
          />,
        );
      });

      const row = container.querySelector(".thread-row");
      expect(row).not.toBeNull();
      act(() => {
        row?.dispatchEvent(
          new MouseEvent("contextmenu", {
            bubbles: true,
            cancelable: true,
            clientX: 10,
            clientY: 10,
          }),
        );
      });

      const item = Array.from(
        document.body.querySelectorAll(".thread-row-context-menu-item"),
      ).find((el) => el.textContent === "重命名对话");
      expect(item).not.toBeUndefined();
      act(() => {
        item?.dispatchEvent(
          new MouseEvent("click", { bubbles: true, cancelable: true }),
        );
      });

      const overlay = document.body.querySelector(
        ".app-modal-backdrop.conversation-search-overlay.sidebar-name-dialog-overlay",
      );
      expect(overlay).not.toBeNull();
      expect(container.querySelector(".sidebar-name-dialog")).toBeNull();
      const dialog = document.body.querySelector(".sidebar-name-dialog");
      expect(dialog?.getAttribute("role")).toBe("dialog");
      const input = document.body.querySelector<HTMLInputElement>(
        ".sidebar-name-dialog-input",
      );
      expect(input?.value).toBe("Old title");
      act(() => {
        changeInput(input!, "New title");
      });
      const save = Array.from(document.body.querySelectorAll("button")).find(
        (el) => el.textContent === "保存",
      );
      act(() => {
        save?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      });

      expect(onRename).toHaveBeenCalledWith(thread, "New title");
      expect(window.prompt).not.toHaveBeenCalled();
    } finally {
      window.prompt = originalPrompt;
    }
  });

  it("never auto-expands the active section — expansion is header-toggle only", () => {
    // Mental model regression: selecting a session (which makes its project
    // or the 对话 pseudo section "active") must not expand anything. Both
    // sections here are active yet absent from the expanded set, so both
    // render collapsed.
    const projects = [
      makeProject(SCRATCH_PSEUDO_PROJECT_ID, "对话", ""),
      makeProject("project-1", "wuu", "/repo/wuu"),
    ];
    act(() => {
      root = createRoot(container);
      root.render(
        <ProjectList
          projects={projects}
          activeID="project-1"
          pendingProjectID={undefined}
          expandedSidebarSectionIDs={new Set()}
          threadsByProjectID={{
            [SCRATCH_PSEUDO_PROJECT_ID]: summarizeThreadsForSidebar([
              makeProjectThread("thread-scratch", "/tmp/scratch", "Scratch talk"),
            ]),
            "project-1": summarizeThreadsForSidebar([
              makeProjectThread("thread-wuu", "/repo/wuu", "Wuu session"),
            ]),
          }}
          activeThreadID="thread-wuu"
          pendingThreadID={undefined}
          
          lastViewedTurnByThreadID={{}}
          scratchPseudoProjectID={SCRATCH_PSEUDO_PROJECT_ID}
          scratchPseudoActive={true}
          onToggleSidebarSectionCollapsed={() => {}}
          onStartNewThread={() => {}}
          onSelectThread={() => {}}
          onToggleThreadPinned={() => {}}
          onArchiveThread={() => {}}
          onDeleteThread={() => {}}
          
        />,
      );
    });

    for (const row of Array.from(container.querySelectorAll(".project-row"))) {
      expect(row.getAttribute("aria-expanded")).toBe("false");
    }
    expect(container.textContent).not.toContain("Wuu session");
    expect(container.textContent).not.toContain("Scratch talk");
  });

  it("keeps pinned sessions out of project lists", () => {
    const projects = [makeProject("project-1", "wuu", "/repo/wuu")];
    act(() => {
      root = createRoot(container);
      root.render(
        <ProjectList
          projects={projects}
          activeID="project-1"
          pendingProjectID={undefined}
          expandedSidebarSectionIDs={new Set(["project-1"])}
          threadsByProjectID={{
            "project-1": summarizeThreadsForSidebar([
              makeProjectThread("thread-pinned", "/repo/wuu", "Pinned session", [], {
                pinned: true,
              }),
              makeProjectThread("thread-normal", "/repo/wuu", "Normal session"),
            ]),
          }}
          activeThreadID={undefined}
          pendingThreadID={undefined}
          
          lastViewedTurnByThreadID={{}}
          scratchPseudoProjectID={SCRATCH_PSEUDO_PROJECT_ID}
          scratchPseudoActive={false}
          onToggleSidebarSectionCollapsed={() => {}}
          onStartNewThread={() => {}}
          onSelectThread={() => {}}
          onToggleThreadPinned={() => {}}
          onArchiveThread={() => {}}
          onDeleteThread={() => {}}
          
        />,
      );
    });

    expect(container.textContent).toContain("Normal session");
    expect(container.textContent).not.toContain("Pinned session");
  });

  it("shows project-level unread state for collapsed unread threads", () => {
    const projects = [makeProject("project-1", "wuu", "/repo/wuu")];

    act(() => {
      root = createRoot(container);
      root.render(
        <ProjectList
          projects={projects}
          activeID={undefined}
          pendingProjectID={undefined}
          expandedSidebarSectionIDs={new Set()}
          threadsByProjectID={{
            "project-1": summarizeThreadsForSidebar([
              makeProjectThread("thread-unread", "/repo/wuu", "Unread session", [
                { id: "turn-unread", status: "completed" },
              ]),
            ]),
          }}
          activeThreadID={undefined}
          pendingThreadID={undefined}
          
          lastViewedTurnByThreadID={{}}
          scratchPseudoProjectID={SCRATCH_PSEUDO_PROJECT_ID}
          scratchPseudoActive={false}
          onToggleSidebarSectionCollapsed={() => {}}
          onStartNewThread={() => {}}
          onSelectThread={() => {}}
          onToggleThreadPinned={() => {}}
          onArchiveThread={() => {}}
          onDeleteThread={() => {}}
          
        />,
      );
    });

    const projectRow = container.querySelector(".project-row");
    expect(projectRow?.classList.contains("has-unread")).toBe(true);
    expect(projectRow?.getAttribute("aria-label")).toContain("有未读会话");
    expect(projectRow?.querySelector(".project-row-unread")).not.toBeNull();
  });

  it("marks only the visible fork endpoint in a chained fork list", () => {
    const projects = [makeProject("project-1", "wuu", "/repo/wuu")];
    const rootThread = makeProjectThread("root-thread", "/repo/wuu", "Root session");
    const middleThread = makeProjectThread(
      "middle-thread",
      "/repo/wuu",
      "Middle session",
      [],
      { forked_from_id: rootThread.id },
    );
    const leafThread = makeProjectThread(
      "leaf-thread",
      "/repo/wuu",
      "Leaf session",
      [],
      { forked_from_id: middleThread.id },
    );

    act(() => {
      root = createRoot(container);
      root.render(
        <ProjectList
          projects={projects}
          activeID="project-1"
          pendingProjectID={undefined}
          expandedSidebarSectionIDs={new Set(["project-1"])}
          threadsByProjectID={{
            "project-1": summarizeThreadsForSidebar([
              rootThread,
              middleThread,
              leafThread,
            ]),
          }}
          activeThreadID={undefined}
          pendingThreadID={undefined}
          
          lastViewedTurnByThreadID={{}}
          scratchPseudoProjectID={SCRATCH_PSEUDO_PROJECT_ID}
          scratchPseudoActive={false}
          onToggleSidebarSectionCollapsed={() => {}}
          onStartNewThread={() => {}}
          onSelectThread={() => {}}
          onToggleThreadPinned={() => {}}
          onArchiveThread={() => {}}
          onDeleteThread={() => {}}
          
        />,
      );
    });

    expect(container.querySelectorAll(".thread-row-fork-icon").length).toBe(1);
    const middleRow = container.querySelector(
      '.thread-row-main[aria-label^="Middle session"]',
    );
    const leafRow = container.querySelector(
      '.thread-row-main[aria-label^="Middle session，分叉自其他会话"]',
    );
    expect(middleRow?.getAttribute("aria-label")).not.toContain("分叉自其他会话");
    expect(leafRow).not.toBeNull();
  });
});

describe("ProjectGroup remove workspace", () => {
  function makeProject(id: string, name: string, path: string): DesktopProject {
    return {
      id,
      name,
      path,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    };
  }

  const baseProps = {
    activeID: undefined,
    pendingProjectID: undefined,
    expandedSidebarSectionIDs: new Set<string>(),
    threadsByProjectID: {},
    activeThreadID: undefined,
    pendingThreadID: undefined,
    
    lastViewedTurnByThreadID: {},
    scratchPseudoProjectID: SCRATCH_PSEUDO_PROJECT_ID,
    scratchPseudoActive: false,
    onToggleSidebarSectionCollapsed: () => {},
    onStartNewThread: () => {},
    onSelectThread: () => {},
    onToggleThreadPinned: () => {},
    onArchiveThread: () => {},
    onDeleteThread: () => {},
    
  };

  function openContextMenu(): void {
    const header = container.querySelector(".sidebar-section-header-group");
    expect(header).not.toBeNull();
    act(() => {
      header?.dispatchEvent(
        new MouseEvent("contextmenu", {
          bubbles: true,
          cancelable: true,
          clientX: 10,
          clientY: 10,
        }),
      );
    });
  }

  it("shows loading instead of an empty state before project sessions hydrate", () => {
    const project = makeProject("project-1", "wuu", "/repo/wuu");
    act(() => {
      root = createRoot(container);
      root.render(
        <ProjectGroup
          {...baseProps}
          project={project}
          expandedSidebarSectionIDs={new Set([project.id])}
          loadingProjectThreadIDs={new Set([project.id])}
        />,
      );
    });

    expect(container.textContent).toContain("正在加载会话");
    expect(container.textContent).not.toContain("还没有会话");
    expect(container.querySelector(".project-row-loading")).not.toBeNull();
  });

  it("opens a 移除工作区 menu on a real project row and reports the id", () => {
    const removed: string[] = [];
    act(() => {
      root = createRoot(container);
      root.render(
        <ProjectGroup
          {...baseProps}
          project={makeProject("project-1", "wuu", "/repo/wuu")}
          onRemoveProject={(id) => removed.push(id)}
        />,
      );
    });

    openContextMenu();
    expect(document.body.querySelector(".thread-row-context-menu")).not.toBeNull();
    const item = Array.from(
      document.body.querySelectorAll(".thread-row-context-menu-item"),
    ).find((el) => el.textContent === "移除工作区");
    expect(item).not.toBeUndefined();

    act(() => {
      item?.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });
    expect(removed).toEqual(["project-1"]);
  });

  it("offers a 重新定位 menu item on a real project row and reports the id", () => {
    const relocated: string[] = [];
    act(() => {
      root = createRoot(container);
      root.render(
        <ProjectGroup
          {...baseProps}
          project={makeProject("project-1", "wuu", "/repo/wuu")}
          onRelocateProject={(id) => relocated.push(id)}
        />,
      );
    });

    openContextMenu();
    const item = Array.from(
      document.body.querySelectorAll(".thread-row-context-menu-item"),
    ).find((el) => el.textContent === "重新定位…");
    expect(item).not.toBeUndefined();

    act(() => {
      item?.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });
    expect(relocated).toEqual(["project-1"]);
  });

  it("offers no context menu on the 对话 scratch pseudo row", () => {
    act(() => {
      root = createRoot(container);
      root.render(
        <ProjectGroup
          {...baseProps}
          project={makeProject(SCRATCH_PSEUDO_PROJECT_ID, "对话", "")}
          onRemoveProject={() => {}}
        />,
      );
    });

    openContextMenu();
    expect(document.body.querySelector(".thread-row-context-menu")).toBeNull();
  });
});

describe("ProjectGroup missing workspace", () => {
  const baseProps = {
    activeID: undefined,
    pendingProjectID: undefined,
    expandedSidebarSectionIDs: new Set<string>(),
    threadsByProjectID: {},
    activeThreadID: undefined,
    pendingThreadID: undefined,
    
    lastViewedTurnByThreadID: {},
    scratchPseudoProjectID: SCRATCH_PSEUDO_PROJECT_ID,
    scratchPseudoActive: false,
    onToggleSidebarSectionCollapsed: () => {},
    onStartNewThread: () => {},
    onSelectThread: () => {},
    onToggleThreadPinned: () => {},
    onArchiveThread: () => {},
    onDeleteThread: () => {},
    
    onRemoveProject: () => {},
  };

  function renderProject(project: DesktopProject): void {
    act(() => {
      root = createRoot(container);
      root.render(<ProjectGroup {...baseProps} project={project} />);
    });
  }

  const makeProject = (missing?: boolean): DesktopProject => ({
    id: "project-1",
    name: "wuu",
    path: "/repo/wuu",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    missing,
  });

  it("dims a missing workspace and disables its 新建会话 button", () => {
    renderProject(makeProject(true));
    expect(container.querySelector(".project-group-missing")).not.toBeNull();
    const newThread = container.querySelector<HTMLButtonElement>(
      ".project-row-new-thread",
    );
    expect(newThread?.disabled).toBe(true);
  });
});
