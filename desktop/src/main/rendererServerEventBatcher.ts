import type { ServerEvent } from "../shared/protocol";

const STREAM_DELTA_NOTIFICATION_METHODS = new Set([
  "item/agentMessage/delta",
  "item/reasoning/delta",
  "item/toolCall/delta",
  "item/toolCall/outputDelta",
]);
const RENDERER_IGNORED_STREAM_EVENT_TYPES = new Set([
  "content_delta",
  "thinking_delta",
  "tool_use_delta",
]);
const DEFAULT_STREAM_BATCH_INTERVAL_MS = 50;

export class RendererServerEventBatcher {
  private pendingStreamEvents: ServerEvent[] = [];
  private pendingFlush: NodeJS.Timeout | undefined;

  constructor(
    private readonly emit: (event: ServerEvent) => void,
    private readonly intervalMs = DEFAULT_STREAM_BATCH_INTERVAL_MS,
  ) {}

  enqueue(event: ServerEvent): void {
    // The renderer deliberately ignores these raw provider events; it consumes
    // the normalized item/* delta emitted beside them. Do not duplicate every
    // token across Electron IPC merely for a handler to discard it.
    if (rendererIgnoresStreamEvent(event)) {
      return;
    }

    const identity = streamDeltaIdentity(event);
    if (!identity) {
      // Terminal and control notifications are ordering barriers. Flush all
      // preceding text first, then publish the control event immediately so it
      // cannot sit behind a timer or a token-sized IPC backlog.
      this.flush();
      this.emit(event);
      return;
    }

    const previous = this.pendingStreamEvents.at(-1);
    if (previous && streamDeltaIdentity(previous) === identity) {
      this.pendingStreamEvents[this.pendingStreamEvents.length - 1] =
        mergeStreamDeltas(previous, event);
    } else {
      this.pendingStreamEvents.push(event);
    }
    if (!this.pendingFlush) {
      this.pendingFlush = setTimeout(() => this.flush(), this.intervalMs);
    }
  }

  async resolveSnapshot<T>(request: Promise<T>): Promise<T> {
    const snapshot = await request;
    // The app-server snapshot already includes every delta processed before
    // this response, while Electron may still be holding those same deltas in
    // the renderer batch. Publish them first so a renderer never seeds the
    // snapshot and then appends its already-included prefix a second time.
    this.flush();
    return snapshot;
  }

  flush(): void {
    if (this.pendingFlush) {
      clearTimeout(this.pendingFlush);
      this.pendingFlush = undefined;
    }
    const events = this.pendingStreamEvents;
    this.pendingStreamEvents = [];
    for (const event of events) {
      this.emit(event);
    }
  }
}

function rendererIgnoresStreamEvent(event: ServerEvent): boolean {
  if (event.kind !== "notification" || event.message.method !== "turn/event") {
    return false;
  }
  const params = eventParams(event.message.params);
  const payload = eventParams(params?.event);
  return (
    typeof payload?.type === "string" &&
    RENDERER_IGNORED_STREAM_EVENT_TYPES.has(payload.type)
  );
}

function streamDeltaIdentity(event: ServerEvent): string | undefined {
  const params =
    event.kind === "notification"
      ? eventParams(event.message.params)
      : undefined;
  if (
    event.kind !== "notification" ||
    !STREAM_DELTA_NOTIFICATION_METHODS.has(event.message.method) ||
    !params ||
    typeof params.delta !== "string" ||
    params.delta.length === 0
  ) {
    return undefined;
  }
  return [
    event.workdir,
    event.message.method,
    params.thread_id,
    params.turn_id,
    params.item_id,
  ]
    .map((value) => (typeof value === "string" ? value : ""))
    .join("\u0000");
}

function mergeStreamDeltas(previous: ServerEvent, next: ServerEvent): ServerEvent {
  if (previous.kind !== "notification" || next.kind !== "notification") {
    return next;
  }
  const previousParams = eventParams(previous.message.params) ?? {};
  const nextParams = eventParams(next.message.params) ?? {};
  return {
    ...next,
    message: {
      ...next.message,
      params: {
        ...nextParams,
        delta: `${String(previousParams.delta ?? "")}${String(nextParams.delta ?? "")}`,
      },
    },
  };
}

function eventParams(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}
