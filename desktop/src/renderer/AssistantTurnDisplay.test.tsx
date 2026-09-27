import { createElement } from "react";
import { describe, expect, it } from "vitest";
import type { ThreadItem, Turn } from "../shared/protocol";
import { buildAssistantTurnDisplay } from "./AssistantTurnDisplay";

let idCounter = 0;

function nextID(prefix: string): string {
  idCounter += 1;
  return `${prefix}-${idCounter}`;
}

function makeTurn(status: Turn["status"], items: ThreadItem[]): Turn {
  return { id: "turn-1", items, items_view: "full", status };
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

function makeReasoning(): ThreadItem {
  return {
    id: nextID("reasoning"),
    type: "reasoning",
    status: "completed",
    text: "thinking",
  };
}

function makeToolCall(): ThreadItem {
  return {
    id: nextID("tool"),
    type: "tool_call",
    status: "completed",
    name: "bash",
  };
}

function makeGeneratedQuery(text: string): ThreadItem {
  return {
    id: nextID("generated-query"),
    type: "user_message",
    status: "completed",
    text,
    read_only: true,
    origin: "plugin",
    presentation_kind: "query_bubble",
  };
}

const stubRenderer = (): JSX.Element => createElement("div");

function build(turn: Turn) {
  const display = buildAssistantTurnDisplay(turn, undefined, stubRenderer);
  if (!display) {
    throw new Error("expected a display");
  }
  return display;
}

describe("buildAssistantTurnDisplay generated queries", () => {
  it("leaves generated queries to the user-message renderer", () => {
    const display = build(
      makeTurn("completed", [
        makeGeneratedQuery("后台结果二已更新"),
        makeCommentary("子代理 1: ok\n子代理 2: ok"),
        makeGeneratedQuery("后台结果一已更新"),
        makeCommentary("已确认"),
      ]),
    );
    expect(display.entries.map((entry) => entry.kind)).toEqual([
      "commentary",
      "commentary",
    ]);
  });

  it("does not duplicate generated queries beside tool activity", () => {
    const display = build(
      makeTurn("completed", [makeToolCall(), makeGeneratedQuery("检查已完成")]),
    );
    expect(display.entries.map((entry) => entry.kind)).toEqual(["activity"]);
  });

  it("does not duplicate generated queries beside reasoning", () => {
    const display = build(
      makeTurn("completed", [makeReasoning(), makeGeneratedQuery("检查已完成")]),
    );
    expect(display.entries).toHaveLength(1);
    expect(display.entries[0].item.type).toBe("reasoning");
  });

  it("does not create an assistant display for a generated-query-only turn", () => {
    expect(
      buildAssistantTurnDisplay(
        makeTurn("completed", [makeGeneratedQuery("一"), makeGeneratedQuery("二")]),
        undefined,
        stubRenderer,
      ),
    ).toBeUndefined();
  });

  it("ignores ordinary user messages too", () => {
    const display = build(
      makeTurn("completed", [
        {
          id: nextID("user"),
          type: "user_message",
          status: "completed",
          text: "普通用户消息",
        },
        makeCommentary("回复"),
      ]),
    );
    expect(display.entries.map((entry) => entry.kind)).toEqual(["commentary"]);
  });
});

describe("buildAssistantTurnDisplay compaction notices", () => {
  it("omits a completed compaction attempt that changed nothing", () => {
    const commentary = makeCommentary("继续处理");
    const unchanged: ThreadItem = {
      id: nextID("compact-unchanged"),
      type: "context_compaction",
      status: "completed",
      reason: "proactive",
      text: "Nothing to compact yet; history is unchanged.",
    };

    const display = build(makeTurn("completed", [commentary, unchanged]));

    expect(display.entries.map((entry) => entry.item.id)).toEqual([commentary.id]);
    expect(
      buildAssistantTurnDisplay(
        makeTurn("completed", [unchanged]),
        undefined,
        stubRenderer,
      ),
    ).toBeUndefined();
  });

  it("keeps only the authoritative result when a legacy note status is also present", () => {
    const noteStatus: ThreadItem = {
      id: nextID("compact-note"),
      type: "context_compaction",
      status: "completed",
      reason: "context_note",
      text: "Context note used.",
    };
    const result: ThreadItem = {
      id: nextID("compact-result"),
      type: "context_compaction",
      status: "completed",
      reason: "proactive",
      text: "Compacted history: 162 → 88 messages (~240k → ~123k tokens)",
    };

    const display = build(makeTurn("completed", [noteStatus, result]));

    expect(display.entries.map((entry) => entry.item.id)).toEqual([result.id]);
  });

  it.each(["Context note updated.", "Context note failed. uncovered history cannot fit beside the continuation note"])("hides standalone background diagnostics: %s", (text) => {
    const noteStatus: ThreadItem = {
      id: nextID("compact-note"),
      type: "context_compaction",
      status: "completed",
      reason: "context_note",
      text,
    };

    expect(buildAssistantTurnDisplay(makeTurn("completed", [noteStatus]), undefined, stubRenderer)).toBeUndefined();
  });
});

it("keeps image-only assistant replies in the answer region", () => {
  const item = {
    ...makeCommentary(""),
    terminal: true,
    images: [{ media_type: "image/png", data: "aW1hZ2U=" }],
  };
  const display = build(makeTurn("completed", [item]));
  expect(display.entries.some(entry => entry.item.id === item.id && entry.position === "answer")).toBe(true);
});
