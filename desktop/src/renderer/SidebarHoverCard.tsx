/*
 * Sidebar hover cards.
 *
 * Sidebar rows truncate their titles and compress state into a dot or a
 * spinner. Resting the pointer on a row (or reaching it by keyboard) shows a
 * card beside the sidebar that answers what the row cannot: which
 * conversation this is, what it is doing now, and where it works.
 *
 * Session cards also offer inline renaming. Workspace cards remain
 * descriptive tooltips. Timing and dismissal come from useHoverReveal.
 */
import { Children, createContext, Fragment, type ReactNode, useContext, useLayoutEffect, useRef, useState } from "react";
import type { DesktopProject } from "../shared/protocol";
import {
  isThreadExecuting,
  isThreadUnread,
  threadBelongsToWorkspace,
  type ThreadSummary,
} from "./AppState";
import { engineLabel } from "./EngineDisplay";
import { EngineIcon } from "./EngineIcons";
import type { HoverRevealLayerProps } from "./HoverReveal";
import { useI18n } from "./i18n";
import { isProjectCoordinator } from "./ProjectSessions";
import { LiveDuration } from "./TurnProgress";
import { formatRelativeTime } from "./TurnViewHelpers";
import { UILayerPortal } from "./ui/layers/UILayerHost";
import { AlertTriangle, Folder, GitBranch, MessageSquare, Project, Split } from "./WuuIcons";

const VIEWPORT_MARGIN = 8;
const ANCHOR_GAP = 8;

/**
 * Cross-list facts a row cannot see from its own list: which workspace owns
 * a conversation, and every session a workspace or project holds. AppSidebar
 * provides it; without a provider the cards show only per-thread facts.
 */
export type SidebarHoverFacts = {
  workspaces: readonly DesktopProject[];
  // Every loaded, unarchived session per workspace, including pinned and
  // filed ones that the workspace group itself does not list.
  threadsByWorkspaceID: Readonly<Record<string, readonly ThreadSummary[]>>;
  sessionsByProjectID: ReadonlyMap<string, readonly ThreadSummary[]>;
  activeThreadID?: string;
  lastViewedTurnByThreadID: Record<string, string>;
};

export const SidebarHoverFactsContext = createContext<SidebarHoverFacts | null>(null);

/** Places a card to the right of its anchor row, clamped to the viewport. */
export function SidebarHoverCardLayer({
  anchor,
  children,
  interaction,
  label,
}: {
  anchor: HTMLElement;
  children: ReactNode;
  interaction?: HoverRevealLayerProps;
  label?: string;
}): JSX.Element {
  const localRef = useRef<HTMLDivElement>(null);
  const layerRef = interaction?.ref ?? localRef;

  // Content can resize without rendering this layer (opening the editor,
  // or a live status update). Observe it so bottom-edge cards stay in view.
  // Direct style writes keep measurement out of React state.
  useLayoutEffect(() => {
    const layer = layerRef.current;
    if (!layer) return;
    const place = (): void => {
      const row = anchor.getBoundingClientRect();
      const card = layer.getBoundingClientRect();
      // Clear of the sidebar edge rather than the row, whose inset would put
      // the card on the divider; and with the title's first line centered on
      // the row, so the card reads as a continuation of that line.
      const sidebarRight = anchor.closest(".sidebar")?.getBoundingClientRect().right ?? row.right;
      const title = layer.querySelector(".sidebar-hover-card-title");
      const titleCenter = title
        ? title.getBoundingClientRect().top - card.top + parseFloat(getComputedStyle(title).lineHeight) / 2
        : 0;
      const maxLeft = Math.max(VIEWPORT_MARGIN, window.innerWidth - card.width - VIEWPORT_MARGIN);
      const maxTop = Math.max(VIEWPORT_MARGIN, window.innerHeight - card.height - VIEWPORT_MARGIN);
      const top = row.top + row.height / 2 - titleCenter;
      layer.style.left = `${Math.min(Math.max(row.right, sidebarRight) + ANCHOR_GAP, maxLeft)}px`;
      layer.style.top = `${Math.min(Math.max(top, VIEWPORT_MARGIN), maxTop)}px`;
      layer.style.visibility = "visible";
      // The entrance starts only once placed; see .sidebar-hover-card.
      layer.dataset.placed = "true";
    };
    place();
    const observer = new ResizeObserver(place);
    observer.observe(layer);
    window.addEventListener("resize", place);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", place);
    };
  }, [anchor, layerRef]);

  return (
    <UILayerPortal layer="popover">
      <div
        {...interaction}
        ref={layerRef}
        className="sidebar-hover-card"
        data-wuu-component="sidebar-hover-card"
        data-wuu-layer="popover"
        role={interaction ? "dialog" : "tooltip"}
        aria-label={label}
        data-interactive={interaction ? "true" : undefined}
        style={{ visibility: "hidden" }}
      >
        {children}
      </div>
    </UILayerPortal>
  );
}

