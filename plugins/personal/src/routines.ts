import { createHash } from "node:crypto";
import type { HostServiceContracts, RuntimeHost } from "@wuu/plugin-sdk";
import type { NamedAgent, PersonalTask } from "./index.js";

const hash = (...parts: string[]) => createHash("sha256").update(parts.join("\0")).digest("hex").slice(0, 28);
const now = () => new Date().toISOString();
const required = (input: Record<string, unknown>, key: string) => { if (typeof input[key] !== "string" || !(input[key] as string).trim()) throw new Error(`${key} is required`); return (input[key] as string).trim(); };
interface Binding { binding_id: string; revision: number; status: string; next_run_at?: string; pending_event?: { event_id: string; binding_revision: number; scheduled_at?: string }; payload?: Record<string, unknown> }
interface CalendarSource { kind: "calendar"; account_id: string; generation: string; calendar_id: string; time_min: string; time_max: string; timezone: string }
export interface CalendarChange { event_id: string; calendar_event_id: string; kind: "created" | "updated" | "cancelled" | "removed"; occurred_at: string; before?: unknown; after?: unknown; source: Record<string, unknown> }
interface CalendarPage { available: boolean; reset_required?: boolean; reason?: string; baseline?: boolean; cursor?: string; changes: CalendarChange[]; source?: Record<string, unknown> }
export interface Routine {
  routine_id: string; agent_id: string; request_id: string; revision: number; config_revision: number;
  title: string; prompt: string; kind: "reminder" | "task"; cron?: string; run_at?: string; timezone: string; missed_policy: "run_once" | "skip";
  status: "active" | "stopping" | "paused" | "stopped" | "completed"; stop_target?: "paused" | "stopped"; binding: { binding_id: string; revision: number; status: "pending" | "active" | "unknown"; next_run_at?: string };
  created_at: string; updated_at: string; last_event_id?: string; last_result_hash?: string; last_error?: string; cancellation_pending?: boolean;
  source?: CalendarSource; source_cursor?: string; source_blocked?: boolean; source_pending?: { cursor: string; changes: CalendarChange[] };
}
interface EventRecord { kind?: "reminder" | "task" | "calendar"; state?: string; created_at?: string; updated_at?: string; scheduled_at?: string; started_at?: string; completed_at?: string; output?: string; error?: string; revision: number; event_id: string; routine_id: string; routine_revision?: number; status: "pending" | "accepted"; request_id: string; session_id?: string; previous_delivery?: { request_id: string; session_id: string } }
interface Preferences { quiet_hours: { enabled: boolean; start: string; end: string; timezone: string }; notifications: boolean }
interface Services {
  active(): void;
  mutation(): () => void;
  agent(id: string, allowInactive?: boolean): Promise<NamedAgent>;
  main(): Promise<NamedAgent>;
  send(agentID: string, params: HostServiceContracts["host.session.send"]["params"]): Promise<HostServiceContracts["host.session.send"]["result"]>;
  task(routine: Routine, eventID: string, agent: NamedAgent, change?: CalendarChange): Promise<unknown>;
  stopTasks(routineID: string): Promise<string[]>;
  preferences(): Promise<Preferences>;
}

