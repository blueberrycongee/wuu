import { BookOpen, Clock3, RefreshCw, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { CollaborationArrangement, CollaborationMemoryEntry, NamedAgent } from "../shared/protocol";
import { useI18n } from "./i18n";
import { RichContent } from "./RichContent";
import { SidebarNameDialog } from "./SidebarNameDialog";
import { toastErrorMessage } from "./Toast";
import "./styles/channel-continuity.css";

export function ChannelContinuity({ roomId, agents }: { roomId: string; agents: NamedAgent[] }): JSX.Element | null {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<"list" | "memory">("list");
  const [owner, setOwner] = useState("");
  const [arrangements, setArrangements] = useState<CollaborationArrangement[]>([]);
  const [entries, setEntries] = useState<CollaborationMemoryEntry[]>([]);
  const [selected, setSelected] = useState<CollaborationMemoryEntry | null>(null);
  const [draft, setDraft] = useState<string | null>(null);
  const [next, setNext] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const generation = useRef(0);
  const available = typeof window.wuu?.channelContinuity === "function";
  useEffect(() => {
    const token = ++generation.current;
    setSelected(null); setDraft(null); setNext(""); setError(""); setArrangements([]); setEntries([]);
    if (!open || !available) return;
    setBusy(true);
    void window.wuu!.channelContinuity({ action: tab, roomId, ownerId: owner, memory: { action: "list" } }).then(result => {
      if (generation.current !== token) return;
      setArrangements(result.arrangements ?? []); setEntries(result.entries ?? []); setNext(result.next ?? "");
    }).catch(reason => { if (generation.current === token) setError(toastErrorMessage(reason)); })
      .finally(() => { if (generation.current === token) setBusy(false); });
    return () => { generation.current++; };
  }, [open, available, roomId, owner, tab, refresh]);
  async function perform(operation: () => Promise<void>): Promise<void> {
    if (busy) return;
    const token = generation.current; setBusy(true); setError("");
    try { await operation(); } catch (reason) { if (generation.current === token) setError(toastErrorMessage(reason)); }
    finally { if (generation.current === token) setBusy(false); }
  }
  async function control(item: CollaborationArrangement, state: "active" | "paused" | "cancelled"): Promise<void> {
    await perform(async () => { await window.wuu!.channelContinuity({ action: "control", roomId, id: item.id, state, revision: item.revision }); setRefresh(value => value + 1); });
  }
  async function read(entry: CollaborationMemoryEntry): Promise<void> {
    const token = generation.current;
    await perform(async () => {
      const result = await window.wuu!.channelContinuity({ action: "memory", roomId, ownerId: owner, memory: { action: "read", name: entry.name } });
      if (generation.current === token) { setSelected(result.entries?.[0] ?? null); setDraft(null); }
    });
  }
  async function updateMemory(action: "write" | "delete"): Promise<void> {
    if (!selected) return;
    await perform(async () => {
      await window.wuu!.channelContinuity({ action: "memory", roomId, ownerId: owner, memory: { action, name: selected.name, revision: selected.revision, content: draft ?? selected.content ?? "" } });
      setRefresh(value => value + 1);
    });
  }
  async function more(): Promise<void> {
    const token = generation.current;
    await perform(async () => {
      const result = await window.wuu!.channelContinuity({ action: tab, roomId, ownerId: owner, after: next, memory: { action: "list", after: next } });
      if (generation.current !== token) return;
      setArrangements(values => [...values, ...(result.arrangements ?? [])]); setEntries(values => [...values, ...(result.entries ?? [])]); setNext(result.next ?? "");
    });
  }
  if (!available) return null;
  return <>
    <button type="button" className="icon-button channel-continuity-launcher" aria-label={t("channels.continuity.title")} title={t("channels.continuity.title")} onClick={() => setOpen(true)}><Clock3 className="icon" /></button>
    <SidebarNameDialog open={open} title="" onTitleChange={() => {}} onSubmit={() => {}} onClose={() => setOpen(false)} dialogTitle={t("channels.continuity.title")} dialogTitleId="channel-continuity-title" fieldLabel="" fieldAriaLabel="" placeholder="" icon={Clock3} submitLabel="" cancelLabel="" variant="drawer" hideActions dialogClassName="channel-sessions-dialog channel-continuity-dialog"
      content={<div className="channel-sessions-body channel-continuity-body">
        <button type="button" className="icon-button channel-sessions-close" aria-label={t("common.close")} onClick={() => setOpen(false)}><X className="icon" /></button>
        <nav className="channel-continuity-tabs" aria-label={t("channels.continuity.title")}>
          <button type="button" aria-pressed={tab === "list"} onClick={() => setTab("list")}><Clock3 className="icon" />{t("channels.continuity.arrangements")}</button>
          <button type="button" aria-pressed={tab === "memory"} onClick={() => setTab("memory")}><BookOpen className="icon" />{t("channels.continuity.memory")}</button>
          <button type="button" className="icon-button" disabled={busy} aria-label={t("channels.continuity.refresh")} onClick={() => setRefresh(value => value + 1)}><RefreshCw className="icon" /></button>
        </nav>
        {error ? <p className="channel-error" role="alert">{error}</p> : null}
        {busy ? <span role="status" className="channel-continuity-loading">{t("channels.continuity.loading")}</span> : null}
        {tab === "list" ? <>
          <p className="channel-continuity-note">{t("channels.continuity.hostRequired")}</p>
          {!busy && arrangements.length === 0 ? <p className="channel-session-empty">{t("channels.continuity.empty")}</p> : null}
          {[...arrangements].sort((a, b) => Number(b.state === "active") - Number(a.state === "active") || a.next_at.localeCompare(b.next_at)).map(item => <section className="channel-continuity-item" key={item.id}>
            <div className="channel-continuity-meta"><span>{agents.find(agent => agent.id === item.owner_id)?.name ?? t("channels.continuity.room")}</span><span>{t(`channels.continuity.state.${item.state}`)}</span></div>
            <p className="channel-continuity-note-body">{item.note}</p>
            <div className="channel-continuity-meta"><span>{item.when_session ? t("channels.continuity.waitingResult") : new Date(item.next_at).toLocaleString()}</span>{item.schedule ? <span>{t("channels.continuity.recurring")}{item.timezone ? ` · ${item.timezone}` : ""}</span> : null}</div>
            {item.reason ? <p className="channel-error">{item.reason}</p> : null}
            <div className="channel-continuity-actions">
              {item.state === "active" ? <button type="button" disabled={busy} onClick={() => void control(item, "paused")}>{t("channels.continuity.pause")}</button> : null}
              {item.state === "paused" ? <button type="button" disabled={busy} onClick={() => void control(item, "active")}>{t("channels.continuity.resume")}</button> : null}
              {["active", "paused", "blocked"].includes(item.state) ? <button type="button" disabled={busy} onClick={() => void control(item, "cancelled")}>{t("channels.continuity.cancel")}</button> : null}
            </div>
          </section>)}
        </> : <>
          <select className="channel-continuity-owner" aria-label={t("channels.continuity.memoryScope")} value={owner} onChange={event => setOwner(event.currentTarget.value)}><option value="">{t("channels.continuity.roomMemory")}</option>{agents.map(agent => <option key={agent.id} value={agent.id}>{agent.name}</option>)}</select>
          {!selected ? <>
            {!busy && entries.length === 0 ? <p className="channel-session-empty">{t("channels.continuity.noMemory")}</p> : null}
            {entries.map(entry => <button type="button" className="channel-continuity-topic" key={entry.name} onClick={() => void read(entry)} disabled={busy}><strong>{entry.name}</strong><span>{entry.content}</span></button>)}
          </> : <section className="channel-continuity-memory">
            <div className="channel-continuity-actions"><button type="button" onClick={() => { setSelected(null); setDraft(null); }}>{t("channels.continuity.back")}</button><strong>{selected.name}</strong></div>
            {draft === null ? <RichContent text={selected.content ?? ""} /> : <textarea aria-label={t("channels.continuity.memory")} value={draft} onChange={event => setDraft(event.currentTarget.value)} />}
            <div className="channel-continuity-actions">{draft === null ? <button type="button" onClick={() => setDraft(selected.content ?? "")}>{t("channels.continuity.edit")}</button> : <button type="button" disabled={busy} onClick={() => void updateMemory("write")}>{t("channels.continuity.save")}</button>}<button type="button" disabled={busy} onClick={() => void updateMemory("delete")}>{t("channels.continuity.forget")}</button></div>
          </section>}
        </>}
        {next && !selected ? <button type="button" disabled={busy} onClick={() => void more()}>{t("channels.continuity.more")}</button> : null}
      </div>} />
  </>;
}
