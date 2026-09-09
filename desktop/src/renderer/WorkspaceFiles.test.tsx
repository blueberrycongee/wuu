import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  RuntimeContext,
  WorkspaceDirectoryListResult,
  WorkspaceFileReadResult,
} from "../shared/protocol";
import { WORKSPACE_FILE_DRAG_MIME } from "./ComposerMessages";
import { WorkspaceFilePreview, WorkspaceFileTree } from "./WorkspaceFiles";

vi.mock("./WorkspaceMonacoEditor", () => ({
  WorkspaceMonacoEditor: ({
    path,
    text,
    selection,
    readOnly,
    onChange,
    onSave,
  }: {
    path: string;
    text: string;
    selection?: { startLineNumber: number; startColumn: number };
    readOnly?: boolean;
    onChange?: (value: string) => void;
    onSave?: () => void;
  }) => (
    <div
      className="workspace-monaco-editor"
      data-path={path}
      data-readonly={readOnly ? "true" : "false"}
      data-selection={selection ? `${selection.startLineNumber}:${selection.startColumn}` : ""}
      data-text={text}
    >
      <pre>{text}</pre>
      <button type="button" className="mock-editor-edit" disabled={readOnly} onClick={() => onChange?.("edited code\n")}>
        mock edit
      </button>
      <button type="button" className="mock-editor-edit-second" disabled={readOnly} onClick={() => onChange?.("second edit\n")}>
        mock edit second
      </button>
      <button type="button" className="mock-editor-save" disabled={readOnly} onClick={() => onSave?.()}>
        mock save
      </button>
    </div>
  ),
}));

vi.mock("./WorkspacePdfPreview", () => ({
  WorkspacePdfPreview: ({ url, title }: { url: string; title: string }) => (
    <div className="mock-workspace-pdf-preview" data-url={url} data-title={title} />
  ),
}));

let container: HTMLDivElement;
let root: Root | null = null;
let listWorkspaceDirectory: ReturnType<typeof vi.fn>;
let readWorkspaceFile: ReturnType<typeof vi.fn>;
let writeWorkspaceFile: ReturnType<typeof vi.fn>;
let revealWorkspaceItem: ReturnType<typeof vi.fn>;
let writeClipboard: ReturnType<typeof vi.fn>;
let scrollIntoView: ReturnType<typeof vi.fn>;

const activeContext: RuntimeContext = {
  kind: "project",
  project_id: "project-1",
  cwd: "/repo",
};

function workspaceFile(overrides: Partial<WorkspaceFileReadResult> = {}): WorkspaceFileReadResult {
  return {
    root: "/repo",
    path: "src/index.ts",
    absolute_path: "/repo/src/index.ts",
    size_bytes: 12,
    mtime_ms: 1000,
    sha256: "a".repeat(64),
    binary: false,
    truncated: false,
    text: "button code",
    ...overrides,
  };
}

const directoryResults: Record<string, WorkspaceDirectoryListResult> = {
  "": { root: "/repo", path: "", entries: [
    { kind: "directory", name: "src", path: "src/" },
    { kind: "file", name: "README.md", path: "README.md" },
  ], truncated: false },
  src: { root: "/repo", path: "src", entries: [
    { kind: "directory", name: "components", path: "src/components/" },
    { kind: "file", name: "index.ts", path: "src/index.ts" },
  ], truncated: false },
  "src/components": { root: "/repo", path: "src/components", entries: [
    { kind: "file", name: "Button.tsx", path: "src/components/Button.tsx" },
  ], truncated: false },
};

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  listWorkspaceDirectory = vi.fn((path = "") => Promise.resolve(directoryResults[path]));
  readWorkspaceFile = vi.fn((path: string) =>
    Promise.resolve(workspaceFile({ path, absolute_path: `/repo/${path}` })),
  );
  writeWorkspaceFile = vi.fn();
  revealWorkspaceItem = vi.fn().mockResolvedValue(undefined);
  writeClipboard = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      listWorkspaceDirectory,
      readWorkspaceFile,
      writeWorkspaceFile,
      revealWorkspaceItem,
      showWorkspaceItemMenu: vi.fn().mockResolvedValue({ action: "none" }),
    },
  });
  Object.defineProperty(globalThis.navigator, "clipboard", {
    configurable: true,
    value: { writeText: writeClipboard },
  });
  scrollIntoView = vi.fn();
  Element.prototype.scrollIntoView = scrollIntoView;
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  // Reset the navigator.clipboard definition we installed so the next
  // test starts from a clean slate.
  Object.defineProperty(globalThis.navigator, "clipboard", {
    configurable: true,
    value: undefined,
  });
  vi.restoreAllMocks();
});

