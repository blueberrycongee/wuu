import { AsyncLocalStorage } from "node:async_hooks";
import { createHash } from "node:crypto";
import { requireBackgroundService, startBackground, requireKernelService, type RuntimeHost, type RuntimePlugin } from "@wuu/plugin-sdk";
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

/** The binding's durable event is the producer-owned outbox. Consumers pull,
 * deduplicate event_id, then acknowledge; the scheduler never calls them back. */
export class UserRoutineScheduler {
  private prefix: string;
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
  validate(raw: unknown) {
    const input = object(raw);
    for (const key of ["cron", "run_at", "timezone", "missed_policy"] as const) if (input[key] !== undefined && typeof input[key] !== "string") throw new Error(`${key} must be a string`);
    const cron = input.cron as string | undefined, run_at = input.run_at as string | undefined;
    const timezone = input.timezone as string || "UTC"; validateTimezone(timezone);
    if (Boolean(cron) === Boolean(run_at)) throw new Error("provide exactly one cron or run_at schedule");
    const expression = cron ? parseCronExpression(cron) : undefined;
    if (run_at && !Number.isFinite(Date.parse(run_at))) throw new Error("invalid run_at timestamp");
    if (input.missed_policy && !["run_once", "skip"].includes(input.missed_policy as string)) throw new Error("invalid missed policy");
    return { cron, run_at, timezone, missed_policy: (input.missed_policy || "run_once") as RoutineBinding["missed_policy"],
      next_run_at: expression ? expression.nextRun(this.clock(), timezone).toISOString() : new Date(run_at!).toISOString() };
  }
  async upsert(raw: unknown, owner = "fixture"): Promise<RoutineBinding> {
    const input = mutation(raw);
    const { next_run_at, ...schedule } = this.validate(input);
    const configuration = { ...schedule, payload: input.payload ?? {} };
    const hash = digest(JSON.stringify(configuration));
    return (await this.update(input.binding_id, current => {
      if (current && current.owner !== owner) throw new Error("routine binding belongs to another consumer");
      if (current?.request_id === input.request_id) { if (current.request_hash !== hash) throw new Error("request_id_conflict"); return current; }
      if ((current?.revision ?? 0) !== input.expected_revision) throw new Error(`revision_conflict: current revision ${current?.revision ?? 0}`);
      return { binding_id: input.binding_id, owner, revision: (current?.revision ?? 0) + 1, request_id: input.request_id, request_hash: hash, ...configuration,
        status: "active", next_run_at };
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
  async pending(owner: string): Promise<RoutineBinding[]> {
    const timestamp = this.clock().getTime();
    return (await this.list(owner)).filter(binding => binding.status === "active" && binding.pending_event &&
      (!binding.pending_event.retry_at || Date.parse(binding.pending_event.retry_at) <= timestamp) &&
      (!binding.pending_event.lease_until || Date.parse(binding.pending_event.lease_until) <= timestamp));
  }
  private dispatchInput(raw: unknown) {
    const input = object(raw);
    for (const key of ["binding_id", "event_id", "claim_id"] as const) if (typeof input[key] !== "string" || !input[key]) throw new Error(`${key} is required`);
    if (!Number.isSafeInteger(input.binding_revision) || Number(input.binding_revision) < 1) throw new Error("binding_revision is required");
    return input as {binding_id:string;event_id:string;claim_id:string;binding_revision:number;accepted?:boolean;defer_until?:string};
  }
  async claim(raw: unknown, owner: string): Promise<{claimed:boolean;binding?:RoutineBinding}> {
    const input = this.dispatchInput(raw), timestamp = this.clock();
    const binding = await this.update(input.binding_id, current => {
      if (!current || current.owner !== owner) throw new Error("routine binding belongs to another consumer");
      const event = current.pending_event;
      if (current.status !== "active" || current.revision !== input.binding_revision || event?.event_id !== input.event_id) throw new Error("stale routine event revision");
      if (event.retry_at && Date.parse(event.retry_at) > timestamp.getTime()) return current;
      if (event.lease_owner !== input.claim_id && event.lease_until && Date.parse(event.lease_until) > timestamp.getTime()) return current;
      return {...current,pending_event:{...event,lease_owner:input.claim_id,lease_until:new Date(timestamp.getTime()+30_000).toISOString()}};
    });
    return binding?.pending_event?.lease_owner === input.claim_id ? {claimed:true,binding} : {claimed:false};
  }
  async acknowledge(raw: unknown, owner: string): Promise<RoutineBinding> {
    const input = this.dispatchInput(raw);
    if (input.accepted === true && input.defer_until !== undefined || input.accepted !== true && !input.defer_until) throw new Error("exactly one acknowledgment outcome is required");
    if (input.defer_until && (!Number.isFinite(Date.parse(input.defer_until)) || Date.parse(input.defer_until) <= this.clock().getTime())) throw new Error("invalid trigger deferral");
    return (await this.update(input.binding_id, current => {
      if (!current || current.owner !== owner) throw new Error("routine binding belongs to another consumer");
      if (current.last_event_id === input.event_id && input.accepted === true && current.revision === input.binding_revision) return current;
      const event = current.pending_event;
      if (current.status !== "active" || current.revision !== input.binding_revision || event?.event_id !== input.event_id) throw new Error("stale routine event revision");
      if (event.lease_owner !== input.claim_id) throw new Error("routine event claim changed");
      if (input.defer_until) return {...current,pending_event:{...event,lease_owner:undefined,lease_until:undefined,retry_at:input.defer_until}};
      return {...current,pending_event:undefined,last_event_id:event.event_id,last_error:undefined,status:current.cron ? "active" : "completed",next_run_at:current.cron ? parseCronExpression(current.cron).nextRun(this.clock(),current.timezone).toISOString() : undefined};
    }))!;
  }
  async tick(): Promise<void> {
    if (this.stopped) return;
    for (const snapshot of await this.list()) {
      if (this.stopped) return;
      const timestamp = this.clock();
      if (snapshot.status !== "active" || snapshot.pending_event || !snapshot.next_run_at || Date.parse(snapshot.next_run_at) > timestamp.getTime()) continue;
      await this.update(snapshot.binding_id, current => {
        if (!current || current.status !== "active" || current.revision !== snapshot.revision || current.pending_event || !current.next_run_at || Date.parse(current.next_run_at) > timestamp.getTime()) return current;
        if (current.missed_policy === "skip" && timestamp.getTime() - Date.parse(current.next_run_at) > 60_000) return {...current,status:current.cron ? "active" : "completed",next_run_at:current.cron ? parseCronExpression(current.cron).nextRun(timestamp,current.timezone).toISOString() : undefined};
        return {...current,pending_event:{event_id:`routine-event-${digest(`${current.binding_id}\0${current.revision}\0${current.next_run_at}`)}`,binding_revision:current.revision,scheduled_at:current.next_run_at,status:"pending"}};
      });
    }
  }

}

export function createUserAutomationPlugin(): RuntimePlugin {
  let scheduler: UserRoutineScheduler, pluginID: string;
  let timer: ReturnType<typeof setInterval> | undefined;
  let running = Promise.resolve();
  let lifetime: AbortSignal;
  const scope = new AsyncLocalStorage<RuntimeHost>();
  const admittedHost = () => {
    const host = scope.getStore();
    if (!host) throw new Error("routine call requires an admitted execution");
    return host;
  };
  const scoped: RuntimeHost = {
    supports: method => admittedHost().supports(method),
    call: (method, params, options) => admittedHost().call(method, params, options),
  };
  return {
    concurrentServices: ["automation.routines"],
    initialize(params) {
      if (!params.user_space_id) throw new Error("user routines require a trusted user space");
      pluginID = params.plugin_id;
      scheduler = new UserRoutineScheduler(scoped, params.user_space_id);
      return { protocol_version: 3, tools: [], capabilities: [],
        required_services: [requireBackgroundService(), ...(["host.storage.get", "host.storage.keys", "host.storage.compare_exchange"] as const).map(requireKernelService)],
        provided_services: [{ name: "automation.routines", version: "1.2.0", methods: ["validate", "upsert", "read", "list", "pause", "stop", "run", "pending", "claim", "acknowledge"].map(name => ({ name, input_schema: `automation.routines.${name}.input.v1`, output_schema: `automation.routines.${name}.output.v1` })) }] };
    },
    async activate(host, lifecycle) {
      lifetime = lifecycle.signal;
      if (lifetime.aborted) { scheduler.stop(); return; }
      lifetime.addEventListener("abort", () => { clearInterval(timer); scheduler.stop(); }, { once: true });
      await startBackground(host, { key: "routines", service: "automation.routines", method: "run" });
    },
    async shutdown() { clearInterval(timer); scheduler.stop(); await running; },
    async invokeService(call, host, execution) {
      return scope.run(host, async () => {
        if (call.service !== "automation.routines") throw new Error("unknown routine service");
        if (call.method === "run") {
          if (call.caller !== pluginID) throw new Error("routine scheduler requires its owner");
          if (!execution) throw new Error("routine scheduler execution is unavailable");
          const signal = AbortSignal.any([lifetime, execution.signal]);
          if (signal.aborted) return {};
          const stop = new Promise<void>(resolve => signal.addEventListener("abort", () => { clearInterval(timer); resolve(); }, { once: true }));
          const tick = () => {
            running = running.then(() => signal.aborted ? undefined : scheduler.tick()).catch(error => {
              if (!signal.aborted) console.error("user routine recovery:", error);
            });
          };
          tick(); timer = setInterval(tick, 15_000);
          await stop; await running;
          return {};
        }
        const input = object(call.params ?? {});
        switch (call.method) {
          case "validate": return { schedule: scheduler.validate(input) };
          case "upsert": return { binding: await scheduler.upsert(input, call.caller) };
          case "pause": case "stop": return { binding: await scheduler.pause(input, call.caller, call.method === "stop") };
          case "list": return { bindings: await scheduler.list(call.caller) };
          case "pending": return { bindings: await scheduler.pending(call.caller) };
          case "claim": return scheduler.claim(input, call.caller);
          case "acknowledge": return { binding: await scheduler.acknowledge(input, call.caller) };
          case "read": { const binding = await scheduler.read(String(input.binding_id ?? "")); if (binding.owner !== call.caller) throw new Error("routine belongs to another consumer"); return { binding }; }
          default: throw new Error("unknown routine method");
        }
      });
    },
  };
}
