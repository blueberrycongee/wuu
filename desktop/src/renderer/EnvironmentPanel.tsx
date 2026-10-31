import { usePreviewContent } from "./PreviewContentContext";
import type { ApplicationConversationThreadV1 } from "../../../packages/plugin-sdk/src/ui-conversation";
import type { PreviewFileV1 } from "../../../packages/plugin-sdk/src/ui-preview-content";
import type { ApplicationEnvironmentV1 } from "../../../packages/plugin-sdk/src/ui-environment";
import {
  AlertCircle,
  Check,
  ChevronRight,
  FileDiff,
  FileText,
  FileX,
  GitBranch,
  Plus,
  Search,
  X
} from "./WuuIcons";
import { type FormEvent as ReactFormEvent, type ReactNode, type RefObject, useEffect, useState, useSyncExternalStore } from "react";
import type {
  GitStatusResult,
  Thread
} from "../shared/protocol";
import { desktopApiErrorMessage, formatBytes } from "./WorkspaceReviewHelpers";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { TruncatedText } from "./TruncatedText";
import { showErrorToast } from "./Toast";
import { WorktreeNotice } from "./WorktreeNotice";

export type EnvironmentPanelMenu = "branch" | "file" | null;
export type EnvironmentPanelMotionState = "open" | "closing";

export function EnvironmentPanel({
  panelRef,
  motionState,
  environment,
  activeMenu,
  running,
  pullRequestDisabledReason,
  onSetActiveMenu,
  onClose,
  onOpenWorktree,
  onOpenReview,
  onOpenCommit,
  onOpenPullRequest,
  rightPanelFilePath,
  onCloseFilePreview,
  pluginSections,
  thread,
}: {
  panelRef: RefObject<HTMLDivElement | null>;
  motionState: EnvironmentPanelMotionState;
  environment: ApplicationEnvironmentV1;
  activeMenu: EnvironmentPanelMenu;
  running: boolean;
  pullRequestDisabledReason: string;
  onSetActiveMenu: (menu: EnvironmentPanelMenu) => void;
  onClose: () => void;
  onOpenWorktree?: (path: string) => Promise<void>;
  onOpenReview: () => void;
  onOpenCommit: () => void;
  onOpenPullRequest: () => void;
  /**
   * Absolute path to a workspace file the right panel should preview. When
   * present together with `activeMenu === "file"`, the panel swaps its
   * default environment body for a file viewer that reads the file via
   * the selected public preview-content service.
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
  thread?: ApplicationConversationThreadV1;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const snapshot = useSyncExternalStore(
    listener => environment.subscribe(listener),
    () => environment.getSnapshot(),
    () => environment.getSnapshot(),
  );
  const gitStatus = snapshot.status;

  async function selectBranch(branch: string): Promise<void> {
    try {
      await environment.checkoutBranch(branch);
      onSetActiveMenu(null);
    } catch (error) {
      showErrorToast(error, t("git.checkoutFailed"));
    }
  }

  async function createBranch(branch: string): Promise<void> {
    await environment.createAndCheckoutBranch(branch);
    onSetActiveMenu(null);
  }
  if (activeMenu === "file" && rightPanelFilePath) {
    return (
      <EnvironmentFilePreview
        filePath={rightPanelFilePath}
        workspaceRoot={snapshot.root}
        onClose={onCloseFilePreview ?? onClose}
        panelRef={panelRef}
        motionState={motionState}
      />
    );
  }

  const diff = gitStatus?.diff ?? { files: 0, additions: 0, deletions: 0 };
  const hasChanges = Boolean(gitStatus?.is_repo && (gitStatus.dirty_count > 0 || diff.files > 0));
  // Outside a repository every Git row would repeat that it is unavailable;
  // say it once instead. Until the first status arrives the rows stay quiet.
  const notRepository = gitStatus?.is_repo === false;
  const branchLabel = gitStatus?.is_repo
    ? gitStatus.branch ?? "detached"
    : t("common.loadingEllipsis");
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
      <div className="environment-panel-header environment-panel-close-row">
        <div className="environment-panel-actions">
          <button className="icon-button" type="button" aria-label={t("environment.closeInfo")} onClick={onClose}>
            <X className="icon" aria-hidden="true" />
          </button>
        </div>
      </div>

      {pluginSections}

      <div className="environment-panel-body">
        {thread?.worktree ? <WorktreeNotice key={thread.worktree.path} thread={thread} /> : null}
        {notRepository ? (
          <p className="environment-panel-note">{t("environment.notGitRepository")}</p>
        ) : <>
        <div className="environment-row-group">
          <EnvironmentActionRow
            className="environment-change-row"
            disabled={!gitStatus?.is_repo}
            onClick={onOpenReview}
            icon={<FileDiff className="icon-lg" aria-hidden="true" />}
            label={t("environment.changes")}
            meta={
              hasChanges
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
            }
          />
          <EnvironmentActionRow
            className={activeMenu === "branch" ? "active" : undefined}
            disabled={!gitStatus?.is_repo}
            onClick={() => toggleMenu("branch")}
            icon={<GitBranch className="icon-lg" aria-hidden="true" />}
            label={branchLabel}
            trailing={gitStatus?.is_repo ? <ChevronRight className="icon-sm" aria-hidden="true" /> : null}
          />
        </div>
        {/* Commit and PR are usually asked of the agent, so they follow the
            status rows as quiet text actions instead of a group of their own. */}
        <div className="environment-git-actions">
          <button
            className="environment-text-action"
            type="button"
            disabled={!hasChanges || running || !snapshot.capabilities.commit}
            onClick={onOpenCommit}
          >
            {t("environment.commit")}
          </button>
          <Tooltip content={prDisabled ? pullRequestDisabledReason : undefined}>
            <button
              className="environment-text-action"
              type="button"
              disabled={prDisabled || running || (!gitStatus?.pr_url && !snapshot.capabilities.pullRequest)}
              onClick={onOpenPullRequest}
            >
              {t(gitStatus?.pr_url ? "environment.viewPR" : "environment.createPR")}
            </button>
          </Tooltip>
        </div>
        </>}
      </div>

      {activeMenu === "branch" && gitStatus?.is_repo ? (
        <EnvironmentBranchMenu
          gitStatus={gitStatus}
          canCheckout={snapshot.capabilities.checkout}
          canCreateBranch={snapshot.capabilities.createBranch}
          onSelectBranch={selectBranch}
          onOpenWorktree={onOpenWorktree}
          onCreateBranch={createBranch}
        />
      ) : null}
    </aside>
  );
}

