import { ChevronLeft, Layers3, Plus, Search, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ChannelRoom, ChannelSessionReadResult, CollaborationSessionBinding, InitializeResult, NamedAgent, ThreadItem } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { useI18n } from "./i18n";
import { RichContent } from "./RichContent";
import { SidebarNameDialog } from "./SidebarNameDialog";
import { toastErrorMessage } from "./Toast";

const isRunning = (session: CollaborationSessionBinding): boolean => session.state === "running" || session.state === "starting";
const isResumable = (session: CollaborationSessionBinding): boolean => ["interrupted", "cancelled", "failed", "missing"].includes(session.state);

function SessionHistory({ result }: { result: ChannelSessionReadResult }): JSX.Element {
  const { t } = useI18n();
  const [showAll, setShowAll] = useState(false);
  const [expandedActivity, setExpandedActivity] = useState<Record<string, boolean>>({});
  const turns = result.thread.turns ?? [];
  const visibleTurns = showAll ? turns : turns.slice(-20);
  function itemText(item: ThreadItem): string {
    return item.text || item.result || item.error || item.summary || item.arguments || "";
  }
  return <div className="channel-session-history" aria-label={t("channels.sessions.history")}>
    {turns.length > visibleTurns.length ? <button type="button" onClick={() => setShowAll(true)}>{t("channels.sessions.earlier")}</button> : null}
    {turns.length === 0 ? <p className="channel-session-empty">{t("channels.sessions.noHistory")}</p> : null}
    {visibleTurns.map((turn) => <section key={turn.id} className="channel-session-turn">
      {turn.items.filter((item) => item.type === "user_message" || item.type === "agent_message").map((item) => <div className={`channel-session-message ${item.type}`} key={item.id}>
        <strong>{t(item.type === "user_message" ? "channels.sessions.input" : "channels.sessions.response")}</strong>
        <RichContent text={itemText(item)} />
      </div>)}
      {turn.items.some((item) => item.type !== "user_message" && item.type !== "agent_message") ? <details className="channel-session-activity" open={Boolean(expandedActivity[turn.id])} onToggle={(event) => { const open = event.currentTarget.open; setExpandedActivity((current) => current[turn.id] === open ? current : { ...current, [turn.id]: open }); }}>
        <summary>{t("channels.sessions.activity")}</summary>
        {expandedActivity[turn.id] ? turn.items.filter((item) => item.type !== "user_message" && item.type !== "agent_message").map((item) => <div key={item.id}>
          <strong>{item.name || item.type}</strong>
          {itemText(item) ? <RichContent text={itemText(item)} /> : null}
        </div>) : null}
      </details> : null}
      {turn.error ? <p className="channel-error" role="alert">{turn.error.message}</p> : null}
    </section>)}
  </div>;
}

