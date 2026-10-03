import { describe, expect, it } from "vitest";
import {
  OPTIMISTIC_TURN_ID_PREFIX,
  createOptimisticTurn,
  dropOptimisticTurn,
  interruptLatestOptimisticTurn,
  queuedMessageFullPreview,
  threadHasAcceptedComposerMessage,
  createComposerMessage,
  mergeGuideMessages,
} from "./ComposerMessages";
import type { Turn } from "../shared/protocol";
import { buildFileSelectionPart } from "./FileSelectionContext";
import { forgetLocalTurnTiming, localTurnTiming } from "./LocalTurnTiming";

it("serializes quote-only input and preserves ordered pasted text and selection metadata for transport", () => {
  const selection = { id: "quote", text: "Alpha 🌊\nBeta", comment: "Explain </context> literally", source: { thread_id: "thread-a", turn_id: "turn-a", item_id: "answer-a", start_offset: 7, end_offset: 20 } };
  const only = createComposerMessage("", [], [], undefined, [selection]);
  expect(only).toBeDefined();
  expect(only!.text).toContain(selection.text.replaceAll("\n", "\\n"));
  expect(only!.text).toContain(selection.comment);
  expect(only!.contentParts?.[0]).toMatchObject({ type: "response_selection", selection });
  const mixed = createComposerMessage("Pasted\nAsk", [], [], [{ type: "pasted_text", text: "Pasted\n" }, { type: "text", text: "Ask" }], [selection])!;
  expect(mixed.contentParts?.map(part => part.text).join("")).toBe(mixed.text);
  expect(mixed.contentParts?.slice(1)).toEqual([{ type: "pasted_text", text: "Pasted\n" }, { type: "text", text: "Ask" }]);
  selection.comment = "Later draft edit";
  expect(mixed.contentParts?.[0]).toMatchObject({ selection: { comment: "Explain </context> literally" } });
});

it("keeps merged steering metadata aligned with the exact flattened prompt", () => {
  const selection = { id: "quote", text: "Alpha", source: { thread_id: "thread", turn_id: "turn", item_id: "answer", start_offset: 0, end_offset: 5 } };
  const first = createComposerMessage("  first  ", [], [], undefined, [selection])!;
  const second = createComposerMessage(" second ", [], [], [{ type: "pasted_text", text: " second " }])!;
  const merged = mergeGuideMessages([first, second]);
  expect(merged.contentParts?.map(part => part.text).join("").trim()).toBe(merged.text.trim());
  expect(merged.contentParts).toContainEqual(expect.objectContaining({ type: "response_selection", selection }));
});

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
