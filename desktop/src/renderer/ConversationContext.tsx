import { useState } from "react";
import type { CollaborationArrangement, CollaborationMemoryEntry } from "../shared/protocol";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

export function ConversationContext({ roomID, agentID }: { roomID: string; agentID?: string }): JSX.Element {
  const { t } = useI18n();
  const [tab, setTab] = useState<"project" | "identity" | "timers">("project");
  const [topics, setTopics] = useState<CollaborationMemoryEntry[]>([]);
  const [timers, setTimers] = useState<CollaborationArrangement[]>([]);
  const [next, setNext] = useState("");
  const [entry, setEntry] = useState<CollaborationMemoryEntry>();
  const [content, setContent] = useState("");
  const [name, setName] = useState("MEMORY.md");
  const [busy, setBusy] = useState(false);
  async function load(scope = tab, after = ""): Promise<void> {
    setBusy(true);
    try {
      const result = await window.wuu.channelContinuity(scope === "timers"
        ? { action: "list", roomId: roomID, after, limit: 20 }
        : { action: "memory", roomId: roomID, ownerId: scope === "identity" ? agentID : undefined, memory: { action: "list", after, limit: 20 } });
      if (scope === "timers") setTimers(previous => after ? [...previous, ...(result.arrangements ?? [])] : result.arrangements ?? []);
      else setTopics(previous => after ? [...previous, ...(result.entries ?? [])] : result.entries ?? []);
      setNext(result.next ?? "");
    } catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  async function read(topic: string): Promise<void> {
    setBusy(true);
    try {
      const result = await window.wuu.channelContinuity({ action: "memory", roomId: roomID, ownerId: tab === "identity" ? agentID : undefined, memory: { action: "read", name: topic } });
      setEntry(result.entries?.[0]); setContent(result.entries?.[0]?.content ?? ""); setName(topic);
    } catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  async function save(): Promise<void> {
    if (!entry) return;
    setBusy(true);
    try {
      const result = await window.wuu.channelContinuity({ action: "memory", roomId: roomID, ownerId: tab === "identity" ? agentID : undefined, memory: { action: "write", name: entry.name, content, revision: entry.revision } });
      setEntry(result.entries?.find(item => item.name === entry.name));
      await load();
    } catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  async function control(timer: CollaborationArrangement, state: "active" | "paused" | "cancelled"): Promise<void> {
    setBusy(true);
    try { await window.wuu.channelContinuity({ action: "control", roomId: roomID, id: timer.id, state, revision: timer.revision }); await load(); }
    catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  return <details className="conversation-context" onToggle={event => { if (event.currentTarget.open) void load(); }}>
    <summary>{t("channels.context.title")}</summary>
    <div className="conversation-context-body">
      <div className="channel-task-actions" role="group" aria-label={t("channels.context.title")}>
        {(["project", "identity", "timers"] as const).filter(scope => scope !== "identity" || agentID).map(scope => <button key={scope} type="button" disabled={busy} aria-pressed={tab === scope} onClick={() => { setTab(scope); setEntry(undefined); setNext(""); void load(scope); }}>{t(`channels.context.${scope}`)}</button>)}
      </div>
      {tab === "timers" ? <>
        {timers.length === 0 ? <p>{t("channels.context.noTimers")}</p> : timers.map(timer => <div className="conversation-timer" key={timer.id}>
          <p>{timer.note}</p><span>{t(`channels.context.state.${timer.state}`)} · {new Date(timer.next_at).toLocaleString()}{timer.timezone ? ` · ${timer.timezone}` : ""}</span>
          {timer.reason ? <p>{timer.reason}</p> : null}
          <div className="channel-task-actions">
            {timer.state === "active" ? <button type="button" disabled={busy} onClick={() => void control(timer, "paused")}>{t("channels.context.pause")}</button> : null}
            {timer.state === "paused" ? <button type="button" disabled={busy} onClick={() => void control(timer, "active")}>{t("channels.context.resume")}</button> : null}
            {["active", "paused", "blocked"].includes(timer.state) ? <button type="button" disabled={busy} onClick={() => void control(timer, "cancelled")}>{t("common.cancel")}</button> : null}
          </div>
        </div>)}
      </> : <>
        <p>{t(tab === "project" ? "channels.context.projectHint" : "channels.context.identityHint")}</p>
        <div className="channel-task-actions">{topics.map(topic => <button type="button" key={topic.name} disabled={busy} onClick={() => void read(topic.name)}>{topic.name}</button>)}</div>
        <form className="channel-task-actions" onSubmit={event => { event.preventDefault(); void read(name); }}>
          <input aria-label={t("channels.context.topic")} value={name} onChange={event => setName(event.target.value)} placeholder="MEMORY.md" />
          <button disabled={busy || !name.trim()} type="submit">{t("channels.context.edit")}</button>
        </form>
        {entry ? <label className="conversation-memory-editor">{entry.name}<textarea value={content} onChange={event => setContent(event.target.value)} /><button type="button" disabled={busy} onClick={() => void save()}>{t("common.save")}</button></label> : null}
      </>}
      {next ? <button type="button" disabled={busy} onClick={() => void load(tab, next)}>{t("channels.context.more")}</button> : null}
    </div>
  </details>;
}
