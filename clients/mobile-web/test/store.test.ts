// Store ordering contract, mirrored from clients/mobile/test/store.test.ts:
// merges never reorder a surviving row below content that happened later.

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

describe("turn item ordering", () => {
  it("a snapshot missing a locally-known item keeps it at its server position", () => {
    const store = new AppStore();
    store.setThreads([thread({})]);
    for (const item of [
      { id: "turn-1-item-1", type: "user_message", text: "A" },
      { id: "turn-1-item-2", type: "agent_message", text: "partial" },
      { id: "turn-1-item-3", type: "user_message", text: "B" },
      { id: "turn-1-item-4", type: "agent_message", text: "answer to B" },
    ]) {
      store.applyNotification("item/completed", { thread_id: "t1", turn_id: "turn-1", item });
    }
    store.applyNotification("turn/completed", {
      thread_id: "t1",
      turn: {
        id: "turn-1",
        items: [
          { id: "turn-1-item-1", type: "user_message", text: "A" },
          { id: "turn-1-item-2", type: "agent_message", text: "partial" },
          { id: "turn-1-item-4", type: "agent_message", text: "answer to B" },
        ],
        items_view: "full",
        status: "completed",
      },
    });
    const items = store.getSnapshot().threads[0].turns[0].items;
    expect(items.map((i) => i.id)).toEqual([
      "turn-1-item-1",
      "turn-1-item-2",
      "turn-1-item-3",
      "turn-1-item-4",
    ]);
  });
});
