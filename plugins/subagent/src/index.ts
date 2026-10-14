import { createHash, randomBytes } from "node:crypto";
import {
  requireKernelService, acquireFileLock, releaseFileLock, FILE_LOCK_SERVICE,
  type AgentTurnInterruptedInput,
  type CapabilityInvokeParams,
  type HostServiceContracts,
  type JSONSchemaObject,
  type KernelHostServiceMethod,
  type RuntimeExecutionContext,
  type RuntimeHost,
  type RuntimeInitializeParams,
  type RuntimeLifecycleContext,
  type RuntimePlugin,
  type ToolExecuteParams,
  type ToolExecuteResult,
  type TurnLifecycleInput,
} from "@wuu/plugin-sdk";

const promptCapability = "agent.system_prompt.section";
const clientCapability = "plugin.client.request";
const lifecycleCapability = "agent.turn.lifecycle";
const interruptCapability = "agent.turn.interrupted";
const taskIndexKey = "tasks.v3";
const legacyTaskIndexKey = "tasks.v2";
const maxTaskIndexBytes = 1 << 20;
const maxTaskRecords = 128;
const foregroundAwaitBudgetMS = 10 * 60 * 1000;

type TaskRecord = {
  session_id: string;
  parent_session_id: string;
  parent_turn_id?: string;
  name: string;
  request_id: string;
  turn_id?: string;
  queue_id?: string;
  state: string;
  suppress_completion?: boolean;
  legacy?: boolean;
};
type SessionSendResult = HostServiceContracts["host.session.send"]["result"];
type TaskDispatch = {
  id: string;
  signature: string;
  record: TaskRecord;
  create?: SessionCreateParams;
  prompt?: string;
  settled?: boolean;
  previous_request_id?: string;
  cancelled?: boolean;
  result?: { record: TaskRecord; sent: SessionSendResult };
};
type TaskIndex = { records: TaskRecord[]; dispatches: TaskDispatch[] };
type SpawnArguments = {
  description: string;
  prompt: string;
  subagent_type?: string;
  name?: string;
  agent_profile?: string;
  model?: string;
  context?: string;
  isolation?: string;
  run_in_background?: boolean;
};
type SessionCreateParams = HostServiceContracts["host.session.create"]["params"];

const stringField = (description: string) => ({ type: "string", description });
const objectSchema = (properties: Record<string, unknown>, ...required: string[]): JSONSchemaObject => ({ type: "object", properties, required });
const textResult = (value: unknown): ToolExecuteResult => ({ result: { content: [{ type: "text", text: JSON.stringify(value) }] } });
const terminalState = (state: string): boolean => ["completed", "failed", "interrupted", "discarded"].includes(state.trim());

class SubagentPlugin implements RuntimePlugin {
  readonly concurrentCapabilities = [promptCapability, clientCapability];
  private lifecycleHost!: RuntimeHost;
  private indexQueue: Promise<unknown> = Promise.resolve();
  private transitionQueue: Promise<unknown> = Promise.resolve();

  initialize(_params: RuntimeInitializeParams, host: RuntimeHost) {
    this.lifecycleHost = host;
    const services: KernelHostServiceMethod[] = [
      "host.session.create", "host.session.send", "host.session.list", "host.session.cancel", "host.session.inspect", "host.session.ready",
      "host.storage.get", "host.storage.set", "host.storage.keys",
    ];
    return {
      protocol_version: 3 as const,
      tools: [
        {
          id: "spawn_agent", direct_only: true,
          description: "Delegate a bounded task when separate context materially improves the result. Set run_in_background=false when the next step depends on the child result; the tool waits in the current turn and returns the result directly. Background tasks return immediately and deliver completion later as a normal read-only query bubble.",
          input_schema: objectSchema({
            description: stringField("Short 3-5 word summary of what the agent will do."),
            prompt: stringField("Concrete, self-contained task brief with scope, constraints, acceptance criteria, and deliverable."),
            subagent_type: stringField("Optional specialized agent type."),
            name: stringField("Optional addressable task name using lowercase letters, digits, and underscores."),
            model: stringField("Optional configured model alias or host capability model such as @verification."),
            context: { type: "string", enum: ["fresh", "fork"], description: "Optional conversation context source. Defaults to fresh; fork inherits the parent conversation." },
            isolation: { type: "string", enum: ["worktree"] },
            run_in_background: { type: "boolean", default: true, description: "Return immediately when true. Set false when the parent must wait for this result before continuing; a foreground wait automatically becomes background after ten minutes." },
          }, "description", "prompt"),
          execution_scopes: ["root" as const], activity: { concurrency_safe: true },
        },
        {
          id: "send_message", direct_only: true,
          description: "Send a follow-up turn to an existing child task by session id or task name.",
          input_schema: objectSchema({ target: stringField("Child session id or task name."), message: stringField("Message to deliver.") }, "target", "message"),
          execution_scopes: ["root" as const], activity: { concurrency_safe: true },
        },
        {
          id: "close_agent", direct_only: true,
          description: "Cancel an existing child task by session id or task name.",
          input_schema: objectSchema({ target: stringField("Child session id or task name.") }, "target"),
          execution_scopes: ["root" as const], activity: { concurrency_safe: true },
        },
      ],
      capabilities: [
        { id: promptCapability, kind: "transform" as const, version: 1 },
        { id: clientCapability, kind: "decision" as const, version: 1 },
        { id: lifecycleCapability, kind: "observe" as const, version: 1, error_policy: "isolate" as const },
        { id: interruptCapability, kind: "observe" as const, version: 1, error_policy: "isolate" as const },
      ],
      required_services: [...services.map(requireKernelService), { name: FILE_LOCK_SERVICE, major_version: 1, required: true }],
    };
  }

