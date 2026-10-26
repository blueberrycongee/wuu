import assert from "node:assert/strict";
import test from "node:test";
import { createPluginSettings, type PluginSettingsSnapshot } from "./settings.js";
import type { RuntimeHost } from "./index.js";

// Public reader failures: mixed/reordered concurrent results; previous operation
// mutation; rejected refresh replacing stable state; notifications after dispose.
test("settings reader publishes frozen revisions, keeps captured operations and stops on retirement", async () => {
  const lifetime = new AbortController();
  let response = { entries: { limit: 1 }, revision: "one", restart_required: [], scope_revisions: { user: "u1" } };
  let fail = false;
  let calls = 0;
  let unblock: (() => void) | undefined;
  const host = { supports: () => true, call: async (method: string, params: unknown, options: { signal?: AbortSignal }) => {
    assert.equal(method, "host.settings.list");
    assert.deepEqual(params, { scope: "user" });
    assert.equal(options.signal, lifetime.signal);
    calls++;
    if (unblock) await new Promise<void>(resolve => { unblock = resolve; });
    if (fail) throw new Error("invalid settings candidate");
    return structuredClone(response);
  } } as RuntimeHost;
  const settings = createPluginSettings(host, { signal: lifetime.signal, scope: "user", validate(values) {
    if (values.limit === 3) throw new Error("cross-field candidate rejected");
  } });
  const changes: PluginSettingsSnapshot[] = [];
  const dispose = settings.subscribe(value => changes.push(value));
  const first = await settings.capture();
  assert.equal(first.values.limit, 1);
  assert(Object.isFrozen(first) && Object.isFrozen(first.values));
  await settings.refresh();
  assert.equal(changes.length, 1);
  response = { entries: { limit: 2 }, revision: "two", restart_required: [], scope_revisions: { user: "u2" } };
  await settings.refresh();
  assert.equal(first.values.limit, 1);
  assert.equal(settings.current?.values.limit, 2);
  assert.equal(changes.length, 2);
  fail = true;
  await assert.rejects(settings.refresh(), /invalid settings/);
  assert.equal(settings.current?.revision, "two");
  assert.equal(changes.length, 2);
  fail = false;
  response = { entries: { limit: 3 }, revision: "three", restart_required: [], scope_revisions: { user: "u3" } };
  await assert.rejects(settings.refresh(), /cross-field candidate/);
  assert.equal(settings.current?.revision, "two");
  assert.equal(changes.length, 2);
  response = { entries: { limit: 2 }, revision: "two", restart_required: [], scope_revisions: { user: "u2" } };
  unblock = () => {};
  const one = settings.refresh(), two = settings.capture();
  const started = calls;
  await Promise.resolve();
  assert.equal(calls, started);
  lifetime.abort(new Error("retired"));
  unblock!();
  await assert.rejects(one, /retired/);
  await assert.rejects(two, /retired/);
  assert.equal(changes.length, 2);
  dispose();
  await assert.rejects(settings.get("limit"), /retired/);
});

test("settings reader explicitly rejects pre-snapshot hosts without changing legacy get/list", async () => {
  const host = { supports: () => true, call: async () => ({ entries: { value: 1 } }) } as unknown as RuntimeHost;
  const settings = createPluginSettings(host, { signal: new AbortController().signal });
  await assert.rejects(settings.capture(), /snapshot/);
  assert.equal(settings.current, null);
});


test("settings watch polls only within its lifecycle and never publishes a late change", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const lifetime = new AbortController();
  let calls = 0;
  const host = { supports: () => true, call: async () => {
    calls++;
    return { entries: { limit: calls }, revision: String(calls), scope_revisions: { user: String(calls) }, restart_required: [] };
  } } as unknown as RuntimeHost;
  const reader = createPluginSettings(host, { signal: lifetime.signal });
  const events: number[] = [];
  reader.subscribe(value => events.push(value.values.limit as number));
  const watching = reader.watch(100);
  await reader.capture();
  assert.equal(calls, 1);
  t.mock.timers.tick(100);
  await reader.capture();
  assert.equal(calls, 2);
  lifetime.abort(new Error("generation retired"));
  await assert.rejects(watching, /generation retired/);
  t.mock.timers.tick(1000);
  assert.deepEqual(events, [1, 2]);
  assert.equal(calls, 2);
});
