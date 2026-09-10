import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ThreadItem, Turn } from "../shared/protocol";
import { ASSISTANT_TURN_PRESENTATION_STABILIZE_MS } from "./AssistantTurnPresentation";
import { PROCESS_NOTIFICATION_NAME } from "./InternalUserNotification";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { TurnView } from "./TurnView";

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function makeTurn(
  status: Turn["status"],
  items: ThreadItem[] = [],
  error?: string,
): Turn {
  return {
    id: "turn-1",
    items,
    items_view: "full",
    status,
    error: error ? { message: error } : undefined,
  };
}

function makeCommentary(text: string): ThreadItem {
  return {
    id: "commentary-1",
    type: "agent_message",
    status: "completed",
    terminal: false,
    role: "assistant",
    text,
  };
}

function makeFinalAnswer(
  text: string,
  status: ThreadItem["status"] = "completed",
): ThreadItem {
  return {
    id: "answer-1",
    type: "agent_message",
    status,
    terminal: true,
    role: "assistant",
    text,
  };
}

function makeError(error: string): ThreadItem {
  return {
    id: "error-1",
    type: "error",
    status: "failed",
    error,
  };
}

function makeReasoning(text: string, id = "reasoning-1"): ThreadItem {
  return {
    id,
    type: "reasoning",
    status: "completed",
    text,
  };
}

function render(turn: Turn, isLatestTurn = false): HTMLDivElement {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(
      <TurnView
        turn={turn}
        onStreamFrame={() => {}}
        isLatestTurn={isLatestTurn}
      />,
    );
  });
  return container;
}

function rerender(turn: Turn, isLatestTurn = false): void {
  act(() => {
    root!.render(
      <TurnView
        turn={turn}
        onStreamFrame={() => {}}
        isLatestTurn={isLatestTurn}
      />,
    );
  });
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  act(() => {
    root?.unmount();
  });
  container?.remove();
  desktopPluginHost.setActiveConversationThread(undefined);
  root = undefined;
  container = undefined;
});

