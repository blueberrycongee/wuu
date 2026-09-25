import { describe, expect, it } from "vitest";
import type {
  SideThreadMessage,
  SideThreadSummary
} from "../shared/protocol";
import {
  SIDE_THREAD_DEFAULT_WIDTH,
  SIDE_THREAD_MAX_WIDTH,
  SIDE_THREAD_MIN_WIDTH,
  clampSideThreadWidth,
  createEmptySideThreadEntry,
  createInitialSideThreadStore,
  ensureSideThreadEntry,
  reduceSideThreadStore
} from "./SideThreadState";

function summary(overrides: Partial<SideThreadSummary> = {}): SideThreadSummary {
  return {
    side_thread_id: "side-1",
    main_thread_id: "main-1",
    status: "completed",
    revision: 1,
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    ...overrides
  };
}

function message(overrides: Partial<SideThreadMessage> = {}): SideThreadMessage {
  return {
    id: "m-1",
    side_thread_id: "side-1",
    role: "user",
    text: "hello",
    created_at: "2026-01-01T00:00:00.000Z",
    ...overrides
  };
}

describe("SideThreadState", () => {
  describe("createInitialSideThreadStore", () => {
    it("treats non-finite width as default", () => {
      expect(createInitialSideThreadStore(NaN).width).toBe(SIDE_THREAD_DEFAULT_WIDTH);
      expect(createInitialSideThreadStore(Infinity).width).toBe(SIDE_THREAD_DEFAULT_WIDTH);
    });
  });

  describe("clampSideThreadWidth", () => {
    it("clamps below the minimum", () => {
      expect(clampSideThreadWidth(200)).toBe(SIDE_THREAD_MIN_WIDTH);
    });
    it("clamps above the maximum", () => {
      expect(clampSideThreadWidth(900)).toBe(SIDE_THREAD_MAX_WIDTH);
    });
    it("rounds fractional values", () => {
      expect(clampSideThreadWidth(421.6)).toBe(422);
    });
  });

  describe("ensureSideThreadEntry", () => {
    it("creates an empty entry on demand without mutating the original store", () => {
      const store = createInitialSideThreadStore();
      const { store: nextStore, entry } = ensureSideThreadEntry(store, "main-x");
      expect(entry).toEqual(createEmptySideThreadEntry());
      expect(nextStore).not.toBe(store);
      expect(store.byThread["main-x"]).toBeUndefined();
      expect(nextStore.byThread["main-x"]).toEqual(entry);
    });
  });

  describe("mergeSummary", () => {
    it("preserves store identity when the summary snapshot has not changed", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary({ status: "running" })
      });
      const entry = store.byThread["main-1"];

      const next = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary({ status: "running" })
      });

      expect(next).toBe(store);
      expect(next.byThread["main-1"]).toBe(entry);
    });

    it("accepts a semantic change when revision and timestamp are tied", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary({ status: "running" })
      });

      const next = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary({ status: "completed" })
      });

      expect(next.byThread["main-1"]?.summary?.status).toBe("completed");
      expect(next.byThread["main-1"]?.streaming).toBe(false);
    });
  });

  describe("messages", () => {
    it("merges late history without overwriting local streaming messages", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "appendMessage",
        mainThreadId: "main-1",
        message: message({ id: "local-user", text: "new prompt" })
      });
      store = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "delta",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 2,
          message_id: "assistant-new",
          text_delta: "new answer"
        }
      });

      const next = reduceSideThreadStore(store, {
        type: "mergeHistory",
        mainThreadId: "main-1",
        summary: summary(),
        messages: [message({ id: "persisted-user", text: "old prompt" })]
      });

      expect(next.byThread["main-1"]?.messages.map((item) => item.id)).toEqual([
        "persisted-user",
        "local-user",
        "assistant-new"
      ]);
      expect(next.byThread["main-1"]?.messages.at(-1)?.text).toBe("new answer");
    });

    it("prefers a persisted terminal message over a local streaming copy", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "appendMessage",
        mainThreadId: "main-1",
        message: message({
          id: "assistant-1",
          role: "assistant",
          text: "partial",
          status: "streaming"
        })
      });

      const next = reduceSideThreadStore(store, {
        type: "mergeHistory",
        mainThreadId: "main-1",
        summary: summary({ status: "completed" }),
        messages: [
          message({
            id: "assistant-1",
            role: "assistant",
            text: "complete answer",
            status: "completed"
          })
        ]
      });

      expect(next.byThread["main-1"]?.messages[0]).toMatchObject({
        text: "complete answer",
        status: "completed"
      });
    });

    it("preserves store identity when recovery history has not changed", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "mergeHistory",
        mainThreadId: "main-1",
        summary: summary({ status: "running" }),
        messages: [
          message({
            id: "assistant-1",
            role: "assistant",
            status: "streaming"
          })
        ]
      });
      const entry = store.byThread["main-1"];

      const next = reduceSideThreadStore(store, {
        type: "mergeHistory",
        mainThreadId: "main-1",
        summary: summary({ status: "running" }),
        messages: [
          message({
            id: "assistant-1",
            role: "assistant",
            status: "streaming"
          })
        ]
      });

      expect(next).toBe(store);
      expect(next.byThread["main-1"]).toBe(entry);
    });
  });

  describe("reset", () => {
    it("clears history and summary but keeps the panel open with its draft", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, { type: "open", mainThreadId: "main-1" });
      store = reduceSideThreadStore(store, {
        type: "setDraft",
        mainThreadId: "main-1",
        draft: "unsent"
      });
      store = reduceSideThreadStore(store, {
        type: "mergeHistory",
        mainThreadId: "main-1",
        summary: summary({ status: "running" }),
        messages: [message(), message({ id: "m-2", role: "assistant", text: "hi" })]
      });
      store = reduceSideThreadStore(store, { type: "reset", mainThreadId: "main-1" });
      const entry = store.byThread["main-1"];
      expect(entry?.messages).toEqual([]);
      expect(entry?.summary).toBeNull();
      expect(entry?.streaming).toBe(false);
      expect(entry?.open).toBe(true);
      expect(entry?.draft).toBe("unsent");
    });

    it("applies a peer reset event the same way", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "mergeHistory",
        mainThreadId: "main-1",
        summary: summary(),
        messages: [message()]
      });
      store = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: { type: "reset", side_thread_id: "side-1", main_thread_id: "main-1" }
      });
      const entry = store.byThread["main-1"];
      expect(entry?.messages).toEqual([]);
      expect(entry?.summary).toBeNull();
    });
  });

  describe("applyEvent", () => {
    it("status event updates streaming flag and summary status", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary()
      });
      const next = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "status",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          summary: summary({ status: "running", revision: 2 })
        }
      });
      expect(next.byThread["main-1"]?.streaming).toBe(true);
      expect(next.byThread["main-1"]?.summary?.status).toBe("running");
      expect(next.byThread["main-1"]?.lastError).toBeUndefined();
    });

    it("keeps a terminal revision when an older running response resolves later", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary({ status: "running", revision: 1 })
      });
      store = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "status",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          summary: summary({ status: "interrupted", revision: 2 })
        }
      });

      const next = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary({
          status: "running",
          revision: 1,
          updated_at: "2026-01-01T00:00:10.000Z"
        })
      });

      expect(next.byThread["main-1"]?.summary?.status).toBe("interrupted");
      expect(next.byThread["main-1"]?.summary?.revision).toBe(2);
      expect(next.byThread["main-1"]?.streaming).toBe(false);
    });

    it("ignores deltas from a revision that is already terminal", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary({ status: "interrupted", revision: 2 })
      });

      const next = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "delta",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 2,
          message_id: "late-assistant",
          text_delta: "late text"
        }
      });

      expect(next.byThread["main-1"]?.messages).toEqual([]);
      expect(next.byThread["main-1"]?.streaming).toBe(false);
    });

    it("settles a streaming assistant when a peer interrupts the revision", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary({ status: "running", revision: 1 })
      });
      store = reduceSideThreadStore(store, {
        type: "appendMessage",
        mainThreadId: "main-1",
        message: message({
          id: "assistant-running",
          role: "assistant",
          text: "partial",
          status: "streaming"
        })
      });

      const next = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "status",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          summary: summary({ status: "interrupted", revision: 2 })
        }
      });

      expect(next.byThread["main-1"]?.messages[0]).toMatchObject({
        text: "partial",
        status: "interrupted"
      });
      expect(next.byThread["main-1"]?.streaming).toBe(false);
    });

    it("items event creates and updates the streaming canonical item snapshot", () => {
      const store = createInitialSideThreadStore();
      const started = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "items",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message_id: "m-tools",
          items: [{ id: "tool-1", type: "tool_call", status: "in_progress", name: "read_file" }]
        }
      });
      const completed = reduceSideThreadStore(started, {
        type: "applyEvent",
        event: {
          type: "items",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message_id: "m-tools",
          items: [{ id: "tool-1", type: "tool_call", status: "completed", name: "read_file", result: "ok" }]
        }
      });

      expect(completed.byThread["main-1"]?.messages[0]).toMatchObject({
        id: "m-tools",
        text: "",
        status: "streaming",
        items: [{ id: "tool-1", type: "tool_call", status: "completed", result: "ok" }]
      });
    });

    it("delta event appends to an unknown message id and streams", () => {
      const store = createInitialSideThreadStore();
      const next = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "delta",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message_id: "m-a",
          text_delta: "你好"
        }
      });
      const messages = next.byThread["main-1"]?.messages ?? [];
      expect(messages).toHaveLength(1);
      expect(messages[0]).toMatchObject({
        id: "m-a",
        role: "assistant",
        text: "你好",
        status: "streaming"
      });
      expect(next.byThread["main-1"]?.streaming).toBe(true);
    });

    it("delta event concatenates into the existing message", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "delta",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message_id: "m-a",
          text_delta: "你好"
        }
      });
      store = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "delta",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message_id: "m-a",
          text_delta: "世界"
        }
      });
      const messages = store.byThread["main-1"]?.messages ?? [];
      expect(messages).toHaveLength(1);
      expect(messages[0]?.text).toBe("你好世界");
    });

    it("message event finalizes a streamed message and clears streaming", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "delta",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message_id: "m-a",
          text_delta: "你好"
        }
      });
      const next = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "message",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message: message({
            id: "m-a",
            role: "assistant",
            text: "你好世界",
            status: "completed"
          })
        }
      });
      const messages = next.byThread["main-1"]?.messages ?? [];
      expect(messages[0]).toMatchObject({
        id: "m-a",
        text: "你好世界",
        status: "completed"
      });
      expect(next.byThread["main-1"]?.streaming).toBe(false);
    });

    it("error event marks the targeted message failed and records lastError", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "delta",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message_id: "m-a",
          text_delta: "中"
        }
      });
      const next = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "error",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 1,
          message_id: "m-a",
          error_message: "rate limited"
        }
      });
      const messages = next.byThread["main-1"]?.messages ?? [];
      expect(messages[0]).toMatchObject({
        id: "m-a",
        status: "failed",
        error_message: "rate limited"
      });
      expect(next.byThread["main-1"]?.lastError).toBe("rate limited");
      expect(next.byThread["main-1"]?.streaming).toBe(false);
    });

    it("keeps the provider error when the failed status arrives last", () => {
      let store = createInitialSideThreadStore();
      store = reduceSideThreadStore(store, {
        type: "mergeSummary",
        mainThreadId: "main-1",
        summary: summary({ status: "running", revision: 1 })
      });
      store = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "error",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          revision: 2,
          message_id: "m-a",
          error_message: "rate limited"
        }
      });

      const next = reduceSideThreadStore(store, {
        type: "applyEvent",
        event: {
          type: "status",
          side_thread_id: "side-1",
          main_thread_id: "main-1",
          summary: summary({ status: "failed", revision: 2 })
        }
      });

      expect(next.byThread["main-1"]?.lastError).toBe("rate limited");
      expect(next.byThread["main-1"]?.summary?.status).toBe("failed");
    });
  });
});
