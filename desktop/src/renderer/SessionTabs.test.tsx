import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RuntimeContext, Thread } from "../shared/protocol";
import {
  createChannelRoomSessionTab,
  createDraftSessionTab,
  createSkillsSessionTab,
  createThreadSessionTab,
  initialState,
  threadSessionTabID,
  type AppState,
} from "./AppState";
import { SessionTabStrip } from "./SessionTabs";

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
});

function makeThread(id: string, preview: string): Thread {
  return {
    id,
    preview,
    model_provider: "fake",
    model: "fake-model",
    cwd: "/tmp/project",
    status: "idle",
    created_at: "2026-06-18T00:00:00Z",
    updated_at: "2026-06-18T00:00:00Z",
    turns: [],
  };
}

function makeThreadWithTurn(
  id: string,
  turnID: string,
  status: "completed" | "in_progress" = "completed",
): Thread {
  return {
    ...makeThread(id, "Thread " + id),
    turns: [
      {
        id: turnID,
        items: [],
        items_view: "full",
        status,
      },
    ],
  };
}

function renderTabs(state: AppState): void {
  renderTabsWith(state, () => {});
}

function renderTabsWith(
  state: AppState,
  onClose: (tabID: string) => void,
  onSelect: (tabID: string) => void = () => {},
  channelUnreadByRoomID: Record<string, number> = {},
): void {
  act(() => {
    root = createRoot(container);
    root.render(
      <SessionTabStrip
        state={state}
        pendingComposerMessagesByThread={{
          "thread-a": {
            queued: [{ id: "queue-a", text: "queued", images: [], files: [] }],
            guides: [{ id: "guide-a", text: "guide", images: [], files: [] }],
          },
        }}
        channelUnreadByRoomID={channelUnreadByRoomID}
        canStartNewThread
        onSelect={onSelect}
        onClose={onClose}
        onCloseTabs={() => {}}
        onPopOut={() => {}}
        onNewThread={() => {}}
        onReorder={() => {}}
      />,
    );
  });
}