async function render(element: JSX.Element): Promise<void> {
  await act(async () => {
    root?.render(element);
    await Promise.resolve();
  });
}

async function settleDirectoryLoads(): Promise<void> {
  for (let index = 0; index < 4; index += 1) {
    await act(async () => {
      await Promise.resolve();
    });
  }
}

// Helpers for the file-tree context-menu tests below. Right-click events
// reach the row's onContextMenu handler, which sets React state; menu
// listeners attach via setTimeout(0) inside useEffect, so tests must flush
// a real macrotask before dispatching outside pointerdown / Escape events.
function treeShadowRoot(): ShadowRoot {
  const shadowRoot = container.querySelector("file-tree-container")?.shadowRoot;
  if (!shadowRoot) throw new Error("missing Pierre tree shadow root");
  return shadowRoot;
}

function rowButtonByTitle(title: string): HTMLButtonElement {
  const row = [...treeShadowRoot().querySelectorAll<HTMLButtonElement>("button[data-type='item']")]
    .find((button) => button.getAttribute("aria-label") === title || button.dataset.itemPath === title);
  if (!row) {
    throw new Error(`missing tree row with title ${title}`);
  }
  return row;
}

function contextMenu(): HTMLElement | null {
  return document.body.querySelector(".workspace-tree-context-menu");
}

async function dispatchContextMenu(
  element: HTMLElement,
  clientX: number,
  clientY: number,
): Promise<void> {
  await act(async () => {
    element.dispatchEvent(
      new MouseEvent("contextmenu", {
        bubbles: true,
        cancelable: true,
        clientX,
        clientY,
        button: 2,
      }),
    );
    await Promise.resolve();
  });
}

async function flushMacrotask(): Promise<void> {
  await new Promise<void>((resolve) => {
    setTimeout(resolve, 0);
  });
}

