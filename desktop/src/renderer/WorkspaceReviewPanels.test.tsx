import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WorkspaceReviewPanel } from "./WorkspaceReviewPanels";
import type { GitChangeFile, GitChangesResult, GitFileDiffResult } from "../shared/protocol";

vi.mock("./WorkspaceMonacoDiffEditor", () => ({
  WorkspaceMonacoDiffEditor: ({ path }: { path: string }) => (
    <div className="workspace-monaco-diff-editor" data-path={path} />
  ),
}));

let container: HTMLDivElement | null = null;
let root: Root | null = null;
const originalWuu = (window as unknown as { wuu?: unknown }).wuu;

function mount(element: JSX.Element): void {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(element);
  });
}

async function flushReviewEffects(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function installGitReviewStub(files: GitChangeFile[]): {
  listGitChanges: ReturnType<typeof vi.fn>;
  readGitFileDiff: ReturnType<typeof vi.fn>;
} {
  const first = files[0];
  const changes: GitChangesResult = {
    is_repo: true,
    root: "/repo",
    files,
  };
  const diff: GitFileDiffResult = {
    is_repo: true,
    path: first?.path ?? "",
    status: first?.status ?? "modified",
    additions: first?.additions ?? 0,
    deletions: first?.deletions ?? 0,
    binary: first?.binary,
    patch: [
      `diff --git a/${first?.path ?? "file.ts"} b/${first?.path ?? "file.ts"}`,
      "@@ -1 +1 @@",
      "-old value",
      "+new value that is deliberately long enough to exercise wrapping in the review pane",
    ].join("\n"),
    original_text: "old value\n",
    modified_text: "new value\n",
    truncated: false,
  };
  const listGitChanges = vi.fn().mockResolvedValue(changes);
  const readGitFileDiff = vi.fn().mockResolvedValue(diff);
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      listGitChanges,
      readGitFileDiff,
    },
  });
  return { listGitChanges, readGitFileDiff };
}

function restoreWuu(): void {
  if (originalWuu === undefined) {
    delete (window as unknown as { wuu?: unknown }).wuu;
    return;
  }
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: originalWuu,
  });
}

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container?.remove();
  container = null;
  restoreWuu();
});

function click(element: Element | null | undefined): void {
  act(() => {
    (element as HTMLElement).click();
  });
}

function reviewRow(name: string): HTMLButtonElement | undefined {
  return Array.from(container?.querySelectorAll<HTMLButtonElement>(".workspace-diff-tree-row.file") ?? [])
    .find((row) => row.querySelector(".workspace-diff-tree-name")?.textContent === name);
}

const THREE_FILES: GitChangeFile[] = [
  { path: "app/one.ts", status: "modified", additions: 1, deletions: 1 },
  { path: "app/two.ts", status: "added", additions: 3, deletions: 0 },
  { path: "lib/three.ts", status: "deleted", additions: 0, deletions: 2 },
];

describe("WorkspaceReviewPanel", () => {
  it("folds the change list into a switcher when the panel is too narrow for both", async () => {
    const api = installGitReviewStub(THREE_FILES);
    mount(<WorkspaceReviewPanel gitStatus={{ is_repo: true, branch: "main", dirty_count: 3 }} workspaceRoot="/repo" />);
    await flushReviewEffects();

    expect(container?.querySelector(".workspace-review-tree-pane")).toBeNull();
    expect(container?.querySelector(".workspace-review-resizer")).toBeNull();
    const switcher = container?.querySelector<HTMLButtonElement>(".workspace-review-switcher");
    expect(switcher?.getAttribute("aria-expanded")).toBe("false");

    click(switcher);
    expect(switcher?.getAttribute("aria-expanded")).toBe("true");
    expect(document.activeElement).toBe(container?.querySelector(".workspace-diff-search"));

    click(reviewRow("two.ts"));
    await flushReviewEffects();
    expect(api.readGitFileDiff).toHaveBeenLastCalledWith("app/two.ts", "/repo");
    expect(switcher?.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(switcher);

    const [previous, next] = Array.from(
      container?.querySelectorAll<HTMLButtonElement>(".workspace-review-stepper button") ?? [],
    );
    click(next);
    await flushReviewEffects();
    expect(api.readGitFileDiff).toHaveBeenLastCalledWith("lib/three.ts", "/repo");
    expect(next.disabled).toBe(true);
    expect(previous.disabled).toBe(false);

    click(switcher);
    act(() => {
      document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });
    expect(switcher?.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(switcher);
  });

  it("keeps the change list beside the diff when the panel has room", async () => {
    const rect = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      const width = this.classList.contains("workspace-review-panel") ? 900 : 0;
      return { width, height: 0, x: 0, y: 0, top: 0, left: 0, right: width, bottom: 0, toJSON: () => ({}) } as DOMRect;
    });
    try {
      installGitReviewStub(THREE_FILES);
      mount(<WorkspaceReviewPanel gitStatus={{ is_repo: true, branch: "main", dirty_count: 3 }} workspaceRoot="/repo" />);
      await flushReviewEffects();

      expect(container?.querySelector(".workspace-review-switcher")).toBeNull();
      expect(container?.querySelector(".workspace-review-resizer")).toBeTruthy();
      expect(container?.querySelector(".workspace-review-tree-pane .workspace-diff-tree-row")).toBeTruthy();
    } finally {
      rect.mockRestore();
    }
  });

  it("uses the full review width for a single changed file", async () => {
    const api = installGitReviewStub([
      {
        path: "desktop/src/renderer/styles/sidebar.css",
        status: "modified",
        additions: 4,
        deletions: 5,
      },
    ]);

    mount(
      <WorkspaceReviewPanel
        gitStatus={{ is_repo: true, branch: "main", dirty_count: 1 }}
        workspaceRoot="/repo/worktree"
      />,
    );
    await flushReviewEffects();

    expect(api.listGitChanges).toHaveBeenCalledWith("/repo/worktree");
    expect(api.readGitFileDiff).toHaveBeenCalledWith(
      "desktop/src/renderer/styles/sidebar.css",
      "/repo/worktree",
    );

    const panel = container?.querySelector<HTMLElement>(".workspace-review-panel");
    expect(panel?.classList.contains("single-file")).toBe(true);
    expect(container?.querySelector(".workspace-review-diff-panel")).toBeTruthy();
    expect(container?.querySelector(".workspace-monaco-diff-editor")).toBeTruthy();
    expect(container?.querySelector(".workspace-review-tree-pane")).toBeNull();
    expect(container?.querySelector(".workspace-review-resizer")).toBeNull();
  });
});
