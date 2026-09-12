import { useEffect, useState } from "react";
import type { ChannelSessionReadResult } from "../shared/protocol";
import { handleStreamingNotification, initialState, reduceServerEvent, syncRunningThreadStreamItems } from "./AppState";
import { subscribeServerEvents } from "./ServerEvents";
import { toastErrorMessage } from "./Toast";

export function useChannelSession(sessionRef: string, retry: number, enabled = true) {
  const [detail, setDetail] = useState<ChannelSessionReadResult | null>(null);
  const [error, setError] = useState("");
  const [streamStatus, setStreamStatus] = useState(initialState.turnStreamStatus);

  useEffect(() => {
    if (!enabled) return;
    let active = true;
    let state = initialState;
    let current: ChannelSessionReadResult | null = null;
    let ownerWorkdir: string | undefined;
    let inFlight = false;
    let requestID = "";
    let receivedSnapshot = false;
    setDetail(null);
    setError("");
    setStreamStatus({});

    const install = (result: ChannelSessionReadResult) => {
      current = result;
      state = { ...initialState, thread: result.thread, threads: [result.thread] };
      syncRunningThreadStreamItems(result.thread);
      setDetail(result);
      setStreamStatus(state.turnStreamStatus);
      setError("");
    };
    const read = async () => {
      if (!active || inFlight) return;
      inFlight = true;
      requestID = crypto.randomUUID();
      receivedSnapshot = false;
      try {
        const result = await window.wuu.readChannelSession({ sessionRef, requestId: requestID });
        // Older bridges can return a snapshot without the ordered notification.
        if (active && !receivedSnapshot) install(result);
      } catch (reason) {
        if (active) setError(toastErrorMessage(reason));
      } finally {
        inFlight = false;
      }
    };
    const off = subscribeServerEvents((event) => {
      if (!active) return;
      if (event.kind === "server-exit") {
        if (event.workdir === ownerWorkdir) setError(event.message);
        return;
      }
      if (event.kind !== "notification") return;
      const { method, params } = event.message;
      if (method === "channel/session/snapshot") {
        const snapshot = params as { request_id: string; result: ChannelSessionReadResult };
        if (snapshot.request_id !== requestID) return;
        receivedSnapshot = true;
        ownerWorkdir = event.workdir;
        install(snapshot.result);
        return;
      }
      if ((params as { thread_id?: string } | undefined)?.thread_id !== sessionRef || !current) return;
      if (method === "turn/started") ownerWorkdir = event.workdir;
      if (ownerWorkdir !== undefined && event.workdir !== ownerWorkdir) return;
      // The same reducer and text store drive Harness. High-rate text deltas
      // publish directly to their subscribers without re-rendering this pane.
      const handling = handleStreamingNotification(event, state);
      if (handling === "stream" || handling === "background-stream") return;
      if (handling === "skip" && method !== "turn/event") return;
      state = reduceServerEvent(state, event);
      if (state.thread !== current.thread) {
        current = { ...current, thread: state.thread! };
        setDetail(current);
      }
      setStreamStatus(state.turnStreamStatus);
      setError("");
    });
    // Reconcile after a suspended renderer; ordinary streaming needs no polls.
    const onVisible = () => { if (document.visibilityState !== "hidden") void read(); };
    document.addEventListener("visibilitychange", onVisible);
    void read();
    return () => {
      active = false;
      off();
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [sessionRef, retry, enabled]);

  return { detail, error, streamStatus };
}