  async executeTool(call: ToolExecuteParams, host: RuntimeHost, execution: RuntimeExecutionContext): Promise<ToolExecuteResult> {
    switch (call.tool_id) {
      case "spawn_agent": return this.spawnAgent(host, call, execution);
      case "send_message": return this.withTransition(execution, () => this.sendMessage(host, call));
      case "close_agent": return this.withTransition(execution, () => this.closeAgent(host, call));
      default: throw new Error(`unknown tool ${JSON.stringify(call.tool_id)}`);
    }
  }

  private async spawnAgent(host: RuntimeHost, call: ToolExecuteParams, execution: RuntimeExecutionContext): Promise<ToolExecuteResult> {
    const args = call.arguments as SpawnArguments;
    if (!args.description?.trim() || !args.prompt?.trim()) throw new Error("spawn_agent requires description and prompt");
    if (args.agent_profile?.trim()) throw new Error("agent_profile is no longer part of the Subagent plugin contract; put required memory in the task prompt");
    if (!call.session_id?.trim()) throw new Error("spawn_agent requires a parent session");
    const parentSessionID = call.session_id;
    const background = args.run_in_background ?? true;
    const name = args.name?.trim() || deriveTaskName(args.description);
    const requestID = dispatchIdentity(call);
    let context: "fresh" | "fork" = "fresh";
    switch (args.context?.trim() ?? "") {
      case "": case "fresh": break;
      case "fork": context = "fork"; break;
      default: throw new Error(`unsupported subagent context ${JSON.stringify(args.context)}`);
    }
    const workspace = args.isolation?.trim() === "worktree" ? "worktree" : "shared";
    if (workspace === "worktree") context = "fork";
    const { record, sent } = await this.withTransition(execution, async () => {
      const dispatch = await this.prepareDispatch(host, {
        id: requestID,
        signature: dispatchSignature({ name, prompt: args.prompt.trim(), context, workspace, model: args.model?.trim(), type: args.subagent_type?.trim() }),
        create: {
          request_id: `create-${requestID}`, name, visibility: "plugin", parent_session_id: parentSessionID,
          context_source: context, workspace, model_alias: args.model?.trim(), instructions: workerInstructions(args.subagent_type?.trim()),
        },
        record: {
          session_id: "", parent_session_id: parentSessionID, parent_turn_id: call.turn_id,
          name, request_id: `turn-${requestID}`, state: "created",
        },
        prompt: args.prompt.trim(),
      });
      return this.dispatchTask(host, dispatch);
    });
    const identity = { session_id: record.session_id, task_name: name, run_in_background: background };
    if (background) return textResult({ ...identity, state: sent.state, backgrounded: true });

    const inspected = await host.call("host.session.inspect", {
      session_id: record.session_id, request_id: record.request_id, wait: "terminal", timeout_ms: foregroundAwaitBudgetMS,
    });
    if (inspected.timed_out || !inspected.turn || !terminalState(inspected.turn.state)) {
      return textResult({ ...identity, state: inspected.turn?.state?.trim() ? inspected.turn.state : sent.state,
        backgrounded: true, ...(inspected.timed_out ? { timed_out: true } : {}) });
    }
    // A follow-up may replace this task while the foreground call waits.
    // Suppress only the request whose terminal result is returned here.
    const completed = inspected.turn;
    await this.withTransition(execution, async () => {
      const current = await this.loadRecord(host, "session_id", record.session_id);
      if (!current || current.request_id !== record.request_id) return;
      current.state = completed.state;
      current.turn_id = completed.turn_id;
      current.queue_id = completed.queue_id;
      current.suppress_completion = true;
      await this.saveRecord(host, current);
    });
    return textResult({ ...identity, state: inspected.turn.state, backgrounded: false,
      ...(inspected.turn.final_output ? { final_output: inspected.turn.final_output } : {}),
      ...(inspected.turn.error ? { error: inspected.turn.error } : {}),
    });
  }

