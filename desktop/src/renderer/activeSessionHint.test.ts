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

  it("ranks failed > needs_review > running > unread > idle", () => {
    const failed = thread({
      id: "failed",
      status: "idle",
      title: "失败",
      updated_at: "2026-05-05T00:00:00Z",
      turns: [turn([agentMessage({ text: "出错了" })])],
    });
    const review = thread({
      id: "review",
      status: "idle",
      updated_at: "2026-05-04T00:00:00Z",
      turns: [turn([agentMessage({ text: "需要批准权限" })])],
    });
    const running = thread({
      id: "running",
      status: "in_progress",
      updated_at: "2026-05-03T00:00:00Z",
      turns: [turn([agentMessage({ text: "正在执行" })])],
    });
    const unread = thread({
      id: "unread",
      status: "idle",
      updated_at: "2026-05-02T00:00:00Z",
      turns: [turn([agentMessage({ text: "未读消息" })])],
    });
    const idle = thread({
      id: "idle",
      status: "idle",
      updated_at: "2026-05-01T00:00:00Z",
      turns: [turn([agentMessage({ text: "空闲" })])],
    });
    const all = [idle, unread, running, review, failed];
    expect(deriveActiveSessionHints({ threads: all })[0]?.thread_id).toBe("failed");

    const withoutFailures = all.filter((t) => t.id !== "failed");
    expect(deriveActiveSessionHints({ threads: withoutFailures })[0]?.thread_id).toBe(
      "review",
    );
    const withoutReview = withoutFailures.filter((t) => t.id !== "review");
    expect(deriveActiveSessionHints({ threads: withoutReview })[0]?.thread_id).toBe(
      "running",
    );
    const withoutRunning = withoutReview.filter((t) => t.id !== "running");
    // Plain idle threads outrank each other only by updated_at, so without an
    // unread set the first non-running leftover wins on recency.
    expect(
      deriveActiveSessionHints({ threads: withoutRunning })[0]?.thread_id,
    ).toBe("unread");

    // Same candidates but flagged as unread → the unread idle outranks the
    // plain idle.
    expect(
      deriveActiveSessionHints({
        threads: withoutRunning,
        unreadThreadIDs: new Set(["unread"]),
      })[0]?.thread_id,
    ).toBe("unread");
    expect(
      deriveActiveSessionHints({
        threads: [unread, idle],
        unreadThreadIDs: new Set(["idle"]),
      })[0]?.thread_id,
    ).toBe("idle");
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

  it("only flags needs_review once the thread has settled", () => {
    const running = thread({
      id: "running-with-permission-text",
      status: "in_progress",
      turns: [turn([agentMessage({ text: "正在请求权限批准" })])],
    });
    const hints = deriveActiveSessionHints({ threads: [running] });
    expect(hints[0]?.status).toBe("running");
    expect(hints[0]?.attention).toBe(false);
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
