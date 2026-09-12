import { act, useEffect, useState, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  DesktopProject,
  InitializeResult,
  RuntimeContext,
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
      const variant = props.variant ?? "dock";
      // Match the real Composer's input-critical local value. App publishes
      // textarea drafts after an idle window, so a mock that reads only the
      // parent prop cannot exercise immediate Enter or slash commands.
      const [prompt, setLocalPrompt] = useState(props.prompt);
      useEffect(
        () => setLocalPrompt(props.prompt),
        [props.prompt, props.promptRevision],
      );
      const label = props.mainConversation
        ? `main composer ${variant}`
        : "side composer";
      return (
        <div
          data-can-select-project={props.canSelectProject}
          data-main-conversation-composer={
            props.mainConversation ? variant : undefined
          }
        >
          <textarea
            aria-label={label}
            value={prompt}
            onChange={(event) => {
              const value = event.currentTarget.value;
              setLocalPrompt(value);
              props.setPrompt(value);
            }}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return;
              event.preventDefault();
              if (prompt.trim() === "/new") {
                props.onStartNewThread();
              } else if (prompt.trim() === "/side") {
                props.onOpenSideThread?.();
              } else {
                props.onSend(prompt);
              }
            }}
          />
        </div>
      );
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

import { App } from "./App";

const scratchCwd = "/tmp/wuu-composer-focus/scratch";
const projectCwd = "/tmp/wuu-composer-focus/project";
const project: DesktopProject = {
  id: "project-focus",
  name: "Focus Project",
  path: projectCwd,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];
let releaseProjectSelection: (() => void) | null = null;
let releaseThreadStart: (() => void) | null = null;

function initialized(cwd: string): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    variant: "high",
    effort: "high",
    workspace_root: cwd,
    permissions: { mode: "standard" },
    providers: [
      {
        name: "fake",
        type: "openai-compatible",
        model: "fake-model",
        api_key_configured: true,
      },
    ],
    advanced_settings: {
      max_steps: 64,
      max_context_tokens: 0,
      temperature: 0,
      disable_auto_compact: false,
    },
  };
}

function persistedThread(): Thread {
  return {
    id: "thread-focus",
    preview: "focus continuity",
    model_provider: "fake",
    model: "fake-model",
    cwd: scratchCwd,
    workspace_kind: "scratch",
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    turns: [
      {
        id: "turn-existing",
        status: "completed",
        items_view: "full",
        items: [
          {
            id: "item-existing",
            type: "user_message",
            status: "completed",
            text: "existing query",
          },
        ],
      },
    ],
  };
}

function newThread(): Thread {
  return {
    id: "thread-new",
    preview: "new query",
    model_provider: "fake",
    model: "fake-model",
    cwd: scratchCwd,
    workspace_kind: "scratch",
    status: "idle",
    created_at: "2026-01-03T00:00:00Z",
    updated_at: "2026-01-03T00:00:00Z",
    turns: [],
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
  Object.defineProperty(window, "requestAnimationFrame", {
    configurable: true,
    value: (callback: FrameRequestCallback) => {
      window.setTimeout(() => callback(0), 0);
      return 1;
    },
  });
}

function installWuuApi(
  options: {
    withThread?: boolean;
    deferProjectSelection?: boolean;
    rejectProjectSelection?: boolean;
    rejectNoProjectSelection?: boolean;
    deferThreadStart?: boolean;
    rejectThreadStart?: boolean;
    rejectTurnStart?: boolean;
  } = {},
): void {
  let activeContext: RuntimeContext = { kind: "no_project", cwd: scratchCwd };
  const thread = persistedThread();
  const api = {
    listProjects: vi.fn().mockImplementation(() =>
      Promise.resolve({ projects: [project], active_context: activeContext }),
    ),
    selectProject: vi.fn().mockImplementation(async () => {
      if (options.deferProjectSelection) {
        await new Promise<void>((resolve) => {
          releaseProjectSelection = resolve;
        });
      }
      if (options.rejectProjectSelection) {
        throw new Error("project selection failed");
      }
      activeContext = {
        kind: "project",
        project_id: project.id,
        cwd: projectCwd,
      };
      return { projects: [project], active_context: activeContext };
    }),
    selectNoProject: vi.fn().mockImplementation(() => {
      if (options.rejectNoProjectSelection) {
        return Promise.reject(new Error("no-project selection failed"));
      }
      activeContext = { kind: "no_project", cwd: scratchCwd };
      return Promise.resolve({ projects: [project], active_context: activeContext });
    }),
    initialize: vi.fn().mockImplementation(() =>
      Promise.resolve(initialized(activeContext.cwd)),
    ),
    listThreads: vi.fn().mockImplementation(() =>
      Promise.resolve({
        threads:
          options.withThread && activeContext.kind === "no_project" ? [thread] : [],
      }),
    ),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    listChannelRooms: vi.fn().mockResolvedValue({ rooms: [] }),
    resumeThread: vi.fn().mockResolvedValue({ thread }),
    startThread: vi.fn().mockImplementation(async () => {
      if (options.deferThreadStart) {
        await new Promise<void>((resolve) => {
          releaseThreadStart = resolve;
        });
      }
      if (options.rejectThreadStart) {
        throw new Error("thread start failed");
      }
      return { thread: newThread() };
    }),
    startTurn: options.rejectTurnStart
      ? vi.fn().mockRejectedValue(new Error("turn start failed"))
      : vi.fn().mockResolvedValue({
          turn: {
            id: "turn-started",
            status: "in_progress",
            items_view: "full",
            items: [],
          },
        }),
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({
      is_repo: false,
      dirty_count: 0,
      files: [],
    }),
    openSideThread: vi.fn().mockResolvedValue({ summary: null }),
    getSideThreadHistory: vi.fn().mockResolvedValue(null),
    sendSideThreadMessage: vi.fn().mockResolvedValue({
      user_message_id: "side-user",
      summary: null,
    }),
    interruptSideThread: vi.fn().mockResolvedValue({ ok: true }),
    resetSideThread: vi.fn().mockResolvedValue({ ok: true }),
    onSideThreadEvent: vi.fn(() => () => {}),
    onServerEvent: vi.fn((handler: (event: ServerEvent) => void) => {
      serverEventHandlers.push(handler);
      return () => {
        serverEventHandlers = serverEventHandlers.filter(
          (candidate) => candidate !== handler,
        );
      };
    }),
    onWindowResizeState: vi.fn(() => () => {}),
    onTerminalEvent: vi.fn(() => () => {}),
    respondToServerRequest: vi.fn().mockResolvedValue(undefined),
    rejectServerRequest: vi.fn().mockResolvedValue(undefined),
  } as unknown as WuuDesktopApi;
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
}

async function flushAsync(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await new Promise((resolve) => window.setTimeout(resolve, 0));
    await Promise.resolve();
  });
}

