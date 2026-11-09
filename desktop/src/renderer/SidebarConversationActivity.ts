import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentPropsWithRef } from "react";
import {
  isThreadExecuting,
  isThreadUnread,
  sidebarThreadSortTime,
  threadBelongsToWorkspace,
  type ThreadSummary,
} from "./AppState";
import type { DesktopProject } from "../shared/protocol";
import { recentViewFilters, type RecentView } from "./SidebarRecentViewMenu";
import { readFocusModality } from "./FocusModality";
import {
  moveThreadInOrder,
  persistThreadOrder,
  reconcileThreadArrivalOrder,
  type ThreadArrivalOrder,
  storedThreadOrder,
  type ThreadDropPosition,
} from "./SidebarThreadOrder";

const RECENT_THREAD_ORDER_ID = "__wuu_recent_threads__";

const RECENT_THREAD_LIMIT = 50;
const RECENT_ARRIVAL_ORDER_KEY = "wuu.desktop.recentThreadArrivalOrder.v1";

function readRecentOrder(): ThreadArrivalOrder {
  try {
    const stored = JSON.parse(window.localStorage.getItem(RECENT_ARRIVAL_ORDER_KEY) || "null") as ThreadArrivalOrder | null;
    if (stored && Array.isArray(stored.ids) && stored.ids.every(id => typeof id === "string")
      && Number.isFinite(stored.newestCreatedAt)) return stored;
  } catch { /* Preserve legacy manual ordering when the saved snapshot is unavailable. */ }
  return { ids: storedThreadOrder(RECENT_THREAD_ORDER_ID), newestCreatedAt: 0 };
}

function persistRecentOrder(order: ThreadArrivalOrder): void {
  try { window.localStorage.setItem(RECENT_ARRIVAL_ORDER_KEY, JSON.stringify(order)); }
  catch { /* Blocked storage must not prevent in-memory navigation. */ }
}
// Seed the initial list by activity. Newly created sessions precede known rows;
// late historical pages follow them without changing their existing positions.
const ACTIVITY_RANK = { running: 0, attention: 1, history: 2 } as const;

/**
 * Preserve Recent order across activity updates, delayed catalogs, and reloads.
 * The view sorts and filters the remembered order without rewriting it, so
 * hidden conversations keep their places when a filter is cleared.
 */
export function useSidebarConversationActivity(
  threads: ThreadSummary[],
  activeThreadID: string | undefined,
  pendingThreadID: string | undefined,
  lastViewedTurnByThreadID: Readonly<Record<string, string>>,
  view: RecentView,
  workspace: DesktopProject | undefined,
  folderByThreadID: Readonly<Record<string, string>>,
): {
  threads: ThreadSummary[];
  interactionProps: ComponentPropsWithRef<"div">;
  /** Absent while the view sorts by time, where a dropped row could not stay put. */
  onReorder?: (activeThreadID: string, overThreadID: string, position: ThreadDropPosition) => void;
} {
  const [arrivalOrder, setArrivalOrder] = useState(readRecentOrder);
  const reconciledArrivalOrder = useMemo(() => {
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
    return reconcileThreadArrivalOrder(seeded.map((row) => row.thread), arrivalOrder);
  }, [activeThreadID, arrivalOrder, lastViewedTurnByThreadID, pendingThreadID, threads]);
  const reconciledOrder = useMemo(() => {
    const visibleIDs = new Set(threads.map(thread => thread.id));
    return reconciledArrivalOrder.ids.filter(id => visibleIDs.has(id));
  }, [reconciledArrivalOrder, threads]);
  // Preserve automatic arrivals across reloads, not just manual drag order.
  // Absent IDs remain remembered while catalogs load; visibility uses live rows.
  useLayoutEffect(() => {
    if (reconciledArrivalOrder === arrivalOrder) return;
    setArrivalOrder(reconciledArrivalOrder);
    persistRecentOrder(reconciledArrivalOrder);
  }, [arrivalOrder, reconciledArrivalOrder]);
  const ordered = useMemo(() => {
    const byID = new Map(threads.map((thread) => [thread.id, thread]));
    const sorted = view.sort === "manual"
      ? reconciledOrder.map((id) => byID.get(id)!)
      : threads.map((thread) => ({
        thread,
        time: view.sort === "created" ? sidebarThreadSortTime(thread, true) : sidebarThreadSortTime(thread, false),
      })).sort((left, right) => right.time - left.time || left.thread.id.localeCompare(right.thread.id))
        .map((row) => row.thread);
    // The open conversation stays listed under a status filter so reading it
    // does not pull the row out from under the pointer.
    const matches = (thread: ThreadSummary) => (view.includeFoldered || !folderByThreadID[thread.id])
      && (!workspace || threadBelongsToWorkspace(thread, workspace))
      && (view.status === "all" || thread.id === activeThreadID || thread.id === pendingThreadID
        || (view.status === "running"
          ? isThreadExecuting(thread)
          : isThreadUnread(thread, lastViewedTurnByThreadID[thread.id])));
    // Apply the cap after inserting arrivals so a full list still shows them.
    return sorted.filter(matches).slice(0, RECENT_THREAD_LIMIT);
  }, [activeThreadID, folderByThreadID, lastViewedTurnByThreadID, pendingThreadID, reconciledOrder, threads, view, workspace]);
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

  useLayoutEffect(() => {
    if (!heldOrder) return;
    const heldIDs = new Set(heldOrder.map(thread => thread.id));
    // Once navigation admits a row, keep it reachable for the whole hold.
    // Otherwise opening the next conversation hides the previously admitted row.
    if (visibleThreads.some(thread => !heldIDs.has(thread.id))) setHeldOrder(visibleThreads);
  }, [heldOrder, visibleThreads]);

  // A view change is deliberate, so it applies even while the pointer holds
  // the list (the options menu opens from inside it).
  const heldView = useRef(view);
  useLayoutEffect(() => {
    if (heldView.current === view) return;
    heldView.current = view;
    setHeldOrder((held) => held && ordered);
  }, [ordered, view]);

  return {
    threads: visibleThreads,
    onReorder: view.sort !== "manual" ? undefined : (activeThreadID, overThreadID, position) => {
      const current = visibleThreads.map((thread) => thread.id);
      const next = moveThreadInOrder(current, activeThreadID, overThreadID, position);
      if (next === current) return;
      const visibleIDs = new Set(next);
      const remaining = [...next];
      // Under a filter, refill the visible rows' slots in their new sequence so
      // the conversations it hides keep their positions.
      const fullOrder = recentViewFilters(view)
        ? [...reconciledArrivalOrder.ids.map((id) => visibleIDs.has(id) ? remaining.shift()! : id), ...remaining]
        : [...next, ...reconciledArrivalOrder.ids.filter((id) => !visibleIDs.has(id))];
      const manualOrder = { ...reconciledArrivalOrder, ids: fullOrder };
      setArrivalOrder(manualOrder);
      persistRecentOrder(manualOrder);
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
