import { Loader2, Sparkles } from "./WuuIcons";
import {
  type FormEvent as ReactFormEvent,
  useState,
} from "react";
import type { GitCommitResult, GitPullRequestResult, GitStatusResult } from "../shared/protocol";
import { humanizeBranchTitle } from "./RuntimeHelpers";
import { Modal } from "./Modal";
import { toastErrorMessage } from "./Toast";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";

export function CommitChangesDialog({
  gitStatus,
  branch,
  onCancel,
  onCommit,
  onGenerateMessage,
}: {
  gitStatus?: GitStatusResult;
  branch?: string;
  onCancel: () => void;
  onCommit: (params: { message: string; includeUnstaged: boolean }) => Promise<GitCommitResult>;
  onGenerateMessage: (params: { includeUnstaged: boolean }) => Promise<string>;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const [message, setMessage] = useState("");
  const [includeUnstaged, setIncludeUnstaged] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [error, setError] = useState("");
  const diff = gitStatus?.diff ?? { files: 0, additions: 0, deletions: 0 };
  const staged = gitStatus?.staged_diff ?? { files: 0, additions: 0, deletions: 0 };
  const hasChanges = Boolean(gitStatus?.is_repo && (gitStatus.dirty_count > 0 || diff.files > 0 || staged.files > 0));
  const busy = submitting || generating;
  const generateLabel = generating ? t("git.commit.generating") : t("git.commit.generate");

  async function submit(event: ReactFormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (!hasChanges || busy || !message.trim()) {
      return;
    }
    setSubmitting(true);
    setError("");
    try {
      await onCommit({ message, includeUnstaged });
      onCancel();
    } catch (commitError) {
      setError(t("git.commit.failed", { reason: toastErrorMessage(commitError, t("common.unknown")) }));
    } finally {
      setSubmitting(false);
    }
  }

  // Explicit AI generation: fills the input for the user to confirm or edit
  // before committing — nothing is committed from this path.
  async function generate(): Promise<void> {
    if (!hasChanges || busy) {
      return;
    }
    setGenerating(true);
    setError("");
    try {
      setMessage(await onGenerateMessage({ includeUnstaged }));
    } catch (generateError) {
      setError(t("git.commit.generateFailed", { reason: toastErrorMessage(generateError, t("common.unknown")) }));
    } finally {
      setGenerating(false);
    }
  }

  return (
    <Modal
      ariaLabel={t("git.commit.title")}
      title={t("git.commit.title")}
      onClose={onCancel}
      showCloseButton={false}
      panelClassName="git-dialog"
      asForm
      onSubmit={(event) => void submit(event)}
      footer={
        <>
          <button className="settings-button settings-button-ghost" type="button" onClick={onCancel}>
            {t("common.cancel")}
          </button>
          <button
            className="settings-button settings-button-primary"
            type="submit"
            disabled={!hasChanges || busy || !message.trim()}
          >
            {submitting ? t("git.commit.submitting") : t("git.commit.submit")}
          </button>
        </>
      }
    >
      <dl className="git-dialog-summary">
        <dt>{t("git.branch")}</dt>
        <dd>{branch ?? t("common.unknown")}</dd>
        <dt>{t("git.changes")}</dt>
        <dd>
          {t(diff.files === 1 ? "git.fileCountOne" : "git.fileCount", { count: formatNumber(diff.files) })}{" "}
          <span className="git-dialog-counts">
            <span className="additions">+{formatNumber(diff.additions)}</span>{" "}
            <span className="deletions">−{formatNumber(diff.deletions)}</span>
          </span>
        </dd>
      </dl>
      <label className="git-dialog-option">
        <input
          type="checkbox"
          checked={includeUnstaged}
          onChange={(event) => setIncludeUnstaged(event.currentTarget.checked)}
        />
        <span>{t("git.commit.includeUnstaged")}</span>
      </label>
      <label className="git-dialog-field">
        <span>{t("git.commit.message")}</span>
        <span className="git-dialog-field-inline">
          <input
            className="settings-input"
            value={message}
            placeholder={t("git.commit.messagePlaceholder")}
            onChange={(event) => setMessage(event.target.value)}
          />
          <Tooltip content={generateLabel}>
            <button
              aria-label={generateLabel}
              className="settings-button settings-icon-button"
              disabled={!hasChanges || busy}
              onClick={() => void generate()}
              type="button"
            >
              {generating ? (
                <Loader2 className="icon settings-spin" aria-hidden="true" />
              ) : (
                <Sparkles className="icon" aria-hidden="true" />
              )}
            </button>
          </Tooltip>
        </span>
      </label>
      {error ? <p className="environment-dialog-error" role="alert">{error}</p> : null}
    </Modal>
  );
}

export function PullRequestDialog({
  gitStatus,
  disabledReason,
  onCancel,
  onCreate,
}: {
  gitStatus?: GitStatusResult;
  disabledReason: string;
  onCancel: () => void;
  onCreate: (params: { title: string; body: string; draft: boolean }) => Promise<GitPullRequestResult>;
}): JSX.Element {
  const { t } = useI18n();
  const [title, setTitle] = useState(() => humanizeBranchTitle(gitStatus?.branch ?? ""));
  const [body, setBody] = useState("");
  const [draft, setDraft] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<GitPullRequestResult | undefined>(undefined);
  const existingURL = gitStatus?.pr_url ?? result?.url;
  const blocked = Boolean(disabledReason && !existingURL);

  async function submit(event: ReactFormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (blocked || submitting) {
      return;
    }
    if (existingURL) {
      window.open(existingURL, "_blank", "noopener,noreferrer");
      return;
    }
    setSubmitting(true);
    setError("");
    try {
      const created = await onCreate({ title, body, draft });
      setResult(created);
    } catch (createError) {
      setError(t("git.pr.createFailed", { reason: toastErrorMessage(createError, t("common.unknown")) }));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal
      ariaLabel={existingURL ? t("git.pr.title") : t("git.pr.createTitle")}
      title={existingURL ? t("git.pr.title") : t("git.pr.createTitle")}
      onClose={onCancel}
      showCloseButton={false}
      panelClassName="git-dialog"
      asForm
      onSubmit={(event) => void submit(event)}
      footer={
        <>
          <button className="settings-button settings-button-ghost" type="button" onClick={onCancel}>
            {existingURL ? t("common.close") : t("common.cancel")}
          </button>
          <button
            className="settings-button settings-button-primary"
            type="submit"
            disabled={blocked || submitting}
          >
            {existingURL ? t("common.open") : submitting ? t("git.pr.submitting") : t("git.pr.submit")}
          </button>
        </>
      }
    >
      {blocked ? <p className="environment-dialog-error" role="alert">{disabledReason}</p> : null}
      {existingURL ? (
        <p className="git-dialog-result">{result && !result.already_exists ? t("git.pr.ready") : t("git.pr.exists")}</p>
      ) : (
        <>
          <label className="git-dialog-field">
            <span>{t("git.pr.fieldTitle")}</span>
            <input
              className="settings-input"
              value={title}
              placeholder={t("git.pr.titlePlaceholder")}
              onChange={(event) => setTitle(event.target.value)}
            />
          </label>
          <label className="git-dialog-field">
            <span>{t("git.pr.description")}</span>
            <textarea
              className="settings-input"
              value={body}
              placeholder={t("git.pr.descriptionPlaceholder")}
              onChange={(event) => setBody(event.target.value)}
            />
          </label>
          <label className="git-dialog-option">
            <input
              type="checkbox"
              checked={draft}
              onChange={(event) => setDraft(event.currentTarget.checked)}
            />
            <span>{t("git.pr.draft")}</span>
          </label>
        </>
      )}
      {error ? <p className="environment-dialog-error" role="alert">{error}</p> : null}
    </Modal>
  );
}
