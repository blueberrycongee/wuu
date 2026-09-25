import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ThreadContextMenu } from "./ThreadContextMenu";
import { Tooltip, TOOLTIP_MAX_CONTENT_LENGTH, tooltipContent } from "./Tooltip";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  document
    .querySelectorAll(".tooltip-layer")
    .forEach((layer) => layer.remove());
  vi.useRealTimers();
});

function renderTooltip(
  props: Partial<React.ComponentProps<typeof Tooltip>> = {},
): HTMLSpanElement {
  act(() => {
    root ??= createRoot(container);
    root.render(
      <WuuUIRoot>
        <Tooltip content={props.content ?? "提示文案"} {...props}>
          {props.children ?? <button type="button">触发</button>}
        </Tooltip>
      </WuuUIRoot>,
    );
  });
  const trigger = container.querySelector(".tooltip-trigger");
  if (!(trigger instanceof HTMLSpanElement)) {
    throw new Error("tooltip trigger wrapper not rendered");
  }
  return trigger;
}

function pointerOver(element: Element): void {
  act(() => {
    element.dispatchEvent(new Event("pointerover", { bubbles: true }));
  });
}

function pointerOut(element: Element): void {
  act(() => {
    element.dispatchEvent(new Event("pointerout", { bubbles: true }));
  });
}

function pointerDown(element: Element): void {
  act(() => {
    element.dispatchEvent(new Event("pointerdown", { bubbles: true }));
  });
}

function tooltipLayer(): HTMLElement | null {
  return document.querySelector(".tooltip-layer");
}

describe("Tooltip", () => {
  it("opens after the hover delay in the protected tooltip layer", () => {
    const trigger = renderTooltip();
    expect(tooltipLayer()).toBeNull();
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(399);
    });
    expect(tooltipLayer()).toBeNull();
    act(() => {
      vi.advanceTimersByTime(1);
    });
    const layer = tooltipLayer();
    expect(layer).not.toBeNull();
    expect(layer?.textContent).toBe("提示文案");
    expect(layer?.getAttribute("role")).toBe("tooltip");
    expect(layer?.closest('[data-wuu-layer-host="true"]')).not.toBeNull();
    // Portaled to the protected host, not inside the trigger wrapper.
    expect(trigger.contains(layer)).toBe(false);
  });

  it("closes on pointer out", () => {
    const trigger = renderTooltip();
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(tooltipLayer()).not.toBeNull();
    pointerOut(trigger);
    expect(tooltipLayer()).toBeNull();
  });

  it("never arms while disabled or contentless", () => {
    const trigger = renderTooltip({ disabled: true });
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(tooltipLayer()).toBeNull();

    const empty = renderTooltip({ content: undefined });
    pointerOver(empty);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(tooltipLayer()).toBeNull();
  });

  it("dismisses on pointer down and stays suppressed until pointer leave", () => {
    const trigger = renderTooltip();
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(tooltipLayer()).not.toBeNull();
    pointerDown(trigger);
    expect(tooltipLayer()).toBeNull();
    // Still hovering: must not re-arm.
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(tooltipLayer()).toBeNull();
    // After the pointer leaves and returns, the tooltip arms again.
    pointerOut(trigger);
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(tooltipLayer()).not.toBeNull();
  });

  it("closes on Escape", () => {
    const trigger = renderTooltip();
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(tooltipLayer()).not.toBeNull();
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    });
    expect(tooltipLayer()).toBeNull();
  });

  it("closes on scroll", () => {
    const trigger = renderTooltip();
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(tooltipLayer()).not.toBeNull();
    act(() => {
      window.dispatchEvent(new Event("scroll"));
    });
    expect(tooltipLayer()).toBeNull();
  });

  it("stays closed while a context menu is open", () => {
    function renderSurface(menuOpen: boolean): void {
      act(() => {
        root ??= createRoot(container);
        root.render(
          <WuuUIRoot>
            <Tooltip content="提示文案">
              <button type="button">触发</button>
            </Tooltip>
            {menuOpen ? (
              <ThreadContextMenu
                x={10}
                y={10}
                items={[{ label: "操作", onSelect: () => {} }]}
                onClose={() => {}}
              />
            ) : null}
          </WuuUIRoot>,
        );
      });
    }
    renderSurface(false);
    const trigger = container.querySelector(".tooltip-trigger")!;
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(tooltipLayer()).not.toBeNull();

    // Opening a menu without a press on the trigger (keyboard, or a menu
    // elsewhere) still retires the hint.
    renderSurface(true);
    expect(tooltipLayer()).toBeNull();
    // The pointer crosses the menu and returns to the trigger.
    pointerOut(trigger);
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(tooltipLayer()).toBeNull();

    renderSurface(false);
    pointerOut(trigger);
    pointerOver(trigger);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(tooltipLayer()).not.toBeNull();
  });

  it("opens immediately when another tooltip closed moments ago (skip delay)", () => {
    const first = renderTooltip();
    pointerOver(first);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    pointerOut(first);

    act(() => {
      root!.render(
        <Tooltip content="第二个">
          <button type="button">第二个触发</button>
        </Tooltip>,
      );
    });
    const second = container.querySelector(".tooltip-trigger")!;
    pointerOver(second);
    // No delay paid the second time — but the open still lands on a
    // zero-length timer tick rather than synchronously.
    act(() => {
      vi.advanceTimersByTime(0);
    });
    expect(tooltipLayer()?.textContent).toBe("第二个");
  });
});

describe("tooltipContent", () => {
  it("passes short content through unchanged", () => {
    expect(tooltipContent("发送")).toBe("发送");
    const exact = "x".repeat(TOOLTIP_MAX_CONTENT_LENGTH);
    expect(tooltipContent(exact)).toBe(exact);
  });

  it("caps long content with an ellipsis", () => {
    const long = "全文内容".repeat(100);
    const capped = tooltipContent(long);
    expect(capped.length).toBeLessThanOrEqual(TOOLTIP_MAX_CONTENT_LENGTH);
    expect(capped.endsWith("…")).toBe(true);
    expect(capped).not.toBe(long);
  });
});
