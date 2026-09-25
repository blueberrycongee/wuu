import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Turn } from "../shared/protocol";
import {
  ConversationTurnList,
  TURN_LIST_COLLAPSE_THRESHOLD,
  TURN_LIST_INITIAL_TAIL_TURNS,
  TURN_LIST_PREPEND_BATCH_TURNS,
  TURN_LIST_RECENT_FULL_TURNS,
} from "./ConversationTurnList";
import {
  requestConversationTurnReveal,
  userMessageAnchorID,
} from "./TurnViewHelpers";
import { ConversationRenderActivityProvider } from "./ConversationRenderActivity";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
});

function render(element: JSX.Element): void {
  act(() => {
    if (!root) {
      root = createRoot(container);
    }
    root.render(element);
  });
}

function makeTurn(index: number, status: Turn["status"] = "completed"): Turn {
  return {
    id: `turn-${index}`,
    items_view: "full",
    status,
    items: [
      {
        id: `user-${index}`,
        type: "user_message",
        text: `User question ${index}`,
      },
      {
        id: `agent-${index}`,
        type: "agent_message",
        status: status === "in_progress" ? "in_progress" : "completed",
        terminal: true,
        text: `Agent answer ${index}`,
      },
    ],
  };
}

function turnList(
  turns: Turn[],
  forcedFullTurnIDs?: string[],
  autoLoadEarlier = true,
): JSX.Element {
  return (
    <ConversationTurnList
      threadID="thread-1"
      turns={turns}
      forcedFullTurnIDs={forcedFullTurnIDs}
      autoLoadEarlier={autoLoadEarlier}
      renderTurn={(turn) => (
        <section
          className="turn"
          data-testid="full-turn"
          data-turn-id={turn.id}
        >
          {turn.id}
        </section>
      )}
    />
  );
}

function mountTurns(turns: Turn[], forcedFullTurnIDs?: string[]): void {
  render(
    turnList(turns, forcedFullTurnIDs),
  );
}

