/**
 * Behaviour of `ConversationForkDialog`. The renderer doesn't have
 * `@testing-library/react`, so we drive the component through
 * `react-dom/client.createRoot` directly. Options are found by their
 * order (local first, worktree second), not by their copy. Backdrop and
 * Escape dismissal and focus return are verified through the real shared Modal.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, createElement, useState, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ConversationForkDialog } from "./ConversationForkDialog";

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

function options(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>(".fork-dialog-option")];
}

function localOption(): HTMLButtonElement {
  return options()[0]!;
}

function worktreeOption(): HTMLButtonElement {
  return options()[1]!;
}

function pressKey(target: Element, key: string): void {
  act(() => {
    target.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
  });
}

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container?.remove();
  container = null;
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("ConversationForkDialog", () => {
  it("names itself with a visible title", () => {
    mount(createElement(ConversationForkDialog, { onCancel: vi.fn(), onChoose: vi.fn() }));

    const dialog = document.querySelector('[role="dialog"]');
    const title = dialog?.querySelector("h2")?.textContent?.trim();
    expect(title).toBeTruthy();
    expect(dialog?.getAttribute("aria-label")).toBe(title);
    expect(options()).toHaveLength(2);
  });

  it.each(["backdrop", "Escape"])("dismisses via %s without forking and restores focus on repeated opens", (dismissal) => {
    const onChoose = vi.fn();
    function Harness(): ReactElement {
      const [open, setOpen] = useState(false);
      return createElement("div", null,
        createElement("button", { type: "button", onClick: () => setOpen(true), "data-testid": "fork-opener" }, "Fork"),
        open ? createElement(ConversationForkDialog, { onCancel: () => setOpen(false), onChoose }) : null,
      );
    }
    mount(createElement(Harness));
    const opener = document.querySelector<HTMLButtonElement>('[data-testid="fork-opener"]')!;
    for (let attempt = 0; attempt < 2; attempt += 1) {
      act(() => { opener.focus(); opener.click(); });
      const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
      // Only the two destinations are actions; dismissal does not compete with them.
      expect([...dialog.querySelectorAll("button")]).toEqual(options());
      expect(document.activeElement).toBe(localOption());
      act(() => dialog.querySelector<HTMLElement>("h2")!.click());
      expect(document.querySelector('[role="dialog"]')).toBe(dialog);
      act(() => worktreeOption().focus());
      pressKey(worktreeOption(), "Tab");
      expect(document.activeElement).toBe(localOption());
      if (dismissal === "backdrop") {
        act(() => document.querySelector<HTMLElement>(".modal-backdrop")!.click());
      } else {
        pressKey(localOption(), "Escape");
      }
      expect(document.querySelector('[role="dialog"]')).toBeNull();
      expect(document.activeElement).toBe(opener);
      expect(onChoose).not.toHaveBeenCalled();
    }
  });

  it("invokes onChoose(\"local\") when the local option is clicked", async () => {
    const onChoose = vi.fn(() => Promise.resolve());
    const onCancel = vi.fn();

    mount(
      createElement(ConversationForkDialog, { onCancel, onChoose }),
    );

    await act(async () => {
      localOption().click();
      await Promise.resolve();
    });

    expect(onChoose).toHaveBeenCalledTimes(1);
    expect(onChoose).toHaveBeenCalledWith("local");
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("invokes onChoose(\"worktree\") when the worktree option is clicked", async () => {
    const onChoose = vi.fn(() => Promise.resolve());
    const onCancel = vi.fn();

    mount(
      createElement(ConversationForkDialog, { onCancel, onChoose }),
    );

    await act(async () => {
      worktreeOption().click();
      await Promise.resolve();
    });

    expect(onChoose).toHaveBeenCalledWith("worktree");
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("disables only the worktree option when the current workspace is not a git repo", async () => {
    const onChoose = vi.fn(() => Promise.resolve());
    const onCancel = vi.fn();
    const reason = "not a git repository";

    mount(
      createElement(ConversationForkDialog, {
        onCancel,
        onChoose,
        worktreeDisabledReason: reason,
      }),
    );

    expect(localOption().disabled).toBe(false);
    expect(worktreeOption().disabled).toBe(true);
    expect(worktreeOption().textContent).toContain(reason);

    await act(async () => {
      worktreeOption().click();
      await Promise.resolve();
    });

    expect(onChoose).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("disables every action button while the chosen promise is in flight", async () => {
    let resolveChoose: () => void = () => undefined;
    const choosePromise = new Promise<void>((resolve) => {
      resolveChoose = resolve;
    });
    const onChoose = vi.fn(() => choosePromise);
    const onCancel = vi.fn();

    mount(
      createElement(ConversationForkDialog, { onCancel, onChoose }),
    );

    act(() => {
      localOption().click();
    });

    expect(onChoose).toHaveBeenCalledWith("local");
    expect(localOption().disabled).toBe(true);
    expect(worktreeOption().disabled).toBe(true);
    expect(localOption().getAttribute("aria-busy")).toBe("true");
    pressKey(localOption(), "Escape");
    act(() => document.querySelector<HTMLElement>(".modal-backdrop")!.click());
    expect(onCancel).not.toHaveBeenCalled();
    expect(onChoose).toHaveBeenCalledTimes(1);

    // Resolve inside act() so the busy-mode-reset state update lands
    // inside a flushed transition — otherwise React 19 logs a noisy
    // "not wrapped in act" warning during unmount.
    await act(async () => {
      resolveChoose();
      await Promise.resolve();
    });
  });

  it("shows a fork error and restores the dialog actions", async () => {
    const onChoose = vi.fn(() => Promise.reject(new Error("fork failed")));

    mount(
      createElement(ConversationForkDialog, {
        onCancel: () => undefined,
        onChoose,
      }),
    );

    await act(async () => {
      localOption().click();
      await Promise.resolve();
    });

    expect(document.querySelector('[role="alert"]')?.textContent).toBe(
      "fork failed",
    );
    expect(localOption().disabled).toBe(false);
    expect(worktreeOption().disabled).toBe(false);
  });

  it("moves between enabled options with the arrow keys", () => {
    mount(createElement(ConversationForkDialog, { onCancel: vi.fn(), onChoose: vi.fn() }));

    expect(document.activeElement).toBe(localOption());
    pressKey(localOption(), "ArrowDown");
    expect(document.activeElement).toBe(worktreeOption());
    pressKey(worktreeOption(), "ArrowDown");
    expect(document.activeElement).toBe(localOption());
    pressKey(localOption(), "ArrowUp");
    expect(document.activeElement).toBe(worktreeOption());
  });

  it("skips a disabled option with the arrow keys", () => {
    mount(createElement(ConversationForkDialog, {
      onCancel: vi.fn(),
      onChoose: vi.fn(),
      worktreeDisabledReason: "not a git repository",
    }));

    pressKey(localOption(), "ArrowDown");
    expect(document.activeElement).toBe(localOption());
  });
});
