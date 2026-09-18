import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ServerEvent, Thread, WuuDesktopApi } from "../shared/protocol";
import {
  emptyComposerDraft,
  initialState,
  type AppState,
  type ComposerDraftState,
} from "./AppState";
import type { QueuedComposerMessage } from "./ComposerMessages";
import {
  heldComposerMessagesFromResumeResult,
  useComposerPendingState,
  type ComposerPendingStateController,
} from "./ComposerPendingState";
import { resolveLocalizedText } from "./i18n";

let mountedRoots: Root[] = [];

afterEach(() => {
  act(() => {
    for (const root of mountedRoots) root.unmount();
  });
  mountedRoots = [];
  document.body.innerHTML = "";
  delete (window as unknown as { wuu?: unknown }).wuu;
  vi.restoreAllMocks();
});

async function flushEffects(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

function message(id: string, text: string): QueuedComposerMessage {
  return { id, text, images: [], files: [] };
}

function thread(id = "thread-a", running = false): Thread {
  return {
    id,
    status: running ? "in_progress" : "completed",
    turns: running
      ? [{ id: "turn-running", status: "in_progress", items: [] }]
      : [],
  } as unknown as Thread;
}

function installWuuStub(overrides: Partial<WuuDesktopApi>): void {
  (window as unknown as { wuu: WuuDesktopApi }).wuu = {
    ...overrides,
  } as WuuDesktopApi;
}

async function renderComposerPendingState({
  appState = {
    ...initialState,
    thread: thread(),
    threads: [thread()],
  },
  primaryDraft = emptyComposerDraft(),
}: {
  appState?: AppState;
  primaryDraft?: ComposerDraftState;
} = {}): Promise<{
  get: () => ComposerPendingStateController;
  setStatus: ReturnType<typeof vi.fn>;
  restorePrimaryComposerDraft: ReturnType<typeof vi.fn>;
  restoreComposerDraftForThread: ReturnType<typeof vi.fn>;
  sendComposerMessageToThread: ReturnType<typeof vi.fn>;
  requestDeferredQueryScroll: ReturnType<typeof vi.fn>;
  setPrimaryDraft: (draft: ComposerDraftState) => void;
  setAppState: (state: AppState) => void;
}> {
  let latest: ComposerPendingStateController | undefined;
  let currentAppState = appState;
  let currentPrimaryDraft = primaryDraft;
  const setStatus = vi.fn();
  const restorePrimaryComposerDraft = vi.fn((draft: ComposerDraftState) => {
    currentPrimaryDraft = draft;
  });
  const restoreComposerDraftForThread = vi.fn(
    (_threadID: string, draft: ComposerDraftState) =>
      restorePrimaryComposerDraft(draft),
  );
  const sendComposerMessageToThread = vi.fn();
  const requestDeferredQueryScroll = vi.fn();

  function Probe() {
    latest = useComposerPendingState({
      getAppState: () => currentAppState,
      getPrimaryComposerDraft: () => currentPrimaryDraft,
      restoreComposerDraftForThread,
      setStatus,
      sendComposerMessageToThread,
      requestDeferredQueryScroll,
    });
    return null;
  }

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  mountedRoots.push(root);

  await act(async () => {
    root.render(createElement(Probe));
    await flushEffects();
  });

  return {
    get: () => {
      if (!latest) {
        throw new Error("composer pending state was not rendered");
      }
      return latest;
    },
    setStatus,
    restorePrimaryComposerDraft,
    restoreComposerDraftForThread,
    sendComposerMessageToThread,
    requestDeferredQueryScroll,
    setPrimaryDraft: (draft) => {
      currentPrimaryDraft = draft;
    },
    setAppState: (state) => {
      currentAppState = state;
    },
  };
}

describe("useComposerPendingState", () => {
  it("restores live queue and guide messages from the resume snapshot", () => {
    const restored = heldComposerMessagesFromResumeResult({
      thread: thread("thread-a", true),
      pending_user_messages: [
        {
          id: "guide-1",
          thread_id: "thread-a",
          origin: "steer",
          prompt: "Guide",
        },
        {
          id: "queue-1",
          thread_id: "thread-a",
          origin: "queue",
          prompt: "Next",
        },
      ],
    });

    expect(restored).toEqual([
      expect.objectContaining({ id: "guide-1", origin: "steer", held: false }),
      expect.objectContaining({ id: "queue-1", origin: "queue", held: false }),
    ]);
  });
  it("enqueues messages per thread", async () => {
    const hook = await renderComposerPendingState();

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "First"));
    });

    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"]?.queued,
    ).toEqual([message("queue-1", "First")]);
  });

  it("cancels a queue submission before attachment preparation completes", async () => {
    const dequeueTurn = vi.fn();
    installWuuStub({ dequeueTurn });
    const hook = await renderComposerPendingState();
    act(() => {
      hook.get().enqueueComposerMessage("thread-a", {
        ...message("queue-1", "Cancel before send"),
        operationState: "preparing",
      });
    });

    let removed = false;
    await act(async () => {
      removed = await hook.get().removeQueuedMessage("queue-1");
    });

    expect(removed).toBe(true);
    expect(dequeueTurn).not.toHaveBeenCalled();
    expect(hook.get().pendingComposerMessagesByThread["thread-a"]).toBeUndefined();
  });

  it("cancels a guide submission before attachment preparation completes", async () => {
    const unsteerTurn = vi.fn();
    installWuuStub({ unsteerTurn });
    const hook = await renderComposerPendingState();
    act(() => {
      hook.get().setPendingComposerMessagesByThreadNow({
        "thread-a": {
          queued: [],
          guides: [{
            ...message("guide-1", "Cancel before send"),
            operationState: "preparing",
          }],
        },
      });
    });

    let removed = false;
    await act(async () => {
      removed = await hook.get().removeGuideMessage("guide-1");
    });

    expect(removed).toBe(true);
    expect(unsteerTurn).not.toHaveBeenCalled();
    expect(hook.get().pendingComposerMessagesByThread["thread-a"]).toBeUndefined();
  });

  it("deduplicates a held snapshot that arrives after the queue response", async () => {
    const hook = await renderComposerPendingState();

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "One send"));
      hook.get().syncPendingComposerMessagesFromServerEvent({
        kind: "notification",
        message: {
          method: "turn/held",
          params: {
            thread_id: "thread-a",
            messages: [
              {
                id: "queue-1",
                origin: "queue",
                prompt: "One send",
                images: [],
                files: [],
              },
            ],
          },
        },
      } as ServerEvent);
    });

    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"]?.queued,
    ).toEqual([
      expect.objectContaining({ id: "queue-1", held: true, origin: "queue" }),
    ]);
  });

  it("does not recreate an optimistic row when the held snapshot arrives first", async () => {
    const hook = await renderComposerPendingState();

    act(() => {
      hook.get().syncPendingComposerMessagesFromServerEvent({
        kind: "notification",
        message: {
          method: "turn/held",
          params: {
            thread_id: "thread-a",
            messages: [
              {
                id: "queue-1",
                origin: "queue",
                prompt: "One send",
                images: [],
                files: [],
              },
            ],
          },
        },
      } as ServerEvent);
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "One send"));
    });

    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"]?.queued,
    ).toEqual([
      expect.objectContaining({ id: "queue-1", held: true, origin: "queue" }),
    ]);
  });

  it("syncs server events by removing materialized queue and guide messages", async () => {
    const hook = await renderComposerPendingState();

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "First"));
      hook
        .get()
        .updateThreadPendingComposerMessages("thread-a", (previous) => ({
          ...previous,
          guides: [message("guide-1", "Guide")],
        }));
      hook.get().syncPendingComposerMessagesFromServerEvent({
        kind: "notification",
        message: {
          method: "turn/started",
          params: { thread_id: "thread-a", queue_id: "queue-1" },
        },
      } as ServerEvent);
      hook.get().syncPendingComposerMessagesFromServerEvent({
        kind: "notification",
        message: {
          method: "item/completed",
          params: {
            thread_id: "thread-a",
            item: {
              id: "item-1",
              type: "user_message",
              source_id: "guide-1",
            },
          },
        },
      } as ServerEvent);
    });

    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"],
    ).toBeUndefined();
  });

  it("syncs accepted queue and guide mode transitions from server events", async () => {
    const hook = await renderComposerPendingState();
    const notify = (method: string, params: Record<string, unknown>) => {
      act(() => {
        hook.get().syncPendingComposerMessagesFromServerEvent({
          kind: "notification",
          workdir: "/tmp/project",
          message: { method, params },
        } as ServerEvent);
      });
    };

    notify("turn/steered", {
      message: {
        id: "shared-1",
        thread_id: "thread-a",
        origin: "steer",
        prompt: "Switch me",
      },
    });
    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"]?.guides.map(({ id }) => id),
    ).toEqual(["shared-1"]);

    notify("turn/queued", {
      message: {
        id: "shared-1",
        thread_id: "thread-a",
        origin: "queue",
        prompt: "Switch me",
      },
    });
    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"]?.guides,
    ).toEqual([]);
    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"]?.queued.map(({ id }) => id),
    ).toEqual(["shared-1"]);
  });

  it("reconciles held messages in server order and releases only the selected idle item", async () => {
    const steerTurn = vi.fn().mockResolvedValue({ turn_id: "turn-released" });
    installWuuStub({ steerTurn });
    const hook = await renderComposerPendingState();
    const heldMessages = [
      {
        id: "guide-1",
        thread_id: "thread-a",
        origin: "steer",
        prompt: "Guide",
        images: [],
        files: [],
      },
      {
        id: "queue-1",
        thread_id: "thread-a",
        origin: "queue",
        prompt: "First",
        images: [{ media_type: "image/png", data: "aW1hZ2U=" }],
        files: [],
      },
      {
        id: "queue-2",
        thread_id: "thread-a",
        origin: "queue",
        prompt: "Second",
        images: [],
        files: [],
      },
    ];

    act(() => {
      hook.get().syncPendingComposerMessagesFromServerEvent({
        kind: "notification",
        message: {
          method: "turn/held",
          params: { thread_id: "thread-a", messages: heldMessages },
        },
      } as ServerEvent);
    });

    const pending = hook.get().pendingComposerMessagesByThread["thread-a"];
    expect(pending?.guides).toEqual([
      expect.objectContaining({
        id: "guide-1",
        held: true,
        heldPosition: 0,
        origin: "steer",
      }),
    ]);
    expect(pending?.queued).toEqual([
      expect.objectContaining({
        id: "queue-1",
        held: true,
        heldPosition: 1,
        images: [
          expect.objectContaining({ media_type: "image/png", data: "aW1hZ2U=" }),
        ],
      }),
      expect.objectContaining({ id: "queue-2", heldPosition: 2 }),
    ]);

    await act(async () => {
      await hook.get().guideQueuedMessage("queue-1");
    });

    expect(steerTurn).toHaveBeenCalledWith(
      "thread-a",
      "",
      "First",
      [{ media_type: "image/png", data: "aW1hZ2U=" }],
      "queue-1",
      [],
      undefined,
      [],
    );
    expect(
      hook
        .get()
        .pendingComposerMessagesByThread["thread-a"]?.queued.map((item) => item.id),
    ).toEqual(["queue-2"]);
    expect(
      hook
        .get()
        .pendingComposerMessagesByThread["thread-a"]?.guides.map((item) => item.id),
    ).toEqual(["guide-1"]);

    act(() => {
      hook.get().syncPendingComposerMessagesFromServerEvent({
        kind: "notification",
        message: {
          method: "thread/resumed",
          params: {
            thread: { id: "thread-a" },
            held_user_messages: [heldMessages[0], heldMessages[2]],
          },
        },
      } as ServerEvent);
    });
    expect(
      hook
        .get()
        .pendingComposerMessagesByThread["thread-a"]?.queued.map((item) => item.id),
    ).toEqual(["queue-2"]);
  });

  it("does not duplicate a queue converted to steer when interrupt holds it before the response", async () => {
    let resolveSteer: ((value: { turn_id: string }) => void) | undefined;
    const steerTurn = vi.fn(
      () =>
        new Promise<{ turn_id: string }>((resolve) => {
          resolveSteer = resolve;
        }),
    );
    installWuuStub({ steerTurn: steerTurn as WuuDesktopApi["steerTurn"] });
    const runningThread = thread("thread-a", true);
    const hook = await renderComposerPendingState({
      appState: {
        ...initialState,
        thread: runningThread,
        threads: [runningThread],
      },
    });

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("shared-id", "Guide this"));
    });
    let converting: Promise<void> | undefined;
    act(() => {
      converting = hook.get().guideQueuedMessage("shared-id");
    });
    act(() => {
      hook.get().syncPendingComposerMessagesFromServerEvent({
        kind: "notification",
        message: {
          method: "turn/held",
          params: {
            thread_id: "thread-a",
            messages: [
              {
                id: "shared-id",
                thread_id: "thread-a",
                origin: "steer",
                prompt: "Guide this",
              },
            ],
          },
        },
      } as ServerEvent);
    });

    await act(async () => {
      resolveSteer?.({ turn_id: "turn-running" });
      await converting;
    });

    const pending = hook.get().pendingComposerMessagesByThread["thread-a"];
    expect(pending?.queued).toEqual([]);
    expect(pending?.guides).toEqual([
      expect.objectContaining({
        id: "shared-id",
        held: true,
        origin: "steer",
      }),
    ]);
  });

  it("keeps a queued document snapshot when converting it to a steer", async () => {
    const steerTurn = vi.fn().mockResolvedValue({ turn_id: "turn-running" });
    installWuuStub({ steerTurn });
    const runningThread = thread("thread-a", true);
    const hook = await renderComposerPendingState({
      appState: {
        ...initialState,
        thread: runningThread,
        threads: [runningThread],
      },
    });
    const queued = {
      ...message("queue-1", "Revise it"),
      activeDocument: { path: "docs/plan.md" },
    };

    act(() => {
      hook.get().enqueueComposerMessage("thread-a", queued);
    });
    await act(async () => {
      await hook.get().guideQueuedMessage("queue-1");
    });

    expect(steerTurn).toHaveBeenCalledWith(
      "thread-a",
      "turn-running",
      "Revise it",
      [],
      "queue-1",
      [],
      { path: "docs/plan.md" },
    );
  });

  it("keeps queued content parts when converting it to a steer", async () => {
    const steerTurn = vi.fn().mockResolvedValue({ turn_id: "turn-running" });
    installWuuStub({ steerTurn });
    const runningThread = thread("thread-a", true);
    const hook = await renderComposerPendingState({
      appState: {
        ...initialState,
        thread: runningThread,
        threads: [runningThread],
      },
    });
    const contentParts = [
      { type: "pasted_text" as const, text: "Pasted text", title: "Pasted text" },
      { type: "text" as const, text: " then revise it" },
    ];
    act(() => {
      hook.get().enqueueComposerMessage("thread-a", {
        ...message("queue-1", "Pasted text then revise it"),
        contentParts,
      });
    });

    await act(async () => {
      await hook.get().guideQueuedMessage("queue-1");
    });

    expect(steerTurn).toHaveBeenCalledWith(
      "thread-a",
      "turn-running",
      "Pasted text then revise it",
      [],
      "queue-1",
      [],
      undefined,
      contentParts,
    );
  });

  it("moves a pending guide back to the queue without changing its identity", async () => {
    const requeueTurn = vi.fn().mockResolvedValue({
      ok: true,
      state: "queued",
      queued: { id: "guide-1", thread_id: "thread-a" },
    });
    installWuuStub({ requeueTurn });
    const runningThread = thread("thread-a", true);
    const hook = await renderComposerPendingState({
      appState: {
        ...initialState,
        thread: runningThread,
        threads: [runningThread],
      },
    });
    const guide = {
      ...message("guide-1", "Send me next"),
      origin: "steer" as const,
      activeDocument: { path: "docs/next.md" },
    };
    act(() => {
      hook.get().setPendingComposerMessagesByThreadNow({
        "thread-a": { queued: [], guides: [guide] },
      });
    });

    await act(async () => {
      await hook.get().guideQueuedMessage("guide-1");
    });

    expect(requeueTurn).toHaveBeenCalledWith("thread-a", "guide-1");
    const pending = hook.get().pendingComposerMessagesByThread["thread-a"];
    expect(pending?.guides).toEqual([]);
    expect(pending?.queued).toEqual([
      expect.objectContaining({
        id: "guide-1",
        text: "Send me next",
        origin: "queue",
        activeDocument: { path: "docs/next.md" },
      }),
    ]);
  });

  it("registers deferred placement when a queued message is steered from the drawer", async () => {
    const steerTurn = vi.fn().mockResolvedValue({ turn_id: "turn-running" });
    installWuuStub({ steerTurn });
    const runningThread = thread("thread-a", true);
    const hook = await renderComposerPendingState({
      appState: {
        ...initialState,
        thread: runningThread,
        threads: [runningThread],
      },
    });

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "Revise it"));
    });
    await act(async () => {
      await hook.get().guideQueuedMessage("queue-1");
    });

    expect(hook.requestDeferredQueryScroll).toHaveBeenCalledWith("queue-1");
    expect(steerTurn).toHaveBeenCalled();
  });

  it("registers deferred placement when a guide is requeued from the drawer", async () => {
    const requeueTurn = vi.fn().mockResolvedValue({
      ok: true,
      state: "queued",
      queued: { id: "guide-1", thread_id: "thread-a" },
    });
    installWuuStub({ requeueTurn });
    const runningThread = thread("thread-a", true);
    const hook = await renderComposerPendingState({
      appState: {
        ...initialState,
        thread: runningThread,
        threads: [runningThread],
      },
    });

    act(() => {
      hook.get().setPendingComposerMessagesByThreadNow({
        "thread-a": {
          queued: [],
          guides: [
            { ...message("guide-1", "Send me next"), origin: "steer" as const },
          ],
        },
      });
    });
    await act(async () => {
      await hook.get().guideQueuedMessage("guide-1");
    });

    expect(hook.requestDeferredQueryScroll).toHaveBeenCalledWith("guide-1");
    expect(requeueTurn).toHaveBeenCalledWith("thread-a", "guide-1");
  });

  it("restores a queued message into the primary composer for editing", async () => {
    const dequeueTurn = vi.fn().mockResolvedValue({ ok: true });
    installWuuStub({ dequeueTurn });
    const hook = await renderComposerPendingState();

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "Edit me"));
    });
    await act(async () => {
      await hook.get().editQueuedMessage("queue-1");
    });

    expect(hook.restorePrimaryComposerDraft).toHaveBeenCalledWith({
      prompt: "Edit me",
      images: [],
      files: [],
    });
    expect(dequeueTurn).toHaveBeenCalledWith("thread-a", "queue-1");
    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"],
    ).toBeUndefined();
    expect(resolveLocalizedText(hook.setStatus.mock.calls[0][0] as string)).toBe(
      "已撤回排队消息，可编辑后重新发送",
    );
  });

  it("refuses to edit pending messages while the primary composer has content", async () => {
    const hook = await renderComposerPendingState({
      primaryDraft: { prompt: "busy", images: [], files: [] },
    });

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "Edit me"));
    });
    await act(async () => {
      await hook.get().editQueuedMessage("queue-1");
    });

    expect(hook.restorePrimaryComposerDraft).not.toHaveBeenCalled();
    expect(resolveLocalizedText(hook.setStatus.mock.calls[0][0] as string)).toBe(
      "先发送或清空当前输入，再编辑排队消息",
    );
  });

  it("restores a queued message when dequeue misses it before turn/started", async () => {
    installWuuStub({
      dequeueTurn: vi.fn().mockResolvedValue({ ok: false }),
    });
    const hook = await renderComposerPendingState();

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "Too late"));
    });
    await act(async () => {
      await hook.get().editQueuedMessage("queue-1");
    });

    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"]?.queued,
    ).toEqual([message("queue-1", "Too late")]);
    expect(hook.restorePrimaryComposerDraft).not.toHaveBeenCalled();
    expect(resolveLocalizedText(hook.setStatus.mock.calls[0][0] as string)).toBe(
      "消息仍在排队，请稍后重试",
    );
  });

  it("does not restore a queued message that already materialized", async () => {
    installWuuStub({
      dequeueTurn: vi.fn().mockResolvedValue({ ok: false }),
    });
    const startedThread = {
      ...thread("thread-a", true),
      turns: [
        {
          id: "turn-running",
          status: "in_progress",
          items: [
            {
              id: "item-user",
              type: "user_message",
              source_id: "queue-1",
              text: "Too late",
            },
          ],
        },
      ],
    } as unknown as Thread;
    const hook = await renderComposerPendingState({
      appState: {
        ...initialState,
        thread: startedThread,
        threads: [startedThread],
      },
    });

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "Too late"));
    });
    await act(async () => {
      await hook.get().editQueuedMessage("queue-1");
    });

    expect(hook.restorePrimaryComposerDraft).not.toHaveBeenCalled();
    expect(
      hook.get().pendingComposerMessagesByThread["thread-a"],
    ).toBeUndefined();
  });

  it("restores an edited draft to its originating thread after a tab switch", async () => {
    let finishDequeue: ((value: { ok: boolean }) => void) | undefined;
    installWuuStub({
      dequeueTurn: vi.fn().mockImplementation(
        () =>
          new Promise<{ ok: boolean }>((resolve) => {
            finishDequeue = resolve;
          }),
      ),
    });
    const hook = await renderComposerPendingState();

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "Keep me"));
    });
    let editing: Promise<void> | undefined;
    await act(async () => {
      editing = hook.get().editQueuedMessage("queue-1");
      await Promise.resolve();
    });
    hook.setAppState({
      ...initialState,
      thread: thread("thread-b"),
      threads: [thread("thread-a"), thread("thread-b")],
    });
    await act(async () => {
      finishDequeue?.({ ok: true });
      await editing;
    });

    expect(hook.restoreComposerDraftForThread).toHaveBeenCalledWith(
      "thread-a",
      { prompt: "Keep me", images: [], files: [] },
    );
  });

  it("leaves a queued message queued when there is no active turn to guide", async () => {
    installWuuStub({});
    const hook = await renderComposerPendingState();

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "Keep queued"));
    });
    await act(async () => {
      await hook.get().guideQueuedMessage("queue-1");
    });

    expect(
      hook
        .get()
        .pendingComposerMessagesByThread["thread-a"]?.queued,
    ).toEqual([message("queue-1", "Keep queued")]);
    expect(hook.sendComposerMessageToThread).not.toHaveBeenCalled();
    expect(resolveLocalizedText(hook.setStatus.mock.calls[0][0] as string)).toBe(
      "没有可引导的任务",
    );
  });

  it("rolls back a queued message removal when dequeue fails", async () => {
    installWuuStub({
      dequeueTurn: vi.fn().mockRejectedValue(new Error("network down")),
    });
    const hook = await renderComposerPendingState();

    act(() => {
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-1", "First"));
      hook
        .get()
        .enqueueComposerMessage("thread-a", message("queue-2", "Second"));
    });
    let removed = true;
    await act(async () => {
      removed = await hook.get().removeQueuedMessage("queue-1");
    });

    expect(removed).toBe(false);
    expect(
      hook
        .get()
        .pendingComposerMessagesByThread["thread-a"]?.queued.map(
          (item) => item.id,
        ),
    ).toEqual(["queue-1", "queue-2"]);
    expect(hook.setStatus).toHaveBeenCalledWith("network down");
  });
});