  private async sendMessage(host: RuntimeHost, call: ToolExecuteParams): Promise<ToolExecuteResult> {
    const args = call.arguments as { target: string; message: string };
    const prompt = args.message?.trim() ?? "";
    if (!prompt) throw new Error("send_message requires a message");
    const previous = await this.resolveTask(host, call.session_id ?? "", args.target);
    const requestID = dispatchIdentity(call);
    const dispatch = await this.prepareDispatch(host, {
      id: requestID, signature: dispatchSignature({ session_id: previous.session_id, prompt }), prompt,
      previous_request_id: previous.request_id,
      record: {
        ...previous, request_id: `turn-${requestID}`, parent_session_id: call.session_id ?? "",
        parent_turn_id: call.turn_id, suppress_completion: false, legacy: undefined, state: "created",
        turn_id: undefined, queue_id: undefined,
      },
    });
    const { record, sent } = await this.dispatchTask(host, dispatch);
    return textResult({ session_id: record.session_id, state: sent.state, steered: sent.steered ?? false });
  }

  activate(host: RuntimeHost, lifecycle: RuntimeLifecycleContext): void {
    // Runtime construction precedes app-server binding. Do not block activation
    // or acquire the dispatch lock until Session services can accept recovery.
    void this.recoverDispatches(host, lifecycle).catch((error: unknown) => {
      if (!lifecycle.signal.aborted) console.error("subagent recovery:", error);
    });
  }

  private async recoverDispatches(host: RuntimeHost, lifecycle: RuntimeLifecycleContext): Promise<void> {
    await host.call("host.session.ready", {}, { signal: lifecycle.signal });
    const execution = { executionId: "", signal: lifecycle.signal };
    const ids = await this.withTransition(execution, async () =>
      (await this.readIndex(host)).dispatches.filter((item) => !item.result && !item.cancelled).map((item) => item.id));
    for (const id of ids) {
      lifecycle.signal.throwIfAborted();
      await this.withTransition(execution, async () => {
        // A retained generation may have accepted or cancelled this intent
        // while recovery waited for the cross-process transition lock.
        const dispatch = (await this.readIndex(host)).dispatches.find((item) => item.id === id);
        if (!dispatch || dispatch.result || dispatch.cancelled) return;
        try { await this.dispatchTask(host, dispatch); }
        catch { /* Preserve uncertain intent for an explicit, same-identity retry. */ }
      });
    }
  }

  private prepareDispatch(host: RuntimeHost, dispatch: TaskDispatch): Promise<TaskDispatch> {
    return this.withIndex(async () => {
      const index = await this.readIndex(host);
      const prior = index.dispatches.find((item) => item.id === dispatch.id);
      if (prior) {
        if (prior.signature !== dispatch.signature) throw new Error("subagent tool call identity was reused with different input");
        if (prior.cancelled) throw new Error("subagent dispatch was cancelled");
        return prior;
      }
      const pending = index.dispatches.find((item) => !item.result && !item.cancelled &&
        item.record.parent_session_id === dispatch.record.parent_session_id &&
        (dispatch.create ? item.create && item.signature === dispatch.signature && item.record.parent_turn_id === dispatch.record.parent_turn_id
          : item.record.session_id === dispatch.record.session_id));
      if (pending) {
        if (pending.signature !== dispatch.signature) throw new Error("the previous child dispatch is unconfirmed; retry it before sending a different message");
        return pending;
      }
      // Retain recent replay receipts within both count and byte bounds. Never
      // evict live/unconfirmed work to admit a new operation.
      compactDispatches(index);
      while (index.dispatches.length >= maxTaskRecords || reservedIndexBytes({ ...index, dispatches: [...index.dispatches, dispatch] }) > maxTaskIndexBytes) {
        const settled = index.dispatches.findIndex((item) => item.cancelled || item.settled);
        if (settled < 0) throw new Error("subagent dispatch storage capacity is full; shorten this task brief or resolve pending tasks before creating more");
        index.dispatches.splice(settled, 1);
      }
      index.dispatches.push(dispatch);
      await this.writeIndex(host, index);
      return dispatch;
    });
  }

