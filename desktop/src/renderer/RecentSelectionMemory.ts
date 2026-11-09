// Storage for pickers that must remember the last choice made for each parent:
// the composer's provider (model + effort) and engine (model + effort) memories.
//
// The payload is one most-recent-first array so a single stored list answers both
// "what did I pick last" (the first entry) and "what did I pick last for this
// parent/model" (the first matching entry). Older builds wrote a single object;
// that shape still parses as a one-entry list, so an upgrade keeps the pick.
const RECENT_SELECTION_LIMIT = 20;

// The list cap bounds storage growth; entries past the limit are the least
// recently used parent/model pairs, which the picker defaults can regenerate.
export function readRecentSelections<T>(
  key: string,
  parse: (value: unknown) => T | undefined,
): T[] {
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    const values = Array.isArray(parsed) ? parsed : [parsed];
    const entries: T[] = [];
    for (const value of values) {
      const entry = parse(value);
      if (entry !== undefined) entries.push(entry);
    }
    return entries;
  } catch {
    // Corrupted or blocked storage means "nothing remembered".
    return [];
  }
}

export function rememberRecentSelection<T>(
  key: string,
  parse: (value: unknown) => T | undefined,
  identity: (entry: T) => string,
  entry: T,
): void {
  const id = identity(entry);
  const rest = readRecentSelections(key, parse).filter((item) => identity(item) !== id);
  try {
    window.localStorage.setItem(
      key,
      JSON.stringify([entry, ...rest].slice(0, RECENT_SELECTION_LIMIT)),
    );
  } catch {
    // A denied/quota-limited write should not break the selection; the
    // in-memory draft still applies for the current window.
  }
}

export function clearRecentSelections(key: string): void {
  try {
    window.localStorage.removeItem(key);
  } catch {
    // Nothing to recover: the next read falls back to the default.
  }
}
