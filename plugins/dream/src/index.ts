import { randomBytes } from "node:crypto";
import { requireKernelService, type RuntimeHost, type RuntimePlugin, type TurnLifecycleInput, type AgentTurnCompletedInput } from "@wuu/plugin-sdk";

const storageKey = "dream.state";
const zeroTime = "0001-01-01T00:00:00Z";
interface Settings { enabled: boolean; interval_days: number; min_sessions: number; model_alias?: string }
interface State {
  settings: Settings; candidates: Record<string, string>;
  last_run_at: string; last_started_at: string; last_finished_at: string;
  last_status?: string; last_error?: string; active_request_id?: string; active_session_id?: string;
}
function initialState(): State {
  return { settings: { enabled: false, interval_days: 7, min_sessions: 5 }, candidates: {}, last_run_at: zeroTime, last_started_at: zeroTime, last_finished_at: zeroTime };
}
function latestCandidate(candidates: Record<string, string>): string {
  let latestID = "", latestAt = "";
  for (const [id, at] of Object.entries(candidates)) if (at > latestAt || (at === latestAt && id > latestID)) { latestID = id; latestAt = at; }
  return latestID;
}
function dreamPrompt(projectMemory: string): string {
  let prompt = "Review the forked completed conversation and consolidate only durable workspace knowledge.";
  prompt += projectMemory.trim()
    ? "\n\nCurrent project_memory, read when this run started; consolidate against it:\n\n" + projectMemory.trim()
    : "\n\nUse session_memory to read project_memory before editing it.";
  return prompt + " Append or replace project_memory via the session_memory tool only for stable architecture decisions, conventions, tool quirks, or recurring workflow lessons that should survive future sessions. Never store secrets, raw transcripts, temporary progress, PR numbers, commit SHAs, or facts likely to go stale within a week. Do not modify source files. If nothing deserves durable memory, reply exactly: Nothing to dream.";
}
function errorText(error: unknown): string { return error instanceof Error ? error.message : String(error); }

