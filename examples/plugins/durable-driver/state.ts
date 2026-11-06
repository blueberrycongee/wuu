import type { Json } from "./task-definitions.js";
import type { ContextOperation, ContextTrigger } from "./context.js";
import { createHash, randomUUID } from "node:crypto";
import {
  documentStore,
  RuntimeHostServiceError,
  type DriverCheckpoint,
  type DriverModelReceipts,
  type RuntimeHost,
} from "@wuu/plugin-sdk";
export const DRIVER_ID = "example.durable-driver";
export const DRIVER_VERSION = "1.0.0";
export const NAMESPACE = "example.durable-driver.sessions";
export const phases = [
  "ready",
  "model_pending",
  "tools_pending",
  "tools_running",
  "tools_settled",
  "waiting",
  "sleeping",
  "finalizing",
  "completing",
  "complete",
  "failed",
  "aborted",
] as const;
export type Phase = (typeof phases)[number];
export type TaskKind =
  | "run"
  | "model_round"
  | "tool"
  | "subagent"
  | "sleep"
  | "approval"
  | "wait"
  | "compaction";
export type TaskStatus =
  | "pending"
  | "running"
  | "waiting"
  | "completing"
  | "completed"
  | "failed"
  | "aborted";
export interface WaitMetadata {
  request_id: string;
  kind: "approval" | "input" | "event" | "sleep" | "subagent";
  started_at: string;
  expires_at?: string;
  status: "pending" | "decided" | "expired" | "canceled";
}
export interface Task {
  id: string;
  parent_id?: string;
  owner: string;
  session_id: string;
  run_id: string;
  kind: TaskKind;
  status: TaskStatus;
  phase: Phase;
  attempt: number;
  created_at: string;
  updated_at: string;
  error_code?: string;
  error_message?: string;
  checkpoint_ref: string;
  turn_ref: string;
  duration_ms?: number;
  usage_status?: "available" | "unavailable" | "incomplete";
  usage?: {
    input_tokens: number;
    output_tokens: number;
    cache_creation_tokens?: number;
    cache_read_tokens?: number;
  };
  usage_by_model?: Array<{
    provider: string;
    model: string;
    input_tokens: number;
    output_tokens: number;
    cache_creation_tokens: number;
    cache_read_tokens: number;
  }>;
  usage_receipts?: DriverModelReceipts["receipts"];
  usage_receipt_status?: DriverModelReceipts["status"];
  wait?: WaitMetadata;
  requester?: { session_id: string; seq: number };
  intent_seq?: number;
  intent_digest?: string;
  source_call_id?: string;
  source_seq?: number;
  child_run_id?: string;
  child_session_id?: string;
  request_id?: string;
  background?: boolean;
  independent?: boolean;
  depth?: number;
  fork_point?: number;
  context_source?: "fresh" | "fork";
  dispatching?: boolean;
  cancel_done?: boolean;
  wake_at?: string;
  delivery_accepted?: boolean;
  delivered?: boolean;
  canceled?: boolean;
  receipt_id?: string;
  compaction?: {
    trigger: ContextTrigger;
    status:
      "running" | "applied" | "unchanged" | "stale" | "failed" | "canceled";
    source_head: number;
  };
  outcome?: { status: "unanswered"; reason: "reset" };
  completion?: {
    status: "completed" | "failed" | "aborted";
    error_code?: string;
    include_background?: boolean;
  };
}
export interface Identity {
  seq?: number;
  client_id?: string;
  digest: string;
}
export interface State {
  schema_version: 1;
  task_name?: string;
  task_version?: number;
  task_input?: Json;
  suspended?: boolean;
  session_id: string;
  run_id: string;
  redirected_run_id?: string;
  turn_id: string;
  phase: Phase;
  attempt: number;
  restart_count: number;
  round: number;
  model_attempt?: number;
  retry_at?: string;
  retry_code?: string;
  input?: Identity;
  model_boundary?: number;
  model_seq?: number;
  model_digest?: string;
  call_digest?: string;
  call_ids?: string[];
  receipt_ids?: string[];
  batch_id?: string;
  wait_id?: string;
  wait_kind?: "approval" | "input" | "event";
  error_code?: string;
  context_revision?: number;
  context_intent?: "compact" | "reset" | "handoff";
  context_call_id?: string;
  context_operation?: ContextOperation;
  background_compaction?: ContextOperation;
  compaction_checked_round?: number;
  background_checked_round?: number;
  overflow_compacted?: boolean;
  tools_execution_id?: string;
  terminal_outcome?: "completed" | "failed" | "aborted";
  receipt_id?: string;
}
export interface SessionDocument {
  schema_version: 1;
  session_id: string;
  tasks: Task[];
  checkpoints: Record<string, DriverCheckpoint<State>>;
  canceled: boolean;
}
function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (value !== null && typeof value === "object")
    return Object.fromEntries(
      Object.entries(value)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([key, item]) => [key, canonical(item)]),
    );
  return value;
}
export const digest = (value: unknown) =>
  createHash("sha256")
    .update(JSON.stringify(canonical(value)))
    .digest("hex");