// Facts follow the header only when there is something to list.
function HoverCardFacts({ children }: { children: ReactNode }): JSX.Element | null {
  return Children.toArray(children).length > 0
    ? <ul className="sidebar-hover-card-facts">{children}</ul>
    : null;
}

function HoverCardFact({
  icon,
  children,
  tone,
}: {
  icon: ReactNode;
  children: ReactNode;
  tone?: "warning";
}): JSX.Element {
  return (
    <li className="sidebar-hover-card-fact" data-tone={tone}>
      <span className="sidebar-hover-card-fact-icon" aria-hidden="true">{icon}</span>
      <span className="sidebar-hover-card-fact-text">{children}</span>
    </li>
  );
}

// A long path wraps after a separator, not inside a folder name.
function PathText({ path }: { path: string }): JSX.Element {
  return <>{path.split(/(?<=[/\\])/).map((part, index) => (
    <Fragment key={index}>{index > 0 ? <wbr /> : null}{part}</Fragment>
  ))}</>;
}

function SessionCounts({ sessions, facts }: {
  sessions: readonly ThreadSummary[];
  facts: SidebarHoverFacts;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  const live = sessions.filter((thread) => !thread.archived);
  const running = live.filter(isThreadExecuting).length;
  const unread = live.filter((thread) =>
    thread.id !== facts.activeThreadID &&
    !isThreadExecuting(thread) &&
    isThreadUnread(thread, facts.lastViewedTurnByThreadID[thread.id]),
  ).length;
  const parts = [t(live.length === 1 ? "sidebarHoverCard.sessionOne" : "sidebarHoverCard.sessions", {
    count: formatNumber(live.length),
  })];
  if (unread > 0) parts.push(t("sidebarHoverCard.unread", { count: formatNumber(unread) }));
  if (running > 0) parts.push(t("sidebarHoverCard.running", { count: formatNumber(running) }));
  // Wrap between counts, never inside one.
  return <>{parts.map((part, index) => (
    <span key={index} className="sidebar-hover-card-count">{index > 0 ? ` · ${part}` : part}</span>
  ))}</>;
}

export function ThreadHoverCardContent({
  thread,
  title,
  running,
  unread,
  onRename,
}: {
  thread: ThreadSummary;
  title: string;
  // The row's own indicator state, so the card never disagrees with the dot.
  running: boolean;
  unread: boolean;
  onRename?: (thread: ThreadSummary, title: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const facts = useContext(SidebarHoverFactsContext);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [editorWidth, setEditorWidth] = useState<number>();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const titleRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef(false);

  useLayoutEffect(() => {
    if (editing) {
      inputRef.current?.focus();
      inputRef.current?.select();
    } else if (returnFocusRef.current) {
      returnFocusRef.current = false;
      titleRef.current?.focus();
    }
  }, [editing]);

  useLayoutEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.style.height = "0px";
    input.style.height = `${input.scrollHeight}px`;
  }, [editing, draft]);

  function finishEditing(save: boolean): void {
    const next = draft.trim();
    if (save && !next) return;
    returnFocusRef.current = true;
    setEditing(false);
    if (save && next !== title.trim()) onRename?.(thread, next);
  }
  const latestTurn = thread.turns.at(-1);
  const lastActivity = latestTurn?.completed_at ?? latestTurn?.started_at ?? thread.updated_at;
  const runStartedAt = running && latestTurn?.status === "in_progress" && latestTurn.started_at
    ? Date.parse(latestTurn.started_at)
    : NaN;
  const workspace = facts?.workspaces.find((candidate) => threadBelongsToWorkspace(thread, candidate));
  const projectSessions = isProjectCoordinator(thread)
    ? facts?.sessionsByProjectID.get(thread.id) ?? []
    : undefined;
  const model = [thread.engine_id ? engineLabel(thread.engine_id) : "", thread.model]
    .filter(Boolean)
    .join(" · ");

  let status: ReactNode = null;
  if (running) {
    status = (
      <p className="sidebar-hover-card-status" data-tone="running">
        <span>
          {t("sidebarHoverCard.statusRunning")}
          {Number.isFinite(runStartedAt) ? <> · <LiveDuration startedAtMs={runStartedAt} /></> : null}
        </span>
      </p>
    );
  } else if (latestTurn?.status === "failed") {
    // A failure is what an unread failed turn has to say, so it wins.
    status = <p className="sidebar-hover-card-status" data-tone="failed">{t("sidebarHoverCard.statusFailed")}</p>;
  } else if (unread) {
    status = <p className="sidebar-hover-card-status" data-tone="unread">{t("sidebarHoverCard.statusUnread")}</p>;
  } else if (latestTurn?.status === "interrupted") {
    status = <p className="sidebar-hover-card-status">{t("sidebarHoverCard.statusInterrupted")}</p>;
  }

  return (
    <>
      <div className="sidebar-hover-card-heading">
        <div className="sidebar-hover-card-header">
          {editing ? (
            <textarea
              ref={inputRef}
              className="sidebar-hover-card-title sidebar-hover-card-title-input"
              aria-label={t("threadSidebar.title")}
              rows={1}
              style={{ width: editorWidth }}
              value={draft}
              spellCheck={false}
              autoComplete="off"
              onChange={(event) => setDraft(event.currentTarget.value.replace(/[\r\n]+/g, " "))}
              onKeyDown={(event) => {
                if (event.nativeEvent.isComposing || event.key === "Process") return;
                if (event.key === "Escape" || event.key === "Enter") {
                  event.preventDefault();
                  event.stopPropagation();
                  finishEditing(event.key === "Enter");
                }
              }}
            />
          ) : onRename ? (
            <button
              ref={titleRef}
              type="button"
              className="sidebar-hover-card-title sidebar-hover-card-title-editable"
              aria-label={`${t("threadSidebar.rename")}: ${title}`}
              onClick={(event) => {
                setEditorWidth(event.currentTarget.getBoundingClientRect().width);
                setDraft(title);
                setEditing(true);
              }}
            >
              {title}
            </button>
          ) : <span className="sidebar-hover-card-title">{title}</span>}
          {lastActivity ? (
            <span className="sidebar-hover-card-time">{formatRelativeTime(lastActivity)}</span>
          ) : null}
        </div>
        {status}
      </div>
      <HoverCardFacts>
        {workspace ? <HoverCardFact icon={<Folder />}>{workspace.name}</HoverCardFact> : null}
        {thread.worktree?.path ? (
          <HoverCardFact icon={<GitBranch />}>{t("sidebarHoverCard.worktree")}</HoverCardFact>
        ) : null}
        {thread.forked_from_id ? (
          <HoverCardFact icon={<Split />}>{t("sidebarHoverCard.forked")}</HoverCardFact>
        ) : null}
        {projectSessions && facts ? (
          <HoverCardFact icon={<Project />}>
            <SessionCounts sessions={projectSessions} facts={facts} />
          </HoverCardFact>
        ) : null}
        {model ? (
          <HoverCardFact icon={<EngineIcon engine={thread.engine_id || "wuu"} className="icon" />}>
            {model}
          </HoverCardFact>
        ) : null}
      </HoverCardFacts>
    </>
  );
}

export function WorkspaceHoverCardContent({
  workspace,
  scratch,
}: {
  workspace: DesktopProject;
  // The 对话 pseudo workspace has no folder of its own.
  scratch: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const facts = useContext(SidebarHoverFactsContext);
  const sessions = facts?.threadsByWorkspaceID[workspace.id];
  return (
    <>
      <div className="sidebar-hover-card-header">
        <span className="sidebar-hover-card-title">{workspace.name}</span>
      </div>
      <HoverCardFacts>
        {sessions && facts ? (
          <HoverCardFact icon={<MessageSquare />}>
            <SessionCounts sessions={sessions} facts={facts} />
          </HoverCardFact>
        ) : null}
        {!scratch ? (
          <HoverCardFact icon={<Folder />}>
            <PathText path={workspace.path} />
          </HoverCardFact>
        ) : null}
        {!scratch && workspace.missing ? (
          <HoverCardFact icon={<AlertTriangle />} tone="warning">
            {t("sidebarHoverCard.missingWorkspace")}
          </HoverCardFact>
        ) : null}
      </HoverCardFacts>
    </>
  );
}
