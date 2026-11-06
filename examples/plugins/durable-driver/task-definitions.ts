import type { DriverCheckpoint, RuntimeHost } from "@wuu/plugin-sdk";
import { checkpoint, codeOf, fault, mutate, readSession, validate, type State } from "./state.js";

export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };
export type TaskState = State & { task_name?: string; task_version?: number; task_input?: Json };
export interface TaskDefinition<Context, Result, Input extends Json = Json, Checkpoint extends { phase: string } = State> {
  name: string;
  version: number;
  initial(input: Input): Checkpoint;
  phases: Partial<{ [Phase in Checkpoint["phase"]]: (context: Context) => Promise<Result> }>;
  abort(context: Context): Promise<Result>;
  // Pure transformation: CAS conflicts may run this again. No host or effects.
  migrate?(input: Json, state: { phase: string }, fromVersion: number): { input: Input; checkpoint: Checkpoint };
}
export interface TaskRecord<Checkpoint extends { phase: string } = { phase: string }> {
  name: string;
  version: number;
  input: Json;
  checkpoint: Checkpoint;
}

function copyJson<T>(value: T): T {
  const serialized = JSON.stringify(value, (_key, item: unknown) => {
    if (typeof item === "number" && !Number.isFinite(item)) throw fault("invalid_task_state");
    if (["bigint", "function", "symbol"].includes(typeof item)) throw fault("invalid_task_state");
    return item;
  });
  if (serialized === undefined) throw fault("invalid_task_state");
  return JSON.parse(serialized) as T;
}

/** Executable provider-local task registry, independent of the builtin driver schema. */
export class TaskRegistry<Context, Result> {
  private readonly definitions = new Map<string, TaskDefinition<Context, Result, Json, { phase: string }>>();

  register<Input extends Json, Checkpoint extends { phase: string }>(definition: TaskDefinition<Context, Result, Input, Checkpoint>): void {
    if (!/^[a-z][a-z0-9._-]{0,127}$/.test(definition.name) ||
        !Number.isSafeInteger(definition.version) || definition.version < 1 ||
        typeof definition.initial !== "function" || typeof definition.abort !== "function" ||
        !Object.values(definition.phases).length ||
        Object.values(definition.phases).some(handler => typeof handler !== "function"))
      throw fault("invalid_task_definition");
    if (this.definitions.has(definition.name)) throw fault("duplicate_task_definition");
    this.definitions.set(definition.name, Object.freeze({ ...definition, phases: Object.freeze({ ...definition.phases }) }) as unknown as TaskDefinition<Context, Result, Json, { phase: string }>);
  }

  resolve(record: TaskRecord) {
    if (!Number.isSafeInteger(record.version) || record.version < 1) throw fault("invalid_task_state");
    const definition = this.definitions.get(record.name);
    if (!definition) throw fault("missing_task");
    if (record.version > definition.version) throw fault("task_too_old");
    return definition;
  }

  initial(name: string, input: Json): TaskRecord {
    const definition = this.resolve({ name, version: 1, input, checkpoint: { phase: "" } });
    const persistedInput = copyJson(input);
    const result = { name, version: definition.version, input: persistedInput,
      checkpoint: copyJson(definition.initial(copyJson(persistedInput))) };
    this.requirePhase(definition, result.checkpoint);
    return result;
  }

  requirePhase(definition: TaskDefinition<Context, Result, Json, { phase: string }>, state: { phase: string }): void {
    if (!state || !Object.hasOwn(definition.phases, state.phase)) throw fault("unknown_task_phase");
  }

  migrate(record: TaskRecord): TaskRecord {
    const definition = this.resolve(record);
    if (record.version === definition.version) {
      this.requirePhase(definition, record.checkpoint);
      return copyJson(record);
    }
    try {
      if (!definition.migrate) throw fault("migration_failed");
      const migrated = definition.migrate(copyJson(record.input), copyJson(record.checkpoint), record.version);
      const result = { name: record.name, version: definition.version,
        input: copyJson(migrated.input), checkpoint: copyJson(migrated.checkpoint) };
      this.requirePhase(definition, result.checkpoint);
      return result;
    } catch { throw fault("migration_failed"); }
  }

  async dispatch(record: TaskRecord, context: Context): Promise<Result> {
    const definition = this.resolve(record);
    if (record.version !== definition.version) throw fault("migration_failed");
    this.requirePhase(definition, record.checkpoint);
    return definition.phases[record.checkpoint.phase]!(context);
  }

