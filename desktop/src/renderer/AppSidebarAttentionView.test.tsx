/**
 * Sidebar attention-view persistence regression test.
 *
 * User report: clicking the sidebar bell switches the sidebar to the attention
 * view (running + unread conversations). Opening Settings from the sidebar
 * footer and leaving it dropped the user back on the session list. Settings
 * replaces the whole workbench tree, so the bell flag used to die with the
 * sidebar component; App now owns it and must hand the user back the view they
 * left.
 *
 * The settings page itself is stubbed: this test is about the shell swap, not
 * about settings content, and the real page needs host APIs jsdom cannot serve.
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

vi.mock("./SettingsView", () => ({
  SettingsView: (): JSX.Element => <div data-testid="settings-view" />,
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

import { App } from "./App";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];
const originalInnerWidth = window.innerWidth;

const workspace = "/tmp/wuu-attention-view-test";

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

function runningThread(): Thread {
  return {
    id: "thread-running",
    preview: "还在跑的会话",
    title: "还在跑的会话",
    model_provider: "fake",
    model: "fake-model",
    cwd: workspace,
    workspace_kind: "scratch",
    status: "in_progress",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
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
}

function installWuuApi(threads: Thread[]): void {
  const projectState = () => ({
    projects: [],
    active_context: { kind: "no_project" as const, cwd: workspace },
  });
  const api = {
    listProjects: vi.fn().mockImplementation(() => Promise.resolve(projectState())),
    selectNoProject: vi.fn().mockImplementation(() => Promise.resolve(projectState())),
    initialize: vi.fn().mockResolvedValue(initialized()),
    listThreads: vi.fn().mockResolvedValue({ threads }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread: vi.fn().mockImplementation((threadID?: string) => {
      const thread = threads.find((item) => item.id === threadID) ?? threads[0];
      return Promise.resolve({ thread });
    }),
    startThread: vi.fn().mockResolvedValue({ thread: runningThread() }),
    deleteThread: vi.fn().mockResolvedValue(undefined),
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({ is_repo: false, dirty_count: 0, files: [] }),
    onServerEvent: vi.fn((handler: (event: ServerEvent) => void) => {
      serverEventHandlers.push(handler);
      return () => {
        serverEventHandlers = serverEventHandlers.filter((item) => item !== handler);
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
    await Promise.resolve();
  });
}

function attentionView(): HTMLElement | null {
  return container.querySelector<HTMLElement>(".sidebar-unread-view");
}

function settingsView(): HTMLElement | null {
  return container.querySelector<HTMLElement>('[data-testid="settings-view"]');
}

describe("sidebar attention view", () => {
  beforeEach(() => {
    window.innerWidth = 1280;
    delete document.documentElement.dataset.hostKind;
    installWindowStubs();
    serverEventHandlers = [];
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
    installWuuApi([runningThread()]);
  });

  afterEach(() => {
    window.innerWidth = originalInnerWidth;
    delete document.documentElement.dataset.hostKind;
    act(() => {
      root?.unmount();
    });
    root = null;
    container.remove();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  });

  it("survives a trip through settings", async () => {
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    const bell = container.querySelector<HTMLButtonElement>(".sidebar-notifications-button");
    expect(bell).not.toBeNull();
    act(() => bell!.click());
    expect(attentionView()).not.toBeNull();

    const accountTrigger = container.querySelector<HTMLButtonElement>(".sidebar-account-trigger");
    expect(accountTrigger?.disabled).toBe(false);
    act(() => accountTrigger!.click());
    const settingsItem = container.querySelector<HTMLButtonElement>(
      '[role="menuitem"][data-settings-page="providers"]',
    );
    expect(settingsItem).not.toBeNull();
    // The settings page is a lazy import behind Suspense, so let it resolve.
    await act(async () => settingsItem!.click());
    await flushAsync();
    // Settings replaces the workbench: the sidebar is gone while it is open.
    expect(settingsView()).not.toBeNull();
    expect(container.querySelector(".app-shell")).toBeNull();

    act(() => {
      window.dispatchEvent(new Event("wuu:workbench-back"));
    });

    expect(settingsView()).toBeNull();
    expect(attentionView()).not.toBeNull();
  });
});
