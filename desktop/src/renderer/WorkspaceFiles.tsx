import { isTouchWebShell } from "./ComposerFocus";
import { hostSupports } from "./HostCapabilities";
import { preparePresortedFileTreeInput } from "@pierre/trees";
import { FileTree, useFileTree } from "@pierre/trees/react";
import { AlertCircle, FileText, FolderOpen, FolderX } from "lucide-react";
import { type CSSProperties, Suspense, lazy, memo, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type {
  RuntimeContext,
  WorkspaceDirectoryListResult,
  WorkspaceFileReadResult,
  WorkspaceFileTreeEntry
} from "../shared/protocol";
import type { WorkspaceFileSelection } from "./LinkTargets";
import { WORKSPACE_FILE_DRAG_MIME } from "./ComposerMessages";
import { RichContent } from "./RichContent";
import type { WorkspaceMonacoViewState } from "./WorkspaceMonacoEditor";
import { desktopApiErrorMessage } from "./WorkspaceReviewHelpers";
import { translateCurrent, useI18n } from "./i18n";
import { desktopPlatform } from "./platform";
import { FilePreviewPresentation } from "./plugins/FilePreviewPresentation";
import { UILayerPortal } from "./ui/layers/UILayerHost";

// monaco-editor is several MB of JS; a static import here would drag it into
// the eager startup chunk. Load it only when a code editor actually mounts.
const WorkspaceMonacoEditor = lazy(async () => ({
  default: (await import("./WorkspaceMonacoEditor")).WorkspaceMonacoEditor,
}));

const WorkspacePdfPreview = lazy(async () => ({
  default: (await import("./WorkspacePdfPreview")).WorkspacePdfPreview,
}));

const WORKSPACE_FILE_TREE_STYLE: CSSProperties = {
  contain: "strict",
  height: "100%",
  minHeight: 0,
  minWidth: 0,
  width: "100%"
};

const WORKSPACE_TREE_CSS = `
  :host {
    --trees-fg-override: var(--wuu-workspace-file-tree-color, var(--ink));
    --trees-fg-muted-override: var(--wuu-workspace-file-tree-muted-color, var(--ink-muted));
    --trees-bg-override: var(--wuu-workspace-file-tree-background, transparent);
    --trees-bg-muted-override: var(--wuu-workspace-file-tree-muted-background, var(--surface-2));
    --trees-search-bg-override: var(--wuu-workspace-file-tree-search-background, transparent);
    --trees-selected-fg-override: var(--wuu-workspace-file-tree-selected-color, var(--ink-strong));
    --trees-selected-bg-override: var(--wuu-workspace-file-tree-selected-background, var(--surface-3));
    --trees-selected-focused-border-color-override: var(--wuu-workspace-file-tree-selected-border, transparent);
    --trees-border-color-override: var(--wuu-workspace-file-tree-border-color, transparent);
    --trees-font-family-override: var(--wuu-workspace-file-tree-font-family, Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif);
    --trees-font-size-override: var(--font-sm);
    --trees-item-margin-x-override: 5px;
    --trees-padding-inline-override: 0px;
  }

  [data-file-tree-search-container] {
    box-sizing: border-box;
    width: 100%;
    margin-inline: 0;
    padding-inline: 8px;
  }

  [data-file-tree-search-input] {
    min-width: 0;
    margin-inline-end: 40px;
    border: var(--wuu-workspace-file-tree-search-border, 1px solid var(--hairline-strong));
    border-radius: var(--wuu-workspace-file-tree-search-radius, var(--radius-sm));
    background: var(--wuu-workspace-file-tree-search-background, transparent);
    color: var(--wuu-workspace-file-tree-color, var(--ink));
  }

  [data-file-tree-search-input]:focus-visible,
  [data-file-tree-search-input][data-file-tree-search-input-fake-focus="true"] {
    outline: none;
  }
`;

export type WorkspaceFileDirtyState = {
  root?: string;
  path?: string;
  dirty: boolean;
};

export function WorkspaceFileTree({
  activeContext,
  open,
  selectedFilePath,
  onOpenFile,
}: {
  activeContext?: RuntimeContext;
  open: boolean;
  selectedFilePath?: string;
  onOpenFile: (path: string) => void;
}): JSX.Element {
  const { locale, t } = useI18n();
  const [directories, setDirectories] = useState<Record<string, WorkspaceDirectoryListResult>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const loadingDirectoriesRef = useRef(new Set<string>());
  const workspaceRoot = activeContext?.cwd;
  const selectedWorkspaceFilePath = useMemo(
    () => normalizeSelectedWorkspaceFilePath(selectedFilePath, workspaceRoot),
    [selectedFilePath, workspaceRoot]
  );

  useEffect(() => {
    if (!open || !workspaceRoot) {
      setDirectories({});
      setLoading(false);
      setError(undefined);
      return;
    }
    let cancelled = false;
    setDirectories({});
    setLoading(true);
    setError(undefined);
    void window.wuu.listWorkspaceDirectory("", workspaceRoot).then((result) => {
      if (!cancelled) setDirectories({ "": result });
    }).catch((nextError) => {
      if (!cancelled) setError(desktopApiErrorMessage(nextError, translateCurrent("workspace.files.readFailed")));
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => { cancelled = true; };
  }, [open, workspaceRoot, locale]);

  if (!workspaceRoot) {
    return <WorkspacePanelEmpty title={t("workspace.files.noProject")} hint={t("workspace.files.noProjectDescription")} />;
  }

  if (loading && !directories[""]) {
    return <WorkspacePanelEmpty title={t("workspace.files.reading")} hint={t("workspace.files.readingDescription")} />;
  }

  if (error) {
    return <WorkspacePanelEmpty title={t("workspace.files.readFailedTitle")} description={error} />;
  }

  const rootDirectory = directories[""];
  if (!rootDirectory || rootDirectory.entries.length === 0) {
    return <WorkspacePanelEmpty title={t("workspace.files.empty")} description={formatWorkspaceRoot(workspaceRoot)} />;
  }

  return (
    <div className="workspace-file-panel">
      {rootDirectory.truncated ? <div className="workspace-file-tree-limit">{t("workspace.files.truncated")}</div> : null}
      <WorkspaceFileTreeView
        directories={directories}
        workspaceRoot={rootDirectory.root}
        selectedFilePath={selectedWorkspaceFilePath}
        onOpenFile={onOpenFile}
        onLoadDirectory={(path) => {
          if (directories[path] || loadingDirectoriesRef.current.has(path)) return;
          loadingDirectoriesRef.current.add(path);
          void window.wuu.listWorkspaceDirectory(path, workspaceRoot).then((result) => {
            setDirectories((current) => ({ ...current, [path]: result }));
          }).catch((nextError) => {
            setError(desktopApiErrorMessage(nextError, translateCurrent("workspace.files.readDirectoryFailed")));
          }).finally(() => loadingDirectoriesRef.current.delete(path));
        }}
      />
    </div>
  );
}

const WorkspaceFileTreeView = memo(function WorkspaceFileTreeView({ directories, workspaceRoot, selectedFilePath, onOpenFile, onLoadDirectory }: {
  directories: Record<string, WorkspaceDirectoryListResult>;
  workspaceRoot: string;
  selectedFilePath?: string;
  onOpenFile: (path: string) => void;
  onLoadDirectory: (path: string) => void;
}): JSX.Element {
  const { locale, t } = useI18n();
  const treeFrameRef = useRef<HTMLDivElement | null>(null);
  const paths = useMemo(() => Object.values(directories).flatMap((directory) => directory.entries.map((entry) => entry.path)), [directories]);
  // useFileTree builds its model once at mount and never re-reads the
  // options object, so the prepared input is only consumed on the first
  // render. Later directory loads stream in through model.batch below;
  // recomputing the full sorted input on every load would be discarded
  // work.
  const initialPreparedInputRef = useRef<ReturnType<typeof preparePresortedFileTreeInput> | null>(null);
  if (initialPreparedInputRef.current === null) {
    initialPreparedInputRef.current = preparePresortedFileTreeInput(paths);
  }
  const selectedFilePathRef = useRef(selectedFilePath);
  const onOpenFileRef = useRef(onOpenFile);
  const onLoadDirectoryRef = useRef(onLoadDirectory);
  const syncingSelectionRef = useRef(false);
  const { model } = useFileTree({
    flattenEmptyDirectories: false,
    initialExpansion: "closed",
    initialSelectedPaths: selectedFilePath ? [selectedFilePath] : [],
    icons: { set: "complete", colored: true },
    // The virtualizer and its shadow DOM must agree on the touch target size.
    itemHeight: window.matchMedia?.("(pointer: coarse)").matches ? 44 : 24,
    overscan: 8,
    preparedInput: initialPreparedInputRef.current,
    search: true,
    searchBlurBehavior: "retain",
    stickyFolders: false,
    unsafeCSS: WORKSPACE_TREE_CSS,
    onSelectionChange: (selectedPaths) => {
      if (syncingSelectionRef.current) return;
      const path = selectedPaths[0];
      if (!path || path.endsWith("/") || path === selectedFilePathRef.current) return;
      onOpenFileRef.current(path);
    }
  });

  useEffect(() => { onOpenFileRef.current = onOpenFile; }, [onOpenFile]);
  useEffect(() => { onLoadDirectoryRef.current = onLoadDirectory; }, [onLoadDirectory]);
  useEffect(() => {
    const addedPaths = paths.filter((path) => model.getItem(path) === null);
    if (addedPaths.length > 0) {
      model.batch(addedPaths.map((path) => ({ type: "add", path })));
    }
  }, [model, paths]);
  useEffect(() => model.subscribe(() => {
    for (const path of model.getSelectedPaths()) {
      const item = model.getItem(path);
      if (item && "isExpanded" in item && item.isExpanded()) onLoadDirectoryRef.current(path.replace(/\/$/, ""));
    }
    for (const path of paths) {
      if (!path.endsWith("/")) continue;
      const item = model.getItem(path);
      if (item && "isExpanded" in item && item.isExpanded()) onLoadDirectoryRef.current(path.replace(/\/$/, ""));
    }
  }), [model, paths]);
  useEffect(() => {
    selectedFilePathRef.current = selectedFilePath;
    if (!selectedFilePath) return;
    for (const parentPath of parentDirectoryPathsForFile(selectedFilePath)) {
      onLoadDirectoryRef.current(parentPath);
      const parent = model.getItem(`${parentPath}/`);
      if (parent && "expand" in parent) parent.expand();
    }
    const selectedItem = model.getItem(selectedFilePath);
    if (!selectedItem) return;
    syncingSelectionRef.current = true;
    try {
      for (const path of model.getSelectedPaths()) {
        if (path !== selectedFilePath) model.getItem(path)?.deselect();
      }
      selectedItem.select();
    } finally {
      syncingSelectionRef.current = false;
    }
    model.scrollToPath(selectedFilePath, { focus: false, offset: "nearest" });
  }, [model, paths, selectedFilePath]);

  useEffect(() => {
    const host = treeFrameRef.current?.querySelector<HTMLElement>("file-tree-container");
    if (!host?.shadowRoot) return undefined;
    const enhanceShadowTree = (): void => {
      const search = host.shadowRoot?.querySelector<HTMLInputElement>("[data-file-tree-search-input]");
      if (search) {
        search.placeholder = t("workspace.files.searchPlaceholder");
        // The drag handle sits above the tree's shadow root, so reserve its
        // light-DOM column on the input itself instead of overlapping it.
        search.style.marginInlineEnd = "40px";
        search.style.minWidth = "0";
        search.style.outline = "none";
      }
      const options = host.shadowRoot?.querySelector<HTMLButtonElement>("[data-type='context-menu-trigger']");
      if (options) options.setAttribute("aria-label", t("workspace.files.options"));
      // Rows render without draggable (the library's own drag-and-drop stays
      // off — it would reselect rows and mutate the tree model). Mark them
      // as drag sources here so a row can carry its workspace-relative path
      // into the composer. React does not rewrite props whose value never
      // changes, so this survives re-renders and only needs re-applying when
      // virtualization remounts a row — which this observer covers.
      for (const row of host.shadowRoot?.querySelectorAll<HTMLElement>("[data-item-path]") ?? []) {
        row.draggable = true;
      }
    };
    enhanceShadowTree();
    const observer = new MutationObserver(enhanceShadowTree);
    observer.observe(host.shadowRoot, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [locale]);

  useEffect(() => {
    const frame = treeFrameRef.current;
    if (!frame) return undefined;
    const handleDragStart = (event: DragEvent): void => {
      const path = workspaceTreeDragPath(event.composedPath());
      if (!path || !event.dataTransfer) return;
      event.dataTransfer.effectAllowed = "copy";
      event.dataTransfer.setData(WORKSPACE_FILE_DRAG_MIME, path);
      event.dataTransfer.setData("text/plain", path);
    };
    frame.addEventListener("dragstart", handleDragStart);
    return () => frame.removeEventListener("dragstart", handleDragStart);
  }, []);

  return (
    <div className="workspace-file-tree-frame" ref={treeFrameRef}>
      <FileTree
        model={model}
        style={WORKSPACE_FILE_TREE_STYLE}
        renderContextMenu={(item, context) => desktopPlatform() === "darwin" && hostSupports("showWorkspaceItemMenu") ? (
          <MacWorkspaceTreeContextMenu
            absolutePath={absoluteWorkspacePath(workspaceRoot, item.path)}
            onClose={() => context.close()}
          />
        ) : (
          <WorkspaceTreeContextMenu
            entry={{ ...item, path: absoluteWorkspacePath(workspaceRoot, item.path) }}
            workspaceRoot={workspaceRoot}
            x={context.anchorRect.left}
            y={context.anchorRect.bottom}
            onClose={() => context.close()}
          />
        )}
      />
    </div>
  );
});

function MacWorkspaceTreeContextMenu({
  absolutePath,
  onClose,
}: {
  absolutePath: string;
  onClose: () => void;
}): null {
  const openedRef = useRef(false);
  useEffect(() => {
    if (openedRef.current) return;
    openedRef.current = true;
    void window.wuu.showWorkspaceItemMenu(absolutePath).finally(onClose);
  }, [absolutePath, onClose]);
  return null;
}

function WorkspaceTreeContextMenu({
  entry,
  workspaceRoot,
  x,
  y,
  onClose
}: {
  entry: WorkspaceFileTreeEntry;
  workspaceRoot: string;
  x: number;
  y: number;
  onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const ref = useRef<HTMLDivElement | null>(null);
  // The menu mounts at the cursor, but until React commits the first
  // paint its own size isn't known — measure on the layout effect that
  // runs just before paint, clamp to viewport so the user never sees
  // it spilling off-screen.
  const [position, setPosition] = useState({ x, y });
  useLayoutEffect(() => {
    const menuElement = ref.current;
    if (!menuElement) {
      return;
    }
    const rect = menuElement.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    const margin = 8;
    const clampedX = Math.max(
      margin,
      Math.min(viewportWidth - rect.width - margin, x),
    );
    const clampedY = Math.max(
      margin,
      Math.min(viewportHeight - rect.height - margin, y),
    );
    if (clampedX !== x || clampedY !== y) {
      setPosition({ x: clampedX, y: clampedY });
    }
  }, [x, y]);

  // Outside click + Escape dismiss. Listener attach is deferred to a
  // setTimeout(0) so the original `contextmenu` event's pointerdown
  // burst doesn't fire dismiss on the same gesture that opened us.
  // Only left clicks dismiss — right clicks land on tree rows whose
  // own `onContextMenu` handler re-opens / re-anchors the menu at the
  // new cursor position; dismissing on right-click would race with
  // that re-open and kill the menu instead of repositioning it.
  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      if (event.button !== 0) {
        return;
      }
      const menuElement = ref.current;
      if (!menuElement) {
        return;
      }
      if (event.target instanceof Node && menuElement.contains(event.target)) {
        return;
      }
      onClose();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    let active = true;
    const id = window.setTimeout(() => {
      if (!active) {
        return;
      }
      document.addEventListener("pointerdown", handlePointerDown);
      document.addEventListener("keydown", handleKeyDown);
    }, 0);
    return () => {
      active = false;
      window.clearTimeout(id);
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [onClose]);

  // `entry.path` is absolute — it comes from the same main-process dir
  // listing the tree itself renders against. Strip the workspace-root
  // prefix so users get a repo-relative path for tools that take one
  // (CLI flags, CI scripts, commit messages).
  const absolutePath = entry.path;
  const relativePath = useMemo(() => {
    const normalizedRoot = workspaceRoot.replace(/\\/g, "/").replace(/\/$/, "");
    const normalizedPath = absolutePath.replace(/\\/g, "/");
    return normalizedPath.startsWith(`${normalizedRoot}/`)
      ? normalizedPath.slice(normalizedRoot.length + 1)
      : absolutePath;
  }, [absolutePath, workspaceRoot]);

  // Silent failures only — the user already saw the menu close, and a
  // notification toast for a clipboard / reveal failure would be louder
  // than the action they tried. Both outcomes dismiss the menu.
  const copyToClipboard = (value: string) => () => {
    void navigator.clipboard.writeText(value).then(
      () => onClose(),
      () => onClose(),
    );
  };
  const revealInFolder = () => {
    void window.wuu.revealWorkspaceItem(absolutePath).then(
      () => onClose(),
      () => onClose(),
    );
  };

  // Pierre Trees renders this component in a slot below its custom element.
  // Portal to the host layer so the tree's strict containment, clipping, and
  // compositor transform cannot change the fixed-position coordinate system
  // or hide the menu outside the tree panel.
  return (
    <UILayerPortal layer="menu">
      <div
        ref={ref}
        className="workspace-tree-context-menu"
        data-wuu-component="menu"
        data-wuu-layer="menu"
        data-wuu-state="open"
        role="menu"
        style={{ left: position.x, top: position.y }}
        onContextMenu={(event) => event.preventDefault()}
      >
        <button
          type="button"
          role="menuitem"
          className="workspace-tree-context-menu-item"
          onClick={copyToClipboard(absolutePath)}
        >
          {t("workspace.files.copyPath")}
        </button>
        <button
          type="button"
          role="menuitem"
          className="workspace-tree-context-menu-item"
          onClick={copyToClipboard(relativePath)}
        >
          {t("workspace.files.copyRelativePath")}
        </button>
        <button
          type="button"
          role="menuitem"
          className="workspace-tree-context-menu-item"
          onClick={copyToClipboard(entry.name)}
        >
          {t("workspace.files.copyFileName")}
        </button>
        <button
          type="button"
          role="menuitem"
          className="workspace-tree-context-menu-item"
          disabled={!hostSupports("revealWorkspaceItem")}
          onClick={revealInFolder}
        >
          {t("workspace.files.revealInFileManager")}
        </button>
      </div>
    </UILayerPortal>
  );
}

function absoluteWorkspacePath(root: string, path: string): string {
  return `${root.replace(/[\\/]+$/, "")}/${path.replace(/^\/+/, "")}`;
}

/**
 * Resolve the workspace-relative path carried by a tree row drag. Rows live
 * in the tree's shadow root, so walk the composed path instead of reading
 * event.target, which is retargeted to the shadow host. Tree paths are
 * already workspace-relative; directories keep their trailing slash.
 */
export function workspaceTreeDragPath(eventPath: readonly EventTarget[]): string | undefined {
  for (const target of eventPath) {
    if (target instanceof HTMLElement && target.dataset.itemPath) {
      return target.dataset.itemPath;
    }
  }
  return undefined;
}

function normalizeSelectedWorkspaceFilePath(path: string | undefined, workspaceRoot: string | undefined): string | undefined {
  if (!path || !workspaceRoot) {
    return undefined;
  }
  const normalizedPath = normalizePathSeparators(path).replace(/\/+$/, "");
  const normalizedRoot = normalizePathSeparators(workspaceRoot).replace(/\/+$/, "");
  const relativePath = normalizedPath.startsWith(`${normalizedRoot}/`)
    ? normalizedPath.slice(normalizedRoot.length + 1)
    : normalizedPath.startsWith("/")
      ? undefined
      : normalizedPath;
  if (!relativePath) {
    return undefined;
  }
  return normalizeWorkspaceRelativeFilePath(relativePath);
}

function normalizeWorkspaceRelativeFilePath(path: string): string | undefined {
  const value = normalizePathSeparators(path)
    .trim()
    .replace(/^\.\/+/, "")
    .replace(/^\/+/, "")
    .replace(/\/+$/, "");
  if (!value || value.includes("\0") || value.split("/").some((segment) => !segment || segment === "..")) {
    return undefined;
  }
  return value;
}

function normalizePathSeparators(path: string): string {
  return path.trim().replace(/\\/g, "/");
}

function parentDirectoryPathsForFile(path: string): string[] {
  const segments = path.split("/").filter(Boolean);
  return segments.slice(0, -1).map((_, index) => segments.slice(0, index + 1).join("/"));
}

export function WorkspacePanelEmpty({
  title,
  description,
  hint,
  icon,
  className
}: {
  title: string;
  description?: string;
  hint?: string;
  icon?: JSX.Element;
  className?: string;
}): JSX.Element {
  return (
    <div
      className={className ? `workspace-panel-empty ${className}` : "workspace-panel-empty"}
      data-wuu-component="workspace-empty-state"
    >
      <div className="workspace-panel-empty-icon" data-wuu-component="workspace-empty-icon">
        {icon ?? <FolderOpen size={24} />}
      </div>
      <strong>{title}</strong>
      {description && <span>{description}</span>}
      {hint && !isTouchWebShell() && <span>{hint}</span>}
    </div>
  );
}

export function formatWorkspaceRoot(root: string): string {
  const segments = root.split(/[\\/]/).filter(Boolean);
  return segments.at(-1) ?? root;
}

export function WorkspaceFilePreview({
  active = true,
  activeContext,
  editorResourceID,
  selectedFilePath,
  selection,
  anchor,
  refreshKey,
  onOpenRightPanel,
  onOpenFile,
  onDirtyChange
}: {
  active?: boolean;
  activeContext?: RuntimeContext;
  editorResourceID?: string;
  selectedFilePath?: string;
  selection?: WorkspaceFileSelection;
  anchor?: string;
  refreshKey?: string;
  onOpenRightPanel: () => void;
  onOpenFile?: (path: string) => void;
  onDirtyChange?: (state: WorkspaceFileDirtyState) => void;
}): JSX.Element {
  const { locale, t } = useI18n();
  const [file, setFile] = useState<WorkspaceFileReadResult | undefined>(undefined);
  const [draftText, setDraftText] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | undefined>(undefined);
  const [presenterReloadKey, setPresenterReloadKey] = useState(0);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState("");
  const editorViewStateRef = useRef<WorkspaceMonacoViewState | null>(null);
  const loadedFileKeyRef = useRef<string | undefined>(undefined);
  const markdownHostRef = useRef<HTMLDivElement>(null);
  const selectedWorkspaceFilePath = useMemo(
    () => normalizeSelectedWorkspaceFilePath(selectedFilePath, activeContext?.cwd),
    [activeContext?.cwd, selectedFilePath]
  );

  useEffect(() => {
    if (!selectedWorkspaceFilePath) {
      setFile(undefined);
      setDraftText("");
      setError(undefined);
      setLoading(false);
      return;
    }

    let cancelled = false;
    const fileKey = `${activeContext?.cwd ?? ""}\n${selectedWorkspaceFilePath}`;
    const refreshing = loadedFileKeyRef.current === fileKey;
    if (!refreshing) {
      editorViewStateRef.current = null;
      setFile(undefined);
      setDraftText("");
      setLoading(true);
    }
    setError(undefined);
    void window.wuu
      .readWorkspaceFile(selectedWorkspaceFilePath, activeContext?.cwd)
      .then((result) => {
        if (!cancelled) {
          loadedFileKeyRef.current = fileKey;
          setFile(result);
          setDraftText(result.text ?? "");
        }
      })
      .catch((nextError) => {
        if (!cancelled) {
          setError(desktopApiErrorMessage(nextError, translateCurrent("workspace.files.openFailed")));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [activeContext?.cwd, selectedWorkspaceFilePath, locale, presenterReloadKey, refreshKey]);

  const isMarkdown = Boolean(file && isMarkdownPath(file.path));
  const isMarkdownReadingMode = isMarkdown && !selection;
  const dirtyStatePath = file?.path ?? selectedWorkspaceFilePath;

  useEffect(() => {
    if (!isMarkdownReadingMode || !anchor) {
      return;
    }
    const target = Array.from(markdownHostRef.current?.querySelectorAll<HTMLElement>("[id]") ?? [])
      .find((element) => element.id === anchor);
    target?.scrollIntoView({ block: "start" });
  }, [anchor, draftText, isMarkdownReadingMode]);

  useEffect(() => {
    onDirtyChange?.({
      root: activeContext?.cwd,
      path: dirtyStatePath,
      dirty: false,
    });
  }, [activeContext?.cwd, dirtyStatePath, onDirtyChange]);

  useEffect(() => {
    return () => {
      onDirtyChange?.({
        root: activeContext?.cwd,
        path: dirtyStatePath,
        dirty: false,
      });
    };
  }, [activeContext?.cwd, dirtyStatePath, onDirtyChange]);

  if (!activeContext) {
    return (
      <div className="workspace-main-empty">
        <FolderX size={36} />
        <strong>{t("workspace.files.noProject")}</strong>
        {!isTouchWebShell() && <span>{t("workspace.files.previewNoProjectDescription")}</span>}
      </div>
    );
  }

  if (!selectedWorkspaceFilePath) {
    return (
      <div className="workspace-main-empty">
        <FolderOpen size={38} />
        <strong>{t("workspace.files.openFile")}</strong>
        {!isTouchWebShell() && <span>{t("workspace.files.openFileDescription")}</span>}
        <button type="button" onClick={onOpenRightPanel}>
          {t("workspace.files.showTree")}
        </button>
      </div>
    );
  }

  const fallback = loading ? (
      <div className="workspace-main-empty">
        <FileText size={36} />
        <strong>{t("workspace.files.opening")}</strong>
        <span>{selectedWorkspaceFilePath}</span>
      </div>
    ) : error ? (
      <div className="workspace-main-empty">
        <AlertCircle size={36} />
        <strong>{t("workspace.files.openFailedTitle")}</strong>
        <span>{error}</span>
      </div>
    ) : !file ? (
      <div className="workspace-main-empty">
        <FileText size={36} />
        <strong>{t("workspace.files.noContent")}</strong>
        <span>{selectedWorkspaceFilePath}</span>
      </div>
    ) : file.renderable_url ? (
      file.renderable_kind === "pdf" ? (
        <article className="workspace-file-preview readonly">
          <Suspense fallback={<div className="workspace-file-pdf-preview" />}>
            <WorkspacePdfPreview url={file.renderable_url} title={file.path} />
          </Suspense>
        </article>
      ) : (
      <article className="workspace-file-preview readonly">
        <div className="workspace-file-image-preview">
          <img src={file.renderable_url} alt={file.path} />
        </div>
      </article>
      )
    ) : file.binary ? (
      <div className="workspace-main-empty">
        <FileText size={36} />
        <strong>{t("workspace.files.cannotPreview")}</strong>
        <span>{t("workspace.files.binary", { path: file.path })}</span>
      </div>
    ) : (
      <article className="workspace-file-preview readonly">
        <div className={`workspace-file-editor-scroll ${isMarkdownReadingMode ? "markdown-reading" : "code"}`}>
          {isMarkdownReadingMode ? (
            <div className="workspace-markdown-reading" ref={markdownHostRef}>
              <RichContent
                text={draftText}
                cwd={activeContext.cwd}
                onOpenFile={onOpenFile}
                allowRawHtml
              />
            </div>
          ) : active ? (
            <Suspense fallback={null}>
              <WorkspaceMonacoEditor
                initialViewState={editorViewStateRef.current}
                path={file.path}
                resourceID={editorResourceID ?? `${activeContext.cwd}:${file.path}`}
                selection={selection}
                text={draftText}
                readOnly
                onViewStateChange={(viewState) => {
                  editorViewStateRef.current = viewState;
                }}
              />
            </Suspense>
          ) : null}
        </div>
      </article>
    );
  const presentation = (
    <FilePreviewPresentation
      workspaceRoot={activeContext.cwd}
      workspaceRelativePath={selectedWorkspaceFilePath}
      file={file}
      text={draftText}
      selection={selection}
      loading={loading}
      error={error}
      fallback={fallback}
      open={onOpenFile}
      reveal={hostSupports("revealWorkspaceItem") ? (path) => window.wuu.revealWorkspaceItem(`${activeContext.cwd}/${path}`) : undefined}
      reload={() => setPresenterReloadKey((value) => value + 1)}
    />
  );
  if (!window.wuu.exportWorkspaceFile) return presentation;
  return <div className="workspace-file-with-export">
    <div className="workspace-file-export-actions">
      <button type="button" disabled={loading || exporting || !file} onClick={() => {
        if (!file || exporting) return;
        setExporting(true); setExportError("");
        void window.wuu.exportWorkspaceFile!(file.path, activeContext.cwd)
          .catch(error => setExportError(desktopApiErrorMessage(error, translateCurrent("workspace.files.openFailed"))))
          .finally(() => setExporting(false));
      }}>{t("artifacts.downloadNamed", { name: file?.path ?? selectedWorkspaceFilePath })}</button>
      {exportError && <span role="alert">{exportError}</span>}
    </div>
    <div className="workspace-file-export-preview">{presentation}</div>
  </div>;
}

function isMarkdownPath(path: string): boolean {
  return /\.mdx?$/i.test(path);
}
