import { PanelRightClose } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { ChannelSessionReadResult } from "../shared/protocol";
import { ConversationTurnList } from "./ConversationTurnList";
import { latestAgentMessageItemID, TurnView } from "./TurnView";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";

export function ChannelSessionInspector({ sessionRef, name, left, width, onClose }: {
  sessionRef: string;
  name: string;
  left: number;
  width: number;
  onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [detail, setDetail] = useState<ChannelSessionReadResult | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const history = useRef<HTMLDivElement>(null);
  const follow = useRef(true);

  useEffect(() => {
    let active = true;
    let inFlight = false;
    let pending = false;
    let timer: number | undefined;
    setDetail(null);
    setError("");
    follow.current = true;
    const schedule = () => {
      if (!active || timer !== undefined) return;
      timer = window.setTimeout(() => { timer = undefined; void read(); }, 200);
    };
    const read = async () => {
      if (!active) return;
      if (inFlight) { pending = true; return; }
      inFlight = true;
      try {
        const result = await window.wuu!.readChannelSession({ sessionRef });
        if (active) { setDetail(result); setError(""); }
      } catch (reason) {
        if (active) setError(toastErrorMessage(reason));
      } finally {
        inFlight = false;
        if (active && pending) { pending = false; schedule(); }
      }
    };
    void read();
    const interval = window.setInterval(() => { if (document.visibilityState !== "hidden") void read(); }, 2_000);
    const off = window.wuu?.onServerEvent?.((event) => {
      if (event.kind !== "notification") return;
      const { method, params } = event.message;
      if ((params as { thread_id?: string } | undefined)?.thread_id === sessionRef && (method.startsWith("item/") || method.startsWith("turn/"))) schedule();
    });
    return () => { active = false; off?.(); window.clearInterval(interval); if (timer !== undefined) window.clearTimeout(timer); };
  }, [sessionRef, retry]);

  useEffect(() => {
    if (follow.current && history.current) history.current.scrollTop = history.current.scrollHeight;
  }, [detail]);

  const turns = detail?.thread.turns ?? [];
  const followLatest = () => { if (follow.current && history.current) history.current.scrollTop = history.current.scrollHeight; };
  return createPortal(<aside className="conversation-pane session-inspector-extension" style={{ left, width }} aria-label={`${name} · ${t("channels.sessions.history")}`}>
    <header className="session-inspector-header">
      <strong>{name}</strong>
      {detail ? <div className="channel-session-meta">{t(`channels.sessions.state.${detail.session.state}`)}</div> : null}
      <button className="icon-button channel-sessions-close" type="button" aria-label={t("common.close")} onClick={onClose}><PanelRightClose className="icon" /></button>
    </header>
      {error ? <div className="channel-error" role="alert">{error}<button type="button" onClick={() => setRetry((value) => value + 1)}>{t("channels.sessions.retry")}</button></div> : null}
      {!detail && !error ? <p role="status">{t("channels.sessions.loading")}</p> : null}
      <div className="scroll-region session-inspector-history" ref={history} onScroll={(event) => {
        const node = event.currentTarget;
        follow.current = node.scrollHeight - node.scrollTop - node.clientHeight < 80;
      }}>
        <div className="conversation-width session-flow">
          <ConversationTurnList threadID={sessionRef} turns={turns} renderTurn={(turn) => (
            <TurnView turn={turn} threadID={sessionRef} cwd={detail?.thread.cwd}
              latestAgentMessageID={latestAgentMessageItemID(turns)} isLatestTurn={turn.id === turns.at(-1)?.id}
              onStreamFrame={followLatest} onCollapseComplete={followLatest} />
          )} />
        </div>
      </div>
    </aside>, document.body);
}
