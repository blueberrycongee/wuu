import { describe, expect, it } from "vitest";

import { createDesktopCompositionRoot } from "./composition";
import { subscribeHostData, type HostDataEvent, type HostDataSource } from "./hostData";

class FakeSource implements HostDataSource {
  readonly listeners = new Set<(event: HostDataEvent) => void>();

  subscribe(listener: (event: HostDataEvent) => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  emit(event: HostDataEvent): void {
    for (const listener of [...this.listeners]) listener(event);
  }
}

describe("host.data.subscribe", () => {
  it("filters metadata and unsubscribes when the scope closes", async () => {
    const root = createDesktopCompositionRoot();
    const scope = root.isolate("pane");
    const source = new FakeSource();
    const seen: HostDataEvent[] = [];

    subscribeHostData(
      scope,
      source,
      { thread_id: "t1", types: ["tool_call"], limit: 2 },
      (event) => seen.push(event),
    );

    source.emit({ type: "tool_call", thread_id: "t1", data: { a: 1 } });
    source.emit({ type: "turn", thread_id: "t1" });
    source.emit({ type: "tool_call", thread_id: "t2" });
    source.emit({ type: "tool_call", thread_id: "t1", data: { b: 2 } });
    source.emit({ type: "tool_call", thread_id: "t1", data: { c: 3 } });

    expect(seen).toHaveLength(2);
    expect(source.listeners.size).toBe(1);

    await scope.fiber.dispose();

    expect(source.listeners.size).toBe(0);
    source.emit({ type: "tool_call", thread_id: "t1", data: { d: 4 } });
    expect(seen).toHaveLength(2);
  });

  it("rejects invalid subscription parameters", () => {
    const root = createDesktopCompositionRoot();
    const source = new FakeSource();
    expect(() => subscribeHostData(root, source, { thread_id: "" }, () => {})).toThrow(/thread_id/);
    expect(() => subscribeHostData(root, source, { thread_id: "t1", limit: -1 }, () => {})).toThrow(/limit/);
  });
});