  private async dispatchTask(host: RuntimeHost, dispatch: TaskDispatch): Promise<{ record: TaskRecord; sent: SessionSendResult }> {
    if (dispatch.cancelled) throw new Error("subagent dispatch was cancelled");
    if (dispatch.result) return dispatch.result;
    if (!dispatch.prompt) throw new Error("subagent dispatch has no recoverable input");
    const record = dispatch.create
      ? await this.createTask(host, dispatch.create, { ...dispatch.record }, dispatch.id)
      : { ...dispatch.record };
    const sent = await host.call("host.session.send", {
      request_id: record.request_id, session_id: record.session_id, input: { prompt: dispatch.prompt },
      presentation: { kind: "query_bubble", text: dispatch.create ? `子任务 ${record.name} 已开始` : "父任务已更新要求", name: record.name },
      cause: dispatch.create ? "subagent.task" : "subagent.followup",
      ...(dispatch.create ? {} : { if_running: "steer" as const }),
    });
    if (sent.steered) record.request_id = dispatch.previous_request_id ?? record.request_id;
    record.state = sent.state;
    record.turn_id = sent.turn_id;
    record.queue_id = sent.queue_id;
    const result = { record, sent };
    await this.withIndex(async () => {
      const index = await this.readIndex(host);
      const current = index.dispatches.find((item) => item.id === dispatch.id);
      if (!current || current.cancelled) throw new Error("subagent dispatch was cancelled");
      current.result = result;
      index.records = index.records.filter((item) => item.session_id !== record.session_id);
      index.records.push(record);
      trimIndex(index, maxTaskRecords);
      await this.writeIndex(host, index);
    });
    return result;
  }

  private cancelDispatches(host: RuntimeHost, matches: (record: TaskRecord) => boolean): Promise<void> {
    return this.withIndex(async () => {
      const index = await this.readIndex(host);
      for (const dispatch of index.dispatches) {
        if (matches(dispatch.record)) dispatch.cancelled = true;
      }
      await this.writeIndex(host, index);
    });
  }

  private async closeAgent(host: RuntimeHost, call: ToolExecuteParams): Promise<ToolExecuteResult> {
    const { target } = call.arguments as { target: string };
    const parentID = call.session_id?.trim() ?? "";
    const uncreated = await this.withIndex(async () => (await this.readIndex(host)).dispatches.filter((dispatch) =>
      !dispatch.result && !dispatch.cancelled && !dispatch.record.session_id &&
      dispatch.record.parent_session_id === parentID && dispatch.record.name === target?.trim()));
    if (uncreated.length > 0) {
      if (uncreated.length > 1) throw new Error(`task name ${JSON.stringify(target)} is ambiguous`);
      const dispatch = uncreated[0];
      // Recover the idempotent create receipt if the host accepted creation
      // before its reply was lost. A rejected create has no execution to stop.
      if (dispatch.create) {
        try { await this.createTask(host, dispatch.create, { ...dispatch.record }, dispatch.id); }
        catch {
          await this.cancelDispatches(host, (task) => task.request_id === dispatch.record.request_id);
          return textResult({ task_name: dispatch.record.name, cancelled: true });
        }
      }
    }
    const record = await this.resolveTask(host, parentID, target);
    await this.cancelDispatches(host, (task) => task.session_id === record.session_id || task.request_id === record.request_id);
    // Persist dismissal before cancellation can produce an interrupted event.
    record.suppress_completion = true;
    await this.saveRecord(host, record);
    const result = await host.call("host.session.cancel", { session_id: record.session_id, turn_id: record.turn_id, queue_id: record.queue_id });
    return textResult({ session_id: result.session_id, cancelled: result.cancelled });
  }

