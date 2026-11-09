import { describe, expect, it } from "vitest";

import type { Thread, ThreadItem, Turn } from "../shared/protocol";
import {
  deriveActiveSessionHints,
  latestAgentMessageText,
} from "./activeSessionHint";

// Fixture helpers keep the tests focused on the selection logic instead of
// TypeScript boilerplate. Each builder drops a raw object so we can hand
// fields like `text: ""` to express placeholder items without TS noise.

function agentMessage(partial: Partial<ThreadItem> & { text: string }): ThreadItem {
  return {
    id: partial.id ?? "msg",
    type: "agent_message",
    terminal: partial.terminal,
    status: partial.status,
    text: partial.text,
  };
}

function turn(items: ThreadItem[], partial: Partial<Turn> = {}): Turn {
  return {
    id: partial.id ?? "turn",
    items,
    items_view: "full",
    status: partial.status ?? "completed",
    ...partial,
  };
}

function thread(partial: Partial<Thread> = {}): Thread {
  return {
    id: partial.id ?? "t",
    preview: partial.preview ?? "",
    title: partial.title ?? "对话",
    model_provider: partial.model_provider ?? "wuu",
    model: partial.model ?? "model",
    cwd: partial.cwd ?? "/tmp",
    status: partial.status ?? "idle",
    created_at: partial.created_at ?? "2026-01-01T00:00:00Z",
    updated_at: partial.updated_at ?? "2026-01-01T00:00:00Z",
    turns: partial.turns ?? [],
    ...partial,
  };
}

describe("latestAgentMessageText", () => {
  it("returns the freshest commentary in the latest turn", () => {
    const t = thread({
      turns: [
        turn([
          agentMessage({ id: "old", text: "更早的回复", terminal: false }),
        ]),
        turn([
          agentMessage({ id: "m1", text: "分析问题中…", terminal: false, status: "in_progress" }),
          agentMessage({ id: "m2", text: "正在读取文件", terminal: false, status: "completed" }),
        ]),
      ],
    });
    expect(latestAgentMessageText(t)).toBe("正在读取文件");
  });

  it("skips empty placeholders so the bubble shows the next-newest text", () => {
    const t = thread({
      turns: [
        turn([
          agentMessage({ id: "stale", text: "实际内容", terminal: false }),
          agentMessage({ id: "fresh", text: "", terminal: false, status: "in_progress" }),
        ]),
      ],
    });
    expect(latestAgentMessageText(t)).toBe("实际内容");
  });

  it("walks across turns when the latest one carries no agent messages yet", () => {
    const t = thread({
      turns: [
        turn([
          agentMessage({ id: "earlier-final", text: "上一轮结论", terminal: true, status: "completed" }),
        ]),
        turn([
          // Tool calls only; no assistant text yet in this turn.
        ] as ThreadItem[]),
      ],
    });
    expect(latestAgentMessageText(t)).toBe("上一轮结论");
  });

  it("returns the latest final_answer once the model finishes a turn", () => {
    const t = thread({
      turns: [
        turn([
          agentMessage({ id: "commentary", text: "先写注释", terminal: false, status: "completed" }),
          agentMessage({ id: "final", text: "完成后的回答", terminal: true, status: "completed" }),
        ]),
      ],
    });
    expect(latestAgentMessageText(t)).toBe("完成后的回答");
  });

  it("ignores non-agent_message items entirely", () => {
    const t = thread({
      turns: [
        turn([
          { id: "tool", type: "tool_call", text: "忽略我" } as ThreadItem,
          agentMessage({ id: "reply", text: "我是回复", terminal: false }),
        ]),
      ],
    });
    expect(latestAgentMessageText(t)).toBe("我是回复");
  });

  it("returns null when no agent_message items exist anywhere", () => {
    const t = thread({
      turns: [
        turn([{ id: "tool", type: "tool_call", text: "tool only" } as ThreadItem]),
      ],
    });
    expect(latestAgentMessageText(t)).toBeNull();
  });

  it("returns null when there are no turns at all", () => {
    expect(latestAgentMessageText(thread({ turns: [] }))).toBeNull();
  });
});

