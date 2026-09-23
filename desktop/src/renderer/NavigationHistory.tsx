import { useLayoutEffect, useRef, useState } from "react";
import { ArrowLeft, ArrowRight } from "./WuuIcons";
import { useI18n } from "./i18n";

/** Record committed destinations; replay must not create another history entry. */
export function useNavigationHistory<T extends { key: string }>(
  current: T | undefined,
  navigate: (destination: T) => Promise<void>,
) {
  const [history, setHistory] = useState<{ entries: T[]; index: number }>({ entries: [], index: -1 });
  const [pending, setPending] = useState<number | null>(null);
  const [settled, setSettled] = useState(false);
  const pendingRef = useRef<number | null>(null);

  useLayoutEffect(() => {
    if (pending !== null) {
      if (!settled) return;
      if (current?.key === history.entries[pending]?.key) {
        setHistory(previous => ({ ...previous, index: pending }));
      }
      pendingRef.current = null;
      setPending(null);
      return;
    }
    if (!current) return;
    setHistory(previous => {
      if (previous.entries[previous.index]?.key === current.key) return previous;
      const entries = [...previous.entries.slice(0, previous.index + 1), current];
      return { entries, index: entries.length - 1 };
    });
  }, [current, history.entries, pending, settled]);

  async function go(offset: number): Promise<void> {
    if (pendingRef.current !== null || !current) return;
    const index = history.index + offset;
    const destination = history.entries[index];
    if (!destination) return;
    pendingRef.current = index;
    setPending(index);
    setSettled(false);
    try {
      await navigate(destination);
    } finally {
      // Resolve after React commits the destination, including async context switches.
      setSettled(true);
    }
  }

  return {
    canGoBack: current !== undefined && pending === null && history.index > 0,
    canGoForward: current !== undefined && pending === null && history.index < history.entries.length - 1,
    back: () => go(-1),
    forward: () => go(1),
  };
}

export function NavigationHistoryButtons({
  canGoBack, canGoForward, back, forward,
}: {
  canGoBack: boolean;
  canGoForward: boolean;
  back: () => Promise<void>;
  forward: () => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  return <div className="navigation-history-controls" role="group" aria-label={t("app.navigationHistory")}>
    <button type="button" className="icon-button" disabled={!canGoBack}
      aria-label={t("app.navigationBack")} title={t("app.navigationBack")} onClick={() => { void back(); }}>
      <ArrowLeft aria-hidden="true" />
    </button>
    <button type="button" className="icon-button" disabled={!canGoForward}
      aria-label={t("app.navigationForward")} title={t("app.navigationForward")} onClick={() => { void forward(); }}>
      <ArrowRight aria-hidden="true" />
    </button>
  </div>;
}
