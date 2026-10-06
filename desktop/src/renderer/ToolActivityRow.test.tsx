/**
 * Tests for `ToolActivityRow`.
 *
 * Contract: an initial live summary renders in full so switching back
 * to a running session does not replay old gray text. Summary text
 * fake-streams when it grows during `streaming`, and snaps to full the
 * moment `streaming` flips false (the catch-up signal AssistantTurnShell
 * raises when an agent_message in the same turn starts streaming).
 */
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ToolActivityRow } from "./ToolActivity";
import type { ThreadItem } from "../shared/protocol";

// jsdom doesn't implement layout. Stub getBoundingClientRect so React
// doesn't crash on layout queries.
beforeAll(() => {
  Element.prototype.getBoundingClientRect = function (): DOMRect {
    return {
      x: 0,
      y: 0,
      top: 0,
      left: 0,
      right: 0,
      bottom: 0,
      width: 0,
      height: 0,
      toJSON() {
        return this;
      },
    } as DOMRect;
  };
});

describe("workspace tool records", () => {
  it("keeps the historical destination and exposes failures", () => {
    const item: ThreadItem = {
      id: "workspace-1", type: "tool_call", name: "set_session_workspace",
      status: "in_progress", arguments: JSON.stringify({ root: "/repo/worktree-one" }),
    };
    mount({ items: [item] });
    const details = container!.querySelector("details")!;
    expect(details.open).toBe(false);
    expect(details.textContent).toContain("/repo/worktree-one");

    act(() => { details.open = true; });
    rerender({ items: [{ ...item, status: "completed", result: JSON.stringify({ root: "/repo/worktree-one" }) }] });
    expect(container!.querySelector("details")!.open).toBe(true);
    expect(container!.querySelector("details")!.textContent).toContain("/repo/worktree-one");

    mount({ items: [{ ...item, id: "workspace-2", status: "failed", arguments: JSON.stringify({ root: "/repo/missing" }), error: "inspect workspace root: no such directory" }] });
    expect(container!.querySelector("details")!.textContent).toContain("/repo/missing");
    expect(container!.querySelector("details")!.textContent).toContain("inspect workspace root: no such directory");
  });
});

describe("PTC program records", () => {
  it.each(["code", "input"])("displays literal %s source and its description", (field) => {
    let code = "  const html = \"<img src=x onerror=alert(1)>\";\n\nawait tools.read_file({path: \"notes.ts\"});\nconsole.log(html);\n";
    const description = "Read notes";
    if (field === "input") code = `// @run_code: ${JSON.stringify({ description })}\n${code}`;
    const item: ThreadItem = {
      id: "program-1", type: "tool_call", name: "run_code", status: "in_progress",
      arguments: JSON.stringify(field === "input" ? { input: code } : { code, description }),
      display: { kind: "command", label: "Run command" },
    };
    mount({ items: [item], streaming: true });
    const details = container!.querySelector("details")!;
    expect(details).not.toBeNull();
    expect(details.open).toBe(false);
    expect(details.querySelector("summary")!.textContent).toBe(description);
    expect(details.querySelector("img")).toBeNull();
    expect(details.querySelector("pre")).toBeNull();

    act(() => {
      details.open = true;
      details.dispatchEvent(new Event("toggle"));
    });
    expect(details.querySelector("pre code")!.textContent).toBe(code);
    expect(details.querySelector("img")).toBeNull();
    rerender({ items: [{ ...item, status: "completed", result: "done" }] });
    expect(container!.querySelector("details")!.open).toBe(true);
    expect(container!.querySelector("pre code")!.textContent).toBe(code);
  });

  it("waits for parseable arguments and can disclose a failed program", () => {
    const item: ThreadItem = {
      id: "program-2", type: "tool_call", name: "run_code", status: "in_progress",
      arguments: '{"code":"await tools.',
    };
    mount({ items: [item], streaming: true });
    expect(container!.querySelector("details")).toBeNull();
    const code = "await tools.read_file({path: \"missing.ts\"});";
    rerender({ items: [{ ...item, status: "failed", arguments: JSON.stringify({ code }), error: "File not found" }] });
    const details = container!.querySelector("details")!;
    act(() => {
      details.open = true;
      details.dispatchEvent(new Event("toggle"));
    });
    expect(details.querySelector("pre code")!.textContent).toBe(code);
    expect(details.textContent).toContain("File not found");
  });
});

