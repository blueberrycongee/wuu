/**
 * Smoke tests for `ConversationForkDialog`. The renderer doesn't have
 * `@testing-library/react`, so we drive the component through
 * `react-dom/client.createRoot` directly. These tests intentionally
 * stay narrow: they assert that the two option buttons trigger the
 * right `onChoose` mode and that the picker disables itself while a
 * chosen option's promise is still in flight. Dismissal is covered by
 * the shared `Modal` tests. Visual layout is exercised by
 * manual review of the new `.fork-dialog*` CSS block — see
 * `styles/environment.css`.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, createElement, type ReactElement } from "react";
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

function buttonByLabel(label: string): HTMLButtonElement {
  const node = document.querySelector(
    `button[aria-label="${label}"]`,
  );
  if (!(node instanceof HTMLButtonElement)) {
    throw new Error(`expected <button aria-label="${label}"> to be rendered`);
  }
  return node;
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
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("ConversationForkDialog", () => {
  it("invokes onChoose(\"local\") when the local option is clicked", async () => {
    const onChoose = vi.fn(() => Promise.resolve());
    const onCancel = vi.fn();

    mount(
      createElement(ConversationForkDialog, { onCancel, onChoose }),
    );

    await act(async () => {
      buttonByLabel("派生到本地").click();
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
      buttonByLabel("派生到 git worktree").click();
      await Promise.resolve();
    });

    expect(onChoose).toHaveBeenCalledWith("worktree");
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("disables only the worktree option when the current workspace is not a git repo", async () => {
    const onChoose = vi.fn(() => Promise.resolve());
    const onCancel = vi.fn();

    mount(
      createElement(ConversationForkDialog, {
        onCancel,
        onChoose,
        worktreeDisabledReason: "当前工作目录不是 git 仓库，不能创建 git worktree",
      }),
    );

    expect(buttonByLabel("派生到本地").disabled).toBe(false);
    expect(buttonByLabel("派生到 git worktree").disabled).toBe(true);
    expect(document.body.textContent ?? "").toContain(
      "当前工作目录不是 git 仓库",
    );

    await act(async () => {
      buttonByLabel("派生到 git worktree").click();
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
      buttonByLabel("派生到本地").click();
    });

    expect(onChoose).toHaveBeenCalledWith("local");
    expect(buttonByLabel("派生到本地").disabled).toBe(true);
    expect(buttonByLabel("派生到 git worktree").disabled).toBe(true);

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
      buttonByLabel("派生到本地").click();
      await Promise.resolve();
    });

    expect(document.querySelector('[role="alert"]')?.textContent).toBe(
      "fork failed",
    );
    expect(buttonByLabel("派生到本地").disabled).toBe(false);
    expect(buttonByLabel("派生到 git worktree").disabled).toBe(false);
  });
});
