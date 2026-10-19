import { randomBytes } from "node:crypto";
import { normalize, parse, sep } from "node:path";
import { requireKernelService, type RuntimeHost, type RuntimePlugin, type RuntimeInitializeParams, type ToolExecuteParams, type TurnLifecycleInput } from "@wuu/plugin-sdk";
import { localTimezone, parseCronExpression, validateTimezone } from "./schedule.js";

const storageKey = "automation.state";
const terminal = new Set(["completed", "failed", "interrupted", "discarded"]);
const zeroTime = "0001-01-01T00:00:00Z";

export interface Task {
  id: string; title: string; prompt: string; cron: string; timezone: string;
  mode: string; workspace_id?: string; workspace_root: string; workspace_mode?: string;
  heartbeat_thread_id?: string; recurring: boolean; paused: boolean; durable: boolean;
  created_at: string; next_run_at: string;
}
export interface Run {
  id: string; task_id: string; task: Task; request_id: string; status: string; triggered_at: string;
  completed_at?: string; session_id?: string; turn_id?: string; queue_id?: string; workspace_root?: string; error?: string;
}
interface Mutation {
  action?: string; id?: string; title?: string; prompt?: string; schedule?: string; cron?: string; timezone?: string;
  mode?: string; heartbeat_thread_id?: string; workspace?: string; workspace_id?: string; workspace_root?: string;
  recurring?: boolean; paused?: boolean; durable?: boolean;
}

function cleanPath(value: string): string {
  const path = normalize(value || ".");
  // Match filepath.Clean's treatment of trailing separators without trimming roots.
  return path.endsWith(sep) && path !== parse(path).root ? path.slice(0, -1) : path;
}

function object(value: unknown): Record<string, unknown> {
  if (value == null) return {};
  if (typeof value !== "object" || Array.isArray(value)) throw new Error("expected a JSON object");
  return value as Record<string, unknown>;
}
function strings(value: Record<string, unknown>, keys: readonly string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const key of keys) {
    if (value[key] == null) continue;
    if (typeof value[key] !== "string") throw new Error(`invalid string field ${key}`);
    out[key] = value[key];
  }
  return out;
}
function mutationInput(raw: unknown): Mutation {
  const value = object(raw);
  const out: Mutation = strings(value, ["action", "id", "title", "prompt", "schedule", "cron", "timezone", "mode", "heartbeat_thread_id", "workspace", "workspace_id", "workspace_root"]);
  for (const key of ["recurring", "paused", "durable"] as const) {
    if (value[key] == null) continue;
    if (typeof value[key] !== "boolean") throw new Error(`invalid boolean field ${key}`);
    out[key] = value[key];
  }
  return out;
}
function restoreTask(raw: unknown): Task {
  const value = object(raw), input = mutationInput(value);
  const extra = strings(value, ["workspace_mode", "created_at", "next_run_at"]);
  for (const key of ["created_at", "next_run_at"] as const) if (extra[key] !== undefined && !Number.isFinite(Date.parse(extra[key]))) throw new Error(`invalid timestamp ${key}`);
  return {
    id: input.id || "", title: input.title || "", prompt: input.prompt || "", cron: input.cron || "", timezone: input.timezone || "", mode: input.mode || "",
    workspace_id: input.workspace_id || undefined, workspace_root: input.workspace_root || "", workspace_mode: extra.workspace_mode || undefined,
    heartbeat_thread_id: input.heartbeat_thread_id || undefined, recurring: input.recurring || false, paused: input.paused || false, durable: input.durable || false,
    created_at: extra.created_at || zeroTime, next_run_at: extra.next_run_at || zeroTime,
  };
}
function restoreRun(raw: unknown): Run {
  const value = object(raw);
  const fields = strings(value, ["id", "task_id", "request_id", "status", "triggered_at", "completed_at", "session_id", "turn_id", "queue_id", "workspace_root", "error"]);
  for (const key of ["triggered_at", "completed_at"] as const) if (fields[key] !== undefined && !Number.isFinite(Date.parse(fields[key]))) throw new Error(`invalid timestamp ${key}`);
  return { id: "", task_id: "", request_id: "", status: "", triggered_at: zeroTime, ...fields, task: restoreTask(value.task) };
}

