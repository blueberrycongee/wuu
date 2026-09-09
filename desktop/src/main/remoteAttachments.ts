import { createHash } from "node:crypto";

const CHUNK_CHARS = 128 * 1024;
const CACHE_CHARS = 128 * 1024 * 1024;
type Location = { thread?: string; turn?: string; item?: string; index?: number; kind?: "result" };

export function threadAttachmentParams(ref: string): { thread_id: string; turn_id: string; item_id: string; index: number; sha256: string; kind?: "result" } | undefined {
  if (!ref.startsWith("thread:")) return undefined;
  const value: unknown = JSON.parse(Buffer.from(ref.slice(7), "base64url").toString());
  if (!Array.isArray(value) || (value.length !== 5 && value.length !== 6) || (value.length === 6 && value[5] !== "result") || value.slice(0, 3).some(part => typeof part !== "string" || !part) || !Number.isSafeInteger(value[3]) || value[3] < 0 || typeof value[4] !== "string" || !/^[a-f0-9]{64}$/.test(value[4])) throw new Error("Invalid thread attachment reference");
  return { thread_id: value[0], turn_id: value[1], item_id: value[2], index: value[3], sha256: value[4], ...(value[5] === "result" ? {kind: "result" as const} : {}) };
}

export function threadContentParams(ref: string): { thread_id: string; turn_id: string; item_id: string; sha256: string } {
  if (!ref.startsWith("content:")) throw new Error("Invalid content reference");
  const value: unknown = JSON.parse(Buffer.from(ref.slice(8), "base64url").toString());
  if (!Array.isArray(value) || value.length !== 4 || value.some(part => typeof part !== "string" || !part) || !/^[a-f0-9]{64}$/.test(value[3])) throw new Error("Invalid content reference");
  return { thread_id: value[0], turn_id: value[1], item_id: value[2], sha256: value[3] };
}

/** Attachment bytes stay on the trusted desktop. References travel only inside
 * the authenticated, encrypted RPC connection, never as public download URLs. */
export class RemoteAttachments {
  private readonly entries = new Map<string, string>();
  private size = 0;

  project(value: unknown, location: Location = {}): unknown {
    if (Array.isArray(value)) return value.map(item => this.project(item, location));
    if (!value || typeof value !== "object") return value;
    const record = value as Record<string, unknown>;
    if (typeof record.thread_id === "string") location = { ...location, thread: record.thread_id };
    if (typeof record.turn_id === "string") location = { ...location, turn: record.turn_id };
    if (typeof record.id === "string" && Array.isArray(record.turns)) location = { thread: record.id };
    if (typeof record.id === "string" && Array.isArray(record.items)) location = { ...location, turn: record.id };
    if (typeof record.id === "string" && (Array.isArray(record.images) || record.type === "tool_call")) location = { ...location, item: record.id };
    const mediaType = record.media_type ?? record.mime_type;
    if (typeof mediaType === "string" && mediaType.startsWith("image/") && typeof record.data === "string" && record.data.length > 16 * 1024) {
      const ref = createHash("sha256").update(mediaType).update("\0").update(record.data).digest("hex");
      if (location.thread && location.turn && location.item && location.index !== undefined) {
        return { ...record, data: "", remote_ref: "thread:" + Buffer.from(JSON.stringify([location.thread, location.turn, location.item, location.index, ref, ...(location.kind ? [location.kind] : [])])).toString("base64url") };
      }
      if (!this.entries.has(ref)) {
        this.entries.set(ref, record.data);
        this.size += record.data.length;
      }
      this.trim(ref);
      return { ...record, data: "", remote_ref: ref };
    }
    return Object.fromEntries(Object.entries(record).map(([key, item]) => [key,
      (key === "images" || (key === "content" && location.kind === "result")) && Array.isArray(item) ? item.map((image, index) => this.project(image, { ...location, index })) : this.project(item, key === "result_detail" ? { ...location, kind: "result" } : location)]));
  }

  async hydrateRemote(value: unknown, read: (ref: string) => Promise<string>): Promise<unknown> {
    if (Array.isArray(value)) return Promise.all(value.map(item => this.hydrateRemote(item, read)));
    if (!value || typeof value !== "object") return value;
    const record = value as Record<string, unknown>;
    if (typeof record.remote_ref === "string" && record.remote_ref.startsWith("thread:") && typeof record.media_type === "string" && record.media_type.startsWith("image/")) {
      const { remote_ref, ...image } = record;
      return { ...image, data: typeof image.data === "string" && image.data ? image.data : await read(remote_ref) };
    }
    return this.hydrate(Object.fromEntries(await Promise.all(Object.entries(record).map(async ([key, item]) => [key, await this.hydrateRemote(item, read)]))));
  }

  hydrate(value: unknown): unknown {
    if (Array.isArray(value)) return value.map(item => this.hydrate(item));
    if (!value || typeof value !== "object") return value;
    const record = value as Record<string, unknown>;
    if (typeof record.remote_ref === "string" && typeof record.media_type === "string" && record.media_type.startsWith("image/")) {
      const { remote_ref, ...image } = record;
      return { ...image, data: typeof image.data === "string" && image.data ? image.data : this.get(remote_ref) };
    }
    return Object.fromEntries(Object.entries(record).map(([key, item]) => [key, this.hydrate(item)]));
  }

  read(params: unknown): { data: string; total: number; offset: number } {
    const { ref, offset = 0 } = (params ?? {}) as { ref?: unknown; offset?: unknown };
    if (typeof ref !== "string" || !Number.isSafeInteger(offset) || (offset as number) < 0) throw new Error("Invalid attachment offset or reference");
    const data = this.get(ref);
    if ((offset as number) > data.length) throw new Error("Attachment offset exceeds its size");
    return { data: data.slice(offset as number, (offset as number) + CHUNK_CHARS), total: data.length, offset: offset as number };
  }

  clear(): void { this.entries.clear(); this.size = 0; }

  private get(ref: string): string {
    const data = this.entries.get(ref);
    if (data === undefined) throw new Error("Attachment expired; reopen the conversation to reload it");
    this.entries.delete(ref); this.entries.set(ref, data);
    return data;
  }

  private trim(retain: string): void {
    while ((this.size > CACHE_CHARS || this.entries.size > 2048) && this.entries.size > 1) {
      const key = this.entries.keys().next().value!;
      if (key === retain) { this.get(key); continue; }
      this.size -= this.entries.get(key)!.length;
      this.entries.delete(key);
    }
  }
}
