import { act, useEffect, useState, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  InitializeResult,
  ServerEvent,
  Thread,
  WuuDesktopApi,
} from "../shared/protocol";

vi.mock("./ComposerView", async (importOriginal) => {
  const original = await importOriginal<typeof import("./ComposerView")>();
  type ComposerProps = ComponentProps<typeof original.Composer>;
  return {
    ...original,
    Composer: (props: ComposerProps): JSX.Element => {
      const [prompt, setLocalPrompt] = useState(props.prompt);
      useEffect(
        () => setLocalPrompt(props.prompt),
        [props.prompt, props.promptRevision],
      );
      return <div
        data-testid="composer-probe"
        data-running={props.running}
        data-draft-pending={props.draftPending}
        data-unconfirmed-ids={[...props.queuedMessages, ...props.guideMessages].filter(message => message.deliveryUnconfirmed).map(message => message.id).join(",")}
        data-stop-state={props.stopState}
        data-queued-ids={props.queuedMessages
          .map((message) => message.id)
          .join(",")}
        data-guide-ids={props.guideMessages
          .map((message) => message.id)
          .join(",")}
        data-held-order={[...props.queuedMessages, ...props.guideMessages]
          .filter((message) => message.held)
          .sort(
            (left, right) =>
              (left.heldPosition ?? Number.MAX_SAFE_INTEGER) -
              (right.heldPosition ?? Number.MAX_SAFE_INTEGER),
          )
          .map((message) => message.id)
          .join(",")}
      >
        <textarea
          aria-label="composer-probe-input"
          value={prompt}
          onChange={(event) => {
            const value = event.currentTarget.value;
            setLocalPrompt(value);
            props.setPrompt(value);
          }}
        />
        <button type="button" onClick={() => props.onSend()}>
          send
        </button>
        <button type="button" aria-label="stop" onClick={props.onInterrupt}>stop</button>
        {props.onSteer ? (
          <button type="button" aria-label="steer" onClick={() => props.onSteer?.()}>
            steer
          </button>
        ) : null}
        {props.queuedMessages[0] ? (
          <>
            <button
              type="button"
              aria-label="edit-first-queued"
              onClick={() =>
                props.onEditQueuedMessage(props.queuedMessages[0].id)
              }
            >
              edit queued
            </button>
            <button
              type="button"
              aria-label="steer-first-queued"
              onClick={() =>
                props.onGuideQueuedMessage(props.queuedMessages[0].id)
              }
            >
              steer queued
            </button>
          </>
        ) : null}
      </div>;
    },
  };
});

vi.mock("@xterm/xterm", () => ({
  Terminal: vi.fn().mockImplementation(() => ({
    loadAddon: vi.fn(),
    open: vi.fn(),
    write: vi.fn(),
    dispose: vi.fn(),
    onData: vi.fn(() => ({ dispose: vi.fn() })),
    onResize: vi.fn(() => ({ dispose: vi.fn() })),
  })),
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: vi.fn().mockImplementation(() => ({ fit: vi.fn() })),
}));

vi.mock("./WorkspaceMonacoEditor", () => ({
  WorkspaceMonacoEditor: (): JSX.Element => <div />,
}));

import { createConversationTestView, createDefaultApplicationTestView } from "./test-support/DefaultApplication";
import { getDesktopApplication } from "./plugins/ApplicationController.test-support";

const workspace = "/tmp/wuu-queued-turn-reconciliation-test";
const threadID = "thread-queued-reconciliation";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];

function initialized(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    workspace_root: workspace,
    permissions: { mode: "standard" },
    providers: [
      { name: "fake", type: "openai-compatible", model: "fake-model", api_key_configured: true },
    ],
    advanced_settings: {
      max_steps: 64,
      max_context_tokens: 0,
      temperature: 0,
      disable_auto_compact: false,
    },
  };
}

function runningThread(materializedQueueID?: string): Thread {
  return {
    id: threadID,
    preview: "queued reconciliation",
    model_provider: "fake",
    model: "fake-model",
    cwd: workspace,
    status: "in_progress",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:01Z",
    turns: [
      {
        id: materializedQueueID ? "turn-queued" : "turn-current",
        status: "in_progress",
        items_view: "full",
        items: [
          {
            id: materializedQueueID ? "item-queued-user" : "item-current-user",
            type: "user_message",
            status: "completed",
            text: materializedQueueID ? "queued follow-up" : "current request",
            source_id: materializedQueueID,
          },
        ],
      },
    ],
  };
}

function answerReadyThread(): Thread {
  const current = runningThread();
  return {
    ...current,
    turns: current.turns.map((turn) => ({
      ...turn,
      answer_ready_at: "2026-08-31T04:49:41.463Z",
      items: [
        ...turn.items,
        {
          id: "item-terminal-answer",
          type: "agent_message" as const,
          status: "completed" as const,
          terminal: true,
          text: "done",
        },
      ],
    })),
  };
}

