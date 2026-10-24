import { useArtifacts } from "./ArtifactsContext";
import { useEffect, useState, type ReactNode } from "react";
import type { ThreadItem } from "../shared/protocol";
import { useI18n } from "./i18n";

/** Callers key this component by the content reference so a new snapshot
 * cannot inherit a completed read for an older version of the item. */
export function RemoteItemContent({ item, render }: { item: ThreadItem; render: (item: ThreadItem, complete: boolean) => ReactNode }): JSX.Element {
  const { t } = useI18n();
  const artifacts = useArtifacts();
  const [loaded, setLoaded] = useState<ThreadItem>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [lifetime] = useState(() => ({ active: true, abort: new AbortController() }));
  useEffect(() => { lifetime.active = true; lifetime.abort = new AbortController(); return () => { lifetime.active = false; lifetime.abort.abort(); }; }, [lifetime]);
  const load = async () => {
    if (loading || !artifacts.capabilities.remoteItem || !item.remote_content_ref) return;
    setLoading(true); setError("");
    try {
      const complete = await artifacts.readItem({remoteRef: item.remote_content_ref}, {signal: lifetime.abort.signal});
      if (complete.id !== item.id || complete.type !== item.type) throw new Error("Content changed during download");
      if (lifetime.active) setLoaded(structuredClone(complete) as ThreadItem);
    } catch (cause) {
      if (lifetime.active) setError(cause instanceof Error ? cause.message : String(cause));
    } finally { if (lifetime.active) setLoading(false); }
  };
  return <>
    {render(loaded ?? { ...item, remote_content_ref: undefined, read_only: true }, Boolean(loaded))}
    {!loaded ? <button type="button" disabled={loading || !artifacts.capabilities.remoteItem} onClick={() => void load()}>
      {t(loading ? "common.loadingEllipsis" : "conversation.loadFullContent")}
    </button> : null}
    {error ? <span role="alert">{error}</span> : null}
  </>;
}
