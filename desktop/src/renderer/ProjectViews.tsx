import { useId, useState } from "react";
import type { ProjectCandidate, Thread, ThreadItem } from "../shared/protocol";
import { isThreadExecuting } from "./AppState";
import { useProjectActions, useProjectCandidates, type ProjectThread } from "./ProjectActions";
import { PROJECT_SESSION_SOURCE, projectSessionsOf } from "./ProjectSessions";
import { baseThreadTitle } from "./ThreadTitles";
import {
  ChevronDown,
  ChevronUp,
  FileDiff,
  LoaderCircle,
  MessagesSquare,
} from "./WuuIcons";
import { useI18n } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";

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
      title={parts.join(" · ")}
      aria-label={parts.join(" · ")}
      onClick={() => actions.openProjectPanel(project)}
    >
      {running ? <span className="project-status-count" aria-hidden="true"><LoaderCircle className="project-status-spinner" />{formatNumber(running)}</span> : null}
      {pending ? <span className="project-status-count" aria-hidden="true"><FileDiff />{formatNumber(pending)}</span> : null}
      <span className="project-status-count" aria-hidden="true"><MessagesSquare />{formatNumber(sessions.length)}</span>
    </button>
  );
}

const PROJECT_EVENTS: Record<string, TranslationKey> = {
  project_message: "projects.event.message",
  project_result: "projects.event.result",
  project_takeover: "projects.event.takeover",
  project_pause: "projects.event.pause",
  project_return: "projects.event.return",
  project_applied: "projects.event.applied",
  project_discarded: "projects.event.discarded",
  project_published: "projects.event.published",
  project_adopted: "projects.event.adopted",
  project_released: "projects.event.released",
};

/** Whether a message is a host event that a coordinator received. */
export function isProjectEvent(item: Pick<ThreadItem, "origin" | "cause">): boolean {
  return item.origin === "plugin" && item.cause !== undefined && item.cause in PROJECT_EVENTS;
}

/**
 * A turn's user messages with each run of two or more project events folded
 * into one group, so a burst of events reads as one marker.
 */
export function groupProjectEvents(items: ThreadItem[]): Array<ThreadItem | ThreadItem[]> {
  const entries: Array<ThreadItem | ThreadItem[]> = [];
  let run: ThreadItem[] = [];
  const flush = () => {
    if (run.length > 1) entries.push(run);
    else entries.push(...run);
    run = [];
  };
  for (const item of items) {
    if (isProjectEvent(item)) {
      run.push(item);
      continue;
    }
    flush();
    entries.push(item);
  }
  flush();
  return entries;
}

function eventSession(item: ThreadItem, threads: readonly ProjectThread[] | undefined): ProjectThread | undefined {
  return threads?.find((thread) => thread.id === item.related_session_id);
}

/** The session an event names, leading to it when it still exists. */
function EventSessionName({ item, session }: { item: ThreadItem; session: ProjectThread | undefined }): JSX.Element {
  const { t } = useI18n();
  const actions = useProjectActions();
  const name = session ? baseThreadTitle(session) : item.name?.trim() || t("projects.event.aSession");
  if (!session || !actions) return <span className="project-event-name"><span>{name}</span></span>;
  return (
    <button type="button" className="project-event-name" title={t("projects.openSession")} onClick={() => actions.openThread(session.id)}>
      <MessagesSquare aria-hidden="true" />
      <span>{name}</span>
    </button>
  );
}

function EventToggle({ expanded, controls, onToggle }: { expanded: boolean; controls: string; onToggle: () => void }): JSX.Element {
  const { t } = useI18n();
  const label = t(expanded ? "projects.event.hideDetails" : "projects.event.details");
  return (
    <button type="button" className="icon-button project-event-action project-event-toggle"
      title={label} aria-label={label} aria-expanded={expanded} aria-controls={controls} onClick={onToggle}>
      {expanded ? <ChevronUp aria-hidden="true" /> : <ChevronDown aria-hidden="true" />}
    </button>
  );
}

// Session names are controls, so they are spliced into the translated sentence.
function spliceSentence(sentence: string): [string, string] {
  const [before, after = ""] = sentence.split("\u0000");
  return [before.trim(), after.trim()];
}

/**
 * A host event in a coordinator's conversation, such as a session's result
 * or the user's decision. It reads as a centered marker between messages:
 * the session's name leads to it, and the text the coordinator read is
 * disclosed on request.
 */
export function ProjectEventRow({ item }: { item: ThreadItem }): JSX.Element {
  const { t } = useI18n();
  const actions = useProjectActions();
  const [expanded, setExpanded] = useState(false);
  const detailsID = useId();
  const label = PROJECT_EVENTS[item.cause ?? ""] ?? PROJECT_EVENTS.project_result;
  const session = eventSession(item, actions?.threads);
  const pending = session?.pending_candidates ?? 0;
  const reviewable = pending > 0 && (item.cause === "project_result" || item.cause === "project_adopted");
  const [before, after] = spliceSentence(t(label, { name: "\u0000" }));
  return (
    <div className="project-event" data-cause={item.cause}>
      <div className="project-event-line">
        <span className="project-event-text">
          {before}
          <EventSessionName item={item} session={session} />
          {after}
        </span>
        <span className="project-event-actions">
          {reviewable && session ? (
            <button type="button" className="icon-button project-event-action"
              title={t("projects.review")} aria-label={t("projects.review")} onClick={() => actions?.openProposal(session)}>
              <FileDiff aria-hidden="true" />
            </button>
          ) : null}
          <EventToggle expanded={expanded} controls={detailsID} onToggle={() => setExpanded((value) => !value)} />
        </span>
      </div>
      {expanded ? <p id={detailsID} className="project-event-details">{item.text}</p> : null}
    </div>
  );
}

/**
 * Consecutive host events as one marker naming their first session;
 * expanding it lists each event with its own actions.
 */
export function ProjectEventGroup({ items }: { items: ThreadItem[] }): JSX.Element {
  const { t, formatNumber } = useI18n();
  const actions = useProjectActions();
  const [expanded, setExpanded] = useState(false);
  const listID = useId();
  const sessions = new Set(items.map((item) => item.related_session_id || item.name || item.id)).size;
  const [before, after] = spliceSentence(sessions === 1
    ? t("projects.event.group", { count: formatNumber(items.length), name: "\u0000" })
    : t("projects.event.groupSessions", { count: formatNumber(items.length), sessions: formatNumber(sessions), name: "\u0000" }));
  return (
    <div className="project-event project-event-group">
      <div className="project-event-line">
        <span className="project-event-text">
          {before}
          <EventSessionName item={items[0]} session={eventSession(items[0], actions?.threads)} />
          {after}
        </span>
        <span className="project-event-actions">
          <EventToggle expanded={expanded} controls={listID} onToggle={() => setExpanded((value) => !value)} />
        </span>
      </div>
      {expanded ? (
        <div id={listID} className="project-event-list">
          {items.map((item) => <ProjectEventRow key={item.id} item={item} />)}
        </div>
      ) : null}
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
        <button type="button" className="icon-button project-icon-button"
          title={t(candidate.disposition ? "projects.viewProposal" : "projects.review")}
          aria-label={t(candidate.disposition ? "projects.viewProposal" : "projects.review")}
          onClick={() => actions.openProposal(session)}>
          <FileDiff aria-hidden="true" />
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
