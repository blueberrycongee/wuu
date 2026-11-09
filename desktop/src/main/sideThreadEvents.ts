import type {
  ServerEvent,
  SideThreadEvent,
  SideThreadEventEnvelope,
  SideThreadMessage,
  SideThreadSummary,
  ThreadItem
} from "../shared/protocol";

const SIDE_THREAD_EVENT_METHOD = "sideThread/event";
const SIDE_THREAD_EVENT_TYPES = new Set<SideThreadEvent["type"]>([
  "status",
  "delta",
  "items",
  "message",
  "error",
  "reset"
]);

export function sideThreadEventFromServerEvent(
  event: ServerEvent
): SideThreadEventEnvelope | undefined {
  if (
    event.kind !== "notification" ||
    event.message.method !== SIDE_THREAD_EVENT_METHOD
  ) {
    return undefined;
  }
  const params = event.message.params;
  if (!hasEventBase(params)) {
    return undefined;
  }
  switch (params.type) {
    case "status":
      return isSideThreadSummary(params.summary) &&
        params.summary.side_thread_id === params.side_thread_id &&
        params.summary.main_thread_id === params.main_thread_id
        ? { workdir: event.workdir, event: params as SideThreadEvent }
        : undefined;
    case "delta":
      return isRevision(params.revision) &&
        typeof params.message_id === "string" &&
        typeof params.text_delta === "string"
        ? { workdir: event.workdir, event: params as SideThreadEvent }
        : undefined;
    case "items":
      return isRevision(params.revision) &&
        typeof params.message_id === "string" &&
        Array.isArray(params.items) &&
        params.items.every(isThreadItem)
        ? { workdir: event.workdir, event: params as SideThreadEvent }
        : undefined;
    case "message":
      return isRevision(params.revision) &&
        isSideThreadMessage(params.message) &&
        params.message.side_thread_id === params.side_thread_id
        ? { workdir: event.workdir, event: params as SideThreadEvent }
        : undefined;
    case "error":
      return isRevision(params.revision) &&
        typeof params.message_id === "string" &&
        typeof params.error_message === "string"
        ? { workdir: event.workdir, event: params as SideThreadEvent }
        : undefined;
    case "reset":
      return { workdir: event.workdir, event: params as SideThreadEvent };
  }
}

function hasEventBase(value: unknown): value is Record<string, unknown> & {
  type: SideThreadEvent["type"];
  side_thread_id: string;
  main_thread_id: string;
} {
  return (
    isRecord(value) &&
    SIDE_THREAD_EVENT_TYPES.has(value.type as SideThreadEvent["type"]) &&
    typeof value.side_thread_id === "string" &&
    typeof value.main_thread_id === "string"
  );
}

function isSideThreadSummary(value: unknown): value is SideThreadSummary {
  if (!isRecord(value)) {
    return false;
  }
  const mainTask = value.main_task_summary;
  return (
    typeof value.side_thread_id === "string" &&
    typeof value.main_thread_id === "string" &&
    isSideThreadStatus(value.status) &&
    isRevision(value.revision) &&
    typeof value.created_at === "string" &&
    typeof value.updated_at === "string" &&
    (mainTask === undefined ||
      (isRecord(mainTask) &&
        typeof mainTask.running === "boolean" &&
        (mainTask.last_user_message === undefined ||
          typeof mainTask.last_user_message === "string")))
  );
}

function isSideThreadMessage(value: unknown): value is SideThreadMessage {
  return (
    isRecord(value) &&
    typeof value.id === "string" &&
    typeof value.side_thread_id === "string" &&
    (value.role === "user" || value.role === "assistant") &&
    typeof value.text === "string" &&
    (value.items === undefined ||
      (Array.isArray(value.items) && value.items.every(isThreadItem))) &&
    typeof value.created_at === "string" &&
    (value.status === undefined ||
      value.status === "streaming" ||
      value.status === "completed" ||
      value.status === "failed" ||
      value.status === "interrupted") &&
    (value.error_message === undefined || typeof value.error_message === "string")
  );
}

function isThreadItem(value: unknown): value is ThreadItem {
  if (!isRecord(value) || typeof value.id !== "string") {
    return false;
  }
  const types = new Set<ThreadItem["type"]>([
    "user_message",
    "agent_message",
    "reasoning",
    "tool_call",
    "context_compaction",
    "error"
  ]);
  return (
    types.has(value.type as ThreadItem["type"]) &&
    (value.status === undefined ||
      value.status === "in_progress" ||
      value.status === "completed" ||
      value.status === "failed")
  );
}

function isSideThreadStatus(value: unknown): boolean {
  return (
    value === "running" ||
    value === "completed" ||
    value === "failed" ||
    value === "interrupted"
  );
}

function isRevision(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
