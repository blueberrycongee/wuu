import { ConversationControlsTestProvider } from "./ConversationControlsTestProvider";
import { act, createElement, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Thread } from "../shared/protocol";
import type { TurnStreamStatus } from "./AppState";
import { CachedConversationPanes } from "./CachedConversationPanes";
import { ImagePreviewProvider } from "./ImagePreview";

const turnListRenders = vi.hoisted(() => new Map<string, number>());

vi.mock("./ConversationTurnList", () => ({
  ConversationTurnList: ({ threadID }: { threadID: string }): JSX.Element => {
    turnListRenders.set(threadID, (turnListRenders.get(threadID) ?? 0) + 1);
    return <div data-testid={`turn-list-${threadID}`} />;
  },
}));

let roots: Root[] = [];

afterEach(() => {
  for (const root of roots) {
    act(() => root.unmount());
  }
  vi.restoreAllMocks();
  roots = [];
  turnListRenders.clear();
  document.body.innerHTML = "";
});

function chatThread(
  id: string,
  overrides: Partial<Thread>,
): Thread {
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
    turns: [
      {
        id: "turn-1",
        status: "completed",
        items_view: "full",
        items: [
          {
            id: "human-1",
            type: "user_message",
            text: "把这个提案收敛一下",
          },
        ],
      },
    ],
    ...overrides,
  } as Thread;
}

function renderPane(
  thread: Thread,
  turnStreamStatus: Record<string, TurnStreamStatus> = {},
): { container: HTMLElement } {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  roots.push(root);
  const props: ComponentProps<typeof CachedConversationPanes> = {
    threadIDs: [thread.id],
    threadsByID: new Map([[thread.id, thread]]),
    activeThreadID: thread.id,
    contextCompositionEntries: [],
    instructionFilesEntries: [],
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
    turnStreamStatus,
  };
  act(() => {
    root.render(
      <ConversationControlsTestProvider>{createElement(
        ConversationControlsTestProvider,
        null,
        createElement(CachedConversationPanes, props),
      )}</ConversationControlsTestProvider>,
    );
  });
  return { container };
}

describe("CachedConversationPanes session switching", () => {
  it("shows a newly mounted conversation without layout polling", () => {
    const requestAnimationFrame = vi.spyOn(window, "requestAnimationFrame");
    const scrollHeight = vi.spyOn(HTMLElement.prototype, "scrollHeight", "get");
    const { container } = renderPane(chatThread("thread-a", {}));
    const pane = container.querySelector<HTMLElement>(".cached-conversation-pane");

    expect(pane?.getAttribute("data-active")).toBe("true");
    expect(pane?.hasAttribute("inert")).toBe(false);
    expect(requestAnimationFrame).not.toHaveBeenCalled();
    expect(scrollHeight).not.toHaveBeenCalled();
  });

  it("does not re-render an unrelated hidden conversation", () => {
    const threads = [
      chatThread("thread-a", {}),
      chatThread("thread-b", {}),
      chatThread("thread-c", {}),
    ];
    const threadsByID = new Map(threads.map((thread) => [thread.id, thread]));
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    roots.push(root);
    const stableProps = {
      threadIDs: threads.map((thread) => thread.id),
      threadsByID,
      contextCompositionEntries: [],
      instructionFilesEntries: [],
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
      turnStreamStatus: {},
    } satisfies Omit<
      ComponentProps<typeof CachedConversationPanes>,
      "activeThreadID"
    >;

    act(() => {
      root.render(
        <ConversationControlsTestProvider><ImagePreviewProvider>
          <CachedConversationPanes {...stableProps} activeThreadID="thread-a" />
        </ImagePreviewProvider></ConversationControlsTestProvider>,
      );
    });

    expect(turnListRenders.get("thread-a")).toBe(1);
    expect(turnListRenders.get("thread-b")).toBe(1);
    expect(turnListRenders.get("thread-c")).toBe(1);

    act(() => {
      root.render(
        <ConversationControlsTestProvider><ImagePreviewProvider>
          <CachedConversationPanes {...stableProps} activeThreadID="thread-b" />
        </ImagePreviewProvider></ConversationControlsTestProvider>,
      );
    });

    expect(turnListRenders.get("thread-a")).toBe(2);
    expect(turnListRenders.get("thread-b")).toBe(2);
    expect(turnListRenders.get("thread-c")).toBe(1);
  });

  it("defers hidden thread updates until the conversation becomes active", () => {
    const threadA = chatThread("thread-a", {});
    const threadB = chatThread("thread-b", {});
    let threadsByID = new Map([
      [threadA.id, threadA],
      [threadB.id, threadB],
    ]);
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    roots.push(root);
    const stableProps = {
      threadIDs: [threadA.id, threadB.id],
      contextCompositionEntries: [],
      instructionFilesEntries: [],
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
      turnStreamStatus: {},
    } satisfies Omit<
      ComponentProps<typeof CachedConversationPanes>,
      "activeThreadID" | "threadsByID"
    >;
    const renderActiveThread = (activeThreadID: string): void => {
      act(() => {
        root.render(
          <ConversationControlsTestProvider><ImagePreviewProvider>
            <CachedConversationPanes
              {...stableProps}
              activeThreadID={activeThreadID}
              threadsByID={threadsByID}
            />
          </ImagePreviewProvider></ConversationControlsTestProvider>,
        );
      });
    };

    renderActiveThread(threadA.id);
    expect(turnListRenders.get(threadB.id)).toBe(1);

    threadsByID = new Map([
      [threadA.id, threadA],
      [threadB.id, { ...threadB, preview: "updated in background" }],
    ]);
    renderActiveThread(threadA.id);
    expect(turnListRenders.get(threadB.id)).toBe(1);

    renderActiveThread(threadB.id);
    expect(turnListRenders.get(threadB.id)).toBe(2);
  });
});
