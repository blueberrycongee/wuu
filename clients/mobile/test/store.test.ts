// Reducer semantics mirrored from desktop AppState: upserts that never lose
// local history, snapshots that never truncate streamed text, and the
// optimistic-send lifecycle.

import { describe, expect, it } from "vitest";
import type { Thread } from "@wuu/protocol";

import { AppStore } from "../src/lib/store";

function thread(partial: Partial<Thread>): Thread {
  return {
    id: "t1",
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

function seeded(): AppStore {
  const store = new AppStore();
  store.setThreads([thread({})]);
  return store;
}

describe("thread upserts", () => {
  it("keeps ordinary threads and filters archived or read-only threads", () => {
    const store = new AppStore();
    store.setThreads([
      thread({ id: "project", workspace_kind: "project" }),
      thread({ id: "scratch", workspace_kind: "scratch" }),
      thread({ id: "archived", archived: true }),
      thread({ id: "readonly", read_only: true }),
    ]);
    expect(store.getSnapshot().threads.map((item) => item.id).sort()).toEqual(["project", "scratch"]);
  });

  it("thread/updated with empty turns keeps local history", () => {
    const store = seeded();
    store.applyNotification("turn/started", {
      thread_id: "t1",
      turn: { id: "turn-1", items: [], items_view: "full", status: "in_progress" },
    });
    store.applyNotification("thread/updated", { thread: thread({ turns: [] }) });
    expect(store.getSnapshot().threads[0].turns).toHaveLength(1);
  });

  it("archiving via thread/updated removes the thread", () => {
    const store = seeded();
    store.applyNotification("thread/updated", { thread: thread({ archived: true }) });
    expect(store.getSnapshot().threads).toHaveLength(0);
  });
});

describe("turn and item ingestion", () => {
  it("tracks running state through turn lifecycle", () => {
    const store = seeded();
    store.applyNotification("turn/started", {
      thread_id: "t1",
      turn: { id: "turn-1", items: [], items_view: "full", status: "in_progress" },
    });
    expect(store.getSnapshot().threads[0].status).toBe("in_progress");
    store.applyNotification("turn/completed", {
      thread_id: "t1",
      turn: { id: "turn-1", items: [], items_view: "full", status: "completed" },
    });
    expect(store.getSnapshot().threads[0].status).toBe("idle");
  });

  it("a shorter completed snapshot never truncates streamed text", () => {
    const store = seeded();
    store.applyNotification("item/started", {
      thread_id: "t1",
      turn_id: "turn-1",
      item: { id: "i1", type: "agent_message", text: "" },
    });
    store.applyNotification("item/agentMessage/delta", {
      thread_id: "t1",
      turn_id: "turn-1",
      item_id: "i1",
      delta: "很长很长的一段流式文本",
    });
    store.applyNotification("item/completed", {
      thread_id: "t1",
      turn_id: "turn-1",
      item: { id: "i1", type: "agent_message", text: "很长" },
    });
    const item = store.getSnapshot().threads[0].turns[0].items[0];
    expect(item.text).toBe("很长很长的一段流式文本");
  });

  it("replace always wins over accumulated text", () => {
    const store = seeded();
    store.applyNotification("item/started", {
      thread_id: "t1",
      turn_id: "turn-1",
      item: { id: "i1", type: "agent_message", text: "旧的长长长长文本" },
    });
    store.applyNotification("item/agentMessage/replace", {
      thread_id: "t1",
      turn_id: "turn-1",
      item_id: "i1",
      text: "新",
    });
    expect(store.getSnapshot().threads[0].turns[0].items[0].text).toBe("新");
  });

  it("removes a provisional item when its inference attempt is reset", () => {
    const store = seeded();
    store.applyNotification("item/started", {
      thread_id: "t1",
      turn_id: "turn-1",
      item: { id: "tool-1", type: "tool_call", status: "in_progress" },
    });
    store.applyNotification("item/removed", {
      thread_id: "t1",
      turn_id: "turn-1",
      item_id: "tool-1",
    });

    expect(store.getSnapshot().threads[0].turns[0].items).toEqual([]);
  });

  it("flags unknown threads so the controller can refresh", () => {
    const store = new AppStore();
    const unknown: string[] = [];
    store.onUnknownThread = (id) => unknown.push(id);
    store.applyNotification("turn/started", {
      thread_id: "mystery",
      turn: { id: "turn-1", items: [], items_view: "full", status: "in_progress" },
    });
    expect(unknown).toEqual(["mystery"]);
  });
});

describe("optimistic sends", () => {
  const pendingSend = {
    clientId: "c1",
    threadId: "t1",
    text: "hi",
    atMs: 1,
    queued: true,
  };

  it("removed when turn/started carries its queue_id", () => {
    const store = seeded();
    store.addPending(pendingSend);
    store.applyNotification("turn/started", {
      thread_id: "t1",
      queue_id: "c1",
      turn: { id: "turn-1", items: [], items_view: "full", status: "in_progress" },
    });
    expect(store.getSnapshot().pending).toHaveLength(0);
  });

  it("removed when its user_message lands with source_id", () => {
    const store = seeded();
    store.addPending(pendingSend);
    store.applyNotification("item/completed", {
      thread_id: "t1",
      turn_id: "turn-1",
      item: { id: "i1", type: "user_message", text: "hi", source_id: "c1" },
    });
    expect(store.getSnapshot().pending).toHaveLength(0);
  });

  it("removed on turn/dequeued", () => {
    const store = seeded();
    store.addPending(pendingSend);
    store.applyNotification("turn/dequeued", { thread_id: "t1", queue_id: "c1" });
    expect(store.getSnapshot().pending).toHaveLength(0);
  });
});

describe("review fixes", () => {
  it("setThreads preserves loaded turns when the list entry has none", () => {
    const store = seeded();
    store.applyNotification("turn/started", {
      thread_id: "t1",
      turn: { id: "turn-1", items: [], items_view: "full", status: "in_progress" },
    });
    store.setThreads([thread({ turns: [] })]);
    expect(store.getSnapshot().threads[0].turns).toHaveLength(1);
  });

  it("upsertTurn uses server timestamps and only moves updated_at forward", () => {
    const store = new AppStore();
    store.setThreads([thread({ updated_at: "2026-07-07T10:00:00Z" })]);
    // A redelivered OLD turn must not bump the thread above newer chats.
    store.applyNotification("turn/completed", {
      thread_id: "t1",
      turn: {
        id: "turn-old",
        items: [],
        items_view: "full",
        status: "completed",
        completed_at: "2026-07-01T00:00:00Z",
      },
    });
    expect(store.getSnapshot().threads[0].updated_at).toBe("2026-07-07T10:00:00Z");
    // A genuinely newer turn moves it forward to the SERVER time.
    store.applyNotification("turn/completed", {
      thread_id: "t1",
      turn: {
        id: "turn-new",
        items: [],
        items_view: "full",
        status: "completed",
        completed_at: "2026-07-07T12:00:00Z",
      },
    });
    expect(store.getSnapshot().threads[0].updated_at).toBe("2026-07-07T12:00:00Z");
  });

  it("turn snapshots never drop locally-known items", () => {
    const store = seeded();
    store.applyNotification("item/completed", {
      thread_id: "t1",
      turn_id: "turn-1",
      item: { id: "i-local", type: "agent_message", text: "先到的消息" },
    });
    store.applyNotification("turn/completed", {
      thread_id: "t1",
      turn: {
        id: "turn-1",
        items: [{ id: "i-snap", type: "user_message", text: "快照里的" }],
        items_view: "full",
        status: "completed",
      },
    });
    const items = store.getSnapshot().threads[0].turns[0].items;
    expect(items.map((i) => i.id).sort()).toEqual(["i-local", "i-snap"]);
  });

  it("seedLastViewed restores cursors without clobbering newer state", () => {
    const store = new AppStore();
    store.setThreads([
      thread({ turns: [{ id: "turn-9", items: [], items_view: "full", status: "completed" }] }),
    ]);
    store.markViewed("t1");
    store.seedLastViewed({ t1: "turn-1", other: "turn-2" });
    expect(store.getSnapshot().lastViewed).toEqual({ t1: "turn-9", other: "turn-2" });
  });
});

describe("unread cursor", () => {
  it("markViewed advances to the newest completed turn", () => {
    const store = new AppStore();
    store.setThreads([
      thread({
        turns: [
          { id: "turn-1", items: [], items_view: "full", status: "completed" },
          { id: "turn-2", items: [], items_view: "full", status: "in_progress" },
        ],
      }),
    ]);
    store.markViewed("t1");
    expect(store.getSnapshot().lastViewed["t1"]).toBe("turn-1");
  });
});
