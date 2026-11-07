// The app's single state store: a notification reducer over the app-server
// stream plus purely-local UI state (unread cursors, optimistic sends).
// Reducer semantics mirror the desktop AppState:
//   - thread/* notifications upsert whole threads, but an update with empty
//     turns must not wipe locally-known turns;
//   - item snapshots upsert by id, and a completed snapshot must never
//     truncate longer text that already streamed in via deltas;
// Pure TS (no react-native imports) so the whole reducer is vitest-able.

import type { Thread, ThreadItem, Turn } from "@wuu/protocol";

import { isVisibleThread } from "./threads";

/** Server item ids end in "-item-N"; that suffix is the chronological order.
 *  Mirrored from the desktop TurnOrdering: merge paths append surviving rows
 *  to the tail, so a snapshot missing a locally-known item must not reorder it
 *  below later content. Rows without the suffix keep their arrival order. */
function turnItemOrder(id: string): number | undefined {
  const match = id.match(/-item-(\d+)$/);
  if (!match) return undefined;
  const order = Number.parseInt(match[1], 10);
  return Number.isFinite(order) ? order : undefined;
}

function orderedTurnItems(items: ThreadItem[]): ThreadItem[] {
  const indexed = items.map((item, index) => ({ item, index, order: turnItemOrder(item.id) }));
  if (indexed.some((entry) => entry.order === undefined)) return items.slice();
  return indexed
    .sort((left, right) => (left.order ?? left.index) - (right.order ?? right.index) || left.index - right.index)
    .map((entry) => entry.item);
}

export type ConnectionPhase = "idle" | "connecting" | "attached" | "reconnecting";

export type PendingSend = {
  clientId: string;
  threadId: string;
  text: string;
  atMs: number;
  queued: boolean; // true once turn/queue acked it server-side
};

export type WorkspaceInfo = {
  id?: string;
  name: string;
  path: string;
};

export type AppSnapshot = {
  phase: ConnectionPhase;
  syncError: string | null;
  hostName: string;
  workdir: string;
  workspaces: WorkspaceInfo[];
  activeWorkspacePath: string;
  threads: Thread[];
  lastViewed: Readonly<Record<string, string>>;
  pending: PendingSend[];
  activeThreadId: string | null;
};

type Listener = () => void;

export class AppStore {
  private threads = new Map<string, Thread>();
  private lastViewed: Record<string, string> = {};
  private pending: PendingSend[] = [];
  private phase: ConnectionPhase = "idle";
  private syncError: string | null = null;
  private hostName = "";
  private workdir = "";
  private workspaces: WorkspaceInfo[] = [];
  private activeWorkspacePath = "";
  private activeThreadId: string | null = null;
  private listeners = new Set<Listener>();
  private snapshot: AppSnapshot | null = null;
  /** Thread ids referenced by notifications but unknown locally — the
   *  controller watches this to schedule a thread/list refresh. */
  onUnknownThread: ((threadId: string) => void) | null = null;
  /** The unread cursor moved — the controller persists it. */
  onLastViewedChanged: ((lastViewed: Readonly<Record<string, string>>) => void) | null = null;

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getSnapshot = (): AppSnapshot => {
    if (!this.snapshot) {
      this.snapshot = {
        phase: this.phase,
        syncError: this.syncError,
        hostName: this.hostName,
        workdir: this.workdir,
        workspaces: [...this.workspaces],
        activeWorkspacePath: this.activeWorkspacePath,
        threads: [...this.threads.values()],
        lastViewed: { ...this.lastViewed },
        pending: [...this.pending],
        activeThreadId: this.activeThreadId,
      };
    }
    return this.snapshot;
  };

  private bump(): void {
    this.snapshot = null;
    for (const listener of this.listeners) listener();
  }

  // --- connection ------------------------------------------------------------

  setPhase(phase: ConnectionPhase): void {
    if (this.phase === phase) return;
    this.phase = phase;
    this.bump();
  }

  setSyncError(message: string | null): void {
    if (this.syncError === message) return;
    this.syncError = message;
    this.bump();
  }

  setHostName(name: string): void {
    if (this.hostName === name) return;
    this.hostName = name;
    this.bump();
  }

  setWorkdir(workdir: string): void {
    if (this.workdir === workdir) return;
    this.workdir = workdir;
    this.bump();
  }

  setWorkspaces(workspaces: WorkspaceInfo[]): void {
    this.workspaces = workspaces;
    this.bump();
  }

  setActiveWorkspacePath(path: string): void {
    if (this.activeWorkspacePath === path) return;
    this.activeWorkspacePath = path;
    this.bump();
  }

  /** Fresh app-server connection (attach resumed=false): every in-memory
   *  mirror of server state is stale; local-only state survives. */
  resetServerState(): void {
    this.threads.clear();
    this.pending = [];
    this.workdir = "";
    this.workspaces = [];
    this.activeWorkspacePath = "";
    this.bump();
  }

