/**
 * Tests for `buildAssistantTurnDisplay`, the pure function that maps
 * a turn's items onto the single `entries` list the shell renders.
 *
 * The shell then splits `entries` into two visual groups
 * (`process` fold + `answer` body), but that's a presentational
 * concern — the data model is one chronological list. Each entry has
 * a `position` derived from `item.phase` and a `settled` flag derived
 * from `item.status`. The shell uses `settled` to decide the fold's
 * default collapse state.
 *
 * Commentary text lives in the `process` position (it describes what
 * the model is doing, not what it is telling the user). `final_answer`
 * messages and phase-unknown live text live in the `answer` position.
 * Reasoning, tool calls, and other process types are also `process`.
 * Multi-`final_answer` turns keep all answer text in arrival order so
 * they don't expose internal stream shape.
 *
 * Live preview: the fold header shows the latest commentary text or
 * the latest tool command while the turn is `in_progress`.
 * Completed turns drop the preview; the fold header collapses to
 * the status line.
 */
import { describe, expect, it } from "vitest";
import { createElement, type JSX } from "react";
import type { ThreadItem, Turn, TurnStatus } from "../shared/protocol";
import { buildAssistantTurnDisplay } from "./AssistantTurnDisplay";

let idCounter = 0;
function nextID(prefix: string): string {
  idCounter += 1;
  return `${prefix}-${idCounter}`;
}

type BuildOptions = {
  status?: TurnStatus;
  items: ThreadItem[];
  durationMs?: number;
};

function makeTurn(options: BuildOptions): Turn {
  return {
    id: "turn-1",
    items: options.items,
    items_view: "full",
    status: options.status ?? "completed",
    duration_ms: options.durationMs,
  };
}

function makeCommentary(text: string): ThreadItem {
  return {
    id: nextID("commentary"),
    type: "agent_message",
    status: "completed",
    terminal: false,
    role: "assistant",
    text,
  };
}

function makeFinalAnswer(text: string): ThreadItem {
  return {
    id: nextID("final"),
    type: "agent_message",
    status: "completed",
    terminal: true,
    role: "assistant",
    text,
  };
}

function makeLiveUnclassifiedAgentMessage(text: string): ThreadItem {
  return {
    id: nextID("live-agent"),
    type: "agent_message",
    status: "in_progress",
    role: "assistant",
    text,
  };
}

function makeToolCall(name = "lookup"): ThreadItem {
  return {
    id: nextID("tool"),
    type: "tool_call",
    status: "completed",
    name,
  };
}

function makeReasoning(text = "thinking..."): ThreadItem {
  return {
    id: nextID("reasoning"),
    type: "reasoning",
    status: "completed",
    text,
  };
}

// Render a stable text node per item so we can assert on identity
// (which items end up where) without invoking React's rendering
// pipeline.
function renderItem(item: ThreadItem): JSX.Element {
  return createElement("div", { "data-item-id": item.id }, textOf(item));
}

function textOf(item: ThreadItem): string {
  if (item.type === "agent_message") {
    return item.text ?? "";
  }
  if (item.type === "reasoning") {
    return item.text ?? "";
  }
  if (item.type === "tool_call") {
    return item.name ?? "tool";
  }
  if (item.type === "context_compaction") {
    return item.text ?? "";
  }
  return "";
}

function processItemIDs(
  display: ReturnType<typeof buildAssistantTurnDisplay>,
): string[] {
  if (!display) return [];
  return display.entries
    .filter(
      (entry) => entry.position === "process" && entry.kind !== "activity",
    )
    .map((entry) => entry.item.id);
}

function activityItemIDs(
  display: ReturnType<typeof buildAssistantTurnDisplay>,
): string[] {
  if (!display) return [];
  return display.entries
    .filter((entry) => entry.position === "process" && entry.kind === "activity")
    .map((entry) => entry.item.id);
}

function answerItemIDs(
  display: ReturnType<typeof buildAssistantTurnDisplay>,
): string[] {
  if (!display) return [];
  return display.entries
    .filter((entry) => entry.position === "answer")
    .map((entry) => entry.item.id);
}

