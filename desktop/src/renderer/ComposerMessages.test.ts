import { describe, expect, it } from "vitest";
import {
  OPTIMISTIC_TURN_ID_PREFIX,
  createOptimisticTurn,
  dropOptimisticTurn,
  interruptLatestOptimisticTurn,
  threadHasAcceptedComposerMessage,
} from "./ComposerMessages";
import type { Turn } from "../shared/protocol";
import { forgetLocalTurnTiming, localTurnTiming } from "./LocalTurnTiming";

function turnWithUserText(id: string, text: string): Turn {
  return {
    id,
    status: "in_progress",
    items_view: "full",
    items: [
      {
        id: `${id}-user`,
        type: "user_message",
        status: "completed",
        text,
      },
    ],
  };
}

describe("threadHasAcceptedComposerMessage", () => {
  it("ignores the local optimistic placeholder for the same text", () => {
    expect(
      threadHasAcceptedComposerMessage(
        {
          turns: [
            turnWithUserText(`${OPTIMISTIC_TURN_ID_PREFIX}local`, "keep this sent"),
          ],
        },
        { text: "keep this sent" },
        `${OPTIMISTIC_TURN_ID_PREFIX}local`,
      ),
    ).toBe(false);
  });

  it("recognizes a later server turn that already carries the sent text", () => {
    expect(
      threadHasAcceptedComposerMessage(
        {
          turns: [
            turnWithUserText(`${OPTIMISTIC_TURN_ID_PREFIX}local`, "keep this sent"),
            turnWithUserText("turn-follow-up", "keep this sent"),
          ],
        },
        { text: "keep this sent" },
        `${OPTIMISTIC_TURN_ID_PREFIX}local`,
      ),
    ).toBe(true);
    expect(
      threadHasAcceptedComposerMessage(
        { turns: [turnWithUserText("earlier-turn", "keep this sent")] },
        { text: "keep this sent" },
        `${OPTIMISTIC_TURN_ID_PREFIX}local`,
        new Set(["earlier-turn"]),
      ),
    ).toBe(false);
  });
});

describe("settling optimistic turns", () => {
  it("freezes a cancelled preparation before its conversation renders again", () => {
    const optimistic = createOptimisticTurn({ id: "cancelled-preparation", text: "pending", images: [], files: [] }, 1000);
    const settled = interruptLatestOptimisticTurn({ turns: [optimistic] }, 2000);
    expect(localTurnTiming(settled.turns[0], 5000)?.elapsed).toBe(1000);
    forgetLocalTurnTiming(optimistic.id);
  });

  it("preserves a real running turn when dropping or stopping a placeholder", () => {
    const optimistic = turnWithUserText(`${OPTIMISTIC_TURN_ID_PREFIX}local`, "pending");
    const accepted = turnWithUserText("accepted-turn", "accepted");
    const thread = { status: "in_progress", turns: [accepted, optimistic] };
    for (const settled of [dropOptimisticTurn(thread, optimistic.id), interruptLatestOptimisticTurn(thread, 0)]) {
      expect(settled.status).toBe("in_progress");
      expect(settled.turns.find((turn) => turn.id === accepted.id)?.status).toBe("in_progress");
    }
  });
});
