import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import type { Thread, ThreadItem, Turn } from "../shared/protocol";
import { initialState, reduceServerEvent, type AppState } from "./AppState";
import { ASSISTANT_TURN_PRESENTATION_STABILIZE_MS } from "./AssistantTurnPresentation";
import { TurnView } from "./TurnView";

it("shows one answer after completion replaces a cached item with a different id", async () => {
  vi.useFakeTimers();
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const text = "配置页已完成中文化。\n\n测试通过，尚未提交。";
  const cachedAnswer: ThreadItem = {
    id: "turn-1-item-9",
    type: "agent_message",
    status: "completed",
    terminal: true,
    text,
  };
  const turn: Turn = {
    id: "turn-1",
    status: "in_progress",
    items_view: "full",
    items: [cachedAnswer],
  };
  const thread: Thread = {
    id: "thread-1",
    status: "in_progress",
    preview: "",
    model_provider: "test",
    model: "test",
    cwd: "/repo",
    created_at: "2026-09-15T00:00:00Z",
    updated_at: "2026-09-15T00:00:00Z",
    turns: [turn],
  };
  let state: AppState = { ...initialState, thread, threads: [thread] };
  const render = () => root.render(
    <TurnView turn={state.thread!.turns[0]} onStreamFrame={() => {}} isLatestTurn />,
  );
  const expectOneAnswer = () => {
    expect(container.querySelectorAll(".turn-answer-body .agent-block")).toHaveLength(1);
    expect(container.textContent?.split("配置页已完成中文化。")).toHaveLength(2);
    expect(container.textContent?.split("测试通过，尚未提交。")).toHaveLength(2);
  };
  try {
    act(render);
    expectOneAnswer();

    const completedTurn: Turn = {
      ...turn,
      status: "completed",
      items: [{ ...cachedAnswer, id: "turn-1-item-8" }],
    };
    // The full completion result is authoritative, unlike an in-flight cache.
    // Replaying it must not retain both identities of the same answer.
    for (let delivery = 0; delivery < 2; delivery++) {
      act(() => {
        state = reduceServerEvent(state, {
          kind: "notification",
          workdir: "/repo",
          message: {
            method: "turn/completed",
            params: { thread_id: thread.id, turn: completedTurn },
          },
        });
        render();
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS + 1);
      });
      expectOneAnswer();
      expect(state.thread!.turns[0].items).toEqual(completedTurn.items);
    }
  } finally {
    act(() => root.unmount());
    container.remove();
    vi.useRealTimers();
  }
});
