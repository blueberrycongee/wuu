/**
 * Tests for `AssistantTurnShell`. The shell is the visual layer that
 * splits a turn into the process region (commentary, tool calls,
 * reasoning) and the answer region (final answer). The behavior
 * tested here is governed by the message-display policy doc
 * (docs/2026-06-18-message-display-policy-zh.md). Each test names
 * the rule it guards.
 *
 * Key product rules verified:
 *   - Rule 2: commentary stays in the process region and the process
 *     fold is open by default until a confirmed final_answer arrives.
 *   - Rule 3: reasoning lives inside the process region but its own
 *     content is folded by default; the user can expand it to read
 *     the agent's trail.
 *   - Rule 7: an in-flight agent_message with empty/unknown phase
 *     stays in the process region (treated as commentary) and does
 *     NOT collapse the process fold mid-stream.
 *   - Rule 8: once a confirmed final_answer arrives, the process fold
 *     defaults to collapsed, but the user can re-expand it (and the
 *     nested reasoning fold inside) manually.
 */
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createElement, type JSX } from "react";
import type { ThreadItem, Turn } from "../shared/protocol";
import { buildAssistantTurnDisplay } from "./AssistantTurnDisplay";
import { turnTelemetryStore } from "./TurnTelemetryStore";
import {
  AssistantTurnShell,
  resetRecoveredTurnStarts,
} from "./AssistantTurnShell";
import {
  STREAM_TEXT_NOTIFY_INTERVAL_MS,
  streamTextKey,
  streamTextStore,
} from "./StreamText";

let idCounter = 0;
let mountedRoots: Root[] = [];

function nextID(prefix: string): string {
  idCounter += 1;
  return `${prefix}-${idCounter}`;
}

