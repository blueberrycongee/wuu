import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Thread, Turn } from "../shared/protocol";
import {
  AGENT_NOTIFICATION_NAME,
  PROCESS_NOTIFICATION_NAME,
} from "./InternalUserNotification";
import {
  firstUserMessageAnchor,
  lastUserMessageAnchor,
  scrollToUserMessage,
  threadReplySnippet,
  truncateReplyPreview,
  turnReplySnippet,
  firstUserMessageText,
  turnAnchorID,
  userMessageAnchorID,
} from "./TurnViewHelpers";

function handoffText(): string {
  return JSON.stringify({
    author: "/root/recovery_worker",
    recipient: "/root",
    content: `<subagent_notification>\n${JSON.stringify({
      agent_path: "/root/recovery_worker",
      status: {
        type: "agent_result",
        agent_id: "worker-1",
        task_name: "recovery_worker",
        status: "completed"
      }
    })}\n</subagent_notification>`,
    trigger_turn: true
  });
}

function processNotificationText(): string {
  return '<process_notification>{"process_id":"proc-1"}</process_notification>';
}

/**
 * Build a tiny DOM tree that mimics the conversation scroll container
 * (`.scroll-region` or `.conversation-split-body`) wrapping a
 * `user-message-block` with the anchor ID. Returns the scroll container
 * so each test can assert the scrollTop delta.
 */
function mountAnchor({
  variant,
  containerHeight = 800,
  containerScrollHeight = 1600,
  nodeOffsetTop = 1200,
}: {
  variant: "scroll-region" | "conversation-split-body";
  containerHeight?: number;
  containerScrollHeight?: number;
  nodeOffsetTop?: number;
}): {
  container: HTMLElement;
  node: HTMLElement;
} {
  const container = document.createElement("div");
  container.className = variant;
  Object.defineProperty(container, "clientHeight", {
    configurable: true,
    value: containerHeight,
  });
  Object.defineProperty(container, "scrollHeight", {
    configurable: true,
    value: containerScrollHeight,
  });
  container.scrollTop = 0;

  const spacer = document.createElement("div");
  spacer.style.height = `${nodeOffsetTop}px`;
  container.appendChild(spacer);

  const node = document.createElement("div");
  node.className = "user-message-block";
  node.id = userMessageAnchorID("turn-1", "item-1");
  container.appendChild(node);

  // Fill the remainder of the scroll surface so it actually overflows.
  const tail = document.createElement("div");
  tail.style.height = `${Math.max(0, containerScrollHeight - nodeOffsetTop - 80)}px`;
  container.appendChild(tail);

  document.body.appendChild(container);

  // jsdom returns {0,0,0,0} for every getBoundingClientRect. Patch the
  // container and node so the helper's offset math resolves to the
  // values we set up above — otherwise `nodeRect.top - containerRect.top`
  // is always 0 and the helper bails out of its scroll call.
  container.getBoundingClientRect = () => ({
    top: 0,
    left: 0,
    right: container.clientWidth,
    bottom: container.clientHeight,
    width: container.clientWidth,
    height: container.clientHeight,
    x: 0,
    y: 0,
    toJSON: () => ({}),
  });
  node.getBoundingClientRect = () => ({
    top: nodeOffsetTop,
    left: 0,
    right: 0,
    bottom: nodeOffsetTop,
    width: 0,
    height: 0,
    x: 0,
    y: nodeOffsetTop,
    toJSON: () => ({}),
  });

  return { container, node };
}

beforeEach(() => {
  vi.useFakeTimers();
  // jsdom does not implement scrollTo — stub it so the tests can
  // observe what scroll target the helper picks.
  Element.prototype.scrollTo = function scrollTo(
    this: HTMLElement,
    options: ScrollToOptions | number,
    _y?: number,
  ) {
    if (typeof options === "number") {
      this.scrollTop = options;
      return;
    }
    if (options?.top !== undefined) {
      this.scrollTop = options.top;
    }
  } as typeof Element.prototype.scrollTo;
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.clearAllTimers();
  vi.useRealTimers();
});

