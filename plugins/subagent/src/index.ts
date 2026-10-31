import { createHash, randomBytes } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";
import {
  requireKernelService,
  type AgentTurnInterruptedInput,
  type CapabilityInvokeParams,
  type HostServiceContracts,
  type JSONSchemaObject,
  type KernelHostServiceMethod,
  type RuntimeExecutionContext,
  type RuntimeLifecycleContext,
  type RuntimeHost,
  type RuntimePlugin,
  type ToolExecuteParams,
  type ToolExecuteResult,
  type TurnLifecycleInput,
} from "@wuu/plugin-sdk";

const promptCapability = "agent.system_prompt.section";
const clientCapability = "plugin.client.request";
const lifecycleCapability = "agent.turn.lifecycle";
const interruptCapability = "agent.turn.interrupted";
const taskIndexKey = "tasks.v2";
const maxTaskRecords = 128;
const foregroundAwaitBudgetMS = 10 * 60 * 1000;
const maintenanceIntervalMS = 10000;
const maintenanceTimeoutMS = 30000;
const deliveryClaimLeaseMS = 30000;

type CompletionRecord = {
  request_id: string;
  output?: string;
  delivered?: boolean;
  claimed_at?: number;
  source_request_id?: string;
  state?: string;
  parent_session_id?: string;
  parent_turn_id?: string;
};
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
  cancelled?: boolean;
  cancel_pending?: boolean;
  admitting?: boolean;
  creation?: { request_id: string; digest: string; params: SessionCreateParams; prompt: string; started?: boolean };
  continuation?: {
    previous_request_id: string; previous_state: string;
    params: HostServiceContracts["host.session.send"]["params"];
    source?: { session_id: string; request_id: string; name: string; state: string };
  };
  followup_calls?: { id: string; digest: string; steered?: boolean }[];
  followup?: { id: string; digest: string; previous_request_id: string; prompt: string; settled?: boolean; steered?: boolean };
  completion?: CompletionRecord;
  pending_completions?: CompletionRecord[];

};
type TaskIndex = { records: TaskRecord[] };
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
  private indexQueue: Promise<unknown> = Promise.resolve();
  private readonly stop = new AbortController();
  private maintenance?: Promise<void>;
  private transitionQueue: Promise<unknown> = Promise.resolve();

  initialize() {
    const services: KernelHostServiceMethod[] = [
      "host.session.create", "host.session.send", "host.session.list", "host.session.cancel", "host.session.inspect",
      "host.storage.get", "host.storage.compare_exchange", "host.storage.keys", "host.storage.delete",
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
      required_services: services.map(requireKernelService),
    };
  }

  async activate(host: RuntimeHost, lifecycle: RuntimeLifecycleContext): Promise<void> {
    const signal = AbortSignal.any([lifecycle.signal, this.stop.signal]);
    try { await this.reconcileRequests(host); } catch { /* Activation can proceed while durable storage recovers. */ }
    this.maintenance = (async () => {
      while (!signal.aborted) {
        try { await delay(maintenanceIntervalMS, undefined, { signal }); } catch { break; }
        const runSignal = AbortSignal.any([signal, AbortSignal.timeout(maintenanceTimeoutMS)]);
        const scoped: RuntimeHost = {
          supports: method => host.supports(method),
          call: (method, params, options) => host.call(method, params, {
            signal: AbortSignal.any([runSignal, ...(options?.signal ? [options.signal] : [])]),
          }),
        };
        try { await this.reconcileRequests(scoped); } catch { /* Durable reservations and deliveries remain retryable. */ }
      }
    })();
  }

  async shutdown(): Promise<void> { this.stop.abort(); await this.maintenance; }

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
    const requestID = toolRequestID(call);
    let context: "fresh" | "fork" = "fresh";
    switch (args.context?.trim() ?? "") {
      case "": case "fresh": break;
      case "fork": context = "fork"; break;
      default: throw new Error(`unsupported subagent context ${JSON.stringify(args.context)}`);
    }
    const workspace = args.isolation?.trim() === "worktree" ? "worktree" : "shared";
    if (workspace === "worktree") context = "fork";
    const { record, sent } = await this.withTransition(execution, async () => {
      const params: SessionCreateParams = {
        request_id: `create-${requestID}`, name, visibility: "plugin", parent_session_id: parentSessionID,
        context_source: context, workspace, model_alias: args.model?.trim(), instructions: workerInstructions(args.subagent_type?.trim()),
      };
      const digest = hash(JSON.stringify({ params, prompt: args.prompt.trim() }));
      const beforeReservation = (await this.readIndex(host)).index;
      if (beforeReservation.records.length >= maxTaskRecords) {
        const { sessions } = await host.call("host.session.list", { scope: "owned" });
        const owned = new Set(sessions.map(session => session.session_id));
        const missing = new Set(beforeReservation.records.filter(record => record.session_id && !owned.has(record.session_id)).map(record => record.session_id));
        await this.updateIndex(host, index => {
          for (const record of index.records) {
            if (!missing.has(record.session_id)) continue;
            record.state = "discarded";
            record.cancelled = true;
            record.suppress_completion = true;
            record.cancel_pending = false;
            record.admitting = false;
          }
        });
      }
      const record = await this.updateIndex(host, index => {
        const prior = index.records.find(record => record.creation?.request_id === params.request_id);
        if (prior) {
          if (prior.creation!.digest !== digest) throw new Error("subagent tool call identity was reused with different input");
          return prior;
        }
        trimIndex(index, maxTaskRecords - 1);
        const reserved: TaskRecord = {
          session_id: "", parent_session_id: parentSessionID, parent_turn_id: call.turn_id,
          name, request_id: `turn-${requestID}`, state: "created",
          creation: { request_id: params.request_id, digest, params, prompt: args.prompt.trim() },
        };
        index.records.push(reserved);
        return reserved;
      });
      const current = await this.resumeTask(host, record);
      return { record: current, sent: { state: current.state } };
    });
    const identity = { session_id: record.session_id, task_name: name, run_in_background: background };
    if (background || record.suppress_completion) return textResult({ ...identity, state: sent.state, backgrounded: true });

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
      await this.changeRecord(host, taskKey(record), record.request_id, current => {
        current.state = completed.state;
        current.turn_id = completed.turn_id;
        current.queue_id = completed.queue_id;
        current.suppress_completion = true;
      });
    });
    return textResult({ ...identity, state: inspected.turn.state, backgrounded: false,
      ...(inspected.turn.final_output ? { final_output: inspected.turn.final_output } : {}),
      ...(inspected.turn.error ? { error: inspected.turn.error } : {}),
    });
  }

  private async sendMessage(host: RuntimeHost, call: ToolExecuteParams): Promise<ToolExecuteResult> {
    const args = call.arguments as { target: string; message: string };
    if (!args.message?.trim()) throw new Error("send_message requires a message");
    const record = await this.resolveTask(host, call.session_id ?? "", args.target);
    const id = `turn-${toolRequestID(call)}`;
    const digest = hash(JSON.stringify({ target: record.session_id, message: args.message.trim() }));
    const next = await this.changeRecord(host, taskKey(record), undefined, current => {
      const prior = current.followup_calls?.find(call => call.id === id);
      if (prior) {
        if (prior.digest !== digest) throw new Error("subagent tool call identity was reused with different input");
        return;
      }
      if ((current.followup_calls?.length ?? 0) >= maxTaskRecords) throw new Error("subagent follow-up receipt capacity reached; start a new bounded task");
      if (!current.session_id || current.admitting || current.cancel_pending || current.continuation) throw new Error("subagent admission or cancellation is pending; retry after it settles");
      archiveCompletion(current);
      current.followup_calls = [...(current.followup_calls ?? []), { id, digest }];
      current.followup = { id, digest, previous_request_id: current.request_id, prompt: args.message.trim() };
      current.request_id = id;
      current.parent_session_id = call.session_id ?? "";
      current.parent_turn_id = call.turn_id;
      current.suppress_completion = false;
      current.cancelled = false;
      current.completion = undefined;
      current.state = "created";
      current.turn_id = undefined;
      current.queue_id = undefined;
    });
    if (!next) throw new Error("subagent task is no longer retained");
    if (next.followup?.id !== id) {
      const inspected = await host.call("host.session.inspect", { session_id: next.session_id, request_id: id, wait: "none" });
      return textResult({ session_id: next.session_id, state: inspected.turn?.state ?? next.state, steered: next.followup_calls?.find(call => call.id === id)?.steered ?? false });
    }
    const current = await this.resumeTask(host, next);
    return textResult({ session_id: current.session_id, state: current.state, steered: current.followup?.steered ?? false });
  }

  private async closeAgent(host: RuntimeHost, call: ToolExecuteParams): Promise<ToolExecuteResult> {
    const { target } = call.arguments as { target: string };
    const record = await this.resolveTask(host, call.session_id ?? "", target);
    const cancelled = await this.dismiss(host, record);
    const superseded = !cancelled || cancelled.request_id !== record.request_id || !cancelledTask(cancelled);
    return textResult({ session_id: cancelled?.session_id ?? record.session_id, cancelled: !superseded && !cancelled?.cancel_pending,
      cancel_pending: !superseded && !!cancelled?.cancel_pending, ...(superseded ? { superseded: true } : {}) });
  }

  async invokeCapability(call: CapabilityInvokeParams, host: RuntimeHost) {
    switch (call.capability) {
      case promptCapability: return { output: { text: promptSection } };
      case clientCapability: {
        const request = call.input as { method: string; input?: HostServiceContracts["host.session.list"]["params"] };
        if (request.method !== "status.list") throw new Error(`unknown client method ${JSON.stringify(request.method)}`);
        return { output: { result: await host.call("host.session.list", request.input ?? {}) } };
      }
      case lifecycleCapability: await this.onLifecycle(host, call.input as TurnLifecycleInput); break;
      case interruptCapability: await this.onInterrupted(host, call.input as AgentTurnInterruptedInput); break;
      default: throw new Error(`unknown capability ${JSON.stringify(call.capability)}`);
    }
    return { output: {} };
  }

  private async onLifecycle(host: RuntimeHost, input: TurnLifecycleInput): Promise<void> {
    const { index } = await this.readIndex(host);
    const delivery = index.records.find(record => completions(record).some(completion =>
      (completion.parent_session_id ?? record.parent_session_id) === input.thread_id && completion.request_id === input.request_id));
    if (delivery) {
      await this.changeCompletion(host, taskKey(delivery), input.request_id, completion => {
        if (input.state === "running" || terminalState(input.state) && !(input.state === "discarded" && input.retryable)) completion.delivered = true;
        if (input.state === "discarded" && input.retryable) completion.claimed_at = undefined;
      });
      if (input.state === "discarded" && input.retryable) {
        await this.deliverCompletion(host, delivery, input.request_id);
        return;
      }
    }
    if (!terminalState(input.state)) return;
    if (index.records.some(record => record.session_id === input.thread_id && record.continuation?.previous_request_id === input.request_id)) throw new Error("subagent continuation admission is pending; retry terminal delivery");
    let record = index.records.find(record => record.session_id === input.thread_id && record.request_id === input.request_id);
    if (!record) {
      const historical = index.records.find(value => value.session_id === input.thread_id
        && value.pending_completions?.some(completion => completion.source_request_id === input.request_id));
      if (historical) {
        await this.deliverCompletion(host, historical, historical.pending_completions!.find(completion => completion.source_request_id === input.request_id)!.request_id);
        return;
      }
      await this.reconcileOwnedSessions(host);
      record = (await this.readIndex(host)).index.records.find(record => record.session_id === input.thread_id && record.request_id === input.request_id);
    }
    if (!record) return;
    const current = await this.changeRecord(host, taskKey(record), input.request_id, current => {
      current.state = input.state;
      if (!current.suppress_completion && !current.completion) current.completion = {
        request_id: `deliver-${hash(`subagent.completion\0${input.thread_id}\0${input.request_id.trim()}`).slice(0, 24)}`,
        source_request_id: current.request_id, state: input.state,
        parent_session_id: current.parent_session_id, parent_turn_id: current.parent_turn_id,
      };
    });
    if (current) await this.deliverCompletion(host, current);
  }

  private async deliverCompletion(host: RuntimeHost, record: TaskRecord, deliveryID = record.completion?.request_id): Promise<void> {
    if (!deliveryID) return;
    const claimedAt = Date.now();
    const claimed = await this.updateIndex(host, index => {
      const current = index.records.find(item => taskKey(item) === taskKey(record));
      if (!current || cancelledTask(current)) return;
      const completion = completions(current).find(value => value.request_id === deliveryID);
      if (!completion || completion.delivered || current.completion === completion && current.suppress_completion) return;
      if (completion.claimed_at && claimedAt - completion.claimed_at < deliveryClaimLeaseMS) return;
      completion.claimed_at = claimedAt;
      return { record: current, completion };
    });
    if (!claimed) return;
    const { record: current, completion } = claimed;
    const parentID = completion.parent_session_id ?? current.parent_session_id;
    try {
      const inspected = await host.call("host.session.inspect", { session_id: parentID, request_id: completion.request_id, wait: "none" });
      if (inspected.turn && terminalState(inspected.turn.state) && !(inspected.turn.state === "discarded" && inspected.turn.retryable)) {
        await this.changeCompletion(host, taskKey(current), deliveryID, value => { value.delivered = true; });
        return;
      }
      if (inspected.turn && ["queued", "running"].includes(inspected.turn.state)) return;
      // A claimed send may already be in flight. Cancellation cannot retract
      // input admitted by the host, but blocks unclaimed pending feedback.
      const latest = await this.loadTask(host, taskKey(current));
      if (!latest || cancelledTask(latest) || !completions(latest).some(value => value.request_id === deliveryID && !value.delivered)) return;
      const parent = await this.loadRecord(host, "session_id", parentID);
      if (parent && cancelledTask(parent)) return;
      const source = { session_id: current.session_id, request_id: completion.source_request_id ?? current.request_id, name: current.name, state: completion.state ?? current.state };
      const output = completion.output ?? await this.completionOutput(host, source.session_id, source.request_id);
      const params: HostServiceContracts["host.session.send"]["params"] = {
        request_id: completion.request_id, session_id: parentID, reply_to_turn_id: completion.source_request_id ? completion.parent_turn_id : current.parent_turn_id,
        input: { prompt: completionPrompt(source.name, source.state, output) },
        presentation: { kind: "query_bubble", text: `子任务 ${current.name} 已更新`, name: current.name, related_session_id: current.session_id },
        cause: "subagent.completion", if_running: "steer",
      };
      let continuation: TaskRecord | undefined;
      if (parent?.parent_session_id) {
        continuation = await this.changeRecord(host, taskKey(parent), parent.request_id, value => {
          if (cancelledTask(value)) return;
          if (value.continuation && value.continuation.params.request_id !== completion.request_id) throw new Error("parent continuation admission is pending; retry child feedback");
          if (!value.continuation) {
            archiveCompletion(value);
            value.continuation = {
              previous_request_id: value.request_id, previous_state: value.state,
              params: completion.output === undefined ? { ...params, input: { prompt: "" } } : params,
              ...(completion.output === undefined ? { source } : {}),
            };
          }
          value.request_id = completion.request_id;
          value.suppress_completion = false;
          value.state = "created";
        });
        if (!continuation || cancelledTask(continuation)) return;
      }
      const sent = await host.call("host.session.send", params);
      if (continuation) await this.settleSend(host, continuation, sent);
      if (sent.state === "running" && !sent.steered || terminalState(sent.state)) {
        await this.changeCompletion(host, taskKey(current), deliveryID, value => { value.delivered = true; });
      }
    } finally {
      await this.changeCompletion(host, taskKey(current), deliveryID, value => {
        if (value.claimed_at === claimedAt) value.claimed_at = undefined;
      });
    }
  }

  private async completionOutput(host: RuntimeHost, sessionID: string, requestID: string): Promise<string> {
    const { turn } = await host.call("host.session.inspect", { session_id: sessionID, request_id: requestID, wait: "none" });
    if (!turn || !terminalState(turn.state)) throw new Error("subagent terminal receipt is unavailable; retry feedback delivery");
    return turn.final_output?.trim() || turn.error?.trim() || "子任务已结束，但没有返回文本结果。";
  }

  private changeCompletion(host: RuntimeHost, key: string, deliveryID: string, mutate: (completion: CompletionRecord) => void): Promise<TaskRecord | undefined> {
    return this.changeRecord(host, key, undefined, current => {
      const completion = completions(current).find(value => value.request_id === deliveryID);
      if (completion) mutate(completion);
      current.pending_completions = current.pending_completions?.filter(value => !value.delivered);
    });
  }

  private async onInterrupted(host: RuntimeHost, input: AgentTurnInterruptedInput): Promise<void> {
    if (!input.thread_id?.trim() || !input.turn_id?.trim()) return;
    const { index } = await this.readIndex(host);
    for (const record of index.records) {
      if (record.parent_session_id === input.thread_id && record.parent_turn_id === input.turn_id) await this.dismiss(host, record);
    }
  }

  private async dismiss(host: RuntimeHost, record: TaskRecord): Promise<TaskRecord | undefined> {
    const current = await this.changeRecord(host, taskKey(record), record.request_id, current => {
      current.suppress_completion = true;
      current.cancelled = true;
      current.cancel_pending = true;
      current.state = "interrupted";
    });
    return current ? this.cancelTask(host, current) : undefined;
  }

  private async cancelTask(host: RuntimeHost, record: TaskRecord): Promise<TaskRecord> {
    let current = await this.loadTask(host, taskKey(record));
    if (!current?.cancel_pending) return current ?? record;
    if (!current.session_id && current.creation?.started) {
      const { sessions } = await host.call("host.session.list", { scope: "owned" });
      const found = sessions.find(session => session.creation_request_id === current!.creation!.request_id);
      if (!found) return current;
      current = await this.changeRecord(host, taskKey(current), current.request_id, value => { value.session_id = found.session_id; }) ?? current;
    }
    if (current.session_id) {
      try {
        const inspected = await host.call("host.session.inspect", { session_id: current.session_id, request_id: current.request_id, wait: "none" });
        if (inspected.turn && !terminalState(inspected.turn.state)) {
          // Queue admission can race cancellation. Retry the same request, never the session's unrelated active turn.
          if (!inspected.turn.turn_id && !inspected.turn.queue_id) return current;
          const result = await host.call("host.session.cancel", {
            session_id: current.session_id, turn_id: inspected.turn.turn_id, queue_id: inspected.turn.queue_id,
          });
          if (!result.cancelled) return current;
        } else if (!inspected.turn && current.admitting) return current;
      } catch {
        // An uncertain inspection/cancel is not an acknowledgment; maintenance retains the durable intent.
        return current;
      }
    }
    return await this.changeRecord(host, taskKey(current), current.request_id, value => { value.cancel_pending = false; value.admitting = false; }) ?? current;
  }

  private async resumeTask(host: RuntimeHost, record: TaskRecord): Promise<TaskRecord> {
    let current = await this.loadTask(host, taskKey(record));
    if (!current) throw new Error("subagent task is no longer retained");
    if (current.suppress_completion) return this.cancelTask(host, current);
    if (!current.session_id && current.creation) {
      current = await this.changeRecord(host, taskKey(current), current.request_id, value => {
        if (!value.suppress_completion) value.creation!.started = true;
      });
      if (!current) throw new Error("subagent request was replaced");
      if (current.suppress_completion) return this.cancelTask(host, current);
      const created = await host.call("host.session.create", current.creation!.params);
      current = await this.changeRecord(host, taskKey(current), current.request_id, value => { value.session_id = created.session_id; });
      if (!current) throw new Error("subagent request was replaced");
    }
    if (current.suppress_completion) return this.cancelTask(host, current);
    if (current.state !== "created" || !current.continuation && current.followup?.settled) return current;
    current = await this.changeRecord(host, taskKey(current), current.request_id, value => {
      if (!value.suppress_completion) value.admitting = true;
    });
    if (!current) throw new Error("subagent request was replaced");
    if (current.suppress_completion) return this.cancelTask(host, current);
    let continuationParams = current.continuation?.params;
    if (current.continuation?.source) {
      const source = current.continuation.source;
      const output = await this.completionOutput(host, source.session_id, source.request_id);
      continuationParams = { ...current.continuation.params, input: { prompt: completionPrompt(source.name, source.state, output) } };
    }
    const sent = await host.call("host.session.send", continuationParams ?? {
      request_id: current.request_id, session_id: current.session_id,
      input: { prompt: current.followup?.prompt ?? current.creation!.prompt },
      presentation: { kind: "query_bubble", text: current.followup ? "父任务已更新要求" : `子任务 ${current.name} 已开始`, name: current.name },
      cause: current.followup ? "subagent.followup" : "subagent.task", ...(current.followup ? { if_running: "steer" as const } : {}),
    });
    current = await this.settleSend(host, current, sent);
    if (!current) throw new Error("subagent request was replaced");
    if (current.suppress_completion) return this.cancelTask(host, current);
    return current;
  }

  private settleSend(host: RuntimeHost, record: TaskRecord, sent: HostServiceContracts["host.session.send"]["result"]): Promise<TaskRecord | undefined> {
    return this.updateIndex(host, index => {
      const current = index.records.find(value => taskKey(value) === taskKey(record));
      if (!current) return;
      if (current.request_id !== record.request_id) return current.followup?.id === record.request_id && current.followup.settled ? current : undefined;
      current.admitting = false;
      // Non-steered admission is durable. A pending steer may still be
      // unconsumed, so it must keep its input rather than claim that guarantee.
      if (!current.continuation && !sent.steered) {
        if (current.followup) current.followup.prompt = "";
        else if (current.creation) current.creation.prompt = "";
      }
      current.turn_id = sent.turn_id;
      current.queue_id = sent.queue_id;
      if (!current.suppress_completion && current.state === "created") current.state = sent.state;
      if (current.continuation) {
        if (sent.steered) {
          current.request_id = current.continuation.previous_request_id;
          if (!current.suppress_completion) current.state = current.continuation.previous_state;
        }
        current.continuation = undefined;
      } else if (current.followup) {
        current.followup.settled = true;
        current.followup.steered = sent.steered ?? false;
        const call = current.followup_calls?.find(call => call.id === current.followup!.id);
        if (call) call.steered = sent.steered ?? false;
        if (sent.steered) current.request_id = current.followup.previous_request_id;
      }
      return current;
    });
  }

  private async resolveTask(host: RuntimeHost, parentID: string, target: string): Promise<TaskRecord> {
    target = target?.trim() ?? "";
    const { index } = await this.readIndex(host);
    const records = index.records.filter(record => record.parent_session_id === parentID.trim() && (record.session_id === target || record.name === target));
    if (records.length > 1) throw new Error(`task name ${JSON.stringify(target)} is ambiguous; use the session id`);
    if (records.length === 1) return records[0];
    const listed = await host.call("host.session.list", { parent_session_id: parentID.trim() });
    const matches = listed.sessions.filter(item => item.session_id === target || item.name === target);
    if (matches.length > 1) throw new Error(`task name ${JSON.stringify(target)} is ambiguous; use the session id`);
    if (!matches[0]) throw new Error(`child session ${JSON.stringify(target)} not found`);
    const match = matches[0];
    const inspected = await host.call("host.session.inspect", { session_id: match.session_id, wait: "none" });
    const recovered: TaskRecord = {
      session_id: match.session_id, parent_session_id: match.parent_session_id ?? "", name: match.name ?? "",
      request_id: inspected.turn?.request_id ?? "", turn_id: inspected.turn?.turn_id,
      queue_id: inspected.turn?.queue_id, state: inspected.turn?.state ?? match.state,
    };
    await this.updateIndex(host, index => { if (!index.records.some(record => record.session_id === recovered.session_id)) index.records.push(recovered); trimIndex(index, maxTaskRecords); });
    return recovered;
  }

  private async reconcileOwnedSessions(host: RuntimeHost): Promise<void> {
    const listed = await host.call("host.session.list", { scope: "owned" });
    for (const child of listed.sessions) {
      if (await this.loadRecord(host, "session_id", child.session_id)) continue;
      const { turn } = await host.call("host.session.inspect", { session_id: child.session_id, wait: "none" });
      if (!turn || terminalState(turn.state) || !turn.request_id?.trim()) continue;
      await this.updateIndex(host, index => {
        if (!index.records.some(record => record.session_id === child.session_id)) index.records.push({
          session_id: child.session_id, parent_session_id: child.parent_session_id ?? "", name: child.name ?? "",
          request_id: turn.request_id!, turn_id: turn.turn_id, queue_id: turn.queue_id, state: turn.state,
        });
        trimIndex(index, maxTaskRecords);
      });
    }
  }

  private async reconcileRequests(host: RuntimeHost): Promise<void> {
    const { index } = await this.readIndex(host);
    for (const record of index.records) {
      try {
        if (record.cancel_pending) { await this.cancelTask(host, record); continue; }
        if (cancelledTask(record)) continue;
        for (const completion of completions(record)) {
          if (!completion.delivered) {
            try { await this.deliverCompletion(host, record, completion.request_id); } catch { /* Retry this receipt without blocking newer task admission. */ }
          }
        }
        if (record.suppress_completion) continue;
        if (record.state === "created" && (record.creation || record.followup || record.continuation)) { await this.resumeTask(host, record); continue; }
        if (!record.session_id || record.completion) continue;
        const { turn } = await host.call("host.session.inspect", { session_id: record.session_id, request_id: record.request_id, wait: "none" });
        if (turn && terminalState(turn.state)) await this.onLifecycle(host, { ...turn, request_id: record.request_id, thread_id: record.session_id });
      } catch { /* One unresolved receipt must not block recovery of other tasks. */ }
    }
  }

  // Local ordering complements CAS; it is not the cross-generation authority.
  private withTransition<T>(execution: RuntimeExecutionContext, operation: () => Promise<T>): Promise<T> {
    const next = this.transitionQueue.then(() => { execution.signal.throwIfAborted(); return operation(); });
    this.transitionQueue = next.catch(() => undefined);
    return next;
  }

  private updateIndex<T>(host: RuntimeHost, mutate: (index: TaskIndex) => T): Promise<T> {
    const next = this.indexQueue.then(async () => {
      for (let attempt = 0; attempt < 8; attempt++) {
        const { index, value } = await this.readIndex(host);
        const result = mutate(index);
        const swapped = await host.call("host.storage.compare_exchange", { scope: "workspace", key: taskIndexKey, expected: value, value: JSON.stringify(index) });
        if (swapped.swapped) return result;
      }
      throw new Error("subagent task state changed too frequently; retry");
    });
    this.indexQueue = next.catch(() => undefined);
    return next;
  }

  private changeRecord(host: RuntimeHost, key: string, requestID: string | undefined, mutate: (record: TaskRecord) => void): Promise<TaskRecord | undefined> {
    return this.updateIndex(host, index => {
      const record = index.records.find(record => taskKey(record) === key && (requestID === undefined || record.request_id === requestID));
      if (record) mutate(record);
      return record;
    });
  }

  private async loadTask(host: RuntimeHost, key: string): Promise<TaskRecord | undefined> {
    return (await this.readIndex(host)).index.records.find(record => taskKey(record) === key);
  }

  private async loadRecord(host: RuntimeHost, key: "session_id" | "request_id", value: string): Promise<TaskRecord | undefined> {
    return (await this.readIndex(host)).index.records.find(record => record[key] === value.trim());
  }

  private async readIndex(host: RuntimeHost): Promise<{ index: TaskIndex; value: string }> {
    const result = await host.call("host.storage.get", { scope: "workspace", key: taskIndexKey });
    if (result.value !== null) return { index: JSON.parse(result.value) as TaskIndex, value: result.value };
    const { keys } = await host.call("host.storage.keys", { scope: "workspace" });
    const records: TaskRecord[] = [];
    for (const key of keys) {
      if (!key.startsWith("session.")) continue;
      const { value } = await host.call("host.storage.get", { scope: "workspace", key });
      if (value === null) continue;
      const record = JSON.parse(value) as TaskRecord;
      if (!records.some(existing => existing.session_id === record.session_id)) records.push(record);
    }
    const index = { records: records.slice(-maxTaskRecords) };
    const value = JSON.stringify(index);
    const swapped = await host.call("host.storage.compare_exchange", { scope: "workspace", key: taskIndexKey, expected: null, value });
    if (!swapped.swapped) return this.readIndex(host);
    for (const key of keys) if (key.startsWith("session.") || key.startsWith("request.")) await host.call("host.storage.delete", { scope: "workspace", key });
    return { index, value };
  }

}

