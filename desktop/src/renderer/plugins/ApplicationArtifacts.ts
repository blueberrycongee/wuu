import type { ApplicationArtifactsV1, ArtifactResourceV1, ArtifactCallOptionsV1, ArtifactSourceV1 } from "../../../../packages/plugin-sdk/src/ui-artifacts";
import type { WuuDesktopApi } from "../../shared/protocol";

type Host = Pick<WuuDesktopApi, "readRemoteAttachment" | "readRemoteAttachmentPreview" | "readRemoteItem" | "showArtifactItemMenu" | "saveArtifactFile" | "acquireArtifactSource" | "releaseArtifactSource" | "artifactSourceCapabilities" | "unsupportedMethods">;
type Preview = { resolveResourceURL(input: { source: string; cwd?: string }): string | undefined;
  openFile(input: { root: string; path: string }, options?: ArtifactCallOptionsV1): Promise<void> };
const MAX_TEXT_PREVIEW_BYTES = 2 * 1024 * 1024;

/** Shared artifact mechanisms; each acquired URL has an explicit bounded lease. */
export class ApplicationArtifacts implements ApplicationArtifactsV1 {
  readonly capabilities: ApplicationArtifactsV1["capabilities"];
  private readonly leases = new Map<string, { url: string; detach(): void; revoke(): void; resource?: ArtifactResourceV1 }>();
  private menuPending = false;
  constructor(private readonly api: Host, private readonly preview: Preview,
    private readonly threadLocator?: (resource:ArtifactResourceV1) => ArtifactResourceV1["locator"]) {
    const available = (name: keyof Host) => Boolean(api[name]) && !api.unsupportedMethods?.includes(name);
    this.capabilities = Object.freeze({ nativeItemMenu: available("showArtifactItemMenu"), save: true,
      remoteAttachment: available("readRemoteAttachment"), remoteAttachmentPreview: available("readRemoteAttachmentPreview"), remoteItem: available("readRemoteItem"), source: api.artifactSourceCapabilities && Object.freeze({...api.artifactSourceCapabilities}) });
  }
  private sourceIdentity(resource:ArtifactResourceV1): ArtifactResourceV1 {
    const locator = resource.locator ?? this.threadLocator?.(resource);
    if (locator?.session_id && locator.session_id !== resource.threadId) throw new Error("Artifact session locator does not match its source");
    return locator ? {...resource,locator:{...locator}} : resource;
  }
  private original(resource: ArtifactResourceV1): ArtifactResourceV1 {
    for (const lease of this.leases.values()) if (lease.url === resource.uri && lease.resource) return {...lease.resource, name: resource.name};
    return resource;
  }
  private managed(resource: ArtifactResourceV1): boolean {
    return Boolean(resource.uri && (resource.sha256 || resource.ref || resource.uri.startsWith("wuu-artifact:")));
  }
  resolveSource(resource: ArtifactResourceV1): string | undefined {
    if (this.managed(resource)) return;
    if (resource.data && /^(image|audio)\//.test(resource.mimeType)) return `data:${resource.mimeType};base64,${resource.data}`;
    if (resource.text !== undefined && resource.mimeType.startsWith("image/")) return `data:${resource.mimeType};charset=utf-8,${encodeURIComponent(resource.text)}`;
    const uri = resource.uri?.trim(); if (!uri) return;
    if (/^(https?:|data:|blob:|wuu-file:|wuu-artifact:)/i.test(uri)) return uri;
    if (/^(text\/html|application\/xhtml\+xml)$/.test(resource.mimeType)) return;
    return /^(image|video)\//.test(resource.mimeType) || resource.mimeType === "application/pdf"
      ? this.preview.resolveResourceURL({ source: uri, cwd: resource.cwd }) : undefined;
  }
  async readAttachment(input: { remoteRef: string; preview?: boolean }, options?: ArtifactCallOptionsV1): Promise<string> {
    options?.signal?.throwIfAborted();
    if (input.preview ? !this.capabilities.remoteAttachmentPreview : !this.capabilities.remoteAttachment) throw new Error("Remote attachment loading is unavailable");
    const result = await (input.preview ? this.api.readRemoteAttachmentPreview!(input.remoteRef) : this.api.readRemoteAttachment!(input.remoteRef));
    options?.signal?.throwIfAborted(); return result;
  }
  async readItem(input: { remoteRef: string }, options?: ArtifactCallOptionsV1) {
    options?.signal?.throwIfAborted(); if (!this.capabilities.remoteItem) throw new Error("Remote content loading is unavailable");
    const result = structuredClone(await this.api.readRemoteItem!(input.remoteRef)); options?.signal?.throwIfAborted();
    const freeze = (value: unknown): void => { if (!value || typeof value !== "object") return; Object.values(value).forEach(freeze); Object.freeze(value); };
    freeze(result); return result;
  }
  async acquireSource(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<ArtifactSourceV1> {
    options?.signal?.throwIfAborted();
    if (this.managed(resource)) {
      resource = this.sourceIdentity(resource);
      this.verifyManaged(resource);
      if (!this.api.acquireArtifactSource || !this.api.releaseArtifactSource || this.api.unsupportedMethods?.includes("acquireArtifactSource")) throw new Error("Selected artifact loading is unavailable");
      const requestId = crypto.randomUUID();
      const release = () => { void this.api.releaseArtifactSource!(requestId).catch(() => undefined); };
      const abortPending = () => release();
      options?.signal?.addEventListener("abort", abortPending, {once:true});
      try {
        const source = await this.api.acquireArtifactSource({uri: resource.uri!, threadId: resource.threadId!, sha256: resource.sha256!, requestId, ...(resource.locator ? {locator:resource.locator} : {})});
        if (source.leaseId !== requestId) { void this.api.releaseArtifactSource!(source.leaseId).catch(() => undefined); throw new Error("Artifact source lease identity changed"); }
        const abort = () => this.releaseSource(source.leaseId);
        this.leases.set(source.leaseId, {url: source.url, resource: {...resource, locator: resource.locator && {...resource.locator}}, detach: () => options?.signal?.removeEventListener("abort", abort), revoke: release});
        options?.signal?.addEventListener("abort", abort, {once:true});
        if (options?.signal?.aborted) { this.releaseSource(source.leaseId); options.signal.throwIfAborted(); }
        return Object.freeze({...source});
      } finally { options?.signal?.removeEventListener("abort", abortPending); }
    }
    const direct = this.resolveSource(resource); if (direct) return Object.freeze({ url: direct });
    let data = resource.data;
    if (!data && resource.remoteRef) data = await this.readAttachment({ remoteRef: resource.remoteRef }, options);
    options?.signal?.throwIfAborted();
    if (!data && resource.text === undefined) throw new Error("Artifact source unavailable");
    const payload = data ? Uint8Array.from(atob(data), character => character.charCodeAt(0)) : resource.text!;
    const url = URL.createObjectURL(new Blob([payload], { type: resource.mimeType }));
    const leaseId = crypto.randomUUID(); const abort = () => this.releaseSource(leaseId);
    this.leases.set(leaseId, { url, detach: () => options?.signal?.removeEventListener("abort", abort), revoke: () => URL.revokeObjectURL(url) });
    options?.signal?.addEventListener("abort", abort, { once: true });
    if (options?.signal?.aborted) { this.releaseSource(leaseId); options.signal.throwIfAborted(); }
    return Object.freeze({ url, leaseId });
  }
  releaseSource(leaseId: string): void {
    const lease = this.leases.get(leaseId); if (!lease) return;
    this.leases.delete(leaseId); lease.detach(); lease.revoke();
  }
  async readText(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<string> {
    options?.signal?.throwIfAborted();
    if (!this.managed(resource) && resource.text !== undefined) return resource.text;
    if (!this.managed(resource) && resource.data) return new TextDecoder().decode(Uint8Array.from(atob(resource.data), character => character.charCodeAt(0)));
    const abort = new AbortController(); const cancel = () => abort.abort(options?.signal?.reason);
    options?.signal?.addEventListener("abort", cancel, { once: true });
    const timer = setTimeout(() => abort.abort(), 10_000); let source: ArtifactSourceV1 | undefined;
    try {
      source = await this.acquireSource(resource, { signal: abort.signal });
      const response = await fetch(source.url, { signal: abort.signal });
      if (!response.ok || !response.body) { await response.body?.cancel(); throw new Error("Preview unavailable"); }
      const reader = response.body.getReader(); const decoder = new TextDecoder(); let size = 0; let text = "";
      try {
        while (true) {
          const { done, value } = await reader.read(); if (done) break;
          size += value.byteLength; if (size > MAX_TEXT_PREVIEW_BYTES) throw new Error("Preview too large");
          text += decoder.decode(value, { stream: true });
        }
        text += decoder.decode(); abort.signal.throwIfAborted(); return text;
      } finally { await reader.cancel(); }
    } finally { clearTimeout(timer); options?.signal?.removeEventListener("abort", cancel); if (source?.leaseId) this.releaseSource(source.leaseId); }
  }
  private verifyManaged(resource: ArtifactResourceV1): void {
    if (!resource.uri || !resource.threadId || !/^[a-f0-9]{64}$/.test(resource.sha256 ?? "")) throw new Error("Artifact source identity is incomplete");
  }
  async save(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<void> {
    resource = this.original(resource);
    options?.signal?.throwIfAborted();
    if (this.managed(resource)) {
      const source = await this.acquireSource(resource, options);
      try {
        options?.signal?.throwIfAborted();
        if (!this.api.saveArtifactFile || this.api.unsupportedMethods?.includes("saveArtifactFile")) throw new Error("Selected artifact saving is unavailable");
        await this.api.saveArtifactFile(resource.name, source.url);
        options?.signal?.throwIfAborted();
      } finally { if (source.leaseId) this.releaseSource(source.leaseId); }
      return;
    }
    // Native save intentionally rejects blob URLs. Inline bytes keep their
    // original content in a protocol already verified by the existing handler.
    let source = this.resolveSource(resource) ?? (resource.data ? `data:${resource.mimeType};base64,${resource.data}`
      : resource.text !== undefined ? `data:${resource.mimeType};charset=utf-8,${encodeURIComponent(resource.text)}` : undefined);
    if (!source && resource.remoteRef) source = `data:${resource.mimeType};base64,${await this.readAttachment({ remoteRef: resource.remoteRef }, options)}`;
    if (!source) throw new Error("Artifact source unavailable");
    options?.signal?.throwIfAborted();
    if (this.api.saveArtifactFile && !this.api.unsupportedMethods?.includes("saveArtifactFile")) await this.api.saveArtifactFile(resource.name, source);
    else { const anchor = document.createElement("a"); anchor.href = source; anchor.download = resource.name; anchor.rel = "noopener"; anchor.click(); }
    options?.signal?.throwIfAborted();
  }
  async showItemMenu(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<void> {
    options?.signal?.throwIfAborted();
    if (!this.capabilities.nativeItemMenu || resource.remoteRef || !resource.uri) throw new Error("Native artifact menu unavailable");
    resource = this.sourceIdentity(resource);
    this.verifyManaged(resource); if (this.menuPending) return;
    this.menuPending = true;
    const requestId = crypto.randomUUID();
    const cancel = () => { void this.api.releaseArtifactSource?.(requestId).catch(() => undefined); };
    options?.signal?.addEventListener("abort", cancel, {once:true});
    try {
      const result = await this.api.showArtifactItemMenu!({ requestId, uri: resource.uri!, threadId: resource.threadId!, sha256: resource.sha256!, ...(resource.locator ? {locator:resource.locator} : {}) });
      options?.signal?.throwIfAborted(); if (result.action === "save") await this.save(resource, options);
    } finally { options?.signal?.removeEventListener("abort", cancel); this.menuPending = false; }
  }
  async openFile(resource: ArtifactResourceV1, options?: ArtifactCallOptionsV1): Promise<void> {
    options?.signal?.throwIfAborted(); if (!resource.cwd || !resource.uri) throw new Error("Workspace artifact unavailable");
    let path = resource.uri;
    if (path.startsWith("file:")) path = decodeURIComponent(new URL(path).pathname);
    await this.preview.openFile({ root: resource.cwd, path }, options);
  }
}