describe("scrollToUserMessage", () => {
  it("scrolls the .scroll-region container so the anchor lands below the top padding", () => {
    const { container, node } = mountAnchor({
      variant: "scroll-region",
      containerHeight: 800,
      // scrollHeight (1800) - clientHeight (800) = 1000, so an offsetTop
      // of 600 leaves plenty of room without triggering the clamp.
      containerScrollHeight: 1800,
      nodeOffsetTop: 600,
    });

    scrollToUserMessage("turn-1", "item-1");

    // The helper subtracts JUMP_TOP_OFFSET_PX (64) from the node's
    // offsetTop so the message sits 64px below the visible top — this
    // is what gives the jump enough headroom to keep the previous turn
    // header in view.
    expect(container.scrollTop).toBe(600 - 64);
    expect(node.classList.contains("user-message-jump-flash")).toBe(true);
  });

  it("scrolls without the highlight pulse when highlight is disabled", () => {
    const { container, node } = mountAnchor({
      variant: "scroll-region",
      containerHeight: 800,
      containerScrollHeight: 1800,
      nodeOffsetTop: 600,
    });

    scrollToUserMessage("turn-1", "item-1", { highlight: false });

    expect(container.scrollTop).toBe(600 - 64);
    expect(node.classList.contains("user-message-jump-flash")).toBe(false);
  });

  it("also scrolls the split-pane container when split mode is active", () => {
    const { container, node } = mountAnchor({
      variant: "conversation-split-body",
      containerHeight: 600,
      // scrollHeight (1600) - clientHeight (600) = 1000, so offsetTop
      // 700 does not hit the clamp.
      containerScrollHeight: 1600,
      nodeOffsetTop: 700,
    });

    scrollToUserMessage("turn-1", "item-1");

    expect(container.scrollTop).toBe(700 - 64);
    expect(node.classList.contains("user-message-jump-flash")).toBe(true);
  });

  it("clamps the target scrollTop so the scroll surface does not overshoot", () => {
    const { container } = mountAnchor({
      variant: "scroll-region",
      containerHeight: 800,
      // Bottom is at 800 (1600 - 800). Pulling the message up by 64
      // would request 1600-64, but the helper should clamp to 800.
      containerScrollHeight: 1600,
      nodeOffsetTop: 1600,
    });

    scrollToUserMessage("turn-1", "item-1");

    expect(container.scrollTop).toBe(800);
  });

  it("retries the anchor lookup until the DOM catches up", async () => {
    const { container } = mountAnchor({
      variant: "scroll-region",
      containerHeight: 800,
      containerScrollHeight: 1800,
      nodeOffsetTop: 600,
    });

    // Hide the anchor first so the helper has to retry.
    const existing = container.querySelector<HTMLElement>(
      `#${userMessageAnchorID("turn-1", "item-1")}`,
    );
    existing?.remove();

    scrollToUserMessage("turn-1", "item-1");

    expect(container.scrollTop).toBe(0);

    // Re-mount the anchor before the next retry fires.
    const replacement = document.createElement("div");
    replacement.className = "user-message-block";
    replacement.id = userMessageAnchorID("turn-1", "item-1");
    Object.defineProperty(replacement, "getBoundingClientRect", {
      configurable: true,
      value: () => ({
        top: 600,
        left: 0,
        right: 0,
        bottom: 600,
        width: 0,
        height: 0,
        x: 0,
        y: 600,
        toJSON: () => ({}),
      }),
    });
    container.appendChild(replacement);

    await vi.advanceTimersByTimeAsync(260);

    expect(container.scrollTop).toBe(600 - 64);
    expect(replacement.classList.contains("user-message-jump-flash")).toBe(true);
  });

  it("does nothing when no anchor exists and retries are exhausted", async () => {
    // No DOM at all — the helper should give up silently.
    scrollToUserMessage("missing", "missing");
    await vi.runAllTimersAsync();
    expect(document.body.innerHTML).toBe("");
  });
});

describe("anchor ID helpers", () => {
  it("turnAnchorID is stable across reloads", () => {
    expect(turnAnchorID("abc")).toBe("turn-abc");
  });

  it("userMessageAnchorID combines turn and item IDs uniquely", () => {
    expect(userMessageAnchorID("abc", "xyz")).toBe("user-msg-abc-xyz");
  });
});

/**
 * Tiny Thread builder for the discovery helpers. Only the fields the
 * helpers actually read (id, turns, items, type, text) are populated —
 * production Threads carry a lot more, but adding stub data here would
 * make the tests brittle to future schema changes.
 */
