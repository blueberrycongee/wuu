import { describe, expect, it, vi } from "vitest";
import type { CodexPetHint, ServerEvent } from "../shared/protocol";
import { CodexPetActivity } from "./codexPetActivity";

// Failure cases the tracker exists to prevent:
// - a blocking question or approval leaves no trace on the pet while the
//   main window is closed, or leaves it stuck "waiting" after it resolves or
//   its core exits;
// - non-blocking offers, subagent turns, ephemeral helper turns, or turns
//   handed to auto-continuation make the pet celebrate or panic;
// - a user stop reads as a failure;
// - with the main window closed the bubble freezes on stale rows, and once it
//   reopens, event patches override the renderer's authoritative rows.

function notification(method: string, params: unknown, workdir = "/w"): ServerEvent {
  return { workdir, kind: "notification", message: { method, params } };
}

function completed(threadID: string, status: string, extra: Record<string, unknown> = {}): ServerEvent {
  return notification("turn/completed", {
    thread_id: threadID,
    turn: { id: "turn-1", status },
    content: "Final answer\nwith details",
    ...extra,
  });
}

function question(requestID: string, threadID: string, mode?: "ask" | "offer"): ServerEvent {
  return notification("user-question/requested", {
    request: { request_id: requestID, thread_id: threadID, turn_id: "turn-1", questions: [], mode },
  });
}

const hint: CodexPetHint = {
  thread_id: "thread-a",
  title: "Refactor",
  status: "running",
  preview: "Running tests",
  attention: false,
  updated_at: 1,
};

function tracker() {
  const listener = { onMood: vi.fn(), onReaction: vi.fn(), onHints: vi.fn() };
  return { activity: new CodexPetActivity(listener), listener };
}

describe("CodexPetActivity", () => {
  it("waits on blocking questions until they resolve or their core exits", () => {
    const { activity } = tracker();
    activity.setRunningThreads([{ workdir: "/w", thread_id: "thread-a" }]);
    expect(activity.mood).toBe("running");

    activity.handleServerEvent(question("q-offer", "thread-a", "offer"));
    expect(activity.mood).toBe("running");

    activity.handleServerEvent(question("q-1", "thread-a"));
    expect(activity.mood).toBe("waiting");
    activity.handleServerEvent(notification("user-question/resolved", { request_id: "q-1" }));
    expect(activity.mood).toBe("running");

    activity.handleServerEvent(question("q-2", "thread-a"));
    activity.handleServerEvent({ workdir: "/w", kind: "server-exit", code: 1, message: "" });
    activity.setRunningThreads([]);
    expect(activity.mood).toBe("idle");
  });

  it("ignores running ephemeral threads", () => {
    const { activity } = tracker();
    activity.handleServerEvent(notification("thread/started", { thread: { id: "helper", ephemeral: true } }));
    activity.setRunningThreads([{ workdir: "/w", thread_id: "helper" }]);
    expect(activity.mood).toBe("idle");
  });

  it("reacts only to settled top-level turns", () => {
    const { activity, listener } = tracker();
    activity.handleServerEvent(notification("thread/started", { thread: { id: "child", parent_id: "thread-a" } }));
    activity.handleServerEvent(completed("child", "completed"));
    activity.handleServerEvent(completed("thread-a", "completed", { awaiting_auto_continuation: true }));
    activity.handleServerEvent(notification("turn/error", {
      thread_id: "thread-a",
      turn_id: "turn-1",
      error: "stopped",
      turn: { id: "turn-1", status: "interrupted" },
    }));
    expect(listener.onReaction).not.toHaveBeenCalled();

    activity.handleServerEvent(completed("thread-a", "completed"));
    activity.handleServerEvent(notification("turn/error", {
      thread_id: "thread-a",
      turn_id: "turn-2",
      error: "rate limited",
      turn: { id: "turn-2", status: "failed" },
    }));
    expect(listener.onReaction.mock.calls).toEqual([["review"], ["failed"]]);
  });

  it("patches renderer rows from events only while the renderer is detached", () => {
    const { activity } = tracker();
    activity.setRendererAttached(true);
    activity.setRendererHints([hint]);
    activity.handleServerEvent(completed("thread-a", "completed"));
    expect(activity.hints()).toEqual([hint]);

    activity.setRendererAttached(false);
    activity.handleServerEvent(question("q-1", "thread-a"));
    expect(activity.hints()[0]).toMatchObject({ status: "needs_review", attention: true });
    activity.handleServerEvent(notification("user-question/resolved", { request_id: "q-1" }));
    activity.handleServerEvent(completed("thread-a", "completed"));
    expect(activity.hints()[0]).toMatchObject({
      status: "done",
      attention: true,
      preview: "Final answer with details",
    });

    activity.setRendererAttached(true);
    expect(activity.hints()).toEqual([hint]);
  });
});
