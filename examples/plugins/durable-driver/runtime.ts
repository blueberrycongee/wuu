import { createHash, randomUUID } from "node:crypto";
import {
  durableWait,
  toolService,
  driverServiceDescriptor,
  driverTurn,
  requireDriverTurnServices,
  requireDocumentStore,
  requireKernelService,
  runJSONLRuntime,
  DriverOperationError,
  DriverModelError,
  RuntimeExecutionCancellation,
  SESSION_FORK_SEQUENCE_HOST_FEATURE,
  SESSION_HISTORICAL_STATE_HOST_FEATURE,
  SESSION_DRIVER_RECOVERY_HOST_FEATURE,
  type DriverModelRound,
  type RuntimePlugin,
  type DriverExecution,
  type DriverCheckpoint,
  type DriverCreateParams,
  type DriverResumeParams,
  type DriverInstanceParams,
  type DriverTurnInput,
  type DriverMessage,
  type RuntimeHost,
  type RuntimeExecutionContext,
  type DriverRunResult,
  type ModelToolCallV1,
} from "@wuu/plugin-sdk";
import {
  phases,
  DRIVER_ID,
  DRIVER_VERSION,
  checkpoint,
  validate,
  digest,
  fault,
  codeOf,
  readSession,
  mutate,
  newTask,
  upsert,
  log,
  type State,
  type Phase,
  type SessionDocument,
} from "./state.js";
import {
  controlTool,
  startScheduler,
  cancelTasks,
  requestCancellation,
  requestCompletion,
  finishTask,
  holdOwned,
  settleCompletions,
  generationClosing,
  schedulerTick,
} from "./orchestration.js";
import {
  changeContext,
  readContextControl,
  shouldCompact,
  shouldBackgroundCompact,
  backgroundContext,
  cancelBackgroundContext,
} from "./context.js";
import { readModelUsage, applyModelUsage } from "./usage.js";
import { TaskDefinitions, type Json } from "./task-definitions.js";
interface Instance {
  execution: DriverExecution;
  checkpoint: DriverCheckpoint<State>;
  running: boolean;
  resumed: boolean;
}
interface RunTaskContext {
  instance: Instance;
  host: RuntimeHost;
  execution: RuntimeExecutionContext;
  error_code?: string;
}
const taskDefinitions = new TaskDefinitions<
  RunTaskContext,
  DriverRunResult<State>