function buildThread(
  turns: Array<{
    id: string;
    items: Array<{ id: string; type: string; text?: string; name?: string }>;
  }>,
): Thread {
  return {
    id: "thread-1",
    preview: "",
    model_provider: "",
    model: "",
    cwd: "",
    status: "idle",
    created_at: "",
    updated_at: "",
    turns: turns.map((turn) => ({
      id: turn.id,
      status: "completed" as const,
      items_view: "full" as const,
      items: turn.items.map((item) => item as never),
    })),
  };
}

describe("firstUserMessageAnchor", () => {
  it("returns the first user_message anchor across multiple turns", () => {
    const thread = buildThread([
      {
        id: "turn-1",
        items: [
          { id: "u-1", type: "user_message" },
          { id: "a-1", type: "agent_message", text: "hi" },
        ],
      },
      {
        id: "turn-2",
        items: [
          { id: "u-2", type: "user_message" },
          { id: "a-2", type: "agent_message", text: "next" },
        ],
      },
    ]);

    expect(firstUserMessageAnchor(thread)).toEqual({
      turnID: "turn-1",
      itemID: "u-1",
    });
  });

  it("skips turns that start with reasoning or tool items", () => {
    const thread = buildThread([
      {
        id: "turn-compact",
        items: [{ id: "r-1", type: "reasoning" }],
      },
      {
        id: "turn-1",
        items: [{ id: "u-1", type: "user_message" }],
      },
    ]);

    expect(firstUserMessageAnchor(thread)?.itemID).toBe("u-1");
  });

  it("returns undefined when the thread has no user_message", () => {
    const thread = buildThread([
      {
        id: "turn-1",
        items: [{ id: "a-1", type: "agent_message", text: "answer" }],
      },
    ]);

    expect(firstUserMessageAnchor(thread)).toBeUndefined();
  });

  it("returns undefined for undefined input", () => {
    expect(firstUserMessageAnchor(undefined)).toBeUndefined();
  });

  it("returns the first user_message anchor when given a single Turn", () => {
    const turn = buildTurn([
      { id: "a-1", type: "agent_message", text: "ignored" },
      { id: "u-1", type: "user_message", text: "hello" },
    ]);
    expect(firstUserMessageAnchor(turn)).toEqual({
      turnID: "turn-1",
      itemID: "u-1",
    });
  });

  it("skips internal agent handoff anchors", () => {
    const turn = buildTurn([
      {
        id: "handoff",
        type: "user_message",
        name: AGENT_NOTIFICATION_NAME,
        text: handoffText(),
      },
      { id: "u-1", type: "user_message", text: "hello" },
    ]);
    expect(firstUserMessageAnchor(turn)).toEqual({
      turnID: "turn-1",
      itemID: "u-1",
    });
  });

  it("skips named process notification anchors", () => {
    const turn = buildTurn([
      {
        id: "process",
        type: "user_message",
        name: PROCESS_NOTIFICATION_NAME,
        text: "unparseable process payload",
      },
      { id: "u-1", type: "user_message", text: "hello" },
    ]);
    expect(firstUserMessageAnchor(turn)).toEqual({
      turnID: "turn-1",
      itemID: "u-1",
    });
  });

  it("returns undefined when the Turn has no user_message", () => {
    const turn = buildTurn([
      { id: "a-1", type: "agent_message", text: "answer" },
    ]);
    expect(firstUserMessageAnchor(turn)).toBeUndefined();
  });
});

