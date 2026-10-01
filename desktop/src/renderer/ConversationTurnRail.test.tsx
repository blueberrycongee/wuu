import { act, type RefObject } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import type { ThreadItem, Turn } from "../shared/protocol";
import {
  conversationTurnRailCapacityForHeight,
  conversationTurnRailWindow,
  ConversationTurnRail,
} from "./ConversationTurnRail";
import type { QueryHistoryEntry } from "./QueryHistoryPopover";

let container: HTMLDivElement | undefined;
let root: Root | undefined;

function userMessage(id: string, text: string): ThreadItem {
  return {
    id,
    type: "user_message",
    status: "completed",
    text,
  };
}

function agentMessage(id: string): ThreadItem {
  return {
    id,
    type: "agent_message",
    status: "completed",
    terminal: true,
    text: "reply",
  };
}

function turn(id: string, query: string): Turn {
  return {
    id,
    items_view: "full",
    status: "completed",
    items: [userMessage(`user-${id}`, query), agentMessage(`agent-${id}`)],
  };
}

function railPointerEvent(
  type: string,
  init: MouseEventInit & { pointerId?: number } = {},
): PointerEvent {
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    ...init,
  }) as PointerEvent;
  Object.defineProperty(event, "pointerId", {
    configurable: true,
    value: init.pointerId ?? 1,
  });
  return event;
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

function stubScrollMetrics(
  node: HTMLElement,
  layout: { scrollHeight: number; clientHeight: number; scrollTop: number },
): void {
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
    set: (value: number) => {
      layout.scrollTop = value;
    },
  });
}

function stubRect(node: HTMLElement, top: number, height: number): void {
  node.getBoundingClientRect = () =>
    ({
      x: 0,
      y: top,
      top,
      right: 800,
      bottom: top + height,
      left: 0,
      width: 800,
      height,
      toJSON: () => ({}),
    }) as DOMRect;
}

function renderRail({
  turns,
  activeTurnID,
  scrollContainerRef,
  getScrollContainer,
  onUserScroll,
  onSelectQueryHistory,
}: {
  turns?: Turn[];
  activeTurnID?: string;
  scrollContainerRef?: RefObject<HTMLElement | null>;
  getScrollContainer?: () => HTMLElement | null;
  onUserScroll?: (direction: "away" | "latest") => void;
  onSelectQueryHistory: (entry: QueryHistoryEntry) => void;
}): void {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root?.render(
      <ConversationTurnRail
        turns={turns ?? [turn("turn-1", "first query"), turn("turn-2", "second query")]}
        activeTurnID={activeTurnID}
        scrollContainerRef={scrollContainerRef}
        getScrollContainer={getScrollContainer}
        onUserScroll={onUserScroll}
        onSelectQueryHistory={onSelectQueryHistory}
      />,
    );
  });
}

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  container?.remove();
  root = undefined;
  container = undefined;
});