  async abort(record: TaskRecord, context: Context): Promise<Result> {
    const definition = this.resolve(record);
    if (record.version !== definition.version) throw fault("migration_failed");
    return definition.abort(context);
  }
}

/** Driver adapter: persists registry records in the builtin run checkpoint. */
export class TaskDefinitions<Context, Result> {
  private readonly registry = new TaskRegistry<Context, Result>();
  register(definition: TaskDefinition<Context, Result>): void { this.registry.register(definition); }
  private record(state: TaskState): TaskRecord<State> {
    return { name: state.task_name ?? "durable.run", version: state.task_version ?? 1,
      input: state.task_input ?? {}, checkpoint: state };
  }
  private state(record: TaskRecord): TaskState {
    return { ...record.checkpoint as State, task_name: record.name,
      task_version: record.version, task_input: record.input };
  }
  initial(name: string, input: Json): State {
    const state = this.state(this.registry.initial(name, input));
    return validate(checkpoint(state), state.session_id).state;
  }
  /** Inspection never writes or runs a phase. Pure migration is previewed on
   * copies so unavailable code can be visible without corrupting its record. */
  blockedReason(supplied: DriverCheckpoint<State>): string | undefined {
    try {
      const valid = validate(supplied, supplied.state.session_id);
      const next = this.state(this.registry.migrate(this.record(valid.state)));
      for (const key of ["session_id", "run_id", "turn_id"] as const)
        if (next[key] !== valid.state[key]) throw fault("migration_failed");
      validate(checkpoint(next), valid.state.session_id);
      return undefined;
    } catch (error) {
      const code = codeOf(error);
      return ["missing_task", "task_too_old", "migration_failed"].includes(code)
        ? code : "invalid_task_state";
    }
  }
  /** Migrate the authoritative provider document before any task handler runs.
   * The checkpoint and task phase commit together under the document store CAS.
   * Host checkpoints remain a separate boundary; stale host copies reconcile here.
   */
  async prepare(host: RuntimeHost, supplied: DriverCheckpoint<State>): Promise<DriverCheckpoint<State>> {
    const valid = validate(supplied, supplied.state.session_id);
    const { doc } = await readSession(host, valid.state.session_id);
    const persisted = doc.checkpoints[valid.state.run_id];
    const current = persisted ? validate(persisted, valid.state.session_id) : valid;
    const definition = this.registry.resolve(this.record(current.state));
    const version = (current.state as TaskState).task_version ?? 1;
    if (version === definition.version) {
      this.registry.requirePhase(definition, current.state);
      // A committed migration wins over a stale host checkpoint, but normal
      // execution/turn bookkeeping remains the caller's responsibility.
      const suppliedVersion = (valid.state as TaskState).task_version ?? 1;
      const result = suppliedVersion < version ? current : valid;
      const suppliedDefinition = this.registry.resolve(this.record(result.state));
      this.registry.requirePhase(suppliedDefinition, result.state);
      return result;
    }
    let result = current;
    await mutate(host, current.state.session_id, document => {
      const original = validate(document.checkpoints[current.state.run_id] ?? valid, current.state.session_id);
      const installed = this.registry.resolve(this.record(original.state));
      const fromVersion = (original.state as TaskState).task_version ?? 1;
      if (fromVersion === installed.version) {
        this.registry.requirePhase(installed, original.state);
        result = original;
        return;
      }
      try {
        const next = this.state(this.registry.migrate(this.record(original.state)));
        for (const identity of ["session_id", "run_id", "turn_id"] as const)
          if (next[identity] !== original.state[identity]) throw fault("migration_failed");
        this.registry.requirePhase(installed, next);
        result = validate(checkpoint(next), original.state.session_id);
      } catch {
        throw fault("migration_failed");
      }
      document.checkpoints[original.state.run_id] = result;
      const task = document.tasks.find(item => item.id === original.state.run_id);
      if (task) { task.phase = result.state.phase; task.updated_at = new Date().toISOString(); }
    });
    return result;
  }

  async dispatch(state: State, context: Context): Promise<Result> {
    return this.registry.dispatch(this.record(state), context);
  }
  async abort(state: State, context: Context): Promise<Result> {
    return this.registry.abort(this.record(state), context);
  }
}
