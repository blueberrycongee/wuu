import { describe, expect, it } from "vitest";
import type { ThreadItem, Turn } from "@wuu/protocol";

import { chatRowsFromTurns } from "../src/lib/chatModel";

function turn(items: ThreadItem[]): Turn {
  return { id: "turn-1", items, items_view: "full", status: "completed" };
}

describe("chatRowsFromTurns", () => {
  it("keeps ordinary user and agent messages", () => {
    const rows = chatRowsFromTurns([
      turn([
        { id: "user", type: "user_message", text: "帮我修个 bug" },
        { id: "agent", type: "agent_message", text: "已经修好" },
      ]),
    ]);

    expect(rows.map((row) => row.kind)).toEqual(["user", "agent"]);
    expect(rows.map((row) => row.item.text)).toEqual(["帮我修个 bug", "已经修好"]);
  });

  it("filters reasoning and tool activity", () => {
    const rows = chatRowsFromTurns([
      turn([
        { id: "reasoning", type: "reasoning", text: "thinking" },
        { id: "tool", type: "tool_call", name: "bash" },
        { id: "answer", type: "agent_message", text: "完成" },
      ]),
    ]);

    expect(rows.map((row) => row.item.id)).toEqual(["answer"]);
  });

  it("filters inter-agent handoff messages", () => {
    const handoff = JSON.stringify({
      content:
        '<subagent_notification>{"agent_path":"a/b","status":{"status":"completed"}}</subagent_notification>',
      trigger_turn: true,
    });
    const rows = chatRowsFromTurns([
      turn([
        { id: "handoff", type: "user_message", text: handoff },
        { id: "plain", type: "user_message", text: "普通消息" },
      ]),
    ]);

    expect(rows.map((row) => row.item.id)).toEqual(["plain"]);
  });
});
