import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ManagedProcessSummary,
  RuntimeContext,
  TerminalSessionEvent,
  Thread,
} from "../shared/protocol";
import {
  appendPendingTerminalEvent,
  WorkspaceTerminalPanel,
} from "./WorkspaceTerminalPanel";
import { appearanceDefaults, saveAppearance } from "./AppearancePreferences";

const { terminalConstructorOptions, terminalDataHandlers, terminalInstances } = vi.hoisted(() => ({
  terminalConstructorOptions: [] as Array<{
    convertEol?: boolean;
    cursorBlink?: boolean;
    disableStdin?: boolean;
    fontSize?: number;
    lineHeight?: number;
    scrollback?: number;
    theme?: Record<string, string>;
  }>,
  terminalDataHandlers: [] as Array<(data: string) => void>,
  terminalInstances: [] as Array<{
    options: { disableStdin?: boolean; fontSize?: number; fontFamily?: string; theme?: Record<string, string> };
    write: ReturnType<typeof vi.fn>;
    writeln: ReturnType<typeof vi.fn>;
  }>,
}));

// Stub xterm/the fit addon so mounting the real WorkspaceTerminalPanel
// doesn't need an actual terminal renderer or ResizeObserver-driven
// layout — mirrors the pattern used by AppApprovalFlow.test.tsx.
vi.mock("@xterm/xterm", () => ({
  Terminal: vi.fn().mockImplementation((options: {
    disableStdin?: boolean;
    theme?: Record<string, string>;
  }) => {
    terminalConstructorOptions.push(options);
    const terminal = {
      options: { ...options },
      loadAddon: vi.fn(),
      open: vi.fn(),
      focus: vi.fn(),
      write: vi.fn(),
      writeln: vi.fn(),
      dispose: vi.fn(),
      onData: vi.fn((handler: (data: string) => void) => {
        terminalDataHandlers.push(handler);
        return { dispose: vi.fn() };
      }),
      cols: 80,
      rows: 24,
    };
    terminalInstances.push(terminal);
    return terminal;
  }),
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: vi.fn().mockImplementation(() => ({
    fit: vi.fn(),
  })),
}));

class StubResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

let container: HTMLDivElement;
let root: Root | null = null;
let startTerminalSession: ReturnType<typeof vi.fn>;
let stopTerminalSession: ReturnType<typeof vi.fn>;
let listManagedProcesses: ReturnType<typeof vi.fn>;
let readManagedProcess: ReturnType<typeof vi.fn>;
let writeManagedProcess: ReturnType<typeof vi.fn>;
let resizeManagedProcess: ReturnType<typeof vi.fn>;
let stopManagedProcess: ReturnType<typeof vi.fn>;
const terminalEventHandlers: Array<(event: TerminalSessionEvent) => void> = [];

beforeEach(() => {
  localStorage.clear();
  document.documentElement.dataset.theme = "light";
  terminalConstructorOptions.length = 0;
  terminalDataHandlers.length = 0;
  terminalInstances.length = 0;
  terminalEventHandlers.length = 0;
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  startTerminalSession = vi.fn().mockImplementation(async () => ({
    id: `term-${startTerminalSession.mock.calls.length}`,
    cwd: "/repo",
    shell: "/bin/zsh",
    started_at: new Date().toISOString(),
  }));
  stopTerminalSession = vi.fn();
  listManagedProcesses = vi.fn().mockResolvedValue({ processes: [] });
  readManagedProcess = vi.fn();
  writeManagedProcess = vi.fn().mockResolvedValue({ bytes_written: 0 });
  resizeManagedProcess = vi.fn().mockResolvedValue({});
  stopManagedProcess = vi.fn();
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      startTerminalSession,
      writeTerminalSession: vi.fn(),
      resizeTerminalSession: vi.fn(),
      stopTerminalSession,
      listManagedProcesses,
      readManagedProcess,
      writeManagedProcess,
      resizeManagedProcess,
      stopManagedProcess,
      onTerminalEvent: vi.fn((handler: (event: TerminalSessionEvent) => void) => {
        terminalEventHandlers.push(handler);
        return () => {};
      }),
    },
  });
  (globalThis as unknown as { ResizeObserver: typeof StubResizeObserver }).ResizeObserver =
    StubResizeObserver;
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  delete document.documentElement.dataset.theme;
  document.documentElement.removeAttribute("style");
  delete document.documentElement.dataset.appearanceMotion;
  localStorage.clear();
  vi.clearAllMocks();
});