const cronSchema = {
  type: "object" as const,
  properties: {
    action: { type: "string", enum: ["list", "add", "remove"] }, cron: { type: "string" }, prompt: { type: "string" }, title: { type: "string" }, timezone: { type: "string" },
    mode: { type: "string", enum: ["new_thread", "thread_heartbeat"] }, heartbeat_thread_id: { type: "string" }, workspace: { type: "string", enum: ["shared", "worktree"] },
    recurring: { type: "boolean" }, durable: { type: "boolean" }, id: { type: "string" },
  }, required: ["action"],
};

export function createAutomationPlugin(): RuntimePlugin {
  let host: RuntimeHost;
  let workspaceID = "", workspaceRoot = "";
  let tasks = new Map<string, Task>(), runs: Run[] = [];
  let timer: ReturnType<typeof setInterval> | undefined;
  let dispatch: Promise<void> = Promise.resolve(), stateQueue: Promise<unknown> = Promise.resolve();
  let stopped = false;
  const abort = new AbortController();
  function stop(): void { stopped = true; clearInterval(timer); abort.abort(); }

  // State writes and mutations have the same atomic boundary as the Go mutex;
  // dispatch releases it while calling the session service so lifecycle events
  // and edits can arrive before a send response.
  function locked<T>(fn: () => T | Promise<T>): Promise<T> {
    const next = stateQueue.then(fn);
    stateQueue = next.catch(() => {});
    return next;
  }
  function snapshotTasks(): Task[] { return [...tasks.values()].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at)); }
  async function save(host: RuntimeHost): Promise<void> {
    await host.call("host.storage.set", { scope: "workspace", key: storageKey, value: JSON.stringify({ tasks: snapshotTasks().filter(task => task.durable), runs }) }, { signal: abort.signal });
  }
  async function saveBestEffort(host: RuntimeHost): Promise<void> { try { await save(host); } catch { /* Dispatch remains recoverable from its stable host request identity. */ } }

  function validated(input: Mutation, base?: Task): Task {
    if (input.workspace_id?.trim() && input.workspace_id.trim() !== workspaceID) throw new Error("automation target workspace does not match the active plugin workspace");
    if (input.workspace_root?.trim() && cleanPath(input.workspace_root.trim()) !== cleanPath(workspaceRoot)) throw new Error("automation target workspace root does not match the active plugin workspace");
    const value: Task = base ? { ...base } : { id: "", title: "", prompt: "", cron: "", timezone: "", mode: "", workspace_root: workspaceRoot, recurring: false, paused: false, durable: false, created_at: zeroTime, next_run_at: zeroTime };
    value.workspace_id = workspaceID || undefined;
    value.workspace_root = workspaceRoot;
    if (input.title?.trim() || !base) value.title = input.title?.trim() || "";
    if (input.prompt?.trim() || !base) value.prompt = input.prompt?.trim() || "";
    const schedule = input.schedule?.trim() || input.cron?.trim() || "";
    if (schedule || !base) value.cron = schedule;
    for (const key of ["recurring", "paused", "durable"] as const) if (input[key] != null) value[key] = input[key];
    if (!value.prompt && !value.paused) throw new Error("automation prompt is required");
    let expression;
    try { expression = parseCronExpression(value.cron); } catch (error) { throw new Error(`invalid automation schedule: ${error instanceof Error ? error.message : error}`); }
    if (input.timezone?.trim() || !base) value.timezone = input.timezone?.trim() || "";
    value.timezone ||= localTimezone();
    validateTimezone(value.timezone);
    if (input.mode?.trim() || !base) value.mode = input.mode?.trim() || "";
    value.mode ||= "new_thread";
    if (value.mode !== "new_thread" && value.mode !== "thread_heartbeat") throw new Error(`invalid automation mode ${JSON.stringify(value.mode)}`);
    if (input.heartbeat_thread_id?.trim()) value.heartbeat_thread_id = input.heartbeat_thread_id.trim();
    if (value.mode === "thread_heartbeat" && !value.heartbeat_thread_id) throw new Error("thread heartbeat automation requires a thread id");
    if (input.workspace?.trim() || !base) value.workspace_mode = input.workspace?.trim() || "";
    value.workspace_mode ||= "shared";
    if (value.workspace_mode !== "shared" && value.workspace_mode !== "worktree") throw new Error(`invalid automation workspace ${JSON.stringify(value.workspace_mode)}`);
    if (value.workspace_mode === "worktree" && value.mode === "thread_heartbeat") throw new Error("worktree workspace requires new_thread mode");
    const now = new Date(), next = expression.nextRun(now, value.timezone), limit = new Date(now);
    limit.setFullYear(limit.getFullYear() + 1);
    if (next > limit) throw new Error("automation next run is more than 1 year away");
    value.next_run_at = next.toISOString();
    value.title ||= value.prompt;
    return value;
  }
  async function add(host: RuntimeHost, input: Mutation): Promise<Task> {
    const task = validated(input);
    task.id = `task-${randomBytes(10).toString("hex")}`;
    task.created_at = new Date().toISOString();
    return locked(async () => {
      if (tasks.size >= 100) throw new Error("automation task limit 100 reached");
      tasks.set(task.id, task);
      try { await save(host); } catch (error) { tasks.delete(task.id); throw error; }
      return task;
    });
  }
  async function update(host: RuntimeHost, input: Mutation): Promise<Task> {
    return locked(async () => {
      const id = input.id?.trim() || "", existing = tasks.get(id);
      if (!existing) throw new Error(`automation ${JSON.stringify(id)} not found`);
      const task = validated(input, existing);
      tasks.set(id, task);
      try { await save(host); } catch (error) { tasks.set(id, existing); throw error; }
      return task;
    });
  }
  async function remove(host: RuntimeHost, rawID?: string): Promise<void> {
    const id = rawID?.trim() || "";
    if (!id) throw new Error("automation id is required");
    return locked(async () => {
      const existing = tasks.get(id);
      if (!existing) throw new Error(`automation ${JSON.stringify(id)} not found`);
      tasks.delete(id);
      try { await save(host); } catch (error) { tasks.set(id, existing); throw error; }
    });
  }
  async function clientRequest(host: RuntimeHost, method: string, input: Mutation = {}): Promise<unknown> {
    switch (method.trim()) {
      case "automation.list": return locked(() => ({ tasks: snapshotTasks(), workspace: { id: workspaceID, root: workspaceRoot } }));
      case "automation.run.list": return locked(() => ({ runs: structuredClone(runs) }));
      case "automation.create": return add(host, mutationInput(input));
      case "automation.update": return update(host, mutationInput(input));
      case "automation.remove": await remove(host, mutationInput(input).id); return { ok: true };
      default: throw new Error(`unknown client method ${JSON.stringify(method)}`);
    }
  }
  async function consume(task: Task, now: Date): Promise<void> {
    await locked(async () => {
      const existing = tasks.get(task.id);
      if (!existing || existing.paused || Date.parse(existing.next_run_at) !== Date.parse(task.next_run_at)) return;
      if (existing.recurring) {
        const next = { ...existing };
        try { next.next_run_at = parseCronExpression(existing.cron).nextRun(now, existing.timezone).toISOString(); }
        catch { next.paused = true; }
        tasks.set(task.id, next);
      } else tasks.delete(task.id);
      await saveBestEffort(host);
    });
  }
  async function fire(task: Task, now: Date): Promise<boolean> {
    const scheduledAt = !task.next_run_at || task.next_run_at === zeroTime ? now.getTime() : Date.parse(task.next_run_at);
    const runID = `run-${task.id}-${Math.floor(scheduledAt / 1000)}`, requestID = `automation-${runID}`;
    const settled = await locked(async () => {
      const existing = runs.find(run => run.id === runID);
      if (existing && terminal.has(existing.status)) return true;
      if (!existing) {
        runs.push({ id: runID, task_id: task.id, task: structuredClone(task), request_id: requestID, status: "starting", triggered_at: now.toISOString() });
        if (runs.length > 500) runs = runs.slice(-500);
      }
      await saveBestEffort(host);
      return false;
    });
    if (settled) return true;
    let sessionID = task.heartbeat_thread_id || "", executionRoot = "", error: unknown;
    const workspaceMode = task.workspace_mode || "shared";
    let sent: Awaited<ReturnType<typeof send>> | undefined;
    async function send() {
      return host.call("host.session.send", {
        request_id: requestID, session_id: sessionID,
        input: { prompt: task.prompt, context_blocks: [{ kind: "automation_trigger", source: "automation", content: `task_id=${task.id}\nrun_id=${runID}\nworkspace_id=${task.workspace_id || ""}\nworkspace_root=${task.workspace_root}\nworkspace_mode=${workspaceMode}\nexecution_root=${executionRoot}\nschedule=${task.cron}\ntimezone=${task.timezone}\ntriggered_at=${now.toISOString().replace(/\.\d{3}Z$/, "Z")}` }] },
        presentation: { kind: "query_bubble", text: "自动化任务已唤醒 Agent", name: task.title }, cause: "automation.trigger",
      }, { signal: abort.signal });
    }
    try {
      if (task.mode === "new_thread") {
        if ((task.workspace_id || "") !== workspaceID || cleanPath(task.workspace_root) !== cleanPath(workspaceRoot)) throw new Error("automation target workspace does not match the active plugin workspace");
        const created = await host.call("host.session.create", { request_id: `create-${runID}`, name: task.title, visibility: "user", context_source: "fresh", workspace: workspaceMode as "shared" | "worktree", workspace_id: task.workspace_id, workspace_root: task.workspace_root }, { signal: abort.signal });
        sessionID = created.session_id;
        executionRoot = created.workspace_root || "";
      }
      sent = await send();
    } catch (cause) { error = cause; }
    return locked(async () => {
      if (stopped) return false;
      const index = runs.findIndex(run => run.id === runID), run = runs[index];
      if (!run) return true;
      if (terminal.has(run.status)) return true;
      const message = error instanceof Error ? error.message : String(error);
      if (error && message.includes("session service is unavailable")) {
        if (run.status !== "starting") return true;
        runs.splice(index, 1);
        await saveBestEffort(host);
        return false;
      }
      run.session_id = sessionID || undefined;
      run.workspace_root = executionRoot || undefined;
      if (error) {
        run.status = "failed"; run.completed_at = new Date().toISOString(); run.error = message;
      } else if (run.status === "running" && sent!.state !== "running") {
        run.queue_id ||= sent!.queue_id;
      } else {
        run.status = sent!.state; run.turn_id = sent!.turn_id; run.queue_id = sent!.queue_id;
      }
      await saveBestEffort(host);
      return true;
    });
  }
  function fireDue(): void {
    dispatch = dispatch.then(async () => {
      if (stopped) return;
      const now = new Date();
      const due = await locked(() => [...tasks.values()].filter(task => !task.paused && Date.parse(task.next_run_at || zeroTime) <= now.getTime()).map(task => ({ ...task })));
      for (const task of due) {
        if (stopped) break;
        if (await fire(task, now) && !stopped) await consume(task, now);
      }
    }).catch(error => { console.error("automation dispatch failed:", error); });
  }
  async function settle(host: RuntimeHost, input: TurnLifecycleInput): Promise<void> {
    if (input.state !== "running" && !terminal.has(input.state)) return;
    await locked(async () => {
      const run = runs.find(value => value.request_id === input.request_id);
      if (!run) return;
      if (input.state === "running") {
        if (terminal.has(run.status)) return;
        run.status = "running";
        if (input.thread_id) run.session_id = input.thread_id;
        if (input.turn_id) run.turn_id = input.turn_id;
        if (input.queue_id) run.queue_id = input.queue_id;
      } else {
        run.status = input.state; run.completed_at = new Date().toISOString();
        run.session_id = input.thread_id || undefined; run.turn_id = input.turn_id || undefined; run.queue_id = input.queue_id || undefined; run.error = input.error || undefined;
      }
      await save(host);
    });
  }
  return {
    async initialize(params: RuntimeInitializeParams, runtimeHost: RuntimeHost) {
      host = runtimeHost; workspaceID = params.workspace_id?.trim() || ""; workspaceRoot = params.project_root.trim();
      tasks = new Map(); runs = []; stopped = false;
      const result = await host.call("host.storage.get", { scope: "workspace", key: storageKey });
      if (result.value != null) {
        try {
          const state = object(JSON.parse(result.value));
          if (state.tasks != null && !Array.isArray(state.tasks)) throw new Error("tasks must be an array");
          if (state.runs != null && !Array.isArray(state.runs)) throw new Error("runs must be an array");
          for (const value of (state.tasks || []) as unknown[]) {
            const task = restoreTask(value);
            if (task.durable) tasks.set(task.id, { ...task, workspace_id: workspaceID || undefined, workspace_root: workspaceRoot });
          }
          runs = ((state.runs || []) as unknown[]).map(restoreRun);
        } catch (error) { throw new Error(`decode automation plugin state: ${String(error)}`); }
      }
      return {
        tools: [{ id: "cron", description: "Manage scheduled Agent prompts. action=list returns tasks; action=add creates a one-shot or recurring task from a five-field cron expression; action=remove deletes a task. Use new_thread for an independent visible conversation and thread_heartbeat to wake an existing conversation. Set workspace=worktree so each triggered session runs in its own git worktree instead of the shared project directory.", input_schema: cronSchema, execution_scopes: ["root" as const] }],
        capabilities: [{ id: "plugin.client.request", kind: "decision" as const, version: 1 }, { id: "agent.turn.lifecycle", kind: "observe" as const, version: 1, error_policy: "isolate" as const }],
        required_services: (["host.session.create", "host.session.send", "host.storage.get", "host.storage.set"] as const).map(requireKernelService),
      };
    },
    activate(_runtimeHost, lifecycle) {
      if (lifecycle.signal.aborted) { stop(); return; }
      lifecycle.signal.addEventListener("abort", stop, { once: true });
      fireDue(); timer = setInterval(fireDue, 15_000);
    },
    async shutdown() { stop(); await dispatch; await stateQueue; },
    async executeTool(call: ToolExecuteParams, host: RuntimeHost) {
      const input = mutationInput(call.arguments);
      let value: unknown;
      switch (input.action?.trim()) {
        case "list": value = await locked(snapshotTasks); break;
        case "add": input.heartbeat_thread_id ||= call.session_id || call.thread_id; value = { action: "cron", task: await add(host, input) }; break;
        case "remove": await remove(host, input.id); value = { action: "cron", removed: input.id?.trim() }; break;
        default: throw new Error("cron requires action=list, add, or remove");
      }
      return { result: { content: [{ type: "text" as const, text: JSON.stringify(value) }] } };
    },
    async invokeCapability(call, host) {
      if (call.capability === "plugin.client.request") {
        const request = call.input as { method: string; input?: Mutation };
        return { output: { result: await clientRequest(host, request.method, request.input) } };
      }
      if (call.capability === "agent.turn.lifecycle") { await settle(host, call.input as TurnLifecycleInput); return { output: {} }; }
      throw new Error(`unknown capability ${JSON.stringify(call.capability)}`);
    },
  };
}

export default createAutomationPlugin();
