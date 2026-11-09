import type { DesktopAutoArchiveDays, Thread } from "../shared/protocol";
import { isThreadRunning } from "./AppState";

const DAY_MS = 86_400_000;

// Pinned, running, open, and child conversations stay put; unknown activity is never treated as stale.
export function autoArchiveCandidates(threads: readonly Thread[], days: DesktopAutoArchiveDays, now: number, openIDs: ReadonlySet<string | undefined>): Thread[] {
  if (days === 0) return [];
  const cutoff = now - days * DAY_MS;
  return threads.filter(thread => !thread.archived && !thread.pinned && !thread.ephemeral && !thread.parent_id && !openIDs.has(thread.id)
    && !isThreadRunning(thread) && Date.parse(thread.updated_at) < cutoff);
}
