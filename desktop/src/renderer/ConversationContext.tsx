import { useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent } from "react";
import type { CollaborationArrangement, CollaborationMemoryEntry } from "../shared/protocol";
import { ArrowLeft, ChevronDown, ChevronRight, Ellipsis, PanelRightClose, Pencil, Plus, Trash2 } from "./WuuIcons";
import { RichContent } from "./RichContent";
import { ThreadContextMenu, type ThreadContextMenuItem } from "./ThreadContextMenu";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

export type ConversationContextTab = "memory" | "timers";
type MemoryScope = "project" | "identity";
type MemoryGroup = { entries: CollaborationMemoryEntry[]; next: string; loaded: boolean };
type OpenTopic = { scope: MemoryScope; group: string; name: string; revision: string; content: string; draft: string; editing: boolean; creating: boolean };

const PAGE_SIZE = 50;
const EMPTY_GROUP: MemoryGroup = { entries: [], next: "", loaded: false };

function isPending(timer: CollaborationArrangement): boolean {
  return timer.state === "active" || timer.state === "paused" || timer.state === "blocked";
}

/**
 * Loads a conversation's timers once for both the header count and the panel.
 * `refreshKey` changes when the transcript advances, because agents schedule
 * timers from their replies.
 */
export function useConversationTimers(roomID: string, refreshKey: unknown) {
  const [timers, setTimers] = useState<CollaborationArrangement[]>([]);
  const [next, setNext] = useState("");
  const generation = useRef(0);
  const load = useCallback(async (after = "") => {
    if (!roomID) return;
    const request = ++generation.current;
    try {
      const result = await window.wuu.channelContinuity({ action: "list", roomId: roomID, after, limit: PAGE_SIZE });
      if (request !== generation.current) return;
      setTimers(previous => after ? [...previous, ...(result.arrangements ?? [])] : result.arrangements ?? []);
      setNext(result.next ?? "");
    } catch (error) { showErrorToast(error); }
  }, [roomID]);
  useEffect(() => {
    setTimers([]);
    setNext("");
  }, [roomID]);
  useEffect(() => { void load(); }, [load, refreshKey]);
  return { timers, next, pendingCount: timers.filter(timer => timer.state === "active" || timer.state === "blocked").length, reload: () => load(), loadMore: () => load(next) };
}

export type ConversationTimers = ReturnType<typeof useConversationTimers>;

// The host maintains MEMORY.md as the notebook's index of topics.
const MEMORY_INDEX = "MEMORY.md";

function topicHeading(content = ""): string {
  const first = content.split("\n").find(line => line.trim());
  return first && /^#\s+\S/.test(first) ? first.replace(/^#\s+/, "").trim() : "";
}

function topicTitle(entry: Pick<CollaborationMemoryEntry, "name" | "content">, indexTitle: string): string {
  if (entry.name === MEMORY_INDEX) return indexTitle;
  return topicHeading(entry.content) || entry.name.replace(/\.md$/i, "");
}

