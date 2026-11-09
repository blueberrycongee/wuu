import { describe, expect, it } from "vitest";

import type { Turn } from "../shared/protocol";
import { setActiveLocale } from "./i18n";
import { messageFlowStatusLabel } from "./message-flow-display";
import { turnProgressContent } from "./TurnViewHelpers";

describe("messageFlowStatusLabel", () => {
  it("distinguishes provider finalization from active text generation", () => {
    expect(
      messageFlowStatusLabel({
        done: false,
        failed: false,
        hasFinalText: true,
        locale: "zh",
      }),
    ).toBe("正在回复");
    expect(
      messageFlowStatusLabel({
        done: false,
        failed: false,
        hasFinalText: true,
        finalizing: true,
        locale: "zh",
      }),
    ).toBe("正在收尾");
  });

  it("marks a completed final text item as finalizing until its turn settles", () => {
    setActiveLocale("zh-CN");
    const turn: Turn = {
      id: "turn-1",
      status: "in_progress",
      items_view: "full",
      items: [
        {
          id: "answer-1",
          type: "agent_message",
          status: "completed",
          terminal: true,
          text: "Visible final answer",
        },
      ],
    };

    expect(turnProgressContent(turn, 10_000, true).label).toBe("正在收尾");
  });
});
