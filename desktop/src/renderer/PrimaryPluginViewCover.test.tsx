/**
 * A primary plugin page shares the conversation pane. With an empty-session
 * wallpaper the page is transparent, so the greeting and dock composer must
 * leave the pane instead of painting through Automations.
 */
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InitializeResult, ServerEvent, Thread, WuuDesktopApi } from "../shared/protocol";

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
  WorkspaceMonacoEditor: (): JSX.Element => <div data-testid="mock-monaco-editor" />,
}));

import { App } from "./App";
import { desktopPluginHost, desktopWorkbenchController } from "./plugins/DesktopPluginRuntime";

const pluginId = "test:primary-cover";
let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];

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
  const workspace = "/tmp/wuu-primary-plugin-cover";
  const thread: Thread = {
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
  const initialized = (): InitializeResult => ({
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
  });
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
    startThread: vi.fn().mockResolvedValue({ thread }),
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

describe("primary plugin view covers the conversation", () => {
  beforeEach(() => {
    installWindowStubs();
    installWuuApi();
    serverEventHandlers = [];
    window.localStorage.clear();
    container = document.createElement("div");
    document.body.appendChild(container);
  });

  afterEach(() => {
    act(() => {
      for (const view of desktopWorkbenchController.getSnapshot().views) {
        void desktopWorkbenchController.closeView(view.id);
      }
      desktopPluginHost.unload(pluginId);
      root?.unmount();
    });
    root = null;
    container.remove();
    window.localStorage.clear();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  });

  it("drops the empty-session greeting and dock composer while Automations is open", async () => {
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    expect(container.querySelector(".empty-home")).not.toBeNull();
    expect(container.querySelector(".conversation-pane > .dock-composer-wrap")).not.toBeNull();

    await act(async () => {
      await desktopPluginHost.activateGeneration({
        pluginId,
        generation: "one",
        register(api) {
          api.registerViewType({
            id: "catalog",
            title: "Automations",
            render: () => <article>Automation catalog</article>,
          });
        },
      });
      await desktopWorkbenchController.openPluginView(pluginId, "catalog");
    });

    const pane = container.querySelector(".conversation-pane");
    expect(pane?.hasAttribute("data-primary-plugin-view")).toBe(true);
    expect(pane?.querySelector(".plugin-workbench-view-primary")?.textContent).toContain("Automation catalog");
    expect(pane?.querySelector(".empty-home")).toBeNull();
    expect(pane?.querySelector(".dock-composer-wrap")).toBeNull();
    expect(pane?.querySelector(".scroll-region")?.hasAttribute("inert")).toBe(true);

    await act(async () => {
      desktopWorkbenchController.deactivateRegion("primary");
    });

    expect(pane?.hasAttribute("data-primary-plugin-view")).toBe(false);
    expect(pane?.querySelector(".empty-home")).not.toBeNull();
    expect(pane?.querySelector(".dock-composer-wrap")).not.toBeNull();
    expect(pane?.querySelector(".scroll-region")?.hasAttribute("inert")).toBe(false);
  });
});
