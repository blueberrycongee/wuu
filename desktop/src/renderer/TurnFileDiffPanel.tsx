import { useEffect, useMemo, useState } from "react";
import { FileText, X } from "./WuuIcons";
import type { GitFileDiffResult, WorkspaceFileReadResult } from "../shared/protocol";
import { RichContent } from "./RichContent";
import { ToolDiffContent } from "./ToolDiffPreview";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import { translateCurrent, useI18n } from "./i18n";
import { TruncatedText } from "./TruncatedText";

type ArtifactView = "changes" | "reading" | "source" | "current";

function isMarkdownPath(path: string): boolean {
  return /\.mdx?$/i.test(path);
}

function workspaceRelativePath(path: string, cwd?: string): string | undefined {
  const normalizedPath = path.replace(/\\/g, "/");
  const normalizedRoot = cwd?.replace(/\\/g, "/").replace(/\/$/, "");
  if (normalizedRoot && normalizedPath.startsWith(`${normalizedRoot}/`)) {
    return normalizedPath.slice(normalizedRoot.length + 1);
  }
  if (normalizedPath.startsWith("/") || /^[a-z]:\//i.test(normalizedPath)) {
    return undefined;
  }
  return normalizedPath.replace(/^\.\//, "");
}

function normalizedSHA(value?: string): string | undefined {
  return value?.replace(/^sha256:/, "").toLowerCase();
}

function gitStatusLabel(result?: GitFileDiffResult): string | undefined {
  if (!result) return undefined;
  if (!result.is_repo) return translateCurrent("turnDiff.git.notRepository");
  switch (result.status) {
    case "ignored":
      return translateCurrent("turnDiff.git.ignored");
    case "untracked":
      return translateCurrent("turnDiff.git.untracked");
    case "added":
      return translateCurrent("turnDiff.git.added");
    case "modified":
      return translateCurrent("turnDiff.git.modified");
    case "deleted":
      return translateCurrent("turnDiff.git.deleted");
    default:
      return translateCurrent("turnDiff.git.clean");
  }
}

function actionLabel(selection: TurnFileDiffSelection): string {
  if (selection.action === "delete") return translateCurrent("turnDiff.action.delete");
  if (selection.action === "rename") return translateCurrent("turnDiff.action.rename");
  return selection.newFile || selection.action === "create"
    ? translateCurrent("turnDiff.action.create")
    : translateCurrent("turnDiff.action.modify");
}

function currentVersionLabel(
  selection: TurnFileDiffSelection,
  current?: WorkspaceFileReadResult,
  missing = false,
): string | undefined {
  if (missing) return translateCurrent("turnDiff.current.missing");
  if (!current) return undefined;
  const afterSHA = normalizedSHA(selection.afterSha);
  if (afterSHA && !current.truncated) {
    return current.sha256.toLowerCase() === afterSHA
      ? translateCurrent("turnDiff.current.matches")
      : translateCurrent("turnDiff.current.changed");
  }
  if (
    selection.snapshotText !== undefined &&
    current.text !== undefined &&
    !current.truncated
  ) {
    return current.text === selection.snapshotText
      ? translateCurrent("turnDiff.current.matches")
      : translateCurrent("turnDiff.current.changed");
  }
  return translateCurrent("turnDiff.current.workspaceVersion");
}

export function TurnFileDiffPanel({
  selection,
  onClose,
}: {
  selection?: TurnFileDiffSelection;
  onClose: () => void;
}): JSX.Element | null {
  const { locale, t, formatNumber } = useI18n();
  const [requestedView, setRequestedView] = useState<ArtifactView>("changes");
  const [currentFile, setCurrentFile] = useState<WorkspaceFileReadResult | undefined>();
  const [gitFile, setGitFile] = useState<GitFileDiffResult | undefined>();
  const [loadingCurrent, setLoadingCurrent] = useState(false);
  const [currentMissing, setCurrentMissing] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setCurrentFile(undefined);
    setGitFile(undefined);
    setCurrentMissing(false);
    if (!selection?.cwd) return () => { cancelled = true; };

    const path = workspaceRelativePath(selection.path, selection.cwd);
    if (!path) return () => { cancelled = true; };
    setLoadingCurrent(true);
    void window.wuu
      .readWorkspaceFile(path, selection.cwd)
      .then((result) => {
        if (!cancelled) setCurrentFile(result);
      })
      .catch(() => {
        if (!cancelled) setCurrentMissing(true);
      })
      .finally(() => {
        if (!cancelled) setLoadingCurrent(false);
      });
    void window.wuu
      .readGitFileDiff(path, selection.cwd)
      .then((result) => {
        if (!cancelled) setGitFile(result);
      })
      .catch(() => undefined);

    return () => { cancelled = true; };
  }, [selection]);

  const views = useMemo(() => {
    if (!selection) return [] as Array<{ id: ArtifactView; label: string }>;
    const next: Array<{ id: ArtifactView; label: string }> = [];
    if (selection.diff?.hunks.length) next.push({ id: "changes", label: t("turnDiff.view.changes") });
    if (selection.snapshotText !== undefined) {
      if (isMarkdownPath(selection.path)) next.push({ id: "reading", label: t("turnDiff.view.reading") });
      next.push({ id: "source", label: isMarkdownPath(selection.path) ? t("turnDiff.view.source") : t("turnDiff.view.turnContent") });
    }
    if (currentFile) next.push({ id: "current", label: t("turnDiff.view.current") });
    return next;
  }, [currentFile, selection, locale]);

  if (!selection) return null;

  const preferredView: ArtifactView = selection.newFile
    ? isMarkdownPath(selection.path) && selection.snapshotText !== undefined
      ? "reading"
      : selection.snapshotText !== undefined
        ? "source"
        : "current"
    : selection.diff?.hunks.length
      ? "changes"
      : selection.snapshotText !== undefined
        ? "source"
        : "current";
  const activeView = views.some((view) => view.id === requestedView)
    ? requestedView
    : views.some((view) => view.id === preferredView)
      ? preferredView
      : views[0]?.id;
  const versionLabel = currentVersionLabel(selection, currentFile, currentMissing);
  const deliveryLabel = gitStatusLabel(gitFile);

  return (
    <section className="turn-file-diff-panel" aria-label={t("turnDiff.panelLabel", { path: selection.path })}>
      <div className="turn-file-diff-header">
        <div className="turn-file-diff-heading">
          <span className="turn-file-diff-kicker">{t("turnDiff.kicker")}</span>
          <TruncatedText as="strong" className="turn-file-diff-path" text={selection.path} />
          <span className="turn-file-diff-meta">
            <span>{actionLabel(selection)}</span>
            {selection.additions > 0 ? (
              <span className="turn-edit-summary-add">+{formatNumber(selection.additions)}</span>
            ) : null}
            {selection.deletions > 0 ? (
              <span className="turn-edit-summary-delete">-{formatNumber(selection.deletions)}</span>
            ) : null}
            {selection.diff?.truncated ? <span>{t("turnDiff.truncated")}</span> : null}
          </span>
        </div>
        <button
          className="icon-button turn-file-diff-close"
          type="button"
          aria-label={t("turnDiff.closePanel")}
          onClick={onClose}
        >
          <X className="icon" />
        </button>
      </div>
      <div className="turn-artifact-toolbar">
        <div className="turn-artifact-view-tabs" role="tablist" aria-label={t("turnDiff.previewMode")}>
          {views.map((view) => (
            <button
              type="button"
              role="tab"
              aria-selected={activeView === view.id}
              className={activeView === view.id ? "selected" : ""}
              key={view.id}
              onClick={() => setRequestedView(view.id)}
            >
              {view.label}
            </button>
          ))}
        </div>
        <div className="turn-artifact-statuses">
          {versionLabel ? <span>{versionLabel}</span> : null}
          {deliveryLabel ? <span>{deliveryLabel}</span> : null}
        </div>
      </div>
      <div className={`turn-file-diff-body is-${activeView ?? "empty"}`}>
        {activeView === "changes" && selection.diff ? (
          <ToolDiffContent diff={selection.diff} />
        ) : activeView === "reading" && selection.snapshotText !== undefined ? (
          <div className="turn-artifact-markdown">
            <RichContent text={selection.snapshotText} cwd={selection.cwd} allowRawHtml />
          </div>
        ) : activeView === "source" && selection.snapshotText !== undefined ? (
          <pre className="turn-artifact-source">{selection.snapshotText}</pre>
        ) : activeView === "current" && currentFile?.renderable_url ? (
          <div className="turn-artifact-image">
            <img src={currentFile.renderable_url} alt={currentFile.path} />
          </div>
        ) : activeView === "current" && currentFile?.text !== undefined ? (
          isMarkdownPath(selection.path) ? (
            <div className="turn-artifact-markdown">
              <RichContent text={currentFile.text} cwd={selection.cwd} allowRawHtml />
            </div>
          ) : (
            <pre className="turn-artifact-source">{currentFile.text}</pre>
          )
        ) : currentFile?.binary ? (
          <div className="turn-artifact-empty">
            <FileText size={28} />
            <strong>{t("turnDiff.unsupportedPreview")}</strong>
            <span>{currentFile.path}</span>
          </div>
        ) : loadingCurrent ? (
          <div className="turn-artifact-empty">{t("turnDiff.readingCurrent")}</div>
        ) : (
          <div className="turn-artifact-empty">{t("turnDiff.noContent")}</div>
        )}
      </div>
    </section>
  );
}