describe("lastUserMessageAnchor", () => {
  it("returns the last user_message across multiple turns", () => {
    const thread = buildThread([
      {
        id: "turn-1",
        items: [
          { id: "u-1", type: "user_message" },
          { id: "a-1", type: "agent_message", text: "hi" },
        ],
      },
      {
        id: "turn-2",
        items: [
          { id: "u-2", type: "user_message" },
          { id: "a-2", type: "agent_message", text: "next" },
        ],
      },
    ]);

    expect(lastUserMessageAnchor(thread)).toEqual({
      turnID: "turn-2",
      itemID: "u-2",
    });
  });

  it("returns the last user_message inside a single turn (latest in items)", () => {
    const thread = buildThread([
      {
        id: "turn-1",
        items: [
          { id: "u-1", type: "user_message" },
          { id: "a-1", type: "agent_message", text: "first reply" },
          { id: "u-2", type: "user_message", text: "follow up" },
          { id: "a-2", type: "agent_message", text: "second reply" },
        ],
      },
    ]);

    expect(lastUserMessageAnchor(thread)).toEqual({
      turnID: "turn-1",
      itemID: "u-2",
    });
  });

  it("ignores trailing non-user items in the last turn", () => {
    // Mirrors the real renderer layout: the latest turn ends with
    // reasoning or tool items after its last user message. The picker
    // must still find the user_message, not bump against the trailing
    // tool call.
    const thread = buildThread([
      {
        id: "turn-1",
        items: [
          { id: "u-1", type: "user_message" },
          { id: "a-1", type: "agent_message", text: "first reply" },
        ],
      },
      {
        id: "turn-2",
        items: [
          { id: "u-2", type: "user_message" },
          { id: "a-2", type: "agent_message", text: "in progress" },
          { id: "r-2", type: "reasoning" },
          { id: "tc-2", type: "tool_call" },
        ],
      },
    ]);

    expect(lastUserMessageAnchor(thread)).toEqual({
      turnID: "turn-2",
      itemID: "u-2",
    });
  });

  it("returns undefined when no user_message exists", () => {
    const thread = buildThread([
      {
        id: "turn-1",
        items: [{ id: "a-1", type: "agent_message", text: "answer" }],
      },
    ]);
    expect(lastUserMessageAnchor(thread)).toBeUndefined();
  });

  it("returns undefined for undefined input", () => {
    expect(lastUserMessageAnchor(undefined)).toBeUndefined();
  });

  it("returns the last user_message when given a single Turn", () => {
    const turn = buildTurn([
      { id: "u-1", type: "user_message", text: "first" },
      { id: "a-1", type: "agent_message", text: "reply" },
      { id: "u-2", type: "user_message", text: "follow up" },
    ]);
    expect(lastUserMessageAnchor(turn)).toEqual({
      turnID: "turn-1",
      itemID: "u-2",
    });
  });

  it("skips internal agent handoff anchors", () => {
    const turn = buildTurn([
      { id: "u-1", type: "user_message", text: "hello" },
      { id: "a-1", type: "agent_message", text: "reply" },
      {
        id: "handoff",
        type: "user_message",
        name: AGENT_NOTIFICATION_NAME,
        text: handoffText(),
      },
    ]);
    expect(lastUserMessageAnchor(turn)).toEqual({
      turnID: "turn-1",
      itemID: "u-1",
    });
  });

  it("skips legacy process notification anchors", () => {
    const turn = buildTurn([
      { id: "u-1", type: "user_message", text: "hello" },
      { id: "process", type: "user_message", text: processNotificationText() },
    ]);
    expect(lastUserMessageAnchor(turn)).toEqual({
      turnID: "turn-1",
      itemID: "u-1",
    });
  });

  it("matches the snapshot the fork picker checks against", () => {
    // App.tsx's `isForkTargetLatest` compares the (turnID, itemID) the
    // user clicked against the value this helper returns, so the
    // returned shape is the load-bearing contract for the picker.
    const thread = buildThread([
      {
        id: "turn-a",
        items: [{ id: "u-old", type: "user_message", text: "earlier" }],
      },
      {
        id: "turn-b",
        items: [{ id: "u-newest", type: "user_message", text: "newest" }],
      },
    ]);

    expect(lastUserMessageAnchor(thread)).toEqual({
      turnID: "turn-b",
      itemID: "u-newest",
    });
  });
});