async function clickMenuItem(text: string): Promise<void> {
  const menu = contextMenu();
  if (!menu) {
    throw new Error("menu not visible");
  }
  const items = [
    ...menu.querySelectorAll<HTMLButtonElement>("button[role='menuitem']"),
  ];
  const item = items.find((b) => b.textContent?.trim() === text);
  if (!item) {
    throw new Error(`menu item ${text} not found`);
  }
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

describe("WorkspaceFileTree", () => {
  it("reserves room beside the search field for the file-tree drag handle", async () => {
    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    const unsafeStyle = treeShadowRoot().querySelector<HTMLStyleElement>(
      "style[data-file-tree-unsafe-css]",
    );
    expect(unsafeStyle?.textContent).toMatch(
      /\[data-file-tree-search-container\]\s*\{[^}]*box-sizing:\s*border-box;[^}]*width:\s*100%;[^}]*margin-inline:\s*0;[^}]*padding-inline:\s*8px;/s,
    );
    expect(unsafeStyle?.textContent).toMatch(
      /\[data-file-tree-search-input\]\s*\{[^}]*min-width:\s*0;[^}]*margin-inline-end:\s*40px;[^}]*border:\s*var\(--wuu-workspace-file-tree-search-border,\s*1px solid var\(--hairline-strong\)\);[^}]*border-radius:\s*var\(--wuu-workspace-file-tree-search-radius,\s*var\(--radius-sm\)\);/s,
    );
    expect(unsafeStyle?.textContent).toMatch(
      /\[data-file-tree-search-input\]:focus-visible,[\s\S]*\[data-file-tree-search-input\]\[data-file-tree-search-input-fake-focus="true"\]\s*\{[^}]*outline:\s*none;/,
    );
    const search = treeShadowRoot().querySelector<HTMLInputElement>(
      "[data-file-tree-search-input]",
    );
    expect(search?.style.marginInlineEnd).toBe("40px");
    expect(search?.style.minWidth).toBe("0");
    expect(search?.style.borderColor).toBe("");
    expect(search?.style.outline).toBe("none");
  });

  it("expands and scrolls to the selected workspace file path", async () => {
    await render(
      <WorkspaceFileTree
        activeContext={activeContext}
        open
        selectedFilePath="/repo/src/components/Button.tsx"
        onOpenFile={() => {}}
      />,
    );

    await settleDirectoryLoads();

    expect(listWorkspaceDirectory).toHaveBeenCalledWith("", "/repo");
    expect(listWorkspaceDirectory).toHaveBeenCalledWith("src", "/repo");
    expect(listWorkspaceDirectory).toHaveBeenCalledWith("src/components", "/repo");
    const selected = treeShadowRoot().querySelector("[aria-selected='true']");
    expect(selected?.getAttribute("data-item-path")).toBe("src/components/Button.tsx");
  });

  it("marks tree rows as drag sources carrying the workspace-relative path", async () => {
    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    const row = rowButtonByTitle("README.md");
    expect(row.draggable).toBe(true);

    const setData = vi.fn();
    const dataTransfer = { effectAllowed: "uninitialized", setData };
    const event = new Event("dragstart", { bubbles: true, composed: true, cancelable: true });
    Object.defineProperty(event, "dataTransfer", { configurable: true, value: dataTransfer });
    await act(async () => {
      row.dispatchEvent(event);
      await Promise.resolve();
    });

    expect(dataTransfer.effectAllowed).toBe("copy");
    expect(setData).toHaveBeenCalledWith(WORKSPACE_FILE_DRAG_MIME, "README.md");
    expect(setData).toHaveBeenCalledWith("text/plain", "README.md");
  });

  it("keeps the trailing slash when a directory row is dragged", async () => {
    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    const row = rowButtonByTitle("src/");
    const setData = vi.fn();
    const dataTransfer = { effectAllowed: "uninitialized", setData };
    const event = new Event("dragstart", { bubbles: true, composed: true, cancelable: true });
    Object.defineProperty(event, "dataTransfer", { configurable: true, value: dataTransfer });
    await act(async () => {
      row.dispatchEvent(event);
      await Promise.resolve();
    });

    expect(setData).toHaveBeenCalledWith(WORKSPACE_FILE_DRAG_MIME, "src/");
  });

  it("does not reopen the previous file when an external tab changes the tree selection", async () => {
    const onOpenFile = vi.fn();
    await render(
      <WorkspaceFileTree
        activeContext={activeContext}
        open
        selectedFilePath="/repo/README.md"
        onOpenFile={onOpenFile}
      />,
    );
    await settleDirectoryLoads();
    onOpenFile.mockClear();

    await render(
      <WorkspaceFileTree
        activeContext={activeContext}
        open
        selectedFilePath="/repo/src/index.ts"
        onOpenFile={onOpenFile}
      />,
    );
    await settleDirectoryLoads();

    expect(onOpenFile).not.toHaveBeenCalled();
    expect(treeShadowRoot().querySelector("[aria-selected='true']")?.getAttribute("data-item-path"))
      .toBe("src/index.ts");
  });

  it("opens a file when the user changes the tree selection", async () => {
    const onOpenFile = vi.fn();
    await render(
      <WorkspaceFileTree
        activeContext={activeContext}
        open
        onOpenFile={onOpenFile}
      />,
    );
    await settleDirectoryLoads();

    await act(async () => {
      rowButtonByTitle("README.md").click();
      await Promise.resolve();
    });

    expect(onOpenFile).toHaveBeenCalledTimes(1);
    expect(onOpenFile).toHaveBeenCalledWith("README.md");
  });

  it("keeps parent directories expanded while nested directories load", async () => {
    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    await act(async () => {
      rowButtonByTitle("src/").click();
      await Promise.resolve();
    });
    await settleDirectoryLoads();

    expect(listWorkspaceDirectory).toHaveBeenCalledWith("src", "/repo");
    expect(rowButtonByTitle("src/").getAttribute("aria-expanded")).toBe("true");

    await act(async () => {
      rowButtonByTitle("src/components/").click();
      await Promise.resolve();
    });
    await settleDirectoryLoads();

    expect(listWorkspaceDirectory).toHaveBeenCalledWith("src/components", "/repo");
    expect(rowButtonByTitle("src/").getAttribute("aria-expanded")).toBe("true");
    expect(rowButtonByTitle("src/components/").getAttribute("aria-expanded")).toBe("true");
    expect(rowButtonByTitle("src/components/Button.tsx")).toBeTruthy();
  });

  it("forwards a worktree root that differs from /repo through to the preload API", async () => {
    const worktreeContext: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/worktrees/fork-1",
    };
    listWorkspaceDirectory.mockResolvedValueOnce({
      root: "/worktrees/fork-1",
      path: "",
      entries: [{ kind: "file", name: "README.md", path: "README.md" }],
      truncated: false,
    });

    await render(
      <WorkspaceFileTree
        activeContext={worktreeContext}
        open
        onOpenFile={() => {}}
      />,
    );

    await settleDirectoryLoads();

    expect(listWorkspaceDirectory).toHaveBeenCalledWith("", "/worktrees/fork-1");
    expect(listWorkspaceDirectory).not.toHaveBeenCalledWith("", "/repo");
  });

  it("reads an absolute selected file path relative to the workspace root", async () => {
    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/src/components/Button.tsx"
        onOpenRightPanel={() => {}}
      />,
    );

    await settleDirectoryLoads();

    expect(readWorkspaceFile).toHaveBeenCalledWith("src/components/Button.tsx", "/repo");
    expect(container.textContent).toContain("button code");
  });

  it("exports the complete selected file and reports native save failures", async () => {
    const exporter = vi.fn().mockRejectedValue(new Error("Share destination unavailable"));
    window.wuu.exportWorkspaceFile = exporter;
    await render(<WorkspaceFilePreview activeContext={activeContext} selectedFilePath="src/components/Button.tsx" onOpenRightPanel={() => {}} />);
    await settleDirectoryLoads();
    const button = container.querySelector<HTMLButtonElement>(".workspace-file-export-actions button");
    expect(button?.disabled).toBe(false);
    await act(async () => { button?.click(); });
    expect(exporter).toHaveBeenCalledWith("src/components/Button.tsx", activeContext.cwd);
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("Share destination unavailable");
    expect(button?.disabled).toBe(false);
  });

  it("refreshes an open file without clearing the current preview", async () => {
    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/src/components/Button.tsx"
        refreshKey="running"
        onOpenRightPanel={() => {}}
      />,
    );
    await settleDirectoryLoads();

    let finishRefresh: ((result: WorkspaceFileReadResult) => void) | undefined;
    readWorkspaceFile.mockImplementationOnce(
      () => new Promise<WorkspaceFileReadResult>((resolve) => {
        finishRefresh = resolve;
      }),
    );
    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/src/components/Button.tsx"
        refreshKey="completed"
        onOpenRightPanel={() => {}}
      />,
    );

    expect(container.textContent).toContain("button code");

    await act(async () => {
      finishRefresh?.(workspaceFile({
        path: "src/components/Button.tsx",
        absolute_path: "/repo/src/components/Button.tsx",
        text: "updated button code",
      }));
      await Promise.resolve();
    });
    expect(container.textContent).toContain("updated button code");
  });

  it("opens selected non-Markdown text files in the center Monaco editor surface", async () => {
    readWorkspaceFile.mockResolvedValueOnce({
      root: "/repo",
      path: "AGENTS.txt",
      absolute_path: "/repo/AGENTS.txt",
      size_bytes: 35,
      mtime_ms: 1000,
      sha256: "b".repeat(64),
      binary: false,
      truncated: false,
      text: "## Execution Autonomy\n\n- Keep going.\n",
    });

    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/AGENTS.txt"
        onOpenRightPanel={() => {}}
      />,
    );

    await settleDirectoryLoads();

    expect(readWorkspaceFile).toHaveBeenCalledWith("AGENTS.txt", "/repo");
    expect(container.querySelector(".workspace-monaco-editor")).not.toBeNull();
    expect(container.querySelector(".workspace-file-preview-header")).toBeNull();
    expect(container.textContent).toContain("Execution Autonomy");
  });

  it("renders code files in Monaco without treating source as HTML", async () => {
    readWorkspaceFile.mockResolvedValueOnce({
      root: "/repo",
      path: "src/index.ts",
      absolute_path: "/repo/src/index.ts",
      size_bytes: 44,
      mtime_ms: 1000,
      sha256: "c".repeat(64),
      binary: false,
      truncated: false,
      text: 'const answer = 42;\nconsole.log("<tag>", answer);\n',
    });

    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/src/index.ts"
        onOpenRightPanel={() => {}}
      />,
    );

    await settleDirectoryLoads();

    const content = container.querySelector<HTMLElement>(".workspace-monaco-editor");
    expect(content?.textContent).toContain('console.log("<tag>", answer);');
    expect(content?.innerHTML).not.toContain("<tag>");
  });

  it("keeps text files readonly without exposing edit or save controls", async () => {
    const onDirtyChange = vi.fn();
    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/src/index.ts"
        onOpenRightPanel={() => {}}
        onDirtyChange={onDirtyChange}
      />,
    );

    await settleDirectoryLoads();
    expect(container.querySelector<HTMLElement>(".workspace-monaco-editor")?.dataset.readonly).toBe("true");
    expect(container.querySelector(".workspace-file-editor-toolbar")).toBeNull();
    expect(container.querySelector(".workspace-file-save-button")).toBeNull();
    expect(writeWorkspaceFile).not.toHaveBeenCalled();
    expect(onDirtyChange).toHaveBeenLastCalledWith({
      root: "/repo",
      path: "src/index.ts",
      dirty: false,
    });
  });

  it("keeps truncated text files readonly", async () => {
    readWorkspaceFile.mockResolvedValueOnce(workspaceFile({
      path: "large.log",
      absolute_path: "/repo/large.log",
      truncated: true,
      text: "partial log\n",
    }));

    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/large.log"
        onOpenRightPanel={() => {}}
      />,
    );

    await settleDirectoryLoads();

    expect(container.querySelector<HTMLElement>(".workspace-monaco-editor")?.dataset.readonly).toBe("true");
    expect(container.querySelector(".workspace-file-editor-toolbar")).toBeNull();
  });

  it("keeps binary files out of the editor surface", async () => {
    readWorkspaceFile.mockResolvedValueOnce(workspaceFile({
      path: "archive.dat",
      absolute_path: "/repo/archive.dat",
      binary: true,
      text: undefined,
    }));

    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/archive.dat"
        onOpenRightPanel={() => {}}
      />,
    );

    await settleDirectoryLoads();

    expect(container.textContent).toContain("二进制文件");
    expect(container.querySelector(".workspace-monaco-editor")).toBeNull();
  });

  it("renders image files with a renderable file URL", async () => {
    readWorkspaceFile.mockResolvedValueOnce(workspaceFile({
      path: "assets/mascot/wuu-mascot-concept-01.png",
      absolute_path: "/repo/assets/mascot/wuu-mascot-concept-01.png",
      binary: true,
      text: undefined,
      renderable_url: "wuu-file://local/encoded-image",
    }));

    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/assets/mascot/wuu-mascot-concept-01.png"
        onOpenRightPanel={() => {}}
      />,
    );

    await settleDirectoryLoads();

    const image = container.querySelector<HTMLImageElement>(".workspace-file-image-preview img");
    expect(image?.getAttribute("src")).toBe("wuu-file://local/encoded-image");
    expect(image?.getAttribute("alt")).toBe("assets/mascot/wuu-mascot-concept-01.png");
    expect(container.textContent).not.toContain("二进制文件");
    expect(container.querySelector(".workspace-monaco-editor")).toBeNull();
  });

  it("uses the themed workspace PDF viewer instead of Chromium's dark iframe", async () => {
    readWorkspaceFile.mockResolvedValueOnce(workspaceFile({
      path: "docs/spec.pdf",
      absolute_path: "/repo/docs/spec.pdf",
      binary: true,
      text: undefined,
      renderable_url: "wuu-file://local/encoded-pdf",
      renderable_kind: "pdf",
    }));

    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/docs/spec.pdf"
        onOpenRightPanel={() => {}}
      />,
    );

    await settleDirectoryLoads();

    const preview = container.querySelector<HTMLElement>(".mock-workspace-pdf-preview");
    expect(preview?.dataset.url).toBe("wuu-file://local/encoded-pdf");
    expect(preview?.dataset.title).toBe("docs/spec.pdf");
    expect(container.querySelector("iframe")).toBeNull();
  });

  it("opens Markdown files in reading mode by default", async () => {
    readWorkspaceFile.mockResolvedValueOnce(workspaceFile({
      path: "README.md",
      absolute_path: "/repo/README.md",
      text: "# Project Notes\n\n- review changes\n",
    }));

    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/README.md"
        onOpenRightPanel={() => {}}
      />,
    );

    await settleDirectoryLoads();

    expect(container.querySelector(".workspace-markdown-reading")).not.toBeNull();
    expect(container.querySelector(".workspace-markdown-reading .rich-heading")?.textContent).toContain("Project Notes");
    expect(container.querySelector(".workspace-monaco-editor")).toBeNull();
    expect(container.querySelector(".workspace-file-editor-toolbar")).toBeNull();
    expect(writeWorkspaceFile).not.toHaveBeenCalled();
  });

  it("opens a line-targeted Markdown file in Monaco at the requested position", async () => {
    readWorkspaceFile.mockResolvedValueOnce(workspaceFile({
      path: "README.md",
      absolute_path: "/repo/README.md",
      text: "# Project Notes\n\n- review changes\n",
    }));

    await render(
      <WorkspaceFilePreview
        activeContext={activeContext}
        selectedFilePath="/repo/README.md"
        selection={{ startLineNumber: 3, startColumn: 1 }}
        onOpenRightPanel={() => {}}
      />,
    );

    await settleDirectoryLoads();

    expect(container.querySelector(".workspace-markdown-reading")).toBeNull();
    expect(
      container.querySelector<HTMLElement>(".workspace-monaco-editor")?.dataset.selection,
    ).toBe("3:1");
  });

  // Right-click context menu on file-tree rows. Lives inside WorkspaceFileTree
  // via WorkspaceTreeContextMenu; uses navigator.clipboard.writeText for the
  // three copy actions and the new window.wuu.revealWorkspaceItem IPC for
  // "show in folder". The menu is also exercised by right-clicking either a
  // file or a directory row — both branches share the same component.
  it("right-clicking a row opens the menu with four menu items", async () => {
    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    expect(contextMenu()).toBeNull();

    const row = rowButtonByTitle("README.md");
    await dispatchContextMenu(row, 120, 240);
    await flushMacrotask();

    const menu = contextMenu();
    expect(menu).toBeTruthy();
    expect(menu?.parentElement).toBe(document.body);
    expect(menu?.textContent).toContain("复制路径");
    expect(menu?.textContent).toContain("复制相对路径");
    expect(menu?.textContent).toContain("复制文件名");
    expect(menu?.textContent).toContain("在文件管理器中显示");
  });

  it("uses the macOS native menu", async () => {
    const showWorkspaceItemMenu = vi.fn().mockResolvedValue({ action: "none" });
    Object.assign(window.wuu, { platform: "darwin", showWorkspaceItemMenu });

    await render(
      <WorkspaceFileTree
        activeContext={activeContext}
        open
        onOpenFile={() => {}}
      />,
    );
    await settleDirectoryLoads();

    await dispatchContextMenu(rowButtonByTitle("README.md"), 120, 240);
    await flushMacrotask();

    expect(showWorkspaceItemMenu).toHaveBeenCalledWith("/repo/README.md");
    expect(contextMenu()).toBeNull();
  });

  it("复制路径 writes the absolute path to clipboard and closes the menu", async () => {
    // Override the root mock so entry.path is absolute — lets the assertions
    // distinguish "the raw entry path" from "the workspace-root-stripped
    // variant" in the next two tests.
    listWorkspaceDirectory.mockResolvedValue({ root: "/repo", path: "", entries: [{ kind: "file", name: "page.tsx", path: "page.tsx" }], truncated: false });

    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    const row = rowButtonByTitle("page.tsx");
    await dispatchContextMenu(row, 120, 240);
    await flushMacrotask();

    await clickMenuItem("复制路径");

    expect(writeClipboard).toHaveBeenCalledWith("/repo/page.tsx");
    expect(contextMenu()).toBeNull();
  });

  it("复制相对路径 strips the workspace-root prefix and closes the menu", async () => {
    listWorkspaceDirectory.mockResolvedValue({ root: "/repo", path: "", entries: [{ kind: "file", name: "page.tsx", path: "page.tsx" }], truncated: false });

    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    const row = rowButtonByTitle("page.tsx");
    await dispatchContextMenu(row, 120, 240);
    await flushMacrotask();

    await clickMenuItem("复制相对路径");

    expect(writeClipboard).toHaveBeenCalledWith("page.tsx");
    expect(contextMenu()).toBeNull();
  });

  it("复制文件名 writes the entry name and closes the menu", async () => {
    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    const row = rowButtonByTitle("README.md");
    await dispatchContextMenu(row, 120, 240);
    await flushMacrotask();

    await clickMenuItem("复制文件名");

    expect(writeClipboard).toHaveBeenCalledWith("README.md");
    expect(contextMenu()).toBeNull();
  });

  it("在文件管理器中显示 invokes the reveal IPC with the entry path and closes the menu", async () => {
    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    const row = rowButtonByTitle("README.md");
    await dispatchContextMenu(row, 120, 240);
    await flushMacrotask();

    await clickMenuItem("在文件管理器中显示");

    expect(revealWorkspaceItem).toHaveBeenCalledWith("/repo/README.md");
    expect(contextMenu()).toBeNull();
  });

  it("Escape dismisses the menu", async () => {
    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    const row = rowButtonByTitle("README.md");
    await dispatchContextMenu(row, 120, 240);
    await flushMacrotask();

    expect(contextMenu()).toBeTruthy();

    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
      await Promise.resolve();
    });

    expect(contextMenu()).toBeNull();
  });

  it("an outside pointerdown dismisses the menu", async () => {
    await render(
      <WorkspaceFileTree activeContext={activeContext} open onOpenFile={() => {}} />,
    );
    await settleDirectoryLoads();

    const row = rowButtonByTitle("README.md");
    await dispatchContextMenu(row, 120, 240);
    await flushMacrotask();

    expect(contextMenu()).toBeTruthy();

    await act(async () => {
      // PointerEvent isn't exposed in every JSDOM version; MouseEvent with the
      // same event.type reaches the same `pointerdown` listener since addEventListener
      // matches by event type, not event class.
      document.dispatchEvent(
        new MouseEvent("pointerdown", {
          bubbles: true,
          cancelable: true,
          button: 0,
        }),
      );
      await Promise.resolve();
    });

    expect(contextMenu()).toBeNull();
  });
});
