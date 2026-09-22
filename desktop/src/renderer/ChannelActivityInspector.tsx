import { ArrowLeft, ArrowUpRight, PanelRightClose } from "./WuuIcons";
import { useEffect, useRef, useState } from "react";
import { ChannelSessionInspector } from "./ChannelSessionInspector";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";
import type { NamedAgent, ManagedHarnessSession } from "../shared/protocol";

export function ChannelActivityInspector({ roomID, agentID, name, agents, fallbackSessionRef, turnID, overlay, closing, onClose, onOpenSession }: {
  roomID: string;
  agentID: string;
  name: string;
  agents?: NamedAgent[];
  fallbackSessionRef?: string;
  turnID?: string;
  overlay: boolean;
  closing: boolean;
  onClose: () => void;
  onOpenSession?: (id: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [managed, setManaged] = useState<ManagedHarnessSession[]>([]);
  const [sessionRef, setSessionRef] = useState(fallbackSessionRef);
  const [loading, setLoading] = useState(!fallbackSessionRef);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const closeButton = useRef<HTMLButtonElement>(null);
  useEffect(() => { closeButton.current?.focus({ preventScroll: true }); }, []);
  useEffect(() => {
    let active = true;
    let inFlight = false;
    const read = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const result = await window.wuu!.listChannelSessions({ roomId: roomID, agentId: agentID });
        if (!active) return;
        const primary = result.sessions.find(session => session.primary) ?? (result.sessions.length === 1 ? result.sessions[0] : undefined);
        setSessionRef(primary?.session_ref ?? fallbackSessionRef);
        setManaged(result.managed_sessions ?? []);
        setError("");
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
  if (sessionRef) return <ChannelSessionInspector key={sessionRef} sessionRef={sessionRef} turnID={turnID} name={name}
    agents={agents} overlay={overlay} closing={closing} onClose={onClose}>
    {managed.length ? <nav className="channel-managed-sessions" aria-label={t("channels.sessions.managed")}>
      <span className="channel-managed-label">{t("channels.sessions.managed")}</span>
      {managed.map(session => <button key={session.session_id} type="button" className="channel-managed-session" onClick={() => onOpenSession?.(session.session_id)}>
        <span className="channel-managed-title">{session.title || session.workspace_root}</span>
        <span className="channel-session-meta">{t(`channels.sessions.control.${
          session.control?.state === "taken_over" ? "takenOver"
            : session.control?.state === "active" ? session.state
              : session.control?.state ?? "idle"
        }`)}</span>
        <ArrowUpRight className="icon" aria-hidden="true" />
      </button>)}
    </nav> : null}
  </ChannelSessionInspector>;
  return <aside inert={closing} className={`conversation-pane session-inspector-extension${closing ? " closing" : ""}`}
    aria-label={`${name} · ${t("channels.executionTrace")}`} onKeyDown={event => {
      if (event.key === "Escape" && !event.defaultPrevented) { event.preventDefault(); event.stopPropagation(); onClose(); }
    }}>
    <header className="session-inspector-header">
      <button ref={closeButton} className="icon-button channel-sessions-close" type="button" aria-label={t(overlay ? "channels.backToChat" : "common.close")} onClick={onClose}>
        {overlay ? <ArrowLeft className="icon" /> : <PanelRightClose className="icon" />}
      </button>
      <strong>{name}</strong>
    </header>
    {error ? <div className="channel-error" role="alert">{error}<button type="button" onClick={() => setRetry(n => n + 1)}>{t("channels.sessions.retry")}</button></div> : null}
    {loading ? <p role="status">{t("channels.sessions.loading")}</p> : null}
    {!loading && !error ? <p>{t("channels.sessions.noHistory")}</p> : null}
  </aside>;
}
