/**
 * Regression test for the empty conversation home: the "跳到最新" control is
 * only meaningful when a transcript exists. The App layer owns that context,
 * so it should not mount the control while rendering the empty home.
 */
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  InitializeResult,
  ServerEvent,
  Thread,
  WuuDesktopApi,
} from "../shared/protocol";

vi.mock("./JumpToLatestPill", () => ({
  JumpToLatestPill: (): JSX.Element => (
    <div data-testid="jump-to-latest-probe" />
  ),
}));

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
  WorkspaceMonacoEditor: (): JSX.Element => (
    <div className="workspace-monaco-editor" data-testid="mock-monaco-editor" />
  ),
}));

import { createDefaultApplicationTestView } from "./test-support/DefaultApplication";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];

const workspace = "/tmp/wuu-jump-to-latest-empty-state-test";

function draftThread(): Thread {
  return {
    id: "draft-thread-1",
    preview: "New conversation",
    title: "",
    model_provider: "fake",
    model: "fake-model",
    cwd: workspace,
    workspace_kind: "scratch",
    status: "idle",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
}

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

function installWuuApi(): void {
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
    listThreads: vi.fn().mockResolvedValue({ threads: [] }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread: vi.fn().mockResolvedValue({ thread: undefined }),
    startThread: vi.fn().mockResolvedValue({ thread: draftThread() }),
    deleteThread: vi.fn().mockResolvedValue(undefined),
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
}

async function flushAsync(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("jump-to-latest empty state", () => {
  beforeEach(() => {
    installWindowStubs();
    installWuuApi();
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
  });

  it("does not mount the jump control on the empty conversation home", async () => {
    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();

    expect(container.querySelector(".empty-home")).not.toBeNull();
    expect(
      container.querySelector('[data-testid="jump-to-latest-probe"]'),
    ).toBeNull();
  });
});
