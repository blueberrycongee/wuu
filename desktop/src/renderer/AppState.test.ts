import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  Agent,
  ChannelRoom,
  DesktopProject,
  ExtensionInventoryRecord,
  RuntimeContext,
  Thread,
  ThreadItem,
  Turn,
} from "../shared/protocol";
import {
  activeTodoUpdateForThread,
  activeThreadForState,
  activeTurnIsAnswerReady,
  activeTurnTokenSpeed,
  activeTurnTokenSpeedSnapshot,
  applyLoadedRuntimeWithDraftCarry,
  appendStreamingTokenSample,
  appendTurnTokenSample,
  composerDraftHasContent,
  conversationPaneThreadsByID,
  conversationSearchContextLabel,
  conversationSearchThreadMeta,
  channelRoomSessionTabID,
  createAgentsSessionTab,
  createChannelRoomSessionTab,
  createDraftSessionTab,
  createSkillsSessionTab,
  createTasksSessionTab,
  createThreadSessionTab,
  sessionTabLabel,
  handleStreamingNotification,
  initialState,
  isScratchThread,
  isStateActiveThreadRunning,
  isThreadExecuting,
  isThreadRunning,
  isThreadUnread,
  latestCompletedTurnID,
  latestContextUsageForThread,
  RETAINED_TURN_TELEMETRY_LIMIT,
  mergeListedThreads,
  reconcileListedThreadState,
  mergeSidebarThread,
  markThreadTurnsViewed,
  openForkThreadAsPrimary,
  pinnedThreads,
  pinnedThreadSummaries,
  presentationRunningThreadIDs,
  projectThreads,
  queryTextsForThread,
  requestedHandoffIntentForThread,
  reconcileChannelRoomSessionTabs,
  reconcileResumedThreadTurns,
  reduceServerEvent,
  resolveComposerRunningAction,
  resolveThreadRuntimeContext,
  scratchThreadSummaries,
  sortThreads,
  summarizeThreadsForSidebar,
  threadBelongsToProject,
  threadNeedsResumeOnReselect,
  threadProjectPath,
  threadSessionTabID,
  turnStreamStatusForThread,
  turnPreview,
  withExtensionInventoryForContext,
  workspacePanelContext,
  type AppState,
  type ComposerDraftState,
  type SessionTab,
  type ThreadSummary,
} from "./AppState";
import { PROCESS_NOTIFICATION_NAME } from "./InternalUserNotification";
import { streamTextKey, streamTextStore } from "./StreamText";
import { resolveLocalizedText, setActiveLocale } from "./i18n";

afterEach(() => {
  setActiveLocale("zh-CN");
});

function installManualRAF(): {
  flush: () => void;
  restore: () => void;
} {
  const realRAF = window.requestAnimationFrame;
  const pending: FrameRequestCallback[] = [];
  let nextHandle = 1;
  window.requestAnimationFrame = ((cb: FrameRequestCallback) => {
    pending.push(cb);
    return nextHandle++;
  }) as typeof window.requestAnimationFrame;
  return {
    flush: () => {
      const callbacks = pending.splice(0);
      for (const cb of callbacks) {
        cb(performance.now());
      }
    },
    restore: () => {
      window.requestAnimationFrame = realRAF;
    },
  };
}

describe("activeThreadForState", () => {
  it("follows the visible session tab when the loaded primary thread briefly lags behind", () => {
    const context: RuntimeContext = { kind: "project", project_id: "project-1", cwd: "/repo" };
    const stale = { ...threadWithUserTexts(["settings task"]), id: "thread-settings" };
    const visible = { ...threadWithUserTexts(["NetEase task"]), id: "thread-netease" };
    const state: AppState = {
      ...initialState,
      activeContext: context,
      thread: stale,
      threads: [stale, visible],
      sessionTabs: [createThreadSessionTab(stale, context), createThreadSessionTab(visible, context)],
      activeSessionTabID: threadSessionTabID(visible.id),
    };

    expect(activeThreadForState(state)?.id).toBe(visible.id);
  });

  it("does not fall back to a stale thread while a draft or unresolved thread tab is visible", () => {
    const context: RuntimeContext = { kind: "project", project_id: "project-1", cwd: "/repo" };
    const stale = { ...threadWithUserTexts(["old task"]), id: "thread-old" };
    const draft = createDraftSessionTab("draft:new", context);
    const unresolved = createThreadSessionTab(
      { ...threadWithUserTexts(["new task"]), id: "thread-loading" },
      context,
    );

    expect(activeThreadForState({
      ...initialState,
      activeContext: context,
      thread: stale,
      threads: [stale],
      sessionTabs: [draft],
      activeSessionTabID: draft.id,
    })).toBeUndefined();
    expect(activeThreadForState({
      ...initialState,
      activeContext: context,
      thread: stale,
      threads: [stale],
      sessionTabs: [unresolved],
      activeSessionTabID: unresolved.id,
    })).toBeUndefined();
  });
});

// The model button locks whenever the active conversation is running, and a
// running background agent counts — even with no in-progress turn. This is the
// load-bearing computation behind runtimeControlsDisabled in App.tsx (issue #81
// frontend rule 2). A completed-turn thread whose only running signal is a
// child agent must still report as running.
describe("isStateActiveThreadRunning with a background agent", () => {
  const context: RuntimeContext = { kind: "project", project_id: "project-1", cwd: "/repo" };

  function stateWithActiveThread(thread: Thread): AppState {
    return {
      ...initialState,
      activeContext: context,
      thread,
      threads: [thread],
      sessionTabs: [createThreadSessionTab(thread, context)],
      activeSessionTabID: threadSessionTabID(thread.id),
    };
  }

  it("keeps an otherwise idle parent visually settled while a background agent runs", () => {
    const thread = {
      ...threadWithUserTexts(["kick off a worker"]),
      status: "idle" as const,
      child_agents: [{ id: "agent-running", status: "running" }],
    } as unknown as Thread;
    expect(isThreadRunning(thread)).toBe(false);
    expect(isStateActiveThreadRunning(stateWithActiveThread(thread))).toBe(false);
  });

  it("unlocks once the background agent reaches a terminal state", () => {
    const thread = {
      ...threadWithUserTexts(["worker finished"]),
      status: "idle" as const,
      child_agents: [{ id: "agent-done", status: "completed" }],
    } as unknown as Thread;
    expect(isThreadRunning(thread)).toBe(false);
    expect(isStateActiveThreadRunning(stateWithActiveThread(thread))).toBe(false);
  });

  it("queues Enter while only a background agent keeps the thread running", () => {
    const thread = {
      ...threadWithUserTexts(["worker is delivering"]),
      status: "idle" as const,
      child_agents: [{ id: "agent-running", status: "running" }],
    } as unknown as Thread;

    expect(isStateActiveThreadRunning(stateWithActiveThread(thread))).toBe(false);
    expect(resolveComposerRunningAction("steer", thread)).toBe("queue");
  });

  it("keeps Enter as steer while the parent turn is still in progress", () => {
    const idleThread = threadWithUserTexts(["parent is running"]);
    const thread = {
      ...idleThread,
      status: "in_progress" as const,
      turns: idleThread.turns.map((turn) => ({
        ...turn,
        status: "in_progress" as const,
        items: [
          ...turn.items,
          {
            id: "commentary-1",
            type: "agent_message" as const,
            status: "completed" as const,
            terminal: false,
            text: "I will inspect that.",
          },
        ],
      })),
    };

    expect(resolveComposerRunningAction("steer", thread)).toBe("steer");
  });

  it("queues Enter once the final answer item completed but the turn is still settling", () => {
    const idleThread = threadWithUserTexts(["parent is settling"]);
    const thread = {
      ...idleThread,
      status: "in_progress" as const,
      turns: idleThread.turns.map((turn) => ({
        ...turn,
        status: "in_progress" as const,
        items: [
          ...turn.items,
          {
            id: "answer-1",
            type: "agent_message" as const,
            status: "completed" as const,
            terminal: true,
            text: "done",
          },
        ],
      })),
    };

    expect(resolveComposerRunningAction("steer", thread)).toBe("queue");
    expect(activeTurnIsAnswerReady(thread)).toBe(true);
  });
});

describe("subagent session state must not leak across tabs", () => {
  const context: RuntimeContext = {
    kind: "project",
    project_id: "project-1",
    cwd: "/repo",
  };

  function threadWithRunningTurn(id: string): Thread {
    return {
      ...threadWithUserTexts([`prompt for ${id}`]),
      id,
      status: "in_progress" as const,
      turns: [
        {
          id: `${id}-turn-running`,
          items_view: "full",
          status: "in_progress" as const,
          items: [],
        },
      ],
    } as unknown as Thread;
  }

  it("does not mark the active thread running when a background subtask starts", () => {
    const active = {
      ...threadWithUserTexts(["an idle conversation"]),
      id: "thread-a",
    };
    const background = {
      ...threadWithUserTexts(["background task"]),
      id: "thread-child",
      read_only: true as const,
    };
    const state = {
      ...initialState,
      activeContext: context,
      thread: active,
      sessionTabs: [createThreadSessionTab(active, context)],
      activeSessionTabID: threadSessionTabID(active.id),
      threads: [active, background],
      running: false,
      status: "ready",
    };

    const next = reduceServerEvent(state, {
      kind: "notification",
      workdir: "/repo",
      message: {
        method: "turn/started",
        params: {
          thread_id: background.id,
          turn: {
            id: `${background.id}-turn-running`,
            items_view: "full",
            status: "in_progress",
            items: [],
          },
        },
      },
    });

    expect(next.running).toBe(false);
    expect(isStateActiveThreadRunning(next)).toBe(false);
    expect(
      next.threads.find((thread) => thread.id === background.id)?.turns.at(-1)
        ?.status,
    ).toBe("in_progress");
  });

  it("keeps the active thread running when a background session completes its turn", () => {
    const active = threadWithRunningTurn("thread-a");
    const background = {
      ...threadWithRunningTurn("thread-child"),
      read_only: true as const,
    };
    const state = {
      ...initialState,
      activeContext: context,
      thread: active,
      sessionTabs: [createThreadSessionTab(active, context)],
      activeSessionTabID: threadSessionTabID(active.id),
      threads: [active, background],
      running: true,
      status: "running",
    };

    const next = reduceServerEvent(state, {
      kind: "notification",
      workdir: "/repo",
      message: {
        method: "turn/completed",
        params: {
          thread_id: background.id,
          turn: {
            id: `${background.id}-turn-running`,
            items_view: "full",
            status: "completed",
            items: [],
          },
        },
      },
    });

    expect(next.running).toBe(true);
    expect(next.status).toBe("running");
    expect(
      next.threads.find((thread) => thread.id === background.id)?.turns.at(-1)
        ?.status,
    ).toBe("completed");
  });

  it("does not auto-activate a read-only subtask thread into an empty pane", () => {
    const draft = createDraftSessionTab("draft:new", context);
    const child = {
      ...threadWithRunningTurn("thread-child"),
      read_only: true as const,
    };
    const state = {
      ...initialState,
      activeContext: context,
      thread: undefined,
      sessionTabs: [draft],
      activeSessionTabID: draft.id,
      threads: [],
      allowThreadAutoActivation: true,
      running: false,
    };

    const next = reduceServerEvent(state, {
      kind: "notification",
      workdir: "/repo",
      message: { method: "thread/started", params: { thread: child } },
    });

    expect(next.thread).toBeUndefined();
    expect(next.threads.map((thread) => thread.id)).toContain(child.id);
  });

  it("re-derives running from the thread that auto-activates into an empty pane", () => {
    const draft = createDraftSessionTab("draft:new", context);
    const idle = threadWithUserTexts(["a normal conversation"]);
    const state = {
      ...initialState,
      activeContext: context,
      thread: undefined,
      sessionTabs: [draft],
      activeSessionTabID: draft.id,
      threads: [],
      allowThreadAutoActivation: true,
      // Stale global flag set by another session's turn.
      running: true,
      status: "running",
    };

    const next = reduceServerEvent(state, {
      kind: "notification",
      workdir: "/repo",
      message: { method: "thread/started", params: { thread: idle } },
    });

    expect(next.thread?.id).toBe(idle.id);
    expect(next.running).toBe(false);
    expect(next.status).toBe("ready");
  });
});

function processNotificationText(): string {
  return '<process_notification>{"process_id":"proc-1","status":"completed"}</process_notification>';
}

function threadWithUserTexts(texts: string[]): Thread {
  return {
    id: "thread-1",
    preview: "preview",
    model_provider: "fake",
    model: "fake-model",
    cwd: "/repo",
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [
      {
        id: "turn-1",
        items_view: "full",
        status: "completed",
        items: texts.map((text, index) => ({
          id: `user-${index + 1}`,
          type: "user_message",
          status: "completed",
          role: "user",
          text
        }))
      }
    ]
  };
}

