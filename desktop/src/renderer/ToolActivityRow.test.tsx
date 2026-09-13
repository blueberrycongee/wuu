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
