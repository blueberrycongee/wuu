import { createHash, randomUUID } from "node:crypto";
import { requireKernelService, type RuntimeHost, type RuntimePlugin } from "@wuu/plugin-sdk";
import { parseCronExpression, validateTimezone } from "./schedule.js";

interface PendingEvent { event_id: string; scheduled_at: string; binding_revision: number; status: "pending"; lease_owner?: string; lease_until?: string; retry_at?: string }
export interface RoutineBinding {
  binding_id: string; owner: string; revision: number; request_id: string; request_hash: string;
  cron?: string; run_at?: string; timezone: string; missed_policy: "run_once" | "skip";
  payload: Record<string, unknown>; status: "active" | "paused" | "stopped" | "completed";
  next_run_at?: string; pending_event?: PendingEvent; last_event_id?: string; last_error?: string;
}
interface Mutation { binding_id: string; request_id: string; expected_revision: number; cron?: string; run_at?: string; timezone?: string; missed_policy?: string; payload?: Record<string, unknown> }
const digest = (value: string) => createHash("sha256").update(value).digest("hex").slice(0, 32);
const object = (input: unknown): Record<string, unknown> => {
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new Error("expected routine parameters");
  return input as Record<string, unknown>;
};
function mutation(input: unknown): Mutation {
  const value = object(input);
  if (typeof value.binding_id !== "string" || !value.binding_id || typeof value.request_id !== "string" || !value.request_id || !Number.isSafeInteger(value.expected_revision) || Number(value.expected_revision) < 0) throw new Error("binding_id, request_id, and expected_revision are required");
  for (const key of ["cron", "run_at", "timezone", "missed_policy"] as const) if (value[key] !== undefined && typeof value[key] !== "string") throw new Error(`${key} must be a string`);
  return { ...value, ...(value.payload ? { payload: object(value.payload) } : {}) } as unknown as Mutation;
}

/** The binding's durable event is the outbox; callbacks must deduplicate event_id. */
export class UserRoutineScheduler {
  private prefix: string;
  private instance = randomUUID();
  private stopped = false;
  constructor(private host: RuntimeHost, space: string, private clock = () => new Date()) { this.prefix = `routines.v1.${digest(space)}.`; }
  stop(): void { this.stopped = true; }
  private key(id: string) { return `${this.prefix}${digest(id)}`; }
  private async raw(id: string) { return this.host.call("host.storage.get", { scope: "user", key: this.key(id) }); }
  async read(id: string): Promise<RoutineBinding> {
    const { value } = await this.raw(id); if (!value) throw new Error("routine binding not found"); return JSON.parse(value) as RoutineBinding;
  }
  async list(owner?: string): Promise<RoutineBinding[]> {
    const { keys } = await this.host.call("host.storage.keys", { scope: "user" }); const records: RoutineBinding[] = [];
    for (const key of keys.filter(k => k.startsWith(this.prefix))) {
      const { value } = await this.host.call("host.storage.get", { scope: "user", key });
      if (value) { const record = JSON.parse(value) as RoutineBinding; if (!owner || owner === record.owner) records.push(record); }
    }
    return records;
  }
  private async update(id: string, change: (current?: RoutineBinding) => RoutineBinding | undefined): Promise<RoutineBinding | undefined> {
    for (let attempt = 0; attempt < 24; attempt++) {
      const { value } = await this.raw(id); const current = value ? JSON.parse(value) as RoutineBinding : undefined; const next = change(current);
      if (!next || next === current) return next;
      const saved = await this.host.call("host.storage.compare_exchange", { scope: "user", key: this.key(id), expected: value, value: JSON.stringify(next) });
      if (saved.swapped) return next;
    }
    throw new Error("revision_conflict: routine binding is busy");
  }
  async upsert(raw: unknown, owner = "fixture"): Promise<RoutineBinding> {
    const input = mutation(raw);
    const timezone = input.timezone || "UTC"; validateTimezone(timezone);
    if (Boolean(input.cron) === Boolean(input.run_at)) throw new Error("provide exactly one cron or run_at schedule");
    const cron = input.cron ? parseCronExpression(input.cron) : undefined;
    if (input.run_at && !Number.isFinite(Date.parse(input.run_at))) throw new Error("invalid run_at timestamp");
    if (input.missed_policy && !["run_once", "skip"].includes(input.missed_policy)) throw new Error("invalid missed policy");
    const configuration = { cron: input.cron, run_at: input.run_at, timezone, missed_policy: (input.missed_policy || "run_once") as RoutineBinding["missed_policy"], payload: input.payload ?? {} };
    const hash = digest(JSON.stringify(configuration));
    return (await this.update(input.binding_id, current => {
      if (current && current.owner !== owner) throw new Error("routine binding belongs to another consumer");
      if (current?.request_id === input.request_id) { if (current.request_hash !== hash) throw new Error("request_id_conflict"); return current; }
      if ((current?.revision ?? 0) !== input.expected_revision) throw new Error(`revision_conflict: current revision ${current?.revision ?? 0}`);
      return { binding_id: input.binding_id, owner, revision: (current?.revision ?? 0) + 1, request_id: input.request_id, request_hash: hash, ...configuration,
        status: "active", next_run_at: cron ? cron.nextRun(this.clock(), timezone).toISOString() : new Date(input.run_at!).toISOString() };
    }))!;
  }
  async pause(raw: unknown, owner = "fixture", stop = false): Promise<RoutineBinding> {
    const input = mutation(raw), hash = digest(stop ? "stop" : "pause");
    return (await this.update(input.binding_id, current => {
      if (!current) throw new Error("routine binding not found");
      if (current.owner !== owner) throw new Error("routine binding belongs to another consumer");
      if (current.request_id === input.request_id) { if (current.request_hash !== hash) throw new Error("request_id_conflict"); return current; }
      if (current.revision !== input.expected_revision) throw new Error(`revision_conflict: current revision ${current.revision}`);
      return { ...current, revision: current.revision + 1, request_id: input.request_id, request_hash: hash, status: stop ? "stopped" : "paused", pending_event: undefined, next_run_at: undefined };
    }))!;
  }
  async tick(): Promise<void> {
    if (this.stopped) return;
    for (const snapshot of await this.list()) {
      if (this.stopped) return;
      const timestamp = this.clock();
      if (snapshot.status !== "active" || (!snapshot.pending_event && (!snapshot.next_run_at || Date.parse(snapshot.next_run_at) > timestamp.getTime()))) continue;
      const claimed = await this.update(snapshot.binding_id, current => {
        if (!current || current.status !== "active" || current.revision !== snapshot.revision) return undefined;
        let pending = current.pending_event;
        if (pending?.retry_at && Date.parse(pending.retry_at) > timestamp.getTime()) return undefined;
        if (pending?.lease_until && Date.parse(pending.lease_until) > timestamp.getTime()) return undefined;
        if (!pending) {
          if (!current.next_run_at || Date.parse(current.next_run_at) > timestamp.getTime()) return undefined;
          if (current.missed_policy === "skip" && timestamp.getTime() - Date.parse(current.next_run_at) > 60_000) return { ...current, status: current.cron ? "active" : "completed", next_run_at: current.cron ? parseCronExpression(current.cron).nextRun(timestamp, current.timezone).toISOString() : undefined };
          pending = { event_id: `routine-event-${digest(`${current.binding_id}\0${current.revision}\0${current.next_run_at}`)}`, binding_revision: current.revision, scheduled_at: current.next_run_at, status: "pending" };
        }
        return { ...current, pending_event: { ...pending, lease_owner: this.instance, lease_until: new Date(timestamp.getTime() + 30_000).toISOString() } };
      });
      if (!claimed?.pending_event || claimed.pending_event.lease_owner !== this.instance) continue;
      const event = claimed.pending_event;
      try {
        if (this.stopped) return;
        const response = await this.host.call("host.service.call", { service: "automation.trigger", method: "fire", params: { binding_id: claimed.binding_id, event_id: event.event_id, binding_revision: event.binding_revision, scheduled_at: event.scheduled_at, payload: claimed.payload } }) as { accepted?: boolean; defer_until?: string };
        if (this.stopped) return;
        await this.update(claimed.binding_id, current => {
          if (!current || current.revision !== claimed.revision || current.pending_event?.event_id !== event.event_id || current.pending_event.lease_owner !== this.instance) return current;
          if (response.defer_until) {
            if (!Number.isFinite(Date.parse(response.defer_until)) || Date.parse(response.defer_until) <= this.clock().getTime()) throw new Error("invalid trigger deferral");
            return { ...current, pending_event: { ...event, lease_owner: undefined, lease_until: undefined, retry_at: response.defer_until } };
          }
          if (!response.accepted) throw new Error("trigger did not acknowledge the event");
          return { ...current, pending_event: undefined, last_event_id: event.event_id, last_error: undefined, status: current.cron ? "active" : "completed", next_run_at: current.cron ? parseCronExpression(current.cron).nextRun(this.clock(), current.timezone).toISOString() : undefined };
        });
      } catch (error) {
        await this.update(claimed.binding_id, current => current?.pending_event?.event_id === event.event_id && current.pending_event.lease_owner === this.instance ? { ...current, pending_event: { ...current.pending_event, lease_owner: undefined, lease_until: undefined }, last_error: String(error) } : current);
      }
    }
  }
}