>();
taskDefinitions.register({
  name: "durable.run",
  version: 1,
  initial(input: Json) {
    if (
      !input ||
      Array.isArray(input) ||
      typeof input !== "object" ||
      typeof input.session_id !== "string" ||
      typeof input.turn_id !== "string" ||
      Object.keys(input).some((key) => !["session_id", "turn_id"].includes(key))
    )
      throw fault("invalid_task_input");
    return {
      schema_version: 1,
      session_id: input.session_id,
      turn_id: input.turn_id,
      run_id: randomUUID(),
      phase: "ready",
      round: 0,
      attempt: 1,
      restart_count: 0,
    };
  },
  phases: Object.fromEntries(
    phases.map((phase) => [
      phase,
      ({ instance, host, execution }: RunTaskContext) =>
        run(instance, host, execution),
    ]),
  ),
  async abort({ instance, error_code }) {
    const state = instance.checkpoint.state;
    await requestCompletion(
      state.session_id,
      state.run_id,
      "aborted",
      error_code ?? "task_canceled",
    );
    return { status: "canceled", checkpoint: instance.checkpoint };
  },
});
function initialRun(state: State): State {
  return {
    ...taskDefinitions.initial("durable.run", {
      session_id: state.session_id,
      turn_id: state.turn_id,
    }),
    ...state,
  };
}
const instances = new Map<string, Instance>();
let restartLimit = 5;
let modelRetries = 3;
let retryBaseMs = 2000;
let retryMaxMs = 60000;
function retrySetting(name: string, fallback: number, maximum: number): number {
  const value = process.env[name];
  if (value === undefined) return fallback;
  const parsed = Number(value);
  if (!/^\d+$/.test(value) || !Number.isSafeInteger(parsed) || parsed > maximum)
    throw fault("invalid_retry_policy");
  return parsed;
}
function retryableModelError(error: unknown): error is DriverModelError {
  if (
    !(error instanceof DriverModelError) ||
    error.round.tool_work_started ||
    error.round.tool_batch_id ||
    error.failure.context_overflow
  )
    return false;
  const failure = error.failure;
  if (
    [
      "workflow_budget_exceeded",
      "workflow_cost_indeterminate",
      "replay_unsafe",
    ].includes(failure.recovery?.stop_reason ?? "")
  )
    return false;
  return [
    "network",
    "server",
    "overloaded",
    "rate_limit",
    "incomplete_stream",
  ].includes(failure.category);
}
let stopScheduler: (() => Promise<void>) | undefined;
const controls = [
  {
    id: "durable_wait",
    description:
      "Request expiring human approval or external input/event. Use alone for input/event; for approval put it in the same batch as the proposed tools.",
    properties: {
      kind: { type: "string", enum: ["approval", "input", "event"] },
      question: { type: "string" },
      expires_at: { type: "string" },
    },
    required: ["kind", "question", "expires_at"],
  },
  {
    id: "durable_sleep",
    description:
      "Suspend this logical task until a durable timer wakes it. Ends the physical turn without an answer.",
    properties: {
      duration_ms: { type: "integer", minimum: 1, maximum: 2592000000 },
    },
    required: ["duration_ms"],
  },
  {
    id: "durable_subagent",
    description:
      "Launch a child session, optionally forked at a historical fork_seq. Foreground waits suspend this turn; background results arrive as a deduplicated follow-up.",
    properties: {
      prompt: { type: "string" },
      background: { type: "boolean" },
      independent: { type: "boolean" },
      fork_seq: { type: "integer", minimum: 1 },
    },
    required: ["prompt"],
  },
  {
    id: "durable_cancel",
    description: "Cancel a logical task and non-independent descendants.",
    properties: {
      run_id: { type: "string" },
      include_background: { type: "boolean" },
    },
    required: [],
  },
  {
    id: "durable_context",
    description:
      "Archive-preserving active context compaction, reset, or handoff at the next safe boundary.",
    properties: {
      action: { type: "string", enum: ["compact", "reset", "handoff"] },
      note: { type: "string" },
    },
    required: ["action"],
  },
];
export const plugin: RuntimePlugin = {
  concurrentServices: ["driver.default", "durable.tasks", "durable.scheduler"],
  initialize() {
    const configured = process.env.WUU_DURABLE_RESTART_LIMIT;
    if (configured !== undefined) {
      const parsed = Number(configured);
      if (
        !/^\d+$/.test(configured) ||
        !Number.isSafeInteger(parsed) ||
        parsed < 0 ||
        parsed > 100
      )
        throw fault("invalid_restart_limit");
      restartLimit = parsed;
    }
    modelRetries = retrySetting("WUU_DURABLE_MODEL_RETRIES", 3, 100);
    retryBaseMs = retrySetting("WUU_DURABLE_RETRY_BASE_MS", 2000, 60000);
    retryMaxMs = retrySetting("WUU_DURABLE_RETRY_MAX_MS", 60000, 3600000);
    if (retryBaseMs > retryMaxMs) throw fault("invalid_retry_policy");
    return {
      protocol_version: 3,
      lifecycle_version: 2,
      required_host_features: [
        SESSION_FORK_SEQUENCE_HOST_FEATURE,
        SESSION_HISTORICAL_STATE_HOST_FEATURE,
        SESSION_DRIVER_RECOVERY_HOST_FEATURE,
      ],
      provided_services: [
        driverServiceDescriptor("default"),
        {
          name: "durable.scheduler",
          version: "1.0.0",
          methods: ["tick", "cancel"].map((name) => ({
            name,
            input_schema: `durable.scheduler.${name}.input.v1`,
            output_schema: `durable.scheduler.${name}.output.v1`,
          })),
        },
        {
          name: "durable.tasks",
          version: "1.0.0",
          methods: ["list", "get"].map((name) => ({
            name,
            input_schema: `durable.tasks.${name}.input.v1`,
            output_schema: `durable.tasks.${name}.output.v1`,
          })),
        },
      ],
      required_services: [
        ...requireDriverTurnServices(),
        requireDocumentStore(),
        { name: "host.durable-wait", major_version: 1, required: true },
        { name: "agent.tools", major_version: 1, required: true },
        { name: "host.background", major_version: 1, required: true },
        ...(
          [
            "host.session.create",
            "host.session.send",
            "host.session.inspect",
            "host.session.list",
            "host.session.cancel",
            "host.session.context",
            "host.session.history.read",
          ] as const
        ).map(requireKernelService),
      ],
      tools: controls.map((t) => ({
        id: t.id,
        direct_only: true,
        description: t.description,
        input_schema: {
          type: "object",
          properties: t.properties,
          required: t.required,
          additionalProperties: false,
        },
        activity: { orchestrator: true, concurrency_safe: true },
      })),
    };
  },
  async prepare(host) {
    await readSession(host, "__schema_probe__");
  },
  activate(host, lifecycle) {
    stopScheduler = startScheduler(host, lifecycle);
  },
  async shutdown() {
    await stopScheduler?.();
    instances.clear();
  },
  async executeTool(params, host) {
    const instance = [...instances.values()].find(
      (i) => i.running && i.execution.session_id === params.session_id,
    );
    if (!instance)
      return {
        result: {
          is_error: true,
          content: [{ type: "text", text: "durable_execution_required" }],
        },
      };
    try {
      return {
        result: {
          content: [
            {
              type: "text",
              text: JSON.stringify(
                await controlTool(params, host, instance.checkpoint.state),
              ),
            },
          ],
        },
      };
    } catch (error) {
      if (params.tool_id === "durable_subagent") {
        const s = instance.checkpoint.state,
          taskId = `${s.run_id}:${s.round}:control:${params.call_id}`;
        await mutate(host, s.session_id, (doc) => {
          if (
            doc.tasks.some((t) => t.id === taskId) &&
            doc.checkpoints[s.run_id]
          )
            doc.checkpoints[s.run_id].state.error_code =
              "uncertain_tool_effect";
        });
      }
      return {
        result: {
          is_error: true,
          content: [{ type: "text", text: codeOf(error) }],
        },
      };
    }
  },
  async invokeService(
    { service, method, params, admission, caller_scope, caller },
    host,
    execution,
  ) {
    if (service === "durable.scheduler") {
      if (caller !== DRIVER_ID) throw fault("scheduler_scope_required");
      if (method === "tick") {
        await schedulerTick(host, execution.signal, (cp) =>
          taskDefinitions.prepare(host, cp),
        );
        return {};
      }
      if (method === "cancel") {
        const input = params as {
          session_id: string;
          run_id: string;
          outcome?: "completed" | "failed" | "aborted";
          error_code?: string;
          include_background?: boolean;
        };
        if (input.outcome)
          await finishTask(
            host,
            input.session_id,
            input.run_id,
            input.outcome,
            input.error_code,
            input.include_background,
            (cp) => taskDefinitions.prepare(host, cp),
          );
        else
          await cancelTasks(
            host,
            input.session_id,
            input.run_id,
            input.include_background,
            (cp) => taskDefinitions.prepare(host, cp),
          );
        return {};
      }
      throw fault("unknown_scheduler_method");
    }
    if (service === "durable.tasks") {
      const input = params as {
        session_id: string;
        run_id?: string;
        task_id?: string;
      };
      const scoped =
        (caller_scope?.session_id ?? execution.caller?.session_id) ===
        input.session_id;
      const admitted =
        caller === "wuu.durable-client" &&
        admission?.version === 1 &&
        admission.action === `durable-tasks.${method}` &&
        createHash("sha256").update(admission.input_json).digest("hex") ===
          admission.input_digest &&
        JSON.stringify(JSON.parse(admission.input_json)) ===
          JSON.stringify(params);
      if (!scoped && !admitted) throw fault("session_scope_required");
      const { doc, revision } = await readSession(host, input.session_id);
      const blocked = new Map<string, string | undefined>();
      const tasks = doc.tasks.map((task) => {
        if (["completed", "failed", "aborted"].includes(task.status))
          return task;
        if (!blocked.has(task.run_id)) {
          const cp = doc.checkpoints[task.run_id];
          blocked.set(
            task.run_id,
            cp ? taskDefinitions.blockedReason(cp) : undefined,
          );
        }
        const reason = blocked.get(task.run_id);
        return reason ? { ...task, blocked_reason: reason } : task;
      });
      if (method === "list")
        return {
          version: 1,
          revision: String(revision),
          tasks: tasks.filter(
            (t) => !input.run_id || t.run_id === input.run_id,
          ),
        };
      if (method === "get")
        return {
          version: 1,
          revision: String(revision),
          task:
            tasks.find(
              (t) =>
                t.id === input.task_id &&
                (!input.run_id || t.run_id === input.run_id),
            ) ?? null,
        };
      throw fault("unknown_task_method");
    }
    if (method === "descriptor")
      return {
        id: DRIVER_ID,
        version: DRIVER_VERSION,
        capabilities: [
          "durable",
          "durable-recovery",
          "historical_fork",
          "tool_recovery",
          "tool_use",
          "checkpoint",
          "sleep",
          "subagent",
          "context-replacement",
        ],
      };
    if (method === "create" || method === "resume") {
      const input = params as DriverCreateParams | DriverResumeParams<State>;
      if (!input.execution?.execution_id || !input.execution.session_id)
        throw fault("execution_required");
      const session = input.execution.session_id;
      let cp: DriverCheckpoint<State>;
      if (method === "resume") {
        cp = await taskDefinitions.prepare(
          host,
          validate((input as DriverResumeParams<State>).checkpoint, session),
        );
        // Cleanup commits through lifecycle admission after a canceled physical
        // turn may outlive that turn's host checkpoint write. Never resurrect it.
        const canonical = (await readSession(host, session)).doc.checkpoints[
          cp.state.run_id
        ];
        if (
          canonical &&
          ["completing", "failed", "aborted"].includes(canonical.state.phase) &&
          !["complete", "failed", "aborted"].includes(cp.state.phase)
        )
          cp = validate(canonical, session);
        if (cp.state.phase === "complete") {
          const doc = (await readSession(host, session)).doc;
          const wake = Object.values(doc.checkpoints).find(
            (c) =>
              ["waiting", "sleeping"].includes(c.state.phase) &&
              doc.tasks.some(
                (t) =>
                  t.id === c.state.run_id &&
                  !t.canceled &&
                  t.status === "waiting",
              ) &&
              doc.tasks.some(
                (t) =>
                  t.run_id === c.state.run_id &&
                  !t.canceled &&
                  (t.dispatching || t.delivered),
              ),
          );
          cp = wake
            ? checkpoint({
                ...wake.state,
                turn_id: input.execution.execution_id,
                phase: wake.state.wait_id ? "waiting" : "ready",
              })
            : checkpoint(
                initialRun({
                  schema_version: 1,
                  session_id: session,
                  run_id: randomUUID(),
                  turn_id: input.execution.execution_id,
                  phase: "ready",
                  attempt: 1,
                  restart_count: 0,
                  round: 0,
                }),
              );
        } else {
          const suspended = cp.state.suspended === true;
          cp = checkpoint({
            ...cp.state,
            turn_id: input.execution.execution_id,
            restart_count: cp.state.restart_count + (suspended ? 0 : 1),
            attempt: cp.state.attempt + 1,
          });
        }
      } else {
        const doc = (await readSession(host, session)).doc;
        const pending = Object.values(doc.checkpoints).find(
          (c) =>
            ["sleeping", "waiting"].includes(c.state.phase) &&
            (["input", "event"].includes(c.state.wait_kind ?? "") ||
              doc.tasks.some(
                (t) =>
                  t.run_id === c.state.run_id && (t.delivered || t.dispatching),
              )),
        );
        cp = checkpoint(
          pending
            ? {
                ...pending.state,
                turn_id: input.execution.execution_id,
                phase: pending.state.wait_id ? "waiting" : "ready",
                call_ids: pending.state.wait_id
                  ? pending.state.call_ids
                  : undefined,
                receipt_ids: undefined,
                batch_id: pending.state.wait_id
                  ? pending.state.batch_id
                  : undefined,
              }
            : initialRun({
                schema_version: 1,
                session_id: session,
                run_id: randomUUID(),
                turn_id: input.execution.execution_id,
                phase: "ready",
                attempt: 1,
                restart_count: 0,
                round: 0,
              }),
        );
      }
      const instance_id = randomUUID();
      instances.set(instance_id, {
        execution: input.execution,
        checkpoint: cp,
        running: false,
        resumed: method === "resume",
      });
      return { instance_id, checkpoint: cp };
    }
    const instance = instances.get(
      (params as DriverInstanceParams).instance_id,
    );
    if (method === "shutdown") {
      instances.delete((params as DriverInstanceParams).instance_id);
      return {};
    }
    if (!instance) throw fault("unknown_driver_instance");
    if (method === "checkpoint") return instance.checkpoint;
    if (method !== "run") throw fault("unknown_driver_method");
    if (instance.running) throw fault("run_already_active");
    instance.checkpoint = await taskDefinitions.prepare(
      host,
      instance.checkpoint,
    );
    instance.running = true;
    try {
      return await taskDefinitions.dispatch(instance.checkpoint.state, {
        instance,
        host,
        execution,
      });
    } finally {
      instance.running = false;
    }
  },
};
async function run(
  instance: Instance,
  host: RuntimeHost,
  execution: RuntimeExecutionContext,
): Promise<DriverRunResult<State>> {
  const turn = driverTurn(host, instance.execution);
  let state = instance.checkpoint.state;
  const hasNewInput = (message: DriverMessage | undefined) =>
    Boolean(
      message &&
      digest(message) !== state.input?.digest &&
      // A reset note seeds future context; it is not a newly submitted request.
      !(
        state.context_operation?.trigger === "reset" &&
        message.client_id ===
          `${state.context_operation.id}:${state.context_call_id}`
      ),
    );
  const save = async (
    phase: Phase,
    patch: Partial<State> = {},
    edit?: (doc: SessionDocument, next: State) => void,
  ) => {
    const next = { ...state, ...patch, phase, suspended: undefined },
      cp = checkpoint(next);
    // Only durable semantic progress resets the consecutive crash budget.
    // Execution IDs, attempts, and repeated checkpoints are not progress.
    if (
      phase !== state.phase ||
      [
        "round",
        "model_digest",
        "call_digest",
        "receipt_ids",
        "wait_id",
        "retry_at",
        "context_revision",
      ].some(
        (key) =>
          digest(next[key as keyof State] ?? null) !==
          digest(state[key as keyof State] ?? null),
      )
    )
      next.restart_count = 0;
    await mutate(host, state.session_id, (doc) => {
      const intent = doc.checkpoints[state.run_id]?.state.context_intent;
      if (
        intent &&
        !Object.prototype.hasOwnProperty.call(patch, "context_intent")
      ) {
        next.context_intent = intent;
        next.context_call_id =
          doc.checkpoints[state.run_id]?.state.context_call_id;
      }
      doc.checkpoints[state.run_id] = cp;
      const task =
        doc.tasks.find((t) => t.id === state.run_id) ??
        newTask(next, "run", state.run_id);
      Object.assign(task, {
        phase,
        attempt: next.attempt,
        turn_ref: next.turn_id,
        updated_at: new Date().toISOString(),
        error_code: next.error_code,
      });
      task.status =
        phase === "complete"
          ? "completed"
          : phase === "failed"
            ? "failed"
            : phase === "aborted"
              ? "aborted"
              : ["waiting", "sleeping"].includes(phase)
                ? "waiting"
                : "running";
      if (["completed", "failed", "aborted"].includes(task.status))
        task.duration_ms =
          Date.parse(task.updated_at) - Date.parse(task.created_at);
      if (phase === "failed" || phase === "aborted") {
        task.usage_status = task.usage ? "incomplete" : "unavailable";
        for (const child of doc.tasks) {
          if (
            child.run_id !== next.run_id ||
            !["model_round", "tool"].includes(child.kind) ||
            ["completed", "failed", "aborted"].includes(child.status)
          )
            continue;
          // Failed describes the attempt, not proof that an effect did not run.
          // In particular uncertain_tool_effect remains an explicit error code.
          child.status = phase;
          child.phase = phase;
          child.error_code = next.error_code;
          child.updated_at = task.updated_at;
          child.duration_ms =
            Date.parse(child.updated_at) - Date.parse(child.created_at);
          if (child.kind === "model_round")
            child.usage_status = child.usage ? "incomplete" : "unavailable";
        }
      }
      upsert(doc, task);
      edit?.(doc, next);
    });
    await turn.checkpoint(cp);
    state = next;
    instance.checkpoint = cp;
    log(state);
  };
  const boundary = async (): Promise<DriverTurnInput> => {
    execution.signal.throwIfAborted();
    const { doc } = await readSession(host, state.session_id);
    if (
      doc.canceled ||
      doc.tasks.some((t) => t.id === state.run_id && t.canceled)
    )
      throw fault("task_canceled");
    const admission = await turn.admitInput();
    if (admission.decision.stop) throw fault("policy_stop");
    const input = await turn.read(),
      latest = input.messages.findLast((m) => m.role === "user" && !m.hidden);
    if (latest) {
      const identity = {
        seq: latest.seq,
        client_id: latest.client_id,
        digest: digest(latest),
      };
      if (
        state.input &&
        ((identity.seq && identity.seq === state.input.seq) ||
          (identity.client_id &&
            identity.client_id === state.input.client_id)) &&
        identity.digest !== state.input.digest
      )
        throw fault("input_identity_conflict");
      state = { ...state, input: identity };
    }
    return input;
  };
  const suspend = async (completion: { stop_reason: string }) => {
    if (state.wait_id && state.call_ids?.length) {
      const context = await turn.context();
      const ids = new Set(state.call_ids);
      const projected = context.messages.flatMap((message) => {
        if (
          message.role !== "assistant" ||
          !message.tool_calls?.some((call) => call.id && ids.has(call.id))
        )
          return [message];
        if (message.tool_calls.some((call) => !call.id || !ids.has(call.id)))
          throw fault("wait_batch_context_conflict");
        return message.content ? [{ ...message, tool_calls: undefined }] : [];
      });
      if (
        projected.length !== context.messages.length ||
        projected.some((m, i) => m !== context.messages[i])
      ) {
        if (!context.can_replace)
          throw fault("context_replacement_unavailable");
        const replaced = await turn.replaceContext(context.revision, projected);
        await save("waiting", { context_revision: replaced.revision });
      }
    }
    const receipt = await turn.complete("", [], completion);
    state = { ...state, suspended: true };
    instance.checkpoint = checkpoint(state);
    return receipt;
  };
  const scheduleRetry = async (code: string, error?: DriverModelError) => {
    const attempt = state.model_attempt ?? 1;
    if (attempt > modelRetries) return false;
    const hinted = error?.failure.retry_after_ms ?? 0;
    const delay = Math.max(
      Math.min(retryMaxMs, retryBaseMs * 2 ** Math.min(attempt - 1, 20)),
      hinted,
    );
    if (!Number.isFinite(delay) || delay > 3600000) return false;
    const retry_at = new Date(Date.now() + delay).toISOString();
    const usage = await readModelUsage(turn, state);
    await save("sleeping", { retry_at, retry_code: code }, (doc, next) => {
      recordModelFailure(doc, state, code);
      applyModelUsage(doc, state, usage);
      const timer = newTask(
        next,
        "sleep",
        `${state.run_id}:retry:${state.round}`,
      );
      timer.status = "waiting";
      timer.phase = "sleeping";
      timer.wake_at = retry_at;
      timer.request_id = `${timer.id}:wake`;
      timer.wait = {
        request_id: timer.request_id,
        kind: "sleep",
        started_at: timer.created_at,
        status: "pending",
      };
      if (!doc.tasks.some((t) => t.id === timer.id)) upsert(doc, timer);
    });
    return true;
  };
  const prepareRetry = async () => {
    // Failed assistant fragments remain archived, but cannot become a successful
    // answer or tool proposal on a later attempt. Keep newly admitted user input.
    await turn.archiveContext();
    const context = await turn.context();
    // The scheduler's persisted timer and host-authenticated plugin provenance
    // identify this control event. Its text is never an identity or authority.
    const timerID = `${state.run_id}:retry:${state.round}`;
    const timer = (await readSession(host, state.session_id)).doc.tasks.find(
      (task) =>
        task.id === timerID &&
        task.run_id === state.run_id &&
        task.owner === DRIVER_ID &&
        task.kind === "sleep",
    );
    const sentRetryWake = Boolean(
      state.retry_at &&
      timer &&
      (timer.dispatching || timer.delivery_accepted || timer.delivered),
    );
    const messages = context.messages.filter((message, index) => {
      if (
        sentRetryWake &&
        message.origin === "plugin" &&
        message.origin_id === DRIVER_ID &&
        message.cause === `durable.sleep:${timerID}` &&
        message.client_id &&
        message.seq &&
        message.seq > 0
      )
        return false;
      return (
        index < (state.model_boundary ?? 0) || message.role !== "assistant"
      );
    });
    if (messages.length !== context.messages.length) {
      if (!context.can_replace) throw fault("context_replacement_unavailable");
      await turn.replaceContext(context.revision, messages);
    }
  };
  try {
    let settings = await turn.settings();
    if (!settings.model_single_attempt)
      throw fault("single_attempt_model_required");
    if (state.phase === "completing") {
      await settleCompletions(host, state.session_id, (cp) =>
        taskDefinitions.prepare(host, cp),
      );
      const restored = (await readSession(host, state.session_id)).doc
        .checkpoints[state.run_id];
      if (!restored) throw fault("checkpoint_missing");
      state = restored.state;
      instance.checkpoint = restored;
      const incoming = await turn.read();
      const latest = incoming.messages.findLast(
        (m) => m.role === "user" && !m.hidden,
      );
      if (hasNewInput(latest)) {
        state = initialRun({
          schema_version: 1,
          session_id: state.session_id,
          run_id: randomUUID(),
          redirected_run_id: state.run_id,
          turn_id: instance.execution.execution_id,
          phase: "ready",
          attempt: 1,
          restart_count: 0,
          round: 0,
        });
        instance.checkpoint = checkpoint(state);
        await save("ready");
      } else {
        if (state.phase === "aborted")
          return { status: "canceled", checkpoint: restored };
        if (state.phase === "failed")
          throw fault(state.error_code ?? "owner_failed");
        const receipt = await turn.complete("", [], {
          stop_reason: "durable_completion",
        });
        return { status: "succeeded", ...receipt, checkpoint: restored };
      }
    }
    if (state.restart_count > restartLimit) throw fault("restart_limit");
    let current = await turn.read();
    const retryMessage = current.messages.findLast(
      (m) => m.role === "user" && !m.hidden,
    );
    const retryRequested =
      state.phase === "waiting" &&
      state.wait_id &&
      retryMessage?.content?.trim() === `/retry-wait ${state.wait_id}`;
    if (["failed", "aborted"].includes(state.phase) || retryRequested) {
      const incoming = current;
      const latest = incoming.messages.findLast(
        (m) => m.role === "user" && !m.hidden,
      );
      if (
        latest &&
        latest.origin !== "plugin" &&
        state.wait_id &&
        state.wait_kind === "approval" &&
        latest.content?.trim() === `/retry-wait ${state.wait_id}` &&
        digest(latest) !== state.input?.digest
      ) {
        await boundary();
        const wait = durableWait(host, instance.execution.execution_id);
        const previous = await wait.inspect(state.session_id, state.wait_id);
        if (
          previous.effect_state !== "not_started" ||
          !["expired", "fenced"].includes(previous.status)
        )
          throw fault("wait_retry_not_proven_safe");
        await wait.fence(state.session_id, state.wait_id);
        if (!state.model_seq || !state.call_digest)
          throw fault("tool_intent_missing");
        const history = await host.call("host.session.history.read", {
          session_id: state.session_id,
          start_seq: state.model_seq,
          end_seq: state.model_seq,
          limit: 1,
        });
        const original = history.records?.find(
          (record) => record.seq === state.model_seq,
        );
        const calls = JSON.parse(
          original?.tool_calls ?? "[]",
        ) as ModelToolCallV1[];
        if (digest(calls) !== state.call_digest)
          throw fault("tool_intent_digest_mismatch");
        const wait_id = `${state.run_id}:${state.round}:wait:${digest({ seq: latest.seq, client_id: latest.client_id }).slice(0, 16)}`;
        await save("waiting", { wait_id, error_code: undefined });
        const record = await wait.register({
          version: 1,
          id: wait_id,
          kind: "approval",
          question: previous.question,
          expires_at: new Date(Date.now() + 3600000).toISOString(),
          batch_id: state.batch_id,
          calls,
        });
        const task = newTask(state, "approval", wait_id);
        task.status = "waiting";
        task.phase = "waiting";
        task.wait = {
          request_id: wait_id,
          kind: "approval",
          started_at: record.created_at,
          expires_at: record.expires_at,
          status: "pending",
        };
        await mutate(host, state.session_id, (doc) => upsert(doc, task));
        const receipt = await suspend({ stop_reason: "durable_wait" });
        return {
          status: "succeeded",
          ...receipt,
          checkpoint: instance.checkpoint,
        };
      }
      if (
        ((latest && digest(latest) !== state.input?.digest) ||
          settings.resume_sampling) &&
        state.error_code !== "uncertain_tool_effect"
      ) {
        state = initialRun({
          schema_version: 1,
          session_id: state.session_id,
          run_id: randomUUID(),
          turn_id: instance.execution.execution_id,
          phase: "ready",
          attempt: 1,
          restart_count: 0,
          round: 0,
        });
        instance.checkpoint = checkpoint(state);
        await save("ready");
      } else throw fault(state.error_code ?? "terminal_checkpoint");
    }
    if (state.phase === "complete")
      return {
        status: "succeeded",
        receipt_id: state.receipt_id,
        checkpoint: instance.checkpoint,
      };
    if (state.retry_at) {
      const previousInput = state.input?.digest;
      current = await boundary();
      await prepareRetry();
      if (
        previousInput !== state.input?.digest &&
        current.messages.findLast((m) => m.role === "user" && !m.hidden)
          ?.origin !== "plugin"
      ) {
        state = initialRun({
          schema_version: 1,
          session_id: state.session_id,
          run_id: randomUUID(),
          redirected_run_id: state.run_id,
          turn_id: instance.execution.execution_id,
          phase: "ready",
          attempt: 1,
          restart_count: 0,
          round: 0,
          input: state.input,
        });
        instance.checkpoint = checkpoint(state);
        await save("ready");
      } else {
        if (Date.now() < Date.parse(state.retry_at)) {
          const receipt = await suspend({ stop_reason: "durable_model_retry" });
          return {
            status: "succeeded",
            ...receipt,
            checkpoint: instance.checkpoint,
          };
        }
        await save(
          "ready",
          {
            retry_at: undefined,
            retry_code: undefined,
            model_attempt: (state.model_attempt ?? 1) + 1,
            call_ids: undefined,
            receipt_ids: undefined,
            batch_id: undefined,
          },
          (doc) => {
            const timer = doc.tasks.find(
              (t) => t.id === `${state.run_id}:retry:${state.round}`,
            );
            if (timer && !timer.dispatching) {
              timer.delivered = true;
              timer.status = "completed";
              timer.phase = "complete";
              timer.updated_at = new Date().toISOString();
              timer.duration_ms =
                Date.parse(timer.updated_at) - Date.parse(timer.created_at);
              if (timer.wait) timer.wait.status = "decided";
            }
          },
        );
      }
    }
    if (["sleeping", "waiting"].includes(state.phase) && !state.wait_id) {
      const previousInput = state.input?.digest;
      current = await boundary();
      // A new admitted user instruction can cancel or redirect a suspended
      // logical task. The host queue supplies its order; no private inbox exists.
      if (previousInput !== state.input?.digest) await save("ready");
    }
    if (state.phase === "waiting" && state.wait_id) {
      const wait = durableWait(host, instance.execution.execution_id);
      const previousInput = state.input?.digest;
      current = await boundary();
      const record =
        state.wait_kind === "approval"
          ? await wait.inspect(state.session_id, state.wait_id)
          : await wait.resolve(state.wait_id);
      if (record.status === "approved" || record.status === "resolved") {
        await mutate(host, state.session_id, (doc) => {
          const task = doc.tasks.find((t) => t.id === state.wait_id);
          if (task) {
            task.status = "completed";
            task.phase = "complete";
            task.delivered = true;
            if (task.wait) task.wait.status = "decided";
          }
        });
        await save("tools_pending");
      } else if (
        record.status === "pending" &&
        state.wait_kind === "approval" &&
        previousInput !== state.input?.digest &&
        current.messages.findLast((m) => m.role === "user" && !m.hidden)
          ?.origin !== "plugin"
      ) {
        const previousRun = state.run_id;
        state = initialRun({
          schema_version: 1,
          session_id: state.session_id,
          run_id: randomUUID(),
          redirected_run_id: previousRun,
          turn_id: instance.execution.execution_id,
          phase: "ready",
          attempt: 1,
          restart_count: 0,
          round: 0,
          input: state.input,
        });
        instance.checkpoint = checkpoint(state);
        await save("ready");
      } else if (record.status === "pending") {
        const t = newTask(
          state,
          state.wait_kind === "approval" ? "approval" : "wait",
          state.wait_id,
        );
        t.status = "waiting";
        t.phase = "waiting";
        t.wait = {
          request_id: state.wait_id,
          kind: state.wait_kind ?? "approval",
          started_at: record.created_at,
          expires_at: record.expires_at,
          status: "pending",
        };
        await mutate(host, state.session_id, (doc) => {
          if (!doc.tasks.some((task) => task.id === t.id)) upsert(doc, t);
        });
        const receipt = await suspend({
          stop_reason: "durable_wait",
        });
        return {
          status: "succeeded",
          ...receipt,
          checkpoint: instance.checkpoint,
        };
      } else throw fault(`wait_${record.status}`);
    }
    if (instance.resumed && state.phase === "tools_running") {
      let results = receipts(current.messages, state);
      if (results.length !== (state.call_ids?.length ?? 0)) {
        if (
          !state.tools_execution_id ||
          !state.batch_id ||
          state.wait_kind === "approval"
        )
          throw fault("uncertain_tool_effect");
        const calls = findCalls(current.messages, state);
        if (
          calls.length !== state.call_ids?.length ||
          digest(calls) !== state.call_digest
        )
          throw fault("uncertain_tool_effect");
        const recovered = await turn.recoverTools(
          state.tools_execution_id,
          state.batch_id,
          calls,
        );
        results = receipts([...current.messages, ...recovered.messages], state);
        if (results.length !== state.call_ids.length)
          throw fault("uncertain_tool_effect");
        await turn.archiveContext();
      }
      await settleToolTasks(host, state, results);
      await save("tools_settled", {
        receipt_ids: results.map((m) => m.tool_invocation_id!),
        wait_id: undefined,
        wait_kind: undefined,
      });
    }
    if (instance.resumed && state.phase === "model_pending") {
      const committed = current.messages
        .slice(state.model_boundary ?? current.messages.length)
        .filter(
          (m) =>
            m.role === "assistant" && m.cause !== "incomplete_stream_fragment",
        );
      if (!committed.length && settings.resume_sampling) {
        // Explicit host continue may abandon an interrupted model-only request.
        // This driver never overlaps model() with tools, so there is no effect
        // intent to replay. Read host receipts before abandoning this attempt.
        const usage = await readModelUsage(turn, state);
        await mutate(host, state.session_id, (doc) => {
          recordModelFailure(doc, state, "model_interrupted");
          for (const task of doc.tasks.filter(
            (t) => t.run_id === state.run_id && t.status === "running",
          )) {
            task.status = "aborted";
            task.phase = "aborted";
            task.error_code = "model_sampling_interrupted";
            task.updated_at = new Date().toISOString();
            task.duration_ms =
              Date.parse(task.updated_at) - Date.parse(task.created_at);
          }
          applyModelUsage(doc, state, usage);
        });
        state = initialRun({
          schema_version: 1,
          session_id: state.session_id,
          run_id: randomUUID(),
          turn_id: instance.execution.execution_id,
          phase: "ready",
          attempt: 1,
          restart_count: 0,
          round: 0,
        });
        instance.checkpoint = checkpoint(state);
        await save("ready");
      } else {
        if (!committed.length) {
          if (await scheduleRetry("model_interrupted")) {
            await prepareRetry();
            const receipt = await suspend({
              stop_reason: "durable_model_retry",
            });
            return {
              status: "succeeded",
              ...receipt,
              checkpoint: instance.checkpoint,
            };
          }
          throw fault("model_retry_limit");
        }
        const calls = committed.flatMap((m) => m.tool_calls ?? []);
        const usage = await readModelUsage(turn, state);
        await mutate(host, state.session_id, (doc) => {
          const task = doc.tasks.find(
            (t) => t.id === `${state.run_id}:model:${state.round}`,
          );
          if (task && task.status !== "completed") {
            task.status = "completed";
            task.updated_at = new Date().toISOString();
            task.duration_ms =
              Date.parse(task.updated_at) - Date.parse(task.created_at);
          }
          applyModelUsage(doc, state, usage);
        });
        await save(calls.length ? "tools_pending" : "finalizing", {
          model_attempt: 1,
          overflow_compacted: undefined,
          call_ids: calls.map((c) => c.id!),
          model_digest: digest(committed),
          call_digest: digest(calls),
          model_seq: committed.findLast((m) => m.role === "assistant")?.seq,
        });
      }
    }
    for (
      let steps = 0;
      steps < Math.min(settings.max_steps || 64, 256);
      steps++
    ) {
      if (state.phase === "tools_pending") {
        current = await boundary();
        await save("tools_pending");
        let calls = findCalls(current.messages, state);
        if (
          calls.length !== state.call_ids?.length &&
          state.wait_id &&
          state.model_seq
        ) {
          const history = await host.call("host.session.history.read", {
            session_id: state.session_id,
            start_seq: state.model_seq,
            end_seq: state.model_seq,
            limit: 1,
          });
          const source = history.records?.find(
            (record) => record.seq === state.model_seq,
          );
          if (source?.tool_calls)
            calls = JSON.parse(source.tool_calls) as ModelToolCallV1[];
          if (digest(calls) !== state.call_digest)
            throw fault("tool_intent_digest_mismatch");
        }
        if (calls.length !== state.call_ids?.length)
          throw fault("tool_intent_missing");
        const waitBinding = await toolService(host).resolveOwn({
          tool_id: "durable_wait",
        });
        const waitCall = calls.find((c) => c.name === waitBinding.name);
        if (waitCall && !state.wait_id) {
          const args = JSON.parse(waitCall.arguments ?? "{}") as {
            kind: "approval" | "input" | "event";
            question: string;
            expires_at: string;
          };
          if (
            !["approval", "input", "event"].includes(args.kind) ||
            !args.question ||
            !Number.isFinite(Date.parse(args.expires_at)) ||
            Date.parse(args.expires_at) <= Date.now()
          )
            throw fault("invalid_wait");
          if (args.kind !== "approval" && calls.length !== 1)
            throw fault("generic_wait_requires_single_call");
          const wait_id = `${state.run_id}:${state.round}:wait`,
            batch_id = state.batch_id ?? `${state.run_id}:${state.round}`;
          await save("waiting", { wait_id, wait_kind: args.kind, batch_id });
          const waitClient = durableWait(host, instance.execution.execution_id);
          const record = await waitClient.register({
            version: 1,
            id: wait_id,
            kind: args.kind,
            question: args.question,
            expires_at: args.expires_at,
            ...(args.kind === "approval" ? { batch_id, calls } : {}),
          });
          const task = newTask(
            state,
            args.kind === "approval" ? "approval" : "wait",
            wait_id,
          );
          task.status = "waiting";
          task.phase = "waiting";
          task.wait = {
            request_id: wait_id,
            kind: args.kind,
            started_at: record.created_at,
            expires_at: record.expires_at,
            status: "pending",
          };
          await mutate(host, state.session_id, (doc) => upsert(doc, task));
          const receipt = await suspend({
            stop_reason: "durable_wait",
          });
          return {
            status: "succeeded",
            ...receipt,
            checkpoint: instance.checkpoint,
          };
        }
        if (state.wait_id) {
          const record = await durableWait(
            host,
            instance.execution.execution_id,
          ).inspect(state.session_id, state.wait_id);
          if (record.status !== "approved" && record.status !== "resolved")
            throw fault(`wait_${record.status}`);
          if (
            state.wait_kind === "approval" &&
            record.effect_state !== "not_started"
          )
            throw fault("uncertain_tool_effect");
        }
        await save("tools_running", {
          tools_execution_id: instance.execution.execution_id,
          batch_id: state.batch_id ?? `${state.run_id}:${state.round}`,
        });
        await mutate(host, state.session_id, (doc) => {
          for (const call of calls)
            upsert(doc, newTask(state, "tool", `${state.batch_id}:${call.id}`));
        });
        const result = await turn.tools(
          state.batch_id!,
          current.messages,
          calls,
          state.wait_kind === "approval"
            ? { approval_wait_id: state.wait_id }
            : undefined,
        );
        if (
          (await readSession(host, state.session_id)).doc.checkpoints[
            state.run_id
          ]?.state.error_code === "uncertain_tool_effect"
        )
          throw fault("uncertain_tool_effect");
        if (
          result.messages.length !== calls.length ||
          result.messages.some((m) => !m.tool_invocation_id)
        )
          throw fault("uncertain_tool_effect");
        await settleToolTasks(host, state, result.messages);
        await turn.archiveContext();
        await save("tools_settled", {
          receipt_ids: result.messages.map((m) => m.tool_invocation_id!),
          wait_id: undefined,
          wait_kind: undefined,
        });
      }
      if (
        state.phase === "tools_settled" ||
        state.phase === "waiting" ||
        state.phase === "sleeping"
      ) {
        const { doc } = await readSession(host, state.session_id),
          pending = doc.tasks.find(
            (t) =>
              t.run_id === state.run_id &&
              t.status === "waiting" &&
              (!t.dispatching || t.turn_ref === state.turn_id) &&
              !t.canceled &&
              (t.kind === "sleep" ||
                t.kind === "wait" ||
                t.kind === "approval" ||
                (t.kind === "subagent" && !t.background)),
          );
        if (
          pending &&
          doc.checkpoints[state.run_id]?.state.context_intent !== "reset"
        ) {
          await boundary();
          await save(pending.kind === "sleep" ? "sleeping" : "waiting");
          const receipt = await suspend({
            stop_reason:
              pending.kind === "sleep" ? "durable_sleep" : "durable_wait",
          });
          return {
            status: "succeeded",
            ...receipt,
            checkpoint: instance.checkpoint,
          };
        }
        const intent = doc.checkpoints[state.run_id]?.state.context_intent;
        if (intent) {
          await save("tools_settled", {
            context_intent: intent,
            context_call_id:
              doc.checkpoints[state.run_id]?.state.context_call_id,
          });
          const control = await readContextControl(host, state);
          await save("tools_settled", {
            context_intent: control.action,
            context_call_id: control.call_id,
          });
          if (state.background_compaction && control.action !== "compact")
            await cancelBackgroundContext(host, turn, state, save);
          const changed = await changeContext(
            host,
            turn,
            state,
            settings,
            control.action === "compact" ? "manual" : control.action,
            save,
            control,
          );
          if (control.action === "reset") {
            const incoming = (await turn.read()).messages.findLast(
              (message) => message.role === "user" && !message.hidden,
            );
            const continueNewInput = hasNewInput(incoming);
            // The logical completion intent must survive a lost host receipt.
            // This CAS only marks ownership; cleanup effects run after complete.
            await save(
              "completing",
              { terminal_outcome: "completed" },
              (document) => {
                const root = document.tasks.find((t) => t.id === state.run_id);
                if (root)
                  root.outcome = { status: "unanswered", reason: "reset" };
                holdOwned(document, state.run_id, "completed", "reset");
              },
            );
            if (continueNewInput) {
              await requestCompletion(
                state.session_id,
                state.run_id,
                "completed",
                "reset",
              );
              state = initialRun({
                schema_version: 1,
                session_id: state.session_id,
                run_id: randomUUID(),
                redirected_run_id: state.run_id,
                turn_id: instance.execution.execution_id,
                phase: "ready",
                attempt: 1,
                restart_count: 0,
                round: 0,
              });
              instance.checkpoint = checkpoint(state);
              await save("ready");
              continue;
            }
            const receipt = await turn.complete("", [], {
              stop_reason: "reset",
            });
            await requestCompletion(
              state.session_id,
              state.run_id,
              "completed",
              "reset",
            );
            instance.checkpoint = (
              await readSession(host, state.session_id)
            ).doc.checkpoints[state.run_id];
            return {
              status: "succeeded",
              ...receipt,
              checkpoint: instance.checkpoint,
            };
          }
          await save("ready", {
            context_intent: undefined,
            context_call_id: undefined,
            context_operation: undefined,
            context_revision: changed.revision,
            call_ids: undefined,
            receipt_ids: undefined,
            batch_id: undefined,
          });
        } else
          await save("ready", {
            call_ids: undefined,
            receipt_ids: undefined,
            batch_id: undefined,
          });
      }
      if (state.phase === "finalizing") {
        const answeredInput = state.input?.digest;
        if (state.background_compaction) {
          try {
            await backgroundContext(
              host,
              turn,
              state,
              await turn.settings(),
              save,
              true,
            );
          } catch (error) {
            execution.signal.throwIfAborted();
            log(state, codeOf(error));
          }
        }
        current = await boundary();
        if (answeredInput !== state.input?.digest) {
          await save("ready");
          continue;
        }
        await save("finalizing");
        const answer =
          current.messages.findLast(
            (m) =>
              m.role === "assistant" &&
              m.cause !== "incomplete_stream_fragment" &&
              !m.tool_calls?.length,
          )?.content ?? "";
        if (!answer.trim()) throw fault("empty_answer");
        const receipt = await turn.complete(answer);
        state = { ...state, receipt_id: receipt.receipt_id };
        instance.checkpoint = checkpoint(state);
        await mutate(host, state.session_id, (doc) => {
          doc.checkpoints[state.run_id] = instance.checkpoint;
        });
        await finishTask(
          host,
          state.session_id,
          state.run_id,
          "completed",
          undefined,
          false,
          (cp) => taskDefinitions.prepare(host, cp),
        );
        instance.checkpoint = (
          await readSession(host, state.session_id)
        ).doc.checkpoints[state.run_id];
        state = instance.checkpoint.state;
        return {
          status: "succeeded",
          ...receipt,
          checkpoint: instance.checkpoint,
        };
      }
      current = await boundary();
      await save("ready");
      settings = await turn.settings();
      const buildRequest = async () => {
        current = await boundary();
        const context = await turn.requestContext();
        return turn.transformRequest({
          ...current,
          messages: [
            ...current.messages,
            ...context
              .filter((s) => !s.durable && s.lifecycle === "request_only")
              .flatMap((s) => s.messages),
          ],
        });
      };
      let request = await buildRequest();
      if (state.background_compaction) {
        const blocking = shouldCompact(settings, request);
        try {
          const proposal = await backgroundContext(
            host,
            turn,
            state,
            settings,
            save,
            blocking,
          );
          if (proposal.status !== "running") {
            if (blocking && proposal.status !== "stale")
              await save("ready", { compaction_checked_round: state.round });
            request = await buildRequest();
          }
        } catch (error) {
          execution.signal.throwIfAborted();
          if (blocking) throw error;
          log(state, codeOf(error));
        }
      }
      if (
        state.compaction_checked_round !== state.round &&
        shouldCompact(settings, request)
      ) {
        await changeContext(host, turn, state, settings, "threshold", save);
        await save("ready", {
          compaction_checked_round: state.round,
          context_operation: undefined,
        });
        request = await buildRequest();
      } else if (
        !state.background_compaction &&
        state.background_checked_round !== state.round &&
        shouldBackgroundCompact(settings, request)
      ) {
        await save("ready", { background_checked_round: state.round });
        try {
          const proposal = await backgroundContext(
            host,
            turn,
            state,
            settings,
            save,
          );
          if (proposal.status !== "running") request = await buildRequest();
        } catch (error) {
          execution.signal.throwIfAborted();
          log(state, codeOf(error));
        }
      }
      await save("model_pending", {
        round: state.round + 1,
        tools_execution_id: undefined,
        model_attempt: state.model_attempt ?? 1,
        model_boundary: current.messages.length,
        batch_id: undefined,
      });
      const modelTask = newTask(
        state,
        "model_round",
        `${state.run_id}:model:${state.round}`,
      );
      modelTask.attempt = state.model_attempt ?? 1;
      await mutate(host, state.session_id, (doc) => upsert(doc, modelTask));
      let round: DriverModelRound;
      try {
        round = await turn.model(request, {
          recovery: "single_attempt",
          request_id: `${state.run_id}:model:${state.round}`,
        });
      } catch (error) {
        execution.signal.throwIfAborted();
        if (error instanceof DriverModelError) {
          const usage = await readModelUsage(turn, state);
          await mutate(host, state.session_id, (doc) => {
            recordModelFailure(doc, state, error.failure.category);
            applyModelUsage(doc, state, usage);
          });
        }
        if (
          error instanceof DriverModelError &&
          error.failure.context_overflow &&
          !error.round.tool_work_started &&
          !error.round.tool_batch_id &&
          settings.can_compact &&
          !settings.disable_auto_compact &&
          !state.overflow_compacted
        ) {
          await prepareRetry();
          const compacted = await changeContext(
            host,
            turn,
            state,
            settings,
            "overflow",
            save,
          );
          await save("ready", {
            overflow_compacted: true,
            context_operation: undefined,
          });
          if (compacted.status === "applied") continue;
        }
        if (
          retryableModelError(error) &&
          (await scheduleRetry(error.failure.category, error))
        ) {
          await prepareRetry();
          const receipt = await suspend({ stop_reason: "durable_model_retry" });
          return {
            status: "succeeded",
            ...receipt,
            checkpoint: instance.checkpoint,
          };
        }
        throw error;
      }
      const archived = await turn.archiveContext();
      const usage = await readModelUsage(turn, state);
      await mutate(host, state.session_id, (doc) => {
        // Each CAS attempt must project from its newly read canonical task.
        // Reusing the outer object would retain usage from a rejected commit.
        const modelTask = doc.tasks.find(
          (task) => task.id === `${state.run_id}:model:${state.round}`,
        );
        if (!modelTask) throw fault("model_task_missing");
        modelTask.status = "completed";
        modelTask.phase = "tools_pending";
        modelTask.updated_at = new Date().toISOString();
        modelTask.duration_ms =
          Date.parse(modelTask.updated_at) - Date.parse(modelTask.created_at);
        upsert(doc, modelTask);
        applyModelUsage(doc, state, usage);
      });
      const calls = round.messages.flatMap((m) => m.tool_calls ?? []);
      if (
        calls.some((c) => !c.id) ||
        calls.length > 128 ||
        new Set(calls.map((c) => c.id!)).size !== calls.length
      )
        throw fault("invalid_tool_batch");
      await save(calls.length ? "tools_pending" : "finalizing", {
        model_attempt: 1,
        overflow_compacted: undefined,
        call_ids: calls.map((c) => c.id!),
        model_digest: digest(round.messages),
        call_digest: digest(calls),
        model_seq: archived.messages.findLast((m) => m.role === "assistant")
          ?.seq,
      });
    }
    throw fault("step_limit");
  } catch (error) {
    if (
      generationClosing() ||
      codeOf(error) === "execution_shutdown" ||
      (execution.signal.reason instanceof RuntimeExecutionCancellation &&
        execution.signal.reason.kind === "shutdown")
    ) {
      // Teardown is not user cancellation. Return the last durable checkpoint,
      // never a terminal replacement that could erase retry/child intent.
      return { status: "canceled", checkpoint: instance.checkpoint };
    }
    const code =
        error instanceof DriverOperationError
          ? (error.failure.code ?? error.failure.category)
          : codeOf(error),
      canceled = execution.signal.aborted || code === "task_canceled";
    state = {
      ...state,
      phase: canceled ? "aborted" : "failed",
      error_code: code,
    };
    instance.checkpoint = checkpoint(state);
    log(state, code);
    if (!generationClosing()) {
      try {
        if (canceled)
          await taskDefinitions.abort(state, {
            instance,
            host,
            execution,
            error_code: code,
          });
        else
          await requestCompletion(
            state.session_id,
            state.run_id,
            "failed",
            code,
          );
        const persisted = (await readSession(host, state.session_id)).doc
          .checkpoints[state.run_id];
        if (persisted) instance.checkpoint = persisted;
      } catch (cleanupError) {
        log(state, codeOf(cleanupError));
      }
    }
    return canceled
      ? { status: "canceled", checkpoint: instance.checkpoint }
      : {
          status: "failed",
          checkpoint: instance.checkpoint,
          failure:
            error instanceof DriverOperationError
              ? error.failure
              : {
                  ...(code === "uncertain_tool_effect"
                    ? { code: "uncertain_tool_effect" as const }
                    : {}),
                  message: code,
                  origin: DRIVER_ID,
                  category: code,
                  provider_code: code,
                  context_overflow: false,
                },
        };
  }
}
function recordModelFailure(doc: SessionDocument, state: State, code: string) {
  const model =
    doc.tasks.find((t) => t.id === `${state.run_id}:model:${state.round}`) ??
    newTask(state, "model_round", `${state.run_id}:model:${state.round}`);
  model.attempt = state.model_attempt ?? 1;
  if (model && model.status !== "failed" && model.status !== "aborted") {
    model.status = code === "model_interrupted" ? "aborted" : "failed";
    model.phase = model.status;
    model.error_code = code;
    model.updated_at = new Date().toISOString();
    model.duration_ms =
      Date.parse(model.updated_at) - Date.parse(model.created_at);
  }
  upsert(doc, model);
}
async function settleToolTasks(
  host: RuntimeHost,
  state: State,
  messages: readonly DriverMessage[],
) {
  await mutate(host, state.session_id, (doc) => {
    for (const message of messages) {
      const task = doc.tasks.find(
        (t) => t.id === `${state.batch_id}:${message.tool_call_id}`,
      );
      if (!task) continue;
      task.error_code = message.tool_result?.is_error
        ? "tool_result_error"
        : undefined;
      task.phase = "tools_settled";
      task.updated_at = new Date().toISOString();
      task.duration_ms =
        Date.parse(task.updated_at) - Date.parse(task.created_at);
      task.receipt_id = message.tool_invocation_id;
    }
  });
  for (const message of messages)
    await finishTask(
      host,
      state.session_id,
      `${state.batch_id}:${message.tool_call_id}`,
      message.tool_result?.is_error ? "failed" : "completed",
      message.tool_result?.is_error ? "tool_result_error" : undefined,
      false,
      (cp) => taskDefinitions.prepare(host, cp),
    );
}
function findCalls(
  messages: readonly DriverMessage[],
  s: State,
): ModelToolCallV1[] {
  const wanted = new Set(s.call_ids ?? []),
    calls: ModelToolCallV1[] = [];
  for (const m of [...messages].reverse())
    for (const call of m.tool_calls ?? [])
      if (call.id && wanted.delete(call.id)) calls.push(call);
  return (s.call_ids ?? []).flatMap((id) => calls.filter((c) => c.id === id));
}
function receipts(messages: readonly DriverMessage[], s: State) {
  return (s.call_ids ?? []).flatMap((id) => {
    const result = messages
      .slice(s.model_boundary ?? 0)
      .findLast(
        (m) =>
          m.role === "tool" && m.tool_call_id === id && m.tool_invocation_id,
      );
    return result ? [result] : [];
  });
}
runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch(
  () => {
    console.error(JSON.stringify({ error_code: "runtime_transport_failed" }));
    process.exitCode = 1;
  },
);
