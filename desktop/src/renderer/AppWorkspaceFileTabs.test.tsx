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
  WorkspaceMonacoEditor: ({ path }: { path: string }): JSX.Element => (
    <div className="workspace-monaco-editor" data-path={path} />
  ),
}));

vi.mock("./JumpToLatestPill", () => ({
  JumpToLatestPill: (): JSX.Element => (
    <div data-testid="jump-to-latest-probe" />
  ),
}));

import { App, SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS } from "./App";
import { RIGHT_PANEL_MOTION_MS } from "./AppLayoutState";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];
let startTurnMock: ReturnType<typeof vi.fn>;
const originalInnerWidth = window.innerWidth;

const workspace = "/tmp/wuu-artifact-tab-test";

function initialized(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    workspace_root: workspace,
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

function completedThread(): Thread {
  return {
    id: "thread-artifact-tabs",
    preview: "artifact conversation",
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
          {
            id: "item-user",
            type: "user_message",
            role: "user",
            status: "completed",
            text: "Show me the document.",
          },
          {
            id: "item-agent",
            type: "agent_message",
            role: "assistant",
            terminal: true,
            status: "completed",
            text: "Open [README.md](README.md) beside this conversation.",
          },
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
  startTurnMock = vi.fn().mockResolvedValue({ turn: thread.turns[0] });
  const api = {
    listProjects: vi.fn().mockResolvedValue({
      projects: [
        {
          id: "project-wuu",
          name: "wuu",
          path: "/repo/wuu",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
      ],
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
    startTurn: startTurnMock,
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({
      is_repo: false,
      dirty_count: 0,
      files: [],
    }),
    listWorkspaceDirectory: vi.fn().mockResolvedValue({
      root: workspace,
      path: "",
      entries: [{ kind: "file", name: "README.md", path: "README.md" }],
      truncated: false,
    }),
    readWorkspaceFile: vi.fn().mockResolvedValue({
      root: workspace,
      path: "README.md",
      absolute_path: `${workspace}/README.md`,
      size_bytes: 16,
      mtime_ms: 1000,
      sha256: "a".repeat(64),
      binary: false,
      truncated: false,
      text: "# Artifact\n",
    }),
    writeWorkspaceFile: vi.fn(),
    revealWorkspaceItem: vi.fn().mockResolvedValue(undefined),
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

function setInnerWidth(width: number): void {
  window.innerWidth = width;
}

async function flushAsync(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("workspace file tabs", () => {
  beforeEach(() => {
    setInnerWidth(1280);
    installWindowStubs();
    installWuuApi();
    Element.prototype.scrollIntoView = vi.fn();
    serverEventHandlers = [];
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
  });

  afterEach(() => {
    setInnerWidth(originalInnerWidth);
    act(() => {
      root?.unmount();
    });
    root = null;
    container.remove();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
    vi.useRealTimers();
  });

  it("opens a document beside the active conversation instead of replacing it", async () => {
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    const fileLink = container.querySelector<HTMLButtonElement>(".rich-file-link");
    expect(fileLink).not.toBeNull();
    expect(container.querySelectorAll(".session-tab")).toHaveLength(0);
    expect(container.querySelector(".conversation-title-heading h1")?.textContent).toContain(
      "artifact conversation",
    );
    expect(
      container.querySelector('[data-testid="jump-to-latest-probe"]'),
    ).not.toBeNull();

    vi.useFakeTimers();
    await act(async () => {
      fileLink?.click();
    });
    await flushAsync();

    expect(container.querySelectorAll(".session-tab")).toHaveLength(0);
    expect(container.querySelector(".conversation-title-heading h1")?.textContent).toContain(
      "artifact conversation",
    );
    expect(container.querySelector(".rich-file-link")).not.toBeNull();
    expect(
      container.querySelector(".workspace-right-panel .workspace-tool-tab.active")?.textContent,
    ).toContain("README.md");
    expect(container.querySelector(".workspace-right-panel .workspace-files-content-header")).toBeNull();
    expect(container.querySelector(".workspace-right-panel .workspace-files-tree")).not.toBeNull();
    const rightFilePreview = container.querySelector(
      ".workspace-right-panel .workspace-file-resource.active .workspace-file-preview",
    );
    expect(rightFilePreview).not.toBeNull();
    expect(rightFilePreview?.textContent).toContain("Artifact");

    act(() => {
      vi.advanceTimersByTime(RIGHT_PANEL_MOTION_MS);
    });
    const shell = container.querySelector<HTMLElement>(".app-shell");
    expect(shell?.classList.contains("right-panel-animating")).toBe(false);

    fileLink?.focus();
    const expand = container.querySelector<HTMLButtonElement>('[aria-label="展开为全面板"]');
    await act(async () => expand?.click());
    await flushAsync();

    expect(shell?.classList.contains("right-panel-animating")).toBe(true);
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(shell?.classList.contains("sidebar-collapsed")).toBe(false);
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(true);
    expect(container.querySelector(".sidebar")?.hasAttribute("inert")).toBe(false);
    expect(container.querySelector(".workspace-right-panel")?.hasAttribute("inert")).toBe(false);
    expect(
      container.querySelector('.globalized-sidebar-toggle[aria-label="展开左侧栏"]'),
    ).toBeNull();
    expect(container.querySelector('[data-testid="workspace-document-composer"]')).not.toBeNull();
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
    expect(
      container.querySelector('[data-main-conversation-composer="document"]'),
    ).not.toBeNull();
    expect(
      container
        .querySelector(".workspace-document-turn-summary")
        ?.getAttribute("aria-expanded"),
    ).toBe("false");
    expect(
      container.querySelector('[data-testid="jump-to-latest-probe"]'),
    ).toBeNull();
    expect(document.activeElement).toBe(
      container.querySelector(".workspace-tool-tab.active .workspace-tool-tab-main"),
    );

    const focusedTextarea = container.querySelector<HTMLTextAreaElement>(
      '[data-testid="workspace-document-composer"] textarea',
    );
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      valueSetter?.call(focusedTextarea, "Rewrite the weak section.");
      focusedTextarea?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    startTurnMock.mockResolvedValueOnce({
      turn: {
        id: "turn-document-edit",
        items_view: "full",
        status: "in_progress",
        items: [
          {
            id: "item-document-edit-user",
            type: "user_message",
            text: "Rewrite the weak section.",
          },
        ],
      },
    });
    await act(async () => {
      focusedTextarea?.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
    });
    await flushAsync();
    expect(startTurnMock).toHaveBeenCalledWith(
      "thread-artifact-tabs",
      "Rewrite the weak section.",
      [],
      [],
      undefined,
      { path: "README.md" },
      undefined,
      { kind: "no_project", cwd: "/tmp/wuu-artifact-tab-test" },
    );
    expect(container.querySelector('[data-testid="workspace-document-turn-drawer"]')).not.toBeNull();

    act(() => {
      vi.advanceTimersByTime(RIGHT_PANEL_MOTION_MS);
    });
    expect(shell?.classList.contains("right-panel-animating")).toBe(false);

    const exit = container.querySelector<HTMLButtonElement>('[aria-label="退出全面板"]');
    await act(async () => exit?.click());
    await flushAsync();
    expect(shell?.classList.contains("right-panel-animating")).toBe(true);
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(false);
    expect(container.querySelector(".sidebar")?.hasAttribute("inert")).toBe(false);
    expect(container.querySelector('[data-testid="workspace-document-composer"]')).toBeNull();
    expect(document.activeElement).toBe(fileLink);
  });

  it("mounts only the focused composer for an empty conversation", async () => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    vi.useFakeTimers();
    await act(async () => {
      container.querySelector<HTMLButtonElement>(".rich-file-link")?.click();
    });
    await flushAsync();
    act(() => {
      vi.advanceTimersByTime(RIGHT_PANEL_MOTION_MS);
    });
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[aria-label="展开为全面板"]')?.click();
    });
    await flushAsync();

    const emptyThread = { ...completedThread(), turns: [] };
    await act(async () => {
      for (const handler of serverEventHandlers) {
        handler({
          kind: "notification",
          workdir: workspace,
          message: {
            method: "thread/updated",
            params: { thread: emptyThread },
          },
        } as ServerEvent);
      }
    });
    await flushAsync();

    expect(container.querySelector('[data-testid="workspace-document-composer"]')).not.toBeNull();
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
  });

  it("focuses the workspace automatically when a compact window cannot keep conversation usable", async () => {
    setInnerWidth(674);
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    await act(async () => {
      container.querySelector<HTMLButtonElement>(".rich-file-link")?.click();
    });
    await flushAsync();

    const shell = container.querySelector<HTMLElement>(".app-shell");
    expect(shell?.classList.contains("right-panel-globalized")).toBe(true);
    expect(shell?.classList.contains("sidebar-collapsed")).toBe(true);
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(true);

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '.globalized-sidebar-toggle[aria-label="展开左侧栏"]',
        )
        ?.click();
    });
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(true);
    expect(container.querySelector(".sidebar")?.hasAttribute("inert")).toBe(false);

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '.globalized-sidebar-toggle[aria-label="收起左侧栏"]',
        )
        ?.click();
    });
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(container.querySelector(".sidebar")?.hasAttribute("inert")).toBe(true);

    const sidebarToggle = container.querySelector<HTMLButtonElement>(
      '.globalized-sidebar-toggle[aria-label="展开左侧栏"]',
    );
    vi.useFakeTimers();
    await act(async () => {
      sidebarToggle?.dispatchEvent(
        new MouseEvent("pointerover", { bubbles: true, relatedTarget: null }),
      );
      vi.advanceTimersByTime(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS);
    });
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(true);
    expect(sidebarToggle?.getAttribute("aria-pressed")).toBe("true");

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>('[aria-label="打开工作区 wuu"]')
        ?.click();
    });
    await flushAsync();
    expect(shell?.classList.contains("right-panel-globalized")).toBe(true);
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(
      container
        .querySelector('[data-section-id="project-wuu"] .project-row')
        ?.getAttribute("aria-current"),
    ).toBe("page");
    expect(window.wuu.listWorkspaceDirectory).toHaveBeenCalledWith("", "/repo/wuu");

    await act(async () => {
      setInnerWidth(1000);
      window.dispatchEvent(new Event("resize"));
    });
    await flushAsync();
    expect(shell?.classList.contains("right-panel-globalized")).toBe(false);
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(false);
  });

  it("keeps all three columns docked when an open sidebar is the only space pressure", async () => {
    // 1000px window (>= 900, so the sidebar stays docked, not auto-collapsed).
    // Conversation + panel fit without the sidebar, but adding the docked
    // sidebar tips the layout over the focus threshold. The panel must NOT
    // auto-globalize as a side effect of the sidebar being open — instead all
    // three stay docked and the conversation column absorbs the squeeze. Only a
    // manual toggle globalizes here.
    setInnerWidth(1000);
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    await act(async () => {
      container.querySelector<HTMLButtonElement>(".rich-file-link")?.click();
    });
    await flushAsync();

    const shell = container.querySelector<HTMLElement>(".app-shell");
    expect(shell?.classList.contains("right-panel-open")).toBe(true);
    expect(shell?.classList.contains("right-panel-globalized")).toBe(false);
    // The sidebar stays a real docked column, not a drawer overlay.
    expect(shell?.classList.contains("sidebar-collapsed")).toBe(false);
    expect(
      container.querySelector(".conversation-pane")?.hasAttribute("inert"),
    ).toBe(false);
  });
});
