import { isThreadExecuting, type ThreadSummary } from "./AppState";

export function sortManagedSessions(threads: readonly ThreadSummary[]): ThreadSummary[] {
  return [...threads].sort((a, b) => Number(isThreadExecuting(b)) - Number(isThreadExecuting(a))
    || (Date.parse(b.updated_at) || 0) - (Date.parse(a.updated_at) || 0) || a.id.localeCompare(b.id));
}