describe("assistant turn entries layout", () => {
  it("normal: commentary + tool + commentary + final_answer → process carries commentary + tool, answer carries final", () => {
    const commentaryA = makeCommentary("Let me look that up.");
    const tool = makeToolCall("read_file");
    const commentaryB = makeCommentary("Got it. The file says...");
    const final = makeFinalAnswer("Here is the answer you wanted.");
    const turn = makeTurn({ items: [commentaryA, tool, commentaryB, final] });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(processItemIDs(display)).toEqual([commentaryA.id, commentaryB.id]);
    expect(activityItemIDs(display)).toEqual([tool.id]);
    expect(answerItemIDs(display)).toEqual([final.id]);
    expect(display?.hasAnswer).toBe(true);
  });

  it("no final: commentary + tool + commentary keeps process entries without an answer", () => {
    const commentaryA = makeCommentary("Let me check.");
    const tool = makeToolCall("ls");
    const commentaryB = makeCommentary("Found something.");
    const turn = makeTurn({ items: [commentaryA, tool, commentaryB] });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(answerItemIDs(display)).toEqual([]);
    expect(processItemIDs(display)).toEqual([commentaryA.id, commentaryB.id]);
    expect(activityItemIDs(display)).toEqual([tool.id]);
    expect(display?.hasAnswer).toBe(false);
    // Completed turn → no live process preview.
    expect(display?.latestProcessPreview?.text).toBeUndefined();
  });

  it("multi final: commentary + final_1 + commentary + final_2 → answer keeps final text in order", () => {
    const commentaryA = makeCommentary("First thought.");
    const finalOne = makeFinalAnswer("First answer.");
    const commentaryB = makeCommentary("Second thought.");
    const finalTwo = makeFinalAnswer("Second answer.");
    const turn = makeTurn({
      items: [commentaryA, finalOne, commentaryB, finalTwo],
    });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(processItemIDs(display)).toEqual([commentaryA.id, commentaryB.id]);
    expect(answerItemIDs(display)).toEqual([finalOne.id, finalTwo.id]);
    expect(display?.hasAnswer).toBe(true);
    expect(display?.latestProcessPreview?.text).toBeUndefined();
  });

  it("pure tool: only tool_calls, no text — entries carry activity, hasAnswer=false", () => {
    const toolA = makeToolCall("read_file");
    const toolB = makeToolCall("grep");

    // in_progress: adjacent tool activity is one process group with count=2.
    const inProgressTurn = makeTurn({
      status: "in_progress",
      items: [toolA, toolB],
    });
    const inProgressDisplay = buildAssistantTurnDisplay(
      inProgressTurn,
      undefined,
      renderItem,
    );
    expect(inProgressDisplay).toBeDefined();
    expect(answerItemIDs(inProgressDisplay)).toEqual([]);
    const processEntries = inProgressDisplay?.entries.filter(
      (e) => e.kind === "process_group",
    );
    expect(processEntries).toHaveLength(1);
    expect(processEntries?.[0]?.count).toBe(2);
    expect(processItemIDs(inProgressDisplay)).toEqual([toolA.id]);
    expect(inProgressDisplay?.hasAnswer).toBe(false);
    // Both tools lack arguments, so the deterministic summary falls back to
    // the search count rather than the latest raw tool command.
    expect(inProgressDisplay?.latestProcessPreview?.kind).toBe("activity");
    expect(inProgressDisplay?.latestProcessPreview?.text).toBe("1 次搜索");

    // Completed with no final answer: pure tool output remains process-only.
    const completedTurn = makeTurn({
      status: "completed",
      items: [toolA, toolB],
      durationMs: 1500,
    });
    const completedDisplay = buildAssistantTurnDisplay(
      completedTurn,
      undefined,
      renderItem,
    );
    expect(completedDisplay).toBeDefined();
    expect(answerItemIDs(completedDisplay)).toEqual([]);
    const completedProcess = completedDisplay?.entries.filter(
      (e) => e.kind === "process_group",
    );
    expect(completedProcess).toHaveLength(1);
    expect(completedProcess?.[0]?.count).toBe(2);
    expect(processItemIDs(completedDisplay)).toEqual([toolA.id]);
    expect(completedDisplay?.hasAnswer).toBe(false);
    expect(completedDisplay?.latestProcessPreview?.text).toBeUndefined();
  });

  it("dedupes duplicate item ids so the same row never renders twice", () => {
    const commentary = makeCommentary("only one row");
    const final = makeFinalAnswer("the answer.");
    // Same id used twice — pathological resync shape. The shell
    // should never see two rows for the same item.
    const duplicate: ThreadItem = { ...commentary, id: "dupe" };
    const finalDup: ThreadItem = { ...final, id: "dupe-final" };
    const turn = makeTurn({
      status: "completed",
      items: [duplicate, duplicate, finalDup, finalDup],
    });
    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);
    expect(display).toBeDefined();
    expect(display?.entries.filter((e) => e.item.id === "dupe")).toHaveLength(
      1,
    );
    expect(
      display?.entries.filter((e) => e.item.id === "dupe-final"),
    ).toHaveLength(1);
  });

  it("drops live empty message placeholders so they cannot create ghost gaps between process rows", () => {
    const commentary = makeCommentary("checking the current behavior");
    const firstTool = makeToolCall("grep");
    const secondTool = makeToolCall("read_file");
    const emptyCommentary: ThreadItem = {
      ...makeLiveUnclassifiedAgentMessage(""),
      terminal: false,
    };
    const emptyUnknown = makeLiveUnclassifiedAgentMessage("   ");
    const reasoning: ThreadItem = {
      ...makeReasoning("comparing the results"),
      status: "in_progress",
    };
    const turn = makeTurn({
      status: "in_progress",
      items: [
        commentary,
        firstTool,
        emptyCommentary,
        secondTool,
        emptyUnknown,
        reasoning,
      ],
    });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(display?.entries.map((entry) => entry.kind)).toEqual([
      "commentary",
      "process_group",
    ]);
    expect(display?.entries[1]?.items?.map((item) => item.id)).toEqual([
      firstTool.id,
      secondTool.id,
      reasoning.id,
    ]);
  });

  it("keeps an empty running turn shell without adding an empty answer entry", () => {
    const emptyFinal: ThreadItem = {
      ...makeLiveUnclassifiedAgentMessage(""),
      terminal: true,
    };
    const turn = makeTurn({ status: "in_progress", items: [emptyFinal] });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(display?.entries).toEqual([]);
    expect(display?.hasAnswer).toBe(false);
  });
});

