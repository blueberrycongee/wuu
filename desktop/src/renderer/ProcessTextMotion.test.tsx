import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AnimatedProcessText } from "./ProcessTextMotion";
import { ConversationRenderActivityProvider } from "./ConversationRenderActivity";

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(text: string): void {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(<AnimatedProcessText text={text} />);
  });
}

function rerender(text: string): void {
  act(() => {
    root!.render(<AnimatedProcessText text={text} />);
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
  unmount();
});

describe("AnimatedProcessText", () => {
  it("does not mark the initial text as an entering transition", () => {
    mount("正在思考");

    const current = container?.querySelector(".process-text-motion-current");
    expect(current?.textContent).toBe("正在思考");
    expect(current?.classList.contains("process-text-motion-enter")).toBe(
      false,
    );
  });

  it("marks replacement text as an entering transition", () => {
    mount("正在思考");
    rerender("思考过程");

    const current = container?.querySelector(".process-text-motion-current");
    const exit = container?.querySelector(".process-text-motion-exit");
    expect(current?.textContent).toBe("思考过程");
    expect(current?.classList.contains("process-text-motion-enter")).toBe(
      true,
    );
    expect(exit?.textContent).toBe("正在思考");
  });

  it("does not mark catch-up text as an entering transition", () => {
    if (container) unmount();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    act(() => {
      root!.render(
        <ConversationRenderActivityProvider active={false}>
          <AnimatedProcessText text="正在思考" />
        </ConversationRenderActivityProvider>,
      );
    });
    act(() => {
      root!.render(
        <ConversationRenderActivityProvider active>
          <AnimatedProcessText text="思考过程" />
        </ConversationRenderActivityProvider>,
      );
    });

    const current = container.querySelector(".process-text-motion-current");
    expect(current?.textContent).toBe("思考过程");
    expect(current?.classList.contains("process-text-motion-enter")).toBe(false);
    expect(container.querySelector(".process-text-motion-exit")).toBeNull();
  });

  it("tweens the container width from the outgoing text to the incoming text", () => {
    const getRect = vi
      .spyOn(HTMLElement.prototype, "getBoundingClientRect")
      .mockImplementation(function (this: HTMLElement) {
        const width = this.classList.contains("process-text-motion-exit")
          ? 160
          : 48;
        return {
          x: 0,
          y: 0,
          top: 0,
          left: 0,
          right: width,
          bottom: 0,
          width,
          height: 0,
          toJSON() {
            return this;
          },
        } as DOMRect;
      });

    try {
      mount("这是一段比较长的工具调用聚合文本");
      rerender("查看");

      const motion = container?.querySelector(
        ".process-text-motion",
      ) as HTMLElement | null;
      // The layout effect freezes at the outgoing width and then commits to
      // the incoming width so the stylesheet's width transition can animate
      // the reflow instead of snapping it in a single frame.
      expect(motion?.style.width).toBe("48px");
    } finally {
      getRect.mockRestore();
    }
  });
});
