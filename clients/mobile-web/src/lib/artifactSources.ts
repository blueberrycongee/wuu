import type { ArtifactSourceParams, ArtifactSourceLease } from '@wuu/protocol';

type Request = <T>(method: string, params: unknown) => Promise<T>;
type Metadata = { id: string; uri: string; name: string; mime_type: string; size: number; sha256: string };
type Resolution = { artifact: Metadata; generation_id: string };
type Chunk = { data: string; mime_type: string; offset: number; total: number; sha256: string };
type Lease = { abort: AbortController; workdir: string; bytes: number; current(): boolean; url?: string };
const CHUNK_BYTES = 1024 * 1024;

/** Explicit small-preview strategy, not an HTTP streaming or persistent cache.
 * The reservation includes pending and acquired leases; clients must release.
 * Delivered/decoded bytes cannot be recalled by a later server revocation.
 */
export const BROWSER_ARTIFACT_SOURCE_CAPABILITIES = Object.freeze({
  strategy: 'bounded-memory' as const,
  maxBytes: 8 * 1024 * 1024,
  maxTotalBytes: 32 * 1024 * 1024,
  unsupportedMimeTypes: Object.freeze(['text/html', 'application/xhtml+xml'] as const),
});

export class ArtifactPreviewLimitError extends Error {
  readonly code = 'artifact_preview_too_large';
  constructor(readonly limit: number, total = false) {
    super(total ? 'Browser artifact preview memory is full; close another preview and try again.'
      : `This browser previews artifacts up to ${limit / 1024 / 1024} MiB. Open the artifact in a streaming-capable desktop host.`);
    this.name = 'ArtifactPreviewLimitError';
  }
}

function abortable<T>(work: Promise<T>, signal: AbortSignal): Promise<T> {
  signal.throwIfAborted();
  return new Promise<T>((resolve, reject) => {
    const abort = () => reject(signal.reason ?? new Error('Artifact request aborted'));
    signal.addEventListener('abort', abort, { once: true });
    work.then(resolve, reject).finally(() => signal.removeEventListener('abort', abort));
  });
}
function same(a: Metadata, b: Metadata): boolean {
  return a.id === b.id && a.uri === b.uri && a.name === b.name && a.mime_type === b.mime_type && a.size === b.size && a.sha256 === b.sha256;
}

export class BrowserArtifactSources {
  private readonly leases = new Map<string, Lease>();
  private reserved = 0;

  async acquire(input: ArtifactSourceParams, request: Request, current: () => boolean, workdir: string): Promise<ArtifactSourceLease> {
    if (!input || typeof input.uri !== 'string' || !input.uri || typeof input.threadId !== 'string' || !input.threadId
      || !/^[a-f0-9]{64}$/.test(input.sha256)) throw new Error('Invalid artifact identity');
    input = { ...input, ...(input.locator ? { locator: structuredClone(input.locator) } : {}) };
    const id = input.requestId ?? crypto.randomUUID();
    if (!id || id.length > 256 || this.leases.has(id)) throw new Error('Artifact request ID is missing or already active');
    const lease: Lease = { abort: new AbortController(), workdir, bytes: 0, current };
    this.leases.set(id, lease);
    const signal = lease.abort.signal;
    const check = () => {
      signal.throwIfAborted();
      if (!current() || this.leases.get(id) !== lease) throw new Error('Artifact source belongs to a replaced connection or workspace');
    };
    const params = { thread_id: input.threadId, uri: input.uri, sha256: input.sha256, ...(input.locator ? { locator: input.locator } : {}) };
    const read = async <T>(method: string, body: Record<string, unknown>, requestID: string): Promise<T> => {
      check();
      const cancel = () => { void request('thread/artifact/cancel', { thread_id: input.threadId, request_id: requestID, ...(input.locator ? { locator: input.locator } : {}) }).catch(() => undefined); };
      signal.addEventListener('abort', cancel, { once: true });
      try { return await abortable(request<T>(method, { ...body, request_id: requestID }), signal); }
      finally { signal.removeEventListener('abort', cancel); }
    };
    try {
      check();
      const resolved = await read<Resolution>('thread/artifact/resolve', params, id);
      check();
      const artifact = resolved.artifact;
      if (!artifact || artifact.uri !== input.uri || artifact.sha256 !== input.sha256 || !artifact.id || !artifact.name || !artifact.mime_type
        || !Number.isSafeInteger(artifact.size) || artifact.size < 0 || !resolved.generation_id) throw new Error('Invalid artifact provider response');
      if (artifact.size > BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxBytes) throw new ArtifactPreviewLimitError(BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxBytes);
      if (this.reserved + artifact.size > BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxTotalBytes) throw new ArtifactPreviewLimitError(BROWSER_ARTIFACT_SOURCE_CAPABILITIES.maxTotalBytes, true);
      // Blob responses cannot install native response CSP. Never preview active
      // HTML under the application's origin; the native streaming host isolates it.
      if (BROWSER_ARTIFACT_SOURCE_CAPABILITIES.unsupportedMimeTypes.some(mime => mime === artifact.mime_type.split(';')[0].trim().toLowerCase())) throw new Error('HTML artifact preview requires a streaming-capable desktop host');
      lease.bytes = artifact.size;
      this.reserved += artifact.size;
      const bytes = new Uint8Array(artifact.size);
      for (let offset = 0; offset < artifact.size;) {
        check();
        const count = Math.min(CHUNK_BYTES, artifact.size - offset);
        const chunk = await read<Chunk>('thread/artifact/read', { ...params, generation_id: resolved.generation_id, offset, max_bytes: count }, crypto.randomUUID());
        check();
        if (chunk.offset !== offset || chunk.total !== artifact.size || chunk.sha256 !== artifact.sha256 || chunk.mime_type !== artifact.mime_type
          || typeof chunk.data !== 'string' || chunk.data.length > Math.ceil(count / 3) * 4) throw new Error('Artifact range identity changed');
        const raw = atob(chunk.data);
        if (raw.length !== count) throw new Error('Artifact range length changed');
        for (let index = 0; index < raw.length; index++) bytes[offset + index] = raw.charCodeAt(index);
        offset += raw.length;
      }
      const hash = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), byte => byte.toString(16).padStart(2, '0')).join('');
      check();
      if (hash !== input.sha256) throw new Error('Artifact digest changed');
      // A zero-byte object also crosses this final authorization barrier. Do not
      // publish a local URL after the provider/permission changed during transfer.
      const latest = await read<Resolution>('thread/artifact/resolve', params, crypto.randomUUID());
      check();
      if (latest.generation_id !== resolved.generation_id || !same(latest.artifact, artifact)) throw new Error('Artifact source changed');
      lease.url = URL.createObjectURL(new Blob([bytes], { type: artifact.mime_type }));
      return Object.freeze({ url: lease.url, leaseId: id });
    } catch (error) {
      this.release(id);
      throw error;
    }
  }

  release(id: string): void {
    const lease = this.leases.get(id);
    if (!lease) return;
    this.leases.delete(id);
    this.reserved -= lease.bytes;
    lease.abort.abort(new Error('Artifact lease released'));
    if (lease.url) URL.revokeObjectURL(lease.url);
  }
  owns(url: string): boolean {
    for (const [id, lease] of this.leases) if (lease.url === url) {
      if (!lease.current()) { this.release(id); return false; }
      return true;
    }
    return false;
  }
  clear(workdir?: string): void {
    for (const [id, lease] of this.leases) if (workdir === undefined || lease.workdir === workdir) this.release(id);
  }
}
