import { useId, useState } from "react";
import type { ProjectCandidate, Thread, ThreadItem } from "../shared/protocol";
import { isThreadExecuting } from "./AppState";
import { useProjectActions, useProjectCandidates, type ProjectThread } from "./ProjectActions";
import { PROJECT_SESSION_SOURCE, projectSessionsOf } from "./ProjectSessions";
import { baseThreadTitle } from "./ThreadTitles";
import {
  CircleCheck,
  CornerUpLeft,
  FileDiff,
  GitPullRequest,
  Hand,
  Inbox,
  LoaderCircle,
  LogOut,
  MessagesSquare,
  Workflow,
  X,
} from "./WuuIcons";
import { useI18n } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";

/** The empty project draft: what a project does before its first message. */
export function ProjectDraftIntro({ workspaceName, onSwitchToConversation }: {
  workspaceName?: string;
  onSwitchToConversation: () => void;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <div className="project-draft-intro">
      <p>{t("projects.draftHint")}</p>
      <p className="project-draft-meta">
        {workspaceName ? <span>{t("projects.inWorkspace", { workspace: workspaceName })}</span> : null}
        <button type="button" className="project-inline-link" onClick={onSwitchToConversation}>
          {t("projects.switchToConversation")}
        </button>
      </p>
    </div>
  );
}

/** The top of a project coordinator's conversation. */
export function ProjectConversationHeader({ project }: { project: Thread }): JSX.Element {
  const { t } = useI18n();
  const actions = useProjectActions();
  const workspace = actions?.workspaceName(project.workspace_id);
  return (
    <header className="project-conversation-header">
      <Workflow className="project-conversation-icon" aria-hidden="true" />
      <h2>{baseThreadTitle(project)}</h2>
      <p className="project-conversation-meta">
        {workspace ? <span>{t("projects.inWorkspace", { workspace })}</span> : null}
        {actions ? (
          <button type="button" className="project-inline-link" onClick={() => actions.openProjectPanel(project)}>
            {t("projects.viewProject")}
          </button>
        ) : null}
      </p>
    </header>
  );
}

/** The coordinator's running and pending work, above its composer. */
export function ProjectStatusStrip({ project }: { project: Thread }): JSX.Element | null {
  const { t, formatNumber } = useI18n();
  const actions = useProjectActions();
  if (!actions) return null;
  const sessions = projectSessionsOf(project.id, actions.threads);
  if (sessions.length === 0) return null;
  const running = sessions.filter(isThreadExecuting).length;
  const pending = project.pending_candidates ?? 0;
  const parts = [
    running ? t("projects.status.running", { count: formatNumber(running) }) : "",
    pending ? t("projects.status.pending", { count: formatNumber(pending) }) : "",
    t(sessions.length === 1 ? "projects.status.sessionsOne" : "projects.status.sessions", { count: formatNumber(sessions.length) }),
  ].filter(Boolean);
  return (
    <button
      type="button"
      className={`project-status-strip${pending ? " has-pending" : ""}`}
      onClick={() => actions.openProjectPanel(project)}
    >
      {running ? <LoaderCircle className="project-status-spinner" aria-hidden="true" /> : <Workflow aria-hidden="true" />}
      <span>{parts.join(" · ")}</span>
    </button>
  );
}

const PROJECT_EVENTS: Record<string, { label: TranslationKey; Icon: typeof Workflow }> = {
  project_result: { label: "projects.event.result", Icon: MessagesSquare },
  project_takeover: { label: "projects.event.takeover", Icon: Hand },
  project_pause: { label: "projects.event.pause", Icon: Hand },
  project_return: { label: "projects.event.return", Icon: CornerUpLeft },
  project_applied: { label: "projects.event.applied", Icon: CircleCheck },
  project_discarded: { label: "projects.event.discarded", Icon: X },
  project_published: { label: "projects.event.published", Icon: GitPullRequest },
  project_adopted: { label: "projects.event.adopted", Icon: Inbox },
  project_released: { label: "projects.event.released", Icon: LogOut },
};

