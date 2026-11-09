import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ThreadItem } from "../shared/protocol";
import { ToolDiffPreview } from "./ToolDiffPreview";

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

function buildEditItem(result: string): ThreadItem {
  return {
    id: "item-1",
    type: "tool_call",
    name: "edit_file",
    status: "completed",
    result,
  };
}

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function rect(overrides: Partial<DOMRect>): DOMRect {
  return {
    x: overrides.x ?? overrides.left ?? 0,
    y: overrides.y ?? overrides.top ?? 0,
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    width: 0,
    height: 0,
    ...overrides,
    toJSON() {
      return this;
    },
  } as DOMRect;
}

function mount(
  item: ThreadItem,
  options: { conversationPaneRect?: DOMRect } = {},
): void {
  if (container) unmount();
  container = document.createElement("div");
  if (options.conversationPaneRect) {
    container.className = "conversation-pane";
    container.getBoundingClientRect = () => options.conversationPaneRect!;
  }
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(
      <ToolDiffPreview item={item}>
        <span>已编辑 .zshrc</span>
      </ToolDiffPreview>,
    );
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

describe("ToolDiffPreview", () => {
  it("renders children when no diff is present", () => {
    mount(buildEditItem('{"path": "/tmp/a.txt"}'));
    expect(container?.textContent).toContain("已编辑 .zshrc");
    expect(container?.querySelector(".tool-diff-preview-card")).toBeFalsy();
  });

  it("keeps the scrollable diff preview open when moving from trigger into the preview", () => {
    vi.useFakeTimers();
    mount(
      buildEditItem(
        JSON.stringify({
          path: "/tmp/a.txt",
          diff: {
            hunks: [
              {
                old_start: 1,
                new_start: 1,
                lines: Array.from({ length: 40 }, (_, index) => ({
                  op: index % 2 === 0 ? "equal" : "insert",
                  content: `line ${index}`,
                })),
              },
            ],
          },
        }),
      ),
    );

    const trigger = container?.querySelector<HTMLElement>(
      ".tool-diff-preview-trigger",
    );
    expect(trigger).toBeTruthy();

    act(() => {
      trigger!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
      vi.advanceTimersByTime(300);
    });

    const card = document.body.querySelector<HTMLElement>(
      ".tool-diff-preview-card",
    );
    expect(card).toBeTruthy();
    const body = card?.querySelector<HTMLElement>(".tool-diff-preview-body");
    expect(body?.className).toContain("tool-diff-preview-body");

    act(() => {
      trigger!.dispatchEvent(new MouseEvent("mouseout", { bubbles: true }));
      card!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
    });

    expect(document.body.querySelector(".tool-diff-preview-card")).toBeTruthy();
  });

  it("keeps the portaled preview inside the conversation pane", () => {
    vi.useFakeTimers();
    mount(
      buildEditItem(
        JSON.stringify({
          path: "/tmp/a.txt",
          diff: {
            hunks: [
              {
                old_start: 1,
                new_start: 1,
                lines: [{ op: "insert", content: "line 1" }],
              },
            ],
          },
        }),
      ),
      {
        conversationPaneRect: rect({
          left: 320,
          right: window.innerWidth,
          width: window.innerWidth - 320,
        }),
      },
    );

    const trigger = container?.querySelector<HTMLElement>(
      ".tool-diff-preview-trigger",
    );
    expect(trigger).toBeTruthy();
    trigger!.getBoundingClientRect = () =>
      rect({
        left: 260,
        right: 300,
        top: 500,
        bottom: 520,
        width: 40,
        height: 20,
      });

    act(() => {
      trigger!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
      vi.advanceTimersByTime(300);
    });

    const card = document.body.querySelector<HTMLElement>(
      ".tool-diff-preview-card",
    );
    expect(card?.style.left).toBe("344px");
  });
});