async function renderApp(
  withThread: boolean,
  options: {
    deferProjectSelection?: boolean;
    rejectProjectSelection?: boolean;
    rejectNoProjectSelection?: boolean;
    deferThreadStart?: boolean;
    rejectThreadStart?: boolean;
    rejectTurnStart?: boolean;
  } = {},
): Promise<void> {
  installWuuApi({ withThread, ...options });
  await act(async () => {
    root = createRoot(container);
    root.render(<App />);
  });
  await flushAsync();
}

function mainComposer(variant: "dock"): HTMLTextAreaElement {
  const textarea = container.querySelector<HTMLTextAreaElement>(
    `textarea[aria-label="main composer ${variant}"]`,
  );
  if (!textarea) throw new Error(`${variant} composer not rendered`);
  return textarea;
}

async function waitForMainComposerFocus(
  variant: "dock",
): Promise<void> {
  await act(async () => {
    await vi.waitFor(() => {
      expect(document.activeElement).toBe(mainComposer(variant));
    });
  });
}

async function waitForSideComposerFocus(): Promise<void> {
  await act(async () => {
    await vi.waitFor(() => {
      const side = container.querySelector<HTMLTextAreaElement>(
        'textarea[aria-label="side composer"]',
      );
      expect(side).not.toBeNull();
      expect(document.activeElement).toBe(side);
    });
  });
}

async function enterCommand(
  textarea: HTMLTextAreaElement,
  command: string,
): Promise<void> {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    setter?.call(textarea, command);
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => {
    textarea.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
    );
  });
  await flushAsync();
}

