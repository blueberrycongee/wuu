/**
 * Render-bailout guarantees for the conversation turn tree.
 *
 * Server events (item/started, item/completed, turn/completed) rebuild the
 * thread object but preserve the identity of every turn they did not touch.
 * PaneTurnView (in CachedConversationPanes) and CollapsedTurnView (in
 * ConversationTurnList) are memoized on that identity, so an event targeting
 * one turn must not re-render the subtrees of the others. These tests pin
 * that contract: without it, every tool-call completion in a long session
 * reconciles the entire conversation (measured ~110ms at 2000 turns).
 */
import { act, createElement, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Thread, Turn } from "../shared/protocol";
import { CachedConversationPanes } from "./CachedConversationPanes";
import { ImagePreviewProvider } from "./ImagePreview";

const turnViewRenders = vi.hoisted(() => new Map<string, number>());
const replySnippetCalls = vi.hoisted(() => ({ count: 0 }));

vi.mock("./TurnView", () => ({
  TurnView: ({ turn }: { turn: Turn }): JSX.Element => {
    turnViewRenders.set(turn.id, (turnViewRenders.get(turn.id) ?? 0) + 1);
    return <section data-turn-id={turn.id} data-testid={`full-${turn.id}`} />;
  },
  latestAgentMessageItemID: (): undefined => undefined,
}));

vi.mock("./TurnViewHelpers", async (importOriginal) => {
  const original = await importOriginal<typeof import("./TurnViewHelpers")>();
  return {
    ...original,
    turnReplySnippet: (turn: Turn) => {
      replySnippetCalls.count += 1;
      return original.turnReplySnippet(turn);
    },
  };
});

let roots: Root[] = [];

afterEach(() => {
  for (const root of roots) {
    act(() => root.unmount());
  }
  roots = [];
  turnViewRenders.clear();
  replySnippetCalls.count = 0;
  document.body.innerHTML = "";
});

function makeTurn(index: number, status: Turn["status"] = "completed"): Turn {
  return {
    id: `turn-${index}`,
    status,
    items_view: "full",
    items: [
      {
        id: `turn-${index}-user`,
        type: "user_message",
        text: `question ${index}`,
      },
      {
        id: `turn-${index}-agent`,
        type: "agent_message",
        text: `answer ${index}`,
        status: "completed",
      },
    ],
  } as Turn;
}

function threadWithTurns(id: string, turns: Turn[]): Thread {
  return {
    id,
    preview: id,
    title: id,
    model_provider: "test",
    model: "test",
    cwd: "/tmp/wuu",
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns,
  } as Thread;
}

// Stable callback identities, mirroring the App's useStableCallback wiring:
// PaneTurnView's memo only bails out when every callback prop keeps its
// identity across App re-renders, which the real App guarantees. Recreating
// them per render in this harness would measure the test, not the component.
const stableCallbacks = {
  onStreamFrame: () => {},
  onCollapseComplete: () => {},
  onDismissContextComposition: () => {},
  onDismissInstructions: () => {},
  canEditThreadMessage: () => false,
  onForkMessage: () => {},
  onOpenAgent: () => {},
  onEditMessage: () => {},
  onCancelEditMessage: () => {},
  onSubmitEditMessage: () => {},
  onOpenFileDiff: () => {},
};

const stableCollections = {
  contextCompositionEntries: [],
  instructionFilesEntries: [],
  turnStreamStatus: {},
};

function paneProps(thread: Thread): ComponentProps<typeof CachedConversationPanes> {
  return {
    threadIDs: [thread.id],
    threadsByID: new Map([[thread.id, thread]]),
    activeThreadID: thread.id,
    ...stableCallbacks,
    ...stableCollections,
  };
}

function mountPanes(thread: Thread): {
  container: HTMLElement;
  rerender: (next: Thread) => void;
} {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  roots.push(root);
  const renderWith = (current: Thread): JSX.Element => (
    <ImagePreviewProvider>
      <CachedConversationPanes {...paneProps(current)} />
    </ImagePreviewProvider>
  );
  act(() => {
    root.render(renderWith(thread));
  });
  return {
    container,
    rerender: (next: Thread) => {
      act(() => {
        root.render(renderWith(next));
      });
    },
  };
}

describe("conversation turn memoization", () => {
  it("re-renders only the turn a server event touched", () => {
    const turns = [makeTurn(1), makeTurn(2), makeTurn(3)];
    const thread = threadWithTurns("thread-memo", turns);
    const { rerender } = mountPanes(thread);

    expect(turnViewRenders.get("turn-1")).toBe(1);
    expect(turnViewRenders.get("turn-2")).toBe(1);
    expect(turnViewRenders.get("turn-3")).toBe(1);

    // Simulate item/completed landing on turn-3: new thread + new turn-3
    // object, turn-1/turn-2 referentially untouched (upsertTurnItem shape).
    const updated = threadWithTurns("thread-memo", [
      turns[0],
      turns[1],
      {
        ...turns[2],
        items: [
          ...turns[2].items,
          {
            id: "turn-3-tool",
            type: "tool_call",
            name: "bash",
            status: "completed",
          },
        ],
      } as Turn,
    ]);
    rerender(updated);

    expect(turnViewRenders.get("turn-1")).toBe(1);
    expect(turnViewRenders.get("turn-2")).toBe(1);
    expect(turnViewRenders.get("turn-3")).toBe(2);
  });

  it("unmounted history stays out of render and snippet work on events", () => {
    // 90 turns: beyond TURN_LIST_COLLAPSE_THRESHOLD (80), so only the newest
    // 40 mount on the cold path.
    const turns = Array.from({ length: 90 }, (_, index) => makeTurn(index + 1));
    const thread = threadWithTurns("thread-collapsed", turns);
    const { container, rerender } = mountPanes(thread);

    const collapsedCount = () =>
      container.querySelectorAll(".turn-collapsed").length;
    expect(collapsedCount()).toBe(0);
    expect(container.querySelectorAll('[data-testid^="full-"]')).toHaveLength(40);
    const snippetsAfterMount = replySnippetCalls.count;
    expect(snippetsAfterMount).toBe(0);
    expect(turnViewRenders.get("turn-90")).toBe(1);

    const updated = threadWithTurns("thread-collapsed", [
      ...turns.slice(0, 89),
      {
        ...turns[89],
        items: [
          ...turns[89].items,
          {
            id: "turn-90-tool",
            type: "tool_call",
            name: "read_file",
            status: "completed",
          },
        ],
      } as Turn,
    ]);
    rerender(updated);

    // Only the touched full turn re-renders; unmounted history does no work.
    expect(turnViewRenders.get("turn-90")).toBe(2);
    expect(turnViewRenders.get("turn-89")).toBe(1);
    expect(replySnippetCalls.count).toBe(snippetsAfterMount);
    expect(collapsedCount()).toBe(0);
  });

  it("still expands a loaded historical turn after memoization", () => {
    const turns = Array.from({ length: 90 }, (_, index) => makeTurn(index + 1));
    const thread = threadWithTurns("thread-expand", turns);
    const { container } = mountPanes(thread);

    expect(turnViewRenders.get("turn-1")).toBeUndefined();
    act(() => {
      container
        .querySelector<HTMLButtonElement>(".conversation-turn-history-loader")
        ?.click();
    });
    const expandButton = container.querySelector<HTMLButtonElement>(
      '[data-turn-id="turn-11"] .turn-collapsed-button',
    );
    expect(expandButton).not.toBeNull();
    act(() => {
      expandButton!.click();
    });
    expect(turnViewRenders.get("turn-11")).toBe(1);
  });
});
