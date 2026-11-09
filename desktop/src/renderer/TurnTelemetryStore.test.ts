import { afterEach, describe, expect, it, vi } from "vitest";
import type { ServerEvent } from "../shared/protocol";
import {
  TURN_TELEMETRY_NOTIFY_INTERVAL_MS,
  TurnTelemetryStore,
} from "./TurnTelemetryStore";

function notification(
  method: string,
  params: Record<string, unknown>,
): ServerEvent {
  return {
    kind: "notification",
    workdir: "/repo",
    message: { method, params },
  };
}

const turn = { thread_id: "thread-1", turn_id: "turn-1" };

afterEach(() => {
  vi.useRealTimers();
});

describe("TurnTelemetryStore", () => {
  it("captures context usage from provider reports", () => {
    const store = new TurnTelemetryStore();
    store.ingest(
      notification("turn/usage", {
        ...turn,
        input_tokens: 500,
        output_tokens: 120,
        context_tokens: 620,
        context_window_tokens: 1_000,
        cache_creation_tokens: 40,
        cache_read_tokens: 80,
      }),
    );

    expect(store.getSnapshot("turn-1").contextUsage).toEqual({
      turnID: "turn-1",
      used: 620,
      window: 1_000,
      inputTokens: 500,
      cacheCreationTokens: 40,
      cacheReadTokens: 80,
    });
    store.reset();
  });

  it("ignores usage reports without a context window", () => {
    const store = new TurnTelemetryStore();
    store.ingest(
      notification("turn/usage", {
        ...turn,
        input_tokens: 500,
        output_tokens: 120,
      }),
    );

    expect(store.getSnapshot("turn-1").contextUsage).toBeUndefined();
    store.reset();
  });

  it("coalesces subscriber notifications", () => {
    vi.useFakeTimers();
    const store = new TurnTelemetryStore();
    const listener = vi.fn();
    store.subscribe(listener);

    for (let index = 0; index < 3; index += 1) {
      store.ingest(
        notification("turn/usage", {
          ...turn,
          context_tokens: 600 + index,
          context_window_tokens: 1_000,
        }),
      );
    }
    expect(listener).not.toHaveBeenCalled();

    vi.advanceTimersByTime(TURN_TELEMETRY_NOTIFY_INTERVAL_MS);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot("turn-1").contextUsage?.used).toBe(602);
    store.reset();
  });
});
