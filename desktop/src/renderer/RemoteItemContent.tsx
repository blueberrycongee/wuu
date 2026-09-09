import { useEffect, useState, type ReactNode } from "react";
import type { ThreadItem } from "../shared/protocol";
import { useI18n } from "./i18n";

/** Callers key this component by the content reference so a new snapshot
 * cannot inherit a completed read for an older version of the item. */
export function RemoteItemContent({ item, render }: { item: ThreadItem; render: (item: ThreadItem, complete: boolean) => ReactNode }): JSX.Element {
  const { t } = useI18n();
  const [loaded, setLoaded] = useState<ThreadItem>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [lifetime] = useState(() => ({ active: true }));
  useEffect(() => { lifetime.active = true; return () => { lifetime.active = false; }; }, [lifetime]);
  const load = async () => {
    if (loading || !window.wuu.readRemoteItem || !item.remote_content_ref) return;
    setLoading(true); setError("");
    try {
      const complete = await window.wuu.readRemoteItem(item.remote_content_ref);
      if (complete.id !== item.id || complete.type !== item.type) throw new Error("Content changed during download");
      if (lifetime.active) setLoaded(complete);
    } catch (cause) {
      if (lifetime.active) setError(cause instanceof Error ? cause.message : String(cause));
    } finally { if (lifetime.active) setLoading(false); }
  };
  return <>
    {render(loaded ?? { ...item, remote_content_ref: undefined, read_only: true }, Boolean(loaded))}
    {!loaded ? <button type="button" disabled={loading || !window.wuu.readRemoteItem} onClick={() => void load()}>
      {t(loading ? "common.loadingEllipsis" : "conversation.loadFullContent")}
    </button> : null}
    {error ? <span role="alert">{error}</span> : null}
  </>;
}
