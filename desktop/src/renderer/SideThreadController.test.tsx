import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  RuntimeContext,
  SideThreadEvent,
  SideThreadEventEnvelope,
  SideThreadHistoryResult,
  SideThreadSendResult,
  SideThreadSummary
} from "../shared/protocol";
import {
  useSideThreadController,
  type SideThreadController,
  type SideThreadIPC
} from "./SideThreadController";

const context: RuntimeContext = { kind: "no_project", cwd: "/repo" };
let roots: Root[] = [];

afterEach(() => {
  act(() => {
    for (const root of roots) {
      root.unmount();
    }
  });
  roots = [];
  document.documentElement.classList.remove("resizing-side-thread");
  vi.useRealTimers();
});

function summary(overrides: Partial<SideThreadSummary> = {}): SideThreadSummary {
  return {
    side_thread_id: "side-1",
    main_thread_id: "main-1",
    status: "completed",
    revision: 1,
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    ...overrides
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function makeIPC(overrides: Partial<SideThreadIPC> = {}) {
  let eventHandler: ((envelope: SideThreadEventEnvelope) => void) | undefined;
  const ipc: SideThreadIPC = {
    openSideThread: vi.fn(async () => ({ summary: null })),
    getSideThreadHistory: vi.fn(async () => null),
    sendSideThreadMessage: vi.fn(async () => ({
      user_message_id: "user-1",
      summary: summary({ status: "running" })
    })),
    interruptSideThread: vi.fn(async () => ({ ok: true })),
    resetSideThread: vi.fn(async () => ({ ok: true })),
    onSideThreadEvent: vi.fn((handler) => {
      eventHandler = handler;
      return () => {
        if (eventHandler === handler) {
          eventHandler = undefined;
        }
      };
    }),
    ...overrides
  };
  return {
    ipc,
    emit(event: SideThreadEvent, workdir = "/repo") {
      act(() => eventHandler?.({ workdir, event }));
    }
  };
}

function mountController(
  ipc: SideThreadIPC,
  initialThreadId = "main-1",
  initialContext = context
) {
  let current: SideThreadController | undefined;
  let threadId = initialThreadId;
  let runtimeContext = initialContext;
  function Probe() {
    current = useSideThreadController({
      activeThreadId: threadId,
      activeContext: runtimeContext,
      ipc
    });
    return null;
  }
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  roots.push(root);
  act(() => root.render(createElement(Probe)));
  return {
    get: () => current!,
    rerender(nextThreadId: string, nextContext = runtimeContext) {
      threadId = nextThreadId;
      runtimeContext = nextContext;
      act(() => root.render(createElement(Probe)));
    }
  };
}

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("useSideThreadController", () => {
  it("opens an empty side panel without requesting missing history", async () => {
    const { ipc } = makeIPC();
    const hook = mountController(ipc);

    act(() => hook.get().open());
    await flush();

    expect(ipc.openSideThread).toHaveBeenCalledWith("main-1");
    expect(ipc.getSideThreadHistory).not.toHaveBeenCalled();
    expect(hook.get().entry?.open).toBe(true);
    expect(hook.get().entry?.lastError).toBeUndefined();
  });

  it("blocks sending while an existing running side thread loads history", async () => {
    const history = deferred<SideThreadHistoryResult | null>();
    const sendSideThreadMessage = vi.fn(async () => ({
      user_message_id: "unexpected",
      summary: summary({ status: "running" })
    }));
    const { ipc } = makeIPC({
      openSideThread: vi.fn(async () => ({
        summary: summary({ status: "running" })
      })),
      getSideThreadHistory: vi.fn(() => history.promise),
      sendSideThreadMessage
    });
    const hook = mountController(ipc);

    act(() => hook.get().open());
    await flush();
    expect(hook.get().entry?.streaming).toBe(true);

    act(() => hook.get().sendMessage("must wait"));
    expect(sendSideThreadMessage).not.toHaveBeenCalled();

    history.resolve(null);
    await flush();
  });

  it("merges late history after an in-flight send and keeps streamed output", async () => {
    const history = deferred<SideThreadHistoryResult | null>();
    const send = deferred<SideThreadSendResult>();
    const openedSummary = summary();
    const sentSummary = summary({
      status: "running",
      revision: 2,
      updated_at: "2026-01-01T00:00:02.000Z"
    });
    const { ipc, emit } = makeIPC({
      openSideThread: vi.fn(async () => ({ summary: openedSummary })),
      getSideThreadHistory: vi.fn(() => history.promise),
      sendSideThreadMessage: vi.fn(() => send.promise)
    });
    const hook = mountController(ipc);

    act(() => hook.get().open());
    await flush();
    act(() => hook.get().sendMessage("new prompt"));
    emit({
      type: "delta",
      side_thread_id: "side-1",
      main_thread_id: "main-1",
      revision: 2,
      message_id: "assistant-new",
      text_delta: "new answer"
    });
    history.resolve({
      summary: openedSummary,
      messages: [
        {
          id: "old-user",
          side_thread_id: "side-1",
          role: "user",
          text: "old prompt",
          created_at: "2026-01-01T00:00:00.000Z"
        }
      ]
    });
    await flush();
    send.resolve({ user_message_id: "new-user", summary: sentSummary });
    await flush();

    expect(hook.get().entry?.messages.map((message) => message.id)).toEqual([
      "old-user",
      "new-user",
      "assistant-new"
    ]);
    expect(hook.get().entry?.messages.at(-1)?.text).toBe("new answer");
    expect(hook.get().entry?.summary?.updated_at).toBe(sentSummary.updated_at);
  });

  it("blocks rapid duplicate sends synchronously", async () => {
    const send = deferred<SideThreadSendResult>();
    const sendSideThreadMessage = vi.fn(() => send.promise);
    const { ipc } = makeIPC({ sendSideThreadMessage });
    const hook = mountController(ipc);

    act(() => {
      hook.get().sendMessage("once");
      hook.get().sendMessage("twice");
    });

    expect(sendSideThreadMessage).toHaveBeenCalledTimes(1);
    expect(hook.get().entry?.messages).toHaveLength(1);
    send.resolve({
      user_message_id: "user-1",
      summary: summary({ status: "running" })
    });
    await flush();
  });

  it("rolls back an optimistic message and restores the draft on send failure", async () => {
    const { ipc } = makeIPC({
      sendSideThreadMessage: vi.fn(async () => {
        throw new Error("provider unavailable");
      })
    });
    const hook = mountController(ipc);

    act(() => hook.get().sendMessage("retry me"));
    await flush();

    expect(hook.get().entry?.messages).toEqual([]);
    expect(hook.get().entry?.draft).toBe("retry me");
    expect(hook.get().entry?.streaming).toBe(false);
    expect(hook.get().entry?.lastError).toBe("provider unavailable");
  });

  it("keeps open state and drafts isolated across main-thread switches", async () => {
    const { ipc } = makeIPC();
    const hook = mountController(ipc);

    act(() => {
      hook.get().setDraft("draft-a");
      hook.get().open();
    });
    await flush();
    hook.rerender("main-2");
    expect(hook.get().entry?.open).toBe(false);
    act(() => hook.get().setDraft("draft-b"));

    hook.rerender("main-1");
    expect(hook.get().entry?.open).toBe(true);
    expect(hook.get().entry?.draft).toBe("draft-a");
  });

  it("ignores side-thread events from another workspace", () => {
    const { ipc, emit } = makeIPC();
    const hook = mountController(ipc);

    emit(
      {
        type: "status",
        side_thread_id: "side-1",
        main_thread_id: "main-1",
        summary: summary({ status: "running", revision: 2 })
      },
      "/other-repo"
    );

    expect(hook.get().entry?.summary).toBeNull();
  });

  it("refreshes an open side thread when its workspace becomes active again", async () => {
    let currentSummary = summary();
    let currentMessages: SideThreadHistoryResult["messages"] = [];
    const { ipc } = makeIPC({
      openSideThread: vi.fn(async () => ({ summary: currentSummary })),
      getSideThreadHistory: vi.fn(async () => ({
        summary: currentSummary,
        messages: currentMessages
      }))
    });
    const hook = mountController(ipc);

    act(() => hook.get().open());
    await flush();
    hook.rerender("main-2", { kind: "no_project", cwd: "/other-repo" });

    currentSummary = summary({
      revision: 3,
      updated_at: "2026-01-01T00:00:03.000Z"
    });
    currentMessages = [
      {
        id: "remote-user",
        side_thread_id: "side-1",
        role: "user",
        text: "remote prompt",
        created_at: "2026-01-01T00:00:02.000Z"
      },
      {
        id: "remote-assistant",
        side_thread_id: "side-1",
        role: "assistant",
        text: "remote answer",
        status: "completed",
        created_at: "2026-01-01T00:00:03.000Z"
      }
    ];

    hook.rerender("main-1", context);
    await flush();

    expect(hook.get().entry?.summary?.revision).toBe(3);
    expect(hook.get().entry?.messages.map((message) => message.id)).toEqual([
      "remote-user",
      "remote-assistant"
    ]);
  });

  it("polls a running side thread until a missed terminal update is recovered", async () => {
    vi.useFakeTimers();
    let currentSummary = summary({ status: "running" });
    const { ipc } = makeIPC({
      openSideThread: vi.fn(async () => ({ summary: currentSummary })),
      getSideThreadHistory: vi.fn(async () => ({
        summary: currentSummary,
        messages: []
      }))
    });
    const hook = mountController(ipc);

    act(() => hook.get().open());
    await flush();
    expect(hook.get().entry?.streaming).toBe(true);
    currentSummary = summary({
      status: "completed",
      revision: 2,
      updated_at: "2026-01-01T00:00:02.000Z"
    });

    await act(async () => {
      vi.advanceTimersByTime(2_000);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(hook.get().entry?.summary?.status).toBe("completed");
    expect(hook.get().entry?.streaming).toBe(false);
  });

  it("updates the shared grid width and clears resize state on pointer up", () => {
    const { ipc } = makeIPC();
    const hook = mountController(ipc);
    const separator = document.createElement("button");

    act(() => {
      hook.get().startResize({
        button: 0,
        clientX: 100,
        pointerId: 1,
        currentTarget: separator,
        preventDefault: vi.fn()
      } as unknown as React.PointerEvent<HTMLButtonElement>);
    });
    expect(document.documentElement.classList.contains("resizing-side-thread")).toBe(true);

    act(() => {
      window.dispatchEvent(new MouseEvent("pointermove", { clientX: 50 }));
    });
    expect(hook.get().width).toBe(450);

    act(() => {
      window.dispatchEvent(new Event("pointerup"));
    });
    expect(document.documentElement.classList.contains("resizing-side-thread")).toBe(false);
  });
});
