import { describe, expect, it } from "vitest";
import type { Thread, Turn } from "../shared/protocol";
import {
  MAX_CACHED_CONVERSATION_PANES,
  conversationPaneRenderWeight,
  retainCachedConversationPaneThreads,
  selectCachedConversationPaneIDs,
} from "./ConversationPaneCache";

function thread(id: string, turnCount: number, runningTurnIndex = -1): Thread {
  return {
    id,
    title: id,
    preview: id,
    model_provider: "test",
    model: "test",
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: Array.from({ length: turnCount }, (_, index) => ({
      id: `${id}-turn-${index}`,
      status: index === runningTurnIndex ? "in_progress" : "completed",
      items_view: "full",
      items: [],
    })) as Turn[],
  } as Thread;
}

function select(threads: Thread[], activeThreadID = threads[0]?.id): string[] {
  const ids = threads.map((item) => item.id);
  return selectCachedConversationPaneIDs({
    activeThreadID,
    previousThreadIDs: ids,
    openThreadIDs: new Set(ids),
    threadsByID: new Map(threads.map((item) => [item.id, item])),
  });
}

describe("conversation pane cache", () => {
  it("keeps at most eight lightweight panes", () => {
    const threads = Array.from({ length: 10 }, (_, index) =>
      thread(`thread-${index}`, 10),
    );

    expect(select(threads)).toEqual(
      threads.slice(0, MAX_CACHED_CONVERSATION_PANES).map((item) => item.id),
    );
  });

  it("stops retaining long panes when the render budget is full", () => {
    const threads = Array.from({ length: 5 }, (_, index) =>
      thread(`thread-${index}`, 80),
    );

    expect(conversationPaneRenderWeight(threads[0])).toBe(80);
    expect(select(threads)).toEqual(["thread-0", "thread-1", "thread-2"]);
  });

  it("charges collapsed history at a lower weight while keeping running turns full", () => {
    expect(conversationPaneRenderWeight(thread("settled", 100))).toBe(46);
    expect(conversationPaneRenderWeight(thread("running", 100, 0))).toBe(47);
  });

  it("always retains the active pane even when it exceeds the budget", () => {
    expect(select([thread("active", 3_000), thread("recent", 1)])).toEqual([
      "active",
    ]);
  });

  it("keeps the previous pane when the open LRU still includes it", () => {
    const active = thread("active", 1);
    const previous = thread("previous", 1);

    expect(
      selectCachedConversationPaneIDs({
        activeThreadID: active.id,
        previousThreadIDs: [previous.id],
        openThreadIDs: new Set([previous.id, active.id]),
        threadsByID: new Map([
          [active.id, active],
          [previous.id, previous],
        ]),
      }),
    ).toEqual([active.id, previous.id]);
  });

  it("drops closed panes from the previous LRU history", () => {
    const active = thread("active", 1);
    const open = thread("open", 1);
    const closed = thread("closed", 1);

    expect(
      selectCachedConversationPaneIDs({
        activeThreadID: active.id,
        previousThreadIDs: [closed.id, open.id],
        openThreadIDs: new Set([active.id, open.id]),
        threadsByID: new Map([
          [active.id, active],
          [open.id, open],
          [closed.id, closed],
        ]),
      }),
    ).toEqual([active.id, open.id]);
  });

  it("retains an open pane snapshot when a runtime load replaces the thread list", () => {
    const source = thread("source-workspace", 40);
    const target = thread("target-workspace", 20);

    const retained = retainCachedConversationPaneThreads({
      threadIDs: [target.id, source.id],
      currentThreadsByID: new Map([[target.id, target]]),
      previousThreadsByID: new Map([[source.id, source]]),
    });

    expect([...retained.keys()]).toEqual([target.id, source.id]);
    expect(retained.get(source.id)).toBe(source);
    expect(retained.get(target.id)).toBe(target);
  });

  it("drops snapshots once their pane is evicted", () => {
    const retained = thread("retained", 1);
    const evicted = thread("evicted", 1);

    expect(
      [...retainCachedConversationPaneThreads({
        threadIDs: [retained.id],
        currentThreadsByID: new Map(),
        previousThreadsByID: new Map([
          [retained.id, retained],
          [evicted.id, evicted],
        ]),
      }).keys()],
    ).toEqual([retained.id]);
  });
});
