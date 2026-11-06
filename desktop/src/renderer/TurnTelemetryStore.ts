import type { ServerEvent } from "../shared/protocol";

export type LiveTurnContextUsage = {
  turnID: string;
  used: number;
  window: number;
  inputTokens: number;
  cacheCreationTokens: number;
  cacheReadTokens: number;
};

export type TurnTelemetrySnapshot = {
  contextUsage?: LiveTurnContextUsage;
};

export const TURN_TELEMETRY_NOTIFY_INTERVAL_MS = 250;
const RETAINED_TURN_TELEMETRY_LIMIT = 24;
const EMPTY_SNAPSHOT: TurnTelemetrySnapshot = Object.freeze({});

export class TurnTelemetryStore {
  private readonly entries = new Map<string, TurnTelemetrySnapshot>();
  private readonly listeners = new Set<() => void>();
  private notifyTimer: ReturnType<typeof setTimeout> | undefined;

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  readonly getSnapshot = (turnID: string | undefined): TurnTelemetrySnapshot => {
    if (!turnID) return EMPTY_SNAPSHOT;
    return this.entries.get(turnID) ?? EMPTY_SNAPSHOT;
  };

  ingest(event: ServerEvent): void {
    if (event.kind !== "notification" || event.message.method !== "turn/usage") {
      return;
    }
    const params = isRecord(event.message.params) ? event.message.params : undefined;
    const turnID = stringValue(params, "turn_id");
    if (!turnID) return;

    const contextTokens = numberValue(params, "context_tokens") ?? 0;
    const contextWindowTokens = numberValue(params, "context_window_tokens") ?? 0;
    if (contextTokens <= 0 || contextWindowTokens <= 0) {
      return;
    }
    this.entries.set(turnID, {
      contextUsage: {
        turnID,
        used: contextTokens,
        window: contextWindowTokens,
        inputTokens: Math.max(0, numberValue(params, "input_tokens") ?? 0),
        cacheCreationTokens: Math.max(
          0,
          numberValue(params, "cache_creation_tokens") ?? 0,
        ),
        cacheReadTokens: Math.max(
          0,
          numberValue(params, "cache_read_tokens") ?? 0,
        ),
      },
    });
    while (this.entries.size > RETAINED_TURN_TELEMETRY_LIMIT) {
      const oldest = this.entries.keys().next().value as string | undefined;
      if (!oldest) break;
      this.entries.delete(oldest);
    }
    this.scheduleNotify();
  }

  reset(): void {
    this.entries.clear();
    if (this.notifyTimer !== undefined) {
      clearTimeout(this.notifyTimer);
      this.notifyTimer = undefined;
    }
  }

  private scheduleNotify(): void {
    if (this.notifyTimer !== undefined) return;
    this.notifyTimer = setTimeout(() => {
      this.notifyTimer = undefined;
      for (const listener of this.listeners) listener();
    }, TURN_TELEMETRY_NOTIFY_INTERVAL_MS);
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringValue(
  record: Record<string, unknown> | undefined,
  key: string,
): string | undefined {
  const value = record?.[key];
  return typeof value === "string" ? value : undefined;
}

function numberValue(
  record: Record<string, unknown> | undefined,
  key: string,
): number | undefined {
  const value = record?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

export const turnTelemetryStore = new TurnTelemetryStore();
