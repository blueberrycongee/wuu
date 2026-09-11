import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
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
import { useI18n } from "./i18n";

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
};

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
  const [expandedTurnIDs, setExpandedTurnIDs] = useState<Set<string>>(
    () => new Set(),
  );
  const [turnWindow, setTurnWindow] = useState<TurnWindowState>(() =>
    initialTurnWindowState(threadID, turns.length),
  );
  const historyLoaderRef = useRef<HTMLButtonElement | null>(null);
  const prependScrollSnapshotRef = useRef<PrependScrollSnapshot | null>(null);
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

  useLayoutEffect(() => {
    const snapshot = prependScrollSnapshotRef.current;
    if (!snapshot) {
      return;
    }
    prependScrollSnapshotRef.current = null;
    const addedHeight = snapshot.node.scrollHeight - snapshot.scrollHeight;
    snapshot.node.scrollTop = snapshot.scrollTop + Math.max(0, addedHeight);
  }, [visibleStartIndex, turns.length]);

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
    (preserveScrollPosition = true) => {
      if (!hasEarlierTurns) {
        return;
      }
      const nextStartIndex = Math.max(
        0,
        visibleStartIndex - TURN_LIST_PREPEND_BATCH_TURNS,
      );
      if (preserveScrollPosition) {
        const loader = historyLoaderRef.current;
        const node = loader ? turnListScrollContainer(loader) : null;
        if (node) {
          prependScrollSnapshotRef.current = {
            node,
            scrollHeight: node.scrollHeight,
            scrollTop: node.scrollTop,
          };
        }
      }
      if (!hasLocalEarlier && historyCursor && window.wuu.loadEarlierThreadHistory) {
        if (loadingRemote.current) return;
        loadingRemote.current = true; setRemoteBusy(true); setRemoteError("");
        setTurnWindow({ threadID, visibleCount: turns.length + 20, coldWindowed: true });
        void window.wuu.loadEarlierThreadHistory(threadID, historyCursor).catch(error => {
          prependScrollSnapshotRef.current = null;
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
      prependScrollSnapshotRef.current = null;
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
      if (node.scrollTop <= TURN_LIST_PRELOAD_SCROLL_TOP_PX) {
        loadEarlierTurns(true);
      }
    };
    node.addEventListener("scroll", loadIfNearTop, { passive: true });
    return () => node.removeEventListener("scroll", loadIfNearTop);
  }, [autoLoadEarlier, hasEarlierTurns, loadEarlierTurns, remoteBusy]);

  return (
    <>
      {hasEarlierTurns ? (
        <button
          disabled={remoteBusy}
          className="conversation-turn-history-loader"
          onClick={() => loadEarlierTurns(true)}
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
    </>
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