/** Personal policy consumes the scheduler's binding; it never computes next cron times. */
export class PersonalRoutines {
  private prefix: string;
  private transitions = new Map<string, Promise<unknown>>();
  constructor(private host: RuntimeHost, private space: string, private services: Services) { this.prefix = `personal.routines.v1.${hash(space)}.`; }
  private async transition<T>(routineID: string, action: () => Promise<T>): Promise<T> {
    const prior = this.transitions.get(routineID) ?? Promise.resolve();
    const next = prior.catch(() => undefined).then(action); this.transitions.set(routineID, next);
    try { return await next; } finally { if (this.transitions.get(routineID) === next) this.transitions.delete(routineID); }
  }
  private key(kind: string, id: string) { return `${this.prefix}${kind}.${id}`; }
  private async read<T>(kind: string, id: string): Promise<T | undefined> {
    const { value } = await this.host.call("host.storage.get", { scope: "user", key: this.key(kind, id) }); return value ? JSON.parse(value) as T : undefined;
  }
  private async update<T extends { revision: number }>(kind: string, id: string, change: (current?: T) => T, expected?: number): Promise<T> {
    for (let attempt = 0; attempt < 24; attempt++) {
      const { value } = await this.host.call("host.storage.get", { scope: "user", key: this.key(kind, id) }); const current = value ? JSON.parse(value) as T : undefined;
      if (expected !== undefined && (current?.revision ?? 0) !== expected) throw new Error(`revision_conflict: current revision ${current?.revision ?? 0}`);
      const next = change(current); if (next === current) return next; next.revision = (current?.revision ?? 0) + 1;
      const finishMutation = this.services.mutation();
      try {
        const saved = await this.host.call("host.storage.compare_exchange", { scope: "user", key: this.key(kind, id), expected: value, value: JSON.stringify(next) });
        if (saved.swapped) return next;
      } finally { finishMutation(); }
      if (expected !== undefined) throw new Error("revision_conflict");
    }
    throw new Error("revision_conflict: routine is busy");
  }
  async list(refresh = false): Promise<Routine[]> {
    const { keys } = await this.host.call("host.storage.keys", { scope: "user" }); const values: Routine[] = [];
    for (const key of keys.filter(k => k.startsWith(this.key("routine", "")))) { const { value } = await this.host.call("host.storage.get", { scope: "user", key }); if (value) values.push(JSON.parse(value) as Routine); }
    if (refresh) {
      for (let i = 0; i < values.length; i++) {
        const routine = values[i];
        if (routine.binding.status !== "active") continue;
        try {
          const binding = await this.scheduler("read", { binding_id: routine.binding.binding_id });
          if (binding?.revision !== routine.binding.revision) continue;
          const status = binding.status === "completed" && !routine.cron && routine.status === "active" ? "completed" : routine.status;
          if (binding.next_run_at !== routine.binding.next_run_at || status !== routine.status) values[i] = await this.update<Routine>("routine", routine.routine_id, current => current?.config_revision === routine.config_revision ? { ...current, status, binding: { ...current.binding, next_run_at: binding.next_run_at } } : current!);
        } catch { /* Cached business state remains readable when scheduling is unavailable. */ }
      }
    }
    return values.sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  }
  async history(input: Record<string, unknown>, main: NamedAgent, taskPrefix: string) {
    const routineID = required(input, "routine_id");
    const routine = await this.read<Routine>("routine", routineID);
    if (!routine) throw new Error("routine not found");
    const agent = await this.services.agent(routine.agent_id, true);
    const limit = input.limit === undefined ? 20 : Number(input.limit);
    if (input.limit !== undefined && typeof input.limit !== "number" || !Number.isSafeInteger(limit) || limit < 1 || limit > 50) throw new Error("history limit must be an integer from 1 to 50");
    const eventPrefix = this.key("event", "");
    let after = "";
    if (input.cursor !== undefined) {
      try {
        if (typeof input.cursor !== "string" || input.cursor.length > 2048) throw new Error();
        const cursor = JSON.parse(Buffer.from(input.cursor, "base64url").toString("utf8"));
        if (cursor.version !== 1 || cursor.routine_id !== routineID || typeof cursor.after !== "string" || ![eventPrefix, taskPrefix].some(prefix => cursor.after.startsWith(prefix))) throw new Error();
        after = cursor.after;
      } catch { throw new Error("invalid routine history cursor"); }
    }
    // The host exposes key enumeration, not range queries. Bound record reads and
    // inspections per request, and expose scan order rather than claiming a
    // globally chronological page from randomly hashed legacy storage keys.
    const { keys } = await this.host.call("host.storage.keys", { scope: "user" });
    const candidates = keys.filter(key => (key.startsWith(eventPrefix) || key.startsWith(taskPrefix)) && key > after).sort();
    const scanLimit = Math.min(100, limit * 5), events = [], tasks: PersonalTask[] = [];
    let scanned = 0, skipped_records = 0;
    for (const key of candidates) {
      if (scanned >= scanLimit || events.length + tasks.length >= limit) break;
      scanned++; after = key;
      const { value } = await this.host.call("host.storage.get", { scope: "user", key });
      if (!value) continue;
      let parsed;
      try { parsed = JSON.parse(value); if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error(); }
      catch { skipped_records++; continue; }
      if (parsed.routine_id !== routineID) continue;
      if (key.startsWith(taskPrefix)) {
        const task = parsed as PersonalTask;
        if (task.user_space_id !== this.space || ![agent.agent_id, main.agent_id].includes(task.agent_id) || typeof task.task_id !== "string" || typeof task.created_at !== "string" || !["queued", "running", "waiting_user", "waiting_external", "completed", "failed", "stopped"].includes(task.state) || !task.execution || !Array.isArray(task.previous_executions)) { skipped_records++; continue; }
        tasks.push(task); continue;
      }
      const record = parsed as EventRecord;
      if (typeof record.event_id !== "string" || typeof record.request_id !== "string" || !["pending", "accepted"].includes(record.status) || [record.created_at, record.updated_at, record.scheduled_at, record.started_at, record.completed_at, record.session_id, record.state, record.output, record.error].some(value => value !== undefined && typeof value !== "string") || record.kind !== undefined && !["reminder", "task", "calendar"].includes(record.kind)) { skipped_records++; continue; }
      const kind = record.kind ?? (record.routine_revision === routine.config_revision ? routine.source ? "calendar" : routine.kind : "unknown");
      const event = { ...record, kind, state: record.state ?? (kind === "reminder" || kind === "unknown" ? "unknown" : record.status === "accepted" ? "dispatched" : "pending") };
      if (kind === "reminder") {
        const sessionID = record.session_id || agent.main_session_id;
        const owner = sessionID === main.main_session_id ? main : sessionID === agent.main_session_id ? agent : undefined;
        if (!owner) { event.state = "unknown"; event.output = undefined; event.error = "Reminder session is not bound to this Personal identity"; }
        else if (!["completed", "failed", "interrupted", "discarded"].includes(event.state)) {
          try {
            const context = await this.host.call("host.session.context", { session_id: sessionID });
            if (context.product_domain !== "personal" || context.user_space_id !== this.space || context.agent_id !== owner.agent_id) throw new Error("Reminder session identity does not match");
            const { turn } = await this.host.call("host.session.inspect", { session_id: sessionID, request_id: record.request_id });
            if (turn) Object.assign(event, { state: turn.state, started_at: turn.started_at, completed_at: turn.completed_at, output: turn.final_output, error: turn.error });
            else event.state = "unknown";
          } catch { event.state = "unknown"; event.output = undefined; }
        }
      }
      events.push(event);
    }
    events.sort((a, b) => (b.scheduled_at || b.created_at || "").localeCompare(a.scheduled_at || a.created_at || "") || a.event_id.localeCompare(b.event_id));
    tasks.sort((a, b) => b.created_at.localeCompare(a.created_at));
    const has_more = scanned < candidates.length;
    const cursor = has_more ? Buffer.from(JSON.stringify({ version: 1, routine_id: routineID, after })).toString("base64url") : undefined;
    return { routine_id: routineID, events, tasks, order: "storage_scan", scanned, skipped_records, has_more, cursor };
  }
  private async scheduler(method: string, params: unknown): Promise<Binding> {
    this.services.active();
    const value = await this.host.call("host.service.call", { service: "automation.routines", method, params }) as { binding: Binding };
    return value.binding;
  }
  private async validateSchedule(config: Pick<Routine, "cron" | "run_at" | "timezone" | "missed_policy">): Promise<void> {
    this.services.active();
    const { cron, run_at, timezone, missed_policy } = config;
    const response = await this.host.call("host.service.call", { service: "automation.routines", method: "validate", params: { cron, run_at, timezone, missed_policy } }) as { schedule?: { next_run_at?: string } };
    if (!response.schedule?.next_run_at || !Number.isFinite(Date.parse(response.schedule.next_run_at))) throw new Error("Automation must support read-only schedule validation before creating or changing routines");
  }
  private validate(input: Record<string, unknown>, previous?: Routine) {
    const title = input.title === undefined && previous ? previous.title : required(input, "title");
    const prompt = input.prompt === undefined && previous ? previous.prompt : required(input, "prompt");
    const timezone = input.timezone === undefined ? previous?.timezone || "UTC" : required(input, "timezone");
    new Intl.DateTimeFormat("en", { timeZone: timezone });
    const kind = input.kind ?? previous?.kind ?? "reminder"; if (kind !== "reminder" && kind !== "task") throw new Error("invalid routine kind");
    const cron = input.run_at ? undefined : input.cron === undefined ? previous?.cron : typeof input.cron === "string" ? input.cron : undefined;
    const run_at = input.cron ? undefined : input.run_at === undefined ? previous?.run_at : typeof input.run_at === "string" ? input.run_at : undefined;
    if (Boolean(cron) === Boolean(run_at)) throw new Error("provide exactly one cron or run_at");
    if (run_at && !Number.isFinite(Date.parse(run_at))) throw new Error("invalid run_at timestamp");
    const missed_policy = input.missed_policy ?? previous?.missed_policy ?? "run_once";
    if (missed_policy !== "run_once" && missed_policy !== "skip") throw new Error("invalid missed policy");
    let source = previous?.source;
    if (input.source !== undefined) {
      if (!input.source || typeof input.source !== "object" || Array.isArray(input.source)) throw new Error("invalid calendar source");
      const raw = input.source as Record<string, unknown>;
      if (raw.kind !== "calendar") throw new Error("only the calendar change source is supported");
      const time_min = new Date(required(raw, "time_min")).toISOString(), time_max = new Date(required(raw, "time_max")).toISOString();
      if (Date.parse(time_max) <= Date.parse(time_min) || Date.parse(time_max) - Date.parse(time_min) > 31 * 86400000) throw new Error("calendar source window must be positive and at most 31 days");
      source = { kind: "calendar", account_id: required(raw, "account_id"), generation: required(raw, "generation"), calendar_id: required(raw, "calendar_id"), time_min, time_max, timezone: required(raw, "timezone") };
      new Intl.DateTimeFormat("en", { timeZone: source.timezone });
    }
    if (source && (kind !== "task" || !cron)) throw new Error("calendar change responsibility requires kind task and one recurring cron binding");
    return { title, prompt, timezone, kind, cron, run_at, missed_policy, source } as Pick<Routine, "title" | "prompt" | "timezone" | "kind" | "cron" | "run_at" | "missed_policy" | "source">;
  }
  async create(input: Record<string, unknown>): Promise<{ routine: Routine }> {
    const agent = await this.services.agent(required(input, "agent_id")); const requestID = required(input, "request_id");
    const routineID = `routine-${hash(agent.agent_id, requestID)}`, config = this.validate(input);
    if (!await this.read<Routine>("routine", routineID)) await this.validateSchedule(config);
    let routine = await this.update<Routine>("routine", routineID, current => {
      if (current) { if (current.config_revision === 1 && Object.entries(config).some(([key, value]) => JSON.stringify(current[key as keyof Routine]) !== JSON.stringify(value))) throw new Error("request_id_conflict"); return current; }
      return { routine_id: routineID, agent_id: agent.agent_id, request_id: requestID, revision: 0, config_revision: 1, ...config, status: "active", binding: { binding_id: `personal-${routineID}`, revision: 0, status: "pending" }, created_at: now(), updated_at: now() };
    });
    if (routine.binding.status !== "active") routine = await this.sync(routine);
    return { routine };
  }
  async mutate(method: string, input: Record<string, unknown>): Promise<{ routine: Routine }> {
    return this.transition(required(input, "routine_id"), () => this.changeRoutine(method, input));
  }
  private async changeRoutine(method: string, input: Record<string, unknown>): Promise<{ routine: Routine }> {
    const routineID = required(input, "routine_id"); if (!Number.isSafeInteger(input.expected_revision)) throw new Error("expected_revision is required");
    const previous = await this.read<Routine>("routine", routineID);
    if (!previous) throw new Error("routine not found");
    if (previous.revision !== input.expected_revision) throw new Error(`revision_conflict: current revision ${previous.revision}`);
    const config = method === "update" ? this.validate(input, previous) : undefined;
    if (method === "update" || method === "resume") await this.validateSchedule(config ?? previous);
    const routine = await this.update<Routine>("routine", routineID, current => {
      if (!current) throw new Error("routine not found");
      if (current.status === "stopped" && method !== "resume") throw new Error("routine is stopped");
      if ((current.status === "stopping" || current.cancellation_pending) && ["resume", "update"].includes(method)) throw new Error("routine cancellation is still stopping; reconcile before resuming");
      if (!["update", "pause", "resume", "stop"].includes(method)) throw new Error("unknown routine action");
      const stopping = method === "pause" || method === "stop";
      return { ...current, ...(config ?? {}), status: stopping ? "stopping" : method === "update" && current.status === "paused" ? "paused" : "active", ...(method === "update" ? { cancellation_pending: true } : {}), ...(stopping ? { stop_target: method === "pause" ? "paused" as const : "stopped" as const, cancellation_pending: true } : {}), config_revision: current.config_revision + 1,
        ...(current.source || input.source ? { source_pending: undefined, ...(method === "resume" || input.source !== undefined ? { source_cursor: undefined, source_blocked: false } : {}) } : {}),
        binding: { ...current.binding, status: "pending" }, updated_at: now(), last_error: undefined };
    }, Number(input.expected_revision));
    if (routine.cancellation_pending || routine.status === "stopping") await this.cancelWork(routine);
    return { routine: await this.sync(routine) };
  }
  private async cancelWork(routine: Routine): Promise<void> {
    const { keys } = await this.host.call("host.storage.keys", { scope: "user" });
    const agent = await this.services.agent(routine.agent_id, true);
    let pending = false, error: string | undefined;
    for (const key of keys.filter(k => k.startsWith(this.key("event", "")))) {
      const { value } = await this.host.call("host.storage.get", { scope: "user", key }); if (!value) continue;
      const event = JSON.parse(value) as EventRecord;
      // The routine may already have a new kind. Cancel the original event,
      // including legacy records without kind, only by its exact session/request.
      if (event.routine_id !== routine.routine_id || event.status !== "pending" || event.kind !== undefined && event.kind !== "reminder") continue;
      try {
        const inspection = await this.host.call("host.session.inspect", { session_id: event.session_id || agent.main_session_id, request_id: event.request_id });
        if (inspection.turn && ["running", "queued"].includes(inspection.turn.state)) {
          await this.host.call("host.session.cancel", { session_id: event.session_id || agent.main_session_id, turn_id: inspection.turn.turn_id, queue_id: inspection.turn.queue_id });
          const remaining = await this.host.call("host.session.inspect", { session_id: event.session_id || agent.main_session_id, request_id: event.request_id });
          if (remaining.turn && ["running", "queued"].includes(remaining.turn.state)) { pending = true; error = "Reminder cancellation is still pending"; }
        }
      } catch (cause) { pending = true; error = `Reminder cancellation requires reconciliation: ${String(cause)}`; }
    }
    try {
      const failures = await this.services.stopTasks(routine.routine_id);
      if (failures.length) { pending = true; error = [error, ...failures].filter(Boolean).join("; "); }
    } catch (cause) { pending = true; error = [error, `Task cancellation requires reconciliation: ${String(cause)}`].filter(Boolean).join("; "); }
    await this.update<Routine>("routine", routine.routine_id, current => current?.config_revision === routine.config_revision && (current.cancellation_pending !== pending || current.last_error !== error || current.status === "stopping" && !pending) ? { ...current, status: current.status === "stopping" && !pending ? current.stop_target! : current.status, cancellation_pending: pending, last_error: error } : current!);
  }
  async sync(routine: Routine, reconciled = false): Promise<Routine> {
    const latest = await this.read<Routine>("routine", routine.routine_id);
    if (!latest || latest.config_revision !== routine.config_revision) return latest!;
    try {
      const requestID = `routine-binding-${hash(routine.routine_id, String(routine.config_revision))}`;
      const state = routine.status === "stopping" ? routine.stop_target : routine.status;
      const binding = await this.scheduler(state === "active" ? "upsert" : state === "paused" ? "pause" : "stop", {
        binding_id: routine.binding.binding_id, request_id: requestID, expected_revision: routine.binding.revision,
        ...(state === "active" ? { cron: routine.cron, run_at: routine.run_at, timezone: routine.timezone, missed_policy: routine.missed_policy, payload: { routine_id: routine.routine_id, routine_revision: routine.config_revision } } : {}),
      });
      return this.update<Routine>("routine", routine.routine_id, current => current?.config_revision === routine.config_revision ? { ...current, binding: { binding_id: binding.binding_id, revision: binding.revision, status: "active", next_run_at: binding.next_run_at }, last_error: current.cancellation_pending ? current.last_error : undefined } : current!);
    } catch (error) {
      if (!reconciled) {
        try {
          const known = await this.scheduler("read", { binding_id: routine.binding.binding_id });
          if (known?.payload?.routine_id === routine.routine_id && known.revision !== routine.binding.revision) {
            const rebased = await this.update<Routine>("routine", routine.routine_id, current => current?.config_revision === routine.config_revision ? { ...current, binding: { ...current.binding, revision: known.revision } } : current!);
            return rebased.config_revision === routine.config_revision ? this.sync(rebased, true) : rebased;
          }
        } catch { /* The original binding operation remains recoverable with its stable request identity. */ }
      }
      return this.update<Routine>("routine", routine.routine_id, current => current?.config_revision === routine.config_revision ? { ...current, binding: { ...current.binding, status: "unknown" }, last_error: `Automation unavailable or binding pending reconciliation: ${String(error)}` } : current!);
    }
  }
  async recover(): Promise<void> {
    for (const routine of await this.list()) {
      if (routine.cancellation_pending || routine.status === "stopping") await this.transition(routine.routine_id, () => this.cancelWork(routine));
      if (routine.binding.status !== "active") await this.transition(routine.routine_id, () => this.sync(routine));
    }
  }
  async pauseAgent(agentID: string): Promise<string[]> {
    const failures: string[] = [];
    for (const routine of (await this.list()).filter(r => r.agent_id === agentID)) {
      try {
        if (routine.status === "active") await this.mutate("pause", { routine_id: routine.routine_id, expected_revision: routine.revision });
        else {
          if (routine.cancellation_pending || routine.status === "stopping") await this.transition(routine.routine_id, () => this.cancelWork(routine));
          if (routine.binding.status !== "active") await this.transition(routine.routine_id, () => this.sync(routine));
        }
      } catch (error) { failures.push(`Routine ${routine.routine_id}: ${String(error)}`); }
    }
    for (const routine of (await this.list()).filter(r => r.agent_id === agentID)) if (routine.status === "active" || routine.cancellation_pending || routine.binding.status !== "active") failures.push(`Routine ${routine.routine_id}: ${routine.last_error || "pause requires reconciliation"}`);
    return failures;
  }
  async fire(input: Record<string, unknown>): Promise<{ accepted?: boolean; defer_until?: string }> {
    const payload = input.payload as { routine_id?: string } | undefined;
    if (!payload?.routine_id) throw new Error("routine trigger requires a bound payload");
    return this.transition(payload.routine_id, () => this.fireBound(input));
  }
  async canExecute(task: PersonalTask): Promise<boolean> {
    if (!task.routine_id) return true;
    const routine = await this.read<Routine>("routine", task.routine_id);
    if (!routine || routine.agent_id !== task.agent_id || routine.config_revision !== task.routine_revision || routine.cancellation_pending || routine.source_blocked) return false;
    if (routine.source && Date.now() >= Date.parse(routine.source.time_max)) return false;
    // A one-time binding can finish admitting its event before that event's task
    // finishes. It retains authority only for the unchanged admitted revision.
    return routine.status === "active" || routine.status === "completed" && !routine.cron && !routine.source;
  }
  async reminderAttention(requestID: string, agentID: string): Promise<boolean> {
    const { keys } = await this.host.call("host.storage.keys", { scope: "user" });
    for (const key of keys.filter(key => key.startsWith(this.key("event", "")))) {
      const { value } = await this.host.call("host.storage.get", { scope: "user", key });
      if (!value) continue;
      const event = JSON.parse(value) as EventRecord;
      if (event.request_id !== requestID) continue;
      const routine = await this.read<Routine>("routine", event.routine_id);
      return Boolean(agentID === (await this.services.main()).agent_id && routine && routine.kind === "reminder" && event.routine_revision === routine.config_revision && !routine.cancellation_pending &&
        (routine.status === "active" || routine.status === "completed" && !routine.cron));
    }
    return false;
  }
  private sourceMatches(source: Record<string, unknown> | undefined, expected: CalendarSource): boolean {
    return Boolean(source && source.user_space_id === this.space && ["account_id", "generation", "calendar_id", "time_min", "time_max", "timezone"].every(key => source[key] === expected[key as keyof CalendarSource]));
  }
  private async sourceError(routine: Routine, message: string, blocked = false): Promise<void> {
    await this.update<Routine>("routine", routine.routine_id, current => {
      if (current?.config_revision !== routine.config_revision || current.source_blocked && !blocked) return current!;
      const source_blocked = current.source_blocked || blocked;
      return current.last_error !== message || current.source_blocked !== source_blocked ? { ...current, last_error: message, source_blocked } : current;
    });
  }
  private async calendarChanges(routine: Routine, agent: NamedAgent): Promise<void> {
    const source = routine.source!;
    if (routine.source_blocked) return;
    if (Date.now() >= Date.parse(source.time_max)) {
      const expired = await this.update<Routine>("routine", routine.routine_id, current => current?.config_revision === routine.config_revision ? { ...current, status: "completed", config_revision: current.config_revision + 1, source_pending: undefined, binding: { ...current.binding, status: "pending" }, last_error: undefined } : current!);
      await this.sync(expired); return;
    }
    const query = { session_id: (await this.services.main()).main_session_id, account_id: source.account_id, calendar_id: source.calendar_id, time_min: source.time_min, time_max: source.time_max, timezone: source.timezone };
    try {
      const refreshed = await this.host.call("host.service.call", { service: "calendar.personal", method: "refresh", params: query }) as { state?: string; source?: Record<string, unknown>; error_code?: string };
      this.services.active();
      if (refreshed.state !== "ready") { await this.sourceError(routine, `Calendar source unavailable: ${refreshed.error_code || refreshed.state || "refresh failed"}`, ["unconnected", "reconnect_required"].includes(refreshed.state || "")); return; }
      if (!this.sourceMatches(refreshed.source, source)) { await this.sourceError(routine, "Calendar account, generation, or query changed; review and rebind this responsibility", true); return; }
      let pending = routine.source_pending;
      if (!pending) {
        const page = await this.host.call("host.service.call", { service: "calendar.personal", method: "changes", params: { ...query, generation: source.generation, ...(routine.source_cursor ? { cursor: routine.source_cursor } : {}) } }) as CalendarPage;
        this.services.active();
        if (!page.available || page.reset_required) { await this.sourceError(routine, `Calendar source requires reconciliation: ${page.reason || "unavailable"}`, page.reset_required === true); return; }
        if (!this.sourceMatches(page.source, source) || !page.cursor || typeof page.cursor !== "string" || !Array.isArray(page.changes) || page.changes.length > 128) throw new Error("invalid Calendar source page");
        if (page.baseline && page.changes.length) throw new Error("Calendar baseline must not replay historical events");
        for (const change of page.changes) if (!change.event_id || !["created", "updated", "cancelled", "removed"].includes(change.kind) || !this.sourceMatches(change.source, source) || JSON.stringify(change).length > 32000) throw new Error("invalid Calendar change provenance");
        pending = { cursor: page.cursor, changes: page.changes };
        const saved = await this.update<Routine>("routine", routine.routine_id, current => current?.status === "active" && !current.source_blocked && current.config_revision === routine.config_revision ? { ...current, source_pending: pending, last_error: undefined } : current!);
        if (saved.status !== "active" || saved.source_blocked || saved.config_revision !== routine.config_revision) return;
      }
      for (const change of pending.changes) {
        const current = await this.read<Routine>("routine", routine.routine_id);
        if (current?.status !== "active" || current.source_blocked || current.config_revision !== routine.config_revision) return;
        this.services.active();
        if (calendarChangeExpired(change, source.timezone)) continue;
        await this.services.task(routine, `calendar-change-${hash(routine.routine_id, source.generation, change.event_id)}`, agent, change);
      }
      this.services.active();
      await this.update<Routine>("routine", routine.routine_id, current => current?.status === "active" && !current.source_blocked && current.config_revision === routine.config_revision ? { ...current, source_cursor: pending!.cursor, source_pending: undefined, last_error: undefined } : current!);
    } catch (error) { this.services.active(); await this.sourceError(routine, `Calendar source pending reconciliation: ${String(error)}`); }
  }
  private async fireBound(input: Record<string, unknown>): Promise<{ accepted?: boolean; defer_until?: string }> {
    this.services.active();
    const payload = input.payload as { routine_id?: string; routine_revision?: number } | undefined;
    if (!payload?.routine_id) throw new Error("routine trigger requires a bound payload");
    const routine = await this.read<Routine>("routine", payload.routine_id);
    if (!routine || routine.status !== "active" || routine.config_revision !== payload.routine_revision || routine.binding.binding_id !== input.binding_id) return { accepted: true };
    if (routine.cancellation_pending) return { defer_until: new Date(Date.now() + 15_000).toISOString() };
    const binding = await this.scheduler("read", { binding_id: routine.binding.binding_id });
    if (!binding.pending_event || binding.pending_event.event_id !== input.event_id || binding.pending_event.binding_revision !== input.binding_revision || binding.revision !== routine.binding.revision) throw new Error("unverified_scheduler_event");
    const agent = await this.services.agent(routine.agent_id);
    const preferences = await this.services.preferences();
    if (!preferences.notifications && routine.kind === "reminder") return { accepted: true };
    const deferred = quietUntil(preferences.quiet_hours);
    if (deferred) return { defer_until: deferred };
    const eventID = required(input, "event_id");
    const main = await this.services.main();
    let record = await this.update<EventRecord>("event", hash(eventID), current => current ?? { kind: routine.source ? "calendar" : routine.kind, created_at: now(), updated_at: now(), scheduled_at: binding.pending_event!.scheduled_at, event_id: eventID, routine_id: routine.routine_id, routine_revision: routine.config_revision, revision: 0, status: "pending", request_id: `personal-reminder-${hash(routine.routine_id, eventID)}`, session_id: main.main_session_id });
    if (record.status === "accepted") return { accepted: true };
    if (routine.kind === "reminder" && (record.session_id || agent.main_session_id) !== main.main_session_id) {
      const previous = { request_id: record.request_id, session_id: record.session_id || agent.main_session_id };
      const inspection = await this.host.call("host.session.inspect", previous);
      if (inspection.turn && ["running", "queued"].includes(inspection.turn.state)) {
        await this.host.call("host.session.cancel", { session_id: previous.session_id, turn_id: inspection.turn.turn_id, queue_id: inspection.turn.queue_id });
        const remaining = await this.host.call("host.session.inspect", previous);
        if (remaining.turn && ["running", "queued"].includes(remaining.turn.state)) return { defer_until: new Date(Date.now() + 15_000).toISOString() };
      }
      record = await this.update<EventRecord>("event", hash(eventID), current => ({ ...current!, previous_delivery: previous, request_id: `personal-reminder-${hash(routine.routine_id, eventID, "main")}`, session_id: main.main_session_id }));
    }
    // Recheck the authoritative pause revision immediately before dispatch.
    const current = await this.read<Routine>("routine", routine.routine_id);
    if (current?.status !== "active" || current.config_revision !== routine.config_revision) return { accepted: true };
    this.services.active();
    if (routine.source) await this.calendarChanges(routine, agent);
    else if (routine.kind === "task") await this.services.task(routine, eventID, agent);
    else {
      let inspected = await this.host.call("host.session.inspect", { session_id: main.main_session_id, request_id: record.request_id });
      let state = inspected.turn?.state;
      if (!inspected.turn || (state === "discarded" && inspected.turn.retryable)) {
        await this.services.send(main.agent_id, { request_id: record.request_id, session_id: (await this.services.main()).main_session_id, cause: "personal.reminder", if_running: "queue",
          input: { prompt: `The user's explicit reminder is due: ${routine.prompt}\nRoutine ${routine.routine_id}; event ${eventID}. Give the reminder once. Do not infer permission for additional actions.` },
          presentation: { kind: "query_bubble", text: routine.title, name: main.name } });
        inspected = await this.host.call("host.session.inspect", { session_id: main.main_session_id, request_id: record.request_id });
        state = inspected.turn?.state;
      }
      await this.update<EventRecord>("event", hash(eventID), previous => ({ ...previous!, state: state || "unknown", updated_at: now(), started_at: inspected.turn?.started_at, completed_at: inspected.turn?.completed_at, output: inspected.turn?.final_output, error: inspected.turn?.error }));
      // A queued input can be discarded by host shutdown. Keep the scheduler's
      // event outbox until the input actually starts, retrying the same identity.
      if (!state || state === "queued" || state === "running") return { defer_until: new Date(Date.now() + 15_000).toISOString() };
      if (state !== "completed" || !inspected.turn?.final_output?.trim()) {
        await this.update<Routine>("routine", routine.routine_id, previous => previous?.config_revision === routine.config_revision ? { ...previous, last_error: `Reminder reply was not confirmed: ${inspected.turn?.error || state}` } : previous!);
      }
    }
    this.services.active();
    await this.update<EventRecord>("event", hash(eventID), previous => ({ ...previous!, status: "accepted", updated_at: now(), ...(routine.kind === "task" ? { state: "dispatched" } : {}) }));
    await this.update<Routine>("routine", routine.routine_id, previous => previous?.config_revision === routine.config_revision ? { ...previous, last_event_id: eventID } : previous!);
    return { accepted: true };
  }
  async resultAttention(task: PersonalTask): Promise<boolean> {
    if (!task.routine_id) return true;
    const routine = await this.read<Routine>("routine", task.routine_id);
    if (!routine || !await this.canExecute(task)) return false;
    const authority = await this.sourceAuthority(routine, false);
    return !authority.suppress && !authority.defer && await this.canExecute(task);
  }
  private async sourceAuthority(routine: Routine, clearDiagnostic = true): Promise<{ suppress?: boolean; defer?: boolean }> {
    if (routine.source_blocked) return { suppress: true };
    if (routine.source) {
      try {
        const agent = await this.services.agent(routine.agent_id);
        const status = await this.host.call("host.service.call", { service: "calendar.personal", method: "status", params: { session_id: (await this.services.main()).main_session_id, account_id: routine.source.account_id, calendar_id: routine.source.calendar_id, time_min: routine.source.time_min, time_max: routine.source.time_max, timezone: routine.source.timezone } }) as { state?: string; freshness?: string; account_id?: string; source?: Record<string, unknown> };
        const changed = status.account_id !== undefined && status.account_id !== routine.source.account_id || status.source && (status.source.user_space_id !== undefined && status.source.user_space_id !== this.space || ["account_id", "generation", "calendar_id", "time_min", "time_max", "timezone"].some(key => status.source![key] !== undefined && status.source![key] !== routine.source![key as keyof CalendarSource]));
        if (changed) { await this.sourceError(routine, "Calendar source changed before result delivery; review the current account and source", true); return { suppress: true }; }
        if (status.state !== "ready" || status.freshness !== "fresh" || !this.sourceMatches(status.source, routine.source)) { await this.sourceError(routine, `Calendar result waiting for source verification: ${status.state || "unavailable"}`); return { defer: true }; }
        if (clearDiagnostic) await this.update<Routine>("routine", routine.routine_id, current => current?.config_revision === routine.config_revision && current.last_error?.startsWith("Calendar result waiting") ? { ...current, last_error: undefined } : current!);
      } catch (error) { await this.sourceError(routine, `Calendar result waiting for source verification: ${String(error)}`); return { defer: true }; }
    }
    return {};
  }
  async notification(task: PersonalTask): Promise<{ suppress?: boolean; defer?: boolean }> {
    if (!task.routine_id) return {};
    const routine = await this.read<Routine>("routine", task.routine_id);
    if (!routine || routine.source_blocked || ["stopping", "paused", "stopped"].includes(routine.status)) return { suppress: true };
    if (task.routine_revision !== undefined && task.routine_revision !== routine.config_revision) return { suppress: true };
    const authority = await this.sourceAuthority(routine);
    if (authority.suppress || authority.defer) return authority;
    const preferences = await this.services.preferences();
    if (!preferences.notifications || task.result?.meaningful === false || !routine.source && routine.last_result_hash === hash(task.result?.summary ?? "")) return { suppress: true };
    if (quietUntil(preferences.quiet_hours)) return { defer: true };
    return {};
  }
  async rememberResult(task: PersonalTask): Promise<void> {
    if (!task.routine_id || !task.result) return;
    await this.update<Routine>("routine", task.routine_id, current => current ? { ...current, last_result_hash: hash(task.result!.summary) } : current!);
  }
}

