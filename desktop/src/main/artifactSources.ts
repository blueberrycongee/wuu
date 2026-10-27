import { randomUUID } from "node:crypto";
import type { ArtifactSourceParams, ArtifactSourceLease } from "../shared/protocol";
import { parseByteRangeHeader, renderableResponseHeaders } from "./renderableFileRange";

export type ArtifactMetadata = { id: string; uri: string; name: string; mime_type: string; size: number; sha256: string };
type ArtifactOperation<T> = { result: Promise<T>; release(): Promise<void> };
export type ArtifactRequest = {
  <T>(method: string, params: unknown): Promise<T>;
  owned?<T>(method: string, params: unknown, cleanup: {method:string; params:unknown}): ArtifactOperation<T>;
};

// A transport with retained cleanup captures the exact cancel capability at
// dispatch. It can release that operation after route retirement without opening
// a general RPC path to stale hosts or a new connection generation.
function operation<T>(request:ArtifactRequest, method:string, params:Record<string,unknown> & {thread_id:string; request_id:string}): ArtifactOperation<T> {
  const cleanup={method:"thread/artifact/cancel",params:Object.freeze({thread_id:params.thread_id,request_id:params.request_id})};
  if (request.owned) return request.owned<T>(method,params,cleanup);
  return {result:request<T>(method,params),release:async()=>{await request(cleanup.method,cleanup.params);}};
}
type Resolution = { artifact: ArtifactMetadata; generation_id: string };
type Chunk = { data: string; mime_type: string; offset: number; total: number; sha256: string };
type Lease = { input: ArtifactSourceParams; resolved: Resolution; request: ArtifactRequest; current(): boolean; abort: AbortController; owner?: number };
const CHUNK_BYTES = 1024 * 1024;

/** Transport leases hold a captured authenticated route, never artifact payloads
 * or a second artifact catalog. Every chunk is authorized by its owning core. */
export class ArtifactSources {
  private readonly leases = new Map<string, Lease>();
  private readonly pending = new Map<string, {owner?: number; current(): boolean; cancel(): void}>();

  async acquire(input: ArtifactSourceParams, request: ArtifactRequest, current: () => boolean, owner?: number): Promise<ArtifactSourceLease> {
    if (!input || typeof input.threadId !== "string" || !input.threadId || typeof input.uri !== "string" || !input.uri || !/^[a-f0-9]{64}$/.test(input.sha256)) throw new Error("Invalid artifact identity");
    if (!current()) throw new Error("Artifact source is no longer active");
    const leaseId = input.requestId ?? randomUUID();
    if (!/^[a-zA-Z0-9_-]{1,128}$/.test(leaseId) || this.leases.has(leaseId) || this.pending.has(leaseId)) throw new Error("Artifact acquisition identity is already active or invalid");
    const pending = {owner,current,cancel: () => {}};
    this.pending.set(leaseId,pending);
    try {
      const resolve = operation<Resolution>(request,"thread/artifact/resolve", {request_id:leaseId,thread_id:input.threadId,uri:input.uri,sha256:input.sha256});
      pending.cancel = () => {void resolve.release().catch(()=>undefined);};
      if (this.pending.get(leaseId) !== pending) pending.cancel();
      const resolved = await resolve.result;
      const artifact = resolved.artifact;
      if (!current() || this.pending.get(leaseId) !== pending) throw new Error("Artifact source is no longer active");
      if (!artifact || artifact.uri !== input.uri || artifact.sha256 !== input.sha256 || !artifact.id || !artifact.name || !artifact.mime_type || !Number.isSafeInteger(artifact.size) || artifact.size < 0 || !resolved.generation_id) throw new Error("Invalid artifact provider response");
      this.leases.set(leaseId, { input: { ...input }, resolved, request, current, owner, abort: new AbortController() });
      return { leaseId, url: `wuu-artifact://lease/${leaseId}` };
    } finally { if (this.pending.get(leaseId) === pending) this.pending.delete(leaseId); }
  }

  release(id: string, owner?: number): void {
    const lease = this.leases.get(id);
    // Cleanup must remain possible after the source document becomes stale.
    if (owner !== undefined && (lease ?? this.pending.get(id))?.owner !== owner) return;
    this.pending.get(id)?.cancel();
    this.pending.delete(id);
    this.leases.delete(id);
    lease?.abort.abort(new Error("Artifact lease released"));
  }

  owns(url: string, owner: number): boolean {
    try { const parsed = new URL(url); const lease = this.leases.get(parsed.pathname.slice(1)) ?? this.pending.get(parsed.pathname.slice(1));
      return parsed.protocol === "wuu-artifact:" && parsed.hostname === "lease" && Boolean(lease && lease.owner === owner && lease.current());
    } catch { return false; }
  }
  releaseOwner(owner: number): void { for (const [id, lease] of [...this.leases, ...this.pending]) if (lease.owner === owner) this.release(id); }