describe("AppState protocol normalization", () => {
  it("removes a provisional item when its inference attempt is reset", () => {
    const thread = threadWithUserTexts(["inspect"]);
    thread.turns[0].items.push({
      id: "tool-1",
      type: "tool_call",
      status: "in_progress",
      name: "read_file",
    });
    const key = streamTextKey("turn-1", "tool-1", "arguments");
    streamTextStore.set(key, '{"path":"partial');
    const state = {
      ...initialState,
      thread,
      threads: [thread],
    };
    const event = {
      kind: "notification" as const,
      workdir: "/repo",
      message: {
        method: "item/removed",
        params: {
          thread_id: "thread-1",
          turn_id: "turn-1",
          item_id: "tool-1",
        },
      },
    };

    expect(handleStreamingNotification(event, state)).toBe("state");
    expect(streamTextStore.has(key)).toBe(false);
    const next = reduceServerEvent(state, event);
    expect(next.thread?.turns[0].items.map((item) => item.id)).toEqual(["user-1"]);
  });

  it("keeps rendering when an older core starts an empty turn with null items", () => {
    const thread = threadWithUserTexts(["continue"]);
    const next = reduceServerEvent(
      {
        ...initialState,
        activeContext: { kind: "no_project", cwd: "/repo" },
        thread,
        threads: [thread],
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "turn/started",
          params: {
            thread_id: thread.id,
            turn: {
              id: "turn-auto-continue",
              items: null,
              items_view: "full",
              status: "in_progress",
            },
          },
        },
      },
    );

    expect(next.thread?.turns.at(-1)).toMatchObject({
      id: "turn-auto-continue",
      items: [],
    });
    expect(next.running).toBe(true);
  });

  it("records the answer-ready boundary when a terminal assistant item completes", () => {
    const current = threadWithUserTexts(["continue"]);
    const thread: Thread = {
      ...current,
      status: "in_progress",
      turns: [{
        ...current.turns[0],
        id: "turn-ready",
        status: "in_progress",
      }],
    };
    const next = reduceServerEvent(
      {
        ...initialState,
        activeContext: { kind: "no_project", cwd: "/repo" },
        thread,
        threads: [thread],
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "item/completed",
          params: {
            thread_id: thread.id,
            turn_id: "turn-ready",
            completed_at_ms: 1_785_560_400_000,
            item: {
              id: "answer-ready",
              type: "agent_message",
              status: "completed",
              terminal: true,
              text: "done",
            },
          },
        },
      },
    );

    expect(next.thread?.turns[0].status).toBe("in_progress");
    expect(next.thread?.turns[0].answer_ready_at).toBe(
      new Date(1_785_560_400_000).toISOString(),
    );
    expect(activeTurnIsAnswerReady(next.thread)).toBe(true);
  });

  it("normalizes null turn items inside a thread snapshot", () => {
    const current = threadWithUserTexts(["continue"]);
    const next = reduceServerEvent(
      {
        ...initialState,
        activeContext: { kind: "no_project", cwd: "/repo" },
        thread: current,
        threads: [current],
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "thread/updated",
          params: {
            thread: {
              ...current,
              turns: [
                {
                  id: "turn-auto-continue",
                  items: null,
                  items_view: "full",
                  status: "in_progress",
                },
              ],
            },
          },
        },
      },
    );

    expect(next.thread?.turns).toEqual([
      expect.objectContaining({ id: "turn-auto-continue", items: [] }),
    ]);
  });

  it("keeps a live follow-up turn when a stale thread/updated snapshot omits it", () => {
    const current = threadWithUserTexts(["first query"]);
    current.status = "in_progress";
    current.turns = [
      {
        ...current.turns[0],
        status: "completed",
      },
      {
        id: "turn-follow-up",
        items_view: "full",
        status: "in_progress",
        items: [
          {
            id: "follow-up-user",
            type: "user_message",
            status: "completed",
            text: "next question",
          },
        ],
      },
    ];
    const next = reduceServerEvent(
      {
        ...initialState,
        activeContext: { kind: "no_project", cwd: "/repo" },
        thread: current,
        threads: [current],
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "thread/updated",
          params: {
            thread: {
              ...current,
              turns: [current.turns[0]],
            },
          },
        },
      },
    );

    expect(next.thread?.turns.map((turn) => turn.id)).toEqual([
      "turn-1",
      "turn-follow-up",
    ]);
  });

  it("keeps an optimistic first turn when thread/started carries an empty snapshot", () => {
    const current = threadWithUserTexts(["first query"]);
    const next = reduceServerEvent(
      {
        ...initialState,
        activeContext: { kind: "no_project", cwd: "/repo" },
        thread: current,
        threads: [current],
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "thread/started",
          params: {
            thread: {
              ...current,
              turns: [],
            },
          },
        },
      },
    );

    expect(next.thread?.turns).toEqual(current.turns);
    expect(next.threads.find((thread) => thread.id === current.id)?.turns).toEqual(
      current.turns,
    );
  });
});

function sessionTabPrompt(
  tabs: SessionTab[],
  tabID: string,
): string | undefined {
  const tab = tabs.find((candidate) => candidate.id === tabID);
  if (!tab || tab.kind !== "draft" && tab.kind !== "thread") {
    return undefined;
  }
  return tab.prompt;
}

describe("AppState server requests", () => {
  it("rejects unsupported server requests", () => {
    const rejectServerRequest = vi.fn();
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { rejectServerRequest },
    });

    const next = reduceServerEvent(initialState, {
      kind: "server-request",
      workdir: "/tmp/project",
      message: {
        id: "server-request-1",
        method: "unsupported/request",
        params: {
          id: "request-1",
          tool_name: "run_shell",
          risk: "high",
          arguments_preview: "{\"command\":\"printf hi\"}",
          permission: "command.bash",
          permission_patterns: ["printf hi"],
          capability: "command.bash",
          capability_object: "printf hi",
          capability_action: "execute",
          capability_rule: "bash-readonly-echo",
        },
      },
    });

    expect(rejectServerRequest).toHaveBeenCalledWith(
      "server-request-1",
      "unsupported server request: unsupported/request",
    );
    expect(next).toBe(initialState);
    expect(next.status).toBe(initialState.status);
  });
});

describe("queryTextsForThread", () => {
  it("keeps generated query summaries in the visible query history", () => {
    const thread = threadWithUserTexts(["子任务已更新", "真正的用户问题"]);
    thread.turns[0].items[0].read_only = true;
    thread.turns[0].items[0].origin = "plugin";
    thread.turns[0].items[0].presentation_kind = "query_bubble";

    expect(queryTextsForThread(thread)).toEqual(["子任务已更新", "真正的用户问题"]);
  });

  it("skips named and legacy process notifications", () => {
    const thread = threadWithUserTexts([
      "unparseable process payload",
      processNotificationText(),
      "真正的用户问题",
    ]);
    thread.turns[0].items[0].name = PROCESS_NOTIFICATION_NAME;

    expect(queryTextsForThread(thread)).toEqual(["真正的用户问题"]);
  });
});

describe("requestedHandoffIntentForThread", () => {
  it("reads the latest completed request_handoff result", () => {
    const thread = threadWithUserTexts(["真正的用户问题"]);
    thread.turns[0].items.push({
      id: "handoff-request",
      type: "tool_call",
      status: "completed",
      name: "request_handoff",
      result: `{"awaiting_user_configuration":true,"intent":"keep the verified fix"}`,
    });
    expect(requestedHandoffIntentForThread(thread)).toBe("keep the verified fix");
  });
});

describe("summarizeThreadsForSidebar", () => {
  it("overlays aggregate running state onto cached project threads", () => {
    const idle = {
      ...threadWithUserTexts(["background project task"]),
      id: "idle-project-thread",
      cwd: "/repo/project",
      status: "idle" as const,
      turns: [],
    };

    const [summary] = summarizeThreadsForSidebar(
      [idle],
      new Set([idle.id]),
    );

    expect(summary.status).toBe("in_progress");
    expect(isThreadRunning(summary)).toBe(true);
  });

  it("presents an answer-ready thread as settled while execution cleanup continues", () => {
    const thread: Thread = {
      ...threadWithUserTexts(["background project task"]),
      id: "answer-ready-project-thread",
      cwd: "/repo/project",
      status: "in_progress",
      turns: [{
        id: "turn-ready",
        status: "in_progress",
        items_view: "full",
        answer_ready_at: "2026-08-29T04:00:10.000Z",
        items: [{
          id: "answer-ready",
          type: "agent_message",
          status: "completed",
          terminal: true,
          text: "done",
        }],
      }],
    };
    const runningIDs = new Set([thread.id]);

    const [summary] = summarizeThreadsForSidebar(
      [thread],
      presentationRunningThreadIDs([thread], runningIDs),
    );

    expect(summary.status).toBe("idle");
    expect(summary.turns[0]).toMatchObject({
      status: "completed",
      completed_at: "2026-08-29T04:00:10.000Z",
    });
    expect(isThreadRunning(summary)).toBe(false);
    expect(isThreadUnread(summary, undefined)).toBe(true);
    expect(isThreadUnread(thread, undefined)).toBe(true);
    expect(presentationRunningThreadIDs([thread], runningIDs).has(thread.id)).toBe(false);
    expect(runningIDs.has(thread.id)).toBe(true);
  });

  it("keeps project sidebar summaries limited to root sessions", () => {
    const summaries = summarizeThreadsForSidebar([
      {
        ...threadWithUserTexts(["root task"]),
        id: "root-thread",
      },
      {
        ...threadWithUserTexts(["child task"]),
        id: "child-thread",
        parent_id: "root-thread",
      },
      {
        ...threadWithUserTexts(["legacy child task"]),
        id: "legacy-read-only-thread",
        read_only: true,
      },
      {
        ...threadWithUserTexts(["recovered child task"]),
        id: "recovered-child-thread",
        // A recovered worker may retain its agent path even when its parent
        // link was not present in the persisted record.
        agent_path: "/root/inspect",
      },
    ]);

    expect(summaries.map((thread) => thread.id)).toEqual(["root-thread"]);
  });

  it("keeps sidebar thread data free of turn item payloads", () => {
    const [summary] = summarizeThreadsForSidebar([
      threadWithUserTexts(["secret message body"]),
    ]);

    expect(summary.turn_count).toBe(1);
    expect(summary.turns[0]).toEqual({
      id: "turn-1",
      status: "completed",
      started_at: undefined,
      completed_at: undefined,
      duration_ms: undefined,
    });
    expect(JSON.stringify(summary)).not.toContain("secret message body");
  });

  it("groups worktree fork sessions by their base repo", () => {
    const project = {
      id: "project-1",
      name: "project",
      path: "/repo/project",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    };
    const [summary] = summarizeThreadsForSidebar([
      {
        ...threadWithUserTexts(["continue in a worktree"]),
        cwd: "/Users/me/.wuu/worktrees/fork-1/project",
        workspace_kind: "scratch",
        worktree: {
          path: "/Users/me/.wuu/worktrees/fork-1/project",
          base_repo: "/repo/project",
          base_head: "d955824f",
        },
      },
    ]);

    expect(summary.worktree?.base_repo).toBe("/repo/project");
    expect(threadProjectPath(summary)).toBe("/repo/project");
    expect(threadBelongsToProject(summary, project)).toBe(true);
    expect(isScratchThread(summary, [project])).toBe(false);
  });

  it("keeps sessions with a project after its path changes", () => {
    const project = {
      id: "project-1",
      name: "project",
      path: "/repo/project-moved",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    };
    const [summary] = summarizeThreadsForSidebar([
      {
        ...threadWithUserTexts(["before the move"]),
        cwd: "/repo/project-old",
        workspace_id: project.id,
        workspace_kind: "project",
      },
    ]);

    expect(summary.workspace_id).toBe(project.id);
    expect(threadBelongsToProject(summary, project)).toBe(true);
    expect(isScratchThread(summary, [project])).toBe(false);
  });
});

describe("mergeSidebarThread", () => {
  it("preserves a known title and preview when an incoming snapshot omits them", () => {
    const existing = threadWithUserTexts(["今天我在这里面提交的内容"]);
    existing.title = "已经生成的标题";
    existing.preview = "今天我在这里面提交的内容";
    const incoming = { ...existing, title: "", preview: "" };

    const merged = mergeSidebarThread(existing, incoming);

    expect(merged.title).toBe(existing.title);
    expect(merged.preview).toBe(existing.preview);
  });
});

describe("resolveThreadRuntimeContext", () => {
  const project: DesktopProject = {
    id: "project-1",
    name: "project",
    path: "/repo/project",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };

  it("resolves a project thread to that project's context", () => {
    const thread = threadWithUserTexts(["hello"]);
    thread.cwd = project.path;
    thread.workspace_kind = "project";

    expect(resolveThreadRuntimeContext(thread, [project])).toEqual({
      kind: "project",
      project_id: project.id,
      cwd: project.path,
    });
  });

  it("resolves a scratch thread to a no_project context rooted at its own cwd", () => {
    const thread = threadWithUserTexts(["hello"]);
    thread.cwd = "/Users/me/.wuu/scratch/2026-07-03";
    thread.workspace_kind = "scratch";

    expect(resolveThreadRuntimeContext(thread, [project])).toEqual({
      kind: "no_project",
      cwd: thread.cwd,
    });
  });

  it("has no registered project to match against and falls back to no_project", () => {
    const thread = threadWithUserTexts(["hello"]);
    thread.cwd = "/Users/me/some/random/cwd";
    thread.workspace_kind = "scratch";

    expect(resolveThreadRuntimeContext(thread, [])).toEqual({
      kind: "no_project",
      cwd: thread.cwd,
    });
  });

  it("prefers a cwd match against a registered project over a stale workspace_kind label, consistent with isScratchThread", () => {
    // Same fixture shape as the worktree fork session case above:
    // cwd belongs to the project, but workspace_kind still says "scratch"
    // (e.g. a legacy thread from before the project was registered at that
    // path). isScratchThread treats the cwd match as authoritative and
    // returns false (not scratch); resolveThreadRuntimeContext must agree
    // and resolve to the project's context rather than no_project.
    const thread = threadWithUserTexts(["continue in a worktree"]);
    thread.cwd = "/Users/me/.wuu/worktrees/fork-1/project";
    thread.workspace_kind = "scratch";
    thread.worktree = {
      path: "/Users/me/.wuu/worktrees/fork-1/project",
      base_repo: project.path,
      base_head: "d955824f",
    };

    expect(isScratchThread(thread, [project])).toBe(false);
    expect(resolveThreadRuntimeContext(thread, [project])).toEqual({
      kind: "project",
      project_id: project.id,
      cwd: project.path,
    });
  });
});

