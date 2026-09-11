import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  clampWorkspaceFileTreeWidth,
  WORKSPACE_FILE_TREE_DEFAULT_WIDTH,
  WORKSPACE_FILE_TREE_MAX_WIDTH,
  WORKSPACE_FILE_TREE_MIN_WIDTH,
  WorkspaceRightPanel,
} from "./WorkspacePanels";
import type { RuntimeContext } from "../shared/protocol";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import {
  workspaceDiffViewTab,
  workspaceFileViewTab,
  workspaceToolViewTab,
  type WorkspaceViewTab,
} from "./WorkspaceViewTabs";
import { hoverTooltipText, unhoverTooltip } from "./tooltipTestUtils";
import type { HeaderSnapshotV1, PresentationHost } from "../shared/workbench";
import { PluginHost } from "./plugins/PluginHost";
import { WorkbenchController } from "./plugins/Workbench";

// Renders the cwd it received so tests can assert which context prop
// (activeContext vs workspaceContext) actually reached the terminal panel,
// without pulling in the real xterm/node-pty-backed component.
vi.mock("./WorkspaceTerminalPanel", () => ({
  WorkspaceTerminalPanel: ({ activeContext }: { activeContext?: RuntimeContext }) => (
    <div data-testid="terminal-panel" data-cwd={activeContext?.cwd ?? ""} />
  ),
}));

vi.mock("./WorkspaceMonacoEditor", () => ({
  WorkspaceMonacoEditor: ({
    initialViewState,
    onViewStateChange,
    path,
    resourceID,
    selection,
    text,
    onChange,
  }: {
    initialViewState?: { scrollTop: number } | null;
    onViewStateChange?: (state: { scrollTop: number }) => void;
    path: string;
    resourceID: string;
    selection?: { startLineNumber: number; startColumn: number };
    text: string;
    onChange?: (value: string) => void;
  }) => (
    <div
      className="workspace-monaco-editor"
      data-path={path}
      data-resource-id={resourceID}
      data-selection={selection ? `${selection.startLineNumber}:${selection.startColumn}` : ""}
      data-text={text}
      data-view-scroll={initialViewState?.scrollTop ?? 0}
    >
      <button type="button" className="mock-editor-edit" onClick={() => onChange?.(`edited ${path}`)}>
        edit
      </button>
      <button
        type="button"
        className="mock-editor-scroll"
        onClick={() => onViewStateChange?.({ scrollTop: 42 })}
      >
        scroll
      </button>
    </div>
  ),
}));

let container: HTMLDivElement | null = null;
let root: Root | null = null;

beforeEach(() => {
  window.localStorage.clear();
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      listWorkspaceDirectory: vi.fn().mockResolvedValue({
        root: "/repo/project",
        path: "",
        entries: [],
        truncated: false,
      }),
      readWorkspaceFile: vi.fn((path: string, rootPath?: string) =>
        Promise.resolve({
          root: rootPath ?? "/repo/project",
          path,
          absolute_path: `${rootPath ?? "/repo/project"}/${path}`,
          size_bytes: 12,
          mtime_ms: 1000,
          sha256: "a".repeat(64),
          binary: false,
          truncated: false,
          text: `source ${path}`,
        }),
      ),
      writeWorkspaceFile: vi.fn(),
      revealWorkspaceItem: vi.fn().mockResolvedValue(undefined),
    },
  });
});

function mount(element: JSX.Element): void {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(element);
  });
}

function unmount(): void {
  if (root) {
    act(() => {
      root!.unmount();
    });
    root = null;
  }
  container?.remove();
  container = null;
}

function fileResource(tabID: string): HTMLElement | undefined {
  return Array.from(container?.querySelectorAll<HTMLElement>(".workspace-file-resource") ?? []).find(
    (resource) => resource.dataset.workspaceTabId === tabID,
  );
}

afterEach(() => {
  unhoverTooltip();
  unmount();
  document.documentElement.classList.remove("resizing-workspace-file-split");
  vi.unstubAllGlobals();
});

function makeSelection(path: string): TurnFileDiffSelection {
  return {
    path,
    additions: 1,
    deletions: 1,
    newFile: false,
    diff: {
      path,
      hunks: [
        {
          oldStart: 1,
          newStart: 1,
          lines: [
            { op: "delete", content: "old value" },
            { op: "insert", content: "new value" },
          ],
        },
      ],
    },
  };
}

