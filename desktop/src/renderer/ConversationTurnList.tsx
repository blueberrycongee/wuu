import {
  Component,
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import type { Turn } from "../shared/protocol";
import { queryTextForUserItem } from "./AppState";
import {
  TURN_LIST_COLLAPSE_THRESHOLD,
  TURN_LIST_RECENT_FULL_TURNS,
} from "./ConversationTurnPolicy";
import {
  turnAnchorID,
  turnReplySnippet,
  CONVERSATION_TURN_REVEAL_EVENT,
  type ConversationTurnRevealDetail,
  userMessageAnchorID,
} from "./TurnViewHelpers";
import { submitGlideActive, subscribeSubmitGlide } from "./AutoFollowScroll";
import { useI18n } from "./i18n";
import { ImagePreviewGallery } from "./ImagePreviewGallery";
import { useConversationRenderActive } from "./ConversationRenderActivity";
import { captureReadingAnchor, readingAnchorScrollTop, type ConversationReadingAnchor } from "./ConversationReadingAnchor";

export {
  TURN_LIST_COLLAPSE_THRESHOLD,
  TURN_LIST_RECENT_FULL_TURNS,
} from "./ConversationTurnPolicy";

type ConversationTurnListProps = {
  threadID: string;
  historyCursor?: string;
  turns: Turn[];
  renderTurn: (turn: Turn) => ReactNode;
  renderBeforeTurns?: ReactNode;
  renderAfterTurn?: (turn: Turn) => ReactNode;
  renderAfterMissingTurn?: ReactNode;
  forcedFullTurnIDs?: Iterable<string>;
  autoLoadEarlier?: boolean;
};

export const TURN_LIST_INITIAL_TAIL_TURNS = TURN_LIST_RECENT_FULL_TURNS;
export const TURN_LIST_PREPEND_BATCH_TURNS = 40;
const TURN_LIST_PRELOAD_SCROLL_TOP_PX = 160;

type TurnWindowState = {
  threadID: string;
  visibleCount: number;
  coldWindowed: boolean;
};

type PrependScrollSnapshot = {
  node: HTMLElement;
  scrollHeight: number;
  scrollTop: number;
  anchor?: ConversationReadingAnchor;
};

type HistoryAnchorProps = {
  turns: Turn[];
  active: boolean;
  loaderRef: RefObject<HTMLButtonElement | null>;
  children: ReactNode;
};

// Read the outgoing DOM immediately before React mutates it, not when the
// request starts: the reader may keep scrolling while history loads.
class HistoryAnchor extends Component<HistoryAnchorProps> {
  getSnapshotBeforeUpdate(previous: HistoryAnchorProps): PrependScrollSnapshot | null {
    const first = previous.turns[0]?.id;
    if (!previous.active || !this.props.active || !first ||
      this.props.turns[0]?.id === first || !this.props.turns.some(turn => turn.id === first)) return null;
    const loader = this.props.loaderRef.current;
    const node = loader ? turnListScrollContainer(loader) : null;
    if (!node) return null;
    return { node, scrollHeight: node.scrollHeight, scrollTop: node.scrollTop, anchor: captureReadingAnchor(node) };
  }

  componentDidUpdate(_previous: HistoryAnchorProps, _state: unknown, snapshot: PrependScrollSnapshot | null): void {
    if (!snapshot) return;
    const { node, anchor } = snapshot;
    if (anchor) {
      const top = readingAnchorScrollTop(node, anchor);
      // Native anchoring may already have compensated. Write only the residual;
      // unrelated output below the anchor never contributes to the adjustment.
      if (top !== undefined && Math.abs(top - node.scrollTop) > 0.5) node.scrollTop = top;
    } else if (Math.abs(node.scrollTop - snapshot.scrollTop) <= 1) {
      node.scrollTop += Math.max(0, node.scrollHeight - snapshot.scrollHeight);
    }
  }

  render(): ReactNode { return this.props.children; }
}

function initialTurnWindowCount(turnCount: number): number {
  if (turnCount <= TURN_LIST_COLLAPSE_THRESHOLD) {
    return turnCount;
  }
  return TURN_LIST_INITIAL_TAIL_TURNS;
}

function initialTurnWindowState(
  threadID: string,
  turnCount: number,
): TurnWindowState {
  return {
    threadID,
    visibleCount: initialTurnWindowCount(turnCount),
    coldWindowed: turnCount > TURN_LIST_COLLAPSE_THRESHOLD,
  };
}

function turnListScrollContainer(start: HTMLElement): HTMLElement | null {
  return start.closest<HTMLElement>(
    ".scroll-region, .conversation-split-body, .side-thread-panel__body",
  );
}

export function ConversationTurnList({
  threadID,
  historyCursor,
  turns,
  renderTurn,
  renderBeforeTurns,
  renderAfterTurn,
  renderAfterMissingTurn,
  forcedFullTurnIDs,
  autoLoadEarlier = true,
}: ConversationTurnListProps): JSX.Element {
  const { t, formatNumber } = useI18n();
  const renderActive = useConversationRenderActive();
  const [expandedTurnIDs, setExpandedTurnIDs] = useState<Set<string>>(
    () => new Set(),
  );
  const [turnWindow, setTurnWindow] = useState<TurnWindowState>(() =>
    initialTurnWindowState(threadID, turns.length),
  );
  const tailID = turns.at(-1)?.id;
  const [previousTail, setPreviousTail] = useState({ threadID, id: tailID });
  if (previousTail.threadID !== threadID || previousTail.id !== tailID) {
    setPreviousTail({ threadID, id: tailID });
    const previousIndex = previousTail.threadID === threadID
      ? turns.findIndex(turn => turn.id === previousTail.id) : -1;
    if (previousIndex >= 0 && previousIndex < turns.length - 1) {
      // Appending output must not evict mounted history or collapse a reply
      // above the reader. Cold mounts and explicit history loads remain bounded;
      // retain already revealed content for this mounted conversation.
      const previousCount = previousIndex + 1;
      const added = turns.length - previousCount;
      setTurnWindow(current => ({
        ...current,
        visibleCount: Math.max(current.visibleCount, initialTurnWindowCount(previousCount)) + added,
      }));
      const firstFull = previousCount <= TURN_LIST_COLLAPSE_THRESHOLD
        ? 0 : Math.max(0, previousCount - TURN_LIST_RECENT_FULL_TURNS);
      const nextFull = Math.max(0, turns.length - TURN_LIST_RECENT_FULL_TURNS);
      setExpandedTurnIDs(current => new Set([
        ...current,
        ...turns.slice(firstFull, Math.min(nextFull, previousCount)).map(turn => turn.id),
      ]));
    }
  }
  const historyLoaderRef = useRef<HTMLButtonElement | null>(null);
  const forcedFull = useMemo(
    () => new Set(forcedFullTurnIDs ?? []),
    [forcedFullTurnIDs],
  );
  const initialVisibleCount = initialTurnWindowCount(turns.length);
  const storedVisibleCount =
    turnWindow.threadID === threadID
      ? Math.min(
          Math.max(
            turnWindow.visibleCount,
            turnWindow.coldWindowed
              ? 0
              : Math.min(turns.length, TURN_LIST_COLLAPSE_THRESHOLD),
          ),
          turns.length,
        )
      : initialVisibleCount;
  const storedStartIndex = Math.max(0, turns.length - storedVisibleCount);
  let requiredStartIndex = turns.length;
  turns.forEach((turn, index) => {
    if (turn.status === "in_progress" || forcedFull.has(turn.id)) {
      requiredStartIndex = Math.min(requiredStartIndex, index);
    }
  });
  const visibleStartIndex = Math.min(storedStartIndex, requiredStartIndex);
  const visibleTurns = turns.slice(visibleStartIndex);
  const hasLocalEarlier = visibleStartIndex > 0;
  const hasRemoteEarlier = Boolean(historyCursor && window.wuu.loadEarlierThreadHistory);
  const hasEarlierTurns = hasLocalEarlier || hasRemoteEarlier;
  const loadingRemote = useRef(false);
  const [remoteBusy, setRemoteBusy] = useState(false);
  const [remoteError, setRemoteError] = useState("");

  useEffect(() => {
    setExpandedTurnIDs(new Set());
  }, [threadID]);

  useEffect(() => {
    if (turnWindow.threadID === threadID) {
      return;
    }
    setTurnWindow(initialTurnWindowState(threadID, turns.length));
  }, [threadID, turnWindow.threadID, turns.length]);

  useEffect(() => {
    setExpandedTurnIDs((current) => {
      if (current.size === 0) {
        return current;
      }
      const live = new Set(turns.map((turn) => turn.id));
      let changed = false;
      const next = new Set<string>();
      for (const id of current) {
        if (live.has(id)) {
          next.add(id);
        } else {
          changed = true;
        }
      }
      return changed ? next : current;
    });
  }, [turns]);

  const collapseOlderTurns = turns.length > TURN_LIST_COLLAPSE_THRESHOLD;
  const firstRecentFullIndex = Math.max(
    0,
    turns.length - TURN_LIST_RECENT_FULL_TURNS,
  );

  // Identity-stable expander: TurnListEntry memoizes its children on turn
  // identity, which only works if this callback never changes between renders.
  const expandTurn = useCallback((turnID: string) => {
    setExpandedTurnIDs((current) => {
      if (current.has(turnID)) {
        return current;
      }
      const next = new Set(current);
      next.add(turnID);
      return next;
    });
  }, []);

  const loadEarlierTurns = useCallback(
    () => {
      if (!hasEarlierTurns) {
        return;
      }
      const nextStartIndex = Math.max(
        0,
        visibleStartIndex - TURN_LIST_PREPEND_BATCH_TURNS,
      );
      if (!hasLocalEarlier && historyCursor && window.wuu.loadEarlierThreadHistory) {
        if (loadingRemote.current) return;
        loadingRemote.current = true; setRemoteBusy(true); setRemoteError("");
        setTurnWindow({ threadID, visibleCount: turns.length + 20, coldWindowed: true });
        void window.wuu.loadEarlierThreadHistory(threadID, historyCursor).catch(error => {
          setRemoteError(error instanceof Error ? error.message : String(error));
        }).finally(() => { loadingRemote.current = false; setRemoteBusy(false); });
        return;
      }
      setTurnWindow({
        threadID,
        visibleCount: turns.length - nextStartIndex,
        coldWindowed: turnWindow.coldWindowed,
      });
    },
    [
      hasEarlierTurns,
      hasLocalEarlier,
      historyCursor,
      threadID,
      turnWindow.coldWindowed,
      turns.length,
      visibleStartIndex,
    ],
  );

  useEffect(() => {
    const handleRevealTurn = (event: Event): void => {
      const detail = (event as CustomEvent<ConversationTurnRevealDetail>).detail;
      const turnIndex = turns.findIndex((turn) => turn.id === detail?.turnID);
      if (turnIndex < 0 || turnIndex >= visibleStartIndex) {
        return;
      }
      setTurnWindow({
        threadID,
        visibleCount: turns.length - turnIndex,
        coldWindowed: turnWindow.coldWindowed,
      });
    };
    window.addEventListener(CONVERSATION_TURN_REVEAL_EVENT, handleRevealTurn);
    return () =>
      window.removeEventListener(CONVERSATION_TURN_REVEAL_EVENT, handleRevealTurn);
  }, [threadID, turnWindow.coldWindowed, turns, visibleStartIndex]);

  useEffect(() => {
    // A failed page remains retryable on the next scroll; keep the error visible
    // until that attempt, without starting an automatic retry loop.
    if (!autoLoadEarlier || !hasEarlierTurns || remoteBusy) {
      return;
    }
    const loader = historyLoaderRef.current;
    const node = loader ? turnListScrollContainer(loader) : null;
    if (!node) {
      return;
    }
    const loadIfNearTop = (): void => {
      // Reading scrollTop on every glide frame forces a layout. Preload still
      // runs once the glide settles.
      if (submitGlideActive()) return;
      if (node.scrollTop <= TURN_LIST_PRELOAD_SCROLL_TOP_PX) {
        loadEarlierTurns();
      }
    };
    const unsubscribeGlide = subscribeSubmitGlide(loadIfNearTop);
    node.addEventListener("scroll", loadIfNearTop, { passive: true });
    return () => {
      unsubscribeGlide();
      node.removeEventListener("scroll", loadIfNearTop);
    };
  }, [autoLoadEarlier, hasEarlierTurns, loadEarlierTurns, remoteBusy]);

  return (
    <ImagePreviewGallery key={threadID}>
    <HistoryAnchor turns={visibleTurns} active={renderActive} loaderRef={historyLoaderRef}>
      {hasEarlierTurns ? (
        <button
          disabled={remoteBusy}
          className="conversation-turn-history-loader"
          onClick={() => loadEarlierTurns()}
          ref={historyLoaderRef}
          type="button"
        >
          {remoteBusy ? t("common.loadingEllipsis") : !hasLocalEarlier ? t("turn.loadEarlierHistory") : t("turn.loadEarlier", {
            count: formatNumber(
              Math.min(visibleStartIndex, TURN_LIST_PREPEND_BATCH_TURNS),
            ),
          })}
        </button>
      ) : (
        renderBeforeTurns
      )}
      {remoteError ? <div role="alert">{remoteError}</div> : null}
      {visibleTurns.map((turn, visibleIndex) => {
        const index = visibleStartIndex + visibleIndex;
        const full =
          !collapseOlderTurns ||
          index >= firstRecentFullIndex ||
          turn.status === "in_progress" ||
          expandedTurnIDs.has(turn.id) ||
          forcedFull.has(turn.id);
        return (
          <TurnListEntry
            key={turn.id}
            turn={turn}
            full={full}
            onExpand={expandTurn}
            renderTurn={renderTurn}
            renderAfterTurn={renderAfterTurn}
          />
        );
      })}
      {renderAfterMissingTurn}
    </HistoryAnchor>
    </ImagePreviewGallery>
  );
}

const TurnListEntry = memo(function TurnListEntry({
  turn,
  full,
  onExpand,
  renderTurn,
  renderAfterTurn,
}: {
  turn: Turn;
  full: boolean;
  onExpand: (turnID: string) => void;
  renderTurn: (turn: Turn) => ReactNode;
  renderAfterTurn?: (turn: Turn) => ReactNode;
}): JSX.Element {
  // CollapsedTurnView is memoized on turn identity; the expand callback must be
  // equally stable or it defeats that memo on every list render.
  const handleExpand = useCallback(
    () => onExpand(turn.id),
    [onExpand, turn.id],
  );
  return (
    <>
      {full ? renderTurn(turn) : <CollapsedTurnView turn={turn} onExpand={handleExpand} />}
      {renderAfterTurn?.(turn)}
    </>
  );
});

// Memoized on turn identity: server events rebuild the thread object but
// keep untouched turns referentially equal, so hundreds of collapsed rows
// skip re-rendering (and skip re-running their snippet scans) on each event.
const CollapsedTurnView = memo(function CollapsedTurnView({
  turn,
  onExpand,
}: {
  turn: Turn;
  onExpand: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const firstUserItem = turn.items.find(
    (item) => item.type === "user_message" && queryTextForUserItem(item),
  );
  const queryText = firstUserItem
    ? queryTextForUserItem(firstUserItem) || t("turn.noUserPreview")
    : t("turn.noUserPreview");
  const reply = turnReplySnippet(turn)?.text;

  return (
    <section
      className="turn turn-collapsed"
      id={turnAnchorID(turn.id)}
      data-turn-id={turn.id}
      data-turn-status={turn.status}
    >
      <button
        className="turn-collapsed-button"
        type="button"
        onClick={onExpand}
        aria-label={t("turn.expandConversationTurn")}
      >
        <span className="turn-collapsed-marker" aria-hidden="true" />
        <span className="turn-collapsed-copy">
          <span
            id={
              firstUserItem
                ? userMessageAnchorID(turn.id, firstUserItem.id)
                : undefined
            }
            className="turn-collapsed-query"
          >
            {compactPreview(queryText)}
          </span>
          {reply ? (
            <span className="turn-collapsed-reply">
              {compactPreview(reply)}
            </span>
          ) : null}
        </span>
        <span className="turn-collapsed-action">{t("common.expand")}</span>
      </button>
    </section>
  );
});

function compactPreview(text: string, maxLength = 180): string {
  const compact = text.replace(/\s+/g, " ").trim();
  if (compact.length <= maxLength) {
    return compact;
  }
  return `${compact.slice(0, maxLength - 1)}…`;
}
