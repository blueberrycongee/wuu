import { hostSupports } from "./HostCapabilities";
import {
  AlertCircle,
  Check,
  ChevronRight,
  CornerDownRight,
  FileText,
  FileX,
  FolderPlus,
  Github,
  GitBranch,
  Plus,
  Search,
  X
} from "lucide-react";
import { type FormEvent as ReactFormEvent, type ReactNode, type RefObject, useEffect, useState } from "react";
import type {
  GitStatusResult,
  InitializeResult,
  WorkspaceFileReadResult
} from "../shared/protocol";
import { desktopApiErrorMessage, formatBytes } from "./WorkspaceReviewHelpers";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { showErrorToast } from "./Toast";

export type EnvironmentPanelMenu = "branch" | "file" | null;
export type EnvironmentPanelMotionState = "open" | "closing";

export function EnvironmentPanel({
  panelRef,
  motionState,
  gitStatus,
  activeMenu,
  running,
  pullRequestDisabledReason,
  onSetActiveMenu,
  onClose,
  onSelectBranch,
  onCreateBranch,
  onOpenReview,
  onOpenCommit,
  onOpenPullRequest,
  rightPanelFilePath,
  onCloseFilePreview,
  pluginSections,
}: {
  panelRef: RefObject<HTMLDivElement | null>;
  motionState: EnvironmentPanelMotionState;
  initialized: InitializeResult;
  gitStatus?: GitStatusResult;
  activeMenu: EnvironmentPanelMenu;
  running: boolean;
  pullRequestDisabledReason: string;
  onSetActiveMenu: (menu: EnvironmentPanelMenu) => void;
  onClose: () => void;
  onSelectBranch: (branch: string) => void;
  onCreateBranch: (branch: string) => Promise<void>;
  onOpenReview: () => void;
  onOpenCommit: () => void;
  onOpenPullRequest: () => void;
  /**
   * Absolute path to a workspace file the right panel should preview. When
   * present together with `activeMenu === "file"`, the panel swaps its
   * default environment body for a file viewer that reads the file via
   * `window.wuu.readWorkspaceFile`.
   */
  rightPanelFilePath?: string;
  /**
   * Invoked when the user closes the file preview from inside the panel.
   * Falls back to `onClose` (which dismisses the whole panel) when the
   * caller does not provide a file-specific closer.
   */
  onCloseFilePreview?: () => void;
  /** Host-mounted plugin summaries. Each contribution owns its own error boundary. */
  pluginSections?: ReactNode;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  if (activeMenu === "file" && rightPanelFilePath) {
    return (
      <EnvironmentFilePreview
        filePath={rightPanelFilePath}
        onClose={onCloseFilePreview ?? onClose}
        panelRef={panelRef}
        motionState={motionState}
      />
    );
  }

  const diff = gitStatus?.diff ?? { files: 0, additions: 0, deletions: 0 };
  const hasChanges = Boolean(gitStatus?.is_repo && (gitStatus.dirty_count > 0 || diff.files > 0));
  const branchLabel = gitStatus?.is_repo
    ? gitStatus.branch ?? "detached"
    : t("environment.notGitRepository");
  const prDisabled = Boolean(pullRequestDisabledReason && !gitStatus?.pr_url);

  function toggleMenu(menu: Exclude<EnvironmentPanelMenu, null>): void {
    onSetActiveMenu(activeMenu === menu ? null : menu);
  }

  return (
    <aside
      className={`environment-panel ${motionState}`}
      ref={panelRef}
      aria-label={t("environment.info")}
      aria-hidden={motionState === "closing" ? true : undefined}
      data-wuu-component="environment-panel"
      data-wuu-state={motionState}
    >
      <div className="environment-panel-header floating">
        <div className="environment-panel-actions">
          <button className="icon-button" type="button" aria-label={t("environment.closeInfo")} onClick={onClose}>
            <X className="icon" />
          </button>
        </div>
      </div>

      {pluginSections}

      <div className="environment-panel-body">
        <button
          className="environment-row environment-change-row"
          type="button"
          disabled={!gitStatus?.is_repo}
          onClick={onOpenReview}
        >
          <FolderPlus className="icon-lg" />
          <strong>{t("environment.changes")}</strong>
          <span className="environment-row-meta">
            {gitStatus?.is_repo
              ? hasChanges
                ? (
                  <>
                    {t(diff.files === 1 ? "environment.fileCountOne" : "environment.fileCount", {
                      count: formatNumber(diff.files),
                    })}
                    <span className="environment-diff">
                      <span className="additions">+{formatNumber(diff.additions)}</span>
                      <span className="deletions">-{formatNumber(diff.deletions)}</span>
                    </span>
                  </>
                )
                : null
              : t("environment.notGit")}
          </span>
          {gitStatus?.is_repo ? <ChevronRight className="icon" /> : null}
        </button>

        <button
          className={`environment-row${activeMenu === "branch" ? " active" : ""}`}
          type="button"
          disabled={!gitStatus?.is_repo || running}
          onClick={() => toggleMenu("branch")}
        >
          <GitBranch className="icon-lg" />
          <strong>{branchLabel}</strong>
          {gitStatus?.is_repo ? <ChevronRight className="icon" /> : null}
        </button>

        <button
          className="environment-row"
          type="button"
          disabled={!hasChanges || running || !hostSupports("commitGitChanges")}
          onClick={onOpenCommit}
        >
          <CornerDownRight className="icon-lg" />
          <strong>{t("environment.commit")}</strong>
          <span>{hasChanges ? t("environment.commitChanges") : ""}</span>
        </button>

        <Tooltip content={prDisabled ? pullRequestDisabledReason : undefined}>
          <button
            className="environment-row"
            type="button"
            disabled={prDisabled || running || (!gitStatus?.pr_url && !hostSupports("createPullRequest"))}
            onClick={onOpenPullRequest}
          >
            <Github className="icon-lg" />
            <strong>{t(gitStatus?.pr_url ? "environment.viewPR" : "environment.createPR")}</strong>
            <span>{gitStatus?.pr_url ? t("environment.existingPR") : prDisabled ? pullRequestDisabledReason : t("environment.pushAndCreatePR")}</span>
          </button>
        </Tooltip>

      </div>

      {activeMenu === "branch" && gitStatus?.is_repo ? (
        <EnvironmentBranchMenu
          gitStatus={gitStatus}
          onSelectBranch={onSelectBranch}
          onCreateBranch={onCreateBranch}
        />
      ) : null}
    </aside>
  );
}

function EnvironmentBranchMenu({
  gitStatus,
  onSelectBranch,
  onCreateBranch
}: {
  gitStatus: GitStatusResult;
  onSelectBranch: (branch: string) => void;
  onCreateBranch: (branch: string) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [newBranch, setNewBranch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const branches = (gitStatus.branches ?? []).filter((branch) =>
    normalizedQuery ? branch.toLocaleLowerCase().includes(normalizedQuery) : true
  );

  async function submitNewBranch(event: ReactFormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    const branch = newBranch.trim();
    if (!branch || submitting) {
      return;
    }
    setSubmitting(true);
    try {
      await onCreateBranch(branch);
      setNewBranch("");
    } catch (createError) {
      showErrorToast(createError, t("environment.createBranchFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="environment-side-menu branch" role="menu">
      <label className="menu-search environment-search">
        <Search className="icon" />
        <input value={query} placeholder={t("environment.searchBranches")} onChange={(event) => setQuery(event.target.value)} />
      </label>
      {gitStatus.dirty_count > 0 ? (
        <div className="environment-side-note">{t("environment.dirtyBranchNote")}</div>
      ) : null}
      <div className="environment-branch-list">
        {branches.length === 0 ? <div className="environment-empty">{t("environment.noMatchingBranches")}</div> : null}
        {branches.map((branch) => {
          const selected = branch === gitStatus.branch;
          return (
            <button key={branch} role="menuitem" type="button" disabled={selected || !hostSupports("checkoutGitBranch")} onClick={() => onSelectBranch(branch)}>
              <GitBranch className="icon" />
              <span>{branch}</span>
              {selected ? <Check className="icon" /> : null}
            </button>
          );
        })}
      </div>
      <form className="environment-create-branch" onSubmit={(event) => void submitNewBranch(event)}>
        <input value={newBranch} placeholder={t("environment.newBranchName")} onChange={(event) => setNewBranch(event.target.value)} />
        <button type="submit" disabled={!newBranch.trim() || submitting || !hostSupports("createCheckoutGitBranch")}>
          <Plus className="icon" />
        </button>
      </form>
    </div>
  );
}

/**
 * Right-panel body that replaces the default environment panel when
 * `activeMenu === "file"` and a `rightPanelFilePath` is supplied. Reads the
 * file via `window.wuu.readWorkspaceFile` and renders loading / error /
 * binary / text-file states. The header close button falls back to
 * `onClose` when the caller does not provide a file-specific closer.
 */
function EnvironmentFilePreview({
  filePath,
  onClose,
  panelRef,
  motionState
}: {
  filePath: string;
  onClose: () => void;
  panelRef: RefObject<HTMLDivElement | null>;
  motionState: EnvironmentPanelMotionState;
}): JSX.Element {
  const { locale, t } = useI18n();
  const [file, setFile] = useState<WorkspaceFileReadResult | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | undefined>(undefined);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(undefined);
    setFile(undefined);
    void window.wuu
      .readWorkspaceFile(filePath)
      .then((result) => {
        if (!cancelled) {
          setFile(result);
        }
      })
      .catch((e) => {
        if (!cancelled) {
          setError(desktopApiErrorMessage(e, t("environment.openFileFailed")));
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
  }, [filePath, locale]);

  let body: JSX.Element;
  if (loading) {
    body = (
      <div className="environment-panel-body">
        <div className="environment-row environment-file-row">
          <FileText aria-hidden="true" className="icon-lg" />
          <strong>{t("environment.opening")}</strong>
          <span>{filePath}</span>
        </div>
      </div>
    );
  } else if (error) {
    body = (
      <div className="environment-panel-body">
        <div className="environment-row environment-file-row">
          <AlertCircle aria-hidden="true" className="icon-lg" />
          <strong>{t("environment.openFailed")}</strong>
          <span>{error}</span>
          <span className="environment-row-meta">{filePath}</span>
        </div>
      </div>
    );
  } else if (!file) {
    body = (
      <div className="environment-panel-body">
        <div className="environment-row environment-file-row">
          <FileText aria-hidden="true" className="icon-lg" />
          <strong>{t("environment.noContent")}</strong>
          <span>{filePath}</span>
        </div>
      </div>
    );
  } else if (file.binary) {
    body = (
      <div className="environment-panel-body">
        <div className="environment-row environment-file-row">
          <FileX aria-hidden="true" className="icon-lg" />
          <strong>{t("environment.cannotPreview")}</strong>
          <span>{t("environment.binaryFile", { path: file.path })}</span>
        </div>
      </div>
    );
  } else {
    body = (
      <article className="workspace-file-preview">
        <header className="workspace-file-preview-header">
          <div>
            <strong>{file.path}</strong>
            <span>
              {formatBytes(file.size_bytes)}
              {file.truncated ? t("environment.truncated512") : ""}
            </span>
          </div>
          <button
            type="button"
            className="icon-button"
            onClick={onClose}
            aria-label={t("environment.backToInfo")}
            title={t("common.back")}
          >
            <ChevronRight aria-hidden="true" className="icon" />
          </button>
        </header>
        <div className="workspace-file-code-scroll">
          <pre className="workspace-file-code">
            <code>{file.text}</code>
          </pre>
        </div>
      </article>
    );
  }

  return (
    <aside
      ref={panelRef}
      className={`environment-panel ${motionState}`}
      aria-label={t("environment.filePreview")}
      aria-hidden={motionState === "closing" ? true : undefined}
      data-wuu-component="environment-panel"
      data-wuu-state={motionState}
    >
      <div className="environment-panel-header">
        <h2>{t("environment.file")}</h2>
        <div className="environment-panel-actions">
          <button
            className="icon-button"
            type="button"
            aria-label={t("environment.backToInfo")}
            title={t("common.back")}
            onClick={onClose}
          >
            <X aria-hidden="true" className="icon" />
          </button>
        </div>
      </div>
      {body}
    </aside>
  );
}
