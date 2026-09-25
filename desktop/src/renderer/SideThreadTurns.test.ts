import { describe, expect, it } from "vitest";
import type { SideThreadMessage } from "../shared/protocol";
import { sideThreadMessagesToTurns } from "./SideThreadTurns";

function message(
  id: string,
  role: SideThreadMessage["role"],
  overrides: Partial<SideThreadMessage> = {},
): SideThreadMessage {
  return {
    id,
    side_thread_id: "side-1",
    role,
    text: `${role}-${id}`,
    created_at: `2026-07-15T00:00:0${id.length}.000Z`,
    ...overrides,
  };
}

describe("sideThreadMessagesToTurns", () => {
  it("pairs each user message with its following assistant in source order", () => {
    const turns = sideThreadMessagesToTurns([
      message("u-1", "user", { text: "first question" }),
      message("a-1", "assistant", {
        text: "first answer",
        status: "completed",
      }),
      message("u-2", "user", { text: "second question" }),
      message("a-2", "assistant", {
        text: "partial answer",
        status: "streaming",
      }),
    ]);

    expect(turns.map((turn) => turn.id)).toEqual([
      "side-turn-u-1",
      "side-turn-u-2",
    ]);
    expect(turns[0]).toMatchObject({
      status: "completed",
      items_view: "full",
      items: [
        {
          id: "u-1",
          type: "user_message",
          status: "completed",
          role: "user",
          text: "first question",
        },
        {
          id: "a-1",
          type: "agent_message",
          status: "completed",
          terminal: true,
          role: "assistant",
          text: "first answer",
        },
      ],
    });
    expect(turns[1]).toMatchObject({
      status: "in_progress",
      items: [
        { id: "u-2", type: "user_message" },
        {
          id: "a-2",
          type: "agent_message",
          status: "in_progress",
          terminal: true,
        },
      ],
    });
  });

  it("maps every assistant lifecycle state onto canonical turn state", () => {
    const turns = sideThreadMessagesToTurns([
      message("a-stream", "assistant", { status: "streaming" }),
      message("a-complete", "assistant", { status: "completed" }),
      message("a-legacy", "assistant"),
      message("a-failed", "assistant", {
        status: "failed",
        error_message: "provider unavailable",
      }),
      message("a-stopped", "assistant", { status: "interrupted" }),
    ]);

    expect(turns.map((turn) => turn.status)).toEqual([
      "in_progress",
      "completed",
      "completed",
      "failed",
      "interrupted",
    ]);
    expect(turns.map((turn) => turn.items[0]?.status)).toEqual([
      "in_progress",
      "completed",
      "completed",
      "failed",
      "completed",
    ]);
    expect(turns[3]?.error).toEqual({ message: "provider unavailable" });
    expect(turns[4]?.error).toBeUndefined();
  });

  it("passes canonical agent items through to the shared TurnView model", () => {
    const turns = sideThreadMessagesToTurns([
      message("u-tools", "user", { text: "inspect it" }),
      message("a-tools", "assistant", {
        text: "done",
        status: "completed",
        items: [
          {
            id: "tool-1",
            source_id: "call-1",
            type: "tool_call",
            status: "completed",
            name: "read_file",
            arguments: `{"path":"README.md"}`,
            result: "contents",
          },
          {
            id: "answer-1",
            type: "agent_message",
            status: "completed",
            terminal: true,
            role: "assistant",
            text: "done",
          },
        ],
      }),
    ]);

    expect(turns[0]?.items.slice(1)).toEqual([
      expect.objectContaining({ id: "tool-1", type: "tool_call", result: "contents" }),
      expect.objectContaining({ id: "answer-1", type: "agent_message", text: "done" }),
    ]);
  });

  it("keeps orphan messages visible without reordering or dropping them", () => {
    const turns = sideThreadMessagesToTurns([
      message("a-before", "assistant", { status: "completed" }),
      message("u-unanswered", "user"),
      message("u-paired", "user"),
      message("a-paired", "assistant", { status: "failed" }),
      message("a-after", "assistant", { status: "interrupted" }),
      message("u-live", "user"),
    ]);

    expect(turns.map((turn) => turn.id)).toEqual([
      "side-turn-a-before",
      "side-turn-u-unanswered",
      "side-turn-u-paired",
      "side-turn-a-after",
      "side-turn-u-live",
    ]);
    expect(turns.map((turn) => turn.items.map((item) => item.id))).toEqual([
      ["a-before"],
      ["u-unanswered"],
      ["u-paired", "a-paired"],
      ["a-after"],
      ["u-live"],
    ]);
    expect(turns.map((turn) => turn.status)).toEqual([
      "completed",
      "completed",
      "failed",
      "interrupted",
      "in_progress",
    ]);
  });

  it("keeps durable ids stable while an assistant reply settles", () => {
    const user = message("user-1", "user");
    const streaming = message("assistant-1", "assistant", {
      status: "streaming",
      text: "partial",
    });
    const completed = { ...streaming, status: "completed" as const, text: "done" };
    const original = [user, streaming];

    const liveTurns = sideThreadMessagesToTurns(original);
    const settledTurns = sideThreadMessagesToTurns([user, completed]);

    expect(settledTurns[0]?.id).toBe(liveTurns[0]?.id);
    expect(settledTurns[0]?.items.map((item) => item.id)).toEqual(
      liveTurns[0]?.items.map((item) => item.id),
    );
    expect(liveTurns[0]?.status).toBe("in_progress");
    expect(settledTurns[0]?.status).toBe("completed");
    expect(original).toEqual([user, streaming]);
  });
});