  // --- server-state ingestion --------------------------------------------------

  setThreads(threads: Thread[]): void {
    const previous = this.threads;
    this.threads = new Map();
    for (const thread of threads) {
      if (!isVisibleThread(thread)) continue;
      // thread/list entries carry summaries with empty turns for threads the
      // server no longer has loaded — full histories loaded via
      // thread/resume must survive a list refresh.
      const existing = previous.get(thread.id);
      if (existing && (!thread.turns || thread.turns.length === 0) && (existing.turns?.length ?? 0) > 0) {
        this.threads.set(thread.id, { ...thread, turns: existing.turns });
      } else {
        this.threads.set(thread.id, thread);
      }
    }
    this.bump();
  }

  setActiveThread(threadId: string | null): void {
    this.activeThreadId = threadId;
    if (threadId) this.markViewed(threadId);
    this.bump();
  }

  /** Seeds persisted unread cursors on startup (never overwrites newer
   *  in-memory state — call before the first markViewed). */
  seedLastViewed(lastViewed: Record<string, string>): void {
    this.lastViewed = { ...lastViewed, ...this.lastViewed };
    this.bump();
  }

  /** Advance the local unread cursor to the thread's newest completed turn. */
  markViewed(threadId: string): void {
    const thread = this.threads.get(threadId);
    if (!thread) return;
    const turns = thread.turns ?? [];
    for (let i = turns.length - 1; i >= 0; i--) {
      if (turns[i].status !== "in_progress") {
        if (this.lastViewed[threadId] !== turns[i].id) {
          this.lastViewed = { ...this.lastViewed, [threadId]: turns[i].id };
          this.onLastViewedChanged?.(this.lastViewed);
          this.bump();
        }
        return;
      }
    }
  }

  // --- optimistic sends ----------------------------------------------------------

  addPending(send: PendingSend): void {
    this.pending = [...this.pending, send];
    this.bump();
  }

  markPendingQueued(clientId: string): void {
    this.pending = this.pending.map((p) => (p.clientId === clientId ? { ...p, queued: true } : p));
    this.bump();
  }

  removePending(clientId: string): void {
    const next = this.pending.filter((p) => p.clientId !== clientId);
    if (next.length !== this.pending.length) {
      this.pending = next;
      this.bump();
    }
  }

  // --- notification reducer ---------------------------------------------------

  applyNotification(method: string, params: unknown): void {
    const p = (params ?? {}) as Record<string, unknown>;
    switch (method) {
      case "thread/started":
      case "thread/resumed":
      case "thread/updated": {
        const thread = p.thread as Thread | undefined;
        if (thread) this.upsertThread(thread);
        return;
      }
      case "turn/started": {
        const threadId = p.thread_id as string;
        const turn = p.turn as Turn | undefined;
        if (turn) this.upsertTurn(threadId, turn);
        const queueId = p.queue_id as string | undefined;
        if (queueId) this.removePending(queueId);
        return;
      }
      case "turn/completed":
      case "turn/error": {
        const threadId = p.thread_id as string;
        const turn = p.turn as Turn | undefined;
        if (turn) this.upsertTurn(threadId, turn);
        return;
      }
      case "turn/dequeued": {
        const queueId = p.queue_id as string | undefined;
        if (queueId) this.removePending(queueId);
        return;
      }
      case "item/started":
      case "item/completed": {
        const item = p.item as ThreadItem | undefined;
        if (item) {
          this.upsertItem(p.thread_id as string, p.turn_id as string, item);
          // A queued send has landed once its user_message echoes back with
          // our client id as source_id.
          if (method === "item/completed" && item.type === "user_message" && item.source_id) {
            this.removePending(item.source_id);
          }
        }
        return;
      }
      case "item/removed": {
        const itemId = p.item_id as string | undefined;
        if (itemId) this.removeItem(p.thread_id as string, p.turn_id as string, itemId);
        return;
      }
      case "item/agentMessage/delta":
      case "item/reasoning/delta":
        this.patchItemText(p.thread_id as string, p.turn_id as string, p.item_id as string, (old) =>
          (old ?? "") + String(p.delta ?? ""),
        );
        return;
      case "item/agentMessage/replace":
      case "item/reasoning/replace":
        this.patchItemText(
          p.thread_id as string,
          p.turn_id as string,
          p.item_id as string,
          () => String(p.text ?? ""),
        );
        return;
      default:
        return; // everything else is safely ignorable on mobile
    }
  }