describe("workspacePanelContext", () => {
  const projectContext: RuntimeContext = {
    kind: "project",
    project_id: "project-1",
    cwd: "/repo/project",
  };

  it("returns activeContext unchanged when there is no active thread", () => {
    expect(workspacePanelContext(projectContext, undefined)).toBe(
      projectContext,
    );
  });

  it("returns the very same activeContext reference when the thread's cwd matches it", () => {
    const thread = threadWithUserTexts(["hello"]);
    thread.cwd = projectContext.cwd;

    expect(workspacePanelContext(projectContext, thread)).toBe(projectContext);
  });

  it("returns activeContext unchanged when the thread has no cwd", () => {
    const thread = threadWithUserTexts(["hello"]);
    thread.cwd = "";

    expect(workspacePanelContext(projectContext, thread)).toBe(projectContext);
  });

  it("overrides cwd to the thread's own cwd when it differs, preserving kind/project_id (worktree fork)", () => {
    // Mirrors a thread/fork "worktree" thread: resolveThreadRuntimeContext
    // resolves it to the base project's context (threadProjectPath prefers
    // worktree.base_repo), but the thread itself runs out of the worktree
    // directory. The workspace panel should follow the thread.
    const thread = threadWithUserTexts(["continue in a worktree"]);
    thread.cwd = "/Users/me/.wuu/worktrees/fork-1/project";
    thread.worktree = {
      path: thread.cwd,
      base_repo: projectContext.cwd,
      base_head: "d955824f",
    };

    expect(workspacePanelContext(projectContext, thread)).toEqual({
      kind: "project",
      project_id: projectContext.project_id,
      cwd: thread.cwd,
    });
  });

  it("falls back to a no_project context rooted at the thread's cwd when there is no activeContext", () => {
    const thread = threadWithUserTexts(["hello"]);
    thread.cwd = "/Users/me/.wuu/scratch/2026-07-03";

    expect(workspacePanelContext(undefined, thread)).toEqual({
      kind: "no_project",
      cwd: thread.cwd,
    });
  });
});

describe("openForkThreadAsPrimary", () => {
  const context: RuntimeContext = {
    kind: "project",
    project_id: "project-1",
    cwd: "/repo",
  };

  it("opens a fork as the primary conversation instead of creating a split", () => {
    const source: Thread = {
      ...threadWithUserTexts(["source prompt"]),
      id: "source-thread",
      preview: "source",
    };
    const fork: Thread = {
      ...threadWithUserTexts(["source prompt"]),
      id: "fork-thread",
      preview: "fork",
      forked_from_id: source.id,
    };

    const next = openForkThreadAsPrimary(
      {
        ...initialState,
        activeContext: context,
        thread: source,
        activePane: "primary",
        activeSessionTabID: threadSessionTabID(source.id),
        sessionTabs: [createThreadSessionTab(source, context)],
        threads: [source],
        status: "ready",
      },
      {
        sourceThread: source,
        forkThread: fork,
        context,
        sourceDraft: {
          prompt: "keep the source draft",
          images: [],
          files: [],
        },
      },
    );

    expect(next.thread?.id).toBe(fork.id);
    expect(next.secondaryThread).toBeUndefined();
    expect(next.activePane).toBe("primary");
    expect(next.activeSessionTabID).toBe(threadSessionTabID(fork.id));
    expect(sessionTabPrompt(next.sessionTabs, threadSessionTabID(source.id))).toBe(
      "keep the source draft",
    );
  });

  it("collapses an existing split and keeps both original pane drafts in tabs", () => {
    const source: Thread = {
      ...threadWithUserTexts(["source prompt"]),
      id: "source-thread",
      preview: "source",
    };
    const secondary: Thread = {
      ...threadWithUserTexts(["secondary prompt"]),
      id: "secondary-thread",
      preview: "secondary",
    };
    const fork: Thread = {
      ...threadWithUserTexts(["source prompt"]),
      id: "fork-thread",
      preview: "fork",
      forked_from_id: source.id,
    };

    const next = openForkThreadAsPrimary(
      {
        ...initialState,
        activeContext: context,
        thread: source,
        secondaryThread: secondary,
        activePane: "secondary",
        activeSessionTabID: threadSessionTabID(secondary.id),
        sessionTabs: [
          createThreadSessionTab(source, context),
          createThreadSessionTab(secondary, context),
        ],
        threads: [source, secondary],
        status: "ready",
      },
      {
        sourceThread: source,
        forkThread: fork,
        context,
        sourceDraft: {
          prompt: "left draft",
          images: [],
          files: [],
        },
        splitDrafts: {
          primary: {
            prompt: "left draft",
            images: [],
            files: [],
          },
          secondary: {
            prompt: "right draft",
            images: [],
            files: [],
          },
        },
      },
    );

    expect(next.thread?.id).toBe(fork.id);
    expect(next.secondaryThread).toBeUndefined();
    expect(next.activePane).toBe("primary");
    expect(
      sessionTabPrompt(next.sessionTabs, threadSessionTabID(source.id)),
    ).toBe("left draft");
    expect(
      sessionTabPrompt(next.sessionTabs, threadSessionTabID(secondary.id)),
    ).toBe("right draft");
  });
});

describe("worktree thread context matching", () => {
  it("applies worktree fork updates while the base repo project is active", () => {
    const worktreeThread: Thread = {
      ...threadWithUserTexts(["continue in worktree"]),
      id: "worktree-thread",
      cwd: "/Users/me/.wuu/worktrees/fork-1/project",
      preview: "before",
      worktree: {
        path: "/Users/me/.wuu/worktrees/fork-1/project",
        base_repo: "/repo",
        base_head: "d955824f",
      },
    };
    const updatedThread: Thread = {
      ...worktreeThread,
      preview: "after",
    };

    const next = reduceServerEvent(
      {
        ...initialState,
        activeContext: {
          kind: "project",
          project_id: "project-1",
          cwd: "/repo",
        },
        thread: worktreeThread,
        threads: [worktreeThread],
        status: "ready",
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "thread/updated",
          params: { thread: updatedThread },
        },
      },
    );

    expect(next.thread?.preview).toBe("after");
  });
});

describe("AppState token usage", () => {
  it("routes live usage telemetry around the App reducer", () => {
    const handling = handleStreamingNotification({
      kind: "notification",
      workdir: "/repo",
      message: {
        method: "turn/usage",
        params: {
          thread_id: "thread-1",
          turn_id: "turn-1",
          input_tokens: 100,
          output_tokens: 20,
        },
      },
    }, initialState);

    expect(handling).toBe("skip");
  });

  it("initializes token usage state before the first usage update", () => {
    expect(initialState.turnTokenUsage).toEqual({});
    expect(initialState.turnRequestContext).toEqual({});
    expect(activeTurnTokenSpeed(initialState, "turn-1")).toBe(0);
  });

  it("derives token speed from cumulative output-token samples", () => {
    const first = appendTurnTokenSample(
      initialState,
      "turn-1",
      "thread-1",
      10,
      2,
      0,
      0,
      1_000,
    );
    const second = appendTurnTokenSample(
      first,
      "turn-1",
      "thread-1",
      10,
      22,
      4,
      8,
      2_000,
    );

    expect(activeTurnTokenSpeed(second, "turn-1")).toBe(20);
    expect(activeTurnTokenSpeedSnapshot(second, "turn-1").source).toBe("real");
    expect(second.turnTokenUsage["turn-1"].cacheCreationTokens).toBe(4);
    expect(second.turnTokenUsage["turn-1"].cacheReadTokens).toBe(8);
  });

  it("derives live token speed from streamed model output deltas", () => {
    const first = appendStreamingTokenSample(
      initialState,
      {
        thread_id: "thread-1",
        turn_id: "turn-1",
        delta: "aaaaaaaa",
      },
      1_000,
    );
    const second = appendStreamingTokenSample(
      first,
      {
        thread_id: "thread-1",
        turn_id: "turn-1",
        delta: "bbbbbbbb",
      },
      2_000,
    );

    expect(activeTurnTokenSpeed(second, "turn-1")).toBe(2);
    expect(activeTurnTokenSpeedSnapshot(second, "turn-1").source).toBe("estimated");
    expect(second.turnTokenUsage["turn-1"].outputTokens).toBe(0);
  });

  it("discards estimated samples when real provider usage arrives", () => {
    const estimated = appendStreamingTokenSample(
      initialState,
      {
        thread_id: "thread-1",
        turn_id: "turn-1",
        delta: "aaaaaaaaaaaaaaaa",
      },
      1_000,
    );
    const real = appendTurnTokenSample(
      estimated,
      "turn-1",
      "thread-1",
      10,
      3,
      0,
      0,
      1_500,
    );

    expect(activeTurnTokenSpeedSnapshot(real, "turn-1").source).toBe("real");
    expect(real.turnTokenUsage["turn-1"].samples).toEqual([
      { tokens: 3, at: 1_500 },
    ]);
  });

  it("attaches request context diagnostics to the latest context usage", () => {
    const thread = threadWithUserTexts(["hi"]);
    const stateWithUsage = appendTurnTokenSample(
      {
        ...initialState,
        thread,
      },
      "turn-1",
      "thread-1",
      100,
      10,
      0,
      0,
      1_000,
      200_000,
      "fake-model",
      12_000,
    );
    const next = reduceServerEvent(stateWithUsage, {
      kind: "notification",
      workdir: "/repo",
      message: {
        method: "turn/event",
        params: {
          thread_id: "thread-1",
          turn_id: "turn-1",
          event: {
            request_context: {
              step_index: 0,
              message_count: 8,
              stable_prefix: 5,
              turn_prefix: 6,
              transient_messages: 1,
              hidden_messages: 1,
              tool_count: 14,
              stable_prefix_bytes: 3200,
              turn_prefix_bytes: 4100,
              message_bytes: 9800,
              dynamic_bytes: 1200,
              tool_schema_bytes: 22000,
              prompt_cache_key: "thread-1",
              stable_prefix_hash: "stable",
              turn_prefix_hash: "turn",
              tool_surface_hash: "tools",
            },
          },
        },
      },
    });

    const usage = latestContextUsageForThread(next, thread);
    expect(usage?.requestContext?.stablePrefix).toBe(5);
    expect(usage?.requestContext?.turnPrefix).toBe(6);
    expect(usage?.requestContext?.toolCount).toBe(14);
    expect(usage?.requestContext?.promptCacheKey).toBe("thread-1");
  });

  it("bounds transient per-turn telemetry during long app sessions", () => {
    let state = initialState;
    const totalTurns = RETAINED_TURN_TELEMETRY_LIMIT + 20;
    for (let index = 0; index < totalTurns; index += 1) {
      const turnID = `turn-${index}`;
      state = appendTurnTokenSample(
        state,
        turnID,
        "thread-1",
        index,
        index,
        0,
        0,
        index,
      );
      state = reduceServerEvent(state, {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "turn/event",
          params: {
            thread_id: "thread-1",
            turn_id: turnID,
            event: {
              request_context: {
                step_index: index,
                message_count: index + 1,
                stable_prefix: index,
                turn_prefix: index,
              },
            },
          },
        },
      });
    }

    expect(Object.keys(state.turnTokenUsage)).toHaveLength(
      RETAINED_TURN_TELEMETRY_LIMIT,
    );
    expect(Object.keys(state.turnRequestContext)).toHaveLength(
      RETAINED_TURN_TELEMETRY_LIMIT,
    );
    expect(state.turnTokenUsage["turn-0"]).toBeUndefined();
    expect(state.turnRequestContext["turn-0"]).toBeUndefined();
    expect(state.turnTokenUsage[`turn-${totalTurns - 1}`]).toBeDefined();
    expect(state.turnRequestContext[`turn-${totalTurns - 1}`]).toBeDefined();

    const persistedThread: Thread = {
      ...threadWithUserTexts(["hi"]),
      model: "fake-model",
      turns: [
        {
          id: "turn-0",
          items: [],
          items_view: "full",
          status: "completed",
          input_tokens: 120,
          context_tokens: 80,
          usage_model: "fake-model",
        },
      ],
    };
    expect(
      latestContextUsageForThread(state, persistedThread, {
        contextWindowTokens: 200_000,
      }),
    ).toMatchObject({ turnID: "turn-0", used: 80, window: 200_000 });
  });

  it("surfaces websocket to http fallback as a static stream status", () => {
    const thread: Thread = {
      ...threadWithUserTexts(["hi"]),
      turns: [
        {
          id: "turn-1",
          items_view: "full",
          status: "in_progress",
          items: [],
        },
      ],
    };
    const state = {
      ...initialState,
      thread,
      threads: [thread],
      running: true,
    };

    const fallback = reduceServerEvent(state, {
      kind: "notification",
      workdir: "/repo",
      message: {
        method: "turn/event",
        params: {
          thread_id: "thread-1",
          turn_id: "turn-1",
          event: {
            provider_state: {
              diagnostic: "provider_transport_failure",
              transport: "http",
              failed_transport: "websocket",
              fallback_transport: "http",
              fallback_active: true,
              transport_failure_phase: "before_message_stream_start",
            },
          },
        },
      },
    });

    expect(turnStreamStatusForThread(fallback, fallback.thread)).toEqual({
      text: "WebSocket 不可用，已切到 HTTP",
      liveProgress: false,
    });
  });

  it("surfaces transport interruption after message stream start", () => {
    const thread: Thread = {
      ...threadWithUserTexts(["hi"]),
      turns: [
        {
          id: "turn-1",
          items_view: "full",
          status: "in_progress",
          items: [],
        },
      ],
    };
    const state = {
      ...initialState,
      thread,
      threads: [thread],
      running: true,
    };

    const interrupted = reduceServerEvent(state, {
      kind: "notification",
      workdir: "/repo",
      message: {
        method: "turn/event",
        params: {
          thread_id: "thread-1",
          turn_id: "turn-1",
          event: {
            provider_state: {
              diagnostic: "provider_transport_failure",
              transport: "websocket",
              failed_transport: "websocket",
              events_emitted: true,
              transport_failure_phase: "after_message_stream_start",
            },
          },
        },
      },
    });

    expect(turnStreamStatusForThread(interrupted, interrupted.thread)).toEqual({
      text: "WebSocket 消息流中断",
      liveProgress: false,
    });
  });

  it("clears the stream status when the turn settles", () => {
    const thread: Thread = {
      ...threadWithUserTexts(["hi"]),
      turns: [
        {
          id: "turn-1",
          items_view: "full",
          status: "in_progress",
          items: [],
        },
      ],
    };
    const state = reduceServerEvent(
      {
        ...initialState,
        thread,
        threads: [thread],
        running: true,
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "turn/event",
          params: {
            thread_id: "thread-1",
            turn_id: "turn-1",
            event: {
              provider_state: {
                diagnostic: "provider_transport_failure",
                transport: "websocket",
                failed_transport: "websocket",
                events_emitted: true,
                transport_failure_phase: "after_message_stream_start",
              },
            },
          },
        },
      },
    );

    expect(turnStreamStatusForThread(state, state.thread)).toBeDefined();

    const settledTurn = {
      ...thread.turns[0],
      status: "failed" as const,
    };
    const settled = reduceServerEvent(state, {
      kind: "notification",
      workdir: "/repo",
      message: {
        method: "turn/error",
        params: {
          thread_id: "thread-1",
          turn: settledTurn,
        },
      },
    });

    expect(turnStreamStatusForThread(settled, settled.thread)).toBeUndefined();
  });
});

