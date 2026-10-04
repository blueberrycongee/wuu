import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  SquarePen,
} from "./WuuIcons";
import {
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
  Suspense,
  lazy,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState
} from "react";
import type { GitChangeFile, GitChangesResult, GitFileDiffResult, GitStatusResult } from "../shared/protocol";
import { TruncatedText } from "./TruncatedText";
import { WorkspacePanelEmpty } from "./WorkspacePanelEmpty";
import { WorkspacePreviewBoundary } from "./WorkspacePreviewBoundary";
import {
  desktopApiErrorMessage,
  desktopApiSupportsGitReview,
  filterGitChangeFiles,
  gitChangeStatusLabel,
  gitChangeStatusText,
  gitDiffDisplayLines,
  gitPathName,
  groupGitChangeFiles,
  summarizeGitChangeFiles,
  workspaceRelativeGitPath,
  type GitChangeGroup
} from "./WorkspaceReviewHelpers";
import { useI18n } from "./i18n";

const WorkspaceMonacoDiffEditor = lazy(async () => ({
  default: (await import("./WorkspaceMonacoDiffEditor")).WorkspaceMonacoDiffEditor,
}));

const WORKSPACE_REVIEW_LIST_DEFAULT_WIDTH = 280;
const WORKSPACE_REVIEW_LIST_MIN_WIDTH = 220;
const WORKSPACE_REVIEW_LIST_MAX_WIDTH = 360;
const WORKSPACE_REVIEW_DIFF_MIN_WIDTH = 420;
const WORKSPACE_REVIEW_RESIZER_WIDTH = 8;
// Narrower than this, the diff and the change list cannot both stay readable
// side by side: the list and the diff take turns at full width.
const WORKSPACE_REVIEW_SPLIT_MIN_WIDTH =
  WORKSPACE_REVIEW_LIST_MIN_WIDTH + WORKSPACE_REVIEW_RESIZER_WIDTH + WORKSPACE_REVIEW_DIFF_MIN_WIDTH;
const WORKSPACE_REVIEW_LIST_STEP = 24;
const WORKSPACE_REVIEW_LIST_WIDTH_KEY = "wuu.desktop.reviewTreeWidth";

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

function initialWorkspaceReviewListWidth(): number {
  const stored = Number(window.localStorage.getItem(WORKSPACE_REVIEW_LIST_WIDTH_KEY));
  if (!stored || !Number.isFinite(stored)) {
    return WORKSPACE_REVIEW_LIST_DEFAULT_WIDTH;
  }
  return clamp(stored, WORKSPACE_REVIEW_LIST_MIN_WIDTH, WORKSPACE_REVIEW_LIST_MAX_WIDTH);
}

function clampWorkspaceReviewListWidth(width: number, panelWidth = Number.POSITIVE_INFINITY): number {
  const maxForPanel = Math.max(
    WORKSPACE_REVIEW_LIST_MIN_WIDTH,
    Math.min(WORKSPACE_REVIEW_LIST_MAX_WIDTH, panelWidth - WORKSPACE_REVIEW_DIFF_MIN_WIDTH)
  );
  return clamp(width, WORKSPACE_REVIEW_LIST_MIN_WIDTH, maxForPanel);
}

type ReviewDiff = { path: string; result?: GitFileDiffResult; error?: string };

/**
 * Review of the uncommitted changes. A wide panel keeps the change list
 * beside the diff; a narrow one opens on the list and shows one file's diff at
 * a time. The list follows the workspace's Git status, so it stays current
 * while an agent keeps editing, without losing the open file.
 */