function EnvironmentActionRow({
  className,
  icon,
  label,
  meta,
  trailing,
  disabled,
  onClick,
}: {
  className?: string;
  icon: ReactNode;
  label: ReactNode;
  meta?: ReactNode;
  trailing?: ReactNode;
  disabled?: boolean;
  onClick?: () => void;
}): JSX.Element {
  return (
    <button
      className={className ? `environment-row ${className}` : "environment-row"}
      type="button"
      disabled={disabled}
      onClick={onClick}
    >
      {icon}
      <strong>{label}</strong>
      <span className="environment-row-meta">{meta}</span>
      {trailing ? <span className="environment-row-trailing">{trailing}</span> : null}
    </button>
  );
}

function EnvironmentBranchMenu({
  gitStatus,
  canCheckout,
  canCreateBranch,
  onSelectBranch,
  onOpenWorktree,
  onCreateBranch
}: {
  gitStatus: GitStatusResult;
  canCheckout: boolean;
  canCreateBranch: boolean;
  onSelectBranch: (branch: string) => void;
  onOpenWorktree?: (path: string) => Promise<void>;
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
        <Search className="icon-sm" aria-hidden="true" />
        <input value={query} aria-label={t("environment.searchBranches")} placeholder={t("environment.searchBranches")} onChange={(event) => setQuery(event.target.value)} />
      </label>
      {gitStatus.dirty_count > 0 ? (
        <div className="environment-side-note">{t("environment.dirtyBranchNote")}</div>
      ) : null}
      <div className="environment-branch-list">
        {branches.length === 0 ? <div className="environment-empty">{t("environment.noMatchingBranches")}</div> : null}
        {branches.map((branch) => {
          const selected = branch === gitStatus.branch;
          const worktreePath = selected ? undefined : gitStatus.branch_worktrees?.find((worktree) => worktree.branch === branch)?.path;
          return (
            <Tooltip asChild content={worktreePath} key={branch}><button aria-label={worktreePath ? `${branch}\n${worktreePath}` : branch} role="menuitem" type="button" aria-current={selected || undefined}
              disabled={selected || submitting || (worktreePath ? !onOpenWorktree : !canCheckout)}
              onClick={async () => {
                setSubmitting(true);
                try {
                  if (worktreePath) await onOpenWorktree?.(worktreePath);
                  else await onSelectBranch(branch);
                } catch (error) {
                  showErrorToast(error, t("git.checkoutFailed"));
                } finally {
                  setSubmitting(false);
                }
              }}>
              <GitBranch className="icon" />
              <span className="environment-branch-name">
                <TruncatedText text={branch} />
                {worktreePath ? <span className="branch-worktree-action">{t("git.openExistingWorktree")}</span> : null}
              </span>
              {selected ? <Check className="icon" /> : null}
            </button></Tooltip>
          );
        })}
      </div>
      <form className="environment-create-branch" onSubmit={(event) => void submitNewBranch(event)}>
        <input value={newBranch} placeholder={t("environment.newBranchName")} onChange={(event) => setNewBranch(event.target.value)} />
        <button type="submit" disabled={!newBranch.trim() || submitting || !canCreateBranch}>
          <Plus className="icon" />
        </button>
      </form>
    </div>
  );
}

/**
 * Right-panel body that replaces the default environment panel when
 * `activeMenu === "file"` and a `rightPanelFilePath` is supplied. Reads the
 * file via the selected public preview-content service and renders loading / error /
 * binary / text-file states. The header close button falls back to
 * `onClose` when the caller does not provide a file-specific closer.
 */
function EnvironmentFilePreview({
  filePath,
  workspaceRoot,
  onClose,
  panelRef,
  motionState
}: {
  filePath: string;
  workspaceRoot?: string;
  onClose: () => void;
  panelRef: RefObject<HTMLDivElement | null>;
  motionState: EnvironmentPanelMotionState;
}): JSX.Element {
  const { locale, t } = useI18n();
  const { service } = usePreviewContent();
  const [file, setFile] = useState<PreviewFileV1 | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | undefined>(undefined);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(undefined);
    setFile(undefined);
    if (!workspaceRoot) {
      setError("session working directory is unavailable");
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    void service.readFile({ path: filePath, root: workspaceRoot }, { signal: controller.signal })
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
      controller.abort();
    };
  }, [filePath, workspaceRoot, locale, service]);

  let body: JSX.Element;
  if (loading) {
    body = (
      <div className="environment-panel-body">
        <div className="environment-row environment-file-row static">
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
        <div className="environment-row environment-file-row static">
          <FileText aria-hidden="true" className="icon-lg" />
          <strong>{t("environment.noContent")}</strong>
          <span>{filePath}</span>
        </div>
      </div>
    );
  } else if (file.binary) {
    body = (
      <div className="environment-panel-body">
        <div className="environment-row environment-file-row static">
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