describe("AppState stream cache lifecycle", () => {
  afterEach(() => {
    streamTextStore.clearItem("turn-1", "agent-1");
    streamTextStore.clearItem("turn-1", "reasoning-1");
    streamTextStore.clearItem("turn-bg", "agent-bg");
  });

  it("keeps visible text through an empty replace while resetting the next delta base", () => {
    const key = streamTextKey("turn-1", "agent-1", "text");
    const state = {
      ...initialState,
      thread: threadWithUserTexts(["hi"]),
    };

    handleStreamingNotification(
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "item/agentMessage/delta",
          params: {
            thread_id: "thread-1",
            turn_id: "turn-1",
            item_id: "agent-1",
            delta: "stale partial",
          },
        },
      },
      state,
    );
    handleStreamingNotification(
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "item/agentMessage/replace",
          params: {
            thread_id: "thread-1",
            turn_id: "turn-1",
            item_id: "agent-1",
            text: "",
          },
        },
      },
      state,
    );

    expect(streamTextStore.get(key)).toBe("stale partial");

    handleStreamingNotification(
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "item/agentMessage/delta",
          params: {
            thread_id: "thread-1",
            turn_id: "turn-1",
            item_id: "agent-1",
            delta: "fresh answer",
          },
        },
      },
      state,
    );

    expect(streamTextStore.get(key)).toBe("fresh answer");
  });

  it("keeps accumulated reasoning when item/started carries a stale snapshot", () => {
    const key = streamTextKey("turn-1", "reasoning-1", "text");
    const state = {
      ...initialState,
      thread: threadWithUserTexts(["hi"]),
    };
    streamTextStore.set(key, "Let me think about that.");

    for (const text of ["", "Let me think"]) {
      const handling = handleStreamingNotification(
        {
          kind: "notification",
          workdir: "/repo",
          message: {
            method: "item/started",
            params: {
              thread_id: "thread-1",
              turn_id: "turn-1",
              item: {
                id: "reasoning-1",
                type: "reasoning",
                status: "in_progress",
                text,
              },
            },
          },
        },
        state,
      );

      expect(handling).toBe("state");
      expect(streamTextStore.get(key)).toBe("Let me think about that.");
    }
  });

  it("keeps stream deltas for a known background thread", () => {
    const key = streamTextKey("turn-bg", "agent-bg", "text");
    const activeThread = threadWithUserTexts(["active"]);
    const backgroundThread = {
      ...threadWithUserTexts(["background"]),
      id: "thread-bg",
    };

    const handling = handleStreamingNotification(
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "item/agentMessage/delta",
          params: {
            thread_id: "thread-bg",
            turn_id: "turn-bg",
            item_id: "agent-bg",
            delta: "background text",
          },
        },
      },
      {
        ...initialState,
        thread: activeThread,
        threads: [activeThread, backgroundThread],
      },
    );

    expect(handling).toBe("background-stream");
    expect(streamTextStore.get(key)).toBe("background text");
  });

  it("syncs completed snapshots for a known background thread", () => {
    const raf = installManualRAF();
    const key = streamTextKey("turn-bg", "agent-bg", "text");
    const activeThread = threadWithUserTexts(["active"]);
    const backgroundThread = {
      ...threadWithUserTexts(["background"]),
      id: "thread-bg",
    };
    streamTextStore.set(key, "partial");

    try {
      const handling = handleStreamingNotification(
        {
          kind: "notification",
          workdir: "/repo",
          message: {
            method: "item/completed",
            params: {
              thread_id: "thread-bg",
              turn_id: "turn-bg",
              item: {
                id: "agent-bg",
                type: "agent_message",
                status: "completed",
                text: "partial complete",
              },
            },
          },
        },
        {
          ...initialState,
          thread: activeThread,
          threads: [activeThread, backgroundThread],
        },
      );

      expect(handling).toBe("state");
      expect(streamTextStore.get(key)).toBe("partial complete");
      raf.flush();
      expect(streamTextStore.has(key)).toBe(false);
    } finally {
      raf.restore();
    }
  });

  it("releases completed agent text from the stream cache once a final snapshot exists", () => {
    const raf = installManualRAF();
    const key = streamTextKey("turn-1", "agent-1", "text");
    streamTextStore.set(key, "Final answer");
    try {
      const handling = handleStreamingNotification(
        {
          kind: "notification",
          workdir: "/repo",
          message: {
            method: "item/completed",
            params: {
              thread_id: "thread-1",
              turn_id: "turn-1",
              item: {
                id: "agent-1",
                type: "agent_message",
                status: "completed",
                text: "Final answer",
              },
            },
          },
        },
        {
          ...initialState,
          thread: threadWithUserTexts(["hi"]),
        },
      );

      expect(handling).toBe("state");
      expect(streamTextStore.has(key)).toBe(true);
      raf.flush();
      expect(streamTextStore.has(key)).toBe(false);
    } finally {
      raf.restore();
    }
  });

  it("keeps completed agent text cached while the completed snapshot is empty", () => {
    const raf = installManualRAF();
    const key = streamTextKey("turn-1", "agent-1", "text");
    streamTextStore.set(key, "Final answer");
    try {
      handleStreamingNotification(
        {
          kind: "notification",
          workdir: "/repo",
          message: {
            method: "item/completed",
            params: {
              thread_id: "thread-1",
              turn_id: "turn-1",
              item: {
                id: "agent-1",
                type: "agent_message",
                status: "completed",
                text: "",
              },
            },
          },
        },
        {
          ...initialState,
          thread: threadWithUserTexts(["hi"]),
        },
      );

      raf.flush();
      expect(streamTextStore.has(key)).toBe(true);
      expect(streamTextStore.get(key)).toBe("Final answer");
    } finally {
      raf.restore();
    }
  });

  it("keeps completed agent text cached while the completed snapshot is behind the stream", () => {
    const raf = installManualRAF();
    const key = streamTextKey("turn-1", "agent-1", "text");
    streamTextStore.set(key, "Final answer");
    try {
      handleStreamingNotification(
        {
          kind: "notification",
          workdir: "/repo",
          message: {
            method: "item/completed",
            params: {
              thread_id: "thread-1",
              turn_id: "turn-1",
              item: {
                id: "agent-1",
                type: "agent_message",
                status: "completed",
                text: "Final",
              },
            },
          },
        },
        {
          ...initialState,
          thread: threadWithUserTexts(["hi"]),
        },
      );

      raf.flush();
      expect(streamTextStore.has(key)).toBe(true);
      expect(streamTextStore.get(key)).toBe("Final answer");
    } finally {
      raf.restore();
    }
  });

  it("releases buffered streams when a completed turn carries final item snapshots", () => {
    const textKey = streamTextKey("turn-1", "agent-1", "text");
    const resultKey = streamTextKey("turn-1", "agent-1", "result");
    streamTextStore.set(textKey, "Final answer");
    streamTextStore.set(resultKey, "Tool result");

    reduceServerEvent(
      {
        ...initialState,
        activeContext: { kind: "no_project", cwd: "/repo" },
        thread: threadWithUserTexts(["hi"]),
        threads: [threadWithUserTexts(["hi"])],
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "turn/completed",
          params: {
            thread_id: "thread-1",
            turn: {
              id: "turn-1",
              items_view: "full",
              status: "completed",
              items: [
                {
                  id: "agent-1",
                  type: "agent_message",
                  status: "completed",
                  text: "Final answer",
                  result: "Tool result",
                },
              ],
            },
          },
        },
      },
    );

    expect(streamTextStore.has(textKey)).toBe(false);
    expect(streamTextStore.has(resultKey)).toBe(false);
  });

  it("keeps buffered streams when a completed turn snapshot is behind the stream", () => {
    const textKey = streamTextKey("turn-1", "agent-1", "text");
    const resultKey = streamTextKey("turn-1", "agent-1", "result");
    streamTextStore.set(textKey, "Final answer");
    streamTextStore.set(resultKey, "Tool result");

    reduceServerEvent(
      {
        ...initialState,
        activeContext: { kind: "no_project", cwd: "/repo" },
        thread: threadWithUserTexts(["hi"]),
        threads: [threadWithUserTexts(["hi"])],
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "turn/completed",
          params: {
            thread_id: "thread-1",
            turn: {
              id: "turn-1",
              items_view: "full",
              status: "completed",
              items: [
                {
                  id: "agent-1",
                  type: "agent_message",
                  status: "completed",
                  text: "Final",
                  result: "Tool",
                },
              ],
            },
          },
        },
      },
    );

    expect(streamTextStore.has(textKey)).toBe(true);
    expect(streamTextStore.get(textKey)).toBe("Final answer");
    expect(streamTextStore.has(resultKey)).toBe(true);
    expect(streamTextStore.get(resultKey)).toBe("Tool result");
  });
});

describe("turn token speed", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 0, 1, 0, 0, 0));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns 0 when there are no samples", () => {
    expect(activeTurnTokenSpeed(initialState, "turn-1")).toBe(0);
  });

  it("returns 0 with fewer than two samples", () => {
    const state = appendTurnTokenSample(
      initialState,
      "turn-1",
      "thread-1",
      0,
      100,
      0,
      0,
      Date.now(),
    );
    expect(activeTurnTokenSpeed(state, "turn-1")).toBe(0);
  });

  it("computes tok/s from the oldest to the newest sample", () => {
    let state = appendTurnTokenSample(
      initialState,
      "turn-1",
      "thread-1",
      0,
      100,
      0,
      0,
      Date.now(),
    );
    vi.advanceTimersByTime(500);
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      0,
      140,
      0,
      0,
      Date.now(),
    );
    expect(activeTurnTokenSpeed(state, "turn-1")).toBeCloseTo(80, 0);
  });

  it("ignores unchanged usage snapshots while tools are running", () => {
    let state = appendTurnTokenSample(
      initialState,
      "turn-1",
      "thread-1",
      0,
      100,
      0,
      0,
      Date.now(),
    );
    vi.advanceTimersByTime(500);
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      0,
      140,
      0,
      0,
      Date.now(),
    );

    vi.advanceTimersByTime(2500);
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      20,
      140,
      0,
      0,
      Date.now(),
    );

    const speed = activeTurnTokenSpeedSnapshot(state, "turn-1");
    expect(speed.tokensPerSecond).toBeCloseTo(80, 0);
    expect(speed.sampledAt).toBe(new Date(2026, 0, 1, 0, 0, 0, 500).getTime());
    expect(state.turnTokenUsage["turn-1"].samples).toEqual([
      { tokens: 100, at: new Date(2026, 0, 1, 0, 0, 0).getTime() },
      { tokens: 140, at: new Date(2026, 0, 1, 0, 0, 0, 500).getTime() },
    ]);
  });

  it("drops samples older than the 2s window", () => {
    let state = appendTurnTokenSample(
      initialState,
      "turn-1",
      "thread-1",
      0,
      100,
      0,
      0,
      Date.now(),
    );
    vi.advanceTimersByTime(2500);
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      0,
      200,
      0,
      0,
      Date.now(),
    );
    expect(state.turnTokenUsage["turn-1"].samples).toHaveLength(1);
    expect(activeTurnTokenSpeed(state, "turn-1")).toBe(0);
  });
});