describe("TurnView", () => {
  it("keeps the conversation timeline plugin surface on each real turn", async () => {
    await desktopPluginHost.activateGeneration({
      pluginId: "test:turn-timeline",
      generation: "one",
      register(api) {
        api.registerSurface("conversation.timeline", {
          id: "turn-replacement",
          mode: "replace",
          render(context) {
            const turns = context.turns as Turn[];
            return <div data-testid="plugin-turn">{turns[0]?.id}</div>;
          },
        });
      },
    });

    const view = render(makeTurn("completed"));
    expect(view.querySelector('[data-testid="plugin-turn"]')?.textContent).toBe(
      "turn-1",
    );

    await act(async () => desktopPluginHost.unload("test:turn-timeline"));
  });

  it("exposes the active thread id on the conversation timeline surface", async () => {
    let received: Readonly<Record<string, unknown>> | undefined;
    await desktopPluginHost.activateGeneration({
      pluginId: "test:turn-timeline",
      generation: "thread-id",
      register(api) {
        api.registerSurface("conversation.timeline", {
          id: "thread-aware-turn",
          mode: "replace",
          render(context) {
            received = context;
            return <div data-thread-aware-turn>{String(context.threadId)}</div>;
          },
        });
      },
    });

    desktopPluginHost.setActiveConversationThread("thread-timeline-1");
    const view = render(makeTurn("completed"));

    expect(view.querySelector("[data-thread-aware-turn]")?.textContent).toBe("thread-timeline-1");
    expect(received).toHaveProperty("threadId", "thread-timeline-1");
    desktopPluginHost.setActiveConversationThread(undefined);
    await act(async () => desktopPluginHost.unload("test:turn-timeline"));
  });

  it("marks the turn root with the live status used by scroll-stable CSS", () => {
    const view = render(makeTurn("in_progress"));

    const turn = view.querySelector<HTMLElement>(".turn");
    expect(turn?.dataset.turnStatus).toBe("in_progress");
  });

  it("reveals actions when a fast answer first mounts already completed", () => {
    vi.useFakeTimers();
    const userItem: ThreadItem = {
      id: "user-1",
      type: "user_message",
      status: "completed",
      text: "Reply quickly.",
    };
    const view = render(makeTurn("in_progress", [userItem]), true);

    rerender(
      makeTurn("completed", [userItem, makeFinalAnswer("Done.")]),
      true,
    );
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS);
    });

    expect(
      view.querySelector(".agent-block")?.classList.contains("agent-actions-enter"),
    ).toBe(true);
  });

  it("does not replay the action entrance for a historical completed answer", () => {
    const view = render(
      makeTurn("completed", [makeFinalAnswer("Already finished.")]),
      true,
    );

    expect(view.querySelector(".agent-message-actions")).not.toBeNull();
    expect(
      view.querySelector(".agent-block")?.classList.contains("agent-actions-enter"),
    ).toBe(false);
  });

  it("keeps the stream status footprint after a live turn settles", () => {
    const userItem: ThreadItem = {
      id: "user-1",
      type: "user_message",
      status: "completed",
      text: "Stream something.",
    };
    const view = render(makeTurn("in_progress", [userItem]), true);

    // While streaming the notice row is present; no spacer yet.
    expect(view.querySelector(".turn-stream-status-spacer")).toBeNull();

    rerender(
      makeTurn("completed", [userItem, makeFinalAnswer("Settled answer.")]),
      true,
    );

    // The settled live turn reserves the row the chip occupied, so the
    // already-rendered answer does not jump on the completion frame.
    expect(view.querySelector(".turn-stream-status-spacer")).not.toBeNull();
  });

  it("does not reserve the stream footprint for a historical completed turn", () => {
    const view = render(
      makeTurn("completed", [makeFinalAnswer("Already finished.")]),
      true,
    );

    expect(view.querySelector(".turn-stream-status-spacer")).toBeNull();
  });

  it("hides named and legacy process notifications from the direct turn renderer", () => {
    const processText =
      '<process_notification>{"process_id":"proc-legacy"}</process_notification>';
    const view = render(
      makeTurn("completed", [
        {
          id: "process-named",
          type: "user_message",
          name: PROCESS_NOTIFICATION_NAME,
          text: '<process_notification>{"process_id":"proc-named"}</process_notification>',
        },
        {
          id: "process-legacy",
          type: "user_message",
          text: processText,
        },
        {
          id: "user-1",
          type: "user_message",
          text: "真正的用户消息",
        },
      ]),
    );

    expect(view.textContent).not.toContain("proc-named");
    expect(view.textContent).not.toContain("proc-legacy");
    expect(view.textContent).toContain("真正的用户消息");
    expect(view.querySelectorAll(".user-message-block")).toHaveLength(1);
  });

  it("renders a plugin-generated query through the existing user-message flow", () => {
    const view = render(
      makeTurn("completed", [
        {
          id: "plugin-update",
          type: "user_message",
          text: "子任务 太阳 已更新",
          read_only: true,
          origin: "plugin",
          origin_id: "subagent",
          cause: "subagent.completion",
          presentation_kind: "query_bubble",
        },
      ]),
    );

    expect(view.querySelectorAll(".user-message-block")).toHaveLength(1);
    expect(view.querySelector(".user-message")?.textContent).toBe("子任务 太阳 已更新");
    expect(view.querySelector(".message-edit-button")).toBeNull();
  });

  it("removes the temporary review card when the next turn begins, retaining tool history", () => {
    vi.useFakeTimers();
    const turn = makeTurn("completed", [
        {
          id: "write-1",
          type: "tool_call",
          name: "write_file",
          status: "completed",
          arguments: JSON.stringify({ path: "notes/brief.md", content: "# Brief\n" }),
          result: JSON.stringify({
            path: "notes/brief.md",
            diff: { new_file: true, lines: 1 },
          }),
        },
      ]);
    const view = render(turn, true);
    expect(view.querySelector(".turn-edit-summary-card")).not.toBeNull();
    rerender(turn, false);
    expect(view.querySelector("[inert] .turn-edit-summary-card")).not.toBeNull();
    act(() => vi.advanceTimersByTime(220));
    expect(view.querySelector(".turn-edit-summary-card")).toBeNull();
    expect(view.querySelector(".turn-edit-presentation")).toBeNull();
    expect(view.querySelector(".assistant-turn-shell")).not.toBeNull();
    expect(turn.items).toHaveLength(1);
  });

  it.each(["failed", "interrupted"] as const)(
    "keeps %s edits collapsed after execution events and available on demand",
    (status) => {
      vi.useFakeTimers();
      const turn = makeTurn(status, [{
        id: "write-1",
        type: "tool_call",
        name: "write_file",
        status: "completed",
        result: JSON.stringify({ path: "partial.ts", diff: { new_file: true, lines: 1 } }),
      }], status === "failed" ? "network connection lost" : undefined);
      const view = render(turn, true);
      const edits = view.querySelector(".turn-edit-presentation")!;
      expect(view.querySelector(".turn-edit-summary-card")).toBeNull();
      const process = view.querySelector(".assistant-turn-shell")!;
      expect(process.compareDocumentPosition(edits) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      const notice = view.querySelector(".system-event-notice");
      if (status === "failed") {
        expect(notice).not.toBeNull();
        expect(notice!.compareDocumentPosition(edits) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      }
      const editToggle = edits.querySelector<HTMLButtonElement>("button")!;
      expect(editToggle.getAttribute("aria-expanded")).toBe("false");
      act(() => editToggle.click());
      expect(editToggle.getAttribute("aria-expanded")).toBe("true");
      expect(edits.querySelector(".turn-edit-summary-card")?.textContent).toContain("partial.ts");
      rerender(turn, false);
      act(() => vi.advanceTimersByTime(220));
      expect(view.querySelector(".turn-edit-presentation")).toBeNull();
      if (notice) expect(view.contains(notice)).toBe(true);
    },
  );

  it("does not resurrect historical cards on mount", () => {
    const view = render(makeTurn("completed", [{
      id: "write-old",
      type: "tool_call",
      name: "write_file",
      status: "completed",
      result: JSON.stringify({ path: "old.ts", diff: { new_file: true, lines: 1 } }),
    }]), false);
    expect(view.querySelector(".turn-edit-presentation")).toBeNull();
  });

  it("removes old cards immediately when reduced motion is requested", () => {
    vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: true })));
    const turn = makeTurn("completed", [{
      id: "write-1", type: "tool_call", name: "write_file", status: "completed",
      result: JSON.stringify({ path: "brief.md", diff: { new_file: true, lines: 1 } }),
    }]);
    const view = render(turn, true);
    expect(view.querySelector(".turn-edit-summary-card")).not.toBeNull();
    rerender(turn, false);
    expect(view.querySelector(".turn-edit-presentation")).toBeNull();
  });

  it("does not offer retained edits when a failed turn made no changes", () => {
    const view = render(makeTurn("failed", [makeError("network connection lost")], "network connection lost"), true);
    expect(view.querySelector(".system-event-notice")).not.toBeNull();
    expect(view.querySelector(".turn-edit-presentation")).toBeNull();
  });

  it("attaches the edit summary to an answer before turn settlement", () => {
    const turn = makeTurn("in_progress", [
      {
        id: "write-1",
        type: "tool_call",
        name: "write_file",
        status: "completed",
        result: JSON.stringify({
          path: "notes/brief.md",
          diff: { new_file: true, lines: 1 },
        }),
      },
      makeFinalAnswer("done"),
    ]);
    turn.answer_ready_at = "2026-08-29T04:00:10.000Z";

    const view = render(turn, true);
    const agentBlock = view.querySelector(".agent-block");

    expect(agentBlock?.textContent).toContain("done");
    expect(agentBlock?.textContent).toContain("本轮修改 1 个文件");
    expect(agentBlock?.querySelector(".turn-edit-summary-card")).toBeTruthy();
  });

  it("buffers structural process changes briefly while keeping the current text visible", () => {
    vi.useFakeTimers();
    const view = render(
      makeTurn("in_progress", [makeCommentary("checking the files")]),
    );

    expect(view.textContent).toContain("checking the files");
    expect(view.textContent).not.toContain("查看思考过程");

    rerender(
      makeTurn("in_progress", [
        makeCommentary("checking the files"),
        makeReasoning("settled reasoning"),
      ]),
    );

    expect(view.textContent).toContain("checking the files");
    expect(view.textContent).not.toContain("查看思考过程");

    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS - 1);
    });
    expect(view.textContent).not.toContain("查看思考过程");

    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(view.textContent).toContain("查看思考过程");
  });

  it("publishes the completed final answer without the structural buffer delay", () => {
    vi.useFakeTimers();
    const view = render(
      makeTurn("in_progress", [makeFinalAnswer("partial reply", "in_progress")]),
    );

    expect(view.textContent).toContain("partial reply");

    rerender(
      makeTurn("completed", [makeFinalAnswer("partial reply and final text")]),
    );

    expect(view.textContent).toContain("partial reply and final text");
  });

  it("coalesces repeated structural changes without extending the buffer forever", () => {
    vi.useFakeTimers();
    const view = render(makeTurn("in_progress", [makeCommentary("checking")]));

    rerender(
      makeTurn("in_progress", [
        makeCommentary("checking"),
        makeReasoning("first reasoning", "reasoning-1"),
      ]),
    );
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS / 2);
    });
    expect(view.textContent).not.toContain("思考过程");

    rerender(
      makeTurn("in_progress", [
        makeCommentary("checking"),
        makeReasoning("first reasoning", "reasoning-1"),
        makeReasoning("second reasoning", "reasoning-2"),
      ]),
    );
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS / 2);
    });

    expect(view.textContent).toContain("思考过程");
  });

  it("keeps partial output without a stop notice after a resumable pause", () => {
    const view = render(
      makeTurn("interrupted", [
        makeCommentary("partial progress"),
        makeError("context canceled"),
      ]),
    );

    expect(view.textContent).toContain("partial progress");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(0);
    expect(view.textContent).not.toContain("已停止");
    expect(view.textContent).not.toContain("回复已中断");
  });

  it("renders one failure notice when a failed turn also records an error item", () => {
    const view = render(
      makeTurn(
        "failed",
        [
          makeCommentary("partial progress"),
          makeError("wait: context deadline exceeded"),
        ],
        "stream request failed: stream error (previous_response_not_found)",
      ),
    );

    expect(view.textContent).toContain("partial progress");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(1);
    expect(view.textContent).toContain("网络异常");
    expect(view.textContent).not.toContain("previous_response_not_found");
    expect(view.querySelectorAll(".turn-notice button, .turn-notice a")).toHaveLength(0);
  });

  it("renders one failure notice when an interrupted turn also records its internal error as an item", () => {
    const error = "wuu internal error: request did not complete";
    const view = render(
      makeTurn(
        "interrupted",
        [makeCommentary("partial progress"), makeError(error)],
        error,
      ),
    );

    expect(view.textContent).toContain("partial progress");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(1);
    expect(view.textContent).toContain("内部错误");
  });

  it("hides a transient stream error item while the turn is still retrying", () => {
    // A retryable attempt that failed terminally lands an error item, but
    // the turn keeps running (reconnect chip carries the cause). Rendering
    // the item here would pile up one settled error line per attempt.
    const view = render(
      makeTurn("in_progress", [
        makeCommentary("partial progress"),
        makeError("HTTP 429: Too Many Requests"),
      ]),
    );

    expect(view.querySelectorAll(".turn-notice")).toHaveLength(0);
    expect(view.textContent).not.toContain("429");
  });

  it("drops a transient stream error item once the turn recovered and completed", () => {
    // The retry succeeded: nothing about the superseded failure should stay
    // behind in the finished turn.
    const view = render(
      makeTurn("completed", [
        makeFinalAnswer("done"),
        makeError("HTTP 429: Too Many Requests"),
      ]),
    );

    expect(view.textContent).toContain("done");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(0);
    expect(view.textContent).not.toContain("429");
  });

  it("does not render a notice when a completed turn has commentary but no final answer", () => {
    vi.useFakeTimers();
    const view = render(
      makeTurn("completed", [makeCommentary("thinking out loud")]),
    );
    // The presentation buffer delays publishing the display for
    // ASSISTANT_TURN_PRESENTATION_STABILIZE_MS so the fold body has
    // time to settle. Advance past it so the chip mounts.
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS);
    });

    // The process records (commentary) are still visible in the fold.
    expect(view.textContent).toContain("thinking out loud");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(0);
  });

  it("keeps command runs out of the final answer actions", () => {
    const view = render(
      makeTurn("completed", [
        {
          id: "call-1",
          type: "tool_call",
          status: "completed",
          name: "bash",
          arguments: JSON.stringify({ command: "npm test" }),
          display: { kind: "command", capability: "command.bash" },
          result: JSON.stringify({ exit_code: 0 }),
        },
        makeFinalAnswer("done"),
      ]),
    );

    expect(view.querySelector("button:has(.lucide-square-terminal)")).toBeNull();
    expect(view.querySelectorAll(".agent-message-actions button")).toHaveLength(2);
  });

  it("settles the reconnect row inside the stream when retries are exhausted", () => {
    vi.useFakeTimers();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    const turn = makeTurn(
      "failed",
      [
        makeCommentary("partial work"),
        {
          id: "reconnect-1",
          type: "stream_reconnect",
          status: "failed",
          text: "connection reset by peer",
          reason: "network",
          retry_count: 5,
          max_retries: 5,
        },
      ],
      "network down",
    );
    act(() => {
      root!.render(
        <TurnView
          turn={turn}
          onStreamFrame={() => {}}
          isLatestTurn
        />,
      );
    });
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS);
    });

    // The failure row renders as a trailing row of the process stream,
    // announcing the stopped state and the cause without retry counts.
    const notice = container!.querySelector("aside.stream-reconnect-notice");
    expect(notice?.textContent).toContain("已停止");
    expect(notice?.textContent).toContain("网络异常");
    expect(notice?.textContent).not.toContain("次重试");
    expect(notice?.closest(".assistant-turn-shell")).not.toBeNull();
    // It stands in for the generic turn error notice.
    expect(
      container!.querySelectorAll("aside:not(.stream-reconnect-notice)"),
    ).toHaveLength(0);
  });
});

describe("TurnView optimistic placeholder", () => {
  it("shows the status label and live timer immediately for a just-sent optimistic turn", async () => {
    const { createOptimisticTurn } = await import("./ComposerMessages");
    const optimistic = createOptimisticTurn(
      { id: "queued-1", text: "帮我看看这个测试", images: [], files: [] },
      Date.now(),
    );
    const view = render(optimistic);

    // The user's message is visible right away.
    expect(view.textContent).toContain("帮我看看这个测试");
    // The process header mounts in the same frame — the user never faces
    // a bare hairline: label + elapsed timer are there from the start.
    expect(view.querySelector(".turn-process-title")?.textContent).toBe(
      "正在处理",
    );
    expect(view.querySelector(".turn-process-meta")?.textContent).toMatch(
      /^\d+s$/,
    );
  });
});
