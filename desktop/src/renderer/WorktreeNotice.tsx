import { ChevronDown, Split } from "./WuuIcons";
import type { Thread } from "../shared/protocol";
import { MessageCopyButton } from "./MessageActions";
import { useI18n } from "./i18n";

/** Current workspace metadata for the inspector, not a historical creation event. */
export function WorktreeNotice({
  thread,
}: {
  thread: Thread;
}): JSX.Element | null {
  const { t } = useI18n();
  const worktree = thread.worktree;
  if (!worktree) {
    return null;
  }
  const head = worktree.base_head?.trim();

  return (
    <section className="fork-worktree-notice" aria-label={t("worktree.current")}>
      <details className="fork-worktree-card">
        <summary className="fork-worktree-summary">
          <Split className="fork-worktree-glyph icon" aria-hidden="true" />
          <span className="fork-worktree-summary-text">
            <strong>{t("worktree.current")}</strong>
          </span>
          <ChevronDown className="fork-worktree-chevron icon" aria-hidden="true" />
        </summary>
        <div className="fork-worktree-details">
          <dl className="fork-worktree-meta">
            <div>
              <dt>{t("worktree.baseRepository")}</dt>
              <dd>{worktree.base_repo || thread.cwd}</dd>
            </div>
            {head ? (
              <div>
                <dt>{t("worktree.baseCommit")}</dt>
                <dd>{shortSHA(head)}</dd>
              </div>
            ) : null}
            <div>
              <dt>{t("worktree.worktree")}</dt>
              <dd>{worktree.path}</dd>
            </div>
          </dl>
          <MessageCopyButton
            getText={() => JSON.stringify(worktree, null, 2)}
            className="fork-worktree-copy"
            iconSize={13}
            idleLabel={t("worktree.copyInfo")}
            copiedLabel={t("worktree.infoCopied")}
            failedLabel={t("common.copyFailed")}
          />
        </div>
      </details>
    </section>
  );
}

function shortSHA(value: string): string {
  return value.length > 8 ? value.slice(0, 8) : value;
}
