// Mirror of desktop AgentHandoff.test.ts's isAgentHandoffItem fixtures.
// Mobile only needs the boolean gate (no display helper on this side), so
// the display assertions are omitted — but the four production-wire
// shapes must still classify correctly:
//
//   1. combineAgentCompletionMessages joins ≥2 envelopes with "\n\n"
//   2. AgentCompletionChatMessage appends <changed_file_overlap> tail
//   3. Legacy envelope with no `name` propagation
//   4. Normal user message — must NOT be classified as a handoff

import { describe, expect, it } from "vitest";

import {
  AGENT_NOTIFICATION_NAME,
  isAgentHandoffItem,
  isAgentHandoffText,
} from "../src/lib/handoff";

function legacyHandoff(status = "completed"): string {
  return JSON.stringify({
    author: "/root/explore",
    recipient: "/root",
    content: `<subagent_notification>\n${JSON.stringify({
      agent_path: "/root/explore_current_directory",
      status: {
        type: "agent_result",
        agent_id: "worker-1",
        task_name: "explore_current_directory",
        status,
      },
    })}\n</subagent_notification>`,
    trigger_turn: true,
  });
}

describe("handoff (mobile)", () => {
  it("classifies \\n\\n-joined envelopes without parsing text", () => {
    // Two complete envelopes joined with "\n\n" — exactly the shape
    // combineAgentCompletionMessages produces. JSON.parse cannot consume
    // this string, so the name field is the only reliable signal.
    const joined = [legacyHandoff("completed"), legacyHandoff("failed")].join("\n\n");
    const item = { name: AGENT_NOTIFICATION_NAME, text: joined };
    expect(isAgentHandoffItem(item)).toBe(true);
  });

  it("classifies <changed_file_overlap> tail without parsing text", () => {
    const overlapTail =
      "\n\n<changed_file_overlap>\n  - foo.ts\n  - foo.ts (again)\n</changed_file_overlap>";
    const item = {
      name: AGENT_NOTIFICATION_NAME,
      text: legacyHandoff("completed") + overlapTail,
    };
    expect(isAgentHandoffItem(item)).toBe(true);
  });

  it("falls back to text sniffing when `name` is absent (legacy wire shape)", () => {
    const item = { text: legacyHandoff("completed") };
    expect(isAgentHandoffItem(item)).toBe(true);
    // Existing text-only sniff still reachable for callers without items.
    expect(isAgentHandoffText(legacyHandoff("completed"))).toBe(true);
  });

  it("does not classify normal user messages as handoffs", () => {
    expect(isAgentHandoffItem({ name: "", text: "帮我检查这个目录" })).toBe(false);
    expect(isAgentHandoffItem({ name: "", text: "hello" })).toBe(false);
    expect(isAgentHandoffItem({ text: "hello" })).toBe(false);
  });
});