describe("AppState unread tracking", () => {
  function makeThreadWithTurns(
    threadID: string,
    turns: Array<{
      id: string;
      status: "completed" | "in_progress" | "failed" | "interrupted";
    }>,
  ): Thread {
    return {
      id: threadID,
      preview: "",
      model_provider: "fake",
      model: "fake-model",
      cwd: "/tmp",
      status: "idle",
      created_at: "2026-06-18T00:00:00Z",
      updated_at: "2026-06-18T00:00:00Z",
      turns: turns.map((t) => ({
        id: t.id,
        items: [],
        items_view: "full" as const,
        status: t.status,
      })),
    };
  }

  it("latestCompletedTurnID returns the most recent non-in_progress turn", () => {
    const thread = makeThreadWithTurns("thread-1", [
      { id: "turn-1", status: "completed" },
      { id: "turn-2", status: "completed" },
      { id: "turn-3", status: "completed" },
    ]);
    expect(latestCompletedTurnID(thread)).toBe("turn-3");
  });

  it("latestCompletedTurnID returns undefined when the latest turn is in_progress", () => {
    const thread = makeThreadWithTurns("thread-1", [
      { id: "turn-1", status: "completed" },
      { id: "turn-2", status: "in_progress" },
    ]);
    expect(latestCompletedTurnID(thread)).toBeUndefined();
  });

  it("latestCompletedTurnID treats an answer-ready in-progress tail as completed", () => {
    const thread = {
      ...makeThreadWithTurns("thread-1", [
        { id: "turn-1", status: "completed" },
        { id: "turn-2", status: "in_progress" },
      ]),
      latest_completed_turn_id: "turn-1",
    };
    thread.turns[1].answer_ready_at = "2026-08-29T04:00:10.000Z";
    thread.turns[1].items = [{
      id: "answer-ready",
      type: "agent_message",
      status: "completed",
      terminal: true,
      text: "done",
    }];
    expect(latestCompletedTurnID(thread)).toBe("turn-2");
    expect(isThreadUnread(thread, "turn-1")).toBe(true);
    expect(isThreadUnread(thread, "turn-2")).toBe(false);
  });

  it("latestCompletedTurnID returns undefined for an empty thread", () => {
    const thread = makeThreadWithTurns("thread-1", []);
    expect(latestCompletedTurnID(thread)).toBeUndefined();
  });

  it("latestCompletedTurnID uses the global summary when turns are not loaded", () => {
    const thread = {
      ...makeThreadWithTurns("thread-1", []),
      latest_completed_turn_id: "turn-global-2",
    };
    expect(latestCompletedTurnID(thread)).toBe("turn-global-2");
    expect(isThreadUnread(thread, undefined)).toBe(true);
    expect(isThreadUnread(thread, "turn-global-2")).toBe(false);
  });

  it("isThreadUnread returns true for a thread with a new completed turn", () => {
    const thread = makeThreadWithTurns("thread-1", [
      { id: "turn-1", status: "completed" },
    ]);
    expect(isThreadUnread(thread, undefined)).toBe(true);
  });

  it("isThreadUnread returns false when lastViewed matches the latest turn", () => {
    const thread = makeThreadWithTurns("thread-1", [
      { id: "turn-1", status: "completed" },
    ]);
    expect(isThreadUnread(thread, "turn-1")).toBe(false);
  });

  it("isThreadUnread returns false for a running thread", () => {
    const thread = makeThreadWithTurns("thread-1", [
      { id: "turn-1", status: "in_progress" },
    ]);
    expect(isThreadUnread(thread, undefined)).toBe(false);
  });

  it("does not re-flag unread when an answer-ready turn later completes", () => {
    const answerReady = {
      ...makeThreadWithTurns("thread-1", [
        { id: "turn-1", status: "in_progress" },
      ]),
      status: "in_progress" as const,
    };
    answerReady.turns[0].answer_ready_at = "2026-08-29T04:00:10.000Z";
    answerReady.turns[0].items = [{
      id: "answer-ready",
      type: "agent_message",
      status: "completed",
      terminal: true,
      text: "done",
    }];
    const viewed = markThreadTurnsViewed({
      ...initialState,
      thread: answerReady,
      threads: [answerReady],
    }, "thread-1");
    expect(viewed.lastViewedTurnByThreadID["thread-1"]).toBe("turn-1");

    const completed = {
      ...answerReady,
      status: "idle" as const,
      turns: [{
        ...answerReady.turns[0],
        status: "completed" as const,
      }],
    };
    expect(isThreadUnread(completed, viewed.lastViewedTurnByThreadID["thread-1"])).toBe(false);
    expect(markThreadTurnsViewed({
      ...viewed,
      thread: completed,
      threads: [completed],
    }, "thread-1").lastViewedTurnByThreadID["thread-1"]).toBe("turn-1");
  });

  it("isThreadUnread returns false for an empty thread", () => {
    const thread = makeThreadWithTurns("thread-1", []);
    expect(isThreadUnread(thread, undefined)).toBe(false);
  });

  it("markThreadTurnsViewed records the latest completed turn ID", () => {
    const thread = makeThreadWithTurns("thread-1", [
      { id: "turn-1", status: "completed" },
    ]);
    const state = {
      ...initialState,
      thread,
      threads: [thread],
    };
    const next = markThreadTurnsViewed(state, "thread-1");
    expect(next.lastViewedTurnByThreadID["thread-1"]).toBe("turn-1");
  });

  it("markThreadTurnsViewed is a no-op when already current", () => {
    const thread = makeThreadWithTurns("thread-1", [
      { id: "turn-1", status: "completed" },
    ]);
    const state = {
      ...initialState,
      thread,
      threads: [thread],
      lastViewedTurnByThreadID: { "thread-1": "turn-1" },
    };
    expect(markThreadTurnsViewed(state, "thread-1")).toBe(state);
  });

  it("markThreadTurnsViewed is a no-op for a running thread", () => {
    const thread = makeThreadWithTurns("thread-1", [
      { id: "turn-1", status: "in_progress" },
    ]);
    const state = {
      ...initialState,
      thread,
      threads: [thread],
    };
    expect(markThreadTurnsViewed(state, "thread-1")).toBe(state);
  });

  it("markThreadTurnsViewed records an answer-ready in-progress turn", () => {
    const thread = {
      ...makeThreadWithTurns("thread-1", [
        { id: "turn-1", status: "in_progress" },
      ]),
      status: "in_progress" as const,
    };
    thread.turns[0].answer_ready_at = "2026-08-29T04:00:10.000Z";
    thread.turns[0].items = [{
      id: "answer-ready",
      type: "agent_message",
      status: "completed",
      terminal: true,
      text: "done",
    }];
    const next = markThreadTurnsViewed({
      ...initialState,
      thread,
      threads: [thread],
    }, "thread-1");
    expect(next.lastViewedTurnByThreadID["thread-1"]).toBe("turn-1");
  });

});

describe("AppState sortThreads (sidebar order)", () => {
  function makeSortableThread(args: {
    id: string;
    createdAt: string;
    updatedAt: string;
    status?: "idle" | "in_progress";
    turns?: Array<{ id: string; status: "completed" | "in_progress" | "failed" | "interrupted" }>;
    childAgents?: Agent[];
    pinned?: boolean;
    archived?: boolean;
    readOnly?: boolean;
  }): Thread {
    return {
      id: args.id,
      preview: "",
      model_provider: "fake",
      model: "fake-model",
      cwd: "/tmp",
      status: args.status ?? "idle",
      created_at: args.createdAt,
      updated_at: args.updatedAt,
      archived: args.archived,
      read_only: args.readOnly,
      child_agents: args.childAgents,
      pinned: args.pinned,
      turns: (args.turns ?? []).map((turn) => ({
        id: turn.id,
        items: [],
        items_view: "full" as const,
        status: turn.status,
      })),
    };
  }

  it("keeps running threads in created_at order regardless of updated_at jitter", () => {
    // Two in_progress threads. updated_at keeps bumping while the model
    // streams; created_at never changes. The old single-key sort shuffled
    // the rows every time either side streamed a token. The fix pins
    // running threads to a created_at order so clicking one is stable.
    const older = makeSortableThread({
      id: "thread-older",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-20T12:00:00Z",
      status: "in_progress",
      turns: [{ id: "turn-1", status: "in_progress" }],
    });
    const newer = makeSortableThread({
      id: "thread-newer",
      createdAt: "2026-06-19T00:00:00Z",
      updatedAt: "2026-06-18T00:00:00Z", // stale; should be ignored while running
      status: "in_progress",
      turns: [{ id: "turn-1", status: "in_progress" }],
    });

    const sorted = sortThreads([older, newer]);
    expect(sorted.map((thread) => thread.id)).toEqual([
      "thread-newer",
      "thread-older",
    ]);

    // Even after flipping updated_at wildly, running order is unchanged.
    const flipped = sortThreads([
      { ...newer, updated_at: "2099-01-01T00:00:00Z" },
      { ...older, updated_at: "1970-01-01T00:00:00Z" },
    ]);
    expect(flipped.map((thread) => thread.id)).toEqual([
      "thread-newer",
      "thread-older",
    ]);
  });

  it("places running threads before settled threads", () => {
    const running = makeSortableThread({
      id: "thread-running",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-18T00:00:00Z",
      status: "in_progress",
      turns: [{ id: "turn-1", status: "in_progress" }],
    });
    // Settled thread updated more recently than the running one. It still
    // sits below the running section — recency bubbles within the settled
    // group, not above active conversations.
    const settledRecent = makeSortableThread({
      id: "thread-settled-recent",
      createdAt: "2026-06-17T00:00:00Z",
      updatedAt: "2099-01-01T00:00:00Z",
    });
    const settledOlder = makeSortableThread({
      id: "thread-settled-older",
      createdAt: "2026-06-16T00:00:00Z",
      updatedAt: "2026-06-19T00:00:00Z",
    });

    const sorted = sortThreads([settledOlder, running, settledRecent]);
    expect(sorted.map((thread) => thread.id)).toEqual([
      "thread-running",
      "thread-settled-recent",
      "thread-settled-older",
    ]);
  });

  it("sorts settled threads by updated_at desc", () => {
    const settledA = makeSortableThread({
      id: "thread-a",
      createdAt: "2026-06-15T00:00:00Z",
      updatedAt: "2026-06-15T00:00:00Z",
    });
    const settledB = makeSortableThread({
      id: "thread-b",
      createdAt: "2026-06-15T00:00:00Z",
      updatedAt: "2026-06-20T00:00:00Z",
    });
    const settledC = makeSortableThread({
      id: "thread-c",
      createdAt: "2026-06-15T00:00:00Z",
      updatedAt: "2026-06-17T00:00:00Z",
    });
    const sorted = sortThreads([settledA, settledB, settledC]);
    expect(sorted.map((thread) => thread.id)).toEqual([
      "thread-b",
      "thread-c",
      "thread-a",
    ]);
  });

  it("detects running via any in-progress turn even when thread status is idle", () => {
    // A thread that has just received its first turn but whose own status
    // hasn't been bumped yet must still be treated as running — the
    // streaming output lives in the latest turn.
    const streaming = makeSortableThread({
      id: "thread-streaming",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-18T00:00:00Z",
      status: "idle",
      turns: [{ id: "turn-1", status: "in_progress" }],
    });
    const settled = makeSortableThread({
      id: "thread-idle",
      createdAt: "2026-06-17T00:00:00Z",
      updatedAt: "2026-06-20T00:00:00Z",
    });

    const sorted = sortThreads([settled, streaming]);
    expect(sorted.map((thread) => thread.id)).toEqual([
      "thread-streaming",
      "thread-idle",
    ]);
  });

  it("does not reorder settled threads around background child work", () => {
    const threadWithRunningAgent = makeSortableThread({
      id: "thread-with-agent",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-18T00:00:00Z",
      childAgents: [
        {
          id: "agent-running",
          parent_id: "thread-with-agent",
          status: "running",
          task_name: "review",
        },
      ],
    });
    const settledRecent = makeSortableThread({
      id: "thread-settled-recent",
      createdAt: "2026-06-17T00:00:00Z",
      updatedAt: "2099-01-01T00:00:00Z",
    });

    const sorted = sortThreads([settledRecent, threadWithRunningAgent]);
    expect(sorted.map((thread) => thread.id)).toEqual([
      "thread-settled-recent",
      "thread-with-agent",
    ]);
    expect(isThreadUnread(threadWithRunningAgent, undefined)).toBe(false);
  });

  it("keeps archived and read-only threads in the sortable list so Settings → Archive can show them", () => {
    const archived = makeSortableThread({
      id: "thread-archived",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2099-01-01T00:00:00Z",
      archived: true,
    });
    const readOnly = makeSortableThread({
      id: "thread-readonly",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2099-01-01T00:00:00Z",
      readOnly: true,
    });
    const normal = makeSortableThread({
      id: "thread-normal",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-19T00:00:00Z",
    });

    const sorted = sortThreads([archived, readOnly, normal]);
    expect(sorted.map((thread) => thread.id).sort()).toEqual([
      "thread-archived",
      "thread-normal",
      "thread-readonly",
    ]);
  });

  it("keeps the active read-only child thread renderable via conversationPaneThreadsByID", () => {
    const child = makeSortableThread({
      id: "child-running",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-18T00:00:00Z",
      readOnly: true,
      turns: [{ id: "child-turn-1", status: "in_progress" }],
    });
    const normal = makeSortableThread({
      id: "thread-normal",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-19T00:00:00Z",
    });

    const sidebarThreads = sortThreads([normal, child]);
    const renderableThreads = conversationPaneThreadsByID(sidebarThreads, child);

    expect(sidebarThreads.map((thread) => thread.id).sort()).toEqual([
      "child-running",
      "thread-normal",
    ]);
    expect(renderableThreads.get("child-running")?.turns).toHaveLength(1);
  });

  it("pinnedThreads and projectThreads hide archived entries but keep read-only ones", () => {
    const pinnedArchived = makeSortableThread({
      id: "pinned-archived",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-20T00:00:00Z",
      pinned: true,
      archived: true,
    });
    const pinnedLive = makeSortableThread({
      id: "pinned-live",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-21T00:00:00Z",
      pinned: true,
    });
    const projectArchived = makeSortableThread({
      id: "project-archived",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-22T00:00:00Z",
      archived: true,
    });
    const projectReadOnly = makeSortableThread({
      id: "project-readonly",
      createdAt: "2026-06-18T00:00:00Z",
      updatedAt: "2026-06-23T00:00:00Z",
      readOnly: true,
    });

    const threads = [
      pinnedArchived,
      pinnedLive,
      projectArchived,
      projectReadOnly,
    ];

    expect(pinnedThreads(threads).map((thread) => thread.id)).toEqual([
      "pinned-live",
    ]);
    expect(projectThreads(threads).map((thread) => thread.id)).toEqual([
      "project-readonly",
    ]);
  });
});

