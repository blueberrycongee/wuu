import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WorkspaceReviewPanel } from "./WorkspaceReviewPanels";
import type { GitChangeFile, GitChangesResult, GitFileDiffResult, GitStatusResult } from "../shared/protocol";

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

function installGitReviewStub(initial: GitChangeFile[], gitRoot = "/repo"): {
  listGitChanges: ReturnType<typeof vi.fn>;
  readGitFileDiff: ReturnType<typeof vi.fn>;
  setFiles: (files: GitChangeFile[]) => void;
} {
  let files = initial;
  const listGitChanges = vi.fn(async (): Promise<GitChangesResult> => ({ is_repo: true, root: gitRoot, files }));
  const readGitFileDiff = vi.fn(async (path: string): Promise<GitFileDiffResult> => {
    const file = files.find((candidate) => candidate.path === path);
    return {
      is_repo: true,
      path,
      status: file?.status ?? "modified",
      additions: file?.additions ?? 0,
      deletions: file?.deletions ?? 0,
      patch: "",
      original_text: `old ${path}\n`,
      modified_text: `new ${path} ${file?.additions ?? 0}\n`,
      truncated: false,
    };
  });
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: { listGitChanges, readGitFileDiff },
  });
  return { listGitChanges, readGitFileDiff, setFiles: (next) => (files = next) };
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
  expect(element).toBeTruthy();
  act(() => {
    (element as HTMLElement).click();
  });
}

function panel(): HTMLElement {
  return container!.querySelector<HTMLElement>("[data-wuu-component=\"workspace-review\"]")!;
}

function row(path: string): HTMLButtonElement | null {
  return container!.querySelector<HTMLButtonElement>(`.workspace-review-row[data-wuu-path="${path}"]`);
}

function action(name: string): HTMLButtonElement | null {
  return container!.querySelector<HTMLButtonElement>(`[data-wuu-action="${name}"]`);
}

function rowOrder(): string[] {
  return Array.from(container!.querySelectorAll<HTMLElement>(".workspace-review-row")).map(
    (element) => element.dataset.wuuPath ?? "",
  );
}

function shownDiffPath(): string | undefined {
  return container!.querySelector<HTMLElement>(".workspace-monaco-diff-editor")?.dataset.path;
}

function mockPanelWidth(width: number): () => void {
  const rect = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    const measured = this.classList.contains("workspace-review-panel") ? width : 0;
    return { width: measured, height: 0, x: 0, y: 0, top: 0, left: 0, right: measured, bottom: 0, toJSON: () => ({}) } as DOMRect;
  });
  return () => rect.mockRestore();
}

const status = (dirty: number): GitStatusResult => ({ is_repo: true, branch: "main", dirty_count: dirty });

// Path order differs from list order: root files lead, then folders.
const FILES: GitChangeFile[] = [
  { path: "app/one.ts", status: "modified", additions: 1, deletions: 1 },
  { path: "app/two.ts", status: "added", additions: 3, deletions: 0 },
  { path: "go.mod", status: "modified", additions: 1, deletions: 1 },
  { path: "lib/three.ts", status: "deleted", additions: 0, deletions: 2 },
];