describe("assistant turn fold header preview", () => {
  it("in_progress + single commentary → preview = commentary text, under 120 chars passes through unchanged", () => {
    const commentary = makeCommentary("Let me look that up.");
    const turn = makeTurn({ status: "in_progress", items: [commentary] });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(display?.latestProcessPreview?.text).toBe("Let me look that up.");
  });

  it("in_progress + phase-unknown live agent text → stays in process region, does NOT trigger final-only", () => {
    // Per the message-display policy: unknown phase is treated like
    // commentary while live (rule 6) and must not collapse the process
    // fold mid-stream (rule 7). Only a confirmed `final_answer` may
    // move into the answer region. Earlier behavior promoted unknown
    // live text to "answer candidate" so the fold would stay open;
    // that broke the moment a settled final arrived and the fold
    // collapsed around the now-mislabeled unknown text. The correct
    // shape is: unknown stays in process; the fold only collapses
    // when an explicit final_answer is present.
    const live = makeLiveUnclassifiedAgentMessage(
      "I will inspect the current prompt path.",
    );
    const turn = makeTurn({ status: "in_progress", items: [live] });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    // The live text shows up as a process entry with commentary kind —
    // it surfaces inline like a normal commentary, not as an answer.
    expect(processItemIDs(display)).toEqual([live.id]);
    const liveEntry = display?.entries.find((e) => e.item.id === live.id);
    expect(liveEntry?.kind).toBe("commentary");
    // And critically: an unknown-phase in-progress item does NOT make
    // `hasAnswer` true, so the process fold does not collapse.
    expect(answerItemIDs(display)).toEqual([]);
    expect(display?.hasAnswer).toBe(false);
  });

  it("in_progress + multiple commentaries → preview = the LATEST commentary, in arrival order", () => {
    const commentaryA = makeCommentary("First thought.");
    const commentaryB = makeCommentary("Second thought.");
    const turn = makeTurn({
      status: "in_progress",
      items: [commentaryA, commentaryB],
    });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(display?.latestProcessPreview?.text).toBe("Second thought.");
  });

  it("in_progress + commentary interleaved with tool calls → preview = the commentary that came AFTER the tool, not before", () => {
    const commentaryA = makeCommentary("Let me look that up.");
    const tool = makeToolCall("read_file");
    const commentaryB = makeCommentary("Got it. The file says...");
    const turn = makeTurn({
      status: "in_progress",
      items: [commentaryA, tool, commentaryB],
    });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(display?.latestProcessPreview?.text).toBe("Got it. The file says...");
  });

  it("in_progress + tool whose args haven't arrived yet → synthesized generic summary", () => {
    const tool = makeToolCall("read_file");
    const turn = makeTurn({
      status: "in_progress",
      items: [tool],
    });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    // makeToolCall() ships no arguments, so the deterministic summary falls
    // back to the generic "using tools" text.
    expect(display?.latestProcessPreview?.kind).toBe("activity");
    expect(display?.latestProcessPreview?.text).toBe("已使用工具");
  });

  it("in_progress + tool with parsed args → preview = the rendered tool action", () => {
    // Once args parse, the renderer can produce a real title and the
    // preview takes it. We never show the pre-args "placeholder" path
    // (e.g. "读取 文件") — that's the whole point of unified timing.
    // Use a single-segment path because formatPathTarget collapses
    // multi-segment paths to their basename.
    const tool: ThreadItem = {
      ...makeToolCall("read_file"),
      arguments: JSON.stringify({ path: "bar.ts" }),
    };
    const turn = makeTurn({
      status: "in_progress",
      items: [tool],
    });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display?.latestProcessPreview?.text).toBe("已探索 1 个文件");
    expect(display?.latestProcessPreview?.kind).toBe("activity");
  });

  it("in_progress + long commentary → preview is truncated to 120 chars + ellipsis so the fold stays on two lines", () => {
    const longText = "a".repeat(200);
    const commentary = makeCommentary(longText);
    const turn = makeTurn({ status: "in_progress", items: [commentary] });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(display?.latestProcessPreview?.text).toBe("a".repeat(120) + "…");
  });

  it("in_progress + reasoning only (no commentary or tool action) → preview is undefined", () => {
    const reasoning = makeReasoning("thinking deeply about the problem");
    const turn = makeTurn({ status: "in_progress", items: [reasoning] });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(display?.latestProcessPreview?.text).toBeUndefined();
  });

  it("completed + commentary → preview is undefined; fold has status only, no live signal", () => {
    const commentary = makeCommentary("Let me look that up.");
    const final = makeFinalAnswer("Here is the answer.");
    const turn = makeTurn({
      status: "completed",
      items: [commentary, final],
      durationMs: 4200,
    });

    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);

    expect(display).toBeDefined();
    expect(display?.latestProcessPreview?.text).toBeUndefined();
  });

  it("every entry exposes settled from item.status (single source of truth)", () => {
    const commentary = makeCommentary("settled commentary");
    const liveAgent = makeLiveUnclassifiedAgentMessage("streaming text");
    const turn = makeTurn({
      status: "in_progress",
      items: [commentary, liveAgent],
    });
    const display = buildAssistantTurnDisplay(turn, undefined, renderItem);
    expect(display).toBeDefined();
    const commentaryEntry = display?.entries.find(
      (e) => e.item.id === commentary.id,
    );
    const liveEntry = display?.entries.find(
      (e) => e.item.id === liveAgent.id,
    );
    expect(commentaryEntry?.settled).toBe(true);
    expect(commentaryEntry?.streaming).toBe(false);
    expect(liveEntry?.settled).toBe(false);
    expect(liveEntry?.streaming).toBe(true);
  });
});