describe("deriveActiveSessionHints", () => {
  it("filters out archived threads", () => {
    const visible = thread({
      id: "live",
      updated_at: "2026-05-02T00:00:00Z",
      turns: [turn([agentMessage({ text: "可见" })])],
    });
    const archived = thread({
      id: "dead",
      archived: true,
      updated_at: "2026-05-01T00:00:00Z",
      turns: [turn([agentMessage({ text: "不应被选中" })])],
    });
    const hints = deriveActiveSessionHints({ threads: [archived, visible] });
    expect(hints.map((hint) => hint.thread_id)).toEqual(["live"]);
    expect(hints[0]?.preview).toBe("可见");
  });

  it("uses the latest agent message as preview and never falls back to thread.preview", () => {
    // The bubble must surface the latest stable agent_message text only.
    // Thread.preview typically carries the first turn's user query. Surfacing
    // that as latest commentary is the failure mode this ranking avoids.
    // A thread with no agent_message is omitted rather than filled from that field.
    const withItems = thread({
      id: "items",
      turns: [turn([agentMessage({ text: "实时文本" })])],
      preview: "聚合后的简介",
    });
    const withStaticOnly = thread({
      id: "static",
      turns: [turn([] as ThreadItem[])],
      preview: "降级到 thread.preview",
    });
    expect(deriveActiveSessionHints({ threads: [withItems] })[0]?.preview).toBe(
      "实时文本",
    );
    expect(deriveActiveSessionHints({ threads: [withStaticOnly] })).toEqual([]);
  });

  it("ranks needs_review > failed > unread > running > idle", () => {
    const review = thread({
      id: "review",
      status: "in_progress",
      updated_at: "2026-05-01T00:00:00Z",
      turns: [turn([agentMessage({ text: "要不要继续？" })], { status: "in_progress" })],
    });
    const failed = thread({
      id: "failed",
      updated_at: "2026-05-02T00:00:00Z",
      turns: [turn([agentMessage({ text: "读取中" })], { status: "failed" })],
    });
    const unread = thread({
      id: "unread",
      updated_at: "2026-05-03T00:00:00Z",
      turns: [turn([agentMessage({ text: "做完了" })])],
    });
    const running = thread({
      id: "running",
      status: "in_progress",
      updated_at: "2026-05-04T00:00:00Z",
      turns: [turn([agentMessage({ text: "正在执行" })], { status: "in_progress" })],
    });
    const idle = thread({
      id: "idle",
      updated_at: "2026-05-05T00:00:00Z",
      turns: [turn([agentMessage({ text: "空闲" })])],
    });
    const hints = deriveActiveSessionHints(
      {
        threads: [idle, running, unread, failed, review],
        unreadThreadIDs: new Set(["unread"]),
        waitingThreadIDs: new Set(["review"]),
      },
      5,
    );
    expect(hints.map((hint) => [hint.thread_id, hint.status, hint.attention])).toEqual([
      ["review", "needs_review", true],
      ["failed", "failed", true],
      ["unread", "done", true],
      ["running", "running", false],
      ["idle", "idle", false],
    ]);
  });

  it("does not infer status from the wording of an answer", () => {
    const t = thread({
      id: "talks-about-errors",
      turns: [turn([agentMessage({ text: "Fixed the error; 需要你批准权限 review" })])],
    });
    expect(deriveActiveSessionHints({ threads: [t] })[0]?.status).toBe("idle");
  });

  it("explains a failed turn and clears it once a later turn succeeds", () => {
    const failedTurn = turn([agentMessage({ text: "开始读取" })], {
      id: "t1",
      status: "failed",
      error: { message: "Rate limit\nexceeded" },
    });
    const failed = thread({ id: "f", turns: [failedTurn] });
    expect(deriveActiveSessionHints({ threads: [failed] })[0]).toMatchObject({
      status: "failed",
      preview: "Rate limit exceeded",
    });

    // A failed internal turn (title generation, compaction) is not the user's.
    const internalFailure = thread({
      id: "i",
      turns: [
        turn([agentMessage({ text: "结论" })], { id: "t1" }),
        turn([], { id: "t2", kind: "internal", status: "failed", error: { message: "x" } }),
      ],
    });
    expect(deriveActiveSessionHints({ threads: [internalFailure] })[0]?.status).toBe("idle");

    const retried = thread({
      id: "r",
      turns: [failedTurn, turn([agentMessage({ text: "重试成功" })], { id: "t2" })],
    });
    expect(deriveActiveSessionHints({ threads: [retried] })[0]).toMatchObject({
      status: "idle",
      preview: "重试成功",
    });
  });

  it("breaks priority ties with updated_at desc", () => {
    const newer = thread({
      id: "newer",
      updated_at: "2026-05-02T00:00:00Z",
      turns: [turn([agentMessage({ text: "新的" })])],
    });
    const older = thread({
      id: "older",
      updated_at: "2026-05-01T00:00:00Z",
      turns: [turn([agentMessage({ text: "旧的" })])],
    });
    expect(
      deriveActiveSessionHints({ threads: [older, newer] })[0]?.thread_id,
    ).toBe("newer");
  });

  it("lets the focused thread win ties via the +0.5 priority bump", () => {
    const focused = thread({
      id: "focused",
      updated_at: "2026-05-01T00:00:00Z",
      turns: [turn([agentMessage({ text: "f" })])],
    });
    const background = thread({
      id: "background",
      updated_at: "2026-05-02T00:00:00Z", // strictly newer
      turns: [turn([agentMessage({ text: "b" })])],
    });
    expect(
      deriveActiveSessionHints({
        threads: [focused, background],
        thread: focused,
      })[0]?.thread_id,
    ).toBe("focused");
    // Without the focus bump, recency would pick background.
    expect(
      deriveActiveSessionHints({ threads: [focused, background] })[0]?.thread_id,
    ).toBe("background");
  });

  it("returns the ranked top threads capped at three", () => {
    const make = (id: string, updatedAt: string, text: string, status: Thread["status"] = "idle") =>
      thread({
        id,
        status,
        updated_at: updatedAt,
        turns: [turn([agentMessage({ text })])],
      });
    const threads = [
      make("a", "2026-05-01T00:00:00Z", "一"),
      make("b", "2026-05-02T00:00:00Z", "二"),
      make("c", "2026-05-03T00:00:00Z", "三"),
      make("d", "2026-05-04T00:00:00Z", "四"),
      make("running", "2026-05-01T00:00:00Z", "跑着", "in_progress"),
    ];
    const hints = deriveActiveSessionHints({ threads });
    // Cap at 3; the running thread outranks all idles, then recency.
    expect(hints.map((h) => h.thread_id)).toEqual(["running", "d", "c"]);
  });

  it("omits threads without commentary instead of pushing empty rows", () => {
    const silent = thread({
      id: "silent",
      status: "in_progress",
      updated_at: "2026-05-09T00:00:00Z",
      turns: [],
    });
    const talking = thread({
      id: "talking",
      updated_at: "2026-05-01T00:00:00Z",
      turns: [turn([agentMessage({ text: "有话说" })])],
    });
    const hints = deriveActiveSessionHints({ threads: [silent, talking] });
    expect(hints.map((h) => h.thread_id)).toEqual(["talking"]);
  });

  it("returns an empty list when nothing has commentary", () => {
    const silent = thread({ id: "s", turns: [] });
    expect(deriveActiveSessionHints({ threads: [silent] })).toEqual([]);
    expect(deriveActiveSessionHints({ threads: [] })).toEqual([]);
  });
});
