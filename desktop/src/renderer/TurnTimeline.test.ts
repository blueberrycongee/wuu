import { describe, expect, it } from "vitest";
import type { ThreadItem } from "../shared/protocol";
import { buildTurnTimeline, displayItemsForSettledSegment } from "./TurnTimeline";

function item(
  id: string,
  type: ThreadItem["type"],
  text: string,
  cause?: string,
): ThreadItem {
  return { id, type, text, cause, status: "completed" };
}

describe("turn timeline", () => {
  it("splits at user messages so a steer stays after the interrupted text", () => {
    const segments = buildTurnTimeline([
      item("u1", "user_message", "first"),
      item("a1", "agent_message", "partial", "steered_stream_fragment"),
      item("u2", "user_message", "new direction"),
      item("a2", "agent_message", "final"),
    ]);

    expect(segments.map((segment) => ({
      users: segment.userItems.map((entry) => entry.id),
      assistants: segment.assistantItems.map((entry) => entry.id),
    }))).toEqual([
      { users: ["u1"], assistants: ["a1"] },
      { users: ["u2"], assistants: ["a2"] },
    ]);
  });

  it("keeps consecutive leading user messages in one segment", () => {
    const segments = buildTurnTimeline([
      item("u1", "user_message", "first"),
      item("u2", "user_message", "second"),
      item("a1", "agent_message", "answer"),
    ]);

    expect(segments).toHaveLength(1);
    expect(segments[0].userItems.map((entry) => entry.id)).toEqual(["u1", "u2"]);
  });

  it("promotes a steered partial answer only in settled display projection", () => {
    const partial = item("a1", "agent_message", "partial", "steered_stream_fragment");
    const [displayed] = displayItemsForSettledSegment([partial]);

    expect(displayed.terminal).toBe(true);
    expect(partial.terminal).toBeUndefined();
  });
});
