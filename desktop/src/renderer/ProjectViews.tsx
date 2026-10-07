import { useProjectWork } from "./ProjectWork";
import { useId, useState } from "react";
import type { Thread, ThreadItem } from "../shared/protocol";
import { isThreadExecuting } from "./AppState";
import { useProjectActions, type ProjectThread } from "./ProjectActions";
import { baseThreadTitle } from "./ThreadTitles";
import {
  ChevronDown,
  ChevronUp,
  LoaderCircle,
  MessagesSquare,
} from "./WuuIcons";
import { useI18n } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";

/**
 * The coordinator's running work, in the conversation's status capsule row.
 * The caller renders it only when the project has sessions.
 */
export function ProjectStatusCapsule({ project, sessions }: { project: Thread; sessions: readonly ProjectThread[] }): JSX.Element | null {
  const { t, formatNumber } = useI18n();
  const actions = useProjectActions();
  const work = useProjectWork(project.id);
  if (!actions) return null;
  const works = work.snapshot?.works ?? [];
  const outstanding = works.filter(item => item.phase !== "delivered");
  const running = sessions.filter(isThreadExecuting).length;
  const parts = works.length ? [t("projects.work.outstanding", { count: formatNumber(outstanding.length) })] : [
    running ? t("projects.status.running", { count: formatNumber(running) }) : "",
    t(sessions.length === 1 ? "projects.status.sessionsOne" : "projects.status.sessions", { count: formatNumber(sessions.length) }),
  ].filter(Boolean);
  return (
    <button
      type="button"
      className="conversation-status-capsule project-status-capsule"
      title={parts.join(" · ")}
      aria-label={parts.join(" · ")}
      onClick={() => actions.openProjectPanel(project)}
    >
      {running ? <span className="project-status-count" aria-hidden="true"><LoaderCircle className="project-status-spinner" />{formatNumber(running)}</span> : null}
      <span className="project-status-count"><MessagesSquare aria-hidden="true" />{works.length ? parts[0] : formatNumber(sessions.length)}</span>
    </button>
  );
}

const PROJECT_EVENTS: Record<string, TranslationKey> = {
  project_message: "projects.event.message",
  project_user_message: "projects.event.userMessage",
  project_stopped: "projects.event.stopped",
  project_result: "projects.event.result",
  project_takeover: "projects.event.takeover",
  project_pause: "projects.event.pause",
  project_return: "projects.event.return",
  project_adopted: "projects.event.adopted",
  project_released: "projects.event.released",
};

/** Whether a message is a host event that a coordinator received. */
export function isProjectEvent(item: Pick<ThreadItem, "origin" | "cause">): boolean {
  return (item.origin === "host" || item.origin === "plugin") && item.cause !== undefined && item.cause in PROJECT_EVENTS;
}

/**
 * A turn's user messages with each run of two or more project events folded
 * into one group, so a burst of events reads as one marker.
 */
export function groupProjectEvents(items: ThreadItem[]): Array<ThreadItem | [ThreadItem, ...ThreadItem[]]> {
  const entries: Array<ThreadItem | [ThreadItem, ...ThreadItem[]]> = [];
  let run: ThreadItem[] = [];
  const flush = () => {
    const [first, ...rest] = run;
    if (first && rest.length > 0) entries.push([first, ...rest]);
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
