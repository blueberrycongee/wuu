import { afterEach, describe, expect, it } from "vitest";
import { act, createElement, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { Thread } from "../shared/protocol";
import { WorktreeNotice } from "./WorktreeNotice";

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

describe("WorktreeNotice", () => {
  it("renders foldable current worktree metadata", () => {
    mount(createElement(WorktreeNotice, { thread: worktreeForkThread() }));

    const details = document.querySelector(".fork-worktree-card");
    const facts = [...document.querySelectorAll(".fork-worktree-meta dd")].map((fact) => fact.textContent);

    expect(details).toHaveProperty("open", false);
    expect(facts).toEqual(["/repo/project", "d955824f", "/Users/me/.wuu/worktrees/fork-1/project"]);
    expect(document.querySelector(".fork-worktree-copy")).not.toBeNull();
  });

  it("does not render for local forks", () => {
    const thread = worktreeForkThread();
    delete thread.worktree;

    mount(createElement(WorktreeNotice, { thread }));

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
