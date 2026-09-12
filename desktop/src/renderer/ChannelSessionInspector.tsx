import { ArrowDown, ArrowLeft, PanelRightClose } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useAutoFollowScrollContainer } from "./AutoFollowScroll";
import { ConversationTurnList } from "./ConversationTurnList";
import { latestAgentMessageItemID, TurnView } from "./TurnView";
import { useI18n } from "./i18n";
import { useChannelSession } from "./useChannelSession";
import type { NamedAgent } from "../shared/protocol";
import { AgentIdentityContext } from "./AgentIdentityContext";

export function ChannelSessionInspector({ sessionRef, turnID, name, agents, overlay = false, closing = false, onBack, onClose }: {
  sessionRef: string;
  turnID?: string;
  name: string;
  agents?: NamedAgent[];
  overlay?: boolean;
  closing?: boolean;
  onBack?: () => void;
  onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [retry, setRetry] = useState(0);
  const { detail, error, streamStatus } = useChannelSession(sessionRef, retry, !closing);
  const [scrolledAway, setScrolledAway] = useState(false);
  const scroll = useAutoFollowScrollContainer({ observeKey: sessionRef });
  const history = scroll.scrollRef;
  const closeButton = useRef<HTMLButtonElement>(null);
  const positioned = useRef(false);
  const anchorTurn = useRef<string | undefined>(turnID);

  useEffect(() => { closeButton.current?.focus({ preventScroll: true }); }, []);
  useLayoutEffect(() => {
    positioned.current = false;
    anchorTurn.current = turnID;
    if (turnID) scroll.pauseAutoFollow();
    else scroll.scrollToBottom({ force: true });
  }, [sessionRef, turnID]);

  const turns = detail?.thread.turns ?? [];
  const targetTurn = turnID ? turns.find((turn) => turn.id === turnID) : undefined;
  const statusTurn = targetTurn ?? turns.at(-1);
  const status = statusTurn ? t(`agent.status.${statusTurn.status === "in_progress" ? "running" : statusTurn.status === "interrupted" ? "cancelled" : statusTurn.status}`) : detail ? t(`channels.sessions.state.${detail.session.state}`) : "";
  const followLatest = scroll.scrollToBottom;
  const alignAnchor = () => {
    const node = history.current;
    if (!node || !anchorTurn.current) return;
    const target = Array.from(node.querySelectorAll<HTMLElement>("[data-turn-id]")).find((item) => item.dataset.turnId === anchorTurn.current);
    if (target) node.scrollTop += target.getBoundingClientRect().top - node.getBoundingClientRect().top - 16;
  };
  useEffect(() => {
    const node = history.current;
    if (!node || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(alignAnchor);
    observer.observe(node);
    if (node.firstElementChild) observer.observe(node.firstElementChild);
    return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => {
    const node = history.current;
    if (!node || !detail) return;
    if (!positioned.current) {
      const target = turnID ? Array.from(node.querySelectorAll<HTMLElement>("[data-turn-id]")).find((item) => item.dataset.turnId === turnID) : undefined;
      if (target) {
        // Scope the lookup and scroll to this panel: the same turn can also be
        // mounted in a cached Harness conversation elsewhere in the window.
        node.scrollTop += target.getBoundingClientRect().top - node.getBoundingClientRect().top - 16;
        if (targetTurn?.status === "in_progress") {
          anchorTurn.current = undefined;
          scroll.scrollToBottom({ force: true });
        } else scroll.pauseAutoFollow();
      } else if (turnID) {
        return;
      }
      positioned.current = true;
    }
    alignAnchor();
    followLatest();
    setScrolledAway(node.scrollHeight - node.scrollTop - node.clientHeight >= 80);
  }, [detail, turnID, targetTurn]);

  return <aside inert={closing} className={`conversation-pane session-inspector-extension${closing ? " closing" : ""}`} aria-label={`${name} · ${t("channels.executionTrace")}`}
    onKeyDown={(event) => {
      if (event.key === "Escape" && !event.defaultPrevented) {
        event.preventDefault();
        event.stopPropagation();
        onClose();
      }
    }}>
    <header className="session-inspector-header">
      <button ref={closeButton} className="icon-button channel-sessions-close" type="button"
        aria-label={t(overlay ? "channels.backToChat" : "common.close")} onClick={onClose}>
        {overlay ? <ArrowLeft className="icon" /> : <PanelRightClose className="icon" />}
      </button>
      {onBack ? <button type="button" className="icon-button" aria-label={t("channels.sessions.back")} onClick={onBack}><ArrowLeft className="icon" /></button> : null}
      <strong>{name}</strong>
      {status ? <div className="channel-session-meta">{status}</div> : null}
    </header>
    {error ? <div className="channel-error" role="alert">{error}<button type="button" onClick={() => setRetry((value) => value + 1)}>{t("channels.sessions.retry")}</button></div> : null}
    {!detail && !error ? <p role="status">{t("channels.sessions.loading")}</p> : null}
    {detail && turnID && !targetTurn ? <p role="status">{t("channels.traceTurnUnavailable")}</p> : null}
    {detail && !turns.length ? <p>{t("channels.sessions.noHistory")}</p> : null}
    <div className="scroll-region session-inspector-history" ref={history} tabIndex={0}
      onWheelCapture={() => { anchorTurn.current = undefined; }}
      onPointerDownCapture={() => { anchorTurn.current = undefined; }}
      onTouchStartCapture={() => { anchorTurn.current = undefined; }}
      onKeyDownCapture={(event) => {
        if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); onClose(); }
        if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "].includes(event.key)) anchorTurn.current = undefined;
      }} onScroll={(event) => {
      const node = event.currentTarget;
      setScrolledAway(node.scrollHeight - node.scrollTop - node.clientHeight >= 80);
    }}>
      <AgentIdentityContext.Provider value={agents?.find(agent => agent.id === (detail?.session.named_agent_id || detail?.session.principal_id))}>
        <div className="conversation-width session-flow">
          <ConversationTurnList threadID={sessionRef} turns={turns} forcedFullTurnIDs={turnID ? [turnID] : undefined} renderTurn={(turn) => (
            <TurnView turn={turn} threadID={sessionRef} cwd={detail?.thread.cwd}
              latestAgentMessageID={latestAgentMessageItemID(turns)} isLatestTurn={turn.id === turns.at(-1)?.id}
              streamStatus={streamStatus[turn.id]} onStreamFrame={followLatest} onCollapseComplete={followLatest} />
          )} />
        </div>
      </AgentIdentityContext.Provider>
    </div>
    {scrolledAway ? <button type="button" className="session-inspector-latest" onClick={() => {
      anchorTurn.current = undefined;
      scroll.scrollToBottom({ force: true });
      setScrolledAway(false);
    }}><ArrowDown className="icon" />{t("conversation.jumpToLatest")}</button> : null}
  </aside>;
}
