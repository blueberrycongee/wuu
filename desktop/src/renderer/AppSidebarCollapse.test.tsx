/**
 * Sidebar collapse-state independence regression test.
 *
 * User report: collapsing 置顶, then interacting with a project, made
 * 置顶 passively expand. Root cause: the App effect that prunes
 * collapsedSidebarSectionIDs against state.projects stripped pseudo-section
 * keys whenever the project list got a fresh array identity (any runtime
 * reload), silently re-expanding those sections.
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
import { translateCurrent } from "./i18n";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];

const workspace = "/tmp/wuu-collapse-test";

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
  // Fresh arrays on every call — mirrors production, where each
  // project-state reload replaces state.projects with a new identity.
  const workspaceState = (): {
    projects: never[];
    active_context: { kind: "no_project"; cwd: string };
  } => ({
    projects: [],
    active_context: { kind: "no_project", cwd: workspace },
  });
  const pinnedThread: Thread = {
    id: "thread-pinned-collapse",
    preview: "Pinned collapse probe",
    model_provider: "fake",
    model: "fake-model",
    cwd: workspace,
    workspace_kind: "scratch",
    status: "idle",
    read_only: false,
    pinned: true,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
  const api = {
    listProjects: vi.fn().mockImplementation(() => Promise.resolve(workspaceState())),
    selectNoProject: vi
      .fn()
      .mockImplementation(() => Promise.resolve(workspaceState())),
    initialize: vi.fn().mockResolvedValue(initialized()),
    listThreads: vi.fn().mockResolvedValue({ threads: [pinnedThread] }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    getSessionOrganization: vi.fn().mockResolvedValue({
      organization: { folders: [{ id: "folder-collapse", name: "Collapse probe" }] },
    }),
    setThemePreference: vi.fn().mockResolvedValue({ ok: true }),
    getBuildInfo: vi.fn().mockResolvedValue({ desktop: { version: "test" } }),
    listMCPServers: vi.fn().mockResolvedValue({ servers: [] }),
    resumeThread: vi.fn().mockResolvedValue({ thread: pinnedThread }),
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

function conversationSectionHeader(): HTMLButtonElement | null {
  return container.querySelector<HTMLButtonElement>(
    'button[aria-label="收起 对话 的会话"], button[aria-label="展开 对话 的会话"]',
  );
}

async function clickConversationHeader(): Promise<void> {
  const header = conversationSectionHeader();
  expect(header).not.toBeNull();
  await act(async () => {
    header?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  await flushAsync();
}

describe("sidebar collapse-state independence", () => {
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
    delete document.documentElement.dataset.theme;
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  });

  it("keeps the account menu inside the sidebar and dismisses it on collapse", async () => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();
    const accountTrigger = () => container.querySelector<HTMLButtonElement>(".sidebar-account-trigger");
    expect(accountTrigger()).not.toBeNull();
    await act(async () => accountTrigger()?.click());
    expect(container.querySelector(".sidebar .sidebar-account-menu")).not.toBeNull();

    const toggle = container.querySelector<HTMLButtonElement>(".sidebar-toggle-button");
    expect(toggle).not.toBeNull();
    await act(async () => toggle?.click());
    expect(document.querySelector(".sidebar-account-menu")).toBeNull();
    expect(accountTrigger()).toBeNull();

    const expandedToggle = container.querySelector<HTMLButtonElement>(".sidebar-toggle-button");
    expect(expandedToggle).not.toBeNull();
    await act(async () => expandedToggle?.click());
    expect(accountTrigger()?.getAttribute("aria-expanded")).toBe("false");
    expect(document.querySelector(".sidebar-account-menu")).toBeNull();
  });

  it.each([
    ['folder', '[data-section-id="__wuu_folder_sort__:folder-collapse"] button[aria-expanded]'],
    ['pinned', '[data-functional-group-id="pinned"] button[aria-expanded]'],
    ['workspace', '[data-functional-group-id="workspace"] button[aria-expanded]'],
    ['scratch', 'button[aria-label="收起 对话 的会话"], button[aria-label="展开 对话 的会话"]'],
  ])("preserves the %s fold across settings visits and theme changes", async (_section, selector) => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();
    const header = () => container.querySelector<HTMLButtonElement>(selector);
    const click = async (button: HTMLButtonElement | null | undefined) => {
      expect(button).toBeTruthy();
      await act(async () => button!.click());
      await flushAsync();
    };
    expect(header()?.getAttribute("aria-expanded")).toBe("true");
    await click(header());
    expect(header()?.getAttribute("aria-expanded")).toBe("false");

    for (const theme of [null, "dark", "light"] as const) {
      await click(container.querySelector(".sidebar-account-trigger"));
      await click(container.querySelector('[data-settings-page="providers"]'));
      await act(async () => { await import("./SettingsView"); });
      expect(container.querySelector(".settings-back-button")).not.toBeNull();
      if (theme) {
        await click([...container.querySelectorAll<HTMLButtonElement>(".settings-nav button")]
          .find((button) => button.textContent === translateCurrent("settings.appearance")));
        await click(container.querySelector(`[data-testid="settings-theme-${theme}"]`));
        expect(document.documentElement.dataset.theme).toBe(theme);
        expect(window.wuu.setThemePreference).toHaveBeenLastCalledWith(theme);
      }
      await click(container.querySelector(".settings-back-button"));
      expect(header()?.getAttribute("aria-expanded")).toBe("false");
    }
    await click(header());
    expect(header()?.getAttribute("aria-expanded")).toBe("true");
  });

  it("collapses the active 对话 section on the first header click", async () => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    expect(conversationSectionHeader()?.getAttribute("aria-expanded")).toBe(
      "true",
    );

    await clickConversationHeader();
    expect(conversationSectionHeader()?.getAttribute("aria-expanded")).toBe(
      "false",
    );

    await clickConversationHeader();
    expect(conversationSectionHeader()?.getAttribute("aria-expanded")).toBe(
      "true",
    );
  });
});
