import { ArrowLeft, PanelRightClose } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { ChannelSessionInspector } from "./ChannelSessionInspector";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";
import type { NamedAgent } from "../shared/protocol";

export function ChannelActivityInspector({ roomID, agentID, name, agents, fallbackSessionRef, overlay, closing, onClose }: {
  roomID: string;
  agentID: string;
  name: string;
  agents?: NamedAgent[];
  fallbackSessionRef?: string;
  overlay: boolean;
  closing: boolean;
  onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
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
  if (sessionRef) return <ChannelSessionInspector key={sessionRef} sessionRef={sessionRef} name={name}
    agents={agents} overlay={overlay} closing={closing} onClose={onClose} />;
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
