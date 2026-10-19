import { useLayoutEffect, useRef, useState } from "react";

export type PageHistory = { entries: string[]; index: number };
export function recordPage(history: PageHistory, page: string, replace = false): PageHistory {
  if (history.entries[history.index] === page) return history;
  if (replace && history.index >= 0) {
    return { ...history, entries: history.entries.map((entry, index) => index === history.index ? page : entry) };
  }
  return { entries: [...history.entries.slice(0, history.index + 1), page], index: history.index + 1 };
}
export function historyTarget(history: PageHistory, direction: -1 | 1, available: (page: string) => boolean): number | undefined {
  for (let index = history.index + direction; index >= 0 && index < history.entries.length; index += direction) {
    if (history.entries[index] !== history.entries[history.index] && available(history.entries[index])) return index;
  }
  return undefined;
}

/** Window-local destination IDs only. The existing route owners retain content and drafts. */
export function usePageHistory(page: string | undefined, available: (page: string) => boolean,
  restore: (page: string, isCurrent: () => boolean) => Promise<void>, replaceCurrent = false): {
    canGoBack: boolean; canGoForward: boolean; onBack: () => void; onForward: () => void;
  } {
  const [history, setHistory] = useState<PageHistory>({ entries: [], index: -1 });
  const historyRef = useRef(history);
  historyRef.current = history;
  const pending = useRef<{ page: string; origin: string | undefined; index: number } | undefined>(undefined);
  const [restoring, setRestoring] = useState(false);
  const [settled, setSettled] = useState<object>();
  useLayoutEffect(() => {
    if (!page) return;
    const replay = pending.current;
    if (replay && page === replay.origin) {
      if (settled === replay) { pending.current = undefined; setRestoring(false); }
      return;
    }
    if (replay && page === replay.page) {
      pending.current = undefined;
      setRestoring(false);
      setHistory(current => ({ ...current, index: replay.index }));
      return;
    }
    // A newer navigation wins over an outstanding replay.
    pending.current = undefined;
    setRestoring(false);
    setHistory(current => recordPage(current, page, replaceCurrent));
  }, [page, settled, replaceCurrent]);
  const go = async (direction: -1 | 1): Promise<void> => {
    if (pending.current) return;
    const current = historyRef.current;
    const index = historyTarget(current, direction, available);
    if (index === undefined) return;
    const replay = { page: current.entries[index], origin: page, index };
    pending.current = replay;
    setRestoring(true);
    try {
      await restore(replay.page, () => pending.current === replay);
    } finally {
      // Activation can fail without throwing (the existing actions show their
      // own notice). Check the rendered route after React commits its updates.
      if (pending.current === replay) setSettled(replay);
    }
  };
  return {
    canGoBack: !restoring && historyTarget(history, -1, available) !== undefined,
    canGoForward: !restoring && historyTarget(history, 1, available) !== undefined,
    onBack: () => { void go(-1); }, onForward: () => { void go(1); },
  };
}
