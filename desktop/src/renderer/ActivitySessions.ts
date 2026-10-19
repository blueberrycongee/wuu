import type { ActivitySession, ServerEvent } from "../shared/protocol";
import { activityTimestamp } from "../shared/activityTimestamp";

export type ActivitySessionsState = {
  byKey: Record<string, ActivitySession>;
};

type NotificationServerEvent = Extract<ServerEvent, { kind: "notification" }>;

const ACTIVITY_NOTIFICATION_METHODS = new Set([
  "activity/started",
  "activity/updated",
  "activity/control_changed",
  "activity/stopped",
]);

export function serverEventCarriesActivitySessionUpdate(
  event: ServerEvent,
): event is NotificationServerEvent {
  return (
    event.kind === "notification" &&
    ACTIVITY_NOTIFICATION_METHODS.has(event.message.method)
  );
}

export function emptyActivitySessions(): ActivitySessionsState {
  return { byKey: {} };
}

export function reduceActivitySessionEvent(
  state: ActivitySessionsState,
  event: ServerEvent,
): ActivitySessionsState {
  if (!serverEventCarriesActivitySessionUpdate(event)) {
    return state;
  }
  const activity = activityFromUnknown(event.message.params);
  if (!activity || activity.workdir !== event.workdir) {
    return state;
  }
  return mergeActivity(state, activity);
}

export function mergeActivityList(
  state: ActivitySessionsState,
  workdir: string,
  threadID: string,
  activities: ActivitySession[],
): ActivitySessionsState {
  let next = state;
  for (const activity of activities) {
    if (activity.workdir === workdir && activity.thread_id === threadID) {
      next = mergeActivity(next, activity);
    }
  }
  return next;
}

export function activitiesForThread(
  state: ActivitySessionsState,
  workdir: string | undefined,
  threadID: string | undefined,
): ActivitySession[] {
  if (!workdir || !threadID) {
    return [];
  }
  return Object.values(state.byKey)
    .filter((activity) => activity.workdir === workdir && activity.thread_id === threadID)
    .sort((left, right) => {
      const timeDelta = Date.parse(left.created_at) - Date.parse(right.created_at);
      return timeDelta || left.id.localeCompare(right.id);
    });
}

// Hard-drop every activity for a workdir whose core was torn down / evicted.
// This deliberately bypasses mergeActivity's "stopped" stickiness and
// monotonic updated_at guard: the core is gone, so its (possibly lost)
// Close-time stopped events can never arrive, and leaving the entries would
// pin ghost browser activity UI forever. Identity is preserved when nothing
// matched so callers don't re-render on unrelated invalidate signals.
export function clearActivitiesForWorkdir(
  state: ActivitySessionsState,
  workdir: string,
): ActivitySessionsState {
  const kept: Record<string, ActivitySession> = {};
  let removed = false;
  for (const [key, activity] of Object.entries(state.byKey)) {
    if (activity.workdir === workdir) {
      removed = true;
      continue;
    }
    kept[key] = activity;
  }
  return removed ? { byKey: kept } : state;
}

function mergeActivity(state: ActivitySessionsState, incoming: ActivitySession): ActivitySessionsState {
  const key = activityKey(incoming);
  const current = state.byKey[key];
  if (current) {
    const incomingTime = activityTimestamp(incoming.updated_at);
    const currentTime = activityTimestamp(current.updated_at);
    if (incomingTime < currentTime) return state;
    const changedExecution = current.execution_id !== incoming.execution_id;
    // Activity IDs survive runs. Only a strictly newer, identified execution
    // can replace a stopped epoch; delayed snapshots cannot restore old control.
    if (changedExecution && (!incoming.execution_id || incomingTime <= currentTime)) return state;
    if (current.state === "stopped" && incoming.state !== "stopped" && !changedExecution) return state;
  }
  if (current === incoming) {
    return state;
  }
  return { byKey: { ...state.byKey, [key]: incoming } };
}

function activityKey(activity: ActivitySession): string {
  return `${activity.workdir}\u0000${activity.thread_id}\u0000${activity.id}`;
}

function activityFromUnknown(value: unknown): ActivitySession | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  if (
    typeof value.id !== "string" ||
    (value.kind !== "browser" && value.kind !== "cua") ||
    typeof value.thread_id !== "string" ||
    typeof value.workdir !== "string" ||
    typeof value.state !== "string" ||
    typeof value.controller !== "string" ||
    typeof value.created_at !== "string" ||
    typeof value.updated_at !== "string"
  ) {
    return undefined;
  }
  return value as ActivitySession;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
