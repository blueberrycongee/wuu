import {
  AlertCircle,
  Check,
  ChevronRight,
  FileText,
  Folder,
  FolderOpen,
  FolderX,
  GitBranch,
  RefreshCw,
  Search
} from "lucide-react";
import {
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  Suspense,
  lazy,
  useEffect,
  useMemo,
  useRef,
  useState
} from "react";
import type { GitChangeFile, GitChangesResult, GitFileDiffResult, GitStatusResult, RuntimeContext } from "../shared/protocol";
import { formatWorkspaceRoot } from "./WorkspaceFiles";
import {
  buildGitChangeTree,
  desktopApiErrorMessage,
  desktopApiSupportsGitReview,
  expandedGitChangeTreePathsForSelection,
  filterGitChangeFiles,
  gitChangeFilePathLabel,
  gitChangeStatusDescription,
  gitChangeStatusLabel,
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

export function WorkspaceReviewPanel({
  gitStatus,
  workspaceRoot,
}: {
  gitStatus?: GitStatusResult;
  workspaceRoot?: string;
}): JSX.Element {
  const { locale, t } = useI18n();
  const panelRef = useRef<HTMLDivElement>(null);
  const splitResizeRef = useRef<{ startX: number; startTreeWidth: number } | null>(null);
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
  const files = changes?.files ?? [];
  const filteredFiles = useMemo(() => filterGitChangeFiles(files, treeQuery), [files, treeQuery]);
  const treeNodes = useMemo(() => buildGitChangeTree(filteredFiles), [filteredFiles]);
  const selectedFile = files.find((file) => file.path === selectedPath);
  const singleFileReview = Boolean(selectedFile && files.length === 1);
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

  if (loadingChanges && !changes) {
    return (
      <div className="workspace-main-empty">
        <GitBranch size={36} />
        <strong>{t("workspaceReview.readingChanges")}</strong>
        <span>{t("workspaceReview.checkingLocalDiff")}</span>
      </div>
    );
  }

  if (error && !changes) {
    return (
      <div className="workspace-main-empty">
        <AlertCircle size={36} />
        <strong>{t("workspaceReview.readFailed")}</strong>
        <span>{error}</span>
      </div>
    );
  }

  if (changes && !changes.is_repo) {
    return (
      <div className="workspace-main-empty">
        <FolderX size={36} />
        <strong>{t("workspaceReview.notGitRepository")}</strong>
        <span>{t("workspaceReview.noGitChanges")}</span>
      </div>
    );
  }

  if (changes && files.length === 0) {
    return (
      <div className="workspace-main-empty">
        <Check size={36} />
        <strong>{t("workspaceReview.clean")}</strong>
        <span>{t("workspaceReview.noReviewDiff")}</span>
      </div>
    );
  }

  return (
    <div
      className={`workspace-review-panel${selectedFile ? " has-diff" : ""}${
        singleFileReview ? " single-file" : ""
      }${
        resizingSplit ? " resizing-split" : ""
      }`}
      aria-label={t("workspaceReview.reviewChanges")}
      data-wuu-component="workspace-review"
      data-wuu-state={selectedFile ? "detail" : "navigation"}
      ref={panelRef}
      style={panelStyle}
    >
      {selectedFile ? (
        <WorkspaceReviewDiffPeekPanel
          file={selectedFile}
          fileDiff={fileDiff}
          loading={loadingDiff}
          error={error}
          branch={gitStatus?.branch}
        />
      ) : null}
      {selectedFile && !singleFileReview ? (
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
      {!singleFileReview ? (
        <div className="workspace-review-tree-pane">
          <GitChangeTreePanel
            files={filteredFiles}
            nodes={treeNodes}
            selectedPath={selectedPath}
            expandedPaths={expandedPaths}
            query={treeQuery}
            onQueryChange={setTreeQuery}
            onSelectFile={setSelectedPath}
            onTogglePath={toggleTreePath}
          />
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
  branch
}: {
  file: GitChangeFile;
  fileDiff?: GitFileDiffResult;
  loading: boolean;
  error?: string;
  branch?: string;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <section
      className="workspace-review-diff-panel workspace-diff-detail"
      data-wuu-component="workspace-review-content"
      aria-label={t("workspaceReview.codeDiffFor", { path: file.path })}
    >
      <div className="workspace-diff-detail-header" data-wuu-component="workspace-review-content-header">
        <div>
          <strong>{gitChangeFilePathLabel(file)}</strong>
          <WorkspaceDiffFileMeta file={file} prefix={branch ?? t("workspaceReview.currentBranch")} />
        </div>
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

export function WorkspaceDiffReview({
  activeContext,
  gitStatus
}: {
  activeContext?: RuntimeContext;
  gitStatus?: GitStatusResult;
}): JSX.Element {
  const { locale, t, formatNumber } = useI18n();
  const [changes, setChanges] = useState<GitChangesResult | undefined>(undefined);
  const [selectedPath, setSelectedPath] = useState<string | undefined>(undefined);
  const [fileDiff, setFileDiff] = useState<GitFileDiffResult | undefined>(undefined);
  const [loadingChanges, setLoadingChanges] = useState(false);
  const [loadingDiff, setLoadingDiff] = useState(false);
  const [error, setError] = useState<string | undefined>(undefined);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [treeQuery, setTreeQuery] = useState("");
  const [expandedPaths, setExpandedPaths] = useState<Set<string>>(() => new Set());
  const workspaceRoot = activeContext?.cwd;
  const files = changes?.files ?? [];
  const totals = useMemo(() => summarizeGitChangeFiles(files), [files]);
  const filteredFiles = useMemo(() => filterGitChangeFiles(files, treeQuery), [files, treeQuery]);
  const treeNodes = useMemo(() => buildGitChangeTree(filteredFiles), [filteredFiles]);
  const selectedFile = files.find((file) => file.path === selectedPath);
  const branchLabel = gitStatus?.is_repo
    ? gitStatus.branch ?? "detached"
    : t("environment.notGitRepository");
  const upstreamLabel = gitStatus?.upstream;

  useEffect(() => {
    if (!workspaceRoot) {
      setChanges(undefined);
      setSelectedPath(undefined);
      setFileDiff(undefined);
      setError(undefined);
      setLoadingChanges(false);
      return;
    }

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
      .listGitChanges()
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
  }, [workspaceRoot, refreshVersion, locale]);

  useEffect(() => {
    if (!selectedPath) {
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
    if (!workspaceRoot || !selectedPath) {
      setFileDiff(undefined);
      setLoadingDiff(false);
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
      .readGitFileDiff(selectedPath)
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
  }, [workspaceRoot, selectedPath, refreshVersion, locale]);

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

  if (!activeContext) {
    return (
      <div className="workspace-main-empty">
        <FolderX size={36} />
        <strong>{t("workspaceReview.noProject")}</strong>
        <span>{t("workspaceReview.openProjectFirst")}</span>
      </div>
    );
  }

  if (loadingChanges && !changes) {
    return (
      <div className="workspace-main-empty">
        <GitBranch size={36} />
        <strong>{t("workspaceReview.readingChanges")}</strong>
        <span>{formatWorkspaceRoot(workspaceRoot ?? "")}</span>
      </div>
    );
  }

  if (error && !changes) {
    return (
      <div className="workspace-main-empty">
        <AlertCircle size={36} />
        <strong>{t("workspaceReview.readFailed")}</strong>
        <span>{error}</span>
      </div>
    );
  }

  if (changes && !changes.is_repo) {
    return (
      <div className="workspace-main-empty">
        <FolderX size={36} />
        <strong>{t("workspaceReview.notGitRepository")}</strong>
        <span>{t("workspaceReview.noGitChanges")}</span>
      </div>
    );
  }

  if (changes && files.length === 0) {
    return (
      <div className="workspace-main-empty">
        <Check size={36} />
        <strong>{t("workspaceReview.clean")}</strong>
        <span>{t("workspaceReview.noUncommittedDiff")}</span>
        <button type="button" onClick={() => setRefreshVersion((version) => version + 1)}>
          {t("common.refresh")}
        </button>
      </div>
    );
  }

  return (
    <article className="workspace-diff-review">
      <header className="workspace-diff-header">
        <div className="workspace-diff-title">
          <strong>{t("workspaceReview.review")}</strong>
          <span>
            {branchLabel}
            {upstreamLabel ? (
              <>
                <span className="workspace-diff-branch-arrow">-&gt;</span>
                {upstreamLabel}
              </>
            ) : null}
          </span>
        </div>
        <div className="workspace-diff-summary">
          <span>{t(files.length === 1 ? "environment.fileCountOne" : "environment.fileCount", { count: formatNumber(files.length) })}</span>
          <span className="additions">+{formatNumber(totals.additions)}</span>
          <span className="deletions">-{formatNumber(totals.deletions)}</span>
          <button
            className="icon-button"
            type="button"
            aria-label={t("workspaceReview.refreshChanges")}
            title={t("workspaceReview.refreshChanges")}
            disabled={loadingChanges || loadingDiff}
            onClick={() => setRefreshVersion((version) => version + 1)}
          >
            <RefreshCw className="icon" />
          </button>
        </div>
      </header>
      <div className="workspace-diff-content">
        <section className="workspace-diff-detail">
          <div className="workspace-diff-detail-header">
            <div>
              <strong>{selectedFile ? gitChangeFilePathLabel(selectedFile) : t("workspaceReview.selectFile")}</strong>
              {selectedFile ? (
                <WorkspaceDiffFileMeta file={selectedFile} />
              ) : (
                <span>{t("workspaceReview.selectFromLeft")}</span>
              )}
            </div>
          </div>
          <WorkspaceDiffBody fileDiff={fileDiff} loading={loadingDiff} error={error} />
          {fileDiff?.truncated ? <div className="workspace-diff-truncated">{t("workspaceReview.diffTruncated")}</div> : null}
        </section>
        <GitChangeTreePanel
          files={filteredFiles}
          nodes={treeNodes}
          selectedPath={selectedPath}
          expandedPaths={expandedPaths}
          query={treeQuery}
          onQueryChange={setTreeQuery}
          onSelectFile={setSelectedPath}
          onTogglePath={toggleTreePath}
        />
      </div>
    </article>
  );
}

function WorkspaceDiffFileMeta({
  file,
  prefix,
}: {
  file: GitChangeFile;
  prefix?: string;
}): JSX.Element {
  const { formatNumber } = useI18n();
  return (
    <span className="workspace-diff-file-meta">
      {prefix ? <span>{prefix}</span> : null}
      <span>{gitChangeStatusDescription(file)}</span>
      {file.binary ? null : (
        <>
          <span className="additions">+{formatNumber(file.additions)}</span>
          <span className="deletions">-{formatNumber(file.deletions)}</span>
        </>
      )}
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
  const diffLines = useMemo(
    () => (fileDiff?.patch ? gitDiffDisplayLines(fileDiff.patch) : []),
    [fileDiff?.patch],
  );
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
      <pre className="workspace-diff-code" aria-label={t("workspaceReview.codeDiffFor", { path: fileDiff.path })}>
        {diffLines.map((line, index) => (
          <span className={`workspace-diff-line ${line.kind}`} key={`${index}:${line.content.slice(0, 24)}`}>
            <span className="workspace-diff-line-number">{line.oldLine ?? ""}</span>
            <span className="workspace-diff-line-number">{line.newLine ?? ""}</span>
            <span className="workspace-diff-line-code">{line.content || " "}</span>
          </span>
        ))}
      </pre>
    </div>
  );
}

function GitChangeTreePanel({
  files,
  nodes,
  selectedPath,
  expandedPaths,
  query,
  onQueryChange,
  onSelectFile,
  onTogglePath
}: {
  files: GitChangeFile[];
  nodes: GitChangeTreeNode[];
  selectedPath?: string;
  expandedPaths: Set<string>;
  query: string;
  onQueryChange: (value: string) => void;
  onSelectFile: (path: string) => void;
  onTogglePath: (path: string) => void;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const forceExpanded = query.trim().length > 0;
  const totals = summarizeGitChangeFiles(files);
  return (
    <aside
      className="workspace-diff-tree"
      data-wuu-component="workspace-review-navigation"
      aria-label={t("workspaceReview.changeFileTree")}
    >
      <div className="workspace-diff-tree-header">
        <div>
          <strong>{t("workspaceReview.files")}</strong>
          <span>
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
            {files.length > 0 ? (
              <>
                {" "}
                <span className="additions">+{formatNumber(totals.additions)}</span>{" "}
                <span className="deletions">-{formatNumber(totals.deletions)}</span>
              </>
            ) : null}
          </span>
        </div>
      </div>
      <label className="menu-search workspace-diff-search" data-wuu-component="workspace-review-search">
        <Search className="icon-sm" aria-hidden="true" />
        <input
          value={query}
          placeholder={t("workspaceReview.filterFiles")}
          onChange={(event) => onQueryChange(event.currentTarget.value)}
        />
      </label>
      <div className="workspace-diff-tree-scroll">
        {nodes.length === 0 ? (
          <div className="workspace-diff-tree-empty">
            {t("workspaceReview.noMatchingFiles")}
          </div>
        ) : (
          <div className="workspace-diff-tree-list">
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
  // Per-level indent (12px) matches the file tree's visual density so the
  // two trees read as the same component family. Root gets a 10px base
  // so the chevron has breathing room from the tree's left edge.
  const indentation = { paddingLeft: `${10 + depth * 12}px` } as CSSProperties;
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
          <ChevronRight className="workspace-diff-tree-chevron icon" />
          {expanded ? <FolderOpen className="icon" /> : <Folder className="icon" />}
          <span className="workspace-diff-tree-name">{node.name}</span>
          <span className="workspace-diff-tree-count">{node.fileCount}</span>
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
      <FileText className="icon" />
      <span className="workspace-diff-tree-name">{node.name}</span>
      {file ? <GitChangeFileStats file={file} /> : null}
    </button>
  );
}

function GitChangeFileStats({ file }: { file: GitChangeFile }): JSX.Element {
  const { t, formatNumber } = useI18n();
  return (
    <span className="workspace-diff-tree-stats">
      <span className={`workspace-diff-file-status ${file.status}`}>{gitChangeStatusLabel(file.status)}</span>
      {file.binary ? (
        <span className="workspace-diff-tree-binary">{t("workspace.review.binary")}</span>
      ) : (
        <>
          <span className="additions">+{formatNumber(file.additions)}</span>
          <span className="deletions">-{formatNumber(file.deletions)}</span>
        </>
      )}
    </span>
  );
}