  async invokeCapability(call: CapabilityInvokeParams, host: RuntimeHost, execution: RuntimeExecutionContext) {
    switch (call.capability) {
      case promptCapability: return { output: { text: promptSection } };
      case clientCapability: {
        const request = call.input as { method: string; input?: HostServiceContracts["host.session.list"]["params"] };
        if (request.method !== "status.list") throw new Error(`unknown client method ${JSON.stringify(request.method)}`);
        return { output: { result: await host.call("host.session.list", request.input ?? {}) } };
      }
      case lifecycleCapability: await this.withTransition(execution, () => this.onLifecycle(host, call.input as TurnLifecycleInput)); break;
      case interruptCapability: await this.withTransition(execution, () => this.onInterrupted(host, call.input as AgentTurnInterruptedInput)); break;
      default: throw new Error(`unknown capability ${JSON.stringify(call.capability)}`);
    }
    return { output: {} };
  }

  private async onLifecycle(host: RuntimeHost, input: TurnLifecycleInput): Promise<void> {
    if (!terminalState(input.state)) return;
    const pending = await this.withIndex(async () => {
      const index = await this.readIndex(host);
      const dispatch = index.dispatches.find((item) => !item.cancelled && !item.settled && item.record.request_id === input.request_id &&
        (!item.result || input.retryable));
      if (dispatch && input.retryable) {
        dispatch.result = undefined;
        await this.writeIndex(host, index);
      }
      return dispatch;
    });
    // Settle lost acknowledgements before delivering results. Only the host's
    // explicit shutdown-discard receipt permits replaying an accepted input.
    if (pending) {
      await this.dispatchTask(host, pending);
      if (input.retryable) return;
    }
    // Receipt settlement follows the request, even after a queued follow-up
    // replaces its visible task record. Do not restore the old delivery route.
    await this.withIndex(async () => {
      const index = await this.readIndex(host);
      for (const dispatch of index.dispatches) {
        if (dispatch.result?.record.request_id === input.request_id) dispatch.settled = true;
      }
      await this.writeIndex(host, index);
    });
    let record = await this.loadRecord(host, "request_id", input.request_id);
    if (!record) {
      await this.reconcileOwnedSessions(host);
      record = await this.loadRecord(host, "request_id", input.request_id);
    }
    if (!record) return;
    record.state = input.state;
    await this.saveRecord(host, record);
    if (record.suppress_completion) return this.deleteRecord(host, record.session_id);
    const output = input.final_output?.trim() || input.error?.trim() || "子任务已结束，但没有返回文本结果。";
    const deliveryID = `deliver-${createHash("sha256").update(`subagent.completion\0${input.request_id.trim()}`).digest("hex").slice(0, 24)}`;
    const parentID = record.parent_session_id.trim();
    const parent = parentID ? await this.loadRecord(host, "session_id", parentID) : undefined;
    const continuation = parent?.parent_session_id?.trim() ? {
      session_id: parentID, parent_session_id: parent.parent_session_id, name: parent.name,
      request_id: deliveryID, state: "created",
    } satisfies TaskRecord : undefined;
    if (continuation) await this.saveRecord(host, continuation);
    let sent: HostServiceContracts["host.session.send"]["result"];
    try {
      sent = await host.call("host.session.send", {
        request_id: deliveryID, session_id: record.parent_session_id, reply_to_turn_id: record.parent_turn_id,
        input: { prompt: `子任务 ${record.name} 已${lifecycleLabel(input.state)}。请检查并整合以下交接结果：\n\n${output}` },
        presentation: { kind: "query_bubble", text: `子任务 ${record.name} 已更新`, name: record.name, related_session_id: record.session_id },
        cause: "subagent.completion", if_running: "steer",
      });
    } catch (error) {
      if (continuation && parent) {
        try { await this.saveRecord(host, parent); } catch (restoreError) {
          throw new AggregateError([error, restoreError], `${String(error)}; restoring parent task: ${String(restoreError)}`);
        }
      }
      throw error;
    }
    if (continuation && parent) {
      // A steer belongs to the existing parent request, which routes the
      // combined result upward rather than creating a second continuation.
      if (sent.steered) await this.saveRecord(host, parent);
      else { continuation.state = sent.state; await this.saveRecord(host, continuation); }
    }
    await this.deleteRecord(host, record.session_id);
  }