export function createUserAutomationPlugin(): RuntimePlugin {
  let scheduler: UserRoutineScheduler;
  let timer: ReturnType<typeof setInterval> | undefined;
  let running = Promise.resolve();
  const tick = () => { running = running.then(() => scheduler.tick()).catch(error => { console.error("user routine recovery:", error); }); };
  return {
    concurrentServices: ["automation.routines"],
    initialize(params, host) {
      if (!params.user_space_id) throw new Error("user routines require a trusted user space");
      scheduler = new UserRoutineScheduler(host, params.user_space_id);
      return { protocol_version: 3, tools: [], capabilities: [],
        required_services: [...(["host.storage.get", "host.storage.keys", "host.storage.compare_exchange"] as const).map(requireKernelService), { name: "automation.trigger", major_version: 1, required: false }],
        provided_services: [{ name: "automation.routines", version: "1.0.0", methods: ["upsert", "read", "list", "pause", "stop"].map(name => ({ name, input_schema: `automation.routines.${name}.input.v1`, output_schema: `automation.routines.${name}.output.v1` })) }] };
    },
    activate(_host, lifecycle) { lifecycle.signal.addEventListener("abort", () => { clearInterval(timer); scheduler.stop(); }, { once: true }); tick(); timer = setInterval(tick, 15_000); },
    async shutdown() { clearInterval(timer); scheduler.stop(); await running; },
    async invokeService(call) {
      if (call.service !== "automation.routines") throw new Error("unknown routine service");
      const input = object(call.params ?? {});
      switch (call.method) {
        case "upsert": return { binding: await scheduler.upsert(input, call.caller) };
        case "pause": case "stop": return { binding: await scheduler.pause(input, call.caller, call.method === "stop") };
        case "list": return { bindings: await scheduler.list(call.caller) };
        case "read": { const binding = await scheduler.read(String(input.binding_id ?? "")); if (binding.owner !== call.caller) throw new Error("routine belongs to another consumer"); return { binding }; }
        default: throw new Error("unknown routine method");
      }
    },
  };
}