describe("threadReplySnippet", () => {
  it("returns the first non-empty agent message and counts replies", () => {
    const thread = buildThread([
      {
        id: "turn-1",
        items: [
          { id: "u-1", type: "user_message" },
          // First agent message is empty (still streaming) and must be skipped.
          { id: "a-1", type: "agent_message", text: "   " },
          { id: "a-2", type: "agent_message", text: "first visible reply" },
        ],
      },
      {
        id: "turn-2",
        items: [
          { id: "u-2", type: "user_message" },
          { id: "a-3", type: "agent_message", text: "second reply" },
        ],
      },
    ]);

    const snippet = threadReplySnippet(thread);
    expect(snippet).toEqual({
      text: "first visible reply",
      totalAgentMessages: 2,
    });
  });

  it("returns undefined when no agent_message has committed text", () => {
    const thread = buildThread([
      {
        id: "turn-1",
        items: [
          { id: "u-1", type: "user_message" },
          { id: "a-1", type: "agent_message", text: "" },
        ],
      },
    ]);

    expect(threadReplySnippet(thread)).toBeUndefined();
  });

  it("returns undefined for undefined input", () => {
    expect(threadReplySnippet(undefined)).toBeUndefined();
  });
});

/**
 * Build a single turn for the per-turn helper tests. Mirrors buildThread
 * but skips the thread-level wrapping so we can exercise turn-level
 * behavior (e.g. the conversation turn rail) in isolation.
 */
function buildTurn(
  items: Array<{ id: string; type: string; text?: string; name?: string }>,
): Turn {
  return {
    id: "turn-1",
    status: "completed",
    items_view: "full",
    items: items.map((item) => item as never),
  };
}

describe("firstUserMessageText", () => {
  it("returns the first user_message text in a turn", () => {
    const turn = buildTurn([
      { id: "a-1", type: "agent_message", text: "ignored" },
      { id: "u-1", type: "user_message", text: "hello" },
      { id: "a-2", type: "agent_message", text: "world" },
    ]);
    expect(firstUserMessageText(turn)).toBe("hello");
  });

  it("skips internal agent handoff text", () => {
    const turn = buildTurn([
      {
        id: "handoff",
        type: "user_message",
        name: AGENT_NOTIFICATION_NAME,
        text: handoffText(),
      },
      { id: "u-1", type: "user_message", text: "hello" },
    ]);
    expect(firstUserMessageText(turn)).toBe("hello");
  });

  it("skips process notifications in turn previews", () => {
    const turn = buildTurn([
      {
        id: "process-named",
        type: "user_message",
        name: PROCESS_NOTIFICATION_NAME,
        text: "unparseable process payload",
      },
      { id: "process-legacy", type: "user_message", text: processNotificationText() },
      { id: "u-1", type: "user_message", text: "hello" },
    ]);
    expect(firstUserMessageText(turn)).toBe("hello");
  });

  it("returns undefined when no user_message exists", () => {
    const turn = buildTurn([
      { id: "a-1", type: "agent_message", text: "answer" },
    ]);
    expect(firstUserMessageText(turn)).toBeUndefined();
  });

  it("returns undefined for undefined input", () => {
    expect(firstUserMessageText(undefined)).toBeUndefined();
  });
});

describe("turnReplySnippet", () => {
  it("returns the first non-empty agent message and counts replies in one turn", () => {
    const turn = buildTurn([
      { id: "u-1", type: "user_message" },
      // First agent message is empty (still streaming) and must be skipped.
      { id: "a-1", type: "agent_message", text: "   " },
      { id: "a-2", type: "agent_message", text: "first visible reply" },
      { id: "a-3", type: "agent_message", text: "second reply" },
    ]);

    const snippet = turnReplySnippet(turn);
    expect(snippet).toEqual({
      text: "first visible reply",
      totalAgentMessages: 2,
    });
  });

  it("returns undefined when no agent_message has committed text", () => {
    const turn = buildTurn([
      { id: "u-1", type: "user_message" },
      { id: "a-1", type: "agent_message", text: "" },
    ]);

    expect(turnReplySnippet(turn)).toBeUndefined();
  });

  it("returns undefined for undefined input", () => {
    expect(turnReplySnippet(undefined)).toBeUndefined();
  });
});

describe("truncateReplyPreview", () => {
  it("returns short text untouched", () => {
    expect(truncateReplyPreview("短回复")).toBe("短回复");
  });

  it("collapses whitespace before measuring", () => {
    const text = "a\n\nb\t\tc";
    expect(truncateReplyPreview(text)).toBe("a b c");
  });

  it("truncates overlong replies with an ellipsis", () => {
    const text = "x".repeat(200);
    const out = truncateReplyPreview(text);
    expect(out.endsWith("…")).toBe(true);
    expect(out.length).toBeLessThanOrEqual(140);
  });
});
