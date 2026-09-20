import type { ExternalAgentActivity, ServerEvent } from "../shared/protocol";
import { engineLabel } from "./EngineDisplay";
import type {
  ComposerStatusItem,
  ComposerStatusState,
  RegisteredComposerStatusSource,
} from "./plugins/PluginHost";

const EMPTY_ITEMS: readonly ComposerStatusItem[] = Object.freeze([]);

type StoredActivity = Readonly<{
  turnId: string;
  item: ComposerStatusItem;
}>;

class ExternalAgentActivityStore {
  private readonly activitiesByThread = new Map<string, Map<string, StoredActivity>>();
  private readonly itemsByThread = new Map<string, readonly ComposerStatusItem[]>();
  private readonly listenersByThread = new Map<string, Set<() => void>>();

  ingest(event: ServerEvent): void {
    if (event.kind !== "notification") return;
    const params = asRecord(event.message.params);
    const threadId = stringField(params, "thread_id");
    const turnId = stringField(params, "turn_id");
    if (!threadId) return;

    switch (event.message.method) {
      case "turn/started":
        this.clearThread(threadId);
        return;
      case "turn/completed":
      case "turn/error":
        if (turnId) this.clearTurn(threadId, turnId);
        else this.clearThread(threadId);
        return;
      case "turn/event": {
        if (!turnId) return;
        const streamEvent = asRecord(params.event);
        if (streamEvent.type !== "agent_activity") return;
        const activity = parseActivity(streamEvent.agent_activity);
        if (activity) this.applyActivity(threadId, turnId, activity);
        return;
      }
    }
  }

  getSnapshot(threadId?: string): readonly ComposerStatusItem[] {
    return threadId ? this.itemsByThread.get(threadId) ?? EMPTY_ITEMS : EMPTY_ITEMS;
  }

  subscribe(threadId: string | undefined, listener: () => void): () => void {
    if (!threadId) return () => {};
    let listeners = this.listenersByThread.get(threadId);
    if (!listeners) {
      listeners = new Set();
      this.listenersByThread.set(threadId, listeners);
    }
    listeners.add(listener);
    return () => {
      listeners?.delete(listener);
      if (listeners?.size === 0) this.listenersByThread.delete(threadId);
    };
  }

  private applyActivity(threadId: string, turnId: string, activity: ExternalAgentActivity): void {
    let activities = this.activitiesByThread.get(threadId);
    if (!activities) {
      activities = new Map();
      this.activitiesByThread.set(threadId, activities);
    }
    const key = `${turnId}:${activity.engine}:${activity.id}`;
    if (activity.state === "completed") {
      if (!activities.delete(key)) return;
    } else {
      const state = composerState(activity.state);
      activities.set(key, Object.freeze({
        turnId,
        item: Object.freeze({
          id: key,
          label: activity.label,
          state,
          secondaryText: activity.state,
          tooltip: `${engineLabel(activity.engine)} native agent`,
          updatedAt: new Date().toISOString(),
        }),
      }));
    }
    this.publish(threadId, activities);
  }

  private clearTurn(threadId: string, turnId: string): void {
    const activities = this.activitiesByThread.get(threadId);
    if (!activities) return;
    let changed = false;
    for (const [key, activity] of activities) {
      if (activity.turnId === turnId) {
        activities.delete(key);
        changed = true;
      }
    }
    if (changed) this.publish(threadId, activities);
  }

  private clearThread(threadId: string): void {
    if (!this.activitiesByThread.delete(threadId)) return;
    this.itemsByThread.delete(threadId);
    this.notify(threadId);
  }

  private publish(threadId: string, activities: Map<string, StoredActivity>): void {
    if (activities.size === 0) {
      this.activitiesByThread.delete(threadId);
      this.itemsByThread.delete(threadId);
    } else {
      this.itemsByThread.set(
        threadId,
        Object.freeze(Array.from(activities.values(), ({ item }) => item)),
      );
    }
    this.notify(threadId);
  }

  private notify(threadId: string): void {
    for (const listener of this.listenersByThread.get(threadId) ?? []) listener();
  }
}

function parseActivity(value: unknown): ExternalAgentActivity | undefined {
  const activity = asRecord(value);
  const id = stringField(activity, "id");
  const engine = stringField(activity, "engine");
  const label = stringField(activity, "label");
  const state = stringField(activity, "state");
  if (!id || !label || !engine || engine === "wuu") return undefined;
  if (state !== "queued" && state !== "running" && state !== "waiting"
    && state !== "failed" && state !== "completed") return undefined;
  return { id, engine, label, state };
}

function composerState(state: ExternalAgentActivity["state"]): ComposerStatusState {
  return state === "failed" ? "error" : state === "completed" ? "idle" : state;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

function stringField(value: Record<string, unknown>, key: string): string | undefined {
  const field = value[key];
  return typeof field === "string" && field.trim() ? field.trim() : undefined;
}

export const externalAgentActivityStore = new ExternalAgentActivityStore();

export const externalAgentActivityStatusSource: RegisteredComposerStatusSource = {
  id: "external-agent-activity",
  pluginId: "wuu.host",
  generation: "host",
  order: 30,
  getSnapshot: (context) => externalAgentActivityStore.getSnapshot(context.threadId),
  subscribe: (context, listener) => externalAgentActivityStore.subscribe(context.threadId, listener),
};