describe("ConversationTurnRail", () => {
  it("routes bar clicks through query-history selection", () => {
    let selected: QueryHistoryEntry | undefined;
    renderRail({
      onSelectQueryHistory: (entry) => {
        selected = entry;
      },
    });

    const bars = container?.querySelectorAll<HTMLElement>(
      ".conversation-turn-rail-bar[role='button']",
    );
    expect(bars).toHaveLength(2);

    act(() => {
      bars?.[1]?.click();
    });

    expect(selected).toEqual({
      turnID: "turn-2",
      itemID: "user-turn-2",
      text: "second query",
    });
  });

  it("keeps the hover preview below the titlebar", () => {
    const titlebar = document.createElement("header");
    titlebar.className = "titlebar";
    document.body.appendChild(titlebar);
    stubRect(titlebar, 0, 94);

    try {
      renderRail({
        turns: [turn("turn-1", "first query")],
        onSelectQueryHistory: () => {},
      });

      const rail = container?.querySelector<HTMLElement>(".conversation-turn-rail");
      const bar = container?.querySelector<HTMLElement>(".conversation-turn-rail-bar");
      expect(rail).toBeTruthy();
      expect(bar).toBeTruthy();
      stubRect(bar!, 500, 5);

      act(() => {
        rail?.dispatchEvent(
          new MouseEvent("mousemove", {
            bubbles: true,
            clientY: 502,
          }),
        );
      });

      const preview = container?.querySelector<HTMLElement>(
        ".conversation-turn-rail-preview",
      );
      expect(preview?.style.maxHeight).toBe("390px");

      stubRect(bar!, 400, 5);
      act(() => window.dispatchEvent(new Event("resize")));
      expect(preview?.style.maxHeight).toBe("290px");
    } finally {
      titlebar.remove();
    }
  });

  it("does not cancel pointerdown before a normal bar click can fire", () => {
    const scrollNode = document.createElement("div");
    renderRail({
      scrollContainerRef: { current: scrollNode },
      onSelectQueryHistory: () => {},
    });

    const bar = container?.querySelector<HTMLElement>(
      ".conversation-turn-rail-bar[role='button']",
    );
    const event = railPointerEvent("pointerdown", { clientY: 10 });
    act(() => {
      bar?.dispatchEvent(event);
    });

    expect(event.defaultPrevented).toBe(false);
  });

  it("stays hidden for an empty conversation", () => {
    renderRail({
      turns: [],
      onSelectQueryHistory: () => {},
    });

    expect(container?.querySelector(".conversation-turn-rail")).toBeNull();
    expect(container?.querySelector(".conversation-turn-rail-bar")).toBeNull();
  });

  it("caps many turns to the latest visible window by default", () => {
    renderRail({
      turns: Array.from({ length: 60 }, (_, index) =>
        turn(`turn-${index + 1}`, `query ${index + 1}`),
      ),
      onSelectQueryHistory: () => {},
    });

    const bars = container?.querySelectorAll<HTMLElement>(
      ".conversation-turn-rail-bar[role='button']",
    );
    expect(bars).toHaveLength(48);
    expect(bars?.[0]?.getAttribute("aria-label")).toBe(
      "跳转到第 13 轮对话",
    );
    expect(bars?.[47]?.getAttribute("aria-label")).toBe(
      "跳转到第 60 轮对话",
    );
  });

  it("derives a smaller visible window when the rail container is short", () => {
    expect(conversationTurnRailCapacityForHeight(474)).toBe(48);
    expect(conversationTurnRailCapacityForHeight(320)).toBe(30);
    expect(conversationTurnRailCapacityForHeight(0)).toBeUndefined();
  });

  it("scrolls the conversation when the user wheels over the rail", () => {
    const scrollNode = document.createElement("div");
    scrollNode.scrollTop = 120;
    let scrollAwayIntentCount = 0;
    renderRail({
      scrollContainerRef: { current: scrollNode },
      onUserScroll: () => {
        scrollAwayIntentCount += 1;
      },
      onSelectQueryHistory: () => {},
    });

    const rail = container?.querySelector<HTMLElement>(".conversation-turn-rail");
    const event = new WheelEvent("wheel", {
      bubbles: true,
      cancelable: true,
      deltaY: -80,
    });
    act(() => {
      rail?.dispatchEvent(event);
    });

    expect(scrollNode.scrollTop).toBe(40);
    expect(scrollAwayIntentCount).toBe(1);
    expect(event.defaultPrevented).toBe(true);
  });

  it("prefers the resolved active scroll container over the wrapper ref", () => {
    const wrapperNode = document.createElement("div");
    wrapperNode.scrollTop = 10;
    const activePaneNode = document.createElement("section");
    activePaneNode.scrollTop = 120;
    renderRail({
      scrollContainerRef: { current: wrapperNode },
      getScrollContainer: () => activePaneNode,
      onSelectQueryHistory: () => {},
    });

    const rail = container?.querySelector<HTMLElement>(".conversation-turn-rail");
    act(() => {
      rail?.dispatchEvent(
        new WheelEvent("wheel", {
          bubbles: true,
          cancelable: true,
          deltaY: -80,
        }),
      );
    });

    expect(activePaneNode.scrollTop).toBe(40);
    expect(wrapperNode.scrollTop).toBe(10);
  });

  it("marks the turn visible in the scroll viewport as active", async () => {
    await withManualAnimationFrames(async (flush) => {
      const scrollNode = document.createElement("div");
      stubScrollMetrics(scrollNode, {
        scrollHeight: 1200,
        clientHeight: 400,
        scrollTop: 120,
      });
      stubRect(scrollNode, 0, 400);

      const firstTurn = document.createElement("div");
      firstTurn.className = "turn";
      firstTurn.dataset.turnId = "turn-1";
      stubRect(firstTurn, 80, 260);

      const secondTurn = document.createElement("div");
      secondTurn.className = "turn";
      secondTurn.dataset.turnId = "turn-2";
      stubRect(secondTurn, 360, 260);

      scrollNode.append(firstTurn, secondTurn);
      renderRail({
        activeTurnID: "turn-2",
        getScrollContainer: () => scrollNode,
        onSelectQueryHistory: () => {},
      });

      await flush();

      const activeBar = container?.querySelector<HTMLElement>(
        ".conversation-turn-rail-bar.active",
      );
      expect(activeBar?.dataset.turnId).toBe("turn-1");
      expect(activeBar?.getAttribute("aria-current")).toBe("location");
    });
  });

  it("centers the capped window around the focused turn", () => {
    const turns = Array.from({ length: 20 }, (_, index) =>
      turn(`turn-${index + 1}`, `query ${index + 1}`),
    );

    expect(
      conversationTurnRailWindow(turns, "turn-2", 5).turns.map(
        (entry) => entry.id,
      ),
    ).toEqual(["turn-1", "turn-2", "turn-3", "turn-4", "turn-5"]);
    expect(
      conversationTurnRailWindow(turns, "turn-10", 5).turns.map(
        (entry) => entry.id,
      ),
    ).toEqual(["turn-8", "turn-9", "turn-10", "turn-11", "turn-12"]);
  });
});