  private async onInterrupted(host: RuntimeHost, input: AgentTurnInterruptedInput): Promise<void> {
    if (!input.thread_id?.trim() || !input.turn_id?.trim()) return;
    await this.cancelDispatches(host, (task) => task.parent_session_id === input.thread_id && task.parent_turn_id === input.turn_id);
    const listed = await host.call("host.session.list", { parent_session_id: input.thread_id });
    for (const child of listed.sessions ?? []) {
      const record = await this.loadRecord(host, "session_id", child.session_id);
      if (!record || record.parent_turn_id?.trim() !== input.turn_id.trim()) continue;
      record.suppress_completion = true;
      await this.saveRecord(host, record);
      const result = await host.call("host.session.cancel", { session_id: record.session_id, turn_id: record.turn_id, queue_id: record.queue_id });
      if (result.cancelled) { record.state = "interrupted"; await this.saveRecord(host, record); }
    }
  }

  private async resolveTask(host: RuntimeHost, parentID: string, target: string): Promise<TaskRecord> {
    target = target?.trim() ?? "";
    const listed = await host.call("host.session.list", { parent_session_id: parentID.trim() });
    const matches = (listed.sessions ?? []).filter((item) => item.session_id === target || item.name === target);
    if (matches.length > 1) throw new Error(`task name ${JSON.stringify(target)} is ambiguous; use the session id`);
    const match = matches[0];
    if (!match) throw new Error(`child session ${JSON.stringify(target)} not found`);
    const record = await this.loadRecord(host, "session_id", match.session_id);
    if (record) return record;
    const inspected = await host.call("host.session.inspect", { session_id: match.session_id, wait: "none" });
    const recovered: TaskRecord = {
      session_id: match.session_id, parent_session_id: match.parent_session_id ?? "", name: match.name ?? "",
      request_id: inspected.turn?.request_id ?? "", turn_id: inspected.turn?.turn_id,
      queue_id: inspected.turn?.queue_id, state: inspected.turn?.state ?? match.state,
    };
    if (recovered.request_id.trim()) await this.saveRecord(host, recovered);
    return recovered;
  }

  private async reconcileOwnedSessions(host: RuntimeHost): Promise<void> {
    const listed = await host.call("host.session.list", { scope: "owned" });
    for (const child of listed.sessions ?? []) {
      if (await this.loadRecord(host, "session_id", child.session_id)) continue;
      const { turn } = await host.call("host.session.inspect", { session_id: child.session_id, wait: "none" });
      // Terminal cache entries stay removed once delivery is acknowledged.
      if (!turn || terminalState(turn.state) || !turn.request_id?.trim()) continue;
      await this.saveRecord(host, {
        session_id: child.session_id, parent_session_id: child.parent_session_id ?? "", name: child.name ?? "",
        request_id: turn.request_id, turn_id: turn.turn_id, queue_id: turn.queue_id, state: turn.state,
      });
    }
  }

  // Serialize state transitions, leaving foreground waits outside the lock.
  private withTransition<T>(execution: RuntimeExecutionContext, operation: () => Promise<T>): Promise<T> {
    const next = this.transitionQueue.then(async () => {
      execution.signal.throwIfAborted();
      const lock = await acquireFileLock(this.lifecycleHost, { root: "workspace_state", path: "subagent/dispatch.lock" }, execution.executionId || undefined);
      try {
        execution.signal.throwIfAborted();
        return await operation();
      } finally {
        await releaseFileLock(this.lifecycleHost, lock.lock_id);
      }
    });
    this.transitionQueue = next.catch(() => undefined);
    return next;
  }

  private withIndex<T>(operation: () => Promise<T>): Promise<T> {
    const next = this.indexQueue.then(operation);
    this.indexQueue = next.catch(() => undefined);
    return next;
  }

  private createTask(host: RuntimeHost, params: SessionCreateParams, record: TaskRecord, dispatchID: string): Promise<TaskRecord> {
    // Creation and reservation share a lock; concurrent requests cannot both
    // consume the last free slot before either record is registered.
    return this.withIndex(async () => {
      const index = await this.readIndex(host);
      const existing = index.records.find((item) => item.request_id === record.request_id);
      if (existing) return { ...record, session_id: existing.session_id };
      if (index.records.length >= maxTaskRecords) await this.refreshIndex(host, index);
      trimIndex(index, maxTaskRecords - 1);
      const created = await host.call("host.session.create", params);
      record.session_id = created.session_id;
      const dispatch = index.dispatches.find((item) => item.id === dispatchID);
      if (dispatch) dispatch.record.session_id = created.session_id;
      index.records = index.records.filter((item) => item.session_id !== record.session_id);
      index.records.push(record);
      await this.writeIndex(host, index);
      return record;
    });
  }

