import { act, useLayoutEffect, useState, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Thread } from "../shared/protocol";
import { CachedConversationPanes } from "./CachedConversationPanes";
import { retainCachedConversationPaneThreads } from "./ConversationPaneCache";
import { ConversationRenderActivityProvider, useConversationRevealSnap } from "./ConversationRenderActivity";
import { ImagePreviewProvider } from "./ImagePreview";

let roots: Root[] = [];

afterEach(() => {
  for (const root of roots) {
    act(() => root.unmount());
  }
  roots = [];
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

function longThread(id: string, turnCount: number): Thread {
  return {
    id,
    title: id,
    preview: id,
    model_provider: "test",
    model: "test",
    cwd: `/tmp/${id}`,
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: Array.from({ length: turnCount }, (_, index) => ({
      id: `${id}-turn-${index}`,
      status: "completed",
      items_view: "full",
      items: [
        {
          id: `${id}-user-${index}`,
          type: "user_message",
          text: `${id} question ${index}`,
        },
        {
          id: `${id}-agent-${index}`,
          type: "agent_message",
          text: `${id} answer ${index}`,
        },
      ],
    })),
  } as Thread;
}

describe("CachedConversationPanes real message tree", () => {
  it("keeps reveal motion suppressed through nested layout commits and the first paint", () => {
    const frames = new Map<number, FrameRequestCallback>();
    let nextFrame = 0;
    vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => {
      frames.set(++nextFrame, callback);
      return nextFrame;
    });
    vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => { frames.delete(id); });
    const paint = () => act(() => {
      const callbacks = [...frames.values()];
      frames.clear();
      callbacks.forEach(callback => callback(0));
    });
    const snapshots: boolean[] = [];
    function CatchUp() {
      const snap = useConversationRevealSnap();
      const [measured, setMeasured] = useState(false);
      useLayoutEffect(() => {
        snapshots.push(snap);
        if (snap && !measured) setMeasured(true);
      });
      return null;
    }
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    roots.push(root);
    act(() => root.render(<ConversationRenderActivityProvider active={false}><CatchUp /></ConversationRenderActivityProvider>));
    snapshots.length = 0;
    act(() => root.render(<ConversationRenderActivityProvider active><CatchUp /></ConversationRenderActivityProvider>));
    expect(snapshots.length).toBeGreaterThan(0);
    expect(snapshots.every(Boolean)).toBe(true);
    paint();
    expect(snapshots.every(Boolean)).toBe(true);
    paint();
    expect(snapshots.at(-1)).toBe(false);
  });

  it("keeps a long cross-workspace pane mounted and reveals it synchronously", () => {
    const source = longThread("source-workspace", 200);
    const target = longThread("target-workspace", 200);
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    roots.push(root);

    const stableProps = {
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
      "activeThreadID" | "threadIDs" | "threadsByID"
    >;
    let paneThreads = retainCachedConversationPaneThreads({
      threadIDs: [source.id, target.id],
      currentThreadsByID: new Map([
        [source.id, source],
        [target.id, target],
      ]),
      previousThreadsByID: new Map(),
    });
    const renderActive = (activeThreadID: string): void => {
      act(() => {
        root.render(
          <ImagePreviewProvider>
            <CachedConversationPanes
              {...stableProps}
              activeThreadID={activeThreadID}
              threadIDs={[target.id, source.id]}
              threadsByID={paneThreads}
            />
          </ImagePreviewProvider>,
        );
      });
    };

    renderActive(source.id);
    const sourcePane = container.querySelector<HTMLElement>(
      `[data-thread-id="${source.id}"]`,
    );
    const sourceLastTurn = sourcePane?.querySelector<HTMLElement>(
      `[data-turn-id="${source.id}-turn-199"]`,
    );
    expect(sourcePane?.querySelectorAll(".turn")).toHaveLength(40);

    paneThreads = retainCachedConversationPaneThreads({
      threadIDs: [target.id, source.id],
      currentThreadsByID: new Map([[target.id, target]]),
      previousThreadsByID: paneThreads,
    });
    renderActive(target.id);
    expect(sourcePane?.getAttribute("data-active")).toBe("false");
    expect(sourcePane?.isConnected).toBe(true);

    renderActive(source.id);
    expect(sourcePane?.getAttribute("data-active")).toBe("true");
    expect(sourcePane?.hasAttribute("inert")).toBe(false);
    expect(
      sourcePane?.querySelector(`[data-turn-id="${source.id}-turn-199"]`),
    ).toBe(sourceLastTurn);
  });

  it("reveals a running conversation's latest process row on the first visible frame", () => {
    const idle = longThread("idle-session", 6);
    const running: Thread = {
      ...longThread("running-session", 2),
      status: "in_progress",
      turns: [
        {
          id: "running-session-turn-0",
          status: "completed",
          items_view: "full",
          items: [
            {
              id: "running-session-user-0",
              type: "user_message",
              text: "start",
            },
          ],
        },
        {
          id: "running-session-turn-1",
          status: "in_progress",
          items_view: "full",
          items: [
            {
              id: "running-session-user-1",
              type: "user_message",
              text: "keep going",
            },
            {
              id: "running-session-tool-1",
              type: "tool_call",
              name: "read_file",
              status: "in_progress",
              arguments: JSON.stringify({ path: "a.ts" }),
            },
          ],
        },
      ],
    } as Thread;
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    roots.push(root);
    const stableProps = {
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
      "activeThreadID" | "threadIDs" | "threadsByID"
    >;
    let paneThreads = retainCachedConversationPaneThreads({
      threadIDs: [running.id, idle.id],
      currentThreadsByID: new Map([
        [running.id, running],
        [idle.id, idle],
      ]),
      previousThreadsByID: new Map(),
    });
    const renderActive = (activeThreadID: string, threadsByID = paneThreads): void => {
      act(() => {
        root.render(
          <ImagePreviewProvider>
            <CachedConversationPanes
              {...stableProps}
              activeThreadID={activeThreadID}
              threadIDs={[running.id, idle.id]}
              threadsByID={threadsByID}
            />
          </ImagePreviewProvider>,
        );
      });
    };

    renderActive(running.id);
    const runningPane = container.querySelector<HTMLElement>(
      `[data-thread-id="${running.id}"]`,
    );
    expect(runningPane?.textContent).toContain("a.ts");

    renderActive(idle.id);
    const grown: Thread = {
      ...running,
      turns: running.turns.map((turn) =>
        turn.id === "running-session-turn-1"
          ? {
              ...turn,
              items: [
                ...turn.items,
                {
                  id: "running-session-tool-2",
                  type: "tool_call",
                  name: "grep",
                  status: "in_progress",
                  arguments: JSON.stringify({ pattern: "switch" }),
                },
              ],
            }
          : turn,
      ),
    } as Thread;
    paneThreads = retainCachedConversationPaneThreads({
      threadIDs: [running.id, idle.id],
      currentThreadsByID: new Map([
        [running.id, grown],
        [idle.id, idle],
      ]),
      previousThreadsByID: paneThreads,
    });
    renderActive(idle.id, paneThreads);
    expect(runningPane?.getAttribute("data-active")).toBe("false");
    expect(runningPane?.textContent).not.toContain("switch");

    renderActive(running.id, paneThreads);
    expect(runningPane?.getAttribute("data-active")).toBe("true");
    expect(runningPane?.textContent).toContain("switch");
    expect(runningPane?.querySelector(".process-text-motion-enter")).toBeNull();
  });
});
