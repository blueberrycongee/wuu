import {
  durableWait,
  documentStore,
  type RuntimeHost,
  type DriverCheckpoint,
  type HostServiceMethod,
  type HostServiceContracts,
  type RuntimeHostCallOptions,
  type RuntimeLifecycleContext,
  type ToolExecuteParams,
} from "@wuu/plugin-sdk";
import {
  NAMESPACE,
  digest,
  codeOf,
  fault,
  mutate,
  newTask,
  readSession,
  upsert,
  type SessionDocument,
  type State,
  type Task,
} from "./state.js";

function tracedHost(host: RuntimeHost): RuntimeHost {
  return {
    supports: (method) => host.supports(method),
    async call<M extends HostServiceMethod>(
      method: M,
      params: HostServiceContracts[M]["params"],
      options?: RuntimeHostCallOptions,
    ) {
      try {
        return await host.call(method, params, options);
      } catch (error) {
        if (error && typeof error === "object")
          Object.assign(error, { operation: method });
        throw error;
      }
    },
  };
}
const sessionOperations = new Map<string, Promise<void>>();
async function serialize<T>(
  session: string,
  operation: () => Promise<T>,
): Promise<T> {
  const previous = sessionOperations.get(session) ?? Promise.resolve();
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const tail = previous.then(() => held);
  sessionOperations.set(session, tail);
  await previous;
  try {
    return await operation();
  } finally {
    release();
    if (sessionOperations.get(session) === tail)
      sessionOperations.delete(session);
  }
}
let lifecycleHost: RuntimeHost | undefined;
let lifecycleSignal: AbortSignal | undefined;
type Outcome = "completed" | "failed" | "aborted";
const terminal = (task: Task) =>
  ["completed", "failed", "aborted"].includes(task.status);
export function generationClosing() {
  return lifecycleSignal?.aborted === true || !lifecycleHost;
}