  private async refreshIndex(host: RuntimeHost, index: TaskIndex): Promise<void> {
    const listed = await host.call("host.session.list", { scope: "owned" });
    const owned = new Set((listed.sessions ?? []).map((child) => child.session_id));
    const kept: TaskRecord[] = [];
    for (const record of index.records) {
      // A created record may still be between registration and send.
      if (record.state !== "created" && !terminalState(record.state)) {
        if (!owned.has(record.session_id)) continue;
        const inspected = await host.call("host.session.inspect", { session_id: record.session_id, request_id: record.request_id, wait: "none" });
        if (inspected.turn) record.state = inspected.turn.state;
        else if (terminalState(inspected.session.state)) continue;
      }
      kept.push(record);
    }
    index.records = kept;
  }

  private saveRecord(host: RuntimeHost, record: TaskRecord): Promise<void> {
    return this.withIndex(async () => {
      const index = await this.readIndex(host);
      index.records = index.records.filter((existing) => existing.session_id !== record.session_id && (!record.request_id || existing.request_id !== record.request_id));
      index.records.push(record);
      trimIndex(index, maxTaskRecords);
      await this.writeIndex(host, index);
    });
  }

  private loadRecord(host: RuntimeHost, key: "session_id" | "request_id", value: string): Promise<TaskRecord | undefined> {
    return this.withIndex(async () => {
      const current = (await this.readIndex(host)).records.find((record) => record[key] === value.trim());
      if (current && (!current.legacy || current.suppress_completion)) return current;
      // Retained baseline tools can still add tasks and settle foreground waits
      // after v3 migration. Read their correlation without writing their key.
      const legacy = await host.call("host.storage.get", { scope: "workspace", key: legacyTaskIndexKey });
      const records = legacy.value === null ? [] : (JSON.parse(legacy.value) as TaskIndex).records ?? [];
      const record = records.find((item) => item[key] === value.trim());
      return record ? { ...record, legacy: true } : undefined;
    });
  }

  private deleteRecord(host: RuntimeHost, sessionID: string): Promise<void> {
    return this.withIndex(async () => {
      const index = await this.readIndex(host);
      index.records = index.records.filter((record) => {
        if (record.session_id !== sessionID.trim()) return true;
        // Keep a bounded completion tombstone while an old writer still owns
        // its source record; otherwise a repeated event would import it again.
        if (record.legacy) { record.suppress_completion = true; return true; }
        return false;
      });
      await this.writeIndex(host, index);
    });
  }

  private async readIndex(host: RuntimeHost): Promise<TaskIndex> {
    const result = await host.call("host.storage.get", { scope: "workspace", key: taskIndexKey });
    if (result.value !== null) {
      const index = JSON.parse(result.value) as TaskIndex;
      return { records: index.records ?? [], dispatches: index.dispatches ?? [] };
    }
    // Retained pre-v3 generations still write tasks.v2. Copy their correlation
    // records once, but leave their key intact and never share the new ledger.
    const legacy = await host.call("host.storage.get", { scope: "workspace", key: legacyTaskIndexKey });
    if (legacy.value !== null) {
      const old = JSON.parse(legacy.value) as TaskIndex;
      const index = { records: (old.records ?? []).map((record) => ({ ...record, legacy: true })), dispatches: old.dispatches ?? [] };
      for (const dispatch of index.dispatches) {
        if (!/^[a-f0-9]{64}$/.test(dispatch.signature)) dispatch.signature = createHash("sha256").update(dispatch.signature).digest("hex");
      }
      await this.writeIndex(host, index);
      return index;
    }
    const { keys } = await host.call("host.storage.keys", { scope: "workspace" });
    const records: TaskRecord[] = [];
    const seen = new Set<string>();
    for (const key of keys ?? []) {
      if (!key.startsWith("session.")) continue;
      const { value } = await host.call("host.storage.get", { scope: "workspace", key });
      if (value === null) continue;
      const record = JSON.parse(value) as TaskRecord;
      if (!seen.has(record.session_id)) { seen.add(record.session_id); records.push(record); }
    }
    const index: TaskIndex = { records: records.slice(-maxTaskRecords), dispatches: [] };
    // Older retained generations can still need their original keys.
    await this.writeIndex(host, index);
    return index;
  }

