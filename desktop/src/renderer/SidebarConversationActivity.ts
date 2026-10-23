import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentPropsWithRef } from "react";
import {
  isThreadExecuting,
  isThreadUnread,
  latestCompletedTurnID,
  sidebarThreadSortTime,
  type ThreadSummary,
} from "./AppState";
import { readFocusModality } from "./FocusModality";

const RECENT_THREAD_LIMIT = 50;
// Reading clears the badge, not the priority earned by a reply or running work.
// These positions last for this mounted sidebar; reopening rebuilds from status.
const ACTIVITY_RANK = { running: 0, attention: 1, history: 2 } as const;

type RecentActivity = {
  rank: number;
  time: number;
  revision: number;
  completedTurnID: string | undefined;
};

/** Keep the Recent list prioritized without moving targets during sidebar interaction. */
export function useSidebarConversationActivity(
  threads: ThreadSummary[],
  activeThreadID: string | undefined,
  pendingThreadID: string | undefined,
  lastViewedTurnByThreadID: Readonly<Record<string, string>>,
): { threads: ThreadSummary[]; interactionProps: ComponentPropsWithRef<"div"> } {
  const committedActivity = useRef({ revision: 0, rows: new Map<string, RecentActivity>() });
  const activity = useMemo(() => {
    const previous = committedActivity.current;
    const rows = new Map<string, RecentActivity>();
    let revision = previous.revision;
    const ordered = threads.map((thread) => {
      const prior = previous.rows.get(thread.id);
      const running = isThreadExecuting(thread);
      const completedTurnID = latestCompletedTurnID(thread) ?? prior?.completedTurnID;
      const changed = prior && (
        running !== (prior.rank === ACTIVITY_RANK.running)
        || (!running && completedTurnID !== prior.completedTurnID)
      );
      let position = prior;
      if (!position || changed) {
        const unread = thread.id !== activeThreadID && thread.id !== pendingThreadID
          && isThreadUnread(thread, lastViewedTurnByThreadID[thread.id]);
        const startedAt = running
          ? thread.turns.findLast((turn) => turn.status === "in_progress")?.started_at
          : undefined;
        if (changed) revision = previous.revision + 1;
        position = {
          rank: running ? ACTIVITY_RANK.running
            : changed || unread ? ACTIVITY_RANK.attention : ACTIVITY_RANK.history,
          time: (startedAt ? Date.parse(startedAt) : 0) || sidebarThreadSortTime(thread, running),
          revision: changed ? previous.revision + 1 : previous.revision,
          completedTurnID,
        };
      }
      // Preserve sort keys through read receipts, selection, stream updates and
      // loading a full turn over its summary. Only real activity moves a row.
      rows.set(thread.id, { ...position, completedTurnID });
      return { thread, ...position };
    }).sort((left, right) => left.rank - right.rank
      || right.revision - left.revision
      || right.time - left.time
      || left.thread.id.localeCompare(right.thread.id));
    return { revision, rows, ordered: ordered.slice(0, RECENT_THREAD_LIMIT).map((row) => row.thread) };
  }, [activeThreadID, lastViewedTurnByThreadID, pendingThreadID, threads]);
  // Commit after render so an abandoned render cannot consume an activity event.
  useLayoutEffect(() => { committedActivity.current = activity; }, [activity]);
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