function baseProps(): Parameters<typeof WorkspaceRightPanel>[0] {
  return {
    open: true,
    present: true,
    tabs: [],
    activeTabID: undefined,
    onSelectTab: () => {},
    onOpenTool: () => {},
    onShowTools: () => {},
    onCloseTab: () => {},
    onReorderTabs: () => {},
    onOpenFile: () => {},
    onClose: () => {},
    globalized: false,
    onToggleGlobalize: () => {},
  };
}

describe("WorkspaceRightPanel", () => {
  it("replaces its owned tabbar boundary with a live workspace header presenter", async () => {
    const pluginHost = new PluginHost({ react: React });
    const workbenchController = new WorkbenchController(pluginHost);
    const snapshots: HeaderSnapshotV1[] = [];
    let presentationHost: PresentationHost | undefined;
    await pluginHost.activateGeneration({ pluginId: "workspace-header", generation: "one", register(api) {
      api.registerPresenter({ id: "workspace", target: "header.workspace", render: (props) => {
        snapshots.push(props.snapshot as HeaderSnapshotV1);
        presentationHost = props.host;
        return <div data-workspace-header>{(props.snapshot as HeaderSnapshotV1).title}</div>;
      } });
    } });
    const context: RuntimeContext = { kind: "project", project_id: "project-1", cwd: "/repo/project" };
    const tab = workspaceFileViewTab({ context, path: "src/main.ts" });
    const onSelectTab = vi.fn();
    const onCloseTab = vi.fn();

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[tab]}
        activeTabID={tab.id}
        activeFileTabID={tab.id}
        workspaceContext={context}
        onSelectTab={onSelectTab}
        onCloseTab={onCloseTab}
        pluginHost={pluginHost}
        workbenchController={workbenchController}
      />,
    );

    const tabbar = container?.querySelector(".workspace-panel-tabbar");
    expect(tabbar?.firstElementChild?.hasAttribute("data-workspace-header")).toBe(true);
    expect(tabbar?.querySelector(".workspace-panel-tabs")).toBeNull();
    expect(snapshots.at(-1)).toMatchObject({
      contractVersion: 1,
      scope: "workspace",
      title: "main.ts",
      subtitle: "src/main.ts",
      activeTabId: tab.id,
      tabs: [{ id: tab.id, title: "main.ts", subtitle: "src/main.ts", kind: "file" }],
    });
    expect("context" in (snapshots.at(-1)?.tabs?.[0] ?? {})).toBe(false);

    await act(async () => presentationHost?.invoke("header.select-tab", { tabId: tab.id }));
    await act(async () => presentationHost?.invoke("header.close-tab", { tabId: tab.id }));
    expect(onSelectTab).toHaveBeenCalledWith(tab.id);
    expect(onCloseTab).toHaveBeenCalledWith(tab.id);
  });

  it("prewarms the hidden lightweight body during idle time", () => {
    let idleCallback: IdleRequestCallback | undefined;
    const cancelIdleCallback = vi.fn();
    vi.stubGlobal(
      "requestIdleCallback",
      vi.fn((callback: IdleRequestCallback) => {
        idleCallback = callback;
        return 17;
      }),
    );
    vi.stubGlobal("cancelIdleCallback", cancelIdleCallback);

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        open={false}
        present={false}
        prewarm
      />,
    );
    expect(container?.querySelector(".workspace-panel-body")).toBeNull();

    act(() => {
      idleCallback?.({
        didTimeout: false,
        timeRemaining: () => 20,
      });
    });

    const panel = container?.querySelector<HTMLElement>(".workspace-right-panel");
    expect(panel?.hasAttribute("inert")).toBe(true);
    expect(container?.querySelector(".workspace-panel-body")).not.toBeNull();

    unmount();
    expect(cancelIdleCallback).toHaveBeenCalledWith(17);
  });

  it("cancels hidden prewarming when the user opens the panel first", () => {
    let idleCallback: IdleRequestCallback | undefined;
    const cancelIdleCallback = vi.fn();
    vi.stubGlobal(
      "requestIdleCallback",
      vi.fn((callback: IdleRequestCallback) => {
        idleCallback = callback;
        return 23;
      }),
    );
    vi.stubGlobal("cancelIdleCallback", cancelIdleCallback);

    const props = {
      ...baseProps(),
      open: false,
      present: false,
      prewarm: true,
    } satisfies Parameters<typeof WorkspaceRightPanel>[0];
    mount(<WorkspaceRightPanel {...props} />);

    act(() => {
      root?.render(<WorkspaceRightPanel {...props} open present />);
    });

    expect(cancelIdleCallback).toHaveBeenCalledWith(23);
    expect(container?.querySelector(".workspace-panel-body")).not.toBeNull();
    expect(idleCallback).toBeDefined();
  });

  it("does not duplicate the shell sidebar toggle in the panel tabbar", () => {
    const props = {
      ...baseProps(),
      globalized: true,
      canExitGlobalized: false,
    } as Parameters<typeof WorkspaceRightPanel>[0];
    mount(<WorkspaceRightPanel {...props} />);

    expect(container?.querySelector(".workspace-panel-sidebar")).toBeNull();
    expect(container?.querySelector(".workspace-panel-sidebar-hit-hole")).not.toBeNull();

    const restore = container?.querySelector<HTMLButtonElement>(
      '[aria-label="窗口过窄，无法停靠右侧栏"]',
    );
    expect(restore?.disabled).toBe(true);
  });

  it("presents the workspace expansion as full-panel focus mode", () => {
    const onToggleGlobalize = vi.fn();
    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        onToggleGlobalize={onToggleGlobalize}
      />,
    );

    const expand = container?.querySelector<HTMLButtonElement>('[aria-label="展开为全面板"]');
    expect(expand).not.toBeNull();
    expect(expand?.title).toBe("展开为全面板");
    act(() => expand?.click());
    expect(onToggleGlobalize).toHaveBeenCalledTimes(1);

    act(() => {
      root?.render(
        <WorkspaceRightPanel
          {...baseProps()}
          globalized
          onToggleGlobalize={onToggleGlobalize}
        />,
      );
    });
    expect(container?.querySelector('[aria-label="退出全面板"]')).not.toBeNull();
  });

  it("makes the retained workspace inert and releases its editor while closed", async () => {
    const fileTab = workspaceFileViewTab({
      context: {
        kind: "project",
        project_id: "project-1",
        cwd: "/repo/project",
      },
      path: "src/App.tsx",
    });
    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        open={false}
        present={false}
        tabs={[fileTab]}
        activeTabID={fileTab.id}
      />,
    );
    await act(async () => Promise.resolve());

    const panel = container?.querySelector<HTMLElement>(".workspace-right-panel");
    expect(panel?.hasAttribute("inert")).toBe(true);
    expect(panel?.querySelector(".workspace-monaco-editor")).toBeNull();
  });

  it("renders file content on the left and the persistent file tree on the right", async () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/repo/project",
    };
    const fileTab = workspaceFileViewTab({
      context,
      path: "src/App.tsx",
    });

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[fileTab]}
        activeTabID={fileTab.id}
        activeFileTabID={fileTab.id}
        workspaceContext={context}
      />,
    );
    await act(async () => Promise.resolve());

    const panel = container?.querySelector<HTMLElement>(".workspace-right-panel.detail.files");
    expect(panel).toBeTruthy();
    expect(panel?.querySelector(".workspace-tool-tab.active")?.textContent).toContain("App.tsx");
    expect(await hoverTooltipText(panel?.querySelector(".workspace-tool-tab-main") ?? null)).toBe("src/App.tsx");
    expect(panel?.querySelector(".workspace-files-content-header")).toBeNull();
    expect(panel?.querySelector(".workspace-file-resource.active .workspace-file-preview")).toBeTruthy();
    const content = panel?.querySelector(".workspace-files-content");
    const tree = panel?.querySelector(".workspace-files-tree");
    expect(content).not.toBeNull();
    expect(tree).not.toBeNull();
    expect(content!.compareDocumentPosition(tree!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("resolves links in Markdown previews relative to the document", async () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/repo/project",
    };
    const fileTab = workspaceFileViewTab({ context, path: "docs/guide.md" });
    const onOpenFile = vi.fn();
    vi.mocked(window.wuu.readWorkspaceFile).mockResolvedValueOnce({
      root: context.cwd,
      path: "docs/guide.md",
      absolute_path: "/repo/project/docs/guide.md",
      size_bytes: 40,
      mtime_ms: 1000,
      sha256: "b".repeat(64),
      binary: false,
      truncated: false,
      text: "[Open app](../src/App.tsx#L12)",
    });

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[fileTab]}
        activeTabID={fileTab.id}
        activeFileTabID={fileTab.id}
        workspaceContext={context}
        onOpenFile={onOpenFile}
      />,
    );
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    act(() => {
      container?.querySelector<HTMLButtonElement>(".rich-file-link")?.click();
    });
    expect(onOpenFile).toHaveBeenCalledWith("src/App.tsx#L12");
  });

  it("resizes and persists the right-side file tree", async () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/repo/project",
    };
    const fileTab = workspaceFileViewTab({ context, path: "src/App.tsx" });
    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[fileTab]}
        activeTabID={fileTab.id}
        activeFileTabID={fileTab.id}
        workspaceContext={context}
      />,
    );
    await act(async () => Promise.resolve());

    const split = container!.querySelector<HTMLElement>(".workspace-files-split")!;
    const separator = split.querySelector<HTMLElement>(".workspace-files-resizer")!;
    Object.defineProperty(split, "getBoundingClientRect", {
      configurable: true,
      value: () => ({ width: 1000 }),
    });

    expect(separator.getAttribute("role")).toBe("separator");
    expect(separator.getAttribute("aria-valuemin")).toBe(String(WORKSPACE_FILE_TREE_MIN_WIDTH));
    expect(separator.getAttribute("aria-valuemax")).toBe(String(WORKSPACE_FILE_TREE_MAX_WIDTH));
    expect(split.style.getPropertyValue("--workspace-file-tree-width")).toBe(
      `${WORKSPACE_FILE_TREE_DEFAULT_WIDTH}px`,
    );

    act(() => {
      separator.dispatchEvent(
        new MouseEvent("pointerdown", { bubbles: true, button: 0, clientX: 680 }),
      );
    });
    expect(document.documentElement.classList.contains("resizing-workspace-file-split")).toBe(true);
    act(() => {
      window.dispatchEvent(new MouseEvent("pointermove", { clientX: 600 }));
      window.dispatchEvent(new MouseEvent("pointerup"));
    });
    expect(split.style.getPropertyValue("--workspace-file-tree-width")).toBe("400px");
    expect(window.localStorage.getItem("wuu.desktop.fileTreeWidth")).toBe("400");

    act(() => {
      separator.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "ArrowLeft" }));
    });
    expect(split.style.getPropertyValue("--workspace-file-tree-width")).toBe("424px");

    act(() => {
      separator.dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    });
    expect(split.style.getPropertyValue("--workspace-file-tree-width")).toBe(
      `${WORKSPACE_FILE_TREE_DEFAULT_WIDTH}px`,
    );
  });

  it("drags, auto-collapses, restores, and persists the file tree layout", async () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/repo/project",
    };
    const fileTab = workspaceFileViewTab({ context, path: "src/App.tsx" });
    const panel = (
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[fileTab]}
        activeTabID={fileTab.id}
        activeFileTabID={fileTab.id}
        workspaceContext={context}
        focusedComposer={<div data-testid="focused-composer-content" />}
      />
    );
    mount(panel);
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    let split = container!.querySelector<HTMLElement>(".workspace-files-split")!;
    const tree = split.querySelector<HTMLElement>(".workspace-files-tree")!;
    const separator = split.querySelector<HTMLElement>(".workspace-files-resizer")!;
    const dragHandle = tree.querySelector<HTMLElement>(".workspace-file-tree-drag-handle")!;
    const dragPreview = document.body.querySelector<HTMLElement>(".workspace-file-tree-drag-preview")!;

    expect(split.dataset.treeSide).toBe("right");
    expect(tree.hidden).toBe(false);
    expect(container!.querySelector(".workspace-panel-file-tree-side")).toBeNull();
    expect(container!.querySelector(".workspace-panel-file-tree-visibility")).toBeNull();
    expect(
      split.querySelector(".workspace-files-content > [data-testid='workspace-document-composer']"),
    ).not.toBeNull();

    Object.defineProperty(split, "getBoundingClientRect", {
      configurable: true,
      value: () => ({ left: 0, width: 1000 }),
    });
    Object.defineProperty(dragPreview, "getBoundingClientRect", {
      configurable: true,
      value: () => ({ width: 168, height: 44 }),
    });
    const dataTransfer = {
      dropEffect: "none",
      effectAllowed: "none",
      setData: vi.fn(),
      setDragImage: vi.fn(),
    };
    function dragEvent(type: string, clientX: number): Event {
      const event = new Event(type, { bubbles: true, cancelable: true });
      Object.defineProperties(event, {
        clientX: { value: clientX },
        dataTransfer: { value: dataTransfer },
      });
      return event;
    }

    act(() => dragHandle.dispatchEvent(dragEvent("dragstart", 850)));
    expect(dataTransfer.setDragImage).toHaveBeenCalledWith(dragPreview, 84, 22);
    act(() => split.dispatchEvent(dragEvent("dragover", 100)));
    expect(split.dataset.treeDropSide).toBe("left");
    act(() => split.dispatchEvent(dragEvent("drop", 100)));
    expect(split.dataset.treeSide).toBe("left");
    expect(window.localStorage.getItem("wuu.desktop.fileTreeSide")).toBe("left");

    act(() => {
      separator.dispatchEvent(
        new MouseEvent("pointerdown", { bubbles: true, button: 0, clientX: 320 }),
      );
    });
    act(() => {
      window.dispatchEvent(new MouseEvent("pointermove", { clientX: 100 }));
      window.dispatchEvent(new MouseEvent("pointerup"));
    });
    await act(async () => Promise.resolve());
    expect(split.classList.contains("tree-hidden")).toBe(true);
    expect(tree.hidden).toBe(true);
    expect(separator.hidden).toBe(true);
    expect(window.localStorage.getItem("wuu.desktop.fileTreeVisible")).toBe("false");

    mount(panel);
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    split = container!.querySelector<HTMLElement>(".workspace-files-split")!;
    expect(split.dataset.treeSide).toBe("left");
    expect(split.classList.contains("tree-hidden")).toBe(true);
    const revealButton = container!.querySelector<HTMLButtonElement>(".workspace-file-tree-reveal.left")!;
    expect(revealButton).not.toBeNull();

    await act(async () => {
      revealButton.click();
      await Promise.resolve();
    });
    await act(async () => Promise.resolve());
    expect(split.classList.contains("tree-hidden")).toBe(false);
    expect(window.localStorage.getItem("wuu.desktop.fileTreeVisible")).toBe("true");
  });

  it("clamps the tree width so the file content keeps usable space", () => {
    expect(clampWorkspaceFileTreeWidth(100)).toBe(WORKSPACE_FILE_TREE_MIN_WIDTH);
    expect(clampWorkspaceFileTreeWidth(900)).toBe(WORKSPACE_FILE_TREE_MAX_WIDTH);
    expect(clampWorkspaceFileTreeWidth(480, 600)).toBe(360);
  });

  it("temporarily shrinks the file tree when the panel gets narrow", async () => {
    const originalResizeObserver = globalThis.ResizeObserver;
    let resizeCallback: ResizeObserverCallback | undefined;
    class MockResizeObserver {
      constructor(callback: ResizeObserverCallback) {
        resizeCallback = callback;
      }
      observe(): void {}
      disconnect(): void {}
    }
    globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver;

    try {
      window.localStorage.setItem("wuu.desktop.fileTreeWidth", "320");
      const context: RuntimeContext = {
        kind: "project",
        project_id: "project-1",
        cwd: "/repo/project",
      };
      const fileTab = workspaceFileViewTab({ context, path: "src/App.tsx" });
      mount(
        <WorkspaceRightPanel
          {...baseProps()}
          tabs={[fileTab]}
          activeTabID={fileTab.id}
          activeFileTabID={fileTab.id}
          workspaceContext={context}
        />,
      );
      await act(async () => Promise.resolve());

      const split = container!.querySelector<HTMLElement>(".workspace-files-split")!;
      let panelWidth = 420;
      Object.defineProperty(split, "getBoundingClientRect", {
        configurable: true,
        value: () => ({ width: panelWidth }),
      });

      act(() => resizeCallback?.([], {} as ResizeObserver));
      expect(split.style.getPropertyValue("--workspace-file-tree-width")).toBe("180px");
      expect(window.localStorage.getItem("wuu.desktop.fileTreeWidth")).toBe("320");

      panelWidth = 600;
      act(() => resizeCallback?.([], {} as ResizeObserver));
      expect(split.style.getPropertyValue("--workspace-file-tree-width")).toBe("320px");
    } finally {
      if (originalResizeObserver) {
        globalThis.ResizeObserver = originalResizeObserver;
      } else {
        Reflect.deleteProperty(globalThis, "ResizeObserver");
      }
    }
  });

  it("keeps inactive file state without retaining inactive Monaco editors", async () => {
    const context: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/repo/project",
    };
    const fileA = workspaceFileViewTab({ context, path: "src/a.ts" });
    const fileB = workspaceFileViewTab({ context, path: "src/b.ts" });
    const tabs: WorkspaceViewTab[] = [fileA, fileB];

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={tabs}
        activeTabID={fileA.id}
        activeFileTabID={fileA.id}
        workspaceContext={context}
      />,
    );
    await act(async () => Promise.resolve());
    act(() => {
      fileResource(fileA.id)?.querySelector<HTMLButtonElement>(".mock-editor-scroll")?.click();
    });

    act(() => {
      root?.render(
        <WorkspaceRightPanel
          {...baseProps()}
          tabs={tabs}
          activeTabID={fileB.id}
          activeFileTabID={fileB.id}
          workspaceContext={context}
        />,
      );
    });
    await act(async () => Promise.resolve());

    const fileAResource = fileResource(fileA.id);
    expect(fileAResource).toBeTruthy();
    expect(fileAResource?.hidden).toBe(true);
    expect(fileAResource?.querySelector(".workspace-monaco-editor")).toBeNull();
    expect(container?.querySelectorAll(".workspace-monaco-editor")).toHaveLength(1);
    expect(container?.querySelectorAll(".workspace-file-resource")).toHaveLength(2);

    act(() => {
      root?.render(
        <WorkspaceRightPanel
          {...baseProps()}
          tabs={tabs}
          activeTabID={fileA.id}
          activeFileTabID={fileA.id}
          workspaceContext={context}
        />,
      );
    });
    const restoredEditor = fileResource(fileA.id)?.querySelector(".workspace-monaco-editor");
    expect(restoredEditor?.getAttribute("data-text")).toBe("source src/a.ts");
    expect(restoredEditor?.getAttribute("data-view-scroll")).toBe("42");
  });

  it("gives same-path files in separate worktrees distinct Monaco resources", async () => {
    const primary = workspaceFileViewTab({
      context: {
        kind: "project",
        project_id: "project-1",
        cwd: "/repo/project",
      },
      path: "src/App.tsx",
    });
    const worktree = workspaceFileViewTab({
      context: {
        kind: "project",
        project_id: "project-1",
        cwd: "/repo/worktrees/feature/project",
      },
      path: "src/App.tsx",
    });
    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[primary, worktree]}
        activeTabID={primary.id}
        activeFileTabID={primary.id}
        workspaceContext={primary.context}
      />,
    );
    await act(async () => Promise.resolve());

    let resourceIDs = Array.from(
      container?.querySelectorAll<HTMLElement>(".workspace-monaco-editor") ?? [],
    ).map((editor) => editor.dataset.resourceId);
    expect(resourceIDs).toEqual([primary.id]);

    await act(async () => {
      root?.render(
        <WorkspaceRightPanel
          {...baseProps()}
          tabs={[primary, worktree]}
          activeTabID={worktree.id}
          activeFileTabID={worktree.id}
          workspaceContext={worktree.context}
        />,
      );
      await Promise.resolve();
    });
    resourceIDs = Array.from(
      container?.querySelectorAll<HTMLElement>(".workspace-monaco-editor") ?? [],
    ).map((editor) => editor.dataset.resourceId);
    expect(resourceIDs).toEqual([worktree.id]);
  });

  it("closes a readonly file without dirty-state confirmation", async () => {
    const onCloseTab = vi.fn();
    const onDirtyFileTabsChange = vi.fn();
    const confirmDiscard = vi.spyOn(window, "confirm");
    const fileTab = workspaceFileViewTab({
      context: {
        kind: "project",
        project_id: "project-1",
        cwd: "/repo/project",
      },
      path: "src/dirty.ts",
    });

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[fileTab]}
        activeTabID={fileTab.id}
        activeFileTabID={fileTab.id}
        workspaceContext={fileTab.context}
        onCloseTab={onCloseTab}
        onDirtyFileTabsChange={onDirtyFileTabsChange}
      />,
    );
    await act(async () => Promise.resolve());
    expect(onDirtyFileTabsChange).toHaveBeenLastCalledWith(false);
    expect(container?.querySelector(".workspace-tool-tab.active.dirty")).toBeNull();
    expect(container?.querySelector(".workspace-tab-dirty-indicator")).toBeNull();
    act(() => {
      container?.querySelector<HTMLButtonElement>(".workspace-tool-tab-close")?.click();
    });

    expect(confirmDiscard).not.toHaveBeenCalled();
    expect(onCloseTab).toHaveBeenCalledWith(fileTab.id);
  });

  it("renders a diff tab as a unified, closable right panel tab (folded into the tab strip)", async () => {
    const onCloseTab = vi.fn();
    const diffTab = workspaceDiffViewTab({
      threadID: "thread-1",
      path: "/tmp/a.txt",
      selection: makeSelection("/tmp/a.txt"),
    });

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[diffTab]}
        activeTabID={diffTab.id}
        onCloseTab={onCloseTab}
      />,
    );

    const panel = container?.querySelector<HTMLElement>(".workspace-right-panel.detail.diff");
    expect(panel).toBeTruthy();
    expect(panel?.querySelector(".workspace-panel-body")?.textContent).toContain("/tmp/a.txt");
    expect(panel?.querySelector(".workspace-panel-body")?.textContent).toContain("new value");

    // The diff tab shows up in the unified tab strip, not as a separate
    // pseudo-tab: it has a title (basename), a close button, and (once
    // there's more than one tab) is reorderable just like a tool tab.
    const tabButton = panel?.querySelector<HTMLButtonElement>(".workspace-tool-tab-main");
    expect(tabButton?.textContent).toContain("a.txt");
    expect(tabButton?.getAttribute("title")).toBeNull();
    expect(await hoverTooltipText(tabButton ?? null)).toBe("/tmp/a.txt");

    act(() => {
      panel?.querySelector<HTMLButtonElement>(".turn-file-diff-close")?.click();
    });
    expect(onCloseTab).toHaveBeenCalledTimes(1);
    expect(onCloseTab).toHaveBeenCalledWith(diffTab.id);

    onCloseTab.mockClear();
    act(() => {
      panel?.querySelector<HTMLButtonElement>(".workspace-tool-tab-close")?.click();
    });
    expect(onCloseTab).toHaveBeenCalledTimes(1);
    expect(onCloseTab).toHaveBeenCalledWith(diffTab.id);
  });

  it("supports several diff tabs open side by side alongside tool tabs", () => {
    const onSelectTab = vi.fn();
    const filesTab = workspaceToolViewTab("files");
    const diffA = workspaceDiffViewTab({
      threadID: "thread-1",
      path: "a.txt",
      selection: makeSelection("a.txt"),
    });
    const diffB = workspaceDiffViewTab({
      threadID: "thread-1",
      path: "src/b.txt",
      selection: makeSelection("src/b.txt"),
    });
    const tabs: WorkspaceViewTab[] = [filesTab, diffA, diffB];

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={tabs}
        activeTabID={diffB.id}
        onSelectTab={onSelectTab}
      />,
    );

    const tabButtons = container?.querySelectorAll(".workspace-panel-tabs .workspace-tool-tab");
    expect(tabButtons?.length).toBe(3);
    // Reorderable once more than one tab is present, regardless of kind.
    expect(container?.querySelectorAll(".workspace-tool-tab.can-reorder").length).toBe(3);

    const diffBTab = container?.querySelector(".workspace-tool-tab.active");
    expect(diffBTab?.textContent).toContain("b.txt");
    expect(container?.querySelector(".workspace-panel-tabs")?.getAttribute("aria-label")).toBe(
      "产物与工具",
    );
    const semanticTabs = container?.querySelectorAll<HTMLButtonElement>(".workspace-tool-tab-main");
    expect(semanticTabs?.[0]?.getAttribute("role")).toBe("tab");
    expect(semanticTabs?.[0]?.tabIndex).toBe(-1);
    expect(semanticTabs?.[2]?.getAttribute("aria-selected")).toBe("true");
    expect(semanticTabs?.[2]?.tabIndex).toBe(0);
    semanticTabs?.[2]?.focus();
    act(() => {
      semanticTabs?.[2]?.dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }),
      );
    });
    expect(onSelectTab).toHaveBeenCalledWith(diffA.id);
    expect(document.activeElement).toBe(semanticTabs?.[1]);

    const diffAButton = Array.from(
      container?.querySelectorAll<HTMLButtonElement>(".workspace-tool-tab-main") ?? [],
    ).find((button) => button.textContent?.includes("a.txt") && !button.textContent.includes("b.txt"));
    act(() => {
      diffAButton?.click();
    });
    expect(onSelectTab).toHaveBeenCalledWith(diffA.id);
  });

  it("restores keyboard focus to the next active workspace tab after close", async () => {
    const filesTab = workspaceToolViewTab("files");
    const terminalTab = workspaceToolViewTab("terminal");
    const renderPanel = (tabs: WorkspaceViewTab[], activeTabID: string): void => {
      root?.render(
        <WorkspaceRightPanel
          {...baseProps()}
          tabs={tabs}
          activeTabID={activeTabID}
          onCloseTab={(id) => {
            expect(id).toBe(terminalTab.id);
            renderPanel([filesTab], filesTab.id);
          }}
        />,
      );
    };
    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[filesTab, terminalTab]}
        activeTabID={terminalTab.id}
        onCloseTab={(id) => {
          expect(id).toBe(terminalTab.id);
          renderPanel([filesTab], filesTab.id);
        }}
      />,
    );

    await act(async () => {});
    const activeClose = container?.querySelector<HTMLButtonElement>(
      ".workspace-tool-tab.active .workspace-tool-tab-close",
    );
    activeClose?.focus();
    act(() => activeClose?.click());

    expect(document.activeElement).toBe(
      container?.querySelector(".workspace-tool-tab.active .workspace-tool-tab-main"),
    );
  });

  it("shows the tool picker when there is no active tab, and marks open tools active", () => {
    const onOpenTool = vi.fn();
    const filesTab = workspaceToolViewTab("files");

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[filesTab]}
        activeTabID={undefined}
        onOpenTool={onOpenTool}
      />,
    );

    const panel = container?.querySelector<HTMLElement>(".workspace-right-panel.tools");
    expect(panel).toBeTruthy();
    const picker = panel?.querySelector(".workspace-tool-menu");
    expect(picker).toBeTruthy();
    expect(picker?.querySelector(".workspace-tool-menu-item.active")?.textContent).toContain("文件");

    act(() => {
      picker?.querySelectorAll<HTMLButtonElement>(".workspace-tool-menu-item")[1]?.click();
    });
    expect(onOpenTool).toHaveBeenCalledWith("review");
  });
});