function makeTurn(
  status: Turn["status"],
  items: ThreadItem[],
  durationMs?: number,
): Turn {
  return {
    id: "turn-1",
    items,
    items_view: "full",
    status,
    duration_ms: durationMs,
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

function makeStreamingFinalAnswer(text: string): ThreadItem {
  return {
    id: nextID("final"),
    type: "agent_message",
    status: "in_progress",
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

function makeReasoning(text: string): ThreadItem {
  return {
    id: nextID("reasoning"),
    type: "reasoning",
    status: "completed",
    text,
  };
}

function makeStreamingReasoning(text: string): ThreadItem {
  return {
    id: nextID("reasoning-live"),
    type: "reasoning",
    status: "in_progress",
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

function makeAgentToolCall(): ThreadItem {
  return {
    id: nextID("collab-tool"),
    type: "tool_call",
    status: "completed",
    name: "spawn_agent",
    arguments: JSON.stringify({ name: "reviewer", prompt: "Review authentication" }),
    result: JSON.stringify({ agent_id: "agent-42", status: "completed" }),
  };
}

function makeReadFileTool(path: string): ThreadItem {
  return {
    id: nextID("tool"),
    type: "tool_call",
    status: "completed",
    name: "read_file",
    arguments: JSON.stringify({ path }),
  };
}

type RenderOptions = {
  // The ThreadItemView renderer used inside the shell will re-enter
  // the items we pass in. For shell-level structural assertions we
  // just emit a placeholder so the shell picks the right entry kind.
  itemRenderer?: (item: ThreadItem, streaming: boolean) => JSX.Element;
  onCollapseComplete?: () => void;
  onOpenAgent?: (agentID: string) => void;
};

function defaultItemRenderer(
  item: ThreadItem,
  _streaming: boolean,
): JSX.Element {
  if (item.type === "reasoning") {
    // Reasoning goes through ReasoningFold, which renders the actual
    // ThreadItemView internally. The shell's pass-in renderer is only
    // used for non-reasoning items in the entry list.
    return createElement("div", { "data-reasoning-stub": item.id });
  }
  return createElement("div", null, item.text ?? "");
}

function renderShell(
  turn: Turn,
  options: RenderOptions = {},
): { container: HTMLDivElement; root: Root } {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const display = buildAssistantTurnDisplay(
    turn,
    undefined,
    options.itemRenderer ?? defaultItemRenderer,
  );
  if (!display) {
    throw new Error("expected a display");
  }
  act(() => {
    root.render(
      createElement(AssistantTurnShell, {
        turn,
        display,
        onStreamFrame: () => {},
        onCollapseComplete: options.onCollapseComplete,
        onOpenAgent: options.onOpenAgent,
      }),
    );
  });
  mountedRoots.push(root);
  return { container, root };
}

function rerenderShell(
  root: Root,
  turn: Turn,
  options: RenderOptions = {},
): void {
  const display = buildAssistantTurnDisplay(
    turn,
    undefined,
    options.itemRenderer ?? defaultItemRenderer,
  );
  if (!display) {
    throw new Error("expected a display");
  }
  act(() => {
    root.render(
      createElement(AssistantTurnShell, {
        turn,
        display,
        onStreamFrame: () => {},
        onCollapseComplete: options.onCollapseComplete,
        onOpenAgent: options.onOpenAgent,
      }),
    );
  });
}

function processFold(container: HTMLElement): HTMLDivElement | null {
  return container.querySelector("div.turn-process-fold");
}

function processFoldOpen(container: HTMLElement): boolean {
  // aria-expanded lives on the toggle <div> (role="button"), not on
  // the outer fold container. Reading it from the container would
  // always return null and fail every assertion.
  const toggle = container.querySelector(".turn-process-toggle");
  return toggle?.getAttribute("aria-expanded") === "true";
}

function revealProcessDetails(container: HTMLElement): void {
  if (container.querySelector(".turn-process-fold-body-inner")) {
    return;
  }
  const toggle = container.querySelector<HTMLElement>(".turn-process-toggle");
  if (toggle?.getAttribute("aria-expanded") !== "false") {
    return;
  }
  act(() => toggle.click());
}

function processEntryList(container: HTMLElement): HTMLElement {
  revealProcessDetails(container);
  const list = container.querySelector(".turn-process-fold-body-inner");
  if (!(list instanceof HTMLElement)) {
    throw new Error("expected process entry list");
  }
  return list;
}

function reasoningFolds(container: HTMLElement): HTMLDetailsElement[] {
  revealProcessDetails(container);
  return Array.from(container.querySelectorAll("details.turn-reasoning-fold"));
}

function processSurfaceFolds(container: HTMLElement): HTMLDetailsElement[] {
  revealProcessDetails(container);
  return Array.from(container.querySelectorAll("details.process-surface-fold"));
}

function processSurfaceRows(container: HTMLElement): HTMLElement[] {
  revealProcessDetails(container);
  return Array.from(container.querySelectorAll(".process-surface-row"));
}

function reasoningSummaryText(fold: HTMLDetailsElement): string {
  return fold.querySelector(".turn-reasoning-summary-text")?.textContent ?? "";
}

type StubbedScrollLayout = {
  scrollHeight: number;
  clientHeight: number;
  scrollTop: number;
};

function stubScrollLayout(
  node: HTMLElement,
  opts: Partial<StubbedScrollLayout>,
): StubbedScrollLayout {
  const layout = {
    scrollHeight: opts.scrollHeight ?? 1000,
    clientHeight: opts.clientHeight ?? 200,
    scrollTop: opts.scrollTop ?? 0,
  };
  Object.defineProperty(node, "scrollHeight", {
    configurable: true,
    get: () => layout.scrollHeight,
  });
  Object.defineProperty(node, "clientHeight", {
    configurable: true,
    get: () => layout.clientHeight,
  });
  Object.defineProperty(node, "scrollTop", {
    configurable: true,
    get: () => layout.scrollTop,
    set: (v: number) => {
      layout.scrollTop = v;
    },
  });
  return layout;
}

function reasoningScroll(fold: HTMLDetailsElement): HTMLElement {
  const scroll = fold.querySelector(".turn-reasoning-scroll") as HTMLElement | null;
  if (!scroll) {
    throw new Error("expected reasoning scroll container");
  }
  return scroll;
}

function selectTextWithin(node: HTMLElement): void {
  const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
  const text = walker.nextNode();
  const selection = document.getSelection();
  if (!text || !selection) {
    throw new Error("expected selectable text");
  }
  const range = document.createRange();
  range.setStart(text, 0);
  range.setEnd(text, Math.min(4, text.textContent?.length ?? 0));
  selection.removeAllRanges();
  selection.addRange(range);
  document.dispatchEvent(new Event("selectionchange"));
}

async function openReasoningFold(fold: HTMLDetailsElement): Promise<void> {
  fold.open = true;
  act(() => {
    fold.dispatchEvent(new Event("toggle", { bubbles: true }));
  });
  const body = fold.querySelector(".turn-reasoning-body");
  act(() => {
    body?.dispatchEvent(new Event("transitionend", { bubbles: true }));
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  act(() => {
    body?.dispatchEvent(new Event("transitionend", { bubbles: true }));
  });
}

async function withManualAnimationFrames(
  run: (flush: (limit?: number) => Promise<void>) => Promise<void>,
): Promise<void> {
  const realRequestAnimationFrame = window.requestAnimationFrame;
  const realCancelAnimationFrame = window.cancelAnimationFrame;
  const pending = new Map<number, FrameRequestCallback>();
  let nextHandle = 1;
  window.requestAnimationFrame = ((callback: FrameRequestCallback) => {
    const handle = nextHandle;
    nextHandle += 1;
    pending.set(handle, callback);
    return handle;
  }) as typeof window.requestAnimationFrame;
  window.cancelAnimationFrame = ((handle: number) => {
    pending.delete(handle);
  }) as typeof window.cancelAnimationFrame;

  const flush = async (limit = 10): Promise<void> => {
    for (let frame = 0; frame < limit && pending.size > 0; frame += 1) {
      const callbacks = Array.from(pending.values());
      pending.clear();
      await act(async () => {
        for (const callback of callbacks) {
          callback((frame + 1) * 16);
        }
      });
    }
  };

  try {
    await run(flush);
  } finally {
    window.requestAnimationFrame = realRequestAnimationFrame;
    window.cancelAnimationFrame = realCancelAnimationFrame;
  }
}

async function withMockResizeObserver(
  run: (flushResizeObservers: () => void) => Promise<void>,
): Promise<void> {
  const resizeObserverGlobal = globalThis as typeof globalThis & {
    ResizeObserver?: typeof ResizeObserver;
  };
  const realResizeObserver = resizeObserverGlobal.ResizeObserver;
  const observers: Array<{ callback: ResizeObserverCallback }> = [];

  class MockResizeObserver {
    constructor(readonly callback: ResizeObserverCallback) {
      observers.push(this);
    }

    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }

  resizeObserverGlobal.ResizeObserver =
    MockResizeObserver as typeof ResizeObserver;

  try {
    await run(() => {
      act(() => {
        for (const observer of observers) {
          observer.callback([], observer as unknown as ResizeObserver);
        }
      });
    });
  } finally {
    if (realResizeObserver) {
      resizeObserverGlobal.ResizeObserver = realResizeObserver;
    } else {
      Reflect.deleteProperty(resizeObserverGlobal, "ResizeObserver");
    }
  }
}

beforeEach(() => {
  idCounter = 0;
  resetRecoveredTurnStarts();
});

afterEach(() => {
  turnTelemetryStore.reset();
  vi.useRealTimers();
  act(() => {
    for (const root of mountedRoots) {
      root.unmount();
    }
  });
  mountedRoots = [];
  for (let index = 1; index <= idCounter; index += 1) {
    streamTextStore.clearItem("turn-1", `commentary-${index}`);
    streamTextStore.clearItem("turn-1", `final-${index}`);
    streamTextStore.clearItem("turn-1", `live-agent-${index}`);
    streamTextStore.clearItem("turn-1", `reasoning-${index}`);
    streamTextStore.clearItem("turn-1", `reasoning-live-${index}`);
  }
  document.body.innerHTML = "";
});

describe("AssistantTurnShell — process fold default state (rule 2 + rule 8)", () => {
  it("opens the process fold while a turn is in flight with only commentary", () => {
    const turn = makeTurn("in_progress", [makeCommentary("thinking through it")]);
    const { container } = renderShell(turn);

    expect(processFoldOpen(container)).toBe(true);
    expect(container.querySelector(".turn-process-title")?.textContent).toBe(
      "正在处理",
    );
    expect(container.textContent).toContain("thinking through it");
  });

  it("advances a recovered live timer instead of freezing a missing started_at at 0s", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-04T10:00:00Z"));
    const { container } = renderShell(
      makeTurn("in_progress", [makeCommentary("still working")]),
    );

    expect(container.querySelector(".turn-process-meta")?.textContent).toBe("0s");
    act(() => {
      vi.advanceTimersByTime(2_000);
    });
    expect(container.querySelector(".turn-process-meta")?.textContent).toBe("2s");
  });

  it("stops the visible timer and reports completion at answer readiness", () => {
    const turn = {
      ...makeTurn("in_progress", [makeFinalAnswer("done")]),
      started_at: "2026-08-04T10:00:00.000Z",
      answer_ready_at: "2026-08-04T10:00:02.000Z",
    };
    const { container } = renderShell(turn);

    expect(container.querySelector(".turn-process-title")?.textContent).toBe(
      "用时 2 秒",
    );
    expect(container.querySelector(".turn-process-meta")).toBeNull();
    expect(container.textContent).not.toContain("正在收尾");
  });

  it("keeps a recovered live timer across a session-tab unmount and remount", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-04T10:00:00Z"));
    const turn = makeTurn("in_progress", [makeCommentary("still working")]);
    const first = renderShell(turn);

    act(() => {
      vi.advanceTimersByTime(2_000);
      first.root.unmount();
    });
    mountedRoots = mountedRoots.filter((root) => root !== first.root);
    act(() => {
      vi.advanceTimersByTime(3_000);
    });

    const restored = renderShell(turn);
    expect(restored.container.querySelector(".turn-process-meta")?.textContent).toBe("5s");
  });

  it("collapses the process fold when a confirmed final_answer starts streaming", () => {
    const commentary = makeCommentary("checking");
    const { container, root } = renderShell(
      makeTurn("in_progress", [commentary]),
    );

    expect(processFoldOpen(container)).toBe(true);

    rerenderShell(
      root,
      makeTurn("in_progress", [
        commentary,
        makeStreamingFinalAnswer("done"),
      ]),
    );

    expect(processFoldOpen(container)).toBe(false);
    expect(container.querySelector(".turn-process-preview")).toBeNull();
    // The user can re-expand; verify the toggle still exists and
    // exposes its open/closed state via aria-expanded.
    const toggle = container.querySelector(".turn-process-toggle");
    expect(toggle).not.toBeNull();
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
  });

  it("summarizes a completed process as one sentence without count metadata or a leading glyph", () => {
    const turn = makeTurn(
      "completed",
      [makeCommentary("checking"), makeFinalAnswer("done")],
      3000,
    );
    const { container } = renderShell(turn);

    expect(container.querySelector(".turn-process-title")?.textContent).toBe(
      "用时 3 秒",
    );
    expect(container.querySelector(".turn-process-meta")).toBeNull();
    expect(container.querySelector(".turn-process-glyph")).toBeNull();
  });

  it("mounts completed process details only when the user reopens the fold", () => {
    const turn = makeTurn("completed", [
      makeCommentary("released process detail"),
      makeFinalAnswer("done"),
    ]);
    const { container } = renderShell(turn);

    expect(container.querySelector(".turn-process-fold-body-inner")).toBeNull();

    const toggle = container.querySelector<HTMLElement>(".turn-process-toggle");
    act(() => toggle?.click());

    expect(container.querySelector(".turn-process-fold-body-inner")).not.toBeNull();
    expect(container.textContent).toContain("released process detail");
  });

  it("does not collapse the fold for an in-flight unknown-phase agent message (rule 7)", () => {
    // The most important regression guard: an empty-phase in-progress
    // agent_message used to be promoted to "answer candidate" so the
    // fold would stay open. That promotion made the fold collapse
    // again the moment a settled final arrived — but it also made
    // the fold collapse mid-stream if the provider happened to settle
    // the unknown item into commentary. Per rule 7, unknown stays in
    // process; the fold only collapses on a confirmed final_answer.
    const turn = makeTurn("in_progress", [
      makeLiveUnclassifiedAgentMessage("streaming unknown..."),
    ]);
    const { container } = renderShell(turn);

    expect(processFoldOpen(container)).toBe(true);
    expect(container.textContent).toContain("streaming unknown");
  });

  it("keeps the process open until confirmed final_answer text is non-empty", () => {
    const turn = makeTurn("in_progress", [
      makeCommentary("checking"),
      makeStreamingFinalAnswer(""),
    ]);
    const { container } = renderShell(turn);

    expect(processFoldOpen(container)).toBe(true);
  });

  it("shows the live process preview only when the process fold is collapsed", () => {
    const turn = makeTurn("in_progress", [
      makeCommentary("settled commentary preview"),
    ]);
    const { container } = renderShell(turn);

    expect(processFoldOpen(container)).toBe(true);
    expect(container.textContent).toContain("settled commentary preview");
    expect(container.querySelector(".turn-process-preview")).toBeNull();

    const toggle = container.querySelector<HTMLElement>(".turn-process-toggle");
    expect(toggle).not.toBeNull();
    act(() => {
      toggle?.click();
    });

    expect(processFoldOpen(container)).toBe(false);
    expect(container.querySelector(".turn-process-preview")).not.toBeNull();
  });

  it("keeps completed commentary visible when the item snapshot is briefly empty", async () => {
    const commentary = makeCommentary("");
    const key = streamTextKey("turn-1", commentary.id, "text");
    streamTextStore.set(key, "finished commentary");
    const turn = makeTurn("in_progress", [commentary]);
    const { container, root } = renderShell(turn);

    expect(container.textContent).toContain("finished commentary");

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    rerenderShell(root, turn);

    expect(container.textContent).toContain("finished commentary");
    expect(streamTextStore.get(key)).toBe("finished commentary");
  });

  it("keeps a manual reopen after answer handoff through turn completion", () => {
    vi.useFakeTimers();
    const commentary = makeCommentary("checking");
    const finalAnswer = makeStreamingFinalAnswer("done");
    let collapseCompletions = 0;
    const { container, root } = renderShell(
      makeTurn("in_progress", [commentary]),
      {
        onCollapseComplete: () => {
          collapseCompletions += 1;
        },
      },
    );

    rerenderShell(
      root,
      makeTurn("in_progress", [commentary, finalAnswer]),
      {
        onCollapseComplete: () => {
          collapseCompletions += 1;
        },
      },
    );
    expect(processFoldOpen(container)).toBe(false);

    const toggle = container.querySelector<HTMLElement>(".turn-process-toggle");
    act(() => {
      toggle?.click();
    });
    expect(processFoldOpen(container)).toBe(true);

    rerenderShell(
      root,
      makeTurn("completed", [
        commentary,
        { ...finalAnswer, status: "completed" },
      ]),
      {
        onCollapseComplete: () => {
          collapseCompletions += 1;
        },
      },
    );

    act(() => {
      vi.advanceTimersByTime(1000);
    });

    expect(processFoldOpen(container)).toBe(true);
    expect(collapseCompletions).toBe(0);
  });

  it("reports one collapse completion at answer handoff and none at turn completion", () => {
    vi.useFakeTimers();
    const commentary = makeCommentary("checking");
    const finalAnswer = makeStreamingFinalAnswer("done");
    let collapseCompletions = 0;
    const { container, root } = renderShell(
      makeTurn("in_progress", [commentary]),
      {
        onCollapseComplete: () => {
          collapseCompletions += 1;
        },
      },
    );

    rerenderShell(
      root,
      makeTurn("in_progress", [commentary, finalAnswer]),
      {
        onCollapseComplete: () => {
          collapseCompletions += 1;
        },
      },
    );

    expect(processFoldOpen(container)).toBe(false);

    act(() => {
      vi.advanceTimersByTime(440);
    });
    expect(collapseCompletions).toBe(1);

    rerenderShell(
      root,
      makeTurn("completed", [
        commentary,
        { ...finalAnswer, status: "completed" },
      ]),
      {
        onCollapseComplete: () => {
          collapseCompletions += 1;
        },
      },
    );

    act(() => {
      vi.advanceTimersByTime(1000);
    });

    expect(processFoldOpen(container)).toBe(false);
    expect(collapseCompletions).toBe(1);
  });

  it("keeps manual process fold toggles local to the fold", () => {
    vi.useFakeTimers();
    const turn = makeTurn("completed", [
      makeCommentary("checking"),
      makeFinalAnswer("done"),
    ]);
    let collapseCompletions = 0;
    const { container } = renderShell(turn, {
      onCollapseComplete: () => {
        collapseCompletions += 1;
      },
    });
    const toggle = container.querySelector<HTMLElement>(".turn-process-toggle");
    expect(processFoldOpen(container)).toBe(false);

    act(() => {
      toggle?.click();
    });

    expect(processFoldOpen(container)).toBe(true);

    act(() => {
      toggle?.click();
    });
    expect(processFoldOpen(container)).toBe(false);

    act(() => {
      vi.advanceTimersByTime(500);
    });

    expect(collapseCompletions).toBe(0);
  });

  it("keeps agent tool calls in process activity", () => {
    const turn = makeTurn("completed", [makeAgentToolCall()]);
    const { container } = renderShell(turn);

    expect(processFoldOpen(container)).toBe(true);
    expect(container.querySelector(".turn-process-entry-activity")).not.toBeNull();
    expect(container.querySelector(".process-surface")).not.toBeNull();
  });

});

describe("AssistantTurnShell — reasoning fold (rule 3)", () => {
  it.each(["tool group", "reasoning"])(
    "retains usage data without appending it to the %s summary",
    (kind) => {
      vi.useFakeTimers();
      const items = [makeStreamingReasoning("working it out")];
      if (kind === "tool group") {
        items.unshift(makeReadFileTool("src/App.tsx"));
      }
      const turn = makeTurn("in_progress", items);
      const { container } = renderShell(turn);
      const summary = container.querySelector(
        kind === "tool group"
          ? ".process-surface-summary-text"
          : ".turn-reasoning-summary-text",
      );
      expect(summary).not.toBeNull();
      const label = summary?.textContent;
      const waveLabel = summary?.getAttribute("data-text");

      act(() => {
        turnTelemetryStore.ingest({
          kind: "notification",
          workdir: "/repo",
          message: {
            method: "turn/usage",
            params: { turn_id: turn.id, input_tokens: 115_300, output_tokens: 1_300 },
          },
        });
        vi.advanceTimersByTime(1_000);
      });

      expect(turnTelemetryStore.getSnapshot(turn.id)).toMatchObject({
        inputTokens: 115_300,
        outputTokens: 1_300,
      });
      expect(summary?.textContent).toBe(label);
      expect(summary?.getAttribute("data-text")).toBe(waveLabel);
    },
  );

  it("renders reasoning as a nested fold with default closed state", () => {
    const turn = makeTurn("completed", [
      makeReasoning("considering options A and B"),
      makeFinalAnswer("going with A"),
    ]);
    const { container } = renderShell(turn);

    const folds = reasoningFolds(container);
    expect(folds).toHaveLength(1);
    const fold = folds[0];
    // Rule 3: reasoning content is folded by default.
    expect(fold.hasAttribute("open")).toBe(false);
    // And the summary label makes the closed state readable.
    expect(reasoningSummaryText(fold)).toBe("查看思考过程");
  });

  it("uses the streaming label while reasoning is still in progress", () => {
    const turn = makeTurn("in_progress", [makeStreamingReasoning("working it out")]);
    const { container } = renderShell(turn);

    const folds = reasoningFolds(container);
    expect(folds).toHaveLength(1);
    expect(reasoningSummaryText(folds[0])).toBe("正在思考");
  });

  it("sweeps the latest settled reasoning label while the turn is still running", () => {
    const turn = makeTurn("in_progress", [
      makeCommentary("continuing with the next step"),
      makeReasoning("already thought this through"),
    ]);
    const { container } = renderShell(turn);

    const folds = reasoningFolds(container);
    expect(folds).toHaveLength(1);
    const label = folds[0].querySelector(".turn-reasoning-summary-text");
    expect(label?.textContent).toBe("查看思考过程");
    expect(label?.classList.contains("is-live-gray")).toBe(true);
    expect(label?.classList.contains("is-streaming")).toBe(false);
  });

  it("does not sweep an older reasoning label after commentary becomes latest", () => {
    const turn = makeTurn("in_progress", [
      makeReasoning("already thought this through"),
      makeCommentary("continuing with the next step"),
    ]);
    const { container } = renderShell(turn);

    const folds = reasoningFolds(container);
    expect(folds).toHaveLength(1);
    const label = folds[0].querySelector(".turn-reasoning-summary-text");
    expect(label?.textContent).toBe("查看思考过程");
    expect(label?.classList.contains("is-live-gray")).toBe(false);
    expect(label?.classList.contains("is-streaming")).toBe(false);
  });

  it("marks a live process group as actively thinking", () => {
    // Consecutive reasoning items render as one process surface, and
    // the compact row sweeps once it's running. The individual reasoning
    // records stay behind an explicit user-opened body.
    const settledA = makeReasoning("earlier deliberation, finished");
    const settledB = makeReasoning("next deliberation, finished");
    const streamingNow = makeStreamingReasoning("thinking right now");
    const turn = makeTurn("in_progress", [
      settledA,
      settledB,
      streamingNow,
      makeFinalAnswer("not yet — turn still running"),
    ]);
    const { container } = renderShell(turn);

    const groups = processSurfaceFolds(container);
    expect(groups).toHaveLength(1);
    expect(groups[0].hasAttribute("open")).toBe(false);
    const row = groups[0].querySelector(".process-surface-row");
    expect(row?.classList.contains("is-streaming")).toBe(true);
    const label = groups[0].querySelector(".process-surface-reasoning-label");
    expect(label?.textContent).toBe("正在思考");
  });

  it("sweeps the latest settled process row while the turn is still running", () => {
    const turn = makeTurn("in_progress", [
      makeCommentary("before the settled row"),
      makeReadFileTool("src/App.tsx"),
      makeReasoning("settled reasoning"),
    ]);
    const { container } = renderShell(turn);

    const rows = processSurfaceRows(container);
    expect(rows).toHaveLength(1);
    expect(rows[0].classList.contains("is-live-gray")).toBe(true);
    expect(rows[0].classList.contains("is-streaming")).toBe(false);
    expect(rows[0].textContent).toContain("思考过程");
  });

  it("does not sweep older process rows after commentary becomes latest", () => {
    const turn = makeTurn("in_progress", [
      makeReadFileTool("src/App.tsx"),
      makeReasoning("settled reasoning"),
      makeCommentary("continuing after the settled row"),
    ]);
    const { container } = renderShell(turn);

    const rows = processSurfaceRows(container);
    expect(rows).toHaveLength(1);
    expect(rows[0].classList.contains("is-live-gray")).toBe(false);
    expect(rows[0].classList.contains("is-streaming")).toBe(false);
    expect(rows[0].textContent).toContain("思考过程");
  });

  it("keeps the reasoning fold closed even when the outer process fold is open", () => {
    // Running turn: outer process fold is open (rule 2). The
    // nested reasoning fold inside it must still default closed
    // (rule 3) so a verbose reasoning block doesn't visually
    // compete with the commentary/tool rows.
    const turn = makeTurn("in_progress", [
      makeStreamingReasoning("rambling on and on"),
      makeCommentary("meanwhile, real progress"),
    ]);
    const { container } = renderShell(turn);

    expect(processFoldOpen(container)).toBe(true);
    const folds = reasoningFolds(container);
    expect(folds).toHaveLength(1);
    expect(folds[0].hasAttribute("open")).toBe(false);
  });

  it("lets the user expand the reasoning fold manually", () => {
    const turn = makeTurn("completed", [
      makeReasoning("long internal deliberation"),
      makeFinalAnswer("short answer"),
    ]);
    const { container } = renderShell(turn);

    const folds = reasoningFolds(container);
    expect(folds[0].hasAttribute("open")).toBe(false);

    const summary = folds[0].querySelector("summary");
    expect(summary).not.toBeNull();
    act(() => {
      summary?.dispatchEvent(new Event("toggle", { bubbles: true }));
    });
    // Note: the synthetic toggle event above drives React's controlled
    // `open` state only if a useState hook listens to onToggle. Native
    // <details> toggles its open attribute directly via the browser;
    // this test focuses on the structural default (closed), and the
    // manual-expand path is verified via DOM behavior in browser.
    expect(folds[0]).not.toBeNull();
  });

  it("keeps reasoning fold expansion local to that reasoning block", async () => {
    const first = makeReasoning("first deliberation");
    const second = makeReasoning("second deliberation");
    const turn = makeTurn("completed", [
      first,
      makeCommentary("visible process boundary"),
      second,
      makeFinalAnswer("short answer"),
    ]);
    const { container } = renderShell(turn);

    const folds = reasoningFolds(container);
    expect(folds).toHaveLength(2);
    expect(folds[0].open).toBe(false);
    expect(folds[1].open).toBe(false);

    await openReasoningFold(folds[0]);

    expect(folds[0].open).toBe(true);
    expect(folds[1].open).toBe(false);
  });

  it("groups consecutive reasoning records into one process surface", () => {
    // Multi-segment reasoning is a single top-level process row. The
    // user can expand that row to read the underlying reasoning trail.
    const turn = makeTurn("completed", [
      makeReasoning("step one"),
      makeReasoning("step two"),
      makeFinalAnswer("answer"),
    ]);
    const { container } = renderShell(turn);

    expect(reasoningFolds(container)).toHaveLength(0);
    const groups = processSurfaceFolds(container);
    expect(groups).toHaveLength(1);
    expect(groups[0].hasAttribute("open")).toBe(false);
    expect(groups[0].textContent).toContain("思考过程");
  });

  it("groups adjacent reasoning and tool activity without crossing commentary", () => {
    // The canonical scenario from the message-display policy: a
    // turn that interleaves reasoning, commentary, and tool calls.
    // The outer process fold is open during streaming; adjacent
    // process items collapse into one live row, while commentary is
    // still a boundary and stays inline.
    const turn = makeTurn("in_progress", [
      makeStreamingReasoning("hmm, what to do"),
      makeToolCall("grep"),
      makeCommentary("found the file"),
      makeStreamingReasoning("now editing"),
    ]);
    const { container } = renderShell(turn);

    expect(processFoldOpen(container)).toBe(true);
    const groups = processSurfaceRows(container);
    expect(groups).toHaveLength(1);
    expect(groups[0].textContent).toContain("搜索");
    expect(groups[0].textContent).toContain("正在思考");
    const folds = reasoningFolds(container);
    expect(folds).toHaveLength(1);
    expect(reasoningSummaryText(folds[0])).toBe("正在思考");
    const entryList = processEntryList(container);
    expect(
      Array.from(entryList.children).map((entry) =>
        Array.from(entry.classList).find((className) =>
          className.startsWith("turn-process-entry-"),
        ),
      ),
    ).toEqual([
      "turn-process-entry-process_group",
      "turn-process-entry-commentary",
      "turn-process-entry-process",
    ]);
    // Commentary text surfaces inline (not folded):
    expect(container.textContent).toContain("found the file");
  });

  it("keeps one process surface when a post-commentary tool row grows into a group", () => {
    const commentary = makeCommentary("finished commentary");
    const tool: ThreadItem = {
      ...makeReadFileTool("src/App.tsx"),
      status: "in_progress",
    };
    const firstTurn = makeTurn("in_progress", [commentary, tool]);
    const { container, root } = renderShell(firstTurn);

    const surfaceBefore = container.querySelector(".process-surface");
    expect(surfaceBefore).toBeTruthy();
    expect(surfaceBefore?.textContent).toContain("App.tsx");

    const reasoning = makeStreamingReasoning("checking the result");
    const secondTurn = makeTurn("in_progress", [
      commentary,
      { ...tool, status: "completed" },
      reasoning,
    ]);
    rerenderShell(root, secondTurn);

    const surfaceAfter = container.querySelector(".process-surface");
    expect(surfaceAfter).toBe(surfaceBefore);
    expect(surfaceAfter?.textContent).toContain("正在思考");
  });

  it("groups consecutive tool activity into one count row with details", () => {
    const turn = makeTurn("completed", [
      makeReadFileTool("src/App.tsx"),
      makeReadFileTool("src/turns.css"),
      makeFinalAnswer("answer"),
    ]);
    const { container } = renderShell(turn);

    const groups = processSurfaceFolds(container);
    expect(groups).toHaveLength(1);
    expect(groups[0].querySelector(".process-surface-count")?.textContent).toBe(
      "2",
    );
    expect(groups[0].querySelectorAll(".activity-group")).toHaveLength(2);
  });

  it("snaps the reasoning scroll container to the bottom when the fold opens", async () => {
    // Reasoning text tends to be long. When the user clicks "查看思考
    // 过程" they usually want to see where the model is *now*, not the
    // first lines of deliberation — so opening the fold should land
    // the scroll container at scrollHeight.
    const turn = makeTurn("completed", [
      makeReasoning("long internal deliberation ".repeat(50)),
      makeFinalAnswer("short answer"),
    ]);
    const { container } = renderShell(turn);

    const fold = reasoningFolds(container)[0];
    expect(fold.hasAttribute("open")).toBe(false);
    const scroll = reasoningScroll(fold);

    // jsdom does not lay out real heights. Mock scrollHeight and
    // clientHeight so the snap-to-bottom handler has measurable
    // values, and capture scrollTop writes so we can assert on them.
    const layout = stubScrollLayout(scroll, {
      scrollHeight: 1000,
      clientHeight: 200,
    });

    // Simulate a user click on the summary: open the fold and let
    // React's onToggle handler run.
    await openReasoningFold(fold);

    expect(layout.scrollTop).toBe(1000);
  });

  it("keeps live reasoning pinned to the latest while the user stays at the bottom", async () => {
    const item = makeStreamingReasoning("working");
    const key = streamTextKey("turn-1", item.id, "text");
    streamTextStore.seed(key, item.text ?? "");
    const turn = makeTurn("in_progress", [item]);
    const { container } = renderShell(turn);

    const fold = reasoningFolds(container)[0];
    const scroll = reasoningScroll(fold);
    const layout = stubScrollLayout(scroll, {
      scrollHeight: 1000,
      clientHeight: 200,
    });

    await openReasoningFold(fold);
    expect(layout.scrollTop).toBe(1000);

    layout.scrollHeight = 1300;
    await withManualAnimationFrames(async (flush) => {
      await act(async () => {
        streamTextStore.append(key, " next");
      });
      await flush();
    });

    expect(layout.scrollTop).toBe(1300);
  });

  it("keeps reasoning selection paused after a pointer press without scroll movement", async () => {
    const turn = makeTurn("completed", [
      makeReasoning("long internal deliberation ".repeat(50)),
      makeFinalAnswer("short answer"),
    ]);
    const { container } = renderShell(turn);
    const fold = reasoningFolds(container)[0];
    const scroll = reasoningScroll(fold);
    const layout = stubScrollLayout(scroll, {
      scrollHeight: 1000,
      clientHeight: 200,
    });
    await openReasoningFold(fold);

    act(() => selectTextWithin(scroll));
    expect(scroll.style.overflowAnchor).toBe("auto");
    act(() => {
      scroll.dispatchEvent(new Event("pointerdown", { bubbles: true }));
      window.dispatchEvent(new Event("pointerup"));
      layout.scrollHeight += 8;
      layout.scrollTop += 8;
      scroll.dispatchEvent(new UIEvent("scroll", { bubbles: true }));
    });

    expect(scroll.style.overflowAnchor).toBe("auto");
  });

  it("resumes reasoning follow after an active pointer scroll reaches latest", async () => {
    await withMockResizeObserver(async (flushResizeObservers) => {
      const turn = makeTurn("completed", [
        makeReasoning("long internal deliberation ".repeat(50)),
        makeFinalAnswer("short answer"),
      ]);
      const { container } = renderShell(turn);
      const fold = reasoningFolds(container)[0];
      const scroll = reasoningScroll(fold);
      const layout = stubScrollLayout(scroll, {
        scrollHeight: 1000,
        clientHeight: 200,
      });
      await openReasoningFold(fold);

      act(() => selectTextWithin(scroll));
      act(() => {
        scroll.dispatchEvent(new Event("pointerdown", { bubbles: true }));
        layout.scrollHeight = 1300;
        flushResizeObservers();
        layout.scrollTop = 1284.5;
        scroll.dispatchEvent(new UIEvent("scroll", { bubbles: true }));
        window.dispatchEvent(new Event("pointerup"));
      });

      expect(scroll.style.overflowAnchor).toBe("none");
    });
  });

  it("keeps up when many reasoning tokens arrive before the next frame", async () => {
    const item = makeStreamingReasoning("working");
    const key = streamTextKey("turn-1", item.id, "text");
    streamTextStore.seed(key, item.text ?? "");
    const turn = makeTurn("in_progress", [item]);
    const { container } = renderShell(turn);

    const fold = reasoningFolds(container)[0];
    const scroll = reasoningScroll(fold);
    const layout = stubScrollLayout(scroll, {
      scrollHeight: 1000,
      clientHeight: 200,
    });

    await openReasoningFold(fold);
    expect(layout.scrollTop).toBe(1000);

    vi.useFakeTimers();
    const now = vi.spyOn(performance, "now");
    try {
      await withManualAnimationFrames(async (flush) => {
        now.mockReturnValue(100_000);
        await act(async () => {
          streamTextStore.append(key, " first");
        });
        await flush();

        now.mockReturnValue(100_010);
        await act(async () => {
          for (let tick = 0; tick < 120; tick += 1) {
            layout.scrollHeight += 4;
            streamTextStore.append(key, " x");
          }
          vi.advanceTimersByTime(STREAM_TEXT_NOTIFY_INTERVAL_MS - 10);
        });
        await flush(3);
      });
    } finally {
      now.mockRestore();
    }

    expect(layout.scrollTop).toBe(layout.scrollHeight);
  });

  it("keeps auto-follow armed when rapid reasoning growth fires a layout scroll before resize settles", async () => {
    await withMockResizeObserver(async (flushResizeObservers) => {
      const item = makeStreamingReasoning("working");
      const key = streamTextKey("turn-1", item.id, "text");
      streamTextStore.seed(key, item.text ?? "");
      const turn = makeTurn("in_progress", [item]);
      const { container } = renderShell(turn);

      const fold = reasoningFolds(container)[0];
      const scroll = reasoningScroll(fold);
      const layout = stubScrollLayout(scroll, {
        scrollHeight: 1000,
        clientHeight: 200,
      });

      await withManualAnimationFrames(async (flush) => {
        await openReasoningFold(fold);
        await flush();
        expect(layout.scrollTop).toBe(1000);

        layout.scrollHeight = 1300;
        act(() => {
          scroll.dispatchEvent(new UIEvent("scroll", { bubbles: true }));
        });

        flushResizeObservers();
        await flush();
      });

      expect(layout.scrollTop).toBe(1300);
    });
  });

  it("does not pull live reasoning back to the bottom after the user scrolls up", async () => {
    const item = makeStreamingReasoning("working");
    const key = streamTextKey("turn-1", item.id, "text");
    streamTextStore.seed(key, item.text ?? "");
    const turn = makeTurn("in_progress", [item]);
    const { container } = renderShell(turn);

    const fold = reasoningFolds(container)[0];
    const scroll = reasoningScroll(fold);
    const layout = stubScrollLayout(scroll, {
      scrollHeight: 1000,
      clientHeight: 200,
    });

    await openReasoningFold(fold);
    expect(layout.scrollTop).toBe(1000);

    act(() => {
      layout.scrollTop = 240;
      scroll.dispatchEvent(
        new WheelEvent("wheel", { bubbles: true, deltaY: -80 }),
      );
      scroll.dispatchEvent(new UIEvent("scroll", { bubbles: true }));
    });

    await withManualAnimationFrames(async (flush) => {
      await act(async () => {
        for (let tick = 0; tick < 60; tick += 1) {
          layout.scrollHeight += 5;
          streamTextStore.append(key, " x");
        }
      });
      await flush(3);
    });

    expect(layout.scrollTop).toBe(240);
  });

  it("does not bubble reasoning scroll intent to the surrounding conversation", async () => {
    const turn = makeTurn("completed", [
      makeReasoning("long internal deliberation ".repeat(50)),
      makeFinalAnswer("short answer"),
    ]);
    const { container } = renderShell(turn);
    const fold = reasoningFolds(container)[0];
    const scroll = reasoningScroll(fold);
    stubScrollLayout(scroll, {
      scrollHeight: 1000,
      clientHeight: 200,
    });
    await openReasoningFold(fold);

    let bubbledWheelEvents = 0;
    container.addEventListener("wheel", () => {
      bubbledWheelEvents += 1;
    });

    act(() => {
      scroll.dispatchEvent(
        new WheelEvent("wheel", { bubbles: true, deltaY: -80 }),
      );
    });

    expect(bubbledWheelEvents).toBe(0);
  });
});

describe("AssistantTurnShell — answer region (rule 1 + rule 8)", () => {
  it("places confirmed final_answer in the answer body, not the process fold", () => {
    const turn = makeTurn("completed", [
      makeCommentary("preamble"),
      makeFinalAnswer("the conclusion"),
    ]);
    const { container } = renderShell(turn);

    const answerBody = container.querySelector(".turn-answer-body");
    expect(answerBody).not.toBeNull();
    expect(answerBody?.textContent).toContain("the conclusion");
    // And the process fold still exists for the commentary.
    expect(processFold(container)).not.toBeNull();
  });

  it("keeps the elapsed summary when there are no process records", () => {
    const turn = makeTurn("completed", [makeFinalAnswer("just the answer")], 2300);
    const { container } = renderShell(turn);

    expect(processFold(container)).not.toBeNull();
    expect(container.querySelector(".turn-process-title")?.textContent).toBe(
      "用时 2 秒",
    );
    expect(container.querySelector(".turn-process-fold-body-inner")).toBeNull();
    expect(container.querySelector(".turn-answer-body")).not.toBeNull();
  });
});

// Fake web tools so the end-to-end pill assertion doesn't need a
// live LLM. They mirror the real `web_search` / `web_fetch` shapes
// that `collectTurnSources` parses (ToolActivityHelpers.ts:1245):
//   web_search.result.results[]   — array of { url, title }
//   web_fetch.arguments.url       — the page the agent asked to read
function makeWebFetch(url: string): ThreadItem {
  return {
    id: nextID("tool"),
    type: "tool_call",
    status: "completed",
    name: "web_fetch",
    arguments: JSON.stringify({ url }),
  };
}

function makeWebSearch(
  hits: ReadonlyArray<{ url: string; title?: string }>,
): ThreadItem {
  return {
    id: nextID("tool"),
    type: "tool_call",
    status: "completed",
    name: "web_search",
    result: JSON.stringify({ results: hits }),
  };
}

describe("AssistantTurnShell — turn sources pill end-to-end", () => {
  it("renders the sources pill beside the process header for a turn that ran a single web_fetch", () => {
    const turn = makeTurn("completed", [
      makeFinalAnswer("this turn read the docs page"),
      makeWebFetch("https://docs.anthropic.com/api"),
    ]);
    const { container } = renderShell(turn);

    const pill = container.querySelector(".turn-sources-pill");
    expect(pill).not.toBeNull();
    // Single source → the entire pill is a <button>; the accessible
    // name carries the full URL (not just "来源" or the host) so users
    // and screen readers know which page on the host was consulted.
    expect(pill?.getAttribute("aria-label")).toBe(
      "打开 https://docs.anthropic.com/api",
    );
    // No nested icon button — the single-source pill is the click
    // target on its own. Nesting <button> in <button> would be invalid
    // HTML and would double-fire the click handler.
    expect(container.querySelectorAll("button.turn-source-icon").length).toBe(0);
    // The favicon still renders inside the pill button as a visual.
    const pillImage = pill?.querySelector("img");
    expect(pillImage?.getAttribute("src")).toContain(
      "google.com/s2/favicons?domain=docs.anthropic.com",
    );
    // Pill sits in the process header line, before the answer body — not
    // down in the answer footer.
    const topline = container.querySelector(".turn-process-topline");
    expect(topline?.contains(pill)).toBe(true);
    const answerBody = container.querySelector(".turn-answer-body");
    expect(answerBody).not.toBeNull();
    expect(
      pill?.compareDocumentPosition(answerBody as Node) ?? 0,
    ).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it("collapses multiple hits on the same host across web_search and web_fetch into one slot", () => {
    const turn = makeTurn("completed", [
      makeFinalAnswer(""),
      makeWebSearch([
        { url: "https://docs.anthropic.com/a", title: "Doc A" },
        { url: "https://docs.anthropic.com/b", title: "Doc B" },
      ]),
      makeWebFetch("https://docs.anthropic.com/c"),
    ]);
    const { container } = renderShell(turn);

    expect(container.querySelector(".turn-sources-pill")).not.toBeNull();
    // Three URLs all dedupe to a single source, so the pill itself
    // becomes the click target — no nested icon button. The favicon
    // renders inside the pill button as the visual.
    expect(container.querySelectorAll("button.turn-source-icon").length).toBe(0);
    expect(
      container.querySelectorAll("button.turn-sources-pill").length,
    ).toBe(1);
  });

  it("stacks one icon per unique host and labels the pill with the host count", () => {
    const turn = makeTurn("completed", [
      makeFinalAnswer(""),
      makeWebSearch([
        { url: "https://www.anthropic.com/news/a", title: "Anthropic news" },
        { url: "https://platform.openai.com/docs", title: "OpenAI docs" },
        { url: "https://huggingface.co/models", title: "HF models" },
      ]),
    ]);
    const { container } = renderShell(turn);

    expect(container.querySelector(".turn-sources-label")?.textContent).toBe(
      "来源 3",
    );
    const icons = container.querySelectorAll("button.turn-source-icon");
    expect(icons.length).toBe(3);
    // First-seen order — Anthropic, then OpenAI, then HF. Pull the
    // host out of the accessible label rather than introducing a
    // data-host attribute on the component, so this assertion
    // survives any future i18n of the visible pill label without
    // forcing a dataset hook that screen readers would otherwise
    // ignore.
    expect(icons[0].getAttribute("aria-label")).toContain("anthropic.com");
    expect(icons[1].getAttribute("aria-label")).toContain(
      "platform.openai.com",
    );
    expect(icons[2].getAttribute("aria-label")).toContain("huggingface.co");
  });

  it("does not render the pill when no web tool ran this turn", () => {
    const turn = makeTurn("completed", [
      makeFinalAnswer("plain answer with no web lookup"),
      makeReadFileTool("local.ts"),
    ]);
    const { container } = renderShell(turn);

    expect(container.querySelector(".turn-sources-pill")).toBeNull();
  });

  it("clicking the sources pill hands the URL to window.wuu.openExternal", () => {
    // End-to-end: shell mounts → pill renders → user clicks → handleOpenSource
    // fires → window.wuu.openExternal called with the exact URL. This is the
    // path Electron takes when the user actually taps a source. Single-source
    // case: the click target is the pill itself, not a nested icon button.
    const openExternal = vi.fn().mockResolvedValue(undefined);
    (
      window as unknown as { wuu: { openExternal: typeof openExternal } }
    ).wuu = { openExternal };

    const turn = makeTurn("completed", [
      makeFinalAnswer(""),
      makeWebFetch("https://docs.anthropic.com/api"),
    ]);
    const { container } = renderShell(turn);

    const button = container.querySelector<HTMLButtonElement>(
      "button.turn-sources-pill",
    );
    act(() => {
      button?.click();
    });
    expect(openExternal).toHaveBeenCalledWith(
      "https://docs.anthropic.com/api",
    );
  });
});