export function createDreamPlugin(): RuntimePlugin {
  let host: RuntimeHost, state = initialState(), running = false, stopped = false;
  let timer: ReturnType<typeof setInterval> | undefined;
  let saves: Promise<unknown> = Promise.resolve();
  const work = new Set<Promise<void>>();
  const abort = new AbortController();
  function stop(): void { stopped = true; clearInterval(timer); abort.abort(); }
  function save(host: RuntimeHost): Promise<void> {
    const value = JSON.stringify(state);
    const next = saves.then(async () => { await host.call("host.storage.set", { scope: "workspace", key: storageKey, value }, { signal: abort.signal }); });
    saves = next.catch(() => {});
    return next;
  }
  async function saveBestEffort(host: RuntimeHost): Promise<void> { try { await save(host); } catch (error) { console.error("dream state save failed:", error); } }
  function snapshot(): State { return structuredClone(state); }
  async function fail(error: unknown): Promise<void> {
    if (stopped) return;
    running = false; state.last_status = "failed"; state.last_finished_at = new Date().toISOString(); state.last_error = errorText(error);
    delete state.active_request_id; delete state.active_session_id;
    await saveBestEffort(host);
  }
  async function launch(parentID: string, modelAlias: string | undefined, projectMemory: string): Promise<void> {
    const id = `dream-${randomBytes(10).toString("hex")}`, requestID = `${id}:turn`;
    try {
      // Publish the identity before dispatch so an immediate terminal lifecycle
      // callback cannot arrive before this generation knows which run it owns.
      state.active_request_id = requestID;
      await save(host);
      if (stopped) return;
      const created = await host.call("host.session.create", {
        request_id: `${id}:session`, name: "Dream", visibility: "plugin", parent_session_id: parentID,
        context_source: "fork", workspace: "shared", model_alias: modelAlias,
      }, { signal: abort.signal });
      if (stopped) return;
      state.active_session_id = created.session_id;
      await save(host);
      if (stopped) return;
      await host.call("host.session.send", { request_id: requestID, session_id: created.session_id, input: { prompt: dreamPrompt(projectMemory) }, cause: "dream.consolidate" }, { signal: abort.signal });
    } catch (error) { if (state.active_request_id === requestID) await fail(error); }
  }
  function startLaunch(parentID: string, modelAlias: string | undefined, memory: string): void {
    const pending = launch(parentID, modelAlias, memory);
    work.add(pending);
    void pending.finally(() => work.delete(pending));
  }
  async function startDream(host: RuntimeHost, force = false): Promise<void> {
    const now = new Date(), settings = state.settings;
    if (stopped || running || !settings.enabled || !Object.keys(state.candidates).length || (!force && Object.keys(state.candidates).length < settings.min_sessions)) return;
    if (!force && state.last_run_at !== zeroTime && now.getTime() - Date.parse(state.last_run_at) < settings.interval_days * 86_400_000) return;
    if (!force && state.last_status === "failed" && state.last_finished_at !== zeroTime && now.getTime() - Date.parse(state.last_finished_at) < 3_600_000) return;
    running = true;
    const parentID = latestCandidate(state.candidates), modelAlias = settings.model_alias;
    let projectMemory: string;
    try {
      // Resolve the versioned service freshly for each run; provider reloads
      // must never leave Dream consolidating against cached workspace memory.
      const result = await host.call("host.service.call", { service: "memory.session", method: "read", params: { target: "project_memory" } }, { signal: abort.signal }) as { exists: boolean; content: string };
      projectMemory = (result.content || "").trim();
    } catch (error) {
      if (stopped) return;
      running = false; state.last_status = "skipped"; state.last_finished_at = new Date().toISOString(); state.last_error = errorText(error);
      await saveBestEffort(host);
      return;
    }
    if (stopped) return;
    state.last_status = "running"; state.last_started_at = now.toISOString(); state.last_finished_at = zeroTime; delete state.last_error;
    startLaunch(parentID, modelAlias, projectMemory);
  }
  function tick(): void {
    const pending = startDream(host).catch(error => fail(error));
    work.add(pending);
    void pending.finally(() => work.delete(pending));
  }
  async function settle(host: RuntimeHost, input: TurnLifecycleInput): Promise<void> {
    if (!["completed", "failed", "interrupted", "discarded"].includes(input.state) || input.request_id !== state.active_request_id) return;
    running = false; state.last_finished_at = new Date().toISOString(); delete state.active_request_id; delete state.active_session_id;
    if (input.state === "completed") {
      state.last_status = "completed"; state.last_run_at = state.last_finished_at; delete state.last_error; state.candidates = {};
    } else { state.last_status = "failed"; state.last_error = input.error?.trim() || input.state; }
    await save(host);
  }
  async function clientRequest(host: RuntimeHost, method: string, input: unknown): Promise<unknown> {
    switch (method.trim()) {
      case "dream.get": return snapshot();
      case "dream.update": {
        const next = input as Settings;
        if (!next || !Number.isInteger(next.interval_days) || next.interval_days < 1 || next.interval_days > 365 || !Number.isInteger(next.min_sessions) || next.min_sessions < 1 || next.min_sessions > 100) throw new Error("Dream interval must be 1-365 days and minimum sessions must be 1-100");
        if (next.enabled != null && typeof next.enabled !== "boolean") throw new Error("Dream enabled must be a boolean");
        if (next.model_alias != null && typeof next.model_alias !== "string") throw new Error("Dream model_alias must be a string");
        state.settings = { enabled: next.enabled || false, interval_days: next.interval_days, min_sessions: next.min_sessions, model_alias: next.model_alias?.trim() || undefined };
        await save(host); await startDream(host); return snapshot();
      }
      case "dream.run": await startDream(host, true); return snapshot();
      default: throw new Error(`unknown dream client method ${JSON.stringify(method)}`);
    }
  }
  return {
    async initialize(_params, runtimeHost) {
      host = runtimeHost; state = initialState(); running = false; stopped = false;
      const result = await host.call("host.storage.get", { scope: "workspace", key: storageKey });
      if (result.value !== null) {
        let loaded: Partial<State>;
        try {
          loaded = JSON.parse(result.value) ?? {};
          if (typeof loaded !== "object" || Array.isArray(loaded)) throw new Error("expected a Dream state object");
          if (loaded.settings != null && (typeof loaded.settings !== "object" || Array.isArray(loaded.settings))) throw new Error("expected Dream settings object");
          if (loaded.settings?.enabled != null && typeof loaded.settings.enabled !== "boolean") throw new Error("invalid Dream enabled setting");
          for (const key of ["interval_days", "min_sessions"] as const) if (loaded.settings?.[key] != null && !Number.isInteger(loaded.settings[key])) throw new Error(`invalid Dream setting ${key}`);
          if (loaded.settings?.model_alias != null && typeof loaded.settings.model_alias !== "string") throw new Error("invalid Dream model alias");
          if (loaded.candidates != null && (typeof loaded.candidates !== "object" || Array.isArray(loaded.candidates) || Object.values(loaded.candidates).some(value => value != null && typeof value !== "string"))) throw new Error("expected Dream candidates object");
          if (loaded.candidates) loaded.candidates = Object.fromEntries(Object.entries(loaded.candidates).map(([id, at]) => [id, at || ""]));
          for (const key of ["last_status", "last_error", "active_request_id", "active_session_id"] as const) if (loaded[key] != null && typeof loaded[key] !== "string") throw new Error(`invalid Dream field ${key}`);
          for (const key of ["last_run_at", "last_started_at", "last_finished_at"] as const) if (loaded[key] != null && (typeof loaded[key] !== "string" || !Number.isFinite(Date.parse(loaded[key])))) throw new Error(`invalid Dream timestamp ${key}`);
        } catch (error) { throw new Error(`decode dream plugin state: ${String(error)}`); }
        state = {
          settings: { enabled: loaded.settings?.enabled ?? false, interval_days: loaded.settings?.interval_days ?? 7, min_sessions: loaded.settings?.min_sessions ?? 5, model_alias: loaded.settings?.model_alias || undefined },
          candidates: loaded.candidates || {}, last_run_at: loaded.last_run_at ?? zeroTime, last_started_at: loaded.last_started_at ?? zeroTime, last_finished_at: loaded.last_finished_at ?? zeroTime,
          last_status: loaded.last_status || undefined, last_error: loaded.last_error || undefined,
          active_request_id: loaded.active_request_id || undefined, active_session_id: loaded.active_session_id || undefined,
        };
        if (state.settings.interval_days <= 0) state.settings.interval_days = 7;
        if (state.settings.min_sessions <= 0) state.settings.min_sessions = 5;
        if (state.last_status === "running") {
          state.last_status = "failed"; state.last_error = "interrupted: plugin generation stopped while Dream was running"; state.last_finished_at = new Date().toISOString();
          delete state.active_request_id; delete state.active_session_id;
        }
      }
      return {
        capabilities: [...["agent.turn.completed", "agent.turn.lifecycle"].map(id => ({ id, kind: "observe" as const, version: 1, error_policy: "isolate" as const })), { id: "plugin.client.request", kind: "decision" as const, version: 1 }],
        required_services: [
          ...(["host.session.create", "host.session.send", "host.storage.get", "host.storage.set"] as const).map(requireKernelService),
          { name: "memory.session", major_version: 1 },
        ],
      };
    },
    async activate(_runtimeHost, lifecycle) {
      if (lifecycle.signal.aborted) { stop(); return; }
      lifecycle.signal.addEventListener("abort", stop, { once: true });
      await save(host);
      if (!stopped) timer = setInterval(tick, 15_000);
    },
    async shutdown() { stop(); await Promise.all([...work]); await saves; },
    async invokeCapability(call, host) {
      switch (call.capability) {
        case "agent.turn.completed": {
          const input = call.input as AgentTurnCompletedInput;
          if (input.succeeded && input.thread_id?.trim()) {
            const timestamp = input.completed_at && input.completed_at !== zeroTime ? input.completed_at : new Date().toISOString();
            if (!Number.isFinite(Date.parse(timestamp))) throw new Error(`invalid completion timestamp ${JSON.stringify(timestamp)}`);
            // Canonical UTC timestamps preserve the lexicographic parent-order
            // contract, including existing Go RFC3339 nanosecond timestamps.
            const fractional = timestamp.match(/\.(\d+)(?:Z|[+-]\d\d:\d\d)$/)?.[1];
            const utc = new Date(timestamp).toISOString().replace(/\.\d{3}Z$/, fractional ? `.${fractional.replace(/0+$/, "")}Z` : "Z").replace(".Z", "Z");
            state.candidates[input.thread_id] = utc;
            await save(host); await startDream(host);
          }
          return { output: {} };
        }
        case "agent.turn.lifecycle": await settle(host, call.input as TurnLifecycleInput); return { output: {} };
        case "plugin.client.request": {
          const request = call.input as { method: string; input?: unknown };
          return { output: { result: await clientRequest(host, request.method, request.input) } };
        }
        default: throw new Error(`unknown dream capability ${JSON.stringify(call.capability)}`);
      }
    },
  };
}

export default createDreamPlugin();