const hash = (value: string): string => createHash("sha256").update(value).digest("hex");
const taskKey = (record: TaskRecord): string => record.creation?.request_id ?? record.session_id;
const cancelledTask = (record: TaskRecord): boolean => record.cancelled === true || record.suppress_completion === true && record.state === "interrupted";
function toolRequestID(call: ToolExecuteParams): string {
  if (!call.call_id) return randomBytes(12).toString("hex");
  const identity = call.turn_id || call.step_index !== undefined
    ? JSON.stringify([call.tool_id, call.session_id, call.call_id, call.turn_id ?? null, call.step_index ?? null])
    : `${call.tool_id}\0${call.session_id}\0${call.call_id}`;
  return hash(identity).slice(0, 32);
}

function completionPrompt(name: string, state: string, output: string): string {
  return `子任务 ${name} 已${lifecycleLabel(state)}。请检查并整合以下交接结果：\n\n${output}`;
}

function completions(record: TaskRecord): CompletionRecord[] {
  return [...(record.pending_completions ?? []), ...(record.completion ? [record.completion] : [])];
}

function archiveCompletion(record: TaskRecord): void {
  const pending = (record.pending_completions ?? []).filter(completion => !completion.delivered);
  if (record.completion && !record.completion.delivered) pending.push({
    ...record.completion, source_request_id: record.completion.source_request_id ?? record.request_id,
    state: record.completion.state ?? record.state,
    parent_session_id: record.completion.parent_session_id ?? record.parent_session_id,
    parent_turn_id: record.completion.parent_turn_id ?? record.parent_turn_id,
  });
  // Leave a slot for the next admitted request's result before starting work.
  if (pending.length >= maxTaskRecords) throw new Error("subagent feedback receipt capacity reached; wait for pending delivery before starting more work");
  record.pending_completions = pending;
  record.completion = undefined;
}

function trimIndex(index: TaskIndex, limit: number): void {
  let excess = index.records.length - limit;
  if (excess <= 0) return;
  index.records = index.records.filter((record) => {
    if (excess > 0 && terminalState(record.state) && (cancelledTask(record) && !record.cancel_pending
      || !(record.pending_completions?.length) && (record.completion?.delivered || record.suppress_completion && !record.cancel_pending))
      && !index.records.some(child => child.parent_session_id === record.session_id && !child.completion?.delivered && !child.suppress_completion)) { excess -= 1; return false; }
    return true;
  });
  if (excess > 0) throw new Error(`subagent task record capacity (${maxTaskRecords}) is full; unfinished records must be resolved before creating more tasks`);
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
