import {
  durableWait,
  documentStore,
  type RuntimeHost,
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
const cancellations = new Map<string, { session: string; run: string }>();
export async function requestCancellation(session: string, run: string) {
  if (lifecycleSignal?.aborted || !lifecycleHost)
    throw fault("generation_closed");
  const key = `${session}:${run}`;
  cancellations.set(key, { session, run });
  const host = lifecycleHost;
  const job = (await host.call("host.service.call", {
    service: "host.background",
    method: "start",
    params: {
      key: `cancel-${run}`,
      service: "durable.scheduler",
      method: "cancel",
      params: { session_id: session, run_id: run },
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
      cancellations.delete(key);
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw fault("cancellation_pending");
}
export async function cancelTasks(
  host: RuntimeHost,
  session: string,
  run?: string,
) {
  return serialize(session, () => cancelTasksLocked(host, session, run));
}
async function cancelTasksLocked(
  host: RuntimeHost,
  session: string,
  run?: string,
) {
  await mutate(host, session, (doc) => {
    if (!run) doc.canceled = true;
    for (const task of doc.tasks) {
      if (run && task.run_id !== run) continue;
      if (task.kind === "subagent" && task.independent) continue;
      if (["completed", "failed"].includes(task.status)) continue;
      if (task.status !== "aborted") task.updated_at = new Date().toISOString();
      task.canceled = true;
      task.status = "aborted";
      task.phase = "aborted";
      task.error_code ??= "task_canceled";
      task.duration_ms =
        Date.parse(task.updated_at) - Date.parse(task.created_at);
      if (task.kind === "run" || task.kind === "model_round")
        task.usage_status = task.usage ? "incomplete" : "unavailable";
      if (task.wait) task.wait.status = "canceled";
    }
  });
  // Cancellation is a persistent outbox. Failure leaves the tombstone and a
  // retryable cleanup item; it never restores the task to runnable status.
  await settleCancellations(host, session);
}
async function settleCancellations(host: RuntimeHost, session: string) {
  const { doc } = await readSession(host, session);
  for (const t of doc.tasks.filter((t) => t.canceled && !t.cancel_done)) {
    if (t.kind === "subagent" && !t.independent) {
      let child = t.child_session_id;
      if (!child) {
        const created = await host.call("host.session.create", {
          request_id: t.request_id!,
          parent_session_id: session,
          visibility: "plugin",
          context_source: "fresh",
          workspace: "shared",
          name: "Durable subagent",
        });
        child = created.session_id;
      }
      await host.call("host.session.cancel", { session_id: child });
    }
    if (t.kind === "approval" || t.kind === "wait") {
      const wait = durableWait(host, "");
      const record = await wait.inspect(session, t.id);
      // Unknown effects cannot be fenced as not-started. The plugin cancellation
      // tombstone still prohibits delivery/replay; host effect evidence is retained.
      if (record.effect_state === "not_started")
        await wait.fence(session, t.id);
    }
    await mutate(host, session, (next) => {
      const task = next.tasks.find((task) => task.id === t.id);
      if (task) task.cancel_done = true;
    });
  }
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
      const task = newTask(s, "subagent", id);
      task.request_id = `${id}:create`;
      task.background = args.background === true;
      task.independent = task.background && args.independent === true;
      task.depth = depth + 1;
      task.fork_point = input.source_message?.seq;
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
        context_source: "fresh",
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
    );
    return { canceled: true };
  }
  if (input.tool_id === "durable_context") {
    if (!["compact", "reset", "handoff"].includes(String(args.action)))
      throw fault("invalid_context_action");
    await mutate(host, s.session_id, (doc) => {
      const cp = doc.checkpoints[s.run_id];
      if (!cp) throw fault("checkpoint_missing");
      cp.state.context_intent = args.action as State["context_intent"];
    });
    return { scheduled: true };
  }
  throw fault("unknown_durable_control");
}

let cursor: string | undefined;
/** Each tick is a finite host.background job. Publication revokes the old
 * generation's admission before the successor scheduler can start. */
export async function schedulerTick(host: RuntimeHost, signal: AbortSignal) {
  signal.throwIfAborted();
  for (const [key, item] of cancellations) {
    await cancelTasks(host, item.session, item.run);
    cancellations.delete(key);
  }
  const view = await documentStore(host).read<SessionDocument>({
    namespace: NAMESPACE,
    start: cursor,
    limit: 16,
  });
  cursor = view.next_cursor ? `${view.next_cursor}\0` : undefined;
  for (const { value: doc } of view.documents) {
    signal.throwIfAborted();
    if (doc.schema_version !== 1) throw fault("incompatible_document");
    if (doc.tasks.some((t) => t.canceled && !t.cancel_done))
      await serialize(doc.session_id, () =>
        settleCancellations(host, doc.session_id),
      );
    if (doc.canceled) continue;
    for (const task of doc.tasks.filter(
      (t) =>
        ["sleep", "subagent", "approval", "wait"].includes(t.kind) &&
        !t.canceled &&
        !t.delivered &&
        t.status === "waiting",
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
            context_source: "fresh",
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
    outcome = child.turn.state;
    // Results live only in host history; the task graph keeps receipt references.
    prompt = `Subagent ${task.child_session_id} finished (${outcome}).\n${child.turn.final_output ?? ""}`;
  }
  const fresh = (await readSession(host, session)).doc;
  const live = fresh.tasks.find((t) => t.id === task.id);
  if (fresh.canceled || !live || live.canceled || live.delivered) return;
  signal.throwIfAborted();
  await mutate(host, session, (doc) => {
    const t = doc.tasks.find((t) => t.id === task.id);
    if (!t || t.canceled || doc.canceled) throw fault("task_canceled");
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
    t.error_code = outcome === "completed" ? undefined : "child_or_wait_failed";
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
