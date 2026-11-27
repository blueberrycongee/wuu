import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { Thread } from "../shared/protocol";
import { ConversationTurnList } from "./ConversationTurnList";
import { ConversationRenderActivityProvider } from "./ConversationRenderActivity";
import { useAutoFollowScrollContainer } from "./AutoFollowScroll";
import { isTouchWebShell } from "./ComposerFocus";
import { latestAgentMessageItemID, TurnView } from "./TurnView";
import { useI18n } from "./i18n";
import { ChevronDown, ChevronRight } from "./WuuIcons";

export function SideChatPanel({ active, thread, loading, composer, onOpenFile }: {
  active: boolean;
  thread?: Thread;
  loading: boolean;
  composer: ReactNode;
  onOpenFile(path: string): void;
}): JSX.Element {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const panelRef = useRef<HTMLElement>(null);
  const footerRef = useRef<HTMLDivElement>(null);
  const bodyScroll = useAutoFollowScrollContainer({ open: active });
  const readingKey = thread ? `wuu.desktop.sideChatReading.v1:${thread.id}` : undefined;
  const restoredKey = useRef<string | undefined>(undefined);
  const writtenReadingState = useRef("");
  const restoreScrollPosition = bodyScroll.restoreScrollPosition;
  useLayoutEffect(() => {
    if (!active || !readingKey || restoredKey.current === readingKey) return;
    restoredKey.current = readingKey;
    try {
      const recovery = JSON.parse(window.localStorage.getItem(readingKey) ?? "null");
      if (!recovery) return;
      setExpanded(recovery.expanded === true);
      if (!recovery.follow && typeof recovery.scrollTop === "number" && Number.isFinite(recovery.scrollTop)) {
        restoreScrollPosition(recovery.scrollTop, recovery.follow === true);
      }
    } catch { /* Storage is optional. */ }
  }, [active, readingKey, restoreScrollPosition]);
  const saveReadingPosition = (foldOpen = expanded) => {
    const scroll = bodyScroll.scrollRef.current;
    if (!active || !readingKey || !scroll) return;
    try {
      const follow = bodyScroll.autoFollowRef.current;
      const readingState = JSON.stringify({ expanded: foldOpen, scrollTop: follow ? 0 : scroll.scrollTop, follow });
      if (readingState !== writtenReadingState.current) {
        window.localStorage.setItem(readingKey, readingState);
        writtenReadingState.current = readingState;
      }
    } catch { /* Storage is optional. */ }
  };
  const inheritedThrough = thread?.inherited_through_seq ?? 0;
  const turns = useMemo(() => {
    if (!thread) return [];
    if (expanded || !inheritedThrough) return thread.turns;
    return thread.turns.filter(turn => {
      const first = turn.items.find(item => item.seq && item.type === "user_message") ?? turn.items.find(item => item.seq);
      return !first?.seq || first.seq > inheritedThrough;
    });
  }, [expanded, inheritedThrough, thread?.turns]);
  const latestMessageID = useMemo(() => latestAgentMessageItemID(turns), [turns]);

  useEffect(() => {
    if (!active || loading || isTouchWebShell()) return;
    const frame = window.requestAnimationFrame(() => footerRef.current?.querySelector<HTMLElement>('[data-wuu-component="composer-input"]')?.focus({ preventScroll: true }));
    return () => window.cancelAnimationFrame(frame);
  }, [active, loading]);
  useLayoutEffect(() => {
    const panel = panelRef.current;
    const footer = footerRef.current;
    if (!active || !panel || !footer) return;
    const measure = () => {
      panel.style.setProperty("--side-thread-footer-height", `${footer.getBoundingClientRect().height}px`);
      bodyScroll.scheduleScrollToBottom();
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(footer);
    return () => observer.disconnect();
  }, [active, bodyScroll]);
  useEffect(() => {
    if (active) bodyScroll.scheduleScrollToBottom();
  }, [active, bodyScroll, turns]);
  const onStreamFrame = useCallback(() => bodyScroll.scheduleScrollToBottom(), [bodyScroll]);

  return <aside ref={panelRef} className="side-thread-panel" data-thread-id={thread?.id} data-wuu-component="side-chat" aria-label={t("sideThread.title")}>
    <div ref={bodyScroll.scrollRef} className="side-thread-panel__body" role="log" aria-busy={loading} onScroll={() => saveReadingPosition()}>
      <ConversationRenderActivityProvider active={active}>
        <div className="conversation-width session-flow side-thread-panel__conversation">
          {inheritedThrough > 0 && <button className="side-chat-inherited-toggle" type="button" aria-expanded={expanded}
            onClick={() => { setExpanded(!expanded); saveReadingPosition(!expanded); }}>
            {expanded ? <ChevronDown className="icon-xs" aria-hidden="true" /> : <ChevronRight className="icon-xs" aria-hidden="true" />}
            {t(expanded ? "sideThread.hideInherited" : "sideThread.showInherited")}
          </button>}
          {thread && <ConversationTurnList threadID={thread.id} historyCursor={expanded || !inheritedThrough ? thread.history_cursor : undefined} turns={turns} renderTurn={turn =>
            <TurnView threadID={thread.id} turn={turn} cwd={thread.cwd} onOpenFile={onOpenFile}
              latestAgentMessageID={latestMessageID} onStreamFrame={onStreamFrame} isLatestTurn={turn.id === turns.at(-1)?.id} />} />}
        </div>
      </ConversationRenderActivityProvider>
    </div>
    <div ref={footerRef} className="side-thread-panel__footer">
      <div className="side-thread-panel__composer-host">{composer}</div>
    </div>
  </aside>;
}
