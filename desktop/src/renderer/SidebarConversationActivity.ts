import { useEffect, useMemo, useRef, useState, type ComponentPropsWithRef } from "react";
import { isThreadExecuting, isThreadUnread, sidebarThreadSortTime, type ThreadSummary } from "./AppState";
import { readFocusModality } from "./FocusModality";

const RECENT_THREAD_LIMIT = 50;
const SECTION_IDS = ["running", "unread", "recent"] as const;

type ConversationSection = {
  id: (typeof SECTION_IDS)[number];
  threads: ThreadSummary[];
};

/** Keep activity groups current without moving targets during sidebar interaction. */
export function useSidebarConversationActivity(
  threads: ThreadSummary[],
  activeThreadID: string | undefined,
  pendingThreadID: string | undefined,
  lastViewedTurnByThreadID: Readonly<Record<string, string>>,
): { sections: ConversationSection[]; interactionProps: ComponentPropsWithRef<"div"> } {
  const ordered = useMemo(() => {
    const rows = threads.map((thread) => {
      const running = isThreadExecuting(thread);
      const unread = !running
        && thread.id !== activeThreadID && thread.id !== pendingThreadID
        && isThreadUnread(thread, lastViewedTurnByThreadID[thread.id]);
      // Live output changes updated_at. Only the turn's start can order running
      // work; unloaded cross-workspace turns fall back to creation time.
      const startedAt = running
        ? thread.turns.findLast((turn) => turn.status === "in_progress")?.started_at
        : undefined;
      const time = (startedAt ? Date.parse(startedAt) : 0)
        || sidebarThreadSortTime(thread, running);
      return { thread, section: running ? "running" : unread ? "unread" : "recent", time };
    }).sort((left, right) => right.time - left.time || left.thread.id.localeCompare(right.thread.id));
    return SECTION_IDS.map((id) => ({
      id,
      threads: rows.filter((row) => row.section === id)
        .slice(0, id === "recent" ? RECENT_THREAD_LIMIT : undefined)
        .map((row) => row.thread),
    }));
  }, [threads, activeThreadID, pendingThreadID, lastViewedTurnByThreadID]);
  const [heldOrder, setHeldOrder] = useState<ConversationSection[]>();
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
  const sections = useMemo(() => {
    if (!heldOrder) return ordered;
    const byID = new Map(threads.map((thread) => [thread.id, thread]));
    return heldOrder.map((section) => ({
      ...section,
      threads: section.threads.flatMap((thread) => {
        const current = byID.get(thread.id);
        return current ? [current] : [];
      }),
    }));
  }, [heldOrder, ordered, threads]);

  return {
    sections,
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