describe("mergeListedThreads", () => {
  it("does not clear known child-agent state when a listed snapshot omits it", () => {
    const current: Thread = {
      ...threadWithUserTexts(["thread work"]),
      id: "thread-1",
      child_agents: [
        {
          id: "agent-running",
          parent_id: "thread-1",
          status: "running",
          task_name: "review",
        },
      ],
    };
    const listed: Thread = {
      ...current,
      turns: [],
      child_agents: undefined,
      updated_at: "2026-01-02T00:00:00Z",
    };

    const [merged] = mergeListedThreads([current], [listed]);

    expect(merged.child_agents?.[0]?.id).toBe("agent-running");
    expect(merged.child_agents?.[0]?.status).toBe("running");
    expect(isThreadUnread(merged, undefined)).toBe(true);
  });

  it("dedupes a live snapshot and a local archived copy of the same thread", () => {
    const live = {
      ...threadWithUserTexts(["thread work"]),
      id: "thread-1",
      archived: false,
    };
    const archivedCopy = { ...live, archived: true };

    const merged = mergeListedThreads([], [live, archivedCopy]);

    expect(merged).toHaveLength(1);
    expect(merged[0].id).toBe("thread-1");
    expect(merged[0].archived).toBe(true);
  });

  it("does not regress a terminal turn when an older list response lands later", () => {
    const completed = {
      ...threadWithUserTexts(["work"]),
      id: "thread-1",
      status: "idle" as const,
      turns: [
        {
          id: "turn-1",
          items_view: "full" as const,
          status: "completed" as const,
          items: [],
        },
      ],
    };
    const stale = {
      ...completed,
      status: "in_progress" as const,
      turns: [{ ...completed.turns[0], status: "in_progress" as const }],
    };

    const [merged] = mergeListedThreads([completed], [stale]);

    expect(merged.status).toBe("idle");
    expect(merged.turns[0].status).toBe("completed");
  });
});

describe("reconcileListedThreadState", () => {
  function runningThread(id: string): Thread {
    return {
      ...threadWithUserTexts([id]),
      id,
      status: "in_progress",
      turns: [
        {
          id: `${id}-turn`,
          items_view: "full",
          status: "in_progress",
          items: [
            {
              id: `${id}-answer`,
              type: "agent_message",
              status: "completed",
              terminal: true,
              text: "done",
            },
          ],
        },
      ],
    };
  }

  function settledThread(thread: Thread): Thread {
    return {
      ...thread,
      status: "idle",
      updated_at: "2026-01-02T00:00:00Z",
      turns: thread.turns.map((turn) => ({
        ...turn,
        status: "completed",
        completed_at: "2026-01-02T00:00:00Z",
      })),
    };
  }

  it("repairs a visible in-progress turn when turn/completed was missed", () => {
    const current = runningThread("thread-1");
    const completed = settledThread(current);

    const next = reconcileListedThreadState(
      {
        ...initialState,
        thread: current,
        threads: [current],
        running: true,
        status: "streaming",
      },
      [completed],
    );

    expect(next.thread).toBe(next.threads[0]);
    expect(next.thread?.turns[0].status).toBe("completed");
    expect(next.thread?.status).toBe("idle");
    expect(next.running).toBe(false);
    expect(next.status).toBe("ready");
  });

  it("reconciles both panes and derives running from the selected pane", () => {
    const primary = runningThread("primary");
    const secondary = runningThread("secondary");

    const next = reconcileListedThreadState(
      {
        ...initialState,
        thread: primary,
        secondaryThread: secondary,
        activePane: "secondary",
        threads: [primary, secondary],
        running: true,
      },
      [primary, settledThread(secondary)],
    );

    expect(next.thread?.turns[0].status).toBe("in_progress");
    expect(next.secondaryThread?.turns[0].status).toBe("completed");
    expect(next.running).toBe(false);
  });
});

describe("latestContextUsageForThread", () => {
  function makeThread(args: {
    id?: string;
    model?: string;
    turns?: Array<{
      id: string;
      status?: "in_progress" | "completed" | "failed" | "interrupted";
    }>;
  } = {}): Thread {
    return {
      id: args.id ?? "thread-1",
      preview: "",
      model_provider: "fake",
      model: args.model ?? "fake-model",
      cwd: "/tmp",
      status: "idle",
      created_at: "2026-06-18T00:00:00Z",
      updated_at: "2026-06-18T00:00:00Z",
      turns: (args.turns ?? []).map((t) => ({
        id: t.id,
        items: [],
        items_view: "full" as const,
        status: t.status ?? "completed",
      })),
    };
  }

  it("returns undefined when the thread is undefined", () => {
    expect(latestContextUsageForThread(initialState, undefined)).toBeUndefined();
  });

  it("falls back to the active runtime model when no thread exists yet", () => {
    const result = latestContextUsageForThread(initialState, undefined, {
      model: "gpt-5",
      contextWindowTokens: 400_000,
    });
    expect(result).toEqual({
      turnID: "",
      used: 0,
      window: 400_000,
      inputTokens: 0,
      cacheCreationTokens: 0,
      cacheReadTokens: 0,
    });
  });

  it("returns undefined when no thread exists and runtime ceiling is absent", () => {
    const result = latestContextUsageForThread(initialState, undefined, {
      model: "claude-sonnet-4-5",
    });
    expect(result).toBeUndefined();
  });

  it("returns undefined for an empty thread with an unrecognized model", () => {
    // "fake-model" has no catalog entry — the ring should hide rather
    // than guess a limit.
    const t = makeThread({ turns: [] });
    expect(latestContextUsageForThread(initialState, t)).toBeUndefined();
  });

  it("hides the meter when no runtime ceiling is available and no turn has run", () => {
    const t = makeThread({ model: "claude-sonnet-4-5", turns: [] });
    const result = latestContextUsageForThread(initialState, t);
    expect(result).toBeUndefined();
  });

  it("does not infer a gateway model ceiling from the client", () => {
    const t = makeThread({ model: "anthropic/claude-sonnet-4-5", turns: [] });
    const result = latestContextUsageForThread(initialState, t);
    expect(result).toBeUndefined();
  });

  it("does not treat raw provider usage as retained context", () => {
    let state = initialState;
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      1_300_000,
      0,
      0,
      0,
      1_000,
      1_000_000,
    );
    const t = makeThread({
      model: "minimax-m3",
      turns: [{ id: "turn-1", status: "completed" }],
    });
    const result = latestContextUsageForThread(state, t, {
      model: "minimax-m3",
      contextWindowTokens: 1_000_000,
    });
    expect(result).toEqual({
      turnID: "",
      used: 0,
      window: 1_000_000,
      inputTokens: 0,
      cacheCreationTokens: 0,
      cacheReadTokens: 0,
    });
  });

  it("prefers the retained estimate over inclusive raw input (A1 normalization)", () => {
    // A live turn/usage sample from an inclusive-input endpoint (MiniMax
    // reports input_tokens including cache_read) carries a large raw input
    // alongside the server-side retained-context estimate. The meter must
    // read the retained estimate (context_tokens = res.ContextTokens), never
    // the raw input, and cache_read stays intact for the hit-rate display.
    let state = initialState;
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      132_600, // raw input_tokens (inclusive of cache_read)
      2_000,
      0,
      113_000, // cache_read preserved
      1_000,
      1_000_000,
      "minimax-m3",
      88_000, // retained context estimate (res.ContextTokens)
    );
    const t = makeThread({
      model: "minimax-m3",
      turns: [{ id: "turn-1", status: "completed" }],
    });
    const result = latestContextUsageForThread(state, t, {
      model: "minimax-m3",
      contextWindowTokens: 1_000_000,
    });
    expect(result?.turnID).toBe("turn-1");
    expect(result?.used).toBe(88_000);
    expect(result?.used).not.toBe(132_600);
    expect(result?.cacheReadTokens).toBe(113_000);
  });

  it("returns real usage from the most recent turn that has one", () => {
    let state = initialState;
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      10,
      0,
      0,
      0,
      1_000,
      200_000,
      undefined,
      10,
    );
    state = appendTurnTokenSample(
      state,
      "turn-2",
      "thread-1",
      20,
      0,
      0,
      0,
      2_000,
      200_000,
      undefined,
      20,
    );
    const t = makeThread({
      turns: [
        { id: "turn-1", status: "completed" },
        { id: "turn-2", status: "completed" },
      ],
    });
    const result = latestContextUsageForThread(state, t);
    expect(result?.turnID).toBe("turn-2");
    expect(result?.used).toBe(20);
    // Real usage wins over the catalog fallback.
    expect(result?.window).toBe(200_000);
  });

  it("uses persisted turn usage after a restart when live usage state is empty", () => {
    const t = makeThread({
      model: "minimax-m3",
      turns: [
        {
          id: "turn-1",
          status: "completed",
        },
      ],
    });
    t.turns[0].input_tokens = 19_600;
    t.turns[0].cache_read_tokens = 113_000;
    t.turns[0].cache_creation_tokens = 0;
    t.turns[0].context_tokens = 88_000;
    t.turns[0].usage_model = "minimax-m3";
    const result = latestContextUsageForThread(initialState, t, {
      model: "minimax-m3",
      contextWindowTokens: 1_000_000,
    });
    expect(result).toEqual({
      turnID: "turn-1",
      used: 88_000,
      window: 1_000_000,
      inputTokens: 19_600,
      cacheCreationTokens: 0,
      cacheReadTokens: 113_000,
    });
  });

  it("walks back to the previous turn when the most recent has no usage", () => {
    // The ring is a passive readout — it must keep showing the last
    // known context after a turn completes. We test that by giving the
    // thread a most-recent turn with no recorded usage, and verifying
    // the selector falls through to the previous one.
    let state = initialState;
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      10,
      0,
      0,
      0,
      1_000,
      200_000,
      undefined,
      10,
    );
    const t = makeThread({
      turns: [
        { id: "turn-1", status: "completed" },
        { id: "turn-2", status: "completed" },
      ],
    });
    const result = latestContextUsageForThread(state, t);
    expect(result?.turnID).toBe("turn-1");
    expect(result?.used).toBe(10);
  });

  it("prefers real usage over the catalog when both are reachable", () => {
    // Even if the active model is in the catalog, real usage from a
    // previous turn in the same thread wins — the catalog is a fallback
    // for "model known, no usage yet", not a default that overrides
    // observed values.
    let state = initialState;
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      10,
      0,
      0,
      0,
      1_000,
      200_000,
      undefined,
      10,
    );
    const t = makeThread({
      model: "claude-sonnet-4-5",
      turns: [{ id: "turn-1", status: "completed" }],
    });
    const result = latestContextUsageForThread(state, t);
    expect(result?.turnID).toBe("turn-1");
    expect(result?.used).toBe(10);
  });

  it("ignores stale usage from a previous model after the thread model changes", () => {
    let state = initialState;
    state = appendTurnTokenSample(
      state,
      "turn-1",
      "thread-1",
      80_000,
      0,
      0,
      0,
      1_000,
      200_000,
      "claude-sonnet-4-5",
      80_000,
    );
    const t = makeThread({
      model: "gpt-5",
      turns: [{ id: "turn-1", status: "completed" }],
    });
    const result = latestContextUsageForThread(state, t);
    expect(result).toBeUndefined();
  });
});