/** Whether a message is a host event that a coordinator received. */
export function isProjectEvent(item: Pick<ThreadItem, "origin" | "cause">): boolean {
  return item.origin === "plugin" && item.cause !== undefined && item.cause in PROJECT_EVENTS;
}

/**
 * A host event in a coordinator's conversation, such as a session's result
 * or the user's decision. The coordinator read the full text; the row shows
 * what happened and where to go next, and discloses the text on request.
 */
export function ProjectEventRow({ item }: { item: ThreadItem }): JSX.Element {
  const { t } = useI18n();
  const actions = useProjectActions();
  const [expanded, setExpanded] = useState(false);
  const detailsID = useId();
  const event = PROJECT_EVENTS[item.cause ?? ""] ?? PROJECT_EVENTS.project_result;
  const session = actions?.threads.find((thread) => thread.id === item.related_session_id);
  const name = session ? baseThreadTitle(session) : item.name?.trim() || t("projects.event.aSession");
  const pending = session?.pending_candidates ?? 0;
  const reviewable = pending > 0 && (item.cause === "project_result" || item.cause === "project_adopted");
  return (
    <div className="project-event" data-cause={item.cause}>
      <div className="project-event-line">
        <event.Icon className="project-event-icon" aria-hidden="true" />
        <span className="project-event-text">{t(event.label, { name })}</span>
        {reviewable && session ? (
          <button type="button" className="project-inline-link" onClick={() => actions?.openProposal(session)}>
            {t("projects.review")}
          </button>
        ) : null}
        {session && actions ? (
          <button type="button" className="project-inline-link" onClick={() => actions.openThread(session.id)}>
            {t("projects.openSession")}
          </button>
        ) : null}
        <button
          type="button"
          className="project-inline-link project-event-toggle"
          aria-expanded={expanded}
          aria-controls={detailsID}
          onClick={() => setExpanded((value) => !value)}
        >
          {t(expanded ? "projects.event.hideDetails" : "projects.event.details")}
        </button>
      </div>
      {expanded ? <p id={detailsID} className="project-event-details">{item.text}</p> : null}
    </div>
  );
}

/**
 * A managed session's proposal under the turn that froze it. Deciding
 * happens in the right panel, next to the diff.
 */
export function ProposalSummary({ session, candidate }: {
  session: ProjectThread;
  candidate: ProjectCandidate;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const actions = useProjectActions();
  const files = candidate.changed_files.length;
  return (
    <div className="project-proposal-summary" data-disposition={candidate.disposition || "pending"}>
      <FileDiff className="project-proposal-icon" aria-hidden="true" />
      <span className="project-proposal-title">{t("projects.candidate.title")}</span>
      <span className="project-proposal-meta">
        {[
          t(files === 1 ? "environment.fileCountOne" : "environment.fileCount", { count: formatNumber(files) }),
          t(candidateStatusKey(candidate)),
        ].join(" · ")}
      </span>
      {actions && candidate.disposition !== "superseded" ? (
        <button type="button" className="project-inline-link" onClick={() => actions.openProposal(session)}>
          {t(candidate.disposition ? "projects.viewProposal" : "projects.review")}
        </button>
      ) : null}
    </div>
  );
}

export function candidateStatusKey(candidate: ProjectCandidate): TranslationKey {
  switch (candidate.disposition) {
    case "applied": return "projects.candidate.applied";
    case "discarded": return "projects.candidate.discarded";
    case "published": return "projects.candidate.published";
    case "superseded": return "projects.candidate.superseded";
    default: return "projects.candidate.pending";
  }
}

/**
 * Renders a managed session's proposals under the turns that froze them, for
 * a conversation pane's per-turn slot.
 */
export function useTurnProposals(thread: Thread): (turnID: string) => JSX.Element | null {
  const managed = thread.source === PROJECT_SESSION_SOURCE && Boolean(thread.project_id);
  const { candidates } = useProjectCandidates(
    managed ? { session_id: thread.id } : undefined,
    `${thread.pending_candidates ?? 0}:${thread.latest_completed_turn_id ?? ""}`,
  );
  return (turnID) => {
    const candidate = candidates.find((item) => item.turn_id === turnID);
    return candidate ? <ProposalSummary session={thread} candidate={candidate} /> : null;
  };
}