describe("main composer focus continuity", () => {
  const originalWindowWidth = window.innerWidth;

  beforeEach(() => {
    installWindowStubs();
    serverEventHandlers = [];
    releaseProjectSelection = null;
    releaseThreadStart = null;
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
  });

  afterEach(() => {
    act(() => root?.unmount());
    Object.defineProperty(window, "innerWidth", { configurable: true, value: originalWindowWidth });
    root = null;
    container.remove();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  });

  it("focuses the dock composer after /new", async () => {
    await renderApp(true);
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "/new");

    await waitForMainComposerFocus("dock");
  });

  it("focuses the dock composer from the new-tab button", async () => {
    await renderApp(true);
    const button = container.querySelector<HTMLButtonElement>(
      'button.session-tab-new[aria-label="新建对话"]',
    );
    if (!button) throw new Error("new-tab button not rendered");
    button.focus();

    await act(async () => button.click());
    await flushAsync();

    await waitForMainComposerFocus("dock");
  });

  it("clears an unpublished local draft when switching to an empty new conversation", async () => {
    await renderApp(true);
    const dock = mainComposer("dock");
    const setter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      setter?.call(dock, "draft newer than App");
      dock.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(dock.value).toBe("draft newer than App");

    const button = container.querySelector<HTMLButtonElement>(
      'button.session-tab-new[aria-label="新建对话"]',
    );
    if (!button) throw new Error("new-tab button not rendered");
    await act(async () => button.click());
    await flushAsync();

    expect(mainComposer("dock").value).toBe("");
  });

  it("focuses the dock composer from a project's new-conversation button", async () => {
    await renderApp(true);
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建会话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();

    await act(async () => button.click());
    await flushAsync();

    await waitForMainComposerFocus("dock");
  });

  it("waits for the destination dock before focusing across projects", async () => {
    await renderApp(false, { deferProjectSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建会话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();

    await act(async () => button.click());
    expect(document.activeElement).toBe(button);

    if (!releaseProjectSelection) {
      throw new Error("project selection was not deferred");
    }
    releaseProjectSelection();
    await flushAsync();

    await waitForMainComposerFocus("dock");
  });

  it("does not focus the old dock when project selection fails", async () => {
    await renderApp(false, { rejectProjectSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建会话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();

    await act(async () => button.click());
    await flushAsync();

    expect(document.activeElement).toBe(button);
  });

  it("does not focus the old dock when no-project selection fails", async () => {
    await renderApp(false, { rejectNoProjectSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 对话 中新建会话"]',
    );
    if (!button) throw new Error("scratch new-conversation button not rendered");
    button.focus();

    await act(async () => button.click());
    await flushAsync();

    expect(document.activeElement).toBe(button);
  });

  it("does not steal focus changed during an asynchronous project switch", async () => {
    await renderApp(false, { deferProjectSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建会话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();
    await act(async () => button.click());

    const other = document.createElement("button");
    document.body.appendChild(other);
    other.focus();
    if (!releaseProjectSelection) {
      throw new Error("project selection was not deferred");
    }
    releaseProjectSelection();
    await flushAsync();

    expect(document.activeElement).toBe(other);
    other.remove();
  });

  it("does not steal focus after a non-focusable user interaction", async () => {
    await renderApp(false, { deferProjectSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建会话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();
    await act(async () => button.click());

    const surface = document.createElement("div");
    document.body.appendChild(surface);
    surface.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    button.blur();
    if (!releaseProjectSelection) {
      throw new Error("project selection was not deferred");
    }
    releaseProjectSelection();
    await flushAsync();

    expect(document.activeElement).not.toBe(mainComposer("dock"));
    surface.remove();
  });

  it("focuses the side composer after /side", async () => {
    await renderApp(true);
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "/side");

    await waitForSideComposerFocus();
  });

  it.each([
    [390, false], [390, true], [1440, false], [1440, true],
  ] as const)("keeps the composer mounted at width=%s when the first send fails=%s", async (width, rejectThreadStart) => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: width });
    await renderApp(false, { rejectThreadStart });
    const dock = mainComposer("dock");
    expect(dock.parentElement?.dataset.canSelectProject).toBe("true");
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
    dock.focus();

    await enterCommand(dock, "first compact query");

    expect(mainComposer("dock")).toBe(dock);
    expect(document.activeElement).toBe(dock);
    expect(dock.value).toBe(rejectThreadStart ? "first compact query" : "");
    expect(dock.parentElement?.dataset.canSelectProject).toBe(String(rejectThreadStart));
    if (!rejectThreadStart) {
      expect(window.wuu.startTurn).toHaveBeenCalled();
    }
  });

  it("focuses the bottom composer after /new in a compact window", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 390 });
    await renderApp(true);
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "/new");

    await waitForMainComposerFocus("dock");
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
    expect(mainComposer("dock").value).toBe("");
  });

  it("keeps focus in the bottom composer on the first query", async () => {
    await renderApp(false);
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "first query");

    await waitForMainComposerFocus("dock");
    expect(window.wuu.startThread).toHaveBeenCalledWith({
      provider: "fake",
      model: "fake-model",
      effort: "high",
      permission_mode: "standard",
    });
    expect(window.wuu.startTurn).toHaveBeenCalled();
  });

  it("shows the first query while thread creation is still pending", async () => {
    await renderApp(false, { deferThreadStart: true });
    const dock = mainComposer("dock");

    await enterCommand(dock, "first query before thread start");

    expect(container.textContent).toContain("first query before thread start");
    expect(container.querySelector(".turn-process-title")?.textContent).toBe(
      "正在处理",
    );
    expect(mainComposer("dock")).not.toBeNull();
    expect(window.wuu.startTurn).not.toHaveBeenCalled();

    if (!releaseThreadStart) {
      throw new Error("thread start was not deferred");
    }
    releaseThreadStart();
    await flushAsync();
  });

  it("removes the pending query and restores the dock draft when thread creation fails", async () => {
    await renderApp(false, { rejectThreadStart: true });
    const dock = mainComposer("dock");

    await enterCommand(dock, "query that fails before thread start");

    expect(mainComposer("dock").value).toBe("query that fails before thread start");
    expect(container.querySelector(".turn-process-title")).toBeNull();
  });

  it("restores focus to the dock when the first query fails", async () => {
    await renderApp(false, { rejectTurnStart: true });
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "first query");

    await waitForMainComposerFocus("dock");
  });

  it("does not hand focus to the dock after the user focuses another control", async () => {
    await renderApp(false);
    const dock = mainComposer("dock");
    const sentinel = document.createElement("button");
    container.appendChild(sentinel);
    dock.focus();

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      setter?.call(dock, "first query");
      dock.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      dock.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
      sentinel.focus();
    });
    await flushAsync();

    expect(document.activeElement).toBe(sentinel);
  });
});
