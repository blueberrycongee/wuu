import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentPropsWithRef } from "react";
import {
  isThreadExecuting,
  isThreadUnread,
  sidebarThreadSortTime,
  type ThreadSummary,
} from "./AppState";
import { readFocusModality } from "./FocusModality";

const RECENT_THREAD_LIMIT = 50;
// Assign priority when a conversation enters this mounted sidebar. Live status
// changes update its indicators without moving it or its neighboring rows.
const ACTIVITY_RANK = { running: 0, attention: 1, history: 2 } as const;

type RecentActivity = {
  rank: number;
  time: number;
};

/** Seed Recent by priority, then keep existing conversations in a stable order. */
export function useSidebarConversationActivity(
  threads: ThreadSummary[],
  activeThreadID: string | undefined,
  pendingThreadID: string | undefined,
  lastViewedTurnByThreadID: Readonly<Record<string, string>>,
): { threads: ThreadSummary[]; interactionProps: ComponentPropsWithRef<"div"> } {
  const committedActivity = useRef(new Map<string, RecentActivity>());
  const activity = useMemo(() => {
    const rows = new Map<string, RecentActivity>();
    const ordered = threads.map((thread) => {
      let position = committedActivity.current.get(thread.id);
      if (!position) {
        const running = isThreadExecuting(thread);
        const unread = thread.id !== activeThreadID && thread.id !== pendingThreadID
          && isThreadUnread(thread, lastViewedTurnByThreadID[thread.id]);
        const startedAt = running
          ? thread.turns.findLast((turn) => turn.status === "in_progress")?.started_at
          : undefined;
        position = {
          rank: running ? ACTIVITY_RANK.running
            : unread ? ACTIVITY_RANK.attention : ACTIVITY_RANK.history,
          time: (startedAt ? Date.parse(startedAt) : 0) || sidebarThreadSortTime(thread, running),
        };
      }
      rows.set(thread.id, position);
      return { thread, ...position };
    }).sort((left, right) => left.rank - right.rank
      || right.time - left.time
      || left.thread.id.localeCompare(right.thread.id));
    return { rows, ordered: ordered.slice(0, RECENT_THREAD_LIMIT).map((row) => row.thread) };
  }, [activeThreadID, lastViewedTurnByThreadID, pendingThreadID, threads]);
  // An abandoned render must not assign a conversation's initial position.
  useLayoutEffect(() => { committedActivity.current = activity.rows; }, [activity]);
  const ordered = activity.ordered;
  const [heldOrder, setHeldOrder] = useState<ThreadSummary[]>();
  const interaction = useRef({ pointer: false, keyboard: false, dragging: false });
  const listRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!heldOrder) return;
    // Removing the focused row does not dispatch React blur. Do not retain a
    // keyboard hold after pin/archive, or any hold after the list disappears.
    if (!listRef.current) {
      interaction.current = { pointer: false, keyboard: false, dragging: false };
      setHeldOrder(undefined);
    } else if (interaction.current.keyboard && document.activeElement === document.body) {
      interaction.current.keyboard = false;
      if (!interaction.current.pointer && !interaction.current.dragging) setHeldOrder(undefined);
    }
  }, [heldOrder, threads]);

  function hold(kind: keyof typeof interaction.current, value: boolean): void {
    interaction.current[kind] = value;
    const busy = Object.values(interaction.current).some(Boolean);
    setHeldOrder((current) => busy ? current ?? ordered : undefined);
  }

  // Hold membership and positions, not row data: read receipts and execution
  // indicators still update immediately. Removed/archived/pinned rows disappear.
  const visibleThreads = useMemo(() => {
    if (!heldOrder) return ordered;
    const byID = new Map(threads.map((thread) => [thread.id, thread]));
    return heldOrder.flatMap((thread) => {
      const current = byID.get(thread.id);
      return current ? [current] : [];
    });
  }, [heldOrder, ordered, threads]);

  return {
    threads: visibleThreads,
    interactionProps: {
      ref: listRef,
      onPointerEnter: (event) => {
        if (event.pointerType === "mouse" || event.pointerType === "pen") hold("pointer", true);
      },
      onPointerLeave: () => hold("pointer", false),
      onPointerDownCapture: () => hold("keyboard", false),
      onFocusCapture: () => {
        if (readFocusModality() === "keyboard") hold("keyboard", true);
      },
      onKeyDownCapture: () => hold("keyboard", true),
      onBlurCapture: (event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) hold("keyboard", false);
      },
      onDragStartCapture: () => hold("dragging", true),
      onDragEndCapture: () => hold("dragging", false),
      onDropCapture: () => hold("dragging", false),
    },
  };
}