describe("ConversationTurnList", () => {
  it("renders all turns fully below the collapse threshold", () => {
    const turns = Array.from(
      { length: TURN_LIST_COLLAPSE_THRESHOLD },
      (_, index) => makeTurn(index),
    );

    mountTurns(turns);

    expect(container.querySelectorAll('[data-testid="full-turn"]')).toHaveLength(
      turns.length,
    );
    expect(container.querySelector(".turn-collapsed")).toBeNull();
  });

  it("keeps an already-mounted short conversation intact as turns append", () => {
    const initialTurns = [makeTurn(0)];
    render(turnList(initialTurns));
    const initialTurn = container.querySelector('[data-turn-id="turn-0"]');

    render(turnList([...initialTurns, makeTurn(1)]));

    expect(container.querySelectorAll(".turn")).toHaveLength(2);
    expect(container.querySelector('[data-turn-id="turn-0"]')).toBe(initialTurn);
  });

  it("cold-mounts only the recent tail of a long conversation", () => {
    const turns = Array.from(
      { length: TURN_LIST_COLLAPSE_THRESHOLD + 10 },
      (_, index) => makeTurn(index),
    );

    mountTurns(turns);

    expect(container.querySelectorAll('[data-testid="full-turn"]')).toHaveLength(
      TURN_LIST_INITIAL_TAIL_TURNS,
    );
    expect(container.querySelectorAll(".turn")).toHaveLength(
      TURN_LIST_INITIAL_TAIL_TURNS,
    );
    expect(container.querySelectorAll(".turn-collapsed")).toHaveLength(0);
    expect(
      container.querySelector(
        `#${userMessageAnchorID("turn-0", "user-0")}`,
      ),
    ).toBeNull();
    expect(container.querySelector(".conversation-turn-history-loader")).not.toBeNull();
  });

  it.each([TURN_LIST_COLLAPSE_THRESHOLD, TURN_LIST_COLLAPSE_THRESHOLD + 10])(
    "retains mounted replies when a queued turn appends to %s turns", count => {
      const turns = Array.from({ length: count }, (_, index) => makeTurn(index));
      render(turnList(turns));
      const before = [...container.querySelectorAll('[data-testid="full-turn"]')];
      render(turnList([...turns, makeTurn(count)]));
      for (const node of before) {
        expect(container.querySelector(`[data-testid="full-turn"][data-turn-id="${node.getAttribute("data-turn-id")}"]`)).toBe(node);
      }
      expect(container.querySelectorAll('[data-testid="full-turn"]')).toHaveLength(before.length + 1);
    },
  );

  it("loads earlier turns in bounded batches and expands a loaded turn", () => {
    const turns = Array.from(
      { length: TURN_LIST_COLLAPSE_THRESHOLD + 10 },
      (_, index) => makeTurn(index),
    );
    mountTurns(turns);

    const historyLoader = container.querySelector<HTMLButtonElement>(
      ".conversation-turn-history-loader",
    );
    expect(historyLoader).not.toBeNull();

    act(() => {
      historyLoader?.click();
    });

    expect(container.querySelectorAll(".turn")).toHaveLength(
      TURN_LIST_INITIAL_TAIL_TURNS + TURN_LIST_PREPEND_BATCH_TURNS,
    );
    expect(container.querySelectorAll(".turn-collapsed")).toHaveLength(
      TURN_LIST_PREPEND_BATCH_TURNS,
    );
    const firstCollapsed = container.querySelector<HTMLButtonElement>(
      ".turn-collapsed-button",
    );
    expect(firstCollapsed).not.toBeNull();

    act(() => {
      firstCollapsed?.click();
    });

    expect(
      container.querySelector('[data-testid="full-turn"][data-turn-id="turn-10"]'),
    ).not.toBeNull();
  });

  it("preserves the viewport offset when an earlier batch is prepended", () => {
    const turns = Array.from(
      { length: TURN_LIST_COLLAPSE_THRESHOLD + 10 },
      (_, index) => makeTurn(index),
    );
    const scrollNode = document.createElement("div");
    scrollNode.className = "scroll-region";
    container.appendChild(scrollNode);
    Object.defineProperty(scrollNode, "scrollHeight", {
      configurable: true,
      get: () => scrollNode.querySelectorAll(".turn").length * 10,
    });
    scrollNode.scrollTop = 120;

    act(() => {
      if (!root) {
        root = createRoot(scrollNode);
      }
      root.render(turnList(turns, undefined, false));
    });
    act(() => {
      scrollNode
        .querySelector<HTMLButtonElement>(".conversation-turn-history-loader")
        ?.click();
    });

    expect(scrollNode.scrollTop).toBe(520);
  });

  it("loads one earlier batch only after the user scrolls near the top", () => {
    const turns = Array.from(
      { length: TURN_LIST_COLLAPSE_THRESHOLD + 50 },
      (_, index) => makeTurn(index),
    );
    const scrollNode = document.createElement("div");
    scrollNode.className = "scroll-region";
    container.appendChild(scrollNode);

    act(() => {
      if (!root) {
        root = createRoot(scrollNode);
      }
      root.render(turnList(turns));
    });

    expect(scrollNode.querySelectorAll(".turn")).toHaveLength(
      TURN_LIST_INITIAL_TAIL_TURNS,
    );

    scrollNode.scrollTop = 0;
    act(() => scrollNode.dispatchEvent(new Event("scroll")));

    expect(scrollNode.querySelectorAll(".turn")).toHaveLength(
      TURN_LIST_INITIAL_TAIL_TURNS + TURN_LIST_PREPEND_BATCH_TURNS,
    );
  });

  it.each([0, 200, 500])("anchors a prepend during a gesture and tail growth (native adjustment: %s)", async (nativeAdjustment) => {
    const prior = window.wuu;
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let calls = 0;
    const turns = [makeTurn(40), makeTurn(41)];
    let tailGrowth = 0;
    let appliedNativeAdjustment = false;
    const view = (historyCursor?: string) => (
      <ConversationTurnList
        threadID="remote"
        historyCursor={historyCursor}
        turns={turns}
        renderTurn={(turn) => <div data-turn-id={turn.id} ref={node => {
          if (!node) return;
          node.getBoundingClientRect = () => {
            const index = [...container.querySelectorAll('[data-turn-id]')].indexOf(node);
            const top = index * 500 - container.scrollTop;
            return { top, bottom: top + 500, height: 500 } as DOMRect;
          };
          if (turn.id === "turn-39" && !appliedNativeAdjustment) {
            appliedNativeAdjustment = true;
            container.scrollTop += nativeAdjustment;
          }
        }}>{turn.id}</div>}
      />
    );
    window.wuu = {
      ...prior,
      loadEarlierThreadHistory: async () => {
        calls += 1;
        await gate;
        turns.unshift(makeTurn(39));
        tailGrowth = 900;
        root!.render(view());
      },
    };
    container.className = "scroll-region";
    Object.defineProperty(container, "scrollHeight", {
      configurable: true,
      get: () => container.querySelectorAll("[data-turn-id]").length * 500 + tailGrowth,
    });
    Object.defineProperty(container, "clientHeight", { configurable: true, value: 300 });
    try {
      render(view("cursor"));
      container.scrollTop = 120;
      await act(async () => {
        container.dispatchEvent(new Event("scroll"));
      });
      expect(calls).toBe(1);

      // The user continues the gesture while the request is pending.
      container.scrollTop = 320;

      await act(async () => {
        release();
      });
      expect(container.scrollTop).toBe(820);
      expect(container.querySelector('[data-turn-id="turn-40"]')!.getBoundingClientRect().top).toBe(-320);
    } finally {
      window.wuu = prior;
    }
  });

  it("does not move the shared viewport when a hidden pane receives history", () => {
    container.className = "scroll-region";
    const prior = window.wuu;
    window.wuu = { ...prior, loadEarlierThreadHistory: async () => {} };
    Object.defineProperty(container, "scrollHeight", {
      configurable: true,
      get: () => container.querySelectorAll('[data-turn-id]').length * 500,
    });
    const view = (turns: Turn[], active: boolean) => <ConversationRenderActivityProvider active={active}>
      <ConversationTurnList threadID="hidden" historyCursor="cursor" turns={turns}
        renderTurn={turn => <div data-turn-id={turn.id} />} />
    </ConversationRenderActivityProvider>;
    try {
      render(view([makeTurn(40)], true));
      render(view([makeTurn(40)], false));
      container.scrollTop = 120;
      render(view([makeTurn(39), makeTurn(40)], false));
      expect(container.scrollTop).toBe(120);
    } finally {
      window.wuu = prior;
    }
  });

  it("reveals an unloaded turn before an anchor jump retries", () => {
    const turns = Array.from(
      { length: TURN_LIST_COLLAPSE_THRESHOLD + 10 },
      (_, index) => makeTurn(index),
    );
    mountTurns(turns);

    expect(container.querySelector('[data-turn-id="turn-0"]')).toBeNull();
    act(() => requestConversationTurnReveal("turn-0"));

    expect(container.querySelector('[data-turn-id="turn-0"]')).not.toBeNull();
    expect(
      container.querySelector(`#${userMessageAnchorID("turn-0", "user-0")}`),
    ).not.toBeNull();
  });

  it("always full-renders in-progress and forced turns", () => {
    const turns = Array.from(
      { length: TURN_LIST_COLLAPSE_THRESHOLD + 10 },
      (_, index) =>
        index === 2 ? makeTurn(index, "in_progress") : makeTurn(index),
    );

    mountTurns(turns, ["turn-3"]);

    expect(
      container.querySelector('[data-testid="full-turn"][data-turn-id="turn-2"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[data-testid="full-turn"][data-turn-id="turn-3"]'),
    ).not.toBeNull();
  });
});

