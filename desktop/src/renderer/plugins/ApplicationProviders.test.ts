import { describe, expect, it } from "vitest";
import type { ApplicationProviderInstanceV1, ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { ApplicationProviders } from "./ApplicationProviders";

function candidate(label: string, events: string[], fail = false): ApplicationProviderInstanceV1 {
  return {
    activate: async checkpoint => { events.push(`${label}:activate:${checkpoint?.schema ?? "empty"}`); if (fail) throw new Error("incompatible state"); },
    deactivate: async () => { events.push(`${label}:stop`); return { schema: "drafts/v1", value: { text: "unsent" } }; },
    createScope: signal => ({ signal, label }) as unknown as ApplicationServiceV1,
    dispose: async () => { events.push(`${label}:dispose`); },
  };
}

describe("selected application business provider lifecycle", () => {
  it("annotates only an active source binding and isolates superseded retirement cleanup", async () => {
    const providers = new ApplicationProviders();
    const firstSignal = new AbortController();
    const empty = providers.beginRetirement(firstSignal.signal);
    expect(providers.getRetirement()).toBeUndefined();
    empty();
    const first = providers.register({ pluginId: "external", generation: "one", id: "ui.application" }, { id: "ui.application", prepare: () => candidate("one", []) });
    await providers.select(first);
    const sourceBinding = providers.getBindingRevision();
    const finishFirst = providers.beginRetirement(firstSignal.signal);
    expect(providers.getRetirement()).toEqual({ binding: sourceBinding });
    expect(providers.current).toBe(first);
    expect(providers.getBindingRevision()).toBe(sourceBinding);
    const newerSignal = new AbortController();
    let observedOnAbort: ReturnType<ApplicationProviders["getRetirement"]>;
    newerSignal.signal.addEventListener("abort", () => { observedOnAbort = providers.getRetirement(); });
    const finishNewer = providers.beginRetirement(newerSignal.signal);
    const newer = providers.getRetirement();
    finishFirst(); firstSignal.abort();
    expect(providers.getRetirement()).toBe(newer);
    newerSignal.abort();
    expect(observedOnAbort).toBeUndefined();
    expect(providers.getRetirement()).toBeUndefined();
    finishNewer();
    providers.beginRetirement(newerSignal.signal);
    expect(providers.getRetirement()).toBeUndefined();
    await providers.remove(first);
  });

  it("starts only the selected provider, stops before transfer, and retires captured scopes", async () => {
    const events: string[] = []; const providers = new ApplicationProviders();
    const first = providers.register({ pluginId: "external", generation: "one", id: "ui.application" }, { id: "ui.application", prepare: () => candidate("one", events) });
    const second = providers.register({ pluginId: "external", generation: "two", id: "ui.application" }, { id: "ui.application", prepare: () => candidate("two", events) });
    expect(events).toEqual([]);
    await providers.select(first);
    const scope = providers.createScope(new AbortController().signal) as unknown as { signal: AbortSignal };
    await providers.select(second);
    expect(scope.signal.aborted).toBe(true);
    expect(events).toEqual(["one:activate:empty", "one:stop", "two:activate:drafts/v1"]);
    await providers.select(undefined);
    expect(() => providers.createScope(new AbortController().signal)).toThrow("No application provider");
    expect(events.at(-1)).toBe("two:stop");
  });

  it("preserves the effective provider on failed preparation and restores it after failed activation", async () => {
    const events: string[] = []; const providers = new ApplicationProviders();
    const first = providers.register({ pluginId: "external", generation: "one", id: "ui.application" }, { id: "ui.application", prepare: () => candidate("one", events) });
    await providers.select(first);
    const broken = providers.register({ pluginId: "other", generation: "bad", id: "ui.application" }, { id: "ui.application", prepare: () => { throw new Error("build failed"); } });
    await expect(providers.select(broken)).rejects.toThrow("build failed");
    expect(providers.current).toBe(first);
    expect(events).toEqual(["one:activate:empty"]);
    const incompatible = providers.register({ pluginId: "other", generation: "incompatible", id: "ui.application" }, { id: "ui.application", prepare: () => candidate("bad", events, true) });
    await expect(providers.select(incompatible)).rejects.toThrow("incompatible state");
    expect(providers.current).toBe(first);
    expect(events).toContain("one:activate:drafts/v1");
  });

  it("awaits asynchronous cleanup and preserves a stopped checkpoint across disable and recovery", async () => {
    const events: string[] = []; const providers = new ApplicationProviders();
    const first = providers.register({ pluginId: "external", generation: "one", id: "ui.application" }, { id: "ui.application", prepare: () => candidate("one", events) });
    await providers.select(first); await providers.remove(first);
    expect(events).toEqual(["one:activate:empty", "one:stop", "one:dispose"]);
    const next = providers.register({ pluginId: "external", generation: "next", id: "ui.application" }, { id: "ui.application", prepare: () => candidate("next", events) });
    await providers.select(next);
    expect(events.at(-1)).toBe("next:activate:drafts/v1");
  });
});

describe("application provider interrupted transitions", () => {
  it("never activates a provider removed while preparation is suspended", async () => {
    const events: string[] = []; const providers = new ApplicationProviders();
    let ready!: (value: ApplicationProviderInstanceV1) => void;
    const prepared = new Promise<ApplicationProviderInstanceV1>(resolve => { ready = resolve; });
    const record = providers.register({ pluginId: "candidate", generation: "one", id: "ui.application" }, { id: "ui.application", prepare: () => prepared });
    const switching = providers.select(record);
    await Promise.resolve();
    const removing = providers.remove(record);
    ready(candidate("removed", events));
    await expect(switching).rejects.toThrow("retired"); await removing;
    expect(events).toEqual(["removed:dispose"]);
    expect(providers.current).toBeUndefined();
  });

  it("reports an uncertain stopped writer rather than leaving an aborted provider effective", async () => {
    const providers = new ApplicationProviders();
    const record = providers.register({ pluginId: "external", generation: "one", id: "ui.application" }, { id: "ui.application", prepare: () => ({ ...candidate("one", []), deactivate: async () => { throw new Error("connection cleanup uncertain"); } }) });
    await providers.select(record);
    await expect(providers.select(undefined)).rejects.toThrow("connection cleanup uncertain");
    expect(providers.current).toBeUndefined();
    expect(providers.failure).toContain("uncertain");
  });

  it("retains activation and cleanup errors and does not restart another writer after uncertain cleanup", async () => {
    const providers = new ApplicationProviders(); const events: string[] = [];
    const first = providers.register({ pluginId: "old", generation: "one", id: "ui.application" }, { id: "ui.application", prepare: () => candidate("old", events) });
    await providers.select(first);
    const broken = providers.register({ pluginId: "new", generation: "two", id: "ui.application" }, { id: "ui.application", prepare: () => ({ ...candidate("new", events, true), deactivate: async () => { throw new Error("writer cleanup uncertain"); } }) });
    await expect(providers.select(broken)).rejects.toThrow("incompatible state; writer cleanup uncertain");
    expect(providers.current).toBeUndefined();
    expect(events.filter(value => value.startsWith("old:activate"))).toHaveLength(1);
  });
});

it("does not restart an old business writer against a different committed Go graph and recovers through selection", async () => {
  const providers = new ApplicationProviders(); const events: string[] = [];
  const first = providers.register({ pluginId: "external", generation: "one", id: "ui.application", graphGeneration: "graph-one" }, { id: "ui.application", prepare: () => candidate("one", events) });
  await providers.select(first);
  const broken = providers.register({ pluginId: "external", generation: "two", id: "ui.application", graphGeneration: "graph-two" }, { id: "ui.application", prepare: () => candidate("two", events, true) });
  await expect(providers.select(broken)).rejects.toThrow("after Go graph graph-two committed");
  expect(providers.current).toBeUndefined();
  expect(events.filter(event => event.startsWith("one:activate"))).toHaveLength(1);
  const repaired = providers.register({ pluginId: "external", generation: "three", id: "ui.application", graphGeneration: "graph-three" }, { id: "ui.application", prepare: () => candidate("three", events) });
  await providers.select(repaired);
  expect(providers.current).toBe(repaired);
  expect(events.at(-1)).toBe("three:activate:drafts/v1");
});

it("cancels a suspended preparation without blocking disposal and cleans a late prepared instance", async () => {
  const providers = new ApplicationProviders(); const events: string[] = [];
  let ready!: (value: ApplicationProviderInstanceV1) => void;
  const preparation = new Promise<ApplicationProviderInstanceV1>(resolve => { ready = resolve; });
  const record = providers.register({ pluginId: "cancelled", generation: "one", id: "ui.application" }, { id: "ui.application", prepare: () => preparation });
  const pending = providers.prepare(record);
  await Promise.resolve();
  const removing = providers.remove(record);
  await expect(pending).rejects.toThrow();
  await removing;
  ready(candidate("late", events));
  await preparation; await Promise.resolve(); await Promise.resolve();
  expect(events).toEqual(["late:dispose"]);
  expect(providers.current).toBeUndefined();
});

it("restores the previous local writer when a candidate is retired while the previous writer drains", async () => {
  const providers = new ApplicationProviders(); const events: string[] = [];
  let drained!: () => void; let entered!: () => void;
  const draining = new Promise<void>(resolve => { entered = resolve; });
  const gate = new Promise<void>(resolve => { drained = resolve; });
  const previous = providers.register({ pluginId: "external", generation: "one", id: "ui.application" }, { id: "ui.application", prepare: () => ({ ...candidate("one", events), deactivate: async () => { entered(); await gate; return { schema: "drafts/v1", value: {} }; } }) });
  const next = providers.register({ pluginId: "external", generation: "two", id: "ui.application" }, { id: "ui.application", prepare: () => candidate("two", events) });
  await providers.select(previous);
  const changing = providers.select(next); await draining;
  const removing = providers.remove(next); drained();
  await expect(changing).rejects.toThrow("retired"); await removing;
  expect(providers.current).toBe(previous);
  expect(events).toContain("one:activate:drafts/v1");
  expect(events).not.toContain("two:activate:drafts/v1");
});