  private upsertThread(incoming: Thread): void {
    if (!isVisibleThread(incoming)) {
      // A thread may transition out of visibility (archived/read-only): drop it.
      if (this.threads.delete(incoming.id)) this.bump();
      return;
    }
    const existing = this.threads.get(incoming.id);
    const merged: Thread = { ...incoming };
    // thread/updated may carry an empty turns array — keep local history.
    if (existing && (!incoming.turns || incoming.turns.length === 0)) {
      merged.turns = existing.turns;
    }
    this.threads.set(incoming.id, merged);
    this.bump();
  }

  private upsertTurn(threadId: string, turn: Turn): void {
    const thread = this.threads.get(threadId);
    if (!thread) {
      this.onUnknownThread?.(threadId);
      return;
    }
    const turns = [...(thread.turns ?? [])];
    const index = turns.findIndex((t) => t.id === turn.id);
    if (index >= 0) {
      turns[index] = this.mergeTurn(turns[index], turn);
    } else {
      turns.push(turn);
    }
    const running = turns.some((t) => t.status === "in_progress");
    // Never stamp updated_at with the phone's clock: use the turn's own
    // server timestamps, and only move forward (a redelivered old turn must
    // not reorder the list).
    const turnStamp = turn.completed_at ?? turn.started_at ?? undefined;
    const existingStamp = thread.updated_at ?? thread.created_at;
    const updatedAt =
      turnStamp && Date.parse(turnStamp) > (Date.parse(existingStamp) || 0) ? turnStamp : existingStamp;
    this.threads.set(threadId, {
      ...thread,
      turns,
      status: running ? "in_progress" : "idle",
      updated_at: updatedAt,
    });
    this.bump();
  }

  private upsertItem(threadId: string, turnId: string, item: ThreadItem): void {
    const thread = this.threads.get(threadId);
    if (!thread) {
      this.onUnknownThread?.(threadId);
      return;
    }
    const turns = [...(thread.turns ?? [])];
    let index = turns.findIndex((t) => t.id === turnId);
    if (index < 0) {
      turns.push({ id: turnId, items: [], items_view: "full", status: "in_progress" });
      index = turns.length - 1;
    }
    const turn = turns[index];
    const items = [...(turn.items ?? [])];
    const itemIndex = items.findIndex((i) => i.id === item.id);
    if (itemIndex >= 0) {
      items[itemIndex] = this.mergeItem(items[itemIndex], item);
    } else {
      items.push(item);
    }
    turns[index] = { ...turn, items: orderedTurnItems(items) };
    this.threads.set(threadId, { ...thread, turns });
    this.bump();
  }

  private removeItem(threadId: string, turnId: string, itemId: string): void {
    const thread = this.threads.get(threadId);
    if (!thread) {
      this.onUnknownThread?.(threadId);
      return;
    }
    const turns = (thread.turns ?? []).map((turn) =>
      turn.id === turnId
        ? { ...turn, items: (turn.items ?? []).filter((item) => item.id !== itemId) }
        : turn,
    );
    this.threads.set(threadId, { ...thread, turns });
    this.bump();
  }

  /** Turn snapshot merge: item-level text preservation, plus locally-known
   *  items missing from the snapshot survive (an item that arrived via
   *  item/completed must not vanish because a later turn snapshot lags). */
  private mergeTurn(existing: Turn, incoming: Turn): Turn {
    if (!incoming.items?.length) {
      return { ...incoming, items: existing.items ?? [] };
    }
    const merged = incoming.items.map((item) =>
      this.mergeItem(existing.items?.find((i) => i.id === item.id), item),
    );
    const seen = new Set(merged.map((i) => i.id));
    for (const item of existing.items ?? []) {
      if (!seen.has(item.id)) merged.push(item);
    }
    return { ...incoming, items: orderedTurnItems(merged) };
  }

  /** Snapshot upsert that never lets a shorter completed snapshot truncate
   *  text that already streamed in. */
  private mergeItem(existing: ThreadItem | undefined, incoming: ThreadItem): ThreadItem {
    if (!existing) return incoming;
    const keepLonger =
      existing.text !== undefined &&
      incoming.text !== undefined &&
      incoming.text.length < existing.text.length;
    return { ...existing, ...incoming, text: keepLonger ? existing.text : incoming.text };
  }

  private patchItemText(
    threadId: string,
    turnId: string,
    itemId: string,
    update: (old: string | undefined) => string,
  ): void {
    const thread = this.threads.get(threadId);
    if (!thread) return;
    const turns = [...(thread.turns ?? [])];
    const index = turns.findIndex((t) => t.id === turnId);
    if (index < 0) return;
    const items = [...(turns[index].items ?? [])];
    const itemIndex = items.findIndex((i) => i.id === itemId);
    if (itemIndex < 0) return;
    items[itemIndex] = { ...items[itemIndex], text: update(items[itemIndex].text) };
    turns[index] = { ...turns[index], items };
    this.threads.set(threadId, { ...thread, turns });
    this.bump();
  }
}