/** Run-scoped hosts cannot persist cleanup after their execution is canceled. */
export async function requestCompletion(
  session: string,
  run: string,
  outcome: Outcome,
  errorCode?: string,
  includeBackground = false,
) {
  if (generationClosing()) throw fault("generation_closed");
  const host = lifecycleHost!;
  const job = (await host.call("host.service.call", {
    service: "host.background",
    method: "start",
    params: {
      key: `complete-${run}-${outcome}-${includeBackground}`,
      service: "durable.scheduler",
      method: "cancel",
      params: {
        session_id: session,
        run_id: run,
        outcome,
        error_code: errorCode,
        include_background: includeBackground,
      },
      timeout_ms: 30000,
    },
  })) as { job_id: string };
  for (let i = 0; i < 300; i++) {
    const result = (await host.call("host.service.call", {
      service: "host.background",
      method: "inspect",
      params: { job_id: job.job_id },
    })) as { state: string; error?: { code: string } };
    if (result.state !== "running") {
      if (result.error) throw fault(result.error.code);
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw fault("cancellation_pending");
}
export async function requestCancellation(
  session: string,
  run: string,
  includeBackground = false,
) {
  return requestCompletion(
    session,
    run,
    "aborted",
    "task_canceled",
    includeBackground,
  );
}

/** Background tasks cut ownership traversal; an inclusive abort crosses them. */
function ownedTasks(
  doc: SessionDocument,
  owner: string,
  includeBackground: boolean,
) {
  const found = new Set<string>();
  const visit = (id: string) => {
    for (const task of doc.tasks) {
      if (task.parent_id !== id || found.has(task.id)) continue;
      if (task.background && !includeBackground) continue;
      found.add(task.id);
      visit(task.id);
    }
  };
  visit(owner);
  return found;
}
function hold(
  task: Task,
  outcome: Outcome,
  errorCode?: string,
  includeBackground = false,
) {
  if (terminal(task)) {
    // Completed foreground conversations may still own background descendants.
    // An inclusive abort must visit them without rewriting their settled outcome.
    if (
      includeBackground &&
      task.kind === "subagent" &&
      !task.completion?.include_background
    ) {
      task.canceled = true;
      task.cancel_done = false;
      task.completion = {
        status: task.status as Outcome,
        include_background: true,
      };
    }
    return;
  }
  // An abort replaces held success. A late successful invocation cannot erase
  // an abort or failure that already committed while its children were live.
  const held = task.completion?.status;
  const status = held && held !== "completed" ? held : outcome;
  task.completion = {
    status,
    error_code: task.completion?.error_code ?? errorCode,
    include_background:
      task.completion?.include_background || includeBackground,
  };
  task.status = "completing";
  task.phase = "completing";
  task.updated_at = new Date().toISOString();
  if (status !== "completed") {
    task.canceled = true;
    task.error_code = task.completion.error_code;
    if (task.wait) task.wait.status = "canceled";
    // Terminal failure does not invalidate complete host accounting receipts.
    if (task.kind === "model_round" && task.usage_receipt_status !== "complete")
      task.usage_status = task.usage ? "incomplete" : "unavailable";
  }
}
export function holdOwned(
  doc: SessionDocument,
  id: string,
  outcome: Outcome,
  errorCode?: string,
  includeBackground = false,
) {
  const task = doc.tasks.find((t) => t.id === id);
  if (task) hold(task, outcome, errorCode, includeBackground);
  if (outcome !== "completed" || task?.outcome?.reason === "reset") {
    const owned = ownedTasks(doc, id, includeBackground);
    for (const child of doc.tasks)
      if (owned.has(child.id))
        hold(child, "aborted", "owner_ended", includeBackground);
  }
  if (
    task?.kind === "run" &&
    outcome !== "completed" &&
    doc.tasks.some(
      (round) =>
        round.run_id === task.run_id &&
        round.kind === "model_round" &&
        round.usage_receipt_status !== "complete",
    )
  )
    task.usage_status = task.usage ? "incomplete" : "unavailable";
  const cp = doc.checkpoints[id];
  if (cp && task?.status === "completing") {
    cp.state.phase = "completing";
    cp.state.terminal_outcome = task.completion!.status;
    cp.state.error_code = task.completion!.error_code;
  }
}
export type PrepareRun = (
  checkpoint: DriverCheckpoint<State>,
) => Promise<DriverCheckpoint<State>>;
async function preparedRuns(
  host: RuntimeHost,
  session: string,
  prepareRun?: PrepareRun,
  requestedRun?: string,
) {
  if (!prepareRun) return undefined;
  const { doc } = await readSession(host, session);
  // Terminal history needs no executable definition until an operation targets it.
  const active = new Set(
    doc.tasks
      .filter((t) => !terminal(t) || (t.canceled && !t.cancel_done))
      .map((t) => t.run_id),
  );
  if (requestedRun) active.add(requestedRun);
  const ready = new Set<string>();
  for (const cp of Object.values(doc.checkpoints).filter((cp) =>
    active.has(cp.state.run_id),
  )) {
    try {
      await prepareRun(cp);
      ready.add(cp.state.run_id);
    } catch (error) {
      console.error(
        JSON.stringify({
          run_id: cp.state.run_id,
          phase: "preparing",
          error_code: codeOf(error),
        }),
      );
    }
  }
  return ready;
}
export async function finishTask(
  host: RuntimeHost,
  session: string,
  taskId: string,
  outcome: Outcome,
  errorCode?: string,
  includeBackground = false,
  prepareRun?: PrepareRun,
) {
  return serialize(session, async () => {
    const owner = (await readSession(host, session)).doc.tasks.find(
      (t) => t.id === taskId,
    );
    const ready = await preparedRuns(host, session, prepareRun, owner?.run_id);
    if (ready && (!owner || !ready.has(owner.run_id)))
      throw fault("task_definition_blocked");
    await mutate(host, session, (doc) =>
      holdOwned(doc, taskId, outcome, errorCode, includeBackground),
    );
    await settleCompletionsLocked(host, session, prepareRun);
  });
}
export async function cancelTasks(
  host: RuntimeHost,
  session: string,
  run?: string,
  includeBackground = false,
  prepareRun?: PrepareRun,
) {
  return serialize(session, async () => {
    const ready = await preparedRuns(host, session, prepareRun, run);
    if (run && ready && !ready.has(run)) throw fault("task_definition_blocked");
    await markCancellation(host, session, run, includeBackground, ready);
    await settleCompletionsLocked(host, session, prepareRun);
  });
}
async function markCancellation(
  host: RuntimeHost,
  session: string,
  run?: string,
  includeBackground = false,
  ready?: Set<string>,
) {
  await mutate(host, session, (doc) => {
    const roots = run
      ? [run]
      : doc.tasks.filter((t) => t.kind === "run").map((t) => t.id);
    for (const id of roots.filter((id) => !ready || ready.has(id)))
      holdOwned(doc, id, "aborted", "task_canceled", includeBackground);
  });
}
export async function settleCompletions(
  host: RuntimeHost,
  session: string,
  prepareRun?: PrepareRun,
) {
  return serialize(session, () =>
    settleCompletionsLocked(host, session, prepareRun),
  );
}
async function settleCompletionsLocked(
  host: RuntimeHost,
  session: string,
  prepareRun?: PrepareRun,
) {
  const ready = await preparedRuns(host, session, prepareRun);
  const { doc } = await readSession(host, session);
  // Older checkpoints may already contain terminal cancellation tombstones.
  for (const task of doc.tasks.filter(
    (t) => t.canceled && !t.cancel_done && (!ready || ready.has(t.run_id)),
  )) {
    if (task.kind === "subagent") {
      let child = task.child_session_id;
      if (!child) {
        const created = await host.call("host.session.create", {
          request_id: task.request_id!,
          parent_session_id: session,
          visibility: "plugin",
          context_source: task.context_source ?? "fresh",
          fork_seq:
            task.context_source === "fork" ? task.fork_point : undefined,
          workspace: "shared",
          name: "Durable subagent",
        });
        child = created.session_id;
        await mutate(host, session, (next) => {
          const live = next.tasks.find((t) => t.id === task.id);
          if (live) live.child_session_id = child;
        });
      }
      // Persist descendants first. A physical cancellation alone would leave
      // suspended children runnable after a restart. Never hold a child lock
      // across host cancellation, which may re-enter its driver's catch path.
      const inclusive = task.completion?.include_background === true;
      const childReady = await preparedRuns(host, child, prepareRun);
      const childBefore = (await readSession(host, child)).doc;
      if (
        childReady &&
        childBefore.tasks.some((t) => !terminal(t) && !childReady.has(t.run_id))
      )
        continue;
      await serialize(child, () =>
        markCancellation(host, child!, undefined, inclusive, childReady),
      );
      await host.call("host.session.cancel", { session_id: child });
      await settleCompletions(host, child, prepareRun);
      const childDoc = (await readSession(host, child)).doc;
      if (
        childDoc.tasks.some(
          (t) => t.canceled && (!t.cancel_done || !terminal(t)),
        )
      )
        continue;
      const inspected = await host.call("host.session.inspect", {
        session_id: child,
        wait: "none",
      });
      // cancel() acknowledges a request, not that the physical execution stopped.
      if (
        inspected.session.state === "running" ||
        (inspected.turn && !inspected.turn.completed_at)
      )
        continue;
    }
    if (task.kind === "approval" || task.kind === "wait") {
      const wait = durableWait(host, "");
      const record = await wait.inspect(session, task.id);
      // An unknown external effect remains unknown; fencing never proves rollback.
      if (record.effect_state === "not_started")
        await wait.fence(session, task.id);
    }
    await mutate(host, session, (next) => {
      const live = next.tasks.find((t) => t.id === task.id);
      if (live) live.cancel_done = true;
    });
  }
  await mutate(host, session, (next) => {
    // A single commit settles leaves before their owners, including completed
    // tool calls that hold a foreground child result without holding a turn open.
    let changed: boolean;
    do {
      changed = false;
      for (const task of next.tasks) {
        if (
          task.status !== "completing" ||
          !task.completion ||
          (ready && !ready.has(task.run_id))
        )
          continue;
        const owned = ownedTasks(
          next,
          task.id,
          task.completion.include_background === true,
        );
        if (
          next.tasks.some(
            (t) =>
              owned.has(t.id) &&
              (!terminal(t) || (t.canceled && !t.cancel_done)),
          )
        )
          continue;
        if (task.canceled && !task.cancel_done) continue;
        const outcome = task.completion;
        task.status = outcome.status;
        task.phase =
          outcome.status === "completed" ? "complete" : outcome.status;
        task.error_code = outcome.error_code;
        task.updated_at = new Date().toISOString();
        task.duration_ms =
          Date.parse(task.updated_at) - Date.parse(task.created_at);
        const cp = next.checkpoints[task.id];
        if (cp) {
          cp.state.phase = task.phase;
          cp.state.terminal_outcome = undefined;
          cp.state.error_code = outcome.error_code;
        }
        changed = true;
      }
    } while (changed);
  });
}

export async function controlTool(
  input: ToolExecuteParams,
  host: RuntimeHost,
  s: State,
) {
  host = tracedHost(host);
  const args = input.arguments as Record<string, unknown>;
  const id = `${s.run_id}:${s.round}:control:${input.call_id}`;
  if (input.tool_id === "durable_wait") {
    if (!s.wait_id || !s.call_ids?.includes(input.call_id))
      throw fault("wait_admission_required");
    const wait = await durableWait(host, s.turn_id).inspect(
      s.session_id,
      s.wait_id,
    );
    if (wait.status !== "approved" && wait.status !== "resolved")
      throw fault("wait_admission_required");
    if (s.wait_kind === "approval" && wait.batch_id !== s.batch_id)
      throw fault("wait_admission_required");
    return { decided: true, request_id: s.wait_id };
  }
  if (input.tool_id === "durable_sleep") {
    const ms = Number(args.duration_ms);
    if (!Number.isSafeInteger(ms) || ms < 1 || ms > 30 * 86400000)
      throw fault("invalid_sleep_duration");
    const task = newTask(s, "sleep", id);
    task.status = "waiting";
    task.phase = "sleeping";
    task.wake_at = new Date(Date.now() + ms).toISOString();
    task.request_id = `${id}:wake`;
    task.wait = {
      request_id: task.request_id,
      kind: "sleep",
      started_at: task.created_at,
      status: "pending",
    };
    await mutate(host, s.session_id, (doc) => {
      if (!doc.tasks.some((t) => t.id === id)) upsert(doc, task);
    });
    return { task_id: id, wake_at: task.wake_at };
  }
  if (input.tool_id === "durable_subagent") {
    return serialize(s.session_id, async () => {
      if (
        typeof args.prompt !== "string" ||
        !args.prompt.trim() ||
        args.prompt.length > 131072
      )
        throw fault("invalid_subagent_prompt");
      if (
        args.fork_seq !== undefined &&
        (!Number.isSafeInteger(args.fork_seq) || Number(args.fork_seq) < 1)
      )
        throw fault("invalid_fork_sequence");
      const lineage = await host.call("host.session.context", {
        session_id: s.session_id,
      });
      let parent = lineage.parent_session_id,
        depth = 0;
      while (parent) {
        if (++depth >= 4) throw fault("subagent_depth_limit");
        parent = (
          await host.call("host.session.context", { session_id: parent })
        ).parent_session_id;
      }
      const task = newTask(s, "subagent", id, `${s.batch_id}:${input.call_id}`);
      task.request_id = `${id}:create`;
      task.background = args.background === true;
      // Background is the ownership boundary; the old independent flag is inert.
      task.depth = depth + 1;
      task.context_source = args.fork_seq === undefined ? "fresh" : "fork";
      task.fork_point = args.fork_seq as number | undefined;
      task.requester = input.source_message;
      task.source_call_id = input.call_id;
      task.source_seq = input.source_message?.seq;
      task.intent_seq = s.model_seq;
      task.intent_digest = s.call_digest;
      task.status = "waiting";
      task.phase = "waiting";
      task.wait = {
        request_id: task.request_id,
        kind: "subagent",
        started_at: task.created_at,
        status: "pending",
      };
      await mutate(host, s.session_id, (doc) => {
        if (doc.tasks.some((t) => t.id === id)) return;
        if (
          doc.tasks.filter(
            (t) =>
              t.kind === "subagent" &&
              !["completed", "failed", "aborted"].includes(t.status),
          ).length >= 8
        )
          throw fault("subagent_limit");
        upsert(doc, task);
      });
      // Creation and initial launch use one host receipt, so a lost create reply is
      // discoverable by request identity without persisting a second copy of prompt.
      const child = await host.call("host.session.create", {
        request_id: task.request_id,
        parent_session_id: s.session_id,
        visibility: "plugin",
        context_source: task.context_source ?? "fresh",
        fork_seq: task.context_source === "fork" ? task.fork_point : undefined,
        workspace: "shared",
        name: "Durable subagent",
      });
      await mutate(host, s.session_id, (doc) => {
        const t = doc.tasks.find((t) => t.id === id)!;
        t.child_session_id = child.session_id;
      });
      await host.call("host.session.send", {
        session_id: child.session_id,
        request_id: `${id}:input`,
        input: { prompt: args.prompt },
      });
      return {
        task_id: id,
        session_id: child.session_id,
        background: task.background,
      };
    });
  }
  if (input.tool_id === "durable_cancel") {
    await requestCancellation(
      s.session_id,
      typeof args.run_id === "string"
        ? args.run_id
        : (s.redirected_run_id ?? s.run_id),
      args.include_background === true,
    );
    return { canceled: true };
  }
  if (input.tool_id === "durable_context") {
    if (!["compact", "reset", "handoff"].includes(String(args.action)))
      throw fault("invalid_context_action");
    if (args.note !== undefined && typeof args.note !== "string")
      throw fault("invalid_context_note");
    if (
      args.action === "handoff" &&
      !(typeof args.note === "string" && args.note.trim())
    )
      throw fault("handoff_note_required");
    await mutate(host, s.session_id, (doc) => {
      const cp = doc.checkpoints[s.run_id];
      if (!cp) throw fault("checkpoint_missing");
      if (cp.state.context_intent !== "reset" || args.action === "reset") {
        cp.state.context_intent = args.action as State["context_intent"];
        cp.state.context_call_id = input.call_id;
      }
    });
    return { scheduled: true };
  }
  throw fault("unknown_durable_control");
}

let cursor: string | undefined;
/** Each tick is a finite host.background job. Publication revokes the old
 * generation's admission before the successor scheduler can start. */
export async function schedulerTick(
  host: RuntimeHost,
  signal: AbortSignal,
  prepareRun?: PrepareRun,
) {
  signal.throwIfAborted();
  const view = await documentStore(host).read<SessionDocument>({
    namespace: NAMESPACE,
    start: cursor,
    limit: 16,
  });
  cursor = view.next_cursor ? `${view.next_cursor}\0` : undefined;
  for (const { value: stored } of view.documents) {
    signal.throwIfAborted();
    if (stored.schema_version !== 1) throw fault("incompatible_document");
    const ready = await preparedRuns(host, stored.session_id, prepareRun);
    const { doc } = await readSession(host, stored.session_id);
    if (
      doc.tasks.some(
        (t) => t.status === "completing" || (t.canceled && !t.cancel_done),
      )
    ) {
      try {
        await settleCompletions(host, doc.session_id, prepareRun);
      } catch (error) {
        if (signal.aborted) throw error;
        console.error(
          JSON.stringify({
            session_id: doc.session_id,
            phase: "completing",
            error_code: codeOf(error),
          }),
        );
        continue;
      }
    }
    // Only the host can prove an interrupted physical execution is eligible.
    // A stale checkpoint or elapsed time never authorizes another admission.
    for (const root of doc.tasks.filter(
      (t) =>
        t.kind === "run" &&
        !terminal(t) &&
        !t.canceled &&
        !t.completion &&
        (!ready || ready.has(t.run_id)),
    )) {
      signal.throwIfAborted();
      try {
        const inspected = await host.call(
          "host.session.inspect",
          { session_id: doc.session_id, turn_id: root.turn_ref, wait: "none" },
          { signal },
        );
        if (!inspected.recovery) continue;
        await host.call(
          "host.session.send",
          {
            session_id: doc.session_id,
            request_id: `recover:${inspected.recovery.turn_id}:${inspected.recovery.checkpoint_token}`,
            recover_after_turn_id: inspected.recovery.turn_id,
            recovery_checkpoint_token: inspected.recovery.checkpoint_token,
            control_revision: inspected.recovery.control_revision,
            input: { prompt: "" },
          },
          { signal },
        );
      } catch (error) {
        if (signal.aborted) throw error;
        // Lease/latest-turn/token conflicts leave work with its current owner.
        console.error(
          JSON.stringify({
            session_id: doc.session_id,
            phase: "recovery_admission",
            error_code: codeOf(error),
          }),
        );
      }
    }
    for (const task of doc.tasks.filter(
      (t) =>
        ["sleep", "subagent", "approval", "wait"].includes(t.kind) &&
        !t.canceled &&
        !t.delivered &&
        t.status === "waiting" &&
        (!ready || ready.has(t.run_id)),
    )) {
      signal.throwIfAborted();
      try {
        await serialize(doc.session_id, () =>
          advance(host, doc.session_id, task, signal),
        );
      } catch (error) {
        if (signal.aborted) throw error;
        if (task.error_code !== codeOf(error))
          await mutate(host, doc.session_id, (next) => {
            const t = next.tasks.find((t) => t.id === task.id);
            if (t) {
              t.error_code = codeOf(error);
              t.error_message =
                typeof error === "object" &&
                error !== null &&
                "operation" in error
                  ? String(error.operation)
                  : undefined;
              t.updated_at = new Date().toISOString();
            }
          });
        console.error(
          JSON.stringify({
            task_id: task.id,
            phase: task.phase,
            error_code: codeOf(error),
          }),
        );
      }
    }
  }
}
export function startScheduler(
  host: RuntimeHost,
  lifecycle: RuntimeLifecycleContext,
) {
  lifecycleHost = host;
  lifecycleSignal = lifecycle.signal;
  let stopped = false,
    timer: ReturnType<typeof setTimeout> | undefined,
    running: Promise<void> | undefined;
  const submit = async () => {
    if (stopped || lifecycle.signal.aborted) return;
    try {
      await host.call("host.service.call", {
        service: "host.background",
        method: "start",
        params: {
          key: "durable-scheduler",
          service: "durable.scheduler",
          method: "tick",
          params: {},
          timeout_ms: 30000,
        },
      });
    } catch (error) {
      const code = codeOf(error);
      if (code === "generation_closed" || code === "generation_invalid") {
        stopped = true;
        return;
      }
      if (code !== "background_conflict")
        console.error(JSON.stringify({ phase: "scheduler", error_code: code }));
    }
    if (!stopped && !lifecycle.signal.aborted)
      timer = setTimeout(() => {
        running = submit();
      }, 500);
  };
  running = submit();
  lifecycle.signal.addEventListener(
    "abort",
    () => {
      stopped = true;
      if (timer) clearTimeout(timer);
    },
    { once: true },
  );
  return async () => {
    stopped = true;
    lifecycleHost = undefined;
    if (timer) clearTimeout(timer);
    await running;
  };
}
async function advance(
  host: RuntimeHost,
  session: string,
  task: Task,
  signal: AbortSignal,
) {
  host = tracedHost(host);
  let prompt = "Durable sleep finished. Continue the pending task.",
    outcome = "completed";
  let waitStatus: "decided" | "expired" | "canceled" = "decided";
  let childOutcome: Task["outcome"];
  if (task.kind === "wait" || task.kind === "approval") {
    const record = await durableWait(host, "").inspect(session, task.id);
    if (record.status === "pending") return;
    if (record.status === "resolved") {
      await mutate(host, session, (doc) => {
        const t = doc.tasks.find((t) => t.id === task.id);
        if (t) {
          t.status = "completed";
          t.phase = "complete";
          t.delivered = true;
          if (t.wait) t.wait.status = "decided";
        }
      });
      return;
    }
    if (record.status === "expired") waitStatus = "expired";
    if (record.status === "fenced") waitStatus = "canceled";
    outcome = record.status === "approved" ? "completed" : "failed";
    prompt = `Durable wait ${task.id} is ${record.status}. Continue only according to the host decision.`;
  }
  if (task.kind === "sleep" && Date.parse(task.wake_at!) > Date.now()) return;
  if (task.kind === "subagent") {
    if (!task.child_session_id) {
      const list = await host.call(
        "host.session.list",
        { parent_session_id: session, scope: "owned" },
        { signal },
      );
      const child =
        list.sessions.find((s) => s.creation_request_id === task.request_id) ??
        (await host.call(
          "host.session.create",
          {
            request_id: task.request_id!,
            parent_session_id: session,
            visibility: "plugin",
            context_source: task.context_source ?? "fresh",
            fork_seq:
              task.context_source === "fork" ? task.fork_point : undefined,
            workspace: "shared",
            name: "Durable subagent",
          },
          { signal },
        ));
      task.child_session_id = child.session_id;
      await mutate(host, session, (doc) => {
        const t = doc.tasks.find((t) => t.id === task.id);
        if (t) t.child_session_id = child.session_id;
      });
    }
    const childDoc = (await readSession(host, task.child_session_id)).doc;
    const logical = task.child_run_id
      ? childDoc.tasks.find((t) => t.id === task.child_run_id)
      : childDoc.tasks
          .filter((t) => t.kind === "run")
          .sort((a, b) => a.created_at.localeCompare(b.created_at))[0];
    if (logical && !task.child_run_id) {
      task.child_run_id = logical.id;
      await mutate(host, session, (doc) => {
        const t = doc.tasks.find((t) => t.id === task.id);
        if (t) t.child_run_id = logical.id;
      });
    }
    if (logical && !["completed", "failed", "aborted"].includes(logical.status))
      return;
    const child = await host.call(
      "host.session.inspect",
      {
        session_id: task.child_session_id,
        ...(logical
          ? { turn_id: logical.turn_ref }
          : { request_id: `${task.id}:input` }),
        wait: "none",
      },
      { signal },
    );
    if (!child.turn) {
      const history = await host.call(
        "host.session.history.read",
        {
          session_id: session,
          start_seq: task.intent_seq,
          end_seq: task.intent_seq,
          limit: 1,
        },
        { signal },
      );
      let original: string | undefined;
      for (const record of history.records ?? []) {
        if (!record.tool_calls) continue;
        const calls = JSON.parse(record.tool_calls) as {
          id?: string;
          arguments?: string;
          function?: { arguments?: string };
        }[];
        if (
          record.seq !== task.intent_seq ||
          digest(calls) !== task.intent_digest
        )
          throw fault("subagent_intent_digest_mismatch");
        const call = calls.find((c) => c.id === task.source_call_id);
        if (call?.arguments || call?.function?.arguments) {
          const args = JSON.parse(call.arguments ?? call.function!.arguments!);
          if (typeof args.prompt === "string") original = args.prompt;
        }
      }
      if (!original) throw fault("subagent_input_reference_missing");
      await host.call(
        "host.session.send",
        {
          session_id: task.child_session_id,
          request_id: `${task.id}:input`,
          input: { prompt: original },
        },
        { signal },
      );
      return;
    }
    if (
      ![
        "succeeded",
        "completed",
        "failed",
        "canceled",
        "cancelled",
        "aborted",
        "interrupted",
        "discarded",
      ].includes(child.turn.state)
    )
      return;
    // A live turn may report completed before its durable lifecycle (and
    // final output) is committed. Never deliver that transient empty snapshot.
    if (!child.turn.completed_at) return;
    outcome = logical?.status ?? child.turn.state;
    // Results live only in host history; the task graph keeps receipt references.
    childOutcome = logical?.outcome;
    prompt =
      childOutcome?.status === "unanswered"
        ? `Subagent ${task.child_session_id} finished (unanswered: ${childOutcome.reason}).`
        : `Subagent ${task.child_session_id} finished (${outcome}).\n${child.turn.final_output ?? ""}`;
  }
  const fresh = (await readSession(host, session)).doc;
  const live = fresh.tasks.find((t) => t.id === task.id);
  if (!live || live.canceled || live.delivered) return;
  signal.throwIfAborted();
  await mutate(host, session, (doc) => {
    const t = doc.tasks.find((t) => t.id === task.id);
    if (!t || t.canceled) throw fault("task_canceled");
    t.dispatching = true;
  });
  if (!live.delivery_accepted) {
    const receipt = await host.call(
      "host.session.send",
      {
        session_id: session,
        request_id: `${task.id}:result`,
        input: { prompt },
        if_running: "queue",
        cause: `durable.${task.kind}:${task.id}`,
        presentation: {
          kind: "background_event",
          text: "",
          related_session_id: task.child_session_id,
        },
      },
      { signal },
    );
    await mutate(host, session, (doc) => {
      const t = doc.tasks.find((t) => t.id === task.id);
      if (t) {
        t.delivery_accepted = true;
        t.receipt_id = receipt.receipt?.receipt_id;
      }
    });
  }
  const delivery = await host.call(
    "host.session.inspect",
    {
      session_id: session,
      request_id: `${task.id}:result`,
      wait: "none",
    },
    { signal },
  );
  // Admission is not model consumption. Keep the stable delivery intent until
  // the host reports read_at, or a durable terminal lifecycle on legacy hosts.
  const consumed = Boolean(
    delivery.receipt?.read_at || delivery.turn?.completed_at,
  );
  await mutate(host, session, (doc) => {
    const t = doc.tasks.find((t) => t.id === task.id);
    if (!t) return;
    if (!consumed) return;
    t.delivered = true;
    if (childOutcome) t.outcome = childOutcome;
    const succeeded = ["completed", "succeeded"].includes(outcome);
    t.error_code = succeeded ? undefined : "child_or_wait_failed";
    if (succeeded) t.error_message = undefined;
    t.status = [
      "failed",
      "canceled",
      "cancelled",
      "aborted",
      "interrupted",
      "discarded",
    ].includes(outcome)
      ? "failed"
      : "completed";
    t.phase = t.status === "completed" ? "complete" : "failed";
    t.updated_at = new Date().toISOString();
    t.duration_ms = Date.parse(t.updated_at) - Date.parse(t.created_at);
    if (t.wait) t.wait.status = waitStatus;
  });
}