export function ChannelSessions({ agents, rooms, roomId, agentId, initialized }: {
  agents: NamedAgent[];
  rooms: ChannelRoom[];
  roomId?: string;
  agentId?: string;
  initialized?: InitializeResult;
}): JSX.Element | null {
  const { t, formatDate } = useI18n();
  const [open, setOpen] = useState(false);
  const [sessions, setSessions] = useState<CollaborationSessionBinding[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [busy, setBusy] = useState(false);
  const [query, setQuery] = useState("");
  const [visibleCounts, setVisibleCounts] = useState<Record<string, number>>({});
  const [selectedRef, setSelectedRef] = useState("");
  const [detail, setDetail] = useState<ChannelSessionReadResult | null>(null);
  const [detailError, setDetailError] = useState("");
  const [createAgentId, setCreateAgentId] = useState(agentId || agents[0]?.id || "");
  const [createRoomId, setCreateRoomId] = useState(roomId || "");
  const [title, setTitle] = useState("");
  const [prompt, setPrompt] = useState("");
  const [model, setModel] = useState("");
  const [followups, setFollowups] = useState<Record<string, string>>({});
  const requestGeneration = useRef(0);
  const detailGeneration = useRef(0);
  const mutationPending = useRef(false);
  const deliveryRequests = useRef(new Map<string, string>());
  const mounted = useRef(true);
  const available = typeof window.wuu?.listChannelSessions === "function";

  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; requestGeneration.current++; detailGeneration.current++; };
  }, []);

  const refresh = useCallback(async (): Promise<void> => {
    if (!available) return;
    const generation = ++requestGeneration.current;
    try {
      const result = await window.wuu!.listChannelSessions({ ...(roomId ? { roomId } : {}), ...(agentId ? { agentId } : {}) });
      if (!mounted.current || generation !== requestGeneration.current) return;
      setSessions(result.sessions ?? []);
      setError("");
    } catch (reason) {
      if (mounted.current && generation === requestGeneration.current) setError(toastErrorMessage(reason));
    } finally {
      if (mounted.current && generation === requestGeneration.current) setLoading(false);
    }
  }, [agentId, available, roomId]);

  useEffect(() => {
    setSessions([]);
    setSelectedRef("");
    setLoading(true);
    setCreating(false);
    setCreateAgentId(agentId || agents[0]?.id || "");
    setCreateRoomId(roomId || "");
    void refresh();
    return () => { requestGeneration.current++; };
  }, [agentId, roomId, refresh]);

  useEffect(() => {
    if (!available) return;
    const timer = window.setInterval(() => {
      if (document.visibilityState !== "hidden" && !mutationPending.current) void refresh();
    }, open ? 3_000 : 10_000);
    return () => window.clearInterval(timer);
  }, [available, open, refresh]);

  const read = useCallback(async (): Promise<void> => {
    if (!selectedRef) return;
    const generation = ++detailGeneration.current;
    try {
      const result = await window.wuu!.readChannelSession({ sessionRef: selectedRef });
      if (!mounted.current || generation !== detailGeneration.current) return;
      setDetail(result);
      setDetailError("");
    } catch (reason) {
      if (mounted.current && generation === detailGeneration.current) setDetailError(toastErrorMessage(reason));
    }
  }, [selectedRef]);

  useEffect(() => {
    setDetail(null);
    setDetailError("");
    if (!open || !selectedRef) return;
    void read();
    const timer = window.setInterval(() => { if (!mutationPending.current) void read(); }, 3_000);
    return () => { detailGeneration.current++; window.clearInterval(timer); };
  }, [open, read, selectedRef]);

  const selectedSession = sessions.find((session) => session.session_ref === selectedRef) ?? detail?.session;
  const groups = useMemo(() => {
    const grouped = new Map<string, CollaborationSessionBinding[]>();
    for (const session of [...sessions].sort((a, b) => Number(isRunning(b)) - Number(isRunning(a)) || b.updated_at.localeCompare(a.updated_at))) {
      const identity = session.named_agent_id || session.principal_id;
      const agent = agents.find((candidate) => candidate.id === identity);
      if (query && !`${agent?.name ?? identity} ${session.title ?? ""} ${session.objective ?? ""} ${session.model ?? ""}`.toLocaleLowerCase().includes(query.toLocaleLowerCase())) continue;
      grouped.set(identity, [...(grouped.get(identity) ?? []), session]);
    }
    return grouped;
  }, [agents, query, sessions]);
  const activeCount = sessions.filter(isRunning).length;
  const createAgent = agents.find((agent) => agent.id === createAgentId);
  const requiresByokSetup = Boolean(createAgent?.engine_override && createAgent.engine_override !== "wuu");
  const candidateRooms = rooms.filter((room) => room.members.some((member) => member.member_type === "agent" && member.member_id === createAgentId));

  async function mutate(action: () => Promise<void>): Promise<void> {
    if (mutationPending.current) return;
    mutationPending.current = true;
    requestGeneration.current++;
    detailGeneration.current++;
    setBusy(true);
    setError("");
    try {
      await action();
      await refresh();
      if (selectedRef) await read();
    } catch (reason) {
      if (mounted.current) setError(toastErrorMessage(reason));
    } finally {
      mutationPending.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  function beginCreate(): void {
    setCreating(true);
    setSelectedRef("");
    setTitle("");
    setPrompt("");
    setModel("");
    setError("");
    setCreateAgentId(agentId || agents[0]?.id || "");
    setCreateRoomId(roomId || "");
  }

  function requestId(key: string): string {
    // Keep the same request after an uncertain network failure so retries do not duplicate work.
    let id = deliveryRequests.current.get(key);
    if (!id) {
      id = Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, "0")).join("");
      deliveryRequests.current.set(key, id);
    }
    return id;
  }

  function submit(): void {
    if (creating) {
      if (!prompt.trim() || !createAgentId) return;
      void mutate(async () => {
        const targetRoom = createRoomId || (await window.wuu!.openChannelDirectMessage({ agent_id: createAgentId })).room.id;
        const [provider, modelId] = model.split("\u0000");
        const deliveryKey = JSON.stringify(["create", createAgentId, targetRoom, title.trim(), prompt.trim(), model]);
        const result = await window.wuu!.createChannelSession({
          agentId: createAgentId, roomId: targetRoom, prompt: prompt.trim(),
          requestId: requestId(deliveryKey),
          ...(title.trim() ? { title: title.trim() } : {}),
          ...(provider && modelId ? { provider, model: modelId } : {}),
        });
        deliveryRequests.current.delete(deliveryKey);
        setCreating(false);
        setPrompt("");
        setSelectedRef(result.session.session_ref);
      });
    } else if (selectedRef && followups[selectedRef]?.trim()) {
      const sentPrompt = followups[selectedRef].trim();
      const deliveryKey = JSON.stringify(["send", selectedRef, sentPrompt]);
      void mutate(async () => {
        await window.wuu!.sendChannelSession({ sessionRef: selectedRef, prompt: sentPrompt, requestId: requestId(deliveryKey) });
        deliveryRequests.current.delete(deliveryKey);
        setFollowups((current) => ({ ...current, [selectedRef]: "" }));
      });
    }
  }

  if (!available) return null;
  return <>
    <button className="channel-sessions-launcher" type="button" onClick={() => { setOpen(true); void refresh(); }} aria-haspopup="dialog" aria-label={t("channels.sessions.title")}>
      <Layers3 className="icon" />
      <span>{t("channels.sessions.title")}</span>
      {activeCount > 0 ? <span className="channel-sessions-count" role="status">{t("channels.sessions.runningCount", { count: activeCount })}</span> : <span>{sessions.length}</span>}
    </button>
    <SidebarNameDialog
      open={open} title="" onTitleChange={() => undefined} onSubmit={submit} onClose={() => setOpen(false)}
      dialogTitle={t("channels.sessions.title")} dialogTitleId="channel-sessions-title" fieldLabel="" fieldAriaLabel="" placeholder=""
      icon={Layers3} submitLabel="" cancelLabel="" variant="drawer" hideActions dialogClassName="channel-sessions-dialog"
      content={<div className="channel-sessions-body">
        <button type="button" className="icon-button channel-sessions-close" aria-label={t("common.close")} onClick={() => setOpen(false)}><X className="icon" /></button>
        {error ? <div className="channel-error" role="alert">{error}<button type="button" onClick={() => void refresh()}>{t("channels.sessions.retry")}</button></div> : null}
        {creating ? <div className="channel-session-create">
          <button type="button" className="channel-session-back" onClick={() => setCreating(false)} disabled={busy}><ChevronLeft className="icon" />{t("channels.sessions.back")}</button>
          <h3>{t("channels.sessions.new")}</h3>
          {requiresByokSetup ? <p className="channel-error">{t("channels.sessions.identitySetupRequired")}</p> : null}
          <label><span>{t("channels.sessions.identity")}</span><select value={createAgentId} disabled={Boolean(agentId) || busy} onChange={(event) => { setCreateAgentId(event.currentTarget.value); if (!roomId) setCreateRoomId(""); }}>
            {agents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
          </select></label>
          {!roomId ? <label><span>{t("channels.sessions.room")}</span><select value={createRoomId} disabled={busy} onChange={(event) => setCreateRoomId(event.currentTarget.value)}>
            <option value="">{t("channels.sessions.direct")}</option>
            {candidateRooms.filter((room) => room.kind === "channel").map((room) => <option value={room.id} key={room.id}>{room.name}</option>)}
          </select></label> : null}
          <label><span>{t("channels.sessions.taskTitle")}</span><input value={title} disabled={busy} onChange={(event) => setTitle(event.currentTarget.value)} /></label>
          <label><span>{t("channels.sessions.objective")}</span><textarea autoFocus rows={5} value={prompt} disabled={busy} onChange={(event) => setPrompt(event.currentTarget.value)} /></label>
          <label><span>{t("channels.model")}</span><select value={model} disabled={busy} onChange={(event) => setModel(event.currentTarget.value)}>
            <option value="">{t("channels.sessions.identityModel")}{createAgent?.model_override ? ` · ${createAgent.model_override}` : initialized?.model ? ` · ${initialized.model}` : ""}</option>
            {(initialized?.providers ?? []).map((provider) => <optgroup key={provider.name} label={provider.name}>
              {(provider.models?.length ? provider.models : [{ id: provider.model, display_name: provider.model }]).filter((entry) => entry.id).map((entry) => <option key={entry.id} value={`${provider.name}\u0000${entry.id}`}>{provider.name} · {entry.display_name || entry.id}</option>)}
            </optgroup>)}
          </select></label>
          <button className="channel-session-primary" type="submit" disabled={busy || !prompt.trim() || !createAgentId || requiresByokSetup}>{t(busy ? "channels.sessions.starting" : "channels.sessions.start")}</button>
        </div> : selectedRef ? <div className="channel-session-detail">
          <button type="button" className="channel-session-back" onClick={() => setSelectedRef("")}><ChevronLeft className="icon" />{t("channels.sessions.back")}</button>
          {selectedSession ? <>
            <h3>{selectedSession.title || selectedSession.objective || t("channels.sessions.untitled")}</h3>
            <div className="channel-session-meta"><span>{agents.find((agent) => agent.id === selectedSession.named_agent_id)?.name || selectedSession.principal_id}</span><span>{t(`channels.sessions.state.${selectedSession.state}`)}</span><span>{[selectedSession.provider, selectedSession.model].filter(Boolean).join(" · ")}</span></div>
            {selectedSession.failure_reason ? <p className="channel-error" role="alert">{selectedSession.failure_reason}</p> : null}
            <div className="channel-session-actions">
              {isRunning(selectedSession) || selectedSession.state === "idle" || selectedSession.state === "queued" || selectedSession.state === "waiting" ? <button type="button" disabled={busy} onClick={() => void mutate(async () => { await window.wuu!.stopChannelSession({ sessionRef: selectedRef }); })}>{t("channels.sessions.stop")}</button> : isResumable(selectedSession) ? <button type="button" disabled={busy} onClick={() => void mutate(async () => { await window.wuu!.resumeChannelSession({ sessionRef: selectedRef }); })}>{t("channels.sessions.resume")}</button> : null}
            </div>
          </> : null}
          {detailError ? <div className="channel-error" role="alert">{detailError}<button type="button" onClick={() => void read()}>{t("channels.sessions.retry")}</button></div> : null}
          {detail ? <SessionHistory key={selectedRef} result={detail} /> : !detailError ? <p role="status">{t("channels.sessions.loading")}</p> : null}
          <div className="channel-session-followup">
            {selectedSession && isResumable(selectedSession) ? <p className="channel-session-empty">{t("channels.sessions.resumeBeforeSend")}</p> : null}
            <label><span>{t("channels.sessions.followup")}</span><textarea rows={3} value={followups[selectedRef] ?? ""} disabled={busy} onChange={(event) => { const value = event.currentTarget.value; setFollowups((current) => ({ ...current, [selectedRef]: value })); }} /></label>
            <button className="channel-session-primary" type="submit" disabled={busy || !followups[selectedRef]?.trim() || Boolean(selectedSession && isResumable(selectedSession))}>{t("channels.sessions.send")}</button>
          </div>
        </div> : <>
          <div className="channel-sessions-toolbar"><span>{t("channels.sessions.count", { count: sessions.length, running: activeCount })}</span><button type="button" onClick={beginCreate} disabled={agents.length === 0}><Plus className="icon" />{t("channels.sessions.new")}</button></div>
          {sessions.length > 8 ? <label className="channel-sessions-search"><Search className="icon" /><input aria-label={t("channels.sessions.search")} placeholder={t("channels.sessions.search")} value={query} onChange={(event) => setQuery(event.currentTarget.value)} /></label> : null}
          {loading ? <p role="status">{t("channels.sessions.loading")}</p> : sessions.length === 0 ? <p className="channel-session-empty">{t("channels.sessions.empty")}</p> : null}
          {[...groups].map(([identity, entries]) => {
            const agent = agents.find((candidate) => candidate.id === identity);
            return <section className="channel-session-group" key={identity} aria-label={agent?.name || identity}>
              <header>{agent ? <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} status="idle" /> : null}<strong>{agent?.name || t("channels.sessions.coordination")}</strong><span>{entries.length}</span></header>
              {entries.slice(0, visibleCounts[identity] ?? 50).map((session) => <button className="channel-session-row" type="button" key={session.session_ref} onClick={() => { setSelectedRef(session.session_ref); setDetail(null); }}>
                <div><strong>{session.title || session.objective || t("channels.sessions.untitled")}</strong><span className={`channel-session-state ${session.state}`}>{t(`channels.sessions.state.${session.state}`)}</span></div>
                {session.objective && session.objective !== session.title ? <p>{session.objective}</p> : null}
                <div className="channel-session-meta"><span>{[session.provider, session.model].filter(Boolean).join(" · ") || t("channels.sessions.identityModel")}</span>{!roomId && session.room_id ? <span>{rooms.find((room) => room.id === session.room_id)?.name}</span> : null}<time dateTime={session.updated_at}>{formatDate(session.updated_at, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</time></div>
                {session.failure_reason ? <p className="channel-error">{session.failure_reason}</p> : null}
              </button>)}
              {entries.length > (visibleCounts[identity] ?? 50) ? <button type="button" onClick={() => setVisibleCounts((current) => ({ ...current, [identity]: (current[identity] ?? 50) + 50 }))}>{t("channels.sessions.more", { count: entries.length - (visibleCounts[identity] ?? 50) })}</button> : null}
            </section>;
          })}
          {!loading && sessions.length > 0 && groups.size === 0 ? <p className="channel-session-empty">{t("channels.sessions.noMatches")}</p> : null}
        </>}
      </div>}
    />
  </>;
}
