import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  ChevronRight,
  FileText,
  Folder,
  FolderOpen,
} from "./WuuIcons";
import {
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  type RefObject,
  Suspense,
  lazy,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState
} from "react";
import type { GitChangeFile, GitChangesResult, GitFileDiffResult, GitStatusResult } from "../shared/protocol";
import { TruncatedText } from "./TruncatedText";
import { WorkspacePanelEmpty } from "./WorkspacePanelEmpty";
import {
  buildGitChangeTree,
  desktopApiErrorMessage,
  desktopApiSupportsGitReview,
  expandedGitChangeTreePathsForSelection,
  filterGitChangeFiles,
  gitChangeStatusLabel,
  gitChangeStatusText,
  gitDiffDisplayLines,
  gitPathAncestors,
  selectGitChangePath,
  summarizeGitChangeFiles,
  type GitChangeTreeNode
} from "./WorkspaceReviewHelpers";
import { useI18n } from "./i18n";

const WorkspaceMonacoDiffEditor = lazy(async () => ({
  default: (await import("./WorkspaceMonacoDiffEditor")).WorkspaceMonacoDiffEditor,
}));

const WORKSPACE_REVIEW_TREE_DEFAULT_WIDTH = 280;
const WORKSPACE_REVIEW_TREE_MIN_WIDTH = 220;
const WORKSPACE_REVIEW_TREE_MAX_WIDTH = 360;
const WORKSPACE_REVIEW_DIFF_MIN_WIDTH = 420;
const WORKSPACE_REVIEW_RESIZER_WIDTH = 8;
// Narrower than this, the diff and the change list cannot both stay readable
// side by side: the list folds into a switcher above a full-width diff.
const WORKSPACE_REVIEW_SPLIT_MIN_WIDTH =
  WORKSPACE_REVIEW_TREE_MIN_WIDTH + WORKSPACE_REVIEW_RESIZER_WIDTH + WORKSPACE_REVIEW_DIFF_MIN_WIDTH;
const WORKSPACE_REVIEW_TREE_STEP = 24;
const WORKSPACE_REVIEW_TREE_WIDTH_KEY = "wuu.desktop.reviewTreeWidth";

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

function initialWorkspaceReviewTreeWidth(): number {
  const stored = Number(window.localStorage.getItem(WORKSPACE_REVIEW_TREE_WIDTH_KEY));
  if (!Number.isFinite(stored)) {
    return WORKSPACE_REVIEW_TREE_DEFAULT_WIDTH;
  }
  return clamp(stored, WORKSPACE_REVIEW_TREE_MIN_WIDTH, WORKSPACE_REVIEW_TREE_MAX_WIDTH);
}

function clampWorkspaceReviewTreeWidth(width: number, panelWidth = Number.POSITIVE_INFINITY): number {
  if (!Number.isFinite(panelWidth)) {
    return clamp(width, WORKSPACE_REVIEW_TREE_MIN_WIDTH, WORKSPACE_REVIEW_TREE_MAX_WIDTH);
  }
  const maxForPanel = Math.max(
    WORKSPACE_REVIEW_TREE_MIN_WIDTH,
    Math.min(WORKSPACE_REVIEW_TREE_MAX_WIDTH, panelWidth - WORKSPACE_REVIEW_DIFF_MIN_WIDTH)
  );
  return clamp(width, WORKSPACE_REVIEW_TREE_MIN_WIDTH, maxForPanel);
}

// Files in the order the change list shows them, for stepping.
function gitChangeTreeFilePaths(nodes: GitChangeTreeNode[]): string[] {
  return nodes.flatMap((node) => (node.kind === "file" ? [node.path] : gitChangeTreeFilePaths(node.children)));
}

type ReviewSwitcher = {
  open: boolean;
  listID: string;
  triggerRef: RefObject<HTMLButtonElement | null>;
  onToggle: () => void;
  onClose: (restoreFocus: boolean) => void;
  onPrevious?: () => void;
  onNext?: () => void;
  list: ReactNode;
};

