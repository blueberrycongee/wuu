import { ChevronDown, Split } from "./WuuIcons";
import type { Thread } from "../shared/protocol";
import { MessageCopyButton } from "./MessageActions";
import { translateCurrent as translate, useI18n } from "./i18n";

export function ForkWorktreeNotice({
  thread,
}: {
  thread: Thread;
}): JSX.Element | null {
  const { t } = useI18n();
  const worktree = thread.worktree;
  if (!worktree || !thread.forked_from_id) {
    return null;
  }

  const log = worktreeCreationLog(thread);
  const head = worktree.base_head?.trim();

  return (
    <section className="fork-worktree-notice" aria-label={t("worktree.forkedAria")}>
      <details className="fork-worktree-card">
        <summary className="fork-worktree-summary">
          <span className="fork-worktree-glyph">
            <Split className="icon" aria-hidden="true" />
          </span>
          <span className="fork-worktree-summary-text">
            <strong>{t("worktree.created")}</strong>
            <span>{t("worktree.forkedFromConversation")}</span>
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
          <div className="fork-worktree-code-block">
            <MessageCopyButton
              getText={() => log}
              className="fork-worktree-copy"
              iconSize={13}
              idleLabel={t("worktree.copyLog")}
              copiedLabel={t("worktree.logCopied")}
              failedLabel={t("common.copyFailed")}
            />
            <pre className="fork-worktree-code">
              <code>{log}</code>
            </pre>
          </div>
        </div>
      </details>
    </section>
  );
}

function worktreeCreationLog(thread: Thread): string {
  const worktree = thread.worktree;
  if (!worktree) {
    return "";
  }
  const head = worktree.base_head?.trim();
  const lines = [
    translate("worktree.logStarting"),
    head ? translate("worktree.logPreparing", { head: shortSHA(head) }) : "",
    translate("worktree.logBaseRepository", { path: worktree.base_repo || thread.cwd }),
    translate("worktree.logCreatedAt", { path: worktree.path }),
    translate("worktree.logForkSession", { id: thread.id }),
  ].filter(Boolean);
  return lines.join("\n");
}

function shortSHA(value: string): string {
  return value.length > 8 ? value.slice(0, 8) : value;
}
