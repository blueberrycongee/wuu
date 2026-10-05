import { useCallback, useEffect, useRef, useState } from "react";
import type { ProjectWork, ProjectWorkList } from "../shared/protocol";
import { subscribeServerEvents } from "./ServerEvents";

// Status comes from the host ledger. Refresh on lifecycle events, never by
// waking a model or polling its transcript.
export function useProjectWork(projectID: string): {
  snapshot?: ProjectWorkList; error: string; busy: string;
  control: (work: ProjectWork, operation: "stop" | "resume") => Promise<void>;
} {
  const [snapshot, setSnapshot] = useState<ProjectWorkList>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const current = useRef(snapshot);
  current.current = snapshot;
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    if (!window.wuu.projectWork) return;
    const request = ++generation.current;
    try {
      const result = await window.wuu.projectWork({ project_id: projectID, operation: "list" });
      if (request === generation.current && "works" in result) { setSnapshot(result); setError(""); }
    } catch (reason) {
      if (request === generation.current) setError(String(reason instanceof Error ? reason.message : reason));
    }
  }, [projectID]);
  useEffect(() => {
    setSnapshot(undefined);
    setError("");
    void refresh();
    const off = subscribeServerEvents((event) => {
      if (event.kind !== "notification") return;
      const { method, params } = event.message;
      const payload = params as { project_id?: string; thread_id?: string; item?: { type?: string } } | undefined;
      if (method === "project/work/updated" && payload?.project_id === projectID) { void refresh(); return; }
      if (!["turn/started", "turn/completed", "item/completed"].includes(method)) return;
      if (method === "item/completed" && payload?.item?.type !== "user_message") return;
      if (payload?.thread_id === projectID || current.current?.works.some(work => [work.lead_id, work.executor_id].includes(payload?.thread_id))) void refresh();
    });
    return () => { generation.current++; off(); };
  }, [projectID, refresh]);
  const control = useCallback(async (work: ProjectWork, operation: "stop" | "resume") => {
    if (!window.wuu.projectWork) return;
    setBusy(work.id);
    try {
      await window.wuu.projectWork({ project_id: projectID, request_id: crypto.randomUUID(), operation,
        work_id: work.id, revision: work.revision, brief: work.brief, acceptance: work.acceptance,
        authority: work.authority, source_refs: work.source_refs });
      await refresh();
    } catch (reason) { setError(String(reason instanceof Error ? reason.message : reason)); }
    finally { setBusy(""); }
  }, [projectID, refresh]);
  return { snapshot: snapshot?.project_id === projectID ? snapshot : undefined, error, busy, control };
}
