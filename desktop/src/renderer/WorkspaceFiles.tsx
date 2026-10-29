import { workspacePdfRevision } from "./PdfSelection";
import { ThreadContextMenu, copyToClipboard } from "./ThreadContextMenu";
import { usePreviewContent } from "./PreviewContentContext";
import { preparePresortedFileTreeInput } from "@pierre/trees";
import { FileTree, useFileTree } from "@pierre/trees/react";
import { type CSSProperties, Suspense, lazy, memo, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type {
  PreviewDirectoryV1 as WorkspaceDirectoryListResult,
  PreviewFileV1 as WorkspaceFileReadResult,
  PreviewDirectoryEntryV1 as WorkspaceFileTreeEntry,
} from "../../../packages/plugin-sdk/src/ui-preview-content";
import type { ApplicationPreviewContextV1 as PreviewWorkspaceContext } from "../../../packages/plugin-sdk/src/ui-preview";
import type { WorkspaceFileSelection } from "./LinkTargets";
import { WORKSPACE_FILE_DRAG_MIME } from "./ComposerMessages";
import { isMarkdownDocument, MarkdownDocumentReading } from "./MarkdownDocumentReading";
import { revealScrollbar } from "./ScrollbarReveal";
import { FileSelectionSurface } from "./FileSelectionSurface";
import type { WorkspaceMonacoViewState } from "./WorkspaceMonacoEditor";
import { desktopApiErrorMessage } from "./WorkspaceReviewHelpers";
import { workspacePathToSlash } from "./WorkspacePaths";
import { readWorkspacePreviewScroll, writeWorkspacePreviewScroll } from "./WorkspacePreviewRecovery";
import { translateCurrent, useI18n } from "./i18n";
import { FilePreviewPresentation } from "./plugins/FilePreviewPresentation";
import { VideoPreview } from "./VideoPreview";
import { WorkspacePanelEmpty } from "./WorkspacePanelEmpty";
import { WorkspacePreviewBoundary } from "./WorkspacePreviewBoundary";

// monaco-editor is several MB of JS; a static import here would drag it into
// the eager startup chunk. Load it only when a code editor actually mounts.
const WorkspaceMonacoEditor = lazy(async () => ({
  default: (await import("./WorkspaceMonacoEditor")).WorkspaceMonacoEditor,
}));

const WorkspacePdfPreview = lazy(async () => ({
  default: (await import("./WorkspacePdfPreview")).WorkspacePdfPreview,
}));

const WORKSPACE_FILE_TREE_STYLE = {
  contain: "strict",
  height: "100%",
  minHeight: 0,
  minWidth: 0,
  width: "100%",
  // The tree measures its scrollbar gutter once per shadow root, before
  // unsafeCSS gives the scroller the renderer's thin scrollbar, and caches the
  // wider default width. A host declaration outranks that :host value, so pass
  // the renderer's measured gutter instead. Remove once @pierre/trees measures
  // with unsafeCSS applied.
  "--trees-scrollbar-gutter-measured": "var(--scrollbar-width)"
} as CSSProperties;

const WORKSPACE_TREE_CSS = `
  :host {
    --trees-fg-override: var(--wuu-workspace-file-tree-color, var(--ink));
    --trees-fg-muted-override: var(--wuu-workspace-file-tree-muted-color, var(--ink-tertiary));
    --trees-bg-override: var(--wuu-workspace-file-tree-background, transparent);
    --trees-bg-muted-override: var(--wuu-workspace-file-tree-muted-background, var(--surface-2));
    --trees-search-bg-override: var(--wuu-workspace-file-tree-search-background, transparent);
    --trees-selected-fg-override: var(--wuu-workspace-file-tree-selected-color, var(--ink-strong));
    --trees-selected-bg-override: var(--wuu-workspace-file-tree-selected-background, var(--selection-surface));
    --trees-selected-focused-border-color-override: var(--wuu-workspace-file-tree-selected-border, transparent);
    --trees-border-color-override: var(--wuu-workspace-file-tree-border-color, transparent);
    --trees-font-family-override: var(--wuu-workspace-file-tree-font-family, var(--appearance-ui-font, system-ui, sans-serif));
    --trees-font-size-override: var(--font-ui);
    --trees-search-font-weight-override: 400;
    --trees-focus-ring-color-override: var(--focus-ring);
    /* Rows and the search field share one outer edge and one content axis:
       highlight edges meet the field border at the panel's row inset, and
       icons meet the placeholder on the pane inset. The scroll container owns
       the row inset because only it subtracts the scrollbar gutter, so rows
       and field also end on the same edge. */
    --trees-padding-inline-override: var(--workspace-row-inset);
    --trees-item-margin-x-override: 0px;
    --trees-item-padding-x-override: var(--workspace-row-pad-x);
    --workspace-tree-content-inset-end: calc(var(--workspace-file-tree-rail-inset, 0px) + max(
      var(--trees-padding-inline),
      var(--trees-scrollbar-gutter-measured, var(--trees-scrollbar-gutter))
    ));
  }

  /* Document styles stop at the shadow boundary, so the tree restates the
     renderer's scrollbar policy (styles/scrollbars.css) for its scroller. */
  [data-file-tree-virtualized-scroll='true'] {
    padding-inline-end: calc(var(--workspace-tree-content-inset-end) - var(--trees-scrollbar-gutter-measured, var(--trees-scrollbar-gutter)));
    scrollbar-width: thin;
    scrollbar-color: var(--scrollbar-ink) var(--scrollbar-track-bg);
    transition: --scrollbar-ink var(--motion-slower) var(--ease-out);
  }

  .scrollbar-visible {
    --scrollbar-ink: var(--scrollbar-thumb);
    transition-duration: var(--motion-base);
  }

  /* Search and rows share the rail inset; the scrollbar stays outside it. */
  [data-file-tree-search-container] {
    box-sizing: border-box;
    width: 100%;
    margin-inline: 0;
    padding-inline-end: var(--workspace-tree-content-inset-end);
  }

  /* The shared field box. The document's border-box sizing stops at the shadow
     boundary, so without it the row-height input grows by its padding and
     border and stands taller than the dock handle beside it. */
  [data-file-tree-search-input] {
    box-sizing: border-box;
    min-width: 0;
    height: var(--control-field-height);
    padding: var(--control-padding-block) calc(var(--trees-item-padding-x) - 1px);
    line-height: 1.5;
    border: var(--wuu-workspace-file-tree-search-border, 1px solid transparent);
    border-radius: var(--wuu-workspace-file-tree-search-radius, var(--radius-sm));
    background: var(--wuu-workspace-file-tree-search-background, var(--ink-overlay-4));
    color: var(--wuu-workspace-file-tree-color, var(--ink));
    transition:
      background-color var(--motion-fast) var(--ease-out),
      border-color var(--motion-fast) var(--ease-out);
  }

  /* A quiet filled field at rest, like the tree rows below it; editing lifts
     it onto the field surface with the shared field edge. */
  [data-file-tree-search-input]:focus,
  [data-file-tree-search-input][data-file-tree-search-input-fake-focus="true"] {
    border-color: var(--field-border);
    background: var(--wuu-workspace-file-tree-search-background, var(--field-bg));
  }

  [data-file-tree-search-input]::placeholder {
    color: var(--placeholder-ink);
  }

  /* Beside a document the dock handle ends the search row on the toolbar inset
     (workspace.css); the field stops one gap before it. */
  :host-context(.workspace-files-tree.dockable) [data-file-tree-search-container] {
    padding-inline-end: calc(var(--control-toolbar-inset) + var(--control-size-toolbar) + var(--space-2));
  }

  :host-context(html[data-focus-modality="pointer"]) [data-file-tree-search-input]:focus-visible,
  :host-context(html[data-focus-modality="pointer"]) [data-file-tree-search-input][data-file-tree-search-input-fake-focus="true"] {
    outline: none;
  }

  /* The ellipsis hides the clipped glyphs under it by painting the row's
     background. The tree itself is transparent over its panel, so the marker
     paints the panel's surface instead; hover and selection still add their
     overlay on top. */
  [data-type="item"] {
    --truncate-marker-background-color: var(--wuu-workspace-file-tree-background, var(--paper));
  }

  /* A middle-truncated name keeps its extension whole and shortens only the
     stem. The library lets the extension take a sliver of the shrink, which
     wraps its break-all measuring copy and shows a second ellipsis over the
     extension. */
  [data-truncate-group-container="middle"] > div[data-truncate-segment-priority="1"] {
    flex-shrink: 0;
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
  activeContext?: PreviewWorkspaceContext;
  open: boolean;
  selectedFilePath?: string;
  onOpenFile: (path: string) => void;
}): JSX.Element {
  const { service } = usePreviewContent();
  const { locale, t } = useI18n();
  const [directories, setDirectories] = useState<Record<string, WorkspaceDirectoryListResult>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [reloadGeneration, setReloadGeneration] = useState(0);
  const loadingDirectoriesRef = useRef(new Set<string>());
  const directoryGenerationRef = useRef(0);
  const workspaceRoot = activeContext?.cwd;
  const selectedWorkspaceFilePath = useMemo(
    () => normalizeSelectedWorkspaceFilePath(selectedFilePath, workspaceRoot),
    [selectedFilePath, workspaceRoot]
  );

  useEffect(() => {
    directoryGenerationRef.current += 1;
    loadingDirectoriesRef.current = new Set();
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
    void service.listDirectory({ path: "", root: workspaceRoot }).then((result) => {
      if (!cancelled) setDirectories({ "": result });
    }).catch((nextError) => {
      if (!cancelled) setError(desktopApiErrorMessage(nextError, translateCurrent("workspace.files.readFailed")));
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => {
      cancelled = true;
      directoryGenerationRef.current += 1;
    };
  }, [service, open, workspaceRoot, locale, reloadGeneration]);

  if (!workspaceRoot) {
    return <WorkspacePanelEmpty title={t("workspace.files.noWorkspace")} />;
  }

  if (loading && !directories[""]) {
    return <WorkspacePanelEmpty title={t("workspace.files.reading")} />;
  }

  if (error) {
    return <WorkspacePanelEmpty
      title={t("workspace.files.readFailedTitle")}
      description={error}
      action={<button type="button" className="secondary-button" onClick={() => setReloadGeneration(value => value + 1)}>
        {t("appState.retryAction")}
      </button>}
    />;
  }

  const rootDirectory = directories[""];
  if (!rootDirectory || rootDirectory.entries.length === 0) {
    return <WorkspacePanelEmpty title={t("workspace.files.empty")} description={formatWorkspaceRoot(workspaceRoot)} />;
  }

  return (
    <div className="workspace-file-panel">
      {rootDirectory.truncated ? <div className="workspace-file-tree-limit">{t("workspace.files.truncated")}</div> : null}
      <WorkspaceFileTreeView
        key={workspaceRoot}
        directories={directories}
        workspaceRoot={rootDirectory.root}
        selectedFilePath={selectedWorkspaceFilePath}
        onOpenFile={onOpenFile}
        onLoadDirectory={(path) => {
          if (directories[path] || loadingDirectoriesRef.current.has(path)) return;
          const generation = directoryGenerationRef.current;
          const pending = loadingDirectoriesRef.current;
          pending.add(path);
          void service.listDirectory({ path, root: workspaceRoot }).then((result) => {
            if (directoryGenerationRef.current === generation) {
              setDirectories((current) => ({ ...current, [path]: result }));
            }
          }).catch((nextError) => {
            if (directoryGenerationRef.current === generation) {
              setError(desktopApiErrorMessage(nextError, translateCurrent("workspace.files.readDirectoryFailed")));
            }
          }).finally(() => pending.delete(path));
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
  const { service } = usePreviewContent();
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
    // Leave room for the full supported 13–20px UI range. Keep the model's
    // virtual offsets and rendered rows identical when font preferences change.
    itemHeight: window.matchMedia?.("(pointer: coarse)").matches ? 44 : 32,
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
    // Scroll events are not composed, so the document's reveal listener never
    // sees the tree's scroller; reveal its thumb from inside the shadow root.
    const shadowRoot = host.shadowRoot;
    const handleScroll = (event: Event): void => {
      if (event.target instanceof HTMLElement) revealScrollbar(event.target);
    };
    shadowRoot.addEventListener("scroll", handleScroll, { capture: true, passive: true });
    return () => {
      observer.disconnect();
      shadowRoot.removeEventListener("scroll", handleScroll, { capture: true });
    };
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
        renderContextMenu={(item, context) => service.capabilities.nativeItemMenu ? (
          <MacWorkspaceTreeContextMenu
            path={item.path}
            workspaceRoot={workspaceRoot}
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
  path,
  workspaceRoot,
  onClose,
}: {
  path: string;
  workspaceRoot: string;
  onClose: () => void;
}): null {
  const { service } = usePreviewContent();
  const openedRef = useRef(false);
  useEffect(() => {
    if (openedRef.current) return;
    openedRef.current = true;
    void service.showItemMenu({ path, root: workspaceRoot }).then(onClose, onClose);
  }, [service, path, workspaceRoot, onClose]);
  return null;
}

function WorkspaceTreeContextMenu({
  entry, workspaceRoot, x, y, onClose
}: {
  entry: WorkspaceFileTreeEntry;
  workspaceRoot: string;
  x: number;
  y: number;
  onClose: () => void;
}): JSX.Element {
  const { service } = usePreviewContent();
  const { t } = useI18n();
  const absolutePath = entry.path;
  const normalizedRoot = workspaceRoot.replace(/\\/g, "/").replace(/\/$/, "");
  const normalizedPath = absolutePath.replace(/\\/g, "/");
  const relativePath = normalizedPath.startsWith(`${normalizedRoot}/`)
    ? normalizedPath.slice(normalizedRoot.length + 1) : absolutePath;
  return <ThreadContextMenu x={x} y={y} onClose={onClose} items={[
    { label: t("workspace.files.copyPath"), onSelect: () => { void copyToClipboard(absolutePath); } },
    { label: t("workspace.files.copyRelativePath"), onSelect: () => { void copyToClipboard(relativePath); } },
    { label: t("workspace.files.copyFileName"), onSelect: () => { void copyToClipboard(entry.name); } },
    { separator: true },
    { label: t("workspace.files.revealInFileManager"), disabled: !service.capabilities.revealItem,
      onSelect: () => service.revealItem({ root: workspaceRoot, path: relativePath }).catch(error => console.error("[desktop] reveal item failed", error)) },
  ]} />;
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
  const normalizedPath = workspacePathToSlash(path, workspaceRoot).replace(/\/+$/, "");
  const normalizedRoot = workspacePathToSlash(workspaceRoot, workspaceRoot).replace(/\/+$/, "");
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
  const value = path
    .replace(/^\.\/+/, "")
    .replace(/^\/+/, "")
    .replace(/\/+$/, "");
  if (!value || value.includes("\0") || value.split("/").some((segment) => !segment || segment === "..")) {
    return undefined;
  }
  return value;
}

function parentDirectoryPathsForFile(path: string): string[] {
  const segments = path.split("/").filter(Boolean);
  return segments.slice(0, -1).map((_, index) => segments.slice(0, index + 1).join("/"));
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
  onOpenFile,
  onDirtyChange
}: {
  active?: boolean;
  activeContext?: PreviewWorkspaceContext;
  editorResourceID?: string;
  selectedFilePath?: string;
  selection?: WorkspaceFileSelection;
  anchor?: string;
  refreshKey?: string;
  onOpenFile?: (path: string) => void;
  onDirtyChange?: (state: WorkspaceFileDirtyState) => void;
}): JSX.Element {
  const { service } = usePreviewContent();
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
  const fileScrollRef = useRef<HTMLDivElement>(null);
  const restoredScrollRef = useRef(false);
  const selectedWorkspaceFilePath = useMemo(
    () => normalizeSelectedWorkspaceFilePath(selectedFilePath, activeContext?.cwd),
    [activeContext?.cwd, selectedFilePath]
  );

  const selectedFileKey = `${activeContext?.cwd ?? ""}\n${selectedWorkspaceFilePath ?? ""}`;
  const selectedFileKeyRef = useRef(selectedFileKey);
  const exportGenerationRef = useRef(0);
  useLayoutEffect(() => {
    selectedFileKeyRef.current = selectedFileKey;
    exportGenerationRef.current += 1;
    return () => { exportGenerationRef.current += 1; };
  }, [selectedFileKey, service]);
  useEffect(() => {
    setExporting(false);
    setExportError("");
  }, [selectedFileKey, service]);

  useEffect(() => {
    if (!selectedWorkspaceFilePath || !activeContext?.cwd) {
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
    void service.readFile({ path: selectedWorkspaceFilePath, root: activeContext.cwd })
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
  }, [service, activeContext?.cwd, selectedWorkspaceFilePath, locale, presenterReloadKey, refreshKey]);

  const isMarkdown = Boolean(file && isMarkdownDocument(file.path));
  const isMarkdownReadingMode = isMarkdown && !selection;
  const dirtyStatePath = file?.path ?? selectedWorkspaceFilePath;

  useLayoutEffect(() => {
    if (!active || !isMarkdownReadingMode || !editorResourceID || !fileScrollRef.current) return;
    const scroll = readWorkspacePreviewScroll(editorResourceID);
    if (scroll !== undefined) {
      fileScrollRef.current.scrollTop = scroll;
      restoredScrollRef.current = Boolean(anchor);
    }
  }, [active, editorResourceID, isMarkdownReadingMode, file?.path]);

  useEffect(() => {
    if (!active || !isMarkdownReadingMode || !anchor) {
      return;
    }
    if (restoredScrollRef.current) { restoredScrollRef.current = false; return; }
    const target = Array.from(markdownHostRef.current?.querySelectorAll<HTMLElement>("[id]") ?? [])
      .find((element) => element.id === anchor);
    target?.scrollIntoView({ block: "start" });
  }, [active, anchor, draftText, isMarkdownReadingMode]);

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
    return <WorkspacePanelEmpty title={t("workspace.files.noWorkspace")} />;
  }

  if (!selectedWorkspaceFilePath) {
    return <WorkspacePanelEmpty title={t("workspace.files.noContent")} />;
  }

  const fallback = loading ? (
      <WorkspacePanelEmpty title={t("workspace.files.opening")} />
    ) : error ? (
      <WorkspacePanelEmpty
        title={t("workspace.files.openFailedTitle")}
        description={error}
        action={<button type="button" className="secondary-button" onClick={() => setPresenterReloadKey(value => value + 1)}>
          {t("appState.retryAction")}
        </button>}
      />
    ) : !file ? (
      <WorkspacePanelEmpty title={t("workspace.files.noContent")} />
    ) : file.renderable_url ? (
      file.renderable_kind === "video" ? (
        <article className="workspace-file-preview readonly">
          <VideoPreview src={file.renderable_url} title={file.path} active={active} />
        </article>
      ) : file.renderable_kind === "pdf" ? (
        <article className="workspace-file-preview readonly">
          <WorkspacePreviewBoundary resourceKey={file.path}>
            <Suspense fallback={<div className="workspace-file-pdf-preview" />}>
              <WorkspacePdfPreview url={file.renderable_url} title={file.path} active={active}
                source={{ workspace: activeContext.cwd, path: file.path, revision: workspacePdfRevision(file) }}
                initialPage={anchor?.startsWith("pdf-page=") ? Number(new URLSearchParams(anchor).get("pdf-page")) : undefined}
                pageRequest={anchor}
                onRefresh={async (signal) => {
                  const refreshed = await service.readFile({ path: file.path, root: activeContext.cwd }, { signal });
                  if (signal.aborted || selectedFileKeyRef.current !== selectedFileKey) return false;
                  setFile(refreshed);
                  return refreshed.renderable_kind === "pdf"
                    && refreshed.renderable_url === file.renderable_url
                    && workspacePdfRevision(refreshed) === workspacePdfRevision(file);
                }} />
            </Suspense>
          </WorkspacePreviewBoundary>
        </article>
      ) : (
      <article className="workspace-file-preview readonly">
        <div className="workspace-file-image-preview">
          <img src={file.renderable_url} alt={file.path} />
        </div>
      </article>
      )
    ) : file.binary ? (
      <WorkspacePanelEmpty
        title={t("workspace.files.cannotPreview")}
        description={t("workspace.files.binary")}
        action={service.capabilities.revealItem ? (
          <button
            className="settings-button"
            type="button"
            onClick={() => void service.revealItem({ path: file.path, root: activeContext.cwd })}
          >
            {t("workspace.files.revealInFileManager")}
          </button>
        ) : undefined}
      />
    ) : (
      <article className="workspace-file-preview readonly">
        {file.truncated ? <p className="workspace-file-preview-notice" role="status">{t("workspace.files.previewTruncated")}</p> : null}
        <FileSelectionSurface key={`${activeContext.cwd}:${file.path}`} workspace={activeContext.cwd} path={file.path} text={draftText} active={active}>
          {(selectionControls) => (
            <div ref={fileScrollRef} className={`workspace-file-editor-scroll ${isMarkdownReadingMode ? "markdown-reading" : "code"}`}
              onScroll={event => { if (isMarkdownReadingMode && editorResourceID) writeWorkspacePreviewScroll(editorResourceID, event.currentTarget.scrollTop); }}>
              {isMarkdownReadingMode ? (
                <MarkdownDocumentReading
                  ref={markdownHostRef}
                  text={draftText}
                  cwd={activeContext.cwd}
                  documentPath={file.path}
                  onOpenFile={onOpenFile}
                  allowRawHtml
                  sourceMapping
                />
              ) : active ? (
                <WorkspacePreviewBoundary resourceKey={file.path}>
                  <Suspense fallback={null}>
                    <WorkspaceMonacoEditor
                      {...selectionControls}
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
                </WorkspacePreviewBoundary>
              ) : null}
            </div>
          )}
        </FileSelectionSurface>
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
      reveal={service.capabilities.revealItem ? (path) => service.revealItem({ path, root: activeContext.cwd }) : undefined}
      reload={() => setPresenterReloadKey((value) => value + 1)}
    />
  );
  if (!service.capabilities.exportFile) return presentation;
  return <div className="workspace-file-with-export">
    <div className="workspace-file-export-actions">
      <button type="button" disabled={loading || exporting || !file} onClick={() => {
        if (!file || exporting) return;
        const generation = exportGenerationRef.current;
        setExporting(true); setExportError("");
        void service.exportFile({ path: file.path, root: activeContext.cwd })
          .catch(error => {
            if (exportGenerationRef.current === generation) {
              setExportError(desktopApiErrorMessage(error, translateCurrent("workspace.files.openFailed")));
            }
          })
          .finally(() => {
            if (exportGenerationRef.current === generation) setExporting(false);
          });
      }}>{t("artifacts.downloadNamed", { name: file?.path ?? selectedWorkspaceFilePath })}</button>
      {exportError && <span role="alert">{exportError}</span>}
    </div>
    <div className="workspace-file-export-preview">{presentation}</div>
  </div>;
}