async function render(element: JSX.Element): Promise<void> {
  await act(async () => {
    root?.render(element);
    await Promise.resolve();
  });
  // Let the requestAnimationFrame-scheduled fit/resize and the
  // startSession() microtask settle.
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("WorkspaceTerminalPanel", () => {
  const worktreeContext: RuntimeContext = {
    kind: "project",
    project_id: "project-1",
    cwd: "/worktrees/fork-1/project",
  };
  const threadWithRuns = {
    id: "thread-1",
    turns: [
      {
        id: "turn-1",
        items_view: "full",
        status: "completed",
        items: [
          {
            id: "call-ok",
            type: "tool_call",
            status: "completed",
            name: "bash",
            arguments: JSON.stringify({ command: "npm test" }),
            display: { kind: "command", capability: "command.bash" },
            result: JSON.stringify({
              exit_code: 0,
              duration_ms: 1234,
              stdout_tail: "tests passed\n",
            }),
          },
          {
            id: "call-failed",
            type: "tool_call",
            status: "completed",
            name: "bash",
            arguments: JSON.stringify({ command: "npm run lint" }),
            display: { kind: "command", capability: "command.bash" },
            result: JSON.stringify({
              exit_code: 2,
              duration_ms: 2500,
              stderr_tail: "lint failed\n",
              stderr_tail_truncated: true,
            }),
          },
        ],
      },
    ],
  } as Thread;
  const runningProcess: ManagedProcessSummary = {
    id: "proc-live",
    owner_kind: "main_agent",
    owner_id: "thread-1",
    lifecycle: "managed",
    status: "running",
    pid: 123,
    tty: true,
    command: "npm run dev",
    cwd: "/worktrees/fork-1/project",
    started_at: "2026-07-18T08:00:00Z",
    updated_at: "2026-07-18T08:00:00Z",
    input_available: true,
  };
  const threadWithLiveRun = {
    id: "thread-1",
    turns: [{
      id: "turn-live",
      items_view: "full",
      status: "in_progress",
      items: [{
        id: "call-live",
        type: "tool_call",
        status: "completed",
        name: "bash",
        arguments: JSON.stringify({ action: "start_background", command: "npm run dev", tty: true }),
        display: { kind: "command", capability: "command.bash" },
        result: JSON.stringify({
          action: "start_background",
          id: "proc-live",
          status: "running",
          tty: true,
        }),
      }],
    }],
  } as Thread;

  it("starts a workspace-rooted pty as soon as the panel opens", async () => {
    await render(
      <WorkspaceTerminalPanel activeContext={worktreeContext} />,
    );

    expect(startTerminalSession).toHaveBeenCalledWith(
      expect.objectContaining({ cwd: "/worktrees/fork-1/project" }),
    );
    expect(startTerminalSession).toHaveBeenCalledTimes(1);
    expect(container.querySelector(".workspace-terminal-path")?.textContent).toBe("/worktrees/fork-1/project");
    expect(container.querySelector(".workspace-terminal-navigation")).toBeNull();
    expect(container.querySelector('button[aria-label="新建终端"]')).toBeNull();
    expect(container.querySelectorAll(".workspace-terminal-panel")).toHaveLength(1);
  });

  it("routes broadcast terminal events to the matching pty", async () => {
    await render(<WorkspaceTerminalPanel activeContext={worktreeContext} />);
    await vi.waitFor(() => expect(startTerminalSession).toHaveBeenCalledTimes(1));

    act(() => {
      for (const handler of terminalEventHandlers) {
        handler({ type: "data", id: "term-1", text: "first" });
        handler({ type: "data", id: "term-2", text: "second" });
      }
    });

    expect(terminalInstances[0]?.write).toHaveBeenCalledWith("first");
    expect(terminalInstances[0]?.write).not.toHaveBeenCalledWith("second");
  });

  it("updates an open terminal from code preferences without restarting its session", async () => {
    saveAppearance({ ...appearanceDefaults, codeSize: 15, codeFont: "Monaco" });
    await render(<WorkspaceTerminalPanel activeContext={worktreeContext} />);
    const terminal = terminalInstances[0]!;
    expect(terminal.options.fontSize).toBe(15);
    expect(terminal.options.fontFamily).toContain("Monaco");

    act(() => saveAppearance({ ...appearanceDefaults, codeSize: 19 }));
    expect(terminal.options.fontSize).toBe(19);
    expect(startTerminalSession).toHaveBeenCalledTimes(1);
    expect(stopTerminalSession).not.toHaveBeenCalled();

    act(() => window.dispatchEvent(new Event("wuu-content-size-change")));
    expect(terminal.options.fontSize).toBe(19);
    act(() => { root?.unmount(); root = null; });
    saveAppearance({ ...appearanceDefaults, codeSize: 12 });
    expect(terminal.options.fontSize).toBe(19);
  });

  it("does not render a terminal without a workspace context", () => {
    act(() => {
      root?.render(<WorkspaceTerminalPanel activeContext={undefined} />);
    });

    expect(startTerminalSession).not.toHaveBeenCalled();
    expect(container.textContent).toContain("没有项目");
  });

  it("uses the applied theme and updates an open terminal when it changes", async () => {
    await render(<WorkspaceTerminalPanel activeContext={worktreeContext} />);

    expect(terminalConstructorOptions[0]?.theme?.background).toBe("#ffffff");

    document.documentElement.dataset.theme = "dark";
    await vi.waitFor(() => {
      expect(terminalInstances[0]?.options.theme?.background).toBe("#1d2024");
      expect(terminalInstances[0]?.options.theme?.foreground).toBe("#e4e6e8");
    });
  });

  it("does not open settled command history as the interactive terminal", async () => {
    await render(
      <WorkspaceTerminalPanel activeContext={worktreeContext} thread={threadWithRuns} />,
    );

    expect(container.querySelector(".workspace-terminal-path")?.textContent).toBe("/worktrees/fork-1/project");
    expect(container.textContent).not.toContain("npm test");
    expect(container.textContent).not.toContain("npm run lint");
    expect(startTerminalSession).toHaveBeenCalledTimes(1);
    expect(terminalInstances).toHaveLength(1);
  });

  it("uses the live process inventory without requiring matching command history", async () => {
    listManagedProcesses.mockResolvedValue({ processes: [runningProcess] });
    readManagedProcess.mockImplementation(() => new Promise(() => {}));

    await render(
      <WorkspaceTerminalPanel activeContext={worktreeContext} thread={threadWithRuns} />,
    );

    await vi.waitFor(() => {
      expect(listManagedProcesses).toHaveBeenCalledWith("thread-1");
      expect(container.querySelector(".workspace-agent-terminal")?.textContent).toContain("npm run dev");
      expect(container.textContent).not.toContain("npm test");
      expect(container.textContent).not.toContain("npm run lint");
    });
    expect(startTerminalSession).not.toHaveBeenCalled();
    expect(container.querySelector(".workspace-terminal-navigation")).toBeNull();
  });

  it("does not keep a stopped managed process instead of the interactive terminal", async () => {
    listManagedProcesses.mockResolvedValue({
      processes: [{ ...runningProcess, status: "stopped", stopped_at: "2026-07-18T08:01:00Z" }],
    });

    await render(
      <WorkspaceTerminalPanel activeContext={worktreeContext} thread={threadWithLiveRun} />,
    );

    await vi.waitFor(() => expect(listManagedProcesses).toHaveBeenCalledWith("thread-1"));
    expect(container.textContent).not.toContain("npm run dev");
    expect(startTerminalSession).toHaveBeenCalledTimes(1);
    expect(terminalInstances).toHaveLength(1);
  });

  it("opens a requested settled run without starting the interactive terminal", async () => {
    await render(
      <WorkspaceTerminalPanel
        activeContext={worktreeContext}
        thread={threadWithRuns}
        requestedRun={{
          threadID: "thread-1",
          turnID: "turn-1",
          requestID: 1,
        }}
      />,
    );

    expect(startTerminalSession).not.toHaveBeenCalled();
    expect(container.querySelector(".workspace-terminal-workspace")?.getAttribute("data-wuu-state")).toBe("standalone");
    expect(container.querySelector('button[aria-label="新建终端"]')).toBeNull();
    expect(container.querySelector(".workspace-agent-terminal")?.textContent).toContain("npm run lint");
    expect(container.textContent).toContain("失败");
    expect(container.textContent).not.toContain("AI 命令");
    expect(container.textContent).not.toContain("第 1 轮");
    expect(terminalConstructorOptions[0]).toMatchObject({ convertEol: true, disableStdin: true });
    expect(terminalInstances[0]?.write).toHaveBeenCalledWith("lint failed\n");
    expect(terminalInstances[0]?.writeln).toHaveBeenCalledWith("[这里只能查看协议保留的输出片段。]");
    expect(container.textContent).not.toContain("npm test");
    expect(writeManagedProcess).not.toHaveBeenCalled();
  });

  it("keeps managed tty controls working when native terminal creation is unavailable", async () => {
    window.wuu.unsupportedMethods = ["startTerminalSession", "writeTerminalSession", "resizeTerminalSession", "stopTerminalSession"];
    listManagedProcesses.mockResolvedValue({ processes: [runningProcess] });
    readManagedProcess
      .mockResolvedValueOnce({
        process: runningProcess,
        output: "ready\r\n",
        truncated: false,
        start_offset: 0,
        end_offset: 7,
        total_bytes: 7,
        timed_out: false,
      })
      .mockImplementation(() => new Promise(() => {}));
    const stoppedProcess: ManagedProcessSummary = {
      ...runningProcess,
      status: "stopped",
      input_available: false,
      stopped_at: "2026-07-18T08:01:00Z",
    };
    stopManagedProcess.mockResolvedValue({ process: stoppedProcess });

    await render(
      <WorkspaceTerminalPanel
        activeContext={worktreeContext}
        thread={threadWithLiveRun}
        requestedRun={{ threadID: "thread-1", turnID: "turn-live", requestID: 1 }}
      />,
    );

    expect(container.querySelector('button[aria-label="新建终端"]')).toBeNull();

    await vi.waitFor(() => {
      expect(readManagedProcess).toHaveBeenCalledWith(expect.objectContaining({
        thread_id: "thread-1",
        process_id: "proc-live",
        offset_bytes: 0,
        wait_ms: 10000,
      }));
      expect(terminalInstances[0]?.write).toHaveBeenCalledWith("ready\r\n");
    });
    expect(startTerminalSession).not.toHaveBeenCalled();
    expect(terminalInstances).toHaveLength(1);
    expect(terminalInstances[0]?.options.disableStdin).toBe(false);
    expect(terminalConstructorOptions[0]).toMatchObject({
      convertEol: false,
      cursorBlink: true,
      fontSize: appearanceDefaults.codeSize,
      scrollback: 10000,
    });

    act(() => terminalDataHandlers[0]?.("hello\r"));
    expect(writeManagedProcess).toHaveBeenCalledWith("thread-1", "proc-live", "hello\r");
    expect(resizeManagedProcess).toHaveBeenCalledWith("thread-1", "proc-live", 80, 24);

    const stopButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("停止"),
    );
    await act(async () => {
      stopButton?.click();
      await Promise.resolve();
    });

    expect(stopManagedProcess).toHaveBeenCalledWith("thread-1", "proc-live");
    expect(container.textContent).toContain("已停止");
  });

  it("continues managed output from the durable byte offset and keeps the settled terminal", async () => {
    const stoppedProcess: ManagedProcessSummary = {
      ...runningProcess,
      status: "stopped",
      input_available: false,
      stopped_at: "2026-07-18T08:01:00Z",
      updated_at: "2026-07-18T08:01:00Z",
    };
    listManagedProcesses.mockResolvedValue({ processes: [runningProcess] });
    readManagedProcess
      .mockResolvedValueOnce({
        process: runningProcess,
        output: "one",
        truncated: false,
        start_offset: 0,
        end_offset: 3,
        total_bytes: 3,
        timed_out: false,
      })
      .mockResolvedValueOnce({
        process: stoppedProcess,
        output: "two",
        truncated: false,
        start_offset: 3,
        end_offset: 6,
        total_bytes: 6,
        timed_out: false,
      });

    await render(
      <WorkspaceTerminalPanel
        activeContext={worktreeContext}
        thread={threadWithLiveRun}
        requestedRun={{ threadID: "thread-1", turnID: "turn-live", requestID: 1 }}
      />,
    );

    await vi.waitFor(() => {
      expect(readManagedProcess).toHaveBeenNthCalledWith(2, expect.objectContaining({
        offset_bytes: 3,
      }));
      expect(terminalInstances[0]?.write).toHaveBeenCalledWith("two");
      expect(container.textContent).toContain("已停止");
    });
    expect(terminalInstances).toHaveLength(1);
    expect(terminalInstances[0]?.options.disableStdin).toBe(true);
  });

  it("keeps a non-tty managed process read-only", async () => {
    const nonTTYProcess: ManagedProcessSummary = {
      ...runningProcess,
      tty: false,
      input_available: false,
    };
    listManagedProcesses.mockResolvedValue({ processes: [nonTTYProcess] });
    readManagedProcess
      .mockResolvedValueOnce({
        process: nonTTYProcess,
        output: "server ready\n",
        truncated: false,
        start_offset: 0,
        end_offset: 13,
        total_bytes: 13,
        timed_out: false,
      })
      .mockImplementation(() => new Promise(() => {}));

    await render(
      <WorkspaceTerminalPanel
        activeContext={worktreeContext}
        thread={threadWithLiveRun}
        requestedRun={{ threadID: "thread-1", turnID: "turn-live", requestID: 1 }}
      />,
    );
    await vi.waitFor(() => {
      expect(terminalInstances[0]?.write).toHaveBeenCalledWith("server ready\n");
    });

    act(() => terminalDataHandlers[0]?.("ignored"));
    expect(terminalInstances[0]?.options.disableStdin).toBe(true);
    expect(writeManagedProcess).not.toHaveBeenCalled();
    expect(resizeManagedProcess).not.toHaveBeenCalled();
  });
});

describe("appendPendingTerminalEvent", () => {
  it("bounds events and text buffered before terminal startup resolves", () => {
    let events: TerminalSessionEvent[] = [];
    for (let index = 0; index < 300; index += 1) {
      events = appendPendingTerminalEvent(events, {
        type: "data",
        id: "term-1",
        text: String(index),
      });
    }

    expect(events).toHaveLength(256);
    expect(events[0]).toMatchObject({ type: "data", text: "44" });

    events = appendPendingTerminalEvent([], {
      type: "data",
      id: "term-1",
      text: "x".repeat(600 * 1024),
    });
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({
      type: "data",
      text: "x".repeat(512 * 1024),
    });
  });
});