describe("SessionTabStrip pending indicators", () => {
  it("marks an inactive channel room tab unread", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const draft = createDraftSessionTab("draft:active", context);
    const room = createChannelRoomSessionTab("room-1", "Design review", context);

    renderTabsWith(
      {
        ...initialState,
        activeContext: context,
        activeSessionTabID: draft.id,
        sessionTabs: [draft, room],
      },
      () => {},
      () => {},
      { "room-1": 2 },
    );

    const roomTab = Array.from(container.querySelectorAll(".session-tab")).find((tab) =>
      tab.textContent?.includes("Design review"),
    );
    expect(roomTab?.classList.contains("has-unread")).toBe(true);
  });

  it("names the center tablist as conversations rather than generic work objects", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const thread = makeThread("thread-a", "Thread A");
    renderTabs({
      ...initialState,
      activeContext: context,
      thread,
      activeSessionTabID: threadSessionTabID(thread.id),
      sessionTabs: [createThreadSessionTab(thread, context)],
      threads: [thread],
    });

    expect(container.querySelector(".session-tab-strip")?.getAttribute("aria-label")).toBe(
      "已打开的对话",
    );
    const tabList = container.querySelector(".session-tab-scroll");
    const tab = container.querySelector<HTMLButtonElement>(".session-tab-main");
    expect(tabList?.getAttribute("role")).toBe("tablist");
    expect(tab?.getAttribute("role")).toBe("tab");
    expect(tab?.getAttribute("aria-selected")).toBe("true");
    expect(tab?.tabIndex).toBe(0);
    const tabShell = container.querySelector('[data-wuu-component="session-tab"]');
    expect(tabShell?.getAttribute("data-wuu-active")).toBe("true");
    expect(tab?.getAttribute("data-wuu-component")).toBe("session-tab-main");
    expect(
      tabShell?.querySelector('[data-wuu-component="session-tab-close"]'),
    ).not.toBeNull();
  });

  it("moves focus and selection across conversations with arrow keys", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const threadA = makeThread("thread-a", "Thread A");
    const threadB = makeThread("thread-b", "Thread B");
    const onSelect = vi.fn();
    renderTabsWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, context),
          createThreadSessionTab(threadB, context),
        ],
        threads: [threadA, threadB],
      },
      () => {},
      onSelect,
    );

    const tabs = container.querySelectorAll<HTMLButtonElement>(".session-tab-main");
    tabs[0]?.focus();
    act(() => {
      tabs[0]?.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    });

    expect(onSelect).toHaveBeenCalledWith(threadSessionTabID(threadB.id));
    expect(document.activeElement).toBe(tabs[1]);
  });

  it("shows pending count only on the owning thread tab", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const threadA = makeThread("thread-a", "Thread A");
    const threadB = makeThread("thread-b", "Thread B");
    renderTabs({
      ...initialState,
      activeContext: context,
      thread: threadA,
      activeSessionTabID: threadSessionTabID(threadA.id),
      sessionTabs: [
        createThreadSessionTab(threadA, context),
        createThreadSessionTab(threadB, context),
      ],
      threads: [threadA, threadB],
    });

    const tabs = Array.from(container.querySelectorAll(".session-tab"));
    expect(tabs).toHaveLength(2);
    expect(tabs[0]?.querySelector(".session-tab-pending-count")?.textContent).toBe("2");
    expect(tabs[1]?.querySelector(".session-tab-pending-count")).toBeNull();
  });

  it("calls onClose with the tab id when the main button is double-clicked", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const threadA = makeThread("thread-a", "Thread A");
    const threadB = makeThread("thread-b", "Thread B");
    const onClose = vi.fn();
    renderTabsWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, context),
          createThreadSessionTab(threadB, context),
        ],
        threads: [threadA, threadB],
      },
      onClose,
    );

    const main = container.querySelectorAll(
      ".session-tab-main",
    )[0] as HTMLElement | undefined;
    expect(main).toBeDefined();
    act(() => {
      main?.dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    });

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledWith(threadSessionTabID(threadA.id));
  });

  it("does not close the tab on a single click", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const threadA = makeThreadWithTurn("thread-a", "turn-a-1");
    const threadB = makeThreadWithTurn("thread-b", "turn-b-1");
    renderTabs({
      ...initialState,
      activeContext: context,
      thread: threadA,
      activeSessionTabID: threadSessionTabID(threadA.id),
      sessionTabs: [
        createThreadSessionTab(threadA, context),
        createThreadSessionTab(threadB, context),
      ],
      threads: [threadA, threadB],
      lastViewedTurnByThreadID: {
        "thread-a": "turn-a-1",
      },
    });

    const tabs = Array.from(container.querySelectorAll(".session-tab"));
    expect(tabs).toHaveLength(2);
    expect(tabs[0]?.classList.contains("has-unread")).toBe(false);
    expect(tabs[1]?.classList.contains("has-unread")).toBe(true);
  });

  it("renders unread from global state even when the tab is active", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const thread = {
      ...makeThread("thread-a", "Thread A"),
      latest_completed_turn_id: "turn-a-1",
    };
    renderTabs({
      ...initialState,
      activeContext: context,
      thread,
      activeSessionTabID: threadSessionTabID(thread.id),
      sessionTabs: [createThreadSessionTab(thread, context)],
      threads: [thread],
    });

    expect(container.querySelector(".session-tab")?.classList.contains("has-unread")).toBe(true);
  });

  it("restores keyboard focus to the next active conversation after close", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const threadA = makeThread("thread-a", "Thread A");
    const threadB = makeThread("thread-b", "Thread B");
    const state = {
      ...initialState,
      activeContext: context,
      thread: threadB,
      activeSessionTabID: threadSessionTabID(threadB.id),
      sessionTabs: [
        createThreadSessionTab(threadA, context),
        createThreadSessionTab(threadB, context),
      ],
      threads: [threadA, threadB],
    };
    const renderState = (nextState: AppState): void => {
      root?.render(
        <SessionTabStrip
          state={nextState}
          pendingComposerMessagesByThread={{}}
          canStartNewThread
          onSelect={() => {}}
          onClose={(tabID) => {
            expect(tabID).toBe(threadSessionTabID(threadB.id));
            renderState({
              ...state,
              thread: threadA,
              activeSessionTabID: threadSessionTabID(threadA.id),
              sessionTabs: [createThreadSessionTab(threadA, context)],
            });
          }}
          onCloseTabs={() => {}}
          onPopOut={() => {}}
          onNewThread={() => {}}
          onReorder={() => {}}
        />,
      );
    };
    act(() => {
      root = createRoot(container);
      renderState(state);
    });

    const activeClose = container.querySelector<HTMLButtonElement>(
      ".session-tab.active .session-tab-close",
    );
    activeClose?.focus();
    act(() => activeClose?.click());

    expect(document.activeElement).toBe(
      container.querySelector(".session-tab.active .session-tab-main"),
    );
  });

  it("does not apply has-unread to running thread tabs even when their latest turn is new", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const threadA = makeThreadWithTurn("thread-a", "turn-a-1");
    const threadB = makeThreadWithTurn("thread-b", "turn-b-1", "in_progress");
    renderTabs({
      ...initialState,
      activeContext: context,
      thread: threadA,
      activeSessionTabID: threadSessionTabID(threadA.id),
      sessionTabs: [
        createThreadSessionTab(threadA, context),
        createThreadSessionTab(threadB, context),
      ],
      threads: [threadA, threadB],
      lastViewedTurnByThreadID: { "thread-a": "turn-a-1" },
    });

    const tabs = Array.from(container.querySelectorAll(".session-tab"));
    expect(tabs).toHaveLength(2);
    expect(tabs[0]?.classList.contains("has-unread")).toBe(false);
    expect(tabs[1]?.classList.contains("has-unread")).toBe(false);
    expect(tabs[1]?.classList.contains("running")).toBe(true);
  });

  it("keeps a thread tab spinning while a child agent is running", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const thread = {
      ...makeThreadWithTurn("thread-a", "turn-a-1"),
      child_agents: [{ id: "agent-running", status: "running" }],
    } as unknown as Thread;

    renderTabs({
      ...initialState,
      activeContext: context,
      thread,
      activeSessionTabID: threadSessionTabID(thread.id),
      sessionTabs: [createThreadSessionTab(thread, context)],
      threads: [thread],
    });

    expect(container.querySelector(".session-tab")?.classList.contains("running")).toBe(true);
  });
});