  private async writeIndex(host: RuntimeHost, index: TaskIndex): Promise<void> {
    compactDispatches(index);
    if (reservedIndexBytes(index) > maxTaskIndexBytes) throw new Error("subagent dispatch storage capacity is full; resolve pending tasks before creating more");
    await host.call("host.storage.set", { scope: "workspace", key: taskIndexKey, value: JSON.stringify(index) });
  }
}

function compactDispatches(index: TaskIndex): void {
  for (const dispatch of index.dispatches) {
    if (dispatch.cancelled || dispatch.settled) {
      // Keep the receipt and fingerprint for replay without retaining task text.
      dispatch.prompt = undefined;
      dispatch.create = undefined;
    }
  }
}

function reservedIndexBytes(index: TaskIndex): number {
  // Reserve record/receipt expansion before creation or send has effects.
  // Host-generated session/turn/queue identifiers fit within the fixed slack;
  // Include the outer JSON string escaping used by plugin storage as well as
  // UTF-8 bytes, so quote-heavy or HTML task briefs cannot bypass admission.
  return storageStringBytes(JSON.stringify(index)) + index.dispatches.reduce((bytes, dispatch) =>
    bytes + (!dispatch.result && !dispatch.cancelled ? 2 * storageStringBytes(JSON.stringify(dispatch.record)) + 2048 : 0), 0);
}

function storageStringBytes(value: string): number {
  return Buffer.byteLength(JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, (character) =>
    `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`));
}

function dispatchSignature(value: unknown): string {
  return createHash("sha256").update(JSON.stringify(value)).digest("hex");
}

function trimIndex(index: TaskIndex, limit: number): void {
  let excess = index.records.length - limit;
  if (excess <= 0) return;
  index.records = index.records.filter((record) => {
    if (excess > 0 && terminalState(record.state)) { excess -= 1; return false; }
    return true;
  });
  if (excess > 0) throw new Error(`subagent task record capacity (${maxTaskRecords}) is full; unfinished records must be resolved before creating more tasks`);
}

function dispatchIdentity(call: ToolExecuteParams): string {
  if (!call.call_id?.trim()) return randomBytes(12).toString("hex");
  return createHash("sha256").update(`${call.session_id ?? ""}\0${call.turn_id ?? ""}\0${call.call_id}`).digest("hex").slice(0, 24);
}

function deriveTaskName(description: string): string {
  return description.trim().toLowerCase().replace(/[^a-z0-9_]+/g, "_").replace(/^_+|_+$/g, "").slice(0, 40).replace(/_+$/, "") || "task";
}

function lifecycleLabel(state: string): string {
  return ({ completed: "完成", failed: "失败", interrupted: "中断" } as Record<string, string>)[state] ?? "取消";
}

function workerInstructions(type?: string): string {
  return type === "worker"
    ? "Implement only the assigned scoped change in the provided isolated workspace. Preserve unrelated work, verify locally, and report exact changed files, commands, blockers, and evidence. Your final message is the deliverable the parent receives."
    : "Work as a bounded child task. Complete only the assigned scope, preserve unrelated work, verify applicable claims, and report exact changed files, commands, blockers, and evidence. Your final message is the deliverable the parent receives.";
}

const promptSection = `# Subagent results

A completed subagent task does not mean the overall task is complete. Integrate the result and verify the overall work before claiming completion.

# Delegation

The main agent owns the user conversation, final synthesis, and decision about whether delegation is worth the overhead. Keep tightly coupled or trivial work local. Delegate bounded research, verification, or disjoint implementation when separate context materially improves the result. Set run_in_background=false when your next step depends on the result so it returns in the current tool call. Use background mode for independent work you can overlap. A foreground wait that exceeds ten minutes automatically becomes background and completes through the normal notification path.

The Subagent plugin owns its task records, cancellation propagation, concurrency policy, and recovery. Use the public Session services to build whatever task topology fits the work; the host does not impose a delegation tree or a global child limit.

Child sessions use fresh conversation context by default while retaining installed plugins and workspace capabilities. Fresh task prompts must be self-contained. Use context=fork only when inherited conversation history is materially necessary, and still provide a concrete directive and scope. Child completion is delivered as a read-only query bubble; do not poll while work is running.`;

export default new SubagentPlugin();
