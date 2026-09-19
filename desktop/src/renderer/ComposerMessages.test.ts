import { describe, expect, it } from "vitest";
import {
  OPTIMISTIC_TURN_ID_PREFIX,
  dropOptimisticTurn,
  interruptLatestOptimisticTurn,
  queuedMessageFullPreview,
  threadHasAcceptedComposerMessage,
} from "./ComposerMessages";
import type { Turn } from "../shared/protocol";
import { buildFileSelectionPart } from "./FileSelectionContext";

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
  });
});

describe("settling optimistic turns", () => {
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

it("previews queued selection instructions without exposing the serialized reference envelope", () => {
  const part = buildFileSelectionPart({ workspace: "/repo", path: "notes.md", start_line: 2,
    start_column: 1, end_line: 4, end_column: 3, quote: "Captured reference", revision: "snapshot" },
  "edit", "Shorten this section");
  const message = { id: "pending", text: `${part.text}Keep the examples`, images: [], files: [],
    contentParts: [part, { type: "text" as const, text: "Keep the examples" }] };
  const preview = queuedMessageFullPreview(message);
  expect(preview).toContain("notes.md:2–4");
  expect(preview).toContain("Shorten this section");
  expect(preview).toContain("Keep the examples");
  expect(preview).not.toContain("snapshot");
  expect(message.contentParts[0].text).toBe(part.text);
});