function installWindowStubs(): void {
  class MockResizeObserver {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
  (globalThis as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver =
    MockResizeObserver as typeof ResizeObserver;
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

function installWuuApi(options: {
  thread?: Thread;
  startTurn?: WuuDesktopApi["startTurn"];
  queueTurn?: WuuDesktopApi["queueTurn"];
  steerTurn?: WuuDesktopApi["steerTurn"];
  resumeThread?: WuuDesktopApi["resumeThread"];
} = {}): {
  queuedClientIDs: string[];
  dequeuedClientIDs: string[];
} {
  const queuedClientIDs: string[] = [];
  const dequeuedClientIDs: string[] = [];
  const initialThread = options.thread ?? runningThread();
  const api = {
    listProjects: vi.fn().mockResolvedValue({
      projects: [],
      active_context: { kind: "no_project", cwd: workspace },
    }),
    selectNoProject: vi.fn().mockResolvedValue({
      projects: [],
      active_context: { kind: "no_project", cwd: workspace },
    }),
    initialize: vi.fn().mockResolvedValue(initialized()),
    listThreads: vi.fn().mockResolvedValue({ threads: [initialThread] }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread:
      options.resumeThread ??
      vi.fn().mockResolvedValue({ thread: initialThread }),
    startTurn: options.startTurn ?? vi.fn().mockResolvedValue({
      turn: {
        id: "turn-follow-up",
        status: "in_progress",
        items_view: "full",
        items: [],
      },
    }),
    queueTurn: options.queueTurn ?? vi
      .fn()
      .mockImplementation(
        (
          _threadID: string,
          _prompt: string,
          _images: unknown[],
          clientID: string,
        ) => {
          queuedClientIDs.push(clientID);
          return Promise.resolve({
            queued: { id: clientID, thread_id: threadID },
          });
        },
      ),
    steerTurn: options.steerTurn ?? vi.fn().mockResolvedValue({ turn_id: "turn-current" }),
    interruptTurn: vi.fn().mockResolvedValue({ ok: true }),
    dequeueTurn: vi
      .fn()
      .mockImplementation((_threadID: string, clientID: string) => {
        dequeuedClientIDs.push(clientID);
        return Promise.resolve({ ok: true });
      }),
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({
      is_repo: false,
      dirty_count: 0,
      files: [],
    }),
    onServerEvent: vi.fn((handler: (event: ServerEvent) => void) => {
      serverEventHandlers.push(handler);
      return () => {
        serverEventHandlers = serverEventHandlers.filter(
          (item) => item !== handler,
        );
      };
    }),
    onWindowResizeState: vi.fn(() => () => {}),
    onTerminalEvent: vi.fn(() => () => {}),
    respondToServerRequest: vi.fn().mockResolvedValue(undefined),
    rejectServerRequest: vi.fn().mockResolvedValue(undefined),
  } as unknown as WuuDesktopApi;
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: api,
  });
  return { queuedClientIDs, dequeuedClientIDs };
}

async function flushAsync(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function composerProbe(): HTMLElement {
  const probe = container.querySelector<HTMLElement>(
    '[data-testid="composer-probe"]',
  );
  if (!probe) {
    throw new Error("composer probe not rendered");
  }
  return probe;
}

describe("queued turn reconciliation", () => {
  beforeEach(() => {
    installWindowStubs();
    serverEventHandlers = [];
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
  });

  afterEach(() => {
    act(() => {
      root?.unmount();
    });
    root = null;
    container.remove();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
    vi.restoreAllMocks();
  });

  it("routes continuation using the source thread context and rejects duplicate requests", async () => {
    const thread: Thread = { ...runningThread(), status: "idle", cwd: "/tmp/wuu-other-runtime", turns: runningThread().turns.map(turn => ({ ...turn, status: "interrupted" as const })) };
    let accept!: (result: { turn: Thread["turns"][number] }) => void;
    const startTurn = vi.fn<WuuDesktopApi["startTurn"]>(() => new Promise<{ turn: Thread["turns"][number] }>(resolve => { accept = resolve; }));
    installWuuApi({ thread, startTurn });
    await act(async () => { root = createRoot(container); root.render(await createConversationTestView(threadID)); });
    await flushAsync();
    const request = () => { void getDesktopApplication().invoke("conversation.continue", { threadId: threadID, turnId: "turn-current", text: "" }); };
    await act(async () => { request(); request(); });
    expect(startTurn).toHaveBeenCalledTimes(1);
    expect(startTurn.mock.calls[0]).toEqual([threadID, "", undefined, undefined, thread.permission_mode, undefined, undefined,
      { kind: "no_project", cwd: thread.cwd }, undefined, "turn-current"]);
    await act(async () => accept({ turn: { id: "continued", status: "completed", items_view: "full", items: [] } }));
    await act(async () => { request(); });
    expect(startTurn).toHaveBeenCalledTimes(1);
  });

  it.each(["queue", "steer"])("preserves reading flow when a %s message enters the conversation", async mode => {
    const { queuedClientIDs } = installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();
    vi.mocked(window.matchMedia).mockImplementation(query => ({
      matches: query.includes("prefers-reduced-motion"),
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
    } as unknown as MediaQueryList));
    const viewport = container.querySelector<HTMLElement>(".scroll-region")!;
    const content = viewport.querySelector<HTMLElement>(".scroll-region-content")!;
    let top = 1400;
    const tail = () => Number.parseFloat(content.style.paddingBottom || "0");
    Object.defineProperties(viewport, {
      clientHeight: { configurable: true, get: () => 600 },
      scrollHeight: { configurable: true, get: () => 2000 + tail() },
      scrollTop: { configurable: true, get: () => top, set: value => { top = Math.max(0, Math.min(value, 1400 + tail())); } },
    });
    const originalRect = HTMLElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      if (this === viewport) return { top: 100, bottom: 700, height: 600 } as DOMRect;
      if (this === content) return { height: 2000 + tail() } as DOMRect;
      if (this.hasAttribute("data-user-message-id")) return { top: 1870 - top, bottom: 1950 - top, height: 80 } as DOMRect;
      return originalRect.call(this);
    });
    await act(async () => {
      const textarea = composerProbe().querySelector("textarea")!;
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(textarea, "follow-up request");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      const button = mode === "queue" ? composerProbe().querySelector("button")!
        : composerProbe().querySelector<HTMLButtonElement>('[aria-label="steer"]')!;
      button.click();
    });
    const sourceID = mode === "queue" ? queuedClientIDs[0] : vi.mocked(window.wuu.steerTurn).mock.calls[0][4];
    expect(sourceID).toBeTruthy();
    expect(tail()).toBe(0);
    await act(async () => {
      for (const handler of serverEventHandlers) handler({
        kind: "notification", workdir: workspace,
        message: { method: "item/completed", params: {
          thread_id: threadID, turn_id: "turn-current",
          item: { id: "accepted-input", type: "user_message", status: "completed", text: "follow-up request", source_id: sourceID },
        } },
      });
    });
    expect(container.querySelector('[data-user-message-id="accepted-input"]')).not.toBeNull();
    expect(tail()).toBe(0);
    expect(top).toBe(1400);
  });

  it("keeps a drawer-steered message in the existing reading flow", async () => {
    const steerTurn = vi.fn();
    installWuuApi({
      steerTurn: steerTurn as unknown as WuuDesktopApi["steerTurn"],
    });
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();
    // A restored queue entry follows the same policy as a locally queued one.
    await act(async () => {
      for (const handler of serverEventHandlers) handler({
        kind: "notification", workdir: workspace,
        message: { method: "turn/queued", params: {
          thread_id: threadID,
          message: { id: "queued-drawer", thread_id: threadID, origin: "queue", prompt: "drawer follow-up" },
        } },
      });
    });
    expect(composerProbe().dataset.queuedIds).toContain("queued-drawer");
    vi.mocked(window.matchMedia).mockImplementation(query => ({
      matches: query.includes("prefers-reduced-motion"),
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
    } as unknown as MediaQueryList));
    const viewport = container.querySelector<HTMLElement>(".scroll-region")!;
    const content = viewport.querySelector<HTMLElement>(".scroll-region-content")!;
    let top = 1400;
    const tail = () => Number.parseFloat(content.style.paddingBottom || "0");
    Object.defineProperties(viewport, {
      clientHeight: { configurable: true, get: () => 600 },
      scrollHeight: { configurable: true, get: () => 2000 + tail() },
      scrollTop: { configurable: true, get: () => top, set: value => { top = Math.max(0, Math.min(value, 1400 + tail())); } },
    });
    const originalRect = HTMLElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      if (this === viewport) return { top: 100, bottom: 700, height: 600 } as DOMRect;
      if (this === content) return { height: 2000 + tail() } as DOMRect;
      if (this.hasAttribute("data-user-message-id")) return { top: 1870 - top, bottom: 1950 - top, height: 80 } as DOMRect;
      return originalRect.call(this);
    });
    await act(async () => {
      composerProbe().querySelector<HTMLButtonElement>('[aria-label="steer-first-queued"]')!.click();
    });
    expect(steerTurn).toHaveBeenCalled();
    const sourceID = steerTurn.mock.calls[0][4];
    expect(sourceID).toBe("queued-drawer");
    expect(tail()).toBe(0);
    await act(async () => {
      for (const handler of serverEventHandlers) handler({
        kind: "notification", workdir: workspace,
        message: { method: "item/completed", params: {
          thread_id: threadID, turn_id: "turn-current",
          item: { id: "accepted-drawer-input", type: "user_message", status: "completed", text: "drawer follow-up", source_id: sourceID },
        } },
      });
    });
    expect(container.querySelector('[data-user-message-id="accepted-drawer-input"]')).not.toBeNull();
    expect(tail()).toBe(0);
    expect(top).toBe(1400);
  });

  it("keeps execution visible until stopping a late admission is confirmed", async () => {
    let resolveStart!: (value: Awaited<ReturnType<WuuDesktopApi["startTurn"]>>) => void;
    const startTurn = vi.fn(() => new Promise<Awaited<ReturnType<WuuDesktopApi["startTurn"]>>>((resolve) => {
      resolveStart = resolve;
    }));
    const idleThread: Thread = { ...runningThread(), status: "idle", turns: [] };
    installWuuApi({ thread: idleThread, startTurn });
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();
    await act(async () => {
      const textarea = composerProbe().querySelector("textarea")!;
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(textarea, "pending request");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      composerProbe().querySelector("button")!.click();
    });
    await flushAsync();
    expect(startTurn).toHaveBeenCalledTimes(1);
    expect(composerProbe().dataset.running).toBe("false");
    expect(composerProbe().dataset.draftPending).toBe("true");
    expect(composerProbe().querySelector("textarea")?.value).toBe("pending request");
    await act(async () => {
      composerProbe().querySelector<HTMLButtonElement>('[aria-label="stop"]')!.click();
    });
    expect(window.wuu.interruptTurn).not.toHaveBeenCalled();
    expect(composerProbe().dataset.draftPending).toBe("true");
    expect(composerProbe().dataset.stopState).toBe("pending");

    await act(async () => {
      resolveStart({ turn: { id: "late-turn", status: "in_progress", items_view: "full", items: [] } });
    });
    await flushAsync();
    expect(window.wuu.interruptTurn).toHaveBeenCalledTimes(1);
    expect(composerProbe().dataset.running).toBe("true");
    expect(composerProbe().dataset.stopState).toBe("pending");
    await act(async () => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: workspace,
        message: { method: "turn/completed", params: { thread_id: threadID,
          turn: { id: "late-turn", status: "interrupted", items_view: "full", items: [] } } } });
    });
    expect(composerProbe().dataset.running).toBe("false");
    expect(composerProbe().dataset.stopState).toBeUndefined();
  });

  it("retains stop intent through a stale snapshot and allows retry after RPC failure", async () => {
    installWuuApi();
    window.wuu.interruptTurn = vi.fn().mockRejectedValueOnce(new Error("IPC unavailable")).mockResolvedValue({ ok: true });
    await act(async () => { root = createRoot(container); root.render(await createConversationTestView(threadID)); });
    await flushAsync();
    await act(async () => { composerProbe().querySelector<HTMLButtonElement>('[aria-label="stop"]')!.click(); });
    expect(composerProbe().dataset.stopState).toBe("retry");
    expect(composerProbe().dataset.running).toBe("true");
    await act(async () => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: workspace,
        message: { method: "thread/updated", params: { thread: runningThread() } } });
    });
    expect(composerProbe().dataset.stopState).toBe("retry");
    await act(async () => { composerProbe().querySelector<HTMLButtonElement>('[aria-label="stop"]')!.click(); });
    expect(window.wuu.interruptTurn).toHaveBeenCalledTimes(2);
    expect(composerProbe().dataset.stopState).toBe("pending");
    expect(composerProbe().dataset.running).toBe("true");
    await act(async () => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: workspace,
        message: { method: "turn/completed", params: { thread_id: threadID,
          turn: { ...runningThread().turns[0], status: "interrupted" } } } });
    });
    expect(composerProbe().dataset.stopState).toBeUndefined();
  });

  it("dequeues a message before restoring it for editing", async () => {
    const { queuedClientIDs, dequeuedClientIDs } = installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    const textarea = composerProbe().querySelector("textarea");
    const send = composerProbe().querySelector("button");
    await act(async () => {
      if (!textarea || !send) throw new Error("composer controls not rendered");
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(textarea, "queued follow-up");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      send.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();

    expect(queuedClientIDs).toHaveLength(1);
    expect(composerProbe().dataset.queuedIds).toBe(queuedClientIDs[0]);
    expect(composerProbe().querySelector("textarea")?.value).toBe("");
    expect(composerProbe().dataset.draftPending).toBe("false");
    await act(async () => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: workspace,
        message: { method: "thread/resumed", params: { thread: runningThread(),
          held_user_messages: [{ id: queuedClientIDs[0], thread_id: threadID, origin: "queue", prompt: "queued follow-up" }] } } } as ServerEvent);
    });
    expect(composerProbe().dataset.queuedIds).toBe(queuedClientIDs[0]);

    const edit = composerProbe().querySelector<HTMLButtonElement>(
      'button[aria-label="edit-first-queued"]',
    );
    await act(async () => {
      if (!edit) throw new Error("queued edit control not rendered");
      edit.click();
    });
    await flushAsync();

    expect(composerProbe().dataset.queuedIds).toBe("");
    expect(dequeuedClientIDs).toEqual([queuedClientIDs[0]]);
    expect(composerProbe().querySelector("textarea")?.value).toBe(
      "queued follow-up",
    );
  });

  it("starts a normal turn after the final answer is ready", async () => {
    const startTurn = vi.fn().mockResolvedValue({
      turn: {
        id: "turn-follow-up",
        status: "in_progress",
        items_view: "full",
        items: [],
      },
    });
    const queueTurn = vi.fn();
    installWuuApi({ thread: answerReadyThread(), startTurn, queueTurn });
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    const textarea = composerProbe().querySelector("textarea");
    const send = composerProbe().querySelector("button");
    await act(async () => {
      if (!textarea || !send) throw new Error("composer controls not rendered");
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(textarea, "start the next turn");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      send.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();

    expect(startTurn).toHaveBeenCalledWith(
      threadID,
      "start the next turn",
      [],
      [],
      undefined,
      undefined,
      undefined,
      { kind: "no_project", cwd: workspace },
      expect.any(String),
    );
    expect(queueTurn).not.toHaveBeenCalled();
    expect(composerProbe().dataset.queuedIds).toBe("");
  });

  it("retains the follow-up draft until turn/start is accepted", async () => {
    const followUpTurn = {
      id: "turn-follow-up",
      status: "in_progress" as const,
      items_view: "full" as const,
      items: [] as [],
    };
    let resolveStart: ((value: { turn: typeof followUpTurn }) => void) | undefined;
    const startTurn = vi.fn(
      () =>
        new Promise<{ turn: typeof followUpTurn }>((resolve) => {
          resolveStart = resolve;
        }),
    );
    const queueTurn = vi.fn();
    installWuuApi({ thread: answerReadyThread(), startTurn, queueTurn });
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    const textarea = composerProbe().querySelector("textarea");
    const send = composerProbe().querySelector("button");
    await act(async () => {
      if (!textarea || !send) throw new Error("composer controls not rendered");
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(textarea, "keep this sent");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      send.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();

    expect(startTurn).toHaveBeenCalledTimes(1);
    expect(queueTurn).not.toHaveBeenCalled();
    expect(composerProbe().querySelector("textarea")?.value).toBe("keep this sent");
    expect(composerProbe().dataset.draftPending).toBe("true");
    expect(composerProbe().dataset.queuedIds).toBe("");

    await act(async () => {
      resolveStart?.({ turn: followUpTurn });
      await Promise.resolve();
    });
    await flushAsync();
    expect(composerProbe().querySelector("textarea")?.value).toBe("");
  });

  it("does not restore the draft when turn/start times out after the real turn arrives", async () => {
    let rejectStart: ((reason: Error) => void) | undefined;
    const startTurn = vi.fn(
      () =>
        new Promise<{
          turn: { id: string; status: "in_progress"; items_view: "full"; items: [] };
        }>((_resolve, reject) => {
          rejectStart = reject;
        }),
    );
    installWuuApi({ thread: answerReadyThread(), startTurn });
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    const textarea = composerProbe().querySelector("textarea");
    const send = composerProbe().querySelector("button");
    await act(async () => {
      if (!textarea || !send) throw new Error("composer controls not rendered");
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(textarea, "keep this sent");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      send.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();
    expect(composerProbe().querySelector("textarea")?.value).toBe("keep this sent");

    await act(async () => {
      for (const handler of serverEventHandlers) {
        handler({
          kind: "notification",
          workdir: workspace,
          message: {
            method: "turn/started",
            params: {
              thread_id: threadID,
              turn: {
                id: "turn-follow-up",
                status: "in_progress",
                items_view: "full",
                items: [
                  {
                    id: "item-follow-up-user",
                    type: "user_message",
                    status: "completed",
                    text: "keep this sent",
                    source_id: vi.mocked(window.wuu.startTurn).mock.calls[0][8],
                  },
                ],
              },
            },
          },
        });
      }
    });
    expect(composerProbe().querySelector("textarea")?.value).toBe("");
    await act(async () => {
      rejectStart?.(new Error("rpc timeout: turn/start"));
      await Promise.resolve();
    });
    await flushAsync();

    expect(container.textContent).toContain("keep this sent");
    expect(composerProbe().querySelector("textarea")?.value).toBe("");
  });

  it("still restores the draft when turn/start fails before a real turn arrives", async () => {
    let rejectStart: ((reason: Error) => void) | undefined;
    const startTurn = vi.fn(
      () =>
        new Promise<{
          turn: { id: string; status: "in_progress"; items_view: "full"; items: [] };
        }>((_resolve, reject) => {
          rejectStart = reject;
        }),
    );
    installWuuApi({ thread: { ...runningThread(), status: "idle", turns: [] }, startTurn });
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    const textarea = composerProbe().querySelector("textarea");
    const send = composerProbe().querySelector("button");
    await act(async () => {
      if (!textarea || !send) throw new Error("composer controls not rendered");
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(textarea, "restore this send");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      send.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();
    expect(composerProbe().querySelector("textarea")?.value).toBe("restore this send");

    await act(async () => {
      rejectStart?.(new Error("rpc timeout: turn/start"));
      await Promise.resolve();
    });
    await flushAsync();

    expect(composerProbe().querySelector("textarea")?.value).toBe("restore this send");
    expect(composerProbe().dataset.running).toBe("false");
  });

  it("removes an already materialized queue entry after a missed start notification", async () => {
    const { queuedClientIDs } = installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    const textarea = composerProbe().querySelector("textarea");
    const send = composerProbe().querySelector("button");
    await act(async () => {
      if (!textarea || !send) throw new Error("composer controls not rendered");
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(textarea, "queued follow-up");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      send.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();

    expect(queuedClientIDs).toHaveLength(1);
    const queueID = queuedClientIDs[0];
    expect(composerProbe().dataset.queuedIds).toBe(queuedClientIDs[0]);
    expect(composerProbe().querySelector("textarea")?.value).toBe("");
    expect(composerProbe().dataset.draftPending).toBe("false");

    // Simulate the live turn/started notification being missed while the
    // thread is backgrounded. A later authoritative thread snapshot already
    // contains the queued user_message with the same source_id.
    await act(async () => {
      for (const handler of serverEventHandlers) {
        handler({
          kind: "notification",
          workdir: workspace,
          message: {
            method: "thread/updated",
            params: { thread: runningThread(queueID) },
          },
        } as ServerEvent);
      }
    });
    await flushAsync();

    expect(composerProbe().dataset.queuedIds).toBe("");
    expect(composerProbe().querySelector("textarea")?.value).toBe("");
    expect(composerProbe().dataset.draftPending).toBe("false");
  });

  it.each([
    { newerDraft: false, rejectReply: false },
    { newerDraft: false, rejectReply: true },
    { newerDraft: true, rejectReply: false },
    { newerDraft: true, rejectReply: true },
  ])("shows server-accepted queue input before a delayed RPC settles ($newerDraft, $rejectReply)", async ({ newerDraft, rejectReply }) => {
    let resolveQueue!: (value: Awaited<ReturnType<WuuDesktopApi["queueTurn"]>>) => void;
    let rejectQueue!: (error: Error) => void;
    const queueTurn = vi.fn<WuuDesktopApi["queueTurn"]>(() => new Promise<Awaited<ReturnType<WuuDesktopApi["queueTurn"]>>>((resolve, reject) => {
      resolveQueue = resolve; rejectQueue = reject;
    }));
    installWuuApi({ queueTurn });
    await act(async () => { root = createRoot(container); root.render(await createConversationTestView(threadID)); });
    await flushAsync();
    const setText = (text: string) => {
      const textarea = composerProbe().querySelector("textarea")!;
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(textarea, text);
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    };
    await act(async () => { setText("accepted follow-up"); });
    await act(async () => { composerProbe().querySelector<HTMLButtonElement>("button")!.click(); });
    expect(queueTurn).toHaveBeenCalledOnce();
    const id = queueTurn.mock.calls[0][3]!;
    expect(composerProbe().dataset.draftPending).toBe("true");
    expect(composerProbe().dataset.queuedIds).toBe("");
    expect(composerProbe().querySelector("textarea")?.value).toBe("accepted follow-up");
    await act(async () => { composerProbe().querySelector<HTMLButtonElement>("button")!.click(); });
    expect(queueTurn).toHaveBeenCalledOnce();
    const notify = (workdir = workspace, messageID = id, owner = threadID) => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir,
        message: { method: "turn/queued", params: {
          message: { id: messageID, thread_id: owner, origin: "queue", prompt: "accepted follow-up" },
        } } });
    };
    // A different source, conversation, or correlation ID cannot acknowledge this draft.
    await act(async () => { notify("/unrelated-runtime"); notify(workspace, id, "another-thread"); notify(workspace, "another-input"); });
    expect(composerProbe().dataset.draftPending).toBe("true");
    expect(composerProbe().querySelector("textarea")?.value).toBe("accepted follow-up");
    if (newerDraft) await act(async () => { setText("newer input"); });
    await act(async () => { notify(); notify(); });
    // The receipt is authoritative; the RPC remains unresolved throughout these assertions.
    expect(composerProbe().dataset.draftPending).toBe("false");
    expect(composerProbe().dataset.queuedIds?.split(",").filter(value => value === id)).toEqual([id]);
    expect(composerProbe().dataset.running).toBe("true");
    expect(composerProbe().querySelector("textarea")?.value).toBe(newerDraft ? "newer input" : "");
    const snapshot = getDesktopApplication().getSnapshot();
    expect(snapshot.submission.operations.find(operation => operation.messageID === id)?.phase).toBe("completed");
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
    await act(async () => {
      if (rejectReply) rejectQueue(new Error("rpc timeout: turn/queue"));
      else resolveQueue({ queued: { id, thread_id: threadID } });
    });
    expect(composerProbe().dataset.draftPending).toBe("false");
    expect(composerProbe().dataset.queuedIds?.split(",").filter(value => value === id)).toEqual([id]);
    expect(composerProbe().querySelector("textarea")?.value).toBe(newerDraft ? "newer input" : "");
    expect(getDesktopApplication().getSnapshot().submission.failedDraftTabIds).toEqual([]);
    expect(queueTurn).toHaveBeenCalledOnce();
  });

  it("acknowledges only the source draft after switching runtime contexts while the queue RPC is pending", async () => {
    let rejectQueue!: (error: Error) => void;
    const queueTurn = vi.fn<WuuDesktopApi["queueTurn"]>(() => new Promise((_resolve, reject) => { rejectQueue = reject; }));
    installWuuApi({ queueTurn });
    await act(async () => { root = createRoot(container); root.render(await createConversationTestView(threadID)); });
    await flushAsync();
    const application = getDesktopApplication();
    await act(async () => {
      await application.invoke("composer.set-draft", { text: "source input" });
      await application.invoke("composer.submit", { ownerTabId: `thread:${threadID}`, mode: "queue" });
    });
    expect(queueTurn).toHaveBeenCalledOnce();
    const id = queueTurn.mock.calls[0][3]!;
    const other = { ...runningThread(), id: "other-thread", cwd: "/tmp/other-runtime", status: "idle" as const, turns: [] };
    const context = { kind: "no_project" as const, cwd: other.cwd };
    await act(async () => {
      application.updateState(state => ({ ...state, activeContext: context, activeSessionTabID: `thread:${other.id}`,
        thread: other, threads: [...state.threads, other], sessionTabs: [...state.sessionTabs, {
          id: `thread:${other.id}`, kind: "thread", threadID: other.id, context, title: "Other", prompt: "other draft", images: [], files: [],
        }] }));
    });
    const notify = (workdir: string) => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir,
        message: { method: "turn/queued", params: {
          message: { id, thread_id: threadID, origin: "queue", prompt: "source input" },
        } } });
    };
    await act(async () => { notify(other.cwd); });
    expect(application.getSnapshot().submission.operations[0].draftPending).toBe(true);
    await act(async () => { notify(workspace); });
    expect(application.getSnapshot().submission.operations[0]).toMatchObject({ phase: "completed", draftPending: false });
    expect(application.getSnapshot().drafts.primary).toMatchObject({ ownerTabId: `thread:${other.id}`, text: "other draft" });
    expect(application.getSnapshot().tabs.items.find(tab => tab.id === `thread:${threadID}`)?.draftText).toBe("");
    await act(async () => { rejectQueue(new Error("rpc timeout: turn/queue")); });
    expect(application.getSnapshot().drafts.primary.text).toBe("other draft");
    expect(application.getSnapshot().submission.failedDraftTabIds).toEqual([]);
    expect(queueTurn).toHaveBeenCalledOnce();
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
  });

  it.each([false, true])("defers a second composer submission only until the queue acknowledgement (notification first=%s)", async notificationFirst => {
    let resolveQueue!: (value: Awaited<ReturnType<WuuDesktopApi["queueTurn"]>>) => void;
    const queueTurn = vi.fn(() => new Promise<Awaited<ReturnType<WuuDesktopApi["queueTurn"]>>>((resolve) => { resolveQueue = resolve; }));
    let resolveSteer: ((value: { turn_id: string }) => void) | undefined;
    const steerTurn = vi.fn(
      () =>
        new Promise<{ turn_id: string }>((resolve) => {
          resolveSteer = resolve;
        }),
    );
    // Recovery reads must agree with the notifications when reconciliation runs between sends.
    let storedThread = runningThread();
    installWuuApi({ steerTurn: steerTurn as WuuDesktopApi["steerTurn"], queueTurn,
      resumeThread: vi.fn(async () => ({ thread: storedThread })),
    });
    vi.mocked(window.wuu.listThreads).mockImplementation(async () => ({ threads: [storedThread] }));
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    const textarea = composerProbe().querySelector("textarea");
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(textarea, "queued follow-up");
      textarea!.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => { composerProbe().querySelector<HTMLButtonElement>("button")!.click(); });
    expect(queueTurn).toHaveBeenCalledTimes(1);
    const steer = composerProbe().querySelector<HTMLButtonElement>(
      'button[aria-label="steer"]',
    );
    await act(async () => {
      if (!textarea || !steer) throw new Error("steer controls not rendered");
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(textarea, "guide the running turn");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      steer.click();
      await Promise.resolve();
    });

    expect(steerTurn).not.toHaveBeenCalled();
    expect(composerProbe().querySelector("textarea")?.value).toBe("guide the running turn");
    expect(composerProbe().dataset.guideIds).toBe("");
    const queuedID = vi.mocked(window.wuu.queueTurn).mock.calls[0][3]!;
    const notifyQueued = () => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: workspace,
        message: { method: "turn/queued", params: { thread_id: threadID,
          message: { id: queuedID, thread_id: threadID, origin: "queue", prompt: "queued follow-up" } } } } as ServerEvent);
    };
    if (notificationFirst) await act(async () => { notifyQueued(); });
    await act(async () => {
      resolveQueue({ queued: { id: queuedID, thread_id: threadID } });
    });
    if (!notificationFirst) await act(async () => { notifyQueued(); });
    expect(composerProbe().dataset.queuedIds).toBe(queuedID);
    expect(composerProbe().dataset.draftPending).toBe("false");
    expect(composerProbe().dataset.running).toBe("true");
    // Queue acknowledgement must not wait for another turn or clear newer text.
    expect(composerProbe().querySelector("textarea")?.value).toBe("guide the running turn");
    await act(async () => { steer!.click(); });
    expect(steerTurn).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolveSteer?.({ turn_id: "turn-queued" });
      const item = { id: "accepted-guide", type: "user_message" as const, status: "completed" as const, text: "guide the running turn", source_id: vi.mocked(window.wuu.steerTurn).mock.calls[0][4] };
      storedThread = { ...storedThread, turns: storedThread.turns.map(turn => ({ ...turn, items: [...turn.items, item] })) };
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: workspace,
        message: { method: "item/completed", params: { thread_id: threadID, turn_id: storedThread.turns[0].id, item } } } as ServerEvent);
    });
    expect(composerProbe().querySelector("textarea")?.value).toBe("");
  });

  it.each([false, true])("hands off a guide before materialization and preserves a later unknown-delivery barrier (notification first=%s)", async notificationFirst => {
    let acknowledge!: (value: Awaited<ReturnType<WuuDesktopApi["steerTurn"]>>) => void;
    const steerTurn = vi.fn(() => new Promise<Awaited<ReturnType<WuuDesktopApi["steerTurn"]>>>(resolve => { acknowledge = resolve; }));
    installWuuApi({ steerTurn });
    await act(async () => { root = createRoot(container); root.render(await createConversationTestView(threadID)); });
    await flushAsync();
    const textarea = composerProbe().querySelector("textarea")!;
    const setText = (text: string) => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(textarea, text);
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    };
    await act(async () => { setText("guide now"); });
    await act(async () => { composerProbe().querySelector<HTMLButtonElement>('[aria-label="steer"]')!.click(); });
    expect(steerTurn).toHaveBeenCalledOnce();
    const id = vi.mocked(window.wuu.steerTurn).mock.calls[0][4]!;
    expect(composerProbe().dataset.draftPending).toBe("true");
    await act(async () => { setText("newer draft"); });
    const notify = (method: string, params: Record<string, unknown>) => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: workspace, message: { method, params } } as ServerEvent);
    };
    const message = { id, thread_id: threadID, origin: "steer", prompt: "guide now" };
    if (notificationFirst) await act(async () => { notify("turn/steered", { message }); });
    await act(async () => { acknowledge({ turn_id: "turn-current", delivery: "active_turn" }); });
    if (!notificationFirst) await act(async () => { notify("turn/steered", { message }); });
    expect(composerProbe().dataset.draftPending).toBe("false");
    expect(composerProbe().dataset.guideIds).toBe(id);
    expect(composerProbe().dataset.running).toBe("true");
    expect(textarea.value).toBe("newer draft");
    await act(async () => { notify("turn/held", { thread_id: threadID, messages: [{ ...message, delivery_unconfirmed: true }] }); });
    expect(composerProbe().dataset.guideIds).toBe(id);
    expect(composerProbe().dataset.unconfirmedIds).toBe(id);
    expect(composerProbe().dataset.draftPending).toBe("false");
    expect(textarea.value).toBe("newer draft");
    expect(steerTurn).toHaveBeenCalledOnce();
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();
  });

  it("rolls back an optimistic steer and restores the draft on IPC failure", async () => {
    let rejectSteer: ((reason: Error) => void) | undefined;
    const steerTurn = vi.fn(
      () =>
        new Promise<{ turn_id: string }>((_resolve, reject) => {
          rejectSteer = reject;
        }),
    );
    installWuuApi({ steerTurn: steerTurn as WuuDesktopApi["steerTurn"] });
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    const textarea = composerProbe().querySelector("textarea");
    const steer = composerProbe().querySelector<HTMLButtonElement>(
      'button[aria-label="steer"]',
    );
    await act(async () => {
      if (!textarea || !steer) throw new Error("steer controls not rendered");
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(textarea, "restore this guide");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
      steer.click();
      await Promise.resolve();
    });
    expect(composerProbe().dataset.guideIds).toBe("");
    expect(composerProbe().querySelector("textarea")?.value).toBe("restore this guide");

    await act(async () => {
      rejectSteer?.(new Error("IPC unavailable"));
      await Promise.resolve();
    });
    await flushAsync();

    expect(composerProbe().dataset.guideIds).toBe("");
    expect(composerProbe().querySelector("textarea")?.value).toBe(
      "restore this guide",
    );
  });

  it("restores the authoritative held order from a resumed thread notification", async () => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    await act(async () => {
      for (const handler of serverEventHandlers) {
        handler({
          kind: "notification",
          workdir: workspace,
          message: {
            method: "thread/resumed",
            params: {
              thread: runningThread(),
              held_user_messages: [
                {
                  id: "guide-1",
                  thread_id: threadID,
                  origin: "steer",
                  prompt: "Guide",
                },
                {
                  id: "queue-1",
                  thread_id: threadID,
                  origin: "queue",
                  prompt: "First",
                },
                {
                  id: "queue-2",
                  thread_id: threadID,
                  origin: "queue",
                  prompt: "Second",
                },
              ],
            },
          },
        } as ServerEvent);
      }
    });
    await flushAsync();

    expect(composerProbe().dataset.guideIds).toBe("guide-1");
    expect(composerProbe().dataset.queuedIds).toBe("queue-1,queue-2");
    expect(composerProbe().dataset.heldOrder).toBe("guide-1,queue-1,queue-2");
  });

  it("restores held and queued messages from the resume result on boot", async () => {
    // Reload simulation: the renderer boots fresh and never receives the
    // `thread/resumed` notification (the active-context gate drops server
    // events until the runtime state is loaded). The queue must survive
    // through the resume RPC result alone.
    installWuuApi({
      resumeThread: vi.fn().mockResolvedValue({
        thread: runningThread(),
        held_user_messages: [
          {
            id: "queue-1",
            thread_id: threadID,
            origin: "queue",
            prompt: "First",
          },
          {
            id: "guide-1",
            thread_id: threadID,
            origin: "steer",
            prompt: "Guide",
          },
          {
            id: "queue-2",
            thread_id: threadID,
            origin: "queue",
            prompt: "Second",
          },
        ],
      }),
    });
    await act(async () => {
      root = createRoot(container);
      root.render(await createConversationTestView(threadID));
    });
    await flushAsync();

    expect(composerProbe().dataset.guideIds).toBe("guide-1");
    expect(composerProbe().dataset.queuedIds).toBe("queue-1,queue-2");
    expect(composerProbe().dataset.heldOrder).toBe("queue-1,guide-1,queue-2");
  });
});
