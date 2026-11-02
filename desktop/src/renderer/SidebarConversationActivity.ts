import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentPropsWithRef } from "react";
import {
  isThreadExecuting,
  isThreadUnread,
  sidebarThreadSortTime,
  type ThreadSummary,
} from "./AppState";
import { readFocusModality } from "./FocusModality";
import {
  moveThreadInOrder,
  persistThreadOrder,
  reconcileThreadOrder,
  storedThreadOrder,
  type ThreadDropPosition,
} from "./SidebarThreadOrder";

const RECENT_THREAD_ORDER_ID = "__wuu_recent_threads__";

const RECENT_THREAD_LIMIT = 50;
// Seed the initial list by activity. Later arrivals go before existing rows,
// whose positions stay stable as their execution and unread indicators change.
const ACTIVITY_RANK = { running: 0, attention: 1, history: 2 } as const;

/** Seed Recent by priority, then prepend new conversations in a stable order. */
export function useSidebarConversationActivity(
  threads: ThreadSummary[],
  activeThreadID: string | undefined,
  pendingThreadID: string | undefined,
  lastViewedTurnByThreadID: Readonly<Record<string, string>>,
): {
  threads: ThreadSummary[];
  interactionProps: ComponentPropsWithRef<"div">;
  onReorder: (activeThreadID: string, overThreadID: string, position: ThreadDropPosition) => void;
} {
  const [threadOrder, setThreadOrder] = useState(() => storedThreadOrder(RECENT_THREAD_ORDER_ID));
  const reconciledOrder = useMemo(() => {
    const seeded = threads.map((thread) => {
      const running = isThreadExecuting(thread);
      const unread = thread.id !== activeThreadID && thread.id !== pendingThreadID
        && isThreadUnread(thread, lastViewedTurnByThreadID[thread.id]);
      const startedAt = running
        ? thread.turns.findLast((turn) => turn.status === "in_progress")?.started_at
        : undefined;
      return {
        thread,
        rank: running ? ACTIVITY_RANK.running
          : unread ? ACTIVITY_RANK.attention : ACTIVITY_RANK.history,
        time: (startedAt ? Date.parse(startedAt) : 0) || sidebarThreadSortTime(thread, running),
      };
    }).sort((left, right) => left.rank - right.rank
      || right.time - left.time
      || left.thread.id.localeCompare(right.thread.id));
    return reconcileThreadOrder(seeded.map((row) => row.thread), threadOrder);
  }, [activeThreadID, lastViewedTurnByThreadID, pendingThreadID, threadOrder, threads]);
  // Commit membership as well as positions. A thread can arrive idle before its
  // first turn starts; freezing its activity rank would bury it below old work.
  useLayoutEffect(() => {
    const knownIDs = new Set(threadOrder);
    const newIDs = reconciledOrder.filter((id) => !knownIDs.has(id));
    // Keep saved IDs while workspace caches are empty or still loading.
    // Reconciliation filters absent rows without erasing their saved positions.
    if (newIDs.length > 0) setThreadOrder([...newIDs, ...threadOrder]);
  }, [reconciledOrder, threadOrder]);
  const ordered = useMemo(() => {
    const byID = new Map(threads.map((thread) => [thread.id, thread]));
    // Apply the cap after inserting arrivals so a full list still shows them.
    return reconciledOrder.slice(0, RECENT_THREAD_LIMIT).map((id) => byID.get(id)!);
  }, [reconciledOrder, threads]);
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
  // A newly created or selected conversation must remain reachable during a hold.
  const visibleThreads = useMemo(() => {
    if (!heldOrder) return ordered;
    const byID = new Map(threads.map((thread) => [thread.id, thread]));
    const heldIDs = new Set(heldOrder.map(thread => thread.id));
    const entering = ordered.filter(thread => !heldIDs.has(thread.id)
      && (thread.id === activeThreadID || thread.id === pendingThreadID));
    return [...entering, ...heldOrder.flatMap((thread) => {
      const current = byID.get(thread.id);
      return current ? [current] : [];
    })];
  }, [activeThreadID, heldOrder, ordered, pendingThreadID, threads]);

  return {
    threads: visibleThreads,
    onReorder: (activeThreadID, overThreadID, position) => {
      const current = visibleThreads.map((thread) => thread.id);
      const next = moveThreadInOrder(current, activeThreadID, overThreadID, position);
      if (next === current) return;
      const visibleIDs = new Set(next);
      const fullOrder = [...next, ...reconciledOrder.filter((id) => !visibleIDs.has(id))];
      setThreadOrder(fullOrder);
      persistThreadOrder(RECENT_THREAD_ORDER_ID, fullOrder);
      // A deliberate drop must take effect even while the pointer holds the list.
      const byID = new Map(visibleThreads.map((thread) => [thread.id, thread]));
      setHeldOrder((held) => held ? next.map((id) => byID.get(id)!) : undefined);
    },
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