describe("WorkspaceRightPanel context routing (Bug 3: worktree-fork panel root)", () => {
  const projectContext: RuntimeContext = {
    kind: "project",
    project_id: "project-1",
    cwd: "/repo/project",
  };

  it("roots the file tree on workspaceContext, not activeContext", () => {
    const filesTab = workspaceToolViewTab("files");

    // activeContext is defined but workspaceContext is left undefined: if
    // the file tree fell back to activeContext it would render the normal
    // "loading" panel instead of the no-project empty state, since it would
    // never observe workspaceRoot as absent.
    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[filesTab]}
        activeTabID={filesTab.id}
        activeContext={projectContext}
        workspaceContext={undefined}
      />,
    );

    const panel = container?.querySelector<HTMLElement>(".workspace-right-panel");
    expect(panel?.textContent).toContain("没有项目");
  });

  it("roots the terminal on workspaceContext, not activeContext", async () => {
    const terminalTab = workspaceToolViewTab("terminal");
    const worktreeContext: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/Users/me/.wuu/worktrees/fork-1/project",
    };

    mount(
      <WorkspaceRightPanel
        {...baseProps()}
        tabs={[terminalTab]}
        activeTabID={terminalTab.id}
        activeContext={projectContext}
        workspaceContext={worktreeContext}
      />,
    );

    await act(async () => {});
    expect(container?.querySelector(".workspace-right-panel.detail.terminal")).not.toBeNull();
    const terminalPanel = container?.querySelector<HTMLElement>('[data-testid="terminal-panel"]');
    expect(terminalPanel?.getAttribute("data-cwd")).toBe(worktreeContext.cwd);
  });
});