it("retries failed remote history on a later scroll without overlapping requests", async () => {
  const prior = window.wuu;
  let calls = 0;
  let rejectPage!: (error: Error) => void;
  const pendingPage = new Promise<void>((_resolve, reject) => { rejectPage = reject; });
  const turns = [makeTurn(40)];
  const view = (historyCursor?: string) => (
    <ConversationTurnList
      threadID="remote"
      historyCursor={historyCursor}
      turns={turns}
      renderTurn={turn => <div data-turn-id={turn.id}>{turn.id}</div>}
    />
  );
  window.wuu = {
    ...prior,
    loadEarlierThreadHistory: async () => {
      calls++;
      if (calls === 1) return pendingPage;
      turns.unshift(makeTurn(39));
      root!.render(view());
    },
  };
  container.className = "scroll-region";
  try {
    render(view("cursor"));
    expect(calls).toBe(0);
    act(() => {
      container.dispatchEvent(new Event("scroll"));
      container.dispatchEvent(new Event("scroll"));
    });
    expect(calls).toBe(1);
    act(() => container.dispatchEvent(new Event("scroll")));
    expect(calls).toBe(1);

    await act(async () => rejectPage(new Error("offline")));
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
    expect(calls).toBe(1);

    await act(async () => container.dispatchEvent(new Event("scroll")));
    expect(calls).toBe(2);
    expect(container.querySelector('[data-turn-id="turn-39"]')).not.toBeNull();
    expect(container.querySelector('[role="alert"]')).toBeNull();
    expect(container.querySelector('.conversation-turn-history-loader')).toBeNull();
  } finally {
    window.wuu = prior;
  }
});

it("requests older remote history on demand and keeps a failed page retryable", async () => {
  const prior = window.wuu;
  let calls = 0;
  window.wuu = { ...prior, loadEarlierThreadHistory: async () => { calls++; if (calls === 1) throw new Error("offline"); } };
  try {
    render(<ConversationTurnList threadID="remote" historyCursor="cursor" turns={[makeTurn(40)]} renderTurn={turn => <div>{turn.id}</div>} />);
    expect(calls).toBe(0);
    await act(async () => container.querySelector<HTMLButtonElement>(".conversation-turn-history-loader")!.click());
    expect(container.querySelector('[role="alert"]')?.textContent).toBe("offline");
    await act(async () => container.querySelector<HTMLButtonElement>(".conversation-turn-history-loader")!.click());
    expect(calls).toBe(2);
  } finally { window.wuu = prior; }
});