function topicBody(content: string): string {
  return topicHeading(content) ? content.replace(/^\s*#[^\n]*\n?/, "") : content;
}

function topicPreview(content = ""): string {
  return topicBody(content).split("\n")
    .map(line => line.trim().replace(/^([-*+>]|\d+\.)\s+/, "").replace(/\[([^\]]*)\]\([^)]*\)/g, "$1").replace(/[`*_#]/g, "").trim())
    .filter(Boolean).join(" · ");
}

function topicFileName(value: string): string {
  const name = value.trim();
  return !name || /\.md$/i.test(name) ? name : `${name}.md`;
}

export function ConversationContextPanel({ roomID, agentID, agentName, projectName, tab, timers, overlay, closing, onTabChange, onClose }: {
  roomID: string;
  agentID: string;
  agentName: string;
  projectName: string;
  tab: ConversationContextTab;
  timers: ConversationTimers;
  overlay: boolean;
  closing: boolean;
  onTabChange: (tab: ConversationContextTab) => void;
  onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [topic, setTopic] = useState<OpenTopic>();
  function handleKeyDown(event: ReactKeyboardEvent<HTMLElement>): void {
    if (event.key !== "Escape" || event.defaultPrevented) return;
    event.preventDefault();
    event.stopPropagation();
    if (topic && !topic.editing) setTopic(undefined);
    else if (!topic) onClose();
  }
  return <aside inert={closing} className={`session-inspector-extension conversation-context-panel${closing ? " closing" : ""}`}
    aria-label={`${agentName} · ${t(tab === "memory" ? "channels.context.memory" : "channels.context.timers")}`} onKeyDown={handleKeyDown}>
    <header className="session-inspector-header">
      <button type="button" className="icon-button" aria-label={t(overlay ? "channels.backToChat" : "common.close")} onClick={onClose}>
        {overlay ? <ArrowLeft aria-hidden="true" /> : <PanelRightClose aria-hidden="true" />}
      </button>
      <div className="theme-segmented conversation-context-tabs" role="group" aria-label={t("channels.context.title")}>
        {(["memory", "timers"] as const).map(value => <button key={value} type="button" aria-pressed={tab === value} onClick={() => { setTopic(undefined); onTabChange(value); }}>
          {t(value === "memory" ? "channels.context.memory" : "channels.context.timers")}
        </button>)}
      </div>
    </header>
    <div className="conversation-context-body scroll-region">
      {tab === "memory"
        ? topic ? <MemoryTopic roomID={roomID} agentID={agentID} topic={topic} onChange={setTopic} onClose={() => setTopic(undefined)} />
          : <MemoryOverview roomID={roomID} agentID={agentID} agentName={agentName} projectName={projectName} onOpen={setTopic} />
        : <TimerList roomID={roomID} timers={timers} />}
    </div>
  </aside>;
}

function MemoryOverview({ roomID, agentID, agentName, projectName, onOpen }: {
  roomID: string; agentID: string; agentName: string; projectName: string; onOpen: (topic: OpenTopic) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [groups, setGroups] = useState<Record<MemoryScope, MemoryGroup>>({ project: EMPTY_GROUP, identity: EMPTY_GROUP });
  const [busy, setBusy] = useState(false);
  const load = useCallback(async (scope: MemoryScope, after = "") => {
    try {
      const result = await window.wuu.channelContinuity({ action: "memory", roomId: roomID, ownerId: scope === "identity" ? agentID : undefined, memory: { action: "list", after, limit: PAGE_SIZE } });
      setGroups(current => ({ ...current, [scope]: { entries: after ? [...current[scope].entries, ...(result.entries ?? [])] : result.entries ?? [], next: result.next ?? "", loaded: true } }));
    } catch (error) { showErrorToast(error); }
  }, [agentID, roomID]);
  useEffect(() => { void load("project"); void load("identity"); }, [load]);
  async function open(scope: MemoryScope, group: string, name: string): Promise<void> {
    setBusy(true);
    try {
      const result = await window.wuu.channelContinuity({ action: "memory", roomId: roomID, ownerId: scope === "identity" ? agentID : undefined, memory: { action: "read", name } });
      const entry = result.entries?.[0];
      if (entry) onOpen({ scope, group, name, revision: entry.revision, content: entry.content ?? "", draft: entry.content ?? "", editing: false, creating: false });
    } catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  return <>
    {(["project", "identity"] as const).map(scope => {
      const group = groups[scope];
      const heading = scope === "project" ? t("channels.context.projectGroup", { project: projectName }) : t("channels.context.identityGroup", { name: agentName });
      return <section key={scope} className="conversation-context-group" aria-label={heading}>
        <header className="conversation-context-group-header">
          <div>
            <h3>{heading}</h3>
            <p>{t(scope === "project" ? "channels.context.projectHint" : "channels.context.identityHint")}</p>
          </div>
          <button type="button" className="icon-button" aria-label={t("channels.context.newTopic")} title={t("channels.context.newTopic")}
            onClick={() => onOpen({ scope, group: heading, name: "", revision: "missing", content: "", draft: "", editing: true, creating: true })}>
            <Plus aria-hidden="true" />
          </button>
        </header>
        {group.entries.length ? <ul className="conversation-context-list">
          {group.entries.map(entry => <li key={entry.name}>
            <button type="button" className="conversation-context-row" disabled={busy} title={entry.name} onClick={() => void open(scope, heading, entry.name)}>
              <strong>{topicTitle(entry, t("channels.context.index"))}</strong>
              {topicPreview(entry.content) ? <span>{topicPreview(entry.content)}</span> : null}
            </button>
          </li>)}
        </ul> : group.loaded ? <p className="conversation-context-empty">{t("channels.context.noMemory")}</p> : null}
        {group.next ? <button type="button" className="settings-button settings-button-ghost conversation-context-more" onClick={() => void load(scope, group.next)}>{t("channels.context.more")}</button> : null}
      </section>;
    })}
  </>;
}

function MemoryTopic({ roomID, agentID, topic, onChange, onClose }: {
  roomID: string; agentID: string; topic: OpenTopic; onChange: (topic: OpenTopic) => void; onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const ownerId = topic.scope === "identity" ? agentID : undefined;
  const name = topic.creating ? topicFileName(topic.name) : topic.name;
  async function save(): Promise<void> {
    setBusy(true);
    try {
      // New topics carry the "missing" revision, so the host rejects a name already in use.
      const result = await window.wuu.channelContinuity({ action: "memory", roomId: roomID, ownerId, memory: { action: "write", name, content: topic.draft, revision: topic.revision } });
      const entry = result.entries?.find(item => item.name === name);
      onChange({ ...topic, name, revision: entry?.revision ?? topic.revision, content: topic.draft, editing: false, creating: false });
    } catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  async function remove(): Promise<void> {
    if (!window.confirm(t("channels.context.deleteConfirm", { name: topic.name }))) return;
    setBusy(true);
    try {
      await window.wuu.channelContinuity({ action: "memory", roomId: roomID, ownerId, memory: { action: "delete", name: topic.name, revision: topic.revision } });
      onClose();
    } catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  const title = topic.creating ? t("channels.context.newTopic") : topicTitle({ name: topic.name, content: topic.content }, t("channels.context.index"));
  return <article className="conversation-context-topic" aria-busy={busy || undefined}>
    <header className="conversation-context-topic-header">
      <button type="button" className="icon-button" aria-label={t("common.back")} disabled={busy} onClick={() => {
        if (topic.editing && !topic.creating) onChange({ ...topic, draft: topic.content, editing: false });
        else onClose();
      }}><ArrowLeft aria-hidden="true" /></button>
      <div className="conversation-context-topic-title">
        <strong>{title}</strong>
        <span>{topic.creating ? topic.group : `${topic.group} · ${topic.name}`}</span>
      </div>
      {!topic.editing ? <>
        <button type="button" className="icon-button" aria-label={t("common.edit")} title={t("common.edit")} disabled={busy} onClick={() => onChange({ ...topic, draft: topic.content, editing: true })}><Pencil aria-hidden="true" /></button>
        {topic.revision !== "missing" ? <button type="button" className="icon-button" aria-label={t("channels.context.delete")} title={t("channels.context.delete")} disabled={busy} onClick={() => void remove()}><Trash2 aria-hidden="true" /></button> : null}
      </> : null}
    </header>
    {topic.editing ? <form className="conversation-context-editor" onSubmit={event => { event.preventDefault(); void save(); }}>
      {topic.creating ? <input className="settings-input" autoFocus aria-label={t("channels.context.topic")} placeholder={t("channels.context.topicPlaceholder")}
        value={topic.name} onChange={event => onChange({ ...topic, name: event.target.value })} /> : null}
      <textarea className="settings-input settings-textarea" autoFocus={!topic.creating} aria-label={t("channels.context.content")} value={topic.draft}
        onChange={event => onChange({ ...topic, draft: event.target.value })} />
      <div className="conversation-context-editor-actions">
        <button type="button" className="settings-button settings-button-ghost" disabled={busy} onClick={() => {
          if (topic.creating) onClose();
          else onChange({ ...topic, draft: topic.content, editing: false });
        }}>{t("common.cancel")}</button>
        <button type="submit" className="settings-button settings-button-primary" disabled={busy || !name || /[\\/]/.test(name)}>{t("common.save")}</button>
      </div>
    </form> : topicBody(topic.content).trim() ? <div className="conversation-context-topic-content"><RichContent text={topicBody(topic.content)} /></div>
      : <p className="conversation-context-empty">{t("channels.context.emptyTopic")}</p>}
  </article>;
}

function TimerList({ roomID, timers }: { roomID: string; timers: ConversationTimers }): JSX.Element {
  const { t } = useI18n();
  const [showFinished, setShowFinished] = useState(false);
  const pending = timers.timers.filter(isPending);
  const finished = timers.timers.filter(timer => !isPending(timer));
  if (!timers.timers.length) return <p className="conversation-context-empty">{t("channels.context.noTimers")}</p>;
  return <>
    <ul className="conversation-context-list">
      {pending.map(timer => <TimerRow key={timer.id} roomID={roomID} timer={timer} onChanged={timers.reload} />)}
    </ul>
    {!pending.length ? <p className="conversation-context-empty">{t("channels.context.noPendingTimers")}</p> : null}
    {finished.length ? <section className="conversation-context-group">
      <button type="button" className="conversation-context-disclosure" aria-expanded={showFinished} onClick={() => setShowFinished(value => !value)}>
        {showFinished ? <ChevronDown aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}
        {t("channels.context.finishedTimers", { count: finished.length })}
      </button>
      {showFinished ? <ul className="conversation-context-list">
        {finished.map(timer => <TimerRow key={timer.id} roomID={roomID} timer={timer} onChanged={timers.reload} />)}
      </ul> : null}
    </section> : null}
    {timers.next ? <button type="button" className="settings-button settings-button-ghost conversation-context-more" onClick={() => void timers.loadMore()}>{t("channels.context.more")}</button> : null}
  </>;
}

function TimerRow({ roomID, timer, onChanged }: { roomID: string; timer: CollaborationArrangement; onChanged: () => Promise<void> }): JSX.Element {
  const { t, formatDate } = useI18n();
  const [menu, setMenu] = useState<{ x: number; y: number }>();
  const [busy, setBusy] = useState(false);
  async function control(state: "active" | "paused" | "cancelled"): Promise<void> {
    setBusy(true);
    try {
      await window.wuu.channelContinuity({ action: "control", roomId: roomID, id: timer.id, state, revision: timer.revision });
      await onChanged();
    } catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  const items: ThreadContextMenuItem[] = [];
  if (timer.state === "active") items.push({ label: t("channels.context.pause"), onSelect: () => void control("paused") });
  if (timer.state === "paused") items.push({ label: t("channels.context.resume"), onSelect: () => void control("active") });
  if (isPending(timer)) items.push({ label: t("channels.context.cancelTimer"), danger: true, onSelect: () => void control("cancelled") });
  const when = timer.next_at && !timer.next_at.startsWith("0001-")
    ? formatDate(timer.state === "done" && timer.last_at ? timer.last_at : timer.next_at, { month: "short", day: "numeric", weekday: "short", hour: "2-digit", minute: "2-digit" })
    : "";
  const meta = [
    timer.state === "active" ? (when ? t("channels.context.nextRun", { time: when }) : "") : t(`channels.context.state.${timer.state}`),
    timer.state !== "active" && when ? when : "",
    timer.schedule ? t("channels.context.repeats") : "",
  ].filter(Boolean).join(" · ");
  const openMenu = (event: ReactMouseEvent<HTMLButtonElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    setMenu({ x: rect.right, y: rect.bottom });
  };
  return <li className="conversation-context-timer" data-state={timer.state} aria-busy={busy || undefined}>
    <div className="conversation-context-timer-copy">
      <p>{timer.note}</p>
      <span title={timer.schedule ? `${timer.schedule}${timer.timezone ? ` · ${timer.timezone}` : ""}` : timer.timezone}>{meta}</span>
      {timer.state === "blocked" && timer.reason ? <span className="conversation-context-warning">{timer.reason}</span> : null}
    </div>
    {items.length ? <button type="button" className="icon-button" aria-label={t("channels.context.timerActions")} aria-haspopup="menu" aria-expanded={Boolean(menu)} disabled={busy} onClick={openMenu}>
      <Ellipsis aria-hidden="true" />
    </button> : null}
    {menu ? <ThreadContextMenu x={menu.x} y={menu.y} items={items} onClose={() => setMenu(undefined)} /> : null}
  </li>;
}
