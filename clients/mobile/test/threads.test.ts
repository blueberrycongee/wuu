import { describe, expect, it } from "vitest";
import type { Thread } from "@wuu/protocol";

import {
  isThreadRunning,
  isThreadUnread,
  latestCompletedTurnID,
  sortThreads,
  threadDisplayTitle,
} from "../src/lib/threads";

function thread(partial: Partial<Thread>): Thread {
  return {
    id: "t",
    preview: "",
    model_provider: "p",
    model: "m",
    cwd: "/",
    status: "idle",
    created_at: "2026-07-07T00:00:00Z",
    updated_at: "2026-07-07T00:00:00Z",
    turns: [],
    ...partial,
  };
}

describe("thread list helpers", () => {
  it("detects running threads and derives display titles", () => {
    expect(isThreadRunning(thread({ status: "in_progress" }))).toBe(true);
    expect(
      isThreadRunning(
        thread({ turns: [{ id: "x", items: [], items_view: "full", status: "in_progress" }] }),
      ),
    ).toBe(true);
    expect(threadDisplayTitle(thread({ title: " 项目会话 " }))).toBe("项目会话");
    expect(threadDisplayTitle(thread({ preview: "早上好" }))).toBe("早上好");
    expect(threadDisplayTitle(thread({}))).toBe("未命名对话");
  });

  it("sorts running first, then finished threads by activity", () => {
    const runningOld = thread({ id: "r-old", status: "in_progress", created_at: "2026-07-01T00:00:00Z" });
    const runningNew = thread({ id: "r-new", status: "in_progress", created_at: "2026-07-06T00:00:00Z" });
    const doneOld = thread({ id: "d-old", updated_at: "2026-07-02T00:00:00Z" });
    const doneNew = thread({ id: "d-new", updated_at: "2026-07-05T00:00:00Z" });

    expect(sortThreads([doneOld, runningOld, doneNew, runningNew]).map(({ id }) => id)).toEqual([
      "r-new",
      "r-old",
      "d-new",
      "d-old",
    ]);
  });
});

describe("unread cursor", () => {
  const finished = thread({
    id: "t1",
    turns: [
      { id: "turn-1", items: [], items_view: "full", status: "completed" },
      { id: "turn-2", items: [], items_view: "full", status: "completed" },
    ],
  });

  it("keys on the newest completed turn", () => {
    expect(latestCompletedTurnID(finished)).toBe("turn-2");
    expect(isThreadUnread(finished, {})).toBe(true);
    expect(isThreadUnread(finished, { t1: "turn-2" })).toBe(false);
  });

  it("does not mark running threads unread", () => {
    const running = thread({
      id: "t1",
      turns: [
        { id: "turn-1", items: [], items_view: "full", status: "completed" },
        { id: "turn-2", items: [], items_view: "full", status: "in_progress" },
      ],
    });
    expect(isThreadUnread(running, {})).toBe(false);
    expect(latestCompletedTurnID(running)).toBe("turn-1");
  });
});