describe("WorkspaceReviewPanel", () => {
  it("opens a narrow panel on the change list and reads one file at a time", async () => {
    const api = installGitReviewStub(FILES);
    mount(<WorkspaceReviewPanel gitStatus={status(4)} workspaceRoot="/repo" />);
    await flushReviewEffects();

    expect(panel().dataset.wuuLayout).toBe("stack");
    expect(panel().dataset.wuuState).toBe("navigation");
    expect(rowOrder()).toEqual(["go.mod", "app/one.ts", "app/two.ts", "lib/three.ts"]);
    expect(api.readGitFileDiff).not.toHaveBeenCalled();

    click(row("app/two.ts"));
    await flushReviewEffects();
    expect(panel().dataset.wuuState).toBe("detail");
    expect(container!.querySelector(".workspace-review-list")).toBeNull();
    expect(shownDiffPath()).toBe("app/two.ts");
    expect(document.activeElement).toBe(action("back"));

    click(action("next"));
    await flushReviewEffects();
    expect(shownDiffPath()).toBe("lib/three.ts");
    expect(action("next")?.disabled).toBe(true);
    expect(action("previous")?.disabled).toBe(false);

    click(action("back"));
    expect(panel().dataset.wuuState).toBe("navigation");
    expect(document.activeElement).toBe(row("lib/three.ts"));
    expect(row("lib/three.ts")?.getAttribute("aria-current")).toBe("true");
  });

  it("keeps the change list beside the diff when the panel has room", async () => {
    const restore = mockPanelWidth(900);
    try {
      installGitReviewStub(FILES);
      mount(<WorkspaceReviewPanel gitStatus={status(4)} workspaceRoot="/repo" />);
      await flushReviewEffects();

      expect(panel().dataset.wuuLayout).toBe("split");
      expect(container!.querySelector(".workspace-review-resizer")).toBeTruthy();
      expect(action("back")).toBeNull();
      // The first file in list order, not in Git's path order.
      expect(shownDiffPath()).toBe("go.mod");
      expect(row("go.mod")?.getAttribute("aria-current")).toBe("true");
    } finally {
      restore();
    }
  });

  it("follows new Git status in place while a file is open", async () => {
    const api = installGitReviewStub(FILES);
    const view = (gitStatus: GitStatusResult): JSX.Element => (
      <WorkspaceReviewPanel gitStatus={gitStatus} workspaceRoot="/repo-refresh" />
    );
    mount(view(status(4)));
    await flushReviewEffects();
    click(row("app/one.ts"));
    await flushReviewEffects();
    click(action("viewed"));
    expect(action("viewed")?.getAttribute("aria-pressed")).toBe("true");

    // An agent edits the open file and adds another.
    api.setFiles([
      { path: "app/one.ts", status: "modified", additions: 5, deletions: 1 },
      ...FILES.slice(1),
      { path: "app/zero.ts", status: "untracked", additions: 2, deletions: 0 },
    ]);
    act(() => root!.render(view(status(5))));
    await flushReviewEffects();

    expect(api.listGitChanges).toHaveBeenCalledTimes(2);
    expect(panel().dataset.wuuState).toBe("detail");
    expect(shownDiffPath()).toBe("app/one.ts");
    expect(api.readGitFileDiff).toHaveBeenLastCalledWith("app/one.ts", "/repo-refresh");
    // The mark was for the earlier change; the new one is unread.
    expect(action("viewed")?.getAttribute("aria-pressed")).toBe("false");
    click(action("next"));
    await flushReviewEffects();
    expect(shownDiffPath()).toBe("app/two.ts");

    // The open file is reverted: the review returns to the list.
    api.setFiles(FILES.filter((file) => file.path !== "app/two.ts"));
    act(() => root!.render(view(status(3))));
    await flushReviewEffects();
    expect(panel().dataset.wuuState).toBe("navigation");
    expect(row("app/two.ts")).toBeNull();
  });

  it("keeps viewed marks for an unchanged file across remounts", async () => {
    installGitReviewStub(FILES);
    const view = <WorkspaceReviewPanel gitStatus={status(4)} workspaceRoot="/repo-viewed" />;
    mount(view);
    await flushReviewEffects();
    click(row("go.mod"));
    await flushReviewEffects();
    click(action("viewed"));
    act(() => root!.unmount());

    root = createRoot(container!);
    act(() => root!.render(view));
    await flushReviewEffects();
    click(row("go.mod"));
    await flushReviewEffects();
    expect(action("viewed")?.getAttribute("aria-pressed")).toBe("true");
    click(action("back"));
    click(row("app/one.ts"));
    await flushReviewEffects();
    expect(action("viewed")?.getAttribute("aria-pressed")).toBe("false");
  });

  it("opens files relative to a workspace folder inside the repository", async () => {
    installGitReviewStub(
      [
        { path: "desktop/src/app.ts", status: "modified", additions: 1, deletions: 0 },
        { path: "desktop/src/gone.ts", status: "deleted", additions: 0, deletions: 4 },
        { path: "go.mod", status: "modified", additions: 1, deletions: 1 },
      ],
      "/repo",
    );
    const onOpenFile = vi.fn();
    mount(<WorkspaceReviewPanel gitStatus={status(3)} workspaceRoot="/repo/desktop" onOpenFile={onOpenFile} />);
    await flushReviewEffects();

    click(row("desktop/src/app.ts"));
    await flushReviewEffects();
    click(action("open"));
    expect(onOpenFile).toHaveBeenCalledWith("src/app.ts");

    click(action("next"));
    await flushReviewEffects();
    expect(shownDiffPath()).toBe("desktop/src/gone.ts");
    expect(action("open")).toBeNull();

    // Outside the workspace folder: nothing the editor can open.
    click(action("back"));
    click(row("go.mod"));
    await flushReviewEffects();
    expect(action("open")).toBeNull();
  });

  it("uses the full review width for a single changed file", async () => {
    const api = installGitReviewStub([
      { path: "desktop/src/renderer/styles/sidebar.css", status: "modified", additions: 4, deletions: 5 },
    ]);
    mount(<WorkspaceReviewPanel gitStatus={status(1)} workspaceRoot="/repo/worktree" />);
    await flushReviewEffects();

    expect(api.listGitChanges).toHaveBeenCalledWith("/repo/worktree");
    expect(panel().dataset.wuuLayout).toBe("single");
    expect(shownDiffPath()).toBe("desktop/src/renderer/styles/sidebar.css");
    expect(container!.querySelector(".workspace-review-list")).toBeNull();
    expect(action("back")).toBeNull();
    expect(action("next")).toBeNull();
  });
});