describe("Codex command records", () => {
  it("shows the command and preserves literal input and output through completion", () => {
    const command = "  printf '%s\\n' '<img src=x onerror=alert(1)>'\nrg ToolActivity desktop/src\n";
    const item: ThreadItem = {
      id: "codex-command", type: "tool_call", name: "exec", status: "in_progress",
      arguments: JSON.stringify({ command, cwd: "/repo/a workspace", commandActions: [] }),
    };
    mount({ items: [item], streaming: true });
    const details = container!.querySelector("details")!;
    expect(details).not.toBeNull();
    expect(details.open).toBe(false);
    expect(details.querySelector("summary")!.textContent).toContain("rg ToolActivity desktop/src");
    expect(details.querySelector("pre")).toBeNull();
    act(() => {
      details.open = true;
      details.dispatchEvent(new Event("toggle"));
    });
    expect(details.querySelector("pre code")!.textContent).toBe(command);
    expect(details.textContent).toContain("/repo/a workspace");
    const result = "<img src=x onerror=alert(1)>\n  ToolActivity.tsx\n";
    rerender({ items: [{ ...item, status: "completed", result }] });
    expect(details.open).toBe(true);
    expect([...details.querySelectorAll("pre code")].map(node => node.textContent)).toEqual([command, result]);
    expect(details.querySelector("img")).toBeNull();
  });

  it("waits for command arguments and exposes failure output", () => {
    const item: ThreadItem = {
      id: "codex-command-failed", type: "tool_call", name: "exec", status: "in_progress",
      arguments: '{"command":"rg',
    };
    mount({ items: [item] });
    expect(container!.querySelector("details")).toBeNull();
    rerender({ items: [{ ...item, status: "failed", arguments: JSON.stringify({ command: "rg missing" }), result: "Exit 2", error: "Command failed" }] });
    const details = container!.querySelector("details")!;
    act(() => {
      details.open = true;
      details.dispatchEvent(new Event("toggle"));
    });
    expect([...details.querySelectorAll("pre code")].map(node => node.textContent)).toEqual(["rg missing", "Exit 2"]);
    expect(details.textContent).toContain("Command failed");
  });
});

function fakeReadFileTool(): ThreadItem {
  // Single-segment path so formatPathTarget's basename collapse lands
  // on a deterministic string we can match exactly in assertions.
  return {
    id: "tool-1",
    type: "tool_call",
    status: "completed",
    name: "read_file",
    arguments: JSON.stringify({ path: "foo.ts" }),
  };
}

function fakeInFlightReadFileTool(): ThreadItem {
  return {
    id: "tool-1",
    type: "tool_call",
    status: "in_progress",
    name: "read_file",
  };
}

// A read activity shows its action and filename, without the parent path.
const SUMMARY_TEXT = "查看 foo.ts";

let container: HTMLDivElement | null = null;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
});

function mount(props: Parameters<typeof ToolActivityRow>[0]): void {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(<ToolActivityRow {...props} />);
  });
}

function rerender(props: Parameters<typeof ToolActivityRow>[0]): void {
  act(() => {
    root!.render(<ToolActivityRow {...props} />);
  });
}

