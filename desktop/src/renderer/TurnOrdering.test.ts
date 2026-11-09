import { describe, expect, it } from "vitest";
import type { ThreadItem, Turn } from "../shared/protocol";
import {
  mergeTurnItemsInOrder,
  orderedTurnItems,
  upsertTurnItemInOrder,
} from "./TurnOrdering";

function item(id: string, type: ThreadItem["type"] = "tool_call"): ThreadItem {
  return {
    id,
    type,
    status: "in_progress",
  };
}

function turn(items: ThreadItem[]): Turn {
  return {
    id: "thread-turn-0001",
    items,
    items_view: "full",
    status: "in_progress",
  };
}

describe("turn item ordering", () => {
  it("keeps app-server item ids in creation order", () => {
    expect(
      orderedTurnItems([
        item("thread-turn-0001-item-3"),
        item("thread-turn-0001-item-1"),
        item("thread-turn-0001-item-2"),
      ]).map((entry) => entry.id),
    ).toEqual([
      "thread-turn-0001-item-1",
      "thread-turn-0001-item-2",
      "thread-turn-0001-item-3",
    ]);
  });

  it("orders items when batched item events arrive out of sequence", () => {
    let current = turn([item("thread-turn-0001-item-1", "user_message")]);
    current = {
      ...current,
      items: upsertTurnItemInOrder(current, item("thread-turn-0001-item-4")),
    };
    current = {
      ...current,
      items: upsertTurnItemInOrder(
        current,
        item("thread-turn-0001-item-2", "agent_message"),
      ),
    };
    current = {
      ...current,
      items: upsertTurnItemInOrder(current, item("thread-turn-0001-item-3")),
    };

    expect(current.items.map((entry) => entry.id)).toEqual([
      "thread-turn-0001-item-1",
      "thread-turn-0001-item-2",
      "thread-turn-0001-item-3",
      "thread-turn-0001-item-4",
    ]);
  });

  it("uses completed turn snapshots to correct prior render order", () => {
    const previous = turn([
      item("thread-turn-0001-item-1", "user_message"),
      item("thread-turn-0001-item-4"),
      item("thread-turn-0001-item-2", "agent_message"),
      item("thread-turn-0001-item-3"),
    ]);
    const next = turn([
      item("thread-turn-0001-item-1", "user_message"),
      item("thread-turn-0001-item-2", "agent_message"),
      item("thread-turn-0001-item-3"),
      item("thread-turn-0001-item-4"),
    ]);

    expect(mergeTurnItemsInOrder(previous, next).map((entry) => entry.id)).toEqual(
      [
        "thread-turn-0001-item-1",
        "thread-turn-0001-item-2",
        "thread-turn-0001-item-3",
        "thread-turn-0001-item-4",
      ],
    );
  });

  it("preserves order for nonstandard ids", () => {
    expect(
      orderedTurnItems([
        item("custom-b"),
        item("thread-turn-0001-item-1"),
        item("custom-a"),
      ]).map((entry) => entry.id),
    ).toEqual(["custom-b", "thread-turn-0001-item-1", "custom-a"]);
  });

  it.each(["completed", "interrupted", "failed"] as const)(
    "replaces obsolete local items with the full %s snapshot",
    (status) => {
      const oldAnswer: ThreadItem = {
        ...item("thread-turn-0001-item-9", "agent_message"),
        status: "completed",
        terminal: true,
        text: "已完成。\n\n测试通过，尚未提交。",
      };
      const answer = { ...oldAnswer, id: "thread-turn-0001-item-8" };
      const user = item("thread-turn-0001-item-1", "user_message");
      const previous = turn([user, oldAnswer]);
      const next: Turn = { ...turn([user, answer]), status };

      expect(mergeTurnItemsInOrder(previous, next)).toEqual([user, answer]);
      expect(previous.items).toEqual([user, oldAnswer]);
    },
  );

  it("retains local items while an in-progress snapshot is catching up", () => {
    const user = item("thread-turn-0001-item-1", "user_message");
    const live = item("thread-turn-0001-item-2", "agent_message");
    expect(mergeTurnItemsInOrder(turn([user, live]), turn([user]))).toEqual([
      user,
      live,
    ]);
  });

  it("preserves intentional repeated text in the authoritative completed snapshot", () => {
    const first: ThreadItem = {
      ...item("thread-turn-0001-item-2", "agent_message"),
      status: "completed",
      terminal: true,
      text: "已完成。",
    };
    const second = { ...first, id: "thread-turn-0001-item-3" };
    const next: Turn = { ...turn([first, second]), status: "completed" };
    expect(mergeTurnItemsInOrder(turn([first]), next)).toEqual([first, second]);
  });
});
