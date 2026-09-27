import { ChevronDown, Split } from "./WuuIcons";
import type { Thread } from "../shared/protocol";
import { MessageCopyButton } from "./MessageActions";
import { translateCurrent as translate, useI18n } from "./i18n";

/** Record of the worktree a conversation runs in, whether it was forked into
 * one or started there. */
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
  const forked = Boolean(thread.forked_from_id);

  const log = worktreeCreationLog(thread);
  const head = worktree.base_head?.trim();

  return (
    <section className="fork-worktree-notice" aria-label={t(forked ? "worktree.forkedAria" : "worktree.created")}>
      <details className="fork-worktree-card">
        <summary className="fork-worktree-summary">
          <span className="fork-worktree-glyph">
            <Split className="icon" aria-hidden="true" />
          </span>
          <span className="fork-worktree-summary-text">
            <strong>{t("worktree.created")}</strong>
            <span>{t(forked ? "worktree.forkedFromConversation" : "worktree.startedSeparately")}</span>
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
    translate(thread.forked_from_id ? "worktree.logForkSession" : "worktree.logSession", { id: thread.id }),
  ].filter(Boolean);
  return lines.join("\n");
}

function shortSHA(value: string): string {
  return value.length > 8 ? value.slice(0, 8) : value;
}
