import { describe, expect, it } from "vitest";
import type { Thread, ThreadItem } from "../shared/protocol";
import { createOptimisticTurn, type QueuedComposerMessage } from "./ComposerMessages";
import {
  appendPendingComposerMessage,
  applyHeldComposerSnapshot,
  findPendingComposerMessage,
  materializedComposerMessageIDs,
  pendingComposerMessageCount,
  pendingComposerMessagesForThread,
  reconcilePendingComposerMessagesForThread,
  removePendingComposerMessagesByID,
  type PendingComposerMessagesByThread,
} from "./ComposerPendingMessages";

function message(id: string, text: string): QueuedComposerMessage {
  return { id, text, images: [], files: [] };
}

function userItem(id: string, sourceID?: string): ThreadItem {
  return { id, type: "user_message", source_id: sourceID } as ThreadItem;
}

function threadWithItems(id: string, items: ThreadItem[]): Thread {
  return { id, turns: [{ id: `${id}-turn-1`, items }] } as unknown as Thread;
}

describe("composer pending messages", () => {
  it("does not append an optimistic message whose id already exists", () => {
    const previous = {
      queued: [message("queue-1", "Held queue")],
      guides: [message("guide-1", "Held guide")],
    };

    expect(
      appendPendingComposerMessage(
        previous,
        "queue",
        message("queue-1", "Late response"),
      ),
    ).toBe(previous);
    expect(
      appendPendingComposerMessage(
        previous,
        "queue",
        message("guide-1", "Late cross-list response"),
      ),
    ).toBe(previous);
  });

  it("lets a held snapshot replace matching optimistic state across both lists", () => {
    const heldQueue = {
      ...message("shared-id", "Server snapshot"),
      held: true,
      heldPosition: 0,
      origin: "queue" as const,
    };
    const previous = {
      queued: [
        message("shared-id", "Optimistic queue"),
        message("queue-in-flight", "Still in flight"),
        { ...message("stale-held", "Stale"), held: true },
      ],
      guides: [message("shared-id", "Optimistic guide")],
    };

    const next = applyHeldComposerSnapshot(previous, [heldQueue]);

    expect(next.queued).toEqual([
      message("queue-in-flight", "Still in flight"),
      heldQueue,
    ]);
    expect(next.guides).toEqual([]);
  });

  it("returns only the pending messages for the requested thread", () => {
    const byThread: PendingComposerMessagesByThread = {
      "thread-a": {
        queued: [message("queue-a", "A queued")],
        guides: [message("guide-a", "A guide")],
      },
      "thread-b": {
        queued: [message("queue-b", "B queued")],
        guides: [],
      },
    };

    const threadA = pendingComposerMessagesForThread(byThread, "thread-a");
    const threadB = pendingComposerMessagesForThread(byThread, "thread-b");
    const missing = pendingComposerMessagesForThread(byThread, "missing");

    expect(threadA.queued.map((item) => item.id)).toEqual(["queue-a"]);
    expect(threadA.guides.map((item) => item.id)).toEqual(["guide-a"]);
    expect(threadB.queued.map((item) => item.id)).toEqual(["queue-b"]);
    expect(pendingComposerMessageCount(missing)).toBe(0);
  });

  it("finds the original thread for a pending message before mutating it", () => {
    const byThread: PendingComposerMessagesByThread = {
      "thread-a": {
        queued: [message("shared-id", "A queued")],
        guides: [],
      },
      "thread-b": {
        queued: [message("other-id", "B queued")],
        guides: [message("guide-b", "B guide")],
      },
    };

    expect(
      findPendingComposerMessage(byThread, "shared-id", "queue", "thread-b"),
    ).toMatchObject({
      threadID: "thread-a",
      index: 0,
      message: { id: "shared-id" },
    });
    expect(
      findPendingComposerMessage(byThread, "guide-b", "guide", "thread-b"),
    ).toMatchObject({
      threadID: "thread-b",
      index: 0,
      message: { id: "guide-b" },
    });
  });

  it("removes only queued messages when a queue id is dequeued", () => {
    const byThread: PendingComposerMessagesByThread = {
      "thread-a": {
        queued: [message("shared-id", "queued")],
        guides: [message("shared-id", "guide")],
      },
    };

    const next = removePendingComposerMessagesByID(
      byThread,
      "thread-a",
      "shared-id",
      "queue",
    );

    expect(next["thread-a"]?.queued).toEqual([]);
    expect(next["thread-a"]?.guides.map((item) => item.id)).toEqual([
      "shared-id",
    ]);
  });

  it("removes queued and guide messages by default when a user message completes", () => {
    const byThread: PendingComposerMessagesByThread = {
      "thread-a": {
        queued: [message("shared-id", "queued")],
        guides: [message("shared-id", "guide")],
      },
    };

    const next = removePendingComposerMessagesByID(
      byThread,
      "thread-a",
      "shared-id",
    );

    expect(next["thread-a"]).toBeUndefined();
  });
});

