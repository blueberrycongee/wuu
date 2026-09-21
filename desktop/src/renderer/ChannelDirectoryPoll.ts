/** Steady cadence while the room or agent directory is changing. */
export const DIRECTORY_POLL_BASE_MS = 2_000;

/** Ceiling after repeated identical directory snapshots. */
export const DIRECTORY_POLL_MAX_MS = 30_000;

export function nextDirectoryPollDelay(current: number, changed: boolean, base = DIRECTORY_POLL_BASE_MS): number {
  if (changed) return base;
  return Math.min(DIRECTORY_POLL_MAX_MS, Math.max(base, current) * 2);
}