function calendarChangeExpired(change: CalendarChange, timezone: string): boolean {
  const after = change.after as { end?: { date_time?: string; date?: string } } | undefined;
  const before = change.before as typeof after;
  const end = after?.end?.date_time || after?.end?.date ? after.end : before?.end;
  if (end?.date_time) return Date.parse(end.date_time) <= Date.now();
  if (end?.date) {
    const parts = new Intl.DateTimeFormat("en-CA", { timeZone: timezone, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(new Date());
    const part = (type: string) => parts.find(item => item.type === type)?.value;
    return end.date <= `${part("year")}-${part("month")}-${part("day")}`;
  }
  return false;
}

/** Evaluate local wall-clock quiet hours without assuming every day has 24 hours. */
export function quietUntil(quiet: Preferences["quiet_hours"], at = new Date()): string | undefined {
  if (!quiet.enabled) return undefined;
  const minutes = (value: string) => Number(value.slice(0, 2)) * 60 + Number(value.slice(3));
  const start = minutes(quiet.start), end = minutes(quiet.end);
  if (start === end) return undefined;
  const format = new Intl.DateTimeFormat("en-US", { timeZone: quiet.timezone, hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
  const inside = (timestamp: number) => { const fields = format.formatToParts(timestamp); const h = Number(fields.find(p => p.type === "hour")?.value), m = Number(fields.find(p => p.type === "minute")?.value), value = h * 60 + m; return start < end ? value >= start && value < end : value >= start || value < end; };
  if (!inside(at.getTime())) return undefined;
  for (let timestamp = Math.floor(at.getTime() / 60_000) * 60_000 + 60_000; timestamp <= at.getTime() + 27 * 3_600_000; timestamp += 60_000) if (!inside(timestamp)) return new Date(timestamp).toISOString();
  throw new Error("quiet-hours boundary could not be resolved");
}
