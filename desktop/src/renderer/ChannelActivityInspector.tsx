import { ArrowLeft, ChevronRight, PanelRightClose } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { CollaborationSessionBinding } from "../shared/protocol";
import { ChannelSessionInspector } from "./ChannelSessionInspector";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";

export function ChannelActivityInspector({ roomID, agentID, name, fallbackSessionRef, overlay, closing, onClose }: {
  roomID: string;
  agentID: string;
  name: string;
  fallbackSessionRef?: string;
  overlay: boolean;
  closing: boolean;
  onClose: () => void;
}): JSX.Element {
  const { t, formatDate } = useI18n();
  const [sessions, setSessions] = useState<CollaborationSessionBinding[]>([]);
  const [selected, setSelected] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const [resuming, setResuming] = useState("");
  const [limit, setLimit] = useState(20);
  const initialized = useRef(false);
  const closeButton = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (!selected) closeButton.current?.focus({ preventScroll: true }); }, [selected]);
  useEffect(() => {
    let active = true;
    let inFlight = false;
    const read = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const result = await window.wuu!.listChannelSessions({ roomId: roomID, agentId: agentID });
        if (!active) return;
        const rank = (s: CollaborationSessionBinding) => ["running", "starting", "waiting", "queued"].includes(s.state) ? 0 : 1;
        const items = [...result.sessions].sort((a, b) => rank(a) - rank(b) || b.updated_at.localeCompare(a.updated_at) || a.session_ref.localeCompare(b.session_ref));
        setSessions(items);
        setError("");
        if (!initialized.current) {
          initialized.current = true;
          if (items.length === 1) setSelected(items[0].session_ref);
          else if (!items.length && fallbackSessionRef) setSelected(fallbackSessionRef);
        }
      } catch (reason) {
        if (active) setError(toastErrorMessage(reason));
      } finally {
        inFlight = false;
        if (active) setLoading(false);
      }
    };
    void read();
    const timer = window.setInterval(() => { if (document.visibilityState !== "hidden") void read(); }, 2_000);
    return () => { active = false; window.clearInterval(timer); };
  }, [roomID, agentID, fallbackSessionRef, retry]);
  const selectedTitle = sessions.find(session => session.session_ref === selected)?.title;
  const resume = async (sessionRef: string) => {
    if (resuming) return;
    setResuming(sessionRef);
    try { await window.wuu!.resumeChannelSession({ sessionRef }); setRetry(n => n + 1); }
    catch (reason) { setError(toastErrorMessage(reason)); }
    finally { setResuming(""); }
  };
  if (selected) return <ChannelSessionInspector key={selected} sessionRef={selected} name={selectedTitle && selectedTitle !== name ? `${name} · ${selectedTitle}` : name}
    overlay={overlay} closing={closing} onClose={onClose}
    onBack={sessions.length > 1 ? () => setSelected(undefined) : undefined} />;
  return <aside inert={closing} className={`conversation-pane session-inspector-extension${closing ? " closing" : ""}`}
    aria-label={`${name} · ${t("channels.sessions.title")}`} onKeyDown={event => {
      if (event.key === "Escape" && !event.defaultPrevented) { event.preventDefault(); event.stopPropagation(); onClose(); }
    }}>
    <header className="session-inspector-header">
      <button ref={closeButton} className="icon-button channel-sessions-close" type="button" aria-label={t(overlay ? "channels.backToChat" : "common.close")} onClick={onClose}>
        {overlay ? <ArrowLeft className="icon" /> : <PanelRightClose className="icon" />}
      </button>
      <strong>{name}</strong><span className="channel-session-meta">{t("channels.sessions.title")}</span>
    </header>
    {error ? <div className="channel-error" role="alert">{error}<button type="button" onClick={() => setRetry(n => n + 1)}>{t("channels.sessions.retry")}</button></div> : null}
    {loading ? <p role="status">{t("channels.sessions.loading")}</p> : null}
    {!loading && !error && !sessions.length ? <p>{t("channels.sessions.noHistory")}</p> : null}
    <div className="scroll-region channel-activity-session-list">
      {sessions.slice(0, limit).map(session => <div className="channel-activity-session-row" key={session.session_ref}><button type="button" title={session.session_ref}
        className="channel-activity-session-entry" onClick={() => setSelected(session.session_ref)}>
        <span><strong>{session.title || session.objective || t("channels.sessions.untitled")}</strong>
          {session.objective && session.objective !== session.title ? <span className="channel-activity-session-objective">{session.objective}</span> : null}
          <small>{t(`channels.sessions.state.${session.state}`)} · {formatDate(session.updated_at, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</small>
        </span><ChevronRight className="icon" />
      </button>
        {session.state === "failed" || session.state === "interrupted" ? <button className="channel-activity-session-resume" type="button" disabled={!!resuming} onClick={() => void resume(session.session_ref)}>{t(session.state === "failed" ? "channels.sessions.retry" : "channels.sessions.resume")}</button> : null}
      </div>)}
      {sessions.length > limit ? <button type="button" onClick={() => setLimit(n => n + 20)}>{t("channels.sessions.earlier")}</button> : null}
    </div>
  </aside>;
}
