import { afterEach, describe, expect, it } from "vitest";
import { act, createElement, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { Thread } from "../shared/protocol";
import { ForkWorktreeNotice } from "./ForkWorktreeNotice";

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
  container?.remove();
  container = null;
});

describe("ForkWorktreeNotice", () => {
  it("renders a foldable worktree creation record", () => {
    mount(createElement(ForkWorktreeNotice, { thread: worktreeForkThread() }));

    const details = document.querySelector(".fork-worktree-card");
    const code = document.querySelector(".fork-worktree-code");

    expect(details).toHaveProperty("open", false);
    expect(code?.textContent).toContain("分离 HEAD d955824f");
    expect(code?.textContent).toContain("基础仓库 /repo/project");
    expect(code?.textContent).toContain(
      "工作树已创建于 /Users/me/.wuu/worktrees/fork-1/project",
    );
  });

  it("does not render for local forks", () => {
    const thread = worktreeForkThread();
    delete thread.worktree;

    mount(createElement(ForkWorktreeNotice, { thread }));

    expect(document.querySelector(".fork-worktree-notice")).toBeNull();
  });
});

function worktreeForkThread(): Thread {
  return {
    id: "thread-worktree",
    preview: "preview",
    model_provider: "fake",
    model: "fake-model",
    cwd: "/Users/me/.wuu/worktrees/fork-1/project",
    status: "idle",
    forked_from_id: "thread-source",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    worktree: {
      path: "/Users/me/.wuu/worktrees/fork-1/project",
      base_repo: "/repo/project",
      base_head: "d955824f12345678",
    },
    turns: [],
  };
}
