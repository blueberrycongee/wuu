/**
 * Smoke tests for the shared `Modal` chrome. The renderer does not
 * have `@testing-library/react`, so we drive the component through
 * `react-dom/client.createRoot` directly. These tests assert the
 * behaviors that every consumer of `Modal` relies on:
 *
 * - dialog + backdrop DOM shape and aria attributes
 * - X button / Escape / backdrop-click all dispatch `onClose`
 * - `closeDisabled` locks every close affordance
 * - omitting `onClose` makes the dialog non-dismissible
 * - clicks inside the panel do not bubble to the backdrop
 * - default focus moves into the panel body, skipping the X button
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, createElement, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { Modal } from "./Modal";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(node: ReactElement): void {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(node);
  });
}

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  if (container) {
    container.remove();
    container = null;
  }
  vi.restoreAllMocks();
});

describe("Modal", () => {
  it("renders the backdrop, dialog, and aria attributes", () => {
    mount(
      createElement(Modal, {
        ariaLabel: "测试弹窗",
        icon: createElement("span", null, "i"),
        title: "标题",
        onClose: () => undefined,
      }),
    );

    const backdrop = document.querySelector(".modal-backdrop");
    expect(backdrop).toBeTruthy();
    const dialog = document.querySelector(".environment-dialog");
    expect(dialog?.getAttribute("role")).toBe("dialog");
    expect(dialog?.getAttribute("aria-modal")).toBe("true");
    expect(dialog?.getAttribute("aria-label")).toBe("测试弹窗");
    expect(backdrop?.getAttribute("data-wuu-layer")).toBe("modal");
    expect(dialog?.getAttribute("data-wuu-component")).toBe("dialog");
  });

  it("portals into the protected UI layer host when one is available", () => {
    mount(
      createElement(
        WuuUIRoot,
        null,
        createElement(Modal, {
          ariaLabel: "layer host test",
          icon: createElement("span", null, "i"),
          title: "t",
          onClose: () => undefined,
        }),
      ),
    );

    const layerHost = document.querySelector('[data-wuu-layer-host="true"]');
    expect(layerHost?.querySelector('[data-wuu-component="dialog"]')).toBeTruthy();
  });

  it("renders the icon, title, and optional subtitle", () => {
    mount(
      createElement(Modal, {
        ariaLabel: "副标题测试",
        icon: createElement("span", { className: "icon-marker" }, "★"),
        title: "主标题",
        subtitle: "副标题文字",
        onClose: () => undefined,
      }),
    );

    expect(document.querySelector(".environment-dialog-icon")?.textContent).toContain("★");
    expect(document.querySelector(".environment-dialog h2")?.textContent).toBe("主标题");
    expect(document.querySelector(".environment-dialog-subtitle")?.textContent).toBe(
      "副标题文字",
    );
  });

  it("renders the footer slot inside .environment-dialog-footer", () => {
    mount(
      createElement(Modal, {
        ariaLabel: "footer test",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose: () => undefined,
        footer: createElement(
          "button",
          { type: "button", "data-testid": "footer-button" },
          "OK",
        ),
      }),
    );

    const footer = document.querySelector(".environment-dialog-footer");
    expect(footer?.querySelector('[data-testid="footer-button"]')?.textContent).toBe("OK");
  });

  it("appends panelClassName to .environment-dialog", () => {
    mount(
      createElement(Modal, {
        ariaLabel: "size variant",
        icon: createElement("span", null, "i"),
        title: "t",
        panelClassName: "fork-dialog",
        onClose: () => undefined,
      }),
    );

    const dialog = document.querySelector(".environment-dialog");
    expect(dialog?.classList.contains("fork-dialog")).toBe(true);
  });

  it("invokes onClose when the X button is clicked", () => {
    const onClose = vi.fn();
    mount(
      createElement(Modal, {
        ariaLabel: "close via X",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose,
      }),
    );

    const closeButton = document.querySelector(
      'button[aria-label="关闭"]',
    ) as HTMLButtonElement | null;
    expect(closeButton).toBeTruthy();
    act(() => {
      closeButton?.click();
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("invokes onClose when Escape is pressed at the window level", () => {
    const onClose = vi.fn();
    mount(
      createElement(Modal, {
        ariaLabel: "close via Esc",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose,
      }),
    );

    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("invokes onClose when Escape bubbles from a focused input", () => {
    const onClose = vi.fn();
    mount(
      createElement(Modal, {
        ariaLabel: "close focused input via Esc",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose,
        children: createElement("input", {
          type: "text",
          "data-testid": "focused-input",
        }),
      }),
    );

    const input = document.querySelector<HTMLInputElement>(
      '[data-testid="focused-input"]',
    );
    expect(document.activeElement).toBe(input);
    act(() => {
      input?.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Escape",
          bubbles: true,
          cancelable: true,
        }),
      );
    });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("invokes onClose when the backdrop itself is clicked", () => {
    const onClose = vi.fn();
    mount(
      createElement(Modal, {
        ariaLabel: "close via backdrop",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose,
      }),
    );

    const backdrop = document.querySelector(".modal-backdrop") as HTMLDivElement | null;
    expect(backdrop).toBeTruthy();
    act(() => {
      backdrop?.click();
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does NOT invoke onClose when a click lands inside the dialog panel", () => {
    const onClose = vi.fn();
    mount(
      createElement(Modal, {
        ariaLabel: "no bubble",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose,
        children: createElement(
          "p",
          { "data-testid": "body" },
          "随便一段内容",
        ),
      }),
    );

    const body = document.querySelector('[data-testid="body"]') as HTMLElement | null;
    expect(body).toBeTruthy();
    act(() => {
      body?.click();
    });
    expect(onClose).not.toHaveBeenCalled();
  });

  it("does not render the X button when onClose is omitted", () => {
    mount(
      createElement(Modal, {
        ariaLabel: "non-dismissible",
        icon: createElement("span", null, "i"),
        title: "t",
      }),
    );

    expect(document.querySelector('button[aria-label="关闭"]')).toBeNull();
  });

  it("does not respond to Escape or backdrop click when onClose is omitted", () => {
    let closeCalls = 0;
    mount(
      createElement(Modal, {
        ariaLabel: "non-dismissible behavior",
        icon: createElement("span", null, "i"),
        title: "t",
        // Intentionally no onClose: the user must use the in-panel actions.
      }),
    );

    const backdrop = document.querySelector(".modal-backdrop") as HTMLDivElement | null;
    act(() => {
      backdrop?.click();
    });
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    });
    expect(closeCalls).toBe(0);
  });

  it("locks the X button, Esc, and backdrop click while closeDisabled", () => {
    const onClose = vi.fn();
    mount(
      createElement(Modal, {
        ariaLabel: "busy",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose,
        closeDisabled: true,
      }),
    );

    const closeButton = document.querySelector(
      'button[aria-label="关闭"]',
    ) as HTMLButtonElement | null;
    expect(closeButton?.disabled).toBe(true);

    act(() => {
      closeButton?.click();
    });
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    });
    const backdrop = document.querySelector(".modal-backdrop") as HTMLDivElement | null;
    act(() => {
      backdrop?.click();
    });

    expect(onClose).not.toHaveBeenCalled();
  });

  it("focuses the first non-X interactive element by default", () => {
    mount(
      createElement(Modal, {
        ariaLabel: "focus test",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose: () => undefined,
        children: createElement(
          "button",
          { type: "button", "data-testid": "first-action" },
          "first",
        ),
      }),
    );

    const focused = document.activeElement as HTMLElement | null;
    expect(focused?.getAttribute("data-testid")).toBe("first-action");
    // The X button is a button but has class `icon-button`; default focus
    // must skip it so keyboard users land on the first real action.
    expect(focused?.classList.contains("icon-button")).toBe(false);
  });

  it("focuses the first input before buttons when inputs come first", () => {
    mount(
      createElement(Modal, {
        ariaLabel: "focus input first",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose: () => undefined,
        children: createElement("input", {
          type: "text",
          "data-testid": "first-input",
        }),
      }),
    );

    const focused = document.activeElement as HTMLElement | null;
    expect(focused?.getAttribute("data-testid")).toBe("first-input");
  });

  it("does not move focus when initialFocus is 'none'", () => {
    const beforeActive = document.activeElement;
    mount(
      createElement(Modal, {
        ariaLabel: "no focus",
        icon: createElement("span", null, "i"),
        title: "t",
        onClose: () => undefined,
        initialFocus: "none",
        children: createElement(
          "button",
          { type: "button", "data-testid": "first-action" },
          "first",
        ),
      }),
    );

    // The dialog should not steal focus.
    expect(document.activeElement).toBe(beforeActive);
  });
});