describe("SessionTabStrip new conversation affordance", () => {
  it("hides the tab-strip plus while preserving its slot for a channel room", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const room = createChannelRoomSessionTab("room-1", "Design review", context);

    renderTabs({
      ...initialState,
      activeContext: context,
      activeSessionTabID: room.id,
      sessionTabs: [room],
    });

    expect(container.querySelector(".session-tab-new")).toBeNull();
    expect(container.querySelector(".session-tab-new-slot")).not.toBeNull();
  });

  it("keeps the tab-strip plus while a conversation draft is active", () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
    const draft = createDraftSessionTab("draft:active", context);

    renderTabs({
      ...initialState,
      activeContext: context,
      activeSessionTabID: draft.id,
      sessionTabs: [draft],
    });

    expect(container.querySelector(".session-tab-new")).not.toBeNull();
  });
});

describe("SessionTabStrip right-click menu", () => {
  type Captured = {
    closed: string[];
    closedBatches: string[][];
    poppedOut: string[];
  };

  function makeCaptured(): Captured & {
    onSelect: (id: string) => void;
    onClose: (id: string) => void;
    onCloseTabs: (ids: string[]) => void;
    onPopOut: (id: string) => void;
  } {
    const captured: Captured = {
      closed: [],
      closedBatches: [],
      poppedOut: [],
    };
    return {
      ...captured,
      onSelect: () => {},
      onClose: (id) => {
        captured.closed.push(id);
      },
      onCloseTabs: (ids) => {
        captured.closedBatches.push([...ids]);
      },
      onPopOut: (id) => {
        captured.poppedOut.push(id);
      },
    };
  }

  function renderWith(
    state: AppState,
    captured: ReturnType<typeof makeCaptured>,
    crossWorkspaceThreads?: Thread[],
    runningThreadIDs?: ReadonlySet<string>,
  ): void {
    act(() => {
      root = createRoot(container);
      root.render(
        <SessionTabStrip
          state={state}
          crossWorkspaceThreads={crossWorkspaceThreads}
          runningThreadIDs={runningThreadIDs}
          pendingComposerMessagesByThread={{}}
          canStartNewThread
          onSelect={captured.onSelect}
          onClose={captured.onClose}
          onCloseTabs={captured.onCloseTabs}
          onPopOut={captured.onPopOut}
          onNewThread={() => {}}
          onReorder={() => {}}
        />,
      );
    });
  }

  function rightClickTab(index: number): void {
    act(() => {
      const tabs = container.querySelectorAll(".session-tab");
      const event = new MouseEvent("contextmenu", {
        bubbles: true,
        cancelable: true,
        clientX: 50,
        clientY: 20,
      });
      tabs[index]?.dispatchEvent(event);
    });
  }

  function getMenuItems(): HTMLButtonElement[] {
    return Array.from(
      document.body.querySelectorAll("[role='menuitem']"),
    ) as HTMLButtonElement[];
  }

  function clickMenuItem(label: string): void {
    act(() => {
      const target = getMenuItems().find((item) => item.textContent === label);
      target?.click();
    });
  }

  function projectContext(): RuntimeContext {
    return {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project",
    };
  }

  it("opens a menu with the close actions when a tab is right-clicked", () => {
    const context = projectContext();
    const threadA = makeThreadWithTurn("thread-a", "turn-a-1", "completed");
    const threadB = makeThreadWithTurn("thread-b", "turn-b-1", "in_progress");
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, context),
          createThreadSessionTab(threadB, context),
        ],
        threads: [threadA, threadB],
      },
      captured,
    );

    rightClickTab(0);

    const menu = document.body.querySelector(".thread-row-context-menu");
    expect(menu).not.toBeNull();
    expect(
      menu?.querySelector("[role='separator']"),
    ).not.toBeNull();
    expect(getMenuItems().map((item) => item.textContent)).toEqual([
      "在新窗口打开",
      "关闭",
      "关闭其他",
      "关闭未运行的",
      "关闭全部",
    ]);
  });

  it("disables native pop-out while keeping tab close available", () => {
    vi.stubGlobal("wuu", { unsupportedMethods: ["popOutSession"] });
    try {
      const context = projectContext();
      const thread = makeThread("thread-a", "A");
      const captured = makeCaptured();
      renderWith({ ...initialState, thread, activeContext: context,
        threads: [thread], sessionTabs: [createThreadSessionTab(thread, context)],
        activeSessionTabID: threadSessionTabID(thread.id) }, captured);
      rightClickTab(0);
      const popOut = getMenuItems().find((item) => item.textContent === "在新窗口打开");
      expect(popOut?.disabled).toBe(true);
      popOut?.click();
      expect(captured.poppedOut).toEqual([]);
      clickMenuItem("关闭");
      expect(captured.closed).toEqual([threadSessionTabID(thread.id)]);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("calls onPopOut with the right-clicked thread tab", () => {
    const context = projectContext();
    const threadA = makeThread("thread-a", "A");
    const threadB = makeThread("thread-b", "B");
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, context),
          createThreadSessionTab(threadB, context),
        ],
        threads: [threadA, threadB],
      },
      captured,
    );

    rightClickTab(1);
    clickMenuItem("在新窗口打开");

    expect(captured.poppedOut).toEqual([threadSessionTabID(threadB.id)]);
    expect(document.body.querySelector(".thread-row-context-menu")).toBeNull();
  });

  it("calls onPopOut with a draft tab", () => {
    const context = projectContext();
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        activeSessionTabID: "draft:test",
        sessionTabs: [createDraftSessionTab("draft:test", context)],
      },
      captured,
    );

    rightClickTab(0);
    clickMenuItem("在新窗口打开");

    expect(captured.poppedOut).toEqual(["draft:test"]);
  });

  it("disables pop-out for non-session tool tabs", () => {
    const context = projectContext();
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        activeSessionTabID: "skills:project:project-1",
        sessionTabs: [createSkillsSessionTab(context)],
      },
      captured,
    );

    rightClickTab(0);

    const popOut = getMenuItems().find(
      (item) => item.textContent === "在新窗口打开",
    );
    expect(popOut?.disabled).toBe(true);
  });

  it("calls onClose with the right-clicked tab for 'close'", () => {
    const context = projectContext();
    const threadA = makeThread("thread-a", "A");
    const threadB = makeThread("thread-b", "B");
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, context),
          createThreadSessionTab(threadB, context),
        ],
        threads: [threadA, threadB],
      },
      captured,
    );

    rightClickTab(1);
    clickMenuItem("关闭");

    expect(captured.closed).toEqual([threadSessionTabID(threadB.id)]);
    expect(captured.closedBatches).toEqual([]);
    expect(document.body.querySelector(".thread-row-context-menu")).toBeNull();
  });

  it("calls onCloseTabs with every tab for 'close all'", () => {
    const context = projectContext();
    const threadA = makeThread("thread-a", "A");
    const threadB = makeThread("thread-b", "B");
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, context),
          createThreadSessionTab(threadB, context),
        ],
        threads: [threadA, threadB],
      },
      captured,
    );

    rightClickTab(0);
    clickMenuItem("关闭全部");

    expect(captured.closedBatches).toEqual([
      [threadSessionTabID(threadA.id), threadSessionTabID(threadB.id)],
    ]);
  });

  it("calls onCloseTabs with everything except the right-clicked tab for 'close others'", () => {
    const context = projectContext();
    const threadA = makeThread("thread-a", "A");
    const threadB = makeThread("thread-b", "B");
    const threadC = makeThread("thread-c", "C");
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, context),
          createThreadSessionTab(threadB, context),
          createThreadSessionTab(threadC, context),
        ],
        threads: [threadA, threadB, threadC],
      },
      captured,
    );

    rightClickTab(1);
    clickMenuItem("关闭其他");

    expect(captured.closedBatches).toEqual([
      [threadSessionTabID(threadA.id), threadSessionTabID(threadC.id)],
    ]);
  });

  it("only passes non-running thread tabs to 'close non-running'", () => {
    const context = projectContext();
    const threadA = makeThreadWithTurn("thread-a", "turn-a-1", "completed");
    const threadB = makeThreadWithTurn("thread-b", "turn-b-1", "in_progress");
    const threadC = makeThreadWithTurn("thread-c", "turn-c-1", "completed");
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, context),
          createThreadSessionTab(threadB, context),
          createThreadSessionTab(threadC, context),
        ],
        threads: [threadA, threadB, threadC],
      },
      captured,
    );

    rightClickTab(0);
    clickMenuItem("关闭未运行的");

    expect(captured.closedBatches).toEqual([
      [threadSessionTabID(threadA.id), threadSessionTabID(threadC.id)],
    ]);
  });

  it("keeps a running tab from another workspace spinning and out of 'close non-running'", () => {
    const contextA = projectContext();
    const contextB: RuntimeContext = {
      kind: "project",
      project_id: "project-2",
      cwd: "/tmp/project-2",
    };
    const threadA = makeThreadWithTurn("thread-a", "turn-a-1", "completed");
    const threadB = {
      ...makeThreadWithTurn("thread-b", "turn-b-1", "in_progress"),
      cwd: contextB.cwd,
    };
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: contextA,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, contextA),
          createThreadSessionTab(threadB, contextB),
        ],
        threads: [threadA],
      },
      captured,
      [threadA, threadB],
    );

    expect(container.querySelectorAll(".session-tab")[1]?.classList.contains("running")).toBe(
      true,
    );

    rightClickTab(0);
    clickMenuItem("关闭未运行的");

    expect(captured.closedBatches).toEqual([[threadSessionTabID(threadA.id)]]);
  });

  it("spins a tab whose turn runs in another workspace even when its cached thread is stale", () => {
    const contextA = projectContext();
    const contextB: RuntimeContext = {
      kind: "project",
      project_id: "project-2",
      cwd: "/tmp/project-2",
    };
    const threadA = makeThreadWithTurn("thread-a", "turn-a-1", "completed");
    // The cached snapshot of threadB says completed (stale); only the
    // main-process running aggregate knows its turn is in progress.
    const threadB = {
      ...makeThreadWithTurn("thread-b", "turn-b-1", "completed"),
      cwd: contextB.cwd,
    };
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: contextA,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, contextA),
          createThreadSessionTab(threadB, contextB),
        ],
        threads: [threadA],
      },
      captured,
      [threadA, threadB],
      new Set(["thread-b"]),
    );

    expect(container.querySelectorAll(".session-tab")[1]?.classList.contains("running")).toBe(
      true,
    );

    rightClickTab(0);
    clickMenuItem("关闭未运行的");

    expect(captured.closedBatches).toEqual([[threadSessionTabID(threadA.id)]]);
  });

  it("disables 'close others' and 'close all' when only one tab is open", () => {
    const context = projectContext();
    const threadA = makeThread("thread-a", "A");
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [createThreadSessionTab(threadA, context)],
        threads: [threadA],
      },
      captured,
    );

    rightClickTab(0);

    const items = getMenuItems();
    const closeOthers = items.find((item) => item.textContent === "关闭其他");
    const closeAll = items.find((item) => item.textContent === "关闭全部");
    expect(closeOthers?.disabled).toBe(true);
    expect(closeAll?.disabled).toBe(true);
  });

  it("disables 'close non-running' when every thread tab is running", () => {
    const context = projectContext();
    const threadA = makeThreadWithTurn("thread-a", "turn-a-1", "in_progress");
    const threadB = makeThreadWithTurn("thread-b", "turn-b-1", "in_progress");
    const captured = makeCaptured();
    renderWith(
      {
        ...initialState,
        activeContext: context,
        thread: threadA,
        activeSessionTabID: threadSessionTabID(threadA.id),
        sessionTabs: [
          createThreadSessionTab(threadA, context),
          createThreadSessionTab(threadB, context),
        ],
        threads: [threadA, threadB],
      },
      captured,
    );

    rightClickTab(0);

    const closeNonRunning = getMenuItems().find(
      (item) => item.textContent === "关闭未运行的",
    );
    expect(closeNonRunning?.disabled).toBe(true);
  });
});
