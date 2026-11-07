import type { ThreadSummary } from "./AppState";

const SIDEBAR_THREAD_ORDER_KEY = "wuu.desktop.sidebarThreadOrderByWorkspace";
type SidebarThreadOrderByWorkspace = Record<string, string[]>;

export function storedThreadOrder(workspaceID: string): string[] {
  try {
    const stored = window.localStorage.getItem(SIDEBAR_THREAD_ORDER_KEY);
    const parsed: unknown = stored ? JSON.parse(stored) : {};
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return [];
    }
    const order = (parsed as SidebarThreadOrderByWorkspace)[workspaceID];
    return Array.isArray(order)
      ? order.filter((id): id is string => typeof id === "string" && id.length > 0)
      : [];
  } catch {
    return [];
  }
}

export function persistThreadOrder(workspaceID: string, order: string[]): void {
  try {
    const stored = window.localStorage.getItem(SIDEBAR_THREAD_ORDER_KEY);
    const parsed: unknown = stored ? JSON.parse(stored) : {};
    const current = parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? parsed as SidebarThreadOrderByWorkspace
      : {};
    window.localStorage.setItem(
      SIDEBAR_THREAD_ORDER_KEY,
      JSON.stringify({ ...current, [workspaceID]: order }),
    );
  } catch {
    // A blocked or full localStorage should not prevent in-memory reordering.
  }
}

export type ThreadDropPosition = "before" | "after";

export function moveThreadInOrder(
  order: string[],
  activeThreadID: string,
  targetThreadID: string,
  position: ThreadDropPosition,
): string[] {
  if (activeThreadID === targetThreadID) return order;
  const withoutActive = order.filter((id) => id !== activeThreadID);
  const targetIndex = withoutActive.indexOf(targetThreadID);
  if (targetIndex < 0 || withoutActive.length === order.length) return order;
  withoutActive.splice(targetIndex + (position === "after" ? 1 : 0), 0, activeThreadID);
  return withoutActive.every((id, index) => id === order[index]) ? order : withoutActive;
}

export function reconcileThreadOrder(threads: ThreadSummary[], storedOrder: string[]): string[] {
  const threadIDs = new Set(threads.map((thread) => thread.id));
  const known = storedOrder.filter((id) => threadIDs.has(id));
  const knownIDs = new Set(known);
  const newIDs = threads
    .map((thread) => thread.id)
    .filter((id) => !knownIDs.has(id));
  return [...newIDs, ...known];
}

export type ThreadArrivalOrder = { ids: string[]; newestCreatedAt: number };

/** Keep presented positions; distinguish newly created sessions from late history. */
export function reconcileThreadArrivalOrder(
  threads: readonly Pick<ThreadSummary, "id" | "created_at">[],
  previous: ThreadArrivalOrder,
): ThreadArrivalOrder {
  const known = new Set(previous.ids);
  const arrivals: string[] = [];
  const history: string[] = [];
  let newestCreatedAt = previous.newestCreatedAt;
  for (const thread of threads) {
    const createdAt = Date.parse(thread.created_at) || 0;
    newestCreatedAt = Math.max(newestCreatedAt, createdAt);
    if (known.has(thread.id)) continue;
    known.add(thread.id);
    (previous.ids.length === 0 || createdAt > previous.newestCreatedAt ? arrivals : history).push(thread.id);
  }
  if (arrivals.length === 0 && history.length === 0 && newestCreatedAt === previous.newestCreatedAt) return previous;
  return { ids: [...arrivals, ...previous.ids, ...history], newestCreatedAt };
}