function unmount(): void {
  if (root) {
    act(() => {
      root!.unmount();
    });
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
}

function surfaceText(): string {
  const span = container?.querySelector(".activity-copy span") as
    | HTMLElement
    | null;
  return span?.textContent ?? "";
}

afterEach(() => {
  unmount();
  vi.useRealTimers();
});

describe("ToolActivityRow", () => {
  it("renders the summary text in full immediately when streaming is false", () => {
    mount({ items: [fakeReadFileTool()], streaming: false });
    expect(surfaceText()).toBe(SUMMARY_TEXT);
  });

  it("shows a command purpose without a generic inspect prefix", () => {
    mount({
      items: [{
        id: "tool-command",
        type: "tool_call",
        status: "completed",
        name: "bash",
        arguments: JSON.stringify({ command: "brew search herdr" }),
      }],
      streaming: false,
    });
    expect(surfaceText()).toBe("搜索软件包");
  });

  it("shows a plugin label without a generic tool prefix", () => {
    mount({
      items: [{
        id: "tool-plugin",
        type: "tool_call",
        status: "completed",
        name: "plugin_notes_work_0123456789abcdef",
        arguments: "{}",
        display: { label: "工作笔记" },
      }],
      streaming: false,
    });
    expect(surfaceText()).toBe("工作笔记");
  });

  it("renders an initial live summary in full", () => {
    mount({ items: [fakeReadFileTool()], streaming: true });
    expect(surfaceText()).toBe(SUMMARY_TEXT);
  });

  it("renders summary text progressively when it grows during streaming", () => {
    mount({ items: [fakeInFlightReadFileTool()], streaming: true });
    expect(surfaceText()).toBe("查看");

    rerender({ items: [fakeReadFileTool()], streaming: true });

    // Mid-reveal: visible is partial (strictly less than full text).
    // The LightweightStreamingText pace is ~12 cps with a 100 ms base,
    // so 200 ms in we should be partway through.
    act(() => {
      vi.advanceTimersByTime(200);
    });
    const mid = surfaceText();
    expect(mid.length).toBeGreaterThan(0);
    expect(mid.length).toBeLessThan(SUMMARY_TEXT.length);
    expect(SUMMARY_TEXT.startsWith(mid)).toBe(true);

    // After enough virtual time the reveal settles. The full string is
    // 7 chars, well within the 1800 ms ceiling.
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(surfaceText()).toBe(SUMMARY_TEXT);
  });

  it("snaps to full text when streaming flips to false mid-reveal (catch-up)", () => {
    mount({ items: [fakeInFlightReadFileTool()], streaming: true });
    rerender({ items: [fakeReadFileTool()], streaming: true });

    // Let the reveal advance partway.
    act(() => {
      vi.advanceTimersByTime(200);
    });
    const mid = surfaceText();
    expect(mid.length).toBeGreaterThan(0);
    expect(mid.length).toBeLessThan(SUMMARY_TEXT.length);

    // AssistantTurnShell raises the catch-up signal the moment an
    // agent_message in the same turn starts streaming. The row must
    // snap to full immediately so the user's eye follows the body
    // text rather than a still-filling title above it.
    rerender({ items: [fakeReadFileTool()], streaming: false });
    expect(surfaceText()).toBe(SUMMARY_TEXT);
  });

  it("still renders the section title when args haven't arrived yet", () => {
    // The action remains visible before streamed arguments provide a target.
    const inFlight: ThreadItem = {
      id: "tool-empty",
      type: "tool_call",
      status: "in_progress",
      name: "read_file",
    };
    mount({ items: [inFlight], streaming: true });
    expect(surfaceText()).toBe("查看");
  });

  it("renders a failed tool as a settled activity without an error notice", () => {
    mount({
      items: [{
        ...fakeReadFileTool(),
        status: "failed",
        error: "command failed",
      }],
      streaming: false,
    });

    expect(surfaceText()).toBe(SUMMARY_TEXT);
    expect(
      container?.querySelector(".activity-group")?.classList.contains("failed"),
    ).toBe(false);
    expect(
      container
        ?.querySelector(".tool-activity-marker")
        ?.classList.contains("is-settled"),
    ).toBe(true);
    expect(container?.querySelector(".activity-detail-error")).toBeNull();
  });
});