export function fault(code: string): Error {
  return Object.assign(new Error(code), { code });
}
export function codeOf(error: unknown): string {
  return error instanceof RuntimeHostServiceError
    ? error.code
    : typeof error === "object" && error !== null && "code" in error
      ? String(error.code)
      : "durable_operation_failed";
}
export function checkpoint(state: State): DriverCheckpoint<State> {
  const result: DriverCheckpoint<State> = {
    contract_version: 1,
    driver_id: DRIVER_ID,
    driver_version: DRIVER_VERSION,
    state,
  };
  if (Buffer.byteLength(JSON.stringify(result)) > 32768)
    throw fault("checkpoint_too_large");
  return result;
}
export function validate(
  value: DriverCheckpoint<State>,
  session: string,
): DriverCheckpoint<State> {
  if (
    value?.contract_version !== 1 ||
    value.driver_id !== DRIVER_ID ||
    value.driver_version !== DRIVER_VERSION ||
    value.state?.schema_version !== 1
  )
    throw fault("incompatible_checkpoint");
  const s = value.state;
  const allowed = new Set([
    "schema_version",
    "task_name",
    "task_version",
    "task_input",
    "suspended",
    "session_id",
    "run_id",
    "redirected_run_id",
    "turn_id",
    "phase",
    "attempt",
    "restart_count",
    "round",
    "model_attempt",
    "retry_at",
    "retry_code",
    "input",
    "model_boundary",
    "model_seq",
    "model_digest",
    "call_digest",
    "call_ids",
    "receipt_ids",
    "batch_id",
    "wait_id",
    "wait_kind",
    "error_code",
    "context_revision",
    "context_intent",
    "context_call_id",
    "context_operation",
    "background_compaction",
    "compaction_checked_round",
    "background_checked_round",
    "overflow_compacted",
    "tools_execution_id",
    "terminal_outcome",
    "receipt_id",
  ]);
  if (Object.keys(s).some((key) => !allowed.has(key)))
    throw fault("incompatible_checkpoint");
  const taskMetadata = [s.task_name, s.task_version, s.task_input];
  if (
    taskMetadata.some((value) => value !== undefined) &&
    (taskMetadata.some((value) => value === undefined) ||
      typeof s.task_name !== "string" ||
      !/^[a-z][a-z0-9._-]{0,127}$/.test(s.task_name) ||
      !Number.isSafeInteger(s.task_version) ||
      s.task_version! < 1)
  )
    throw fault("invalid_task_state");
  for (const ids of [s.call_ids, s.receipt_ids])
    if (ids?.some((value) => typeof value !== "string" || value.length > 512))
      throw fault("invalid_checkpoint");
  if (
    s.session_id !== session ||
    !phases.includes(s.phase) ||
    !s.run_id ||
    !s.turn_id ||
    !Number.isSafeInteger(s.round) ||
    s.round < 0 ||
    !Number.isSafeInteger(s.restart_count) ||
    s.restart_count < 0 ||
    !Number.isSafeInteger(s.attempt) ||
    s.attempt < 1
  )
    throw fault("invalid_checkpoint");
  if (
    (s.model_attempt !== undefined &&
      (!Number.isSafeInteger(s.model_attempt) ||
        s.model_attempt < 1 ||
        s.model_attempt > 101)) ||
    (s.retry_at !== undefined && !Number.isFinite(Date.parse(s.retry_at))) ||
    (s.retry_code !== undefined && !/^[a-z_]{1,80}$/.test(s.retry_code))
  )
    throw fault("invalid_checkpoint");
  for (const reference of [s.context_call_id, s.tools_execution_id])
    if (
      reference !== undefined &&
      (typeof reference !== "string" || reference.length > 512)
    )
      throw fault("invalid_checkpoint");
  if (
    s.terminal_outcome !== undefined &&
    !["completed", "failed", "aborted"].includes(s.terminal_outcome)
  )
    throw fault("invalid_checkpoint");
  for (const operation of [s.context_operation, s.background_compaction]) {
    if (!operation) continue;
    if (
      Object.keys(operation).some(
        (key) =>
          ![
            "id",
            "trigger",
            "source_head",
            "source_digest",
            "attempt",
            "status",
          ].includes(key),
      ) ||
      typeof operation.id !== "string" ||
      operation.id.length > 512 ||
      !["manual", "threshold", "overflow", "reset", "handoff"].includes(
        operation.trigger,
      ) ||
      !Number.isSafeInteger(operation.source_head) ||
      operation.source_head < 0 ||
      !/^[0-9a-f]{64}$/.test(operation.source_digest) ||
      !Number.isSafeInteger(operation.attempt) ||
      operation.attempt < 1 ||
      (operation.status !== undefined &&
        !["applied", "unchanged", "stale"].includes(operation.status))
    )
      throw fault("invalid_checkpoint");
  }
  if ((s.call_ids?.length ?? 0) > 128 || (s.receipt_ids?.length ?? 0) > 128)
    throw fault("checkpoint_too_large");
  return checkpoint(s);
}
export function newTask(
  s: State,
  kind: TaskKind,
  id: string,
  parent_id = s.run_id,
): Task {
  const now = new Date().toISOString();
  return {
    id,
    parent_id: kind === "run" ? undefined : parent_id,
    owner: DRIVER_ID,
    session_id: s.session_id,
    run_id: s.run_id,
    kind,
    status: "running",
    phase: s.phase,
    attempt: s.attempt,
    created_at: now,
    updated_at: now,
    checkpoint_ref: s.run_id,
    turn_ref: s.turn_id,
  };
}
export function upsert(doc: SessionDocument, task: Task): void {
  const index = doc.tasks.findIndex((item) => item.id === task.id);
  if (index < 0) doc.tasks.push(task);
  else doc.tasks[index] = task;
  const live = doc.tasks.filter(
    (t) => !["completed", "failed", "aborted"].includes(t.status),
  );
  if (live.length > 256) throw fault("task_limit");
  // Completed ancestors are still ownership edges for live background work.
  // Keep those edges until descendants settle, even beyond recent history.
  const retainedTasks = new Set(live.map((t) => t.id));
  const byId = new Map(doc.tasks.map((t) => [t.id, t]));
  for (const task of live) {
    let parent = task.parent_id;
    const visited = new Set<string>();
    while (parent && !visited.has(parent)) {
      visited.add(parent);
      retainedTasks.add(parent);
      parent = byId.get(parent)?.parent_id;
    }
  }
  const terminal = doc.tasks
    .filter((t) => !retainedTasks.has(t.id))
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
    .slice(0, 128);
  doc.tasks = [
    ...doc.tasks.filter((t) => retainedTasks.has(t.id)),
    ...terminal,
  ];
  const retained = new Set(doc.tasks.map((t) => t.run_id));
  for (const key of Object.keys(doc.checkpoints))
    if (!retained.has(key)) delete doc.checkpoints[key];
}
export async function readSession(host: RuntimeHost, session_id: string) {
  const view = await documentStore(host).read<SessionDocument>({
    namespace: NAMESPACE,
    keys: [session_id],
    limit: 1,
  });
  const doc = view.documents[0]?.value ?? {
    schema_version: 1 as const,
    session_id,
    tasks: [],
    checkpoints: {},
    canceled: false,
  };
  if (
    doc.schema_version !== 1 ||
    doc.session_id !== session_id ||
    !Array.isArray(doc.tasks)
  )
    throw fault("incompatible_document");
  return { revision: view.revision, doc };
}
export async function mutate(
  host: RuntimeHost,
  session_id: string,
  change: (doc: SessionDocument) => void,
) {
  const store = documentStore(host);
  for (let attempt = 0; attempt < 12; attempt++) {
    const { revision, doc } = await readSession(host, session_id);
    change(doc);
    if (Buffer.byteLength(JSON.stringify(doc)) > 524288)
      throw fault("checkpoint_too_large");
    const transaction = {
      id: randomUUID(),
      expected_revision: revision,
      writes: [{ namespace: NAMESPACE, key: session_id, value: doc }],
    };
    try {
      return await store.commit(transaction);
    } catch (error) {
      if (codeOf(error) === "state_conflict") continue;
      // An uncertain commit is reconciled only by replaying the identical store transaction.
      if (codeOf(error) === "canceled" || codeOf(error) === "permission_denied")
        throw error;
      return await store.commit(transaction);
    }
  }
  throw fault("state_contention");
}
export function log(s: State, code?: string) {
  console.error(
    JSON.stringify({
      run_id: s.run_id,
      task_id: s.run_id,
      phase: s.phase,
      attempt: s.attempt,
      restart_count: s.restart_count,
      error_code: code,
    }),
  );
}
