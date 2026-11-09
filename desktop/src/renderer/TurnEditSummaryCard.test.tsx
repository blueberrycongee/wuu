import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { JsonValue, ThreadItem, Turn } from "../shared/protocol";
import { TurnEditSummaryCard } from "./TurnEditSummaryCard";

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

function buildEditItem(path: string, additions: number, deletions: number): ThreadItem {
  return {
    id: `item-${path}`,
    type: "tool_call",
    name: "edit_file",
    status: "completed",
    result: JSON.stringify({
      path,
      diff: {
        hunks: [
          {
            old_start: 1,
            new_start: 1,
            lines: [
              ...Array.from({ length: additions }, () => ({ op: "insert", content: "new" })),
              ...Array.from({ length: deletions }, () => ({ op: "delete", content: "old" })),
            ],
          },
        ],
      },
    }),
  };
}

function buildApplyPatchItem(): ThreadItem {
  const structuredContent: JsonValue = {
    files: [
      {
        path: "src/a.ts",
        action: "update",
        diff: {
          hunks: [
            {
              old_start: 2,
              new_start: 2,
              lines: [
                { op: "equal", content: "const oldName = true;" },
                { op: "delete", content: "export const value = oldName;" },
                { op: "insert", content: "export const value = true;" },
              ],
            },
          ],
        },
      },
      {
        path: "src/b.ts",
        action: "add",
        diff: { new_file: true, lines: 3 },
      },
    ],
  };
  return {
    id: "item-apply-patch",
    type: "tool_call",
    name: "apply_patch",
    status: "completed",
    arguments: JSON.stringify({
      patchText:
        "*** Begin Patch\n*** Update File: src/a.ts\n@@\n-old\n+new\n*** Add File: src/b.ts\n+first\n+second\n+third\n*** End Patch",
    }),
    result: "Success. Updated the following files:\nM src/a.ts\nA src/b.ts",
    result_detail: {
      content: [{
        type: "text",
        text: "Success. Updated the following files:\nM src/a.ts\nA src/b.ts",
      }],
      structured_content: structuredContent,
    },
  };
}

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(card: JSX.Element): void {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(card);
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

afterEach(() => {
  vi.useRealTimers();
  unmount();
});

describe("TurnEditSummaryCard", () => {
  it("returns null when no file edits are present", () => {
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [],
    };
    mount(<TurnEditSummaryCard turn={turn} />);
    expect(container?.querySelector('.turn-edit-summary-card')).toBeFalsy();
  });

  it("renders a card with file name and diff stats", () => {
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [buildEditItem("/tmp/.zshrc", 4, 1)],
    };
    mount(<TurnEditSummaryCard turn={turn} />);
    expect(container?.textContent).toContain("本轮修改 1 个文件");
    expect(container?.textContent).toContain(".zshrc");
    expect(container?.textContent).toContain("+4");
    expect(container?.textContent).toContain("-1");
    expect(container?.querySelector(".turn-edit-summary-icon")).toBeTruthy();
    expect(container?.querySelector(".turn-edit-summary-overview")).toBeTruthy();
    expect(container?.querySelector(".turn-edit-summary-header")).toBeFalsy();
    expect(container?.querySelector(".turn-edit-summary-row")).toBeFalsy();
  });

  it("shows the complete file path instead of dropping leading directories", () => {
    const path = "desktop/src/main/rendererServerEventBatcher.ts";
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [buildEditItem(path, 10, 0)],
    };

    mount(<TurnEditSummaryCard turn={turn} />);

    expect(
      container?.querySelector(".turn-edit-summary-overview-path")?.textContent,
    ).toBe(path);
  });

  it("waits until the final answer is ready", () => {
    const turn: Turn = {
      id: "turn-1",
      status: "in_progress",
      items_view: "full",
      items: [buildEditItem("/tmp/.zshrc", 4, 1)],
    };

    mount(<TurnEditSummaryCard turn={turn} />);

    expect(container?.querySelector(".turn-edit-summary-card")).toBeFalsy();
  });

  it("renders when the final answer is ready before turn settlement", () => {
    const turn: Turn = {
      id: "turn-1",
      status: "in_progress",
      answer_ready_at: "2026-08-29T04:00:10.000Z",
      items_view: "full",
      items: [
        buildEditItem("/tmp/.zshrc", 4, 1),
        {
          id: "answer-ready",
          type: "agent_message",
          status: "completed",
          terminal: true,
          text: "done",
        },
      ],
    };

    mount(<TurnEditSummaryCard turn={turn} />);

    expect(container?.textContent).toContain("本轮修改 1 个文件");
  });

  it("aggregates multiple edits and shows the file count", () => {
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [
        buildEditItem("/tmp/a.txt", 1, 0),
        buildEditItem("/tmp/b.txt", 0, 2),
      ],
    };
    mount(<TurnEditSummaryCard turn={turn} />);
    expect(container?.textContent).toContain("本轮修改 2 个文件");
    expect(container?.textContent).toContain("a.txt");
    expect(container?.textContent).toContain("b.txt");
  });

  it("renders each file from Codex apply_patch structured results with per-file stats", () => {
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [buildApplyPatchItem()],
    };

    mount(<TurnEditSummaryCard turn={turn} />);

    expect(container?.textContent).toContain("本轮修改 2 个文件");
    expect(container?.textContent).toContain("a.ts");
    expect(container?.textContent).toContain("b.ts");
    expect(container?.textContent).toContain("+1");
    expect(container?.textContent).toContain("-1");
    expect(container?.textContent).toContain("+3");
  });

  it("shows added apply_patch contents in the hover diff preview", () => {
    vi.useFakeTimers();
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [buildApplyPatchItem()],
    };

    mount(<TurnEditSummaryCard turn={turn} />);

    const row = container?.querySelectorAll<HTMLElement>(".turn-edit-summary-row")[1];
    expect(row).toBeTruthy();
    act(() => {
      row!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
      vi.advanceTimersByTime(300);
    });

    const preview = document.body.querySelector<HTMLElement>(".tool-diff-preview-card");
    expect(preview?.textContent).toContain("first");
    expect(preview?.textContent).toContain("second");
    expect(preview?.textContent).toContain("third");
    expect(preview?.querySelectorAll(".tool-diff-line-insert")).toHaveLength(3);
  });

  it("opens the selected file diff when a file row is clicked", () => {
    const onOpenFileDiff = vi.fn();
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [buildEditItem("/tmp/a.txt", 2, 1)],
    };

    mount(
      <TurnEditSummaryCard turn={turn} onOpenFileDiff={onOpenFileDiff} />,
    );

    const row = container?.querySelector<HTMLButtonElement>(
      ".turn-edit-summary-overview",
    );
    expect(row).toBeTruthy();

    act(() => {
      row?.click();
    });

    expect(onOpenFileDiff).toHaveBeenCalledTimes(1);
    expect(onOpenFileDiff).toHaveBeenCalledWith(
      expect.objectContaining({
        path: "/tmp/a.txt",
        additions: 2,
        deletions: 1,
        diff: expect.objectContaining({
          path: "/tmp/a.txt",
        }),
      }),
    );
  });

  it("uses the shared workspace file chain for the primary row click", () => {
    const onOpenFile = vi.fn();
    const onOpenFileDiff = vi.fn();
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [buildEditItem("docs/brief.md", 2, 1)],
    };

    mount(
      <TurnEditSummaryCard
        turn={turn}
        onOpenFile={onOpenFile}
        onOpenFileDiff={onOpenFileDiff}
      />,
    );
    act(() => {
      container?.querySelector<HTMLButtonElement>(".turn-edit-summary-overview")?.click();
    });

    expect(onOpenFile).toHaveBeenCalledWith("docs/brief.md");
    expect(onOpenFileDiff).not.toHaveBeenCalled();
  });

  it("restores an added apply_patch file as a turn snapshot", () => {
    const onOpenFileDiff = vi.fn();
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [buildApplyPatchItem()],
    };
    mount(<TurnEditSummaryCard turn={turn} onOpenFileDiff={onOpenFileDiff} />);

    act(() => {
      container?.querySelectorAll<HTMLButtonElement>(".turn-edit-summary-row")[1]?.click();
    });

    expect(onOpenFileDiff).toHaveBeenCalledWith(
      expect.objectContaining({
        path: "src/b.ts",
        action: "create",
        snapshotText: "first\nsecond\nthird\n",
      }),
    );
  });

  it("renders newly-created files from write_file", () => {
    const writeFileItem: ThreadItem = {
      id: "item-new",
      type: "tool_call",
      name: "write_file",
      status: "completed",
      arguments: JSON.stringify({
        path: "/tmp/new.txt",
        content: "# Draft\n\nBody\n",
      }),
      result: JSON.stringify({
        path: "/tmp/new.txt",
        new_file_sha: "sha256:abc123",
        diff: { new_file: true, lines: 7 },
      }),
    };
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [writeFileItem],
    };
    const onOpenFileDiff = vi.fn();
    mount(
      <TurnEditSummaryCard
        turn={turn}
        cwd="/tmp"
        onOpenFileDiff={onOpenFileDiff}
      />,
    );
    expect(container?.textContent).toContain("本轮修改 1 个文件");
    expect(container?.textContent).toContain("new.txt");
    expect(container?.querySelector(".turn-edit-summary-action")).toBeFalsy();
    expect(container?.textContent).toContain("+7");
    act(() => {
      container?.querySelector<HTMLButtonElement>(".turn-edit-summary-overview")?.click();
    });
    expect(onOpenFileDiff).toHaveBeenCalledWith(
      expect.objectContaining({
        path: "/tmp/new.txt",
        cwd: "/tmp",
        action: "create",
        snapshotText: "# Draft\n\nBody\n",
        afterSha: "sha256:abc123",
      }),
    );
  });

  it("shows newly-created file contents as additions in the hover preview", () => {
    vi.useFakeTimers();
    const writeFileItem: ThreadItem = {
      id: "item-new",
      type: "tool_call",
      name: "write_file",
      status: "completed",
      arguments: JSON.stringify({
        path: "/tmp/new.txt",
        content: "first line\nsecond line\n",
      }),
      result: JSON.stringify({
        path: "/tmp/new.txt",
        diff: { new_file: true, lines: 2 },
      }),
    };
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: [writeFileItem],
    };
    mount(<TurnEditSummaryCard turn={turn} />);

    const row = container?.querySelector<HTMLElement>(".turn-edit-summary-overview");
    act(() => {
      row!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
      vi.advanceTimersByTime(300);
    });

    const preview = document.body.querySelector<HTMLElement>(".tool-diff-preview-card");
    expect(preview?.textContent).toContain("first line");
    expect(preview?.textContent).toContain("second line");
    expect(preview?.querySelectorAll(".tool-diff-line-insert")).toHaveLength(2);
  });

  it("hides files beyond the visible limit", () => {
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: Array.from({ length: 7 }, (_, i) =>
        buildEditItem(`/tmp/file${i}.txt`, 1, 0),
      ),
    };
    mount(<TurnEditSummaryCard turn={turn} />);
    expect(container?.textContent).toContain("本轮修改 7 个文件");
    expect(container?.querySelectorAll(".turn-edit-summary-row")).toHaveLength(3);
    expect(container?.textContent).toContain("还有 4 个文件");
    expect(container?.textContent).toContain("再显示 3 个");
  });

  it("reveals additional files three at a time", () => {
    const turn: Turn = {
      id: "turn-1",
      status: "completed",
      items_view: "full",
      items: Array.from({ length: 7 }, (_, i) =>
        buildEditItem(`/tmp/file${i}.txt`, 1, 0),
      ),
    };
    mount(<TurnEditSummaryCard turn={turn} />);

    const showMore = () =>
      container?.querySelector<HTMLButtonElement>(".turn-edit-summary-more-button");

    expect(container?.querySelectorAll(".turn-edit-summary-row")).toHaveLength(3);
    expect(showMore()?.textContent).toContain("再显示 3 个");

    act(() => {
      showMore()?.click();
    });
    expect(container?.querySelectorAll(".turn-edit-summary-row")).toHaveLength(6);
    expect(showMore()?.textContent).toContain("再显示 1 个");
    expect(container?.textContent).toContain("还有 1 个文件");

    act(() => {
      showMore()?.click();
    });
    expect(container?.querySelectorAll(".turn-edit-summary-row")).toHaveLength(7);
    expect(showMore()).toBeFalsy();
  });
});