describe("reconcile pending composer messages against thread turns", () => {
  it("collects user_message source ids that have materialized", () => {
    const thread = threadWithItems("thread-a", [
      userItem("thread-a-turn-1-item-1", "queue-1"),
      { id: "thread-a-turn-1-item-2", type: "agent_message" } as ThreadItem,
      userItem("thread-a-turn-1-item-3", "guide-1"),
      userItem("thread-a-turn-1-item-4"), // no source_id — ignored
    ]);

    expect([...materializedComposerMessageIDs(thread)].sort()).toEqual([
      "guide-1",
      "queue-1",
    ]);
    expect(materializedComposerMessageIDs(undefined).size).toBe(0);
  });

  it("retains an in-flight fallback until a real turn acknowledges its client identity", () => {
    const pending = { ...message("queue-fallback", "Restore rejected queue input"), operationState: "sending" as const };
    const byThread: PendingComposerMessagesByThread = {
      "thread-a": { queued: [pending], guides: [] },
    };
    // A delayed render can still contain the rejected admission's placeholder
    // when reconciliation observes the new fallback queue entry.
    const optimistic = createOptimisticTurn(pending, Date.now());
    const staleThread = { ...threadWithItems("thread-a", []), turns: [optimistic] };

    expect.soft([...materializedComposerMessageIDs(staleThread)]).toEqual([]);
    expect.soft(reconcilePendingComposerMessagesForThread(byThread, staleThread)).toBe(byThread);

    const acceptedThread = threadWithItems("thread-a", [
      userItem("server-user-message", pending.id),
    ]);
    acceptedThread.turns = [...staleThread.turns, ...acceptedThread.turns];
    expect([...materializedComposerMessageIDs(acceptedThread)]).toEqual([pending.id]);
    expect(reconcilePendingComposerMessagesForThread(byThread, acceptedThread)).toEqual({});
  });

  it("drops a queued message that already went out (missed removal notification)", () => {
    const byThread: PendingComposerMessagesByThread = {
      "thread-a": {
        queued: [message("queue-sent", "已发出"), message("queue-pending", "仍排队")],
        guides: [],
      },
    };
    const thread = threadWithItems("thread-a", [
      userItem("thread-a-turn-1-item-1", "queue-sent"),
    ]);

    const next = reconcilePendingComposerMessagesForThread(byThread, thread);

    expect(next["thread-a"]?.queued.map((item) => item.id)).toEqual([
      "queue-pending",
    ]);
  });

  it("drops materialized guide (steer) messages too", () => {
    const byThread: PendingComposerMessagesByThread = {
      "thread-a": {
        queued: [],
        guides: [message("guide-sent", "已引导")],
      },
    };
    const thread = threadWithItems("thread-a", [
      userItem("thread-a-turn-1-item-1", "guide-sent"),
    ]);

    const next = reconcilePendingComposerMessagesForThread(byThread, thread);

    expect(next["thread-a"]).toBeUndefined();
  });

  it("returns the same reference when nothing has materialized", () => {
    const byThread: PendingComposerMessagesByThread = {
      "thread-a": {
        queued: [message("queue-pending", "仍排队")],
        guides: [],
      },
    };
    const thread = threadWithItems("thread-a", [
      userItem("thread-a-turn-1-item-1", "some-other-message"),
    ]);

    expect(reconcilePendingComposerMessagesForThread(byThread, thread)).toBe(
      byThread,
    );
    expect(
      reconcilePendingComposerMessagesForThread(byThread, undefined),
    ).toBe(byThread);
  });
});