export function WorkspaceReviewPanel({
  gitStatus,
  workspaceRoot,
}: {
  gitStatus?: GitStatusResult;
  workspaceRoot?: string;
}): JSX.Element {
  const { locale, t } = useI18n();
  const panelRef = useRef<HTMLDivElement | null>(null);
  const switcherRef = useRef<HTMLButtonElement>(null);
  const splitResizeRef = useRef<{ startX: number; startTreeWidth: number } | null>(null);
  const listID = useId();
  const [panelNode, setPanelNode] = useState<HTMLDivElement | null>(null);
  const [changes, setChanges] = useState<GitChangesResult | undefined>(undefined);
  const [selectedPath, setSelectedPath] = useState<string | undefined>(undefined);
  const [fileDiff, setFileDiff] = useState<GitFileDiffResult | undefined>(undefined);
  const [loadingChanges, setLoadingChanges] = useState(false);
  const [loadingDiff, setLoadingDiff] = useState(false);
  const [error, setError] = useState<string | undefined>(undefined);
  const [treeQuery, setTreeQuery] = useState("");
  const [expandedPaths, setExpandedPaths] = useState<Set<string>>(() => new Set());
  const [treePaneWidth, setTreePaneWidth] = useState(initialWorkspaceReviewTreeWidth);
  const [resizingSplit, setResizingSplit] = useState(false);
  const [compact, setCompact] = useState(false);
  const [listOpen, setListOpen] = useState(false);
  const files = changes?.files ?? [];
  const filteredFiles = useMemo(() => filterGitChangeFiles(files, treeQuery), [files, treeQuery]);
  const treeNodes = useMemo(() => buildGitChangeTree(filteredFiles), [filteredFiles]);
  const orderedPaths = useMemo(() => gitChangeTreeFilePaths(buildGitChangeTree(files)), [files]);
  const selectedFile = files.find((file) => file.path === selectedPath);
  const singleFileReview = Boolean(selectedFile && files.length === 1);
  const switcherLayout = compact && Boolean(selectedFile) && !singleFileReview;
  const sheetOpen = switcherLayout && listOpen;
  const panelStyle = {
    "--workspace-review-tree-width": `${treePaneWidth}px`
  } as CSSProperties;

  useEffect(() => {
    let cancelled = false;
    setChanges(undefined);
    setSelectedPath(undefined);
    setFileDiff(undefined);
    if (!desktopApiSupportsGitReview()) {
      setError(t("workspaceReview.apiUnavailable"));
      setLoadingChanges(false);
      return;
    }
    setLoadingChanges(true);
    setError(undefined);
    void window.wuu
      .listGitChanges(workspaceRoot)
      .then((result) => {
        if (cancelled) {
          return;
        }
        const nextSelectedPath = selectGitChangePath(result.files, selectedPath);
        setChanges(result);
        setSelectedPath(nextSelectedPath);
        setExpandedPaths(expandedGitChangeTreePathsForSelection(nextSelectedPath));
      })
      .catch((nextError) => {
        if (!cancelled) {
          setError(desktopApiErrorMessage(nextError, t("workspaceReview.readChangesFailed")));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingChanges(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [locale, workspaceRoot]);

  useEffect(() => {
    if (!selectedPath) {
      setFileDiff(undefined);
      setLoadingDiff(false);
      return;
    }
    setExpandedPaths((current) => {
      const next = new Set(current);
      for (const ancestor of gitPathAncestors(selectedPath)) {
        next.add(ancestor);
      }
      return next;
    });
  }, [selectedPath]);

  useEffect(() => {
    if (!selectedPath) {
      return;
    }
    let cancelled = false;
    setFileDiff(undefined);
    if (!desktopApiSupportsGitReview()) {
      setError(t("workspaceReview.apiUnavailable"));
      setLoadingDiff(false);
      return;
    }
    setLoadingDiff(true);
    setError(undefined);
    void window.wuu
      .readGitFileDiff(selectedPath, workspaceRoot)
      .then((result) => {
        if (!cancelled) {
          setFileDiff(result);
        }
      })
      .catch((nextError) => {
        if (!cancelled) {
          setError(desktopApiErrorMessage(nextError, t("workspaceReview.readDiffFailed")));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingDiff(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [selectedPath, locale, workspaceRoot]);

  useEffect(() => {
    window.localStorage.setItem(WORKSPACE_REVIEW_TREE_WIDTH_KEY, String(treePaneWidth));
  }, [treePaneWidth]);

  // The layout follows the panel's own width, so a docked, resized or
  // full-window panel each gets the arrangement that fits it.
  useLayoutEffect(() => {
    if (!panelNode) {
      return undefined;
    }
    const measure = (): void => {
      const narrow = panelNode.getBoundingClientRect().width < WORKSPACE_REVIEW_SPLIT_MIN_WIDTH;
      setCompact(narrow);
      if (!narrow) {
        setListOpen(false);
      }
    };
    measure();
    if (typeof ResizeObserver === "undefined") {
      return undefined;
    }
    const observer = new ResizeObserver(measure);
    observer.observe(panelNode);
    return () => observer.disconnect();
  }, [panelNode]);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.toggle("resizing-review-split", resizingSplit);
    if (!resizingSplit) {
      return () => root.classList.remove("resizing-review-split");
    }

    function handlePointerMove(event: PointerEvent): void {
      const session = splitResizeRef.current;
      if (!session) {
        return;
      }
      const panelWidth = panelRef.current?.getBoundingClientRect().width;
      setTreePaneWidth(
        clampWorkspaceReviewTreeWidth(session.startTreeWidth - (event.clientX - session.startX), panelWidth)
      );
    }

    function handlePointerUp(): void {
      splitResizeRef.current = null;
      setResizingSplit(false);
    }

    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp);
    window.addEventListener("pointercancel", handlePointerUp);
    return () => {
      root.classList.remove("resizing-review-split");
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
      window.removeEventListener("pointercancel", handlePointerUp);
    };
  }, [resizingSplit]);

  function toggleTreePath(path: string): void {
    setExpandedPaths((current) => {
      const next = new Set(current);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
      }
      return next;
    });
  }

  function resizeTreePaneBy(delta: number): void {
    const panelWidth = panelRef.current?.getBoundingClientRect().width;
    setTreePaneWidth((current) => clampWorkspaceReviewTreeWidth(current + delta, panelWidth));
  }

  function startReviewSplitResize(event: ReactPointerEvent<HTMLDivElement>): void {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    splitResizeRef.current = {
      startX: event.clientX,
      startTreeWidth: treePaneWidth
    };
    setResizingSplit(true);
  }

  function handleReviewSplitKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      resizeTreePaneBy(WORKSPACE_REVIEW_TREE_STEP);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      resizeTreePaneBy(-WORKSPACE_REVIEW_TREE_STEP);
    } else if (event.key === "Home") {
      event.preventDefault();
      resizeTreePaneBy(WORKSPACE_REVIEW_TREE_MAX_WIDTH);
    } else if (event.key === "End") {
      event.preventDefault();
      resizeTreePaneBy(-WORKSPACE_REVIEW_TREE_MAX_WIDTH);
    }
  }

  function closeList(restoreFocus: boolean): void {
    setListOpen(false);
    setTreeQuery("");
    if (restoreFocus) {
      switcherRef.current?.focus();
    }
  }

  function selectFile(path: string): void {
    setSelectedPath(path);
    if (sheetOpen) {
      closeList(true);
    }
  }

  if (loadingChanges && !changes) {
    return <WorkspacePanelEmpty title={t("workspaceReview.readingChanges")} />;
  }

  if (error && !changes) {
    return <WorkspacePanelEmpty title={t("workspaceReview.readFailed")} description={error} />;
  }

  if (changes && !changes.is_repo) {
    return <WorkspacePanelEmpty title={t("workspaceReview.notGitRepository")} />;
  }

  if (changes && files.length === 0) {
    return <WorkspacePanelEmpty title={t("workspaceReview.clean")} />;
  }

  const selectedIndex = selectedPath ? orderedPaths.indexOf(selectedPath) : -1;
  const changeList = (
    <GitChangeTreePanel
      branch={gitStatus?.is_repo ? gitStatus.branch : undefined}
      files={filteredFiles}
      nodes={treeNodes}
      selectedPath={selectedPath}
      expandedPaths={expandedPaths}
      query={treeQuery}
      autoFocusFilter={sheetOpen}
      onQueryChange={setTreeQuery}
      onSelectFile={selectFile}
      onTogglePath={toggleTreePath}
    />
  );

  return (
    <div
      className={`workspace-review-panel${selectedFile ? " has-diff" : ""}${
        singleFileReview ? " single-file" : ""
      }${switcherLayout ? " compact" : ""}${
        resizingSplit ? " resizing-split" : ""
      }`}
      aria-label={t("workspaceReview.reviewChanges")}
      data-wuu-component="workspace-review"
      data-wuu-state={selectedFile ? "detail" : "navigation"}
      ref={(node) => {
        panelRef.current = node;
        setPanelNode(node);
      }}
      style={panelStyle}
    >
      {selectedFile ? (
        <WorkspaceReviewDiffPeekPanel
          file={selectedFile}
          fileDiff={fileDiff}
          loading={loadingDiff}
          error={error}
          switcher={switcherLayout ? {
            open: sheetOpen,
            listID,
            triggerRef: switcherRef,
            onToggle: () => (sheetOpen ? closeList(false) : setListOpen(true)),
            onClose: closeList,
            onPrevious: selectedIndex > 0 ? () => setSelectedPath(orderedPaths[selectedIndex - 1]) : undefined,
            onNext: selectedIndex >= 0 && selectedIndex < orderedPaths.length - 1
              ? () => setSelectedPath(orderedPaths[selectedIndex + 1])
              : undefined,
            list: changeList,
          } : undefined}
        />
      ) : null}
      {selectedFile && !singleFileReview && !switcherLayout ? (
        <div
          className="workspace-review-resizer"
          role="separator"
          aria-label={t("workspaceReview.resizeDiffTree")}
          aria-orientation="vertical"
          aria-valuemin={WORKSPACE_REVIEW_TREE_MIN_WIDTH}
          aria-valuemax={WORKSPACE_REVIEW_TREE_MAX_WIDTH}
          aria-valuenow={Math.round(treePaneWidth)}
          tabIndex={0}
          onPointerDown={startReviewSplitResize}
          onKeyDown={handleReviewSplitKeyDown}
        />
      ) : null}
      {!singleFileReview && !switcherLayout ? (
        <div className="workspace-review-tree-pane">
          {changeList}
          {error && !selectedFile ? <div className="workspace-review-overlay error">{error}</div> : null}
        </div>
      ) : null}
    </div>
  );
}

function WorkspaceReviewDiffPeekPanel({
  file,
  fileDiff,
  loading,
  error,
  switcher,
}: {
  file: GitChangeFile;
  fileDiff?: GitFileDiffResult;
  loading: boolean;
  error?: string;
  switcher?: ReviewSwitcher;
}): JSX.Element {
  const { t } = useI18n();
  const headerRef = useRef<HTMLDivElement>(null);
  const open = switcher?.open ?? false;
  const onClose = switcher?.onClose;

  // The list sheet closes like a menu: a press outside it or the switcher.
  useEffect(() => {
    if (!open || !onClose) {
      return undefined;
    }
    function handlePointerDown(event: PointerEvent): void {
      if (event.target instanceof Node && headerRef.current?.contains(event.target)) {
        return;
      }
      onClose?.(false);
    }
    document.addEventListener("pointerdown", handlePointerDown);
    return () => document.removeEventListener("pointerdown", handlePointerDown);
  }, [open, onClose]);

  return (
    <section
      className="workspace-review-diff-panel workspace-diff-detail"
      data-wuu-component="workspace-review-content"
      aria-label={t("workspaceReview.codeDiffFor", { path: file.path })}
    >
      <div
        className="workspace-diff-detail-header"
        data-wuu-component="workspace-review-content-header"
        ref={headerRef}
        onKeyDown={(event) => {
          if (event.key === "Escape" && open) {
            event.preventDefault();
            event.stopPropagation();
            switcher?.onClose(true);
          }
        }}
      >
        {switcher ? (
          <>
            <button
              ref={switcher.triggerRef}
              className="workspace-review-switcher"
              type="button"
              aria-expanded={switcher.open}
              aria-controls={switcher.listID}
              onClick={switcher.onToggle}
            >
              <WorkspaceReviewFileTitle file={file} />
              <ChevronDown className="icon-sm workspace-review-switcher-chevron" aria-hidden="true" />
            </button>
            <div className="workspace-review-stepper">
              <button
                className="icon-button"
                type="button"
                aria-label={t("workspaceReview.previousFile")}
                title={t("workspaceReview.previousFile")}
                disabled={!switcher.onPrevious}
                onClick={switcher.onPrevious}
              >
                <ArrowUp className="icon" />
              </button>
              <button
                className="icon-button"
                type="button"
                aria-label={t("workspaceReview.nextFile")}
                title={t("workspaceReview.nextFile")}
                disabled={!switcher.onNext}
                onClick={switcher.onNext}
              >
                <ArrowDown className="icon" />
              </button>
            </div>
            {switcher.open ? (
              <div className="workspace-review-sheet" id={switcher.listID} data-wuu-layer="menu">
                {switcher.list}
              </div>
            ) : null}
          </>
        ) : (
          <div className="workspace-review-file-row">
            <WorkspaceReviewFileTitle file={file} />
          </div>
        )}
      </div>
      <WorkspaceDiffBody fileDiff={fileDiff} loading={loading} error={error} />
      {fileDiff?.truncated ? (
        <div className="workspace-diff-truncated">
          {t("workspaceReview.diffTruncated")}
        </div>
      ) : null}
    </section>
  );
}

/**
 * The file a diff shows: its name first and whole, its folder (or former
 * path) as quieter context that gives way first, then status and counts.
 */
function WorkspaceReviewFileTitle({ file }: { file: GitChangeFile }): JSX.Element {
  const { t } = useI18n();
  const separator = file.path.lastIndexOf("/");
  const name = file.path.slice(separator + 1);
  const renamed = Boolean(file.old_path && file.old_path !== file.path);
  const context = renamed
    ? t("workspaceReview.renamedFrom", { path: file.old_path ?? "" })
    : separator > 0 ? file.path.slice(0, separator) : "";
  return (
    <>
      <span className="workspace-review-file">
        <TruncatedText as="strong" className="workspace-review-file-name" text={name} />
        {context ? (
          <TruncatedText
            className={`workspace-review-file-context${renamed ? "" : " is-folder"}`}
            text={context}
          />
        ) : null}
      </span>
      <span className="workspace-review-file-meta">
        <span>{gitChangeStatusText(file.status)}</span>
        {file.binary ? null : <GitChangeCounts additions={file.additions} deletions={file.deletions} />}
      </span>
    </>
  );
}

function GitChangeCounts({ additions, deletions }: { additions: number; deletions: number }): JSX.Element {
  const { formatNumber } = useI18n();
  return (
    <span className="workspace-change-counts">
      <span className="additions">+{formatNumber(additions)}</span>
      <span className="deletions">−{formatNumber(deletions)}</span>
    </span>
  );
}

function WorkspaceDiffBody({
  fileDiff,
  loading,
  error,
}: {
  fileDiff?: GitFileDiffResult;
  loading: boolean;
  error?: string;
}): JSX.Element {
  const { t } = useI18n();
  if (error) return <div className="workspace-diff-error">{error}</div>;
  if (loading) return <div className="workspace-diff-empty">{t("workspaceReview.readingDiff")}</div>;
  if (fileDiff?.binary) {
    return <div className="workspace-diff-empty">{t("workspaceReview.binaryNoTextDiff")}</div>;
  }
  if (fileDiff && typeof fileDiff.original_text === "string" && typeof fileDiff.modified_text === "string") {
    return (
      <Suspense fallback={<div className="workspace-diff-empty">{t("workspaceReview.readingDiff")}</div>}>
        <WorkspaceMonacoDiffEditor
          path={fileDiff.path}
          originalText={fileDiff.original_text}
          modifiedText={fileDiff.modified_text}
        />
      </Suspense>
    );
  }
  if (!fileDiff?.patch) {
    return <div className="workspace-diff-empty">{t("workspaceReview.noTextDiff")}</div>;
  }
  return (
    <div className="workspace-diff-code-scroll">
      <GitPatchLines patch={fileDiff.patch} label={t("workspaceReview.codeDiffFor", { path: fileDiff.path })} />
    </div>
  );
}

/** A unified Git patch with old and new line numbers. */
export function GitPatchLines({ patch, label }: { patch: string; label: string }): JSX.Element {
  const diffLines = useMemo(() => gitDiffDisplayLines(patch), [patch]);
  return (
    <pre className="workspace-diff-code" aria-label={label}>
      {diffLines.map((line, index) => (
        <span className={`workspace-diff-line ${line.kind}`} key={`${index}:${line.content.slice(0, 24)}`}>
          <span className="workspace-diff-line-number">{line.oldLine ?? ""}</span>
          <span className="workspace-diff-line-number">{line.newLine ?? ""}</span>
          <span className="workspace-diff-line-code">{line.content || " "}</span>
        </span>
      ))}
    </pre>
  );
}

function GitChangeTreePanel({
  branch,
  files,
  nodes,
  selectedPath,
  expandedPaths,
  query,
  autoFocusFilter,
  onQueryChange,
  onSelectFile,
  onTogglePath
}: {
  branch?: string;
  files: GitChangeFile[];
  nodes: GitChangeTreeNode[];
  selectedPath?: string;
  expandedPaths: Set<string>;
  query: string;
  autoFocusFilter: boolean;
  onQueryChange: (value: string) => void;
  onSelectFile: (path: string) => void;
  onTogglePath: (path: string) => void;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const listRef = useRef<HTMLDivElement>(null);
  const filterRef = useRef<HTMLInputElement>(null);
  const forceExpanded = query.trim().length > 0;
  const totals = summarizeGitChangeFiles(files);

  useEffect(() => {
    if (autoFocusFilter) {
      filterRef.current?.focus();
    }
  }, [autoFocusFilter]);

  // Rows move like a tree: arrows walk the visible rows, Right and Left open
  // and close folders.
  function handleListKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    const rows = Array.from(listRef.current?.querySelectorAll<HTMLButtonElement>(".workspace-diff-tree-row") ?? []);
    const index = rows.indexOf(document.activeElement as HTMLButtonElement);
    const row = rows[index];
    const target =
      event.key === "ArrowDown" ? rows[index + 1]
        : event.key === "ArrowUp" ? rows[index - 1]
          : event.key === "Home" ? rows[0]
            : event.key === "End" ? rows.at(-1)
              : undefined;
    if (target) {
      event.preventDefault();
      target.focus();
      return;
    }
    const expanded = row?.getAttribute("aria-expanded");
    if ((event.key === "ArrowRight" && expanded === "false") || (event.key === "ArrowLeft" && expanded === "true")) {
      event.preventDefault();
      row.click();
    }
  }

  return (
    <aside
      className="workspace-diff-tree"
      data-wuu-component="workspace-review-navigation"
      aria-label={t("workspaceReview.changeFileTree")}
    >
      <div className="workspace-diff-tree-header">
        <span className="workspace-diff-tree-summary">
          <strong>
            {t(
              forceExpanded
                ? files.length === 1
                  ? "workspaceReview.matchCountOne"
                  : "workspaceReview.matchCount"
                : files.length === 1
                  ? "environment.fileCountOne"
                  : "environment.fileCount",
              { count: formatNumber(files.length) },
            )}
          </strong>
          {files.length > 0 ? <GitChangeCounts additions={totals.additions} deletions={totals.deletions} /> : null}
        </span>
        {branch ? <TruncatedText className="workspace-diff-tree-branch" text={branch} /> : null}
      </div>
      <input
        ref={filterRef}
        className="workspace-diff-search"
        data-wuu-component="workspace-review-search"
        type="search"
        value={query}
        placeholder={t("workspaceReview.filterFiles")}
        aria-label={t("workspaceReview.filterFiles")}
        onChange={(event) => onQueryChange(event.currentTarget.value)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown") {
            event.preventDefault();
            listRef.current?.querySelector<HTMLButtonElement>(".workspace-diff-tree-row")?.focus();
          }
        }}
      />
      <div className="workspace-diff-tree-scroll">
        {nodes.length === 0 ? (
          <div className="workspace-diff-tree-empty">
            {t("workspaceReview.noMatchingFiles")}
          </div>
        ) : (
          <div className="workspace-diff-tree-list" ref={listRef} onKeyDown={handleListKeyDown}>
            {nodes.map((node) => (
              <GitChangeTreeNodeView
                key={node.id}
                node={node}
                depth={0}
                forceExpanded={forceExpanded}
                selectedPath={selectedPath}
                expandedPaths={expandedPaths}
                onSelectFile={onSelectFile}
                onTogglePath={onTogglePath}
              />
            ))}
          </div>
        )}
      </div>
    </aside>
  );
}

function GitChangeTreeNodeView({
  node,
  depth,
  forceExpanded,
  selectedPath,
  expandedPaths,
  onSelectFile,
  onTogglePath
}: {
  node: GitChangeTreeNode;
  depth: number;
  forceExpanded: boolean;
  selectedPath?: string;
  expandedPaths: Set<string>;
  onSelectFile: (path: string) => void;
  onTogglePath: (path: string) => void;
}): JSX.Element {
  const { formatNumber } = useI18n();
  // One indent step per level; the row's own inset lives in the stylesheet.
  const indentation = { "--workspace-diff-tree-depth": depth } as CSSProperties;
  if (node.kind === "directory") {
    const expanded = forceExpanded || expandedPaths.has(node.path);
    return (
      <div className="workspace-diff-tree-node">
        <button
          className="workspace-diff-tree-row directory"
          data-wuu-component="workspace-review-item"
          data-wuu-kind="directory"
          type="button"
          style={indentation}
          aria-expanded={expanded}
          onClick={() => onTogglePath(node.path)}
        >
          <ChevronRight className="workspace-diff-tree-chevron icon-sm" />
          {expanded ? <FolderOpen className="icon-sm" /> : <Folder className="icon-sm" />}
          <TruncatedText className="workspace-diff-tree-name" text={node.name} />
          <span className="workspace-diff-tree-count">{formatNumber(node.fileCount)}</span>
        </button>
        {expanded ? (
          <div className="workspace-diff-tree-children">
            {node.children.map((child) => (
              <GitChangeTreeNodeView
                key={child.id}
                node={child}
                depth={depth + 1}
                forceExpanded={forceExpanded}
                selectedPath={selectedPath}
                expandedPaths={expandedPaths}
                onSelectFile={onSelectFile}
                onTogglePath={onTogglePath}
              />
            ))}
          </div>
        ) : null}
      </div>
    );
  }

  const file = node.file;
  const selected = file?.path === selectedPath;
  return (
    <button
      className={`workspace-diff-tree-row file${selected ? " active" : ""}`}
      data-wuu-component="workspace-review-item"
      data-wuu-kind="file"
      data-wuu-active={selected}
      type="button"
      style={indentation}
      aria-pressed={selected}
      onClick={() => {
        if (file) {
          onSelectFile(file.path);
        }
      }}
    >
      <span className="workspace-diff-tree-spacer" />
      <FileText className="icon-sm" />
      <TruncatedText className="workspace-diff-tree-name" text={node.name} />
      {file ? <GitChangeFileStats file={file} /> : null}
    </button>
  );
}

function GitChangeFileStats({ file }: { file: GitChangeFile }): JSX.Element {
  const { t } = useI18n();
  return (
    <span className="workspace-diff-tree-stats">
      {file.binary ? (
        <span>{t("workspace.review.binary")}</span>
      ) : (
        <GitChangeCounts additions={file.additions} deletions={file.deletions} />
      )}
      <span className={`workspace-diff-file-status ${file.status}`} title={gitChangeStatusText(file.status)}>
        {gitChangeStatusLabel(file.status)}
      </span>
    </span>
  );
}
