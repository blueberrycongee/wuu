import { accountRequest, AccountRequestError, type AccountSession } from '@wuu/remote-core';

export type HistorySettings = { host: string; enabled: boolean; generation: string };
export type HistoryEntry = { id: string; title: string; updated_at: string; revision: string; digest: string; deleted: boolean };
export type HistoryThread = { id: string; title: string; updated_at: string; status?: string; messages: { id: string; turn_id: string; role: 'user' | 'assistant'; text: string }[] };
type Body = { thread: HistoryThread; revision: string };
type Page = HistorySettings & { entries: HistoryEntry[]; cursor: string; more: boolean };
export type HistoryCache = { settings?: HistorySettings; cursor: string; entries: Record<string, HistoryEntry>; bodies: Record<string, Body>; checkedAt?: number; selectedID?: string; storageError?: string };
export const emptyHistory = (): HistoryCache => ({ cursor: '0', entries: {}, bodies: {} });
let epoch = 0;

function database(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open('wuu-conversations-v1', 1);
    request.onupgradeneeded = () => request.result.createObjectStore('hosts');
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}
async function storage<T>(mode: IDBTransactionMode, action: (store: IDBObjectStore) => IDBRequest<T>, valid = () => true): Promise<T | undefined> {
  const db = await database();
  try {
    if (!valid()) return undefined;
    return await new Promise<T>((resolve, reject) => {
      const tx = db.transaction('hosts', mode);
      const request = action(tx.objectStore('hosts'));
      tx.oncomplete = () => resolve(request.result);
      tx.onabort = tx.onerror = () => reject(tx.error || request.error);
    });
  } finally { db.close(); }
}
export async function clearConversationCaches(): Promise<void> {
  // Invalidate readers before awaiting IndexedDB, so pending HTTP responses
  // cannot repopulate the cache after logout/revocation.
  epoch++;
  if (typeof indexedDB !== 'undefined') await storage('readwrite', store => store.clear());
}

export function mergeHistoryPage(current: HistoryCache, page: Page): HistoryCache {
  const next = current.settings?.generation === page.generation ? structuredClone(current) : emptyHistory();
  next.settings = { host: page.host, enabled: page.enabled, generation: page.generation };
  if (!page.enabled) return { ...emptyHistory(), settings: next.settings };
  for (const entry of page.entries) {
    if (entry.deleted) { delete next.entries[entry.id]; delete next.bodies[entry.id]; if (next.selectedID === entry.id) delete next.selectedID; }
    else Object.defineProperty(next.entries, entry.id, { value: entry, enumerable: true, writable: true, configurable: true });
  }
  next.cursor = page.cursor;
  return next;
}

/** One serialized cache writer per mounted host. Metadata and cursor commit
 * together; body revisions prevent partially downloaded pages looking fresh. */
export class ConversationHistory {
  state = emptyHistory();
  private readonly epoch = epoch;
  private closed = false;
  private queue: Promise<unknown> = Promise.resolve();
  private readonly key: string;
  constructor(private readonly session: AccountSession, readonly host: string, private readonly changed: (state: HistoryCache) => void, private readonly unauthorized?: () => Promise<void>) {
    this.key = JSON.stringify([session.server, session.username, session.pub, host]);
  }
  private valid = () => !this.closed && this.epoch === epoch;
  close(): void { this.closed = true; }
  private serial<T>(work: () => Promise<T>): Promise<T> {
    const next = this.queue.then(async () => { if (!this.valid()) throw new Error('历史读取已结束'); return work(); });
    this.queue = next.catch(() => {}); return next;
  }
  private emit(): void { if (this.valid()) this.changed(this.state); }
  private async save(): Promise<void> {
    if (!this.valid()) return;
    try { await storage('readwrite', store => store.put(this.state, this.key), this.valid); }
    catch (error) { this.state = { ...this.state, storageError: String(error) }; }
    this.emit();
  }
  private async request<T>(method: string, path: string, data?: unknown): Promise<T> {
    try {
      const result = await accountRequest<T>(this.session.server, this.session.token, method, path, data);
      if (!this.valid()) throw new Error('历史读取已结束');
      return result;
    } catch (error) {
      if (error instanceof AccountRequestError && error.status === 401 && this.valid()) {
        this.state = emptyHistory(); this.emit(); this.closed = true;
        try { await storage('readwrite', store => store.delete(this.key)); }
        finally { await this.unauthorized?.(); }
      }
      throw error;
    }
  }
  load(): Promise<void> {
    return this.serial(async () => {
      try {
        const cached = await storage<HistoryCache | undefined>('readonly', store => store.get(this.key));
        if (this.valid() && cached) { this.state = cached; this.emit(); }
      } catch (error) { this.state = { ...this.state, storageError: String(error) }; this.emit(); }
    });
  }
  sync(active?: string): Promise<void> {
    return this.serial(async () => {
      active ||= this.state.selectedID;
      let more = true;
      while (more) {
        const query = new URLSearchParams({ host: this.host, generation: this.state.settings?.generation || '', after: this.state.cursor });
        const page = await this.request<Page>('GET', '/history?' + query);
        this.state = mergeHistoryPage(this.state, page);
        await this.save(); more = page.more && page.enabled;
      }
      const retained = new Set(Object.keys(this.state.bodies));
      const newest = Object.values(this.state.entries).sort((a, b) => b.updated_at.localeCompare(a.updated_at))[0];
      if (active) retained.add(active); else if (newest) retained.add(newest.id);
      for (const id of retained) await this.fetchBody(id);
      this.state = { ...this.state, checkedAt: Date.now() }; await this.save();
    });
  }
  read(id: string): Promise<void> {
    return this.serial(async () => {
      this.state = { ...this.state, selectedID: id }; await this.save();
      await this.fetchBody(id);
    });
  }
  private async fetchBody(id: string): Promise<void> {
    const entry = this.state.entries[id], settings = this.state.settings;
    if (!entry || !settings?.enabled || this.state.bodies[id]?.revision === entry.revision) return;
    const query = new URLSearchParams({ host: this.host, generation: settings.generation, id });
    const body = await this.request<Body>('GET', '/history/thread?' + query);
    this.state = { ...this.state, bodies: { ...this.state.bodies, [id]: body } };
    // Bound retained text to 20 snapshots and 16 MiB (UTF-16 estimate).
    let bytes = 0, count = 0;
    const ids = [id, ...Object.keys(this.state.bodies).filter(key => key !== id).reverse()];
    for (const key of ids) {
      bytes += JSON.stringify(this.state.bodies[key]).length * 2;
      if (++count > 20 || bytes > 16 * 1024 * 1024) delete this.state.bodies[key];
    }
    await this.save();
  }
  setEnabled(enabled: boolean): Promise<void> {
    return this.serial(async () => {
      const settings = await this.request<HistorySettings>('POST', '/history/settings', { host: this.host, enabled });
      this.state = { ...emptyHistory(), settings }; await this.save();
    });
  }
}