describe("activeTodoUpdateForThread", () => {
  // The floating "跳到最新" pill cluster's progress chip must disappear
  // once its turn completes — otherwise it lingers on top of the same
  // turn's now-persistent action row (issue #7). `latestTodoUpdateForThread`
  // itself (used by the environment side panel) stays turn-status-agnostic;
  // this wrapper is the one the pill actually reads.
  function threadWithTodo(turnStatus: Turn["status"]): Thread {
    const todoItem: ThreadItem = {
      id: "turn-1-item-1",
      type: "tool_call",
      status: "completed",
      name: "plugin_todo_update_todo_abc123",
      display: {
        kind: "todo",
        text: "Updating TODO",
        capability: "todo",
      },
      arguments: JSON.stringify({
        todos: [
          { content: "定位问题", status: "completed" },
          { content: "实现修复", status: "in_progress" },
        ],
      }),
    };
    return {
      id: "thread-1",
      preview: "preview",
      model_provider: "fake",
      model: "fake-model",
      cwd: "/repo",
      status: "idle",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      turns: [
        {
          id: "turn-1",
          items_view: "full",
          status: turnStatus,
          items: [todoItem],
        },
      ],
    };
  }

  it("returns the TODO list while its turn is still running", () => {
    const thread = threadWithTodo("in_progress");
    expect(activeTodoUpdateForThread(thread)?.todos).toEqual([
      { content: "定位问题", status: "completed" },
      { content: "实现修复", status: "in_progress" },
    ]);
  });

  it("hides the TODO list once the turn completes", () => {
    expect(activeTodoUpdateForThread(threadWithTodo("completed"))).toBeUndefined();
  });

  it("hides the TODO list for a failed or interrupted turn", () => {
    expect(activeTodoUpdateForThread(threadWithTodo("failed"))).toBeUndefined();
    expect(activeTodoUpdateForThread(threadWithTodo("interrupted"))).toBeUndefined();
  });

  it("returns undefined when there is no thread", () => {
    expect(activeTodoUpdateForThread(undefined)).toBeUndefined();
  });
});

describe("sidebar pin/archive matrix", () => {
  // Mental model under test: pinning MOVES an entity to 置顶 (no
  // duplicate left in its own section); archiving removes it from the
  // sidebar completely (own section AND 置顶).
  function summaries(
    overrides: Array<Partial<Thread> & { id: string }>,
  ): ThreadSummary[] {
    return summarizeThreadsForSidebar(
      overrides.map((entry) => ({
        ...threadWithUserTexts([entry.id]),
        ...entry,
      })),
    );
  }

  it("pinnedThreadSummaries hosts pinned threads, never archived ones", () => {
    const all = summaries([
      { id: "scratch-pinned", pinned: true },
      { id: "scratch-live" },
      { id: "scratch-archived-pinned", pinned: true, archived: true },
    ]);
    const pinned = pinnedThreadSummaries(all).map((thread) => thread.id);
    expect(pinned).toContain("scratch-pinned");
    expect(pinned).not.toContain("scratch-live");
    // Archived-but-pinned must not ghost in 置顶.
    expect(pinned).not.toContain("scratch-archived-pinned");
  });

  it("scratchThreadSummaries drops pinned and archived entries (move semantics)", () => {
    const all = summaries([
      { id: "scratch-live" },
      { id: "scratch-pinned", pinned: true },
      { id: "scratch-archived", archived: true },
    ]);
    expect(
      scratchThreadSummaries(all, []).map((thread) => thread.id),
    ).toEqual(["scratch-live"]);
  });
});

describe("composerDraftHasContent", () => {
  it("is false for a blank draft and true once text, an image, or a file is present", () => {
    expect(
      composerDraftHasContent({ prompt: "", images: [], files: [] }),
    ).toBe(false);
    expect(
      composerDraftHasContent({ prompt: "   ", images: [], files: [] }),
    ).toBe(false);
    expect(
      composerDraftHasContent({ prompt: "hi", images: [], files: [] }),
    ).toBe(true);
    expect(
      composerDraftHasContent({
        prompt: "",
        images: [{ id: "img-1", dataUrl: "data:," }] as never,
        files: [],
      }),
    ).toBe(true);
    expect(
      composerDraftHasContent({
        prompt: "",
        images: [],
        files: [{ id: "file-1", name: "a.txt" }] as never,
      }),
    ).toBe(true);
  });
});

describe("applyLoadedRuntimeWithDraftCarry (R2: draft follows a pill switch)", () => {
  const projectContext: RuntimeContext = {
    kind: "project",
    project_id: "p1",
    cwd: "/repo/p1",
  };
  const noProjectContext: RuntimeContext = {
    kind: "no_project",
    cwd: "/scratch/2026-07-03",
  };

  function draftTab(
    id: string,
    context: RuntimeContext,
    prompt: string,
  ): SessionTab {
    return {
      id,
      kind: "draft",
      context,
      title: "新对话",
      prompt,
      images: [],
      files: [],
      createdAt: 0,
    };
  }

  function draftOf(tab: SessionTab): string | undefined {
    return tab.kind === "draft" || tab.kind === "thread"
      ? tab.prompt
      : undefined;
  }

  it("carries a non-empty draft into the target context's tab, overwriting that tab's own stale content", () => {
    const oldTab = draftTab("draft:old", projectContext, "");
    const staleTargetTab = draftTab(
      "draft:target-stale",
      noProjectContext,
      "an earlier, unrelated draft",
    );
    const current: AppState = {
      ...initialState,
      activeContext: projectContext,
      sessionTabs: [oldTab, staleTargetTab],
      activeSessionTabID: oldTab.id,
    };
    const outgoingDraft: ComposerDraftState = {
      prompt: "hello from the project draft",
      images: [],
      files: [],
    };
    const loadedState: Partial<AppState> = {
      activeContext: noProjectContext,
      thread: undefined,
    };

    const next = applyLoadedRuntimeWithDraftCarry(
      current,
      loadedState,
      outgoingDraft,
    );

    // Lands on the pre-existing draft tab for the target context...
    expect(next.activeSessionTabID).toBe(staleTargetTab.id);
    const resultTargetTab = next.sessionTabs.find(
      (tab) => tab.id === staleTargetTab.id,
    );
    // ...and its content is the carried draft, not its own stale one.
    expect(draftOf(resultTargetTab!)).toBe("hello from the project draft");
    // The tab the user left behind keeps whatever it already had — no
    // duplicate copy of the carried text is written back into it.
    const resultOldTab = next.sessionTabs.find((tab) => tab.id === oldTab.id);
    expect(draftOf(resultOldTab!)).toBe("");
  });

  it("does not carry an empty draft (falls back to persisting + loading normally)", () => {
    const oldTab = draftTab("draft:old", projectContext, "");
    const current: AppState = {
      ...initialState,
      activeContext: projectContext,
      sessionTabs: [oldTab],
      activeSessionTabID: oldTab.id,
    };
    const emptyDraft: ComposerDraftState = {
      prompt: "",
      images: [],
      files: [],
    };
    const loadedState: Partial<AppState> = {
      activeContext: noProjectContext,
      thread: undefined,
    };

    const next = applyLoadedRuntimeWithDraftCarry(
      current,
      loadedState,
      emptyDraft,
    );

    // A brand new draft tab is created for the target context, still empty.
    expect(next.activeSessionTabID).not.toBe(oldTab.id);
    const resultNewTab = next.sessionTabs.find(
      (tab) => tab.id === next.activeSessionTabID,
    );
    expect(draftOf(resultNewTab!)).toBe("");
  });

  it("does not carry when the outgoing tab is a real thread, not a draft", () => {
    const thread: Thread = {
      ...threadWithUserTexts(["reply in progress"]),
      id: "thread-1",
      cwd: projectContext.cwd,
    };
    const threadTab = createThreadSessionTab(thread, projectContext);
    const current: AppState = {
      ...initialState,
      activeContext: projectContext,
      thread,
      sessionTabs: [threadTab],
      activeSessionTabID: threadTab.id,
      threads: [thread],
    };
    const outgoingDraft: ComposerDraftState = {
      prompt: "typed mid-thread, not a fresh draft",
      images: [],
      files: [],
    };
    const loadedState: Partial<AppState> = {
      activeContext: noProjectContext,
      thread: undefined,
    };

    const next = applyLoadedRuntimeWithDraftCarry(
      current,
      loadedState,
      outgoingDraft,
    );

    // The thread tab keeps the in-progress reply (ordinary persist path)...
    const resultThreadTab = next.sessionTabs.find(
      (tab) => tab.id === threadTab.id,
    );
    expect(draftOf(resultThreadTab!)).toBe(
      "typed mid-thread, not a fresh draft",
    );
    // ...and the freshly-created no-project draft tab starts blank, since
    // this isn't the "carry a fresh draft along" case.
    const resultNewTab = next.sessionTabs.find(
      (tab) => tab.id === next.activeSessionTabID,
    );
    expect(draftOf(resultNewTab!)).toBe("");
  });
});

describe("conversationSearchContextLabel (R4: no raw scratch paths in the UI)", () => {
  // A no-project (scratch) thread's cwd is an internal
  // ~/.wuu/scratch/<date> directory (allocateNoProjectCwd in
  // src/main/projects.ts) — not something a user ever named or should see
  // spelled out in the conversation search results.
  const scratchThread: Thread = {
    ...threadWithUserTexts(["scratch prompt"]),
    id: "scratch-thread",
    cwd: "/Users/test/.wuu/scratch/2026-07-03",
  };

  it("shows the project name for a thread that belongs to a registered project", () => {
    const project: DesktopProject = {
      id: "proj-1",
      name: "MyApp",
      path: "/repo/myapp",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    };
    const thread: Thread = {
      ...threadWithUserTexts(["hi"]),
      id: "project-thread",
      cwd: project.path,
    };
    expect(conversationSearchContextLabel(thread, [project])).toBe("MyApp");
  });

  it("labels a no-project (scratch) thread 无项目 instead of its raw scratch directory name", () => {
    const label = conversationSearchContextLabel(scratchThread, []);
    expect(label).toBe("无项目");
    expect(label).not.toContain("2026-07-03");
    expect(label).not.toContain("scratch");
  });

  it("still says 无项目 when other registered projects exist but none match this thread's cwd", () => {
    const otherProject: DesktopProject = {
      id: "proj-2",
      name: "OtherApp",
      path: "/repo/other",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    };
    expect(
      conversationSearchContextLabel(scratchThread, [otherProject]),
    ).toBe("无项目");
  });
});

describe("sessionTabLabel (draft tabs read as their workspace)", () => {
  const project: DesktopProject = {
    id: "p1",
    name: "acme-web",
    path: "/repo/acme-web",
    created_at: "2026-07-04T00:00:00.000Z",
    updated_at: "2026-07-04T00:00:00.000Z",
  };
  const state: AppState = { ...initialState, projects: [project] };

  it("labels a project draft tab with the project name", () => {
    const tab = createDraftSessionTab("draft:initial:project:p1", {
      kind: "project",
      project_id: "p1",
      cwd: "/repo/acme-web",
    });
    expect(sessionTabLabel(tab, state)).toBe("acme-web");
  });

  it("labels a no-project draft tab as 对话", () => {
    const tab = createDraftSessionTab("draft:initial:no_project:/scratch", {
      kind: "no_project",
      cwd: "/scratch",
    });
    expect(sessionTabLabel(tab, state)).toBe("对话");
  });

  it("does not surface the typed prompt in the draft label", () => {
    const tab = createDraftSessionTab(
      "draft:initial:project:p1",
      { kind: "project", project_id: "p1", cwd: "/repo/acme-web" },
      { prompt: "please refactor the auth module", images: [], files: [] },
    );
    expect(sessionTabLabel(tab, state)).toBe("acme-web");
  });

  it("falls back to the cwd basename when the project is gone (removed/relocated)", () => {
    const tab = createDraftSessionTab("draft:initial:project:ghost", {
      kind: "project",
      project_id: "ghost",
      cwd: "/repo/orphaned-dir",
    });
    expect(sessionTabLabel(tab, state)).toBe("orphaned-dir");
  });

  it("gives each channel room a stable tab identity and its room title", () => {
    const context: RuntimeContext = { kind: "no_project", cwd: "/scratch" };
    const tab = createChannelRoomSessionTab("room-1", "Design review", context);

    expect(tab.id).toBe(channelRoomSessionTabID("room-1"));
    expect(tab.id).toBe("channel-room:room-1");
    expect(sessionTabLabel(tab, state)).toBe("Design review");
  });

  it("labels the singleton Agents and Tasks tabs", () => {
    const context: RuntimeContext = { kind: "no_project", cwd: "/scratch" };

    expect(sessionTabLabel(createAgentsSessionTab(context), state)).toBe("Agents");
    expect(sessionTabLabel(createTasksSessionTab(context), state)).toBe("任务");
  });
});

describe("thread session tab title sync", () => {
  const context: RuntimeContext = { kind: "no_project", cwd: "/repo" };

  it("refreshes a thread tab's snapshot title when thread/updated carries a new title", () => {
    const thread = { ...threadWithUserTexts(["first query"]), preview: "" };
    const tab = createThreadSessionTab(thread, context);
    expect(tab.title).toBe("未命名对话");

    const next = reduceServerEvent(
      {
        ...initialState,
        activeContext: context,
        thread,
        threads: [thread],
        sessionTabs: [tab],
        activeSessionTabID: tab.id,
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "thread/updated",
          params: {
            thread: { ...thread, preview: "Fix login crash" },
          },
        },
      },
    );

    expect(next.sessionTabs[0]?.title).toBe("Fix login crash");
  });

  it("keeps the freshest known title when the thread leaves renderer state (workspace switch)", () => {
    const thread = { ...threadWithUserTexts(["first query"]), preview: "" };
    const tab = createThreadSessionTab(thread, context);
    const next = reduceServerEvent(
      {
        ...initialState,
        activeContext: context,
        thread,
        threads: [thread],
        sessionTabs: [tab],
        activeSessionTabID: tab.id,
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "thread/updated",
          params: {
            thread: { ...thread, preview: "Fix login crash" },
          },
        },
      },
    );

    // The active workspace moved on; the thread is no longer resolvable from
    // state.thread / state.threads, so the label must fall back to the tab's
    // synced snapshot instead of the stale creation-time placeholder.
    const switched = {
      ...next,
      activeContext: { kind: "no_project", cwd: "/other" } as RuntimeContext,
      thread: undefined,
      threads: [],
    };
    expect(sessionTabLabel(switched.sessionTabs[0], switched)).toBe(
      "Fix login crash",
    );
  });

  it("leaves unrelated tabs and unchanged titles alone", () => {
    const thread = threadWithUserTexts(["first query"]);
    const tab = createThreadSessionTab(thread, context);
    const draft = createDraftSessionTab("draft:active", context);
    const next = reduceServerEvent(
      {
        ...initialState,
        activeContext: context,
        thread,
        threads: [thread],
        sessionTabs: [tab, draft],
        activeSessionTabID: tab.id,
      },
      {
        kind: "notification",
        workdir: "/repo",
        message: {
          method: "thread/updated",
          params: {
            thread: { ...thread, preview: "preview" },
          },
        },
      },
    );

    expect(next.sessionTabs).toHaveLength(2);
    expect(next.sessionTabs[0]?.title).toBe(tab.title);
    expect(next.sessionTabs[1]).toBe(draft);
  });
});

