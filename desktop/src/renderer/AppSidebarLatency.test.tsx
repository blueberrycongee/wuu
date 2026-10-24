/**
 * Sidebar click latency regression test (coordinator task A).
 *
 * Root cause under test: CachedConversationPanes is wrapped in
 * React.memo, but App.tsx passed it freshly-created arrow functions
 * (onOpenAgent / onOpenFileDiff /
 * onDismissContextComposition / onOpenSubthread) on every render, so
 * the memo bailout never fired and EVERY sidebar interaction — even a
 * pure section collapse — re-rendered the full conversation turn list
 * (markdown and all). That re-render is the perceived click lag.
 *
 * The test mounts the real App with a stub API, mounts the pane once by
 * selecting a thread, then toggles the Agents section header — a
 * sidebar-only state change — and asserts the turn list does NOT render
 * again.
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

const turnListProbe = vi.hoisted(() => ({ renders: 0 }));

vi.mock("./ConversationTurnList", () => ({
  ConversationTurnList: (): JSX.Element => {
    turnListProbe.renders += 1;
    return <div data-testid="turn-list-probe" />;
  },
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

const workspace = "/tmp/wuu-latency-test";

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

function completedThread(): Thread {
  return {
    id: "thread-latency",
    preview: "latency probe session",
    model_provider: "fake",
    model: "fake-model",
    cwd: workspace,
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [
      {
        id: "turn-1",
        items_view: "full",
        status: "completed",
        items: [
          { id: "item-1", type: "user_message", text: "hello" },
          { id: "item-2", type: "agent_message", text: "world" },
        ],
      },
    ],
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
  const thread = completedThread();
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
    listThreads: vi.fn().mockResolvedValue({ threads: [thread] }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread: vi.fn().mockResolvedValue({ thread }),
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

describe("sidebar click latency", () => {
  beforeEach(() => {
    installWindowStubs();
    serverEventHandlers = [];
    turnListProbe.renders = 0;
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

  it("does not re-render the conversation turn list on a sidebar-only click", async () => {
    installWuuApi();

    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();

    // Activate the thread so the cached conversation pane mounts.
    const row = Array.from(
      container.querySelectorAll<HTMLButtonElement>(".thread-row-main"),
    ).find((button) => button.textContent?.includes("latency probe session"));
    expect(row).toBeDefined();
    await act(async () => {
      row?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();
    expect(
      container.querySelector('[data-testid="turn-list-probe"]'),
    ).not.toBeNull();
    expect(turnListProbe.renders).toBeGreaterThan(0);

    const baseline = turnListProbe.renders;

    // A pure sidebar interaction: collapse + expand the scratch section.
    // This flips App-local state (collapsedSidebarSectionIDs) and must not
    // cascade into the memoized conversation pane.
    const scratchHeader = container.querySelector<HTMLButtonElement>(
      'section[data-section-id="__wuu_scratch__"] .sidebar-section-row',
    );
    expect(scratchHeader).not.toBeNull();
    await act(async () => {
      scratchHeader?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();
    await act(async () => {
      scratchHeader?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();

    // The memoized conversation pane should not re-render from a sidebar-only
    // state change. Allow a single async flush render; the original regression
    // cascaded into many extra renders here.
    expect(turnListProbe.renders).toBeLessThanOrEqual(baseline + 1);
  });
});