  metadata(id: string): ArtifactMetadata {
    const lease = this.leases.get(id);
    if (!lease || !lease.current()) { this.release(id); throw new Error("Artifact source is no longer active"); }
    return { ...lease.resolved.artifact };
  }

  async response(request: Request): Promise<Response> {
    request.signal.throwIfAborted();
    const url = new URL(request.url);
    const id = url.pathname.slice(1);
    const lease = url.protocol === "wuu-artifact:" && url.hostname === "lease" && !url.search && !url.hash && !url.username && !url.password && !url.port ? this.leases.get(id) : undefined;
    if (!lease || !lease.current()) { if (lease) this.release(id); return new Response("Not found", { status: 404 }); }
    if (!["GET", "HEAD"].includes(request.method)) return new Response("Method not allowed", { status: 405 });
    const artifact = lease.resolved.artifact;
    const range = parseByteRangeHeader(request.headers.get("range"), artifact.size);
    const headers = renderableResponseHeaders(artifact.mime_type);
    headers.set("cache-control", "no-store");
    headers.set("x-artifact-sha256", artifact.sha256);
    headers.set("x-content-type-options", "nosniff");
    // All HTML is untrusted, irrespective of which provider stores it.
    if (/^(text\/html|application\/xhtml\+xml)(;|$)/i.test(artifact.mime_type)) headers.set("content-security-policy", "sandbox; default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'; font-src data:; media-src data: blob:");
    // Responses without a payload still disclose metadata and must reauthorize.
    if (request.method === "HEAD" || artifact.size === 0 || range === "unsatisfiable") {
      const signal = AbortSignal.any([request.signal, lease.abort.signal]);
      const request_id = randomUUID();
      const resolve = operation<Resolution>(lease.request,"thread/artifact/resolve",{request_id,thread_id:lease.input.threadId,uri:lease.input.uri,sha256:lease.input.sha256});
      const cancel = () => { void resolve.release().catch(() => undefined); };
      signal.addEventListener("abort",cancel,{once:true});
      if (signal.aborted) cancel();
      let current: Resolution;
      try { current = await resolve.result; }
      finally { signal.removeEventListener("abort",cancel); }
      signal.throwIfAborted();
      if (!lease.current() || current.generation_id !== lease.resolved.generation_id || (Object.keys(artifact) as (keyof ArtifactMetadata)[]).some(key => current.artifact?.[key] !== artifact[key])) throw new Error("Artifact source changed");
    }
    if (range === "unsatisfiable") { headers.set("content-range", `bytes */${artifact.size}`); return new Response(null, { status: 416, headers }); }
    let offset = range?.start ?? 0;
    const end = range ? range.end + 1 : artifact.size;
    headers.set("content-length", String(end - offset));
    if (range) headers.set("content-range", `bytes ${offset}-${end - 1}/${artifact.size}`);
    if (request.method === "HEAD" || artifact.size === 0) return new Response(null, {status:range ? 206 : 200,headers});
    let canceled = false;
    const responseAbort = new AbortController();
    const signal = AbortSignal.any([request.signal, lease.abort.signal, responseAbort.signal]);
    const check = (): void => {
      signal.throwIfAborted();
      if (canceled || !lease.current()) throw new Error("Artifact source was revoked");
    };
    const body = new ReadableStream<Uint8Array>({
      async pull(controller) {
        try {
          check();
          if (offset === end) { controller.close(); return; }
          const max_bytes = Math.min(CHUNK_BYTES, end - offset);
          const request_id = randomUUID();
          const read = operation<Chunk>(lease.request,"thread/artifact/read", {request_id,thread_id:lease.input.threadId,uri:lease.input.uri,
            sha256:lease.input.sha256,generation_id:lease.resolved.generation_id,offset,max_bytes});
          const cancel = () => { void read.release().catch(() => undefined); };
          signal.addEventListener("abort", cancel, {once: true});
          if (signal.aborted) cancel();
          let result: Chunk;
          try { result = await read.result; }
          finally { signal.removeEventListener("abort", cancel); }
          check();
          const bytes = Buffer.from(result.data, "base64");
          if (result.offset !== offset || result.total !== artifact.size || result.sha256 !== artifact.sha256 || result.mime_type !== artifact.mime_type || bytes.length !== max_bytes) throw new Error("Artifact range identity changed");
          offset += bytes.length; controller.enqueue(bytes);
          if (offset === end) controller.close();
        } catch (error) { if (!canceled) controller.error(error); }
      },
      cancel() { canceled = true; responseAbort.abort(); },
    });
    return new Response(body, { status: range ? 206 : 200, headers });
  }
}