describe("reconcileChannelRoomSessionTabs", () => {
  const context: RuntimeContext = { kind: "no_project", cwd: "/scratch" };

  it("updates room tab titles and closes tabs for deleted rooms", () => {
    const draft = createDraftSessionTab("draft:active", context);
    const renamed = createChannelRoomSessionTab("room-1", "Old name", context);
    const deleted = createChannelRoomSessionTab("room-2", "Deleted", context);
    const next = reconcileChannelRoomSessionTabs(
      {
        ...initialState,
        activeContext: context,
        activeSessionTabID: draft.id,
        sessionTabs: [draft, renamed, deleted],
      },
      [{ id: "room-1", name: "New name" } as ChannelRoom],
    );

    expect(next.sessionTabs.map((tab) => [tab.id, tab.title])).toEqual([
      [draft.id, draft.title],
      [renamed.id, "New name"],
    ]);
    expect(next.activeSessionTabID).toBe(draft.id);
  });

  it("selects the nearest remaining tab when the active room is deleted", () => {
    const first = createDraftSessionTab("draft:first", context);
    const room = createChannelRoomSessionTab("room-1", "Room", context);
    const last = createTasksSessionTab(context);
    const next = reconcileChannelRoomSessionTabs(
      {
        ...initialState,
        activeContext: context,
        activeSessionTabID: room.id,
        sessionTabs: [first, room, last],
      },
      [],
    );

    expect(next.sessionTabs.map((tab) => tab.id)).toEqual([first.id, last.id]);
    expect(next.activeSessionTabID).toBe(last.id);
  });

  it("restores a draft tab when the deleted room was the last open tab", () => {
    const room = createChannelRoomSessionTab("room-1", "Room", context);
    const next = reconcileChannelRoomSessionTabs(
      {
        ...initialState,
        activeContext: context,
        activeSessionTabID: room.id,
        sessionTabs: [room],
      },
      [],
    );

    expect(next.sessionTabs).toHaveLength(1);
    expect(next.sessionTabs[0]?.kind).toBe("draft");
    expect(next.activeSessionTabID).toBe(next.sessionTabs[0]?.id);
  });
});

describe("AppState English localization", () => {
  it("localizes generated labels while preserving project data", () => {
    setActiveLocale("en-US");
    const scratchThread: Thread = {
      ...threadWithUserTexts(["original prompt"]),
      cwd: "/Users/test/.wuu/scratch/2026-07-03",
      pinned: true,
      updated_at: "invalid",
      created_at: "invalid",
    };
    const draft = createDraftSessionTab("draft:en", {
      kind: "no_project",
      cwd: "/scratch",
    });

    expect(conversationSearchContextLabel(scratchThread, [])).toBe("No project");
    expect(conversationSearchThreadMeta(scratchThread)).toBe("Pinned · Unknown time");
    expect(sessionTabLabel(draft, { ...initialState, projects: [] })).toBe(
      "Conversations",
    );
    const attachmentPreview = turnPreview({
      id: "turn-attachments",
      items_view: "full",
      status: "completed",
      items: [
        {
          id: "user",
          type: "user_message",
          images: [{ media_type: "image/png", data: "AA==" }],
        },
      ],
    });
    expect(resolveLocalizedText(attachmentPreview)).toBe("[Image #1]");
    setActiveLocale("zh-CN");
    expect(resolveLocalizedText(attachmentPreview)).toBe("[图片 #1]");
  });

  it("re-resolves persisted app labels after the locale changes", () => {
    setActiveLocale("en-US");
    const skills = createSkillsSessionTab({ kind: "no_project", cwd: "/scratch" });
    const exited = reduceServerEvent(initialState, {
      kind: "server-exit",
      workdir: "",
      code: 0,
      message: "",
    });
    expect(sessionTabLabel(skills, initialState)).toBe("Extensions");
    expect(resolveLocalizedText(exited.status)).toBe("wuu core exited");

    setActiveLocale("zh-CN");
    expect(sessionTabLabel(skills, initialState)).toBe("扩展");
    expect(resolveLocalizedText(exited.status)).toBe("wuu core 已退出");
  });
});

describe("reconcileResumedThreadTurns", () => {
  function threadWithTurnIDs(ids: string[]): Thread {
    return {
      ...threadWithUserTexts([]),
      turns: ids.map((id) => ({
        id,
        items_view: "full",
        status: "completed",
        items: [
          {
            id: `${id}-user`,
            type: "user_message",
            status: "completed",
            role: "user",
            text: `message ${id}`,
          },
        ],
      })),
    };
  }

  it("salvages the client tail when the resumed snapshot is a strict prefix (just-sent turn not yet in the snapshot)", () => {
    // The reported bug: send a message (turn-3 lands only in client state),
    // switch session tabs, switch back. resumeThread returns [turn-1, turn-2]
    // — lagging behind — and the wholesale replace would drop turn-3.
    const resumed = threadWithTurnIDs(["turn-1", "turn-2"]);
    const local = threadWithTurnIDs(["turn-1", "turn-2", "turn-3"]);
    const merged = reconcileResumedThreadTurns(resumed, local);
    expect(merged.turns.map((turn) => turn.id)).toEqual([
      "turn-1",
      "turn-2",
      "turn-3",
    ]);
    // The overlapping prefix uses the server's (fresher) turn objects; only
    // the client's extra tail is carried over verbatim.
    expect(merged.turns[0]).toBe(resumed.turns[0]);
    expect(merged.turns[1]).toBe(resumed.turns[1]);
    expect(merged.turns[2]).toBe(local.turns[2]);
  });

  it("salvages client tail items when a resumed turn snapshot lags behind live items", () => {
    const resumed = threadWithTurnIDs(["turn-1", "turn-2"]);
    const local = threadWithTurnIDs(["turn-1", "turn-2"]);
    const liveItem: ThreadItem = {
      id: "turn-2-tool",
      seq: 8,
      type: "tool_call",
      status: "completed",
      name: "bash",
    };
    local.turns[1] = {
      ...local.turns[1],
      items: [...local.turns[1].items, liveItem],
    };

    const merged = reconcileResumedThreadTurns(resumed, local);

    expect(merged.turns).toHaveLength(2);
    expect(merged.turns[1].items).toEqual([
      resumed.turns[1].items[0],
      liveItem,
    ]);
  });

  it("trusts the resumed snapshot when history diverges (edit/fork truncated the tail)", () => {
    // An edit/fork replaces turn-2 with a new turn id and truncates what
    // followed. That is a genuine server-side truncation, not lag, so we must
    // NOT resurrect the client's stale [turn-2, turn-3].
    const resumed = threadWithTurnIDs(["turn-1", "turn-2b"]);
    const local = threadWithTurnIDs(["turn-1", "turn-2", "turn-3"]);
    const merged = reconcileResumedThreadTurns(resumed, local);
    expect(merged).toBe(resumed);
    expect(merged.turns.map((turn) => turn.id)).toEqual(["turn-1", "turn-2b"]);
  });

  it("trusts the resumed snapshot when it is at least as long as the local copy (server is ahead or equal)", () => {
    const resumed = threadWithTurnIDs(["turn-1", "turn-2", "turn-3"]);
    const local = threadWithTurnIDs(["turn-1", "turn-2"]);
    expect(reconcileResumedThreadTurns(resumed, local)).toBe(resumed);
  });

  it("returns the resumed thread unchanged when there is no local copy", () => {
    const resumed = threadWithTurnIDs(["turn-1"]);
    expect(reconcileResumedThreadTurns(resumed, undefined)).toBe(resumed);
  });
});

describe("threadNeedsResumeOnReselect", () => {
  const context: RuntimeContext = {
    kind: "no_project",
    cwd: "/tmp/wuu-scratch",
  };

  it("asks the caller to resume when the active thread is only an empty list snapshot", () => {
    const thread = { ...threadWithUserTexts([]), turns: [] };
    const state: AppState = {
      ...initialState,
      activeContext: context,
      thread,
      threads: [thread],
      sessionTabs: [createThreadSessionTab(thread, context)],
      activeSessionTabID: threadSessionTabID(thread.id),
    };

    expect(threadNeedsResumeOnReselect(state, thread.id)).toBe(true);
  });

  it("does not re-resume an active thread that already has loaded turns", () => {
    const thread = threadWithUserTexts(["hello"]);
    const state: AppState = {
      ...initialState,
      activeContext: context,
      thread,
      threads: [thread],
      sessionTabs: [createThreadSessionTab(thread, context)],
      activeSessionTabID: threadSessionTabID(thread.id),
    };

    expect(threadNeedsResumeOnReselect(state, thread.id)).toBe(false);
  });
});

describe("extension inventory context", () => {
  const projectA: RuntimeContext = { kind: "project", project_id: "a", cwd: "/a" };
  const projectB: RuntimeContext = { kind: "project", project_id: "b", cwd: "/b" };
  const oldInventory: ExtensionInventoryRecord[] = [{
    id: "old",
    name: "Old",
    kind: "plugin",
    provenance: { kind: "plugin", source: "project", scope: "project" },
    state: "pending",
  }];
  const nextInventory: ExtensionInventoryRecord[] = [{
    id: "next",
    name: "Next",
    kind: "plugin",
    provenance: { kind: "plugin", source: "project", scope: "project" },
    state: "pending",
  }];

  it("applies inventory only to the runtime that requested it", () => {
    const state = {
      ...initialState,
      activeContext: projectB,
      initialized: { extension_inventory: oldInventory },
    } as AppState;

    expect(withExtensionInventoryForContext(state, projectA, nextInventory)).toBe(state);
    expect(withExtensionInventoryForContext(state, projectB, nextInventory).initialized?.extension_inventory).toEqual(nextInventory);
  });

  it("applies a live plugin generation inventory notification", () => {
    const state = {
      ...initialState,
      activeContext: projectB,
      initialized: { extension_inventory: oldInventory },
    } as AppState;

    const next = reduceServerEvent(state, {
      kind: "notification",
      workdir: projectB.cwd,
      message: {
        method: "plugin/inventory/changed",
        params: {
          epoch: 2,
          extension_inventory: nextInventory,
          skills: [],
        },
      },
    });

    expect(next.initialized?.extension_inventory).toEqual(nextInventory);
  });

  it("ignores malformed live plugin inventory notifications", () => {
    const state = {
      ...initialState,
      activeContext: projectB,
      initialized: { extension_inventory: oldInventory },
    } as AppState;

    const next = reduceServerEvent(state, {
      kind: "notification",
      workdir: projectB.cwd,
      message: {
        method: "plugin/inventory/changed",
        params: { epoch: 2, extension_inventory: [{ id: "incomplete" }] },
      },
    });

    expect(next).toBe(state);
  });
});

it("prepends remote history without overwriting live turns and ignores superseded cursors", () => {
  const live = {id:"recent",status:"in_progress",items_view:"full",items:[{id:"text",type:"agent_message",text:"live"}]} as Turn;
  const current = {id:"remote",cwd:"/tmp",turns:[live],history_cursor:"cursor"} as Thread;
  const state = {...initialState,thread:current,threads:[current]};
  const earlier = {id:"older",status:"completed",items_view:"full",items:[]} as Turn;
  const event = {workdir:"/tmp",kind:"notification" as const,message:{method:"thread/historyLoaded",params:{thread_id:"remote",cursor:"cursor",turns:[earlier,{...live,items:[]}]}}};
  const next = reduceServerEvent(state,event);
  expect(next.thread?.turns).toEqual([earlier,live]);
  expect(next.thread?.history_cursor).toBeUndefined();
  expect(reduceServerEvent(next,event).thread?.turns).toEqual([earlier,live]);
});

it("joins pages within a long turn while retaining newer live item content", () => {
  const live = {id:"turn",status:"in_progress",items_view:"full",items:[{id:"answer",type:"agent_message",text:"newer"}]} as Turn;
  const thread = {id:"remote",cwd:"/tmp",turns:[live],history_cursor:"item:boundary"} as Thread;
  const tool = {id:"tool",type:"tool_call",result:"earlier"} as ThreadItem;
  const next = reduceServerEvent({...initialState,thread,threads:[thread]}, {workdir:"/tmp",kind:"notification",message:{method:"thread/historyLoaded",params:{thread_id:"remote",cursor:"item:boundary",turns:[{...live,items:[tool,{...live.items[0],text:"older"}]}]}}});
  expect(next.thread?.turns).toHaveLength(1);
  expect(next.thread?.turns[0].items).toEqual([tool,...live.items]);
  expect(next.thread?.turns[0].status).toBe("in_progress");
});