export function WorkspaceReviewPanel({
  gitStatus,
  workspaceRoot,
  onOpenFile,
}: {
  gitStatus?: GitStatusResult;
  workspaceRoot?: string;
  onOpenFile?: (path: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const panelRef = useRef<HTMLDivElement | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const resizeRef = useRef<{ startX: number; startWidth: number } | null>(null);
  const [panelNode, setPanelNode] = useState<HTMLDivElement | null>(null);
  const [changes, setChanges] = useState<GitChangesResult | undefined>(undefined);
  const [listError, setListError] = useState<string | undefined>(undefined);
  const [selectedPath, setSelectedPath] = useState<string | undefined>(undefined);
  const [detailOpen, setDetailOpen] = useState(false);
  const [diff, setDiff] = useState<ReviewDiff | undefined>(undefined);
  const [query, setQuery] = useState("");
  const [listWidth, setListWidth] = useState(initialWorkspaceReviewListWidth);
  const [resizing, setResizing] = useState(false);
  // Unknown until the panel is measured, so a narrow panel never starts
  // loading a diff it will not show.
  const [split, setSplit] = useState<boolean | undefined>(undefined);
  // Focus that must land after a layout switch replaces the focused control.
  const pendingFocusRef = useRef<string | undefined>(undefined);

  const files = changes?.files ?? [];
  const groups = useMemo(() => groupGitChangeFiles(files), [files]);
  const visibleGroups = useMemo(() => groupGitChangeFiles(filterGitChangeFiles(files, query)), [files, query]);
  const order = useMemo(() => groups.flatMap((group) => group.files), [groups]);
  const visibleOrder = useMemo(() => visibleGroups.flatMap((group) => group.files), [visibleGroups]);
  const single = files.length === 1;
  const selectedFile = files.find((file) => file.path === selectedPath);
  // A wide panel always shows a diff; a narrow one only once a file is opened.
  const shownFile = selectedFile ?? (split || single ? order[0] : undefined);
  const showDetail = Boolean(shownFile) && (split || single || detailOpen);
  const shownPath = showDetail ? shownFile?.path : undefined;
  const showList = !single && split !== undefined && (split || !showDetail);
  const layout = single ? "single" : split === undefined ? undefined : split ? "split" : "stack";

  useEffect(() => {
    setChanges(undefined);
    setListError(undefined);
    setSelectedPath(undefined);
    setDetailOpen(false);
    setDiff(undefined);
    setQuery("");
  }, [workspaceRoot]);

  // Every new Git status, including the ones an agent's edits cause, reloads
  // the list in place: the open file and the scroll position stay.
  useEffect(() => {
    if (!desktopApiSupportsGitReview()) {
      setListError(t("workspaceReview.apiUnavailable"));
      return undefined;
    }
    let cancelled = false;
    void window.wuu
      .listGitChanges(workspaceRoot)
      .then((result) => {
        if (!cancelled) {
          setChanges(result);
          setListError(undefined);
        }
      })
      .catch((error) => {
        if (!cancelled) {
          setListError(desktopApiErrorMessage(error, t("workspaceReview.readChangesFailed")));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [workspaceRoot, gitStatus]);

  // The open file's diff reloads with the list. Until the reload lands the
  // previous diff stays, so a refresh never flashes a loading state.
  useEffect(() => {
    if (!shownPath) {
      return undefined;
    }
    setDiff((current) => (current?.path === shownPath ? current : { path: shownPath }));
    if (!desktopApiSupportsGitReview()) {
      setDiff({ path: shownPath, error: t("workspaceReview.apiUnavailable") });
      return undefined;
    }
    let cancelled = false;
    void window.wuu
      .readGitFileDiff(shownPath, workspaceRoot)
      .then((result) => {
        if (!cancelled) {
          setDiff({ path: shownPath, result });
        }
      })
      .catch((error) => {
        if (!cancelled) {
          setDiff({ path: shownPath, error: desktopApiErrorMessage(error, t("workspaceReview.readDiffFailed")) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [shownPath, workspaceRoot, changes]);

  useLayoutEffect(() => {
    const selector = pendingFocusRef.current;
    if (selector) {
      pendingFocusRef.current = undefined;
      panelRef.current?.querySelector<HTMLElement>(selector)?.focus();
    }
  });

  useEffect(() => {
    window.localStorage.setItem(WORKSPACE_REVIEW_LIST_WIDTH_KEY, String(listWidth));
  }, [listWidth]);

  // The layout follows the panel's own width, so a docked, resized or
  // full-window panel each gets the arrangement that fits it.
  useLayoutEffect(() => {
    if (!panelNode) {
      return undefined;
    }
    const measure = (): void => {
      setSplit(panelNode.getBoundingClientRect().width >= WORKSPACE_REVIEW_SPLIT_MIN_WIDTH);
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
    root.classList.toggle("resizing-review-split", resizing);
    if (!resizing) {
      return () => root.classList.remove("resizing-review-split");
    }

    function handlePointerMove(event: PointerEvent): void {
      const session = resizeRef.current;
      if (!session) {
        return;
      }
      const panelWidth = panelRef.current?.getBoundingClientRect().width;
      setListWidth(clampWorkspaceReviewListWidth(session.startWidth - (event.clientX - session.startX), panelWidth));
    }

    function handlePointerUp(): void {
      resizeRef.current = null;
      setResizing(false);
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
  }, [resizing]);

  function resizeListBy(delta: number): void {
    const panelWidth = panelRef.current?.getBoundingClientRect().width;
    setListWidth((current) => clampWorkspaceReviewListWidth(current + delta, panelWidth));
  }

  function startResize(event: ReactPointerEvent<HTMLDivElement>): void {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    resizeRef.current = { startX: event.clientX, startWidth: listWidth };
    setResizing(true);
  }

  function handleResizerKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    const delta =
      event.key === "ArrowLeft" ? WORKSPACE_REVIEW_LIST_STEP
        : event.key === "ArrowRight" ? -WORKSPACE_REVIEW_LIST_STEP
          : event.key === "Home" ? WORKSPACE_REVIEW_LIST_MAX_WIDTH
            : event.key === "End" ? -WORKSPACE_REVIEW_LIST_MAX_WIDTH
              : 0;
    if (delta) {
      event.preventDefault();
      resizeListBy(delta);
    }
  }

  function openFile(path: string): void {
    setSelectedPath(path);
    setDetailOpen(true);
    if (layout === "stack" && !showDetail) {
      // The pressed row leaves with the list; focus follows to the diff.
      pendingFocusRef.current = ".workspace-review-back";
    }
  }

  // Back to the list, with focus on the file just read.
  function closeDetail(): void {
    setDetailOpen(false);
    pendingFocusRef.current = `.workspace-review-row[data-wuu-path="${CSS.escape(shownFile?.path ?? "")}"]`;
  }

  if (!changes) {
    return listError
      ? <WorkspacePanelEmpty title={t("workspaceReview.readFailed")} description={listError} />
      : <WorkspacePanelEmpty title={t("workspaceReview.readingChanges")} />;
  }

  if (!changes.is_repo) {
    return <WorkspacePanelEmpty title={t("workspaceReview.notGitRepository")} />;
  }

  if (files.length === 0) {
    return <WorkspacePanelEmpty title={t("workspaceReview.clean")} />;
  }

  // Stepping follows the list the reader sees, filtered when a filter is set.
  const sequence = shownFile && visibleOrder.includes(shownFile) ? visibleOrder : order;
  const shownIndex = shownFile ? sequence.indexOf(shownFile) : -1;
  const previousFile = shownIndex > 0 ? sequence[shownIndex - 1] : undefined;
  const nextFile = shownIndex >= 0 ? sequence[shownIndex + 1] : undefined;
  const editablePath = shownFile && shownFile.status !== "deleted" && onOpenFile
    ? workspaceRelativeGitPath(shownFile.path, changes.root, workspaceRoot)
    : undefined;

  return (
    <div
      className={`workspace-review-panel${resizing ? " resizing-split" : ""}`}
      aria-label={t("workspaceReview.reviewChanges")}
      data-wuu-component="workspace-review"
      data-wuu-layout={layout}
      data-wuu-state={showDetail ? "detail" : "navigation"}
      ref={(node) => {
        panelRef.current = node;
        setPanelNode(node);
      }}
      style={{ "--workspace-review-list-width": `${listWidth}px` } as CSSProperties}
    >
      {showDetail && shownFile ? (
        <section
          className="workspace-review-diff-panel"
          data-wuu-component="workspace-review-content"
          aria-label={t("workspaceReview.codeDiffFor", { path: shownFile.path })}
        >
          <div className="workspace-diff-detail-header" data-wuu-component="workspace-review-content-header">
            {layout === "stack" ? (
              <button
                className="icon-button workspace-review-back"
                data-wuu-action="back"
                type="button"
                aria-label={t("workspaceReview.backToList")}
                title={t("workspaceReview.backToList")}
                onClick={closeDetail}
              >
                <ArrowLeft className="icon" />
              </button>
            ) : null}
            <div className="workspace-review-file-row">
              <WorkspaceReviewFileTitle file={shownFile} />
            </div>
            <div className="workspace-review-actions">
              {editablePath ? (
                <button
                  className="icon-button"
                  type="button"
                  data-wuu-action="open"
                  aria-label={t("workspaceReview.openInEditor")}
                  title={t("workspaceReview.openInEditor")}
                  onClick={() => onOpenFile?.(editablePath)}
                >
                  <SquarePen className="icon" />
                </button>
              ) : null}
              {single ? null : (
                <>
                  <button
                    className="icon-button"
                    type="button"
                    data-wuu-action="previous"
                    aria-label={t("workspaceReview.previousFile")}
                    title={t("workspaceReview.previousFile")}
                    disabled={!previousFile}
                    onClick={() => previousFile && openFile(previousFile.path)}
                  >
                    <ArrowUp className="icon" />
                  </button>
                  <button
                    className="icon-button"
                    type="button"
                    data-wuu-action="next"
                    aria-label={t("workspaceReview.nextFile")}
                    title={t("workspaceReview.nextFile")}
                    disabled={!nextFile}
                    onClick={() => nextFile && openFile(nextFile.path)}
                  >
                    <ArrowDown className="icon" />
                  </button>
                </>
              )}
            </div>
          </div>
          <WorkspaceDiffBody diff={diff?.path === shownFile.path ? diff : undefined} />
          {diff?.path === shownFile.path && diff.result?.truncated ? (
            <div className="workspace-diff-truncated">
              {t("workspaceReview.diffTruncated")}
            </div>
          ) : null}
        </section>
      ) : null}
      {showDetail && showList ? (
        <div
          className="workspace-review-resizer"
          role="separator"
          aria-label={t("workspaceReview.resizeDiffTree")}
          aria-orientation="vertical"
          aria-valuemin={WORKSPACE_REVIEW_LIST_MIN_WIDTH}
          aria-valuemax={WORKSPACE_REVIEW_LIST_MAX_WIDTH}
          aria-valuenow={Math.round(listWidth)}
          tabIndex={0}
          onPointerDown={startResize}
          onKeyDown={handleResizerKeyDown}
        />
      ) : null}
      {showList ? (
        <GitChangeList
          listRef={listRef}
          branch={gitStatus?.is_repo ? gitStatus.branch : undefined}
          files={files}
          groups={visibleGroups}
          query={query}
          activePath={showDetail ? shownFile?.path : selectedFile?.path}
          onQueryChange={setQuery}
          onOpenFile={openFile}
        />
      ) : null}
    </div>
  );
}

/**
 * The file a diff shows: its name first and whole, its folder (or former
 * path) as quieter context that gives way first, then status and counts.
 */
function WorkspaceReviewFileTitle({ file }: { file: GitChangeFile }): JSX.Element {
  const { t } = useI18n();
  const separator = file.path.lastIndexOf("/");
  const renamed = Boolean(file.old_path && file.old_path !== file.path);
  const context = renamed
    ? t("workspaceReview.renamedFrom", { path: file.old_path ?? "" })
    : separator > 0 ? file.path.slice(0, separator) : "";
  return (
    <>
      <span className="workspace-review-file">
        <TruncatedText as="strong" className="workspace-review-file-name" text={gitPathName(file.path)} />
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

function WorkspaceDiffBody({ diff }: { diff?: ReviewDiff }): JSX.Element {
  const { t } = useI18n();
  const fileDiff = diff?.result;
  if (diff?.error) return <div className="workspace-diff-error">{diff.error}</div>;
  if (!fileDiff) return <div className="workspace-diff-empty">{t("workspaceReview.readingDiff")}</div>;
  if (fileDiff.binary) {
    return <div className="workspace-diff-empty">{t("workspaceReview.binaryNoTextDiff")}</div>;
  }
  if (typeof fileDiff.original_text === "string" && typeof fileDiff.modified_text === "string") {
    return (
      <WorkspacePreviewBoundary resourceKey={fileDiff.path}>
        <Suspense fallback={<div className="workspace-diff-empty">{t("workspaceReview.readingDiff")}</div>}>
          <WorkspaceMonacoDiffEditor
            path={fileDiff.path}
            originalText={fileDiff.original_text}
            modifiedText={fileDiff.modified_text}
          />
        </Suspense>
      </WorkspacePreviewBoundary>
    );
  }
  if (!fileDiff.patch) {
    return <div className="workspace-diff-empty">{t("workspaceReview.noTextDiff")}</div>;
  }
  return (
    <div className="workspace-diff-code-scroll">
      <GitPatchLines patch={fileDiff.patch} label={t("workspaceReview.codeDiffFor", { path: fileDiff.path })} />
    </div>
  );
}

/** A unified Git patch with old and new line numbers. */
function GitPatchLines({ patch, label }: { patch: string; label: string }): JSX.Element {
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

function GitChangeList({
  listRef,
  branch,
  files,
  groups,
  query,
  activePath,
  onQueryChange,
  onOpenFile,
}: {
  listRef: RefObject<HTMLDivElement | null>;
  branch?: string;
  files: GitChangeFile[];
  groups: GitChangeGroup[];
  query: string;
  activePath?: string;
  onQueryChange: (value: string) => void;
  onOpenFile: (path: string) => void;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const totals = summarizeGitChangeFiles(files);

  // Arrows walk the rows; the filter hands focus to the first row.
  function handleListKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    const rows = Array.from(listRef.current?.querySelectorAll<HTMLButtonElement>(".workspace-review-row") ?? []);
    const index = rows.indexOf(document.activeElement as HTMLButtonElement);
    const target =
      event.key === "ArrowDown" ? rows[index + 1]
        : event.key === "ArrowUp" ? rows[index - 1]
          : event.key === "Home" ? rows[0]
            : event.key === "End" ? rows.at(-1)
              : undefined;
    if (target) {
      event.preventDefault();
      target.focus();
    }
  }

  return (
    <aside
      className="workspace-review-list"
      data-wuu-component="workspace-review-navigation"
      aria-label={t("workspaceReview.changeFileTree")}
    >
      <div className="workspace-review-list-header">
        <span className="workspace-review-list-summary">
          <strong>
            {t(files.length === 1 ? "environment.fileCountOne" : "environment.fileCount", {
              count: formatNumber(files.length),
            })}
          </strong>
          <GitChangeCounts additions={totals.additions} deletions={totals.deletions} />
        </span>
        {branch ? <TruncatedText className="workspace-review-branch" text={branch} /> : null}
      </div>
      <input
        className="workspace-review-search"
        data-wuu-component="workspace-review-search"
        type="search"
        value={query}
        placeholder={t("workspaceReview.filterFiles")}
        aria-label={t("workspaceReview.filterFiles")}
        onChange={(event) => onQueryChange(event.currentTarget.value)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown") {
            event.preventDefault();
            listRef.current?.querySelector<HTMLButtonElement>(".workspace-review-row")?.focus();
          }
        }}
      />
      <div className="workspace-review-list-scroll" ref={listRef} onKeyDown={handleListKeyDown}>
        {groups.length === 0 ? (
          <div className="workspace-review-list-empty">{t("workspaceReview.noMatchingFiles")}</div>
        ) : (
          groups.map((group) => (
            <div className="workspace-review-group" key={group.directory} role="group" aria-label={group.directory || undefined}>
              {group.directory ? (
                <TruncatedText className="workspace-review-group-label" text={group.directory} />
              ) : null}
              {group.files.map((file) => (
                <GitChangeRow
                  key={file.path}
                  file={file}
                  active={file.path === activePath}
                  onOpen={() => onOpenFile(file.path)}
                />
              ))}
            </div>
          ))
        )}
      </div>
    </aside>
  );
}

function GitChangeRow({
  file,
  active,
  onOpen,
}: {
  file: GitChangeFile;
  active: boolean;
  onOpen: () => void;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <button
      className={`workspace-review-row${active ? " active" : ""}`}
      data-wuu-component="workspace-review-item"
      data-wuu-active={active}
      data-wuu-path={file.path}
      type="button"
      aria-current={active ? "true" : undefined}
      onClick={onOpen}
    >
      <TruncatedText className="workspace-review-row-name" text={gitPathName(file.path)} />
      <span className="workspace-review-row-stats">
        {file.binary ? (
          <span>{t("workspace.review.binary")}</span>
        ) : (
          <GitChangeCounts additions={file.additions} deletions={file.deletions} />
        )}
        <span className={`workspace-review-status ${file.status}`} title={gitChangeStatusText(file.status)}>
          {gitChangeStatusLabel(file.status)}
        </span>
      </span>
    </button>
  );
}
